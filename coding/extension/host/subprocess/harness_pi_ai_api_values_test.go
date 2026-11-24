//go:build !pig_strip_node_extensions

package subprocess

// pi: packages/ai/test/openai-completions-tool-result-images.test.ts
// pi: packages/ai/test/google-shared-convert-tools.test.ts
// pi: packages/ai/test/google-shared-retry.test.ts

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The values the stand-ins re-export from Pi's own modules (api/openai-completions convertMessages, api/pi-messages PiMessagesResponseError, every api/google-shared value),
// run in a helper that resolves `@earendil-works/pi-ai/api/<name>` the way Pi's layout does (the pinned package) and in the one PiG's harness peer serves, on the inputs of Pi's own
// tests of them; each row's JSON must be identical.
const apiValueRows = `
const out = {};
const row = async (name, fn) => { try { out[name] = await fn(); } catch (e) { out[name] = "threw: " + String(e && e.message).split("\n")[0]; } };
const completions = await import("@earendil-works/pi-ai/api/openai-completions");
const piMessages = await import("@earendil-works/pi-ai/api/pi-messages");
const google = await import("@earendil-works/pi-ai/api/google-shared");
const { normalizeContext } = await import("@earendil-works/pi-ai/compat");

const usage = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } };
const compat = { supportsStore: true, supportsDeveloperRole: true, supportsReasoningEffort: true, supportsUsageInStreaming: true, supportsFinishReason: true, maxTokensField: "max_completion_tokens", requiresToolResultName: false, requiresAssistantAfterToolResult: false, requiresNonNullAssistantContent: false, requiresToolsWithToolHistory: false, requiresThinkingAsText: false, requiresReasoningContentOnAssistantMessages: false, thinkingFormat: "openai", openRouterRouting: {}, vercelGatewayRouting: {}, chatTemplateKwargs: {}, chatTemplateArgs: {}, zaiToolStream: false, supportsThinkingTokenBudget: false, thinkingTokenBudgetField: undefined, supportsStrictMode: true, supportsOpenAIGrammarTools: false, supportsMidConvoSystemMessages: false, supportsMidConvoToolAdditions: false, cacheControlFormat: "anthropic", sendSessionAffinityHeaders: false, sessionAffinityFormat: "openai", supportsLongCacheRetention: true };
const openaiModel = { id: "gpt-4o-mini", name: "GPT-4o mini", api: "openai-completions", provider: "openai", baseUrl: "https://api.openai.com/v1", reasoning: false, input: ["text", "image"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 16384 };
const toolResult = (id, timestamp) => ({ role: "toolResult", toolCallId: id, toolName: "read", content: [{ type: "text", text: "Read image file [image/png]" }, { type: "image", data: "ZmFrZQ==", mimeType: "image/png" }], isError: false, timestamp });

await row("convertMessages omits empty text parts from user messages with images", () => completions.convertMessages(openaiModel, normalizeContext({ messages: [{ role: "user", content: [{ type: "text", text: "" }, { type: "image", data: "ZmFrZQ==", mimeType: "image/png" }], timestamp: 1 }] }), compat));
await row("convertMessages batches tool-result images after consecutive tool results", () => completions.convertMessages(openaiModel, normalizeContext({ messages: [
  { role: "user", content: "Read the images", timestamp: 1 },
  { role: "assistant", content: [{ type: "toolCall", id: "tool-1", name: "read", arguments: { path: "img-1.png" } }, { type: "toolCall", id: "tool-2", name: "read", arguments: { path: "img-2.png" } }], api: "openai-completions", provider: "openai", model: "gpt-4o-mini", usage, stopReason: "toolUse", timestamp: 2 },
  toolResult("tool-1", 3), toolResult("tool-2", 4)] }), compat));

await row("PiMessagesResponseError fields", () => {
  const e = new piMessages.PiMessagesResponseError("Pi messages failed", "rate_limited", { status: 429, body: { a: [1, 2] } });
  return { name: e.name, message: e.message, code: e.code, details: e.diagnosticDetails, isError: e instanceof Error, isClass: e instanceof piMessages.PiMessagesResponseError, own: Object.keys(e), string: String(e), proto: Object.getPrototypeOf(piMessages.PiMessagesResponseError) === Error, arity: piMessages.PiMessagesResponseError.length, stack: String(e.stack).split("\n")[0] };
});
await row("PiMessagesResponseError without a code", () => { const e = new piMessages.PiMessagesResponseError("m", undefined, {}); return { code: e.code ?? null, own: Object.keys(e) }; });

const tool = (parameters) => ({ name: "test_tool", description: "A test tool", parameters });
await row("convertTools strips JSON Schema meta keys", () => google.convertTools([tool({ $schema: "http://json-schema.org/draft-07/schema#", $id: "urn:bash-tool", $comment: "A bash tool", $defs: { commandDef: { type: "string" } }, definitions: { legacyDef: { type: "number" } }, type: "object", properties: { command: { type: "string" } }, required: ["command"] })], true));
await row("convertTools without parametersJsonSchema", () => google.convertTools([tool({ type: "object", properties: { a: { const: "x" } } })], false));
const each = (fn, inputs) => inputs.map((x) => { try { return fn(x); } catch (e) { return "threw: " + String(e.message).split("\n")[0]; } });
await row("mapStopReason", () => each((r) => google.mapStopReason(r), ["STOP", "MAX_TOKENS", "CONTINUATION", "SAFETY", "FINISH_REASON_UNSPECIFIED", "MALFORMED_FUNCTION_CALL", "BOGUS"]));
await row("mapToolChoice", () => each((c) => google.mapToolChoice(c), ["auto", "none", "any", "other"]));
await row("thinking levels", () => ({ levels: ["minimal", "low", "medium", "high"].map((l) => google.toGoogleThinkingLevel(l)), sdk: ["THINKING_LEVEL_UNSPECIFIED", "MINIMAL", "LOW", "MEDIUM", "HIGH"].map((l) => google.toGoogleSdkThinkingLevel(l)) }));
await row("model id helpers", () => ({ byId: ["gemini-3-pro-preview", "gemini-2.5-flash", "gemma-3-27b"].map((id) => [google.usesGoogleThinkingLevel({ id }), google.requiresToolCallId(id), google.supportsGoogleStrictToolSampling(id), google.supportsGoogleStrictToolSampling({ id })]), strictOverride: google.supportsGoogleStrictToolSampling({ id: "gemini-3-pro-preview", compat: { supportsStrictMode: false } }) }));
const geminiModel = { id: "gemini-3-pro-preview", name: "Gemini 3", api: "google-generative-ai", provider: "google", baseUrl: "https://generativelanguage.googleapis.com", reasoning: true, input: ["text", "image"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000000, maxTokens: 64000 };
await row("convertMessages (google-shared) with a tool call and an image tool result", () => google.convertMessages(geminiModel, normalizeContext({ messages: [
  { role: "user", content: [{ type: "text", text: "Read it" }, { type: "image", data: "ZmFrZQ==", mimeType: "image/png" }], timestamp: 1 },
  { role: "assistant", content: [{ type: "thinking", thinking: "plan", thinkingSignature: "c2ln" }, { type: "toolCall", id: "call_1", name: "read", arguments: { path: "a.png" }, thoughtSignature: "dHNpZw==" }], api: "google-generative-ai", provider: "google", model: "gemini-3-pro-preview", usage, stopReason: "toolUse", timestamp: 2 },
  toolResult("call_1", 3)] })));
await row("retryGoogleRequest retries a headers-less SDK error with a retryable status", async () => {
  let calls = 0;
  const apiError = (status) => Object.assign(new Error("got status: " + status), { status });
  const result = await google.retryGoogleRequest(async () => { calls++; if (calls === 1) throw apiError(429); return "ok"; }, { maxRetries: 1 });
  return { result, calls };
});
await row("retryGoogleRequest does not retry without maxRetries or on a non-retryable status", async () => {
  const seen = [];
  for (const [status, options] of [[429, undefined], [400, { maxRetries: 2 }]]) {
    let calls = 0; const error = Object.assign(new Error("got status: " + status), { status });
    try { await google.retryGoogleRequest(async () => { calls++; throw error; }, options); } catch (e) { seen.push({ same: e === error, calls }); }
  }
  return seen;
});
process.stdout.write(JSON.stringify(out));
`

