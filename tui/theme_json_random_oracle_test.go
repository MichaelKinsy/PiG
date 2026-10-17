package tui

import (
	"encoding/json"
	"math/rand"
	"os"
	"slices"
	"testing"
)

// TestThemeJSONValidationOnRandomMutationsMatchesPi: ValidateThemeJSON (theme-schema.ts validateThemeJson, TypeBox schema messages included) over the dark theme with one to three random edits each: a token or top-level key deleted, retyped (numbers in and out of 0..255, fractions, strings, hex spellings, variable references, null, arrays, objects) or added, and the vars, export and appearance sections replaced.
func TestThemeJSONValidationOnRandomMutationsMatchesPi(t *testing.T) {
	base, err := os.ReadFile("../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/modes/interactive/theme/dark.json")
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(20261011))
	values := []any{
		"", "#fff", "#ffffff", "#GGGGGG", "#12345", "red", "accent", "$accent", "  ", 0, 1, 255, 256, -1, 1.5, 1e3, 0.0, true, false, nil,
		[]any{}, []any{1, "a"}, map[string]any{}, map[string]any{"a": 1}, "\u00e9", "#ABCDEF", "\u0000",
	}
	pick := func(m map[string]any) string {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		return keys[rng.Intn(len(keys))]
	}
	var docs []string
	for len(docs) < 1500 {
		var doc map[string]any
		if err := json.Unmarshal(base, &doc); err != nil {
			t.Fatal(err)
		}
		for range 1 + rng.Intn(3) {
			section := doc
			if c, ok := doc["colors"].(map[string]any); ok && rng.Intn(3) != 0 {
				section = c
			} else if e, ok := doc["export"].(map[string]any); ok && rng.Intn(5) == 0 {
				section = e
			}
			if len(section) == 0 {
				continue
			}
			key := pick(section)
			switch rng.Intn(6) {
			case 0:
				delete(section, key)
			case 1:
				section["extra"+string(rune('a'+rng.Intn(3)))] = values[rng.Intn(len(values))]
			case 2:
				doc["vars"] = map[string]any{"accent": values[rng.Intn(len(values))], "v": values[rng.Intn(len(values))]}
			case 3:
				doc["export"] = map[string]any{"pageBg": values[rng.Intn(len(values))], "cardBg": values[rng.Intn(len(values))], "infoBg": values[rng.Intn(len(values))]}
			case 4:
				doc["appearance"] = values[rng.Intn(len(values))]
			default:
				section[key] = values[rng.Intn(len(values))]
			}
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, string(raw))
	}
	compareThemeValidation(t, docs)
}
