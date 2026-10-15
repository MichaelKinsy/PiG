package coding

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/sessionentry"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func compactionStampEntry(t *testing.T, id, typ, timestamp string) icodingagent.SessionEntry {
	t.Helper()
	raw := json.RawMessage(`{"type":"` + typ + `","id":"` + id + `","parentId":null,"timestamp":"` + timestamp + `","summary":"s","firstKeptEntryId":"k","tokensBefore":1}`)
	var base icodingagent.SessionEntryBase
	if err := json.Unmarshal(raw, &base); err != nil {
		t.Fatal(err)
	}
	return sessionentry.DecodeSessionEntry(raw)
}

// upstream: session-manager.ts:372-379 getLatestCompactionEntry takes the newest compaction entry, whatever its timestamp: an unparsable newest timestamp does not fall back to an older compaction.
func TestLatestCompactionTimestampUsesTheNewestEntryOnly(t *testing.T) {
	older := compactionStampEntry(t, "c1", "compaction", "2025-01-01T00:00:00.500Z")
	newer := compactionStampEntry(t, "c2", "compaction", "2025-01-02T00:00:00Z")
	message := compactionStampEntry(t, "m", "message", "2025-01-03T00:00:00Z")
	if got, ok := latestCompactionTimestamp(nil); ok || got != 0 {
		t.Fatalf("no entries = %d, %v", got, ok)
	}
	if got, ok := latestCompactionTimestamp([]icodingagent.SessionEntry{older, message}); !ok || got != 1735689600500 {
		t.Fatalf("one compaction = %d, %v", got, ok)
	}
	if got, ok := latestCompactionTimestamp([]icodingagent.SessionEntry{older, newer, message}); !ok || got != 1735776000000 {
		t.Fatalf("newest compaction = %d, %v", got, ok)
	}
	broken := compactionStampEntry(t, "c3", "compaction", "not-a-date")
	if got, ok := latestCompactionTimestamp([]icodingagent.SessionEntry{older, broken}); ok {
		t.Fatalf("an unparsable newest timestamp = %d, want none (no fallback to an older compaction)", got)
	}
	// new Date(...) reads more than RFC 3339: a space instead of T and an RFC 2822 date are valid for V8.
	spaced := compactionStampEntry(t, "c4", "compaction", "2025-01-02 00:00:00Z")
	if got, ok := latestCompactionTimestamp([]icodingagent.SessionEntry{spaced}); !ok || got != 1735776000000 {
		t.Fatalf("a timestamp with a space = %d, %v", got, ok)
	}
}
