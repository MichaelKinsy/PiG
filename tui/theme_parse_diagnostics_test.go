package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// Pi 0.87.1 theme.ts:496-503 interpolates the JSON.parse SyntaxError, including its name, after stripping a BOM.
func TestLoadThemeFileParseDiagnosticsMatchPi(t *testing.T) {
	for _, tc := range []struct {
		name, input, message string
	}{
		{"empty", "", "Unexpected end of JSON input"},
		{"object", "{", "Expected property name or '}' in JSON at position 1 (line 1 column 2)"},
		{"bom", "\ufeff{", "Expected property name or '}' in JSON at position 1 (line 1 column 2)"},
		{"trailing", "{} x", "Unexpected non-whitespace character after JSON at position 3 (line 1 column 4)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "theme.json")
			if err := os.WriteFile(path, []byte(tc.input), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadThemeFile(path)
			want := "Failed to parse theme " + path + ": SyntaxError: " + tc.message
			if err == nil || err.Error() != want {
				t.Fatalf("error = %v, want %q", err, want)
			}
		})
	}
}
