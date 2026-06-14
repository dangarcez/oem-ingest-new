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
	Output              io.Writer
	LookupEnv           func(string) (string, bool)
	Logger              validate.Logger
	TargetListerFactory validate.TargetListerFactory
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
		result, err := validateStartupTargetIDs(ctx, env, opts)
		if err != nil {
			return err
		}
		if opts.Output != nil {
			_, err := fmt.Fprintf(
				opts.Output,
				"validacao de IDs concluida: %d correcoes, %d avisos\n",
				len(result.IDCorrections),
				len(result.Warnings),
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

func validateStartupTargetIDs(ctx context.Context, env config.Env, opts Options) (validate.IDValidationResult, error) {
	sites, err := config.LoadTargets(env.TargetsPath)
	if err != nil {
		return validate.IDValidationResult{}, err
	}

	factory := opts.TargetListerFactory
	if factory == nil {
		factory, err = targetListerFactory(env)
		if err != nil {
			return validate.IDValidationResult{}, err
		}
	}

	return validate.ValidateTargetIDs(ctx, sites, factory, validate.IDValidationOptions{
		Enabled: true,
		Logger:  opts.Logger,
	})
}

func targetListerFactory(env config.Env) (validate.TargetListerFactory, error) {
	credentials, err := auth.Resolve(auth.Options{
		User:          env.User,
		Password:      env.Password,
		Token:         env.Token,
		TokenHashFile: env.AuthTokenHashFile,
	})
	if err != nil {
		return nil, err
	}

	return func(site config.SiteConfig) (validate.TargetLister, error) {
		return oem.New(oem.Options{
			Endpoint:       site.Endpoint,
			Credentials:    credentials,
			Timeout:        env.HTTPTimeout,
			ConnectTimeout: env.HTTPConnectTimeout,
			MaxRetries:     env.HTTPMaxRetries,
		})
	}, nil
}

func lookupEnv(lookup func(string) (string, bool)) func(string) (string, bool) {
	if lookup != nil {
		return lookup
	}
	return os.LookupEnv
}
