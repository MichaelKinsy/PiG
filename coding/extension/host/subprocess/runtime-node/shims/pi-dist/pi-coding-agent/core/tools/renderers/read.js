/**
 * Presentation for the read tool.
 *
 * Renderers live apart from the implementation so a process that only displays tool output does not
 * load the execution path or its typebox parameter schema. `read.ts` spreads these into its
 * definition, so the tool's public shape is unchanged.
 */
import { basename, dirname, isAbsolute, relative, resolve as resolvePath, sep } from "node:path";
import { Text } from "../../../../../pi-tui.mjs";
import { getReadmePath } from "../../../config.js";
import { keyHint, keyText } from "../../../modes/interactive/components/keybinding-hints.js";
import { getLanguageFromPath, highlightCode } from "../../../modes/interactive/theme/theme.js";
import { formatPathRelativeToCwdOrAbsolute } from "../../../utils/paths.js";
import { resolveToCwd } from "../path-utils.js";
import { getTextOutput, renderToolPath, replaceTabs, str } from "../render-utils.js";
import { DEFAULT_MAX_BYTES, DEFAULT_MAX_LINES, formatSize } from "../truncate.js";
const COMPACT_RESOURCE_FILE_NAMES = new Set(["AGENTS.override.md", "AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"]);
function formatReadLineRange(args, theme) {
    if (args?.offset === undefined && args?.limit === undefined)
        return "";
    const startLine = args.offset ?? 1;
    const endLine = args.limit !== undefined ? startLine + args.limit - 1 : "";
    return theme.fg("warning", `:${startLine}${endLine ? `-${endLine}` : ""}`);
}
function formatReadCall(args, theme, cwd) {
    const pathDisplay = renderToolPath(str(args?.file_path ?? args?.path), theme, cwd);
    return `${theme.fg("toolTitle", theme.bold("read"))} ${pathDisplay}${formatReadLineRange(args, theme)}`;
}
function trimTrailingEmptyLines(lines) {
    let end = lines.length;
    while (end > 0 && lines[end - 1] === "") {
        end--;
    }
    return lines.slice(0, end);
}
function toPosixPath(filePath) {
    return filePath.split(sep).join("/");
}
function getPiDocsClassification(absolutePath) {
    const packageRoot = dirname(getReadmePath());
    const relativePath = relative(resolvePath(packageRoot), resolvePath(absolutePath));
    if (relativePath === "" ||
        relativePath === ".." ||
        relativePath.startsWith(`..${sep}`) ||
        isAbsolute(relativePath)) {
        return undefined;
    }
    const label = toPosixPath(relativePath);
    if (label === "README.md" || label.startsWith("docs/") || label.startsWith("examples/")) {
        return { kind: "docs", label };
    }
    return undefined;
}
function getCompactReadClassification(args, cwd) {
    const rawPath = str(args?.file_path ?? args?.path);
    if (!rawPath)
        return undefined;
    const absolutePath = resolveToCwd(rawPath, cwd);
    const fileName = basename(absolutePath);
    if (fileName === "SKILL.md") {
        return { kind: "skill", label: basename(dirname(absolutePath)) || fileName };
    }
    const docsClassification = getPiDocsClassification(absolutePath);
    if (docsClassification)
        return docsClassification;
    if (COMPACT_RESOURCE_FILE_NAMES.has(fileName)) {
        return { kind: "resource", label: formatPathRelativeToCwdOrAbsolute(absolutePath, cwd) };
    }
    return undefined;
}
function formatCompactReadCall(classification, args, theme) {
    const expandHint = theme.fg("dim", ` (${keyText("app.tools.expand")} to expand)`);
    if (classification.kind === "skill") {
        return (theme.fg("customMessageLabel", `\x1b[1m[skill]\x1b[22m `) +
            theme.fg("customMessageText", classification.label) +
            formatReadLineRange(args, theme) +
            expandHint);
    }
    return (theme.fg("toolTitle", theme.bold(`read ${classification.kind}`)) +
        " " +
        theme.fg("accent", classification.label) +
        formatReadLineRange(args, theme) +
        expandHint);
}
function formatReadResult(args, result, options, theme, showImages, _cwd, isError) {
    if (!options.expanded && !isError) {
        return "";
    }
    const rawPath = str(args?.file_path ?? args?.path);
    const output = getTextOutput(result, showImages);
    const lang = !isError && rawPath ? getLanguageFromPath(rawPath) : undefined;
    const renderedLines = lang ? highlightCode(replaceTabs(output), lang) : output.split("\n");
    const lines = trimTrailingEmptyLines(renderedLines);
    const maxLines = options.expanded ? lines.length : 10;
    const displayLines = lines.slice(0, maxLines);
    const remaining = lines.length - maxLines;
    let text = `\n${displayLines.map((line) => (lang ? replaceTabs(line) : theme.fg("toolOutput", replaceTabs(line)))).join("\n")}`;
    if (remaining > 0) {
        text += `${theme.fg("muted", `\n... (${remaining} more lines,`)} ${keyHint("app.tools.expand", "to expand")}${theme.fg("muted", ")")}`;
    }
    const truncation = result.details?.truncation;
    if (truncation?.truncated) {
        if (truncation.firstLineExceedsLimit) {
            text += `\n${theme.fg("warning", `[First line exceeds ${formatSize(truncation.maxBytes ?? DEFAULT_MAX_BYTES)} limit]`)}`;
        }
        else if (truncation.truncatedBy === "lines") {
            text += `\n${theme.fg("warning", `[Truncated: showing ${truncation.outputLines} of ${truncation.totalLines} lines (${truncation.maxLines ?? DEFAULT_MAX_LINES} line limit)]`)}`;
        }
        else {
            text += `\n${theme.fg("warning", `[Truncated: ${truncation.outputLines} lines shown (${formatSize(truncation.maxBytes ?? DEFAULT_MAX_BYTES)} limit)]`)}`;
        }
    }
    return text;
}
export const readRenderers = {
    renderCall(rawArgs, theme, context) {
        const args = rawArgs;
        const text = context.lastComponent ?? new Text("", 0, 0);
        const classification = !context.expanded ? getCompactReadClassification(args, context.cwd) : undefined;
        text.setText(classification ? formatCompactReadCall(classification, args, theme) : formatReadCall(args, theme, context.cwd));
        return text;
    },
    renderResult(result, options, theme, context) {
        const text = context.lastComponent ?? new Text("", 0, 0);
        text.setText(formatReadResult(context.args, result, options, theme, context.showImages, context.cwd, context.isError));
        return text;
    },
};
//# sourceMappingURL=read.js.map