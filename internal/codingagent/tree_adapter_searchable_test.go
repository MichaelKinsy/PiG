package codingagent

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// NodeSearchableText feeds /tree's type-to-search. It must expose role
// and message body text so a query finds the row even when the rendered
// prefix ("user:", "assistant:") is a colored label rather than the body.
// Mirrors upstream getSearchableText (tree-selector.ts:559-600).
func TestTreeNodeAdapterSearchableText(t *testing.T) {
	sess := NewSession("sess-test", t.TempDir())
	f := newTreeRowFormatter(sess)

	cases := []struct {
		name string
		msg  agent.AgentMessage
		want []string
	}{
		{
			name: "user body",
			msg:  agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "compile the parser"}}}},
			want: []string{"user", "compile", "parser"},
		},
		{
			name: "assistant body",
			msg:  agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "run deploy now"}}}},
			want: []string{"assistant", "deploy"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := sess.AppendEntry(MessageEntry{SessionEntryBase: SessionEntryBase{Type: "message"}, Message: tc.msg}); err != nil {
				t.Fatalf("append: %v", err)
			}
			entries := sess.Entries()
			adapter := &treeNodeAdapter{n: &SessionTreeNode{Entry: entries[len(entries)-1]}, f: f}
			text := strings.ToLower(adapter.NodeSearchableText())
			for _, w := range tc.want {
				if !strings.Contains(text, w) {
					t.Fatalf("searchable text %q missing %q", text, w)
				}
			}
		})
	}
}

// NodeSearchableText must never leak ANSI prefix formatting the rendered
// label carries, so search matches body text, not colored prefixes.
func TestTreeNodeAdapterSearchableTextHasNoAnsi(t *testing.T) {
	sess := NewSession("sess-test", t.TempDir())
	f := newTreeRowFormatter(sess)
	msg := agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "hello world"}}}}
	if err := sess.AppendEntry(MessageEntry{SessionEntryBase: SessionEntryBase{Type: "message"}, Message: msg}); err != nil {
		t.Fatalf("append: %v", err)
	}
	entries := sess.Entries()
	adapter := &treeNodeAdapter{n: &SessionTreeNode{Entry: entries[0]}, f: f}
	if text := adapter.NodeSearchableText(); strings.Contains(text, "\x1b[") {
		t.Fatalf("searchable text leaked ANSI: %q", text)
	}
}

// Ports the v0.87 tree-selector.ts context_edit delta: a context edit is a
// settings entry (hidden in the default filter) and searchable by its mode
// and target. A label entry is searchable by its label text.
func TestTreeNodeAdapterContextEditAndLabelEntries(t *testing.T) {
	f := newTreeRowFormatter(nil)
	for _, tc := range []struct {
		name       string
		entry      map[string]any
		searchable string
		settings   bool
	}{
		{"context edit omit", map[string]any{"type": "context_edit", "id": "ce1", "targetId": "u1", "replacement": nil}, "context edit omit u1", true},
		{"context edit replace", map[string]any{"type": "context_edit", "id": "ce2", "targetId": "u2", "replacement": map[string]any{"content": "x"}}, "context edit replace u2", true},
		{"label", map[string]any{"type": "label", "id": "l1", "targetId": "u1", "label": "milestone"}, "label milestone", true},
	} {
		adapter := &treeNodeAdapter{n: &SessionTreeNode{Entry: mustEntry(t, tc.entry)}, f: f}
		if got := adapter.NodeSearchableText(); got != tc.searchable {
			t.Errorf("%s: searchable text = %q, want %q", tc.name, got, tc.searchable)
		}
		if got := slices.Contains(adapter.NodeFilterTags(), "settings"); got != tc.settings {
			t.Errorf("%s: settings tag = %v, want %v", tc.name, got, tc.settings)
		}
	}
}
