import { spawnSync } from "node:child_process";
import { homedir } from "node:os";
import { join } from "node:path";
import { getRuntime } from "../state.mjs";
import { EventEmitter } from "node:events";
import { Type } from "./typebox.mjs";
import { Text } from "./pi-tui.mjs";
// Pi's own code for these, copied verbatim from the pinned release
// (automation/gen/vendor-pi-dist.sh).
export { convertToLlm } from "./pi-dist/pi-coding-agent/core/messages.js";
export {
  buildContextEntries,
  buildSessionContext,
  buildSessionProjection,
  CURRENT_SESSION_VERSION,
  getLatestCompactionEntry,
  migrateSessionEntries,
  parseSessionEntries,
  sessionEntryToContextMessages,
} from "./pi-dist/pi-coding-agent/core/session-manager.js";
export { parseFrontmatter, stripFrontmatter } from "./pi-dist/pi-coding-agent/utils/frontmatter.js";

export {
  allToolNames,
  createAllTools,
  createBashTool,
  createCodingTools,
  createEditTool,
  createFindTool,
  createGrepTool,
  createLocalBashOperations,
  createLsTool,
  createReadOnlyTools,
  createReadTool,
  createTool,
  createWriteTool,
  DEFAULT_MAX_BYTES,
  DEFAULT_MAX_LINES,
  formatSize,
  GREP_MAX_LINE_LENGTH,
  truncateHead,
  truncateLine,
  truncateTail,
  withFileMutationQueue,
} from "./builtin-tools.mjs";

export const VERSION = "0.87.1";

export class ExtensionAPI {}
export class ExtensionContext {}
export class ExtensionCommandContext {}
export class ToolResultEvent {}
export class ModelSelectEvent {}
export class TurnEndEvent {}
export class MessageRenderer {}
export class ModelRegistry {}
export class SessionManager {}
export class AgentSession {}
export class AgentSessionEvent {}
export class ResourceLoader {}
export class Theme {}
export class SessionEntry {}
export class SettingsManager {}
// CustomEditor is the subprocess shim's stand-in for upstream's
// CustomEditor base class. Real extensions extend this class to
// customise the editor's appearance (border color, mode label) and
// history. Because pig cannot serialise the per-keystroke handler
// across the socket, this shim does NOT replace the editor: instead
// it captures the decoration state and forwards it to the host as a
// structured payload that the Go editor applies in-place.
//
// pig-specific: no upstream equivalent. The subset captured here
// covers the decoration surface used by prompt-editor extensions: borderColor, lockBorderColor, modeLabelProvider,
// modeLabelColor, addToHistory, requestRenderNow.
export class CustomEditor {
  constructor(_tui, _theme, _keybindings) {
    this._borderColor = undefined;
    this._lockedBorder = false;
    this._modeLabelProvider = undefined;
    this._modeLabelColor = undefined;
    this._history = [];
  }
  get borderColor() { return this._borderColor || ((text) => text); }
  set borderColor(fn) {
    if (this._lockedBorder) return;
    this._borderColor = typeof fn === "function" ? fn : undefined;
  }
  lockBorderColor() { this._lockedBorder = true; }
  get modeLabelProvider() { return this._modeLabelProvider; }
  set modeLabelProvider(fn) { this._modeLabelProvider = typeof fn === "function" ? fn : undefined; }
  get modeLabelColor() { return this._modeLabelColor; }
  set modeLabelColor(fn) { this._modeLabelColor = typeof fn === "function" ? fn : undefined; }
  addToHistory(text) {
    if (!text) return;
    this._history.push(String(text));
  }
  requestRenderNow() {
    // The runtime hooks this method when applyEditorComponent invokes
    // the factory so that dynamic mode-label changes (e.g. switching
    // prompt modes) flow back to the host.
    if (typeof this._onRequestRender === "function") this._onRequestRender();
  }
}
export class ModelSelectorComponent {}

