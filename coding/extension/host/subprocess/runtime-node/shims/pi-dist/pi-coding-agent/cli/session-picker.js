/**
 * TUI session selector for --resume flag
 */
import { setKeybindings } from "../../../pi-tui.mjs";
import { KeybindingsManager } from "../core/keybindings.js";
import { SessionSelectorComponent } from "../modes/interactive/components/session-selector.js";
import { flushStdout } from "../utils/flush-stdout.js";
import { createStartupTui, startStartupTui } from "./startup-ui.js";
/** Show TUI session selector and return selected session path or null if cancelled */
export async function selectSession(currentSessionsLoader, allSessionsLoader, settingsManager) {
    const ui = await createStartupTui(settingsManager);
    return new Promise((resolve) => {
        const keybindings = KeybindingsManager.create();
        setKeybindings(keybindings);
        let resolved = false;
        // Callers may exit right after; flush the TUI's terminal mode resets first (#12133).
        const stopUi = () => {
            ui.stop();
            return flushStdout();
        };
        const selector = new SessionSelectorComponent(currentSessionsLoader, allSessionsLoader, (path) => {
            if (!resolved) {
                resolved = true;
                void stopUi().then(() => resolve(path));
            }
        }, () => {
            if (!resolved) {
                resolved = true;
                void stopUi().then(() => resolve(null));
            }
        }, () => {
            void stopUi().then(() => process.exit(0));
        }, () => ui.requestRender(), { showRenameHint: false, keybindings });
        ui.addChild(selector);
        ui.setFocus(selector.getSessionList());
        startStartupTui(ui, settingsManager);
    });
}
//# sourceMappingURL=session-picker.js.map