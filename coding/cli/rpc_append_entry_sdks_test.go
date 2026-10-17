package cli

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi's pi.appendEntry appends to the session log before it returns and the runtime action emits entry_appended
// (agent-session.ts:3406-3411), so a handler that appends and then reads ctx.sessionManager.getEntries() counts its entry.
// The robust-notes extension's /robust-note does exactly that in each SDK, over a real Session and the production session
// binders of RPC mode: every SDK must report "robust note 1: first" and then "robust note 2: second" (an SDK whose read misses
// the append reports one fewer) and the mode must emit entry_appended for each.
func TestRPCAppendEntryIsVisibleToTheSameHandlerInEverySDK(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fixtures := filepath.Join(root, "test", "parity", "testdata", "robust-notes")
	for _, sdk := range []string{"node", "go", "python", "rust"} {
		t.Run(sdk, func(t *testing.T) {
			home := t.TempDir()
			p := startRPCProcessAt(t, t.TempDir(), []string{
				"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1",
				"PIG_SDK_GO_ROOT=" + filepath.Join(root, "extensions", "sdk"),
			}, "--model", "test-faux/faux-1", "--no-session", "-e", filepath.Join(fixtures, sdk))
			// The load builds the Go and Rust fixtures cold (Rust compiles its crate dependencies), so only the wait for the
			// session_start status allows a build; the append and read are held to the ordinary budget.
			p.budget = probeWait(t, 10*time.Minute)
			p.await("robust-notes session_start status", func(record rpcRecord) bool {
				return record["type"] == "extension_ui_request" && record["method"] == "setStatus" && record["statusText"] == "robust: ready"
			})
			p.budget = testbudget.Wait(t)
			// The second note runs after the first read subscribed the SDK's session mirror, so it counts only when the
			// appended entry reaches an already-subscribed mirror before appendEntry returns; the first note alone is
			// satisfied by the subscription's own fetch of the whole log.
			for index, note := range []string{"first", "second"} {
				p.send(fmt.Sprintf(`{"id":"note-%d","type":"prompt","message":"/robust-note %s"}`, index, note))
				want := fmt.Sprintf("robust note %d: %s", index+1, note)
				var notified, appended bool
				p.await(want+" and entry_appended", func(record rpcRecord) bool {
					switch record["type"] {
					case "extension_ui_request":
						if record["method"] == "notify" {
							if message, _ := record["message"].(string); message != "" {
								if message != want {
									t.Fatalf("notification = %q, want %q: every appended entry counted in the same handler", message, want)
								}
								notified = true
							}
						}
					case "entry_appended":
						if entry, _ := record["entry"].(map[string]any); entry["customType"] == "robust-note" {
							if data, _ := entry["data"].(map[string]any); data["text"] == note {
								appended = true
							}
						}
					}
					return notified && appended
				})
			}
		})
	}
}
