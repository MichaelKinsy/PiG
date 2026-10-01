import { ProcessTerminal, TuiAltScreen, TuiMainScreen } from "../../../../pi-tui.mjs";
import { copyToClipboard } from "../../utils/clipboard.js";
import { openBrowser } from "../../utils/open-browser.js";
import { keyDisplayText } from "./components/keybinding-hints.js";
import { theme } from "./theme/theme.js";
export function createInteractiveTui(options) {
    const terminal = options.terminal ?? new ProcessTerminal();
    if (options.tuiMode === "fullscreen") {
        const styleSearchMatch = (text) => theme.bg("searchMatchBg", theme.fg("searchMatchText", text));
        return new TuiAltScreen(terminal, options.showHardwareCursor, options.logDirectory, {
            searchMatchStyle: (text) => theme.underline(styleSearchMatch(text)),
            searchCurrentMatchStyle: (text) => theme.bold(theme.inverse(styleSearchMatch(text))),
            searchNavigationButtonStyle: (text, hovered) => (hovered ? theme.underline(text) : text),
            scrollToEndIndicator: () => {
                const shortcut = keyDisplayText("tui.altScreen.bottom");
                const label = ` ↓ Jump to latest message${shortcut ? ` · ${shortcut}` : ""} `;
                return theme.bg("selectedBg", theme.fg("text", label));
            },
            openUrl: openBrowser,
            onRightClickPaste: options.onRightClickPaste,
            copyOnSelect: options.fullscreenCopyOnSelect,
            wheelScrollLines: options.fullscreenWheelScrollLines ?? "auto",
            copySelection: async (text) => {
                try {
                    await copyToClipboard(text);
                    return true;
                }
                catch (error) {
                    return error instanceof Error ? error.message : String(error);
                }
            },
        });
    }
    return new TuiMainScreen(terminal, options.showHardwareCursor, options.logDirectory);
}
/** Stable reference for components while InteractiveMode replaces the active renderer. */
export function createInteractiveTuiReference(getTui) {
    return new Proxy({}, {
        get: (_target, property) => {
            const tui = getTui();
            const value = Reflect.get(tui, property, tui);
            if (typeof value !== "function")
                return value;
            let methodTui = tui;
            let method = value;
            return (...args) => {
                const currentTui = getTui();
                if (currentTui !== methodTui) {
                    const currentMethod = Reflect.get(currentTui, property, currentTui);
                    if (typeof currentMethod !== "function") {
                        throw new TypeError(`TUI property ${String(property)} is not callable`);
                    }
                    methodTui = currentTui;
                    method = currentMethod;
                }
                return Reflect.apply(method, methodTui, args);
            };
        },
        set: (_target, property, value) => {
            const tui = getTui();
            return Reflect.set(tui, property, value, tui);
        },
        has: (_target, property) => Reflect.has(getTui(), property),
        getPrototypeOf: () => Reflect.getPrototypeOf(getTui()),
    });
}
//# sourceMappingURL=tui-renderer.js.map