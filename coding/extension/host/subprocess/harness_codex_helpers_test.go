//go:build !pig_strip_node_extensions && !windows

package subprocess

// pi: packages/ai/src/api/openai-codex-responses.ts closeOpenAICodexWebSocketSessions, getOpenAICodexWebSocketDebugStats, resetOpenAICodexWebSocketDebugStats

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// The three Codex WebSocket helpers of `@earendil-works/pi-ai/api/openai-codex-responses` act on process-local maps in Pi. A detached helper that resolves the module from PiG's harness peer reaches the
// Go cache of the `pig provider-bridge` child that serves its streams: close and reset are one-way lines to it; the synchronous getter reads a replica the child pushes after each change, before the
// result line of the stream. The scenario below runs in Pi's own module and in the harness peer against one local Codex WebSocket server; every observation must be identical.

// codexE2EServer is the one local endpoint both helpers talk to. GET /_counts reports the connections and messages it has seen, POST /_mode?m=ok|drop|hold sets how a message is answered (drop: partial output,
// then the connection ends; hold: the first two events, then nothing until POST /_release).
type codexE2EServer struct {
	*httptest.Server
	upgrades, messages, posts atomic.Int32
	mu                        sync.Mutex
	mode                      string
	frames                    []string
	release                   chan struct{}
}

// reset returns the server to its state before any helper ran: no connections counted, mode ok, no release pending.
func (s *codexE2EServer) reset() {
	s.upgrades.Store(0)
	s.messages.Store(0)
	s.posts.Store(0)
	s.mu.Lock()
	s.mode = "ok"
	s.frames = nil
	s.mu.Unlock()
	for len(s.release) > 0 {
		<-s.release
	}
}

