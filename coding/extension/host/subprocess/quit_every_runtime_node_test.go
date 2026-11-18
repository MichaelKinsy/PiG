//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

const (
	quitSilentSource = `export default function (pi) { pi.on("session_shutdown", () => new Promise(() => {})); }`
	quitIdleSource   = `export default function (pi) { pi.registerCommand("idle", { description: "nothing", handler: async () => {} }); }`
	// quitTimerSource arms a 3 s timer in its session_shutdown handler and returns.
	quitTimerSource = `export default function (pi) { pi.on("session_shutdown", () => { setTimeout(() => {}, 3000); }); }`
)

// startQuitRuntimes loads the sources as real Node runtimes, each isolated in a process of its own, in the order of names, and returns the host, the loaded
// extensions by name and a channel that closes when the host's drain handler runs.
func startQuitRuntimes(t *testing.T, names []string, sources map[string]string) (*Host, map[string]*extension.Extension, <-chan struct{}, context.Context) {
	return startQuitRuntimesIn(t, "isolated", names, sources)
}

// startQuitRuntimesIn is startQuitRuntimes with the isolation of every extension named: "shared-ok" packs the Node extensions into one process.
func startQuitRuntimesIn(t *testing.T, isolation string, names []string, sources map[string]string) (*Host, map[string]*extension.Extension, <-chan struct{}, context.Context) {
	t.Helper()
	nodeCellRequireNode(t)
	dir := t.TempDir()
	h := NewHost(t.TempDir())
	h.SetMode("rpc")
	t.Cleanup(func() { h.Shutdown("test done") })
	drained := make(chan struct{})
	var once sync.Once
	h.SetRuntimeDrainHandler(func() { once.Do(func() { close(drained) }) })
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	t.Cleanup(cancel)
	var configs []ExtConfig
	for _, name := range names {
		path := filepath.Join(dir, name+".mjs")
		if err := os.WriteFile(path, []byte(sources[name]), 0o600); err != nil {
			t.Fatal(err)
		}
		configs = append(configs, ExtConfig{Name: name, Source: path, Enabled: true, Isolation: isolation})
	}
	loaded, errs := h.LoadAll(ctx, configs)
	if len(errs) != 0 || len(loaded) != len(names) {
		t.Fatalf("LoadAll = %d extensions, errors %v", len(loaded), errs)
	}
	exts := map[string]*extension.Extension{}
	for i := range loaded {
		exts[loaded[i].Name] = &loaded[i]
	}
	return h, exts, drained, ctx
}

// quitShutdownHandler is the one session_shutdown handler of ext.
func quitShutdownHandler(t *testing.T, ext *extension.Extension) extension.HandlerFn {
	t.Helper()
	handlers := ext.EventHandlers("session_shutdown")
	if len(handlers) != 1 {
		t.Fatalf("session_shutdown handlers of %s = %d, want 1", ext.Name, len(handlers))
	}
	return handlers[0]
}

// TestQuitDrainLastsAsLongAsAnotherNodeProcessHoldsATimer: Pi 1.5.2 under tmux, a session_shutdown handler that never settles beside an extension whose own
// session_shutdown handler armed a 3 s timer: the process exits 0 after 3.09 s, when the timer ended (RPC stdin close: 3.4 s with the start). Not at once, and not
// never. With the two extensions in two Node processes the host acted at once on the drained loop of the silent handler's process.
func TestQuitDrainLastsAsLongAsAnotherNodeProcessHoldsATimer(t *testing.T) {
	t.Parallel()
	h, exts, drained, ctx := startQuitRuntimes(t, []string{"timer", "silent"}, map[string]string{"timer": quitTimerSource, "silent": quitSilentSource})
	event := map[string]any{"type": "session_shutdown", "reason": "quit"}
	arm, silent := quitShutdownHandler(t, exts["timer"]), quitShutdownHandler(t, exts["silent"])
	h.EndInput()
	// The timer is armed inside the first handler, so it ends no earlier than 3 s from here.
	before := time.Now()
	if _, err := arm(event); err != nil {
		t.Fatalf("the timer extension's session_shutdown: %v", err)
	}
	go func() { _, _ = silent(event) }()
	select {
	case <-drained:
	case <-ctx.Done():
		t.Fatalf("the host never acted: the process whose timer ended did not report its drained loop\n%s", quitHoldState(h))
	}
	held := time.Since(before)
	if held < 3*time.Second {
		t.Fatalf("the host acted on a drained loop after %s while another Node process held a 3 s timer, want no earlier than 3 s", held)
	}
	t.Logf("the quit was held %s by the 3 s timer of the second process", held)
}

// TestQuitDrainCountsAnIdleNodeProcess: a Node process with nothing unanswered and nothing held does not hold the quit: it reports its drained loop itself once input
// ended. The logged times are from input's end to the host's act, alone and beside idle processes.
func TestQuitDrainCountsAnIdleNodeProcess(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		names []string
	}{
		{"the silent process alone", []string{"silent"}},
		{"beside an idle process", []string{"idle", "silent"}},
		{"beside two idle processes", []string{"idle", "idle-two", "silent"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h, exts, drained, ctx := startQuitRuntimes(t, tc.names, map[string]string{"idle": quitIdleSource, "idle-two": quitIdleSource, "silent": quitSilentSource})
			silent := quitShutdownHandler(t, exts["silent"])
			before := time.Now()
			h.EndInput()
			go func() { _, _ = silent(map[string]any{"type": "session_shutdown", "reason": "quit"}) }()
			select {
			case <-drained:
			case <-ctx.Done():
				t.Fatalf("the host never acted: an idle Node process did not report its drained loop\n%s", quitHoldState(h))
			}
			t.Logf("quit-drain time, %s: %s", tc.name, time.Since(before))
		})
	}
}

