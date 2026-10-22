package extensionconformance

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// The Node row: a Node extension calls Pi's own createAgentSession (served by the runtime's independent-session shim) with the sentinel scenarios of the other SDKs' fixtures, and must produce the golden those rows assert. The Node child builds its model runtime itself, as a Node extension does in Pi; the host's does not matter to it.
const childSessionNode = `
import { createAgentSession, DefaultResourceLoader, ModelRuntime, SessionManager, SettingsManager, createExtensionRuntime } from "@earendil-works/pi-coding-agent";
import { createAssistantMessageEventStream } from "@earendil-works/pi-ai";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const usage = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } };
const text = m => typeof m?.content === "string" ? m.content : (m?.content ?? []).filter(c => c.type === "text").map(c => c.text).join("");
const modelConfig = { reasoning: false, input: ["text"], contextWindow: 32768, maxTokens: 1024, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } };

function sentinel(model, context) {
  let prompt = "", toolResult = "";
  for (const message of context.messages) {
    if (message.role === "user") prompt = text(message);
    if (message.role === "toolResult") toolResult = text(message);
  }
  const partial = (content, stopReason = "stop") => ({ role: "assistant", content, api: model.api, provider: model.provider, model: model.id, usage, stopReason, timestamp: Date.now() });
  const out = createAssistantMessageEventStream();
  queueMicrotask(() => {
    if (prompt.startsWith("stream:") && toolResult === "") {
      out.push({ type: "start", partial: partial([]) });
      out.push({ type: "text_start", contentIndex: 0, partial: partial([{ type: "text", text: "" }]) });
      let all = "";
      for (let i = 0; i < Number(prompt.slice(7)); i++) { const delta = "chunk-" + String(i).padStart(3, "0") + " "; all += delta; out.push({ type: "text_delta", contentIndex: 0, delta, partial: partial([{ type: "text", text: all }]) }); }
      out.push({ type: "text_end", contentIndex: 0, content: all, partial: partial([{ type: "text", text: all }]) });
      out.push({ type: "done", reason: "stop", message: partial([{ type: "text", text: all }]) });
      return;
    }
    let message;
    if (toolResult !== "") message = partial([{ type: "text", text: "sentinel-final:" + toolResult }]);
    else if (prompt.startsWith("tool:")) message = partial([{ type: "toolCall", id: "call-1", name: "sentinel_tool", arguments: { x: prompt.slice(5) } }], "toolUse");
    else message = partial([{ type: "text", text: "sentinel-model:" + model.id + ":" + prompt }]);
    out.push({ type: "done", reason: message.stopReason, message });
  });
  return out;
}

async function world(body, withSentinel = true) {
  const dir = mkdtempSync(join(tmpdir(), "pig-child-node-row-"));
  try {
    const runtime = await ModelRuntime.create({ authPath: join(dir, "auth.json"), modelsPath: join(dir, "models.json"), allowModelNetwork: false });
    if (withSentinel) runtime.registerProvider("sentinel", { api: "openai-completions", baseUrl: "https://sentinel.invalid", apiKey: "sentinel-key", models: [{ id: "echo", name: "Echo", ...modelConfig }], streamSimple: sentinel });
    runtime.registerProvider("nokey", { api: "openai-completions", baseUrl: "https://nokey.invalid", models: [{ id: "locked", name: "Locked", ...modelConfig }], streamSimple: () => { throw new Error("never streamed"); } });
    const loader = appendix => ({
      getExtensions: () => ({ extensions: [], errors: [], runtime: createExtensionRuntime() }), getSkills: () => ({ skills: [], diagnostics: [] }), getPrompts: () => ({ prompts: [], diagnostics: [] }), getThemes: () => ({ themes: [], diagnostics: [] }),
      getAgentsFiles: () => ({ agentsFiles: [] }), getSystemPrompt: () => undefined, getSystemPromptSource: () => undefined, getAppendSystemPrompt: () => appendix, getAppendSystemPromptSources: () => [], extendResources() {}, async reload() {},
    });
    const create = options => createAgentSession({ cwd: dir, modelRuntime: runtime, sessionManager: SessionManager.inMemory(dir), settingsManager: SettingsManager.inMemory({ compaction: { enabled: false } }), resourceLoader: loader([]), ...options });
    return await body({ runtime, create, loader });
  } finally { rmSync(dir, { recursive: true, force: true }); }
}

const sentinelTool = { name: "sentinel_tool", label: "sentinel", description: "uppercases x", parameters: { type: "object", properties: { x: { type: "string" } }, required: ["x"] }, execute: async (_id, params) => ({ content: [{ type: "text", text: params.x.toUpperCase() + "!" }], details: {} }) };
const reply = value => ({ content: [{ type: "text", text: JSON.stringify(value) }], details: {} });
const object = { type: "object" };

export default function (pi) {
  pi.registerTool({ name: "child_run", label: "child_run", description: "child_run", parameters: object, execute: async (_id, args) => reply(await world(async ({ runtime, create, loader }) => {
    const { session } = await create({ model: runtime.getModel("sentinel", "echo"), resourceLoader: loader(["Child appendix"]), tools: ["sentinel_tool"], customTools: [sentinelTool] });
    try {
      const events = [];
      let updates = 0, streamed = "", partial = "";
      session.subscribe(e => {
        events.push(e.type + (e.type.startsWith("message_") ? ":" + e.message.role : ""));
        if (e.type === "message_update") { updates++; streamed = text(e.message); partial = text(e.assistantMessageEvent.partial); }
      });
      await session.prompt(args.prompt);
      const first = session.messages[0];
      return {
        events, updates, streamed, partial, roles: session.messages.map(m => m.role), final: session.getLastAssistantText() ?? "", model: session.model?.id, activeTools: session.getActiveToolNames(), file: session.sessionFile ?? "", appendix: session.systemPrompt.includes("Child appendix"),
        system: { keys: Object.keys(first).sort(), role: first.role, content: first.content, sections: Object.keys(first.sections).sort(), toolsAdded: first.toolsAdded },
      };
    } finally { session.dispose(); }
  })) });

  pi.registerTool({ name: "child_lifecycle", label: "child_lifecycle", description: "child_lifecycle", parameters: object, execute: async () => reply(await world(async ({ runtime, create }) => {
    let started, sawAbort = false;
    const toolStarted = new Promise(resolve => { started = resolve; });
    const hang = { ...sentinelTool, description: "waits for its abort", execute: (_id, _params, signal) => new Promise((_resolve, reject) => { started(); signal.addEventListener("abort", () => { sawAbort = true; reject(new Error("aborted")); }); }) };
    const { session } = await create({ model: runtime.getModel("sentinel", "echo"), tools: ["sentinel_tool"], customTools: [hang] });
    try {
      const events = [];
      session.subscribe(e => events.push(e.type + (e.type === "agent_settled" ? ":aborted=" + e.aborted : "")));
      const running = session.prompt("tool:hang");
      await toolStarted;
      let concurrentPrompt = "resolved";
      try { await session.prompt("again"); } catch (e) { concurrentPrompt = e.message; }
      await session.abort();
      await running;
      const result = { concurrentPrompt, toolSawAbort: sawAbort, idleAfterAbort: !session.isStreaming, tail: events.slice(-4), lastRole: session.messages.at(-1).role };
      session.dispose();
      result.afterDispose = "resolved";
      try { await session.prompt("after dispose"); } catch (e) { result.afterDispose = e.message; }
      return result;
    } finally { session.dispose(); }
  })) });

  pi.registerTool({ name: "child_events", label: "child_events", description: "child_events", parameters: object, execute: async (_id, args) => reply(await world(async ({ runtime, create }) => {
    const settingsManager = SettingsManager.inMemory({ compaction: { enabled: false } });
    const resourceLoader = new DefaultResourceLoader({ cwd: args.cwd, agentDir: args.agentDir, settingsManager, noExtensions: true, noSkills: true, noPromptTemplates: true, noThemes: true, noContextFiles: true, additionalExtensionPaths: [args.probe] });
    await resourceLoader.reload();
    const { session } = await create({ model: runtime.getModel("sentinel", "echo"), settingsManager, resourceLoader, scopedModels: [{ model: runtime.getModel("sentinel", "echo"), thinkingLevel: "high" }, { model: runtime.getModel("nokey", "locked"), thinkingLevel: "low" }] });
    try {
      await session.bindExtensions({});
      await session.prompt("stream:3");
      return "done";
    } finally { session.dispose(); }
  })) });

  pi.registerTool({ name: "child_errors", label: "child_errors", description: "child_errors", parameters: object, execute: async () => reply(await world(async ({ runtime, create }) => {
    // The docs paths are the installation's: the message keeps its first line and the names of the documents it points at.
    const norm = message => message == null ? null : { first: message.split("\n")[0], docs: message.split("\n").filter(line => line.trim().endsWith(".md")).map(line => line.trim().split(/[\\/]/).at(-1)) };
    const out = {};
    for (const [name, options] of [["no-key", { model: runtime.getModel("nokey", "locked") }], ["no-model", {}]]) {
      const { session, modelFallbackMessage } = await create(options);
      try {
        let error = null;
        try { await session.prompt("hi"); } catch (e) { error = e.message; }
        out[name] = { fallback: norm(modelFallbackMessage), model: session.model?.id ?? null, promptError: norm(error) };
      } finally { session.dispose(); }
    }
    return out;
  }, false)) });
}
`

