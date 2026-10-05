import {
  wrapToolDefinition
} from "./chunk-RUCWNNX6.js";
import {
  DEFAULT_MAX_BYTES,
  DEFAULT_MAX_LINES,
  GREP_MAX_LINE_LENGTH,
  VisualLinePreview,
  createOutputFileStream,
  formatSize,
  getPowerShellConfig,
  getShellConfig,
  getShellEnv,
  getTextOutput,
  invalidArgText,
  killProcessTree,
  normalizeDisplayText,
  renderToolPath,
  replaceTabs,
  shortenPath,
  str,
  trackDetachedChildPid,
  truncateHead,
  truncateLine,
  truncateTail,
  untrackDetachedChildPid
} from "./chunk-EDGTPAH6.js";
import {
  APP_NAME,
  formatPathRelativeToCwdOrAbsolute,
  getBinDir,
  getLanguageFromPath,
  getReadmePath,
  highlightCode,
  keyHint,
  keyText,
  resolvePath,
  splitBom,
  theme,
  waitForChildProcess
} from "./chunk-H7ICR3WT.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-coding-agent/core/tools/bash.js
import { constants } from "node:fs";
import { access as fsAccess } from "node:fs/promises";
import { constants as osConstants } from "node:os";
import { spawn } from "child_process";
import { Type } from "../../../typebox.mjs";

// pi-dist/pi-coding-agent/core/tools/output-accumulator.js
import { open } from "node:fs/promises";
function byteLength(text) {
  return Buffer.byteLength(text, "utf-8");
}
__name(byteLength, "byteLength");
var OutputAccumulator = class {
  static {
    __name(this, "OutputAccumulator");
  }
  maxLines;
  maxBytes;
  maxRollingBytes;
  tempFilePrefix;
  decoder = new TextDecoder();
  rawChunks = [];
  tailText = "";
  tailBytes = 0;
  tailStartsAtLineBoundary = true;
  totalRawBytes = 0;
  totalDecodedBytes = 0;
  completedLines = 0;
  totalLines = 0;
  currentLineBytes = 0;
  hasOpenLine = false;
  finished = false;
  tempFilePath;
  tempFileStream;
  constructor(options = {}) {
    this.maxLines = options.maxLines ?? DEFAULT_MAX_LINES;
    this.maxBytes = options.maxBytes ?? DEFAULT_MAX_BYTES;
    this.maxRollingBytes = Math.max(this.maxBytes * 2, 1);
    this.tempFilePrefix = options.tempFilePrefix ?? "pi-output";
  }
  append(data) {
    if (this.finished) {
      throw new Error("Cannot append to a finished output accumulator");
    }
    this.totalRawBytes += data.length;
    this.appendDecodedText(this.decoder.decode(data, { stream: true }));
    if (this.tempFileStream || this.shouldUseTempFile()) {
      this.ensureTempFile();
      this.tempFileStream?.write(data);
    } else if (data.length > 0) {
      this.rawChunks.push(data);
    }
  }
  finish() {
    if (this.finished) {
      return;
    }
    this.finished = true;
    this.appendDecodedText(this.decoder.decode());
    if (this.shouldUseTempFile()) {
      this.ensureTempFile();
    }
  }
  snapshot(options = {}) {
    const tailTruncation = truncateTail(this.getSnapshotText(), {
      maxLines: this.maxLines,
      maxBytes: this.maxBytes
    });
    const truncated = this.totalLines > this.maxLines || this.totalDecodedBytes > this.maxBytes;
    const truncatedBy = truncated ? tailTruncation.truncatedBy ?? (this.totalDecodedBytes > this.maxBytes ? "bytes" : "lines") : null;
    const truncation = {
      ...tailTruncation,
      truncated,
      truncatedBy,
      totalLines: this.totalLines,
      totalBytes: this.totalDecodedBytes,
      maxLines: this.maxLines,
      maxBytes: this.maxBytes
    };
    if (options.persistIfTruncated && truncation.truncated) {
      this.ensureTempFile();
    }
    return {
      content: truncation.content,
      truncation,
      fullOutputPath: this.tempFilePath
    };
  }
  async closeTempFile() {
    if (!this.tempFileStream) {
      return;
    }
    const stream = this.tempFileStream;
    this.tempFileStream = void 0;
    await new Promise((resolve2, reject) => {
      const onError = /* @__PURE__ */ __name((error) => {
        stream.off("finish", onFinish);
        reject(error);
      }, "onError");
      const onFinish = /* @__PURE__ */ __name(() => {
        stream.off("error", onError);
        resolve2();
      }, "onFinish");
      stream.once("error", onError);
      stream.once("finish", onFinish);
      stream.end();
    });
  }
  /**
   * The complete output, for callers that can take more than the display snapshot. Call after
   * `finish()` and `closeTempFile()`. Output longer than `maxBytes` raw bytes keeps its first and
   * last `maxBytes / 2` bytes around an omission marker.
   */
  async readFullOutput(maxBytes) {
    if (!this.tempFilePath) {
      return { content: new TextDecoder().decode(Buffer.concat(this.rawChunks)), truncated: false };
    }
    const file = await open(this.tempFilePath, "r");
    try {
      const size = (await file.stat()).size;
      if (size <= maxBytes) {
        return { content: new TextDecoder().decode(await file.readFile()), truncated: false };
      }
      const headBytes = Math.floor(maxBytes / 2);
      const tailBytes = maxBytes - headBytes;
      const head = Buffer.alloc(headBytes);
      const tail = Buffer.alloc(tailBytes);
      await file.read(head, 0, headBytes, 0);
      await file.read(tail, 0, tailBytes, size - tailBytes);
      const headText = new TextDecoder().decode(head, { stream: true });
      let tailStart = 0;
      while (tailStart < tail.length && (tail[tailStart] & 192) === 128)
        tailStart++;
      const tailText = new TextDecoder().decode(tail.subarray(tailStart));
      const omitted = size - headBytes - tailBytes;
      return { content: `${headText}

[... ${omitted} bytes omitted ...]

${tailText}`, truncated: true };
    } finally {
      await file.close();
    }
  }
  getLastLineBytes() {
    return this.currentLineBytes;
  }
  appendDecodedText(text) {
    if (text.length === 0) {
      return;
    }
    const bytes = byteLength(text);
    this.totalDecodedBytes += bytes;
    this.tailText += text;
    this.tailBytes += bytes;
    if (this.tailBytes > this.maxRollingBytes * 2) {
      this.trimTail();
    }
    let newlines = 0;
    let lastNewline = -1;
    for (let i = text.indexOf("\n"); i !== -1; i = text.indexOf("\n", i + 1)) {
      newlines++;
      lastNewline = i;
    }
    if (newlines === 0) {
      this.currentLineBytes += bytes;
      this.hasOpenLine = true;
    } else {
      this.completedLines += newlines;
      const tail = text.slice(lastNewline + 1);
      this.currentLineBytes = byteLength(tail);
      this.hasOpenLine = tail.length > 0;
    }
    this.totalLines = this.completedLines + (this.hasOpenLine ? 1 : 0);
  }
  trimTail() {
    const buffer = Buffer.from(this.tailText, "utf-8");
    if (buffer.length <= this.maxRollingBytes) {
      this.tailBytes = buffer.length;
      return;
    }
    let start = buffer.length - this.maxRollingBytes;
    while (start < buffer.length && (buffer[start] & 192) === 128) {
      start++;
    }
    this.tailStartsAtLineBoundary = start === 0 ? this.tailStartsAtLineBoundary : buffer[start - 1] === 10;
    this.tailText = buffer.subarray(start).toString("utf-8");
    this.tailBytes = byteLength(this.tailText);
  }
  getSnapshotText() {
    if (this.tailStartsAtLineBoundary) {
      return this.tailText;
    }
    const firstNewline = this.tailText.indexOf("\n");
    return firstNewline === -1 ? this.tailText : this.tailText.slice(firstNewline + 1);
  }
  shouldUseTempFile() {
    return this.totalRawBytes > this.maxBytes || this.totalDecodedBytes > this.maxBytes || this.totalLines > this.maxLines;
  }
  ensureTempFile() {
    if (this.tempFilePath) {
      return;
    }
    const { path: path4, stream } = createOutputFileStream(this.tempFilePrefix, ".log");
    this.tempFilePath = path4;
    this.tempFileStream = stream;
    for (const chunk of this.rawChunks) {
      this.tempFileStream.write(chunk);
    }
    this.rawChunks = [];
  }
};

// pi-dist/pi-coding-agent/core/tools/renderers/bash.js
import { Container, Spacer, Text } from "../../../pi-tui.mjs";
var BASH_PREVIEW_LINES = 5;
var BASH_UPDATE_THROTTLE_MS = 100;
function formatDuration(ms) {
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
__name(formatDuration, "formatDuration");
function formatShellCall(args, prompt) {
  const command = str(args?.command);
  const timeout = args?.timeout;
  const timeoutSuffix = timeout ? theme.fg("muted", ` (timeout ${timeout}s)`) : "";
  const commandDisplay = command === null ? invalidArgText(theme) : command ? command : theme.fg("toolOutput", "...");
  return theme.fg("toolTitle", theme.bold(`${prompt} ${commandDisplay}`)) + timeoutSuffix;
}
__name(formatShellCall, "formatShellCall");
function rebuildBashResultRenderComponent(component, result, options, showImages, startedAt, endedAt) {
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
    const styledOutput = output.split("\n").map((line) => theme.fg("toolOutput", line)).join("\n");
    if (options.expanded) {
      component.addChild(new Text(`
${styledOutput}`, 0, 0));
    } else {
      component.addChild(new Spacer(1));
      component.addChild(new VisualLinePreview({
        text: styledOutput,
        maxVisualLines: BASH_PREVIEW_LINES,
        keep: "end",
        formatHint: /* @__PURE__ */ __name((hidden) => theme.fg("muted", `... (${hidden} earlier lines,`) + ` ${keyHint("app.tools.expand", "to expand")}${theme.fg("muted", ")")}`, "formatHint")
      }));
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
      } else {
        warnings.push(`Truncated: ${truncation.outputLines} lines shown (${formatSize(truncation.maxBytes ?? DEFAULT_MAX_BYTES)} limit)`);
      }
    }
    component.addChild(new Text(`
${theme.fg("warning", `[${warnings.join(". ")}]`)}`, 0, 0));
  }
  if (startedAt !== void 0) {
    const label = options.isPartial ? "Elapsed" : "Took";
    const endTime = endedAt ?? Date.now();
    component.addChild(new Text(`
${theme.fg("muted", `${label} ${formatDuration(endTime - startedAt)}`)}`, 0, 0));
  }
}
__name(rebuildBashResultRenderComponent, "rebuildBashResultRenderComponent");
function createShellRenderers(prompt) {
  return {
    renderCall(args, _theme, context) {
      const state = context.state;
      if (context.executionStarted && state.startedAt === void 0) {
        state.startedAt = Date.now();
        state.endedAt = void 0;
      }
      const text = context.lastComponent ?? new Text("", 0, 0);
      text.setText(formatShellCall(args, prompt));
      return text;
    },
    renderResult(result, options, _theme, context) {
      const state = context.state;
      if (state.startedAt !== void 0 && options.isPartial && !state.interval) {
        state.interval = setInterval(() => context.invalidate(), 1e3);
      }
      if (!options.isPartial || context.isError) {
        state.endedAt ??= Date.now();
        if (state.interval) {
          clearInterval(state.interval);
          state.interval = void 0;
        }
      }
      const component = context.lastComponent ?? new Container();
      rebuildBashResultRenderComponent(component, result, options, context.showImages, state.startedAt, state.endedAt);
      component.invalidate();
      return component;
    }
  };
}
__name(createShellRenderers, "createShellRenderers");

// pi-dist/pi-coding-agent/core/tools/bash.js
var MAX_TIMEOUT_MS = 2147483647;
var STRUCTURED_OUTPUT_MAX_BYTES = 1024 * 1024;
var MAX_TIMEOUT_SECONDS = MAX_TIMEOUT_MS / 1e3;
function resolveTimeoutMs(timeout) {
  if (timeout === void 0)
    return void 0;
  if (!Number.isFinite(timeout) || timeout <= 0) {
    throw new Error("Invalid timeout: must be a finite number of seconds");
  }
  const timeoutMs = timeout * 1e3;
  if (timeoutMs > MAX_TIMEOUT_MS) {
    throw new Error(`Invalid timeout: maximum is ${MAX_TIMEOUT_SECONDS} seconds`);
  }
  return timeoutMs;
}
__name(resolveTimeoutMs, "resolveTimeoutMs");
var bashSchema = Type.Object({
  command: Type.String({ description: "Shell command to execute" }),
  timeout: Type.Optional(Type.Number({ description: "Timeout in seconds (optional, no default timeout)" }))
});
var bashToolSystemPromptContribution = {
  snippet: "Execute bash commands (ls, grep, find, etc.)",
  guidelines: ["You can inspect PI_* environment variables for current model and session details."]
};
var bashOutputSchema = Type.Object({
  output: Type.String({ description: "Combined stdout and stderr, possibly truncated" }),
  truncated: Type.Boolean(),
  full_output_path: Type.Optional(Type.String({ description: "Full output, when truncated" })),
  exit_code: Type.Number(),
  wall_time_seconds: Type.Number()
});
function createLocalShellOperations(shellName, resolveShellConfig) {
  return {
    exec: /* @__PURE__ */ __name(async (command, cwd, { onData, signal, timeout, env }) => {
      const timeoutMs = resolveTimeoutMs(timeout);
      if (signal?.aborted) {
        throw new Error("aborted");
      }
      const shellConfig = resolveShellConfig();
      try {
        await fsAccess(cwd, constants.F_OK);
      } catch {
        throw new Error(`Working directory does not exist: ${cwd}
Cannot execute ${shellName} commands.`);
      }
      const commandFromStdin = shellConfig.commandTransport === "stdin";
      const child = spawn(shellConfig.shell, commandFromStdin ? shellConfig.args : [...shellConfig.args, command], {
        cwd,
        detached: process.platform !== "win32",
        env: env ?? getShellEnv(),
        stdio: [commandFromStdin ? "pipe" : "ignore", "pipe", "pipe"],
        windowsHide: true
      });
      if (commandFromStdin) {
        child.stdin?.on("error", () => {
        });
        child.stdin?.end(command);
      }
      if (child.pid)
        trackDetachedChildPid(child.pid);
      let timedOut = false;
      let timeoutHandle;
      const onAbort = /* @__PURE__ */ __name(() => {
        if (child.pid)
          killProcessTree(child.pid);
      }, "onAbort");
      try {
        if (timeoutMs !== void 0) {
          timeoutHandle = setTimeout(() => {
            timedOut = true;
            if (child.pid)
              killProcessTree(child.pid);
          }, timeoutMs);
        }
        child.stdout?.on("data", onData);
        child.stderr?.on("data", onData);
        if (signal) {
          if (signal.aborted)
            onAbort();
          else
            signal.addEventListener("abort", onAbort, { once: true });
        }
        const exitCode = await waitForChildProcess(child);
        if (signal?.aborted) {
          throw new Error("aborted");
        }
        if (timedOut) {
          throw new Error(`timeout:${timeout}`);
        }
        const signalCode = child.signalCode;
        return { exitCode: exitCode ?? (signalCode ? 128 + (osConstants.signals[signalCode] ?? 0) : 1) };
      } finally {
        if (child.pid)
          untrackDetachedChildPid(child.pid);
        if (timeoutHandle)
          clearTimeout(timeoutHandle);
        if (signal)
          signal.removeEventListener("abort", onAbort);
      }
    }, "exec")
  };
}
__name(createLocalShellOperations, "createLocalShellOperations");
function createLocalBashOperations(options) {
  return createLocalShellOperations("bash", () => getShellConfig(options?.shellPath));
}
__name(createLocalBashOperations, "createLocalBashOperations");
function resolveSpawnContext(command, cwd, spawnHook, exposeSessionEnvironment, ctx) {
  const env = { ...getShellEnv() };
  delete env.PI_SESSION_ID;
  delete env.PI_SESSION_FILE;
  delete env.PI_PROVIDER;
  delete env.PI_MODEL;
  delete env.PI_REASONING_LEVEL;
  if (exposeSessionEnvironment && ctx) {
    const model = ctx.model;
    env.PI_SESSION_ID = ctx.sessionManager.getSessionId();
    const sessionFile = ctx.sessionManager.getSessionFile();
    if (sessionFile)
      env.PI_SESSION_FILE = sessionFile;
    if (model) {
      env.PI_PROVIDER = model.provider;
      env.PI_MODEL = model.id;
    }
    if (ctx.thinkingLevel)
      env.PI_REASONING_LEVEL = ctx.thinkingLevel;
  }
  const baseContext = { command, cwd, env };
  return spawnHook ? spawnHook(baseContext) : baseContext;
}
__name(resolveSpawnContext, "resolveSpawnContext");
function createShellToolDefinition(cwd, config, options) {
  const ops = options?.operations ?? createLocalBashOperations({ shellPath: options?.shellPath });
  const commandPrefix = options?.commandPrefix;
  const exposeSessionEnvironment = options?.exposeSessionEnvironment ?? true;
  const spawnHook = options?.spawnHook;
  return {
    name: config.name,
    label: config.label,
    description: `Execute a ${config.shellName} command in the current working directory. Returns stdout and stderr. Output is truncated to last ${DEFAULT_MAX_LINES} lines or ${DEFAULT_MAX_BYTES / 1024}KB (whichever is hit first). If truncated, full output is saved to a temp file. Optionally provide a timeout in seconds.`,
    promptSnippet: config.promptSnippet,
    promptGuidelines: exposeSessionEnvironment && config.promptGuidelines ? [...config.promptGuidelines] : void 0,
    parameters: bashSchema,
    outputSchema: bashOutputSchema,
    constrainedSampling: { type: "json_schema", strict: "prefer" },
    async execute(_toolCallId, { command, timeout }, signal, onUpdate, ctx) {
      const resolvedCommand = commandPrefix ? `${commandPrefix}
${command}` : command;
      const spawnContext = resolveSpawnContext(resolvedCommand, ctx?.cwd || cwd, spawnHook, exposeSessionEnvironment, ctx);
      const output = new OutputAccumulator({ tempFilePrefix: config.tempFilePrefix });
      let acceptingOutput = true;
      let updateTimer;
      let updateDirty = false;
      let lastUpdateAt = 0;
      const emitOutputUpdate = /* @__PURE__ */ __name(() => {
        if (!onUpdate || !updateDirty)
          return;
        updateDirty = false;
        lastUpdateAt = Date.now();
        const snapshot = output.snapshot({ persistIfTruncated: true });
        onUpdate({
          content: [{ type: "text", text: snapshot.content || "" }],
          details: {
            truncation: snapshot.truncation.truncated ? snapshot.truncation : void 0,
            fullOutputPath: snapshot.fullOutputPath
          }
        });
      }, "emitOutputUpdate");
      const clearUpdateTimer = /* @__PURE__ */ __name(() => {
        if (updateTimer) {
          clearTimeout(updateTimer);
          updateTimer = void 0;
        }
      }, "clearUpdateTimer");
      const scheduleOutputUpdate = /* @__PURE__ */ __name(() => {
        if (!onUpdate)
          return;
        updateDirty = true;
        const delay = BASH_UPDATE_THROTTLE_MS - (Date.now() - lastUpdateAt);
        if (delay <= 0) {
          clearUpdateTimer();
          emitOutputUpdate();
          return;
        }
        updateTimer ??= setTimeout(() => {
          updateTimer = void 0;
          emitOutputUpdate();
        }, delay);
      }, "scheduleOutputUpdate");
      if (onUpdate) {
        onUpdate({ content: [], details: void 0 });
      }
      const handleData = /* @__PURE__ */ __name((data) => {
        if (!acceptingOutput)
          return;
        output.append(data);
        scheduleOutputUpdate();
      }, "handleData");
      const finishOutput = /* @__PURE__ */ __name(async () => {
        acceptingOutput = false;
        output.finish();
        clearUpdateTimer();
        emitOutputUpdate();
        const snapshot = output.snapshot({ persistIfTruncated: true });
        await output.closeTempFile();
        return snapshot;
      }, "finishOutput");
      const formatOutput = /* @__PURE__ */ __name((snapshot, emptyText = "(no output)") => {
        const truncation = snapshot.truncation;
        let text = snapshot.content || emptyText;
        let details;
        if (truncation.truncated) {
          details = { truncation, fullOutputPath: snapshot.fullOutputPath };
          const startLine = truncation.totalLines - truncation.outputLines + 1;
          const endLine = truncation.totalLines;
          if (truncation.lastLinePartial) {
            const lastLineSize = formatSize(output.getLastLineBytes());
            text += `

[Showing last ${formatSize(truncation.outputBytes)} of line ${endLine} (line is ${lastLineSize}). Full output: ${snapshot.fullOutputPath}]`;
          } else if (truncation.truncatedBy === "lines") {
            text += `

[Showing lines ${startLine}-${endLine} of ${truncation.totalLines}. Full output: ${snapshot.fullOutputPath}]`;
          } else {
            text += `

[Showing lines ${startLine}-${endLine} of ${truncation.totalLines} (${formatSize(DEFAULT_MAX_BYTES)} limit). Full output: ${snapshot.fullOutputPath}]`;
          }
        }
        return { text, details };
      }, "formatOutput");
      const appendStatus = /* @__PURE__ */ __name((text, status) => `${text ? `${text}

` : ""}${status}`, "appendStatus");
      const startedAt = performance.now();
      try {
        let exitCode;
        try {
          const result = await ops.exec(spawnContext.command, spawnContext.cwd, {
            onData: handleData,
            signal,
            timeout,
            env: spawnContext.env
          });
          exitCode = result.exitCode;
        } catch (err) {
          const snapshot2 = await finishOutput();
          const { text } = formatOutput(snapshot2, "");
          if (err instanceof Error && err.message === "aborted") {
            throw new Error(appendStatus(text, "Command aborted"));
          }
          if (err instanceof Error && err.message.startsWith("timeout:")) {
            const timeoutSecs = err.message.split(":")[1];
            throw new Error(appendStatus(text, `Command timed out after ${timeoutSecs} seconds`));
          }
          throw err;
        }
        const snapshot = await finishOutput();
        const { text: outputText, details } = formatOutput(snapshot);
        if (exitCode === null) {
          throw new Error(appendStatus(outputText, "Command terminated without an exit code"));
        }
        const wallTimeSeconds = Math.round((performance.now() - startedAt) / 100) / 10;
        const fullOutput = await output.readFullOutput(STRUCTURED_OUTPUT_MAX_BYTES);
        const structuredContent = {
          output: fullOutput.content,
          truncated: fullOutput.truncated,
          ...fullOutput.truncated && snapshot.fullOutputPath ? { full_output_path: snapshot.fullOutputPath } : {},
          exit_code: exitCode,
          wall_time_seconds: wallTimeSeconds
        };
        if (exitCode !== 0) {
          return {
            content: [{ type: "text", text: appendStatus(outputText, `Command exited with code ${exitCode}`) }],
            details,
            structuredContent,
            isError: true
          };
        }
        return { content: [{ type: "text", text: outputText }], details, structuredContent };
      } finally {
        clearUpdateTimer();
      }
    },
    ...createShellRenderers(config.prompt)
  };
}
__name(createShellToolDefinition, "createShellToolDefinition");
var bashToolConfig = {
  name: "bash",
  label: "bash",
  shellName: "bash",
  prompt: "$",
  promptSnippet: bashToolSystemPromptContribution.snippet,
  promptGuidelines: bashToolSystemPromptContribution.guidelines,
  tempFilePrefix: "pi-bash"
};
function createBashToolDefinition(cwd, options) {
  return createShellToolDefinition(cwd, bashToolConfig, options);
}
__name(createBashToolDefinition, "createBashToolDefinition");
function createBashTool(cwd, options) {
  const definition = createBashToolDefinition(cwd, options);
  const tool = wrapToolDefinition(definition);
  Object.assign(tool, {
    promptSnippet: definition.promptSnippet,
    promptGuidelines: definition.promptGuidelines
  });
  return tool;
}
__name(createBashTool, "createBashTool");

