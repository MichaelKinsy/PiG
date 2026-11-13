// Tools whose name, description, parameter name, parameter description, enum value and default each hold a lone UTF-16 surrogate: a high one,
// a low one, and a low one before a high one.
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const units: Record<string, string> = { high: "\ud83d", low: "\ude00", reversed: "\ude00\ud83d" };

export default function (pi: ExtensionAPI) {
  for (const [id, unit] of Object.entries(units)) {
    pi.registerTool({
      name: `N_${id}[${unit}]`,
      label: id,
      description: `D[${unit}]`,
      parameters: {
        type: "object",
        properties: { [`K[${unit}]`]: { type: "string", description: `PD[${unit}]`, enum: [`E[${unit}]`], default: `DF[${unit}]` } },
        required: [`K[${unit}]`],
      },
      async execute() {
        return { content: [{ type: "text", text: `ran ${id}` }], details: {} };
      },
    });
  }
}
