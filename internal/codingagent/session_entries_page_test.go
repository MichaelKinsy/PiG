package codingagent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSessionEntriesRawPageBoundsPayload(t *testing.T) {
	entry := func(id string, size int) SessionEntry {
		raw, err := json.Marshal(map[string]any{"id": id, "body": strings.Repeat("x", size)})
		if err != nil {
			t.Fatal(err)
		}
		return SessionEntry{raw: raw}
	}
	entries := []SessionEntry{entry("a", 700), entry("b", 700), entry("c", 700)}
	page, next := sessionEntriesRawPage(entries, 0, 1500)
	if len(page) != 2 || next != 2 {
		t.Fatalf("first page len=%d next=%d", len(page), next)
	}
	page, next = sessionEntriesRawPage(entries, next, 1500)
	if len(page) != 1 || next != 3 {
		t.Fatalf("second page len=%d next=%d", len(page), next)
	}
}

func TestSessionEntriesRawPageMakesProgressForOversizedEntry(t *testing.T) {
	raw := json.RawMessage(`{"id":"large","body":"` + strings.Repeat("x", 2048) + `"}`)
	page, next := sessionEntriesRawPage([]SessionEntry{{raw: raw}}, 0, 64)
	if len(page) != 1 || next != 1 {
		t.Fatalf("oversized page len=%d next=%d", len(page), next)
	}
}