// pi-dist/pi-coding-agent/core/tools/edit.js
import { constants as constants4 } from "fs";
import { access as fsAccess2, readFile as fsReadFile, writeFile as fsWriteFile } from "fs/promises";
import { Type as Type2 } from "../../../typebox.mjs";

// pi-dist/pi-coding-agent/core/tools/edit-diff.js
import * as Diff from "../../../diff/libesm/index.js";
import { constants as constants3 } from "fs";
import { access as access2, readFile } from "fs/promises";

// pi-dist/pi-coding-agent/core/tools/path-utils.js
import { accessSync, constants as constants2 } from "node:fs";
import { access } from "node:fs/promises";
var NARROW_NO_BREAK_SPACE = "\u202F";
function tryMacOSScreenshotPath(filePath) {
  return filePath.replace(/ (AM|PM)\./gi, `${NARROW_NO_BREAK_SPACE}$1.`);
}
__name(tryMacOSScreenshotPath, "tryMacOSScreenshotPath");
function tryNFDVariant(filePath) {
  return filePath.normalize("NFD");
}
__name(tryNFDVariant, "tryNFDVariant");
function tryCurlyQuoteVariant(filePath) {
  return filePath.replace(/'/g, "\u2019");
}
__name(tryCurlyQuoteVariant, "tryCurlyQuoteVariant");
function fileExists(filePath) {
  try {
    accessSync(filePath, constants2.F_OK);
    return true;
  } catch {
    return false;
  }
}
__name(fileExists, "fileExists");
async function pathExists(filePath) {
  try {
    await access(filePath, constants2.F_OK);
    return true;
  } catch {
    return false;
  }
}
__name(pathExists, "pathExists");
function resolveToCwd(filePath, cwd) {
  return resolvePath(filePath, cwd, { normalizeUnicodeSpaces: true, stripAtPrefix: true });
}
__name(resolveToCwd, "resolveToCwd");
function resolveReadPath(filePath, cwd) {
  const resolved = resolveToCwd(filePath, cwd);
  if (fileExists(resolved)) {
    return resolved;
  }
  const amPmVariant = tryMacOSScreenshotPath(resolved);
  if (amPmVariant !== resolved && fileExists(amPmVariant)) {
    return amPmVariant;
  }
  const nfdVariant = tryNFDVariant(resolved);
  if (nfdVariant !== resolved && fileExists(nfdVariant)) {
    return nfdVariant;
  }
  const curlyVariant = tryCurlyQuoteVariant(resolved);
  if (curlyVariant !== resolved && fileExists(curlyVariant)) {
    return curlyVariant;
  }
  const nfdCurlyVariant = tryCurlyQuoteVariant(nfdVariant);
  if (nfdCurlyVariant !== resolved && fileExists(nfdCurlyVariant)) {
    return nfdCurlyVariant;
  }
  return resolved;
}
__name(resolveReadPath, "resolveReadPath");
async function resolveReadPathAsync(filePath, cwd) {
  const resolved = resolveToCwd(filePath, cwd);
  if (await pathExists(resolved)) {
    return resolved;
  }
  const amPmVariant = tryMacOSScreenshotPath(resolved);
  if (amPmVariant !== resolved && await pathExists(amPmVariant)) {
    return amPmVariant;
  }
  const nfdVariant = tryNFDVariant(resolved);
  if (nfdVariant !== resolved && await pathExists(nfdVariant)) {
    return nfdVariant;
  }
  const curlyVariant = tryCurlyQuoteVariant(resolved);
  if (curlyVariant !== resolved && await pathExists(curlyVariant)) {
    return curlyVariant;
  }
  const nfdCurlyVariant = tryCurlyQuoteVariant(nfdVariant);
  if (nfdCurlyVariant !== resolved && await pathExists(nfdCurlyVariant)) {
    return nfdCurlyVariant;
  }
  return resolved;
}
__name(resolveReadPathAsync, "resolveReadPathAsync");

// pi-dist/pi-coding-agent/core/tools/edit-diff.js
function detectLineEnding(content) {
  const crlfIdx = content.indexOf("\r\n");
  const lfIdx = content.indexOf("\n");
  if (lfIdx === -1)
    return "\n";
  if (crlfIdx === -1)
    return "\n";
  return crlfIdx < lfIdx ? "\r\n" : "\n";
}
__name(detectLineEnding, "detectLineEnding");
function normalizeToLF(text) {
  return text.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
}
__name(normalizeToLF, "normalizeToLF");
function restoreLineEndings(text, ending) {
  return ending === "\r\n" ? text.replace(/\n/g, "\r\n") : text;
}
__name(restoreLineEndings, "restoreLineEndings");
function normalizeForFuzzyMatch(text) {
  return text.normalize("NFKC").split("\n").map((line) => line.trimEnd()).join("\n").replace(/[\u2018\u2019\u201A\u201B]/g, "'").replace(/[\u201C\u201D\u201E\u201F]/g, '"').replace(/[\u2010\u2011\u2012\u2013\u2014\u2015\u2212]/g, "-").replace(/[\u00A0\u2002-\u200A\u202F\u205F\u3000]/g, " ");
}
__name(normalizeForFuzzyMatch, "normalizeForFuzzyMatch");
function splitLinesWithEndings(content) {
  return content.match(/[^\n]*\n|[^\n]+/g) ?? [];
}
__name(splitLinesWithEndings, "splitLinesWithEndings");
function getLineSpans(content) {
  let offset = 0;
  return splitLinesWithEndings(content).map((line) => {
    const span = { start: offset, end: offset + line.length };
    offset = span.end;
    return span;
  });
}
__name(getLineSpans, "getLineSpans");
function getReplacementLineRange(lines, replacement) {
  const replacementStart = replacement.matchIndex;
  const replacementEnd = replacement.matchIndex + replacement.matchLength;
  let startLine = -1;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (replacementStart >= line.start && replacementStart < line.end) {
      startLine = i;
      break;
    }
  }
  if (startLine === -1) {
    throw new Error("Replacement range is outside the base content.");
  }
  let endLine = startLine;
  while (endLine < lines.length && lines[endLine].end < replacementEnd) {
    endLine++;
  }
  if (endLine >= lines.length) {
    throw new Error("Replacement range is outside the base content.");
  }
  return { startLine, endLine: endLine + 1 };
}
__name(getReplacementLineRange, "getReplacementLineRange");
function applyReplacements(content, replacements, offset = 0) {
  let result = content;
  for (let i = replacements.length - 1; i >= 0; i--) {
    const replacement = replacements[i];
    const matchIndex = replacement.matchIndex - offset;
    result = result.substring(0, matchIndex) + replacement.newText + result.substring(matchIndex + replacement.matchLength);
  }
  return result;
}
__name(applyReplacements, "applyReplacements");
function applyReplacementsPreservingUnchangedLines(originalContent, baseContent, replacements) {
  const originalLines = splitLinesWithEndings(originalContent);
  const baseLines = getLineSpans(baseContent);
  if (originalLines.length !== baseLines.length) {
    throw new Error("Cannot preserve unchanged lines because the base content has a different line count.");
  }
  const groups = [];
  const sortedReplacements = [...replacements].sort((a, b) => a.matchIndex - b.matchIndex);
  for (const replacement of sortedReplacements) {
    const range = getReplacementLineRange(baseLines, replacement);
    const current = groups[groups.length - 1];
    if (current && range.startLine < current.endLine) {
      current.endLine = Math.max(current.endLine, range.endLine);
      current.replacements.push(replacement);
      continue;
    }
    groups.push({ ...range, replacements: [replacement] });
  }
  let originalLineIndex = 0;
  let result = "";
  for (const group of groups) {
    result += originalLines.slice(originalLineIndex, group.startLine).join("");
    const groupStartOffset = baseLines[group.startLine].start;
    const groupEndOffset = baseLines[group.endLine - 1].end;
    result += applyReplacements(baseContent.slice(groupStartOffset, groupEndOffset), group.replacements, groupStartOffset);
    originalLineIndex = group.endLine;
  }
  result += originalLines.slice(originalLineIndex).join("");
  return result;
}
__name(applyReplacementsPreservingUnchangedLines, "applyReplacementsPreservingUnchangedLines");
function fuzzyFindText(content, oldText) {
  const exactIndex = content.indexOf(oldText);
  if (exactIndex !== -1) {
    return {
      found: true,
      index: exactIndex,
      matchLength: oldText.length,
      usedFuzzyMatch: false,
      contentForReplacement: content
    };
  }
  const fuzzyContent = normalizeForFuzzyMatch(content);
  const fuzzyOldText = normalizeForFuzzyMatch(oldText);
  const fuzzyIndex = fuzzyContent.indexOf(fuzzyOldText);
  if (fuzzyIndex === -1) {
    return {
      found: false,
      index: -1,
      matchLength: 0,
      usedFuzzyMatch: false,
      contentForReplacement: content
    };
  }
  return {
    found: true,
    index: fuzzyIndex,
    matchLength: fuzzyOldText.length,
    usedFuzzyMatch: true,
    contentForReplacement: fuzzyContent
  };
}
__name(fuzzyFindText, "fuzzyFindText");
function countOccurrences(content, oldText) {
  const fuzzyContent = normalizeForFuzzyMatch(content);
  const fuzzyOldText = normalizeForFuzzyMatch(oldText);
  return fuzzyContent.split(fuzzyOldText).length - 1;
}
__name(countOccurrences, "countOccurrences");
function getNotFoundError(path4, editIndex, totalEdits) {
  if (totalEdits === 1) {
    return new Error(`Could not find the exact text in ${path4}. The old text must match exactly including all whitespace and newlines.`);
  }
  return new Error(`Could not find edits[${editIndex}] in ${path4}. The oldText must match exactly including all whitespace and newlines.`);
}
__name(getNotFoundError, "getNotFoundError");
function getDuplicateError(path4, editIndex, totalEdits, occurrences) {
  if (totalEdits === 1) {
    return new Error(`Found ${occurrences} occurrences of the text in ${path4}. The text must be unique. Please provide more context to make it unique.`);
  }
  return new Error(`Found ${occurrences} occurrences of edits[${editIndex}] in ${path4}. Each oldText must be unique. Please provide more context to make it unique.`);
}
__name(getDuplicateError, "getDuplicateError");
function getEmptyOldTextError(path4, editIndex, totalEdits) {
  if (totalEdits === 1) {
    return new Error(`oldText must not be empty in ${path4}.`);
  }
  return new Error(`edits[${editIndex}].oldText must not be empty in ${path4}.`);
}
__name(getEmptyOldTextError, "getEmptyOldTextError");
function getNoChangeError(path4, totalEdits) {
  if (totalEdits === 1) {
    return new Error(`No changes made to ${path4}. The replacement produced identical content. This might indicate an issue with special characters or the text not existing as expected.`);
  }
  return new Error(`No changes made to ${path4}. The replacements produced identical content.`);
}
__name(getNoChangeError, "getNoChangeError");
function applyEditsToNormalizedContent(normalizedContent, edits, path4) {
  const normalizedEdits = edits.map((edit) => ({
    oldText: normalizeToLF(edit.oldText),
    newText: normalizeToLF(edit.newText)
  }));
  for (let i = 0; i < normalizedEdits.length; i++) {
    if (normalizedEdits[i].oldText.length === 0) {
      throw getEmptyOldTextError(path4, i, normalizedEdits.length);
    }
  }
  const initialMatches = normalizedEdits.map((edit) => fuzzyFindText(normalizedContent, edit.oldText));
  const usedFuzzyMatch = initialMatches.some((match) => match.usedFuzzyMatch);
  const replacementBaseContent = usedFuzzyMatch ? normalizeForFuzzyMatch(normalizedContent) : normalizedContent;
  const matchedEdits = [];
  for (let i = 0; i < normalizedEdits.length; i++) {
    const edit = normalizedEdits[i];
    const matchResult = fuzzyFindText(replacementBaseContent, edit.oldText);
    if (!matchResult.found) {
      throw getNotFoundError(path4, i, normalizedEdits.length);
    }
    const occurrences = countOccurrences(replacementBaseContent, edit.oldText);
    if (occurrences > 1) {
      throw getDuplicateError(path4, i, normalizedEdits.length, occurrences);
    }
    matchedEdits.push({
      editIndex: i,
      matchIndex: matchResult.index,
      matchLength: matchResult.matchLength,
      newText: edit.newText
    });
  }
  matchedEdits.sort((a, b) => a.matchIndex - b.matchIndex);
  for (let i = 1; i < matchedEdits.length; i++) {
    const previous = matchedEdits[i - 1];
    const current = matchedEdits[i];
    if (previous.matchIndex + previous.matchLength > current.matchIndex) {
      throw new Error(`edits[${previous.editIndex}] and edits[${current.editIndex}] overlap in ${path4}. Merge them into one edit or target disjoint regions.`);
    }
  }
  const baseContent = normalizedContent;
  const newContent = usedFuzzyMatch ? applyReplacementsPreservingUnchangedLines(normalizedContent, replacementBaseContent, matchedEdits) : applyReplacements(replacementBaseContent, matchedEdits);
  if (baseContent === newContent) {
    throw getNoChangeError(path4, normalizedEdits.length);
  }
  return { baseContent, newContent };
}
__name(applyEditsToNormalizedContent, "applyEditsToNormalizedContent");
function generateUnifiedPatch(path4, oldContent, newContent, contextLines = 4) {
  return Diff.createTwoFilesPatch(path4, path4, oldContent, newContent, void 0, void 0, {
    context: contextLines,
    headerOptions: Diff.FILE_HEADERS_ONLY
  });
}
__name(generateUnifiedPatch, "generateUnifiedPatch");
function generateDiffString(oldContent, newContent, contextLines = 4) {
  const parts = Diff.diffLines(oldContent, newContent);
  const output = [];
  const oldLines = oldContent.split("\n");
  const newLines = newContent.split("\n");
  const maxLineNum = Math.max(oldLines.length, newLines.length);
  const lineNumWidth = String(maxLineNum).length;
  let oldLineNum = 1;
  let newLineNum = 1;
  let lastWasChange = false;
  let firstChangedLine;
  for (let i = 0; i < parts.length; i++) {
    const part = parts[i];
    const raw = part.value.split("\n");
    if (raw[raw.length - 1] === "") {
      raw.pop();
    }
    if (part.added || part.removed) {
      if (firstChangedLine === void 0) {
        firstChangedLine = newLineNum;
      }
      for (const line of raw) {
        if (part.added) {
          const lineNum = String(newLineNum).padStart(lineNumWidth, " ");
          output.push(`+${lineNum} ${line}`);
          newLineNum++;
        } else {
          const lineNum = String(oldLineNum).padStart(lineNumWidth, " ");
          output.push(`-${lineNum} ${line}`);
          oldLineNum++;
        }
      }
      lastWasChange = true;
    } else {
      const nextPartIsChange = i < parts.length - 1 && (parts[i + 1].added || parts[i + 1].removed);
      const hasLeadingChange = lastWasChange;
      const hasTrailingChange = nextPartIsChange;
      if (hasLeadingChange && hasTrailingChange) {
        if (raw.length <= contextLines * 2) {
          for (const line of raw) {
            const lineNum = String(oldLineNum).padStart(lineNumWidth, " ");
            output.push(` ${lineNum} ${line}`);
            oldLineNum++;
            newLineNum++;
          }
        } else {
          const leadingLines = raw.slice(0, contextLines);
          const trailingLines = raw.slice(raw.length - contextLines);
          const skippedLines = raw.length - leadingLines.length - trailingLines.length;
          for (const line of leadingLines) {
            const lineNum = String(oldLineNum).padStart(lineNumWidth, " ");
            output.push(` ${lineNum} ${line}`);
            oldLineNum++;
            newLineNum++;
          }
          output.push(` ${"".padStart(lineNumWidth, " ")} ...`);
          oldLineNum += skippedLines;
          newLineNum += skippedLines;
          for (const line of trailingLines) {
            const lineNum = String(oldLineNum).padStart(lineNumWidth, " ");
            output.push(` ${lineNum} ${line}`);
            oldLineNum++;
            newLineNum++;
          }
        }
      } else if (hasLeadingChange) {
        const shownLines = raw.slice(0, contextLines);
        const skippedLines = raw.length - shownLines.length;
        for (const line of shownLines) {
          const lineNum = String(oldLineNum).padStart(lineNumWidth, " ");
          output.push(` ${lineNum} ${line}`);
          oldLineNum++;
          newLineNum++;
        }
        if (skippedLines > 0) {
          output.push(` ${"".padStart(lineNumWidth, " ")} ...`);
          oldLineNum += skippedLines;
          newLineNum += skippedLines;
        }
      } else if (hasTrailingChange) {
        const skippedLines = Math.max(0, raw.length - contextLines);
        if (skippedLines > 0) {
          output.push(` ${"".padStart(lineNumWidth, " ")} ...`);
          oldLineNum += skippedLines;
          newLineNum += skippedLines;
        }
        for (const line of raw.slice(skippedLines)) {
          const lineNum = String(oldLineNum).padStart(lineNumWidth, " ");
          output.push(` ${lineNum} ${line}`);
          oldLineNum++;
          newLineNum++;
        }
      } else {
        oldLineNum += raw.length;
        newLineNum += raw.length;
      }
      lastWasChange = false;
    }
  }
  return { diff: output.join("\n"), firstChangedLine };
}
__name(generateDiffString, "generateDiffString");
async function computeEditsDiff(path4, edits, cwd) {
  const absolutePath = resolveToCwd(path4, cwd);
  try {
    try {
      await access2(absolutePath, constants3.R_OK);
    } catch (error) {
      const errorMessage = error instanceof Error && "code" in error ? `Error code: ${error.code}` : String(error);
      return { error: `Could not edit file: ${path4}. ${errorMessage}.` };
    }
    const rawContent = await readFile(absolutePath, "utf-8");
    const { text: content } = splitBom(rawContent);
    const normalizedContent = normalizeToLF(content);
    const { baseContent, newContent } = applyEditsToNormalizedContent(normalizedContent, edits, path4);
    return generateDiffString(baseContent, newContent);
  } catch (err) {
    return { error: err instanceof Error ? err.message : String(err) };
  }
}
__name(computeEditsDiff, "computeEditsDiff");

