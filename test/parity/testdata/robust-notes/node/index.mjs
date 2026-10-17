// robust-notes: one realistic extension, written once per SDK (node, go, python, rust).
// Every SDK version must draw the same screen as this one does in Pi.
import { split } from "robust-words";

export default function (pi) {
  let toolRuns = 0;
  pi.registerFlag("robust-prefix", { description: "Prefix for robust_words output", type: "string", default: "rw" });
  pi.registerTool({
    name: "robust_words",
    label: "Robust words",
    description: "Split a command line into words.",
    parameters: { type: "object", properties: { line: { type: "string" } }, required: ["line"] },
    async execute(_id, params) {
      const words = split(params.line);
      return { content: [{ type: "text", text: `${pi.getFlag("robust-prefix")}: ${words.join("|")}` }], details: { count: words.length } };
    },
  });
  pi.on("session_start", (_event, ctx) => {
    ctx.ui.setStatus("robust", "robust: ready");
  });
  pi.on("tool_result", (event, ctx) => {
    if (event.toolName !== "robust_words") return;
    toolRuns++;
    ctx.ui.setStatus("robust", `robust: tools=${toolRuns}`);
  });
  pi.registerCommand("robust-settings", {
    description: "Show the robust-notes settings",
    handler: async (_args, ctx) => {
      const settings = pi.getSettings();
      ctx.ui.notify(`robust settings: greeting=${settings.robustNotes?.greeting ?? "none"} prefix=${pi.getFlag("robust-prefix")}`, "info");
    },
  });
  pi.registerCommand("robust-note", {
    description: "Save a note",
    handler: async (args, ctx) => {
      pi.appendEntry("robust-note", { text: args });
      const notes = ctx.sessionManager.getEntries().filter((entry) => entry.type === "custom" && entry.customType === "robust-note");
      ctx.ui.notify(`robust note ${notes.length}: ${args}`, "info");
    },
  });
  pi.registerCommand("robust-pick", {
    description: "Pick an item in an overlay",
    handler: async (_args, ctx) => {
      const items = ["alpha", "beta", "gamma"];
      const choice = await ctx.ui.custom((tui, _theme, _keybindings, done) => {
        let selected = 0;
        return {
          render(width) {
            return [`robust pick (${width})`, ...items.map((item, index) => `${index === selected ? ">" : " "} ${item}`)];
          },
          invalidate() {},
          handleInput(data) {
            if (data === "j") {
              selected = (selected + 1) % items.length;
              tui.requestRender();
            } else if (data === "s") {
              done(items[selected]);
            } else if (data === "q") {
              done(undefined);
            }
          },
        };
      }, { overlay: true });
      ctx.ui.notify(`robust picked: ${choice ?? "nothing"}`, "info");
    },
  });
}
