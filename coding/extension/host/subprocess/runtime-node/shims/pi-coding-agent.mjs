import { spawnSync } from "node:child_process";
import { homedir } from "node:os";
import { join } from "node:path";
import { getRuntime } from "../state.mjs";
import { Type } from "./typebox.mjs";

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

const COMPACTION_SUMMARY_PREFIX = `The conversation history before this point was compacted into the following summary:\n\n<summary>\n`;
const COMPACTION_SUMMARY_SUFFIX = `\n</summary>`;
const BRANCH_SUMMARY_PREFIX = `The following is a summary of a branch that this conversation came back from:\n\n<summary>\n`;
const BRANCH_SUMMARY_SUFFIX = `</summary>`;

function bashExecutionToText(message) {
  let text = `Ran \`${message.command}\`\n`;
  text += message.output ? `\`\`\`\n${message.output}\n\`\`\`` : "(no output)";
  if (message.cancelled) text += "\n\n(command cancelled)";
  else if (message.exitCode !== null && message.exitCode !== undefined && message.exitCode !== 0) text += `\n\nCommand exited with code ${message.exitCode}`;
  if (message.truncated && message.fullOutputPath) text += `\n\n[Output truncated. Full output: ${message.fullOutputPath}]`;
  return text;
}

export function convertToLlm(messages) {
  return (messages ?? []).map((message) => {
    switch (message?.role) {
      case "bashExecution":
        if (message.excludeFromContext) return undefined;
        return { role: "user", content: [{ type: "text", text: bashExecutionToText(message) }], timestamp: message.timestamp };
      case "custom":
        return { role: "user", content: typeof message.content === "string" ? [{ type: "text", text: message.content }] : message.content, timestamp: message.timestamp };
      case "branchSummary":
        return { role: "user", content: [{ type: "text", text: BRANCH_SUMMARY_PREFIX + message.summary + BRANCH_SUMMARY_SUFFIX }], timestamp: message.timestamp };
      case "compactionSummary":
        return { role: "user", content: [{ type: "text", text: COMPACTION_SUMMARY_PREFIX + message.summary + COMPACTION_SUMMARY_SUFFIX }], timestamp: message.timestamp };
      case "user":
      case "assistant":
      case "toolResult":
        return message;
      default:
        return undefined;
    }
  }).filter((message) => message !== undefined);
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

export function buildSessionContext(entries = [], leafId = undefined) {
  const messages = [];
  for (const entry of entries) {
    if (!entry || entry.type !== "message" || !entry.message) continue;
    const msg = entry.message;
    switch (msg.role) {
      case "user":
      case "assistant":
        messages.push(msg);
        break;
      case "toolResult":
        messages.push({ role: "user", content: msg.content ?? [], timestamp: entry.timestamp ?? Date.now() });
        break;
    }
  }
  return { messages, leafId };
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