export function defineTool(tool) {
  return tool;
}

export class DynamicBorder {
  constructor(colorize = (s) => s) { this.colorize = colorize; }
  render(width = 80) { return [this.colorize("─".repeat(Math.max(1, width)))]; }
  invalidate() {}
}

export class BorderedLoader {
  constructor(_tui, _theme, message = "") { this.message = message; }
  setMessage(message) { this.message = message; }
  invalidate() {}
  dispose() {}
  render(width = 80) { return [this.message.slice(0, width)]; }
}

export function getMarkdownTheme() {
  return {};
}

// Upstream reads CONFIG_DIR_NAME/getAgentDir from its own package config and
// homedir. pig keeps a separate config root, so an extension that resolves
// paths through these helpers must land in pig's tree, not ~/.pi.
export const CONFIG_DIR_NAME = ".pig";

export function getAgentDir() {
  const configRoot = process.env.PIG_HOME || join(homedir(), CONFIG_DIR_NAME);
  return join(configRoot, "agent");
}


function contentText(content, separator = "\n") {
  if (typeof content === "string") return content;
  return (content ?? []).filter((block) => block.type === "text").map((block) => block.text).join(separator);
}

export function serializeConversation(messages) {
  const parts = [];
  for (const message of messages ?? []) {
    if (message.role === "user") {
      const content = contentText(message.content, "");
      if (content) parts.push(`[User]: ${content}`);
      continue;
    }
    if (message.role === "assistant") {
      const thinking = [];
      const calls = [];
      for (const block of message.content ?? []) {
        if (block.type === "thinking") thinking.push(block.thinking);
        else if (block.type === "toolCall") calls.push(`${block.name}(${Object.entries(block.arguments ?? {}).map(([key, value]) => `${key}=${JSON.stringify(value)}`).join(", ")})`);
      }
      if (thinking.length > 0) parts.push(`[Assistant thinking]: ${thinking.join("\n")}`);
      if ((message.content ?? []).some((block) => block.type === "text")) parts.push(`[Assistant]: ${contentText(message.content)}`);
      if (calls.length > 0) parts.push(`[Assistant tool calls]: ${calls.join("; ")}`);
      continue;
    }
    if (message.role === "toolResult") {
      const content = contentText(message.content, "");
      if (content) parts.push(`[Tool result]: ${content.length <= 2000 ? content : `${content.slice(0, 2000)}\n\n[... ${content.length - 2000} more characters truncated]`}`);
    }
  }
  return parts.join("\n\n");
}

// Ported from packages/coding-agent/src/core/compaction/compaction.ts. The
// chars/4 heuristic and the image constant must match upstream, because
// extensions compare these estimates against context-window sizes.
const ESTIMATED_IMAGE_CHARS = 4800;

function estimateTextAndImageContentChars(content) {
  if (typeof content === "string") return content.length;
  let chars = 0;
  for (const block of content ?? []) {
    if (block?.type === "text" && block.text) chars += block.text.length;
    else if (block?.type === "image") chars += ESTIMATED_IMAGE_CHARS;
  }
  return chars;
}

export function estimateTokens(message) {
  if (!message) return 0;
  let chars = 0;
  switch (message.role) {
    case "user":
      return Math.ceil(estimateTextAndImageContentChars(message.content) / 4);
    case "assistant":
      for (const block of message.content ?? []) {
        if (block?.type === "text") chars += block.text.length;
        else if (block?.type === "thinking") chars += block.thinking.length;
        else if (block?.type === "toolCall") chars += block.name.length + JSON.stringify(block.arguments).length;
      }
      return Math.ceil(chars / 4);
    case "custom":
    case "toolResult":
      return Math.ceil(estimateTextAndImageContentChars(message.content) / 4);
    case "bashExecution":
      return Math.ceil((message.command.length + message.output.length) / 4);
    case "branchSummary":
    case "compactionSummary":
      return Math.ceil(message.summary.length / 4);
    default:
      return 0;
  }
}

