package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// NewTextLogger creates the process logger with the configured minimum level.
func NewTextLogger(w io.Writer, levelName string) (*slog.Logger, error) {
	level, err := ParseLevel(levelName)
	if err != nil {
		return nil, err
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})), nil
}

// ParseLevel converts OEM_LOG_LEVEL values to slog levels.
func ParseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("OEM_LOG_LEVEL: use debug, info, warn ou error")
	}
}
