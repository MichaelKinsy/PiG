// host-wiring-session-actions.mjs calls the Session actions Pi binds in every
// mode (agent-session.ts _bindExtensionCore) from session_start and records
// each answer, then records the model and thinking-level entries the Session
// wrote. It registers the only provider with credentials, so Pi and Pig both
// start on probe/probe-model, and switches to probe/probe-model-2. A second
// provider has no credentials, so setModel to its model answers false and
// changes nothing (agent-session.ts _bindExtensionCore setModel).
import { appendFileSync } from "node:fs";

const log = (record) => appendFileSync(process.env.WIRING_REPORT, JSON.stringify(record) + "\n");

export default function hostWiringSessionActions(pi) {
  pi.registerProvider("probe", {
    baseUrl: "http://127.0.0.1:9/v1",
    apiKey: "probe-key",
    api: "openai-completions",
    models: ["probe-model", "probe-model-2"].map((id) => ({ id, name: id, reasoning: true, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 4096 })),
  });
  pi.registerProvider("probe-noauth", {
    baseUrl: "http://127.0.0.1:9/v1",
    api: "openai-completions",
    models: [{ id: "noauth-model", name: "noauth-model", reasoning: true, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 4096 }],
  });
  pi.on("model_select", (event) => log({ event: "model_select", model: `${event.model.provider}/${event.model.id}` }));
  pi.on("thinking_level_select", (event) => log({ event: "thinking_level_select", level: event.level }));
  pi.on("session_start", async (_event, ctx) => {
    log({ event: "trusted", value: ctx.isProjectTrusted() });
    try {
      log({ event: "setModelNoAuth", value: await pi.setModel(ctx.modelRegistry.find("probe-noauth", "noauth-model")) });
    } catch (error) {
      log({ event: "setModelNoAuth", error: error.message });
    }
    try {
      log({ event: "setModel", value: await pi.setModel(ctx.modelRegistry.find("probe", "probe-model-2")) });
    } catch (error) {
      log({ event: "setModel", error: error.message });
    }
    await new Promise((resolve) => ctx.compact({
      onComplete: () => { log({ event: "compact", value: "complete" }); resolve(); },
      onError: (error) => { log({ event: "compact", error: error.message }); resolve(); },
    }));
    pi.setThinkingLevel("high");
  });
  pi.on("session_shutdown", (_event, ctx) => {
    const entries = ctx.sessionManager.getEntries()
      .filter((entry) => entry.type === "model_change" || entry.type === "thinking_level_change")
      .map((entry) => entry.type === "model_change" ? `model_change:${entry.provider}/${entry.modelId}` : `thinking_level_change:${entry.thinkingLevel}`);
    log({ event: "entries", value: entries });
  });
}
