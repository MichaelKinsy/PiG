package search

// pi: packages/durable/src/search.ts

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi's tokenize and rankDocuments (search.ts, imported as-is under Node) answer for seeded random texts and rankings;
// tokenize and RankDocuments must answer the same. The words are the shapes the tokenizer and the stemmer branch on:
// camelCase and acronym boundaries, every plural ending, stop words, digits, punctuation, and letters whose lower case
// is not their ASCII lower case. A ranking compares the full order Pi gives, ties included.

type searchOp struct {
	Op        string     `json:"op"`
	Text      string     `json:"text"`
	Query     string     `json:"query"`
	Documents []docInput `json:"documents"`
	Limit     int        `json:"limit"`
}

type docInput struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

var searchWords = []string{
	"issue", "issues", "Issue", "searches", "search", "searchTools", "getHTTPResponse", "XMLParser", "parseURL", "ABCDef2Go",
	"boxes", "box", "axes", "ches", "shes", "sses", "zzes", "oxes", "ox", "classes", "class", "buses", "bus", "dries", "dry", "tries", "ties", "ies", "series", "gas", "ass", "glass",
	"status", "analysis", "churches", "wishes", "quizzes", "fizzes", "matches", "bash", "run", "Run", "RUN", "read", "write",
	"file", "files", "disk", "path", "offset", "limit", "content", "command", "timeout", "list", "list_issues", "mcp__github__x",
	"x_y-z.w", "v2", "v2s", "2fa", "a", "an", "and", "are", "as", "at", "be", "by", "for", "from", "in", "is", "it", "of", "on",
	"or", "that", "the", "this", "to", "with", "The", "WITH", "s", "ss", "sss", "ies", "yes", "is-a", "tool", "tools", "Tool",
	"İstanbul", "ΣΑΣ", "Straße", "\u212Aelvin", "ǅ", "ﬁle", "日本語", "naïve", "café", "snake_case_name", "kebab-case-name",
	"dot.separated.name", "path/to/file.ts", "line1\nline2", "tab\tsep", "  ", "!!!", "x", "12", "007", "Mixed123Case",
}

func randomText(rng *rand.Rand, words int) string {
	parts := make([]string, words)
	for i := range parts {
		parts[i] = searchWords[rng.IntN(len(searchWords))]
	}
	return strings.Join(parts, []string{" ", " ", "_", "-", ", ", ""}[rng.IntN(6)])
}

func searchOps() []searchOp {
	rng := rand.New(rand.NewPCG(20261116, 7))
	var ops []searchOp
	for _, word := range searchWords {
		ops = append(ops, searchOp{Op: "tokenize", Text: word})
	}
	for range 600 {
		ops = append(ops, searchOp{Op: "tokenize", Text: randomText(rng, rng.IntN(8))})
	}
	for range 4000 {
		documents := []docInput{}
		count := rng.IntN(13)
		if rng.IntN(8) == 0 {
			count = 13 + rng.IntN(40)
		}
		for i := range count {
			name := []string{"tool", "mcp__a__b", "read", "dup"}[rng.IntN(4)]
			if rng.IntN(3) > 0 {
				name += string(rune('a' + i%26))
			}
			documents = append(documents, docInput{Name: name, Text: randomText(rng, rng.IntN(26))})
		}
		// Duplicated documents tie exactly, which the order must keep.
		if len(documents) > 1 && rng.IntN(3) == 0 {
			documents = append(documents, documents[rng.IntN(len(documents))])
		}
		limit := []int{-1, 0, 1, 2, 3, 5, 8, 100}[rng.IntN(8)]
		ops = append(ops, searchOp{Op: "rank", Query: randomText(rng, 1+rng.IntN(4)), Documents: documents, Limit: limit})
	}
	return ops
}

func TestTokenizeAndRankDocumentsMatchPiOnSeededInputs(t *testing.T) {
	ops := searchOps()
	input, err := json.Marshal(ops)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/search_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var pi [][]string
	if err := json.Unmarshal(output, &pi); err != nil || len(pi) != len(ops) {
		t.Fatalf("oracle output: %v (%d answers for %d ops)", err, len(pi), len(ops))
	}
	ranked, tokens := 0, 0
	for i, op := range ops {
		var got []string
		switch op.Op {
		case "tokenize":
			got = tokenize(op.Text)
			if len(pi[i]) > 0 {
				tokens++
			}
			if !slices.Equal(got, pi[i]) {
				t.Errorf("tokenize(%q) = %q, Pi %q", op.Text, got, pi[i])
			}
		default:
			documents := make([]Document, len(op.Documents))
			for j, d := range op.Documents {
				documents[j] = Document{Name: d.Name, Text: d.Text}
			}
			got = RankDocuments(op.Query, documents, op.Limit)
			if len(pi[i]) > 1 {
				ranked++
			}
			if !slices.Equal(got, pi[i]) {
				t.Errorf("RankDocuments(%q, %+v, %d) = %q, Pi %q", op.Query, op.Documents, op.Limit, got, pi[i])
			}
		}
	}
	// The corpus has to reach real rankings, or it would pass on empty answers.
	if ranked < 100 || tokens < 500 {
		t.Errorf("only %d rankings of two or more names and %d non-empty tokenizations reached the comparison", ranked, tokens)
	}
}
