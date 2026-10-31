// Ports packages/durable/src/search.ts and packages/durable/test/search.test.ts. The tokenize and length-normalization expectations are values Pi's
// tokenize and rankDocuments return for the same inputs (run in Node).

package search

// pi: packages/durable/src/search.ts

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/searchtext"
)

// Ports packages/durable/test/search.test.ts "tokenize" (Pi 79006f06, #11037) and the pinned Pi's expectations for plain text.
func TestTokenizeMatchesPi(t *testing.T) {
	for text, want := range map[string][]string{
		"searchTools getHTTPResponse issues searches boxes classes buses a an the": {"search", "tool", "get", "http", "response", "issue", "search", "box", "class", "buse"},
		"ABCDef2Go x_y-z.w":              {"abc", "def2", "go", "x", "y", "z", "w"},
		"dries tries":                    {"dry", "try"},
		"":                               {},
		"listIssues for the GitHub_repo": {"list", "issue", "git", "hub", "repo"},
		"searches queries HTTPServer":    {"search", "query", "http", "server"},
		"repo.issues/search:queries":     {"repo", "issue", "search", "query"},
		"Éléments cafés":                 {"éléments", "cafés"},
		"搜索GitHub_repoIssues résumé cafés": {"搜索", "git", "hub", "repo", "issue", "résumé", "cafés"},
		"CAFÉ cafe\u0301":                  {"café", "café"},
		" \t，。!? 😀 ":                       {},
	} {
		if got := searchtext.Tokenize(text); !slices.Equal(got, want) {
			t.Errorf("tokenize(%q) = %v, want %v", text, got, want)
		}
	}
}

func doc(name, text string, namespaceText ...string) Document {
	d := Document{Name: name, Text: text}
	if len(namespaceText) > 0 {
		d.NamespaceText = namespaceText[0]
	}
	return d
}

func checkRank(t *testing.T, query string, documents []Document, limit int, want ...string) {
	t.Helper()
	if got := RankDocuments(query, documents, limit); !slices.Equal(got, append([]string{}, want...)) {
		t.Errorf("RankDocuments(%q, %d) = %v, want %v", query, limit, got, want)
	}
}

