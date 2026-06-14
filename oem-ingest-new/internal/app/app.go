package app

import (
	"context"
	"fmt"
	"io"
)

// Options holds process-level dependencies for the application entry point.
type Options struct {
	Output io.Writer
}

// Run is the application entry point. Collection wiring will be added by later
// tasks; for now it exits successfully without contacting OEM or OTLP services.
func Run(ctx context.Context, opts Options) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if opts.Output != nil {
		_, err := fmt.Fprintln(opts.Output, "oem-ingest: scaffold inicializado; coleta ainda nao implementada")
		return err
	}
	return nil
}
