// mouse.mjs: Pi's fullscreen mouse delivery to a ui.custom component that
// lives in this runtime. The host's renderer reads the
// terminal's reports and hands each event inside the component's bounds
// here as "ui.custom.mouse"; this module routes it as Pi's renderer does
// (tui-alt-screen.ts handleMouseEvent): a press to the component, and the
// drag, release and click of its gesture to the component that took the
// press, with coordinates local to it.

import { runtimeRequire } from "./runtime-modules.mjs";

let bases;

function baseHandlers() {
  if (!bases) {
    const { Container } = runtimeRequire("./shims/pi-dist/pi-tui/tui.js");
    const tui = runtimeRequire("./shims/pi-dist/pi-tui/sdk-bundle/index.js");
    bases = new Set([Container.prototype.handleMouse, tui.Box.prototype.handleMouse]);
  }
  return bases;
}

// takesMouse reports whether a component handles the mouse: its own
// handleMouse, or a container's (Container, Box) that forwards to a child
// that does. The host must answer the terminal before this runtime can, so
// it hands such a component every event inside its bounds, and a component
// that takes none leaves the pointer to text selection, as in Pi.
export function takesMouse(component, depth = 0) {
  if (!component || typeof component.handleMouse !== "function" || depth > 64) return false;
  if (!baseHandlers().has(component.handleMouse)) return true;
  return Array.isArray(component.children) && component.children.some((child) => takesMouse(child, depth + 1));
}

// dispatchOverlayMouse delivers one event to overlay.component and returns
// the dispatch result of the component that took it, or undefined.
export function dispatchOverlayMouse(overlay, event) {
  const { dispatchMouseEvent, retargetMouseEvent } = runtimeRequire("./shims/pi-dist/pi-tui/tui.js");
  const target = overlay.mouseTarget;
  if (target && (!overlay.mouseReleased || event.type === "click")) {
    const result = dispatchMouseEvent(target.component, retargetMouseEvent(event, target));
    if (event.type === "release") overlay.mouseReleased = true;
    if (event.type === "click") {
      overlay.mouseTarget = undefined;
      overlay.mouseReleased = false;
    }
    return result;
  }
  overlay.mouseTarget = undefined;
  overlay.mouseReleased = false;
  const result = dispatchMouseEvent(overlay.component, event);
  if (result && event.type === "press") overlay.mouseTarget = result.target;
  return result;
}

// dispatchComponentMouse delivers one event, local to component, to a component of a transcript message or entry (Pi's dispatchMouseEvent), and
// returns the dispatch result of the component that took it, or undefined.
export function dispatchComponentMouse(component, event) {
  const { dispatchMouseEvent } = runtimeRequire("./shims/pi-dist/pi-tui/tui.js");
  return dispatchMouseEvent(component, event);
}

