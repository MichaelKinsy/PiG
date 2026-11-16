//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Pi's print and JSON modes run a prompt and go on in the same continuation, to the next prompt or to disposing the runtime: session_shutdown, then the runner's invalidate (print-mode.ts:131-166, agent-session-runtime.ts:404-410, agent-session.ts:1393-1406). An immediate queued by the prompt's last call (its command, its handled input or agent_settled) or by session_shutdown therefore runs only after the runner went stale. Probed with Pi 1.0.4 -p and --mode json: "agent_settled", "session_shutdown immediate=pending", "immediate:stale". pi-goal-x's agent_settled continuation timer otherwise fired before session_shutdown and started a run during disposal, which failed with the stale message.
func TestNodePromptEndCallbacksRunOnlyAfterTheRuntimeIsStale(t *testing.T) {
	t.Parallel()
	cases := []struct{ mode, isolation, ending string }{
		{"print", "", "agent_settled"},
		{"print", "isolated", "agent_settled"},
		{"print", "", "command"},
		{"print", "isolated", "command"},
		{"print", "", "input"},
		{"print", "isolated", "input"},
		{"json", "", "agent_settled"},
		{"json", "isolated", "agent_settled"},
	}
	for _, tc := range cases {
		layout := "packed"
		if tc.isolation != "" {
			layout = tc.isolation
		}
		t.Run(tc.mode+"/"+layout+"/"+tc.ending, func(t *testing.T) {
			t.Parallel()
			f := startPromptEnd(t, tc.mode, tc.isolation, false)
			f.end(tc.ending)
			f.emit("session_shutdown", map[string]any{"reason": "quit"})
			f.host.Invalidate("")
			want := []string{
				tc.ending,
				"session_shutdown pending=" + tc.ending,
				tc.ending + "-immediate stale",
				"session_shutdown-immediate stale",
			}
			if got := f.lines(len(want)); strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("order:\n got %q\nwant %q", got, want)
			}
		})
	}
}

// RPC mode answers a prompt and then waits for its next stdin line, an I/O wait in Pi too, so a callback the prompt's last call queued runs before any later session_shutdown, with a live ctx.
func TestNodeRPCPromptEndCallbacksRunBeforeTheNextStep(t *testing.T) {
	t.Parallel()
	f := startPromptEnd(t, "rpc", "", false)
	f.end("agent_settled")
	f.lines(2)
	f.emit("session_shutdown", map[string]any{"reason": "quit"})
	want := []string{"agent_settled", "agent_settled-immediate live", "session_shutdown pending="}
	if got := f.lines(len(want)); strings.Join(got[:len(want)], "\n") != strings.Join(want, "\n") {
		t.Fatalf("order:\n got %q\nwant %q", got, want)
	}
}

// A reload's next step reaches a Node process on its control channel, which a process waiting after a prompt's last call does not read. Reload tells every generation that it began first, so the replacement generation loads.
func TestNodePromptEndWaitEndsWhenReloadBegins(t *testing.T) {
	t.Parallel()
	for _, isolation := range []string{"", "isolated"} {
		name := "packed"
		if isolation != "" {
			name = isolation
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := startPromptEnd(t, "print", isolation, false)
			f.end("agent_settled")
			ctx, cancel := context.WithTimeout(f.ctx, 20*time.Second)
			defer cancel()
			exts, err := f.host.Reload(ctx)
			if err != nil {
				t.Fatalf("Reload after a prompt's last call: %v", err)
			}
			if len(exts) != 1 || len(exts[0].EventHandlers("agent_settled")) != 1 {
				t.Fatalf("reloaded extensions = %+v, want prompt-end with its agent_settled handler", exts)
			}
		})
	}
}

// Every Node extension of a packed process shares the wait, as they share Pi's one event loop, and a request that still needs that loop is never held up by it: a member that answers a prompt's last call does not wait while another member's request is unanswered, and a request to another member ends the wait. Isolated processes give the same answers.
func TestNodePromptEndWaitLeavesOtherRequestsRunning(t *testing.T) {
	t.Parallel()
	for _, isolation := range []string{"", "isolated"} {
		name := "packed"
		if isolation != "" {
			name = isolation
		}
		for _, order := range []string{"pending-first", "waiting-first"} {
			t.Run(name+"/"+order, func(t *testing.T) {
				t.Parallel()
				f := startPromptEnd(t, "print", isolation, true)
				done := make(chan error, 1)
				slow := func() { done <- f.slow.Commands["slow"].Handler(f.ctx, "") }
				if order == "pending-first" {
					go slow()
					for {
						if data, _ := os.ReadFile(f.log + ".slow"); len(data) > 0 {
							break
						}
						select {
						case <-f.ctx.Done():
							t.Fatal("the slow command never started")
						case <-time.After(5 * time.Millisecond):
						}
					}
					f.end("agent_settled")
				} else {
					f.end("agent_settled")
					go slow()
				}
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("slow command: %v", err)
					}
				case <-time.After(20 * time.Second):
					t.Fatal("the slow command never answered: the wait held its timer")
				}
			})
		}
	}
}

