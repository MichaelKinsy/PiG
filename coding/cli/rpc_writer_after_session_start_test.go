package cli

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// rpc-mode.ts:316-361: rebindSession awaits session.bindExtensions (every session_start handler) and only then subscribes the stdout writer, so the
// entry_appended and session_info_changed events a session_start handler emits through pi.appendEntry and pi.setSessionName are not written. A command's
// appends after startup are written. A replacement command binds twice (rpc-mode.ts:435-441): the first bind's session_start output is not written, the
// second bind runs with the writer subscribed and its output is. The writer-start fixture (every SDK) appends, names and sends in session_start and in
// /writer-after; the status its session_start sets last marks the end of startup. The custom message session_start sends is not asserted here: its
// startup write is the forwarder's, covered by the parity scenario 63-rpc-session-start-message-after-response.
func TestRPCWritesNothingSessionStartEmittedBeforeTheWriterSubscribesInEverySDK(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fixtures := filepath.Join(root, "test", "parity", "testdata", "writer-start")
	for _, sdk := range []string{"node", "go", "python", "rust"} {
		t.Run(sdk, func(t *testing.T) {
			home := t.TempDir()
			p := startRPCProcessAt(t, t.TempDir(), []string{
				"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1",
				"PIG_SDK_GO_ROOT=" + filepath.Join(root, "extensions", "sdk"),
			}, "--model", "test-faux/faux-1", "--no-session", "-e", filepath.Join(fixtures, sdk))
			// The load builds the Go and Rust fixtures cold, so only the wait for the session_start status allows a build.
			p.budget = probeWait(t, 10*time.Minute)
			var log []rpcRecord
			p.await("writer-start session_start status", func(record rpcRecord) bool {
				log = append(log, record)
				return record["type"] == "extension_ui_request" && record["method"] == "setStatus" && record["statusText"] == "writer: ready"
			})
			p.budget = testbudget.Wait(t)
			// A command's response follows everything startup wrote, so the barrier leaves no startup event unread.
			p.send(`{"id":"barrier","type":"get_state"}`)
			p.await("barrier response", func(record rpcRecord) bool {
				log = append(log, record)
				return record["type"] == "response" && record["id"] == "barrier"
			})
			for _, record := range log {
				if record["type"] == "entry_appended" || record["type"] == "session_info_changed" {
					t.Fatalf("startup wrote %v from a session_start handler, before the writer subscribed (Pi writes neither)", record)
				}
			}

			// A command's append, after the writer subscribed, is written.
			log = nil
			p.send(`{"id":"after","type":"prompt","message":"/writer-after"}`)
			p.await("the command's events and response", func(record rpcRecord) bool {
				log = append(log, record)
				return record["type"] == "response" && record["id"] == "after"
			})
			if n := countEntryAppended(log, "writer-after"); n != 1 {
				t.Errorf("/writer-after: %d entry_appended for its appendEntry, want 1: %v", n, log)
			}
			if n := countSessionInfoChanged(log, "after-name"); n != 1 {
				t.Errorf("/writer-after: %d session_info_changed for its setSessionName, want 1: %v", n, log)
			}

			// A replacement binds twice; only the second bind's session_start output is written.
			log = nil
			p.send(`{"id":"new","type":"new_session"}`)
			p.await("new_session response", func(record rpcRecord) bool {
				log = append(log, record)
				return record["type"] == "response" && record["id"] == "new"
			})
			if n := countEntryAppended(log, "writer-start"); n != 1 {
				t.Errorf("new_session: %d entry_appended from session_start, want 1 (the second bind's; the first is before the writer subscribes): %v", n, log)
			}
			if n := countSessionInfoChanged(log, "start-name"); n != 1 {
				t.Errorf("new_session: %d session_info_changed from session_start, want 1 (the second bind's): %v", n, log)
			}
		})
	}
}

func countEntryAppended(log []rpcRecord, customType string) int {
	n := 0
	for _, record := range log {
		if entry, _ := record["entry"].(map[string]any); record["type"] == "entry_appended" && entry["customType"] == customType {
			n++
		}
	}
	return n
}

func countSessionInfoChanged(log []rpcRecord, name string) int {
	n := 0
	for _, record := range log {
		if record["type"] == "session_info_changed" && record["name"] == name {
			n++
		}
	}
	return n
}
