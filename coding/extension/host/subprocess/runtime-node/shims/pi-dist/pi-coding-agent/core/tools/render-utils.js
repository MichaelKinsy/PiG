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
function parseLineNumber(value) {
    if (typeof value === "string" && value.trim() !== "")
        value = Number(value);
    return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}
/** Format finite numbers or numeric strings as a read line range, omitting malformed ranges. */
export function formatReadLineRange(offset, limit) {
    // Strict schemas represent omitted optional arguments as null.
    if (offset == null && limit == null)
        return "";
    const start = offset == null ? 1 : parseLineNumber(offset);
    if (start === undefined)
        return "";
    if (limit == null)
        return `:${start}`;
    const count = parseLineNumber(limit);
    if (count === undefined)
        return "";
    const end = start + count - 1;
    return `:${start}${end ? `-${end}` : ""}`;
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
/** Duration as `4.2s` below a minute, then `1m 30s` and `2h 3m 4s`. */
export function formatToolDuration(ms) {
    const seconds = ms / 1000;
    if (seconds < 60)
        return `${seconds.toFixed(1)}s`;
    const totalSeconds = Math.floor(seconds);
    const minutes = Math.floor(totalSeconds / 60);
    const remainder = totalSeconds % 60;
    if (minutes < 60)
        return `${minutes}m ${remainder}s`;
    return `${Math.floor(minutes / 60)}h ${minutes % 60}m ${remainder}s`;
}
/** Start the row's clock once execution starts. Call from `renderCall`. */
export function startToolTimer(context) {
    const state = context.state;
    if (context.executionStarted && state.startedAt === undefined) {
        state.startedAt = Date.now();
        state.endedAt = undefined;
    }
}
/**
 * The row's timing line, from `renderResult`: `Elapsed 4.2s` while the result is partial, redrawn
 * every second, and `Took 4.2s` once it is final. Undefined when the row has no start time and no
 * recorded duration.
 */
export function toolTimerText(context, isPartial) {
    const state = context.state;
    if (state.startedAt !== undefined && isPartial && !state.interval) {
        state.interval = setInterval(() => context.invalidate(), 1000);
    }
    if (!isPartial || context.isError) {
        state.endedAt ??= Date.now();
        if (state.interval) {
            clearInterval(state.interval);
            state.interval = undefined;
        }
    }
    // A final result's recorded duration wins: it is monotonic and survives reloads. The renderer's own clock is the
    // fallback for live progress and for results stored without one.
    if (!isPartial && context.durationMs !== undefined)
        return `Took ${formatToolDuration(context.durationMs)}`;
    if (state.startedAt === undefined)
        return undefined;
    const endTime = state.endedAt ?? Date.now();
    return `${isPartial ? "Elapsed" : "Took"} ${formatToolDuration(endTime - state.startedAt)}`;
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