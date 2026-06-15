package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunHelpDoesNotStartCollector(t *testing.T) {
	t.Setenv("OEM_VALIDATE_CONFIG", "false")
	t.Setenv("OEM_LOG_LEVEL", "")
	var stdout, stderr bytes.Buffer

	err := run(context.Background(), []string{"--help"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Uso: oem-ingest") {
		t.Fatalf("expected help output, got %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "scaffold inicializado") {
		t.Fatalf("help should not execute the application, got %q", stderr.String())
	}
}

func TestRunVersion(t *testing.T) {
	t.Setenv("OEM_VALIDATE_CONFIG", "false")
	t.Setenv("OEM_LOG_LEVEL", "")
	var stdout, stderr bytes.Buffer

	err := run(context.Background(), []string{"--version"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != version {
		t.Fatalf("expected version %q, got %q", version, stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRunInvalidFlagReturnsError(t *testing.T) {
	t.Setenv("OEM_VALIDATE_CONFIG", "false")
	t.Setenv("OEM_LOG_LEVEL", "")
	var stdout, stderr bytes.Buffer

	err := run(context.Background(), []string{"--unknown"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected invalid flag error")
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("expected flag parser error, got %q", stderr.String())
	}
	if strings.Contains(stdout.String(), "scaffold inicializado") ||
		strings.Contains(stderr.String(), "scaffold inicializado") {
		t.Fatalf("invalid flags should not execute the application; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunInvalidLogLevelReturnsError(t *testing.T) {
	t.Setenv("OEM_VALIDATE_CONFIG", "false")
	t.Setenv("OEM_LOG_LEVEL", "verbose")
	var stdout, stderr bytes.Buffer

	err := run(context.Background(), nil, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "OEM_LOG_LEVEL") {
		t.Fatalf("expected OEM_LOG_LEVEL error, got %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("expected no output before app starts; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}
