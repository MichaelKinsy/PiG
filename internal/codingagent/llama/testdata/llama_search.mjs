// Drive the installed Pi LlamaView.searchModels (HuggingFaceSearch in extensions/llama/ui.ts) on a virtual clock, never a translated oracle.
// setTimeout and clearTimeout are replaced by a scheduler that only runs a timer when the scenario advances the clock to it, so the 500 ms debounce
// and the fake search's delay take no real time and cannot be disturbed by load.
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
setCapabilities({ images: null, trueColor: true, hyperlinks: false });
initTheme("dark");
const keybindings = new KeybindingsManager({});
setKeybindings(keybindings);
const flush = () => new Promise((resolve) => setImmediate(resolve));
// The virtual clock: timers fire in due-time order, ties in creation order.
let now = 0;
let sequence = 0;
let timers = [];
const realSetTimeout = globalThis.setTimeout;
globalThis.setTimeout = (callback, delay = 0) => {
  const timer = { due: now + Math.max(0, delay), sequence: sequence++, callback };
  timers.push(timer);
  return timer;
};
globalThis.clearTimeout = (timer) => { timers = timers.filter((candidate) => candidate !== timer); };
const advance = async (ms) => {
  const target = now + ms;
  for (;;) {
    await flush();
    const due = timers.filter((timer) => timer.due <= target).sort((a, b) => a.due - b.due || a.sequence - b.sequence)[0];
    if (!due) break;
    timers = timers.filter((timer) => timer !== due);
    now = due.due;
    due.callback();
  }
  now = target;
  await flush();
};
const sleep = (ms) => new Promise((resolve) => globalThis.setTimeout(resolve, ms));
const runScenario = async (scenario) => {
  now = 0; sequence = 0; timers = [];
  const calls = [];
  const search = async (query, signal) => {
    // A search stops waiting when it is aborted, as a request that honors its signal does.
    const aborted = new Promise((resolve) => signal.addEventListener("abort", resolve, { once: true }));
    await Promise.race([sleep(scenario.delay), aborted]);
    calls.push(`${query}|aborted=${signal.aborted}`);
    if (query.includes("err")) throw new Error("search failed: " + query);
    return scenario.catalog.filter((model) => query.length > 2 || model.id.toLowerCase().includes(query.toLowerCase().slice(0, 1))).slice(0, scenario.limit);
  };
  let view;
  let finish;
  const finished = new Promise((resolve) => { finish = resolve; });
  const record = { frames: [], settled: [], calls };
  const ctx = { ui: { notify() {}, custom: (factory) => { view = factory({ requestRender() {} }, theme, keybindings, () => finish()); return finished; } } };
  await showLlamaUi(ctx, async (ui) => {
    await flush();
    view.focused = true; // the host focuses the custom component, which turns on the input cursor
    let outcome = { done: false };
    ui.searchModels(search).then((value) => { outcome = { done: true, value: value === undefined ? null : value }; });
    const snap = async () => {
      await flush();
      record.frames.push(scenario.widths.map((width) => view.render(width)));
      record.settled.push(outcome.done ? outcome.value : null);
    };
    await snap();
    for (const op of scenario.ops) {
      if (op.wait) { await advance(op.wait); await snap(); continue; }
      if (outcome.done) { await snap(); continue; } // keys after the search closed are not sent: Pi would start a search nobody waits for
      view.handleInput(op.key);
      await snap();
    }
    if (!outcome.done) { view.handleInput("\x1b"); await flush(); } // close, so no timer outlives the scenario
  });
  return record;
};
const results = [];
for (const scenario of JSON.parse(input)) results.push(await runScenario(scenario));
void realSetTimeout;
process.stdout.write(JSON.stringify(results), () => process.exit(0));
