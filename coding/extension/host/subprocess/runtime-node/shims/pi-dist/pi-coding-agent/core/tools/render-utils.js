import * as os from "node:os";
import { pathToFileURL } from "node:url";
import { getCapabilities, getImageDimensions, hyperlink, imageFallback } from "../../../../pi-tui.mjs";
import { stripAnsi } from "../../utils/ansi.js";
import { resolvePath } from "../../utils/paths.js";
import { sanitizeBinaryOutput } from "../../utils/shell.js";
export function shortenPath(path) {
    if (typeof path !== "string")
        return "";
    const home = os.homedir();
    if (path.startsWith(home)) {
        return `~${path.slice(home.length)}`;
    }
    return path;
}
export function linkPath(styledText, rawPath, cwd) {
    if (!getCapabilities().hyperlinks)
        return styledText;
    const absolutePath = resolvePath(rawPath, cwd);
    return hyperlink(styledText, pathToFileURL(absolutePath).href);
}
export function str(value) {
    if (typeof value === "string")
        return value;
    if (value == null)
        return "";
    return null;
}
export function replaceTabs(text) {
    return text.replace(/\t/g, "   ");
}
export function normalizeDisplayText(text) {
    return text.replace(/\r/g, "");
}
export function getTextOutput(result, showImages) {
    if (!result)
        return "";
    const textBlocks = result.content.filter((c) => c.type === "text");
    const imageBlocks = result.content.filter((c) => c.type === "image");
    let output = textBlocks.map((c) => sanitizeBinaryOutput(stripAnsi(c.text || "")).replace(/\r/g, "")).join("\n");
    const caps = getCapabilities();
    if (imageBlocks.length > 0 && (!caps.images || !showImages)) {
        const imageIndicators = imageBlocks
            .map((img) => {
            const mimeType = img.mimeType ?? "image/unknown";
            const dims = img.data && img.mimeType ? (getImageDimensions(img.data, img.mimeType) ?? undefined) : undefined;
            return imageFallback(mimeType, dims);
        })
            .join("\n");
        output = output ? `${output}\n${imageIndicators}` : imageIndicators;
    }
    return output;
}
const COLLAPSED_ARGS_CHARS = 100;
/**
 * Generic tool call header: the title followed by the arguments. Collapsed, they are `key=value`
 * pairs on the title line, cut to {@link COLLAPSED_ARGS_CHARS}. Expanded, each is a `key: value`
 * line below the title, with strings shown raw and continuation lines indented.
 */
export function formatToolCallWithArgs(title, args, theme, expanded) {
    const header = theme.fg("toolTitle", theme.bold(title));
    if (args == null)
        return header;
    const entries = typeof args === "object" && !Array.isArray(args)
        ? Object.entries(args)
        : [["args", args]];
    if (entries.length === 0)
        return header;
    if (expanded) {
        const lines = entries.map(([key, value]) => {
            const text = typeof value === "string" ? value : (JSON.stringify(value, null, 2) ?? String(value));
            return `  ${key}: ${replaceTabs(text).replace(/\r/g, "").split("\n").join("\n    ")}`;
        });
        return `${header}\n${theme.fg("muted", lines.join("\n"))}`;
    }
    const pairs = entries.map(([key, value]) => `${key}=${JSON.stringify(value) ?? String(value)}`).join(" ");
    const preview = pairs.length > COLLAPSED_ARGS_CHARS ? `${pairs.slice(0, COLLAPSED_ARGS_CHARS - 3)}...` : pairs;
    return `${header} ${theme.fg("muted", preview)}`;
}
export function invalidArgText(theme) {
    return theme.fg("error", "[invalid arg]");
}
export function renderToolPath(rawPath, theme, cwd, options) {
    if (rawPath === null)
        return invalidArgText(theme);
    const value = rawPath || options?.emptyFallback;
    if (!value)
        return theme.fg("toolOutput", "...");
    return linkPath(theme.fg("accent", shortenPath(value)), value, cwd);
}
//# sourceMappingURL=render-utils.js.map