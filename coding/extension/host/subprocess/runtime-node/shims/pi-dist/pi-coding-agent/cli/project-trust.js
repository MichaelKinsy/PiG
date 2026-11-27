import chalk from "../../../chalk/source/index.js";
import { showStartupInput, showStartupSelector } from "./startup-ui.js";
export function createProjectTrustContext(options) {
    return {
        cwd: options.cwd,
        mode: options.mode === "interactive" ? "tui" : options.mode,
        hasUI: options.hasUI,
        ui: {
            select: async (title, selectOptions, opts) => {
                if (!options.hasUI) {
                    return undefined;
                }
                if (options.mode !== "interactive") {
                    return undefined;
                }
                return showStartupSelector(options.settingsManager, title, selectOptions.map((option) => ({ label: option, value: option })), opts?.blockedKind);
            },
            confirm: async (title, message, opts) => {
                if (!options.hasUI) {
                    return false;
                }
                if (options.mode !== "interactive") {
                    return false;
                }
                return ((await showStartupSelector(options.settingsManager, `${title}\n${message}`, [
                    { label: "Yes", value: true },
                    { label: "No", value: false },
                ], opts?.blockedKind ?? "permission")) ?? false);
            },
            input: async (title, placeholder, opts) => {
                if (!options.hasUI) {
                    return undefined;
                }
                if (options.mode !== "interactive") {
                    return undefined;
                }
                return showStartupInput(options.settingsManager, title, placeholder, opts?.blockedKind);
            },
            notify: (message, type = "info") => {
                if (options.mode !== "interactive") {
                    const color = type === "error" ? chalk.red : type === "warning" ? chalk.yellow : chalk.cyan;
                    console.error(color(message));
                }
            },
        },
    };
}
//# sourceMappingURL=project-trust.js.map