import {
  ProjectTrustStore,
  addMcpServerConfig,
  getProjectTrustOptions,
  hasTrustRequiringProjectResources,
  loadMcpConfig,
  openBrowser,
  updateMcpServerConfig
} from "./chunk-IV7PGGPV.js";
import {
  ArminComponent
} from "./chunk-HAVIQ2BN.js";
import {
  AgentSession,
  BUG_REPORT_CUSTOM_ENTRY_TYPE,
  BUILTIN_PATH_PREFIX,
  CACHE_WARMING_MODES,
  CURRENT_SESSION_VERSION,
  CredentialSynchronizationError,
  DEFAULT_COMPACTION_SETTINGS,
  DEFAULT_THINKING_LEVEL,
  DefaultPackageManager,
  DefaultResourceLoader,
  ExtensionRunner,
  FooterDataProvider,
  HTTP_IDLE_TIMEOUT_CHOICES,
  InMemoryCodingAgentModelsStore,
  MissingSessionCwdError,
  ModelRegistry,
  ModelRuntime,
  SessionImportFileNotFoundError,
  SessionManager,
  SettingsManager,
  THINKING_LEVEL_OPTIONS,
  VIRTUAL_MODEL_STATE_ENTRY,
  applyHttpProxySettings,
  assertValidSessionId,
  bugReportArchiveFileName,
  bugReportFiles,
  buildContextEntries,
  buildSessionContext,
  buildSessionProjection,
  calculateContextTokens,
  collectBugReportDiagnostics,
  collectBugReportMetadata,
  collectEntriesForBranchSummary,
  compact,
  configureHttpDispatcher,
  convertToLlm,
  createAgentSessionFromServices,
  createAgentSessionRuntime,
  createAgentSessionServices,
  createCompactionSummaryMessage,
  createCustomMessage,
  createEventBus,
  createExtensionRuntime,
  createSyntheticSourceInfo,
  defaultModelPerProvider,
  defineTool,
  discoverAndLoadExtensions,
  emitProjectTrustEvent,
  estimateTokens,
  exportFromFile,
  exportSessionToJsonl,
  findCutPoint,
  findExactModelReferenceMatch,
  findTurnStartIndex,
  flushRawStdout,
  formatCacheWarmingStatus,
  formatCacheWarmingUsage,
  formatHttpIdleTimeoutMs,
  formatMissingSessionCwdPrompt,
  formatNoModelsAvailableMessage,
  formatSkillsForPrompt,
  generateBranchSummary,
  generateSummary,
  generateSummaryWithUsage,
  getLastAssistantUsage,
  getLatestCompactionEntry,
  getMissingSessionCwdIssue,
  getPiUserAgent,
  isBashToolResult,
  isBuiltinExtension,
  isEditToolResult,
  isFindToolResult,
  isGrepToolResult,
  isInstallTelemetryEnabled,
  isLsToolResult,
  isPowerShellToolResult,
  isReadToolResult,
  isSyntheticPath,
  isToolCallEventType,
  isWriteToolResult,
  loadProjectContextFiles,
  loadSkills,
  loadSkillsFromDir,
  migrateSessionEntries,
  normalizeSessionName,
  parseArgs,
  parseFrontmatter,
  parseGitUrl,
  parseSessionEntries,
  parseSkillBlock,
  prepareBranchEntries,
  printHelp,
  printTimings,
  resetTimings,
  resolveCliModel,
  resolveModelScope,
  resolveModelScopeFromModels,
  resolveModelScopeWithDiagnostics,
  restoreStdout,
  serializeConversation,
  serializeSessionBranch,
  sessionEntryToContextMessages,
  shouldCompact,
  stripFrontmatter,
  takeOverStdout,
  time,
  waitForRawStdoutBackpressure,
  wrapRegisteredTool,
  wrapRegisteredTools,
  writeBugReportArchive,
  writeRawStdout
} from "./chunk-5PYVJCJM.js";
import {
  convertToPng,
  createBashToolDefinition,
  createEditToolDefinition,
  createFindToolDefinition,
  createGrepToolDefinition,
  createLocalBashOperations,
  createLocalPowerShellOperations,
  createLsToolDefinition,
  createPowerShellToolDefinition,
  createReadToolDefinition,
  createShellRenderers,
  createWriteToolDefinition,
  detectSupportedImageMimeType,
  detectSupportedImageMimeTypeFromFile,
  editRenderers,
  ensurePngTranscoder,
  ensureTool,
  fetchWithRetry,
  findRenderers,
  formatDimensionNote,
  generateDiffString,
  generateUnifiedPatch,
  grepRenderers,
  loadPhoton,
  lsRenderers,
  processImage,
  readRenderers,
  renderDiff,
  resizeImage,
  resolveReadPath,
  withFileMutationQueue,
  writeRenderers
} from "./chunk-3WVBPXSG.js";
import {
  CODEMODE_TOOL_NAME,
  TOOL_SEARCH_TOOL_NAME,
  createCodemodeToolDefinition,
  createToolSearchToolDefinition,
  isCodemodeTool,
  isToolSearchTool
} from "./chunk-LXMQZ4LI.js";
import {
  addUsageToTotals,
  createUsageTotals,
  getUsageCostBreakdown
} from "./chunk-M5LAR3ND.js";
import "./chunk-RUCWNNX6.js";
import {
  createMcpResourceToolDefinitions,
  createMcpToolDefinition,
  createMcpToolName,
  createMcpToolRenderers
} from "./chunk-MT4ACEHI.js";
import {
  AuthStorage,
  LIST_MCP_RESOURCES_TOOL,
  LIST_MCP_RESOURCE_TEMPLATES_TOOL,
  READ_MCP_RESOURCE_TOOL,
  ReadOnlyAuthStorage,
  getMcpToolExposure,
  mcpNamespace,
  raceWithAbortSignal,
  readStoredCredential
} from "./chunk-74O2H2KA.js";
import {
  DEFAULT_MAX_BYTES,
  DEFAULT_MAX_LINES,
  formatSize,
  formatToolCallWithArgs,
  getPowerShellConfig,
  getShellConfig,
  getTextOutput,
  killTrackedDetachedChildren,
  stripAnsi,
  truncateHead,
  truncateLine,
  truncateTail,
  truncateToVisualLines
} from "./chunk-ZQYGA4IN.js";
import {
  APP_NAME,
  APP_TITLE,
  CONFIG_DIR_NAME,
  ENV_AGENT_DIR,
  ENV_SESSION_DIR,
  PACKAGE_NAME,
  SYSTEM_THEME_NAME,
  Theme,
  VERSION,
  canonicalizePath,
  detectInstallChange,
  detectInstallMethod,
  expandTildePath,
  formatKeyText,
  getAgentDir,
  getAuthPath,
  getAvailableThemes,
  getAvailableThemesWithPaths,
  getBinDir,
  getBundledInteractiveAssetPath,
  getChangelogPath,
  getCwdRelativePath,
  getDebugLogPath,
  getDocsPath,
  getEditorTheme,
  getExamplesPath,
  getLanguageFromPath,
  getMarkdownTheme,
  getPackageDir,
  getReadmePath,
  getSelectListTheme,
  getSelfUpdateCommand,
  getSelfUpdateUnavailableInstruction,
  getSettingsListTheme,
  getSettingsPath,
  getShareViewerUrl,
  getTerminalTheme,
  getThemeByName,
  highlightCode,
  initTheme,
  isLocalPath,
  keyDisplayText,
  keyHint,
  keyText,
  loadThemeFromPath,
  markTerminalColorsPending,
  normalizePath,
  onThemeChange,
  parseAutoThemeSetting,
  rawKeyHint,
  resolvePath,
  resolveThemeSetting,
  setRegisteredThemes,
  setTerminalColorScheme,
  setTerminalColors,
  setTheme,
  setThemeInstance,
  setThemeJsonValidator,
  spawnProcess,
  spawnProcessSync,
  stopThemeWatcher,
  stripBom,
  theme,
  waitForChildProcess
} from "./chunk-UMGFL43X.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-coding-agent/index.js
import {
  AgentSessionRuntime,
  createAgentSession,
  createAgentSessionFromServices as createAgentSessionFromServices2,
  createAgentSessionRuntime as createAgentSessionRuntime2,
  createAgentSessionServices as createAgentSessionServices2,
  createBashTool,
  createCodingTools,
  createEditTool,
  createFindTool,
  createGrepTool,
  createLsTool,
  createPowerShellTool,
  createReadOnlyTools,
  createReadTool,
  createWriteTool
} from "../../../independent-session.mjs";

// pi-dist/pi-coding-agent/extensions/codemode/index.js
function readMode(pi) {
  return pi.getSettings().codemode?.mode === "only" ? "only" : "on";
}
__name(readMode, "readMode");
function readInlineBudget(pi) {
  const budget = pi.getSettings().codemode?.inlineBudget;
  return typeof budget === "number" && Number.isFinite(budget) && budget >= 0 ? budget : void 0;
}
__name(readInlineBudget, "readInlineBudget");
function createCodemodeExtension(options = {}) {
  return (pi) => {
    pi.registerTool({
      ...createCodemodeToolDefinition({
        appendEntry: /* @__PURE__ */ __name((customType, data) => pi.appendEntry(customType, data), "appendEntry"),
        models: options.models ?? true,
        getToolNamespace: /* @__PURE__ */ __name((name) => pi.getAllTools().find((tool) => tool.name === name)?.namespace, "getToolNamespace"),
        getToolGuidelines: /* @__PURE__ */ __name(() => new Map(pi.getAllTools().map((tool) => [tool.name, tool.promptGuidelines ?? []])), "getToolGuidelines"),
        getMode: /* @__PURE__ */ __name(() => options.mode ?? readMode(pi), "getMode"),
        getInlineBudget: /* @__PURE__ */ __name(() => options.inlineBudget ?? readInlineBudget(pi), "getInlineBudget")
      }),
      defaultActive: false
    });
  };
}
__name(createCodemodeExtension, "createCodemodeExtension");
var codemode_default = createCodemodeExtension();

// pi-dist/pi-coding-agent/extensions/mcp/index.js
import { join as join2, resolve } from "node:path";

// pi-dist/pi-coding-agent/extensions/mcp/runtime.lazy.js
var loadMcpRuntime = /* @__PURE__ */ __name(() => import("./chunk-7JYFZT2H.js"), "loadMcpRuntime");

// pi-dist/pi-coding-agent/extensions/mcp/ui.js
import { Container as Container2, Input, SelectList, Spacer, Text as Text2, truncateToWidth, visibleWidth } from "../../../pi-tui.mjs";

// pi-dist/pi-coding-agent/modes/interactive/components/auth-url.js
import { Container, hyperlink, Text } from "../../../pi-tui.mjs";

// pi-dist/pi-coding-agent/utils/clipboard.js
import { randomUUID } from "node:crypto";
import { unlinkSync, writeFileSync } from "node:fs";
import { platform, tmpdir } from "node:os";
import { join } from "node:path";
import { getNativeClipboard } from "../../../pi-tui.mjs";

// pi-dist/pi-coding-agent/utils/clipboard-command.js
import { spawn } from "node:child_process";
function runClipboardCommand(command, args, options) {
  return new Promise((resolve7) => {
    const child = spawn(command, args, {
      stdio: ["pipe", options?.input === void 0 ? "pipe" : "ignore", "ignore"],
      windowsHide: true
    });
    const chunks = [];
    let length = 0;
    let settled = false;
    const finish = /* @__PURE__ */ __name((result) => {
      if (settled)
        return;
      settled = true;
      clearTimeout(timer);
      resolve7(result);
    }, "finish");
    const abort = /* @__PURE__ */ __name(() => {
      child.kill("SIGKILL");
      child.stdout?.destroy();
      child.stdin?.destroy();
      finish(void 0);
    }, "abort");
    const timer = setTimeout(abort, options?.timeoutMs ?? 3e3);
    child.on("error", () => finish(void 0));
    child.on("close", (code) => {
      if (!settled)
        finish(code === 0 ? Buffer.concat(chunks, length) : void 0);
    });
    child.stdout?.on("data", (chunk) => {
      if (settled)
        return;
      length += chunk.length;
      if (length > (options?.maxBufferBytes ?? 50 * 1024 * 1024))
        abort();
      else
        chunks.push(chunk);
    });
    child.stdin?.on("error", () => {
    });
    child.stdin?.end(options?.input);
  });
}
__name(runClipboardCommand, "runClipboardCommand");

// pi-dist/pi-coding-agent/utils/wsl.js
import { readFileSync } from "node:fs";
function isWSL(env = process.env) {
  if (env.WSL_DISTRO_NAME || env.WSLENV) {
    return true;
  }
  try {
    const release = readFileSync("/proc/version", "utf-8");
    return /microsoft|wsl/i.test(release);
  } catch {
    return false;
  }
}
__name(isWSL, "isWSL");

// pi-dist/pi-coding-agent/utils/clipboard.js
var MAX_OSC52_ENCODED_LENGTH = 1e5;
function isRemoteSession(env) {
  return Boolean(env.SSH_CONNECTION || env.SSH_CLIENT || env.MOSH_CONNECTION);
}
__name(isRemoteSession, "isRemoteSession");
function emitOsc52(text) {
  const encoded = Buffer.from(text).toString("base64");
  if (encoded.length > MAX_OSC52_ENCODED_LENGTH) {
    return false;
  }
  process.stdout.write(`\x1B]52;c;${encoded}\x07`);
  return true;
}
__name(emitOsc52, "emitOsc52");
async function copyViaWindowsClipboard(text) {
  const tmpFile = join(tmpdir(), `pi-wsl-clip-${randomUUID()}.txt`);
  try {
    writeFileSync(tmpFile, text, { encoding: "utf8", mode: 384 });
    const winPath = (await runClipboardCommand("wslpath", ["-w", tmpFile], { timeoutMs: 1e3 }))?.toString("utf8").trim();
    if (!winPath)
      return false;
    const script = `Set-Clipboard -Value ([System.IO.File]::ReadAllText('${winPath.replaceAll("'", "''")}', [System.Text.Encoding]::UTF8))`;
    const result = await runClipboardCommand("powershell.exe", ["-NoProfile", "-Command", script], {
      timeoutMs: 5e3
    });
    return result !== void 0;
  } catch {
    return false;
  } finally {
    try {
      unlinkSync(tmpFile);
    } catch {
    }
  }
}
__name(copyViaWindowsClipboard, "copyViaWindowsClipboard");
async function readClipboardText() {
  const commands = [];
  if (process.env.TERMUX_VERSION)
    commands.push(["termux-clipboard-get", []]);
  if (platform() === "linux") {
    if (process.env.WAYLAND_DISPLAY)
      commands.push(["wl-paste", ["--no-newline", "--type", "text"]]);
    if (process.env.DISPLAY) {
      commands.push(["xclip", ["-selection", "clipboard", "-out"]], ["xsel", ["--clipboard", "--output"]]);
    }
  }
  for (const [command, args] of commands) {
    const bytes = await runClipboardCommand(command, args, { timeoutMs: 5e3 });
    if (bytes !== void 0)
      return bytes.toString("utf8") || null;
  }
  try {
    return await getNativeClipboard()?.getText() || null;
  } catch {
    return null;
  }
}
__name(readClipboardText, "readClipboardText");
async function readClipboardFilePaths() {
  const paths = await getNativeClipboard()?.getFilePaths?.();
  return paths?.length ? paths : null;
}
__name(readClipboardFilePaths, "readClipboardFilePaths");
async function copyToClipboard(text) {
  const p = platform();
  const env = process.env;
  let copied = false;
  if (p !== "linux") {
    try {
      const clipboard = getNativeClipboard();
      if (clipboard?.setText) {
        await clipboard.setText(text);
        copied = true;
      }
    } catch {
    }
  }
  if (!copied) {
    const commands = [];
    if (p === "darwin")
      commands.push(["pbcopy", []]);
    else if (p === "win32")
      commands.push(["clip", []]);
    else {
      if (env.TERMUX_VERSION)
        commands.push(["termux-clipboard-set", []]);
      if (env.WAYLAND_DISPLAY)
        commands.push(["wl-copy", []]);
      if (env.DISPLAY) {
        commands.push(["xclip", ["-selection", "clipboard"]], ["xsel", ["--clipboard", "--input"]]);
      }
    }
    for (const [command, args] of commands) {
      if (await runClipboardCommand(command, args, { input: text, timeoutMs: 5e3 }) !== void 0) {
        copied = true;
        break;
      }
    }
  }
  let osc52Emitted = false;
  if (!copied && p === "linux" && isWSL(env)) {
    if (env.WT_SESSION)
      osc52Emitted = emitOsc52(text);
    copied = osc52Emitted || await copyViaWindowsClipboard(text);
  }
  const headless = p === "linux" && !env.DISPLAY && !env.WAYLAND_DISPLAY && !env.TERMUX_VERSION;
  let oversized = false;
  if (!osc52Emitted && (isRemoteSession(env) || !copied && headless)) {
    if (emitOsc52(text))
      copied = true;
    else
      oversized = true;
  }
  if (copied)
    return;
  if (oversized)
    throw new Error("Clipboard unavailable: text exceeds the OSC 52 size limit");
  if (env.TERMUX_VERSION) {
    throw new Error("Clipboard unavailable: install the Termux:API app and `termux-api` package");
  }
  if (p === "linux") {
    if (env.WAYLAND_DISPLAY) {
      throw new Error("Clipboard unavailable: install `wl-clipboard` (`wl-copy`) or check Wayland access");
    }
    if (env.DISPLAY) {
      throw new Error("Clipboard unavailable: install `xclip` or `xsel`, or check X11 access");
    }
  }
  throw new Error("Clipboard unavailable");
}
__name(copyToClipboard, "copyToClipboard");

// pi-dist/pi-coding-agent/modes/interactive/components/auth-url.js
var AuthUrlComponent = class extends Container {
  static {
    __name(this, "AuthUrlComponent");
  }
  url;
  tui;
  hint;
  constructor(tui, url) {
    super();
    this.tui = tui;
    this.url = url;
    this.addChild(new Text(theme.fg("accent", hyperlink(url, url)), 1, 0));
    this.hint = new Text("", 1, 0);
    this.addChild(this.hint);
    this.setHint(keyHint("app.message.copy", "to copy"));
  }
  setHint(suffix) {
    const clickHint = process.platform === "darwin" ? "Cmd+click to open" : "Ctrl+click to open";
    this.hint.setText(`${theme.fg("dim", hyperlink(clickHint, this.url))} ${theme.fg("dim", "\u2022")} ${suffix}`);
    this.tui.requestRender();
  }
  async copy() {
    try {
      await copyToClipboard(this.url);
      this.setHint(theme.fg("success", "Copied URL to clipboard"));
    } catch (error) {
      this.setHint(theme.fg("error", error instanceof Error ? error.message : String(error)));
    }
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/dynamic-border.js
var DynamicBorder = class {
  static {
    __name(this, "DynamicBorder");
  }
  color;
  constructor(color = (str) => theme.fg("border", str)) {
    this.color = color;
  }
  invalidate() {
  }
  render(width) {
    return [this.color("\u2500".repeat(Math.max(1, width)))];
  }
};

// pi-dist/pi-coding-agent/extensions/mcp/ui.js
function frame(theme2, title, body, footer) {
  const container = new Container2();
  container.addChild(new DynamicBorder((text) => theme2.fg("accent", text)));
  container.addChild(new Text2(theme2.fg("accent", theme2.bold(title)), 1, 0));
  for (const child of body)
    container.addChild(child);
  if (footer) {
    container.addChild(new Spacer(1));
    container.addChild(new Text2(theme2.fg("dim", footer), 1, 0));
  }
  container.addChild(new DynamicBorder((text) => theme2.fg("accent", text)));
  return container;
}
__name(frame, "frame");
var MAX_VISIBLE_ITEMS = 12;
var McpManagerView = class {
  static {
    __name(this, "McpManagerView");
  }
  tui;
  theme;
  keybindings;
  content;
  inputHandler;
  inputTarget;
  _focused = false;
  constructor(tui, theme2, keybindings) {
    this.tui = tui;
    this.theme = theme2;
    this.keybindings = keybindings;
    this.content = frame(theme2, "MCP servers", [new Text2(theme2.fg("muted", "Loading\u2026"), 1, 1)]);
  }
  get focused() {
    return this._focused;
  }
  set focused(value) {
    this._focused = value;
    if (this.inputTarget)
      this.inputTarget.focused = value;
  }
  setContent(content, inputHandler, inputTarget) {
    if (this.inputTarget)
      this.inputTarget.focused = false;
    this.content = content;
    this.inputHandler = inputHandler;
    this.inputTarget = inputTarget;
    if (this.inputTarget)
      this.inputTarget.focused = this._focused;
    this.tui.requestRender();
  }
  menu(build, subscribe) {
    return new Promise((resolve7) => {
      let unsubscribe;
      let settled = false;
      const finish = /* @__PURE__ */ __name((value) => {
        if (settled)
          return;
        settled = true;
        unsubscribe?.();
        resolve7(value);
      }, "finish");
      let selected;
      const render2 = /* @__PURE__ */ __name(() => {
        const menu = build();
        const wanted = selected ?? menu.selected;
        const body = [];
        if (menu.details)
          body.push(new Text2(this.theme.fg("muted", menu.details), 1, 0));
        if (menu.error)
          body.push(new Text2(this.theme.fg("error", menu.error), 1, 0));
        body.push(new Spacer(1));
        const footer = `${keyHint("tui.select.confirm", menu.confirmLabel)} \u2022 ${keyHint("tui.select.cancel", menu.cancelLabel)}`;
        if (menu.items.length === 0) {
          body.push(new Text2(this.theme.fg("muted", menu.empty ?? "Nothing to show."), 1, 0));
          this.setContent(frame(this.theme, menu.title, body, keyHint("tui.select.cancel", menu.cancelLabel)), (data) => {
            if (this.keybindings.matches(data, "tui.select.cancel"))
              finish(void 0);
          });
          return;
        }
        const list = new SelectList(menu.items, Math.min(menu.items.length, MAX_VISIBLE_ITEMS), getSelectListTheme());
        const index = menu.items.findIndex((item) => item.value === wanted);
        if (index !== -1)
          list.setSelectedIndex(index);
        selected = list.getSelectedItem()?.value;
        list.onSelectionChange = (item) => {
          selected = item.value;
        };
        list.onSelect = (item) => finish(item.value);
        list.onCancel = () => finish(void 0);
        body.push(list);
        this.setContent(frame(this.theme, menu.title, body, footer), (data) => list.handleInput(data));
      }, "render");
      render2();
      unsubscribe = subscribe?.(() => {
        if (!settled)
          render2();
      });
    });
  }
  status(title, message, onCancel) {
    const body = [new Spacer(1), new Text2(this.theme.fg("muted", message), 1, 0)];
    if (!onCancel) {
      this.setContent(frame(this.theme, title, body));
      return;
    }
    this.setContent(frame(this.theme, title, body, keyHint("tui.select.cancel", "cancel")), (data) => {
      if (this.keybindings.matches(data, "tui.select.cancel"))
        onCancel();
    });
  }
  redirectUrl(title, authorizationUrl, signal) {
    return new Promise((resolve7) => {
      let settled = false;
      const finish = /* @__PURE__ */ __name((value) => {
        if (settled)
          return;
        settled = true;
        signal.removeEventListener("abort", onAbort);
        resolve7(value);
      }, "finish");
      const onAbort = /* @__PURE__ */ __name(() => finish(void 0), "onAbort");
      if (signal.aborted) {
        finish(void 0);
        return;
      }
      signal.addEventListener("abort", onAbort, { once: true });
      const input2 = new Input();
      const link = new AuthUrlComponent(this.tui, authorizationUrl);
      const body = [
        new Spacer(1),
        new Text2(this.theme.fg("muted", "Approve access in your browser. If it did not open, visit:"), 1, 0),
        link,
        new Spacer(1),
        new Text2(this.theme.fg("muted", "If the browser runs on another machine, paste the URL it was redirected to:"), 1, 0),
        input2
      ];
      this.setContent(frame(this.theme, title, body, `${keyHint("tui.select.confirm", "submit")} \u2022 ${keyHint("tui.select.cancel", "cancel")}`), (data) => {
        if (this.keybindings.matches(data, "tui.select.confirm")) {
          const value = input2.getValue().trim();
          if (value)
            finish(value);
          return;
        }
        if (this.keybindings.matches(data, "tui.select.cancel")) {
          finish(void 0);
          return;
        }
        if (this.keybindings.matches(data, "app.message.copy")) {
          void link.copy();
          return;
        }
        input2.handleInput(data);
      }, input2);
    });
  }
  handleInput(data) {
    this.inputHandler?.(data);
    this.tui.requestRender();
  }
  render(width) {
    return this.content.render(width).map((line) => visibleWidth(line) > width ? truncateToWidth(line, width, "") : line);
  }
  invalidate() {
    this.content.invalidate();
  }
};
async function showMcpManager(ctx, manage) {
  await ctx.ui.custom((tui, theme2, keybindings, done) => {
    const view = new McpManagerView(tui, theme2, keybindings);
    void manage(view).then(() => done(), (error) => {
      ctx.ui.notify(error instanceof Error ? error.message : String(error), "error");
      done();
    });
    return view;
  });
}
__name(showMcpManager, "showMcpManager");

// pi-dist/pi-coding-agent/extensions/mcp/index.js
var DEFAULT_STARTUP_WAIT_MS = 1e4;
var RESOURCE_TOOL_NAMES = /* @__PURE__ */ new Set([
  LIST_MCP_RESOURCES_TOOL,
  LIST_MCP_RESOURCE_TEMPLATES_TOOL,
  READ_MCP_RESOURCE_TOOL
]);
var EXPOSURE_DESCRIPTIONS = {
  codemode: "called from codemode scripts, which find them with searchTools()",
  deferred: "not declared until tool_search loads them, then called directly; no codemode needed",
  direct: "declared to the model like built-in tools"
};
function errorMessage(error) {
  return error instanceof Error ? error.message : String(error);
}
__name(errorMessage, "errorMessage");
function firstLine(text) {
  return text.split("\n", 1)[0] ?? "";
}
__name(firstLine, "firstLine");
function isEnabled(server) {
  return server.entry.config.enabled !== false;
}
__name(isEnabled, "isEnabled");
function exposureOf(entry) {
  return entry.config.exposure ?? "codemode";
}
__name(exposureOf, "exposureOf");
function configuredExposures(entry) {
  return /* @__PURE__ */ new Set([exposureOf(entry), ...Object.values(entry.config.toolExposure ?? {})]);
}
__name(configuredExposures, "configuredExposures");
function hasDirectTools(entry) {
  return configuredExposures(entry).has("direct");
}
__name(hasDirectTools, "hasDirectTools");
function hasIndirectTools(entry) {
  const exposures = configuredExposures(entry);
  return exposures.has("codemode") || exposures.has("deferred");
}
__name(hasIndirectTools, "hasIndirectTools");
var MCP_SERVERS_SECTION = "mcp_servers";
var MAX_SERVER_DESCRIPTION_CHARS = 250;
var MAX_SERVERS_SECTION_CHARS = 4096;
function serversSectionIntro(reaches) {
  let intro = "MCP servers whose tools are not declared to you.";
  if (reaches.has("codemode"))
    intro += " Call the tools of `codemode` servers from codemode scripts.";
  if (reaches.has("tool_search"))
    intro += " Load the tools of `tool_search` servers with `tool_search`.";
  return intro;
}
__name(serversSectionIntro, "serversSectionIntro");
function truncate(text, max) {
  if (text.length <= max)
    return text;
  return max <= 1 ? "" : `${text.slice(0, max - 1).trimEnd()}\u2026`;
}
__name(truncate, "truncate");
function serverSummary(server) {
  const text = server.entry.config.description?.trim() || server.connection?.instructions || "";
  return (text.split("\n", 1)[0] ?? "").trim();
}
__name(serverSummary, "serverSummary");
function renderServersSection(servers) {
  const listed = servers.filter((server) => isEnabled(server) && hasIndirectTools(server.entry)).sort((a, b) => a.entry.name.localeCompare(b.entry.name));
  if (listed.length === 0)
    return void 0;
  const reaches = listed.map((server) => configuredExposures(server.entry).has("codemode") ? "codemode" : "tool_search");
  const intro = serversSectionIntro(new Set(reaches));
  const heads = listed.map((server, index) => `- ${mcpNamespace(server.entry.name)} (${reaches[index]})`);
  const omitted = /* @__PURE__ */ __name((count) => count > 0 ? [`- \u2026 ${count} more server${count === 1 ? "" : "s"}; find their tools with searchTools()`] : [], "omitted");
  const size = /* @__PURE__ */ __name((kept2) => [intro, ...heads.slice(0, kept2), ...omitted(listed.length - kept2)].join("\n").length, "size");
  let kept = listed.length;
  while (kept > 0 && size(kept) > MAX_SERVERS_SECTION_CHARS)
    kept--;
  const perServer = kept === 0 ? 0 : Math.min(MAX_SERVER_DESCRIPTION_CHARS, Math.floor((MAX_SERVERS_SECTION_CHARS - size(kept)) / kept) - 2);
  const lines = listed.slice(0, kept).map((server, index) => {
    const summary = perServer > 0 ? truncate(serverSummary(server), perServer) : "";
    return summary ? `${heads[index]}: ${summary}` : heads[index];
  });
  return [intro, ...lines, ...omitted(listed.length - kept)].join("\n");
}
__name(renderServersSection, "renderServersSection");
function scriptNeedsServer(code, server) {
  if (/\b(searchTools|describeNamespace|describeTool|ALL_TOOLS)\b/.test(code))
    return true;
  return code.includes(mcpNamespace(server));
}
__name(scriptNeedsServer, "scriptNeedsServer");
function describeState(server, withError = true) {
  if (!isEnabled(server))
    return "disabled";
  const connection = server.connection;
  if (!connection)
    return "starting";
  switch (connection.state) {
    case "needs-auth":
      return "needs sign-in";
    case "failed":
      return withError ? `failed: ${firstLine(connection.error ?? "unknown error")}` : "failed";
    case "connected": {
      const { tools, resources } = connection;
      const count = resources.length;
      const resourceCount = count > 0 ? ` \xB7 ${count} resource${count === 1 ? "" : "s"}` : "";
      return `connected \xB7 ${tools.length} tool${tools.length === 1 ? "" : "s"}${resourceCount}`;
    }
    case "connecting":
      return "connecting\u2026";
    default:
      return connection.state;
  }
}
__name(describeState, "describeState");
function attentionRank(server) {
  if (!isEnabled(server))
    return 5;
  switch (server.connection?.state) {
    case "needs-auth":
      return 0;
    case "failed":
      return 1;
    case "disconnected":
      return 2;
    case "connected":
      return 4;
    default:
      return 3;
  }
}
__name(attentionRank, "attentionRank");
function describeTransport(entry) {
  const { config } = entry;
  if ("url" in config)
    return config.url;
  return [config.command, ...config.args ?? []].join(" ");
}
__name(describeTransport, "describeTransport");
var MCP_USAGE = "Usage: /mcp, /mcp login [server], /mcp logout [server], /mcp reconnect [server]";
function createMcpExtension(options = {}) {
  return (pi) => {
    let servers = [];
    let configuredEntries = [];
    let configErrors = [];
    let projectConfig;
    let overridden = [];
    let autoEnableCodemode = true;
    let warnedUnreachable = false;
    let pending;
    let waitedForStartup = false;
    const startupWaitMs = options.startupWaitMs ?? DEFAULT_STARTUP_WAIT_MS;
    let sessionCwd = process.cwd();
    let credentials = options.credentials;
    let modelRegistry;
    let serverLog;
    const openUrl = options.openUrl ?? openBrowser;
    const updateConfig = options.updateConfig ?? ((entry, patch) => updateMcpServerConfig(entry.override ?? entry.source, entry.name, patch, {
      override: entry.override !== void 0
    }));
    let session = new AbortController();
    session.abort();
    const backgroundActions = /* @__PURE__ */ new Set();
    const track = /* @__PURE__ */ __name((work) => {
      const task = work.then(() => void 0, () => void 0);
      backgroundActions.add(task);
      void task.finally(() => backgroundActions.delete(task));
      return work;
    }, "track");
    const listeners = /* @__PURE__ */ new Set();
    const emitChange = /* @__PURE__ */ __name(() => {
      for (const listener of listeners)
        listener();
    }, "emitChange");
    const subscribe = /* @__PURE__ */ __name((listener) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    }, "subscribe");
    const connections = /* @__PURE__ */ __name(() => servers.flatMap((server) => server.connection ? [server.connection] : []), "connections");
    const findServer = /* @__PURE__ */ __name((name) => servers.find((server) => server.entry.name === name), "findServer");
    const registeredServers = /* @__PURE__ */ __name(() => {
      const registered = [];
      const overriddenNames = [];
      for (const { name, config, extensionPath } of pi.getMcpServers()) {
        const configured = configuredEntries.find((entry) => mcpNamespace(entry.name) === mcpNamespace(name));
        if (configured) {
          overriddenNames.push(`"${name}" registered by ${extensionPath} is overridden by "${configured.name}" in ${configured.source}`);
          continue;
        }
        registered.push({
          entry: { name, config, source: extensionPath, scope: "extension" },
          registeredConfig: JSON.stringify(config)
        });
      }
      return { servers: registered, overridden: overriddenNames };
    }, "registeredServers");
    const getCredentials = /* @__PURE__ */ __name((runtime) => {
      credentials ??= new runtime.McpOAuthCredentialStore();
      return credentials;
    }, "getCredentials");
    const getServerLog = /* @__PURE__ */ __name((runtime) => {
      serverLog ??= new runtime.McpServerLog(options.logPath ?? join2(getAgentDir(), "mcp.log"));
      return serverLog;
    }, "getServerLog");
    const toolOwners = /* @__PURE__ */ new Map();
    const serverTools = /* @__PURE__ */ new Map();
    const definitions = /* @__PURE__ */ new Map();
    pi.registerToolRenderer((toolName, next) => {
      const match = /^mcp__(.+?)__(.+)$/.exec(toolName);
      return next() ?? (match ? createMcpToolRenderers(`${match[1]}/${match[2]}`) : void 0);
    });
    const registerTools = /* @__PURE__ */ __name((connection) => {
      const server = connection.entry.name;
      const entry = findServer(server)?.entry ?? connection.entry;
      const description = entry.config.description?.trim();
      const namespace = {
        name: mcpNamespace(server),
        ...description ? { description } : {},
        ...connection.instructions ? { instructions: connection.instructions } : {}
      };
      const previous = serverTools.get(server) ?? /* @__PURE__ */ new Set();
      const current = /* @__PURE__ */ new Set();
      const plain = [...new Set(connection.tools.map((tool) => tool.name))].map((tool) => createMcpToolName(server, tool));
      const assignName = /* @__PURE__ */ __name((tool, owner) => {
        const name = createMcpToolName(server, tool, (candidate) => {
          const existing = toolOwners.get(candidate);
          return existing !== void 0 && existing !== owner || current.has(candidate) || plain.indexOf(candidate) !== plain.lastIndexOf(candidate);
        });
        toolOwners.set(name, owner);
        current.add(name);
        return name;
      }, "assignName");
      for (const tool of connection.tools) {
        const definition = createMcpToolDefinition({
          server,
          tool,
          name: assignName(tool.name, `${server}\0${tool.name}`),
          exposure: getMcpToolExposure(entry.config, tool.name),
          namespace,
          timeoutMs: connection.timeoutMs,
          getClient: /* @__PURE__ */ __name(async () => {
            const current2 = findServer(server);
            if (!current2 || !isEnabled(current2))
              throw new Error(`MCP server "${server}" is disabled.`);
            const client = current2.connection;
            if (!client)
              throw new Error(`MCP server "${server}" is still starting.`);
            if (getMcpToolExposure(current2.entry.config, tool.name) === "hidden" || client.state === "connected" && !client.tools.some((offered) => offered.name === tool.name)) {
              throw new Error(`MCP tool "${server}/${tool.name}" is no longer available.`);
            }
            return client;
          }, "getClient"),
          readableResources: /* @__PURE__ */ __name(() => resourceServers().some((current2) => current2.name === server), "readableResources")
        });
        definitions.set(definition.name, definition);
        pi.registerTool(definition);
      }
      serverTools.set(server, current);
      for (const name of previous) {
        const definition = definitions.get(name);
        if (!current.has(name) && definition)
          pi.registerTool({ ...definition, exposure: "hidden" });
      }
      syncResourceTools();
    }, "registerTools");
    const hideTools = /* @__PURE__ */ __name((server) => {
      for (const name of serverTools.get(server) ?? []) {
        const definition = definitions.get(name);
        if (definition)
          pi.registerTool({ ...definition, exposure: "hidden" });
      }
      serverTools.set(server, /* @__PURE__ */ new Set());
      syncResourceTools();
    }, "hideTools");
    const serversWithResources = /* @__PURE__ */ __name(() => servers.filter((server) => server.connection?.hasResources && isEnabled(server) && exposureOf(server.entry) !== "hidden"), "serversWithResources");
    const resourceServers = /* @__PURE__ */ __name(() => serversWithResources().flatMap((server) => server.connection ? [server.connection] : []), "resourceServers");
    let resourceToolsExposure;
    const syncResourceTools = /* @__PURE__ */ __name(() => {
      const exposures = new Set(serversWithResources().map((server) => exposureOf(server.entry)));
      const exposure = ["direct", "codemode", "deferred"].find((candidate) => exposures.has(candidate));
      const next = exposure ?? "hidden";
      if (next === resourceToolsExposure || resourceToolsExposure === void 0 && next === "hidden")
        return;
      const wasDirect = resourceToolsExposure === "direct";
      resourceToolsExposure = next;
      const resourceDefinitions = createMcpResourceToolDefinitions({ exposure: next, servers: resourceServers });
      for (const definition of resourceDefinitions)
        pi.registerTool(definition);
      if (wasDirect) {
        const names = new Set(resourceDefinitions.map((definition) => definition.name));
        pi.setActiveTools(pi.getActiveTools().filter((name) => !names.has(name)));
      }
    }, "syncResourceTools");
    const ensureDiscoveryActive = /* @__PURE__ */ __name((ctx) => {
      const exposures = /* @__PURE__ */ new Set();
      for (const server of servers) {
        if (isEnabled(server))
          for (const exposure of configuredExposures(server.entry))
            exposures.add(exposure);
      }
      const needsCodemode = exposures.has("codemode");
      const needsToolSearch = exposures.has("deferred");
      if (!needsCodemode && !needsToolSearch)
        return;
      const tools = pi.getAllTools();
      const hasCodemode = tools.some(isCodemodeTool);
      const hasToolSearch = tools.some(isToolSearchTool);
      const active = pi.getActiveTools();
      const activate = [];
      if (needsCodemode && hasCodemode && autoEnableCodemode && !active.includes(CODEMODE_TOOL_NAME)) {
        activate.push(CODEMODE_TOOL_NAME);
      }
      if (needsToolSearch && hasToolSearch && !active.includes(TOOL_SEARCH_TOOL_NAME)) {
        activate.push(TOOL_SEARCH_TOOL_NAME);
      }
      if (activate.length > 0)
        pi.setActiveTools([...active, ...activate]);
      const reachable = [...active, ...activate];
      if (hasCodemode && reachable.includes(CODEMODE_TOOL_NAME))
        return;
      if (hasToolSearch && reachable.includes(TOOL_SEARCH_TOOL_NAME))
        return;
      if (warnedUnreachable)
        return;
      warnedUnreachable = true;
      const reason = needsCodemode && hasCodemode && !autoEnableCodemode ? " (autoEnableCodemode is false)" : "";
      ctx.ui.notify(`MCP tools are only reachable from the codemode or tool_search tool, but neither is active${reason}; they cannot be called.`, "warning");
    }, "ensureDiscoveryActive");
    const tokensAtSignIn = /* @__PURE__ */ new Map();
    const storedTokens = /* @__PURE__ */ __name((connection) => {
      const url = connection.oauthUrl;
      return url && credentials ? JSON.stringify(credentials.tokens(connection.name, url) ?? null) : "null";
    }, "storedTokens");
    const onConnectionChange = /* @__PURE__ */ __name((connection) => {
      if (connection.state !== "needs-auth")
        tokensAtSignIn.delete(connection);
      else if (!tokensAtSignIn.has(connection))
        tokensAtSignIn.set(connection, storedTokens(connection));
      emitChange();
    }, "onConnectionChange");
    const reconnectSignedIn = /* @__PURE__ */ __name(async (ctx) => {
      const signedIn = [...tokensAtSignIn].filter(([connection, tokens]) => storedTokens(connection) !== tokens);
      if (signedIn.length === 0)
        return;
      for (const [connection] of signedIn)
        tokensAtSignIn.delete(connection);
      await Promise.allSettled(signedIn.map(([connection]) => connection.reconnect()));
      ensureDiscoveryActive(ctx);
    }, "reconnectSignedIn");
    const createConnection = /* @__PURE__ */ __name(async (server, isCurrent) => {
      const runtime = await loadMcpRuntime();
      if (!isCurrent())
        return void 0;
      const connection = new runtime.McpServerConnection({
        entry: server.entry,
        cwd: sessionCwd,
        createTransport: options.createTransport ?? runtime.createDefaultTransport,
        credentials: getCredentials(runtime),
        providerToken: /* @__PURE__ */ __name(async (provider) => modelRegistry?.getApiKeyForProvider(provider), "providerToken"),
        log: getServerLog(runtime),
        onTools: /* @__PURE__ */ __name((connection2) => {
          if (isCurrent())
            registerTools(connection2);
        }, "onTools"),
        onChange: /* @__PURE__ */ __name((connection2) => {
          if (isCurrent())
            onConnectionChange(connection2);
          else
            tokensAtSignIn.delete(connection2);
        }, "onChange")
      });
      server.connection = connection;
      emitChange();
      return connection;
    }, "createConnection");
    const startConnection = /* @__PURE__ */ __name((server, after) => {
      const { signal } = session;
      const attempt = /* @__PURE__ */ Symbol();
      server.attempt = attempt;
      const isCurrent = /* @__PURE__ */ __name(() => !signal.aborted && server.attempt === attempt && isEnabled(server) && servers.includes(server), "isCurrent");
      const ready = (async () => {
        await after;
        if (!isCurrent())
          return;
        const connection = await createConnection(server, isCurrent);
        if (!connection || !isCurrent())
          return;
        await connection.getClient().catch(() => void 0);
      })();
      server.ready = ready.catch(() => void 0);
      return ready;
    }, "startConnection");
    const waitForServers = /* @__PURE__ */ __name(async (waiting, signal) => {
      for (; ; ) {
        const ready = waiting.flatMap((server) => isEnabled(server) && servers.includes(server) && server.ready ? [{ server, ready: server.ready }] : []);
        if (ready.length === 0 || signal?.aborted)
          return;
        let onAbort;
        await Promise.race([
          Promise.all(ready.map((attempt) => attempt.ready)),
          new Promise((resolve7) => {
            onAbort = /* @__PURE__ */ __name(() => resolve7(), "onAbort");
            signal?.addEventListener("abort", onAbort, { once: true });
          })
        ]);
        if (onAbort)
          signal?.removeEventListener("abort", onAbort);
        if (signal?.aborted || ready.every((attempt) => !isEnabled(attempt.server) || !servers.includes(attempt.server) || attempt.ready === attempt.server.ready))
          return;
      }
    }, "waitForServers");
    const reportProblems = /* @__PURE__ */ __name((ctx, only) => {
      const lines = only ? [] : configErrors.map((error) => `config: ${error}`);
      for (const server of only ?? servers) {
        const state = server.connection?.state;
        if (state === "needs-auth" || state === "failed")
          lines.push(`${server.entry.name}: ${describeState(server)}`);
      }
      if (lines.length === 0)
        return;
      ctx.ui.notify(`MCP servers need attention:
${lines.map((line) => `  ${line}`).join("\n")}
Run /mcp to fix.`, "warning");
    }, "reportProblems");
    const saveConfig = /* @__PURE__ */ __name((server, patch, inProject = false) => {
      const override = inProject ? projectConfig : server.entry.override;
      const entry = override ? { ...server.entry, override } : server.entry;
      if (entry.scope !== "extension") {
        try {
          updateConfig(entry, patch);
        } catch (error) {
          return `Could not update ${entry.override ?? entry.source}: ${errorMessage(error)}`;
        }
      }
      server.entry = { ...entry, config: { ...entry.config, ...patch } };
      return void 0;
    }, "saveConfig");
    const signIn = /* @__PURE__ */ __name(async (server, prompt, cancel) => {
      const connection = server.connection;
      const url = connection?.oauthUrl;
      if (!connection || !url)
        return `MCP server "${server.entry.name}" does not use OAuth.`;
      const runtime = await loadMcpRuntime();
      const signal = cancel ? AbortSignal.any([session.signal, cancel]) : session.signal;
      try {
        await track(runtime.signInMcpServer({
          serverUrl: url,
          store: getCredentials(runtime).forServer(server.entry.name, url),
          settings: connection.oauthSettings(),
          challenge: connection.challenge,
          prompt,
          signal
        }));
      } catch (error) {
        if (error instanceof runtime.McpSignInCancelledError)
          return "Sign-in cancelled.";
        return `Sign-in failed: ${errorMessage(error)}`;
      }
      connection.challenge = void 0;
      try {
        await connection.reconnect();
      } catch (error) {
        return `Signed in, but ${errorMessage(error)}`;
      }
      return void 0;
    }, "signIn");
    const signOut = /* @__PURE__ */ __name(async (server) => {
      const connection = server.connection;
      const url = connection?.oauthUrl;
      if (!connection || !url)
        return false;
      const removed = getCredentials(await loadMcpRuntime()).remove(server.entry.name, url);
      await connection.signOut();
      return removed;
    }, "signOut");
    const reconnect = /* @__PURE__ */ __name(async (server) => {
      const connection = server.connection;
      if (!connection)
        return `MCP server "${server.entry.name}" is disabled.`;
      const previous = server.ready;
      const ready = (async () => {
        await previous;
        await connection.reconnect();
      })();
      server.ready = ready.catch(() => void 0);
      try {
        await ready;
        return void 0;
      } catch (error) {
        return errorMessage(error);
      }
    }, "reconnect");
    const setEnabled = /* @__PURE__ */ __name(async (server, enabled, inProject = false) => {
      const failed = saveConfig(server, { enabled }, inProject);
      if (failed)
        return failed;
      if (!enabled) {
        const connection = server.connection;
        server.attempt = void 0;
        server.connection = void 0;
        if (connection) {
          server.closing = Promise.all([connection.close(), server.ready]).then(() => void 0);
        }
        const closing = server.closing;
        hideTools(server.entry.name);
        emitChange();
        try {
          await closing;
        } finally {
          if (server.closing === closing)
            server.closing = void 0;
        }
        return void 0;
      }
      await startConnection(server, server.closing);
      return void 0;
    }, "setEnabled");
    const setExposure = /* @__PURE__ */ __name((server, exposure) => {
      const failed = saveConfig(server, { exposure });
      if (failed)
        return failed;
      if (server.connection?.state === "connected")
        registerTools(server.connection);
      syncResourceTools();
      const indirect = new Set(pi.getAllTools().filter((tool) => tool.exposure !== "direct").map((tool) => tool.name));
      const tools = serverTools.get(server.entry.name) ?? /* @__PURE__ */ new Set();
      pi.setActiveTools(pi.getActiveTools().filter((name) => !tools.has(name) || !indirect.has(name)));
      emitChange();
      return void 0;
    }, "setExposure");
    const notices = /* @__PURE__ */ __name(() => [
      ...configErrors.map((error) => `config: ${error}`),
      ...overridden.map((line) => `overridden: ${line}`)
    ], "notices");
    const serversMenu = /* @__PURE__ */ __name(() => ({
      title: "MCP servers",
      error: notices().join("\n") || void 0,
      items: [...servers].sort((a, b) => attentionRank(a) - attentionRank(b) || a.entry.name.localeCompare(b.entry.name)).map((server) => ({
        value: server.entry.name,
        label: server.entry.name,
        description: `${describeState(server)} \xB7 ${exposureOf(server.entry)} \xB7 ${server.entry.override ? "global, project override" : server.entry.scope ?? server.entry.source}`
      })),
      empty: `No MCP servers configured. Add them to ${resolve(getAgentDir(), "mcp.json")} or .pi/mcp.json.`,
      confirmLabel: "manage",
      cancelLabel: "close"
    }), "serversMenu");
    const serverMenu = /* @__PURE__ */ __name((name) => {
      const server = findServer(name);
      if (!server) {
        return {
          title: name,
          items: [],
          empty: "This server is no longer configured.",
          confirmLabel: "",
          cancelLabel: "back"
        };
      }
      const { entry, connection } = server;
      const saved = entry.scope === "extension" ? "for this session" : entry.override ? "saved to the project mcp.json" : entry.scope ? `saved to the ${entry.scope} mcp.json` : "saved to mcp.json";
      const inProject = entry.scope === "global" && !entry.override && projectConfig !== void 0;
      const inProjectSaved = "saved to the project mcp.json";
      const items = [];
      if (!isEnabled(server)) {
        items.push({ value: "enable", label: "Enable", description: saved });
        if (inProject) {
          items.push({ value: "enable-project", label: "Enable in this project", description: inProjectSaved });
        }
      } else {
        const state = connection?.state;
        if (state === "needs-auth")
          items.push({ value: "signin", label: "Sign in", description: "opens the browser" });
        if (state === "connected" && connection) {
          items.push({ value: "tools", label: "Tools", description: `${connection.tools.length} offered` });
        }
        if (state === "failed" || state === "disconnected" || state === "connected" || state === "needs-auth") {
          items.push({ value: "reconnect", label: "Reconnect" });
        }
        if (state === "connected" && connection?.oauthUrl) {
          items.push({ value: "signout", label: "Sign out", description: "deletes the stored credentials" });
        }
        items.push({ value: "exposure", label: "Exposure", description: exposureOf(entry) });
        items.push({ value: "disable", label: "Disable", description: saved });
        if (inProject) {
          items.push({ value: "disable-project", label: "Disable in this project", description: inProjectSaved });
        }
      }
      const details = [
        describeTransport(entry),
        `${entry.scope ?? "config"}: ${entry.source}`,
        ...entry.override ? [`project override: ${entry.override}`] : [],
        `State: ${describeState(server, false)}`
      ];
      const error = [server.message, connection?.state === "connected" ? void 0 : connection?.error].filter((line) => line !== void 0).join("\n");
      return {
        title: `MCP server ${name}`,
        details: details.join("\n"),
        error: error || void 0,
        items,
        selected: items[0]?.value,
        confirmLabel: "select",
        cancelLabel: "back"
      };
    }, "serverMenu");
    const showTools = /* @__PURE__ */ __name(async (ui, server) => {
      const exposure = exposureOf(server.entry);
      const overridden2 = Object.keys(server.entry.config.toolExposure ?? {}).length > 0;
      await ui.menu(() => ({
        title: `Tools of ${server.entry.name}`,
        details: `Exposure ${exposure}: ${exposure === "hidden" ? "unreachable" : EXPOSURE_DESCRIPTIONS[exposure]}${overridden2 ? "\nSome tools override it with toolExposure." : ""}`,
        items: (server.connection?.tools ?? []).map((tool) => {
          const toolExposure = getMcpToolExposure(server.entry.config, tool.name);
          const description = firstLine(tool.description ?? "");
          return {
            value: tool.name,
            label: tool.name,
            description: toolExposure === exposure ? description : `[${toolExposure}] ${description}`
          };
        }),
        empty: "The server offers no tools.",
        confirmLabel: "back",
        cancelLabel: "back"
      }));
    }, "showTools");
    const chooseExposure = /* @__PURE__ */ __name(async (ui, server) => {
      const current = exposureOf(server.entry);
      const choice = await ui.menu(() => ({
        title: `Exposure of ${server.entry.name}`,
        details: server.entry.scope === "extension" ? `Applies to this session; the server is registered by ${server.entry.source}.` : `Saved to ${server.entry.override ?? server.entry.source}.`,
        items: Object.keys(EXPOSURE_DESCRIPTIONS).map((exposure) => ({
          value: exposure,
          label: `${exposure === current ? "\u2713 " : "  "}${exposure}`,
          description: EXPOSURE_DESCRIPTIONS[exposure]
        })),
        selected: current,
        confirmLabel: "save",
        cancelLabel: "back"
      }));
      if (!choice || choice === current)
        return void 0;
      return setExposure(server, choice);
    }, "chooseExposure");
    const signInWithUi = /* @__PURE__ */ __name((ui, server) => {
      const title = `Sign in to ${server.entry.name}`;
      const cancel = new AbortController();
      const status = /* @__PURE__ */ __name((message) => ui.status(title, message, () => cancel.abort()), "status");
      let authorizationUrl = "";
      status("Contacting the authorization server\u2026");
      return signIn(server, {
        showAuthorizationUrl: /* @__PURE__ */ __name((url) => {
          authorizationUrl = url.href;
          openUrl(url.href);
        }, "showAuthorizationUrl"),
        promptForRedirectUrl: /* @__PURE__ */ __name(async (signal) => {
          const value = await ui.redirectUrl(title, authorizationUrl, signal);
          status("Connecting\u2026");
          return value;
        }, "promptForRedirectUrl")
      }, cancel.signal);
    }, "signInWithUi");
    const runInBackground = /* @__PURE__ */ __name((ctx, server, operation) => {
      const ready = operation();
      const attempt = server.attempt;
      const { signal } = session;
      const finish = /* @__PURE__ */ __name((message) => {
        if (signal.aborted || server.attempt !== attempt || !servers.includes(server))
          return;
        server.message = message;
        ensureDiscoveryActive(ctx);
        emitChange();
      }, "finish");
      void track(ready.then(finish, (error) => finish(errorMessage(error))));
    }, "runInBackground");
    const runAction = /* @__PURE__ */ __name(async (ui, ctx, server, action) => {
      const { signal } = session;
      let message;
      switch (action) {
        case "signin":
          message = await signInWithUi(ui, server);
          break;
        case "reconnect":
          runInBackground(ctx, server, async () => {
            await reconnect(server);
            return void 0;
          });
          break;
        case "signout":
          await signOut(server);
          break;
        case "tools":
          await showTools(ui, server);
          break;
        case "exposure":
          message = await chooseExposure(ui, server);
          break;
        case "enable":
        case "disable":
        case "enable-project":
        case "disable-project": {
          const enable = action.startsWith("enable");
          runInBackground(ctx, server, () => setEnabled(server, enable, action.endsWith("-project")));
          break;
        }
      }
      if (signal.aborted)
        return;
      server.message = message;
      ensureDiscoveryActive(ctx);
      emitChange();
    }, "runAction");
    const manage = /* @__PURE__ */ __name(async (ui, ctx) => {
      for (; ; ) {
        const name = await ui.menu(serversMenu, subscribe);
        if (!name)
          return;
        for (; ; ) {
          const action = await ui.menu(() => serverMenu(name), subscribe);
          const server = findServer(name);
          if (!action || !server)
            break;
          await runAction(ui, ctx, server, action);
        }
      }
    }, "manage");
    const formatStatus = /* @__PURE__ */ __name(() => {
      if (servers.length === 0 && configErrors.length === 0 && overridden.length === 0) {
        return `No MCP servers configured. Add them to ${resolve(getAgentDir(), "mcp.json")} or .pi/mcp.json.`;
      }
      const lines = servers.map((server) => {
        const { name } = server.entry;
        const exposure = exposureOf(server.entry);
        const connection = server.connection;
        if (connection?.state === "needs-auth")
          return `${name}: needs sign-in, run /mcp login ${name} (${exposure})`;
        const tools = connection?.state === "connected" ? `, ${connection.tools.length} tools` : "";
        const state = !isEnabled(server) ? "disabled" : connection?.state === "disconnected" ? "disconnected, reconnects on next call" : connection?.state ?? "starting";
        const error = connection?.error && connection.state !== "connected" ? `
    ${connection.error.split("\n").join("\n    ")}` : "";
        return `${name}: ${state}${tools} (${exposure})${error}`;
      });
      for (const error of configErrors)
        lines.push(`config error: ${error}`);
      for (const line of overridden)
        lines.push(`overridden: ${line}`);
      return lines.join("\n");
    }, "formatStatus");
    const pickServer = /* @__PURE__ */ __name(async (name, ctx, options2) => {
      if (name) {
        const server = findServer(name);
        if (!server)
          ctx.ui.notify(`No MCP server named "${name}".`, "error");
        else if (!options2.eligible(server))
          ctx.ui.notify(options2.none, "error");
        return server && options2.eligible(server) ? server : void 0;
      }
      const candidates = servers.filter(options2.eligible);
      if (candidates.length === 0) {
        ctx.ui.notify(options2.none, "info");
        return void 0;
      }
      const preferred = candidates.filter(options2.preferred);
      if (candidates.length === 1)
        return candidates[0];
      if (preferred.length === 1)
        return preferred[0];
      const choice = await ctx.ui.select("MCP server", candidates.map((server) => server.entry.name));
      return candidates.find((server) => server.entry.name === choice);
    }, "pickServer");
    const usesOAuth = /* @__PURE__ */ __name((server) => server.connection?.oauthUrl !== void 0, "usesOAuth");
    const oauthPick = {
      eligible: usesOAuth,
      preferred: /* @__PURE__ */ __name((server) => server.connection?.state === "needs-auth", "preferred"),
      none: "No enabled MCP server uses OAuth. Only HTTP servers without an Authorization header do."
    };
    const loginCommand = /* @__PURE__ */ __name(async (server, ctx) => {
      const { name } = server.entry;
      if (!ctx.hasUI) {
        ctx.ui.notify(`Signing in to MCP server "${name}" requires interactive mode.`, "error");
        return;
      }
      const { signal } = session;
      let failure;
      if (ctx.mode === "tui") {
        await showMcpManager(ctx, async (ui) => {
          failure = await signInWithUi(ui, server);
        });
      } else {
        failure = await signIn(server, {
          showAuthorizationUrl: /* @__PURE__ */ __name((url) => {
            ctx.ui.notify(`Sign in to MCP server "${name}" in your browser:
${url.href}`, "info");
            openUrl(url.href);
          }, "showAuthorizationUrl"),
          promptForRedirectUrl: /* @__PURE__ */ __name((signal2) => ctx.ui.input(`Waiting for sign-in to "${name}". If the browser cannot reach this machine, paste the URL it was redirected to.`, "http://127.0.0.1:.../callback?code=...", { signal: signal2 }), "promptForRedirectUrl")
        });
      }
      if (signal.aborted)
        return;
      if (failure) {
        ctx.ui.notify(failure, failure === "Sign-in cancelled." ? "info" : "error");
        return;
      }
      ensureDiscoveryActive(ctx);
      ctx.ui.notify(`Signed in to MCP server "${name}" (${server.connection?.tools.length ?? 0} tools).`, "info");
    }, "loginCommand");
    pi.on("session_start", (_event, ctx) => {
      const loaded = (options.loadConfig ?? defaultLoadConfig)(ctx);
      configErrors = loaded.errors;
      projectConfig = loaded.projectConfig;
      autoEnableCodemode = loaded.autoEnableCodemode ?? true;
      warnedUnreachable = false;
      waitedForStartup = false;
      sessionCwd = ctx.cwd;
      modelRegistry = ctx.modelRegistry;
      session = new AbortController();
      const { signal } = session;
      configuredEntries = loaded.servers;
      const registered = registeredServers();
      overridden = registered.overridden;
      servers = [...loaded.servers.map((entry) => ({ entry })), ...registered.servers];
      emitChange();
      ensureDiscoveryActive(ctx);
      const enabled = servers.filter(isEnabled);
      if (enabled.length === 0) {
        reportProblems(ctx);
        return;
      }
      const runtime = new Promise((resolve7) => setImmediate(resolve7)).then(() => loadMcpRuntime());
      pending = Promise.all(enabled.map((server) => startConnection(server, runtime))).then(() => {
        if (!signal.aborted)
          reportProblems(ctx);
      }).catch((error) => {
        try {
          ctx.ui.notify(`MCP failed to load: ${errorMessage(error)}`, "error");
        } catch {
        }
      });
    });
    const waitForDirectServers = /* @__PURE__ */ __name(async (ctx) => {
      if (waitedForStartup)
        return;
      waitedForStartup = true;
      const ready = servers.flatMap((server) => isEnabled(server) && hasDirectTools(server.entry) && server.ready ? [server.ready] : []);
      if (ready.length === 0)
        return;
      let timer;
      const finished = await Promise.race([
        Promise.all(ready).then(() => true),
        new Promise((resolve7) => {
          timer = setTimeout(() => resolve7(false), startupWaitMs);
        })
      ]);
      clearTimeout(timer);
      if (!finished) {
        ctx.ui.notify("MCP servers are still connecting; their tools become available once connected.", "info");
      }
    }, "waitForDirectServers");
    pi.on("before_agent_start", async (event, ctx) => {
      await waitForDirectServers(ctx);
      const { sections } = event.systemPromptOptions;
      const section = renderServersSection(servers);
      if (section)
        sections[MCP_SERVERS_SECTION] = section;
      else
        delete sections[MCP_SERVERS_SECTION];
    });
    pi.on("tool_call", async (event, ctx) => {
      const tool = pi.getAllTools().find((candidate) => candidate.name === event.toolName);
      if (!tool)
        return;
      const readyServers = servers.filter((server) => isEnabled(server) && server.ready);
      if (readyServers.length === 0)
        return;
      let waiting = [];
      if (isCodemodeTool(tool)) {
        const { code } = event.input;
        const source = typeof code === "string" ? code : "";
        waiting = readyServers.filter((server) => scriptNeedsServer(source, server.entry.name));
      } else if (isToolSearchTool(tool) || RESOURCE_TOOL_NAMES.has(tool.name)) {
        waiting = readyServers;
      } else {
        const owner = toolOwners.get(event.toolName)?.split("\0", 1)[0];
        waiting = readyServers.filter((server) => server.entry.name === owner);
      }
      await waitForServers(waiting, ctx.signal);
    });
    pi.on("turn_start", async (_event, ctx) => {
      if (tokensAtSignIn.size > 0)
        await reconnectSignedIn(ctx);
    });
    pi.on("mcp_servers_change", async (_event, ctx) => {
      const { signal } = session;
      if (signal.aborted)
        return;
      const registered = registeredServers();
      overridden = registered.overridden;
      const next = new Map(registered.servers.map((server) => [server.entry.name, server]));
      const removed = servers.filter((server) => server.entry.scope === "extension" && next.get(server.entry.name)?.registeredConfig !== server.registeredConfig);
      servers = servers.filter((server) => !removed.includes(server));
      for (const server of removed)
        hideTools(server.entry.name);
      const added = registered.servers.filter((server) => !findServer(server.entry.name));
      servers.push(...added);
      emitChange();
      ensureDiscoveryActive(ctx);
      await Promise.all(removed.map((server) => server.connection?.close()));
      const connecting = added.filter(isEnabled);
      if (signal.aborted || connecting.length === 0)
        return;
      try {
        await Promise.all(connecting.map((server) => startConnection(server)));
        if (signal.aborted) {
          await Promise.all(connecting.map((server) => server.connection?.close()));
          return;
        }
      } catch (error) {
        ctx.ui.notify(`MCP failed to load: ${errorMessage(error)}`, "error");
        return;
      }
      if (signal.aborted)
        return;
      reportProblems(ctx, connecting);
    });
    pi.on("session_shutdown", async () => {
      session.abort();
      const closing = connections();
      servers = [];
      emitChange();
      await Promise.all([...backgroundActions, ...closing.map((connection) => connection.close())]);
    });
    pi.registerCommand("mcp", {
      description: "Manage MCP servers: sign in, reconnect, enable or disable, and change exposure",
      getArgumentCompletions: /* @__PURE__ */ __name((prefix) => {
        const [action, server, ...rest] = prefix.trimStart().split(/\s+/);
        if (rest.length > 0)
          return null;
        if (server === void 0) {
          return ["login", "logout", "reconnect"].filter((item) => item.startsWith(action ?? "")).map((item) => ({ value: `${item} `, label: item }));
        }
        if (action !== "login" && action !== "logout" && action !== "reconnect")
          return null;
        const items = servers.filter((candidate) => action === "reconnect" ? candidate.connection !== void 0 : usesOAuth(candidate)).filter((candidate) => candidate.entry.name.startsWith(server)).map((candidate) => ({
          value: `${action} ${candidate.entry.name}`,
          label: candidate.entry.name,
          description: describeState(candidate)
        }));
        return items.length > 0 ? items : null;
      }, "getArgumentCompletions"),
      handler: /* @__PURE__ */ __name(async (args, ctx) => {
        const [action, name, ...extra] = args.trim().split(/\s+/).filter(Boolean);
        if (action === void 0) {
          if (ctx.mode === "tui")
            await showMcpManager(ctx, (ui) => manage(ui, ctx));
          else {
            await pending;
            ctx.ui.notify(formatStatus(), "info");
          }
          return;
        }
        if (extra.length > 0) {
          ctx.ui.notify(MCP_USAGE, "warning");
          return;
        }
        await pending;
        switch (action) {
          case "login": {
            const server = await pickServer(name, ctx, oauthPick);
            if (server)
              await loginCommand(server, ctx);
            return;
          }
          case "logout": {
            const server = await pickServer(name, ctx, oauthPick);
            if (!server)
              return;
            const removed = await signOut(server);
            ctx.ui.notify(removed ? `Signed out of MCP server "${server.entry.name}".` : `No stored credentials for MCP server "${server.entry.name}".`, "info");
            return;
          }
          case "reconnect": {
            const server = await pickServer(name, ctx, {
              eligible: /* @__PURE__ */ __name((candidate) => candidate.connection !== void 0, "eligible"),
              preferred: /* @__PURE__ */ __name((candidate) => candidate.connection?.state === "failed" || candidate.connection?.state === "disconnected", "preferred"),
              none: "No enabled MCP server to reconnect."
            });
            if (!server)
              return;
            const failure = await reconnect(server);
            if (failure)
              ctx.ui.notify(failure, "error");
            else {
              ensureDiscoveryActive(ctx);
              ctx.ui.notify(`Reconnected to MCP server "${server.entry.name}" (${describeState(server)}).`, "info");
            }
            return;
          }
          default:
            ctx.ui.notify(MCP_USAGE, "warning");
        }
      }, "handler")
    });
  };
}
__name(createMcpExtension, "createMcpExtension");
function defaultLoadConfig(ctx) {
  return loadMcpConfig({ agentDir: getAgentDir(), cwd: ctx.cwd, projectTrusted: ctx.isProjectTrusted() });
}
__name(defaultLoadConfig, "defaultLoadConfig");
var mcp_default = createMcpExtension();

// pi-dist/pi-coding-agent/extensions/tool-search/index.js
function createToolSearchExtension() {
  return (pi) => {
    pi.registerTool({ ...createToolSearchToolDefinition({ tools: pi }), defaultActive: false });
  };
}
__name(createToolSearchExtension, "createToolSearchExtension");
var tool_search_default = createToolSearchExtension();

// pi-dist/pi-coding-agent/main.js
import { createInterface } from "node:readline";
import { modelsAreEqual as modelsAreEqual2 } from "../../pi-ai/sdk-bundle/index.js";
import { setCapabilityOverrides as setCapabilityOverrides3 } from "../../../pi-tui.mjs";
import chalk7 from "../../../chalk/source/index.js";

// pi-dist/pi-coding-agent/cli/auth-command.js
var AuthCommandError = class extends Error {
  static {
    __name(this, "AuthCommandError");
  }
};
var AUTH_COMMAND_USAGE = {
  check: `${APP_NAME} auth check --provider <provider> [--json] [--credentials] [--no-refresh]`,
  api_key: `${APP_NAME} auth print-api-key --provider <provider> [--model <model>]`,
  bearer_token: `${APP_NAME} auth print-bearer-token --provider <provider> [--model <model>] [--min-expiry <duration>]`
};
function getAuthCommandName(kind) {
  return kind === "check" ? "auth check" : kind === "api_key" ? "auth print-api-key" : "auth print-bearer-token";
}
__name(getAuthCommandName, "getAuthCommandName");
function getAuthCommandUsage(kind) {
  return AUTH_COMMAND_USAGE[kind];
}
__name(getAuthCommandUsage, "getAuthCommandUsage");
function isAuthCommandHelp(args) {
  return args[0] === "auth" && (args[1] === void 0 || args[1] === "help" || args.includes("--help") || args.includes("-h"));
}
__name(isAuthCommandHelp, "isAuthCommandHelp");
function printAuthCommandHelp() {
  console.log(`Usage:
  pi auth print-api-key [--provider <provider>] [--model <model>]
  pi auth print-bearer-token [--provider <provider>] [--model <model>] [--min-expiry <duration>]
  pi auth check [--provider <provider>] [--model <model>] [--json] [--credentials] [--no-refresh]

Auth commands require at least one of --provider or --model. Checks refresh expired OAuth credentials by default; --no-refresh prevents this. --credentials emits the credential, or includes it in JSON output.`);
}
__name(printAuthCommandHelp, "printAuthCommandHelp");
function parseAuthCommand(args) {
  if (args[0] !== "auth")
    return void 0;
  const kind = args[1] === "check" ? "check" : args[1] === "print-api-key" ? "api_key" : args[1] === "print-bearer-token" ? "bearer_token" : void 0;
  if (!kind) {
    throw new AuthCommandError(`Unknown auth command "${args[1] ?? ""}". Use "${APP_NAME} auth print-api-key", "${APP_NAME} auth print-bearer-token", or "${APP_NAME} auth check".`);
  }
  const commandArgs = [];
  let json2 = false;
  let credentials = false;
  let noRefresh = false;
  let minExpiryMs;
  for (let index = 2; index < args.length; index++) {
    const arg = args[index];
    if (arg === "--min-expiry") {
      if (kind !== "bearer_token")
        throw new AuthCommandError("--min-expiry is only supported by print-bearer-token");
      const value = args[++index];
      const match = value ? /^(\d+)(ms|s|m|h)$/iu.exec(value) : void 0;
      if (!match)
        throw new AuthCommandError("--min-expiry must use a duration such as 30m or 1h");
      const amount = Number(match[1]);
      const unit = match[2];
      minExpiryMs = amount * (unit === "ms" ? 1 : unit === "s" ? 1e3 : unit === "m" ? 6e4 : 36e5);
      continue;
    }
    if (arg === "--json" || arg === "--credentials" || arg === "--no-refresh") {
      if (kind !== "check")
        throw new AuthCommandError(`${arg} is only supported by auth check`);
      if (arg === "--json")
        json2 = true;
      else if (arg === "--credentials")
        credentials = true;
      else
        noRefresh = true;
      continue;
    }
    commandArgs.push(arg);
  }
  return minExpiryMs === void 0 ? { kind, args: commandArgs, json: json2, credentials, noRefresh } : { kind, args: commandArgs, json: json2, credentials, noRefresh, minExpiryMs };
}
__name(parseAuthCommand, "parseAuthCommand");
function validateAuthCommandArgs(args, kind) {
  const provider = args.provider?.trim() || void 0;
  const model = args.model?.trim() || void 0;
  if (args.unknownFlags.size > 0) {
    const option = args.unknownFlags.keys().next().value;
    throw new AuthCommandError(`Unknown option --${option} for "${getAuthCommandName(kind)}".`);
  }
  if (args.apiKey !== void 0 || args.messages.length > 0 || args.fileArgs.length > 0) {
    throw new AuthCommandError("Auth commands only accept --provider and --model");
  }
  if (kind === "check") {
    if (!provider && !model) {
      throw new AuthCommandError("Auth checks require --provider <provider> or --model <model>");
    }
    return { provider, model };
  }
  if (!provider && !model) {
    throw new AuthCommandError("Credential printing requires --provider <provider> or --model <model>");
  }
  return { provider, model };
}
__name(validateAuthCommandArgs, "validateAuthCommandArgs");
function getAuthCredential(auth) {
  if (auth?.auth.apiKey)
    return auth.auth.apiKey;
  const authorization = Object.entries(auth?.auth.headers ?? {}).find(([name]) => name.toLowerCase() === "authorization")?.[1];
  return typeof authorization === "string" ? /^Bearer\s+(.+)$/iu.exec(authorization)?.[1] : void 0;
}
__name(getAuthCredential, "getAuthCredential");

// pi-dist/pi-coding-agent/cli/auth-check.js
async function checkProviderAuth(args, modelRuntime, options = { refresh: false }) {
  const { provider: cliProvider, model: cliModel } = validateAuthCommandArgs(args, "check");
  let provider = cliProvider;
  if (cliModel) {
    const resolved = resolveCliModel({ cliProvider, cliModel, modelRuntime });
    if (resolved.error || !resolved.model) {
      throw new AuthCommandError(resolved.error ?? `Unable to resolve model "${cliModel}"`);
    }
    provider = resolved.model.provider;
  }
  if (!provider)
    throw new AuthCommandError("Unable to resolve an auth provider");
  if (modelRuntime.getError()) {
    return { status: "invalid", provider, reason: "invalid_state" };
  }
  if (!modelRuntime.getProvider(provider)) {
    return { status: "not_ready", provider, reason: "provider_not_found" };
  }
  try {
    const auth = await modelRuntime.checkAuth(provider);
    if (!auth)
      return { status: "not_ready", provider, reason: "credentials_not_configured" };
    if (options.refresh && !await modelRuntime.getAuth(provider)) {
      return { status: "not_ready", provider, reason: "credentials_not_configured" };
    }
    return { status: "ready", provider, authType: auth.type };
  } catch {
    return { status: "invalid", provider, reason: "invalid_state" };
  }
}
__name(checkProviderAuth, "checkProviderAuth");
async function getProviderCredential(providerId, modelRuntime, credentials, options) {
  const credential = await credentials.read(providerId);
  if (!options.refresh && credential?.type === "oauth")
    return credential.access;
  return getAuthCredential(await modelRuntime.getAuth(providerId));
}
__name(getProviderCredential, "getProviderCredential");
async function createAuthCheckModelRuntime(credentials) {
  return ModelRuntime.create({
    credentials,
    modelsStore: new InMemoryCodingAgentModelsStore(),
    allowModelNetwork: false,
    refreshOnCreate: false
  });
}
__name(createAuthCheckModelRuntime, "createAuthCheckModelRuntime");

// pi-dist/pi-coding-agent/cli/credential-print.js
var DEFAULT_BEARER_TOKEN_MIN_EXPIRY_MS = 30 * 6e4;
async function resolveCredentialForPrint(args, modelRuntime, kind, minExpiryMs, signal) {
  const { provider: cliProvider, model: cliModel } = validateAuthCommandArgs(args, kind);
  const credentialTypes = new Map((await modelRuntime.listCredentials({ signal })).map((credential) => [credential.providerId, credential.type]));
  const providers = [];
  if (cliProvider) {
    const provider = modelRuntime.getProvider(cliProvider);
    if (!provider) {
      throw new AuthCommandError(`Unknown provider "${cliProvider}". Use --list-models to see available providers.`);
    }
    if (cliModel) {
      const resolved = resolveCliModel({ cliProvider: provider.id, cliModel, modelRuntime });
      if (resolved.error || !resolved.model) {
        throw new AuthCommandError(resolved.error ?? "Unable to resolve the requested provider/model");
      }
      providers.push({ id: provider.id, model: resolved.model });
    } else {
      providers.push({ id: provider.id });
    }
  } else {
    for (const provider of modelRuntime.getProviders()) {
      if (!credentialTypes.has(provider.id))
        continue;
      const resolved = resolveCliModel({ cliProvider: provider.id, cliModel, modelRuntime });
      if (resolved.model && !resolved.error && !resolved.warning?.includes("Using custom model id")) {
        providers.push({ id: provider.id, model: resolved.model });
      }
    }
    if (providers.length === 0) {
      throw new AuthCommandError(`Model "${cliModel}" not found. Use --list-models to see available models.`);
    }
  }
  const credentials = [];
  for (const provider of providers) {
    const type = credentialTypes.get(provider.id);
    if (kind === "api_key" && type === "oauth")
      continue;
    if (kind === "bearer_token" && type !== "oauth")
      continue;
    const authOptions = {
      ...kind === "bearer_token" ? { minOAuthValidityMs: minExpiryMs ?? DEFAULT_BEARER_TOKEN_MIN_EXPIRY_MS } : {},
      signal
    };
    const auth = provider.model ? await modelRuntime.getAuth(provider.model, authOptions) : await modelRuntime.getAuth(provider.id, authOptions);
    const value = getAuthCredential(auth);
    if (value)
      credentials.push({ providerId: provider.id, value });
  }
  if (credentials.length === 1)
    return credentials[0].value;
  if (credentials.length === 0) {
    const providerId = providers[0]?.id;
    const type = providerId ? credentialTypes.get(providerId) : void 0;
    if (cliProvider && kind === "api_key" && type === "oauth") {
      throw new AuthCommandError(`Provider "${providerId}" is configured with OAuth, not an API key`);
    }
    if (cliProvider && kind === "bearer_token" && type !== "oauth") {
      throw new AuthCommandError(`Provider "${providerId}" is not configured with an OAuth bearer token`);
    }
    throw new AuthCommandError(`No usable ${kind === "api_key" ? "API key" : "OAuth bearer token"} is configured`);
  }
  throw new AuthCommandError(`Multiple configured providers matched (${credentials.map(({ providerId }) => providerId).join(", ")}). Specify --provider.`);
}
__name(resolveCredentialForPrint, "resolveCredentialForPrint");

// pi-dist/pi-coding-agent/cli/file-processor.js
import { access, readFile, stat } from "node:fs/promises";
import chalk from "../../../chalk/source/index.js";
import { resolve as resolve2 } from "path";
async function processFileArguments(fileArgs, options) {
  const autoResizeImages = options?.autoResizeImages ?? true;
  let text = "";
  const images = [];
  for (const fileArg of fileArgs) {
    const absolutePath = resolve2(resolveReadPath(fileArg, process.cwd()));
    try {
      await access(absolutePath);
    } catch {
      console.error(chalk.red(`Error: File not found: ${absolutePath}`));
      process.exit(1);
    }
    const stats = await stat(absolutePath);
    if (stats.size === 0) {
      continue;
    }
    const mimeType = await detectSupportedImageMimeTypeFromFile(absolutePath);
    if (mimeType) {
      const content = await readFile(absolutePath);
      const processed = await processImage(content, mimeType, { autoResizeImages });
      if (!processed.ok) {
        text += `<file name="${absolutePath}">${processed.message}</file>
`;
        continue;
      }
      const attachment = {
        type: "image",
        mimeType: processed.mimeType,
        data: processed.data
      };
      images.push(attachment);
      if (processed.hints.length > 0) {
        text += `<file name="${absolutePath}">${processed.hints.join("\n")}</file>
`;
      } else {
        text += `<file name="${absolutePath}"></file>
`;
      }
    } else {
      try {
        const content = stripBom(await readFile(absolutePath, "utf-8"));
        text += `<file name="${absolutePath}">
${content}
</file>
`;
      } catch (error) {
        const message = error instanceof Error ? error.message : String(error);
        console.error(chalk.red(`Error: Could not read file ${absolutePath}: ${message}`));
        process.exit(1);
      }
    }
  }
  return { text, images };
}
__name(processFileArguments, "processFileArguments");

// pi-dist/pi-coding-agent/cli/initial-message.js
function buildInitialMessage({ parsed, fileText, fileImages, stdinContent }) {
  const parts = [];
  if (stdinContent !== void 0) {
    parts.push(stdinContent);
  }
  if (fileText) {
    parts.push(fileText);
  }
  if (parsed.messages.length > 0) {
    parts.push(parsed.messages[0]);
    parsed.messages.shift();
  }
  return {
    initialMessage: parts.length > 0 ? parts.join("") : void 0,
    initialImages: fileImages && fileImages.length > 0 ? fileImages : void 0
  };
}
__name(buildInitialMessage, "buildInitialMessage");

// pi-dist/pi-coding-agent/cli/list-models.js
import { fuzzyFilter } from "../../../pi-tui.mjs";
import chalk2 from "../../../chalk/source/index.js";
function formatTokenCount(count) {
  if (count >= 1e6) {
    const millions = count / 1e6;
    return millions % 1 === 0 ? `${millions}M` : `${millions.toFixed(1)}M`;
  }
  if (count >= 1e3) {
    const thousands = count / 1e3;
    return thousands % 1 === 0 ? `${thousands}K` : `${thousands.toFixed(1)}K`;
  }
  return count.toString();
}
__name(formatTokenCount, "formatTokenCount");
async function listModels(modelRuntime, searchPattern, signal) {
  const loadError = modelRuntime.getError();
  if (loadError) {
    console.error(chalk2.yellow(`Warning: errors loading models.json:
${loadError}`));
  }
  const models = [...await modelRuntime.getAvailable(void 0, { signal })];
  if (models.length === 0) {
    console.log(formatNoModelsAvailableMessage());
    return;
  }
  let filteredModels = models;
  if (searchPattern) {
    filteredModels = fuzzyFilter(models, searchPattern, (m) => `${m.provider} ${m.id}`);
  }
  if (filteredModels.length === 0) {
    console.log(`No models matching "${searchPattern}"`);
    return;
  }
  filteredModels.sort((a, b) => {
    const providerCmp = a.provider.localeCompare(b.provider);
    if (providerCmp !== 0)
      return providerCmp;
    return a.id.localeCompare(b.id);
  });
  const rows = filteredModels.map((m) => ({
    provider: m.provider,
    model: m.id,
    context: formatTokenCount(m.contextWindow),
    maxOut: formatTokenCount(m.maxTokens),
    thinking: m.reasoning ? "yes" : "no",
    images: m.input.includes("image") ? "yes" : "no"
  }));
  const headers = {
    provider: "provider",
    model: "model",
    context: "context",
    maxOut: "max-out",
    thinking: "thinking",
    images: "images"
  };
  const widths = {
    provider: Math.max(headers.provider.length, ...rows.map((r) => r.provider.length)),
    model: Math.max(headers.model.length, ...rows.map((r) => r.model.length)),
    context: Math.max(headers.context.length, ...rows.map((r) => r.context.length)),
    maxOut: Math.max(headers.maxOut.length, ...rows.map((r) => r.maxOut.length)),
    thinking: Math.max(headers.thinking.length, ...rows.map((r) => r.thinking.length)),
    images: Math.max(headers.images.length, ...rows.map((r) => r.images.length))
  };
  const headerLine = [
    headers.provider.padEnd(widths.provider),
    headers.model.padEnd(widths.model),
    headers.context.padEnd(widths.context),
    headers.maxOut.padEnd(widths.maxOut),
    headers.thinking.padEnd(widths.thinking),
    headers.images.padEnd(widths.images)
  ].join("  ");
  console.log(headerLine);
  for (const row of rows) {
    const line = [
      row.provider.padEnd(widths.provider),
      row.model.padEnd(widths.model),
      row.context.padEnd(widths.context),
      row.maxOut.padEnd(widths.maxOut),
      row.thinking.padEnd(widths.thinking),
      row.images.padEnd(widths.images)
    ].join("  ");
    console.log(line);
  }
}
__name(listModels, "listModels");

// pi-dist/pi-coding-agent/cli/project-trust.js
import chalk3 from "../../../chalk/source/index.js";

// pi-dist/pi-coding-agent/cli/startup-ui.js
import { ProcessTerminal, setCapabilityOverrides, setKeybindings, TuiMainScreen } from "../../../pi-tui.mjs";
import { existsSync as existsSync2 } from "fs";

// pi-dist/pi-coding-agent/core/experimental.js
function areExperimentalFeaturesEnabled() {
  return process.env.PI_EXPERIMENTAL === "1";
}
__name(areExperimentalFeaturesEnabled, "areExperimentalFeaturesEnabled");

// pi-dist/pi-coding-agent/core/keybindings.js
import { TUI_KEYBINDINGS, KeybindingsManager as TuiKeybindingsManager } from "../../../pi-tui.mjs";
import { existsSync, readFileSync as readFileSync2 } from "fs";
import { join as join3 } from "path";
function useWindowsKeybindings(platform2 = process.platform, env = process.env) {
  return platform2 === "win32" || platform2 === "linux" && Boolean(env.WSL_DISTRO_NAME || env.WSL_INTEROP);
}
__name(useWindowsKeybindings, "useWindowsKeybindings");
var windowsKeybindings = useWindowsKeybindings();
var KEYBINDINGS = {
  ...TUI_KEYBINDINGS,
  "tui.editor.undo": {
    ...TUI_KEYBINDINGS["tui.editor.undo"],
    defaultKeys: process.platform === "win32" ? "ctrl+z" : windowsKeybindings ? "alt+z" : "ctrl+-"
  },
  "tui.altScreen.previousPrompt": {
    ...TUI_KEYBINDINGS["tui.altScreen.previousPrompt"],
    defaultKeys: windowsKeybindings ? "ctrl+up" : ["ctrl+shift+up", "ctrl+up"]
  },
  "tui.altScreen.nextPrompt": {
    ...TUI_KEYBINDINGS["tui.altScreen.nextPrompt"],
    defaultKeys: windowsKeybindings ? "ctrl+down" : ["ctrl+shift+down", "ctrl+down"]
  },
  "tui.altScreen.search": {
    ...TUI_KEYBINDINGS["tui.altScreen.search"],
    defaultKeys: windowsKeybindings ? "ctrl+f" : "ctrl+shift+f"
  },
  "app.interrupt": { defaultKeys: "escape", description: "Cancel or abort" },
  "app.clear": { defaultKeys: "ctrl+c", description: "Clear editor" },
  "app.exit": { defaultKeys: "ctrl+d", description: "Exit when editor is empty" },
  "app.suspend": {
    defaultKeys: process.platform === "win32" ? [] : "ctrl+z",
    description: "Suspend to background"
  },
  "app.thinking.cycle": {
    defaultKeys: "shift+tab",
    description: "Cycle thinking level"
  },
  "app.thinking.save": {
    defaultKeys: "ctrl+s",
    description: "Save thinking level"
  },
  "app.model.cycleForward": {
    defaultKeys: "ctrl+p",
    description: "Cycle to next model"
  },
  "app.model.cycleBackward": {
    defaultKeys: windowsKeybindings ? "alt+p" : "shift+ctrl+p",
    description: "Cycle to previous model"
  },
  "app.model.select": { defaultKeys: "ctrl+l", description: "Open model selector" },
  "app.tools.expand": { defaultKeys: "ctrl+o", description: "Toggle tool output" },
  "app.thinking.toggle": {
    defaultKeys: "ctrl+t",
    description: "Toggle thinking blocks"
  },
  "app.session.toggleNamedFilter": {
    defaultKeys: "ctrl+n",
    description: "Toggle named session filter"
  },
  "app.editor.external": {
    defaultKeys: "ctrl+g",
    description: "Open external editor"
  },
  "app.message.copy": {
    defaultKeys: "ctrl+x",
    description: "Copy selection or last assistant message"
  },
  "app.message.followUp": {
    defaultKeys: windowsKeybindings ? "ctrl+q" : "alt+enter",
    description: "Queue follow-up message"
  },
  "app.message.dequeue": {
    defaultKeys: windowsKeybindings ? "alt+q" : "alt+up",
    description: "Restore queued messages"
  },
  "app.clipboard.pasteImage": {
    defaultKeys: windowsKeybindings ? "alt+v" : "ctrl+v",
    description: "Paste files on macOS, images, or text from clipboard"
  },
  "app.session.new": { defaultKeys: [], description: "Start a new session" },
  "app.session.tree": { defaultKeys: [], description: "Open session tree" },
  "app.session.fork": { defaultKeys: [], description: "Fork current session" },
  "app.session.resume": { defaultKeys: [], description: "Resume a session" },
  "app.tree.foldOrUp": {
    defaultKeys: process.platform === "darwin" ? ["alt+left", "ctrl+left"] : ["ctrl+left", "alt+left"],
    description: "Fold tree branch or move up"
  },
  "app.tree.unfoldOrDown": {
    defaultKeys: process.platform === "darwin" ? ["alt+right", "ctrl+right"] : ["ctrl+right", "alt+right"],
    description: "Unfold tree branch or move down"
  },
  "app.tree.editLabel": {
    defaultKeys: "shift+l",
    description: "Edit tree label"
  },
  "app.tree.toggleLabelTimestamp": {
    defaultKeys: "shift+t",
    description: "Toggle tree label timestamps"
  },
  "app.session.togglePath": {
    defaultKeys: "ctrl+p",
    description: "Toggle session path display"
  },
  "app.session.toggleSort": {
    defaultKeys: "ctrl+s",
    description: "Toggle session sort mode"
  },
  "app.session.rename": {
    defaultKeys: "ctrl+r",
    description: "Rename session"
  },
  "app.session.delete": {
    defaultKeys: "ctrl+d",
    description: "Delete session"
  },
  "app.session.deleteNoninvasive": {
    defaultKeys: "ctrl+backspace",
    description: "Delete session when query is empty"
  },
  "app.models.save": {
    defaultKeys: "ctrl+s",
    description: "Save model selection"
  },
  "app.models.enableAll": {
    defaultKeys: "ctrl+a",
    description: "Enable all models"
  },
  "app.models.clearAll": {
    defaultKeys: "ctrl+x",
    description: "Clear all models"
  },
  "app.models.toggleProvider": {
    defaultKeys: "ctrl+p",
    description: "Toggle all models for provider"
  },
  "app.models.reorderUp": {
    defaultKeys: "alt+up",
    description: "Move model up in order"
  },
  "app.models.reorderDown": {
    defaultKeys: "alt+down",
    description: "Move model down in order"
  },
  "app.tree.filter.default": {
    defaultKeys: "ctrl+d",
    description: "Tree filter: default view"
  },
  "app.tree.filter.noTools": {
    defaultKeys: "ctrl+t",
    description: "Tree filter: hide tool results"
  },
  "app.tree.filter.userOnly": {
    defaultKeys: "ctrl+u",
    description: "Tree filter: user messages only"
  },
  "app.tree.filter.labeledOnly": {
    defaultKeys: "ctrl+l",
    description: "Tree filter: labeled entries only"
  },
  "app.tree.filter.all": {
    defaultKeys: "ctrl+a",
    description: "Tree filter: show all entries"
  },
  "app.tree.filter.cycleForward": {
    defaultKeys: "ctrl+o",
    description: "Tree filter: cycle forward"
  },
  "app.tree.filter.cycleBackward": {
    defaultKeys: "shift+ctrl+o",
    description: "Tree filter: cycle backward"
  }
};
var KEYBINDING_NAME_MIGRATIONS = {
  cursorUp: "tui.editor.cursorUp",
  cursorDown: "tui.editor.cursorDown",
  cursorLeft: "tui.editor.cursorLeft",
  cursorRight: "tui.editor.cursorRight",
  cursorWordLeft: "tui.editor.cursorWordLeft",
  cursorWordRight: "tui.editor.cursorWordRight",
  cursorLineStart: "tui.editor.cursorLineStart",
  cursorLineEnd: "tui.editor.cursorLineEnd",
  jumpForward: "tui.editor.jumpForward",
  jumpBackward: "tui.editor.jumpBackward",
  pageUp: "tui.editor.pageUp",
  pageDown: "tui.editor.pageDown",
  deleteCharBackward: "tui.editor.deleteCharBackward",
  deleteCharForward: "tui.editor.deleteCharForward",
  deleteWordBackward: "tui.editor.deleteWordBackward",
  deleteWordForward: "tui.editor.deleteWordForward",
  deleteToLineStart: "tui.editor.deleteToLineStart",
  deleteToLineEnd: "tui.editor.deleteToLineEnd",
  yank: "tui.editor.yank",
  yankPop: "tui.editor.yankPop",
  undo: "tui.editor.undo",
  newLine: "tui.input.newLine",
  submit: "tui.input.submit",
  tab: "tui.input.tab",
  copy: "tui.input.copy",
  selectUp: "tui.select.up",
  selectDown: "tui.select.down",
  selectPageUp: "tui.select.pageUp",
  selectPageDown: "tui.select.pageDown",
  selectConfirm: "tui.select.confirm",
  selectCancel: "tui.select.cancel",
  interrupt: "app.interrupt",
  clear: "app.clear",
  exit: "app.exit",
  suspend: "app.suspend",
  cycleThinkingLevel: "app.thinking.cycle",
  cycleModelForward: "app.model.cycleForward",
  cycleModelBackward: "app.model.cycleBackward",
  selectModel: "app.model.select",
  expandTools: "app.tools.expand",
  toggleThinking: "app.thinking.toggle",
  toggleSessionNamedFilter: "app.session.toggleNamedFilter",
  externalEditor: "app.editor.external",
  followUp: "app.message.followUp",
  dequeue: "app.message.dequeue",
  pasteImage: "app.clipboard.pasteImage",
  newSession: "app.session.new",
  tree: "app.session.tree",
  fork: "app.session.fork",
  resume: "app.session.resume",
  treeFoldOrUp: "app.tree.foldOrUp",
  treeUnfoldOrDown: "app.tree.unfoldOrDown",
  treeEditLabel: "app.tree.editLabel",
  treeToggleLabelTimestamp: "app.tree.toggleLabelTimestamp",
  toggleSessionPath: "app.session.togglePath",
  toggleSessionSort: "app.session.toggleSort",
  renameSession: "app.session.rename",
  deleteSession: "app.session.delete",
  deleteSessionNoninvasive: "app.session.deleteNoninvasive"
};
function isLegacyKeybindingName(key) {
  return key in KEYBINDING_NAME_MIGRATIONS;
}
__name(isLegacyKeybindingName, "isLegacyKeybindingName");
function toKeybindingsConfig(value) {
  const config = {};
  for (const [key, binding] of Object.entries(value)) {
    if (typeof binding === "string") {
      config[key] = binding;
      continue;
    }
    if (Array.isArray(binding) && binding.every((entry) => typeof entry === "string")) {
      config[key] = binding;
    }
  }
  return config;
}
__name(toKeybindingsConfig, "toKeybindingsConfig");
function migrateKeybindingsConfig(rawConfig) {
  const config = {};
  let migrated = false;
  for (const [key, value] of Object.entries(rawConfig)) {
    const nextKey = isLegacyKeybindingName(key) ? KEYBINDING_NAME_MIGRATIONS[key] : key;
    if (nextKey !== key) {
      migrated = true;
    }
    if (key !== nextKey && Object.hasOwn(rawConfig, nextKey)) {
      migrated = true;
      continue;
    }
    config[nextKey] = value;
  }
  return { config: orderKeybindingsConfig(config), migrated };
}
__name(migrateKeybindingsConfig, "migrateKeybindingsConfig");
function orderKeybindingsConfig(config) {
  const ordered = {};
  for (const keybinding of Object.keys(KEYBINDINGS)) {
    if (Object.hasOwn(config, keybinding)) {
      ordered[keybinding] = config[keybinding];
    }
  }
  const extras = Object.keys(config).filter((key) => !Object.hasOwn(ordered, key)).sort();
  for (const key of extras) {
    ordered[key] = config[key];
  }
  return ordered;
}
__name(orderKeybindingsConfig, "orderKeybindingsConfig");
function loadRawConfig(path5) {
  if (!existsSync(path5))
    return void 0;
  try {
    const parsed = JSON.parse(stripBom(readFileSync2(path5, "utf-8")));
    if (typeof parsed !== "object" || parsed === null)
      return void 0;
    return parsed;
  } catch {
    return void 0;
  }
}
__name(loadRawConfig, "loadRawConfig");
var KeybindingsManager = class _KeybindingsManager extends TuiKeybindingsManager {
  static {
    __name(this, "KeybindingsManager");
  }
  configPath;
  constructor(userBindings = {}, configPath) {
    super(KEYBINDINGS, userBindings);
    this.configPath = configPath;
  }
  static create(agentDir = getAgentDir()) {
    const configPath = join3(agentDir, "keybindings.json");
    const userBindings = _KeybindingsManager.loadFromFile(configPath);
    return new _KeybindingsManager(userBindings, configPath);
  }
  reload() {
    if (!this.configPath)
      return;
    this.setUserBindings(_KeybindingsManager.loadFromFile(this.configPath));
  }
  getEffectiveConfig() {
    return this.getResolvedBindings();
  }
  static loadFromFile(path5) {
    const rawConfig = loadRawConfig(path5);
    if (!rawConfig)
      return {};
    return toKeybindingsConfig(migrateKeybindingsConfig(rawConfig).config);
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/extension-input.js
import { Container as Container3, getKeybindings, Input as Input2, Spacer as Spacer2, Text as Text3 } from "../../../pi-tui.mjs";

// pi-dist/pi-coding-agent/modes/interactive/components/countdown-timer.js
var CountdownTimer = class {
  static {
    __name(this, "CountdownTimer");
  }
  intervalId;
  remainingSeconds;
  tui;
  onTick;
  onExpire;
  constructor(timeoutMs, tui, onTick, onExpire) {
    this.tui = tui;
    this.onTick = onTick;
    this.onExpire = onExpire;
    this.remainingSeconds = Math.ceil(timeoutMs / 1e3);
    this.onTick(this.remainingSeconds);
    this.intervalId = setInterval(() => {
      this.remainingSeconds--;
      this.onTick(this.remainingSeconds);
      this.tui?.requestRender();
      if (this.remainingSeconds <= 0) {
        this.dispose();
        this.onExpire();
      }
    }, 1e3);
  }
  dispose() {
    if (this.intervalId) {
      clearInterval(this.intervalId);
      this.intervalId = void 0;
    }
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/extension-input.js
var ExtensionInputComponent = class extends Container3 {
  static {
    __name(this, "ExtensionInputComponent");
  }
  input;
  onSubmitCallback;
  onCancelCallback;
  titleText;
  baseTitle;
  countdown;
  // Focusable implementation - propagate to input for IME cursor positioning
  _focused = false;
  get focused() {
    return this._focused;
  }
  set focused(value) {
    this._focused = value;
    this.input.focused = value;
  }
  constructor(title, _placeholder, onSubmit, onCancel, opts) {
    super();
    this.onSubmitCallback = onSubmit;
    this.onCancelCallback = onCancel;
    this.baseTitle = title;
    this.addChild(new DynamicBorder());
    this.addChild(new Spacer2(1));
    this.titleText = new Text3(theme.fg("accent", title), 1, 0);
    this.addChild(this.titleText);
    if (opts?.description) {
      this.addChild(new Spacer2(1));
      this.addChild(new Text3(theme.fg("text", opts.description), 1, 0));
    }
    this.addChild(new Spacer2(1));
    if (opts?.timeout && opts.timeout > 0 && opts.tui) {
      this.countdown = new CountdownTimer(opts.timeout, opts.tui, (s) => this.titleText.setText(theme.fg("accent", `${this.baseTitle} (${s}s)`)), () => this.onCancelCallback());
    }
    this.input = new Input2();
    if (opts?.initialValue)
      this.input.setValue(opts.initialValue);
    this.addChild(this.input);
    this.addChild(new Spacer2(1));
    this.addChild(new Text3(`${keyHint("tui.select.confirm", "submit")}  ${keyHint("tui.select.cancel", "cancel")}`, 1, 0));
    this.addChild(new Spacer2(1));
    this.addChild(new DynamicBorder());
  }
  handleInput(keyData) {
    const kb = getKeybindings();
    if (kb.matches(keyData, "tui.select.confirm") || keyData === "\n") {
      this.onSubmitCallback(this.input.getValue());
    } else if (kb.matches(keyData, "tui.select.cancel")) {
      this.onCancelCallback();
    } else {
      this.input.handleInput(keyData);
    }
  }
  dispose() {
    this.countdown?.dispose();
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/extension-selector.js
import { Container as Container4, getKeybindings as getKeybindings2, Spacer as Spacer3, Text as Text4 } from "../../../pi-tui.mjs";
var ExtensionSelectorComponent = class extends Container4 {
  static {
    __name(this, "ExtensionSelectorComponent");
  }
  options;
  selectedIndex = 0;
  listContainer;
  onSelectCallback;
  onCancelCallback;
  titleText;
  baseTitle;
  countdown;
  onToggleToolsExpanded;
  constructor(title, options, onSelect, onCancel, opts) {
    super();
    this.options = options;
    this.onSelectCallback = onSelect;
    this.onCancelCallback = onCancel;
    this.onToggleToolsExpanded = opts?.onToggleToolsExpanded;
    this.baseTitle = title;
    this.addChild(new DynamicBorder());
    this.addChild(new Spacer3(1));
    this.titleText = new Text4(theme.fg("accent", theme.bold(title)), 1, 0);
    this.addChild(this.titleText);
    if (opts?.description) {
      this.addChild(new Spacer3(1));
      this.addChild(new Text4(theme.fg("text", opts.description), 1, 0));
    }
    this.addChild(new Spacer3(1));
    if (opts?.timeout && opts.timeout > 0 && opts.tui) {
      this.countdown = new CountdownTimer(opts.timeout, opts.tui, (s) => this.titleText.setText(theme.fg("accent", theme.bold(`${this.baseTitle} (${s}s)`))), () => this.onCancelCallback());
    }
    this.listContainer = new Container4();
    this.addChild(this.listContainer);
    this.addChild(new Spacer3(1));
    this.addChild(new Text4(rawKeyHint("\u2191\u2193", "navigate") + "  " + keyHint("tui.select.confirm", "select") + "  " + keyHint("tui.select.cancel", "cancel"), 1, 0));
    this.addChild(new Spacer3(1));
    this.addChild(new DynamicBorder());
    this.updateList();
  }
  updateList() {
    this.listContainer.clear();
    for (let i = 0; i < this.options.length; i++) {
      const isSelected = i === this.selectedIndex;
      const text = isSelected ? theme.fg("accent", "\u2192 ") + theme.fg("accent", this.options[i]) : `  ${theme.fg("text", this.options[i])}`;
      this.listContainer.addChild(new Text4(text, 1, 0));
    }
  }
  handleInput(keyData) {
    const kb = getKeybindings2();
    if (kb.matches(keyData, "app.tools.expand")) {
      this.onToggleToolsExpanded?.();
    } else if (kb.matches(keyData, "tui.select.up") || keyData === "k") {
      this.selectedIndex = Math.max(0, this.selectedIndex - 1);
      this.updateList();
    } else if (kb.matches(keyData, "tui.select.down") || keyData === "j") {
      this.selectedIndex = Math.min(this.options.length - 1, this.selectedIndex + 1);
      this.updateList();
    } else if (kb.matches(keyData, "tui.select.confirm") || keyData === "\n") {
      const selected = this.options[this.selectedIndex];
      if (selected)
        this.onSelectCallback(selected);
    } else if (kb.matches(keyData, "tui.select.cancel")) {
      this.onCancelCallback();
    }
  }
  dispose() {
    this.countdown?.dispose();
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/first-time-setup.js
import { Container as Container5, getKeybindings as getKeybindings3, Spacer as Spacer4, Text as Text5 } from "../../../pi-tui.mjs";
var THEME_OPTIONS = [
  { value: SYSTEM_THEME_NAME, label: "System (matches your terminal colors)" },
  { value: "dark", label: "Dark" },
  { value: "light", label: "Light" }
];
var ANALYTICS_OPTIONS = [
  { value: true, label: "Share anonymous usage data" },
  { value: false, label: "Don't share" }
];
var SETUP_LOGO_LINES = ["\u2588\u2588\u2588\u2588\u2588\u2588", "\u2588\u2588  \u2588\u2588", "\u2588\u2588\u2588\u2588  \u2588\u2588", "\u2588\u2588    \u2588\u2588"];
var FirstTimeSetupComponent = class extends Container5 {
  static {
    __name(this, "FirstTimeSetupComponent");
  }
  step = "theme";
  themeIndex;
  analyticsIndex = 0;
  options;
  constructor(options) {
    super();
    this.options = options;
    this.themeIndex = 0;
    this.update();
  }
  /** Rebuild on theme changes, e.g. when the system theme receives the terminal's colors. */
  invalidate() {
    this.update();
    super.invalidate();
  }
  // Rebuild the whole dialog on every change so theme previews recolor all text.
  update() {
    this.clear();
    this.addChild(new DynamicBorder());
    this.addChild(new Spacer4(1));
    this.addChild(new Text5(theme.fg("accent", SETUP_LOGO_LINES.join("\n")), 1, 0));
    this.addChild(new Spacer4(1));
    this.addChild(new Text5(theme.fg("accent", theme.bold(`Welcome to ${APP_NAME}, the minimal coding agent.`)), 1, 0));
    this.addChild(new Spacer4(1));
    if (this.step === "theme") {
      this.addChild(new Text5(theme.fg("text", "Pick a theme."), 1, 0));
      this.addChild(new Spacer4(1));
      this.addOptionList(THEME_OPTIONS.map((option) => option.label), this.themeIndex);
    } else {
      this.addChild(new Text5(theme.fg("text", "Opt-in to anonymous usage data sharing?"), 1, 0));
      this.addChild(new Text5(theme.fg("muted", "Opting in stores a tracking identifier in settings.json and enables anonymous\nusage analytics. This helps us to better debug, reproduce, and resolve issues\nand bugs within Pi. You can observe what is shared using /privacy and make\nchanges anytime in settings.json."), 1, 0));
      this.addChild(new Spacer4(1));
      this.addOptionList(ANALYTICS_OPTIONS.map((option) => option.label), this.analyticsIndex);
    }
    this.addChild(new Spacer4(1));
    this.addChild(new Text5(rawKeyHint("\u2191\u2193", "navigate") + "  " + keyHint("tui.select.confirm", this.step === "theme" ? "continue" : "finish") + "  " + keyHint("tui.select.cancel", "skip setup"), 1, 0));
    this.addChild(new Spacer4(1));
    this.addChild(new DynamicBorder());
  }
  addOptionList(labels, selectedIndex) {
    for (let i = 0; i < labels.length; i++) {
      const isSelected = i === selectedIndex;
      const prefix = isSelected ? theme.fg("accent", "\u2192 ") : "  ";
      const label = isSelected ? theme.fg("accent", labels[i]) : theme.fg("text", labels[i]);
      this.addChild(new Text5(`${prefix}${label}`, 1, 0));
    }
  }
  moveSelection(delta) {
    if (this.step === "theme") {
      const next = Math.max(0, Math.min(THEME_OPTIONS.length - 1, this.themeIndex + delta));
      if (next !== this.themeIndex) {
        this.themeIndex = next;
        this.options.onThemePreview(THEME_OPTIONS[this.themeIndex].value);
      }
    } else {
      this.analyticsIndex = Math.max(0, Math.min(ANALYTICS_OPTIONS.length - 1, this.analyticsIndex + delta));
    }
    this.update();
  }
  handleInput(keyData) {
    const kb = getKeybindings3();
    if (kb.matches(keyData, "tui.select.up") || keyData === "k") {
      this.moveSelection(-1);
    } else if (kb.matches(keyData, "tui.select.down") || keyData === "j") {
      this.moveSelection(1);
    } else if (kb.matches(keyData, "tui.select.confirm") || keyData === "\n") {
      if (this.step === "theme") {
        this.step = "analytics";
        this.update();
      } else {
        this.options.onSubmit({
          theme: THEME_OPTIONS[this.themeIndex].value,
          shareAnalytics: ANALYTICS_OPTIONS[this.analyticsIndex].value
        });
      }
    } else if (kb.matches(keyData, "tui.select.cancel")) {
      this.options.onCancel();
    }
  }
};

// pi-dist/pi-coding-agent/modes/interactive/theme/theme-controller.js
var TERMINAL_QUERY_TIMEOUT_MS = 100;
function requestTerminalColors(ui, apply) {
  let query;
  try {
    query = ui.queryTerminalColors({ timeoutMs: TERMINAL_QUERY_TIMEOUT_MS, onLateReply: apply });
  } catch {
    query = Promise.resolve({});
  }
  return query.then(apply, () => apply({}));
}
__name(requestTerminalColors, "requestTerminalColors");
function sameRgb(a, b) {
  return a === b || a !== void 0 && b !== void 0 && a.r === b.r && a.g === b.g && a.b === b.b;
}
__name(sameRgb, "sameRgb");
function sameTerminalColors(a, b) {
  if (!sameRgb(a.foreground, b.foreground) || !sameRgb(a.background, b.background))
    return false;
  if (a.palette === b.palette)
    return true;
  if (!a.palette || !b.palette || a.palette.length !== b.palette.length)
    return false;
  return a.palette.every((color, index) => sameRgb(color, b.palette?.[index]));
}
__name(sameTerminalColors, "sameTerminalColors");
var InteractiveThemeController = class {
  static {
    __name(this, "InteractiveThemeController");
  }
  ui;
  getSettingsManager;
  showError;
  onChanged;
  currentThemeSetting;
  // Last reported colors; a query that times out keeps them instead of erasing them.
  terminalColors;
  activeThemeName;
  autoSyncEnabled = false;
  terminalColorSchemeUnsubscribe;
  // Settles when the latest color query completed or timed out, and its colors applied.
  terminalColorQuery = Promise.resolve();
  constructor(ui, options) {
    this.ui = ui;
    this.getSettingsManager = options.getSettingsManager;
    this.showError = options.showError;
    this.onChanged = options.onChanged;
    this.currentThemeSetting = options.initialThemeSetting;
    this.activeThemeName = this.resolveThemeName();
    markTerminalColorsPending();
    initTheme(this.activeThemeName, true);
    this.bindTerminalColorSchemeListener();
  }
  rebindTui() {
    this.terminalColorSchemeUnsubscribe?.();
    this.bindTerminalColorSchemeListener();
    this.ui.setTerminalColorSchemeNotifications(this.autoSyncEnabled);
  }
  /**
   * Apply the theme setting now and query the terminal's colors, which update the theme when they arrive.
   * Theme pairs and the system theme follow terminal appearance changes.
   */
  applyFromSettings() {
    const themeSetting = this.getThemeSetting();
    const themeName = this.resolveThemeName();
    this.setAutoSync(parseAutoThemeSetting(themeSetting) !== void 0 || themeName === SYSTEM_THEME_NAME);
    this.applyThemeName(themeName, themeSetting !== void 0);
    this.queryTerminalColors();
  }
  /**
   * Wait until the latest color query completed or timed out. Content that bakes theme colors into
   * strings, such as the startup header, should be built after this. Terminals answer the DA1 request
   * right after the color replies, so this only takes the full timeout when a terminal answers nothing.
   */
  waitForTerminalColors() {
    return this.terminalColorQuery;
  }
  getThemeSelection() {
    return this.currentThemeSetting ?? this.getSettingsManager().getThemeSetting() ?? this.activeThemeName;
  }
  setThemeName(themeName, showError = false) {
    this.setAutoSync(themeName === SYSTEM_THEME_NAME);
    const result = this.applyThemeName(themeName, showError);
    if (result.success) {
      this.currentThemeSetting = themeName;
    }
    return result;
  }
  setThemeSetting(themeSetting) {
    this.currentThemeSetting = themeSetting;
    this.applyFromSettings();
  }
  setThemeInstance(themeInstance) {
    this.setAutoSync(false);
    setThemeInstance(themeInstance);
    this.activeThemeName = "<in-memory>";
    this.notifyChanged();
    return { success: true };
  }
  preview(themeSettingOrName) {
    const themeName = resolveThemeSetting(themeSettingOrName, getTerminalTheme()) ?? this.activeThemeName;
    if (!themeName)
      return;
    if (setTheme(themeName, true).success) {
      this.ui.invalidate();
      this.ui.requestRender();
    }
  }
  disableAutoSync() {
    this.setAutoSync(false);
  }
  dispose() {
    this.setAutoSync(false);
    this.terminalColorSchemeUnsubscribe?.();
    this.terminalColorSchemeUnsubscribe = void 0;
  }
  getTerminalTheme() {
    return getTerminalTheme();
  }
  getThemeSetting() {
    return this.currentThemeSetting ?? this.getSettingsManager().getThemeSetting();
  }
  /** The theme for the current setting and terminal appearance. Without a setting, pi uses the system theme. */
  resolveThemeName() {
    return resolveThemeSetting(this.getThemeSetting(), getTerminalTheme()) ?? SYSTEM_THEME_NAME;
  }
  applyThemeName(themeName, showError = false) {
    const result = setTheme(themeName, true);
    this.activeThemeName = result.success ? themeName : SYSTEM_THEME_NAME;
    this.notifyChanged();
    if (!result.success && showError) {
      this.showError(`Failed to load theme "${themeName}": ${result.error}
Fell back to the system theme.`);
    }
    return result;
  }
  /** Query the terminal's colors without waiting for them; `waitForTerminalColors()` waits for this query. */
  queryTerminalColors() {
    this.terminalColorQuery = requestTerminalColors(this.ui, (colors) => this.applyTerminalColors(colors));
  }
  /**
   * Record reported colors: themes use the default colors for tokens set to "", the system theme is
   * generated from all of them, and light/dark detection uses them. Re-renders only when they changed.
   */
  applyTerminalColors(reported) {
    const previous = this.terminalColors;
    const next = {
      foreground: reported.foreground ?? previous?.foreground,
      background: reported.background ?? previous?.background,
      palette: reported.palette ?? previous?.palette
    };
    if (previous && sameTerminalColors(previous, next))
      return;
    this.terminalColors = next;
    setTerminalColors(next);
    this.reapplyForTerminal();
    this.ui.invalidate();
    this.ui.requestRender();
  }
  /**
   * Re-apply the setting after the terminal's colors or appearance changed: regenerate the system theme,
   * or switch the theme of a pair. Themes set through extensions or previews are left alone.
   */
  reapplyForTerminal() {
    if (this.activeThemeName === "<in-memory>")
      return;
    const themeName = this.resolveThemeName();
    if (themeName === SYSTEM_THEME_NAME || themeName !== this.activeThemeName) {
      this.applyThemeName(themeName);
    }
  }
  setAutoSync(enabled) {
    if (this.autoSyncEnabled === enabled)
      return;
    this.autoSyncEnabled = enabled;
    this.ui.setTerminalColorSchemeNotifications(enabled);
  }
  bindTerminalColorSchemeListener() {
    this.terminalColorSchemeUnsubscribe = this.ui.onTerminalColorSchemeChange((terminalTheme) => this.applyTerminalColorSchemeChange(terminalTheme));
  }
  /**
   * The terminal reported a light/dark switch. Its colors changed too, so query them again: they decide
   * the appearance. The reported scheme only matters for terminals that do not report their background.
   */
  applyTerminalColorSchemeChange(terminalTheme) {
    if (!this.autoSyncEnabled)
      return;
    const previous = getTerminalTheme();
    setTerminalColorScheme(terminalTheme);
    if (getTerminalTheme() !== previous)
      this.reapplyForTerminal();
    this.queryTerminalColors();
  }
  notifyChanged() {
    this.ui.invalidate();
    this.onChanged();
  }
};

// pi-dist/pi-coding-agent/cli/startup-ui.js
var OFFICIAL_PACKAGE_NAME = "@earendil-works/pi-coding-agent";
var OFFICIAL_APP_NAME = "pi";
var OFFICIAL_CONFIG_DIR_NAME = ".pi";
function isOfficialDistribution({ packageName, appName, configDirName }) {
  return packageName === OFFICIAL_PACKAGE_NAME && appName === OFFICIAL_APP_NAME && configDirName === OFFICIAL_CONFIG_DIR_NAME;
}
__name(isOfficialDistribution, "isOfficialDistribution");
function loadThemes(resources) {
  const themes = [];
  const seen = /* @__PURE__ */ new Set();
  for (const resource of resources) {
    if (!resource.enabled)
      continue;
    try {
      const loadedTheme = loadThemeFromPath(resource.path);
      if (loadedTheme.name) {
        if (seen.has(loadedTheme.name))
          continue;
        seen.add(loadedTheme.name);
      }
      themes.push(loadedTheme);
    } catch {
    }
  }
  return themes;
}
__name(loadThemes, "loadThemes");
async function loadStartupThemes(settingsManager) {
  const globalSettingsManager = SettingsManager.inMemory(settingsManager.getGlobalSettings(), {
    projectTrusted: false
  });
  const packageManager = new DefaultPackageManager({
    cwd: process.cwd(),
    agentDir: getAgentDir(),
    settingsManager: globalSettingsManager
  });
  const resolvedPaths = await packageManager.resolve(async () => "skip");
  return loadThemes(resolvedPaths.themes);
}
__name(loadStartupThemes, "loadStartupThemes");
async function createStartupTui(settingsManager) {
  setCapabilityOverrides(settingsManager.getTerminalCapabilityOverrides());
  setRegisteredThemes(await loadStartupThemes(settingsManager));
  markTerminalColorsPending();
  initTheme(resolveThemeSetting(settingsManager.getThemeSetting(), getTerminalTheme()) ?? SYSTEM_THEME_NAME);
  setKeybindings(KeybindingsManager.create());
  const ui = new TuiMainScreen(new ProcessTerminal(), settingsManager.getShowHardwareCursor(), getAgentDir());
  ui.setClearOnShrink(settingsManager.getClearOnShrink());
  return ui;
}
__name(createStartupTui, "createStartupTui");
function startStartupTui(ui, settingsManager) {
  ui.start();
  const themeSetting = settingsManager.getThemeSetting();
  queryStartupTerminalColors(ui, () => {
    setTheme(resolveThemeSetting(themeSetting, getTerminalTheme()) ?? SYSTEM_THEME_NAME);
  });
}
__name(startStartupTui, "startStartupTui");
function queryStartupTerminalColors(ui, onColors) {
  void requestTerminalColors(ui, (colors) => {
    setTerminalColors(colors);
    onColors();
    ui.invalidate();
    ui.requestRender();
  });
}
__name(queryStartupTerminalColors, "queryStartupTerminalColors");
async function clearStartupTui(ui) {
  ui.clear();
  ui.requestRender();
  await new Promise((resolve7) => setTimeout(resolve7, 25));
}
__name(clearStartupTui, "clearStartupTui");
function shouldRunFirstTimeSetup(settingsPath = getSettingsPath()) {
  if (!isOfficialDistribution({
    packageName: PACKAGE_NAME,
    appName: APP_NAME,
    configDirName: CONFIG_DIR_NAME
  })) {
    return false;
  }
  if (!areExperimentalFeaturesEnabled()) {
    return false;
  }
  if (process.env[ENV_AGENT_DIR]) {
    return false;
  }
  return !existsSync2(settingsPath);
}
__name(shouldRunFirstTimeSetup, "shouldRunFirstTimeSetup");
async function showStartupSelector(settingsManager, title, options) {
  const ui = await createStartupTui(settingsManager);
  return new Promise((resolve7) => {
    let settled = false;
    const finish = /* @__PURE__ */ __name(async (result) => {
      if (settled) {
        return;
      }
      settled = true;
      await clearStartupTui(ui);
      ui.stop();
      resolve7(result);
    }, "finish");
    const selector = new ExtensionSelectorComponent(title, options.map((option) => option.label), (option) => void finish(options.find((entry) => entry.label === option)?.value), () => void finish(void 0), { tui: ui });
    ui.addChild(selector);
    ui.setFocus(selector);
    startStartupTui(ui, settingsManager);
  });
}
__name(showStartupSelector, "showStartupSelector");
async function showFirstTimeSetup(settingsManager) {
  const ui = await createStartupTui(settingsManager);
  return new Promise((resolve7) => {
    let settled = false;
    const finish = /* @__PURE__ */ __name(async (result) => {
      if (settled) {
        return;
      }
      settled = true;
      if (result) {
        settingsManager.setTheme(result.theme);
        settingsManager.setEnableAnalytics(result.shareAnalytics);
        await settingsManager.flush();
      }
      await clearStartupTui(ui);
      ui.stop();
      resolve7();
    }, "finish");
    ui.start();
    let previewTheme = SYSTEM_THEME_NAME;
    setTheme(previewTheme);
    const component = new FirstTimeSetupComponent({
      onThemePreview: /* @__PURE__ */ __name((themeName) => {
        previewTheme = themeName;
        setTheme(themeName);
        ui.requestRender();
      }, "onThemePreview"),
      onSubmit: /* @__PURE__ */ __name((result) => void finish(result), "onSubmit"),
      onCancel: /* @__PURE__ */ __name(() => void finish(void 0), "onCancel")
    });
    ui.addChild(component);
    ui.setFocus(component);
    ui.requestRender();
    queryStartupTerminalColors(ui, () => setTheme(previewTheme));
  });
}
__name(showFirstTimeSetup, "showFirstTimeSetup");
async function showStartupInput(settingsManager, title, placeholder) {
  const ui = await createStartupTui(settingsManager);
  return new Promise((resolve7) => {
    let settled = false;
    const finish = /* @__PURE__ */ __name(async (result) => {
      if (settled) {
        return;
      }
      settled = true;
      input2.dispose();
      await clearStartupTui(ui);
      ui.stop();
      resolve7(result);
    }, "finish");
    const input2 = new ExtensionInputComponent(title, placeholder, (value) => void finish(value), () => void finish(void 0), {
      tui: ui
    });
    ui.addChild(input2);
    ui.setFocus(input2);
    startStartupTui(ui, settingsManager);
  });
}
__name(showStartupInput, "showStartupInput");

// pi-dist/pi-coding-agent/cli/project-trust.js
function createProjectTrustContext(options) {
  return {
    cwd: options.cwd,
    mode: options.mode === "interactive" ? "tui" : options.mode,
    hasUI: options.hasUI,
    ui: {
      select: /* @__PURE__ */ __name(async (title, selectOptions) => {
        if (!options.hasUI) {
          return void 0;
        }
        if (options.mode !== "interactive") {
          return void 0;
        }
        return showStartupSelector(options.settingsManager, title, selectOptions.map((option) => ({ label: option, value: option })));
      }, "select"),
      confirm: /* @__PURE__ */ __name(async (title, message) => {
        if (!options.hasUI) {
          return false;
        }
        if (options.mode !== "interactive") {
          return false;
        }
        return await showStartupSelector(options.settingsManager, `${title}
${message}`, [
          { label: "Yes", value: true },
          { label: "No", value: false }
        ]) ?? false;
      }, "confirm"),
      input: /* @__PURE__ */ __name(async (title, placeholder) => {
        if (!options.hasUI) {
          return void 0;
        }
        if (options.mode !== "interactive") {
          return void 0;
        }
        return showStartupInput(options.settingsManager, title, placeholder);
      }, "input"),
      notify: /* @__PURE__ */ __name((message, type = "info") => {
        if (options.mode !== "interactive") {
          const color = type === "error" ? chalk3.red : type === "warning" ? chalk3.yellow : chalk3.cyan;
          console.error(color(message));
        }
      }, "notify")
    }
  };
}
__name(createProjectTrustContext, "createProjectTrustContext");

// pi-dist/pi-coding-agent/cli/session-picker.js
import { setKeybindings as setKeybindings2 } from "../../../pi-tui.mjs";

// pi-dist/pi-coding-agent/modes/interactive/components/session-selector.js
import { spawnSync } from "node:child_process";
import { existsSync as existsSync3 } from "node:fs";
import { unlink } from "node:fs/promises";
import * as os from "node:os";
import { Container as Container6, getKeybindings as getKeybindings4, Input as Input3, Spacer as Spacer5, Text as Text6, truncateToWidth as truncateToWidth2, visibleWidth as visibleWidth2 } from "../../../pi-tui.mjs";

// pi-dist/pi-coding-agent/modes/interactive/components/session-selector-search.js
import { fuzzyMatch } from "../../../pi-tui.mjs";
function normalizeWhitespaceLower(text) {
  return text.toLowerCase().replace(/\s+/g, " ").trim();
}
__name(normalizeWhitespaceLower, "normalizeWhitespaceLower");
function getSessionSearchText(session) {
  return `${session.id} ${session.name ?? ""} ${session.allMessagesText} ${session.cwd}`;
}
__name(getSessionSearchText, "getSessionSearchText");
function hasSessionName(session) {
  return Boolean(session.name?.trim());
}
__name(hasSessionName, "hasSessionName");
function matchesNameFilter(session, filter) {
  if (filter === "all")
    return true;
  return hasSessionName(session);
}
__name(matchesNameFilter, "matchesNameFilter");
function parseSearchQuery(query) {
  const trimmed = query.trim();
  if (!trimmed) {
    return { mode: "tokens", tokens: [], regex: null };
  }
  if (trimmed.startsWith("re:")) {
    const pattern = trimmed.slice(3).trim();
    if (!pattern) {
      return { mode: "regex", tokens: [], regex: null, error: "Empty regex" };
    }
    try {
      return { mode: "regex", tokens: [], regex: new RegExp(pattern, "i") };
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      return { mode: "regex", tokens: [], regex: null, error: msg };
    }
  }
  const tokens = [];
  let buf = "";
  let inQuote = false;
  let hadUnclosedQuote = false;
  const flush = /* @__PURE__ */ __name((kind) => {
    const v = buf.trim();
    buf = "";
    if (!v)
      return;
    tokens.push({ kind, value: v });
  }, "flush");
  for (let i = 0; i < trimmed.length; i++) {
    const ch = trimmed[i];
    if (ch === '"') {
      if (inQuote) {
        flush("phrase");
        inQuote = false;
      } else {
        flush("fuzzy");
        inQuote = true;
      }
      continue;
    }
    if (!inQuote && /\s/.test(ch)) {
      flush("fuzzy");
      continue;
    }
    buf += ch;
  }
  if (inQuote) {
    hadUnclosedQuote = true;
  }
  if (hadUnclosedQuote) {
    return {
      mode: "tokens",
      tokens: trimmed.split(/\s+/).map((t) => t.trim()).filter((t) => t.length > 0).map((t) => ({ kind: "fuzzy", value: t })),
      regex: null
    };
  }
  flush(inQuote ? "phrase" : "fuzzy");
  return { mode: "tokens", tokens, regex: null };
}
__name(parseSearchQuery, "parseSearchQuery");
function matchSession(session, parsed) {
  const text = getSessionSearchText(session);
  if (parsed.mode === "regex") {
    if (!parsed.regex) {
      return { matches: false, score: 0 };
    }
    const idx = text.search(parsed.regex);
    if (idx < 0)
      return { matches: false, score: 0 };
    return { matches: true, score: idx * 0.1 };
  }
  if (parsed.tokens.length === 0) {
    return { matches: true, score: 0 };
  }
  let totalScore = 0;
  let normalizedText = null;
  for (const token of parsed.tokens) {
    if (token.kind === "phrase") {
      if (normalizedText === null) {
        normalizedText = normalizeWhitespaceLower(text);
      }
      const phrase = normalizeWhitespaceLower(token.value);
      if (!phrase)
        continue;
      const idx = normalizedText.indexOf(phrase);
      if (idx < 0)
        return { matches: false, score: 0 };
      totalScore += idx * 0.1;
      continue;
    }
    const m = fuzzyMatch(token.value, text);
    if (!m.matches)
      return { matches: false, score: 0 };
    totalScore += m.score;
  }
  return { matches: true, score: totalScore };
}
__name(matchSession, "matchSession");
function filterAndSortSessions(sessions, query, sortMode, nameFilter = "all") {
  const nameFiltered = nameFilter === "all" ? sessions : sessions.filter((session) => matchesNameFilter(session, nameFilter));
  const trimmed = query.trim();
  if (!trimmed)
    return nameFiltered;
  const parsed = parseSearchQuery(query);
  if (parsed.error)
    return [];
  if (sortMode === "recent") {
    const filtered = [];
    for (const s of nameFiltered) {
      const res = matchSession(s, parsed);
      if (res.matches)
        filtered.push(s);
    }
    return filtered;
  }
  const scored = [];
  for (const s of nameFiltered) {
    const res = matchSession(s, parsed);
    if (!res.matches)
      continue;
    scored.push({ session: s, score: res.score });
  }
  scored.sort((a, b) => {
    if (a.score !== b.score)
      return a.score - b.score;
    return b.session.modified.getTime() - a.session.modified.getTime();
  });
  return scored.map((r) => r.session);
}
__name(filterAndSortSessions, "filterAndSortSessions");

// pi-dist/pi-coding-agent/modes/interactive/components/session-selector.js
function shortenPath(path5) {
  const home = os.homedir();
  if (!path5)
    return path5;
  if (path5.startsWith(home)) {
    return `~${path5.slice(home.length)}`;
  }
  return path5;
}
__name(shortenPath, "shortenPath");
function formatSessionDate(date) {
  const now = /* @__PURE__ */ new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMins = Math.floor(diffMs / 6e4);
  const diffHours = Math.floor(diffMs / 36e5);
  const diffDays = Math.floor(diffMs / 864e5);
  if (diffMins < 1)
    return "now";
  if (diffMins < 60)
    return `${diffMins}m`;
  if (diffHours < 24)
    return `${diffHours}h`;
  if (diffDays < 7)
    return `${diffDays}d`;
  if (diffDays < 30)
    return `${Math.floor(diffDays / 7)}w`;
  if (diffDays < 365)
    return `${Math.floor(diffDays / 30)}mo`;
  return `${Math.floor(diffDays / 365)}y`;
}
__name(formatSessionDate, "formatSessionDate");
function canonicalizePath2(path5) {
  if (!path5)
    return path5;
  return canonicalizePath(path5);
}
__name(canonicalizePath2, "canonicalizePath");
var SessionSelectorHeader = class {
  static {
    __name(this, "SessionSelectorHeader");
  }
  scope;
  sortMode;
  nameFilter;
  requestRender;
  loading = false;
  loadProgress = null;
  showPath = false;
  confirmingDeletePath = null;
  statusMessage = null;
  statusTimeout = null;
  showRenameHint = false;
  constructor(scope, sortMode, nameFilter, requestRender) {
    this.scope = scope;
    this.sortMode = sortMode;
    this.nameFilter = nameFilter;
    this.requestRender = requestRender;
  }
  setScope(scope) {
    this.scope = scope;
  }
  setSortMode(sortMode) {
    this.sortMode = sortMode;
  }
  setNameFilter(nameFilter) {
    this.nameFilter = nameFilter;
  }
  setLoading(loading) {
    this.loading = loading;
    this.loadProgress = null;
  }
  setProgress(loaded, total) {
    this.loadProgress = { loaded, total };
  }
  setShowPath(showPath) {
    this.showPath = showPath;
  }
  setShowRenameHint(show) {
    this.showRenameHint = show;
  }
  setConfirmingDeletePath(path5) {
    this.confirmingDeletePath = path5;
  }
  clearStatusTimeout() {
    if (!this.statusTimeout)
      return;
    clearTimeout(this.statusTimeout);
    this.statusTimeout = null;
  }
  setStatusMessage(msg, autoHideMs) {
    this.clearStatusTimeout();
    this.statusMessage = msg;
    if (!msg || !autoHideMs)
      return;
    this.statusTimeout = setTimeout(() => {
      this.statusMessage = null;
      this.statusTimeout = null;
      this.requestRender();
    }, autoHideMs);
  }
  invalidate() {
  }
  render(width) {
    const title = this.scope === "current" ? "Resume Session (Current Folder)" : "Resume Session (All)";
    const leftText = theme.bold(title);
    const sortLabel = this.sortMode === "threaded" ? "Threaded" : this.sortMode === "recent" ? "Recent" : "Fuzzy";
    const sortText = theme.fg("muted", "Sort: ") + theme.fg("accent", sortLabel);
    const nameLabel = this.nameFilter === "all" ? "All" : "Named";
    const nameText = theme.fg("muted", "Name: ") + theme.fg("accent", nameLabel);
    let scopeText;
    if (this.loading) {
      const progressText = this.loadProgress ? `${this.loadProgress.loaded}/${this.loadProgress.total}` : "...";
      scopeText = `${theme.fg("muted", "\u25CB Current Folder | ")}${theme.fg("accent", `Loading ${progressText}`)}`;
    } else if (this.scope === "current") {
      scopeText = `${theme.fg("accent", "\u25C9 Current Folder")}${theme.fg("muted", " | \u25CB All")}`;
    } else {
      scopeText = `${theme.fg("muted", "\u25CB Current Folder | ")}${theme.fg("accent", "\u25C9 All")}`;
    }
    const rightText = truncateToWidth2(`${scopeText}  ${nameText}  ${sortText}`, width, "");
    const availableLeft = Math.max(0, width - visibleWidth2(rightText) - 1);
    const left = truncateToWidth2(leftText, availableLeft, "");
    const spacing = Math.max(0, width - visibleWidth2(left) - visibleWidth2(rightText));
    let hintLine1;
    let hintLine2;
    if (this.confirmingDeletePath !== null) {
      const confirmHint = `Delete session? ${keyHint("tui.select.confirm", "confirm")} \xB7 ${keyHint("tui.select.cancel", "cancel")}`;
      hintLine1 = theme.fg("error", truncateToWidth2(confirmHint, width, "\u2026"));
      hintLine2 = "";
    } else if (this.statusMessage) {
      const color = this.statusMessage.type === "error" ? "error" : "accent";
      hintLine1 = theme.fg(color, truncateToWidth2(this.statusMessage.message, width, "\u2026"));
      hintLine2 = "";
    } else {
      const pathState = this.showPath ? "(on)" : "(off)";
      const sep3 = theme.fg("muted", " \xB7 ");
      const hint1 = keyHint("tui.input.tab", "scope") + sep3 + theme.fg("muted", 're:<pattern> regex \xB7 "phrase" exact');
      const hint2Parts = [
        keyHint("app.session.toggleSort", "sort"),
        keyHint("app.session.toggleNamedFilter", "named"),
        keyHint("app.session.delete", "delete"),
        keyHint("app.session.togglePath", `path ${pathState}`)
      ];
      if (this.showRenameHint) {
        hint2Parts.push(keyHint("app.session.rename", "rename"));
      }
      const hint2 = hint2Parts.join(sep3);
      hintLine1 = truncateToWidth2(hint1, width, "\u2026");
      hintLine2 = truncateToWidth2(hint2, width, "\u2026");
    }
    return [`${left}${" ".repeat(spacing)}${rightText}`, hintLine1, hintLine2];
  }
};
function buildSessionTree(sessions) {
  const byPath = /* @__PURE__ */ new Map();
  for (const session of sessions) {
    const sessionPath = canonicalizePath2(session.path) ?? session.path;
    byPath.set(sessionPath, { session, children: [], latestActivity: session.modified.getTime() });
  }
  const roots = [];
  for (const session of sessions) {
    const sessionPath = canonicalizePath2(session.path) ?? session.path;
    const node = byPath.get(sessionPath);
    const parentPath = canonicalizePath2(session.parentSessionPath);
    if (parentPath && byPath.has(parentPath)) {
      byPath.get(parentPath).children.push(node);
    } else {
      roots.push(node);
    }
  }
  const updateLatestActivity = /* @__PURE__ */ __name((node) => {
    let latestActivity = node.session.modified.getTime();
    for (const child of node.children) {
      latestActivity = Math.max(latestActivity, updateLatestActivity(child));
    }
    node.latestActivity = latestActivity;
    return latestActivity;
  }, "updateLatestActivity");
  for (const root of roots) {
    updateLatestActivity(root);
  }
  const sortNodes = /* @__PURE__ */ __name((nodes) => {
    nodes.sort((a, b) => b.latestActivity - a.latestActivity);
    for (const node of nodes) {
      sortNodes(node.children);
    }
  }, "sortNodes");
  sortNodes(roots);
  return roots;
}
__name(buildSessionTree, "buildSessionTree");
function flattenSessionTree(roots) {
  const result = [];
  const walk = /* @__PURE__ */ __name((node, depth, ancestorContinues, isLast) => {
    result.push({ session: node.session, depth, isLast, ancestorContinues });
    for (let i = 0; i < node.children.length; i++) {
      const childIsLast = i === node.children.length - 1;
      const continues = depth > 0 ? !isLast : false;
      walk(node.children[i], depth + 1, [...ancestorContinues, continues], childIsLast);
    }
  }, "walk");
  for (let i = 0; i < roots.length; i++) {
    walk(roots[i], 0, [], i === roots.length - 1);
  }
  return result;
}
__name(flattenSessionTree, "flattenSessionTree");
var SessionList = class {
  static {
    __name(this, "SessionList");
  }
  getSelectedSessionPath() {
    const selected = this.filteredSessions[this.selectedIndex];
    return selected?.session.path;
  }
  allSessions = [];
  filteredSessions = [];
  selectedIndex = 0;
  selectionTouched = false;
  searchInput;
  showCwd = false;
  sortMode = "threaded";
  nameFilter = "all";
  keybindings;
  showPath = false;
  confirmingDeletePath = null;
  currentSessionCanonicalPath;
  onSelect;
  onCancel;
  onExit = /* @__PURE__ */ __name(() => {
  }, "onExit");
  onToggleScope;
  onToggleSort;
  onToggleNameFilter;
  onTogglePath;
  onDeleteConfirmationChange;
  onDeleteSession;
  onRenameSession;
  onError;
  maxVisible = 10;
  // Max sessions visible (one line each)
  // Focusable implementation - propagate to searchInput for IME cursor positioning
  _focused = false;
  get focused() {
    return this._focused;
  }
  set focused(value) {
    this._focused = value;
    this.searchInput.focused = value;
  }
  constructor(sessions, showCwd, sortMode, nameFilter, keybindings, currentSessionFilePath) {
    this.allSessions = sessions;
    this.filteredSessions = [];
    this.searchInput = new Input3();
    this.showCwd = showCwd;
    this.sortMode = sortMode;
    this.nameFilter = nameFilter;
    this.keybindings = keybindings;
    this.currentSessionCanonicalPath = canonicalizePath2(currentSessionFilePath);
    this.filterSessions("");
    this.searchInput.onSubmit = () => {
      if (this.filteredSessions[this.selectedIndex]) {
        const selected = this.filteredSessions[this.selectedIndex];
        if (this.onSelect) {
          this.onSelect(selected.session.path);
        }
      }
    };
  }
  setSortMode(sortMode) {
    this.sortMode = sortMode;
    this.filterSessions(this.searchInput.getValue());
  }
  setNameFilter(nameFilter) {
    this.nameFilter = nameFilter;
    this.filterSessions(this.searchInput.getValue());
  }
  setSessions(sessions, showCwd) {
    const selectedPath = this.selectionTouched ? this.getSelectedSessionPath() : void 0;
    this.allSessions = sessions;
    this.showCwd = showCwd;
    this.filterSessions(this.searchInput.getValue());
    if (!this.selectionTouched) {
      this.selectedIndex = 0;
    } else if (selectedPath) {
      const selectedIndex = this.filteredSessions.findIndex((node) => node.session.path === selectedPath);
      if (selectedIndex >= 0)
        this.selectedIndex = selectedIndex;
    }
  }
  filterSessions(query) {
    const trimmed = query.trim();
    const nameFiltered = this.nameFilter === "all" ? this.allSessions : this.allSessions.filter((session) => hasSessionName(session));
    if (this.sortMode === "threaded" && !trimmed) {
      const roots = buildSessionTree(nameFiltered);
      this.filteredSessions = flattenSessionTree(roots);
    } else {
      const filtered = filterAndSortSessions(nameFiltered, query, this.sortMode, "all");
      this.filteredSessions = filtered.map((session) => ({
        session,
        depth: 0,
        isLast: true,
        ancestorContinues: []
      }));
    }
    this.selectedIndex = Math.min(this.selectedIndex, Math.max(0, this.filteredSessions.length - 1));
  }
  setConfirmingDeletePath(path5) {
    this.confirmingDeletePath = path5;
    this.onDeleteConfirmationChange?.(path5);
  }
  startDeleteConfirmationForSelectedSession() {
    const selected = this.filteredSessions[this.selectedIndex];
    if (!selected)
      return;
    if (this.isCurrentSessionPath(selected.session.path)) {
      this.onError?.("Cannot delete the currently active session");
      return;
    }
    this.setConfirmingDeletePath(selected.session.path);
  }
  isCurrentSessionPath(path5) {
    if (!this.currentSessionCanonicalPath)
      return false;
    return (canonicalizePath2(path5) ?? path5) === this.currentSessionCanonicalPath;
  }
  invalidate() {
  }
  render(width) {
    const lines = [];
    lines.push(...this.searchInput.render(width));
    lines.push("");
    if (this.filteredSessions.length === 0) {
      let emptyMessage;
      if (this.nameFilter === "named") {
        const toggleKey = keyText("app.session.toggleNamedFilter");
        if (this.showCwd) {
          emptyMessage = `  No named sessions found. Press ${toggleKey} to show all.`;
        } else {
          emptyMessage = `  No named sessions in current folder. Press ${toggleKey} to show all, or Tab to view all.`;
        }
      } else if (this.showCwd) {
        emptyMessage = "  No sessions found";
      } else {
        emptyMessage = "  No sessions in current folder. Press Tab to view all.";
      }
      lines.push(theme.fg("muted", truncateToWidth2(emptyMessage, width, "\u2026")));
      return lines;
    }
    const startIndex = Math.max(0, Math.min(this.selectedIndex - Math.floor(this.maxVisible / 2), this.filteredSessions.length - this.maxVisible));
    const endIndex = Math.min(startIndex + this.maxVisible, this.filteredSessions.length);
    for (let i = startIndex; i < endIndex; i++) {
      const node = this.filteredSessions[i];
      const session = node.session;
      const isSelected = i === this.selectedIndex;
      const isConfirmingDelete = session.path === this.confirmingDeletePath;
      const isCurrent = this.isCurrentSessionPath(session.path);
      const prefix = this.buildTreePrefix(node);
      const hasName = !!session.name;
      const displayText = session.name ?? session.firstMessage;
      const normalizedMessage = displayText.replace(/[\x00-\x1f\x7f]/g, " ").trim();
      const age = formatSessionDate(session.modified);
      const msgCount = String(session.messageCount);
      let rightPart = `${msgCount} ${age}`;
      if (this.showCwd && session.cwd) {
        rightPart = `${shortenPath(session.cwd)} ${rightPart}`;
      }
      if (this.showPath) {
        rightPart = `${shortenPath(session.path)} ${rightPart}`;
      }
      const cursor = isSelected ? theme.fg("accent", "\u203A ") : "  ";
      const prefixWidth = visibleWidth2(prefix);
      const rightWidth = visibleWidth2(rightPart) + 2;
      const availableForMsg = width - 2 - prefixWidth - rightWidth;
      const truncatedMsg = truncateToWidth2(normalizedMessage, Math.max(10, availableForMsg), "\u2026");
      let messageColor = null;
      if (isConfirmingDelete) {
        messageColor = "error";
      } else if (isCurrent) {
        messageColor = "accent";
      } else if (hasName) {
        messageColor = "warning";
      }
      let styledMsg = messageColor ? theme.fg(messageColor, truncatedMsg) : truncatedMsg;
      if (isSelected) {
        styledMsg = theme.bold(styledMsg);
      }
      const leftPart = cursor + theme.fg("dim", prefix) + styledMsg;
      const leftWidth = visibleWidth2(leftPart);
      const spacing = Math.max(1, width - leftWidth - visibleWidth2(rightPart));
      const styledRight = theme.fg(isConfirmingDelete ? "error" : "dim", rightPart);
      let line = leftPart + " ".repeat(spacing) + styledRight;
      if (isSelected) {
        line = theme.bg("selectedBg", line);
      }
      lines.push(truncateToWidth2(line, width));
    }
    if (startIndex > 0 || endIndex < this.filteredSessions.length) {
      const scrollText = `  (${this.selectedIndex + 1}/${this.filteredSessions.length})`;
      const scrollInfo = theme.fg("muted", truncateToWidth2(scrollText, width, ""));
      lines.push(scrollInfo);
    }
    return lines;
  }
  buildTreePrefix(node) {
    if (node.depth === 0) {
      return "";
    }
    const parts = node.ancestorContinues.map((continues) => continues ? "\u2502  " : "   ");
    const branch = node.isLast ? "\u2514\u2500 " : "\u251C\u2500 ";
    return parts.join("") + branch;
  }
  handleInput(keyData) {
    const kb = getKeybindings4();
    if (this.confirmingDeletePath !== null) {
      if (kb.matches(keyData, "tui.select.confirm")) {
        const pathToDelete = this.confirmingDeletePath;
        this.setConfirmingDeletePath(null);
        void this.onDeleteSession?.(pathToDelete);
        return;
      }
      if (kb.matches(keyData, "tui.select.cancel")) {
        this.setConfirmingDeletePath(null);
        return;
      }
      return;
    }
    if (kb.matches(keyData, "tui.input.tab")) {
      if (this.onToggleScope) {
        this.onToggleScope();
      }
      return;
    }
    if (kb.matches(keyData, "app.session.toggleSort")) {
      this.onToggleSort?.();
      return;
    }
    if (this.keybindings.matches(keyData, "app.session.toggleNamedFilter")) {
      this.onToggleNameFilter?.();
      return;
    }
    if (kb.matches(keyData, "app.session.togglePath")) {
      this.showPath = !this.showPath;
      this.onTogglePath?.(this.showPath);
      return;
    }
    if (kb.matches(keyData, "app.session.delete")) {
      this.startDeleteConfirmationForSelectedSession();
      return;
    }
    if (kb.matches(keyData, "app.session.rename")) {
      const selected = this.filteredSessions[this.selectedIndex];
      if (selected) {
        this.onRenameSession?.(selected.session.path);
      }
      return;
    }
    if (kb.matches(keyData, "app.session.deleteNoninvasive")) {
      if (this.searchInput.getValue().length > 0) {
        this.searchInput.handleInput(keyData);
        this.filterSessions(this.searchInput.getValue());
        return;
      }
      this.startDeleteConfirmationForSelectedSession();
      return;
    }
    this.selectionTouched = true;
    if (kb.matches(keyData, "tui.select.up")) {
      this.selectedIndex = Math.max(0, this.selectedIndex - 1);
    } else if (kb.matches(keyData, "tui.select.down")) {
      this.selectedIndex = Math.min(this.filteredSessions.length - 1, this.selectedIndex + 1);
    } else if (kb.matches(keyData, "tui.select.pageUp")) {
      this.selectedIndex = Math.max(0, this.selectedIndex - this.maxVisible);
    } else if (kb.matches(keyData, "tui.select.pageDown")) {
      this.selectedIndex = Math.min(this.filteredSessions.length - 1, this.selectedIndex + this.maxVisible);
    } else if (kb.matches(keyData, "tui.select.confirm")) {
      const selected = this.filteredSessions[this.selectedIndex];
      if (selected && this.onSelect) {
        this.onSelect(selected.session.path);
      }
    } else if (kb.matches(keyData, "tui.select.cancel")) {
      if (this.onCancel) {
        this.onCancel();
      }
    } else {
      this.searchInput.handleInput(keyData);
      this.filterSessions(this.searchInput.getValue());
    }
  }
};
async function deleteSessionFile(sessionPath) {
  const trashArgs = sessionPath.startsWith("-") ? ["--", sessionPath] : [sessionPath];
  const trashResult = spawnSync("trash", trashArgs, { encoding: "utf-8" });
  const getTrashErrorHint = /* @__PURE__ */ __name(() => {
    const parts = [];
    if (trashResult.error) {
      parts.push(trashResult.error.message);
    }
    const stderr = trashResult.stderr?.trim();
    if (stderr) {
      parts.push(stderr.split("\n")[0] ?? stderr);
    }
    if (parts.length === 0)
      return null;
    return `trash: ${parts.join(" \xB7 ").slice(0, 200)}`;
  }, "getTrashErrorHint");
  if (trashResult.status === 0 || !existsSync3(sessionPath)) {
    return { ok: true, method: "trash" };
  }
  try {
    await unlink(sessionPath);
    return { ok: true, method: "unlink" };
  } catch (err) {
    const unlinkError = err instanceof Error ? err.message : String(err);
    const trashErrorHint = getTrashErrorHint();
    const error = trashErrorHint ? `${unlinkError} (${trashErrorHint})` : unlinkError;
    return { ok: false, method: "unlink", error };
  }
}
__name(deleteSessionFile, "deleteSessionFile");
var SessionSelectorComponent = class extends Container6 {
  static {
    __name(this, "SessionSelectorComponent");
  }
  handleInput(data) {
    if (this.mode === "rename") {
      const kb = getKeybindings4();
      if (kb.matches(data, "tui.select.cancel")) {
        this.exitRenameMode();
        return;
      }
      this.renameInput.handleInput(data);
      return;
    }
    this.sessionList.handleInput(data);
  }
  canRename = true;
  sessionList;
  header;
  keybindings;
  scope = "current";
  sortMode = "threaded";
  nameFilter = "all";
  currentSessions = null;
  allSessions = null;
  currentSessionsLoader;
  allSessionsLoader;
  requestRender;
  renameSession;
  currentLoad = null;
  allLoad = null;
  mode = "list";
  renameInput = new Input3();
  renameTargetPath = null;
  // Focusable implementation - propagate to sessionList for IME cursor positioning
  _focused = false;
  get focused() {
    return this._focused;
  }
  set focused(value) {
    this._focused = value;
    this.sessionList.focused = value;
    this.renameInput.focused = value;
    if (value && this.mode === "rename") {
      this.renameInput.focused = true;
    }
  }
  buildBaseLayout(content, options) {
    this.clear();
    this.addChild(new Spacer5(1));
    this.addChild(new DynamicBorder((s) => theme.fg("accent", s)));
    this.addChild(new Spacer5(1));
    if (options?.showHeader ?? true) {
      this.addChild(this.header);
      this.addChild(new Spacer5(1));
    }
    this.addChild(content);
    this.addChild(new Spacer5(1));
    this.addChild(new DynamicBorder((s) => theme.fg("accent", s)));
  }
  constructor(currentSessionsLoader, allSessionsLoader, onSelect, onCancel, onExit, requestRender, options, currentSessionFilePath) {
    super();
    this.keybindings = options?.keybindings ?? KeybindingsManager.create();
    this.currentSessionsLoader = currentSessionsLoader;
    this.allSessionsLoader = allSessionsLoader;
    this.requestRender = requestRender;
    this.header = new SessionSelectorHeader(this.scope, this.sortMode, this.nameFilter, this.requestRender);
    const renameSession = options?.renameSession;
    this.renameSession = renameSession;
    this.canRename = !!renameSession;
    this.header.setShowRenameHint(options?.showRenameHint ?? this.canRename);
    this.sessionList = new SessionList([], false, this.sortMode, this.nameFilter, this.keybindings, currentSessionFilePath);
    this.buildBaseLayout(this.sessionList);
    this.renameInput.onSubmit = (value) => {
      void this.confirmRename(value);
    };
    const clearStatusMessage = /* @__PURE__ */ __name(() => this.header.setStatusMessage(null), "clearStatusMessage");
    this.sessionList.onSelect = (sessionPath) => {
      clearStatusMessage();
      this.cancelLoads();
      onSelect(sessionPath);
    };
    this.sessionList.onCancel = () => {
      clearStatusMessage();
      this.cancelLoads();
      onCancel();
    };
    this.sessionList.onExit = () => {
      clearStatusMessage();
      this.cancelLoads();
      onExit();
    };
    this.sessionList.onToggleScope = () => this.toggleScope();
    this.sessionList.onToggleSort = () => this.toggleSortMode();
    this.sessionList.onToggleNameFilter = () => this.toggleNameFilter();
    this.sessionList.onRenameSession = (sessionPath) => {
      if (!renameSession)
        return;
      if (this.scope === "current" ? this.currentLoad : this.allLoad)
        return;
      const sessions = this.scope === "all" ? this.allSessions ?? [] : this.currentSessions ?? [];
      const session = sessions.find((s) => s.path === sessionPath);
      this.enterRenameMode(sessionPath, session?.name);
    };
    this.sessionList.onTogglePath = (showPath) => {
      this.header.setShowPath(showPath);
      this.requestRender();
    };
    this.sessionList.onDeleteConfirmationChange = (path5) => {
      this.header.setConfirmingDeletePath(path5);
      this.requestRender();
    };
    this.sessionList.onError = (msg) => {
      this.header.setStatusMessage({ type: "error", message: msg }, 3e3);
      this.requestRender();
    };
    this.sessionList.onDeleteSession = async (sessionPath) => {
      const result = await deleteSessionFile(sessionPath);
      if (result.ok) {
        if (this.currentSessions) {
          this.currentSessions = this.currentSessions.filter((s) => s.path !== sessionPath);
        }
        if (this.allSessions) {
          this.allSessions = this.allSessions.filter((s) => s.path !== sessionPath);
        }
        const sessions = this.scope === "all" ? this.allSessions ?? [] : this.currentSessions ?? [];
        const showCwd = this.scope === "all";
        this.sessionList.setSessions(sessions, showCwd);
        const msg = result.method === "trash" ? "Session moved to trash" : "Session deleted";
        this.header.setStatusMessage({ type: "info", message: msg }, 2e3);
        await this.refreshSessionsAfterMutation();
      } else {
        const errorMessage4 = result.error ?? "Unknown error";
        this.header.setStatusMessage({ type: "error", message: `Failed to delete: ${errorMessage4}` }, 3e3);
      }
      this.requestRender();
    };
    void this.loadScope("current");
  }
  cancelLoads() {
    if (this.currentLoad) {
      this.currentLoad.abort();
      this.currentLoad = null;
      this.currentSessions = null;
    }
    if (this.allLoad) {
      this.allLoad.abort();
      this.allLoad = null;
      this.allSessions = null;
    }
  }
  enterRenameMode(sessionPath, currentName) {
    this.mode = "rename";
    this.renameTargetPath = sessionPath;
    this.renameInput.setValue(currentName ?? "");
    this.renameInput.focused = true;
    const panel = new Container6();
    panel.addChild(new Text6(theme.bold("Rename Session"), 1, 0));
    panel.addChild(new Spacer5(1));
    panel.addChild(this.renameInput);
    panel.addChild(new Spacer5(1));
    panel.addChild(new Text6(theme.fg("muted", `${keyText("tui.select.confirm")} to save \xB7 ${keyText("tui.select.cancel")} to cancel`), 1, 0));
    this.buildBaseLayout(panel, { showHeader: false });
    this.requestRender();
  }
  exitRenameMode() {
    this.mode = "list";
    this.renameTargetPath = null;
    this.buildBaseLayout(this.sessionList);
    this.requestRender();
  }
  async confirmRename(value) {
    const next = value.trim();
    if (!next)
      return;
    const target = this.renameTargetPath;
    if (!target) {
      this.exitRenameMode();
      return;
    }
    const renameSession = this.renameSession;
    if (!renameSession) {
      this.exitRenameMode();
      return;
    }
    try {
      await renameSession(target, next);
      await this.refreshSessionsAfterMutation();
    } finally {
      this.exitRenameMode();
    }
  }
  async loadScope(scope) {
    if (scope === "current" ? this.currentLoad : this.allLoad)
      return;
    const showCwd = scope === "all";
    const controller = new AbortController();
    if (scope === "current") {
      this.currentLoad = controller;
    } else {
      this.allLoad = controller;
    }
    this.header.setScope(scope);
    this.header.setLoading(true);
    this.requestRender();
    const isActive = /* @__PURE__ */ __name(() => (scope === "current" ? this.currentLoad : this.allLoad) === controller, "isActive");
    const onProgress = /* @__PURE__ */ __name((loaded, total, partialSessions) => {
      if (!isActive())
        return;
      if (partialSessions) {
        const sessions = [...partialSessions];
        if (scope === "current") {
          this.currentSessions = sessions;
        } else {
          this.allSessions = sessions;
        }
        if (scope === this.scope)
          this.sessionList.setSessions(sessions, showCwd);
      }
      if (scope !== this.scope)
        return;
      this.header.setProgress(loaded, total);
      this.requestRender();
    }, "onProgress");
    try {
      const sessions = await (scope === "current" ? this.currentSessionsLoader(onProgress, controller.signal) : this.allSessionsLoader(onProgress, controller.signal));
      if (!isActive())
        return;
      if (scope === "current") {
        this.currentSessions = sessions;
        this.currentLoad = null;
      } else {
        this.allSessions = sessions;
        this.allLoad = null;
      }
      if (scope !== this.scope)
        return;
      this.header.setLoading(false);
      this.sessionList.setSessions(sessions, showCwd);
      this.requestRender();
    } catch (err) {
      if (!isActive())
        return;
      if (scope === "current") {
        this.currentLoad = null;
        this.currentSessions = null;
      } else {
        this.allLoad = null;
        this.allSessions = null;
      }
      if (scope !== this.scope)
        return;
      const message = err instanceof Error ? err.message : String(err);
      this.header.setLoading(false);
      this.header.setStatusMessage({ type: "error", message: `Failed to load sessions: ${message}` }, 4e3);
      this.sessionList.setSessions([], showCwd);
      this.requestRender();
    }
  }
  toggleSortMode() {
    this.sortMode = this.sortMode === "threaded" ? "recent" : this.sortMode === "recent" ? "relevance" : "threaded";
    this.header.setSortMode(this.sortMode);
    this.sessionList.setSortMode(this.sortMode);
    this.requestRender();
  }
  toggleNameFilter() {
    this.nameFilter = this.nameFilter === "all" ? "named" : "all";
    this.header.setNameFilter(this.nameFilter);
    this.sessionList.setNameFilter(this.nameFilter);
    this.requestRender();
  }
  async refreshSessionsAfterMutation() {
    this.cancelLoads();
    this.currentSessions = null;
    this.allSessions = null;
    await this.loadScope(this.scope);
  }
  toggleScope() {
    this.scope = this.scope === "current" ? "all" : "current";
    const sessions = this.scope === "current" ? this.currentSessions : this.allSessions;
    const loading = (this.scope === "current" ? this.currentLoad : this.allLoad) !== null;
    this.header.setScope(this.scope);
    this.header.setLoading(loading);
    this.sessionList.setSessions(sessions ?? [], this.scope === "all");
    this.requestRender();
    if (sessions === null && !loading)
      void this.loadScope(this.scope);
  }
  getSessionList() {
    return this.sessionList;
  }
};

// pi-dist/pi-coding-agent/cli/session-picker.js
async function selectSession(currentSessionsLoader, allSessionsLoader, settingsManager) {
  const ui = await createStartupTui(settingsManager);
  return new Promise((resolve7) => {
    const keybindings = KeybindingsManager.create();
    setKeybindings2(keybindings);
    let resolved = false;
    const selector = new SessionSelectorComponent(currentSessionsLoader, allSessionsLoader, (path5) => {
      if (!resolved) {
        resolved = true;
        ui.stop();
        resolve7(path5);
      }
    }, () => {
      if (!resolved) {
        resolved = true;
        ui.stop();
        resolve7(null);
      }
    }, () => {
      ui.stop();
      process.exit(0);
    }, () => ui.requestRender(), { showRenameHint: false, keybindings });
    ui.addChild(selector);
    ui.setFocus(selector.getSessionList());
    startStartupTui(ui, settingsManager);
  });
}
__name(selectSession, "selectSession");

// pi-dist/pi-coding-agent/core/project-trust.js
function formatProjectTrustPrompt(cwd) {
  return `Trust project folder?
${cwd}

This allows ${APP_NAME} to load ${CONFIG_DIR_NAME} settings and resources, install missing project packages, and execute project extensions.`;
}
__name(formatProjectTrustPrompt, "formatProjectTrustPrompt");
async function selectProjectTrustOption(cwd, ctx) {
  const options = getProjectTrustOptions(cwd, { includeSessionOnly: true });
  const selected = await ctx.ui.select(formatProjectTrustPrompt(cwd), options.map((option) => option.label));
  return options.find((option) => option.label === selected);
}
__name(selectProjectTrustOption, "selectProjectTrustOption");
function saveProjectTrustPromptResult(trustStore, result) {
  if (result.updates.length > 0) {
    trustStore.setMany(result.updates);
  }
}
__name(saveProjectTrustPromptResult, "saveProjectTrustPromptResult");
async function resolveProjectTrusted(options) {
  if (options.trustOverride !== void 0) {
    return options.trustOverride;
  }
  if (!hasTrustRequiringProjectResources(options.cwd)) {
    return true;
  }
  if (options.extensionsResult) {
    const { result, errors } = await emitProjectTrustEvent(options.extensionsResult, { type: "project_trust", cwd: options.cwd }, options.projectTrustContext);
    for (const error of errors) {
      options.onExtensionError?.(`Extension "${error.extensionPath}" project_trust error: ${error.error}`);
    }
    if (result) {
      const trusted = result.trusted === "yes";
      if (result.remember === true) {
        options.trustStore.set(options.cwd, trusted);
      }
      return trusted;
    }
  }
  const decision = options.trustStore.get(options.cwd);
  if (decision !== null) {
    return decision;
  }
  switch (options.defaultProjectTrust ?? "ask") {
    case "always":
      return true;
    case "never":
      return false;
    case "ask":
      break;
  }
  if (!options.projectTrustContext.hasUI) {
    return false;
  }
  const selected = await selectProjectTrustOption(options.cwd, options.projectTrustContext);
  if (selected !== void 0) {
    saveProjectTrustPromptResult(options.trustStore, selected);
    return selected.trusted;
  }
  return false;
}
__name(resolveProjectTrusted, "resolveProjectTrusted");

// pi-dist/pi-coding-agent/core/settings-diagnostics.js
function collectSettingsDiagnostics(settingsManager) {
  return settingsManager.drainErrors().map(({ scope, path: path5, error }) => ({
    type: "warning",
    message: path5 ? `Invalid settings file ${path5}: ${error.message}` : `Invalid ${scope} settings: ${error.message}`
  }));
}
__name(collectSettingsDiagnostics, "collectSettingsDiagnostics");
function deduplicateDiagnostics(diagnostics) {
  const seen = /* @__PURE__ */ new Set();
  return diagnostics.filter((diagnostic) => {
    const key = `${diagnostic.type}\0${diagnostic.message}`;
    if (seen.has(key))
      return false;
    seen.add(key);
    return true;
  });
}
__name(deduplicateDiagnostics, "deduplicateDiagnostics");

// pi-dist/pi-coding-agent/extensions/llama/client.js
function errorMessage2(payload, fallback) {
  if (typeof payload !== "object" || payload === null)
    return fallback;
  const error = payload.error;
  if (typeof error !== "object" || error === null)
    return fallback;
  const message = error.message;
  return typeof message === "string" && message ? message : fallback;
}
__name(errorMessage2, "errorMessage");
function isModelInfo(value) {
  if (typeof value !== "object" || value === null)
    return false;
  const candidate = value;
  return typeof candidate.id === "string" && typeof candidate.status?.value === "string";
}
__name(isModelInfo, "isModelInfo");
function linkSignal(source, target) {
  if (!source)
    return () => {
    };
  if (source.aborted) {
    target.abort(source.reason);
    return () => {
    };
  }
  const abort = /* @__PURE__ */ __name(() => target.abort(source.reason), "abort");
  source.addEventListener("abort", abort, { once: true });
  return () => source.removeEventListener("abort", abort);
}
__name(linkSignal, "linkSignal");
function sleep(ms, signal) {
  return new Promise((resolve7, reject) => {
    if (signal?.aborted) {
      reject(signal.reason ?? new Error("Cancelled"));
      return;
    }
    const abort = /* @__PURE__ */ __name(() => {
      clearTimeout(timeout);
      reject(signal?.reason ?? new Error("Cancelled"));
    }, "abort");
    const timeout = setTimeout(() => {
      signal?.removeEventListener("abort", abort);
      resolve7();
    }, ms);
    signal?.addEventListener("abort", abort, { once: true });
  });
}
__name(sleep, "sleep");
function parseLoadProgress(data) {
  if (typeof data !== "object" || data === null)
    return void 0;
  const progress = data.progress;
  if (typeof progress !== "object" || progress === null)
    return void 0;
  const value = progress;
  const stage = typeof value.current === "string" ? value.current : typeof value.stage === "string" ? value.stage : void 0;
  const stages = Array.isArray(value.stages) ? value.stages.filter((entry) => typeof entry === "string") : [];
  const stageRatio = typeof value.value === "number" ? Math.max(0, Math.min(1, value.value)) : void 0;
  let ratio = stageRatio;
  if (stage && stages.length > 0) {
    const index = stages.indexOf(stage);
    if (index >= 0)
      ratio = (index + (stageRatio ?? 0)) / stages.length;
  }
  return {
    message: stage ? `Loading ${stage.replaceAll("_", " ")}` : "Loading model",
    ratio
  };
}
__name(parseLoadProgress, "parseLoadProgress");
function parseDownloadProgress(data) {
  if (typeof data !== "object" || data === null)
    return void 0;
  const nested = data.progress;
  const files = typeof nested === "object" && nested !== null ? nested : data;
  let done = 0;
  let total = 0;
  for (const value of Object.values(files)) {
    if (typeof value !== "object" || value === null)
      continue;
    const entry = value;
    if (typeof entry.done !== "number" || typeof entry.total !== "number")
      continue;
    done += entry.done;
    total += entry.total;
  }
  if (total <= 0)
    return void 0;
  return {
    message: "Downloading model",
    ratio: done / total,
    detail: `${formatBytes(done)} / ${formatBytes(total)}`
  };
}
__name(parseDownloadProgress, "parseDownloadProgress");
function formatBytes(bytes) {
  if (bytes < 1024)
    return `${bytes} B`;
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let value = bytes / 1024;
  let unit = units[0];
  for (let index = 1; index < units.length && value >= 1024; index++) {
    value /= 1024;
    unit = units[index];
  }
  return `${value >= 10 ? value.toFixed(1) : value.toFixed(2)} ${unit}`;
}
__name(formatBytes, "formatBytes");
function normalizeLlamaServerUrl(value) {
  const url = new URL(value.trim());
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new Error("Server URL must use http or https");
  }
  url.hash = "";
  url.search = "";
  url.pathname = url.pathname.replace(/\/+$/u, "").replace(/\/v1$/u, "") || "/";
  return url.toString().replace(/\/$/u, "");
}
__name(normalizeLlamaServerUrl, "normalizeLlamaServerUrl");
function llamaInferenceUrl(serverUrl) {
  return `${normalizeLlamaServerUrl(serverUrl)}/v1`;
}
__name(llamaInferenceUrl, "llamaInferenceUrl");
var LlamaClient = class {
  static {
    __name(this, "LlamaClient");
  }
  serverUrl;
  apiKey;
  constructor(serverUrl, apiKey) {
    this.serverUrl = normalizeLlamaServerUrl(serverUrl);
    this.apiKey = apiKey;
  }
  async request(path5, init = {}) {
    const headers = new Headers(init.headers);
    if (init.body !== void 0)
      headers.set("Content-Type", "application/json");
    if (this.apiKey)
      headers.set("Authorization", `Bearer ${this.apiKey}`);
    const timeout = AbortSignal.timeout(15e3);
    const signal = init.signal ? AbortSignal.any([init.signal, timeout]) : timeout;
    const response = await fetch(`${this.serverUrl}${path5}`, { ...init, headers, signal });
    let payload;
    try {
      payload = await response.json();
    } catch {
      payload = void 0;
    }
    if (!response.ok)
      throw new Error(errorMessage2(payload, `llama.cpp returned HTTP ${response.status}`));
    return payload;
  }
  async list(options = {}) {
    const payload = await this.request(`/models${options.reload ? "?reload=1" : ""}`, { signal: options.signal });
    if (typeof payload !== "object" || payload === null || !Array.isArray(payload.data)) {
      throw new Error("llama.cpp returned an invalid model catalog");
    }
    const data = payload.data;
    if (!data.every(isModelInfo))
      throw new Error("Server is not running in llama.cpp router mode");
    return data;
  }
  async props(options = {}) {
    const query = options.model ? `?${new URLSearchParams({ model: options.model, autoload: "false" })}` : "";
    const payload = await this.request(`/props${query}`, { signal: options.signal });
    if (typeof payload !== "object" || payload === null)
      return {};
    const { models_autoload: modelsAutoload, chat_template: chatTemplate } = payload;
    return {
      ...typeof modelsAutoload === "boolean" ? { models_autoload: modelsAutoload } : {},
      ...typeof chatTemplate === "string" ? { chat_template: chatTemplate } : {}
    };
  }
  async load(model, signal) {
    await this.request("/models/load", { method: "POST", body: JSON.stringify({ model }), signal });
  }
  async unload(model, signal) {
    await this.request("/models/unload", { method: "POST", body: JSON.stringify({ model }), signal });
  }
  async unloadAndWait(model, signal) {
    await this.unload(model, signal);
    while (true) {
      const entry = (await this.list({ signal })).find((candidate) => candidate.id === model);
      if (!entry || entry.status.value === "unloaded")
        return;
      await sleep(100, signal);
    }
  }
  async download(model, signal) {
    await this.request("/models", { method: "POST", body: JSON.stringify({ model }), signal });
  }
  async watch(onEvent, signal) {
    const headers = new Headers();
    if (this.apiKey)
      headers.set("Authorization", `Bearer ${this.apiKey}`);
    const response = await fetch(`${this.serverUrl}/models/sse`, { headers, signal });
    if (!response.ok || !response.body)
      throw new Error(`llama.cpp SSE returned HTTP ${response.status}`);
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    while (true) {
      const chunk = await reader.read();
      if (chunk.done)
        break;
      buffer += decoder.decode(chunk.value, { stream: true }).replaceAll("\r\n", "\n");
      let boundary = buffer.indexOf("\n\n");
      while (boundary >= 0) {
        const frame3 = buffer.slice(0, boundary);
        buffer = buffer.slice(boundary + 2);
        const data = frame3.split("\n").filter((line) => line.startsWith("data:")).map((line) => line.slice(5).trimStart()).join("\n");
        if (data) {
          try {
            const event = JSON.parse(data);
            if (event && typeof event.model === "string" && typeof event.event === "string")
              onEvent(event);
          } catch {
          }
        }
        boundary = buffer.indexOf("\n\n");
      }
    }
  }
  async loadAndWait(model, onProgress, signal) {
    const watcher = new AbortController();
    const unlink2 = linkSignal(signal, watcher);
    let eventLoaded = false;
    let eventError;
    void this.watch((event) => {
      if (event.model !== model)
        return;
      if (event.event !== "model_status" && event.event !== "status_change")
        return;
      const data = event.data;
      if (data?.status === "loaded")
        eventLoaded = true;
      if (data?.status === "unloaded")
        eventError = "Model failed to load";
      const progress = parseLoadProgress(event.data);
      if (progress)
        onProgress(progress);
    }, watcher.signal).catch(() => {
    });
    try {
      await this.load(model, signal);
      onProgress({ message: "Loading model" });
      while (true) {
        if (signal?.aborted)
          throw signal.reason ?? new Error("Cancelled");
        const entry = (await this.list({ signal })).find((candidate) => candidate.id === model);
        if (entry?.status.value === "loaded")
          return entry;
        if (eventLoaded && !entry)
          return { id: model, status: { value: "loaded" } };
        if (entry?.status.failed || eventError) {
          throw new Error(entry?.status.exit_code === void 0 ? eventError ?? "Model failed to load" : `Model exited with code ${entry.status.exit_code}`);
        }
        await sleep(250, signal);
      }
    } finally {
      unlink2();
      watcher.abort();
    }
  }
  async downloadAndWait(model, onProgress, signal) {
    const watcher = new AbortController();
    const unlink2 = linkSignal(signal, watcher);
    let finished = false;
    let failure;
    let sawDownloading = false;
    let polls = 0;
    void this.watch((event) => {
      if (event.model !== model)
        return;
      if (event.event === "download_finished")
        finished = true;
      if (event.event === "download_failed")
        failure = errorMessage2(event.data, "Download failed");
      if (event.event === "download_progress") {
        sawDownloading = true;
        const progress = parseDownloadProgress(event.data);
        if (progress)
          onProgress(progress);
      }
    }, watcher.signal).catch(() => {
    });
    try {
      await this.download(model, signal);
      onProgress({ message: "Downloading model" });
      while (true) {
        if (signal?.aborted)
          throw signal.reason ?? new Error("Cancelled");
        if (failure)
          throw new Error(failure);
        const models = await this.list({ signal });
        polls++;
        const entry = models.find((candidate) => candidate.id === model);
        if (entry?.status.value === "downloading") {
          sawDownloading = true;
          const progress = parseDownloadProgress(entry.status.progress);
          if (progress)
            onProgress(progress);
        } else if (finished || entry && (sawDownloading || polls >= 2)) {
          return this.list({ reload: true, signal });
        }
        await sleep(500, signal);
      }
    } finally {
      unlink2();
      watcher.abort();
    }
  }
};

// pi-dist/pi-coding-agent/extensions/llama/huggingface.js
import { readFile as readFile2 } from "node:fs/promises";
import { homedir as homedir2 } from "node:os";
import { join as join4 } from "node:path";
var DEFAULT_HUGGING_FACE_URL = "https://huggingface.co";
var QUANTIZATION_PATTERN = /(?:^|[-_.])((?:UD-)?(?:IQ\d(?:_[A-Z0-9]+)+|Q\d(?:_[A-Z0-9]+)+|BF16|F16|F32|MXFP\d(?:_[A-Z0-9]+)*))$/iu;
var SHARD_SUFFIX_PATTERN = /-\d{5}-of-\d{5}$/u;
function payloadError(payload, fallback) {
  if (typeof payload !== "object" || payload === null)
    return fallback;
  const error = payload.error;
  return typeof error === "string" && error ? error : fallback;
}
__name(payloadError, "payloadError");
function parseRateLimitDelay(value) {
  const match = value?.match(/(?:^|;)t=(\d+)/u);
  return match ? Number(match[1]) : void 0;
}
__name(parseRateLimitDelay, "parseRateLimitDelay");
async function readToken(path5) {
  try {
    const token = (await readFile2(path5, "utf8")).trim();
    return token || void 0;
  } catch {
    return void 0;
  }
}
__name(readToken, "readToken");
async function findHuggingFaceToken(env = process.env) {
  const fromEnvironment = env.HF_TOKEN?.trim();
  if (fromEnvironment)
    return fromEnvironment;
  const paths = [
    env.HF_TOKEN_PATH,
    env.HF_HOME ? join4(env.HF_HOME, "token") : void 0,
    env.XDG_CACHE_HOME ? join4(env.XDG_CACHE_HOME, "huggingface", "token") : void 0,
    join4(homedir2(), ".cache", "huggingface", "token")
  ].filter((path5) => Boolean(path5));
  for (const path5 of new Set(paths)) {
    const token = await readToken(path5);
    if (token)
      return token;
  }
  return void 0;
}
__name(findHuggingFaceToken, "findHuggingFaceToken");
var HuggingFaceClient = class {
  static {
    __name(this, "HuggingFaceClient");
  }
  token;
  baseUrl;
  constructor(token, baseUrl = DEFAULT_HUGGING_FACE_URL) {
    this.token = token;
    this.baseUrl = baseUrl.replace(/\/+$/u, "");
  }
  async request(path5, signal) {
    const headers = new Headers();
    if (this.token)
      headers.set("Authorization", `Bearer ${this.token}`);
    const timeout = AbortSignal.timeout(15e3);
    const response = await fetch(`${this.baseUrl}${path5}`, {
      headers,
      signal: signal ? AbortSignal.any([signal, timeout]) : timeout
    });
    let payload;
    try {
      payload = await response.json();
    } catch {
      payload = void 0;
    }
    if (!response.ok) {
      const fallback = `Hugging Face returned HTTP ${response.status}`;
      if (response.status === 429) {
        const delay = Number(response.headers.get("retry-after")) || parseRateLimitDelay(response.headers.get("ratelimit"));
        throw new Error(delay ? `Hugging Face rate limit reached; retry in ${delay}s` : "Hugging Face rate limit reached");
      }
      throw new Error(payloadError(payload, fallback));
    }
    return payload;
  }
  async search(query, signal) {
    const params = new URLSearchParams({
      search: query,
      filter: "gguf",
      sort: "downloads",
      direction: "-1",
      limit: "20"
    });
    const payload = await this.request(`/api/models?${params}`, signal);
    if (!Array.isArray(payload))
      throw new Error("Hugging Face returned invalid search results");
    return payload.flatMap((value) => {
      if (typeof value !== "object" || value === null || typeof value.id !== "string")
        return [];
      const model = value;
      return [{ id: model.id, downloads: typeof model.downloads === "number" ? model.downloads : 0 }];
    });
  }
  async details(id, signal) {
    const encodedId = id.split("/").map(encodeURIComponent).join("/");
    const payload = await this.request(`/api/models/${encodedId}?blobs=true`, signal);
    if (typeof payload !== "object" || payload === null) {
      throw new Error("Hugging Face returned invalid model details");
    }
    const model = payload;
    const sizes = /* @__PURE__ */ new Map();
    if (Array.isArray(model.siblings)) {
      for (const value of model.siblings) {
        if (typeof value !== "object" || value === null)
          continue;
        const file = value;
        if (typeof file.rfilename !== "string" || !file.rfilename.toLowerCase().endsWith(".gguf"))
          continue;
        const filename = file.rfilename.split("/").at(-1);
        if (filename.toLowerCase().startsWith("mmproj"))
          continue;
        const stem = filename.slice(0, -5).replace(SHARD_SUFFIX_PATTERN, "");
        const quantization = stem.match(QUANTIZATION_PATTERN)?.[1]?.toUpperCase();
        if (!quantization)
          continue;
        const current = sizes.get(quantization) ?? { total: 0, complete: true };
        if (typeof file.size === "number")
          current.total += file.size;
        else
          current.complete = false;
        sizes.set(quantization, current);
      }
    }
    const quantizations = [...sizes].map(([name, size]) => ({ name, size: size.complete ? size.total : void 0 })).sort((left, right) => {
      if (left.name === "Q4_K_M")
        return -1;
      if (right.name === "Q4_K_M")
        return 1;
      return (left.size ?? Number.MAX_SAFE_INTEGER) - (right.size ?? Number.MAX_SAFE_INTEGER) || left.name.localeCompare(right.name);
    });
    return {
      id: typeof model.id === "string" ? model.id : id,
      gated: model.gated === "auto" || model.gated === "manual" ? model.gated : false,
      quantizations
    };
  }
};

// pi-dist/pi-coding-agent/extensions/llama/provider.js
import { isModelType } from "../../pi-ai/sdk-bundle/index.js";
import { llamaCppClassifyApi } from "../../pi-ai/api/llama-cpp-classify.lazy.js";
import { typesafeSystemOneApi } from "../../pi-ai/api/typesafe-system-one.lazy.js";
import { stream, streamSimple } from "../../pi-ai/sdk-bundle/compat.js";
var LLAMA_PROVIDER_ID = "llama.cpp";
var DEFAULT_LLAMA_SERVER_URL = "http://127.0.0.1:8080";
function credentialServerUrl(credential) {
  const value = credential?.env?.LLAMA_BASE_URL;
  return typeof value === "string" && value.trim() ? normalizeLlamaServerUrl(value) : void 0;
}
__name(credentialServerUrl, "credentialServerUrl");
async function resolveServerUrl(ctx, credential) {
  const configured = credentialServerUrl(credential) ?? (await ctx.env("LLAMA_BASE_URL"))?.trim();
  return configured ? normalizeLlamaServerUrl(configured) : void 0;
}
__name(resolveServerUrl, "resolveServerUrl");
function modelIsSelectable(model, routerAutoload) {
  if (model.status.value === "loaded")
    return true;
  if (model.status.value === "sleeping")
    return true;
  return routerAutoload && model.status.value === "unloaded" && !model.status.failed && model.source === "preset";
}
__name(modelIsSelectable, "modelIsSelectable");
async function routerAutoloadEnabled(client, catalog, signal) {
  if (!catalog.some((model) => model.status.value === "unloaded" && model.source === "preset"))
    return false;
  try {
    return (await client.props({ signal })).models_autoload === true;
  } catch {
    return false;
  }
}
__name(routerAutoloadEnabled, "routerAutoloadEnabled");
function configuredContextWindow(model) {
  const args = model.status.args ?? [];
  for (let index = 0; index < args.length - 1; index++) {
    const flag = args[index];
    if (flag !== "--ctx-size" && flag !== "-c" && flag !== "-ctx")
      continue;
    const contextWindow = Number(args[index + 1]);
    if (Number.isSafeInteger(contextWindow) && contextWindow > 0)
      return contextWindow;
  }
  return void 0;
}
__name(configuredContextWindow, "configuredContextWindow");
function contextWindowOf(model, cachedContextWindow) {
  const runtimeContextWindow = model.meta?.n_ctx;
  if (runtimeContextWindow && runtimeContextWindow > 0)
    return runtimeContextWindow;
  const configuredContext = configuredContextWindow(model);
  if (configuredContext)
    return configuredContext;
  if (cachedContextWindow && cachedContextWindow > 0)
    return cachedContextWindow;
  const trainingContextWindow = model.meta?.n_ctx_train;
  return trainingContextWindow && trainingContextWindow > 0 ? trainingContextWindow : 128e3;
}
__name(contextWindowOf, "contextWindowOf");
function isDecisionModel(model) {
  return model.architecture?.output_modalities?.includes("decisions") === true;
}
__name(isDecisionModel, "isDecisionModel");
function isChatModel(model) {
  return !isDecisionModel(model) || model.architecture?.output_modalities?.includes("text") === true;
}
__name(isChatModel, "isChatModel");
function toPiClassifierModel(model, serverUrl, cachedContextWindow) {
  const decision = isDecisionModel(model);
  return {
    type: "classifier",
    id: model.id,
    name: model.id,
    api: decision ? "typesafe-system-one" : "llama-cpp-classify",
    provider: LLAMA_PROVIDER_ID,
    baseUrl: decision ? llamaInferenceUrl(serverUrl) : serverUrl,
    input: ["text"],
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow: contextWindowOf(model, cachedContextWindow)
  };
}
__name(toPiClassifierModel, "toPiClassifierModel");
function isLlamaClassifierModel(model) {
  return isModelType(model, "classifier") && (model.api === "llama-cpp-classify" || model.api === "typesafe-system-one");
}
__name(isLlamaClassifierModel, "isLlamaClassifierModel");
function toPiModel(model, serverUrl, props, cachedContextWindow) {
  const contextWindow = contextWindowOf(model, cachedContextWindow);
  const reasoning = props?.chat_template?.includes("enable_thinking") === true;
  return {
    id: model.id,
    name: model.id,
    api: "openai-completions",
    provider: LLAMA_PROVIDER_ID,
    baseUrl: llamaInferenceUrl(serverUrl),
    reasoning,
    ...reasoning && {
      thinkingLevelMap: { off: "off", minimal: null, low: null, medium: "medium", high: null, xhigh: null }
    },
    input: model.architecture?.input_modalities?.includes("image") ? ["text", "image"] : ["text"],
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow,
    maxTokens: contextWindow,
    compat: {
      supportsStore: false,
      supportsDeveloperRole: false,
      supportsReasoningEffort: false,
      supportsUsageInStreaming: true,
      supportsStrictMode: false,
      maxTokensField: "max_tokens",
      ...reasoning && { thinkingFormat: "qwen-chat-template" }
    }
  };
}
__name(toPiModel, "toPiModel");
function createLlamaProvider() {
  let models = [];
  let classifiers = [];
  const fallbackClassifier = llamaCppClassifyApi();
  const decisionClassifier = typesafeSystemOneApi();
  const setCatalog = /* @__PURE__ */ __name((catalog, serverUrl, options = {}) => {
    const selectable = catalog.filter((model) => modelIsSelectable(model, options.routerAutoload === true));
    models = selectable.filter(isChatModel).map((model) => toPiModel(model, serverUrl));
    classifiers = selectable.map((model) => toPiClassifierModel(model, serverUrl));
  }, "setCatalog");
  const provider = {
    id: LLAMA_PROVIDER_ID,
    name: "llama.cpp",
    baseUrl: llamaInferenceUrl(DEFAULT_LLAMA_SERVER_URL),
    auth: {
      apiKey: {
        name: "llama.cpp server",
        login: /* @__PURE__ */ __name(async (interaction) => {
          const enteredUrl = await interaction.prompt({
            type: "text",
            message: "llama.cpp server URL",
            placeholder: process.env.LLAMA_BASE_URL ?? DEFAULT_LLAMA_SERVER_URL
          });
          const serverUrl = normalizeLlamaServerUrl(enteredUrl.trim() || process.env.LLAMA_BASE_URL || DEFAULT_LLAMA_SERVER_URL);
          const apiKey = (await interaction.prompt({
            type: "secret",
            message: "API key (optional)"
          })).trim();
          await new LlamaClient(serverUrl, apiKey || void 0).list({ signal: interaction.signal });
          return {
            type: "api_key",
            key: apiKey || void 0,
            env: { LLAMA_BASE_URL: serverUrl }
          };
        }, "login"),
        check: /* @__PURE__ */ __name(async ({ ctx, credential }) => {
          const serverUrl = await resolveServerUrl(ctx, credential);
          return serverUrl ? { type: "api_key", source: credential ? "stored credential" : "LLAMA_BASE_URL" } : void 0;
        }, "check"),
        resolve: /* @__PURE__ */ __name(async ({ ctx, credential }) => {
          const serverUrl = await resolveServerUrl(ctx, credential);
          if (!serverUrl)
            return void 0;
          const apiKey = credential?.key ?? await ctx.env("LLAMA_API_KEY") ?? "local";
          return {
            auth: { apiKey, baseUrl: llamaInferenceUrl(serverUrl) },
            env: { ...credential?.env, LLAMA_BASE_URL: serverUrl },
            source: credential ? "stored credential" : "LLAMA_BASE_URL"
          };
        }, "resolve")
      }
    },
    getModels: /* @__PURE__ */ __name(() => models, "getModels"),
    getAllModels: /* @__PURE__ */ __name(() => [...models, ...classifiers], "getAllModels"),
    refreshModels: /* @__PURE__ */ __name(async (context) => {
      const cachedContextWindows = /* @__PURE__ */ new Map();
      if (context.stored) {
        const stored = context.stored.models.filter((model) => model.provider === LLAMA_PROVIDER_ID);
        const restored = stored.filter((model) => isModelType(model, "chat") && model.api === "openai-completions");
        const restoredClassifiers = stored.filter(isLlamaClassifierModel);
        for (const model of [...restored, ...restoredClassifiers]) {
          cachedContextWindows.set(model.id, model.contextWindow);
        }
        if (!await context.publish({
          update: /* @__PURE__ */ __name(() => {
            models = restored;
            classifiers = restoredClassifiers;
          }, "update")
        })) {
          return;
        }
      }
      if (!context.allowNetwork || context.signal.aborted || context.credential?.type !== "api_key")
        return;
      const serverUrl = credentialServerUrl(context.credential);
      if (!serverUrl)
        return;
      const client = new LlamaClient(serverUrl, context.credential.key);
      const catalog = await client.list({ signal: context.signal });
      if (context.signal.aborted)
        return;
      const routerAutoload = await routerAutoloadEnabled(client, catalog, context.signal);
      if (context.signal.aborted)
        return;
      const selectable = catalog.filter((model) => modelIsSelectable(model, routerAutoload));
      const refreshed = await Promise.all(selectable.filter(isChatModel).map(async (model) => {
        const cachedContextWindow = cachedContextWindows.get(model.id);
        if (model.status.value !== "loaded")
          return toPiModel(model, serverUrl, void 0, cachedContextWindow);
        const props = await client.props({ model: model.id, signal: context.signal });
        return toPiModel(model, serverUrl, props, cachedContextWindow);
      }));
      const refreshedClassifiers = selectable.map((model) => toPiClassifierModel(model, serverUrl, cachedContextWindows.get(model.id)));
      if (context.signal.aborted)
        return;
      await context.publish({
        persist: { models: [...refreshed, ...refreshedClassifiers], checkedAt: Date.now() },
        update: /* @__PURE__ */ __name(() => {
          models = refreshed;
          classifiers = refreshedClassifiers;
        }, "update")
      });
    }, "refreshModels"),
    stream: /* @__PURE__ */ __name((model, context, options) => stream(model, context, options), "stream"),
    streamSimple: /* @__PURE__ */ __name((model, context, options) => streamSimple(model, context, options), "streamSimple"),
    classify: /* @__PURE__ */ __name((model, context, options) => (model.api === "typesafe-system-one" ? decisionClassifier : fallbackClassifier).classify(model, context, options), "classify")
  };
  return { provider, setCatalog };
}
__name(createLlamaProvider, "createLlamaProvider");

// pi-dist/pi-coding-agent/extensions/llama/ui.js
import { Container as Container7, fuzzyFilter as fuzzyFilter2, Input as Input4, SelectList as SelectList2, Spacer as Spacer6, Text as Text7, truncateToWidth as truncateToWidth3, visibleWidth as visibleWidth3 } from "../../../pi-tui.mjs";
var DOWNLOAD_VALUE = "\0download";
function contextLabel(model) {
  const context = model.meta?.n_ctx ?? model.meta?.n_ctx_train;
  if (context)
    return context >= 1e3 ? `${Math.round(context / 1e3)}k` : String(context);
  const args = model.status.args ?? [];
  for (let index = 0; index < args.length - 1; index++) {
    if (args[index] !== "--ctx-size" && args[index] !== "-c" && args[index] !== "-ctx")
      continue;
    const value = Number(args[index + 1]);
    if (Number.isFinite(value) && value > 0)
      return value >= 1e3 ? `${Math.round(value / 1e3)}k` : String(value);
  }
  return void 0;
}
__name(contextLabel, "contextLabel");
function modelDescription(model) {
  const details = [];
  const loaded = model.status.value === "loaded" || model.status.value === "sleeping";
  if (loaded)
    details.push("loaded");
  else if (model.status.value !== "unloaded")
    details.push(model.status.value);
  const context = loaded ? contextLabel(model) : void 0;
  if (context)
    details.push(`${context} context`);
  return details.join(" \xB7 ");
}
__name(modelDescription, "modelDescription");
function selectTheme(theme2) {
  return {
    selectedPrefix: /* @__PURE__ */ __name((text) => theme2.fg("accent", text), "selectedPrefix"),
    selectedText: /* @__PURE__ */ __name((text) => theme2.fg("accent", text), "selectedText"),
    description: /* @__PURE__ */ __name((text) => theme2.fg("muted", text), "description"),
    scrollInfo: /* @__PURE__ */ __name((text) => theme2.fg("dim", text), "scrollInfo"),
    noMatch: /* @__PURE__ */ __name((text) => theme2.fg("warning", text), "noMatch")
  };
}
__name(selectTheme, "selectTheme");
function frame2(theme2, title, body, footer) {
  const container = new Container7();
  container.addChild(new DynamicBorder((text) => theme2.fg("accent", text)));
  container.addChild(new Text7(theme2.fg("accent", theme2.bold(title)), 1, 0));
  for (const child of body)
    container.addChild(child);
  if (footer) {
    container.addChild(new Spacer6(1));
    container.addChild(new Text7(theme2.fg("dim", footer), 1, 0));
  }
  container.addChild(new DynamicBorder((text) => theme2.fg("accent", text)));
  return container;
}
__name(frame2, "frame");
function compactCount(value) {
  if (value >= 1e6)
    return `${(value / 1e6).toFixed(value >= 1e7 ? 0 : 1)}M`;
  if (value >= 1e3)
    return `${(value / 1e3).toFixed(value >= 1e5 ? 0 : 1)}k`;
  return String(value);
}
__name(compactCount, "compactCount");
var HuggingFaceSearch = class extends Container7 {
  static {
    __name(this, "HuggingFaceSearch");
  }
  tui;
  theme;
  keybindings;
  search;
  cache;
  onSelectModel;
  input = new Input4();
  resultsContainer = new Container7();
  results = [];
  filteredResults = [];
  selectedIndex = 0;
  query = "";
  status = "Type at least 2 characters";
  debounce;
  request;
  closed = false;
  _focused = false;
  constructor(tui, theme2, keybindings, search, cache, onSelectModel) {
    super();
    this.tui = tui;
    this.theme = theme2;
    this.keybindings = keybindings;
    this.search = search;
    this.cache = cache;
    this.onSelectModel = onSelectModel;
    this.addChild(new Text7(theme2.fg("dim", "Model name or owner/repository[:quant]"), 1, 0));
    this.addChild(this.input);
    this.addChild(new Spacer6(1));
    this.addChild(this.resultsContainer);
    this.updateResults();
  }
  get focused() {
    return this._focused;
  }
  set focused(value) {
    this._focused = value;
    this.input.focused = value;
  }
  updateResults() {
    this.resultsContainer.clear();
    const maxVisible = 10;
    const start = Math.max(0, Math.min(this.selectedIndex - Math.floor(maxVisible / 2), this.filteredResults.length - maxVisible));
    const end = Math.min(start + maxVisible, this.filteredResults.length);
    for (let index = start; index < end; index++) {
      const model = this.filteredResults[index];
      if (!model)
        continue;
      const prefix = index === this.selectedIndex ? "\u2192 " : "  ";
      const details = `${compactCount(model.downloads)} downloads`;
      this.resultsContainer.addChild(new Text7(index === this.selectedIndex ? this.theme.fg("accent", `${prefix}${model.id}  ${details}`) : `${prefix}${model.id}${this.theme.fg("muted", `  ${details}`)}`, 0, 0));
    }
    if (start > 0 || end < this.filteredResults.length) {
      this.resultsContainer.addChild(new Text7(this.theme.fg("dim", `  (${this.selectedIndex + 1}/${this.filteredResults.length})`), 0, 0));
    }
    if (this.filteredResults.length === 0) {
      this.resultsContainer.addChild(new Text7(this.theme.fg("dim", `  ${this.status}`), 0, 0));
    } else if (this.status === "Searching Hugging Face\u2026") {
      this.resultsContainer.addChild(new Text7(this.theme.fg("dim", `  ${this.status}`), 0, 0));
    }
    this.tui.requestRender();
  }
  filterResults() {
    if (this.query) {
      const matches = new Set(fuzzyFilter2(this.results, this.query, (model) => model.id).map((model) => model.id));
      this.filteredResults = this.results.filter((model) => matches.has(model.id));
    } else {
      this.filteredResults = this.results;
    }
    this.selectedIndex = Math.min(this.selectedIndex, Math.max(0, this.filteredResults.length - 1));
    this.updateResults();
  }
  scheduleSearch() {
    if (this.debounce)
      clearTimeout(this.debounce);
    this.request?.abort();
    this.request = void 0;
    if (this.query.length < 2) {
      this.status = "Type at least 2 characters";
      this.filterResults();
      return;
    }
    const cached = this.cache.get(this.query.toLowerCase());
    if (cached) {
      this.results = cached;
      this.status = cached.length === 0 ? "No GGUF models found" : "";
      this.filterResults();
      return;
    }
    this.status = "Searching Hugging Face\u2026";
    this.filterResults();
    this.debounce = setTimeout(() => void this.runSearch(this.query), 500);
  }
  async runSearch(query) {
    const request = new AbortController();
    this.request = request;
    try {
      const results = await this.search(query, request.signal);
      this.cache.set(query.toLowerCase(), results);
      if (this.closed || request.signal.aborted || this.query !== query)
        return;
      this.results = results;
      this.selectedIndex = 0;
      this.status = results.length === 0 ? "No GGUF models found" : "";
      this.filterResults();
    } catch (error) {
      if (this.closed || request.signal.aborted || this.query !== query)
        return;
      this.results = [];
      this.status = error instanceof Error ? error.message : String(error);
      this.filterResults();
    } finally {
      if (this.request === request)
        this.request = void 0;
    }
  }
  close(model) {
    if (this.closed)
      return;
    this.closed = true;
    if (this.debounce)
      clearTimeout(this.debounce);
    this.request?.abort();
    this.onSelectModel(model);
  }
  handleInput(data) {
    if (this.keybindings.matches(data, "tui.select.up")) {
      if (this.filteredResults.length > 0) {
        this.selectedIndex = this.selectedIndex === 0 ? this.filteredResults.length - 1 : this.selectedIndex - 1;
        this.updateResults();
      }
      return;
    }
    if (this.keybindings.matches(data, "tui.select.down")) {
      if (this.filteredResults.length > 0) {
        this.selectedIndex = this.selectedIndex === this.filteredResults.length - 1 ? 0 : this.selectedIndex + 1;
        this.updateResults();
      }
      return;
    }
    if (this.keybindings.matches(data, "tui.select.confirm")) {
      const exact = /^[^/\s]+\/[^:\s]+(?::[^\s:]+)?$/u.test(this.query) ? this.query : void 0;
      const selected = exact ?? this.filteredResults[this.selectedIndex]?.id;
      if (selected)
        this.close(selected);
      return;
    }
    if (this.keybindings.matches(data, "tui.select.cancel")) {
      this.close(void 0);
      return;
    }
    this.input.handleInput(data);
    const query = this.input.getValue().trim();
    if (query === this.query)
      return;
    this.query = query;
    this.scheduleSearch();
  }
};
var LlamaView = class {
  static {
    __name(this, "LlamaView");
  }
  tui;
  theme;
  keybindings;
  searchCache = /* @__PURE__ */ new Map();
  content;
  inputHandler;
  inputTarget;
  progressPromise;
  progressResolver;
  showingProgress = false;
  _focused = false;
  constructor(tui, theme2, keybindings) {
    this.tui = tui;
    this.theme = theme2;
    this.keybindings = keybindings;
    this.content = frame2(theme2, "llama.cpp models", [new Text7(theme2.fg("muted", "Loading\u2026"), 1, 1)]);
  }
  get focused() {
    return this._focused;
  }
  set focused(value) {
    this._focused = value;
    if (this.inputTarget)
      this.inputTarget.focused = value;
  }
  setContent(content, inputHandler, inputTarget) {
    if (this.inputTarget)
      this.inputTarget.focused = false;
    this.progressPromise = void 0;
    this.progressResolver = void 0;
    this.showingProgress = false;
    this.content = content;
    this.inputHandler = inputHandler;
    this.inputTarget = inputTarget;
    if (this.inputTarget)
      this.inputTarget.focused = this._focused;
    this.tui.requestRender();
  }
  showModels(serverUrl, models) {
    const sorted = [...models].sort((left, right) => {
      const loaded = Number(right.status.value === "loaded") - Number(left.status.value === "loaded");
      return loaded || left.id.localeCompare(right.id);
    });
    const byId = new Map(sorted.map((model) => [model.id, model]));
    const items = [
      ...sorted.map((model) => ({
        value: model.id,
        label: model.id,
        description: modelDescription(model)
      })),
      { value: DOWNLOAD_VALUE, label: "Download model\u2026", description: "Hugging Face owner/repository[:quant]" }
    ];
    return new Promise((resolve7) => {
      const list = new SelectList2(items, Math.min(items.length, 12), selectTheme(this.theme), {
        minPrimaryColumnWidth: 36,
        maxPrimaryColumnWidth: 56
      });
      list.onSelect = (item) => {
        if (item.value === DOWNLOAD_VALUE)
          resolve7({ type: "download" });
        else {
          const model = byId.get(item.value);
          if (model)
            resolve7({ type: "model", model });
        }
      };
      list.onCancel = () => resolve7({ type: "close" });
      this.setContent(frame2(this.theme, "llama.cpp models", [new Text7(this.theme.fg("dim", serverUrl), 1, 0), new Spacer6(1), list], `${keyHint("tui.select.confirm", "load/unload/download")} \u2022 ${keyHint("tui.select.cancel", "close")}`), list);
    });
  }
  select(title, options) {
    return new Promise((resolve7) => {
      const list = new SelectList2(options.map((option) => ({ value: option, label: option })), Math.min(options.length, 12), selectTheme(this.theme));
      list.onSelect = (item) => resolve7(item.value);
      list.onCancel = () => resolve7(void 0);
      this.setContent(frame2(this.theme, title, [new Spacer6(1), list], `${keyHint("tui.select.confirm", "select")} \u2022 ${keyHint("tui.select.cancel", "cancel")}`), list);
    });
  }
  async confirm(title, message) {
    return await this.select(`${title}
${message}`, ["Yes", "No"]) === "Yes";
  }
  async connectionError(serverUrl, message) {
    const choice = await this.select(`llama.cpp unavailable
${serverUrl}

${message}`, ["Retry", "Close"]);
    return choice === "Retry" ? "retry" : "close";
  }
  searchModels(search) {
    return new Promise((resolve7) => {
      const component = new HuggingFaceSearch(this.tui, this.theme, this.keybindings, search, this.searchCache, resolve7);
      this.setContent(frame2(this.theme, "Download model", [new Spacer6(1), component], `${keyHint("tui.select.confirm", "select")} \u2022 ${keyHint("tui.select.cancel", "back")}`), component, component);
    });
  }
  showStatus(title, message) {
    this.setContent(frame2(this.theme, title, [new Spacer6(1), new Text7(this.theme.fg("muted", message), 1, 0)]));
  }
  progress(state) {
    if (!this.progressPromise) {
      this.progressPromise = new Promise((resolve7) => {
        this.progressResolver = resolve7;
      });
    }
    this.showingProgress = true;
    this.updateProgress(state);
    return this.progressPromise;
  }
  updateProgress(state) {
    if (!this.showingProgress)
      return;
    const body = [
      new Text7(this.theme.fg("text", state.model), 1, 0),
      new Spacer6(1),
      new Text7(this.theme.fg("muted", state.message), 1, 0)
    ];
    if (state.ratio !== void 0) {
      const available = 40;
      const filled = Math.round(Math.max(0, Math.min(1, state.ratio)) * available);
      body.push(new Text7(this.theme.fg("accent", `${"\u2588".repeat(filled)}${"\u2500".repeat(available - filled)} ${Math.round(state.ratio * 100)}%`), 1, 0));
    }
    if (state.detail)
      body.push(new Text7(this.theme.fg("dim", state.detail), 1, 0));
    this.content = frame2(this.theme, state.title, body, keyHint("tui.select.cancel", "stop"));
    this.inputHandler = void 0;
    this.tui.requestRender();
  }
  handleInput(data) {
    if (this.progressResolver && this.keybindings.matches(data, "tui.select.cancel")) {
      const resolve7 = this.progressResolver;
      this.progressPromise = void 0;
      this.progressResolver = void 0;
      resolve7();
      return;
    }
    this.inputHandler?.handleInput?.(data);
    this.tui.requestRender();
  }
  render(width) {
    return this.content.render(width).map((line) => visibleWidth3(line) > width ? truncateToWidth3(line, width, "") : line);
  }
  invalidate() {
    this.content.invalidate();
  }
};
async function showLlamaUi(ctx, run) {
  await ctx.ui.custom((tui, theme2, keybindings, done) => {
    const view = new LlamaView(tui, theme2, keybindings);
    void run(view).then(() => done(), (error) => {
      ctx.ui.notify(error instanceof Error ? error.message : String(error), "error");
      done();
    });
    return view;
  });
}
__name(showLlamaUi, "showLlamaUi");
async function runWithProgress(ui, options) {
  const controller = new AbortController();
  const state = { title: options.title, model: options.model, message: options.initialMessage };
  const settled = options.run(controller.signal, (progress) => {
    Object.assign(state, progress);
    ui.updateProgress(state);
  }).then((value) => ({ ok: true, value }), (error) => ({ ok: false, error }));
  let completed = false;
  settled.finally(() => {
    completed = true;
  });
  while (!completed) {
    const outcome = await Promise.race([
      settled.then(() => "settled"),
      ui.progress(state).then(() => "stop")
    ]);
    if (outcome === "settled")
      break;
    const stop = await ui.confirm(options.cancelTitle, options.cancelMessage);
    if (!stop || completed)
      continue;
    try {
      await options.cancel();
    } finally {
      controller.abort(new Error("Cancelled"));
    }
    await settled;
    return { cancelled: true };
  }
  const result = await settled;
  if (!result.ok)
    throw result.error;
  return { cancelled: false, value: result.value };
}
__name(runWithProgress, "runWithProgress");

// pi-dist/pi-coding-agent/extensions/llama/index.js
function modelIsLoaded(model) {
  return model.status.value === "loaded" || model.status.value === "sleeping";
}
__name(modelIsLoaded, "modelIsLoaded");
function isConnectionError(error) {
  if (!(error instanceof Error))
    return false;
  const message = `${error.name} ${error.message}`.toLowerCase();
  return message.includes("fetch failed") || message.includes("timeout") || message.includes("network");
}
__name(isConnectionError, "isConnectionError");
function connectionErrorMessage(error) {
  if (isConnectionError(error))
    return "Could not connect to the server.";
  return error instanceof Error ? error.message : String(error);
}
__name(connectionErrorMessage, "connectionErrorMessage");
function parseHuggingFaceModel(value) {
  const colon = value.indexOf(":", value.indexOf("/") + 1);
  return colon < 0 ? { repository: value } : { repository: value.slice(0, colon), quantization: value.slice(colon + 1) };
}
__name(parseHuggingFaceModel, "parseHuggingFaceModel");
async function configuredClient(ctx) {
  const result = await ctx.modelRegistry.getProviderAuth(LLAMA_PROVIDER_ID);
  if (!result) {
    ctx.ui.notify(`Configure llama.cpp with /login ${LLAMA_PROVIDER_ID}`, "warning");
    return void 0;
  }
  const configuredUrl = result.env?.LLAMA_BASE_URL;
  const serverUrl = normalizeLlamaServerUrl(typeof configuredUrl === "string" && configuredUrl ? configuredUrl : result.auth.baseUrl ?? "");
  return new LlamaClient(serverUrl, result.auth.apiKey);
}
__name(configuredClient, "configuredClient");
function llamaExtension(pi) {
  const provider = createLlamaProvider();
  pi.registerProvider(provider.provider);
  const syncCatalog = /* @__PURE__ */ __name(async (ctx, client, catalog) => {
    const signal = AbortSignal.timeout(15e3);
    const current = catalog ?? await client.list({ signal });
    provider.setCatalog(current, client.serverUrl);
    const result = await ctx.modelRegistry.refresh({
      providers: [LLAMA_PROVIDER_ID],
      // /llama already contacted the configured llama.cpp server, so keep this refresh live even in PI_OFFLINE.
      allowNetwork: true,
      signal
    });
    if (result.aborted)
      throw new Error("Model catalog refresh timed out.");
    const refreshError = result.errors.get(LLAMA_PROVIDER_ID);
    if (refreshError)
      throw refreshError;
    return current;
  }, "syncCatalog");
  const loadModel = /* @__PURE__ */ __name(async (ctx, ui, client, catalog, target) => {
    const loaded = catalog.filter((model) => model.id !== target.id && modelIsLoaded(model));
    let replace = false;
    if (loaded.length > 0) {
      const choice = await ui.select(`${loaded.length} model${loaded.length === 1 ? " is" : "s are"} loaded`, [
        "Unload all and load",
        "Keep loaded and load",
        "Cancel"
      ]);
      if (!choice || choice === "Cancel")
        return;
      replace = choice === "Unload all and load";
    }
    const restoreLoaded = /* @__PURE__ */ __name(async () => {
      ctx.ui.notify("Restoring previously loaded models");
      for (const model of loaded)
        await client.loadAndWait(model.id, () => {
        });
      await syncCatalog(ctx, client);
    }, "restoreLoaded");
    if (replace) {
      for (const model of loaded)
        await client.unloadAndWait(model.id);
    }
    try {
      const result = await runWithProgress(ui, {
        title: "Loading model",
        model: target.id,
        initialMessage: "Starting\u2026",
        cancelTitle: "Stop loading?",
        cancelMessage: target.id,
        run: /* @__PURE__ */ __name((signal, update) => client.loadAndWait(target.id, update, signal), "run"),
        cancel: /* @__PURE__ */ __name(() => client.unload(target.id), "cancel")
      });
      if (result.cancelled) {
        if (replace)
          await restoreLoaded();
        return;
      }
      const refreshed = await syncCatalog(ctx, client);
      const loadedModel = refreshed.find((model) => model.id === target.id);
      ctx.ui.notify(loadedModel?.status.value === "loaded" ? `Loaded ${target.id}` : `Load started for ${target.id}`);
    } catch (error) {
      if (replace) {
        try {
          await restoreLoaded();
        } catch {
        }
      }
      throw error;
    }
  }, "loadModel");
  const unloadModel = /* @__PURE__ */ __name(async (ctx, ui, client, model) => {
    if (!await ui.confirm("Unload model?", model.id))
      return;
    await client.unloadAndWait(model.id);
    await syncCatalog(ctx, client);
    ctx.ui.notify(`Unloaded ${model.id}`);
  }, "unloadModel");
  const downloadModel = /* @__PURE__ */ __name(async (ctx, ui, client) => {
    const huggingFace = new HuggingFaceClient(await findHuggingFaceToken());
    const selected = await ui.searchModels((query, signal) => huggingFace.search(query, signal));
    if (!selected)
      return;
    const parsed = parseHuggingFaceModel(selected);
    ui.showStatus("Loading model details", parsed.repository);
    const details = await huggingFace.details(parsed.repository);
    if (details.gated) {
      const approval = details.gated === "manual" ? "Manual approval is required" : "Accept the access terms";
      const choice = await ui.select(`Hugging Face access required
${details.id}

${approval} at:
https://huggingface.co/${details.id}

The llama.cpp server needs HF_TOKEN with access.`, ["Continue", "Back"]);
      if (choice !== "Continue")
        return;
    }
    let quantization = parsed.quantization;
    if (!quantization && details.quantizations.length > 0) {
      const options = details.quantizations.map((entry) => {
        const detail = [
          entry.size === void 0 ? void 0 : formatBytes(entry.size),
          entry.name === "Q4_K_M" ? "recommended" : void 0
        ].filter((value) => Boolean(value)).join(" \xB7 ");
        return detail ? `${entry.name} \xB7 ${detail}` : entry.name;
      });
      const choice = await ui.select(`Select quantization
${details.id}`, options);
      if (!choice)
        return;
      quantization = details.quantizations[options.indexOf(choice)]?.name;
      if (!quantization)
        return;
    }
    const model = quantization ? `${details.id}:${quantization}` : details.id;
    const result = await runWithProgress(ui, {
      title: "Downloading model",
      model,
      initialMessage: "Starting\u2026",
      cancelTitle: "Stop download?",
      cancelMessage: model,
      run: /* @__PURE__ */ __name((signal, update) => client.downloadAndWait(model, update, signal), "run"),
      cancel: /* @__PURE__ */ __name(() => client.unload(model), "cancel")
    });
    if (result.cancelled)
      return;
    await syncCatalog(ctx, client, result.value);
    ctx.ui.notify(`Downloaded ${model}`);
  }, "downloadModel");
  pi.registerCommand("llama", {
    description: "Manage llama.cpp router models",
    handler: /* @__PURE__ */ __name(async (_args, ctx) => {
      if (ctx.mode !== "tui") {
        ctx.ui.notify("/llama is available in interactive mode", "warning");
        return;
      }
      const client = await configuredClient(ctx);
      if (!client)
        return;
      await showLlamaUi(ctx, async (ui) => {
        const readCatalog = /* @__PURE__ */ __name(async () => {
          while (true) {
            try {
              return await syncCatalog(ctx, client);
            } catch (error) {
              if (await ui.connectionError(client.serverUrl, connectionErrorMessage(error)) === "close") {
                return void 0;
              }
            }
          }
        }, "readCatalog");
        let catalog = await readCatalog();
        if (!catalog)
          return;
        while (true) {
          const action = await ui.showModels(client.serverUrl, catalog);
          if (action.type === "close")
            return;
          let actionError;
          try {
            if (action.type === "download")
              await downloadModel(ctx, ui, client);
            else if (modelIsLoaded(action.model))
              await unloadModel(ctx, ui, client, action.model);
            else if (action.model.status.value === "unloaded")
              await loadModel(ctx, ui, client, catalog, action.model);
            else
              ctx.ui.notify(`${action.model.id} is ${action.model.status.value}`, "warning");
          } catch (error) {
            actionError = error;
          }
          const refreshed = await readCatalog();
          if (!refreshed)
            return;
          catalog = refreshed;
          if (actionError && !isConnectionError(actionError)) {
            ctx.ui.notify(actionError instanceof Error ? actionError.message : String(actionError), "error");
          }
        }
      });
    }, "handler")
  });
}
__name(llamaExtension, "llamaExtension");

// pi-dist/pi-coding-agent/extensions/index.js
var builtInExtensions = [
  { name: "llama.cpp", factory: llamaExtension, builtin: true },
  // Replaceable: an extension that registers `codemode`, `tool_search`, or `/mcp` (such as a third-party
  // MCP extension) takes over instead of running alongside the built-in one.
  { name: "codemode", factory: codemode_default, replaceable: true, builtin: true },
  { name: "tool-search", factory: tool_search_default, replaceable: true, builtin: true },
  { name: "mcp", factory: mcp_default, replaceable: true, builtin: true }
];

// pi-dist/pi-coding-agent/extensions/mcp/cli.lazy.js
var loadMcpCommand = /* @__PURE__ */ __name(() => import("./chunk-ONCXY5DA.js"), "loadMcpCommand");

// pi-dist/pi-coding-agent/migrations.js
import chalk4 from "../../../chalk/source/index.js";
import { existsSync as existsSync4, mkdirSync, readdirSync, readFileSync as readFileSync3, renameSync, rmSync, writeFileSync as writeFileSync2 } from "fs";
import { dirname, join as join5 } from "path";
var MIGRATION_GUIDE_URL = "https://github.com/earendil-works/pi/blob/main/packages/coding-agent/CHANGELOG.md#extensions-migration";
var EXTENSIONS_DOC_URL = "https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/extensions.md";
function migrateAuthToAuthJson() {
  const agentDir = getAgentDir();
  const authPath = join5(agentDir, "auth.json");
  const oauthPath = join5(agentDir, "oauth.json");
  const settingsPath = join5(agentDir, "settings.json");
  if (existsSync4(authPath))
    return [];
  const migrated = {};
  const providers = [];
  if (existsSync4(oauthPath)) {
    try {
      const oauth = JSON.parse(stripBom(readFileSync3(oauthPath, "utf-8")));
      for (const [provider, cred] of Object.entries(oauth)) {
        migrated[provider] = { type: "oauth", ...cred };
        providers.push(provider);
      }
      renameSync(oauthPath, `${oauthPath}.migrated`);
    } catch {
    }
  }
  if (existsSync4(settingsPath)) {
    try {
      const content = readFileSync3(settingsPath, "utf-8");
      const settings = JSON.parse(stripBom(content));
      if (settings.apiKeys && typeof settings.apiKeys === "object") {
        for (const [provider, key] of Object.entries(settings.apiKeys)) {
          if (!migrated[provider] && typeof key === "string") {
            migrated[provider] = { type: "api_key", key };
            providers.push(provider);
          }
        }
        delete settings.apiKeys;
        writeFileSync2(settingsPath, JSON.stringify(settings, null, 2));
      }
    } catch {
    }
  }
  if (Object.keys(migrated).length > 0) {
    mkdirSync(dirname(authPath), { recursive: true });
    writeFileSync2(authPath, JSON.stringify(migrated, null, 2), { mode: 384 });
  }
  return providers;
}
__name(migrateAuthToAuthJson, "migrateAuthToAuthJson");
function migrateSessionsFromAgentRoot() {
  const agentDir = getAgentDir();
  let files;
  try {
    files = readdirSync(agentDir).filter((f) => f.endsWith(".jsonl")).map((f) => join5(agentDir, f));
  } catch {
    return;
  }
  if (files.length === 0)
    return;
  for (const file of files) {
    try {
      const content = readFileSync3(file, "utf8");
      const firstLine3 = content.split("\n")[0];
      if (!firstLine3?.trim())
        continue;
      const header = JSON.parse(firstLine3);
      if (header.type !== "session" || !header.cwd)
        continue;
      const cwd = header.cwd;
      const safePath = `--${cwd.replace(/^[/\\]/, "").replace(/[/\\:]/g, "-")}--`;
      const correctDir = join5(agentDir, "sessions", safePath);
      if (!existsSync4(correctDir)) {
        mkdirSync(correctDir, { recursive: true });
      }
      const fileName = file.split("/").pop() || file.split("\\").pop();
      const newPath = join5(correctDir, fileName);
      if (existsSync4(newPath))
        continue;
      renameSync(file, newPath);
    } catch {
    }
  }
}
__name(migrateSessionsFromAgentRoot, "migrateSessionsFromAgentRoot");
function migrateCommandsToPrompts(baseDir, label) {
  const commandsDir = join5(baseDir, "commands");
  const promptsDir = join5(baseDir, "prompts");
  if (existsSync4(commandsDir) && !existsSync4(promptsDir)) {
    try {
      renameSync(commandsDir, promptsDir);
      console.log(chalk4.green(`Migrated ${label} commands/ \u2192 prompts/`));
      return true;
    } catch (err) {
      console.log(chalk4.yellow(`Warning: Could not migrate ${label} commands/ to prompts/: ${err instanceof Error ? err.message : err}`));
    }
  }
  return false;
}
__name(migrateCommandsToPrompts, "migrateCommandsToPrompts");
function migrateKeybindingsConfigFile() {
  const configPath = join5(getAgentDir(), "keybindings.json");
  if (!existsSync4(configPath))
    return;
  try {
    const parsed = JSON.parse(stripBom(readFileSync3(configPath, "utf-8")));
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
      return;
    }
    const { config, migrated } = migrateKeybindingsConfig(parsed);
    if (!migrated)
      return;
    writeFileSync2(configPath, `${JSON.stringify(config, null, 2)}
`, "utf-8");
  } catch {
  }
}
__name(migrateKeybindingsConfigFile, "migrateKeybindingsConfigFile");
function migrateToolsToBin() {
  const agentDir = getAgentDir();
  const toolsDir = join5(agentDir, "tools");
  const binDir = getBinDir();
  if (!existsSync4(toolsDir))
    return;
  const binaries = ["fd", "rg", "fd.exe", "rg.exe"];
  let movedAny = false;
  for (const bin of binaries) {
    const oldPath = join5(toolsDir, bin);
    const newPath = join5(binDir, bin);
    if (existsSync4(oldPath)) {
      if (!existsSync4(binDir)) {
        mkdirSync(binDir, { recursive: true });
      }
      if (!existsSync4(newPath)) {
        try {
          renameSync(oldPath, newPath);
          movedAny = true;
        } catch {
        }
      } else {
        try {
          rmSync?.(oldPath, { force: true });
        } catch {
        }
      }
    }
  }
  if (movedAny) {
    console.log(chalk4.green(`Migrated managed binaries tools/ \u2192 bin/`));
  }
}
__name(migrateToolsToBin, "migrateToolsToBin");
function checkDeprecatedExtensionDirs(baseDir, label) {
  const hooksDir = join5(baseDir, "hooks");
  const toolsDir = join5(baseDir, "tools");
  const warnings = [];
  if (existsSync4(hooksDir)) {
    warnings.push(`${label} hooks/ directory found. Hooks have been renamed to extensions.`);
  }
  if (existsSync4(toolsDir)) {
    try {
      const entries = readdirSync(toolsDir);
      const customTools = entries.filter((e) => {
        const lower = e.toLowerCase();
        return lower !== "fd" && lower !== "rg" && lower !== "fd.exe" && lower !== "rg.exe" && !e.startsWith(".");
      });
      if (customTools.length > 0) {
        warnings.push(`${label} tools/ directory contains custom tools. Custom tools have been merged into extensions.`);
      }
    } catch {
    }
  }
  return warnings;
}
__name(checkDeprecatedExtensionDirs, "checkDeprecatedExtensionDirs");
function migrateExtensionSystem(cwd) {
  const agentDir = getAgentDir();
  const projectDir = join5(cwd, CONFIG_DIR_NAME);
  migrateCommandsToPrompts(agentDir, "Global");
  migrateCommandsToPrompts(projectDir, "Project");
  const warnings = [
    ...checkDeprecatedExtensionDirs(agentDir, "Global"),
    ...checkDeprecatedExtensionDirs(projectDir, "Project")
  ];
  return warnings;
}
__name(migrateExtensionSystem, "migrateExtensionSystem");
async function showDeprecationWarnings(warnings) {
  if (warnings.length === 0)
    return;
  for (const warning of warnings) {
    console.log(chalk4.yellow(`Warning: ${warning}`));
  }
  console.log(chalk4.yellow(`
Move your extensions to the extensions/ directory.`));
  console.log(chalk4.yellow(`Migration guide: ${MIGRATION_GUIDE_URL}`));
  console.log(chalk4.yellow(`Documentation: ${EXTENSIONS_DOC_URL}`));
  console.log(chalk4.dim(`
Press any key to continue...`));
  await new Promise((resolve7) => {
    process.stdin.setRawMode?.(true);
    process.stdin.resume();
    process.stdin.once("data", () => {
      process.stdin.setRawMode?.(false);
      process.stdin.pause();
      resolve7();
    });
  });
  console.log();
}
__name(showDeprecationWarnings, "showDeprecationWarnings");
function runMigrations(cwd) {
  const migratedAuthProviders = migrateAuthToAuthJson();
  migrateSessionsFromAgentRoot();
  migrateToolsToBin();
  migrateKeybindingsConfigFile();
  const deprecationWarnings = migrateExtensionSystem(cwd);
  return { migratedAuthProviders, deprecationWarnings };
}
__name(runMigrations, "runMigrations");

// pi-dist/pi-coding-agent/modes/interactive/interactive-mode.js
import * as crypto2 from "node:crypto";
import * as fs3 from "node:fs";
import * as os3 from "node:os";
import * as path4 from "node:path";
import { isRetryableAssistantError } from "../../pi-ai/sdk-bundle/compat.js";
import * as TuiLayouts from "../../../pi-tui.mjs";
import { CombinedAutocompleteProvider, Container as Container30, fuzzyFilter as fuzzyFilter8, getCapabilities as getCapabilities3, hyperlink as hyperlink3, Markdown as Markdown7, matchesKey as matchesKey2, Spacer as Spacer27, setCapabilityOverrides as setCapabilityOverrides2, setKeybindings as setKeybindings3, Text as Text30, TruncatedText as TruncatedText2, TuiAltScreen as TuiAltScreen3, TuiMainScreen as TuiMainScreen3, visibleWidth as visibleWidth7 } from "../../../pi-tui.mjs";
import chalk5 from "../../../chalk/source/index.js";
import { spawn as spawn4 } from "child_process";

// pi-dist/pi-coding-agent/core/cache-stats.js
var CACHE_TTL_MS = 5 * 60 * 1e3;
var NOISE_FLOOR_TOKENS = 1024;
function detectMiss(prev, message, models) {
  const usage = message.usage;
  const promptTokens = usage.input + usage.cacheRead + usage.cacheWrite;
  if (!prev || promptTokens <= 0 || usage.cacheRead + usage.cacheWrite === 0 && !prev.reportedCache) {
    return void 0;
  }
  const missedTokens = Math.min(prev.promptTokens, promptTokens) - usage.cacheRead;
  if (missedTokens <= NOISE_FLOOR_TOKENS)
    return void 0;
  const paidTokens = usage.input + usage.cacheWrite;
  const paidPerToken = paidTokens > 0 ? (usage.cost.input + usage.cost.cacheWrite) / paidTokens : 0;
  const readPerToken = usage.cacheRead > 0 ? usage.cost.cacheRead / usage.cacheRead : (models.getModel(message.provider, message.model)?.cost.cacheRead ?? 0) / 1e6;
  return {
    missedTokens,
    missedCost: missedTokens * Math.max(0, paidPerToken - readPerToken),
    idleMs: Math.max(0, message.timestamp - prev.timestamp),
    modelChanged: `${message.provider}/${message.model}` !== prev.modelKey
  };
}
__name(detectMiss, "detectMiss");
function asPreviousRequest(message, reportedCache) {
  const usage = message.usage;
  const promptTokens = usage.input + usage.cacheRead + usage.cacheWrite;
  if (promptTokens <= 0)
    return void 0;
  return {
    promptTokens,
    modelKey: `${message.provider}/${message.model}`,
    timestamp: message.timestamp,
    reportedCache: reportedCache || usage.cacheRead + usage.cacheWrite > 0
  };
}
__name(asPreviousRequest, "asPreviousRequest");
function scan(entries, models) {
  let prev;
  const totals = { missedTokens: 0, missedCost: 0, missCount: 0 };
  const misses = /* @__PURE__ */ new Map();
  for (const entry of entries) {
    if (entry.type === "compaction" || entry.type === "branch_summary") {
      prev = void 0;
      continue;
    }
    if (entry.type === "usage" && entry.kind === "cache_warm") {
      const promptTokens = entry.usage.input + entry.usage.cacheRead + entry.usage.cacheWrite;
      if (promptTokens > 0) {
        prev = {
          promptTokens,
          modelKey: `${entry.provider}/${entry.model}`,
          timestamp: Date.parse(entry.timestamp),
          reportedCache: true
        };
      }
    } else if (entry.type === "message" && entry.message.role === "assistant") {
      const miss = detectMiss(prev, entry.message, models);
      if (miss) {
        totals.missedTokens += miss.missedTokens;
        totals.missedCost += miss.missedCost;
        totals.missCount += 1;
        misses.set(entry.message, miss);
      }
      prev = asPreviousRequest(entry.message, prev?.reportedCache ?? false) ?? prev;
    }
  }
  return { prev, totals, misses };
}
__name(scan, "scan");
function computeCacheWaste(entries, models) {
  return scan(entries, models).totals;
}
__name(computeCacheWaste, "computeCacheWaste");
function collectCacheMisses(entries, models) {
  return scan(entries, models).misses;
}
__name(collectCacheMisses, "collectCacheMisses");
function detectCacheMiss(entries, message, models) {
  return detectMiss(scan(entries, models).prev, message, models);
}
__name(detectCacheMiss, "detectCacheMiss");

// pi-dist/pi-coding-agent/core/crash-log.js
import { mkdirSync as mkdirSync2, readFileSync as readFileSync4, rmSync as rmSync2, writeFileSync as writeFileSync3 } from "node:fs";
import { dirname as dirname2, join as join6 } from "node:path";
var MAX_CRASH_RECORDS = 5;
var MAX_AGE = 7 * 24 * 60 * 60 * 1e3;
function crashLogPath(agentDir = getAgentDir()) {
  return join6(agentDir, "crashes.json");
}
__name(crashLogPath, "crashLogPath");
function readCrashLog(path5 = crashLogPath()) {
  try {
    const records = JSON.parse(readFileSync4(path5, "utf8"));
    return Array.isArray(records) ? records.filter((record) => typeof record === "object" && record !== null && typeof record.timestamp === "string" && typeof record.message === "string") : [];
  } catch {
    return [];
  }
}
__name(readCrashLog, "readCrashLog");
function writeCrashLog(records, path5) {
  mkdirSync2(dirname2(path5), { recursive: true });
  writeFileSync3(path5, `${JSON.stringify(records, null, 2)}
`);
}
__name(writeCrashLog, "writeCrashLog");
function normalizeStackPath(value) {
  return value.replace(/\\/g, "/").replace(/\/+$/u, "");
}
__name(normalizeStackPath, "normalizeStackPath");
function stackContainsPath(stack, targetPath, includeDescendants) {
  const target = normalizeStackPath(targetPath);
  if (!target || isSyntheticPath(target))
    return false;
  const caseInsensitive = /^[a-z]:\//iu.test(target);
  const haystack = caseInsensitive ? stack.toLowerCase() : stack;
  const needle = caseInsensitive ? target.toLowerCase() : target;
  if (includeDescendants)
    return haystack.includes(`${needle}/`);
  let index = haystack.indexOf(needle);
  while (index !== -1) {
    const next = haystack[index + needle.length];
    if (next === void 0 || next === ":" || next === ")" || /\s/u.test(next))
      return true;
    index = haystack.indexOf(needle, index + needle.length);
  }
  return false;
}
__name(stackContainsPath, "stackContainsPath");
function findExtensionStackMatches(stack, extensions) {
  if (!stack)
    return [];
  const normalizedStack = stack.split("\n").slice(1).filter((line) => /^\s+at\s/u.test(line)).map((line) => {
    try {
      return decodeURI(line);
    } catch {
      return line;
    }
  }).join("\n").replace(/\\/g, "/");
  const matches = [];
  const seen = /* @__PURE__ */ new Set();
  for (const extension of extensions) {
    const resolvedPath = normalizeStackPath(extension.resolvedPath);
    const singleFilePackage = extension.sourceInfo.origin === "package" && !/^(?:npm:|git:|https?:\/\/|ssh:\/\/)/u.test(extension.sourceInfo.source) && /\.[cm]?[jt]s$/u.test(extension.sourceInfo.source);
    const packageRoot = extension.sourceInfo.origin === "package" && !singleFilePackage && extension.sourceInfo.baseDir ? extension.sourceInfo.baseDir : void 0;
    const slashIndex = resolvedPath.lastIndexOf("/");
    const directoryEntry = /\/index\.[cm]?[jt]s$/u.test(resolvedPath);
    const matched = packageRoot ? stackContainsPath(normalizedStack, packageRoot, true) : directoryEntry && slashIndex !== -1 ? stackContainsPath(normalizedStack, resolvedPath.slice(0, slashIndex), true) : stackContainsPath(normalizedStack, resolvedPath, false);
    if (!matched)
      continue;
    const label = extension.sourceInfo.origin === "package" && extension.sourceInfo.source ? extension.sourceInfo.source : extension.path;
    if (!seen.has(label)) {
      seen.add(label);
      matches.push(label);
    }
  }
  return matches;
}
__name(findExtensionStackMatches, "findExtensionStackMatches");
function recordCrash(crash, path5 = crashLogPath()) {
  try {
    const { error } = crash;
    const record = {
      timestamp: (/* @__PURE__ */ new Date()).toISOString(),
      version: VERSION,
      kind: crash.kind,
      message: error instanceof Error ? error.message || error.name : String(error),
      stack: error instanceof Error && error.stack ? error.stack : null,
      sessionFile: crash.sessionFile ?? null,
      cwd: crash.cwd
    };
    writeCrashLog([...readCrashLog(path5), record].slice(-MAX_CRASH_RECORDS), path5);
    return record;
  } catch {
    return void 0;
  }
}
__name(recordCrash, "recordCrash");
function takeUnnotifiedCrash(path5 = crashLogPath(), now = Date.now()) {
  const records = readCrashLog(path5);
  const crash = [...records].reverse().find((record) => !record.notified && now - Date.parse(record.timestamp) <= MAX_AGE);
  if (!crash)
    return void 0;
  try {
    writeCrashLog(records.map((record) => record.notified ? record : { ...record, notified: true }), path5);
  } catch {
  }
  return crash;
}
__name(takeUnnotifiedCrash, "takeUnnotifiedCrash");
function clearCrashLog(path5 = crashLogPath()) {
  try {
    rmSync2(path5, { force: true });
  } catch {
  }
}
__name(clearCrashLog, "clearCrashLog");

// pi-dist/pi-coding-agent/core/radius.js
import { DEFAULT_RADIUS_GATEWAY, normalizeRadiusGatewayUrl } from "../../pi-ai/providers/radius-config.js";
var RADIUS_PROVIDER_ID = "radius";
var ENV_RADIUS_GATEWAY = "PI_RADIUS_GATEWAY";
var RADIUS_MCP_URL = `${normalizeRadiusGatewayUrl(DEFAULT_RADIUS_GATEWAY)}/mcp`;
function getRadiusGatewayUrl() {
  return normalizeRadiusGatewayUrl(process.env[ENV_RADIUS_GATEWAY] ?? DEFAULT_RADIUS_GATEWAY);
}
__name(getRadiusGatewayUrl, "getRadiusGatewayUrl");

// pi-dist/pi-coding-agent/core/slash-commands.js
var BUILTIN_SLASH_COMMANDS = [
  { name: "settings", description: "Open settings menu" },
  { name: "model", description: "Select model (opens selector UI)", argumentHint: "<provider/model>" },
  { name: "tree", description: "Navigate session tree (switch branches)" },
  { name: "thinking", description: "Set thinking level", argumentHint: "<level>" },
  { name: "scoped-models", description: "Enable/disable models for Ctrl+P cycling" },
  { name: "export", description: "Export session (HTML default, or specify path: .html/.jsonl)" },
  { name: "import", description: "Import and resume a session from a JSONL file" },
  { name: "share", description: "Share session as a secret GitHub gist" },
  { name: "bug", description: "Report a bug to the Pi developers", argumentHint: "<description>" },
  { name: "copy", description: "Copy last agent message to clipboard" },
  { name: "name", description: "Set session display name" },
  { name: "session", description: "Show session info and stats" },
  { name: "changelog", description: "Show changelog entries" },
  { name: "hotkeys", description: "Show all keyboard shortcuts" },
  { name: "fork", description: "Create a new fork from a previous user message" },
  { name: "clone", description: "Duplicate the current session at the current position" },
  { name: "trust", description: "Save project trust decision for future sessions" },
  { name: "login", description: "Configure provider authentication", argumentHint: "<provider>" },
  { name: "logout", description: "Remove provider authentication" },
  { name: "new", description: "Start a new session" },
  { name: "compact", description: "Manually compact the session context" },
  { name: "resume", description: "Resume a different session" },
  { name: "reload", description: "Reload keybindings, extensions, skills, prompts, themes, and context files" },
  { name: "quit", description: `Quit ${APP_NAME}` }
];

// pi-dist/pi-coding-agent/core/tools/renderers/index.js
function createAllToolRenderers() {
  return {
    read: readRenderers,
    bash: createShellRenderers("$"),
    powershell: createShellRenderers("PS>"),
    edit: editRenderers,
    write: writeRenderers,
    grep: grepRenderers,
    find: findRenderers,
    ls: lsRenderers
  };
}
__name(createAllToolRenderers, "createAllToolRenderers");
function withBuiltInRenderers(toolName, definition) {
  const builtIn = createAllToolRenderers()[toolName];
  if (!definition)
    return builtIn;
  if (!builtIn)
    return definition;
  return {
    ...definition,
    renderCall: definition.renderCall ?? builtIn.renderCall,
    renderResult: definition.renderResult ?? builtIn.renderResult
  };
}
__name(withBuiltInRenderers, "withBuiltInRenderers");

// pi-dist/pi-coding-agent/utils/changelog.js
import path from "node:path";
import { existsSync as existsSync5, readFileSync as readFileSync5 } from "fs";
var GITHUB_REPO = "earendil-works/pi";
var CHANGELOG_LINK_BASE_PATH = "packages/coding-agent";
var LEGACY_REPO_RE = /^https:\/\/github\.com\/(?:badlogic|earendil-works)\/pi-mono(?=\/|$)/;
var URL_SCHEME_RE = /^[a-z][a-z0-9+.-]*:/i;
var INLINE_MARKDOWN_LINK_RE = /(!?\[[^\]\n]+\]\()([^\s)]+)((?:\s+[^)]*)?\))/g;
function entryVersion(entry) {
  return `${entry.major}.${entry.minor}.${entry.patch}`;
}
__name(entryVersion, "entryVersion");
function normalizeTag(version) {
  const versionString = typeof version === "string" ? version : entryVersion(version);
  return versionString.startsWith("v") ? versionString : `v${versionString}`;
}
__name(normalizeTag, "normalizeTag");
function splitLocalTarget(target) {
  const hashIndex = target.indexOf("#");
  const beforeHash = hashIndex === -1 ? target : target.slice(0, hashIndex);
  const fragment = hashIndex === -1 ? "" : target.slice(hashIndex);
  const queryIndex = beforeHash.indexOf("?");
  if (queryIndex === -1) {
    return { fragment, pathPart: beforeHash, query: "" };
  }
  return {
    fragment,
    pathPart: beforeHash.slice(0, queryIndex),
    query: beforeHash.slice(queryIndex)
  };
}
__name(splitLocalTarget, "splitLocalTarget");
function normalizePathPart(value) {
  return value.replaceAll("\\", "/");
}
__name(normalizePathPart, "normalizePathPart");
function resolveRepositoryPath(targetPath) {
  const normalizedTarget = normalizePathPart(targetPath);
  const joined = normalizedTarget.startsWith("/") ? path.posix.normalize(normalizedTarget.replace(/^\/+/, "")) : path.posix.normalize(path.posix.join(CHANGELOG_LINK_BASE_PATH, normalizedTarget));
  if (joined === "." || joined.startsWith("../") || joined === "..") {
    return void 0;
  }
  return joined;
}
__name(resolveRepositoryPath, "resolveRepositoryPath");
function isDirectoryTarget(originalPath, repositoryPath) {
  if (originalPath.endsWith("/")) {
    return true;
  }
  const basename4 = path.posix.basename(repositoryPath);
  return !basename4.includes(".");
}
__name(isDirectoryTarget, "isDirectoryTarget");
function normalizeChangelogLinkTarget(target, tag) {
  let canonicalTarget = target.replace(LEGACY_REPO_RE, `https://github.com/${GITHUB_REPO}`);
  const repoUrl = `https://github.com/${GITHUB_REPO}`;
  for (const route2 of ["blob", "tree"]) {
    for (const branch of ["main", "master"]) {
      const floatingRefPrefix = `${repoUrl}/${route2}/${branch}/`;
      if (canonicalTarget.startsWith(floatingRefPrefix)) {
        canonicalTarget = `${repoUrl}/${route2}/${tag}/${canonicalTarget.slice(floatingRefPrefix.length)}`;
      }
    }
  }
  if (canonicalTarget.startsWith("#") || canonicalTarget.startsWith("//") || URL_SCHEME_RE.test(canonicalTarget)) {
    return canonicalTarget;
  }
  const { fragment, pathPart, query } = splitLocalTarget(canonicalTarget);
  if (!pathPart) {
    return canonicalTarget;
  }
  const repositoryPath = resolveRepositoryPath(pathPart);
  if (!repositoryPath) {
    return canonicalTarget;
  }
  const route = isDirectoryTarget(pathPart, repositoryPath) ? "tree" : "blob";
  return `https://github.com/${GITHUB_REPO}/${route}/${tag}/${encodeURI(repositoryPath)}${query}${fragment}`;
}
__name(normalizeChangelogLinkTarget, "normalizeChangelogLinkTarget");
function normalizeChangelogLinks(markdown, version) {
  const tag = normalizeTag(version);
  return markdown.replace(INLINE_MARKDOWN_LINK_RE, (_match, prefix, target, suffix) => {
    return `${prefix}${normalizeChangelogLinkTarget(target, tag)}${suffix}`;
  });
}
__name(normalizeChangelogLinks, "normalizeChangelogLinks");
function parseChangelog(changelogPath) {
  if (!existsSync5(changelogPath)) {
    return [];
  }
  try {
    const content = readFileSync5(changelogPath, "utf-8");
    const lines = content.split("\n");
    const entries = [];
    let currentLines = [];
    let currentVersion = null;
    for (const line of lines) {
      if (line.startsWith("## ")) {
        if (currentVersion && currentLines.length > 0) {
          entries.push({
            ...currentVersion,
            content: currentLines.join("\n").trim()
          });
        }
        const versionMatch = line.match(/##\s+\[?(\d+)\.(\d+)\.(\d+)\]?/);
        if (versionMatch) {
          currentVersion = {
            major: Number.parseInt(versionMatch[1], 10),
            minor: Number.parseInt(versionMatch[2], 10),
            patch: Number.parseInt(versionMatch[3], 10)
          };
          currentLines = [line];
        } else {
          currentVersion = null;
          currentLines = [];
        }
      } else if (currentVersion) {
        currentLines.push(line);
      }
    }
    if (currentVersion && currentLines.length > 0) {
      entries.push({
        ...currentVersion,
        content: currentLines.join("\n").trim()
      });
    }
    return entries;
  } catch (error) {
    console.error(`Warning: Could not parse changelog: ${error}`);
    return [];
  }
}
__name(parseChangelog, "parseChangelog");
function compareVersions(v1, v2) {
  if (v1.major !== v2.major)
    return v1.major - v2.major;
  if (v1.minor !== v2.minor)
    return v1.minor - v2.minor;
  return v1.patch - v2.patch;
}
__name(compareVersions, "compareVersions");
function getNewEntries(entries, lastVersion) {
  const parts = lastVersion.split(".").map(Number);
  const last = {
    major: parts[0] || 0,
    minor: parts[1] || 0,
    patch: parts[2] || 0,
    content: ""
  };
  return entries.filter((entry) => compareVersions(entry, last) > 0);
}
__name(getNewEntries, "getNewEntries");

// pi-dist/pi-coding-agent/utils/clipboard-image.js
import { getNativeClipboard as getNativeClipboard2 } from "../../../pi-tui.mjs";
import { randomUUID as randomUUID2 } from "crypto";
import { readFileSync as readFileSync6, unlinkSync as unlinkSync2 } from "fs";
import { tmpdir as tmpdir2 } from "os";
import { join as join7 } from "path";
var SUPPORTED_IMAGE_MIME_TYPES = ["image/png", "image/jpeg", "image/webp", "image/gif"];
var DEFAULT_LIST_TIMEOUT_MS = 1e3;
var DEFAULT_POWERSHELL_TIMEOUT_MS = 5e3;
function isWaylandSession(env = process.env) {
  return Boolean(env.WAYLAND_DISPLAY) || env.XDG_SESSION_TYPE === "wayland";
}
__name(isWaylandSession, "isWaylandSession");
function baseMimeType(mimeType) {
  return mimeType.split(";")[0]?.trim().toLowerCase() ?? mimeType.toLowerCase();
}
__name(baseMimeType, "baseMimeType");
function extensionForImageMimeType(mimeType) {
  switch (baseMimeType(mimeType)) {
    case "image/png":
      return "png";
    case "image/jpeg":
      return "jpg";
    case "image/webp":
      return "webp";
    case "image/gif":
      return "gif";
    default:
      return null;
  }
}
__name(extensionForImageMimeType, "extensionForImageMimeType");
function selectPreferredImageMimeType(mimeTypes) {
  const normalized = mimeTypes.map((t) => t.trim()).filter(Boolean).map((t) => ({ raw: t, base: baseMimeType(t) }));
  for (const preferred of SUPPORTED_IMAGE_MIME_TYPES) {
    const match = normalized.find((t) => t.base === preferred);
    if (match) {
      return match.raw;
    }
  }
  const anyImage = normalized.find((t) => t.base.startsWith("image/"));
  return anyImage?.raw ?? null;
}
__name(selectPreferredImageMimeType, "selectPreferredImageMimeType");
function isSupportedImageMimeType(mimeType) {
  const base = baseMimeType(mimeType);
  return SUPPORTED_IMAGE_MIME_TYPES.some((t) => t === base);
}
__name(isSupportedImageMimeType, "isSupportedImageMimeType");
async function convertToPng2(bytes) {
  const photon = await loadPhoton();
  if (!photon) {
    return null;
  }
  try {
    const image = photon.PhotonImage.new_from_byteslice(bytes);
    try {
      return image.get_bytes();
    } finally {
      image.free();
    }
  } catch {
    return null;
  }
}
__name(convertToPng2, "convertToPng");
async function readClipboardImageViaWlPaste() {
  const list = await runClipboardCommand("wl-paste", ["--list-types"], { timeoutMs: DEFAULT_LIST_TIMEOUT_MS });
  if (list === void 0)
    return void 0;
  const types = list.toString("utf-8").split(/\r?\n/).map((t) => t.trim()).filter(Boolean);
  const selectedType = selectPreferredImageMimeType(types);
  if (!selectedType) {
    return null;
  }
  const data = await runClipboardCommand("wl-paste", ["--type", selectedType, "--no-newline"]);
  if (data === void 0)
    return void 0;
  if (data.length === 0)
    return null;
  return { bytes: data, mimeType: baseMimeType(selectedType) };
}
__name(readClipboardImageViaWlPaste, "readClipboardImageViaWlPaste");
async function readClipboardImageViaPowerShell() {
  const tmpFile = join7(tmpdir2(), `pi-wsl-clip-${randomUUID2()}.png`);
  try {
    const winPathResult = await runClipboardCommand("wslpath", ["-w", tmpFile], {
      timeoutMs: DEFAULT_LIST_TIMEOUT_MS
    });
    if (winPathResult === void 0) {
      return null;
    }
    const winPath = winPathResult.toString("utf-8").trim();
    if (!winPath) {
      return null;
    }
    const psQuotedWinPath = winPath.replaceAll("'", "''");
    const psScript = [
      "Add-Type -AssemblyName System.Windows.Forms",
      "Add-Type -AssemblyName System.Drawing",
      `$path = '${psQuotedWinPath}'`,
      "$img = [System.Windows.Forms.Clipboard]::GetImage()",
      "if ($img) { $img.Save($path, [System.Drawing.Imaging.ImageFormat]::Png); Write-Output 'ok' } else { Write-Output 'empty' }"
    ].join("; ");
    const result = await runClipboardCommand("powershell.exe", ["-NoProfile", "-Command", psScript], {
      timeoutMs: DEFAULT_POWERSHELL_TIMEOUT_MS
    });
    if (result === void 0) {
      return null;
    }
    const output = result.toString("utf-8").trim();
    if (output !== "ok") {
      return null;
    }
    const bytes = readFileSync6(tmpFile);
    if (bytes.length === 0) {
      return null;
    }
    return { bytes: new Uint8Array(bytes), mimeType: "image/png" };
  } catch {
    return null;
  } finally {
    try {
      unlinkSync2(tmpFile);
    } catch {
    }
  }
}
__name(readClipboardImageViaPowerShell, "readClipboardImageViaPowerShell");
async function readClipboardImageViaXclip() {
  const targets = await runClipboardCommand("xclip", ["-selection", "clipboard", "-t", "TARGETS", "-o"], {
    timeoutMs: DEFAULT_LIST_TIMEOUT_MS
  });
  if (targets === void 0)
    return void 0;
  const candidateTypes = targets.toString("utf-8").split(/\r?\n/).map((t) => t.trim()).filter(Boolean);
  const preferred = selectPreferredImageMimeType(candidateTypes);
  if (!preferred)
    return null;
  const data = await runClipboardCommand("xclip", ["-selection", "clipboard", "-t", preferred, "-o"]);
  if (data === void 0)
    return void 0;
  if (data.length === 0)
    return null;
  return { bytes: data, mimeType: baseMimeType(preferred) };
}
__name(readClipboardImageViaXclip, "readClipboardImageViaXclip");
async function readClipboardImageViaNativeClipboard() {
  const bytes = await getNativeClipboard2()?.getImage();
  if (bytes === void 0)
    return void 0;
  if (!bytes?.length)
    return null;
  return { bytes, mimeType: detectSupportedImageMimeType(bytes) ?? "application/octet-stream" };
}
__name(readClipboardImageViaNativeClipboard, "readClipboardImageViaNativeClipboard");
async function readClipboardImage(options) {
  const env = options?.env ?? process.env;
  const platform2 = options?.platform ?? process.platform;
  if (env.TERMUX_VERSION) {
    return null;
  }
  let image;
  if (platform2 === "linux") {
    const wsl = isWSL(env);
    if (isWaylandSession(env) || wsl) {
      image = await readClipboardImageViaWlPaste();
    }
    if (image === void 0)
      image = await readClipboardImageViaXclip();
    if (!image && wsl)
      image = await readClipboardImageViaPowerShell() ?? image;
    if (image === void 0)
      image = await readClipboardImageViaNativeClipboard();
  } else {
    image = await readClipboardImageViaNativeClipboard();
  }
  if (!image) {
    return null;
  }
  if (!isSupportedImageMimeType(image.mimeType)) {
    const pngBytes = await convertToPng2(image.bytes);
    if (!pngBytes) {
      return null;
    }
    return { bytes: pngBytes, mimeType: "image/png" };
  }
  return image;
}
__name(readClipboardImage, "readClipboardImage");

// pi-dist/pi-coding-agent/utils/syntax-highlight.js
import hljs from "../../../highlight.js/lib/core.js";
import bash from "../../../highlight.js/lib/languages/bash.js";
import c from "../../../highlight.js/lib/languages/c.js";
import cpp from "../../../highlight.js/lib/languages/cpp.js";
import csharp from "../../../highlight.js/lib/languages/csharp.js";
import dart from "../../../highlight.js/lib/languages/dart.js";
import go from "../../../highlight.js/lib/languages/go.js";
import groovy from "../../../highlight.js/lib/languages/groovy.js";
import java from "../../../highlight.js/lib/languages/java.js";
import javascript from "../../../highlight.js/lib/languages/javascript.js";
import json from "../../../highlight.js/lib/languages/json.js";
import kotlin from "../../../highlight.js/lib/languages/kotlin.js";
import lua from "../../../highlight.js/lib/languages/lua.js";
import nix from "../../../highlight.js/lib/languages/nix.js";
import perl from "../../../highlight.js/lib/languages/perl.js";
import php from "../../../highlight.js/lib/languages/php.js";
import python from "../../../highlight.js/lib/languages/python.js";
import ruby from "../../../highlight.js/lib/languages/ruby.js";
import rust from "../../../highlight.js/lib/languages/rust.js";
import scala from "../../../highlight.js/lib/languages/scala.js";
import swift from "../../../highlight.js/lib/languages/swift.js";
import typescript from "../../../highlight.js/lib/languages/typescript.js";
var eagerLanguages = {
  python,
  java,
  go,
  javascript,
  json,
  cpp,
  typescript,
  php,
  ruby,
  c,
  csharp,
  nix,
  bash,
  rust,
  scala,
  kotlin,
  swift,
  dart,
  groovy,
  perl,
  lua
};
for (const [name, language] of Object.entries(eagerLanguages)) {
  hljs.registerLanguage(name, language);
}
var allLanguagesPromise;
function loadAllHighlightLanguages() {
  if (!allLanguagesPromise) {
    allLanguagesPromise = new Promise((resolve7) => {
      setImmediate(() => {
        void import("../../../highlight.js/lib/index.js").then(() => resolve7(), () => {
          resolve7();
        });
      });
    });
  }
  return allLanguagesPromise;
}
__name(loadAllHighlightLanguages, "loadAllHighlightLanguages");

// pi-dist/pi-coding-agent/utils/version-check.js
import { compare, valid } from "../../../semver/index.js";
var LATEST_VERSION_URL = "https://pi-in-go.dev/api/latest-version";
var DEFAULT_VERSION_CHECK_TIMEOUT_MS = 1e4;
function formatVersionCheckError(error) {
  const rootMessage = error instanceof Error && error.message ? error.message : String(error);
  const cause = error instanceof Error ? error.cause : void 0;
  const causes = cause instanceof AggregateError ? cause.errors : cause === void 0 ? [] : [cause];
  const codes = causes.map((value) => typeof value === "object" && value !== null && "code" in value && typeof value.code === "string" ? value.code : void 0).filter((code) => code !== void 0);
  if (codes.length > 0)
    return `${rootMessage} (${[...new Set(codes)].join(", ")})`;
  const causeMessage = causes.find((value) => value instanceof Error && Boolean(value.message))?.message;
  return causeMessage ? `${rootMessage} (cause: ${causeMessage})` : rootMessage;
}
__name(formatVersionCheckError, "formatVersionCheckError");
function comparePackageVersions(leftVersion, rightVersion) {
  const left = valid(leftVersion.trim());
  const right = valid(rightVersion.trim());
  if (!left || !right) {
    return void 0;
  }
  return compare(left, right);
}
__name(comparePackageVersions, "comparePackageVersions");
function isNewerPackageVersion(candidateVersion, currentVersion) {
  const comparison = comparePackageVersions(candidateVersion, currentVersion);
  if (comparison !== void 0) {
    return comparison > 0;
  }
  return candidateVersion.trim() !== currentVersion.trim();
}
__name(isNewerPackageVersion, "isNewerPackageVersion");
async function getLatestPiRelease(currentVersion, options = {}) {
  if (process.env.PI_OFFLINE)
    return void 0;
  const response = await fetchWithRetry(LATEST_VERSION_URL, {
    headers: {
      "User-Agent": getPiUserAgent(currentVersion),
      accept: "application/json"
    }
  }, {
    maxRetries: options.retry ? 2 : 0,
    timeoutMs: options.timeoutMs ?? DEFAULT_VERSION_CHECK_TIMEOUT_MS
  });
  if (!response.ok)
    return void 0;
  const data = await response.json();
  if (typeof data.version !== "string" || !data.version.trim()) {
    return void 0;
  }
  const packageName = typeof data.packageName === "string" && data.packageName.trim() ? data.packageName.trim() : void 0;
  const note = typeof data.note === "string" && data.note.trim() ? data.note.trim() : void 0;
  return {
    version: data.version.trim(),
    packageName,
    ...note ? { note } : {}
  };
}
__name(getLatestPiRelease, "getLatestPiRelease");
async function checkForNewPiVersion(currentVersion) {
  if (process.env.PI_SKIP_VERSION_CHECK)
    return void 0;
  try {
    const latestRelease = await getLatestPiRelease(currentVersion);
    if (latestRelease && isNewerPackageVersion(latestRelease.version, currentVersion)) {
      return latestRelease;
    }
    return void 0;
  } catch {
    return void 0;
  }
}
__name(checkForNewPiVersion, "checkForNewPiVersion");

// pi-dist/pi-coding-agent/modes/interactive/bug-report.js
import * as path3 from "node:path";

// pi-dist/pi-coding-agent/core/bug-report-upload.js
async function uploadBugReport(bundle, options = {}) {
  throw new Error("PiG does not upload bug reports (D62): export the report and attach it to a PiG issue instead.");
  const body = new FormData();
  for (const file of bugReportFiles(bundle)) {
    body.append(file.name, new Blob([file.data], { type: file.contentType }), file.name);
  }
  const response = await fetch(new URL("/v1/bug-reports", options.gatewayUrl ?? getRadiusGatewayUrl()), {
    method: "POST",
    headers: options.token ? { Authorization: `Bearer ${options.token}` } : void 0,
    body,
    signal: options.signal
  });
  const json2 = await response.json().catch(() => null);
  if (response.ok && json2?.ok === true && typeof json2.bug_report?.id === "string") {
    return { id: json2.bug_report.id };
  }
  const detail = json2 && !json2.ok ? json2.description || json2.error : void 0;
  throw new Error(`Bug report upload failed: ${detail || response.statusText || response.status}`);
}
__name(uploadBugReport, "uploadBugReport");

// pi-dist/pi-coding-agent/modes/interactive/components/bordered-loader.js
import { CancellableLoader, Container as Container8, Loader, Spacer as Spacer7, Text as Text8 } from "../../../pi-tui.mjs";
var BorderedLoader = class extends Container8 {
  static {
    __name(this, "BorderedLoader");
  }
  loader;
  cancellable;
  signalController;
  constructor(tui, theme2, message, options) {
    super();
    this.cancellable = options?.cancellable ?? true;
    const borderColor = /* @__PURE__ */ __name((s) => theme2.fg("border", s), "borderColor");
    this.addChild(new DynamicBorder(borderColor));
    if (this.cancellable) {
      this.loader = new CancellableLoader(tui, (s) => theme2.fg("accent", s), (s) => theme2.fg("muted", s), message);
    } else {
      this.signalController = new AbortController();
      this.loader = new Loader(tui, (s) => theme2.fg("accent", s), (s) => theme2.fg("muted", s), message);
    }
    this.addChild(this.loader);
    if (this.cancellable) {
      this.addChild(new Spacer7(1));
      this.addChild(new Text8(keyHint("tui.select.cancel", "cancel"), 1, 0));
    }
    this.addChild(new Spacer7(1));
    this.addChild(new DynamicBorder(borderColor));
  }
  get signal() {
    if (this.cancellable) {
      return this.loader.signal;
    }
    return this.signalController?.signal ?? new AbortController().signal;
  }
  set onAbort(fn) {
    if (this.cancellable) {
      this.loader.onAbort = fn;
    }
  }
  handleInput(data) {
    if (this.cancellable) {
      this.loader.handleInput(data);
    }
  }
  dispose() {
    if ("dispose" in this.loader && typeof this.loader.dispose === "function") {
      this.loader.dispose();
    } else if ("stop" in this.loader && typeof this.loader.stop === "function") {
      this.loader.stop();
    }
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/extension-editor.js
import { Container as Container9, Editor, getKeybindings as getKeybindings5, Spacer as Spacer8, Text as Text9 } from "../../../pi-tui.mjs";

// pi-dist/pi-coding-agent/modes/interactive/external-editor.js
import { spawn as spawn2 } from "node:child_process";
import { mkdtempSync, readFileSync as readFileSync7, rmSync as rmSync3, writeFileSync as writeFileSync4 } from "node:fs";
import { tmpdir as tmpdir3 } from "node:os";
import { join as join8 } from "node:path";
async function editInExternalEditor(options) {
  const directory = mkdtempSync(join8(tmpdir3(), "pi-editor-"));
  const filePath = join8(directory, "prompt.md");
  try {
    writeFileSync4(filePath, options.content, "utf-8");
    const [editor, ...editorArgs] = options.command.split(" ");
    process.stdout.write(`Launching external editor: ${options.command}
Pi will resume when the editor exits.
`);
    const exitCode = await new Promise((resolve7) => {
      const child = spawn2(editor, [...editorArgs, filePath], {
        stdio: "inherit",
        shell: process.platform === "win32"
      });
      child.on("error", () => resolve7(null));
      child.on("close", (code) => resolve7(code));
    });
    if (exitCode !== 0) {
      return { status: "failed" };
    }
    return { status: "complete", content: stripBom(readFileSync7(filePath, "utf-8")).replace(/\n$/, "") };
  } finally {
    try {
      rmSync3(directory, { recursive: true, force: true });
    } catch {
    }
  }
}
__name(editInExternalEditor, "editInExternalEditor");

// pi-dist/pi-coding-agent/modes/interactive/components/extension-editor.js
var ExtensionEditorComponent = class extends Container9 {
  static {
    __name(this, "ExtensionEditorComponent");
  }
  editor;
  onSubmitCallback;
  onCancelCallback;
  tui;
  keybindings;
  externalEditorCommand;
  _focused = false;
  get focused() {
    return this._focused;
  }
  set focused(value) {
    this._focused = value;
    this.editor.focused = value;
  }
  constructor(tui, keybindings, title, prefill, onSubmit, onCancel, options, externalEditorCommand) {
    super();
    this.tui = tui;
    this.keybindings = keybindings;
    this.externalEditorCommand = externalEditorCommand || process.env.VISUAL || process.env.EDITOR || (process.platform === "win32" ? "notepad" : "nano");
    this.onSubmitCallback = onSubmit;
    this.onCancelCallback = onCancel;
    const { description, ...editorOptions } = options ?? {};
    this.addChild(new DynamicBorder());
    this.addChild(new Spacer8(1));
    this.addChild(new Text9(theme.fg("accent", title), 1, 0));
    if (description) {
      this.addChild(new Spacer8(1));
      this.addChild(new Text9(theme.fg("text", description), 1, 0));
    }
    this.addChild(new Spacer8(1));
    this.editor = new Editor(tui, getEditorTheme(), editorOptions);
    if (prefill) {
      this.editor.setText(prefill);
    }
    this.editor.onSubmit = (text) => {
      this.onSubmitCallback(text);
    };
    this.addChild(this.editor);
    this.addChild(new Spacer8(1));
    const hint = keyHint("tui.select.confirm", "submit") + "  " + keyHint("tui.input.newLine", "newline") + "  " + keyHint("tui.select.cancel", "cancel") + `  ${keyHint("app.editor.external", "external editor")}`;
    this.addChild(new Text9(hint, 1, 0));
    this.addChild(new Spacer8(1));
    this.addChild(new DynamicBorder());
  }
  handleInput(keyData) {
    const kb = getKeybindings5();
    if (kb.matches(keyData, "tui.select.cancel")) {
      this.onCancelCallback();
      return;
    }
    if (this.keybindings.matches(keyData, "app.editor.external")) {
      void this.handleOpenExternalEditor();
      return;
    }
    this.editor.handleInput(keyData);
  }
  async handleOpenExternalEditor() {
    const content = this.editor.getText();
    this.tui.stop();
    try {
      const result = await editInExternalEditor({
        command: this.externalEditorCommand,
        content
      });
      if (result.status === "complete") {
        this.editor.setText(result.content);
      }
    } finally {
      this.tui.start();
      this.tui.requestRender(true);
    }
  }
};

// pi-dist/pi-coding-agent/modes/interactive/session-share.js
import { spawn as spawn3, spawnSync as spawnSync2 } from "node:child_process";
import * as crypto from "node:crypto";
import * as fs from "node:fs";
import * as os2 from "node:os";
import * as path2 from "node:path";
import { DEFAULT_RADIUS_GATEWAY as DEFAULT_RADIUS_GATEWAY2 } from "../../pi-ai/providers/radius-config.js";
import { hyperlink as hyperlink2 } from "../../../pi-tui.mjs";
function createShareTrailingEntries(session, parentId, timestamp) {
  return [
    {
      type: "custom",
      customType: "pi.share",
      id: crypto.randomUUID().slice(0, 8),
      parentId,
      timestamp,
      data: {
        systemPrompt: session.state.systemPrompt,
        tools: session.state.tools.map((tool) => ({
          name: tool.name,
          description: tool.description,
          parameters: tool.parameters
        }))
      }
    }
  ];
}
__name(createShareTrailingEntries, "createShareTrailingEntries");
function exportSessionForShare(filePath, session) {
  exportSessionToJsonl(session.sessionManager, filePath, (parentId, timestamp) => createShareTrailingEntries(session, parentId, timestamp));
}
__name(exportSessionForShare, "exportSessionForShare");
async function shareSession(context) {
  const tempDir = fs.mkdtempSync(path2.join(os2.tmpdir(), "pi-share-"));
  const jsonlFile = path2.join(tempDir, "session.jsonl");
  const htmlFile = path2.join(tempDir, "session.html");
  try {
    try {
      exportSessionForShare(jsonlFile, context.session);
    } catch (error) {
      context.showError(`Failed to export session: ${error instanceof Error ? error.message : "Unknown error"}`);
      return;
    }
    if (await tryShareViaRadius(jsonlFile, context))
      return;
    try {
      const authResult = spawnSync2("gh", ["auth", "status"], { encoding: "utf-8" });
      if (authResult.status !== 0) {
        context.showError("GitHub CLI is not logged in. Run 'gh auth login' first.");
        return;
      }
    } catch {
      context.showError("GitHub CLI (gh) is not installed. Install it from https://cli.github.com/");
      return;
    }
    try {
      await context.session.exportToHtml(htmlFile, { themeName: theme.name });
    } catch (error) {
      context.showError(`Failed to export session: ${error instanceof Error ? error.message : "Unknown error"}`);
      return;
    }
    await shareViaGist(htmlFile, context);
  } finally {
    try {
      fs.rmSync(tempDir, { recursive: true, force: true });
    } catch {
    }
  }
}
__name(shareSession, "shareSession");
async function tryShareViaRadius(tmpFile, context) {
  return false;
  const provider = context.session.modelRuntime.getProvider("radius");
  if (!provider)
    return false;
  const token = getAuthCredential(await context.session.modelRuntime.getAuth("radius", { minOAuthValidityMs: 5 * 6e4 }));
  if (!token)
    return false;
  const loader = new BorderedLoader(context.ui, theme, "Uploading to Radius...");
  context.editorContainer.clear();
  context.editorContainer.addChild(loader);
  context.ui.setFocus(loader);
  context.ui.requestRender();
  loader.onAbort = () => {
    restoreEditor(loader, context);
    context.showStatus("Share cancelled");
  };
  try {
    const body = fs.readFileSync(tmpFile);
    const url = new URL("/v1/artifacts", DEFAULT_RADIUS_GATEWAY2);
    url.searchParams.set("visibility", "organization");
    url.searchParams.set("title", "Pi session");
    const response = await fetch(url, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${token}`,
        "Content-Type": "application/x-ndjson",
        "Content-Length": String(body.byteLength)
      },
      body,
      signal: loader.signal
    });
    if (loader.signal.aborted)
      return true;
    const json2 = await response.json().catch(() => null);
    if (loader.signal.aborted)
      return true;
    restoreEditor(loader, context);
    if (!response.ok || !json2?.artifact) {
      context.showError(`Failed to upload Radius artifact: ${json2?.error || response.statusText || response.status}`);
      return true;
    }
    const shareUrl = json2.artifact.canonical_url;
    context.showStatus(`Share URL: ${hyperlink2(shareUrl, shareUrl)}`);
    return true;
  } catch (error) {
    if (!loader.signal.aborted) {
      restoreEditor(loader, context);
      context.showError(`Failed to upload Radius artifact: ${error instanceof Error ? error.message : "Unknown error"}`);
    }
    return true;
  }
}
__name(tryShareViaRadius, "tryShareViaRadius");
async function shareViaGist(tmpFile, context) {
  const loader = new BorderedLoader(context.ui, theme, "Creating gist...");
  context.editorContainer.clear();
  context.editorContainer.addChild(loader);
  context.ui.setFocus(loader);
  context.ui.requestRender();
  let proc = null;
  loader.onAbort = () => {
    proc?.kill();
    restoreEditor(loader, context);
    context.showStatus("Share cancelled");
  };
  try {
    const result = await new Promise((resolve7) => {
      proc = spawn3("gh", ["gist", "create", "--public=false", tmpFile]);
      let stdout = "";
      let stderr = "";
      proc.stdout?.on("data", (data) => {
        stdout += data.toString();
      });
      proc.stderr?.on("data", (data) => {
        stderr += data.toString();
      });
      proc.on("close", (code) => resolve7({ stdout, stderr, code }));
    });
    if (loader.signal.aborted)
      return;
    restoreEditor(loader, context);
    if (result.code !== 0) {
      context.showError(`Failed to create gist: ${result.stderr?.trim() || "Unknown error"}`);
      return;
    }
    const gistUrl = result.stdout?.trim();
    const gistId = gistUrl?.split("/").pop();
    if (!gistId) {
      context.showError("Failed to parse gist ID from gh output");
      return;
    }
    const previewUrl = getShareViewerUrl(gistId);
    context.showStatus(`Share URL: ${hyperlink2(previewUrl, previewUrl)}
Gist: ${hyperlink2(gistUrl, gistUrl)}`);
  } catch (error) {
    if (!loader.signal.aborted) {
      restoreEditor(loader, context);
      context.showError(`Failed to create gist: ${error instanceof Error ? error.message : "Unknown error"}`);
    }
  }
}
__name(shareViaGist, "shareViaGist");
function restoreEditor(loader, context) {
  loader.dispose();
  context.editorContainer.clear();
  context.editorContainer.addChild(context.editor);
  context.ui.setFocus(context.editor);
}
__name(restoreEditor, "restoreEditor");

// pi-dist/pi-coding-agent/modes/interactive/bug-report.js
var DISCLAIMER = "This report goes to the Pi developers (Earendil) and is not shared publicly. It includes your pi version, operating system, the current model and provider configuration (without API keys), loaded extensions, settings, and provider error diagnostics from this session.";
var TRANSCRIPT_NOTE = "The transcript contains your messages, model output, tool calls and their results, including file contents and command output read during this session.";
async function reportBug(context, initialHint) {
  const options = await promptForOptions(context, initialHint);
  if (!options) {
    context.showStatus("Bug report cancelled");
    return;
  }
  if (options.delivery === "upload" && process.env.PI_OFFLINE) {
    context.showError("Uploading bug reports requires online mode. Use Export as Zip instead.");
    return;
  }
  let summary;
  if (options.includeSummary) {
    const loader = showLoader(context, `Writing summary with ${context.session.model?.name ?? "the current model"}...`);
    try {
      summary = await context.session.summarizeForBugReport({ hint: options.hint, signal: loader.signal });
    } catch (error) {
      restoreEditor2(context, loader);
      if (loader.signal.aborted)
        context.showStatus("Bug report cancelled");
      else
        context.showError(`Failed to write bug report summary: ${errorMessage3(error)}`);
      return;
    }
    restoreEditor2(context, loader);
    if (loader.signal.aborted) {
      context.showStatus("Bug report cancelled");
      return;
    }
  }
  let bundle;
  try {
    bundle = buildBundle(context.session, options, summary);
  } catch (error) {
    context.showError(`Failed to build bug report: ${errorMessage3(error)}`);
    return;
  }
  if (options.delivery === "upload") {
    const failure = await upload(context, bundle);
    if (failure === void 0)
      return;
    const fallback = await choose(context, "Upload failed", ["Export as Zip", "Cancel"], `${failure}

Export the report as a zip archive instead?`);
    if (fallback !== "Export as Zip") {
      context.showStatus("Bug report cancelled");
      return;
    }
  }
  await exportZip(context, bundle);
}
__name(reportBug, "reportBug");
async function promptForOptions(context, initialHint) {
  const hint = await input(context, "Report a bug", `${DISCLAIMER}

What went wrong? (optional)`, initialHint);
  if (hint === null)
    return void 0;
  const transcript = await choose(context, "Include the session transcript?", ["Yes, include the transcript", "No"], TRANSCRIPT_NOTE);
  if (!transcript)
    return void 0;
  const includeSession = transcript !== "No";
  let includeSummary = false;
  if (!includeSession) {
    const model = context.session.model;
    const summary = await choose(context, `Attach a summary written by ${model?.name ?? "the current model"} instead?`, ["Yes, generate a summary", "No"], `The transcript is sent to ${model?.provider ?? "your provider"} with your credentials and tokens. Only the generated summary is attached; the transcript stays on your machine.`);
    if (!summary)
      return void 0;
    includeSummary = summary !== "No";
  }
  const description = hint.trim();
  const delivery = await choose(context, "Bug report", ["Upload Report", "Export as Zip", "Cancel"], `Description: ${description || "none"}
Transcript: ${includeSession ? "included" : "not included"}
Summary: ${includeSummary ? `written by ${context.session.model?.name ?? "the current model"}` : "none"}

Upload sends the report to ${new URL(getRadiusGatewayUrl()).host}. Export writes a zip archive to the current directory instead.`);
  if (!delivery || delivery === "Cancel")
    return void 0;
  return {
    hint: description || void 0,
    includeSession,
    includeSummary,
    delivery: delivery === "Upload Report" ? "upload" : "zip"
  };
}
__name(promptForOptions, "promptForOptions");
function buildBundle(session, options, summary) {
  const extensions = session.resourceLoader.getExtensions();
  return {
    metadata: collectBugReportMetadata({
      hint: options.hint,
      sessionId: session.sessionId,
      cwd: session.sessionManager.getCwd(),
      includeSession: options.includeSession,
      includeSummary: summary !== void 0,
      messageCount: session.messages.length,
      model: session.model,
      modelRuntime: session.modelRuntime,
      thinkingLevel: session.thinkingLevel,
      extensions: extensions.extensions,
      extensionErrors: extensions.errors,
      globalSettings: session.settingsManager.getGlobalSettings(),
      projectSettings: session.settingsManager.getProjectSettings()
    }),
    diagnostics: collectBugReportDiagnostics(session.sessionManager, readCrashLog()),
    summary,
    sessionJsonl: options.includeSession ? serializeSessionBranch(session.sessionManager, (parentId, timestamp) => createShareTrailingEntries(session, parentId, timestamp)) : void 0
  };
}
__name(buildBundle, "buildBundle");
async function upload(context, bundle) {
  const loader = showLoader(context, "Uploading bug report...");
  try {
    const provider = context.session.modelRuntime.getProvider(RADIUS_PROVIDER_ID);
    const token = provider ? getAuthCredential(await context.session.modelRuntime.getAuth(RADIUS_PROVIDER_ID, { minOAuthValidityMs: 5 * 6e4 })) : void 0;
    const result = await uploadBugReport(bundle, { token, signal: loader.signal });
    restoreEditor2(context, loader);
    recordInSession(context.session, bundle, { delivery: "upload" });
    context.showStatus(`Bug report uploaded. Report ID: ${result.id}`);
    return void 0;
  } catch (error) {
    restoreEditor2(context, loader);
    if (loader.signal.aborted) {
      context.showStatus("Bug report cancelled");
      return void 0;
    }
    return errorMessage3(error);
  }
}
__name(upload, "upload");
async function exportZip(context, bundle) {
  const archivePath = path3.join(process.cwd(), bugReportArchiveFileName(bundle.metadata.id));
  try {
    await writeBugReportArchive(bundle, archivePath);
  } catch (error) {
    context.showError(`Failed to write bug report: ${errorMessage3(error)}`);
    return;
  }
  recordInSession(context.session, bundle, { delivery: "zip", path: archivePath });
  context.showStatus(`Bug report exported to: ${archivePath}
Report ID: ${bundle.metadata.id}`);
}
__name(exportZip, "exportZip");
function recordInSession(session, bundle, delivery) {
  session.sessionManager.appendCustomEntry(BUG_REPORT_CUSTOM_ENTRY_TYPE, {
    id: bundle.metadata.id,
    createdAt: bundle.metadata.createdAt,
    hint: bundle.metadata.hint,
    sessionIncluded: bundle.metadata.session.included,
    summaryIncluded: bundle.metadata.session.summaryIncluded,
    ...delivery
  });
  if (bundle.diagnostics.crashes.length > 0)
    clearCrashLog();
}
__name(recordInSession, "recordInSession");
function input(context, title, description, initialValue) {
  return new Promise((resolve7) => {
    let component;
    const finish = /* @__PURE__ */ __name((value) => {
      restoreEditor2(context, component);
      resolve7(value);
    }, "finish");
    component = new ExtensionEditorComponent(context.ui, context.keybindings, title, initialValue, (value) => finish(value), () => finish(null), { description }, context.session.settingsManager.getExternalEditorCommand());
    showOverlay(context, component);
  });
}
__name(input, "input");
function choose(context, title, options, description) {
  return new Promise((resolve7) => {
    let component;
    const finish = /* @__PURE__ */ __name((value) => {
      restoreEditor2(context, component);
      resolve7(value);
    }, "finish");
    component = new ExtensionSelectorComponent(title, options, finish, () => finish(), {
      tui: context.ui,
      description
    });
    showOverlay(context, component);
  });
}
__name(choose, "choose");
function showLoader(context, message) {
  const loader = new BorderedLoader(context.ui, theme, message);
  showOverlay(context, loader);
  return loader;
}
__name(showLoader, "showLoader");
function showOverlay(context, component) {
  context.editorContainer.clear();
  context.editorContainer.addChild(component);
  context.ui.setFocus(component);
  context.ui.requestRender();
}
__name(showOverlay, "showOverlay");
function restoreEditor2(context, component) {
  component.dispose?.();
  context.editorContainer.clear();
  context.editorContainer.addChild(context.editor);
  context.ui.setFocus(context.editor);
  context.ui.requestRender();
}
__name(restoreEditor2, "restoreEditor");
function errorMessage3(error) {
  return error instanceof Error ? error.message : "Unknown error";
}
__name(errorMessage3, "errorMessage");

// pi-dist/pi-coding-agent/modes/interactive/chat-viewport.js
import { ScrollView, VStack } from "../../../pi-tui.mjs";
function createChatViewport(options) {
  const transcript = new ScrollView(options.document, {
    follow: "end",
    primary: true,
    overscroll: "chain",
    scrollbar: options.scrollbar ?? "auto",
    ...options.scrollbarTrackStyle === void 0 ? {} : { scrollbarTrackStyle: options.scrollbarTrackStyle },
    ...options.scrollbarThumbStyle === void 0 ? {} : { scrollbarThumbStyle: options.scrollbarThumbStyle }
  });
  const dock = new VStack([
    { component: options.pendingMessages, shrink: 1, minSize: 0 },
    { component: options.status, shrink: 1, minSize: 0 },
    ...options.widgetsAbove === void 0 ? [] : [{ component: options.widgetsAbove, shrink: 1, minSize: 0 }],
    { component: options.editor, shrink: 1, minSize: 3 },
    ...options.widgetsBelow === void 0 ? [] : [{ component: options.widgetsBelow, shrink: 1, minSize: 0 }],
    { component: options.footer, shrink: 1, minSize: 0 }
  ]);
  return {
    transcript,
    root: new VStack([
      { component: transcript, basis: 0, grow: 1, shrink: 1, minSize: 1 },
      { component: dock, basis: "auto", grow: 0, shrink: 1, minSize: 1 }
    ])
  };
}
__name(createChatViewport, "createChatViewport");

// pi-dist/pi-coding-agent/modes/interactive/components/assistant-message.js
import { Container as Container10, Markdown, MouseRegion, Spacer as Spacer9, Text as Text10 } from "../../../pi-tui.mjs";

// pi-dist/pi-coding-agent/modes/interactive/components/markdown-transform.js
function createMarkdownTransform(messageType, isStreaming, transformers) {
  return (markdown, availableWidth) => applyMarkdownTransformers(markdown, { messageType, isStreaming, availableWidth }, transformers);
}
__name(createMarkdownTransform, "createMarkdownTransform");
function applyMarkdownTransformers(markdown, context, transformers) {
  let transformedMarkdown = markdown;
  for (const transformer of transformers) {
    try {
      const transformed = transformer(transformedMarkdown, context);
      if (typeof transformed === "string") {
        transformedMarkdown = transformed;
      }
    } catch {
    }
  }
  return transformedMarkdown;
}
__name(applyMarkdownTransformers, "applyMarkdownTransformers");

// pi-dist/pi-coding-agent/modes/interactive/components/assistant-message.js
var OSC133_ZONE_START = "\x1B]133;A\x07";
var OSC133_ZONE_END = "\x1B]133;B\x07";
var OSC133_ZONE_FINAL = "\x1B]133;C\x07";
var AssistantMessageComponent = class extends Container10 {
  static {
    __name(this, "AssistantMessageComponent");
  }
  contentContainer;
  hideThinkingBlock;
  markdownTheme;
  hiddenThinkingLabel;
  outputPad;
  markdownTransformers;
  lastMessage;
  hasToolCalls = false;
  isStreaming = false;
  thinkingVisibilityOverrides = /* @__PURE__ */ new Map();
  constructor(message, hideThinkingBlock = false, markdownTheme = getMarkdownTheme(), hiddenThinkingLabel = "Thinking...", outputPad = 1, markdownTransformers = []) {
    super();
    this.hideThinkingBlock = hideThinkingBlock;
    this.markdownTheme = markdownTheme;
    this.hiddenThinkingLabel = hiddenThinkingLabel;
    this.outputPad = outputPad;
    this.markdownTransformers = markdownTransformers;
    this.contentContainer = new Container10();
    this.addChild(this.contentContainer);
    if (message) {
      this.updateContent(message);
    }
  }
  invalidate() {
    super.invalidate();
    if (this.lastMessage) {
      this.updateContent(this.lastMessage);
    }
  }
  setHideThinkingBlock(hide) {
    this.hideThinkingBlock = hide;
    this.thinkingVisibilityOverrides.clear();
    if (this.lastMessage) {
      this.updateContent(this.lastMessage);
    }
  }
  setHiddenThinkingLabel(label) {
    this.hiddenThinkingLabel = label;
    if (this.lastMessage) {
      this.updateContent(this.lastMessage);
    }
  }
  setOutputPad(padding) {
    this.outputPad = padding;
    if (this.lastMessage) {
      this.updateContent(this.lastMessage);
    }
  }
  render(width) {
    const lines = super.render(width);
    if (this.hasToolCalls || lines.length === 0) {
      return lines;
    }
    lines[0] = OSC133_ZONE_START + lines[0];
    lines[lines.length - 1] = OSC133_ZONE_END + OSC133_ZONE_FINAL + lines[lines.length - 1];
    return lines;
  }
  updateContent(message, isStreaming = this.isStreaming) {
    this.lastMessage = message;
    this.isStreaming = isStreaming;
    this.contentContainer.clear();
    const hasVisibleContent = message.content.some((c2) => c2.type === "text" && c2.text.trim() || c2.type === "thinking" && c2.thinking.trim());
    if (hasVisibleContent) {
      this.contentContainer.addChild(new Spacer9(1));
    }
    let thinkingRunIndex = 0;
    for (let i = 0; i < message.content.length; i++) {
      const content = message.content[i];
      if (content.type === "text" && content.text.trim()) {
        this.contentContainer.addChild(new Markdown(content.text.trim(), this.outputPad, 0, this.markdownTheme, void 0, {
          transform: createMarkdownTransform("assistant", this.isStreaming, this.markdownTransformers)
        }));
      } else if (content.type === "thinking") {
        const thinkingBlocks = [];
        for (; i < message.content.length; i++) {
          const thinkingContent = message.content[i];
          if (thinkingContent.type !== "thinking") {
            break;
          }
          const thinking = thinkingContent.thinking.trim();
          if (thinking) {
            thinkingBlocks.push(thinking);
          }
        }
        i--;
        if (thinkingBlocks.length === 0) {
          continue;
        }
        const hasVisibleContentAfter = message.content.slice(i + 1).some((c2) => c2.type === "text" && c2.text.trim() || c2.type === "thinking" && c2.thinking.trim());
        const runIndex = thinkingRunIndex++;
        const hidden = this.thinkingVisibilityOverrides.get(runIndex) ?? this.hideThinkingBlock;
        const thinkingComponent = hidden ? new Text10(theme.italic(theme.fg("thinkingText", this.hiddenThinkingLabel)), this.outputPad, 0) : new Markdown(thinkingBlocks.join("\n\n"), this.outputPad, 0, this.markdownTheme, {
          color: /* @__PURE__ */ __name((text) => theme.fg("thinkingText", text), "color"),
          italic: true
        }, {
          transform: createMarkdownTransform("assistant-thinking", this.isStreaming, this.markdownTransformers)
        });
        this.contentContainer.addChild(new MouseRegion(thinkingComponent, (event) => {
          if (event.type !== "click" || event.button !== "left")
            return void 0;
          this.thinkingVisibilityOverrides.set(runIndex, !hidden);
          if (this.lastMessage)
            this.updateContent(this.lastMessage);
          return { handled: true };
        }));
        if (hasVisibleContentAfter) {
          this.contentContainer.addChild(new Spacer9(1));
        }
      }
    }
    const hasToolCalls = message.content.some((c2) => c2.type === "toolCall");
    this.hasToolCalls = hasToolCalls;
    if (message.stopReason === "length") {
      this.contentContainer.addChild(new Spacer9(1));
      this.contentContainer.addChild(new Text10(theme.fg("error", "Response was truncated before completion."), this.outputPad, 0));
    } else if (!hasToolCalls) {
      if (message.stopReason === "aborted") {
        const abortMessage = message.errorMessage && message.errorMessage !== "Request was aborted" ? message.errorMessage : "Operation aborted";
        this.contentContainer.addChild(new Spacer9(1));
        this.contentContainer.addChild(new Text10(theme.fg("error", abortMessage), this.outputPad, 0));
      } else if (message.stopReason === "error") {
        const errorMsg = message.errorMessage || "Unknown error";
        this.contentContainer.addChild(new Spacer9(1));
        this.contentContainer.addChild(new Text10(theme.fg("error", `Error: ${errorMsg}`), this.outputPad, 0));
      }
    }
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/bash-execution.js
import { Container as Container11, Loader as Loader2, Spacer as Spacer10, Text as Text11 } from "../../../pi-tui.mjs";
var PREVIEW_LINES = 20;
var BashExecutionComponent = class extends Container11 {
  static {
    __name(this, "BashExecutionComponent");
  }
  command;
  outputLines = [];
  status = "running";
  exitCode = void 0;
  loader;
  truncationResult;
  fullOutputPath;
  expanded = false;
  contentContainer;
  /** `dim` marks `!!` commands, whose output is excluded from the model context. */
  colorKey;
  outputPad;
  constructor(command, ui, excludeFromContext = false, outputPad = 1) {
    super();
    this.command = command;
    this.colorKey = excludeFromContext ? "dim" : "bashMode";
    this.outputPad = outputPad;
    const borderColor = /* @__PURE__ */ __name((str) => theme.fg(this.colorKey, str), "borderColor");
    this.addChild(new Spacer10(1));
    this.addChild(new DynamicBorder(borderColor));
    this.contentContainer = new Container11();
    this.addChild(this.contentContainer);
    this.loader = new Loader2(ui, (spinner) => theme.fg(this.colorKey, spinner), (text) => theme.fg("muted", text), `Running... (${keyText("tui.select.cancel")} to cancel)`);
    this.addChild(new DynamicBorder(borderColor));
    this.updateDisplay();
  }
  /**
   * Set whether the output is expanded (shows full output) or collapsed (preview only).
   */
  setExpanded(expanded) {
    this.expanded = expanded;
    this.updateDisplay();
  }
  setOutputPad(outputPad) {
    this.outputPad = outputPad;
    this.updateDisplay();
  }
  invalidate() {
    super.invalidate();
    this.updateDisplay();
  }
  appendOutput(chunk) {
    const clean = stripAnsi(chunk).replace(/\r\n/g, "\n").replace(/\r/g, "\n");
    const newLines = clean.split("\n");
    if (this.outputLines.length > 0 && newLines.length > 0) {
      this.outputLines[this.outputLines.length - 1] += newLines[0];
      this.outputLines.push(...newLines.slice(1));
    } else {
      this.outputLines.push(...newLines);
    }
    this.updateDisplay();
  }
  setComplete(exitCode, cancelled, truncationResult, fullOutputPath) {
    this.exitCode = exitCode;
    this.status = cancelled ? "cancelled" : exitCode !== 0 && exitCode !== void 0 && exitCode !== null ? "error" : "complete";
    this.truncationResult = truncationResult;
    this.fullOutputPath = fullOutputPath;
    this.loader.stop();
    this.updateDisplay();
  }
  updateDisplay() {
    const fullOutput = this.outputLines.join("\n");
    const contextTruncation = truncateTail(fullOutput, {
      maxLines: DEFAULT_MAX_LINES,
      maxBytes: DEFAULT_MAX_BYTES
    });
    const availableLines = contextTruncation.content ? contextTruncation.content.split("\n") : [];
    const previewLogicalLines = availableLines.slice(-PREVIEW_LINES);
    const hiddenLineCount = availableLines.length - previewLogicalLines.length;
    this.contentContainer.clear();
    const header = new Text11(theme.fg(this.colorKey, theme.bold(`$ ${this.command}`)), this.outputPad, 0);
    this.contentContainer.addChild(header);
    if (availableLines.length > 0) {
      if (this.expanded) {
        const displayText = availableLines.map((line) => theme.fg("muted", line)).join("\n");
        this.contentContainer.addChild(new Text11(`
${displayText}`, this.outputPad, 0));
      } else {
        const styledOutput = previewLogicalLines.map((line) => theme.fg("muted", line)).join("\n");
        const styledInput = `
${styledOutput}`;
        let cachedWidth;
        let cachedLines;
        this.contentContainer.addChild({
          render: /* @__PURE__ */ __name((width) => {
            if (cachedLines === void 0 || cachedWidth !== width) {
              const result = truncateToVisualLines(styledInput, PREVIEW_LINES, width, this.outputPad);
              cachedLines = result.visualLines;
              cachedWidth = width;
            }
            return cachedLines ?? [];
          }, "render"),
          invalidate: /* @__PURE__ */ __name(() => {
            cachedWidth = void 0;
            cachedLines = void 0;
          }, "invalidate")
        });
      }
    }
    if (this.status === "running") {
      this.contentContainer.addChild(this.loader);
    } else {
      const statusParts = [];
      if (hiddenLineCount > 0) {
        if (this.expanded) {
          statusParts.push(`${theme.fg("muted", "(")}${keyHint("app.tools.expand", "to collapse")}${theme.fg("muted", ")")}`);
        } else {
          statusParts.push(`${theme.fg("muted", `... ${hiddenLineCount} more lines (`)}${keyHint("app.tools.expand", "to expand")}${theme.fg("muted", ")")}`);
        }
      }
      if (this.status === "cancelled") {
        statusParts.push(theme.fg("warning", "(cancelled)"));
      } else if (this.status === "error") {
        statusParts.push(theme.fg("error", `(exit ${this.exitCode})`));
      }
      const wasTruncated = this.truncationResult?.truncated || contextTruncation.truncated;
      if (wasTruncated && this.fullOutputPath) {
        statusParts.push(theme.fg("warning", `Output truncated. Full output: ${this.fullOutputPath}`));
      }
      if (statusParts.length > 0) {
        this.contentContainer.addChild(new Text11(`
${statusParts.join("\n")}`, this.outputPad, 0));
      }
    }
  }
  /**
   * Get the raw output for creating BashExecutionMessage.
   */
  getOutput() {
    return this.outputLines.join("\n");
  }
  /**
   * Get the command that was executed.
   */
  getCommand() {
    return this.command;
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/branch-summary-message.js
import { Box, Container as Container12, Markdown as Markdown2, MouseRegion as MouseRegion2, Spacer as Spacer11, Text as Text12 } from "../../../pi-tui.mjs";
var BranchSummaryMessageComponent = class extends Box {
  static {
    __name(this, "BranchSummaryMessageComponent");
  }
  expanded = false;
  message;
  markdownTheme;
  constructor(message, markdownTheme = getMarkdownTheme(), outputPad = 1) {
    super(outputPad, 1, (t) => theme.bg("customMessageBg", t));
    this.message = message;
    this.markdownTheme = markdownTheme;
    this.updateDisplay();
  }
  setExpanded(expanded) {
    this.expanded = expanded;
    this.updateDisplay();
  }
  setOutputPad(outputPad) {
    this.setPaddingX(outputPad);
  }
  invalidate() {
    super.invalidate();
    this.updateDisplay();
  }
  updateDisplay() {
    this.clear();
    const content = new Container12();
    const label = theme.fg("customMessageLabel", `\x1B[1m[branch]\x1B[22m`);
    content.addChild(new Text12(label, 0, 0));
    content.addChild(new Spacer11(1));
    if (this.expanded) {
      const header = "**Branch Summary**\n\n";
      content.addChild(new Markdown2(header + this.message.summary, 0, 0, this.markdownTheme, {
        color: /* @__PURE__ */ __name((text) => theme.fg("customMessageText", text), "color")
      }));
    } else {
      content.addChild(new Text12(theme.fg("customMessageText", "Branch summary (") + theme.fg("dim", keyText("app.tools.expand")) + theme.fg("customMessageText", " to expand)"), 0, 0));
    }
    this.addChild(new MouseRegion2(content, (event) => {
      if (event.type !== "click" || event.button !== "left")
        return void 0;
      this.setExpanded(!this.expanded);
      return { handled: true };
    }));
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/compaction-summary-message.js
import { Box as Box2, Container as Container13, Markdown as Markdown3, MouseRegion as MouseRegion3, Spacer as Spacer12, Text as Text13 } from "../../../pi-tui.mjs";
var CompactionSummaryMessageComponent = class extends Box2 {
  static {
    __name(this, "CompactionSummaryMessageComponent");
  }
  expanded = false;
  message;
  markdownTheme;
  constructor(message, markdownTheme = getMarkdownTheme(), outputPad = 1) {
    super(outputPad, 1, (t) => theme.bg("customMessageBg", t));
    this.message = message;
    this.markdownTheme = markdownTheme;
    this.updateDisplay();
  }
  setExpanded(expanded) {
    this.expanded = expanded;
    this.updateDisplay();
  }
  setOutputPad(outputPad) {
    this.setPaddingX(outputPad);
  }
  invalidate() {
    super.invalidate();
    this.updateDisplay();
  }
  updateDisplay() {
    this.clear();
    const content = new Container13();
    const tokenStr = this.message.tokensBefore.toLocaleString();
    const label = theme.fg("customMessageLabel", `\x1B[1m[compaction]\x1B[22m`);
    content.addChild(new Text13(label, 0, 0));
    content.addChild(new Spacer12(1));
    if (this.expanded) {
      const header = `**Compacted from ${tokenStr} tokens**

`;
      content.addChild(new Markdown3(header + this.message.summary, 0, 0, this.markdownTheme, {
        color: /* @__PURE__ */ __name((text) => theme.fg("customMessageText", text), "color")
      }));
    } else {
      content.addChild(new Text13(theme.fg("customMessageText", `Compacted from ${tokenStr} tokens (`) + theme.fg("dim", keyText("app.tools.expand")) + theme.fg("customMessageText", " to expand)"), 0, 0));
    }
    this.addChild(new MouseRegion3(content, (event) => {
      if (event.type !== "click" || event.button !== "left")
        return void 0;
      this.setExpanded(!this.expanded);
      return { handled: true };
    }));
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/custom-editor.js
import { Editor as Editor2, visibleWidth as visibleWidth4 } from "../../../pi-tui.mjs";
var CustomEditor = class extends Editor2 {
  static {
    __name(this, "CustomEditor");
  }
  keybindings;
  workingStatusIndicator;
  embedWorkingStatus;
  actionHandlers = /* @__PURE__ */ new Map();
  // Special handlers that can be dynamically replaced
  onEscape;
  onCtrlD;
  onPasteImage;
  /** Handler for extension-registered shortcuts. Returns true if handled. */
  onExtensionShortcut;
  constructor(tui, theme2, keybindings, options) {
    super(tui, theme2, options);
    this.keybindings = keybindings;
    this.embedWorkingStatus = options?.embedWorkingStatus ?? false;
  }
  setWorkingStatusIndicator(indicator) {
    this.workingStatusIndicator = indicator;
  }
  renderTopBorder(width, hiddenLineCount) {
    if (!this.embedWorkingStatus || !this.workingStatusIndicator || width <= 0) {
      return super.renderTopBorder(width, hiddenLineCount);
    }
    let status = this.workingStatusIndicator.renderInBorder(Math.max(1, width - 5));
    let statusWidth = visibleWidth4(status);
    if (statusWidth === 0)
      return super.renderTopBorder(width, hiddenLineCount);
    const overflowLabel = hiddenLineCount > 0 ? ` \u2191 ${hiddenLineCount} more ` : void 0;
    const overflowLabelWidth = overflowLabel ? visibleWidth4(overflowLabel) : 0;
    const overflowStart = Math.floor((width - overflowLabelWidth) / 2);
    const canFitOverflow = /* @__PURE__ */ __name(() => overflowLabel !== void 0 && overflowLabelWidth + 2 <= width && overflowStart - (3 + statusWidth + 1) >= 1, "canFitOverflow");
    if (overflowLabel && !canFitOverflow()) {
      status = this.workingStatusIndicator.renderSpinnerInBorder(width);
      statusWidth = visibleWidth4(status);
    }
    if (canFitOverflow()) {
      const leftBlockWidth = 3 + statusWidth + 1;
      return this.borderColor("\u2500\u2500 ") + status + this.borderColor(` ${"\u2500".repeat(overflowStart - leftBlockWidth)}${overflowLabel}${"\u2500".repeat(width - overflowStart - overflowLabelWidth)}`);
    }
    if (width >= statusWidth + 5) {
      return this.borderColor("\u2500\u2500 ") + status + this.borderColor(` ${"\u2500".repeat(width - statusWidth - 4)}`);
    }
    status = this.workingStatusIndicator.renderSpinnerInBorder(width);
    statusWidth = visibleWidth4(status);
    const prefixWidth = Math.min(3, Math.max(0, width - statusWidth));
    return this.borderColor("\u2500".repeat(prefixWidth)) + status + this.borderColor("\u2500".repeat(Math.max(0, width - prefixWidth - statusWidth)));
  }
  /**
   * Register a handler for an app action.
   */
  onAction(action, handler) {
    this.actionHandlers.set(action, handler);
  }
  handleInput(data) {
    if (this.onExtensionShortcut?.(data)) {
      return;
    }
    if (this.keybindings.matches(data, "app.clipboard.pasteImage")) {
      this.onPasteImage?.();
      return;
    }
    if (this.keybindings.matches(data, "app.interrupt")) {
      if (!this.isShowingAutocomplete()) {
        const handler = this.onEscape ?? this.actionHandlers.get("app.interrupt");
        if (handler) {
          handler();
          return;
        }
      }
      super.handleInput(data);
      return;
    }
    if (this.keybindings.matches(data, "app.exit")) {
      if (this.getText().length === 0) {
        const handler = this.onCtrlD ?? this.actionHandlers.get("app.exit");
        if (handler)
          handler();
        return;
      }
    }
    if (this.keybindings.matches(data, "tui.editor.historyPrevious") || this.keybindings.matches(data, "tui.editor.historyNext")) {
      super.handleInput(data);
      return;
    }
    for (const [action, handler] of this.actionHandlers) {
      if (action !== "app.interrupt" && action !== "app.exit" && this.keybindings.matches(data, action)) {
        handler();
        return;
      }
    }
    super.handleInput(data);
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/custom-entry.js
import { Box as Box3, Container as Container14, Spacer as Spacer13, Text as Text14 } from "../../../pi-tui.mjs";
var CustomEntryComponent = class extends Container14 {
  static {
    __name(this, "CustomEntryComponent");
  }
  entry;
  renderer;
  customComponent;
  _expanded = false;
  outputPad;
  constructor(entry, renderer, outputPad = 1) {
    super();
    this.entry = entry;
    this.renderer = renderer;
    this.outputPad = outputPad;
    this.rebuild();
  }
  hasContent() {
    return this.customComponent !== void 0;
  }
  setExpanded(expanded) {
    if (this._expanded !== expanded) {
      this._expanded = expanded;
      this.rebuild();
    }
  }
  setOutputPad(outputPad) {
    this.outputPad = outputPad;
    this.rebuild();
  }
  invalidate() {
    super.invalidate();
    this.rebuild();
  }
  rebuild() {
    this.clear();
    this.customComponent = void 0;
    let component;
    try {
      component = this.renderer(this.entry, { expanded: this._expanded }, theme);
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      const box = new Box3(this.outputPad, 1, (text) => theme.bg("customMessageBg", text));
      box.addChild(new Text14(theme.fg("error", `[${this.entry.customType}] renderer failed: ${message}`), 0, 0));
      component = box;
    }
    if (!component) {
      return;
    }
    this.customComponent = component;
    this.addChild(new Spacer13(1));
    this.addChild(component);
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/custom-message.js
import { Box as Box4, Container as Container15, Markdown as Markdown4, Spacer as Spacer14, Text as Text15 } from "../../../pi-tui.mjs";
var CustomMessageComponent = class extends Container15 {
  static {
    __name(this, "CustomMessageComponent");
  }
  message;
  customRenderer;
  box;
  customComponent;
  markdownTheme;
  _expanded = false;
  outputPad;
  constructor(message, customRenderer, markdownTheme = getMarkdownTheme(), outputPad = 1) {
    super();
    this.message = message;
    this.customRenderer = customRenderer;
    this.markdownTheme = markdownTheme;
    this.outputPad = outputPad;
    this.addChild(new Spacer14(1));
    this.box = new Box4(1, 1, (t) => theme.bg("customMessageBg", t));
    this.rebuild();
  }
  setExpanded(expanded) {
    if (this._expanded !== expanded) {
      this._expanded = expanded;
      this.rebuild();
    }
  }
  setOutputPad(outputPad) {
    if (this.outputPad !== outputPad) {
      this.outputPad = outputPad;
      this.rebuild();
    }
  }
  invalidate() {
    super.invalidate();
    this.rebuild();
  }
  rebuild() {
    if (this.customComponent) {
      this.removeChild(this.customComponent);
      this.customComponent = void 0;
    }
    this.removeChild(this.box);
    if (this.customRenderer) {
      try {
        const component = this.customRenderer(this.message, { expanded: this._expanded, outputPad: this.outputPad }, theme);
        if (component) {
          this.customComponent = component;
          this.addChild(component);
          return;
        }
      } catch {
      }
    }
    this.addChild(this.box);
    this.box.clear();
    this.box.setPaddingX(this.outputPad);
    const label = theme.fg("customMessageLabel", `\x1B[1m[${this.message.customType}]\x1B[22m`);
    this.box.addChild(new Text15(label, 0, 0));
    this.box.addChild(new Spacer14(1));
    let text;
    if (typeof this.message.content === "string") {
      text = this.message.content;
    } else {
      text = this.message.content.filter((c2) => c2.type === "text").map((c2) => c2.text).join("\n");
    }
    this.box.addChild(new Markdown4(text, 0, 0, this.markdownTheme, {
      color: /* @__PURE__ */ __name((text2) => theme.fg("customMessageText", text2), "color")
    }));
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/earendil-announcement.js
import * as fs2 from "node:fs";
import { Container as Container16, Image, Spacer as Spacer15, Text as Text16 } from "../../../pi-tui.mjs";
var BLOG_URL = "https://mariozechner.at/posts/2026-04-08-ive-sold-out/";
var IMAGE_FILENAME = "clankolas.png";
var cachedImageBase64;
var attemptedImageLoad = false;
function loadImageBase64() {
  if (attemptedImageLoad) {
    return cachedImageBase64;
  }
  attemptedImageLoad = true;
  try {
    cachedImageBase64 = fs2.readFileSync(getBundledInteractiveAssetPath(IMAGE_FILENAME)).toString("base64");
  } catch {
    cachedImageBase64 = void 0;
  }
  return cachedImageBase64;
}
__name(loadImageBase64, "loadImageBase64");
var EarendilAnnouncementComponent = class extends Container16 {
  static {
    __name(this, "EarendilAnnouncementComponent");
  }
  constructor() {
    super();
    this.addChild(new DynamicBorder((text) => theme.fg("accent", text)));
    this.addChild(new Text16(theme.bold(theme.fg("accent", "pi has joined Earendil")), 1, 0));
    this.addChild(new Spacer15(1));
    this.addChild(new Text16(theme.fg("muted", "Read the blog post:"), 1, 0));
    this.addChild(new Text16(theme.fg("mdLink", BLOG_URL), 1, 0));
    this.addChild(new Spacer15(1));
    const imageBase64 = loadImageBase64();
    if (imageBase64) {
      this.addChild(new Image(imageBase64, "image/png", { fallbackColor: /* @__PURE__ */ __name((text) => theme.fg("muted", text), "fallbackColor") }, { maxWidthCells: 56, filename: IMAGE_FILENAME }));
      this.addChild(new Spacer15(1));
    }
    this.addChild(new DynamicBorder((text) => theme.fg("accent", text)));
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/easter-egg-3d.lazy.js
import { TuiAltScreen } from "../../../pi-tui.mjs";
function playEasterEgg3d(tui, egg) {
  if (!(tui instanceof TuiAltScreen))
    return false;
  if (tui.hasOverlay())
    return true;
  const screen = tui.getScreenLines();
  import("./chunk-SW7WNE7Y.js").then((module) => module.playEasterEgg3d(tui, screen, egg), () => {
  });
  return true;
}
__name(playEasterEgg3d, "playEasterEgg3d");
function playPiLogo3d(tui, column, row) {
  playEasterEgg3d(tui, { kind: "pi-logo", column, row });
}
__name(playPiLogo3d, "playPiLogo3d");
function playArmin3d(tui) {
  return playEasterEgg3d(tui, { kind: "armin" });
}
__name(playArmin3d, "playArmin3d");

// pi-dist/pi-coding-agent/modes/interactive/components/footer.js
import { isAbsolute, relative, resolve as resolve3, sep } from "node:path";
import { truncateToWidth as truncateToWidth4, visibleWidth as visibleWidth5 } from "../../../pi-tui.mjs";
function sanitizeStatusText(text) {
  return text.replace(/[\r\n\t]/g, " ").replace(/ +/g, " ").trim();
}
__name(sanitizeStatusText, "sanitizeStatusText");
function formatTokens(count) {
  if (count < 1e3)
    return count.toString();
  if (count < 1e4)
    return `${(count / 1e3).toFixed(1)}k`;
  if (count < 1e6)
    return `${Math.round(count / 1e3)}k`;
  if (count < 1e7)
    return `${(count / 1e6).toFixed(1)}M`;
  return `${Math.round(count / 1e6)}M`;
}
__name(formatTokens, "formatTokens");
function formatCwdForFooter(cwd, home) {
  if (!home)
    return cwd;
  const resolvedCwd = resolve3(cwd);
  const resolvedHome = resolve3(home);
  const relativeToHome = relative(resolvedHome, resolvedCwd);
  const isInsideHome = relativeToHome === "" || relativeToHome !== ".." && !relativeToHome.startsWith(`..${sep}`) && !isAbsolute(relativeToHome);
  if (!isInsideHome)
    return cwd;
  return relativeToHome === "" ? "~" : `~${sep}${relativeToHome}`;
}
__name(formatCwdForFooter, "formatCwdForFooter");
var FooterComponent = class {
  static {
    __name(this, "FooterComponent");
  }
  autoCompactEnabled = true;
  session;
  footerData;
  sessionStats;
  constructor(session, footerData) {
    this.session = session;
    this.footerData = footerData;
  }
  setSession(session) {
    this.session = session;
  }
  setAutoCompactEnabled(enabled) {
    this.autoCompactEnabled = enabled;
  }
  /**
   * No-op: git branch caching now handled by provider.
   * Kept for compatibility with existing call sites in interactive-mode.
   */
  invalidate() {
  }
  /**
   * Clean up resources.
   * Git watcher cleanup now handled by provider.
   */
  dispose() {
  }
  /**
   * Usage totals and context usage scan the whole session, and the footer renders on every frame.
   * Entries are append-only and every append moves the leaf, so the results only change with the
   * session, leaf, entry count, or the model whose context window applies.
   */
  getSessionStats() {
    const sessionManager = this.session.sessionManager;
    const entryCount = sessionManager.getEntryCount();
    const sessionId = sessionManager.getSessionId();
    const leafId = sessionManager.getLeafId();
    const limitsModel = this.session.routedModel?.model ?? this.session.model;
    const cached = this.sessionStats;
    if (cached && cached.session === this.session && cached.sessionId === sessionId && cached.leafId === leafId && cached.entryCount === entryCount && cached.limitsModel === limitsModel) {
      return cached;
    }
    const usageTotals = createUsageTotals();
    let latestCacheHitRate;
    for (const entry of sessionManager.getEntries()) {
      if (entry.type === "usage") {
        addUsageToTotals(usageTotals, entry.usage);
      } else if (entry.type === "message" && entry.message.role === "assistant") {
        addUsageToTotals(usageTotals, entry.message.usage);
        const latestPromptTokens = entry.message.usage.input + entry.message.usage.cacheRead + entry.message.usage.cacheWrite;
        latestCacheHitRate = latestPromptTokens > 0 ? entry.message.usage.cacheRead / latestPromptTokens * 100 : void 0;
      } else if (entry.type === "message" && entry.message.role === "toolResult" && entry.message.usage) {
        addUsageToTotals(usageTotals, entry.message.usage);
      } else if ((entry.type === "branch_summary" || entry.type === "compaction") && entry.usage) {
        addUsageToTotals(usageTotals, entry.usage);
      }
    }
    const contextUsage = this.session.getContextUsage();
    this.sessionStats = {
      session: this.session,
      sessionId,
      leafId,
      entryCount,
      limitsModel,
      usageTotals,
      latestCacheHitRate,
      contextUsage
    };
    return this.sessionStats;
  }
  render(width) {
    const state = this.session.state;
    const { usageTotals, latestCacheHitRate, contextUsage } = this.getSessionStats();
    const contextWindow = contextUsage?.contextWindow ?? state.model?.contextWindow ?? 0;
    const contextPercentValue = contextUsage?.percent ?? 0;
    const contextPercent = contextUsage?.percent !== null ? contextPercentValue.toFixed(1) : "?";
    let pwd = formatCwdForFooter(this.session.sessionManager.getCwd(), process.env.HOME || process.env.USERPROFILE);
    const branch = this.footerData.getGitBranch();
    if (branch) {
      pwd = `${pwd} (${branch})`;
    }
    const sessionName = this.session.sessionManager.getSessionName();
    if (sessionName) {
      pwd = `${pwd} \u2022 ${sessionName}`;
    }
    const statsParts = [];
    if (usageTotals.input)
      statsParts.push(`\u2191${formatTokens(usageTotals.input)}`);
    if (usageTotals.output)
      statsParts.push(`\u2193${formatTokens(usageTotals.output)}`);
    if (usageTotals.cacheRead)
      statsParts.push(`R${formatTokens(usageTotals.cacheRead)}`);
    if (usageTotals.cacheWrite)
      statsParts.push(`W${formatTokens(usageTotals.cacheWrite)}`);
    if ((usageTotals.cacheRead > 0 || usageTotals.cacheWrite > 0) && latestCacheHitRate !== void 0) {
      statsParts.push(`CH${latestCacheHitRate.toFixed(1)}%`);
    }
    const usingSubscription = state.model ? state.model.provider === "kimi-coding" || this.session.modelRuntime.isUsingSubscription(state.model.provider) : false;
    if (usageTotals.cost || usingSubscription) {
      const costStr = `$${usageTotals.cost.toFixed(3)}${usingSubscription ? " (sub)" : ""}`;
      statsParts.push(costStr);
    }
    let contextPercentStr;
    const autoIndicator = this.autoCompactEnabled ? " (auto)" : "";
    const contextPercentDisplay = contextPercent === "?" ? `?/${formatTokens(contextWindow)}${autoIndicator}` : `${contextPercent}%/${formatTokens(contextWindow)}${autoIndicator}`;
    if (contextPercentValue > 90) {
      contextPercentStr = theme.fg("error", contextPercentDisplay);
    } else if (contextPercentValue > 70) {
      contextPercentStr = theme.fg("warning", contextPercentDisplay);
    } else {
      contextPercentStr = contextPercentDisplay;
    }
    statsParts.push(contextPercentStr);
    if (areExperimentalFeaturesEnabled()) {
      statsParts.push(`${theme.fg("dim", "\u2022")} ${theme.bold(theme.fg("warning", "xp"))}`);
    }
    let statsLeft = statsParts.join(" ");
    const modelName = state.model?.id || "no-model";
    let statsLeftWidth = visibleWidth5(statsLeft);
    if (statsLeftWidth > width) {
      statsLeft = truncateToWidth4(statsLeft, width, "...");
      statsLeftWidth = visibleWidth5(statsLeft);
    }
    const minPadding = 2;
    let rightSideWithoutProvider = modelName;
    if (state.model?.reasoning) {
      const thinkingLevel = state.thinkingLevel || "off";
      rightSideWithoutProvider = thinkingLevel === "off" ? `${modelName} \u2022 thinking off` : `${modelName} \u2022 ${thinkingLevel}`;
    }
    const routed = this.session.routedModel;
    if (routed) {
      const level = routed.thinkingLevel ? ` \u2022 ${routed.thinkingLevel}` : "";
      rightSideWithoutProvider += ` \u2192 ${routed.model.id}${level}`;
    }
    let rightSide = rightSideWithoutProvider;
    if (this.footerData.getAvailableProviderCount() > 1 && state.model) {
      rightSide = `(${state.model.provider}) ${rightSideWithoutProvider}`;
      if (statsLeftWidth + minPadding + visibleWidth5(rightSide) > width) {
        rightSide = rightSideWithoutProvider;
      }
    }
    const rightSideWidth = visibleWidth5(rightSide);
    const totalNeeded = statsLeftWidth + minPadding + rightSideWidth;
    let statsLine;
    if (totalNeeded <= width) {
      const padding = " ".repeat(width - statsLeftWidth - rightSideWidth);
      statsLine = statsLeft + padding + rightSide;
    } else {
      const availableForRight = width - statsLeftWidth - minPadding;
      if (availableForRight > 0) {
        const truncatedRight = truncateToWidth4(rightSide, availableForRight, "");
        const truncatedRightWidth = visibleWidth5(truncatedRight);
        const padding = " ".repeat(Math.max(0, width - statsLeftWidth - truncatedRightWidth));
        statsLine = statsLeft + padding + truncatedRight;
      } else {
        statsLine = statsLeft;
      }
    }
    const dimStatsLeft = theme.fg("dim", statsLeft);
    const remainder = statsLine.slice(statsLeft.length);
    const dimRemainder = theme.fg("dim", remainder);
    const pwdLine = truncateToWidth4(theme.fg("dim", pwd), width, theme.fg("dim", "..."));
    const lines = [pwdLine, dimStatsLeft + dimRemainder];
    const extensionStatuses = this.footerData.getExtensionStatuses();
    if (extensionStatuses.size > 0) {
      const sortedStatuses = Array.from(extensionStatuses.entries()).sort(([a], [b]) => a.localeCompare(b)).map(([, text]) => sanitizeStatusText(text));
      const statusLine = sortedStatuses.join(" ");
      lines.push(truncateToWidth4(statusLine, width, theme.fg("dim", "...")));
    }
    return lines;
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/login-dialog.js
import { Container as Container17, getKeybindings as getKeybindings6, Input as Input5, Spacer as Spacer16, Text as Text17 } from "../../../pi-tui.mjs";
var LoginDialogComponent = class extends Container17 {
  static {
    __name(this, "LoginDialogComponent");
  }
  contentContainer;
  input;
  tui;
  /** The shown sign-in URL, which `app.message.copy` copies. */
  authUrl;
  abortController = new AbortController();
  inputResolver;
  inputRejecter;
  onComplete;
  // Focusable implementation - propagate to input for IME cursor positioning
  _focused = false;
  get focused() {
    return this._focused;
  }
  set focused(value) {
    this._focused = value;
    this.input.focused = value;
  }
  constructor(tui, providerId, onComplete, providerNameOverride, titleOverride) {
    super();
    this.tui = tui;
    this.onComplete = onComplete;
    const providerName = providerNameOverride || providerId;
    const title = titleOverride ?? `Login to ${providerName}`;
    this.addChild(new DynamicBorder());
    this.addChild(new Text17(theme.fg("accent", theme.bold(title)), 1, 0));
    this.contentContainer = new Container17();
    this.addChild(this.contentContainer);
    this.input = new Input5();
    this.input.onSubmit = () => {
      if (this.inputResolver) {
        const value = this.input.getValue();
        this.replaceInputWithSubmittedText(value);
        this.inputResolver(value);
        this.inputResolver = void 0;
        this.inputRejecter = void 0;
      }
    };
    this.input.onEscape = () => {
      this.cancel();
    };
    this.addChild(new DynamicBorder());
  }
  get signal() {
    return this.abortController.signal;
  }
  replaceInputWithSubmittedText(value) {
    this.contentContainer.children = this.contentContainer.children.map((child) => child === this.input ? new Text17(`> ${value}`, 0, 0) : child);
  }
  cancel() {
    this.abortController.abort();
    if (this.inputRejecter) {
      this.inputRejecter(new Error("Login cancelled"));
      this.inputResolver = void 0;
      this.inputRejecter = void 0;
    }
    this.onComplete(false, "Login cancelled");
  }
  /**
   * Called by onAuth callback - show URL and optional instructions
   */
  showAuth(url, instructions) {
    this.contentContainer.clear();
    this.contentContainer.addChild(new Spacer16(1));
    this.authUrl = new AuthUrlComponent(this.tui, url);
    this.contentContainer.addChild(this.authUrl);
    if (instructions) {
      this.contentContainer.addChild(new Spacer16(1));
      this.contentContainer.addChild(new Text17(theme.fg("warning", instructions), 1, 0));
    }
    openBrowser(url);
    this.tui.requestRender();
  }
  /**
   * Called by onDeviceCode callback - show URL and user code.
   */
  showDeviceCode(info) {
    this.authUrl = void 0;
    this.contentContainer.clear();
    this.contentContainer.addChild(new Spacer16(1));
    const linkedUrl = `\x1B]8;;${info.verificationUri}\x07${info.verificationUri}\x1B]8;;\x07`;
    this.contentContainer.addChild(new Text17(theme.fg("accent", linkedUrl), 1, 0));
    const clickHint = process.platform === "darwin" ? "Cmd+click to open" : "Ctrl+click to open";
    const hyperlink4 = `\x1B]8;;${info.verificationUri}\x07${clickHint}\x1B]8;;\x07`;
    this.contentContainer.addChild(new Text17(theme.fg("dim", hyperlink4), 1, 0));
    this.contentContainer.addChild(new Spacer16(1));
    this.contentContainer.addChild(new Text17(theme.fg("warning", `Enter code: ${info.userCode}`), 1, 0));
    this.tui.requestRender();
  }
  /**
   * Show input for manual code/URL entry (for callback server providers)
   */
  showManualInput(prompt) {
    this.input.setValue("");
    this.contentContainer.addChild(new Spacer16(1));
    this.contentContainer.addChild(new Text17(theme.fg("dim", prompt), 1, 0));
    this.contentContainer.addChild(this.input);
    this.contentContainer.addChild(new Text17(`(${keyHint("tui.select.cancel", "to cancel")})`, 1, 0));
    this.tui.requestRender();
    return new Promise((resolve7, reject) => {
      this.inputResolver = resolve7;
      this.inputRejecter = reject;
    });
  }
  /**
   * Called by onPrompt callback - show prompt and wait for input
   * Note: Does NOT clear content, appends to existing (preserves URL from showAuth)
   */
  showPrompt(message, placeholder) {
    this.contentContainer.addChild(new Spacer16(1));
    this.contentContainer.addChild(new Text17(theme.fg("text", message), 1, 0));
    if (placeholder) {
      this.contentContainer.addChild(new Text17(theme.fg("dim", `e.g., ${placeholder}`), 1, 0));
    }
    this.contentContainer.addChild(this.input);
    this.contentContainer.addChild(new Text17(`(${keyHint("tui.select.cancel", "to cancel,")} ${keyHint("tui.select.confirm", "to submit")})`, 1, 0));
    this.input.setValue("");
    this.tui.requestRender();
    return new Promise((resolve7, reject) => {
      this.inputResolver = resolve7;
      this.inputRejecter = reject;
    });
  }
  /** Show informational text before another login step. */
  showDetails(lines) {
    this.authUrl = void 0;
    this.contentContainer.clear();
    this.contentContainer.addChild(new Spacer16(1));
    for (const line of lines) {
      this.contentContainer.addChild(new Text17(line, 1, 0));
    }
    this.tui.requestRender();
  }
  /** Show provider-owned information and links without starting an auth callback flow. */
  showInfo(message, links = [], showCloseHint = false) {
    this.contentContainer.addChild(new Spacer16(1));
    this.contentContainer.addChild(new Text17(theme.fg("text", message), 1, 0));
    for (const link of links) {
      const text = link.label ? `${link.label}: ${link.url}` : link.url;
      const hyperlink4 = `\x1B]8;;${link.url}\x07${text}\x1B]8;;\x07`;
      this.contentContainer.addChild(new Text17(theme.fg("accent", hyperlink4), 1, 0));
    }
    if (showCloseHint) {
      this.contentContainer.addChild(new Spacer16(1));
      this.contentContainer.addChild(new Text17(`(${keyHint("tui.select.cancel", "to close")})`, 1, 0));
    }
    this.tui.requestRender();
  }
  /**
   * Show waiting message (for polling flows like GitHub Copilot)
   */
  showWaiting(message) {
    this.contentContainer.addChild(new Spacer16(1));
    this.contentContainer.addChild(new Text17(theme.fg("dim", message), 1, 0));
    this.contentContainer.addChild(new Text17(`(${keyHint("tui.select.cancel", "to cancel")})`, 1, 0));
    this.tui.requestRender();
  }
  /**
   * Called by onProgress callback
   */
  showProgress(message) {
    this.contentContainer.addChild(new Text17(theme.fg("dim", message), 1, 0));
    this.tui.requestRender();
  }
  handleInput(data) {
    const kb = getKeybindings6();
    if (kb.matches(data, "tui.select.cancel")) {
      this.cancel();
      return;
    }
    if (this.authUrl && kb.matches(data, "app.message.copy")) {
      void this.authUrl.copy();
      return;
    }
    this.input.handleInput(data);
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/mermaid.js
import { Marked } from "../../../pi-tui.mjs";
import { render } from "../../../grok-mermaid/dist/index.js";
var markdownParser = new Marked();
function isMermaid(token) {
  return token.type === "code" && token.lang?.trim().split(/\s+/, 1)[0]?.toLowerCase() === "mermaid";
}
__name(isMermaid, "isMermaid");
function codeSpan(line) {
  const content = line || "\xA0";
  const longestBacktickRun = Math.max(0, ...Array.from(content.matchAll(/`+/g), (match) => match[0].length));
  const fence = "`".repeat(longestBacktickRun + 1);
  const padding = content.startsWith("`") || content.endsWith("`") ? " " : "";
  return `${fence}${padding}${content}${padding}${fence}`;
}
__name(codeSpan, "codeSpan");
function styleSpan(span, theme2) {
  switch (span.cls) {
    case "border":
      return theme2.fg("borderMuted", span.text);
    case "text":
      return theme2.fg("text", span.text);
    case "edge":
      return theme2.fg("accent", span.text);
    case "edgeLabel":
      return theme2.fg("muted", span.text);
    case "title":
      return theme2.fg("accent", theme2.bold(span.text));
    case "none":
      return span.text;
  }
}
__name(styleSpan, "styleSpan");
function themedLines(art, theme2) {
  return art.styled.map((row) => row.map((span) => styleSpan(span, theme2)).join(""));
}
__name(themedLines, "themedLines");
function createMermaidMarkdownTransformer(options) {
  return (markdown, context) => {
    const mode = options.getMode();
    if (mode === "off" || context.messageType === "assistant-thinking" || context.isStreaming && mode !== "streaming") {
      return markdown;
    }
    return markdownParser.lexer(markdown).map((token) => {
      if (!isMermaid(token))
        return token.raw;
      const art = render(token.text);
      if (!art || art.width > context.availableWidth)
        return token.raw;
      if (!context.isStreaming && art.warnings.length > 0) {
        const suffix = art.warnings.length > 1 ? ` (+${art.warnings.length - 1} more)` : "";
        const warning = `Mermaid diagram not rendered: ${art.warnings[0]}${suffix}`;
        const styledWarning = options.theme ? options.theme.fg("warning", warning) : warning;
        return `${token.raw}
${codeSpan(styledWarning)}  
`;
      }
      const lines = options.theme ? themedLines(art, options.theme) : art.plain;
      return `${lines.map(codeSpan).join("  \n")}
`;
    }).join("");
  };
}
__name(createMermaidMarkdownTransformer, "createMermaidMarkdownTransformer");

// pi-dist/pi-coding-agent/modes/interactive/components/model-selector.js
import { modelsAreEqual } from "../../pi-ai/sdk-bundle/index.js";
import { Container as Container18, fuzzyFilter as fuzzyFilter3, getKeybindings as getKeybindings7, Input as Input6, Spacer as Spacer17, Text as Text18 } from "../../../pi-tui.mjs";

// pi-dist/pi-coding-agent/modes/interactive/model-catalog-refresh.js
var ModelCatalogRefreshCoordinator = class {
  static {
    __name(this, "ModelCatalogRefreshCoordinator");
  }
  activeByRuntime = /* @__PURE__ */ new WeakMap();
  refresh(modelRuntime, signal) {
    signal.throwIfAborted();
    let active = this.activeByRuntime.get(modelRuntime);
    if (!active) {
      const controller = new AbortController();
      let created;
      const operation = modelRuntime.refresh({ signal: controller.signal });
      const promise = raceWithAbortSignal(operation, controller.signal).finally(() => {
        if (this.activeByRuntime.get(modelRuntime) === created) {
          this.activeByRuntime.delete(modelRuntime);
        }
      });
      created = { controller, promise, waiters: 0 };
      active = created;
      this.activeByRuntime.set(modelRuntime, active);
    }
    active.waiters++;
    return raceWithAbortSignal(active.promise, signal).finally(() => {
      active.waiters--;
      if (active.waiters === 0 && this.activeByRuntime.get(modelRuntime) === active) {
        active.controller.abort();
      }
    });
  }
};
var modelCatalogRefreshCoordinator = new ModelCatalogRefreshCoordinator();
function refreshModelCatalogs(modelRuntime, signal) {
  return modelCatalogRefreshCoordinator.refresh(modelRuntime, signal);
}
__name(refreshModelCatalogs, "refreshModelCatalogs");

// pi-dist/pi-coding-agent/modes/interactive/model-search.js
function getModelSearchText(item) {
  const { id, provider } = item;
  const name = item.name ? ` ${item.name}` : "";
  return `${id} ${provider} ${provider}/${id} ${provider} ${id}${name}`;
}
__name(getModelSearchText, "getModelSearchText");
function getModelSelectorSearchText(item) {
  const { id, provider } = item;
  const name = item.name ? ` ${item.name}` : "";
  return `${provider} ${provider}/${id} ${provider} ${id}${name}`;
}
__name(getModelSelectorSearchText, "getModelSelectorSearchText");

// pi-dist/pi-coding-agent/modes/interactive/components/model-selector.js
var ModelSelectorComponent = class extends Container18 {
  static {
    __name(this, "ModelSelectorComponent");
  }
  searchInput;
  // Focusable implementation - propagate to searchInput for IME cursor positioning
  _focused = false;
  get focused() {
    return this._focused;
  }
  set focused(value) {
    this._focused = value;
    this.searchInput.focused = value;
  }
  listContainer;
  allModels = [];
  scopedModelItems = [];
  activeModels = [];
  filteredModels = [];
  selectedIndex = 0;
  currentModel;
  modelRuntime;
  onSelectCallback;
  onSelectAsDefaultCallback;
  onCancelCallback;
  errorMessage;
  refreshStatusMessage = "Refreshing model catalogs\u2026";
  refreshStatusSuccess = false;
  tui;
  scopedModels;
  defaultModel;
  scope = "all";
  scopeText;
  scopeHintText;
  refreshAbortController = new AbortController();
  refreshTimeout;
  closed = false;
  constructor(tui, currentModel, modelRuntime, scopedModels, onSelect, onCancel, initialSearchInput, onSelectAsDefault, defaultModel) {
    super();
    this.tui = tui;
    this.currentModel = currentModel;
    this.modelRuntime = modelRuntime;
    this.scopedModels = scopedModels;
    this.defaultModel = defaultModel;
    this.scope = scopedModels.length > 0 ? "scoped" : "all";
    this.onSelectCallback = onSelect;
    this.onSelectAsDefaultCallback = onSelectAsDefault;
    this.onCancelCallback = onCancel;
    this.addChild(new DynamicBorder());
    this.addChild(new Spacer17(1));
    if (scopedModels.length > 0) {
      this.scopeText = new Text18(this.getScopeText(), 0, 0);
      this.addChild(this.scopeText);
      this.scopeHintText = new Text18(this.getScopeHintText(), 0, 0);
      this.addChild(this.scopeHintText);
    } else {
      const hintText = "Only showing models from configured providers. Use /login to add providers.";
      this.addChild(new Text18(theme.fg("warning", hintText), 0, 0));
    }
    this.addChild(new Spacer17(1));
    this.searchInput = new Input6();
    if (initialSearchInput) {
      this.searchInput.setValue(initialSearchInput);
    }
    this.searchInput.onSubmit = () => {
      if (this.filteredModels[this.selectedIndex]) {
        this.handleSelect(this.filteredModels[this.selectedIndex].model);
      }
    };
    this.addChild(this.searchInput);
    this.addChild(new Spacer17(1));
    this.listContainer = new Container18();
    this.addChild(this.listContainer);
    this.addChild(new Spacer17(1));
    if (this.onSelectAsDefaultCallback) {
      this.addChild(new Text18(theme.fg("dim", `  ${keyDisplayText("tui.select.confirm")} to select \xB7 ${keyDisplayText("app.models.save")} to set as default \xB7 ${keyDisplayText("tui.select.cancel")} to cancel`), 0, 0));
    }
    this.addChild(new DynamicBorder());
    this.loadModelsFromSnapshot();
    if (initialSearchInput)
      this.filterModels(initialSearchInput);
    else
      this.updateList();
    this.tui.requestRender();
    void this.refreshModels();
  }
  loadModelsFromSnapshot() {
    const models = this.modelRuntime.getAvailableSnapshot().map((model) => ({
      provider: model.provider,
      id: model.id,
      model
    }));
    this.allModels = this.sortModels(models);
    this.scopedModels = this.scopedModels.map((scoped) => {
      const refreshed = this.modelRuntime.getModel(scoped.model.provider, scoped.model.id);
      return refreshed ? { ...scoped, model: refreshed } : scoped;
    });
    this.scopedModelItems = this.scopedModels.map((scoped) => ({
      provider: scoped.model.provider,
      id: scoped.model.id,
      model: scoped.model
    }));
    this.activeModels = this.scope === "scoped" ? this.scopedModelItems : this.allModels;
    this.filteredModels = this.activeModels;
    const currentIndex = this.filteredModels.findIndex((item) => modelsAreEqual(this.currentModel, item.model));
    this.selectedIndex = currentIndex >= 0 ? currentIndex : Math.min(this.selectedIndex, Math.max(0, this.filteredModels.length - 1));
  }
  async refreshModels() {
    const timeoutMs = 15e3;
    let timedOut = false;
    this.refreshTimeout = setTimeout(() => {
      timedOut = true;
      this.refreshAbortController.abort();
    }, timeoutMs);
    try {
      const result = await refreshModelCatalogs(this.modelRuntime, this.refreshAbortController.signal);
      if (this.closed)
        return;
      this.refreshStatusMessage = "";
      if (result.aborted && timedOut) {
        this.errorMessage = "Model refresh timed out; showing cached models.";
      } else if (result.errors.size === 1) {
        this.errorMessage = `Could not refresh ${result.errors.keys().next().value}; showing cached models.`;
      } else if (result.errors.size > 1) {
        this.errorMessage = `Could not refresh ${result.errors.size} model catalogs (${[...result.errors.keys()].join(", ")}); showing cached models.`;
      } else {
        this.errorMessage = this.modelRuntime.getError();
        if (!this.errorMessage) {
          this.refreshStatusMessage = "Model catalogs refreshed.";
          this.refreshStatusSuccess = true;
        }
      }
      this.loadModelsFromSnapshot();
      this.filterModels(this.searchInput.getValue());
      this.tui.requestRender();
    } catch (error) {
      if (this.closed)
        return;
      this.refreshStatusMessage = "";
      this.errorMessage = timedOut ? "Model refresh timed out; showing cached models." : `Could not refresh model catalogs: ${error instanceof Error ? error.message : String(error)}`;
      this.updateList();
      this.tui.requestRender();
    } finally {
      if (this.refreshTimeout)
        clearTimeout(this.refreshTimeout);
    }
  }
  dispose() {
    if (this.closed)
      return;
    this.closed = true;
    if (this.refreshTimeout)
      clearTimeout(this.refreshTimeout);
    this.refreshAbortController.abort();
  }
  sortModels(models) {
    const sorted = [...models];
    sorted.sort((a, b) => {
      const aIsCurrent = modelsAreEqual(this.currentModel, a.model);
      const bIsCurrent = modelsAreEqual(this.currentModel, b.model);
      if (aIsCurrent && !bIsCurrent)
        return -1;
      if (!aIsCurrent && bIsCurrent)
        return 1;
      const aIsDefault = this.isDefaultModel(a.model);
      const bIsDefault = this.isDefaultModel(b.model);
      if (aIsDefault && !bIsDefault)
        return -1;
      if (!aIsDefault && bIsDefault)
        return 1;
      return a.provider.localeCompare(b.provider);
    });
    return sorted;
  }
  getScopeText() {
    const allText = this.scope === "all" ? theme.fg("accent", "all") : theme.fg("muted", "all");
    const scopedText = this.scope === "scoped" ? theme.fg("accent", "scoped") : theme.fg("muted", "scoped");
    return `${theme.fg("muted", "Scope: ")}${allText}${theme.fg("muted", " | ")}${scopedText}`;
  }
  getScopeHintText() {
    return keyHint("tui.input.tab", "scope") + theme.fg("muted", " (all/scoped)");
  }
  isDefaultModel(model) {
    return this.defaultModel?.provider === model.provider && this.defaultModel.id === model.id;
  }
  isDefaultSearch(query) {
    const normalized = query.trim().toLowerCase();
    return normalized.length > 0 && "default".startsWith(normalized);
  }
  setScope(scope) {
    if (this.scope === scope)
      return;
    this.scope = scope;
    this.activeModels = this.scope === "scoped" ? this.scopedModelItems : this.allModels;
    const currentIndex = this.activeModels.findIndex((item) => modelsAreEqual(this.currentModel, item.model));
    this.selectedIndex = currentIndex >= 0 ? currentIndex : 0;
    this.filterModels(this.searchInput.getValue());
    if (this.scopeText) {
      this.scopeText.setText(this.getScopeText());
    }
  }
  filterModels(query) {
    if (query) {
      const filtered = fuzzyFilter3(this.activeModels, query, (item) => {
        const defaultText = this.isDefaultModel(item.model) ? " default" : "";
        return `${getModelSelectorSearchText({ id: item.id, provider: item.provider, name: item.model.name })}${defaultText}`;
      });
      if (this.isDefaultSearch(query)) {
        const defaultItems = this.activeModels.filter((item) => this.isDefaultModel(item.model));
        const defaultKeys = new Set(defaultItems.map((item) => `${item.provider}\0${item.id}`));
        this.filteredModels = [
          ...defaultItems,
          ...filtered.filter((item) => !defaultKeys.has(`${item.provider}\0${item.id}`))
        ];
      } else {
        this.filteredModels = filtered;
      }
    } else {
      this.filteredModels = this.activeModels;
    }
    this.selectedIndex = query ? 0 : Math.min(this.selectedIndex, Math.max(0, this.filteredModels.length - 1));
    this.updateList();
  }
  updateList() {
    this.listContainer.clear();
    const maxVisible = 10;
    const startIndex = Math.max(0, Math.min(this.selectedIndex - Math.floor(maxVisible / 2), this.filteredModels.length - maxVisible));
    const endIndex = Math.min(startIndex + maxVisible, this.filteredModels.length);
    for (let i = startIndex; i < endIndex; i++) {
      const item = this.filteredModels[i];
      if (!item)
        continue;
      const isSelected = i === this.selectedIndex;
      const isCurrent = modelsAreEqual(this.currentModel, item.model);
      const isDefault = this.isDefaultModel(item.model);
      const defaultBadge = isDefault ? theme.fg("muted", " \xB7 default") : "";
      const cursor = isSelected ? theme.fg("accent", "\u2192 ") : "  ";
      const currentMarker = isCurrent ? theme.fg("accent", "\u2713 ") : "  ";
      const modelText = isSelected ? theme.fg("accent", item.id) : item.id;
      const providerBadge = theme.fg("muted", `[${item.provider}]`);
      const line = `${cursor}${currentMarker}${modelText} ${providerBadge}${defaultBadge}`;
      this.listContainer.addChild(new Text18(line, 0, 0));
    }
    if (startIndex > 0 || endIndex < this.filteredModels.length) {
      const scrollInfo = theme.fg("muted", `  (${this.selectedIndex + 1}/${this.filteredModels.length})`);
      this.listContainer.addChild(new Text18(scrollInfo, 0, 0));
    }
    if (this.errorMessage) {
      const errorLines = this.errorMessage.split("\n");
      for (const line of errorLines) {
        this.listContainer.addChild(new Text18(theme.fg("error", line), 0, 0));
      }
    } else if (this.filteredModels.length === 0) {
      this.listContainer.addChild(new Text18(theme.fg("muted", "  No matching models"), 0, 0));
    } else {
      const selected = this.filteredModels[this.selectedIndex];
      this.listContainer.addChild(new Spacer17(1));
      this.listContainer.addChild(new Text18(theme.fg("muted", `  Model Name: ${selected.model.name}`), 0, 0));
    }
    if (this.refreshStatusMessage) {
      this.listContainer.addChild(new Spacer17(1));
      this.listContainer.addChild(new Text18(theme.fg(this.refreshStatusSuccess ? "success" : "muted", `  ${this.refreshStatusMessage}`), 0, 0));
    }
  }
  handleInput(keyData) {
    const kb = getKeybindings7();
    if (kb.matches(keyData, "tui.input.tab")) {
      if (this.scopedModelItems.length > 0) {
        const nextScope = this.scope === "all" ? "scoped" : "all";
        this.setScope(nextScope);
        if (this.scopeHintText) {
          this.scopeHintText.setText(this.getScopeHintText());
        }
      }
      return;
    }
    if (kb.matches(keyData, "tui.select.up")) {
      if (this.filteredModels.length === 0)
        return;
      this.selectedIndex = this.selectedIndex === 0 ? this.filteredModels.length - 1 : this.selectedIndex - 1;
      this.updateList();
    } else if (kb.matches(keyData, "tui.select.down")) {
      if (this.filteredModels.length === 0)
        return;
      this.selectedIndex = this.selectedIndex === this.filteredModels.length - 1 ? 0 : this.selectedIndex + 1;
      this.updateList();
    } else if (kb.matches(keyData, "tui.select.confirm")) {
      const selectedModel = this.filteredModels[this.selectedIndex];
      if (selectedModel) {
        this.handleSelect(selectedModel.model);
      }
    } else if (kb.matches(keyData, "tui.select.cancel")) {
      this.dispose();
      this.onCancelCallback();
    } else if (kb.matches(keyData, "app.models.save") && this.onSelectAsDefaultCallback) {
      const selectedModel = this.filteredModels[this.selectedIndex];
      if (selectedModel) {
        this.dispose();
        this.onSelectAsDefaultCallback(selectedModel.model);
      }
    } else {
      this.searchInput.handleInput(keyData);
      this.filterModels(this.searchInput.getValue());
    }
  }
  handleSelect(model) {
    this.dispose();
    this.onSelectCallback(model);
  }
  getSearchInput() {
    return this.searchInput;
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/oauth-selector.js
import { Container as Container19, fuzzyFilter as fuzzyFilter4, getKeybindings as getKeybindings8, Input as Input7, Spacer as Spacer18, TruncatedText } from "../../../pi-tui.mjs";
function formatAuthSelectorProviderType(authType, subscription) {
  if (authType === "api_key")
    return "API key";
  return subscription === false ? "account" : "subscription";
}
__name(formatAuthSelectorProviderType, "formatAuthSelectorProviderType");
function formatAuthSelectorProviderStatus(provider) {
  if (!provider.status)
    return theme.fg("muted", " \u2022 not configured");
  if (provider.status.type !== provider.authType) {
    const label = `${formatAuthSelectorProviderType(provider.status.type, provider.subscription)} configured`;
    return theme.fg("muted", " \u2022 ") + theme.fg("warning", label);
  }
  if (!provider.status.source || provider.status.source === "OAuth" || provider.status.source === "stored credential") {
    return theme.fg("success", " \u2713 configured");
  }
  const source = /^[A-Z][A-Z0-9_]*(?:, [A-Z][A-Z0-9_]*)*$/.test(provider.status.source) ? `env: ${provider.status.source}` : provider.status.source;
  return theme.fg("success", ` \u2713 ${source}`);
}
__name(formatAuthSelectorProviderStatus, "formatAuthSelectorProviderStatus");
var OAuthSelectorComponent = class extends Container19 {
  static {
    __name(this, "OAuthSelectorComponent");
  }
  searchInput;
  // Focusable implementation - propagate to search input for IME cursor positioning
  _focused = false;
  get focused() {
    return this._focused;
  }
  set focused(value) {
    this._focused = value;
    this.searchInput.focused = value;
  }
  listContainer;
  allProviders;
  filteredProviders;
  selectedIndex = 0;
  mode;
  onSelectCallback;
  onCancelCallback;
  showAuthTypeLabels;
  constructor(mode, providers, onSelect, onCancel, initialSearchInput) {
    super();
    this.mode = mode;
    this.allProviders = providers;
    this.filteredProviders = providers;
    this.showAuthTypeLabels = new Set(providers.map((provider) => provider.authType)).size > 1;
    this.onSelectCallback = onSelect;
    this.onCancelCallback = onCancel;
    this.addChild(new DynamicBorder());
    this.addChild(new Spacer18(1));
    const title = mode === "login" ? "Select provider to configure:" : "Select provider to logout:";
    this.addChild(new TruncatedText(theme.fg("accent", theme.bold(title)), 1, 0));
    this.addChild(new Spacer18(1));
    this.searchInput = new Input7();
    if (initialSearchInput) {
      this.searchInput.setValue(initialSearchInput);
    }
    this.searchInput.onSubmit = () => {
      const selectedProvider = this.filteredProviders[this.selectedIndex];
      if (selectedProvider) {
        this.onSelectCallback(selectedProvider.id, selectedProvider.authType);
      }
    };
    this.addChild(this.searchInput);
    this.addChild(new Spacer18(1));
    this.listContainer = new Container19();
    this.addChild(this.listContainer);
    this.addChild(new Spacer18(1));
    this.addChild(new DynamicBorder());
    this.filterProviders(initialSearchInput ?? "");
  }
  filterProviders(query) {
    this.filteredProviders = query ? fuzzyFilter4(this.allProviders, query, (provider) => `${provider.name} ${provider.id} ${provider.authType} ${provider.method?.name ?? ""}`) : this.allProviders;
    this.selectedIndex = Math.max(0, Math.min(this.selectedIndex, Math.max(0, this.filteredProviders.length - 1)));
    this.updateList();
  }
  updateList() {
    this.listContainer.clear();
    const maxVisible = 8;
    const startIndex = Math.max(0, Math.min(this.selectedIndex - Math.floor(maxVisible / 2), this.filteredProviders.length - maxVisible));
    const endIndex = Math.min(startIndex + maxVisible, this.filteredProviders.length);
    for (let i = startIndex; i < endIndex; i++) {
      const provider = this.filteredProviders[i];
      if (!provider)
        continue;
      const isSelected = i === this.selectedIndex;
      const statusIndicator = formatAuthSelectorProviderStatus(provider);
      const authTypeLabel = this.showAuthTypeLabels ? theme.fg("muted", ` [${formatAuthSelectorProviderType(provider.authType, provider.subscription)}]`) : "";
      let line = "";
      if (isSelected) {
        const prefix = theme.fg("accent", "\u2192 ");
        const text = theme.fg("accent", provider.name);
        line = prefix + text + authTypeLabel + statusIndicator;
      } else {
        const text = `  ${theme.fg("text", provider.name)}`;
        line = text + authTypeLabel + statusIndicator;
      }
      this.listContainer.addChild(new TruncatedText(line, 1, 0));
    }
    if (startIndex > 0 || endIndex < this.filteredProviders.length) {
      const scrollInfo = theme.fg("muted", `  (${this.selectedIndex + 1}/${this.filteredProviders.length})`);
      this.listContainer.addChild(new TruncatedText(scrollInfo, 1, 0));
    }
    if (this.filteredProviders.length === 0) {
      const message = this.allProviders.length === 0 ? this.mode === "login" ? "No providers available" : "No providers logged in. Use /login first." : "No matching providers";
      this.listContainer.addChild(new TruncatedText(theme.fg("muted", `  ${message}`), 1, 0));
    }
  }
  handleInput(keyData) {
    const kb = getKeybindings8();
    if (kb.matches(keyData, "tui.select.up")) {
      if (this.filteredProviders.length === 0)
        return;
      this.selectedIndex = Math.max(0, this.selectedIndex - 1);
      this.updateList();
    } else if (kb.matches(keyData, "tui.select.down")) {
      if (this.filteredProviders.length === 0)
        return;
      this.selectedIndex = Math.min(this.filteredProviders.length - 1, this.selectedIndex + 1);
      this.updateList();
    } else if (kb.matches(keyData, "tui.select.confirm")) {
      const selectedProvider = this.filteredProviders[this.selectedIndex];
      if (selectedProvider) {
        this.onSelectCallback(selectedProvider.id, selectedProvider.authType);
      }
    } else if (kb.matches(keyData, "tui.select.cancel")) {
      this.onCancelCallback();
    } else {
      this.searchInput.handleInput(keyData);
      this.filterProviders(this.searchInput.getValue());
    }
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/pi-logo.js
import { backgroundAnsi, foregroundAnsi, isAppleTerminalSession, rgbColor } from "../../../pi-tui.mjs";
var CORAL = rgbColor(228, 138, 122);
var BLUE = rgbColor(79, 142, 179);
var YELLOW = rgbColor(234, 182, 93);
var RESET = "\x1B[0m";
function piLogoLines() {
  const mode = theme.getColorMode();
  const fg = /* @__PURE__ */ __name((color) => foregroundAnsi(color, mode), "fg");
  const top = `${fg(CORAL)}${backgroundAnsi(BLUE, mode)}\u2580${RESET}${fg(CORAL)}\u2580\u2588${RESET} `;
  const bottom = `${fg(BLUE)}\u2588\u2580${RESET} ${fg(YELLOW)}\u2588${RESET}`;
  return [top, bottom];
}
__name(piLogoLines, "piLogoLines");
function supportsPiLogo() {
  return !isAppleTerminalSession();
}
__name(supportsPiLogo, "supportsPiLogo");
function piWordmark() {
  const mode = theme.getColorMode();
  return `${foregroundAnsi(CORAL, mode)}P${RESET}${foregroundAnsi(YELLOW, mode)}i${RESET}`;
}
__name(piWordmark, "piWordmark");

// pi-dist/pi-coding-agent/modes/interactive/components/radius-login-selector.js
import { foregroundAnsi as foregroundAnsi2, mixColors, parseColor, Text as Text19 } from "../../../pi-tui.mjs";
var RADIUS_COLORS = ["#4d9abf", "#83ccd2", "#f1be57", "#f09082"].map((hex) => parseColor(hex));
var CHARS_PER_COLOR = 4;
var CHARS_PER_SECOND = 10;
var ANIMATION_FRAME_MS = 50;
function radiusShimmer(text, elapsedMs) {
  const mode = theme.getColorMode();
  const cycle = RADIUS_COLORS.length * CHARS_PER_COLOR;
  const offset = elapsedMs / 1e3 * CHARS_PER_SECOND;
  let result = "";
  let index = 0;
  for (const char of text) {
    const position = ((index - offset) % cycle + cycle) % cycle;
    const band = Math.floor(position / CHARS_PER_COLOR);
    const t = position / CHARS_PER_COLOR - band;
    const amount = t * t * (3 - 2 * t);
    const from = RADIUS_COLORS[band];
    const to = RADIUS_COLORS[(band + 1) % RADIUS_COLORS.length];
    result += foregroundAnsi2(mixColors(from, to, amount, "srgb"), mode) + char;
    index++;
  }
  return `${result}\x1B[39m`;
}
__name(radiusShimmer, "radiusShimmer");
var RadiusLoginMenuComponent = class extends ExtensionSelectorComponent {
  static {
    __name(this, "RadiusLoginMenuComponent");
  }
  radiusOption;
  animationStart = performance.now();
  animationTimer;
  animating = false;
  constructor(tui, title, options, radiusOption, onSelect, onCancel) {
    super(title, options, (option) => {
      this.stopAnimation();
      onSelect(option);
    }, () => {
      this.stopAnimation();
      onCancel();
    });
    this.radiusOption = radiusOption;
    this.animationTimer = setInterval(() => {
      if (this.animating)
        tui.requestRender();
    }, ANIMATION_FRAME_MS);
    this.animationTimer.unref?.();
  }
  render(width) {
    const lines = super.render(width);
    const { label, text } = this.radiusOption;
    const selectedLine = new Text19(theme.fg("accent", "\u2192 ") + theme.fg("accent", label), 1, 0).render(width)[0];
    const index = selectedLine === void 0 ? -1 : lines.indexOf(selectedLine);
    this.animating = index >= 0;
    if (this.animating) {
      const shimmer = radiusShimmer(text, performance.now() - this.animationStart);
      const animatedLine = theme.fg("accent", "\u2192 ") + shimmer + label.slice(text.length);
      lines[index] = new Text19(animatedLine, 1, 0).render(width)[0] ?? "";
    }
    return lines;
  }
  stopAnimation() {
    clearInterval(this.animationTimer);
    this.animationTimer = void 0;
    this.animating = false;
  }
  dispose() {
    this.stopAnimation();
    super.dispose();
  }
};
function createLoginMenuSelector(tui, title, options, radiusOption, onSelect, onCancel) {
  return new RadiusLoginMenuComponent(tui, title, options, radiusOption, onSelect, onCancel);
}
__name(createLoginMenuSelector, "createLoginMenuSelector");

// pi-dist/pi-coding-agent/modes/interactive/components/scoped-models-selector.js
import { Container as Container20, fuzzyFilter as fuzzyFilter5, getKeybindings as getKeybindings9, Input as Input8, Key, matchesKey, Spacer as Spacer19, Text as Text20 } from "../../../pi-tui.mjs";
function isEnabled2(enabledIds, id) {
  return enabledIds === null || enabledIds.includes(id);
}
__name(isEnabled2, "isEnabled");
function normalizeEnabled(result, allIds) {
  return result.length === allIds.length && result.every((id) => allIds.includes(id)) ? null : result;
}
__name(normalizeEnabled, "normalizeEnabled");
function toggle(enabledIds, allIds, id) {
  if (enabledIds === null)
    return allIds.filter((modelId) => modelId !== id);
  const index = enabledIds.indexOf(id);
  if (index >= 0)
    return [...enabledIds.slice(0, index), ...enabledIds.slice(index + 1)];
  return normalizeEnabled([...enabledIds, id], allIds);
}
__name(toggle, "toggle");
function enableAll(enabledIds, allIds, targetIds) {
  if (enabledIds === null)
    return null;
  const targets = targetIds ?? allIds;
  const result = [...enabledIds];
  for (const id of targets) {
    if (!result.includes(id))
      result.push(id);
  }
  return normalizeEnabled(result, allIds);
}
__name(enableAll, "enableAll");
function clearAll(enabledIds, allIds, targetIds) {
  if (enabledIds === null) {
    return targetIds ? allIds.filter((id) => !targetIds.includes(id)) : [];
  }
  const targets = new Set(targetIds ?? enabledIds);
  return enabledIds.filter((id) => !targets.has(id));
}
__name(clearAll, "clearAll");
function move(enabledIds, id, delta) {
  if (enabledIds === null)
    return null;
  const list = [...enabledIds];
  const index = list.indexOf(id);
  if (index < 0)
    return list;
  const newIndex = index + delta;
  if (newIndex < 0 || newIndex >= list.length)
    return list;
  const result = [...list];
  [result[index], result[newIndex]] = [result[newIndex], result[index]];
  return result;
}
__name(move, "move");
function getSortedIds(enabledIds, allIds) {
  if (enabledIds === null)
    return allIds;
  const enabledSet = new Set(enabledIds);
  return [...enabledIds, ...allIds.filter((id) => !enabledSet.has(id))];
}
__name(getSortedIds, "getSortedIds");
var ScopedModelsSelectorComponent = class extends Container20 {
  static {
    __name(this, "ScopedModelsSelectorComponent");
  }
  modelsById = /* @__PURE__ */ new Map();
  allIds = [];
  enabledIds = null;
  filteredItems = [];
  selectedIndex = 0;
  searchInput;
  // Focusable implementation - propagate to searchInput for IME cursor positioning
  _focused = false;
  get focused() {
    return this._focused;
  }
  set focused(value) {
    this._focused = value;
    this.searchInput.focused = value;
  }
  listContainer;
  footerText;
  callbacks;
  maxVisible = 8;
  isDirty = false;
  refreshStatusText;
  constructor(config, callbacks) {
    super();
    this.callbacks = callbacks;
    for (const model of config.allModels) {
      const fullId = `${model.provider}/${model.id}`;
      this.modelsById.set(fullId, model);
      this.allIds.push(fullId);
    }
    this.enabledIds = config.enabledModelIds === null ? null : [...config.enabledModelIds];
    this.filteredItems = this.buildItems();
    this.addChild(new DynamicBorder());
    this.addChild(new Spacer19(1));
    this.addChild(new Text20(theme.fg("accent", theme.bold("Model Configuration")), 0, 0));
    this.addChild(new Text20(theme.fg("muted", `Session-only. ${keyDisplayText("app.models.save")} to save to settings.`), 0, 0));
    this.addChild(new Spacer19(1));
    this.searchInput = new Input8();
    this.addChild(this.searchInput);
    this.addChild(new Spacer19(1));
    this.listContainer = new Container20();
    this.addChild(this.listContainer);
    this.addChild(new Spacer19(1));
    if (config.refreshStatus) {
      this.refreshStatusText = new Text20(theme.fg("muted", `  ${config.refreshStatus}`), 0, 0);
      this.addChild(this.refreshStatusText);
    }
    this.footerText = new Text20(this.getFooterText(), 0, 0);
    this.addChild(this.footerText);
    this.addChild(new DynamicBorder());
    this.updateList();
  }
  updateModels(models, enabledModelIds) {
    const selectedId = this.filteredItems[this.selectedIndex]?.fullId;
    if (enabledModelIds !== void 0)
      this.enabledIds = enabledModelIds === null ? null : [...enabledModelIds];
    this.modelsById.clear();
    this.allIds = [];
    for (const model of models) {
      const fullId = `${model.provider}/${model.id}`;
      this.modelsById.set(fullId, model);
      this.allIds.push(fullId);
    }
    this.refresh();
    const refreshedIndex = selectedId ? this.filteredItems.findIndex((item) => item.fullId === selectedId) : -1;
    if (refreshedIndex >= 0) {
      this.selectedIndex = refreshedIndex;
      this.updateList();
    }
  }
  setRefreshStatus(message, kind) {
    this.refreshStatusText?.setText(theme.fg(kind, `  ${message}`));
  }
  buildItems() {
    return getSortedIds(this.enabledIds, this.allIds).map((id) => ({
      fullId: id,
      model: this.modelsById.get(id),
      enabled: isEnabled2(this.enabledIds, id)
    }));
  }
  getFooterText() {
    const enabledCount = this.enabledIds?.filter((id) => this.modelsById.has(id)).length ?? this.allIds.length;
    const unavailableCount = this.enabledIds?.filter((id) => !this.modelsById.has(id)).length ?? 0;
    const allEnabled = this.enabledIds === null;
    const countText = allEnabled ? "all enabled" : `${enabledCount}/${this.allIds.length} enabled${unavailableCount ? ` \xB7 ${unavailableCount} unavailable` : ""}`;
    const parts = [
      `${keyDisplayText("tui.select.confirm")} toggle`,
      `${keyDisplayText("app.models.enableAll")} all`,
      `${keyDisplayText("app.models.clearAll")} clear`,
      `${keyDisplayText("app.models.toggleProvider")} provider`,
      `${keyDisplayText("app.models.reorderUp")}/${keyDisplayText("app.models.reorderDown")} reorder`,
      `${keyDisplayText("app.models.save")} save`,
      countText
    ];
    return this.isDirty ? theme.fg("dim", `  ${parts.join(" \xB7 ")} `) + theme.fg("warning", "(unsaved)") : theme.fg("dim", `  ${parts.join(" \xB7 ")}`);
  }
  refresh() {
    const query = this.searchInput.getValue();
    const items = this.buildItems();
    this.filteredItems = query ? fuzzyFilter5(items, query, (item) => item.model ? getModelSearchText({ id: item.model.id, provider: item.model.provider, name: item.model.name }) : item.fullId) : items;
    this.selectedIndex = Math.min(this.selectedIndex, Math.max(0, this.filteredItems.length - 1));
    this.updateList();
    this.footerText.setText(this.getFooterText());
  }
  notifyChange() {
    this.callbacks.onChange(this.enabledIds === null ? null : [...this.enabledIds]);
  }
  updateList() {
    this.listContainer.clear();
    if (this.filteredItems.length === 0) {
      this.listContainer.addChild(new Text20(theme.fg("muted", "  No matching models"), 0, 0));
      return;
    }
    const startIndex = Math.max(0, Math.min(this.selectedIndex - Math.floor(this.maxVisible / 2), this.filteredItems.length - this.maxVisible));
    const endIndex = Math.min(startIndex + this.maxVisible, this.filteredItems.length);
    for (let i = startIndex; i < endIndex; i++) {
      const item = this.filteredItems[i];
      const isSelected = i === this.selectedIndex;
      const prefix = isSelected ? theme.fg("accent", "\u2192 ") : "  ";
      const id = item.model?.id ?? item.fullId;
      const styledId = item.model ? id : theme.strikethrough(id);
      const modelText = isSelected ? theme.fg("accent", styledId) : styledId;
      const providerBadge = theme.fg("muted", item.model ? ` [${item.model.provider}]` : " [unavailable]");
      const status = item.model && item.enabled ? theme.fg("accent", "\u2713 ") : "  ";
      this.listContainer.addChild(new Text20(`${prefix}${status}${modelText}${providerBadge}`, 0, 0));
    }
    if (startIndex > 0 || endIndex < this.filteredItems.length) {
      this.listContainer.addChild(new Text20(theme.fg("muted", `  (${this.selectedIndex + 1}/${this.filteredItems.length})`), 0, 0));
    }
    if (this.filteredItems.length > 0) {
      const selected = this.filteredItems[this.selectedIndex];
      this.listContainer.addChild(new Spacer19(1));
      this.listContainer.addChild(new Text20(theme.fg("muted", `  ${selected.model ? `Model Name: ${selected.model.name}` : "Model unavailable"}`), 0, 0));
    }
  }
  handleInput(data) {
    const kb = getKeybindings9();
    if (kb.matches(data, "tui.select.up")) {
      if (this.filteredItems.length === 0)
        return;
      this.selectedIndex = this.selectedIndex === 0 ? this.filteredItems.length - 1 : this.selectedIndex - 1;
      this.updateList();
      return;
    }
    if (kb.matches(data, "tui.select.down")) {
      if (this.filteredItems.length === 0)
        return;
      this.selectedIndex = this.selectedIndex === this.filteredItems.length - 1 ? 0 : this.selectedIndex + 1;
      this.updateList();
      return;
    }
    const reorderUp = kb.matches(data, "app.models.reorderUp");
    const reorderDown = kb.matches(data, "app.models.reorderDown");
    if (reorderUp || reorderDown) {
      if (this.enabledIds === null)
        return;
      const item = this.filteredItems[this.selectedIndex];
      if (item && isEnabled2(this.enabledIds, item.fullId)) {
        const delta = reorderUp ? -1 : 1;
        const currentIndex = this.enabledIds.indexOf(item.fullId);
        const newIndex = currentIndex + delta;
        if (newIndex >= 0 && newIndex < this.enabledIds.length) {
          this.enabledIds = move(this.enabledIds, item.fullId, delta);
          this.isDirty = true;
          this.selectedIndex += delta;
          this.refresh();
          this.notifyChange();
        }
      }
      return;
    }
    if (kb.matches(data, "tui.select.confirm")) {
      const item = this.filteredItems[this.selectedIndex];
      if (item) {
        this.enabledIds = toggle(this.enabledIds, this.allIds, item.fullId);
        this.isDirty = true;
        this.refresh();
        this.notifyChange();
      }
      return;
    }
    if (kb.matches(data, "app.models.enableAll")) {
      const targetIds = this.searchInput.getValue() ? this.filteredItems.map((i) => i.fullId) : void 0;
      this.enabledIds = enableAll(this.enabledIds, this.allIds, targetIds);
      this.isDirty = true;
      this.refresh();
      this.notifyChange();
      return;
    }
    if (kb.matches(data, "app.models.clearAll")) {
      const targetIds = this.searchInput.getValue() ? this.filteredItems.map((i) => i.fullId) : void 0;
      this.enabledIds = clearAll(this.enabledIds, this.allIds, targetIds);
      this.isDirty = true;
      this.refresh();
      this.notifyChange();
      return;
    }
    if (kb.matches(data, "app.models.toggleProvider")) {
      const item = this.filteredItems[this.selectedIndex];
      if (item?.model) {
        const provider = item.model.provider;
        const providerIds = this.allIds.filter((id) => this.modelsById.get(id).provider === provider);
        const allEnabled = providerIds.every((id) => isEnabled2(this.enabledIds, id));
        this.enabledIds = allEnabled ? clearAll(this.enabledIds, this.allIds, providerIds) : enableAll(this.enabledIds, this.allIds, providerIds);
        this.isDirty = true;
        this.refresh();
        this.notifyChange();
      }
      return;
    }
    if (kb.matches(data, "app.models.save")) {
      this.callbacks.onPersist(this.enabledIds === null ? null : [...this.enabledIds]);
      this.isDirty = false;
      this.footerText.setText(this.getFooterText());
      return;
    }
    if (matchesKey(data, Key.ctrl("c"))) {
      if (this.searchInput.getValue()) {
        this.searchInput.setValue("");
        this.refresh();
      } else {
        this.callbacks.onCancel();
      }
      return;
    }
    if (matchesKey(data, Key.escape)) {
      this.callbacks.onCancel();
      return;
    }
    this.searchInput.handleInput(data);
    this.refresh();
  }
  getSearchInput() {
    return this.searchInput;
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/settings-selector.js
import { getSupportedThinkingLevels } from "../../pi-ai/sdk-bundle/index.js";
import { Container as Container22, getCapabilities, SettingsList, Spacer as Spacer21, Text as Text22 } from "../../../pi-tui.mjs";

// pi-dist/pi-coding-agent/modes/interactive/components/settings-submenu.js
import { Container as Container21, fuzzyFilter as fuzzyFilter6, getKeybindings as getKeybindings10, Input as Input9, SelectList as SelectList3, Spacer as Spacer20, Text as Text21 } from "../../../pi-tui.mjs";
var SUBMENU_SELECT_LIST_LAYOUT = {
  minPrimaryColumnWidth: 12,
  maxPrimaryColumnWidth: 32
};
var SelectSubmenu = class extends Container21 {
  static {
    __name(this, "SelectSubmenu");
  }
  selectList;
  listChildIndex;
  allOptions;
  listLayout;
  searchInput;
  onSelectCb;
  onCancelCb;
  onSelectionChangeCb;
  constructor(title, description, options, currentValue, onSelect, onCancel, onSelectionChange, submenuOptions) {
    super();
    this.allOptions = options;
    this.listLayout = submenuOptions?.layout ?? SUBMENU_SELECT_LIST_LAYOUT;
    this.onSelectCb = onSelect;
    this.onCancelCb = onCancel;
    this.onSelectionChangeCb = onSelectionChange;
    this.addChild(new Text21(theme.bold(theme.fg("accent", title)), 0, 0));
    if (description) {
      this.addChild(new Spacer20(1));
      this.addChild(new Text21(theme.fg("muted", description), 0, 0));
    }
    if (submenuOptions?.searchable) {
      this.addChild(new Spacer20(1));
      this.searchInput = new Input9();
      this.searchInput.onSubmit = () => {
        this.selectList.handleInput("\r");
      };
      this.addChild(this.searchInput);
    }
    this.addChild(new Spacer20(1));
    this.selectList = this.buildSelectList(options, currentValue);
    this.listChildIndex = this.children.length;
    this.addChild(this.selectList);
    this.addChild(new Spacer20(1));
    const hint = submenuOptions?.searchable ? "  Type to filter \xB7 Enter to select \xB7 Esc to go back" : "  Enter to select \xB7 Esc to go back";
    this.addChild(new Text21(theme.fg("dim", hint), 0, 0));
  }
  buildSelectList(options, preselect) {
    const list = new SelectList3(options, Math.min(options.length, 10), getSelectListTheme(), this.listLayout);
    const idx = options.findIndex((o) => o.value === preselect);
    if (idx !== -1)
      list.setSelectedIndex(idx);
    list.onSelect = (item) => this.onSelectCb(item.value);
    list.onCancel = this.onCancelCb;
    if (this.onSelectionChangeCb) {
      const cb = this.onSelectionChangeCb;
      list.onSelectionChange = (item) => cb(item.value);
    }
    return list;
  }
  applyFilter(query) {
    const filtered = query ? fuzzyFilter6(this.allOptions, query, (item) => `${item.label} ${item.description ?? ""}`) : this.allOptions;
    const newList = this.buildSelectList(filtered, "");
    this.children[this.listChildIndex] = newList;
    this.selectList = newList;
  }
  handleInput(data) {
    if (this.searchInput) {
      const kb = getKeybindings10();
      const isNav = kb.matches(data, "tui.select.up") || kb.matches(data, "tui.select.down") || kb.matches(data, "tui.select.confirm") || kb.matches(data, "tui.select.cancel");
      if (isNav) {
        this.selectList.handleInput(data);
      } else {
        this.searchInput.handleInput(data);
        this.applyFilter(this.searchInput.getValue());
      }
    } else {
      this.selectList.handleInput(data);
    }
  }
};
var SteppedSubmenu = class extends Container21 {
  static {
    __name(this, "SteppedSubmenu");
  }
  steps;
  onComplete;
  onCancel;
  opts;
  activeComponent;
  context;
  constructor(steps, onComplete, onCancel, opts = {}) {
    super();
    this.steps = steps;
    this.onComplete = onComplete;
    this.onCancel = onCancel;
    this.opts = opts;
    this.context = { ...opts.initialContext ?? {} };
    this.activeComponent = this.buildStep(opts.startAtStep ?? 0);
  }
  buildStep(stepIndex) {
    const step = this.steps[stepIndex];
    const total = this.steps.length;
    const stepLabel = total > 1 ? `Step ${stepIndex + 1}/${total} \xB7 ` : "";
    const title = typeof step.title === "function" ? step.title(this.context) : step.title;
    const desc = typeof step.description === "function" ? step.description(this.context) : step.description;
    const items = step.options(this.context);
    const preselect = step.preselect?.(this.context) ?? "";
    return new SelectSubmenu(title, `${stepLabel}${desc}`, items, preselect, (value) => {
      this.context[step.key] = value;
      if (stepIndex < total - 1) {
        this.activeComponent = this.buildStep(stepIndex + 1);
      } else {
        this.onComplete({ ...this.context });
        if (this.opts.loop) {
          this.context = {};
          this.activeComponent = this.buildStep(0);
        } else {
          this.onCancel();
        }
      }
    }, () => {
      if (stepIndex > 0) {
        delete this.context[step.key];
        this.activeComponent = this.buildStep(stepIndex - 1);
      } else {
        this.onCancel();
      }
    }, void 0, step.searchable || step.layout ? { searchable: step.searchable, layout: step.layout } : void 0);
  }
  render(width) {
    return this.activeComponent.render(width);
  }
  handleInput(data) {
    this.activeComponent.handleInput?.(data);
  }
  invalidate() {
    this.activeComponent.invalidate?.();
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/settings-selector.js
var MODEL_PICKER_LAYOUT = { minPrimaryColumnWidth: 12, maxPrimaryColumnWidth: 46 };
var THINKING_DESCRIPTIONS = {
  off: "No reasoning",
  minimal: "Very brief reasoning (~1k tokens)",
  low: "Light reasoning (~2k tokens)",
  medium: "Moderate reasoning (~8k tokens)",
  high: "Deep reasoning (~16k tokens)",
  xhigh: "Extra-high reasoning (~32k tokens)",
  max: "Maximum reasoning"
};
var DEFAULT_PROJECT_TRUST_LABELS = {
  ask: "Ask",
  always: "Always trust",
  never: "Never trust"
};
var DEFAULT_PROJECT_TRUST_BY_LABEL = new Map(Object.entries(DEFAULT_PROJECT_TRUST_LABELS).map(([value, label]) => [label, value]));
var WarningSettingsSubmenu = class extends Container22 {
  static {
    __name(this, "WarningSettingsSubmenu");
  }
  settingsList;
  state;
  constructor(warnings, onChange, onCancel) {
    super();
    this.state = { ...warnings };
    const items = [
      {
        id: "anthropic-extra-usage",
        label: "Anthropic extra usage",
        description: "Warn when Anthropic subscription auth may use paid extra usage",
        currentValue: this.state.anthropicExtraUsage ?? true ? "true" : "false",
        values: ["true", "false"]
      }
    ];
    this.settingsList = new SettingsList(items, Math.min(items.length, 10), getSettingsListTheme(), (id, newValue) => {
      switch (id) {
        case "anthropic-extra-usage":
          this.state = { ...this.state, anthropicExtraUsage: newValue === "true" };
          onChange({ ...this.state });
          break;
      }
    }, onCancel);
    this.addChild(this.settingsList);
  }
  handleInput(data) {
    this.settingsList.handleInput(data);
  }
};
var CLEAR_OVERRIDE_VALUE = "__clear__";
function modelSettingKey(model) {
  return `${model.provider}/${model.id}`;
}
__name(modelSettingKey, "modelSettingKey");
function modelDisplayLabel(model) {
  return `${model.id} [${model.provider}]`;
}
__name(modelDisplayLabel, "modelDisplayLabel");
function modelThinkingOverridesSummary(overrides) {
  const count = Object.keys(overrides).length;
  if (count === 0)
    return "none";
  return `${count} configured`;
}
__name(modelThinkingOverridesSummary, "modelThinkingOverridesSummary");
function modelItemLabel(model) {
  return `${model.id} ${theme.fg("muted", `[${model.provider}]`)}`;
}
__name(modelItemLabel, "modelItemLabel");
function themeItems(availableThemes, currentTheme) {
  return availableThemes.map((name) => ({
    value: name,
    label: `${name === currentTheme ? "\u2713 " : "  "}${name}`,
    ...name === SYSTEM_THEME_NAME ? { description: "Theme created from your terminal's colors" } : {}
  }));
}
__name(themeItems, "themeItems");
var AUTOMATIC_THEME_VALUE = "/";
function singleModeThemeItems(availableThemes, currentTheme) {
  const items = themeItems(availableThemes, currentTheme);
  const systemIndex = items.findIndex((item) => item.value === SYSTEM_THEME_NAME);
  const system = systemIndex === -1 ? [] : items.splice(systemIndex, 1);
  return [
    ...system,
    {
      value: AUTOMATIC_THEME_VALUE,
      label: "  automatic",
      description: "Use separate themes for light and dark terminal appearance"
    },
    ...items
  ];
}
__name(singleModeThemeItems, "singleModeThemeItems");
function preferredTheme(availableThemes, preferred, fallback) {
  if (preferred && availableThemes.includes(preferred))
    return preferred;
  if (availableThemes.includes(fallback))
    return fallback;
  return availableThemes[0] ?? fallback;
}
__name(preferredTheme, "preferredTheme");
function defaultAutomaticThemes(currentThemeSetting, availableThemes) {
  const autoTheme = parseAutoThemeSetting(currentThemeSetting);
  if (autoTheme)
    return autoTheme;
  const currentFixedTheme = currentThemeSetting.includes("/") ? void 0 : currentThemeSetting;
  const themeName = preferredTheme(availableThemes, currentFixedTheme, SYSTEM_THEME_NAME);
  return { lightTheme: themeName, darkTheme: themeName };
}
__name(defaultAutomaticThemes, "defaultAutomaticThemes");
var ThemeSubmenu = class extends Container22 {
  static {
    __name(this, "ThemeSubmenu");
  }
  inputComponent;
  callbacks;
  availableThemes;
  terminalTheme;
  onDone;
  originalThemeSetting;
  mode;
  singleTheme;
  lightTheme;
  darkTheme;
  constructor(currentThemeSetting, terminalTheme, availableThemes, callbacks, onDone) {
    super();
    this.callbacks = callbacks;
    this.availableThemes = availableThemes;
    this.terminalTheme = terminalTheme;
    this.onDone = onDone;
    this.originalThemeSetting = currentThemeSetting;
    const autoTheme = parseAutoThemeSetting(currentThemeSetting);
    const automaticThemes = defaultAutomaticThemes(currentThemeSetting, availableThemes);
    const fixedTheme = autoTheme || currentThemeSetting.includes("/") ? void 0 : currentThemeSetting;
    this.mode = autoTheme ? "automatic" : "single";
    this.lightTheme = automaticThemes.lightTheme;
    this.darkTheme = automaticThemes.darkTheme;
    this.singleTheme = preferredTheme(availableThemes, fixedTheme ?? (autoTheme ? this.getActiveAutomaticTheme() : void 0), SYSTEM_THEME_NAME);
    if (this.mode === "automatic") {
      this.showAutomaticMenu();
    } else {
      this.showSingleMenu();
    }
  }
  handleInput(data) {
    this.inputComponent?.handleInput?.(data);
  }
  setContent(renderComponent, inputComponent = renderComponent) {
    this.clear();
    this.addChild(renderComponent);
    this.inputComponent = inputComponent;
  }
  showSingleMenu() {
    this.mode = "single";
    const menu = new SelectSubmenu("Theme", "Select a theme, or choose automatic to follow terminal appearance.", singleModeThemeItems(this.availableThemes, this.singleTheme), this.singleTheme, (value) => {
      if (value === AUTOMATIC_THEME_VALUE) {
        this.mode = "automatic";
        this.callbacks.onThemePreview?.(this.getThemeSetting());
        this.showAutomaticMenu();
        return;
      }
      this.singleTheme = value;
      this.apply(value);
    }, () => this.cancel(), (value) => {
      this.callbacks.onThemePreview?.(value === AUTOMATIC_THEME_VALUE ? this.getAutomaticThemeSetting() : value);
    });
    this.setContent(menu);
  }
  showAutomaticMenu() {
    this.mode = "automatic";
    const content = new Container22();
    content.addChild(new Text22(theme.bold(theme.fg("accent", "Automatic Theme")), 0, 0));
    content.addChild(new Spacer21(1));
    content.addChild(new Text22(theme.fg("muted", "Choose themes for terminal light and dark appearance."), 0, 0));
    content.addChild(new Text22(theme.fg("muted", "Light/dark detection requires terminal support."), 0, 0));
    content.addChild(new Spacer21(1));
    const items = [
      {
        id: "light-theme",
        label: "Light theme",
        description: "Theme to use in automatic mode when the terminal is light",
        currentValue: this.lightTheme,
        submenu: /* @__PURE__ */ __name((currentValue, done) => this.createThemeSelect("Light Theme", "Select the theme to use for light terminal appearance", currentValue, done, (value) => {
          this.lightTheme = value;
          this.callbacks.onThemePreview?.(this.getThemeSetting());
          done(value);
        }), "submenu")
      },
      {
        id: "dark-theme",
        label: "Dark theme",
        description: "Theme to use in automatic mode when the terminal is dark",
        currentValue: this.darkTheme,
        submenu: /* @__PURE__ */ __name((currentValue, done) => this.createThemeSelect("Dark Theme", "Select the theme to use for dark terminal appearance", currentValue, done, (value) => {
          this.darkTheme = value;
          this.callbacks.onThemePreview?.(this.getThemeSetting());
          done(value);
        }), "submenu")
      },
      {
        id: "apply",
        label: "Apply",
        description: "Save and go back",
        currentValue: "save and go back",
        values: ["save and go back"]
      },
      {
        id: "single-mode",
        label: "Change mode",
        description: "Switch to one theme for light and dark",
        currentValue: "switch to single theme",
        values: ["switch to single theme"]
      }
    ];
    const settingsList = new SettingsList(items, Math.min(items.length, 10), getSettingsListTheme(), (id) => {
      switch (id) {
        case "single-mode":
          this.mode = "single";
          this.singleTheme = this.getActiveAutomaticTheme();
          this.callbacks.onThemePreview?.(this.singleTheme);
          this.showSingleMenu();
          break;
        case "apply":
          this.apply(this.getAutomaticThemeSetting());
          break;
      }
    }, () => this.cancel());
    content.addChild(settingsList);
    this.setContent(content, settingsList);
  }
  createThemeSelect(title, description, currentValue, done, onSelect) {
    return new SelectSubmenu(title, description, themeItems(this.availableThemes, currentValue), currentValue, onSelect, () => {
      this.callbacks.onThemePreview?.(this.getThemeSetting());
      done();
    }, (value) => this.callbacks.onThemePreview?.(value));
  }
  getThemeSetting() {
    return this.mode === "automatic" ? this.getAutomaticThemeSetting() : this.singleTheme;
  }
  getActiveAutomaticTheme() {
    return this.terminalTheme === "light" ? this.lightTheme : this.darkTheme;
  }
  getAutomaticThemeSetting() {
    return `${this.lightTheme}/${this.darkTheme}`;
  }
  apply(themeSetting) {
    this.onDone(themeSetting);
  }
  cancel() {
    this.callbacks.onThemePreview?.(this.originalThemeSetting);
    this.onDone();
  }
};
var SettingsSelectorComponent = class extends Container22 {
  static {
    __name(this, "SettingsSelectorComponent");
  }
  settingsList;
  constructor(config, callbacks) {
    super();
    const supportsImages = getCapabilities().images;
    const followUpKey = keyDisplayText("app.message.followUp");
    const cycleThinkingKey = keyDisplayText("app.thinking.cycle");
    let currentWarnings = { ...config.warnings };
    const currentModelThinkingLevels = { ...config.modelThinkingLevels };
    const defaultModelByValue = new Map(config.availableDefaultModels.map((model) => [modelSettingKey(model), model]));
    const currentDefaultModelKey = defaultModelByValue.has(config.defaultModel) ? config.defaultModel : void 0;
    const currentModelKey = config.currentModel ? modelSettingKey(config.currentModel) : void 0;
    const items = [
      {
        id: "autocompact",
        label: "Auto-compact",
        description: "Automatically compact context when it gets too large",
        currentValue: config.autoCompact ? "true" : "false",
        values: ["true", "false"]
      },
      {
        id: "steering-mode",
        label: "Steering mode",
        description: "Enter while streaming queues steering messages. 'one-at-a-time': deliver one, wait for response. 'all': deliver all at once.",
        currentValue: config.steeringMode,
        values: ["one-at-a-time", "all"]
      },
      {
        id: "follow-up-mode",
        label: "Follow-up mode",
        description: `${followUpKey} queues follow-up messages until agent stops. 'one-at-a-time': deliver one, wait for response. 'all': deliver all at once.`,
        currentValue: config.followUpMode,
        values: ["one-at-a-time", "all"]
      },
      {
        id: "transport",
        label: "Transport",
        description: "Preferred transport for providers that support multiple transports",
        currentValue: config.transport,
        values: ["sse", "websocket", "websocket-cached", "auto"]
      },
      {
        id: "http-idle-timeout",
        label: "HTTP idle timeout",
        description: "Maximum idle gap while waiting for HTTP headers or body chunks. Disable for local models that pause longer than five minutes.",
        currentValue: formatHttpIdleTimeoutMs(config.httpIdleTimeoutMs),
        values: HTTP_IDLE_TIMEOUT_CHOICES.map((choice) => choice.label)
      },
      {
        id: "cache-warming-mode",
        label: "Cache warming",
        description: "off; streaming while the agent runs; idle also between runs while continuation stays profitable",
        currentValue: config.cacheWarmingMode,
        values: [...CACHE_WARMING_MODES]
      },
      {
        id: "hide-thinking",
        label: "Hide thinking",
        description: "Hide thinking blocks in assistant responses",
        currentValue: config.hideThinkingBlock ? "true" : "false",
        values: ["true", "false"]
      },
      {
        id: "mermaid-rendering",
        label: "Mermaid diagrams",
        description: "Render Mermaid code blocks as Unicode diagrams",
        currentValue: config.mermaidRenderingMode,
        values: ["off", "final", "streaming"]
      },
      {
        id: "cache-miss-notices",
        label: "Cache miss notices",
        description: "Show transcript notices for cache costs and provider recovery diagnostics",
        currentValue: config.showCacheMissNotices ? "true" : "false",
        values: ["true", "false"]
      },
      {
        id: "collapse-changelog",
        label: "Collapse changelog",
        description: "Show condensed changelog after updates",
        currentValue: config.collapseChangelog ? "true" : "false",
        values: ["true", "false"]
      },
      {
        id: "quiet-startup",
        label: "Quiet startup",
        description: "Disable verbose printing at startup (header: keep only the startup header)",
        currentValue: String(config.quietStartup),
        values: ["true", "header", "false"]
      },
      {
        id: "install-telemetry",
        label: "Install telemetry",
        description: "Send an anonymous version/update ping after changelog-detected updates",
        currentValue: config.enableInstallTelemetry ? "true" : "false",
        values: ["true", "false"]
      },
      {
        id: "default-project-trust",
        label: "Default project trust",
        description: "Fallback behavior when no extension or saved trust decision decides project trust",
        currentValue: DEFAULT_PROJECT_TRUST_LABELS[config.defaultProjectTrust],
        values: Object.values(DEFAULT_PROJECT_TRUST_LABELS)
      },
      {
        id: "double-escape-action",
        label: "Double-escape action",
        description: "Action when pressing Escape twice with empty editor",
        currentValue: config.doubleEscapeAction,
        values: ["tree", "fork", "none"]
      },
      {
        id: "tree-filter-mode",
        label: "Tree filter mode",
        description: "Default filter when opening /tree",
        currentValue: config.treeFilterMode,
        values: ["default", "no-tools", "user-only", "labeled-only", "all"]
      },
      {
        id: "warnings",
        label: "Warnings",
        description: "Enable or disable individual warnings",
        currentValue: "configure",
        submenu: /* @__PURE__ */ __name((_currentValue, done) => new WarningSettingsSubmenu(currentWarnings, (warnings) => {
          currentWarnings = warnings;
          callbacks.onWarningsChange(warnings);
        }, () => done()), "submenu")
      },
      {
        id: "model-thinking",
        label: "Default thinking level per model",
        description: `Override the default thinking level for specific models. ${cycleThinkingKey} cycles in-session.`,
        currentValue: modelThinkingOverridesSummary(currentModelThinkingLevels),
        submenu: /* @__PURE__ */ __name((_currentValue, done) => {
          const steps = [
            {
              key: "model",
              title: "Per-Model Thinking Level",
              description: "Select a model to configure",
              options: /* @__PURE__ */ __name(() => {
                const sorted = [...config.availableDefaultModels].sort((a, b) => {
                  const aKey = modelSettingKey(a);
                  const bKey = modelSettingKey(b);
                  if (aKey === currentModelKey)
                    return -1;
                  if (bKey === currentModelKey)
                    return 1;
                  if (aKey === currentDefaultModelKey)
                    return -1;
                  if (bKey === currentDefaultModelKey)
                    return 1;
                  return a.provider.localeCompare(b.provider);
                });
                const items2 = sorted.map((model) => {
                  const key = modelSettingKey(model);
                  const override = currentModelThinkingLevels[key];
                  return {
                    value: key,
                    label: modelItemLabel(model),
                    description: override ?? void 0
                  };
                });
                if (items2.length === 0) {
                  items2.push({
                    value: "__none__",
                    label: "No models available",
                    description: "Log in to a provider or configure an API key first"
                  });
                }
                return items2;
              }, "options"),
              preselect: /* @__PURE__ */ __name(() => currentModelKey ?? currentDefaultModelKey, "preselect"),
              searchable: true,
              layout: MODEL_PICKER_LAYOUT
            },
            {
              key: "level",
              title: /* @__PURE__ */ __name((ctx) => {
                const m = defaultModelByValue.get(ctx.model);
                return `Thinking Level for ${m ? modelDisplayLabel(m) : ctx.model}`;
              }, "title"),
              description: "Select default thinking level for this model",
              options: /* @__PURE__ */ __name((ctx) => {
                const model = defaultModelByValue.get(ctx.model);
                if (!model)
                  return [];
                const levels = model.reasoning ? getSupportedThinkingLevels(model) : ["off"];
                const activeLevel = currentModelThinkingLevels[ctx.model];
                const items2 = levels.map((level) => ({
                  value: level,
                  label: `${level === activeLevel ? "\u2713 " : "  "}${level}`,
                  description: THINKING_DESCRIPTIONS[level]
                }));
                if (currentModelThinkingLevels[ctx.model] !== void 0) {
                  items2.push({
                    value: CLEAR_OVERRIDE_VALUE,
                    label: "  (clear override)",
                    description: `Revert to global default (${config.thinkingLevel})`
                  });
                }
                return items2;
              }, "options"),
              preselect: /* @__PURE__ */ __name((ctx) => currentModelThinkingLevels[ctx.model], "preselect")
            }
          ];
          const summary = /* @__PURE__ */ __name(() => modelThinkingOverridesSummary(currentModelThinkingLevels), "summary");
          return new SteppedSubmenu(steps, (selections) => {
            const model = defaultModelByValue.get(selections.model);
            if (!model)
              return;
            if (selections.level === CLEAR_OVERRIDE_VALUE) {
              callbacks.onModelThinkingLevelRemove(model.provider, model.id);
              delete currentModelThinkingLevels[selections.model];
            } else {
              callbacks.onModelThinkingLevelChange(model.provider, model.id, selections.level);
              currentModelThinkingLevels[selections.model] = selections.level;
            }
          }, () => {
            done(summary());
          }, { loop: true });
        }, "submenu")
      },
      {
        id: "tui-mode",
        label: "TUI mode",
        description: "Interface layout; regular mode uses the terminal's normal scrollback",
        currentValue: config.tuiMode,
        values: ["regular", "fullscreen"]
      },
      {
        id: "fullscreen-exit-output",
        label: "Fullscreen exit output",
        description: "Print the transcript or only a session resume hint when exiting fullscreen mode",
        currentValue: config.fullscreenExitOutput,
        values: ["transcript", "resume-hint"]
      },
      {
        id: "fullscreen-scrollbar",
        label: "Fullscreen scrollbar",
        description: "Scrollbar behavior in fullscreen mode; has no effect in regular mode",
        currentValue: config.fullscreenScrollbar,
        values: ["auto", "always", "hidden"]
      },
      {
        id: "fullscreen-copy-on-select",
        label: "Fullscreen copy on select",
        description: "Automatically copy selected text in fullscreen mode; disable to copy selections with Ctrl+X",
        currentValue: config.fullscreenCopyOnSelect ? "true" : "false",
        values: ["true", "false"]
      },
      {
        id: "fullscreen-wheel-scroll-lines",
        label: "Fullscreen wheel scrolling",
        description: "Lines per mouse-wheel event in fullscreen mode; 'auto' speeds up fast wheel spins where the terminal does not",
        currentValue: String(config.fullscreenWheelScrollLines),
        values: [
          "auto",
          ...[.../* @__PURE__ */ new Set([1, 2, 3, 5, 10, config.fullscreenWheelScrollLines])].filter((lines) => lines !== "auto").sort((a, b) => a - b).map(String)
        ]
      },
      {
        id: "theme",
        label: "Theme",
        description: "Color theme for the interface",
        currentValue: config.currentTheme,
        submenu: /* @__PURE__ */ __name((currentValue, done) => new ThemeSubmenu(currentValue, config.terminalTheme, config.availableThemes, callbacks, done), "submenu")
      }
    ];
    if (supportsImages) {
      items.splice(1, 0, {
        id: "show-images",
        label: "Show images",
        description: "Render images inline in terminal",
        currentValue: config.showImages ? "true" : "false",
        values: ["true", "false"]
      });
      items.splice(2, 0, {
        id: "image-width-cells",
        label: "Image width",
        description: "Preferred inline image width in terminal cells",
        currentValue: String(config.imageWidthCells),
        values: ["60", "80", "120"]
      });
    }
    items.splice(supportsImages ? 3 : 1, 0, {
      id: "auto-resize-images",
      label: "Auto-resize images",
      description: "Resize large images to 2000x2000 max for better model compatibility",
      currentValue: config.autoResizeImages ? "true" : "false",
      values: ["true", "false"]
    });
    const autoResizeIndex = items.findIndex((item) => item.id === "auto-resize-images");
    items.splice(autoResizeIndex + 1, 0, {
      id: "block-images",
      label: "Block images",
      description: "Prevent images from being sent to LLM providers",
      currentValue: config.blockImages ? "true" : "false",
      values: ["true", "false"]
    });
    const blockImagesIndex = items.findIndex((item) => item.id === "block-images");
    items.splice(blockImagesIndex + 1, 0, {
      id: "skill-commands",
      label: "Skill commands",
      description: "Register skills as /skill:name commands",
      currentValue: config.enableSkillCommands ? "true" : "false",
      values: ["true", "false"]
    });
    const skillCommandsIndex = items.findIndex((item) => item.id === "skill-commands");
    items.splice(skillCommandsIndex + 1, 0, {
      id: "show-hardware-cursor",
      label: "Show hardware cursor",
      description: "Show the terminal cursor while still positioning it for IME support",
      currentValue: config.showHardwareCursor ? "true" : "false",
      values: ["true", "false"]
    });
    const hardwareCursorIndex = items.findIndex((item) => item.id === "show-hardware-cursor");
    items.splice(hardwareCursorIndex + 1, 0, {
      id: "editor-padding",
      label: "Editor padding",
      description: "Horizontal padding for input editor (0-3)",
      currentValue: String(config.editorPaddingX),
      values: ["0", "1", "2", "3"]
    });
    const editorPaddingIndex = items.findIndex((item) => item.id === "editor-padding");
    items.splice(editorPaddingIndex + 1, 0, {
      id: "output-padding",
      label: "Output padding",
      description: "Horizontal padding for messages, tool output, and command output",
      currentValue: String(config.outputPad),
      values: ["0", "1"]
    });
    const outputPaddingIndex = items.findIndex((item) => item.id === "output-padding");
    items.splice(outputPaddingIndex + 1, 0, {
      id: "autocomplete-max-visible",
      label: "Autocomplete max items",
      description: "Max visible items in autocomplete dropdown (3-20)",
      currentValue: String(config.autocompleteMaxVisible),
      values: ["3", "5", "7", "10", "15", "20"]
    });
    const autocompleteIndex = items.findIndex((item) => item.id === "autocomplete-max-visible");
    items.splice(autocompleteIndex + 1, 0, {
      id: "clear-on-shrink",
      label: "Clear on shrink",
      description: "Clear empty rows when content shrinks (may cause flicker)",
      currentValue: config.clearOnShrink ? "true" : "false",
      values: ["true", "false"]
    });
    const clearOnShrinkIndex = items.findIndex((item) => item.id === "clear-on-shrink");
    items.splice(clearOnShrinkIndex + 1, 0, {
      id: "terminal-progress",
      label: "Terminal progress",
      description: "Show OSC 9;4 progress indicators in the terminal tab bar",
      currentValue: config.showTerminalProgress ? "true" : "false",
      values: ["true", "false"]
    });
    this.addChild(new DynamicBorder());
    this.settingsList = new SettingsList(items, 10, getSettingsListTheme(), (id, newValue) => {
      switch (id) {
        case "autocompact":
          callbacks.onAutoCompactChange(newValue === "true");
          break;
        case "show-images":
          callbacks.onShowImagesChange(newValue === "true");
          break;
        case "image-width-cells":
          callbacks.onImageWidthCellsChange(parseInt(newValue, 10));
          break;
        case "auto-resize-images":
          callbacks.onAutoResizeImagesChange(newValue === "true");
          break;
        case "block-images":
          callbacks.onBlockImagesChange(newValue === "true");
          break;
        case "skill-commands":
          callbacks.onEnableSkillCommandsChange(newValue === "true");
          break;
        case "steering-mode":
          callbacks.onSteeringModeChange(newValue);
          break;
        case "follow-up-mode":
          callbacks.onFollowUpModeChange(newValue);
          break;
        case "transport":
          callbacks.onTransportChange(newValue);
          break;
        case "http-idle-timeout": {
          const choice = HTTP_IDLE_TIMEOUT_CHOICES.find((item) => item.label === newValue);
          if (choice) {
            callbacks.onHttpIdleTimeoutMsChange(choice.timeoutMs);
          }
          break;
        }
        case "cache-warming-mode":
          callbacks.onCacheWarmingModeChange(newValue);
          break;
        case "hide-thinking":
          callbacks.onHideThinkingBlockChange(newValue === "true");
          break;
        case "mermaid-rendering":
          callbacks.onMermaidRenderingModeChange(newValue);
          break;
        case "cache-miss-notices":
          callbacks.onShowCacheMissNoticesChange(newValue === "true");
          break;
        case "collapse-changelog":
          callbacks.onCollapseChangelogChange(newValue === "true");
          break;
        case "quiet-startup":
          callbacks.onQuietStartupChange(newValue === "header" ? "header" : newValue === "true");
          break;
        case "install-telemetry":
          callbacks.onEnableInstallTelemetryChange(newValue === "true");
          break;
        case "default-project-trust": {
          const defaultProjectTrust = DEFAULT_PROJECT_TRUST_BY_LABEL.get(newValue);
          if (defaultProjectTrust) {
            callbacks.onDefaultProjectTrustChange(defaultProjectTrust);
          }
          break;
        }
        case "double-escape-action":
          callbacks.onDoubleEscapeActionChange(newValue);
          break;
        case "tree-filter-mode":
          callbacks.onTreeFilterModeChange(newValue);
          break;
        case "show-hardware-cursor":
          callbacks.onShowHardwareCursorChange(newValue === "true");
          break;
        case "editor-padding":
          callbacks.onEditorPaddingXChange(parseInt(newValue, 10));
          break;
        case "output-padding":
          callbacks.onOutputPadChange(newValue === "0" ? 0 : 1);
          break;
        case "autocomplete-max-visible":
          callbacks.onAutocompleteMaxVisibleChange(parseInt(newValue, 10));
          break;
        case "clear-on-shrink":
          callbacks.onClearOnShrinkChange(newValue === "true");
          break;
        case "terminal-progress":
          callbacks.onShowTerminalProgressChange(newValue === "true");
          break;
        case "tui-mode":
          callbacks.onTuiModeChange(newValue);
          break;
        case "fullscreen-exit-output":
          callbacks.onFullscreenExitOutputChange(newValue);
          break;
        case "fullscreen-scrollbar":
          callbacks.onFullscreenScrollbarChange(newValue);
          break;
        case "fullscreen-copy-on-select":
          callbacks.onFullscreenCopyOnSelectChange(newValue === "true");
          break;
        case "fullscreen-wheel-scroll-lines":
          callbacks.onFullscreenWheelScrollLinesChange(newValue === "auto" ? "auto" : parseInt(newValue, 10));
          break;
        case "theme":
          callbacks.onThemeChange(newValue);
          break;
      }
    }, callbacks.onCancel, { enableSearch: true });
    this.addChild(this.settingsList);
    this.addChild(new DynamicBorder());
  }
  getSettingsList() {
    return this.settingsList;
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/skill-invocation-message.js
import { Box as Box5, Container as Container23, Markdown as Markdown5, MouseRegion as MouseRegion4, Text as Text23 } from "../../../pi-tui.mjs";
var SkillInvocationMessageComponent = class extends Box5 {
  static {
    __name(this, "SkillInvocationMessageComponent");
  }
  expanded = false;
  skillBlock;
  markdownTheme;
  constructor(skillBlock, markdownTheme = getMarkdownTheme(), outputPad = 1) {
    super(outputPad, 1, (t) => theme.bg("customMessageBg", t));
    this.skillBlock = skillBlock;
    this.markdownTheme = markdownTheme;
    this.updateDisplay();
  }
  setExpanded(expanded) {
    this.expanded = expanded;
    this.updateDisplay();
  }
  setOutputPad(outputPad) {
    this.setPaddingX(outputPad);
  }
  invalidate() {
    super.invalidate();
    this.updateDisplay();
  }
  updateDisplay() {
    this.clear();
    const content = new Container23();
    if (this.expanded) {
      const label = theme.fg("customMessageLabel", `\x1B[1m[skill]\x1B[22m`);
      content.addChild(new Text23(label, 0, 0));
      const header = `**${this.skillBlock.name}**

`;
      content.addChild(new Markdown5(header + this.skillBlock.content, 0, 0, this.markdownTheme, {
        color: /* @__PURE__ */ __name((text) => theme.fg("customMessageText", text), "color")
      }));
    } else {
      const line = theme.fg("customMessageLabel", `\x1B[1m[skill]\x1B[22m `) + theme.fg("customMessageText", this.skillBlock.name) + theme.fg("dim", ` (${keyText("app.tools.expand")} to expand)`);
      content.addChild(new Text23(line, 0, 0));
    }
    this.addChild(new MouseRegion4(content, (event) => {
      if (event.type !== "click" || event.button !== "left")
        return void 0;
      this.setExpanded(!this.expanded);
      return { handled: true };
    }));
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/status-indicator.js
import { Loader as Loader3, truncateToWidth as truncateToWidth5 } from "../../../pi-tui.mjs";
var StatusIndicator = class extends Loader3 {
  static {
    __name(this, "StatusIndicator");
  }
  kind;
  constructor(kind, ui, spinnerColorFn, messageColorFn, message, indicator) {
    super(ui, spinnerColorFn, messageColorFn, message, indicator);
    this.kind = kind;
  }
  renderInBorder(width) {
    const line = super.render(width + 2)[1] ?? "";
    return truncateToWidth5(line.startsWith(" ") ? line.slice(1).trimEnd() : line.trimEnd(), width, "");
  }
  renderSpinnerInBorder(width) {
    return truncateToWidth5(this.getRenderedIndicator(), width, "");
  }
  dispose() {
    this.stop();
  }
};
var WorkingStatusIndicator = class extends StatusIndicator {
  static {
    __name(this, "WorkingStatusIndicator");
  }
  constructor(ui, message, indicator, colorFn) {
    super("working", ui, colorFn ?? ((text) => theme.fg("accent", text)), colorFn ?? ((text) => theme.fg("muted", text)), message, indicator);
  }
};
var RetryStatusIndicator = class extends StatusIndicator {
  static {
    __name(this, "RetryStatusIndicator");
  }
  countdown;
  constructor(ui, attempt, maxAttempts, delayMs) {
    const retryMessage = /* @__PURE__ */ __name((seconds) => `Retrying (${attempt}/${maxAttempts}) in ${seconds}s... (${keyText("app.interrupt")} to cancel)`, "retryMessage");
    super("retry", ui, (spinner) => theme.fg("warning", spinner), (text) => theme.fg("muted", text), retryMessage(Math.ceil(delayMs / 1e3)));
    this.countdown = new CountdownTimer(delayMs, ui, (seconds) => {
      this.setMessage(retryMessage(seconds));
    }, () => {
      this.countdown = void 0;
    });
  }
  dispose() {
    this.countdown?.dispose();
    this.countdown = void 0;
    super.dispose();
  }
};
var CompactionStatusIndicator = class extends StatusIndicator {
  static {
    __name(this, "CompactionStatusIndicator");
  }
  constructor(ui, reason) {
    const cancelHint = `(${keyText("app.interrupt")} to cancel)`;
    const label = reason === "manual" ? `Compacting context... ${cancelHint}` : `${reason === "overflow" ? "Context overflow detected, " : ""}Auto-compacting... ${cancelHint}`;
    super("compaction", ui, (spinner) => theme.fg("accent", spinner), (text) => theme.fg("muted", text), label);
  }
};
var BranchSummaryStatusIndicator = class extends StatusIndicator {
  static {
    __name(this, "BranchSummaryStatusIndicator");
  }
  constructor(ui) {
    super("branchSummary", ui, (spinner) => theme.fg("accent", spinner), (text) => theme.fg("muted", text), `Summarizing branch... (${keyText("app.interrupt")} to cancel)`);
  }
};
var IdleStatus = class {
  static {
    __name(this, "IdleStatus");
  }
  invalidate() {
  }
  render(width) {
    const emptyLine = " ".repeat(width);
    return [emptyLine, emptyLine];
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/themed-text.js
import { Text as Text24 } from "../../../pi-tui.mjs";
var ThemedText = class extends Text24 {
  static {
    __name(this, "ThemedText");
  }
  build;
  stale = true;
  constructor(build, paddingX = 1, paddingY = 1) {
    super("", paddingX, paddingY);
    this.build = build;
  }
  invalidate() {
    super.invalidate();
    this.stale = true;
  }
  render(width) {
    if (this.stale) {
      this.stale = false;
      this.setText(this.build());
    }
    return super.render(width);
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/thinking-selector.js
import { Container as Container24, fuzzyFilter as fuzzyFilter7, getKeybindings as getKeybindings11, Input as Input10, SelectList as SelectList4, Spacer as Spacer22, Text as Text25 } from "../../../pi-tui.mjs";
var THINKING_SELECT_LIST_LAYOUT = {
  minPrimaryColumnWidth: 12,
  maxPrimaryColumnWidth: 32
};
var LEVEL_DESCRIPTIONS = {
  off: "No reasoning",
  minimal: "Very brief reasoning (~1k tokens)",
  low: "Light reasoning (~2k tokens)",
  medium: "Moderate reasoning (~8k tokens)",
  high: "Deep reasoning (~16k tokens)",
  xhigh: "Extra-high reasoning (~32k tokens)",
  max: "Maximum reasoning"
};
var ThinkingSelectorComponent = class extends Container24 {
  static {
    __name(this, "ThinkingSelectorComponent");
  }
  searchInput;
  selectList;
  selectListChildIndex;
  allItems;
  onSelect;
  onCancel;
  onSelectAsDefault;
  _focused = false;
  get focused() {
    return this._focused;
  }
  set focused(value) {
    this._focused = value;
    this.searchInput.focused = value;
  }
  constructor(currentLevel, availableLevels, onSelect, onCancel, onSelectAsDefault, defaultThinkingLevel) {
    super();
    this.onSelect = onSelect;
    this.onCancel = onCancel;
    this.onSelectAsDefault = onSelectAsDefault;
    this.allItems = availableLevels.map((level) => ({
      value: level,
      label: `${level === currentLevel ? "\u2713 " : "  "}${level}`,
      description: level === defaultThinkingLevel ? `${LEVEL_DESCRIPTIONS[level]} \xB7 default` : LEVEL_DESCRIPTIONS[level]
    }));
    this.addChild(new DynamicBorder());
    this.addChild(new Spacer22(1));
    this.addChild(new Text25("Thinking Level", 0, 0));
    this.addChild(new Spacer22(1));
    this.addChild(new Text25(`${keyDisplayText("app.thinking.cycle")} cycles thinking levels in-session`, 0, 0));
    this.addChild(new Spacer22(1));
    this.searchInput = new Input10();
    this.searchInput.onSubmit = () => this.selectList.handleInput("\r");
    this.addChild(this.searchInput);
    this.addChild(new Spacer22(1));
    this.selectList = this.buildSelectList(this.allItems, currentLevel);
    this.selectListChildIndex = this.children.length;
    this.addChild(this.selectList);
    this.addChild(new Spacer22(1));
    this.addChild(new Text25(theme.fg("dim", `  ${keyDisplayText("tui.select.confirm")} to select \xB7 ${keyDisplayText("app.thinking.save")} to set as default \xB7 ${keyDisplayText("tui.select.cancel")} to cancel`), 0, 0));
    this.addChild(new DynamicBorder());
  }
  buildSelectList(items, preselect) {
    const list = new SelectList4(items, Math.max(1, items.length), getSelectListTheme(), THINKING_SELECT_LIST_LAYOUT);
    const currentIndex = items.findIndex((item) => item.value === preselect);
    if (currentIndex !== -1) {
      list.setSelectedIndex(currentIndex);
    }
    list.onSelect = (item) => this.onSelect(item.value);
    list.onCancel = () => this.onCancel();
    return list;
  }
  applyFilter(query) {
    const filtered = query ? fuzzyFilter7(this.allItems, query, (item) => `${item.value} ${item.description ?? ""}`) : this.allItems;
    const selectedValue = this.selectList.getSelectedItem()?.value;
    const newList = this.buildSelectList(filtered, selectedValue);
    this.children[this.selectListChildIndex] = newList;
    this.selectList = newList;
  }
  handleInput(keyData) {
    const kb = getKeybindings11();
    if (kb.matches(keyData, "app.thinking.save") && this.onSelectAsDefault) {
      const item = this.selectList.getSelectedItem();
      if (item)
        this.onSelectAsDefault(item.value);
      return;
    }
    const isNav = kb.matches(keyData, "tui.select.up") || kb.matches(keyData, "tui.select.down") || kb.matches(keyData, "tui.select.confirm") || kb.matches(keyData, "tui.select.cancel");
    if (isNav) {
      this.selectList.handleInput(keyData);
      return;
    }
    this.searchInput.handleInput(keyData);
    this.applyFilter(this.searchInput.getValue());
  }
  getSelectList() {
    return this.selectList;
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/tool-execution.js
import { Box as Box6, Container as Container25, getCapabilities as getCapabilities2, Image as Image2, MouseRegion as MouseRegion5, Spacer as Spacer23, Text as Text26 } from "../../../pi-tui.mjs";
var FALLBACK_PREVIEW_LINES = 10;
var ToolExecutionComponent = class extends Container25 {
  static {
    __name(this, "ToolExecutionComponent");
  }
  contentBox;
  contentText;
  contentTextRegion;
  selfRenderContainer;
  selfRenderHeight = 0;
  callRendererComponent;
  resultRendererComponent;
  rendererState = {};
  imageComponents = [];
  /** Inputs of imageComponents, so updateDisplay can reuse images and keep their converted PNG data. */
  imageSources = [];
  imageSpacers = [];
  toolName;
  toolCallId;
  args;
  expanded = false;
  showImages;
  imageWidthCells;
  outputPad;
  isPartial = true;
  toolDefinition;
  ui;
  cwd;
  executionStarted = false;
  argsComplete = false;
  result;
  hideComponent = false;
  constructor(toolName, toolCallId, args, options = {}, toolDefinition, ui, cwd) {
    super();
    this.toolName = toolName;
    this.toolCallId = toolCallId;
    this.args = args;
    this.toolDefinition = toolDefinition;
    this.showImages = options.showImages ?? true;
    this.imageWidthCells = options.imageWidthCells ?? 60;
    this.outputPad = options.outputPad ?? 1;
    this.ui = ui;
    this.cwd = cwd;
    this.addChild(new Spacer23(1));
    this.contentBox = new Box6(1, 1, (text) => theme.bg("toolPendingBg", text));
    this.contentText = new Text26("", 1, 1, (text) => theme.bg("toolPendingBg", text));
    this.contentTextRegion = this.createResultRegion(this.contentText);
    this.selfRenderContainer = new Container25();
    if (this.hasRendererDefinition()) {
      this.addChild(this.getRenderShell() === "self" ? this.selfRenderContainer : this.contentBox);
    } else {
      this.addChild(this.contentTextRegion);
    }
    this.updateDisplay();
  }
  getCallRenderer() {
    return this.toolDefinition?.renderCall;
  }
  getResultRenderer() {
    return this.toolDefinition?.renderResult;
  }
  hasRendererDefinition() {
    return this.toolDefinition !== void 0;
  }
  getRenderShell() {
    return this.toolDefinition?.renderShell ?? "default";
  }
  getRenderContext(lastComponent) {
    return {
      args: this.args,
      toolCallId: this.toolCallId,
      invalidate: /* @__PURE__ */ __name(() => {
        this.invalidate();
        this.ui.requestRender();
      }, "invalidate"),
      lastComponent,
      state: this.rendererState,
      cwd: this.cwd,
      executionStarted: this.executionStarted,
      argsComplete: this.argsComplete,
      isPartial: this.isPartial,
      expanded: this.expanded,
      showImages: this.showImages,
      isError: this.result?.isError ?? false,
      durationMs: this.isPartial ? void 0 : this.result?.durationMs,
      outputPad: this.outputPad
    };
  }
  createCallFallback() {
    return new Text26(formatToolCallWithArgs(this.toolName, this.args, theme, this.expanded), 0, 0);
  }
  createResultFallback() {
    const output = this.getTextOutput();
    if (!output) {
      return void 0;
    }
    const lines = output.split("\n");
    const displayLines = this.expanded ? lines : lines.slice(0, FALLBACK_PREVIEW_LINES);
    const remaining = lines.length - displayLines.length;
    let text = displayLines.map((line) => theme.fg("toolOutput", line)).join("\n");
    if (remaining > 0) {
      text += `${theme.fg("muted", `
... (${remaining} more lines,`)} ${keyHint("app.tools.expand", "to expand")}${theme.fg("muted", ")")}`;
    }
    return new Text26(text, 0, 0);
  }
  createResultRegion(component) {
    return new MouseRegion5(component, (event) => {
      if (!this.result || event.type !== "click" || event.button !== "left")
        return void 0;
      this.setExpanded(!this.expanded);
      return { handled: true };
    });
  }
  updateArgs(args) {
    this.args = args;
    this.updateDisplay();
  }
  markExecutionStarted() {
    this.executionStarted = true;
    this.updateDisplay();
    this.ui.requestRender();
  }
  setArgsComplete() {
    this.argsComplete = true;
    this.updateDisplay();
    this.ui.requestRender();
  }
  updateResult(result, isPartial = false) {
    this.result = result;
    this.isPartial = isPartial;
    this.updateDisplay();
  }
  setExpanded(expanded) {
    this.expanded = expanded;
    this.updateDisplay();
  }
  setOutputPad(outputPad) {
    this.outputPad = outputPad;
    this.updateDisplay();
  }
  setShowImages(show) {
    this.showImages = show;
    this.updateDisplay();
  }
  setImageWidthCells(width) {
    this.imageWidthCells = Math.max(1, Math.floor(width));
    this.updateDisplay();
  }
  invalidate() {
    super.invalidate();
    this.updateDisplay();
  }
  render(width) {
    if (this.hideComponent) {
      return [];
    }
    if (this.hasRendererDefinition() && this.getRenderShell() === "self") {
      const contentLines = this.selfRenderContainer.render(width);
      this.selfRenderHeight = contentLines.length;
      if (contentLines.length === 0 && this.imageComponents.length === 0) {
        return [];
      }
      const lines = [];
      if (contentLines.length > 0) {
        lines.push("");
        lines.push(...contentLines);
      }
      for (let i = 0; i < this.imageComponents.length; i++) {
        const spacer = this.imageSpacers[i];
        if (spacer) {
          lines.push(...spacer.render(width));
        }
        const imageComponent = this.imageComponents[i];
        if (imageComponent) {
          lines.push(...imageComponent.render(width));
        }
      }
      return lines;
    }
    return super.render(width);
  }
  handleMouse(event) {
    if (!this.hasRendererDefinition() || this.getRenderShell() !== "self")
      return super.handleMouse(event);
    if (event.y <= 0 || event.y > this.selfRenderHeight)
      return void 0;
    return this.selfRenderContainer.handleMouse({
      ...event,
      y: event.y - 1,
      height: this.selfRenderHeight
    });
  }
  updateDisplay() {
    const bgFn = this.isPartial ? (text) => theme.bg("toolPendingBg", text) : this.result?.isError ? (text) => theme.bg("toolErrorBg", text) : (text) => theme.bg("toolSuccessBg", text);
    let hasContent = false;
    this.hideComponent = false;
    if (this.hasRendererDefinition()) {
      const renderContainer = this.getRenderShell() === "self" ? this.selfRenderContainer : this.contentBox;
      if (renderContainer instanceof Box6) {
        renderContainer.setBgFn(bgFn);
        renderContainer.setPaddingX(this.outputPad);
      }
      renderContainer.clear();
      const callRenderer = this.getCallRenderer();
      if (!callRenderer) {
        renderContainer.addChild(this.createResultRegion(this.createCallFallback()));
        hasContent = true;
      } else {
        try {
          const component = callRenderer(this.args, theme, this.getRenderContext(this.callRendererComponent));
          this.callRendererComponent = component;
          renderContainer.addChild(this.createResultRegion(component));
          hasContent = true;
        } catch {
          this.callRendererComponent = void 0;
          renderContainer.addChild(this.createResultRegion(this.createCallFallback()));
          hasContent = true;
        }
      }
      if (this.result) {
        const resultRenderer = this.getResultRenderer();
        if (!resultRenderer) {
          const component = this.createResultFallback();
          if (component) {
            renderContainer.addChild(this.createResultRegion(component));
            hasContent = true;
          }
        } else {
          try {
            const component = resultRenderer({ content: this.result.content, details: this.result.details }, { expanded: this.expanded, isPartial: this.isPartial }, theme, this.getRenderContext(this.resultRendererComponent));
            this.resultRendererComponent = component;
            renderContainer.addChild(this.createResultRegion(component));
            hasContent = true;
          } catch {
            this.resultRendererComponent = void 0;
            const component = this.createResultFallback();
            if (component) {
              renderContainer.addChild(this.createResultRegion(component));
              hasContent = true;
            }
          }
        }
      }
    } else {
      this.contentText.setCustomBgFn(bgFn);
      this.contentText.setPaddingX(this.outputPad);
      this.contentText.setText(this.formatToolExecution());
      hasContent = true;
    }
    const previousImages = this.imageComponents;
    const previousSources = this.imageSources;
    for (const img of this.imageComponents) {
      this.removeChild(img);
    }
    this.imageComponents = [];
    this.imageSources = [];
    for (const spacer of this.imageSpacers) {
      this.removeChild(spacer);
    }
    this.imageSpacers = [];
    if (this.result) {
      const imageBlocks = this.result.content.filter((c2) => c2.type === "image");
      const caps = getCapabilities2();
      for (const img of imageBlocks) {
        if (caps.images && this.showImages && img.data && img.mimeType) {
          const spacer = new Spacer23(1);
          this.addChild(spacer);
          this.imageSpacers.push(spacer);
          const source = { data: img.data, mimeType: img.mimeType, widthCells: this.imageWidthCells };
          const index = this.imageComponents.length;
          const previous = previousSources[index];
          const imageComponent = previous?.data === source.data && previous.mimeType === source.mimeType && previous.widthCells === source.widthCells ? previousImages[index] : new Image2(source.data, source.mimeType, { fallbackColor: /* @__PURE__ */ __name((s) => theme.fg("toolOutput", s), "fallbackColor") }, { maxWidthCells: source.widthCells });
          if (source.mimeType !== "image/png") {
            ensurePngTranscoder(() => {
              this.invalidate();
              this.ui.requestRender();
            });
          }
          this.imageComponents.push(imageComponent);
          this.imageSources.push(source);
          this.addChild(imageComponent);
        }
      }
    }
    if (this.hasRendererDefinition() && !hasContent && this.imageComponents.length === 0) {
      this.hideComponent = true;
    }
  }
  getTextOutput() {
    return getTextOutput(this.result, this.showImages);
  }
  formatToolExecution() {
    let text = theme.fg("toolTitle", theme.bold(this.toolName));
    const content = JSON.stringify(this.args, null, 2);
    if (content) {
      text += `

${content}`;
    }
    const output = this.getTextOutput();
    if (output) {
      text += `
${output}`;
    }
    return text;
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/tree-selector.js
import { Container as Container26, getKeybindings as getKeybindings12, Input as Input11, Spacer as Spacer24, sliceByColumn, Text as Text27, truncateToWidth as truncateToWidth6, visibleWidth as visibleWidth6, wrapTextWithAnsi } from "../../../pi-tui.mjs";
var TREE_GUTTER_WIDTH = 2;
var MIN_VISIBLE_ANCHOR_CONTENT_WIDTH = 4;
var MAX_VISIBLE_ANCHOR_CONTENT_WIDTH = 20;
var MIN_ANCHOR_CONTEXT_WIDTH = 2;
var MAX_ANCHOR_CONTEXT_WIDTH = 12;
function renderHorizontalViewport(rows, width) {
  const viewportWidth = Math.max(0, width - TREE_GUTTER_WIDTH);
  const maxBodyWidth = rows.reduce((max, row) => Math.max(max, row.bodyWidth), 0);
  const maxHorizontalScroll = Math.max(0, maxBodyWidth - viewportWidth);
  const selectedRow = rows.find((row) => row.isSelected);
  let horizontalScroll = 0;
  if (selectedRow && maxHorizontalScroll > 0) {
    const minVisibleAnchorContentWidth = Math.min(MAX_VISIBLE_ANCHOR_CONTENT_WIDTH, Math.max(MIN_VISIBLE_ANCHOR_CONTENT_WIDTH, Math.floor(viewportWidth / 3)));
    if (selectedRow.anchorCol > viewportWidth - minVisibleAnchorContentWidth) {
      const anchorContextWidth = Math.min(MAX_ANCHOR_CONTEXT_WIDTH, Math.max(MIN_ANCHOR_CONTEXT_WIDTH, Math.floor(viewportWidth / 4)));
      horizontalScroll = Math.min(maxHorizontalScroll, selectedRow.anchorCol - anchorContextWidth);
    }
  }
  return rows.map((row) => {
    const line = horizontalScroll > 0 ? `${row.gutter}${sliceByColumn(row.body, horizontalScroll, viewportWidth, true)}\x1B[0m` : row.gutter + row.body;
    return truncateToWidth6(line, width, "");
  });
}
__name(renderHorizontalViewport, "renderHorizontalViewport");
var TreeList = class {
  static {
    __name(this, "TreeList");
  }
  flatNodes = [];
  filteredNodes = [];
  selectedIndex = 0;
  currentLeafId;
  maxVisibleLines;
  filterMode = "default";
  searchQuery = "";
  toolCallMap = /* @__PURE__ */ new Map();
  multipleRoots = false;
  showLabelTimestamps = false;
  activePathIds = /* @__PURE__ */ new Set();
  visibleParentMap = /* @__PURE__ */ new Map();
  visibleChildrenMap = /* @__PURE__ */ new Map();
  lastSelectedId = null;
  foldedNodes = /* @__PURE__ */ new Set();
  onSelect;
  onCancel;
  onCopy;
  onLabelEdit;
  constructor(tree, currentLeafId, maxVisibleLines, initialSelectedId, initialFilterMode) {
    this.currentLeafId = currentLeafId;
    this.maxVisibleLines = maxVisibleLines;
    this.filterMode = initialFilterMode ?? "default";
    this.multipleRoots = tree.length > 1;
    this.flatNodes = this.flattenTree(tree);
    this.buildActivePath();
    this.applyFilter();
    const targetId = initialSelectedId ?? currentLeafId;
    this.selectedIndex = this.findNearestVisibleIndex(targetId);
    this.lastSelectedId = this.filteredNodes[this.selectedIndex]?.node.entry.id ?? null;
  }
  /**
   * Find the index of the nearest visible entry, walking up the parent chain if needed.
   * Returns the index in filteredNodes, or the last index as fallback.
   */
  findNearestVisibleIndex(entryId) {
    if (this.filteredNodes.length === 0)
      return 0;
    const entryMap = /* @__PURE__ */ new Map();
    for (const flatNode of this.flatNodes) {
      entryMap.set(flatNode.node.entry.id, flatNode);
    }
    const visibleIdToIndex = new Map(this.filteredNodes.map((node, i) => [node.node.entry.id, i]));
    let currentId = entryId;
    while (currentId !== null) {
      const index = visibleIdToIndex.get(currentId);
      if (index !== void 0)
        return index;
      const node = entryMap.get(currentId);
      if (!node)
        break;
      currentId = node.node.entry.parentId ?? null;
    }
    return this.filteredNodes.length - 1;
  }
  /** Build the set of entry IDs on the path from root to current leaf */
  buildActivePath() {
    this.activePathIds.clear();
    if (!this.currentLeafId)
      return;
    const entryMap = /* @__PURE__ */ new Map();
    for (const flatNode of this.flatNodes) {
      entryMap.set(flatNode.node.entry.id, flatNode);
    }
    let currentId = this.currentLeafId;
    while (currentId) {
      this.activePathIds.add(currentId);
      const node = entryMap.get(currentId);
      if (!node)
        break;
      currentId = node.node.entry.parentId ?? null;
    }
  }
  flattenTree(roots) {
    const result = [];
    this.toolCallMap.clear();
    const stack = [];
    const containsActive = /* @__PURE__ */ new Map();
    const leafId = this.currentLeafId;
    {
      const allNodes = [];
      const preOrderStack = [...roots];
      while (preOrderStack.length > 0) {
        const node = preOrderStack.pop();
        allNodes.push(node);
        for (let i = node.children.length - 1; i >= 0; i--) {
          preOrderStack.push(node.children[i]);
        }
      }
      for (let i = allNodes.length - 1; i >= 0; i--) {
        const node = allNodes[i];
        let has = leafId !== null && node.entry.id === leafId;
        for (const child of node.children) {
          if (containsActive.get(child)) {
            has = true;
          }
        }
        containsActive.set(node, has);
      }
    }
    const multipleRoots = roots.length > 1;
    const orderedRoots = [...roots].sort((a, b) => Number(containsActive.get(b)) - Number(containsActive.get(a)));
    for (let i = orderedRoots.length - 1; i >= 0; i--) {
      const isLast = i === orderedRoots.length - 1;
      stack.push([orderedRoots[i], multipleRoots ? 1 : 0, multipleRoots, multipleRoots, isLast, [], multipleRoots]);
    }
    while (stack.length > 0) {
      const [node, indent, justBranched, showConnector, isLast, gutters, isVirtualRootChild] = stack.pop();
      const entry = node.entry;
      if (entry.type === "message" && entry.message.role === "assistant") {
        const content = entry.message.content;
        if (Array.isArray(content)) {
          for (const block of content) {
            if (typeof block === "object" && block !== null && "type" in block && block.type === "toolCall") {
              const tc = block;
              this.toolCallMap.set(tc.id, { name: tc.name, arguments: tc.arguments });
            }
          }
        }
      }
      result.push({ node, indent, showConnector, isLast, gutters, isVirtualRootChild });
      const children = node.children;
      const multipleChildren = children.length > 1;
      const orderedChildren = (() => {
        const prioritized = [];
        const rest = [];
        for (const child of children) {
          if (containsActive.get(child)) {
            prioritized.push(child);
          } else {
            rest.push(child);
          }
        }
        return [...prioritized, ...rest];
      })();
      let childIndent;
      if (multipleChildren) {
        childIndent = indent + 1;
      } else if (justBranched && indent > 0) {
        childIndent = indent + 1;
      } else {
        childIndent = indent;
      }
      const connectorDisplayed = showConnector && !isVirtualRootChild;
      const currentDisplayIndent = this.multipleRoots ? Math.max(0, indent - 1) : indent;
      const connectorPosition = Math.max(0, currentDisplayIndent - 1);
      const childGutters = connectorDisplayed ? [...gutters, { position: connectorPosition, show: !isLast }] : gutters;
      for (let i = orderedChildren.length - 1; i >= 0; i--) {
        const childIsLast = i === orderedChildren.length - 1;
        stack.push([
          orderedChildren[i],
          childIndent,
          multipleChildren,
          multipleChildren,
          childIsLast,
          childGutters,
          false
        ]);
      }
    }
    return result;
  }
  applyFilter() {
    if (this.filteredNodes.length > 0) {
      this.lastSelectedId = this.filteredNodes[this.selectedIndex]?.node.entry.id ?? this.lastSelectedId;
    }
    const searchTokens = this.searchQuery.toLowerCase().split(/\s+/).filter(Boolean);
    this.filteredNodes = this.flatNodes.filter((flatNode) => {
      const entry = flatNode.node.entry;
      if (entry.type === "usage")
        return false;
      const isCurrentLeaf = entry.id === this.currentLeafId;
      if (entry.type === "message" && entry.message.role === "assistant" && !isCurrentLeaf) {
        const msg = entry.message;
        const hasText = this.hasTextContent(msg.content);
        const isErrorOrAborted = msg.stopReason && msg.stopReason !== "stop" && msg.stopReason !== "toolUse";
        if (!hasText && !isErrorOrAborted) {
          return false;
        }
      }
      let passesFilter = true;
      const isSettingsEntry = entry.type === "label" || entry.type === "context_edit" || entry.type === "custom" || entry.type === "model_change" || entry.type === "thinking_level_change" || entry.type === "session_info";
      switch (this.filterMode) {
        case "user-only":
          passesFilter = entry.type === "message" && entry.message.role === "user";
          break;
        case "no-tools":
          passesFilter = !isSettingsEntry && !(entry.type === "message" && entry.message.role === "toolResult");
          break;
        case "labeled-only":
          passesFilter = flatNode.node.label !== void 0;
          break;
        case "all":
          passesFilter = true;
          break;
        default:
          passesFilter = !isSettingsEntry;
          break;
      }
      if (!passesFilter)
        return false;
      if (searchTokens.length > 0) {
        const nodeText = this.getSearchableText(flatNode.node).toLowerCase();
        return searchTokens.every((token) => nodeText.includes(token));
      }
      return true;
    });
    if (this.foldedNodes.size > 0) {
      const skipSet = /* @__PURE__ */ new Set();
      for (const flatNode of this.flatNodes) {
        const { id, parentId } = flatNode.node.entry;
        if (parentId != null && (this.foldedNodes.has(parentId) || skipSet.has(parentId))) {
          skipSet.add(id);
        }
      }
      this.filteredNodes = this.filteredNodes.filter((flatNode) => !skipSet.has(flatNode.node.entry.id));
    }
    this.recalculateVisualStructure();
    if (this.lastSelectedId) {
      this.selectedIndex = this.findNearestVisibleIndex(this.lastSelectedId);
    } else if (this.selectedIndex >= this.filteredNodes.length) {
      this.selectedIndex = Math.max(0, this.filteredNodes.length - 1);
    }
    if (this.filteredNodes.length > 0) {
      this.lastSelectedId = this.filteredNodes[this.selectedIndex]?.node.entry.id ?? this.lastSelectedId;
    }
  }
  /**
   * Recompute indentation/connectors for the filtered view
   *
   * Filtering can hide intermediate entries; descendants attach to the nearest visible ancestor.
   * Keep indentation semantics aligned with flattenTree() so single-child chains don't drift right.
   */
  recalculateVisualStructure() {
    if (this.filteredNodes.length === 0)
      return;
    const visibleIds = new Set(this.filteredNodes.map((n) => n.node.entry.id));
    const entryMap = /* @__PURE__ */ new Map();
    for (const flatNode of this.flatNodes) {
      entryMap.set(flatNode.node.entry.id, flatNode);
    }
    const findVisibleAncestor = /* @__PURE__ */ __name((nodeId) => {
      let currentId = entryMap.get(nodeId)?.node.entry.parentId ?? null;
      while (currentId !== null) {
        if (visibleIds.has(currentId)) {
          return currentId;
        }
        currentId = entryMap.get(currentId)?.node.entry.parentId ?? null;
      }
      return null;
    }, "findVisibleAncestor");
    const visibleParent = /* @__PURE__ */ new Map();
    const visibleChildren = /* @__PURE__ */ new Map();
    visibleChildren.set(null, []);
    for (const flatNode of this.filteredNodes) {
      const nodeId = flatNode.node.entry.id;
      const ancestorId = findVisibleAncestor(nodeId);
      visibleParent.set(nodeId, ancestorId);
      if (!visibleChildren.has(ancestorId)) {
        visibleChildren.set(ancestorId, []);
      }
      visibleChildren.get(ancestorId).push(nodeId);
    }
    const visibleRootIds = visibleChildren.get(null);
    this.multipleRoots = visibleRootIds.length > 1;
    const filteredNodeMap = /* @__PURE__ */ new Map();
    for (const flatNode of this.filteredNodes) {
      filteredNodeMap.set(flatNode.node.entry.id, flatNode);
    }
    const stack = [];
    for (let i = visibleRootIds.length - 1; i >= 0; i--) {
      const isLast = i === visibleRootIds.length - 1;
      stack.push([
        visibleRootIds[i],
        this.multipleRoots ? 1 : 0,
        this.multipleRoots,
        this.multipleRoots,
        isLast,
        [],
        this.multipleRoots
      ]);
    }
    while (stack.length > 0) {
      const [nodeId, indent, justBranched, showConnector, isLast, gutters, isVirtualRootChild] = stack.pop();
      const flatNode = filteredNodeMap.get(nodeId);
      if (!flatNode)
        continue;
      flatNode.indent = indent;
      flatNode.showConnector = showConnector;
      flatNode.isLast = isLast;
      flatNode.gutters = gutters;
      flatNode.isVirtualRootChild = isVirtualRootChild;
      const children = visibleChildren.get(nodeId) || [];
      const multipleChildren = children.length > 1;
      let childIndent;
      if (multipleChildren) {
        childIndent = indent + 1;
      } else if (justBranched && indent > 0) {
        childIndent = indent + 1;
      } else {
        childIndent = indent;
      }
      const connectorDisplayed = showConnector && !isVirtualRootChild;
      const currentDisplayIndent = this.multipleRoots ? Math.max(0, indent - 1) : indent;
      const connectorPosition = Math.max(0, currentDisplayIndent - 1);
      const childGutters = connectorDisplayed ? [...gutters, { position: connectorPosition, show: !isLast }] : gutters;
      for (let i = children.length - 1; i >= 0; i--) {
        const childIsLast = i === children.length - 1;
        stack.push([
          children[i],
          childIndent,
          multipleChildren,
          multipleChildren,
          childIsLast,
          childGutters,
          false
        ]);
      }
    }
    this.visibleParentMap = visibleParent;
    this.visibleChildrenMap = visibleChildren;
  }
  /** Get searchable text content from a node */
  getSearchableText(node) {
    const entry = node.entry;
    const parts = [];
    if (node.label) {
      parts.push(node.label);
    }
    switch (entry.type) {
      case "message": {
        const msg = entry.message;
        parts.push(msg.role);
        if ("content" in msg && msg.content) {
          parts.push(this.extractContent(msg.content));
        }
        if (msg.role === "bashExecution") {
          const bashMsg = msg;
          if (bashMsg.command)
            parts.push(bashMsg.command);
        }
        break;
      }
      case "custom_message": {
        parts.push(entry.customType);
        if (typeof entry.content === "string") {
          parts.push(entry.content);
        } else {
          parts.push(this.extractContent(entry.content));
        }
        break;
      }
      case "compaction":
        parts.push("compaction");
        break;
      case "branch_summary":
        parts.push("branch summary", entry.summary);
        break;
      case "session_info":
        parts.push("title");
        if (entry.name)
          parts.push(entry.name);
        break;
      case "model_change":
        parts.push("model", entry.modelId);
        break;
      case "thinking_level_change":
        parts.push("thinking", entry.thinkingLevel);
        break;
      case "custom":
        parts.push("custom", entry.customType);
        break;
      case "context_edit":
        parts.push("context edit", entry.replacement === null ? "omit" : "replace", entry.targetId);
        break;
      case "label":
        parts.push("label", entry.label ?? "");
        break;
    }
    return parts.join(" ");
  }
  invalidate() {
  }
  getSearchQuery() {
    return this.searchQuery;
  }
  getSelectedNode() {
    return this.filteredNodes[this.selectedIndex]?.node;
  }
  copySelected() {
    const node = this.getSelectedNode();
    this.onCopy?.(node ? this.getEntryCopyText(node) : void 0);
  }
  updateNodeLabel(entryId, label, labelTimestamp) {
    for (const flatNode of this.flatNodes) {
      if (flatNode.node.entry.id === entryId) {
        flatNode.node.label = label;
        flatNode.node.labelTimestamp = label ? labelTimestamp ?? (/* @__PURE__ */ new Date()).toISOString() : void 0;
        break;
      }
    }
  }
  getStatusLabels() {
    let labels = "";
    switch (this.filterMode) {
      case "no-tools":
        labels += " [no-tools]";
        break;
      case "user-only":
        labels += " [user]";
        break;
      case "labeled-only":
        labels += " [labeled]";
        break;
      case "all":
        labels += " [all]";
        break;
    }
    if (this.showLabelTimestamps) {
      labels += " [+label time]";
    }
    return labels;
  }
  render(width) {
    const lines = [];
    if (this.filteredNodes.length === 0) {
      lines.push(truncateToWidth6(theme.fg("muted", "  No entries found"), width));
      lines.push(truncateToWidth6(theme.fg("muted", `  (0/0)${this.getStatusLabels()}`), width));
      return lines;
    }
    const startIndex = Math.max(0, Math.min(this.selectedIndex - Math.floor(this.maxVisibleLines / 2), this.filteredNodes.length - this.maxVisibleLines));
    const endIndex = Math.min(startIndex + this.maxVisibleLines, this.filteredNodes.length);
    const renderedRows = [];
    for (let i = startIndex; i < endIndex; i++) {
      const flatNode = this.filteredNodes[i];
      const entry = flatNode.node.entry;
      const isSelected = i === this.selectedIndex;
      const cursor = isSelected ? theme.fg("accent", "\u203A ") : "  ";
      const displayIndent = this.multipleRoots ? Math.max(0, flatNode.indent - 1) : flatNode.indent;
      const connector = flatNode.showConnector && !flatNode.isVirtualRootChild ? flatNode.isLast ? "\u2514\u2500 " : "\u251C\u2500 " : "";
      const connectorPosition = connector ? displayIndent - 1 : -1;
      const totalChars = displayIndent * 3;
      const prefixChars = [];
      const isFolded = this.foldedNodes.has(entry.id);
      for (let i2 = 0; i2 < totalChars; i2++) {
        const level = Math.floor(i2 / 3);
        const posInLevel = i2 % 3;
        const gutter2 = flatNode.gutters.find((g) => g.position === level);
        if (gutter2) {
          if (posInLevel === 0) {
            prefixChars.push(gutter2.show ? "\u2502" : " ");
          } else {
            prefixChars.push(" ");
          }
        } else if (connector && level === connectorPosition) {
          if (posInLevel === 0) {
            prefixChars.push(flatNode.isLast ? "\u2514" : "\u251C");
          } else if (posInLevel === 1) {
            const foldable = this.isFoldable(entry.id);
            prefixChars.push(isFolded ? "\u229E" : foldable ? "\u229F" : "\u2500");
          } else {
            prefixChars.push(" ");
          }
        } else {
          prefixChars.push(" ");
        }
      }
      const prefix = prefixChars.join("");
      const showsFoldInConnector = flatNode.showConnector && !flatNode.isVirtualRootChild;
      const foldMarker = isFolded && !showsFoldInConnector ? theme.fg("accent", "\u229E ") : "";
      const isOnActivePath = this.activePathIds.has(entry.id);
      const pathMarker = isOnActivePath ? theme.fg("accent", "\u2022 ") : "";
      const label = flatNode.node.label ? theme.fg("warning", `[${flatNode.node.label}] `) : "";
      const labelTimestamp = this.showLabelTimestamps && flatNode.node.label && flatNode.node.labelTimestamp ? theme.fg("muted", `${this.formatLabelTimestamp(flatNode.node.labelTimestamp)} `) : "";
      const content = this.getEntryDisplayText(flatNode.node, isSelected);
      const prefixPart = theme.fg("dim", prefix) + foldMarker + pathMarker;
      const anchorCol = visibleWidth6(prefixPart);
      let gutter = cursor;
      let body = prefixPart + label + labelTimestamp + content;
      if (isSelected) {
        gutter = theme.bg("selectedBg", gutter);
        body = theme.bg("selectedBg", body);
      }
      renderedRows.push({ gutter, body, anchorCol, bodyWidth: visibleWidth6(body), isSelected });
    }
    lines.push(...renderHorizontalViewport(renderedRows, width));
    lines.push(truncateToWidth6(theme.fg("muted", `  (${this.selectedIndex + 1}/${this.filteredNodes.length})${this.getStatusLabels()}`), width));
    return lines;
  }
  getEntryDisplayText(node, isSelected) {
    const entry = node.entry;
    let result;
    const normalize = /* @__PURE__ */ __name((s) => s.replace(/[\n\t]/g, " ").trim(), "normalize");
    switch (entry.type) {
      case "message": {
        const msg = entry.message;
        const role = msg.role;
        if (role === "user") {
          const msgWithContent = msg;
          const content = normalize(this.extractContent(msgWithContent.content));
          result = theme.fg("accent", "user: ") + content;
        } else if (role === "assistant") {
          const msgWithContent = msg;
          const textContent = normalize(this.extractContent(msgWithContent.content));
          if (textContent) {
            result = theme.fg("success", "assistant: ") + textContent;
          } else if (msgWithContent.stopReason === "aborted") {
            result = theme.fg("success", "assistant: ") + theme.fg("muted", "(aborted)");
          } else if (msgWithContent.errorMessage) {
            const errMsg = normalize(msgWithContent.errorMessage).slice(0, 80);
            result = theme.fg("success", "assistant: ") + theme.fg("error", errMsg);
          } else {
            result = theme.fg("success", "assistant: ") + theme.fg("muted", "(no content)");
          }
        } else if (role === "toolResult") {
          const toolMsg = msg;
          const toolCall = toolMsg.toolCallId ? this.toolCallMap.get(toolMsg.toolCallId) : void 0;
          if (toolCall) {
            result = theme.fg("muted", this.formatToolCall(toolCall.name, toolCall.arguments));
          } else {
            result = theme.fg("muted", `[${toolMsg.toolName ?? "tool"}]`);
          }
        } else if (role === "bashExecution") {
          const bashMsg = msg;
          result = theme.fg("dim", `[bash]: ${normalize(bashMsg.command ?? "")}`);
        } else {
          result = theme.fg("dim", `[${role}]`);
        }
        break;
      }
      case "custom_message": {
        const content = typeof entry.content === "string" ? entry.content : entry.content.filter((c2) => c2.type === "text").map((c2) => c2.text).join("");
        result = theme.fg("customMessageLabel", `[${entry.customType}]: `) + normalize(content);
        break;
      }
      case "compaction": {
        const tokens = Math.round(entry.tokensBefore / 1e3);
        result = theme.fg("borderAccent", `[compaction: ${tokens}k tokens]`);
        break;
      }
      case "branch_summary":
        result = theme.fg("warning", `[branch summary]: `) + normalize(entry.summary);
        break;
      case "model_change":
        result = theme.fg("dim", `[model: ${entry.modelId}]`);
        break;
      case "thinking_level_change":
        result = theme.fg("dim", `[thinking: ${entry.thinkingLevel}]`);
        break;
      case "custom":
        result = theme.fg("dim", `[custom: ${entry.customType}]`);
        break;
      case "context_edit":
        result = theme.fg("dim", `[context ${entry.replacement === null ? "omit" : "replace"}: ${entry.targetId}]`);
        break;
      case "label":
        result = theme.fg("dim", `[label: ${entry.label ?? "(cleared)"}]`);
        break;
      case "session_info":
        result = entry.name ? [theme.fg("dim", "[title: "), theme.fg("dim", entry.name), theme.fg("dim", "]")].join("") : [theme.fg("dim", "[title: "), theme.italic(theme.fg("dim", "empty")), theme.fg("dim", "]")].join("");
        break;
      default:
        result = "";
    }
    return isSelected ? theme.bold(result) : result;
  }
  formatLabelTimestamp(timestamp) {
    const date = new Date(timestamp);
    const now = /* @__PURE__ */ new Date();
    const hours = date.getHours().toString().padStart(2, "0");
    const minutes = date.getMinutes().toString().padStart(2, "0");
    const time2 = `${hours}:${minutes}`;
    if (date.getFullYear() === now.getFullYear() && date.getMonth() === now.getMonth() && date.getDate() === now.getDate()) {
      return time2;
    }
    const month = date.getMonth() + 1;
    const day = date.getDate();
    if (date.getFullYear() === now.getFullYear()) {
      return `${month}/${day} ${time2}`;
    }
    const year = date.getFullYear().toString().slice(-2);
    return `${year}/${month}/${day} ${time2}`;
  }
  extractContent(content) {
    return this.extractFullContent(content).slice(0, 200);
  }
  extractFullContent(content) {
    if (typeof content === "string")
      return content;
    if (!Array.isArray(content))
      return "";
    let result = "";
    for (const block of content) {
      if (typeof block === "object" && block !== null && "type" in block && block.type === "text") {
        result += block.text;
      }
    }
    return result;
  }
  getEntryCopyText(node) {
    const entry = node.entry;
    let text;
    switch (entry.type) {
      case "message":
        if (entry.message.role === "bashExecution") {
          text = entry.message.command;
        } else if ("content" in entry.message) {
          text = this.extractFullContent(entry.message.content);
          if (!text && entry.message.role === "assistant") {
            text = entry.message.errorMessage;
          }
        }
        break;
      case "custom_message":
        text = this.extractFullContent(entry.content);
        break;
      case "compaction":
        text = entry.summary;
        break;
      case "branch_summary":
        text = entry.summary;
        break;
    }
    return text?.trim() ? text : void 0;
  }
  hasTextContent(content) {
    if (typeof content === "string")
      return content.trim().length > 0;
    if (Array.isArray(content)) {
      for (const c2 of content) {
        if (typeof c2 === "object" && c2 !== null && "type" in c2 && c2.type === "text") {
          const text = c2.text;
          if (text && text.trim().length > 0)
            return true;
        }
      }
    }
    return false;
  }
  formatToolCall(name, args) {
    const shortenPath2 = /* @__PURE__ */ __name((p) => {
      const home = process.env.HOME || process.env.USERPROFILE || "";
      if (home && p.startsWith(home))
        return `~${p.slice(home.length)}`;
      return p;
    }, "shortenPath");
    switch (name) {
      case "read": {
        const path5 = shortenPath2(String(args.path || args.file_path || ""));
        const offset = args.offset;
        const limit = args.limit;
        let display = path5;
        if (offset !== void 0 || limit !== void 0) {
          const start = offset ?? 1;
          const end = limit !== void 0 ? start + limit - 1 : "";
          display += `:${start}${end ? `-${end}` : ""}`;
        }
        return `[read: ${display}]`;
      }
      case "write": {
        const path5 = shortenPath2(String(args.path || args.file_path || ""));
        return `[write: ${path5}]`;
      }
      case "edit": {
        const path5 = shortenPath2(String(args.path || args.file_path || ""));
        return `[edit: ${path5}]`;
      }
      case "bash": {
        const rawCmd = String(args.command || "");
        const cmd = rawCmd.replace(/[\n\t]/g, " ").trim().slice(0, 50);
        return `[bash: ${cmd}${rawCmd.length > 50 ? "..." : ""}]`;
      }
      case "grep": {
        const pattern = String(args.pattern || "");
        const path5 = shortenPath2(String(args.path || "."));
        return `[grep: /${pattern}/ in ${path5}]`;
      }
      case "find": {
        const pattern = String(args.pattern || "");
        const path5 = shortenPath2(String(args.path || "."));
        return `[find: ${pattern} in ${path5}]`;
      }
      case "ls": {
        const path5 = shortenPath2(String(args.path || "."));
        return `[ls: ${path5}]`;
      }
      default: {
        const argsStr = JSON.stringify(args).slice(0, 40);
        return `[${name}: ${argsStr}${JSON.stringify(args).length > 40 ? "..." : ""}]`;
      }
    }
  }
  handleInput(keyData) {
    const kb = getKeybindings12();
    if (kb.matches(keyData, "tui.select.up")) {
      this.selectedIndex = this.selectedIndex === 0 ? this.filteredNodes.length - 1 : this.selectedIndex - 1;
    } else if (kb.matches(keyData, "tui.select.down")) {
      this.selectedIndex = this.selectedIndex === this.filteredNodes.length - 1 ? 0 : this.selectedIndex + 1;
    } else if (kb.matches(keyData, "app.tree.foldOrUp")) {
      const currentId = this.filteredNodes[this.selectedIndex]?.node.entry.id;
      if (currentId && this.isFoldable(currentId) && !this.foldedNodes.has(currentId)) {
        this.foldedNodes.add(currentId);
        this.applyFilter();
      } else {
        this.selectedIndex = this.findBranchSegmentStart("up");
      }
    } else if (kb.matches(keyData, "app.tree.unfoldOrDown")) {
      const currentId = this.filteredNodes[this.selectedIndex]?.node.entry.id;
      if (currentId && this.foldedNodes.has(currentId)) {
        this.foldedNodes.delete(currentId);
        this.applyFilter();
      } else {
        this.selectedIndex = this.findBranchSegmentStart("down");
      }
    } else if (kb.matches(keyData, "tui.editor.cursorLeft") || kb.matches(keyData, "tui.select.pageUp")) {
      this.selectedIndex = Math.max(0, this.selectedIndex - this.maxVisibleLines);
    } else if (kb.matches(keyData, "tui.editor.cursorRight") || kb.matches(keyData, "tui.select.pageDown")) {
      this.selectedIndex = Math.min(this.filteredNodes.length - 1, this.selectedIndex + this.maxVisibleLines);
    } else if (kb.matches(keyData, "tui.select.confirm")) {
      const selected = this.filteredNodes[this.selectedIndex];
      if (selected && this.onSelect) {
        this.onSelect(selected.node.entry.id);
      }
    } else if (kb.matches(keyData, "app.message.copy")) {
      this.copySelected();
    } else if (kb.matches(keyData, "tui.select.cancel")) {
      if (this.searchQuery) {
        this.searchQuery = "";
        this.foldedNodes.clear();
        this.applyFilter();
      } else {
        this.onCancel?.();
      }
    } else if (kb.matches(keyData, "app.tree.filter.default")) {
      this.filterMode = "default";
      this.foldedNodes.clear();
      this.applyFilter();
    } else if (kb.matches(keyData, "app.tree.filter.noTools")) {
      this.filterMode = this.filterMode === "no-tools" ? "default" : "no-tools";
      this.foldedNodes.clear();
      this.applyFilter();
    } else if (kb.matches(keyData, "app.tree.filter.userOnly")) {
      this.filterMode = this.filterMode === "user-only" ? "default" : "user-only";
      this.foldedNodes.clear();
      this.applyFilter();
    } else if (kb.matches(keyData, "app.tree.filter.labeledOnly")) {
      this.filterMode = this.filterMode === "labeled-only" ? "default" : "labeled-only";
      this.foldedNodes.clear();
      this.applyFilter();
    } else if (kb.matches(keyData, "app.tree.filter.all")) {
      this.filterMode = this.filterMode === "all" ? "default" : "all";
      this.foldedNodes.clear();
      this.applyFilter();
    } else if (kb.matches(keyData, "app.tree.filter.cycleBackward")) {
      const modes = ["default", "no-tools", "user-only", "labeled-only", "all"];
      const currentIndex = modes.indexOf(this.filterMode);
      this.filterMode = modes[(currentIndex - 1 + modes.length) % modes.length];
      this.foldedNodes.clear();
      this.applyFilter();
    } else if (kb.matches(keyData, "app.tree.filter.cycleForward")) {
      const modes = ["default", "no-tools", "user-only", "labeled-only", "all"];
      const currentIndex = modes.indexOf(this.filterMode);
      this.filterMode = modes[(currentIndex + 1) % modes.length];
      this.foldedNodes.clear();
      this.applyFilter();
    } else if (kb.matches(keyData, "tui.editor.deleteCharBackward")) {
      if (this.searchQuery.length > 0) {
        this.searchQuery = this.searchQuery.slice(0, -1);
        this.foldedNodes.clear();
        this.applyFilter();
      }
    } else if (kb.matches(keyData, "app.tree.editLabel")) {
      const selected = this.filteredNodes[this.selectedIndex];
      if (selected && this.onLabelEdit) {
        this.onLabelEdit(selected.node.entry.id, selected.node.label);
      }
    } else if (kb.matches(keyData, "app.tree.toggleLabelTimestamp")) {
      this.showLabelTimestamps = !this.showLabelTimestamps;
    } else {
      const hasControlChars = [...keyData].some((ch) => {
        const code = ch.charCodeAt(0);
        return code < 32 || code === 127 || code >= 128 && code <= 159;
      });
      if (!hasControlChars && keyData.length > 0) {
        this.searchQuery += keyData;
        this.foldedNodes.clear();
        this.applyFilter();
      }
    }
  }
  /**
   * Whether a node can be folded. A node is foldable if it has visible children
   * and is either a root (no visible parent) or a segment start (visible parent
   * has multiple visible children).
   */
  isFoldable(entryId) {
    const children = this.visibleChildrenMap.get(entryId);
    if (!children || children.length === 0)
      return false;
    const parentId = this.visibleParentMap.get(entryId);
    if (parentId === null || parentId === void 0)
      return true;
    const siblings = this.visibleChildrenMap.get(parentId);
    return siblings !== void 0 && siblings.length > 1;
  }
  /**
   * Find the index of the next branch segment start in the given direction.
   * A segment start is the first child of a branch point.
   *
   * "up" walks the visible parent chain; "down" walks visible children
   * (always following the first child).
   */
  findBranchSegmentStart(direction) {
    const selectedId = this.filteredNodes[this.selectedIndex]?.node.entry.id;
    if (!selectedId)
      return this.selectedIndex;
    const indexByEntryId = new Map(this.filteredNodes.map((node, i) => [node.node.entry.id, i]));
    let currentId = selectedId;
    if (direction === "down") {
      while (true) {
        const children = this.visibleChildrenMap.get(currentId) ?? [];
        if (children.length === 0)
          return indexByEntryId.get(currentId);
        if (children.length > 1)
          return indexByEntryId.get(children[0]);
        currentId = children[0];
      }
    }
    while (true) {
      const parentId = this.visibleParentMap.get(currentId) ?? null;
      if (parentId === null)
        return indexByEntryId.get(currentId);
      const children = this.visibleChildrenMap.get(parentId) ?? [];
      if (children.length > 1) {
        const segmentStart = indexByEntryId.get(currentId);
        if (segmentStart < this.selectedIndex) {
          return segmentStart;
        }
      }
      currentId = parentId;
    }
  }
};
var SearchLine = class {
  static {
    __name(this, "SearchLine");
  }
  treeList;
  constructor(treeList) {
    this.treeList = treeList;
  }
  invalidate() {
  }
  render(width) {
    const query = this.treeList.getSearchQuery();
    if (query) {
      return [truncateToWidth6(`  ${theme.fg("muted", "Type to search:")} ${theme.fg("accent", query)}`, width)];
    }
    return [truncateToWidth6(`  ${theme.fg("muted", "Type to search:")}`, width)];
  }
  handleInput(_keyData) {
  }
};
var TreeHelp = class {
  static {
    __name(this, "TreeHelp");
  }
  invalidate() {
  }
  render(width) {
    const items = TREE_HELP_ITEMS.map(({ keys, label, labelFirst }) => {
      const text = formatHelpKeys(keys);
      if (!text)
        return label;
      return labelFirst ? `${label} ${text}` : `${text} ${label}`;
    });
    const availableWidth = Math.max(1, width);
    const indent = "  ";
    const separator = " \xB7 ";
    const lines = [];
    let currentLine = "";
    for (const item of items) {
      const candidate = currentLine ? `${currentLine}${separator}${item}` : visibleWidth6(`${indent}${item}`) <= availableWidth ? `${indent}${item}` : item;
      if (!currentLine || visibleWidth6(candidate) <= availableWidth) {
        currentLine = candidate;
        continue;
      }
      lines.push(...wrapTextWithAnsi(currentLine.trimEnd(), availableWidth));
      currentLine = visibleWidth6(`${indent}${item}`) <= availableWidth ? `${indent}${item}` : item;
    }
    if (currentLine) {
      lines.push(...wrapTextWithAnsi(currentLine.trimEnd(), availableWidth));
    }
    return lines.map((line) => theme.fg("muted", line));
  }
};
var TREE_HELP_ITEMS = [
  { keys: ["tui.select.up", "tui.select.down"], label: "move" },
  { keys: ["tui.editor.cursorLeft", "tui.editor.cursorRight"], label: "page" },
  { keys: ["app.tree.foldOrUp", "app.tree.unfoldOrDown"], label: "branch" },
  { keys: ["app.message.copy"], label: "copy" },
  { keys: ["app.tree.editLabel"], label: "label" },
  { keys: ["app.tree.toggleLabelTimestamp"], label: "label time" },
  {
    keys: [
      "app.tree.filter.default",
      "app.tree.filter.noTools",
      "app.tree.filter.userOnly",
      "app.tree.filter.labeledOnly",
      "app.tree.filter.all"
    ],
    label: "filters",
    labelFirst: true
  },
  { keys: ["app.tree.filter.cycleForward", "app.tree.filter.cycleBackward"], label: "cycle", labelFirst: true }
];
function formatHelpKeys(keybindings) {
  const keys = [];
  for (const keybinding of keybindings) {
    const key = getKeybindings12().getKeys(keybinding)[0];
    if (key !== void 0)
      keys.push(key);
  }
  if (keys.length === 0)
    return "";
  return formatKeyText(compactRawKeys(keys)).replace(/\bpageUp\b/g, "pgup").replace(/\bpageDown\b/g, "pgdn").replace(/\bup\b/g, "\u2191").replace(/\bdown\b/g, "\u2193").replace(/\bleft\b/g, "\u2190").replace(/\bright\b/g, "\u2192");
}
__name(formatHelpKeys, "formatHelpKeys");
function compactRawKeys(keys) {
  if (keys.length === 1)
    return keys[0];
  const parts = keys.map((key) => {
    const separatorIndex = key.lastIndexOf("+");
    return separatorIndex === -1 ? { prefix: "", suffix: key } : { prefix: key.slice(0, separatorIndex + 1), suffix: key.slice(separatorIndex + 1) };
  });
  const prefix = parts[0].prefix;
  return prefix && parts.every((part) => part.prefix === prefix) ? `${prefix}${parts.map((part) => part.suffix).join("/")}` : keys.join("/");
}
__name(compactRawKeys, "compactRawKeys");
var LabelInput = class {
  static {
    __name(this, "LabelInput");
  }
  input;
  entryId;
  onSubmit;
  onCancel;
  // Focusable implementation - propagate to input for IME cursor positioning
  _focused = false;
  get focused() {
    return this._focused;
  }
  set focused(value) {
    this._focused = value;
    this.input.focused = value;
  }
  constructor(entryId, currentLabel) {
    this.entryId = entryId;
    this.input = new Input11();
    if (currentLabel) {
      this.input.setValue(currentLabel);
    }
  }
  invalidate() {
  }
  render(width) {
    const lines = [];
    const indent = "  ";
    const availableWidth = width - indent.length;
    lines.push(truncateToWidth6(`${indent}${theme.fg("muted", "Label (empty to remove):")}`, width));
    lines.push(...this.input.render(availableWidth).map((line) => truncateToWidth6(`${indent}${line}`, width)));
    lines.push(truncateToWidth6(`${indent}${keyHint("tui.select.confirm", "save")}  ${keyHint("tui.select.cancel", "cancel")}`, width));
    return lines;
  }
  handleInput(keyData) {
    const kb = getKeybindings12();
    if (kb.matches(keyData, "tui.select.confirm")) {
      const value = this.input.getValue().trim();
      this.onSubmit?.(this.entryId, value || void 0);
    } else if (kb.matches(keyData, "tui.select.cancel")) {
      this.onCancel?.();
    } else {
      this.input.handleInput(keyData);
    }
  }
};
var TreeSelectorComponent = class extends Container26 {
  static {
    __name(this, "TreeSelectorComponent");
  }
  treeList;
  labelInput = null;
  labelInputContainer;
  treeContainer;
  onLabelChangeCallback;
  onCopy;
  // Focusable implementation - propagate to labelInput when active for IME cursor positioning
  _focused = false;
  get focused() {
    return this._focused;
  }
  set focused(value) {
    this._focused = value;
    if (this.labelInput) {
      this.labelInput.focused = value;
    }
  }
  constructor(tree, currentLeafId, terminalHeight, onSelect, onCancel, onLabelChange, initialSelectedId, initialFilterMode) {
    super();
    this.onLabelChangeCallback = onLabelChange;
    const maxVisibleLines = Math.max(5, Math.floor(terminalHeight / 2));
    this.treeList = new TreeList(tree, currentLeafId, maxVisibleLines, initialSelectedId, initialFilterMode);
    this.treeList.onSelect = onSelect;
    this.treeList.onCancel = onCancel;
    this.treeList.onCopy = (text) => this.onCopy?.(text);
    this.treeList.onLabelEdit = (entryId, currentLabel) => this.showLabelInput(entryId, currentLabel);
    this.treeContainer = new Container26();
    this.treeContainer.addChild(this.treeList);
    this.labelInputContainer = new Container26();
    this.addChild(new Spacer24(1));
    this.addChild(new DynamicBorder());
    this.addChild(new Text27(theme.bold("  Session Tree"), 1, 0));
    this.addChild(new TreeHelp());
    this.addChild(new SearchLine(this.treeList));
    this.addChild(new DynamicBorder());
    this.addChild(new Spacer24(1));
    this.addChild(this.treeContainer);
    this.addChild(this.labelInputContainer);
    this.addChild(new Spacer24(1));
    this.addChild(new DynamicBorder());
    if (tree.length === 0) {
      setTimeout(() => onCancel(), 100);
    }
  }
  showLabelInput(entryId, currentLabel) {
    this.labelInput = new LabelInput(entryId, currentLabel);
    this.labelInput.onSubmit = (id, label) => {
      this.treeList.updateNodeLabel(id, label);
      this.onLabelChangeCallback?.(id, label);
      this.hideLabelInput();
    };
    this.labelInput.onCancel = () => this.hideLabelInput();
    this.labelInput.focused = this._focused;
    this.treeContainer.clear();
    this.labelInputContainer.clear();
    this.labelInputContainer.addChild(this.labelInput);
  }
  hideLabelInput() {
    this.labelInput = null;
    this.labelInputContainer.clear();
    this.treeContainer.clear();
    this.treeContainer.addChild(this.treeList);
  }
  handleInput(keyData) {
    if (this.labelInput) {
      this.labelInput.handleInput(keyData);
    } else {
      this.treeList.handleInput(keyData);
    }
  }
  getTreeList() {
    return this.treeList;
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/trust-selector.js
import { Container as Container27, getKeybindings as getKeybindings13, Spacer as Spacer25, Text as Text28 } from "../../../pi-tui.mjs";
function formatDecision(trustPath, decision) {
  if (decision === null) {
    return "none";
  }
  const label = decision.decision ? "trusted" : "untrusted";
  if (trustPath !== void 0 && decision.path !== trustPath) {
    return `${label} (inherited from ${decision.path})`;
  }
  return `${label} (${decision.path})`;
}
__name(formatDecision, "formatDecision");
var TrustSelectorComponent = class extends Container27 {
  static {
    __name(this, "TrustSelectorComponent");
  }
  selectedIndex;
  listContainer;
  trustOptions;
  savedDecision;
  onSelectCallback;
  onCancelCallback;
  constructor(options) {
    super();
    this.savedDecision = options.savedDecision;
    this.trustOptions = getProjectTrustOptions(options.cwd);
    this.selectedIndex = Math.max(0, this.trustOptions.findIndex((option) => this.isSavedOption(option)));
    this.onSelectCallback = options.onSelect;
    this.onCancelCallback = options.onCancel;
    this.addChild(new DynamicBorder());
    this.addChild(new Spacer25(1));
    this.addChild(new Text28(theme.fg("accent", theme.bold("Project trust")), 1, 0));
    this.addChild(new Text28(theme.fg("muted", options.cwd), 1, 0));
    this.addChild(new Spacer25(1));
    this.addChild(new Text28(theme.fg("muted", `Saved decision: ${formatDecision(this.trustOptions[0]?.savedPath, options.savedDecision)}`), 1, 0));
    this.addChild(new Text28(theme.fg("muted", `Current session: ${options.projectTrusted ? "trusted" : "untrusted"}`), 1, 0));
    this.addChild(new Spacer25(1));
    this.listContainer = new Container27();
    this.addChild(this.listContainer);
    this.addChild(new Spacer25(1));
    this.addChild(new Text28(rawKeyHint("\u2191\u2193", "navigate") + "  " + keyHint("tui.select.confirm", "save") + "  " + keyHint("tui.select.cancel", "cancel"), 1, 0));
    this.addChild(new Spacer25(1));
    this.addChild(new DynamicBorder());
    this.updateList();
  }
  isSavedOption(option) {
    return option.savedPath !== void 0 && this.savedDecision?.decision === option.trusted && this.savedDecision.path === option.savedPath;
  }
  updateList() {
    this.listContainer.clear();
    for (let i = 0; i < this.trustOptions.length; i++) {
      const option = this.trustOptions[i];
      if (!option) {
        continue;
      }
      const isSelected = i === this.selectedIndex;
      const isCurrent = this.isSavedOption(option);
      const currentMarker = isCurrent ? theme.fg("accent", "\u2713 ") : "  ";
      const prefix = isSelected ? theme.fg("accent", "\u2192 ") : "  ";
      const label = isSelected ? theme.fg("accent", option.label) : theme.fg("text", option.label);
      this.listContainer.addChild(new Text28(`${prefix}${currentMarker}${label}`, 1, 0));
    }
  }
  handleInput(keyData) {
    const kb = getKeybindings13();
    if (kb.matches(keyData, "tui.select.up") || keyData === "k") {
      this.selectedIndex = Math.max(0, this.selectedIndex - 1);
      this.updateList();
    } else if (kb.matches(keyData, "tui.select.down") || keyData === "j") {
      this.selectedIndex = Math.min(this.trustOptions.length - 1, this.selectedIndex + 1);
      this.updateList();
    } else if (kb.matches(keyData, "tui.select.confirm") || keyData === "\n") {
      const selected = this.trustOptions[this.selectedIndex];
      if (selected) {
        this.onSelectCallback({ trusted: selected.trusted, updates: selected.updates });
      }
    } else if (kb.matches(keyData, "tui.select.cancel")) {
      this.onCancelCallback();
    }
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/user-message.js
import { Container as Container28, Markdown as Markdown6 } from "../../../pi-tui.mjs";
var OSC133_ZONE_START2 = "\x1B]133;A\x07";
var OSC133_ZONE_END2 = "\x1B]133;B\x07";
var OSC133_ZONE_FINAL2 = "\x1B]133;C\x07";
var UserMessageComponent = class extends Container28 {
  static {
    __name(this, "UserMessageComponent");
  }
  text;
  markdownTheme;
  outputPad;
  markdownTransformers;
  constructor(text, markdownTheme = getMarkdownTheme(), outputPad = 1, markdownTransformers = []) {
    super();
    this.text = text;
    this.markdownTheme = markdownTheme;
    this.outputPad = outputPad;
    this.markdownTransformers = markdownTransformers;
    this.rebuild();
  }
  setOutputPad(padding) {
    this.outputPad = padding;
    this.rebuild();
  }
  rebuild() {
    this.clear();
    this.addChild(new Markdown6(this.text, this.outputPad, 1, this.markdownTheme, {
      color: /* @__PURE__ */ __name((content) => theme.fg("userMessageText", content), "color"),
      bgColor: /* @__PURE__ */ __name((content) => theme.bg("userMessageBg", content), "bgColor")
    }, {
      preserveOrderedListMarkers: true,
      preserveBackslashEscapes: true,
      transform: createMarkdownTransform("user", false, this.markdownTransformers)
    }));
  }
  render(width) {
    const lines = super.render(width);
    if (lines.length === 0) {
      return lines;
    }
    lines[0] = OSC133_ZONE_START2 + lines[0];
    lines[lines.length - 1] = OSC133_ZONE_END2 + OSC133_ZONE_FINAL2 + lines[lines.length - 1];
    return lines;
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/user-message-selector.js
import { Container as Container29, getKeybindings as getKeybindings14, Spacer as Spacer26, Text as Text29, truncateToWidth as truncateToWidth7 } from "../../../pi-tui.mjs";
var UserMessageList = class {
  static {
    __name(this, "UserMessageList");
  }
  messages = [];
  selectedIndex = 0;
  onSelect;
  onCancel;
  maxVisible = 10;
  // Max messages visible
  constructor(messages, initialSelectedId) {
    this.messages = messages;
    const initialIndex = initialSelectedId ? messages.findIndex((message) => message.id === initialSelectedId) : -1;
    this.selectedIndex = initialIndex >= 0 ? initialIndex : Math.max(0, messages.length - 1);
  }
  invalidate() {
  }
  render(width) {
    const lines = [];
    if (this.messages.length === 0) {
      lines.push(theme.fg("muted", "  No user messages found"));
      return lines;
    }
    const startIndex = Math.max(0, Math.min(this.selectedIndex - Math.floor(this.maxVisible / 2), this.messages.length - this.maxVisible));
    const endIndex = Math.min(startIndex + this.maxVisible, this.messages.length);
    for (let i = startIndex; i < endIndex; i++) {
      const message = this.messages[i];
      const isSelected = i === this.selectedIndex;
      const normalizedMessage = message.text.replace(/\n/g, " ").trim();
      const cursor = isSelected ? theme.fg("accent", "\u203A ") : "  ";
      const maxMsgWidth = width - 2;
      const truncatedMsg = truncateToWidth7(normalizedMessage, maxMsgWidth);
      const messageLine = cursor + (isSelected ? theme.bold(truncatedMsg) : truncatedMsg);
      lines.push(messageLine);
      const position = i + 1;
      const metadata = `  Message ${position} of ${this.messages.length}`;
      const metadataLine = theme.fg("muted", metadata);
      lines.push(metadataLine);
      lines.push("");
    }
    if (startIndex > 0 || endIndex < this.messages.length) {
      const scrollInfo = theme.fg("muted", `  (${this.selectedIndex + 1}/${this.messages.length})`);
      lines.push(scrollInfo);
    }
    return lines;
  }
  handleInput(keyData) {
    const kb = getKeybindings14();
    if (kb.matches(keyData, "tui.select.up")) {
      this.selectedIndex = this.selectedIndex === 0 ? this.messages.length - 1 : this.selectedIndex - 1;
    } else if (kb.matches(keyData, "tui.select.down")) {
      this.selectedIndex = this.selectedIndex === this.messages.length - 1 ? 0 : this.selectedIndex + 1;
    } else if (kb.matches(keyData, "tui.select.confirm")) {
      const selected = this.messages[this.selectedIndex];
      if (selected && this.onSelect) {
        this.onSelect(selected.id);
      }
    } else if (kb.matches(keyData, "tui.select.cancel")) {
      if (this.onCancel) {
        this.onCancel();
      }
    }
  }
};
var UserMessageSelectorComponent = class extends Container29 {
  static {
    __name(this, "UserMessageSelectorComponent");
  }
  messageList;
  constructor(messages, onSelect, onCancel, initialSelectedId) {
    super();
    this.addChild(new Spacer26(1));
    this.addChild(new Text29(theme.bold("Fork from Message"), 1, 0));
    this.addChild(new Text29(theme.fg("muted", "Select a user message to copy the active path up to that point into a new session"), 1, 0));
    this.addChild(new Spacer26(1));
    this.addChild(new DynamicBorder());
    this.addChild(new Spacer26(1));
    this.messageList = new UserMessageList(messages, initialSelectedId);
    this.messageList.onSelect = onSelect;
    this.messageList.onCancel = onCancel;
    this.addChild(this.messageList);
    this.addChild(new Spacer26(1));
    this.addChild(new DynamicBorder());
    if (messages.length === 0) {
      setTimeout(() => onCancel(), 100);
    }
  }
  getMessageList() {
    return this.messageList;
  }
};

// pi-dist/pi-coding-agent/modes/interactive/program-status-reporter.js
function firstLine2(text) {
  return text?.split(/\r?\n/, 1)[0]?.trim() || "Error";
}
__name(firstLine2, "firstLine");
var ProgramStatusReporter = class {
  static {
    __name(this, "ProgramStatusReporter");
  }
  getTerminal;
  getSessionName;
  runActive = false;
  compacting = false;
  /** Outcome of the current run, reported once it settles. */
  runResult = { state: "done" };
  /** Status while no run is active. */
  restingStatus = { state: "idle" };
  /** Open dialogs by source, in the order they opened. The most recent one is reported. */
  blocked = /* @__PURE__ */ new Map();
  lastReport;
  constructor(getTerminal, getSessionName) {
    this.getTerminal = getTerminal;
    this.getSessionName = getSessionName;
  }
  handleEvent(event) {
    switch (event.type) {
      case "agent_start":
        this.runActive = true;
        this.runResult = { state: "done" };
        break;
      case "message_end":
        if (event.message.role !== "assistant")
          return;
        this.runResult = event.message.stopReason === "error" ? { state: "error", message: firstLine2(event.message.errorMessage) } : { state: "done" };
        break;
      case "compaction_start":
        this.compacting = true;
        break;
      case "compaction_end":
        this.compacting = false;
        if (this.runActive) {
          if (event.aborted)
            this.runResult = { state: "idle" };
          else if (event.errorMessage)
            this.runResult = { state: "error", message: firstLine2(event.errorMessage) };
        } else if (event.aborted) {
          this.restingStatus = { state: "idle" };
        } else if (event.reason === "manual") {
          this.restingStatus = event.errorMessage ? { state: "error", message: firstLine2(event.errorMessage) } : { state: "done" };
        }
        break;
      case "agent_settled":
        this.runActive = false;
        this.restingStatus = event.aborted ? { state: "idle" } : this.runResult;
        break;
      case "session_info_changed":
        break;
      default:
        return;
    }
    this.report();
  }
  /** Report `blocked` for a dialog until it is cleared with `undefined`. Reopening a source replaces it. */
  setBlocked(source, status) {
    this.blocked.delete(source);
    if (status)
      this.blocked.set(source, status);
    this.report();
  }
  /** Forget the previous session's run, for example after switching sessions. */
  reset() {
    this.runActive = false;
    this.compacting = false;
    this.runResult = { state: "done" };
    this.restingStatus = { state: "idle" };
    this.report();
  }
  report() {
    const status = { ...this.currentStatus(), app: APP_NAME };
    const key = JSON.stringify(status);
    if (key === this.lastReport)
      return;
    this.lastReport = key;
    this.getTerminal().setProgramStatus(status);
  }
  currentStatus() {
    const blocked = [...this.blocked.values()].at(-1);
    if (blocked)
      return { state: "blocked", ...blocked };
    if (this.compacting)
      return { state: "working", message: "Compacting context" };
    const status = this.runActive ? { state: "working" } : this.restingStatus;
    if (status.state === "working" || status.state === "done")
      return { ...status, message: this.getSessionName() };
    return status;
  }
};

// pi-dist/pi-coding-agent/modes/interactive/tui-renderer.js
import { ProcessTerminal as ProcessTerminal2, TuiAltScreen as TuiAltScreen2, TuiMainScreen as TuiMainScreen2 } from "../../../pi-tui.mjs";
function createInteractiveTui(options) {
  const terminal = options.terminal ?? new ProcessTerminal2();
  if (options.tuiMode === "fullscreen") {
    const styleSearchMatch = /* @__PURE__ */ __name((text) => theme.bg("searchMatchBg", theme.fg("searchMatchText", text)), "styleSearchMatch");
    return new TuiAltScreen2(terminal, options.showHardwareCursor, options.logDirectory, {
      searchMatchStyle: /* @__PURE__ */ __name((text) => theme.underline(styleSearchMatch(text)), "searchMatchStyle"),
      searchCurrentMatchStyle: /* @__PURE__ */ __name((text) => theme.bold(theme.inverse(styleSearchMatch(text))), "searchCurrentMatchStyle"),
      searchNavigationButtonStyle: /* @__PURE__ */ __name((text, hovered) => hovered ? theme.underline(text) : text, "searchNavigationButtonStyle"),
      scrollToEndIndicator: /* @__PURE__ */ __name(() => {
        const shortcut = keyDisplayText("tui.altScreen.bottom");
        const label = ` \u2193 Jump to latest message${shortcut ? ` \xB7 ${shortcut}` : ""} `;
        return theme.bg("selectedBg", theme.fg("text", label));
      }, "scrollToEndIndicator"),
      openUrl: openBrowser,
      onRightClickPaste: options.onRightClickPaste,
      copyOnSelect: options.fullscreenCopyOnSelect,
      wheelScrollLines: options.fullscreenWheelScrollLines ?? "auto",
      copySelection: /* @__PURE__ */ __name(async (text) => {
        try {
          await copyToClipboard(text);
          return true;
        } catch (error) {
          return error instanceof Error ? error.message : String(error);
        }
      }, "copySelection")
    });
  }
  return new TuiMainScreen2(terminal, options.showHardwareCursor, options.logDirectory);
}
__name(createInteractiveTui, "createInteractiveTui");
function createInteractiveTuiReference(getTui) {
  return new Proxy({}, {
    get: /* @__PURE__ */ __name((_target, property) => {
      const tui = getTui();
      const value = Reflect.get(tui, property, tui);
      if (typeof value !== "function")
        return value;
      let methodTui = tui;
      let method = value;
      return (...args) => {
        const currentTui = getTui();
        if (currentTui !== methodTui) {
          const currentMethod = Reflect.get(currentTui, property, currentTui);
          if (typeof currentMethod !== "function") {
            throw new TypeError(`TUI property ${String(property)} is not callable`);
          }
          methodTui = currentTui;
          method = currentMethod;
        }
        return Reflect.apply(method, methodTui, args);
      };
    }, "get"),
    set: /* @__PURE__ */ __name((_target, property, value) => {
      const tui = getTui();
      return Reflect.set(tui, property, value, tui);
    }, "set"),
    has: /* @__PURE__ */ __name((_target, property) => Reflect.has(getTui(), property), "has"),
    getPrototypeOf: /* @__PURE__ */ __name(() => Reflect.getPrototypeOf(getTui()), "getPrototypeOf")
  });
}
__name(createInteractiveTuiReference, "createInteractiveTuiReference");

// pi-dist/pi-coding-agent/modes/interactive/interactive-mode.js
function isWorkingStatusEditor(editor) {
  return "embedWorkingStatus" in editor && editor.embedWorkingStatus === true && "setWorkingStatusIndicator" in editor && typeof editor.setWorkingStatusIndicator === "function";
}
__name(isWorkingStatusEditor, "isWorkingStatusEditor");
function isExpandable(obj) {
  return typeof obj === "object" && obj !== null && "setExpanded" in obj && typeof obj.setExpanded === "function";
}
__name(isExpandable, "isExpandable");
var ExpandableText = class extends ThemedText {
  static {
    __name(this, "ExpandableText");
  }
  state;
  constructor(getCollapsedText, getExpandedText, expanded = false, paddingX = 0, paddingY = 0) {
    const state = { expanded };
    super(() => state.expanded ? getExpandedText() : getCollapsedText(), paddingX, paddingY);
    this.state = state;
  }
  setExpanded(expanded) {
    this.state.expanded = expanded;
    this.invalidate();
  }
};
var BuiltInHeader = class extends ExpandableText {
  static {
    __name(this, "BuiltInHeader");
  }
  onLogoClick;
  handleMouse(event) {
    if (event.type !== "click" || event.y > 1 || event.x < 1 || event.x > 4 || !this.onLogoClick)
      return void 0;
    this.onLogoClick(event.screenX - event.x + 1, event.screenY - event.y);
    return { handled: true };
  }
};
function isCustomSessionEntry(item) {
  return "type" in item && item.type === "custom";
}
__name(isCustomSessionEntry, "isCustomSessionEntry");
function isCompactionCostNotice(item) {
  return "type" in item && item.type === "compaction_cost";
}
__name(isCompactionCostNotice, "isCompactionCostNotice");
function isUsageSessionEntry(item) {
  return "type" in item && item.type === "usage";
}
__name(isUsageSessionEntry, "isUsageSessionEntry");
var DEAD_TERMINAL_ERROR_CODES = /* @__PURE__ */ new Set(["EIO", "EPIPE", "ENOTCONN", "ENOTTY"]);
function isDeadTerminalError(error) {
  if (!error || typeof error !== "object" || !("code" in error)) {
    return false;
  }
  const code = error.code;
  return code !== void 0 && DEAD_TERMINAL_ERROR_CODES.has(code);
}
__name(isDeadTerminalError, "isDeadTerminalError");
function formatCrashExtensionHint(extensionMatches) {
  const matches = Array.isArray(extensionMatches) ? extensionMatches.filter((match) => typeof match === "string" && match.length > 0) : [];
  if (matches.length === 0)
    return void 0;
  const quoted = matches.map((match) => `\`${match}\``);
  const labels = quoted.length === 1 ? quoted[0] : quoted.length === 2 ? quoted.join(" and ") : `${quoted.slice(0, -1).join(", ")}, and ${quoted[quoted.length - 1]}`;
  const noun = matches.length === 1 ? "extension" : "extensions";
  const pronoun = matches.length === 1 ? "it" : "them";
  return `A stack frame came from loaded ${noun} ${labels}, which may be involved. Try disabling ${pronoun} with \`${APP_NAME} config\`, or run \`${APP_NAME} -ne\` to confirm.`;
}
__name(formatCrashExtensionHint, "formatCrashExtensionHint");
var ANTHROPIC_SUBSCRIPTION_AUTH_WARNING = "Anthropic subscription auth is active. Third-party harness usage draws from extra usage and is billed per token, not your Claude plan limits. Manage extra usage at https://claude.ai/settings/usage. Disable this warning in /settings.";
function isAnthropicSubscriptionAuthKey(apiKey) {
  return typeof apiKey === "string" && apiKey.startsWith("sk-ant-oat");
}
__name(isAnthropicSubscriptionAuthKey, "isAnthropicSubscriptionAuthKey");
function isUnknownModel(model) {
  return !!model && model.provider === "unknown" && model.id === "unknown" && model.api === "unknown";
}
__name(isUnknownModel, "isUnknownModel");
function quoteIfNeeded(value) {
  if (value.length > 0 && !/[^a-zA-Z0-9_\-./~:@]/.test(value)) {
    return value;
  }
  return `'${value.replace(/'/g, `'\\''`)}'`;
}
__name(quoteIfNeeded, "quoteIfNeeded");
function formatResumeCommand(sessionManager) {
  if (!process.stdout.isTTY)
    return void 0;
  if (!sessionManager.isPersisted())
    return void 0;
  const sessionFile = sessionManager.getSessionFile();
  if (!sessionFile || !fs3.existsSync(sessionFile))
    return void 0;
  const args = [APP_NAME];
  if (!sessionManager.usesDefaultSessionDir()) {
    args.push("--session-dir", quoteIfNeeded(sessionManager.getSessionDir()));
  }
  args.push("--session", sessionManager.getSessionId());
  return args.join(" ");
}
__name(formatResumeCommand, "formatResumeCommand");
function hasDefaultModelProvider(providerId) {
  return providerId in defaultModelPerProvider;
}
__name(hasDefaultModelProvider, "hasDefaultModelProvider");
function llamaCppPostLoginGuidance(actionLabel, loadedModelCount) {
  return loadedModelCount === 0 ? `${actionLabel}. No llama.cpp models are loaded. Use /llama to load a model, then /model to select it.` : `${actionLabel}. Use /model to select a loaded llama.cpp model, or /llama to manage models.`;
}
__name(llamaCppPostLoginGuidance, "llamaCppPostLoginGuidance");
var RADIUS_LOGIN_INTRO = "Radius is a service crafted for Pi by the builders of Pi, Earendil Works";
var AUTH_TYPE_ORDER = { oauth: 0, api_key: 1 };
function createFuzzyAutocompleteItems(items, prefix, getSearchText, toAutocompleteItem) {
  const filtered = fuzzyFilter8(items, prefix, getSearchText);
  if (filtered.length === 0)
    return null;
  return filtered.map(toAutocompleteItem);
}
__name(createFuzzyAutocompleteItems, "createFuzzyAutocompleteItems");
function getLoginProviderCompletionOptions(providerOptions) {
  const byId = /* @__PURE__ */ new Map();
  for (const provider of providerOptions) {
    const existing = byId.get(provider.id);
    if (existing) {
      if (!existing.authTypes.includes(provider.authType)) {
        existing.authTypes.push(provider.authType);
        existing.authTypes.sort((a, b) => AUTH_TYPE_ORDER[a] - AUTH_TYPE_ORDER[b]);
      }
      continue;
    }
    byId.set(provider.id, {
      id: provider.id,
      name: provider.name,
      authTypes: [provider.authType],
      subscription: provider.subscription
    });
  }
  return Array.from(byId.values()).sort((a, b) => a.name.localeCompare(b.name));
}
__name(getLoginProviderCompletionOptions, "getLoginProviderCompletionOptions");
function getLoginProviderSearchText(provider) {
  const authTypes = provider.authTypes.map((authType) => `${authType} ${formatAuthSelectorProviderType(authType, provider.subscription)}`).join(" ");
  return `${provider.id} ${provider.name} ${authTypes}`;
}
__name(getLoginProviderSearchText, "getLoginProviderSearchText");
function formatLoginProviderCompletionDescription(provider) {
  const authTypes = provider.authTypes.map((authType) => formatAuthSelectorProviderType(authType, provider.subscription)).join("/");
  return provider.name === provider.id ? authTypes : `${provider.name} \xB7 ${authTypes}`;
}
__name(formatLoginProviderCompletionDescription, "formatLoginProviderCompletionDescription");
var InteractiveMode = class _InteractiveMode {
  static {
    __name(this, "InteractiveMode");
  }
  runtimeHost;
  renderer;
  ui;
  mainScreenRenderState;
  loadedResourcesContainer;
  chatContainer;
  documentContainer;
  transcriptScrollView;
  fullscreenLayoutRoot;
  pendingMessagesContainer;
  statusContainer;
  defaultEditor;
  editor;
  editorComponentFactory;
  autocompleteProvider;
  autocompleteProviderWrappers = [];
  fdPath;
  editorContainer;
  activeSelectorToken;
  activeSelectorDispose;
  footer;
  footerContainer;
  footerDataProvider;
  // Stored so the same manager can be injected into custom editors, selectors, and extension UI.
  keybindings;
  version;
  isInitialized = false;
  onInputCallback;
  pendingUserInputs = [];
  activeStatusIndicator = void 0;
  activeWorkingIndicatorEmbedded = false;
  idleStatus = new IdleStatus();
  workingMessage = void 0;
  workingVisible = true;
  workingIndicatorOptions = void 0;
  defaultWorkingMessage = "Working";
  defaultHiddenThinkingLabel = "Thinking...";
  hiddenThinkingLabel = this.defaultHiddenThinkingLabel;
  lastSigintTime = 0;
  lastEscapeTime = 0;
  changelogMarkdown = void 0;
  startupNoticesShown = false;
  anthropicSubscriptionWarningShown = false;
  // Status line tracking (for mutating immediately-sequential status updates)
  lastStatusSpacer = void 0;
  lastStatusText = void 0;
  lastStatusMessage = "";
  managedToolStatusStarted = false;
  // Streaming message tracking
  streamingComponent = void 0;
  entriesRenderedByBoundaryCompaction = /* @__PURE__ */ new Set();
  streamingMessage = void 0;
  // Tool execution tracking: toolCallId -> component
  pendingTools = /* @__PURE__ */ new Map();
  // Tool output expansion state
  toolOutputExpanded = false;
  // Thinking block visibility state
  hideThinkingBlock = false;
  outputPad = 1;
  mermaidMarkdownTransformer = createMermaidMarkdownTransformer({
    getMode: /* @__PURE__ */ __name(() => this.settingsManager.getMermaidRenderingMode(), "getMode"),
    theme
  });
  // Skill commands: command name -> skill file path
  skillCommands = /* @__PURE__ */ new Map();
  // Agent subscription unsubscribe function
  unsubscribe;
  signalCleanupHandlers = [];
  // Track if editor is in bash mode (text starts with !)
  isBashMode = false;
  // Track current bash execution component
  bashComponent = void 0;
  // Track pending bash components (shown in pending area, moved to chat on submit)
  pendingBashComponents = [];
  // Auto-compaction state
  autoCompactionEscapeHandler;
  // Auto-retry state
  retryEscapeHandler;
  // Messages queued while compaction is running
  compactionQueuedMessages = [];
  // Shutdown state
  shutdownRequested = false;
  /** Reports working, blocked, done, and error states to terminals that support OSC 7501. */
  programStatus = new ProgramStatusReporter(() => this.ui.terminal, () => this.sessionManager.getSessionName());
  /** The `/bug` hint is shown at most once per session so error output stays readable. */
  bugReportHintShown = false;
  installChangeWarningShown = false;
  // Extension UI state
  extensionSelector = void 0;
  extensionInput = void 0;
  extensionEditor = void 0;
  extensionTerminalInputSubscriptions = /* @__PURE__ */ new Set();
  // Extension widgets (components rendered above/below the editor)
  extensionWidgetsAbove = /* @__PURE__ */ new Map();
  extensionWidgetsBelow = /* @__PURE__ */ new Map();
  widgetContainerAbove;
  widgetContainerBelow;
  // Custom footer from extension (undefined = use built-in footer)
  customFooter = void 0;
  // Header container that holds the built-in or custom header
  headerContainer;
  // Built-in header (logo + keybinding hints + changelog)
  builtInHeader = void 0;
  // Custom header from extension (undefined = use built-in header)
  customHeader = void 0;
  options;
  onRightClickPaste = /* @__PURE__ */ __name(() => {
    void this.handleRightClickPaste();
  }, "onRightClickPaste");
  autoTrustOnReloadCwd;
  themeController;
  // Convenience accessors
  get session() {
    return this.runtimeHost.session;
  }
  get agent() {
    return this.session.agent;
  }
  get sessionManager() {
    return this.session.sessionManager;
  }
  get settingsManager() {
    return this.session.settingsManager;
  }
  constructor(runtimeHost, options = {}) {
    this.runtimeHost = runtimeHost;
    setCapabilityOverrides2(this.settingsManager.getTerminalCapabilityOverrides());
    const tuiMode = options.tuiMode ?? this.settingsManager.getTuiMode();
    this.options = { ...options, tuiMode };
    this.autoTrustOnReloadCwd = options.autoTrustOnReloadCwd;
    this.runtimeHost.setBeforeSessionInvalidate(() => {
      this.resetExtensionUI();
    });
    this.runtimeHost.setRebindSession(async () => {
      await this.rebindCurrentSession({ renderBeforeBind: true });
      this.themeController.applyFromSettings();
    });
    this.version = VERSION;
    this.renderer = createInteractiveTui({
      tuiMode,
      showHardwareCursor: this.settingsManager.getShowHardwareCursor(),
      logDirectory: getAgentDir(),
      terminal: options.terminal,
      onRightClickPaste: this.onRightClickPaste,
      fullscreenCopyOnSelect: this.settingsManager.getFullscreenCopyOnSelect(),
      fullscreenWheelScrollLines: this.settingsManager.getFullscreenWheelScrollLines()
    });
    this.ui = createInteractiveTuiReference(() => this.renderer);
    this.ui.setClearOnShrink(this.settingsManager.getClearOnShrink());
    this.headerContainer = new Container30();
    this.loadedResourcesContainer = new Container30();
    this.chatContainer = new Container30();
    this.documentContainer = new Container30();
    this.documentContainer.addChild(this.headerContainer);
    this.documentContainer.addChild(this.loadedResourcesContainer);
    this.documentContainer.addChild(this.chatContainer);
    this.pendingMessagesContainer = new Container30();
    this.statusContainer = new Container30();
    this.widgetContainerAbove = new Container30();
    this.widgetContainerBelow = new Container30();
    this.keybindings = KeybindingsManager.create();
    setKeybindings3(this.keybindings);
    const editorPaddingX = this.settingsManager.getEditorPaddingX();
    const autocompleteMaxVisible = this.settingsManager.getAutocompleteMaxVisible();
    this.defaultEditor = new CustomEditor(this.ui, getEditorTheme(), this.keybindings, {
      paddingX: editorPaddingX,
      autocompleteMaxVisible,
      embedWorkingStatus: true
    });
    this.editor = this.defaultEditor;
    this.editorContainer = new Container30();
    this.editorContainer.addChild(this.editor);
    this.footerDataProvider = new FooterDataProvider(this.sessionManager.getCwd());
    this.footer = new FooterComponent(this.session, this.footerDataProvider);
    this.footer.setAutoCompactEnabled(this.session.autoCompactionEnabled);
    this.footerContainer = new Container30();
    this.footerContainer.addChild(this.footer);
    this.hideThinkingBlock = this.settingsManager.getHideThinkingBlock();
    this.outputPad = this.settingsManager.getOutputPad();
    setRegisteredThemes(this.session.resourceLoader.getThemes().themes);
    this.themeController = new InteractiveThemeController(this.ui, {
      getSettingsManager: /* @__PURE__ */ __name(() => this.settingsManager, "getSettingsManager"),
      showError: /* @__PURE__ */ __name((message) => this.showError(message), "showError"),
      onChanged: /* @__PURE__ */ __name(() => this.updateEditorBorderColor(), "onChanged"),
      initialThemeSetting: options.initialThemeSetting
    });
  }
  getAutocompleteSourceTag(sourceInfo) {
    if (!sourceInfo || sourceInfo.source === "builtin") {
      return void 0;
    }
    const scopePrefix = sourceInfo.scope === "user" ? "u" : sourceInfo.scope === "project" ? "p" : "t";
    const source = sourceInfo.source.trim();
    if (source === "auto" || source === "local" || source === "cli") {
      return scopePrefix;
    }
    if (source.startsWith("npm:")) {
      return `${scopePrefix}:${source}`;
    }
    const gitSource = parseGitUrl(source);
    if (gitSource) {
      const ref = gitSource.ref ? `@${gitSource.ref}` : "";
      return `${scopePrefix}:git:${gitSource.host}/${gitSource.path}${ref}`;
    }
    return scopePrefix;
  }
  prefixAutocompleteDescription(description, sourceInfo) {
    const sourceTag = this.getAutocompleteSourceTag(sourceInfo);
    if (!sourceTag) {
      return description;
    }
    return description ? `[${sourceTag}] ${description}` : `[${sourceTag}]`;
  }
  getBuiltInCommandConflictDiagnostics(extensionRunner) {
    const builtinNames = new Set(BUILTIN_SLASH_COMMANDS.map((command) => command.name));
    return extensionRunner.getRegisteredCommands().filter((command) => builtinNames.has(command.name)).map((command) => ({
      type: "warning",
      message: command.invocationName === command.name ? `Extension command '/${command.name}' conflicts with built-in interactive command. Skipping in autocomplete.` : `Extension command '/${command.name}' conflicts with built-in interactive command. Available as '/${command.invocationName}'.`,
      path: command.sourceInfo.path
    }));
  }
  createBaseAutocompleteProvider() {
    const slashCommands = BUILTIN_SLASH_COMMANDS.map((command) => ({
      name: command.name,
      description: command.description,
      ...command.argumentHint && { argumentHint: command.argumentHint }
    }));
    const modelCommand = slashCommands.find((command) => command.name === "model");
    if (modelCommand) {
      modelCommand.getArgumentCompletions = (prefix) => {
        const models = this.session.scopedModels.length > 0 ? this.session.scopedModels.map((s) => s.model) : this.session.modelRuntime.getAvailableSnapshot();
        if (models.length === 0)
          return null;
        const items = models.map((m) => ({
          id: m.id,
          provider: m.provider,
          name: m.name,
          label: `${m.provider}/${m.id}`
        }));
        return createFuzzyAutocompleteItems(items, prefix, getModelSearchText, (item) => ({
          value: item.label,
          label: item.id,
          description: item.provider
        }));
      };
    }
    const thinkingCommand = slashCommands.find((command) => command.name === "thinking");
    if (thinkingCommand) {
      thinkingCommand.getArgumentCompletions = (prefix) => {
        return createFuzzyAutocompleteItems(this.session.getAvailableThinkingLevels(), prefix, (level) => level, (level) => ({
          value: level,
          label: level
        }));
      };
    }
    const loginCommand = slashCommands.find((command) => command.name === "login");
    if (loginCommand) {
      loginCommand.getArgumentCompletions = (prefix) => {
        const providers = getLoginProviderCompletionOptions(this.getLoginProviderOptions());
        return createFuzzyAutocompleteItems(providers, prefix, getLoginProviderSearchText, (provider) => ({
          value: provider.id,
          label: provider.id,
          description: formatLoginProviderCompletionDescription(provider)
        }));
      };
    }
    const templateCommands = this.session.promptTemplates.map((cmd) => ({
      name: cmd.name,
      description: this.prefixAutocompleteDescription(cmd.description, cmd.sourceInfo),
      ...cmd.argumentHint && { argumentHint: cmd.argumentHint }
    }));
    const builtinCommandNames = new Set(slashCommands.map((c2) => c2.name));
    const extensionCommands = this.session.extensionRunner.getRegisteredCommands().filter((cmd) => !builtinCommandNames.has(cmd.name)).map((cmd) => ({
      name: cmd.invocationName,
      description: this.prefixAutocompleteDescription(cmd.description, cmd.sourceInfo),
      getArgumentCompletions: cmd.getArgumentCompletions
    }));
    this.skillCommands.clear();
    const skillCommandList = [];
    if (this.settingsManager.getEnableSkillCommands()) {
      for (const skill of this.session.resourceLoader.getSkills().skills) {
        const commandName = `skill:${skill.name}`;
        this.skillCommands.set(commandName, skill.filePath);
        skillCommandList.push({
          name: commandName,
          description: this.prefixAutocompleteDescription(skill.description, skill.sourceInfo)
        });
      }
    }
    return new CombinedAutocompleteProvider([...slashCommands, ...templateCommands, ...extensionCommands, ...skillCommandList], this.sessionManager.getCwd(), this.fdPath);
  }
  setupAutocompleteProvider() {
    let provider = this.createBaseAutocompleteProvider();
    const triggerCharacters = [];
    for (const wrapProvider of this.autocompleteProviderWrappers) {
      provider = wrapProvider(provider);
      triggerCharacters.push(...provider.triggerCharacters ?? []);
    }
    if (triggerCharacters.length > 0) {
      provider.triggerCharacters = [...new Set(triggerCharacters)];
    }
    this.autocompleteProvider = provider;
    this.defaultEditor.setAutocompleteProvider(provider);
    if (this.editor !== this.defaultEditor) {
      this.editor.setAutocompleteProvider?.(provider);
    }
  }
  showStartupNoticesIfNeeded() {
    if (this.startupNoticesShown) {
      return;
    }
    this.startupNoticesShown = true;
    if (!this.changelogMarkdown) {
      return;
    }
    if (this.chatContainer.children.length > 0) {
      this.chatContainer.addChild(new Spacer27(1));
    }
    this.chatContainer.addChild(new DynamicBorder());
    if (this.settingsManager.getCollapseChangelog()) {
      const versionMatch = this.changelogMarkdown.match(/##\s+\[?(\d+\.\d+\.\d+)\]?/);
      const latestVersion = versionMatch ? versionMatch[1] : this.version;
      const condensedText = `Updated to v${latestVersion}. Use ${theme.bold("/changelog")} to view full changelog.`;
      this.chatContainer.addChild(new Text30(condensedText, 1, 0));
    } else {
      this.chatContainer.addChild(new ThemedText(() => theme.bold(theme.fg("accent", "What's New")), 1, 0));
      this.chatContainer.addChild(new Spacer27(1));
      this.chatContainer.addChild(new Markdown7(this.changelogMarkdown.trim(), 1, 0, this.getMarkdownThemeWithSettings()));
      this.chatContainer.addChild(new Spacer27(1));
    }
    this.chatContainer.addChild(new DynamicBorder());
  }
  mountInteractiveTui(tui, components) {
    for (const component of components)
      tui.addChild(component);
    if (TuiLayouts.isViewportTUI(tui)) {
      if (!this.fullscreenLayoutRoot)
        throw new Error("Fullscreen layout is not initialized");
      tui.setLayoutRoot(this.fullscreenLayoutRoot);
    }
  }
  stopInteractiveTui(fullscreenExitOutput) {
    if (this.renderer.mode === "fullscreen" && fullscreenExitOutput === "transcript") {
      while (this.renderer.hasOverlayEntries)
        this.renderer.hideOverlay();
      this.switchTuiMode("regular", false, false);
      this.renderer.renderNow();
    }
    this.ui.stop({ preserveScreen: this.renderer.mode === "fullscreen" });
  }
  switchTuiMode(mode, restoreProgress = true, startRenderer = true) {
    const previousUi = this.renderer;
    if (mode === previousUi.mode)
      return true;
    if (previousUi.hasOverlayEntries)
      return false;
    const components = [...previousUi.children];
    const focus = previousUi.getFocusedComponent();
    const terminal = previousUi.terminal;
    const showHardwareCursor = previousUi.getShowHardwareCursor();
    const clearOnShrink = previousUi.getClearOnShrink();
    const onDebug = previousUi.onDebug;
    if (previousUi instanceof TuiMainScreen3) {
      this.mainScreenRenderState = previousUi.captureRenderState();
    }
    previousUi.stop({ preserveScreen: true });
    previousUi.setFocus(null);
    previousUi.clear();
    if (TuiLayouts.isViewportTUI(previousUi))
      previousUi.setLayoutRoot(void 0);
    const nextUi = createInteractiveTui({
      tuiMode: mode,
      showHardwareCursor,
      logDirectory: getAgentDir(),
      terminal,
      onRightClickPaste: this.onRightClickPaste,
      fullscreenCopyOnSelect: this.settingsManager.getFullscreenCopyOnSelect(),
      fullscreenWheelScrollLines: this.settingsManager.getFullscreenWheelScrollLines()
    });
    nextUi.setClearOnShrink(clearOnShrink);
    nextUi.onDebug = onDebug;
    if (nextUi instanceof TuiMainScreen3 && this.mainScreenRenderState) {
      nextUi.restoreRenderState(this.mainScreenRenderState);
    }
    this.renderer = nextUi;
    this.options.tuiMode = mode;
    this.mountInteractiveTui(nextUi, components);
    nextUi.invalidate();
    nextUi.setFocus(focus);
    if (!startRenderer)
      return true;
    nextUi.start();
    this.themeController.rebindTui();
    this.rebindExtensionTerminalInputListeners();
    if (restoreProgress && this.settingsManager.getShowTerminalProgress() && (this.session.isStreaming || this.session.isCompacting)) {
      terminal.setProgress(true);
    }
    return true;
  }
  async init() {
    if (this.isInitialized)
      return;
    this.registerSignalHandlers();
    this.changelogMarkdown = this.getChangelogForDisplay();
    if (this.session.scopedModels.length > 0 && this.shouldShowStartupDetails()) {
      const modelList = this.session.scopedModels.map((sm) => {
        const thinkingStr = sm.thinkingLevel ? `:${sm.thinkingLevel}` : "";
        return `${sm.model.id}${thinkingStr}`;
      }).join(", ");
      const cycleKeys = this.keybindings.getKeys("app.model.cycleForward");
      const cycleHint = cycleKeys.length > 0 ? theme.fg("muted", ` (${formatKeyText(cycleKeys.join("/"), { capitalize: true })} to cycle)`) : "";
      console.log(theme.fg("dim", `Model scope: ${modelList}${cycleHint}`));
    }
    this.renderWidgets();
    const viewport = createChatViewport({
      document: this.documentContainer,
      pendingMessages: this.pendingMessagesContainer,
      status: this.statusContainer,
      widgetsAbove: this.widgetContainerAbove,
      editor: this.editorContainer,
      widgetsBelow: this.widgetContainerBelow,
      footer: this.footerContainer,
      scrollbar: this.settingsManager.getFullscreenScrollbar(),
      scrollbarTrackStyle: /* @__PURE__ */ __name((text) => theme.fg("scrollbarTrack", text), "scrollbarTrackStyle"),
      scrollbarThumbStyle: /* @__PURE__ */ __name((text) => theme.fg("scrollbarThumb", text), "scrollbarThumbStyle")
    });
    this.transcriptScrollView = viewport.transcript;
    this.fullscreenLayoutRoot = viewport.root;
    this.mountInteractiveTui(this.renderer, [
      this.documentContainer,
      this.pendingMessagesContainer,
      this.statusContainer,
      this.widgetContainerAbove,
      this.editorContainer,
      this.widgetContainerBelow,
      this.footerContainer
    ]);
    this.defaultEditor.onAction("app.clear", () => this.handleCtrlC());
    this.defaultEditor.onCtrlD = () => this.handleCtrlD();
    this.defaultEditor.onSubmit = (text) => this.handleStartupSubmit(text);
    this.ui.setFocus(this.editor);
    this.ui.start();
    this.isInitialized = true;
    this.programStatus.report();
    this.ensurePngTranscoder();
    this.themeController.applyFromSettings();
    await this.themeController.waitForTerminalColors();
    if (this.shouldShowStartupHeader()) {
      const showDetails = this.shouldShowStartupDetails();
      const showLogo = supportsPiLogo();
      const withLogo = /* @__PURE__ */ __name((hints) => {
        if (!showLogo)
          return `${piWordmark()} ${theme.fg("dim", `v${this.version}`)}
${hints}`;
        const [top, bottom] = piLogoLines();
        return `${top} ${theme.fg("dim", `v${this.version}`)}
${bottom} ${hints}`;
      }, "withLogo");
      const hint = /* @__PURE__ */ __name((keybinding, description) => keyHint(keybinding, description), "hint");
      const expandedInstructions = /* @__PURE__ */ __name(() => [
        hint("app.interrupt", "to interrupt"),
        hint("app.clear", "to clear"),
        rawKeyHint(`${keyText("app.clear")} twice`, "to exit"),
        hint("app.exit", "to exit (empty)"),
        hint("app.suspend", "to suspend"),
        keyHint("tui.editor.deleteToLineEnd", "to delete to end"),
        hint("app.thinking.cycle", "to cycle thinking level"),
        rawKeyHint(`${keyText("app.model.cycleForward")}/${keyText("app.model.cycleBackward")}`, "to cycle models"),
        hint("app.model.select", "to select model"),
        hint("app.tools.expand", "to expand tools"),
        hint("app.thinking.toggle", "to expand thinking"),
        hint("app.editor.external", "for external editor"),
        rawKeyHint("/", "for commands"),
        rawKeyHint("!", "to run bash"),
        rawKeyHint("!!", "to run bash (no context)"),
        hint("app.message.followUp", "to queue follow-up"),
        hint("app.message.dequeue", "to edit all queued messages"),
        hint("app.clipboard.pasteImage", "to paste files on macOS, images, or text"),
        rawKeyHint("drop files", "to attach")
      ].join("\n"), "expandedInstructions");
      const compactInstructions = /* @__PURE__ */ __name(() => [
        hint("app.interrupt", "interrupt"),
        rawKeyHint(`${keyText("app.clear")}/${keyText("app.exit")}`, "clear/exit"),
        rawKeyHint("/", "commands"),
        rawKeyHint("!", "bash"),
        hint("app.tools.expand", "more")
      ].join(theme.fg("muted", " \xB7 ")), "compactInstructions");
      const compactOnboarding = /* @__PURE__ */ __name(() => theme.fg("dim", `Press ${keyText("app.tools.expand")} to show full startup help${showDetails ? " and loaded resources" : ""}.`), "compactOnboarding");
      const onboarding = /* @__PURE__ */ __name(() => theme.fg("dim", `Pi can explain its own features and look up its docs. Ask it how to use or extend Pi.`), "onboarding");
      const header = new BuiltInHeader(() => `${withLogo(compactInstructions())}
${compactOnboarding()}

${onboarding()}`, () => `${withLogo(expandedInstructions())}

${onboarding()}`, this.getStartupExpansionState(), 1, 0);
      if (showLogo)
        header.onLogoClick = (column, row) => playPiLogo3d(this.renderer, column, row);
      this.builtInHeader = header;
      this.headerContainer.addChild(new Spacer27(1));
      this.headerContainer.addChild(this.builtInHeader);
      this.headerContainer.addChild(new Spacer27(1));
    } else {
      this.builtInHeader = new Text30("", 0, 0);
      this.headerContainer.addChild(this.builtInHeader);
    }
    this.ui.requestRender();
    const [fdPath] = await Promise.all([
      ensureTool("fd", (status) => this.showManagedToolStatus(status)),
      ensureTool("rg", (status) => this.showManagedToolStatus(status))
    ]);
    this.fdPath = fdPath;
    this.setupKeyHandlers();
    this.setupEditorSubmitHandler();
    this.ui.requestRender();
    await this.rebindCurrentSession();
    this.renderInitialMessages();
    onThemeChange(() => {
      this.ui.invalidate();
      this.updateEditorBorderColor();
      this.ui.requestRender();
    });
    this.footerDataProvider.onBranchChange(() => {
      this.ui.requestRender();
    });
    await this.updateAvailableProviderCount();
    this.ui.renderNow();
    void loadAllHighlightLanguages().then(() => {
      if (!this.isInitialized)
        return;
      this.ui.invalidate();
      this.ui.requestRender();
    });
  }
  /**
   * Update terminal title with session name and cwd.
   */
  updateTerminalTitle() {
    const cwdBasename = path4.basename(this.sessionManager.getCwd());
    const sessionName = this.sessionManager.getSessionName();
    if (sessionName) {
      this.ui.terminal.setTitle(`${APP_TITLE} - ${sessionName} - ${cwdBasename}`);
    } else {
      this.ui.terminal.setTitle(`${APP_TITLE} - ${cwdBasename}`);
    }
  }
  /**
   * Run the interactive mode. This is the main entry point.
   * Initializes the UI, shows warnings, processes initial messages, and starts the interactive loop.
   */
  async run() {
    await this.init();
    if (!process.env.PI_OFFLINE) {
      const controller = new AbortController();
      const timeout = setTimeout(() => controller.abort(), 15e3);
      void refreshModelCatalogs(this.session.modelRuntime, controller.signal).then(() => this.updateAvailableProviderCount()).catch(() => {
      }).finally(() => clearTimeout(timeout));
    }
    checkForNewPiVersion(this.version).then((newRelease) => {
      if (newRelease) {
        this.showNewVersionNotification(newRelease);
      }
    });
    this.checkForPackageUpdates().then((updates) => {
      if (updates.length > 0) {
        this.showPackageUpdateNotification(updates);
      }
    }).finally(() => {
      if (process.platform === "win32" && this.isInitialized) {
        this.updateTerminalTitle();
      }
    });
    this.checkTmuxKeyboardSetup().then((warning) => {
      if (warning) {
        this.showWarning(warning);
      }
    });
    const { migratedProviders, startupDiagnostics, modelFallbackMessage, initialMessage, initialImages, initialMessages } = this.options;
    for (const diagnostic of startupDiagnostics ?? []) {
      if (diagnostic.type === "error") {
        this.showError(diagnostic.message);
      } else if (diagnostic.type === "warning") {
        this.showWarning(diagnostic.message);
      } else {
        this.showStatus(diagnostic.message);
      }
    }
    if (migratedProviders && migratedProviders.length > 0) {
      this.showWarning(`Migrated credentials to auth.json: ${migratedProviders.join(", ")}`);
    }
    const modelsJsonError = this.session.modelRuntime.getError();
    if (modelsJsonError) {
      this.showError(`models.json error: ${modelsJsonError}`);
    }
    if (modelFallbackMessage) {
      this.showWarning(modelFallbackMessage);
    }
    const crash = takeUnnotifiedCrash();
    if (crash) {
      const when = new Date(crash.timestamp).toLocaleString();
      this.showWarning(`${APP_NAME} crashed on ${when} (${crash.message}). Run /bug to report it; the crash details are attached automatically.`);
    }
    void this.maybeWarnAboutAnthropicSubscriptionAuth();
    if (initialMessage) {
      try {
        await this.session.prompt(initialMessage, { images: initialImages });
      } catch (error) {
        const errorMessage4 = error instanceof Error ? error.message : "Unknown error occurred";
        this.showError(errorMessage4);
      }
    }
    if (initialMessages) {
      for (const message of initialMessages) {
        try {
          await this.session.prompt(message);
        } catch (error) {
          const errorMessage4 = error instanceof Error ? error.message : "Unknown error occurred";
          this.showError(errorMessage4);
        }
      }
    }
    while (true) {
      const userInput = await this.getUserInput();
      try {
        await this.session.prompt(userInput);
      } catch (error) {
        const errorMessage4 = error instanceof Error ? error.message : "Unknown error occurred";
        this.showError(errorMessage4);
      }
    }
  }
  async checkForPackageUpdates() {
    if (process.env.PI_OFFLINE) {
      return [];
    }
    try {
      const packageManager = new DefaultPackageManager({
        cwd: this.sessionManager.getCwd(),
        agentDir: getAgentDir(),
        settingsManager: this.settingsManager
      });
      const updates = await packageManager.checkForAvailableUpdates();
      return updates.map((update) => update.displayName);
    } catch {
      return [];
    }
  }
  async checkTmuxKeyboardSetup() {
    if (!process.env.TMUX)
      return void 0;
    const runTmuxShow = /* @__PURE__ */ __name((option) => {
      return new Promise((resolve7) => {
        const proc = spawn4("tmux", ["show", "-gv", option], {
          stdio: ["ignore", "pipe", "ignore"]
        });
        let stdout = "";
        const timer = setTimeout(() => {
          proc.kill();
          resolve7(void 0);
        }, 2e3);
        proc.stdout?.on("data", (data) => {
          stdout += data.toString();
        });
        proc.on("error", () => {
          clearTimeout(timer);
          resolve7(void 0);
        });
        proc.on("close", (code) => {
          clearTimeout(timer);
          resolve7(code === 0 ? stdout.trim() : void 0);
        });
      });
    }, "runTmuxShow");
    const [extendedKeys, extendedKeysFormat] = await Promise.all([
      runTmuxShow("extended-keys"),
      runTmuxShow("extended-keys-format")
    ]);
    if (extendedKeys === void 0)
      return void 0;
    if (extendedKeys !== "on" && extendedKeys !== "always") {
      return "tmux extended-keys is off. Modified Enter keys may not work. Add `set -g extended-keys on` to ~/.tmux.conf and restart tmux.";
    }
    if (extendedKeysFormat === "xterm") {
      return "tmux extended-keys-format is xterm. Pi works best with csi-u. Add `set -g extended-keys-format csi-u` to ~/.tmux.conf and restart tmux.";
    }
    return void 0;
  }
  /**
   * Get changelog entries to display on startup.
   * Only shows new entries since last seen version, skips for resumed sessions.
   */
  getChangelogForDisplay() {
    if (this.session.state.messages.length > 0) {
      return void 0;
    }
    const lastVersion = this.settingsManager.getLastChangelogVersion();
    const changelogPath = getChangelogPath();
    const entries = parseChangelog(changelogPath);
    if (!lastVersion) {
      this.settingsManager.setLastChangelogVersion(VERSION);
      this.reportInstallTelemetry(VERSION);
      return void 0;
    }
    const newEntries = getNewEntries(entries, lastVersion);
    if (newEntries.length > 0) {
      this.settingsManager.setLastChangelogVersion(VERSION);
      this.reportInstallTelemetry(VERSION);
      return newEntries.map((e) => normalizeChangelogLinks(e.content, e)).join("\n\n");
    }
    return void 0;
  }
  reportInstallTelemetry(version) {
    if (process.env.PI_OFFLINE) {
      return;
    }
    if (!isInstallTelemetryEnabled(this.settingsManager)) {
      return;
    }
    void fetch(`https://pi-in-go.dev/api/report-install?version=${encodeURIComponent(version)}`, {
      headers: {
        "User-Agent": getPiUserAgent(version)
      },
      signal: AbortSignal.timeout(5e3)
    }).then(() => void 0).catch(() => void 0);
  }
  getMarkdownThemeWithSettings() {
    return {
      ...getMarkdownTheme(),
      codeBlockIndent: this.settingsManager.getCodeBlockIndent()
    };
  }
  // =========================================================================
  // Extension System
  // =========================================================================
  formatDisplayPath(p) {
    const home = os3.homedir();
    let result = p;
    if (result.startsWith(home)) {
      result = `~${result.slice(home.length)}`;
    }
    return result;
  }
  formatExtensionDisplayPath(path5) {
    let result = this.formatDisplayPath(path5);
    result = result.replace(/\/index\.ts$/, "").replace(/\/index\.js$/, "");
    return result;
  }
  formatContextPath(p) {
    const cwd = path4.resolve(this.sessionManager.getCwd());
    const absolutePath = path4.isAbsolute(p) ? path4.resolve(p) : path4.resolve(cwd, p);
    const relativePath = getCwdRelativePath(absolutePath, cwd);
    if (relativePath !== void 0) {
      return relativePath;
    }
    return this.formatDisplayPath(absolutePath);
  }
  getStartupExpansionState() {
    return this.options.verbose || this.toolOutputExpanded;
  }
  /** Startup header (logo, version, key hints). Hidden only by quietStartup: true. */
  shouldShowStartupHeader() {
    return this.options.verbose === true || this.settingsManager.getQuietStartup() !== true;
  }
  /** Startup details (model scope, loaded resources). Hidden by quietStartup: true or "header". */
  shouldShowStartupDetails() {
    return this.options.verbose === true || this.settingsManager.getQuietStartup() === false;
  }
  /**
   * Get a short path relative to the package root for display.
   */
  getShortPath(fullPath, sourceInfo) {
    const normalizedFullPath = fullPath.replace(/\\/g, "/");
    const baseDir = sourceInfo?.baseDir;
    if (baseDir && this.isPackageSource(sourceInfo)) {
      const normalizedBaseDir = baseDir.replace(/\\/g, "/");
      const npmRootMatch = normalizedBaseDir.match(/^(.*\/node_modules)\/(@?[^/]+(?:\/[^/]+)?)$/);
      if (npmRootMatch?.[1] && normalizedFullPath.startsWith(`${npmRootMatch[1]}/`)) {
        return path4.posix.relative(normalizedBaseDir, normalizedFullPath);
      }
      const relativePath = path4.relative(path4.resolve(baseDir), path4.resolve(fullPath));
      if (relativePath && relativePath !== "." && !relativePath.startsWith("..") && !relativePath.startsWith(`..${path4.sep}`) && !path4.isAbsolute(relativePath)) {
        return relativePath.replace(/\\/g, "/");
      }
    }
    const source = sourceInfo?.source ?? "";
    const npmMatch = normalizedFullPath.match(/node_modules\/(@?[^/]+(?:\/[^/]+)?)\/(.*)/);
    if (npmMatch && source.startsWith("npm:")) {
      return npmMatch[2];
    }
    const gitMatch = normalizedFullPath.match(/git\/[^/]+\/[^/]+\/(.*)/);
    if (gitMatch && source.startsWith("git:")) {
      return gitMatch[1];
    }
    return this.formatDisplayPath(fullPath);
  }
  getCompactPathLabel(resourcePath, sourceInfo) {
    const shortPath = this.getShortPath(resourcePath, sourceInfo);
    const normalizedPath = shortPath.replace(/\\/g, "/");
    const segments = normalizedPath.split("/").filter((segment) => segment.length > 0 && segment !== "~");
    if (segments.length > 0) {
      return segments[segments.length - 1];
    }
    return shortPath;
  }
  getCompactPackageSourceLabel(sourceInfo) {
    const source = sourceInfo?.source ?? "";
    if (source.startsWith("npm:")) {
      return source.slice("npm:".length) || source;
    }
    const gitSource = parseGitUrl(source);
    if (gitSource) {
      return gitSource.path || source;
    }
    return source;
  }
  getCompactExtensionLabel(resourcePath, sourceInfo) {
    if (!this.isPackageSource(sourceInfo)) {
      return this.getCompactPathLabel(resourcePath, sourceInfo);
    }
    const sourceLabel = this.getCompactPackageSourceLabel(sourceInfo);
    if (!sourceLabel) {
      return this.getCompactPathLabel(resourcePath, sourceInfo);
    }
    const shortPath = this.getShortPath(resourcePath, sourceInfo).replace(/\\/g, "/");
    const packagePath = shortPath.startsWith("extensions/") ? shortPath.slice("extensions/".length) : shortPath;
    const parsedPath = path4.posix.parse(packagePath);
    if (parsedPath.name === "index") {
      return !parsedPath.dir || parsedPath.dir === "." ? sourceLabel : `${sourceLabel}:${parsedPath.dir}`;
    }
    return `${sourceLabel}:${packagePath}`;
  }
  getCompactDisplayPathSegments(resourcePath) {
    return this.formatDisplayPath(resourcePath).replace(/\\/g, "/").split("/").filter((segment) => segment.length > 0 && segment !== "~");
  }
  getCompactNonPackageExtensionLabel(resourcePath, index, allPaths) {
    const segments = allPaths[index]?.segments;
    if (!segments || segments.length === 0) {
      return this.getCompactPathLabel(resourcePath);
    }
    for (let segmentCount = 1; segmentCount <= segments.length; segmentCount += 1) {
      const candidate = segments.slice(-segmentCount).join("/");
      const isUnique = allPaths.every((item, itemIndex) => {
        if (itemIndex === index) {
          return true;
        }
        return item.segments.slice(-segmentCount).join("/") !== candidate;
      });
      if (isUnique) {
        return candidate;
      }
    }
    return segments.join("/");
  }
  getCompactExtensionLabels(extensions) {
    const nonPackageExtensions = extensions.map((extension) => {
      const segments = this.getCompactDisplayPathSegments(extension.path);
      const lastSegment = segments[segments.length - 1];
      if (segments.length > 1 && (lastSegment === "index.ts" || lastSegment === "index.js")) {
        segments.pop();
      }
      return {
        path: extension.path,
        sourceInfo: extension.sourceInfo,
        segments
      };
    }).filter((extension) => !this.isPackageSource(extension.sourceInfo));
    return extensions.map((extension) => {
      if (this.isPackageSource(extension.sourceInfo)) {
        return this.getCompactExtensionLabel(extension.path, extension.sourceInfo);
      }
      const nonPackageIndex = nonPackageExtensions.findIndex((item) => item.path === extension.path);
      if (nonPackageIndex === -1) {
        return this.getCompactPathLabel(extension.path, extension.sourceInfo);
      }
      return this.getCompactNonPackageExtensionLabel(extension.path, nonPackageIndex, nonPackageExtensions);
    });
  }
  getDisplaySourceInfo(sourceInfo) {
    const source = sourceInfo?.source ?? "local";
    const scope = sourceInfo?.scope ?? "project";
    if (source === "local") {
      if (scope === "user") {
        return { label: "user", color: "muted" };
      }
      if (scope === "project") {
        return { label: "project", color: "muted" };
      }
      if (scope === "temporary") {
        return { label: "path", scopeLabel: "temp", color: "muted" };
      }
      return { label: "path", color: "muted" };
    }
    if (source === "cli") {
      return { label: "path", scopeLabel: scope === "temporary" ? "temp" : void 0, color: "muted" };
    }
    const scopeLabel = scope === "user" ? "user" : scope === "project" ? "project" : scope === "temporary" ? "temp" : void 0;
    return { label: source, scopeLabel, color: "accent" };
  }
  getScopeGroup(sourceInfo) {
    const source = sourceInfo?.source ?? "local";
    const scope = sourceInfo?.scope ?? "project";
    if (source === "cli" || scope === "temporary")
      return "path";
    if (scope === "user")
      return "user";
    if (scope === "project")
      return "project";
    return "path";
  }
  isPackageSource(sourceInfo) {
    const source = sourceInfo?.source ?? "";
    return source.startsWith("npm:") || source.startsWith("git:");
  }
  buildScopeGroups(items) {
    const groups = {
      user: { scope: "user", paths: [], packages: /* @__PURE__ */ new Map() },
      project: { scope: "project", paths: [], packages: /* @__PURE__ */ new Map() },
      path: { scope: "path", paths: [], packages: /* @__PURE__ */ new Map() }
    };
    for (const item of items) {
      const groupKey = this.getScopeGroup(item.sourceInfo);
      const group = groups[groupKey];
      const source = item.sourceInfo?.source ?? "local";
      if (this.isPackageSource(item.sourceInfo)) {
        const list = group.packages.get(source) ?? [];
        list.push(item);
        group.packages.set(source, list);
      } else {
        group.paths.push(item);
      }
    }
    return [groups.project, groups.user, groups.path].filter((group) => group.paths.length > 0 || group.packages.size > 0);
  }
  formatScopeGroups(groups, options) {
    const lines = [];
    for (const group of groups) {
      lines.push(`  ${theme.fg("accent", group.scope)}`);
      const sortedPaths = [...group.paths].sort((a, b) => a.path.localeCompare(b.path));
      for (const item of sortedPaths) {
        lines.push(theme.fg("dim", `    ${options.formatPath(item)}`));
      }
      const sortedPackages = Array.from(group.packages.entries()).sort(([a], [b]) => a.localeCompare(b));
      for (const [source, items] of sortedPackages) {
        lines.push(`    ${theme.fg("mdLink", source)}`);
        const sortedPackagePaths = [...items].sort((a, b) => a.path.localeCompare(b.path));
        for (const item of sortedPackagePaths) {
          lines.push(theme.fg("dim", `      ${options.formatPackagePath(item, source)}`));
        }
      }
    }
    return lines.join("\n");
  }
  findSourceInfoForPath(p, sourceInfos) {
    const exact = sourceInfos.get(p);
    if (exact)
      return exact;
    let current = p;
    while (current.includes("/")) {
      current = current.substring(0, current.lastIndexOf("/"));
      const parent = sourceInfos.get(current);
      if (parent)
        return parent;
    }
    return void 0;
  }
  formatPathWithSource(p, sourceInfo) {
    if (sourceInfo) {
      const shortPath = this.getShortPath(p, sourceInfo);
      const { label, scopeLabel } = this.getDisplaySourceInfo(sourceInfo);
      const labelText = scopeLabel ? `${label} (${scopeLabel})` : label;
      return `${labelText} ${shortPath}`;
    }
    return this.formatDisplayPath(p);
  }
  formatDiagnostics(diagnostics, sourceInfos) {
    const lines = [];
    const collisions = /* @__PURE__ */ new Map();
    const otherDiagnostics = [];
    for (const d of diagnostics) {
      if (d.type === "collision" && d.collision) {
        const list = collisions.get(d.collision.name) ?? [];
        list.push(d);
        collisions.set(d.collision.name, list);
      } else {
        otherDiagnostics.push(d);
      }
    }
    for (const [name, collisionList] of collisions) {
      const first = collisionList[0]?.collision;
      if (!first)
        continue;
      lines.push(theme.fg("warning", `  "${name}" collision:`));
      lines.push(theme.fg("dim", `    ${theme.fg("success", "\u2713")} ${this.formatPathWithSource(first.winnerPath, this.findSourceInfoForPath(first.winnerPath, sourceInfos))}`));
      for (const d of collisionList) {
        if (d.collision) {
          lines.push(theme.fg("dim", `    ${theme.fg("warning", "\u2717")} ${this.formatPathWithSource(d.collision.loserPath, this.findSourceInfoForPath(d.collision.loserPath, sourceInfos))} (skipped)`));
        }
      }
    }
    for (const d of otherDiagnostics) {
      if (d.path) {
        const formattedPath = this.formatPathWithSource(d.path, this.findSourceInfoForPath(d.path, sourceInfos));
        lines.push(theme.fg(d.type === "error" ? "error" : "warning", `  ${formattedPath}`));
        lines.push(theme.fg(d.type === "error" ? "error" : "warning", `    ${d.message}`));
      } else {
        lines.push(theme.fg(d.type === "error" ? "error" : "warning", `  ${d.message}`));
      }
    }
    return lines.join("\n");
  }
  showLoadedResources(options) {
    this.loadedResourcesContainer.clear();
    const showListing = options?.force || this.shouldShowStartupDetails();
    const showDiagnostics = showListing || options?.showDiagnosticsWhenQuiet === true;
    if (!showListing && !showDiagnostics) {
      return;
    }
    const sectionHeader = /* @__PURE__ */ __name((name, color = "mdHeading") => theme.fg(color, `[${name}]`), "sectionHeader");
    const formatCompactList = /* @__PURE__ */ __name((items, options2) => {
      const labels = items.map((item) => item.trim()).filter((item) => item.length > 0);
      if (options2?.sort !== false) {
        labels.sort((a, b) => a.localeCompare(b));
      }
      return theme.fg("dim", `  ${labels.join(", ")}`);
    }, "formatCompactList");
    const addLoadedSection = /* @__PURE__ */ __name((name, collapsedBody, expandedBody = collapsedBody, color = "mdHeading") => {
      const section = new ExpandableText(() => `${sectionHeader(name, color)}
${collapsedBody()}`, () => `${sectionHeader(name, color)}
${expandedBody()}`, this.getStartupExpansionState(), 0, 0);
      this.loadedResourcesContainer.addChild(section);
      this.loadedResourcesContainer.addChild(new Spacer27(1));
    }, "addLoadedSection");
    const skillsResult = this.session.resourceLoader.getSkills();
    const promptsResult = this.session.resourceLoader.getPrompts();
    const themesResult = this.session.resourceLoader.getThemes();
    const extensions = options?.extensions ?? this.session.resourceLoader.getExtensions().extensions.filter((extension) => !extension.hidden).map((extension) => ({
      path: extension.path,
      sourceInfo: extension.sourceInfo
    }));
    const sourceInfos = /* @__PURE__ */ new Map();
    for (const extension of extensions) {
      if (extension.sourceInfo) {
        sourceInfos.set(extension.path, extension.sourceInfo);
      }
    }
    for (const skill of skillsResult.skills) {
      if (skill.sourceInfo) {
        sourceInfos.set(skill.filePath, skill.sourceInfo);
      }
    }
    for (const prompt of promptsResult.prompts) {
      if (prompt.sourceInfo) {
        sourceInfos.set(prompt.filePath, prompt.sourceInfo);
      }
    }
    for (const loadedTheme of themesResult.themes) {
      if (loadedTheme.sourcePath && loadedTheme.sourceInfo) {
        sourceInfos.set(loadedTheme.sourcePath, loadedTheme.sourceInfo);
      }
    }
    if (showListing) {
      const systemPromptSource = this.session.resourceLoader.getSystemPromptSource();
      const contextFiles = [
        ...systemPromptSource ? [systemPromptSource] : [],
        ...this.session.resourceLoader.getAppendSystemPromptSources(),
        ...this.session.resourceLoader.getAgentsFiles().agentsFiles
      ];
      if (contextFiles.length > 0) {
        this.loadedResourcesContainer.addChild(new Spacer27(1));
        const contextList = /* @__PURE__ */ __name(() => contextFiles.map((f) => theme.fg("dim", `  ${this.formatDisplayPath(f.path)}`)).join("\n"), "contextList");
        const contextCompactList = /* @__PURE__ */ __name(() => formatCompactList(contextFiles.map((contextFile) => this.formatContextPath(contextFile.path)), { sort: false }), "contextCompactList");
        addLoadedSection("Context", contextCompactList, contextList);
      }
      const skills = skillsResult.skills;
      if (skills.length > 0) {
        const groups = this.buildScopeGroups(skills.map((skill) => ({ path: skill.filePath, sourceInfo: skill.sourceInfo })));
        const skillList = /* @__PURE__ */ __name(() => this.formatScopeGroups(groups, {
          formatPath: /* @__PURE__ */ __name((item) => this.formatDisplayPath(item.path), "formatPath"),
          formatPackagePath: /* @__PURE__ */ __name((item) => this.getShortPath(item.path, item.sourceInfo), "formatPackagePath")
        }), "skillList");
        const skillCompactList = /* @__PURE__ */ __name(() => formatCompactList(skills.map((skill) => skill.name)), "skillCompactList");
        addLoadedSection("Skills", skillCompactList, skillList);
      }
      const templates = this.session.promptTemplates;
      if (templates.length > 0) {
        const groups = this.buildScopeGroups(templates.map((template) => ({ path: template.filePath, sourceInfo: template.sourceInfo })));
        const templateByPath = new Map(templates.map((t) => [t.filePath, t]));
        const templateList = /* @__PURE__ */ __name(() => this.formatScopeGroups(groups, {
          formatPath: /* @__PURE__ */ __name((item) => {
            const template = templateByPath.get(item.path);
            return template ? `/${template.name}` : this.formatDisplayPath(item.path);
          }, "formatPath"),
          formatPackagePath: /* @__PURE__ */ __name((item) => {
            const template = templateByPath.get(item.path);
            return template ? `/${template.name}` : this.formatDisplayPath(item.path);
          }, "formatPackagePath")
        }), "templateList");
        const promptCompactList = /* @__PURE__ */ __name(() => formatCompactList(templates.map((template) => `/${template.name}`)), "promptCompactList");
        addLoadedSection("Prompts", promptCompactList, templateList);
      }
      if (extensions.length > 0) {
        const groups = this.buildScopeGroups(extensions);
        const extList = /* @__PURE__ */ __name(() => this.formatScopeGroups(groups, {
          formatPath: /* @__PURE__ */ __name((item) => this.formatExtensionDisplayPath(item.path), "formatPath"),
          formatPackagePath: /* @__PURE__ */ __name((item) => this.formatExtensionDisplayPath(this.getShortPath(item.path, item.sourceInfo)), "formatPackagePath")
        }), "extList");
        const extensionLabels = this.getCompactExtensionLabels(extensions);
        const extensionCompactList = /* @__PURE__ */ __name(() => formatCompactList(extensionLabels), "extensionCompactList");
        addLoadedSection("Extensions", extensionCompactList, extList, "mdHeading");
      }
    }
    if (showDiagnostics) {
      const skillDiagnostics = skillsResult.diagnostics;
      if (skillDiagnostics.length > 0) {
        const warningLines = /* @__PURE__ */ __name(() => this.formatDiagnostics(skillDiagnostics, sourceInfos), "warningLines");
        this.loadedResourcesContainer.addChild(new ThemedText(() => `${theme.fg("warning", "[Skill conflicts]")}
${warningLines()}`, 0, 0));
        this.loadedResourcesContainer.addChild(new Spacer27(1));
      }
      const promptDiagnostics = promptsResult.diagnostics;
      if (promptDiagnostics.length > 0) {
        const warningLines = /* @__PURE__ */ __name(() => this.formatDiagnostics(promptDiagnostics, sourceInfos), "warningLines");
        this.loadedResourcesContainer.addChild(new ThemedText(() => `${theme.fg("warning", "[Prompt conflicts]")}
${warningLines()}`, 0, 0));
        this.loadedResourcesContainer.addChild(new Spacer27(1));
      }
      const extensionDiagnostics = [];
      const extensionsResult = this.session.resourceLoader.getExtensions();
      for (const error of extensionsResult.errors) {
        extensionDiagnostics.push({ type: "error", message: error.error, path: error.path });
      }
      for (const warning of extensionsResult.warnings ?? []) {
        extensionDiagnostics.push({ type: "warning", message: warning.warning, path: warning.path });
      }
      const commandDiagnostics = this.session.extensionRunner.getCommandDiagnostics();
      extensionDiagnostics.push(...commandDiagnostics);
      extensionDiagnostics.push(...this.getBuiltInCommandConflictDiagnostics(this.session.extensionRunner));
      const shortcutDiagnostics = this.session.extensionRunner.getShortcutDiagnostics();
      extensionDiagnostics.push(...shortcutDiagnostics);
      if (extensionDiagnostics.length > 0) {
        const warningLines = /* @__PURE__ */ __name(() => this.formatDiagnostics(extensionDiagnostics, sourceInfos), "warningLines");
        this.loadedResourcesContainer.addChild(new ThemedText(() => `${theme.fg("warning", "[Extension issues]")}
${warningLines()}`, 0, 0));
        this.loadedResourcesContainer.addChild(new Spacer27(1));
      }
      const themeDiagnostics = themesResult.diagnostics;
      if (themeDiagnostics.length > 0) {
        const warningLines = /* @__PURE__ */ __name(() => this.formatDiagnostics(themeDiagnostics, sourceInfos), "warningLines");
        this.loadedResourcesContainer.addChild(new ThemedText(() => `${theme.fg("warning", "[Theme conflicts]")}
${warningLines()}`, 0, 0));
        this.loadedResourcesContainer.addChild(new Spacer27(1));
      }
    }
  }
  /**
   * Initialize the extension system with TUI-based UI context.
   */
  async bindCurrentSessionExtensions() {
    const uiContext = this.createExtensionUIContext();
    await this.session.bindExtensions({
      uiContext,
      mode: "tui",
      abortHandler: /* @__PURE__ */ __name(() => {
        this.restoreQueuedMessagesToEditor({ abort: true });
      }, "abortHandler"),
      commandContextActions: {
        waitForIdle: /* @__PURE__ */ __name(() => this.session.waitForIdle(), "waitForIdle"),
        newSession: /* @__PURE__ */ __name(async (options) => {
          this.clearStatusIndicator();
          try {
            return await this.runtimeHost.newSession(options);
          } catch (error) {
            return this.handleFatalRuntimeError("Failed to create session", error);
          }
        }, "newSession"),
        fork: /* @__PURE__ */ __name(async (entryId, options) => {
          try {
            const result = await this.runtimeHost.fork(entryId, options);
            if (!result.cancelled) {
              this.editor.setText(result.selectedText ?? "");
              this.showStatus("Forked to new session");
            }
            return { cancelled: result.cancelled };
          } catch (error) {
            return this.handleFatalRuntimeError("Failed to fork session", error);
          }
        }, "fork"),
        navigateTree: /* @__PURE__ */ __name(async (targetId, options) => {
          const result = await this.session.navigateTree(targetId, {
            summarize: options?.summarize,
            customInstructions: options?.customInstructions,
            replaceInstructions: options?.replaceInstructions,
            label: options?.label
          });
          if (result.cancelled) {
            return { cancelled: true };
          }
          this.chatContainer.clear();
          this.renderInitialMessages();
          if (result.editorText && !this.editor.getText().trim()) {
            this.editor.setText(result.editorText);
          }
          this.showStatus("Navigated to selected point");
          void this.flushCompactionQueue({ willRetry: false });
          return { cancelled: false };
        }, "navigateTree"),
        switchSession: /* @__PURE__ */ __name(async (sessionPath, options) => {
          return this.handleResumeSession(sessionPath, options);
        }, "switchSession"),
        reload: /* @__PURE__ */ __name(async () => {
          await this.handleReloadCommand();
        }, "reload")
      },
      shutdownHandler: /* @__PURE__ */ __name(() => {
        this.shutdownRequested = true;
        if (this.session.isIdle) {
          void this.shutdown();
        }
      }, "shutdownHandler"),
      onError: /* @__PURE__ */ __name((error) => {
        this.showExtensionError(error.extensionPath, error.error, error.stack);
      }, "onError")
    });
    setRegisteredThemes(this.session.resourceLoader.getThemes().themes);
    this.setupAutocompleteProvider();
    const extensionRunner = this.session.extensionRunner;
    this.setupExtensionShortcuts(extensionRunner);
    this.showLoadedResources({ force: false, showDiagnosticsWhenQuiet: true });
    this.showStartupNoticesIfNeeded();
  }
  applyFullscreenScrollbarSetting() {
    this.transcriptScrollView?.setScrollbar(this.settingsManager.getFullscreenScrollbar());
  }
  /** Lets extension images use the PNG transcoder; tool results register it themselves. */
  ensurePngTranscoder() {
    ensurePngTranscoder(() => {
      this.ui.invalidate();
      this.ui.requestRender();
    });
  }
  applyRuntimeSettings() {
    setCapabilityOverrides2(this.settingsManager.getTerminalCapabilityOverrides());
    this.ensurePngTranscoder();
    configureHttpDispatcher(this.settingsManager.getHttpIdleTimeoutMs());
    this.applyFullscreenScrollbarSetting();
    if (this.renderer instanceof TuiAltScreen3) {
      this.renderer.setCopyOnSelect(this.settingsManager.getFullscreenCopyOnSelect());
      this.renderer.setWheelScrollLines(this.settingsManager.getFullscreenWheelScrollLines());
    }
    this.footer.setSession(this.session);
    this.footer.setAutoCompactEnabled(this.session.autoCompactionEnabled);
    this.footerDataProvider.setCwd(this.sessionManager.getCwd());
    this.hideThinkingBlock = this.settingsManager.getHideThinkingBlock();
    this.outputPad = this.settingsManager.getOutputPad();
    this.ui.setShowHardwareCursor(this.settingsManager.getShowHardwareCursor());
    const clearOnShrink = this.settingsManager.getClearOnShrink();
    this.ui.setClearOnShrink(clearOnShrink);
    if (!clearOnShrink && !this.activeStatusIndicator) {
      this.statusContainer.clear();
    }
    const editorPaddingX = this.settingsManager.getEditorPaddingX();
    const autocompleteMaxVisible = this.settingsManager.getAutocompleteMaxVisible();
    this.defaultEditor.setPaddingX(editorPaddingX);
    this.defaultEditor.setAutocompleteMaxVisible(autocompleteMaxVisible);
    if (this.editor !== this.defaultEditor) {
      this.editor.setPaddingX?.(editorPaddingX);
      this.editor.setAutocompleteMaxVisible?.(autocompleteMaxVisible);
    }
  }
  async rebindCurrentSession(options = {}) {
    const session = this.session;
    this.unsubscribe?.();
    this.unsubscribe = void 0;
    this.programStatus.reset();
    this.applyRuntimeSettings();
    if (options.renderBeforeBind) {
      this.renderCurrentSessionState();
      this.subscribeToAgent();
    }
    await this.bindCurrentSessionExtensions();
    if (this.session !== session) {
      return;
    }
    if (!options.renderBeforeBind) {
      this.subscribeToAgent();
    }
    await this.updateAvailableProviderCount();
    this.updateEditorBorderColor();
    this.updateTerminalTitle();
  }
  async handleFatalRuntimeError(prefix, error) {
    const message = error instanceof Error ? error.message : String(error);
    this.showError(`${prefix}: ${message}`);
    const extensionHint = this.getCrashExtensionHint(error);
    if (extensionHint) {
      this.chatContainer.addChild(new ThemedText(() => theme.fg("warning", extensionHint), this.outputPad, 0));
    }
    if (this.recordCrash("fatal_error", error)) {
      const instructions = this.crashReportInstructions();
      this.chatContainer.addChild(new ThemedText(() => theme.fg("muted", instructions), this.outputPad, 0));
    }
    stopThemeWatcher();
    this.stop("transcript");
    process.exit(1);
  }
  getCrashExtensionHint(error) {
    try {
      return formatCrashExtensionHint(findExtensionStackMatches(error instanceof Error ? error.stack : void 0, this.session.resourceLoader.getExtensions().extensions));
    } catch {
      return void 0;
    }
  }
  /** Persist a crash so the next start can point the user at `/bug`. Returns false when nothing was written. */
  recordCrash(kind, error) {
    try {
      return recordCrash({
        kind,
        error,
        sessionFile: this.session.sessionFile,
        cwd: this.session.sessionManager.getCwd()
      }) !== void 0;
    } catch {
      return false;
    }
  }
  crashReportInstructions() {
    const resume = this.session.sessionFile ? `run \`${APP_NAME} -r\` to resume the session, then` : "start pi and";
    return `To report this crash: ${resume} run /bug. The crash details are attached automatically.`;
  }
  suggestBugReport() {
    if (this.bugReportHintShown)
      return;
    this.bugReportHintShown = true;
    this.chatContainer.addChild(new ThemedText(() => theme.fg("muted", `If this looks like a ${APP_NAME} bug, /bug sends a report to the developers.`), this.outputPad, 0));
    this.ui.requestRender();
  }
  maybeSuggestBugReport(message) {
    if (message.stopReason !== "error" || isRetryableAssistantError(message))
      return;
    if (/\b(?:abort(?:ed)?|cancel(?:l?ed)?)\b/i.test(message.errorMessage ?? ""))
      return;
    if (this.maybeShowInstallChangeWarning())
      return;
    this.suggestBugReport();
  }
  /**
   * After an error, check whether an update replaced or removed this install while the session ran.
   * Code loaded on demand then fails with missing modules until restart (#10439). Returns true when
   * the install changed.
   */
  maybeShowInstallChangeWarning() {
    if (this.installChangeWarningShown)
      return true;
    const change = detectInstallChange();
    if (!change)
      return false;
    this.installChangeWarningShown = true;
    const cause = change.kind === "updated" ? `${APP_NAME} was updated to ${change.version} while this session was running (${VERSION})` : `The ${APP_NAME} installation this session runs from was removed or replaced`;
    const resumeCommand = formatResumeCommand(this.sessionManager);
    const restart = resumeCommand ? `Restart with \`${resumeCommand}\` to continue this session.` : `Restart ${APP_NAME}.`;
    this.showWarning(`${cause}. Features that load code on demand can fail until restart. ${restart}`);
    return true;
  }
  renderCurrentSessionState() {
    this.loadedResourcesContainer.clear();
    this.chatContainer.clear();
    this.pendingMessagesContainer.clear();
    this.compactionQueuedMessages = [];
    this.streamingComponent = void 0;
    this.streamingMessage = void 0;
    this.pendingTools.clear();
    this.renderInitialMessages();
  }
  /**
   * Get a registered tool definition by name (for custom rendering).
   */
  /**
   * Extension-registered definition, falling back to the built-in one. The renderer components take
   * whatever this returns, so they never reach into the tool registry themselves.
   */
  getRegisteredToolDefinition(toolName) {
    return this.session.extensionRunner.resolveToolRenderers(toolName, () => withBuiltInRenderers(toolName, this.session.getToolDefinition(toolName)));
  }
  getMarkdownTransformers() {
    return [this.mermaidMarkdownTransformer, ...this.session.extensionRunner.getMarkdownTransformers()];
  }
  /**
   * Set up keyboard shortcuts registered by extensions.
   */
  setupExtensionShortcuts(extensionRunner) {
    const shortcuts = extensionRunner.getShortcuts(this.keybindings.getEffectiveConfig());
    if (shortcuts.size === 0)
      return;
    const createContext = /* @__PURE__ */ __name(() => ({
      ui: this.createExtensionUIContext(),
      mode: "tui",
      hasUI: true,
      cwd: this.sessionManager.getCwd(),
      sessionManager: this.sessionManager,
      modelRegistry: extensionRunner.getModelRegistry(),
      model: this.session.model,
      scopedModels: this.session.scopedModels,
      thinkingLevel: this.session.thinkingLevel,
      isIdle: /* @__PURE__ */ __name(() => this.session.isIdle, "isIdle"),
      isProjectTrusted: /* @__PURE__ */ __name(() => this.settingsManager.isProjectTrusted(), "isProjectTrusted"),
      signal: this.session.agent.signal,
      abort: /* @__PURE__ */ __name(() => {
        this.restoreQueuedMessagesToEditor({ abort: true });
      }, "abort"),
      hasPendingMessages: /* @__PURE__ */ __name(() => this.session.pendingMessageCount > 0, "hasPendingMessages"),
      shutdown: /* @__PURE__ */ __name(() => {
        this.shutdownRequested = true;
      }, "shutdown"),
      getContextUsage: /* @__PURE__ */ __name(() => this.session.getContextUsage(), "getContextUsage"),
      compact: /* @__PURE__ */ __name((options) => {
        void (async () => {
          try {
            const result = await this.session.compact(options?.customInstructions);
            options?.onComplete?.(result);
          } catch (error) {
            const err = error instanceof Error ? error : new Error(String(error));
            options?.onError?.(err);
          }
        })();
      }, "compact"),
      getSystemPrompt: /* @__PURE__ */ __name(() => this.session.systemPrompt, "getSystemPrompt")
    }), "createContext");
    this.defaultEditor.onExtensionShortcut = (data) => {
      for (const [shortcutStr, shortcut] of shortcuts) {
        if (matchesKey2(data, shortcutStr)) {
          Promise.resolve(shortcut.handler(createContext())).catch((err) => {
            this.showError(`Shortcut handler error: ${err instanceof Error ? err.message : String(err)}`);
          });
          return true;
        }
      }
      return false;
    };
  }
  /**
   * Set extension status text in the footer.
   */
  setExtensionStatus(key, text) {
    this.footerDataProvider.setExtensionStatus(key, text);
    this.ui.requestRender();
  }
  setEditorWorkingStatusIndicator(indicator) {
    this.defaultEditor.setWorkingStatusIndicator(void 0);
    if (!isWorkingStatusEditor(this.editor))
      return false;
    this.editor.setWorkingStatusIndicator(indicator);
    return true;
  }
  showStatusIndicator(indicator) {
    this.activeStatusIndicator?.dispose();
    this.activeStatusIndicator = indicator;
    this.activeWorkingIndicatorEmbedded = false;
    this.statusContainer.clear();
    this.setEditorWorkingStatusIndicator(void 0);
    if (this.setEditorWorkingStatusIndicator(indicator)) {
      this.activeWorkingIndicatorEmbedded = true;
      return;
    }
    this.statusContainer.addChild(indicator);
  }
  clearStatusIndicator(kind) {
    if (kind && this.activeStatusIndicator?.kind !== kind) {
      return;
    }
    const clearedIndicator = this.activeStatusIndicator;
    const clearedIndicatorWasEmbedded = this.activeWorkingIndicatorEmbedded;
    clearedIndicator?.dispose();
    this.activeStatusIndicator = void 0;
    this.activeWorkingIndicatorEmbedded = false;
    this.statusContainer.clear();
    this.setEditorWorkingStatusIndicator(void 0);
    if (clearedIndicator && !clearedIndicatorWasEmbedded && this.options.tuiMode === "regular" && this.ui.getClearOnShrink()) {
      this.statusContainer.addChild(this.idleStatus);
    }
  }
  showWorkingStatusIndicator() {
    const colorFn = isWorkingStatusEditor(this.editor) ? (text) => (this.editor.borderColor ?? theme.getThinkingBorderColor(this.session.thinkingLevel || "off"))(text) : void 0;
    this.showStatusIndicator(new WorkingStatusIndicator(this.ui, this.workingMessage ?? this.defaultWorkingMessage, this.workingIndicatorOptions, colorFn));
  }
  setWorkingVisible(visible) {
    this.workingVisible = visible;
    if (!visible) {
      this.clearStatusIndicator("working");
      this.ui.requestRender();
      return;
    }
    if (this.session.isStreaming && this.activeStatusIndicator?.kind !== "working") {
      this.showWorkingStatusIndicator();
    }
    this.ui.requestRender();
  }
  setWorkingIndicator(options) {
    this.workingIndicatorOptions = options;
    if (this.activeStatusIndicator?.kind === "working") {
      this.activeStatusIndicator.setIndicator(options);
    }
    this.ui.requestRender();
  }
  setHiddenThinkingLabel(label) {
    this.hiddenThinkingLabel = label ?? this.defaultHiddenThinkingLabel;
    for (const child of this.chatContainer.children) {
      if (child instanceof AssistantMessageComponent) {
        child.setHiddenThinkingLabel(this.hiddenThinkingLabel);
      }
    }
    if (this.streamingComponent) {
      this.streamingComponent.setHiddenThinkingLabel(this.hiddenThinkingLabel);
    }
    this.ui.requestRender();
  }
  /**
   * Set an extension widget (string array or custom component).
   */
  setExtensionWidget(key, content, options) {
    const placement = options?.placement ?? "aboveEditor";
    const removeExisting = /* @__PURE__ */ __name((map) => {
      const existing = map.get(key);
      if (existing?.dispose)
        existing.dispose();
      map.delete(key);
    }, "removeExisting");
    removeExisting(this.extensionWidgetsAbove);
    removeExisting(this.extensionWidgetsBelow);
    if (content === void 0) {
      this.renderWidgets();
      return;
    }
    let component;
    if (Array.isArray(content)) {
      const container = new Container30();
      for (const line of content.slice(0, _InteractiveMode.MAX_WIDGET_LINES)) {
        container.addChild(new Text30(line, 1, 0));
      }
      if (content.length > _InteractiveMode.MAX_WIDGET_LINES) {
        container.addChild(new ThemedText(() => theme.fg("muted", "... (widget truncated)"), 1, 0));
      }
      component = container;
    } else {
      component = content(this.ui, theme);
    }
    const targetMap = placement === "belowEditor" ? this.extensionWidgetsBelow : this.extensionWidgetsAbove;
    targetMap.set(key, component);
    this.renderWidgets();
  }
  clearExtensionWidgets() {
    for (const widget of this.extensionWidgetsAbove.values()) {
      widget.dispose?.();
    }
    for (const widget of this.extensionWidgetsBelow.values()) {
      widget.dispose?.();
    }
    this.extensionWidgetsAbove.clear();
    this.extensionWidgetsBelow.clear();
    this.renderWidgets();
  }
  resetExtensionUI() {
    if (this.extensionSelector) {
      this.hideExtensionSelector();
    }
    if (this.extensionInput) {
      this.hideExtensionInput();
    }
    if (this.extensionEditor) {
      this.hideExtensionEditor();
    }
    this.ui.hideOverlay();
    this.clearExtensionTerminalInputListeners();
    this.setExtensionFooter(void 0);
    this.setExtensionHeader(void 0);
    this.clearExtensionWidgets();
    this.footerDataProvider.clearExtensionStatuses();
    this.footer.invalidate();
    this.autocompleteProviderWrappers = [];
    this.setCustomEditorComponent(void 0);
    this.setupAutocompleteProvider();
    this.defaultEditor.onExtensionShortcut = void 0;
    this.updateTerminalTitle();
    this.workingMessage = void 0;
    this.workingVisible = true;
    this.setWorkingIndicator();
    if (this.activeStatusIndicator?.kind === "working") {
      this.activeStatusIndicator.setMessage(`${this.defaultWorkingMessage} (${keyText("app.interrupt")} to interrupt)`);
    }
    this.setHiddenThinkingLabel();
  }
  // Maximum total widget lines to prevent viewport overflow
  static MAX_WIDGET_LINES = 10;
  /**
   * Render all extension widgets to the widget container.
   */
  renderWidgets() {
    if (!this.widgetContainerAbove || !this.widgetContainerBelow)
      return;
    this.renderWidgetContainer(this.widgetContainerAbove, this.extensionWidgetsAbove, true, true);
    this.renderWidgetContainer(this.widgetContainerBelow, this.extensionWidgetsBelow, false, false);
    this.ui.requestRender();
  }
  renderWidgetContainer(container, widgets, spacerWhenEmpty, leadingSpacer) {
    container.clear();
    if (widgets.size === 0) {
      if (spacerWhenEmpty) {
        container.addChild(new Spacer27(1));
      }
      return;
    }
    if (leadingSpacer) {
      container.addChild(new Spacer27(1));
    }
    for (const component of widgets.values()) {
      container.addChild(component);
    }
  }
  /**
   * Set a custom footer component, or restore the built-in footer.
   */
  setExtensionFooter(factory) {
    if (this.customFooter?.dispose) {
      this.customFooter.dispose();
    }
    this.footerContainer.clear();
    if (factory) {
      this.customFooter = factory(this.ui, theme, this.footerDataProvider);
      this.footerContainer.addChild(this.customFooter);
    } else {
      this.customFooter = void 0;
      this.footerContainer.addChild(this.footer);
    }
    this.ui.requestRender();
  }
  /**
   * Set a custom header component, or restore the built-in header.
   */
  setExtensionHeader(factory) {
    if (!this.builtInHeader) {
      return;
    }
    if (this.customHeader?.dispose) {
      this.customHeader.dispose();
    }
    const currentHeader = this.customHeader || this.builtInHeader;
    const index = this.headerContainer.children.indexOf(currentHeader);
    if (factory) {
      this.customHeader = factory(this.ui, theme);
      if (isExpandable(this.customHeader)) {
        this.customHeader.setExpanded(this.toolOutputExpanded);
      }
      if (index !== -1) {
        this.headerContainer.children[index] = this.customHeader;
      } else {
        this.headerContainer.children.unshift(this.customHeader);
      }
    } else {
      this.customHeader = void 0;
      if (isExpandable(this.builtInHeader)) {
        this.builtInHeader.setExpanded(this.toolOutputExpanded);
      }
      if (index !== -1) {
        this.headerContainer.children[index] = this.builtInHeader;
      }
    }
    this.ui.requestRender();
  }
  addExtensionTerminalInputListener(handler) {
    const subscription = { handler, unsubscribe: this.ui.addInputListener(handler) };
    this.extensionTerminalInputSubscriptions.add(subscription);
    return () => {
      subscription.unsubscribe();
      this.extensionTerminalInputSubscriptions.delete(subscription);
    };
  }
  rebindExtensionTerminalInputListeners() {
    for (const subscription of this.extensionTerminalInputSubscriptions) {
      subscription.unsubscribe();
      subscription.unsubscribe = this.ui.addInputListener(subscription.handler);
    }
  }
  clearExtensionTerminalInputListeners() {
    for (const subscription of this.extensionTerminalInputSubscriptions)
      subscription.unsubscribe();
    this.extensionTerminalInputSubscriptions.clear();
  }
  /**
   * Create the ExtensionUIContext for extensions.
   */
  createProjectTrustContext(cwd) {
    const ui = this.createExtensionUIContext();
    return {
      cwd,
      mode: "tui",
      hasUI: true,
      ui: {
        select: ui.select,
        confirm: ui.confirm,
        input: ui.input,
        notify: ui.notify
      }
    };
  }
  createExtensionUIContext() {
    return {
      select: /* @__PURE__ */ __name((title, options, opts) => this.showExtensionSelector(title, options, opts), "select"),
      confirm: /* @__PURE__ */ __name((title, message, opts) => this.showExtensionConfirm(title, message, opts), "confirm"),
      input: /* @__PURE__ */ __name((title, placeholder, opts) => this.showExtensionInput(title, placeholder, opts), "input"),
      notify: /* @__PURE__ */ __name((message, type) => this.showExtensionNotify(message, type), "notify"),
      onTerminalInput: /* @__PURE__ */ __name((handler) => this.addExtensionTerminalInputListener(handler), "onTerminalInput"),
      setStatus: /* @__PURE__ */ __name((key, text) => this.setExtensionStatus(key, text), "setStatus"),
      setWorkingMessage: /* @__PURE__ */ __name((message) => {
        this.workingMessage = message;
        if (this.activeStatusIndicator?.kind === "working") {
          this.activeStatusIndicator.setMessage(message ?? this.defaultWorkingMessage);
        }
      }, "setWorkingMessage"),
      setWorkingVisible: /* @__PURE__ */ __name((visible) => this.setWorkingVisible(visible), "setWorkingVisible"),
      setWorkingIndicator: /* @__PURE__ */ __name((options) => this.setWorkingIndicator(options), "setWorkingIndicator"),
      setHiddenThinkingLabel: /* @__PURE__ */ __name((label) => this.setHiddenThinkingLabel(label), "setHiddenThinkingLabel"),
      setWidget: /* @__PURE__ */ __name((key, content, options) => this.setExtensionWidget(key, content, options), "setWidget"),
      setFooter: /* @__PURE__ */ __name((factory) => this.setExtensionFooter(factory), "setFooter"),
      setHeader: /* @__PURE__ */ __name((factory) => this.setExtensionHeader(factory), "setHeader"),
      setTitle: /* @__PURE__ */ __name((title) => this.ui.terminal.setTitle(title), "setTitle"),
      custom: /* @__PURE__ */ __name((factory, options) => this.showExtensionCustom(factory, options), "custom"),
      pasteToEditor: /* @__PURE__ */ __name((text) => this.editor.handleInput(`\x1B[200~${text}\x1B[201~`), "pasteToEditor"),
      setEditorText: /* @__PURE__ */ __name((text) => this.editor.setText(text), "setEditorText"),
      getEditorText: /* @__PURE__ */ __name(() => this.editor.getExpandedText?.() ?? this.editor.getText(), "getEditorText"),
      editor: /* @__PURE__ */ __name((title, prefill) => this.showExtensionEditor(title, prefill), "editor"),
      addAutocompleteProvider: /* @__PURE__ */ __name((factory) => {
        this.autocompleteProviderWrappers.push(factory);
        this.setupAutocompleteProvider();
      }, "addAutocompleteProvider"),
      setEditorComponent: /* @__PURE__ */ __name((factory) => this.setCustomEditorComponent(factory), "setEditorComponent"),
      getEditorComponent: /* @__PURE__ */ __name(() => this.editorComponentFactory, "getEditorComponent"),
      get theme() {
        return theme;
      },
      getAllThemes: /* @__PURE__ */ __name(() => getAvailableThemesWithPaths(), "getAllThemes"),
      getTheme: /* @__PURE__ */ __name((name) => getThemeByName(name), "getTheme"),
      setTheme: /* @__PURE__ */ __name((themeOrName) => {
        if (themeOrName instanceof Theme) {
          return this.themeController.setThemeInstance(themeOrName);
        }
        const result = this.themeController.setThemeName(themeOrName);
        if (result.success) {
          if (this.settingsManager.getTheme() !== themeOrName) {
            this.settingsManager.setTheme(themeOrName);
          }
        }
        return result;
      }, "setTheme"),
      getToolsExpanded: /* @__PURE__ */ __name(() => this.toolOutputExpanded, "getToolsExpanded"),
      setToolsExpanded: /* @__PURE__ */ __name((expanded) => this.setToolsExpanded(expanded), "setToolsExpanded")
    };
  }
  /**
   * Show a selector for extensions.
   */
  showExtensionSelector(title, options, opts, blocked = { kind: "question", message: title }) {
    return new Promise((resolve7) => {
      if (opts?.signal?.aborted) {
        resolve7(void 0);
        return;
      }
      const onAbort = /* @__PURE__ */ __name(() => {
        this.hideExtensionSelector();
        resolve7(void 0);
      }, "onAbort");
      opts?.signal?.addEventListener("abort", onAbort, { once: true });
      this.extensionSelector = new ExtensionSelectorComponent(title, options, (option) => {
        opts?.signal?.removeEventListener("abort", onAbort);
        this.hideExtensionSelector();
        resolve7(option);
      }, () => {
        opts?.signal?.removeEventListener("abort", onAbort);
        this.hideExtensionSelector();
        resolve7(void 0);
      }, { tui: this.ui, timeout: opts?.timeout, onToggleToolsExpanded: /* @__PURE__ */ __name(() => this.toggleToolOutputExpansion(), "onToggleToolsExpanded") });
      this.disposeActiveSelector();
      this.editorContainer.clear();
      this.editorContainer.addChild(this.extensionSelector);
      this.ui.setFocus(this.extensionSelector);
      this.programStatus.setBlocked("extension-dialog", blocked);
      this.ui.requestRender();
    });
  }
  /**
   * Hide the extension selector.
   */
  hideExtensionSelector() {
    this.extensionSelector?.dispose();
    this.editorContainer.clear();
    this.editorContainer.addChild(this.editor);
    this.extensionSelector = void 0;
    this.programStatus.setBlocked("extension-dialog", void 0);
    this.ui.setFocus(this.editor);
    this.ui.requestRender();
  }
  /**
   * Show a confirmation dialog for extensions.
   */
  async showExtensionConfirm(title, message, opts) {
    const result = await this.showExtensionSelector(`${title}
${message}`, ["Yes", "No"], opts, {
      kind: "permission",
      message: title
    });
    return result === "Yes";
  }
  async promptForMissingSessionCwd(error) {
    const confirmed = await this.showExtensionConfirm("Session cwd not found", formatMissingSessionCwdPrompt(error.issue));
    return confirmed ? error.issue.fallbackCwd : void 0;
  }
  /**
   * Show a text input for extensions.
   */
  showExtensionInput(title, placeholder, opts) {
    return new Promise((resolve7) => {
      if (opts?.signal?.aborted) {
        resolve7(void 0);
        return;
      }
      const onAbort = /* @__PURE__ */ __name(() => {
        this.hideExtensionInput();
        resolve7(void 0);
      }, "onAbort");
      opts?.signal?.addEventListener("abort", onAbort, { once: true });
      this.extensionInput = new ExtensionInputComponent(title, placeholder, (value) => {
        opts?.signal?.removeEventListener("abort", onAbort);
        this.hideExtensionInput();
        resolve7(value);
      }, () => {
        opts?.signal?.removeEventListener("abort", onAbort);
        this.hideExtensionInput();
        resolve7(void 0);
      }, { tui: this.ui, timeout: opts?.timeout });
      this.disposeActiveSelector();
      this.editorContainer.clear();
      this.editorContainer.addChild(this.extensionInput);
      this.ui.setFocus(this.extensionInput);
      this.programStatus.setBlocked("extension-dialog", { kind: "question", message: title });
      this.ui.requestRender();
    });
  }
  /**
   * Hide the extension input.
   */
  hideExtensionInput() {
    this.extensionInput?.dispose();
    this.editorContainer.clear();
    this.editorContainer.addChild(this.editor);
    this.extensionInput = void 0;
    this.programStatus.setBlocked("extension-dialog", void 0);
    this.ui.setFocus(this.editor);
    this.ui.requestRender();
  }
  /**
   * Show a multi-line editor for extensions (with Ctrl+G support).
   */
  showExtensionEditor(title, prefill) {
    return new Promise((resolve7) => {
      this.extensionEditor = new ExtensionEditorComponent(this.ui, this.keybindings, title, prefill, (value) => {
        this.hideExtensionEditor();
        resolve7(value);
      }, () => {
        this.hideExtensionEditor();
        resolve7(void 0);
      }, void 0, this.settingsManager.getExternalEditorCommand());
      this.disposeActiveSelector();
      this.editorContainer.clear();
      this.editorContainer.addChild(this.extensionEditor);
      this.ui.setFocus(this.extensionEditor);
      this.programStatus.setBlocked("extension-dialog", { kind: "question", message: title });
      this.ui.requestRender();
    });
  }
  /**
   * Hide the extension editor.
   */
  hideExtensionEditor() {
    this.editorContainer.clear();
    this.editorContainer.addChild(this.editor);
    this.extensionEditor = void 0;
    this.programStatus.setBlocked("extension-dialog", void 0);
    this.ui.setFocus(this.editor);
    this.ui.requestRender();
  }
  /**
   * Set a custom editor component from an extension.
   * Pass undefined to restore the default editor.
   */
  setCustomEditorComponent(factory) {
    this.editorComponentFactory = factory;
    const currentText = this.editor.getText();
    this.disposeActiveSelector();
    this.editorContainer.clear();
    if (factory) {
      const newEditor = factory(this.ui, getEditorTheme(), this.keybindings);
      newEditor.onSubmit = this.defaultEditor.onSubmit;
      newEditor.onChange = this.defaultEditor.onChange;
      newEditor.setText(currentText);
      if (newEditor.borderColor !== void 0) {
        newEditor.borderColor = this.defaultEditor.borderColor;
      }
      if (newEditor.setPaddingX !== void 0) {
        newEditor.setPaddingX(this.defaultEditor.getPaddingX());
      }
      if (newEditor.setAutocompleteMaxVisible !== void 0) {
        newEditor.setAutocompleteMaxVisible(this.defaultEditor.getAutocompleteMaxVisible());
      }
      if (newEditor.setAutocompleteProvider && this.autocompleteProvider) {
        newEditor.setAutocompleteProvider(this.autocompleteProvider);
      }
      const customEditor = newEditor;
      if ("actionHandlers" in customEditor && customEditor.actionHandlers instanceof Map) {
        if (!customEditor.onEscape) {
          customEditor.onEscape = () => this.defaultEditor.onEscape?.();
        }
        if (!customEditor.onCtrlD) {
          customEditor.onCtrlD = () => this.defaultEditor.onCtrlD?.();
        }
        if (!customEditor.onPasteImage) {
          customEditor.onPasteImage = () => this.defaultEditor.onPasteImage?.();
        }
        if (!customEditor.onExtensionShortcut) {
          customEditor.onExtensionShortcut = (data) => this.defaultEditor.onExtensionShortcut?.(data);
        }
        for (const [action, handler] of this.defaultEditor.actionHandlers) {
          customEditor.actionHandlers.set(action, handler);
        }
      }
      this.editor = newEditor;
    } else {
      this.defaultEditor.setText(currentText);
      this.editor = this.defaultEditor;
    }
    this.editorContainer.addChild(this.editor);
    if (this.activeStatusIndicator) {
      this.statusContainer.clear();
      this.activeWorkingIndicatorEmbedded = this.setEditorWorkingStatusIndicator(this.activeStatusIndicator);
      if (!this.activeWorkingIndicatorEmbedded) {
        this.statusContainer.addChild(this.activeStatusIndicator);
      }
    }
    this.ui.setFocus(this.editor);
    this.ui.requestRender();
  }
  /**
   * Show a notification for extensions.
   */
  showExtensionNotify(message, type) {
    if (type === "error") {
      this.showError(message);
    } else if (type === "warning") {
      this.showWarning(message);
    } else {
      this.showStatus(message);
    }
  }
  /** Show a custom component with keyboard focus. Overlay mode renders on top of existing content. */
  async showExtensionCustom(factory, options) {
    const savedText = this.editor.getText();
    const isOverlay = options?.overlay ?? false;
    const restoreEditor3 = /* @__PURE__ */ __name(() => {
      this.editorContainer.clear();
      this.editorContainer.addChild(this.editor);
      this.editor.setText(savedText);
      this.ui.setFocus(this.editor);
      this.ui.requestRender();
    }, "restoreEditor");
    return new Promise((resolve7, reject) => {
      let component;
      let closed = false;
      const close = /* @__PURE__ */ __name((result) => {
        if (closed)
          return;
        closed = true;
        if (isOverlay)
          this.ui.hideOverlay();
        else
          restoreEditor3();
        resolve7(result);
        try {
          component?.dispose?.();
        } catch {
        }
      }, "close");
      Promise.resolve(factory(this.ui, theme, this.keybindings, close)).then((c2) => {
        if (closed)
          return;
        component = c2;
        if (isOverlay) {
          const resolveOptions = /* @__PURE__ */ __name(() => {
            if (options?.overlayOptions) {
              const opts = typeof options.overlayOptions === "function" ? options.overlayOptions() : options.overlayOptions;
              return opts;
            }
            const w = component.width;
            return w ? { width: w } : void 0;
          }, "resolveOptions");
          const handle = this.ui.showOverlay(component, resolveOptions());
          options?.onHandle?.(handle);
        } else {
          this.disposeActiveSelector();
          this.editorContainer.clear();
          this.editorContainer.addChild(component);
          this.ui.setFocus(component);
          this.ui.requestRender();
        }
      }).catch((err) => {
        if (closed)
          return;
        if (!isOverlay)
          restoreEditor3();
        reject(err);
      });
    });
  }
  /**
   * Show an extension error in the UI.
   */
  showExtensionError(extensionPath, error, stack) {
    const errorMsg = `Extension "${extensionPath}" error: ${error}`;
    const errorText = new ThemedText(() => theme.fg("error", errorMsg), 1, 0);
    this.chatContainer.addChild(errorText);
    if (stack) {
      const stackLines = stack.split("\n").slice(1);
      if (stackLines.length > 0) {
        const renderStack = /* @__PURE__ */ __name(() => stackLines.map((line) => theme.fg("dim", `  ${line.trim()}`)).join("\n"), "renderStack");
        this.chatContainer.addChild(new ThemedText(renderStack, 1, 0));
      }
    }
    this.ui.requestRender();
  }
  // =========================================================================
  // Key Handlers
  // =========================================================================
  setupKeyHandlers() {
    this.defaultEditor.onEscape = () => {
      if (this.session.isStreaming) {
        this.restoreQueuedMessagesToEditor({ abort: true });
      } else if (this.session.isBashRunning) {
        this.session.abortBash();
      } else if (this.isBashMode) {
        this.editor.setText("");
        this.isBashMode = false;
        this.updateEditorBorderColor();
      } else if (!this.editor.getText().trim()) {
        const action = this.settingsManager.getDoubleEscapeAction();
        if (action !== "none") {
          const now = Date.now();
          if (now - this.lastEscapeTime < 500) {
            if (action === "tree") {
              this.showTreeSelector();
            } else {
              this.showUserMessageSelector();
            }
            this.lastEscapeTime = 0;
          } else {
            this.lastEscapeTime = now;
          }
        }
      }
    };
    this.defaultEditor.onAction("app.clear", () => this.handleCtrlC());
    this.defaultEditor.onCtrlD = () => this.handleCtrlD();
    this.defaultEditor.onAction("app.suspend", () => this.handleCtrlZ());
    this.defaultEditor.onAction("app.thinking.cycle", () => this.cycleThinkingLevel());
    this.defaultEditor.onAction("app.model.cycleForward", () => this.cycleModel("forward"));
    this.defaultEditor.onAction("app.model.cycleBackward", () => this.cycleModel("backward"));
    this.ui.onDebug = () => this.handleDebugCommand();
    this.defaultEditor.onAction("app.model.select", () => this.showModelSelector());
    this.defaultEditor.onAction("app.tools.expand", () => this.toggleToolOutputExpansion());
    this.defaultEditor.onAction("app.thinking.toggle", () => this.toggleThinkingBlockVisibility());
    this.defaultEditor.onAction("app.editor.external", () => void this.handleOpenExternalEditor());
    this.defaultEditor.onAction("app.message.copy", () => void this.handleCopyCommand({ flashConfirmation: true, preferSelection: true }));
    this.defaultEditor.onAction("app.message.followUp", () => this.handleFollowUp());
    this.defaultEditor.onAction("app.message.dequeue", () => this.handleDequeue());
    this.defaultEditor.onAction("app.session.new", () => this.handleClearCommand());
    this.defaultEditor.onAction("app.session.tree", () => this.showTreeSelector());
    this.defaultEditor.onAction("app.session.fork", () => this.showUserMessageSelector());
    this.defaultEditor.onAction("app.session.resume", () => this.showSessionSelector());
    this.defaultEditor.onChange = (text) => {
      const wasBashMode = this.isBashMode;
      this.isBashMode = text.trimStart().startsWith("!");
      if (wasBashMode !== this.isBashMode) {
        this.updateEditorBorderColor();
      }
    };
    this.defaultEditor.onPasteImage = () => {
      void this.handleClipboardPaste();
    };
  }
  async handleRightClickPaste() {
    const target = this.renderer.getFocusedComponent();
    const handleInput = target?.handleInput;
    if (!target || !handleInput)
      return;
    try {
      const text = await readClipboardText();
      if (!text || this.renderer.getFocusedComponent() !== target)
        return;
      handleInput.call(target, `\x1B[200~${text}\x1B[201~`);
      this.ui.requestRender();
    } catch {
    }
  }
  async handleClipboardPaste() {
    try {
      const filePaths = await readClipboardFilePaths();
      if (filePaths) {
        if (filePaths.some((filePath) => new RegExp("\\p{Cc}", "u").test(filePath))) {
          throw new Error("Clipboard file path contains control characters");
        }
        const paths = this.isBashMode ? filePaths.map(quoteIfNeeded).join(" ") : filePaths.join("\n");
        const cursor = this.editor.getCursor?.();
        const currentLine = cursor ? this.editor.getText().split("\n")[cursor.line] ?? "" : "";
        const characterBeforeCursor = cursor && cursor.col > 0 ? currentLine[cursor.col - 1] : "";
        const characterAfterCursor = cursor ? currentLine[cursor.col] : "";
        const leadingSpace = characterBeforeCursor && !/\s/.test(characterBeforeCursor) ? " " : "";
        const trailingSpace = characterAfterCursor && !/\s/.test(characterAfterCursor) ? " " : "";
        this.editor.insertTextAtCursor?.(`${leadingSpace}${paths}${trailingSpace}`);
        this.ui.requestRender();
        return;
      }
      const image = await readClipboardImage();
      if (image) {
        const tmpDir = os3.tmpdir();
        const ext = extensionForImageMimeType(image.mimeType) ?? "png";
        const fileName = `pi-clipboard-${crypto2.randomUUID()}.${ext}`;
        const filePath = path4.join(tmpDir, fileName);
        fs3.writeFileSync(filePath, Buffer.from(image.bytes));
        this.editor.insertTextAtCursor?.(filePath);
        this.ui.requestRender();
        return;
      }
      const text = await readClipboardText();
      if (text) {
        this.editor.insertTextAtCursor?.(text);
        this.ui.requestRender();
      }
    } catch (error) {
      this.showError(`Failed to paste from clipboard: ${error instanceof Error ? error.message : String(error)}`);
    }
  }
  handleStartupSubmit(text) {
    this.editor.setText(text);
    this.showStatus("Startup is still in progress");
  }
  setupEditorSubmitHandler() {
    this.defaultEditor.onSubmit = async (text) => {
      text = text.trim();
      if (!text)
        return;
      if (text === "/settings") {
        this.showSettingsSelector();
        this.editor.setText("");
        return;
      }
      if (text === "/scoped-models") {
        this.editor.setText("");
        await this.showModelsSelector();
        return;
      }
      if (text === "/model" || text.startsWith("/model ")) {
        const searchTerm = text.startsWith("/model ") ? text.slice(7).trim() : void 0;
        this.editor.setText("");
        await this.handleModelCommand(searchTerm);
        return;
      }
      if (text === "/thinking" || text.startsWith("/thinking ")) {
        const searchTerm = text.startsWith("/thinking ") ? text.slice(10).trim() : void 0;
        this.editor.setText("");
        this.handleThinkingCommand(searchTerm);
        return;
      }
      if (text === "/export" || text.startsWith("/export ")) {
        await this.handleExportCommand(text);
        this.editor.setText("");
        return;
      }
      if (text === "/import" || text.startsWith("/import ")) {
        await this.handleImportCommand(text);
        this.editor.setText("");
        return;
      }
      if (text === "/share") {
        await this.handleShareCommand();
        this.editor.setText("");
        return;
      }
      if (text === "/bug" || text.startsWith("/bug ")) {
        const hint = text.slice("/bug".length).trim();
        this.editor.setText("");
        await this.handleBugCommand(hint ? hint : void 0);
        return;
      }
      if (text === "/copy") {
        await this.handleCopyCommand();
        this.editor.setText("");
        return;
      }
      if (text === "/name" || text.startsWith("/name ")) {
        this.handleNameCommand(text);
        this.editor.setText("");
        return;
      }
      if (text === "/session") {
        this.handleSessionCommand();
        this.editor.setText("");
        return;
      }
      if (text === "/changelog") {
        this.handleChangelogCommand();
        this.editor.setText("");
        return;
      }
      if (text === "/hotkeys") {
        this.handleHotkeysCommand();
        this.editor.setText("");
        return;
      }
      if (text === "/fork") {
        this.showUserMessageSelector();
        this.editor.setText("");
        return;
      }
      if (text === "/clone") {
        this.editor.setText("");
        await this.handleCloneCommand();
        return;
      }
      if (text === "/tree") {
        this.showTreeSelector();
        this.editor.setText("");
        return;
      }
      if (text === "/trust") {
        this.showTrustSelector();
        this.editor.setText("");
        return;
      }
      if (text === "/login" || text.startsWith("/login ")) {
        const providerRef = text.startsWith("/login ") ? text.slice(7).trim() : void 0;
        this.editor.setText("");
        await this.handleLoginCommand(providerRef);
        return;
      }
      if (text === "/logout") {
        this.showOAuthSelector("logout");
        this.editor.setText("");
        return;
      }
      if (text === "/new") {
        this.editor.setText("");
        await this.handleClearCommand();
        return;
      }
      if (text === "/compact" || text.startsWith("/compact ")) {
        const customInstructions = text.startsWith("/compact ") ? text.slice(9).trim() : void 0;
        this.editor.setText("");
        await this.handleCompactCommand(customInstructions);
        return;
      }
      if (text === "/reload") {
        this.editor.setText("");
        await this.handleReloadCommand();
        return;
      }
      if (text === "/debug") {
        this.handleDebugCommand();
        this.editor.setText("");
        return;
      }
      if (text === "/arminsayshi") {
        this.handleArminSaysHi();
        this.editor.setText("");
        return;
      }
      if (text === "/dementedelves") {
        this.handleDementedDelves();
        this.editor.setText("");
        return;
      }
      if (text === "/resume") {
        this.showSessionSelector();
        this.editor.setText("");
        return;
      }
      if (text === "/quit") {
        this.editor.setText("");
        await this.shutdown();
        return;
      }
      if (text.startsWith("!")) {
        const isExcluded = text.startsWith("!!");
        const command = isExcluded ? text.slice(2).trim() : text.slice(1).trim();
        if (command) {
          if (this.session.isBashRunning) {
            this.showWarning("A bash command is already running. Press Esc to cancel it first.");
            this.editor.setText(text);
            return;
          }
          this.editor.addToHistory?.(text);
          await this.handleBashCommand(command, isExcluded);
          this.isBashMode = false;
          this.updateEditorBorderColor();
          return;
        }
      }
      if (this.session.isCompacting) {
        if (this.isExtensionCommand(text)) {
          this.editor.addToHistory?.(text);
          this.editor.setText("");
          await this.session.prompt(text);
        } else {
          this.queueCompactionMessage(text, "steer");
        }
        return;
      }
      if (this.session.isStreaming) {
        this.editor.addToHistory?.(text);
        this.editor.setText("");
        await this.session.prompt(text, { streamingBehavior: "steer" });
        this.updatePendingMessagesDisplay();
        this.ui.requestRender();
        return;
      }
      this.flushPendingBashComponents();
      if (this.onInputCallback) {
        this.onInputCallback(text);
      } else {
        this.pendingUserInputs.push(text);
      }
      this.editor.addToHistory?.(text);
    };
  }
  subscribeToAgent() {
    this.unsubscribe = this.session.subscribe(async (event) => {
      await this.handleEvent(event);
    });
  }
  async handleEvent(event) {
    if (!this.isInitialized) {
      await this.init();
    }
    this.footer.invalidate();
    this.programStatus.handleEvent(event);
    switch (event.type) {
      case "agent_start":
        this.pendingTools.clear();
        if (this.retryEscapeHandler) {
          this.defaultEditor.onEscape = this.retryEscapeHandler;
          this.retryEscapeHandler = void 0;
        }
        break;
      case "turn_start":
        if (this.settingsManager.getShowTerminalProgress()) {
          this.ui.terminal.setProgress(true);
        }
        if (this.workingVisible) {
          if (this.activeStatusIndicator?.kind !== "working") {
            this.showWorkingStatusIndicator();
          }
        } else {
          this.clearStatusIndicator();
        }
        this.ui.requestRender();
        break;
      case "queue_update":
        this.updatePendingMessagesDisplay();
        this.ui.requestRender();
        break;
      case "entry_appended":
        if (this.entriesRenderedByBoundaryCompaction.delete(event.entry.id))
          break;
        if (event.entry.type === "custom") {
          this.addCustomEntryToChat(event.entry);
          this.ui.requestRender();
        } else if (event.entry.type === "usage" && event.entry.kind === "cache_warm") {
          this.addCacheWarmingUsage(event.entry);
          this.ui.requestRender();
        } else if (event.entry.type === "custom_message" && event.entry.display) {
          this.addMessageToChat(createCustomMessage(event.entry.customType, event.entry.content, event.entry.display, event.entry.details, event.entry.timestamp));
          this.ui.requestRender();
        } else if (event.entry.type === "compaction") {
          const entries = this.sessionManager.buildContextEntries();
          if (entries[0]?.id !== event.entry.id)
            break;
          this.chatContainer.clear();
          const branch = this.sessionManager.getBranch();
          const compactionIndex = branch.findIndex((entry) => entry.id === event.entry.id);
          const entriesAfterCompaction = new Set(branch.slice(compactionIndex + 1).map((entry) => entry.id));
          const retainedEntries = entries.slice(1);
          this.renderSessionEntries(retainedEntries.filter((entry) => !entriesAfterCompaction.has(entry.id)));
          this.addMessageToChat(createCompactionSummaryMessage(event.entry.summary, event.entry.tokensBefore, event.entry.timestamp));
          if (event.entry.usage) {
            this.addCompactionCostNotice({
              type: "compaction_cost",
              kind: "compaction",
              usage: event.entry.usage
            });
          }
          this.renderSessionEntries(retainedEntries.filter((entry) => entriesAfterCompaction.has(entry.id)));
          for (const entryId of entriesAfterCompaction)
            this.entriesRenderedByBoundaryCompaction.add(entryId);
          this.footer.invalidate();
          this.ui.requestRender();
        }
        break;
      case "session_info_changed":
        this.updateTerminalTitle();
        this.footer.invalidate();
        this.ui.requestRender();
        break;
      case "thinking_level_changed":
        this.footer.invalidate();
        this.updateEditorBorderColor();
        break;
      case "message_start":
        if (event.message.role === "custom") {
          this.addMessageToChat(event.message);
          this.ui.requestRender();
        } else if (event.message.role === "user") {
          this.addMessageToChat(event.message);
          this.updatePendingMessagesDisplay();
          this.ui.requestRender();
        } else if (event.message.role === "assistant") {
          this.streamingComponent = new AssistantMessageComponent(void 0, this.hideThinkingBlock, this.getMarkdownThemeWithSettings(), this.hiddenThinkingLabel, this.outputPad, this.getMarkdownTransformers());
          this.streamingMessage = event.message;
          this.chatContainer.addChild(this.streamingComponent);
          this.streamingComponent.updateContent(this.streamingMessage, true);
          this.ui.requestRender();
        }
        break;
      case "message_update":
        if (this.streamingComponent && event.message.role === "assistant") {
          this.streamingMessage = event.message;
          this.streamingComponent.updateContent(this.streamingMessage, true);
          for (const content of this.streamingMessage.content) {
            if (content.type === "toolCall") {
              if (!this.pendingTools.has(content.id)) {
                const component = new ToolExecutionComponent(content.name, content.id, content.arguments, {
                  showImages: this.settingsManager.getShowImages(),
                  imageWidthCells: this.settingsManager.getImageWidthCells(),
                  outputPad: this.outputPad
                }, this.getRegisteredToolDefinition(content.name), this.ui, this.sessionManager.getCwd());
                component.setExpanded(this.toolOutputExpanded);
                this.chatContainer.addChild(component);
                this.pendingTools.set(content.id, component);
              } else {
                const component = this.pendingTools.get(content.id);
                if (component) {
                  component.updateArgs(content.arguments);
                }
              }
            }
          }
          this.ui.requestRender();
        }
        break;
      case "message_end":
        if (event.message.role === "user")
          break;
        if (this.streamingComponent && event.message.role === "assistant") {
          this.streamingMessage = event.message;
          let errorMessage4;
          if (this.streamingMessage.stopReason === "aborted") {
            const retryAttempt = this.session.retryAttempt;
            errorMessage4 = retryAttempt > 0 ? `Aborted after ${retryAttempt} retry attempt${retryAttempt > 1 ? "s" : ""}` : "Operation aborted";
            this.streamingMessage.errorMessage = errorMessage4;
          }
          this.streamingComponent.updateContent(this.streamingMessage, false);
          if (this.streamingMessage.stopReason === "aborted" || this.streamingMessage.stopReason === "error") {
            if (!errorMessage4) {
              errorMessage4 = this.streamingMessage.errorMessage || "Error";
            }
            for (const [, component] of this.pendingTools.entries()) {
              component.updateResult({
                content: [{ type: "text", text: errorMessage4 }],
                isError: true
              });
            }
            this.pendingTools.clear();
            this.maybeSuggestBugReport(this.streamingMessage);
          } else {
            for (const [, component] of this.pendingTools.entries()) {
              component.setArgsComplete();
            }
            this.maybeShowThinkingDropNotice(this.streamingMessage);
            this.maybeShowCacheMissNotice(this.streamingMessage);
          }
          this.streamingComponent = void 0;
          this.streamingMessage = void 0;
          this.footer.invalidate();
        }
        this.ui.requestRender();
        break;
      case "bash_execution_update":
        break;
      case "tool_execution_start": {
        if (event.parentToolCallId)
          break;
        let component = this.pendingTools.get(event.toolCallId);
        if (!component) {
          component = new ToolExecutionComponent(event.toolName, event.toolCallId, event.args, {
            showImages: this.settingsManager.getShowImages(),
            imageWidthCells: this.settingsManager.getImageWidthCells(),
            outputPad: this.outputPad
          }, this.getRegisteredToolDefinition(event.toolName), this.ui, this.sessionManager.getCwd());
          component.setExpanded(this.toolOutputExpanded);
          this.chatContainer.addChild(component);
          this.pendingTools.set(event.toolCallId, component);
        }
        component.markExecutionStarted();
        this.ui.requestRender();
        break;
      }
      case "tool_execution_update": {
        const component = this.pendingTools.get(event.toolCallId);
        if (component) {
          component.updateResult({ ...event.partialResult, isError: false }, true);
          this.ui.requestRender();
        }
        break;
      }
      case "tool_execution_end": {
        if (event.isError)
          this.maybeShowInstallChangeWarning();
        const component = this.pendingTools.get(event.toolCallId);
        if (component) {
          component.updateResult({ ...event.result, isError: event.isError, durationMs: event.durationMs });
          this.pendingTools.delete(event.toolCallId);
          this.ui.requestRender();
        }
        break;
      }
      case "agent_end":
        if (this.settingsManager.getShowTerminalProgress()) {
          this.ui.terminal.setProgress(false);
        }
        this.clearStatusIndicator("working");
        if (this.streamingComponent) {
          this.chatContainer.removeChild(this.streamingComponent);
          this.streamingComponent = void 0;
          this.streamingMessage = void 0;
        }
        this.pendingTools.clear();
        this.ui.requestRender();
        break;
      case "agent_settled":
        await this.checkShutdownRequested();
        break;
      case "compaction_start": {
        if (this.settingsManager.getShowTerminalProgress()) {
          this.ui.terminal.setProgress(true);
        }
        this.autoCompactionEscapeHandler = this.defaultEditor.onEscape;
        this.defaultEditor.onEscape = () => {
          this.session.abortCompaction();
        };
        this.showStatusIndicator(new CompactionStatusIndicator(this.ui, event.reason));
        this.ui.requestRender();
        break;
      }
      case "compaction_end": {
        if (this.settingsManager.getShowTerminalProgress()) {
          this.ui.terminal.setProgress(false);
        }
        if (this.autoCompactionEscapeHandler) {
          this.defaultEditor.onEscape = this.autoCompactionEscapeHandler;
          this.autoCompactionEscapeHandler = void 0;
        }
        this.clearStatusIndicator("compaction");
        if (event.aborted) {
          if (event.reason === "manual") {
            this.showError("Compaction cancelled");
          } else {
            this.showStatus("Auto-compaction cancelled");
          }
        } else if (event.result) {
          const entries = this.sessionManager.buildContextEntries();
          if (entries[0]?.type !== "compaction") {
            throw new Error("Completed compaction is missing from the session context");
          }
          this.chatContainer.clear();
          this.renderSessionEntries(entries.slice(1));
          this.addMessageToChat(createCompactionSummaryMessage(event.result.summary, event.result.tokensBefore, (/* @__PURE__ */ new Date()).toISOString()));
          if (event.result.usage) {
            this.addCompactionCostNotice({
              type: "compaction_cost",
              kind: "compaction",
              usage: event.result.usage
            });
          }
          this.footer.invalidate();
        } else if (event.errorMessage) {
          if (event.reason === "manual") {
            this.showError(event.errorMessage);
          } else {
            this.chatContainer.addChild(new Spacer27(1));
            const errorMessage4 = event.errorMessage;
            this.chatContainer.addChild(new ThemedText(() => theme.fg("error", errorMessage4), 1, 0));
          }
        }
        void this.flushCompactionQueue({ willRetry: event.willRetry });
        this.ui.requestRender();
        break;
      }
      case "auto_retry_start": {
        this.retryEscapeHandler = this.defaultEditor.onEscape;
        this.defaultEditor.onEscape = () => {
          this.session.abortRetry();
        };
        this.showStatusIndicator(new RetryStatusIndicator(this.ui, event.attempt, event.maxAttempts, event.delayMs));
        this.ui.requestRender();
        break;
      }
      case "auto_retry_end": {
        if (this.retryEscapeHandler) {
          this.defaultEditor.onEscape = this.retryEscapeHandler;
          this.retryEscapeHandler = void 0;
        }
        this.clearStatusIndicator("retry");
        if (!event.success) {
          this.showError(`Retry failed after ${event.attempt} attempts: ${event.finalError || "Unknown error"}`);
        }
        this.ui.requestRender();
        break;
      }
      case "summarization_retry_scheduled": {
        this.showError(event.errorMessage);
        this.showStatusIndicator(new RetryStatusIndicator(this.ui, event.attempt, event.maxAttempts, event.delayMs));
        this.ui.requestRender();
        break;
      }
      case "summarization_retry_attempt_start": {
        this.clearStatusIndicator("retry");
        if (event.source === "branchSummary") {
          this.showStatusIndicator(new BranchSummaryStatusIndicator(this.ui));
        } else {
          this.showStatusIndicator(new CompactionStatusIndicator(this.ui, event.reason));
        }
        this.ui.requestRender();
        break;
      }
      case "summarization_retry_finished": {
        this.clearStatusIndicator("retry");
        this.ui.requestRender();
        break;
      }
    }
  }
  /** Extract text content from a user message */
  getUserMessageText(message) {
    if (message.role !== "user")
      return "";
    const textBlocks = typeof message.content === "string" ? [{ type: "text", text: message.content }] : message.content.filter((c2) => c2.type === "text");
    return textBlocks.map((c2) => c2.text).join("");
  }
  /** Show a managed-tool status update in the chat. */
  showManagedToolStatus(status) {
    if (!this.managedToolStatusStarted) {
      this.chatContainer.addChild(new Spacer27(1));
      this.managedToolStatusStarted = true;
    }
    const message = status.type === "warning" ? `Warning: ${status.message}` : status.message;
    const color = status.type === "warning" ? "warning" : "dim";
    this.chatContainer.addChild(new ThemedText(() => theme.fg(color, message), 1, 0));
    this.lastStatusSpacer = void 0;
    this.lastStatusText = void 0;
    this.ui.requestRender();
  }
  /**
   * Show a status message in the chat.
   *
   * If multiple status messages are emitted back-to-back (without anything else being added to the chat),
   * we update the previous status line instead of appending new ones to avoid log spam.
   */
  showStatus(message) {
    const children = this.chatContainer.children;
    const last = children.length > 0 ? children[children.length - 1] : void 0;
    const secondLast = children.length > 1 ? children[children.length - 2] : void 0;
    if (last && secondLast && last === this.lastStatusText && secondLast === this.lastStatusSpacer) {
      this.lastStatusMessage = message;
      this.lastStatusText.invalidate();
      this.ui.requestRender();
      return;
    }
    const spacer = new Spacer27(1);
    this.lastStatusMessage = message;
    const text = new ThemedText(() => theme.fg("dim", this.lastStatusMessage), 1, 0);
    this.chatContainer.addChild(spacer);
    this.chatContainer.addChild(text);
    this.lastStatusSpacer = spacer;
    this.lastStatusText = text;
    this.ui.requestRender();
  }
  addCustomEntryToChat(entry) {
    const renderer = this.session.extensionRunner.getEntryRenderer(entry.customType);
    if (!renderer) {
      return;
    }
    const component = new CustomEntryComponent(entry, renderer, this.outputPad);
    component.setExpanded(this.toolOutputExpanded);
    if (!component.hasContent()) {
      return;
    }
    if (this.streamingComponent) {
      const streamingIndex = this.chatContainer.children.indexOf(this.streamingComponent);
      if (streamingIndex >= 0) {
        this.chatContainer.children.splice(streamingIndex, 0, component);
        return;
      }
    }
    this.chatContainer.addChild(component);
  }
  addMessageToChat(message, options) {
    switch (message.role) {
      case "bashExecution": {
        const component = new BashExecutionComponent(message.command, this.ui, message.excludeFromContext, this.outputPad);
        if (message.output) {
          component.appendOutput(message.output);
        }
        component.setComplete(message.exitCode, message.cancelled, message.truncated ? { truncated: true } : void 0, message.fullOutputPath);
        this.chatContainer.addChild(component);
        break;
      }
      case "custom": {
        if (message.display) {
          const renderer = this.session.extensionRunner.getMessageRenderer(message.customType);
          const component = new CustomMessageComponent(message, renderer, this.getMarkdownThemeWithSettings(), this.outputPad);
          component.setExpanded(this.toolOutputExpanded);
          this.chatContainer.addChild(component);
        }
        break;
      }
      case "compactionSummary": {
        this.chatContainer.addChild(new Spacer27(1));
        const component = new CompactionSummaryMessageComponent(message, this.getMarkdownThemeWithSettings(), this.outputPad);
        component.setExpanded(this.toolOutputExpanded);
        this.chatContainer.addChild(component);
        break;
      }
      case "branchSummary": {
        this.chatContainer.addChild(new Spacer27(1));
        const component = new BranchSummaryMessageComponent(message, this.getMarkdownThemeWithSettings(), this.outputPad);
        component.setExpanded(this.toolOutputExpanded);
        this.chatContainer.addChild(component);
        break;
      }
      case "system":
        break;
      case "user": {
        const textContent = this.getUserMessageText(message);
        if (textContent) {
          if (this.chatContainer.children.length > 0) {
            this.chatContainer.addChild(new Spacer27(1));
          }
          const skillBlock = parseSkillBlock(textContent);
          if (skillBlock) {
            const component = new SkillInvocationMessageComponent(skillBlock, this.getMarkdownThemeWithSettings(), this.outputPad);
            component.setExpanded(this.toolOutputExpanded);
            this.chatContainer.addChild(component);
            if (skillBlock.userMessage) {
              this.chatContainer.addChild(new Spacer27(1));
              const userComponent = new UserMessageComponent(skillBlock.userMessage, this.getMarkdownThemeWithSettings(), this.outputPad, this.getMarkdownTransformers());
              this.chatContainer.addChild(userComponent);
            }
          } else {
            const userComponent = new UserMessageComponent(textContent, this.getMarkdownThemeWithSettings(), this.outputPad, this.getMarkdownTransformers());
            this.chatContainer.addChild(userComponent);
          }
          if (options?.populateHistory) {
            this.editor.addToHistory?.(textContent);
          }
        }
        break;
      }
      case "assistant": {
        const assistantComponent = new AssistantMessageComponent(message, this.hideThinkingBlock, this.getMarkdownThemeWithSettings(), this.hiddenThinkingLabel, this.outputPad, this.getMarkdownTransformers());
        this.chatContainer.addChild(assistantComponent);
        break;
      }
      case "toolResult": {
        break;
      }
      default: {
        const _exhaustive = message;
      }
    }
  }
  renderSessionItems(items, options = {}) {
    this.pendingTools.clear();
    const renderedPendingTools = /* @__PURE__ */ new Map();
    const cacheMisses = this.settingsManager.getShowCacheMissNotices() ? collectCacheMisses(this.sessionManager.getEntries(), this.session.modelRuntime) : /* @__PURE__ */ new Map();
    if (options.updateFooter) {
      this.footer.invalidate();
      this.updateEditorBorderColor();
    }
    for (const item of items) {
      if (isCustomSessionEntry(item)) {
        this.addCustomEntryToChat(item);
        continue;
      }
      if (isUsageSessionEntry(item)) {
        this.addCacheWarmingUsage(item);
        continue;
      }
      if (isCompactionCostNotice(item)) {
        this.addCompactionCostNotice(item);
        continue;
      }
      const message = item;
      if (message.role === "assistant") {
        this.addMessageToChat(message);
        for (const content of message.content) {
          if (content.type === "toolCall") {
            const component = new ToolExecutionComponent(content.name, content.id, content.arguments, {
              showImages: this.settingsManager.getShowImages(),
              imageWidthCells: this.settingsManager.getImageWidthCells(),
              outputPad: this.outputPad
            }, this.getRegisteredToolDefinition(content.name), this.ui, this.sessionManager.getCwd());
            component.setExpanded(this.toolOutputExpanded);
            this.chatContainer.addChild(component);
            if (message.stopReason === "aborted" || message.stopReason === "error") {
              let errorMessage4;
              if (message.stopReason === "aborted") {
                const retryAttempt = this.session.retryAttempt;
                errorMessage4 = retryAttempt > 0 ? `Aborted after ${retryAttempt} retry attempt${retryAttempt > 1 ? "s" : ""}` : "Operation aborted";
              } else {
                errorMessage4 = message.errorMessage || "Error";
              }
              component.updateResult({ content: [{ type: "text", text: errorMessage4 }], isError: true });
            } else {
              renderedPendingTools.set(content.id, component);
            }
          }
        }
        if (message.stopReason !== "aborted" && message.stopReason !== "error") {
          const miss = cacheMisses.get(message);
          if (miss)
            this.addCacheMissNotice(miss);
        }
      } else if (message.role === "toolResult") {
        const component = renderedPendingTools.get(message.toolCallId);
        if (component) {
          component.updateResult(message);
          renderedPendingTools.delete(message.toolCallId);
        }
      } else {
        this.addMessageToChat(message, options);
      }
    }
    for (const [toolCallId, component] of renderedPendingTools) {
      this.pendingTools.set(toolCallId, component);
    }
    this.ui.requestRender();
  }
  /**
   * Render session entries to chat. Used for initial load and rebuild after compaction.
   * @param entries Compaction-aware session entries to render
   * @param options.updateFooter Update footer state
   * @param options.populateHistory Add user messages to editor history
   */
  renderSessionEntries(entries, options = {}) {
    if (this.renderer instanceof TuiAltScreen3)
      this.renderer.resetTextSelection();
    const items = entries.flatMap((entry) => {
      if (entry.type === "custom" || entry.type === "usage" && entry.kind === "cache_warm") {
        return [entry];
      }
      const messages = sessionEntryToContextMessages(entry);
      if ((entry.type === "compaction" || entry.type === "branch_summary") && entry.usage && messages.length > 0) {
        return [...messages, { type: "compaction_cost", kind: entry.type, usage: entry.usage }];
      }
      return messages;
    });
    this.renderSessionItems(items, options);
  }
  addCacheWarmingUsage(entry) {
    if (!this.settingsManager.getShowCacheMissNotices())
      return;
    this.chatContainer.addChild(new Spacer27(1));
    const usage = formatCacheWarmingUsage(entry);
    this.chatContainer.addChild(new ThemedText(() => theme.fg("dim", usage), 1, 0));
  }
  /**
   * Render billing usage for a compaction or branch summary. The notice is derived
   * from persisted summary usage and is not stored as a separate session entry.
   */
  addCompactionCostNotice(notice) {
    if (!this.settingsManager.getShowCacheMissNotices())
      return;
    const { usage } = notice;
    const tokens = usage.input + usage.output + usage.cacheRead + usage.cacheWrite;
    const cost = usage.cost.total >= 0.01 ? ` (~$${usage.cost.total.toFixed(2)})` : "";
    const label = notice.kind === "compaction" ? "Compaction" : "Branch summary";
    this.chatContainer.addChild(new Spacer27(1));
    this.chatContainer.addChild(new ThemedText(() => theme.fg("warning", `${label}: ${formatTokens(tokens)} tokens billed${cost}`), 1, 0));
  }
  static countDroppedThinkingBlocks(message) {
    let count = 0;
    for (const diagnostic of message.diagnostics ?? []) {
      if (diagnostic.type !== "anthropic_input_transformations")
        continue;
      const transformations = diagnostic.details?.transformations;
      if (!Array.isArray(transformations))
        continue;
      count += transformations.filter((transformation) => typeof transformation === "object" && transformation !== null && transformation.type === "thinking_dropped").length;
    }
    return count;
  }
  maybeShowThinkingDropNotice(message) {
    if (!this.settingsManager.getShowCacheMissNotices())
      return;
    const droppedCount = _InteractiveMode.countDroppedThinkingBlocks(message);
    if (droppedCount === 0)
      return;
    let previousDroppedCount = 0;
    const branch = this.sessionManager.getBranch();
    for (let i = branch.length - 1; i >= 0; i--) {
      const entry = branch[i];
      if (entry.type === "message" && entry.message.role === "assistant") {
        previousDroppedCount = _InteractiveMode.countDroppedThinkingBlocks(entry.message);
        break;
      }
    }
    if (droppedCount <= previousDroppedCount)
      return;
    const noun = droppedCount === 1 ? "thinking block" : "thinking blocks";
    this.chatContainer.addChild(new Spacer27(1));
    this.chatContainer.addChild(new ThemedText(() => theme.fg("warning", `Anthropic dropped ${droppedCount} ${noun} (details in session)`), 1, 0));
  }
  /**
   * Show a transcript notice when a completed assistant message paid for a
   * significant cache miss. Only states observable facts: the miss itself,
   * a model switch, or an idle gap past the cache TTL.
   */
  maybeShowCacheMissNotice(message) {
    if (!this.settingsManager.getShowCacheMissNotices())
      return;
    const miss = detectCacheMiss(this.sessionManager.getEntries(), message, this.session.modelRuntime);
    if (miss)
      this.addCacheMissNotice(miss);
  }
  addCacheMissNotice(miss) {
    if (miss.missedTokens < 2e4 && miss.missedCost < 0.1)
      return;
    const cost = miss.missedCost >= 0.01 ? ` (~$${miss.missedCost.toFixed(2)})` : "";
    const reBilled = `${formatTokens(miss.missedTokens)} tokens re-billed${cost}`;
    let label = "Cache miss";
    if (miss.modelChanged) {
      label = "Cache miss after model switch";
    } else if (miss.idleMs >= CACHE_TTL_MS) {
      label = `Cache miss after ${Math.round(miss.idleMs / 6e4)}m idle`;
    }
    this.chatContainer.addChild(new Spacer27(1));
    this.chatContainer.addChild(new ThemedText(() => theme.fg("warning", `${label}: ${reBilled}`), 1, 0));
  }
  renderInitialMessages() {
    const entries = this.sessionManager.buildContextEntries();
    this.renderSessionEntries(entries, {
      updateFooter: true,
      populateHistory: true
    });
    this.renderProjectTrustWarningIfNeeded();
    const allEntries = this.sessionManager.getEntries();
    const compactionCount = allEntries.filter((e) => e.type === "compaction").length;
    if (compactionCount > 0) {
      const times = compactionCount === 1 ? "1 time" : `${compactionCount} times`;
      this.showStatus(`Session compacted ${times}`);
    }
  }
  renderProjectTrustWarningIfNeeded() {
    if (this.settingsManager.isProjectTrusted() || !hasTrustRequiringProjectResources(this.sessionManager.getCwd())) {
      return;
    }
    if (this.chatContainer.children.length > 0) {
      this.chatContainer.addChild(new Spacer27(1));
    }
    this.chatContainer.addChild(new ThemedText(() => theme.fg("warning", `This project is not trusted. Project ${CONFIG_DIR_NAME} resources and packages are ignored. Use /trust to save a trust decision, then restart pi.`), 1, 0));
  }
  async getUserInput() {
    const queuedInput = this.pendingUserInputs.shift();
    if (queuedInput !== void 0) {
      return queuedInput;
    }
    return new Promise((resolve7) => {
      this.onInputCallback = (text) => {
        this.onInputCallback = void 0;
        resolve7(text);
      };
    });
  }
  rebuildChatFromMessages() {
    this.chatContainer.clear();
    this.renderSessionEntries(this.sessionManager.buildContextEntries());
  }
  // =========================================================================
  // Key handlers
  // =========================================================================
  handleCtrlC() {
    const now = Date.now();
    if (now - this.lastSigintTime < 500) {
      void this.shutdown();
    } else {
      this.clearEditor();
      this.lastSigintTime = now;
    }
  }
  handleCtrlD() {
    void this.shutdown();
  }
  /**
   * Gracefully shutdown the agent.
   * Stops the TUI before emitting shutdown events so extension UI cleanup cannot
   * repaint the final frame while the process is exiting.
   */
  isShuttingDown = false;
  async shutdown(options) {
    if (this.isShuttingDown)
      return;
    this.isShuttingDown = true;
    if (options?.fromSignal) {
      await this.runtimeHost.dispose();
      this.themeController.disableAutoSync();
      await this.ui.terminal.drainInput(1e3);
      this.stop();
      process.exit(0);
    }
    this.themeController.disableAutoSync();
    await this.ui.terminal.drainInput(1e3);
    this.stop();
    await this.runtimeHost.dispose();
    const resumeCommand = formatResumeCommand(this.sessionManager);
    if (resumeCommand) {
      process.stdout.write(`${chalk5.dim("To resume this session:")} ${resumeCommand}
`);
    }
    process.exit(0);
  }
  emergencyTerminalExit() {
    this.isShuttingDown = true;
    this.unregisterSignalHandlers();
    killTrackedDetachedChildren();
    process.exit(129);
  }
  /**
   * Last-resort handler for uncaught exceptions. The TUI puts stdin into raw
   * mode and hides the cursor; without this handler, an uncaught throw from
   * anywhere (e.g. an extension's async `ChildProcess.on("exit")` callback)
   * tears down the process while leaving the terminal in raw mode with no
   * cursor, requiring `stty sane && reset` to recover.
   *
   * Unlike emergencyTerminalExit, the terminal is still alive here, so we
   * call ui.stop() to restore cooked mode, the cursor, and disable bracketed
   * paste / Kitty / modifyOtherKeys sequences.
   */
  uncaughtCrash(error) {
    if (isDeadTerminalError(error)) {
      this.emergencyTerminalExit();
    }
    if (this.isShuttingDown) {
      process.exit(1);
    }
    this.isShuttingDown = true;
    try {
      this.unregisterSignalHandlers();
    } catch {
    }
    try {
      killTrackedDetachedChildren();
    } catch {
    }
    try {
      this.ui.stop();
    } catch {
    }
    console.error(`${APP_NAME} exiting due to uncaughtException:`);
    console.error(error);
    const extensionHint = this.getCrashExtensionHint(error);
    if (extensionHint)
      console.error(`
${extensionHint}`);
    if (this.recordCrash("uncaught_exception", error)) {
      console.error(`
${this.crashReportInstructions()}`);
    }
    process.exit(1);
  }
  /**
   * Check if shutdown was requested and perform shutdown if so.
   */
  async checkShutdownRequested() {
    if (!this.shutdownRequested)
      return;
    await this.shutdown();
  }
  registerSignalHandlers() {
    this.unregisterSignalHandlers();
    const signals = ["SIGTERM"];
    if (process.platform !== "win32") {
      signals.push("SIGHUP");
    }
    for (const signal of signals) {
      const handler = /* @__PURE__ */ __name(() => {
        killTrackedDetachedChildren();
        void this.shutdown({ fromSignal: true });
      }, "handler");
      process.prependListener(signal, handler);
      this.signalCleanupHandlers.push(() => process.off(signal, handler));
    }
    const terminalErrorHandler = /* @__PURE__ */ __name((error) => {
      if (isDeadTerminalError(error)) {
        this.emergencyTerminalExit();
      }
      throw error;
    }, "terminalErrorHandler");
    for (const stream2 of [process.stdin, process.stdout, process.stderr]) {
      stream2.on("error", terminalErrorHandler);
      this.signalCleanupHandlers.push(() => stream2.off("error", terminalErrorHandler));
    }
    const uncaughtExceptionHandler = /* @__PURE__ */ __name((error) => this.uncaughtCrash(error), "uncaughtExceptionHandler");
    process.prependListener("uncaughtException", uncaughtExceptionHandler);
    this.signalCleanupHandlers.push(() => process.off("uncaughtException", uncaughtExceptionHandler));
  }
  unregisterSignalHandlers() {
    for (const cleanup of this.signalCleanupHandlers) {
      cleanup();
    }
    this.signalCleanupHandlers = [];
  }
  handleCtrlZ() {
    if (process.platform === "win32") {
      this.showStatus("Suspend to background is not supported on Windows");
      return;
    }
    const suspendKeepAlive = setInterval(() => {
    }, 2 ** 30);
    const ignoreSigint = /* @__PURE__ */ __name(() => {
    }, "ignoreSigint");
    process.on("SIGINT", ignoreSigint);
    process.once("SIGCONT", () => {
      clearInterval(suspendKeepAlive);
      process.removeListener("SIGINT", ignoreSigint);
      this.ui.start();
      this.ui.requestRender(true);
    });
    try {
      this.ui.stop();
      process.kill(0, "SIGTSTP");
    } catch (error) {
      clearInterval(suspendKeepAlive);
      process.removeListener("SIGINT", ignoreSigint);
      throw error;
    }
  }
  async handleFollowUp() {
    const text = (this.editor.getExpandedText?.() ?? this.editor.getText()).trim();
    if (!text)
      return;
    if (this.session.isCompacting) {
      if (this.isExtensionCommand(text)) {
        this.editor.addToHistory?.(text);
        this.editor.setText("");
        await this.session.prompt(text);
      } else {
        this.queueCompactionMessage(text, "followUp");
      }
      return;
    }
    if (this.session.isStreaming) {
      this.editor.addToHistory?.(text);
      this.editor.setText("");
      await this.session.prompt(text, { streamingBehavior: "followUp" });
      this.updatePendingMessagesDisplay();
      this.ui.requestRender();
    } else if (this.editor.onSubmit) {
      this.editor.setText("");
      this.editor.onSubmit(text);
    }
  }
  handleDequeue() {
    const restored = this.restoreQueuedMessagesToEditor();
    if (restored === 0) {
      this.showStatus("No queued messages to restore");
    } else {
      this.showStatus(`Restored ${restored} queued message${restored > 1 ? "s" : ""} to editor`);
    }
  }
  updateEditorBorderColor() {
    if (this.isBashMode) {
      this.editor.borderColor = theme.getBashModeBorderColor();
    } else {
      const level = this.session.thinkingLevel || "off";
      this.editor.borderColor = theme.getThinkingBorderColor(level);
    }
    this.activeStatusIndicator?.invalidate();
    this.ui.requestRender();
  }
  cycleThinkingLevel() {
    const newLevel = this.session.cycleThinkingLevel();
    if (newLevel === void 0) {
      this.showStatus("Current model does not support thinking");
    } else {
      this.footer.invalidate();
      this.updateEditorBorderColor();
      this.showStatus(`Thinking level: ${newLevel}`);
    }
  }
  async cycleModel(direction) {
    try {
      const result = await this.session.cycleModel(direction);
      if (result === void 0) {
        const msg = this.session.scopedModels.length > 0 ? "Only one model in scope" : "Only one model available";
        this.showStatus(msg);
      } else {
        this.footer.invalidate();
        this.updateEditorBorderColor();
        const thinkingStr = result.model.reasoning && result.thinkingLevel !== "off" ? ` (thinking: ${result.thinkingLevel})` : "";
        this.showStatus(`Switched to ${result.model.name || result.model.id}${thinkingStr}`);
        void this.maybeWarnAboutAnthropicSubscriptionAuth(result.model);
      }
    } catch (error) {
      this.showError(error instanceof Error ? error.message : String(error));
    }
  }
  toggleToolOutputExpansion() {
    this.setToolsExpanded(!this.toolOutputExpanded);
  }
  setToolsExpanded(expanded) {
    if (expanded === this.toolOutputExpanded)
      return;
    this.toolOutputExpanded = expanded;
    const activeHeader = this.customHeader ?? this.builtInHeader;
    if (isExpandable(activeHeader)) {
      activeHeader.setExpanded(expanded);
    }
    for (const container of [this.loadedResourcesContainer, this.chatContainer]) {
      for (const child of container.children) {
        if (isExpandable(child)) {
          child.setExpanded(expanded);
        }
      }
    }
    this.showStatus(`Tool output: ${expanded ? "expanded" : "collapsed"}`);
  }
  /** Update rendered assistant messages without rebuilding live tool components. */
  updateThinkingBlockVisibility() {
    for (const child of this.chatContainer.children) {
      if (child instanceof AssistantMessageComponent) {
        child.setHideThinkingBlock(this.hideThinkingBlock);
      }
    }
    this.ui.requestRender();
  }
  toggleThinkingBlockVisibility() {
    this.hideThinkingBlock = !this.hideThinkingBlock;
    this.settingsManager.setHideThinkingBlock(this.hideThinkingBlock);
    this.updateThinkingBlockVisibility();
    this.showStatus(`Thinking blocks: ${this.hideThinkingBlock ? "hidden" : "visible"}`);
  }
  async handleOpenExternalEditor() {
    const editorCmd = this.settingsManager.getExternalEditorCommand();
    const content = this.editor.getExpandedText?.() ?? this.editor.getText();
    this.ui.stop();
    try {
      const result = await editInExternalEditor({
        command: editorCmd,
        content
      });
      if (result.status === "complete") {
        this.editor.setText(result.content);
      }
    } finally {
      this.ui.start();
      this.ui.requestRender(true);
    }
  }
  // =========================================================================
  // UI helpers
  // =========================================================================
  clearEditor() {
    this.editor.setText("");
    this.ui.requestRender();
  }
  showError(errorMessage4) {
    this.chatContainer.addChild(new Spacer27(1));
    this.chatContainer.addChild(new ThemedText(() => theme.fg("error", `Error: ${errorMessage4}`), this.outputPad, 0));
    this.ui.requestRender();
  }
  showWarning(warningMessage) {
    this.chatContainer.addChild(new Spacer27(1));
    this.chatContainer.addChild(new ThemedText(() => theme.fg("warning", `Warning: ${warningMessage}`), 1, 0));
    this.ui.requestRender();
  }
  showNewVersionNotification(release) {
    const updateInstruction = /* @__PURE__ */ __name(() => theme.fg("muted", `New version ${release.version} is available. Run `) + theme.fg("accent", `${APP_NAME} update`), "updateInstruction");
    const changelogUrl = "https://pi.dev/changelog";
    const changelogLine = /* @__PURE__ */ __name(() => {
      const changelogLink = getCapabilities3().hyperlinks ? hyperlink3(theme.fg("accent", changelogUrl), changelogUrl) : theme.fg("accent", changelogUrl);
      return theme.fg("muted", "Changelog: ") + changelogLink;
    }, "changelogLine");
    const note = release.note?.trim();
    this.chatContainer.addChild(new Spacer27(1));
    this.chatContainer.addChild(new DynamicBorder((text) => theme.fg("warning", text)));
    this.chatContainer.addChild(new ThemedText(() => `${theme.bold(theme.fg("warning", "Update Available"))}
${updateInstruction()}`, 1, 0));
    if (note) {
      this.chatContainer.addChild(new Spacer27(1));
      this.chatContainer.addChild(new Markdown7(note, 1, 0, this.getMarkdownThemeWithSettings(), {
        color: /* @__PURE__ */ __name((text) => theme.fg("muted", text), "color")
      }));
      this.chatContainer.addChild(new Spacer27(1));
    }
    this.chatContainer.addChild(new ThemedText(changelogLine, 1, 0));
    this.chatContainer.addChild(new DynamicBorder((text) => theme.fg("warning", text)));
    this.ui.requestRender();
  }
  showPackageUpdateNotification(packages) {
    const updateInstruction = /* @__PURE__ */ __name(() => theme.fg("muted", "Package updates are available. Run ") + theme.fg("accent", `${APP_NAME} update --extensions`), "updateInstruction");
    const packageLines = packages.map((pkg) => `- ${pkg}`).join("\n");
    this.chatContainer.addChild(new Spacer27(1));
    this.chatContainer.addChild(new DynamicBorder((text) => theme.fg("warning", text)));
    this.chatContainer.addChild(new ThemedText(() => `${theme.bold(theme.fg("warning", "Package Updates Available"))}
${updateInstruction()}
${theme.fg("muted", "Packages:")}
${packageLines}`, 1, 0));
    this.chatContainer.addChild(new DynamicBorder((text) => theme.fg("warning", text)));
    this.ui.requestRender();
  }
  /**
   * Get all queued messages (read-only).
   * Combines session queue and compaction queue.
   */
  getAllQueuedMessages() {
    return {
      steering: [
        ...this.session.getSteeringMessages(),
        ...this.compactionQueuedMessages.filter((msg) => msg.mode === "steer").map((msg) => msg.text)
      ],
      followUp: [
        ...this.session.getFollowUpMessages(),
        ...this.compactionQueuedMessages.filter((msg) => msg.mode === "followUp").map((msg) => msg.text)
      ]
    };
  }
  /**
   * Clear all queued messages and return their contents.
   * Clears both session queue and compaction queue.
   */
  clearAllQueues() {
    const { steering, followUp } = this.session.clearQueue();
    const compactionSteering = this.compactionQueuedMessages.filter((msg) => msg.mode === "steer").map((msg) => msg.text);
    const compactionFollowUp = this.compactionQueuedMessages.filter((msg) => msg.mode === "followUp").map((msg) => msg.text);
    this.compactionQueuedMessages = [];
    return {
      steering: [...steering, ...compactionSteering],
      followUp: [...followUp, ...compactionFollowUp]
    };
  }
  updatePendingMessagesDisplay() {
    this.pendingMessagesContainer.clear();
    const { steering: steeringMessages, followUp: followUpMessages } = this.getAllQueuedMessages();
    if (steeringMessages.length > 0 || followUpMessages.length > 0) {
      this.pendingMessagesContainer.addChild(new Spacer27(1));
      for (const message of steeringMessages) {
        const text = theme.fg("dim", `Steering: ${message}`);
        this.pendingMessagesContainer.addChild(new TruncatedText2(text, 1, 0));
      }
      for (const message of followUpMessages) {
        const text = theme.fg("dim", `Follow-up: ${message}`);
        this.pendingMessagesContainer.addChild(new TruncatedText2(text, 1, 0));
      }
      const dequeueHint = this.getAppKeyDisplay("app.message.dequeue");
      const hintText = theme.fg("dim", `\u21B3 ${dequeueHint} to edit all queued messages`);
      this.pendingMessagesContainer.addChild(new TruncatedText2(hintText, 1, 0));
    }
  }
  restoreQueuedMessagesToEditor(options) {
    const { steering, followUp } = this.clearAllQueues();
    const allQueued = [...steering, ...followUp];
    if (allQueued.length === 0) {
      this.updatePendingMessagesDisplay();
      if (options?.abort) {
        void this.session.abort();
      }
      return 0;
    }
    const queuedText = allQueued.join("\n\n");
    const currentText = options?.currentText ?? this.editor.getText();
    const combinedText = [queuedText, currentText].filter((t) => t.trim()).join("\n\n");
    this.editor.setText(combinedText);
    this.updatePendingMessagesDisplay();
    if (options?.abort) {
      void this.session.abort();
    }
    return allQueued.length;
  }
  queueCompactionMessage(text, mode) {
    this.compactionQueuedMessages.push({ text, mode });
    this.editor.addToHistory?.(text);
    this.editor.setText("");
    this.updatePendingMessagesDisplay();
    this.showStatus("Queued message for after compaction");
  }
  isExtensionCommand(text) {
    if (!text.startsWith("/"))
      return false;
    const extensionRunner = this.session.extensionRunner;
    const spaceIndex = text.indexOf(" ");
    const commandName = spaceIndex === -1 ? text.slice(1) : text.slice(1, spaceIndex);
    return !!extensionRunner.getCommand(commandName);
  }
  async flushCompactionQueue(options) {
    if (this.compactionQueuedMessages.length === 0) {
      return;
    }
    const queuedMessages = [...this.compactionQueuedMessages];
    this.compactionQueuedMessages = [];
    this.updatePendingMessagesDisplay();
    const restoreQueue = /* @__PURE__ */ __name((error) => {
      this.session.clearQueue();
      this.compactionQueuedMessages = queuedMessages;
      this.updatePendingMessagesDisplay();
      this.showError(`Failed to send queued message${queuedMessages.length > 1 ? "s" : ""}: ${error instanceof Error ? error.message : String(error)}`);
    }, "restoreQueue");
    try {
      if (options?.willRetry) {
        for (const message of queuedMessages) {
          if (this.isExtensionCommand(message.text)) {
            await this.session.prompt(message.text);
          } else if (message.mode === "followUp") {
            await this.session.followUp(message.text);
          } else {
            await this.session.steer(message.text);
          }
        }
        this.updatePendingMessagesDisplay();
        return;
      }
      const firstPromptIndex = queuedMessages.findIndex((message) => !this.isExtensionCommand(message.text));
      if (firstPromptIndex === -1) {
        for (const message of queuedMessages) {
          await this.session.prompt(message.text);
        }
        return;
      }
      const preCommands = queuedMessages.slice(0, firstPromptIndex);
      const firstPrompt = queuedMessages[firstPromptIndex];
      const rest = queuedMessages.slice(firstPromptIndex + 1);
      for (const message of preCommands) {
        await this.session.prompt(message.text);
      }
      const promptPromise = this.session.prompt(firstPrompt.text, { streamingBehavior: firstPrompt.mode }).catch((error) => {
        restoreQueue(error);
      });
      for (const message of rest) {
        if (this.isExtensionCommand(message.text)) {
          await this.session.prompt(message.text);
        } else if (message.mode === "followUp") {
          await this.session.followUp(message.text);
        } else {
          await this.session.steer(message.text);
        }
      }
      this.updatePendingMessagesDisplay();
      void promptPromise;
    } catch (error) {
      restoreQueue(error);
    }
  }
  /** Move pending bash components from pending area to chat */
  flushPendingBashComponents() {
    for (const component of this.pendingBashComponents) {
      this.pendingMessagesContainer.removeChild(component);
      this.chatContainer.addChild(component);
    }
    this.pendingBashComponents = [];
  }
  // =========================================================================
  // Selectors
  // =========================================================================
  disposeActiveSelector() {
    const dispose = this.activeSelectorDispose;
    this.activeSelectorToken = void 0;
    this.activeSelectorDispose = void 0;
    dispose?.();
  }
  /**
   * Shows a selector component in place of the editor.
   * @param create Factory that receives a `done` callback and returns the component and focus target
   */
  showSelector(create) {
    const token = {};
    let dispose;
    const done = /* @__PURE__ */ __name(() => {
      dispose?.();
      if (this.activeSelectorToken !== token)
        return;
      this.activeSelectorToken = void 0;
      this.activeSelectorDispose = void 0;
      this.editorContainer.clear();
      this.editorContainer.addChild(this.editor);
      this.ui.setFocus(this.editor);
    }, "done");
    const created = create(done);
    dispose = created.dispose;
    this.disposeActiveSelector();
    this.activeSelectorToken = token;
    this.activeSelectorDispose = dispose;
    this.editorContainer.clear();
    this.editorContainer.addChild(created.component);
    this.ui.setFocus(created.focus);
    this.ui.requestRender();
  }
  showSettingsSelector() {
    this.showSelector((done) => {
      let selector;
      const defaultProvider = this.settingsManager.getDefaultProvider();
      const defaultModelId = this.settingsManager.getDefaultModel();
      const defaultModel = defaultProvider && defaultModelId ? `${defaultProvider}/${defaultModelId}` : "not set";
      selector = new SettingsSelectorComponent({
        autoCompact: this.session.autoCompactionEnabled,
        defaultModel,
        currentModel: this.session.model,
        availableDefaultModels: this.session.modelRuntime.getAvailableSnapshot(),
        showImages: this.settingsManager.getShowImages(),
        imageWidthCells: this.settingsManager.getImageWidthCells(),
        autoResizeImages: this.settingsManager.getImageAutoResize(),
        blockImages: this.settingsManager.getBlockImages(),
        enableSkillCommands: this.settingsManager.getEnableSkillCommands(),
        steeringMode: this.session.steeringMode,
        followUpMode: this.session.followUpMode,
        transport: this.settingsManager.getTransport(),
        httpIdleTimeoutMs: this.settingsManager.getHttpIdleTimeoutMs(),
        cacheWarmingMode: this.settingsManager.getCacheWarmingMode(),
        thinkingLevel: this.settingsManager.getDefaultThinkingLevel() ?? DEFAULT_THINKING_LEVEL,
        availableThinkingLevels: [...THINKING_LEVEL_OPTIONS],
        modelThinkingLevels: this.settingsManager.getAllModelThinkingLevels(),
        currentTheme: this.themeController.getThemeSelection() || SYSTEM_THEME_NAME,
        terminalTheme: this.themeController.getTerminalTheme(),
        availableThemes: getAvailableThemes(),
        hideThinkingBlock: this.hideThinkingBlock,
        mermaidRenderingMode: this.settingsManager.getMermaidRenderingMode(),
        collapseChangelog: this.settingsManager.getCollapseChangelog(),
        enableInstallTelemetry: this.settingsManager.getEnableInstallTelemetry(),
        doubleEscapeAction: this.settingsManager.getDoubleEscapeAction(),
        treeFilterMode: this.settingsManager.getTreeFilterMode(),
        showHardwareCursor: this.settingsManager.getShowHardwareCursor(),
        showCacheMissNotices: this.settingsManager.getShowCacheMissNotices(),
        defaultProjectTrust: this.settingsManager.getDefaultProjectTrust(),
        editorPaddingX: this.settingsManager.getEditorPaddingX(),
        outputPad: this.settingsManager.getOutputPad(),
        autocompleteMaxVisible: this.settingsManager.getAutocompleteMaxVisible(),
        quietStartup: this.settingsManager.getQuietStartup(),
        clearOnShrink: this.settingsManager.getClearOnShrink(),
        showTerminalProgress: this.settingsManager.getShowTerminalProgress(),
        tuiMode: this.ui.mode,
        fullscreenExitOutput: this.settingsManager.getFullscreenExitOutput(),
        fullscreenScrollbar: this.settingsManager.getFullscreenScrollbar(),
        fullscreenCopyOnSelect: this.settingsManager.getFullscreenCopyOnSelect(),
        fullscreenWheelScrollLines: this.settingsManager.getFullscreenWheelScrollLines(),
        warnings: this.settingsManager.getWarnings()
      }, {
        onAutoCompactChange: /* @__PURE__ */ __name((enabled) => {
          this.session.setAutoCompactionEnabled(enabled);
          this.footer.setAutoCompactEnabled(enabled);
        }, "onAutoCompactChange"),
        onShowImagesChange: /* @__PURE__ */ __name((enabled) => {
          this.settingsManager.setShowImages(enabled);
          for (const child of this.chatContainer.children) {
            if (child instanceof ToolExecutionComponent) {
              child.setShowImages(enabled);
            }
          }
        }, "onShowImagesChange"),
        onImageWidthCellsChange: /* @__PURE__ */ __name((width) => {
          this.settingsManager.setImageWidthCells(width);
          for (const child of this.chatContainer.children) {
            if (child instanceof ToolExecutionComponent) {
              child.setImageWidthCells(width);
            }
          }
        }, "onImageWidthCellsChange"),
        onAutoResizeImagesChange: /* @__PURE__ */ __name((enabled) => {
          this.settingsManager.setImageAutoResize(enabled);
        }, "onAutoResizeImagesChange"),
        onBlockImagesChange: /* @__PURE__ */ __name((blocked) => {
          this.settingsManager.setBlockImages(blocked);
        }, "onBlockImagesChange"),
        onEnableSkillCommandsChange: /* @__PURE__ */ __name((enabled) => {
          this.settingsManager.setEnableSkillCommands(enabled);
          this.setupAutocompleteProvider();
        }, "onEnableSkillCommandsChange"),
        onSteeringModeChange: /* @__PURE__ */ __name((mode) => {
          this.session.setSteeringMode(mode);
        }, "onSteeringModeChange"),
        onFollowUpModeChange: /* @__PURE__ */ __name((mode) => {
          this.session.setFollowUpMode(mode);
        }, "onFollowUpModeChange"),
        onTransportChange: /* @__PURE__ */ __name((transport) => {
          this.settingsManager.setTransport(transport);
          this.session.agent.transport = transport;
        }, "onTransportChange"),
        onHttpIdleTimeoutMsChange: /* @__PURE__ */ __name((timeoutMs) => {
          this.settingsManager.setHttpIdleTimeoutMs(timeoutMs);
          configureHttpDispatcher(timeoutMs);
          this.showStatus(`HTTP idle timeout: ${formatHttpIdleTimeoutMs(timeoutMs)}`);
        }, "onHttpIdleTimeoutMsChange"),
        onCacheWarmingModeChange: /* @__PURE__ */ __name((mode) => {
          this.session.setCacheWarmingMode(mode);
          this.showStatus(`Cache warming: ${mode}`);
        }, "onCacheWarmingModeChange"),
        onModelThinkingLevelChange: /* @__PURE__ */ __name((provider, modelId, level) => {
          this.settingsManager.setModelThinkingLevel(provider, modelId, level);
          const current = this.session.model;
          if (current && current.provider === provider && current.id === modelId) {
            this.session.setThinkingLevel(level);
            this.footer.invalidate();
            this.updateEditorBorderColor();
          }
        }, "onModelThinkingLevelChange"),
        onModelThinkingLevelRemove: /* @__PURE__ */ __name((provider, modelId) => {
          this.settingsManager.removeModelThinkingLevel(provider, modelId);
          const current = this.session.model;
          if (current && current.provider === provider && current.id === modelId) {
            const globalDefault = this.settingsManager.getDefaultThinkingLevel() ?? DEFAULT_THINKING_LEVEL;
            this.session.setThinkingLevel(globalDefault);
            this.footer.invalidate();
            this.updateEditorBorderColor();
          }
        }, "onModelThinkingLevelRemove"),
        onThemeChange: /* @__PURE__ */ __name((themeSetting) => {
          this.settingsManager.setTheme(themeSetting);
          this.themeController.setThemeSetting(themeSetting);
        }, "onThemeChange"),
        onThemePreview: /* @__PURE__ */ __name((themeName) => this.themeController.preview(themeName), "onThemePreview"),
        onHideThinkingBlockChange: /* @__PURE__ */ __name((hidden) => {
          this.hideThinkingBlock = hidden;
          this.settingsManager.setHideThinkingBlock(hidden);
          this.updateThinkingBlockVisibility();
        }, "onHideThinkingBlockChange"),
        onMermaidRenderingModeChange: /* @__PURE__ */ __name((mode) => {
          this.settingsManager.setMermaidRenderingMode(mode);
          this.chatContainer.invalidate();
          this.ui.requestRender();
        }, "onMermaidRenderingModeChange"),
        onShowCacheMissNoticesChange: /* @__PURE__ */ __name((shown) => {
          this.settingsManager.setShowCacheMissNotices(shown);
          this.rebuildChatFromMessages();
        }, "onShowCacheMissNoticesChange"),
        onCollapseChangelogChange: /* @__PURE__ */ __name((collapsed) => {
          this.settingsManager.setCollapseChangelog(collapsed);
        }, "onCollapseChangelogChange"),
        onEnableInstallTelemetryChange: /* @__PURE__ */ __name((enabled) => {
          this.settingsManager.setEnableInstallTelemetry(enabled);
        }, "onEnableInstallTelemetryChange"),
        onQuietStartupChange: /* @__PURE__ */ __name((quiet) => {
          this.settingsManager.setQuietStartup(quiet);
        }, "onQuietStartupChange"),
        onDefaultProjectTrustChange: /* @__PURE__ */ __name((defaultProjectTrust) => {
          this.settingsManager.setDefaultProjectTrust(defaultProjectTrust);
        }, "onDefaultProjectTrustChange"),
        onDoubleEscapeActionChange: /* @__PURE__ */ __name((action) => {
          this.settingsManager.setDoubleEscapeAction(action);
        }, "onDoubleEscapeActionChange"),
        onTreeFilterModeChange: /* @__PURE__ */ __name((mode) => {
          this.settingsManager.setTreeFilterMode(mode);
        }, "onTreeFilterModeChange"),
        onShowHardwareCursorChange: /* @__PURE__ */ __name((enabled) => {
          this.settingsManager.setShowHardwareCursor(enabled);
          this.ui.setShowHardwareCursor(enabled);
        }, "onShowHardwareCursorChange"),
        onEditorPaddingXChange: /* @__PURE__ */ __name((padding) => {
          this.settingsManager.setEditorPaddingX(padding);
          this.defaultEditor.setPaddingX(padding);
          if (this.editor !== this.defaultEditor && this.editor.setPaddingX !== void 0) {
            this.editor.setPaddingX(padding);
          }
        }, "onEditorPaddingXChange"),
        onOutputPadChange: /* @__PURE__ */ __name((padding) => {
          this.settingsManager.setOutputPad(padding);
          this.outputPad = padding;
          for (const container of [this.chatContainer, this.pendingMessagesContainer]) {
            for (const child of container.children) {
              if ("setOutputPad" in child && typeof child.setOutputPad === "function") {
                child.setOutputPad(padding);
              }
            }
          }
          this.ui.requestRender();
        }, "onOutputPadChange"),
        onAutocompleteMaxVisibleChange: /* @__PURE__ */ __name((maxVisible) => {
          this.settingsManager.setAutocompleteMaxVisible(maxVisible);
          this.defaultEditor.setAutocompleteMaxVisible(maxVisible);
          if (this.editor !== this.defaultEditor && this.editor.setAutocompleteMaxVisible !== void 0) {
            this.editor.setAutocompleteMaxVisible(maxVisible);
          }
        }, "onAutocompleteMaxVisibleChange"),
        onClearOnShrinkChange: /* @__PURE__ */ __name((enabled) => {
          this.settingsManager.setClearOnShrink(enabled);
          this.ui.setClearOnShrink(enabled);
          if (!enabled && !this.activeStatusIndicator) {
            this.statusContainer.clear();
          }
        }, "onClearOnShrinkChange"),
        onShowTerminalProgressChange: /* @__PURE__ */ __name((enabled) => {
          this.settingsManager.setShowTerminalProgress(enabled);
        }, "onShowTerminalProgressChange"),
        onTuiModeChange: /* @__PURE__ */ __name((mode) => {
          if (!this.switchTuiMode(mode)) {
            selector?.getSettingsList().updateValue("tui-mode", this.ui.mode);
            this.showStatus("Close active overlays before changing TUI mode");
            return;
          }
          this.settingsManager.setTuiMode(mode);
          if (!this.activeStatusIndicator)
            this.statusContainer.clear();
          this.showStatus(`TUI mode: ${mode}`);
        }, "onTuiModeChange"),
        onFullscreenExitOutputChange: /* @__PURE__ */ __name((output) => {
          this.settingsManager.setFullscreenExitOutput(output);
        }, "onFullscreenExitOutputChange"),
        onFullscreenScrollbarChange: /* @__PURE__ */ __name((mode) => {
          this.settingsManager.setFullscreenScrollbar(mode);
          this.applyFullscreenScrollbarSetting();
        }, "onFullscreenScrollbarChange"),
        onFullscreenCopyOnSelectChange: /* @__PURE__ */ __name((enabled) => {
          this.settingsManager.setFullscreenCopyOnSelect(enabled);
          if (this.renderer instanceof TuiAltScreen3)
            this.renderer.setCopyOnSelect(enabled);
        }, "onFullscreenCopyOnSelectChange"),
        onFullscreenWheelScrollLinesChange: /* @__PURE__ */ __name((lines) => {
          this.settingsManager.setFullscreenWheelScrollLines(lines);
          if (this.renderer instanceof TuiAltScreen3)
            this.renderer.setWheelScrollLines(lines);
        }, "onFullscreenWheelScrollLinesChange"),
        onWarningsChange: /* @__PURE__ */ __name((warnings) => {
          this.settingsManager.setWarnings(warnings);
        }, "onWarningsChange"),
        onCancel: /* @__PURE__ */ __name(() => {
          done();
          this.ui.requestRender();
        }, "onCancel")
      });
      return { component: selector, focus: selector.getSettingsList() };
    });
  }
  handleThinkingCommand(searchTerm) {
    const availableLevels = this.session.getAvailableThinkingLevels();
    if (!searchTerm) {
      this.showThinkingSelector();
      return;
    }
    const normalized = searchTerm.trim().toLowerCase();
    const level = availableLevels.find((candidate) => candidate.toLowerCase() === normalized);
    if (!level) {
      this.showError(`Unknown thinking level "${searchTerm}". Available levels: ${availableLevels.join(", ")}.`);
      return;
    }
    this.selectThinkingLevel(level, false);
  }
  selectThinkingLevel(level, persist) {
    try {
      this.session.setThinkingLevel(level, { persist });
      this.footer.invalidate();
      this.updateEditorBorderColor();
      this.showStatus(persist ? `Default thinking level: ${level}` : `Thinking level: ${level}`);
    } catch (error) {
      this.showError(error instanceof Error ? error.message : String(error));
    }
  }
  showThinkingSelector() {
    this.showSelector((done) => {
      const selectLevel = /* @__PURE__ */ __name((level, persist) => {
        this.selectThinkingLevel(level, persist);
        done();
      }, "selectLevel");
      const selector = new ThinkingSelectorComponent(this.session.thinkingLevel ?? DEFAULT_THINKING_LEVEL, this.session.getAvailableThinkingLevels(), (level) => selectLevel(level, false), () => {
        done();
        this.ui.requestRender();
      }, (level) => selectLevel(level, true), this.settingsManager.getDefaultThinkingLevel() ?? DEFAULT_THINKING_LEVEL);
      return { component: selector, focus: selector };
    });
  }
  async handleModelCommand(searchTerm) {
    if (!searchTerm) {
      this.showModelSelector();
      return;
    }
    const model = await this.findExactModelMatch(searchTerm);
    if (model) {
      try {
        await this.session.setModel(model, { persist: false });
        this.footer.invalidate();
        this.updateEditorBorderColor();
        this.showStatus(`Model: ${model.id}`);
        void this.maybeWarnAboutAnthropicSubscriptionAuth(model);
      } catch (error) {
        this.showError(error instanceof Error ? error.message : String(error));
      }
      return;
    }
    this.showModelSelector(searchTerm);
  }
  async findExactModelMatch(searchTerm) {
    const cachedModels = this.session.scopedModels.length > 0 ? this.session.scopedModels.map((scoped) => scoped.model) : [...this.session.modelRuntime.getAvailableSnapshot()];
    const cachedMatch = findExactModelReferenceMatch(searchTerm, cachedModels);
    if (cachedMatch || this.session.scopedModels.length > 0)
      return cachedMatch;
    this.showStatus("Refreshing model catalogs\u2026");
    const controller = new AbortController();
    let timedOut = false;
    const timeout = setTimeout(() => {
      timedOut = true;
      controller.abort();
    }, 15e3);
    try {
      const result = await refreshModelCatalogs(this.session.modelRuntime, controller.signal);
      if (result.aborted && timedOut) {
        this.showWarning("Model refresh timed out; searching cached models.");
      } else if (result.errors.size > 0) {
        this.showWarning(`Could not refresh ${[...result.errors.keys()].join(", ")}; searching cached models.`);
      }
    } catch (error) {
      this.showWarning(timedOut ? "Model refresh timed out; searching cached models." : `Could not refresh model catalogs: ${error instanceof Error ? error.message : String(error)}`);
    } finally {
      clearTimeout(timeout);
    }
    return findExactModelReferenceMatch(searchTerm, [...this.session.modelRuntime.getAvailableSnapshot()]);
  }
  /** Update the footer's available provider count from the current snapshot without refreshing catalogs. */
  updateAvailableProviderCount() {
    const models = this.session.scopedModels.length > 0 ? this.session.scopedModels.map((scoped) => scoped.model) : this.session.modelRuntime.getAvailableSnapshot();
    const uniqueProviders = new Set(models.map((model) => model.provider));
    this.footerDataProvider.setAvailableProviderCount(uniqueProviders.size);
  }
  async maybeWarnAboutAnthropicSubscriptionAuth(model = this.session.model) {
    if (this.settingsManager.getWarnings().anthropicExtraUsage === false) {
      return;
    }
    if (this.anthropicSubscriptionWarningShown) {
      return;
    }
    if (!model || model.provider !== "anthropic") {
      return;
    }
    try {
      if ((await this.session.modelRuntime.checkAuth("anthropic"))?.type === "oauth") {
        this.anthropicSubscriptionWarningShown = true;
        this.showWarning(ANTHROPIC_SUBSCRIPTION_AUTH_WARNING);
        return;
      }
      const apiKey = (await this.session.modelRuntime.getAuth(model.provider))?.auth.apiKey;
      if (!isAnthropicSubscriptionAuthKey(apiKey)) {
        return;
      }
      this.anthropicSubscriptionWarningShown = true;
      this.showWarning(ANTHROPIC_SUBSCRIPTION_AUTH_WARNING);
    } catch {
    }
  }
  maybeSaveImplicitProjectTrustAfterReload() {
    const cwd = this.sessionManager.getCwd();
    if (this.autoTrustOnReloadCwd !== cwd) {
      return false;
    }
    if (!this.settingsManager.isProjectTrusted() || !hasTrustRequiringProjectResources(cwd)) {
      return false;
    }
    const trustStore = new ProjectTrustStore(this.runtimeHost.services.agentDir);
    try {
      if (trustStore.get(cwd) !== null) {
        this.autoTrustOnReloadCwd = void 0;
        return false;
      }
      trustStore.set(cwd, true);
      this.autoTrustOnReloadCwd = void 0;
      return true;
    } catch (error) {
      this.showWarning(`Could not save project trust after reload: ${error instanceof Error ? error.message : String(error)}`);
      return false;
    }
  }
  showTrustSelector() {
    const cwd = this.sessionManager.getCwd();
    const trustStore = new ProjectTrustStore(this.runtimeHost.services.agentDir);
    const savedDecision = trustStore.getEntry(cwd);
    this.showSelector((done) => {
      const selector = new TrustSelectorComponent({
        cwd,
        savedDecision,
        projectTrusted: this.settingsManager.isProjectTrusted(),
        onSelect: /* @__PURE__ */ __name((selection) => {
          trustStore.setMany(selection.updates);
          done();
          this.showStatus(`Saved trust decision: ${selection.trusted ? "trusted" : "untrusted"}. Restart ${APP_NAME} for this to take effect.`);
        }, "onSelect"),
        onCancel: /* @__PURE__ */ __name(() => {
          done();
          this.ui.requestRender();
        }, "onCancel")
      });
      return { component: selector, focus: selector };
    });
  }
  showModelSelector(initialSearchInput) {
    this.showSelector((done) => {
      const selectModel = /* @__PURE__ */ __name(async (model, persist) => {
        try {
          await this.session.setModel(model, { persist });
          this.updateAvailableProviderCount();
          this.footer.invalidate();
          this.updateEditorBorderColor();
          done();
          this.showStatus(persist ? `Default model: ${model.provider}/${model.id}` : `Model: ${model.id}`);
          void this.maybeWarnAboutAnthropicSubscriptionAuth(model);
        } catch (error) {
          done();
          this.showError(error instanceof Error ? error.message : String(error));
        }
      }, "selectModel");
      const defaultProvider = this.settingsManager.getDefaultProvider();
      const defaultModel = this.settingsManager.getDefaultModel();
      const selector = new ModelSelectorComponent(this.ui, this.session.model, this.session.modelRuntime, this.session.scopedModels, (model) => selectModel(model, false), () => {
        done();
        this.ui.requestRender();
      }, initialSearchInput, (model) => selectModel(model, true), defaultProvider && defaultModel ? { provider: defaultProvider, id: defaultModel } : void 0);
      return { component: selector, focus: selector, dispose: /* @__PURE__ */ __name(() => selector.dispose(), "dispose") };
    });
  }
  showModelsSelector() {
    let availableModels = [...this.session.modelRuntime.getAvailableSnapshot()];
    let availableModelIds = new Set(availableModels.map((model) => `${model.provider}/${model.id}`));
    const configuredPatterns = this.settingsManager.getEnabledModels();
    const sessionScopedModels = this.session.scopedModels;
    const configuredEnabledIds = /* @__PURE__ */ __name((models) => {
      if (!configuredPatterns?.length)
        return null;
      const resolved = resolveModelScopeFromModels(configuredPatterns, models);
      const ids = resolved.scopedModels.map((scoped) => `${scoped.model.provider}/${scoped.model.id}`);
      for (const diagnostic of resolved.diagnostics) {
        if (diagnostic.code === "no-match" && !ids.includes(diagnostic.pattern))
          ids.push(diagnostic.pattern);
      }
      return ids;
    }, "configuredEnabledIds");
    let currentEnabledIds = sessionScopedModels.length > 0 ? sessionScopedModels.map((scoped) => `${scoped.model.provider}/${scoped.model.id}`) : configuredEnabledIds(availableModels);
    let selectionChanged = false;
    const updateSessionModels = /* @__PURE__ */ __name((enabledIds) => {
      currentEnabledIds = enabledIds === null ? null : [...enabledIds];
      const hasEnabledAvailableModel = enabledIds?.some((id) => availableModelIds.has(id)) ?? false;
      const allAvailableModelsEnabled = enabledIds !== null && [...availableModelIds].every((id) => enabledIds.includes(id));
      if (enabledIds && hasEnabledAvailableModel && !allAvailableModelsEnabled) {
        const newScopedModels = resolveModelScopeFromModels(enabledIds, availableModels).scopedModels;
        this.session.setScopedModels(newScopedModels.map((scoped) => ({
          model: scoped.model,
          thinkingLevel: scoped.thinkingLevel
        })));
      } else {
        this.session.setScopedModels([]);
      }
      this.updateAvailableProviderCount();
      this.ui.requestRender();
    }, "updateSessionModels");
    this.showSelector((done) => {
      let disposed = false;
      let timedOut = false;
      const controller = new AbortController();
      const timeout = setTimeout(() => {
        timedOut = true;
        controller.abort();
      }, 15e3);
      const selector = new ScopedModelsSelectorComponent({
        allModels: availableModels,
        enabledModelIds: currentEnabledIds,
        refreshStatus: "Refreshing model catalogs\u2026"
      }, {
        onChange: /* @__PURE__ */ __name((enabledIds) => {
          selectionChanged = true;
          updateSessionModels(enabledIds);
        }, "onChange"),
        onPersist: /* @__PURE__ */ __name((enabledIds) => {
          const allEnabled = enabledIds !== null && enabledIds.length === availableModels.length && enabledIds.every((id) => availableModelIds.has(id));
          const newPatterns = enabledIds === null || allEnabled ? void 0 : enabledIds;
          this.settingsManager.setEnabledModels(newPatterns ? [...newPatterns] : void 0);
          this.showStatus("Model selection saved to settings");
        }, "onPersist"),
        onCancel: /* @__PURE__ */ __name(() => {
          done();
          this.ui.requestRender();
        }, "onCancel")
      });
      void refreshModelCatalogs(this.session.modelRuntime, controller.signal).then((result) => {
        if (disposed)
          return;
        availableModels = [...this.session.modelRuntime.getAvailableSnapshot()];
        availableModelIds = new Set(availableModels.map((model) => `${model.provider}/${model.id}`));
        if (!selectionChanged && sessionScopedModels.length === 0) {
          currentEnabledIds = configuredEnabledIds(availableModels);
          selector.updateModels(availableModels, currentEnabledIds);
        } else {
          selector.updateModels(availableModels);
        }
        if (currentEnabledIds !== null)
          updateSessionModels(currentEnabledIds);
        if (result.aborted && timedOut) {
          selector.setRefreshStatus("Model refresh timed out; showing cached models.", "warning");
        } else if (result.errors.size > 0) {
          selector.setRefreshStatus(`Could not refresh ${[...result.errors.keys()].join(", ")}; showing cached models.`, "warning");
        } else {
          selector.setRefreshStatus("Model catalogs refreshed.", "success");
        }
        this.ui.requestRender();
      }).catch((error) => {
        if (disposed)
          return;
        selector.setRefreshStatus(timedOut ? "Model refresh timed out; showing cached models." : `Could not refresh model catalogs: ${error instanceof Error ? error.message : String(error)}`, "warning");
        this.ui.requestRender();
      }).finally(() => clearTimeout(timeout));
      return {
        component: selector,
        focus: selector,
        dispose: /* @__PURE__ */ __name(() => {
          disposed = true;
          clearTimeout(timeout);
          controller.abort();
        }, "dispose")
      };
    });
  }
  showUserMessageSelector() {
    const userMessages = this.session.getUserMessagesForForking();
    if (userMessages.length === 0) {
      this.showStatus("No messages to fork from");
      return;
    }
    const initialSelectedId = userMessages[userMessages.length - 1]?.entryId;
    this.showSelector((done) => {
      const selector = new UserMessageSelectorComponent(userMessages.map((m) => ({ id: m.entryId, text: m.text })), async (entryId) => {
        done();
        try {
          const result = await this.runtimeHost.fork(entryId);
          if (result.cancelled) {
            this.ui.requestRender();
            return;
          }
          this.editor.setText(result.selectedText ?? "");
          this.showStatus("Forked to new session");
        } catch (error) {
          this.showError(error instanceof Error ? error.message : String(error));
        }
      }, () => {
        done();
        this.ui.requestRender();
      }, initialSelectedId);
      return { component: selector, focus: selector.getMessageList() };
    });
  }
  async handleCloneCommand() {
    const leafId = this.sessionManager.getLeafId();
    if (!leafId) {
      this.showStatus("Nothing to clone yet");
      return;
    }
    try {
      const result = await this.runtimeHost.fork(leafId, { position: "at" });
      if (result.cancelled) {
        this.ui.requestRender();
        return;
      }
      this.editor.setText("");
      this.showStatus("Cloned to new session");
    } catch (error) {
      this.showError(error instanceof Error ? error.message : String(error));
    }
  }
  showTreeSelector(initialSelectedId) {
    const tree = this.sessionManager.getTree();
    const realLeafId = this.sessionManager.getLeafId();
    const initialFilterMode = this.settingsManager.getTreeFilterMode();
    if (tree.length === 0) {
      this.showStatus("No entries in session");
      return;
    }
    this.showSelector((done) => {
      const selector = new TreeSelectorComponent(tree, realLeafId, this.ui.terminal.rows, async (entryId) => {
        if (entryId === this.sessionManager.getLeafId()) {
          done();
          this.showStatus("Already at this point");
          return;
        }
        done();
        let wantsSummary = false;
        let customInstructions;
        if (!this.settingsManager.getBranchSummarySkipPrompt()) {
          while (true) {
            const summaryChoice = await this.showExtensionSelector("Summarize branch?", [
              "No summary",
              "Summarize",
              "Summarize with custom prompt"
            ]);
            if (summaryChoice === void 0) {
              this.showTreeSelector(entryId);
              return;
            }
            wantsSummary = summaryChoice !== "No summary";
            if (summaryChoice === "Summarize with custom prompt") {
              customInstructions = await this.showExtensionEditor("Custom summarization instructions");
              if (customInstructions === void 0) {
                continue;
              }
            }
            break;
          }
        }
        if (this.session.isStreaming) {
          this.restoreQueuedMessagesToEditor();
          await this.session.abort();
        }
        if (this.session.isCompacting) {
          this.showError("Wait for the current compaction or tree navigation to finish before navigating the session tree.");
          return;
        }
        let showingSummaryIndicator = false;
        const originalOnEscape = this.defaultEditor.onEscape;
        if (wantsSummary) {
          this.defaultEditor.onEscape = () => {
            this.session.abortBranchSummary();
          };
          this.chatContainer.addChild(new Spacer27(1));
          this.showStatusIndicator(new BranchSummaryStatusIndicator(this.ui));
          showingSummaryIndicator = true;
          this.ui.requestRender();
        }
        try {
          const result = await this.session.navigateTree(entryId, {
            summarize: wantsSummary,
            customInstructions
          });
          if (result.aborted) {
            this.showStatus("Branch summarization cancelled");
            this.showTreeSelector(entryId);
            return;
          }
          if (result.cancelled) {
            this.showStatus("Navigation cancelled");
            return;
          }
          this.chatContainer.clear();
          this.renderInitialMessages();
          if (result.editorText && !this.editor.getText().trim()) {
            this.editor.setText(result.editorText);
          }
          this.showStatus("Navigated to selected point");
          void this.flushCompactionQueue({ willRetry: false });
        } catch (error) {
          this.showError(error instanceof Error ? error.message : String(error));
        } finally {
          if (showingSummaryIndicator) {
            this.clearStatusIndicator("branchSummary");
          }
          this.defaultEditor.onEscape = originalOnEscape;
        }
      }, () => {
        done();
        this.ui.requestRender();
      }, (entryId, label) => {
        this.sessionManager.appendLabelChange(entryId, label);
        this.ui.requestRender();
      }, initialSelectedId, initialFilterMode);
      selector.onCopy = async (text) => {
        if (!text) {
          this.showError("Selected entry has no text to copy");
          return;
        }
        try {
          await copyToClipboard(text);
          this.showStatus("Copied selected message to clipboard");
        } catch (error) {
          this.showError(error instanceof Error ? error.message : String(error));
        }
      };
      return { component: selector, focus: selector };
    });
  }
  showSessionSelector() {
    this.showSelector((done) => {
      const selector = new SessionSelectorComponent((onProgress, signal) => SessionManager.list(this.sessionManager.getCwd(), this.sessionManager.getSessionDir(), onProgress, signal), (onProgress, signal) => this.sessionManager.usesDefaultSessionDir() ? SessionManager.listAll(onProgress, signal) : SessionManager.listAll(this.sessionManager.getSessionDir(), onProgress, signal), async (sessionPath) => {
        done();
        await this.handleResumeSession(sessionPath);
      }, () => {
        done();
        this.ui.requestRender();
      }, () => {
        void this.shutdown();
      }, () => this.ui.requestRender(), {
        renameSession: /* @__PURE__ */ __name(async (sessionFilePath, nextName) => {
          const next = (nextName ?? "").trim();
          if (!next)
            return;
          const mgr = SessionManager.open(sessionFilePath);
          mgr.appendSessionInfo(next);
        }, "renameSession"),
        showRenameHint: true,
        keybindings: this.keybindings
      }, this.sessionManager.getSessionFile());
      return { component: selector, focus: selector };
    });
  }
  async handleResumeSession(sessionPath, options) {
    this.clearStatusIndicator();
    try {
      const result = await this.runtimeHost.switchSession(sessionPath, {
        withSession: options?.withSession,
        projectTrustContextFactory: /* @__PURE__ */ __name((cwd) => this.createProjectTrustContext(cwd), "projectTrustContextFactory")
      });
      if (result.cancelled) {
        return result;
      }
      this.showStatus("Resumed session");
      return result;
    } catch (error) {
      if (error instanceof MissingSessionCwdError) {
        const selectedCwd = await this.promptForMissingSessionCwd(error);
        if (!selectedCwd) {
          this.showStatus("Resume cancelled");
          return { cancelled: true };
        }
        const result = await this.runtimeHost.switchSession(sessionPath, {
          cwdOverride: selectedCwd,
          withSession: options?.withSession,
          projectTrustContextFactory: /* @__PURE__ */ __name((cwd) => this.createProjectTrustContext(cwd), "projectTrustContextFactory")
        });
        if (result.cancelled) {
          return result;
        }
        this.showStatus("Resumed session in current cwd");
        return result;
      }
      return this.handleFatalRuntimeError("Failed to resume session", error);
    }
  }
  getLoginProviderOptions(authType) {
    const options = [];
    for (const provider of this.session.modelRuntime.getProviders()) {
      const authStatus = this.session.modelRuntime.getProviderAuthStatus(provider.id);
      const status = authStatus.configured ? {
        type: this.session.modelRuntime.isUsingOAuth(provider.id) ? "oauth" : "api_key",
        source: authStatus.label ?? authStatus.source
      } : void 0;
      const subscription = provider.auth.oauth?.isSubscription === true;
      if ((!authType || authType === "oauth") && provider.auth.oauth) {
        options.push({
          id: provider.id,
          name: provider.name,
          authType: "oauth",
          method: provider.auth.oauth,
          status,
          subscription
        });
      }
      if ((!authType || authType === "api_key") && provider.auth.apiKey) {
        options.push({
          id: provider.id,
          name: provider.name,
          authType: "api_key",
          method: provider.auth.apiKey,
          status,
          subscription
        });
      }
    }
    return options.sort((a, b) => a.name.localeCompare(b.name));
  }
  async getLogoutProviderOptions() {
    return (await this.session.modelRuntime.listCredentials({ signal: AbortSignal.timeout(15e3) })).map(({ providerId, type }) => {
      const provider = this.session.modelRuntime.getProvider(providerId);
      return {
        id: providerId,
        name: provider?.name ?? providerId,
        authType: type,
        status: { type, source: "stored credential" },
        subscription: provider?.auth.oauth?.isSubscription === true
      };
    }).sort((a, b) => a.name.localeCompare(b.name));
  }
  findLoginProviderOptions(providerRef) {
    const normalizedProviderRef = providerRef.trim().toLowerCase();
    if (!normalizedProviderRef) {
      return [];
    }
    return this.getLoginProviderOptions().filter((provider) => provider.id.toLowerCase() === normalizedProviderRef || provider.name.toLowerCase() === normalizedProviderRef);
  }
  async handleLoginCommand(providerRef) {
    if (!providerRef) {
      this.showLoginAuthTypeSelector();
      return;
    }
    const providerOptions = this.findLoginProviderOptions(providerRef);
    if (providerOptions.length === 1) {
      await this.startProviderLogin(providerOptions[0]);
      return;
    }
    if (providerOptions.length > 1) {
      const providerIds = new Set(providerOptions.map((provider) => provider.id));
      if (providerIds.size === 1) {
        this.showLoginAuthTypeSelector(providerOptions);
        return;
      }
    }
    this.showLoginProviderSelector(void 0, providerRef);
  }
  /** `onBack` reopens the selector the login was started from when the user cancels it. */
  async startProviderLogin(providerOption, onBack) {
    if (providerOption.authType === "oauth") {
      await this.showLoginDialog(providerOption.id, providerOption.name, onBack);
    } else if (providerOption.method?.login) {
      await this.showApiKeyLoginDialog(providerOption.id, providerOption.name, onBack);
    } else {
      this.showAmbientAuthDialog(providerOption, onBack);
    }
  }
  showLoginAuthTypeSelector(providerOptions) {
    const radiusOption = providerOptions ? void 0 : this.getLoginProviderOptions("oauth").find((provider) => provider.id === RADIUS_PROVIDER_ID);
    const radiusText = radiusOption ? `Sign in with ${radiusOption.name}` : void 0;
    const radiusLabel = radiusOption ? `${radiusText}${formatAuthSelectorProviderStatus(radiusOption)}` : void 0;
    const oauthProvider = providerOptions?.find((provider) => provider.authType === "oauth");
    const oauthLoginLabel = oauthProvider?.method && "loginLabel" in oauthProvider.method ? oauthProvider.method.loginLabel : void 0;
    const subscriptionLabel = oauthLoginLabel ?? "Sign in with an account";
    const apiKeyLabel = "Sign in with an API key";
    const availableAuthTypes = providerOptions ? new Set(providerOptions.map((provider) => provider.authType)) : /* @__PURE__ */ new Set(["oauth", "api_key"]);
    const options = [];
    if (availableAuthTypes.has("oauth")) {
      options.push(subscriptionLabel);
    }
    if (availableAuthTypes.has("api_key")) {
      options.push(apiKeyLabel);
    }
    if (radiusLabel)
      options.push(radiusLabel);
    if (options.length === 0) {
      this.showStatus("No login methods available.");
      return;
    }
    if (providerOptions && options.length === 1) {
      const providerOption = providerOptions[0];
      if (providerOption) {
        void this.startProviderLogin(providerOption);
      }
      return;
    }
    const title = providerOptions?.[0] ? `Select authentication method for ${providerOptions[0].name}:` : "Select authentication method:";
    this.showSelector((done) => {
      const onSelect = /* @__PURE__ */ __name((option) => {
        done();
        if (radiusOption && option === radiusLabel) {
          void this.startProviderLogin(radiusOption, () => this.showLoginAuthTypeSelector());
          return;
        }
        const authType = option === subscriptionLabel ? "oauth" : "api_key";
        if (providerOptions) {
          const providerOption = providerOptions.find((provider) => provider.authType === authType);
          if (providerOption) {
            void this.startProviderLogin(providerOption, () => this.showLoginAuthTypeSelector(providerOptions));
          }
          return;
        }
        this.showLoginProviderSelector(authType);
      }, "onSelect");
      const onCancel = /* @__PURE__ */ __name(() => {
        done();
        this.ui.requestRender();
      }, "onCancel");
      const selector = radiusLabel && radiusText ? createLoginMenuSelector(this.ui, title, options, { label: radiusLabel, text: radiusText }, onSelect, onCancel) : new ExtensionSelectorComponent(title, options, onSelect, onCancel);
      return { component: selector, focus: selector, dispose: /* @__PURE__ */ __name(() => selector.dispose(), "dispose") };
    });
  }
  showLoginProviderSelector(authType, initialSearchInput) {
    const providerOptions = this.getLoginProviderOptions(authType);
    if (providerOptions.length === 0) {
      const message = authType === "oauth" ? "No account providers available." : authType === "api_key" ? "No API key providers available." : "No login providers available.";
      this.showStatus(message);
      return;
    }
    this.showSelector((done) => {
      const selector = new OAuthSelectorComponent("login", providerOptions, async (providerId, selectedAuthType) => {
        done();
        const providerOption = providerOptions.find((provider) => provider.id === providerId && provider.authType === selectedAuthType);
        if (!providerOption) {
          return;
        }
        await this.startProviderLogin(providerOption, () => this.showLoginProviderSelector(authType, initialSearchInput));
      }, () => {
        done();
        if (authType) {
          this.showLoginAuthTypeSelector();
        } else {
          this.ui.requestRender();
        }
      }, initialSearchInput);
      return { component: selector, focus: selector };
    });
  }
  async showOAuthSelector(mode) {
    if (mode === "login") {
      this.showLoginAuthTypeSelector();
      return;
    }
    let providerOptions;
    try {
      providerOptions = await this.getLogoutProviderOptions();
    } catch (error) {
      this.showError(`Could not read stored credentials: ${error instanceof Error ? error.message : String(error)}`);
      return;
    }
    if (providerOptions.length === 0) {
      this.showStatus("No stored credentials to remove. /logout only removes credentials saved by /login; environment variables and models.json config are unchanged.");
      return;
    }
    this.showSelector((done) => {
      const selector = new OAuthSelectorComponent(mode, providerOptions, async (providerId) => {
        done();
        const providerOption = providerOptions.find((provider) => provider.id === providerId);
        if (!providerOption) {
          return;
        }
        try {
          await this.session.modelRuntime.logout(providerOption.id, {
            signal: AbortSignal.timeout(15e3)
          });
          await this.updateAvailableProviderCount();
          const message = providerOption.authType === "oauth" ? `Logged out of ${providerOption.name}` : `Removed stored API key for ${providerOption.name}. Environment variables and models.json config are unchanged.`;
          this.showStatus(message);
        } catch (error) {
          const message = error instanceof Error ? error.message : String(error);
          this.showError(error instanceof CredentialSynchronizationError ? `Credentials removed for ${providerOption.name}, but local model state could not be synchronized: ${message}` : `Logout failed: ${message}`);
        }
      }, () => {
        done();
        this.ui.requestRender();
      });
      return { component: selector, focus: selector };
    });
  }
  async completeProviderAuthentication(providerId, providerName, authType, previousModel) {
    const actionLabel = authType === "oauth" ? `Logged in to ${providerName}` : `Saved API key for ${providerName}`;
    const session = this.session;
    const deferSelection = isUnknownModel(previousModel) && hasDefaultModelProvider(providerId) && !session.modelRuntime.getAvailableSnapshot().some((model) => model.provider === providerId && model.id === defaultModelPerProvider[providerId]);
    const finishAuthentication = /* @__PURE__ */ __name(async () => {
      let selectedModel;
      let selectionError;
      if (isUnknownModel(previousModel)) {
        const availableModels = this.session.modelRuntime.getAvailableSnapshot();
        const providerModels = availableModels.filter((model) => model.provider === providerId);
        if (providerId === "llama.cpp") {
          selectionError = llamaCppPostLoginGuidance(actionLabel, providerModels.length);
        } else if (!hasDefaultModelProvider(providerId)) {
          selectionError = `${actionLabel}, but no default model is configured for provider "${providerId}". Use /model to select a model.`;
        } else if (providerModels.length === 0) {
          selectionError = `${actionLabel}, but no models are available for that provider. Use /model to select a model.`;
        } else {
          const defaultModelId = defaultModelPerProvider[providerId];
          selectedModel = providerModels.find((model) => model.id === defaultModelId) ?? (providerId === "radius" ? providerModels[0] : void 0);
          if (!selectedModel) {
            selectionError = `${actionLabel}, but its default model "${defaultModelId}" is not available. Use /model to select a model.`;
          } else {
            try {
              await this.session.setModel(selectedModel, { persist: true });
            } catch (error) {
              selectedModel = void 0;
              const errorMessage4 = error instanceof Error ? error.message : String(error);
              selectionError = `${actionLabel}, but selecting its default model failed: ${errorMessage4}. Use /model to select a model.`;
            }
          }
        }
      }
      await this.updateAvailableProviderCount();
      this.footer.invalidate();
      this.updateEditorBorderColor();
      if (selectedModel) {
        this.showStatus(`${actionLabel}. Selected ${selectedModel.id}. Credentials saved to ${getAuthPath()}`);
        void this.maybeWarnAboutAnthropicSubscriptionAuth(selectedModel);
      } else {
        this.showStatus(`${actionLabel}. Credentials saved to ${getAuthPath()}`);
        if (selectionError) {
          this.showError(selectionError);
        } else {
          void this.maybeWarnAboutAnthropicSubscriptionAuth();
        }
      }
    }, "finishAuthentication");
    if (deferSelection) {
      this.showStatus(`${actionLabel}. Credentials saved to ${getAuthPath()}. Refreshing model catalog\u2026`);
    } else {
      await finishAuthentication();
    }
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 15e3);
    void session.modelRuntime.refresh({ providers: [providerId], signal: controller.signal }).then(async (result) => {
      if (result.aborted) {
        this.showWarning(`${actionLabel}, but its model catalog refresh timed out; using cached models.`);
      } else if (result.errors.size > 0) {
        this.showWarning(`${actionLabel}, but its model catalog could not be refreshed; using cached models.`);
      }
      if (deferSelection && this.session === session && session.model === previousModel) {
        await finishAuthentication();
      }
      this.updateAvailableProviderCount();
      this.footer.invalidate();
      this.ui.requestRender();
    }).catch((error) => {
      this.showWarning(`${actionLabel}, but its model catalog could not be refreshed: ${error instanceof Error ? error.message : String(error)}`);
    }).finally(() => clearTimeout(timeout));
  }
  showAmbientAuthDialog(providerOption, onBack) {
    const restoreEditor3 = /* @__PURE__ */ __name(() => {
      this.editorContainer.clear();
      this.editorContainer.addChild(this.editor);
      this.ui.setFocus(this.editor);
      this.ui.requestRender();
    }, "restoreEditor");
    const dialog = new LoginDialogComponent(this.ui, providerOption.id, () => {
      restoreEditor3();
      onBack?.();
    }, providerOption.name, `${providerOption.name} setup`);
    dialog.showInfo(`${providerOption.method?.name ?? "Authentication"} is configured outside ${APP_NAME}.`, [], true);
    this.editorContainer.clear();
    this.editorContainer.addChild(dialog);
    this.ui.setFocus(dialog);
    this.ui.requestRender();
  }
  async showApiKeyLoginDialog(providerId, providerName, onBack) {
    const previousModel = this.session.model;
    const dialog = new LoginDialogComponent(this.ui, providerId, (_success, _message) => {
    }, providerName);
    if (providerId === "amazon-bedrock") {
      dialog.showDetails([
        theme.fg("text", "You can also use an AWS profile, IAM keys, or role-based credentials."),
        theme.fg("muted", "See:"),
        theme.fg("accent", `  ${path4.join(getDocsPath(), "providers.md")}`)
      ]);
    }
    this.editorContainer.clear();
    this.editorContainer.addChild(dialog);
    this.ui.setFocus(dialog);
    this.ui.requestRender();
    const restoreEditor3 = /* @__PURE__ */ __name(() => {
      this.editorContainer.clear();
      this.editorContainer.addChild(this.editor);
      this.ui.setFocus(this.editor);
      this.ui.requestRender();
    }, "restoreEditor");
    try {
      await this.loginProvider(dialog, providerId, providerName, "api_key");
      restoreEditor3();
      await this.completeProviderAuthentication(providerId, providerName, "api_key", previousModel);
    } catch (error) {
      restoreEditor3();
      const errorMsg = error instanceof Error ? error.message : String(error);
      if (error instanceof CredentialSynchronizationError) {
        this.showError(`Saved API key for ${providerName}, but local model state could not be synchronized: ${errorMsg}`);
      } else if (errorMsg === "Login cancelled") {
        onBack?.();
      } else {
        this.showError(`Failed to save API key for ${providerName}: ${errorMsg}`);
      }
    }
  }
  showAuthSelect(dialog, prompt, providerId) {
    return new Promise((resolve7, reject) => {
      const restoreDialog = /* @__PURE__ */ __name(() => {
        this.editorContainer.clear();
        this.editorContainer.addChild(dialog);
        this.ui.setFocus(dialog);
        this.ui.requestRender();
      }, "restoreDialog");
      const labels = prompt.options.map((option) => option.label);
      const selector = new ExtensionSelectorComponent(prompt.message, labels, (optionLabel) => {
        restoreDialog();
        const id = prompt.options.find((option) => option.label === optionLabel)?.id;
        if (id)
          resolve7(id);
        else
          reject(new Error("Login cancelled"));
      }, () => {
        restoreDialog();
        reject(new Error("Login cancelled"));
      }, { description: providerId === RADIUS_PROVIDER_ID ? RADIUS_LOGIN_INTRO : void 0 });
      this.editorContainer.clear();
      this.editorContainer.addChild(selector);
      this.ui.setFocus(selector);
      this.ui.requestRender();
    });
  }
  async showAuthPrompt(dialog, prompt, providerId) {
    let response;
    if (prompt.type === "select") {
      response = this.showAuthSelect(dialog, prompt, providerId);
    } else if (prompt.type === "manual_code") {
      response = dialog.showManualInput(prompt.message);
    } else {
      response = dialog.showPrompt(prompt.message, prompt.placeholder);
    }
    if (!prompt.signal)
      return response;
    if (prompt.signal.aborted)
      throw new Error("Login cancelled");
    const signal = prompt.signal;
    let onAbort;
    const aborted = new Promise((_resolve, reject) => {
      onAbort = /* @__PURE__ */ __name(() => reject(new Error("Login cancelled")), "onAbort");
      signal.addEventListener("abort", onAbort, { once: true });
    });
    try {
      return await Promise.race([response, aborted]);
    } finally {
      if (onAbort)
        signal.removeEventListener("abort", onAbort);
    }
  }
  notifyAuthDialog(dialog, event) {
    if (event.type === "auth_url") {
      dialog.showAuth(event.url, event.instructions);
    } else if (event.type === "device_code") {
      dialog.showDeviceCode(event);
      dialog.showWaiting("Waiting for authentication...");
    } else if (event.type === "info") {
      dialog.showInfo(event.message, event.links);
    } else {
      dialog.showProgress(event.message);
    }
  }
  async loginProvider(dialog, providerId, providerName, method) {
    this.programStatus.setBlocked("login", { kind: "auth", message: `Log in to ${providerName}` });
    try {
      await this.session.modelRuntime.login(providerId, method, {
        signal: dialog.signal,
        prompt: /* @__PURE__ */ __name((prompt) => this.showAuthPrompt(dialog, prompt, providerId), "prompt"),
        notify: /* @__PURE__ */ __name((event) => this.notifyAuthDialog(dialog, event), "notify")
      }, { getDeviceId: /* @__PURE__ */ __name(() => this.settingsManager.getOrCreateDeviceId(), "getDeviceId") });
    } finally {
      this.programStatus.setBlocked("login", void 0);
    }
  }
  async showLoginDialog(providerId, providerName, onBack) {
    const previousModel = this.session.model;
    const dialog = new LoginDialogComponent(this.ui, providerId, (_success, _message) => {
    }, providerName);
    this.editorContainer.clear();
    this.editorContainer.addChild(dialog);
    this.ui.setFocus(dialog);
    this.ui.requestRender();
    const restoreEditor3 = /* @__PURE__ */ __name(() => {
      this.editorContainer.clear();
      this.editorContainer.addChild(this.editor);
      this.ui.setFocus(this.editor);
      this.ui.requestRender();
    }, "restoreEditor");
    try {
      await this.loginProvider(dialog, providerId, providerName, "oauth");
      restoreEditor3();
      await this.completeProviderAuthentication(providerId, providerName, "oauth", previousModel);
      if (providerId === RADIUS_PROVIDER_ID)
        this.offerRadiusMcpServer(providerId, providerName);
    } catch (error) {
      restoreEditor3();
      const errorMsg = error instanceof Error ? error.message : String(error);
      if (error instanceof CredentialSynchronizationError) {
        this.showError(`Logged in to ${providerName}, but local model state could not be synchronized: ${errorMsg}`);
      } else if (errorMsg === "Login cancelled") {
        onBack?.();
      } else {
        this.showError(`Failed to login to ${providerName}: ${errorMsg}`);
      }
    }
  }
  /**
   * Offer to point the Radius MCP server in the global mcp.json at the Radius login, adding the server
   * when missing. Nothing is asked when a global server already uses this login.
   */
  offerRadiusMcpServer(providerId, providerName) {
    const mcpPath = path4.join(getAgentDir(), "mcp.json");
    const normalizeUrl = /* @__PURE__ */ __name((url) => url.replace(/\/+$/u, ""), "normalizeUrl");
    const { servers } = loadMcpConfig({
      agentDir: getAgentDir(),
      cwd: this.sessionManager.getCwd(),
      projectTrusted: false
    });
    const existing = servers.find((server) => "url" in server.config && normalizeUrl(server.config.url) === normalizeUrl(RADIUS_MCP_URL));
    if (existing && "url" in existing.config && existing.config.auth?.provider === providerId)
      return;
    let name = existing?.name ?? "radius";
    if (!existing && servers.some((server) => server.name === name))
      name = "radius-mcp";
    const config = existing && "url" in existing.config ? { ...existing.config, auth: { provider: providerId } } : { url: RADIUS_MCP_URL, auth: { provider: providerId } };
    delete config.oauth;
    this.showSelector((done) => {
      const selector = new ExtensionSelectorComponent(`Configure ${providerName} MCP in ${mcpPath}?`, ["Yes", "No"], (option) => {
        done();
        if (option !== "Yes")
          return;
        try {
          addMcpServerConfig(mcpPath, name, config);
        } catch (error) {
          this.showError(`Could not update ${mcpPath}: ${error instanceof Error ? error.message : String(error)}`);
          return;
        }
        void this.handleReloadCommand();
      }, () => {
        done();
        this.ui.requestRender();
      });
      return { component: selector, focus: selector };
    });
  }
  // =========================================================================
  // Command handlers
  // =========================================================================
  async handleReloadCommand() {
    if (this.session.isStreaming) {
      this.showWarning("Wait for the current response to finish before reloading.");
      return;
    }
    if (this.session.isCompacting) {
      this.showWarning("Wait for compaction to finish before reloading.");
      return;
    }
    this.resetExtensionUI();
    const reloadBox = new Container30();
    const borderColor = /* @__PURE__ */ __name((s) => theme.fg("border", s), "borderColor");
    reloadBox.addChild(new DynamicBorder(borderColor));
    reloadBox.addChild(new Spacer27(1));
    reloadBox.addChild(new ThemedText(() => theme.fg("muted", "Reloading keybindings, extensions, skills, prompts, themes, and context files..."), 1, 0));
    reloadBox.addChild(new Spacer27(1));
    reloadBox.addChild(new DynamicBorder(borderColor));
    const previousEditor = this.editor;
    this.editorContainer.clear();
    this.editorContainer.addChild(reloadBox);
    this.ui.setFocus(reloadBox);
    this.ui.requestRender(true);
    await new Promise((resolve7) => process.nextTick(resolve7));
    const dismissReloadBox = /* @__PURE__ */ __name((editor) => {
      this.editorContainer.clear();
      this.editorContainer.addChild(editor);
      this.ui.setFocus(editor);
      this.ui.requestRender();
    }, "dismissReloadBox");
    let chatRestoredBeforeSessionStart = false;
    let reloadBoxDismissed = false;
    const restoreChatBeforeSessionStart = /* @__PURE__ */ __name(() => {
      if (chatRestoredBeforeSessionStart) {
        return;
      }
      this.hideThinkingBlock = this.settingsManager.getHideThinkingBlock();
      this.outputPad = this.settingsManager.getOutputPad();
      this.rebuildChatFromMessages();
      chatRestoredBeforeSessionStart = true;
    }, "restoreChatBeforeSessionStart");
    try {
      await this.session.reload({ beforeSessionStart: restoreChatBeforeSessionStart });
      restoreChatBeforeSessionStart();
      this.keybindings.reload();
      const activeHeader = this.customHeader ?? this.builtInHeader;
      if (isExpandable(activeHeader)) {
        activeHeader.setExpanded(this.toolOutputExpanded);
      }
      setRegisteredThemes(this.session.resourceLoader.getThemes().themes);
      this.applyRuntimeSettings();
      this.themeController.applyFromSettings();
      this.setupAutocompleteProvider();
      const runner = this.session.extensionRunner;
      this.setupExtensionShortcuts(runner);
      this.showLoadedResources({
        force: false,
        showDiagnosticsWhenQuiet: true
      });
      const savedImplicitProjectTrust = this.maybeSaveImplicitProjectTrustAfterReload();
      const modelsJsonError = this.session.modelRuntime.getError();
      if (modelsJsonError) {
        this.showError(`models.json error: ${modelsJsonError}`);
      }
      this.showStatus(savedImplicitProjectTrust ? "Reloaded keybindings, extensions, skills, prompts, themes, and context files; saved project trust" : "Reloaded keybindings, extensions, skills, prompts, themes, and context files");
      dismissReloadBox(this.editor);
      reloadBoxDismissed = true;
    } catch (error) {
      if (!reloadBoxDismissed) {
        dismissReloadBox(previousEditor);
      }
      this.showError(`Reload failed: ${error instanceof Error ? error.message : String(error)}`);
    }
  }
  async handleExportCommand(text) {
    const outputPath = this.getPathCommandArgument(text, "/export");
    try {
      if (outputPath?.endsWith(".jsonl")) {
        const filePath = this.session.exportToJsonl(outputPath);
        this.showStatus(`Session exported to: ${filePath}`);
      } else {
        const filePath = await this.session.exportToHtml(outputPath, {
          themeName: theme.name
        });
        this.showStatus(`Session exported to: ${filePath}`);
      }
    } catch (error) {
      this.showError(`Failed to export session: ${error instanceof Error ? error.message : "Unknown error"}`);
    }
  }
  getPathCommandArgument(text, command) {
    if (text === command) {
      return void 0;
    }
    if (!text.startsWith(`${command} `)) {
      return void 0;
    }
    const argsString = text.slice(command.length + 1).trimStart();
    if (!argsString) {
      return void 0;
    }
    const firstChar = argsString[0];
    if (firstChar === '"' || firstChar === "'") {
      const closingQuoteIndex = argsString.indexOf(firstChar, 1);
      if (closingQuoteIndex < 0) {
        return void 0;
      }
      return argsString.slice(1, closingQuoteIndex);
    }
    const firstWhitespaceIndex = argsString.search(/\s/);
    if (firstWhitespaceIndex < 0) {
      return argsString;
    }
    return argsString.slice(0, firstWhitespaceIndex);
  }
  async handleImportCommand(text) {
    const inputPath = this.getPathCommandArgument(text, "/import");
    if (!inputPath) {
      this.showError("Usage: /import <path.jsonl>");
      return;
    }
    const confirmed = await this.showExtensionConfirm("Import session", `Replace current session with ${inputPath}?`);
    if (!confirmed) {
      this.showStatus("Import cancelled");
      return;
    }
    try {
      this.clearStatusIndicator();
      const result = await this.runtimeHost.importFromJsonl(inputPath);
      if (result.cancelled) {
        this.showStatus("Import cancelled");
        return;
      }
      this.showStatus(`Session imported from: ${inputPath}`);
    } catch (error) {
      if (error instanceof MissingSessionCwdError) {
        const selectedCwd = await this.promptForMissingSessionCwd(error);
        if (!selectedCwd) {
          this.showStatus("Import cancelled");
          return;
        }
        const result = await this.runtimeHost.importFromJsonl(inputPath, selectedCwd);
        if (result.cancelled) {
          this.showStatus("Import cancelled");
          return;
        }
        this.showStatus(`Session imported from: ${inputPath}`);
        return;
      }
      if (error instanceof SessionImportFileNotFoundError) {
        this.showError(`Failed to import session: ${error.message}`);
        return;
      }
      await this.handleFatalRuntimeError("Failed to import session", error);
    }
  }
  async handleShareCommand() {
    await shareSession({
      session: this.session,
      ui: this.ui,
      editorContainer: this.editorContainer,
      editor: this.editor,
      showStatus: /* @__PURE__ */ __name((message) => this.showStatus(message), "showStatus"),
      showError: /* @__PURE__ */ __name((message) => this.showError(message), "showError")
    });
  }
  async handleBugCommand(hint) {
    await reportBug({
      session: this.session,
      ui: this.ui,
      editorContainer: this.editorContainer,
      editor: this.editor,
      keybindings: this.keybindings,
      showStatus: /* @__PURE__ */ __name((message) => this.showStatus(message), "showStatus"),
      showError: /* @__PURE__ */ __name((message) => this.showError(message), "showError")
    }, hint);
  }
  async handleCopyCommand(options = {}) {
    if (options.preferSelection && this.ui instanceof TuiAltScreen3 && !this.ui.getCopyOnSelect() && this.ui.hasActiveSelection()) {
      await this.ui.copyActiveSelectionToClipboard();
      return;
    }
    const text = this.session.getLastAssistantText();
    if (!text) {
      this.showError("No agent messages to copy yet.");
      return;
    }
    try {
      await copyToClipboard(text);
      if (options.flashConfirmation && this.ui instanceof TuiAltScreen3) {
        this.ui.flash("Copied!");
      } else {
        this.showStatus("Copied last agent message to clipboard");
      }
    } catch (error) {
      this.showError(error instanceof Error ? error.message : String(error));
    }
  }
  handleNameCommand(text) {
    const name = text.replace(/^\/name\s*/, "").trim();
    if (!name) {
      const currentName = this.sessionManager.getSessionName();
      if (currentName) {
        this.chatContainer.addChild(new Spacer27(1));
        this.chatContainer.addChild(new ThemedText(() => theme.fg("dim", `Session name: ${currentName}`), 1, 0));
      } else {
        this.showWarning("Usage: /name <name>");
      }
      this.ui.requestRender();
      return;
    }
    this.session.setSessionName(name);
    const sessionName = this.sessionManager.getSessionName();
    if (sessionName !== name) {
      this.showWarning(`Session name was normalized from ${JSON.stringify(name)} to ${JSON.stringify(sessionName)}`);
    }
    this.chatContainer.addChild(new Spacer27(1));
    const displayName = sessionName ?? name;
    this.chatContainer.addChild(new ThemedText(() => theme.fg("dim", `Session name set: ${displayName}`), 1, 0));
    this.ui.requestRender();
  }
  handleSessionCommand() {
    const stats = this.session.getSessionStats();
    const sessionName = this.sessionManager.getSessionName();
    const entries = this.sessionManager.getEntries();
    const cacheWaste = computeCacheWaste(entries, this.session.modelRuntime);
    const usageBreakdown = getUsageCostBreakdown(entries);
    const cacheWarmingStatus = this.session.cacheWarmingStatus;
    const cacheWarmingMode = this.settingsManager.getCacheWarmingMode();
    const model = this.session.model;
    const selectedModelKey = `${model?.provider}/${model?.id}`;
    const renderInfo = /* @__PURE__ */ __name(() => {
      let info = `${theme.bold("Session Info")}

`;
      if (sessionName) {
        info += `${theme.fg("dim", "Name:")} ${sessionName}
`;
      }
      info += `${theme.fg("dim", "File:")} ${stats.sessionFile ?? "In-memory"}
`;
      info += `${theme.fg("dim", "ID:")} ${stats.sessionId}

`;
      info += `${theme.bold("Messages")}
`;
      info += `${theme.fg("dim", "Total:")} ${stats.totalMessages}
`;
      info += `${theme.fg("dim", "User:")} ${stats.userMessages}
`;
      info += `${theme.fg("dim", "Assistant:")} ${stats.assistantMessages}
`;
      info += `${theme.fg("dim", "Tools:")} ${stats.toolCalls} calls, ${stats.toolResults} results

`;
      info += `${theme.bold("Tokens")}
`;
      const { input: input2, cacheRead, cacheWrite } = stats.tokens;
      const promptTokens = input2 + cacheRead + cacheWrite;
      info += `${theme.fg("dim", "Input:")} ${promptTokens.toLocaleString()}
`;
      if (promptTokens > 0 && (cacheRead > 0 || cacheWrite > 0)) {
        const hitRate = theme.fg("dim", `(${(cacheRead / promptTokens * 100).toFixed(1)}%)`);
        info += `  ${theme.fg("dim", "Cached:")} ${cacheRead.toLocaleString()} ${hitRate}
`;
        const written = cacheWrite > 0 ? ` ${theme.fg("dim", `(${cacheWrite.toLocaleString()} written to cache)`)}` : "";
        info += `  ${theme.fg("dim", "Uncached:")} ${(input2 + cacheWrite).toLocaleString()}${written}
`;
      }
      info += `${theme.fg("dim", "Output:")} ${stats.tokens.output.toLocaleString()}
`;
      info += `${theme.fg("dim", "Total:")} ${stats.tokens.total.toLocaleString()}
`;
      info += `
${theme.bold("Cache Warming")}
`;
      info += `${theme.fg("dim", "Mode:")} ${cacheWarmingMode}
`;
      info += `${theme.fg("dim", "Status:")} ${cacheWarmingStatus ? formatCacheWarmingStatus(cacheWarmingStatus) : "Inactive (cache warming unavailable)"}
`;
      const decision = cacheWarmingStatus?.decision;
      if (decision?.economicsAvailable) {
        info += `${theme.fg("dim", "Cache miss penalty:")} $${decision.missCost.toFixed(3)}
`;
        info += `${theme.fg("dim", "Refresh cost:")} $${decision.warmCost.toFixed(3)}
`;
      }
      if (stats.cost > 0 || cacheWaste.missedTokens > 0) {
        info += `
${theme.bold("Cost")}
`;
        info += `${theme.fg("dim", "Total:")} $${stats.cost.toFixed(3)}`;
        if (usageBreakdown.length > 1 || usageBreakdown[0]?.key !== selectedModelKey) {
          for (const entry of usageBreakdown) {
            info += `
  ${theme.fg("dim", `${entry.key}:`)} $${entry.cost.toFixed(3)} ${theme.fg("dim", `(${formatTokens(entry.tokens)} tokens)`)}`;
          }
        }
        if (cacheWaste.missedTokens > 0) {
          const missLabel = cacheWaste.missCount === 1 ? "1 miss" : `${cacheWaste.missCount} misses`;
          const detail = `${cacheWaste.missedTokens.toLocaleString()} tokens, ${missLabel}`;
          info += cacheWaste.missedCost >= 1e-4 ? `
${theme.fg("dim", "Cache Re-billed:")} $${cacheWaste.missedCost.toFixed(3)} ${theme.fg("dim", `(${detail})`)}` : `
${theme.fg("dim", "Cache Re-billed:")} ${detail}`;
        }
      }
      return info;
    }, "renderInfo");
    this.chatContainer.addChild(new Spacer27(1));
    this.chatContainer.addChild(new ThemedText(renderInfo, 1, 0));
    this.ui.requestRender();
  }
  handleChangelogCommand() {
    const changelogPath = getChangelogPath();
    const allEntries = parseChangelog(changelogPath);
    const changelogMarkdown = allEntries.length > 0 ? allEntries.reverse().map((e) => normalizeChangelogLinks(e.content, e)).join("\n\n") : "No changelog entries found.";
    this.chatContainer.addChild(new Spacer27(1));
    this.chatContainer.addChild(new DynamicBorder());
    this.chatContainer.addChild(new ThemedText(() => theme.bold(theme.fg("accent", "What's New")), 1, 0));
    this.chatContainer.addChild(new Spacer27(1));
    this.chatContainer.addChild(new Markdown7(changelogMarkdown, 1, 1, this.getMarkdownThemeWithSettings()));
    this.chatContainer.addChild(new DynamicBorder());
    this.ui.requestRender();
  }
  /**
   * Get capitalized display string for an app keybinding action.
   */
  getAppKeyDisplay(action) {
    return keyDisplayText(action);
  }
  /**
   * Get capitalized display string for an editor keybinding action.
   */
  getEditorKeyDisplay(action) {
    return keyDisplayText(action);
  }
  handleHotkeysCommand() {
    const cursorUp = this.getEditorKeyDisplay("tui.editor.cursorUp");
    const cursorDown = this.getEditorKeyDisplay("tui.editor.cursorDown");
    const cursorLeft = this.getEditorKeyDisplay("tui.editor.cursorLeft");
    const cursorRight = this.getEditorKeyDisplay("tui.editor.cursorRight");
    const cursorWordLeft = this.getEditorKeyDisplay("tui.editor.cursorWordLeft");
    const cursorWordRight = this.getEditorKeyDisplay("tui.editor.cursorWordRight");
    const cursorLineStart = this.getEditorKeyDisplay("tui.editor.cursorLineStart");
    const cursorLineEnd = this.getEditorKeyDisplay("tui.editor.cursorLineEnd");
    const jumpForward = this.getEditorKeyDisplay("tui.editor.jumpForward");
    const jumpBackward = this.getEditorKeyDisplay("tui.editor.jumpBackward");
    const pageUp = this.getEditorKeyDisplay("tui.editor.pageUp");
    const pageDown = this.getEditorKeyDisplay("tui.editor.pageDown");
    const submit = this.getEditorKeyDisplay("tui.input.submit");
    const newLine = this.getEditorKeyDisplay("tui.input.newLine");
    const deleteWordBackward = this.getEditorKeyDisplay("tui.editor.deleteWordBackward");
    const deleteWordForward = this.getEditorKeyDisplay("tui.editor.deleteWordForward");
    const deleteToLineStart = this.getEditorKeyDisplay("tui.editor.deleteToLineStart");
    const deleteToLineEnd = this.getEditorKeyDisplay("tui.editor.deleteToLineEnd");
    const yank = this.getEditorKeyDisplay("tui.editor.yank");
    const yankPop = this.getEditorKeyDisplay("tui.editor.yankPop");
    const undo = this.getEditorKeyDisplay("tui.editor.undo");
    const tab = this.getEditorKeyDisplay("tui.input.tab");
    const interrupt = this.getAppKeyDisplay("app.interrupt");
    const clear = this.getAppKeyDisplay("app.clear");
    const exit = this.getAppKeyDisplay("app.exit");
    const suspend = this.getAppKeyDisplay("app.suspend");
    const cycleThinkingLevel = this.getAppKeyDisplay("app.thinking.cycle");
    const cycleModelForward = this.getAppKeyDisplay("app.model.cycleForward");
    const selectModel = this.getAppKeyDisplay("app.model.select");
    const expandTools = this.getAppKeyDisplay("app.tools.expand");
    const toggleThinking = this.getAppKeyDisplay("app.thinking.toggle");
    const externalEditor = this.getAppKeyDisplay("app.editor.external");
    const cycleModelBackward = this.getAppKeyDisplay("app.model.cycleBackward");
    const copyMessage = this.getAppKeyDisplay("app.message.copy");
    const followUp = this.getAppKeyDisplay("app.message.followUp");
    const dequeue = this.getAppKeyDisplay("app.message.dequeue");
    const pasteImage = this.getAppKeyDisplay("app.clipboard.pasteImage");
    let hotkeys = `
**Navigation**
| Key | Action |
|-----|--------|
| \`${cursorUp}\` / \`${cursorDown}\` / \`${cursorLeft}\` / \`${cursorRight}\` | Move cursor / browse history |
| \`${cursorWordLeft}\` / \`${cursorWordRight}\` | Move by word |
| \`${cursorLineStart}\` | Start of line |
| \`${cursorLineEnd}\` | End of line |
| \`${jumpForward}\` | Jump forward to character |
| \`${jumpBackward}\` | Jump backward to character |
| \`${pageUp}\` / \`${pageDown}\` | Scroll by page |

**Editing**
| Key | Action |
|-----|--------|
| \`${submit}\` | Send message |
| \`${newLine}\` | New line${process.platform === "win32" ? " (Ctrl+Enter on Windows Terminal)" : ""} |
| \`${deleteWordBackward}\` | Delete word backwards |
| \`${deleteWordForward}\` | Delete word forwards |
| \`${deleteToLineStart}\` | Delete to start of line |
| \`${deleteToLineEnd}\` | Delete to end of line |
| \`${yank}\` | Paste the most-recently-deleted text |
| \`${yankPop}\` | Cycle through the deleted text after pasting |
| \`${undo}\` | Undo |

**Other**
| Key | Action |
|-----|--------|
| \`${tab}\` | Path completion / accept autocomplete |
| \`${interrupt}\` | Cancel autocomplete / abort streaming |
| \`${clear}\` | Clear editor (first) / exit (second) |
| \`${exit}\` | Exit (when editor is empty) |
| \`${suspend}\` | Suspend to background |
| \`${cycleThinkingLevel}\` | Cycle thinking level |
| \`${cycleModelForward}\` / \`${cycleModelBackward}\` | Cycle models |
| \`${selectModel}\` | Open model selector |
| \`${expandTools}\` | Toggle tool output expansion |
| \`${toggleThinking}\` | Toggle thinking block visibility |
| \`${externalEditor}\` | Edit message in external editor |
| \`${copyMessage}\` | Copy selection or last assistant message |
| \`${followUp}\` | Queue follow-up message |
| \`${dequeue}\` | Restore queued messages |
| \`${pasteImage}\` | Paste files on macOS, images, or text from clipboard |
| \`/\` | Slash commands |
| \`!\` | Run bash command |
| \`!!\` | Run bash command (excluded from context) |
`;
    const extensionRunner = this.session.extensionRunner;
    const shortcuts = extensionRunner.getShortcuts(this.keybindings.getEffectiveConfig());
    if (shortcuts.size > 0) {
      hotkeys += `
**Extensions**
| Key | Action |
|-----|--------|
`;
      for (const [key, shortcut] of shortcuts) {
        const description = shortcut.description ?? shortcut.extensionPath;
        const keyDisplay = formatKeyText(key, { capitalize: true });
        hotkeys += `| \`${keyDisplay}\` | ${description} |
`;
      }
    }
    this.chatContainer.addChild(new Spacer27(1));
    this.chatContainer.addChild(new DynamicBorder());
    this.chatContainer.addChild(new ThemedText(() => theme.bold(theme.fg("accent", "Keyboard Shortcuts")), 1, 0));
    this.chatContainer.addChild(new Spacer27(1));
    this.chatContainer.addChild(new Markdown7(hotkeys.trim(), 1, 1, this.getMarkdownThemeWithSettings()));
    this.chatContainer.addChild(new DynamicBorder());
    this.ui.requestRender();
  }
  async handleClearCommand() {
    this.clearStatusIndicator();
    try {
      const result = await this.runtimeHost.newSession();
      if (result.cancelled) {
        return;
      }
      this.chatContainer.addChild(new Spacer27(1));
      this.chatContainer.addChild(new ThemedText(() => theme.fg("accent", "\u2713 New session started"), 1, 1));
      this.ui.requestRender();
    } catch (error) {
      await this.handleFatalRuntimeError("Failed to create session", error);
    }
  }
  handleDebugCommand() {
    const width = this.ui.terminal.columns;
    const height = this.ui.terminal.rows;
    const allLines = this.ui.render(width);
    const debugLogPath = getDebugLogPath();
    const debugData = [
      `Debug output at ${(/* @__PURE__ */ new Date()).toISOString()}`,
      `Terminal: ${width}x${height}`,
      `Total lines: ${allLines.length}`,
      "",
      "=== All rendered lines with visible widths ===",
      ...allLines.map((line, idx) => {
        const vw = visibleWidth7(line);
        const escaped = JSON.stringify(line);
        return `[${idx}] (w=${vw}) ${escaped}`;
      }),
      "",
      "=== Agent messages (JSONL) ===",
      ...this.session.messages.map((msg) => JSON.stringify(msg)),
      ""
    ].join("\n");
    fs3.mkdirSync(path4.dirname(debugLogPath), { recursive: true });
    fs3.writeFileSync(debugLogPath, debugData);
    this.chatContainer.addChild(new Spacer27(1));
    this.chatContainer.addChild(new ThemedText(() => `${theme.fg("accent", "\u2713 Debug log written")}
${theme.fg("muted", debugLogPath)}`, 1, 1));
    this.ui.requestRender();
  }
  handleArminSaysHi() {
    if (playArmin3d(this.renderer))
      return;
    this.chatContainer.addChild(new Spacer27(1));
    this.chatContainer.addChild(new ArminComponent(this.ui));
    this.ui.requestRender();
  }
  handleDementedDelves() {
    this.chatContainer.addChild(new Spacer27(1));
    this.chatContainer.addChild(new EarendilAnnouncementComponent());
    this.ui.requestRender();
  }
  async handleBashCommand(command, excludeFromContext = false) {
    const extensionRunner = this.session.extensionRunner;
    let eventResult;
    try {
      eventResult = await extensionRunner.emitUserBash({
        type: "user_bash",
        command,
        excludeFromContext,
        cwd: this.sessionManager.getCwd()
      });
    } catch {
      return;
    }
    if (eventResult?.result) {
      const result = eventResult.result;
      this.bashComponent = new BashExecutionComponent(command, this.ui, excludeFromContext, this.outputPad);
      if (this.session.isStreaming) {
        this.pendingMessagesContainer.addChild(this.bashComponent);
        this.pendingBashComponents.push(this.bashComponent);
      } else {
        this.chatContainer.addChild(this.bashComponent);
      }
      if (result.output) {
        this.bashComponent.appendOutput(result.output);
      }
      this.bashComponent.setComplete(result.exitCode, result.cancelled, result.truncated ? { truncated: true, content: result.output } : void 0, result.fullOutputPath);
      this.session.recordBashResult(command, result, { excludeFromContext });
      this.bashComponent = void 0;
      this.ui.requestRender();
      return;
    }
    const isDeferred = this.session.isStreaming;
    this.bashComponent = new BashExecutionComponent(command, this.ui, excludeFromContext, this.outputPad);
    if (isDeferred) {
      this.pendingMessagesContainer.addChild(this.bashComponent);
      this.pendingBashComponents.push(this.bashComponent);
    } else {
      this.chatContainer.addChild(this.bashComponent);
    }
    this.ui.requestRender();
    try {
      const result = await this.session.executeBash(command, (chunk) => {
        if (this.bashComponent) {
          this.bashComponent.appendOutput(chunk);
          this.ui.requestRender();
        }
      }, { excludeFromContext, operations: eventResult?.operations });
      if (this.bashComponent) {
        this.bashComponent.setComplete(result.exitCode, result.cancelled, result.truncated ? { truncated: true, content: result.output } : void 0, result.fullOutputPath);
      }
    } catch (error) {
      if (this.bashComponent) {
        this.bashComponent.setComplete(void 0, false);
      }
      this.showError(`Bash command failed: ${error instanceof Error ? error.message : "Unknown error"}`);
    }
    this.bashComponent = void 0;
    this.ui.requestRender();
  }
  async handleCompactCommand(customInstructions) {
    this.clearStatusIndicator();
    try {
      await this.session.compact(customInstructions);
    } catch {
    }
  }
  stop(fullscreenExitOutput = this.settingsManager.getFullscreenExitOutput()) {
    this.disposeActiveSelector();
    if (this.settingsManager.getShowTerminalProgress()) {
      this.ui.terminal.setProgress(false);
    }
    this.clearStatusIndicator();
    this.themeController.disableAutoSync();
    this.clearExtensionTerminalInputListeners();
    this.footer.dispose();
    this.footerDataProvider.dispose();
    if (this.unsubscribe) {
      this.unsubscribe();
    }
    if (this.isInitialized) {
      this.stopInteractiveTui(fullscreenExitOutput);
      this.isInitialized = false;
    }
    this.unregisterSignalHandlers();
  }
};

// pi-dist/pi-coding-agent/modes/json-event.js
function toJsonAssistantMessageEvent(event) {
  if (event.type === "toolcall_start") {
    const toolCall = event.partial.content[event.contentIndex];
    if (toolCall?.type !== "toolCall") {
      throw new Error(`toolcall_start content at index ${event.contentIndex} is not a tool call`);
    }
    const { partial: _partial2, ...deltaEvent2 } = event;
    return { ...deltaEvent2, id: toolCall.id, toolName: toolCall.name };
  }
  if (!("partial" in event)) {
    return event;
  }
  const { partial: _partial, ...deltaEvent } = event;
  return deltaEvent;
}
__name(toJsonAssistantMessageEvent, "toJsonAssistantMessageEvent");
function toJsonEvent(event) {
  if (event.type !== "message_update") {
    return event;
  }
  if (event.message.role !== "assistant") {
    throw new Error("message_update message is not an assistant message");
  }
  return {
    type: "message_update",
    usage: event.message.usage,
    assistantMessageEvent: toJsonAssistantMessageEvent(event.assistantMessageEvent)
  };
}
__name(toJsonEvent, "toJsonEvent");

// pi-dist/pi-coding-agent/modes/print-mode.js
async function runPrintMode(runtimeHost, options) {
  const { mode, messages = [], initialMessage, initialImages } = options;
  let exitCode = 0;
  let session = runtimeHost.session;
  let unsubscribe;
  let unsubscribeBackpressure;
  let disposed = false;
  const signalCleanupHandlers = [];
  const disposeRuntime = /* @__PURE__ */ __name(async () => {
    if (disposed)
      return;
    disposed = true;
    unsubscribe?.();
    unsubscribeBackpressure?.();
    await runtimeHost.dispose();
  }, "disposeRuntime");
  const registerSignalHandlers = /* @__PURE__ */ __name(() => {
    const signals = ["SIGTERM"];
    if (process.platform !== "win32") {
      signals.push("SIGHUP");
    }
    for (const signal of signals) {
      const handler = /* @__PURE__ */ __name(() => {
        killTrackedDetachedChildren();
        void disposeRuntime().finally(() => {
          process.exit(signal === "SIGHUP" ? 129 : 143);
        });
      }, "handler");
      process.on(signal, handler);
      signalCleanupHandlers.push(() => process.off(signal, handler));
    }
  }, "registerSignalHandlers");
  registerSignalHandlers();
  runtimeHost.setRebindSession(async () => {
    await rebindSession();
  });
  const rebindSession = /* @__PURE__ */ __name(async () => {
    session = runtimeHost.session;
    await session.bindExtensions({
      mode: mode === "json" ? "json" : "print",
      commandContextActions: {
        waitForIdle: /* @__PURE__ */ __name(() => session.waitForIdle(), "waitForIdle"),
        newSession: /* @__PURE__ */ __name(async (newSessionOptions) => runtimeHost.newSession(newSessionOptions), "newSession"),
        fork: /* @__PURE__ */ __name(async (entryId, forkOptions) => {
          const result = await runtimeHost.fork(entryId, forkOptions);
          return { cancelled: result.cancelled };
        }, "fork"),
        navigateTree: /* @__PURE__ */ __name(async (targetId, navigateOptions) => {
          const result = await session.navigateTree(targetId, {
            summarize: navigateOptions?.summarize,
            customInstructions: navigateOptions?.customInstructions,
            replaceInstructions: navigateOptions?.replaceInstructions,
            label: navigateOptions?.label
          });
          return { cancelled: result.cancelled };
        }, "navigateTree"),
        switchSession: /* @__PURE__ */ __name(async (sessionPath, switchOptions) => {
          return runtimeHost.switchSession(sessionPath, switchOptions);
        }, "switchSession"),
        reload: /* @__PURE__ */ __name(async () => {
          await session.reload();
        }, "reload")
      },
      onError: /* @__PURE__ */ __name((err) => {
        console.error(`Extension error (${err.extensionPath}): ${err.error}`);
      }, "onError")
    });
    unsubscribe?.();
    unsubscribeBackpressure?.();
    unsubscribe = session.subscribe((event) => {
      if (mode === "json") {
        writeRawStdout(`${JSON.stringify(toJsonEvent(event))}
`);
      }
    });
    unsubscribeBackpressure = mode === "json" ? session.agent.subscribe(async () => {
      await waitForRawStdoutBackpressure();
    }) : void 0;
  }, "rebindSession");
  try {
    if (mode === "json") {
      const header = session.sessionManager.getHeader();
      if (header) {
        writeRawStdout(`${JSON.stringify(header)}
`);
      }
    }
    await rebindSession();
    if (initialMessage) {
      await session.prompt(initialMessage, { images: initialImages });
    }
    for (const message of messages) {
      await session.prompt(message);
    }
    if (mode === "text") {
      const state = session.state;
      const lastMessage = state.messages[state.messages.length - 1];
      if (lastMessage?.role === "assistant") {
        const assistantMsg = lastMessage;
        if (assistantMsg.stopReason === "error" || assistantMsg.stopReason === "aborted") {
          console.error(assistantMsg.errorMessage || `Request ${assistantMsg.stopReason}`);
          exitCode = 1;
        } else {
          for (const content of assistantMsg.content) {
            if (content.type === "text") {
              writeRawStdout(`${content.text}
`);
            }
          }
        }
      }
    }
    return exitCode;
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    return 1;
  } finally {
    for (const cleanup of signalCleanupHandlers) {
      cleanup();
    }
    await disposeRuntime();
    await flushRawStdout();
  }
}
__name(runPrintMode, "runPrintMode");

// pi-dist/pi-coding-agent/modes/rpc/rpc-client.js
import { spawn as spawn5 } from "node:child_process";

// pi-dist/pi-coding-agent/modes/rpc/jsonl.js
import { StringDecoder } from "node:string_decoder";
function serializeJsonLine(value) {
  return `${JSON.stringify(value)}
`;
}
__name(serializeJsonLine, "serializeJsonLine");
function attachJsonlLineReader(stream2, onLine) {
  const decoder = new StringDecoder("utf8");
  let buffer = "";
  const emitLine = /* @__PURE__ */ __name((line) => {
    onLine(line.endsWith("\r") ? line.slice(0, -1) : line);
  }, "emitLine");
  const onData = /* @__PURE__ */ __name((chunk) => {
    buffer += typeof chunk === "string" ? chunk : decoder.write(chunk);
    while (true) {
      const newlineIndex = buffer.indexOf("\n");
      if (newlineIndex === -1) {
        return;
      }
      emitLine(buffer.slice(0, newlineIndex));
      buffer = buffer.slice(newlineIndex + 1);
    }
  }, "onData");
  const onEnd = /* @__PURE__ */ __name(() => {
    buffer += decoder.end();
    if (buffer.length > 0) {
      emitLine(buffer);
      buffer = "";
    }
  }, "onEnd");
  stream2.on("data", onData);
  stream2.on("end", onEnd);
  return () => {
    stream2.off("data", onData);
    stream2.off("end", onEnd);
  };
}
__name(attachJsonlLineReader, "attachJsonlLineReader");

// pi-dist/pi-coding-agent/modes/rpc/rpc-client.js
var RpcClient = class {
  static {
    __name(this, "RpcClient");
  }
  process = null;
  stopReadingStdout = null;
  eventListeners = [];
  pendingRequests = /* @__PURE__ */ new Map();
  requestId = 0;
  stderr = "";
  exitError = null;
  options;
  constructor(options = {}) {
    this.options = options;
  }
  /**
   * Start the RPC agent process.
   */
  async start() {
    if (this.process) {
      throw new Error("Client already started");
    }
    this.exitError = null;
    const cliPath = this.options.cliPath ?? "dist/cli.js";
    const args = ["--mode", "rpc"];
    if (this.options.provider) {
      args.push("--provider", this.options.provider);
    }
    if (this.options.model) {
      args.push("--model", this.options.model);
    }
    if (this.options.args) {
      args.push(...this.options.args);
    }
    const childProcess = spawn5("node", [cliPath, ...args], {
      cwd: this.options.cwd,
      env: { ...process.env, ...this.options.env },
      stdio: ["pipe", "pipe", "pipe"]
    });
    this.process = childProcess;
    childProcess.stderr?.on("data", (data) => {
      this.stderr += data.toString();
      process.stderr.write(data);
    });
    childProcess.once("exit", (code, signal) => {
      if (this.process !== childProcess)
        return;
      const error = this.createProcessExitError(code, signal);
      this.exitError = error;
      this.rejectPendingRequests(error);
    });
    childProcess.once("error", (error) => {
      if (this.process !== childProcess)
        return;
      const processError = new Error(`Agent process error: ${error.message}. Stderr: ${this.stderr}`);
      this.exitError = processError;
      this.rejectPendingRequests(processError);
    });
    childProcess.stdin?.on("error", (error) => {
      if (this.process !== childProcess)
        return;
      const stdinError = this.exitError ?? new Error(`Agent process stdin error: ${error.message}. Stderr: ${this.stderr}`);
      this.exitError = stdinError;
      this.rejectPendingRequests(stdinError);
    });
    this.stopReadingStdout = attachJsonlLineReader(childProcess.stdout, (line) => {
      this.handleLine(line);
    });
    await new Promise((resolve7) => setTimeout(resolve7, 100));
    if (this.process.exitCode !== null) {
      const error = this.exitError ?? this.createProcessExitError(this.process.exitCode, this.process.signalCode);
      this.exitError = error;
      throw error;
    }
  }
  /**
   * Stop the RPC agent process.
   */
  async stop() {
    if (!this.process)
      return;
    this.stopReadingStdout?.();
    this.stopReadingStdout = null;
    this.process.kill("SIGTERM");
    await new Promise((resolve7) => {
      const timeout = setTimeout(() => {
        this.process?.kill("SIGKILL");
        resolve7();
      }, 1e3);
      this.process?.on("exit", () => {
        clearTimeout(timeout);
        resolve7();
      });
    });
    this.process = null;
    this.pendingRequests.clear();
  }
  /**
   * Subscribe to agent events.
   */
  onEvent(listener) {
    this.eventListeners.push(listener);
    return () => {
      const index = this.eventListeners.indexOf(listener);
      if (index !== -1) {
        this.eventListeners.splice(index, 1);
      }
    };
  }
  /**
   * Get collected stderr output (useful for debugging).
   */
  getStderr() {
    return this.stderr;
  }
  // =========================================================================
  // Command Methods
  // =========================================================================
  /**
   * Send a prompt to the agent.
   * Returns the prompt's disposition after acceptance; use onEvent() to receive streaming events.
   * If the disposition is "handled", no run started for this prompt, so don't wait for agent_settled.
   */
  async prompt(message, images, streamingBehavior) {
    const response = await this.send({ type: "prompt", message, images, streamingBehavior });
    return this.getData(response).disposition;
  }
  /**
   * Queue a steering message to interrupt the agent mid-run.
   */
  async steer(message, images) {
    const response = await this.send({ type: "steer", message, images });
    return this.getData(response).disposition;
  }
  /**
   * Queue a follow-up message to be processed after the agent finishes.
   */
  async followUp(message, images) {
    const response = await this.send({ type: "follow_up", message, images });
    return this.getData(response).disposition;
  }
  /**
   * Abort current operation.
   */
  async abort() {
    await this.send({ type: "abort" });
  }
  /**
   * Clear queued steering and follow-up messages, returning their text.
   */
  async clearQueue() {
    const response = await this.send({ type: "clear_queue" });
    return this.getData(response);
  }
  /**
   * Start a new session, optionally with parent tracking.
   * @param parentSession - Optional parent session path for lineage tracking
   * @returns Object with `cancelled: true` if an extension cancelled the new session
   */
  async newSession(parentSession) {
    const response = await this.send({ type: "new_session", parentSession });
    return this.getData(response);
  }
  /**
   * Get current session state.
   */
  async getState() {
    const response = await this.send({ type: "get_state" });
    return this.getData(response);
  }
  /**
   * Set model by provider and ID.
   */
  async setModel(provider, modelId) {
    const response = await this.send({ type: "set_model", provider, modelId });
    return this.getData(response);
  }
  /**
   * Cycle to next model.
   */
  async cycleModel() {
    const response = await this.send({ type: "cycle_model" });
    return this.getData(response);
  }
  /**
   * Get list of available models.
   */
  async getAvailableModels() {
    const response = await this.send({ type: "get_available_models" });
    return this.getData(response).models;
  }
  /**
   * Set thinking level.
   */
  async setThinkingLevel(level) {
    await this.send({ type: "set_thinking_level", level });
  }
  /**
   * Cycle thinking level.
   */
  async cycleThinkingLevel() {
    const response = await this.send({ type: "cycle_thinking_level" });
    return this.getData(response);
  }
  /**
   * Get list of available thinking levels for the current model.
   */
  async getAvailableThinkingLevels() {
    const response = await this.send({ type: "get_available_thinking_levels" });
    return this.getData(response).levels;
  }
  /**
   * Set steering mode.
   */
  async setSteeringMode(mode) {
    await this.send({ type: "set_steering_mode", mode });
  }
  /**
   * Set follow-up mode.
   */
  async setFollowUpMode(mode) {
    await this.send({ type: "set_follow_up_mode", mode });
  }
  /**
   * Compact session context.
   */
  async compact(customInstructions) {
    const response = await this.send({ type: "compact", customInstructions });
    return this.getData(response);
  }
  /**
   * Set auto-compaction enabled/disabled.
   */
  async setAutoCompaction(enabled) {
    await this.send({ type: "set_auto_compaction", enabled });
  }
  /**
   * Set auto-retry enabled/disabled.
   */
  async setAutoRetry(enabled) {
    await this.send({ type: "set_auto_retry", enabled });
  }
  /**
   * Abort in-progress retry.
   */
  async abortRetry() {
    await this.send({ type: "abort_retry" });
  }
  /**
   * Execute a bash command.
   */
  async bash(command) {
    const response = await this.send({ type: "bash", command });
    return this.getData(response);
  }
  /**
   * Abort running bash command.
   */
  async abortBash() {
    await this.send({ type: "abort_bash" });
  }
  /**
   * Get session statistics.
   */
  async getSessionStats() {
    const response = await this.send({ type: "get_session_stats" });
    return this.getData(response);
  }
  /**
   * Export session to HTML.
   */
  async exportHtml(outputPath) {
    const response = await this.send({ type: "export_html", outputPath });
    return this.getData(response);
  }
  /**
   * Switch to a different session file.
   * @returns Object with `cancelled: true` if an extension cancelled the switch
   */
  async switchSession(sessionPath) {
    const response = await this.send({ type: "switch_session", sessionPath });
    return this.getData(response);
  }
  /**
   * Fork from a specific message.
   * @returns Object with `text` (the message text) and `cancelled` (if extension cancelled)
   */
  async fork(entryId) {
    const response = await this.send({ type: "fork", entryId });
    return this.getData(response);
  }
  /**
   * Clone the current active branch into a new session.
   * @returns Object with `cancelled: true` if an extension cancelled the clone
   */
  async clone() {
    const response = await this.send({ type: "clone" });
    return this.getData(response);
  }
  /**
   * Get messages available for forking.
   */
  async getForkMessages() {
    const response = await this.send({ type: "get_fork_messages" });
    return this.getData(response).messages;
  }
  /**
   * Get session entries in append order, optionally only those after the `since` entry id.
   */
  async getEntries(since) {
    const response = await this.send({ type: "get_entries", since });
    return this.getData(response);
  }
  /**
   * Get the session entry tree.
   */
  async getTree() {
    const response = await this.send({ type: "get_tree" });
    return this.getData(response);
  }
  /**
   * Get text of last assistant message.
   */
  async getLastAssistantText() {
    const response = await this.send({ type: "get_last_assistant_text" });
    return this.getData(response).text;
  }
  /**
   * Set the session display name.
   */
  async setSessionName(name) {
    await this.send({ type: "set_session_name", name });
  }
  /**
   * Get all messages in the session.
   */
  async getMessages() {
    const response = await this.send({ type: "get_messages" });
    return this.getData(response).messages;
  }
  /**
   * Get available commands (extension commands, prompt templates, skills).
   */
  async getCommands() {
    const response = await this.send({ type: "get_commands" });
    return this.getData(response).commands;
  }
  // =========================================================================
  // Helpers
  // =========================================================================
  /**
   * Wait for agent to become idle (no streaming).
   * Resolves when agent_settled event is received.
   */
  waitForIdle(timeout = 6e4) {
    return new Promise((resolve7, reject) => {
      const timer = setTimeout(() => {
        unsubscribe();
        reject(new Error(`Timeout waiting for agent to become idle. Stderr: ${this.stderr}`));
      }, timeout);
      const unsubscribe = this.onEvent((event) => {
        if (event.type === "agent_settled") {
          clearTimeout(timer);
          unsubscribe();
          resolve7();
        }
      });
    });
  }
  /**
   * Collect events until agent becomes idle.
   */
  collectEvents(timeout = 6e4) {
    return new Promise((resolve7, reject) => {
      const events = [];
      const timer = setTimeout(() => {
        unsubscribe();
        reject(new Error(`Timeout collecting events. Stderr: ${this.stderr}`));
      }, timeout);
      const unsubscribe = this.onEvent((event) => {
        events.push(event);
        if (event.type === "agent_settled") {
          clearTimeout(timer);
          unsubscribe();
          resolve7(events);
        }
      });
    });
  }
  /**
   * Send prompt and wait for completion, returning all events.
   */
  async promptAndWait(message, images, timeout = 6e4) {
    const eventsPromise = this.collectEvents(timeout);
    await this.prompt(message, images);
    return eventsPromise;
  }
  // =========================================================================
  // Internal
  // =========================================================================
  handleLine(line) {
    try {
      const data = JSON.parse(line);
      if (data.type === "response" && data.id && this.pendingRequests.has(data.id)) {
        const pending = this.pendingRequests.get(data.id);
        this.pendingRequests.delete(data.id);
        pending.resolve(data);
        return;
      }
      for (const listener of [...this.eventListeners]) {
        listener(data);
      }
    } catch {
    }
  }
  createProcessExitError(code, signal) {
    return new Error(`Agent process exited (code=${code} signal=${signal}). Stderr: ${this.stderr}`);
  }
  rejectPendingRequests(error) {
    for (const pending of this.pendingRequests.values()) {
      pending.reject(error);
    }
    this.pendingRequests.clear();
  }
  async send(command) {
    const childProcess = this.process;
    const stdin = childProcess?.stdin;
    if (!childProcess || !stdin) {
      throw new Error("Client not started");
    }
    if (this.exitError) {
      throw this.exitError;
    }
    if (childProcess.exitCode !== null) {
      const error = this.createProcessExitError(childProcess.exitCode, childProcess.signalCode);
      this.exitError = error;
      throw error;
    }
    if (stdin.destroyed || !stdin.writable) {
      const error = new Error(`Agent process stdin is not writable. Stderr: ${this.stderr}`);
      this.exitError = error;
      throw error;
    }
    const id = `req_${++this.requestId}`;
    const fullCommand = { ...command, id };
    return new Promise((resolve7, reject) => {
      const timeout = setTimeout(() => {
        this.pendingRequests.delete(id);
        reject(new Error(`Timeout waiting for response to ${command.type}. Stderr: ${this.stderr}`));
      }, 3e4);
      this.pendingRequests.set(id, {
        resolve: /* @__PURE__ */ __name((response) => {
          clearTimeout(timeout);
          resolve7(response);
        }, "resolve"),
        reject: /* @__PURE__ */ __name((error) => {
          clearTimeout(timeout);
          reject(error);
        }, "reject")
      });
      try {
        stdin.write(serializeJsonLine(fullCommand));
      } catch (error) {
        const writeError = error instanceof Error ? error : new Error(String(error));
        const pending = this.pendingRequests.get(id);
        this.pendingRequests.delete(id);
        pending?.reject(writeError);
      }
    });
  }
  getData(response) {
    if (!response.success) {
      const errorResponse = response;
      throw new Error(errorResponse.error);
    }
    const successResponse = response;
    return successResponse.data;
  }
};

// pi-dist/pi-coding-agent/modes/rpc/rpc-mode.js
import * as crypto3 from "node:crypto";
async function runRpcMode(runtimeHost) {
  takeOverStdout();
  let session = runtimeHost.session;
  let unsubscribe;
  let unsubscribeBackpressure;
  const output = /* @__PURE__ */ __name((obj) => {
    writeRawStdout(serializeJsonLine(obj));
  }, "output");
  const success = /* @__PURE__ */ __name((id, command, data) => {
    if (data === void 0) {
      return { id, type: "response", command, success: true };
    }
    return { id, type: "response", command, success: true, data };
  }, "success");
  const error = /* @__PURE__ */ __name((id, command, message) => {
    return { id, type: "response", command, success: false, error: message };
  }, "error");
  const pendingExtensionRequests = /* @__PURE__ */ new Map();
  let shutdownRequested = false;
  let shuttingDown = false;
  const signalCleanupHandlers = [];
  function createDialogPromise(opts, defaultValue, request, parseResponse) {
    if (opts?.signal?.aborted)
      return Promise.resolve(defaultValue);
    const id = crypto3.randomUUID();
    return new Promise((resolve7, reject) => {
      let timeoutId;
      const cleanup = /* @__PURE__ */ __name(() => {
        if (timeoutId)
          clearTimeout(timeoutId);
        opts?.signal?.removeEventListener("abort", onAbort);
        pendingExtensionRequests.delete(id);
      }, "cleanup");
      const onAbort = /* @__PURE__ */ __name(() => {
        cleanup();
        resolve7(defaultValue);
      }, "onAbort");
      opts?.signal?.addEventListener("abort", onAbort, { once: true });
      if (opts?.timeout) {
        timeoutId = setTimeout(() => {
          cleanup();
          resolve7(defaultValue);
        }, opts.timeout);
      }
      pendingExtensionRequests.set(id, {
        resolve: /* @__PURE__ */ __name((response) => {
          cleanup();
          resolve7(parseResponse(response));
        }, "resolve"),
        reject
      });
      output({ type: "extension_ui_request", id, ...request });
    });
  }
  __name(createDialogPromise, "createDialogPromise");
  const createExtensionUIContext = /* @__PURE__ */ __name(() => ({
    select: /* @__PURE__ */ __name((title, options, opts) => createDialogPromise(opts, void 0, { method: "select", title, options, timeout: opts?.timeout }, (r) => "cancelled" in r && r.cancelled ? void 0 : "value" in r ? r.value : void 0), "select"),
    confirm: /* @__PURE__ */ __name((title, message, opts) => createDialogPromise(opts, false, { method: "confirm", title, message, timeout: opts?.timeout }, (r) => "cancelled" in r && r.cancelled ? false : "confirmed" in r ? r.confirmed : false), "confirm"),
    input: /* @__PURE__ */ __name((title, placeholder, opts) => createDialogPromise(opts, void 0, { method: "input", title, placeholder, timeout: opts?.timeout }, (r) => "cancelled" in r && r.cancelled ? void 0 : "value" in r ? r.value : void 0), "input"),
    notify(message, type) {
      output({
        type: "extension_ui_request",
        id: crypto3.randomUUID(),
        method: "notify",
        message,
        notifyType: type
      });
    },
    onTerminalInput() {
      return () => {
      };
    },
    setStatus(key, text) {
      output({
        type: "extension_ui_request",
        id: crypto3.randomUUID(),
        method: "setStatus",
        statusKey: key,
        statusText: text
      });
    },
    setWorkingMessage(_message) {
    },
    setWorkingVisible(_visible) {
    },
    setWorkingIndicator(_options) {
    },
    setHiddenThinkingLabel(_label) {
    },
    setWidget(key, content, options) {
      if (content === void 0 || Array.isArray(content)) {
        output({
          type: "extension_ui_request",
          id: crypto3.randomUUID(),
          method: "setWidget",
          widgetKey: key,
          widgetLines: content,
          widgetPlacement: options?.placement
        });
      }
    },
    setFooter(_factory) {
    },
    setHeader(_factory) {
    },
    setTitle(title) {
      output({
        type: "extension_ui_request",
        id: crypto3.randomUUID(),
        method: "setTitle",
        title
      });
    },
    async custom() {
      return void 0;
    },
    pasteToEditor(text) {
      this.setEditorText(text);
    },
    setEditorText(text) {
      output({
        type: "extension_ui_request",
        id: crypto3.randomUUID(),
        method: "set_editor_text",
        text
      });
    },
    getEditorText() {
      return "";
    },
    async editor(title, prefill) {
      const id = crypto3.randomUUID();
      return new Promise((resolve7, reject) => {
        pendingExtensionRequests.set(id, {
          resolve: /* @__PURE__ */ __name((response) => {
            if ("cancelled" in response && response.cancelled) {
              resolve7(void 0);
            } else if ("value" in response) {
              resolve7(response.value);
            } else {
              resolve7(void 0);
            }
          }, "resolve"),
          reject
        });
        output({ type: "extension_ui_request", id, method: "editor", title, prefill });
      });
    },
    addAutocompleteProvider() {
    },
    setEditorComponent() {
    },
    getEditorComponent() {
      return void 0;
    },
    get theme() {
      return theme;
    },
    getAllThemes() {
      return [];
    },
    getTheme(_name) {
      return void 0;
    },
    setTheme(_theme) {
      return { success: false, error: "Theme switching not supported in RPC mode" };
    },
    getToolsExpanded() {
      return false;
    },
    setToolsExpanded(_expanded) {
    }
  }), "createExtensionUIContext");
  runtimeHost.setRebindSession(async () => {
    await rebindSession();
  });
  const rebindSession = /* @__PURE__ */ __name(async () => {
    session = runtimeHost.session;
    await session.bindExtensions({
      uiContext: createExtensionUIContext(),
      mode: "rpc",
      commandContextActions: {
        waitForIdle: /* @__PURE__ */ __name(() => session.waitForIdle(), "waitForIdle"),
        newSession: /* @__PURE__ */ __name(async (options) => runtimeHost.newSession(options), "newSession"),
        fork: /* @__PURE__ */ __name(async (entryId, forkOptions) => {
          const result = await runtimeHost.fork(entryId, forkOptions);
          return { cancelled: result.cancelled };
        }, "fork"),
        navigateTree: /* @__PURE__ */ __name(async (targetId, options) => {
          const result = await session.navigateTree(targetId, {
            summarize: options?.summarize,
            customInstructions: options?.customInstructions,
            replaceInstructions: options?.replaceInstructions,
            label: options?.label
          });
          return { cancelled: result.cancelled };
        }, "navigateTree"),
        switchSession: /* @__PURE__ */ __name(async (sessionPath, options) => {
          return runtimeHost.switchSession(sessionPath, options);
        }, "switchSession"),
        reload: /* @__PURE__ */ __name(async () => {
          await session.reload();
        }, "reload")
      },
      shutdownHandler: /* @__PURE__ */ __name(() => {
        shutdownRequested = true;
      }, "shutdownHandler"),
      onError: /* @__PURE__ */ __name((err) => {
        output({ type: "extension_error", extensionPath: err.extensionPath, event: err.event, error: err.error });
      }, "onError")
    });
    unsubscribe?.();
    unsubscribeBackpressure?.();
    unsubscribe = session.subscribe((event) => {
      output(toJsonEvent(event));
      if (event.type === "agent_settled") {
        void checkShutdownRequested();
      }
    });
    unsubscribeBackpressure = session.agent.subscribe(async () => {
      await waitForRawStdoutBackpressure();
    });
  }, "rebindSession");
  const registerSignalHandlers = /* @__PURE__ */ __name(() => {
    const signals = ["SIGTERM"];
    if (process.platform !== "win32") {
      signals.push("SIGHUP");
    }
    for (const signal of signals) {
      const handler = /* @__PURE__ */ __name(() => {
        killTrackedDetachedChildren();
        void shutdown(signal === "SIGHUP" ? 129 : 143, signal);
      }, "handler");
      process.on(signal, handler);
      signalCleanupHandlers.push(() => process.off(signal, handler));
    }
  }, "registerSignalHandlers");
  await rebindSession();
  registerSignalHandlers();
  const handleCommand = /* @__PURE__ */ __name(async (command) => {
    const id = command.id;
    switch (command.type) {
      // =================================================================
      // Prompting
      // =================================================================
      case "prompt": {
        let preflightSucceeded = false;
        void session.prompt(command.message, {
          images: command.images,
          streamingBehavior: command.streamingBehavior,
          source: "rpc",
          preflightResult: /* @__PURE__ */ __name((disposition) => {
            preflightSucceeded = true;
            output(success(id, "prompt", { disposition }));
          }, "preflightResult")
        }).catch((e) => {
          if (!preflightSucceeded) {
            output(error(id, "prompt", e.message));
          }
        });
        return void 0;
      }
      case "steer": {
        const disposition = await session.steer(command.message, command.images, { source: "rpc" });
        return success(id, "steer", { disposition });
      }
      case "follow_up": {
        const disposition = await session.followUp(command.message, command.images, { source: "rpc" });
        return success(id, "follow_up", { disposition });
      }
      case "abort": {
        await session.abort();
        return success(id, "abort");
      }
      case "clear_queue": {
        return success(id, "clear_queue", session.clearQueue());
      }
      case "new_session": {
        const options = command.parentSession ? { parentSession: command.parentSession } : void 0;
        const result = await runtimeHost.newSession(options);
        if (!result.cancelled) {
          await rebindSession();
        }
        return success(id, "new_session", result);
      }
      // =================================================================
      // State
      // =================================================================
      case "get_state": {
        const state = {
          model: session.model,
          thinkingLevel: session.thinkingLevel,
          isStreaming: session.isStreaming,
          isCompacting: session.isCompacting,
          steeringMode: session.steeringMode,
          followUpMode: session.followUpMode,
          sessionFile: session.sessionFile,
          sessionId: session.sessionId,
          sessionName: session.sessionName,
          autoCompactionEnabled: session.autoCompactionEnabled,
          messageCount: session.messages.length,
          pendingMessageCount: session.pendingMessageCount
        };
        return success(id, "get_state", state);
      }
      // =================================================================
      // Model
      // =================================================================
      case "set_model": {
        const models = session.modelRuntime.getAvailableSnapshot();
        const model = models.find((m) => m.provider === command.provider && m.id === command.modelId);
        if (!model) {
          return error(id, "set_model", `Model not found: ${command.provider}/${command.modelId}`);
        }
        await session.setModel(model);
        return success(id, "set_model", model);
      }
      case "cycle_model": {
        const result = await session.cycleModel();
        if (!result) {
          return success(id, "cycle_model", null);
        }
        return success(id, "cycle_model", result);
      }
      case "get_available_models": {
        const models = session.modelRuntime.getAvailableSnapshot();
        return success(id, "get_available_models", { models });
      }
      // =================================================================
      // Thinking
      // =================================================================
      case "set_thinking_level": {
        session.setThinkingLevel(command.level);
        return success(id, "set_thinking_level");
      }
      case "cycle_thinking_level": {
        const level = session.cycleThinkingLevel();
        if (!level) {
          return success(id, "cycle_thinking_level", null);
        }
        return success(id, "cycle_thinking_level", { level });
      }
      case "get_available_thinking_levels": {
        const levels = session.getAvailableThinkingLevels();
        return success(id, "get_available_thinking_levels", { levels });
      }
      // =================================================================
      // Queue Modes
      // =================================================================
      case "set_steering_mode": {
        session.setSteeringMode(command.mode);
        return success(id, "set_steering_mode");
      }
      case "set_follow_up_mode": {
        session.setFollowUpMode(command.mode);
        return success(id, "set_follow_up_mode");
      }
      // =================================================================
      // Compaction
      // =================================================================
      case "compact": {
        const result = await session.compact(command.customInstructions);
        return success(id, "compact", result);
      }
      case "set_auto_compaction": {
        session.setAutoCompactionEnabled(command.enabled);
        return success(id, "set_auto_compaction");
      }
      // =================================================================
      // Retry
      // =================================================================
      case "set_auto_retry": {
        session.setAutoRetryEnabled(command.enabled);
        return success(id, "set_auto_retry");
      }
      case "abort_retry": {
        session.abortRetry();
        return success(id, "abort_retry");
      }
      // =================================================================
      // Bash
      // =================================================================
      case "bash": {
        const eventResult = await session.extensionRunner.emitUserBash({
          type: "user_bash",
          command: command.command,
          excludeFromContext: command.excludeFromContext ?? false,
          cwd: session.sessionManager.getCwd()
        });
        if (eventResult?.result) {
          session.recordBashResult(command.command, eventResult.result, {
            excludeFromContext: command.excludeFromContext
          });
          return success(id, "bash", eventResult.result);
        }
        const result = await session.executeBash(command.command, void 0, {
          excludeFromContext: command.excludeFromContext,
          id,
          operations: eventResult?.operations
        });
        return success(id, "bash", result);
      }
      case "abort_bash": {
        session.abortBash();
        return success(id, "abort_bash");
      }
      // =================================================================
      // Session
      // =================================================================
      case "get_session_stats": {
        const stats = session.getSessionStats();
        return success(id, "get_session_stats", stats);
      }
      case "export_html": {
        const path5 = await session.exportToHtml(command.outputPath);
        return success(id, "export_html", { path: path5 });
      }
      case "switch_session": {
        const result = await runtimeHost.switchSession(command.sessionPath);
        if (!result.cancelled) {
          await rebindSession();
        }
        return success(id, "switch_session", result);
      }
      case "fork": {
        const result = await runtimeHost.fork(command.entryId);
        if (!result.cancelled) {
          await rebindSession();
        }
        return success(id, "fork", { text: result.selectedText, cancelled: result.cancelled });
      }
      case "clone": {
        const leafId = session.sessionManager.getLeafId();
        if (!leafId) {
          return error(id, "clone", "Cannot clone session: no current entry selected");
        }
        const result = await runtimeHost.fork(leafId, { position: "at" });
        if (!result.cancelled) {
          await rebindSession();
        }
        return success(id, "clone", { cancelled: result.cancelled });
      }
      case "get_fork_messages": {
        const messages = session.getUserMessagesForForking();
        return success(id, "get_fork_messages", { messages });
      }
      case "get_entries": {
        const sessionManager = session.sessionManager;
        let entries = sessionManager.getEntries();
        if (command.since !== void 0) {
          const sinceIndex = entries.findIndex((e) => e.id === command.since);
          if (sinceIndex === -1) {
            return error(id, "get_entries", `Entry not found: ${command.since}`);
          }
          entries = entries.slice(sinceIndex + 1);
        }
        return success(id, "get_entries", { entries, leafId: sessionManager.getLeafId() });
      }
      case "get_tree": {
        const sessionManager = session.sessionManager;
        return success(id, "get_tree", { tree: sessionManager.getTree(), leafId: sessionManager.getLeafId() });
      }
      case "get_last_assistant_text": {
        const text = session.getLastAssistantText();
        return success(id, "get_last_assistant_text", { text });
      }
      case "set_session_name": {
        const name = command.name.trim();
        if (!name) {
          return error(id, "set_session_name", "Session name cannot be empty");
        }
        session.setSessionName(name);
        return success(id, "set_session_name");
      }
      // =================================================================
      // Messages
      // =================================================================
      case "get_messages": {
        return success(id, "get_messages", { messages: session.messages });
      }
      // =================================================================
      // Commands (available for invocation via prompt)
      // =================================================================
      case "get_commands": {
        const commands = [];
        for (const command2 of session.extensionRunner.getRegisteredCommands()) {
          commands.push({
            name: command2.invocationName,
            description: command2.description,
            source: "extension",
            sourceInfo: command2.sourceInfo
          });
        }
        for (const template of session.promptTemplates) {
          commands.push({
            name: template.name,
            description: template.description,
            source: "prompt",
            sourceInfo: template.sourceInfo
          });
        }
        for (const skill of session.resourceLoader.getSkills().skills) {
          commands.push({
            name: `skill:${skill.name}`,
            description: skill.description,
            source: "skill",
            sourceInfo: skill.sourceInfo
          });
        }
        return success(id, "get_commands", { commands });
      }
      default: {
        const unknownCommand = command;
        return error(id, unknownCommand.type, `Unknown command: ${unknownCommand.type}`);
      }
    }
  }, "handleCommand");
  let detachInput = /* @__PURE__ */ __name(() => {
  }, "detachInput");
  async function shutdown(exitCode = 0, signal) {
    if (shuttingDown) {
      process.exit(exitCode);
    }
    shuttingDown = true;
    for (const cleanup of signalCleanupHandlers) {
      cleanup();
    }
    unsubscribe?.();
    unsubscribeBackpressure?.();
    await runtimeHost.dispose();
    detachInput();
    process.stdin.pause();
    if (signal !== "SIGTERM") {
      await flushRawStdout();
    }
    process.exit(exitCode);
  }
  __name(shutdown, "shutdown");
  async function checkShutdownRequested() {
    if (!shutdownRequested)
      return;
    await shutdown();
  }
  __name(checkShutdownRequested, "checkShutdownRequested");
  const handleInputLine = /* @__PURE__ */ __name(async (line) => {
    let parsed;
    try {
      parsed = JSON.parse(line);
    } catch (parseError) {
      output(error(void 0, "parse", `Failed to parse command: ${parseError instanceof Error ? parseError.message : String(parseError)}`));
      await waitForRawStdoutBackpressure();
      return;
    }
    if (typeof parsed === "object" && parsed !== null && "type" in parsed && parsed.type === "extension_ui_response") {
      const response = parsed;
      const pending = pendingExtensionRequests.get(response.id);
      if (pending) {
        pendingExtensionRequests.delete(response.id);
        pending.resolve(response);
      }
      return;
    }
    const command = parsed;
    try {
      const response = await handleCommand(command);
      if (response) {
        output(response);
        await waitForRawStdoutBackpressure();
      }
      await checkShutdownRequested();
    } catch (commandError) {
      output(error(command.id, command.type, commandError instanceof Error ? commandError.message : String(commandError)));
      await waitForRawStdoutBackpressure();
    }
  }, "handleInputLine");
  const onInputEnd = /* @__PURE__ */ __name(() => {
    void shutdown();
  }, "onInputEnd");
  process.stdin.on("end", onInputEnd);
  detachInput = (() => {
    const detachJsonl = attachJsonlLineReader(process.stdin, (line) => {
      void handleInputLine(line);
    });
    return () => {
      detachJsonl();
      process.stdin.off("end", onInputEnd);
    };
  })();
  return new Promise(() => {
  });
}
__name(runRpcMode, "runRpcMode");

// pi-dist/pi-coding-agent/modes/interactive/theme/theme-json.js
import { Type } from "../../../typebox.mjs";
import { Compile } from "../../../typebox-compile.mjs";
var ColorValueSchema = Type.Union([
  Type.String(),
  // hex, OKLCH, OKHSL, var ref "primary", or empty ""
  Type.Integer({ minimum: 0, maximum: 255 })
  // 256-color index
]);
var ThemeJsonSchema = Type.Object({
  $schema: Type.Optional(Type.String()),
  name: Type.String(),
  // Background the theme is designed for. Detected from the theme colors when omitted.
  appearance: Type.Optional(Type.Union([Type.Literal("dark"), Type.Literal("light")])),
  vars: Type.Optional(Type.Record(Type.String(), ColorValueSchema)),
  colors: Type.Object({
    // Core UI (11 colors)
    accent: ColorValueSchema,
    border: ColorValueSchema,
    borderAccent: ColorValueSchema,
    borderMuted: ColorValueSchema,
    success: ColorValueSchema,
    error: ColorValueSchema,
    warning: ColorValueSchema,
    muted: ColorValueSchema,
    dim: ColorValueSchema,
    text: ColorValueSchema,
    thinkingText: ColorValueSchema,
    // Scrollbar (2 optional colors)
    scrollbarTrack: Type.Optional(ColorValueSchema),
    scrollbarThumb: Type.Optional(ColorValueSchema),
    // Backgrounds & Content Text (11 required, 2 optional)
    selectedBg: ColorValueSchema,
    searchMatchBg: Type.Optional(ColorValueSchema),
    searchMatchText: Type.Optional(ColorValueSchema),
    userMessageBg: ColorValueSchema,
    userMessageText: ColorValueSchema,
    customMessageBg: ColorValueSchema,
    customMessageText: ColorValueSchema,
    customMessageLabel: ColorValueSchema,
    toolPendingBg: ColorValueSchema,
    toolSuccessBg: ColorValueSchema,
    toolErrorBg: ColorValueSchema,
    toolTitle: ColorValueSchema,
    toolOutput: ColorValueSchema,
    // Markdown (10 colors)
    mdHeading: ColorValueSchema,
    mdLink: ColorValueSchema,
    mdLinkUrl: ColorValueSchema,
    mdCode: ColorValueSchema,
    mdCodeBlock: ColorValueSchema,
    mdCodeBlockBorder: ColorValueSchema,
    mdQuote: ColorValueSchema,
    mdQuoteBorder: ColorValueSchema,
    mdHr: ColorValueSchema,
    mdListBullet: ColorValueSchema,
    // Tool Diffs (3 colors)
    toolDiffAdded: ColorValueSchema,
    toolDiffRemoved: ColorValueSchema,
    toolDiffContext: ColorValueSchema,
    // Syntax Highlighting (9 colors)
    syntaxComment: ColorValueSchema,
    syntaxKeyword: ColorValueSchema,
    syntaxFunction: ColorValueSchema,
    syntaxVariable: ColorValueSchema,
    syntaxString: ColorValueSchema,
    syntaxNumber: ColorValueSchema,
    syntaxType: ColorValueSchema,
    syntaxOperator: ColorValueSchema,
    syntaxPunctuation: ColorValueSchema,
    // Thinking Level Borders (6 colors)
    thinkingOff: ColorValueSchema,
    thinkingMinimal: ColorValueSchema,
    thinkingLow: ColorValueSchema,
    thinkingMedium: ColorValueSchema,
    thinkingHigh: ColorValueSchema,
    thinkingXhigh: ColorValueSchema,
    thinkingMax: Type.Optional(ColorValueSchema),
    // Bash Mode (1 color)
    bashMode: ColorValueSchema
  }),
  export: Type.Optional(Type.Object({
    pageBg: Type.Optional(ColorValueSchema),
    cardBg: Type.Optional(ColorValueSchema),
    infoBg: Type.Optional(ColorValueSchema)
  }))
});
var compiledThemeSchema = Compile(ThemeJsonSchema);
function validateThemeJson(label, json2) {
  if (!compiledThemeSchema.Check(json2)) {
    const errors = Array.from(compiledThemeSchema.Errors(json2));
    const missingColors = /* @__PURE__ */ new Set();
    const otherErrors = [];
    for (const error of errors) {
      if (error.keyword === "required" && error.instancePath === "/colors") {
        const requiredProperties = error.params.requiredProperties;
        for (const requiredProperty of requiredProperties ?? []) {
          missingColors.add(requiredProperty);
        }
        continue;
      }
      const path5 = error.instancePath || "/";
      otherErrors.push(`  - ${path5}: ${error.message}`);
    }
    let errorMessage4 = `Invalid theme "${label}":
`;
    if (missingColors.size > 0) {
      errorMessage4 += "\nMissing required color tokens:\n";
      errorMessage4 += Array.from(missingColors).sort().map((color) => `  - ${color}`).join("\n");
      errorMessage4 += `

Please add these colors to your theme's "colors" object.`;
      errorMessage4 += "\nSee the built-in themes (dark.json, light.json) for reference values.";
    }
    if (otherErrors.length > 0) {
      errorMessage4 += `

Other errors:
${otherErrors.join("\n")}`;
    }
    throw new Error(errorMessage4);
  }
  const themeJson = json2;
  if (themeJson.name.includes("/")) {
    throw new Error(`Invalid theme name "${themeJson.name}": theme names cannot contain "/" because it is reserved for automatic light/dark theme settings.`);
  }
  return themeJson;
}
__name(validateThemeJson, "validateThemeJson");

// pi-dist/pi-coding-agent/package-manager-cli.js
import { existsSync as existsSync8, mkdirSync as mkdirSync5, mkdtempSync as mkdtempSync3, readdirSync as readdirSync2, readFileSync as readFileSync10, renameSync as renameSync3, rmSync as rmSync6, writeFileSync as writeFileSync6 } from "node:fs";
import { join as join14, resolve as resolve6 } from "node:path";
import { Markdown as Markdown8 } from "../../../pi-tui.mjs";
import chalk6 from "../../../chalk/source/index.js";
import lockfile from "../../../proper-lockfile.mjs";

// pi-dist/pi-coding-agent/cli/config-selector.js
import { ProcessTerminal as ProcessTerminal3, TuiMainScreen as TuiMainScreen4 } from "../../../pi-tui.mjs";

// pi-dist/pi-coding-agent/modes/interactive/components/config-selector.js
import { homedir as homedir4 } from "node:os";
import { basename as basename2, dirname as dirname4, join as join12, relative as relative3 } from "node:path";
import { Container as Container31, getKeybindings as getKeybindings15, Input as Input12, matchesKey as matchesKey3, Spacer as Spacer28, truncateToWidth as truncateToWidth8, visibleWidth as visibleWidth8 } from "../../../pi-tui.mjs";
var RESOURCE_TYPES = ["extensions", "skills", "prompts", "themes"];
var RESOURCE_TYPE_LABELS = {
  extensions: "Extensions",
  skills: "Skills",
  prompts: "Prompts",
  themes: "Themes"
};
function formatBaseDir(baseDir) {
  const homeDir = homedir4();
  let displayPath;
  if (baseDir === homeDir) {
    displayPath = "~";
  } else if (baseDir.startsWith(homeDir)) {
    const rest = baseDir.slice(homeDir.length);
    displayPath = `~${rest.replace(/\\/g, "/")}`;
  } else {
    displayPath = baseDir.replace(/\\/g, "/");
  }
  return displayPath.endsWith("/") ? displayPath : `${displayPath}/`;
}
__name(formatBaseDir, "formatBaseDir");
function getGroupLabel(metadata, agentDir) {
  if (metadata.origin === "package") {
    return `${metadata.source} (${metadata.scope})`;
  }
  if (metadata.source === "builtin") {
    return metadata.scope === "user" ? "Built-in" : "Built-in (project override)";
  }
  if (metadata.source === "auto") {
    if (metadata.baseDir) {
      return metadata.scope === "user" ? `User (${formatBaseDir(metadata.baseDir)})` : `Project (${formatBaseDir(metadata.baseDir)})`;
    }
    return metadata.scope === "user" ? `User (${formatBaseDir(agentDir)})` : `Project (${CONFIG_DIR_NAME}/)`;
  }
  return metadata.scope === "user" ? "User settings" : "Project settings";
}
__name(getGroupLabel, "getGroupLabel");
function buildGroups(resolved, agentDir) {
  const groupMap = /* @__PURE__ */ new Map();
  const addToGroup = /* @__PURE__ */ __name((resources, resourceType) => {
    for (const res of resources) {
      const { path: path5, enabled, metadata } = res;
      const groupKey = `${metadata.origin}:${metadata.scope}:${metadata.source}:${metadata.baseDir ?? ""}`;
      if (!groupMap.has(groupKey)) {
        groupMap.set(groupKey, {
          key: groupKey,
          label: getGroupLabel(metadata, agentDir),
          scope: metadata.scope,
          origin: metadata.origin,
          source: metadata.source,
          subgroups: []
        });
      }
      const group = groupMap.get(groupKey);
      const subgroupKey = `${groupKey}:${resourceType}`;
      let subgroup = group.subgroups.find((sg) => sg.type === resourceType);
      if (!subgroup) {
        subgroup = {
          type: resourceType,
          label: RESOURCE_TYPE_LABELS[resourceType],
          items: []
        };
        group.subgroups.push(subgroup);
      }
      const fileName = basename2(path5);
      const parentFolder = basename2(dirname4(path5));
      let displayName;
      if (metadata.source === "builtin") {
        displayName = path5.slice(BUILTIN_PATH_PREFIX.length);
      } else if (resourceType === "extensions" && parentFolder !== "extensions") {
        displayName = `${parentFolder}/${fileName}`;
      } else if (resourceType === "skills" && fileName === "SKILL.md") {
        displayName = parentFolder;
      } else {
        displayName = fileName;
      }
      subgroup.items.push({
        path: path5,
        enabled,
        metadata,
        resourceType,
        displayName,
        groupKey,
        subgroupKey
      });
    }
  }, "addToGroup");
  addToGroup(resolved.extensions, "extensions");
  addToGroup(resolved.skills, "skills");
  addToGroup(resolved.prompts, "prompts");
  addToGroup(resolved.themes, "themes");
  const groups = Array.from(groupMap.values());
  groups.sort((a, b) => {
    if (a.origin !== b.origin) {
      return a.origin === "package" ? -1 : 1;
    }
    if (a.scope !== b.scope) {
      return a.scope === "user" ? -1 : 1;
    }
    return a.source.localeCompare(b.source);
  });
  const typeOrder = { extensions: 0, skills: 1, prompts: 2, themes: 3 };
  for (const group of groups) {
    group.subgroups.sort((a, b) => typeOrder[a.type] - typeOrder[b.type]);
    for (const subgroup of group.subgroups) {
      subgroup.items.sort((a, b) => a.displayName.localeCompare(b.displayName));
    }
  }
  return groups;
}
__name(buildGroups, "buildGroups");
var ConfigSelectorHeader = class {
  static {
    __name(this, "ConfigSelectorHeader");
  }
  writeScope;
  projectModeAvailable;
  constructor(writeScope, projectModeAvailable) {
    this.writeScope = writeScope;
    this.projectModeAvailable = projectModeAvailable;
  }
  setWriteScope(writeScope) {
    this.writeScope = writeScope;
  }
  invalidate() {
  }
  render(width) {
    const title = theme.bold(this.writeScope === "project" ? "Project Local Resources" : "Global Resources");
    const sep3 = theme.fg("muted", " \xB7 ");
    const switchHint = this.projectModeAvailable ? keyHint("tui.input.tab", "switch mode") + sep3 : "";
    const actionHint = this.writeScope === "project" ? rawKeyHint("space", "cycle inherit/+/-") : rawKeyHint("space", "toggle");
    const hint = switchHint + actionHint + sep3 + rawKeyHint("esc", "close");
    const spacing = Math.max(1, width - visibleWidth8(title) - visibleWidth8(hint));
    const scopeHint = this.writeScope === "project" ? theme.fg("muted", `${CONFIG_DIR_NAME}/settings.json \xB7 inherited global resources are dimmed`) : theme.fg("muted", `~/${CONFIG_DIR_NAME}/agent/settings.json`);
    return [
      truncateToWidth8(`${title}${" ".repeat(spacing)}${hint}`, width, ""),
      truncateToWidth8(scopeHint, width, "")
    ];
  }
};
var ResourceList = class {
  static {
    __name(this, "ResourceList");
  }
  groupsByScope;
  flatItems = [];
  filteredItems = [];
  selectedIndex = 0;
  searchInput;
  maxVisible;
  settingsManager;
  cwd;
  agentDir;
  writeScope;
  inheritedEnabledByKey;
  onCancel;
  onExit;
  onToggle;
  onSwitchMode;
  _focused = false;
  get focused() {
    return this._focused;
  }
  set focused(value) {
    this._focused = value;
    this.searchInput.focused = value;
  }
  constructor(groupsByScope, settingsManager, cwd, agentDir, terminalHeight, writeScope = "global") {
    this.groupsByScope = groupsByScope;
    this.settingsManager = settingsManager;
    this.cwd = cwd;
    this.agentDir = agentDir;
    this.writeScope = writeScope;
    this.inheritedEnabledByKey = this.buildInheritedEnabledMap(groupsByScope.global);
    this.searchInput = new Input12();
    const chrome = 8;
    this.maxVisible = Math.max(5, (terminalHeight ?? 24) - chrome);
    this.buildFlatList();
    this.filteredItems = [...this.flatItems];
  }
  setWriteScope(writeScope) {
    this.writeScope = writeScope;
    this.buildFlatList();
    this.filterItems(this.searchInput.getValue());
  }
  get groups() {
    return this.groupsByScope[this.writeScope];
  }
  buildInheritedEnabledMap(groups) {
    const result = /* @__PURE__ */ new Map();
    for (const group of groups) {
      for (const subgroup of group.subgroups) {
        for (const item of subgroup.items) {
          result.set(this.getResourceItemKey(item), item.enabled);
        }
      }
    }
    return result;
  }
  buildFlatList() {
    this.flatItems = [];
    for (const group of this.groups) {
      this.flatItems.push({ type: "group", group });
      for (const subgroup of group.subgroups) {
        this.flatItems.push({ type: "subgroup", subgroup, group });
        for (const item of subgroup.items) {
          this.flatItems.push({ type: "item", item });
        }
      }
    }
    this.selectedIndex = this.flatItems.findIndex((e) => e.type === "item");
    if (this.selectedIndex < 0)
      this.selectedIndex = 0;
  }
  findNextItem(fromIndex, direction) {
    let idx = fromIndex + direction;
    while (idx >= 0 && idx < this.filteredItems.length) {
      if (this.filteredItems[idx].type === "item") {
        return idx;
      }
      idx += direction;
    }
    return fromIndex;
  }
  filterItems(query) {
    if (!query.trim()) {
      this.filteredItems = [...this.flatItems];
      this.selectFirstItem();
      return;
    }
    const lowerQuery = query.toLowerCase();
    const matchingItems = /* @__PURE__ */ new Set();
    const matchingSubgroups = /* @__PURE__ */ new Set();
    const matchingGroups = /* @__PURE__ */ new Set();
    for (const entry of this.flatItems) {
      if (entry.type === "item") {
        const item = entry.item;
        if (item.displayName.toLowerCase().includes(lowerQuery) || item.resourceType.toLowerCase().includes(lowerQuery) || item.path.toLowerCase().includes(lowerQuery)) {
          matchingItems.add(item);
        }
      }
    }
    for (const group of this.groups) {
      for (const subgroup of group.subgroups) {
        for (const item of subgroup.items) {
          if (matchingItems.has(item)) {
            matchingSubgroups.add(subgroup);
            matchingGroups.add(group);
          }
        }
      }
    }
    this.filteredItems = [];
    for (const entry of this.flatItems) {
      if (entry.type === "group" && matchingGroups.has(entry.group)) {
        this.filteredItems.push(entry);
      } else if (entry.type === "subgroup" && matchingSubgroups.has(entry.subgroup)) {
        this.filteredItems.push(entry);
      } else if (entry.type === "item" && matchingItems.has(entry.item)) {
        this.filteredItems.push(entry);
      }
    }
    this.selectFirstItem();
  }
  selectFirstItem() {
    const firstItemIndex = this.filteredItems.findIndex((e) => e.type === "item");
    this.selectedIndex = firstItemIndex >= 0 ? firstItemIndex : 0;
  }
  updateItem(item, enabled) {
    item.enabled = enabled;
    for (const group of this.groups) {
      for (const subgroup of group.subgroups) {
        const found = subgroup.items.find((i) => i.path === item.path && i.resourceType === item.resourceType);
        if (found) {
          found.enabled = enabled;
          return;
        }
      }
    }
  }
  invalidate() {
  }
  render(width) {
    const lines = [];
    lines.push(...this.searchInput.render(width));
    lines.push("");
    if (this.filteredItems.length === 0) {
      lines.push(theme.fg("muted", "  No resources found"));
      return lines;
    }
    const startIndex = Math.max(0, Math.min(this.selectedIndex - Math.floor(this.maxVisible / 2), this.filteredItems.length - this.maxVisible));
    const endIndex = Math.min(startIndex + this.maxVisible, this.filteredItems.length);
    for (let i = startIndex; i < endIndex; i++) {
      const entry = this.filteredItems[i];
      const isSelected = i === this.selectedIndex;
      if (entry.type === "group") {
        const inherited = this.writeScope === "project" && entry.group.scope === "user";
        const label = theme.bold(`${entry.group.label}${inherited ? " \xB7 inherited global" : ""}`);
        const groupLine = theme.fg(inherited ? "dim" : "accent", label);
        lines.push(truncateToWidth8(`  ${groupLine}`, width, ""));
      } else if (entry.type === "subgroup") {
        const color = this.writeScope === "project" && entry.group.scope === "user" ? "dim" : "muted";
        const subgroupLine = theme.fg(color, entry.subgroup.label);
        lines.push(truncateToWidth8(`    ${subgroupLine}`, width, ""));
      } else {
        const item = entry.item;
        const cursor = isSelected ? "> " : "  ";
        const dimmed = this.isDimmedItem(item);
        const nameText = isSelected && !dimmed ? theme.bold(item.displayName) : item.displayName;
        const name = dimmed ? theme.fg("dim", nameText) : nameText;
        lines.push(truncateToWidth8(`${cursor}    ${this.renderCheckbox(item)} ${name}${this.getItemSuffix(item)}`, width, "..."));
      }
    }
    if (startIndex > 0 || endIndex < this.filteredItems.length) {
      const itemCount = this.filteredItems.filter((e) => e.type === "item").length;
      const currentItemIndex = this.filteredItems.slice(0, this.selectedIndex).filter((e) => e.type === "item").length + 1;
      lines.push(theme.fg("dim", `  (${currentItemIndex}/${itemCount})`));
    }
    return lines;
  }
  handleInput(data) {
    const kb = getKeybindings15();
    if (kb.matches(data, "tui.select.up")) {
      this.selectedIndex = this.findNextItem(this.selectedIndex, -1);
      return;
    }
    if (kb.matches(data, "tui.select.down")) {
      this.selectedIndex = this.findNextItem(this.selectedIndex, 1);
      return;
    }
    if (kb.matches(data, "tui.select.pageUp")) {
      let target = Math.max(0, this.selectedIndex - this.maxVisible);
      while (target < this.filteredItems.length && this.filteredItems[target].type !== "item") {
        target++;
      }
      if (target < this.filteredItems.length) {
        this.selectedIndex = target;
      }
      return;
    }
    if (kb.matches(data, "tui.select.pageDown")) {
      let target = Math.min(this.filteredItems.length - 1, this.selectedIndex + this.maxVisible);
      while (target >= 0 && this.filteredItems[target].type !== "item") {
        target--;
      }
      if (target >= 0) {
        this.selectedIndex = target;
      }
      return;
    }
    if (kb.matches(data, "tui.select.cancel")) {
      this.onCancel?.();
      return;
    }
    if (matchesKey3(data, "ctrl+c")) {
      this.onExit?.();
      return;
    }
    if (kb.matches(data, "tui.input.tab")) {
      this.onSwitchMode?.();
      return;
    }
    if (data === " " || kb.matches(data, "tui.select.confirm")) {
      const entry = this.filteredItems[this.selectedIndex];
      if (entry?.type === "item" && (this.writeScope === "project" || this.getItemScope(entry.item) === "user")) {
        const newEnabled = this.toggleResource(entry.item);
        if (newEnabled !== void 0) {
          this.updateItem(entry.item, newEnabled);
          this.onToggle?.(entry.item, newEnabled);
        }
      }
      return;
    }
    this.searchInput.handleInput(data);
    this.filterItems(this.searchInput.getValue());
  }
  toggleResource(item) {
    if (this.writeScope === "project") {
      const state = this.getNextOverrideState(item);
      if (!this.setProjectResourceOverride(item, state))
        return void 0;
      return state === "inherit" ? this.getInheritedEnabled(item) : state === "load";
    }
    const enabled = !item.enabled;
    if (item.metadata.origin === "top-level") {
      this.toggleTopLevelResource(item, enabled);
    } else {
      this.togglePackageResource(item, enabled);
    }
    return enabled;
  }
  toggleTopLevelResource(item, enabled) {
    const scope = item.metadata.scope;
    const settings = scope === "project" ? this.settingsManager.getProjectSettings() : this.settingsManager.getGlobalSettings();
    const arrayKey = item.resourceType;
    const current = settings[arrayKey] ?? [];
    const pattern = this.getResourcePattern(item);
    const disablePattern = `-${pattern}`;
    const enablePattern = `+${pattern}`;
    const updated = current.filter((p) => {
      const stripped = p.startsWith("!") || p.startsWith("+") || p.startsWith("-") ? p.slice(1) : p;
      return stripped !== pattern;
    });
    if (enabled) {
      updated.push(enablePattern);
    } else {
      updated.push(disablePattern);
    }
    if (scope === "project") {
      if (arrayKey === "extensions") {
        this.settingsManager.setProjectExtensionPaths(updated);
      } else if (arrayKey === "skills") {
        this.settingsManager.setProjectSkillPaths(updated);
      } else if (arrayKey === "prompts") {
        this.settingsManager.setProjectPromptTemplatePaths(updated);
      } else if (arrayKey === "themes") {
        this.settingsManager.setProjectThemePaths(updated);
      }
    } else {
      if (arrayKey === "extensions") {
        this.settingsManager.setExtensionPaths(updated);
      } else if (arrayKey === "skills") {
        this.settingsManager.setSkillPaths(updated);
      } else if (arrayKey === "prompts") {
        this.settingsManager.setPromptTemplatePaths(updated);
      } else if (arrayKey === "themes") {
        this.settingsManager.setThemePaths(updated);
      }
    }
  }
  togglePackageResource(item, enabled) {
    const scope = item.metadata.scope;
    const settings = scope === "project" ? this.settingsManager.getProjectSettings() : this.settingsManager.getGlobalSettings();
    const packages = [...settings.packages ?? []];
    const pkgIndex = packages.findIndex((pkg2) => {
      const source = typeof pkg2 === "string" ? pkg2 : pkg2.source;
      return source === item.metadata.source;
    });
    if (pkgIndex === -1)
      return;
    let pkg = packages[pkgIndex];
    if (typeof pkg === "string") {
      pkg = { source: pkg };
      packages[pkgIndex] = pkg;
    }
    const arrayKey = item.resourceType;
    const current = pkg[arrayKey] ?? [];
    const pattern = this.getPackageResourcePattern(item);
    const disablePattern = `-${pattern}`;
    const enablePattern = `+${pattern}`;
    const updated = current.filter((p) => {
      const stripped = p.startsWith("!") || p.startsWith("+") || p.startsWith("-") ? p.slice(1) : p;
      return stripped !== pattern;
    });
    if (enabled) {
      updated.push(enablePattern);
    } else {
      updated.push(disablePattern);
    }
    pkg[arrayKey] = updated.length > 0 ? updated : void 0;
    const hasFilters = ["extensions", "skills", "prompts", "themes"].some((k) => pkg[k] !== void 0);
    if (!hasFilters) {
      packages[pkgIndex] = pkg.source;
    }
    if (scope === "project") {
      this.settingsManager.setProjectPackages(packages);
    } else {
      this.settingsManager.setPackages(packages);
    }
  }
  renderCheckbox(item) {
    if (this.writeScope === "project") {
      const state = this.getProjectOverrideState(item);
      if (state === "load")
        return theme.fg("success", "[+]");
      if (state === "unload")
        return theme.fg("warning", "[-]");
      return theme.fg("dim", item.enabled ? "[x]" : "[ ]");
    }
    return item.enabled ? theme.fg("success", "[x]") : theme.fg("dim", "[ ]");
  }
  getItemSuffix(item) {
    if (this.writeScope !== "project")
      return "";
    const state = this.getProjectOverrideState(item);
    if (state === "load")
      return theme.fg("muted", "  project load");
    if (state === "unload")
      return theme.fg("muted", "  project unload");
    return this.isInheritedGlobalItem(item) ? theme.fg("dim", "  inherited global") : "";
  }
  isDimmedItem(item) {
    return this.writeScope === "project" && this.isInheritedGlobalItem(item) && this.getProjectOverrideState(item) === "inherit";
  }
  setProjectResourceOverride(item, state) {
    return item.metadata.origin === "top-level" ? this.setProjectTopLevelOverride(item, state) : this.setProjectPackageOverride(item, state);
  }
  setProjectTopLevelOverride(item, state) {
    const current = this.settingsManager.getProjectSettings()[item.resourceType] ?? [];
    const pattern = this.isInheritedGlobalItem(item) ? item.path : this.getResourcePatternForScope(item, "project");
    const patterns = this.getTopLevelOverridePatterns(item, "project");
    const updated = current.filter((entry) => {
      const target = this.getPatternEntryTarget(entry);
      if ((entry.startsWith("!") || entry.startsWith("+") || entry.startsWith("-")) && patterns.has(target))
        return false;
      return !(state === "inherit" && this.isInheritedGlobalItem(item) && target === pattern);
    });
    if (state !== "inherit") {
      if (this.isInheritedGlobalItem(item) && item.metadata.source !== "builtin" && !updated.includes(pattern))
        updated.push(pattern);
      updated.push(`${state === "load" ? "+" : "-"}${pattern}`);
    }
    this.setProjectTopLevelPaths(item.resourceType, updated);
    return true;
  }
  setProjectTopLevelPaths(key, paths) {
    if (key === "extensions")
      this.settingsManager.setProjectExtensionPaths(paths);
    else if (key === "skills")
      this.settingsManager.setProjectSkillPaths(paths);
    else if (key === "prompts")
      this.settingsManager.setProjectPromptTemplatePaths(paths);
    else
      this.settingsManager.setProjectThemePaths(paths);
  }
  setProjectPackageOverride(item, state) {
    const packages = [...this.settingsManager.getProjectSettings().packages ?? []];
    let pkgIndex = packages.findIndex((pkg2) => this.packageSourceStringMatches(item.metadata.source, this.getItemScope(item), typeof pkg2 === "string" ? pkg2 : pkg2.source, "project"));
    if (pkgIndex === -1) {
      if (state === "inherit")
        return false;
      packages.push(this.createPackageOverrideSource(item));
      pkgIndex = packages.length - 1;
    }
    let pkg = packages[pkgIndex];
    if (pkg === void 0)
      return false;
    if (typeof pkg === "string") {
      pkg = { source: pkg };
      packages[pkgIndex] = pkg;
    }
    const pattern = this.getPackageResourcePattern(item);
    const updated = (pkg[item.resourceType] ?? []).filter((entry) => this.getPatternEntryTarget(entry) !== pattern);
    if (state !== "inherit")
      updated.push(`${state === "load" ? "+" : "-"}${pattern}`);
    pkg[item.resourceType] = updated.length > 0 ? updated : void 0;
    if (!RESOURCE_TYPES.some((key) => pkg[key] !== void 0)) {
      if (pkg.autoload === false)
        packages.splice(pkgIndex, 1);
      else
        packages[pkgIndex] = pkg.source;
    }
    this.settingsManager.setProjectPackages(packages);
    return true;
  }
  getNextOverrideState(item) {
    const state = this.getProjectOverrideState(item);
    const inheritedEnabled = this.getInheritedEnabled(item);
    if (state === "inherit")
      return inheritedEnabled ? "unload" : "load";
    if (state === "unload")
      return inheritedEnabled ? "load" : "inherit";
    return inheritedEnabled ? "inherit" : "unload";
  }
  getProjectOverrideState(item) {
    if (this.writeScope !== "project")
      return "inherit";
    if (item.metadata.origin === "top-level") {
      return this.getOverrideStateFromEntries(this.settingsManager.getProjectSettings()[item.resourceType] ?? [], this.getTopLevelOverridePatterns(item, "project"), false);
    }
    const pkg = this.findMatchingPackageSource(item, "project");
    if (typeof pkg !== "object")
      return "inherit";
    const entries = pkg[item.resourceType];
    if (entries === void 0)
      return "inherit";
    return this.getOverrideStateFromEntries(entries, /* @__PURE__ */ new Set([this.getPackageResourcePattern(item)]), pkg.autoload !== false);
  }
  getOverrideStateFromEntries(entries, patterns, emptyArrayIsUnload) {
    if (entries.length === 0 && emptyArrayIsUnload)
      return "unload";
    let state = "inherit";
    for (const entry of entries) {
      if (!patterns.has(this.getPatternEntryTarget(entry)))
        continue;
      if (entry.startsWith("!") || entry.startsWith("-"))
        state = "unload";
      else
        state = "load";
    }
    return state;
  }
  getInheritedEnabled(item) {
    return this.inheritedEnabledByKey.get(this.getResourceItemKey(item)) ?? (this.getItemScope(item) === "user" ? item.enabled : true);
  }
  isInheritedGlobalItem(item) {
    return this.getItemScope(item) === "user" || this.inheritedEnabledByKey.has(this.getResourceItemKey(item));
  }
  getTopLevelOverridePatterns(item, scope) {
    const baseDir = this.getTopLevelBaseDir(scope);
    const patterns = /* @__PURE__ */ new Set([
      this.getResourcePatternForScope(item, scope),
      item.path,
      relative3(baseDir, item.path)
    ]);
    if (item.metadata.baseDir)
      patterns.add(relative3(item.metadata.baseDir, item.path));
    return patterns;
  }
  getResourcePatternForScope(item, scope) {
    const sourceScope = this.getItemScope(item);
    if (scope !== sourceScope || item.metadata.source === "builtin")
      return item.path;
    const baseDir = item.metadata.baseDir ?? this.getTopLevelBaseDir(sourceScope);
    return relative3(baseDir, item.path);
  }
  createPackageOverrideSource(item) {
    const source = item.metadata.source;
    if (!isLocalPath(source))
      return { source, autoload: false };
    const sourcePath = resolvePath(source, this.getTopLevelBaseDir(this.getItemScope(item)), { trim: true });
    return { source: relative3(this.getTopLevelBaseDir("project"), sourcePath) || ".", autoload: false };
  }
  packageSourceStringMatches(leftSource, leftScope, rightSource, rightScope) {
    if (leftSource === rightSource)
      return true;
    if (!isLocalPath(leftSource) || !isLocalPath(rightSource))
      return false;
    const left = resolvePath(leftSource, this.getTopLevelBaseDir(leftScope), { trim: true });
    const right = resolvePath(rightSource, this.getTopLevelBaseDir(rightScope), { trim: true });
    return left === right;
  }
  findMatchingPackageSource(item, targetScope) {
    const settings = targetScope === "project" ? this.settingsManager.getProjectSettings() : this.settingsManager.getGlobalSettings();
    return (settings.packages ?? []).find((pkg) => this.packageSourceStringMatches(item.metadata.source, this.getItemScope(item), typeof pkg === "string" ? pkg : pkg.source, targetScope));
  }
  getPatternEntryTarget(entry) {
    return entry.startsWith("!") || entry.startsWith("+") || entry.startsWith("-") ? entry.slice(1) : entry;
  }
  getResourceItemKey(item) {
    return `${item.resourceType}:${canonicalizePath(item.path)}`;
  }
  getItemScope(item) {
    return item.metadata.scope === "project" ? "project" : "user";
  }
  getTopLevelBaseDir(scope) {
    return scope === "project" ? join12(this.cwd, CONFIG_DIR_NAME) : this.agentDir;
  }
  getResourcePattern(item) {
    if (item.metadata.source === "builtin")
      return item.path;
    const scope = item.metadata.scope;
    const baseDir = item.metadata.baseDir ?? this.getTopLevelBaseDir(scope);
    return relative3(baseDir, item.path);
  }
  getPackageResourcePattern(item) {
    const baseDir = item.metadata.baseDir ?? dirname4(item.path);
    return relative3(baseDir, item.path);
  }
};
var ConfigSelectorComponent = class extends Container31 {
  static {
    __name(this, "ConfigSelectorComponent");
  }
  header;
  resourceList;
  writeScope;
  _focused = false;
  get focused() {
    return this._focused;
  }
  set focused(value) {
    this._focused = value;
    this.resourceList.focused = value;
  }
  constructor(resolvedPaths, settingsManager, cwd, agentDir, onClose, onExit, requestRender, terminalHeight, writeScope = "global", projectModeAvailable = true) {
    super();
    this.writeScope = writeScope;
    const groupsByScope = {
      global: buildGroups(resolvedPaths.global, agentDir),
      project: buildGroups(resolvedPaths.project, agentDir)
    };
    this.addChild(new Spacer28(1));
    this.addChild(new DynamicBorder());
    this.addChild(new Spacer28(1));
    this.header = new ConfigSelectorHeader(this.writeScope, projectModeAvailable);
    this.addChild(this.header);
    this.addChild(new Spacer28(1));
    this.resourceList = new ResourceList(groupsByScope, settingsManager, cwd, agentDir, terminalHeight, this.writeScope);
    this.resourceList.onCancel = onClose;
    this.resourceList.onExit = onExit;
    this.resourceList.onToggle = () => requestRender();
    if (projectModeAvailable) {
      this.resourceList.onSwitchMode = () => {
        this.switchWriteScope();
        requestRender();
      };
    }
    this.addChild(this.resourceList);
    this.addChild(new Spacer28(1));
    this.addChild(new DynamicBorder());
  }
  switchWriteScope() {
    this.writeScope = this.writeScope === "global" ? "project" : "global";
    this.header.setWriteScope(this.writeScope);
    this.resourceList.setWriteScope(this.writeScope);
  }
  getResourceList() {
    return this.resourceList;
  }
};

// pi-dist/pi-coding-agent/cli/config-selector.js
async function selectConfig(options) {
  initTheme(options.settingsManager.getTheme(), true);
  return new Promise((resolve7) => {
    const ui = new TuiMainScreen4(new ProcessTerminal3(), options.settingsManager.getShowHardwareCursor(), options.agentDir);
    ui.setClearOnShrink(options.settingsManager.getClearOnShrink());
    let resolved = false;
    const selector = new ConfigSelectorComponent(options.resolvedPaths, options.settingsManager, options.cwd, options.agentDir, () => {
      if (!resolved) {
        resolved = true;
        ui.stop();
        stopThemeWatcher();
        resolve7();
      }
    }, () => {
      ui.stop();
      stopThemeWatcher();
      process.exit(0);
    }, () => ui.requestRender(), ui.terminal.rows, options.writeScope, options.projectModeAvailable);
    ui.addChild(selector);
    ui.setFocus(selector.getResourceList());
    ui.start();
  });
}
__name(selectConfig, "selectConfig");

// pi-dist/pi-coding-agent/utils/windows-self-update.js
import { randomUUID as randomUUID6 } from "node:crypto";
import { copyFileSync, existsSync as existsSync7, mkdirSync as mkdirSync4, renameSync as renameSync2, rmSync as rmSync5 } from "node:fs";
import { basename as basename3, dirname as dirname5, join as join13, relative as relative4, resolve as resolve5, toNamespacedPath } from "node:path";
var QUARANTINE_DIR_NAME = ".pi-native-quarantine";
function normalizePath2(path5) {
  return toNamespacedPath(resolve5(path5));
}
__name(normalizePath2, "normalizePath");
function getQuarantineRoot(packageDir) {
  let current = resolve5(packageDir);
  while (true) {
    if (basename3(current).toLowerCase() === "node_modules") {
      return join13(current, QUARANTINE_DIR_NAME);
    }
    const parent = dirname5(current);
    if (parent === current) {
      return void 0;
    }
    current = parent;
  }
}
__name(getQuarantineRoot, "getQuarantineRoot");
function getLoadedSharedObjectsInPackageDir(packageDir) {
  const sharedObjects = process.report.getReport().sharedObjects;
  if (!Array.isArray(sharedObjects)) {
    return [];
  }
  const root = normalizePath2(packageDir).toLowerCase();
  const seen = /* @__PURE__ */ new Set();
  const loadedFiles = [];
  for (const value of sharedObjects) {
    if (typeof value !== "string") {
      continue;
    }
    const filePath = normalizePath2(value);
    const comparisonPath = filePath.toLowerCase();
    if (getCwdRelativePath(comparisonPath, root) === void 0 || seen.has(comparisonPath)) {
      continue;
    }
    seen.add(comparisonPath);
    loadedFiles.push(filePath);
  }
  return loadedFiles;
}
__name(getLoadedSharedObjectsInPackageDir, "getLoadedSharedObjectsInPackageDir");
function cleanupWindowsSelfUpdateQuarantine(packageDir) {
  const quarantineRoot = getQuarantineRoot(packageDir);
  if (!quarantineRoot) {
    return;
  }
  try {
    rmSync5(quarantineRoot, { recursive: true, force: true });
  } catch {
  }
}
__name(cleanupWindowsSelfUpdateQuarantine, "cleanupWindowsSelfUpdateQuarantine");
function quarantineWindowsNativeDependencies(packageDir) {
  const resolvedPackageDir = normalizePath2(packageDir);
  const quarantineRoot = getQuarantineRoot(resolvedPackageDir);
  if (!quarantineRoot) {
    return;
  }
  const loadedFiles = getLoadedSharedObjectsInPackageDir(resolvedPackageDir);
  if (loadedFiles.length === 0) {
    return;
  }
  const quarantineRunDir = join13(quarantineRoot, `${Date.now()}-${process.pid}-${randomUUID6()}`);
  for (const loadedFile of loadedFiles) {
    if (!existsSync7(loadedFile)) {
      continue;
    }
    const quarantinePath = join13(quarantineRunDir, relative4(resolvedPackageDir, loadedFile));
    mkdirSync4(dirname5(quarantinePath), { recursive: true });
    renameSync2(loadedFile, quarantinePath);
    copyFileSync(quarantinePath, loadedFile);
  }
}
__name(quarantineWindowsNativeDependencies, "quarantineWindowsNativeDependencies");

// pi-dist/pi-coding-agent/package-manager-cli.js
var DEFAULT_INSTALLER_API_BASE = "https://pi-in-go.dev/api/installer/releases";
var MANAGED_INSTALL_MARKER = "managed-install.json";
var MANAGED_RELEASE_VERSION_RE = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/;
function getActiveManagedInstallRoot() {
  const configuredRoot = process.env.PI_MANAGED_INSTALL_ROOT?.trim();
  if (!configuredRoot)
    return void 0;
  const managedRoot = resolve6(configuredRoot);
  const releasesDir = canonicalizePath(join14(managedRoot, "releases"));
  if (getCwdRelativePath(canonicalizePath(getPackageDir()), releasesDir) === void 0)
    return void 0;
  const markerPath = join14(managedRoot, MANAGED_INSTALL_MARKER);
  try {
    const marker = JSON.parse(readFileSync10(markerPath, "utf8"));
    if (marker.kind !== "pi-managed-install" || marker.schemaVersion !== 1 || marker.layout !== "releases-v1") {
      throw new Error();
    }
  } catch {
    throw new Error(`Managed install marker is missing or invalid: ${markerPath}`);
  }
  return managedRoot;
}
__name(getActiveManagedInstallRoot, "getActiveManagedInstallRoot");
async function fetchInstallerArtifact(url, label) {
  const response = await fetch(url, { headers: { "User-Agent": getPiUserAgent(VERSION) } });
  if (!response.ok) {
    throw new Error(`Could not download managed installer ${label} from ${url}: HTTP ${response.status}`);
  }
  return await response.text();
}
__name(fetchInstallerArtifact, "fetchInstallerArtifact");
async function runManagedNpmCi(stageDir) {
  const args = [
    "ci",
    "--ignore-scripts",
    "--min-release-age=0",
    "--omit=dev",
    "--include=optional",
    "--no-fund",
    "--no-audit",
    "--loglevel=error",
    "--progress=false"
  ];
  const code = await waitForChildProcess(spawnProcess("npm", args, { cwd: stageDir, stdio: "inherit" }));
  if (code !== 0)
    throw new Error(`npm ${args.join(" ")} exited with code ${code ?? "unknown"}`);
}
__name(runManagedNpmCi, "runManagedNpmCi");
function verifyManagedRelease(releaseDir, expectedVersion) {
  const binPath = join14(releaseDir, "node_modules", ".bin", process.platform === "win32" ? `${APP_NAME}.cmd` : APP_NAME);
  const result = spawnProcessSync(binPath, ["--version"], {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"]
  });
  if (result.error || result.status !== 0) {
    const reason = result.error?.message || result.stderr.trim() || `exit code ${result.status ?? "unknown"}`;
    throw new Error(`Could not verify managed Pi ${expectedVersion}: ${reason}`);
  }
  const installedVersion = result.stdout.trim();
  if (installedVersion !== expectedVersion) {
    throw new Error(`Managed Pi smoke test returned version ${installedVersion}; expected ${expectedVersion}.`);
  }
}
__name(verifyManagedRelease, "verifyManagedRelease");
function activateManagedRelease(managedRoot, version) {
  const currentPath = join14(managedRoot, "current-version");
  const temporaryPath = join14(managedRoot, `current-version.tmp.${process.pid}-${Date.now()}`);
  try {
    writeFileSync6(temporaryPath, `${version}
`);
    renameSync3(temporaryPath, currentPath);
  } finally {
    rmSync6(temporaryPath, { force: true });
  }
}
__name(activateManagedRelease, "activateManagedRelease");
function pruneManagedReleases(managedRoot, activeVersion) {
  const releasesRoot = join14(managedRoot, "releases");
  let entries;
  try {
    entries = readdirSync2(releasesRoot);
  } catch {
    return;
  }
  for (const entry of entries) {
    if (entry === activeVersion || entry === VERSION || !MANAGED_RELEASE_VERSION_RE.test(entry))
      continue;
    try {
      rmSync6(join14(releasesRoot, entry), { force: true, recursive: true });
    } catch {
    }
  }
}
__name(pruneManagedReleases, "pruneManagedReleases");
function cleanupManagedStaging(managedRoot) {
  const stagingRoot = join14(managedRoot, "staging");
  try {
    for (const entry of readdirSync2(stagingRoot)) {
      if (entry.startsWith("update-")) {
        rmSync6(join14(stagingRoot, entry), { force: true, recursive: true });
      }
    }
  } catch {
  }
}
__name(cleanupManagedStaging, "cleanupManagedStaging");
function cleanupManagedInstall() {
  let managedRoot;
  try {
    managedRoot = getActiveManagedInstallRoot();
  } catch {
    return;
  }
  if (!managedRoot)
    return;
  try {
    const releaseLock = lockfile.lockSync(join14(managedRoot, "update"), { realpath: false });
    try {
      cleanupManagedStaging(managedRoot);
    } finally {
      releaseLock();
    }
  } catch {
  }
}
__name(cleanupManagedInstall, "cleanupManagedInstall");
async function runManagedSelfUpdate(managedRoot, version) {
  if (!MANAGED_RELEASE_VERSION_RE.test(version)) {
    throw new Error(`Invalid managed release version: ${version}`);
  }
  let releaseLock;
  try {
    releaseLock = await lockfile.lock(join14(managedRoot, "update"), { realpath: false });
  } catch (error) {
    if (error instanceof Error && "code" in error && error.code === "ELOCKED") {
      throw new Error("Another managed Pi update is already running.");
    }
    throw error;
  }
  let stageDir;
  try {
    cleanupManagedStaging(managedRoot);
    const installerApiBase = (process.env.PI_INSTALLER_API_BASE?.trim() || DEFAULT_INSTALLER_API_BASE).replace(/\/+$/, "");
    const releaseUrl = `${installerApiBase}/${encodeURIComponent(version)}`;
    const stagingRoot = join14(managedRoot, "staging");
    const releasesRoot = join14(managedRoot, "releases");
    mkdirSync5(releasesRoot, { recursive: true });
    const releaseDir = join14(releasesRoot, version);
    if (existsSync8(releaseDir)) {
      verifyManagedRelease(releaseDir, version);
      activateManagedRelease(managedRoot, version);
      pruneManagedReleases(managedRoot, version);
      return;
    }
    mkdirSync5(stagingRoot, { recursive: true });
    stageDir = mkdtempSync3(join14(stagingRoot, "update-"));
    const [packageJsonContent, packageLockContent] = await Promise.all([
      fetchInstallerArtifact(`${releaseUrl}/package.json`, "package.json"),
      fetchInstallerArtifact(`${releaseUrl}/package-lock.json`, "package-lock.json")
    ]);
    writeFileSync6(join14(stageDir, "package.json"), packageJsonContent);
    writeFileSync6(join14(stageDir, "package-lock.json"), packageLockContent);
    await runManagedNpmCi(stageDir);
    verifyManagedRelease(stageDir, version);
    renameSync3(stageDir, releaseDir);
    activateManagedRelease(managedRoot, version);
    pruneManagedReleases(managedRoot, version);
  } finally {
    if (stageDir)
      rmSync6(stageDir, { force: true, recursive: true });
    await releaseLock();
  }
}
__name(runManagedSelfUpdate, "runManagedSelfUpdate");
var SELF_UPDATE_NOTE_MARKDOWN_THEME = {
  heading: /* @__PURE__ */ __name((text) => chalk6.bold(chalk6.yellow(text)), "heading"),
  link: /* @__PURE__ */ __name((text) => chalk6.cyan(text), "link"),
  linkUrl: /* @__PURE__ */ __name((text) => chalk6.dim(text), "linkUrl"),
  code: /* @__PURE__ */ __name((text) => chalk6.yellow(text), "code"),
  codeBlock: /* @__PURE__ */ __name((text) => chalk6.dim(text), "codeBlock"),
  codeBlockBorder: /* @__PURE__ */ __name((text) => chalk6.dim(text), "codeBlockBorder"),
  quote: /* @__PURE__ */ __name((text) => chalk6.dim(text), "quote"),
  quoteBorder: /* @__PURE__ */ __name((text) => chalk6.dim(text), "quoteBorder"),
  hr: /* @__PURE__ */ __name((text) => chalk6.dim(text), "hr"),
  listBullet: /* @__PURE__ */ __name((text) => chalk6.yellow(text), "listBullet"),
  bold: /* @__PURE__ */ __name((text) => chalk6.bold(text), "bold"),
  italic: /* @__PURE__ */ __name((text) => chalk6.italic(text), "italic"),
  strikethrough: /* @__PURE__ */ __name((text) => chalk6.strikethrough(text), "strikethrough"),
  underline: /* @__PURE__ */ __name((text) => chalk6.underline(text), "underline")
};
function reportSettingsErrors(settingsManager, context) {
  const errors = settingsManager.drainErrors();
  for (const { scope, error } of errors) {
    console.error(chalk6.yellow(`Warning (${context}, ${scope} settings): ${error.message}`));
    if (error.stack) {
      console.error(chalk6.dim(error.stack));
    }
  }
}
__name(reportSettingsErrors, "reportSettingsErrors");
function getPackageCommandUsage(command) {
  switch (command) {
    case "install":
      return `${APP_NAME} install <source> [-l] [--approve|--no-approve]`;
    case "remove":
      return `${APP_NAME} remove <source> [-l] [--approve|--no-approve]`;
    case "update":
      return `${APP_NAME} update [source|self|pi] [--self|--extensions|--models|--all] [--extension <source>] [--approve|--no-approve] [--force]`;
    case "list":
      return `${APP_NAME} list [--approve|--no-approve]`;
  }
}
__name(getPackageCommandUsage, "getPackageCommandUsage");
var CONFIG_COMMAND_USAGE = `${APP_NAME} config [-l] [--approve|--no-approve]`;
function printConfigCommandHelp() {
  console.log(`${chalk6.bold("Usage:")}
  ${CONFIG_COMMAND_USAGE}

Open the resource configuration TUI to enable or disable package resources.
Without -l, starts in global settings (~/${CONFIG_DIR_NAME}/agent/settings.json).
Press Tab in the TUI to switch between global and project-local modes.

Options:
  -l, --local       Edit project overrides (${CONFIG_DIR_NAME}/settings.json)
  -a, --approve     Trust project-local files for this command with -l
  -na, --no-approve Ignore project-local files for this command with -l
`);
}
__name(printConfigCommandHelp, "printConfigCommandHelp");
function printPackageCommandHelp(command) {
  switch (command) {
    case "install":
      console.log(`${chalk6.bold("Usage:")}
  ${getPackageCommandUsage("install")}

Install a package and add it to settings.

Options:
  -l, --local       Install project-locally (${CONFIG_DIR_NAME}/settings.json)
  -a, --approve     Trust project-local files for this command
  -na, --no-approve Ignore project-local files for this command

Examples:
  ${APP_NAME} install npm:@foo/bar
  ${APP_NAME} install git:github.com/user/repo
  ${APP_NAME} install git:git@github.com:user/repo
  ${APP_NAME} install https://github.com/user/repo
  ${APP_NAME} install ssh://git@github.com/user/repo
  ${APP_NAME} install ./local/path
`);
      return;
    case "remove":
      console.log(`${chalk6.bold("Usage:")}
  ${getPackageCommandUsage("remove")}

Remove a package and its source from settings.
Alias: ${APP_NAME} uninstall <source> [-l]

Options:
  -l, --local       Remove from project settings (${CONFIG_DIR_NAME}/settings.json)
  -a, --approve     Trust project-local files for this command
  -na, --no-approve Ignore project-local files for this command

Examples:
  ${APP_NAME} remove npm:@foo/bar
  ${APP_NAME} uninstall npm:@foo/bar
`);
      return;
    case "update":
      console.log(`${chalk6.bold("Usage:")}
  ${getPackageCommandUsage("update")}

Update pi, installed packages, or model catalogs.

Options:
  --self                  Update pi only (default when no target is given)
  --extensions            Update installed packages only
  --models                Refresh model catalogs only
  --all                   Update pi and installed packages
  --extension <source>    Update one package only
  -a, --approve           Trust project-local files for this command
  -na, --no-approve       Ignore project-local files for this command
  --force                 Reinstall pi even if the current version is latest

Short forms:
  ${APP_NAME} update                Update pi only
  ${APP_NAME} update --all          Update pi and all extensions
  ${APP_NAME} update --models       Refresh model catalogs only
  ${APP_NAME} update <source>       Update one package
  ${APP_NAME} update pi             Update pi only (self works as alias to pi)
`);
      return;
    case "list":
      console.log(`${chalk6.bold("Usage:")}
  ${getPackageCommandUsage("list")}

List installed packages from user and project settings.

Options:
  -a, --approve      Trust project-local files for this command
  -na, --no-approve  Ignore project-local files for this command
`);
      return;
  }
}
__name(printPackageCommandHelp, "printPackageCommandHelp");
function parsePackageCommand(args) {
  const [rawCommand, ...rest] = args;
  let command;
  if (rawCommand === "uninstall") {
    command = "remove";
  } else if (rawCommand === "install" || rawCommand === "remove" || rawCommand === "update" || rawCommand === "list") {
    command = rawCommand;
  }
  if (!command) {
    return void 0;
  }
  let local = false;
  let force = false;
  let projectTrustOverride;
  let help = false;
  let invalidOption;
  let invalidArgument;
  let missingOptionValue;
  let conflictingOptions;
  let source;
  let selfFlag = false;
  let extensionsFlag = false;
  let modelsFlag = false;
  let allFlag = false;
  let extensionFlagSource;
  for (let index = 0; index < rest.length; index++) {
    const arg = rest[index];
    if (arg === "-h" || arg === "--help") {
      help = true;
      continue;
    }
    if (arg === "-l" || arg === "--local") {
      if (command === "install" || command === "remove") {
        local = true;
      } else {
        invalidOption = invalidOption ?? arg;
      }
      continue;
    }
    if (arg === "--self") {
      if (command === "update") {
        selfFlag = true;
      } else {
        invalidOption = invalidOption ?? arg;
      }
      continue;
    }
    if (arg === "--extensions") {
      if (command === "update") {
        extensionsFlag = true;
      } else {
        invalidOption = invalidOption ?? arg;
      }
      continue;
    }
    if (arg === "--models") {
      if (command === "update") {
        modelsFlag = true;
      } else {
        invalidOption = invalidOption ?? arg;
      }
      continue;
    }
    if (arg === "--all") {
      if (command === "update") {
        allFlag = true;
      } else {
        invalidOption = invalidOption ?? arg;
      }
      continue;
    }
    if (arg === "--approve" || arg === "-a") {
      projectTrustOverride = true;
      continue;
    }
    if (arg === "--no-approve" || arg === "-na") {
      projectTrustOverride = false;
      continue;
    }
    if (arg === "--force") {
      if (command === "update") {
        force = true;
      } else {
        invalidOption = invalidOption ?? arg;
      }
      continue;
    }
    if (arg === "--extension") {
      if (command !== "update") {
        invalidOption = invalidOption ?? arg;
        continue;
      }
      const value = rest[index + 1];
      if (!value || value.startsWith("-")) {
        missingOptionValue = missingOptionValue ?? arg;
      } else if (extensionFlagSource) {
        conflictingOptions = conflictingOptions ?? "--extension can only be provided once";
        index++;
      } else {
        extensionFlagSource = value;
        index++;
      }
      continue;
    }
    if (arg.startsWith("-")) {
      invalidOption = invalidOption ?? arg;
      continue;
    }
    if (!source) {
      source = arg;
    } else {
      invalidArgument = invalidArgument ?? arg;
    }
  }
  let updateTarget;
  let showExtensionsSkippedNote = false;
  if (command === "update") {
    if (allFlag && (selfFlag || extensionsFlag || modelsFlag || extensionFlagSource)) {
      conflictingOptions = conflictingOptions ?? "--all cannot be combined with --self, --extensions, --models, or --extension";
    }
    if (allFlag && source) {
      conflictingOptions = conflictingOptions ?? "--all cannot be combined with a positional source";
    }
    if (modelsFlag) {
      if (selfFlag || extensionsFlag || allFlag || extensionFlagSource) {
        conflictingOptions = conflictingOptions ?? "--models cannot be combined with --self, --extensions, --all, or --extension";
      }
      if (source) {
        conflictingOptions = conflictingOptions ?? "--models cannot be combined with a positional source";
      }
      updateTarget = { type: "models" };
    } else if (extensionFlagSource) {
      if (selfFlag || extensionsFlag || allFlag) {
        conflictingOptions = conflictingOptions ?? "--extension cannot be combined with --self, --extensions, or --all";
      }
      if (source) {
        conflictingOptions = conflictingOptions ?? "--extension cannot be combined with a positional source";
      }
      updateTarget = { type: "extensions", source: extensionFlagSource };
    } else if (source) {
      const sourceIsSelf = source === "self" || source === "pi";
      if (sourceIsSelf) {
        updateTarget = extensionsFlag ? { type: "all" } : { type: "self" };
      } else {
        if (extensionsFlag || selfFlag || allFlag) {
          conflictingOptions = conflictingOptions ?? "positional update targets cannot be combined with --self, --extensions, or --all";
        }
        updateTarget = { type: "extensions", source };
      }
    } else if (allFlag) {
      updateTarget = { type: "all" };
    } else if (selfFlag && extensionsFlag) {
      updateTarget = { type: "all" };
    } else if (selfFlag) {
      updateTarget = { type: "self" };
    } else if (extensionsFlag) {
      updateTarget = { type: "extensions" };
    } else {
      updateTarget = { type: "self" };
      showExtensionsSkippedNote = true;
    }
  }
  return {
    command,
    source,
    updateTarget,
    showExtensionsSkippedNote,
    local,
    force,
    projectTrustOverride,
    help,
    invalidOption,
    invalidArgument,
    missingOptionValue,
    conflictingOptions
  };
}
__name(parsePackageCommand, "parsePackageCommand");
function updateTargetIncludesSelf(target) {
  return target.type === "all" || target.type === "self";
}
__name(updateTargetIncludesSelf, "updateTargetIncludesSelf");
function updateTargetIncludesExtensions(target) {
  return target.type === "all" || target.type === "extensions";
}
__name(updateTargetIncludesExtensions, "updateTargetIncludesExtensions");
async function refreshModelCatalogs2(agentDir) {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 15e3);
  try {
    const modelRuntime = await ModelRuntime.create({
      authPath: join14(agentDir, "auth.json"),
      modelsPath: join14(agentDir, "models.json"),
      allowModelNetwork: false,
      signal: controller.signal
    });
    const result = await modelRuntime.refresh({
      allowNetwork: true,
      force: true,
      signal: controller.signal
    });
    if (result.aborted) {
      throw new Error("Model catalog refresh timed out.");
    }
    if (result.errors.size > 0) {
      const details = Array.from(result.errors, ([provider, error]) => `${provider}: ${error.message}`).join("; ");
      throw new Error(`Could not refresh model catalogs: ${details}`);
    }
  } finally {
    clearTimeout(timeout);
  }
  console.log(chalk6.green("Model catalogs refreshed"));
}
__name(refreshModelCatalogs2, "refreshModelCatalogs");
function printSelfUpdateUnavailable(npmCommand, updatePackageTarget = PACKAGE_NAME) {
  console.error(`error: ${APP_NAME} cannot self-update this installation.`);
  console.error(getSelfUpdateUnavailableInstruction(PACKAGE_NAME, npmCommand, updatePackageTarget));
  const entrypoint = process.argv[1];
  if (entrypoint) {
    console.error("");
    console.error(`Location of ${APP_NAME} executable: ${entrypoint}`);
  }
}
__name(printSelfUpdateUnavailable, "printSelfUpdateUnavailable");
function printSelfUpdateFallback(command) {
  console.error(chalk6.dim(`If this keeps failing, run this command yourself: ${command.display}`));
}
__name(printSelfUpdateFallback, "printSelfUpdateFallback");
function printPnpmSelfUpdateMetadataHint() {
  console.error(chalk6.yellow("If pnpm reports missing package versions, its cached registry metadata may be stale."));
  console.error(chalk6.yellow(`Run \`pnpm store prune\` and retry \`${APP_NAME} update --self\`.`));
}
__name(printPnpmSelfUpdateMetadataHint, "printPnpmSelfUpdateMetadataHint");
function printSelfUpdateNote(note) {
  const trimmedNote = note.trim();
  if (!trimmedNote) {
    return;
  }
  console.log();
  console.log(chalk6.bold(chalk6.yellow("Update note")));
  try {
    const width = Math.max(20, process.stdout.columns ?? 80);
    const renderedLines = new Markdown8(trimmedNote, 0, 0, SELF_UPDATE_NOTE_MARKDOWN_THEME).render(width).map((line) => line.trimEnd());
    console.log(renderedLines.join("\n"));
  } catch {
    console.log(trimmedNote);
  }
  console.log();
}
__name(printSelfUpdateNote, "printSelfUpdateNote");
async function getSelfUpdatePlan(force) {
  let latestRelease;
  try {
    latestRelease = await getLatestPiRelease(VERSION, { retry: true });
  } catch (error) {
    throw new Error(`Could not determine latest ${APP_NAME} version: ${formatVersionCheckError(error)}`, {
      cause: error
    });
  }
  if (!latestRelease) {
    throw new Error(`Could not determine latest ${APP_NAME} version.`);
  }
  const packageName = latestRelease.packageName ?? PACKAGE_NAME;
  const installSpec = `${packageName}@${latestRelease.version}`;
  if (force || packageName !== PACKAGE_NAME || isNewerPackageVersion(latestRelease.version, VERSION)) {
    return {
      packageName,
      installSpec,
      version: latestRelease.version,
      ...latestRelease.note ? { note: latestRelease.note } : {},
      shouldRun: true
    };
  }
  console.log(chalk6.green(`${APP_NAME} is already up to date (v${VERSION})`));
  return { packageName, installSpec, version: latestRelease.version, shouldRun: false };
}
__name(getSelfUpdatePlan, "getSelfUpdatePlan");
async function runSelfUpdate(command) {
  console.log(chalk6.dim(`Updating ${APP_NAME} with ${command.display}...`));
  for (const step of command.steps ?? [command]) {
    await new Promise((resolve7, reject) => {
      const child = spawnProcess(step.command, step.args, {
        stdio: "inherit"
      });
      child.on("error", (error) => {
        reject(error);
      });
      child.on("close", (code, signal) => {
        if (code === 0) {
          resolve7();
        } else if (signal) {
          reject(new Error(`${step.display} terminated by signal ${signal}`));
        } else {
          reject(new Error(`${step.display} exited with code ${code ?? "unknown"}`));
        }
      });
    });
  }
}
__name(runSelfUpdate, "runSelfUpdate");
function prepareWindowsNpmSelfUpdate() {
  if (process.platform !== "win32") {
    return;
  }
  const packageDir = getPackageDir();
  cleanupWindowsSelfUpdateQuarantine(packageDir);
  quarantineWindowsNativeDependencies(packageDir);
}
__name(prepareWindowsNpmSelfUpdate, "prepareWindowsNpmSelfUpdate");
function getCommandAppMode() {
  return process.stdin.isTTY && process.stdout.isTTY ? "interactive" : "print";
}
__name(getCommandAppMode, "getCommandAppMode");
function reportProjectTrustWarnings(warnings) {
  for (const warning of warnings) {
    console.error(chalk6.yellow(`Warning: ${warning}`));
  }
}
__name(reportProjectTrustWarnings, "reportProjectTrustWarnings");
async function createCommandSettingsManager(options) {
  const settingsManager = SettingsManager.create(options.cwd, options.agentDir, { projectTrusted: false });
  const projectTrustWarnings = [];
  const trustStore = new ProjectTrustStore(options.agentDir);
  if (options.useSavedProjectTrustOnly) {
    const savedProjectTrusted = trustStore.get(options.cwd) === true;
    settingsManager.setProjectTrusted(options.projectTrustOverride ?? savedProjectTrusted);
    return { settingsManager, projectTrustWarnings };
  }
  const appMode = getCommandAppMode();
  const extensionsResult = options.projectTrustOverride === void 0 && hasTrustRequiringProjectResources(options.cwd) ? await new DefaultResourceLoader({
    cwd: options.cwd,
    agentDir: options.agentDir,
    settingsManager,
    extensionFactories: options.extensionFactories
  }).loadProjectTrustExtensions() : void 0;
  for (const error of extensionsResult?.errors ?? []) {
    projectTrustWarnings.push(`Failed to load extension "${error.path}": ${error.error}`);
  }
  const projectTrusted = await resolveProjectTrusted({
    cwd: options.cwd,
    trustStore,
    trustOverride: options.projectTrustOverride,
    defaultProjectTrust: settingsManager.getDefaultProjectTrust(),
    extensionsResult,
    projectTrustContext: createProjectTrustContext({
      cwd: options.cwd,
      mode: appMode,
      settingsManager,
      hasUI: appMode === "interactive"
    }),
    onExtensionError: /* @__PURE__ */ __name((message) => projectTrustWarnings.push(message), "onExtensionError")
  });
  settingsManager.setProjectTrusted(projectTrusted);
  return { settingsManager, projectTrustWarnings };
}
__name(createCommandSettingsManager, "createCommandSettingsManager");
async function handleConfigCommand(args, runtimeOptions = {}) {
  const [command, ...rest] = args;
  if (command !== "config") {
    return false;
  }
  if (rest.includes("-h") || rest.includes("--help")) {
    printConfigCommandHelp();
    return true;
  }
  let local = false;
  let projectTrustOverride;
  for (const arg of rest) {
    if (arg === "-l" || arg === "--local") {
      local = true;
    } else if (arg === "-a" || arg === "--approve") {
      projectTrustOverride = true;
    } else if (arg === "-na" || arg === "--no-approve") {
      projectTrustOverride = false;
    } else if (arg.startsWith("-")) {
      console.error(chalk6.red(`Unknown option ${arg} for "config".`));
      console.error(chalk6.dim(`Use "${APP_NAME} --help" or "${CONFIG_COMMAND_USAGE}".`));
      process.exitCode = 1;
      return true;
    } else {
      console.error(chalk6.red(`Unexpected argument ${arg}.`));
      console.error(chalk6.dim(`Usage: ${CONFIG_COMMAND_USAGE}`));
      process.exitCode = 1;
      return true;
    }
  }
  const cwd = process.cwd();
  const agentDir = getAgentDir();
  const { settingsManager, projectTrustWarnings } = await createCommandSettingsManager({
    cwd,
    agentDir,
    projectTrustOverride,
    extensionFactories: runtimeOptions.extensionFactories
  });
  reportProjectTrustWarnings(projectTrustWarnings);
  if (local && !settingsManager.isProjectTrusted()) {
    console.error(chalk6.red("Project is not trusted. Use --approve to modify local resource config."));
    process.exitCode = 1;
    return true;
  }
  reportSettingsErrors(settingsManager, "config command");
  const builtinExtensions = (runtimeOptions.extensionFactories ?? []).filter(isBuiltinExtension).map((input2) => input2.name);
  const globalSettingsManager = SettingsManager.create(cwd, agentDir, { projectTrusted: false });
  const globalResolvedPaths = await new DefaultPackageManager({
    cwd,
    agentDir,
    settingsManager: globalSettingsManager,
    builtinExtensions
  }).resolve();
  const projectResolvedPaths = settingsManager.isProjectTrusted() ? await new DefaultPackageManager({ cwd, agentDir, settingsManager, builtinExtensions }).resolve() : globalResolvedPaths;
  await selectConfig({
    resolvedPaths: { global: globalResolvedPaths, project: projectResolvedPaths },
    settingsManager,
    cwd,
    agentDir,
    writeScope: local ? "project" : "global",
    projectModeAvailable: settingsManager.isProjectTrusted()
  });
  process.exit(0);
}
__name(handleConfigCommand, "handleConfigCommand");
async function handlePackageCommand(args, runtimeOptions = {}) {
  const options = parsePackageCommand(args);
  if (!options) {
    return false;
  }
  if (options.help) {
    printPackageCommandHelp(options.command);
    return true;
  }
  if (options.invalidOption) {
    console.error(chalk6.red(`Unknown option ${options.invalidOption} for "${options.command}".`));
    console.error(chalk6.dim(`Use "${APP_NAME} --help" or "${getPackageCommandUsage(options.command)}".`));
    process.exitCode = 1;
    return true;
  }
  if (options.missingOptionValue) {
    console.error(chalk6.red(`Missing value for ${options.missingOptionValue}.`));
    console.error(chalk6.dim(`Usage: ${getPackageCommandUsage(options.command)}`));
    process.exitCode = 1;
    return true;
  }
  if (options.invalidArgument) {
    console.error(chalk6.red(`Unexpected argument ${options.invalidArgument}.`));
    console.error(chalk6.dim(`Usage: ${getPackageCommandUsage(options.command)}`));
    process.exitCode = 1;
    return true;
  }
  if (options.conflictingOptions) {
    console.error(chalk6.red(options.conflictingOptions));
    console.error(chalk6.dim(`Usage: ${getPackageCommandUsage(options.command)}`));
    process.exitCode = 1;
    return true;
  }
  const source = options.source;
  if ((options.command === "install" || options.command === "remove") && !source) {
    console.error(chalk6.red(`Missing ${options.command} source.`));
    console.error(chalk6.dim(`Usage: ${getPackageCommandUsage(options.command)}`));
    process.exitCode = 1;
    return true;
  }
  if (options.command === "update" && options.updateTarget?.type === "models") {
    try {
      await refreshModelCatalogs2(getAgentDir());
    } catch (error) {
      const message = error instanceof Error ? error.message : "Unknown model catalog refresh error";
      console.error(chalk6.red(`Error: ${message}`));
      process.exitCode = 1;
    }
    return true;
  }
  const cwd = process.cwd();
  const agentDir = getAgentDir();
  const writesProjectPackageConfig = (options.command === "install" || options.command === "remove") && options.local;
  const { settingsManager, projectTrustWarnings } = await createCommandSettingsManager({
    cwd,
    agentDir,
    projectTrustOverride: options.projectTrustOverride,
    useSavedProjectTrustOnly: options.command === "update",
    extensionFactories: runtimeOptions.extensionFactories
  });
  reportProjectTrustWarnings(projectTrustWarnings);
  if (!settingsManager.isProjectTrusted() && writesProjectPackageConfig) {
    console.error(chalk6.red("Project is not trusted. Use --approve to modify local package config."));
    process.exitCode = 1;
    return true;
  }
  reportSettingsErrors(settingsManager, "package command");
  const selfUpdateNpmCommand = settingsManager.getGlobalSettings().npmCommand;
  const packageManager = new DefaultPackageManager({ cwd, agentDir, settingsManager });
  packageManager.setProgressCallback((event) => {
    if (event.type === "start") {
      process.stdout.write(chalk6.dim(`${event.message}
`));
    }
  });
  try {
    switch (options.command) {
      case "install":
        await packageManager.installAndPersist(source, { local: options.local });
        console.log(chalk6.green(`Installed ${source}`));
        return true;
      case "remove": {
        const removed = await packageManager.removeAndPersist(source, { local: options.local });
        if (!removed) {
          console.error(chalk6.red(`No matching package found for ${source}`));
          process.exitCode = 1;
          return true;
        }
        console.log(chalk6.green(`Removed ${source}`));
        return true;
      }
      case "list": {
        const configuredPackages = packageManager.listConfiguredPackages();
        const userPackages = configuredPackages.filter((pkg) => pkg.scope === "user");
        const projectPackages = configuredPackages.filter((pkg) => pkg.scope === "project");
        if (configuredPackages.length === 0) {
          console.log(chalk6.dim("No packages installed."));
          return true;
        }
        const formatPackage = /* @__PURE__ */ __name((pkg) => {
          const display = pkg.filtered ? `${pkg.source} (filtered)` : pkg.source;
          console.log(`  ${display}`);
          if (pkg.installedPath) {
            console.log(chalk6.dim(`    ${pkg.installedPath}`));
          }
        }, "formatPackage");
        if (userPackages.length > 0) {
          console.log(chalk6.bold("User packages:"));
          for (const pkg of userPackages) {
            formatPackage(pkg);
          }
        }
        if (projectPackages.length > 0) {
          if (userPackages.length > 0)
            console.log();
          console.log(chalk6.bold("Project packages:"));
          for (const pkg of projectPackages) {
            formatPackage(pkg);
          }
        }
        return true;
      }
      case "update": {
        const target = options.updateTarget ?? { type: "self" };
        if (options.showExtensionsSkippedNote) {
          console.log(chalk6.dim(`Extensions are skipped. Run ${APP_NAME} update --extensions to update extensions.`));
        }
        if (updateTargetIncludesExtensions(target)) {
          const updateSource = target.type === "extensions" ? target.source : void 0;
          await packageManager.update(updateSource);
          if (updateSource) {
            console.log(chalk6.green(`Updated ${updateSource}`));
          } else {
            console.log(chalk6.green("Updated packages"));
          }
        }
        if (updateTargetIncludesSelf(target)) {
          const managedInstallRoot = getActiveManagedInstallRoot();
          if (managedInstallRoot && options.force) {
            console.error(chalk6.red(`Managed ${APP_NAME} installations do not support --force; rerun the installer to repair this installation.`));
            process.exitCode = 1;
            return true;
          }
          const selfUpdatePlan = await getSelfUpdatePlan(options.force);
          if (!selfUpdatePlan.shouldRun) {
            return true;
          }
          if (managedInstallRoot) {
            if (selfUpdatePlan.note) {
              printSelfUpdateNote(selfUpdatePlan.note);
            }
            try {
              console.log(chalk6.dim(`Updating managed ${APP_NAME} installation...`));
              await runManagedSelfUpdate(managedInstallRoot, selfUpdatePlan.version);
            } catch (error) {
              const message = error instanceof Error ? error.message : "Unknown managed update error";
              console.error(chalk6.red(`Error: ${message}`));
              process.exitCode = 1;
              return true;
            }
            console.log(chalk6.green(`Updated ${APP_NAME} from ${VERSION} to ${selfUpdatePlan.version}`));
            return true;
          }
          const installMethod = detectInstallMethod();
          if (process.platform === "win32" && installMethod !== "npm" && installMethod !== "pnpm") {
            console.error(chalk6.red(`${APP_NAME} self-update on Windows is only supported for npm and pnpm installs.`));
            console.error(chalk6.dim(`Detected install method: ${installMethod}. Update ${APP_NAME} manually.`));
            process.exitCode = 1;
            return true;
          }
          const selfUpdateTarget = {
            packageName: selfUpdatePlan.packageName,
            installSpec: selfUpdatePlan.installSpec
          };
          const selfUpdateCommand = getSelfUpdateCommand(PACKAGE_NAME, selfUpdateNpmCommand, selfUpdateTarget);
          if (!selfUpdateCommand) {
            printSelfUpdateUnavailable(selfUpdateNpmCommand, selfUpdateTarget);
            process.exitCode = 1;
            return true;
          }
          if (selfUpdatePlan.note) {
            printSelfUpdateNote(selfUpdatePlan.note);
          }
          try {
            if (installMethod === "npm") {
              prepareWindowsNpmSelfUpdate();
            }
            await runSelfUpdate(selfUpdateCommand);
          } catch (error) {
            const message = error instanceof Error ? error.message : "Unknown package command error";
            console.error(chalk6.red(`Error: ${message}`));
            if (installMethod === "pnpm") {
              printPnpmSelfUpdateMetadataHint();
            }
            printSelfUpdateFallback(selfUpdateCommand);
            process.exitCode = 1;
            return true;
          }
          console.log(chalk6.green(`Updated ${APP_NAME} from ${VERSION} to ${selfUpdatePlan.version}`));
          if (installMethod === "npm") {
            const installerCommand = process.platform === "win32" ? 'powershell -c "irm https://pi.dev/install.ps1 | iex"' : "curl -fsSL https://pi.dev/install.sh | sh";
            console.log();
            console.log(chalk6.yellow(`This npm installation of ${APP_NAME} does not pin its dependencies.`));
            console.log(chalk6.yellow("Run the installer to migrate to a managed installation that does:"));
            console.log();
            console.log(`  ${chalk6.bold(installerCommand)}`);
          }
        }
        return true;
      }
    }
  } catch (error) {
    const message = error instanceof Error ? error.message : "Unknown package command error";
    console.error(chalk6.red(`Error: ${message}`));
    process.exitCode = 1;
    return true;
  }
}
__name(handlePackageCommand, "handlePackageCommand");

// pi-dist/pi-coding-agent/main.js
var EXTENSION_LOAD_FAILURE_HINT = `Hint: Start without extensions using "${APP_NAME} -ne".`;
async function readPipedStdin() {
  if (process.stdin.isTTY) {
    return void 0;
  }
  return new Promise((resolve7) => {
    let data = "";
    process.stdin.setEncoding("utf8");
    process.stdin.on("data", (chunk) => {
      data += chunk;
    });
    process.stdin.on("end", () => {
      resolve7(data.trim() || void 0);
    });
    process.stdin.resume();
  });
}
__name(readPipedStdin, "readPipedStdin");
function reportDiagnostics(diagnostics) {
  for (const diagnostic of diagnostics) {
    const color = diagnostic.type === "error" ? chalk7.red : diagnostic.type === "warning" ? chalk7.yellow : chalk7.dim;
    const prefix = diagnostic.type === "error" ? "Error: " : diagnostic.type === "warning" ? "Warning: " : "";
    console.error(color(`${prefix}${diagnostic.message}`));
  }
}
__name(reportDiagnostics, "reportDiagnostics");
function isTruthyEnvFlag(value) {
  if (!value)
    return false;
  return value === "1" || value.toLowerCase() === "true" || value.toLowerCase() === "yes";
}
__name(isTruthyEnvFlag, "isTruthyEnvFlag");
function resolveAppMode(parsed, stdinIsTTY, stdoutIsTTY) {
  if (parsed.mode === "rpc") {
    return "rpc";
  }
  if (parsed.mode === "json") {
    return "json";
  }
  if (parsed.print || !stdinIsTTY || !stdoutIsTTY) {
    return "print";
  }
  return "interactive";
}
__name(resolveAppMode, "resolveAppMode");
function toPrintOutputMode(appMode) {
  return appMode === "json" ? "json" : "text";
}
__name(toPrintOutputMode, "toPrintOutputMode");
function isPlainRuntimeMetadataCommand(parsed) {
  return !parsed.print && parsed.mode === void 0 && (parsed.help === true || parsed.listModels !== void 0);
}
__name(isPlainRuntimeMetadataCommand, "isPlainRuntimeMetadataCommand");
async function runAuthCommand(args) {
  if (isAuthCommandHelp(args)) {
    printAuthCommandHelp();
    return true;
  }
  let command;
  try {
    command = parseAuthCommand(args);
  } catch (error) {
    const message = error instanceof AuthCommandError ? error.message : "Failed to parse auth command";
    console.error(chalk7.red(`Error: ${message}`));
    process.exitCode = 1;
    return true;
  }
  if (!command)
    return false;
  const parsed = parseArgs(command.args);
  if (parsed.unknownFlags.size > 0) {
    const option = parsed.unknownFlags.keys().next().value;
    console.error(chalk7.red(`Unknown option --${option} for "${getAuthCommandName(command.kind)}".`));
    console.error(chalk7.dim(`Use "${APP_NAME} --help" or "${getAuthCommandUsage(command.kind)}".`));
    process.exitCode = 1;
    return true;
  }
  try {
    if (parsed.diagnostics.length > 0) {
      throw new AuthCommandError(parsed.diagnostics.map((diagnostic) => diagnostic.message).join("\n"));
    }
    if (command.kind !== "check") {
      const signal = AbortSignal.timeout(15e3);
      const modelRuntime = await ModelRuntime.create({ allowModelNetwork: false, signal });
      const credential2 = await resolveCredentialForPrint(parsed, modelRuntime, command.kind, command.minExpiryMs, signal);
      process.stdout.write(`${credential2}
`);
      return true;
    }
    const requestedAuth = validateAuthCommandArgs(parsed, command.kind);
    let result;
    let credential;
    try {
      const credentials = command.noRefresh ? new ReadOnlyAuthStorage() : AuthStorage.create();
      const modelRuntime = await createAuthCheckModelRuntime(credentials);
      result = await checkProviderAuth(parsed, modelRuntime, { refresh: !command.noRefresh });
      if (command.credentials && result.status === "ready") {
        credential = await getProviderCredential(result.provider, modelRuntime, credentials, {
          refresh: !command.noRefresh
        });
        if (!credential) {
          result = { status: "not_ready", provider: result.provider, reason: "credential_not_available" };
        }
      }
    } catch {
      result = {
        status: "invalid",
        provider: requestedAuth.provider ?? requestedAuth.model,
        reason: "invalid_state"
      };
    }
    const output = command.json ? JSON.stringify({ ...result, ...credential ? { credentials: credential } : {} }) : credential ?? result.status;
    process.stdout.write(`${output}
`);
    process.exitCode = result.status === "ready" ? 0 : result.status === "not_ready" ? 1 : 2;
  } catch (error) {
    const message = error instanceof AuthCommandError ? error.message : "Failed to resolve credential";
    console.error(chalk7.red(`Error: ${message}`));
    process.exitCode = command.kind === "check" ? 2 : 1;
  }
  return true;
}
__name(runAuthCommand, "runAuthCommand");
async function prepareInitialMessage(parsed, stdinContent) {
  if (parsed.fileArgs.length === 0) {
    return buildInitialMessage({ parsed, stdinContent });
  }
  const { text, images } = await processFileArguments(parsed.fileArgs, { autoResizeImages: false });
  return buildInitialMessage({
    parsed,
    fileText: text,
    fileImages: images,
    stdinContent
  });
}
__name(prepareInitialMessage, "prepareInitialMessage");
function findLocalSessionByExactId(sessionId, cwd, sessionDir) {
  const path5 = SessionManager.findById(cwd, sessionId, sessionDir);
  return path5 ? { type: "local", path: path5 } : void 0;
}
__name(findLocalSessionByExactId, "findLocalSessionByExactId");
async function resolveSessionPath(sessionArg, cwd, sessionDir) {
  if (sessionArg.includes("/") || sessionArg.includes("\\") || sessionArg.endsWith(".jsonl")) {
    return { type: "path", path: resolvePath(sessionArg, cwd) };
  }
  const exactLocalMatch = findLocalSessionByExactId(sessionArg, cwd, sessionDir);
  if (exactLocalMatch) {
    return exactLocalMatch;
  }
  const localSessions = await SessionManager.list(cwd, sessionDir);
  const localMatch = localSessions.find((s) => s.id.startsWith(sessionArg));
  if (localMatch) {
    return { type: "local", path: localMatch.path };
  }
  const allSessions = await SessionManager.listAll(sessionDir);
  const globalMatch = allSessions.find((s) => s.id === sessionArg) ?? allSessions.find((s) => s.id.startsWith(sessionArg));
  if (globalMatch) {
    return { type: "global", path: globalMatch.path, cwd: globalMatch.cwd };
  }
  return { type: "not_found", arg: sessionArg };
}
__name(resolveSessionPath, "resolveSessionPath");
async function promptConfirm(message) {
  return new Promise((resolve7) => {
    const rl = createInterface({
      input: process.stdin,
      output: process.stdout
    });
    rl.question(`${message} [y/N] `, (answer) => {
      rl.close();
      resolve7(answer.toLowerCase() === "y" || answer.toLowerCase() === "yes");
    });
  });
}
__name(promptConfirm, "promptConfirm");
function validateForkFlags(parsed) {
  if (!parsed.fork)
    return;
  const conflictingFlags = [
    parsed.session ? "--session" : void 0,
    parsed.continue ? "--continue" : void 0,
    parsed.resume ? "--resume" : void 0,
    parsed.noSession ? "--no-session" : void 0
  ].filter((flag) => flag !== void 0);
  if (conflictingFlags.length > 0) {
    console.error(chalk7.red(`Error: --fork cannot be combined with ${conflictingFlags.join(", ")}`));
    process.exit(1);
  }
}
__name(validateForkFlags, "validateForkFlags");
function validateSessionIdFlags(parsed) {
  if (parsed.sessionId === void 0)
    return;
  const conflictingFlags = [
    parsed.session ? "--session" : void 0,
    parsed.continue ? "--continue" : void 0,
    parsed.resume ? "--resume" : void 0
  ].filter((flag) => flag !== void 0);
  if (conflictingFlags.length > 0) {
    console.error(chalk7.red(`Error: --session-id cannot be combined with ${conflictingFlags.join(", ")}`));
    process.exit(1);
  }
  try {
    assertValidSessionId(parsed.sessionId);
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    console.error(chalk7.red(`Error: ${message}`));
    process.exit(1);
  }
}
__name(validateSessionIdFlags, "validateSessionIdFlags");
function openSessionOrExit(path5, sessionDir) {
  try {
    return SessionManager.open(path5, sessionDir);
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    console.error(chalk7.red(`Error: ${message}`));
    process.exit(1);
  }
}
__name(openSessionOrExit, "openSessionOrExit");
function forkSessionOrExit(sourcePath, cwd, sessionDir, sessionId) {
  try {
    return SessionManager.forkFrom(sourcePath, cwd, sessionDir, { id: sessionId });
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    console.error(chalk7.red(`Error: ${message}`));
    process.exit(1);
  }
}
__name(forkSessionOrExit, "forkSessionOrExit");
async function createSessionManager(parsed, cwd, sessionDir, settingsManager) {
  if (parsed.noSession || parsed.help || parsed.listModels !== void 0) {
    return SessionManager.inMemory(cwd, parsed.sessionId !== void 0 ? { id: parsed.sessionId } : void 0);
  }
  if (parsed.fork) {
    if (parsed.sessionId) {
      const existingTarget = findLocalSessionByExactId(parsed.sessionId, cwd, sessionDir);
      if (existingTarget) {
        console.error(chalk7.red(`Session already exists with id '${parsed.sessionId}'`));
        process.exit(1);
      }
    }
    const resolved = await resolveSessionPath(parsed.fork, cwd, sessionDir);
    switch (resolved.type) {
      case "path":
      case "local":
      case "global":
        return forkSessionOrExit(resolved.path, cwd, sessionDir, parsed.sessionId);
      case "not_found":
        console.error(chalk7.red(`No session found matching '${resolved.arg}'`));
        process.exit(1);
    }
  }
  if (parsed.session) {
    const resolved = await resolveSessionPath(parsed.session, cwd, sessionDir);
    switch (resolved.type) {
      case "path":
      case "local":
        return openSessionOrExit(resolved.path, sessionDir);
      case "global": {
        console.log(chalk7.yellow(`Session found in different project: ${resolved.cwd}`));
        const shouldFork = await promptConfirm("Fork this session into current directory?");
        if (!shouldFork) {
          console.log(chalk7.dim("Aborted."));
          process.exit(0);
        }
        return forkSessionOrExit(resolved.path, cwd, sessionDir);
      }
      case "not_found":
        console.error(chalk7.red(`No session found matching '${resolved.arg}'`));
        process.exit(1);
    }
  }
  if (parsed.resume) {
    try {
      const selectedPath = await selectSession((onProgress, signal) => SessionManager.list(cwd, sessionDir, onProgress, signal), (onProgress, signal) => SessionManager.listAll(sessionDir, onProgress, signal), settingsManager);
      if (!selectedPath) {
        console.log(chalk7.dim("No session selected"));
        process.exit(0);
      }
      return SessionManager.open(selectedPath, sessionDir);
    } finally {
      stopThemeWatcher();
    }
  }
  if (parsed.continue) {
    return SessionManager.continueRecent(cwd, sessionDir);
  }
  if (parsed.sessionId) {
    const existingSession = findLocalSessionByExactId(parsed.sessionId, cwd, sessionDir);
    if (existingSession) {
      return SessionManager.open(existingSession.path, sessionDir);
    }
    console.error(chalk7.yellow(`Warning: No project session found with id '${parsed.sessionId}'; creating a new session with that id.`));
  }
  return SessionManager.create(cwd, sessionDir, { id: parsed.sessionId });
}
__name(createSessionManager, "createSessionManager");
function buildSessionOptions(parsed, scopedModels, hasExistingSession, modelRuntime, settingsManager) {
  const options = {};
  const diagnostics = [];
  let cliThinkingFromModel = false;
  if (parsed.provider && !parsed.model) {
    diagnostics.push({
      type: "error",
      message: `--provider requires --model (for example: --provider ${parsed.provider} --model <pattern>)`
    });
  }
  if (parsed.model) {
    const resolved = resolveCliModel({
      cliProvider: parsed.provider,
      cliModel: parsed.model,
      cliThinking: parsed.thinking,
      modelRuntime
    });
    if (resolved.warning) {
      diagnostics.push({ type: "warning", message: resolved.warning });
    }
    if (resolved.error) {
      diagnostics.push({ type: "error", message: resolved.error });
    }
    if (resolved.model) {
      options.model = resolved.model;
      if (!parsed.thinking && resolved.thinkingLevel) {
        options.thinkingLevel = resolved.thinkingLevel;
        cliThinkingFromModel = true;
      }
    }
  }
  if (!options.model && scopedModels.length > 0 && !hasExistingSession) {
    const savedProvider = settingsManager.getDefaultProvider();
    const savedModelId = settingsManager.getDefaultModel();
    const savedModel = savedProvider && savedModelId ? modelRuntime.getModel(savedProvider, savedModelId) : void 0;
    const savedInScope = savedModel ? scopedModels.find((sm) => modelsAreEqual2(sm.model, savedModel)) : void 0;
    if (savedInScope) {
      options.model = savedInScope.model;
      if (!parsed.thinking && savedInScope.thinkingLevel) {
        options.thinkingLevel = savedInScope.thinkingLevel;
      }
    } else {
      options.model = scopedModels[0].model;
      if (!parsed.thinking && scopedModels[0].thinkingLevel) {
        options.thinkingLevel = scopedModels[0].thinkingLevel;
      }
    }
  }
  if (parsed.thinking) {
    options.thinkingLevel = parsed.thinking;
  }
  if (scopedModels.length > 0) {
    options.scopedModels = scopedModels.map((sm) => ({
      model: sm.model,
      thinkingLevel: sm.thinkingLevel
    }));
  }
  if (parsed.noTools) {
    options.noTools = "all";
  } else if (parsed.noBuiltinTools) {
    options.noTools = "builtin";
  }
  if (parsed.tools) {
    options.tools = [...parsed.tools];
  }
  if (parsed.excludeTools) {
    options.excludeTools = [...parsed.excludeTools];
  }
  return { options, cliThinkingFromModel, diagnostics };
}
__name(buildSessionOptions, "buildSessionOptions");
function resolveCliPaths(cwd, paths) {
  return paths?.map((value) => isLocalPath(value) ? resolvePath(value, cwd) : value);
}
__name(resolveCliPaths, "resolveCliPaths");
async function promptForMissingSessionCwd(issue, settingsManager) {
  return showStartupSelector(settingsManager, formatMissingSessionCwdPrompt(issue), [
    { label: "Continue", value: issue.fallbackCwd },
    { label: "Cancel", value: void 0 }
  ]);
}
__name(promptForMissingSessionCwd, "promptForMissingSessionCwd");
async function main(args, options) {
  resetTimings();
  const extensionFactories = [...builtInExtensions, ...options?.extensionFactories ?? []];
  const offlineMode = args.includes("--offline") || isTruthyEnvFlag(process.env.PI_OFFLINE);
  if (offlineMode) {
    process.env.PI_OFFLINE = "1";
    process.env.PI_SKIP_VERSION_CHECK = "1";
  }
  if (await runAuthCommand(args)) {
    return;
  }
  if (process.platform === "win32") {
    cleanupWindowsSelfUpdateQuarantine(getPackageDir());
  }
  cleanupManagedInstall();
  const cwd = process.cwd();
  const agentDir = getAgentDir();
  const bootstrapSettingsManager = SettingsManager.create(cwd, agentDir, { projectTrusted: false });
  applyHttpProxySettings(bootstrapSettingsManager.getGlobalSettings().httpProxy);
  configureHttpDispatcher();
  if (await handlePackageCommand(args, { extensionFactories })) {
    const exitCode = process.exitCode ?? 0;
    if (process.platform === "win32" && exitCode === 0 && args[0] === "update") {
      return;
    }
    process.exit(exitCode);
    return;
  }
  if (await handleConfigCommand(args, { extensionFactories })) {
    return;
  }
  if (args[0] === "mcp") {
    const { runMcpCommand } = await loadMcpCommand();
    process.exitCode = await runMcpCommand(args.slice(1), { cwd, agentDir });
    return;
  }
  const parsed = parseArgs(args);
  if (parsed.diagnostics.length > 0) {
    for (const d of parsed.diagnostics) {
      const color = d.type === "error" ? chalk7.red : chalk7.yellow;
      console.error(color(`${d.type === "error" ? "Error" : "Warning"}: ${d.message}`));
    }
    if (parsed.diagnostics.some((d) => d.type === "error")) {
      process.exit(1);
    }
  }
  time("parseArgs");
  if (parsed.version) {
    console.log(VERSION);
    process.exit(0);
  }
  if (parsed.export) {
    let result;
    try {
      const outputPath = parsed.messages.length > 0 ? parsed.messages[0] : void 0;
      result = await exportFromFile(parsed.export, outputPath);
    } catch (error) {
      const message = error instanceof Error ? error.message : "Failed to export session";
      console.error(chalk7.red(`Error: ${message}`));
      process.exit(1);
    }
    console.log(`Exported to: ${result}`);
    process.exit(0);
  }
  let appMode = resolveAppMode(parsed, process.stdin.isTTY, process.stdout.isTTY);
  const shouldTakeOverStdout = appMode !== "interactive" && !isPlainRuntimeMetadataCommand(parsed);
  if (shouldTakeOverStdout) {
    takeOverStdout();
  }
  if (parsed.mode === "rpc" && parsed.fileArgs.length > 0) {
    console.error(chalk7.red("Error: @file arguments are not supported in RPC mode"));
    process.exit(1);
  }
  validateForkFlags(parsed);
  validateSessionIdFlags(parsed);
  const { migratedAuthProviders: migratedProviders, deprecationWarnings } = runMigrations(cwd);
  time("runMigrations");
  const startupSettingsManager = SettingsManager.create(cwd, agentDir);
  const startupSettingsDiagnostics = collectSettingsDiagnostics(startupSettingsManager);
  if (appMode === "interactive" && !parsed.help && parsed.listModels === void 0 && shouldRunFirstTimeSetup()) {
    await showFirstTimeSetup(startupSettingsManager);
    time("firstTimeSetup");
  }
  if (appMode === "interactive" && parsed.useTheme !== void 0) {
    startupSettingsManager.applyOverrides({ theme: parsed.useTheme });
  }
  const envSessionDir = process.env[ENV_SESSION_DIR];
  const sessionDir = (parsed.sessionDir ? normalizePath(parsed.sessionDir) : void 0) ?? (envSessionDir ? expandTildePath(envSessionDir) : void 0) ?? startupSettingsManager.getSessionDir();
  let sessionManager = await createSessionManager(parsed, cwd, sessionDir, startupSettingsManager);
  const missingSessionCwdIssue = getMissingSessionCwdIssue(sessionManager, cwd);
  if (missingSessionCwdIssue) {
    if (appMode === "interactive") {
      const selectedCwd = await promptForMissingSessionCwd(missingSessionCwdIssue, startupSettingsManager);
      if (!selectedCwd) {
        process.exit(0);
      }
      sessionManager = SessionManager.open(missingSessionCwdIssue.sessionFile, sessionDir, selectedCwd);
    } else {
      console.error(chalk7.red(new MissingSessionCwdError(missingSessionCwdIssue).message));
      process.exit(1);
    }
  }
  if (parsed.name !== void 0) {
    const name = normalizeSessionName(parsed.name);
    if (name === void 0) {
      console.error(chalk7.red("Error: --name requires a non-empty value"));
      process.exit(1);
    }
    sessionManager.appendSessionInfo(name);
  }
  time("createSessionManager");
  const trustStore = new ProjectTrustStore(agentDir);
  const sessionCwd = sessionManager.getCwd();
  const autoTrustOnReloadCwd = parsed.projectTrustOverride === void 0 && !hasTrustRequiringProjectResources(sessionCwd) ? sessionCwd : void 0;
  const trustPromptMode = parsed.help || parsed.listModels !== void 0 ? "print" : appMode;
  const projectTrustByCwd = /* @__PURE__ */ new Map();
  const resolvedExtensionPaths = resolveCliPaths(cwd, parsed.extensions);
  const resolvedSkillPaths = resolveCliPaths(cwd, parsed.skills);
  const resolvedPromptTemplatePaths = resolveCliPaths(cwd, parsed.promptTemplates);
  const resolvedThemePaths = resolveCliPaths(cwd, parsed.themes);
  const createRuntime = /* @__PURE__ */ __name(async ({ cwd: cwd2, agentDir: agentDir2, sessionManager: sessionManager2, sessionStartEvent, projectTrustContext }) => {
    const isInitialRuntime = sessionStartEvent === void 0;
    const projectTrustDiagnostics = [];
    const cachedProjectTrust = projectTrustByCwd.get(cwd2);
    const hasTrustRequiringResources = hasTrustRequiringProjectResources(cwd2);
    const shouldResolveProjectTrust = parsed.projectTrustOverride === void 0 && cachedProjectTrust === void 0 && hasTrustRequiringResources;
    const projectTrusted = shouldResolveProjectTrust ? false : cachedProjectTrust ?? parsed.projectTrustOverride ?? (!hasTrustRequiringResources || trustStore.get(cwd2) === true);
    const runtimeSettingsManager = SettingsManager.create(cwd2, agentDir2, { projectTrusted });
    const services2 = await createAgentSessionServices({
      cwd: cwd2,
      agentDir: agentDir2,
      settingsManager: runtimeSettingsManager,
      modelRuntimeSignal: AbortSignal.timeout(15e3),
      extensionFlagValues: parsed.unknownFlags,
      resourceLoaderReloadOptions: shouldResolveProjectTrust ? {
        resolveProjectTrust: /* @__PURE__ */ __name(async ({ extensionsResult }) => {
          const trusted = await resolveProjectTrusted({
            cwd: cwd2,
            trustStore,
            trustOverride: parsed.projectTrustOverride,
            defaultProjectTrust: startupSettingsManager.getDefaultProjectTrust(),
            extensionsResult,
            projectTrustContext: projectTrustContext ?? createProjectTrustContext({
              cwd: cwd2,
              mode: isInitialRuntime ? trustPromptMode : appMode,
              settingsManager: startupSettingsManager,
              hasUI: isInitialRuntime && trustPromptMode === "interactive"
            }),
            onExtensionError: /* @__PURE__ */ __name((message) => projectTrustDiagnostics.push({ type: "warning", message }), "onExtensionError")
          });
          projectTrustByCwd.set(cwd2, trusted);
          return trusted;
        }, "resolveProjectTrust")
      } : void 0,
      resourceLoaderOptions: {
        additionalExtensionPaths: resolvedExtensionPaths,
        additionalSkillPaths: resolvedSkillPaths,
        additionalPromptTemplatePaths: resolvedPromptTemplatePaths,
        additionalThemePaths: resolvedThemePaths,
        noExtensions: parsed.noExtensions,
        disabledBuiltinExtensions: parsed.noMcp ? ["mcp"] : void 0,
        noSkills: parsed.noSkills,
        noPromptTemplates: parsed.noPromptTemplates,
        noThemes: parsed.noThemes,
        noContextFiles: parsed.noContextFiles,
        systemPrompt: parsed.systemPrompt,
        appendSystemPrompt: parsed.appendSystemPrompt,
        extensionFactories
      }
    });
    const { settingsManager: settingsManager2, modelRuntime: modelRuntime2, resourceLoader: resourceLoader2 } = services2;
    const diagnostics = [
      ...projectTrustDiagnostics,
      ...services2.diagnostics,
      ...collectSettingsDiagnostics(settingsManager2),
      ...resourceLoader2.getExtensions().errors.map(({ path: path5, error }) => ({
        type: "error",
        message: `Failed to load extension "${path5}": ${error}`
      })),
      ...(resourceLoader2.getExtensions().warnings ?? []).map(({ path: path5, warning }) => ({
        type: "warning",
        message: `Extension package "${path5}": ${warning}`
      }))
    ];
    const modelPatterns = parsed.models ?? settingsManager2.getEnabledModels();
    const scopedModels = modelPatterns && modelPatterns.length > 0 ? await resolveModelScope(modelPatterns, modelRuntime2, { signal: AbortSignal.timeout(15e3) }) : [];
    const { options: sessionOptions, cliThinkingFromModel, diagnostics: sessionOptionDiagnostics } = buildSessionOptions(parsed, scopedModels, sessionManager2.buildSessionContext().messages.length > 0, modelRuntime2, settingsManager2);
    diagnostics.push(...sessionOptionDiagnostics);
    if (parsed.apiKey) {
      if (!sessionOptions.model) {
        diagnostics.push({
          type: "error",
          message: "--api-key requires a model to be specified via --model, --provider/--model, or --models"
        });
      } else {
        await modelRuntime2.setRuntimeApiKey(sessionOptions.model.provider, parsed.apiKey);
      }
    }
    const created = await createAgentSessionFromServices({
      services: services2,
      sessionManager: sessionManager2,
      sessionStartEvent,
      model: sessionOptions.model,
      thinkingLevel: sessionOptions.thinkingLevel,
      scopedModels: sessionOptions.scopedModels,
      tools: sessionOptions.tools,
      excludeTools: sessionOptions.excludeTools,
      noTools: sessionOptions.noTools,
      customTools: sessionOptions.customTools
    });
    const cliThinkingOverride = parsed.thinking !== void 0 || cliThinkingFromModel;
    if (created.session.model && cliThinkingOverride) {
      created.session.setThinkingLevel(created.session.thinkingLevel);
    }
    return {
      ...created,
      services: services2,
      diagnostics
    };
  }, "createRuntime");
  time("createRuntime");
  const runtime = await createAgentSessionRuntime(createRuntime, {
    cwd: sessionManager.getCwd(),
    agentDir,
    sessionManager
  });
  time("createAgentSessionRuntime");
  const { services, session, modelFallbackMessage } = runtime;
  const { settingsManager, modelRuntime, resourceLoader } = services;
  setCapabilityOverrides3(settingsManager.getTerminalCapabilityOverrides());
  applyHttpProxySettings(settingsManager.getGlobalSettings().httpProxy);
  configureHttpDispatcher(settingsManager.getHttpIdleTimeoutMs());
  if (parsed.help) {
    reportDiagnostics(startupSettingsDiagnostics);
    const extensionFlags = resourceLoader.getExtensions().extensions.flatMap((extension) => Array.from(extension.flags.values()));
    printHelp(extensionFlags);
    process.exit(0);
  }
  if (parsed.listModels !== void 0) {
    reportDiagnostics(startupSettingsDiagnostics);
    const searchPattern = typeof parsed.listModels === "string" ? parsed.listModels : void 0;
    await listModels(modelRuntime, searchPattern, AbortSignal.timeout(15e3));
    process.exit(0);
  }
  let stdinContent;
  if (appMode !== "rpc") {
    stdinContent = await readPipedStdin();
    if (stdinContent !== void 0 && appMode === "interactive") {
      appMode = "print";
    }
  }
  time("readPipedStdin");
  const { initialMessage, initialImages } = await prepareInitialMessage(parsed, stdinContent);
  time("prepareInitialMessage");
  setThemeJsonValidator(validateThemeJson);
  initTheme(settingsManager.getTheme(), appMode === "interactive");
  time("initTheme");
  if (appMode === "interactive" && deprecationWarnings.length > 0) {
    await showDeprecationWarnings(deprecationWarnings);
  }
  time("resolveModelScope");
  const startupDiagnostics = deduplicateDiagnostics([...startupSettingsDiagnostics, ...runtime.diagnostics]);
  const hasRuntimeErrors = runtime.diagnostics.some((diagnostic) => diagnostic.type === "error");
  if (appMode !== "interactive" || hasRuntimeErrors) {
    reportDiagnostics(startupDiagnostics);
  }
  if (hasRuntimeErrors) {
    if (runtime.diagnostics.some((diagnostic) => diagnostic.message.includes("Failed to load extension"))) {
      console.error(chalk7.yellow(EXTENSION_LOAD_FAILURE_HINT));
    }
    process.exit(1);
  }
  time("createAgentSession");
  if (appMode !== "interactive" && !session.model) {
    console.error(chalk7.red(formatNoModelsAvailableMessage()));
    process.exit(1);
  }
  const startupBenchmark = isTruthyEnvFlag(process.env.PI_STARTUP_BENCHMARK);
  if (startupBenchmark && appMode !== "interactive") {
    console.error(chalk7.red("Error: PI_STARTUP_BENCHMARK only supports interactive mode"));
    process.exit(1);
  }
  if (!offlineMode && appMode === "rpc") {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 15e3);
    void modelRuntime.refresh({ signal: controller.signal }).catch(() => {
    }).finally(() => clearTimeout(timeout));
  }
  if (appMode === "rpc") {
    printTimings();
    await runRpcMode(runtime);
  } else if (appMode === "interactive") {
    const interactiveMode = new InteractiveMode(runtime, {
      migratedProviders,
      startupDiagnostics,
      modelFallbackMessage,
      autoTrustOnReloadCwd,
      initialMessage,
      initialImages,
      initialMessages: parsed.messages,
      verbose: parsed.verbose,
      tuiMode: parsed.tuiMode,
      initialThemeSetting: parsed.useTheme
    });
    if (startupBenchmark) {
      await interactiveMode.init();
      time("interactiveMode.init");
      await new Promise((resolve7) => setTimeout(resolve7, 150));
      interactiveMode.stop();
      stopThemeWatcher();
      printTimings();
      if (process.stdout.writableLength > 0) {
        await new Promise((resolve7) => process.stdout.once("drain", resolve7));
      }
      if (process.stderr.writableLength > 0) {
        await new Promise((resolve7) => process.stderr.once("drain", resolve7));
      }
      return;
    }
    printTimings();
    await interactiveMode.run();
  } else {
    printTimings();
    const exitCode = await runPrintMode(runtime, {
      mode: toPrintOutputMode(appMode),
      messages: parsed.messages,
      initialMessage,
      initialImages
    });
    stopThemeWatcher();
    restoreStdout();
    if (exitCode !== 0) {
      process.exitCode = exitCode;
    }
    return;
  }
}
__name(main, "main");

// pi-dist/pi-coding-agent/modes/interactive/components/show-images-selector.js
import { Container as Container32, SelectList as SelectList5 } from "../../../pi-tui.mjs";
var SHOW_IMAGES_SELECT_LIST_LAYOUT = {
  minPrimaryColumnWidth: 12,
  maxPrimaryColumnWidth: 32
};
var ShowImagesSelectorComponent = class extends Container32 {
  static {
    __name(this, "ShowImagesSelectorComponent");
  }
  selectList;
  constructor(currentValue, onSelect, onCancel) {
    super();
    const items = [
      { value: "yes", label: "Yes", description: "Show images inline in terminal" },
      { value: "no", label: "No", description: "Show text placeholder instead" }
    ];
    this.addChild(new DynamicBorder());
    this.selectList = new SelectList5(items, 5, getSelectListTheme(), SHOW_IMAGES_SELECT_LIST_LAYOUT);
    this.selectList.setSelectedIndex(currentValue ? 0 : 1);
    this.selectList.onSelect = (item) => {
      onSelect(item.value === "yes");
    };
    this.selectList.onCancel = () => {
      onCancel();
    };
    this.addChild(this.selectList);
    this.addChild(new DynamicBorder());
  }
  getSelectList() {
    return this.selectList;
  }
};

// pi-dist/pi-coding-agent/modes/interactive/components/theme-selector.js
import { Container as Container33, SelectList as SelectList6 } from "../../../pi-tui.mjs";
var THEME_SELECT_LIST_LAYOUT = {
  minPrimaryColumnWidth: 12,
  maxPrimaryColumnWidth: 32
};
var ThemeSelectorComponent = class extends Container33 {
  static {
    __name(this, "ThemeSelectorComponent");
  }
  selectList;
  onPreview;
  constructor(currentTheme, onSelect, onCancel, onPreview) {
    super();
    this.onPreview = onPreview;
    const themes = getAvailableThemes();
    const themeItems2 = themes.map((name) => ({
      value: name,
      label: name,
      description: name === currentTheme ? "(current)" : void 0
    }));
    this.addChild(new DynamicBorder());
    this.selectList = new SelectList6(themeItems2, 10, getSelectListTheme(), THEME_SELECT_LIST_LAYOUT);
    const currentIndex = themes.indexOf(currentTheme);
    if (currentIndex !== -1) {
      this.selectList.setSelectedIndex(currentIndex);
    }
    this.selectList.onSelect = (item) => {
      onSelect(item.value);
    };
    this.selectList.onCancel = () => {
      onCancel();
    };
    this.selectList.onSelectionChange = (item) => {
      this.onPreview(item.value);
    };
    this.addChild(this.selectList);
    this.addChild(new DynamicBorder());
  }
  getSelectList() {
    return this.selectList;
  }
};
export {
  AgentSession,
  AgentSessionRuntime,
  ArminComponent,
  AssistantMessageComponent,
  BashExecutionComponent,
  BorderedLoader,
  BranchSummaryMessageComponent,
  CONFIG_DIR_NAME,
  CURRENT_SESSION_VERSION,
  CompactionSummaryMessageComponent,
  CredentialSynchronizationError,
  CustomEditor,
  CustomMessageComponent,
  DEFAULT_COMPACTION_SETTINGS,
  DEFAULT_MAX_BYTES,
  DEFAULT_MAX_LINES,
  DefaultPackageManager,
  DefaultResourceLoader,
  DynamicBorder,
  ExtensionEditorComponent,
  ExtensionInputComponent,
  ExtensionRunner,
  ExtensionSelectorComponent,
  FooterComponent,
  InteractiveMode,
  LoginDialogComponent,
  ModelRegistry,
  ModelRuntime,
  ModelSelectorComponent,
  OAuthSelectorComponent,
  ProjectTrustStore,
  RpcClient,
  SessionManager,
  SessionSelectorComponent,
  SettingsManager,
  SettingsSelectorComponent,
  ShowImagesSelectorComponent,
  SkillInvocationMessageComponent,
  Theme,
  ThemeSelectorComponent,
  ThinkingSelectorComponent,
  ToolExecutionComponent,
  TreeSelectorComponent,
  UserMessageComponent,
  UserMessageSelectorComponent,
  VERSION,
  VIRTUAL_MODEL_STATE_ENTRY,
  buildContextEntries,
  buildSessionContext,
  buildSessionProjection,
  calculateContextTokens,
  collectEntriesForBranchSummary,
  compact,
  convertToLlm,
  convertToPng,
  copyToClipboard,
  createAgentSession,
  createAgentSessionFromServices2 as createAgentSessionFromServices,
  createAgentSessionRuntime2 as createAgentSessionRuntime,
  createAgentSessionServices2 as createAgentSessionServices,
  createBashTool,
  createBashToolDefinition,
  createCodemodeExtension,
  createCodingTools,
  createEditTool,
  createEditToolDefinition,
  createEventBus,
  createExtensionRuntime,
  createFindTool,
  createFindToolDefinition,
  createGrepTool,
  createGrepToolDefinition,
  createLocalBashOperations,
  createLocalPowerShellOperations,
  createLsTool,
  createLsToolDefinition,
  createMcpExtension,
  createPowerShellTool,
  createPowerShellToolDefinition,
  createReadOnlyTools,
  createReadTool,
  createReadToolDefinition,
  createSyntheticSourceInfo,
  createToolSearchExtension,
  createWriteTool,
  createWriteToolDefinition,
  defineTool,
  detectSupportedImageMimeTypeFromFile,
  discoverAndLoadExtensions,
  estimateTokens,
  findCutPoint,
  findTurnStartIndex,
  formatDimensionNote,
  formatSize,
  formatSkillsForPrompt,
  generateBranchSummary,
  generateDiffString,
  generateSummary,
  generateSummaryWithUsage,
  generateUnifiedPatch,
  getAgentDir,
  getDocsPath,
  getExamplesPath,
  getLanguageFromPath,
  getLastAssistantUsage,
  getLatestCompactionEntry,
  getMarkdownTheme,
  getPackageDir,
  getPowerShellConfig,
  getReadmePath,
  getSelectListTheme,
  getSettingsListTheme,
  getShellConfig,
  hasTrustRequiringProjectResources,
  highlightCode,
  initTheme,
  isBashToolResult,
  isEditToolResult,
  isFindToolResult,
  isGrepToolResult,
  isLsToolResult,
  isPowerShellToolResult,
  isReadToolResult,
  isToolCallEventType,
  isWriteToolResult,
  keyHint,
  keyText,
  loadProjectContextFiles,
  loadSkills,
  loadSkillsFromDir,
  main,
  migrateSessionEntries,
  parseArgs,
  parseFrontmatter,
  parseSessionEntries,
  parseSkillBlock,
  prepareBranchEntries,
  rawKeyHint,
  readStoredCredential,
  renderDiff,
  resizeImage,
  resolveCliModel,
  resolveModelScopeWithDiagnostics,
  runPrintMode,
  runRpcMode,
  serializeConversation,
  sessionEntryToContextMessages,
  shouldCompact,
  stripFrontmatter,
  truncateHead,
  truncateLine,
  truncateTail,
  truncateToVisualLines,
  withFileMutationQueue,
  wrapRegisteredTool,
  wrapRegisteredTools
};