// pi-dist/pi-coding-agent/core/tools/file-mutation-queue.js
import { realpath } from "node:fs/promises";
import { resolve } from "node:path";
var fileMutationQueues = /* @__PURE__ */ new Map();
var registrationQueue = Promise.resolve();
function isMissingPathError(error) {
  return typeof error === "object" && error !== null && "code" in error && (error.code === "ENOENT" || error.code === "ENOTDIR");
}
__name(isMissingPathError, "isMissingPathError");
async function getMutationQueueKey(filePath) {
  const resolvedPath = resolve(filePath);
  try {
    return await realpath(resolvedPath);
  } catch (error) {
    if (isMissingPathError(error)) {
      return resolvedPath;
    }
    throw error;
  }
}
__name(getMutationQueueKey, "getMutationQueueKey");
async function withFileMutationQueue(filePath, fn) {
  const registration = registrationQueue.then(async () => {
    const key2 = await getMutationQueueKey(filePath);
    const currentQueue2 = fileMutationQueues.get(key2) ?? Promise.resolve();
    let releaseNext2;
    const nextQueue = new Promise((resolveQueue) => {
      releaseNext2 = resolveQueue;
    });
    const chainedQueue2 = currentQueue2.then(() => nextQueue);
    fileMutationQueues.set(key2, chainedQueue2);
    return { key: key2, currentQueue: currentQueue2, chainedQueue: chainedQueue2, releaseNext: releaseNext2 };
  });
  registrationQueue = registration.then(() => void 0, () => void 0);
  const { key, currentQueue, chainedQueue, releaseNext } = await registration;
  await currentQueue;
  try {
    return await fn();
  } finally {
    releaseNext();
    if (fileMutationQueues.get(key) === chainedQueue) {
      fileMutationQueues.delete(key);
    }
  }
}
__name(withFileMutationQueue, "withFileMutationQueue");

// pi-dist/pi-coding-agent/core/tools/renderers/edit.js
import { Box, Container as Container2, Spacer as Spacer2, Text as Text2 } from "../../../pi-tui.mjs";

// pi-dist/pi-coding-agent/modes/interactive/components/diff.js
import * as Diff2 from "../../../diff/libesm/index.js";
function parseDiffLine(line) {
  const match = line.match(/^([+-\s])(\s*\d*)\s(.*)$/);
  if (!match)
    return null;
  return { prefix: match[1], lineNum: match[2], content: match[3] };
}
__name(parseDiffLine, "parseDiffLine");
function replaceTabs2(text) {
  return text.replace(/\t/g, "   ");
}
__name(replaceTabs2, "replaceTabs");
function renderIntraLineDiff(oldContent, newContent) {
  const wordDiff = Diff2.diffWords(oldContent, newContent);
  let removedLine = "";
  let addedLine = "";
  let isFirstRemoved = true;
  let isFirstAdded = true;
  for (const part of wordDiff) {
    if (part.removed) {
      let value = part.value;
      if (isFirstRemoved) {
        const leadingWs = value.match(/^(\s*)/)?.[1] || "";
        value = value.slice(leadingWs.length);
        removedLine += leadingWs;
        isFirstRemoved = false;
      }
      if (value) {
        removedLine += theme.inverse(value);
      }
    } else if (part.added) {
      let value = part.value;
      if (isFirstAdded) {
        const leadingWs = value.match(/^(\s*)/)?.[1] || "";
        value = value.slice(leadingWs.length);
        addedLine += leadingWs;
        isFirstAdded = false;
      }
      if (value) {
        addedLine += theme.inverse(value);
      }
    } else {
      removedLine += part.value;
      addedLine += part.value;
    }
  }
  return { removedLine, addedLine };
}
__name(renderIntraLineDiff, "renderIntraLineDiff");
function renderDiff(diffText, _options = {}) {
  const lines = diffText.split("\n");
  const result = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    const parsed = parseDiffLine(line);
    if (!parsed) {
      result.push(theme.fg("toolDiffContext", line));
      i++;
      continue;
    }
    if (parsed.prefix === "-") {
      const removedLines = [];
      while (i < lines.length) {
        const p = parseDiffLine(lines[i]);
        if (!p || p.prefix !== "-")
          break;
        removedLines.push({ lineNum: p.lineNum, content: p.content });
        i++;
      }
      const addedLines = [];
      while (i < lines.length) {
        const p = parseDiffLine(lines[i]);
        if (!p || p.prefix !== "+")
          break;
        addedLines.push({ lineNum: p.lineNum, content: p.content });
        i++;
      }
      if (removedLines.length === 1 && addedLines.length === 1) {
        const removed = removedLines[0];
        const added = addedLines[0];
        const { removedLine, addedLine } = renderIntraLineDiff(replaceTabs2(removed.content), replaceTabs2(added.content));
        result.push(theme.fg("toolDiffRemoved", `-${removed.lineNum} ${removedLine}`));
        result.push(theme.fg("toolDiffAdded", `+${added.lineNum} ${addedLine}`));
      } else {
        for (const removed of removedLines) {
          result.push(theme.fg("toolDiffRemoved", `-${removed.lineNum} ${replaceTabs2(removed.content)}`));
        }
        for (const added of addedLines) {
          result.push(theme.fg("toolDiffAdded", `+${added.lineNum} ${replaceTabs2(added.content)}`));
        }
      }
    } else if (parsed.prefix === "+") {
      result.push(theme.fg("toolDiffAdded", `+${parsed.lineNum} ${replaceTabs2(parsed.content)}`));
      i++;
    } else {
      result.push(theme.fg("toolDiffContext", ` ${parsed.lineNum} ${replaceTabs2(parsed.content)}`));
      i++;
    }
  }
  return result.join("\n");
}
__name(renderDiff, "renderDiff");

// pi-dist/pi-coding-agent/core/tools/renderers/edit.js
function createEditCallRenderComponent() {
  return Object.assign(new Box(1, 1, (text) => text), {
    preview: void 0,
    previewArgsKey: void 0,
    previewPending: false,
    settledError: false
  });
}
__name(createEditCallRenderComponent, "createEditCallRenderComponent");
function getEditCallRenderComponent(state, lastComponent) {
  if (lastComponent instanceof Box) {
    const component2 = lastComponent;
    state.callComponent = component2;
    return component2;
  }
  if (state.callComponent) {
    return state.callComponent;
  }
  const component = createEditCallRenderComponent();
  state.callComponent = component;
  return component;
}
__name(getEditCallRenderComponent, "getEditCallRenderComponent");
function getRenderablePreviewInput(args) {
  if (!args) {
    return null;
  }
  const path4 = typeof args.path === "string" ? args.path : typeof args.file_path === "string" ? args.file_path : null;
  if (!path4) {
    return null;
  }
  if (Array.isArray(args.edits) && args.edits.length > 0 && args.edits.every((edit) => typeof edit?.oldText === "string" && typeof edit?.newText === "string")) {
    return { path: path4, edits: args.edits };
  }
  if (typeof args.oldText === "string" && typeof args.newText === "string") {
    return { path: path4, edits: [{ oldText: args.oldText, newText: args.newText }] };
  }
  return null;
}
__name(getRenderablePreviewInput, "getRenderablePreviewInput");
function formatEditCall(args, theme2, cwd) {
  const pathDisplay = renderToolPath(str(args?.file_path ?? args?.path), theme2, cwd);
  return `${theme2.fg("toolTitle", theme2.bold("edit"))} ${pathDisplay}`;
}
__name(formatEditCall, "formatEditCall");
function formatEditResult(args, preview, result, theme2, isError) {
  const rawPath = str(args?.file_path ?? args?.path);
  const previewDiff = preview && !("error" in preview) ? preview.diff : void 0;
  const previewError = preview && "error" in preview ? preview.error : void 0;
  if (isError) {
    const errorText = result.content.filter((c) => c.type === "text").map((c) => c.text || "").join("\n");
    if (!errorText || errorText === previewError) {
      return void 0;
    }
    return theme2.fg("error", errorText);
  }
  const resultDiff = result.details?.diff;
  if (resultDiff && resultDiff !== previewDiff) {
    return renderDiff(resultDiff, { filePath: rawPath ?? void 0 });
  }
  return void 0;
}
__name(formatEditResult, "formatEditResult");
function getEditHeaderBg(preview, settledError, theme2) {
  if (preview) {
    if ("error" in preview) {
      return (text) => theme2.bg("toolErrorBg", text);
    }
    return (text) => theme2.bg("toolSuccessBg", text);
  }
  if (settledError) {
    return (text) => theme2.bg("toolErrorBg", text);
  }
  return (text) => theme2.bg("toolPendingBg", text);
}
__name(getEditHeaderBg, "getEditHeaderBg");
function buildEditCallComponent(component, args, theme2, cwd) {
  component.setBgFn(getEditHeaderBg(component.preview, component.settledError, theme2));
  component.clear();
  component.addChild(new Text2(formatEditCall(args, theme2, cwd), 0, 0));
  if (!component.preview) {
    return component;
  }
  const body = "error" in component.preview ? theme2.fg("error", component.preview.error) : renderDiff(component.preview.diff);
  component.addChild(new Spacer2(1));
  component.addChild(new Text2(body, 0, 0));
  return component;
}
__name(buildEditCallComponent, "buildEditCallComponent");
function setEditPreview(component, preview, argsKey) {
  const current = component.preview;
  const changed = current === void 0 || ("error" in current && "error" in preview ? current.error !== preview.error : "error" in current !== "error" in preview) || !("error" in current) && !("error" in preview) && (current.diff !== preview.diff || current.firstChangedLine !== preview.firstChangedLine);
  component.preview = preview;
  component.previewArgsKey = argsKey;
  component.previewPending = false;
  return changed;
}
__name(setEditPreview, "setEditPreview");
var editRenderers = {
  renderCall(args, theme2, context) {
    const component = getEditCallRenderComponent(context.state, context.lastComponent);
    const previewInput = getRenderablePreviewInput(args);
    const argsKey = previewInput ? JSON.stringify({ path: previewInput.path, edits: previewInput.edits }) : void 0;
    if (component.previewArgsKey !== argsKey) {
      component.preview = void 0;
      component.previewArgsKey = argsKey;
      component.previewPending = false;
      component.settledError = false;
    }
    if (context.argsComplete && previewInput && !component.preview && !component.previewPending) {
      component.previewPending = true;
      const requestKey = argsKey;
      void computeEditsDiff(previewInput.path, previewInput.edits, context.cwd).then((preview) => {
        if (component.previewArgsKey === requestKey) {
          setEditPreview(component, preview, requestKey);
          context.invalidate();
        }
      });
    }
    return buildEditCallComponent(component, args, theme2, context.cwd);
  },
  renderResult(result, _options, theme2, context) {
    const callComponent = context.state.callComponent;
    const previewInput = getRenderablePreviewInput(context.args);
    const argsKey = previewInput ? JSON.stringify({ path: previewInput.path, edits: previewInput.edits }) : void 0;
    const typedResult = result;
    const resultDiff = !context.isError ? typedResult.details?.diff : void 0;
    let changed = false;
    if (callComponent) {
      if (typeof resultDiff === "string") {
        changed = setEditPreview(callComponent, { diff: resultDiff, firstChangedLine: typedResult.details?.firstChangedLine }, argsKey) || changed;
      }
      if (callComponent.settledError !== context.isError) {
        callComponent.settledError = context.isError;
        changed = true;
      }
      if (changed) {
        buildEditCallComponent(callComponent, context.args, theme2, context.cwd);
      }
    }
    const output = formatEditResult(context.args, callComponent?.preview, typedResult, theme2, context.isError);
    const component = context.lastComponent ?? new Container2();
    component.clear();
    if (!output) {
      return component;
    }
    component.addChild(new Spacer2(1));
    component.addChild(new Text2(output, 1, 0));
    return component;
  }
};

// pi-dist/pi-coding-agent/core/tools/edit.js
var replaceEditSchema = Type2.Object({
  oldText: Type2.String({
    description: "Exact text for one targeted replacement. It must be unique in the original file and must not overlap with any other edits[].oldText in the same call."
  }),
  newText: Type2.String({ description: "Replacement text for this targeted edit." })
}, {});
var editSchema = Type2.Object({
  path: Type2.String({ description: "Path to the file to edit (relative or absolute)" }),
  edits: Type2.Array(replaceEditSchema, {
    description: "One or more targeted replacements. Each edit is matched against the original file, not incrementally. Do not include overlapping or nested edits. If two changes touch the same block or nearby lines, merge them into one edit instead."
  })
}, {});
var editToolSystemPromptContribution = {
  snippet: "Make precise file edits with exact text replacement, including multiple disjoint edits in one call",
  guidelines: [
    "Use edit for precise changes (edits[].oldText must match exactly)",
    "When changing multiple separate locations in one file, use one edit call with multiple entries in edits[] instead of multiple edit calls",
    "Each edits[].oldText is matched against the original file, not after earlier edits are applied. Do not emit overlapping or nested edits. Merge nearby changes into one edit.",
    "Keep edits[].oldText as small as possible while still being unique in the file. Do not pad with large unchanged regions."
  ]
};
function isSingleEditInput(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return false;
  }
  const edit = value;
  return typeof edit.oldText === "string" && typeof edit.newText === "string";
}
__name(isSingleEditInput, "isSingleEditInput");
var defaultEditOperations = {
  readFile: /* @__PURE__ */ __name((path4) => fsReadFile(path4), "readFile"),
  writeFile: /* @__PURE__ */ __name((path4, content) => fsWriteFile(path4, content, "utf-8"), "writeFile"),
  access: /* @__PURE__ */ __name((path4) => fsAccess2(path4, constants4.R_OK | constants4.W_OK), "access")
};
function prepareEditArguments(input) {
  if (!input || typeof input !== "object") {
    return input;
  }
  const args = input;
  if (typeof args.edits === "string") {
    try {
      const parsed = JSON.parse(args.edits);
      if (Array.isArray(parsed)) {
        args.edits = parsed;
      } else if (isSingleEditInput(parsed)) {
        args.edits = [parsed];
      }
    } catch {
    }
  } else if (isSingleEditInput(args.edits)) {
    args.edits = [args.edits];
  }
  const legacy = args;
  if (typeof legacy.oldText !== "string" || typeof legacy.newText !== "string") {
    return args;
  }
  const edits = Array.isArray(legacy.edits) ? [...legacy.edits] : [];
  edits.push({ oldText: legacy.oldText, newText: legacy.newText });
  const { oldText: _oldText, newText: _newText, ...rest } = legacy;
  return { ...rest, edits };
}
__name(prepareEditArguments, "prepareEditArguments");
function validateEditInput(input) {
  if (!Array.isArray(input.edits) || input.edits.length === 0) {
    throw new Error("Edit tool input is invalid. edits must contain at least one replacement.");
  }
  return { path: input.path, edits: input.edits };
}
__name(validateEditInput, "validateEditInput");
function createEditToolDefinition(cwd, options) {
  const ops = options?.operations ?? defaultEditOperations;
  return {
    name: "edit",
    label: "edit",
    description: "Edit a single file using exact text replacement. Every edits[].oldText must match a unique, non-overlapping region of the original file. If two changes affect the same block or nearby lines, merge them into one edit instead of emitting overlapping edits. Do not include large unchanged regions just to connect distant changes.",
    promptSnippet: editToolSystemPromptContribution.snippet,
    promptGuidelines: [...editToolSystemPromptContribution.guidelines],
    parameters: editSchema,
    constrainedSampling: { type: "json_schema", strict: "prefer" },
    renderShell: "self",
    prepareArguments: prepareEditArguments,
    async execute(_toolCallId, input, signal, _onUpdate, ctx) {
      const { path: path4, edits } = validateEditInput(input);
      const absolutePath = resolveToCwd(path4, ctx?.cwd || cwd);
      return withFileMutationQueue(absolutePath, async () => {
        const throwIfAborted = /* @__PURE__ */ __name(() => {
          if (signal?.aborted)
            throw new Error("Operation aborted");
        }, "throwIfAborted");
        throwIfAborted();
        try {
          await ops.access(absolutePath);
        } catch (error) {
          throwIfAborted();
          const errorMessage = error instanceof Error && "code" in error ? `Error code: ${error.code}` : String(error);
          throw new Error(`Could not edit file: ${path4}. ${errorMessage}.`);
        }
        throwIfAborted();
        const buffer = await ops.readFile(absolutePath);
        const rawContent = buffer.toString("utf-8");
        throwIfAborted();
        const { bom, text: content } = splitBom(rawContent);
        const originalEnding = detectLineEnding(content);
        const normalizedContent = normalizeToLF(content);
        const { baseContent, newContent } = applyEditsToNormalizedContent(normalizedContent, edits, path4);
        throwIfAborted();
        const finalContent = bom + restoreLineEndings(newContent, originalEnding);
        await ops.writeFile(absolutePath, finalContent);
        throwIfAborted();
        const diffResult = generateDiffString(baseContent, newContent);
        const patch = generateUnifiedPatch(path4, baseContent, newContent);
        return {
          content: [
            {
              type: "text",
              text: `Successfully replaced ${edits.length} block(s) in ${path4}.`
            }
          ],
          details: { diff: diffResult.diff, patch, firstChangedLine: diffResult.firstChangedLine }
        };
      });
    },
    ...editRenderers
  };
}
__name(createEditToolDefinition, "createEditToolDefinition");
function createEditTool(cwd, options) {
  return wrapToolDefinition(createEditToolDefinition(cwd, options));
}
__name(createEditTool, "createEditTool");

