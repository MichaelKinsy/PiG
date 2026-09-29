import { writeFileSync } from "node:fs";
export default function (pi) {
  pi.registerFlag("scope-output", { type: "string", description: "Scope recording path" });
  const rows = [];
  const record = (phase, ctx) => {
    rows.push([phase, ctx.scopedModels.map(item => [item.model.provider, item.model.id, item.thinkingLevel ?? null])]);
    writeFileSync(pi.getFlag("scope-output"), JSON.stringify(rows));
  };
  pi.on("session_start", (_event, ctx) => record("start", ctx));
  pi.registerCommand("scope", { handler: async (_args, ctx) => record("command", ctx) });
}
