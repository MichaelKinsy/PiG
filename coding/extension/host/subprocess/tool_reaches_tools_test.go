package subprocess

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi 1.2.0 types.ts:2122-2130 picks reachesTools into ToolInfo, and agent-session.ts:1507 copies the definition's hook onto the
// tools getAllTools lists. A function cannot cross the wire, so getAllTools marks such a tool `"reachesTools": true` in the member
// position upstream's object literal gives it, and an extension runs the hook with the tool.reachesTools call: the answer is the
// string "all" or the words, the input defaults to an empty object, a tool without the hook or not listed fails, and the hook's
// throw is the call's error.
func TestGetAllToolsMarksReachesToolsAndRunsItForTheExtension(t *testing.T) {
	var seen []string
	b := NewUIBridge(func() {})
	b.SetActions(&HostCallbacks{GetAllTools: func() []ToolInfo {
		return []ToolInfo{
			{Name: "finder", Description: "Finds", Parameters: json.RawMessage(`{}`), Exposure: extension.ToolExposureDirect, SourceInfo: extension.SourceInfo{Path: "/f"},
				ReachesTools: func(input json.RawMessage) extension.ToolReach {
					seen = append(seen, string(input))
					var fields map[string]any
					_ = json.Unmarshal(input, &fields)
					if fields["all"] == true {
						return extension.ToolReach{All: true}
					}
					words := make([]string, 0, len(fields))
					for key := range fields {
						words = append(words, key)
					}
					slices.Sort(words)
					return extension.ToolReach{Words: words}
				}},
			{Name: "plain", Description: "Plain", Parameters: json.RawMessage(`{}`), Exposure: extension.ToolExposureDirect, SourceInfo: extension.SourceInfo{Path: "/p"}},
			{Name: "boom", Description: "Boom", Parameters: json.RawMessage(`{}`), Exposure: extension.ToolExposureDirect, SourceInfo: extension.SourceInfo{Path: "/b"},
				ReachesTools: func(json.RawMessage) extension.ToolReach { panic(errors.New("reach exploded")) }},
		}
	}})
	result, err := b.HandleCall("ext", &CallPayload{Method: "getAllTools"})
	if err != nil {
		t.Fatal(err)
	}
	listed := string(result.Result)
	if !strings.Contains(listed, `{"name":"finder","description":"Finds","parameters":{},"exposure":"direct","reachesTools":true,"sourceInfo":`) ||
		!strings.Contains(listed, `{"name":"plain","description":"Plain","parameters":{},"exposure":"direct","sourceInfo":`) {
		t.Fatalf("getAllTools result = %s, want finder marked reachesTools before sourceInfo and plain unmarked", listed)
	}
	reach := func(args string) (string, error) {
		t.Helper()
		result, err := b.HandleCall("ext", &CallPayload{Method: CallToolReachesTools, Args: json.RawMessage(args)})
		if err != nil {
			return "", err
		}
		return string(result.Result), nil
	}
	for _, tc := range []struct{ args, want string }{
		{`{"tool":"finder","input":{"b":1,"a":2}}`, `["a","b"]`},
		{`{"tool":"finder","input":{"all":true}}`, `"all"`},
		{`{"tool":"finder"}`, `[]`},
	} {
		if got, err := reach(tc.args); err != nil || got != tc.want {
			t.Errorf("tool.reachesTools %s = %s, %v; want %s", tc.args, got, err, tc.want)
		}
	}
	if want := []string{`{"b":1,"a":2}`, `{"all":true}`, `{}`}; !slices.Equal(seen, want) {
		t.Errorf("hook inputs = %q, want %q", seen, want)
	}
	for _, tool := range []string{"plain", "missing"} {
		if got, err := reach(`{"tool":"` + tool + `","input":{}}`); err == nil || !strings.Contains(err.Error(), tool+" has no reachesTools") {
			t.Errorf("tool.reachesTools for %s = %s, %v; want an error", tool, got, err)
		}
	}
	if got, err := reach(`{"tool":"boom","input":{}}`); err == nil || err.Error() != "reach exploded" {
		t.Errorf("tool.reachesTools for boom = %s, %v; want the hook's error", got, err)
	}
}

