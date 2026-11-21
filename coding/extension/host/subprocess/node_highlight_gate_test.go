//go:build !pig_strip_node_extensions

package subprocess

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// pig additive (D20): the registration pass of the highlight.js load holds the thread, so it waits for the host's requests (HighlightLoadGate).
// The gate runs here on a clock and an event-loop turn the script moves, so every case is an order of events and none waits for time:
//   - a request in flight when the grammars are imported is answered before the pass, also once the idle delay has passed;
//   - a request queued behind the answered one, or read in the turn of the answer, is answered first too;
//   - an extension the host asks nothing gets the languages when the idle delay has passed, not before;
//   - requests that leave no gap do not keep the languages away for longer than the bound;
//   - a request answered before the grammars were imported lets the pass start at once, as before the gate.
//
// Then the real load runs behind a gate: with a request in flight the grammars are imported and the pass has not run; a highlight call made
// then answers as before the load (an eager grammar highlights, a deferred one is not there); the answer opens the gate, the pass registers
// every language in one turn, and the deferred grammar highlights.
func TestNodeHighlightRegistrationWaitsForHostRequests(t *testing.T) {
	nodeCellRequireNode(t)
	root := filepath.Join(findModuleRoot(t), "coding", "extension", "host", "subprocess", "runtime-node")
	script := `import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
const root = pathToFileURL(process.argv[1] + "/");
const { HighlightLoadGate } = await import(new URL("runtime.mjs", root));
const hljs = (await import(new URL("shims/highlight.js/lib/core.js", root))).default;
const { loadAllHighlightLanguages, supportsLanguage, highlight, getHighlightLanguageCount } = await import(new URL("shims/syntax-highlight.mjs", root));
const realTurn = () => new Promise((resolve) => setImmediate(resolve));

// A clock and a turn the script moves.
function world(outstanding) {
  const w = { now: 0, timers: [], deferred: [], outstanding, opened: false };
  w.options = {
    setTimer: (fn, ms) => { const timer = { fn, at: w.now + ms }; w.timers.push(timer); return timer; },
    clearTimer: (timer) => { const i = w.timers.indexOf(timer); if (i >= 0) w.timers.splice(i, 1); },
    defer: (fn) => w.deferred.push(fn),
  };
  w.gate = new HighlightLoadGate(() => w.outstanding, w.options);
  w.turn = async () => { for (const fn of w.deferred.splice(0)) fn(); await realTurn(); return w.opened; };
  w.advance = async (ms) => {
    w.now += ms;
    for (const timer of w.timers.filter((timer) => timer.at <= w.now).sort((a, b) => a.at - b.at)) { w.options.clearTimer(timer); timer.fn(); }
    return w.turn();
  };
  w.wait = () => { void w.gate.wait().then(() => { w.opened = true; }); return w.turn(); };
  return w;
}

{ // in flight when the grammars are imported
  const w = world(true);
  assert.equal(await w.wait(), false, "the pass started with a request in flight");
  assert.equal(await w.advance(250), false, "the idle delay started the pass with a request in flight");
  w.outstanding = false;
  w.gate.requestAnswered();
  assert.notEqual(w.gate.open, undefined, "the pass started in the turn of the answer");
  assert.equal(await w.turn(), true, "the pass did not start after the request was answered");
  assert.equal(w.timers.length, 0, "the opened gate left a delay running");
}
{ // a request queued behind the answered one
  const w = world(true);
  await w.wait();
  w.gate.requestAnswered();
  assert.equal(await w.turn(), false, "the pass started ahead of a request queued behind the answered one");
  w.outstanding = false;
  w.gate.requestAnswered();
  assert.equal(await w.turn(), true, "the pass did not start after the last request was answered");
}
{ // a request read in the turn of the answer
  const w = world(true);
  await w.wait();
  w.outstanding = false;
  w.gate.requestAnswered();
  w.outstanding = true;
  assert.equal(await w.turn(), false, "the pass started ahead of a request read in the turn of the answer");
  w.outstanding = false;
  w.gate.requestAnswered();
  assert.equal(await w.turn(), true, "the pass did not start after that request was answered");
}
{ // the host asks nothing
  const w = world(false);
  assert.equal(await w.wait(), false, "the pass started before any request and before the idle delay");
  assert.equal(await w.advance(249), false, "the pass started before the idle delay");
  assert.equal(await w.advance(1), true, "an extension the host asks nothing did not get the languages after the idle delay");
}
{ // requests that leave no gap
  const w = world(true);
  await w.wait();
  for (let i = 0; i < 5; i++) { w.gate.requestAnswered(); assert.equal(await w.turn(), false, "the pass started with a request outstanding"); }
  assert.equal(await w.advance(1999), false, "the pass started before the bound with a request outstanding");
  assert.equal(await w.advance(1), true, "requests that leave no gap kept the languages away past the bound");
}
{ // answered before the grammars were imported
  const w = world(false);
  w.gate.requestAnswered();
  assert.equal(await w.wait(), true, "a request answered before the grammars were imported did not let the pass start");
}

// The real load behind a gate.
const theme = { keyword: (text) => "[" + text + "]" };
const ada = "procedure Hello is begin null; end Hello;";
const eager = getHighlightLanguageCount();
const w = world(true);
const events = [];
const sizes = new Set();
let done = false;
const watch = () => { sizes.add(hljs.listLanguages().length); if (!done) setImmediate(watch); };
setImmediate(watch);
const load = loadAllHighlightLanguages(() => { events.push("grammars imported"); return w.gate.wait().then(() => { events.push("gate opened"); }); });
while (!events.includes("grammars imported")) await realTurn();
for (let i = 0; i < 3; i++) await w.turn();
const before = { count: getHighlightLanguageCount() - eager, ada: supportsLanguage("ada"), js: highlight("const a = 1;", { language: "javascript", theme }) };
let adaBefore;
try { adaBefore = highlight(ada, { language: "ada", theme }); } catch (error) { adaBefore = "throws: " + error.message; }
events.push("request answered");
w.outstanding = false;
w.gate.requestAnswered();
await w.turn();
await load;
done = true;
sizes.add(hljs.listLanguages().length);
console.log(JSON.stringify({ events, before, adaBefore, eager, sizes: [...sizes], after: { count: getHighlightLanguageCount(), ada: supportsLanguage("ada"), adaText: highlight(ada, { language: "ada", theme }) } }));
`
	cmd := exec.CommandContext(t.Context(), "node", "--import", registerLoaderURL(t, root), "--input-type=module", "--eval", script, root)
	// highlight.js also writes its unknown-language error to stderr.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("highlight gate: %v\n%s%s", err, output, stderr.Bytes())
	}
	var got struct {
		Events []string `json:"events"`
		Before struct {
			Count int    `json:"count"`
			Ada   bool   `json:"ada"`
			JS    string `json:"js"`
		} `json:"before"`
		AdaBefore string `json:"adaBefore"`
		Eager     int    `json:"eager"`
		Sizes     []int  `json:"sizes"`
		After     struct {
			Count   int    `json:"count"`
			Ada     bool   `json:"ada"`
			AdaText string `json:"adaText"`
		} `json:"after"`
	}
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("highlight gate output: %v\n%s", err, output)
	}
	if want := "grammars imported,request answered,gate opened"; strings.Join(got.Events, ",") != want {
		t.Fatalf("events = %v, want %s", got.Events, want)
	}
	// With the request in flight: the eager set, as before the load.
	if got.Before.Count != 0 || got.Before.Ada || got.Before.JS != "[const] a = 1;" || !strings.HasPrefix(got.AdaBefore, "throws: ") {
		t.Fatalf("a highlight call before the pass = %+v, ada %q; want the eager set only, JavaScript highlighted, ada unknown", got.Before, got.AdaBefore)
	}
	if got.After.Count <= got.Eager || !got.After.Ada || !strings.HasPrefix(got.After.AdaText, "[procedure]") {
		t.Fatalf("after the pass = %+v (eager %d); want every language and ada highlighted", got.After, got.Eager)
	}
	// One pass: no turn saw some of the languages and not the rest.
	for _, size := range got.Sizes {
		if size != got.Eager && size != got.After.Count {
			t.Fatalf("a turn saw %d languages; want %d before the pass or %d after it", size, got.Eager, got.After.Count)
		}
	}
}

