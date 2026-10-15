package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pi's filters lowercase with String.prototype.toLowerCase, which maps İ (U+0130) to i followed by U+0307; Go's strings.ToLower maps it to a
// plain i. A query typed as "i̇z" therefore matches İzmir and not izmir.
const jsLowerQuery = "i\u0307z"

func TestScoreEntryMatchesOnlyTheJavaScriptLowercaseOfDottedI(t *testing.T) {
	if got := scoreEntry("src/İzmir.go", jsLowerQuery, false); got != 80 {
		t.Fatalf("scoreEntry(İzmir.go) = %d, want the prefix score 80", got)
	}
	if got := scoreEntry("src/izmir.go", jsLowerQuery, false); got != 0 {
		t.Fatalf("scoreEntry(izmir.go) = %d, want 0", got)
	}
}

func TestReaddirFileSuggestionsLowercaseAsJavaScriptDoes(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"İzmir.txt", "izmir.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	items := (&CombinedProvider{baseDir: dir}).readdirFileSuggestions(jsLowerQuery, false)
	if len(items) != 1 || items[0].Label != "İzmir.txt" {
		t.Fatalf("suggestions = %+v, want only İzmir.txt", items)
	}
}

func TestFilterableListLowercasesAsJavaScriptDoes(t *testing.T) {
	list := NewFilterableList("", []string{"İzmir", "izmir"})
	list.HandleInput(jsLowerQuery)
	if len(list.filtered) != 1 || list.Labels[list.filtered[0]] != "İzmir" {
		t.Fatalf("filtered = %v, want only İzmir", list.filtered)
	}
}

func TestConfigSelectorFilterLowercasesAsJavaScriptDoes(t *testing.T) {
	cs := NewConfigSelector(BuildResourceGroups([]ResourceItem{
		{Path: "/agent/prompts/a.md", ResourceType: ResourcePrompts, DisplayName: "İzmir.md", Enabled: true, Scope: "user", Origin: "top-level", Source: "auto"},
		{Path: "/agent/prompts/b.md", ResourceType: ResourcePrompts, DisplayName: "izmir-other.md", Enabled: true, Scope: "user", Origin: "top-level", Source: "auto"},
	}), 0)
	cs.HandleInput(jsLowerQuery)
	joined := strings.Join(cs.Render(100), "\n")
	if !strings.Contains(joined, "İzmir.md") || strings.Contains(joined, "izmir-other.md") {
		t.Fatalf("filtered render:\n%s", joined)
	}
}