// Upstream colorizes through the active interactive theme. A subprocess
// extension has no theme handle, so the shim keeps the same callable shape and
// renders uncolored text.
export function getSettingsListTheme() {
  return {
    label: (text) => text,
    value: (text) => text,
    description: (text) => text,
    cursor: "→ ",
    hint: (text) => text,
  };
}

export function copyToClipboard(text) {
  try {
    const proc = spawnSync("pbcopy", [], { input: String(text ?? "") });
    return proc.status === 0;
  } catch {
    return false;
  }
}

export function keyHint(_action, fallback = "") {
  return fallback || "";
}

export async function compact(ctx, options = {}) {
  return ctx.compact(options);
}


export async function createAgentSession() {
  throw new Error("createAgentSession is not yet supported in the pig TS subprocess shim");
}

export function createExtensionRuntime() {
  throw new Error("createExtensionRuntime is not yet supported in the pig TS subprocess shim");
}

export function __runtime() {
  return getRuntime();
}


// ---------------------------------------------------------------------------
// Pure helpers ported from Pi 0.87.1 (packages/coding-agent/src). They behave
// exactly as upstream inside the extension process.
// ---------------------------------------------------------------------------

// core/extensions/types.ts
export function isToolCallEventType(toolName, event) {
  return event.toolName === toolName;
}
export function isBashToolResult(e) { return e.toolName === "bash"; }
export function isPowerShellToolResult(e) { return e.toolName === "powershell"; }
export function isReadToolResult(e) { return e.toolName === "read"; }
export function isEditToolResult(e) { return e.toolName === "edit"; }
export function isWriteToolResult(e) { return e.toolName === "write"; }
export function isGrepToolResult(e) { return e.toolName === "grep"; }
export function isFindToolResult(e) { return e.toolName === "find"; }
export function isLsToolResult(e) { return e.toolName === "ls"; }


// core/compaction/compaction.ts
export const DEFAULT_COMPACTION_SETTINGS = {
  enabled: true,
  reserveTokens: 16384,
  keepRecentTokens: 20000,
};

export function calculateContextTokens(usage) {
  return usage.totalTokens || usage.input + usage.output + usage.cacheRead + usage.cacheWrite;
}

function getAssistantUsage(msg) {
  if (msg.role === "assistant" && "usage" in msg) {
    if (msg.stopReason !== "aborted" && msg.stopReason !== "error" && msg.usage && calculateContextTokens(msg.usage) > 0) {
      return msg.usage;
    }
  }
  return undefined;
}

export function getLastAssistantUsage(entries) {
  for (let i = entries.length - 1; i >= 0; i--) {
    const entry = entries[i];
    if (entry.type === "message") {
      const usage = getAssistantUsage(entry.message);
      if (usage) return usage;
    }
  }
  return undefined;
}

export function shouldCompact(contextTokens, contextWindow, settings) {
  if (!settings.enabled) return false;
  return contextTokens > contextWindow - settings.reserveTokens;
}

// core/event-bus.ts
export function createEventBus() {
  const emitter = new EventEmitter();
  return {
    emit: (channel, data) => {
      emitter.emit(channel, data);
    },
    on: (channel, handler) => {
      const safeHandler = async (data) => {
        try {
          await handler(data);
        } catch (err) {
          console.error(`Event handler error (${channel}):`, err);
        }
      };
      emitter.on(channel, safeHandler);
      return () => emitter.off(channel, safeHandler);
    },
    clear: () => {
      emitter.removeAllListeners();
    },
  };
}

// core/source-info.ts
export function createSyntheticSourceInfo(path, options) {
  return {
    path,
    source: options.source,
    scope: options.scope ?? "temporary",
    origin: options.origin ?? "top-level",
    baseDir: options.baseDir,
  };
}

