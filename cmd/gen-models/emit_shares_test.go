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
		"var generatedHeaders [2]map[string]string\n",
		"var generatedHeadersPairs = [...][]generatedPair[string, string]{\n\t{{\"A\", \"1\"}, {\"B\", \"2\"}},\n\t{{\"A\", \"1\"}},\n}\n",
		"var generatedHeadersRefs = [...]generatedMapRef{{0, 0}, {1, 1}, {2, 0}}\n",
		"var generatedThinkingLevelMaps [2]map[ModelThinkingLevel]*string\n",
		"var generatedThinkingLevelMapsPairs = [...][]generatedPair[ModelThinkingLevel, *string]{\n\t{{\"high\", &generatedStrings[0]}, {\"off\", nil}},\n\t{{\"max\", &generatedStrings[1]}},\n}\n",
		"var generatedThinkingLevelMapsRefs = [...]generatedMapRef{{0, 0}, {1, 1}, {2, 0}}\n",
		"generatedBuildMaps(generatedHeaders[:], generatedHeadersPairs[:])\n\tfor _, ref := range generatedHeadersRefs[:] {\n\t\tgeneratedRows[ref.row].Headers = generatedHeaders[ref.element]\n\t}\n",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("generated catalog lacks %q:\n%s", want, src)
		}
	}
	if strings.Contains(src, "Headers:") || strings.Contains(src, "ThinkingLevelMap:") {
		t.Fatalf("a row sets a map the linker cannot lay out:\n%s", src)
	}
}

// PromptCache and SamplingParams maps are not shared: a row that has one gets a map of its own, as it did when each row spelled a map literal, so a
// write through one row of the exported catalog cannot change another. A JSON null sampling value is spelled nil, not fmt's <nil>, which does not compile.
func TestEmitGivesEachRowItsOwnPromptCacheAndSamplingMap(t *testing.T) {
	rows := []modelRow{
		{Provider: "p", ID: "a", PromptCache: map[string]float64{"ttl": 5}, SamplingParams: map[string]any{"top_p": 0.5, "stop": nil, "mode": "x", "on": true}},
		{Provider: "p", ID: "b", PromptCache: map[string]float64{"ttl": 5}, SamplingParams: map[string]any{"top_p": 0.5, "stop": nil, "mode": "x", "on": true}},
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
		"var generatedPromptCachesPairs = [...][]generatedPair[string, float64]{\n\t{{\"ttl\", 5}},\n}\n",
		"var generatedPromptCachesRefs = [...]generatedMapRef{{0, 0}, {1, 0}}\n",
		"for _, ref := range generatedPromptCachesRefs[:] {\n\t\tgeneratedRows[ref.row].PromptCache = generatedMap(generatedPromptCachesPairs[ref.element])\n\t}\n",
		"var generatedSamplingParamsPairs = [...][]generatedPair[string, any]{\n\t{{\"mode\", \"x\"}, {\"on\", true}, {\"stop\", nil}, {\"top_p\", float64(0.5)}},\n}\n",
		"generatedRows[ref.row].SamplingParams = generatedMap(generatedSamplingParamsPairs[ref.element])\n",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("generated catalog lacks %q:\n%s", want, src)
		}
	}
	for _, shared := range []string{"var generatedPromptCaches [", "var generatedSamplingParams [", "generatedBuildMaps(generatedPromptCaches", "generatedBuildMaps(generatedSamplingParams"} {
		if strings.Contains(src, shared) {
			t.Fatalf("generated catalog shares an owned map (%q):\n%s", shared, src)
		}
	}
}
