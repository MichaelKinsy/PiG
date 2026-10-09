// mouse.mjs: Pi's fullscreen mouse delivery to a ui.custom component that
// lives in this runtime. The host's renderer reads the
// terminal's reports and hands each event inside the component's bounds
// here as "ui.custom.mouse"; this module routes it as Pi's renderer does
// (tui-alt-screen.ts handleMouseEvent): a press to the component, and the
// drag, release and click of its gesture to the component that took the
// press, with coordinates local to it.

import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
let bases;

function baseHandlers() {
  if (!bases) {
    const { Container } = require("./shims/pi-dist/pi-tui/tui.js");
    const tui = require("./shims/pi-dist/pi-tui/sdk-bundle/index.js");
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
  const { dispatchMouseEvent, retargetMouseEvent } = require("./shims/pi-dist/pi-tui/tui.js");
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
