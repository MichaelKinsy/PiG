package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every map literal in a package-level initializer is built at package initialization, so equal catalog maps are emitted once
// and the models refer to the shared element.
func TestEmitSharesEqualCatalogMaps(t *testing.T) {
	headers := func() map[string]string { return map[string]string{"B": "2", "A": "1"} }
	levels := func() map[string]*string { return map[string]*string{"high": new("high"), "off": nil} }
	rows := []modelRow{
		{Provider: "p", ID: "a", Headers: headers(), ThinkingLevelMap: levels()},
		{Provider: "p", ID: "b", Headers: map[string]string{"A": "1"}, ThinkingLevelMap: map[string]*string{"max": new("max")}},
		{Provider: "q", ID: "c", Headers: headers(), ThinkingLevelMap: levels()},
	}
	output := filepath.Join(t.TempDir(), "models.go")
	if err := emit(output, "rows", rows); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, want := range []string{
		"Headers:              generatedHeaders[0],", "Headers:              generatedHeaders[1],",
		"ThinkingLevelMap:     generatedThinkingLevelMaps[0],", "ThinkingLevelMap:     generatedThinkingLevelMaps[1],",
		"var generatedHeaders = [...]map[string]string{\n\t{\"A\": \"1\", \"B\": \"2\"},\n\t{\"A\": \"1\"},\n}\n",
		"var generatedThinkingLevelMaps = [...]map[ModelThinkingLevel]*string{\n\t{ModelThinkingLevel(\"high\"): &generatedStrings[0], ModelThinkingLevel(\"off\"): nil},\n\t{ModelThinkingLevel(\"max\"): &generatedStrings[1]},\n}\n",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("generated catalog lacks %q:\n%s", want, src)
		}
	}
	if got := strings.Count(src, "generatedHeaders[0]"); got != 2 {
		t.Fatalf("models with equal headers refer to generatedHeaders[0] %d times, want 2:\n%s", got, src)
	}
	if got := strings.Count(src, "generatedThinkingLevelMaps[0]"); got != 2 {
		t.Fatalf("models with equal thinking level maps refer to generatedThinkingLevelMaps[0] %d times, want 2:\n%s", got, src)
	}
}
