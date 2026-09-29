/**
 * Presentation for the grep tool.
 *
 * Renderers live apart from the implementation so a process that only displays tool output does not
 * load the execution path or its typebox parameter schema. `grep.ts` spreads these into its
 * definition, so the tool's public shape is unchanged.
 */
import { Text } from "../../../../../pi-tui.mjs";
import { keyHint } from "../../../modes/interactive/components/keybinding-hints.js";
import { getTextOutput, invalidArgText, shortenPath, str } from "../render-utils.js";
import { DEFAULT_MAX_BYTES, formatSize } from "../truncate.js";
function formatGrepCall(args, theme) {
    const pattern = str(args?.pattern);
    const rawPath = str(args?.path);
    const path = rawPath !== null ? shortenPath(rawPath || ".") : null;
    const glob = str(args?.glob);
    const limit = args?.limit;
    const invalidArg = invalidArgText(theme);
    let text = theme.fg("toolTitle", theme.bold("grep")) +
        " " +
        (pattern === null ? invalidArg : theme.fg("accent", `/${pattern || ""}/`)) +
        theme.fg("toolOutput", ` in ${path === null ? invalidArg : path}`);
    if (glob)
        text += theme.fg("toolOutput", ` (${glob})`);
    if (limit !== undefined)
        text += theme.fg("toolOutput", ` limit ${limit}`);
    return text;
}
function formatGrepResult(result, options, theme, showImages) {
    const output = getTextOutput(result, showImages).trim();
    let text = "";
    if (output) {
        const lines = output.split("\n");
        const maxLines = options.expanded ? lines.length : 15;
        const displayLines = lines.slice(0, maxLines);
        const remaining = lines.length - maxLines;
        text += `\n${displayLines.map((line) => theme.fg("toolOutput", line)).join("\n")}`;
        if (remaining > 0) {
            text += `${theme.fg("muted", `\n... (${remaining} more lines,`)} ${keyHint("app.tools.expand", "to expand")}${theme.fg("muted", ")")}`;
        }
    }
    const matchLimit = result.details?.matchLimitReached;
    const truncation = result.details?.truncation;
    const linesTruncated = result.details?.linesTruncated;
    if (matchLimit || truncation?.truncated || linesTruncated) {
        const warnings = [];
        if (matchLimit)
            warnings.push(`${matchLimit} matches limit`);
        if (truncation?.truncated)
            warnings.push(`${formatSize(truncation.maxBytes ?? DEFAULT_MAX_BYTES)} limit`);
        if (linesTruncated)
            warnings.push("some lines truncated");
        text += `\n${theme.fg("warning", `[Truncated: ${warnings.join(", ")}]`)}`;
    }
    return text;
}
export const grepRenderers = {
    renderCall(args, theme, context) {
        const text = context.lastComponent ?? new Text("", 0, 0);
        text.setText(formatGrepCall(args, theme));
        return text;
    },
    renderResult(result, options, theme, context) {
        const text = context.lastComponent ?? new Text("", 0, 0);
        text.setText(formatGrepResult(result, options, theme, context.showImages));
        return text;
    },
};
//# sourceMappingURL=grep.js.map