func TestRestoredAPIValuesBehaveAsUnderPi(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required: %v", err)
	}
	agent := filepath.Join(findModuleRoot(t), "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent")
	aiRoot, err := filepath.EvalSymlinks(filepath.Join(agent, "node_modules", "@earendil-works", "pi-ai"))
	if err != nil {
		t.Fatal(err)
	}
	run := func(dir string) map[string]json.RawMessage {
		file := filepath.Join(dir, "api-values.mjs")
		if err := os.WriteFile(file, []byte(apiValueRows), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.CommandContext(t.Context(), node, file).CombinedOutput()
		if err != nil {
			t.Fatalf("helper in %s: %v\n%s", dir, err, out)
		}
		var rows map[string]json.RawMessage
		if err := json.Unmarshal(out, &rows); err != nil {
			t.Fatalf("helper in %s printed %q: %v", dir, out, err)
		}
		return rows
	}
	piDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(piDir, "node_modules", "@earendil-works"), 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.RequireDirectoryLink(t, aiRoot, filepath.Join(piDir, "node_modules", "@earendil-works", "pi-ai"))
	runtime := t.TempDir()
	if err := extractNodeRuntime(runtime); err != nil {
		t.Fatal(err)
	}
	pi, pig := run(piDir), run(filepath.Join(runtime, "harness"))
	if len(pi) != 13 {
		t.Fatalf("Pi produced %d rows, want the 13 the test declares", len(pi))
	}
	for name, want := range pi {
		if strings.HasPrefix(string(want), `"threw`) {
			t.Errorf("%s: Pi's own module fails the row: %s", name, want)
		}
		if got := string(pig[name]); got != string(want) {
			t.Errorf("%s differs\n Pi : %s\n PiG: %s", name, want, got)
		}
	}
}
