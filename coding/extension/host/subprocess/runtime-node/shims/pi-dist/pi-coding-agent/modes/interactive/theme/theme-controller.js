import { getTerminalTheme, initTheme, markTerminalColorsPending, parseAutoThemeSetting, resolveThemeSetting, SYSTEM_THEME_NAME, setTerminalColorScheme, setTerminalColors, setTheme, setThemeInstance, } from "./theme.js";
/**
 * How long the system theme stays grayscale before falling back to palette indices. Terminals answer
 * the trailing DA1 request right after the color replies, so this only matters for terminals that
 * answer neither. Replies arriving later still apply.
 */
const TERMINAL_QUERY_TIMEOUT_MS = 100;
/**
 * Query the terminal's colors and pass them to `apply` when the query completes or times out, and again
 * if the terminal answers after the timeout. A failed query applies no colors. Settles after the first apply.
 */
export function requestTerminalColors(ui, apply) {
    let query;
    try {
        query = ui.queryTerminalColors({ timeoutMs: TERMINAL_QUERY_TIMEOUT_MS, onLateReply: apply });
    }
    catch {
        // Treat a failed query like a terminal that does not report colors.
        query = Promise.resolve({});
    }
    return query.then(apply, () => apply({}));
}
function sameRgb(a, b) {
    return a === b || (a !== undefined && b !== undefined && a.r === b.r && a.g === b.g && a.b === b.b);
}
function sameTerminalColors(a, b) {
    if (!sameRgb(a.foreground, b.foreground) || !sameRgb(a.background, b.background))
        return false;
    if (a.palette === b.palette)
        return true;
    if (!a.palette || !b.palette || a.palette.length !== b.palette.length)
        return false;
    return a.palette.every((color, index) => sameRgb(color, b.palette?.[index]));
}
/**
 * Applies the theme setting and keeps it in sync with the terminal. The theme applies immediately, and the
 * terminal's colors update it when they arrive; the system theme renders in grayscale until then. Callers
 * that bake theme colors into content can wait for the colors with `waitForTerminalColors()`.
 */
