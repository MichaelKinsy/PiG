// Exercises the node runtime's TypeScript source resolution and the pi-tui /
// pi-coding-agent shim exports that real upstream extensions import.
//
// The "./helper.js" specifier resolves to helper.ts: TypeScript's NodeNext
// module resolution requires relative imports of TypeScript sources to carry
// the emitted JavaScript extension. Upstream pi resolves these through jiti.
//
// HelperShape is a type imported without the `type` keyword; the runtime must
// elide it after stripping, exactly as jiti's babel transform does.
import { helperLabel, HelperShape } from "./helper.js";
import { HStack, SettingsList, Text } from "@earendil-works/pi-tui";
import { CONFIG_DIR_NAME, estimateTokens, getAgentDir, getSettingsListTheme } from "@earendil-works/pi-coding-agent";

// Extensions are loaded as ES modules, where `require` is not defined; upstream
// pi supplies CommonJS interop through jiti.
const { EOL } = require("node:os");

export default function (pi: any) {
  pi.registerCommand({
    name: "ts-resolution",
    description: "Report node runtime resolution and shim availability",
    handler: async (_args: string, ctx: any) => {
      const stack = new HStack([
        { component: new Text("left", 0, 0), basis: 10, grow: 0, shrink: 0 },
        { component: new Text("right", 0, 0), basis: 0, grow: 1, shrink: 1 },
      ]);
      const settings = new SettingsList(
        [{ id: "a", label: "alpha", currentValue: "on", values: ["on", "off"] }],
        5,
        getSettingsListTheme(),
        () => {},
        () => {},
      );
      // 20 characters: ceil(20/4) = 5 distinguishes upstream's chars/4
      // heuristic from a neighbouring divisor.
      const tokens = estimateTokens({ role: "user", content: "12345678901234567890" });
      const shape: HelperShape = { label: helperLabel() };
      await ctx.ui.notify(
        [
          shape.label,
          `requireWorks=${typeof EOL === "string"}`,
          `configDir=${CONFIG_DIR_NAME}`,
          `agentDirUnderConfigRoot=${getAgentDir().endsWith("agent")}`,
          `stackWidth=${stack.render(30)[0]?.length ?? 0}`,
          `settingsLines=${settings.render(40).length > 0}`,
          `tokens=${tokens}`,
        ].join(" "),
        "info",
      );
    },
  });
}
