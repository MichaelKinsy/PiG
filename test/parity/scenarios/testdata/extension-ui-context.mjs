import { writeFileSync } from "node:fs";

// Pi 0.87.1 core/extensions/runner.ts:320-351 is the complete no-op UI.
export async function probeNoUI(ctx) {
  const ui = ctx.ui;
  const calls = [];
  const factory = () => { calls.push("factory"); throw new Error("headless factory invoked"); };
  const result = { hasUI: ctx.hasUI };
  const observe = async (name, fn) => {
    try { const value = await fn(); result[name] = value === undefined ? "undefined" : value; }
    catch (err) { result[name] = `error:${err.message}`; }
  };
  await observe("select", () => ui.select("Choose", ["A", "B"]));
  await observe("confirm", () => ui.confirm("Confirm", "Continue?"));
  await observe("input", () => ui.input("Input", "placeholder"));
  await observe("editor", () => ui.editor("Editor", "prefill"));
  await observe("custom", () => ui.custom(factory));
  for (const [name, args] of [
    ["notify", ["HEADLESS_NOTIFICATION_MUST_NOT_APPEAR", "info"]],
    ["setStatus", ["probe", "status"]], ["setWorkingMessage", ["working"]],
    ["setWorkingVisible", [false]], ["setWorkingIndicator", [{ frames: ["x"] }]],
    ["setHiddenThinkingLabel", ["hidden"]], ["setWidget", ["probe", factory]],
    ["setFooter", [factory]], ["setHeader", [factory]], ["setTitle", ["title"]],
    ["pasteToEditor", ["paste"]], ["setEditorText", ["text"]],
    ["addAutocompleteProvider", [factory]], ["setEditorComponent", [factory]],
    ["setToolsExpanded", [true]],
  ]) await observe(name, () => ui[name](...args));
  await observe("onTerminalInput", () => { const off = ui.onTerminalInput(factory); off(); off(); });
  await observe("getEditorText", () => ui.getEditorText());
  await observe("getEditorComponent", () => ui.getEditorComponent());
  await observe("getAllThemes", () => ui.getAllThemes());
  await observe("getTheme", () => ui.getTheme("dark"));
  await observe("getThemeWithoutName", () => ui.getTheme());
  await observe("setTheme", () => ui.setTheme("dark"));
  await observe("getToolsExpanded", () => ui.getToolsExpanded());
  result.themeAvailable = typeof ui.theme.fg === "function";
  result.factories = calls;
  return result;
}

export default function (pi) {
  pi.registerCommand("ui-probe", {
    handler: async (path, ctx) => {
      if (ctx.mode === "rpc") {
        ctx.ui.notify(`hasUI:${ctx.hasUI}`, "info");
        const selected = await ctx.ui.select("Choose", ["A", "B"]);
        ctx.ui.notify(`selected:${selected}`, "info");
      } else {
        writeFileSync(path, JSON.stringify(await probeNoUI(ctx), null, 2) + "\n");
      }
    },
  });
}