export class InteractiveThemeController {
    ui;
    getSettingsManager;
    showError;
    onChanged;
    currentThemeSetting;
    // Last reported colors; a query that times out keeps them instead of erasing them.
    terminalColors;
    activeThemeName;
    autoSyncEnabled = false;
    terminalColorSchemeUnsubscribe;
    // Settles when the latest color query completed or timed out, and its colors applied.
    terminalColorQuery = Promise.resolve();
    constructor(ui, options) {
        this.ui = ui;
        this.getSettingsManager = options.getSettingsManager;
        this.showError = options.showError;
        this.onChanged = options.onChanged;
        this.currentThemeSetting = options.initialThemeSetting;
        this.activeThemeName = this.resolveThemeName();
        // The system theme starts in grayscale; color follows once the terminal reports its colors.
        markTerminalColorsPending();
        initTheme(this.activeThemeName, true);
        this.bindTerminalColorSchemeListener();
    }
    rebindTui() {
        this.terminalColorSchemeUnsubscribe?.();
        this.bindTerminalColorSchemeListener();
        this.ui.setTerminalColorSchemeNotifications(this.autoSyncEnabled);
    }
    /**
     * Apply the theme setting now and query the terminal's colors, which update the theme when they arrive.
     * Theme pairs and the system theme follow terminal appearance changes.
     */
    applyFromSettings() {
        const themeSetting = this.getThemeSetting();
        const themeName = this.resolveThemeName();
        this.setAutoSync(parseAutoThemeSetting(themeSetting) !== undefined || themeName === SYSTEM_THEME_NAME);
        this.applyThemeName(themeName, themeSetting !== undefined);
        this.queryTerminalColors();
    }
    /**
     * Wait until the latest color query completed or timed out. Content that bakes theme colors into
     * strings, such as the startup header, should be built after this. Terminals answer the DA1 request
     * right after the color replies, so this only takes the full timeout when a terminal answers nothing.
     */
    waitForTerminalColors() {
        return this.terminalColorQuery;
    }
    getThemeSelection() {
        return this.currentThemeSetting ?? this.getSettingsManager().getThemeSetting() ?? this.activeThemeName;
    }
    setThemeName(themeName, showError = false) {
        this.setAutoSync(themeName === SYSTEM_THEME_NAME);
        const result = this.applyThemeName(themeName, showError);
        if (result.success) {
            this.currentThemeSetting = themeName;
        }
        return result;
    }
    setThemeSetting(themeSetting) {
        this.currentThemeSetting = themeSetting;
        this.applyFromSettings();
    }
    setThemeInstance(themeInstance) {
        this.setAutoSync(false);
        setThemeInstance(themeInstance);
        this.activeThemeName = "<in-memory>";
        this.notifyChanged();
        return { success: true };
    }
    preview(themeSettingOrName) {
        const themeName = resolveThemeSetting(themeSettingOrName, getTerminalTheme()) ?? this.activeThemeName;
        if (!themeName)
            return;
        if (setTheme(themeName, true).success) {
            this.ui.invalidate();
            this.ui.requestRender();
        }
    }
    disableAutoSync() {
        this.setAutoSync(false);
    }
    dispose() {
        this.setAutoSync(false);
        this.terminalColorSchemeUnsubscribe?.();
        this.terminalColorSchemeUnsubscribe = undefined;
    }
    getTerminalTheme() {
        return getTerminalTheme();
    }
    getThemeSetting() {
        return this.currentThemeSetting ?? this.getSettingsManager().getThemeSetting();
    }
    /** The theme for the current setting and terminal appearance. Without a setting, pi uses the system theme. */
    resolveThemeName() {
        return resolveThemeSetting(this.getThemeSetting(), getTerminalTheme()) ?? SYSTEM_THEME_NAME;
    }
    applyThemeName(themeName, showError = false) {
        const result = setTheme(themeName, true);
        this.activeThemeName = result.success ? themeName : SYSTEM_THEME_NAME;
        this.notifyChanged();
        if (!result.success && showError) {
            this.showError(`Failed to load theme "${themeName}": ${result.error}\nFell back to the system theme.`);
        }
        return result;
    }
    /** Query the terminal's colors without waiting for them; `waitForTerminalColors()` waits for this query. */
    queryTerminalColors() {
        this.terminalColorQuery = requestTerminalColors(this.ui, (colors) => this.applyTerminalColors(colors));
    }
    /**
     * Record reported colors: themes use the default colors for tokens set to "", the system theme is
     * generated from all of them, and light/dark detection uses them. Re-renders only when they changed.
     */
    applyTerminalColors(reported) {
        const previous = this.terminalColors;
        const next = {
            foreground: reported.foreground ?? previous?.foreground,
            background: reported.background ?? previous?.background,
            palette: reported.palette ?? previous?.palette,
        };
        // Re-rendering rebuilds every component, so skip it when nothing changed (including timeouts).
        if (previous && sameTerminalColors(previous, next))
            return;
        this.terminalColors = next;
        setTerminalColors(next);
        this.reapplyForTerminal();
        this.ui.invalidate();
        this.ui.requestRender();
    }
    /**
     * Re-apply the setting after the terminal's colors or appearance changed: regenerate the system theme,
     * or switch the theme of a pair. Themes set through extensions or previews are left alone.
     */
    reapplyForTerminal() {
        if (this.activeThemeName === "<in-memory>")
            return;
        const themeName = this.resolveThemeName();
        if (themeName === SYSTEM_THEME_NAME || themeName !== this.activeThemeName) {
            this.applyThemeName(themeName);
        }
    }
    setAutoSync(enabled) {
        if (this.autoSyncEnabled === enabled)
            return;
        this.autoSyncEnabled = enabled;
        this.ui.setTerminalColorSchemeNotifications(enabled);
    }
    bindTerminalColorSchemeListener() {
        this.terminalColorSchemeUnsubscribe = this.ui.onTerminalColorSchemeChange((terminalTheme) => this.applyTerminalColorSchemeChange(terminalTheme));
    }
    /**
     * The terminal reported a light/dark switch. Its colors changed too, so query them again: they decide
     * the appearance. The reported scheme only matters for terminals that do not report their background.
     */
    applyTerminalColorSchemeChange(terminalTheme) {
        if (!this.autoSyncEnabled)
            return;
        const previous = getTerminalTheme();
        setTerminalColorScheme(terminalTheme);
        if (getTerminalTheme() !== previous)
            this.reapplyForTerminal();
        this.queryTerminalColors();
    }
    notifyChanged() {
        this.ui.invalidate();
        this.onChanged();
    }
}
//# sourceMappingURL=theme-controller.js.map