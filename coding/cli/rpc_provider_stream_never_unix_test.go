//go:build unix

package cli

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// TestRPCAbortedExtensionProviderTurnWhoseAuthorNeverReacts: an author that never reacts to the abort of its request (it neither pushes nor ends its stream) holds the
// turn, because Pi's consumer waits for the stream (packages/agent/src/agent-loop.ts:424-478). Pi 1.5.2, run with this extension, gives: no agent_end and no answer to the
// abort command (rpc-mode.ts awaits session.abort()), isStreaming true; a second abort has no answer either; a new prompt is refused with Pi's "Agent is already
// processing" error; nothing of the turn is persisted; and when stdin closes the process exits 0 at once (0.03 s), because nothing keeps Pi's event loop alive: the
// author's stream holds no timer and no I/O. PiG adds no bound and gives the same: the Node runtime reports its drained loop (D19), the process exits 0, and no runtime
// process is left. The row watches each silence for one second; Pi was watched for three.
func TestRPCAbortedExtensionProviderTurnWhoseAuthorNeverReacts(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"isolated", "packed"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			fixture := filepath.Join(dir, "provider-abort.mjs")
			if err := os.WriteFile(fixture, []byte(providerStreamAbortExtension), 0o600); err != nil {
				t.Fatal(err)
			}
			sessions := filepath.Join(dir, "sessions")
			args := []string{"--model", "abort-probe/author-never", "--session-dir", sessions, "-e", fixture}
			if kind == "packed" {
				sibling := filepath.Join(dir, "provider-abort-sibling.mjs")
				if err := os.WriteFile(sibling, []byte(providerStreamAbortSibling), 0o600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "-e", sibling)
			}
			p := startRPCProcess(t, nil, args...)
			shown := ""
			held := regexp.MustCompile(`^alpha beta pid ([0-9]+) $`)
			p.send(`{"id":"hold","type":"prompt","message":"HOLD the stream"}`)
			p.await("the partial text before the abort", func(record rpcRecord) bool {
				if record["type"] == "message_update" {
					if event, _ := record["assistantMessageEvent"].(map[string]any); event["type"] == "text_delta" {
						delta, _ := event["delta"].(string)
						shown += delta
					}
				}
				return held.MatchString(shown)
			})
			runtimeProcess, err := strconv.Atoi(held.FindStringSubmatch(shown)[1])
			if err != nil {
				t.Fatal(err)
			}
			// streaming asks for the state and returns isStreaming. Every record on the way is checked: the turn must not end, and no abort may be answered.
			streaming := func(id string) bool {
				t.Helper()
				value := false
				p.send(`{"id":"` + id + `","type":"get_state"}`)
				p.await("the state "+id, func(record rpcRecord) bool {
					message, _ := record["message"].(map[string]any)
					if record["type"] == "agent_end" || (record["type"] == "message_end" && message["role"] == "assistant") {
						t.Errorf("the turn ended (%v) though its author's stream had not", record["type"])
					}
					if record["type"] == "response" && record["command"] == "abort" {
						t.Errorf("the abort %v was answered though the turn had not ended", record["id"])
					}
					if !isSuccessResponse(record, id) {
						return false
					}
					data, _ := record["data"].(map[string]any)
					value, _ = data["isStreaming"].(bool)
					return true
				})
				return value
			}
			p.send(`{"id":"abort","type":"abort"}`)
			time.Sleep(time.Second)
			if !streaming("state-1") {
				t.Error("isStreaming is false one second after the abort, want true: the turn waits for its author's stream")
			}
			p.send(`{"id":"abort-2","type":"abort"}`)
			time.Sleep(time.Second)
			if !streaming("state-2") {
				t.Error("isStreaming is false after a second abort, want true")
			}
			p.send(`{"id":"again","type":"prompt","message":"a new prompt while streaming"}`)
			refused := ""
			p.await("the refusal of a new prompt", func(record rpcRecord) bool {
				if record["type"] != "response" || record["id"] != "again" {
					return false
				}
				if record["success"] == false {
					refused, _ = record["error"].(string)
				}
				return true
			})
			if want := "Agent is already processing. Specify streamingBehavior ('steer' or 'followUp') to queue the message."; refused != want {
				t.Errorf("a new prompt while the aborted turn waits was answered with the error %q, want %q", refused, want)
			}
			if !streaming("state-3") {
				t.Error("isStreaming is false after the refused prompt, want true")
			}

			// The way out: stdin closes. The process exits 0, and the runtime's process does not outlive it.
			p.closeAndWait("while an aborted extension provider turn waits for an author that never reacts")
			budget := testbudget.Wait(t)
			deadline := time.Now().Add(budget)
			for !errors.Is(syscall.Kill(runtimeProcess, 0), syscall.ESRCH) {
				if time.Now().After(deadline) {
					t.Errorf("the extension runtime's process %d is alive %s after pig exited", runtimeProcess, budget)
					_ = syscall.Kill(runtimeProcess, syscall.SIGKILL)
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			// Nothing of the turn is persisted.
			if persisted := providerAbortPersisted(t, sessions); len(persisted) != 0 {
				t.Errorf("the session file holds the assistant messages %q of a turn that never ended, want none", persisted)
			}
		})
	}
}