func loadChildNode(t *testing.T, host *subprocess.Host, isolation string) extension.Extension {
	t.Helper()
	path := filepath.Join(t.TempDir(), "child-session.mjs")
	if err := os.WriteFile(path, []byte(childSessionNode), 0o600); err != nil {
		t.Fatal(err)
	}
	configs := []subprocess.ExtConfig{{Name: "child-session", Source: path, Enabled: true, Isolation: isolation}}
	if isolation == "shared-ok" {
		peer := filepath.Join(t.TempDir(), "child-session-peer.mjs")
		if err := os.WriteFile(peer, []byte("export default function () {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		configs = append(configs, subprocess.ExtConfig{Name: "child-session-peer", Source: peer, Enabled: true, Isolation: isolation})
	}
	if isolation == "shared-ok" {
		// Packed: both extensions share one Node process.
		host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })
		loaded, err := host.Reload(t.Context())
		if err != nil || len(loaded) != len(configs) {
			t.Fatalf("reload: %v (%+v)", err, host.LastReloadReport())
		}
		if report := host.LastReloadReport(); report == nil || len(report.Cells) != 1 || report.Cells[0].Strategy != subprocess.CellStrategy("packed-node") {
			t.Fatalf("expected one packed node cell: %+v", report)
		}
		return loaded[0]
	}
	loaded, failures := host.LoadAll(t.Context(), configs)
	if len(failures) != 0 || len(loaded) != len(configs) {
		t.Fatalf("load: %v", failures)
	}
	return loaded[0]
}

func TestChildAgentSessionNode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (starts Node subprocesses)")
	}
	for _, isolation := range []string{"strict", "shared-ok"} {
		t.Run(isolation, func(t *testing.T) {
			host, services := newChildSessionHostWith(t, false, nil)
			ext := loadChildNode(t, host, isolation)
			checkChildSession(t, services.CWD(), ext)
			if got := runChildTool(t, ext, "child_lifecycle", `{}`); !reflect.DeepEqual(got, childSessionWant()["lifecycle"]) {
				t.Fatalf("lifecycle = %v, want Pi's %v", got, childSessionWant()["lifecycle"])
			}
			if got := runChildTool(t, ext, "child_errors", `{}`); !reflect.DeepEqual(got, childSessionWant()["model-errors"]) {
				t.Fatalf("model errors = %v, want Pi's %v", got, childSessionWant()["model-errors"])
			}
		})
	}
}
