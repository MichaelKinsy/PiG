import {
  sanitizeBinaryOutput
} from "./chunk-PCYDWUGN.js";
import {
  resolvePath
} from "./chunk-YXY3JV6E.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

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

// pi-dist/pi-coding-agent/core/tools/truncate.js
var DEFAULT_MAX_LINES = 2e3;
var DEFAULT_MAX_BYTES = 50 * 1024;
var GREP_MAX_LINE_LENGTH = 500;
function splitLinesForCounting(content) {
  if (content.length === 0) {
    return [];
  }
  const lines = content.split("\n");
  if (content.endsWith("\n")) {
    lines.pop();
  }
  return lines;
}
__name(splitLinesForCounting, "splitLinesForCounting");
function formatSize(bytes) {
  if (bytes < 1024) {
    return `${bytes}B`;
  } else if (bytes < 1024 * 1024) {
    return `${(bytes / 1024).toFixed(1)}KB`;
  } else {
    return `${(bytes / (1024 * 1024)).toFixed(1)}MB`;
  }
}
__name(formatSize, "formatSize");
function truncateHead(content, options = {}) {
  const maxLines = options.maxLines ?? DEFAULT_MAX_LINES;
  const maxBytes = options.maxBytes ?? DEFAULT_MAX_BYTES;
  const totalBytes = Buffer.byteLength(content, "utf-8");
  const lines = splitLinesForCounting(content);
  const totalLines = lines.length;
  if (totalLines <= maxLines && totalBytes <= maxBytes) {
    return {
      content,
      truncated: false,
      truncatedBy: null,
      totalLines,
      totalBytes,
      outputLines: totalLines,
      outputBytes: totalBytes,
      lastLinePartial: false,
      firstLineExceedsLimit: false,
      maxLines,
      maxBytes
    };
  }
  const firstLineBytes = Buffer.byteLength(lines[0], "utf-8");
  if (firstLineBytes > maxBytes) {
    return {
      content: "",
      truncated: true,
      truncatedBy: "bytes",
      totalLines,
      totalBytes,
      outputLines: 0,
      outputBytes: 0,
      lastLinePartial: false,
      firstLineExceedsLimit: true,
      maxLines,
      maxBytes
    };
  }
  const outputLinesArr = [];
  let outputBytesCount = 0;
  let truncatedBy = "lines";
  for (let i = 0; i < lines.length && i < maxLines; i++) {
    const line = lines[i];
    const lineBytes = Buffer.byteLength(line, "utf-8") + (i > 0 ? 1 : 0);
    if (outputBytesCount + lineBytes > maxBytes) {
      truncatedBy = "bytes";
      break;
    }
    outputLinesArr.push(line);
    outputBytesCount += lineBytes;
  }
  if (outputLinesArr.length >= maxLines && outputBytesCount <= maxBytes) {
    truncatedBy = "lines";
  }
  const outputContent = outputLinesArr.join("\n");
  const finalOutputBytes = Buffer.byteLength(outputContent, "utf-8");
  return {
    content: outputContent,
    truncated: true,
    truncatedBy,
    totalLines,
    totalBytes,
    outputLines: outputLinesArr.length,
    outputBytes: finalOutputBytes,
    lastLinePartial: false,
    firstLineExceedsLimit: false,
    maxLines,
    maxBytes
  };
}
__name(truncateHead, "truncateHead");
function truncateTail(content, options = {}) {
  const maxLines = options.maxLines ?? DEFAULT_MAX_LINES;
  const maxBytes = options.maxBytes ?? DEFAULT_MAX_BYTES;
  const totalBytes = Buffer.byteLength(content, "utf-8");
  const lines = splitLinesForCounting(content);
  const totalLines = lines.length;
  if (totalLines <= maxLines && totalBytes <= maxBytes) {
    return {
      content,
      truncated: false,
      truncatedBy: null,
      totalLines,
      totalBytes,
      outputLines: totalLines,
      outputBytes: totalBytes,
      lastLinePartial: false,
      firstLineExceedsLimit: false,
      maxLines,
      maxBytes
    };
  }
  const outputLinesArr = [];
  let outputBytesCount = 0;
  let truncatedBy = "lines";
  let lastLinePartial = false;
  for (let i = lines.length - 1; i >= 0 && outputLinesArr.length < maxLines; i--) {
    const line = lines[i];
    const lineBytes = Buffer.byteLength(line, "utf-8") + (outputLinesArr.length > 0 ? 1 : 0);
    if (outputBytesCount + lineBytes > maxBytes) {
      truncatedBy = "bytes";
      if (outputLinesArr.length === 0) {
        const truncatedLine = truncateStringToBytesFromEnd(line, maxBytes);
        outputLinesArr.unshift(truncatedLine);
        outputBytesCount = Buffer.byteLength(truncatedLine, "utf-8");
        lastLinePartial = true;
      }
      break;
    }
    outputLinesArr.unshift(line);
    outputBytesCount += lineBytes;
  }
  if (outputLinesArr.length >= maxLines && outputBytesCount <= maxBytes) {
    truncatedBy = "lines";
  }
  const outputContent = outputLinesArr.join("\n");
  const finalOutputBytes = Buffer.byteLength(outputContent, "utf-8");
  return {
    content: outputContent,
    truncated: true,
    truncatedBy,
    totalLines,
    totalBytes,
    outputLines: outputLinesArr.length,
    outputBytes: finalOutputBytes,
    lastLinePartial,
    firstLineExceedsLimit: false,
    maxLines,
    maxBytes
  };
}
__name(truncateTail, "truncateTail");
function truncateStringToBytesFromEnd(str2, maxBytes) {
  const buf = Buffer.from(str2, "utf-8");
  if (buf.length <= maxBytes) {
    return str2;
  }
  let start = buf.length - maxBytes;
  while (start < buf.length && (buf[start] & 192) === 128) {
    start++;
  }
  return buf.slice(start).toString("utf-8");
}
__name(truncateStringToBytesFromEnd, "truncateStringToBytesFromEnd");
function truncateLine(line, maxChars = GREP_MAX_LINE_LENGTH) {
  if (line.length <= maxChars) {
    return { text: line, wasTruncated: false };
  }
  return { text: `${line.slice(0, maxChars)}... [truncated]`, wasTruncated: true };
}
__name(truncateLine, "truncateLine");
function truncateMiddle(content, maxBytes) {
  const buf = Buffer.from(content, "utf-8");
  const totalLines = splitLinesForCounting(content).length;
  if (buf.length <= maxBytes) {
    return { content, truncated: false, removedChars: 0, totalBytes: buf.length, totalLines };
  }
  const isBoundary = /* @__PURE__ */ __name((index) => index >= buf.length || (buf[index] & 192) !== 128, "isBoundary");
  let headEnd = Math.floor(maxBytes / 2);
  while (headEnd > 0 && !isBoundary(headEnd))
    headEnd--;
  let tailStart = buf.length - (maxBytes - Math.floor(maxBytes / 2));
  while (tailStart < buf.length && !isBoundary(tailStart))
    tailStart++;
  const head = buf.subarray(0, headEnd).toString("utf-8");
  const tail = buf.subarray(tailStart).toString("utf-8");
  const removedChars = Array.from(buf.subarray(headEnd, tailStart).toString("utf-8")).length;
  return {
    content: `${head}\u2026${removedChars} chars truncated\u2026${tail}`,
    truncated: true,
    removedChars,
    totalBytes: buf.length,
    totalLines
  };
}
__name(truncateMiddle, "truncateMiddle");

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
import * as os from "node:os";
import { pathToFileURL } from "node:url";
import { getCapabilities, getImageDimensions, hyperlink, imageFallback } from "../../../pi-tui.mjs";

// pi-dist/pi-coding-agent/utils/ansi.js
var ST = "(?:\\u0007|\\u001B\\u005C|\\u009C)";
var OSC_START = "\\u001B\\]";
var CSI_START = "[\\u001B\\u009B][[\\]()#;?]*(?:\\d{1,4}(?:[;:]\\d{0,4})*)?";
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
  const home = os.homedir();
  if (path.startsWith(home)) {
    return `~${path.slice(home.length)}`;
  }
  return path;
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
  DEFAULT_MAX_LINES,
  DEFAULT_MAX_BYTES,
  GREP_MAX_LINE_LENGTH,
  formatSize,
  truncateHead,
  truncateTail,
  truncateLine,
  truncateMiddle,
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
