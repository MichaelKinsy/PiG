import chalk from "../../../chalk/source/index.js";
// Pi c47ad0432bb8 (post-v1.5.2), backported by automation/gen/pi-post-release-backports.mjs.
/** Print each diagnostic to stderr as `Error: ...` (red), `Warning: ...` (yellow) or a dim info line. */
export function writeDiagnosticsToStderr(diagnostics) {
    for (const diagnostic of diagnostics) {
        const color = diagnostic.type === "error" ? chalk.red : diagnostic.type === "warning" ? chalk.yellow : chalk.dim;
        const prefix = diagnostic.type === "error" ? "Error: " : diagnostic.type === "warning" ? "Warning: " : "";
        console.error(color(`${prefix}${diagnostic.message}`));
    }
}
export function collectSettingsDiagnostics(settingsManager) {
    return settingsManager.drainErrors().map(({ scope, path, error }) => ({
        type: "warning",
        message: path ? `Invalid settings file ${path}: ${error.message}` : `Invalid ${scope} settings: ${error.message}`,
    }));
}
/**
 * Remove duplicate type/message diagnostics while preserving their first occurrence.
 * Startup and runtime settings managers can report the same file error.
 */
export function deduplicateDiagnostics(diagnostics) {
    const seen = new Set();
    return diagnostics.filter((diagnostic) => {
        const key = `${diagnostic.type}\0${diagnostic.message}`;
        if (seen.has(key))
            return false;
        seen.add(key);
        return true;
    });
}
//# sourceMappingURL=settings-diagnostics.js.map