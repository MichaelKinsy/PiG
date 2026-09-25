package tui

import "testing"

// searchableNode is a test TreeNode that also carries searchable text
// distinct from its rendered label, so type-to-search matching can be
// exercised independently of NodeLabel.
type searchableNode struct {
	id       string
	label    string
	search   string
	kids     []TreeNode
	nodeTags []string
}

func (n *searchableNode) NodeID() string             { return n.id }
func (n *searchableNode) NodeLabel() string          { return n.label }
func (n *searchableNode) NodeChildren() []TreeNode   { return n.kids }
func (n *searchableNode) NodeFilterTags() []string   { return n.nodeTags }
func (n *searchableNode) NodeSearchableText() string { return n.search }

// Type-to-search: printable runes accrue into searchQuery and the visible
// rows are filtered to those whose searchable text contains every token.
// Mirrors upstream tree-selector.ts:390-393 and :1104-1110.
func TestTreeTypeToSearchFiltersRows(t *testing.T) {
	root := &searchableNode{id: "root", kids: []TreeNode{
		&searchableNode{id: "a", label: "A", search: "user compile the parser"},
		&searchableNode{id: "b", label: "B", search: "assistant run deploy"},
		&searchableNode{id: "c", label: "C", search: "user parser error"},
	}}
	ts := NewTreeSelect("t", root)
	if got := len(ts.rows); got != 3 {
		t.Fatalf("initial rows = %d, want 3", got)
	}

	ts.HandleInput("parse")
	idSet := map[string]bool{}
	for _, r := range ts.rows {
		idSet[r.id] = true
	}
	// "parse" matches "compile the parser" (a) and "parser error" (c).
	if !idSet["a"] || !idSet["c"] {
		t.Fatalf("after search 'parse', expected rows a and c, got %v", idSet)
	}
	if idSet["b"] {
		t.Fatalf("row b should be filtered out by 'parse', got %v", idSet)
	}

	// Multi-token and case-insensitivity: "Parser COMPILE" (upper case).
	// Start from a fresh selector so the query doesn't accumulate with the
	// single-token check above.
	ts = NewTreeSelect("t", root)
	ts.HandleInput("Parser")
	ts.HandleInput(" ")
	ts.HandleInput("COMPILE")
	if ts.searchQuery != "Parser COMPILE" {
		t.Fatalf("query = %q, want Parser COMPILE", ts.searchQuery)
	}
	idSet = map[string]bool{}
	for _, r := range ts.rows {
		idSet[r.id] = true
	}
	if !idSet["a"] {
		t.Fatalf("'parser compile' should match a, got %v", idSet)
	}
	if len(idSet) != 1 {
		t.Fatalf("'parser compile' should leave exactly one row, got %v", idSet)
	}
}

// Backspace shortens the query and Esc clears it without closing the
// picker. Mirrors upstream tree-selector.ts:1032-1035 and :1078-1082.
func TestTreeTypeToSearchBackspaceAndClear(t *testing.T) {
	root := &searchableNode{id: "root", kids: []TreeNode{
		&searchableNode{id: "a", label: "A", search: "compile parser"},
		&searchableNode{id: "b", label: "B", search: "deploy"},
	}}
	ts := NewTreeSelect("t", root)

	ts.HandleInput("pars")
	if ts.searchQuery != "pars" {
		t.Fatalf("query = %q, want pars", ts.searchQuery)
	}
	if len(ts.rows) != 1 {
		t.Fatalf("'pars' should leave exactly one row, got %d", len(ts.rows))
	}

	// Backspace removes the trailing 's'.
	ts.HandleInput("\x7f")
	if ts.searchQuery != "par" {
		t.Fatalf("after backspace query = %q, want par", ts.searchQuery)
	}
	if len(ts.rows) != 1 {
		t.Fatalf("'par' should still match one row, got %d", len(ts.rows))
	}

	// Esc clears the query, does not cancel.
	ts.HandleInput("\x1b")
	if ts.searchQuery != "" {
		t.Fatalf("after Esc query = %q, want empty", ts.searchQuery)
	}
	if ts.Done() {
		t.Fatal("Esc with an active query must clear it, not close the picker")
	}
	if len(ts.rows) != 2 {
		t.Fatalf("after clearing search all rows should return, got %d", len(ts.rows))
	}

	// A second Esc with no query cancels.
	ts.HandleInput("\x1b")
	if !ts.Cancelled() {
		t.Fatal("Esc with no query should cancel the picker")
	}
}

// Searchable text comes from NodeSearchableText, not the rendered label,
// so a query matching the label alone must not over-match when the
// node's searchable text differs.
func TestTreeTypeToSearchUsesSearchableTextNotLabel(t *testing.T) {
	root := &searchableNode{id: "root", kids: []TreeNode{
		// label is "user:", searchable text is the message body.
		&searchableNode{id: "a", label: "user: first step", search: "assistant 42 answer"},
	}}
	ts := NewTreeSelect("t", root)

	// The label contains "user:" but the searchable text does not; a
	// search for "user" over the searchable text matches nothing.
	ts.HandleInput("user")
	if len(ts.rows) != 0 {
		t.Fatalf("search 'user' should filter out row a, got %d rows", len(ts.rows))
	}

	ts = NewTreeSelect("t", root)
	ts.HandleInput("42")
	if len(ts.rows) != 1 {
		t.Fatalf("search '42' should match row a via searchable text, got %d rows", len(ts.rows))
	}
}
