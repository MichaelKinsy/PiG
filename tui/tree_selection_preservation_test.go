package tui

import (
	"slices"
	"testing"
)

// upstream: packages/coding-agent/src/modes/interactive/components/tree-selector.ts:330-427
func TestTreeSearchPreservesSelectionAcrossQueryChanges(t *testing.T) {
	useTreeKeybindings(t, nil)
	root := &searchableNode{id: "root", kids: []TreeNode{
		&searchableNode{id: "common", search: "user common", kids: []TreeNode{
			&searchableNode{id: "active-user", search: "user active 12345", kids: []TreeNode{
				&searchableNode{id: "active-assistant", search: "assistant active 12345"},
			}},
			&searchableNode{id: "old-user", search: "user old", kids: []TreeNode{
				&searchableNode{id: "old-assistant", search: "assistant old"},
			}},
		}},
	}}
	ts := NewTreeSelect("", root)
	ts.SetInitialCursor("active-assistant", "")
	for _, step := range []struct {
		key, selected string
		visible       []string
	}{
		{"12345", "active-assistant", []string{"active-user", "active-assistant"}},
		{"\x1b", "active-assistant", []string{"common", "active-user", "active-assistant", "old-user", "old-assistant"}},
		{"absent", "", nil},
		{"\x1b", "active-assistant", []string{"common", "active-user", "active-assistant", "old-user", "old-assistant"}},
		{"assistant old", "old-assistant", []string{"old-assistant"}},
		{"\x1b", "old-assistant", []string{"common", "active-user", "active-assistant", "old-user", "old-assistant"}},
	} {
		ts.HandleInput(step.key)
		assertTreeSelection(t, ts, step.selected)
		if got := rowIDs(ts); !slices.Equal(got, step.visible) {
			t.Fatalf("after %q rows = %q, want %q", step.key, got, step.visible)
		}
	}
}

// upstream: packages/coding-agent/test/tree-selector.test.ts:232-295
func TestTreeFilterSwitchingWithParentTraversal(t *testing.T) {
	useTreeKeybindings(t, nil)
	root := &fakeNodeTagged{id: "root", kids: []TreeNode{
		&fakeNodeTagged{id: "user-1", label: "hello", tags: []string{"user"}, kids: []TreeNode{
			&fakeNodeTagged{id: "asst-1", label: "hi", kids: []TreeNode{
				&fakeNodeTagged{id: "user-2", label: "active branch", tags: []string{"user"}, kids: []TreeNode{
					&fakeNodeTagged{id: "asst-2", label: "response"},
				}},
				&fakeNodeTagged{id: "user-3", label: "sibling branch", tags: []string{"user"}},
			}},
		}},
	}}
	for _, name := range []string{
		"switches to nearest visible user message when changing to user-only filter",
		"returns to nearest visible ancestor when switching back to default filter",
	} {
		t.Run(name, func(t *testing.T) {
			ts := NewTreeSelect("", root)
			ts.SetInitialCursor("asst-2", "")
			assertTreeSelection(t, ts, "asst-2")
			ts.HandleInput("\x15")
			assertTreeSelection(t, ts, "user-2")
			if name == "returns to nearest visible ancestor when switching back to default filter" {
				ts.HandleInput("\x04")
				assertTreeSelection(t, ts, "user-2")
			}
		})
	}
}

// upstream: packages/coding-agent/test/tree-selector.test.ts:381-447
func TestTreeEmptyFilterPreservation(t *testing.T) {
	useTreeKeybindings(t, nil)
	for _, tc := range []struct {
		name, leaf string
		root       TreeNode
		keys       []string
		selected   []string
	}{
		{
			name: "preserves selection when switching to empty labeled filter and back", leaf: "asst-2",
			root: &fakeNodeTagged{id: "root", kids: []TreeNode{
				&fakeNodeTagged{id: "user-1", label: "hello", tags: []string{"user"}, kids: []TreeNode{
					&fakeNodeTagged{id: "asst-1", label: "hi", kids: []TreeNode{
						&fakeNodeTagged{id: "user-2", label: "bye", tags: []string{"user"}, kids: []TreeNode{
							&fakeNodeTagged{id: "asst-2", label: "goodbye"},
						}},
					}},
				}},
			}},
			keys: []string{"\x0c", "\x04"}, selected: []string{"", "asst-2"},
		},
		{
			name: "preserves selection through multiple empty filter switches", leaf: "asst-1",
			root: &fakeNodeTagged{id: "root", kids: []TreeNode{
				&fakeNodeTagged{id: "user-1", label: "hello", tags: []string{"user"}, kids: []TreeNode{
					&fakeNodeTagged{id: "asst-1", label: "hi"},
				}},
			}},
			keys: []string{"\x0c", "\x0c", "\x0c", "\x04"}, selected: []string{"", "asst-1", "", "asst-1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := NewTreeSelect("", tc.root)
			ts.SetInitialCursor(tc.leaf, "")
			assertTreeSelection(t, ts, tc.leaf)
			for i, key := range tc.keys {
				ts.HandleInput(key)
				assertTreeSelection(t, ts, tc.selected[i])
			}
		})
	}
}

// upstream: packages/coding-agent/src/modes/interactive/components/tree-selector.ts:1039-1085
func TestTreeQueryAndFilterChangesClearFolds(t *testing.T) {
	useTreeKeybindings(t, nil)
	for _, key := range []string{"\x1b", "\x04", "\x14", "\x15", "\x0c", "\x01", "\x0f", "\x1b[111;6u"} {
		t.Run(key, func(t *testing.T) {
			ts := NewTreeSelect("", chain())
			ts.HandleInput(" ")
			ts.HandleInput("\x1b[1;5D")
			ts.HandleInput("\x1b[1;5D")
			if !ts.foldedNodes["a"] {
				t.Fatal("root did not fold")
			}
			ts.HandleInput(key)
			if len(ts.foldedNodes) != 0 {
				t.Fatalf("query/filter change retained folds: %v", ts.foldedNodes)
			}
		})
	}
}

func assertTreeSelection(t *testing.T, ts *TreeSelect, want string) {
	t.Helper()
	got := ""
	if ts.cursor >= 0 && ts.cursor < len(ts.rows) {
		got = ts.rows[ts.cursor].id
	}
	if got != want {
		t.Fatalf("selection = %q, want %q (query %q, filter %q, cursor %d, rows %q)", got, want, ts.searchQuery, ts.filterMode, ts.cursor, rowIDs(ts))
	}
}
