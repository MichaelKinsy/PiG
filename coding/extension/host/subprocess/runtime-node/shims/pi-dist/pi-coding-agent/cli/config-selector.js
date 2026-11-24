/**
 * TUI config selector for `pi config` command
 */
import { ProcessTerminal, setKeybindings, TuiMainScreen } from "../../../pi-tui.mjs";
import { KeybindingsManager } from "../core/keybindings.js";
import { ConfigSelectorComponent } from "../modes/interactive/components/config-selector.js";
import { initTheme, stopThemeWatcher } from "../modes/interactive/theme/theme.js";
import { flushStdout } from "../utils/sleep.js";
/** Show TUI config selector and return when closed */
export async function selectConfig(options) {
    // Initialize theme before showing TUI
    initTheme(options.settingsManager.getTheme(), true);
    setKeybindings(KeybindingsManager.create(options.agentDir));
    return new Promise((resolve) => {
        const ui = new TuiMainScreen(new ProcessTerminal(), options.settingsManager.getShowHardwareCursor(), options.agentDir);
        ui.setClearOnShrink(options.settingsManager.getClearOnShrink());
        let resolved = false;
        // The caller exits right after; flush the TUI's terminal mode resets first (#12133).
        const stopUi = () => {
            ui.stop();
            stopThemeWatcher();
            return flushStdout();
        };
        const selector = new ConfigSelectorComponent(options.resolvedPaths, options.settingsManager, options.cwd, options.agentDir, () => {
            if (!resolved) {
                resolved = true;
                void stopUi().then(resolve);
            }
        }, () => {
            void stopUi().then(() => process.exit(0));
        }, () => ui.requestRender(), ui.terminal.rows, options.writeScope, options.projectModeAvailable);
        ui.addChild(selector);
        ui.setFocus(selector.getResourceList());
        ui.start();
    });
}
//# sourceMappingURL=config-selector.js.map