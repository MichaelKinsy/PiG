package tui

import (
	"encoding/json"
	"strings"
	"testing"
)

// Ports packages/coding-agent/test/config-schemas.test.ts (Pi 1.2.0) "generated configuration schemas" › "accepts $schema in themes and
// rejects malformed themes": the built-in dark theme with a $schema and another name validates, and a "/" in the name, missing color
// tokens, an unknown top-level member, a misspelled color token and an unknown export member are rejected.
func TestConfigSchemasAcceptSchemaInThemesAndRejectMalformedThemes(t *testing.T) {
	data, err := builtinThemes.ReadFile("theme_dark.json")
	if err != nil {
		t.Fatal(err)
	}
	theme := func(edit func(map[string]any)) []byte {
		t.Helper()
		var builtIn map[string]any
		if err := json.Unmarshal(data, &builtIn); err != nil {
			t.Fatal(err)
		}
		edit(builtIn)
		out, err := json.Marshal(builtIn)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	if err := ValidateThemeJSON("custom", theme(func(m map[string]any) {
		m["$schema"] = "https://pi.dev/schemas/theme.schema.json"
		m["name"] = "custom"
	})); err != nil {
		t.Fatalf("the built-in theme with $schema: %v", err)
	}
	if err := ValidateThemeJSON("invalid", theme(func(m map[string]any) { m["name"] = "invalid/name" })); err == nil || !strings.Contains(err.Error(), "theme names cannot contain") {
		t.Fatalf("a name with /: %v, want %q", err, "theme names cannot contain")
	}
	if err := ValidateThemeJSON("invalid", theme(func(m map[string]any) { m["colors"] = map[string]any{} })); err == nil || !strings.Contains(err.Error(), "Missing required color tokens") {
		t.Fatalf("empty colors: %v, want %q", err, "Missing required color tokens")
	}
	for name, edit := range map[string]func(map[string]any){
		"unexpected member":    func(m map[string]any) { m["unexpected"] = true },
		"misspelled color":     func(m map[string]any) { m["colors"].(map[string]any)["scrollbarThmb"] = "" },
		"unknown export entry": func(m map[string]any) { m["export"] = map[string]any{"unexpected": ""} },
	} {
		if err := ValidateThemeJSON("invalid", theme(edit)); err == nil || !strings.Contains(strings.ToLower(err.Error()), "additional properties") {
			t.Errorf("%s: %v, want an additional properties error", name, err)
		}
	}
}
