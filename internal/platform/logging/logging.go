// Package logging builds the structured logger shared by every service.
package logging

import (
	"log/slog"
	"os"
	"strings"
)

// New returns a JSON logger that tags every record with the service name.
// LOG_LEVEL=debug enables debug records.
func New(service string) *slog.Logger {
	level := slog.LevelInfo
	if strings.EqualFold(os.Getenv("LOG_LEVEL"), "debug") {
		level = slog.LevelDebug
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})

	return slog.New(handler).With("service", service)
}
