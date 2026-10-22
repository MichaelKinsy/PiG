package cli

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi's RPC mode writes one extension_ui_request per ctx.ui.setWidget call, in call order among the other UI calls of the handler, and
// before the command's response (rpc-mode.ts setWidget and notify write synchronously, and the response follows the handler). The
// robust-widgets command of every SDK's robust-notes fixture makes the same calls: a list, a notification, the same list again, a clear,
// another list, a notification. Node is the reference (scenario rpc/68-rpc-set-widget-per-call-order compares its sequence with Pi's); the
// Go, Python and Rust SDKs sent a list with no options as an unanswered widget_push frame, which the host applied on a lane of its own: a
// repeat of the same list was dropped, a list set after a clear stayed cleared, and the last list could follow the response.
func TestRPCWidgetsAreWrittenPerCallInEverySDK(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fixtures := filepath.Join(root, "test", "parity", "testdata", "robust-notes")
	want := []string{
		`setWidget robust-w ["w-one"]`,
		`notify w-n1`,
		`setWidget robust-w ["w-one"]`,
		`setWidget robust-w <clear>`,
		`setWidget robust-w ["w-two"]`,
		`notify w-n2`,
		`response widgets`,
		`response sync`,
	}
	for _, sdk := range []string{"node", "go", "python", "rust"} {
		t.Run(sdk, func(t *testing.T) {
			home := t.TempDir()
			p := startRPCProcessAt(t, t.TempDir(), []string{
				"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1",
				"PIG_SDK_GO_ROOT=" + filepath.Join(root, "extensions", "sdk"),
			}, "--model", "test-faux/faux-1", "--no-session", "-e", filepath.Join(fixtures, sdk))
			// The load builds the Go and Rust fixtures cold, so only the wait for the session_start status allows a build.
			p.budget = probeWait(t, 10*time.Minute)
			p.await("robust-notes session_start status", func(record rpcRecord) bool {
				return record["type"] == "extension_ui_request" && record["method"] == "setStatus" && record["statusText"] == "robust: ready"
			})
			p.budget = testbudget.Wait(t)
			// Repeat the command: a frame that raced the response in one run is applied after it in the next.
			for run := range 3 {
				p.send(`{"id":"widgets","type":"prompt","message":"/robust-widgets"}`)
				var got []string
				// A second command after the first one's response: a frame the host applied late shows up between the two responses.
				sent := false
				p.await("the get_state response that follows the command", func(record rpcRecord) bool {
					switch record["type"] {
					case "extension_ui_request":
						switch record["method"] {
						case "setWidget":
							lines := "<clear>"
							if raw, present := record["widgetLines"]; present {
								encoded, _ := json.Marshal(raw)
								lines = string(encoded)
							}
							got = append(got, "setWidget "+record["widgetKey"].(string)+" "+lines)
						case "notify":
							got = append(got, "notify "+record["message"].(string))
						}
					case "response":
						got = append(got, "response "+record["id"].(string))
						if record["id"] == "widgets" && !sent {
							sent = true
							p.send(`{"id":"sync","type":"get_state"}`)
						}
						return record["id"] == "sync"
					}
					return false
				})
				if !slices.Equal(got, want) {
					t.Fatalf("run %d: RPC records = %q, want %q", run, got, want)
				}
			}
		})
	}
}
