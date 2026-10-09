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
	if got := latestCompactionTimestamp(nil); got != 0 {
		t.Fatalf("no entries = %d", got)
	}
	if got := latestCompactionTimestamp([]icodingagent.SessionEntry{older, message}); got != 1735689600500 {
		t.Fatalf("one compaction = %d", got)
	}
	if got := latestCompactionTimestamp([]icodingagent.SessionEntry{older, newer, message}); got != 1735776000000 {
		t.Fatalf("newest compaction = %d", got)
	}
	broken := compactionStampEntry(t, "c3", "compaction", "not-a-date")
	if got := latestCompactionTimestamp([]icodingagent.SessionEntry{older, broken}); got != 0 {
		t.Fatalf("an unparsable newest timestamp = %d, want 0 (no fallback to an older compaction)", got)
	}
}
