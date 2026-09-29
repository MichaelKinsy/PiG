/**
 * Presentation for the ls tool.
 *
 * Renderers live apart from the implementation so a process that only displays tool output does not
 * load the execution path or its typebox parameter schema. `ls.ts` spreads these into its
 * definition, so the tool's public shape is unchanged.
 */
import { Text } from "../../../../../pi-tui.mjs";
import { keyHint } from "../../../modes/interactive/components/keybinding-hints.js";
import { getTextOutput, renderToolPath, str } from "../render-utils.js";
import { DEFAULT_MAX_BYTES, formatSize } from "../truncate.js";
function formatLsCall(args, theme, cwd) {
    const limit = args?.limit;
    const pathDisplay = renderToolPath(str(args?.path), theme, cwd, { emptyFallback: "." });
    let text = `${theme.fg("toolTitle", theme.bold("ls"))} ${pathDisplay}`;
    if (limit !== undefined) {
        text += theme.fg("toolOutput", ` (limit ${limit})`);
    }
    return text;
}
function formatLsResult(result, options, theme, showImages) {
    const output = getTextOutput(result, showImages).trim();
    let text = "";
    if (output) {
        const lines = output.split("\n");
        const maxLines = options.expanded ? lines.length : 20;
        const displayLines = lines.slice(0, maxLines);
        const remaining = lines.length - maxLines;
        text += `\n${displayLines.map((line) => theme.fg("toolOutput", line)).join("\n")}`;
        if (remaining > 0) {
            text += `${theme.fg("muted", `\n... (${remaining} more lines,`)} ${keyHint("app.tools.expand", "to expand")}${theme.fg("muted", ")")}`;
        }
    }
    const entryLimit = result.details?.entryLimitReached;
    const truncation = result.details?.truncation;
    if (entryLimit || truncation?.truncated) {
        const warnings = [];
        if (entryLimit)
            warnings.push(`${entryLimit} entries limit`);
        if (truncation?.truncated)
            warnings.push(`${formatSize(truncation.maxBytes ?? DEFAULT_MAX_BYTES)} limit`);
        text += `\n${theme.fg("warning", `[Truncated: ${warnings.join(", ")}]`)}`;
    }
    return text;
}
export const lsRenderers = {
    renderCall(args, theme, context) {
        const text = context.lastComponent ?? new Text("", 0, 0);
        text.setText(formatLsCall(args, theme, context.cwd));
        return text;
    },
    renderResult(result, options, theme, context) {
        const text = context.lastComponent ?? new Text("", 0, 0);
        text.setText(formatLsResult(result, options, theme, context.showImages));
        return text;
    },
};
//# sourceMappingURL=ls.js.map