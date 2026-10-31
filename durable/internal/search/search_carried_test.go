package search

// pi: packages/durable/src/search.ts

import (
	_ "embed"
	"encoding/json"
	"slices"
	"testing"
)

//go:embed testdata/ranker_goldens.json
var rankerGoldens []byte

// The goldens are Pi's own ranking at c7372af4 (testdata/gen_ranker_goldens.mjs) over random documents with namespace texts, names that share words with the queries, names with
// U+0130, the Kelvin sign and a character outside the BMP, and limits: the names and their order must match exactly. The pinned Pi's oracle (search_pi_oracle_test.go) cannot supply
// them: it scores the name as part of the text and has no namespace text.
func TestRankDocumentsMatchesPiTheLatestCarriedRanker(t *testing.T) {
	var probes []struct {
		Documents []struct {
			Name          string `json:"name"`
			Text          string `json:"text"`
			NamespaceText string `json:"namespaceText"`
		} `json:"documents"`
		Query  string   `json:"query"`
		Limit  int      `json:"limit"`
		Ranked []string `json:"ranked"`
	}
	if err := json.Unmarshal(rankerGoldens, &probes); err != nil {
		t.Fatal(err)
	}
	ranked, ordered := 0, 0
	for i, probe := range probes {
		documents := make([]Document, len(probe.Documents))
		for j, d := range probe.Documents {
			documents[j] = Document(d)
		}
		got := RankDocuments(probe.Query, documents, probe.Limit)
		ranked += len(got)
		if len(got) > 1 {
			ordered++
		}
		if !slices.Equal(got, probe.Ranked) {
			t.Errorf("probe %d %q limit %d: got %v, want %v", i, probe.Query, probe.Limit, got, probe.Ranked)
		}
	}
	if ranked < len(probes) || ordered < 100 {
		t.Fatalf("%d matches over %d probes, %d with an order to check: the goldens do not exercise the ranker", ranked, len(probes), ordered)
	}
}