func newCodexE2EServer(t *testing.T) *codexE2EServer {
	t.Helper()
	server := &codexE2EServer{mode: "ok", release: make(chan struct{}, 8)}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	events := func(id string) []map[string]any {
		return []map[string]any{
			{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "message", "id": "msg_" + id, "status": "in_progress"}},
			{"type": "response.output_text.delta", "output_index": 0, "delta": "ok"},
			{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "message", "id": "msg_" + id, "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "ok"}}}},
			{"type": "response.completed", "response": map[string]any{"id": "resp_" + id, "status": "completed"}},
		}
	}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/_counts":
			_ = json.NewEncoder(w).Encode(map[string]int32{"upgrades": server.upgrades.Load(), "messages": server.messages.Load(), "posts": server.posts.Load()})
			return
		case r.URL.Path == "/_frames":
			server.mu.Lock()
			taken := append([]string{}, server.frames...)
			server.frames = nil
			server.mu.Unlock()
			_ = json.NewEncoder(w).Encode(taken)
			return
		case r.URL.Path == "/_mode":
			server.mu.Lock()
			server.mode = r.URL.Query().Get("m")
			server.mu.Unlock()
			return
		case r.URL.Path == "/_release":
			server.release <- struct{}{}
			return
		case r.Method == http.MethodPost:
			server.posts.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"type\":\"response.done\",\"response\":{\"id\":\"resp_sse\",\"status\":\"completed\"}}\n\n"))
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		server.upgrades.Add(1)
		defer func() { _ = conn.Close() }()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			server.mu.Lock()
			server.frames = append(server.frames, string(data))
			server.mu.Unlock()
			server.messages.Add(1)
			server.mu.Lock()
			mode := server.mode
			server.mu.Unlock()
			sent := events("ok")
			switch mode {
			case "drop":
				_ = conn.WriteJSON(sent[0])
				_ = conn.WriteJSON(sent[1])
				return
			case "hold":
				_ = conn.WriteJSON(sent[0])
				_ = conn.WriteJSON(sent[1])
				select {
				case <-server.release:
				case <-time.After(30 * time.Second):
					return
				}
				sent = sent[2:]
			}
			for _, event := range sent {
				if err := conn.WriteJSON(event); err != nil {
					return
				}
			}
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// codexScenario is the helper both sides run. describe records a stats object as a reader of it sees it: its member names in order, each member's type and value, `undefined` as its own entry
// (a member Pi assigned undefined is a member), and the text of lastWebSocketError as only its type (it is a Node transport error under Pi and a Go one under PiG).
const codexScenario = `
const base = process.env.CODEX_SERVER;
const mod = await import("@earendil-works/pi-ai/api/openai-codex-responses");
const { closeOpenAICodexWebSocketSessions: close, getOpenAICodexWebSocketDebugStats: get, resetOpenAICodexWebSocketDebugStats: reset } = mod;
const claims = Buffer.from(JSON.stringify({ "https://api.openai.com/auth": { chatgpt_account_id: "acct_e2e" } })).toString("base64url");
const apiKey = "header." + claims + ".signature";
const model = { id: "gpt-5.2", name: "Codex", api: "openai-codex-responses", provider: "openai-codex", baseUrl: base, reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 4096 };
const context = (text) => ({ messages: [{ role: "user", content: text, timestamp: 0 }] });
const counts = async () => (await fetch(base + "/_counts")).json();
const mode = (m) => fetch(base + "/_mode?m=" + m, { method: "POST" });
const describe = (stats) => {
  if (stats === undefined) return "undefined";
  const members = {};
  for (const key of Object.keys(stats)) {
    const value = stats[key];
    members[key] = value === undefined ? "<undefined>" : key === "lastWebSocketError" ? [typeof value, "<text>"] : [typeof value, value];
  }
  return { names: Object.keys(stats), members };
};
const log = [];
const note = async (step, ...sessions) => log.push({ step, stats: sessions.map((s) => describe(get(s))), counts: await counts() });
// transport "auto" is named, as Pi's coding agent always names one; the requests with no transport and with each other value are compared by the "transport" subtest.
const run = async (session, text, onEvent) => {
  const stream = mod.streamSimple(model, context(text), { apiKey, sessionId: session, transport: "auto" });
  const types = [];
  for await (const event of stream) { types.push(event.type); if (onEvent) await onEvent(event, types); }
  return { types, stop: (await stream.result()).stopReason };
};

await note("fresh", "sA");
log.push({ step: "stream sA", run: await run("sA", "one") }); await note("after sA", "sA");
log.push({ step: "stream sA again", run: await run("sA", "two") }); await note("sA reused", "sA");
log.push({ step: "stream sB", run: await run("sB", "three") }); await note("sB", "sA", "sB");
close("sA"); log.push({ step: "stream sA after close(sA)", run: await run("sA", "four") }); await note("close(sA)", "sA", "sB");
log.push({ step: "stream sB after close(sA)", run: await run("sB", "five") }); await note("sB kept its connection", "sA", "sB");
close(); log.push({ step: "stream sA after close()", run: await run("sA", "six") }); log.push({ step: "stream sB after close()", run: await run("sB", "seven") }); await note("close() closed every session", "sA", "sB");
close("never-used"); close(""); await note("close of a session with no connection is silent", "sA", "sB");

await mode("drop");
log.push({ step: "stream sC dropped mid-stream", run: await run("sC", "eight") }); await note("sC failed", "sC");
await mode("ok");
log.push({ step: "stream sC falls back to SSE", run: await run("sC", "nine") }); await note("sC sse fallback", "sC");
reset("sC"); await note("reset(sC)", "sC", "sA");
log.push({ step: "stream sC tries the WebSocket again", run: await run("sC", "ten") }); await note("sC after reset", "sC");
await mode("drop");
log.push({ step: "stream sD dropped", run: await run("sD", "eleven") });
await mode("ok");
reset(); await note("reset() cleared every session", "sA", "sB", "sC", "sD");
log.push({ step: "stream sD after reset()", run: await run("sD", "twelve") }); await note("sD after reset()", "sD");

// The lag window: during a stream the getter shows what the helper has been told; once the stream's result is read it is exact.
await mode("hold");
const during = [];
const held = run("sE", "thirteen", async (event, types) => {
  if (types.length === 3) { during.push({ atEvent: types.length, stats: describe(get("sE")) }); await fetch(base + "/_release", { method: "POST" }); }
});
log.push({ step: "stream sE held", run: await held, during });
await mode("ok");
await note("sE after its result", "sE");
process.stdout.write(JSON.stringify(log), () => process.exit(0));
`

