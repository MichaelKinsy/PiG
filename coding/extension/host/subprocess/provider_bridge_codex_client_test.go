//go:build !pig_strip_node_extensions

package subprocess

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// pi: packages/ai/src/api/openai-codex-responses.ts closeOpenAICodexWebSocketSessions, getOpenAICodexWebSocketDebugStats, resetOpenAICodexWebSocketDebugStats
//
// The client side of the Codex WebSocket helper lines (shims/provider-bridge-client.mjs): one-way close and reset lines to the bridge child that serves the process, a replica of its stats the child pushes,
// the synchronous getter over it. A fake bridge speaks the wire so each rule is checked alone; the whole path with the Go bridge is TestCodexWebSocketHelpersOfTheHarnessPeerActAsUnderPi.
const fakeCodexBridge = `
import { createInterface } from "node:readline";
import { appendFileSync } from "node:fs";
const log = process.env.FAKE_BRIDGE_LOG;
appendFileSync(log, JSON.stringify({ started: true }) + "\n");
const send = (o) => process.stdout.write(JSON.stringify(o) + "\n");
const base = (requests) => ({ requests, connectionsCreated: 1, connectionsReused: 0, cachedContextRequests: 0, storeTrueRequests: 0, fullContextRequests: requests, deltaRequests: 0, lastInputItems: 1, websocketFailures: 0, sseFallbacks: 0 });
const message = (text) => ({ role: "assistant", content: [{ type: "text", text }], stopReason: "stop", api: "openai-codex-responses", provider: "p", model: "m", usage: { input: 1, output: 1, cacheRead: 0, cacheWrite: 0, totalTokens: 2, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } }, timestamp: 0 });
const start = (id) => send({ id, type: "event", event: { type: "start", partial: message("") } });
const finish = (id) => { send({ id, type: "event", event: { type: "done", reason: "stop", message: message("done") } }); send({ id, type: "result", result: message("done") }); };
createInterface({ input: process.stdin }).on("line", (line) => {
  appendFileSync(log, line + "\n");
  const r = JSON.parse(line);
  if (r.type !== "stream") return;
  const mode = r.request.messages[0].content;
  if (mode === "first") { send({ id: "", type: "codexStats", sessionId: "s1", stats: base(1) }); start(r.id); finish(r.id); }
  else if (mode === "second") { send({ id: "", type: "codexStats", sessionId: "s2", stats: base(1) }); start(r.id); finish(r.id); }
  else if (mode === "lag") { start(r.id); setTimeout(() => { send({ id: "", type: "codexStats", sessionId: "s1", stats: { ...base(2), deltaRequests: 1, fullContextRequests: 1, lastDeltaInputItems: 1, lastPreviousResponseId: "resp_1" } }); finish(r.id); }, 300); }
  else if (mode === "failure") {
    send({ id: "", type: "codexStats", sessionId: "s3", stats: { ...base(0), fullContextRequests: 0, lastInputItems: 0, websocketFailures: 1, lastWebSocketError: "boom", websocketFallbackActive: true } });
    send({ id: "", type: "codexStats", sessionId: "s3", stats: { ...base(1), websocketFailures: 1, lastWebSocketError: "boom", websocketFallbackActive: true } });
    start(r.id); finish(r.id);
  }
  else if (mode === "clear-one") { send({ id: "", type: "codexStats", sessionId: "s2" }); start(r.id); finish(r.id); }
  else if (mode === "clear-all") { send({ id: "", type: "codexStats" }); start(r.id); finish(r.id); }
  else if (mode === "die") { start(r.id); process.exit(3); }
});
`

