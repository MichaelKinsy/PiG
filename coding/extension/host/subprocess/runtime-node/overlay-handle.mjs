// Mounted custom-overlay handles use the transport's narrow synchronous call.
// The host applies controls on its UI owner loop without reentering Node.
// onHide runs after the host applied the handle's hide; a raw overlay (tui.showOverlay) ends there.
export function mountedOverlayHandle(runtime, overlay, initial, { onHide } = {}) {
  let state = initial;
  const apply = next => {
    state = next;
    if (overlay.component && "focused" in overlay.component) overlay.component.focused = state.focused;
    overlay.renderFrame();
  };
  overlay.applyHandleState = apply;
  overlay.applyFocus = focused => {
    if (overlay.active) apply({ ...state, focused });
  };
  // Pi's setFocus clears the focused flag of every component but the one it focuses (tui.ts:602-665).
  overlay.setFocused = focused => {
    if (overlay.active && state.focused !== focused) apply({ ...state, focused });
  };
  overlay.releaseHandle = () => apply({ ...state, focused: false, visible: false, bounds: undefined });
  const control = (action, hidden, target) => {
    if (!overlay.active) {
      if (action === "setHidden") state = { ...state, hidden };
      return;
    }
    const result = runtime.callSync("ui.custom.control", { key: overlay.key, action, hidden, ...(target ? { target } : {}) });
    apply(result);
    runtime.syncOverlayFocus(result?.focusedKey, overlay.key);
    if (action === "hide") onHide?.();
  };
  apply(initial);
  return {
    hide: () => control("hide"),
    setHidden: hidden => control("setHidden", hidden),
    isHidden: () => state.hidden,
    focus: () => control("focus"),
    unfocus: options => {
      if (!options) control("unfocus");
      else control("unfocus", undefined, overlayFocusTarget(runtime, options.target));
    },
    isFocused: () => state.focused,
    getBounds: () => state.visible && state.bounds ? { ...state.bounds } : undefined,
  };
}

// overlayFocusTarget names an explicit focus target for the host (tui.ts setFocus, OverlayHandle.unfocus). The host can focus nothing,
// its editor, or a mounted overlay of this extension.
export function overlayFocusTarget(runtime, component, caller = "Overlay unfocus(target)") {
  if (component === null || component === undefined) return { kind: "null" };
  for (const mounted of runtime.customOverlays.values()) {
    if (mounted.active && mounted.component === component) return { kind: "overlay", key: mounted.key };
  }
  if (runtime.editorHost?.session?.component === component) return { kind: "editor" };
  // pig divergence (D73): a component that is not mounted by this extension has no main-process identity to focus.
  throw new Error(`${caller} requires null, the editor component, or a mounted overlay of this extension (D73)`);
}
