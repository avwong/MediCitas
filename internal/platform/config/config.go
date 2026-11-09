// Package config reads service configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strings"
)

// Loader reads environment variables and collects every missing required
// variable, so a misconfigured service reports all problems at once instead
// of failing on the first one.
type Loader struct {
	missing []string
}

// Required returns the trimmed value of name and records it as missing when
// it is empty.
func (l *Loader) Required(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		l.missing = append(l.missing, name)
	}

	return value
}

// Optional returns the trimmed value of name, or fallback when it is empty.
func (l *Loader) Optional(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}

	return value
}

// Err reports every required variable that was missing.
func (l *Loader) Err() error {
	if len(l.missing) == 0 {
		return nil
	}

	return fmt.Errorf(
		"missing required environment variables: %s",
		strings.Join(l.missing, ", "),
	)
}
