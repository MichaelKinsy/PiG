package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// A message entry whose record does not decode into a MessageEntry is decided once, when the record is read: it is a RawEntry, so every
// traversal (/tree walks the transcript several times per open: row formatter pre-walk, child suppression, filter tags) answers from the entry's
// type and never parses the record again. messageFor counts it once however often it is asked.
func TestMessageForReportsAnUnparseableEntryWithoutParsingItAgain(t *testing.T) {
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
	entry := sessionentry.DecodeSessionEntry(raw)

	if _, ok := entry.(MessageEntry); ok {
		t.Fatal("fixture must be unparseable for this test to mean anything")
	}
	if _, ok := entry.(RawEntry); !ok {
		t.Fatalf("an unparseable message entry is %T, want the RawEntry that holds its record", entry)
	}
	if entry.Base().Type != "message" || entry.Base().ID != "e1" {
		t.Fatalf("base = %+v, want the message entry's type and id", entry.Base())
	}

	for range 3 {
		if _, ok := sess.messageFor(entry); ok {
			t.Fatal("messageFor reported success for an unparseable entry")
		}
	}
	if n := sess.UndecodableCount(); n != 1 {
		t.Fatalf("UndecodableCount = %d, want the entry counted once", n)
	}
}

// A parseable entry is a MessageEntry from the moment it is read and messageFor hands out its decoded message.
func TestMessageForReportsAParsedEntry(t *testing.T) {
	sess := NewSession("sess-test", t.TempDir())
	raw := []byte(`{"type":"message","id":"e2","timestamp":"2026-01-01T00:00:00Z",` +
		`"message":{"role":"assistant","content":[{"type":"text","text":"hello"}]}}`)
	entry := sessionentry.DecodeSessionEntry(raw)

	me, ok := sess.messageFor(entry)
	if !ok {
		t.Fatalf("expected a parseable entry to decode")
	}
	if me.Message.Assistant == nil {
		t.Fatal("assistant variant lost")
	}
	if me.ID != "e2" || string(entry.Raw()) != string(raw) {
		t.Fatalf("entry = %+v, want id e2 and the record it was read from", me)
	}
	if n := sess.UndecodableCount(); n != 0 {
		t.Fatalf("UndecodableCount = %d, want 0", n)
	}
}
