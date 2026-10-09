// Run the installed Pi's setupEditorSubmitHandler (interactive-mode.ts) over a recording `this`, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "0";
const load = (path) => import(pathToFileURL(root + path));
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
initTheme("dark");
const { InteractiveMode } = await load("dist/modes/interactive/interactive-mode.js");
const { AgentSession } = await load("dist/core/agent-session.js");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const { texts, extensionNames, extensionTexts } = JSON.parse(input);
const results = [];
for (const text of texts) {
  const calls = [];
  const pendingUserInputs = [];
  const target = {
    session: { isBashRunning: false, isCompacting: false, isStreaming: false },
    defaultEditor: {},
    editor: { setText() {}, addToHistory() {} },
    pendingUserInputs,
    isExtensionCommand: () => false,
    flushPendingBashComponents() {},
    updateEditorBorderColor() {},
    isBashMode: false,
  };
  const self = new Proxy(target, {
    get(t, prop) {
      if (prop in t) return t[prop];
      return (...args) => { calls.push([String(prop), ...args]); };
    },
    set(t, prop, value) { t[prop] = value; return true; },
  });
  InteractiveMode.prototype.setupEditorSubmitHandler.call(self);
  await target.defaultEditor.onSubmit(text);
  if (pendingUserInputs.length > 0) calls.push(["prompt", pendingUserInputs[0]]);
  results.push(calls);
}
const extension = extensionTexts.map((text) => {
  let handled;
  const runner = { getCommand: (name) => (extensionNames.includes(name) ? { handler: async (args) => { handled = args; } } : undefined), createCommandContext: () => ({}), emitError() {} };
  const interactive = { session: { extensionRunner: runner } };
  return { isExtensionCommand: InteractiveMode.prototype.isExtensionCommand.call(interactive, text), run: AgentSession.prototype._tryExecuteExtensionCommand.call({ _extensionRunner: runner }, text).then((ran) => ({ ran, args: handled })) };
});
const extensionResults = [];
for (const item of extension) { const { ran, args } = await item.run; extensionResults.push({ isExtensionCommand: item.isExtensionCommand, ran, args: args ?? null }); }
process.stdout.write(JSON.stringify({ results, extensionResults }), () => process.exit(0));
