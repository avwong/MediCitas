// Package logging construye el logger estructurado que comparten todos los
// servicios.
package logging

import (
	"log/slog"
	"os"
	"strings"
)

// New devuelve un logger en JSON que marca cada registro con el nombre del
// servicio. LOG_LEVEL=debug activa los registros de depuración.
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
