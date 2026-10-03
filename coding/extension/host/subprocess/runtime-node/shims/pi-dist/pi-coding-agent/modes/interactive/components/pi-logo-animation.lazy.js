import { TuiAltScreen } from "../../../../../pi-tui.mjs";
/**
 * Plays the logo easter egg (see pi-logo-animation.ts), which loads on the first click. Only fullscreen mode
 * can show it, because it dissolves the rendered screen. The screen is captured before loading.
 */
export function playPiLogoAnimation(tui, logoColumn, logoRow) {
    if (!(tui instanceof TuiAltScreen) || tui.hasOverlay())
        return;
    const screen = tui.getScreenLines();
    import("./pi-logo-animation.js").then((module) => module.playPiLogoAnimation(tui, { screen, logoColumn, logoRow }), () => { });
}
//# sourceMappingURL=pi-logo-animation.lazy.js.map