// core/skills.ts
function escapeXml(str) {
  return str.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&apos;");
}

export function formatSkillsForPrompt(skills, fileReadTool = "read") {
  const visibleSkills = skills.filter((s) => !s.disableModelInvocation);
  if (visibleSkills.length === 0) return "";
  const lines = [
    "\n\nThe following skills provide specialized instructions for specific tasks.",
    fileReadTool === "read"
      ? "Use the read tool to load a skill's file when the task matches its description."
      : "Use bash to load a skill's file when the task matches its description.",
    "When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.",
    "",
    "<available_skills>",
  ];
  for (const skill of visibleSkills) {
    lines.push("  <skill>");
    lines.push(`    <name>${escapeXml(skill.name)}</name>`);
    lines.push(`    <description>${escapeXml(skill.description)}</description>`);
    lines.push(`    <location>${escapeXml(skill.filePath)}</location>`);
    lines.push("  </skill>");
  }
  lines.push("</available_skills>");
  return lines.join("\n");
}

// core/agent-session.ts
export function parseSkillBlock(text) {
  const match = text.match(/^<skill name="([^"]+)" location="([^"]+)">\n([\s\S]*?)\n<\/skill>(?:\n\n([\s\S]+))?$/);
  if (!match) return null;
  return { name: match[1], location: match[2], content: match[3], userMessage: match[4]?.trim() || undefined };
}


// modes/interactive/theme/theme.ts
const EXT_TO_LANG = {
  ts: "typescript", tsx: "typescript", js: "javascript", jsx: "javascript", mjs: "javascript", cjs: "javascript",
  py: "python", rb: "ruby", rs: "rust", go: "go", java: "java", kt: "kotlin", swift: "swift", c: "c", h: "c",
  cpp: "cpp", cc: "cpp", cxx: "cpp", hpp: "cpp", cs: "csharp", php: "php", sh: "bash", bash: "bash", zsh: "bash",
  fish: "fish", ps1: "powershell", sql: "sql", html: "html", htm: "html", css: "css", scss: "scss", sass: "sass",
  less: "less", json: "json", yaml: "yaml", yml: "yaml", toml: "toml", xml: "xml", md: "markdown", markdown: "markdown",
  dockerfile: "dockerfile", makefile: "makefile", cmake: "cmake", lua: "lua", perl: "perl", r: "r", scala: "scala",
  clj: "clojure", ex: "elixir", exs: "elixir", erl: "erlang", hs: "haskell", ml: "ocaml", vim: "vim",
  graphql: "graphql", proto: "protobuf", tf: "hcl", hcl: "hcl",
};

export function getLanguageFromPath(filePath) {
  const ext = filePath.split(".").pop()?.toLowerCase();
  if (!ext) return undefined;
  return EXT_TO_LANG[ext];
}

// Like getSettingsListTheme above: the host owns the theme, so the extension
// process keeps upstream's callable shape and renders uncolored text.
export function getSelectListTheme() {
  return {
    selectedPrefix: (text) => text,
    selectedText: (text) => text,
    description: (text) => text,
    scrollInfo: (text) => text,
    noMatch: (text) => text,
  };
}

export function highlightCode(code) {
  return String(code ?? "").split("\n");
}

export function initTheme() {}

// modes/interactive/components/keybinding-hints.ts (uncolored; the host owns
// keybindings and theme).
export function keyText(keybinding) {
  return String(keybinding ?? "");
}

export function rawKeyHint(key, description) {
  return `${key} ${description}`;
}

// modes/interactive/components/visual-truncate.ts
export function truncateToVisualLines(text, maxVisualLines, width, paddingX = 0) {
  if (!text) return { visualLines: [], skippedCount: 0 };
  const allVisualLines = new Text(text, paddingX, 0).render(width);
  if (allVisualLines.length <= maxVisualLines) return { visualLines: allVisualLines, skippedCount: 0 };
  return { visualLines: allVisualLines.slice(-maxVisualLines), skippedCount: allVisualLines.length - maxVisualLines };
}