// Pi 24382bed types.ts reachesTools: `(input) => "all" | readonly string[]`, run by the extension that defines the tool. The host asks
// with tool_reaches_tools, a tool that does not declare reaches_tools has none, a failed answer is the hook's throw, and a namespace
// keeps its aliases across the wire.
func TestReachesToolsRunsInTheExtension(t *testing.T) {
	host := newWireHost(t)
	w := newWireExt(t)
	answer := `["docs","search"]`
	w.respond = func(env *Envelope) *ResponsePayload {
		if env.Request.Method != RequestReachesTools {
			return nil
		}
		return &ResponsePayload{Result: json.RawMessage(answer)}
	}
	ext, err := loadWireExt(t, host, w, &RegisterPayload{Name: "reach", Tools: []ToolDecl{
		{Name: "finder", Description: "Finds", Parameters: json.RawMessage(`{"type":"object"}`), ReachesTools: true,
			Namespace: &extension.ToolNamespace{Name: "mcp__docs", Aliases: []string{"docs"}}},
		{Name: "plain", Description: "Plain", Parameters: json.RawMessage(`{"type":"object"}`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if ext.Tools["plain"].Definition.ReachesTools != nil {
		t.Fatal("a tool that does not declare reaches_tools has ReachesTools")
	}
	finder := ext.Tools["finder"].Definition
	if finder.ReachesTools == nil {
		t.Fatal("a tool that declares reaches_tools has no ReachesTools")
	}
	if !reflect.DeepEqual(finder.Namespace.Aliases, []string{"docs"}) {
		t.Errorf("namespace aliases = %v", finder.Namespace.Aliases)
	}
	if got := finder.ReachesTools(json.RawMessage(`{"q":"x"}`)); got.All || !reflect.DeepEqual(got.Words, []string{"docs", "search"}) {
		t.Fatalf("reach = %+v, want words docs, search", got)
	}
	env := w.request(RequestReachesTools)
	if env.Request.Tool != "finder" || string(env.Request.Args) != `{"q":"x"}` {
		t.Fatalf("request = tool %q args %s", env.Request.Tool, env.Request.Args)
	}
	answer = `"all"`
	if got := finder.ReachesTools(json.RawMessage(`{}`)); !got.All || len(got.Words) != 0 {
		t.Fatalf("reach = %+v, want all", got)
	}
	answer = `[]`
	if got := finder.ReachesTools(json.RawMessage(`{}`)); got.All || len(got.Words) != 0 {
		t.Fatalf("reach = %+v, want nothing", got)
	}
	w.respond = func(env *Envelope) *ResponsePayload {
		if env.Request.Method != RequestReachesTools {
			return nil
		}
		return &ResponsePayload{Error: &ErrorInfo{Message: "reach exploded"}}
	}
	defer func() {
		if recovered := recover(); recovered == nil || fmt.Sprint(recovered) != "reach exploded" {
			t.Fatalf("recovered %v, want the extension's \"reach exploded\"", recovered)
		}
	}()
	finder.ReachesTools(json.RawMessage(`{}`))
}

// The Node runtime declares reaches_tools for a tool that defines the hook and answers tool_reaches_tools with its value on
// receipt, as it answers the other synchronous hooks: the answer leaves even while the process waits in a synchronous host call,
// which is where an extension running a hook through getAllTools waits when the hook is its own or a packed neighbor's. Its
// getAllTools turns a tool's reachesTools mark into a function in the same member position that runs tool.reachesTools.
func TestNodeRuntimeReachesTools(t *testing.T) {
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { Runtime } from %q;
const runtime = new Runtime("/ext/reach.mjs");
runtime.conn = { closed: false, callSync() { return null; }, call: async () => null, requestState() {}, notify() {}, send() {} };
runtime.hostReady = true;
runtime.commitLoad();
const seen = [];
runtime.api.registerTool({
  name: "finder", label: "F", description: "d", parameters: { type: "object" },
  namespace: { name: "mcp__docs", aliases: ["docs"] },
  reachesTools(input) { seen.push(input); if (input.fail) throw new Error("reach exploded"); return input.all ? "all" : ["docs"]; },
  async execute() { return { content: [] }; },
});
runtime.api.registerTool({ name: "plain", label: "P", description: "d", parameters: { type: "object" }, async execute() { return { content: [] }; } });
assert.equal(runtime.toolDeclaration(runtime.tools.get("finder")).reaches_tools, true);
assert.deepEqual(runtime.toolDeclaration(runtime.tools.get("finder")).namespace, { name: "mcp__docs", aliases: ["docs"] });
assert.equal(runtime.toolDeclaration(runtime.tools.get("plain")).reaches_tools, undefined);
const frames = [];
const socket = new EventEmitter();
socket.write = (data) => {
  for (let at = 0; at < data.length; ) {
    const size = data.readUInt32BE(at);
    frames.push(JSON.parse(data.subarray(at + 4, at + 4 + size).toString()));
    at += 4 + size;
  }
  return true;
};
const conn = runtime.attachConnection(socket);
const answer = (id, request) => {
  socket.emit("envelope", { type: "request", id, request });
  return frames.find((env) => env.type === "response" && env.id === id)?.response;
};
assert.deepEqual(answer("r1", { method: "tool_reaches_tools", tool: "finder", args: { all: true } }), { result: "all" });
assert.deepEqual(answer("r2", { method: "tool_reaches_tools", tool: "finder", args: {} }), { result: ["docs"] });
assert.equal(answer("r3", { method: "tool_reaches_tools", tool: "finder", args: { fail: true } })?.error?.message, "reach exploded");
assert.match(answer("r4", { method: "tool_reaches_tools", tool: "plain", args: {} })?.error?.message ?? "", /plain has no reachesTools/);
assert.match(answer("r5", { method: "tool_reaches_tools", tool: "missing", args: {} })?.error?.message ?? "", /missing has no reachesTools/);
assert.deepEqual(conn.queue, []);
assert.deepEqual(seen, [{ all: true }, {}, { fail: true }]);

const calls = [];
runtime.conn = {
  closed: false, call: async () => null, requestState() {}, notify() {}, send() {},
  callSync(method, args) {
    calls.push([method, args]);
    if (method === "getAllTools") return { tools: [
      { name: "finder", description: "d", parameters: {}, exposure: "direct", reachesTools: true, sourceInfo: { path: "/f" }, source: "reach" },
      { name: "plain", description: "d", parameters: {}, exposure: "direct", sourceInfo: { path: "/p" }, source: "reach" },
    ] };
    if (method === "tool.reachesTools") return args.input.all ? "all" : ["docs"];
    throw new Error("unexpected host call " + method);
  },
};
const [finderInfo, plainInfo] = runtime.api.getAllTools();
assert.deepEqual(Object.keys(finderInfo), ["name", "description", "parameters", "exposure", "reachesTools", "sourceInfo"]);
assert.equal(typeof finderInfo.reachesTools, "function");
assert.deepEqual(Object.keys(plainInfo), ["name", "description", "parameters", "exposure", "sourceInfo"]);
assert.equal(finderInfo.reachesTools({ all: true }), "all");
assert.deepEqual(finderInfo.reachesTools({ q: 1 }), ["docs"]);
assert.deepEqual(calls.slice(1), [["tool.reachesTools", { tool: "finder", input: { all: true } }], ["tool.reachesTools", { tool: "finder", input: { q: 1 } }]]);
assert.equal(JSON.stringify(finderInfo).includes("reachesTools"), false);
`, (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String())
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}
