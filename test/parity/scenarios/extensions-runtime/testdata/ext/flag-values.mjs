import { writeFileSync } from "node:fs";

export default function (pi) {
  pi.registerFlag("capture", { type: "string" });
  pi.registerFlag("configured", { type: "string", default: "factory-default" });
  pi.registerFlag("enabled", { type: "boolean", default: false });
  const record = () => writeFileSync(pi.getFlag("capture"), JSON.stringify([pi.getFlag("configured"), pi.getFlag("enabled")]) + "\n");
  pi.on("session_start", record);
  pi.registerCommand("flags", { handler: async () => record() });
}
