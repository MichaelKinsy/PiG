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

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// promptEndSource records each call that can end a print-mode prompt, and session_shutdown, then queues an immediate that records whether the ctx was still live when it ran. session_shutdown also records which of those immediates had not run yet.
const promptEndSource = `import { appendFileSync } from "node:fs";
const LOG = %s;
const record = (line) => appendFileSync(LOG, line + "\n");
const pending = new Set();
const state = (ctx) => {
  try { ctx.cwd; return "live"; } catch (error) { return String(error?.message).includes("stale") ? "stale" : "threw " + error?.message; }
};
const queue = (name, ctx) => {
  pending.add(name);
  setImmediate(() => { pending.delete(name); record(name + "-immediate " + state(ctx)); });
};
export default function (pi) {
  appendFileSync(LOG + ".pid", process.pid + "\n");
  pi.registerCommand("prompt-end", { description: "ends a prompt", handler: async (_args, ctx) => { record("command"); queue("command", ctx); } });
  pi.on("input", (_event, ctx) => { record("input"); queue("input", ctx); return { action: "handled" }; });
  pi.on("agent_settled", (_event, ctx) => { record("agent_settled"); queue("agent_settled", ctx); });
  pi.on("session_shutdown", (_event, ctx) => { record("session_shutdown pending=" + [...pending].join(",")); queue("session_shutdown", ctx); });
}
`

// slowSource registers a command that records its start in the file it is given, then awaits a timer, so its answer needs the event loop of its process.
const slowSource = `import { appendFileSync } from "node:fs";
export default function (pi) {
  pi.registerCommand("slow", { description: "awaits a timer", handler: async () => {
    appendFileSync(%s, "slow\n");
    await new Promise((resolve) => setTimeout(resolve, 100));
  } });
}
`

type promptEndFixture struct {
	t    *testing.T
	host *Host
	ext  extension.Extension
	// slow is the extension loaded from slowSource, when the fixture has one.
	slow extension.Extension
	log  string
	ctx  context.Context
}

// startPromptEnd loads the prompt-end extension, and with slow also slowSource, in one Host in mode. An empty isolation packs them into one Node process.
func startPromptEnd(t *testing.T, mode, isolation string, slow bool) *promptEndFixture {
	t.Helper()
	nodeCellRequireNode(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "prompt-end.log")
	entry := filepath.Join(dir, "prompt-end.mjs")
	if err := os.WriteFile(entry, fmt.Appendf(nil, promptEndSource, strconv.Quote(log)), 0o600); err != nil {
		t.Fatal(err)
	}
	configs := []ExtConfig{{Name: "prompt-end", Source: entry, Enabled: true, Isolation: isolation}}
	if slow {
		slowEntry := filepath.Join(dir, "slow.mjs")
		if err := os.WriteFile(slowEntry, fmt.Appendf(nil, slowSource, strconv.Quote(log+".slow")), 0o600); err != nil {
			t.Fatal(err)
		}
		configs = append(configs, ExtConfig{Name: "slow", Source: slowEntry, Enabled: true, Isolation: isolation})
	}
	host := NewHost(t.TempDir())
	host.SetMode(mode)
	host.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	t.Cleanup(func() { host.Shutdown("test done") })
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	t.Cleanup(cancel)
	loaded, errs := host.LoadAll(ctx, configs)
	if len(errs) != 0 || len(loaded) != len(configs) {
		t.Fatalf("LoadAll = %d extensions, errors %v", len(loaded), errs)
	}
	f := &promptEndFixture{t: t, host: host, log: log, ctx: ctx}
	for _, ext := range loaded {
		if ext.Name == "slow" {
			f.slow = ext
		} else {
			f.ext = ext
		}
	}
	return f
}

// pid is the process that runs the prompt-end extension.
func (f *promptEndFixture) pid() int {
	f.t.Helper()
	data, err := os.ReadFile(f.log + ".pid")
	if err != nil {
		f.t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		f.t.Fatal(err)
	}
	return pid
}

func (f *promptEndFixture) emit(event string, payload map[string]any) {
	f.t.Helper()
	handlers := f.ext.EventHandlers(event)
	if len(handlers) != 1 {
		f.t.Fatalf("%s handlers = %d, want 1", event, len(handlers))
	}
	payload["type"] = event
	if _, err := handlers[0](payload); err != nil {
		f.t.Fatalf("%s: %v", event, err)
	}
}

// end runs the call that ends the prompt.
func (f *promptEndFixture) end(ending string) {
	f.t.Helper()
	switch ending {
	case "command":
		if err := f.ext.Commands["prompt-end"].Handler(f.ctx, ""); err != nil {
			f.t.Fatalf("command: %v", err)
		}
	case "input":
		f.emit("input", map[string]any{"text": "handled input", "source": "user"})
	default:
		f.emit(ending, map[string]any{})
	}
}

