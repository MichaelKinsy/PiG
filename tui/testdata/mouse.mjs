// Drive the installed pi-tui components' handleMouse (SelectList, Input, MouseRegion) with seeded mouse events, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { SelectList, Input, MouseRegion, Container, Box, Text, Spacer, setCapabilities } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/index.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
setCapabilities({ images: null, trueColor: true, hyperlinks: false });
const theme = { selectedPrefix: (t) => `<sp>${t}</sp>`, selectedText: (t) => `<st>${t}</st>`, description: (t) => `<d>${t}</d>`, scrollInfo: (t) => `<si>${t}</si>`, noMatch: (t) => `<nm>${t}</nm>` };
const results = JSON.parse(input).map((scenario) => {
  const calls = [];
  const makeList = () => {
    const list = new SelectList(scenario.items.map((i) => ({ value: i.value, label: i.label, ...(i.description ? { description: i.description } : {}) })), scenario.maxVisible, theme);
    list.onSelect = (item) => calls.push("select:" + item.value);
    list.onSelectionChange = (item) => calls.push("change:" + item.value);
    if (scenario.filter) list.setFilter(scenario.filter);
    for (let i = 0; i < scenario.preselect; i++) list.handleInput("\x1b[B");
    calls.length = 0;
    return list;
  };
  let component;
  let childList;
  let kids = [];
  const buildKids = () => scenario.children.map((child) => {
    if (child.kind === "text") return new Text(child.text, 0, 0);
    if (child.kind === "spacer") return new Spacer(child.n);
    if (child.kind === "input") { const input = new Input(); input.focused = true; input.setValue(child.text ?? ""); return input; }
    const list = new SelectList((child.items ?? []).map((i) => ({ value: i.value, label: i.label })), child.maxVisible, theme);
    list.onSelect = (item) => calls.push("select:" + item.value);
    list.onSelectionChange = (item) => calls.push("change:" + item.value);
    return list;
  });
  if (scenario.kind === "selectlist") component = makeList();
  else if (scenario.kind === "region") {
    const fallback = { none: () => undefined, handled: () => ({ handled: true }), focus: () => ({ focus: true }), capture: () => ({ capture: true }), norender: () => ({ handled: true, render: false }), render: () => ({ handled: true, render: true }) }[scenario.fallback];
    childList = makeList();
    component = new MouseRegion(childList, (event) => { calls.push(`fallback:${event.type}`); return fallback(event); });
  } else if (scenario.kind === "container" || scenario.kind === "box") {
    kids = buildKids();
    component = scenario.kind === "box" ? new Box(scenario.paddingX, scenario.paddingY) : new Container();
    for (const kid of kids) component.addChild(kid);
  } else {
    component = new Input();
    component.focused = true;
    component.setValue(scenario.value);
  }
  const frames = [scenario.renderFirst === false && (scenario.kind === "container" || scenario.kind === "box") ? [] : component.render(scenario.width)];
  const outs = [];
  for (const event of scenario.events) {
    calls.length = 0;
    if (event.type === "filter") {
      // Reshape a child's list without rendering: the container's recorded layout goes stale, as it does between a state change and the next frame.
      const kid = kids[event.child];
      if (kid && kid.setFilter) kid.setFilter(event.filter ?? "");
      outs.push({ result: null, calls: [] });
      frames.push(frames[frames.length - 1]);
      continue;
    }
    const result = component.handleMouse({ shift: false, alt: false, ctrl: false, ...event });
    let normalized = null;
    if (result) {
      normalized = { handled: result.handled ?? null, capture: result.capture ?? null, focus: result.focus ?? null, render: result.render ?? null };
      if (scenario.kind === "container" || scenario.kind === "box") {
        const label = (c) => (c === undefined ? null : c === component ? "self" : kids.includes(c) ? `child:${kids.indexOf(c)}` : "other");
        normalized.target = result.target ? { component: label(result.target.component), originX: result.target.originX, originY: result.target.originY, width: result.target.width, height: result.target.height, focusTarget: result.focusTarget ? label(result.focusTarget) : null } : null;
      }
      if (scenario.kind === "region") normalized.target = result.target ? { originX: result.target.originX, originY: result.target.originY, width: result.target.width, height: result.target.height, focusTarget: result.focusTarget ? (result.focusTarget === childList ? "child" : result.focusTarget === component ? "self" : "other") : null } : null;
    }
    outs.push({ result: normalized, calls: [...calls] });
    frames.push(component.render(scenario.width));
  }
  return { frames, outs };
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
