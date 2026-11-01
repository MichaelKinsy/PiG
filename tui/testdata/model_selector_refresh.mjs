// Drive the installed Pi's ModelSelectorComponent through refreshModels, never a translated oracle.
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
// model-selector.ts refreshModels aborts the refresh after 15 seconds; the probes shorten that timer, as the Go test shortens modelSelectorRefreshTimeout.
const realSetTimeout = globalThis.setTimeout;
globalThis.setTimeout = (fn, ms, ...args) => realSetTimeout(fn, ms === 15_000 ? 20 : ms, ...args);
const wait = (ms) => new Promise((resolve) => realSetTimeout(resolve, ms));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const toModel = (m) => ({ ...m, api: "openai-responses", baseUrl: "", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 });
const results = [];
for (const test of JSON.parse(input)) {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme(test.theme);
  setKeybindings(new KeybindingsManager({}));
  let models = test.before.map(toModel);
  const find = (provider, id) => models.find((m) => m.provider === provider && m.id === id);
  const settle = () => {
    models = test.after.map(toModel);
    return { aborted: test.aborted, errors: new Map(test.errors.map((id) => [id, new Error(`${id} catalog failed`)])) };
  };
  // "settle" resolves at once; "abort" settles when the selector's timeout aborts it; "hang" never settles.
  const refresh = ({ signal }) => {
    if (test.refresh === "settle") return Promise.resolve(settle());
    if (test.refresh === "abort") return new Promise((resolve) => signal.addEventListener("abort", () => resolve(settle()), { once: true }));
    return new Promise(() => {});
  };
  const runtime = { getAvailableSnapshot: () => models, getModel: find, getError: () => test.runtimeError || undefined, refresh };
  const component = new ModelSelectorComponent(
    { requestRender() {} }, test.current ? find(...test.current.split("/")) : undefined, runtime, [],
    () => {}, () => {}, test.initialSearch || undefined, () => {}, undefined,
  );
  component.focused = true;
  const frames = [component.render(test.width)];
  for (const key of test.keysBefore ?? []) component.handleInput(key);
  frames.push(component.render(test.width));
  await wait(test.refresh === "settle" ? 5 : 80);
  frames.push(component.render(test.width));
  for (const key of test.keysAfter ?? []) {
    component.handleInput(key);
    frames.push(component.render(test.width));
  }
  component.dispose();
  results.push({ frames });
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
