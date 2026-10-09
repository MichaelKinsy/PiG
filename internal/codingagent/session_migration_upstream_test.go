package codingagent

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

func decodeFileEntry(t *testing.T, raw FileEntry) map[string]any {
	t.Helper()
	var entry map[string]any
	if err := json.Unmarshal(raw.Raw(), &entry); err != nil {
		t.Fatalf("entry %s: %v", raw, err)
	}
	return entry
}

// packages/coding-agent/test/session-manager/migration.test.ts "should add id/parentId to v1 entries" (:5-45): a header without a version and two entries
// without ids migrate to the current version (3), and each entry gets an 8-character id and the previous entry as its parent (the first has a null parent).
func TestMigrateSessionEntriesAddsIDAndParentIDToV1Entries(t *testing.T) {
	entries := []FileEntry{
		sessionentry.DecodeFileEntry(json.RawMessage(`{"type":"session","id":"sess-1","timestamp":"2025-01-01T00:00:00Z","cwd":"/tmp"}`)),
		sessionentry.DecodeFileEntry(json.RawMessage(`{"type":"message","timestamp":"2025-01-01T00:00:01Z","message":{"role":"user","content":"hi","timestamp":1}}`)),
		sessionentry.DecodeFileEntry(json.RawMessage(`{"type":"message","timestamp":"2025-01-01T00:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"hello"}],"api":"test","provider":"test","model":"test","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0},"stopReason":"stop","timestamp":2}}`)),
	}
	if err := MigrateSessionEntries(entries); err != nil {
		t.Fatal(err)
	}
	if version := decodeFileEntry(t, entries[0])["version"]; version != float64(3) {
		t.Fatalf("header version = %v, want 3", version)
	}
	first, second := decodeFileEntry(t, entries[1]), decodeFileEntry(t, entries[2])
	firstID, _ := first["id"].(string)
	secondID, _ := second["id"].(string)
	if len(firstID) != 8 || len(secondID) != 8 {
		t.Fatalf("ids = %q, %q, want 8 characters each", firstID, secondID)
	}
	if parent, present := first["parentId"]; !present || parent != nil {
		t.Fatalf("first parentId = %v (present %v), want null", parent, present)
	}
	if second["parentId"] != firstID {
		t.Fatalf("second parentId = %v, want the first id %q", second["parentId"], firstID)
	}
}

// migration.test.ts "should be idempotent (skip already migrated)" (:47-75): a version 2 file's ids and parents are left alone.
func TestMigrateSessionEntriesIsIdempotentForMigratedEntries(t *testing.T) {
	entries := []FileEntry{
		sessionentry.DecodeFileEntry(json.RawMessage(`{"type":"session","id":"sess-1","version":2,"timestamp":"2025-01-01T00:00:00Z","cwd":"/tmp"}`)),
		sessionentry.DecodeFileEntry(json.RawMessage(`{"type":"message","id":"abc12345","parentId":null,"timestamp":"2025-01-01T00:00:01Z","message":{"role":"user","content":"hi","timestamp":1}}`)),
		sessionentry.DecodeFileEntry(json.RawMessage(`{"type":"message","id":"def67890","parentId":"abc12345","timestamp":"2025-01-01T00:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"hello"}],"api":"test","provider":"test","model":"test","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0},"stopReason":"stop","timestamp":2}}`)),
	}
	if err := MigrateSessionEntries(entries); err != nil {
		t.Fatal(err)
	}
	first, second := decodeFileEntry(t, entries[1]), decodeFileEntry(t, entries[2])
	if first["id"] != "abc12345" || second["id"] != "def67890" || second["parentId"] != "abc12345" {
		t.Fatalf("ids changed: first %v, second %v parent %v", first["id"], second["id"], second["parentId"])
	}
}
