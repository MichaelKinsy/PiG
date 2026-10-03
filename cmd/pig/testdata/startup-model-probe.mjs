// startup-model-probe.mjs registers the only provider with credentials, so a
// startup with no model selection starts on probe/probe-model, and records the
// model the Session starts on from session_start (ctx.model). Pi 0.99.1 records
// `router/auto` when --model names a virtual model an extension registered,
// because createAgentSessionServices flushes the virtual models before model
// resolution (agent-session-services.ts:182-193).
import { appendFileSync } from "node:fs";

const log = (record) => appendFileSync(process.env.WIRING_REPORT, JSON.stringify(record) + "\n");

export default function startupModelProbe(pi) {
  pi.registerProvider("probe", {
    baseUrl: "http://127.0.0.1:9/v1",
    apiKey: "probe-key",
    api: "openai-completions",
    models: ["probe-model", "probe-model-2"].map((id) => ({ id, name: id, reasoning: true, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 4096 })),
  });
  pi.on("session_start", (_event, ctx) => {
    log({ event: "start_model", value: ctx.model ? `${ctx.model.provider}/${ctx.model.id}` : null });
  });
}