const codexClientScript = `
import { readFileSync, existsSync } from "node:fs";
const client = await import("file://" + process.argv[2]);
const { bridgeStream, getCodexWebSocketDebugStats: get, closeCodexWebSocketSessions: close, resetCodexWebSocketDebugStats: reset } = client;
const log = process.env.FAKE_BRIDGE_LOG;
const model = { provider: "p", id: "m", api: "openai-codex-responses" };
const failed = (m, message) => ({ failedWith: message });
const drain = async (stream) => { const types = []; for await (const e of stream) types.push(e.type); await stream.result(); return types; };
const ask = (text) => bridgeStream(model, { messages: [{ role: "user", content: text, timestamp: 0 }] }, { apiKey: "K", sessionId: "x" }, true, failed);
const out = {};

// No child yet: the helpers are Pi's no-ops and the getter has nothing; none of them starts a bridge.
close("s1"); close(); reset("s1"); reset();
out.noChild = { stats: get("s1") === undefined, started: existsSync(log) };

await drain(ask("first"));
const first = get("s1");
out.firstKeys = Object.keys(first);
out.firstUndefinedMembers = { delta: "lastDeltaInputItems" in first && first.lastDeltaInputItems === undefined, previous: "lastPreviousResponseId" in first && first.lastPreviousResponseId === undefined, error: "lastWebSocketError" in first, active: "websocketFallbackActive" in first };
first.requests = 99;
out.copy = get("s1").requests;
out.unknown = get("nope") === undefined;

// During a stream the getter shows what the bridge has pushed so far; the next push lands when its line is read.
const lagging = ask("lag");
const seen = [];
for await (const event of lagging) { seen.push({ type: event.type, requests: get("s1").requests }); }
await lagging.result();
out.lag = seen;
out.lagDelta = { delta: get("s1").lastDeltaInputItems, previous: get("s1").lastPreviousResponseId };

await drain(ask("failure"));
out.failureKeys = Object.keys(get("s3"));
out.failure = { failures: get("s3").websocketFailures, active: get("s3").websocketFallbackActive, requests: get("s3").requests };

await drain(ask("second"));
close("s1"); close(); 
reset("s1");
out.resetSync = get("s1") === undefined && get("s2") !== undefined;
reset();
out.resetAllSync = get("s2") === undefined && get("s3") === undefined;

await drain(ask("first")); await drain(ask("second"));
await drain(ask("clear-one"));
out.clearOne = { s1: get("s1") !== undefined, s2: get("s2") === undefined };
await drain(ask("clear-all"));
out.clearAll = get("s1") === undefined;

await drain(ask("first"));
const dying = ask("die");
for await (const e of dying) void e;
await dying.result();
out.afterDeath = get("s1") === undefined;
close("s1"); reset("s1");
await new Promise((resolve) => setTimeout(resolve, 100));
out.log = readFileSync(log, "utf8").trim().split("\n").map((l) => JSON.parse(l));
console.log(JSON.stringify(out));
`