// ---------------------------------------------------------------------------
// pig divergence (D73): Pi exports these from its own process: the
// interactive UI, session and runtime construction, package and resource
// loading, CLI entry points. PiG runs those natively in Go, so inside an
// extension they are importable (an ESM import of a missing name would fail
// the whole extension at link time) and throw a descriptive error only when
// called or constructed.
// ---------------------------------------------------------------------------
function hostOnly(name) {
  return new Error(`${name} is not available to extensions running in PiG: it belongs to Pi's own process, which PiG implements natively (see DIVERGENCES.md D73)`);
}
function hostOnlyFunction(name) {
  const fn = function () { throw hostOnly(name); };
  Object.defineProperty(fn, "name", { value: name });
  return fn;
}
function hostOnlyClass(name) {
  return { [name]: class { constructor() { throw hostOnly(name); } } }[name];
}

// core/model-runtime.ts
export class CredentialSynchronizationError extends Error {
  constructor(providerId, operation, credential, options) {
    super(`Credential ${operation} committed for ${providerId}, but local synchronization failed`, options);
    this.name = "CredentialSynchronizationError";
    this.providerId = providerId;
    this.operation = operation;
    this.credential = credential;
  }
}

export const AgentSessionRuntime = hostOnlyClass("AgentSessionRuntime");
export const ArminComponent = hostOnlyClass("ArminComponent");
export const AssistantMessageComponent = hostOnlyClass("AssistantMessageComponent");
export const BashExecutionComponent = hostOnlyClass("BashExecutionComponent");
export const BranchSummaryMessageComponent = hostOnlyClass("BranchSummaryMessageComponent");
export const CompactionSummaryMessageComponent = hostOnlyClass("CompactionSummaryMessageComponent");
export const CustomMessageComponent = hostOnlyClass("CustomMessageComponent");
export const DefaultPackageManager = hostOnlyClass("DefaultPackageManager");
export const DefaultResourceLoader = hostOnlyClass("DefaultResourceLoader");
export const ExtensionEditorComponent = hostOnlyClass("ExtensionEditorComponent");
export const ExtensionInputComponent = hostOnlyClass("ExtensionInputComponent");
export const ExtensionRunner = hostOnlyClass("ExtensionRunner");
export const ExtensionSelectorComponent = hostOnlyClass("ExtensionSelectorComponent");
export const FooterComponent = hostOnlyClass("FooterComponent");
export const InteractiveMode = hostOnlyClass("InteractiveMode");
export const LoginDialogComponent = hostOnlyClass("LoginDialogComponent");
export const ModelRuntime = hostOnlyClass("ModelRuntime");
export const OAuthSelectorComponent = hostOnlyClass("OAuthSelectorComponent");
export const ProjectTrustStore = hostOnlyClass("ProjectTrustStore");
export const RpcClient = hostOnlyClass("RpcClient");
export const SessionSelectorComponent = hostOnlyClass("SessionSelectorComponent");
export const SettingsSelectorComponent = hostOnlyClass("SettingsSelectorComponent");
export const ShowImagesSelectorComponent = hostOnlyClass("ShowImagesSelectorComponent");
export const SkillInvocationMessageComponent = hostOnlyClass("SkillInvocationMessageComponent");
export const ThemeSelectorComponent = hostOnlyClass("ThemeSelectorComponent");
export const ThinkingSelectorComponent = hostOnlyClass("ThinkingSelectorComponent");
export const ToolExecutionComponent = hostOnlyClass("ToolExecutionComponent");
export const TreeSelectorComponent = hostOnlyClass("TreeSelectorComponent");
export const UserMessageComponent = hostOnlyClass("UserMessageComponent");
export const UserMessageSelectorComponent = hostOnlyClass("UserMessageSelectorComponent");
export const collectEntriesForBranchSummary = hostOnlyFunction("collectEntriesForBranchSummary");
export const convertToPng = hostOnlyFunction("convertToPng");
export const createAgentSessionFromServices = hostOnlyFunction("createAgentSessionFromServices");
export const createAgentSessionRuntime = hostOnlyFunction("createAgentSessionRuntime");
export const createAgentSessionServices = hostOnlyFunction("createAgentSessionServices");
export const createBashToolDefinition = hostOnlyFunction("createBashToolDefinition");
export const createEditToolDefinition = hostOnlyFunction("createEditToolDefinition");
export const createFindToolDefinition = hostOnlyFunction("createFindToolDefinition");
export const createGrepToolDefinition = hostOnlyFunction("createGrepToolDefinition");
export const createLocalPowerShellOperations = hostOnlyFunction("createLocalPowerShellOperations");
export const createLsToolDefinition = hostOnlyFunction("createLsToolDefinition");
export const createPowerShellTool = hostOnlyFunction("createPowerShellTool");
export const createPowerShellToolDefinition = hostOnlyFunction("createPowerShellToolDefinition");
export const createReadToolDefinition = hostOnlyFunction("createReadToolDefinition");
export const createWriteToolDefinition = hostOnlyFunction("createWriteToolDefinition");
export const detectSupportedImageMimeTypeFromFile = hostOnlyFunction("detectSupportedImageMimeTypeFromFile");
export const discoverAndLoadExtensions = hostOnlyFunction("discoverAndLoadExtensions");
export const findCutPoint = hostOnlyFunction("findCutPoint");
export const findTurnStartIndex = hostOnlyFunction("findTurnStartIndex");
export const formatDimensionNote = hostOnlyFunction("formatDimensionNote");
export const generateBranchSummary = hostOnlyFunction("generateBranchSummary");
export const generateDiffString = hostOnlyFunction("generateDiffString");
export const generateSummary = hostOnlyFunction("generateSummary");
export const generateSummaryWithUsage = hostOnlyFunction("generateSummaryWithUsage");
export const generateUnifiedPatch = hostOnlyFunction("generateUnifiedPatch");
export const getDocsPath = hostOnlyFunction("getDocsPath");
export const getExamplesPath = hostOnlyFunction("getExamplesPath");
export const getPackageDir = hostOnlyFunction("getPackageDir");
export const getPowerShellConfig = hostOnlyFunction("getPowerShellConfig");
export const getReadmePath = hostOnlyFunction("getReadmePath");
export const getShellConfig = hostOnlyFunction("getShellConfig");
export const hasTrustRequiringProjectResources = hostOnlyFunction("hasTrustRequiringProjectResources");
export const loadProjectContextFiles = hostOnlyFunction("loadProjectContextFiles");
export const loadSkills = hostOnlyFunction("loadSkills");
export const loadSkillsFromDir = hostOnlyFunction("loadSkillsFromDir");
export const main = hostOnlyFunction("main");
export const parseArgs = hostOnlyFunction("parseArgs");
export const prepareBranchEntries = hostOnlyFunction("prepareBranchEntries");
export const readStoredCredential = hostOnlyFunction("readStoredCredential");
export const renderDiff = hostOnlyFunction("renderDiff");
export const resizeImage = hostOnlyFunction("resizeImage");
export const resolveCliModel = hostOnlyFunction("resolveCliModel");
export const resolveModelScopeWithDiagnostics = hostOnlyFunction("resolveModelScopeWithDiagnostics");
export const runPrintMode = hostOnlyFunction("runPrintMode");
export const runRpcMode = hostOnlyFunction("runRpcMode");
export const wrapRegisteredTool = hostOnlyFunction("wrapRegisteredTool");
export const wrapRegisteredTools = hostOnlyFunction("wrapRegisteredTools");
