package docsdrift

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The keybinding reference names every keybinding ID by the string PiG's default tables declare. A new ID that no page names is a shortcut users cannot find, and an ID the page names that no table declares is one they cannot bind.

var (
	keybindingLiteral = regexp.MustCompile(`"((?:app|tui)\.[A-Za-z]+(?:\.[A-Za-z]+)*)"`)
	keybindingDoc     = regexp.MustCompile("`((?:app|tui)\\.[A-Za-z]+(?:\\.[A-Za-z]+)*)`")
)

func keybindingIDsInSource(t *testing.T) map[string]bool {
	t.Helper()
	ids := map[string]bool{}
	for _, rel := range []string{"tui/keybindings.go", "tui/alt_screen_keybindings.go", "internal/codingagent/keybindings.go", "internal/codingagent/app_keybinding.go"} {
		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range keybindingLiteral.FindAllStringSubmatch(string(data), -1) {
			ids[m[1]] = true
		}
	}
	if len(ids) < 60 {
		t.Fatalf("found only %d keybinding IDs in the default tables; the reader is broken", len(ids))
	}
	return ids
}

func TestKeybindingReferenceMatchesTheDefaultTables(t *testing.T) {
	ids := keybindingIDsInSource(t)
	for _, src := range corpus(t) {
		if src.Rel != "docs/site/docs/keybindings.md" && src.Rel != "internal/pigdocs/content/keybindings.md" {
			continue
		}
		documented := map[string]bool{}
		for _, m := range keybindingDoc.FindAllStringSubmatch(src.Body, -1) {
			documented[m[1]] = true
		}
		var missing, unknown []string
		for id := range ids {
			if !documented[id] {
				missing = append(missing, id)
			}
		}
		for id := range documented {
			if !ids[id] && !strings.HasSuffix(id, ".") {
				unknown = append(unknown, id)
			}
		}
		sort.Strings(missing)
		sort.Strings(unknown)
		if len(missing) > 0 {
			t.Errorf("%s does not name these keybinding IDs: %v", src.Rel, missing)
		}
		if len(unknown) > 0 {
			t.Errorf("%s names these keybinding IDs, which no default table declares: %v", src.Rel, unknown)
		}
	}
}