// mouseRegions finds where a transcript component takes the click, as Pi's hit test would: the rectangles (x, y, width, height, local to the
// component's first cell) in which a MouseRegion of its tree answers. The host answers the terminal before this runtime can, so it hit-tests
// these rectangles itself, and a click outside them is left unhandled as in Pi (it falls through to text selection and copy-on-select).
//
// Pi's dispatchMouseEvent hands an event down the tree through three handlers only, and each one is a rectangle rule: a Container passes an event
// whose y is inside its height to the child whose rows (the heights of the last render) hold it; a Box does the same inside its padding, at the
// content width; a MouseRegion answers for every event that reaches it. The cells a MouseRegion answers for are therefore the rectangle that reaches
// it: the walk below follows those rules once over the tree (the layouts the component's own render left, or a render of the children when a
// layout is for another width, as dispatch does), which costs the size of the tree, where asking Pi's dispatch about every cell costs the cells
// times the children of each container (a 1000-line card took 150-290 ms, and a few thousand lines passed the renderer's time limit). The result
// is the same cells; mouse_regions_test.go pins it against that per-cell probe over generated trees. Nothing of the extension runs, except the
// render of a stale layout.
//
// It returns undefined for a tree with a component that handles the mouse itself (not a MouseRegion, Container or Box): such a component answers
// for itself, and the host takes it as a whole (D277).
export function mouseRegions(component, width, height) {
  const tui = runtimeRequire("./shims/pi-dist/pi-tui/sdk-bundle/index.js");
  const bases = baseHandlers();
  const regionHandler = tui.MouseRegion.prototype.handleMouse;
  const boxHandler = tui.Box.prototype.handleMouse;
  const rects = [];
  let handWritten = false;
  // at is where the component's event coordinates start (x, y) and the width dispatch hands it (w), with the cells that reach it as the rectangle
  // [x0, x1) x [y0, y1) in the root's coordinates; the height dispatch hands a component is the rows its parent's layout gave it, which the reach
  // already holds. It is undefined when no cell needs to be found there: none reaches the component, or a MouseRegion above answers for all of them.
  const layoutOf = (c, contentWidth) =>
    c.mouseLayout?.width === contentWidth ? c.mouseLayout.children : c.children.map((child) => ({ component: child, height: child.render(contentWidth).length }));
  const walkLayout = (c, layout, at, depth) => {
    let childY = at.y;
    for (const { component: child, height: childHeight } of layout) {
      const y0 = Math.max(at.y0, childY);
      const y1 = Math.min(at.y1, childY + childHeight);
      walk(child, y0 < y1 && at.x0 < at.x1 ? { x: at.x, y: childY, w: at.w, x0: at.x0, x1: at.x1, y0, y1 } : undefined, depth + 1);
      childY += childHeight;
    }
    // A child the layout does not hold (it changed since the last render) is not reached by dispatch, but it may handle the mouse itself.
    if (layout.length !== c.children.length || layout.some((entry, i) => entry.component !== c.children[i])) {
      const laid = new Set(layout.map((entry) => entry.component));
      for (const child of c.children) if (!laid.has(child)) walk(child, undefined, depth + 1);
    }
  };
  const walk = (c, at, depth) => {
    if (!c || typeof c !== "object" || depth > 64) return;
    const handler = typeof c.handleMouse === "function" ? c.handleMouse : undefined;
    if (handler === regionHandler) {
      if (at) rects.push({ x: at.x0, y: at.y0, width: at.x1 - at.x0, height: at.y1 - at.y0 });
      at = undefined;
    } else if (handler && !bases.has(handler)) {
      handWritten = true;
    } else if (handler === boxHandler && Array.isArray(c.children)) {
      // Box.handleMouse: contentX in [0, contentWidth) and contentY >= 0, the children stacked from the padding.
      if (at) {
        const contentWidth = Math.max(1, at.w - c.paddingX * 2);
        const x = at.x + c.paddingX;
        const y = at.y + c.paddingY;
        const x0 = Math.max(at.x0, x);
        const x1 = Math.min(at.x1, x + contentWidth);
        const y0 = Math.max(at.y0, y);
        if (x0 < x1 && y0 < at.y1) walkLayout(c, layoutOf(c, contentWidth), { x, y, w: contentWidth, x0, x1, y0, y1: at.y1 }, depth);
        else at = undefined;
      }
      if (!at) for (const child of c.children) walk(child, undefined, depth + 1);
      return;
    } else if (handler && Array.isArray(c.children)) {
      // Container.handleMouse: y in [0, height), the children stacked from the top. Its parent's layout already holds the reach to that height.
      if (at) walkLayout(c, layoutOf(c, at.w), at, depth);
      if (!at) for (const child of c.children) walk(child, undefined, depth + 1);
      return;
    }
    if (c.child) walk(c.child, undefined, depth + 1);
    if (Array.isArray(c.children)) for (const child of c.children) walk(child, undefined, depth + 1);
  };
  walk(component, width > 0 && height > 0 ? { x: 0, y: 0, w: width, x0: 0, x1: width, y0: 0, y1: height } : undefined, 0);
  return handWritten ? undefined : rects;
}