// runCodexScenario runs the scenario in dir: Pi's own layout (the pinned package in node_modules) or the harness directory of the extracted runtime, against the one server.
func runCodexScenario(t *testing.T, server *codexE2EServer, node, dir, pigBinary, script string) []json.RawMessage {
	t.Helper()
	server.reset()
	file := filepath.Join(dir, "codex-scenario.mjs")
	if err := os.WriteFile(file, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	command := exec.CommandContext(t.Context(), node, file)
	command.Dir = dir
	command.Env = append(os.Environ(), "CODEX_SERVER="+server.URL, "HOME="+scratch, "PIG_HOME="+scratch, "PI_HOME="+scratch, "PIG_CODING_AGENT_DIR="+scratch, "PI_CODING_AGENT_DIR="+scratch)
	if pigBinary != "" {
		command.Env = append(command.Env, "PIG_HARNESS_BINARY="+pigBinary)
	}
	done := make(chan struct{})
	var output []byte
	var runErr error
	go func() {
		defer close(done)
		output, runErr = command.CombinedOutput()
	}()
	select {
	case <-done:
	case <-time.After(testbudget.Wait(t)):
		_ = command.Process.Kill()
		<-done
		t.Fatalf("scenario in %s did not finish:\n%s", dir, output)
	}
	if runErr != nil {
		t.Fatalf("scenario in %s: %v\n%s", dir, runErr, output)
	}
	var log []json.RawMessage
	if err := json.Unmarshal(output, &log); err != nil {
		t.Fatalf("scenario in %s printed %q: %v", dir, output, err)
	}
	return log
}

// buildPigBinary builds the production `pig` once into dir: the helper's PIG_HARNESS_BINARY, so the bridge child is the real Go one.
func buildPigBinary(t *testing.T, dir string) string {
	t.Helper()
	binary := filepath.Join(dir, "pig")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/pig")
	build.Dir = findModuleRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/pig: %v\n%s", err, out)
	}
	return binary
}