// Pi's runner emits an event to each extension in turn and awaits each handler, and print mode invalidates the runner only after session_shutdown (agent-session-runtime.ts:404-410, agent-session.ts:1393-1406). When a later extension's handler awaits a timer or I/O, Pi's one event loop turns and runs the immediate and the timer that an earlier extension's handler queued, with a live ctx. Probed with Pi 1.1.0 -p (extensions-runtime/75-print-shutdown-loop-turn): "a session_shutdown", "b session_shutdown", "a immediate live", "a timeout live", "b session_shutdown done", when b awaits 0, 1 or 20 ms. A packed process shares the event loop. An isolated process learns of the turn from loop_turned: the other runtime sends it when its handler's window closes unanswered, and the host forwards it to every other Node process before it reads that handler's response.
func TestNodePromptEndWaitEndsWhenAnotherProcessTurnsTheLoop(t *testing.T) {
	t.Parallel()
	for _, layout := range []string{"packed", "isolated", "quarantined"} {
		for _, mode := range []string{"print", "json"} {
			for _, event := range []string{"session_shutdown", "agent_settled"} {
				t.Run(layout+"/"+mode+"/"+event, func(t *testing.T) {
					t.Parallel()
					f, loaded := loadLoopTurnPair(t, layout, mode, loopTurnSecondSource)
					runLoopTurnEvent(f, loaded, event, nil)
					want := []string{"first " + event, "second " + event, "first immediate live", "first timeout live", "second " + event + " done"}
					if got := f.lines(len(want)); strings.Join(got, "\n") != strings.Join(want, "\n") {
						t.Fatalf("order:\n got %q\nwant %q", got, want)
					}
				})
			}
		}
	}
}

// Pi's one event loop runs an earlier session_shutdown handler's immediate before a later handler's continuation, and its zero-delay timer after that continuation when the later handler awaits an immediate, so after the runner's invalidate. Probed with Pi 1.1.0 -p, a later handler awaiting setImmediate: "a session_shutdown", "b session_shutdown", "a immediate live", "b session_shutdown done", "a timeout stale". A packed process shares the loop, so its order is Pi's only when nothing ends its wait before the invalidate reaches every member: not a copy of the later handler's own loop_turned, which the host must not forward to the sender's process, and not the invalidate of one member's connection while another member's copy is still unread. Each placement is run several times because the order of a packed process's sockets varies from run to run.
func TestNodePackedPromptEndWaitHoldsTimersUntilEveryMemberIsInvalidated(t *testing.T) {
	t.Parallel()
	second := strings.Replace(loopTurnSecondSource, "setTimeout(resolve, 100)", "setImmediate(resolve)", 1)
	if second == loopTurnSecondSource {
		t.Fatal("the second extension does not await an immediate")
	}
	for _, mode := range []string{"print", "json"} {
		for run := range 6 {
			t.Run(fmt.Sprintf("%s/%d", mode, run), func(t *testing.T) {
				t.Parallel()
				nodeCellRequireNode(t)
				dir := t.TempDir()
				log := filepath.Join(dir, "loop-turn.log")
				var configs []ExtConfig
				for _, ext := range []struct{ name, source string }{{"first", loopTurnFirstSource}, {"second", second}} {
					entry := filepath.Join(dir, ext.name+".mjs")
					if err := os.WriteFile(entry, fmt.Appendf(nil, ext.source, strconv.Quote(log)), 0o600); err != nil {
						t.Fatal(err)
					}
					configs = append(configs, ExtConfig{Name: ext.name, Source: entry, Enabled: true})
				}
				host := NewHost(t.TempDir())
				host.SetMode(mode)
				host.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
				t.Cleanup(func() { host.Shutdown("test done") })
				ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
				t.Cleanup(cancel)
				loaded, errs := host.LoadAll(ctx, configs)
				if len(errs) != 0 || len(loaded) != len(configs) || loaded[0].Name != "first" || loaded[1].Name != "second" {
					t.Fatalf("LoadAll = %+v, errors %v; want first, then second", loaded, errs)
				}
				for _, ext := range loaded {
					handlers := ext.EventHandlers("session_shutdown")
					if len(handlers) != 1 {
						t.Fatalf("%s session_shutdown handlers = %d, want 1", ext.Name, len(handlers))
					}
					if _, err := handlers[0](map[string]any{"type": "session_shutdown", "reason": "quit"}); err != nil {
						t.Fatalf("%s session_shutdown: %v", ext.Name, err)
					}
				}
				host.Invalidate("")
				want := []string{"first session_shutdown", "second session_shutdown", "first immediate live", "second session_shutdown done", "first timeout stale"}
				f := &promptEndFixture{t: t, host: host, log: log, ctx: ctx}
				if got := f.lines(len(want)); strings.Join(got, "\n") != strings.Join(want, "\n") {
					t.Fatalf("order:\n got %q\nwant %q", got, want)
				}
			})
		}
	}
}
