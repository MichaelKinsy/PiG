package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// completeThemeJSON returns a theme document whose colors object starts with
// colorsFragment (a comma-separated list of members, kept in its position) and
// then fills every other required token, so it passes ValidateThemeJSON the way
// upstream tests clone the built-in dark.json.
func completeThemeJSON(t *testing.T, name, colorsFragment string) string {
	t.Helper()
	given := map[string]any{}
	if err := json.Unmarshal([]byte("{"+colorsFragment+"}"), &given); err != nil {
		t.Fatalf("colors fragment %q: %v", colorsFragment, err)
	}
	var members []string
	if strings.TrimSpace(colorsFragment) != "" {
		members = append(members, colorsFragment)
	}
	for _, token := range themeColorTokens {
		if _, present := given[token.name]; !present && !token.optional {
			members = append(members, fmt.Sprintf("%q:%q", token.name, "#808080"))
		}
	}
	return fmt.Sprintf(`{"name":%q,"colors":{%s}}`, name, strings.Join(members, ","))
}

// TestValidateThemeJSONMatchesUpstream replays testdata/theme_json_cases.json.
// Each case's expected message was produced by upstream theme-json.ts
// validateThemeJson (Pi 0.87.1 source, run against the TypeBox 1.3.x build
// shipped with Pi) on the same JSON text, including TypeBox's error order and
// eight-error cap, JavaScript property order for vars, and duplicate keys.
func TestValidateThemeJSONMatchesUpstream(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "theme_json_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Label string  `json:"label"`
		Input string  `json:"input"`
		Error *string `json:"error"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 40 {
		t.Fatalf("only %d cases", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.Label, func(t *testing.T) {
			got := ValidateThemeJSON(tc.Label, []byte(tc.Input))
			switch {
			case tc.Error == nil && got != nil:
				t.Fatalf("unexpected error:\n%v", got)
			case tc.Error != nil && got == nil:
				t.Fatalf("no error, want:\n%s", *tc.Error)
			case tc.Error != nil && got.Error() != *tc.Error:
				t.Fatalf("error mismatch\n got: %q\nwant: %q", got.Error(), *tc.Error)
			}
		})
	}
}

// Upstream loadThemeFromPath validates with the file path as the label.
func TestLoadThemeFileRejectsSchemaInvalidTheme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "partial.json")
	if err := os.WriteFile(path, []byte(`{"name":"partial","colors":{"accent":"#ffffff"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadThemeFile(path)
	if err == nil {
		t.Fatal("partial theme loaded")
	}
	if want := "Invalid theme \"" + path + "\":\n\nMissing required color tokens:\n"; !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("error = %q, want prefix %q", err.Error(), want)
	}
}

func TestLoadThemeFileRejectsSlashInName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "slash.json")
	if err := os.WriteFile(path, []byte(completeThemeJSON(t, "light/dark", "")), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadThemeFile(path)
	want := `Invalid theme name "light/dark": theme names cannot contain "/" because it is reserved for automatic light/dark theme settings.`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

// Upstream parseThemeJsonContent parses JSON.parse(stripBom(content)).
func TestLoadThemeFileStripsBOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bom.json")
	if err := os.WriteFile(path, []byte("\xef\xbb\xbf"+completeThemeJSON(t, "bom", "")), 0o644); err != nil {
		t.Fatal(err)
	}
	theme, err := LoadThemeFile(path)
	if err != nil {
		t.Fatalf("LoadThemeFile: %v", err)
	}
	if theme.Name != "bom" {
		t.Fatalf("name = %q", theme.Name)
	}
}

func TestBuiltinThemesPassValidation(t *testing.T) {
	for _, name := range []string{"theme_dark.json", "theme_light.json"} {
		data, err := builtinThemes.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateThemeJSON(name, data); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
