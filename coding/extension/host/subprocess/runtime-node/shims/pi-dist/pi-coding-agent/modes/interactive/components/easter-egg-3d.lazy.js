import { TuiAltScreen } from "../../../../../pi-tui.mjs";
/**
 * Plays a 3D easter egg (see easter-egg-3d.ts), which loads on first use. Only fullscreen mode can show it, because
 * it dissolves the rendered screen. The screen is captured before loading. Returns false when it cannot play.
 */
function playEasterEgg3d(tui, egg) {
    if (!(tui instanceof TuiAltScreen))
        return false;
    if (tui.hasOverlay())
        return true;
    const screen = tui.getScreenLines();
    import("./easter-egg-3d.js").then((module) => module.playEasterEgg3d(tui, screen, egg), () => { });
    return true;
}
/** Plays the 3D pi logo, lifting off the header logo whose top-left cell is at `column`, `row`. */
export function playPiLogo3d(tui, column, row) {
    playEasterEgg3d(tui, { kind: "pi-logo", column, row });
}
/** Plays the 3D Armin. Returns false when it cannot play, so the caller can fall back to the inline version. */
export function playArmin3d(tui) {
    return playEasterEgg3d(tui, { kind: "armin" });
}
//# sourceMappingURL=easter-egg-3d.lazy.js.map