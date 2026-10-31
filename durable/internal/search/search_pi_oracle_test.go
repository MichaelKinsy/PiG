package search

// pi: packages/durable/src/search.ts

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/searchtext"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi's tokenize and rankDocuments (search.ts, imported as-is under Node) answer for seeded random texts and rankings;
// tokenize and RankDocuments must answer the same. The words are the shapes the tokenizer and the stemmer branch on:
// camelCase and acronym boundaries, every plural ending, stop words, digits, punctuation, and letters whose lower case
// is not their ASCII lower case. A ranking compares the full order Pi gives, ties included.
//
// D217 (Pi c7372af4, #11631): the pinned Pi scores the name as part of the text, has no namespace text and ranks by score alone, PiG weighs the name and the namespace as fields of their own
// and ranks a tool matching more of the query's distinct terms first. The two agree where the name holds no term (every part is a stop word, which also leaves a tool's whole name out of
// the query) and the query has one distinct term, so every match covers it: those rankings compare in full order. For a query of several distinct terms the two agree on which tools match,
// compared as a set when the limit keeps them all. TestRankDocumentsMatchesPiTheLatestCarriedRanker pins the carried ranking with Pi's own answers.

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

// stopWordName is a name of stop words only, so it holds no term; index makes it distinct from the other names of a ranking, except for a duplicated document.
var stopWordList = []string{"a", "an", "and", "are", "as", "at", "be", "by", "for", "from", "in", "is", "it", "of", "on", "or", "that", "the", "this", "to", "with"}

func stopWordName(rng *rand.Rand, index int) string {
	words := make([]string, 0, 3)
	for index > 0 || len(words) == 0 {
		words = append(words, stopWordList[index%len(stopWordList)])
		index /= len(stopWordList)
	}
	return strings.Join(words, []string{"_", "__", "-"}[rng.IntN(3)])
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
			documents = append(documents, docInput{Name: stopWordName(rng, i), Text: randomText(rng, rng.IntN(26))})
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

func sameSet(a, b []string) bool {
	a, b = slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b))
	return slices.Equal(a, b)
}

var nonASCIICharacter = regexp.MustCompile(`[^\x00-\x7f]`)

func TestTokenizeAndRankDocumentsMatchPiOnSeededInputs(t *testing.T) {
	// D217 (Pi 79006f06, #11037): the pinned Pi splits a term at every character outside ASCII, PiG keeps the words of every script. Every such character becomes a space before both sides see
	// the probes, so they are compared where the two agree; TestTokenizeMatchesPiOnEveryScriptClass pins the carried tokenizer with Pi's own answers.
	input, err := json.Marshal(searchOps())
	if err != nil {
		t.Fatal(err)
	}
	input = nonASCIICharacter.ReplaceAll(input, []byte(" "))
	var ops []searchOp
	if err := json.Unmarshal(input, &ops); err != nil {
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
			got = searchtext.Tokenize(op.Text)
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
			distinct := map[string]bool{}
			for _, term := range searchtext.Tokenize(op.Query) {
				distinct[term] = true
			}
			switch {
			case len(distinct) <= 1:
				if len(pi[i]) > 1 {
					ranked++
				}
				if !slices.Equal(got, pi[i]) {
					t.Errorf("RankDocuments(%q, %+v, %d) = %q, Pi %q", op.Query, op.Documents, op.Limit, got, pi[i])
				}
			case len(pi[i]) < op.Limit:
				if !sameSet(got, pi[i]) {
					t.Errorf("RankDocuments(%q, %+v, %d) matches %q, Pi %q", op.Query, op.Documents, op.Limit, got, pi[i])
				}
			}
		}
	}
	// The corpus has to reach real rankings, or it would pass on empty answers.
	if ranked < 50 || tokens < 500 {
		t.Errorf("only %d rankings of two or more names and %d non-empty tokenizations reached the comparison", ranked, tokens)
	}
}
