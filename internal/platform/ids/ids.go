// Package ids validates identifiers before they reach the database, so a
// malformed identifier is answered with 400 instead of a database error.
package ids

import (
	"regexp"
	"strings"
)

var uuidPattern = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
)

// Normalize lower-cases and trims value and reports whether it is a UUID in
// canonical form.
func Normalize(value string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	return normalized, uuidPattern.MatchString(normalized)
}

// IsUUID reports whether value is a UUID in canonical form.
func IsUUID(value string) bool {
	_, ok := Normalize(value)
	return ok
}

// NormalizeAll normalizes every value, removes duplicates and reports whether
// all of them are UUIDs. The result is never nil.
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