func piAndHarnessDirs(t *testing.T) (piDir, harnessDir string) {
	t.Helper()
	agent := filepath.Join(findModuleRoot(t), "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent")
	aiRoot, err := filepath.EvalSymlinks(filepath.Join(agent, "node_modules", "@earendil-works", "pi-ai"))
	if err != nil {
		t.Fatal(err)
	}
	piDir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(piDir, "node_modules", "@earendil-works"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(aiRoot, filepath.Join(piDir, "node_modules", "@earendil-works", "pi-ai")); err != nil {
		t.Fatal(err)
	}
	runtime := t.TempDir()
	if err := extractNodeRuntime(runtime); err != nil {
		t.Fatal(err)
	}
	return piDir, filepath.Join(runtime, "harness")
}

func TestCodexWebSocketHelpersOfTheHarnessPeerActAsUnderPi(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required: %v", err)
	}
	piDir, harnessDir := piAndHarnessDirs(t)
	pigBinary := buildPigBinary(t, t.TempDir())

	t.Run("scenario", func(t *testing.T) {
		server := newCodexE2EServer(t)
		pi := runCodexScenario(t, server, node, piDir, "", codexScenario)
		pig := runCodexScenario(t, server, node, harnessDir, pigBinary, codexScenario)
		if len(pi) != len(pig) || len(pi) < 20 {
			t.Fatalf("Pi logged %d observations, PiG %d, want the same count of at least 20", len(pi), len(pig))
		}
		for i := range pi {
			if string(pi[i]) != string(pig[i]) {
				t.Errorf("observation %d differs\n Pi : %s\n PiG: %s", i, pi[i], pig[i])
			}
		}
	})

	// The request path with the transport named or not: two turns of one session per transport value against one server, the request frames Pi and the harness peer sent compared byte for byte, and the session's stats. An unset
	// transport is none of "auto" and "websocket-cached", so Pi's second request sends the full context (openai-codex-responses.ts:1539), where the transport "auto" sends the delta.
	t.Run("transport", func(t *testing.T) {
		server := newCodexE2EServer(t)
		pi := runCodexScenario(t, server, node, piDir, "", codexTransportScenario)
		pig := runCodexScenario(t, server, node, harnessDir, pigBinary, codexTransportScenario)
		compareCodexTransportObservations(t, pi, pig)
	})

	// The cleanup Pi registers at module load and the helpers called with no stream never start a bridge child; a close line written just before the helper exits leaves no child behind.
	t.Run("no child unless a stream needs one", func(t *testing.T) {
		pidFile := filepath.Join(t.TempDir(), "child-pid")
		wrapper := recordingBinary(t, pigBinary, pidFile)
		runHelper(t, node, harnessDir, wrapper, `
const mod = await import("@earendil-works/pi-ai/api/openai-codex-responses");
mod.closeOpenAICodexWebSocketSessions("x"); mod.closeOpenAICodexWebSocketSessions(); mod.resetOpenAICodexWebSocketDebugStats("x"); mod.resetOpenAICodexWebSocketDebugStats();
if (mod.getOpenAICodexWebSocketDebugStats("x") !== undefined) throw new Error("stats of a session nothing used");
`)
		if _, err := os.Stat(pidFile); err == nil {
			t.Fatal("close, reset and the getter started a provider bridge child")
		}
	})
	t.Run("a close line written just before the helper exits leaves no child", func(t *testing.T) {
		server := newCodexE2EServer(t)
		pidFile := filepath.Join(t.TempDir(), "child-pid")
		wrapper := recordingBinary(t, pigBinary, pidFile)
		runHelper(t, node, harnessDir, wrapper, strings.ReplaceAll(`
const mod = await import("@earendil-works/pi-ai/api/openai-codex-responses");
const claims = Buffer.from(JSON.stringify({ "https://api.openai.com/auth": { chatgpt_account_id: "acct_e2e" } })).toString("base64url");
const model = { id: "gpt-5.2", name: "Codex", api: "openai-codex-responses", provider: "openai-codex", baseUrl: "SERVER", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 4096 };
const stream = mod.streamSimple(model, { messages: [{ role: "user", content: "x", timestamp: 0 }] }, { apiKey: "header." + claims + ".signature", sessionId: "sX" });
for await (const event of stream) void event;
mod.closeOpenAICodexWebSocketSessions("sX");
process.exit(0);
`, "SERVER", server.URL))
		pid := waitHarnessChildPID(t, pidFile)
		deadline := time.Now().Add(testbudget.Wait(t))
		for harnessChildAlive(pid) {
			if time.Now().After(deadline) {
				t.Fatalf("the bridge child (pid %d) outlived the helper", pid)
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
}

// recordingBinary is a PIG_HARNESS_BINARY that writes its pid to pidFile and becomes the real pig, so the test names the bridge child by the pid it started.
func recordingBinary(t *testing.T, real, pidFile string) string {
	t.Helper()
	wrapper := filepath.Join(t.TempDir(), "pig-wrapper")
	script := "#!/bin/sh\necho $$ > '" + pidFile + ".tmp' && mv '" + pidFile + ".tmp' '" + pidFile + "'\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return wrapper
}

func runHelper(t *testing.T, node, dir, binary, body string) {
	t.Helper()
	file := filepath.Join(dir, "helper-"+strings.ReplaceAll(t.Name(), "/", "-")+".mjs")
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	command := exec.CommandContext(t.Context(), node, file)
	command.Dir = dir
	command.Env = append(os.Environ(), "PIG_HARNESS_BINARY="+binary, "HOME="+scratch, "PIG_HOME="+scratch, "PI_HOME="+scratch, "PIG_CODING_AGENT_DIR="+scratch, "PI_CODING_AGENT_DIR="+scratch)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("helper: %v\n%s", err, out)
	}
}
