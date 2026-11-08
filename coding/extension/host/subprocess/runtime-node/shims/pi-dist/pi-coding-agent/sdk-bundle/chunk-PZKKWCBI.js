import {
  sanitizeBinaryOutput
} from "./chunk-BXLOEZ7M.js";
import {
  resolvePath,
  shortenHomePath
} from "./chunk-JGRZB36T.js";
import {
  __name
} from "./chunk-FIOAHQVQ.js";

// pi-dist/pi-coding-agent/utils/output-files.js
import { randomBytes } from "node:crypto";
import { createWriteStream } from "node:fs";
import { writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
var OUTPUT_FILE_MODE = 384;
function createOutputFilePath(prefix, extension) {
  return join(tmpdir(), `${prefix}-${randomBytes(8).toString("hex")}${extension}`);
}
__name(createOutputFilePath, "createOutputFilePath");
async function writeOutputFile(prefix, extension, data) {
  const path = createOutputFilePath(prefix, extension);
  await writeFile(path, data, { mode: OUTPUT_FILE_MODE, flag: "wx" });
  return path;
}
__name(writeOutputFile, "writeOutputFile");
function createOutputFileStream(prefix, extension) {
  const path = createOutputFilePath(prefix, extension);
  return { path, stream: createWriteStream(path, { mode: OUTPUT_FILE_MODE, flags: "wx" }) };
}
__name(createOutputFileStream, "createOutputFileStream");

// pi-dist/pi-coding-agent/modes/interactive/components/visual-truncate.js
import { Text, truncateToWidth } from "../../../pi-tui.mjs";
function truncateToVisualLines(text, maxVisualLines, width, paddingX = 0, keep = "end") {
  if (!text) {
    return { visualLines: [], skippedCount: 0 };
  }
  const tempText = new Text(text, paddingX, 0);
  const allVisualLines = tempText.render(width);
  if (allVisualLines.length <= maxVisualLines) {
    return { visualLines: allVisualLines, skippedCount: 0 };
  }
  const truncatedLines = keep === "start" ? allVisualLines.slice(0, maxVisualLines) : allVisualLines.slice(-maxVisualLines);
  const skippedCount = allVisualLines.length - maxVisualLines;
  return { visualLines: truncatedLines, skippedCount };
}
__name(truncateToVisualLines, "truncateToVisualLines");
var VisualLinePreview = class {
  static {
    __name(this, "VisualLinePreview");
  }
  options;
  cachedWidth;
  cachedLines;
  constructor(options) {
    this.options = options;
  }
  render(width) {
    if (this.cachedLines === void 0 || this.cachedWidth !== width) {
      const { text, maxVisualLines, keep, formatHint } = this.options;
      const preview = truncateToVisualLines(text, maxVisualLines, width, 0, keep);
      const lines = preview.visualLines;
      if (preview.skippedCount > 0) {
        const hint = truncateToWidth(formatHint(preview.skippedCount), width, "...");
        this.cachedLines = keep === "start" ? [...lines, hint] : [hint, ...lines];
      } else {
        this.cachedLines = lines;
      }
      this.cachedWidth = width;
    }
    return this.cachedLines;
  }
  invalidate() {
    this.cachedWidth = void 0;
    this.cachedLines = void 0;
  }
};

// pi-dist/pi-coding-agent/core/tools/render-utils.js
import { pathToFileURL } from "node:url";
import { getCapabilities, getImageDimensions, hyperlink, imageFallback } from "../../../pi-tui.mjs";

// pi-dist/pi-coding-agent/utils/ansi.js
var ST = "(?:\\u0007|\\u001B\\u005C|\\u009C)";
var OSC_START = "\\u001B\\]";
var CSI_START = "(?:(?:\\u001B\\[|\\u009B)[<=>]|[\\u001B\\u009B][[\\]()#;?]*)(?:\\d{1,4}(?:[;:]\\d{0,4})*)?";
var CSI_FINAL = "[\\dA-PR-TZcf-nq-uy=><~]";
var ansiRegex = new RegExp(`(?:${OSC_START}[\\s\\S]*?${ST})|${CSI_START}${CSI_FINAL}`, "g");
var unfinishedAnsiAtEndRegex = new RegExp(`(?:${OSC_START}(?:[^\\u0007\\u009C\\u001B]|\\u001B(?!\\\\))*|${CSI_START})$`);
var MAX_PENDING_ANSI_LENGTH = 256;
function splitIncompleteAnsiSuffix(value) {
  if (!value.includes("\x1B") && !value.includes("\x9B")) {
    return { complete: value, pending: "" };
  }
  const windowStart = Math.max(0, value.length - MAX_PENDING_ANSI_LENGTH);
  const match = unfinishedAnsiAtEndRegex.exec(value.slice(windowStart));
  if (!match) {
    return { complete: value, pending: "" };
  }
  const splitAt = windowStart + match.index;
  return { complete: value.slice(0, splitAt), pending: value.slice(splitAt) };
}
__name(splitIncompleteAnsiSuffix, "splitIncompleteAnsiSuffix");
function stripAnsi(value) {
  if (typeof value !== "string") {
    throw new TypeError(`Expected a \`string\`, got \`${typeof value}\``);
  }
  if (!value.includes("\x1B") && !value.includes("\x9B")) {
    return value;
  }
  return value.replace(ansiRegex, "");
}
__name(stripAnsi, "stripAnsi");

// pi-dist/pi-coding-agent/core/tools/render-utils.js
function shortenPath(path) {
  if (typeof path !== "string")
    return "";
  return shortenHomePath(path);
}
__name(shortenPath, "shortenPath");
function linkPath(styledText, rawPath, cwd) {
  if (!getCapabilities().hyperlinks)
    return styledText;
  const absolutePath = resolvePath(rawPath, cwd);
  return hyperlink(styledText, pathToFileURL(absolutePath).href);
}
__name(linkPath, "linkPath");
function str(value) {
  if (typeof value === "string")
    return value;
  if (value == null)
    return "";
  return null;
}
__name(str, "str");
function parseLineNumber(value) {
  if (typeof value === "string" && value.trim() !== "")
    value = Number(value);
  return typeof value === "number" && Number.isFinite(value) ? value : void 0;
}
__name(parseLineNumber, "parseLineNumber");
function formatReadLineRange(offset, limit) {
  if (offset == null && limit == null)
    return "";
  const start = offset == null ? 1 : parseLineNumber(offset);
  if (start === void 0)
    return "";
  if (limit == null)
    return `:${start}`;
  const count = parseLineNumber(limit);
  if (count === void 0)
    return "";
  const end = start + count - 1;
  return `:${start}${end ? `-${end}` : ""}`;
}
__name(formatReadLineRange, "formatReadLineRange");
function replaceTabs(text) {
  return text.replace(/\t/g, "   ");
}
__name(replaceTabs, "replaceTabs");
function normalizeDisplayText(text) {
  return text.replace(/\r/g, "");
}
__name(normalizeDisplayText, "normalizeDisplayText");
function getTextOutput(result, showImages) {
  if (!result)
    return "";
  const textBlocks = result.content.filter((c) => c.type === "text");
  const imageBlocks = result.content.filter((c) => c.type === "image");
  let output = textBlocks.map((c) => sanitizeBinaryOutput(stripAnsi(c.text || "")).replace(/\r/g, "")).join("\n");
  const caps = getCapabilities();
  if (imageBlocks.length > 0 && (!caps.images || !showImages)) {
    const imageIndicators = imageBlocks.map((img) => {
      const mimeType = img.mimeType ?? "image/unknown";
      const dims = img.data && img.mimeType ? getImageDimensions(img.data, img.mimeType) ?? void 0 : void 0;
      return imageFallback(mimeType, dims);
    }).join("\n");
    output = output ? `${output}
${imageIndicators}` : imageIndicators;
  }
  return output;
}
__name(getTextOutput, "getTextOutput");
var COLLAPSED_ARGS_CHARS = 100;
function formatToolCallWithArgs(title, args, theme, expanded) {
  const header = theme.fg("toolTitle", theme.bold(title));
  if (args == null)
    return header;
  const entries = typeof args === "object" && !Array.isArray(args) ? Object.entries(args) : [["args", args]];
  if (entries.length === 0)
    return header;
  if (expanded) {
    const lines = entries.map(([key, value]) => {
      const text = typeof value === "string" ? value : JSON.stringify(value, null, 2) ?? String(value);
      return `  ${key}: ${replaceTabs(text).replace(/\r/g, "").split("\n").join("\n    ")}`;
    });
    return `${header}
${theme.fg("muted", lines.join("\n"))}`;
  }
  const pairs = entries.map(([key, value]) => `${key}=${JSON.stringify(value) ?? String(value)}`).join(" ");
  const preview = pairs.length > COLLAPSED_ARGS_CHARS ? `${pairs.slice(0, COLLAPSED_ARGS_CHARS - 3)}...` : pairs;
  return `${header} ${theme.fg("muted", preview)}`;
}
__name(formatToolCallWithArgs, "formatToolCallWithArgs");
function formatToolDuration(ms) {
  const seconds = ms / 1e3;
  if (seconds < 60)
    return `${seconds.toFixed(1)}s`;
  const totalSeconds = Math.floor(seconds);
  const minutes = Math.floor(totalSeconds / 60);
  const remainder = totalSeconds % 60;
  if (minutes < 60)
    return `${minutes}m ${remainder}s`;
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m ${remainder}s`;
}
__name(formatToolDuration, "formatToolDuration");
function startToolTimer(context) {
  const state = context.state;
  if (context.executionStarted && state.startedAt === void 0) {
    state.startedAt = Date.now();
    state.endedAt = void 0;
  }
}
__name(startToolTimer, "startToolTimer");
function toolTimerText(context, isPartial) {
  const state = context.state;
  if (state.startedAt !== void 0 && isPartial && !state.interval) {
    state.interval = setInterval(() => context.invalidate(), 1e3);
  }
  if (!isPartial || context.isError) {
    state.endedAt ??= Date.now();
    if (state.interval) {
      clearInterval(state.interval);
      state.interval = void 0;
    }
  }
  if (!isPartial && context.durationMs !== void 0)
    return `Took ${formatToolDuration(context.durationMs)}`;
  if (state.startedAt === void 0)
    return void 0;
  const endTime = state.endedAt ?? Date.now();
  return `${isPartial ? "Elapsed" : "Took"} ${formatToolDuration(endTime - state.startedAt)}`;
}
__name(toolTimerText, "toolTimerText");
function invalidArgText(theme) {
  return theme.fg("error", "[invalid arg]");
}
__name(invalidArgText, "invalidArgText");
function renderToolPath(rawPath, theme, cwd, options) {
  if (rawPath === null)
    return invalidArgText(theme);
  const value = rawPath || options?.emptyFallback;
  if (!value)
    return theme.fg("toolOutput", "...");
  return linkPath(theme.fg("accent", shortenPath(value)), value, cwd);
}
__name(renderToolPath, "renderToolPath");

// pi-dist/pi-coding-agent/core/tools/tool-definition-wrapper.js
function wrapToolDefinition(definition, ctxFactory) {
  return {
    name: definition.name,
    label: definition.label,
    description: definition.description,
    parameters: definition.parameters,
    outputSchema: definition.outputSchema,
    constrainedSampling: definition.constrainedSampling,
    prepareArguments: definition.prepareArguments,
    executionMode: definition.executionMode,
    execute: /* @__PURE__ */ __name((toolCallId, params, signal, onUpdate, ctx) => definition.execute(toolCallId, params, signal, onUpdate, ctx ?? ctxFactory?.(toolCallId, signal)), "execute")
  };
}
__name(wrapToolDefinition, "wrapToolDefinition");
function createToolDefinitionFromAgentTool(tool) {
  return {
    name: tool.name,
    label: tool.label,
    description: tool.description,
    parameters: tool.parameters,
    outputSchema: tool.outputSchema,
    constrainedSampling: tool.constrainedSampling,
    prepareArguments: tool.prepareArguments,
    executionMode: tool.executionMode,
    execute: /* @__PURE__ */ __name(async (toolCallId, params, signal, onUpdate) => tool.execute(toolCallId, params, signal, onUpdate), "execute")
  };
}
__name(createToolDefinitionFromAgentTool, "createToolDefinitionFromAgentTool");

export {
  splitIncompleteAnsiSuffix,
  stripAnsi,
  writeOutputFile,
  createOutputFileStream,
  wrapToolDefinition,
  createToolDefinitionFromAgentTool,
  truncateToVisualLines,
  VisualLinePreview,
  shortenPath,
  str,
  formatReadLineRange,
  replaceTabs,
  normalizeDisplayText,
  getTextOutput,
  formatToolCallWithArgs,
  startToolTimer,
  toolTimerText,
  invalidArgText,
  renderToolPath
};
