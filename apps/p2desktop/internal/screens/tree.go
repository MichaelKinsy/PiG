package screens

import (
	"errors"
	"fmt"

	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	treecontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/tree/contract"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/surfacehost"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Tree is the owner's `/tree` (docs/tree.md): switching the branch a session continues in, branching from a message, and
// naming branches. Each call holds the session's `p2.tree` for its own duration (the web does the same: a reference of its
// own, since the switch reaches the session row before its result). The catalog follows by itself: the worker pushes the
// new main conversation and branches before it answers.
type Tree struct{ h *surfacehost.Host }

// NewTree is the tree actions over the host's server.
func NewTree(h *surfacehost.Host) *Tree { return &Tree{h: h} }

func (t *Tree) call(session, method string, args delta.JsonValue) (*delta.JsonObject, error) {
	scope := t.h.Scope(func() {})
	defer scope.Dispose()
	ref, err := scope.Hold(treecontract.SessionTree, delta.JsonObjectOf("session", session))
	if err != nil {
		return nil, err
	}
	result, err := ref.Call(method, args, t.h.Context())
	if err != nil {
		return nil, err
	}
	object, _ := result.(*delta.JsonObject)
	return object, nil
}

// Navigate continues the session at message entry (after an answer, before a user message): the conversation it now
// continues in, and for a user message its text, for the composer ("" for an answer).
func (t *Tree) Navigate(session string, entry int) (conversation int, text string, err error) {
	result, err := t.call(session, "navigate", delta.JsonObjectOf("entry", float64(entry)))
	if err != nil {
		return 0, "", err
	}
	if result == nil {
		return 0, "", errors.New("the tree answered nothing")
	}
	number, _ := result.Value("conversation").(float64)
	text, _ = result.Value("text").(string)
	return int(number), text, nil
}

// Select continues the session at the end of the branch conversation.
func (t *Tree) Select(session string, conversation int) error {
	_, err := t.call(session, "select", delta.JsonObjectOf("conversation", float64(conversation)))
	return err
}

// Rename labels a branch; "" resets conversation 1 to the session title (other branches need a label).
func (t *Tree) Rename(session string, conversation int, label string) error {
	var value any
	if label != "" {
		value = label
	}
	_, err := t.call(session, "rename", delta.JsonObjectOf("conversation", float64(conversation), "label", value))
	return err
}

// BranchItem is a `/tree` branch under its session's row.
type BranchItem struct {
	Row    *Row
	Branch sessionscontract.Branch
	// Label is what the row shows: the branch's label, the session title for none.
	Label string
	// Current is the branch the session continues in.
	Current bool
}

// Branches is a session's branches as the drawer lists them, in the catalog's order (newest first); nil under two branches
// (the drawer shows them from two on) and for an archived session.
func Branches(row *Row) []BranchItem {
	if row.ArchivedAt != nil || len(row.Branches) < 2 {
		return nil
	}
	items := make([]BranchItem, len(row.Branches))
	for i, branch := range row.Branches {
		label := row.Title
		if branch.Label != nil {
			label = *branch.Label
		}
		items[i] = BranchItem{Row: row, Branch: branch, Label: label, Current: branch.Conversation == row.Main}
	}
	return items
}

// BranchCount names the toggle of a session with branches: "3 branches".
func BranchCount(row *Row) string { return fmt.Sprintf("%d branches", len(row.Branches)) }

// ExpandBranches puts the branches of each session of expanded under its row.
func ExpandBranches(items []Item, expanded map[string]bool) []Item {
	out := make([]Item, 0, len(items))
	for _, item := range items {
		out = append(out, item)
		if item.Row == nil || !expanded[item.Row.ID] {
			continue
		}
		for _, branch := range Branches(item.Row) {
			out = append(out, Item{Branch: &branch})
		}
	}
	return out
}
