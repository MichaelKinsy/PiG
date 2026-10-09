//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A Session replacement starts fresh extension processes whose first session read may be getBranch. Upstream getBranch walks the in-process log from the leaf. The Node runtime answered from its replicated log before that log was requested, so the first read of a persisted Session returned an empty branch while getEntries returned the entries.
func TestNodeRuntimeFirstBranchReadOfAPersistedSession(t *testing.T) {
	shortSockDir(t)
	entries := []string{
		`{"type":"message","id":"u1","parentId":null}`,
		`{"type":"message","id":"a1","parentId":"u1"}`,
		`{"type":"message","id":"u2","parentId":"a1"}`,
	}
	file := filepath.Join(t.TempDir(), "session.jsonl")
	header := `{"type":"session","version":3,"id":"resumed","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/work"}`
	if err := os.WriteFile(file, []byte(header+"\n"+strings.Join(entries, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var notifyMu sync.Mutex
	var notifications []string
	fakeUI := newTestUIContext()
	fakeUI.onNotify = func(msg, _ string) {
		notifyMu.Lock()
		notifications = append(notifications, msg)
		notifyMu.Unlock()
	}
	h := newTestHost(t)
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	bridge.SetHostAction("getSessionID", func() string { return "resumed" })
	bridge.SetHostAction("getSessionFile", func() string { return file })
	bridge.SetHostAction("getLeafID", func() string { return "u2" })
	bridge.SetHostAction("getEntriesPage", func(cursor, _ int) ([]json.RawMessage, int, bool, string) {
		raw := make([]json.RawMessage, len(entries))
		for i, entry := range entries {
			raw[i] = json.RawMessage(entry)
		}
		if cursor < 0 || cursor > len(raw) {
			cursor = 0
		}
		return raw[cursor:], len(raw), false, "u2"
	})
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ext, err := h.Load(ctx, ExtConfig{Name: "branch-first", Source: filepath.Join("testdata", "branch-first.mjs"), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = ext.Commands["branch_first"].Handler(context.Background(), "") }()
	var got string
	pollUntil(t, 15*time.Second, "extension never reported its branch", func() bool {
		notifyMu.Lock()
		defer notifyMu.Unlock()
		for _, n := range notifications {
			if strings.HasPrefix(n, "branch=") {
				got = n
				return true
			}
		}
		return false
	})
	if want := fmt.Sprintf("branch=[%s]", "u1,a1,u2"); got != want {
		t.Fatalf("first branch read = %q, want %q", got, want)
	}
}
