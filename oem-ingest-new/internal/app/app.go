package app

import (
	"context"
	"fmt"
	"io"
	"os"

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

	return startupValidationResult{IDs: idResult, Correlation: correlationResult}, nil
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
