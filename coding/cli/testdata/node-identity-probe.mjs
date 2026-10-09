// Makes the model calls a Node extension can make on its own, so a test can
// record what PiG's runtime sends. `/probe <mode> <provider> <baseUrl>`:
//   session: the vendored SDK's createAgentSession + session.prompt (Pi's
//            core/sdk.ts wiring, including mergeProviderAttributionHeaders);
//   stream:  pi-ai's completeSimple on the compat entry point.
import { createAgentSession, ModelRuntime, SessionManager, SettingsManager } from "@earendil-works/pi-coding-agent";
import { completeSimple } from "@earendil-works/pi-ai/compat";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

export default function (pi) {
  pi.registerCommand("probe", {
    description: "make one model call",
    handler: async (args) => {
      const [mode, provider, baseUrl] = args.split(" ");
      const model = { provider, id: "identity-model", name: "Identity", api: "openai-completions", baseUrl, reasoning: false, input: ["text"], contextWindow: 32768, maxTokens: 1024, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } };
      if (mode === "stream") {
        const result = await completeSimple(model, { messages: [{ role: "user", content: "hello", timestamp: Date.now() }] }, { apiKey: "probe-key", sessionId: "stream-session" });
        if (result.stopReason !== "stop") throw new Error(`stream result = ${JSON.stringify(result)}`);
        return;
      }
      const dir = mkdtempSync(join(tmpdir(), "pig-identity-"));
      const runtime = await ModelRuntime.create({ authPath: join(dir, "auth.json"), modelsPath: join(dir, "models.json"), allowModelNetwork: false });
      let sessionModel = model;
      if (runtime.getProvider(provider)) {
        runtime.setRuntimeApiKey(provider, "probe-key");
      } else {
        runtime.registerProvider(provider, { baseUrl, apiKey: "probe-key", api: "openai-completions", models: [{ id: model.id, name: model.name, reasoning: false, input: ["text"], cost: model.cost, contextWindow: model.contextWindow, maxTokens: model.maxTokens }] });
        sessionModel = runtime.getModel(provider, model.id);
      }
      const { session } = await createAgentSession({ cwd: dir, model: sessionModel, modelRuntime: runtime, sessionManager: SessionManager.inMemory(dir), settingsManager: SettingsManager.inMemory({ compaction: { enabled: false } }), tools: [] });
      try {
        await session.prompt("hello");
        const last = session.messages.at(-1);
        if (last?.stopReason !== "stop") throw new Error(`session result = ${JSON.stringify(last)}`);
      } finally {
        session.dispose();
      }
    },
  });
}
