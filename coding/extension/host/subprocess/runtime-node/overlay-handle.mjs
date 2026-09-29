// Mounted custom-overlay handles use the transport's narrow synchronous call.
// The host applies controls on its UI owner loop without reentering Node.
export function mountedOverlayHandle(runtime, overlay, initial) {
  let state = initial;
  const apply = next => {
    state = next;
    if (overlay.component && "focused" in overlay.component) overlay.component.focused = state.focused;
    overlay.renderFrame();
  };
  overlay.applyHandleState = apply;
  overlay.releaseHandle = () => apply({ ...state, focused: false, visible: false, bounds: undefined });
  const control = (action, hidden, target) => {
    if (!overlay.active) {
      if (action === "setHidden") state = { ...state, hidden };
      return;
    }
    apply(runtime.callSync("ui.custom.control", { key: overlay.key, action, hidden, ...(target ? { target } : {}) }));
  };
  // Pi focuses exactly the explicit target (tui.ts unfocus). The host can focus nothing, its editor, or a mounted overlay of this extension.
  const focusTarget = component => {
    if (component === null || component === undefined) return { kind: "null" };
    for (const mounted of runtime.customOverlays.values()) {
      if (mounted.active && mounted.component === component) return { kind: "overlay", key: mounted.key };
    }
    if (runtime.editorHost?.session?.component === component) return { kind: "editor" };
    // pig divergence (D73): a component that is not mounted by this extension has no main-process identity to focus.
    throw new Error("Overlay unfocus(target) requires null, the editor component, or a mounted overlay of this extension (D73)");
  };
  apply(initial);
  return {
    hide: () => control("hide"),
    setHidden: hidden => control("setHidden", hidden),
    isHidden: () => state.hidden,
    focus: () => control("focus"),
    unfocus: options => {
      if (!options) control("unfocus");
      else control("unfocus", undefined, focusTarget(options.target));
    },
    isFocused: () => state.focused,
    getBounds: () => state.visible && state.bounds ? { ...state.bounds } : undefined,
  };
}
