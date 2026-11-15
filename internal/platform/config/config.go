// Package config lee la configuración del servicio desde variables de entorno.
package config

import (
	"fmt"
	"os"
	"strings"
)

// Loader lee variables de entorno y recuerda todas las obligatorias que
// falten, para que un servicio mal configurado reporte todos los problemas de
// una vez en lugar de fallar con el primero.
type Loader struct {
	missing []string
}

// Required devuelve el valor de name sin espacios y lo anota como faltante si
// está vacío.
func (l *Loader) Required(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		l.missing = append(l.missing, name)
	}

	return value
}

// Optional devuelve el valor de name sin espacios, o fallback si está vacío.
func (l *Loader) Optional(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}

	return value
}

// Err reporta todas las variables obligatorias que faltaron.
func (l *Loader) Err() error {
	if len(l.missing) == 0 {
		return nil
	}

	return fmt.Errorf(
		"missing required environment variables: %s",
		strings.Join(l.missing, ", "),
	)
}
