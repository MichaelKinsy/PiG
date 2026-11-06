// Drive the installed pi-tui Editor with keys typed without letting them settle (one event-loop turn apart, with pauses where a probe has them) and a provider that records every query it receives, optionally slow; log the queries, the text and the rows once everything settled. Never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
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
const turn = () => new Promise((resolve) => setImmediate(resolve));
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
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
// A probe whose timing the machine disturbed (a pause that ran long, keys that took longer than the debounce) is run again: the log is only comparable when the keys were as close as the probe says.
const attempt = async (probe) => {
  const queries = [];
  const provider = {
    triggerCharacters: ["$"],
    getSuggestions: async (lines, cursorLine, cursorCol, options) => {
      queries.push(lines[cursorLine].slice(0, cursorCol));
      if (probe.slow === 1) await pause(SLOW_MS, options.signal);
      else if (probe.slow === 2) await pause(SLOW_MS);
      return answer(lines[cursorLine].slice(0, cursorCol));
    },
    applyCompletion: (lines, cursorLine, cursorCol) => ({ lines, cursorLine, cursorCol }),
  };
  const tui = { requestRender() {}, terminal: { rows: 16, columns: 80 } };
  const editor = new Editor(tui, getEditorTheme(), { paddingX: 0, autocompleteMaxVisible: 5 });
  editor.focused = true;
  editor.setAutocompleteProvider(provider);
  let disturbed = false;
  for (const op of probe.ops) {
    const start = performance.now();
    if (op.sleep) {
      await sleep(op.sleep);
      if (performance.now() - start > op.sleep + 6) disturbed = true;
    } else {
      for (const keys of op.keys) editor.handleInput(keys);
      await turn();
      if (performance.now() - start > 6) disturbed = true;
    }
  }
  await sleep(200);
  for (let i = 0; i < 4; i++) await turn();
  return { disturbed, log: { queries, text: editor.getText(), cursor: editor.getCursor(), rows: editor.render(40) } };
};
const results = [];
for (const probe of JSON.parse(input)) {
  let result;
  for (let tries = 0; tries < 100; tries++) {
    result = await attempt(probe);
    if (!result.disturbed) break;
  }
  if (result.disturbed) throw new Error("could not run a probe undisturbed: " + JSON.stringify(probe));
  results.push(result.log);
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
