// Registers CLI flags for --help: flags keep registration order rather than name order, a repeated name keeps its first position and last declaration, and a flag without a description names its extension.
export default function (pi) {
  pi.registerFlag("plan", { description: "Start in plan mode", type: "boolean", default: false });
  pi.registerFlag("apply", { description: "Apply the plan", type: "boolean" });
  pi.registerFlag("target-environment-name", { type: "string" });
  pi.registerFlag("plan", { description: "Plan again", type: "boolean" });
}