// pi-dist/pi-coding-agent/core/tools/find.js
import { createInterface } from "node:readline";
import { spawn as spawn2 } from "child_process";
import path from "path";
import { Type as Type3 } from "../../../typebox.mjs";

// pi-dist/pi-coding-agent/utils/tools-manager.js
import { spawnSync } from "child_process";
import { chmodSync, createWriteStream, existsSync, mkdirSync, readdirSync, renameSync, rmSync } from "fs";
import { arch, platform } from "os";
import { join } from "path";
import { Readable } from "stream";
import { pipeline } from "stream/promises";

// pi-dist/pi-coding-agent/utils/management-http.js
var RETRYABLE_STATUS_CODES = /* @__PURE__ */ new Set([408, 425, 429, 500, 502, 503, 504]);
async function fetchWithRetry(input, init = void 0, options = {}) {
  const maxRetries = options.maxRetries === void 0 || !Number.isFinite(options.maxRetries) ? 2 : Math.max(0, Math.floor(options.maxRetries));
  const retryOnStatus = options.retryOnStatus ?? true;
  const parentSignal = init?.signal ?? void 0;
  const timeoutSignal = options.timeoutMs !== void 0 && options.timeoutMs > 0 ? AbortSignal.timeout(options.timeoutMs) : void 0;
  const attemptTimeoutMs = options.attemptTimeoutMs !== void 0 && options.attemptTimeoutMs > 0 ? options.attemptTimeoutMs : void 0;
  for (let attempt = 0; ; attempt++) {
    parentSignal?.throwIfAborted();
    timeoutSignal?.throwIfAborted();
    const attemptTimeoutSignal = attemptTimeoutMs ? AbortSignal.timeout(attemptTimeoutMs) : void 0;
    const signals = [parentSignal, timeoutSignal, attemptTimeoutSignal].filter((signal2) => signal2 !== void 0);
    const signal = signals.length > 1 ? AbortSignal.any(signals) : signals[0];
    try {
      const response = await fetch(input, signal ? { ...init, signal } : init);
      const shouldRetry = retryOnStatus && RETRYABLE_STATUS_CODES.has(response.status) && attempt < maxRetries;
      if (!shouldRetry)
        return response;
      try {
        await response.body?.cancel();
      } catch {
      }
    } catch (error) {
      const attemptTimedOut = attemptTimeoutSignal?.aborted === true && !parentSignal?.aborted && !timeoutSignal?.aborted;
      if (parentSignal?.aborted || timeoutSignal?.aborted || error instanceof Error && error.name === "AbortError" && !attemptTimedOut && timeoutSignal === void 0 || attempt >= maxRetries) {
        throw error;
      }
    }
  }
}
__name(fetchWithRetry, "fetchWithRetry");

// pi-dist/pi-coding-agent/utils/tools-manager.js
var TOOLS_DIR = getBinDir();
var NETWORK_TIMEOUT_MS = 1e4;
var DOWNLOAD_TIMEOUT_MS = 12e4;
function isOfflineModeEnabled() {
  const value = process.env.PI_OFFLINE;
  if (!value)
    return false;
  return value === "1" || value.toLowerCase() === "true" || value.toLowerCase() === "yes";
}
__name(isOfflineModeEnabled, "isOfflineModeEnabled");
var TOOLS = {
  fd: {
    name: "fd",
    repo: "sharkdp/fd",
    binaryName: "fd",
    systemBinaryNames: ["fd", "fdfind"],
    tagPrefix: "v",
    getAssetName: /* @__PURE__ */ __name((version, plat, architecture) => {
      if (plat === "darwin") {
        const archStr = architecture === "arm64" ? "aarch64" : "x86_64";
        return `fd-v${version}-${archStr}-apple-darwin.tar.gz`;
      } else if (plat === "linux") {
        const archStr = architecture === "arm64" ? "aarch64" : "x86_64";
        return `fd-v${version}-${archStr}-unknown-linux-musl.tar.gz`;
      } else if (plat === "win32") {
        const archStr = architecture === "arm64" ? "aarch64" : "x86_64";
        return `fd-v${version}-${archStr}-pc-windows-msvc.zip`;
      }
      return null;
    }, "getAssetName")
  },
  rg: {
    name: "ripgrep",
    repo: "BurntSushi/ripgrep",
    binaryName: "rg",
    tagPrefix: "",
    getAssetName: /* @__PURE__ */ __name((version, plat, architecture) => {
      if (plat === "darwin") {
        const archStr = architecture === "arm64" ? "aarch64" : "x86_64";
        return `ripgrep-${version}-${archStr}-apple-darwin.tar.gz`;
      } else if (plat === "linux") {
        const archStr = architecture === "arm64" ? "aarch64" : "x86_64";
        return `ripgrep-${version}-${archStr}-unknown-linux-musl.tar.gz`;
      } else if (plat === "win32") {
        const archStr = architecture === "arm64" ? "aarch64" : "x86_64";
        return `ripgrep-${version}-${archStr}-pc-windows-msvc.zip`;
      }
      return null;
    }, "getAssetName")
  }
};
function commandExists(cmd) {
  try {
    const result = spawnSync(cmd, ["--version"], { stdio: "pipe" });
    return result.error === void 0 || result.error === null;
  } catch {
    return false;
  }
}
__name(commandExists, "commandExists");
function getToolPath(tool) {
  const config = TOOLS[tool];
  if (!config)
    return null;
  const localPath = join(TOOLS_DIR, config.binaryName + (platform() === "win32" ? ".exe" : ""));
  if (existsSync(localPath)) {
    return localPath;
  }
  const systemBinaryNames = config.systemBinaryNames ?? [config.binaryName];
  for (const systemBinaryName of systemBinaryNames) {
    if (commandExists(systemBinaryName)) {
      return systemBinaryName;
    }
  }
  return null;
}
__name(getToolPath, "getToolPath");
async function getLatestVersion(repo) {
  const response = await fetchWithRetry(`https://github.com/${repo}/releases/latest`, {
    headers: { "User-Agent": `${APP_NAME}-coding-agent` },
    redirect: "manual"
  }, { timeoutMs: NETWORK_TIMEOUT_MS });
  try {
    await response.body?.cancel();
  } catch {
  }
  const location = response.status >= 300 && response.status < 400 ? response.headers.get("location") : null;
  if (!location) {
    throw new Error(`Failed to resolve latest ${repo} release: HTTP ${response.status} without redirect`);
  }
  const tag = new URL(location, "https://github.com").pathname.split("/").pop();
  if (!tag || !location.includes("/releases/tag/")) {
    throw new Error(`Failed to resolve latest ${repo} release: unexpected redirect to ${location}`);
  }
  return decodeURIComponent(tag).replace(/^v/, "");
}
__name(getLatestVersion, "getLatestVersion");
async function downloadFile(url, dest) {
  const response = await fetchWithRetry(url, void 0, { timeoutMs: DOWNLOAD_TIMEOUT_MS });
  if (!response.ok) {
    throw new Error(`Download failed with HTTP ${response.status}: ${url}`);
  }
  if (!response.body) {
    throw new Error("No response body");
  }
  const fileStream = createWriteStream(dest);
  await pipeline(Readable.fromWeb(response.body), fileStream);
}
__name(downloadFile, "downloadFile");
function findBinaryRecursively(rootDir, binaryFileName) {
  const stack = [rootDir];
  while (stack.length > 0) {
    const currentDir = stack.pop();
    if (!currentDir)
      continue;
    const entries = readdirSync(currentDir, { withFileTypes: true });
    for (const entry of entries) {
      const fullPath = join(currentDir, entry.name);
      if (entry.isFile() && entry.name === binaryFileName) {
        return fullPath;
      }
      if (entry.isDirectory()) {
        stack.push(fullPath);
      }
    }
  }
  return null;
}
__name(findBinaryRecursively, "findBinaryRecursively");
function formatSpawnFailure(result) {
  if (result.error?.message) {
    return result.error.message;
  }
  const stderr = result.stderr?.toString().trim();
  if (stderr) {
    return stderr;
  }
  const stdout = result.stdout?.toString().trim();
  if (stdout) {
    return stdout;
  }
  return `exit status ${result.status ?? "unknown"}`;
}
__name(formatSpawnFailure, "formatSpawnFailure");
function runExtractionCommand(command, args) {
  const result = spawnSync(command, args, { stdio: "pipe" });
  if (!result.error && result.status === 0) {
    return null;
  }
  return `${command}: ${formatSpawnFailure(result)}`;
}
__name(runExtractionCommand, "runExtractionCommand");
function extractTarGzArchive(archivePath, extractDir, assetName) {
  const failure = runExtractionCommand("tar", ["xzf", archivePath, "-C", extractDir]);
  if (failure) {
    throw new Error(`Failed to extract ${assetName}: ${failure}`);
  }
}
__name(extractTarGzArchive, "extractTarGzArchive");
function getWindowsTarCommand() {
  const systemRoot = process.env.SystemRoot ?? process.env.WINDIR;
  if (systemRoot) {
    const systemTar = join(systemRoot, "System32", "tar.exe");
    if (existsSync(systemTar)) {
      return systemTar;
    }
  }
  return "tar.exe";
}
__name(getWindowsTarCommand, "getWindowsTarCommand");
function extractZipArchive(archivePath, extractDir, assetName) {
  const failures = [];
  if (platform() === "win32") {
    const tarFailure = runExtractionCommand(getWindowsTarCommand(), ["xf", archivePath, "-C", extractDir]);
    if (!tarFailure)
      return;
    failures.push(tarFailure);
    const script = "& { param($archive, $destination) $ErrorActionPreference = 'Stop'; Expand-Archive -LiteralPath $archive -DestinationPath $destination -Force }";
    const powershellFailure = runExtractionCommand("powershell.exe", [
      "-NoLogo",
      "-NoProfile",
      "-NonInteractive",
      "-ExecutionPolicy",
      "Bypass",
      "-Command",
      script,
      archivePath,
      extractDir
    ]);
    if (!powershellFailure)
      return;
    failures.push(powershellFailure);
  } else {
    const unzipFailure = runExtractionCommand("unzip", ["-q", archivePath, "-d", extractDir]);
    if (!unzipFailure)
      return;
    failures.push(unzipFailure);
    const tarFailure = runExtractionCommand("tar", ["xf", archivePath, "-C", extractDir]);
    if (!tarFailure)
      return;
    failures.push(tarFailure);
  }
  throw new Error(`Failed to extract ${assetName}: ${failures.join("; ")}`);
}
__name(extractZipArchive, "extractZipArchive");
async function downloadTool(tool) {
  const config = TOOLS[tool];
  if (!config)
    throw new Error(`Unknown tool: ${tool}`);
  const plat = platform();
  const architecture = arch();
  const version = tool === "fd" && plat === "darwin" && architecture === "x64" ? "10.3.0" : await getLatestVersion(config.repo);
  const assetName = config.getAssetName(version, plat, architecture);
  if (!assetName) {
    throw new Error(`Unsupported platform: ${plat}/${architecture}`);
  }
  mkdirSync(TOOLS_DIR, { recursive: true });
  const downloadUrl = `https://github.com/${config.repo}/releases/download/${config.tagPrefix}${version}/${assetName}`;
  const archivePath = join(TOOLS_DIR, assetName);
  const binaryExt = plat === "win32" ? ".exe" : "";
  const binaryPath = join(TOOLS_DIR, config.binaryName + binaryExt);
  await downloadFile(downloadUrl, archivePath);
  const extractDir = join(TOOLS_DIR, `extract_tmp_${config.binaryName}_${process.pid}_${Date.now()}_${Math.random().toString(36).slice(2, 10)}`);
  mkdirSync(extractDir, { recursive: true });
  try {
    if (assetName.endsWith(".tar.gz")) {
      extractTarGzArchive(archivePath, extractDir, assetName);
    } else if (assetName.endsWith(".zip")) {
      extractZipArchive(archivePath, extractDir, assetName);
    } else {
      throw new Error(`Unsupported archive format: ${assetName}`);
    }
    const binaryFileName = config.binaryName + binaryExt;
    const extractedDir = join(extractDir, assetName.replace(/\.(tar\.gz|zip)$/, ""));
    const extractedBinaryCandidates = [join(extractedDir, binaryFileName), join(extractDir, binaryFileName)];
    let extractedBinary = extractedBinaryCandidates.find((candidate) => existsSync(candidate));
    if (!extractedBinary) {
      extractedBinary = findBinaryRecursively(extractDir, binaryFileName) ?? void 0;
    }
    if (extractedBinary) {
      renameSync(extractedBinary, binaryPath);
    } else {
      throw new Error(`Binary not found in archive: expected ${binaryFileName} under ${extractDir}`);
    }
    if (plat !== "win32") {
      chmodSync(binaryPath, 493);
    }
  } finally {
    rmSync(archivePath, { force: true });
    rmSync(extractDir, { recursive: true, force: true });
  }
  return binaryPath;
}
__name(downloadTool, "downloadTool");
var TERMUX_PACKAGES = {
  fd: "fd",
  rg: "ripgrep"
};
async function ensureTool(tool, onStatus) {
  const existingPath = getToolPath(tool);
  if (existingPath) {
    return existingPath;
  }
  const config = TOOLS[tool];
  if (!config)
    return void 0;
  if (isOfflineModeEnabled()) {
    onStatus?.({ type: "warning", message: `${config.name} not found. Offline mode enabled, skipping download.` });
    return void 0;
  }
  if (platform() === "android") {
    const pkgName = TERMUX_PACKAGES[tool] ?? tool;
    onStatus?.({ type: "warning", message: `${config.name} not found. Install with: pkg install ${pkgName}` });
    return void 0;
  }
  onStatus?.({ type: "info", message: `${config.name} not found. Downloading...` });
  try {
    const path4 = await downloadTool(tool);
    onStatus?.({ type: "info", message: `${config.name} installed to ${path4}` });
    return path4;
  } catch (e) {
    const messages = [];
    for (let current = e, depth = 0; current instanceof Error && depth < 5; current = current.cause, depth++) {
      if (!messages.includes(current.message))
        messages.push(current.message);
    }
    onStatus?.({
      type: "warning",
      message: `Failed to download ${config.name}: ${messages.length > 0 ? messages.join(": ") : String(e)}`
    });
    return void 0;
  }
}
__name(ensureTool, "ensureTool");

