// Drive the installed Pi LlamaView (extensions/llama/ui.ts) through showLlamaUi with a fake command context, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme, theme } = await load("dist/modes/interactive/theme/theme.js");
const { showLlamaUi } = await load("dist/extensions/llama/ui.js");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const flush = () => new Promise((resolve) => setImmediate(resolve));
const results = [];
for (const scenario of JSON.parse(input)) {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme(scenario.theme);
  const keybindings = new KeybindingsManager({});
  setKeybindings(keybindings);
  let view;
  let finish;
  const finished = new Promise((resolve) => { finish = resolve; });
  const frames = () => scenario.widths.map((width) => view.render(width));
  const record = { frames: [], settled: [] };
  const ctx = {
    ui: {
      notify() {},
      custom: (factory) => { view = factory({ requestRender() {} }, theme, keybindings, () => finish()); return finished; },
    },
  };
  const run = async (ui) => {
    await flush(); // the view exists once showLlamaUi has returned the factory's component
    record.frames.push(frames()); // the loading view before any operation
    record.settled.push(null);
    let outcome = { done: false };
    const track = (promise, shape) => { promise.then((value) => { outcome = { done: true, value: shape(value) }; }); };
    const op = scenario.op;
    if (op.kind === "models") track(ui.showModels(op.url, op.models.map((m) => ({ id: m.id, status: { value: m.status, ...(m.args ? { args: m.args } : {}) }, ...(m.meta ? { meta: m.meta } : {}) }))), (action) => ({ type: action.type, model: action.model ? action.model.id : null }));
    else if (op.kind === "select") track(ui.select(op.title, op.options), (value) => (value === undefined ? null : value));
    else if (op.kind === "confirm") track(ui.confirm(op.title, op.message), (value) => value);
    else if (op.kind === "connectionError") track(ui.connectionError(op.url, op.message), (value) => value);
    else if (op.kind === "status") ui.showStatus(op.title, op.message);
    else if (op.kind === "progress") {
      ui.updateProgress(op.states[0]); // ignored: no progress is showing yet
      track(ui.progress(op.states[0]), () => "stopped");
      for (const state of op.states.slice(1)) {
        record.frames.push(frames()); record.settled.push(null);
        ui.updateProgress(state);
      }
    }
    await flush();
    record.frames.push(frames());
    record.settled.push(outcome.done ? outcome.value : null);
    for (const key of op.keys ?? []) {
      view.handleInput(key);
      await flush();
      record.frames.push(frames());
      record.settled.push(outcome.done ? outcome.value : null);
    }
  };
  await showLlamaUi(ctx, run);
  results.push(record);
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
