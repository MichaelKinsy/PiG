package extensionconformance

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi's RPC mode runs the extensions' session_start once for a replacement Session, and writes the events the handler emits (rpc-mode.ts: the runtime's
// rebind callback is the only rebind, and the Session's events are subscribed before bindExtensions; Pi beffcde92dea, #12230, issue #10470). Before it, new_session,
// switch_session, fork and clone rebound a second time, so every handler ran twice for the new Session with no session_shutdown between, an appended entry was
// written twice, and the events of the startup session_start never reached the client.
//
// The row drives the pig command line in RPC mode with the extension of the nested-events row in every SDK and placement it has (Node packed; Go packed, isolated
// and fused; Python and Rust packed and isolated). Its session_start handler appends a "nested-start" entry carrying the call count of its own instance, and its
// session_shutdown handler notifies; each Session's extensions are new instances, so a second call on one Session would carry starts 2, a value no default
// produces. The count of the calls is read from the client's stream: one entry_appended per session_start, written once, and the old Session's one shutdown.
func TestRPCSessionReplacementRunsSessionStartOnceInEverySDK(t *testing.T) {
	if testing.Short() {
		t.Skip("builds pig and every SDK's extension")
	}
	for _, tc := range []struct{ sdk, placement string }{
		{"node", "packed"},
		{"go", "packed"}, {"go", "isolated"}, {"go", "fused"},
		{"python", "packed"}, {"python", "isolated"},
		{"rust", "packed"}, {"rust", "isolated"},
	} {
		t.Run(tc.sdk+"/"+tc.placement, func(t *testing.T) {
			t.Parallel()
			pig, err := nestedEventsPig.Do(t, func(ctx context.Context) (string, error) {
				return buildNestedEventsCommand(ctx, "nested-events-pig", "pig")
			})
			if err != nil {
				t.Fatal(err)
			}
			label := tc.sdk + "/" + tc.placement
			home := t.TempDir()
			args := []string{"--mode", "rpc", "--offline", "--no-extensions", "--no-skills", "--no-context-files", "--session-dir", filepath.Join(home, "sessions"), "--system-prompt", "P.", "--model", "test-faux/faux-1"}
			for _, path := range nestedEventsExtensions(t, tc.sdk, tc.placement) {
				args = append(args, "-e", path)
			}
			p := startNestedEventsRPC(t, label, pig, []string{"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1"}, args)

			var seen []map[string]any
			next := 0
			await := func(what string, budget time.Duration, done func(map[string]any) bool) {
				t.Helper()
				p.await(what, budget, func(record map[string]any) bool {
					seen = append(seen, record)
					return done(record)
				})
			}
			isResponse := func(id string) func(map[string]any) bool {
				return func(record map[string]any) bool { return record["type"] == "response" && record["id"] == id }
			}
			// send runs a command and the barrier after it: a response follows every record the command wrote, and the barrier's follows the deferred UI calls the
			// replacement's extensions made. It returns what was written since the previous call.
			send := func(what, line string) []map[string]any {
				t.Helper()
				from := len(seen)
				id := fmt.Sprintf("c%d", next)
				barrier := fmt.Sprintf("b%d", next)
				next++
				p.send(fmt.Sprintf(`{"id":%q,%s}`, id, line))
				await(what, testbudget.Wait(t), isResponse(id))
				p.send(fmt.Sprintf(`{"id":%q,"type":"get_state"}`, barrier))
				await(what+": the barrier", testbudget.Wait(t), isResponse(barrier))
				return seen[from:]
			}
			// A cold load builds the Go and Rust extensions, so only the first state query waits for a build.
			p.send(`{"id":"load","type":"get_state"}`)
			await("the startup to finish", 10*time.Minute, isResponse("load"))
			if got, want := startEntries(seen), []string{"startup:1"}; !slices.Equal(got, want) {
				t.Fatalf("%s: the startup session_start's entries reached the client as %q, want %q", label, got, want)
			}

			// Pi's promptOnce: a session with a user message, written to disk.
			from := len(seen)
			p.send(`{"id":"prompt","type":"prompt","message":"reply with exactly: first"}`)
			await("the prompt to settle", testbudget.Wait(t), func(record map[string]any) bool { return record["type"] == "agent_settled" })
			if got := startEntries(seen[from:]); len(got) != 0 {
				t.Fatalf("%s: a prompt wrote session_start entries %q", label, got)
			}
			var entryID, sessionFile string
			for _, record := range send("get_fork_messages", `"type":"get_fork_messages"`) {
				if data, _ := record["data"].(map[string]any); data != nil {
					if messages, _ := data["messages"].([]any); len(messages) > 0 {
						entryID, _ = messages[0].(map[string]any)["entryId"].(string)
					}
				}
			}
			for _, record := range send("get_state", `"type":"get_state"`) {
				if data, _ := record["data"].(map[string]any); data != nil && data["sessionFile"] != nil {
					sessionFile, _ = data["sessionFile"].(string)
				}
			}
			if entryID == "" || sessionFile == "" {
				t.Fatalf("%s: no user entry (%q) or session file (%q) to replace from", label, entryID, sessionFile)
			}

			// Every replacement command: one session_start for the new Session (starts 1 on its new instance), its entry streamed once, and the old Session's one
			// session_shutdown with the replacement's reason.
			for _, replacement := range []struct{ name, line, reason, shutdown string }{
				{"new_session", `"type":"new_session"`, "new", "new"},
				{"switch_session", fmt.Sprintf(`"type":"switch_session","sessionPath":%q`, sessionFile), "resume", "resume"},
				// clone runs on the resumed Session, which has the prompt's entries (a forked-at-the-first-message Session has no current entry to clone).
				{"clone", `"type":"clone"`, "fork", "fork"},
				{"fork", fmt.Sprintf(`"type":"fork","entryId":%q`, entryID), "fork", "fork"},
			} {
				window := send(replacement.name, replacement.line)
				if got, want := startEntries(window), []string{replacement.reason + ":1"}; !slices.Equal(got, want) {
					t.Errorf("%s: %s streamed the replacement's session_start entries as %q, want %q (one session_start on the new instance)", label, replacement.name, got, want)
				}
				shutdowns := 0
				for _, record := range window {
					if record["type"] == "extension_ui_request" && record["method"] == "notify" && record["message"] == "shutdown:"+replacement.shutdown {
						shutdowns++
					}
				}
				if shutdowns != 1 {
					t.Errorf("%s: %s wrote %d session_shutdown notifications for %q, want 1", label, replacement.name, shutdowns, replacement.shutdown)
				}
			}
			p.closeAndWait()
		})
	}
}

// startEntries is the "reason:starts" of every entry_appended record of the extension's session_start handler, in the order the client received them.
func startEntries(records []map[string]any) []string {
	var entries []string
	for _, record := range records {
		entry, _ := record["entry"].(map[string]any)
		if record["type"] != "entry_appended" || entry["customType"] != "nested-start" {
			continue
		}
		data, _ := entry["data"].(map[string]any)
		entries = append(entries, fmt.Sprintf("%v:%v", data["reason"], data["starts"]))
	}
	return entries
}
