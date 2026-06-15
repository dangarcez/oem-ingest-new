package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevelAcceptsSupportedValues(t *testing.T) {
	tests := map[string]slog.Level{
		"":        slog.LevelInfo,
		"debug":   slog.LevelDebug,
		"INFO":    slog.LevelInfo,
		"WARN":    slog.LevelWarn,
		"WARNING": slog.LevelWarn,
		"error":   slog.LevelError,
	}

	for value, want := range tests {
		got, err := ParseLevel(value)
		if err != nil {
			t.Fatalf("ParseLevel(%q) returned error: %v", value, err)
		}
		if got != want {
			t.Fatalf("ParseLevel(%q) = %s, want %s", value, got, want)
		}
	}
}

func TestParseLevelRejectsUnsupportedValue(t *testing.T) {
	_, err := ParseLevel("verbose")
	if err == nil || !strings.Contains(err.Error(), "OEM_LOG_LEVEL") {
		t.Fatalf("expected OEM_LOG_LEVEL error, got %v", err)
	}
}

func TestNewTextLoggerFiltersBelowConfiguredLevel(t *testing.T) {
	var output bytes.Buffer
	logger, err := NewTextLogger(&output, "WARN")
	if err != nil {
		t.Fatalf("NewTextLogger returned error: %v", err)
	}

	ctx := context.Background()
	logger.InfoContext(ctx, "info oculto")
	logger.WarnContext(ctx, "warn visivel")

	got := output.String()
	if strings.Contains(got, "info oculto") {
		t.Fatalf("info log should have been filtered, got %q", got)
	}
	if !strings.Contains(got, "warn visivel") {
		t.Fatalf("warn log should have been emitted, got %q", got)
	}
}
