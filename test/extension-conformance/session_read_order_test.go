package extensionconformance

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

const piSessionOrderDir = "../../internal/codingagent/testdata/pi-session-order"

// upstream: packages/coding-agent/src/core/session-manager.ts:439-585, 1529-1560. The expectations are what the real Pi 0.99.1 SessionManager returns on entries.jsonl, written by JSON.stringify; its session-manager.ts and messages.ts are the same in 0.99.2.
//
// An extension in Node reads the session from the log the host replicated and Pi's own code; an extension in Rust or Python asks the host, which answers with the same entries and projections. Both must hand the extension objects in Pi's member order, so the extension sees and re-serializes them as Pi does. The Go SDK decodes every object into a Go map, which has no member order (a Go language mechanic, not a host difference), so it is not a row here.
func TestSessionReadsKeepPiMemberOrderAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	expectedFile, err := os.ReadFile(filepath.Join(piSessionOrderDir, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var expected map[string]string
	if err := json.Unmarshal(expectedFile, &expected); err != nil {
		t.Fatal(err)
	}
	var want strings.Builder
	want.WriteByte('{')
	for i, method := range []string{"getEntries", "getEntry", "getLeafEntry", "getBranch", "getChildren", "getTree", "buildContextEntries", "buildSessionProjection", "buildSessionContext"} {
		if i > 0 {
			want.WriteByte(',')
		}
		key, _ := json.Marshal(method)
		want.Write(key)
		want.WriteByte(':')
		want.WriteString(expected[method])
	}
	want.WriteByte('}')

	for _, tc := range allHarnessCases() {
		switch tc.name {
		case "subprocess-node", "subprocess-node-packed", "subprocess-rust", "subprocess-python":
		default:
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			session := codingagent.NewSession("pi-order", "/w")
			entries, err := os.Open(filepath.Join(piSessionOrderDir, "entries.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = entries.Close() })
			scanner := bufio.NewScanner(entries)
			scanner.Buffer(nil, 1<<20)
			for scanner.Scan() {
				if err := session.AppendEntry(json.RawMessage(bytes.Clone(scanner.Bytes()))); err != nil {
					t.Fatal(err)
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			session.SetPath(filepath.Join(t.TempDir(), "session.jsonl"))
			view := codingagent.ExtensionSessionView{Session: session, CWD: "/w"}
			h.bridge.SetHostAction("sessionRead", func(method string, args json.RawMessage) (any, error) {
				return codingagent.ExtensionSessionRead(view, method, args)
			})
			h.bridge.SetHostAction("getSessionID", func() string { return view.Session.ID() })
			h.bridge.SetHostAction("getSessionName", func() string { return "" })
			h.bridge.SetHostAction("getSessionFile", func() string { return view.Session.Path() })
			h.bridge.SetHostAction("getLeafID", func() string { return *view.Session.LeafID() })
			h.bridge.SetHostAction("getEntriesPage", func(cursor, _ int) ([]json.RawMessage, int, bool, string) {
				all := view.Session.Entries()
				if cursor < 0 || cursor > len(all) {
					cursor = 0
				}
				page := []json.RawMessage{}
				for _, entry := range all[cursor:] {
					page = append(page, entry.Raw())
				}
				return page, len(all), false, *view.Session.LeafID()
			})
			h.host.BroadcastStateUpdate()

			command, ok := findCommand(h.runner, "session-order")
			if !ok {
				t.Fatal("missing session-order command")
			}
			*h.notify = nil
			if err := command.Handler(context.Background(), ""); err != nil {
				t.Fatal(err)
			}
			if len(*h.notify) != 1 {
				t.Fatalf("notifications: %v", *h.notify)
			}
			got := strings.TrimSuffix((*h.notify)[0], ":info")
			if got != want.String() {
				t.Fatalf("session reads differ from Pi's\n got  %s\n want %s", got, want.String())
			}
		})
	}
}
