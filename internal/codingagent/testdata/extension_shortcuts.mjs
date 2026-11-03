// Run the installed Pi's InteractiveMode.setupExtensionShortcuts (interactive-mode.ts) over a recording `this`, then drive the defaultEditor.onExtensionShortcut it installs
// with raw terminal input, never a translated oracle. Handlers record themselves and then resolve or reject; the stub's showError records what the shortcut handler's
// rejection reports. Every input is followed by a drain of the microtask queue, so the asynchronous error report is attributed to the input that caused it.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "0";
const load = (path) => import(pathToFileURL(root + path));
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
initTheme("dark");
const { InteractiveMode } = await load("dist/modes/interactive/interactive-mode.js");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const flush = () => new Promise((resolve) => setImmediate(resolve));
const results = [];
for (const probe of JSON.parse(input)) {
  const called = [];
  const errors = [];
  const contexts = new Set();
  const shortcuts = new Map(probe.shortcuts.map(({ key, behavior }) => [key, {
    shortcut: key,
    extensionPath: "/ext/" + key,
    handler: async (ctx) => {
      called.push(key);
      contexts.add(ctx);
      if (behavior.kind === "reject") throw new Error(behavior.message);
      if (behavior.kind === "rejectValue") throw behavior.message;
    },
  }]));
  const target = {
    keybindings: { getEffectiveConfig: () => ({}) },
    defaultEditor: {},
    createExtensionUIContext: () => ({}),
    sessionManager: { getCwd: () => "/cwd" },
    session: { model: undefined, scopedModels: [], thinkingLevel: "off", isIdle: true, agent: { signal: undefined }, pendingMessageCount: 0 },
    settingsManager: { isProjectTrusted: () => true },
    showError: (message) => errors.push(message),
  };
  InteractiveMode.prototype.setupExtensionShortcuts.call(target, { getShortcuts: () => shortcuts, getModelRegistry: () => ({}) });
  const installed = typeof target.defaultEditor.onExtensionShortcut === "function";
  const steps = [];
  if (installed) {
    for (const data of probe.inputs) {
      called.length = 0;
      errors.length = 0;
      const consumed = target.defaultEditor.onExtensionShortcut(data);
      await flush();
      steps.push({ consumed, called: [...called], errors: [...errors] });
    }
  }
  results.push({ installed, steps, freshContexts: contexts.size });
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
