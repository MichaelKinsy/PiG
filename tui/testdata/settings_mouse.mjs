// Drive the installed pi-tui SettingsList's handleMouse (rows, wheel, search input, submenu) with seeded mouse events and keys, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { SettingsList, SelectList, setKeybindings, setCapabilities } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/index.js"));
const { KeybindingsManager } = await import(pathToFileURL(root + "dist/core/keybindings.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
// Styles are SGR pairs, not printable tags: a themed row must keep its visible width, as a real theme's does.
const sgr = (open, close) => (text) => `\x1b[${open}m${text}\x1b[${close}m`;
const tag = (name) => (text) => `<${name}>${text}</${name}>`;
const theme = { label: (text, selected) => (selected ? sgr("1;36", "22;39")(text) : sgr("37", "39")(text)), value: (text, selected) => (selected ? sgr("1;33", "22;39")(text) : sgr("90", "39")(text)), description: sgr("2", "22"), cursor: "→ ", hint: sgr("3", "23") };
const selectTheme = { selectedPrefix: tag("sp"), selectedText: tag("st"), description: tag("d"), scrollInfo: tag("si"), noMatch: tag("nm") };
const results = JSON.parse(input).map((scenario) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  setKeybindings(new KeybindingsManager({}));
  const calls = [];
  const items = scenario.items.map((item) => ({
    id: item.id, label: item.label, currentValue: item.currentValue,
    ...(item.description ? { description: item.description } : {}),
    ...(item.values && item.values.length ? { values: item.values } : {}),
    ...(item.submenu ? {
      submenu: (current, done) => {
        const list = new SelectList(["one", "two", "three", "four"].map((v) => ({ value: v, label: v })), 2, selectTheme);
        list.onSelect = (selected) => { calls.push("submenu-select:" + selected.value); done(selected.value, item.navigate ? { navigateTo: item.navigate } : undefined); };
        list.onCancel = () => done();
        return list;
      },
    } : {}),
  }));
  const list = new SettingsList(items, scenario.maxVisible, theme, (id, value) => calls.push(`change:${id}=${value}`), () => calls.push("cancel"), { enableSearch: scenario.search });
  const frames = [list.render(scenario.width)];
  const outs = [];
  for (const step of scenario.steps) {
    calls.length = 0;
    let result = null;
    if (step.key !== undefined) list.handleInput(step.key);
    else {
      const r = list.handleMouse({ shift: false, alt: false, ctrl: false, screenX: step.x, screenY: step.y, width: scenario.width, height: frames[frames.length - 1].length, ...step });
      if (r) result = { handled: r.handled ?? null, capture: r.capture ?? null, focus: r.focus ?? null, render: r.render ?? null };
    }
    outs.push({ result, calls: [...calls] });
    frames.push(list.render(scenario.width));
  }
  return { frames, outs };
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
