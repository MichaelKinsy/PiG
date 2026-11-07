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
// these rectangles itself, and a click outside them is left unhandled as in Pi (it falls through to text selection and copy-on-select). Pi's own
// dispatchMouseEvent walks the real layout (Container and Box heights, Box padding), so each cell is probed through it with every MouseRegion's
// handler replaced by one that only records that it was reached; nothing of the extension runs. It returns undefined for a tree with a component
// that handles the mouse itself (not a MouseRegion, Container or Box): such a component answers for itself, and the host takes it as a whole (D277).
export function mouseRegions(component, width, height) {
  const tui = runtimeRequire("./shims/pi-dist/pi-tui/sdk-bundle/index.js");
  const { dispatchMouseEvent } = runtimeRequire("./shims/pi-dist/pi-tui/tui.js");
  const regions = [];
  let handWritten = false;
  const walk = (c, depth) => {
    if (!c || typeof c !== "object" || depth > 64) return;
    if (typeof c.handleMouse === "function") {
      if (c.handleMouse === tui.MouseRegion.prototype.handleMouse) regions.push(c);
      else if (!baseHandlers().has(c.handleMouse)) handWritten = true;
    }
    if (c.child) walk(c.child, depth + 1);
    if (Array.isArray(c.children)) for (const child of c.children) walk(child, depth + 1);
  };
  walk(component, 0);
  if (handWritten) return undefined;
  if (regions.length === 0 || !(width > 0) || !(height > 0)) return [];
  const saved = regions.map((region) => region.onMouse);
  let reached = false;
  for (const region of regions) {
    region.onMouse = () => {
      reached = true;
      return { handled: true };
    };
  }
  const rects = [];
  try {
    let open = [];
    for (let y = 0; y < height; y++) {
      const runs = [];
      let start = -1;
      for (let x = 0; x <= width; x++) {
        let hit = false;
        if (x < width) {
          reached = false;
          dispatchMouseEvent(component, { type: "click", button: "left", x, y, screenX: x, screenY: y, width, height, shift: false, alt: false, ctrl: false, clickCount: 1 });
          hit = reached;
        }
        if (hit && start < 0) start = x;
        if (!hit && start >= 0) {
          runs.push([start, x]);
          start = -1;
        }
      }
      // A run that repeats the one above it on consecutive rows extends that rectangle.
      const next = [];
      for (const [x0, x1] of runs) {
        const above = open.find((r) => r.x === x0 && r.width === x1 - x0 && r.y + r.height === y);
        if (above) {
          above.height += 1;
          next.push(above);
        } else {
          const rect = { x: x0, y, width: x1 - x0, height: 1 };
          rects.push(rect);
          next.push(rect);
        }
      }
      open = next;
    }
  } finally {
    regions.forEach((region, i) => {
      region.onMouse = saved[i];
    });
  }
  return rects;
}
