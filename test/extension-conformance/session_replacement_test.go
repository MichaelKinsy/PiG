package extensionconformance

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's replacement event files cross the same wire in every SDK realization.
func TestSessionReplacementLifecycleAcrossSDKs(t *testing.T) {
	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				h.host.Shutdown("test complete")
			})
			for _, reason := range []string{"new", "resume", "fork"} {
				before := len(h.ui.Recorded())
				for _, event := range []extension.ExtensionEvent{
					extension.SessionShutdownEvent{Type: "session_shutdown", Reason: reason, TargetSessionFile: "/destination.jsonl"},
					extension.SessionStartEvent{Type: "session_start", Reason: reason, PreviousSessionFile: "/outgoing.jsonl"},
				} {
					if _, err := h.runner.Emit(t.Context(), event); err != nil {
						t.Fatal(err)
					}
				}
				want := []string{"session_shutdown:" + reason + ":info", "target:/destination.jsonl:info", "session_start:" + reason + ":info", "previous:/outgoing.jsonl:info"}
				got := h.ui.Recorded()[before:]
				if !slices.Equal(got, want) {
					t.Fatalf("%s lifecycle = %v, want %v", reason, got, want)
				}
			}
		})
	}
}

// Pi hands each replacement context the destination SessionManager. A mirror
// must replace even a zero-length log; an old cursor cannot trim a longer one.
func TestSessionReplacementMirrorsAcrossSDKs(t *testing.T) {
	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				h.host.Shutdown("test complete")
			})
			var mu sync.Mutex
			var entries []json.RawMessage
			id, leaf := "", ""
			h.bridge.SetHostAction("getSessionID", func() string { mu.Lock(); defer mu.Unlock(); return id })
			h.bridge.SetHostAction("getSessionFile", func() string { return "" })
			h.bridge.SetHostAction("getLeafID", func() string { mu.Lock(); defer mu.Unlock(); return leaf })
			h.bridge.SetHostAction("getEntriesPage", func(cursor, _ int) ([]json.RawMessage, int, bool, string) {
				mu.Lock()
				defer mu.Unlock()
				if cursor > len(entries) {
					cursor = 0
				}
				return entries[cursor:], len(entries), false, leaf
			})
			command, ok := findCommand(h.runner, "session-log-probe")
			if !ok {
				t.Fatal("missing session-log-probe")
			}
			for generation, count := range []int{2, 5, 0, 1} {
				mu.Lock()
				id = fmt.Sprintf("replacement-%d", generation)
				entries, leaf = nil, ""
				for i := range count {
					entryID := fmt.Sprintf("%s-%d", id, i)
					parent := "null"
					if leaf != "" {
						parent = fmt.Sprintf("%q", leaf)
					}
					entries = append(entries, json.RawMessage(fmt.Sprintf(`{"type":"message","id":%q,"parentId":%s,"message":{"role":"user","content":"probe"}}`, entryID, parent)))
					leaf = entryID
				}
				want := fmt.Sprintf("session entries=%d branch=%d:info", len(entries), len(entries))
				mu.Unlock()
				before := len(h.ui.Recorded())
				if err := command.Handler(t.Context(), ""); err != nil {
					t.Fatal(err)
				}
				waitFor(t, func() bool { return len(h.ui.Recorded()) > before })
				got := h.ui.Recorded()[before:]
				if len(got) != 1 || got[0] != want {
					t.Fatalf("generation %d = %v, want %s", generation, got, want)
				}
			}
		})
	}
}
