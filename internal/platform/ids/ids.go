// Package ids valida los identificadores antes de que lleguen a la base de
// datos, para que uno mal formado se responda con 400 y no con un error de la
// base.
package ids

import (
	"regexp"
	"strings"
)

var uuidPattern = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
)

// Normalize pasa value a minúsculas, le quita los espacios e indica si es un
// UUID en forma canónica.
func Normalize(value string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	return normalized, uuidPattern.MatchString(normalized)
}

// IsUUID indica si value es un UUID en forma canónica.
func IsUUID(value string) bool {
	_, ok := Normalize(value)
	return ok
}

// NormalizeAll normaliza todos los valores, quita los repetidos e indica si
// todos son UUID. El resultado nunca es nil.
func NormalizeAll(values []string) ([]string, bool) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))

	for _, value := range values {
		normalized, ok := Normalize(value)
		if !ok {
			return nil, false
		}

		if _, duplicated := seen[normalized]; duplicated {
			continue
		}

		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}

	return result, true
}
