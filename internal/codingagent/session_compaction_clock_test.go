package codingagent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Pi's appendCompaction stamps the entry with `new Date()` (session-manager.ts:1280), the clock a test's fake timers hold, like the header's. The
// session's clock seam is that clock here.
func TestAppendCompactionEntryReadsTheSessionClock(t *testing.T) {
	fixed := time.Date(2026, 2, 3, 4, 5, 6, 789_000_000, time.UTC)
	restore := sessionClock
	sessionClock = func() time.Time { return fixed }
	t.Cleanup(func() { sessionClock = restore })

	session := NewSession("compaction-clock", t.TempDir())
	id, err := session.AppendCompaction("summary", "", 10, nil, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := session.GetEntry(id)
	if !ok {
		t.Fatalf("no entry %s", id)
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if want := `"timestamp":"2026-02-03T04:05:06.789Z"`; !strings.Contains(string(data), want) {
		t.Errorf("the compaction entry is %s, want %s: its time is not the session clock's", data, want)
	}
}