// Ports packages/durable/test/search.test.ts "rankDocuments" (Pi c7372af4, #11631).
func TestRankDocumentsMatchesPi(t *testing.T) {
	documents := []Document{
		doc("mcp__github__list_issues", "List issues in a repository. state open or closed"),
		doc("mcp__github__create_pull_request", "Open a pull request."),
		doc("mcp__linear__search_issues", "Search Linear issues by text."),
		doc("mcp__docs__search", "Search the documentation."),
	}
	t.Run("ranks by term relevance and respects the limit", func(t *testing.T) {
		checkRank(t, "issue", documents, 8, "mcp__linear__search_issues", "mcp__github__list_issues")
		if got := RankDocuments("pull requests", documents, 8); len(got) == 0 || got[0] != "mcp__github__create_pull_request" {
			t.Errorf("pull requests = %v", got)
		}
		if got := RankDocuments("search", documents, 1); len(got) != 1 {
			t.Errorf("search limit 1 = %v", got)
		}
		checkRank(t, "closed", documents, 8, "mcp__github__list_issues")
	})
	t.Run("returns nothing for unknown or empty queries", func(t *testing.T) {
		checkRank(t, "kubernetes", documents, 8)
		checkRank(t, "the", documents, 8)
		checkRank(t, "issue", documents, 0)
		checkRank(t, "issue", documents, -1)
		checkRank(t, "issue", nil, 8)
	})
	t.Run("searches the namespace text", func(t *testing.T) {
		checkRank(t, "kubernetes", []Document{doc("mcp__x__run", "Run it.", "mcp__x Kubernetes cluster tools")}, 8, "mcp__x__run")
	})
	t.Run("ranks a tool first when the query is its name", func(t *testing.T) {
		named := []Document{
			doc("mcp__app__queue_read", "Read a queued message of this thread."),
			doc("mcp__dev-app__thread_read", "Read durable state and a paginated timeline from any thread. The default view returns user messages, assistant messages and plans. Continue with afterPosition until hasMore is false."),
		}
		for _, query := range []string{"thread_read", "dev_app__thread_read", "mcp__dev-app__thread_read", "Thread_Read"} {
			checkRank(t, query, named, 1, "mcp__dev-app__thread_read")
		}
		// Partial names still rank by BM25.
		checkRank(t, "read", named, 1, "mcp__app__queue_read")
	})
	t.Run("ranks a name match above mentions in descriptions", func(t *testing.T) {
		ci := []Document{
			doc("mcp__ci__status", "Deploy status: last deploy, deploy log, deploy errors."),
			doc("mcp__ci__deploy_release", "Start a release of the current branch to an environment, run the migrations, switch traffic over, and wait until the new version is live and healthy."),
		}
		checkRank(t, "deploy", ci, 1, "mcp__ci__deploy_release")
	})
	t.Run("ranks a tool first when the query contains its whole name", func(t *testing.T) {
		peekaboo := "mcp__peekaboo Peekaboo controls macOS apps by app name."
		named := []Document{
			doc("mcp__peekaboo__see", "Capture the app window of the peekaboo app and describe the app.", peekaboo),
			doc("mcp__peekaboo__menu", "Click a menu item of an app.", peekaboo),
			doc("mcp__peekaboo__app", "Launch, quit, relaunch, focus, hide, or switch applications. Targets an application by name, bundle identifier, or process ID, and can wait until it is ready.", peekaboo),
			doc("mcp__app__queue_read", "Read a queued message of this thread."),
			doc("mcp__dev-app__thread_read", "Read durable state and a paginated timeline from any thread. The default view returns user messages, assistant messages and plans."),
		}
		checkRank(t, "peekaboo app", named, 1, "mcp__peekaboo__app")
		checkRank(t, "open the peekaboo app tool", named, 1, "mcp__peekaboo__app")
		checkRank(t, "read thread_read state", named, 1, "mcp__dev-app__thread_read")
		// Without the whole name, mentions in descriptions decide.
		checkRank(t, "describe window", named, 1, "mcp__peekaboo__see")
	})
	t.Run("ranks a tool matching more query terms first, whatever the scores", func(t *testing.T) {
		peekaboo := []Document{
			doc("mcp__peekaboo__app", "Launch, quit, focus, or switch applications by name or bundle identifier."),
			doc("mcp__peekaboo__see", "Capture the app window and describe the UI elements of the app."),
			doc("mcp__peekaboo__window", "Move, resize, or close a window of an app."),
			doc("mcp__peekaboo__image", "Take a screenshot of the screen, an app, or a window."),
		}
		// `window` and `app` are whole names, but each covers one of the two terms.
		checkRank(t, "screenshot window", peekaboo, 1, "mcp__peekaboo__image")
		checkRank(t, "describe app", peekaboo, 1, "mcp__peekaboo__see")
		// An exact name still ranks first.
		checkRank(t, "window", peekaboo, 1, "mcp__peekaboo__window")
	})
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
	documents := []Document{doc("long2", "alpha alpha "+filler(14)), doc("short1", "alpha beta"), doc("mid1", "alpha "+filler(5))}
	if got, want := RankDocuments("alpha", documents, 3), []string{"short1", "mid1", "long2"}; !slices.Equal(got, want) {
		t.Errorf("rank = %v, want %v", got, want)
	}
}

// The two documents tie in JavaScript, which rounds k1 * (1 - b + b * length / averageLength) before adding the count, so
// they keep document order; a fused multiply-add rounds once and breaks the tie (values from Pi's rankDocuments in Node).
func TestRankDocumentsRoundsEveryOperationLikeJavaScript(t *testing.T) {
	documents := []Document{doc("once", "alpha "+filler(3)), doc("thrice", "alpha alpha alpha "+filler(15)), doc("none", filler(5))}
	if got, want := RankDocuments("alpha", documents, 3), []string{"once", "thrice"}; !slices.Equal(got, want) {
		t.Errorf("rank = %v, want %v", got, want)
	}
}
