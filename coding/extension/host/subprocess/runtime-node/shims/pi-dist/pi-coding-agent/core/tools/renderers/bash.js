/**
 * Presentation for the shell tools.
 *
 * Renderers live apart from the implementation so a process that only displays tool output does not
 * load the execution path or its typebox parameter schema. `bash.ts` spreads these into the shell
 * tool definition, so the tool's public shape is unchanged.
 */
import { Container, Text, truncateToWidth } from "../../../../../pi-tui.mjs";
import { keyHint } from "../../../modes/interactive/components/keybinding-hints.js";
import { truncateToVisualLines } from "../../../modes/interactive/components/visual-truncate.js";
import { theme } from "../../../modes/interactive/theme/theme.js";
import { getTextOutput, invalidArgText, str } from "../render-utils.js";
import { DEFAULT_MAX_BYTES, formatSize } from "../truncate.js";
const BASH_PREVIEW_LINES = 5;
export const BASH_UPDATE_THROTTLE_MS = 100;
class BashResultRenderComponent extends Container {
    state = {
        cachedWidth: undefined,
        cachedLines: undefined,
        cachedSkipped: undefined,
    };
}
function formatDuration(ms) {
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
function formatShellCall(args, prompt) {
    const command = str(args?.command);
    const timeout = args?.timeout;
    const timeoutSuffix = timeout ? theme.fg("muted", ` (timeout ${timeout}s)`) : "";
    const commandDisplay = command === null ? invalidArgText(theme) : command ? command : theme.fg("toolOutput", "...");
    return theme.fg("toolTitle", theme.bold(`${prompt} ${commandDisplay}`)) + timeoutSuffix;
}
function rebuildBashResultRenderComponent(component, result, options, showImages, startedAt, endedAt) {
    const state = component.state;
    component.clear();
    let output = getTextOutput(result, showImages).trim();
    const truncation = result.details?.truncation;
    const fullOutputPath = result.details?.fullOutputPath;
    if (!options.isPartial && truncation?.truncated && fullOutputPath && output.endsWith("]")) {
        const footerStart = output.lastIndexOf("\n\n[");
        if (footerStart !== -1 && output.slice(footerStart).includes(fullOutputPath)) {
            output = output.slice(0, footerStart).trimEnd();
        }
    }
    if (output) {
        const styledOutput = output
            .split("\n")
            .map((line) => theme.fg("toolOutput", line))
            .join("\n");
        if (options.expanded) {
            component.addChild(new Text(`\n${styledOutput}`, 0, 0));
        }
        else {
            component.addChild({
                render: (width) => {
                    if (state.cachedLines === undefined || state.cachedWidth !== width) {
                        const preview = truncateToVisualLines(styledOutput, BASH_PREVIEW_LINES, width);
                        state.cachedLines = preview.visualLines;
                        state.cachedSkipped = preview.skippedCount;
                        state.cachedWidth = width;
                    }
                    if (state.cachedSkipped && state.cachedSkipped > 0) {
                        const hint = theme.fg("muted", `... (${state.cachedSkipped} earlier lines,`) +
                            ` ${keyHint("app.tools.expand", "to expand")}${theme.fg("muted", ")")}`;
                        return ["", truncateToWidth(hint, width, "..."), ...(state.cachedLines ?? [])];
                    }
                    return ["", ...(state.cachedLines ?? [])];
                },
                invalidate: () => {
                    state.cachedWidth = undefined;
                    state.cachedLines = undefined;
                    state.cachedSkipped = undefined;
                },
            });
        }
    }
    if (truncation?.truncated || fullOutputPath) {
        const warnings = [];
        if (fullOutputPath) {
            warnings.push(`Full output: ${fullOutputPath}`);
        }
        if (truncation?.truncated) {
            if (truncation.truncatedBy === "lines") {
                warnings.push(`Truncated: showing ${truncation.outputLines} of ${truncation.totalLines} lines`);
            }
            else {
                warnings.push(`Truncated: ${truncation.outputLines} lines shown (${formatSize(truncation.maxBytes ?? DEFAULT_MAX_BYTES)} limit)`);
            }
        }
        component.addChild(new Text(`\n${theme.fg("warning", `[${warnings.join(". ")}]`)}`, 0, 0));
    }
    if (startedAt !== undefined) {
        const label = options.isPartial ? "Elapsed" : "Took";
        const endTime = endedAt ?? Date.now();
        component.addChild(new Text(`\n${theme.fg("muted", `${label} ${formatDuration(endTime - startedAt)}`)}`, 0, 0));
    }
}
/** Shell renderers are shared by bash and powershell, which differ only in the prompt they display. */
export function createShellRenderers(prompt) {
    return {
        renderCall(args, _theme, context) {
            const state = context.state;
            if (context.executionStarted && state.startedAt === undefined) {
                state.startedAt = Date.now();
                state.endedAt = undefined;
            }
            const text = context.lastComponent ?? new Text("", 0, 0);
            text.setText(formatShellCall(args, prompt));
            return text;
        },
        renderResult(result, options, _theme, context) {
            const state = context.state;
            if (state.startedAt !== undefined && options.isPartial && !state.interval) {
                state.interval = setInterval(() => context.invalidate(), 1000);
            }
            if (!options.isPartial || context.isError) {
                state.endedAt ??= Date.now();
                if (state.interval) {
                    clearInterval(state.interval);
                    state.interval = undefined;
                }
            }
            const component = context.lastComponent ?? new BashResultRenderComponent();
            rebuildBashResultRenderComponent(component, result, options, context.showImages, state.startedAt, state.endedAt);
            component.invalidate();
            return component;
        },
    };
}
//# sourceMappingURL=bash.js.map