package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"oem-ingest-new/internal/auth"
	"oem-ingest-new/internal/config"
	"oem-ingest-new/internal/oem"
	"oem-ingest-new/internal/validate"
)

// Options holds process-level dependencies for the application entry point.
type Options struct {
	Output                 io.Writer
	LookupEnv              func(string) (string, bool)
	Logger                 validate.Logger
	TargetInventoryFactory validate.TargetInventoryFactory
}

type startupValidationResult struct {
	IDs         validate.IDValidationResult
	Correlation validate.CorrelationValidationResult
	OutputPath  string
}

// Run is the application entry point. Collection wiring will be added by later
// tasks; for now it only performs startup validation when explicitly enabled.
func Run(ctx context.Context, opts Options) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	env, err := config.ReadEnv(lookupEnv(opts.LookupEnv))
	if err != nil {
		return err
	}
	if env.ValidateConfig {
		result, err := validateStartupTargets(ctx, env, opts)
		if err != nil {
			return err
		}
		if opts.Output != nil {
			_, err := fmt.Fprintf(
				opts.Output,
				"validacao de configuracao concluida: %d correcoes de ID, %d targets adicionados, %d tags corrigidas, %d avisos\n",
				len(result.IDs.IDCorrections),
				len(result.Correlation.TargetAdds),
				len(result.Correlation.TagCorrections),
				len(result.IDs.Warnings)+len(result.Correlation.Warnings),
			)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(opts.Output, "configuracao validada escrita em %s\n", result.OutputPath); err != nil {
				return err
			}
		}
	}
	if opts.Output != nil {
		_, err := fmt.Fprintln(opts.Output, "oem-ingest: scaffold inicializado; coleta ainda nao implementada")
		return err
	}
	return nil
}

func validateStartupTargets(ctx context.Context, env config.Env, opts Options) (startupValidationResult, error) {
	sites, err := config.LoadTargets(env.TargetsPath)
	if err != nil {
		return startupValidationResult{}, err
	}

	factory := opts.TargetInventoryFactory
	if factory == nil {
		factory, err = targetInventoryFactory(env)
		if err != nil {
			return startupValidationResult{}, err
		}
	}

	idResult, err := validate.ValidateTargetIDs(ctx, sites, targetListerFactory(factory), validate.IDValidationOptions{
		Enabled: true,
		Logger:  opts.Logger,
	})
	if err != nil {
		return startupValidationResult{}, err
	}

	correlationResult, err := validate.ValidateTargetCorrelations(ctx, idResult.Sites, factory, validate.CorrelationValidationOptions{
		Enabled: true,
		Logger:  opts.Logger,
	})
	if err != nil {
		return startupValidationResult{}, err
	}

	if err := validateOutputPath(env.TargetsPath, env.ValidatedConfigOutput); err != nil {
		return startupValidationResult{}, err
	}
	if err := config.WriteTargets(env.ValidatedConfigOutput, correlationResult.Sites); err != nil {
		return startupValidationResult{}, err
	}
	logValidationSummary(ctx, opts.Logger, env.ValidatedConfigOutput, idResult, correlationResult)

	return startupValidationResult{IDs: idResult, Correlation: correlationResult, OutputPath: env.ValidatedConfigOutput}, nil
}

func targetInventoryFactory(env config.Env) (validate.TargetInventoryFactory, error) {
	credentials, err := auth.Resolve(auth.Options{
		User:          env.User,
		Password:      env.Password,
		Token:         env.Token,
		TokenHashFile: env.AuthTokenHashFile,
	})
	if err != nil {
		return nil, err
	}

	return func(site config.SiteConfig) (validate.TargetInventory, error) {
		return oem.New(oem.Options{
			Endpoint:       site.Endpoint,
			Credentials:    credentials,
			Timeout:        env.HTTPTimeout,
			ConnectTimeout: env.HTTPConnectTimeout,
			MaxRetries:     env.HTTPMaxRetries,
		})
	}, nil
}

func targetListerFactory(factory validate.TargetInventoryFactory) validate.TargetListerFactory {
	return func(site config.SiteConfig) (validate.TargetLister, error) {
		return factory(site)
	}
}

func lookupEnv(lookup func(string) (string, bool)) func(string) (string, bool) {
	if lookup != nil {
		return lookup
	}
	return os.LookupEnv
}

func validateOutputPath(targetsPath, outputPath string) error {
	targetsAbs, err := filepath.Abs(targetsPath)
	if err != nil {
		return fmt.Errorf("resolver OEM_CONFIG_TARGETS %q: %w", targetsPath, err)
	}
	outputAbs, err := filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("resolver OEM_VALIDATED_CONFIG_OUTPUT %q: %w", outputPath, err)
	}
	if filepath.Clean(targetsAbs) == filepath.Clean(outputAbs) {
		return fmt.Errorf("OEM_VALIDATED_CONFIG_OUTPUT %q deve ser diferente de OEM_CONFIG_TARGETS %q para preservar o arquivo original", outputPath, targetsPath)
	}
	return nil
}

type infoLogger interface {
	InfoContext(ctx context.Context, msg string, args ...any)
}

func logValidationSummary(ctx context.Context, logger validate.Logger, outputPath string, ids validate.IDValidationResult, correlation validate.CorrelationValidationResult) {
	if logger == nil {
		return
	}
	info, ok := logger.(infoLogger)
	if !ok {
		return
	}
	info.InfoContext(ctx, "configuracao validada escrita",
		"output", outputPath,
		"id_corrections", len(ids.IDCorrections),
		"targets_added", len(correlation.TargetAdds),
		"tag_corrections", len(correlation.TagCorrections),
		"warnings", len(ids.Warnings)+len(correlation.Warnings),
	)
}
