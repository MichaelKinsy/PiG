// Ports packages/durable/src/codemode/search.ts's behavior; Pi's tests reach it only through searchTools(), so the
// expectations here are the values Pi's tokenize and RankDocuments return for the same inputs (run in Node).

package search

// pi: packages/durable/src/search.ts

import (
	"slices"
	"strings"
	"testing"
)

func TestTokenizeMatchesPi(t *testing.T) {
	for text, want := range map[string][]string{
		"searchTools getHTTPResponse issues searches boxes classes buses a an the": {"search", "tool", "get", "http", "response", "issue", "search", "box", "class", "buse"},
		"ABCDef2Go x_y-z.w": {"abc", "def2", "go", "x", "y", "z", "w"},
		"dries tries":       {"dry", "try"},
		"":                  {},
	} {
		if got := tokenize(text); !slices.Equal(got, want) {
			t.Errorf("tokenize(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestRankDocumentsMatchesPi(t *testing.T) {
	documents := []Document{
		{"mcp__github__list_issues", "mcp__github__list_issues List issues of a repository. state labels"},
		{"read", "read Read a file from disk. path offset limit"},
		{"write", "write Write a file to disk. path content"},
		{"mcp__github__search_issues", "mcp__github__search_issues Search issues and pull requests across repositories"},
		{"bash", "bash Run a shell command. command timeout"},
		{"empty", ""},
	}
	for _, c := range []struct {
		query string
		limit int
		want  []string
	}{
		{"issues", 8, []string{"mcp__github__list_issues", "mcp__github__search_issues"}},
		{"read file", 8, []string{"read", "write"}},
		{"file disk", 1, []string{"write"}},
		{"the of a", 8, []string{}},
		{"SearchIssues repos", 2, []string{"mcp__github__search_issues", "mcp__github__list_issues"}},
		{"shell command", 8, []string{"bash"}},
		{"zzz", 8, []string{}},
		{"issue", 0, []string{}},
	} {
		if got := RankDocuments(c.query, documents, c.limit); !slices.Equal(got, c.want) {
			t.Errorf("RankDocuments(%q, %d) = %v, want %v", c.query, c.limit, got, c.want)
		}
	}
}

// filler is n distinct words that are neither stop words nor stemmed.
func filler(n int) string {
	var words []string
	for i := range n {
		words = append(words, "w"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	return strings.Join(words, " ")
}

// The length normalization (b 0.75): a document that repeats the term but is long ranks below a short one and a middle one
// that has it once; with b 0.5 the long one would outrank the middle one (values from Pi's RankDocuments in Node).
func TestRankDocumentsNormalizesByDocumentLength(t *testing.T) {
	documents := []Document{{"long2", "alpha alpha " + filler(14)}, {"short1", "alpha beta"}, {"mid1", "alpha " + filler(5)}}
	if got, want := RankDocuments("alpha", documents, 3), []string{"short1", "mid1", "long2"}; !slices.Equal(got, want) {
		t.Errorf("rank = %v, want %v", got, want)
	}
}

// The two documents tie in JavaScript, which rounds k1 * (1 - b + b * length / averageLength) before adding the count, so
// they keep document order; a fused multiply-add rounds once and breaks the tie (values from Pi's rankDocuments in Node).
func TestRankDocumentsRoundsEveryOperationLikeJavaScript(t *testing.T) {
	documents := []Document{{"once", "alpha " + filler(3)}, {"thrice", "alpha alpha alpha " + filler(15)}, {"none", filler(5)}}
	if got, want := RankDocuments("alpha", documents, 3), []string{"once", "thrice"}; !slices.Equal(got, want) {
		t.Errorf("rank = %v, want %v", got, want)
	}
}
