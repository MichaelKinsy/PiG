// Two tools whose names differ only in a lone UTF-16 surrogate. Each says which one ran.
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const units: Record<string, string> = { high: "\ud83d", low: "\ude00" };

export default function (pi: ExtensionAPI) {
  for (const [id, unit] of Object.entries(units)) {
    pi.registerTool({
      name: `N[${unit}]`,
      label: id,
      description: `D[${unit}]`,
      parameters: { type: "object", properties: {} },
      async execute() {
        return { content: [{ type: "text", text: `ran ${id}` }], details: {} };
      },
    });
  }
}
