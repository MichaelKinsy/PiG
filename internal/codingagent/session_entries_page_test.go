package codingagent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

func TestSessionEntriesRawPageBoundsPayload(t *testing.T) {
	entry := func(id string, size int) SessionEntry {
		raw, err := json.Marshal(map[string]any{"id": id, "body": strings.Repeat("x", size)})
		if err != nil {
			t.Fatal(err)
		}
		return sessionentry.DecodeSessionEntry(raw)
	}
	s := pageSession(entry("a", 700), entry("b", 700), entry("c", 700))
	page, next, total := s.RawEntriesPage(0, 1500)
	if len(page) != 2 || next != 2 || total != 3 {
		t.Fatalf("first page len=%d next=%d total=%d", len(page), next, total)
	}
	page, next, _ = s.RawEntriesPage(next, 1500)
	if len(page) != 1 || next != 3 {
		t.Fatalf("second page len=%d next=%d", len(page), next)
	}
	if page, next, _ = s.RawEntriesPage(next, 1500); page != nil || next != 3 {
		t.Fatalf("end page %v next=%d", page, next)
	}
}

func TestSessionEntriesRawPageMakesProgressForOversizedEntry(t *testing.T) {
	raw := json.RawMessage(`{"id":"large","body":"` + strings.Repeat("x", 2048) + `"}`)
	page, next, _ := pageSession(sessionentry.DecodeSessionEntry(raw)).RawEntriesPage(0, 64)
	if len(page) != 1 || next != 1 {
		t.Fatalf("oversized page len=%d next=%d", len(page), next)
	}
}

func pageSession(entries ...SessionEntry) *Session {
	s := NewSession("page", "/test")
	for _, entry := range entries {
		s.addSlotLocked(newSlot(entry), entry.Base().ParentID, entry.Raw())
	}
	return s
}
