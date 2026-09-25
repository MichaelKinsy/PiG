package subprocess

import (
	"encoding/json"
	"fmt"
	"testing"
)

// entriesOfSize builds n Session entries with parent links and message bodies.
func entriesOfSize(n int) []json.RawMessage {
	out := make([]json.RawMessage, 0, n)
	for i := range n {
		body := fmt.Sprintf(
			`{"type":"message","id":"e%d","parentId":"e%d","message":{"role":"assistant","content":[{"type":"text","text":%q}]}}`,
			i, i-1, "some assistant text that stands in for a real turn body")
		out = append(out, json.RawMessage(body))
	}
	return out
}

func TestWatchSessionLogCompletionBarrierIncludesConcurrentAppend(t *testing.T) {
	entries := entriesOfSize(1)
	bridge := NewUIBridge(func() {})
	bridge.SetHostAction("getEntriesPage", func(cursor, _ int) ([]json.RawMessage, int, bool, string) {
		if cursor < 0 || cursor > len(entries) {
			cursor = 0
		}
		if cursor == len(entries) {
			return nil, cursor, false, "e1"
		}
		next := cursor + 1
		return entries[cursor:next], next, next < len(entries), "e1"
	})
	host := NewHost(t.TempDir())
	host.SetUIBridge(bridge)
	managed := &managedExt{config: ExtConfig{Name: "session-ext"}}
	host.exts["session-ext"] = managed

	page, cursor, more, _ := host.watchSessionLog("session-ext", 0, false)
	if len(page) != 1 || cursor != 1 || more || !managed.sessionTransferActive {
		t.Fatalf("initial page = %d/%d/%t active=%t", len(page), cursor, more, managed.sessionTransferActive)
	}
	entries = append(entries, entriesOfSize(2)[1])
	page, cursor, more, _ = host.watchSessionLog("session-ext", cursor, true)
	if len(page) != 1 || cursor != 2 || more || !managed.sessionTransferActive {
		t.Fatalf("completion delta = %d/%d/%t active=%t", len(page), cursor, more, managed.sessionTransferActive)
	}
	page, cursor, more, _ = host.watchSessionLog("session-ext", cursor, true)
	if len(page) != 0 || cursor != 2 || more || managed.sessionTransferActive {
		t.Fatalf("completion ack = %d/%d/%t active=%t", len(page), cursor, more, managed.sessionTransferActive)
	}

	entries = entriesOfSize(3)
	managed.entryCursor = 0
	page, cursor, more, _ = host.watchSessionLog("session-ext", 0, false)
	collected := append([]json.RawMessage(nil), page...)
	entries = append(entries, entriesOfSize(4)[3])
	for more {
		page, cursor, more, _ = host.watchSessionLog("session-ext", cursor, false)
		collected = append(collected, page...)
	}
	page, cursor, more, _ = host.watchSessionLog("session-ext", cursor, true)
	collected = append(collected, page...)
	if len(collected) != 4 || len(page) != 0 || cursor != 4 || more || managed.sessionTransferActive {
		t.Fatalf("multipage completion = %d collected/%d final cursor=%d more=%t active=%t", len(collected), len(page), cursor, more, managed.sessionTransferActive)
	}
	for index, raw := range collected {
		var entry struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &entry); err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("e%d", index); entry.ID != want {
			t.Errorf("entry %d = %q, want %q", index, entry.ID, want)
		}
	}
}

// TestStatePushDoesNotScaleWithSessionSize checks that one incremental state payload stays bounded as Session history grows.
func TestStatePushDoesNotScaleWithSessionSize(t *testing.T) {
	sizeAt := func(n int) int {
		all := entriesOfSize(n)
		b := NewUIBridge(func() {})
		b.SetHostAction("getEntriesPage", func(cursor, _ int) ([]json.RawMessage, int, bool, string) {
			if cursor < 0 || cursor > len(all) {
				cursor = 0
			}
			return all[cursor:], len(all), false, "e0"
		})
		b.SetHostAction("getSessionID", func() string { return "sess-1" })
		// Steady state sends only entries after the local cursor.
		payload, err := json.Marshal(b.Snapshot(nil, n-1, true))
		if err != nil {
			t.Fatal(err)
		}
		return len(payload)
	}

	small := sizeAt(10)
	large := sizeAt(10000)

	// A thousandfold larger session may not meaningfully enlarge one push.
	if large > small*2 {
		t.Errorf("state push grew from %d to %d bytes when the session grew 10 -> 10000 entries; "+
			"every event pays this, for every extension, whether or not it reads the session",
			small, large)
	}
}
