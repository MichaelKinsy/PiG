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

// loopTurnFirstSource handles agent_settled and session_shutdown by queueing an immediate and a zero-delay timer, each of which records whether the ctx is still live when it runs. It records its process ID next to the log.
const loopTurnFirstSource = `import { appendFileSync } from "node:fs";
const LOG = %s;
const record = (line) => appendFileSync(LOG, line + "\n");
const state = (ctx) => {
  try { ctx.cwd; return "live"; } catch (error) { return String(error?.message).includes("stale") ? "stale" : "threw " + error?.message; }
};
export default function (pi) {
  appendFileSync(LOG + ".pid", process.pid + "\n");
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

// loadLoopTurnPair loads loopTurnFirstSource and then second in mode, in layout: one packed process, isolated processes, or quarantined. quarantined is the layout Pig's own crash recovery gives Node factories: each in a packed cell of its own process (planCells), which is how pig -p comes to run Node factories in separate processes.
func loadLoopTurnPair(t *testing.T, layout, mode, second string) (*promptEndFixture, []extension.Extension) {
	t.Helper()
	nodeCellRequireNode(t)
	isolation := ""
	if layout == "isolated" {
		isolation = layout
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "loop-turn.log")
	var configs []ExtConfig
	for _, ext := range []struct{ name, source string }{{"first", loopTurnFirstSource}, {"second", second}} {
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
	return &promptEndFixture{t: t, host: host, log: log, ctx: ctx}, loaded
}

// runLoopTurnEvent runs each loaded extension's event handler in turn and waits for it, as Pi's runner awaits each handler, calls between, when given, once the first handler answered, and then invalidates the runtime, as print mode's disposal does.
func runLoopTurnEvent(f *promptEndFixture, loaded []extension.Extension, event string, between func()) {
	f.t.Helper()
	for i, ext := range loaded {
		if i == 1 && between != nil {
			between()
		}
		handlers := ext.EventHandlers(event)
		if len(handlers) != 1 {
			f.t.Fatalf("%s %s handlers = %d, want 1", ext.Name, event, len(handlers))
		}
		if _, err := handlers[0](map[string]any{"type": event, "reason": "quit"}); err != nil {
			f.t.Fatalf("%s %s: %v", ext.Name, event, err)
		}
	}
	f.host.Invalidate("")
}
