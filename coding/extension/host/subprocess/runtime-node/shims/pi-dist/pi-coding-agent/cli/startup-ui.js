import { ProcessTerminal, setCapabilityOverrides, setKeybindings, TuiMainScreen, } from "../../../pi-tui.mjs";
import { existsSync } from "fs";
import { APP_NAME, CONFIG_DIR_NAME, ENV_AGENT_DIR, getAgentDir, getSettingsPath, PACKAGE_NAME } from "../config.js";
import { areExperimentalFeaturesEnabled } from "../core/experimental.js";
import { KeybindingsManager } from "../core/keybindings.js";
import { DefaultPackageManager } from "../core/package-manager.js";
import { SettingsManager } from "../core/settings-manager.js";
import { ExtensionInputComponent } from "../modes/interactive/components/extension-input.js";
import { ExtensionSelectorComponent } from "../modes/interactive/components/extension-selector.js";
import { FirstTimeSetupComponent, } from "../modes/interactive/components/first-time-setup.js";
import { SYSTEM_THEME_NAME } from "../modes/interactive/theme/system-theme.js";
import { getTerminalTheme, initTheme, loadThemeFromPath, markTerminalColorsPending, resolveThemeSetting, setRegisteredThemes, setTerminalColors, setTheme, } from "../modes/interactive/theme/theme.js";
import { requestTerminalColors } from "../modes/interactive/theme/theme-controller.js";
const OFFICIAL_PACKAGE_NAME = "@earendil-works/pi-coding-agent";
const OFFICIAL_APP_NAME = "pi";
const OFFICIAL_CONFIG_DIR_NAME = ".pi";
function isOfficialDistribution({ packageName, appName, configDirName }) {
    return (packageName === OFFICIAL_PACKAGE_NAME &&
        appName === OFFICIAL_APP_NAME &&
        configDirName === OFFICIAL_CONFIG_DIR_NAME);
}
function loadThemes(resources) {
    const themes = [];
    const seen = new Set();
    for (const resource of resources) {
        if (!resource.enabled)
            continue;
        try {
            const loadedTheme = loadThemeFromPath(resource.path);
            if (loadedTheme.name) {
                if (seen.has(loadedTheme.name))
                    continue;
                seen.add(loadedTheme.name);
            }
            themes.push(loadedTheme);
        }
        catch {
            // Startup prompts should not fail because a theme is broken. The normal
            // resource loader reports theme diagnostics later in startup.
        }
    }
    return themes;
}
async function loadStartupThemes(settingsManager) {
    const globalSettingsManager = SettingsManager.inMemory(settingsManager.getGlobalSettings(), {
        projectTrusted: false,
    });
    const packageManager = new DefaultPackageManager({
        cwd: process.cwd(),
        agentDir: getAgentDir(),
        settingsManager: globalSettingsManager,
    });
    const resolvedPaths = await packageManager.resolve(async () => "skip");
    return loadThemes(resolvedPaths.themes);
}
export async function createStartupTui(settingsManager) {
    setCapabilityOverrides(settingsManager.getTerminalCapabilityOverrides());
    setRegisteredThemes(await loadStartupThemes(settingsManager));
    // The system theme starts in grayscale until the terminal reports its colors.
    markTerminalColorsPending();
    initTheme(resolveThemeSetting(settingsManager.getThemeSetting(), getTerminalTheme()) ?? SYSTEM_THEME_NAME);
    setKeybindings(KeybindingsManager.create());
    const ui = new TuiMainScreen(new ProcessTerminal(), settingsManager.getShowHardwareCursor(), getAgentDir());
    ui.setClearOnShrink(settingsManager.getClearOnShrink());
    return ui;
}
export function startStartupTui(ui, settingsManager) {
    ui.start();
    const themeSetting = settingsManager.getThemeSetting();
    queryStartupTerminalColors(ui, () => {
        setTheme(resolveThemeSetting(themeSetting, getTerminalTheme()) ?? SYSTEM_THEME_NAME);
    });
}
/**
 * Query the terminal's colors without waiting for them. When they arrive, including after the timeout,
 * record them for the system theme and "" (terminal default) tokens, run `onColors`, and re-render.
 */
