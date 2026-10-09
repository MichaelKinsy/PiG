package tui

// pi: packages/tui/src/fuzzy.ts

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type fuzzyProbe struct {
	Op    string   `json:"op"`
	Query string   `json:"query"`
	Text  string   `json:"text"`
	Items []string `json:"items"`
}

// fuzzyProbes draw queries and texts from an alphabet whose lowercase form changes length or position in UTF-16 (İ, ß, Σ, surrogate pairs, combining
// marks, full-width letters), the word-boundary characters, digits for the letter/digit swap, and JavaScript whitespace with its two edges against
// Unicode White_Space: U+0085 is not JavaScript whitespace and U+FEFF is.
func fuzzyProbes() []fuzzyProbe {
	random := rand.New(rand.NewSource(20260931))
	alphabet := []string{"a", "b", "c", "A", "B", "x", "1", "2", "10", " ", "-", "_", ".", "/", ":", "\t", "\u00a0", "\u2028", "\u0085", "\ufeff", "İ", "i", "ß", "ss", "Σ", "σ", "ς", "é", "e\u0301", "😀", "𝐀", "日", "Ａ", "ａ", "ǅ", "ǆ", "K", "ﬃ", "ŉ"}
	word := func(n int) string {
		var out string
		for range random.Intn(n + 1) {
			out += alphabet[random.Intn(len(alphabet))]
		}
		return out
	}
	var probes []fuzzyProbe
	for range 3000 {
		text := word(12)
		query := word(4)
		if len(text) > 0 && random.Intn(2) == 0 {
			// a subsequence of the text, so matches are common
			query = ""
			for _, r := range text {
				if random.Intn(3) == 0 {
					query += string(r)
				}
			}
		}
		if random.Intn(8) == 0 {
			query = []string{"a1", "1a", "abc12", "12abc", "ab1c", "x10", "10x", "A1"}[random.Intn(8)]
		}
		probes = append(probes, fuzzyProbe{Op: "match", Query: query, Text: text})
	}
	for range 500 {
		items := make([]string, random.Intn(8))
		for i := range items {
			items[i] = word(10)
		}
		query := word(3) + []string{"", " ", "/", " a", "a b", "a/b", "  ", "\u00a0x"}[random.Intn(8)] + word(2)
		probes = append(probes, fuzzyProbe{Op: "filter", Items: items, Query: query})
	}
	return probes
}

// fuzzy.ts runs in Node from the pinned source against the same probes: every match flag and score (UTF-16 positions, the lowercasing of İ and Σ,
// word boundaries, the letter/digit swap) and every filtered order must match Pi's.
func TestFuzzyMatchesPiOnSeededProbes(t *testing.T) {
	probes := fuzzyProbes()
	for i := range probes {
		if probes[i].Items == nil {
			probes[i].Items = []string{}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "tui", "src", "fuzzy.ts")
	cmd := exec.CommandContext(t.Context(), "node", "testdata/fuzzy.mjs", source)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []json.RawMessage
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi answered %d of %d probes", len(expected), len(probes))
	}
	matched, differing := 0, 0
	for i, probe := range probes {
		var differs bool
		var got, want any
		if probe.Op == "match" {
			result := FuzzyMatchScore(probe.Query, probe.Text)
			var piMatch FuzzyMatch
			var raw struct {
				Matches bool    `json:"matches"`
				Score   float64 `json:"score"`
			}
			_ = json.Unmarshal(expected[i], &raw)
			piMatch = FuzzyMatch{Matches: raw.Matches, Score: raw.Score}
			got, want, differs = result, piMatch, result != piMatch
			if piMatch.Matches {
				matched++
			}
		} else {
			var piItems []string
			_ = json.Unmarshal(expected[i], &piItems)
			result := FuzzyFilter(probe.Items, probe.Query, func(s string) string { return s })
			if len(result) == 0 {
				result = []string{}
			}
			if len(piItems) == 0 {
				piItems = []string{}
			}
			got, want, differs = result, piItems, !reflect.DeepEqual(result, piItems)
		}
		if differs {
			if differing++; differing <= 8 {
				t.Errorf("probe %d (%s query %q text %q items %q) differs from Pi:\n  Pig %v\n  Pi  %v", i, probe.Op, probe.Query, probe.Text, probe.Items, got, want)
			}
		}
	}
	if differing > 8 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(probes))
	}
	if matched < 300 {
		t.Errorf("probes are one-sided: Pi matched %d", matched)
	}
}
