package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"oem-ingest-new/internal/app"
)

var version = "dev"

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
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
		fmt.Fprintln(stderr, "Coletor OEM em Go. Este scaffold ainda nao inicia coleta real.")
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

	logger := slog.New(slog.NewTextHandler(stderr, nil))
	return app.Run(ctx, app.Options{Output: stdout, Logger: logger})
}
