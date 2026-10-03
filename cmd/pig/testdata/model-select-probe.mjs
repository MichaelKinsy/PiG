// model-select-probe.mjs registers two models of the only provider with
// credentials and records every model_select event with its model,
// previousModel and source, as Pi's AgentSession emits them once per changed
// selection (agent-session.ts:2372-2384 _emitModelSelect, called from
// setModel :2411 with source "set" and cycleModel :2480,2512 with "cycle").
import { appendFileSync } from "node:fs";

const log = (record) => appendFileSync(process.env.WIRING_REPORT, JSON.stringify(record) + "\n");
const name = (model) => (model ? `${model.provider}/${model.id}` : null);

export default function modelSelectProbe(pi) {
  pi.registerProvider("probe", {
    baseUrl: "http://127.0.0.1:9/v1",
    apiKey: "probe-key",
    api: "openai-completions",
    models: ["probe-model", "probe-model-2"].map((id) => ({ id, name: id, reasoning: true, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 4096 })),
  });
  pi.on("model_select", (event) => log({ event: "model_select", model: name(event.model), previousModel: name(event.previousModel), source: event.source }));
}
