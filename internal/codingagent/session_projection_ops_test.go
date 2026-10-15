package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

func projectionBytes(t *testing.T, p SessionProjection) []byte {
	t.Helper()
	ids := make([]string, len(p.Entries))
	counts := make([]int, len(p.Entries))
	for i, entry := range p.Entries {
		ids[i], counts[i] = entry.SourceEntry.Base().ID, len(entry.Messages)
	}
	out, err := json.Marshal(struct {
		Messages []agent.AgentMessage
		IDs      []string
		Counts   []int
		Thinking string
		Model    *SessionContextModel
	}{p.Messages, ids, counts, p.ThinkingLevel, p.Model})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// requireViewIsFresh fails when the cached view differs from a projection built from the path.
func requireViewIsFresh(t *testing.T, s *Session, after string) {
	t.Helper()
	got, want := projectionBytes(t, s.BuildSessionProjectionView()), projectionBytes(t, freshProjectionView(s))
	if !bytes.Equal(got, want) {
		t.Fatalf("after %s the cached view differs from a fresh projection:\n got %s\nwant %s", after, got, want)
	}
}

// Every operation that changes what the branch projects, and a call between each, must leave the cached view equal to a projection built from the path: appends extend the cache, and compaction, /tree, /fork, a context edit and a model change must each show.
func TestProjectionViewCacheEqualsAFreshProjectionAfterEveryOperation(t *testing.T) {
	s := NewSession("cache", "/project")
	var ids []string
	add := func(message agent.AgentMessage) {
		t.Helper()
		id, err := s.AppendMessage(message)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	for i := range 4 {
		add(mkUserMsg(fmt.Sprintf("ask %d", i)))
		requireViewIsFresh(t, s, "a user message")
		add(mkAssistantMsg(fmt.Sprintf("answer %d", i)))
		requireViewIsFresh(t, s, "an assistant message")
	}
	if _, err := s.AppendThinkingLevelChange("high"); err != nil {
		t.Fatal(err)
	}
	requireViewIsFresh(t, s, "a thinking level change")
	if _, err := s.AppendModelChange("other-provider", "other-model"); err != nil {
		t.Fatal(err)
	}
	requireViewIsFresh(t, s, "a model change")
	if view := s.BuildSessionProjectionView(); view.Model == nil || view.Model.ModelID != "other-model" || view.ThinkingLevel != "high" {
		t.Fatalf("settings after the changes: %+v %q", view.Model, view.ThinkingLevel)
	}
	if _, err := s.AppendContextEdit(ids[2], &ContextEditReplacement{Content: json.RawMessage(`"edited"`)}); err != nil {
		t.Fatal(err)
	}
	requireViewIsFresh(t, s, "a context edit")
	if _, err := s.AppendContextEdit(ids[3], nil); err != nil {
		t.Fatal(err)
	}
	requireViewIsFresh(t, s, "an omitting context edit")
	add(mkUserMsg("after the edits"))
	requireViewIsFresh(t, s, "a message after the edits")
	if _, err := s.AppendCompaction("summary", ids[4], 100, nil, false, nil); err != nil {
		t.Fatal(err)
	}
	requireViewIsFresh(t, s, "a compaction")
	add(mkAssistantMsg("after the compaction"))
	requireViewIsFresh(t, s, "a message after the compaction")
	if err := s.SetLeafID(&ids[1]); err != nil {
		t.Fatal(err)
	}
	requireViewIsFresh(t, s, "moving the leaf (/tree)")
	add(mkUserMsg("on the other branch"))
	requireViewIsFresh(t, s, "a message on the other branch")
	if err := s.Branch(ids[3]); err != nil {
		t.Fatal(err)
	}
	requireViewIsFresh(t, s, "a fork")
	add(mkUserMsg("on the fork"))
	requireViewIsFresh(t, s, "a message on the fork")
	s.ResetLeaf()
	requireViewIsFresh(t, s, "resetting the leaf")
	add(mkUserMsg("a new root"))
	requireViewIsFresh(t, s, "a message on a new root")
}

// A shared view is read-only to its callers: an append to a returned slice must not write into the cache's spare capacity, or the next extension would overwrite it.
func TestProjectionViewCacheSlicesCannotBeAppendedInPlace(t *testing.T) {
	s := NewSession("cap", "/project")
	for i := range 3 {
		if _, err := s.AppendMessage(mkUserMsg(fmt.Sprintf("ask %d", i))); err != nil {
			t.Fatal(err)
		}
		s.BuildSessionProjectionView()
	}
	view := s.BuildSessionProjectionView()
	if cap(view.Messages) != len(view.Messages) || cap(view.Entries) != len(view.Entries) {
		t.Fatalf("a view hands out spare capacity: messages %d/%d entries %d/%d", len(view.Messages), cap(view.Messages), len(view.Entries), cap(view.Entries))
	}
	//nolint:gocritic // appendAssign: the test appends to the view as a caller would, to prove the append copies instead of writing into the kept projection.
	mine := append(view.Messages, mkUserMsg("a caller's append"))
	if _, err := s.AppendMessage(mkUserMsg("next")); err != nil {
		t.Fatal(err)
	}
	s.BuildSessionProjectionView()
	if got := mine[len(mine)-1].User.Content; fmt.Sprint(got) != fmt.Sprint(mkUserMsg("a caller's append").User.Content) {
		t.Fatalf("the cache overwrote a caller's append: %v", got)
	}
	requireViewIsFresh(t, s, "a caller's append and an entry")
}
