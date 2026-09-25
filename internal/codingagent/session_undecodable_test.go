package codingagent

import (
	"encoding/json"
	"testing"
)

// Entries this build cannot decode are dropped from BuildContext, so the
// rebuilt conversation silently loses turns. The count must be observable so
// the loss can be reported instead of surfacing later as a model that has
// forgotten its own tool calls.
func TestUndecodableEntriesAreCountedAndOmitted(t *testing.T) {
	sess := NewSession("s", t.TempDir())
	entries := []string{
		`{"type":"message","id":"m1","parentId":null,"timestamp":"","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}`,
		`{"type":"message","id":"m2","parentId":"m1","timestamp":"","message":{"content":[{"type":"text","text":"no role"}]}}`,
		`{"type":"message","id":"m3","parentId":"m2","timestamp":"","message":{"role":"assistant","content":[{"type":"text","text":"bye"}]}}`,
	}
	for _, e := range entries {
		if err := sess.AppendEntry(json.RawMessage(e)); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	if n := sess.UndecodableCount(); n != 0 {
		t.Fatalf("count before any walk = %d, want 0 (counted lazily)", n)
	}

	ctx := sess.BuildContext(sess.LeafID())
	if len(ctx) != 2 {
		t.Fatalf("BuildContext = %d messages, want 2 (the undecodable one is dropped)", len(ctx))
	}
	if n := sess.UndecodableCount(); n != 1 {
		t.Fatalf("UndecodableCount = %d, want 1", n)
	}

	// Counting is by distinct entry, so repeated walks do not inflate it.
	sess.BuildContext(sess.LeafID())
	sess.BuildContext(sess.LeafID())
	if n := sess.UndecodableCount(); n != 1 {
		t.Fatalf("UndecodableCount after repeat walks = %d, want 1", n)
	}
}

// A fully decodable session reports zero, so the warning never fires on a
// healthy transcript.
func TestUndecodableCountZeroOnCleanSession(t *testing.T) {
	sess := NewSession("s", t.TempDir())
	for _, e := range []string{
		`{"type":"message","id":"m1","parentId":null,"timestamp":"","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}`,
		`{"type":"message","id":"m2","parentId":"m1","timestamp":"","message":{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"bash","arguments":{"command":"true"}}]}}`,
		`{"type":"message","id":"m3","parentId":"m2","timestamp":"","message":{"role":"toolResult","toolCallId":"c1","toolName":"bash","content":[{"type":"text","text":"ok"}],"isError":false}}`,
	} {
		if err := sess.AppendEntry(json.RawMessage(e)); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if got := len(sess.BuildContext(sess.LeafID())); got != 3 {
		t.Fatalf("BuildContext = %d, want 3", got)
	}
	if n := sess.UndecodableCount(); n != 0 {
		t.Fatalf("UndecodableCount = %d, want 0 on a clean session", n)
	}
}