// lines waits until the log holds n lines and returns them.
func (f *promptEndFixture) lines(n int) []string {
	f.t.Helper()
	for {
		data, _ := os.ReadFile(f.log)
		got := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		if len(data) > 0 && len(got) >= n {
			return got
		}
		select {
		case <-f.ctx.Done():
			f.t.Fatalf("log never reached %d lines: %q", n, got)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

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

// loopTurnFirstSource handles agent_settled and session_shutdown by queueing an immediate and a zero-delay timer, each of which records whether the ctx is still live when it runs.
const loopTurnFirstSource = `import { appendFileSync } from "node:fs";
const LOG = %s;
const record = (line) => appendFileSync(LOG, line + "\n");
const state = (ctx) => {
  try { ctx.cwd; return "live"; } catch (error) { return String(error?.message).includes("stale") ? "stale" : "threw " + error?.message; }
};
export default function (pi) {
  for (const event of ["agent_settled", "session_shutdown"]) {
    pi.on(event, (_event, ctx) => {
      record("first " + event);
      setImmediate(() => record("first immediate " + state(ctx)));
      setTimeout(() => record("first timeout " + state(ctx)), 0);
    });
  }
}
`

// loopTurnSecondSource handles the same events by awaiting a timer, which turns Pi's event loop.
const loopTurnSecondSource = `import { appendFileSync } from "node:fs";
const LOG = %s;
const record = (line) => appendFileSync(LOG, line + "\n");
export default function (pi) {
  for (const event of ["agent_settled", "session_shutdown"]) {
    pi.on(event, async () => {
      record("second " + event);
      await new Promise((resolve) => setTimeout(resolve, 100));
      record("second " + event + " done");
    });
  }
}
`

// Pi's runner emits an event to each extension in turn and awaits each handler, and print mode invalidates the runner only after session_shutdown (agent-session-runtime.ts:404-410, agent-session.ts:1393-1406). When a later extension's handler awaits a timer or I/O, Pi's one event loop turns and runs the immediate and the timer that an earlier extension's handler queued, with a live ctx. Probed with Pi 1.1.0 -p (extensions-runtime/75-print-shutdown-loop-turn): "a session_shutdown", "b session_shutdown", "a immediate live", "a timeout live", "b session_shutdown done", when b awaits 0, 1 or 20 ms. A packed process shares the event loop. An isolated process learns of the turn from loop_turned: the other runtime sends it when its handler's window closes unanswered, and the host forwards it to every other Node process before it reads that handler's response.
func TestNodePromptEndWaitEndsWhenAnotherProcessTurnsTheLoop(t *testing.T) {
	t.Parallel()
	// quarantined is the layout Pig's own crash recovery gives Node factories: each in a packed cell of its own process (planCells), which is how pig -p comes to run Node factories in separate processes.
	for _, layout := range []string{"packed", "isolated", "quarantined"} {
		isolation := ""
		if layout == "isolated" {
			isolation = layout
		}
		for _, mode := range []string{"print", "json"} {
			for _, event := range []string{"session_shutdown", "agent_settled"} {
				t.Run(layout+"/"+mode+"/"+event, func(t *testing.T) {
					t.Parallel()
					nodeCellRequireNode(t)
					dir := t.TempDir()
					log := filepath.Join(dir, "loop-turn.log")
					var configs []ExtConfig
					for _, ext := range []struct{ name, source string }{{"first", loopTurnFirstSource}, {"second", loopTurnSecondSource}} {
						entry := filepath.Join(dir, ext.name+".mjs")
						if err := os.WriteFile(entry, fmt.Appendf(nil, ext.source, strconv.Quote(log)), 0o600); err != nil {
							t.Fatal(err)
						}
						configs = append(configs, ExtConfig{Name: ext.name, Source: entry, Enabled: true, Isolation: isolation})
					}
					host := NewHost(t.TempDir())
					host.SetMode(mode)
					host.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
					if layout == "quarantined" {
						host.nodeFaults = map[string]string{"first": "crashed"}
					}
					t.Cleanup(func() { host.Shutdown("test done") })
					ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
					t.Cleanup(cancel)
					loaded, errs := host.LoadAll(ctx, configs)
					if len(errs) != 0 || len(loaded) != len(configs) || loaded[0].Name != "first" || loaded[1].Name != "second" {
						t.Fatalf("LoadAll = %+v, errors %v; want first, then second", loaded, errs)
					}
					if layout == "quarantined" {
						host.mu.Lock()
						first, second := host.exts["first"].packedProcess, host.exts["second"].packedProcess
						host.mu.Unlock()
						if first == nil || second == nil || first == second {
							t.Fatalf("packed processes = %p, %p; want two", first, second)
						}
					}
					for _, ext := range loaded {
						handlers := ext.EventHandlers(event)
						if len(handlers) != 1 {
							t.Fatalf("%s %s handlers = %d, want 1", ext.Name, event, len(handlers))
						}
						if _, err := handlers[0](map[string]any{"type": event, "reason": "quit"}); err != nil {
							t.Fatalf("%s %s: %v", ext.Name, event, err)
						}
					}
					host.Invalidate("")
					want := []string{"first " + event, "second " + event, "first immediate live", "first timeout live", "second " + event + " done"}
					f := &promptEndFixture{t: t, host: host, log: log, ctx: ctx}
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
