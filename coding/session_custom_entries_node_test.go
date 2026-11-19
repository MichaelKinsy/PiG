//go:build !pig_strip_node_extensions

package coding_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// agent-session.ts _bindExtensionCore emits entry_appended for pi.appendEntry; session-manager.ts appendCustomEntry writes only the log. A subprocess host must keep the identity already returned synchronously in the extension process.
func TestAppendExtensionEntryThroughNodeHost(t *testing.T) {
	for _, isolation := range []string{"isolated", "packed"} {
		t.Run(isolation, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			session := newEntryAppendSession(t, coding.SessionOptions{NoSession: true})
			bridge := subprocess.NewUIBridge(nil)
			coding.BindSessionReadActions(bridge, func() *coding.Session { return session }, session.CWD(), "")
			bridge.SetHostAction("appendEntry", func(customType string, data any, direct *subprocess.DirectEntryAppend) error {
				_, err := session.AppendExtensionEntry(customType, data, direct)
				return err
			})
			root := t.TempDir()
			source := filepath.Join(root, "entry.mjs")
			if err := os.WriteFile(source, []byte(extensionEntryFixture), 0o600); err != nil {
				t.Fatal(err)
			}
			host := subprocess.NewHostWithConfigRoot(root, filepath.Join(root, "config"))
			host.SetUIBridge(bridge)
			defer host.Shutdown("test complete")
			config := subprocess.ExtConfig{Name: "entry", Source: source, Enabled: true}
			if isolation == "isolated" {
				config.Isolation = "isolated"
			}
			loaded, errs := host.LoadAll(ctx, []subprocess.ExtConfig{config})
			if len(errs) != 0 || len(loaded) != 1 {
				t.Fatalf("loaded %d extensions, errors = %v", len(loaded), errs)
			}
			for _, args := range []string{`{"event":true}`, `{"event":false}`} {
				if _, err := loaded[0].Tools["append_probe"].Definition.Execute(ctx, "append", json.RawMessage(args), nil); err != nil {
					t.Fatal(err)
				}
			}
			result, err := loaded[0].Tools["read_probe"].Definition.Execute(ctx, "read", json.RawMessage(`{}`), nil)
			if err != nil {
				t.Fatal(err)
			}
			var view struct {
				Seen    []struct{ ID, Timestamp string }
				Entries []json.RawMessage
			}
			if len(result.Content) != 1 {
				t.Fatalf("read returned %d content blocks, want one", len(result.Content))
			}
			content, ok := result.Content[0].(ai.TextContent)
			if !ok {
				t.Fatalf("read content = %T, want text", result.Content[0])
			}
			if err := json.Unmarshal([]byte(content.Text), &view); err != nil {
				t.Fatal(err)
			}
			if len(view.Seen) != 2 || len(view.Entries) != len(view.Seen) {
				t.Fatalf("extension view = %s, want both appends exactly once", content.Text)
			}
			for i, seen := range view.Seen {
				entry, ok := session.SessionManager().GetEntry(seen.ID)
				if !ok || entry.Base().Timestamp != seen.Timestamp || !bytes.Equal(entry.Raw(), view.Entries[i]) {
					t.Fatalf("extension identity %+v and mirror %s disagree with the host entry %+v (found=%t)", seen, view.Entries[i], entry, ok)
				}
			}
		})
	}
}
