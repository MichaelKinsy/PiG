package subprocess

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// xrefFixture loads each source as a Node extension with the given isolation and returns the loaded extensions by name.
func xrefFixture(t *testing.T, isolation []string, sources ...string) (*Host, map[string]extension.Extension) {
	t.Helper()
	return xrefFixtureB(t, isolation, sources...)
}

func xrefFixtureB(t testing.TB, isolation []string, sources ...string) (*Host, map[string]extension.Extension) {
	t.Helper()
	nodeCellRequireNode(t)
	root := t.TempDir()
	var configs []ExtConfig
	for i, source := range sources {
		name := fmt.Sprintf("xref-%d", i)
		path := filepath.Join(root, name+".mjs")
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		configs = append(configs, ExtConfig{Name: name, Source: path, Enabled: true, Isolation: isolation[i]})
	}
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	loaded, errs := h.LoadAll(t.Context(), configs)
	if len(errs) != 0 || len(loaded) != len(configs) {
		t.Fatalf("loaded=%v errs=%v", loaded, errs)
	}
	byName := map[string]extension.Extension{}
	for _, ext := range loaded {
		byName[ext.Name] = ext
	}
	return h, byName
}

func xrefRunTool(t *testing.T, ext extension.Extension, tool string) string {
	t.Helper()
	registered, ok := ext.Tools[tool]
	if !ok {
		t.Fatalf("%s has no tool %s", ext.Name, tool)
	}
	result, err := registered.Definition.Execute(t.Context(), "xref-"+tool, json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	text, ok := result.(agent.AgentToolResult)
	if !ok {
		t.Fatalf("%s result %T", tool, result)
	}
	return text.Text()
}

func xrefTool(name, body string) string {
	return fmt.Sprintf(`pi.registerTool({name:%q,description:%q,parameters:{type:"object",properties:{}},async execute(){%s}});`, name, name, body)
}

// Pi event-bus.ts:15-27: emit passes the same object to each listener and runs each synchronous prefix before emit returns, so the emitter observes a foreign listener's increment. Strict isolation joins the shared bus (owner decision Q1 = B).
func TestXrefEventBusForeignPrefixMutationMatchesPi(t *testing.T) {
	t.Parallel()
	for _, isolation := range [][]string{{"isolated", "isolated"}, {"", "isolated"}, {"isolated", ""}} {
		t.Run(fmt.Sprintf("%q", isolation), func(t *testing.T) {
			_, exts := xrefFixture(t, isolation,
				`export default pi=>{`+xrefTool("remote_payload", `const data={n:0};pi.events.emit("increment",data);return {content:[{type:"text",text:JSON.stringify(data)}]};`)+`};`,
				`export default pi=>{pi.events.on("increment",data=>{data.n++;});};`,
			)
			if got := xrefRunTool(t, exts["xref-0"], "remote_payload"); got != `{"n":1}` {
				t.Fatalf("emitter observed %s, Pi observes {\"n\":1}", got)
			}
		})
	}
}

// piEventBusOracle runs factories against Pi's own createEventBus (the verbatim vendored module) in one heap, as Pi's resource loader does, and returns the named tool's text.
func piEventBusOracle(t *testing.T, tool string, sources ...string) string {
	t.Helper()
	nodeCellRequireNode(t)
	busPath, err := filepath.Abs("runtime-node/shims/pi-dist/pi-coding-agent/core/event-bus.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var paths []string
	for i, source := range sources {
		path := filepath.Join(dir, fmt.Sprintf("oracle-%d.mjs", i))
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	encoded, _ := json.Marshal(paths)
	script := fmt.Sprintf(`import { pathToFileURL } from "node:url";
// Pi's setupCli discards process warnings before it loads any extension (packages/coding-agent/src/cli/setup.ts:8).
process.emitWarning = () => {};
const { createEventBus } = await import(pathToFileURL(%q));
const bus = createEventBus();
const tools = {};
for (const path of %s) {
  const factory = (await import(pathToFileURL(path))).default;
  await factory({ events: { emit: (c, d) => bus.emit(c, d), on: (c, h) => bus.on(c, h) }, registerTool: def => { tools[def.name] = def; } });
}
const result = await tools[%q].execute("oracle", {});
process.stdout.write(result.content[0].text);
`, busPath, encoded, tool)
	out, err := exec.CommandContext(testbudget.Context(t), "node", "--input-type=module", "--eval", script).CombinedOutput()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, out)
	}
	return string(out)
}

var xrefBusScenario = []string{
	`export default pi => {
  let lateDone;
  const late = new Promise(resolve => { lateDone = resolve; });
  pi.events.on("late", resolve => lateDone(resolve));
  pi.registerTool({ name: "probe", description: "probe", parameters: { type: "object", properties: {} }, async execute() {
    class Point { #secret = 7; constructor(x) { this.x = x; } norm() { return this.x * this.#secret; } get doubled() { return this.x * 2; } }
    const log = [];
    const data = { n: 0, list: [1, 2], map: new Map([["k", 1]]), when: new Date(0), point: new Point(3), log, fn(a) { return a + this.n; } };
    data.self = data;
    data[Symbol.for("pig.shared")] = "g";
    Object.defineProperty(data, "fixed", { value: 42, enumerable: false, configurable: false, writable: false });
    pi.events.emit("probe", data);
    const afterEmit = { n: data.n, seen: data.seen === true, post: data.post ?? null };
    await Promise.resolve();
    const afterMicro = { post: data.post ?? null };
    await late;
    const afterMacro = { late: data.late === true };
    data.fromEmitter = "hello";
    pi.events.emit("check");
    data.cb("x");
    return { content: [{ type: "text", text: JSON.stringify({ afterEmit, afterMicro, afterMacro, log, retainedRead: data.retainedRead ?? null, reply: data.reply ?? null, cbSeen: data.cbSeen ?? null, keys: Object.keys(data) }) }] };
  } });
};`,
	`export default pi => {
  let saved;
  pi.events.on("probe", async data => {
    saved = data;
    data.n++;
    data.log.push(["L1", data.self === data, Array.isArray(data.list), data.list.length, JSON.stringify(data.list), data.map.get("k"), data.map instanceof Map, data.when.getTime(), data.when instanceof Date, data.point.norm(), data.point.doubled, data.fn(10), typeof data.fn, data[Symbol.for("pig.shared")], data.fixed, Object.getOwnPropertyDescriptor(data, "fixed").writable, "fixed" in data, Object.keys(data).length].join("|"));
    data.seen = true;
    data.cb = function (v) { this.cbSeen = v + ":" + (this === data); };
    await Promise.resolve();
    data.post = "L1";
    await new Promise(setImmediate);
    data.late = true;
    pi.events.emit("late");
  });
  pi.events.on("check", () => { saved.retainedRead = saved.fromEmitter; });
};`,
	`export default pi => {
  let first;
  pi.events.on("probe", d => { first = d; });
  pi.events.on("probe", d => { d.log.push("L2:" + (d === first) + ":" + d.n); pi.events.emit("reply", d); });
  pi.events.on("reply", d => { d.reply = "replied:" + d.seen; });
};`,
}

// Pi event-bus.ts:12-33 with one heap: identity, synchronous prefixes, reentrant emit, post-await and retained mutations, and non-JSON contents (private fields, accessors, Map/Date methods, symbols, non-configurable properties, callbacks with receivers). Every realm topology must produce Pi's exact observation.
func TestXrefEventBusMatchesPiAcrossRealms(t *testing.T) {
	t.Parallel()
	want := piEventBusOracle(t, "probe", xrefBusScenario...)
	for _, isolation := range [][]string{{"", "", ""}, {"isolated", "isolated", "isolated"}, {"", "isolated", ""}, {"isolated", "", ""}} {
		t.Run(fmt.Sprintf("%q", isolation), func(t *testing.T) {
			_, exts := xrefFixture(t, isolation, xrefBusScenario...)
			if got := xrefRunTool(t, exts["xref-0"], "probe"); got != want {
				t.Fatalf("PiG observation differs from Pi\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

var xrefBusOrderScenario = []string{
	`export default pi => {
  pi.registerTool({ name: "order", description: "order", parameters: { type: "object", properties: {} }, async execute() {
    const log = [];
    const offNew = pi.events.on("newListener", ch => log.push("new:" + ch));
    const offRemoved = pi.events.on("removeListener", ch => log.push("removed:" + ch));
    pi.events.emit("setup", log);
    pi.events.emit("tick", 1);
    pi.events.emit("unsub");
    pi.events.emit("tick", 2);
    const boom = new Error("boom");
    let thrown;
    try { pi.events.emit("error", boom); } catch (error) { thrown = error === boom; }
    offNew();
    offRemoved();
    offNew();
    pi.events.emit("tick", 3);
    return { content: [{ type: "text", text: JSON.stringify({ log, thrown }) }] };
  } });
};`,
	`export default pi => {
  let offTick;
  pi.events.on("setup", log => { offTick = pi.events.on("tick", n => { log.push("L1:" + n); if (n === 1) pi.events.emit("unsub2"); }); });
  pi.events.on("unsub", () => offTick());
};`,
	`export default pi => {
  let offTick;
  pi.events.on("setup", log => { offTick = pi.events.on("tick", n => { log.push("L2:" + n); }); });
  pi.events.on("unsub2", () => offTick());
};`,
}

// EventEmitter semantics behind Pi's bus: newListener before adding, removeListener after removing, a per-emit listener snapshot (a listener removed during an emit is still called by it), idempotent unsubscribe and the unhandled "error" rule, across realms.
func TestXrefEventBusListenerOrderMatchesPi(t *testing.T) {
	t.Parallel()
	want := piEventBusOracle(t, "order", xrefBusOrderScenario...)
	for _, isolation := range [][]string{{"", "", ""}, {"isolated", "isolated", "isolated"}, {"", "isolated", ""}} {
		t.Run(fmt.Sprintf("%q", isolation), func(t *testing.T) {
			_, exts := xrefFixture(t, isolation, xrefBusOrderScenario...)
			if got := xrefRunTool(t, exts["xref-0"], "order"); got != want {
				t.Fatalf("PiG observation differs from Pi\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

const xrefForceGC = `import v8 from "node:v8"; import vm from "node:vm"; v8.setFlagsFromString("--expose-gc"); const gc = vm.runInNewContext("gc");`

var xrefBusLifetimeScenario = []string{
	xrefForceGC + `
export default pi => {
  pi.registerTool({ name: "lifetime", description: "lifetime", parameters: { type: "object", properties: {} }, async execute() {
    let payload = { n: 0 };
    const ref = new WeakRef(payload);
    pi.events.emit("retain", payload);
    payload = undefined;
    for (let i = 0; i < 5; i++) { await new Promise(resolve => setTimeout(resolve, 10)); gc(); pi.events.emit("gc"); }
    await new Promise(resolve => setTimeout(resolve, 10));
    const alive = ref.deref() !== undefined;
    const seen = alive ? ref.deref().n : -1;
    pi.events.emit("drop");
    let collected = false;
    // WeakRef.deref keeps its target until the current job ends, so collection is observed from a later job than the check.
    for (let i = 0; i < 300 && !collected; i++) { await new Promise(resolve => setTimeout(resolve, 10)); gc(); pi.events.emit("gc"); await new Promise(resolve => setTimeout(resolve, 10)); collected = ref.deref() === undefined; }
    return { content: [{ type: "text", text: JSON.stringify({ alive, seen, collected }) }] };
  } });
};`,
	xrefForceGC + `
export default pi => {
  let saved;
  pi.events.on("retain", data => { saved = data; });
  pi.events.on("gc", () => { gc(); if (saved) saved.n++; });
  pi.events.on("drop", () => { saved = undefined; });
};`,
}

// A retained foreign alias keeps the emitter's original alive and live; once every realm drops it, the owner's export, the Host lease and the Node proxy are all collectable, as the object is in Pi's single heap.
func TestXrefEventBusPayloadLifetimeMatchesPi(t *testing.T) {
	t.Parallel()
	want := piEventBusOracle(t, "lifetime", xrefBusLifetimeScenario...)
	if want != `{"alive":true,"seen":5,"collected":true}` {
		t.Fatalf("oracle = %s", want)
	}
	for _, isolation := range [][]string{{"", ""}, {"isolated", "isolated"}, {"", "isolated"}} {
		t.Run(fmt.Sprintf("%q", isolation), func(t *testing.T) {
			h, exts := xrefFixture(t, isolation, xrefBusLifetimeScenario...)
			if got := xrefRunTool(t, exts["xref-0"], "lifetime"); got != want {
				t.Fatalf("PiG observation differs from Pi\n got: %s\nwant: %s", got, want)
			}
			if stats := h.xref.stats(); stats.Leases != 0 || stats.Holds != 0 || stats.Expected != 0 {
				t.Fatalf("Host xref ledger retained %+v", stats)
			}
		})
	}
}

var xrefBusSymbolLifetimeScenario = []string{
	xrefForceGC + `
export default pi => {
  pi.registerTool({ name: "symlife", description: "symlife", parameters: { type: "object", properties: {} }, async execute() {
    let sym = Symbol("unique");
    const symRef = new WeakRef(sym);
    let payload = { n: 0, sym, [sym]: 1 };
    const payloadRef = new WeakRef(payload);
    pi.events.emit("retain", payload);
    payload = undefined;
    sym = undefined;
    for (let i = 0; i < 5; i++) { await new Promise(resolve => setTimeout(resolve, 10)); gc(); pi.events.emit("gc"); }
    await new Promise(resolve => setTimeout(resolve, 10));
    const alive = symRef.deref() !== undefined;
    const seen = payloadRef.deref()?.n ?? -1;
    pi.events.emit("drop");
    let collected = false;
    // WeakRef.deref keeps its target until the current job ends, so collection is observed from a later job than the check.
    for (let i = 0; i < 300 && !collected; i++) { await new Promise(resolve => setTimeout(resolve, 10)); gc(); pi.events.emit("gc"); await new Promise(resolve => setTimeout(resolve, 10)); collected = symRef.deref() === undefined && payloadRef.deref() === undefined; }
    return { content: [{ type: "text", text: JSON.stringify({ alive, seen, collected }) }] };
  } });
};`,
	xrefForceGC + `
export default pi => {
  let saved, savedSym, savedKeys;
  pi.events.on("retain", data => { saved = data; savedSym = data.sym; savedKeys = Object.getOwnPropertySymbols(data); });
  pi.events.on("gc", () => { gc(); if (saved && savedKeys[0] === savedSym && saved[savedSym] === 1) saved.n++; });
  pi.events.on("drop", () => { saved = savedSym = savedKeys = undefined; });
};`,
}

// A unique symbol crossing realms follows an object's lifetime: a retained foreign alias keeps the owner's symbol alive and identical, and once every realm drops it the owner's export, the Host lease and the importer's symbol are all collectable, as in Pi's single heap.
func TestXrefEventBusSymbolLifetimeMatchesPi(t *testing.T) {
	t.Parallel()
	want := piEventBusOracle(t, "symlife", xrefBusSymbolLifetimeScenario...)
	if want != `{"alive":true,"seen":5,"collected":true}` {
		t.Fatalf("oracle = %s", want)
	}
	for _, isolation := range [][]string{{"", ""}, {"isolated", "isolated"}, {"", "isolated"}} {
		t.Run(fmt.Sprintf("%q", isolation), func(t *testing.T) {
			h, exts := xrefFixture(t, isolation, xrefBusSymbolLifetimeScenario...)
			if got := xrefRunTool(t, exts["xref-0"], "symlife"); got != want {
				t.Fatalf("PiG observation differs from Pi\n got: %s\nwant: %s", got, want)
			}
			if stats := h.xref.stats(); stats.Leases != 0 || stats.Holds != 0 || stats.Expected != 0 {
				t.Fatalf("Host xref ledger retained %+v", stats)
			}
		})
	}
}

var xrefBusReentrantScenario = []string{
	`export default pi => {
  pi.events.on("p2", d => { d.trace.push("A2:" + d.depth); d.depth++; pi.events.emit("p3", d); d.trace.push("A2-after:" + d.depth); });
  pi.events.on("p4", d => { d.trace.push("A4:" + d.depth + ":" + (d.origin === d.self)); d.depth++; d.fromA = d.bFn(d.depth); });
  pi.registerTool({ name: "reentrant", description: "reentrant", parameters: { type: "object", properties: {} }, async execute() {
    const trace = [];
    const data = { depth: 0, trace };
    data.self = data;
    data.origin = data;
    pi.events.emit("p1", data);
    return { content: [{ type: "text", text: JSON.stringify({ depth: data.depth, trace, fromA: data.fromA, fromB: data.fromB }) }] };
  } });
};`,
	`export default pi => {
  pi.events.on("p1", d => { d.trace.push("B1:" + d.depth); d.depth++; d.bFn = n => "b" + n + ":" + d.depth; pi.events.emit("p2", d); d.trace.push("B1-after:" + d.depth); });
  pi.events.on("p3", d => { d.trace.push("B3:" + d.depth); d.depth++; pi.events.emit("p4", d); d.fromB = d.depth; });
};`,
}

// Nested emits alternate between two realms four levels deep while each realm waits in its own emit; every level reads and writes the same original object, and a callback crosses back into its owner.
func TestXrefEventBusReentrantDispatchMatchesPi(t *testing.T) {
	t.Parallel()
	want := piEventBusOracle(t, "reentrant", xrefBusReentrantScenario...)
	for _, isolation := range [][]string{{"", ""}, {"isolated", "isolated"}, {"", "isolated"}} {
		t.Run(fmt.Sprintf("%q", isolation), func(t *testing.T) {
			_, exts := xrefFixture(t, isolation, xrefBusReentrantScenario...)
			for range 3 {
				if got := xrefRunTool(t, exts["xref-0"], "reentrant"); got != want {
					t.Fatalf("PiG observation differs from Pi\n got: %s\nwant: %s", got, want)
				}
			}
		})
	}
}

// Two realms emit into each other concurrently from independent tool calls. Each realm services the other's dispatches and property operations while it waits in its own emit, so neither deadlocks and every write lands.
func TestXrefEventBusCrossingEmittersDoNotDeadlock(t *testing.T) {
	source := func(self, other string) string {
		return `export default pi => {
  pi.events.on("` + other + `", d => { d.hits++; });
  pi.registerTool({ name: "` + self + `", description: "run", parameters: { type: "object", properties: {} }, async execute() {
    const data = { hits: 0 };
    for (let i = 0; i < 200; i++) { pi.events.emit("` + self + `", data); if (i % 20 === 0) await new Promise(setImmediate); }
    return { content: [{ type: "text", text: String(data.hits) }] };
  } });
};`
	}
	_, exts := xrefFixture(t, []string{"isolated", "isolated"}, source("a", "b"), source("b", "a"))
	results := make(chan string, 2)
	for _, run := range []struct{ ext, tool string }{{"xref-0", "a"}, {"xref-1", "b"}} {
		go func() {
			registered := exts[run.ext].Tools[run.tool]
			result, err := registered.Definition.Execute(t.Context(), "cross-"+run.tool, json.RawMessage(`{}`), nil)
			if err != nil {
				results <- err.Error()
				return
			}
			results <- result.(agent.AgentToolResult).Text()
		}()
	}
	for range 2 {
		if got := <-results; got != "200" {
			t.Fatalf("hits = %s, want 200", got)
		}
	}
}

// A foreign alias whose owning process has exited fails loudly instead of returning a stale value; the Host drops the dead realm's leases.
func TestXrefOwnerExitFailsRetainedAlias(t *testing.T) {
	h, exts := xrefFixture(t, []string{"isolated", "isolated"},
		`export default pi => {`+xrefTool("share", `pi.events.emit("share", { n: 7 });return {content:[{type:"text",text:"shared"}]};`)+`};`,
		`export default pi => { let saved; pi.events.on("share", d => { saved = d; });`+xrefTool("read", `try { return {content:[{type:"text",text:String(saved.n)}]}; } catch (error) { return {content:[{type:"text",text:"threw:" + error.message}]}; }`)+`};`,
	)
	xrefRunTool(t, exts["xref-0"], "share")
	if got := xrefRunTool(t, exts["xref-1"], "read"); got != "7" {
		t.Fatalf("live read = %s", got)
	}
	h.mu.Lock()
	owner := h.exts["xref-0"]
	h.mu.Unlock()
	realms := h.xref.stats().Realms
	if err := owner.proc.Kill(); err != nil {
		t.Fatal(err)
	}
	for h.xref.stats().Realms >= realms {
		select {
		case <-t.Context().Done():
			t.Fatal("owner realm was not retired")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if got := xrefRunTool(t, exts["xref-1"], "read"); got != "threw:cross-process reference owner exited" {
		t.Fatalf("read after owner exit = %s", got)
	}
	if stats := h.xref.stats(); stats.Leases != 0 || stats.Holds != 0 {
		t.Fatalf("Host kept dead-owner leases %+v", stats)
	}
}

// Pi resource-loader.ts:258 gives every extension of a loader one bus. The project-trust preload and the final extension set are loaded by two LoadAll calls; the preloaded realm keeps its in-heap listeners until the second realm arrives, then hands them to the Host registry in EventEmitter order, including listeners added after load.
func TestXrefEventBusFinalSetJoinsPreloadedRealm(t *testing.T) {
	nodeCellRequireNode(t)
	root := t.TempDir()
	write := func(name, source string) ExtConfig {
		path := filepath.Join(root, name+".mjs")
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		return ExtConfig{Name: name, Source: path, Enabled: true}
	}
	emitTool := func(name string) string {
		return xrefTool(name, `const x = []; pi.events.emit("ch", x); return {content:[{type:"text",text:JSON.stringify(x)}]};`)
	}
	a := write("a", `export default pi => { pi.events.on("ch", d => d.push("a-load"));`+xrefTool("late", `pi.events.on("ch", d => d.push("a-late")); return {content:[{type:"text",text:"ok"}]};`)+emitTool("emitA")+`};`)
	a2 := write("a2", `export default pi => { pi.events.on("ch", d => d.push("a2-load")); };`)
	b := write("b", `export default pi => { pi.events.on("ch", d => d.push("b-load"));`+emitTool("emitB")+`};`)
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	preloaded, errs := h.LoadAll(t.Context(), []ExtConfig{a, a2})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	byName := map[string]extension.Extension{}
	for _, ext := range preloaded {
		byName[ext.Name] = ext
	}
	xrefRunTool(t, byName["a"], "late")
	if got := xrefRunTool(t, byName["a"], "emitA"); got != `["a-load","a2-load","a-late"]` {
		t.Fatalf("preloaded realm = %s", got)
	}
	final, errs := h.LoadFinalExtensionSet(t.Context(), []ExtConfig{a, a2, b}, []ExtConfig{a, a2})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	for _, ext := range final {
		byName[ext.Name] = ext
	}
	const want = `["a-load","a2-load","a-late","b-load"]`
	for _, run := range []struct{ ext, tool string }{{"a", "emitA"}, {"b", "emitB"}} {
		if got := xrefRunTool(t, byName[run.ext], run.tool); got != want {
			t.Fatalf("%s = %s, Pi's one bus gives %s", run.tool, got, want)
		}
	}
}

// Host.Load adds one extension to the running set. Pi gives it the same bus as the extensions already loaded, so a second Node realm switches the first to the Host registry before it starts.
func TestXrefEventBusLoadJoinsRunningRealm(t *testing.T) {
	nodeCellRequireNode(t)
	root := t.TempDir()
	write := func(name, source string) ExtConfig {
		path := filepath.Join(root, name+".mjs")
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		return ExtConfig{Name: name, Source: path, Enabled: true}
	}
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	a, err := h.Load(t.Context(), write("a", `export default pi => { pi.events.on("ch", d => d.push("a"));`+xrefTool("emitA", `const x = []; pi.events.emit("ch", x); return {content:[{type:"text",text:JSON.stringify(x)}]};`)+`};`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Load(t.Context(), write("b", `export default pi => { pi.events.on("ch", d => d.push("b")); };`)); err != nil {
		t.Fatal(err)
	}
	if got := xrefRunTool(t, *a, "emitA"); got != `["a","b"]` {
		t.Fatalf("emitA = %s, Pi's one bus gives [\"a\",\"b\"]", got)
	}
}

var xrefBusIntegrityScenario = []string{
	`export default pi => {
  pi.registerTool({ name: "integrity", description: "integrity", parameters: { type: "object", properties: {} }, async execute() {
    const out = [];
    const open = { a: 1, nested: { b: 2 } };
    const constant = Object.freeze({ k: "v", list: Object.freeze([1, 2]) });
    const sealed = Object.seal({ s: 1 });
    pi.events.emit("integrity", { open, constant, sealed, out });
    out.push("owner:" + Object.isFrozen(open) + ":" + open.a + ":" + Object.isExtensible(sealed));
    return { content: [{ type: "text", text: JSON.stringify(out) }] };
  } });
};`,
	`export default pi => {
  pi.events.on("integrity", ({ open, constant, sealed, out }) => {
    const r = (name, fn) => { try { out.push(name + "=" + fn()); } catch (error) { out.push(name + "!" + error.name + ":" + error.message); } };
    r("constantFrozen", () => Object.isFrozen(constant) + ":" + Object.isFrozen(constant.list));
    r("constantKeys", () => Object.keys(constant).join(",") + ":" + JSON.stringify(constant));
    r("constantWrite", () => { constant.k = "w"; return constant.k; });
    r("constantUnchanged", () => constant.k);
    r("constantStrictWrite", () => { "use strict"; constant.k = "w"; return constant.k; });
    r("sealed", () => Object.isSealed(sealed) + ":" + Object.isExtensible(sealed) + ":" + Object.keys(sealed).join(","));
    r("sealedAdd", () => { sealed.extra = 1; return "extra" in sealed; });
    r("freeze", () => Object.isFrozen(Object.freeze(open)) + ":" + Object.isFrozen(open.nested));
    r("frozenKeys", () => Object.keys(open).join(","));
    r("frozenWrite", () => { open.a = 5; return open.a; });
    r("frozenStrictWrite", () => { "use strict"; open.a = 6; return open.a; });
    r("frozenDelete", () => delete open.a);
    r("frozenUnchanged", () => open.a);
    r("preventExtensions", () => { Object.preventExtensions(open.nested); open.nested.c = 3; return Object.keys(open.nested).join(","); });
  });
};`,
}

// Object integrity levels apply to the one object, as in Pi's single heap: a foreign listener can inspect and use a frozen or sealed payload, and Object.freeze, Object.seal and Object.preventExtensions on a foreign payload change the emitter's object and keep the proxy invariants V8 checks.
func TestXrefEventBusIntegrityLevelsMatchPi(t *testing.T) {
	want := piEventBusOracle(t, "integrity", xrefBusIntegrityScenario...)
	for _, isolation := range [][]string{{"", ""}, {"isolated", "isolated"}} {
		t.Run(fmt.Sprintf("%q", isolation), func(t *testing.T) {
			_, exts := xrefFixture(t, isolation, xrefBusIntegrityScenario...)
			if got := xrefRunTool(t, exts["xref-0"], "integrity"); got != want {
				t.Fatalf("PiG observation differs from Pi\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

var xrefBusRejectedWriteScenario = []string{
	`export default pi => {
  pi.registerTool({ name: "rejected", description: "rejected", parameters: { type: "object", properties: {} }, async execute() {
    const out = [];
    const readOnly = {};
    Object.defineProperty(readOnly, "r", { value: 1, writable: false, enumerable: true, configurable: true });
    const getterOnly = { get g() { return 1; } };
    pi.events.emit("rejected", { frozen: Object.freeze({ k: 1 }), sealed: Object.seal({ k: 1 }), closed: Object.preventExtensions({ k: 1 }), list: Object.freeze([1, 2]), readOnly, getterOnly, thrower: { set s(v) { throw new TypeError("Cannot set: the owner setter threw"); } }, out });
    return { content: [{ type: "text", text: JSON.stringify(out) }] };
  } });
};`,
	`export default pi => {
  pi.events.on("rejected", ({ frozen, sealed, closed, list, readOnly, getterOnly, thrower, out }) => {
    const sloppy = (source, ...args) => new Function("a", "b", "c", source)(...args);
    const r = (name, fn) => { try { out.push(name + "=" + String(fn())); } catch (error) { out.push(name + "!" + error.constructor.name + ":" + error.message); } };
    r("strictAssign", () => { frozen.k = 2; });
    r("sloppyAssign", () => sloppy("a.k = 2; return a.k", frozen));
    r("strictAdd", () => { frozen.extra = 2; });
    r("sloppyAdd", () => sloppy("a.extra = 2; return 'extra' in a", frozen));
    r("sealedAssign", () => { sealed.k = 2; return sealed.k; });
    r("sealedAdd", () => { sealed.extra = 2; });
    r("sealedDelete", () => { delete sealed.k; });
    r("sloppySealedDelete", () => sloppy("return delete a.k", sealed));
    r("closedAdd", () => { closed.extra = 2; });
    r("closedDelete", () => delete closed.k);
    r("closedDeleteMissing", () => delete closed.missing);
    r("strictDelete", () => { delete frozen.k; });
    r("sloppyDelete", () => sloppy("return delete a.k", frozen));
    r("readOnlyStrict", () => { readOnly.r = 2; });
    r("readOnlySloppy", () => sloppy("a.r = 2; return a.r", readOnly));
    r("getterStrict", () => { getterOnly.g = 2; });
    r("getterSloppy", () => sloppy("a.g = 2; return a.g", getterOnly));
    r("defineExisting", () => Object.defineProperty(frozen, "k", { value: 3 }));
    r("defineNew", () => Object.defineProperty(closed, "z", { value: 3 }));
    r("defineSame", () => Object.defineProperty(frozen, "k", { value: 1 }) === frozen);
    r("reflectSet", () => Reflect.set(frozen, "k", 2));
    r("reflectDelete", () => Reflect.deleteProperty(frozen, "k"));
    r("reflectDefine", () => Reflect.defineProperty(frozen, "k", { value: 4 }));
    r("assign", () => Object.assign(frozen, { k: 5 }));
    r("push", () => list.push(3));
    r("pop", () => list.pop());
    r("sloppyPush", () => sloppy("return a.push(3)", list));
    r("setIndex", () => { list[0] = 9; });
    r("ownerSetterThrows", () => { thrower.s = 1; });
    r("setPrototype", () => Object.setPrototypeOf(closed, null));
    r("setPrototypeSame", () => Object.setPrototypeOf(closed, Object.prototype) === closed);
    r("sloppySetPrototype", () => sloppy("return Object.setPrototypeOf(a, null)", closed));
    r("reflectSetPrototype", () => Reflect.setPrototypeOf(closed, null));
    r("protoAssign", () => { closed.__proto__ = null; });
    r("setPrototypeLocalError", () => { try { Object.setPrototypeOf(closed, null); } catch (error) { return [Object.getPrototypeOf(error) === TypeError.prototype, /xref\.mjs/.test(String(error.stack))].join("|"); } });
    r("unchanged", () => JSON.stringify([frozen, sealed, closed, list, readOnly]));
  });
};`,
}

// A rejected write, delete or property add on a frozen, sealed or non-extensible payload throws V8's message for the object itself, and only in a strict caller; a sloppy caller and the Reflect functions see the false result silently, as in Pi's single heap.
func TestXrefEventBusRejectedWritesMatchPi(t *testing.T) {
	want := piEventBusOracle(t, "rejected", xrefBusRejectedWriteScenario...)
	for _, isolation := range [][]string{{"", ""}, {"isolated", "isolated"}} {
		t.Run(fmt.Sprintf("%q", isolation), func(t *testing.T) {
			_, exts := xrefFixture(t, isolation, xrefBusRejectedWriteScenario...)
			if got := xrefRunTool(t, exts["xref-0"], "rejected"); got != want {
				t.Fatalf("PiG observation differs from Pi\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

// xrefBusInspectScenario prints every payload value. The frozen, sealed and non-extensible ones are never queried for their extensibility: a program that asks makes Node print them as Proxy(...) (TestXrefEventBusInspectProxyBoundaryIsStated).
func xrefBusInspectScenario() []string {
	sources := []string{
		`export default pi => {
  pi.registerTool({ name: "inspected", description: "inspected", parameters: { type: "object", properties: {} }, async execute() {
    class Point { #secret = 7; constructor(x) { this.x = x; } get doubled() { return this.x * 2; } }
    class Tagged { get [Symbol.toStringTag]() { return "Tag"; } }
    const failure = Object.assign(new Error("boom"), { stack: "Error: boom\n    at fake (fake.js:1:1)", code: "E_BOOM" });
    const cyclic = { name: "loop", list: [1, 2] };
    cyclic.self = cyclic;
    const custom = { [Symbol.for("nodejs.util.inspect.custom")](depth, options) { return "custom<" + depth + "," + typeof options.stylize + ">"; } };
    function plain(a, b) { return a + b; }
    plain.tag = 1;
    const values = {
      map: new Map([["k", 2], ["nested", { a: 1 }]]),
      set: new Set([1, "two", { three: 3 }]),
      date: new Date(0),
      invalidDate: new Date(NaN),
      fn: plain,
      arrow: (x) => x,
      klass: class Named extends Point {},
      anonymous: (() => () => 1)(),
      frozen: Object.freeze({ k: 1, nested: { z: 1 } }),
      sealed: Object.seal({ k: 1 }),
      closed: Object.preventExtensions({ k: 1 }),
      frozenList: Object.freeze([1, 2, 3]),
      cyclic,
      getter: { get g() { return 1; }, set s(v) {}, plainValue: 2 },
      point: new Point(3),
      tagged: new Tagged(),
      bare: Object.assign(Object.create(null), { a: 1 }),
      symbols: { [Symbol("hidden")]: 1, [Symbol.for("shared")]: 2, visible: 3 },
      failure,
      settled: Promise.resolve(1),
      pending: new Promise(() => {}),
      bytes: new Uint8Array([1, 2, 3]),
      buffer: Buffer.from("hi"),
      boxed: Object.assign(new Number(3), { extra: true }),
      weak: new WeakMap(),
      holes: [1, , 3],
      long: Array.from({ length: 30 }, (_, i) => i * 3),
      deep: { a: { b: { c: { d: 1 } } } },
      words: Array.from({ length: 10 }, (_, i) => "word-" + i),
      custom,
      customObject: { [Symbol.for("nodejs.util.inspect.custom")](depth, options, inspectFn) { return { inner: true, list: [1, 2, 3], echoed: inspectFn({ deep: { deeper: { deepest: 1 } } }, options), depth }; } },
      customLines: { [Symbol.for("nodejs.util.inspect.custom")]() { return "first\nsecond"; } },
      customSelf: { visible: 1, [Symbol.for("nodejs.util.inspect.custom")]() { return this; } },
      nameless: (() => { const f = function () {}; delete f.name; return f; })(),
      noStack: (() => { const e = new Error("gone"); delete e.stack; return e; })(),
      stack: (() => { class Stack extends Array { top() { return this[this.length - 1]; } } const list = Stack.from([1, 2, 3]); list.label = "s"; return list; })(),
      aggregate: (() => { const inner = [Object.assign(new Error("a"), { stack: "Error: a\n    at a (fake.js:1:1)" }), Object.assign(new TypeError("b"), { stack: "TypeError: b\n    at b (fake.js:2:2)" })]; const e = new AggregateError(inner, "many", { cause: { why: 1 } }); e.stack = "AggregateError: many\n    at fake (fake.js:1:1)"; return e; })(),
      bigSparse: (() => { const a = [1]; a[3000] = 2; a.extra = true; return a; })(),
      protoKey: JSON.parse('{"__proto__": {"polluted": true}, "plain": 1}'),
      subMap: new (class Registry extends Map { size2() { return this.size; } })([["a", { n: 1 }]]),
      subDate: new (class Stamp extends Date {})(86400000),
      regexpState: (() => { const r = /x(y)/g; r.lastIndex = 3; r.note = "n"; return r; })(),
      bound: (function original(a) { return a; }).bind(null, 1),
      asyncFn: async function loader() {},
      generatorFn: function* counter() { yield 1; },
      asyncGeneratorFn: async function* stream() { yield 1; },
      staticClass: class Config { static defaults = { a: 1 }; static make() { return new Config(); } },
      weakSet: new WeakSet(),
      arrayBuffer: new Uint8Array([1, 2, 3, 250]).buffer,
      floats: new Float64Array([1.5, -0, NaN]),
      bigInts: new BigInt64Array([1n, -2n]),
      offsetView: new Uint16Array(new ArrayBuffer(16), 4, 3),
      boxedString: Object.assign(new String("abc"), { tag: 1 }),
      boxedSymbol: Object(Symbol("boxed")),
      boxedBigInt: Object(10n),
      crossMap: (() => { const m = new Map(); const key = { k: 1 }; m.set(key, m); m.set("self", key); return m; })(),
      setOfArrays: new Set([[1, [2, [3, [4]]]], new Set([1])]),
      arguments: (function () { return arguments; })(1, 2),
      typedSubclass: new (class Bytes extends Uint8Array {})([9, 8, 7]),
      nullProtoArray: Object.setPrototypeOf([1, 2], null),
      dataView: new DataView(new ArrayBuffer(4)),
      urlLike: new URL("https://example.com/a?b=1"),
      symbolValued: { first: Symbol("one"), [Symbol("key")]: Symbol.for("shared") },
      regexp: /a+b/gi,
      text: "quoted 'string'\nwith newline",
      big: 12n,
      money: new (class Money { toString() { return "$5"; } })(),
      primitive: { [Symbol.toPrimitive]() { return "prim"; } },
    };
    const out = [];
    pi.events.emit("inspected", { values, out });
    return { content: [{ type: "text", text: JSON.stringify(out) }] };
  } });
};`,
		`import { inspect, format } from "node:util";
import { Console } from "node:console";
import { Writable } from "node:stream";
export default pi => {
  pi.events.on("inspected", ({ values, out }) => {
    const lines = [];
    const console2 = new Console({ stdout: new Writable({ write(chunk, _encoding, done) { lines.push(String(chunk)); done(); } }), inspectOptions: {} });
    const options = [{}, { depth: 0 }, { depth: null }, { colors: true }, { compact: false, breakLength: 40 }, { showHidden: true }, { sorted: true, maxArrayLength: 2, maxStringLength: 5 }, { getters: true }, { showHidden: true, depth: 0, breakLength: 20 }];
    const r = (name, fn) => { try { out.push(name + ": " + fn()); } catch (error) { out.push(name + "!" + error.name + ":" + error.message); } };
    const nonExtensible = new Set(["frozen", "sealed", "closed", "frozenList"]);
    for (const [name, value] of Object.entries(values)) {
      if (!nonExtensible.has(name)) r(name + "/extensible", () => Object.isExtensible(value) + ":" + Object.isFrozen(value));
      options.forEach((option, index) => r(name + "#" + index, () => inspect(value, option)));
      r(name + "/inLocalObject", () => inspect({ inner: value }));
      r(name + "/inLocalArray", () => inspect([value, value]));
      // A foreign value at a nested column and after wide siblings breaks lines by the caller's indentation, seen list and depth, as Pi's one inspect pass does.
      r(name + "/wide", () => inspect({ pad: "p".repeat(50), inner: value, tail: [value] }));
      // A Promise's state and an arguments object are readable only through V8 internals, so the owner formats them and they do not count as a nesting level of the layout (TestXrefEventBusInspectProxyBoundaryIsStated).
      if (!(value instanceof Promise) && name !== "arguments") r(name + "/deepLocal", () => inspect({ a: { b: { c: value } } }, { depth: 6 }));
      r(name + "/inMap", () => inspect(new Map([["key", value], [value, "reverse"]]), { depth: 4 }));
      r(name + "/inSet", () => inspect(new Set([[value]]), { depth: 4 }));
      // JSON.stringify unwraps a boxed primitive by its internal slot, which a foreign object does not have (D83 boundary 4).
      r(name + "/format", () => name.startsWith("boxed") ? format("%O|%s", value, value) : format("%O|%s|%j", value, value, value));
      r(name + "/console", () => { lines.length = 0; console2.log("head", value); console2.log("%s|%O", value, value); return lines.join("|"); });
    }
    r("wholeValues", () => inspect(values));
    r("wholeValuesDepth", () => inspect(values, { depth: 0 }));
  });
};`,
	}
	return sources
}

// util.inspect and console.log of a foreign payload print exactly what Pi prints for the object in its own heap: Map, Set, Date, functions, classes, frozen and non-extensible objects, cycles, accessors, Promises, typed arrays, custom inspect hooks, with the caller's inspect options.
func TestXrefEventBusInspectMatchesPi(t *testing.T) {
	t.Parallel()
	xrefInspectMatchesPi(t)
}

func xrefInspectMatchesPi(t *testing.T) {
	t.Helper()
	sources := xrefBusInspectScenario()
	want := piEventBusOracle(t, "inspected", sources...)
	for _, isolation := range [][]string{{"", ""}, {"isolated", "isolated"}} {
		t.Run(fmt.Sprintf("%q", isolation), func(t *testing.T) {
			_, exts := xrefFixture(t, isolation, sources...)
			got := xrefRunTool(t, exts["xref-0"], "inspected")
			if got != want {
				var wantLines, gotLines []string
				_ = json.Unmarshal([]byte(want), &wantLines)
				_ = json.Unmarshal([]byte(got), &gotLines)
				for i := range max(len(wantLines), len(gotLines)) {
					var w, g string
					if i < len(wantLines) {
						w = wantLines[i]
					}
					if i < len(gotLines) {
						g = gotLines[i]
					}
					if w != g {
						t.Errorf("line %d differs from Pi\n got: %q\nwant: %q", i, g, w)
					}
				}
				t.Fatalf("PiG inspect output differs from Pi (%d vs %d lines)", len(gotLines), len(wantLines))
			}
		})
	}
}

var xrefBusCloneScenario = []string{
	`export default pi => {
  pi.registerTool({ name: "cloned", description: "cloned", parameters: { type: "object", properties: {} }, async execute() {
    class Point { #secret = 7; constructor(x) { this.x = x; } norm() { return this.x * this.#secret; } }
    const err = Object.assign(new RangeError("bad", { cause: "why" }), { stack: "RangeError: bad\n    at fake (fake.js:1:1)" });
    const data = {
      n: 1, list: [1, [2, 3]], map: new Map([["k", { deep: 1 }]]), set: new Set([1, 2]), when: new Date(5), nested: { a: 1 },
      bytes: new Uint8Array([1, 2, 3]), text: "t", undef: undefined, big: 5n, nan: NaN, negz: -0, re: /x/g, err,
      boxed: new String("s"), sparse: [1, , 3], getter: { get g() { return 9; } }, point: new Point(3), buffer: Buffer.from("hi"),
    };
    data.self = data;
    data.alias = data.nested;
    data[Symbol("hidden")] = 1;
    const withFn = { fn() {} };
    const ab = new ArrayBuffer(8);
    const out = [];
    const state = { done: false };
    pi.events.emit("cloned", { data, withFn, ab, out, state });
    for (let i = 0; i < 1000 && !state.done; i++) await new Promise(resolve => setTimeout(resolve, 10));
    out.push("done:" + state.done);
    return { content: [{ type: "text", text: JSON.stringify(out) }] };
  } });
};`,
	`import { inspect } from "node:util";
import v8 from "node:v8";
import { MessageChannel, receiveMessageOnPort, Worker } from "node:worker_threads";
export default pi => {
  pi.events.on("cloned", async ({ data, withFn, ab, out, state }) => {
    const show = value => inspect(value, { depth: null });
    // v8.serialize writes arrays densely only while V8 considers the process's arrays ordinary; a probe after every step shows whether an operation changed that for later, unrelated arrays.
    const packed = () => { const probe = []; probe["0"] = 1; probe["1"] = 2; return v8.serialize(probe).toString("hex"); };
    const r = (name, fn) => { try { out.push(name + ": " + fn()); } catch (error) { out.push(name + "!" + error.name + ":" + error.message); } out.push(name + " packed " + packed()); };
    r("clone", () => { const c = structuredClone(data); return [c !== data, c.self === c, c.alias === c.nested, c.map instanceof Map, c.when instanceof Date, c.err instanceof RangeError, show(c)].join("|"); });
    r("cloneNested", () => show(structuredClone(data.nested)));
    r("cloneList", () => show(structuredClone(data.list)));
    r("cloneMap", () => { const c = structuredClone(data.map); return [c instanceof Map, show(c)].join("|"); });
    r("cloneDate", () => show(structuredClone(data.when)));
    r("cloneBytes", () => show(structuredClone(data.bytes)));
    r("cloneBuffer", () => show(structuredClone(data.buffer)));
    r("cloneBoxed", () => show(structuredClone(data.boxed)));
    r("cloneInstance", () => show(structuredClone(data.point)));
    r("cloneGetter", () => show(structuredClone(data.getter)));
    r("wrapped", () => { const c = structuredClone({ wrapper: data, again: data.nested, local: { x: [1, 2] } }); return [c.wrapper.self === c.wrapper, c.again === c.wrapper.alias, c.wrapper.alias === c.wrapper.nested, show(c.local)].join("|"); });
    r("array", () => { const c = structuredClone([data, data, data.nested]); return [c[0] === c[1], c[2] === c[0].nested].join("|"); });
    r("mapWithForeign", () => { const c = structuredClone(new Map([[data.nested, data], ["k", new Set([data.nested])]])); return show(c); });
    r("functionTop", () => structuredClone(withFn.fn));
    r("functionMember", () => structuredClone(withFn));
    r("functionNested", () => structuredClone({ inside: [withFn] }));
    r("symbolValue", () => structuredClone({ s: Symbol("x"), data: data.nested }));
    r("transfer", () => { const local = new ArrayBuffer(4); const c = structuredClone({ data: data.nested, local, foreign: ab }, { transfer: [local] }); return [local.byteLength, c.local.byteLength, c.foreign.byteLength, show(c.data)].join("|"); });
    r("transferForeign", () => { const before = ab.byteLength; const c = structuredClone(data.nested, { transfer: [ab] }); return [before, ab.byteLength, c.a].join("|"); });
    r("serialize", () => v8.serialize(data).toString("hex"));
    r("serializeNested", () => v8.serialize({ wrapper: data.nested, list: [data.list] }).toString("hex"));
    r("deserialize", () => show(v8.deserialize(v8.serialize(data))));
    r("serializeFn", () => v8.serialize(withFn));
    r("writeValue", () => { const s = new v8.Serializer(); s.writeHeader(); s.writeValue(data.nested); s.writeValue(data.list); return s.releaseBuffer().toString("hex"); });
    r("defaultSerializer", () => { const s = new v8.DefaultSerializer(); s.writeHeader(); s.writeValue({ v: data.bytes }); return s.releaseBuffer().toString("hex"); });
    r("port", () => { const { port1, port2 } = new MessageChannel(); try { port1.postMessage(data); return show(receiveMessageOnPort(port2).message); } finally { port1.close(); port2.close(); } });
    r("portNested", () => { const { port1, port2 } = new MessageChannel(); try { port1.postMessage({ wrapper: data.nested, list: data.list }); return show(receiveMessageOnPort(port2).message); } finally { port1.close(); port2.close(); } });
    r("portTransfer", () => { const { port1, port2 } = new MessageChannel(); try { const local = new ArrayBuffer(4); port1.postMessage({ data: data.nested, local }, [local]); const m = receiveMessageOnPort(port2).message; return [local.byteLength, m.local.byteLength, show(m.data)].join("|"); } finally { port1.close(); port2.close(); } });
    r("portFn", () => { const { port1, port2 } = new MessageChannel(); try { port1.postMessage(withFn); } finally { port1.close(); port2.close(); } });
    const worker = new Worker("const { parentPort } = process.getBuiltinModule('node:worker_threads'); parentPort.on('message', message => parentPort.postMessage(message));", { eval: true });
    try {
      const echoed = new Promise((resolve, reject) => { worker.once("message", resolve); worker.once("error", reject); });
      worker.postMessage({ wrapper: data.nested, list: data.list, map: data.map });
      out.push("worker: " + show(await echoed));
    } catch (error) {
      out.push("worker!" + error.name + ":" + error.message);
    } finally {
      await worker.terminate();
    }
    state.done = true;
  });
};`,
}

// A foreign listener that clones, serializes or posts a payload gets what Pi's listener gets from the object in its own heap: structuredClone, v8.serialize, v8.Serializer and MessagePort or Worker postMessage all succeed on it, keep sharing and cycles, and fail with the same DataCloneError where Pi fails.
func TestXrefEventBusCloneMatchesPi(t *testing.T) {
	t.Parallel()
	want := piEventBusOracle(t, "cloned", xrefBusCloneScenario...)
	for _, isolation := range [][]string{{"", ""}, {"isolated", "isolated"}} {
		t.Run(fmt.Sprintf("%q", isolation), func(t *testing.T) {
			_, exts := xrefFixture(t, isolation, xrefBusCloneScenario...)
			got := xrefRunTool(t, exts["xref-0"], "cloned")
			if got != want {
				var wantLines, gotLines []string
				_ = json.Unmarshal([]byte(want), &wantLines)
				_ = json.Unmarshal([]byte(got), &gotLines)
				for i := range max(len(wantLines), len(gotLines)) {
					var w, g string
					if i < len(wantLines) {
						w = wantLines[i]
					}
					if i < len(gotLines) {
						g = gotLines[i]
					}
					if w != g {
						t.Errorf("line %d differs from Pi\n got: %q\nwant: %q", i, g, w)
					}
				}
				t.Fatalf("PiG observation differs from Pi (%d vs %d lines)", len(gotLines), len(wantLines))
			}
		})
	}
}

var xrefBusMaxListenersScenario = []string{
	`export default pi => {
  const log = [];
  const record = line => { if (log.at(-1) !== line) log.push(line); };
  process.on("warning", warning => { if (warning.name === "MaxListenersExceededWarning") record([warning.name, warning.message, warning.count, warning.type, warning.emitter?.constructor?.name].join("|")); });
  pi.events.on("warned", record);
  pi.registerTool({ name: "warnings", description: "warnings", parameters: { type: "object", properties: {} }, async execute() {
    const settle = () => new Promise(resolve => setTimeout(resolve, 100));
    const own = [];
    for (let i = 0; i < 6; i++) own.push(pi.events.on("probe", () => {}));
    pi.events.emit("addListeners", 5);
    await settle();
    log.push("--- 11 listeners, then 2 more");
    own.push(pi.events.on("probe", () => {}), pi.events.on("probe", () => {}));
    await settle();
    log.push("--- drop to one listener, then 10 more");
    pi.events.emit("dropListeners");
    while (own.length > 1) own.pop()();
    for (let i = 0; i < 10; i++) own.push(pi.events.on("probe", () => {}));
    await settle();
    log.push("--- another channel");
    for (let i = 0; i < 11; i++) pi.events.on("other", () => {});
    await settle();
    return { content: [{ type: "text", text: JSON.stringify(log) }] };
  } });
};`,
	`export default pi => {
  let unsubscribe = [];
  process.on("warning", warning => { if (warning.name === "MaxListenersExceededWarning") pi.events.emit("warned", [warning.name, warning.message, warning.count, warning.type, warning.emitter?.constructor?.name].join("|")); });
  pi.events.on("addListeners", count => { for (let i = 0; i < count; i++) unsubscribe.push(pi.events.on("probe", () => {})); });
  pi.events.on("dropListeners", () => { for (const off of unsubscribe) off(); unsubscribe = []; });
};`,
}

// EventEmitter warns when more than ten listeners are added to one channel, but Pi's setupCli replaces process.emitWarning with a no-op before any extension loads (cli/setup.ts:8), so no extension observes MaxListenersExceededWarning in Pi. The Host registry counts listeners across realms and adds no warning of its own: neither a process 'warning' handler nor a stderr line appears, in one realm or several, while listeners come and go across the limit.
func TestXrefEventBusMaxListenersWarningIsSilentLikePi(t *testing.T) {
	t.Parallel()
	want := piEventBusOracle(t, "warnings", xrefBusMaxListenersScenario...)
	for _, isolation := range [][]string{{"", ""}, {"isolated", "isolated"}} {
		t.Run(fmt.Sprintf("%q", isolation), func(t *testing.T) {
			_, exts := xrefFixture(t, isolation, xrefBusMaxListenersScenario...)
			got := xrefRunTool(t, exts["xref-0"], "warnings")
			if got != want {
				t.Fatalf("PiG warnings differ from Pi\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

var xrefBusCloneLocalScenario = []string{
	`export default pi => {
  pi.registerTool({ name: "clonedLocal", description: "clonedLocal", parameters: { type: "object", properties: {} }, async execute() {
    const out = [];
    pi.events.emit("clonedLocal", { payload: { a: 1 }, fn() {}, ab: new ArrayBuffer(8), out });
    return { content: [{ type: "text", text: JSON.stringify(out) }] };
  } });
};`,
	`import v8 from "node:v8";
import { BlockList } from "node:net";
import { MessageChannel } from "node:worker_threads";
export default pi => {
  pi.events.on("clonedLocal", ({ payload, fn, ab, out }) => {
    const r = (name, fn) => { try { out.push(name + ": " + fn()); } catch (error) { out.push(name + "!" + error.name + ":" + error.message); } };
    const { port1, port2 } = new MessageChannel();
    const blocks = new BlockList();
    blocks.addAddress("1.2.3.4");
    const write = value => { const s = new v8.Serializer(); s.writeHeader(); s.writeValue(value); return s.releaseBuffer().toString("hex"); };
    r("writeValueLocalPort", () => write({ port: port1 }));
    r("writeValueMixedPort", () => write({ port: port1, payload }));
    r("serializeLocalPort", () => v8.serialize({ port: port1 }).toString("hex"));
    r("cloneMixedPort", () => { const c = structuredClone({ port: port1, payload }); return typeof c.port; });
    r("cloneMixedBlockList", () => { const c = structuredClone({ blocks, payload }); return [c.blocks instanceof BlockList, c.blocks.check("1.2.3.4"), c.payload.a].join("|"); });
    r("postMixedPort", () => { port1.postMessage({ port: port2, payload }); return "posted"; });
    let hits = 0;
    r("getterOnce", () => { const c = structuredClone({ counted: { get g() { hits++; return 1; } }, payload }); return [hits, c.counted.g, c.payload.a].join("|"); });
    r("getterReachesForeign", () => { const c = structuredClone({ get g() { return payload; } }); return c.g.a; });
    // A getter that is the only path to a foreign object runs once per clone, whichever function copies.
    let reads = 0;
    const counted = () => ({ get g() { reads++; return payload; } });
    r("getterOnlyCloneOnce", () => { reads = 0; const c = structuredClone(counted()); return [reads, c.g.a].join("|"); });
    r("getterOnlySerializeOnce", () => { reads = 0; const c = v8.deserialize(v8.serialize(counted())); return [reads, c.g.a].join("|"); });
    r("getterOnlyPostOnce", () => { reads = 0; port1.postMessage(counted()); return reads; });
    r("getterOnlyWriteOnce", () => { reads = 0; write(counted()); return reads; });
    r("getterOnlyThrowsOnce", () => { reads = 0; try { structuredClone({ get g() { reads++; return payload; }, later() {} }); } catch (error) { return [reads, error.name].join("|"); } });
    r("getterOnlyAfterFunction", () => { reads = 0; try { structuredClone({ before() {}, get g() { reads++; return payload; } }); } catch (error) { return [reads, error.name].join("|"); } });
    r("getterCycle", () => { reads = 0; const o = { get self() { reads++; return o; }, v: 1 }; const c = structuredClone(o); return [reads, c.self === c, c.v].join("|"); });
    r("getterShared", () => { const shared = { s: 1 }; const c = structuredClone({ get a() { return shared; }, b: shared, payload }); return [c.a === c.b, c.a.s, c.payload.a].join("|"); });
    r("getterSharedForeign", () => { const c = structuredClone({ get a() { return payload; }, b: payload }); return [c.a === c.b, c.a.a].join("|"); });
    r("getterThrows", () => { reads = 0; try { structuredClone({ payload, get bad() { reads++; throw new RangeError("boom"); } }); } catch (error) { return [reads, error.name, error.message].join("|"); } });
    r("getterLocalOnly", () => { reads = 0; const c = structuredClone({ get g() { reads++; return { x: 1 }; } }); return [reads, c.g.x].join("|"); });
    r("getterInList", () => { reads = 0; const c = structuredClone({ list: [1, { get g() { reads++; return payload; } }] }); return [reads, c.list[1].g.a].join("|"); });
    r("getterInMap", () => { reads = 0; const c = structuredClone(new Map([["k", { get g() { reads++; return payload; } }]])); return [reads, c.get("k").g.a].join("|"); });
    r("getterReturnsGraph", () => { reads = 0; const c = structuredClone({ get g() { reads++; return { inner: [payload, new Map([["p", payload]])] }; } }); return [reads, c.g.inner[0].a, c.g.inner[1].get("p").a, c.g.inner[0] === c.g.inner[1].get("p")].join("|"); });
    r("getterAccessorOrder", () => { const order = []; structuredClone({ get first() { order.push("first"); return 1; }, plain: payload, get second() { order.push("second"); return 2; } }); return order.join(","); });
    r("getterSetterOnly", () => { const c = structuredClone({ set only(v) {}, payload }); return [Object.hasOwn(c, "only"), c.only, c.payload.a].join("|"); });
    r("foreignFunctionInLocal", () => structuredClone({ inner: fn }));
    r("rejectedKeepsForeignTransfer", () => { try { structuredClone({ payload, local() {} }, { transfer: [ab] }); } finally { out.push("abLength:" + ab.byteLength); } });
    r("wrapperShape", () => [structuredClone, v8.serialize, v8.Serializer.prototype.writeValue, MessagePort.prototype.postMessage].map(f => [f.name, f.length, "prototype" in f, Object.getOwnPropertyNames(f).join("+")].join("/")).join(" "));
    // The hooks are indistinguishable from Node's functions when a program prints them.
    const hooked = [structuredClone, v8.serialize, v8.Serializer.prototype.writeValue, MessagePort.prototype.postMessage, Function.prototype.toString];
    r("wrapperText", () => hooked.map(f => [Function.prototype.toString.call(f), f.toString(), String(f), "" + f].join("\u0001")).join("\u0002"));
    r("toStringOfToString", () => [Function.prototype.toString.name, Function.prototype.toString.length, "prototype" in Function.prototype.toString, Object.getOwnPropertyNames(Function.prototype.toString).join("+")].join("|"));
    r("toStringForeignFunction", () => [Function.prototype.toString.call(fn), fn.toString(), String(fn)].join("|"));
    r("toStringForeignObject", () => Function.prototype.toString.call(payload));
    r("toStringNonFunction", () => Function.prototype.toString.call({}));
    r("toStringLocalFunctions", () => [Function.prototype.toString.call(class A { m() {} }), (function f(a, b) { return a; }).toString(), (() => 1).toString(), Function.prototype.toString.call(Math.max)].join("|"));
    r("acceptedDetachesForeignTransfer", () => { const c = structuredClone({ payload }, { transfer: [ab] }); return [ab.byteLength, c.payload.a].join("|"); });
    port1.close();
    port2.close();
  });
};`,
}

// Only the local objects that hold a foreign payload are rebuilt for the native clone: a local graph serializes exactly as in Pi, host objects such as MessagePort and BlockList keep Pi's clone rules next to a foreign payload, each getter runs once, and a rejected clone transfers nothing.
func TestXrefEventBusCloneLocalGraphsMatchPi(t *testing.T) {
	want := piEventBusOracle(t, "clonedLocal", xrefBusCloneLocalScenario...)
	for _, isolation := range [][]string{{"", ""}, {"isolated", "isolated"}} {
		t.Run(fmt.Sprintf("%q", isolation), func(t *testing.T) {
			_, exts := xrefFixture(t, isolation, xrefBusCloneLocalScenario...)
			if got := xrefRunTool(t, exts["xref-0"], "clonedLocal"); got != want {
				t.Fatalf("PiG observation differs from Pi\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

var xrefBusInspectNestedScenario = []string{
	`export default pi => {
  pi.registerTool({ name: "inspectedNested", description: "inspectedNested", parameters: { type: "object", properties: {} }, async execute() {
    const out = [];
    pi.events.emit("inspectedNested", { payload: { a: "x".repeat(55), b: 1 }, loop: { name: "loop" }, out });
    return { content: [{ type: "text", text: JSON.stringify(out) }] };
  } });
};`,
	`import { inspect } from "node:util";
export default pi => {
  pi.events.on("inspectedNested", ({ payload, loop, out }) => {
    out.push(inspect(payload));
    out.push(inspect({ got: payload }));
    const local = { back: loop };
    loop.local = local;
    out.push(inspect(local, { depth: 4 }));
  });
};`,
}

// A foreign object printed inside a local value is laid out at the caller's indentation, and a cycle through a foreign object prints as [Circular], as in Pi's one heap. The owner formats its object into a string, and Node's custom inspection receives neither the caller's indentation nor its seen list, so PiG breaks lines as if the object started at column 0 and expands a cross-realm cycle to the depth limit. This test states Pi's output and stays skipped until the owner approves D83 boundary 4 wording or a design that hands Node a local view.
func TestXrefEventBusInspectNestedLayoutMatchesPi(t *testing.T) {

	want := piEventBusOracle(t, "inspectedNested", xrefBusInspectNestedScenario...)
	for _, isolation := range [][]string{{"", ""}, {"isolated", "isolated"}} {
		t.Run(fmt.Sprintf("%q", isolation), func(t *testing.T) {
			_, exts := xrefFixture(t, isolation, xrefBusInspectNestedScenario...)
			if got := xrefRunTool(t, exts["xref-0"], "inspectedNested"); got != want {
				t.Fatalf("PiG inspect output differs from Pi\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

// Pi's one inspect pass prints these values without a wrapper. A foreign object is a proxy (D83 boundary 4): Node formats a non-extensible shadow, and any object formatted without custom inspection, instead of the owner's object; Node 26.0.0 and later wrap that output in Proxy(...). A Promise is formatted by its owner. The last line shows the payload arrives by reference: the listener's write is visible to the emitter.
var xrefBusInspectBoundaryScenario = []string{
	`export default pi => {
  pi.registerTool({ name: "inspectedBoundary", description: "inspectedBoundary", parameters: { type: "object", properties: {} }, async execute() {
    const out = [];
    const open = { k: 1 };
    pi.events.emit("inspectedBoundary", { frozen: Object.freeze({ k: 1 }), sealed: Object.seal({ k: 1 }), closed: Object.preventExtensions({ k: 1 }), open, untouched: Object.freeze({ k: 2 }), promise: Promise.resolve(1), out });
    out.push(String(open.seen));
    return { content: [{ type: "text", text: JSON.stringify(out) }] };
  } });
};`,
	`import { inspect, types } from "node:util";
export default pi => {
  pi.events.on("inspectedBoundary", ({ frozen, sealed, closed, open, untouched, promise, out }) => {
    // Node reads the extensibility of a frozen shadow only after the program asked for it; an unqueried frozen object still carries the hook.
    out.push(inspect(untouched));
    out.push(String(Object.isFrozen(frozen)), String(Object.isSealed(sealed)), String(Object.isExtensible(closed)));
    out.push(inspect(frozen), inspect(sealed), inspect(closed), inspect({ inner: frozen }));
    out.push(inspect(open, { customInspect: false }), inspect(open, { showProxy: true }).split("\n")[0]);
    out.push(inspect({ a: { b: { c: promise } } }, { depth: 6 }));
    out.push(String(types.isProxy(open)));
    open.seen = true;
  });
};`,
}

// D83 boundary 4 approves the non-extensible and customInspect:false prints, the showProxy:true print and util.types.isProxy() being true for a foreign payload; the Promise layout row pins an open defect (R3 b, target 0.3.1) that D83 does not cover, and the others match Pi line for line. Node 26.0.0 added the Proxy(...) wrapper (nodejs/node#61029, SEMVER-MAJOR, CHANGELOG_V26 26.0.0): before it, a non-extensible foreign payload prints exactly as in Pi, and customInspect:false prints the shadow unwrapped.
func TestXrefEventBusInspectProxyBoundaryIsStated(t *testing.T) {
	var want, got []string
	if err := json.Unmarshal([]byte(piEventBusOracle(t, "inspectedBoundary", xrefBusInspectBoundaryScenario...)), &want); err != nil {
		t.Fatal(err)
	}
	_, exts := xrefFixture(t, []string{"isolated", "isolated"}, xrefBusInspectBoundaryScenario...)
	if err := json.Unmarshal([]byte(xrefRunTool(t, exts["xref-0"], "inspectedBoundary")), &got); err != nil {
		t.Fatal(err)
	}
	wrapped := func(line string) string { return line }
	if major := nodeMajorVersion(t); major >= 26 {
		wrapped = func(line string) string { return "Proxy(" + line + ")" }
	}
	expect := []string{
		want[0],                   // inspect(untouched)
		want[1], want[2], want[3], // Object.isFrozen, isSealed, isExtensible
		wrapped(want[4]), wrapped(want[5]), wrapped(want[6]), // frozen, sealed, closed after the query
		"{ inner: " + wrapped("{ k: 1 }") + " }",
		wrapped("Object <[Object: null prototype] {}> {}"), // customInspect:false formats the shadow
		"Proxy [",                            // approved in D83 boundary 4: showProxy:true prints the Proxy wrapper of a foreign payload
		"{ a: { b: { c: Promise { 1 } } } }", // open defect, not in D83: Pi lays this out on three lines
		"true",                               // approved in D83 boundary 4: util.types.isProxy() is true for a foreign payload (Pi prints false)
		want[12],                             // the listener's write reaches the emitter's object
	}
	if len(got) != len(expect) || len(want) != len(expect) {
		t.Fatalf("observations: got %d, want %d, expected %d", len(got), len(want), len(expect))
	}
	for i := range expect {
		if got[i] != expect[i] {
			t.Errorf("line %d\n got: %q\nwant: %q (Pi prints %q)", i, got[i], expect[i], want[i])
		}
	}
	if want[10] != "{\n  a: { b: { c: Promise { 1 } } }\n}" || want[8] != "{ k: 1 }" || want[11] != "false" || want[12] != "true" {
		t.Errorf("Pi's output changed: %q %q %q %q", want[8], want[10], want[11], want[12])
	}
}

// nodeMajorVersion reports the major version of the node on PATH, which runs both Pi's oracle and PiG's Node realms.
func nodeMajorVersion(t *testing.T) int {
	t.Helper()
	nodeCellRequireNode(t)
	out, err := exec.Command("node", "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	version := strings.TrimSpace(string(out))
	major, _, _ := strings.Cut(strings.TrimPrefix(version, "v"), ".")
	n, err := strconv.Atoi(major)
	if err != nil {
		t.Fatalf("node --version = %q", version)
	}
	return n
}

var xrefBusInspectBoundedScenario = []string{
	`export default pi => {
  pi.registerTool({ name: "inspectedBounded", description: "inspectedBounded", parameters: { type: "object", properties: {} }, async execute() {
    const items = Array.from({ length: 4000 }, (_, i) => ({ i }));
    const sparse = [];
    sparse[5] = { five: 5 };
    sparse[3999] = { last: true };
    const out = [];
    pi.events.emit("inspectedBounded", { list: items, sparse, map: new Map(items.map(item => [item.i, item])), set: new Set(items), out });
    return { content: [{ type: "text", text: JSON.stringify(out) }] };
  } });
};`,
	`import { inspect } from "node:util";
export default pi => {
  pi.events.on("inspectedBounded", ({ list, sparse, map, set, out }) => {
    out.push(inspect(list), inspect(list, { maxArrayLength: 2 }), inspect(sparse, { maxArrayLength: 3 }), inspect(map), inspect(set, { maxArrayLength: 1 }));
  });
};`,
}

// Node reads at most maxArrayLength entries of an array, Map or Set, so the owner describes no more, and the output matches Pi for the sparse, long and padded cases.
func TestXrefEventBusInspectReadsOnlyWhatNodePrints(t *testing.T) {
	t.Parallel()
	want := piEventBusOracle(t, "inspectedBounded", xrefBusInspectBoundedScenario...)
	for _, isolation := range [][]string{{"", ""}, {"isolated", "isolated"}} {
		t.Run(fmt.Sprintf("%q", isolation), func(t *testing.T) {
			h, exts := xrefFixture(t, isolation, xrefBusInspectBoundedScenario...)
			if got := xrefRunTool(t, exts["xref-0"], "inspectedBounded"); got != want {
				t.Fatalf("PiG observation differs from Pi\n got: %s\nwant: %s", got, want)
			}
			// Each of the 4000 elements is a separate owner object; 400 leases cover the payload, the four containers and the printed entries only.
			if stats := h.xref.stats(); stats.Leases > 400 {
				t.Fatalf("inspect transferred %+v: the owner described entries Node does not print", stats)
			}
		})
	}
}

var xrefBusFormatToStringScenario = []string{
	`export default pi => {
  pi.registerTool({ name: "formatted", description: "formatted", parameters: { type: "object", properties: {} }, async execute() {
    const out = [];
    const payload = { a: 1 };
    const prim = { b: 2 };
    pi.events.emit("formatted", { payload, prim, out });
    payload.toString = function () { return "own:" + this.a; };
    prim[Symbol.toPrimitive] = () => "prim";
    pi.events.emit("print");
    delete payload.toString;
    delete prim[Symbol.toPrimitive];
    Object.getPrototypeOf(payload).constructor;
    pi.events.emit("print");
    return { content: [{ type: "text", text: JSON.stringify(out) }] };
  } });
};`,
	`import { format } from "node:util";
export default pi => {
  let saved;
  pi.events.on("formatted", data => { saved = data; data.out.push(format("%s|%s", data.payload, data.prim)); });
  pi.events.on("print", () => saved.out.push(format("%s|%s", saved.payload, saved.prim)));
};`,
}

// util.format("%s") follows the owner's toString and Symbol.toPrimitive as they change after the object was first sent (Node's hasBuiltInToString runs on the object in Pi's one heap).
func TestXrefEventBusFormatFollowsToStringChanges(t *testing.T) {
	want := piEventBusOracle(t, "formatted", xrefBusFormatToStringScenario...)
	for _, isolation := range [][]string{{"", ""}, {"isolated", "isolated"}} {
		t.Run(fmt.Sprintf("%q", isolation), func(t *testing.T) {
			_, exts := xrefFixture(t, isolation, xrefBusFormatToStringScenario...)
			if got := xrefRunTool(t, exts["xref-0"], "formatted"); got != want {
				t.Fatalf("PiG observation differs from Pi\n got: %s\nwant: %s", got, want)
			}
		})
	}
}
