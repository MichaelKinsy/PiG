// Drive the installed pi-tui renderLayoutFrame over component trees (stacks, scroll views, text) and scroll operations, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const tui = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/index.js"));
const { HStack, VStack, ScrollView, Text, setCapabilities } = tui;
const { renderLayoutFrame } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/layout.js"));
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((test) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  const scrolls = [];
  let calls = [];
  let nextRows = 0;
  const build = (spec) => {
    if (spec.t === "text") return new Text(spec.text ?? "", 0, 0);
    if (spec.t === "rows") { const id = nextRows++; return { render: (width) => { calls.push([id, width]); return spec.rows ?? []; }, invalidate() {} }; }
    if (spec.t === "scroll") {
      const view = new ScrollView(build(spec.child), { follow: spec.follow, primary: spec.primary, scrollbar: spec.scrollbar });
      scrolls.push(view);
      return view;
    }
    const children = (spec.children ?? []).map((child) => {
      const entry = { component: build(child.spec) };
      for (const key of ["basis", "grow", "shrink", "minSize", "maxSize"]) if (child[key] !== null && child[key] !== undefined) entry[key] = child[key];
      return entry;
    });
    const options = { align: spec.align || undefined };
    if (spec.gap !== null) options.gap = spec.gap;
    return new (spec.t === "h" ? HStack : VStack)(children, options);
  };
  const component = build(test.tree);
  const summarize = (box) => ({ rect: box.rect, clip: box.clip, lineOffset: box.lineOffset ?? 0, layer: box.layer, hasLines: box.lines !== undefined, children: box.children.map(summarize) });
  const frameOf = () => {
    calls = [];
    const frame = renderLayoutFrame(component, test.width, test.height, () => {});
    return { calls, lines: frame.lines, root: summarize(frame.root), scrolls: scrolls.map((view) => [view.scrollTop, view.isFollowingEnd]), primary: frame.primaryScrollView ? scrolls.indexOf(frame.primaryScrollView) : -1 };
  };
  const frames = [frameOf()];
  for (const op of test.ops ?? []) {
    const view = scrolls[op.view];
    if (view) {
      if (op.kind === "to") view.scrollTo(op.n);
      else if (op.kind === "by") view.scrollBy(op.n);
      else if (op.kind === "end") view.scrollToEnd();
      else if (op.kind === "start") view.scrollToStart();
    }
    frames.push(frameOf());
  }
  return frames;
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
