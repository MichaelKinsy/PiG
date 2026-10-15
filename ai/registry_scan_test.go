package ai

import (
	"strings"
	"testing"
)

// LookupModelExact answers from a catalog scan until the indexes exist; the scan must answer every key as the byFQ index does.
func TestScanModelExactAnswersAsTheIndex(t *testing.T) {
	byFQ := indexGeneratedModelsByFQ(GeneratedModels)
	specs := []string{"", "/", "anthropic", "anthropic/", "/claude-sonnet-4-5", "anthropic//claude-sonnet-4-5", "Anthropic/claude-sonnet-4-5",
		"anthropic/claude-sonnet-4-5 ", "nope/x", "openrouter/openai/gpt-4o", "openai/gpt-4o:high"}
	for i := range GeneratedModels {
		m := &GeneratedModels[i]
		key := m.Provider + "/" + m.ID
		specs = append(specs, key, key[:len(key)-1], m.Provider+"/"+strings.ToUpper(m.ID), m.ID, m.Provider+"x/"+m.ID)
	}
	for _, spec := range specs {
		want, wantOK := byFQ[spec]
		got, gotOK := scanModelExact(GeneratedModels, spec)
		if got != want || gotOK != wantOK {
			t.Fatalf("scanModelExact(%q) = %v, %v; the index has %v, %v", spec, got, gotOK, want, wantOK)
		}
	}
}

// A key the catalog repeats resolves to its last model, as indexGeneratedModelsByFQ' map keeps it.
func TestScanModelExactKeepsTheLastRepeatedKey(t *testing.T) {
	generated := []GeneratedModel{{Provider: "p", ID: "m", DisplayName: "first"}, {Provider: "p/m", ID: ""}, {Provider: "p", ID: "m", DisplayName: "last"}, {Provider: "q", ID: "m"}}
	byFQ := indexGeneratedModelsByFQ(generated)
	for _, spec := range []string{"p/m", "p/m/", "q/m", "p", "m"} {
		want, wantOK := byFQ[spec]
		got, gotOK := scanModelExact(generated, spec)
		if got != want || gotOK != wantOK {
			t.Fatalf("scanModelExact(%q) = %v, %v; the index has %v, %v", spec, got, gotOK, want, wantOK)
		}
	}
	if got, _ := scanModelExact(generated, "p/m"); got.DisplayName != "last" {
		t.Fatalf("repeated key resolved to %q, want the last model", got.DisplayName)
	}
}

func TestScanModelExactDoesNotAllocate(t *testing.T) {
	if allocs := testing.AllocsPerRun(10, func() { _, _ = scanModelExact(GeneratedModels, "nope/x") }); allocs != 0 {
		t.Fatalf("scanModelExact allocated %v times per lookup", allocs)
	}
}

func BenchmarkScanModelExactMiss(b *testing.B) {
	for b.Loop() {
		_, _ = scanModelExact(GeneratedModels, "fake/fake-model")
	}
}
