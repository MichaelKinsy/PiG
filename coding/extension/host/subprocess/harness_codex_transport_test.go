//go:build !pig_strip_node_extensions && !windows

package subprocess

// pi: packages/ai/src/api/openai-codex-responses.ts:313 (the transport defaults to "auto" only to choose the WebSocket path) and :1539 (`useCachedContext = options?.transport === "websocket-cached" || options?.transport === "auto"`)

import (
	"encoding/json"
	"slices"
	"testing"
)

// codexTransportScenario is the helper both sides run: for each transport value (unset, auto, websocket-cached, websocket) two turns of one session against the one server, then what the server received (the request frames,
// text for text), the session's stats as a reader of them sees them, and the server's counts.
const codexTransportScenario = `
const base = process.env.CODEX_SERVER;
const mod = await import("@earendil-works/pi-ai/api/openai-codex-responses");
const { closeOpenAICodexWebSocketSessions: close, getOpenAICodexWebSocketDebugStats: get } = mod;
const claims = Buffer.from(JSON.stringify({ "https://api.openai.com/auth": { chatgpt_account_id: "acct_e2e" } })).toString("base64url");
const apiKey = "header." + claims + ".signature";
const model = { id: "gpt-5.2", name: "Codex", api: "openai-codex-responses", provider: "openai-codex", baseUrl: base, reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 4096 };
const describe = (stats) => {
  if (stats === undefined) return "undefined";
  const members = {};
  for (const key of Object.keys(stats)) {
    const value = stats[key];
    members[key] = value === undefined ? "<undefined>" : key === "lastWebSocketError" ? [typeof value, "<text>"] : [typeof value, value];
  }
  return { names: Object.keys(stats), members };
};
const counts = async () => (await fetch(base + "/_counts")).json();
const frames = async () => (await fetch(base + "/_frames")).json();
const log = [];
for (const name of ["unset", "auto", "websocket-cached", "websocket"]) {
  const sessionId = "transport-" + name;
  const options = { apiKey, sessionId };
  if (name !== "unset") options.transport = name;
  const one = { role: "user", content: "one", timestamp: 1 };
  const first = await mod.streamSimple(model, { messages: [one] }, options).result();
  const second = await mod.streamSimple(model, { messages: [one, first, { role: "user", content: "two", timestamp: 2 }] }, options).result();
  log.push({ transport: name, stops: [first.stopReason, second.stopReason], frames: await frames(), stats: describe(get(sessionId)), counts: await counts() });
  close(sessionId);
}
process.stdout.write(JSON.stringify(log), () => process.exit(0));
`

// compareCodexTransportObservations compares what Pi and the harness peer logged per transport value, one member at a time: the stop reasons, each request frame byte for byte, the session's stats and the server's counts.
func compareCodexTransportObservations(t *testing.T, pi, pig []json.RawMessage) {
	t.Helper()
	if len(pi) != 4 || len(pig) != 4 {
		t.Fatalf("Pi logged %d transports, PiG %d, want 4 each", len(pi), len(pig))
	}
	type observation struct {
		Transport string          `json:"transport"`
		Stops     []string        `json:"stops"`
		Frames    []string        `json:"frames"`
		Stats     json.RawMessage `json:"stats"`
		Counts    json.RawMessage `json:"counts"`
	}
	for i := range pi {
		var a, b observation
		if err := json.Unmarshal(pi[i], &a); err != nil {
			t.Fatalf("Pi observation %d: %v", i, err)
		}
		if err := json.Unmarshal(pig[i], &b); err != nil {
			t.Fatalf("PiG observation %d: %v", i, err)
		}
		if a.Transport != b.Transport || len(a.Frames) != 2 || len(b.Frames) != 2 {
			t.Errorf("transport %q: Pi sent %d frames, PiG sent %d for %q, want 2 each", a.Transport, len(a.Frames), len(b.Frames), b.Transport)
			continue
		}
		if !slices.Equal(a.Stops, b.Stops) {
			t.Errorf("transport %q: stop reasons differ\n Pi : %v\n PiG: %v", a.Transport, a.Stops, b.Stops)
		}
		for n := range a.Frames {
			if a.Frames[n] != b.Frames[n] {
				t.Errorf("transport %q: request %d differs\n Pi : %s\n PiG: %s", a.Transport, n+1, a.Frames[n], b.Frames[n])
			}
		}
		if string(a.Stats) != string(b.Stats) {
			t.Errorf("transport %q: stats differ\n Pi : %s\n PiG: %s", a.Transport, a.Stats, b.Stats)
		}
		if string(a.Counts) != string(b.Counts) {
			t.Errorf("transport %q: server counts differ\n Pi : %s\n PiG: %s", a.Transport, a.Counts, b.Counts)
		}
	}
}
