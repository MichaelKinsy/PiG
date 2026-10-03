package tui

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"testing"
)

// Ports packages/coding-agent/test/theme-export.test.ts:73 "converts OKHSL export colors to hex because CSS does not support them" (upstream 0.99.1).
//
// Per-case substitution: upstream sets `vars: { card: ... }` and reads only the export section through loadThemeJson. PiG reads the export colors from a loaded Theme, which must resolve every color, so the built-in dark vars are kept next to `card`, as TestThemeExportColorsUpstream does.
func TestThemeExportOkhslColorsUpstream(t *testing.T) {
	dark, err := builtinThemes.ReadFile("theme_dark.json")
	if err != nil {
		t.Fatal(err)
	}
	var theme map[string]any
	if err := json.Unmarshal(dark, &theme); err != nil {
		t.Fatal(err)
	}
	theme["name"] = "custom-export-okhsl"
	maps.Copy(theme["vars"].(map[string]any), map[string]any{"card": "okhsl(250 20% 20%)"})
	theme["export"] = map[string]any{"pageBg": "okhsl(250 20% 15%)", "cardBg": "card", "infoBg": "oklch(30% 0.05 80)"}
	data, err := json.MarshalIndent(theme, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "custom-export-okhsl.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadThemeFile(path)
	if err != nil {
		t.Fatal(err)
	}

	page, err := NewOkhslColor(250, 0.2, 0.15)
	if err != nil {
		t.Fatal(err)
	}
	card, err := NewOkhslColor(250, 0.2, 0.2)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{"pageBg": loaded.ExportPageBg, "cardBg": loaded.ExportCardBg, "infoBg": loaded.ExportInfoBg}
	want := map[string]string{"pageBg": ColorToHex(page), "cardBg": ColorToHex(card), "infoBg": "oklch(30% 0.05 80)"}
	if !maps.Equal(got, want) {
		t.Fatalf("export colors = %v, want %v", got, want)
	}
}
