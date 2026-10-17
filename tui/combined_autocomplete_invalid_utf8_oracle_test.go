package tui

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// pi: packages/tui/src/autocomplete.ts

// Direct path completion over a directory whose names are not valid UTF-8. Node's readdirSync decodes each name as UTF-8 (U+FFFD per ill-formed subpart) and keeps the entry; a
// symlink's stat on the decoded name then fails, so the entry stays a file. PiG must offer the same values and labels, not the raw bytes.
func TestCombinedAutocompleteNamesThatAreNotValidUTF8MatchPi(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("file names are arbitrary bytes only on Linux")
	}
	base := t.TempDir()
	for _, name := range []string{"ok.txt", "bad\xffname.txt", "bad\xe2\x82", "\xc0\xafx", "z\xed\xa0\x80"} {
		if err := os.WriteFile(filepath.Join(base, name), nil, 0o600); err != nil {
			t.Skipf("file system refuses the name %q: %v", name, err)
		}
	}
	for _, name := range []string{"dir\xff", "dir"} {
		if err := os.Mkdir(filepath.Join(base, name), 0o700); err != nil {
			t.Skipf("file system refuses the directory name %q: %v", name, err)
		}
	}
	testenv.RequireDirectoryLink(t, "dir", filepath.Join(base, "link\xff"))
	testenv.RequireDirectoryLink(t, "dir", filepath.Join(base, "link"))
	type item struct {
		Value string `json:"value"`
		Label string `json:"label"`
	}
	type result struct {
		Items  []item `json:"items"`
		Prefix string `json:"prefix"`
		Null   bool   `json:"null"`
	}
	befores := []string{"./", "./b", "./ba", "./d", "./l", "./o", "./z", "cat ./", `"./b`, "./\u00c0", "./dir/"}
	var want []result
	pioracle.Run(t, `
const mod = await load("pi-tui/autocomplete.js");
const provider = new mod.CombinedAutocompleteProvider([], input.base, null);
const out = [];
for (const before of input.cases) {
	const suggestions = await provider.getSuggestions([before], 0, before.length, { signal: new AbortController().signal, force: true });
	if (!suggestions) { out.push({ items: [], prefix: "", null: true }); continue; }
	out.push({ items: suggestions.items.map((i) => ({ value: i.value, label: i.label })), prefix: suggestions.prefix, null: false });
}
emit(out);`, map[string]any{"base": base, "cases": befores}, &want)
	provider := NewCombinedProvider(nil, base, "")
	for i, before := range befores {
		got := result{Null: true, Items: []item{}}
		if suggestions := provider.GetSuggestions(context.Background(), []string{before}, 0, len(before), AutocompleteSuggestionOptions{Force: true}); suggestions != nil {
			got.Null = false
			got.Prefix = suggestions.Prefix
			for _, it := range suggestions.Items {
				got.Items = append(got.Items, item{it.Value, it.Label})
			}
		}
		if !reflect.DeepEqual(got, want[i]) {
			t.Errorf("%q:\n  PiG %+v\n  Pi  %+v", before, got, want[i])
		}
	}
}