func TestProviderBridgeClientCodexWebSocketHelpers(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required: %v", err)
	}
	runtime := t.TempDir()
	if err := extractNodeRuntime(runtime); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	fake, script, logPath := write("fake-codex-bridge.mjs", fakeCodexBridge), write("client.mjs", codexClientScript), filepath.Join(dir, "bridge.log")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), node, script, filepath.Join(runtime, "shims", "provider-bridge-client.mjs"))
	command.Env = append(os.Environ(), "PIG_HARNESS_BINARY="+binary, fakeProviderBridgeNodeEnv+"="+node, fakeProviderBridgeScriptEnv+"="+fake, "FAKE_BRIDGE_LOG="+logPath)
	done := make(chan struct{})
	var output []byte
	go func() {
		defer close(done)
		output, err = command.Output()
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("the client did not finish")
	}
	if err != nil {
		var stderr []byte
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			stderr = exit.Stderr
		}
		t.Fatalf("client script: %v\n%s\n%s", err, output, stderr)
	}
	var got struct {
		NoChild               struct{ Stats, Started bool }
		FirstKeys             []string
		FirstUndefinedMembers struct{ Delta, Previous, Error, Active bool }
		Copy                  int
		Unknown               bool
		Lag                   []struct {
			Type     string
			Requests int
		}
		LagDelta struct {
			Delta    int
			Previous string
		}
		FailureKeys []string
		Failure     struct {
			Failures int
			Active   bool
			Requests int
		}
		ResetSync    bool
		ResetAllSync bool
		ClearOne     struct{ S1, S2 bool }
		ClearAll     bool
		AfterDeath   bool
		Log          []map[string]any
	}
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("output %q: %v", output, err)
	}
	if !got.NoChild.Stats || got.NoChild.Started {
		t.Errorf("close, reset and the getter with no stream: %+v, want no stats and no bridge started", got.NoChild)
	}
	// Pi's first object: the ten counters in declaration order, then lastDeltaInputItems and lastPreviousResponseId, which the request path assigns `undefined` (members, not absent).
	wantKeys := []string{"requests", "connectionsCreated", "connectionsReused", "cachedContextRequests", "storeTrueRequests", "fullContextRequests", "deltaRequests", "lastInputItems", "websocketFailures", "sseFallbacks", "lastDeltaInputItems", "lastPreviousResponseId"}
	if !reflect.DeepEqual(got.FirstKeys, wantKeys) {
		t.Errorf("members of a replicated stats object %v, want %v", got.FirstKeys, wantKeys)
	}
	if !got.FirstUndefinedMembers.Delta || !got.FirstUndefinedMembers.Previous || got.FirstUndefinedMembers.Error || got.FirstUndefinedMembers.Active {
		t.Errorf("optional members %+v: lastDeltaInputItems and lastPreviousResponseId are undefined members, lastWebSocketError and websocketFallbackActive absent", got.FirstUndefinedMembers)
	}
	if got.Copy != 1 || !got.Unknown {
		t.Errorf("getter returns a copy (requests %d after the caller changed its object, want 1) and undefined for an unknown session (%v)", got.Copy, got.Unknown)
	}
	// Lag window, pinned: at the first event the helper has read only the first push (requests 1); the push of the next request lands when its line is read, before that stream's last event.
	if len(got.Lag) != 2 || got.Lag[0].Type != "start" || got.Lag[0].Requests != 1 || got.Lag[1].Type != "done" || got.Lag[1].Requests != 2 {
		t.Errorf("getter during a stream %+v, want requests 1 at start and 2 at done", got.Lag)
	}
	if got.LagDelta.Delta != 1 || got.LagDelta.Previous != "resp_1" {
		t.Errorf("delta members %+v after a delta request", got.LagDelta)
	}
	// A session whose first change was a failure has Pi's insertion order: the failure's members before the request's.
	wantFailureKeys := append(append([]string{}, wantKeys[:10]...), "lastWebSocketError", "websocketFallbackActive", "lastDeltaInputItems", "lastPreviousResponseId")
	if !reflect.DeepEqual(got.FailureKeys, wantFailureKeys) {
		t.Errorf("members after failure then request %v, want %v", got.FailureKeys, wantFailureKeys)
	}
	if got.Failure.Failures != 1 || !got.Failure.Active || got.Failure.Requests != 1 {
		t.Errorf("failure stats %+v", got.Failure)
	}
	if !got.ResetSync || !got.ResetAllSync {
		t.Errorf("reset must clear the replica before it returns (one session %v, all %v)", got.ResetSync, got.ResetAllSync)
	}
	if !got.ClearOne.S1 || !got.ClearOne.S2 || !got.ClearAll {
		t.Errorf("a push with no stats clears that session (%+v); one with no session clears all (%v)", got.ClearOne, got.ClearAll)
	}
	if !got.AfterDeath {
		t.Error("the replica of a bridge that exited must not outlive its Go cache")
	}
	var lines []string
	starts := 0
	for _, entry := range got.Log {
		if entry["started"] == true {
			starts++
		}
		if entry["type"] != "codexWebSocket" {
			continue
		}
		id, _ := entry["id"].(string)
		if !strings.HasPrefix(id, "codex-ws-") {
			t.Errorf("line %v has no codex-ws- id", entry)
		}
		delete(entry, "id")
		raw, _ := json.Marshal(entry)
		lines = append(lines, string(raw))
	}
	want := []string{
		`{"op":"close","sessionId":"s1","type":"codexWebSocket"}`, `{"op":"close","type":"codexWebSocket"}`,
		`{"op":"reset","sessionId":"s1","type":"codexWebSocket"}`, `{"op":"reset","type":"codexWebSocket"}`,
	}
	// After the bridge exited, close and reset write nothing and start nothing: the last two calls of the script add no line and no second bridge.
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("lines written to the bridge %v, want %v", lines, want)
	}
	if starts != 1 {
		t.Errorf("bridge started %d times, want once, by the first stream", starts)
	}
}
