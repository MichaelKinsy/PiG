// Drive the installed pi-tui Editor with keys typed without letting them settle (one event-loop turn apart, with pauses where a probe has them) and a provider that records every query it receives, optionally slow; log the queries, the text and the rows once everything settled. Never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
// Time is a manual clock: Pi's editor arms its debounce with setTimeout, and the probe's providers wait with it too. A timer runs only when advance() passes it, in order of its time, with every promise job settled between
// timers, so no probe depends on how fast the machine is. Real setImmediate is the checkpoint after a key (the end of the terminal read), where promise jobs run but no timer does.
const realSetImmediate = globalThis.setImmediate;
const timers = new Map();
let clock = 0;
let timerSeq = 0;
globalThis.setTimeout = (callback, ms = 0, ...args) => {
  const timer = { id: ++timerSeq, at: clock + Math.max(0, Number(ms) || 0), callback, args };
  timers.set(timer.id, timer);
  return timer;
};
globalThis.clearTimeout = (timer) => { if (timer) timers.delete(timer.id); };
const settle = async () => { for (let i = 0; i < 3; i++) await new Promise((resolve) => realSetImmediate(resolve)); };
const advance = async (ms) => {
  const end = clock + ms;
  for (;;) {
    await settle();
    let next;
    for (const timer of timers.values()) if (timer.at <= end && (!next || timer.at < next.at || (timer.at === next.at && timer.id < next.id))) next = timer;
    if (!next) break;
    timers.delete(next.id);
    clock = next.at;
    next.callback(...next.args);
  }
  clock = end;
  await settle();
};
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { Editor, setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme, getEditorTheme } = await load("dist/modes/interactive/theme/theme.js");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const SLOW_MS = 40;
// probe.slow: 0 answers at once, 1 answers after SLOW_MS and stops early when the request is aborted, 2 answers after SLOW_MS whatever happens to the request.
const pause = (ms, signal) => new Promise((resolve) => {
  const timer = setTimeout(resolve, ms);
  signal?.addEventListener("abort", () => { clearTimeout(timer); resolve(); });
});
const commands = ["load-skills", "list", "help"];
const skills = ["skill-a", "skill-b", "éskill-a"];
const tokens = ["alpha", "alpine", "beta", "alé", "al/dir", "résumé"];
const matching = (names, prefix) => names.filter((name) => name.startsWith(prefix)).map((name) => ({ value: name, label: name }));
const answer = (before) => {
  let match, items;
  if ((match = /^\/(\S*)$/.exec(before))) {
    items = matching(commands, match[1]);
    return items.length > 0 ? { items, prefix: before } : null;
  }
  if ((match = /^\/load-skills (\S*)$/.exec(before))) {
    items = matching(skills, match[1]);
    return items.length > 0 ? { items, prefix: match[1] } : null;
  }
  if ((match = /(?:^|[\s([{<`])([@#$])(\S*)$/.exec(before))) {
    items = matching(tokens, match[2]);
    return items.length > 0 ? { items, prefix: match[1] + match[2] } : null;
  }
  return null;
};
setCapabilities({ images: null, trueColor: true, hyperlinks: false });
initTheme("dark");
setKeybindings(new KeybindingsManager({}));
const run = async (probe) => {
  timers.clear();
  const queries = [];
  // aborted[i] is whether the signal of the request that asked queries[i] was aborted when the provider finished.
  const aborted = [];
  const provider = {
    triggerCharacters: ["$"],
    getSuggestions: async (lines, cursorLine, cursorCol, options) => {
      const before = lines[cursorLine].slice(0, cursorCol);
      const index = queries.push(before) - 1;
      aborted[index] = false;
      if (probe.slow === 1) await pause(SLOW_MS, options.signal);
      else if (probe.slow === 2) await pause(SLOW_MS);
      aborted[index] = options.signal.aborted;
      return answer(before);
    },
    applyCompletion: (lines, cursorLine, cursorCol) => ({ lines, cursorLine, cursorCol }),
  };
  const tui = { requestRender() {}, terminal: { rows: 16, columns: 80 } };
  const editor = new Editor(tui, getEditorTheme(), { paddingX: 0, autocompleteMaxVisible: 5 });
  editor.focused = true;
  editor.setAutocompleteProvider(provider);
  for (const op of probe.ops) {
    if (op.sleep) await advance(op.sleep);
    else {
      for (const keys of op.keys) editor.handleInput(keys);
      await settle();
    }
  }
  await advance(200);
  return { queries, aborted, text: editor.getText(), cursor: editor.getCursor(), rows: editor.render(40) };
};
const results = [];
for (const probe of JSON.parse(input)) results.push(await run(probe));
process.stdout.write(JSON.stringify(results), () => process.exit(0));