function queryStartupTerminalColors(ui, onColors) {
    void requestTerminalColors(ui, (colors) => {
        setTerminalColors(colors);
        onColors();
        ui.invalidate();
        ui.requestRender();
    });
}
async function clearStartupTui(ui) {
    ui.clear();
    ui.requestRender();
    await new Promise((resolve) => setTimeout(resolve, 25));
}
/**
 * First-time setup runs when all of these hold:
 * - this is the official Pi distribution (not a fork/rebrand)
 * - experimental features are enabled (PI_EXPERIMENTAL=1)
 * - the default agent directory is used (no custom agent dir override)
 * - setup was not completed before (settings.json does not exist)
 */
export function shouldRunFirstTimeSetup(settingsPath = getSettingsPath()) {
    if (!isOfficialDistribution({
        packageName: PACKAGE_NAME,
        appName: APP_NAME,
        configDirName: CONFIG_DIR_NAME,
    })) {
        return false;
    }
    if (!areExperimentalFeaturesEnabled()) {
        return false;
    }
    if (process.env[ENV_AGENT_DIR]) {
        return false;
    }
    return !existsSync(settingsPath);
}
export async function showStartupSelector(settingsManager, title, options) {
    const ui = await createStartupTui(settingsManager);
    return new Promise((resolve) => {
        let settled = false;
        const finish = async (result) => {
            if (settled) {
                return;
            }
            settled = true;
            await clearStartupTui(ui);
            ui.stop();
            resolve(result);
        };
        const selector = new ExtensionSelectorComponent(title, options.map((option) => option.label), (option) => void finish(options.find((entry) => entry.label === option)?.value), () => void finish(undefined), { tui: ui });
        ui.addChild(selector);
        ui.setFocus(selector);
        startStartupTui(ui, settingsManager);
    });
}
/** Show the first-time setup dialog and persist the result */
export async function showFirstTimeSetup(settingsManager) {
    const ui = await createStartupTui(settingsManager);
    return new Promise((resolve) => {
        let settled = false;
        const finish = async (result) => {
            if (settled) {
                return;
            }
            settled = true;
            if (result) {
                settingsManager.setTheme(result.theme);
                settingsManager.setEnableAnalytics(result.shareAnalytics);
                await settingsManager.flush();
            }
            await clearStartupTui(ui);
            ui.stop();
            resolve();
        };
        ui.start();
        let previewTheme = SYSTEM_THEME_NAME;
        setTheme(previewTheme);
        const component = new FirstTimeSetupComponent({
            onThemePreview: (themeName) => {
                previewTheme = themeName;
                setTheme(themeName);
                ui.requestRender();
            },
            onSubmit: (result) => void finish(result),
            onCancel: () => void finish(undefined),
        });
        ui.addChild(component);
        ui.setFocus(component);
        ui.requestRender();
        // The terminal's colors regenerate the system theme; re-rendering rebuilds the dialog with it.
        queryStartupTerminalColors(ui, () => setTheme(previewTheme));
    });
}
export async function showStartupInput(settingsManager, title, placeholder) {
    const ui = await createStartupTui(settingsManager);
    return new Promise((resolve) => {
        let settled = false;
        const finish = async (result) => {
            if (settled) {
                return;
            }
            settled = true;
            input.dispose();
            await clearStartupTui(ui);
            ui.stop();
            resolve(result);
        };
        const input = new ExtensionInputComponent(title, placeholder, (value) => void finish(value), () => void finish(undefined), {
            tui: ui,
        });
        ui.addChild(input);
        ui.setFocus(input);
        startStartupTui(ui, settingsManager);
    });
}
//# sourceMappingURL=startup-ui.js.map