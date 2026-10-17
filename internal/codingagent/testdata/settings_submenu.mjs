// Drive the installed Pi's SelectSubmenu and SteppedSubmenu, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const { SelectSubmenu, SteppedSubmenu } = await load("dist/modes/interactive/components/settings-submenu.js");
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const items = (n, tag) => Array.from({ length: n }, (_, i) => ({ value: `${tag}${i}`, label: `Option ${tag} ${i} ${["alpha", "beta", "gamma"][i % 3]}`, ...(i % 2 ? { description: `desc ${i % 3 ? "x" : "y"} ${i}` } : {}) }));
const results = JSON.parse(input).map((test) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme(test.theme);
  setKeybindings(new KeybindingsManager(test.bindings ?? {}));
  const events = [];
  let component;
  if (test.kind === "select") {
    component = new SelectSubmenu("Pick one", test.description, items(test.count, "v"), test.current,
      (value) => events.push("select:" + value), () => events.push("cancel"), (value) => events.push("change:" + value),
      test.searchable ? { searchable: true } : undefined);
  } else {
    const steps = [0, 1, 2].map((i) => ({
      key: "k" + i,
      title: (ctx) => `Title ${i} ${Object.keys(ctx).length}`,
      description: (ctx) => `Desc ${i} ${JSON.stringify(ctx)}`,
      options: (ctx) => items(test.count + i, "s" + i + Object.keys(ctx).length + "_"),
      preselect: (ctx) => (test.preselect ? `s${i}${Object.keys(ctx).length}_1` : undefined),
      searchable: test.searchableStep === i,
    }));
    const used = steps.slice(0, test.stepCount);
    component = new SteppedSubmenu(used, (ctx) => events.push("complete:" + JSON.stringify(ctx)), () => events.push("cancel"),
      { startAtStep: test.start, initialContext: test.initial ?? undefined, loop: test.loop });
  }
  const steps = [events.slice()], frames = [component.render(test.width)];
  for (const key of test.keys) {
    component.handleInput(key);
    steps.push(events.slice());
    frames.push(component.render(test.width));
  }
  return { steps, frames };
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
