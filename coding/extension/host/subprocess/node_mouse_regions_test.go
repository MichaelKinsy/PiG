package subprocess

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
)

// mouseRegions (runtime-node/mouse.mjs) finds the cells where Pi's dispatchMouseEvent reaches a MouseRegion of a transcript component, as rectangles, in
// one walk over the tree. The reference below is the per-cell probe it replaced: Pi's own dispatchMouseEvent asked about every cell, with each region's
// handler swapped for a recorder. The two must name the same cells for every tree: generated Containers, Boxes with padding, MouseRegions (nested
// and bare), wrappers that pass the mouse to nobody, layouts rendered at another width, and layouts that changed after the render.
const mouseRegionsOracleScript = `
import assert from "node:assert/strict";
import {createRequire} from "node:module";
import {pathToFileURL} from "node:url";
const {mouseRegions} = await import(pathToFileURL(%q));
const require = createRequire(%q);
const tui = require("./shims/pi-dist/pi-tui/sdk-bundle/index.js");
const {Container, dispatchMouseEvent} = require("./shims/pi-dist/pi-tui/tui.js");

// The per-cell probe: the cells at which a click reaches a MouseRegion, found through Pi's own dispatch.
function probeCells(component, width, height) {
  const regions = [];
  const collect = (c, depth) => {
    if (!c || typeof c !== "object" || depth > 64) return;
    if (typeof c.handleMouse === "function" && c.handleMouse === tui.MouseRegion.prototype.handleMouse) regions.push(c);
    if (c.child) collect(c.child, depth + 1);
    if (Array.isArray(c.children)) for (const child of c.children) collect(child, depth + 1);
  };
  collect(component, 0);
  const saved = regions.map((region) => region.onMouse);
  let reached = false;
  for (const region of regions) region.onMouse = () => { reached = true; return {handled: true}; };
  const cells = new Set();
  try {
    for (let y = 0; y < height; y++) for (let x = 0; x < width; x++) {
      reached = false;
      dispatchMouseEvent(component, {type: "click", button: "left", x, y, screenX: x, screenY: y, width, height, shift: false, alt: false, ctrl: false, clickCount: 1});
      if (reached) cells.add(x + "," + y);
    }
  } finally {
    regions.forEach((region, i) => { region.onMouse = saved[i]; });
  }
  return cells;
}
function rectCells(rects, width, height) {
  const cells = new Set();
  for (const r of rects) {
    assert.ok(r.width > 0 && r.height > 0, "an empty rectangle: " + JSON.stringify(r));
    assert.ok(r.x >= 0 && r.y >= 0 && r.x + r.width <= width && r.y + r.height <= height, "a rectangle outside the component: " + JSON.stringify(r));
    for (let y = r.y; y < r.y + r.height; y++) for (let x = r.x; x < r.x + r.width; x++) cells.add(x + "," + y);
  }
  return cells;
}
const same = (a, b) => a.size === b.size && [...a].every((cell) => b.has(cell));

const leaf = (lines) => ({render: () => Array.from({length: lines}, (_, i) => "row" + i), invalidate() {}});
const region = (child) => new tui.MouseRegion(child, () => ({handled: true}));
const container = (...children) => { const c = new Container(); children.forEach((child) => c.addChild(child)); return c; };
const box = (px, py, ...children) => { const b = new tui.Box(px, py); children.forEach((child) => b.addChild(child)); return b; };
// A component that passes the mouse to nobody, however it holds its child.
const wrapper = (child) => ({child, render: (w) => child.render(w), invalidate() {}});

function check(name, component, width, expected) {
  const lines = component.render(width);
  const rects = mouseRegions(component, width, lines.length);
  assert.ok(Array.isArray(rects), name + ": no rectangles");
  const got = rectCells(rects, width, lines.length);
  const want = probeCells(component, width, lines.length);
  assert.ok(same(got, want), name + ": rectangles " + JSON.stringify(rects) + " differ from the probe's cells (" + got.size + " vs " + want.size + ")");
  if (expected) assert.deepEqual(rects, expected, name);
  return rects;
}

// Hand-checked layouts.
check("a card in a padded box", box(2, 1, region(leaf(3))), 20, [{x: 2, y: 1, width: 16, height: 3}]);
check("a region over a container", region(container(leaf(2), leaf(0), leaf(5))), 11, [{x: 0, y: 0, width: 11, height: 7}]);
check("a region inside a region is the outer one", container(region(container(region(leaf(1)), leaf(2)))), 8, [{x: 0, y: 0, width: 8, height: 3}]);
check("regions among plain rows", container(leaf(2), region(leaf(1)), leaf(1), region(leaf(2))), 9, [{x: 0, y: 2, width: 9, height: 1}, {x: 0, y: 4, width: 9, height: 2}]);
check("a box too narrow for its padding", box(3, 0, region(leaf(2))), 4, [{x: 3, y: 0, width: 1, height: 2}]);
check("a region under a component that passes the mouse to nobody", container(wrapper(region(leaf(2))), leaf(1)), 7, []);
check("no region", container(leaf(3), box(1, 1, leaf(2))), 12, []);

// A tree with a component that handles the mouse itself is the host's whole (undefined).
{
  const own = {render: () => ["x"], invalidate() {}, handleMouse: () => ({handled: true})};
  assert.equal(mouseRegions(container(region(leaf(1)), own), 5, 2), undefined);
  assert.equal(mouseRegions(box(1, 1, own), 5, 3), undefined);
  assert.equal(mouseRegions(wrapper(own), 5, 1), undefined, "a hand-written handler below a component that passes the mouse on");
}

// Generated trees.
let seed = 0x9e3779b9;
const random = () => { seed = (seed + 0x6d2b79f5) | 0; let t = Math.imul(seed ^ (seed >>> 15), 1 | seed); t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t; return ((t ^ (t >>> 14)) >>> 0) / 4294967296; };
const pick = (n) => Math.floor(random() * n);
function generate(depth) {
  const kind = depth <= 0 ? 0 : pick(7);
  switch (kind) {
    case 0: return leaf(pick(4));
    case 1: case 2: return container(...Array.from({length: pick(5)}, () => generate(depth - 1)));
    case 3: return box(pick(3), pick(3), ...Array.from({length: pick(4)}, () => generate(depth - 1)));
    case 4: case 5: return region(generate(depth - 1));
    default: return wrapper(generate(depth - 1));
  }
}
let withRegions = 0;
for (let i = 0; i < 600; i++) {
  const component = generate(4);
  const width = 1 + pick(24);
  const lines = component.render(width);
  const rects = mouseRegions(component, width, lines.length);
  const got = rectCells(rects, width, lines.length);
  const want = probeCells(component, width, lines.length);
  assert.ok(same(got, want), "tree " + i + " at width " + width + ": " + JSON.stringify(rects) + " vs probe " + [...want].join(" "));
  if (want.size > 0) withRegions++;
  // A layout rendered at another width is stale for this one: Pi's dispatch renders the children again, and so does the walk.
  component.render(width + 1 + pick(5));
  const staleRects = mouseRegions(component, width, lines.length);
  assert.ok(same(rectCells(staleRects, width, lines.length), probeCells(component, width, lines.length)), "tree " + i + " rendered at another width");
}
assert.ok(withRegions > 100, "the generated trees hold too few regions to mean anything: " + withRegions);

// A container whose children changed after the render still hits by the layout the render left, as Pi's dispatch does.
{
  const first = region(leaf(2));
  const root = container(first, leaf(1));
  root.render(10);
  root.addChild(region(leaf(2)));
  const rects = mouseRegions(root, 10, 3);
  assert.ok(same(rectCells(rects, 10, 3), probeCells(root, 10, 3)));
  assert.deepEqual(rects, [{x: 0, y: 0, width: 10, height: 2}]);
}

// A container passes an event down only inside the height its parent's layout gave it, however many rows its own layout holds: the card below grew
// after the outer render and its own layout re-rendered, and the rows past the height the outer layout kept belong to the plain rows below it.
{
  const grow = {rows: 1, render() { return Array.from({length: this.rows}, () => "r"); }, invalidate() {}};
  const inner = container(region(leaf(1)), region(grow));
  const outer = container(inner, leaf(3));
  outer.render(10);
  grow.rows = 3;
  inner.render(10);
  const rects = mouseRegions(outer, 10, 5);
  assert.ok(same(rectCells(rects, 10, 5), probeCells(outer, 10, 5)));
  assert.deepEqual(rects, [{x: 0, y: 0, width: 10, height: 1}, {x: 0, y: 1, width: 10, height: 1}]);
}

// The walk asks nothing of the extension and does no probing: no MouseRegion handler is read or replaced, and a layout the render left is not rendered
// again, however many lines the card has.
{
  const lines = 4000;
  let renders = 0, touches = 0;
  const rows = Array.from({length: lines}, () => ({render: () => { renders++; return ["row"]; }, invalidate() {}}));
  const card = region(container(...rows));
  for (const r of [card]) Object.defineProperty(r, "onMouse", {get() { touches++; return () => undefined; }, set() { touches++; }});
  const root = box(1, 1, card);
  root.render(100);
  const rendered = renders;
  const started = performance.now();
  const rects = mouseRegions(root, 100, lines + 2);
  const elapsed = performance.now() - started;
  assert.deepEqual(rects, [{x: 1, y: 1, width: 98, height: lines}]);
  assert.equal(renders, rendered, "the walk rendered a layout that was current");
  assert.equal(touches, 0, "the walk touched a MouseRegion handler");
  // The per-cell probe took seconds here; the walk is milliseconds. The bound only catches a return to it.
  assert.ok(elapsed < 1000, "a " + lines + "-line card took " + elapsed + " ms");
}
`

func TestNodeMouseRegionsAreTheCellsPiDispatchReaches(t *testing.T) {
	nodeCellRequireNode(t)
	mouse, err := filepath.Abs("runtime-node/mouse.mjs")
	if err != nil {
		t.Fatal(err)
	}
	runtimes, err := filepath.Abs("runtime-node/runtime-modules.mjs")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", fmt.Sprintf(mouseRegionsOracleScript, mouse, runtimes))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
}
