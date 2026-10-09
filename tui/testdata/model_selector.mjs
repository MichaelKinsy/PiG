// Drive the installed Pi's ModelSelectorComponent, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const { ModelSelectorComponent } = await load("dist/modes/interactive/components/model-selector.js");
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const test of JSON.parse(input)) {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme(test.theme);
  setKeybindings(new KeybindingsManager(test.bindings ?? {}));
  const models = test.all.map((m) => ({ ...m, api: "openai-responses", baseUrl: "", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 }));
  const find = (provider, id) => models.find((m) => m.provider === provider && m.id === id);
  // The catalog refresh never settles, so the status stays "Refreshing model catalogs…" as Pig's picker shows it.
  const runtime = { getAvailableSnapshot: () => models, getModel: find, getError: () => undefined, refresh: () => new Promise(() => {}) };
  const events = [];
  const component = new ModelSelectorComponent(
    { requestRender() {} }, test.current ? find(...test.current.split("/")) : undefined, runtime,
    (test.scoped ?? []).map((s) => ({ model: find(...s.split("/")) })),
    (model) => events.push(`select:${model.provider}/${model.id}`),
    () => events.push("cancel"),
    test.initialSearch || undefined,
    test.saveCallback ? (model) => events.push(`default:${model.provider}/${model.id}`) : undefined,
    test.defaultModel ? { provider: test.defaultModel.split("/")[0], id: test.defaultModel.split("/")[1] } : undefined,
  );
  component.focused = true;
  const steps = [events.slice()], frames = [component.render(test.width)];
  for (const key of test.keys) {
    component.handleInput(key);
    steps.push(events.slice());
    frames.push(component.render(test.width));
  }
  component.dispose();
  results.push({ steps, frames });
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
