import * as fs from "node:fs";
import * as path from "node:path";
import { backgroundAnsi, colorToHex, colorToOklch, foregroundAnsi, indexedColor, mixColors, parseColor, rgbColor, styleTextWithAnsi, } from "../../../../pi-tui/colors.js";
import { getTerminalColorMode, } from "../../../../pi-tui/terminal-image.js";
import chalk from "../../../../../chalk/source/index.js";
import { getCustomThemesDir, getThemesDir } from "../../../config.js";
import { closeWatcher, watchWithErrorHandler } from "../../../utils/fs-watch.js";
import { highlight, supportsLanguage } from "../../../../../syntax-highlight.mjs";
import { stripBom } from "../../../utils/text.js";
import { generateSystemThemeColors, SYSTEM_THEME_NAME, terminalAppearance } from "./system-theme.js";
export { SYSTEM_THEME_NAME } from "./system-theme.js";
let themeJsonValidator;
/**
 * Install full theme validation. Without it, documents are accepted as-is, which is what built-in
 * themes already do: validating user-authored JSON needs typebox, and a presentation that only uses
 * built-in themes should not pay ~17 MB of module graph for it.
 */
export function setThemeJsonValidator(validator) {
    themeJsonValidator = validator;
}
// ============================================================================
// Color Utilities
// ============================================================================
function resolveVarRefs(value, vars, visited = new Set()) {
    if (typeof value === "number" || value === "" || value.startsWith("#") || /^ok(lch|hsl)\(/i.test(value)) {
        return value;
    }
    if (visited.has(value)) {
        throw new Error(`Circular variable reference detected: ${value}`);
    }
    if (!(value in vars)) {
        throw new Error(`Variable reference not found: ${value}`);
    }
    visited.add(value);
    return resolveVarRefs(vars[value], vars, visited);
}
function resolveThemeColors(colors, vars = {}) {
    const resolved = {};
    for (const [key, value] of Object.entries(colors)) {
        resolved[key] = resolveVarRefs(value, vars);
    }
    return resolved;
}
function withThemeColorFallbacks(colors) {
    return {
        ...colors,
        scrollbarTrack: colors.scrollbarTrack ?? colors.muted,
        scrollbarThumb: colors.scrollbarThumb ?? colors.text,
        thinkingMax: colors.thinkingMax ?? colors.thinkingXhigh,
        searchMatchBg: colors.searchMatchBg ?? colors.selectedBg,
        searchMatchText: colors.searchMatchText ?? colors.text,
    };
}
// The terminal's reported colors. Replaced (never mutated) on update, so themes can cache resolved colors
// by identity.
let terminalColors = {};
// While the terminal color query is in flight, the system theme renders in grayscale.
let terminalColorsPending = false;
// The terminal's last light/dark report (mode 2031). Only used while it has not reported a background.
let terminalColorScheme;
/**
 * Record the terminal's reported colors. Themes use the default colors for tokens set to "" (terminal
 * default); the system theme is generated from all of them. Ends the pending state.
 */
export function setTerminalColors(colors) {
    terminalColors = { ...colors };
    terminalColorsPending = false;
}
/** Record the terminal's light/dark report, the fallback for terminals that do not report their background. */
export function setTerminalColorScheme(scheme) {
    terminalColorScheme = scheme;
}
/** Render the system theme in grayscale until `setTerminalColors()` reports the terminal's colors. */
export function markTerminalColorsPending() {
    terminalColorsPending = true;
}
/** Assumed terminal default colors when the terminal does not report them. */
const GUESSED_DEFAULT_COLORS = {
    dark: { foreground: parseColor("#e5e5e7"), background: parseColor("#000000") },
    light: { foreground: parseColor("#000000"), background: parseColor("#ffffff") },
};
function averageLightness(colors) {
    // Palette colors 0-15 follow the user's terminal palette, so they say nothing about the theme.
    const fixed = colors.filter((color) => color.kind !== "indexed" || color.index >= 16);
    if (fixed.length === 0)
        return undefined;
    return fixed.reduce((sum, color) => sum + colorToOklch(color).l, 0) / fixed.length;
}
/** Detect the background a theme is designed for from the lightness of its own colors. */
function detectAppearance(foregrounds, backgrounds) {
    const fg = averageLightness(foregrounds);
    const bg = averageLightness(backgrounds);
    if (fg !== undefined && bg !== undefined)
        return bg < fg ? "dark" : "light";
    if (bg !== undefined)
        return bg < 0.5 ? "dark" : "light";
    if (fg !== undefined)
        return fg > 0.5 ? "dark" : "light";
    return undefined;
}
// ============================================================================
// Theme Class
// ============================================================================
export class Theme {
    name;
    sourcePath;
    sourceInfo;
    mode;
    // Precomputed escape sequences keep fg()/bg() on the render hot path to a lookup and concat.
    fgAnsi = new Map();
    bgAnsi = new Map();
    // Tokens set to "" have no color of their own; `colors` fills them from the terminal defaults.
    concreteColors = {};
    defaultForegroundTokens = [];
    defaultBackgroundTokens = [];
    // Foreground tokens rendered faint (SGR 2) on top of their color.
    dimTokens;
    ownAppearance;
    resolvedColors;
    constructor(fgColors, bgColors, mode, options = {}) {
        this.name = options.name;
        this.sourcePath = options.sourcePath;
        this.sourceInfo = options.sourceInfo;
        this.mode = mode;
        this.dimTokens = new Set(options.dim);
        const foregrounds = {
            ...fgColors,
            scrollbarTrack: fgColors.scrollbarTrack ?? fgColors.muted,
            scrollbarThumb: fgColors.scrollbarThumb ?? fgColors.text,
            thinkingMax: fgColors.thinkingMax ?? fgColors.thinkingXhigh,
            searchMatchText: fgColors.searchMatchText ?? fgColors.text,
        };
        const backgrounds = { ...bgColors, searchMatchBg: bgColors.searchMatchBg ?? bgColors.selectedBg };
        const concreteForegrounds = [];
        const concreteBackgrounds = [];
        // Returns the escape sequence for the token's own slot.
        const addToken = (token, value, isBackground) => {
            if (value === "") {
                (isBackground ? this.defaultBackgroundTokens : this.defaultForegroundTokens).push(token);
                return isBackground ? "\x1b[49m" : "\x1b[39m";
            }
            const color = parseColor(value);
            this.concreteColors[token] = color;
            (isBackground ? concreteBackgrounds : concreteForegrounds).push(color);
            return isBackground ? backgroundAnsi(color, mode) : foregroundAnsi(color, mode);
        };
        for (const [token, value] of Object.entries(foregrounds)) {
            this.fgAnsi.set(token, addToken(token, value, false));
        }
        for (const [token, value] of Object.entries(backgrounds)) {
            this.bgAnsi.set(token, addToken(token, value, true));
        }
        this.ownAppearance = options.appearance ?? detectAppearance(concreteForegrounds, concreteBackgrounds);
    }
    /**
     * The background the theme is designed for: declared in the theme JSON, detected from its colors,
     * or, for themes without usable colors, the terminal's appearance.
     */
    get appearance() {
        return this.ownAppearance ?? getTerminalTheme();
    }
    /**
     * Concrete colors for all tokens. Tokens set to "" (terminal default) use the terminal's reported
     * default colors, or a guess based on `appearance` when the terminal did not report them. Faint
     * tokens are approximated by mixing their color toward the background.
     */
    get colors() {
        const terminal = terminalColors;
        if (this.resolvedColors?.terminal !== terminal) {
            const guess = GUESSED_DEFAULT_COLORS[this.appearance];
            const toColor = (rgb, fallback) => rgb ? rgbColor(rgb.r, rgb.g, rgb.b) : fallback;
            const foreground = toColor(terminal.foreground, guess.foreground);
            const background = toColor(terminal.background, guess.background);
            const colors = { ...this.concreteColors };
            for (const token of this.defaultForegroundTokens)
                colors[token] = foreground;
            for (const token of this.defaultBackgroundTokens)
                colors[token] = background;
            for (const token of this.dimTokens) {
                const color = colors[token];
                if (color)
                    colors[token] = mixColors(color, background, 0.4);
            }
            this.resolvedColors = { terminal, colors: Object.freeze(colors) };
        }
        return this.resolvedColors.colors;
    }
    style(text, options) {
        const { fg, bg } = options;
        if (typeof fg === "string" && this.dimTokens.has(fg))
            options = { ...options, dim: true };
        return styleTextWithAnsi(text, fg === undefined
            ? undefined
            : typeof fg === "string"
                ? this.tokenAnsi(this.fgAnsi, fg)
                : foregroundAnsi(fg, this.mode), bg === undefined
            ? undefined
            : typeof bg === "string"
                ? this.tokenAnsi(this.bgAnsi, bg)
                : backgroundAnsi(bg, this.mode), options);
    }
    fg(color, text) {
        const ansi = this.tokenAnsi(this.fgAnsi, color);
        if (this.dimTokens.has(color))
            return `${ansi}\x1b[2m${text}\x1b[22;39m`;
        return `${ansi}${text}\x1b[39m`;
    }
    bg(color, text) {
        const ansi = this.tokenAnsi(this.bgAnsi, color);
        return `${ansi}${text}\x1b[49m`;
    }
    tokenAnsi(ansi, token) {
        const value = ansi.get(token);
        if (value === undefined)
            throw new Error(`Unknown theme color: ${token}`);
        return value;
    }
    bold(text) {
        return chalk.bold(text);
    }
    italic(text) {
        return chalk.italic(text);
    }
    underline(text) {
        return chalk.underline(text);
    }
    inverse(text) {
        return chalk.inverse(text);
    }
    strikethrough(text) {
        return chalk.strikethrough(text);
    }
    /** Opening escape sequence for a foreground token. Faint tokens include SGR 2, which `\x1b[22m` closes. */
    getFgAnsi(color) {
        const ansi = this.tokenAnsi(this.fgAnsi, color);
        return this.dimTokens.has(color) ? `${ansi}\x1b[2m` : ansi;
    }
    getBgAnsi(color) {
        return this.tokenAnsi(this.bgAnsi, color);
    }
    getColorMode() {
        return this.mode;
    }
    getThinkingBorderColor(level) {
        // Map thinking levels to dedicated theme colors
        switch (level) {
            case "off":
                return (str) => this.fg("thinkingOff", str);
            case "minimal":
                return (str) => this.fg("thinkingMinimal", str);
            case "low":
                return (str) => this.fg("thinkingLow", str);
            case "medium":
                return (str) => this.fg("thinkingMedium", str);
            case "high":
                return (str) => this.fg("thinkingHigh", str);
            case "xhigh":
                return (str) => this.fg("thinkingXhigh", str);
            case "max":
                return (str) => this.fg("thinkingMax", str);
            default:
                return (str) => this.fg("thinkingOff", str);
        }
    }
    getBashModeBorderColor() {
        return (str) => this.fg("bashMode", str);
    }
}
// ============================================================================
// Theme Loading
// ============================================================================
let BUILTIN_THEMES;
function getBuiltinThemes() {
    if (!BUILTIN_THEMES) {
        const themesDir = getThemesDir();
        const darkPath = path.join(themesDir, "dark.json");
        const lightPath = path.join(themesDir, "light.json");
        BUILTIN_THEMES = {
            dark: JSON.parse(stripBom(fs.readFileSync(darkPath, "utf-8"))),
            light: JSON.parse(stripBom(fs.readFileSync(lightPath, "utf-8"))),
        };
    }
    return BUILTIN_THEMES;
}
export function getAvailableThemes() {
    return getAvailableThemesWithPaths().map(({ name }) => name);
}
export function getAvailableThemesWithPaths() {
    const themesDir = getThemesDir();
    const result = [];
    const seen = new Set();
    const addTheme = (themeInfo) => {
        if (seen.has(themeInfo.name)) {
            return;
        }
        seen.add(themeInfo.name);
        result.push(themeInfo);
    };
    // Built-in themes. The system theme is generated, so it has no file.
    addTheme({ name: SYSTEM_THEME_NAME, path: undefined });
    for (const name of Object.keys(getBuiltinThemes())) {
        addTheme({ name, path: path.join(themesDir, `${name}.json`) });
    }
    // Custom themes
    for (const themeInfo of getCustomThemeInfos()) {
        addTheme(themeInfo);
    }
    for (const [name, theme] of registeredThemes.entries()) {
        addTheme({ name, path: theme.sourcePath });
    }
    // The system theme comes first: it is the default and adapts to every terminal.
    return result.sort((a, b) => a.name === SYSTEM_THEME_NAME ? -1 : b.name === SYSTEM_THEME_NAME ? 1 : a.name.localeCompare(b.name));
}
function getCustomThemeInfos() {
    const customThemesDir = getCustomThemesDir();
    const result = [];
    if (!fs.existsSync(customThemesDir)) {
        return result;
    }
    for (const file of fs.readdirSync(customThemesDir)) {
        if (!file.endsWith(".json")) {
            continue;
        }
        const themePath = path.join(customThemesDir, file);
        try {
            const customTheme = loadThemeFromPath(themePath);
            if (customTheme.name) {
                result.push({ name: customTheme.name, path: themePath });
            }
        }
        catch {
            // Invalid themes are ignored here; the resource loader reports them
            // during normal startup/reload.
        }
    }
    return result;
}
function assertThemeNameIsValid(name) {
    if (name.includes("/")) {
        throw new Error(`Invalid theme name "${name}": theme names cannot contain "/" because it is reserved for automatic light/dark theme settings.`);
    }
}
function parseThemeJson(label, json) {
    if (themeJsonValidator)
        return themeJsonValidator(label, json);
    if (typeof json !== "object" || json === null || !("colors" in json)) {
        throw new Error(`Invalid theme "${label}": expected an object with a "colors" map.`);
    }
    return json;
}
function parseThemeJsonContent(label, content) {
    let json;
    try {
        json = JSON.parse(stripBom(content));
    }
    catch (error) {
        throw new Error(`Failed to parse theme ${label}: ${error}`);
    }
    return parseThemeJson(label, json);
}
function loadThemeJson(name) {
    const builtinThemes = getBuiltinThemes();
    if (name in builtinThemes) {
        return builtinThemes[name];
    }
    const registeredTheme = registeredThemes.get(name);
    if (registeredTheme?.sourcePath) {
        const content = fs.readFileSync(registeredTheme.sourcePath, "utf-8");
        return parseThemeJsonContent(registeredTheme.sourcePath, content);
    }
    if (registeredTheme) {
        throw new Error(`Theme "${name}" does not have a source path for export`);
    }
    const customThemesDir = getCustomThemesDir();
    const themePath = path.join(customThemesDir, `${name}.json`);
    if (!fs.existsSync(themePath)) {
        throw new Error(`Theme not found: ${name}`);
    }
    const content = fs.readFileSync(themePath, "utf-8");
    return parseThemeJsonContent(name, content);
}
const BACKGROUND_TOKENS = new Set([
    "selectedBg",
    "searchMatchBg",
    "userMessageBg",
    "customMessageBg",
    "toolPendingBg",
    "toolSuccessBg",
    "toolErrorBg",
]);
function splitThemeColors(colors) {
    const fgColors = {};
    const bgColors = {};
    for (const [key, value] of Object.entries(colors)) {
        if (BACKGROUND_TOKENS.has(key)) {
            bgColors[key] = value;
        }
        else {
            fgColors[key] = value;
        }
    }
    return { fgColors, bgColors };
}
function createTheme(themeJson, mode, sourcePath) {
    const colorMode = mode ?? getTerminalColorMode();
    const resolvedColors = resolveThemeColors(withThemeColorFallbacks(themeJson.colors), themeJson.vars);
    const { fgColors, bgColors } = splitThemeColors(resolvedColors);
    return new Theme(fgColors, bgColors, colorMode, {
        name: themeJson.name,
        sourcePath,
        appearance: themeJson.appearance,
    });
}
/** Generate the system theme from the terminal's reported colors (grayscale while they are pending). */
function createSystemTheme(mode) {
    const generated = generateSystemThemeColors({
        ...terminalColors,
        saturation: terminalColorsPending ? 0 : 1,
        appearanceHint: getTerminalTheme(),
    });
    const { fgColors, bgColors } = splitThemeColors(generated.colors);
    return new Theme(fgColors, bgColors, mode ?? getTerminalColorMode(), {
        name: SYSTEM_THEME_NAME,
        appearance: generated.appearance,
        dim: generated.dim,
    });
}
export function loadThemeFromPath(themePath, mode) {
    const content = fs.readFileSync(themePath, "utf-8");
    const themeJson = parseThemeJsonContent(themePath, content);
    return createTheme(themeJson, mode, themePath);
}
function loadTheme(name, mode) {
    // The system theme name is reserved: it takes precedence over custom themes of the same name.
    if (name === SYSTEM_THEME_NAME)
        return createSystemTheme(mode);
    const registeredTheme = registeredThemes.get(name);
    if (registeredTheme) {
        return registeredTheme;
    }
    const themeJson = loadThemeJson(name);
    return createTheme(themeJson, mode);
}
export function getThemeByName(name) {
    try {
        return loadTheme(name);
    }
    catch {
        return undefined;
    }
}
export function parseAutoThemeSetting(themeSetting) {
    if (!themeSetting)
        return undefined;
    const slashIndex = themeSetting.indexOf("/");
    if (slashIndex === -1 || themeSetting.indexOf("/", slashIndex + 1) !== -1) {
        return undefined;
    }
    const lightTheme = themeSetting.slice(0, slashIndex).trim();
    const darkTheme = themeSetting.slice(slashIndex + 1).trim();
    if (!lightTheme || !darkTheme) {
        return undefined;
    }
    return { lightTheme, darkTheme };
}
export function resolveThemeSetting(themeSetting, terminalTheme) {
    const autoTheme = parseAutoThemeSetting(themeSetting);
    if (autoTheme) {
        return terminalTheme === "light" ? autoTheme.lightTheme : autoTheme.darkTheme;
    }
    if (themeSetting?.includes("/"))
        return undefined;
    if (typeof themeSetting === "string")
        return themeSetting;
    return undefined;
}
/**
 * Dark or light from the `COLORFGBG` environment variable some terminals set, or undefined without a
 * usable background index. The value is `fg;bg` or `fg;xpm;bg` (rxvt), where a field is an ANSI color
 * index or `default` when the color is not in the palette. The index refers to the terminal's own palette,
 * whose colors are unknown here, so it is classified by index like Vim does: 0-6 and 8 (bright black, e.g.
 * Solarized Dark's background) are dark, 7 and 9-15 are light.
 */
export function detectColorFgBgTheme(env = process.env) {
    const bg = env.COLORFGBG?.split(";").at(-1)?.trim();
    if (!bg || !/^\d{1,2}$/.test(bg))
        return undefined;
    const index = Number(bg);
    if (index > 15)
        return undefined;
    return index <= 6 || index === 8 ? "dark" : "light";
}
/**
 * Whether the terminal is dark or light. The background it renders decides, classified the same way the
 * system theme does. Without a reported background: the terminal's light/dark report, then COLORFGBG,
 * then dark.
 */
export function detectTerminalTheme(colors = {}, reportedScheme, env = process.env) {
    const { background, foreground } = colors;
    if (background)
        return terminalAppearance(background, foreground);
    return reportedScheme ?? detectColorFgBgTheme(env) ?? "dark";
}
/** Whether the terminal is dark or light, from everything it reported so far. See `detectTerminalTheme()`. */
export function getTerminalTheme() {
    return detectTerminalTheme(terminalColors, terminalColorScheme);
}
// ============================================================================
// Global Theme Instance
// ============================================================================
// Use globalThis to share theme across module loaders (node + jiti in dev mode)
const THEME_KEY = Symbol.for("@earendil-works/pi-coding-agent:theme");
const THEME_KEY_OLD = Symbol.for("@mariozechner/pi-coding-agent:theme");
// Export theme as a getter that reads from globalThis
// This ensures all module instances (node, jiti) see the same theme
export const theme = new Proxy({}, {
    get(_target, prop) {
        const t = globalThis[THEME_KEY];
        if (!t)
            throw new Error("Theme not initialized. Call initTheme() first.");
        return t[prop];
    },
});
function setGlobalTheme(t) {
    globalThis[THEME_KEY] = t;
    globalThis[THEME_KEY_OLD] = t;
}
let currentThemeName;
let themeWatcher;
let themeReloadTimer;
let onThemeChangeCallback;
const registeredThemes = new Map();
export function setRegisteredThemes(themes) {
    registeredThemes.clear();
    for (const theme of themes) {
        if (theme.name) {
            assertThemeNameIsValid(theme.name);
            registeredThemes.set(theme.name, theme);
        }
    }
}
export function initTheme(themeName, enableWatcher = false) {
    const name = themeName ?? SYSTEM_THEME_NAME;
    currentThemeName = name;
    try {
        setGlobalTheme(loadTheme(name));
        if (enableWatcher) {
            startThemeWatcher();
        }
    }
    catch (_error) {
        // Theme is invalid - fall back to the system theme silently
        currentThemeName = SYSTEM_THEME_NAME;
        setGlobalTheme(loadTheme(SYSTEM_THEME_NAME));
        // Don't start watcher for fallback theme
    }
}
export function setTheme(name, enableWatcher = false) {
    currentThemeName = name;
    try {
        setGlobalTheme(loadTheme(name));
        if (enableWatcher) {
            startThemeWatcher();
        }
        if (onThemeChangeCallback) {
            onThemeChangeCallback();
        }
        return { success: true };
    }
    catch (error) {
        // Theme is invalid - fall back to the system theme
        currentThemeName = SYSTEM_THEME_NAME;
        setGlobalTheme(loadTheme(SYSTEM_THEME_NAME));
        // Don't start watcher for fallback theme
        return {
            success: false,
            error: error instanceof Error ? error.message : String(error),
        };
    }
}
export function setThemeInstance(themeInstance) {
    setGlobalTheme(themeInstance);
    currentThemeName = "<in-memory>";
    stopThemeWatcher(); // Can't watch a direct instance
    if (onThemeChangeCallback) {
        onThemeChangeCallback();
    }
}
export function onThemeChange(callback) {
    onThemeChangeCallback = callback;
}
function startThemeWatcher() {
    stopThemeWatcher();
    // Only watch if it's a custom theme (not built-in)
    if (!currentThemeName ||
        currentThemeName === "dark" ||
        currentThemeName === "light" ||
        currentThemeName === SYSTEM_THEME_NAME) {
        return;
    }
    const customThemesDir = getCustomThemesDir();
    const watchedThemeName = currentThemeName;
    const watchedFileName = `${watchedThemeName}.json`;
    const themeFile = path.join(customThemesDir, watchedFileName);
    // Only watch if the file exists
    if (!fs.existsSync(themeFile)) {
        return;
    }
    const scheduleReload = () => {
        if (themeReloadTimer) {
            clearTimeout(themeReloadTimer);
        }
        themeReloadTimer = setTimeout(() => {
            themeReloadTimer = undefined;
            // Ignore stale timers after switching themes or stopping the watcher
            if (currentThemeName !== watchedThemeName) {
                return;
            }
            // Keep the last successfully loaded theme active if the file is temporarily missing
            if (!fs.existsSync(themeFile)) {
                return;
            }
            try {
                // Reload the theme from disk and refresh the registry cache
                const reloadedTheme = loadThemeFromPath(themeFile);
                registeredThemes.set(watchedThemeName, reloadedTheme);
                setGlobalTheme(reloadedTheme);
                // Notify callback (to invalidate UI)
                if (onThemeChangeCallback) {
                    onThemeChangeCallback();
                }
            }
            catch (_error) {
                // Ignore errors (file might be in invalid state while being edited)
            }
        }, 100);
    };
    themeWatcher =
        watchWithErrorHandler(customThemesDir, (_eventType, filename) => {
            if (currentThemeName !== watchedThemeName) {
                return;
            }
            if (!filename) {
                scheduleReload();
                return;
            }
            if (filename !== watchedFileName) {
                return;
            }
            scheduleReload();
        }, () => {
            closeWatcher(themeWatcher);
            themeWatcher = undefined;
        }) ?? undefined;
}
export function stopThemeWatcher() {
    if (themeReloadTimer) {
        clearTimeout(themeReloadTimer);
        themeReloadTimer = undefined;
    }
    closeWatcher(themeWatcher);
    themeWatcher = undefined;
}
// ============================================================================
// HTML Export Helpers
// ============================================================================
/**
 * Get resolved theme colors as CSS-compatible hex strings.
 * Used by HTML export to generate CSS custom properties.
 */
export function getResolvedThemeColors(themeName) {
    const colors = loadTheme(themeName ?? currentThemeName ?? SYSTEM_THEME_NAME).colors;
    return Object.fromEntries(Object.entries(colors).map(([token, color]) => [token, colorToHex(color)]));
}
/**
 * Check if a theme is a "light" theme (for CSS that needs light/dark variants).
 */
export function isLightTheme(themeName) {
    return loadTheme(themeName ?? currentThemeName ?? SYSTEM_THEME_NAME).appearance === "light";
}
/**
 * Get explicit export colors from theme JSON, if specified.
 * Returns undefined for each color that isn't explicitly set.
 */
export function getThemeExportColors(themeName) {
    const name = themeName ?? currentThemeName ?? SYSTEM_THEME_NAME;
    if (name === SYSTEM_THEME_NAME)
        return {};
    try {
        const themeJson = loadThemeJson(name);
        const exportSection = themeJson.export;
        if (!exportSection)
            return {};
        const vars = themeJson.vars ?? {};
        // Export colors end up in CSS, which understands hex and oklch() values directly but not okhsl().
        const resolve = (value) => {
            if (value === undefined)
                return undefined;
            const resolved = resolveVarRefs(value, vars);
            if (typeof resolved === "number")
                return colorToHex(indexedColor(resolved));
            if (resolved === "")
                return undefined;
            if (/^okhsl\(/i.test(resolved))
                return colorToHex(parseColor(resolved));
            return resolved;
        };
        return {
            pageBg: resolve(exportSection.pageBg),
            cardBg: resolve(exportSection.cardBg),
            infoBg: resolve(exportSection.infoBg),
        };
    }
    catch {
        return {};
    }
}
let cachedHighlightThemeFor;
let cachedCliHighlightTheme;
function buildCliHighlightTheme(t) {
    return {
        keyword: (s) => t.fg("syntaxKeyword", s),
        built_in: (s) => t.fg("syntaxType", s),
        literal: (s) => t.fg("syntaxNumber", s),
        number: (s) => t.fg("syntaxNumber", s),
        regexp: (s) => t.fg("syntaxString", s),
        string: (s) => t.fg("syntaxString", s),
        comment: (s) => t.fg("syntaxComment", s),
        doctag: (s) => t.fg("syntaxComment", s),
        meta: (s) => t.fg("muted", s),
        function: (s) => t.fg("syntaxFunction", s),
        title: (s) => t.fg("syntaxFunction", s),
        class: (s) => t.fg("syntaxType", s),
        type: (s) => t.fg("syntaxType", s),
        tag: (s) => t.fg("syntaxPunctuation", s),
        name: (s) => t.fg("syntaxKeyword", s),
        attr: (s) => t.fg("syntaxVariable", s),
        variable: (s) => t.fg("syntaxVariable", s),
        params: (s) => t.fg("syntaxVariable", s),
        operator: (s) => t.fg("syntaxOperator", s),
        punctuation: (s) => t.fg("syntaxPunctuation", s),
        emphasis: (s) => t.italic(s),
        strong: (s) => t.bold(s),
        link: (s) => t.underline(s),
        addition: (s) => t.fg("toolDiffAdded", s),
        deletion: (s) => t.fg("toolDiffRemoved", s),
    };
}
function getCliHighlightTheme(t) {
    if (cachedHighlightThemeFor !== t || !cachedCliHighlightTheme) {
        cachedHighlightThemeFor = t;
        cachedCliHighlightTheme = buildCliHighlightTheme(t);
    }
    return cachedCliHighlightTheme;
}
/**
 * Highlight code with syntax coloring based on file extension or language.
 * Returns array of highlighted lines.
 */
export function highlightCode(code, lang) {
    // Validate language before highlighting to avoid stderr spam from cli-highlight
    const validLang = lang && supportsLanguage(lang) ? lang : undefined;
    // Skip highlighting when no valid language is specified. cli-highlight's
    // auto-detection is unreliable and can misidentify prose as AppleScript,
    // LiveCodeServer, etc., coloring random English words as keywords.
    if (!validLang) {
        return code.split("\n").map((line) => theme.fg("mdCodeBlock", line));
    }
    const opts = {
        language: validLang,
        ignoreIllegals: true,
        theme: getCliHighlightTheme(theme),
    };
    try {
        return highlight(code, opts).split("\n");
    }
    catch {
        return code.split("\n");
    }
}
/**
 * Get language identifier from file path extension.
 */
export function getLanguageFromPath(filePath) {
    const ext = filePath.split(".").pop()?.toLowerCase();
    if (!ext)
        return undefined;
    const extToLang = {
        ts: "typescript",
        tsx: "typescript",
        js: "javascript",
        jsx: "javascript",
        mjs: "javascript",
        cjs: "javascript",
        py: "python",
        rb: "ruby",
        rs: "rust",
        go: "go",
        java: "java",
        kt: "kotlin",
        swift: "swift",
        c: "c",
        h: "c",
        cpp: "cpp",
        cc: "cpp",
        cxx: "cpp",
        hpp: "cpp",
        cs: "csharp",
        php: "php",
        sh: "bash",
        bash: "bash",
        zsh: "bash",
        fish: "fish",
        ps1: "powershell",
        sql: "sql",
        html: "html",
        htm: "html",
        css: "css",
        scss: "scss",
        sass: "sass",
        less: "less",
        json: "json",
        yaml: "yaml",
        yml: "yaml",
        toml: "toml",
        xml: "xml",
        md: "markdown",
        markdown: "markdown",
        dockerfile: "dockerfile",
        makefile: "makefile",
        cmake: "cmake",
        lua: "lua",
        perl: "perl",
        r: "r",
        scala: "scala",
        clj: "clojure",
        ex: "elixir",
        exs: "elixir",
        erl: "erlang",
        hs: "haskell",
        ml: "ocaml",
        vim: "vim",
        graphql: "graphql",
        proto: "protobuf",
        tf: "hcl",
        hcl: "hcl",
    };
    return extToLang[ext];
}
export function getMarkdownTheme() {
    return {
        heading: (text) => theme.fg("mdHeading", text),
        link: (text) => theme.fg("mdLink", text),
        linkUrl: (text) => theme.fg("mdLinkUrl", text),
        code: (text) => theme.fg("mdCode", text),
        codeBlock: (text) => theme.fg("mdCodeBlock", text),
        codeBlockBorder: (text) => theme.fg("mdCodeBlockBorder", text),
        quote: (text) => theme.fg("mdQuote", text),
        quoteBorder: (text) => theme.fg("mdQuoteBorder", text),
        hr: (text) => theme.fg("mdHr", text),
        listBullet: (text) => theme.fg("mdListBullet", text),
        bold: (text) => theme.bold(text),
        italic: (text) => theme.italic(text),
        underline: (text) => theme.underline(text),
        strikethrough: (text) => theme.strikethrough(text),
        highlightCode: (code, lang) => {
            // Validate language before highlighting to avoid stderr spam from cli-highlight
            const validLang = lang && supportsLanguage(lang) ? lang : undefined;
            // Skip highlighting when no valid language is specified. cli-highlight's
            // auto-detection is unreliable and can misidentify prose as AppleScript,
            // LiveCodeServer, etc., coloring random English words as keywords.
            if (!validLang) {
                return code.split("\n").map((line) => theme.fg("mdCodeBlock", line));
            }
            const opts = {
                language: validLang,
                ignoreIllegals: true,
                theme: getCliHighlightTheme(theme),
            };
            try {
                return highlight(code, opts).split("\n");
            }
            catch {
                return code.split("\n").map((line) => theme.fg("mdCodeBlock", line));
            }
        },
    };
}
export function getSelectListTheme() {
    return {
        selectedPrefix: (text) => theme.fg("accent", text),
        selectedText: (text) => theme.fg("accent", text),
        description: (text) => theme.fg("muted", text),
        scrollInfo: (text) => theme.fg("muted", text),
        noMatch: (text) => theme.fg("muted", text),
    };
}
export function getEditorTheme() {
    return {
        borderColor: (text) => theme.fg("borderMuted", text),
        selectList: getSelectListTheme(),
    };
}
export function getSettingsListTheme() {
    return {
        label: (text, selected) => (selected ? theme.fg("accent", text) : text),
        value: (text, selected) => (selected ? theme.fg("accent", text) : theme.fg("muted", text)),
        description: (text) => theme.fg("dim", text),
        cursor: theme.fg("accent", "→ "),
        hint: (text) => theme.fg("dim", text),
    };
}
//# sourceMappingURL=theme.js.map