const highlightGateFixture = `import { pathToFileURL } from "node:url";
import vm from "node:vm";
const turn = () => new Promise((resolve) => setImmediate(resolve));
// This file is evaluated by jiti, whose import() would evaluate a second copy of a runtime module: the main context's loader returns the running one.
const nodeImport = (name) => new vm.Script("import(" + JSON.stringify(new URL(name, pathToFileURL(process.argv[1])).href) + ")", { importModuleDynamically: vm.constants.USE_MAIN_CONTEXT_DEFAULT_LOADER }).runInThisContext();
export default function (pi) {
  let gate, languages, eager;
  const report = [];
  pi.registerMessageRenderer("probe", async () => {
    ({ highlightLoadGate: gate } = await nodeImport("../runtime.mjs"));
    languages = await nodeImport("../shims/syntax-highlight.mjs");
    // The clock of this test never advances: only an answered request opens the gate.
    gate.setTimer = () => undefined;
    eager = languages.getHighlightLanguageCount();
    report.push("before=ada:" + languages.supportsLanguage("ada") + ",js:" + languages.highlight("const a = 1;", { language: "javascript", theme: { keyword: (text) => "[" + text + "]" } }));
    while (gate.waiting === undefined && languages.getHighlightLanguageCount() === eager) await turn();
    for (let i = 0; i < 3; i++) await turn();
    report.push("render=reached:" + (gate.waiting !== undefined) + ",held:" + (gate.open !== undefined) + ",new:" + (languages.getHighlightLanguageCount() - eager));
    return { render: () => ["probe"], invalidate() {} };
  });
  pi.registerCommand("second", { handler: async () => {
    while (!gate?.answered) await turn();
    for (let i = 0; i < 3; i++) await turn();
    report.push("second=held:" + (gate.open !== undefined) + ",new:" + (languages.getHighlightLanguageCount() - eager));
    const watch = () => {
      if (languages.getHighlightLanguageCount() === eager) return void setImmediate(watch);
      report.push("after=ada:" + languages.supportsLanguage("ada") + ",more:" + (languages.getHighlightLanguageCount() > eager));
      pi.appendEntry("languages", { report: report.join(";") });
    };
    setImmediate(watch);
  } });
}`

