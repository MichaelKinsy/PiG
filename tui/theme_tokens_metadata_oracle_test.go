package tui

// pi: packages/coding-agent/src/modes/interactive/theme/theme-tokens.ts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type piThemeToken struct {
	Name        string  `json:"name"`
	Slot        string  `json:"slot"`
	Description string  `json:"description"`
	Fallback    *string `json:"fallback"`
}

func piThemeTokens(t *testing.T) []piThemeToken {
	t.Helper()
	var tokens []piThemeToken
	pioracle.Run(t, `
const { THEME_TOKENS } = await load("pi-coding-agent/modes/interactive/theme/theme-tokens.js");
emit(Object.entries(THEME_TOKENS).map(([name, d]) => ({ name, slot: d.slot, description: d.description, fallback: d.fallback ?? null })));`, nil, &tokens)
	if len(tokens) == 0 {
		t.Fatal("the oracle returned no theme tokens")
	}
	return tokens
}

// THEME_TOKENS is the table of every theme token: its order, slot, description and the token an omitted one falls back to. The Go tables must carry
// the same tokens in the same order, split into the same foreground and background slots, with the same descriptions, and an optional token is exactly
// one that has a fallback.
func TestThemeTokenMetadataMatchesPi(t *testing.T) {
	pi := piThemeTokens(t)
	var names []string
	for _, token := range pi {
		names = append(names, token.Name)
	}
	var got []string
	for i, token := range themeColorTokens {
		got = append(got, token.name)
		if i >= len(pi) || token.name != pi[i].Name {
			continue
		}
		want := pi[i]
		if token.description != want.Description {
			t.Errorf("%s description %q, Pi %q", token.name, token.description, want.Description)
		}
		if token.optional != (want.Fallback != nil) {
			t.Errorf("%s optional = %v, Pi fallback %v", token.name, token.optional, want.Fallback)
		}
		if themeBackgroundTokens[token.name] != (want.Slot == "background") {
			t.Errorf("%s is a background = %v, Pi slot %q", token.name, themeBackgroundTokens[token.name], want.Slot)
		}
		if strings.HasSuffix(token.name, "Bg") != (want.Slot == "background") {
			t.Errorf("%s: the Bg suffix rule disagrees with Pi slot %q", token.name, want.Slot)
		}
	}
	if !slices.Equal(got, names) {
		t.Errorf("theme tokens\n got %v\n Pi  %v", got, names)
	}
	wantFallbacks := map[string]string{}
	for _, token := range pi {
		if token.Fallback != nil {
			wantFallbacks[token.Name] = *token.Fallback
		}
	}
	gotFallbacks := map[string]string{}
	for _, fallback := range themeColorFallbacks {
		gotFallbacks[fallback.token] = fallback.from
	}
	if len(gotFallbacks) != len(themeColorFallbacks) || len(gotFallbacks) != len(wantFallbacks) {
		t.Fatalf("fallbacks %v, Pi %v", gotFallbacks, wantFallbacks)
	}
	for token, from := range wantFallbacks {
		if gotFallbacks[token] != from {
			t.Errorf("%s falls back to %q, Pi %q", token, gotFallbacks[token], from)
		}
	}
}

// Dropping each optional token from a copy of Pi's dark theme makes Pi's loader resolve it to its fallback token; PiG's loader must resolve the same ANSI
// opening for every token, in truecolor and 256-color mode, whether one optional token or all of them are omitted.
func TestOmittedOptionalThemeTokensFallBackAsInPi(t *testing.T) {
	root := pioracle.Root(t)
	data, err := os.ReadFile(filepath.Join(root, "pi-coding-agent", "modes", "interactive", "theme", "dark.json"))
	if err != nil {
		t.Fatal(err)
	}
	var base map[string]any
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatal(err)
	}
	var optional []string
	for _, token := range piThemeTokens(t) {
		if token.Fallback != nil {
			optional = append(optional, token.Name)
		}
	}
	// Give each optional token a color of its own in the base so that omitting it changes the answer.
	baseColors := base["colors"].(map[string]any)
	for i, token := range optional {
		baseColors[token] = []string{"#102030", "#405060", "#708090", "#a0b0c0", "#d0e0f0"}[i%5]
	}
	variants := [][]string{{}, optional}
	for _, token := range optional {
		variants = append(variants, []string{token})
	}
	type variantAnswer struct {
		Fg map[string]string `json:"fg"`
		Bg map[string]string `json:"bg"`
	}
	dir := t.TempDir()
	var files []string
	for i, omit := range variants {
		doc := map[string]any{}
		for k, v := range base {
			doc[k] = v
		}
		colors := map[string]any{}
		for k, v := range baseColors {
			if !slices.Contains(omit, k) {
				colors[k] = v
			}
		}
		doc["colors"] = colors
		encoded, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "variant"+string(rune('a'+i))+".json")
		if err := os.WriteFile(path, encoded, 0o600); err != nil {
			t.Fatal(err)
		}
		files = append(files, path)
	}
	var want []map[string]variantAnswer
	pioracle.Run(t, `
const { loadThemeFromPath } = await load("pi-coding-agent/modes/interactive/theme/theme.js");
const { THEME_TOKENS } = await load("pi-coding-agent/modes/interactive/theme/theme-tokens.js");
emit(input.map((file) => {
	const row = {};
	for (const mode of ["truecolor", "256color"]) {
		const theme = loadThemeFromPath(file, mode);
		const fg = {}, bg = {};
		for (const [name, d] of Object.entries(THEME_TOKENS)) (d.slot === "background" ? bg : fg)[name] = d.slot === "background" ? theme.getBgAnsi(name) : theme.getFgAnsi(name);
		row[mode] = { fg, bg };
	}
	return row;
}));`, files, &want)
	if len(want) != len(files) {
		t.Fatalf("Pi answered %d of %d themes", len(want), len(files))
	}
	for i, path := range files {
		for mode, answer := range want[i] {
			theme, err := LoadThemeFromPath(path, TerminalColorMode(mode))
			if err != nil {
				t.Fatalf("omitting %v: %v", variants[i], err)
			}
			fg, bg := theme.ANSIPalette()
			if !mapsEqual(fg, answer.Fg) || !mapsEqual(bg, answer.Bg) {
				t.Errorf("omitting %v in %s: PiG palette differs from Pi\n fg PiG %q\n fg Pi  %q\n bg PiG %q\n bg Pi  %q", variants[i], mode, fg, answer.Fg, bg, answer.Bg)
			}
		}
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || v != w {
			return false
		}
	}
	return true
}
