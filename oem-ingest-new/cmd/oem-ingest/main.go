package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"oem-ingest-new/internal/app"
	"oem-ingest-new/internal/logging"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("oem-ingest", flag.ContinueOnError)
	flags.SetOutput(stderr)

	showVersion := flags.Bool("version", false, "exibe a versao e encerra")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Uso: oem-ingest [opcoes]")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Coletor OEM em Go. Defina OTEL_EXPORT_URL para iniciar coleta e exportacao.")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Opcoes:")
		flags.PrintDefaults()
	}

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if *showVersion {
		fmt.Fprintln(stdout, version)
		return nil
	}

	logger, err := logging.NewTextLogger(stderr, os.Getenv("OEM_LOG_LEVEL"))
	if err != nil {
		return err
	}
	return app.Run(ctx, app.Options{Output: stdout, Logger: logger})
}
