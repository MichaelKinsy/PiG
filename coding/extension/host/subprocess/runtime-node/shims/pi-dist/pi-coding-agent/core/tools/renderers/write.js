/**
 * Presentation for the write tool.
 *
 * Renderers live apart from the implementation so a process that only displays tool output does not
 * load the execution path or its typebox parameter schema. `write.ts` spreads these into its
 * definition, so the tool's public shape is unchanged.
 */
import { Container, Text } from "../../../../../pi-tui.mjs";
import { keyHint } from "../../../modes/interactive/components/keybinding-hints.js";
import { CodeHighlighter, getLanguageFromPath, highlightCode, } from "../../../modes/interactive/theme/theme.js";
import { normalizeDisplayText, renderToolPath, replaceTabs, str } from "../render-utils.js";
class WriteCallRenderComponent extends Text {
    // Line by line highlighting of a call whose content is still streaming.
    cache;
    // A streamed call highlights its first lines on every delta, which stop changing once the file is longer, and a
    // complete call highlights the whole file on every update and expand toggle. Either way the row reuses the last
    // highlighting; it also holds the only copy of a complete call's displayed source.
    highlighter = new CodeHighlighter();
    constructor() {
        super("", 0, 0);
    }
}
const WRITE_PARTIAL_FULL_HIGHLIGHT_LINES = 50;
function highlightSingleLine(line, lang) {
    const highlighted = highlightCode(line, lang);
    return highlighted[0] ?? "";
}
function refreshWriteHighlightPrefix(cache, highlighter) {
    const prefixCount = Math.min(WRITE_PARTIAL_FULL_HIGHLIGHT_LINES, cache.normalizedLines.length);
    if (prefixCount === 0)
        return;
    const prefixSource = cache.normalizedLines.slice(0, prefixCount).join("\n");
    const prefixHighlighted = highlighter.highlight(prefixSource, cache.lang);
    for (let i = 0; i < prefixCount; i++) {
        cache.highlightedLines[i] =
            prefixHighlighted[i] ?? highlightSingleLine(cache.normalizedLines[i] ?? "", cache.lang);
    }
}
function rebuildWriteHighlightCacheFull(rawPath, fileContent) {
    const lang = rawPath ? getLanguageFromPath(rawPath) : undefined;
    if (!lang)
        return undefined;
    const displayContent = normalizeDisplayText(fileContent);
    const normalized = replaceTabs(displayContent);
    return {
        rawPath,
        lang,
        rawContent: fileContent,
        normalizedLines: normalized.split("\n"),
        highlightedLines: highlightCode(normalized, lang),
    };
}
function updateWriteHighlightCacheIncremental(cache, rawPath, fileContent, highlighter) {
    const lang = rawPath ? getLanguageFromPath(rawPath) : undefined;
    if (!lang)
        return undefined;
    if (!cache)
        return rebuildWriteHighlightCacheFull(rawPath, fileContent);
    if (cache.lang !== lang || cache.rawPath !== rawPath || !fileContent.startsWith(cache.rawContent)) {
        return rebuildWriteHighlightCacheFull(rawPath, fileContent);
    }
    if (fileContent.length === cache.rawContent.length)
        return cache;
    const deltaRaw = fileContent.slice(cache.rawContent.length);
    const deltaDisplay = normalizeDisplayText(deltaRaw);
    const deltaNormalized = replaceTabs(deltaDisplay);
    cache.rawContent = fileContent;
    if (cache.normalizedLines.length === 0) {
        cache.normalizedLines.push("");
        cache.highlightedLines.push("");
    }
    const segments = deltaNormalized.split("\n");
    const lastIndex = cache.normalizedLines.length - 1;
    cache.normalizedLines[lastIndex] += segments[0];
    cache.highlightedLines[lastIndex] = highlightSingleLine(cache.normalizedLines[lastIndex], cache.lang);
    for (let i = 1; i < segments.length; i++) {
        cache.normalizedLines.push(segments[i]);
        cache.highlightedLines.push(highlightSingleLine(segments[i], cache.lang));
    }
    refreshWriteHighlightPrefix(cache, highlighter);
    return cache;
}
function trimTrailingEmptyLines(lines) {
    let end = lines.length;
    while (end > 0 && lines[end - 1] === "") {
        end--;
    }
    return lines.slice(0, end);
}
function formatWriteCall(args, options, theme, cache, highlighter, cwd) {
    const rawPath = str(args?.file_path ?? args?.path);
    const fileContent = str(args?.content);
    const pathDisplay = renderToolPath(rawPath, theme, cwd);
    let text = `${theme.fg("toolTitle", theme.bold("write"))} ${pathDisplay}`;
    if (fileContent === null) {
        text += `\n\n${theme.fg("error", "[invalid content arg - expected string]")}`;
    }
    else if (fileContent) {
        const lang = rawPath ? getLanguageFromPath(rawPath) : undefined;
        const renderedLines = lang
            ? (cache?.highlightedLines ?? highlighter.highlight(replaceTabs(normalizeDisplayText(fileContent)), lang))
            : normalizeDisplayText(fileContent).split("\n");
        const lines = trimTrailingEmptyLines(renderedLines);
        const totalLines = lines.length;
        const maxLines = options.expanded ? lines.length : 10;
        const displayLines = lines.slice(0, maxLines);
        const remaining = lines.length - maxLines;
        text += `\n\n${displayLines.map((line) => (lang ? line : theme.fg("toolOutput", replaceTabs(line)))).join("\n")}`;
        if (remaining > 0) {
            text += `${theme.fg("muted", `\n... (${remaining} more lines, ${totalLines} total,`)} ${keyHint("app.tools.expand", "to expand")}${theme.fg("muted", ")")}`;
        }
    }
    return text;
}
function formatWriteResult(result, theme) {
    if (!result.isError) {
        return undefined;
    }
    const output = result.content
        .filter((c) => c.type === "text")
        .map((c) => c.text || "")
        .join("\n");
    if (!output) {
        return undefined;
    }
    return `\n${theme.fg("error", output)}`;
}
export const writeRenderers = {
    renderCall(args, theme, context) {
        const renderArgs = args;
        const rawPath = str(renderArgs?.file_path ?? renderArgs?.path);
        const fileContent = str(renderArgs?.content);
        const component = context.lastComponent ?? new WriteCallRenderComponent();
        component.cache =
            fileContent !== null && !context.argsComplete
                ? updateWriteHighlightCacheIncremental(component.cache, rawPath, fileContent, component.highlighter)
                : undefined;
        component.setText(formatWriteCall(renderArgs, { expanded: context.expanded, isPartial: context.isPartial }, theme, component.cache, component.highlighter, context.cwd));
        return component;
    },
    renderResult(result, _options, theme, context) {
        const output = formatWriteResult({ ...result, isError: context.isError }, theme);
        if (!output) {
            const component = context.lastComponent ?? new Container();
            component.clear();
            return component;
        }
        const text = context.lastComponent ?? new Text("", 0, 0);
        text.setText(output);
        return text;
    },
};
//# sourceMappingURL=write.js.map