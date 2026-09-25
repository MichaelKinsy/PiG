package codingagent

import (
	"strings"
	"testing"
)

// messageFor must memoize a failed parse. Entries whose content-block
// discriminator this build does not accept are otherwise re-scanned in full by
// every traversal, and /tree walks the transcript several times per open (row
// formatter pre-walk, child suppression, filter tags) plus once more per
// filter or fold. On a long session that turned each keystroke into a re-parse
// of the whole file.
func TestMessageForCachesUnparseableEntry(t *testing.T) {
	sess := NewSession("sess-test", t.TempDir())

	// A structurally invalid message: AgentMessage requires a non-empty role,
	// and no role can be added that would make this decode.
	//
	// An unknown content-block discriminator no longer works as the fixture.
	// The decoder now keeps an unrecognized block as raw text rather than
	// discarding the message around it, because rejecting the provider-spelled
	// "tool_use" and "tool_result" blocks made 54% of a real session render as
	// "[<unknown:message>]".
	raw := []byte(`{"type":"message","id":"e1","timestamp":"2026-01-01T00:00:00Z",` +
		`"message":{"content":[{"type":"text","text":"no role"}]}}`)
	entry := SessionEntry{
		Base: SessionEntryBase{Type: "message", ID: "e1", Timestamp: "2026-01-01T00:00:00Z"},
		raw:  raw,
	}

	if _, ok := entry.AsMessage(); ok {
		t.Fatal("fixture must be unparseable for this test to mean anything")
	}

	if _, ok := sess.messageFor(entry); ok {
		t.Fatal("messageFor reported success for an unparseable entry")
	}
	sess.msgMu.Lock()
	cached, hit := sess.msgCache["e1"]
	sess.msgMu.Unlock()
	if !hit {
		t.Fatal("failed parse was not memoized; every later walk re-parses it")
	}
	if cached.ok {
		t.Fatal("memoized outcome must record the failure, not a success")
	}

	// Repeat calls keep reporting failure from the cache.
	for range 3 {
		if _, ok := sess.messageFor(entry); ok {
			t.Fatal("cached failure must keep reporting failure")
		}
	}
}

// A parseable entry still memoizes its decoded value and reports success.
func TestMessageForCachesParsedEntry(t *testing.T) {
	sess := NewSession("sess-test", t.TempDir())
	raw := []byte(`{"type":"message","id":"e2","timestamp":"2026-01-01T00:00:00Z",` +
		`"message":{"role":"assistant","content":[{"type":"text","text":"hello"}]}}`)
	entry := SessionEntry{
		Base: SessionEntryBase{Type: "message", ID: "e2", Timestamp: "2026-01-01T00:00:00Z"},
		raw:  raw,
	}

	me, ok := sess.messageFor(entry)
	if !ok {
		t.Fatalf("expected a parseable entry to decode")
	}
	if me.Message.Assistant == nil {
		t.Fatal("assistant variant lost")
	}
	sess.msgMu.Lock()
	cached, hit := sess.msgCache["e2"]
	sess.msgMu.Unlock()
	if !hit || !cached.ok {
		t.Fatalf("successful parse not memoized (hit=%v ok=%v)", hit, cached.ok)
	}
	again, ok := sess.messageFor(entry)
	if !ok || again.Message.Assistant == nil {
		t.Fatal("cached success must keep reporting the decoded message")
	}
	if !strings.Contains(string(entry.raw), "hello") {
		t.Fatal("fixture sanity")
	}
}
