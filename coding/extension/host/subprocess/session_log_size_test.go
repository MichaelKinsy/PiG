package subprocess

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// sessionLogPageBytes chunks the session log on the wire; it never limits what
// an extension sees. A session larger than several pages, including one entry
// larger than a page, reaches the extension's synchronous getEntries whole.
func TestSessionLogLargerThanAPageReachesExtensionWhole(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the session log fixture: %v", err)
	}
	big := strings.Repeat("x", sessionLogPageBytes+1024)
	var entries []json.RawMessage
	parent := ""
	for i := range 4 {
		id := fmt.Sprintf("e%d", i)
		entry := map[string]any{"id": id, "type": "message", "message": map[string]any{"role": "user", "content": big}}
		if parent != "" {
			entry["parentId"] = parent
		}
		raw, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, raw)
		parent = id
	}
	dir := t.TempDir()
	source := `export default function (pi) {
  pi.registerCommand("count", { description: "count entries", handler: async (_args, ctx) => {
    const all = ctx.sessionManager.getEntries();
    ctx.ui.notify("entries=" + all.length + " bytes=" + all.reduce((n, e) => n + e.message.content.length, 0), "info");
  } });
}
`
	if err := os.WriteFile(filepath.Join(dir, "index.mjs"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var notices []string
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(&testUIContext{UIContext: extension.NoopUIContext, onNotify: func(msg, _ string) {
		mu.Lock()
		defer mu.Unlock()
		notices = append(notices, msg)
	}})
	bridge.SetActions(&HostCallbacks{
		GetSessionID:   func() string { return "big" },
		GetSessionFile: func() string { return "" },
		GetLeafID:      func() string { return parent },
		GetEntriesPage: func(cursor, maxBytes int) ([]json.RawMessage, int, bool, string) {
			if cursor < 0 || cursor >= len(entries) {
				return nil, len(entries), false, parent
			}
			var page []json.RawMessage
			size := 0
			next := cursor
			for next < len(entries) && (len(page) == 0 || size+len(entries[next]) <= maxBytes) {
				page = append(page, entries[next])
				size += len(entries[next])
				next++
			}
			return page, next, next < len(entries), parent
		},
	})
	host := NewHost(t.TempDir())
	host.SetUIBridge(bridge)
	defer host.Shutdown("test done")
	ext, err := host.Load(testbudget.Context(t), ExtConfig{Name: "big-session", Source: filepath.Join(dir, "index.mjs"), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := ext.Commands["count"].Handler(testbudget.Context(t), ""); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("entries=%d bytes=%d", len(entries), len(entries)*len(big))
	pollUntil(t, testbudget.Wait(t), "extension never reported the session size", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(notices) > 0
	})
	mu.Lock()
	defer mu.Unlock()
	if notices[0] != want {
		t.Fatalf("extension saw %q, want %q", notices[0], want)
	}
}