// The gate in a running runtime, at the wire. The host sends a render request and a command right after ready. The renderer stays in flight until
// the grammars are imported: the pass has not run then, and a highlight call answers from the eager set. The render request is answered with the
// command still in flight: the pass still has not run. The command is answered, and only then do the languages arrive (the extension tells
// the host with an entry). The fixture stops the gate's clock, so nothing here waits for time.
func TestNodeRenderRequestAfterReadyIsAnsweredBeforeTheHighlightRegistration(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node unavailable: %v", err)
	}
	root := testenv.ShortTempDir(t, "pig-hl-gate-")
	entry := filepath.Join(root, "highlight_gate.mjs")
	if err := os.WriteFile(entry, []byte(highlightGateFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, address, err := ListenExtension(filepath.Join(root, "runtime.sock"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	cmd := exec.CommandContext(t.Context(), node, filepath.Join("runtime-node", "cli.mjs"), entry)
	cmd.Env = append(os.Environ(), "PIG_EXT_SOCKET="+address, "PIG_EXT_NAME=node-highlight-gate")
	peer := startAndAccept(t, cmd, listener)
	t.Cleanup(func() { _ = peer.Close() })
	if err := peer.SetReadDeadline(time.Now().Add(testbudget.Wait(t))); err != nil {
		t.Fatal(err)
	}
	if register := readLivenessEnvelope(t, peer); register.Type != MsgRegister {
		t.Fatalf("register = %+v", register)
	}
	writeSignalOnlyFrames(t, peer,
		Envelope{Type: MsgReady, Ready: &ReadyPayload{Cwd: root, Width: 80, Mode: "tui", State: &StatePayload{HasUI: true}}},
		Envelope{Type: MsgRequest, ID: "req-render", Request: &RequestPayload{Method: "render_message", Tool: "probe", Args: json.RawMessage(`{"key":"render-1","message":{"customType":"probe","content":"x","display":true},"options":{"expanded":false},"width":80,"rerender":true}`)}},
		Envelope{Type: MsgRequest, ID: "req-second", Request: &RequestPayload{Method: "command", Tool: "second"}},
	)
	var order []string
	for {
		env := readSignalOnlyFrame(t, peer)
		if env.Type == MsgResponse {
			if env.Response == nil {
				t.Fatalf("request %s has no response payload", env.ID)
			}
			if env.Response.Error != nil {
				t.Fatalf("request %s failed: %+v", env.ID, env.Response.Error)
			}
			order = append(order, env.ID)
			continue
		}
		if env.Type != MsgCall || env.Call == nil || env.Call.Method != "appendEntry" || !strings.Contains(string(env.Call.Args), "languages") {
			continue
		}
		order = append(order, "languages")
		args := string(env.Call.Args)
		for _, want := range []string{
			"before=ada:false,js:[const] a = 1;",
			"render=reached:true,held:true,new:0",
			"second=held:true,new:0",
			"after=ada:true,more:true",
		} {
			if !strings.Contains(args, want) {
				t.Errorf("the extension's report lacks %q: %s", want, args)
			}
		}
		break
	}
	if want := "req-render,req-second,languages"; strings.Join(order, ",") != want {
		t.Fatalf("order at the host = %v, want %s", order, want)
	}
}
