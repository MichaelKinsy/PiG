package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// themeValidationDocs mutates the built-in dark theme into documents the schema of theme-json.ts accepts or rejects.
func themeValidationDocs(t *testing.T) []string {
	t.Helper()
	base, err := os.ReadFile("../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/modes/interactive/theme/dark.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(base, &doc); err != nil {
		t.Fatal(err)
	}
	clone := func() map[string]any {
		var c map[string]any
		raw, _ := json.Marshal(doc)
		_ = json.Unmarshal(raw, &c)
		return c
	}
	colors := func(m map[string]any) map[string]any { return m["colors"].(map[string]any) }
	var docs []map[string]any
	add := func(mutate func(m map[string]any)) {
		m := clone()
		mutate(m)
		docs = append(docs, m)
	}
	add(func(m map[string]any) {})
	for _, token := range []string{"accent", "bashMode", "thinkingXhigh", "syntaxPunctuation", "mdHr"} {
		add(func(m map[string]any) { delete(colors(m), token) })
	}
	add(func(m map[string]any) {
		delete(colors(m), "accent")
		delete(colors(m), "text")
		delete(colors(m), "bashMode")
	})
	add(func(m map[string]any) { delete(m, "name") })
	add(func(m map[string]any) { m["name"] = 5 })
	add(func(m map[string]any) { delete(m, "colors") })
	add(func(m map[string]any) { m["colors"] = []any{} })
	add(func(m map[string]any) { m["colors"] = "x" })
	add(func(m map[string]any) { colors(m)["accent"] = 256 })
	add(func(m map[string]any) { colors(m)["accent"] = -1 })
	add(func(m map[string]any) { colors(m)["accent"] = 1.5 })
	add(func(m map[string]any) { colors(m)["accent"] = true })
	add(func(m map[string]any) { colors(m)["accent"] = nil })
	add(func(m map[string]any) { colors(m)["accent"] = map[string]any{} })
	add(func(m map[string]any) { colors(m)["accent"] = 12; colors(m)["text"] = []any{1} })
	add(func(m map[string]any) { m["appearance"] = "dark" })
	add(func(m map[string]any) { m["appearance"] = "blue" })
	add(func(m map[string]any) { m["appearance"] = 3 })
	add(func(m map[string]any) { m["vars"] = map[string]any{"a": "#fff", "b": 7} })
	add(func(m map[string]any) { m["vars"] = map[string]any{"a": true} })
	add(func(m map[string]any) { m["vars"] = []any{} })
	add(func(m map[string]any) { m["export"] = map[string]any{"pageBg": "#000", "cardBg": 3} })
	add(func(m map[string]any) { m["export"] = map[string]any{"pageBg": false, "extra": 1} })
	add(func(m map[string]any) { m["export"] = "x" })
	add(func(m map[string]any) { m["$schema"] = 1 })
	add(func(m map[string]any) { colors(m)["scrollbarTrack"] = 3; colors(m)["scrollbarThumb"] = false })
	add(func(m map[string]any) { colors(m)["thinkingMax"] = []any{} })
	add(func(m map[string]any) { colors(m)["unknownToken"] = "#fff" })
	add(func(m map[string]any) { m["unknown"] = 1 })
	add(func(m map[string]any) { delete(m, "colors"); delete(m, "name") })
	var out []string
	for _, m := range docs {
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(raw))
	}
	out = append(out, []string{`null`, `[]`, `1`, `"x"`, `true`, `{}`}...)
	return out
}

func TestThemeJSONValidationMatchesPi(t *testing.T) {
	docs := themeValidationDocs(t)
	dir := t.TempDir()
	for i, doc := range docs {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprint(i)), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/theme_json_validate.mjs", pigversion.UpstreamVersion, dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, doc := range docs {
		got := ""
		if err := ValidateThemeJSON(fmt.Sprintf("doc-%d", i), []byte(doc)); err != nil {
			got = err.Error()
		}
		if got != expected[i] {
			if failures++; failures <= 8 {
				t.Errorf("doc %d %s\n got  %q\n want %q", i, strings.TrimSpace(doc[:min(len(doc), 60)]), got, expected[i])
			}
		}
	}
	if failures > 0 {
		t.Fatalf("%d of %d documents differ from Pi", failures, len(docs))
	}
}
