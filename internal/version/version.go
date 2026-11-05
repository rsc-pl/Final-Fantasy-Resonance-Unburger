// Package version reads the single version number of a build.
//
// The number lives in version.txt at the project root, as MAJOR.MINOR.PATCH with no prefix, no spaces and no
// trailing content beyond one line ending. It is the only source of the version: the archives, the metadata and
// the artefact names all take it from here, so changing the file changes the next build and nothing else.
package version

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Marker is the literal token an approved translation carries where the version belongs.
const Marker = "@@UNBURGER_VERSION@@"

// FileName is the canonical file name.
const FileName = "version.txt"

var pattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// Validate checks a candidate version string.
func Validate(s string) error {
	if s == "" {
		return fmt.Errorf("version is empty")
	}
	if strings.HasPrefix(s, "v") || strings.HasPrefix(s, "V") {
		return fmt.Errorf("version %q carries a v prefix; the file holds the number only", s)
	}
	if strings.TrimSpace(s) != s {
		return fmt.Errorf("version %q carries surrounding whitespace", s)
	}
	if strings.ContainsAny(s, "\r\n") {
		return fmt.Errorf("version %q carries a line break inside the value", s)
	}
	if !pattern.MatchString(s) {
		return fmt.Errorf("version %q is not MAJOR.MINOR.PATCH", s)
	}
	return nil
}

// Load reads and validates the version file. A missing or malformed file is an error, never a default.
func Load(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	s := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if s == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	if strings.ContainsAny(s, "\r\n") {
		return "", fmt.Errorf("%s holds more than one line", path)
	}
	if err := Validate(s); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Replace substitutes every exact occurrence of the marker and reports how many were replaced. It does not look
// at anything else in the text: the marker is a literal token in the approved translation, not a pattern.
func Replace(text, v string) (string, int) {
	if !strings.Contains(text, Marker) {
		return text, 0
	}
	n := strings.Count(text, Marker)
	return strings.ReplaceAll(text, Marker, v), n
}

// Unresolved reports whether a composed text still carries the marker.
func Unresolved(text string) bool { return strings.Contains(text, Marker) }
