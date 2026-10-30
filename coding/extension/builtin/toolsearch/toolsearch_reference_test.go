package toolsearch_test

import (
	"fmt"
	"math"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin/toolsearch"
)

// Scores and ties measured on Pi's own Bm25Ranker for the same documents; the upstream tests assert order only, so these pin the numbers, the schema walk and
// the tie order. The numbers are from Pi cc44fa21 (tool-search/tool.ts, #11331, D217), which scores the name apart from the description; the 0.99.1 numbers
// they replace came from a ranker that folded the name into the text.

func referenceDocuments() []toolsearch.Document {
	return []toolsearch.Document{
		toolsearch.CreateDocument(info("alpha_tool", "Fetch pull requests and issues.", `{"repo":{"type":"string","description":"repository name"}}`), nil),
		toolsearch.CreateDocument(info("beta_tool", "Fetch pull requests.", `{"q":{"anyOf":[{"type":"string","description":"free text alternative"}]}}`), nil),
		toolsearch.CreateDocument(info("gamma_tool", "Fetch pull requests.", `{"z":{"oneOf":[{"type":"number","description":"zebra oneof"}]},"y":{"allOf":[{"type":"string","description":"yak allof"}]}}`), nil),
	}
}

func TestBm25ScoresMatchUpstream(t *testing.T) {
	documents := referenceDocuments()
	for _, c := range []struct {
		query string
		want  []toolsearch.Match
	}{
		{"pull requests", []toolsearch.Match{{Name: "alpha_tool", Score: 0.2769134617541329}, {Name: "beta_tool", Score: 0.2769134617541329}, {Name: "gamma_tool", Score: 0.24932429766792777}}},
		{"issues", []toolsearch.Match{{Name: "alpha_tool", Score: 1.0170073811965852}}},
		{"repository", []toolsearch.Match{{Name: "alpha_tool", Score: 1.0170073811965852}}},
		{"alternative", []toolsearch.Match{{Name: "beta_tool", Score: 1.0170073811965852}}},
		{"zebra", []toolsearch.Match{{Name: "gamma_tool", Score: 0.915681922553383}}},
		{"yak", []toolsearch.Match{{Name: "gamma_tool", Score: 0.915681922553383}}},
		{"fetch", []toolsearch.Match{{Name: "alpha_tool", Score: 0.13845673087706645}, {Name: "beta_tool", Score: 0.13845673087706645}, {Name: "gamma_tool", Score: 0.12466214883396388}}},
		{"tool", []toolsearch.Match{{Name: "alpha_tool", Score: 0.4005941778735677}, {Name: "beta_tool", Score: 0.4005941778735677}, {Name: "gamma_tool", Score: 0.4005941778735677}}},
		{"alpha tool", []toolsearch.Match{{Name: "alpha_tool", Score: 4.457442582544996}, {Name: "beta_tool", Score: 0.4005941778735677}, {Name: "gamma_tool", Score: 0.4005941778735677}}},
	} {
		got := toolsearch.NewBm25Ranker().Rank(c.query, documents, 8)
		if len(got) != len(c.want) {
			t.Errorf("%q: %+v, want %+v", c.query, got, c.want)
			continue
		}
		for i := range got {
			if got[i].Name != c.want[i].Name || math.Abs(got[i].Score-c.want[i].Score) > 1e-12 {
				t.Errorf("%q[%d]: %+v, want %+v", c.query, i, got[i], c.want[i])
			}
		}
	}
}

func TestDocumentsWithEqualScoresKeepDocumentOrder(t *testing.T) {
	documents := []toolsearch.Document{
		toolsearch.CreateDocument(extension.ToolInfo{Name: "one", Description: "same words"}, nil),
		toolsearch.CreateDocument(extension.ToolInfo{Name: "one2", Description: "same words"}, nil),
	}
	got := toolsearch.NewBm25Ranker().Rank("words", documents, 8)
	if len(got) != 2 || got[0].Name != "one" || got[1].Name != "one2" || math.Abs(got[0].Score-0.1823215567939546) > 1e-12 {
		t.Fatalf("Rank = %+v", got)
	}
}

// Documents of three lengths interleave three score groups; ties inside a group must keep document order. An unstable
// sort scrambles the groups (sort.Slice is stable only for short or already ordered input).
func TestManyInterleavedScoreGroupsKeepDocumentOrderInsideEachGroup(t *testing.T) {
	descriptions := []string{"same words", "same words plus more", "same words plus even more terms here"}
	var documents []toolsearch.Document
	groups := make([][]string, 3)
	for i := range 90 {
		name := fmt.Sprintf("tool%02d", i)
		documents = append(documents, toolsearch.CreateDocument(extension.ToolInfo{Name: name, Description: descriptions[i%3]}, nil))
		groups[i%3] = append(groups[i%3], name)
	}
	want := slices.Concat(groups[0], groups[1], groups[2]) // shorter documents score higher
	if got := names(toolsearch.NewBm25Ranker().Rank("words", documents, 100)); !slices.Equal(got, want) {
		t.Fatalf("order changed: %q", got)
	}
}
