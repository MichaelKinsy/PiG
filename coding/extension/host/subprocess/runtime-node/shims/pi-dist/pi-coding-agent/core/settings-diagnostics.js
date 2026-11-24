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