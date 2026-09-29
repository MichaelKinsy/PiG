package subprocess_test

import "testing"

// ScrollView is a local component, not a terminal owner (packages/tui/src/components/scroll-view.ts:41-236).
func TestPiScrollViewMatchesThePinnedPackage(t *testing.T) {
	runPinnedComparison(t, []string{"node_modules", "@earendil-works", "pi-tui", "dist", "index.js"}, "pi-tui.mjs", `
import assert from "node:assert/strict";
const [pi, pig] = await Promise.all([import(process.argv[1]), import(process.argv[2])]);
function scene(m) {
  const out = [];
  const child = new m.Text("one\n\x1b[31m界 two\x1b[39m\nthree", 0, 0);
  for (const follow of ["none", "end"]) {
    for (const scrollbar of ["hidden", "auto", "always"]) {
      const view = new m.ScrollView(child, { follow, scrollbar, overscroll: "contain", primary: true });
      let invalidations = 0;
      const snapshot = () => out.push([view.scrollTop, view.viewportHeight, view.isFollowingEnd,
        view.isScrollbarVisible, view.isScrollbarActive, invalidations, view.render(12), view.render(1)]);
      assert.ok(view instanceof m.Container);
      assert.equal(view[Symbol.for("@earendil-works/pi-tui/layout-node")]().component, child);
      assert.equal(typeof view.handleMouse, "function"); // gentle-pi binds the inherited method.
      for (const [height, viewport] of [[0, 0], [1, 1], [3, 2], [100000, 12]]) {
        view.updateLayout(height, viewport, () => invalidations++);
        snapshot();
        view.setScrollbarActive(true);
        for (const delta of [-100001, -1.9, 0, 1.9, 100001, NaN, Infinity]) {
          out.push(view.scrollBy(delta)); snapshot();
        }
        view.scrollToEnd(); snapshot();
        view.scrollTo(view.scrollTop, { disableFollow: true }); snapshot();
        view.updateLayout(height + 2, viewport, () => invalidations++); snapshot();
        view.scrollToStart(); snapshot();
        for (const top of [-1, 1.5, NaN, Infinity, 100001]) { view.scrollTo(top); snapshot(); }
      }
      view.setScrollbar("hidden");
      view.setScrollbarActive(false);
      assert.throws(() => view.addChild(child), /ScrollView has exactly one child/);
      assert.throws(() => view.removeChild(child), /ScrollView child cannot be removed/);
      assert.throws(() => view.clear(), /ScrollView child cannot be cleared/);
    }
  }
  assert.throws(() => new m.ScrollView(child, { axis: "horizontal" }), /Unsupported ScrollView axis: horizontal/);
  return out;
}
const want = scene(pi);
assert.deepEqual(scene(pig), want);
`)
}

func TestPiTuiPublicExportsAreRealImplementations(t *testing.T) {
	runPinnedComparison(t, []string{"node_modules", "@earendil-works", "pi-tui", "dist", "index.js"}, "pi-tui.mjs", `
import assert from "node:assert/strict";
const [pi, pig] = await Promise.all([import(process.argv[1]), import(process.argv[2])]);
function scene(m) {
  m.resetCapabilitiesCache();
  const detected = m.detectCapabilities();
  m.setCapabilityOverrides({ hyperlinks: false });
  m.setCapabilities({ images: null, trueColor: true, hyperlinks: true });
  const caps = m.getCapabilities();
  m.setCellDimensions({ widthPx: 9, heightPx: 18 });
  const dimensions = m.getCellDimensions();
  const image = new m.Image("AAAA", "image/png", { fallbackColor: s => "<" + s + ">" }, {}, { widthPx: 20, heightPx: 40 });
  const out = [detected, caps, dimensions, image.render(20), m.renderImage("AAAA", { widthPx: 20, heightPx: 40 })];
  m.setCapabilities({ images: "kitty", trueColor: true, hyperlinks: false });
  out.push(m.renderImage("AAAA", { widthPx: 20, heightPx: 40 }, { imageId: 42 }));
  const terminal = { columns: 20, rows: 8, kittyProtocolActive: false, write() {}, hideCursor() {}, showCursor() {},
    start() {}, stop() {}, moveBy() {}, clearLine() {}, clearFromCursor() {}, clearScreen() {}, setTitle() {} };
  for (const Screen of [m.TuiMainScreen, m.TuiAltScreen]) {
    const screen = new Screen(terminal);
    screen.addChild(new m.Text("independent screen", 0, 0));
    out.push(screen.mode, screen.render(20));
    screen.stop();
  }
  assert.equal(typeof new m.ProcessTerminal().start, "function");
  // Availability is platform/display dependent, not a throwing stand-in.
  const clipboard = m.getNativeClipboard();
  out.push(clipboard === undefined ? "unavailable" : [typeof clipboard.getText, typeof clipboard.getImage]);
  return out;
}
const want = scene(pi);
assert.deepEqual(scene(pig), want);
assert.deepEqual(Object.keys(pig), Object.keys(pi)); // TypeScript-only types must not become fabricated classes.
`)
}