// quitRuntimeScript runs body in Node against runtime.mjs: one runtime over a connection whose frames the script records and feeds. loopDrained is what Node does
// when nothing keeps the event loop alive: it emits beforeExit.
func quitRuntimeScript(t *testing.T, body string) {
	t.Helper()
	nodeCellRequireNode(t)
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`import assert from "node:assert/strict";
import { Runtime } from %q;
const frames = [];
const runtime = new Runtime("/ext/idle.mjs");
const queue = [];
let wake;
runtime.connect = async () => {
  const conn = runtime.attachConnection({ on() {}, off() {}, write() { return true; }, waitUntil() {} });
  conn.send = (...envs) => { frames.push(...envs); return true; };
  conn.next = () => (queue.length ? Promise.resolve(queue.shift()) : new Promise((resolve) => { wake = resolve; }));
  runtime.conn = conn;
};
const feed = (env) => { if (wake) { const w = wake; wake = undefined; w(env); } else queue.push(env); };
const turn = async () => { for (let i = 0; i < 6; i++) await new Promise((resolve) => setImmediate(resolve)); await new Promise((resolve) => setTimeout(resolve, 5)); };
const reports = () => frames.filter((f) => f.type === "notify" && f.notify.method.startsWith("runtime_")).map((f) => f.notify.method);
const loopDrained = () => process.emit("beforeExit", 0);
const command = (id, name) => feed({ type: "request", id, request: { method: "command", tool: name, args: "" } });
const inputEnd = () => feed({ type: "notify", notify: { method: "runtime_input_end" } });
runtime.commands.set("quick", { handler: async () => {} });
runtime.commands.set("never", { handler: () => new Promise(() => {}) });
void runtime.run();
feed({ type: "ready", ready: { mode: "rpc", models: [], cwd: "/" } });
await turn();
%s
process.exit(0);
`, (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String(), body)
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}

// A Node runtime with nothing unanswered reports its drained loop (runtime_commands_drained, which then covers no command) only after runtime_input_end: a live
// session's idle runtime sends nothing. It reports once per idle period, and again after it answered a later request.
func TestNodeRuntimeReportsAnIdleLoopOnlyAfterInputEnd(t *testing.T) {
	quitRuntimeScript(t, `
loopDrained();
command("r1", "quick");
await turn();
loopDrained();
assert.deepEqual(reports(), [], "a live session's idle runtime reports nothing");
inputEnd();
await turn();
loopDrained();
assert.deepEqual(reports(), ["runtime_commands_drained"], "after input ended an idle runtime reports its drained loop");
loopDrained();
assert.deepEqual(reports(), ["runtime_commands_drained"], "one report per idle period");
command("r2", "quick");
await turn();
loopDrained();
assert.deepEqual(reports(), ["runtime_commands_drained", "runtime_commands_drained"], "it reports again after it answered a later request");
`)
}

// A packed cell is one process with one loop, so the report is the process's and not an extension's: a member that holds a timer keeps the loop alive for every
// member and nothing is reported (Node emits no beforeExit), and when the loop drains the process reports on every connection it hosts, the busy member's and the
// idle member's alike, once. An extension sees none of it, packed or isolated.
func TestPackedNodeCellReportsItsDrainedLoopOnEveryConnection(t *testing.T) {
	nodeGroupScript(t, `
const sent = [];
const a = member("a", "");
const b = member("b", "");
for (const [name, runtime] of [["a", a], ["b", b]]) runtime.conn.notify = (method) => { if (String(method).startsWith("runtime_")) sent.push(name + " " + method); };
process.emit("beforeExit", 0);
assert.deepEqual(sent, [], "before input ended the process reports nothing");
a.requestRecords.set("r1", { responded: false });
a.handleNotify({ method: "runtime_input_end" });
process.emit("beforeExit", 0);
assert.deepEqual(sent, ["a runtime_commands_drained", "b runtime_commands_drained"], "the drained process reports on every connection it hosts, with a member's request unanswered");
process.emit("beforeExit", 0);
assert.deepEqual(sent.length, 2, "one report per drained loop");
`)
}

// quitHoldState describes, for a failure message, what the host's quit evaluation sees of each extension, and the last lines each extension process wrote to its log.
func quitHoldState(h *Host) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var lines []string
	for name, me := range h.exts {
		conn := me.connection()
		if conn == nil {
			lines = append(lines, name+": no connection")
			continue
		}
		conn.pendingMu.Lock()
		var pending []string
		for id := range conn.pending {
			_, started := conn.started[id]
			pending = append(pending, fmt.Sprintf("%s started=%v", id, started))
		}
		line := fmt.Sprintf("%s: node=%v closed=%v loopDrained=%v pending=%v", name, me.hostsNodeRuntime(), conn.closed.Load(), conn.loopDrained, pending)
		conn.pendingMu.Unlock()
		if log, err := os.ReadFile(me.stderrLogPath); err == nil && len(log) > 0 {
			tail := strings.Split(strings.TrimSpace(string(log)), "\n")
			line += "\n    log: " + strings.Join(tail[max(0, len(tail)-14):], "\n    log: ")
		}
		lines = append(lines, line)
	}
	sort.Strings(lines)
	return fmt.Sprintf("a pending handler's process reported=%v, handed over=%v\n  %s", h.loopDrainPending.Load(), h.loopDrainActed, strings.Join(lines, "\n  "))
}
