package app

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunDoesNotRequireExternalServices(t *testing.T) {
	var output bytes.Buffer

	err := Run(context.Background(), Options{Output: &output})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(output.String(), "scaffold inicializado") {
		t.Fatalf("expected scaffold message, got %q", output.String())
	}
}

func TestRunReturnsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Run(ctx, Options{})
	if err == nil {
		t.Fatal("expected canceled context error")
	}
}
