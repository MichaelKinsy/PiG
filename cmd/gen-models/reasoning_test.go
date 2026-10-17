package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestVendorReasoningCatalogIngestion(t *testing.T) {
	dir := t.TempDir()
	modelsDev := filepath.Join(dir, "models-dev.json")
	openRouter := filepath.Join(dir, "openrouter.json")
	for path, data := range map[string]string{modelsDev: `{"google":{"models":{"gemini-test":{"reasoning_options":[{"type":"effort","values":["low","high"]}]}}}}`, openRouter: `{"data":[{"id":"stealth/ox-alpha","reasoning":{"mandatory":true,"supported_efforts":["max"]}}]}`} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	rows := []modelRow{{Provider: "google", ID: "gemini-test"}, {Provider: "google-vertex", ID: "gemini-test"}, {Provider: "openrouter", ID: "stealth/ox-alpha"}, {Provider: "unrelated", ID: "kept", ThinkingLevelMap: map[string]*string{"high": new("high")}}}
	if err := applyVendorReasoning(rows, modelsDev, openRouter); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, 1} {
		if rows[i].ThinkingLevelMap == nil || rows[i].ThinkingLevelMap["low"] == nil || *rows[i].ThinkingLevelMap["low"] != "low" || rows[i].ThinkingLevelMap["off"] != nil {
			t.Fatal("models.dev map lost", rows[i])
		}
	}
	if rows[2].ThinkingLevelMap["max"] == nil || *rows[2].ThinkingLevelMap["max"] != "max" || rows[2].ThinkingLevelMap["off"] != nil {
		t.Fatal("OpenRouter map lost")
	}
	if *rows[3].ThinkingLevelMap["high"] != "high" {
		t.Fatal("unrelated row changed")
	}
	output := filepath.Join(dir, "models.go")
	if err := emit(output, "vendor snapshots", rows); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	// "max" points at an element of generatedStrings whose value is "max".
	pointer := regexp.MustCompile(`ThinkingLevel\("max"\): &generatedStrings\[(\d+)\]`).FindStringSubmatch(string(raw))
	if !strings.Contains(string(raw), `ThinkingLevel("off"): nil`) || pointer == nil {
		t.Fatalf("derived controls absent from generated catalog: %s", raw)
	}
	index, _ := strconv.Atoi(pointer[1])
	values := strings.SplitN(string(raw), "var generatedStrings = [...]string{\n", 2)
	if len(values) != 2 {
		t.Fatalf("generatedStrings absent from generated catalog: %s", raw)
	}
	if lines := strings.Split(values[1], "\n"); index >= len(lines) || strings.TrimSpace(lines[index]) != `"max",` {
		t.Fatalf("generatedStrings[%d] is not \"max\": %s", index, values[1])
	}
}

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
