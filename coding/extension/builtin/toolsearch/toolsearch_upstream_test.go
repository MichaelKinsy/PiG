package toolsearch_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin/toolsearch"
)

// Ports packages/coding-agent/test/tool-search.test.ts (v1.0.0): the tokenize, Bm25Ranker and tool_search description
// cases. The codemode description catalog cases of the same file are in ../codemode/description_upstream_test.go.

func info(name, description, properties string) extension.ToolInfo {
	if properties == "" {
		properties = "{}"
	}
	return extension.ToolInfo{Name: name, Description: description, Parameters: json.RawMessage(`{"type":"object","properties":` + properties + `}`)}
}

func names(matches []toolsearch.Match) []string {
	out := []string{}
	for _, m := range matches {
		out = append(out, m.Name)
	}
	return out
}

func TestTokenizeSplitsCamelCaseAndSnakeCaseDropsStopWordsAndFoldsPlurals(t *testing.T) {
	for input, want := range map[string][]string{
		"listIssues for the GitHub_repo": {"list", "issue", "git", "hub", "repo"},
		"searches queries HTTPServer":    {"search", "query", "http", "server"},
	} {
		if got := toolsearch.Tokenize(input); !reflect.DeepEqual(got, want) {
			t.Errorf("Tokenize(%q) = %q, want %q", input, got, want)
		}
	}
}

func rankerDocuments() []toolsearch.Document {
	tools := []extension.ToolInfo{
		info("mcp__github__list_issues", "List issues in a repository.", `{"state":{"type":"string","description":"open or closed"}}`),
		info("mcp__github__create_pull_request", "Open a pull request.", ""),
		info("mcp__linear__search_issues", "Search Linear issues by text.", ""),
		info("mcp__docs__search", "Search the documentation.", ""),
	}
	documents := make([]toolsearch.Document, len(tools))
	for i, tool := range tools {
		documents[i] = toolsearch.CreateDocument(tool, nil)
	}
	return documents
}

func TestBm25RankerRanksByTermRelevanceAndRespectsTheLimit(t *testing.T) {
	ranker, documents := toolsearch.NewBm25Ranker(), rankerDocuments()
	if got, want := names(ranker.Rank("issue", documents, 8)), []string{"mcp__linear__search_issues", "mcp__github__list_issues"}; !reflect.DeepEqual(got, want) {
		t.Errorf("issue: %q, want %q", got, want)
	}
	if got := ranker.Rank("pull requests", documents, 8); len(got) == 0 || got[0].Name != "mcp__github__create_pull_request" {
		t.Errorf("pull requests: %+v", got)
	}
	if got := ranker.Rank("search", documents, 1); len(got) != 1 {
		t.Errorf("search limit 1: %+v", got)
	}
	// Property names and descriptions are searchable.
	if got, want := names(ranker.Rank("closed", documents, 8)), []string{"mcp__github__list_issues"}; !reflect.DeepEqual(got, want) {
		t.Errorf("closed: %q, want %q", got, want)
	}
}

func TestBm25RankerReturnsNothingForUnknownOrEmptyQueries(t *testing.T) {
	ranker, documents := toolsearch.NewBm25Ranker(), rankerDocuments()
	for _, query := range []string{"kubernetes", "the", "tickets"} { // "tickets": known v1 limit, no synonyms.
		if got := ranker.Rank(query, documents, 8); len(got) != 0 {
			t.Errorf("Rank(%q) = %+v, want none", query, got)
		}
	}
}

func TestBm25RankerIncludesTheNamespaceInTheSearchText(t *testing.T) {
	document := toolsearch.CreateDocument(info("mcp__x__run", "Run it.", ""), &extension.ToolNamespace{Name: "mcp__x", Description: "Kubernetes cluster tools"})
	got := toolsearch.NewBm25Ranker().Rank("kubernetes", []toolsearch.Document{document}, 8)
	if len(got) != 1 || got[0].Name != "mcp__x__run" || got[0].Score <= 0 {
		t.Fatalf("Rank = %+v", got)
	}
}

// agent-session-mcp.test.ts (0.99.2) `expect(description("tool_search")).toBe(TOOL_SEARCH_DESCRIPTION)`: the description
// lists neither the searchable tools nor their namespaces, so it stays the same while MCP servers connect. The upstream
// "tool_search description lists the sources" case is gone.
func TestToolSearchDescriptionDoesNotListTheSearchableSources(t *testing.T) {
	want := "# Tool discovery\n\nSearches over deferred tool metadata with BM25 and exposes matching tools for the next model call.\n\nSome of the tools, such as tools of MCP servers, may not have been provided to you upfront, and you should use this tool (`tool_search`) to search for the required tools. For MCP tool discovery, always use `tool_search`."
	if toolsearch.ToolSearchDescription != want {
		t.Errorf("ToolSearchDescription:\n%q\nwant\n%q", toolsearch.ToolSearchDescription, want)
	}
	definition := toolsearch.Definition()
	if definition.Description != toolsearch.ToolSearchDescription {
		t.Errorf("definition description = %q", definition.Description)
	}
	if definition.PrepareLoadout != nil {
		t.Error("the tool still rewrites its description from the loadout")
	}
}

// createToolSearchDocument (tool.ts, 0.99.2) searches the namespace with its description and instructions.
func TestBm25RankerIncludesTheNamespaceInstructionsInTheSearchText(t *testing.T) {
	document := toolsearch.CreateDocument(info("mcp__x__run", "Run it.", ""), &extension.ToolNamespace{Name: "mcp__x", Instructions: "Kubernetes cluster tools"})
	got := toolsearch.NewBm25Ranker().Rank("kubernetes", []toolsearch.Document{document}, 8)
	if len(got) != 1 || got[0].Name != "mcp__x__run" || got[0].Score <= 0 {
		t.Fatalf("Rank = %+v", got)
	}
}