// pi-dist/pi-coding-agent/core/tools/renderers/find.js
import { Text as Text3 } from "../../../pi-tui.mjs";
function formatFindCall(args, theme2) {
  const pattern = str(args?.pattern);
  const rawPath = str(args?.path);
  const path4 = rawPath !== null ? shortenPath(rawPath || ".") : null;
  const limit = args?.limit;
  const invalidArg = invalidArgText(theme2);
  let text = theme2.fg("toolTitle", theme2.bold("find")) + " " + (pattern === null ? invalidArg : theme2.fg("accent", pattern || "")) + theme2.fg("toolOutput", ` in ${path4 === null ? invalidArg : path4}`);
  if (limit !== void 0) {
    text += theme2.fg("toolOutput", ` (limit ${limit})`);
  }
  return text;
}
__name(formatFindCall, "formatFindCall");
function formatFindResult(result, options, theme2, showImages) {
  const output = getTextOutput(result, showImages).trim();
  let text = "";
  if (output) {
    const lines = output.split("\n");
    const maxLines = options.expanded ? lines.length : 20;
    const displayLines = lines.slice(0, maxLines);
    const remaining = lines.length - maxLines;
    text += `
${displayLines.map((line) => theme2.fg("toolOutput", line)).join("\n")}`;
    if (remaining > 0) {
      text += `${theme2.fg("muted", `
... (${remaining} more lines,`)} ${keyHint("app.tools.expand", "to expand")}${theme2.fg("muted", ")")}`;
    }
  }
  const resultLimit = result.details?.resultLimitReached;
  const truncation = result.details?.truncation;
  if (resultLimit || truncation?.truncated) {
    const warnings = [];
    if (resultLimit)
      warnings.push(`${resultLimit} results limit`);
    if (truncation?.truncated)
      warnings.push(`${formatSize(truncation.maxBytes ?? DEFAULT_MAX_BYTES)} limit`);
    text += `
${theme2.fg("warning", `[Truncated: ${warnings.join(", ")}]`)}`;
  }
  return text;
}
__name(formatFindResult, "formatFindResult");
var findRenderers = {
  renderCall(args, theme2, context) {
    const text = context.lastComponent ?? new Text3("", 0, 0);
    text.setText(formatFindCall(args, theme2));
    return text;
  },
  renderResult(result, options, theme2, context) {
    const text = context.lastComponent ?? new Text3("", 0, 0);
    text.setText(formatFindResult(result, options, theme2, context.showImages));
    return text;
  }
};

// pi-dist/pi-coding-agent/core/tools/find.js
function relativizeFindResultPath(resultPath, searchPath, pathModule = path) {
  const hadTrailingSeparator = resultPath.endsWith(pathModule.sep) || pathModule.sep === "\\" && resultPath.endsWith("/");
  const relativePath = pathModule.isAbsolute(resultPath) ? pathModule.relative(searchPath, resultPath) : resultPath;
  const posixPath = relativePath.split(pathModule.sep).join("/");
  return hadTrailingSeparator && !posixPath.endsWith("/") ? `${posixPath}/` : posixPath;
}
__name(relativizeFindResultPath, "relativizeFindResultPath");
var findSchema = Type3.Object({
  pattern: Type3.String({
    description: "Glob pattern to match files, e.g. '*.ts', '**/*.json', or 'src/**/*.spec.ts'"
  }),
  path: Type3.Optional(Type3.String({ description: "Directory to search in (default: current directory)" })),
  limit: Type3.Optional(Type3.Number({ description: "Maximum number of results (default: 1000)" }))
});
var findToolSystemPromptContribution = {
  snippet: "Find files by glob pattern (respects .gitignore)",
  guidelines: []
};
var DEFAULT_LIMIT = 1e3;
var defaultFindOperations = {
  exists: pathExists,
  // This is a placeholder. Actual fd execution happens in execute() when no custom glob is provided.
  glob: /* @__PURE__ */ __name(() => [], "glob")
};
function createFindToolDefinition(cwd, options) {
  const customOps = options?.operations;
  return {
    name: "find",
    label: "find",
    description: `Search for files by glob pattern. Returns matching file paths relative to the search directory. Respects .gitignore. Output is truncated to ${DEFAULT_LIMIT} results or ${DEFAULT_MAX_BYTES / 1024}KB (whichever is hit first).`,
    promptSnippet: findToolSystemPromptContribution.snippet,
    parameters: findSchema,
    async execute(_toolCallId, { pattern, path: searchDir, limit }, signal, _onUpdate, ctx) {
      return new Promise((resolve2, reject) => {
        if (signal?.aborted) {
          reject(new Error("Operation aborted"));
          return;
        }
        let settled = false;
        let stopChild;
        const settle = /* @__PURE__ */ __name((fn) => {
          if (settled)
            return;
          settled = true;
          signal?.removeEventListener("abort", onAbort);
          stopChild = void 0;
          fn();
        }, "settle");
        const onAbort = /* @__PURE__ */ __name(() => {
          stopChild?.();
          settle(() => reject(new Error("Operation aborted")));
        }, "onAbort");
        signal?.addEventListener("abort", onAbort, { once: true });
        (async () => {
          try {
            const searchPath = resolveToCwd(searchDir || ".", ctx?.cwd || cwd);
            const effectiveLimit = limit ?? DEFAULT_LIMIT;
            const ops = customOps ?? defaultFindOperations;
            if (customOps?.glob) {
              if (!await ops.exists(searchPath)) {
                settle(() => reject(new Error(`Path not found: ${searchPath}`)));
                return;
              }
              if (signal?.aborted) {
                settle(() => reject(new Error("Operation aborted")));
                return;
              }
              const results = await ops.glob(pattern, searchPath, {
                ignore: ["**/node_modules/**", "**/.git/**"],
                limit: effectiveLimit
              });
              if (signal?.aborted) {
                settle(() => reject(new Error("Operation aborted")));
                return;
              }
              if (results.length === 0) {
                settle(() => resolve2({
                  content: [{ type: "text", text: "No files found matching pattern" }],
                  details: void 0
                }));
                return;
              }
              const relativized = results.map((p) => relativizeFindResultPath(p, searchPath));
              const resultLimitReached = relativized.length >= effectiveLimit;
              const rawOutput = relativized.join("\n");
              const truncation = truncateHead(rawOutput, { maxLines: Number.MAX_SAFE_INTEGER });
              let resultOutput = truncation.content;
              const details = {};
              const notices = [];
              if (resultLimitReached) {
                notices.push(`${effectiveLimit} results limit reached`);
                details.resultLimitReached = effectiveLimit;
              }
              if (truncation.truncated) {
                notices.push(`${formatSize(DEFAULT_MAX_BYTES)} limit reached`);
                details.truncation = truncation;
              }
              if (notices.length > 0) {
                resultOutput += `

[${notices.join(". ")}]`;
              }
              settle(() => resolve2({
                content: [{ type: "text", text: resultOutput }],
                details: Object.keys(details).length > 0 ? details : void 0
              }));
              return;
            }
            const fdPath = await ensureTool("fd");
            if (signal?.aborted) {
              settle(() => reject(new Error("Operation aborted")));
              return;
            }
            if (!fdPath) {
              settle(() => reject(new Error("fd is not available and could not be downloaded")));
              return;
            }
            const args = ["--glob", "--color=never", "--hidden"];
            let insideGitRepo = false;
            for (let current = searchPath; ; ) {
              if (await pathExists(path.join(current, ".git"))) {
                insideGitRepo = true;
                break;
              }
              const parent = path.dirname(current);
              if (parent === current)
                break;
              current = parent;
            }
            if (!insideGitRepo)
              args.push("--no-require-git");
            args.push("--max-results", String(effectiveLimit));
            let effectivePattern = pattern;
            if (pattern.includes("/")) {
              args.push("--full-path");
              if (!pattern.startsWith("/") && !pattern.startsWith("**/") && pattern !== "**") {
                effectivePattern = `**/${pattern}`;
              }
              if (process.platform === "win32")
                effectivePattern = effectivePattern.replaceAll("/", String.raw`[/\\]`);
            }
            args.push("--", effectivePattern, searchPath);
            const child = spawn2(fdPath, args, { stdio: ["ignore", "pipe", "pipe"] });
            const rl = createInterface({ input: child.stdout });
            let stderr = "";
            const lines = [];
            stopChild = /* @__PURE__ */ __name(() => {
              if (!child.killed) {
                child.kill();
              }
            }, "stopChild");
            const cleanup = /* @__PURE__ */ __name(() => {
              rl.close();
            }, "cleanup");
            child.stderr?.on("data", (chunk) => {
              stderr += chunk.toString();
            });
            rl.on("line", (line) => {
              lines.push(line);
            });
            child.on("error", (error) => {
              cleanup();
              settle(() => reject(new Error(`Failed to run fd: ${error.message}`)));
            });
            child.on("close", (code) => {
              cleanup();
              if (signal?.aborted) {
                settle(() => reject(new Error("Operation aborted")));
                return;
              }
              const output = lines.join("\n");
              if (code !== 0) {
                const errorMsg = stderr.trim() || `fd exited with code ${code}`;
                if (!output) {
                  settle(() => reject(new Error(errorMsg)));
                  return;
                }
              }
              if (!output) {
                settle(() => resolve2({
                  content: [{ type: "text", text: "No files found matching pattern" }],
                  details: void 0
                }));
                return;
              }
              const relativized = [];
              for (const rawLine of lines) {
                const line = rawLine.replace(/\r$/, "").trim();
                if (!line)
                  continue;
                relativized.push(relativizeFindResultPath(line, searchPath));
              }
              const resultLimitReached = relativized.length >= effectiveLimit;
              const rawOutput = relativized.join("\n");
              const truncation = truncateHead(rawOutput, { maxLines: Number.MAX_SAFE_INTEGER });
              let resultOutput = truncation.content;
              const details = {};
              const notices = [];
              if (resultLimitReached) {
                notices.push(`${effectiveLimit} results limit reached. Use limit=${effectiveLimit * 2} for more, or refine pattern`);
                details.resultLimitReached = effectiveLimit;
              }
              if (truncation.truncated) {
                notices.push(`${formatSize(DEFAULT_MAX_BYTES)} limit reached`);
                details.truncation = truncation;
              }
              if (notices.length > 0) {
                resultOutput += `

[${notices.join(". ")}]`;
              }
              settle(() => resolve2({
                content: [{ type: "text", text: resultOutput }],
                details: Object.keys(details).length > 0 ? details : void 0
              }));
            });
          } catch (e) {
            if (signal?.aborted) {
              settle(() => reject(new Error("Operation aborted")));
              return;
            }
            const error = e instanceof Error ? e : new Error(String(e));
            settle(() => reject(error));
          }
        })();
      });
    },
    ...findRenderers
  };
}
__name(createFindToolDefinition, "createFindToolDefinition");
function createFindTool(cwd, options) {
  return wrapToolDefinition(createFindToolDefinition(cwd, options));
}
__name(createFindTool, "createFindTool");

// pi-dist/pi-coding-agent/core/tools/grep.js
import { readFile as fsReadFile2, stat as fsStat } from "node:fs/promises";
import { createInterface as createInterface2 } from "node:readline";
import { spawn as spawn3 } from "child_process";
import path2 from "path";
import { Type as Type4 } from "../../../typebox.mjs";

// pi-dist/pi-coding-agent/core/tools/renderers/grep.js
import { Text as Text4 } from "../../../pi-tui.mjs";
function formatGrepCall(args, theme2) {
  const pattern = str(args?.pattern);
  const rawPath = str(args?.path);
  const path4 = rawPath !== null ? shortenPath(rawPath || ".") : null;
  const glob = str(args?.glob);
  const limit = args?.limit;
  const invalidArg = invalidArgText(theme2);
  let text = theme2.fg("toolTitle", theme2.bold("grep")) + " " + (pattern === null ? invalidArg : theme2.fg("accent", `/${pattern || ""}/`)) + theme2.fg("toolOutput", ` in ${path4 === null ? invalidArg : path4}`);
  if (glob)
    text += theme2.fg("toolOutput", ` (${glob})`);
  if (limit !== void 0)
    text += theme2.fg("toolOutput", ` limit ${limit}`);
  return text;
}
__name(formatGrepCall, "formatGrepCall");
function formatGrepResult(result, options, theme2, showImages) {
  const output = getTextOutput(result, showImages).trim();
  let text = "";
  if (output) {
    const lines = output.split("\n");
    const maxLines = options.expanded ? lines.length : 15;
    const displayLines = lines.slice(0, maxLines);
    const remaining = lines.length - maxLines;
    text += `
${displayLines.map((line) => theme2.fg("toolOutput", line)).join("\n")}`;
    if (remaining > 0) {
      text += `${theme2.fg("muted", `
... (${remaining} more lines,`)} ${keyHint("app.tools.expand", "to expand")}${theme2.fg("muted", ")")}`;
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
    text += `
${theme2.fg("warning", `[Truncated: ${warnings.join(", ")}]`)}`;
  }
  return text;
}
__name(formatGrepResult, "formatGrepResult");
var grepRenderers = {
  renderCall(args, theme2, context) {
    const text = context.lastComponent ?? new Text4("", 0, 0);
    text.setText(formatGrepCall(args, theme2));
    return text;
  },
  renderResult(result, options, theme2, context) {
    const text = context.lastComponent ?? new Text4("", 0, 0);
    text.setText(formatGrepResult(result, options, theme2, context.showImages));
    return text;
  }
};

// pi-dist/pi-coding-agent/core/tools/grep.js
var grepSchema = Type4.Object({
  pattern: Type4.String({ description: "Search pattern (regex or literal string)" }),
  path: Type4.Optional(Type4.String({ description: "Directory or file to search (default: current directory)" })),
  glob: Type4.Optional(Type4.String({ description: "Filter files by glob pattern, e.g. '*.ts' or '**/*.spec.ts'" })),
  ignoreCase: Type4.Optional(Type4.Boolean({ description: "Case-insensitive search (default: false)" })),
  literal: Type4.Optional(Type4.Boolean({ description: "Treat pattern as literal string instead of regex (default: false)" })),
  context: Type4.Optional(Type4.Number({ description: "Number of lines to show before and after each match (default: 0)" })),
  limit: Type4.Optional(Type4.Number({ description: "Maximum number of matches to return (default: 100)" }))
});
var grepToolSystemPromptContribution = {
  snippet: "Search file contents for patterns (respects .gitignore)",
  guidelines: []
};
var DEFAULT_LIMIT2 = 100;
var defaultGrepOperations = {
  isDirectory: /* @__PURE__ */ __name(async (p) => (await fsStat(p)).isDirectory(), "isDirectory"),
  readFile: /* @__PURE__ */ __name((p) => fsReadFile2(p, "utf-8"), "readFile")
};
function createGrepToolDefinition(cwd, options) {
  const customOps = options?.operations;
  return {
    name: "grep",
    label: "grep",
    description: `Search file contents for a pattern. Returns matching lines with file paths and line numbers. Respects .gitignore. Output is truncated to ${DEFAULT_LIMIT2} matches or ${DEFAULT_MAX_BYTES / 1024}KB (whichever is hit first). Long lines are truncated to ${GREP_MAX_LINE_LENGTH} chars.`,
    promptSnippet: grepToolSystemPromptContribution.snippet,
    parameters: grepSchema,
    async execute(_toolCallId, { pattern, path: searchDir, glob, ignoreCase, literal, context, limit }, signal, _onUpdate, ctx) {
      return new Promise((resolve2, reject) => {
        if (signal?.aborted) {
          reject(new Error("Operation aborted"));
          return;
        }
        let settled = false;
        const settle = /* @__PURE__ */ __name((fn) => {
          if (!settled) {
            settled = true;
            fn();
          }
        }, "settle");
        (async () => {
          try {
            const rgPath = await ensureTool("rg");
            if (!rgPath) {
              settle(() => reject(new Error("ripgrep (rg) is not available and could not be downloaded")));
              return;
            }
            const searchPath = resolveToCwd(searchDir || ".", ctx?.cwd || cwd);
            const ops = customOps ?? defaultGrepOperations;
            let isDirectory;
            try {
              isDirectory = await ops.isDirectory(searchPath);
            } catch {
              settle(() => reject(new Error(`Path not found: ${searchPath}`)));
              return;
            }
            const contextValue = context && context > 0 ? context : 0;
            const effectiveLimit = Math.max(1, limit ?? DEFAULT_LIMIT2);
            const formatPath = /* @__PURE__ */ __name((filePath) => {
              if (isDirectory) {
                const relative2 = path2.relative(searchPath, filePath);
                if (relative2 && !relative2.startsWith("..")) {
                  return relative2.replace(/\\/g, "/");
                }
              }
              return path2.basename(filePath);
            }, "formatPath");
            const fileCache = /* @__PURE__ */ new Map();
            const getFileLines = /* @__PURE__ */ __name(async (filePath) => {
              let lines = fileCache.get(filePath);
              if (!lines) {
                try {
                  const content = await ops.readFile(filePath);
                  lines = content.replace(/\r\n/g, "\n").replace(/\r/g, "\n").split("\n");
                } catch {
                  lines = [];
                }
                fileCache.set(filePath, lines);
              }
              return lines;
            }, "getFileLines");
            const args = ["--json", "--line-number", "--color=never", "--hidden"];
            if (ignoreCase)
              args.push("--ignore-case");
            if (literal)
              args.push("--fixed-strings");
            if (glob)
              args.push("--glob", glob);
            args.push("--", pattern, searchPath);
            const child = spawn3(rgPath, args, { stdio: ["ignore", "pipe", "pipe"] });
            const rl = createInterface2({ input: child.stdout });
            let stderr = "";
            let matchCount = 0;
            let matchLimitReached = false;
            let linesTruncated = false;
            let aborted = false;
            let killedDueToLimit = false;
            const outputLines = [];
            const cleanup = /* @__PURE__ */ __name(() => {
              rl.close();
              signal?.removeEventListener("abort", onAbort);
            }, "cleanup");
            const stopChild = /* @__PURE__ */ __name((dueToLimit = false) => {
              if (!child.killed) {
                killedDueToLimit = dueToLimit;
                child.kill();
              }
            }, "stopChild");
            const onAbort = /* @__PURE__ */ __name(() => {
              aborted = true;
              stopChild();
            }, "onAbort");
            signal?.addEventListener("abort", onAbort, { once: true });
            child.stderr?.on("data", (chunk) => {
              stderr += chunk.toString();
            });
            const formatBlock = /* @__PURE__ */ __name(async (filePath, lineNumber) => {
              const relativePath = formatPath(filePath);
              const lines = await getFileLines(filePath);
              if (!lines.length)
                return [`${relativePath}:${lineNumber}: (unable to read file)`];
              const block = [];
              const start = contextValue > 0 ? Math.max(1, lineNumber - contextValue) : lineNumber;
              const end = contextValue > 0 ? Math.min(lines.length, lineNumber + contextValue) : lineNumber;
              for (let current = start; current <= end; current++) {
                const lineText = lines[current - 1] ?? "";
                const sanitized = lineText.replace(/\r/g, "");
                const isMatchLine = current === lineNumber;
                const { text: truncatedText, wasTruncated } = truncateLine(sanitized);
                if (wasTruncated)
                  linesTruncated = true;
                if (isMatchLine)
                  block.push(`${relativePath}:${current}: ${truncatedText}`);
                else
                  block.push(`${relativePath}-${current}- ${truncatedText}`);
              }
              return block;
            }, "formatBlock");
            const matches = [];
            rl.on("line", (line) => {
              if (!line.trim() || matchCount >= effectiveLimit)
                return;
              let event;
              try {
                event = JSON.parse(line);
              } catch {
                return;
              }
              if (event.type === "match") {
                matchCount++;
                const filePath = event.data?.path?.text;
                const lineNumber = event.data?.line_number;
                const lineText = event.data?.lines?.text;
                if (filePath && typeof lineNumber === "number")
                  matches.push({ filePath, lineNumber, lineText });
                if (matchCount >= effectiveLimit) {
                  matchLimitReached = true;
                  stopChild(true);
                }
              }
            });
            child.on("error", (error) => {
              cleanup();
              settle(() => reject(new Error(`Failed to run ripgrep: ${error.message}`)));
            });
            child.on("close", async (code) => {
              cleanup();
              if (aborted) {
                settle(() => reject(new Error("Operation aborted")));
                return;
              }
              if (!killedDueToLimit && code !== 0 && code !== 1) {
                const errorMsg = stderr.trim() || `ripgrep exited with code ${code}`;
                settle(() => reject(new Error(errorMsg)));
                return;
              }
              if (matchCount === 0) {
                settle(() => resolve2({ content: [{ type: "text", text: "No matches found" }], details: void 0 }));
                return;
              }
              for (const match of matches) {
                if (contextValue === 0 && match.lineText !== void 0) {
                  const relativePath = formatPath(match.filePath);
                  const sanitized = match.lineText.replace(/\r\n/g, "\n").replace(/\r/g, "").replace(/\n$/, "");
                  const { text: truncatedText, wasTruncated } = truncateLine(sanitized);
                  if (wasTruncated)
                    linesTruncated = true;
                  outputLines.push(`${relativePath}:${match.lineNumber}: ${truncatedText}`);
                } else {
                  const block = await formatBlock(match.filePath, match.lineNumber);
                  outputLines.push(...block);
                }
              }
              const rawOutput = outputLines.join("\n");
              const truncation = truncateHead(rawOutput, { maxLines: Number.MAX_SAFE_INTEGER });
              let output = truncation.content;
              const details = {};
              const notices = [];
              if (matchLimitReached) {
                notices.push(`${effectiveLimit} matches limit reached. Use limit=${effectiveLimit * 2} for more, or refine pattern`);
                details.matchLimitReached = effectiveLimit;
              }
              if (truncation.truncated) {
                notices.push(`${formatSize(DEFAULT_MAX_BYTES)} limit reached`);
                details.truncation = truncation;
              }
              if (linesTruncated) {
                notices.push(`Some lines truncated to ${GREP_MAX_LINE_LENGTH} chars. Use read tool to see full lines`);
                details.linesTruncated = true;
              }
              if (notices.length > 0)
                output += `

[${notices.join(". ")}]`;
              settle(() => resolve2({
                content: [{ type: "text", text: output }],
                details: Object.keys(details).length > 0 ? details : void 0
              }));
            });
          } catch (err) {
            settle(() => reject(err));
          }
        })();
      });
    },
    ...grepRenderers
  };
}
__name(createGrepToolDefinition, "createGrepToolDefinition");
function createGrepTool(cwd, options) {
  return wrapToolDefinition(createGrepToolDefinition(cwd, options));
}
__name(createGrepTool, "createGrepTool");

// pi-dist/pi-coding-agent/core/tools/ls.js
import { readdir as fsReaddir, stat as fsStat2 } from "node:fs/promises";
import nodePath from "path";
import { Type as Type5 } from "../../../typebox.mjs";

// pi-dist/pi-coding-agent/core/tools/renderers/ls.js
import { Text as Text5 } from "../../../pi-tui.mjs";
function formatLsCall(args, theme2, cwd) {
  const limit = args?.limit;
  const pathDisplay = renderToolPath(str(args?.path), theme2, cwd, { emptyFallback: "." });
  let text = `${theme2.fg("toolTitle", theme2.bold("ls"))} ${pathDisplay}`;
  if (limit !== void 0) {
    text += theme2.fg("toolOutput", ` (limit ${limit})`);
  }
  return text;
}
__name(formatLsCall, "formatLsCall");
function formatLsResult(result, options, theme2, showImages) {
  const output = getTextOutput(result, showImages).trim();
  let text = "";
  if (output) {
    const lines = output.split("\n");
    const maxLines = options.expanded ? lines.length : 20;
    const displayLines = lines.slice(0, maxLines);
    const remaining = lines.length - maxLines;
    text += `
${displayLines.map((line) => theme2.fg("toolOutput", line)).join("\n")}`;
    if (remaining > 0) {
      text += `${theme2.fg("muted", `
... (${remaining} more lines,`)} ${keyHint("app.tools.expand", "to expand")}${theme2.fg("muted", ")")}`;
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
    text += `
${theme2.fg("warning", `[Truncated: ${warnings.join(", ")}]`)}`;
  }
  return text;
}
__name(formatLsResult, "formatLsResult");
var lsRenderers = {
  renderCall(args, theme2, context) {
    const text = context.lastComponent ?? new Text5("", 0, 0);
    text.setText(formatLsCall(args, theme2, context.cwd));
    return text;
  },
  renderResult(result, options, theme2, context) {
    const text = context.lastComponent ?? new Text5("", 0, 0);
    text.setText(formatLsResult(result, options, theme2, context.showImages));
    return text;
  }
};

// pi-dist/pi-coding-agent/core/tools/ls.js
var lsSchema = Type5.Object({
  path: Type5.Optional(Type5.String({ description: "Directory to list (default: current directory)" })),
  limit: Type5.Optional(Type5.Number({ description: "Maximum number of entries to return (default: 500)" }))
});
var lsToolSystemPromptContribution = {
  snippet: "List directory contents",
  guidelines: []
};
var DEFAULT_LIMIT3 = 500;
var defaultLsOperations = {
  exists: pathExists,
  stat: fsStat2,
  readdir: fsReaddir
};
function createLsToolDefinition(cwd, options) {
  const ops = options?.operations ?? defaultLsOperations;
  return {
    name: "ls",
    label: "ls",
    description: `List directory contents. Returns entries sorted alphabetically, with '/' suffix for directories. Includes dotfiles. Output is truncated to ${DEFAULT_LIMIT3} entries or ${DEFAULT_MAX_BYTES / 1024}KB (whichever is hit first).`,
    promptSnippet: lsToolSystemPromptContribution.snippet,
    parameters: lsSchema,
    async execute(_toolCallId, { path: path4, limit }, signal, _onUpdate, ctx) {
      return new Promise((resolve2, reject) => {
        if (signal?.aborted) {
          reject(new Error("Operation aborted"));
          return;
        }
        const onAbort = /* @__PURE__ */ __name(() => reject(new Error("Operation aborted")), "onAbort");
        signal?.addEventListener("abort", onAbort, { once: true });
        (async () => {
          try {
            const dirPath = resolveToCwd(path4 || ".", ctx?.cwd || cwd);
            const effectiveLimit = limit ?? DEFAULT_LIMIT3;
            if (!await ops.exists(dirPath)) {
              reject(new Error(`Path not found: ${dirPath}`));
              return;
            }
            const stat = await ops.stat(dirPath);
            if (!stat.isDirectory()) {
              reject(new Error(`Not a directory: ${dirPath}`));
              return;
            }
            let entries;
            try {
              entries = await ops.readdir(dirPath);
            } catch (e) {
              reject(new Error(`Cannot read directory: ${e.message}`));
              return;
            }
            entries.sort((a, b) => a.toLowerCase().localeCompare(b.toLowerCase()));
            const results = [];
            let entryLimitReached = false;
            for (const entry of entries) {
              if (results.length >= effectiveLimit) {
                entryLimitReached = true;
                break;
              }
              const fullPath = nodePath.join(dirPath, entry);
              let suffix = "";
              try {
                const entryStat = await ops.stat(fullPath);
                if (entryStat.isDirectory())
                  suffix = "/";
              } catch {
                continue;
              }
              results.push(entry + suffix);
            }
            signal?.removeEventListener("abort", onAbort);
            if (results.length === 0) {
              resolve2({ content: [{ type: "text", text: "(empty directory)" }], details: void 0 });
              return;
            }
            const rawOutput = results.join("\n");
            const truncation = truncateHead(rawOutput, { maxLines: Number.MAX_SAFE_INTEGER });
            let output = truncation.content;
            const details = {};
            const notices = [];
            if (entryLimitReached) {
              notices.push(`${effectiveLimit} entries limit reached. Use limit=${effectiveLimit * 2} for more`);
              details.entryLimitReached = effectiveLimit;
            }
            if (truncation.truncated) {
              notices.push(`${formatSize(DEFAULT_MAX_BYTES)} limit reached`);
              details.truncation = truncation;
            }
            if (notices.length > 0) {
              output += `

[${notices.join(". ")}]`;
            }
            resolve2({
              content: [{ type: "text", text: output }],
              details: Object.keys(details).length > 0 ? details : void 0
            });
          } catch (e) {
            signal?.removeEventListener("abort", onAbort);
            reject(e);
          }
        })();
      });
    },
    ...lsRenderers
  };
}
__name(createLsToolDefinition, "createLsToolDefinition");
function createLsTool(cwd, options) {
  return wrapToolDefinition(createLsToolDefinition(cwd, options));
}
__name(createLsTool, "createLsTool");

// pi-dist/pi-coding-agent/core/tools/powershell.js
var UTF8_OUTPUT_PREFIX = "try { [Console]::OutputEncoding=[System.Text.Encoding]::UTF8 } catch {}\n";
var powershellToolSystemPromptContribution = {
  snippet: "Execute PowerShell commands",
  guidelines: ["You can inspect PI_* environment variables for current model and session details."]
};
function createLocalPowerShellOperations() {
  const operations = createLocalShellOperations("PowerShell", getPowerShellConfig);
  return {
    exec: /* @__PURE__ */ __name((command, cwd, options) => operations.exec(`${UTF8_OUTPUT_PREFIX}${command}`, cwd, options), "exec")
  };
}
__name(createLocalPowerShellOperations, "createLocalPowerShellOperations");
var powershellToolConfig = {
  name: "powershell",
  label: "powershell",
  shellName: "PowerShell",
  prompt: "PS>",
  promptSnippet: powershellToolSystemPromptContribution.snippet,
  promptGuidelines: powershellToolSystemPromptContribution.guidelines,
  tempFilePrefix: "pi-powershell"
};
function createPowerShellToolDefinition(cwd, options) {
  return createShellToolDefinition(cwd, powershellToolConfig, {
    ...options,
    operations: options?.operations ?? createLocalPowerShellOperations()
  });
}
__name(createPowerShellToolDefinition, "createPowerShellToolDefinition");
function createPowerShellTool(cwd, options) {
  const definition = createPowerShellToolDefinition(cwd, options);
  const tool = wrapToolDefinition(definition);
  Object.assign(tool, {
    promptSnippet: definition.promptSnippet,
    promptGuidelines: definition.promptGuidelines
  });
  return tool;
}
__name(createPowerShellTool, "createPowerShellTool");

// pi-dist/pi-coding-agent/core/tools/read.js
import { constants as constants5 } from "fs";
import { access as fsAccess3, readFile as fsReadFile3 } from "fs/promises";
import { Type as Type6 } from "../../../typebox.mjs";

// pi-dist/pi-coding-agent/utils/image-convert.js
import { getCapabilities, setImageTranscoder } from "../../../pi-tui.mjs";

// pi-dist/pi-coding-agent/utils/exif-orientation.js
function readOrientationFromTiff(bytes, tiffStart) {
  if (tiffStart + 8 > bytes.length)
    return 1;
  const byteOrder = bytes[tiffStart] << 8 | bytes[tiffStart + 1];
  const le = byteOrder === 18761;
  const read16 = /* @__PURE__ */ __name((pos) => {
    if (le)
      return bytes[pos] | bytes[pos + 1] << 8;
    return bytes[pos] << 8 | bytes[pos + 1];
  }, "read16");
  const read32 = /* @__PURE__ */ __name((pos) => {
    if (le)
      return bytes[pos] | bytes[pos + 1] << 8 | bytes[pos + 2] << 16 | bytes[pos + 3] << 24;
    return (bytes[pos] << 24 | bytes[pos + 1] << 16 | bytes[pos + 2] << 8 | bytes[pos + 3]) >>> 0;
  }, "read32");
  const ifdOffset = read32(tiffStart + 4);
  const ifdStart = tiffStart + ifdOffset;
  if (ifdStart + 2 > bytes.length)
    return 1;
  const entryCount = read16(ifdStart);
  for (let i = 0; i < entryCount; i++) {
    const entryPos = ifdStart + 2 + i * 12;
    if (entryPos + 12 > bytes.length)
      return 1;
    if (read16(entryPos) === 274) {
      const value = read16(entryPos + 8);
      return value >= 1 && value <= 8 ? value : 1;
    }
  }
  return 1;
}
__name(readOrientationFromTiff, "readOrientationFromTiff");
function findJpegTiffOffset(bytes) {
  let offset = 2;
  while (offset < bytes.length - 1) {
    if (bytes[offset] !== 255)
      return -1;
    const marker = bytes[offset + 1];
    if (marker === 255) {
      offset++;
      continue;
    }
    if (marker === 225) {
      if (offset + 4 >= bytes.length)
        return -1;
      const segmentStart = offset + 4;
      if (segmentStart + 6 > bytes.length)
        return -1;
      if (hasExifHeader(bytes, segmentStart))
        return segmentStart + 6;
    }
    if (offset + 4 > bytes.length)
      return -1;
    const length = bytes[offset + 2] << 8 | bytes[offset + 3];
    offset += 2 + length;
  }
  return -1;
}
__name(findJpegTiffOffset, "findJpegTiffOffset");
function findWebpTiffOffset(bytes) {
  let offset = 12;
  while (offset + 8 <= bytes.length) {
    const chunkId = String.fromCharCode(bytes[offset], bytes[offset + 1], bytes[offset + 2], bytes[offset + 3]);
    const chunkSize = bytes[offset + 4] | bytes[offset + 5] << 8 | bytes[offset + 6] << 16 | bytes[offset + 7] << 24;
    const dataStart = offset + 8;
    if (chunkId === "EXIF") {
      if (dataStart + chunkSize > bytes.length)
        return -1;
      const tiffStart = chunkSize >= 6 && hasExifHeader(bytes, dataStart) ? dataStart + 6 : dataStart;
      return tiffStart;
    }
    offset = dataStart + chunkSize + chunkSize % 2;
  }
  return -1;
}
__name(findWebpTiffOffset, "findWebpTiffOffset");
function hasExifHeader(bytes, offset) {
  return bytes[offset] === 69 && bytes[offset + 1] === 120 && bytes[offset + 2] === 105 && bytes[offset + 3] === 102 && bytes[offset + 4] === 0 && bytes[offset + 5] === 0;
}
__name(hasExifHeader, "hasExifHeader");
function getExifOrientation(bytes) {
  let tiffOffset = -1;
  if (bytes.length >= 2 && bytes[0] === 255 && bytes[1] === 216) {
    tiffOffset = findJpegTiffOffset(bytes);
  } else if (bytes.length >= 12 && bytes[0] === 82 && bytes[1] === 73 && bytes[2] === 70 && bytes[3] === 70 && bytes[8] === 87 && bytes[9] === 69 && bytes[10] === 66 && bytes[11] === 80) {
    tiffOffset = findWebpTiffOffset(bytes);
  }
  if (tiffOffset === -1)
    return 1;
  return readOrientationFromTiff(bytes, tiffOffset);
}
__name(getExifOrientation, "getExifOrientation");
function rotate90(photon, image, dstIndex) {
  const w = image.get_width();
  const h = image.get_height();
  const src = image.get_raw_pixels();
  const dst = new Uint8Array(src.length);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const srcIdx = (y * w + x) * 4;
      const dstIdx = dstIndex(x, y, w, h) * 4;
      dst[dstIdx] = src[srcIdx];
      dst[dstIdx + 1] = src[srcIdx + 1];
      dst[dstIdx + 2] = src[srcIdx + 2];
      dst[dstIdx + 3] = src[srcIdx + 3];
    }
  }
  return new photon.PhotonImage(dst, h, w);
}
__name(rotate90, "rotate90");
function applyExifOrientation(photon, image, originalBytes) {
  const orientation = getExifOrientation(originalBytes);
  if (orientation === 1)
    return image;
  switch (orientation) {
    case 2:
      photon.fliph(image);
      return image;
    case 3:
      photon.fliph(image);
      photon.flipv(image);
      return image;
    case 4:
      photon.flipv(image);
      return image;
    case 5: {
      const rotated = rotate90(photon, image, (x, y, _w, h) => x * h + (h - 1 - y));
      photon.fliph(rotated);
      return rotated;
    }
    case 6:
      return rotate90(photon, image, (x, y, _w, h) => x * h + (h - 1 - y));
    case 7: {
      const rotated = rotate90(photon, image, (x, y, w, h) => (w - 1 - x) * h + y);
      photon.fliph(rotated);
      return rotated;
    }
    case 8:
      return rotate90(photon, image, (x, y, w, h) => (w - 1 - x) * h + y);
    default:
      return image;
  }
}
__name(applyExifOrientation, "applyExifOrientation");

// pi-dist/pi-coding-agent/utils/photon.js
import { createRequire } from "module";
import * as path3 from "path";
import { fileURLToPath } from "url";
var require2 = createRequire(new URL("../utils/photon.js", import.meta.url).href);
var fs = require2("fs");
var WASM_FILENAME = "photon_rs_bg.wasm";
var photonModule = null;
var loadPromise = null;
function pathOrNull(file) {
  if (typeof file === "string") {
    return file;
  }
  if (file instanceof URL) {
    return fileURLToPath(file);
  }
  return null;
}
__name(pathOrNull, "pathOrNull");
function getFallbackWasmPaths() {
  const execDir = path3.dirname(process.execPath);
  return [
    path3.join(execDir, WASM_FILENAME),
    path3.join(execDir, "photon", WASM_FILENAME),
    path3.join(process.cwd(), WASM_FILENAME)
  ];
}
__name(getFallbackWasmPaths, "getFallbackWasmPaths");
function patchPhotonWasmRead() {
  const originalReadFileSync = fs.readFileSync.bind(fs);
  const fallbackPaths = getFallbackWasmPaths();
  const mutableFs = fs;
  const patchedReadFileSync = /* @__PURE__ */ __name(((...args) => {
    const [file, options] = args;
    const resolvedPath = pathOrNull(file);
    if (resolvedPath?.endsWith(WASM_FILENAME)) {
      try {
        return originalReadFileSync(...args);
      } catch (error) {
        const err = error;
        if (err?.code && err.code !== "ENOENT") {
          throw error;
        }
        for (const fallbackPath of fallbackPaths) {
          if (!fs.existsSync(fallbackPath)) {
            continue;
          }
          if (options === void 0) {
            return originalReadFileSync(fallbackPath);
          }
          return originalReadFileSync(fallbackPath, options);
        }
        throw error;
      }
    }
    return originalReadFileSync(...args);
  }), "patchedReadFileSync");
  try {
    mutableFs.readFileSync = patchedReadFileSync;
  } catch {
    Object.defineProperty(fs, "readFileSync", {
      value: patchedReadFileSync,
      writable: true,
      configurable: true
    });
  }
  return () => {
    try {
      mutableFs.readFileSync = originalReadFileSync;
    } catch {
      Object.defineProperty(fs, "readFileSync", {
        value: originalReadFileSync,
        writable: true,
        configurable: true
      });
    }
  };
}
__name(patchPhotonWasmRead, "patchPhotonWasmRead");
async function loadPhoton() {
  if (photonModule) {
    return photonModule;
  }
  if (loadPromise) {
    return loadPromise;
  }
  loadPromise = (async () => {
    const restoreReadFileSync = patchPhotonWasmRead();
    try {
      photonModule = await import("../../../photon-node/photon_rs.js");
      return photonModule;
    } catch {
      photonModule = null;
      return photonModule;
    } finally {
      restoreReadFileSync();
    }
  })();
  return loadPromise;
}
__name(loadPhoton, "loadPhoton");

// pi-dist/pi-coding-agent/utils/image-convert.js
function encodePng(photon, bytes) {
  try {
    const rawImage = photon.PhotonImage.new_from_byteslice(bytes);
    const image = applyExifOrientation(photon, rawImage, bytes);
    if (image !== rawImage)
      rawImage.free();
    try {
      return new Uint8Array(image.get_bytes());
    } finally {
      image.free();
    }
  } catch {
    return null;
  }
}
__name(encodePng, "encodePng");
async function convertImageBytesToPng(bytes) {
  const photon = await loadPhoton();
  if (!photon) {
    return null;
  }
  return encodePng(photon, bytes);
}
__name(convertImageBytesToPng, "convertImageBytesToPng");
async function convertToPng(base64Data, mimeType) {
  if (mimeType === "image/png") {
    return { data: base64Data, mimeType };
  }
  const bytes = new Uint8Array(Buffer.from(base64Data, "base64"));
  const pngBytes = await convertImageBytesToPng(bytes);
  if (!pngBytes) {
    return null;
  }
  return {
    data: Buffer.from(pngBytes).toString("base64"),
    mimeType: "image/png"
  };
}
__name(convertToPng, "convertToPng");
async function loadPngTranscoder() {
  const photon = await loadPhoton();
  if (!photon)
    return void 0;
  return (base64Data) => {
    const pngBytes = encodePng(photon, new Uint8Array(Buffer.from(base64Data, "base64")));
    return pngBytes ? Buffer.from(pngBytes).toString("base64") : null;
  };
}
__name(loadPngTranscoder, "loadPngTranscoder");
var pngTranscoderLoad;
var pngTranscoderRegistered = false;
function ensurePngTranscoder(onRegistered) {
  if (pngTranscoderRegistered || getCapabilities().images !== "kitty")
    return;
  pngTranscoderLoad ??= loadPngTranscoder().then((transcoder) => {
    if (!transcoder)
      return false;
    setImageTranscoder(transcoder);
    pngTranscoderRegistered = true;
    return true;
  });
  void pngTranscoderLoad.then((registered) => {
    if (registered)
      onRegistered();
  });
}
__name(ensurePngTranscoder, "ensurePngTranscoder");

// pi-dist/pi-coding-agent/utils/image-resize.js
import { Worker } from "node:worker_threads";

// pi-dist/pi-coding-agent/utils/image-resize-core.js
var DEFAULT_MAX_BYTES2 = 4.5 * 1024 * 1024;
var DEFAULT_OPTIONS = {
  maxWidth: 2e3,
  maxHeight: 2e3,
  maxBytes: DEFAULT_MAX_BYTES2,
  jpegQuality: 80
};
function encodeCandidate(buffer, mimeType) {
  const data = Buffer.from(buffer).toString("base64");
  return {
    data,
    encodedSize: Buffer.byteLength(data, "utf-8"),
    mimeType
  };
}
__name(encodeCandidate, "encodeCandidate");
async function resizeImageInProcess(inputBytes, mimeType, options) {
  const opts = { ...DEFAULT_OPTIONS, ...options };
  const inputBase64Size = Math.ceil(inputBytes.byteLength / 3) * 4;
  const photon = await loadPhoton();
  if (!photon) {
    return null;
  }
  let image;
  try {
    let tryEncodings = function(width, height, jpegQualities) {
      const resized = photon.resize(image, width, height, photon.SamplingFilter.Lanczos3);
      try {
        const candidates = [encodeCandidate(resized.get_bytes(), "image/png")];
        for (const quality of jpegQualities) {
          candidates.push(encodeCandidate(resized.get_bytes_jpeg(quality), "image/jpeg"));
        }
        return candidates;
      } finally {
        resized.free();
      }
    };
    __name(tryEncodings, "tryEncodings");
    const rawImage = photon.PhotonImage.new_from_byteslice(inputBytes);
    image = applyExifOrientation(photon, rawImage, inputBytes);
    if (image !== rawImage)
      rawImage.free();
    const originalWidth = image.get_width();
    const originalHeight = image.get_height();
    const format = mimeType.split("/")[1] ?? "png";
    if (originalWidth <= opts.maxWidth && originalHeight <= opts.maxHeight && inputBase64Size < opts.maxBytes) {
      return {
        data: Buffer.from(inputBytes).toString("base64"),
        mimeType: mimeType || `image/${format}`,
        originalWidth,
        originalHeight,
        width: originalWidth,
        height: originalHeight,
        wasResized: false
      };
    }
    let targetWidth = originalWidth;
    let targetHeight = originalHeight;
    if (targetWidth > opts.maxWidth) {
      targetHeight = Math.round(targetHeight * opts.maxWidth / targetWidth);
      targetWidth = opts.maxWidth;
    }
    if (targetHeight > opts.maxHeight) {
      targetWidth = Math.round(targetWidth * opts.maxHeight / targetHeight);
      targetHeight = opts.maxHeight;
    }
    const qualitySteps = Array.from(/* @__PURE__ */ new Set([opts.jpegQuality, 85, 70, 55, 40]));
    let currentWidth = targetWidth;
    let currentHeight = targetHeight;
    while (true) {
      const candidates = tryEncodings(currentWidth, currentHeight, qualitySteps);
      for (const candidate of candidates) {
        if (candidate.encodedSize < opts.maxBytes) {
          return {
            data: candidate.data,
            mimeType: candidate.mimeType,
            originalWidth,
            originalHeight,
            width: currentWidth,
            height: currentHeight,
            wasResized: true
          };
        }
      }
      if (currentWidth === 1 && currentHeight === 1) {
        break;
      }
      const nextWidth = currentWidth === 1 ? 1 : Math.max(1, Math.floor(currentWidth * 0.75));
      const nextHeight = currentHeight === 1 ? 1 : Math.max(1, Math.floor(currentHeight * 0.75));
      if (nextWidth === currentWidth && nextHeight === currentHeight) {
        break;
      }
      currentWidth = nextWidth;
      currentHeight = nextHeight;
    }
    return null;
  } catch {
    return null;
  } finally {
    if (image) {
      image.free();
    }
  }
}
__name(resizeImageInProcess, "resizeImageInProcess");

// pi-dist/pi-coding-agent/utils/image-resize.js
function toTransferableBytes(input) {
  return new Uint8Array(input);
}
__name(toTransferableBytes, "toTransferableBytes");
function isResizeImageWorkerResponse(value) {
  return value !== null && typeof value === "object";
}
__name(isResizeImageWorkerResponse, "isResizeImageWorkerResponse");
function createResizeWorker(workerSpecifier) {
  return new Worker(workerSpecifier);
}
__name(createResizeWorker, "createResizeWorker");
async function resizeImageInWorker(workerSpecifier, inputBytes, mimeType, options) {
  const worker = createResizeWorker(workerSpecifier);
  try {
    const inputBytesForWorker = toTransferableBytes(inputBytes);
    return await new Promise((resolve2, reject) => {
      let settled = false;
      const settle = /* @__PURE__ */ __name((result) => {
        if (settled)
          return;
        settled = true;
        resolve2(result);
      }, "settle");
      const fail = /* @__PURE__ */ __name((error) => {
        if (settled)
          return;
        settled = true;
        reject(error);
      }, "fail");
      worker.once("message", (message) => {
        if (!isResizeImageWorkerResponse(message)) {
          fail(new Error("Invalid image resize worker response"));
          return;
        }
        if (message.error) {
          fail(new Error(message.error));
          return;
        }
        settle(message.result ?? null);
      });
      worker.once("error", fail);
      worker.once("exit", (code) => {
        if (!settled) {
          fail(new Error(`Image resize worker exited with code ${code}`));
        }
      });
      worker.postMessage({
        inputBytes: inputBytesForWorker,
        mimeType,
        options
      }, [inputBytesForWorker.buffer]);
    });
  } finally {
    void worker.terminate().catch(() => void 0);
  }
}
__name(resizeImageInWorker, "resizeImageInWorker");
async function resizeImage(inputBytes, mimeType, options) {
  const isTypeScriptRuntime = new URL("../utils/image-resize.js", import.meta.url).href.endsWith(".ts");
  const workerUrl = new URL(isTypeScriptRuntime ? "./image-resize-worker.ts" : "./image-resize-worker.js", new URL("../utils/image-resize.js", import.meta.url).href);
  if (typeof process.versions.bun === "string") {
    try {
      return await resizeImageInWorker("./src/utils/image-resize-worker.ts", inputBytes, mimeType, options);
    } catch {
    }
  }
  try {
    return await resizeImageInWorker(workerUrl, inputBytes, mimeType, options);
  } catch {
    return resizeImageInProcess(inputBytes, mimeType, options);
  }
}
__name(resizeImage, "resizeImage");
function formatDimensionNote(result) {
  if (!result.wasResized) {
    return void 0;
  }
  const scale = result.originalWidth / result.width;
  return `[Image: original ${result.originalWidth}x${result.originalHeight}, displayed at ${result.width}x${result.height}. Multiply coordinates by ${scale.toFixed(2)} to map to original image.]`;
}
__name(formatDimensionNote, "formatDimensionNote");

// pi-dist/pi-coding-agent/utils/image-process.js
function baseMimeType(mimeType) {
  return mimeType.split(";")[0]?.trim().toLowerCase() ?? mimeType.toLowerCase();
}
__name(baseMimeType, "baseMimeType");
function normalizeSupportedImageMimeType(mimeType) {
  switch (baseMimeType(mimeType)) {
    case "image/png":
      return "image/png";
    case "image/jpeg":
    case "image/jpg":
      return "image/jpeg";
    case "image/gif":
      return "image/gif";
    case "image/webp":
      return "image/webp";
    default:
      return null;
  }
}
__name(normalizeSupportedImageMimeType, "normalizeSupportedImageMimeType");
async function normalizeImage(bytes, mimeType) {
  const normalizedMimeType = normalizeSupportedImageMimeType(mimeType);
  if (normalizedMimeType) {
    return { bytes, mimeType: normalizedMimeType };
  }
  const pngBytes = await convertImageBytesToPng(bytes);
  if (!pngBytes) {
    return null;
  }
  return {
    bytes: pngBytes,
    mimeType: "image/png",
    convertedFrom: baseMimeType(mimeType)
  };
}
__name(normalizeImage, "normalizeImage");
function conversionHint(from, to) {
  if (!from || from === to)
    return void 0;
  return `[Image converted from ${from} to ${to}.]`;
}
__name(conversionHint, "conversionHint");
async function processImage(bytes, mimeType, options) {
  const autoResizeImages = options?.autoResizeImages ?? true;
  const normalized = await normalizeImage(bytes, mimeType);
  if (!normalized) {
    return {
      ok: false,
      message: "[Image omitted: could not be converted to a supported inline image format.]"
    };
  }
  if (autoResizeImages) {
    const resized = await resizeImage(normalized.bytes, normalized.mimeType, options?.resizeOptions);
    if (!resized) {
      return {
        ok: false,
        message: "[Image omitted: could not be resized below the inline image size limit.]"
      };
    }
    const hints2 = [];
    const convertedHint2 = conversionHint(normalized.convertedFrom, resized.mimeType);
    if (convertedHint2)
      hints2.push(convertedHint2);
    const dimensionNote = formatDimensionNote(resized);
    if (dimensionNote)
      hints2.push(dimensionNote);
    return {
      ok: true,
      data: resized.data,
      mimeType: resized.mimeType,
      hints: hints2
    };
  }
  const hints = [];
  const convertedHint = conversionHint(normalized.convertedFrom, normalized.mimeType);
  if (convertedHint)
    hints.push(convertedHint);
  return {
    ok: true,
    data: Buffer.from(normalized.bytes).toString("base64"),
    mimeType: normalized.mimeType,
    hints
  };
}
__name(processImage, "processImage");

// pi-dist/pi-coding-agent/utils/mime.js
import { open as open2 } from "node:fs/promises";
var IMAGE_TYPE_SNIFF_BYTES = 4100;
var PNG_SIGNATURE = [137, 80, 78, 71, 13, 10, 26, 10];
function detectSupportedImageMimeType(buffer) {
  if (startsWith(buffer, [255, 216, 255])) {
    return buffer[3] === 247 ? null : "image/jpeg";
  }
  if (startsWith(buffer, PNG_SIGNATURE)) {
    return isPng(buffer) && !isAnimatedPng(buffer) ? "image/png" : null;
  }
  if (startsWithAscii(buffer, 0, "GIF87a") || startsWithAscii(buffer, 0, "GIF89a")) {
    return "image/gif";
  }
  if (startsWithAscii(buffer, 0, "RIFF") && startsWithAscii(buffer, 8, "WEBP")) {
    return "image/webp";
  }
  if (startsWithAscii(buffer, 0, "BM") && isBmp(buffer)) {
    return "image/bmp";
  }
  return null;
}
__name(detectSupportedImageMimeType, "detectSupportedImageMimeType");
async function detectSupportedImageMimeTypeFromFile(filePath) {
  const fileHandle = await open2(filePath, "r");
  try {
    const buffer = Buffer.alloc(IMAGE_TYPE_SNIFF_BYTES);
    const { bytesRead } = await fileHandle.read(buffer, 0, IMAGE_TYPE_SNIFF_BYTES, 0);
    return detectSupportedImageMimeType(buffer.subarray(0, bytesRead));
  } finally {
    await fileHandle.close();
  }
}
__name(detectSupportedImageMimeTypeFromFile, "detectSupportedImageMimeTypeFromFile");
function isPng(buffer) {
  return buffer.length >= 16 && readUint32BE(buffer, PNG_SIGNATURE.length) === 13 && startsWithAscii(buffer, 12, "IHDR");
}
__name(isPng, "isPng");
function isAnimatedPng(buffer) {
  let offset = PNG_SIGNATURE.length;
  while (offset + 8 <= buffer.length) {
    const chunkLength = readUint32BE(buffer, offset);
    const chunkTypeOffset = offset + 4;
    if (startsWithAscii(buffer, chunkTypeOffset, "acTL"))
      return true;
    if (startsWithAscii(buffer, chunkTypeOffset, "IDAT"))
      return false;
    const nextOffset = offset + 8 + chunkLength + 4;
    if (nextOffset <= offset || nextOffset > buffer.length)
      return false;
    offset = nextOffset;
  }
  return false;
}
__name(isAnimatedPng, "isAnimatedPng");
function isBmp(buffer) {
  if (buffer.length < 26)
    return false;
  const declaredFileSize = readUint32LE(buffer, 2);
  const pixelDataOffset = readUint32LE(buffer, 10);
  const dibHeaderSize = readUint32LE(buffer, 14);
  if (declaredFileSize !== 0 && declaredFileSize < 26)
    return false;
  if (pixelDataOffset < 14 + dibHeaderSize)
    return false;
  if (declaredFileSize !== 0 && pixelDataOffset >= declaredFileSize)
    return false;
  let colorPlanes;
  let bitsPerPixel;
  if (dibHeaderSize === 12) {
    colorPlanes = readUint16LE(buffer, 22);
    bitsPerPixel = readUint16LE(buffer, 24);
  } else if (dibHeaderSize >= 40 && dibHeaderSize <= 124) {
    if (buffer.length < 30)
      return false;
    colorPlanes = readUint16LE(buffer, 26);
    bitsPerPixel = readUint16LE(buffer, 28);
  } else {
    return false;
  }
  return colorPlanes === 1 && [1, 4, 8, 16, 24, 32].includes(bitsPerPixel);
}
__name(isBmp, "isBmp");
function readUint16LE(buffer, offset) {
  return (buffer[offset] ?? 0) + ((buffer[offset + 1] ?? 0) << 8);
}
__name(readUint16LE, "readUint16LE");
function readUint32BE(buffer, offset) {
  return (buffer[offset] ?? 0) * 16777216 + ((buffer[offset + 1] ?? 0) << 16) + ((buffer[offset + 2] ?? 0) << 8) + (buffer[offset + 3] ?? 0);
}
__name(readUint32BE, "readUint32BE");
function readUint32LE(buffer, offset) {
  return (buffer[offset] ?? 0) + ((buffer[offset + 1] ?? 0) << 8) + ((buffer[offset + 2] ?? 0) << 16) + (buffer[offset + 3] ?? 0) * 16777216;
}
__name(readUint32LE, "readUint32LE");
function startsWith(buffer, bytes) {
  if (buffer.length < bytes.length)
    return false;
  return bytes.every((byte, index) => buffer[index] === byte);
}
__name(startsWith, "startsWith");
function startsWithAscii(buffer, offset, text) {
  if (buffer.length < offset + text.length)
    return false;
  for (let index = 0; index < text.length; index++) {
    if (buffer[offset + index] !== text.charCodeAt(index))
      return false;
  }
  return true;
}
__name(startsWithAscii, "startsWithAscii");

// pi-dist/pi-coding-agent/core/tools/renderers/read.js
import { basename, dirname as dirname2, isAbsolute, relative, resolve as resolvePath2, sep } from "node:path";
import { Text as Text6 } from "../../../pi-tui.mjs";
var COMPACT_RESOURCE_FILE_NAMES = /* @__PURE__ */ new Set(["AGENTS.override.md", "AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"]);
function formatReadLineRange(args, theme2) {
  if (args?.offset == null && args?.limit == null)
    return "";
  const startLine = args.offset ?? 1;
  const endLine = args.limit != null ? startLine + args.limit - 1 : "";
  return theme2.fg("warning", `:${startLine}${endLine ? `-${endLine}` : ""}`);
}
__name(formatReadLineRange, "formatReadLineRange");
function formatReadCall(args, theme2, cwd) {
  const pathDisplay = renderToolPath(str(args?.file_path ?? args?.path), theme2, cwd);
  return `${theme2.fg("toolTitle", theme2.bold("read"))} ${pathDisplay}${formatReadLineRange(args, theme2)}`;
}
__name(formatReadCall, "formatReadCall");
function trimTrailingEmptyLines(lines) {
  let end = lines.length;
  while (end > 0 && lines[end - 1] === "") {
    end--;
  }
  return lines.slice(0, end);
}
__name(trimTrailingEmptyLines, "trimTrailingEmptyLines");
function toPosixPath(filePath) {
  return filePath.split(sep).join("/");
}
__name(toPosixPath, "toPosixPath");
function getPiDocsClassification(absolutePath) {
  const packageRoot = dirname2(getReadmePath());
  const relativePath = relative(resolvePath2(packageRoot), resolvePath2(absolutePath));
  if (relativePath === "" || relativePath === ".." || relativePath.startsWith(`..${sep}`) || isAbsolute(relativePath)) {
    return void 0;
  }
  const label = toPosixPath(relativePath);
  if (label === "README.md" || label.startsWith("docs/") || label.startsWith("examples/")) {
    return { kind: "docs", label };
  }
  return void 0;
}
__name(getPiDocsClassification, "getPiDocsClassification");
function getCompactReadClassification(args, cwd) {
  const rawPath = str(args?.file_path ?? args?.path);
  if (!rawPath)
    return void 0;
  const absolutePath = resolveToCwd(rawPath, cwd);
  const fileName = basename(absolutePath);
  if (fileName === "SKILL.md") {
    return { kind: "skill", label: basename(dirname2(absolutePath)) || fileName };
  }
  const docsClassification = getPiDocsClassification(absolutePath);
  if (docsClassification)
    return docsClassification;
  if (COMPACT_RESOURCE_FILE_NAMES.has(fileName)) {
    return { kind: "resource", label: formatPathRelativeToCwdOrAbsolute(absolutePath, cwd) };
  }
  return void 0;
}
__name(getCompactReadClassification, "getCompactReadClassification");
function formatCompactReadCall(classification, args, theme2) {
  const expandHint = theme2.fg("dim", ` (${keyText("app.tools.expand")} to expand)`);
  if (classification.kind === "skill") {
    return theme2.fg("customMessageLabel", `\x1B[1m[skill]\x1B[22m `) + theme2.fg("customMessageText", classification.label) + formatReadLineRange(args, theme2) + expandHint;
  }
  return theme2.fg("toolTitle", theme2.bold(`read ${classification.kind}`)) + " " + theme2.fg("accent", classification.label) + formatReadLineRange(args, theme2) + expandHint;
}
__name(formatCompactReadCall, "formatCompactReadCall");
function formatReadResult(args, result, options, theme2, showImages, _cwd, isError) {
  if (!options.expanded && !isError) {
    return "";
  }
  const rawPath = str(args?.file_path ?? args?.path);
  const output = getTextOutput(result, showImages);
  const lang = !isError && rawPath ? getLanguageFromPath(rawPath) : void 0;
  const renderedLines = lang ? highlightCode(replaceTabs(output), lang) : output.split("\n");
  const lines = trimTrailingEmptyLines(renderedLines);
  const maxLines = options.expanded ? lines.length : 10;
  const displayLines = lines.slice(0, maxLines);
  const remaining = lines.length - maxLines;
  let text = `
${displayLines.map((line) => lang ? replaceTabs(line) : theme2.fg("toolOutput", replaceTabs(line))).join("\n")}`;
  if (remaining > 0) {
    text += `${theme2.fg("muted", `
... (${remaining} more lines,`)} ${keyHint("app.tools.expand", "to expand")}${theme2.fg("muted", ")")}`;
  }
  const truncation = result.details?.truncation;
  if (truncation?.truncated) {
    if (truncation.firstLineExceedsLimit) {
      text += `
${theme2.fg("warning", `[First line exceeds ${formatSize(truncation.maxBytes ?? DEFAULT_MAX_BYTES)} limit]`)}`;
    } else if (truncation.truncatedBy === "lines") {
      text += `
${theme2.fg("warning", `[Truncated: showing ${truncation.outputLines} of ${truncation.totalLines} lines (${truncation.maxLines ?? DEFAULT_MAX_LINES} line limit)]`)}`;
    } else {
      text += `
${theme2.fg("warning", `[Truncated: ${truncation.outputLines} lines shown (${formatSize(truncation.maxBytes ?? DEFAULT_MAX_BYTES)} limit)]`)}`;
    }
  }
  return text;
}
__name(formatReadResult, "formatReadResult");
var readRenderers = {
  renderCall(rawArgs, theme2, context) {
    const args = rawArgs;
    const text = context.lastComponent ?? new Text6("", 0, 0);
    const classification = !context.expanded ? getCompactReadClassification(args, context.cwd) : void 0;
    text.setText(classification ? formatCompactReadCall(classification, args, theme2) : formatReadCall(args, theme2, context.cwd));
    return text;
  },
  renderResult(result, options, theme2, context) {
    const text = context.lastComponent ?? new Text6("", 0, 0);
    text.setText(formatReadResult(context.args, result, options, theme2, context.showImages, context.cwd, context.isError));
    return text;
  }
};

// pi-dist/pi-coding-agent/core/tools/read.js
var readSchema = Type6.Object({
  path: Type6.String({ description: "Path to the file to read (relative or absolute)" }),
  offset: Type6.Optional(Type6.Number({ description: "Line number to start reading from (1-indexed)" })),
  limit: Type6.Optional(Type6.Number({ description: "Maximum number of lines to read" }))
});
var readToolSystemPromptContribution = {
  snippet: "Read file contents",
  guidelines: ["Use read to examine files instead of cat or sed."]
};
var defaultReadOperations = {
  readFile: /* @__PURE__ */ __name((path4) => fsReadFile3(path4), "readFile"),
  access: /* @__PURE__ */ __name((path4) => fsAccess3(path4, constants5.R_OK), "access"),
  detectImageMimeType: detectSupportedImageMimeTypeFromFile
};
function getNonVisionImageNote(model) {
  if (!model || model.input.includes("image")) {
    return void 0;
  }
  return "[Current model does not support images. The image will be omitted from this request.]";
}
__name(getNonVisionImageNote, "getNonVisionImageNote");
function createReadToolDefinition(cwd, options) {
  const autoResizeImages = options?.autoResizeImages ?? true;
  const fallbackResizeOptions = options?.resizeOptions;
  const ops = options?.operations ?? defaultReadOperations;
  return {
    name: "read",
    label: "read",
    description: `Read the contents of a file. Supports text files and images (jpg, png, gif, webp, bmp). Images are sent as attachments. For text files, output is truncated to ${DEFAULT_MAX_LINES} lines or ${DEFAULT_MAX_BYTES / 1024}KB (whichever is hit first). Use offset/limit for large files. When you need the full file, continue with offset until complete.`,
    promptSnippet: readToolSystemPromptContribution.snippet,
    promptGuidelines: [...readToolSystemPromptContribution.guidelines],
    parameters: readSchema,
    constrainedSampling: { type: "json_schema", strict: "prefer" },
    async execute(_toolCallId, { path: path4, offset, limit }, signal, _onUpdate, ctx) {
      return new Promise((resolve2, reject) => {
        if (signal?.aborted) {
          reject(new Error("Operation aborted"));
          return;
        }
        let aborted = false;
        const onAbort = /* @__PURE__ */ __name(() => {
          aborted = true;
          reject(new Error("Operation aborted"));
        }, "onAbort");
        signal?.addEventListener("abort", onAbort, { once: true });
        (async () => {
          try {
            const absolutePath = await resolveReadPathAsync(path4, ctx?.cwd || cwd);
            if (aborted)
              return;
            await ops.access(absolutePath);
            if (aborted)
              return;
            const mimeType = ops.detectImageMimeType ? await ops.detectImageMimeType(absolutePath) : void 0;
            let content;
            let details;
            const nonVisionImageNote = getNonVisionImageNote(ctx?.model);
            if (mimeType) {
              const buffer = await ops.readFile(absolutePath);
              const processed = await processImage(buffer, mimeType, {
                autoResizeImages,
                resizeOptions: ctx?.model?.inputLimits?.images?.resize ?? fallbackResizeOptions
              });
              if (!processed.ok) {
                let textNote = `Read image file [${mimeType}]
${processed.message}`;
                if (nonVisionImageNote)
                  textNote += `
${nonVisionImageNote}`;
                content = [{ type: "text", text: textNote }];
              } else {
                let textNote = `Read image file [${processed.mimeType}]`;
                if (processed.hints.length > 0)
                  textNote += `
${processed.hints.join("\n")}`;
                if (nonVisionImageNote)
                  textNote += `
${nonVisionImageNote}`;
                content = [
                  { type: "text", text: textNote },
                  { type: "image", data: processed.data, mimeType: processed.mimeType }
                ];
              }
            } else {
              const buffer = await ops.readFile(absolutePath);
              const textContent = buffer.toString("utf-8");
              const allLines = textContent.split("\n");
              const totalFileLines = allLines.length;
              const startLine = offset ? Math.max(0, offset - 1) : 0;
              const startLineDisplay = startLine + 1;
              if (startLine >= allLines.length) {
                throw new Error(`Offset ${offset} is beyond end of file (${allLines.length} lines total)`);
              }
              let selectedContent;
              let userLimitedLines;
              if (limit !== void 0) {
                const endLine = Math.min(startLine + limit, allLines.length);
                selectedContent = allLines.slice(startLine, endLine).join("\n");
                userLimitedLines = endLine - startLine;
              } else {
                selectedContent = allLines.slice(startLine).join("\n");
              }
              const truncation = truncateHead(selectedContent);
              let outputText;
              if (truncation.firstLineExceedsLimit) {
                const firstLineSize = formatSize(Buffer.byteLength(allLines[startLine], "utf-8"));
                outputText = `[Line ${startLineDisplay} is ${firstLineSize}, exceeds ${formatSize(DEFAULT_MAX_BYTES)} limit. Use bash: sed -n '${startLineDisplay}p' ${path4} | head -c ${DEFAULT_MAX_BYTES}]`;
                details = { truncation };
              } else if (truncation.truncated) {
                const endLineDisplay = startLineDisplay + truncation.outputLines - 1;
                const nextOffset = endLineDisplay + 1;
                outputText = truncation.content;
                if (truncation.truncatedBy === "lines") {
                  outputText += `

[Showing lines ${startLineDisplay}-${endLineDisplay} of ${totalFileLines}. Use offset=${nextOffset} to continue.]`;
                } else {
                  outputText += `

[Showing lines ${startLineDisplay}-${endLineDisplay} of ${totalFileLines} (${formatSize(DEFAULT_MAX_BYTES)} limit). Use offset=${nextOffset} to continue.]`;
                }
                details = { truncation };
              } else if (userLimitedLines !== void 0 && startLine + userLimitedLines < allLines.length) {
                const remaining = allLines.length - (startLine + userLimitedLines);
                const nextOffset = startLine + userLimitedLines + 1;
                outputText = `${truncation.content}

[${remaining} more lines in file. Use offset=${nextOffset} to continue.]`;
              } else {
                outputText = truncation.content;
              }
              content = [{ type: "text", text: outputText }];
            }
            if (aborted)
              return;
            signal?.removeEventListener("abort", onAbort);
            resolve2({ content, details });
          } catch (error) {
            signal?.removeEventListener("abort", onAbort);
            if (!aborted)
              reject(error);
          }
        })();
      });
    },
    ...readRenderers
  };
}
__name(createReadToolDefinition, "createReadToolDefinition");
function createReadTool(cwd, options) {
  return wrapToolDefinition(createReadToolDefinition(cwd, options));
}
__name(createReadTool, "createReadTool");

// pi-dist/pi-coding-agent/core/tools/write.js
import { mkdir as fsMkdir, writeFile as fsWriteFile2 } from "fs/promises";
import { dirname as dirname3 } from "path";
import { Type as Type7 } from "../../../typebox.mjs";

// pi-dist/pi-coding-agent/core/tools/renderers/write.js
import { Container as Container3, Text as Text7 } from "../../../pi-tui.mjs";
var WriteCallRenderComponent = class extends Text7 {
  static {
    __name(this, "WriteCallRenderComponent");
  }
  cache;
  constructor() {
    super("", 0, 0);
  }
};
var WRITE_PARTIAL_FULL_HIGHLIGHT_LINES = 50;
function highlightSingleLine(line, lang) {
  const highlighted = highlightCode(line, lang);
  return highlighted[0] ?? "";
}
__name(highlightSingleLine, "highlightSingleLine");
function refreshWriteHighlightPrefix(cache) {
  const prefixCount = Math.min(WRITE_PARTIAL_FULL_HIGHLIGHT_LINES, cache.normalizedLines.length);
  if (prefixCount === 0)
    return;
  const prefixSource = cache.normalizedLines.slice(0, prefixCount).join("\n");
  const prefixHighlighted = highlightCode(prefixSource, cache.lang);
  for (let i = 0; i < prefixCount; i++) {
    cache.highlightedLines[i] = prefixHighlighted[i] ?? highlightSingleLine(cache.normalizedLines[i] ?? "", cache.lang);
  }
}
__name(refreshWriteHighlightPrefix, "refreshWriteHighlightPrefix");
function rebuildWriteHighlightCacheFull(rawPath, fileContent) {
  const lang = rawPath ? getLanguageFromPath(rawPath) : void 0;
  if (!lang)
    return void 0;
  const displayContent = normalizeDisplayText(fileContent);
  const normalized = replaceTabs(displayContent);
  return {
    rawPath,
    lang,
    rawContent: fileContent,
    normalizedLines: normalized.split("\n"),
    highlightedLines: highlightCode(normalized, lang)
  };
}
__name(rebuildWriteHighlightCacheFull, "rebuildWriteHighlightCacheFull");
function updateWriteHighlightCacheIncremental(cache, rawPath, fileContent) {
  const lang = rawPath ? getLanguageFromPath(rawPath) : void 0;
  if (!lang)
    return void 0;
  if (!cache)
    return rebuildWriteHighlightCacheFull(rawPath, fileContent);
  if (cache.lang !== lang || cache.rawPath !== rawPath)
    return rebuildWriteHighlightCacheFull(rawPath, fileContent);
  if (!fileContent.startsWith(cache.rawContent))
    return rebuildWriteHighlightCacheFull(rawPath, fileContent);
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
  refreshWriteHighlightPrefix(cache);
  return cache;
}
__name(updateWriteHighlightCacheIncremental, "updateWriteHighlightCacheIncremental");
function trimTrailingEmptyLines2(lines) {
  let end = lines.length;
  while (end > 0 && lines[end - 1] === "") {
    end--;
  }
  return lines.slice(0, end);
}
__name(trimTrailingEmptyLines2, "trimTrailingEmptyLines");
function formatWriteCall(args, options, theme2, cache, cwd) {
  const rawPath = str(args?.file_path ?? args?.path);
  const fileContent = str(args?.content);
  const pathDisplay = renderToolPath(rawPath, theme2, cwd);
  let text = `${theme2.fg("toolTitle", theme2.bold("write"))} ${pathDisplay}`;
  if (fileContent === null) {
    text += `

${theme2.fg("error", "[invalid content arg - expected string]")}`;
  } else if (fileContent) {
    const lang = rawPath ? getLanguageFromPath(rawPath) : void 0;
    const renderedLines = lang ? cache?.highlightedLines ?? highlightCode(replaceTabs(normalizeDisplayText(fileContent)), lang) : normalizeDisplayText(fileContent).split("\n");
    const lines = trimTrailingEmptyLines2(renderedLines);
    const totalLines = lines.length;
    const maxLines = options.expanded ? lines.length : 10;
    const displayLines = lines.slice(0, maxLines);
    const remaining = lines.length - maxLines;
    text += `

${displayLines.map((line) => lang ? line : theme2.fg("toolOutput", replaceTabs(line))).join("\n")}`;
    if (remaining > 0) {
      text += `${theme2.fg("muted", `
... (${remaining} more lines, ${totalLines} total,`)} ${keyHint("app.tools.expand", "to expand")}${theme2.fg("muted", ")")}`;
    }
  }
  return text;
}
__name(formatWriteCall, "formatWriteCall");
function formatWriteResult(result, theme2) {
  if (!result.isError) {
    return void 0;
  }
  const output = result.content.filter((c) => c.type === "text").map((c) => c.text || "").join("\n");
  if (!output) {
    return void 0;
  }
  return `
${theme2.fg("error", output)}`;
}
__name(formatWriteResult, "formatWriteResult");
var writeRenderers = {
  renderCall(args, theme2, context) {
    const renderArgs = args;
    const rawPath = str(renderArgs?.file_path ?? renderArgs?.path);
    const fileContent = str(renderArgs?.content);
    const component = context.lastComponent ?? new WriteCallRenderComponent();
    if (fileContent !== null) {
      component.cache = context.argsComplete ? rebuildWriteHighlightCacheFull(rawPath, fileContent) : updateWriteHighlightCacheIncremental(component.cache, rawPath, fileContent);
    } else {
      component.cache = void 0;
    }
    component.setText(formatWriteCall(renderArgs, { expanded: context.expanded, isPartial: context.isPartial }, theme2, component.cache, context.cwd));
    return component;
  },
  renderResult(result, _options, theme2, context) {
    const output = formatWriteResult({ ...result, isError: context.isError }, theme2);
    if (!output) {
      const component = context.lastComponent ?? new Container3();
      component.clear();
      return component;
    }
    const text = context.lastComponent ?? new Text7("", 0, 0);
    text.setText(output);
    return text;
  }
};

// pi-dist/pi-coding-agent/core/tools/write.js
var writeSchema = Type7.Object({
  path: Type7.String({ description: "Path to the file to write (relative or absolute)" }),
  content: Type7.String({ description: "Content to write to the file" })
});
var writeToolSystemPromptContribution = {
  snippet: "Create or overwrite files",
  guidelines: ["Use write only for new files or complete rewrites."]
};
var defaultWriteOperations = {
  writeFile: /* @__PURE__ */ __name((path4, content) => fsWriteFile2(path4, content, "utf-8"), "writeFile"),
  mkdir: /* @__PURE__ */ __name((dir) => fsMkdir(dir, { recursive: true }).then(() => {
  }), "mkdir")
};
function createWriteToolDefinition(cwd, options) {
  const ops = options?.operations ?? defaultWriteOperations;
  return {
    name: "write",
    label: "write",
    description: "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories.",
    promptSnippet: writeToolSystemPromptContribution.snippet,
    promptGuidelines: [...writeToolSystemPromptContribution.guidelines],
    parameters: writeSchema,
    constrainedSampling: { type: "json_schema", strict: "prefer" },
    async execute(_toolCallId, { path: path4, content }, signal, _onUpdate, ctx) {
      const absolutePath = resolveToCwd(path4, ctx?.cwd || cwd);
      const dir = dirname3(absolutePath);
      return withFileMutationQueue(absolutePath, async () => {
        const throwIfAborted = /* @__PURE__ */ __name(() => {
          if (signal?.aborted)
            throw new Error("Operation aborted");
        }, "throwIfAborted");
        throwIfAborted();
        await ops.mkdir(dir);
        throwIfAborted();
        await ops.writeFile(absolutePath, content);
        throwIfAborted();
        return {
          content: [{ type: "text", text: `Successfully wrote to ${path4}` }],
          details: void 0
        };
      });
    },
    ...writeRenderers
  };
}
__name(createWriteToolDefinition, "createWriteToolDefinition");
function createWriteTool(cwd, options) {
  return wrapToolDefinition(createWriteToolDefinition(cwd, options));
}
__name(createWriteTool, "createWriteTool");

// pi-dist/pi-coding-agent/core/tools/index.js
var allToolNames = /* @__PURE__ */ new Set([
  "read",
  "bash",
  "powershell",
  "edit",
  "write",
  "grep",
  "find",
  "ls"
]);
function createToolDefinition(toolName, cwd, options) {
  switch (toolName) {
    case "read":
      return createReadToolDefinition(cwd, options?.read);
    case "bash":
      return createBashToolDefinition(cwd, options?.bash);
    case "powershell":
      return createPowerShellToolDefinition(cwd, options?.powershell);
    case "edit":
      return createEditToolDefinition(cwd, options?.edit);
    case "write":
      return createWriteToolDefinition(cwd, options?.write);
    case "grep":
      return createGrepToolDefinition(cwd, options?.grep);
    case "find":
      return createFindToolDefinition(cwd, options?.find);
    case "ls":
      return createLsToolDefinition(cwd, options?.ls);
    default:
      throw new Error(`Unknown tool name: ${toolName}`);
  }
}
__name(createToolDefinition, "createToolDefinition");
function createTool(toolName, cwd, options) {
  switch (toolName) {
    case "read":
      return createReadTool(cwd, options?.read);
    case "bash":
      return createBashTool(cwd, options?.bash);
    case "powershell":
      return createPowerShellTool(cwd, options?.powershell);
    case "edit":
      return createEditTool(cwd, options?.edit);
    case "write":
      return createWriteTool(cwd, options?.write);
    case "grep":
      return createGrepTool(cwd, options?.grep);
    case "find":
      return createFindTool(cwd, options?.find);
    case "ls":
      return createLsTool(cwd, options?.ls);
    default:
      throw new Error(`Unknown tool name: ${toolName}`);
  }
}
__name(createTool, "createTool");
function createCodingToolDefinitions(cwd, options) {
  return [
    createReadToolDefinition(cwd, options?.read),
    createBashToolDefinition(cwd, options?.bash),
    createEditToolDefinition(cwd, options?.edit),
    createWriteToolDefinition(cwd, options?.write)
  ];
}
__name(createCodingToolDefinitions, "createCodingToolDefinitions");
function createReadOnlyToolDefinitions(cwd, options) {
  return [
    createReadToolDefinition(cwd, options?.read),
    createGrepToolDefinition(cwd, options?.grep),
    createFindToolDefinition(cwd, options?.find),
    createLsToolDefinition(cwd, options?.ls)
  ];
}
__name(createReadOnlyToolDefinitions, "createReadOnlyToolDefinitions");
function createAllToolDefinitions(cwd, options) {
  return {
    read: createReadToolDefinition(cwd, options?.read),
    bash: createBashToolDefinition(cwd, options?.bash),
    powershell: createPowerShellToolDefinition(cwd, options?.powershell),
    edit: createEditToolDefinition(cwd, options?.edit),
    write: createWriteToolDefinition(cwd, options?.write),
    grep: createGrepToolDefinition(cwd, options?.grep),
    find: createFindToolDefinition(cwd, options?.find),
    ls: createLsToolDefinition(cwd, options?.ls)
  };
}
__name(createAllToolDefinitions, "createAllToolDefinitions");
function createCodingTools(cwd, options) {
  return [
    createReadTool(cwd, options?.read),
    createBashTool(cwd, options?.bash),
    createEditTool(cwd, options?.edit),
    createWriteTool(cwd, options?.write)
  ];
}
__name(createCodingTools, "createCodingTools");
function createReadOnlyTools(cwd, options) {
  return [
    createReadTool(cwd, options?.read),
    createGrepTool(cwd, options?.grep),
    createFindTool(cwd, options?.find),
    createLsTool(cwd, options?.ls)
  ];
}
__name(createReadOnlyTools, "createReadOnlyTools");
function createAllTools(cwd, options) {
  return {
    read: createReadTool(cwd, options?.read),
    bash: createBashTool(cwd, options?.bash),
    powershell: createPowerShellTool(cwd, options?.powershell),
    edit: createEditTool(cwd, options?.edit),
    write: createWriteTool(cwd, options?.write),
    grep: createGrepTool(cwd, options?.grep),
    find: createFindTool(cwd, options?.find),
    ls: createLsTool(cwd, options?.ls)
  };
}
__name(createAllTools, "createAllTools");

export {
  loadPhoton,
  convertToPng,
  ensurePngTranscoder,
  resizeImage,
  formatDimensionNote,
  processImage,
  createShellRenderers,
  createLocalBashOperations,
  createBashToolDefinition,
  createBashTool,
  resolveReadPath,
  generateUnifiedPatch,
  generateDiffString,
  withFileMutationQueue,
  renderDiff,
  editRenderers,
  createEditToolDefinition,
  createEditTool,
  fetchWithRetry,
  ensureTool,
  findRenderers,
  createFindToolDefinition,
  createFindTool,
  grepRenderers,
  createGrepToolDefinition,
  createGrepTool,
  lsRenderers,
  createLsToolDefinition,
  createLsTool,
  createLocalPowerShellOperations,
  createPowerShellToolDefinition,
  createPowerShellTool,
  detectSupportedImageMimeType,
  detectSupportedImageMimeTypeFromFile,
  readRenderers,
  createReadToolDefinition,
  createReadTool,
  writeRenderers,
  createWriteToolDefinition,
  createWriteTool,
  allToolNames,
  createToolDefinition,
  createTool,
  createCodingToolDefinitions,
  createReadOnlyToolDefinitions,
  createAllToolDefinitions,
  createCodingTools,
  createReadOnlyTools,
  createAllTools
};
