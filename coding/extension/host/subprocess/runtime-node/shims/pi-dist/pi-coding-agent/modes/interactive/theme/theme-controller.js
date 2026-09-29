import { detectTerminalBackgroundFromEnv, detectTerminalBackgroundTheme, detectTerminalThemeForAuto, initTheme, parseAutoThemeSetting, resolveThemeSetting, setTheme, setThemeInstance, } from "./theme.js";
export class InteractiveThemeController {
    ui;
    getSettingsManager;
    showError;
    onChanged;
    currentThemeSetting;
    terminalTheme = detectTerminalBackgroundFromEnv().theme;
    activeThemeName;
    autoSyncEnabled = false;
    terminalColorSchemeUnsubscribe;
    constructor(ui, options) {
        this.ui = ui;
        this.getSettingsManager = options.getSettingsManager;
        this.showError = options.showError;
        this.onChanged = options.onChanged;
        this.currentThemeSetting = options.initialThemeSetting;
        this.activeThemeName = resolveThemeSetting(this.currentThemeSetting ?? this.getSettingsManager().getThemeSetting(), this.terminalTheme);
        initTheme(this.activeThemeName, true);
        this.bindTerminalColorSchemeListener();
    }
    rebindTui() {
        this.terminalColorSchemeUnsubscribe?.();
        this.bindTerminalColorSchemeListener();
        this.ui.setTerminalColorSchemeNotifications(this.autoSyncEnabled);
    }
    async applyFromSettings() {
        const settingsManager = this.getSettingsManager();
        const themeSetting = this.currentThemeSetting ?? settingsManager.getThemeSetting();
        const autoTheme = parseAutoThemeSetting(themeSetting);
        if (autoTheme) {
            this.terminalTheme = await detectTerminalThemeForAuto({ ui: this.ui, timeoutMs: 100 });
            this.setAutoSync(true);
            this.applyThemeName(this.terminalTheme === "light" ? autoTheme.lightTheme : autoTheme.darkTheme, true);
            return;
        }
        this.setAutoSync(false);
        if (themeSetting !== undefined) {
            this.applyThemeName(themeSetting, true);
            return;
        }
        const detection = await detectTerminalBackgroundTheme({ ui: this.ui, timeoutMs: 100 });
        this.terminalTheme = detection.theme;
        if (!this.applyThemeName(detection.theme).success)
            return;
        if (detection.confidence === "high") {
            settingsManager.setTheme(detection.theme);
            await settingsManager.flush();
        }
    }
    getThemeSelection() {
        return this.currentThemeSetting ?? this.getSettingsManager().getThemeSetting() ?? this.activeThemeName;
    }
    setThemeName(themeName, showError = false) {
        this.setAutoSync(false);
        const result = this.applyThemeName(themeName, showError);
        if (result.success) {
            this.currentThemeSetting = themeName;
        }
        return result;
    }
    async setThemeSetting(themeSetting) {
        this.currentThemeSetting = themeSetting;
        await this.applyFromSettings();
    }
    setThemeInstance(themeInstance) {
        this.setAutoSync(false);
        setThemeInstance(themeInstance);
        this.activeThemeName = "<in-memory>";
        this.notifyChanged();
        return { success: true };
    }
    preview(themeSettingOrName) {
        const themeName = resolveThemeSetting(themeSettingOrName, this.terminalTheme) ?? this.activeThemeName;
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
        return this.terminalTheme;
    }
    applyThemeName(themeName, showError = false) {
        const result = setTheme(themeName, true);
        this.activeThemeName = result.success ? themeName : "dark";
        this.notifyChanged();
        if (!result.success && showError) {
            this.showError(`Failed to load theme "${themeName}": ${result.error}\nFell back to dark theme.`);
        }
        return result;
    }
    notifyChanged() {
        this.ui.invalidate();
        this.onChanged();
    }
    setAutoSync(enabled) {
        if (this.autoSyncEnabled === enabled)
            return;
        this.autoSyncEnabled = enabled;
        this.ui.setTerminalColorSchemeNotifications(enabled);
    }
    bindTerminalColorSchemeListener() {
        this.terminalColorSchemeUnsubscribe = this.ui.onTerminalColorSchemeChange((terminalTheme) => this.applyTerminalTheme(terminalTheme));
    }
    applyTerminalTheme(terminalTheme) {
        if (!this.autoSyncEnabled)
            return;
        this.terminalTheme = terminalTheme;
        const autoTheme = parseAutoThemeSetting(this.currentThemeSetting ?? this.getSettingsManager().getThemeSetting());
        if (!autoTheme) {
            this.setAutoSync(false);
            return;
        }
        const themeName = terminalTheme === "light" ? autoTheme.lightTheme : autoTheme.darkTheme;
        if (themeName !== this.activeThemeName) {
            this.applyThemeName(themeName);
        }
    }
}
//# sourceMappingURL=theme-controller.js.map