import {
  APP_NAME,
  CONFIG_DIR_NAME,
  DEFAULT_MAX_BYTES,
  ENV_AGENT_DIR,
  ENV_SESSION_DIR,
  FS_WATCH_RETRY_DELAY_MS,
  VERSION,
  canonicalizePath,
  closeWatcher,
  createAllToolDefinitions,
  createLocalBashOperations,
  createToolDefinitionFromAgentTool,
  fetchWithRetry,
  getAgentDir,
  getDocsPath,
  getExamplesPath,
  getExportTemplateDir,
  getFileRevision,
  getReadmePath,
  getResolvedThemeColors,
  getSessionsDir,
  getShellConfig,
  getThemeByName,
  getThemeExportColors,
  isBunBinary,
  isBundledNode,
  isLocalPath,
  loadThemeFromPath,
  markPathIgnoredByCloudSync,
  normalizePath,
  processImage,
  resolvePath,
  sanitizeBinaryOutput,
  spawnProcess,
  spawnProcessSync,
  stripAnsi,
  stripBom,
  theme,
  truncateTail,
  waitForChildProcess,
  watchWithErrorHandler,
  wrapToolDefinition
} from "./chunk-42BDWAQD.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-coding-agent/cli/args.js
import chalk from "../../../chalk/source/index.js";
var VALID_THINKING_LEVELS = ["off", "minimal", "low", "medium", "high", "xhigh", "max"];
function isValidThinkingLevel(level) {
  return VALID_THINKING_LEVELS.includes(level);
}
__name(isValidThinkingLevel, "isValidThinkingLevel");
function normalizeSessionName(value) {
  const name = value.trim();
  return name.length > 0 ? name : void 0;
}
__name(normalizeSessionName, "normalizeSessionName");
function parseArgs(args) {
  const result = {
    messages: [],
    fileArgs: [],
    unknownFlags: /* @__PURE__ */ new Map(),
    diagnostics: []
  };
  for (let i = 0; i < args.length; i++) {
    const arg = args[i];
    if (arg === "--") {
      for (const positionalArg of args.slice(i + 1)) {
        if (positionalArg.startsWith("@")) {
          result.fileArgs.push(positionalArg.slice(1));
        } else {
          result.messages.push(positionalArg);
        }
      }
      break;
    } else if (arg === "--help" || arg === "-h") {
      result.help = true;
    } else if (arg === "--version" || arg === "-v") {
      result.version = true;
    } else if (arg === "--mode") {
      const mode = args[i + 1];
      if (mode === void 0 || mode.startsWith("-")) {
        result.diagnostics.push({ type: "error", message: "--mode requires text, json, or rpc" });
        continue;
      }
      i++;
      if (mode !== "text" && mode !== "json" && mode !== "rpc") {
        result.diagnostics.push({
          type: "error",
          message: `Invalid mode "${mode}". Valid values: text, json, rpc`
        });
        continue;
      }
      result.mode = mode;
    } else if (arg === "--continue" || arg === "-c") {
      result.continue = true;
    } else if (arg === "--resume" || arg === "-r") {
      result.resume = true;
    } else if (arg === "--provider" && i + 1 < args.length) {
      result.provider = args[++i];
    } else if (arg === "--model" && i + 1 < args.length) {
      result.model = args[++i];
    } else if (arg === "--api-key" && i + 1 < args.length) {
      result.apiKey = args[++i];
    } else if (arg === "--system-prompt" && i + 1 < args.length) {
      result.systemPrompt = args[++i];
    } else if (arg === "--append-system-prompt" && i + 1 < args.length) {
      result.appendSystemPrompt = result.appendSystemPrompt ?? [];
      result.appendSystemPrompt.push(args[++i]);
    } else if (arg === "--name" || arg === "-n") {
      if (i + 1 < args.length) {
        result.name = args[++i];
      } else {
        result.diagnostics.push({ type: "error", message: "--name requires a value" });
      }
    } else if (arg === "--no-session") {
      result.noSession = true;
    } else if (arg === "--session" && i + 1 < args.length) {
      result.session = args[++i];
    } else if (arg === "--session-id" && i + 1 < args.length) {
      result.sessionId = args[++i];
    } else if (arg === "--fork" && i + 1 < args.length) {
      result.fork = args[++i];
    } else if (arg === "--session-dir" && i + 1 < args.length) {
      result.sessionDir = args[++i];
    } else if (arg === "--models" && i + 1 < args.length) {
      result.models = args[++i].split(",").map((s) => s.trim());
    } else if (arg === "--no-tools" || arg === "-nt") {
      result.noTools = true;
    } else if (arg === "--no-builtin-tools" || arg === "-nbt") {
      result.noBuiltinTools = true;
    } else if ((arg === "--tools" || arg === "-t") && i + 1 < args.length) {
      result.tools = args[++i].split(",").map((s) => s.trim()).filter((name) => name.length > 0);
    } else if ((arg === "--exclude-tools" || arg === "-xt") && i + 1 < args.length) {
      result.excludeTools = args[++i].split(",").map((s) => s.trim()).filter((name) => name.length > 0);
    } else if (arg === "--thinking" && i + 1 < args.length) {
      const level = args[++i];
      if (isValidThinkingLevel(level)) {
        result.thinking = level;
      } else {
        result.diagnostics.push({
          type: "warning",
          message: `Invalid thinking level "${level}". Valid values: ${VALID_THINKING_LEVELS.join(", ")}`
        });
      }
    } else if (arg === "--print" || arg === "-p") {
      result.print = true;
      const next = args[i + 1];
      if (next !== void 0 && !next.startsWith("@") && (!next.startsWith("-") || next.startsWith("---"))) {
        result.messages.push(next);
        i++;
      }
    } else if (arg === "--export" && i + 1 < args.length) {
      result.export = args[++i];
    } else if ((arg === "--extension" || arg === "-e") && i + 1 < args.length) {
      result.extensions = result.extensions ?? [];
      result.extensions.push(args[++i]);
    } else if (arg === "--no-extensions" || arg === "-ne") {
      result.noExtensions = true;
    } else if (arg === "--skill" && i + 1 < args.length) {
      result.skills = result.skills ?? [];
      result.skills.push(args[++i]);
    } else if (arg === "--prompt-template" && i + 1 < args.length) {
      result.promptTemplates = result.promptTemplates ?? [];
      result.promptTemplates.push(args[++i]);
    } else if (arg === "--theme" && i + 1 < args.length) {
      result.themes = result.themes ?? [];
      result.themes.push(args[++i]);
    } else if (arg === "--use-theme") {
      const themeName = args[i + 1];
      if (themeName === void 0 || themeName.startsWith("-")) {
        result.diagnostics.push({ type: "error", message: "--use-theme requires a theme name" });
      } else {
        result.useTheme = themeName;
        i++;
      }
    } else if (arg === "--no-skills" || arg === "-ns") {
      result.noSkills = true;
    } else if (arg === "--no-prompt-templates" || arg === "-np") {
      result.noPromptTemplates = true;
    } else if (arg === "--no-themes") {
      result.noThemes = true;
    } else if (arg === "--no-context-files" || arg === "-nc") {
      result.noContextFiles = true;
    } else if (arg === "--list-models") {
      if (i + 1 < args.length && !args[i + 1].startsWith("-") && !args[i + 1].startsWith("@")) {
        result.listModels = args[++i];
      } else {
        result.listModels = true;
      }
    } else if (arg === "--tui-mode") {
      const mode = args[i + 1];
      if (mode === "regular" || mode === "fullscreen") {
        result.tuiMode = mode;
        i++;
      } else if (mode === void 0 || mode.startsWith("-")) {
        result.diagnostics.push({ type: "error", message: "--tui-mode requires regular or fullscreen" });
      } else {
        i++;
        result.diagnostics.push({
          type: "error",
          message: `Invalid TUI mode "${mode}". Valid values: regular, fullscreen`
        });
      }
    } else if (arg === "--verbose") {
      result.verbose = true;
    } else if (arg === "--approve" || arg === "-a") {
      result.projectTrustOverride = true;
    } else if (arg === "--no-approve" || arg === "-na") {
      result.projectTrustOverride = false;
    } else if (arg === "--offline") {
      result.offline = true;
    } else if (arg.startsWith("@")) {
      result.fileArgs.push(arg.slice(1));
    } else if (arg.startsWith("--")) {
      const eqIndex = arg.indexOf("=");
      if (eqIndex !== -1) {
        result.unknownFlags.set(arg.slice(2, eqIndex), arg.slice(eqIndex + 1));
      } else {
        const flagName = arg.slice(2);
        const next = args[i + 1];
        if (next !== void 0 && !next.startsWith("-") && !next.startsWith("@")) {
          result.unknownFlags.set(flagName, next);
          i++;
        } else {
          result.unknownFlags.set(flagName, true);
        }
      }
    } else if (arg.startsWith("-") && !arg.startsWith("--")) {
      result.diagnostics.push({ type: "error", message: `Unknown option: ${arg}` });
    } else if (!arg.startsWith("-")) {
      result.messages.push(arg);
    }
  }
  return result;
}
__name(parseArgs, "parseArgs");
function printHelp(extensionFlags) {
  const extensionFlagsText = extensionFlags && extensionFlags.length > 0 ? `
${chalk.bold("Extension CLI Flags:")}
${extensionFlags.map((flag) => {
    const value = flag.type === "string" ? " <value>" : "";
    const description = flag.description ?? `Registered by ${flag.extensionPath}`;
    return `  --${flag.name}${value}`.padEnd(30) + description;
  }).join("\n")}
` : "";
  console.log(`${chalk.bold(APP_NAME)} - AI coding assistant with read, bash, edit, write tools

${chalk.bold("Usage:")}
  ${APP_NAME} [options] [--] [@files...] [messages...]

${chalk.bold("Commands:")}
  ${APP_NAME} install <source> [-l]     Install extension source and add to settings
  ${APP_NAME} remove <source> [-l]      Remove extension source from settings
  ${APP_NAME} uninstall <source> [-l]   Alias for remove
  ${APP_NAME} update [source|self|pi]   Update pi, extensions, or model catalogs
  ${APP_NAME} list                      List installed extensions from settings
  ${APP_NAME} config [-l]               Open TUI to enable/disable package resources (Tab switches scope)
  ${APP_NAME} auth <command>            Print credentials or check provider readiness
  ${APP_NAME} <command> --help          Show help for install/remove/uninstall/update/list/config/auth

${chalk.bold("Options:")}
  --provider <name>              Provider name (default: google)
  --model <pattern>              Model pattern or ID (supports "provider/id" and optional ":<thinking>")
  --api-key <key>                API key (defaults to env vars)
  --system-prompt <text>         System prompt (default: coding assistant prompt)
  --append-system-prompt <text>  Append text or file contents to the system prompt (can be used multiple times)
  --mode <mode>                  Output mode: text (default), json, or rpc
  --print, -p                    Non-interactive mode: process prompt and exit
  --continue, -c                 Continue previous session
  --resume, -r                   Select a session to resume
  --session <path|id>            Use specific session file or partial UUID
  --session-id <id>              Use exact project session ID, creating it if missing
  --fork <path|id>               Fork specific session file or partial UUID into a new session
  --session-dir <dir>            Directory for session storage and lookup
  --no-session                   Don't save session (ephemeral)
  --name, -n <name>              Set session display name
  --models <patterns>            Comma-separated model patterns for Ctrl+P cycling
                                 Supports globs (anthropic/*, *sonnet*) and fuzzy matching
  --no-tools, -nt                Disable all tools by default (built-in and extension)
  --no-builtin-tools, -nbt       Disable built-in tools by default but keep extension/custom tools enabled
  --tools, -t <tools>            Comma-separated allowlist of tool names to enable
                                 Applies to built-in, extension, and custom tools
  --exclude-tools, -xt <tools>   Comma-separated denylist of tool names to disable
                                 Applies to built-in, extension, and custom tools
  --thinking <level>             Set thinking level: off, minimal, low, medium, high, xhigh, max
  --extension, -e <path>         Load an extension file (can be used multiple times)
  --no-extensions, -ne           Disable extension discovery (explicit -e paths still work)
  --skill <path>                 Load a skill file or directory (can be used multiple times)
  --no-skills, -ns               Disable skills discovery and loading
  --prompt-template <path>       Load a prompt template file or directory (can be used multiple times)
  --no-prompt-templates, -np     Disable prompt template discovery and loading
  --theme <path>                 Load a theme file or directory (can be used multiple times)
  --use-theme <name[/name]>      Set the initial interactive theme for this run
  --no-themes                    Disable theme discovery and loading
  --no-context-files, -nc        Disable AGENTS.md and CLAUDE.md discovery and loading
  --export <file>                Export session file to HTML and exit
  --list-models [search]         List available models (with optional fuzzy search)
  --verbose                      Force verbose startup (overrides quietStartup setting)
  --tui-mode <mode>              TUI mode: regular (default) or fullscreen
  --approve, -a                  Trust project-local files for this run
  --no-approve, -na              Ignore project-local files for this run
  --offline                      Disable startup network operations (same as PI_OFFLINE=1)
  --                             End option parsing; treat remaining arguments as messages/files
  --help, -h                     Show this help
  --version, -v                  Show version number

Extensions can register additional flags (e.g., --plan from plan-mode extension).${extensionFlagsText}

${chalk.bold("Examples:")}
  # Print a provider API key for an external client
  ${APP_NAME} auth print-api-key --provider openai

  # Print an OAuth bearer token for an external client (refreshes if expired)
  ${APP_NAME} auth print-bearer-token --provider openai-codex

  # Interactive mode
  ${APP_NAME}

  # Interactive mode with initial prompt
  ${APP_NAME} "List all .ts files in src/"

  # Include files in initial message
  ${APP_NAME} @prompt.md @image.png "What color is the sky?"

  # Non-interactive mode (process and exit)
  ${APP_NAME} -p "List all .ts files in src/"

  # Prompt beginning with a dash
  ${APP_NAME} -p -- "- Summarize these points"

  # Multiple messages (interactive)
  ${APP_NAME} "Read package.json" "What dependencies do we have?"

  # Continue previous session
  ${APP_NAME} --continue "What did we discuss?"

  # Start a named session
  ${APP_NAME} --name "Refactor auth module"

  # Use different model
  ${APP_NAME} --provider openai --model gpt-4o-mini "Help me refactor this code"

  # Use model with provider prefix (no --provider needed)
  ${APP_NAME} --model openai/gpt-4o "Help me refactor this code"

  # Use model with thinking level shorthand
  ${APP_NAME} --model sonnet:high "Solve this complex problem"

  # Limit model cycling to specific models
  ${APP_NAME} --models claude-sonnet,claude-haiku,gpt-4o

  # Limit to a specific provider with glob pattern
  ${APP_NAME} --models "github-copilot/*"

  # Cycle models with fixed thinking levels
  ${APP_NAME} --models sonnet:high,haiku:low

  # Start with a specific thinking level
  ${APP_NAME} --thinking high "Solve this complex problem"

  # Read-only mode (no file modifications possible)
  ${APP_NAME} --tools read,grep,find,ls -p "Review the code in src/"

  # Disable one tool while keeping the rest available
  ${APP_NAME} --exclude-tools ask_question

  # Export a session file to HTML
  ${APP_NAME} --export ~/${CONFIG_DIR_NAME}/agent/sessions/--path--/session.jsonl
  ${APP_NAME} --export session.jsonl output.html

${chalk.bold("Environment Variables:")}
  ANTHROPIC_AUTH_TOKEN             - Anthropic bearer auth token
  ANTHROPIC_API_KEY                - Anthropic Claude API key
  ANTHROPIC_OAUTH_TOKEN            - Anthropic OAuth token (alternative to API key)
  ANT_LING_API_KEY                 - Ant Ling API key
  OPENAI_API_KEY                   - OpenAI GPT API key
  AZURE_OPENAI_API_KEY             - Azure OpenAI API key
  AZURE_OPENAI_BASE_URL            - Azure OpenAI/Cognitive Services base URL (e.g. https://{resource}.openai.azure.com)
  AZURE_OPENAI_RESOURCE_NAME       - Azure OpenAI resource name (alternative to base URL)
  AZURE_OPENAI_API_VERSION         - Azure OpenAI API version (default: v1)
  AZURE_OPENAI_DEPLOYMENT_NAME_MAP - Azure OpenAI model=deployment map (comma-separated)
  DEEPSEEK_API_KEY                 - DeepSeek API key
  NVIDIA_API_KEY                   - NVIDIA NIM API key
  GEMINI_API_KEY                   - Google Gemini API key
  GROQ_API_KEY                     - Groq API key
  CEREBRAS_API_KEY                 - Cerebras API key
  XAI_API_KEY                      - xAI Grok API key
  FIREWORKS_API_KEY                - Fireworks API key
  TOGETHER_API_KEY                 - Together AI API key
  BASETEN_API_KEY                  - Baseten API key
  OPENROUTER_API_KEY               - OpenRouter API key
  AI_GATEWAY_API_KEY               - Vercel AI Gateway API key
  ZAI_API_KEY                      - ZAI Coding Plan API key (Global)
  ZAI_CODING_CN_API_KEY            - ZAI Coding Plan API key (China)
  MISTRAL_API_KEY                  - Mistral API key
  MINIMAX_API_KEY                  - MiniMax API key
  MOONSHOT_API_KEY                 - Moonshot AI API key
  OPENCODE_API_KEY                 - OpenCode Zen/OpenCode Go API key
  KIMI_API_KEY                     - Kimi For Coding API key
  META_API_KEY                     - Meta Model API key
  CLOUDFLARE_API_KEY               - Cloudflare API token (Workers AI and AI Gateway)
  CLOUDFLARE_ACCOUNT_ID            - Cloudflare account id (required for both)
  CLOUDFLARE_GATEWAY_ID            - Cloudflare AI Gateway slug (required for AI Gateway)
  QWEN_TOKEN_PLAN_API_KEY          - Qwen Token Plan API key (international region)
  QWEN_TOKEN_PLAN_CN_API_KEY       - Qwen Token Plan API key (China region)
  XIAOMI_API_KEY                   - Xiaomi MiMo API key (api.xiaomimimo.com billing)
  XIAOMI_TOKEN_PLAN_CN_API_KEY     - Xiaomi MiMo Token Plan API key (China region)
  XIAOMI_TOKEN_PLAN_AMS_API_KEY    - Xiaomi MiMo Token Plan API key (Amsterdam region)
  XIAOMI_TOKEN_PLAN_SGP_API_KEY    - Xiaomi MiMo Token Plan API key (Singapore region)
  AWS_PROFILE                      - AWS profile for Amazon Bedrock
  AWS_ACCESS_KEY_ID                - AWS access key for Amazon Bedrock
  AWS_SECRET_ACCESS_KEY            - AWS secret key for Amazon Bedrock
  AWS_BEARER_TOKEN_BEDROCK         - Bedrock API key (bearer token)
  AWS_REGION                       - AWS region for Amazon Bedrock (e.g., us-east-1)
  ${ENV_AGENT_DIR.padEnd(32)} - Config directory (default: ~/${CONFIG_DIR_NAME}/agent)
  ${ENV_SESSION_DIR.padEnd(32)} - Session storage directory (overridden by --session-dir)
  PI_PACKAGE_DIR                   - Override package directory (for Nix/Guix store paths)
  PI_OFFLINE                       - Disable startup network operations when set to 1/true/yes
  PI_TELEMETRY                     - Override install telemetry when set to 1/true/yes or 0/false/no
  PI_SHARE_VIEWER_URL              - Base URL for /share command (default: https://pi.dev/session/)

${chalk.bold("Built-in Tool Names:")}
  read       - Read file contents
  bash       - Execute bash commands
  powershell - Execute PowerShell commands on Windows
  edit       - Edit files with find/replace
  write      - Write files (creates/overwrites)
  grep       - Search file contents (read-only, off by default)
  find       - Find files by glob pattern (read-only, off by default)
  ls         - List directory contents (read-only, off by default)
`);
}
__name(printHelp, "printHelp");

// pi-dist/pi-coding-agent/utils/frontmatter.js
import { parse } from "../../../yaml/index.js";
var normalizeNewlines = /* @__PURE__ */ __name((value) => value.replace(/\r\n/g, "\n").replace(/\r/g, "\n"), "normalizeNewlines");
var extractFrontmatter = /* @__PURE__ */ __name((content) => {
  const normalized = normalizeNewlines(stripBom(content));
  if (!normalized.startsWith("---")) {
    return { yamlString: null, body: normalized };
  }
  const endIndex = normalized.indexOf("\n---", 3);
  if (endIndex === -1) {
    return { yamlString: null, body: normalized };
  }
  return {
    yamlString: normalized.slice(4, endIndex),
    body: normalized.slice(endIndex + 4).trim()
  };
}, "extractFrontmatter");
var parseFrontmatter = /* @__PURE__ */ __name((content) => {
  const { yamlString, body } = extractFrontmatter(content);
  if (!yamlString) {
    return { frontmatter: {}, body };
  }
  const parsed = parse(yamlString);
  return { frontmatter: parsed ?? {}, body };
}, "parseFrontmatter");
var stripFrontmatter = /* @__PURE__ */ __name((content) => parseFrontmatter(content).body, "stripFrontmatter");

// pi-dist/pi-coding-agent/core/auth-guidance.js
import { join } from "node:path";
var UNKNOWN_PROVIDER = "unknown";
function getProviderLoginHelp() {
  return [
    "Use /login to log into a provider via OAuth or API key. See:",
    `  ${join(getDocsPath(), "providers.md")}`,
    `  ${join(getDocsPath(), "models.md")}`
  ].join("\n");
}
__name(getProviderLoginHelp, "getProviderLoginHelp");
function formatNoModelsAvailableMessage() {
  return `No models available. ${getProviderLoginHelp()}`;
}
__name(formatNoModelsAvailableMessage, "formatNoModelsAvailableMessage");
function formatNoModelSelectedMessage() {
  return `No model selected.

${getProviderLoginHelp()}

Then use /model to select a model.`;
}
__name(formatNoModelSelectedMessage, "formatNoModelSelectedMessage");
function formatNoApiKeyFoundMessage(provider) {
  const providerDisplay = provider === UNKNOWN_PROVIDER ? "the selected model" : provider;
  return `No API key found for ${providerDisplay}.

${getProviderLoginHelp()}`;
}
__name(formatNoApiKeyFoundMessage, "formatNoApiKeyFoundMessage");

// pi-dist/pi-coding-agent/core/messages.js
var COMPACTION_SUMMARY_PREFIX = `The conversation history before this point was compacted into the following summary:

<summary>
`;
var COMPACTION_SUMMARY_SUFFIX = `
</summary>`;
var BRANCH_SUMMARY_PREFIX = `The following is a summary of a branch that this conversation came back from:

<summary>
`;
var BRANCH_SUMMARY_SUFFIX = `</summary>`;
function bashExecutionToText(msg) {
  let text = `Ran \`${msg.command}\`
`;
  if (msg.output) {
    text += `\`\`\`
${msg.output}
\`\`\``;
  } else {
    text += "(no output)";
  }
  if (msg.cancelled) {
    text += "\n\n(command cancelled)";
  } else if (msg.exitCode !== null && msg.exitCode !== void 0 && msg.exitCode !== 0) {
    text += `

Command exited with code ${msg.exitCode}`;
  }
  if (msg.truncated && msg.fullOutputPath) {
    text += `

[Output truncated. Full output: ${msg.fullOutputPath}]`;
  }
  return text;
}
__name(bashExecutionToText, "bashExecutionToText");
function createBranchSummaryMessage(summary, fromId, timestamp) {
  return {
    role: "branchSummary",
    summary,
    fromId,
    timestamp: new Date(timestamp).getTime()
  };
}
__name(createBranchSummaryMessage, "createBranchSummaryMessage");
function createCompactionSummaryMessage(summary, tokensBefore, timestamp) {
  return {
    role: "compactionSummary",
    summary,
    tokensBefore,
    timestamp: new Date(timestamp).getTime()
  };
}
__name(createCompactionSummaryMessage, "createCompactionSummaryMessage");
function createCustomMessage(customType, content, display, details, timestamp) {
  return {
    role: "custom",
    customType,
    content,
    display,
    details,
    timestamp: new Date(timestamp).getTime()
  };
}
__name(createCustomMessage, "createCustomMessage");
function convertToLlm(messages) {
  return messages.map((m) => {
    switch (m.role) {
      case "bashExecution":
        if (m.excludeFromContext) {
          return void 0;
        }
        return {
          role: "user",
          content: [{ type: "text", text: bashExecutionToText(m) }],
          timestamp: m.timestamp
        };
      case "custom": {
        const content = typeof m.content === "string" ? [{ type: "text", text: m.content }] : m.content;
        return {
          role: "user",
          content,
          timestamp: m.timestamp
        };
      }
      case "branchSummary":
        return {
          role: "user",
          content: [{ type: "text", text: BRANCH_SUMMARY_PREFIX + m.summary + BRANCH_SUMMARY_SUFFIX }],
          timestamp: m.timestamp
        };
      case "compactionSummary":
        return {
          role: "user",
          content: [
            { type: "text", text: COMPACTION_SUMMARY_PREFIX + m.summary + COMPACTION_SUMMARY_SUFFIX }
          ],
          timestamp: m.timestamp
        };
      case "system":
      case "user":
      case "assistant":
      case "toolResult":
        return m;
      default:
        const _exhaustiveCheck = m;
        return void 0;
    }
  }).filter((m) => m !== void 0);
}
__name(convertToLlm, "convertToLlm");

// pi-dist/pi-coding-agent/core/session-manager.js
import { getCurrentSystemMessage, uuidv7 } from "../../pi-ai/sdk-bundle/index.js";
import { randomUUID } from "crypto";
import { appendFileSync, closeSync, createReadStream, existsSync, mkdirSync, openSync, readdirSync, readSync, statSync, writeFileSync } from "fs";
import { readdir, stat } from "fs/promises";
import { basename, join as join2, resolve } from "path";
import { createInterface } from "readline";
import { StringDecoder } from "string_decoder";
var CURRENT_SESSION_VERSION = 3;
function createSessionId() {
  return uuidv7();
}
__name(createSessionId, "createSessionId");
function assertValidSessionId(id) {
  if (!/^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$/.test(id)) {
    throw new Error("Session id must be non-empty, contain only alphanumeric characters, '-', '_', and '.', and start and end with an alphanumeric character");
  }
}
__name(assertValidSessionId, "assertValidSessionId");
function generateId(byId) {
  for (let i = 0; i < 100; i++) {
    const id = randomUUID().slice(0, 8);
    if (!byId.has(id))
      return id;
  }
  return randomUUID();
}
__name(generateId, "generateId");
function migrateV1ToV2(entries) {
  const ids = /* @__PURE__ */ new Set();
  let prevId = null;
  for (const entry of entries) {
    if (entry.type === "session") {
      entry.version = 2;
      continue;
    }
    entry.id = generateId(ids);
    entry.parentId = prevId;
    prevId = entry.id;
    if (entry.type === "compaction") {
      const comp = entry;
      if (typeof comp.firstKeptEntryIndex === "number") {
        const targetEntry = entries[comp.firstKeptEntryIndex];
        if (targetEntry && targetEntry.type !== "session") {
          comp.firstKeptEntryId = targetEntry.id;
        }
        delete comp.firstKeptEntryIndex;
      }
    }
  }
}
__name(migrateV1ToV2, "migrateV1ToV2");
function migrateV2ToV3(entries) {
  for (const entry of entries) {
    if (entry.type === "session") {
      entry.version = 3;
      continue;
    }
    if (entry.type === "message") {
      const msgEntry = entry;
      if (msgEntry.message && msgEntry.message.role === "hookMessage") {
        msgEntry.message.role = "custom";
      }
    }
  }
}
__name(migrateV2ToV3, "migrateV2ToV3");
function migrateToCurrentVersion(entries) {
  const header = entries.find((e) => e.type === "session");
  const version2 = header?.version ?? 1;
  if (version2 >= CURRENT_SESSION_VERSION)
    return false;
  if (version2 < 2)
    migrateV1ToV2(entries);
  if (version2 < 3)
    migrateV2ToV3(entries);
  return true;
}
__name(migrateToCurrentVersion, "migrateToCurrentVersion");
function migrateSessionEntries(entries) {
  migrateToCurrentVersion(entries);
}
__name(migrateSessionEntries, "migrateSessionEntries");
function parseSessionEntries(content) {
  const entries = [];
  const lines = content.trim().split("\n");
  for (const line of lines) {
    if (!line.trim())
      continue;
    try {
      const entry = JSON.parse(line);
      entries.push(entry);
    } catch {
    }
  }
  return entries;
}
__name(parseSessionEntries, "parseSessionEntries");
function getLatestCompactionEntry(entries) {
  for (let i = entries.length - 1; i >= 0; i--) {
    if (entries[i].type === "compaction") {
      return entries[i];
    }
  }
  return null;
}
__name(getLatestCompactionEntry, "getLatestCompactionEntry");
function buildEntryIndex(entries, byId) {
  if (byId)
    return byId;
  const index = /* @__PURE__ */ new Map();
  for (const entry of entries) {
    index.set(entry.id, entry);
  }
  return index;
}
__name(buildEntryIndex, "buildEntryIndex");
function buildSessionPath(entries, leafId, byId) {
  const index = buildEntryIndex(entries, byId);
  let leaf;
  if (leafId === null) {
    return [];
  }
  if (leafId) {
    leaf = index.get(leafId);
  }
  leaf ??= entries[entries.length - 1];
  if (!leaf) {
    return [];
  }
  const path2 = [];
  let current = leaf;
  while (current) {
    path2.push(current);
    current = current.parentId ? index.get(current.parentId) : void 0;
  }
  path2.reverse();
  return path2;
}
__name(buildSessionPath, "buildSessionPath");
function getSessionContextSettings(path2) {
  let thinkingLevel = "off";
  let model = null;
  for (const entry of path2) {
    if (entry.type === "thinking_level_change") {
      thinkingLevel = entry.thinkingLevel;
    } else if (entry.type === "model_change") {
      model = { provider: entry.provider, modelId: entry.modelId };
    } else if (entry.type === "message" && entry.message.role === "assistant") {
      model = { provider: entry.message.provider, modelId: entry.message.model };
    }
  }
  return { thinkingLevel, model };
}
__name(getSessionContextSettings, "getSessionContextSettings");
function sessionEntryToContextMessages(entry) {
  if (entry.type === "message") {
    const message = entry.message;
    if (message.role === "system" && message.content == null)
      return [{ ...message, content: "" }];
    if ((message.role === "user" || message.role === "assistant" || message.role === "toolResult") && message.content == null) {
      return [{ ...message, content: [] }];
    }
    return [message];
  }
  if (entry.type === "custom_message") {
    return [
      createCustomMessage(entry.customType, entry.content ?? [], entry.display, entry.details, entry.timestamp)
    ];
  }
  if (entry.type === "branch_summary" && entry.summary) {
    return [createBranchSummaryMessage(entry.summary, entry.fromId, entry.timestamp)];
  }
  if (entry.type === "compaction") {
    const summary = createCompactionSummaryMessage(entry.summary, entry.tokensBefore, entry.timestamp);
    return entry.systemMessage ? [entry.systemMessage, summary] : [summary];
  }
  return [];
}
__name(sessionEntryToContextMessages, "sessionEntryToContextMessages");
function buildContextEntries(entries, leafId, byId) {
  const path2 = buildSessionPath(entries, leafId, byId);
  let compaction = null;
  for (const entry of path2) {
    if (entry.type === "compaction") {
      compaction = entry;
    }
  }
  if (!compaction) {
    return path2;
  }
  const compactionIdx = path2.findIndex((entry) => entry.id === compaction.id);
  if (compactionIdx < 0) {
    return path2;
  }
  const contextEntries = [compaction];
  let foundFirstKept = false;
  for (let i = 0; i < compactionIdx; i++) {
    const entry = path2[i];
    if (entry.id === compaction.firstKeptEntryId) {
      foundFirstKept = true;
    }
    if (foundFirstKept && !(entry.type === "message" && entry.message.role === "system")) {
      contextEntries.push(entry);
    }
  }
  contextEntries.push(...path2.slice(compactionIdx + 1));
  return contextEntries;
}
__name(buildContextEntries, "buildContextEntries");
function projectContextEntry(entry, edit) {
  const messages = sessionEntryToContextMessages(entry);
  if (!edit)
    return messages;
  const replacement = edit.replacement;
  if (replacement === null)
    return [];
  return messages.map((message) => {
    if (message.role !== "user" && message.role !== "assistant" && message.role !== "toolResult" && message.role !== "custom") {
      return message;
    }
    const content = (message.role === "assistant" || message.role === "toolResult") && typeof replacement.content === "string" ? [{ type: "text", text: replacement.content }] : replacement.content;
    return { ...message, content };
  });
}
__name(projectContextEntry, "projectContextEntry");
function buildSessionProjection(entries, leafId, byId) {
  const path2 = buildSessionPath(entries, leafId, byId);
  const { thinkingLevel, model } = getSessionContextSettings(path2);
  const contextEntries = buildContextEntries(entries, leafId, byId);
  const edits = /* @__PURE__ */ new Map();
  for (const entry of contextEntries) {
    if (entry.type === "context_edit")
      edits.set(entry.targetId, entry);
  }
  const projectedEntries = contextEntries.map((sourceEntry, index) => ({
    sourceEntry,
    // buildContextEntries() may retain an older compaction entry because its
    // raw ID lies inside the newest retained range. Only the newest compaction
    // at index zero contributes a checkpoint and summary.
    messages: sourceEntry.type === "compaction" && index > 0 ? [] : projectContextEntry(sourceEntry, edits.get(sourceEntry.id))
  }));
  return {
    entries: projectedEntries,
    messages: projectedEntries.flatMap((entry) => entry.messages),
    thinkingLevel,
    model
  };
}
__name(buildSessionProjection, "buildSessionProjection");
function buildSessionContext(entries, leafId, byId) {
  const { messages, thinkingLevel, model } = buildSessionProjection(entries, leafId, byId);
  return { messages, thinkingLevel, model };
}
__name(buildSessionContext, "buildSessionContext");
function getDefaultSessionDirPath(cwd, agentDir = getAgentDir()) {
  const resolvedCwd = resolvePath(cwd);
  const resolvedAgentDir = resolvePath(agentDir);
  const safePath = `--${resolvedCwd.replace(/^[/\\]/, "").replace(/[/\\:]/g, "-")}--`;
  return join2(resolvedAgentDir, "sessions", safePath);
}
__name(getDefaultSessionDirPath, "getDefaultSessionDirPath");
function getDefaultSessionDir(cwd, agentDir = getAgentDir()) {
  const sessionDir = getDefaultSessionDirPath(cwd, agentDir);
  if (!existsSync(sessionDir)) {
    mkdirSync(sessionDir, { recursive: true });
  }
  return sessionDir;
}
__name(getDefaultSessionDir, "getDefaultSessionDir");
var SESSION_READ_BUFFER_SIZE = 1024 * 1024;
var SESSION_HEADER_READ_BUFFER_SIZE = 4096;
var MAX_SESSION_HEADER_SCAN_BYTES = 1024 * 1024;
var SessionHeaderScanLimitError = class extends Error {
  static {
    __name(this, "SessionHeaderScanLimitError");
  }
  constructor(filePath) {
    super(`Session header exceeds ${MAX_SESSION_HEADER_SCAN_BYTES}-byte scan limit: ${filePath}`);
    this.name = "SessionHeaderScanLimitError";
  }
};
function parseSessionEntryLine(line) {
  if (!line.trim())
    return null;
  try {
    return JSON.parse(line);
  } catch {
    return null;
  }
}
__name(parseSessionEntryLine, "parseSessionEntryLine");
function loadEntriesFromFile(filePath) {
  const resolvedFilePath = normalizePath(filePath);
  if (!existsSync(resolvedFilePath))
    return [];
  const entries = [];
  let pending = "";
  const fd = openSync(resolvedFilePath, "r");
  try {
    const decoder = new StringDecoder("utf8");
    const buffer = Buffer.allocUnsafe(SESSION_READ_BUFFER_SIZE);
    while (true) {
      const bytesRead = readSync(fd, buffer, 0, buffer.length, null);
      if (bytesRead === 0)
        break;
      pending += decoder.write(buffer.subarray(0, bytesRead));
      let lineStart = 0;
      let newlineIndex = pending.indexOf("\n", lineStart);
      while (newlineIndex !== -1) {
        const entry = parseSessionEntryLine(pending.slice(lineStart, newlineIndex));
        if (entry)
          entries.push(entry);
        lineStart = newlineIndex + 1;
        newlineIndex = pending.indexOf("\n", lineStart);
      }
      pending = pending.slice(lineStart);
    }
    pending += decoder.end();
    const finalEntry = parseSessionEntryLine(pending);
    if (finalEntry)
      entries.push(finalEntry);
  } finally {
    closeSync(fd);
  }
  if (entries.length === 0)
    return entries;
  const header = entries[0];
  if (header.type !== "session" || typeof header.id !== "string") {
    return [];
  }
  if (pending)
    appendFileSync(resolvedFilePath, "\n");
  return entries;
}
__name(loadEntriesFromFile, "loadEntriesFromFile");
function parseSessionHeaderCandidate(line) {
  if (!line.trim())
    return void 0;
  const entry = parseSessionEntryLine(line);
  if (!entry)
    return void 0;
  if (entry.type !== "session" || typeof entry.id !== "string")
    return null;
  return entry;
}
__name(parseSessionHeaderCandidate, "parseSessionHeaderCandidate");
function readSessionHeader(filePath) {
  const fd = openSync(filePath, "r");
  try {
    const decoder = new StringDecoder("utf8");
    const buffer = Buffer.allocUnsafe(SESSION_HEADER_READ_BUFFER_SIZE);
    const lineChunks = [];
    let scannedBytes = 0;
    while (scannedBytes < MAX_SESSION_HEADER_SCAN_BYTES) {
      const readLength = Math.min(buffer.length, MAX_SESSION_HEADER_SCAN_BYTES - scannedBytes);
      const bytesRead = readSync(fd, buffer, 0, readLength, null);
      if (bytesRead === 0) {
        lineChunks.push(decoder.end());
        return parseSessionHeaderCandidate(lineChunks.join("")) ?? null;
      }
      scannedBytes += bytesRead;
      const chunk = decoder.write(buffer.subarray(0, bytesRead));
      let lineStart = 0;
      let newlineIndex = chunk.indexOf("\n", lineStart);
      while (newlineIndex !== -1) {
        lineChunks.push(chunk.slice(lineStart, newlineIndex));
        const header = parseSessionHeaderCandidate(lineChunks.join(""));
        if (header !== void 0)
          return header;
        lineChunks.length = 0;
        lineStart = newlineIndex + 1;
        newlineIndex = chunk.indexOf("\n", lineStart);
      }
      lineChunks.push(chunk.slice(lineStart));
    }
    const probe = Buffer.allocUnsafe(1);
    if (readSync(fd, probe, 0, probe.length, null) === 0) {
      lineChunks.push(decoder.end());
      return parseSessionHeaderCandidate(lineChunks.join("")) ?? null;
    }
    throw new SessionHeaderScanLimitError(filePath);
  } finally {
    closeSync(fd);
  }
}
__name(readSessionHeader, "readSessionHeader");
function readSessionHeaderForDiscovery(filePath) {
  try {
    return readSessionHeader(filePath);
  } catch {
    return null;
  }
}
__name(readSessionHeaderForDiscovery, "readSessionHeaderForDiscovery");
function getSessionHeaderCwd(header) {
  const cwd = header.cwd;
  return typeof cwd === "string" ? cwd : void 0;
}
__name(getSessionHeaderCwd, "getSessionHeaderCwd");
function sessionCwdMatches(cwd, resolvedCwd) {
  return cwd !== void 0 && cwd !== "" && resolvePath(cwd) === resolvedCwd;
}
__name(sessionCwdMatches, "sessionCwdMatches");
function findMostRecentSession(sessionDir, cwd) {
  const resolvedSessionDir = normalizePath(sessionDir);
  const resolvedCwd = cwd ? resolvePath(cwd) : void 0;
  try {
    const files = readdirSync(resolvedSessionDir).filter((file) => file.endsWith(".jsonl")).map((file) => join2(resolvedSessionDir, file)).map((path2) => ({ path: path2, mtime: statSync(path2).mtimeMs })).sort((a, b) => b.mtime - a.mtime);
    for (const { path: path2 } of files) {
      const header = readSessionHeaderForDiscovery(path2);
      if (header && (!resolvedCwd || sessionCwdMatches(getSessionHeaderCwd(header), resolvedCwd)))
        return path2;
    }
    return null;
  } catch {
    return null;
  }
}
__name(findMostRecentSession, "findMostRecentSession");
function isMessageWithContent(message) {
  return typeof message.role === "string" && "content" in message;
}
__name(isMessageWithContent, "isMessageWithContent");
function extractTextContent(message) {
  const content = message.content;
  if (typeof content === "string") {
    return content;
  }
  return content.filter((block) => block.type === "text").map((block) => block.text).join(" ");
}
__name(extractTextContent, "extractTextContent");
function getMessageActivityTime(entry) {
  const message = entry.message;
  if (!isMessageWithContent(message))
    return void 0;
  if (message.role !== "user" && message.role !== "assistant")
    return void 0;
  const msgTimestamp = message.timestamp;
  if (typeof msgTimestamp === "number") {
    return msgTimestamp;
  }
  const t = new Date(entry.timestamp).getTime();
  return Number.isNaN(t) ? void 0 : t;
}
__name(getMessageActivityTime, "getMessageActivityTime");
async function buildSessionInfo(filePath, signal, fileStats) {
  try {
    const stats = fileStats ?? await stat(filePath);
    let header = null;
    let messageCount = 0;
    let firstMessage = "";
    const allMessages = [];
    let name;
    let lastActivityTime;
    const rl = createInterface({
      input: createReadStream(filePath, { encoding: "utf8", signal }),
      crlfDelay: Infinity
    });
    for await (const line of rl) {
      const entry = parseSessionEntryLine(line);
      if (!entry)
        continue;
      if (!header) {
        if (entry.type !== "session")
          return null;
        header = entry;
        continue;
      }
      if (entry.type === "session_info") {
        name = entry.name?.trim() || void 0;
      }
      if (entry.type !== "message")
        continue;
      messageCount++;
      const activityTime = getMessageActivityTime(entry);
      if (typeof activityTime === "number") {
        lastActivityTime = Math.max(lastActivityTime ?? 0, activityTime);
      }
      const message = entry.message;
      if (!isMessageWithContent(message))
        continue;
      if (message.role !== "user" && message.role !== "assistant")
        continue;
      const textContent = extractTextContent(message);
      if (!textContent)
        continue;
      allMessages.push(textContent);
      if (!firstMessage && message.role === "user") {
        firstMessage = textContent;
      }
    }
    if (!header)
      return null;
    const cwd = typeof header.cwd === "string" ? header.cwd : "";
    const parentSessionPath = header.parentSession;
    const headerTime = typeof header.timestamp === "string" ? new Date(header.timestamp).getTime() : NaN;
    const modified = typeof lastActivityTime === "number" && lastActivityTime > 0 ? new Date(lastActivityTime) : !Number.isNaN(headerTime) ? new Date(headerTime) : stats.mtime;
    return {
      path: filePath,
      id: header.id,
      cwd,
      name,
      parentSessionPath,
      created: new Date(header.timestamp),
      modified,
      messageCount,
      firstMessage: firstMessage || "(no messages)",
      allMessagesText: allMessages.join(" ")
    };
  } catch {
    signal?.throwIfAborted();
    return null;
  }
}
__name(buildSessionInfo, "buildSessionInfo");
var MAX_CONCURRENT_SESSION_INFO_LOADS = 10;
var MAX_CONCURRENT_SESSION_DISCOVERY_LOADS = 64;
var CURRENT_SESSION_LIST_PUBLISH_INTERVAL = 10;
var ALL_SESSION_LIST_PUBLISH_INTERVAL = 100;
async function mapWithConcurrency(items, limit, map, signal) {
  const results = new Array(items.length);
  let nextIndex = 0;
  const worker = /* @__PURE__ */ __name(async () => {
    while (nextIndex < items.length) {
      signal?.throwIfAborted();
      const index = nextIndex++;
      results[index] = await map(items[index], index);
    }
  }, "worker");
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, () => worker()));
  return results;
}
__name(mapWithConcurrency, "mapWithConcurrency");
function sortSessionInfos(sessions) {
  return sessions.sort((a, b) => b.modified.getTime() - a.modified.getTime());
}
__name(sortSessionInfos, "sortSessionInfos");
function buildSessionInfosWithConcurrency(files, onLoaded, signal) {
  return mapWithConcurrency(files, MAX_CONCURRENT_SESSION_INFO_LOADS, async (file, index) => {
    const info = await buildSessionInfo(file.path, signal, file.stats);
    onLoaded(info, index);
    return info;
  }, signal);
}
__name(buildSessionInfosWithConcurrency, "buildSessionInfosWithConcurrency");
async function listSessionsFromDir(dir, onProgress, signal) {
  signal?.throwIfAborted();
  if (!existsSync(dir))
    return [];
  try {
    const dirEntries = await readdir(dir);
    const files = dirEntries.filter((file) => file.endsWith(".jsonl")).sort((a, b) => b.localeCompare(a)).map((file) => ({ path: join2(dir, file) }));
    const total = files.length;
    const partialSessions = [];
    let loaded = 0;
    const results = await buildSessionInfosWithConcurrency(files, (info) => {
      loaded++;
      if (info)
        partialSessions.push(info);
      const publishPartial = loaded === 1 || loaded % CURRENT_SESSION_LIST_PUBLISH_INTERVAL === 0 || loaded === files.length;
      onProgress?.(loaded, total, publishPartial ? sortSessionInfos([...partialSessions]) : void 0);
    }, signal);
    return results.filter((info) => info !== null);
  } catch {
    signal?.throwIfAborted();
    return [];
  }
}
__name(listSessionsFromDir, "listSessionsFromDir");
var SessionManager = class _SessionManager {
  static {
    __name(this, "SessionManager");
  }
  sessionId = "";
  sessionFile;
  sessionDir;
  cwd;
  persist;
  flushed = false;
  fileEntries = [];
  byId = /* @__PURE__ */ new Map();
  labelsById = /* @__PURE__ */ new Map();
  labelTimestampsById = /* @__PURE__ */ new Map();
  leafId = null;
  constructor(cwd, sessionDir, sessionFile, persist, newSessionOptions, preloadedFileEntries) {
    this.cwd = resolvePath(cwd);
    this.sessionDir = normalizePath(sessionDir);
    this.persist = persist;
    if (persist && this.sessionDir && !existsSync(this.sessionDir)) {
      mkdirSync(this.sessionDir, { recursive: true });
    }
    if (sessionFile) {
      this._setSessionFile(sessionFile, preloadedFileEntries);
    } else if (preloadedFileEntries?.length) {
      this._loadEntries(preloadedFileEntries, newSessionOptions);
    } else {
      this.newSession(newSessionOptions);
    }
  }
  /** Switch to a different session file (used for resume and branching) */
  setSessionFile(sessionFile) {
    this._setSessionFile(sessionFile);
  }
  _setSessionFile(sessionFile, preloadedFileEntries) {
    this.sessionFile = resolvePath(sessionFile);
    if (existsSync(this.sessionFile)) {
      const entries = preloadedFileEntries ?? loadEntriesFromFile(this.sessionFile);
      if (entries.length === 0) {
        const explicitPath = this.sessionFile;
        if (statSync(explicitPath).size > 0) {
          throw new Error(`Session file is not a valid ${APP_NAME} session: ${explicitPath}`);
        }
        this.newSession();
        this.sessionFile = explicitPath;
        this._rewriteFile();
        this.flushed = true;
        return;
      }
      this._loadEntries(entries);
      this.flushed = true;
    } else {
      const explicitPath = this.sessionFile;
      this.newSession();
      this.sessionFile = explicitPath;
    }
  }
  newSession(options) {
    if (options?.id !== void 0) {
      assertValidSessionId(options.id);
    }
    this.sessionId = options?.id ?? createSessionId();
    const timestamp = (/* @__PURE__ */ new Date()).toISOString();
    const header = {
      type: "session",
      version: CURRENT_SESSION_VERSION,
      id: this.sessionId,
      timestamp,
      cwd: this.cwd,
      parentSession: options?.parentSession
    };
    this.fileEntries = [header];
    this.byId.clear();
    this.labelsById.clear();
    this.labelTimestampsById.clear();
    this.leafId = null;
    this.flushed = false;
    if (this.persist) {
      const fileTimestamp = timestamp.replace(/[:.]/g, "-");
      this.sessionFile = join2(this.getSessionDir(), `${fileTimestamp}_${this.sessionId}.jsonl`);
    }
    return this.sessionFile;
  }
  _loadEntries(entries, options) {
    const header = entries.find((e) => e.type === "session");
    if (header) {
      this.fileEntries = entries;
      this.sessionId = header.id;
      if (migrateToCurrentVersion(this.fileEntries)) {
        this._rewriteFile();
      }
    } else {
      this.newSession(options);
      this.fileEntries = this.fileEntries.concat(entries);
    }
    this._buildIndex();
  }
  _buildIndex() {
    this.byId.clear();
    this.labelsById.clear();
    this.labelTimestampsById.clear();
    this.leafId = null;
    for (const entry of this.fileEntries) {
      if (entry.type === "session")
        continue;
      this.byId.set(entry.id, entry);
      this.leafId = entry.id;
      if (entry.type === "label") {
        if (entry.label) {
          this.labelsById.set(entry.targetId, entry.label);
          this.labelTimestampsById.set(entry.targetId, entry.timestamp);
        } else {
          this.labelsById.delete(entry.targetId);
          this.labelTimestampsById.delete(entry.targetId);
        }
      }
    }
  }
  _rewriteFile() {
    if (!this.persist || !this.sessionFile)
      return;
    const fd = openSync(this.sessionFile, "w");
    try {
      for (const entry of this.fileEntries) {
        writeFileSync(fd, `${JSON.stringify(entry)}
`);
      }
    } finally {
      closeSync(fd);
    }
  }
  isPersisted() {
    return this.persist;
  }
  getCwd() {
    return this.cwd;
  }
  getSessionDir() {
    return this.sessionDir;
  }
  usesDefaultSessionDir() {
    return this.sessionDir === getDefaultSessionDirPath(this.cwd);
  }
  getSessionId() {
    return this.sessionId;
  }
  getSessionFile() {
    return this.sessionFile;
  }
  _persist(entry) {
    if (!this.persist || !this.sessionFile)
      return;
    const hasAssistant = this.fileEntries.some((e) => e.type === "message" && e.message.role === "assistant");
    if (!hasAssistant) {
      if (this.flushed) {
        appendFileSync(this.sessionFile, `${JSON.stringify(entry)}
`);
      } else {
        this.flushed = false;
      }
      return;
    }
    if (!this.flushed) {
      const fd = openSync(this.sessionFile, "wx");
      try {
        for (const e of this.fileEntries) {
          writeFileSync(fd, `${JSON.stringify(e)}
`);
        }
      } finally {
        closeSync(fd);
      }
      this.flushed = true;
    } else {
      appendFileSync(this.sessionFile, `${JSON.stringify(entry)}
`);
    }
  }
  _appendEntry(entry) {
    this.fileEntries.push(entry);
    this.byId.set(entry.id, entry);
    this.leafId = entry.id;
    this._persist(entry);
  }
  /** Append a message as child of current leaf, then advance leaf. Returns entry id.
   * Does not allow writing CompactionSummaryMessage and BranchSummaryMessage directly.
   * Reason: we want these to be top-level entries in the session, not message session entries,
   * so it is easier to find them.
   * These need to be appended via appendCompaction() and appendBranchSummary() methods.
   */
  appendMessage(message) {
    const entry = {
      type: "message",
      id: generateId(this.byId),
      parentId: this.leafId,
      timestamp: (/* @__PURE__ */ new Date()).toISOString(),
      message
    };
    this._appendEntry(entry);
    return entry.id;
  }
  /** Append a thinking level change as child of current leaf, then advance leaf. Returns entry id. */
  appendThinkingLevelChange(thinkingLevel) {
    const entry = {
      type: "thinking_level_change",
      id: generateId(this.byId),
      parentId: this.leafId,
      timestamp: (/* @__PURE__ */ new Date()).toISOString(),
      thinkingLevel
    };
    this._appendEntry(entry);
    return entry.id;
  }
  /** Append a model change as child of current leaf, then advance leaf. Returns entry id. */
  appendModelChange(provider, modelId) {
    const entry = {
      type: "model_change",
      id: generateId(this.byId),
      parentId: this.leafId,
      timestamp: (/* @__PURE__ */ new Date()).toISOString(),
      provider,
      modelId
    };
    this._appendEntry(entry);
    return entry.id;
  }
  /** Append model-attributed usage that does not participate in LLM context. Returns the appended entry. */
  appendUsage(kind, provider, model, usage, note) {
    const entry = {
      type: "usage",
      id: generateId(this.byId),
      parentId: this.leafId,
      timestamp: (/* @__PURE__ */ new Date()).toISOString(),
      kind,
      provider,
      model,
      usage,
      ...note ? { note } : {}
    };
    this._appendEntry(entry);
    return entry;
  }
  /** Append a compaction summary as child of current leaf, then advance leaf. Returns entry id. */
  appendCompaction(summary, firstKeptEntryId, tokensBefore, details, fromHook, usage) {
    const timestamp = (/* @__PURE__ */ new Date()).toISOString();
    const systemMessage = getCurrentSystemMessage(this.buildSessionProjection().messages);
    const id = generateId(this.byId);
    const entry = {
      type: "compaction",
      id,
      parentId: this.leafId,
      timestamp,
      summary,
      firstKeptEntryId: firstKeptEntryId ?? id,
      tokensBefore,
      details,
      usage,
      fromHook,
      ...systemMessage ? { systemMessage: { ...systemMessage, timestamp: new Date(timestamp).getTime() } } : {}
    };
    this._appendEntry(entry);
    return entry.id;
  }
  /** Append a custom entry (for extensions) as child of current leaf, then advance leaf. Returns entry id. */
  appendCustomEntry(customType, data) {
    const entry = {
      type: "custom",
      customType,
      data,
      id: generateId(this.byId),
      parentId: this.leafId,
      timestamp: (/* @__PURE__ */ new Date()).toISOString()
    };
    this._appendEntry(entry);
    return entry.id;
  }
  /** Append a session info entry (e.g., display name). Returns entry id. */
  appendSessionInfo(name) {
    const sanitizedName = name.replace(/[\r\n]+/g, " ").trim();
    const entry = {
      type: "session_info",
      id: generateId(this.byId),
      parentId: this.leafId,
      timestamp: (/* @__PURE__ */ new Date()).toISOString(),
      name: sanitizedName
    };
    this._appendEntry(entry);
    return entry.id;
  }
  /** Get the current session name from the latest session_info entry, if any. */
  getSessionName() {
    const entries = this.getEntries();
    for (let i = entries.length - 1; i >= 0; i--) {
      const entry = entries[i];
      if (entry.type === "session_info") {
        return entry.name?.trim() || void 0;
      }
    }
    return void 0;
  }
  /**
   * Append a custom message entry (for extensions) that participates in LLM context.
   * @param customType Extension identifier for filtering on reload
   * @param content Message content (string or TextContent/ImageContent array)
   * @param display Whether to show in TUI (true = styled display, false = hidden)
   * @param details Optional extension-specific metadata (not sent to LLM)
   * @returns Entry id
   */
  appendCustomMessageEntry(customType, content, display, details) {
    const entry = {
      type: "custom_message",
      customType,
      content,
      display,
      details,
      id: generateId(this.byId),
      parentId: this.leafId,
      timestamp: (/* @__PURE__ */ new Date()).toISOString()
    };
    this._appendEntry(entry);
    return entry.id;
  }
  /** Append a branch-local edit to an earlier model-visible entry. */
  appendContextEdit(targetId, replacement) {
    if (replacement !== null && (typeof replacement !== "object" || !("content" in replacement) || typeof replacement.content !== "string" && !Array.isArray(replacement.content))) {
      throw new Error("Context edit replacement must be null or contain string/array content");
    }
    const target = this.byId.get(targetId);
    if (!target)
      throw new Error(`Entry ${targetId} not found`);
    if (!this.getBranch().some((entry2) => entry2.id === targetId)) {
      throw new Error(`Entry ${targetId} is not on the active branch`);
    }
    const editable = target.type === "custom_message" || target.type === "message" && (target.message.role === "user" || target.message.role === "assistant" || target.message.role === "toolResult");
    if (!editable)
      throw new Error(`Entry ${targetId} does not contribute editable model content`);
    const targetRole = target.type === "message" ? target.message.role : "custom";
    const normalizedReplacement = replacement !== null && (targetRole === "assistant" || targetRole === "toolResult") && typeof replacement.content === "string" ? { content: [{ type: "text", text: replacement.content }] } : replacement;
    const entry = {
      type: "context_edit",
      id: generateId(this.byId),
      parentId: this.leafId,
      timestamp: (/* @__PURE__ */ new Date()).toISOString(),
      targetId,
      replacement: normalizedReplacement
    };
    this._appendEntry(entry);
    return entry.id;
  }
  // =========================================================================
  // Tree Traversal
  // =========================================================================
  getLeafId() {
    return this.leafId;
  }
  getLeafEntry() {
    return this.leafId ? this.byId.get(this.leafId) : void 0;
  }
  getEntry(id) {
    return this.byId.get(id);
  }
  /**
   * Get all direct children of an entry.
   */
  getChildren(parentId) {
    const children = [];
    for (const entry of this.byId.values()) {
      if (entry.parentId === parentId) {
        children.push(entry);
      }
    }
    return children;
  }
  /**
   * Get the label for an entry, if any.
   */
  getLabel(id) {
    return this.labelsById.get(id);
  }
  /**
   * Set or clear a label on an entry.
   * Labels are user-defined markers for bookmarking/navigation.
   * Pass undefined or empty string to clear the label.
   */
  appendLabelChange(targetId, label) {
    if (!this.byId.has(targetId)) {
      throw new Error(`Entry ${targetId} not found`);
    }
    const entry = {
      type: "label",
      id: generateId(this.byId),
      parentId: this.leafId,
      timestamp: (/* @__PURE__ */ new Date()).toISOString(),
      targetId,
      label
    };
    this._appendEntry(entry);
    if (label) {
      this.labelsById.set(targetId, label);
      this.labelTimestampsById.set(targetId, entry.timestamp);
    } else {
      this.labelsById.delete(targetId);
      this.labelTimestampsById.delete(targetId);
    }
    return entry.id;
  }
  /**
   * Walk from entry to root, returning all entries in path order.
   * Includes all entry types (messages, compaction, model changes, etc.).
   * Use buildSessionContext() to get the resolved messages for the LLM.
   */
  getBranch(fromId) {
    const path2 = [];
    const startId = fromId ?? this.leafId;
    let current = startId ? this.byId.get(startId) : void 0;
    while (current) {
      path2.push(current);
      current = current.parentId ? this.byId.get(current.parentId) : void 0;
    }
    path2.reverse();
    return path2;
  }
  /**
   * Build the active, compaction-aware entry list for context/rendering.
   * Uses tree traversal from current leaf.
   */
  buildContextEntries() {
    return buildContextEntries(this.getEntries(), this.leafId, this.byId);
  }
  /**
   * Build the session context (what gets sent to the LLM).
   * Uses tree traversal from current leaf.
   */
  buildSessionProjection() {
    return buildSessionProjection(this.getEntries(), this.leafId, this.byId);
  }
  buildSessionContext() {
    const { messages, thinkingLevel, model } = this.buildSessionProjection();
    return { messages, thinkingLevel, model };
  }
  /**
   * Get session header.
   */
  getHeader() {
    const h = this.fileEntries.find((e) => e.type === "session");
    return h ? h : null;
  }
  /**
   * Get all session entries (excludes header). Returns a shallow copy.
   * The session is append-only: use appendXXX() to add entries, branch() to
   * change the leaf pointer. Entries cannot be modified or deleted.
   */
  getEntries() {
    return this.fileEntries.filter((e) => e.type !== "session");
  }
  /**
   * Get the session as a tree structure. Returns a shallow defensive copy of all entries.
   * A well-formed session has exactly one root (first entry with parentId === null).
   * Orphaned entries (broken parent chain) are also returned as roots.
   */
  getTree() {
    const entries = this.getEntries();
    const nodeMap = /* @__PURE__ */ new Map();
    const roots = [];
    for (const entry of entries) {
      const label = this.labelsById.get(entry.id);
      const labelTimestamp = this.labelTimestampsById.get(entry.id);
      nodeMap.set(entry.id, { entry, children: [], label, labelTimestamp });
    }
    for (const entry of entries) {
      const node = nodeMap.get(entry.id);
      if (entry.parentId === null || entry.parentId === entry.id) {
        roots.push(node);
      } else {
        const parent = nodeMap.get(entry.parentId);
        if (parent) {
          parent.children.push(node);
        } else {
          roots.push(node);
        }
      }
    }
    const stack = [...roots];
    while (stack.length > 0) {
      const node = stack.pop();
      node.children.sort((a, b) => new Date(a.entry.timestamp).getTime() - new Date(b.entry.timestamp).getTime());
      stack.push(...node.children);
    }
    return roots;
  }
  // =========================================================================
  // Branching
  // =========================================================================
  /**
   * Start a new branch from an earlier entry.
   * Moves the leaf pointer to the specified entry. The next appendXXX() call
   * will create a child of that entry, forming a new branch. Existing entries
   * are not modified or deleted.
   */
  branch(branchFromId) {
    if (!this.byId.has(branchFromId)) {
      throw new Error(`Entry ${branchFromId} not found`);
    }
    this.leafId = branchFromId;
  }
  /**
   * Reset the leaf pointer to null (before any entries).
   * The next appendXXX() call will create a new root entry (parentId = null).
   * Use this when navigating to re-edit the first user message.
   */
  resetLeaf() {
    this.leafId = null;
  }
  /**
   * Start a new branch with a summary of the abandoned path.
   * Same as branch(), but also appends a branch_summary entry that captures
   * context from the abandoned conversation path.
   */
  branchWithSummary(branchFromId, summary, details, fromHook, usage) {
    if (branchFromId !== null && !this.byId.has(branchFromId)) {
      throw new Error(`Entry ${branchFromId} not found`);
    }
    const fromId = this.leafId ?? "root";
    this.leafId = branchFromId;
    const entry = {
      type: "branch_summary",
      id: generateId(this.byId),
      parentId: branchFromId,
      timestamp: (/* @__PURE__ */ new Date()).toISOString(),
      fromId,
      summary,
      details,
      usage,
      fromHook
    };
    this._appendEntry(entry);
    return entry.id;
  }
  /**
   * Create a new session file containing only the path from root to the specified leaf.
   * Useful for extracting a single conversation path from a branched session.
   * Returns the new session file path, or undefined if not persisting.
   */
  createBranchedSession(leafId) {
    const previousSessionFile = this.sessionFile;
    const path2 = this.getBranch(leafId);
    if (path2.length === 0) {
      throw new Error(`Entry ${leafId} not found`);
    }
    const pathWithoutLabels = [];
    const replacementByLabelId = /* @__PURE__ */ new Map();
    const pendingLabelIds = [];
    let pathParentId = null;
    for (const entry of path2) {
      if (entry.type === "label") {
        pendingLabelIds.push(entry.id);
        continue;
      }
      for (const labelId of pendingLabelIds) {
        replacementByLabelId.set(labelId, entry.id);
      }
      pendingLabelIds.length = 0;
      pathWithoutLabels.push(entry.type === "compaction" ? {
        ...entry,
        parentId: pathParentId,
        firstKeptEntryId: entry.firstKeptEntryId === entry.id ? entry.id : replacementByLabelId.get(entry.firstKeptEntryId) ?? entry.firstKeptEntryId
      } : { ...entry, parentId: pathParentId });
      pathParentId = entry.id;
    }
    const newSessionId = createSessionId();
    const timestamp = (/* @__PURE__ */ new Date()).toISOString();
    const fileTimestamp = timestamp.replace(/[:.]/g, "-");
    const newSessionFile = join2(this.getSessionDir(), `${fileTimestamp}_${newSessionId}.jsonl`);
    const header = {
      type: "session",
      version: CURRENT_SESSION_VERSION,
      id: newSessionId,
      timestamp,
      cwd: this.cwd,
      parentSession: this.persist ? previousSessionFile : void 0
    };
    const pathEntryIds = new Set(pathWithoutLabels.map((e) => e.id));
    const labelsToWrite = [];
    for (const [targetId, label] of this.labelsById) {
      if (pathEntryIds.has(targetId)) {
        labelsToWrite.push({ targetId, label, timestamp: this.labelTimestampsById.get(targetId) });
      }
    }
    if (this.persist) {
      const lastEntryId = pathWithoutLabels[pathWithoutLabels.length - 1]?.id || null;
      let parentId2 = lastEntryId;
      const labelEntries2 = [];
      for (const { targetId, label, timestamp: labelTimestamp } of labelsToWrite) {
        const labelEntry = {
          type: "label",
          id: generateId(new Set(pathEntryIds)),
          parentId: parentId2,
          timestamp: labelTimestamp,
          targetId,
          label
        };
        pathEntryIds.add(labelEntry.id);
        labelEntries2.push(labelEntry);
        parentId2 = labelEntry.id;
      }
      this.fileEntries = [header, ...pathWithoutLabels, ...labelEntries2];
      this.sessionId = newSessionId;
      this.sessionFile = newSessionFile;
      this._buildIndex();
      const hasAssistant = this.fileEntries.some((e) => e.type === "message" && e.message.role === "assistant");
      if (hasAssistant) {
        this._rewriteFile();
        this.flushed = true;
      } else {
        this.flushed = false;
      }
      return newSessionFile;
    }
    const labelEntries = [];
    let parentId = pathWithoutLabels[pathWithoutLabels.length - 1]?.id || null;
    for (const { targetId, label, timestamp: labelTimestamp } of labelsToWrite) {
      const labelEntry = {
        type: "label",
        id: generateId(/* @__PURE__ */ new Set([...pathEntryIds, ...labelEntries.map((e) => e.id)])),
        parentId,
        timestamp: labelTimestamp,
        targetId,
        label
      };
      labelEntries.push(labelEntry);
      parentId = labelEntry.id;
    }
    this.fileEntries = [header, ...pathWithoutLabels, ...labelEntries];
    this.sessionId = newSessionId;
    this._buildIndex();
    return void 0;
  }
  /**
   * Create a new session.
   * @param cwd Working directory (stored in session header)
   * @param sessionDir Optional session directory. If omitted, uses default (~/.pi/agent/sessions/<encoded-cwd>/).
   */
  static create(cwd, sessionDir, options) {
    const dir = sessionDir ? normalizePath(sessionDir) : getDefaultSessionDir(cwd);
    return new _SessionManager(cwd, dir, void 0, true, options);
  }
  /**
   * Open a specific session file.
   * @param path Path to session file
   * @param sessionDir Optional session directory for /new or /branch. If omitted, derives from file's parent.
   * @param cwdOverride Optional cwd override instead of the session header cwd.
   */
  static open(path2, sessionDir, cwdOverride) {
    const resolvedPath = resolvePath(path2);
    let header = null;
    let preloadedFileEntries;
    if (cwdOverride === void 0 && existsSync(resolvedPath)) {
      try {
        header = readSessionHeader(resolvedPath);
      } catch (error) {
        if (!(error instanceof SessionHeaderScanLimitError))
          throw error;
        preloadedFileEntries = loadEntriesFromFile(resolvedPath);
        const firstEntry = preloadedFileEntries[0];
        header = firstEntry?.type === "session" ? firstEntry : null;
      }
    }
    const cwd = cwdOverride ?? (header ? getSessionHeaderCwd(header) : void 0) ?? process.cwd();
    const dir = sessionDir ? normalizePath(sessionDir) : resolve(resolvedPath, "..");
    return new _SessionManager(cwd, dir, resolvedPath, true, void 0, preloadedFileEntries);
  }
  /**
   * Continue the most recent session, or create new if none.
   * @param cwd Working directory
   * @param sessionDir Optional session directory. If omitted, uses default (~/.pi/agent/sessions/<encoded-cwd>/).
   */
  static continueRecent(cwd, sessionDir) {
    const dir = sessionDir ? normalizePath(sessionDir) : getDefaultSessionDir(cwd);
    const filterCwd = sessionDir !== void 0 && dir !== getDefaultSessionDirPath(cwd);
    const mostRecent = findMostRecentSession(dir, filterCwd ? cwd : void 0);
    if (mostRecent) {
      return new _SessionManager(cwd, dir, mostRecent, true);
    }
    return new _SessionManager(cwd, dir, void 0, true);
  }
  /** Create an in-memory session (no file persistence), optionally from entries held outside the filesystem. */
  static inMemory(cwd = process.cwd(), options, entries) {
    return new _SessionManager(cwd, "", void 0, false, options, entries);
  }
  /**
   * Fork a session from another project directory into the current project.
   * Creates a new session in the target cwd with the full history from the source session.
   * @param sourcePath Path to the source session file
   * @param targetCwd Target working directory (where the new session will be stored)
   * @param sessionDir Optional session directory. If omitted, uses default for targetCwd.
   */
  static forkFrom(sourcePath, targetCwd, sessionDir, options) {
    const resolvedSourcePath = resolvePath(sourcePath);
    const resolvedTargetCwd = resolvePath(targetCwd);
    const sourceEntries = loadEntriesFromFile(resolvedSourcePath);
    if (sourceEntries.length === 0) {
      throw new Error(`Cannot fork: source session file is empty or invalid: ${resolvedSourcePath}`);
    }
    const sourceHeader = sourceEntries.find((e) => e.type === "session");
    if (!sourceHeader) {
      throw new Error(`Cannot fork: source session has no header: ${resolvedSourcePath}`);
    }
    const dir = sessionDir ? normalizePath(sessionDir) : getDefaultSessionDir(resolvedTargetCwd);
    if (!existsSync(dir)) {
      mkdirSync(dir, { recursive: true });
    }
    if (options?.id !== void 0) {
      assertValidSessionId(options.id);
    }
    const newSessionId = options?.id ?? createSessionId();
    const timestamp = (/* @__PURE__ */ new Date()).toISOString();
    const fileTimestamp = timestamp.replace(/[:.]/g, "-");
    const newSessionFile = join2(dir, `${fileTimestamp}_${newSessionId}.jsonl`);
    const newHeader = {
      type: "session",
      version: CURRENT_SESSION_VERSION,
      id: newSessionId,
      timestamp,
      cwd: resolvedTargetCwd,
      parentSession: resolvedSourcePath
    };
    writeFileSync(newSessionFile, `${JSON.stringify(newHeader)}
`, { flag: "wx" });
    for (const entry of sourceEntries) {
      if (entry.type !== "session") {
        appendFileSync(newSessionFile, `${JSON.stringify(entry)}
`);
      }
    }
    return new _SessionManager(resolvedTargetCwd, dir, newSessionFile, true);
  }
  /**
   * Find an exact session ID without loading transcript bodies.
   * @param cwd Working directory (used to compute default session directory)
   * @param id Exact session ID
   * @param sessionDir Optional session directory. If omitted, uses default (~/.pi/agent/sessions/<encoded-cwd>/).
   */
  static findById(cwd, id, sessionDir) {
    const dir = sessionDir ? normalizePath(sessionDir) : getDefaultSessionDir(cwd);
    const filterCwd = sessionDir !== void 0 && dir !== getDefaultSessionDirPath(cwd);
    const resolvedCwd = resolvePath(cwd);
    try {
      for (const file of readdirSync(dir)) {
        if (!file.endsWith(".jsonl"))
          continue;
        const path2 = join2(dir, file);
        const header = readSessionHeaderForDiscovery(path2);
        if (header?.id !== id)
          continue;
        if (filterCwd && !sessionCwdMatches(getSessionHeaderCwd(header), resolvedCwd))
          continue;
        return path2;
      }
    } catch {
    }
    return void 0;
  }
  /**
   * List all sessions for a directory.
   * @param cwd Working directory (used to compute default session directory)
   * @param sessionDir Optional session directory. If omitted, uses default (~/.pi/agent/sessions/<encoded-cwd>/).
   * @param onProgress Optional callback for progress updates (loaded, total)
   */
  static async list(cwd, sessionDir, onProgress, signal) {
    const dir = sessionDir ? normalizePath(sessionDir) : getDefaultSessionDir(cwd);
    const filterCwd = sessionDir !== void 0 && dir !== getDefaultSessionDirPath(cwd);
    const resolvedCwd = resolvePath(cwd);
    const includeSession = /* @__PURE__ */ __name((session) => !filterCwd || sessionCwdMatches(session.cwd, resolvedCwd), "includeSession");
    const progress = onProgress ? (loaded, total, partialSessions) => onProgress(loaded, total, partialSessions?.filter(includeSession)) : void 0;
    const sessions = (await listSessionsFromDir(dir, progress, signal)).filter(includeSession);
    return sortSessionInfos(sessions);
  }
  static async listAll(sessionDirOrOnProgress, onProgressOrSignal, signal) {
    const customSessionDir = typeof sessionDirOrOnProgress === "string" ? normalizePath(sessionDirOrOnProgress) : void 0;
    const progress = typeof sessionDirOrOnProgress === "function" ? sessionDirOrOnProgress : typeof onProgressOrSignal === "function" ? onProgressOrSignal : void 0;
    const abortSignal = typeof sessionDirOrOnProgress === "string" || typeof onProgressOrSignal === "function" ? signal : onProgressOrSignal ?? signal;
    abortSignal?.throwIfAborted();
    if (customSessionDir) {
      return sortSessionInfos(await listSessionsFromDir(customSessionDir, progress, abortSignal));
    }
    const sessionsDir = getSessionsDir();
    try {
      if (!existsSync(sessionsDir))
        return [];
      const entries = await readdir(sessionsDir, { withFileTypes: true });
      const dirs = entries.filter((entry) => entry.isDirectory() || entry.isSymbolicLink()).map((entry) => join2(sessionsDir, entry.name));
      const dirFiles = await mapWithConcurrency(dirs, MAX_CONCURRENT_SESSION_DISCOVERY_LOADS, async (dir) => {
        try {
          return (await readdir(dir)).filter((file) => file.endsWith(".jsonl")).map((file) => join2(dir, file));
        } catch {
          return [];
        }
      }, abortSignal);
      const allFiles = dirFiles.flat();
      const candidates = await mapWithConcurrency(allFiles, MAX_CONCURRENT_SESSION_DISCOVERY_LOADS, async (path2) => {
        try {
          return { path: path2, stats: await stat(path2) };
        } catch {
          return { path: path2 };
        }
      }, abortSignal);
      candidates.sort((a, b) => (b.stats?.mtimeMs ?? Number.NEGATIVE_INFINITY) - (a.stats?.mtimeMs ?? Number.NEGATIVE_INFINITY) || basename(b.path).localeCompare(basename(a.path)));
      const totalFiles = candidates.length;
      let loaded = 0;
      let firstCandidateLoaded = false;
      const partialSessions = [];
      const results = await buildSessionInfosWithConcurrency(candidates, (info, index) => {
        loaded++;
        if (index === 0)
          firstCandidateLoaded = true;
        if (info)
          partialSessions.push(info);
        const publishPartial = firstCandidateLoaded && (index === 0 || loaded % ALL_SESSION_LIST_PUBLISH_INTERVAL === 0 || loaded === totalFiles);
        progress?.(loaded, totalFiles, publishPartial ? sortSessionInfos([...partialSessions]) : void 0);
      }, abortSignal);
      return sortSessionInfos(results.filter((info) => info !== null));
    } catch {
      abortSignal?.throwIfAborted();
      return [];
    }
  }
};

// pi-dist/pi-coding-agent/core/compaction/utils.js
import { contentText } from "../../pi-ai/sdk-bundle/index.js";
function createFileOps() {
  return {
    read: /* @__PURE__ */ new Set(),
    written: /* @__PURE__ */ new Set(),
    edited: /* @__PURE__ */ new Set()
  };
}
__name(createFileOps, "createFileOps");
function extractFileOpsFromMessage(message, fileOps) {
  if (message.role !== "assistant")
    return;
  if (!("content" in message) || !Array.isArray(message.content))
    return;
  for (const block of message.content) {
    if (typeof block !== "object" || block === null)
      continue;
    if (!("type" in block) || block.type !== "toolCall")
      continue;
    if (!("arguments" in block) || !("name" in block))
      continue;
    const args = block.arguments;
    if (!args)
      continue;
    const path2 = typeof args.path === "string" ? args.path : void 0;
    if (!path2)
      continue;
    switch (block.name) {
      case "read":
        fileOps.read.add(path2);
        break;
      case "write":
        fileOps.written.add(path2);
        break;
      case "edit":
        fileOps.edited.add(path2);
        break;
    }
  }
}
__name(extractFileOpsFromMessage, "extractFileOpsFromMessage");
function computeFileLists(fileOps) {
  const modified = /* @__PURE__ */ new Set([...fileOps.edited, ...fileOps.written]);
  const readOnly = [...fileOps.read].filter((f) => !modified.has(f)).sort();
  const modifiedFiles = [...modified].sort();
  return { readFiles: readOnly, modifiedFiles };
}
__name(computeFileLists, "computeFileLists");
function formatFileOperations(readFiles, modifiedFiles) {
  const sections = [];
  if (readFiles.length > 0) {
    sections.push(`<read-files>
${readFiles.join("\n")}
</read-files>`);
  }
  if (modifiedFiles.length > 0) {
    sections.push(`<modified-files>
${modifiedFiles.join("\n")}
</modified-files>`);
  }
  if (sections.length === 0)
    return "";
  return `

${sections.join("\n\n")}`;
}
__name(formatFileOperations, "formatFileOperations");
var TOOL_RESULT_MAX_CHARS = 2e3;
function truncateForSummary(text, maxChars) {
  if (text.length <= maxChars)
    return text;
  const truncatedChars = text.length - maxChars;
  return `${text.slice(0, maxChars)}

[... ${truncatedChars} more characters truncated]`;
}
__name(truncateForSummary, "truncateForSummary");
function serializeConversation(messages) {
  const parts = [];
  for (const msg of messages) {
    if (msg.role === "user") {
      const content = contentText(msg.content, "");
      if (content)
        parts.push(`[User]: ${content}`);
    } else if (msg.role === "assistant") {
      const thinkingParts = [];
      const toolCalls = [];
      for (const block of msg.content) {
        if (block.type === "thinking") {
          thinkingParts.push(block.thinking);
        } else if (block.type === "toolCall") {
          const args = block.arguments;
          const argsStr = Object.entries(args).map(([k, v]) => `${k}=${JSON.stringify(v)}`).join(", ");
          toolCalls.push(`${block.name}(${argsStr})`);
        }
      }
      if (thinkingParts.length > 0) {
        parts.push(`[Assistant thinking]: ${thinkingParts.join("\n")}`);
      }
      if (msg.content.some((block) => block.type === "text")) {
        parts.push(`[Assistant]: ${contentText(msg.content)}`);
      }
      if (toolCalls.length > 0) {
        parts.push(`[Assistant tool calls]: ${toolCalls.join("; ")}`);
      }
    } else if (msg.role === "toolResult") {
      const content = contentText(msg.content, "");
      if (content) {
        parts.push(`[Tool result]: ${truncateForSummary(content, TOOL_RESULT_MAX_CHARS)}`);
      }
    }
  }
  return parts.join("\n\n");
}
__name(serializeConversation, "serializeConversation");
var SUMMARIZATION_SYSTEM_PROMPT = `You are a context summarization assistant. Your task is to read a conversation between a user and an AI assistant, then produce a structured summary following the exact format specified.

Do NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the structured summary.`;

// pi-dist/pi-coding-agent/core/compaction/compaction.js
import { contentText as contentText2, getCurrentSystemMessage as getCurrentSystemMessage2, normalizeContext, retryAssistantCall, uuidv7 as uuidv72 } from "../../pi-ai/sdk-bundle/index.js";
import { completeSimple } from "../../pi-ai/sdk-bundle/compat.js";
function extractFileOperations(messages, entries, prevCompactionIndex) {
  const fileOps = createFileOps();
  if (prevCompactionIndex >= 0) {
    const prevCompaction = entries[prevCompactionIndex];
    if (!prevCompaction.fromHook && prevCompaction.details) {
      const details = prevCompaction.details;
      if (Array.isArray(details.readFiles)) {
        for (const f of details.readFiles)
          fileOps.read.add(f);
      }
      if (Array.isArray(details.modifiedFiles)) {
        for (const f of details.modifiedFiles)
          fileOps.edited.add(f);
      }
    }
  }
  for (const msg of messages) {
    extractFileOpsFromMessage(msg, fileOps);
  }
  return fileOps;
}
__name(extractFileOperations, "extractFileOperations");
function getMessagesFromProjectedEntryForCompaction(entry) {
  if (entry.sourceEntry.type === "compaction")
    return [];
  return entry.messages.filter((message) => message.role !== "system");
}
__name(getMessagesFromProjectedEntryForCompaction, "getMessagesFromProjectedEntryForCompaction");
function combineUsage(first, second) {
  return {
    input: first.input + second.input,
    output: first.output + second.output,
    cacheRead: first.cacheRead + second.cacheRead,
    cacheWrite: first.cacheWrite + second.cacheWrite,
    ...first.cacheWrite1h !== void 0 || second.cacheWrite1h !== void 0 ? { cacheWrite1h: (first.cacheWrite1h ?? 0) + (second.cacheWrite1h ?? 0) } : {},
    ...first.reasoning !== void 0 || second.reasoning !== void 0 ? { reasoning: (first.reasoning ?? 0) + (second.reasoning ?? 0) } : {},
    totalTokens: first.totalTokens + second.totalTokens,
    cost: {
      input: first.cost.input + second.cost.input,
      output: first.cost.output + second.cost.output,
      cacheRead: first.cost.cacheRead + second.cost.cacheRead,
      cacheWrite: first.cost.cacheWrite + second.cost.cacheWrite,
      total: first.cost.total + second.cost.total
    }
  };
}
__name(combineUsage, "combineUsage");
var DEFAULT_COMPACTION_SETTINGS = {
  enabled: true,
  reserveTokens: 16384,
  keepRecentTokens: 2e4
};
function calculateContextTokens(usage) {
  return usage.totalTokens || usage.input + usage.output + usage.cacheRead + usage.cacheWrite;
}
__name(calculateContextTokens, "calculateContextTokens");
function getAssistantUsage(msg) {
  if (msg.role === "assistant" && "usage" in msg) {
    const assistantMsg = msg;
    if (assistantMsg.stopReason !== "aborted" && assistantMsg.stopReason !== "error" && assistantMsg.usage && calculateContextTokens(assistantMsg.usage) > 0) {
      return assistantMsg.usage;
    }
  }
  return void 0;
}
__name(getAssistantUsage, "getAssistantUsage");
function getLastAssistantUsage(entries) {
  for (let i = entries.length - 1; i >= 0; i--) {
    const entry = entries[i];
    if (entry.type === "message") {
      const usage = getAssistantUsage(entry.message);
      if (usage)
        return usage;
    }
  }
  return void 0;
}
__name(getLastAssistantUsage, "getLastAssistantUsage");
function getLastAssistantUsageInfo(messages) {
  for (let i = messages.length - 1; i >= 0; i--) {
    const usage = getAssistantUsage(messages[i]);
    if (usage)
      return { usage, index: i };
  }
  return void 0;
}
__name(getLastAssistantUsageInfo, "getLastAssistantUsageInfo");
function estimateContextTokens(messages) {
  const usageInfo = getLastAssistantUsageInfo(messages);
  if (!usageInfo) {
    let estimated = 0;
    for (const message of messages) {
      estimated += estimateTokens(message);
    }
    return {
      tokens: estimated,
      usageTokens: 0,
      trailingTokens: estimated,
      lastUsageIndex: null
    };
  }
  const usageTokens = calculateContextTokens(usageInfo.usage);
  let trailingTokens = 0;
  for (let i = usageInfo.index + 1; i < messages.length; i++) {
    trailingTokens += estimateTokens(messages[i]);
  }
  return {
    tokens: usageTokens + trailingTokens,
    usageTokens,
    trailingTokens,
    lastUsageIndex: usageInfo.index
  };
}
__name(estimateContextTokens, "estimateContextTokens");
function estimateProjectedContextTokens(projection, branchEntries) {
  const estimate = estimateContextTokens(projection.messages);
  if (estimate.lastUsageIndex !== null) {
    let projectedMessageIndex = 0;
    let usageEntryId;
    for (const entry of projection.entries) {
      const nextMessageIndex = projectedMessageIndex + entry.messages.length;
      if (estimate.lastUsageIndex < nextMessageIndex) {
        usageEntryId = entry.sourceEntry.id;
        break;
      }
      projectedMessageIndex = nextMessageIndex;
    }
    const usageEntryIndex = usageEntryId ? branchEntries.findIndex((entry) => entry.id === usageEntryId) : -1;
    let latestInvalidatingEntryIndex = -1;
    for (let i = branchEntries.length - 1; i >= 0; i--) {
      const entry = branchEntries[i];
      if (entry.type === "context_edit" || entry.type === "compaction") {
        latestInvalidatingEntryIndex = i;
        break;
      }
    }
    if (usageEntryIndex > latestInvalidatingEntryIndex)
      return estimate;
  }
  const currentSystem = getCurrentSystemMessage2(projection.messages);
  let tokens = currentSystem ? estimateTokens(currentSystem) : 0;
  for (const message of projection.messages) {
    if (message.role !== "system")
      tokens += estimateTokens(message);
  }
  return { tokens, usageTokens: 0, trailingTokens: tokens, lastUsageIndex: null };
}
__name(estimateProjectedContextTokens, "estimateProjectedContextTokens");
function shouldCompact(contextTokens, contextWindow, settings) {
  if (!settings.enabled)
    return false;
  return contextTokens > contextWindow - settings.reserveTokens;
}
__name(shouldCompact, "shouldCompact");
var ESTIMATED_IMAGE_CHARS = 4800;
function estimateTextAndImageContentChars(content) {
  if (typeof content === "string") {
    return content.length;
  }
  let chars = 0;
  for (const block of content) {
    if (block.type === "text" && block.text) {
      chars += block.text.length;
    } else if (block.type === "image") {
      chars += ESTIMATED_IMAGE_CHARS;
    }
  }
  return chars;
}
__name(estimateTextAndImageContentChars, "estimateTextAndImageContentChars");
function estimateTokens(message) {
  let chars = 0;
  switch (message.role) {
    case "system": {
      const system = message;
      chars = estimateTextAndImageContentChars(system.content);
      if (system.sections) {
        for (const section of Object.values(system.sections)) {
          if (section)
            chars += section.length;
        }
      }
      if (system.toolsAdded)
        chars += JSON.stringify(system.toolsAdded).length;
      return Math.ceil(chars / 4);
    }
    case "user": {
      chars = estimateTextAndImageContentChars(message.content);
      return Math.ceil(chars / 4);
    }
    case "assistant": {
      const assistant = message;
      for (const block of assistant.content) {
        if (block.type === "text") {
          chars += block.text.length;
        } else if (block.type === "thinking") {
          chars += block.thinking.length;
        } else if (block.type === "toolCall") {
          chars += block.name.length + JSON.stringify(block.arguments).length;
        }
      }
      return Math.ceil(chars / 4);
    }
    case "custom":
    case "toolResult": {
      chars = estimateTextAndImageContentChars(message.content);
      return Math.ceil(chars / 4);
    }
    case "bashExecution": {
      chars = message.command.length + message.output.length;
      return Math.ceil(chars / 4);
    }
    case "branchSummary":
    case "compactionSummary": {
      chars = message.summary.length;
      return Math.ceil(chars / 4);
    }
  }
  return 0;
}
__name(estimateTokens, "estimateTokens");
function isCutPointMessage(message) {
  switch (message.role) {
    case "user":
    case "assistant":
    case "bashExecution":
    case "custom":
    case "branchSummary":
    case "compactionSummary":
      return true;
    case "toolResult":
      return false;
  }
  return false;
}
__name(isCutPointMessage, "isCutPointMessage");
function isTurnStartMessage(message) {
  switch (message.role) {
    case "user":
    case "bashExecution":
    case "custom":
    case "branchSummary":
    case "compactionSummary":
      return true;
    case "assistant":
    case "toolResult":
      return false;
  }
  return false;
}
__name(isTurnStartMessage, "isTurnStartMessage");
function isTurnStartEntry(entry) {
  if (entry.type === "compaction") {
    return false;
  }
  return sessionEntryToContextMessages(entry).some(isTurnStartMessage);
}
__name(isTurnStartEntry, "isTurnStartEntry");
function findValidCutPoints(entries, startIndex, endIndex) {
  const cutPoints = [];
  for (let i = startIndex; i < endIndex; i++) {
    const entry = entries[i];
    if (entry.type === "compaction") {
      continue;
    }
    if (sessionEntryToContextMessages(entry).some(isCutPointMessage)) {
      cutPoints.push(i);
    }
  }
  return cutPoints;
}
__name(findValidCutPoints, "findValidCutPoints");
function findTurnStartIndex(entries, entryIndex, startIndex) {
  for (let i = entryIndex; i >= startIndex; i--) {
    if (isTurnStartEntry(entries[i])) {
      return i;
    }
  }
  return -1;
}
__name(findTurnStartIndex, "findTurnStartIndex");
function findCutPoint(entries, startIndex, endIndex, keepRecentTokens) {
  const cutPoints = findValidCutPoints(entries, startIndex, endIndex);
  if (cutPoints.length === 0) {
    return { firstKeptEntryIndex: startIndex, turnStartIndex: -1, isSplitTurn: false };
  }
  let accumulatedTokens = 0;
  let cutIndex = cutPoints[0];
  for (let i = endIndex - 1; i >= startIndex; i--) {
    const entry = entries[i];
    const messageTokens = sessionEntryToContextMessages(entry).reduce((sum, message) => sum + estimateTokens(message), 0);
    if (messageTokens === 0)
      continue;
    accumulatedTokens += messageTokens;
    if (accumulatedTokens >= keepRecentTokens) {
      cutIndex = cutPoints.find((candidate) => candidate >= i) ?? cutPoints[cutPoints.length - 1];
      break;
    }
  }
  while (cutIndex > startIndex) {
    const prevEntry = entries[cutIndex - 1];
    if (prevEntry.type === "compaction" || sessionEntryToContextMessages(prevEntry).length > 0) {
      break;
    }
    cutIndex--;
  }
  const cutEntry = entries[cutIndex];
  const startsTurn = isTurnStartEntry(cutEntry);
  const turnStartIndex = startsTurn ? -1 : findTurnStartIndex(entries, cutIndex, startIndex);
  return {
    firstKeptEntryIndex: cutIndex,
    turnStartIndex,
    isSplitTurn: !startsTurn && turnStartIndex !== -1
  };
}
__name(findCutPoint, "findCutPoint");
var SUMMARIZATION_PROMPT = `The messages above are a conversation to summarize. Create a structured context checkpoint summary that another LLM will use to continue the work.

Use this EXACT format:

## Goal
[What is the user trying to accomplish? Can be multiple items if the session covers different tasks.]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned by user]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Current work]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [Ordered list of what should happen next]

## Critical Context
- [Any data, examples, or references needed to continue]
- [Or "(none)" if not applicable]

Keep each section concise. Preserve exact file paths, function names, and error messages.`;
var UPDATE_SUMMARIZATION_INSTRUCTIONS = `Update the existing structured summary with new information. RULES:
- PRESERVE all existing information from the previous summary
- ADD new progress, decisions, and context from the new messages
- UPDATE the Progress section: move items from "In Progress" to "Done" when completed
- UPDATE "Next Steps" based on what was accomplished
- PRESERVE exact file paths, function names, and error messages
- If something is no longer relevant, you may remove it

Use this EXACT format:

## Goal
[Preserve existing goals, add new ones if the task expanded]

## Constraints & Preferences
- [Preserve existing, add new ones discovered]

## Progress
### Done
- [x] [Include previously done items AND newly completed items]

### In Progress
- [ ] [Current work - update based on progress]

### Blocked
- [Current blockers - remove if resolved]

## Key Decisions
- **[Decision]**: [Brief rationale] (preserve all previous, add new)

## Next Steps
1. [Update based on current state]

## Critical Context
- [Preserve important context, add new if needed]

Keep each section concise. Preserve exact file paths, function names, and error messages.`;
var UPDATE_SUMMARIZATION_PROMPT = `The messages above are NEW conversation messages to incorporate into the existing summary provided in <previous-summary> tags.

${UPDATE_SUMMARIZATION_INSTRUCTIONS}`;
function getSummarizationFailure(response, label) {
  if (response.stopReason === "error") {
    return `${label} failed: ${response.errorMessage || "Unknown error"}`;
  }
  if (response.stopReason === "length") {
    return `${label} failed: generation hit the token cap and the summary is incomplete`;
  }
  return void 0;
}
__name(getSummarizationFailure, "getSummarizationFailure");
function createSummarizationOptions(model, maxTokens, apiKey, headers, env, signal, thinkingLevel, sessionId) {
  const options = { maxTokens, signal, apiKey, headers, env, sessionId };
  if (model.reasoning && thinkingLevel && thinkingLevel !== "off") {
    options.reasoning = thinkingLevel;
  }
  return options;
}
__name(createSummarizationOptions, "createSummarizationOptions");
async function completeSummarization(model, context, options, streamFn, retry, callbacks) {
  const requestOptions = {
    ...options,
    cacheRetention: "none",
    sessionId: options.sessionId ?? uuidv72()
  };
  const produce = /* @__PURE__ */ __name(async () => streamFn ? (await streamFn(model, context, requestOptions)).result() : completeSimple(model, context, requestOptions), "produce");
  return retryAssistantCall(produce, retry, requestOptions.signal, callbacks);
}
__name(completeSummarization, "completeSummarization");
async function generateSummary(currentMessages, model, reserveTokens, apiKey, headers, signal, customInstructions, previousSummary, thinkingLevel, streamFn, env, retry, callbacks, sessionId) {
  return (await generateSummaryWithUsage(currentMessages, model, reserveTokens, apiKey, headers, signal, customInstructions, previousSummary, thinkingLevel, streamFn, env, retry, callbacks, sessionId)).text;
}
__name(generateSummary, "generateSummary");
function buildSummarizationContext(promptText) {
  return normalizeContext({
    systemPrompt: SUMMARIZATION_SYSTEM_PROMPT,
    messages: [
      {
        role: "user",
        content: [{ type: "text", text: promptText }],
        timestamp: Date.now()
      }
    ]
  });
}
__name(buildSummarizationContext, "buildSummarizationContext");
async function generateSummaryWithUsage(currentMessages, model, reserveTokens, apiKey, headers, signal, customInstructions, previousSummary, thinkingLevel, streamFn, env, retry, callbacks, sessionId) {
  const maxTokens = Math.min(Math.floor(0.8 * reserveTokens), model.maxTokens > 0 ? model.maxTokens : Number.POSITIVE_INFINITY);
  let basePrompt = previousSummary ? UPDATE_SUMMARIZATION_PROMPT : SUMMARIZATION_PROMPT;
  if (customInstructions) {
    basePrompt = `${basePrompt}

Additional focus: ${customInstructions}`;
  }
  const llmMessages = convertToLlm(currentMessages);
  const conversationText = serializeConversation(llmMessages);
  let promptText = `<conversation>
${conversationText}
</conversation>

`;
  if (previousSummary) {
    promptText += `<previous-summary>
${previousSummary}
</previous-summary>

`;
  }
  promptText += basePrompt;
  const completionOptions = createSummarizationOptions(model, maxTokens, apiKey, headers, env, signal, thinkingLevel, sessionId);
  const response = await completeSummarization(model, buildSummarizationContext(promptText), completionOptions, streamFn, retry, callbacks);
  const failure = getSummarizationFailure(response, "Summarization");
  if (failure) {
    throw new Error(failure);
  }
  if (response.content.some((block) => block.type === "toolCall")) {
    throw new Error("Summarization attempted to call a tool");
  }
  const textContent = contentText2(response.content);
  return { text: textContent, usage: response.usage };
}
__name(generateSummaryWithUsage, "generateSummaryWithUsage");
function isProjectedTurnStart(entry) {
  if (entry.sourceEntry.type === "compaction")
    return false;
  return entry.messages.some(isTurnStartMessage);
}
__name(isProjectedTurnStart, "isProjectedTurnStart");
function findProjectedTurnStartIndex(entries, entryIndex, startIndex) {
  for (let i = entryIndex; i >= startIndex; i--) {
    if (isProjectedTurnStart(entries[i]))
      return i;
  }
  return -1;
}
__name(findProjectedTurnStartIndex, "findProjectedTurnStartIndex");
function findProjectedCutPoint(entries, startIndex, endIndex, keepRecentTokens) {
  const cutPoints = [];
  for (let i = startIndex; i < endIndex; i++) {
    const entry = entries[i];
    if (entry.sourceEntry.type !== "compaction" && entry.messages.some(isCutPointMessage))
      cutPoints.push(i);
  }
  if (cutPoints.length === 0) {
    return { firstKeptEntryIndex: startIndex, turnStartIndex: -1, isSplitTurn: false };
  }
  let accumulatedTokens = 0;
  let exceededBudget = false;
  let cutIndex = cutPoints[0];
  for (let i = endIndex - 1; i >= startIndex; i--) {
    const messageTokens = entries[i].messages.reduce((sum, message) => sum + estimateTokens(message), 0);
    if (messageTokens === 0)
      continue;
    accumulatedTokens += messageTokens;
    if (accumulatedTokens >= keepRecentTokens) {
      exceededBudget = true;
      cutIndex = cutPoints.find((candidate) => candidate >= i) ?? cutPoints[cutPoints.length - 1];
      break;
    }
  }
  const suffix = entries.slice(cutIndex + 1, endIndex);
  const isIntrinsicallyVisible = /* @__PURE__ */ __name((entry) => entry.sourceEntry.type !== "context_edit" && sessionEntryToContextMessages(entry.sourceEntry).length > 0, "isIntrinsicallyVisible");
  const isOmitted = /* @__PURE__ */ __name((entry) => isIntrinsicallyVisible(entry) && entry.messages.length === 0, "isOmitted");
  const omittedSuffixIds = new Set(suffix.filter(isOmitted).map((entry) => entry.sourceEntry.id));
  const hasExternalReplacement = suffix.some((entry) => entry.sourceEntry.type === "context_edit" && entry.sourceEntry.replacement !== null && !omittedSuffixIds.has(entry.sourceEntry.targetId));
  const isRecoveryOmissionSuffix = exceededBudget && !hasExternalReplacement && suffix.some((entry) => entry.sourceEntry.type === "message" && entry.sourceEntry.message.role === "assistant" && isOmitted(entry)) && suffix.every((entry) => entry.sourceEntry.type !== "compaction" && (!isIntrinsicallyVisible(entry) || isOmitted(entry)));
  if (isRecoveryOmissionSuffix)
    cutIndex++;
  while (cutIndex > startIndex) {
    const previous = entries[cutIndex - 1];
    if (previous.sourceEntry.type === "compaction" || previous.messages.length > 0)
      break;
    cutIndex--;
  }
  const startsTurn = isProjectedTurnStart(entries[cutIndex]);
  const turnStartIndex = startsTurn ? -1 : findProjectedTurnStartIndex(entries, cutIndex, startIndex);
  return {
    firstKeptEntryIndex: cutIndex,
    turnStartIndex,
    isSplitTurn: !startsTurn && turnStartIndex !== -1
  };
}
__name(findProjectedCutPoint, "findProjectedCutPoint");
function prepareCompaction(pathEntries, settings) {
  if (pathEntries.length > 0 && pathEntries[pathEntries.length - 1].type === "compaction") {
    return void 0;
  }
  const projection = buildSessionProjection(pathEntries);
  const projectedEntries = projection.entries;
  const sourceEntries = projectedEntries.map((entry) => entry.sourceEntry);
  const prevCompactionIndex = projectedEntries.findIndex((entry) => entry.sourceEntry.type === "compaction" && entry.messages.length > 0);
  let previousSummary;
  let boundaryStart = 0;
  if (prevCompactionIndex >= 0) {
    previousSummary = projectedEntries[prevCompactionIndex].sourceEntry.summary;
    boundaryStart = prevCompactionIndex + 1;
  }
  const boundaryEnd = projectedEntries.length;
  const tokensBefore = estimateProjectedContextTokens(projection, pathEntries).tokens;
  const cutPoint = findProjectedCutPoint(projectedEntries, boundaryStart, boundaryEnd, settings.keepRecentTokens);
  const firstKeptEntry = projectedEntries[cutPoint.firstKeptEntryIndex]?.sourceEntry;
  if (!firstKeptEntry?.id)
    return void 0;
  const firstKeptEntryId = firstKeptEntry.id;
  const historyEnd = cutPoint.isSplitTurn ? cutPoint.turnStartIndex : cutPoint.firstKeptEntryIndex;
  const messagesToSummarize = projectedEntries.slice(boundaryStart, historyEnd).flatMap(getMessagesFromProjectedEntryForCompaction);
  const turnPrefixMessages = cutPoint.isSplitTurn ? projectedEntries.slice(cutPoint.turnStartIndex, cutPoint.firstKeptEntryIndex).flatMap(getMessagesFromProjectedEntryForCompaction) : [];
  if (messagesToSummarize.length === 0 && turnPrefixMessages.length === 0)
    return void 0;
  const fileOps = extractFileOperations(messagesToSummarize, sourceEntries, prevCompactionIndex);
  if (cutPoint.isSplitTurn) {
    for (const msg of turnPrefixMessages) {
      extractFileOpsFromMessage(msg, fileOps);
    }
  }
  return {
    firstKeptEntryId,
    messagesToSummarize,
    turnPrefixMessages,
    isSplitTurn: cutPoint.isSplitTurn,
    tokensBefore,
    previousSummary,
    fileOps,
    settings
  };
}
__name(prepareCompaction, "prepareCompaction");
var TURN_PREFIX_SUMMARIZATION_PROMPT = `The messages above are earlier context from an ongoing conversation. Later messages are stored separately and do not need to be reconstructed.

Create a concise checkpoint of the user's request and the progress shown above. This checkpoint will be placed before the later messages so the conversation can continue with the necessary context.

## Original Request
[What did the user ask for?]

## Progress So Far
- [Key decisions and work completed in these messages]

## Context Needed to Continue
- [Information from these messages needed to understand the later work]

Only summarize information explicitly present above. Do not infer or recreate later messages.`;
async function compact(preparation, model, apiKey, headers, customInstructions, signal, thinkingLevel, streamFn, env, retry, callbacks, sessionId) {
  const { firstKeptEntryId, messagesToSummarize, turnPrefixMessages, isSplitTurn, tokensBefore, previousSummary, fileOps, settings } = preparation;
  let summary;
  let summaryUsage;
  if (isSplitTurn && turnPrefixMessages.length > 0) {
    let historyText = previousSummary ?? "No prior history.";
    let historyUsage;
    if (messagesToSummarize.length > 0) {
      const historyResult = await generateSummaryWithUsage(messagesToSummarize, model, settings.reserveTokens, apiKey, headers, signal, customInstructions, previousSummary, thinkingLevel, streamFn, env, retry, callbacks, sessionId);
      historyText = historyResult.text;
      historyUsage = historyResult.usage;
    }
    const turnPrefixResult = await generateTurnPrefixSummary(turnPrefixMessages, model, settings.reserveTokens, apiKey, headers, env, signal, thinkingLevel, streamFn, retry, callbacks, sessionId);
    summary = `${historyText}

---

**Turn Context (split turn):**

${turnPrefixResult.text}`;
    summaryUsage = historyUsage ? combineUsage(historyUsage, turnPrefixResult.usage) : turnPrefixResult.usage;
  } else {
    const result = await generateSummaryWithUsage(messagesToSummarize, model, settings.reserveTokens, apiKey, headers, signal, customInstructions, previousSummary, thinkingLevel, streamFn, env, retry, callbacks, sessionId);
    summary = result.text;
    summaryUsage = result.usage;
  }
  const { readFiles, modifiedFiles } = computeFileLists(fileOps);
  summary += formatFileOperations(readFiles, modifiedFiles);
  if (!firstKeptEntryId) {
    throw new Error("First kept entry has no UUID - session may need migration");
  }
  return {
    summary,
    firstKeptEntryId,
    tokensBefore,
    usage: summaryUsage,
    details: { readFiles, modifiedFiles }
  };
}
__name(compact, "compact");
async function generateTurnPrefixSummary(messages, model, reserveTokens, apiKey, headers, env, signal, thinkingLevel, streamFn, retry, callbacks, sessionId) {
  const maxTokens = Math.min(Math.floor(0.5 * reserveTokens), model.maxTokens > 0 ? model.maxTokens : Number.POSITIVE_INFINITY);
  const llmMessages = convertToLlm(messages);
  const conversationText = serializeConversation(llmMessages);
  const promptText = `# Conversation
${conversationText}

# Instructions
${TURN_PREFIX_SUMMARIZATION_PROMPT}`;
  const response = await completeSummarization(model, buildSummarizationContext(promptText), createSummarizationOptions(model, maxTokens, apiKey, headers, env, signal, thinkingLevel, sessionId), streamFn, retry, callbacks);
  const failure = getSummarizationFailure(response, "Turn prefix summarization");
  if (failure) {
    throw new Error(failure);
  }
  if (response.content.some((block) => block.type === "toolCall")) {
    throw new Error("Turn prefix summarization attempted to call a tool");
  }
  return {
    text: contentText2(response.content),
    usage: response.usage
  };
}
__name(generateTurnPrefixSummary, "generateTurnPrefixSummary");

// pi-dist/pi-coding-agent/core/compaction/branch-summarization.js
import { contentText as contentText3, normalizeContext as normalizeContext2 } from "../../pi-ai/sdk-bundle/index.js";
function collectEntriesForBranchSummary(session, oldLeafId, targetId) {
  if (!oldLeafId) {
    return { entries: [], commonAncestorId: null };
  }
  const oldPath = new Set(session.getBranch(oldLeafId).map((e) => e.id));
  const targetPath = session.getBranch(targetId);
  let commonAncestorId = null;
  for (let i = targetPath.length - 1; i >= 0; i--) {
    if (oldPath.has(targetPath[i].id)) {
      commonAncestorId = targetPath[i].id;
      break;
    }
  }
  const entries = [];
  let current = oldLeafId;
  while (current && current !== commonAncestorId) {
    const entry = session.getEntry(current);
    if (!entry)
      break;
    entries.push(entry);
    current = entry.parentId;
  }
  entries.reverse();
  return { entries, commonAncestorId };
}
__name(collectEntriesForBranchSummary, "collectEntriesForBranchSummary");
function getMessageFromEntry(entry) {
  switch (entry.type) {
    case "message":
      if (entry.message.role === "toolResult")
        return void 0;
      return entry.message;
    case "custom_message":
      return createCustomMessage(entry.customType, entry.content, entry.display, entry.details, entry.timestamp);
    case "branch_summary":
      return createBranchSummaryMessage(entry.summary, entry.fromId, entry.timestamp);
    case "compaction":
      return createCompactionSummaryMessage(entry.summary, entry.tokensBefore, entry.timestamp);
    // These don't contribute to conversation content
    case "thinking_level_change":
    case "model_change":
    case "custom":
    case "label":
    case "session_info":
      return void 0;
  }
}
__name(getMessageFromEntry, "getMessageFromEntry");
function prepareBranchEntries(entries, tokenBudget = 0) {
  const messages = [];
  const fileOps = createFileOps();
  let totalTokens = 0;
  for (const entry of entries) {
    if (entry.type === "branch_summary" && !entry.fromHook && entry.details) {
      const details = entry.details;
      if (Array.isArray(details.readFiles)) {
        for (const f of details.readFiles)
          fileOps.read.add(f);
      }
      if (Array.isArray(details.modifiedFiles)) {
        for (const f of details.modifiedFiles) {
          fileOps.edited.add(f);
        }
      }
    }
  }
  for (let i = entries.length - 1; i >= 0; i--) {
    const entry = entries[i];
    const message = getMessageFromEntry(entry);
    if (!message)
      continue;
    extractFileOpsFromMessage(message, fileOps);
    const tokens = estimateTokens(message);
    if (tokenBudget > 0 && totalTokens + tokens > tokenBudget) {
      if (entry.type === "compaction" || entry.type === "branch_summary") {
        if (totalTokens < tokenBudget * 0.9) {
          messages.unshift(message);
          totalTokens += tokens;
        }
      }
      break;
    }
    messages.unshift(message);
    totalTokens += tokens;
  }
  return { messages, fileOps, totalTokens };
}
__name(prepareBranchEntries, "prepareBranchEntries");
var BRANCH_SUMMARY_PREAMBLE = `The user explored a different conversation branch before returning here.
Summary of that exploration:

`;
var BRANCH_SUMMARY_PROMPT = `Create a structured summary of this conversation branch for context when returning later.

Use this EXACT format:

## Goal
[What was the user trying to accomplish in this branch?]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Work that was started but not finished]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [What should happen next to continue this work]

Keep each section concise. Preserve exact file paths, function names, and error messages.`;
async function generateBranchSummary(entries, options) {
  const { model, apiKey, headers, env, signal, customInstructions, replaceInstructions, reserveTokens = 16384, streamFn, retry, callbacks } = options;
  const contextWindow = model.contextWindow || 128e3;
  const tokenBudget = contextWindow - reserveTokens;
  const { messages, fileOps } = prepareBranchEntries(entries, tokenBudget);
  if (messages.length === 0) {
    return { summary: "No content to summarize" };
  }
  const llmMessages = convertToLlm(messages);
  const conversationText = serializeConversation(llmMessages);
  let instructions;
  if (replaceInstructions && customInstructions) {
    instructions = customInstructions;
  } else if (customInstructions) {
    instructions = `${BRANCH_SUMMARY_PROMPT}

Additional focus: ${customInstructions}`;
  } else {
    instructions = BRANCH_SUMMARY_PROMPT;
  }
  const promptText = `<conversation>
${conversationText}
</conversation>

${instructions}`;
  const summarizationMessages = [
    {
      role: "user",
      content: [{ type: "text", text: promptText }],
      timestamp: Date.now()
    }
  ];
  const maxTokens = Math.min(4096, model.maxTokens > 0 ? model.maxTokens : Number.POSITIVE_INFINITY);
  const context = normalizeContext2({ systemPrompt: SUMMARIZATION_SYSTEM_PROMPT, messages: summarizationMessages });
  const requestOptions = { apiKey, headers, env, signal, maxTokens };
  const response = await completeSummarization(model, context, requestOptions, streamFn, retry, callbacks);
  if (response.stopReason === "aborted") {
    return { aborted: true };
  }
  const failure = getSummarizationFailure(response, "Branch summarization");
  if (failure) {
    return { error: failure };
  }
  if (response.content.some((block) => block.type === "toolCall")) {
    return { error: "Branch summarization attempted to call a tool" };
  }
  let summary = contentText3(response.content);
  summary = BRANCH_SUMMARY_PREAMBLE + summary;
  const { readFiles, modifiedFiles } = computeFileLists(fileOps);
  summary += formatFileOperations(readFiles, modifiedFiles);
  return {
    summary: summary || "No summary generated",
    usage: response.usage,
    readFiles,
    modifiedFiles
  };
}
__name(generateBranchSummary, "generateBranchSummary");

// pi-dist/pi-coding-agent/core/defaults.js
var DEFAULT_THINKING_LEVEL = "medium";
var THINKING_LEVEL_OPTIONS = [
  "off",
  "minimal",
  "low",
  "medium",
  "high",
  "xhigh",
  "max"
];

// pi-dist/pi-coding-agent/core/event-bus.js
import { EventEmitter } from "node:events";
function createEventBus() {
  const emitter = new EventEmitter();
  return {
    emit: /* @__PURE__ */ __name((channel, data) => {
      emitter.emit(channel, data);
    }, "emit"),
    on: /* @__PURE__ */ __name((channel, handler) => {
      const safeHandler = /* @__PURE__ */ __name(async (data) => {
        try {
          await handler(data);
        } catch (err) {
          console.error(`Event handler error (${channel}):`, err);
        }
      }, "safeHandler");
      emitter.on(channel, safeHandler);
      return () => emitter.off(channel, safeHandler);
    }, "on"),
    clear: /* @__PURE__ */ __name(() => {
      emitter.removeAllListeners();
    }, "clear")
  };
}
__name(createEventBus, "createEventBus");

// pi-dist/pi-coding-agent/core/source-info.js
function createSourceInfo(path2, metadata) {
  return {
    path: path2,
    source: metadata.source,
    scope: metadata.scope,
    origin: metadata.origin,
    baseDir: metadata.baseDir
  };
}
__name(createSourceInfo, "createSourceInfo");
function createSyntheticSourceInfo(path2, options) {
  return {
    path: path2,
    source: options.source,
    scope: options.scope ?? "temporary",
    origin: options.origin ?? "top-level",
    baseDir: options.baseDir
  };
}
__name(createSyntheticSourceInfo, "createSyntheticSourceInfo");

// pi-dist/pi-coding-agent/core/timings.js
var ENABLED = process.env.PI_TIMING === "1";
var timingNamespaces = /* @__PURE__ */ new Map();
function resetTimings(namespace = "main") {
  if (!ENABLED)
    return;
  timingNamespaces.set(namespace, { timings: [], lastTime: Date.now() });
}
__name(resetTimings, "resetTimings");
function time(label, namespace = "main") {
  if (!ENABLED)
    return;
  const now = Date.now();
  if (!timingNamespaces.has(namespace)) {
    resetTimings(namespace);
  }
  const timingNamespace = timingNamespaces.get(namespace);
  timingNamespace.timings.push({ label, ms: now - timingNamespace.lastTime });
  timingNamespace.lastTime = now;
}
__name(time, "time");
function printTimingGroup(title, timings) {
  const printableTimings = timings.filter((timing) => timing.ms >= 0);
  if (printableTimings.length === 0)
    return;
  console.error(`
--- ${title} ---`);
  for (const t of printableTimings) {
    console.error(`  ${t.label}: ${t.ms}ms`);
  }
  console.error(`  TOTAL: ${printableTimings.reduce((a, b) => a + b.ms, 0)}ms`);
  console.error(`${"-".repeat(title.length + 8)}
`);
}
__name(printTimingGroup, "printTimingGroup");
function printTimings() {
  if (!ENABLED)
    return;
  for (const [namespace, timingNamespace] of timingNamespaces) {
    printTimingGroup(`Startup Timings: ${namespace}`, timingNamespace.timings);
  }
}
__name(printTimings, "printTimings");

// pi-dist/pi-coding-agent/core/extensions/loader.js
import * as fs from "node:fs";
import { createRequire } from "node:module";
import * as path from "node:path";
import { fileURLToPath } from "node:url";

// pi-dist/pi-coding-agent/core/exec.js
import { spawn } from "node:child_process";
async function execCommand(command, args, cwd, options) {
  return new Promise((resolve9) => {
    const proc = spawn(command, args, {
      cwd,
      shell: false,
      stdio: ["ignore", "pipe", "pipe"]
    });
    let stdout = "";
    let stderr = "";
    let killed = false;
    let timeoutId;
    const killProcess = /* @__PURE__ */ __name(() => {
      if (!killed) {
        killed = true;
        proc.kill("SIGTERM");
        setTimeout(() => {
          if (!proc.killed) {
            proc.kill("SIGKILL");
          }
        }, 5e3);
      }
    }, "killProcess");
    if (options?.signal) {
      if (options.signal.aborted) {
        killProcess();
      } else {
        options.signal.addEventListener("abort", killProcess, { once: true });
      }
    }
    if (options?.timeout && options.timeout > 0) {
      timeoutId = setTimeout(() => {
        killProcess();
      }, options.timeout);
    }
    proc.stdout?.on("data", (data) => {
      stdout += data.toString();
    });
    proc.stderr?.on("data", (data) => {
      stderr += data.toString();
    });
    waitForChildProcess(proc).then((code) => {
      if (timeoutId)
        clearTimeout(timeoutId);
      if (options?.signal) {
        options.signal.removeEventListener("abort", killProcess);
      }
      resolve9({ stdout, stderr, code: code ?? 0, killed });
    }).catch((_err) => {
      if (timeoutId)
        clearTimeout(timeoutId);
      if (options?.signal) {
        options.signal.removeEventListener("abort", killProcess);
      }
      resolve9({ stdout, stderr, code: 1, killed });
    });
  });
}
__name(execCommand, "execCommand");

// pi-dist/pi-coding-agent/core/pi-manifest.js
import { readFileSync } from "node:fs";
var RESOURCE_FIELDS = ["extensions", "skills", "prompts", "themes"];
function isObject(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
__name(isObject, "isObject");
function readPiManifest(packageJsonPath) {
  try {
    const pkg = JSON.parse(stripBom(readFileSync(packageJsonPath, "utf-8")));
    if (!isObject(pkg) || !isObject(pkg.pi)) {
      return null;
    }
    const manifest = {};
    for (const field of RESOURCE_FIELDS) {
      const entries = pkg.pi[field];
      if (Array.isArray(entries) && entries.every((entry) => typeof entry === "string")) {
        manifest[field] = entries;
      }
    }
    return manifest;
  } catch {
    return null;
  }
}
__name(readPiManifest, "readPiManifest");

// pi-dist/pi-coding-agent/core/extensions/loader.js
var require2 = createRequire(new URL("../core/extensions/loader.js", import.meta.url).href);
var isNodeSeaBinary = "sea" in process.features && process.features.sea === true || process.getBuiltinModule("node:sea")?.isSea() === true;
var isTypeScriptSourceRuntime = !isBunBinary && path.extname(fileURLToPath(new URL("../core/extensions/loader.js", import.meta.url).href)) === ".ts";
var usesEmbeddedModules = isBunBinary || isNodeSeaBinary || isBundledNode;
var createJitiPromise;
function getCreateJiti() {
  createJitiPromise ??= (usesEmbeddedModules ? import("./chunk-YU4WW7EQ.js") : import("./chunk-2RLEIHIL.js")).then((module) => module.createJiti);
  return createJitiPromise;
}
__name(getCreateJiti, "getCreateJiti");
var virtualModulesPromise;
function getVirtualModules() {
  virtualModulesPromise ??= import("./chunk-G2FVTWRH.js").then((module) => module.VIRTUAL_MODULES);
  return virtualModulesPromise;
}
__name(getVirtualModules, "getVirtualModules");
var _aliases = null;
function getAliases() {
  if (_aliases)
    return _aliases;
  const __dirname = path.dirname(fileURLToPath(new URL("../core/extensions/loader.js", import.meta.url).href));
  const packageIndex = path.resolve(__dirname, "../..", "index.js");
  const typeboxEntry = require2.resolve("typebox");
  const typeboxCompileEntry = require2.resolve("typebox/compile");
  const typeboxValueEntry = require2.resolve("typebox/value");
  const packagesRoot = path.resolve(__dirname, "../../../../");
  const resolveWorkspaceOrImport = /* @__PURE__ */ __name((workspaceRelativePath, specifier) => {
    const workspacePath = path.join(packagesRoot, workspaceRelativePath);
    if (fs.existsSync(workspacePath)) {
      return workspacePath;
    }
    return fileURLToPath(import.meta.resolve(specifier));
  }, "resolveWorkspaceOrImport");
  const piCodingAgentEntry = packageIndex;
  const piAgentCoreEntry = resolveWorkspaceOrImport("agent/dist/index.js", "@earendil-works/pi-agent-core");
  const piTuiEntry = resolveWorkspaceOrImport("tui/dist/index.js", "@earendil-works/pi-tui");
  const piAiCompatEntry = resolveWorkspaceOrImport("ai/dist/compat.js", "@earendil-works/pi-ai/compat");
  const piAiOauthEntry = resolveWorkspaceOrImport("ai/dist/oauth.js", "@earendil-works/pi-ai/oauth");
  const piAiProvidersEntry = resolveWorkspaceOrImport("ai/dist/providers/all.js", "@earendil-works/pi-ai/providers/all");
  _aliases = {
    "@earendil-works/pi-coding-agent": piCodingAgentEntry,
    "@earendil-works/pi-agent-core": piAgentCoreEntry,
    "@earendil-works/pi-tui": piTuiEntry,
    "@earendil-works/pi-ai/providers/all": piAiProvidersEntry,
    "@earendil-works/pi-ai/compat": piAiCompatEntry,
    "@earendil-works/pi-ai/oauth": piAiOauthEntry,
    "@earendil-works/pi-ai": piAiCompatEntry,
    "@mariozechner/pi-coding-agent": piCodingAgentEntry,
    "@mariozechner/pi-agent-core": piAgentCoreEntry,
    "@mariozechner/pi-tui": piTuiEntry,
    "@mariozechner/pi-ai/providers/all": piAiProvidersEntry,
    "@mariozechner/pi-ai/compat": piAiCompatEntry,
    "@mariozechner/pi-ai/oauth": piAiOauthEntry,
    "@mariozechner/pi-ai": piAiCompatEntry,
    typebox: typeboxEntry,
    "typebox/compile": typeboxCompileEntry,
    "typebox/value": typeboxValueEntry,
    "@sinclair/typebox": typeboxEntry,
    "@sinclair/typebox/compile": typeboxCompileEntry,
    "@sinclair/typebox/value": typeboxValueEntry
  };
  return _aliases;
}
__name(getAliases, "getAliases");
var extensionCacheCwd;
var extensionCacheGeneration = 0;
var extensionCache = /* @__PURE__ */ new Map();
function clearExtensionCache() {
  extensionCache.clear();
  extensionCacheCwd = void 0;
  extensionCacheGeneration++;
}
__name(clearExtensionCache, "clearExtensionCache");
function useExtensionCacheCwd(cwd) {
  const resolvedCwd = resolvePath(cwd);
  if (extensionCacheCwd !== void 0 && extensionCacheCwd !== resolvedCwd) {
    clearExtensionCache();
  }
  extensionCacheCwd = resolvedCwd;
  return { cwd: resolvedCwd, generation: extensionCacheGeneration };
}
__name(useExtensionCacheCwd, "useExtensionCacheCwd");
function createExtensionRuntime() {
  const notInitialized = /* @__PURE__ */ __name(() => {
    throw new Error("Extension runtime not initialized. Action methods cannot be called during extension loading.");
  }, "notInitialized");
  const state = {};
  const eventBusUnsubscribers = /* @__PURE__ */ new Set();
  const assertActive = /* @__PURE__ */ __name(() => {
    if (state.staleMessage) {
      throw new Error(state.staleMessage);
    }
  }, "assertActive");
  const runtime = {
    sendMessage: notInitialized,
    sendUserMessage: notInitialized,
    appendEntry: notInitialized,
    setSessionName: notInitialized,
    getSessionName: notInitialized,
    setLabel: notInitialized,
    getActiveTools: notInitialized,
    getAllTools: notInitialized,
    setActiveTools: notInitialized,
    // registerTool() is valid during extension load; refresh is only needed post-bind.
    refreshTools: /* @__PURE__ */ __name(() => {
    }, "refreshTools"),
    getCommands: notInitialized,
    setModel: /* @__PURE__ */ __name(() => Promise.reject(new Error("Extension runtime not initialized")), "setModel"),
    getThinkingLevel: notInitialized,
    setThinkingLevel: notInitialized,
    flagValues: /* @__PURE__ */ new Map(),
    pendingProviderRegistrations: [],
    pendingNativeProviderRegistrations: [],
    assertActive,
    invalidate: /* @__PURE__ */ __name((message) => {
      if (state.staleMessage)
        return;
      state.staleMessage = message ?? "This extension ctx is stale after session replacement or reload. Do not use a captured pi or command ctx after ctx.newSession(), ctx.fork(), ctx.switchSession(), or ctx.reload(). For newSession, fork, and switchSession, move post-replacement work into withSession and use the ctx passed to withSession. For reload, do not use the old ctx after await ctx.reload().";
      for (const unsubscribe of eventBusUnsubscribers)
        unsubscribe();
      eventBusUnsubscribers.clear();
    }, "invalidate"),
    trackEventBusSubscription: /* @__PURE__ */ __name((unsubscribe) => {
      let active = true;
      const trackedUnsubscribe = /* @__PURE__ */ __name(() => {
        if (!active)
          return;
        active = false;
        eventBusUnsubscribers.delete(trackedUnsubscribe);
        unsubscribe();
      }, "trackedUnsubscribe");
      eventBusUnsubscribers.add(trackedUnsubscribe);
      return trackedUnsubscribe;
    }, "trackEventBusSubscription"),
    // Pre-bind: queue registrations so bindCore() can flush them once the
    // model registry is available. bindCore() replaces both with direct calls.
    registerProvider: /* @__PURE__ */ __name((name, config, extensionPath = "<unknown>") => {
      runtime.pendingProviderRegistrations.push({ name, config, extensionPath });
    }, "registerProvider"),
    registerNativeProvider: /* @__PURE__ */ __name((provider, extensionPath = "<unknown>") => {
      runtime.pendingNativeProviderRegistrations.push({ provider, extensionPath });
    }, "registerNativeProvider"),
    unregisterProvider: /* @__PURE__ */ __name((name) => {
      runtime.pendingProviderRegistrations = runtime.pendingProviderRegistrations.filter((r) => r.name !== name);
      runtime.pendingNativeProviderRegistrations = runtime.pendingNativeProviderRegistrations.filter((r) => r.provider.id !== name);
    }, "unregisterProvider")
  };
  return runtime;
}
__name(createExtensionRuntime, "createExtensionRuntime");
function createExtensionAPI(extension, runtime, cwd, eventBus) {
  const pendingFlagValues = /* @__PURE__ */ new Map();
  const pendingRuntimeChanges = [];
  const loadingUnsubscribers = [];
  let state = "loading";
  const assertActive = /* @__PURE__ */ __name(() => {
    if (state === "failed") {
      throw new Error(`Extension "${extension.path}" failed to load and its API is no longer active.`);
    }
    runtime.assertActive();
  }, "assertActive");
  const applyRuntimeChange = /* @__PURE__ */ __name((change) => {
    if (state === "loading")
      pendingRuntimeChanges.push(change);
    else
      change();
  }, "applyRuntimeChange");
  const clearPending = /* @__PURE__ */ __name(() => {
    pendingFlagValues.clear();
    pendingRuntimeChanges.length = 0;
    loadingUnsubscribers.length = 0;
  }, "clearPending");
  const api = {
    // Registration methods - write to extension
    on(event, handler) {
      assertActive();
      const registeredHandler = /* @__PURE__ */ __name((...args) => handler(...args), "registeredHandler");
      const list = extension.handlers.get(event) ?? [];
      list.push(registeredHandler);
      extension.handlers.set(event, list);
      return () => {
        const handlers = extension.handlers.get(event);
        if (!handlers)
          return;
        const handlerIndex = handlers.indexOf(registeredHandler);
        if (handlerIndex === -1)
          return;
        handlers.splice(handlerIndex, 1);
        if (handlers.length === 0)
          extension.handlers.delete(event);
      };
    },
    registerTool(tool) {
      assertActive();
      if (typeof tool.parameters !== "object" || tool.parameters === null || Array.isArray(tool.parameters)) {
        throw new Error(`Tool "${tool.name}" registered by extension "${extension.path}" must define an object parameter schema.`);
      }
      extension.tools.set(tool.name, {
        definition: tool,
        sourceInfo: extension.sourceInfo
      });
      runtime.refreshTools();
    },
    registerCommand(name, options) {
      assertActive();
      extension.commands.set(name, {
        name,
        sourceInfo: extension.sourceInfo,
        ...options
      });
    },
    registerShortcut(shortcut, options) {
      assertActive();
      extension.shortcuts.set(shortcut, { shortcut, extensionPath: extension.path, ...options });
    },
    registerFlag(name, options) {
      assertActive();
      if (options.default !== void 0 && typeof options.default !== options.type) {
        throw new Error(`Invalid default for flag "${name}": expected ${options.type}, got ${typeof options.default}`);
      }
      extension.flags.set(name, { name, extensionPath: extension.path, ...options });
      if (options.default !== void 0 && !runtime.flagValues.has(name)) {
        if (state === "loading") {
          if (!pendingFlagValues.has(name))
            pendingFlagValues.set(name, options.default);
        } else {
          runtime.flagValues.set(name, options.default);
        }
      }
    },
    registerMessageRenderer(customType, renderer) {
      assertActive();
      extension.messageRenderers.set(customType, renderer);
    },
    registerMarkdownTransformer(transformer) {
      assertActive();
      extension.markdownTransformer = transformer;
    },
    registerEntryRenderer(customType, renderer) {
      assertActive();
      extension.entryRenderers ??= /* @__PURE__ */ new Map();
      extension.entryRenderers.set(customType, renderer);
    },
    // Flag access - checks extension registered it, reads from runtime
    getFlag(name) {
      assertActive();
      if (!extension.flags.has(name))
        return void 0;
      return runtime.flagValues.has(name) ? runtime.flagValues.get(name) : pendingFlagValues.get(name);
    },
    // Action methods - delegate to shared runtime
    sendMessage(message, options) {
      assertActive();
      runtime.sendMessage(message, options);
    },
    sendUserMessage(content, options) {
      assertActive();
      runtime.sendUserMessage(content, options);
    },
    appendEntry(customType, data) {
      assertActive();
      runtime.appendEntry(customType, data);
    },
    setSessionName(name) {
      assertActive();
      runtime.setSessionName(name);
    },
    getSessionName() {
      assertActive();
      return runtime.getSessionName();
    },
    setLabel(entryId, label) {
      assertActive();
      runtime.setLabel(entryId, label);
    },
    exec(command, args, options) {
      assertActive();
      return execCommand(command, args, options?.cwd ?? cwd, options);
    },
    getActiveTools() {
      assertActive();
      return runtime.getActiveTools();
    },
    getAllTools() {
      assertActive();
      return runtime.getAllTools();
    },
    setActiveTools(toolNames) {
      assertActive();
      runtime.setActiveTools(toolNames);
    },
    getCommands() {
      assertActive();
      return runtime.getCommands();
    },
    setModel(model) {
      assertActive();
      return runtime.setModel(model);
    },
    getThinkingLevel() {
      assertActive();
      return runtime.getThinkingLevel();
    },
    setThinkingLevel(level) {
      assertActive();
      runtime.setThinkingLevel(level);
    },
    registerProvider(providerOrName, config) {
      assertActive();
      if (typeof providerOrName === "string") {
        if (!config)
          throw new Error("Provider config is required when registering by name");
        applyRuntimeChange(() => runtime.registerProvider(providerOrName, config, extension.path));
        return;
      }
      applyRuntimeChange(() => runtime.registerNativeProvider(providerOrName, extension.path));
    },
    unregisterProvider(name) {
      assertActive();
      applyRuntimeChange(() => runtime.unregisterProvider(name, extension.path));
    },
    events: {
      emit(channel, data) {
        assertActive();
        eventBus.emit(channel, data);
      },
      on(channel, handler) {
        assertActive();
        const unsubscribe = runtime.trackEventBusSubscription(eventBus.on(channel, handler));
        if (state === "loading")
          loadingUnsubscribers.push(unsubscribe);
        return unsubscribe;
      }
    }
  };
  return {
    api,
    commit: /* @__PURE__ */ __name(() => {
      if (state !== "loading")
        return;
      runtime.assertActive();
      for (const [name, value] of pendingFlagValues) {
        if (!runtime.flagValues.has(name))
          runtime.flagValues.set(name, value);
      }
      for (const apply of pendingRuntimeChanges)
        apply();
      state = "active";
      clearPending();
    }, "commit"),
    discard: /* @__PURE__ */ __name(() => {
      if (state !== "loading")
        return;
      state = "failed";
      for (const unsubscribe of loadingUnsubscribers)
        unsubscribe();
      clearPending();
    }, "discard")
  };
}
__name(createExtensionAPI, "createExtensionAPI");
function isCurrentCacheToken(cacheToken) {
  return cacheToken !== void 0 && extensionCacheCwd === cacheToken.cwd && extensionCacheGeneration === cacheToken.generation;
}
__name(isCurrentCacheToken, "isCurrentCacheToken");
async function loadExtensionModule(extensionPath, cacheToken) {
  if (isCurrentCacheToken(cacheToken)) {
    const cachedFactory = extensionCache.get(extensionPath);
    if (cachedFactory) {
      return cachedFactory;
    }
  }
  const createJitiImpl = await getCreateJiti();
  const resolutionOptions = usesEmbeddedModules ? { virtualModules: await getVirtualModules(), tryNative: false } : isTypeScriptSourceRuntime ? { virtualModules: await getVirtualModules(), tsconfigPaths: true } : { alias: getAliases() };
  const jiti = createJitiImpl(new URL("../core/extensions/loader.js", import.meta.url).href, {
    moduleCache: false,
    ...resolutionOptions
  });
  const module = await jiti.import(extensionPath, { default: true });
  const factory = module;
  if (typeof factory !== "function") {
    return void 0;
  }
  if (isCurrentCacheToken(cacheToken)) {
    extensionCache.set(extensionPath, factory);
  }
  return factory;
}
__name(loadExtensionModule, "loadExtensionModule");
function createExtension(extensionPath, resolvedPath) {
  const source = extensionPath.startsWith("<") && extensionPath.endsWith(">") ? extensionPath.slice(1, -1).split(":")[0] || "temporary" : "local";
  const baseDir = extensionPath.startsWith("<") ? void 0 : path.dirname(resolvedPath);
  return {
    path: extensionPath,
    resolvedPath,
    sourceInfo: createSyntheticSourceInfo(extensionPath, { source, baseDir }),
    handlers: /* @__PURE__ */ new Map(),
    tools: /* @__PURE__ */ new Map(),
    messageRenderers: /* @__PURE__ */ new Map(),
    entryRenderers: /* @__PURE__ */ new Map(),
    commands: /* @__PURE__ */ new Map(),
    flags: /* @__PURE__ */ new Map(),
    shortcuts: /* @__PURE__ */ new Map()
  };
}
__name(createExtension, "createExtension");
async function initializeExtension(factory, extensionPath, resolvedPath, cwd, eventBus, runtime) {
  const extension = createExtension(extensionPath, resolvedPath);
  const load = createExtensionAPI(extension, runtime, cwd, eventBus);
  try {
    await factory(load.api);
    load.commit();
  } catch (error) {
    load.discard();
    throw error;
  }
  time(`${extensionPath} factory`, "extensions");
  return extension;
}
__name(initializeExtension, "initializeExtension");
async function loadExtension(extensionPath, cwd, eventBus, runtime, cacheToken) {
  const resolvedPath = resolvePath(extensionPath, cwd, { normalizeUnicodeSpaces: true });
  try {
    const factory = await loadExtensionModule(resolvedPath, cacheToken);
    time(`${extensionPath} module import`, "extensions");
    if (!factory) {
      return { extension: null, error: `Extension does not export a valid factory function: ${extensionPath}` };
    }
    const extension = await initializeExtension(factory, extensionPath, resolvedPath, cwd, eventBus, runtime);
    return { extension, error: null };
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    return { extension: null, error: `Failed to load extension: ${message}` };
  }
}
__name(loadExtension, "loadExtension");
async function loadExtensionFromFactory(factory, cwd, eventBus, runtime, extensionPath = "<inline>") {
  const resolvedCwd = resolvePath(cwd);
  return initializeExtension(factory, extensionPath, extensionPath, resolvedCwd, eventBus, runtime);
}
__name(loadExtensionFromFactory, "loadExtensionFromFactory");
async function loadExtensionsInternal(paths, cwd, eventBus, runtime, useCache = false) {
  const extensions = [];
  const errors = [];
  const cacheToken = useCache ? useExtensionCacheCwd(cwd) : void 0;
  const resolvedCwd = cacheToken?.cwd ?? resolvePath(cwd);
  const resolvedEventBus = eventBus ?? createEventBus();
  const resolvedRuntime = runtime ?? createExtensionRuntime();
  for (const extPath of paths) {
    const { extension, error } = await loadExtension(extPath, resolvedCwd, resolvedEventBus, resolvedRuntime, cacheToken);
    if (error) {
      errors.push({ path: extPath, error });
      continue;
    }
    if (extension) {
      extensions.push(extension);
    }
  }
  return {
    extensions,
    errors,
    runtime: resolvedRuntime
  };
}
__name(loadExtensionsInternal, "loadExtensionsInternal");
async function loadExtensions(paths, cwd, eventBus, runtime) {
  return loadExtensionsInternal(paths, cwd, eventBus, runtime);
}
__name(loadExtensions, "loadExtensions");
async function loadExtensionsCached(paths, cwd, eventBus, runtime) {
  return loadExtensionsInternal(paths, cwd, eventBus, runtime, true);
}
__name(loadExtensionsCached, "loadExtensionsCached");
function isExtensionFile(name) {
  return name.endsWith(".ts") || name.endsWith(".js");
}
__name(isExtensionFile, "isExtensionFile");
function resolveExtensionEntries(dir) {
  const packageJsonPath = path.join(dir, "package.json");
  if (fs.existsSync(packageJsonPath)) {
    const manifest = readPiManifest(packageJsonPath);
    if (manifest?.extensions?.length) {
      const entries = [];
      for (const extPath of manifest.extensions) {
        const resolvedExtPath = path.resolve(dir, extPath);
        if (fs.existsSync(resolvedExtPath)) {
          entries.push(resolvedExtPath);
        }
      }
      if (entries.length > 0) {
        return entries;
      }
    }
  }
  const indexTs = path.join(dir, "index.ts");
  const indexJs = path.join(dir, "index.js");
  if (fs.existsSync(indexTs)) {
    return [indexTs];
  }
  if (fs.existsSync(indexJs)) {
    return [indexJs];
  }
  return null;
}
__name(resolveExtensionEntries, "resolveExtensionEntries");
function discoverExtensionsInDir(dir) {
  if (!fs.existsSync(dir)) {
    return [];
  }
  const discovered = [];
  try {
    const entries = fs.readdirSync(dir, { withFileTypes: true });
    for (const entry of entries) {
      const entryPath = path.join(dir, entry.name);
      if ((entry.isFile() || entry.isSymbolicLink()) && isExtensionFile(entry.name)) {
        discovered.push(entryPath);
        continue;
      }
      if (entry.isDirectory() || entry.isSymbolicLink()) {
        const entries2 = resolveExtensionEntries(entryPath);
        if (entries2) {
          discovered.push(...entries2);
        }
      }
    }
  } catch {
    return [];
  }
  return discovered;
}
__name(discoverExtensionsInDir, "discoverExtensionsInDir");
async function discoverAndLoadExtensions(configuredPaths, cwd, agentDir = getAgentDir(), eventBus) {
  const resolvedCwd = resolvePath(cwd);
  const resolvedAgentDir = resolvePath(agentDir);
  const allPaths = [];
  const seen = /* @__PURE__ */ new Set();
  const addPaths = /* @__PURE__ */ __name((paths) => {
    for (const p of paths) {
      const resolved = path.resolve(p);
      if (!seen.has(resolved)) {
        seen.add(resolved);
        allPaths.push(p);
      }
    }
  }, "addPaths");
  const localExtDir = path.join(resolvedCwd, CONFIG_DIR_NAME, "extensions");
  addPaths(discoverExtensionsInDir(localExtDir));
  const globalExtDir = path.join(resolvedAgentDir, "extensions");
  addPaths(discoverExtensionsInDir(globalExtDir));
  for (const p of configuredPaths) {
    const resolved = resolvePath(p, resolvedCwd, { normalizeUnicodeSpaces: true });
    if (fs.existsSync(resolved) && fs.statSync(resolved).isDirectory()) {
      const entries = resolveExtensionEntries(resolved);
      if (entries) {
        addPaths(entries);
        continue;
      }
      addPaths(discoverExtensionsInDir(resolved));
      continue;
    }
    addPaths([resolved]);
  }
  return loadExtensions(allPaths, resolvedCwd, eventBus);
}
__name(discoverAndLoadExtensions, "discoverAndLoadExtensions");

// pi-dist/pi-coding-agent/core/skills.js
import { existsSync as existsSync3, readdirSync as readdirSync3, readFileSync as readFileSync2, statSync as statSync3 } from "fs";
import ignore from "../../../ignore/index.js";
import { basename as basename2, dirname as dirname2, join as join4, relative, resolve as resolve3, sep } from "path";
var MAX_NAME_LENGTH = 64;
var MAX_DESCRIPTION_LENGTH = 1024;
var IGNORE_FILE_NAMES = [".gitignore", ".ignore", ".fdignore"];
function toPosixPath(p) {
  return p.split(sep).join("/");
}
__name(toPosixPath, "toPosixPath");
function prefixIgnorePattern(line, prefix) {
  const trimmed = line.trim();
  if (!trimmed)
    return null;
  if (trimmed.startsWith("#") && !trimmed.startsWith("\\#"))
    return null;
  let pattern = line;
  let negated = false;
  if (pattern.startsWith("!")) {
    negated = true;
    pattern = pattern.slice(1);
  } else if (pattern.startsWith("\\!")) {
    pattern = pattern.slice(1);
  }
  if (pattern.startsWith("/")) {
    pattern = pattern.slice(1);
  }
  const prefixed = prefix ? `${prefix}${pattern}` : pattern;
  return negated ? `!${prefixed}` : prefixed;
}
__name(prefixIgnorePattern, "prefixIgnorePattern");
function addIgnoreRules(ig, dir, rootDir) {
  const relativeDir = relative(rootDir, dir);
  const prefix = relativeDir ? `${toPosixPath(relativeDir)}/` : "";
  for (const filename of IGNORE_FILE_NAMES) {
    const ignorePath = join4(dir, filename);
    if (!existsSync3(ignorePath))
      continue;
    try {
      const content = readFileSync2(ignorePath, "utf-8");
      const patterns = content.split(/\r?\n/).map((line) => prefixIgnorePattern(line, prefix)).filter((line) => Boolean(line));
      if (patterns.length > 0) {
        ig.add(patterns);
      }
    } catch {
    }
  }
}
__name(addIgnoreRules, "addIgnoreRules");
function validateName(name) {
  const errors = [];
  if (name.length > MAX_NAME_LENGTH) {
    errors.push(`name exceeds ${MAX_NAME_LENGTH} characters (${name.length})`);
  }
  if (!/^[a-z0-9-]+$/.test(name)) {
    errors.push(`name contains invalid characters (must be lowercase a-z, 0-9, hyphens only)`);
  }
  if (name.startsWith("-") || name.endsWith("-")) {
    errors.push(`name must not start or end with a hyphen`);
  }
  if (name.includes("--")) {
    errors.push(`name must not contain consecutive hyphens`);
  }
  return errors;
}
__name(validateName, "validateName");
function validateDescription(description) {
  const errors = [];
  if (typeof description !== "string" || description.trim() === "") {
    errors.push("description is required");
  } else if (description.length > MAX_DESCRIPTION_LENGTH) {
    errors.push(`description exceeds ${MAX_DESCRIPTION_LENGTH} characters (${description.length})`);
  }
  return errors;
}
__name(validateDescription, "validateDescription");
function createSkillSourceInfo(filePath, baseDir, source) {
  switch (source) {
    case "user":
      return createSyntheticSourceInfo(filePath, {
        source: "local",
        scope: "user",
        baseDir
      });
    case "project":
      return createSyntheticSourceInfo(filePath, {
        source: "local",
        scope: "project",
        baseDir
      });
    case "path":
      return createSyntheticSourceInfo(filePath, {
        source: "local",
        baseDir
      });
    default:
      return createSyntheticSourceInfo(filePath, { source, baseDir });
  }
}
__name(createSkillSourceInfo, "createSkillSourceInfo");
function loadSkillsFromDir(options) {
  const { dir, source } = options;
  return loadSkillsFromDirInternal(dir, source, true);
}
__name(loadSkillsFromDir, "loadSkillsFromDir");
function loadSkillsFromDirInternal(dir, source, includeRootFiles, ignoreMatcher, rootDir) {
  const skills = [];
  const diagnostics = [];
  if (!existsSync3(dir)) {
    return { skills, diagnostics };
  }
  const root = rootDir ?? dir;
  const ig = ignoreMatcher ?? ignore();
  addIgnoreRules(ig, dir, root);
  try {
    const entries = readdirSync3(dir, { withFileTypes: true });
    for (const entry of entries) {
      if (entry.name !== "SKILL.md") {
        continue;
      }
      const fullPath = join4(dir, entry.name);
      let isFile = entry.isFile();
      if (entry.isSymbolicLink()) {
        try {
          isFile = statSync3(fullPath).isFile();
        } catch {
          continue;
        }
      }
      const relPath = toPosixPath(relative(root, fullPath));
      if (!isFile || ig.ignores(relPath)) {
        continue;
      }
      const result = loadSkillFromFile(fullPath, source);
      if (result.skill) {
        skills.push(result.skill);
      }
      diagnostics.push(...result.diagnostics);
      return { skills, diagnostics };
    }
    for (const entry of entries) {
      if (entry.name.startsWith(".")) {
        continue;
      }
      if (entry.name === "node_modules") {
        continue;
      }
      const fullPath = join4(dir, entry.name);
      let isDirectory = entry.isDirectory();
      let isFile = entry.isFile();
      if (entry.isSymbolicLink()) {
        try {
          const stats = statSync3(fullPath);
          isDirectory = stats.isDirectory();
          isFile = stats.isFile();
        } catch {
          continue;
        }
      }
      const relPath = toPosixPath(relative(root, fullPath));
      const ignorePath = isDirectory ? `${relPath}/` : relPath;
      if (ig.ignores(ignorePath)) {
        continue;
      }
      if (isDirectory) {
        const subResult = loadSkillsFromDirInternal(fullPath, source, false, ig, root);
        skills.push(...subResult.skills);
        diagnostics.push(...subResult.diagnostics);
        continue;
      }
      if (!isFile || !includeRootFiles || !entry.name.endsWith(".md")) {
        continue;
      }
      const result = loadSkillFromFile(fullPath, source);
      if (result.skill) {
        skills.push(result.skill);
      }
      diagnostics.push(...result.diagnostics);
    }
  } catch {
  }
  return { skills, diagnostics };
}
__name(loadSkillsFromDirInternal, "loadSkillsFromDirInternal");
function loadSkillFromFile(filePath, source) {
  const diagnostics = [];
  const isDeclaredSkill = basename2(filePath) === "SKILL.md";
  let rawContent;
  try {
    rawContent = readFileSync2(filePath, "utf-8");
  } catch (error) {
    const message = error instanceof Error ? error.message : "failed to read skill file";
    diagnostics.push({ type: "warning", message, path: filePath });
    return { skill: null, diagnostics };
  }
  let frontmatter;
  try {
    ({ frontmatter } = parseFrontmatter(rawContent));
  } catch (error) {
    if (isDeclaredSkill) {
      const message = error instanceof Error ? error.message : "failed to parse skill file";
      diagnostics.push({ type: "warning", message, path: filePath });
    }
    return { skill: null, diagnostics };
  }
  const description = frontmatter.description;
  const hasDescription = typeof description === "string" && description.trim() !== "";
  if (!isDeclaredSkill && !hasDescription) {
    return { skill: null, diagnostics };
  }
  const skillDir = dirname2(filePath);
  const parentDirName = basename2(skillDir);
  const descErrors = validateDescription(description);
  for (const error of descErrors) {
    diagnostics.push({ type: "warning", message: error, path: filePath });
  }
  const frontmatterName = typeof frontmatter.name === "string" ? frontmatter.name : void 0;
  const name = frontmatterName || parentDirName;
  const nameErrors = validateName(name);
  for (const error of nameErrors) {
    diagnostics.push({ type: "warning", message: error, path: filePath });
  }
  if (!hasDescription) {
    return { skill: null, diagnostics };
  }
  return {
    skill: {
      name,
      description,
      filePath,
      baseDir: skillDir,
      sourceInfo: createSkillSourceInfo(filePath, skillDir, source),
      disableModelInvocation: frontmatter["disable-model-invocation"] === true
    },
    diagnostics
  };
}
__name(loadSkillFromFile, "loadSkillFromFile");
function formatSkillsForPrompt(skills, fileReadTool = "read") {
  const visibleSkills = skills.filter((s) => !s.disableModelInvocation);
  if (visibleSkills.length === 0) {
    return "";
  }
  const lines = [
    "\n\nThe following skills provide specialized instructions for specific tasks.",
    fileReadTool === "read" ? "Use the read tool to load a skill's file when the task matches its description." : "Use bash to load a skill's file when the task matches its description.",
    "When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.",
    "",
    "<available_skills>"
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
__name(formatSkillsForPrompt, "formatSkillsForPrompt");
function escapeXml(str) {
  return str.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&apos;");
}
__name(escapeXml, "escapeXml");
function loadSkills(options) {
  const { agentDir, skillPaths, includeDefaults } = options;
  const resolvedCwd = resolvePath(options.cwd);
  const resolvedAgentDir = resolvePath(agentDir ?? getAgentDir());
  const skillMap = /* @__PURE__ */ new Map();
  const realPathSet = /* @__PURE__ */ new Set();
  const allDiagnostics = [];
  const collisionDiagnostics = [];
  function addSkills(result) {
    allDiagnostics.push(...result.diagnostics);
    for (const skill of result.skills) {
      const realPath = canonicalizePath(skill.filePath);
      if (realPathSet.has(realPath)) {
        continue;
      }
      const existing = skillMap.get(skill.name);
      if (existing) {
        collisionDiagnostics.push({
          type: "collision",
          message: `name "${skill.name}" collision`,
          path: skill.filePath,
          collision: {
            resourceType: "skill",
            name: skill.name,
            winnerPath: existing.filePath,
            loserPath: skill.filePath
          }
        });
      } else {
        skillMap.set(skill.name, skill);
        realPathSet.add(realPath);
      }
    }
  }
  __name(addSkills, "addSkills");
  if (includeDefaults) {
    addSkills(loadSkillsFromDirInternal(join4(resolvedAgentDir, "skills"), "user", true));
    addSkills(loadSkillsFromDirInternal(resolve3(resolvedCwd, CONFIG_DIR_NAME, "skills"), "project", true));
  }
  const userSkillsDir = join4(resolvedAgentDir, "skills");
  const projectSkillsDir = resolve3(resolvedCwd, CONFIG_DIR_NAME, "skills");
  const isUnderPath = /* @__PURE__ */ __name((target, root) => {
    const normalizedRoot = resolve3(root);
    if (target === normalizedRoot) {
      return true;
    }
    const prefix = normalizedRoot.endsWith(sep) ? normalizedRoot : `${normalizedRoot}${sep}`;
    return target.startsWith(prefix);
  }, "isUnderPath");
  const getSource = /* @__PURE__ */ __name((resolvedPath) => {
    if (!includeDefaults) {
      if (isUnderPath(resolvedPath, userSkillsDir))
        return "user";
      if (isUnderPath(resolvedPath, projectSkillsDir))
        return "project";
    }
    return "path";
  }, "getSource");
  for (const rawPath of skillPaths) {
    const resolvedPath = resolvePath(rawPath, resolvedCwd, { trim: true });
    if (!existsSync3(resolvedPath)) {
      allDiagnostics.push({ type: "warning", message: "skill path does not exist", path: resolvedPath });
      continue;
    }
    try {
      const stats = statSync3(resolvedPath);
      const source = getSource(resolvedPath);
      if (stats.isDirectory()) {
        addSkills(loadSkillsFromDirInternal(resolvedPath, source, true));
      } else if (stats.isFile() && resolvedPath.endsWith(".md")) {
        const result = loadSkillFromFile(resolvedPath, source);
        if (result.skill) {
          addSkills({ skills: [result.skill], diagnostics: result.diagnostics });
        } else {
          allDiagnostics.push(...result.diagnostics);
        }
      } else {
        allDiagnostics.push({ type: "warning", message: "skill path is not a markdown file", path: resolvedPath });
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : "failed to read skill path";
      allDiagnostics.push({ type: "warning", message, path: resolvedPath });
    }
  }
  return {
    skills: Array.from(skillMap.values()),
    diagnostics: [...allDiagnostics, ...collisionDiagnostics]
  };
}
__name(loadSkills, "loadSkills");

// pi-dist/pi-coding-agent/core/extensions/runner.js
import { getCurrentSystemMessage as getCurrentSystemMessage3 } from "../../pi-ai/sdk-bundle/index.js";

// pi-dist/pi-coding-agent/core/system-prompt.js
import { getSystemMessageText } from "../../pi-ai/sdk-bundle/index.js";
var SYSTEM_PROMPT_SECTION_NAME = /^[a-z][a-z0-9_-]*$/;
function normalizeBuildSystemPromptOptions(input) {
  return {
    customPrompt: input.customPrompt,
    forceSystemPrompt: input.forceSystemPrompt,
    selectedTools: [...input.selectedTools ?? ["read", "bash", "edit", "write"]],
    toolSnippets: { ...input.toolSnippets ?? {} },
    toolGuidelines: Object.fromEntries(Object.entries(input.toolGuidelines ?? {}).map(([name, guidelines]) => [name, [...guidelines]])),
    promptGuidelines: [...input.promptGuidelines ?? []],
    appendSystemPrompt: input.appendSystemPrompt ?? "",
    sections: { ...input.sections ?? {} },
    cwd: input.cwd,
    contextFiles: (input.contextFiles ?? []).map((file) => ({ ...file })),
    skills: (input.skills ?? []).map((skill) => ({ ...skill }))
  };
}
__name(normalizeBuildSystemPromptOptions, "normalizeBuildSystemPromptOptions");
function renderProjectContext(contextFiles) {
  return [
    "Project-specific instructions and guidelines:",
    ...contextFiles.map(({ path: path2, content }) => `<project_instructions path="${path2}">
${content}
</project_instructions>`)
  ].join("\n\n");
}
__name(renderProjectContext, "renderProjectContext");
function buildRules(selectedTools, toolGuidelines, promptGuidelines) {
  const rules = [];
  const seen = /* @__PURE__ */ new Set();
  const addRule = /* @__PURE__ */ __name((rule) => {
    const normalized = rule.trim();
    if (!normalized || seen.has(normalized))
      return;
    seen.add(normalized);
    rules.push(normalized);
  }, "addRule");
  const hasBash = selectedTools.includes("bash");
  const hasPowerShell = selectedTools.includes("powershell");
  const hasGrep = selectedTools.includes("grep");
  const hasFind = selectedTools.includes("find");
  const hasLs = selectedTools.includes("ls");
  if ((hasBash || hasPowerShell) && !hasGrep && !hasFind && !hasLs) {
    if (hasBash && hasPowerShell) {
      addRule("Use bash or PowerShell for file operations like listing, searching, and finding files");
    } else if (hasPowerShell) {
      addRule("Use PowerShell for file operations like listing, searching, and finding files");
    } else {
      addRule("Use bash for file operations like ls, rg, find");
    }
  }
  for (const name of selectedTools) {
    for (const rule of toolGuidelines[name] ?? [])
      addRule(rule);
  }
  for (const rule of promptGuidelines)
    addRule(rule);
  addRule("Be concise in your responses");
  addRule("Show file paths clearly when working with files");
  return rules.map((rule) => `- ${rule}`).join("\n");
}
__name(buildRules, "buildRules");
function buildSystemPromptSections(input) {
  const options = normalizeBuildSystemPromptOptions(input);
  const { customPrompt, selectedTools, toolSnippets, toolGuidelines, promptGuidelines, appendSystemPrompt, sections: customSections, cwd, contextFiles, skills } = options;
  for (const name of Object.keys(customSections)) {
    if (!SYSTEM_PROMPT_SECTION_NAME.test(name) || name === "preamble") {
      throw new Error(`Invalid system prompt section name: ${name}`);
    }
  }
  const promptSections = {};
  if (customPrompt) {
    promptSections.preamble = customPrompt;
  } else {
    promptSections.preamble = "You are an expert coding assistant operating inside pi, a coding agent harness. You help users by reading files, executing commands, editing code, and writing new files.";
    const visibleTools = selectedTools.filter((name) => !!toolSnippets[name]);
    const tools = visibleTools.length > 0 ? visibleTools.map((name) => `- ${name}: ${toolSnippets[name]}`).join("\n") : "(none)";
    promptSections.tools = `${tools}

In addition to the tools above, you may have access to other custom tools depending on the project.`;
    promptSections.rules = buildRules(selectedTools, toolGuidelines, promptGuidelines);
    promptSections.docs = `Pi documentation (read only when the user asks about pi itself, its SDK, extensions, themes, skills, or TUI):
- Main documentation: ${getReadmePath()}
- Additional docs: ${getDocsPath()}
- Examples: ${getExamplesPath()} (extensions, custom tools, SDK)
- When reading pi docs or examples, resolve docs/... under Additional docs and examples/... under Examples, not the current working directory
- When asked about: extensions (docs/extensions.md, examples/extensions/), themes (docs/themes.md), skills (docs/skills.md), prompt templates (docs/prompt-templates.md), TUI components (docs/tui.md), keybindings (docs/keybindings.md), SDK integrations (docs/sdk.md), custom providers (docs/custom-provider.md), adding models (docs/models.md), pi packages (docs/packages.md), environment variables (docs/environment-variables.md)
- When working on pi topics, read the docs and examples, and follow .md cross-references before implementing
- Always read pi .md files completely and follow links to related docs (e.g., tui.md for TUI API details)`;
  }
  if (appendSystemPrompt)
    promptSections.addendum = appendSystemPrompt;
  if (contextFiles.length > 0)
    promptSections.project_context = renderProjectContext(contextFiles);
  const skillFileReadTool = ["read", "bash"].find((tool) => selectedTools.includes(tool));
  if (skillFileReadTool && skills.length > 0) {
    const skillsPrompt = formatSkillsForPrompt(skills, skillFileReadTool).trim();
    if (skillsPrompt)
      promptSections.skills = skillsPrompt;
  }
  promptSections.cwd = cwd.replace(/\\/g, "/");
  for (const [name, content] of Object.entries(customSections)) {
    if (content)
      promptSections[name] = content;
  }
  const sections = { preamble: promptSections.preamble };
  for (const [name, content] of Object.entries(promptSections)) {
    if (name !== "preamble")
      sections[name] = `<${name}>
${content}
</${name}>`;
  }
  return sections;
}
__name(buildSystemPromptSections, "buildSystemPromptSections");
function buildSystemPromptState(input) {
  if (input.forceSystemPrompt !== void 0)
    return { content: input.forceSystemPrompt };
  return { content: "", sections: buildSystemPromptSections(input) };
}
__name(buildSystemPromptState, "buildSystemPromptState");
function buildSystemPrompt(input) {
  return getSystemMessageText({ role: "system", ...buildSystemPromptState(input), timestamp: 0 });
}
__name(buildSystemPrompt, "buildSystemPrompt");
function diffSystemPromptSections(previous, current) {
  const patch = {};
  for (const [name, text] of Object.entries(current)) {
    if (previous[name] !== text)
      patch[name] = text;
  }
  for (const name of Object.keys(previous)) {
    if (current[name] === void 0)
      patch[name] = null;
  }
  return Object.keys(patch).length > 0 ? patch : void 0;
}
__name(diffSystemPromptSections, "diffSystemPromptSections");

// pi-dist/pi-coding-agent/core/extensions/runner.js
var RESERVED_KEYBINDINGS_FOR_EXTENSION_CONFLICTS = [
  "app.interrupt",
  "app.clear",
  "app.exit",
  "app.suspend",
  "app.thinking.cycle",
  "app.model.cycleForward",
  "app.model.cycleBackward",
  "app.model.select",
  "app.tools.expand",
  "app.thinking.toggle",
  "app.editor.external",
  "app.message.copy",
  "app.message.followUp",
  "tui.input.submit",
  "tui.select.confirm",
  "tui.select.cancel",
  "tui.input.copy",
  "tui.editor.deleteToLineEnd"
];
var buildBuiltinKeybindings = /* @__PURE__ */ __name((resolvedKeybindings) => {
  const builtinKeybindings = {};
  for (const [keybinding, keys] of Object.entries(resolvedKeybindings)) {
    if (keys === void 0)
      continue;
    const keyList = Array.isArray(keys) ? keys : [keys];
    const restrictOverride = RESERVED_KEYBINDINGS_FOR_EXTENSION_CONFLICTS.includes(keybinding);
    for (const key of keyList) {
      const normalizedKey = key.toLowerCase();
      const existing = builtinKeybindings[normalizedKey];
      if (existing?.restrictOverride && !restrictOverride)
        continue;
      builtinKeybindings[normalizedKey] = {
        keybinding,
        restrictOverride
      };
    }
  }
  return builtinKeybindings;
}, "buildBuiltinKeybindings");
function isUserBashEventResult(value) {
  if (typeof value !== "object" || value === null)
    return false;
  const candidate = value;
  const hasOperations = candidate.operations !== void 0;
  const hasResult = candidate.result !== void 0;
  if (hasOperations === hasResult)
    return false;
  if (hasOperations) {
    const operations = candidate.operations;
    if (typeof operations !== "object" || operations === null)
      return false;
    return typeof operations.exec === "function";
  }
  const result = candidate.result;
  if (typeof result !== "object" || result === null)
    return false;
  const resultRecord = result;
  return typeof resultRecord.output === "string" && "exitCode" in resultRecord && (resultRecord.exitCode === void 0 || typeof resultRecord.exitCode === "number") && typeof resultRecord.cancelled === "boolean" && typeof resultRecord.truncated === "boolean" && (resultRecord.fullOutputPath === void 0 || typeof resultRecord.fullOutputPath === "string");
}
__name(isUserBashEventResult, "isUserBashEventResult");
async function emitSessionShutdownEvent(extensionRunner, event) {
  if (extensionRunner.hasHandlers("session_shutdown")) {
    await extensionRunner.emit(event);
    return true;
  }
  return false;
}
__name(emitSessionShutdownEvent, "emitSessionShutdownEvent");
function snapshotEventHandlers(extensions, event) {
  return extensions.map((ext) => ({ ext, handlers: ext.handlers.get(event)?.slice() ?? [] }));
}
__name(snapshotEventHandlers, "snapshotEventHandlers");
function sameMessages(left, right) {
  return left.length === right.length && left.every((message, index) => message === right[index]);
}
__name(sameMessages, "sameMessages");
function restoreSystemMessages(current, visible, returned) {
  if (sameMessages(returned, visible))
    return current;
  const head = getCurrentSystemMessage3(current);
  return head ? [head, ...returned] : returned;
}
__name(restoreSystemMessages, "restoreSystemMessages");
async function emitProjectTrustEvent(extensionsResult, event, ctx) {
  const errors = [];
  for (const { ext, handlers } of snapshotEventHandlers(extensionsResult.extensions, "project_trust")) {
    for (const handler of handlers) {
      try {
        const handlerResult = await handler(event, ctx);
        if (handlerResult.trusted === "undecided") {
          continue;
        }
        return { result: handlerResult, errors };
      } catch (error) {
        errors.push({
          extensionPath: ext.path,
          event: event.type,
          error: error instanceof Error ? error.message : String(error),
          stack: error instanceof Error ? error.stack : void 0
        });
      }
    }
  }
  return { errors };
}
__name(emitProjectTrustEvent, "emitProjectTrustEvent");
var noOpUIContext = {
  select: /* @__PURE__ */ __name(async () => void 0, "select"),
  confirm: /* @__PURE__ */ __name(async () => false, "confirm"),
  input: /* @__PURE__ */ __name(async () => void 0, "input"),
  notify: /* @__PURE__ */ __name(() => {
  }, "notify"),
  onTerminalInput: /* @__PURE__ */ __name(() => () => {
  }, "onTerminalInput"),
  setStatus: /* @__PURE__ */ __name(() => {
  }, "setStatus"),
  setWorkingMessage: /* @__PURE__ */ __name(() => {
  }, "setWorkingMessage"),
  setWorkingVisible: /* @__PURE__ */ __name(() => {
  }, "setWorkingVisible"),
  setWorkingIndicator: /* @__PURE__ */ __name(() => {
  }, "setWorkingIndicator"),
  setHiddenThinkingLabel: /* @__PURE__ */ __name(() => {
  }, "setHiddenThinkingLabel"),
  setWidget: /* @__PURE__ */ __name(() => {
  }, "setWidget"),
  setFooter: /* @__PURE__ */ __name(() => {
  }, "setFooter"),
  setHeader: /* @__PURE__ */ __name(() => {
  }, "setHeader"),
  setTitle: /* @__PURE__ */ __name(() => {
  }, "setTitle"),
  custom: /* @__PURE__ */ __name(async () => void 0, "custom"),
  pasteToEditor: /* @__PURE__ */ __name(() => {
  }, "pasteToEditor"),
  setEditorText: /* @__PURE__ */ __name(() => {
  }, "setEditorText"),
  getEditorText: /* @__PURE__ */ __name(() => "", "getEditorText"),
  editor: /* @__PURE__ */ __name(async () => void 0, "editor"),
  addAutocompleteProvider: /* @__PURE__ */ __name(() => {
  }, "addAutocompleteProvider"),
  setEditorComponent: /* @__PURE__ */ __name(() => {
  }, "setEditorComponent"),
  getEditorComponent: /* @__PURE__ */ __name(() => void 0, "getEditorComponent"),
  get theme() {
    return theme;
  },
  getAllThemes: /* @__PURE__ */ __name(() => [], "getAllThemes"),
  getTheme: /* @__PURE__ */ __name(() => void 0, "getTheme"),
  setTheme: /* @__PURE__ */ __name((_theme) => ({ success: false, error: "UI not available" }), "setTheme"),
  getToolsExpanded: /* @__PURE__ */ __name(() => false, "getToolsExpanded"),
  setToolsExpanded: /* @__PURE__ */ __name(() => {
  }, "setToolsExpanded")
};
var ExtensionRunner = class {
  static {
    __name(this, "ExtensionRunner");
  }
  extensions;
  runtime;
  uiContext;
  mode = "print";
  cwd;
  sessionManager;
  modelRegistry;
  errorListeners = /* @__PURE__ */ new Set();
  getModel = /* @__PURE__ */ __name(() => void 0, "getModel");
  getScopedModels = /* @__PURE__ */ __name(() => [], "getScopedModels");
  isIdleFn = /* @__PURE__ */ __name(() => true, "isIdleFn");
  isProjectTrustedFn = /* @__PURE__ */ __name(() => true, "isProjectTrustedFn");
  getSignalFn = /* @__PURE__ */ __name(() => void 0, "getSignalFn");
  waitForIdleFn = /* @__PURE__ */ __name(async () => {
  }, "waitForIdleFn");
  abortFn = /* @__PURE__ */ __name(() => {
  }, "abortFn");
  hasPendingMessagesFn = /* @__PURE__ */ __name(() => false, "hasPendingMessagesFn");
  getContextUsageFn = /* @__PURE__ */ __name(() => void 0, "getContextUsageFn");
  compactFn = /* @__PURE__ */ __name(() => {
  }, "compactFn");
  getSystemPromptFn = /* @__PURE__ */ __name(() => "", "getSystemPromptFn");
  getSystemPromptOptionsFn = /* @__PURE__ */ __name(() => normalizeBuildSystemPromptOptions({ cwd: this.cwd }), "getSystemPromptOptionsFn");
  newSessionHandler = /* @__PURE__ */ __name(async () => ({ cancelled: false }), "newSessionHandler");
  forkHandler = /* @__PURE__ */ __name(async () => ({ cancelled: false }), "forkHandler");
  navigateTreeHandler = /* @__PURE__ */ __name(async () => ({ cancelled: false }), "navigateTreeHandler");
  switchSessionHandler = /* @__PURE__ */ __name(async () => ({ cancelled: false }), "switchSessionHandler");
  reloadHandler = /* @__PURE__ */ __name(async () => {
  }, "reloadHandler");
  shutdownHandler = /* @__PURE__ */ __name(() => {
  }, "shutdownHandler");
  shortcutDiagnostics = [];
  commandDiagnostics = [];
  staleMessage;
  uiPromptDepth = 0;
  activeUIPrompt;
  constructor(extensions, runtime, cwd, sessionManager, modelRegistry) {
    this.extensions = extensions;
    this.runtime = runtime;
    this.uiContext = noOpUIContext;
    this.cwd = cwd;
    this.sessionManager = sessionManager;
    this.modelRegistry = modelRegistry;
  }
  bindCore(actions, contextActions, providerActions) {
    this.runtime.sendMessage = actions.sendMessage;
    this.runtime.sendUserMessage = actions.sendUserMessage;
    this.runtime.appendEntry = actions.appendEntry;
    this.runtime.setSessionName = actions.setSessionName;
    this.runtime.getSessionName = actions.getSessionName;
    this.runtime.setLabel = actions.setLabel;
    this.runtime.getActiveTools = actions.getActiveTools;
    this.runtime.getAllTools = actions.getAllTools;
    this.runtime.setActiveTools = actions.setActiveTools;
    this.runtime.refreshTools = actions.refreshTools;
    this.runtime.getCommands = actions.getCommands;
    this.runtime.setModel = actions.setModel;
    this.runtime.getThinkingLevel = actions.getThinkingLevel;
    this.runtime.setThinkingLevel = actions.setThinkingLevel;
    this.getModel = contextActions.getModel;
    this.getScopedModels = contextActions.getScopedModels;
    this.isIdleFn = contextActions.isIdle;
    this.isProjectTrustedFn = contextActions.isProjectTrusted;
    this.getSignalFn = contextActions.getSignal;
    this.abortFn = contextActions.abort;
    this.hasPendingMessagesFn = contextActions.hasPendingMessages;
    this.shutdownHandler = contextActions.shutdown;
    this.getContextUsageFn = contextActions.getContextUsage;
    this.compactFn = contextActions.compact;
    this.getSystemPromptFn = contextActions.getSystemPrompt;
    this.getSystemPromptOptionsFn = contextActions.getSystemPromptOptions ?? (() => normalizeBuildSystemPromptOptions({ cwd: this.cwd }));
    for (const { name, config, extensionPath } of this.runtime.pendingProviderRegistrations) {
      try {
        if (providerActions?.registerProvider) {
          providerActions.registerProvider(name, config);
        } else {
          this.modelRegistry.registerProvider(name, config);
        }
      } catch (err) {
        this.emitError({
          extensionPath,
          event: "register_provider",
          error: err instanceof Error ? err.message : String(err),
          stack: err instanceof Error ? err.stack : void 0
        });
      }
    }
    this.runtime.pendingProviderRegistrations = [];
    for (const { provider, extensionPath } of this.runtime.pendingNativeProviderRegistrations) {
      try {
        if (providerActions?.registerNativeProvider) {
          providerActions.registerNativeProvider(provider);
        } else {
          this.modelRegistry.registerProvider(provider);
        }
      } catch (err) {
        this.emitError({
          extensionPath,
          event: "register_provider",
          error: err instanceof Error ? err.message : String(err),
          stack: err instanceof Error ? err.stack : void 0
        });
      }
    }
    this.runtime.pendingNativeProviderRegistrations = [];
    this.runtime.registerProvider = (name, config) => {
      if (providerActions?.registerProvider) {
        providerActions.registerProvider(name, config);
        return;
      }
      this.modelRegistry.registerProvider(name, config);
    };
    this.runtime.registerNativeProvider = (provider) => {
      if (providerActions?.registerNativeProvider) {
        providerActions.registerNativeProvider(provider);
        return;
      }
      this.modelRegistry.registerProvider(provider);
    };
    this.runtime.unregisterProvider = (name) => {
      if (providerActions?.unregisterProvider) {
        providerActions.unregisterProvider(name);
        return;
      }
      this.modelRegistry.unregisterProvider(name);
    };
  }
  bindCommandContext(actions) {
    if (actions) {
      this.waitForIdleFn = actions.waitForIdle;
      this.newSessionHandler = actions.newSession;
      this.forkHandler = actions.fork;
      this.navigateTreeHandler = actions.navigateTree;
      this.switchSessionHandler = actions.switchSession;
      this.reloadHandler = actions.reload;
      return;
    }
    this.waitForIdleFn = async () => {
    };
    this.newSessionHandler = async () => ({ cancelled: false });
    this.forkHandler = async () => ({ cancelled: false });
    this.navigateTreeHandler = async () => ({ cancelled: false });
    this.switchSessionHandler = async () => ({ cancelled: false });
    this.reloadHandler = async () => {
    };
  }
  setUIContext(uiContext, mode = "print") {
    this.uiContext = uiContext ? this.wrapUIPromptContext(uiContext) : noOpUIContext;
    this.mode = mode;
  }
  wrapUIPromptContext(ui) {
    return {
      ...ui,
      select: /* @__PURE__ */ __name((title, options, opts) => this.withUIPrompt("select", title, () => ui.select(title, options, opts)), "select"),
      confirm: /* @__PURE__ */ __name((title, message, opts) => this.withUIPrompt("confirm", title, () => ui.confirm(title, message, opts)), "confirm"),
      input: /* @__PURE__ */ __name((title, placeholder, opts) => this.withUIPrompt("input", title, () => ui.input(title, placeholder, opts)), "input"),
      editor: /* @__PURE__ */ __name((title, prefill) => this.withUIPrompt("editor", title, () => ui.editor(title, prefill)), "editor"),
      custom: /* @__PURE__ */ __name((factory, options) => this.withUIPrompt("custom", void 0, () => ui.custom(factory, options)), "custom")
    };
  }
  withUIPrompt(kind, title, run) {
    const outerPrompt = this.uiPromptDepth++ === 0;
    if (outerPrompt) {
      this.activeUIPrompt = { kind, title };
      this.emitUIPromptEvent({ type: "ui_prompt_start", reason: "ui_prompt", kind, ...title ? { title } : {} });
    }
    const finish = /* @__PURE__ */ __name(() => {
      if (--this.uiPromptDepth > 0)
        return;
      this.uiPromptDepth = 0;
      const prompt = this.activeUIPrompt ?? { kind, title };
      this.activeUIPrompt = void 0;
      this.emitUIPromptEvent({
        type: "ui_prompt_end",
        reason: "ui_prompt",
        kind: prompt.kind,
        ...prompt.title ? { title: prompt.title } : {}
      });
    }, "finish");
    try {
      return run().finally(finish);
    } catch (err) {
      finish();
      throw err;
    }
  }
  emitUIPromptEvent(event) {
    queueMicrotask(() => {
      void this.emit(event);
    });
  }
  getUIContext() {
    return this.uiContext;
  }
  hasUI() {
    return this.uiContext !== noOpUIContext;
  }
  getExtensionPaths() {
    return this.extensions.map((e) => e.path);
  }
  /** Get all registered tools from all extensions (first registration per name wins). */
  getAllRegisteredTools() {
    const toolsByName = /* @__PURE__ */ new Map();
    for (const ext of this.extensions) {
      for (const tool of ext.tools.values()) {
        if (!toolsByName.has(tool.definition.name)) {
          toolsByName.set(tool.definition.name, tool);
        }
      }
    }
    return Array.from(toolsByName.values());
  }
  /** Get a tool definition by name. Returns undefined if not found. */
  getToolDefinition(toolName) {
    for (const ext of this.extensions) {
      const tool = ext.tools.get(toolName);
      if (tool) {
        return tool.definition;
      }
    }
    return void 0;
  }
  getFlags() {
    const allFlags = /* @__PURE__ */ new Map();
    for (const ext of this.extensions) {
      for (const [name, flag] of ext.flags) {
        if (!allFlags.has(name)) {
          allFlags.set(name, flag);
        }
      }
    }
    return allFlags;
  }
  setFlagValue(name, value) {
    this.runtime.flagValues.set(name, value);
  }
  getFlagValues() {
    return new Map(this.runtime.flagValues);
  }
  getShortcuts(resolvedKeybindings) {
    this.shortcutDiagnostics = [];
    const builtinKeybindings = buildBuiltinKeybindings(resolvedKeybindings);
    const extensionShortcuts = /* @__PURE__ */ new Map();
    const addDiagnostic = /* @__PURE__ */ __name((message, extensionPath) => {
      this.shortcutDiagnostics.push({ type: "warning", message, path: extensionPath });
      if (!this.hasUI()) {
        console.warn(message);
      }
    }, "addDiagnostic");
    for (const ext of this.extensions) {
      for (const [key, shortcut] of ext.shortcuts) {
        const normalizedKey = key.toLowerCase();
        const builtInKeybinding = builtinKeybindings[normalizedKey];
        if (builtInKeybinding?.restrictOverride === true) {
          addDiagnostic(`Extension shortcut '${key}' from ${shortcut.extensionPath} conflicts with built-in shortcut. Skipping.`, shortcut.extensionPath);
          continue;
        }
        if (builtInKeybinding?.restrictOverride === false) {
          addDiagnostic(`Extension shortcut conflict: '${key}' is built-in shortcut for ${builtInKeybinding.keybinding} and ${shortcut.extensionPath}. Using ${shortcut.extensionPath}.`, shortcut.extensionPath);
        }
        const existingExtensionShortcut = extensionShortcuts.get(normalizedKey);
        if (existingExtensionShortcut) {
          addDiagnostic(`Extension shortcut conflict: '${key}' registered by both ${existingExtensionShortcut.extensionPath} and ${shortcut.extensionPath}. Using ${shortcut.extensionPath}.`, shortcut.extensionPath);
        }
        extensionShortcuts.set(normalizedKey, shortcut);
      }
    }
    return extensionShortcuts;
  }
  getShortcutDiagnostics() {
    return this.shortcutDiagnostics;
  }
  invalidate(message = "This extension ctx is stale after session replacement or reload. Do not use a captured pi or command ctx after ctx.newSession(), ctx.fork(), ctx.switchSession(), or ctx.reload(). For newSession, fork, and switchSession, move post-replacement work into withSession and use the ctx passed to withSession. For reload, do not use the old ctx after await ctx.reload().") {
    if (!this.staleMessage) {
      this.staleMessage = message;
      this.runtime.invalidate(message);
    }
  }
  assertActive() {
    if (this.staleMessage) {
      throw new Error(this.staleMessage);
    }
  }
  onError(listener) {
    this.errorListeners.add(listener);
    return () => this.errorListeners.delete(listener);
  }
  emitError(error) {
    for (const listener of this.errorListeners) {
      listener(error);
    }
  }
  hasHandlers(eventType) {
    for (const ext of this.extensions) {
      const handlers = ext.handlers.get(eventType);
      if (handlers && handlers.length > 0) {
        return true;
      }
    }
    return false;
  }
  getMessageRenderer(customType) {
    for (const ext of this.extensions) {
      const renderer = ext.messageRenderers.get(customType);
      if (renderer) {
        return renderer;
      }
    }
    return void 0;
  }
  getMarkdownTransformers() {
    return this.extensions.flatMap((ext) => ext.markdownTransformer ? [ext.markdownTransformer] : []);
  }
  getEntryRenderer(customType) {
    for (const ext of this.extensions) {
      const renderer = ext.entryRenderers?.get(customType);
      if (renderer) {
        return renderer;
      }
    }
    return void 0;
  }
  resolveRegisteredCommands() {
    const commands = [];
    const counts = /* @__PURE__ */ new Map();
    for (const ext of this.extensions) {
      for (const command of ext.commands.values()) {
        commands.push(command);
        counts.set(command.name, (counts.get(command.name) ?? 0) + 1);
      }
    }
    const seen = /* @__PURE__ */ new Map();
    const takenInvocationNames = /* @__PURE__ */ new Set();
    return commands.map((command) => {
      const occurrence = (seen.get(command.name) ?? 0) + 1;
      seen.set(command.name, occurrence);
      let invocationName = (counts.get(command.name) ?? 0) > 1 ? `${command.name}:${occurrence}` : command.name;
      if (takenInvocationNames.has(invocationName)) {
        let suffix = occurrence;
        do {
          suffix++;
          invocationName = `${command.name}:${suffix}`;
        } while (takenInvocationNames.has(invocationName));
      }
      takenInvocationNames.add(invocationName);
      return {
        ...command,
        invocationName
      };
    });
  }
  getModelRegistry() {
    return this.modelRegistry;
  }
  getRegisteredCommands() {
    this.commandDiagnostics = [];
    return this.resolveRegisteredCommands();
  }
  getCommandDiagnostics() {
    return this.commandDiagnostics;
  }
  getCommand(name) {
    return this.resolveRegisteredCommands().find((command) => command.invocationName === name);
  }
  /**
   * Request a graceful shutdown. Called by extension tools and event handlers.
   * The actual shutdown behavior is provided by the mode via bindExtensions().
   */
  shutdown() {
    this.shutdownHandler();
  }
  getActiveTools() {
    this.assertActive();
    return this.runtime.getActiveTools();
  }
  /**
   * Create an ExtensionContext for use in event handlers and tool execution.
   * Context values are resolved at call time, so changes via bindCore/bindUI are reflected.
   */
  createContext() {
    const runner = this;
    const getModel = this.getModel;
    const getScopedModels = this.getScopedModels;
    return {
      get ui() {
        runner.assertActive();
        return runner.uiContext;
      },
      get mode() {
        runner.assertActive();
        return runner.mode;
      },
      get hasUI() {
        runner.assertActive();
        return runner.hasUI();
      },
      get cwd() {
        runner.assertActive();
        return runner.cwd;
      },
      get sessionManager() {
        runner.assertActive();
        return runner.sessionManager;
      },
      get modelRegistry() {
        runner.assertActive();
        return runner.modelRegistry;
      },
      get model() {
        runner.assertActive();
        return getModel();
      },
      get scopedModels() {
        runner.assertActive();
        return getScopedModels();
      },
      get thinkingLevel() {
        runner.assertActive();
        return runner.runtime.getThinkingLevel();
      },
      isIdle: /* @__PURE__ */ __name(() => {
        runner.assertActive();
        return runner.isIdleFn();
      }, "isIdle"),
      isProjectTrusted: /* @__PURE__ */ __name(() => {
        runner.assertActive();
        return runner.isProjectTrustedFn();
      }, "isProjectTrusted"),
      get signal() {
        runner.assertActive();
        return runner.getSignalFn();
      },
      abort: /* @__PURE__ */ __name(() => {
        runner.assertActive();
        runner.abortFn();
      }, "abort"),
      hasPendingMessages: /* @__PURE__ */ __name(() => {
        runner.assertActive();
        return runner.hasPendingMessagesFn();
      }, "hasPendingMessages"),
      shutdown: /* @__PURE__ */ __name(() => {
        runner.assertActive();
        runner.shutdownHandler();
      }, "shutdown"),
      getContextUsage: /* @__PURE__ */ __name(() => {
        runner.assertActive();
        return runner.getContextUsageFn();
      }, "getContextUsage"),
      compact: /* @__PURE__ */ __name((options) => {
        runner.assertActive();
        runner.compactFn(options);
      }, "compact"),
      getSystemPrompt: /* @__PURE__ */ __name(() => {
        runner.assertActive();
        return runner.getSystemPromptFn();
      }, "getSystemPrompt")
    };
  }
  createCommandContext() {
    const context = Object.defineProperties({}, Object.getOwnPropertyDescriptors(this.createContext()));
    context.getSystemPromptOptions = () => {
      this.assertActive();
      return this.getSystemPromptOptionsFn();
    };
    context.waitForIdle = () => {
      this.assertActive();
      return this.waitForIdleFn();
    };
    context.newSession = (options) => {
      this.assertActive();
      return this.newSessionHandler(options);
    };
    context.fork = (entryId, options) => {
      this.assertActive();
      return this.forkHandler(entryId, options);
    };
    context.navigateTree = (targetId, options) => {
      this.assertActive();
      return this.navigateTreeHandler(targetId, options);
    };
    context.switchSession = (sessionPath, options) => {
      this.assertActive();
      return this.switchSessionHandler(sessionPath, options);
    };
    context.reload = () => {
      this.assertActive();
      return this.reloadHandler();
    };
    return context;
  }
  async emitBoundary(baseEvent, buildContext) {
    const ctx = this.createContext();
    let entries = [];
    let shouldContinue = false;
    let context = await buildContext(entries);
    let valid2 = true;
    for (const { ext, handlers } of snapshotEventHandlers(this.extensions, baseEvent.type)) {
      for (const handler of handlers) {
        const event = {
          ...baseEvent,
          entries,
          continue: shouldContinue,
          context
        };
        try {
          const handlerResult = await handler(event, ctx);
          if (handlerResult?.entries !== void 0)
            entries = handlerResult.entries;
          if (handlerResult?.continue !== void 0)
            shouldContinue = handlerResult.continue;
        } catch (err) {
          this.emitError({
            extensionPath: ext.path,
            event: baseEvent.type,
            error: err instanceof Error ? err.message : String(err),
            stack: err instanceof Error ? err.stack : void 0
          });
        }
        try {
          context = await buildContext(entries);
          valid2 = true;
        } catch (err) {
          valid2 = false;
          this.emitError({
            extensionPath: ext.path,
            event: baseEvent.type,
            error: `Invalid boundary entries: ${err instanceof Error ? err.message : String(err)}`,
            stack: err instanceof Error ? err.stack : void 0
          });
        }
      }
    }
    return valid2 ? { entries, continue: shouldContinue, context, valid: true } : { entries: [], continue: false, context, valid: false };
  }
  isSessionBeforeEvent(event) {
    return event.type === "session_before_switch" || event.type === "session_before_fork" || event.type === "session_before_compact" || event.type === "session_before_tree";
  }
  async emit(event) {
    const ctx = this.createContext();
    let result;
    for (const { ext, handlers } of snapshotEventHandlers(this.extensions, event.type)) {
      for (const handler of handlers) {
        try {
          const handlerResult = await handler(event, ctx);
          if (this.isSessionBeforeEvent(event) && handlerResult) {
            result = handlerResult;
            if (result.cancel) {
              return result;
            }
          }
        } catch (err) {
          const message = err instanceof Error ? err.message : String(err);
          const stack = err instanceof Error ? err.stack : void 0;
          this.emitError({
            extensionPath: ext.path,
            event: event.type,
            error: message,
            stack
          });
        }
      }
    }
    return result;
  }
  /** Returns the event's own action unless a handler overrides it; the last override wins. */
  async emitCacheWarmingDecision(event) {
    const ctx = this.createContext();
    let action = event.action;
    for (const { ext, handlers } of snapshotEventHandlers(this.extensions, event.type)) {
      for (const handler of handlers) {
        try {
          const result = await handler(event, ctx);
          if (result?.action !== void 0)
            action = result.action;
        } catch (err) {
          this.emitError({
            extensionPath: ext.path,
            event: event.type,
            error: err instanceof Error ? err.message : String(err),
            stack: err instanceof Error ? err.stack : void 0
          });
        }
      }
    }
    return action;
  }
  async emitMessageEnd(event) {
    const ctx = this.createContext();
    let currentMessage = event.message;
    let modified = false;
    for (const { ext, handlers } of snapshotEventHandlers(this.extensions, "message_end")) {
      for (const handler of handlers) {
        try {
          const currentEvent = { ...event, message: currentMessage };
          const handlerResult = await handler(currentEvent, ctx);
          if (!handlerResult?.message)
            continue;
          if (handlerResult.message.role !== currentMessage.role) {
            this.emitError({
              extensionPath: ext.path,
              event: "message_end",
              error: "message_end handlers must return a message with the same role"
            });
            continue;
          }
          currentMessage = handlerResult.message;
          modified = true;
        } catch (err) {
          const message = err instanceof Error ? err.message : String(err);
          const stack = err instanceof Error ? err.stack : void 0;
          this.emitError({
            extensionPath: ext.path,
            event: "message_end",
            error: message,
            stack
          });
        }
      }
    }
    return modified ? currentMessage : void 0;
  }
  async emitToolResult(event) {
    const ctx = this.createContext();
    const currentEvent = { ...event };
    let modified = false;
    for (const { ext, handlers } of snapshotEventHandlers(this.extensions, "tool_result")) {
      for (const handler of handlers) {
        try {
          const handlerResult = await handler(currentEvent, ctx);
          if (!handlerResult)
            continue;
          if (handlerResult.content !== void 0) {
            currentEvent.content = handlerResult.content;
            modified = true;
          }
          if (handlerResult.details !== void 0) {
            currentEvent.details = handlerResult.details;
            modified = true;
          }
          if (handlerResult.isError !== void 0) {
            currentEvent.isError = handlerResult.isError;
            modified = true;
          }
          if (handlerResult.usage !== void 0) {
            currentEvent.usage = handlerResult.usage;
            modified = true;
          }
        } catch (err) {
          const message = err instanceof Error ? err.message : String(err);
          const stack = err instanceof Error ? err.stack : void 0;
          this.emitError({
            extensionPath: ext.path,
            event: "tool_result",
            error: message,
            stack
          });
        }
      }
    }
    if (!modified) {
      return void 0;
    }
    return {
      content: currentEvent.content,
      details: currentEvent.details,
      isError: currentEvent.isError,
      usage: currentEvent.usage
    };
  }
  async emitToolCall(event) {
    const ctx = this.createContext();
    let result;
    for (const { handlers } of snapshotEventHandlers(this.extensions, "tool_call")) {
      for (const handler of handlers) {
        const handlerResult = await handler(event, ctx);
        if (handlerResult) {
          result = handlerResult;
          if (result.block) {
            return result;
          }
        }
      }
    }
    return result;
  }
  async emitUserBash(event) {
    const ctx = this.createContext();
    for (const { ext, handlers } of snapshotEventHandlers(this.extensions, "user_bash")) {
      for (const handler of handlers) {
        try {
          const handlerResult = await handler(event, ctx);
          if (handlerResult === void 0)
            continue;
          if (!isUserBashEventResult(handlerResult)) {
            throw new Error("Invalid user_bash handler result: return undefined for local execution or exactly one valid { operations } or { result } object");
          }
          return handlerResult;
        } catch (err) {
          const message = err instanceof Error ? err.message : String(err);
          const stack = err instanceof Error ? err.stack : void 0;
          this.emitError({
            extensionPath: ext.path,
            event: "user_bash",
            error: message,
            stack
          });
          throw err;
        }
      }
    }
    return void 0;
  }
  /**
   * Run the request-time transforms in two phases. `context` handlers see the conversation
   * only and Pi restores the prompt and tool state after each; `context_with_system`
   * handlers then see the full transcript and their output is used as returned.
   */
  async emitContext(messages) {
    const ctx = this.createContext();
    let currentMessages = structuredClone(messages);
    for (const { ext, handlers } of snapshotEventHandlers(this.extensions, "context")) {
      for (const handler of handlers) {
        try {
          const visibleMessages = currentMessages.filter((message) => message.role !== "system");
          const visibleSnapshot = visibleMessages.slice();
          const event = { type: "context", messages: visibleMessages };
          const handlerResult = await handler(event, ctx);
          const returned = handlerResult?.messages ?? (sameMessages(visibleMessages, visibleSnapshot) ? void 0 : visibleMessages);
          if (!returned)
            continue;
          currentMessages = restoreSystemMessages(currentMessages, visibleSnapshot, returned);
        } catch (err) {
          const message = err instanceof Error ? err.message : String(err);
          const stack = err instanceof Error ? err.stack : void 0;
          this.emitError({
            extensionPath: ext.path,
            event: "context",
            error: message,
            stack
          });
        }
      }
    }
    for (const { ext, handlers } of snapshotEventHandlers(this.extensions, "context_with_system")) {
      for (const handler of handlers) {
        try {
          const hadLeadingSystemMessage = currentMessages[0]?.role === "system";
          const event = { type: "context_with_system", messages: currentMessages };
          const handlerResult = await handler(event, ctx);
          currentMessages = handlerResult?.messages ?? currentMessages;
          if (hadLeadingSystemMessage && currentMessages[0]?.role !== "system") {
            this.emitError({
              extensionPath: ext.path,
              event: "context_with_system",
              error: "Handler removed the leading system message; the request has no prompt or initial tool declarations. Keep it at index 0 or replace a dropped prefix with getCurrentSystemMessage()."
            });
          }
        } catch (err) {
          const message = err instanceof Error ? err.message : String(err);
          const stack = err instanceof Error ? err.stack : void 0;
          this.emitError({
            extensionPath: ext.path,
            event: "context_with_system",
            error: message,
            stack
          });
        }
      }
    }
    return currentMessages;
  }
  async emitBeforeProviderRequest(payload) {
    const ctx = this.createContext();
    let currentPayload = payload;
    for (const { ext, handlers } of snapshotEventHandlers(this.extensions, "before_provider_request")) {
      for (const handler of handlers) {
        try {
          const event = {
            type: "before_provider_request",
            payload: currentPayload
          };
          const handlerResult = await handler(event, ctx);
          if (handlerResult !== void 0) {
            currentPayload = handlerResult;
          }
        } catch (err) {
          const message = err instanceof Error ? err.message : String(err);
          const stack = err instanceof Error ? err.stack : void 0;
          this.emitError({
            extensionPath: ext.path,
            event: "before_provider_request",
            error: message,
            stack
          });
        }
      }
    }
    return currentPayload;
  }
  async emitBeforeProviderHeaders(headers) {
    const ctx = this.createContext();
    for (const { ext, handlers } of snapshotEventHandlers(this.extensions, "before_provider_headers")) {
      for (const handler of handlers) {
        try {
          const event = {
            type: "before_provider_headers",
            headers
          };
          await handler(event, ctx);
        } catch (err) {
          const message = err instanceof Error ? err.message : String(err);
          const stack = err instanceof Error ? err.stack : void 0;
          this.emitError({
            extensionPath: ext.path,
            event: "before_provider_headers",
            error: message,
            stack
          });
        }
      }
    }
    return headers;
  }
  async emitBeforeAgentStart(prompt, images, systemPromptOptions) {
    const currentOptions = normalizeBuildSystemPromptOptions(systemPromptOptions);
    const renderCurrentSystemPrompt = /* @__PURE__ */ __name(() => buildSystemPrompt(currentOptions), "renderCurrentSystemPrompt");
    const ctx = Object.defineProperties({}, Object.getOwnPropertyDescriptors(this.createContext()));
    ctx.getSystemPrompt = () => {
      this.assertActive();
      return renderCurrentSystemPrompt();
    };
    const messages = [];
    for (const { ext, handlers } of snapshotEventHandlers(this.extensions, "before_agent_start")) {
      for (const handler of handlers) {
        try {
          const event = {
            type: "before_agent_start",
            prompt,
            images,
            get systemPrompt() {
              return renderCurrentSystemPrompt();
            },
            systemPromptOptions: currentOptions
          };
          const handlerResult = await handler(event, ctx);
          if (handlerResult) {
            const result = handlerResult;
            if (result.message)
              messages.push(result.message);
            if (result.systemPrompt !== void 0) {
              currentOptions.forceSystemPrompt = result.systemPrompt;
            }
          }
        } catch (err) {
          const message = err instanceof Error ? err.message : String(err);
          const stack = err instanceof Error ? err.stack : void 0;
          this.emitError({
            extensionPath: ext.path,
            event: "before_agent_start",
            error: message,
            stack
          });
        }
      }
    }
    return { messages, systemPromptOptions: currentOptions };
  }
  async emitResourcesDiscover(cwd, reason) {
    const ctx = this.createContext();
    const skillPaths = [];
    const promptPaths = [];
    const themePaths = [];
    for (const { ext, handlers } of snapshotEventHandlers(this.extensions, "resources_discover")) {
      for (const handler of handlers) {
        try {
          const event = { type: "resources_discover", cwd, reason };
          const handlerResult = await handler(event, ctx);
          const result = handlerResult;
          if (result?.skillPaths?.length) {
            skillPaths.push(...result.skillPaths.map((path2) => ({ path: path2, extensionPath: ext.path })));
          }
          if (result?.promptPaths?.length) {
            promptPaths.push(...result.promptPaths.map((path2) => ({ path: path2, extensionPath: ext.path })));
          }
          if (result?.themePaths?.length) {
            themePaths.push(...result.themePaths.map((path2) => ({ path: path2, extensionPath: ext.path })));
          }
        } catch (err) {
          const message = err instanceof Error ? err.message : String(err);
          const stack = err instanceof Error ? err.stack : void 0;
          this.emitError({
            extensionPath: ext.path,
            event: "resources_discover",
            error: message,
            stack
          });
        }
      }
    }
    return { skillPaths, promptPaths, themePaths };
  }
  /** Emit input event. Transforms chain, "handled" short-circuits. */
  async emitInput(text, images, source, streamingBehavior) {
    const ctx = this.createContext();
    let currentText = text;
    let currentImages = images;
    for (const { ext, handlers } of snapshotEventHandlers(this.extensions, "input")) {
      for (const handler of handlers) {
        try {
          const event = {
            type: "input",
            text: currentText,
            images: currentImages,
            source,
            streamingBehavior
          };
          const result = await handler(event, ctx);
          if (result?.action === "handled")
            return result;
          if (result?.action === "transform") {
            currentText = result.text;
            currentImages = result.images ?? currentImages;
          }
        } catch (err) {
          this.emitError({
            extensionPath: ext.path,
            event: "input",
            error: err instanceof Error ? err.message : String(err),
            stack: err instanceof Error ? err.stack : void 0
          });
        }
      }
    }
    return currentText !== text || currentImages !== images ? { action: "transform", text: currentText, images: currentImages } : { action: "continue" };
  }
};

// pi-dist/pi-coding-agent/core/extensions/types.js
function defineTool(tool) {
  return tool;
}
__name(defineTool, "defineTool");
function isBashToolResult(e) {
  return e.toolName === "bash";
}
__name(isBashToolResult, "isBashToolResult");
function isPowerShellToolResult(e) {
  return e.toolName === "powershell";
}
__name(isPowerShellToolResult, "isPowerShellToolResult");
function isReadToolResult(e) {
  return e.toolName === "read";
}
__name(isReadToolResult, "isReadToolResult");
function isEditToolResult(e) {
  return e.toolName === "edit";
}
__name(isEditToolResult, "isEditToolResult");
function isWriteToolResult(e) {
  return e.toolName === "write";
}
__name(isWriteToolResult, "isWriteToolResult");
function isGrepToolResult(e) {
  return e.toolName === "grep";
}
__name(isGrepToolResult, "isGrepToolResult");
function isFindToolResult(e) {
  return e.toolName === "find";
}
__name(isFindToolResult, "isFindToolResult");
function isLsToolResult(e) {
  return e.toolName === "ls";
}
__name(isLsToolResult, "isLsToolResult");
function isToolCallEventType(toolName, event) {
  return event.toolName === toolName;
}
__name(isToolCallEventType, "isToolCallEventType");

// pi-dist/pi-coding-agent/core/extensions/wrapper.js
function wrapRegisteredTool(registeredTool, runner) {
  return wrapToolDefinition(registeredTool.definition, () => runner.createContext());
}
__name(wrapRegisteredTool, "wrapRegisteredTool");
function wrapRegisteredTools(registeredTools, runner) {
  return registeredTools.map((tool) => wrapRegisteredTool(tool, runner));
}
__name(wrapRegisteredTools, "wrapRegisteredTools");

// pi-dist/pi-coding-agent/core/provider-composer.js
import { lazyStream } from "../../pi-ai/sdk-bundle/index.js";
import { getApiProvider } from "../../pi-ai/sdk-bundle/compat.js";

// pi-dist/pi-coding-agent/core/resolve-config-value.js
import { execSync, spawnSync } from "child_process";
var commandResultCache = /* @__PURE__ */ new Map();
var ENV_VAR_NAME_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;
var ENV_VAR_NAME_PREFIX_RE = /^[A-Za-z_][A-Za-z0-9_]*/;
function appendLiteral(parts, value) {
  if (!value)
    return;
  const previousPart = parts[parts.length - 1];
  if (previousPart?.type === "literal") {
    previousPart.value += value;
    return;
  }
  parts.push({ type: "literal", value });
}
__name(appendLiteral, "appendLiteral");
function parseConfigValueTemplate(config) {
  const parts = [];
  let index = 0;
  while (index < config.length) {
    const dollarIndex = config.indexOf("$", index);
    if (dollarIndex < 0) {
      appendLiteral(parts, config.slice(index));
      break;
    }
    appendLiteral(parts, config.slice(index, dollarIndex));
    const nextChar = config[dollarIndex + 1];
    if (nextChar === "$" || nextChar === "!") {
      appendLiteral(parts, nextChar);
      index = dollarIndex + 2;
      continue;
    }
    if (nextChar === "{") {
      const endIndex = config.indexOf("}", dollarIndex + 2);
      if (endIndex < 0) {
        appendLiteral(parts, "$");
        index = dollarIndex + 1;
        continue;
      }
      const name = config.slice(dollarIndex + 2, endIndex);
      if (ENV_VAR_NAME_RE.test(name)) {
        parts.push({ type: "env", name });
      } else {
        appendLiteral(parts, config.slice(dollarIndex, endIndex + 1));
      }
      index = endIndex + 1;
      continue;
    }
    const match = config.slice(dollarIndex + 1).match(ENV_VAR_NAME_PREFIX_RE);
    if (match) {
      parts.push({ type: "env", name: match[0] });
      index = dollarIndex + 1 + match[0].length;
      continue;
    }
    appendLiteral(parts, "$");
    index = dollarIndex + 1;
  }
  return parts;
}
__name(parseConfigValueTemplate, "parseConfigValueTemplate");
function parseConfigValueReference(config) {
  if (config.startsWith("!")) {
    return { type: "command", config };
  }
  return { type: "template", parts: parseConfigValueTemplate(config) };
}
__name(parseConfigValueReference, "parseConfigValueReference");
function resolveEnvConfigValue(name, env) {
  return env?.[name] || process.env[name] || void 0;
}
__name(resolveEnvConfigValue, "resolveEnvConfigValue");
function getTemplateEnvVarNames(parts) {
  const names = [];
  for (const part of parts) {
    if (part.type !== "env" || names.includes(part.name))
      continue;
    names.push(part.name);
  }
  return names;
}
__name(getTemplateEnvVarNames, "getTemplateEnvVarNames");
function resolveTemplate(parts, env) {
  let resolved = "";
  for (const part of parts) {
    if (part.type === "literal") {
      resolved += part.value;
      continue;
    }
    const envValue = resolveEnvConfigValue(part.name, env);
    if (envValue === void 0)
      return void 0;
    resolved += envValue;
  }
  return resolved;
}
__name(resolveTemplate, "resolveTemplate");
function getConfigValueEnvVarNames(config) {
  const reference = parseConfigValueReference(config);
  return reference.type === "template" ? getTemplateEnvVarNames(reference.parts) : [];
}
__name(getConfigValueEnvVarNames, "getConfigValueEnvVarNames");
function getMissingConfigValueEnvVarNames(config, env) {
  return getConfigValueEnvVarNames(config).filter((name) => resolveEnvConfigValue(name, env) === void 0);
}
__name(getMissingConfigValueEnvVarNames, "getMissingConfigValueEnvVarNames");
function isCommandConfigValue(config) {
  return parseConfigValueReference(config).type === "command";
}
__name(isCommandConfigValue, "isCommandConfigValue");
function isConfigValueConfigured(config, env) {
  return getMissingConfigValueEnvVarNames(config, env).length === 0;
}
__name(isConfigValueConfigured, "isConfigValueConfigured");
function resolveConfigValue(config, env) {
  const reference = parseConfigValueReference(config);
  if (reference.type === "command") {
    return executeCommand(reference.config);
  }
  return resolveTemplate(reference.parts, env);
}
__name(resolveConfigValue, "resolveConfigValue");
function executeWithConfiguredShell(command) {
  try {
    const { shell, args, commandTransport } = getShellConfig();
    const commandFromStdin = commandTransport === "stdin";
    const result = spawnSync(shell, commandFromStdin ? args : [...args, command], {
      encoding: "utf-8",
      input: commandFromStdin ? command : void 0,
      timeout: 1e4,
      stdio: [commandFromStdin ? "pipe" : "ignore", "pipe", "ignore"],
      shell: false,
      windowsHide: true
    });
    if (result.error) {
      const error = result.error;
      if (error.code === "ENOENT") {
        return { executed: false, value: void 0 };
      }
      return { executed: true, value: void 0 };
    }
    if (result.status !== 0) {
      return { executed: true, value: void 0 };
    }
    const value = (result.stdout ?? "").trim();
    return { executed: true, value: value || void 0 };
  } catch {
    return { executed: false, value: void 0 };
  }
}
__name(executeWithConfiguredShell, "executeWithConfiguredShell");
function executeWithDefaultShell(command) {
  try {
    const output = execSync(command, {
      encoding: "utf-8",
      timeout: 1e4,
      stdio: ["ignore", "pipe", "ignore"]
    });
    return output.trim() || void 0;
  } catch {
    return void 0;
  }
}
__name(executeWithDefaultShell, "executeWithDefaultShell");
function executeCommandUncached(commandConfig) {
  const command = commandConfig.slice(1);
  return process.platform === "win32" ? (() => {
    const configuredResult = executeWithConfiguredShell(command);
    return configuredResult.executed ? configuredResult.value : executeWithDefaultShell(command);
  })() : executeWithDefaultShell(command);
}
__name(executeCommandUncached, "executeCommandUncached");
function executeCommand(commandConfig) {
  if (commandResultCache.has(commandConfig)) {
    return commandResultCache.get(commandConfig);
  }
  const result = executeCommandUncached(commandConfig);
  commandResultCache.set(commandConfig, result);
  return result;
}
__name(executeCommand, "executeCommand");
function resolveConfigValueUncached(config, env) {
  const reference = parseConfigValueReference(config);
  if (reference.type === "command") {
    return executeCommandUncached(reference.config);
  }
  return resolveTemplate(reference.parts, env);
}
__name(resolveConfigValueUncached, "resolveConfigValueUncached");
function resolveConfigValueOrThrow(config, description, env) {
  const resolvedValue = resolveConfigValueUncached(config, env);
  if (resolvedValue !== void 0) {
    return resolvedValue;
  }
  const reference = parseConfigValueReference(config);
  if (reference.type === "command") {
    throw new Error(`Failed to resolve ${description} from shell command: ${reference.config.slice(1)}`);
  }
  if (reference.type === "template") {
    const missingEnvVars = getMissingConfigValueEnvVarNames(config, env);
    if (missingEnvVars.length === 1) {
      throw new Error(`Failed to resolve ${description} from environment variable: ${missingEnvVars[0]}`);
    }
    if (missingEnvVars.length > 1) {
      throw new Error(`Failed to resolve ${description} from environment variables: ${missingEnvVars.join(", ")}`);
    }
  }
  throw new Error(`Failed to resolve ${description}`);
}
__name(resolveConfigValueOrThrow, "resolveConfigValueOrThrow");
function resolveHeadersOrThrow(headers, description, env) {
  if (!headers)
    return void 0;
  const resolved = {};
  for (const [key, value] of Object.entries(headers)) {
    resolved[key] = resolveConfigValueOrThrow(value, `${description} header "${key}"`, env);
  }
  return Object.keys(resolved).length > 0 ? resolved : void 0;
}
__name(resolveHeadersOrThrow, "resolveHeadersOrThrow");

// pi-dist/pi-coding-agent/core/provider-composer.js
function mergeCompat(base, override) {
  if (!override)
    return base;
  const merged = { ...base, ...override };
  const baseNested = base;
  const overrideNested = override;
  const mergedNested = merged;
  for (const key of ["openRouterRouting", "vercelGatewayRouting", "chatTemplateKwargs", "chatTemplateArgs"]) {
    const baseValue = baseNested?.[key];
    const overrideValue = overrideNested[key];
    if (typeof baseValue === "object" && baseValue !== null || typeof overrideValue === "object" && overrideValue !== null) {
      mergedNested[key] = { ...baseValue, ...overrideValue };
    }
  }
  return merged;
}
__name(mergeCompat, "mergeCompat");
function mergeInputLimits(base, override) {
  if (!override)
    return base;
  return {
    ...base,
    ...override,
    images: override.images ? {
      ...base?.images,
      ...override.images,
      resize: override.images.resize ? { ...base?.images?.resize, ...override.images.resize } : base?.images?.resize
    } : base?.images
  };
}
__name(mergeInputLimits, "mergeInputLimits");
function applyModelOverride(model, override) {
  return {
    ...model,
    name: override.name ?? model.name,
    reasoning: override.reasoning ?? model.reasoning,
    thinkingLevelMap: override.thinkingLevelMap ? { ...model.thinkingLevelMap, ...override.thinkingLevelMap } : model.thinkingLevelMap,
    input: override.input ?? model.input,
    inputLimits: mergeInputLimits(model.inputLimits, override.inputLimits),
    cost: override.cost ? {
      input: override.cost.input ?? model.cost.input,
      output: override.cost.output ?? model.cost.output,
      cacheRead: override.cost.cacheRead ?? model.cost.cacheRead,
      cacheWrite: override.cost.cacheWrite ?? model.cost.cacheWrite,
      tiers: override.cost.tiers ?? model.cost.tiers
    } : model.cost,
    promptCache: override.promptCache ? { ...model.promptCache, ...override.promptCache } : model.promptCache,
    contextWindow: override.contextWindow ?? model.contextWindow,
    maxTokens: override.maxTokens ?? model.maxTokens,
    samplingParams: override.samplingParams ? { ...model.samplingParams, ...override.samplingParams } : model.samplingParams,
    compat: mergeCompat(model.compat, override.compat)
  };
}
__name(applyModelOverride, "applyModelOverride");
function modelFromJson(providerId, definition, providerConfig, defaults) {
  const api = definition.api ?? providerConfig.api ?? defaults?.api;
  if (!api) {
    throw new Error(`Provider ${providerId}, model ${definition.id}: no "api" specified. Set at provider or model level.`);
  }
  const baseUrl = definition.baseUrl ?? providerConfig.baseUrl ?? defaults?.baseUrl;
  if (!baseUrl)
    throw new Error(`Provider ${providerId}: "baseUrl" is required when defining custom models.`);
  if (definition.contextWindow !== void 0 && definition.contextWindow <= 0) {
    throw new Error(`Provider ${providerId}, model ${definition.id}: invalid contextWindow`);
  }
  if (definition.maxTokens !== void 0 && definition.maxTokens <= 0) {
    throw new Error(`Provider ${providerId}, model ${definition.id}: invalid maxTokens`);
  }
  return {
    id: definition.id,
    name: definition.name ?? definition.id,
    api,
    provider: providerId,
    baseUrl,
    reasoning: definition.reasoning ?? false,
    thinkingLevelMap: definition.thinkingLevelMap,
    input: definition.input ?? ["text"],
    inputLimits: definition.inputLimits,
    cost: definition.cost ?? { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    promptCache: definition.promptCache,
    contextWindow: definition.contextWindow ?? 128e3,
    maxTokens: definition.maxTokens ?? 16384,
    samplingParams: definition.samplingParams,
    headers: void 0,
    compat: mergeCompat(providerConfig.compat, definition.compat)
  };
}
__name(modelFromJson, "modelFromJson");
function findModelDefaults(models, modelId, api) {
  return models.find((model) => model.id === modelId) ?? (api ? models.find((model) => model.api === api) : void 0) ?? models.find((model) => model.api === "openai-completions") ?? models[0];
}
__name(findModelDefaults, "findModelDefaults");
function applyModelsJson(providerId, baseModels, config) {
  if (!config)
    return [...baseModels];
  if (config.oauth && !config.baseUrl) {
    throw new Error(`Provider ${providerId}: "baseUrl" is required when "oauth" is set.`);
  }
  const hasOverrides = config.modelOverrides && Object.keys(config.modelOverrides).length > 0;
  if (!config.models?.length && !config.baseUrl && !config.headers && !config.compat && !hasOverrides && !config.apiKey && !config.oauth && config.authHeader === void 0) {
    throw new Error(`Provider ${providerId}: must specify "baseUrl", "headers", "compat", "modelOverrides", or "models".`);
  }
  const models = baseModels.map((model) => ({
    ...model,
    baseUrl: config.oauth === "radius" ? model.baseUrl : config.baseUrl ?? model.baseUrl,
    compat: mergeCompat(model.compat, config.compat)
  }));
  for (const definition of config.models ?? []) {
    const existingIndex = models.findIndex((model2) => model2.id === definition.id);
    const defaults = findModelDefaults(models, definition.id, definition.api ?? config.api);
    const model = modelFromJson(providerId, definition, config, defaults);
    if (existingIndex >= 0)
      models[existingIndex] = model;
    else
      models.push(model);
  }
  return models;
}
__name(applyModelsJson, "applyModelsJson");
function applyExtension(providerId, models, config) {
  if (!config)
    return [...models];
  if (!config.models) {
    return config.baseUrl ? models.map((model) => ({ ...model, baseUrl: config.baseUrl })) : [...models];
  }
  return config.models.map((definition) => {
    const defaults = findModelDefaults(models, definition.id, definition.api ?? config.api);
    const api = definition.api ?? config.api ?? defaults?.api;
    if (!api) {
      throw new Error(`Provider ${providerId}, model ${definition.id}: no "api" specified. Set at provider or model level.`);
    }
    const baseUrl = definition.baseUrl ?? config.baseUrl ?? defaults?.baseUrl;
    if (!baseUrl)
      throw new Error(`Provider ${providerId}: "baseUrl" is required when defining custom models.`);
    return {
      ...definition,
      api,
      provider: providerId,
      baseUrl,
      headers: void 0
    };
  });
}
__name(applyExtension, "applyExtension");
function adaptOAuth(config) {
  return {
    name: config.name,
    isSubscription: config.isSubscription,
    login: /* @__PURE__ */ __name(async (callbacks) => {
      const credential = await config.login({
        onAuth: /* @__PURE__ */ __name((info) => callbacks.notify({ type: "auth_url", ...info }), "onAuth"),
        onDeviceCode: /* @__PURE__ */ __name((info) => callbacks.notify({ type: "device_code", ...info }), "onDeviceCode"),
        onPrompt: /* @__PURE__ */ __name((prompt) => callbacks.prompt({ type: "text", ...prompt }), "onPrompt"),
        onProgress: /* @__PURE__ */ __name((message) => callbacks.notify({ type: "progress", message }), "onProgress"),
        onManualCodeInput: /* @__PURE__ */ __name(() => callbacks.prompt({ type: "manual_code", message: "Paste the authorization code" }), "onManualCodeInput"),
        onSelect: /* @__PURE__ */ __name((prompt) => callbacks.prompt({ type: "select", ...prompt }), "onSelect"),
        signal: callbacks.signal
      });
      return { ...credential, type: "oauth" };
    }, "login"),
    refresh: /* @__PURE__ */ __name(async (credential, signal) => ({ ...await config.refreshToken(credential, signal), type: "oauth" }), "refresh"),
    toAuth: /* @__PURE__ */ __name(async (credential) => ({ apiKey: config.getApiKey(credential) }), "toAuth")
  };
}
__name(adaptOAuth, "adaptOAuth");
function withConfiguredAuth(auth, headers, authHeader) {
  let mergedHeaders = auth.headers || headers ? { ...auth.headers, ...headers } : void 0;
  if (authHeader) {
    if (!auth.apiKey)
      throw new Error("authHeader requires a resolved API key");
    mergedHeaders = { ...mergedHeaders, Authorization: `Bearer ${auth.apiKey}` };
  }
  return { ...auth, headers: mergedHeaders };
}
__name(withConfiguredAuth, "withConfiguredAuth");
function configuredApiKey(config, extension) {
  return extension?.apiKey ?? config?.apiKey;
}
__name(configuredApiKey, "configuredApiKey");
function configuredHeaders(config, extension) {
  if (!config?.headers && !extension?.headers)
    return void 0;
  return { ...config?.headers, ...extension?.headers };
}
__name(configuredHeaders, "configuredHeaders");
async function configContextEnv(values, ctx, explicit) {
  const env = { ...explicit };
  for (const name of new Set(values.flatMap(getConfigValueEnvVarNames))) {
    if (env[name] !== void 0)
      continue;
    const value = await ctx.env(name);
    if (value !== void 0)
      env[name] = value;
  }
  return Object.keys(env).length > 0 ? env : void 0;
}
__name(configContextEnv, "configContextEnv");
function composeApiKeyAuth(providerId, base, config, extension) {
  const inherited = base?.auth.apiKey;
  const rawKey = configuredApiKey(config, extension);
  const oauth = extension?.oauth ?? base?.auth.oauth;
  if (!inherited && rawKey === void 0 && oauth)
    return void 0;
  const rawHeaders = configuredHeaders(config, extension);
  const authHeader = extension?.authHeader ?? config?.authHeader ?? false;
  return {
    name: inherited?.name ?? "API key",
    login: inherited?.login ?? (async (interaction) => ({
      type: "api_key",
      key: await interaction.prompt({ type: "secret", message: "Enter API key" })
    })),
    check: /* @__PURE__ */ __name(async (input) => {
      if (input.credential) {
        if (inherited?.check)
          return inherited.check(input);
        if (input.credential.key)
          return { type: "api_key", source: "stored credential" };
        const resolved2 = await inherited?.resolve(input);
        return resolved2 ? { type: "api_key", source: resolved2.source } : void 0;
      }
      if (rawKey !== void 0) {
        if (isCommandConfigValue(rawKey))
          return { type: "api_key", source: "configured API key" };
        const envNames = getConfigValueEnvVarNames(rawKey);
        for (const name of envNames) {
          if (await input.ctx.env(name) === void 0)
            return void 0;
        }
        return { type: "api_key", source: "configured API key" };
      }
      if (inherited?.check)
        return inherited.check(input);
      const resolved = await inherited?.resolve(input);
      return resolved ? { type: "api_key", source: resolved.source } : void 0;
    }, "check"),
    resolve: /* @__PURE__ */ __name(async (input) => {
      let result;
      if (input.credential) {
        result = inherited ? await inherited.resolve(input) : input.credential.key ? { auth: { apiKey: input.credential.key }, env: input.credential.env, source: "stored credential" } : void 0;
      } else if (rawKey !== void 0) {
        const env = await configContextEnv([rawKey], input.ctx);
        const key = resolveConfigValueOrThrow(rawKey, `API key for provider "${providerId}"`, env);
        result = inherited ? await inherited.resolve({ ...input, credential: { type: "api_key", key } }) : { auth: { apiKey: key }, source: "configured API key" };
      } else {
        result = await inherited?.resolve(input);
      }
      if (!result)
        return void 0;
      const explicitEnv = { ...input.credential?.env ?? {}, ...result.env ?? {} };
      const headerEnv = await configContextEnv(Object.values(rawHeaders ?? {}), input.ctx, explicitEnv);
      const headers = resolveHeadersOrThrow(rawHeaders, `provider "${providerId}"`, headerEnv);
      return { ...result, auth: withConfiguredAuth(result.auth, headers, authHeader) };
    }, "resolve")
  };
}
__name(composeApiKeyAuth, "composeApiKeyAuth");
function composeOAuthAuth(providerId, base, config, extension) {
  const oauth = extension?.oauth ? adaptOAuth(extension.oauth) : base?.auth.oauth;
  if (!oauth)
    return void 0;
  const rawHeaders = configuredHeaders(config, extension);
  const authHeader = extension?.authHeader ?? config?.authHeader ?? false;
  return {
    ...oauth,
    toAuth: /* @__PURE__ */ __name(async (credential) => {
      const auth = await oauth.toAuth(credential);
      const env = credential.env;
      const headers = resolveHeadersOrThrow(rawHeaders, `provider "${providerId}"`, typeof env === "object" && env !== null ? env : void 0);
      return withConfiguredAuth(auth, headers, authHeader);
    }, "toAuth")
  };
}
__name(composeOAuthAuth, "composeOAuthAuth");
function rawModelHeaders(model, config, extension) {
  const definition = config?.models?.find((entry) => entry.id === model.id);
  const extensionModel = extension?.models?.find((entry) => entry.id === model.id);
  const headers = {
    ...config?.modelOverrides?.[model.id]?.headers,
    ...definition?.headers,
    ...extensionModel?.headers
  };
  return Object.keys(headers).length > 0 ? headers : void 0;
}
__name(rawModelHeaders, "rawModelHeaders");
function validateExtensionProvider(providerId, base, modelsConfig, extension) {
  if (extension.streamSimple && !extension.api) {
    throw new Error(`Provider ${providerId}: "api" is required when registering streamSimple.`);
  }
  applyExtension(providerId, applyModelsJson(providerId, base?.getModels() ?? [], modelsConfig), extension);
}
__name(validateExtensionProvider, "validateExtensionProvider");
function composeModelProvider(providerId, base, modelConfig, extension) {
  const config = modelConfig.getProvider(providerId);
  let extensionOAuthCredential;
  let refreshedExtensionModels;
  const currentExtension = /* @__PURE__ */ __name(() => extension && refreshedExtensionModels ? { ...extension, models: refreshedExtensionModels } : extension, "currentExtension");
  const getModels = /* @__PURE__ */ __name(() => {
    let models = applyExtension(providerId, applyModelsJson(providerId, base?.getModels() ?? [], config), currentExtension());
    if (extensionOAuthCredential && extension?.oauth?.modifyModels) {
      models = extension.oauth.modifyModels(models, extensionOAuthCredential);
    }
    return models.map((model) => {
      const override = config?.modelOverrides?.[model.id];
      return override ? applyModelOverride(model, override) : model;
    });
  }, "getModels");
  getModels();
  const apiKey = composeApiKeyAuth(providerId, base, config, extension);
  const oauth = composeOAuthAuth(providerId, base, config, extension);
  if (!apiKey && !oauth)
    throw new Error(`Provider ${providerId}: no authentication method configured.`);
  const supportsBaseApi = /* @__PURE__ */ __name((model) => base?.getModels().some((entry) => entry.api === model.api) ?? false, "supportsBaseApi");
  const streamWith = /* @__PURE__ */ __name((model, context, options, simple) => lazyStream(model, async () => {
    if (extension?.streamSimple && model.api === extension.api) {
      return extension.streamSimple(model, context, options);
    }
    if (base && supportsBaseApi(model)) {
      return simple ? base.streamSimple(model, context, options) : base.stream(model, context, options);
    }
    const api = getApiProvider(model.api);
    if (!api)
      throw new Error(`No API provider registered for api: ${model.api}`);
    return simple ? api.streamSimple(model, context, options) : api.stream(model, context, options);
  }), "streamWith");
  const provider = {
    id: providerId,
    name: extension?.name ?? config?.name ?? base?.name ?? extension?.oauth?.name ?? providerId,
    baseUrl: extension?.baseUrl ?? config?.baseUrl ?? base?.baseUrl,
    headers: base?.headers,
    auth: { ...apiKey ? { apiKey } : {}, ...oauth ? { oauth } : {} },
    getModels,
    refreshModels: base?.refreshModels || extension?.refreshModels || extension?.oauth?.modifyModels ? async (context) => {
      await base?.refreshModels?.(context);
      let refreshed;
      if (extension?.refreshModels)
        refreshed = await extension.refreshModels(context);
      if (context.signal.aborted)
        return;
      const oauthCredential = context.credential?.type === "oauth" ? context.credential : void 0;
      await context.publish({
        update: /* @__PURE__ */ __name(() => {
          if (refreshed) {
            applyExtension(providerId, applyModelsJson(providerId, base?.getModels() ?? [], config), {
              ...extension,
              models: refreshed
            });
            refreshedExtensionModels = refreshed;
          }
          extensionOAuthCredential = oauthCredential;
        }, "update")
      });
    } : void 0,
    filterModels: base?.filterModels ? (models, credential) => base.filterModels(models, credential) : void 0,
    stream: /* @__PURE__ */ __name((model, context, options) => streamWith(model, context, options, false), "stream"),
    streamSimple: /* @__PURE__ */ __name((model, context, options) => streamWith(model, context, options, true), "streamSimple")
  };
  const fetchDeferred = base?.fetchDeferred;
  if (fetchDeferred) {
    provider.fetchDeferred = (model, handle, options) => fetchDeferred(model, handle, options);
  }
  const cancelDeferred = base?.cancelDeferred;
  if (cancelDeferred) {
    provider.cancelDeferred = (model, handle, options) => cancelDeferred(model, handle, options);
  }
  return provider;
}
__name(composeModelProvider, "composeModelProvider");
function resolveConfiguredModelHeaders(model, config, extension, env) {
  return resolveHeadersOrThrow(rawModelHeaders(model, config, extension), `model "${model.provider}/${model.id}"`, env);
}
__name(resolveConfiguredModelHeaders, "resolveConfiguredModelHeaders");
function resolveCompatibilityRequestConfig(model, config, extension) {
  const configured = resolveHeadersOrThrow({ ...configuredHeaders(config, extension), ...rawModelHeaders(model, config, extension) }, `model "${model.provider}/${model.id}"`);
  return {
    headers: model.headers || configured ? { ...model.headers, ...configured } : void 0,
    authHeader: extension?.authHeader ?? config?.authHeader ?? false
  };
}
__name(resolveCompatibilityRequestConfig, "resolveCompatibilityRequestConfig");
function configuredRequestAuthStatus(config, extension) {
  const value = configuredApiKey(config, extension);
  if (value === void 0)
    return void 0;
  if (isCommandConfigValue(value))
    return { configured: true, source: "models_json_command" };
  const names = getConfigValueEnvVarNames(value);
  if (names.length > 0) {
    return isConfigValueConfigured(value) ? { configured: true, source: "environment", label: names.join(", ") } : { configured: false };
  }
  return { configured: true, source: extension?.apiKey !== void 0 ? "fallback" : "models_json_key" };
}
__name(configuredRequestAuthStatus, "configuredRequestAuthStatus");

// pi-dist/pi-coding-agent/core/model-registry.js
var ModelRegistry = class {
  static {
    __name(this, "ModelRegistry");
  }
  runtime;
  constructor(runtime) {
    this.runtime = runtime;
  }
  /** Reload models.json asynchronously. Await before making synchronous registry reads. */
  refresh(options) {
    return this.runtime.refresh(options);
  }
  getError() {
    return this.runtime.getError();
  }
  getAll() {
    return [...this.runtime.getModels()];
  }
  getAvailable() {
    return [...this.runtime.getAvailableSnapshot()];
  }
  find(provider, modelId) {
    return this.runtime.getModel(provider, modelId);
  }
  hasConfiguredAuth(model) {
    return this.runtime.hasConfiguredAuth(model.provider);
  }
  async getApiKeyAndHeaders(model) {
    try {
      const resolution = await this.runtime.getAuth(model);
      if (!resolution) {
        const compatibility = this.runtime.getCompatibilityRequestConfig(model);
        if (compatibility.authHeader) {
          return { ok: false, error: `No API key found for "${model.provider}"` };
        }
        return { ok: true, headers: compatibility.headers };
      }
      return {
        ok: true,
        apiKey: resolution.auth.apiKey,
        headers: resolution.auth.headers,
        ...resolution.auth.baseUrl ? { baseUrl: resolution.auth.baseUrl } : {},
        env: resolution.env
      };
    } catch (error) {
      const cause = error instanceof Error ? error.cause : void 0;
      const message = cause instanceof Error ? cause.message : error instanceof Error ? error.message : String(error);
      return {
        ok: false,
        error: message === "authHeader requires a resolved API key" ? `No API key found for "${model.provider}"` : message
      };
    }
  }
  getProviderAuthStatus(provider) {
    return this.runtime.getProviderAuthStatus(provider);
  }
  getProvider(provider) {
    return this.runtime.getProvider(provider);
  }
  /** Stream through the configured provider with request-time authentication. */
  stream(model, context, options) {
    return this.runtime.stream(model, context, options);
  }
  /** Stream with provider-neutral options and request-time authentication. */
  streamSimple(model, context, options) {
    return this.runtime.streamSimple(model, context, options);
  }
  complete(model, context, options) {
    return this.runtime.complete(model, context, options);
  }
  getProviderDisplayName(provider) {
    return this.runtime.getProvider(provider)?.name ?? provider;
  }
  getProviderAuth(provider) {
    return this.runtime.getAuth(provider);
  }
  async getApiKeyForProvider(provider) {
    try {
      return (await this.runtime.getAuth(provider))?.auth.apiKey;
    } catch {
      return void 0;
    }
  }
  isUsingOAuth(model) {
    return this.runtime.isUsingOAuth(model.provider);
  }
  registerProvider(providerOrName, config) {
    if (typeof providerOrName === "string") {
      if (!config)
        throw new Error("Provider config is required when registering by name");
      this.runtime.registerProvider(providerOrName, config);
      return;
    }
    this.runtime.registerNativeProvider(providerOrName);
  }
  unregisterProvider(providerName) {
    this.runtime.unregisterProvider(providerName);
  }
  getRegisteredProviderConfig(providerName) {
    return this.runtime.getRegisteredProviderConfig(providerName);
  }
  getRegisteredNativeProvider(providerName) {
    return this.runtime.getRegisteredNativeProvider(providerName);
  }
  getRegisteredProviderIds() {
    return this.runtime.getRegisteredProviderIds();
  }
};

// pi-dist/pi-coding-agent/core/agent-session.js
import { readFileSync as readFileSync5 } from "node:fs";
import { basename as basename5, dirname as dirname5 } from "node:path";
import { contentText as contentText5, getCurrentSystemMessage as getCurrentSystemMessage4, retryDelayMs } from "../../pi-ai/sdk-bundle/index.js";
import { clampThinkingLevel, cleanupSessionResources, getSupportedThinkingLevels, isContextOverflow, isRecoverableLength, isRetryableAssistantError, modelsAreEqual, resetApiProviders, streamSimple } from "../../pi-ai/sdk-bundle/compat.js";

// pi-dist/pi-coding-agent/utils/sleep.js
function sleep(ms, signal) {
  return new Promise((resolve9, reject) => {
    if (signal?.aborted) {
      reject(new Error("Aborted"));
      return;
    }
    const timeout = setTimeout(resolve9, ms);
    signal?.addEventListener("abort", () => {
      clearTimeout(timeout);
      reject(new Error("Aborted"));
    });
  });
}
__name(sleep, "sleep");

// pi-dist/pi-coding-agent/utils/tool-result-images.js
async function normalizeToolResultImages(content, options) {
  if (!content.some((block) => block.type === "image")) {
    return content;
  }
  const autoResizeImages = options?.autoResizeImages ?? true;
  const normalized = [];
  let changed = false;
  for (const block of content) {
    if (block.type !== "image") {
      normalized.push(block);
      continue;
    }
    const processed = await processImage(Buffer.from(block.data, "base64"), block.mimeType, {
      autoResizeImages,
      resizeOptions: options?.resizeOptions
    });
    if (!processed.ok) {
      normalized.push(block);
      continue;
    }
    if (processed.data === block.data && processed.mimeType === block.mimeType && processed.hints.length === 0) {
      normalized.push(block);
      continue;
    }
    normalized.push({ type: "image", data: processed.data, mimeType: processed.mimeType });
    if (processed.hints.length > 0) {
      normalized.push({ type: "text", text: processed.hints.join("\n") });
    }
    changed = true;
  }
  return changed ? normalized : content;
}
__name(normalizeToolResultImages, "normalizeToolResultImages");

// pi-dist/pi-coding-agent/core/bash-executor.js
import { randomBytes } from "node:crypto";
import { createWriteStream } from "node:fs";
import { tmpdir } from "node:os";
import { join as join5 } from "node:path";
async function executeBashWithOperations(command, cwd, operations, options) {
  const outputChunks = [];
  let outputBytes = 0;
  const maxOutputBytes = DEFAULT_MAX_BYTES * 2;
  let tempFilePath;
  let tempFileStream;
  let totalBytes = 0;
  const ensureTempFile = /* @__PURE__ */ __name(() => {
    if (tempFilePath) {
      return;
    }
    const id = randomBytes(8).toString("hex");
    tempFilePath = join5(tmpdir(), `pi-bash-${id}.log`);
    tempFileStream = createWriteStream(tempFilePath);
    for (const chunk of outputChunks) {
      tempFileStream.write(chunk);
    }
  }, "ensureTempFile");
  const decoder = new TextDecoder();
  const onData = /* @__PURE__ */ __name((data) => {
    totalBytes += data.length;
    const text = sanitizeBinaryOutput(stripAnsi(decoder.decode(data, { stream: true }))).replace(/\r/g, "");
    if (totalBytes > DEFAULT_MAX_BYTES) {
      ensureTempFile();
    }
    if (tempFileStream) {
      tempFileStream.write(text);
    }
    outputChunks.push(text);
    outputBytes += text.length;
    while (outputBytes > maxOutputBytes && outputChunks.length > 1) {
      const removed = outputChunks.shift();
      outputBytes -= removed.length;
    }
    if (options?.onChunk) {
      options.onChunk(text);
    }
  }, "onData");
  try {
    const result = await operations.exec(command, cwd, {
      onData,
      signal: options?.signal
    });
    const fullOutput = outputChunks.join("");
    const truncationResult = truncateTail(fullOutput);
    if (truncationResult.truncated) {
      ensureTempFile();
    }
    if (tempFileStream) {
      tempFileStream.end();
    }
    const cancelled = options?.signal?.aborted ?? false;
    return {
      output: truncationResult.truncated ? truncationResult.content : fullOutput,
      exitCode: cancelled ? void 0 : result.exitCode ?? void 0,
      cancelled,
      truncated: truncationResult.truncated,
      fullOutputPath: tempFilePath
    };
  } catch (err) {
    if (options?.signal?.aborted) {
      const fullOutput = outputChunks.join("");
      const truncationResult = truncateTail(fullOutput);
      if (truncationResult.truncated) {
        ensureTempFile();
      }
      if (tempFileStream) {
        tempFileStream.end();
      }
      return {
        output: truncationResult.truncated ? truncationResult.content : fullOutput,
        exitCode: void 0,
        cancelled: true,
        truncated: truncationResult.truncated,
        fullOutputPath: tempFilePath
      };
    }
    if (tempFileStream) {
      tempFileStream.end();
    }
    throw err;
  }
}
__name(executeBashWithOperations, "executeBashWithOperations");

// pi-dist/pi-coding-agent/core/bug-report.js
import * as os from "node:os";
import { contentText as contentText4, normalizeContext as normalizeContext3, uuidv7 as uuidv73 } from "../../pi-ai/sdk-bundle/index.js";

// pi-dist/pi-coding-agent/utils/pi-user-agent.js
import { pigUserAgent } from "../../../pig-identity.mjs";
function getPiUserAgent(version2) {
  return pigUserAgent("pig");
}
__name(getPiUserAgent, "getPiUserAgent");

// pi-dist/pi-coding-agent/utils/zip.js
import { writeFile } from "node:fs/promises";
import { crc32, deflateRawSync } from "node:zlib";
function dosDateTime(date) {
  return {
    time: date.getHours() << 11 | date.getMinutes() << 5 | date.getSeconds() >> 1,
    day: Math.max(1980, date.getFullYear()) - 1980 << 9 | date.getMonth() + 1 << 5 | date.getDate()
  };
}
__name(dosDateTime, "dosDateTime");
function createZipArchive(entries) {
  const files = [];
  const directory = [];
  const { time: time2, day } = dosDateTime(/* @__PURE__ */ new Date());
  let offset = 0;
  for (const entry of entries) {
    const name = Buffer.from(entry.name);
    const data = typeof entry.data === "string" ? Buffer.from(entry.data) : Buffer.from(entry.data);
    const compressed = deflateRawSync(data);
    const checksum = crc32(data);
    const local = Buffer.alloc(30);
    local.writeUInt32LE(67324752, 0);
    local.writeUInt16LE(20, 4);
    local.writeUInt16LE(2048, 6);
    local.writeUInt16LE(8, 8);
    local.writeUInt16LE(time2, 10);
    local.writeUInt16LE(day, 12);
    local.writeUInt32LE(checksum, 14);
    local.writeUInt32LE(compressed.length, 18);
    local.writeUInt32LE(data.length, 22);
    local.writeUInt16LE(name.length, 26);
    const central = Buffer.alloc(46);
    central.writeUInt32LE(33639248, 0);
    central.writeUInt16LE(20, 4);
    central.writeUInt16LE(20, 6);
    central.writeUInt16LE(2048, 8);
    central.writeUInt16LE(8, 10);
    central.writeUInt16LE(time2, 12);
    central.writeUInt16LE(day, 14);
    central.writeUInt32LE(checksum, 16);
    central.writeUInt32LE(compressed.length, 20);
    central.writeUInt32LE(data.length, 24);
    central.writeUInt16LE(name.length, 28);
    central.writeUInt32LE(offset, 42);
    files.push(local, name, compressed);
    directory.push(central, name);
    offset += local.length + name.length + compressed.length;
  }
  const centralDirectory = Buffer.concat(directory);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(101010256, 0);
  end.writeUInt16LE(entries.length, 8);
  end.writeUInt16LE(entries.length, 10);
  end.writeUInt32LE(centralDirectory.length, 12);
  end.writeUInt32LE(offset, 16);
  return Buffer.concat([...files, centralDirectory, end]);
}
__name(createZipArchive, "createZipArchive");
function writeZipArchive(filePath, entries) {
  return writeFile(filePath, createZipArchive(entries));
}
__name(writeZipArchive, "writeZipArchive");

// pi-dist/pi-coding-agent/core/bug-report.js
var BUG_REPORT_CUSTOM_ENTRY_TYPE = "pi.bug-report";
var BUG_REPORT_SCHEMA_VERSION = 1;
var REDACTED = "<redacted>";
var SENSITIVE_KEY = /(?:^|[-_])(api[-_]?key|secret|token|password|passwd|credential|authorization|cookie)(?:$|[-_])/i;
function isSensitiveKey(key) {
  return SENSITIVE_KEY.test(key.replace(/([a-z0-9])([A-Z])/g, "$1_$2"));
}
__name(isSensitiveKey, "isSensitiveKey");
function redactUrl(value) {
  const nested = /^([a-z][a-z0-9+.-]*:)([a-z][a-z0-9+.-]*:\/\/.*)$/i.exec(value);
  if (nested)
    return `${nested[1]}${redactUrl(nested[2])}`;
  try {
    const url = new URL(value);
    let changed = false;
    if (url.username || url.password) {
      url.username = "";
      url.password = "";
      changed = true;
    }
    for (const key of url.searchParams.keys()) {
      if (isSensitiveKey(key)) {
        url.searchParams.set(key, REDACTED);
        changed = true;
      }
    }
    return changed ? url.toString() : value;
  } catch {
    return value;
  }
}
__name(redactUrl, "redactUrl");
function redactJsonValue(value) {
  if (value === void 0)
    return void 0;
  return JSON.parse(JSON.stringify(value, (key, child) => {
    if (child !== null && child !== void 0 && isSensitiveKey(key))
      return REDACTED;
    return typeof child === "string" ? redactUrl(child) : child;
  }));
}
__name(redactJsonValue, "redactJsonValue");
function redactSettings(settings) {
  const { trackingId: _trackingId, ...rest } = settings;
  return redactJsonValue(rest);
}
__name(redactSettings, "redactSettings");
function collectEnvironment() {
  const env = /* @__PURE__ */ __name((name) => process.env[name] || null, "env");
  return {
    version: VERSION,
    userAgent: getPiUserAgent(VERSION),
    runtime: process.versions.bun ? `bun/${process.versions.bun}` : `node/${process.version}`,
    platform: process.platform,
    arch: process.arch,
    osRelease: os.release(),
    osVersion: os.version(),
    shell: process.env.SHELL?.split(/[\\/]/).pop() || null,
    terminal: {
      term: env("TERM"),
      program: env("TERM_PROGRAM"),
      programVersion: env("TERM_PROGRAM_VERSION"),
      colorterm: env("COLORTERM"),
      tmux: Boolean(process.env.TMUX),
      ssh: Boolean(process.env.SSH_CONNECTION || process.env.SSH_CLIENT || process.env.SSH_TTY),
      ci: Boolean(process.env.CI)
    },
    // Names help diagnose configuration; values never leave the machine.
    piEnvironmentVariables: Object.keys(process.env).filter((name) => name.startsWith("PI_")).sort()
  };
}
__name(collectEnvironment, "collectEnvironment");
function describeModel(model) {
  return {
    provider: model.provider,
    id: model.id,
    name: model.name,
    api: model.api,
    baseUrl: redactUrl(model.baseUrl),
    reasoning: model.reasoning,
    input: model.input,
    contextWindow: model.contextWindow,
    maxTokens: model.maxTokens,
    samplingParams: model.samplingParams ? redactJsonValue(model.samplingParams) : null,
    compat: model.compat ? redactJsonValue(model.compat) : null,
    thinkingLevelMap: model.thinkingLevelMap ?? null,
    headerNames: Object.keys(model.headers ?? {}).sort()
  };
}
__name(describeModel, "describeModel");
function describeProvider(modelRuntime, provider) {
  const authTypes = [];
  if (provider.auth.apiKey)
    authTypes.push("api_key");
  if (provider.auth.oauth)
    authTypes.push("oauth");
  return {
    id: provider.id,
    name: provider.name,
    baseUrl: provider.baseUrl ? redactUrl(provider.baseUrl) : null,
    headerNames: Object.keys(provider.headers ?? {}).sort(),
    authTypes,
    authStatus: modelRuntime.getProviderAuthStatus(provider.id),
    usingOAuth: modelRuntime.isUsingOAuth(provider.id),
    registeredByExtension: modelRuntime.getRegisteredProviderIds().includes(provider.id)
  };
}
__name(describeProvider, "describeProvider");
function describeExtension(extension) {
  return {
    path: extension.path,
    source: redactUrl(extension.sourceInfo.source),
    scope: extension.sourceInfo.scope,
    origin: extension.sourceInfo.origin,
    hidden: extension.hidden === true
  };
}
__name(describeExtension, "describeExtension");
function collectBugReportMetadata(options) {
  const hint = options.hint?.trim() || null;
  const provider = options.model ? options.modelRuntime.getProvider(options.model.provider) : void 0;
  return {
    schemaVersion: BUG_REPORT_SCHEMA_VERSION,
    id: options.id ?? uuidv73(),
    createdAt: (/* @__PURE__ */ new Date()).toISOString(),
    hint,
    environment: collectEnvironment(),
    session: {
      id: options.sessionId,
      included: options.includeSession,
      summaryIncluded: options.includeSummary,
      messageCount: options.messageCount,
      ...options.includeSession ? { cwd: options.cwd } : {}
    },
    model: options.model ? describeModel(options.model) : null,
    provider: provider ? describeProvider(options.modelRuntime, provider) : null,
    thinkingLevel: options.thinkingLevel,
    extensions: options.extensions.map(describeExtension),
    extensionErrors: options.extensionErrors.map(({ path: path2, error }) => ({ path: path2, error })),
    settings: {
      global: redactSettings(options.globalSettings),
      project: redactSettings(options.projectSettings)
    }
  };
}
__name(collectBugReportMetadata, "collectBugReportMetadata");
function collectBugReportDiagnostics(sessionManager, crashes = []) {
  const entries = sessionManager.getEntries();
  const assistant = [];
  let assistantMessageCount = 0;
  for (const entry of entries) {
    if (entry.type !== "message" || entry.message.role !== "assistant")
      continue;
    assistantMessageCount++;
    const message = entry.message;
    const diagnostics = message.diagnostics ?? [];
    if (diagnostics.length === 0 && message.stopReason !== "error" && message.stopReason !== "aborted" && !message.errorMessage) {
      continue;
    }
    assistant.push({
      entryId: entry.id,
      timestamp: entry.timestamp,
      provider: message.provider,
      model: message.model,
      api: message.api,
      stopReason: message.stopReason,
      ...message.rawStopReason === void 0 ? {} : { rawStopReason: message.rawStopReason },
      ...message.errorMessage === void 0 ? {} : { errorMessage: message.errorMessage },
      diagnostics
    });
  }
  return {
    schemaVersion: BUG_REPORT_SCHEMA_VERSION,
    sessionId: sessionManager.getSessionId(),
    entryCount: entries.length,
    assistantMessageCount,
    assistant,
    crashes: crashes.map(({ notified: _notified, ...record }) => record)
  };
}
__name(collectBugReportDiagnostics, "collectBugReportDiagnostics");
function bugReportFiles(bundle) {
  const files = [
    { name: "report.json", contentType: "application/json", data: `${JSON.stringify(bundle.metadata, null, 2)}
` },
    {
      name: "diagnostics.json",
      contentType: "application/json",
      data: `${JSON.stringify(bundle.diagnostics, null, 2)}
`
    }
  ];
  if (bundle.sessionJsonl !== void 0) {
    files.push({ name: "session.jsonl", contentType: "application/x-ndjson", data: bundle.sessionJsonl });
  }
  if (bundle.summary !== void 0) {
    files.push({
      name: "summary.md",
      contentType: "text/markdown",
      data: bundle.summary.endsWith("\n") ? bundle.summary : `${bundle.summary}
`
    });
  }
  return files;
}
__name(bugReportFiles, "bugReportFiles");
function writeBugReportArchive(bundle, filePath) {
  return writeZipArchive(filePath, bugReportFiles(bundle));
}
__name(writeBugReportArchive, "writeBugReportArchive");
function bugReportArchiveFileName(id) {
  return `pi-bug-report-${id}.zip`;
}
__name(bugReportArchiveFileName, "bugReportArchiveFileName");
var BUG_SUMMARY_SYSTEM_PROMPT = `You are helping a user file a bug report about pi, the coding agent they are talking to. You will be shown the conversation transcript. Write a report for the pi developers describing what the user was doing and what went wrong.

Do NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the report.`;
var BUG_SUMMARY_INSTRUCTIONS = `Write the bug report in Markdown with these sections:

## What the user was doing
One short paragraph.

## What went wrong
Concrete description of the failure: wrong output, errors, hangs, tool failures, unexpected behavior. Quote error messages and tool output verbatim where they exist.

## Steps to reproduce
Numbered list, as specific as the transcript allows.

## Relevant details
Tool calls involved, files touched, model behavior, anything else that helps a developer reproduce or locate the problem.

Do not include file contents, secrets, or credentials from the transcript; refer to files by path only. Keep the report factual and concise.`;
function selectMessages(messages, tokenBudget) {
  const selected = [];
  let tokens = 0;
  for (let index = messages.length - 1; index >= 0; index--) {
    const message = messages[index];
    const next = estimateTokens(message);
    if (selected.length > 0 && tokens + next > tokenBudget)
      break;
    selected.push(message);
    tokens += next;
  }
  return selected.reverse();
}
__name(selectMessages, "selectMessages");
async function generateBugReportSummary(options) {
  const { model } = options;
  const contextWindow = model.contextWindow > 0 ? model.contextWindow : 128e3;
  const messages = selectMessages(options.messages, Math.floor(contextWindow * 0.6));
  const hint = options.hint?.trim();
  const prompt = [
    messages.length < options.messages.length ? `Note: only the last ${messages.length} of ${options.messages.length} messages are shown.` : void 0,
    `<conversation>
${serializeConversation(convertToLlm(messages))}
</conversation>`,
    hint ? `<user-report>
${hint}
</user-report>` : void 0,
    BUG_SUMMARY_INSTRUCTIONS
  ].filter((part) => part !== void 0).join("\n\n");
  const requestOptions = {
    maxTokens: Math.min(4096, model.maxTokens > 0 ? model.maxTokens : Number.POSITIVE_INFINITY),
    signal: options.signal,
    apiKey: options.apiKey,
    headers: options.headers,
    env: options.env,
    sessionId: options.sessionId,
    ...model.reasoning && options.thinkingLevel && options.thinkingLevel !== "off" ? { reasoning: options.thinkingLevel } : {}
  };
  const response = await completeSummarization(model, normalizeContext3({
    systemPrompt: BUG_SUMMARY_SYSTEM_PROMPT,
    messages: [{ role: "user", content: [{ type: "text", text: prompt }], timestamp: Date.now() }]
  }), requestOptions, options.streamFn, options.retry);
  if (response.stopReason === "aborted")
    throw new Error("Bug report summary was cancelled");
  const failure = getSummarizationFailure(response, "Bug report summary");
  if (failure)
    throw new Error(failure);
  if (response.content.some((block) => block.type === "toolCall")) {
    throw new Error("Bug report summary attempted to call a tool");
  }
  const text = contentText4(response.content).trim();
  if (!text)
    throw new Error("Bug report summary was empty");
  return text;
}
__name(generateBugReportSummary, "generateBugReportSummary");

// pi-dist/pi-coding-agent/core/export-html/index.js
import { existsSync as existsSync4, readFileSync as readFileSync3, writeFileSync as writeFileSync2 } from "fs";
import { basename as basename3, join as join6 } from "path";
function parseColor(color) {
  const hexMatch = color.match(/^#([0-9a-fA-F]{2})([0-9a-fA-F]{2})([0-9a-fA-F]{2})$/);
  if (hexMatch) {
    return {
      r: Number.parseInt(hexMatch[1], 16),
      g: Number.parseInt(hexMatch[2], 16),
      b: Number.parseInt(hexMatch[3], 16)
    };
  }
  const rgbMatch = color.match(/^rgb\s*\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*\)$/);
  if (rgbMatch) {
    return {
      r: Number.parseInt(rgbMatch[1], 10),
      g: Number.parseInt(rgbMatch[2], 10),
      b: Number.parseInt(rgbMatch[3], 10)
    };
  }
  return void 0;
}
__name(parseColor, "parseColor");
function getLuminance(r, g, b) {
  const toLinear = /* @__PURE__ */ __name((c) => {
    const s = c / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  }, "toLinear");
  return 0.2126 * toLinear(r) + 0.7152 * toLinear(g) + 0.0722 * toLinear(b);
}
__name(getLuminance, "getLuminance");
function adjustBrightness(color, factor) {
  const parsed = parseColor(color);
  if (!parsed)
    return color;
  const adjust = /* @__PURE__ */ __name((c) => Math.min(255, Math.max(0, Math.round(c * factor))), "adjust");
  return `rgb(${adjust(parsed.r)}, ${adjust(parsed.g)}, ${adjust(parsed.b)})`;
}
__name(adjustBrightness, "adjustBrightness");
function deriveExportColors(baseColor) {
  const parsed = parseColor(baseColor);
  if (!parsed) {
    return {
      pageBg: "rgb(24, 24, 30)",
      cardBg: "rgb(30, 30, 36)",
      infoBg: "rgb(60, 55, 40)"
    };
  }
  const luminance = getLuminance(parsed.r, parsed.g, parsed.b);
  const isLight = luminance > 0.5;
  if (isLight) {
    return {
      pageBg: adjustBrightness(baseColor, 0.96),
      cardBg: baseColor,
      infoBg: `rgb(${Math.min(255, parsed.r + 10)}, ${Math.min(255, parsed.g + 5)}, ${Math.max(0, parsed.b - 20)})`
    };
  }
  return {
    pageBg: adjustBrightness(baseColor, 0.7),
    cardBg: adjustBrightness(baseColor, 0.85),
    infoBg: `rgb(${Math.min(255, parsed.r + 20)}, ${Math.min(255, parsed.g + 15)}, ${parsed.b})`
  };
}
__name(deriveExportColors, "deriveExportColors");
function generateThemeVars(themeName) {
  const colors = getResolvedThemeColors(themeName);
  const lines = [];
  for (const [key, value] of Object.entries(colors)) {
    lines.push(`--${key}: ${value};`);
  }
  const themeExport = getThemeExportColors(themeName);
  const userMessageBg = colors.userMessageBg || "#343541";
  const derivedColors = deriveExportColors(userMessageBg);
  lines.push(`--exportPageBg: ${themeExport.pageBg ?? derivedColors.pageBg};`);
  lines.push(`--exportCardBg: ${themeExport.cardBg ?? derivedColors.cardBg};`);
  lines.push(`--exportInfoBg: ${themeExport.infoBg ?? derivedColors.infoBg};`);
  return lines.join("\n      ");
}
__name(generateThemeVars, "generateThemeVars");
function generateHtml(sessionData, themeName) {
  const templateDir = getExportTemplateDir();
  const template = readFileSync3(join6(templateDir, "template.html"), "utf-8");
  const templateCss = readFileSync3(join6(templateDir, "template.css"), "utf-8");
  const templateJs = readFileSync3(join6(templateDir, "template.js"), "utf-8");
  const markedJs = readFileSync3(join6(templateDir, "vendor", "marked.min.js"), "utf-8");
  const hljsJs = readFileSync3(join6(templateDir, "vendor", "highlight.min.js"), "utf-8");
  const themeVars = generateThemeVars(themeName);
  const colors = getResolvedThemeColors(themeName);
  const themeExport = getThemeExportColors(themeName);
  const derivedExportColors = deriveExportColors(colors.userMessageBg || "#343541");
  const bodyBg = themeExport.pageBg ?? derivedExportColors.pageBg;
  const containerBg = themeExport.cardBg ?? derivedExportColors.cardBg;
  const infoBg = themeExport.infoBg ?? derivedExportColors.infoBg;
  const sessionDataBase64 = Buffer.from(JSON.stringify(sessionData)).toString("base64");
  const css = templateCss.replace("{{THEME_VARS}}", themeVars).replace("{{BODY_BG}}", bodyBg).replace("{{CONTAINER_BG}}", containerBg).replace("{{INFO_BG}}", infoBg);
  return template.replace("{{CSS}}", css).replace("{{JS}}", templateJs).replace("{{SESSION_DATA}}", sessionDataBase64).replace("{{MARKED_JS}}", markedJs).replace("{{HIGHLIGHT_JS}}", hljsJs);
}
__name(generateHtml, "generateHtml");
var TEMPLATE_RENDERED_TOOLS = /* @__PURE__ */ new Set(["bash", "read", "write", "edit", "ls"]);
function preRenderCustomTools(entries, toolRenderer) {
  const renderedTools = {};
  for (const entry of entries) {
    if (entry.type !== "message")
      continue;
    const msg = entry.message;
    if (msg.role === "assistant" && Array.isArray(msg.content)) {
      for (const block of msg.content) {
        if (block.type === "toolCall" && !TEMPLATE_RENDERED_TOOLS.has(block.name)) {
          const callHtml = toolRenderer.renderCall(block.id, block.name, block.arguments);
          if (callHtml) {
            renderedTools[block.id] = { callHtml };
          }
        }
      }
    }
    if (msg.role === "toolResult" && msg.toolCallId) {
      const toolName = msg.toolName || "";
      const existing = renderedTools[msg.toolCallId];
      if (existing || !TEMPLATE_RENDERED_TOOLS.has(toolName)) {
        const rendered = toolRenderer.renderResult(msg.toolCallId, toolName, msg.content, msg.details, msg.isError || false);
        if (rendered) {
          renderedTools[msg.toolCallId] = {
            ...existing,
            resultHtmlCollapsed: rendered.collapsed,
            resultHtmlExpanded: rendered.expanded
          };
        }
      }
    }
  }
  return renderedTools;
}
__name(preRenderCustomTools, "preRenderCustomTools");
async function exportSessionToHtml(sm, state, options) {
  const opts = typeof options === "string" ? { outputPath: options } : options || {};
  const sessionFile = sm.getSessionFile();
  if (!sessionFile) {
    throw new Error("Cannot export in-memory session to HTML");
  }
  if (!existsSync4(sessionFile)) {
    throw new Error("Nothing to export yet - start a conversation first");
  }
  const entries = sm.getEntries();
  let renderedTools;
  if (opts.toolRenderer) {
    renderedTools = preRenderCustomTools(entries, opts.toolRenderer);
    if (Object.keys(renderedTools).length === 0) {
      renderedTools = void 0;
    }
  }
  const sessionData = {
    header: sm.getHeader(),
    entries,
    leafId: sm.getLeafId(),
    systemPrompt: state?.systemPrompt,
    tools: state?.tools?.map((t) => ({ name: t.name, description: t.description, parameters: t.parameters })),
    renderedTools
  };
  const html = generateHtml(sessionData, opts.themeName);
  let outputPath = opts.outputPath ? normalizePath(opts.outputPath) : void 0;
  if (!outputPath) {
    const sessionBasename = basename3(sessionFile, ".jsonl");
    outputPath = `${APP_NAME}-session-${sessionBasename}.html`;
  }
  writeFileSync2(outputPath, html, "utf8");
  return outputPath;
}
__name(exportSessionToHtml, "exportSessionToHtml");
async function exportFromFile(inputPath, options) {
  const opts = typeof options === "string" ? { outputPath: options } : options || {};
  const resolvedInputPath = resolvePath(inputPath);
  if (!existsSync4(resolvedInputPath)) {
    throw new Error(`File not found: ${resolvedInputPath}`);
  }
  const sm = SessionManager.open(resolvedInputPath);
  const sessionData = {
    header: sm.getHeader(),
    entries: sm.getEntries(),
    leafId: sm.getLeafId(),
    systemPrompt: void 0,
    tools: void 0
  };
  const html = generateHtml(sessionData, opts.themeName);
  let outputPath = opts.outputPath ? normalizePath(opts.outputPath) : void 0;
  if (!outputPath) {
    const inputBasename = basename3(resolvedInputPath, ".jsonl");
    outputPath = `${APP_NAME}-session-${inputBasename}.html`;
  }
  writeFileSync2(outputPath, html, "utf8");
  return outputPath;
}
__name(exportFromFile, "exportFromFile");

// pi-dist/pi-coding-agent/core/export-html/ansi-to-html.js
var ANSI_COLORS = [
  "#000000",
  // 0: black
  "#800000",
  // 1: red
  "#008000",
  // 2: green
  "#808000",
  // 3: yellow
  "#000080",
  // 4: blue
  "#800080",
  // 5: magenta
  "#008080",
  // 6: cyan
  "#c0c0c0",
  // 7: white
  "#808080",
  // 8: bright black
  "#ff0000",
  // 9: bright red
  "#00ff00",
  // 10: bright green
  "#ffff00",
  // 11: bright yellow
  "#0000ff",
  // 12: bright blue
  "#ff00ff",
  // 13: bright magenta
  "#00ffff",
  // 14: bright cyan
  "#ffffff"
  // 15: bright white
];
function color256ToHex(index) {
  if (index < 16) {
    return ANSI_COLORS[index];
  }
  if (index < 232) {
    const cubeIndex = index - 16;
    const r = Math.floor(cubeIndex / 36);
    const g = Math.floor(cubeIndex % 36 / 6);
    const b = cubeIndex % 6;
    const toComponent = /* @__PURE__ */ __name((n) => n === 0 ? 0 : 55 + n * 40, "toComponent");
    const toHex = /* @__PURE__ */ __name((n) => toComponent(n).toString(16).padStart(2, "0"), "toHex");
    return `#${toHex(r)}${toHex(g)}${toHex(b)}`;
  }
  const gray = 8 + (index - 232) * 10;
  const grayHex = gray.toString(16).padStart(2, "0");
  return `#${grayHex}${grayHex}${grayHex}`;
}
__name(color256ToHex, "color256ToHex");
function escapeHtml(text) {
  return text.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#039;");
}
__name(escapeHtml, "escapeHtml");
function createEmptyStyle() {
  return {
    fg: null,
    bg: null,
    bold: false,
    dim: false,
    italic: false,
    underline: false
  };
}
__name(createEmptyStyle, "createEmptyStyle");
function styleToInlineCSS(style) {
  const parts = [];
  if (style.fg)
    parts.push(`color:${style.fg}`);
  if (style.bg)
    parts.push(`background-color:${style.bg}`);
  if (style.bold)
    parts.push("font-weight:bold");
  if (style.dim)
    parts.push("opacity:0.6");
  if (style.italic)
    parts.push("font-style:italic");
  if (style.underline)
    parts.push("text-decoration:underline");
  return parts.join(";");
}
__name(styleToInlineCSS, "styleToInlineCSS");
function hasStyle(style) {
  return style.fg !== null || style.bg !== null || style.bold || style.dim || style.italic || style.underline;
}
__name(hasStyle, "hasStyle");
function applySgrCode(params, style) {
  let i = 0;
  while (i < params.length) {
    const code = params[i];
    if (code === 0) {
      style.fg = null;
      style.bg = null;
      style.bold = false;
      style.dim = false;
      style.italic = false;
      style.underline = false;
    } else if (code === 1) {
      style.bold = true;
    } else if (code === 2) {
      style.dim = true;
    } else if (code === 3) {
      style.italic = true;
    } else if (code === 4) {
      style.underline = true;
    } else if (code === 22) {
      style.bold = false;
      style.dim = false;
    } else if (code === 23) {
      style.italic = false;
    } else if (code === 24) {
      style.underline = false;
    } else if (code >= 30 && code <= 37) {
      style.fg = ANSI_COLORS[code - 30];
    } else if (code === 38) {
      if (params[i + 1] === 5 && params.length > i + 2) {
        style.fg = color256ToHex(params[i + 2]);
        i += 2;
      } else if (params[i + 1] === 2 && params.length > i + 4) {
        const r = params[i + 2];
        const g = params[i + 3];
        const b = params[i + 4];
        style.fg = `rgb(${r},${g},${b})`;
        i += 4;
      }
    } else if (code === 39) {
      style.fg = null;
    } else if (code >= 40 && code <= 47) {
      style.bg = ANSI_COLORS[code - 40];
    } else if (code === 48) {
      if (params[i + 1] === 5 && params.length > i + 2) {
        style.bg = color256ToHex(params[i + 2]);
        i += 2;
      } else if (params[i + 1] === 2 && params.length > i + 4) {
        const r = params[i + 2];
        const g = params[i + 3];
        const b = params[i + 4];
        style.bg = `rgb(${r},${g},${b})`;
        i += 4;
      }
    } else if (code === 49) {
      style.bg = null;
    } else if (code >= 90 && code <= 97) {
      style.fg = ANSI_COLORS[code - 90 + 8];
    } else if (code >= 100 && code <= 107) {
      style.bg = ANSI_COLORS[code - 100 + 8];
    }
    i++;
  }
}
__name(applySgrCode, "applySgrCode");
var ANSI_REGEX = /\x1b\[([\d;]*)m/g;
function ansiToHtml(text) {
  const style = createEmptyStyle();
  let result = "";
  let lastIndex = 0;
  let inSpan = false;
  ANSI_REGEX.lastIndex = 0;
  let match = ANSI_REGEX.exec(text);
  while (match !== null) {
    const beforeText = text.slice(lastIndex, match.index);
    if (beforeText) {
      result += escapeHtml(beforeText);
    }
    const paramStr = match[1];
    const params = paramStr ? paramStr.split(";").map((p) => parseInt(p, 10) || 0) : [0];
    if (inSpan) {
      result += "</span>";
      inSpan = false;
    }
    applySgrCode(params, style);
    if (hasStyle(style)) {
      result += `<span style="${styleToInlineCSS(style)}">`;
      inSpan = true;
    }
    lastIndex = match.index + match[0].length;
    match = ANSI_REGEX.exec(text);
  }
  const remainingText = text.slice(lastIndex);
  if (remainingText) {
    result += escapeHtml(remainingText);
  }
  if (inSpan) {
    result += "</span>";
  }
  return result;
}
__name(ansiToHtml, "ansiToHtml");
function ansiLinesToHtml(lines) {
  return lines.map((line) => `<div class="ansi-line">${ansiToHtml(line) || "&nbsp;"}</div>`).join("");
}
__name(ansiLinesToHtml, "ansiLinesToHtml");

// pi-dist/pi-coding-agent/core/export-html/tool-renderer.js
var ANSI_ESCAPE_REGEX = /\x1b\[[\d;]*m/g;
function isBlankRenderedLine(line) {
  return line.replace(ANSI_ESCAPE_REGEX, "").trim().length === 0;
}
__name(isBlankRenderedLine, "isBlankRenderedLine");
function trimRenderedResultLines(lines) {
  let start = 0;
  let end = lines.length;
  while (start < end && isBlankRenderedLine(lines[start]))
    start++;
  while (end > start && isBlankRenderedLine(lines[end - 1]))
    end--;
  return lines.slice(start, end);
}
__name(trimRenderedResultLines, "trimRenderedResultLines");
function createToolHtmlRenderer(deps) {
  const { getToolDefinition, theme: theme2, cwd, width = 100 } = deps;
  const renderedCallComponents = /* @__PURE__ */ new Map();
  const renderedResultComponents = /* @__PURE__ */ new Map();
  const renderedStates = /* @__PURE__ */ new Map();
  const renderedArgs = /* @__PURE__ */ new Map();
  const getState = /* @__PURE__ */ __name((toolCallId) => {
    let state = renderedStates.get(toolCallId);
    if (!state) {
      state = {};
      renderedStates.set(toolCallId, state);
    }
    return state;
  }, "getState");
  const createRenderContext = /* @__PURE__ */ __name((toolCallId, lastComponent, expanded, isPartial, isError) => {
    return {
      args: renderedArgs.get(toolCallId),
      toolCallId,
      invalidate: /* @__PURE__ */ __name(() => {
      }, "invalidate"),
      lastComponent,
      state: getState(toolCallId),
      cwd,
      executionStarted: true,
      argsComplete: true,
      isPartial,
      expanded,
      showImages: false,
      isError
    };
  }, "createRenderContext");
  return {
    renderCall(toolCallId, toolName, args) {
      try {
        renderedArgs.set(toolCallId, args);
        const toolDef = getToolDefinition(toolName);
        if (!toolDef?.renderCall) {
          return void 0;
        }
        const component = toolDef.renderCall(args, theme2, createRenderContext(toolCallId, renderedCallComponents.get(toolCallId), false, true, false));
        renderedCallComponents.set(toolCallId, component);
        const lines = component.render(width);
        return ansiLinesToHtml(lines);
      } catch {
        return void 0;
      }
    },
    renderResult(toolCallId, toolName, result, details, isError) {
      try {
        const toolDef = getToolDefinition(toolName);
        if (!toolDef?.renderResult) {
          return void 0;
        }
        const agentToolResult = {
          content: result,
          details,
          isError
        };
        const collapsedComponent = toolDef.renderResult(agentToolResult, { expanded: false, isPartial: false }, theme2, createRenderContext(toolCallId, renderedResultComponents.get(toolCallId), false, false, isError));
        renderedResultComponents.set(toolCallId, collapsedComponent);
        const collapsed = ansiLinesToHtml(trimRenderedResultLines(collapsedComponent.render(width)));
        const expandedComponent = toolDef.renderResult(agentToolResult, { expanded: true, isPartial: false }, theme2, createRenderContext(toolCallId, renderedResultComponents.get(toolCallId), true, false, isError));
        renderedResultComponents.set(toolCallId, expandedComponent);
        const expanded = ansiLinesToHtml(trimRenderedResultLines(expandedComponent.render(width)));
        return {
          ...collapsed && collapsed !== expanded ? { collapsed } : {},
          expanded
        };
      } catch {
        return void 0;
      }
    }
  };
}
__name(createToolHtmlRenderer, "createToolHtmlRenderer");

// pi-dist/pi-coding-agent/core/prompt-templates.js
import { existsSync as existsSync5, readdirSync as readdirSync4, readFileSync as readFileSync4, statSync as statSync4 } from "fs";
import { basename as basename4, dirname as dirname3, join as join7, resolve as resolve4, sep as sep2 } from "path";
function parseCommandArgs(argsString) {
  const args = [];
  let current = "";
  let inQuote = null;
  for (let i = 0; i < argsString.length; i++) {
    const char = argsString[i];
    if (inQuote) {
      if (char === inQuote) {
        inQuote = null;
      } else {
        current += char;
      }
    } else if (char === '"' || char === "'") {
      inQuote = char;
    } else if (/\s/.test(char)) {
      if (current) {
        args.push(current);
        current = "";
      }
    } else {
      current += char;
    }
  }
  if (current) {
    args.push(current);
  }
  return args;
}
__name(parseCommandArgs, "parseCommandArgs");
function substituteArgs(content, args) {
  const allArgs = args.join(" ");
  return content.replace(/\$\{(\d+|ARGUMENTS|@):-([^}]*)\}|\$\{@:(\d+)(?::(\d+))?\}|\$(ARGUMENTS|@|\d+)/g, (_match, defaultTarget, defaultValue, sliceStart, sliceLength, simple) => {
    if (defaultTarget) {
      const value = defaultTarget === "@" || defaultTarget === "ARGUMENTS" ? allArgs : args[parseInt(defaultTarget, 10) - 1];
      return value ? value : defaultValue;
    }
    if (sliceStart) {
      let start = parseInt(sliceStart, 10) - 1;
      if (start < 0)
        start = 0;
      if (sliceLength) {
        const length = parseInt(sliceLength, 10);
        return args.slice(start, start + length).join(" ");
      }
      return args.slice(start).join(" ");
    }
    if (simple === "ARGUMENTS" || simple === "@") {
      return allArgs;
    }
    const index = parseInt(simple, 10) - 1;
    return args[index] ?? "";
  });
}
__name(substituteArgs, "substituteArgs");
function loadTemplateFromFile(filePath, sourceInfo) {
  const diagnostics = [];
  let rawContent;
  try {
    rawContent = readFileSync4(filePath, "utf-8");
  } catch (error) {
    const message = error instanceof Error ? error.message : "failed to read prompt template file";
    diagnostics.push({ type: "warning", message, path: filePath });
    return { template: null, diagnostics };
  }
  let frontmatter;
  let body;
  try {
    ({ frontmatter, body } = parseFrontmatter(rawContent));
  } catch (error) {
    const message = error instanceof Error ? error.message : "failed to parse prompt template file";
    diagnostics.push({ type: "warning", message, path: filePath });
    return { template: null, diagnostics };
  }
  const name = basename4(filePath).replace(/\.md$/, "");
  let description = typeof frontmatter.description === "string" ? frontmatter.description : "";
  if (!description) {
    const firstLine = body.split("\n").find((line) => line.trim());
    if (firstLine) {
      description = firstLine.slice(0, 60);
      if (firstLine.length > 60)
        description += "...";
    }
  }
  const argumentHint = typeof frontmatter["argument-hint"] === "string" ? frontmatter["argument-hint"] : void 0;
  return {
    template: {
      name,
      description,
      ...argumentHint && { argumentHint },
      content: body,
      sourceInfo,
      filePath
    },
    diagnostics
  };
}
__name(loadTemplateFromFile, "loadTemplateFromFile");
function loadTemplatesFromDir(dir, getSourceInfo) {
  const templates = [];
  const diagnostics = [];
  if (!existsSync5(dir)) {
    return { templates, diagnostics };
  }
  try {
    const entries = readdirSync4(dir, { withFileTypes: true });
    for (const entry of entries) {
      const fullPath = join7(dir, entry.name);
      let isFile = entry.isFile();
      if (entry.isSymbolicLink()) {
        try {
          const stats = statSync4(fullPath);
          isFile = stats.isFile();
        } catch {
          continue;
        }
      }
      if (isFile && entry.name.endsWith(".md")) {
        const result = loadTemplateFromFile(fullPath, getSourceInfo(fullPath));
        if (result.template) {
          templates.push(result.template);
        }
        diagnostics.push(...result.diagnostics);
      }
    }
  } catch {
    return { templates, diagnostics };
  }
  return { templates, diagnostics };
}
__name(loadTemplatesFromDir, "loadTemplatesFromDir");
function loadPromptTemplates(options) {
  const resolvedCwd = resolvePath(options.cwd);
  const resolvedAgentDir = resolvePath(options.agentDir);
  const promptPaths = options.promptPaths;
  const includeDefaults = options.includeDefaults;
  const templates = [];
  const diagnostics = [];
  const addResult = /* @__PURE__ */ __name((result) => {
    templates.push(...result.templates);
    diagnostics.push(...result.diagnostics);
  }, "addResult");
  const globalPromptsDir = join7(resolvedAgentDir, "prompts");
  const projectPromptsDir = resolve4(resolvedCwd, CONFIG_DIR_NAME, "prompts");
  const isUnderPath = /* @__PURE__ */ __name((target, root) => {
    const normalizedRoot = resolve4(root);
    if (target === normalizedRoot) {
      return true;
    }
    const prefix = normalizedRoot.endsWith(sep2) ? normalizedRoot : `${normalizedRoot}${sep2}`;
    return target.startsWith(prefix);
  }, "isUnderPath");
  const getSourceInfo = /* @__PURE__ */ __name((resolvedPath) => {
    if (isUnderPath(resolvedPath, globalPromptsDir)) {
      return createSyntheticSourceInfo(resolvedPath, {
        source: "local",
        scope: "user",
        baseDir: globalPromptsDir
      });
    }
    if (isUnderPath(resolvedPath, projectPromptsDir)) {
      return createSyntheticSourceInfo(resolvedPath, {
        source: "local",
        scope: "project",
        baseDir: projectPromptsDir
      });
    }
    return createSyntheticSourceInfo(resolvedPath, {
      source: "local",
      baseDir: statSync4(resolvedPath).isDirectory() ? resolvedPath : dirname3(resolvedPath)
    });
  }, "getSourceInfo");
  if (includeDefaults) {
    addResult(loadTemplatesFromDir(globalPromptsDir, getSourceInfo));
    addResult(loadTemplatesFromDir(projectPromptsDir, getSourceInfo));
  }
  for (const rawPath of promptPaths) {
    const resolvedPath = resolvePath(rawPath, resolvedCwd, { trim: true });
    if (!existsSync5(resolvedPath)) {
      continue;
    }
    try {
      const stats = statSync4(resolvedPath);
      if (stats.isDirectory()) {
        addResult(loadTemplatesFromDir(resolvedPath, getSourceInfo));
      } else if (stats.isFile() && resolvedPath.endsWith(".md")) {
        const result = loadTemplateFromFile(resolvedPath, getSourceInfo(resolvedPath));
        if (result.template) {
          templates.push(result.template);
        }
        diagnostics.push(...result.diagnostics);
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : "failed to read prompt template path";
      diagnostics.push({ type: "warning", message, path: resolvedPath });
    }
  }
  return { templates, diagnostics };
}
__name(loadPromptTemplates, "loadPromptTemplates");
function expandPromptTemplate(text, templates) {
  if (!text.startsWith("/"))
    return text;
  const match = text.match(/^\/([^\s]+)(?:\s+([\s\S]*))?$/);
  if (!match)
    return text;
  const templateName = match[1];
  const argsString = match[2] ?? "";
  const template = templates.find((t) => t.name === templateName);
  if (template) {
    const args = parseCommandArgs(argsString);
    return substituteArgs(template.content, args);
  }
  return text;
}
__name(expandPromptTemplate, "expandPromptTemplate");

// pi-dist/pi-coding-agent/core/session-export.js
import { existsSync as existsSync6, mkdirSync as mkdirSync2, writeFileSync as writeFileSync3 } from "node:fs";
import { dirname as dirname4 } from "node:path";
function serializeSessionBranch(sessionManager, createTrailingEntries) {
  const timestamp = (/* @__PURE__ */ new Date()).toISOString();
  const header = {
    type: "session",
    version: CURRENT_SESSION_VERSION,
    id: sessionManager.getSessionId(),
    timestamp,
    cwd: sessionManager.getCwd()
  };
  const entries = [header];
  let parentId = null;
  for (const entry of sessionManager.getBranch()) {
    entries.push({ ...entry, parentId });
    parentId = entry.id;
  }
  entries.push(...createTrailingEntries?.(parentId, timestamp) ?? []);
  return `${entries.map((entry) => JSON.stringify(entry)).join("\n")}
`;
}
__name(serializeSessionBranch, "serializeSessionBranch");
function exportSessionToJsonl(sessionManager, outputPath, createTrailingEntries) {
  const filePath = resolvePath(outputPath ?? `session-${(/* @__PURE__ */ new Date()).toISOString().replace(/[:.]/g, "-")}.jsonl`, process.cwd());
  const dir = dirname4(filePath);
  if (!existsSync6(dir))
    mkdirSync2(dir, { recursive: true });
  writeFileSync3(filePath, serializeSessionBranch(sessionManager, createTrailingEntries));
  return filePath;
}
__name(exportSessionToJsonl, "exportSessionToJsonl");

// pi-dist/pi-coding-agent/core/usage-totals.js
function createUsageTotals() {
  return {
    input: 0,
    output: 0,
    cacheRead: 0,
    cacheWrite: 0,
    cost: 0
  };
}
__name(createUsageTotals, "createUsageTotals");
function addUsageToTotals(totals, usage) {
  totals.input += usage.input;
  totals.output += usage.output;
  totals.cacheRead += usage.cacheRead;
  totals.cacheWrite += usage.cacheWrite;
  totals.cost += usage.cost.total;
}
__name(addUsageToTotals, "addUsageToTotals");
function getUsageCostBreakdown(entries) {
  const totalsByKey = /* @__PURE__ */ new Map();
  for (const entry of entries) {
    let key;
    let usage;
    if (entry.type === "message" && entry.message.role === "assistant") {
      key = `${entry.message.provider}/${entry.message.responseModel ?? entry.message.model}`;
      usage = entry.message.usage;
    } else if (entry.type === "usage") {
      key = `${entry.provider}/${entry.model}`;
      usage = entry.usage;
    } else if (entry.type === "message" && entry.message.role === "toolResult" && entry.message.usage) {
      key = "Tools/summaries";
      usage = entry.message.usage;
    } else if ((entry.type === "branch_summary" || entry.type === "compaction") && entry.usage) {
      key = "Tools/summaries";
      usage = entry.usage;
    }
    if (!key || !usage)
      continue;
    let totals = totalsByKey.get(key);
    if (!totals) {
      totals = createUsageTotals();
      totalsByKey.set(key, totals);
    }
    addUsageToTotals(totals, usage);
  }
  return Array.from(totalsByKey, ([key, totals]) => ({
    key,
    cost: totals.cost,
    tokens: totals.input + totals.output + totals.cacheRead + totals.cacheWrite
  })).filter((entry) => entry.cost > 0 || entry.tokens > 0).sort((a, b) => b.cost - a.cost);
}
__name(getUsageCostBreakdown, "getUsageCostBreakdown");

// pi-dist/pi-coding-agent/core/agent-session.js
function parseSkillBlock(text) {
  const match = text.match(/^<skill name="([^"]+)" location="([^"]+)">\n([\s\S]*?)\n<\/skill>(?:\n\n([\s\S]+))?$/);
  if (!match)
    return null;
  return {
    name: match[1],
    location: match[2],
    content: match[3],
    userMessage: match[4]?.trim() || void 0
  };
}
__name(parseSkillBlock, "parseSkillBlock");
function withoutDeletedHeaders(headers) {
  return headers ? Object.fromEntries(Object.entries(headers).filter((entry) => entry[1] !== null)) : void 0;
}
__name(withoutDeletedHeaders, "withoutDeletedHeaders");
function estimateMessagesTokens(messages) {
  let tokens = 0;
  for (const message of messages) {
    tokens += estimateTokens(message);
  }
  return tokens;
}
__name(estimateMessagesTokens, "estimateMessagesTokens");
var AgentSession = class {
  static {
    __name(this, "AgentSession");
  }
  agent;
  sessionManager;
  settingsManager;
  _scopedModels;
  // Event subscription state
  _unsubscribeAgent;
  _eventListeners = [];
  _isAgentRunActive = false;
  _agentRunAbortRequested = false;
  _idleWaitPromise;
  _resolveIdleWait;
  /** Tracks pending steering messages for UI display. Removed when delivered. */
  _steeringMessages = [];
  /** Tracks pending follow-up messages for UI display. Removed when delivered. */
  _followUpMessages = [];
  /** Messages queued to be included with the next user prompt as context ("asides"). */
  _pendingNextTurnMessages = [];
  /** Context-only custom messages queued during a run, flushed once the current turn's tool results are in. */
  _pendingCustomMessages = [];
  // Compaction state
  _compactionAbortController = void 0;
  _autoCompactionAbortController = void 0;
  _overflowRecoveryAttempted = false;
  // Branch summarization state
  _branchSummaryAbortController = void 0;
  // Retry state
  _retryAbortController = void 0;
  _retryAttempt = 0;
  // Bash execution state
  _bashAbortControllers = /* @__PURE__ */ new Set();
  _pendingBashMessages = [];
  // Extension system
  _extensionRunner;
  _turnIndex = 0;
  _entryIdsByMessage = /* @__PURE__ */ new WeakMap();
  _boundaryDispatchedMessages = /* @__PURE__ */ new WeakSet();
  _lastAssistantMessage;
  _lastAssistantToolResults = [];
  _lastActivityOutcome = "completed";
  _isBeforeSettle = false;
  _abortDuringBeforeSettle = false;
  _isEmittingAgentSettled = false;
  _deferredSettledActions = [];
  _resourceLoader;
  _customTools;
  _baseToolDefinitions = /* @__PURE__ */ new Map();
  _cwd;
  _extensionRunnerRef;
  _initialActiveToolNames;
  _allowedToolNames;
  _excludedToolNames;
  _baseToolsOverride;
  _sessionStartEvent;
  _extensionUIContext;
  _extensionMode = "print";
  _extensionCommandContextActions;
  _extensionAbortHandler;
  _extensionShutdownHandler;
  _extensionErrorListener;
  _extensionErrorUnsubscriber;
  _modelRuntime;
  _cacheWarmer;
  // Tool registry for extension getTools/setTools
  _toolRegistry = /* @__PURE__ */ new Map();
  _toolDefinitions = /* @__PURE__ */ new Map();
  _toolPromptSnippets = /* @__PURE__ */ new Map();
  _toolPromptGuidelines = /* @__PURE__ */ new Map();
  _baseSystemPromptOptions;
  /** Prompt options after before_agent_start mutations for the active run. */
  _runSystemPromptOptions;
  constructor(config) {
    this.agent = config.agent;
    this.sessionManager = config.sessionManager;
    this.settingsManager = config.settingsManager;
    this._scopedModels = config.scopedModels ?? [];
    this._resourceLoader = config.resourceLoader;
    this._customTools = config.customTools ?? [];
    this._cwd = config.cwd;
    this._modelRuntime = config.modelRuntime;
    this._cacheWarmer = config.cacheWarmer;
    if (this._cacheWarmer) {
      this._cacheWarmer.onWarmed = (entry) => this._emit({ type: "entry_appended", entry });
    }
    this._extensionRunnerRef = config.extensionRunnerRef;
    this._initialActiveToolNames = config.initialActiveToolNames;
    this._allowedToolNames = config.allowedToolNames ? new Set(config.allowedToolNames) : void 0;
    this._excludedToolNames = config.excludedToolNames ? new Set(config.excludedToolNames) : void 0;
    this._baseToolsOverride = config.baseToolsOverride;
    this._sessionStartEvent = config.sessionStartEvent ?? { type: "session_start", reason: "startup" };
    this._unsubscribeAgent = this.agent.subscribe(this._handleAgentEvent);
    this._installAgentToolHooks();
    this._installAgentNextTurnRefresh();
    this._installAgentRequestProjection();
    this._installAgentBoundaryHooks();
    this._installAgentForcedPromptProjection();
    this._buildRuntime({
      activeToolNames: this._initialActiveToolNames,
      includeAllExtensionTools: true
    });
    if (this._initialActiveToolNames === void 0)
      this._restoreToolsFromTranscript();
  }
  get modelRuntime() {
    return this._modelRuntime;
  }
  async _getRequiredRequestAuth(model, signal) {
    let result;
    try {
      result = await this._modelRuntime.getAuth(model, { signal });
    } catch (error) {
      const cause = error instanceof Error ? error.cause : void 0;
      if (cause instanceof Error && cause.message === "authHeader requires a resolved API key") {
        throw new Error(formatNoApiKeyFoundMessage(model.provider));
      }
      throw error;
    }
    if (result && (result.auth.apiKey || result.auth.headers)) {
      const requestModel = result.auth.baseUrl ? { ...model, baseUrl: result.auth.baseUrl } : model;
      return {
        model: requestModel,
        apiKey: result.auth.apiKey,
        headers: withoutDeletedHeaders(result.auth.headers),
        env: result.env
      };
    }
    const isOAuth = this._modelRuntime.isUsingOAuth(model.provider);
    if (isOAuth) {
      throw new Error(`Authentication failed for "${model.provider}". Credentials may have expired or network is unavailable. Run '/login ${model.provider}' to re-authenticate.`);
    }
    throw new Error(formatNoApiKeyFoundMessage(model.provider));
  }
  async _getSummarizationRequestAuth(model, signal) {
    if (this.agent.streamFunction === streamSimple) {
      return this._getRequiredRequestAuth(model, signal);
    }
    try {
      const result = await this._modelRuntime.getAuth(model, { signal });
      if (!result)
        return { model };
      const requestModel = result.auth.baseUrl ? { ...model, baseUrl: result.auth.baseUrl } : model;
      return {
        model: requestModel,
        apiKey: result.auth.apiKey,
        headers: withoutDeletedHeaders(result.auth.headers),
        env: result.env
      };
    } catch (error) {
      if (signal?.aborted)
        throw error;
      return { model };
    }
  }
  /**
   * Install tool hooks once on the Agent instance.
   *
   * The callbacks read `this._extensionRunner` at execution time, so extension reload swaps in the
   * new runner without reinstalling hooks. Extension-specific tool wrappers are still used to adapt
   * registered tool execution to the extension context. Tool call and tool result interception now
   * happens here instead of in wrappers.
   */
  _installAgentToolHooks() {
    this.agent.beforeToolCall = async ({ toolCall, args }) => {
      const runner = this._extensionRunner;
      if (!runner.hasHandlers("tool_call")) {
        return void 0;
      }
      try {
        return await runner.emitToolCall({
          type: "tool_call",
          toolName: toolCall.name,
          toolCallId: toolCall.id,
          input: args
        });
      } catch (err) {
        if (err instanceof Error) {
          throw err;
        }
        throw new Error(`Extension failed, blocking execution: ${String(err)}`);
      }
    };
    this.agent.afterToolCall = async ({ toolCall, args, result, isError }) => {
      const runner = this._extensionRunner;
      const hookResult = runner.hasHandlers("tool_result") ? await runner.emitToolResult({
        type: "tool_result",
        toolName: toolCall.name,
        toolCallId: toolCall.id,
        input: args,
        content: result.content,
        details: result.details,
        isError,
        usage: result.usage
      }) : void 0;
      const content = hookResult?.content ?? result.content ?? [];
      const resizeOptions = this.model?.inputLimits?.images?.resize;
      const normalizedContent = await normalizeToolResultImages(content, {
        autoResizeImages: this.settingsManager.getImageAutoResize(),
        ...resizeOptions ? { resizeOptions } : {}
      });
      if (!hookResult && normalizedContent === content) {
        return void 0;
      }
      return {
        content: normalizedContent,
        details: hookResult?.details,
        isError: hookResult?.isError ?? isError,
        usage: hookResult?.usage
      };
    };
  }
  async _compactBeforeNextAssistantResponse(context) {
    const model = this.model;
    const settings = this.settingsManager.getCompactionSettings(model);
    const projection = this.sessionManager.buildSessionProjection();
    if (!model || model.contextWindow <= 0 || !shouldCompact(estimateProjectedContextTokens(projection, this.sessionManager.getBranch()).tokens, model.contextWindow, settings)) {
      return { ...context, messages: projection.messages };
    }
    await this._runAutoCompaction("threshold", false);
    return { ...context, messages: this.sessionManager.buildSessionProjection().messages };
  }
  _installAgentRequestProjection() {
    const previousPrepareRequest = this.agent.prepareRequest;
    this.agent.prepareRequest = async (request, signal) => {
      const canonicalContext = {
        ...request.context,
        messages: this.sessionManager.buildSessionProjection().messages,
        // Messages declare the provider-visible loadout; context.tools keeps executable implementations.
        tools: this.agent.state.tools.slice()
      };
      const previous = await previousPrepareRequest?.({
        ...request,
        context: canonicalContext,
        model: this.agent.state.model,
        thinkingLevel: this.agent.state.thinkingLevel
      }, signal);
      return {
        ...previous,
        context: previous?.context ?? canonicalContext,
        model: previous?.model ?? this.agent.state.model,
        thinkingLevel: previous?.thinkingLevel ?? this.agent.state.thinkingLevel
      };
    };
  }
  async _dispatchTurnEndBoundary(message, toolResults) {
    this._lastActivityOutcome = message.stopReason === "aborted" ? "aborted" : message.stopReason === "error" ? "error" : "completed";
    const messageEntryId = this._findPersistedMessageEntryId(message);
    if (!this._extensionRunner.hasHandlers("turn_end"))
      return false;
    if (!messageEntryId) {
      this._extensionRunner.emitError({
        extensionPath: "<boundary>",
        event: "turn_end",
        error: "turn_end could not resolve the persisted assistant entry ID"
      });
      return false;
    }
    const toolResultEntryIds = toolResults.flatMap((result) => {
      const entryId = this._findPersistedMessageEntryId(result);
      return entryId ? [entryId] : [];
    });
    const boundary = await this._extensionRunner.emitBoundary({
      type: "turn_end",
      turnIndex: this._turnIndex,
      message,
      toolResults,
      messageEntryId,
      toolResultEntryIds,
      outcome: this._lastActivityOutcome
    }, (entries) => this._buildBoundaryContext(entries, "turn_end"));
    this._commitBoundaryDrafts(boundary.entries);
    if (boundary.continue && !this._buildBoundaryContext([], "turn_end").canContinue) {
      this._reportInvalidBoundaryContinuation("turn_end");
      return false;
    }
    return boundary.continue;
  }
  _installAgentBoundaryHooks() {
    const previousFinishTurn = this.agent.finishTurn;
    this.agent.finishTurn = async (turn, signal) => {
      this._boundaryDispatchedMessages.add(turn.message);
      const extensionContinue = await this._dispatchTurnEndBoundary(turn.message, turn.toolResults);
      const previousDecision = await previousFinishTurn?.(turn, signal);
      if (previousDecision?.action === "end")
        return previousDecision;
      if (extensionContinue || previousDecision?.action === "continue")
        return { action: "continue" };
      return void 0;
    };
  }
  _installAgentNextTurnRefresh() {
    const previousPrepareNextTurnWithContext = this.agent.prepareNextTurnWithContext ?? (this.agent.prepareNextTurn ? async (_turn, signal) => await this.agent.prepareNextTurn?.(signal) : void 0);
    this.agent.prepareNextTurnWithContext = async (turn, signal) => {
      const context = await this._compactBeforeNextAssistantResponse({
        ...turn.context,
        messages: this.sessionManager.buildSessionProjection().messages
      });
      const previousSnapshot = await previousPrepareNextTurnWithContext?.({ ...turn, context }, signal);
      const nextContext = previousSnapshot?.context ?? context;
      const runOptions = this._runSystemPromptOptions ?? this._baseSystemPromptOptions;
      const options = normalizeBuildSystemPromptOptions({
        ...runOptions,
        selectedTools: this.getActiveToolNames(),
        toolSnippets: { ...this._baseSystemPromptOptions.toolSnippets, ...runOptions.toolSnippets },
        toolGuidelines: { ...this._baseSystemPromptOptions.toolGuidelines, ...runOptions.toolGuidelines }
      });
      const updateMessage = this._preparePromptAndToolLoadout(options, nextContext.messages);
      this._runSystemPromptOptions = options;
      return {
        ...previousSnapshot,
        context: {
          ...nextContext,
          tools: this.agent.state.tools.slice()
        },
        messages: updateMessage ? [...previousSnapshot?.messages ?? [], updateMessage] : previousSnapshot?.messages,
        model: this.agent.state.model,
        thinkingLevel: this.agent.state.thinkingLevel
      };
    };
  }
  // =========================================================================
  // Event Subscription
  // =========================================================================
  _refreshFinalizedContext() {
    const projection = this.sessionManager.buildSessionProjection();
    for (const entry of projection.entries) {
      for (const message of entry.messages)
        this._entryIdsByMessage.set(message, entry.sourceEntry.id);
    }
    this.agent.state.messages = projection.messages;
  }
  _applyBoundaryDrafts(manager, drafts) {
    const appended = [];
    for (const draft of drafts) {
      let entryId;
      switch (draft.type) {
        case "custom":
          entryId = manager.appendCustomEntry(draft.customType, draft.data);
          break;
        case "custom_message":
          entryId = manager.appendCustomMessageEntry(draft.customType, draft.content, draft.display, draft.details);
          break;
        case "context_edit":
          entryId = manager.appendContextEdit(draft.targetId, draft.replacement);
          break;
        case "compaction": {
          const tokensBefore = estimateProjectedContextTokens(manager.buildSessionProjection(), manager.getBranch()).tokens;
          entryId = manager.appendCompaction(draft.summary, draft.firstKeptEntryId, tokensBefore, draft.details, true, draft.usage);
          break;
        }
      }
      const entry = manager.getEntry(entryId);
      if (entry)
        appended.push(entry);
    }
    return appended;
  }
  _createBoundaryPreviewManager(drafts) {
    const header = this.sessionManager.getHeader();
    if (!header)
      throw new Error("Session header is missing");
    const manager = SessionManager.inMemory(this._cwd, void 0, [header, ...this.sessionManager.getBranch()]);
    this._applyBoundaryDrafts(manager, drafts);
    return manager;
  }
  _getPendingBoundaryMessages() {
    return [...this.agent.peekQueuedMessages(), ...this._pendingCustomMessages];
  }
  _buildBoundaryContext(drafts, boundary) {
    const projection = this._createBoundaryPreviewManager(drafts).buildSessionProjection();
    const pendingMessages = this._getPendingBoundaryMessages();
    const llmMessages = convertToLlm(projection.messages);
    const finalRole = llmMessages[llmMessages.length - 1]?.role;
    const hasNonSystemContext = llmMessages.some((message) => message.role !== "system");
    const contextCanContinue = hasNonSystemContext && finalRole !== "assistant";
    const pendingCustomContext = this._pendingCustomMessages.length > 0;
    return {
      contextEntries: projection.entries,
      contextMessages: projection.messages,
      llmMessages,
      pendingMessages,
      canContinue: contextCanContinue || pendingCustomContext || (boundary === "turn_end" ? this.agent.hasQueuedMessages() : finalRole === "assistant" && this.agent.hasQueuedMessages())
    };
  }
  _commitBoundaryDrafts(drafts) {
    const appended = this._applyBoundaryDrafts(this.sessionManager, drafts);
    this._refreshFinalizedContext();
    for (const entry of appended)
      this._emit({ type: "entry_appended", entry });
  }
  _reportInvalidBoundaryContinuation(event) {
    this._extensionRunner.emitError({
      extensionPath: "<boundary>",
      event,
      error: `${event} requested continuation without runnable model context`
    });
  }
  /** Emit an event to all listeners */
  _emit(event) {
    for (const l of this._eventListeners) {
      l(event);
    }
  }
  _emitQueueUpdate() {
    this._emit({
      type: "queue_update",
      steering: [...this._steeringMessages],
      followUp: [...this._followUpMessages]
    });
  }
  async _emitSessionCompactFailed(event) {
    if (this._extensionRunner.hasHandlers("session_compact_failed")) {
      await this._extensionRunner.emit({ type: "session_compact_failed", ...event });
    }
  }
  _getIdleWaitPromise() {
    if (!this._idleWaitPromise) {
      this._idleWaitPromise = new Promise((resolve9) => {
        this._resolveIdleWait = resolve9;
      });
    }
    return this._idleWaitPromise;
  }
  _resolveIdleWaitIfIdle() {
    if (!this.isIdle || !this._resolveIdleWait) {
      return;
    }
    const resolve9 = this._resolveIdleWait;
    this._idleWaitPromise = void 0;
    this._resolveIdleWait = void 0;
    resolve9();
  }
  async _emitAgentSettled() {
    this._cacheWarmer?.onAgentSettled();
    this._isAgentRunActive = false;
    this._isEmittingAgentSettled = true;
    try {
      await this._extensionRunner.emit({ type: "agent_settled" });
      this._emit({ type: "agent_settled" });
    } finally {
      this._isEmittingAgentSettled = false;
    }
    const deferred = this._deferredSettledActions.splice(0);
    if (deferred.length > 0) {
      try {
        for (const action of deferred)
          await action();
      } finally {
        this._resolveIdleWaitIfIdle();
      }
      return;
    }
    this._resolveIdleWaitIfIdle();
  }
  /** Internal handler for agent events - shared by subscribe and reconnect */
  _handleAgentEvent = /* @__PURE__ */ __name(async (event) => {
    if (event.type === "message_start" && event.message.role === "user") {
      this._overflowRecoveryAttempted = false;
      const messageText = contentText5(event.message.content, "");
      if (messageText) {
        const steeringIndex = this._steeringMessages.indexOf(messageText);
        if (steeringIndex !== -1) {
          this._steeringMessages.splice(steeringIndex, 1);
          this._emitQueueUpdate();
        } else {
          const followUpIndex = this._followUpMessages.indexOf(messageText);
          if (followUpIndex !== -1) {
            this._followUpMessages.splice(followUpIndex, 1);
            this._emitQueueUpdate();
          }
        }
      }
    }
    await this._emitExtensionEvent(event);
    this._emit(event.type === "agent_end" ? { ...event, willRetry: this._willRetryAfterAgentEnd(event) } : event);
    if (event.type === "message_end") {
      let entryId;
      if (event.message.role === "custom") {
        entryId = this.sessionManager.appendCustomMessageEntry(event.message.customType, event.message.content, event.message.display, event.message.details);
      } else if (event.message.role === "system" || event.message.role === "user" || event.message.role === "assistant" || event.message.role === "toolResult") {
        entryId = this.sessionManager.appendMessage(event.message);
      }
      if (entryId)
        this._entryIdsByMessage.set(event.message, entryId);
      if (event.message.role === "assistant") {
        const assistantMsg = event.message;
        this._lastAssistantMessage = assistantMsg;
        if (assistantMsg.stopReason !== "error" && assistantMsg.stopReason !== "length") {
          this._overflowRecoveryAttempted = false;
        }
        if (assistantMsg.stopReason !== "error" && this._retryAttempt > 0) {
          this._emit({
            type: "auto_retry_end",
            success: true,
            attempt: this._retryAttempt
          });
          this._retryAttempt = 0;
        }
      }
    }
    if (event.type === "turn_end") {
      this._lastAssistantToolResults = event.toolResults;
      this._flushPendingCustomMessages();
    }
  }, "_handleAgentEvent");
  _willRetryAfterAgentEnd(event) {
    if (this._agentRunAbortRequested)
      return false;
    const settings = this.settingsManager.getRetrySettings();
    if (!settings.enabled || this._retryAttempt >= settings.maxRetries) {
      return false;
    }
    for (let i = event.messages.length - 1; i >= 0; i--) {
      const message = event.messages[i];
      if (message.role === "assistant") {
        return this._isRetryableError(message);
      }
    }
    return false;
  }
  _findPersistedMessageEntryId(message) {
    const mapped = this._entryIdsByMessage.get(message);
    if (mapped)
      return mapped;
    for (const entry of [...this.sessionManager.getBranch()].reverse()) {
      if (entry.type === "message" && entry.message === message)
        return entry.id;
    }
    const messageIndex = this.agent.state.messages.indexOf(message);
    if (messageIndex < 0)
      return void 0;
    const projection = this.sessionManager.buildSessionProjection();
    let projectedIndex = 0;
    for (const entry of projection.entries) {
      for (let i = 0; i < entry.messages.length; i++) {
        if (projectedIndex === messageIndex) {
          this._entryIdsByMessage.set(message, entry.sourceEntry.id);
          return entry.sourceEntry.id;
        }
        projectedIndex++;
      }
    }
    return void 0;
  }
  _omitRecoveryAttempt(message, toolResults = []) {
    const targets = [message, ...toolResults];
    const targetIds = targets.map((target) => this._findPersistedMessageEntryId(target));
    const unresolvedProjectedTarget = targets.some((target, index) => targetIds[index] === void 0 && this.agent.state.messages.includes(target));
    if (unresolvedProjectedTarget) {
      throw new Error("Cannot persist recovery omission because a projected message has no source entry");
    }
    for (const targetId of targetIds) {
      if (!targetId)
        continue;
      const editId = this.sessionManager.appendContextEdit(targetId, null);
      const entry = this.sessionManager.getEntry(editId);
      if (entry)
        this._emit({ type: "entry_appended", entry });
    }
    this._refreshFinalizedContext();
  }
  /** Find the last assistant message in agent state (including aborted ones) */
  _findLastAssistantMessage() {
    const messages = this.agent.state.messages;
    for (let i = messages.length - 1; i >= 0; i--) {
      const msg = messages[i];
      if (msg.role === "assistant") {
        return msg;
      }
    }
    return void 0;
  }
  _replaceMessageInPlace(target, replacement) {
    if (target === replacement) {
      return;
    }
    const targetRecord = target;
    for (const key of Object.keys(targetRecord)) {
      delete targetRecord[key];
    }
    Object.assign(targetRecord, replacement);
  }
  /** Emit extension events based on agent events */
  async _emitExtensionEvent(event) {
    if (event.type === "agent_start") {
      this._turnIndex = 0;
      await this._extensionRunner.emit({ type: "agent_start" });
    } else if (event.type === "agent_end") {
      await this._extensionRunner.emit({ type: "agent_end", messages: event.messages });
    } else if (event.type === "turn_start") {
      const extensionEvent = {
        type: "turn_start",
        turnIndex: this._turnIndex,
        timestamp: Date.now()
      };
      await this._extensionRunner.emit(extensionEvent);
    } else if (event.type === "turn_end") {
      if (event.message.role === "assistant" && !this._boundaryDispatchedMessages.delete(event.message)) {
        await this._dispatchTurnEndBoundary(event.message, event.toolResults);
      }
      this._turnIndex++;
    } else if (event.type === "message_start") {
      const extensionEvent = {
        type: "message_start",
        message: event.message
      };
      await this._extensionRunner.emit(extensionEvent);
    } else if (event.type === "message_update") {
      const extensionEvent = {
        type: "message_update",
        message: event.message,
        assistantMessageEvent: event.assistantMessageEvent
      };
      await this._extensionRunner.emit(extensionEvent);
    } else if (event.type === "message_end") {
      const extensionEvent = {
        type: "message_end",
        message: event.message
      };
      const replacement = await this._extensionRunner.emitMessageEnd(extensionEvent);
      if (replacement) {
        const normalized = (replacement.role === "user" || replacement.role === "assistant" || replacement.role === "toolResult" || replacement.role === "custom") && replacement.content == null ? { ...replacement, content: [] } : replacement;
        this._replaceMessageInPlace(event.message, normalized);
      }
    } else if (event.type === "tool_execution_start") {
      const extensionEvent = {
        type: "tool_execution_start",
        toolCallId: event.toolCallId,
        toolName: event.toolName,
        args: event.args
      };
      await this._extensionRunner.emit(extensionEvent);
    } else if (event.type === "tool_execution_update") {
      const extensionEvent = {
        type: "tool_execution_update",
        toolCallId: event.toolCallId,
        toolName: event.toolName,
        args: event.args,
        partialResult: event.partialResult
      };
      await this._extensionRunner.emit(extensionEvent);
    } else if (event.type === "tool_execution_end") {
      const extensionEvent = {
        type: "tool_execution_end",
        toolCallId: event.toolCallId,
        toolName: event.toolName,
        result: event.result,
        isError: event.isError
      };
      await this._extensionRunner.emit(extensionEvent);
    }
  }
  /**
   * Subscribe to agent events.
   * Session persistence is handled internally (saves messages on message_end).
   * Multiple listeners can be added. Returns unsubscribe function for this listener.
   */
  subscribe(listener) {
    this._eventListeners.push(listener);
    return () => {
      const index = this._eventListeners.indexOf(listener);
      if (index !== -1) {
        this._eventListeners.splice(index, 1);
      }
    };
  }
  /** Disconnect from agent events during disposal. */
  _disconnectFromAgent() {
    if (this._unsubscribeAgent) {
      this._unsubscribeAgent();
      this._unsubscribeAgent = void 0;
    }
  }
  /**
   * Remove all listeners and disconnect from agent.
   * Call this when completely done with the session.
   */
  dispose() {
    try {
      this.abortRetry();
      this.abortCompaction();
      this.abortBranchSummary();
      this.abortBash();
      this.agent.abort();
    } catch {
    }
    this._extensionRunner.invalidate("This extension ctx is stale after session replacement or reload. Do not use a captured pi or command ctx after ctx.newSession(), ctx.fork(), ctx.switchSession(), or ctx.reload(). For newSession, fork, and switchSession, move post-replacement work into withSession and use the ctx passed to withSession. For reload, do not use the old ctx after await ctx.reload().");
    this._disconnectFromAgent();
    this._eventListeners = [];
    if (this._cacheWarmer) {
      this._cacheWarmer.onWarmed = void 0;
      this._cacheWarmer.cancel();
    }
    cleanupSessionResources(this.sessionId);
  }
  // =========================================================================
  // Read-only State Access
  // =========================================================================
  /** Refresh the public finalized transcript from the canonical session projection. */
  refreshContext() {
    this._refreshFinalizedContext();
  }
  /** Full agent state */
  get state() {
    return this.agent.state;
  }
  /** Current cache-warming state and the policy inputs that produced it. */
  get cacheWarmingStatus() {
    return this._cacheWarmer?.status;
  }
  /** Persist the cache-warming mode and immediately reconcile active warming. */
  setCacheWarmingMode(mode) {
    this.settingsManager.setCacheWarmingMode(mode);
    this._cacheWarmer?.onModeChanged();
  }
  /** Current model (may be undefined if not yet selected) */
  get model() {
    return this.agent.state.model;
  }
  /** Current thinking level */
  get thinkingLevel() {
    return this.agent.state.thinkingLevel;
  }
  /** Whether the session is currently processing an agent run or post-run continuation. */
  get isStreaming() {
    return this._isAgentRunActive;
  }
  /** Whether the session has no active agent run, compaction, branch summary, retry, or queued continuation. */
  get isIdle() {
    return !this._isAgentRunActive && !this.isCompacting;
  }
  /** Current effective system prompt, including changes not yet sent to the model. */
  get systemPrompt() {
    return buildSystemPrompt(this._runSystemPromptOptions ?? this._baseSystemPromptOptions);
  }
  /** Current retry attempt (0 if not retrying) */
  get retryAttempt() {
    return this._retryAttempt;
  }
  /**
   * Get the names of currently active tools.
   * Returns the names of tools currently set on the agent.
   */
  getActiveToolNames() {
    return this.agent.state.tools.map((t) => t.name);
  }
  /**
   * Get all configured tools with name, description, parameter schema, prompt guidelines, and source metadata.
   */
  getAllTools() {
    return Array.from(this._toolDefinitions.values()).map(({ definition, sourceInfo }) => ({
      name: definition.name,
      description: definition.description,
      parameters: definition.parameters,
      promptGuidelines: definition.promptGuidelines,
      sourceInfo
    }));
  }
  getToolDefinition(name) {
    return this._toolDefinitions.get(name)?.definition;
  }
  /**
   * Set active tools by name.
   * Only tools in the registry can be enabled. Unknown tool names are ignored.
   * Also rebuilds the system prompt to reflect the new tool set.
   * Changes take effect on the next agent turn.
   */
  setActiveToolsByName(toolNames) {
    const tools = [];
    const validToolNames = [];
    for (const name of toolNames) {
      const tool = this._toolRegistry.get(name);
      if (tool) {
        tools.push(tool);
        validToolNames.push(name);
      }
    }
    this.agent.state.tools = tools;
    this._rebuildSystemPrompt(validToolNames);
  }
  /** Whether compaction or branch summarization is currently running */
  get isCompacting() {
    return this._autoCompactionAbortController !== void 0 || this._compactionAbortController !== void 0 || this._branchSummaryAbortController !== void 0;
  }
  /** All messages including custom types like BashExecutionMessage */
  get messages() {
    return this.agent.state.messages;
  }
  /** Current steering mode */
  get steeringMode() {
    return this.agent.steeringMode;
  }
  /** Current follow-up mode */
  get followUpMode() {
    return this.agent.followUpMode;
  }
  /** Current session file path, or undefined if sessions are disabled */
  get sessionFile() {
    return this.sessionManager.getSessionFile();
  }
  /** Current session ID */
  get sessionId() {
    return this.sessionManager.getSessionId();
  }
  /** Current session display name, if set */
  get sessionName() {
    return this.sessionManager.getSessionName();
  }
  /** Scoped models for cycling (from --models flag) */
  get scopedModels() {
    return this._scopedModels;
  }
  /** Update scoped models for cycling */
  setScopedModels(scopedModels) {
    this._scopedModels = scopedModels;
  }
  /** File-based prompt templates */
  get promptTemplates() {
    return this._resourceLoader.getPrompts().prompts;
  }
  _normalizePromptSnippet(text) {
    if (!text)
      return void 0;
    const oneLine = text.replace(/[\r\n]+/g, " ").replace(/\s+/g, " ").trim();
    return oneLine.length > 0 ? oneLine : void 0;
  }
  _normalizePromptGuidelines(guidelines) {
    if (!guidelines || guidelines.length === 0) {
      return [];
    }
    const unique = /* @__PURE__ */ new Set();
    for (const guideline of guidelines) {
      const normalized = guideline.trim();
      if (normalized.length > 0) {
        unique.add(normalized);
      }
    }
    return Array.from(unique);
  }
  _rebuildSystemPrompt(toolNames) {
    const validToolNames = toolNames.filter((name) => this._toolRegistry.has(name));
    const toolSnippets = {};
    for (const name of this._toolRegistry.keys()) {
      const snippet = this._toolPromptSnippets.get(name);
      if (snippet)
        toolSnippets[name] = snippet;
    }
    const loaderSystemPrompt = this._resourceLoader.getSystemPrompt();
    const loaderAppendSystemPrompt = this._resourceLoader.getAppendSystemPrompt();
    const appendSystemPrompt = loaderAppendSystemPrompt.length > 0 ? loaderAppendSystemPrompt.join("\n\n") : "";
    const loadedSkills = this._resourceLoader.getSkills().skills;
    const loadedContextFiles = this._resourceLoader.getAgentsFiles().agentsFiles;
    this._baseSystemPromptOptions = normalizeBuildSystemPromptOptions({
      cwd: this._cwd,
      skills: loadedSkills,
      contextFiles: loadedContextFiles,
      customPrompt: loaderSystemPrompt,
      appendSystemPrompt,
      selectedTools: validToolNames,
      toolSnippets,
      toolGuidelines: Object.fromEntries(this._toolPromptGuidelines)
    });
  }
  /**
   * Apply a prompt and tool loadout for the next request. Sets the executable tools and
   * returns a system message patching the prompt sections the model currently has (replayed
   * from `messages`), or undefined when the prompt is unchanged. Tool changes are declared by
   * the agent loop before the request.
   *
   * A forced prompt does not affect the transcript: the structured sections are still diffed
   * and persisted, and the forced text is projected onto the request by
   * {@link _installAgentForcedPromptProjection}.
   */
  _preparePromptAndToolLoadout(options, messages = this.agent.state.messages) {
    options.selectedTools = [...new Set(options.selectedTools)].filter((name) => this._toolRegistry.has(name));
    this.agent.state.tools = options.selectedTools.flatMap((name) => {
      const tool = this._toolRegistry.get(name);
      return tool ? [tool] : [];
    });
    const sections = diffSystemPromptSections(getCurrentSystemMessage4(messages)?.sections ?? {}, buildSystemPromptSections(options));
    return sections ? { role: "system", content: "", sections, timestamp: Date.now() } : void 0;
  }
  /**
   * Send a forced prompt as the provider's leading system prompt without recording it.
   *
   * A `before_agent_start` handler that returns `systemPrompt` needs that exact text at the
   * head of the request; a mid-conversation system message would leave the original prompt
   * in place. The forced text is a rendering of the current prompt, so the transcript keeps
   * its structured sections and the request is projected instead: the system messages
   * collapse into one head holding the forced text and the current tools. Runs after the
   * `context` extension handlers.
   */
  _installAgentForcedPromptProjection() {
    const previousTransformContext = this.agent.transformContext;
    this.agent.transformContext = async (messages, signal) => {
      const transformed = previousTransformContext ? await previousTransformContext(messages, signal) : messages;
      const forced = this._runSystemPromptOptions?.forceSystemPrompt;
      if (forced === void 0)
        return transformed;
      const current = getCurrentSystemMessage4(transformed);
      const head = {
        role: "system",
        content: forced,
        ...current?.toolsAdded ? { toolsAdded: current.toolsAdded } : {},
        timestamp: current?.timestamp ?? Date.now()
      };
      return [head, ...transformed.filter((message) => message.role !== "system")];
    };
  }
  /** Restore the active tool loadout declared by the session transcript, if it declares one. */
  _restoreToolsFromTranscript() {
    const current = getCurrentSystemMessage4(this.sessionManager.buildSessionContext().messages);
    if (!current)
      return;
    const toolNames = (current.toolsAdded ?? []).map((tool) => tool.name).filter((name) => this._toolRegistry.has(name));
    this.agent.state.tools = toolNames.flatMap((name) => {
      const registered = this._toolRegistry.get(name);
      return registered ? [registered] : [];
    });
    this._rebuildSystemPrompt(toolNames);
  }
  // =========================================================================
  // Prompting
  // =========================================================================
  async _runAgentPrompt(messages) {
    this._agentRunAbortRequested = false;
    this._isAgentRunActive = true;
    try {
      await this.agent.prompt(messages);
      while (!this._agentRunAbortRequested) {
        if (await this._handlePostAgentRun()) {
          if (this._agentRunAbortRequested)
            break;
          await this.agent.continue();
          continue;
        }
        if (this._agentRunAbortRequested || !await this._runBeforeSettleBoundary())
          break;
        if (this._agentRunAbortRequested)
          break;
        await this.agent.continue();
      }
    } finally {
      if (this._agentRunAbortRequested)
        this._finishCancelledRetry();
      this._runSystemPromptOptions = void 0;
      this._flushPendingBashMessages();
      this._flushPendingCustomMessages();
      await this._emitAgentSettled();
    }
  }
  async _handlePostAgentRun() {
    const message = this._lastAssistantMessage;
    const toolResults = this._lastAssistantToolResults;
    this._lastAssistantMessage = void 0;
    this._lastAssistantToolResults = [];
    if (this._agentRunAbortRequested) {
      this._finishCancelledRetry();
      return false;
    }
    if (!message)
      return this.agent.hasQueuedMessages();
    if (this._isRetryableError(message) && await this._prepareRetry(message)) {
      if (this._agentRunAbortRequested)
        this._finishCancelledRetry();
      return !this._agentRunAbortRequested;
    }
    if (this._agentRunAbortRequested) {
      this._finishCancelledRetry();
      return false;
    }
    if (message.stopReason === "error" && this._retryAttempt > 0) {
      this._emit({
        type: "auto_retry_end",
        success: false,
        attempt: this._retryAttempt,
        finalError: message.errorMessage
      });
      this._retryAttempt = 0;
    }
    if (await this._checkCompaction(message, true, toolResults)) {
      return !this._agentRunAbortRequested;
    }
    return !this._agentRunAbortRequested && this.agent.hasQueuedMessages();
  }
  async _runBeforeSettleBoundary() {
    if (!this._extensionRunner.hasHandlers("agent_before_settle"))
      return this.agent.hasQueuedMessages();
    this._isBeforeSettle = true;
    this._abortDuringBeforeSettle = false;
    try {
      const result = await this._extensionRunner.emitBoundary({ type: "agent_before_settle", outcome: this._lastActivityOutcome }, (entries) => this._buildBoundaryContext(entries, "agent_before_settle"));
      this._commitBoundaryDrafts(result.entries);
      this._flushPendingCustomMessages();
      const finalContext = this._buildBoundaryContext([], "agent_before_settle");
      if (this._abortDuringBeforeSettle)
        return false;
      const shouldContinue = result.continue || this.agent.hasQueuedMessages();
      if (shouldContinue && !finalContext.canContinue) {
        if (result.continue)
          this._reportInvalidBoundaryContinuation("agent_before_settle");
        return false;
      }
      return shouldContinue;
    } finally {
      this._isBeforeSettle = false;
    }
  }
  async _runInputHandlers(text, images, source, streamingBehavior) {
    if (!this._extensionRunner.hasHandlers("input")) {
      return { text, images };
    }
    const inputResult = await this._extensionRunner.emitInput(text, images, source, streamingBehavior);
    if (inputResult.action === "handled") {
      return void 0;
    }
    if (inputResult.action === "transform") {
      return { text: inputResult.text, images: inputResult.images ?? images };
    }
    return { text, images };
  }
  async _normalizePromptImages(images) {
    if (!images)
      return { images: [], hints: [] };
    const normalizedImages = [];
    const hints = [];
    for (const image of images) {
      const processed = await processImage(Buffer.from(image.data, "base64"), image.mimeType, {
        autoResizeImages: this.settingsManager.getImageAutoResize(),
        resizeOptions: this.model?.inputLimits?.images?.resize
      });
      if (!processed.ok) {
        hints.push(processed.message);
        continue;
      }
      normalizedImages.push({ type: "image", data: processed.data, mimeType: processed.mimeType });
      hints.push(...processed.hints);
    }
    return { images: normalizedImages, hints };
  }
  /**
   * Send a prompt to the agent.
   * - Handles extension commands (registered via pi.registerCommand) immediately, even during streaming
   * - Expands file-based prompt templates by default
   * - During streaming, queues via steer() or followUp() based on streamingBehavior option
   * - Validates model and API key before sending (when not streaming)
   * @throws Error if streaming and no streamingBehavior specified
   * @throws Error if no model selected or no API key available (when not streaming)
   */
  async prompt(text, options) {
    if (this._isEmittingAgentSettled) {
      this._deferredSettledActions.push(async () => await this.prompt(text, options));
      return;
    }
    const expandPromptTemplates = options?.expandPromptTemplates ?? true;
    const preflightResult = options?.preflightResult;
    let messages;
    try {
      if (expandPromptTemplates && text.startsWith("/")) {
        const handled = await this._tryExecuteExtensionCommand(text);
        if (handled) {
          preflightResult?.(true);
          return;
        }
      }
      if (this._compactionAbortController !== void 0) {
        throw new Error("Cannot submit a prompt while compaction is in progress. Wait for compaction to finish and retry.");
      }
      const processedInput = await this._runInputHandlers(text, options?.images, options?.source ?? "interactive", this.isStreaming ? options?.streamingBehavior : void 0);
      if (!processedInput) {
        preflightResult?.(true);
        return;
      }
      const { text: currentText, images: currentImages } = processedInput;
      let expandedText = currentText;
      if (expandPromptTemplates) {
        expandedText = this._expandSkillCommand(expandedText);
        expandedText = expandPromptTemplate(expandedText, [...this.promptTemplates]);
      }
      if (this.isStreaming) {
        if (!options?.streamingBehavior) {
          throw new Error("Agent is already processing. Specify streamingBehavior ('steer' or 'followUp') to queue the message.");
        }
        if (options.streamingBehavior === "followUp") {
          await this._queueFollowUp(expandedText, currentImages);
        } else {
          await this._queueSteer(expandedText, currentImages);
        }
        preflightResult?.(true);
        return;
      }
      this._flushPendingBashMessages();
      this._flushPendingCustomMessages();
      if (!this.model) {
        throw new Error(formatNoModelSelectedMessage());
      }
      const hasConfiguredAuth = this._modelRuntime.hasConfiguredAuth(this.model.provider) || await this._modelRuntime.checkAuth(this.model.provider) !== void 0;
      if (!hasConfiguredAuth) {
        const isOAuth = this._modelRuntime.isUsingOAuth(this.model.provider);
        if (isOAuth) {
          throw new Error(`Authentication failed for "${this.model.provider}". Credentials may have expired or network is unavailable. Run '/login ${this.model.provider}' to re-authenticate.`);
        }
        throw new Error(formatNoApiKeyFoundMessage(this.model.provider));
      }
      const lastAssistant = this._findLastAssistantMessage();
      if (lastAssistant) {
        await this._checkCompaction(lastAssistant, false);
      }
      const selectedToolsBefore = this._baseSystemPromptOptions.selectedTools;
      const result = await this._extensionRunner.emitBeforeAgentStart(expandedText, currentImages, this._baseSystemPromptOptions);
      const handlerEditedTools = result.systemPromptOptions.selectedTools.length !== selectedToolsBefore.length || result.systemPromptOptions.selectedTools.some((name, index) => name !== selectedToolsBefore[index]);
      if (!handlerEditedTools)
        result.systemPromptOptions.selectedTools = this.getActiveToolNames();
      const normalized = await this._normalizePromptImages(currentImages);
      const userText = normalized.hints.length > 0 ? `${expandedText}

${normalized.hints.join("\n")}` : expandedText;
      messages = [];
      const userContent = [{ type: "text", text: userText }];
      userContent.push(...normalized.images);
      messages.push({
        role: "user",
        content: userContent,
        timestamp: Date.now()
      });
      for (const msg of this._pendingNextTurnMessages) {
        messages.push(msg);
      }
      this._pendingNextTurnMessages = [];
      for (const msg of result.messages) {
        messages.push({
          role: "custom",
          customType: msg.customType,
          // Untyped extensions can pass null/missing content; normalize at ingestion.
          content: msg.content ?? [],
          display: msg.display,
          details: msg.details,
          timestamp: Date.now()
        });
      }
      const updateMessage = this._preparePromptAndToolLoadout(result.systemPromptOptions);
      this._runSystemPromptOptions = result.systemPromptOptions;
      if (updateMessage)
        messages.unshift(updateMessage);
    } catch (error) {
      preflightResult?.(false);
      throw error;
    }
    if (!messages) {
      return;
    }
    preflightResult?.(true);
    await this._runAgentPrompt(messages);
  }
  /**
   * Try to execute an extension command. Returns true if command was found and executed.
   */
  async _tryExecuteExtensionCommand(text) {
    const spaceIndex = text.indexOf(" ");
    const commandName = spaceIndex === -1 ? text.slice(1) : text.slice(1, spaceIndex);
    const args = spaceIndex === -1 ? "" : text.slice(spaceIndex + 1);
    const command = this._extensionRunner.getCommand(commandName);
    if (!command)
      return false;
    const ctx = this._extensionRunner.createCommandContext();
    try {
      await command.handler(args, ctx);
      return true;
    } catch (err) {
      this._extensionRunner.emitError({
        extensionPath: `command:${commandName}`,
        event: "command",
        error: err instanceof Error ? err.message : String(err)
      });
      return true;
    }
  }
  /**
   * Expand skill commands (/skill:name args) to their full content.
   * Returns the expanded text, or the original text if not a skill command or skill not found.
   * Emits errors via extension runner if file read fails.
   */
  _expandSkillCommand(text) {
    if (!text.startsWith("/skill:"))
      return text;
    const spaceIndex = text.indexOf(" ");
    const skillName = spaceIndex === -1 ? text.slice(7) : text.slice(7, spaceIndex);
    const args = spaceIndex === -1 ? "" : text.slice(spaceIndex + 1).trim();
    const skill = this.resourceLoader.getSkills().skills.find((s) => s.name === skillName);
    if (!skill)
      return text;
    try {
      const content = readFileSync5(skill.filePath, "utf-8");
      const body = stripFrontmatter(content).trim();
      const skillBlock = `<skill name="${skill.name}" location="${skill.filePath}">
References are relative to ${skill.baseDir}.

${body}
</skill>`;
      return args ? `${skillBlock}

${args}` : skillBlock;
    } catch (err) {
      this._extensionRunner.emitError({
        extensionPath: skill.filePath,
        event: "skill_expansion",
        error: err instanceof Error ? err.message : String(err)
      });
      return text;
    }
  }
  async _queueUserInput(text, images, behavior, source) {
    if (text.startsWith("/")) {
      this._throwIfExtensionCommand(text);
    }
    const processedInput = await this._runInputHandlers(text, images, source, this.isStreaming ? behavior : void 0);
    if (!processedInput)
      return;
    let expandedText = this._expandSkillCommand(processedInput.text);
    expandedText = expandPromptTemplate(expandedText, [...this.promptTemplates]);
    if (behavior === "steer") {
      await this._queueSteer(expandedText, processedInput.images);
    } else {
      await this._queueFollowUp(expandedText, processedInput.images);
    }
  }
  /**
   * Queue a steering message while the agent is running.
   * Delivered after the current assistant turn finishes executing its tool calls,
   * before the next LLM call.
   * Expands skill commands and prompt templates. Errors on extension commands.
   * @param images Optional image attachments to include with the message
   * @param options Input source; defaults to interactive
   * @throws Error if text is an extension command
   */
  async steer(text, images, options) {
    await this._queueUserInput(text, images, "steer", options?.source ?? "interactive");
  }
  /**
   * Queue a follow-up message to be processed after the agent finishes.
   * Delivered only when agent has no more tool calls or steering messages.
   * Expands skill commands and prompt templates. Errors on extension commands.
   * @param images Optional image attachments to include with the message
   * @param options Input source; defaults to interactive
   * @throws Error if text is an extension command
   */
  async followUp(text, images, options) {
    await this._queueUserInput(text, images, "followUp", options?.source ?? "interactive");
  }
  /**
   * Internal: Queue a steering message (already expanded, no extension command check).
   */
  async _queueSteer(text, images) {
    this._steeringMessages.push(text);
    this._emitQueueUpdate();
    const content = [{ type: "text", text }];
    if (images) {
      content.push(...images);
    }
    this.agent.steer({
      role: "user",
      content,
      timestamp: Date.now()
    });
  }
  /**
   * Internal: Queue a follow-up message (already expanded, no extension command check).
   */
  async _queueFollowUp(text, images) {
    this._followUpMessages.push(text);
    this._emitQueueUpdate();
    const content = [{ type: "text", text }];
    if (images) {
      content.push(...images);
    }
    this.agent.followUp({ role: "user", content, timestamp: Date.now() });
  }
  /**
   * Throw an error if the text is an extension command.
   */
  _throwIfExtensionCommand(text) {
    const spaceIndex = text.indexOf(" ");
    const commandName = spaceIndex === -1 ? text.slice(1) : text.slice(1, spaceIndex);
    const command = this._extensionRunner.getCommand(commandName);
    if (command) {
      throw new Error(`Extension command "/${commandName}" cannot be queued. Use prompt() or execute the command when not streaming.`);
    }
  }
  /**
   * Send a custom message to the session. Creates a CustomMessageEntry.
   *
   * Handles four cases:
   * - Streaming: queues message, processed when loop pulls from queue
   * - Streaming + triggerTurn false: appended to state/session once the current turn ends
   * - Not streaming + triggerTurn: appends to state/session, starts new turn
   * - Not streaming + no trigger: appends to state/session, no turn
   *
   * @param message Custom message with customType, content, display, details
   * @param options.triggerTurn If true and not streaming, triggers a new LLM turn
   * @param options.deliverAs Delivery mode: "steer", "followUp", or "nextTurn"
   */
  async sendCustomMessage(message, options) {
    const appMessage = {
      role: "custom",
      customType: message.customType,
      // Untyped extensions can pass null/missing content; normalize at ingestion.
      content: message.content ?? [],
      display: message.display,
      details: message.details,
      timestamp: Date.now()
    };
    if (options?.deliverAs === "nextTurn") {
      this._pendingNextTurnMessages.push(appMessage);
    } else if (this.isStreaming && options?.triggerTurn !== false) {
      if (options?.deliverAs === "followUp") {
        this.agent.followUp(appMessage);
      } else {
        this.agent.steer(appMessage);
      }
    } else if (options?.triggerTurn) {
      if (this._isEmittingAgentSettled) {
        this._deferredSettledActions.push(async () => await this._runAgentPrompt(appMessage));
        return;
      }
      await this._runAgentPrompt(appMessage);
    } else if (this.isStreaming) {
      this._pendingCustomMessages.push(appMessage);
    } else {
      this._appendCustomMessage(appMessage);
    }
  }
  _appendCustomMessage(appMessage) {
    this.sessionManager.appendCustomMessageEntry(appMessage.customType, appMessage.content, appMessage.display, appMessage.details);
    this._refreshFinalizedContext();
    this._emit({ type: "message_start", message: appMessage });
    this._emit({ type: "message_end", message: appMessage });
  }
  /**
   * Append custom messages queued while the agent was running.
   * Called once the current turn's tool results are in agent state and session history.
   */
  _flushPendingCustomMessages() {
    if (this._pendingCustomMessages.length === 0)
      return;
    const pending = this._pendingCustomMessages;
    this._pendingCustomMessages = [];
    for (const appMessage of pending) {
      this._appendCustomMessage(appMessage);
    }
  }
  /**
   * Send a user message to the agent. Always triggers a turn.
   * When the agent is streaming, use deliverAs to specify how to queue the message.
   *
   * @param content User message content (string or content array)
   * @param options.deliverAs Delivery mode when streaming: "steer" or "followUp"
   * @param options.expandPromptTemplates Whether to dispatch extension commands and expand skill commands and prompt templates. Default: false.
   */
  async sendUserMessage(content, options) {
    let text;
    let images;
    if (typeof content === "string") {
      text = content;
    } else {
      const textParts = [];
      images = [];
      for (const part of content) {
        if (part.type === "text") {
          textParts.push(part.text);
        } else {
          images.push(part);
        }
      }
      text = textParts.join("\n");
      if (images.length === 0)
        images = void 0;
    }
    await this.prompt(text, {
      expandPromptTemplates: options?.expandPromptTemplates ?? false,
      streamingBehavior: options?.deliverAs,
      images,
      source: "extension"
    });
  }
  /**
   * Clear all queued messages and return them.
   * Useful for restoring to editor when user aborts.
   * @returns Object with steering and followUp arrays
   */
  clearQueue() {
    const steering = [...this._steeringMessages];
    const followUp = [...this._followUpMessages];
    this._steeringMessages = [];
    this._followUpMessages = [];
    this.agent.clearAllQueues();
    this._emitQueueUpdate();
    return { steering, followUp };
  }
  /** Number of pending messages (includes both steering and follow-up) */
  get pendingMessageCount() {
    return this._steeringMessages.length + this._followUpMessages.length;
  }
  /** Get pending steering messages (read-only) */
  getSteeringMessages() {
    return this._steeringMessages;
  }
  /** Get pending follow-up messages (read-only) */
  getFollowUpMessages() {
    return this._followUpMessages;
  }
  get resourceLoader() {
    return this._resourceLoader;
  }
  /**
   * Abort current operation and wait for agent to become idle.
   */
  async abort() {
    if (this._isAgentRunActive) {
      this._agentRunAbortRequested = true;
    }
    this.abortRetry();
    this.abortCompaction();
    this.abortBranchSummary();
    if (this._isBeforeSettle)
      this._abortDuringBeforeSettle = true;
    this.agent.abort();
    await this.waitForIdle();
  }
  async waitForIdle() {
    if (this.isIdle) {
      return;
    }
    await this._getIdleWaitPromise();
  }
  // =========================================================================
  // Model Management
  // =========================================================================
  async _emitModelSelect(nextModel, previousModel, source) {
    if (modelsAreEqual(previousModel, nextModel))
      return;
    await this._extensionRunner.emit({
      type: "model_select",
      model: nextModel,
      previousModel,
      source
    });
  }
  /**
   * Set model directly.
   * Validates that auth is configured and saves to the session transcript.
   * Persists to global defaults only when options.persist is true.
   * @throws Error if no auth is configured for the model
   */
  async setModel(model, options = {}) {
    if (!await this._modelRuntime.checkAuth(model.provider)) {
      throw new Error(`No API key for ${model.provider}/${model.id}`);
    }
    const previousModel = this.model;
    const thinkingLevel = this._getThinkingLevelForModelSwitch(model);
    this.agent.state.model = model;
    this.sessionManager.appendModelChange(model.provider, model.id);
    if (options.persist) {
      this.settingsManager.setDefaultModelAndProvider(model.provider, model.id);
      this._addPersistedDefaultToNonEmptyScope(model);
    }
    this.setThinkingLevel(thinkingLevel);
    await this._emitModelSelect(model, previousModel, "set");
  }
  _addPersistedDefaultToNonEmptyScope(model) {
    if (this._scopedModels.length === 0)
      return;
    if (this._scopedModels.some((scoped) => modelsAreEqual(scoped.model, model)))
      return;
    this._scopedModels = [...this._scopedModels, { model }];
    const enabledModels = this.settingsManager.getEnabledModels();
    if (!enabledModels?.length)
      return;
    const modelReference = `${model.provider}/${model.id}`;
    if (enabledModels.some((pattern) => pattern.toLowerCase() === modelReference.toLowerCase()))
      return;
    this.settingsManager.setEnabledModels([...enabledModels, modelReference]);
  }
  /**
   * Cycle to next/previous model.
   * Uses scoped models (from --models flag) if available, otherwise all available models.
   * @param direction - "forward" (default) or "backward"
   * @returns The new model info, or undefined if only one model available
   */
  async cycleModel(direction = "forward", options = {}) {
    if (this._scopedModels.length > 0) {
      return this._cycleScopedModel(direction, options);
    }
    return this._cycleAvailableModel(direction, options);
  }
  async _cycleScopedModel(direction, options) {
    const availableIds = new Set(this._modelRuntime.getAvailableSnapshot().map((model) => `${model.provider}\0${model.id}`));
    const scopedModels = this._scopedModels.filter((scoped) => availableIds.has(`${scoped.model.provider}\0${scoped.model.id}`));
    if (scopedModels.length <= 1)
      return void 0;
    const currentModel = this.model;
    let currentIndex = scopedModels.findIndex((sm) => modelsAreEqual(sm.model, currentModel));
    if (currentIndex === -1)
      currentIndex = 0;
    const len = scopedModels.length;
    const nextIndex = direction === "forward" ? (currentIndex + 1) % len : (currentIndex - 1 + len) % len;
    const next = scopedModels[nextIndex];
    const thinkingLevel = this._getThinkingLevelForModelSwitch(next.model, next.thinkingLevel);
    this.agent.state.model = next.model;
    this.sessionManager.appendModelChange(next.model.provider, next.model.id);
    if (options.persist) {
      this.settingsManager.setDefaultModelAndProvider(next.model.provider, next.model.id);
      this._addPersistedDefaultToNonEmptyScope(next.model);
    }
    this.setThinkingLevel(thinkingLevel);
    await this._emitModelSelect(next.model, currentModel, "cycle");
    return { model: next.model, thinkingLevel: this.thinkingLevel, isScoped: true };
  }
  async _cycleAvailableModel(direction, options) {
    const availableModels = this._modelRuntime.getAvailableSnapshot();
    if (availableModels.length <= 1)
      return void 0;
    const currentModel = this.model;
    let currentIndex = availableModels.findIndex((m) => modelsAreEqual(m, currentModel));
    if (currentIndex === -1)
      currentIndex = 0;
    const len = availableModels.length;
    const nextIndex = direction === "forward" ? (currentIndex + 1) % len : (currentIndex - 1 + len) % len;
    const nextModel = availableModels[nextIndex];
    const thinkingLevel = this._getThinkingLevelForModelSwitch(nextModel);
    this.agent.state.model = nextModel;
    this.sessionManager.appendModelChange(nextModel.provider, nextModel.id);
    if (options.persist) {
      this.settingsManager.setDefaultModelAndProvider(nextModel.provider, nextModel.id);
      this._addPersistedDefaultToNonEmptyScope(nextModel);
    }
    this.setThinkingLevel(thinkingLevel);
    await this._emitModelSelect(nextModel, currentModel, "cycle");
    return { model: nextModel, thinkingLevel: this.thinkingLevel, isScoped: false };
  }
  // =========================================================================
  // Thinking Level Management
  // =========================================================================
  /**
   * Set thinking level.
   * Clamps to model capabilities based on available thinking levels.
   * Saves the clamped level to the session transcript only if the level actually changes.
   * Persists the requested level to global defaults only when options.persist is true.
   */
  setThinkingLevel(level, options = {}) {
    const availableLevels = this.getAvailableThinkingLevels();
    const effectiveLevel = availableLevels.includes(level) ? level : this._clampThinkingLevel(level, availableLevels);
    const previousLevel = this.agent.state.thinkingLevel;
    const isChanging = effectiveLevel !== previousLevel;
    this.agent.state.thinkingLevel = effectiveLevel;
    if (options.persist) {
      this.settingsManager.setDefaultThinkingLevel(level);
    }
    if (isChanging) {
      this.sessionManager.appendThinkingLevelChange(effectiveLevel);
      this._emit({ type: "thinking_level_changed", level: effectiveLevel });
      void this._extensionRunner.emit({
        type: "thinking_level_select",
        level: effectiveLevel,
        previousLevel
      });
    }
  }
  /**
   * Cycle to next thinking level.
   * @returns New level, or undefined if model doesn't support thinking
   */
  cycleThinkingLevel(options = {}) {
    if (!this.supportsThinking())
      return void 0;
    const levels = this.getAvailableThinkingLevels();
    const currentIndex = levels.indexOf(this.thinkingLevel);
    const nextIndex = (currentIndex + 1) % levels.length;
    const nextLevel = levels[nextIndex];
    this.setThinkingLevel(nextLevel, options);
    return nextLevel;
  }
  /**
   * Get available thinking levels for current model.
   * The provider will clamp to what the specific model supports internally.
   */
  getAvailableThinkingLevels() {
    if (!this.model)
      return [...THINKING_LEVEL_OPTIONS];
    return getSupportedThinkingLevels(this.model);
  }
  /**
   * Check if current model supports thinking/reasoning.
   */
  supportsThinking() {
    return !!this.model?.reasoning;
  }
  _getThinkingLevelForModelSwitch(targetModel, explicitLevel) {
    if (explicitLevel !== void 0) {
      return explicitLevel;
    }
    if (targetModel) {
      const perModel = this.settingsManager.getModelThinkingLevel(targetModel.provider, targetModel.id);
      if (perModel !== void 0) {
        return perModel;
      }
    }
    return this.settingsManager.getDefaultThinkingLevel() ?? this.thinkingLevel ?? DEFAULT_THINKING_LEVEL;
  }
  _clampThinkingLevel(level, _availableLevels) {
    return this.model ? clampThinkingLevel(this.model, level) : "off";
  }
  // =========================================================================
  // Queue Mode Management
  // =========================================================================
  syncQueueModesFromSettings() {
    this.agent.steeringMode = this.settingsManager.getSteeringMode();
    this.agent.followUpMode = this.settingsManager.getFollowUpMode();
  }
  /**
   * Set steering message mode.
   * Saves to settings.
   */
  setSteeringMode(mode) {
    this.agent.steeringMode = mode;
    this.settingsManager.setSteeringMode(mode);
  }
  /**
   * Set follow-up message mode.
   * Saves to settings.
   */
  setFollowUpMode(mode) {
    this.agent.followUpMode = mode;
    this.settingsManager.setFollowUpMode(mode);
  }
  // =========================================================================
  // Compaction
  // =========================================================================
  /** Generate Pi's built-in compaction summary for manual and automatic compaction. */
  async _runDefaultCompaction(preparation, requestModel, apiKey, headers, customInstructions, signal, env, reason) {
    return compact(preparation, requestModel, apiKey, headers, customInstructions, signal, this.thinkingLevel, this.agent.streamFunction, env, this.settingsManager.getRetrySettings(), this._summarizationRetryCallbacks({ source: "compaction", reason }), void 0);
  }
  _clearManualCompactionState() {
    this._compactionAbortController = void 0;
    this._resolveIdleWaitIfIdle();
  }
  /**
   * Manually compact the session context.
   *
   * This is the manual entry point used by `/compact`, RPC, and extensions. It is
   * separate from automatic threshold/overflow compaction, which enters through
   * `_checkCompaction()` and `_runAutoCompaction()`. After preparation and the
   * `session_before_compact` hook, both paths call the lower-level `compact()`
   * function imported from `./compaction/index.ts`, unless the hook cancels or
   * supplies a custom result.
   *
   * Aborts the current agent operation first. Manual compaction never retries or
   * continues the interrupted agent turn.
   *
   * @param customInstructions Optional instructions for the compaction summary
   */
  async compact(customInstructions) {
    await this.abort();
    this._compactionAbortController = new AbortController();
    this._emit({ type: "compaction_start", reason: "manual" });
    let fromExtension = false;
    let cancelledByExtension = false;
    try {
      const model = this.model;
      if (!model) {
        throw new Error(formatNoModelSelectedMessage());
      }
      const settings = this.settingsManager.getCompactionSettings(model);
      const { model: requestModel, apiKey, headers, env } = await this._getSummarizationRequestAuth(model, this._compactionAbortController.signal);
      const pathEntries = this.sessionManager.getBranch();
      const preparation = prepareCompaction(pathEntries, settings);
      if (!preparation) {
        const lastEntry = pathEntries[pathEntries.length - 1];
        if (lastEntry?.type === "compaction") {
          throw new Error("Already compacted");
        }
        throw new Error("Nothing to compact (session too small)");
      }
      let extensionCompaction;
      if (this._extensionRunner.hasHandlers("session_before_compact")) {
        const result = await this._extensionRunner.emit({
          type: "session_before_compact",
          preparation,
          branchEntries: pathEntries,
          customInstructions,
          reason: "manual",
          willRetry: false,
          signal: this._compactionAbortController.signal
        });
        if (result?.cancel) {
          cancelledByExtension = true;
          throw new Error("Compaction cancelled");
        }
        if (result?.compaction) {
          extensionCompaction = result.compaction;
          fromExtension = true;
        }
      }
      let summary;
      let firstKeptEntryId;
      let tokensBefore;
      let usage;
      let details;
      if (extensionCompaction) {
        summary = extensionCompaction.summary;
        firstKeptEntryId = extensionCompaction.firstKeptEntryId;
        tokensBefore = extensionCompaction.tokensBefore;
        usage = extensionCompaction.usage;
        details = extensionCompaction.details;
      } else {
        const result = await this._runDefaultCompaction(preparation, requestModel, apiKey, headers, customInstructions, this._compactionAbortController.signal, env, "manual");
        summary = result.summary;
        firstKeptEntryId = result.firstKeptEntryId;
        tokensBefore = result.tokensBefore;
        usage = result.usage;
        details = result.details;
      }
      if (this._compactionAbortController.signal.aborted) {
        throw new Error("Compaction cancelled");
      }
      this.sessionManager.appendCompaction(summary, firstKeptEntryId, tokensBefore, details, fromExtension, usage);
      const newEntries = this.sessionManager.getEntries();
      this._refreshFinalizedContext();
      const estimatedTokensAfter = estimateMessagesTokens(this.sessionManager.buildSessionProjection().messages);
      const savedCompactionEntry = newEntries.find((e) => e.type === "compaction" && e.summary === summary);
      if (this._extensionRunner && savedCompactionEntry) {
        await this._extensionRunner.emit({
          type: "session_compact",
          compactionEntry: savedCompactionEntry,
          fromExtension,
          reason: "manual",
          willRetry: false
        });
      }
      const compactionResult = {
        summary,
        firstKeptEntryId,
        tokensBefore,
        estimatedTokensAfter,
        usage,
        details
      };
      this._clearManualCompactionState();
      this._emit({
        type: "compaction_end",
        reason: "manual",
        result: compactionResult,
        aborted: false,
        willRetry: false
      });
      return compactionResult;
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      const aborted = this._compactionAbortController.signal.aborted || cancelledByExtension;
      const errorMessage = aborted ? void 0 : `Compaction failed: ${message}`;
      this._clearManualCompactionState();
      this._emit({
        type: "compaction_end",
        reason: "manual",
        result: void 0,
        aborted,
        willRetry: false,
        errorMessage
      });
      await this._emitSessionCompactFailed({
        reason: "manual",
        errorMessage,
        aborted,
        willRetry: false,
        fromExtension
      });
      throw error;
    } finally {
      this._clearManualCompactionState();
    }
  }
  /**
   * Cancel in-progress compaction (manual or auto).
   */
  abortCompaction() {
    this._compactionAbortController?.abort();
    this._autoCompactionAbortController?.abort();
  }
  /**
   * Cancel in-progress branch summarization.
   */
  abortBranchSummary() {
    this._branchSummaryAbortController?.abort();
  }
  /**
   * Dispatch automatic compaction after `agent_end` or before prompt submission.
   * Manual compaction does not call this method; it enters through `compact()`.
   *
   * Automatic cases:
   * 1. Overflow with retry: a context-overflow error or recoverable length stop;
   *    remove the failed assistant message, compact, and retry the turn once.
   * 2. Overflow without retry: a successful response exceeded the configured
   *    context window; compact but preserve the completed response.
   * 3. Threshold without retry: valid or estimated context usage crossed the
   *    configured threshold; compact without retrying the completed response.
   *
   * Each case calls `_runAutoCompaction()`. After preparation and the
   * `session_before_compact` hook, that method calls the lower-level `compact()`
   * function imported from `./compaction/index.ts`, unless the hook cancels or
   * supplies a custom result.
   *
   * @param assistantMessage The assistant message to check
   * @param skipAbortedCheck If false, include aborted messages (for pre-prompt check). Default: true
   * @returns Whether the post-run loop should call `agent.continue()` for overflow recovery or queued messages
   */
  async _checkCompaction(assistantMessage, skipAbortedCheck = true, toolResults = []) {
    const settings = this.settingsManager.getCompactionSettings(this.model);
    if (!settings.enabled)
      return false;
    if (skipAbortedCheck && assistantMessage.stopReason === "aborted")
      return false;
    const contextWindow = this.model?.contextWindow ?? 0;
    const sameModel = this.model && assistantMessage.provider === this.model.provider && assistantMessage.model === this.model.id;
    const compactionEntry = getLatestCompactionEntry(this.sessionManager.getBranch());
    const assistantIsFromBeforeCompaction = compactionEntry !== null && assistantMessage.timestamp <= new Date(compactionEntry.timestamp).getTime();
    if (assistantIsFromBeforeCompaction) {
      return false;
    }
    const currentProjection = this.sessionManager.buildSessionProjection();
    const assistantEntryId = this._findPersistedMessageEntryId(assistantMessage);
    const assistantIsProjected = assistantEntryId === void 0 || currentProjection.entries.some((entry) => entry.sourceEntry.id === assistantEntryId && entry.messages.some((message) => message.role === "assistant"));
    const branch = this.sessionManager.getBranch();
    const assistantIndex = assistantEntryId ? branch.findIndex((entry) => entry.id === assistantEntryId) : -1;
    const entriesAfterAssistant = assistantIndex >= 0 ? branch.slice(assistantIndex + 1) : [];
    const hasPostAssistantContextEdit = entriesAfterAssistant.some((entry) => entry.type === "context_edit");
    const latestAssistantEdit = entriesAfterAssistant.filter((entry) => entry.type === "context_edit" && entry.targetId === assistantEntryId).at(-1);
    const assistantRetainedForExplicitRecovery = assistantEntryId === void 0 || !entriesAfterAssistant.some((entry) => entry.type === "compaction") && latestAssistantEdit?.replacement !== null;
    const assistantUsageMatchesProjection = assistantIsProjected && !hasPostAssistantContextEdit;
    const explicitOverflow = assistantMessage.stopReason === "error" && isContextOverflow(assistantMessage);
    const contextOverflow = sameModel && (explicitOverflow && assistantRetainedForExplicitRecovery || assistantUsageMatchesProjection && isContextOverflow(assistantMessage, contextWindow));
    const recoverableLength = sameModel && assistantIsProjected && isRecoverableLength(assistantMessage, this.model?.maxTokens ?? 0);
    if (contextOverflow || recoverableLength) {
      const willRetry = assistantMessage.stopReason !== "stop";
      if (!willRetry) {
        return await this._runAutoCompaction("overflow", false);
      }
      if (this._overflowRecoveryAttempted) {
        const errorMessage = contextOverflow ? "Context overflow recovery failed after one compact-and-retry attempt. Try reducing context or switching to a larger-context model." : "Truncated response recovery failed after one compact-and-retry attempt.";
        this._emit({
          type: "compaction_end",
          reason: "overflow",
          result: void 0,
          aborted: false,
          willRetry: false,
          errorMessage
        });
        await this._emitSessionCompactFailed({
          reason: "overflow",
          errorMessage,
          aborted: false,
          willRetry: false,
          fromExtension: false
        });
        return false;
      }
      this._overflowRecoveryAttempted = true;
      this._omitRecoveryAttempt(assistantMessage, toolResults);
      return await this._runAutoCompaction("overflow", willRetry);
    }
    let contextTokens;
    const projection = currentProjection;
    const hasContextEdits = projection.entries.some((entry) => entry.sourceEntry.type === "context_edit");
    const directContextTokens = assistantMessage.usage ? calculateContextTokens(assistantMessage.usage) : 0;
    if (hasContextEdits) {
      contextTokens = estimateProjectedContextTokens(projection, branch).tokens;
    } else if (assistantMessage.stopReason === "error" || directContextTokens === 0) {
      const messages = this.agent.state.messages;
      const estimate = estimateContextTokens(messages);
      if (estimate.lastUsageIndex !== null) {
        const usageMsg = messages[estimate.lastUsageIndex];
        if (compactionEntry && usageMsg.role === "assistant" && usageMsg.timestamp <= new Date(compactionEntry.timestamp).getTime()) {
          return false;
        }
      }
      contextTokens = estimate.tokens;
    } else {
      contextTokens = directContextTokens;
    }
    if (shouldCompact(contextTokens, contextWindow, settings)) {
      return await this._runAutoCompaction("threshold", false);
    }
    return false;
  }
  /**
   * Execute threshold or overflow compaction. Manual compaction uses
   * `AgentSession.compact()` instead. Both paths call the lower-level `compact()`
   * function imported from `./compaction/index.ts` after preparation and extension
   * interception.
   *
   * @param reason Automatic trigger selected by `_checkCompaction()`
   * @param willRetry Whether to continue the interrupted turn after overflow compaction
   * @returns Whether the post-run loop should call `agent.continue()`
   */
  async _runAutoCompaction(reason, willRetry) {
    const model = this.model;
    const settings = this.settingsManager.getCompactionSettings(model);
    let abortController;
    let started = false;
    let fromExtension = false;
    let cancelledByExtension = false;
    try {
      if (!model) {
        return false;
      }
      const pathEntries = this.sessionManager.getBranch();
      const preparation = prepareCompaction(pathEntries, settings);
      if (!preparation) {
        return false;
      }
      abortController = new AbortController();
      this._autoCompactionAbortController = abortController;
      started = true;
      this._emit({ type: "compaction_start", reason });
      abortController.signal.throwIfAborted();
      const { model: requestModel, apiKey, headers, env } = await this._getSummarizationRequestAuth(model, abortController.signal);
      abortController.signal.throwIfAborted();
      let extensionCompaction;
      if (this._extensionRunner.hasHandlers("session_before_compact")) {
        const extensionResult = await this._extensionRunner.emit({
          type: "session_before_compact",
          preparation,
          branchEntries: pathEntries,
          customInstructions: void 0,
          reason,
          willRetry,
          signal: abortController.signal
        });
        if (extensionResult?.cancel) {
          cancelledByExtension = true;
          throw new Error("Compaction cancelled");
        }
        if (extensionResult?.compaction) {
          extensionCompaction = extensionResult.compaction;
          fromExtension = true;
        }
      }
      abortController.signal.throwIfAborted();
      let summary;
      let firstKeptEntryId;
      let tokensBefore;
      let usage;
      let details;
      if (extensionCompaction) {
        summary = extensionCompaction.summary;
        firstKeptEntryId = extensionCompaction.firstKeptEntryId;
        tokensBefore = extensionCompaction.tokensBefore;
        usage = extensionCompaction.usage;
        details = extensionCompaction.details;
      } else {
        const compactResult = await this._runDefaultCompaction(preparation, requestModel, apiKey, headers, void 0, abortController.signal, env, reason);
        summary = compactResult.summary;
        firstKeptEntryId = compactResult.firstKeptEntryId;
        tokensBefore = compactResult.tokensBefore;
        usage = compactResult.usage;
        details = compactResult.details;
      }
      abortController.signal.throwIfAborted();
      this.sessionManager.appendCompaction(summary, firstKeptEntryId, tokensBefore, details, fromExtension, usage);
      const newEntries = this.sessionManager.getEntries();
      this._refreshFinalizedContext();
      const estimatedTokensAfter = estimateMessagesTokens(this.sessionManager.buildSessionProjection().messages);
      const savedCompactionEntry = newEntries.find((e) => e.type === "compaction" && e.summary === summary);
      if (this._extensionRunner && savedCompactionEntry) {
        await this._extensionRunner.emit({
          type: "session_compact",
          compactionEntry: savedCompactionEntry,
          fromExtension,
          reason,
          willRetry
        });
      }
      const result = {
        summary,
        firstKeptEntryId,
        tokensBefore,
        estimatedTokensAfter,
        usage,
        details
      };
      this._emit({ type: "compaction_end", reason, result, aborted: false, willRetry });
      if (willRetry)
        return true;
      return this.agent.hasQueuedMessages();
    } catch (error) {
      const message = error instanceof Error ? error.message : "compaction failed";
      const aborted = abortController?.signal.aborted === true || cancelledByExtension;
      if (started) {
        const errorMessage = aborted ? void 0 : reason === "overflow" ? `Context overflow recovery failed: ${message}` : `Auto-compaction failed: ${message}`;
        this._emit({
          type: "compaction_end",
          reason,
          result: void 0,
          aborted,
          willRetry: false,
          errorMessage
        });
        await this._emitSessionCompactFailed({
          reason,
          errorMessage,
          aborted,
          willRetry: false,
          fromExtension
        });
      }
      return false;
    } finally {
      if (this._autoCompactionAbortController === abortController) {
        this._autoCompactionAbortController = void 0;
      }
      this._resolveIdleWaitIfIdle();
    }
  }
  /**
   * Toggle auto-compaction setting.
   */
  setAutoCompactionEnabled(enabled) {
    this.settingsManager.setCompactionEnabled(enabled);
  }
  /** Whether auto-compaction is enabled */
  get autoCompactionEnabled() {
    return this.settingsManager.getCompactionEnabled();
  }
  async bindExtensions(bindings) {
    if (bindings.uiContext !== void 0) {
      this._extensionUIContext = bindings.uiContext;
    }
    if (bindings.mode !== void 0) {
      this._extensionMode = bindings.mode;
    }
    if (bindings.commandContextActions !== void 0) {
      this._extensionCommandContextActions = bindings.commandContextActions;
    }
    if (bindings.abortHandler !== void 0) {
      this._extensionAbortHandler = bindings.abortHandler;
    }
    if (bindings.shutdownHandler !== void 0) {
      this._extensionShutdownHandler = bindings.shutdownHandler;
    }
    if (bindings.onError !== void 0) {
      this._extensionErrorListener = bindings.onError;
    }
    this._applyExtensionBindings(this._extensionRunner);
    await this._extensionRunner.emit(this._sessionStartEvent);
    await this.extendResourcesFromExtensions(this._sessionStartEvent.reason === "reload" ? "reload" : "startup");
  }
  async extendResourcesFromExtensions(reason) {
    if (!this._extensionRunner.hasHandlers("resources_discover")) {
      return;
    }
    const { skillPaths, promptPaths, themePaths } = await this._extensionRunner.emitResourcesDiscover(this._cwd, reason);
    if (skillPaths.length === 0 && promptPaths.length === 0 && themePaths.length === 0) {
      return;
    }
    const extensionPaths = {
      skillPaths: this.buildExtensionResourcePaths(skillPaths),
      promptPaths: this.buildExtensionResourcePaths(promptPaths),
      themePaths: this.buildExtensionResourcePaths(themePaths)
    };
    this._resourceLoader.extendResources(extensionPaths);
    this._rebuildSystemPrompt(this.getActiveToolNames());
  }
  buildExtensionResourcePaths(entries) {
    return entries.map((entry) => {
      const source = this.getExtensionSourceLabel(entry.extensionPath);
      const baseDir = entry.extensionPath.startsWith("<") ? void 0 : dirname5(entry.extensionPath);
      return {
        path: entry.path,
        metadata: {
          source,
          scope: "temporary",
          origin: "top-level",
          baseDir
        }
      };
    });
  }
  getExtensionSourceLabel(extensionPath) {
    if (extensionPath.startsWith("<")) {
      return `extension:${extensionPath.replace(/[<>]/g, "")}`;
    }
    const base = basename5(extensionPath);
    const name = base.replace(/\.(ts|js)$/, "");
    return `extension:${name}`;
  }
  _applyExtensionBindings(runner) {
    runner.setUIContext(this._extensionUIContext, this._extensionMode);
    runner.bindCommandContext(this._extensionCommandContextActions);
    this._extensionErrorUnsubscriber?.();
    this._extensionErrorUnsubscriber = this._extensionErrorListener ? runner.onError(this._extensionErrorListener) : void 0;
  }
  _refreshCurrentModelFromRegistry() {
    const currentModel = this.model;
    if (!currentModel) {
      return;
    }
    const refreshedModel = this._modelRuntime.getModel(currentModel.provider, currentModel.id);
    if (!refreshedModel || refreshedModel === currentModel) {
      return;
    }
    this.agent.state.model = refreshedModel;
  }
  _bindExtensionCore(runner) {
    const getCommands = /* @__PURE__ */ __name(() => {
      const extensionCommands = runner.getRegisteredCommands().map((command) => ({
        name: command.invocationName,
        description: command.description,
        source: "extension",
        sourceInfo: command.sourceInfo
      }));
      const templates = this.promptTemplates.map((template) => ({
        name: template.name,
        description: template.description,
        source: "prompt",
        sourceInfo: template.sourceInfo
      }));
      const skills = this._resourceLoader.getSkills().skills.map((skill) => ({
        name: `skill:${skill.name}`,
        description: skill.description,
        source: "skill",
        sourceInfo: skill.sourceInfo
      }));
      return [...extensionCommands, ...templates, ...skills];
    }, "getCommands");
    runner.bindCore({
      sendMessage: /* @__PURE__ */ __name((message, options) => {
        this.sendCustomMessage(message, options).catch((err) => {
          runner.emitError({
            extensionPath: "<runtime>",
            event: "send_message",
            error: err instanceof Error ? err.message : String(err)
          });
        });
      }, "sendMessage"),
      sendUserMessage: /* @__PURE__ */ __name((content, options) => {
        this.sendUserMessage(content, options).catch((err) => {
          runner.emitError({
            extensionPath: "<runtime>",
            event: "send_user_message",
            error: err instanceof Error ? err.message : String(err)
          });
        });
      }, "sendUserMessage"),
      appendEntry: /* @__PURE__ */ __name((customType, data) => {
        const entryId = this.sessionManager.appendCustomEntry(customType, data);
        const entry = this.sessionManager.getEntry(entryId);
        if (entry) {
          this._emit({ type: "entry_appended", entry });
        }
      }, "appendEntry"),
      setSessionName: /* @__PURE__ */ __name((name) => {
        this.setSessionName(name);
      }, "setSessionName"),
      getSessionName: /* @__PURE__ */ __name(() => {
        return this.sessionManager.getSessionName();
      }, "getSessionName"),
      setLabel: /* @__PURE__ */ __name((entryId, label) => {
        this.sessionManager.appendLabelChange(entryId, label);
      }, "setLabel"),
      getActiveTools: /* @__PURE__ */ __name(() => this.getActiveToolNames(), "getActiveTools"),
      getAllTools: /* @__PURE__ */ __name(() => this.getAllTools(), "getAllTools"),
      setActiveTools: /* @__PURE__ */ __name((toolNames) => this.setActiveToolsByName(toolNames), "setActiveTools"),
      refreshTools: /* @__PURE__ */ __name(() => this._refreshToolRegistry(), "refreshTools"),
      getCommands,
      setModel: /* @__PURE__ */ __name(async (model) => {
        if (!this._modelRuntime.hasConfiguredAuth(model.provider))
          return false;
        await this.setModel(model);
        return true;
      }, "setModel"),
      getThinkingLevel: /* @__PURE__ */ __name(() => this.thinkingLevel, "getThinkingLevel"),
      setThinkingLevel: /* @__PURE__ */ __name((level) => this.setThinkingLevel(level), "setThinkingLevel")
    }, {
      getModel: /* @__PURE__ */ __name(() => this.model, "getModel"),
      getScopedModels: /* @__PURE__ */ __name(() => this._scopedModels, "getScopedModels"),
      isIdle: /* @__PURE__ */ __name(() => this.isIdle, "isIdle"),
      isProjectTrusted: /* @__PURE__ */ __name(() => this.settingsManager.isProjectTrusted(), "isProjectTrusted"),
      getSignal: /* @__PURE__ */ __name(() => this.agent.signal, "getSignal"),
      abort: /* @__PURE__ */ __name(() => {
        if (this._extensionAbortHandler) {
          this._extensionAbortHandler();
          return;
        }
        void this.abort();
      }, "abort"),
      hasPendingMessages: /* @__PURE__ */ __name(() => this.pendingMessageCount > 0, "hasPendingMessages"),
      shutdown: /* @__PURE__ */ __name(() => {
        this._extensionShutdownHandler?.();
      }, "shutdown"),
      getContextUsage: /* @__PURE__ */ __name(() => this.getContextUsage(), "getContextUsage"),
      compact: /* @__PURE__ */ __name((options) => {
        void (async () => {
          try {
            const result = await this.compact(options?.customInstructions);
            options?.onComplete?.(result);
          } catch (error) {
            const err = error instanceof Error ? error : new Error(String(error));
            options?.onError?.(err);
          }
        })();
      }, "compact"),
      getSystemPrompt: /* @__PURE__ */ __name(() => this.systemPrompt, "getSystemPrompt"),
      getSystemPromptOptions: /* @__PURE__ */ __name(() => this._baseSystemPromptOptions, "getSystemPromptOptions")
    }, {
      registerProvider: /* @__PURE__ */ __name((name, config) => {
        this._modelRuntime.registerProvider(name, config);
        this._refreshCurrentModelFromRegistry();
      }, "registerProvider"),
      registerNativeProvider: /* @__PURE__ */ __name((provider) => {
        this._modelRuntime.registerNativeProvider(provider);
        this._refreshCurrentModelFromRegistry();
      }, "registerNativeProvider"),
      unregisterProvider: /* @__PURE__ */ __name((name) => {
        this._modelRuntime.unregisterProvider(name);
        this._refreshCurrentModelFromRegistry();
      }, "unregisterProvider")
    });
  }
  _refreshToolRegistry(options) {
    const previousRegistryNames = new Set(this._toolRegistry.keys());
    const previousActiveToolNames = this.getActiveToolNames();
    const allowedToolNames = this._allowedToolNames;
    const excludedToolNames = this._excludedToolNames;
    const isAllowedTool = /* @__PURE__ */ __name((name) => (!allowedToolNames || allowedToolNames.has(name)) && !excludedToolNames?.has(name), "isAllowedTool");
    const registeredTools = this._extensionRunner.getAllRegisteredTools();
    const allCustomTools = [
      ...registeredTools,
      ...this._customTools.map((definition) => ({
        definition,
        sourceInfo: createSyntheticSourceInfo(`<sdk:${definition.name}>`, { source: "sdk" })
      }))
    ].filter((tool) => isAllowedTool(tool.definition.name));
    const definitionRegistry = new Map(Array.from(this._baseToolDefinitions.entries()).filter(([name]) => isAllowedTool(name)).map(([name, definition]) => [
      name,
      {
        definition,
        sourceInfo: createSyntheticSourceInfo(`<builtin:${name}>`, { source: "builtin" })
      }
    ]));
    for (const tool of allCustomTools) {
      definitionRegistry.set(tool.definition.name, {
        definition: tool.definition,
        sourceInfo: tool.sourceInfo
      });
    }
    this._toolDefinitions = definitionRegistry;
    this._toolPromptSnippets = new Map(Array.from(definitionRegistry.values()).map(({ definition }) => {
      const snippet = this._normalizePromptSnippet(definition.promptSnippet);
      return snippet ? [definition.name, snippet] : void 0;
    }).filter((entry) => entry !== void 0));
    this._toolPromptGuidelines = new Map(Array.from(definitionRegistry.values()).map(({ definition }) => {
      const guidelines = this._normalizePromptGuidelines(definition.promptGuidelines);
      return guidelines.length > 0 ? [definition.name, guidelines] : void 0;
    }).filter((entry) => entry !== void 0));
    const runner = this._extensionRunner;
    const wrappedExtensionTools = wrapRegisteredTools(allCustomTools, runner);
    const wrappedBuiltInTools = wrapRegisteredTools(Array.from(this._baseToolDefinitions.values()).filter((definition) => isAllowedTool(definition.name)).map((definition) => ({
      definition,
      sourceInfo: createSyntheticSourceInfo(`<builtin:${definition.name}>`, { source: "builtin" })
    })), runner);
    const toolRegistry = new Map(wrappedBuiltInTools.map((tool) => [tool.name, tool]));
    for (const tool of wrappedExtensionTools) {
      toolRegistry.set(tool.name, tool);
    }
    this._toolRegistry = toolRegistry;
    const nextActiveToolNames = (options?.activeToolNames ? [...options.activeToolNames] : [...previousActiveToolNames]).filter((name) => isAllowedTool(name));
    if (allowedToolNames) {
      for (const toolName of this._toolRegistry.keys()) {
        if (allowedToolNames.has(toolName)) {
          nextActiveToolNames.push(toolName);
        }
      }
    } else if (options?.includeAllExtensionTools) {
      for (const tool of wrappedExtensionTools) {
        nextActiveToolNames.push(tool.name);
      }
    } else if (!options?.activeToolNames) {
      for (const toolName of this._toolRegistry.keys()) {
        if (!previousRegistryNames.has(toolName)) {
          nextActiveToolNames.push(toolName);
        }
      }
    }
    this.setActiveToolsByName([...new Set(nextActiveToolNames)]);
  }
  _buildRuntime(options) {
    const autoResizeImages = this.settingsManager.getImageAutoResize();
    const shellCommandPrefix = this.settingsManager.getShellCommandPrefix();
    const shellPath = this.settingsManager.getShellPath();
    const baseToolDefinitions = this._baseToolsOverride ? Object.fromEntries(Object.entries(this._baseToolsOverride).map(([name, tool]) => [
      name,
      createToolDefinitionFromAgentTool(tool)
    ])) : createAllToolDefinitions(this._cwd, {
      read: { autoResizeImages },
      bash: { commandPrefix: shellCommandPrefix, shellPath }
    });
    this._baseToolDefinitions = new Map(Object.entries(baseToolDefinitions).map(([name, tool]) => [name, tool]));
    const extensionsResult = this._resourceLoader.getExtensions();
    if (options.flagValues) {
      for (const [name, value] of options.flagValues) {
        extensionsResult.runtime.flagValues.set(name, value);
      }
    }
    this._extensionRunner = new ExtensionRunner(extensionsResult.extensions, extensionsResult.runtime, this._cwd, this.sessionManager, new ModelRegistry(this._modelRuntime));
    if (this._extensionRunnerRef) {
      this._extensionRunnerRef.current = this._extensionRunner;
    }
    this._bindExtensionCore(this._extensionRunner);
    this._applyExtensionBindings(this._extensionRunner);
    const defaultActiveToolNames = this._baseToolsOverride ? Object.keys(this._baseToolsOverride) : ["read", "bash", "edit", "write"];
    const baseActiveToolNames = options.activeToolNames ?? defaultActiveToolNames;
    this._refreshToolRegistry({
      activeToolNames: baseActiveToolNames,
      includeAllExtensionTools: options.includeAllExtensionTools
    });
  }
  async reload(options) {
    const oldRunner = this._extensionRunner;
    const previousFlagValues = oldRunner.getFlagValues();
    await emitSessionShutdownEvent(oldRunner, { type: "session_shutdown", reason: "reload" });
    oldRunner.invalidate();
    await this.settingsManager.reload();
    this.syncQueueModesFromSettings();
    resetApiProviders();
    await this._resourceLoader.reload();
    this._buildRuntime({
      activeToolNames: this.getActiveToolNames(),
      flagValues: previousFlagValues,
      includeAllExtensionTools: true
    });
    const hasBindings = this._extensionUIContext || this._extensionCommandContextActions || this._extensionShutdownHandler || this._extensionErrorListener;
    if (hasBindings) {
      await options?.beforeSessionStart?.();
      await this._extensionRunner.emit({ type: "session_start", reason: "reload" });
      await this.extendResourcesFromExtensions("reload");
    }
  }
  // =========================================================================
  // Auto-Retry
  // =========================================================================
  /**
   * Check if an error is retryable (overloaded, rate limit, server errors).
   * Context overflow errors are NOT retryable (handled by compaction instead).
   */
  _isRetryableError(message) {
    if (isContextOverflow(message, this.model?.contextWindow ?? 0))
      return false;
    return isRetryableAssistantError(message);
  }
  /**
   * Retry policy + callbacks shared by compaction and branch-summary summarization calls.
   * Uses the same `settings.retry` budget/backoff as agent-turn retries so a single transient
   * stream drop no longer fails the whole operation. `source` carries the context
   * the TUI needs to render the retry and recreate the underlying indicator.
   */
  _summarizationRetryCallbacks(source) {
    return {
      onRetryScheduled: /* @__PURE__ */ __name((attempt, maxAttempts, delayMs, errorMessage) => {
        this._emit({
          type: "summarization_retry_scheduled",
          attempt,
          maxAttempts,
          delayMs,
          errorMessage
        });
      }, "onRetryScheduled"),
      onRetryAttemptStart: /* @__PURE__ */ __name(() => {
        this._emit({
          type: "summarization_retry_attempt_start",
          ...source
        });
      }, "onRetryAttemptStart"),
      onRetryFinished: /* @__PURE__ */ __name(() => {
        this._emit({ type: "summarization_retry_finished" });
      }, "onRetryFinished")
    };
  }
  _finishCancelledRetry() {
    if (this._retryAttempt === 0)
      return;
    const attempt = this._retryAttempt;
    this._retryAttempt = 0;
    this._emit({
      type: "auto_retry_end",
      success: false,
      attempt,
      finalError: "Retry cancelled"
    });
  }
  /**
   * Prepare a retryable error for continuation with exponential backoff.
   * @returns true if the caller should continue the agent, false otherwise
   */
  async _prepareRetry(message) {
    const settings = this.settingsManager.getRetrySettings();
    if (!settings.enabled) {
      return false;
    }
    this._retryAttempt++;
    if (this._retryAttempt > settings.maxRetries) {
      this._retryAttempt--;
      return false;
    }
    const delayMs = retryDelayMs(settings, this._retryAttempt);
    this._emit({
      type: "auto_retry_start",
      attempt: this._retryAttempt,
      maxAttempts: settings.maxRetries,
      delayMs,
      errorMessage: message.errorMessage || "Unknown error"
    });
    this._omitRecoveryAttempt(message);
    this._retryAbortController = new AbortController();
    try {
      await sleep(delayMs, this._retryAbortController.signal);
    } catch {
      this._finishCancelledRetry();
      return false;
    } finally {
      this._retryAbortController = void 0;
    }
    return true;
  }
  /**
   * Cancel in-progress retry.
   */
  abortRetry() {
    this._retryAbortController?.abort();
  }
  /** Whether auto-retry is currently in progress */
  get isRetrying() {
    return this._retryAbortController !== void 0;
  }
  /** Whether auto-retry is enabled */
  get autoRetryEnabled() {
    return this.settingsManager.getRetryEnabled();
  }
  /**
   * Toggle auto-retry setting.
   */
  setAutoRetryEnabled(enabled) {
    this.settingsManager.setRetryEnabled(enabled);
  }
  // =========================================================================
  // Bash Execution
  // =========================================================================
  /**
   * Execute a bash command.
   * Adds result to agent context and session.
   * @param command The bash command to execute
   * @param onChunk Optional streaming callback for output
   * @param options.excludeFromContext If true, command output won't be sent to LLM (!! prefix)
   * @param options.id Optional identifier included in bash execution update events
   * @param options.operations Custom BashOperations for remote execution
   */
  async executeBash(command, onChunk, options) {
    const abortController = new AbortController();
    this._bashAbortControllers.add(abortController);
    const prefix = this.settingsManager.getShellCommandPrefix();
    const shellPath = this.settingsManager.getShellPath();
    const resolvedCommand = prefix ? `${prefix}
${command}` : command;
    try {
      const result = await executeBashWithOperations(resolvedCommand, this.sessionManager.getCwd(), options?.operations ?? createLocalBashOperations({ shellPath }), {
        onChunk: /* @__PURE__ */ __name((delta) => {
          onChunk?.(delta);
          this._emit({ type: "bash_execution_update", id: options?.id, delta });
        }, "onChunk"),
        signal: abortController.signal
      });
      this.recordBashResult(command, result, options);
      return result;
    } finally {
      this._bashAbortControllers.delete(abortController);
    }
  }
  /**
   * Record a bash execution result in session history.
   * Used by executeBash and by extensions that handle bash execution themselves.
   */
  recordBashResult(command, result, options) {
    const bashMessage = {
      role: "bashExecution",
      command,
      output: result.output,
      exitCode: result.exitCode,
      cancelled: result.cancelled,
      truncated: result.truncated,
      fullOutputPath: result.fullOutputPath,
      timestamp: Date.now(),
      excludeFromContext: options?.excludeFromContext
    };
    if (this.isStreaming) {
      this._pendingBashMessages.push(bashMessage);
    } else {
      this.sessionManager.appendMessage(bashMessage);
      this._refreshFinalizedContext();
    }
  }
  /**
   * Cancel running bash command.
   */
  abortBash() {
    for (const abortController of [...this._bashAbortControllers]) {
      abortController.abort();
    }
  }
  /** Whether a bash command is currently running */
  get isBashRunning() {
    return this._bashAbortControllers.size > 0;
  }
  /** Whether there are pending bash messages waiting to be flushed */
  get hasPendingBashMessages() {
    return this._pendingBashMessages.length > 0;
  }
  /**
   * Flush pending bash messages to agent state and session.
   * Called after agent turn completes to maintain proper message ordering.
   */
  _flushPendingBashMessages() {
    if (this._pendingBashMessages.length === 0)
      return;
    for (const bashMessage of this._pendingBashMessages) {
      this.sessionManager.appendMessage(bashMessage);
    }
    this._pendingBashMessages = [];
    this._refreshFinalizedContext();
  }
  // =========================================================================
  // Session Management
  // =========================================================================
  /**
   * Set a display name for the current session.
   */
  setSessionName(name) {
    this.sessionManager.appendSessionInfo(name);
    const event = { type: "session_info_changed", name: this.sessionManager.getSessionName() };
    this._emit(event);
    void this._extensionRunner.emit(event);
  }
  // =========================================================================
  // Tree Navigation
  // =========================================================================
  /**
   * Navigate to a different node in the session tree.
   * Unlike fork() which creates a new session file, this stays in the same file.
   *
   * @param targetId The entry ID to navigate to
   * @param options.summarize Whether user wants to summarize abandoned branch
   * @param options.customInstructions Custom instructions for summarizer
   * @param options.replaceInstructions If true, customInstructions replaces the default prompt
   * @param options.label Label to attach to the branch summary entry
   * @returns Result with editorText (if user message) and cancelled status
   */
  async navigateTree(targetId, options = {}) {
    if (this.isStreaming) {
      throw new Error("Wait for the current response to finish before navigating the session tree.");
    }
    if (this.isCompacting) {
      throw new Error("Wait for the current compaction or tree navigation to finish before navigating the session tree.");
    }
    const oldLeafId = this.sessionManager.getLeafId();
    if (targetId === oldLeafId) {
      return { cancelled: false };
    }
    if (options.summarize && !this.model) {
      throw new Error("No model available for summarization");
    }
    const targetEntry = this.sessionManager.getEntry(targetId);
    if (!targetEntry) {
      throw new Error(`Entry ${targetId} not found`);
    }
    const { entries: entriesToSummarize, commonAncestorId } = collectEntriesForBranchSummary(this.sessionManager, oldLeafId, targetId);
    let customInstructions = options.customInstructions;
    let replaceInstructions = options.replaceInstructions;
    let label = options.label;
    const preparation = {
      targetId,
      oldLeafId,
      commonAncestorId,
      entriesToSummarize,
      userWantsSummary: options.summarize ?? false,
      customInstructions,
      replaceInstructions,
      label
    };
    this._branchSummaryAbortController = new AbortController();
    try {
      let extensionSummary;
      let fromExtension = false;
      if (this._extensionRunner.hasHandlers("session_before_tree")) {
        const result = await this._extensionRunner.emit({
          type: "session_before_tree",
          preparation,
          signal: this._branchSummaryAbortController.signal
        });
        if (result?.cancel) {
          return { cancelled: true };
        }
        if (result?.summary && options.summarize) {
          extensionSummary = result.summary;
          fromExtension = true;
        }
        if (result?.customInstructions !== void 0) {
          customInstructions = result.customInstructions;
        }
        if (result?.replaceInstructions !== void 0) {
          replaceInstructions = result.replaceInstructions;
        }
        if (result?.label !== void 0) {
          label = result.label;
        }
      }
      let summaryText;
      let summaryDetails;
      let summaryUsage;
      if (options.summarize && entriesToSummarize.length > 0 && !extensionSummary) {
        const model = this.model;
        const { model: requestModel, apiKey, headers, env } = await this._getSummarizationRequestAuth(model);
        const branchSummarySettings = this.settingsManager.getBranchSummarySettings();
        const result = await generateBranchSummary(entriesToSummarize, {
          model: requestModel,
          apiKey,
          headers,
          env,
          signal: this._branchSummaryAbortController.signal,
          customInstructions,
          replaceInstructions,
          reserveTokens: branchSummarySettings.reserveTokens,
          streamFn: this.agent.streamFunction,
          retry: this.settingsManager.getRetrySettings(),
          callbacks: this._summarizationRetryCallbacks({ source: "branchSummary" })
        });
        if (result.aborted) {
          return { cancelled: true, aborted: true };
        }
        if (result.error) {
          throw new Error(result.error);
        }
        summaryText = result.summary;
        summaryUsage = result.usage;
        summaryDetails = {
          readFiles: result.readFiles || [],
          modifiedFiles: result.modifiedFiles || []
        };
      } else if (extensionSummary) {
        summaryText = extensionSummary.summary;
        summaryDetails = extensionSummary.details;
        summaryUsage = extensionSummary.usage;
      }
      let newLeafId;
      let editorText;
      if (targetEntry.type === "message" && targetEntry.message.role === "user") {
        newLeafId = targetEntry.parentId;
        editorText = contentText5(targetEntry.message.content, "");
      } else if (targetEntry.type === "custom_message") {
        newLeafId = targetEntry.parentId;
        editorText = contentText5(targetEntry.content, "");
      } else {
        newLeafId = targetId;
      }
      let summaryEntry;
      if (summaryText) {
        const summaryId = this.sessionManager.branchWithSummary(newLeafId, summaryText, summaryDetails, fromExtension, summaryUsage);
        summaryEntry = this.sessionManager.getEntry(summaryId);
        if (label) {
          this.sessionManager.appendLabelChange(summaryId, label);
        }
      } else if (newLeafId === null) {
        this.sessionManager.resetLeaf();
      } else {
        this.sessionManager.branch(newLeafId);
      }
      if (label && !summaryText) {
        this.sessionManager.appendLabelChange(targetId, label);
      }
      this._refreshFinalizedContext();
      this._restoreToolsFromTranscript();
      await this._extensionRunner.emit({
        type: "session_tree",
        newLeafId: this.sessionManager.getLeafId(),
        oldLeafId,
        summaryEntry,
        fromExtension: summaryText ? fromExtension : void 0
      });
      return { editorText, cancelled: false, summaryEntry };
    } finally {
      this._branchSummaryAbortController = void 0;
      this._resolveIdleWaitIfIdle();
    }
  }
  /**
   * Get all user messages from session for fork selector.
   */
  getUserMessagesForForking() {
    const entries = this.sessionManager.getEntries();
    const result = [];
    for (const entry of entries) {
      if (entry.type !== "message")
        continue;
      if (entry.message.role !== "user")
        continue;
      const text = contentText5(entry.message.content, "");
      if (text) {
        result.push({ entryId: entry.id, text });
      }
    }
    return result;
  }
  /**
   * Get session statistics. Aggregates over ALL session entries (including
   * history that was compacted away), so token/cost totals reflect what was
   * actually billed across the session.
   */
  getSessionStats() {
    let userMessages = 0;
    let assistantMessages = 0;
    let toolResults = 0;
    let totalMessages = 0;
    let toolCalls = 0;
    const usageTotals = createUsageTotals();
    for (const entry of this.sessionManager.getEntries()) {
      if (entry.type === "usage") {
        addUsageToTotals(usageTotals, entry.usage);
      } else if ((entry.type === "branch_summary" || entry.type === "compaction") && entry.usage) {
        addUsageToTotals(usageTotals, entry.usage);
      }
      if (entry.type !== "message")
        continue;
      totalMessages++;
      const message = entry.message;
      if (message.role === "user") {
        userMessages++;
      } else if (message.role === "toolResult") {
        toolResults++;
        if (message.usage) {
          addUsageToTotals(usageTotals, message.usage);
        }
      } else if (message.role === "assistant") {
        assistantMessages++;
        const assistantMsg = message;
        if (Array.isArray(assistantMsg.content)) {
          toolCalls += assistantMsg.content.filter((c) => c.type === "toolCall").length;
        }
        addUsageToTotals(usageTotals, assistantMsg.usage);
      }
    }
    return {
      sessionFile: this.sessionFile,
      sessionId: this.sessionId,
      userMessages,
      assistantMessages,
      toolCalls,
      toolResults,
      totalMessages,
      tokens: {
        input: usageTotals.input,
        output: usageTotals.output,
        cacheRead: usageTotals.cacheRead,
        cacheWrite: usageTotals.cacheWrite,
        total: usageTotals.input + usageTotals.output + usageTotals.cacheRead + usageTotals.cacheWrite
      },
      cost: usageTotals.cost,
      contextUsage: this.getContextUsage()
    };
  }
  getContextUsage() {
    const model = this.model;
    if (!model)
      return void 0;
    const contextWindow = model.contextWindow ?? 0;
    if (contextWindow <= 0)
      return void 0;
    const projection = this.sessionManager.buildSessionProjection();
    const branch = this.sessionManager.getBranch();
    const latestCompaction = getLatestCompactionEntry(branch);
    if (latestCompaction) {
      const projectedAssistants = new Set(projection.entries.flatMap((entry) => entry.messages.some((message) => message.role === "assistant" && message.stopReason !== "aborted" && message.stopReason !== "error" && calculateContextTokens(message.usage) > 0) ? [entry.sourceEntry.id] : []));
      const compactionIndex = branch.findIndex((entry) => entry.id === latestCompaction.id);
      const hasPostCompactionUsage = branch.slice(compactionIndex + 1).some((entry) => projectedAssistants.has(entry.id));
      if (!hasPostCompactionUsage)
        return { tokens: null, contextWindow, percent: null };
    }
    const estimate = estimateProjectedContextTokens(projection, branch);
    const percent = estimate.tokens / contextWindow * 100;
    return {
      tokens: estimate.tokens,
      contextWindow,
      percent
    };
  }
  /**
   * Export session to HTML.
   * @param outputPath Optional output path (defaults to session directory)
   * @param options Optional export presentation settings
   * @returns Path to exported file
   */
  async exportToHtml(outputPath, options = {}) {
    const themeName = [options.themeName, this.settingsManager.getTheme()].find((candidate) => candidate !== void 0 && getThemeByName(candidate) !== void 0);
    const toolRenderer = createToolHtmlRenderer({
      getToolDefinition: /* @__PURE__ */ __name((name) => this.getToolDefinition(name), "getToolDefinition"),
      theme,
      cwd: this.sessionManager.getCwd()
    });
    return await exportSessionToHtml(this.sessionManager, this.state, {
      outputPath,
      themeName,
      toolRenderer
    });
  }
  /**
   * Export the current session branch to a JSONL file.
   * Writes the session header followed by all entries on the current branch path.
   * @param outputPath Target file path. If omitted, generates a timestamped file in cwd.
   * @returns The resolved output file path.
   */
  exportToJsonl(outputPath) {
    return exportSessionToJsonl(this.sessionManager, outputPath);
  }
  /**
   * Ask the current model to describe what went wrong in this session for a bug report.
   * Used when the user declines to share the transcript itself.
   */
  async summarizeForBugReport(options) {
    const model = this.model;
    if (!model) {
      throw new Error("No model selected");
    }
    const { model: requestModel, apiKey, headers, env } = await this._getSummarizationRequestAuth(model);
    return generateBugReportSummary({
      messages: this.messages,
      hint: options.hint,
      model: requestModel,
      apiKey,
      headers,
      env,
      signal: options.signal,
      thinkingLevel: this.thinkingLevel,
      streamFn: this.agent.streamFunction,
      retry: this.settingsManager.getRetrySettings(),
      sessionId: this.sessionId
    });
  }
  // =========================================================================
  // Utilities
  // =========================================================================
  /**
   * Get text content of last assistant message.
   * Useful for /copy command.
   * @returns Text content, or undefined if no assistant message exists
   */
  getLastAssistantText() {
    const lastAssistant = this.messages.slice().reverse().find((m) => {
      if (m.role !== "assistant")
        return false;
      const msg = m;
      if (msg.stopReason === "aborted" && msg.content.length === 0)
        return false;
      return true;
    });
    if (!lastAssistant)
      return void 0;
    let text = "";
    for (const content of lastAssistant.content) {
      if (content.type === "text") {
        text += content.text;
      }
    }
    return text.trim() || void 0;
  }
  // =========================================================================
  // Extension System
  // =========================================================================
  createReplacedSessionContext() {
    const context = Object.defineProperties({}, Object.getOwnPropertyDescriptors(this._extensionRunner.createCommandContext()));
    context.sendMessage = (message, options) => this.sendCustomMessage(message, options);
    context.sendUserMessage = (content, options) => this.sendUserMessage(content, options);
    return context;
  }
  /**
   * Check if extensions have handlers for a specific event type.
   */
  hasExtensionHandlers(eventType) {
    return this._extensionRunner.hasHandlers(eventType);
  }
  /**
   * Get the extension runner (for setting UI context and error handlers).
   */
  get extensionRunner() {
    return this._extensionRunner;
  }
};

// pi-dist/pi-coding-agent/core/auth-storage.js
import { existsSync as existsSync7, mkdirSync as mkdirSync3, readFileSync as readFileSync6, writeFileSync as writeFileSync4 } from "fs";
import { dirname as dirname6, join as join8 } from "path";
import lockfile from "../../../proper-lockfile.mjs";
import { setTimeout as sleep2 } from "timers/promises";

// pi-dist/pi-coding-agent/utils/abort.js
function abortReason(signal) {
  if (signal.reason !== void 0)
    return signal.reason;
  const error = new Error("The operation was aborted");
  error.name = "AbortError";
  return error;
}
__name(abortReason, "abortReason");
function operationSignal(signal) {
  return signal ?? new AbortController().signal;
}
__name(operationSignal, "operationSignal");
function raceWithAbortSignal(operation, signal) {
  if (!signal)
    return operation;
  if (signal.aborted) {
    void operation.catch(() => {
    });
    return Promise.reject(abortReason(signal));
  }
  return new Promise((resolve9, reject) => {
    let settled = false;
    const cleanup = /* @__PURE__ */ __name(() => signal.removeEventListener("abort", onAbort), "cleanup");
    const onAbort = /* @__PURE__ */ __name(() => {
      if (settled)
        return;
      settled = true;
      cleanup();
      reject(abortReason(signal));
    }, "onAbort");
    signal.addEventListener("abort", onAbort, { once: true });
    void operation.then((value) => {
      if (settled)
        return;
      settled = true;
      cleanup();
      resolve9(value);
    }, (error) => {
      if (settled)
        return;
      settled = true;
      cleanup();
      reject(error);
    });
    if (signal.aborted)
      onAbort();
  });
}
__name(raceWithAbortSignal, "raceWithAbortSignal");

// pi-dist/pi-coding-agent/core/auth-storage.js
var AUTH_FILE_WRITE_OPTIONS = { encoding: "utf-8", mode: 384 };
var sharedAuthFileReadState;
var FileAuthStorageBackend = class {
  static {
    __name(this, "FileAuthStorageBackend");
  }
  authPath;
  constructor(authPath = join8(getAgentDir(), "auth.json")) {
    this.authPath = normalizePath(authPath);
  }
  ensureParentDir() {
    const dir = dirname6(this.authPath);
    if (!existsSync7(dir)) {
      mkdirSync3(dir, { recursive: true, mode: 448 });
    }
  }
  ensureFileExists() {
    if (!existsSync7(this.authPath)) {
      writeFileSync4(this.authPath, "{}", AUTH_FILE_WRITE_OPTIONS);
    }
  }
  acquireLockSyncWithRetry(path2) {
    const maxAttempts = 10;
    const delayMs = 20;
    let lastError;
    for (let attempt = 1; attempt <= maxAttempts; attempt++) {
      try {
        return lockfile.lockSync(path2, { realpath: false });
      } catch (error) {
        const code = typeof error === "object" && error !== null && "code" in error ? String(error.code) : void 0;
        if (code !== "ELOCKED" || attempt === maxAttempts) {
          throw error;
        }
        lastError = error;
        const start = Date.now();
        while (Date.now() - start < delayMs) {
        }
      }
    }
    throw lastError ?? new Error("Failed to acquire auth storage lock");
  }
  withLock(fn) {
    this.ensureParentDir();
    this.ensureFileExists();
    let release2;
    try {
      release2 = this.acquireLockSyncWithRetry(this.authPath);
      const current = existsSync7(this.authPath) ? readFileSync6(this.authPath, "utf-8") : void 0;
      const { result, next } = fn(current);
      if (next !== void 0) {
        writeFileSync4(this.authPath, next, AUTH_FILE_WRITE_OPTIONS);
      }
      return result;
    } finally {
      if (release2) {
        release2();
      }
    }
  }
  async acquireLockAsync(signal, onCompromised) {
    const staleMs = 3e4;
    const maxDelayMs = 2e3;
    const deadline = Date.now() + staleMs;
    let retry = 0;
    while (true) {
      signal?.throwIfAborted();
      let release2;
      try {
        release2 = await lockfile.lock(this.authPath, {
          realpath: false,
          retries: 0,
          stale: staleMs,
          onCompromised
        });
      } catch (error) {
        signal?.throwIfAborted();
        const code = typeof error === "object" && error !== null && "code" in error ? String(error.code) : void 0;
        const remainingMs = deadline - Date.now();
        if (code !== "ELOCKED" || remainingMs <= 0)
          throw error;
        const baseDelayMs = Math.min(10 * 2 ** retry, maxDelayMs / 2);
        retry++;
        const delayMs = Math.min(Math.round(baseDelayMs * (1 + Math.random())), remainingMs);
        if (signal)
          await sleep2(delayMs, void 0, { signal });
        else
          await sleep2(delayMs);
        continue;
      }
      if (signal?.aborted) {
        await release2();
        signal.throwIfAborted();
      }
      return release2;
    }
  }
  async withLockAsync(fn, options) {
    options?.signal?.throwIfAborted();
    this.ensureParentDir();
    this.ensureFileExists();
    let release2;
    let lockCompromised = false;
    let lockCompromisedError;
    const throwIfCompromised = /* @__PURE__ */ __name(() => {
      if (lockCompromised) {
        throw lockCompromisedError ?? new Error("Auth storage lock was compromised");
      }
    }, "throwIfCompromised");
    try {
      release2 = await this.acquireLockAsync(options?.signal, (error) => {
        lockCompromised = true;
        lockCompromisedError = error;
      });
      throwIfCompromised();
      options?.signal?.throwIfAborted();
      const current = existsSync7(this.authPath) ? readFileSync6(this.authPath, "utf-8") : void 0;
      const { result, next } = await fn(current);
      throwIfCompromised();
      options?.signal?.throwIfAborted();
      if (next !== void 0) {
        writeFileSync4(this.authPath, next, AUTH_FILE_WRITE_OPTIONS);
      }
      throwIfCompromised();
      return result;
    } finally {
      if (release2) {
        try {
          await release2();
        } catch {
        }
      }
    }
  }
};
var ReadOnlyAuthStorage = class {
  static {
    __name(this, "ReadOnlyAuthStorage");
  }
  authPath;
  data;
  constructor(authPath = join8(getAgentDir(), "auth.json")) {
    this.authPath = normalizePath(authPath);
  }
  load() {
    if (this.data)
      return this.data;
    let parsed;
    try {
      parsed = JSON.parse(stripBom(readFileSync6(this.authPath, "utf-8")));
    } catch (error) {
      if (error.code === "ENOENT") {
        this.data = {};
        return this.data;
      }
      throw new Error(`Failed to read auth.json: ${error instanceof Error ? error.message : String(error)}`);
    }
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
      throw new Error("Invalid auth.json: expected an object");
    }
    for (const [providerId, credential] of Object.entries(parsed)) {
      if (typeof credential !== "object" || credential === null || Array.isArray(credential)) {
        throw new Error(`Invalid auth.json credential for provider "${providerId}"`);
      }
      const value = credential;
      if (value.type === "api_key") {
        const validKey = value.key === void 0 || typeof value.key === "string";
        const validEnv = value.env === void 0 || typeof value.env === "object" && value.env !== null && !Array.isArray(value.env) && Object.values(value.env).every((entry) => typeof entry === "string");
        if (validKey && validEnv)
          continue;
      } else if (value.type === "oauth" && typeof value.access === "string" && typeof value.refresh === "string" && typeof value.expires === "number" && Number.isFinite(value.expires)) {
        continue;
      }
      throw new Error(`Invalid auth.json credential for provider "${providerId}"`);
    }
    this.data = parsed;
    return this.data;
  }
  async read(providerId, options) {
    options?.signal?.throwIfAborted();
    const credential = this.load()[providerId];
    options?.signal?.throwIfAborted();
    if (!credential)
      return void 0;
    if (credential.type !== "api_key" || !credential.key || isCommandConfigValue(credential.key)) {
      return structuredClone(credential);
    }
    return { ...credential, key: resolveConfigValue(credential.key, credential.env) };
  }
  async list(options) {
    options?.signal?.throwIfAborted();
    const credentials = Object.entries(this.load()).map(([providerId, credential]) => ({
      providerId,
      type: credential.type
    }));
    options?.signal?.throwIfAborted();
    return credentials;
  }
  async modify(_providerId, _fn, _options) {
    throw new Error("Read-only credential storage cannot modify auth.json");
  }
  async delete(_providerId, _options) {
    throw new Error("Read-only credential storage cannot modify auth.json");
  }
};
var InMemoryAuthStorageBackend = class {
  static {
    __name(this, "InMemoryAuthStorageBackend");
  }
  value;
  asyncChain = Promise.resolve();
  withLock(fn) {
    const { result, next } = fn(this.value);
    if (next !== void 0) {
      this.value = next;
    }
    return result;
  }
  withLockAsync(fn, options) {
    const previous = this.asyncChain;
    const operation = (async () => {
      await previous.catch(() => {
      });
      options?.signal?.throwIfAborted();
      const { result, next } = await fn(this.value);
      options?.signal?.throwIfAborted();
      if (next !== void 0) {
        this.value = next;
      }
      return result;
    })();
    this.asyncChain = operation.catch(() => {
    });
    return raceWithAbortSignal(operation, options?.signal);
  }
};
var AuthStorage = class _AuthStorage {
  static {
    __name(this, "AuthStorage");
  }
  storage;
  authPath;
  readState;
  constructor(storage, authPath) {
    this.storage = storage;
    this.authPath = authPath;
    this.readState = authPath && sharedAuthFileReadState?.authPath === authPath ? sharedAuthFileReadState.readState : { data: {} };
    if (authPath && !sharedAuthFileReadState) {
      sharedAuthFileReadState = { authPath, readState: this.readState };
    }
    if (authPath) {
      const revision = getFileRevision(authPath);
      if (revision !== void 0 && revision === this.readState.revision)
        return;
    }
    this.reload();
  }
  static create(authPath = join8(getAgentDir(), "auth.json")) {
    const normalizedAuthPath = normalizePath(authPath);
    return new _AuthStorage(new FileAuthStorageBackend(normalizedAuthPath), normalizedAuthPath);
  }
  static fromStorage(storage) {
    return new _AuthStorage(storage);
  }
  static inMemory(data = {}) {
    const storage = new InMemoryAuthStorageBackend();
    storage.withLock(() => ({ result: void 0, next: JSON.stringify(data, null, 2) }));
    return _AuthStorage.fromStorage(storage);
  }
  parseStorageData(content) {
    if (!content) {
      return {};
    }
    return JSON.parse(stripBom(content));
  }
  updateReadState(data, revision) {
    this.readState.data = data;
    this.readState.revision = revision;
  }
  /**
   * Reload credentials from storage.
   */
  reload() {
    let content;
    let revision;
    try {
      this.storage.withLock((current) => {
        content = current;
        revision = this.authPath ? getFileRevision(this.authPath) : void 0;
        return { result: void 0 };
      });
      this.updateReadState(this.parseStorageData(content), revision);
    } catch {
    }
  }
  async reloadFromStorageAsync(options) {
    return this.storage.withLockAsync(async (content) => {
      const currentData = this.parseStorageData(content);
      const revision = this.authPath ? getFileRevision(this.authPath) : void 0;
      this.updateReadState(currentData, revision);
      return { result: currentData };
    }, options);
  }
  async readLatestData(options) {
    options?.signal?.throwIfAborted();
    if (!this.authPath) {
      const reload2 = this.reloadFromStorageAsync(options);
      return options?.signal ? reload2 : reload2.catch(() => this.readState.data);
    }
    const revision = getFileRevision(this.authPath);
    if (revision !== void 0 && revision === this.readState.revision)
      return this.readState.data;
    if (!this.readState.reload) {
      const controller = new AbortController();
      const reload2 = {
        controller,
        promise: this.reloadFromStorageAsync({ signal: controller.signal }),
        readers: 0
      };
      this.readState.reload = reload2;
      void reload2.promise.then(() => {
        if (this.readState.reload === reload2)
          this.readState.reload = void 0;
      }, () => {
        if (this.readState.reload === reload2)
          this.readState.reload = void 0;
      });
    }
    const reload = this.readState.reload;
    reload.readers++;
    try {
      const result = raceWithAbortSignal(reload.promise, options?.signal);
      return options?.signal ? await result : await result.catch(() => this.readState.data);
    } finally {
      reload.readers--;
      if (reload.readers === 0 && this.readState.reload === reload) {
        this.readState.reload = void 0;
        reload.controller.abort();
      }
    }
  }
  async read(provider, options) {
    const credential = (await this.readLatestData(options))[provider];
    options?.signal?.throwIfAborted();
    if (credential?.type !== "api_key")
      return credential;
    if (credential.key === void 0)
      return credential;
    return { ...credential, key: resolveConfigValue(credential.key, credential.env) };
  }
  async modify(provider, fn, options) {
    let latestData = this.readState.data;
    let revision;
    const result = await this.storage.withLockAsync(async (content) => {
      const currentData = this.parseStorageData(content);
      const next = await fn(currentData[provider]);
      if (next === void 0) {
        latestData = currentData;
        revision = this.authPath ? getFileRevision(this.authPath) : void 0;
        return { result: currentData[provider] };
      }
      const merged = { ...currentData, [provider]: next };
      latestData = merged;
      return { result: next, next: JSON.stringify(merged, null, 2) };
    }, options);
    this.updateReadState(latestData, revision);
    return result;
  }
  async delete(provider, options) {
    let latestData = this.readState.data;
    await this.storage.withLockAsync(async (content) => {
      const currentData = this.parseStorageData(content);
      delete currentData[provider];
      latestData = currentData;
      return { result: void 0, next: JSON.stringify(currentData, null, 2) };
    }, options);
    this.updateReadState(latestData);
  }
  /** List credential metadata without resolving configured key values. */
  async list(options) {
    const entries = Object.entries(await this.readLatestData(options));
    options?.signal?.throwIfAborted();
    return entries.map(([providerId, credential]) => ({ providerId, type: credential.type }));
  }
};
function readStoredCredential(providerId, authPath = join8(getAgentDir(), "auth.json")) {
  try {
    const data = JSON.parse(stripBom(readFileSync6(normalizePath(authPath), "utf-8")));
    return data[providerId];
  } catch {
    return void 0;
  }
}
__name(readStoredCredential, "readStoredCredential");

// pi-dist/pi-coding-agent/core/model-resolver.js
import { modelsAreEqual as modelsAreEqual2 } from "../../pi-ai/sdk-bundle/index.js";
import chalk2 from "../../../chalk/source/index.js";
import { minimatch } from "../../../minimatch/dist/esm/index.js";
var defaultModelPerProvider = {
  "amazon-bedrock": "us.anthropic.claude-opus-4-6-v1",
  "ant-ling": "Ring-2.6-1T",
  anthropic: "claude-opus-4-8",
  openai: "gpt-5.5",
  "azure-openai-responses": "gpt-5.4",
  "openai-codex": "gpt-5.5",
  radius: "balanced",
  nvidia: "nvidia/nemotron-3-super-120b-a12b",
  deepseek: "deepseek-v4-pro",
  google: "gemini-3.1-pro-preview",
  "google-vertex": "gemini-3.1-pro-preview",
  "github-copilot": "gpt-5.4",
  openrouter: "moonshotai/kimi-k2.6",
  "vercel-ai-gateway": "zai/glm-5.1",
  xai: "grok-4.7",
  groq: "openai/gpt-oss-120b",
  cerebras: "gpt-oss-120b",
  zai: "glm-5.3",
  "zai-coding-cn": "glm-5.3",
  mistral: "devstral-medium-latest",
  minimax: "MiniMax-M2.7",
  "minimax-cn": "MiniMax-M2.7",
  moonshotai: "kimi-k2.6",
  "moonshotai-cn": "kimi-k2.6",
  huggingface: "moonshotai/Kimi-K2.6",
  fireworks: "accounts/fireworks/models/kimi-k2p6",
  together: "moonshotai/Kimi-K2.6",
  baseten: "zai-org/GLM-5.2",
  opencode: "kimi-k2.6",
  "opencode-go": "kimi-k2.6",
  "kimi-coding": "kimi-for-coding",
  meta: "muse-spark-1.3",
  "cloudflare-workers-ai": "@cf/moonshotai/kimi-k2.6",
  "cloudflare-ai-gateway": "workers-ai/@cf/moonshotai/kimi-k2.6",
  "qwen-token-plan": "qwen3.7-max",
  "qwen-token-plan-cn": "qwen3.7-max",
  "qwen-token-plan-individual": "qwen3.8-max",
  xiaomi: "mimo-v2.5-pro",
  "xiaomi-token-plan-cn": "mimo-v2.5-pro",
  "xiaomi-token-plan-ams": "mimo-v2.5-pro",
  "xiaomi-token-plan-sgp": "mimo-v2.5-pro"
};
function isAlias(id) {
  if (id.endsWith("-latest"))
    return true;
  const datePattern = /-\d{8}$/;
  return !datePattern.test(id);
}
__name(isAlias, "isAlias");
function findExactModelReferenceMatch(modelReference, availableModels) {
  const trimmedReference = modelReference.trim();
  if (!trimmedReference) {
    return void 0;
  }
  const normalizedReference = trimmedReference.toLowerCase();
  const canonicalMatches = availableModels.filter((model) => `${model.provider}/${model.id}`.toLowerCase() === normalizedReference);
  if (canonicalMatches.length === 1) {
    return canonicalMatches[0];
  }
  if (canonicalMatches.length > 1) {
    return void 0;
  }
  const slashIndex = trimmedReference.indexOf("/");
  if (slashIndex !== -1) {
    const provider = trimmedReference.substring(0, slashIndex).trim();
    const modelId = trimmedReference.substring(slashIndex + 1).trim();
    if (provider && modelId) {
      const providerMatches = availableModels.filter((model) => model.provider.toLowerCase() === provider.toLowerCase() && model.id.toLowerCase() === modelId.toLowerCase());
      if (providerMatches.length === 1) {
        return providerMatches[0];
      }
      if (providerMatches.length > 1) {
        return void 0;
      }
    }
  }
  const idMatches = availableModels.filter((model) => model.id.toLowerCase() === normalizedReference);
  return idMatches.length === 1 ? idMatches[0] : void 0;
}
__name(findExactModelReferenceMatch, "findExactModelReferenceMatch");
function tryMatchModel(modelPattern, availableModels) {
  const exactMatch = findExactModelReferenceMatch(modelPattern, availableModels);
  if (exactMatch) {
    return exactMatch;
  }
  const matches = availableModels.filter((m) => m.id.toLowerCase().includes(modelPattern.toLowerCase()) || m.name?.toLowerCase().includes(modelPattern.toLowerCase()));
  if (matches.length === 0) {
    return void 0;
  }
  const aliases = matches.filter((m) => isAlias(m.id));
  const datedVersions = matches.filter((m) => !isAlias(m.id));
  if (aliases.length > 0) {
    aliases.sort((a, b) => b.id.localeCompare(a.id));
    return aliases[0];
  } else {
    datedVersions.sort((a, b) => b.id.localeCompare(a.id));
    return datedVersions[0];
  }
}
__name(tryMatchModel, "tryMatchModel");
function buildFallbackModel(provider, modelId, availableModels) {
  const providerModels = availableModels.filter((m) => m.provider === provider);
  if (providerModels.length === 0)
    return void 0;
  const defaultId = defaultModelPerProvider[provider];
  const baseModel = defaultId ? providerModels.find((m) => m.id === defaultId) ?? providerModels[0] : providerModels[0];
  return {
    ...baseModel,
    id: modelId,
    name: modelId
  };
}
__name(buildFallbackModel, "buildFallbackModel");
function parseModelPattern(pattern, availableModels, options) {
  const exactMatch = tryMatchModel(pattern, availableModels);
  if (exactMatch) {
    return { model: exactMatch, thinkingLevel: void 0, warning: void 0 };
  }
  const lastColonIndex = pattern.lastIndexOf(":");
  if (lastColonIndex === -1) {
    return { model: void 0, thinkingLevel: void 0, warning: void 0 };
  }
  const prefix = pattern.substring(0, lastColonIndex);
  const suffix = pattern.substring(lastColonIndex + 1);
  if (isValidThinkingLevel(suffix)) {
    const result = parseModelPattern(prefix, availableModels, options);
    if (result.model) {
      return {
        model: result.model,
        thinkingLevel: result.warning ? void 0 : suffix,
        warning: result.warning
      };
    }
    return result;
  } else {
    const allowFallback = options?.allowInvalidThinkingLevelFallback ?? true;
    if (!allowFallback) {
      return { model: void 0, thinkingLevel: void 0, warning: void 0 };
    }
    const result = parseModelPattern(prefix, availableModels, options);
    if (result.model) {
      return {
        model: result.model,
        thinkingLevel: void 0,
        warning: `Invalid thinking level "${suffix}" in pattern "${pattern}". Using default instead.`
      };
    }
    return result;
  }
}
__name(parseModelPattern, "parseModelPattern");
function resolveModelScopeFromModels(patterns, models) {
  const availableModels = [...models];
  const scopedModels = [];
  const diagnostics = [];
  for (const pattern of patterns) {
    if (pattern.includes("*") || pattern.includes("?") || pattern.includes("[")) {
      const colonIdx = pattern.lastIndexOf(":");
      let globPattern = pattern;
      let thinkingLevel2;
      if (colonIdx !== -1) {
        const suffix = pattern.substring(colonIdx + 1);
        if (isValidThinkingLevel(suffix)) {
          thinkingLevel2 = suffix;
          globPattern = pattern.substring(0, colonIdx);
        }
      }
      const exactMatch = findExactModelReferenceMatch(globPattern, availableModels);
      if (exactMatch) {
        if (!scopedModels.find((sm) => modelsAreEqual2(sm.model, exactMatch))) {
          scopedModels.push({ model: exactMatch, thinkingLevel: thinkingLevel2 });
        }
        continue;
      }
      const matchingModels = availableModels.filter((m) => {
        const fullId = `${m.provider}/${m.id}`;
        return minimatch(fullId, globPattern, { nocase: true }) || minimatch(m.id, globPattern, { nocase: true });
      });
      if (matchingModels.length === 0) {
        diagnostics.push({
          type: "warning",
          code: "no-match",
          message: `No models match pattern "${pattern}"`,
          pattern
        });
        continue;
      }
      for (const model2 of matchingModels) {
        if (!scopedModels.find((sm) => modelsAreEqual2(sm.model, model2))) {
          scopedModels.push({ model: model2, thinkingLevel: thinkingLevel2 });
        }
      }
      continue;
    }
    const { model, thinkingLevel, warning } = parseModelPattern(pattern, availableModels);
    if (warning) {
      diagnostics.push({ type: "warning", code: "invalid-thinking-level", message: warning, pattern });
    }
    if (!model) {
      diagnostics.push({
        type: "warning",
        code: "no-match",
        message: `No models match pattern "${pattern}"`,
        pattern
      });
      continue;
    }
    if (!scopedModels.find((sm) => modelsAreEqual2(sm.model, model))) {
      scopedModels.push({ model, thinkingLevel });
    }
  }
  return { scopedModels, diagnostics };
}
__name(resolveModelScopeFromModels, "resolveModelScopeFromModels");
async function resolveModelScopeWithDiagnostics(patterns, modelRuntime, options) {
  return resolveModelScopeFromModels(patterns, await modelRuntime.getAvailable(void 0, options));
}
__name(resolveModelScopeWithDiagnostics, "resolveModelScopeWithDiagnostics");
async function resolveModelScope(patterns, modelRuntime, options) {
  const { scopedModels, diagnostics } = await resolveModelScopeWithDiagnostics(patterns, modelRuntime, options);
  for (const diagnostic of diagnostics) {
    console.warn(chalk2.yellow(`Warning: ${diagnostic.message}`));
  }
  return scopedModels;
}
__name(resolveModelScope, "resolveModelScope");
function resolveCliModel(options) {
  const { cliProvider, cliModel, cliThinking, modelRuntime } = options;
  if (!cliModel) {
    return { model: void 0, warning: void 0, error: void 0 };
  }
  const availableModels = [...modelRuntime.getModels()];
  if (availableModels.length === 0) {
    return {
      model: void 0,
      warning: void 0,
      error: "No models available. Check your installation or add models to models.json."
    };
  }
  const providerMap = /* @__PURE__ */ new Map();
  for (const m of availableModels) {
    providerMap.set(m.provider.toLowerCase(), m.provider);
  }
  let provider = cliProvider ? providerMap.get(cliProvider.toLowerCase()) : void 0;
  if (cliProvider && !provider) {
    return {
      model: void 0,
      warning: void 0,
      error: `Unknown provider "${cliProvider}". Use --list-models to see available providers/models.`
    };
  }
  let pattern = cliModel;
  let inferredProvider = false;
  if (!provider) {
    const slashIndex = cliModel.indexOf("/");
    if (slashIndex !== -1) {
      const maybeProvider = cliModel.substring(0, slashIndex);
      const canonical = providerMap.get(maybeProvider.toLowerCase());
      if (canonical) {
        provider = canonical;
        pattern = cliModel.substring(slashIndex + 1);
        inferredProvider = true;
      }
    }
  }
  if (!provider) {
    const lower = cliModel.toLowerCase();
    const exactMatches = availableModels.filter((m) => m.id.toLowerCase() === lower || `${m.provider}/${m.id}`.toLowerCase() === lower);
    if (exactMatches.length === 1) {
      return { model: exactMatches[0], warning: void 0, thinkingLevel: void 0, error: void 0 };
    }
    if (exactMatches.length > 1) {
      const authenticatedExactMatches = exactMatches.filter((m) => modelRuntime.hasConfiguredAuth(m.provider));
      if (authenticatedExactMatches.length === 1) {
        return {
          model: authenticatedExactMatches[0],
          warning: void 0,
          thinkingLevel: void 0,
          error: void 0
        };
      }
      const matches = exactMatches.map((m) => `${m.provider}/${m.id}`).sort((a, b) => a.localeCompare(b)).join(", ");
      const authHint = authenticatedExactMatches.length === 0 ? "No matching provider is authenticated." : "More than one matching provider is authenticated.";
      return {
        model: void 0,
        warning: void 0,
        thinkingLevel: void 0,
        error: `Model "${cliModel}" is ambiguous across providers: ${matches}. ${authHint} Use --provider or provider/model.`
      };
    }
  }
  if (cliProvider && provider) {
    const prefix = `${provider}/`;
    if (cliModel.toLowerCase().startsWith(prefix.toLowerCase())) {
      pattern = cliModel.substring(prefix.length);
    }
  }
  const candidates = provider ? availableModels.filter((m) => m.provider === provider) : availableModels;
  const { model, thinkingLevel, warning } = parseModelPattern(pattern, candidates, {
    allowInvalidThinkingLevelFallback: false
  });
  if (model) {
    if (inferredProvider) {
      const rawExactMatches = availableModels.filter((m) => m.id.toLowerCase() === cliModel.toLowerCase() && !modelsAreEqual2(m, model));
      if (rawExactMatches.length > 0 && !modelRuntime.hasConfiguredAuth(model.provider)) {
        const authenticatedRawMatches = rawExactMatches.filter((m) => modelRuntime.hasConfiguredAuth(m.provider));
        if (authenticatedRawMatches.length === 1) {
          return {
            model: authenticatedRawMatches[0],
            thinkingLevel: void 0,
            warning: void 0,
            error: void 0
          };
        }
      }
    }
    return { model, thinkingLevel, warning, error: void 0 };
  }
  if (inferredProvider) {
    const lower = cliModel.toLowerCase();
    const exact = availableModels.find((m) => m.id.toLowerCase() === lower || `${m.provider}/${m.id}`.toLowerCase() === lower);
    if (exact) {
      return { model: exact, warning: void 0, thinkingLevel: void 0, error: void 0 };
    }
    const fallback = parseModelPattern(cliModel, availableModels, {
      allowInvalidThinkingLevelFallback: false
    });
    if (fallback.model) {
      return {
        model: fallback.model,
        thinkingLevel: fallback.thinkingLevel,
        warning: fallback.warning,
        error: void 0
      };
    }
  }
  if (provider) {
    let fallbackPattern = pattern;
    let fallbackThinking;
    if (!cliThinking) {
      const lastColon = pattern.lastIndexOf(":");
      if (lastColon !== -1) {
        const suffix = pattern.substring(lastColon + 1);
        if (isValidThinkingLevel(suffix)) {
          fallbackPattern = pattern.substring(0, lastColon);
          fallbackThinking = suffix;
        }
      }
    }
    const fallbackModel = buildFallbackModel(provider, fallbackPattern, availableModels);
    if (fallbackModel) {
      const requestedThinking = cliThinking ?? fallbackThinking;
      const model2 = requestedThinking && requestedThinking !== "off" ? { ...fallbackModel, reasoning: true } : fallbackModel;
      const fallbackWarning = warning ? `${warning} Model "${fallbackPattern}" not found for provider "${provider}". Using custom model id.` : `Model "${fallbackPattern}" not found for provider "${provider}". Using custom model id.`;
      return { model: model2, thinkingLevel: fallbackThinking, warning: fallbackWarning, error: void 0 };
    }
  }
  const display = provider ? `${provider}/${pattern}` : cliModel;
  return {
    model: void 0,
    thinkingLevel: void 0,
    warning,
    error: `Model "${display}" not found. Use --list-models to see available models.`
  };
}
__name(resolveCliModel, "resolveCliModel");
async function findInitialModel(options) {
  const { cliProvider, cliModel, scopedModels, isContinuing, defaultProvider, defaultModelId, defaultThinkingLevel, modelThinkingLevels, modelRuntime } = options;
  let model;
  let thinkingLevel = DEFAULT_THINKING_LEVEL;
  if (cliProvider && cliModel) {
    const resolved = resolveCliModel({
      cliProvider,
      cliModel,
      modelRuntime
    });
    if (resolved.error) {
      console.error(chalk2.red(resolved.error));
      process.exit(1);
    }
    if (resolved.model) {
      return { model: resolved.model, thinkingLevel: DEFAULT_THINKING_LEVEL, fallbackMessage: void 0 };
    }
  }
  if (scopedModels.length > 0 && !isContinuing) {
    const scopedModel = scopedModels[0];
    const perModel = modelThinkingLevels?.[`${scopedModel.model.provider}/${scopedModel.model.id}`];
    return {
      model: scopedModel.model,
      thinkingLevel: scopedModel.thinkingLevel ?? perModel ?? defaultThinkingLevel ?? DEFAULT_THINKING_LEVEL,
      fallbackMessage: void 0
    };
  }
  if (defaultProvider && defaultModelId) {
    const found = modelRuntime.getModel(defaultProvider, defaultModelId);
    if (found && modelRuntime.hasConfiguredAuth(found.provider)) {
      model = found;
      const perModel = modelThinkingLevels?.[`${defaultProvider}/${defaultModelId}`];
      if (perModel) {
        thinkingLevel = perModel;
      } else if (defaultThinkingLevel) {
        thinkingLevel = defaultThinkingLevel;
      }
      return { model, thinkingLevel, fallbackMessage: void 0 };
    }
  }
  const availableModels = [...modelRuntime.getAvailableSnapshot()];
  if (availableModels.length > 0) {
    for (const provider of Object.keys(defaultModelPerProvider)) {
      const defaultId = defaultModelPerProvider[provider];
      const match = availableModels.find((m) => m.provider === provider && m.id === defaultId);
      if (match) {
        return { model: match, thinkingLevel: DEFAULT_THINKING_LEVEL, fallbackMessage: void 0 };
      }
    }
    return { model: availableModels[0], thinkingLevel: DEFAULT_THINKING_LEVEL, fallbackMessage: void 0 };
  }
  return { model: void 0, thinkingLevel: DEFAULT_THINKING_LEVEL, fallbackMessage: void 0 };
}
__name(findInitialModel, "findInitialModel");

// pi-dist/pi-coding-agent/core/model-runtime.js
import { dirname as dirname7, join as join10 } from "node:path";
import { createModels, lazyStream as lazyStream2, ModelsError, normalizeContext as normalizeContext4 } from "../../pi-ai/sdk-bundle/index.js";
import * as builtinProviderCatalog from "../../pi-ai/sdk-bundle/providers.js";

// pi-dist/pi-coding-agent/core/model-config.js
import { readFile } from "node:fs/promises";
import { Type } from "../../../typebox.mjs";
import { Compile } from "../../../typebox-compile.mjs";

// pi-dist/pi-coding-agent/utils/json.js
function stripJsonComments(input) {
  return input.replace(/"(?:\\.|[^"\\])*"|\/\/[^\n]*/g, (m) => m[0] === '"' ? m : "").replace(/"(?:\\.|[^"\\])*"|,(\s*[}\]])/g, (m, tail) => tail ?? (m[0] === '"' ? m : ""));
}
__name(stripJsonComments, "stripJsonComments");

// pi-dist/pi-coding-agent/core/model-config.js
var PercentileCutoffsSchema = Type.Object({
  p50: Type.Optional(Type.Number()),
  p75: Type.Optional(Type.Number()),
  p90: Type.Optional(Type.Number()),
  p99: Type.Optional(Type.Number())
});
var OpenRouterRoutingSchema = Type.Object({
  allow_fallbacks: Type.Optional(Type.Boolean()),
  require_parameters: Type.Optional(Type.Boolean()),
  data_collection: Type.Optional(Type.Union([Type.Literal("deny"), Type.Literal("allow")])),
  zdr: Type.Optional(Type.Boolean()),
  enforce_distillable_text: Type.Optional(Type.Boolean()),
  order: Type.Optional(Type.Array(Type.String())),
  only: Type.Optional(Type.Array(Type.String())),
  ignore: Type.Optional(Type.Array(Type.String())),
  quantizations: Type.Optional(Type.Array(Type.String())),
  sort: Type.Optional(Type.Union([
    Type.String(),
    Type.Object({
      by: Type.Optional(Type.String()),
      partition: Type.Optional(Type.Union([Type.String(), Type.Null()]))
    })
  ])),
  max_price: Type.Optional(Type.Object({
    prompt: Type.Optional(Type.Union([Type.Number(), Type.String()])),
    completion: Type.Optional(Type.Union([Type.Number(), Type.String()])),
    image: Type.Optional(Type.Union([Type.Number(), Type.String()])),
    audio: Type.Optional(Type.Union([Type.Number(), Type.String()])),
    request: Type.Optional(Type.Union([Type.Number(), Type.String()]))
  })),
  preferred_min_throughput: Type.Optional(Type.Union([Type.Number(), PercentileCutoffsSchema])),
  preferred_max_latency: Type.Optional(Type.Union([Type.Number(), PercentileCutoffsSchema]))
});
var VercelGatewayRoutingSchema = Type.Object({
  only: Type.Optional(Type.Array(Type.String())),
  order: Type.Optional(Type.Array(Type.String()))
});
var ThinkingLevelMapValueSchema = Type.Union([Type.String(), Type.Null()]);
var ThinkingLevelMapSchema = Type.Object({
  off: Type.Optional(ThinkingLevelMapValueSchema),
  minimal: Type.Optional(ThinkingLevelMapValueSchema),
  low: Type.Optional(ThinkingLevelMapValueSchema),
  medium: Type.Optional(ThinkingLevelMapValueSchema),
  high: Type.Optional(ThinkingLevelMapValueSchema),
  xhigh: Type.Optional(ThinkingLevelMapValueSchema),
  max: Type.Optional(ThinkingLevelMapValueSchema)
});
var ChatTemplateKwargScalarSchema = Type.Union([Type.String(), Type.Number(), Type.Boolean(), Type.Null()]);
var ChatTemplateKwargVariableSchema = Type.Object({
  $var: Type.Union([Type.Literal("thinking.enabled"), Type.Literal("thinking.effort")]),
  omitWhenOff: Type.Optional(Type.Boolean())
});
var ChatTemplateKwargSchema = Type.Union([ChatTemplateKwargScalarSchema, ChatTemplateKwargVariableSchema]);
var OpenAICompletionsCompatSchema = Type.Object({
  supportsStore: Type.Optional(Type.Boolean()),
  supportsDeveloperRole: Type.Optional(Type.Boolean()),
  supportsReasoningEffort: Type.Optional(Type.Boolean()),
  supportsUsageInStreaming: Type.Optional(Type.Boolean()),
  supportsFinishReason: Type.Optional(Type.Boolean()),
  maxTokensField: Type.Optional(Type.Union([Type.Literal("max_completion_tokens"), Type.Literal("max_tokens")])),
  requiresToolResultName: Type.Optional(Type.Boolean()),
  requiresAssistantAfterToolResult: Type.Optional(Type.Boolean()),
  requiresThinkingAsText: Type.Optional(Type.Boolean()),
  requiresReasoningContentOnAssistantMessages: Type.Optional(Type.Boolean()),
  thinkingFormat: Type.Optional(Type.Union([
    Type.Literal("openai"),
    Type.Literal("openrouter"),
    Type.Literal("together"),
    Type.Literal("baseten"),
    Type.Literal("deepseek"),
    Type.Literal("zai"),
    Type.Literal("qwen"),
    Type.Literal("chat-template"),
    Type.Literal("qwen-chat-template"),
    Type.Literal("string-thinking"),
    Type.Literal("ant-ling")
  ])),
  chatTemplateKwargs: Type.Optional(Type.Record(Type.String(), ChatTemplateKwargSchema)),
  chatTemplateArgs: Type.Optional(Type.Record(Type.String(), ChatTemplateKwargSchema)),
  cacheControlFormat: Type.Optional(Type.Literal("anthropic")),
  openRouterRouting: Type.Optional(OpenRouterRoutingSchema),
  vercelGatewayRouting: Type.Optional(VercelGatewayRoutingSchema),
  supportsOpenAIGrammarTools: Type.Optional(Type.Boolean()),
  supportsStrictMode: Type.Optional(Type.Boolean()),
  sendSessionAffinityHeaders: Type.Optional(Type.Boolean()),
  sessionAffinityFormat: Type.Optional(Type.Union([Type.Literal("openai"), Type.Literal("openai-nosession"), Type.Literal("openrouter")])),
  supportsLongCacheRetention: Type.Optional(Type.Boolean()),
  vllmPriority: Type.Optional(Type.Number())
});
var OpenAIResponsesCompatSchema = Type.Object({
  supportsDeveloperRole: Type.Optional(Type.Boolean()),
  sessionAffinityFormat: Type.Optional(Type.Union([Type.Literal("openai"), Type.Literal("openai-nosession"), Type.Literal("openrouter")])),
  supportsLongCacheRetention: Type.Optional(Type.Boolean()),
  supportsStrictMode: Type.Optional(Type.Boolean()),
  supportsOpenAIGrammarTools: Type.Optional(Type.Boolean()),
  supportsMaxOutputTokens: Type.Optional(Type.Boolean())
});
var ModelCostRatesSchema = {
  input: Type.Number(),
  output: Type.Number(),
  cacheRead: Type.Number(),
  cacheWrite: Type.Number()
};
var ModelCostTierSchema = Type.Object({
  inputTokensAbove: Type.Number(),
  ...ModelCostRatesSchema
});
var ModelCostSchema = Type.Object({
  ...ModelCostRatesSchema,
  tiers: Type.Optional(Type.Array(ModelCostTierSchema))
});
var ModelPromptCacheSchema = Type.Object({
  short: Type.Optional(Type.Number({ exclusiveMinimum: 0 })),
  long: Type.Optional(Type.Number({ exclusiveMinimum: 0 }))
});
var ImageResizeSchema = Type.Object({
  maxWidth: Type.Optional(Type.Integer({ minimum: 1 })),
  maxHeight: Type.Optional(Type.Integer({ minimum: 1 })),
  maxBytes: Type.Optional(Type.Integer({ minimum: 1 })),
  jpegQuality: Type.Optional(Type.Integer({ minimum: 1, maximum: 100 }))
});
var ModelInputLimitsSchema = Type.Object({
  maxRequestBytes: Type.Optional(Type.Integer({ minimum: 1 })),
  images: Type.Optional(Type.Object({
    resize: Type.Optional(ImageResizeSchema),
    maxPerMessage: Type.Optional(Type.Integer({ minimum: 1 })),
    maxPerRequest: Type.Optional(Type.Integer({ minimum: 1 }))
  }))
});
var AnthropicMessagesCompatSchema = Type.Object({
  supportsEagerToolInputStreaming: Type.Optional(Type.Boolean()),
  supportsLongCacheRetention: Type.Optional(Type.Boolean()),
  sendSessionAffinityHeaders: Type.Optional(Type.Boolean()),
  supportsCacheControlOnTools: Type.Optional(Type.Boolean()),
  supportsTemperature: Type.Optional(Type.Boolean()),
  forceAdaptiveThinking: Type.Optional(Type.Boolean()),
  allowEmptySignature: Type.Optional(Type.Boolean()),
  supportsStrictTools: Type.Optional(Type.Boolean()),
  supportsMidConvoEffort: Type.Optional(Type.Boolean()),
  allowedFallbackModels: Type.Optional(Type.Array(Type.Object({
    provider: Type.String({ minLength: 1 }),
    model: Type.String({ minLength: 1 }),
    cost: ModelCostSchema
  }), { maxItems: 3 }))
});
var ProviderCompatSchema = Type.Union([
  OpenAICompletionsCompatSchema,
  OpenAIResponsesCompatSchema,
  AnthropicMessagesCompatSchema
]);
var ModelDefinitionSchema = Type.Object({
  id: Type.String({ minLength: 1 }),
  name: Type.Optional(Type.String({ minLength: 1 })),
  api: Type.Optional(Type.String({ minLength: 1 })),
  baseUrl: Type.Optional(Type.String({ minLength: 1 })),
  reasoning: Type.Optional(Type.Boolean()),
  thinkingLevelMap: Type.Optional(ThinkingLevelMapSchema),
  input: Type.Optional(Type.Array(Type.Union([Type.Literal("text"), Type.Literal("image")]))),
  inputLimits: Type.Optional(ModelInputLimitsSchema),
  cost: Type.Optional(ModelCostSchema),
  promptCache: Type.Optional(ModelPromptCacheSchema),
  contextWindow: Type.Optional(Type.Number()),
  maxTokens: Type.Optional(Type.Number()),
  samplingParams: Type.Optional(Type.Record(Type.String(), Type.Unknown())),
  headers: Type.Optional(Type.Record(Type.String(), Type.String())),
  compat: Type.Optional(ProviderCompatSchema)
});
var ModelOverrideSchema = Type.Object({
  name: Type.Optional(Type.String({ minLength: 1 })),
  reasoning: Type.Optional(Type.Boolean()),
  thinkingLevelMap: Type.Optional(ThinkingLevelMapSchema),
  input: Type.Optional(Type.Array(Type.Union([Type.Literal("text"), Type.Literal("image")]))),
  inputLimits: Type.Optional(ModelInputLimitsSchema),
  cost: Type.Optional(Type.Object({
    input: Type.Optional(Type.Number()),
    output: Type.Optional(Type.Number()),
    cacheRead: Type.Optional(Type.Number()),
    cacheWrite: Type.Optional(Type.Number()),
    tiers: Type.Optional(Type.Array(ModelCostTierSchema))
  })),
  promptCache: Type.Optional(ModelPromptCacheSchema),
  contextWindow: Type.Optional(Type.Number()),
  maxTokens: Type.Optional(Type.Number()),
  samplingParams: Type.Optional(Type.Record(Type.String(), Type.Unknown())),
  headers: Type.Optional(Type.Record(Type.String(), Type.String())),
  compat: Type.Optional(ProviderCompatSchema)
});
var ProviderConfigSchema = Type.Object({
  name: Type.Optional(Type.String({ minLength: 1 })),
  baseUrl: Type.Optional(Type.String({ minLength: 1 })),
  apiKey: Type.Optional(Type.String({ minLength: 1 })),
  api: Type.Optional(Type.String({ minLength: 1 })),
  oauth: Type.Optional(Type.Literal("radius")),
  headers: Type.Optional(Type.Record(Type.String(), Type.String())),
  compat: Type.Optional(ProviderCompatSchema),
  authHeader: Type.Optional(Type.Boolean()),
  models: Type.Optional(Type.Array(ModelDefinitionSchema)),
  modelOverrides: Type.Optional(Type.Record(Type.String(), ModelOverrideSchema))
});
var ModelsConfigSchema = Type.Object({
  providers: Type.Record(Type.String(), ProviderConfigSchema)
});
var validateModelsConfig = Compile(ModelsConfigSchema);
function formatValidationPath(error) {
  if (error.keyword === "required") {
    const requiredProperties = error.params.requiredProperties;
    const requiredProperty = requiredProperties?.[0];
    if (requiredProperty) {
      const basePath = error.instancePath.replace(/^\//, "").replace(/\//g, ".");
      return basePath ? `${basePath}.${requiredProperty}` : requiredProperty;
    }
  }
  const path2 = error.instancePath.replace(/^\//, "").replace(/\//g, ".");
  return path2 || "root";
}
__name(formatValidationPath, "formatValidationPath");
function deepFreeze(value) {
  if (typeof value !== "object" || value === null || Object.isFrozen(value))
    return value;
  for (const child of Object.values(value))
    deepFreeze(child);
  return Object.freeze(value);
}
__name(deepFreeze, "deepFreeze");
var ModelConfig = class _ModelConfig {
  static {
    __name(this, "ModelConfig");
  }
  providers;
  error;
  constructor(providers, error) {
    this.providers = providers;
    this.error = error;
  }
  static async load(modelsJsonPath) {
    if (!modelsJsonPath)
      return new _ModelConfig(/* @__PURE__ */ new Map());
    const path2 = normalizePath(modelsJsonPath);
    let content;
    try {
      content = await readFile(path2, "utf-8");
    } catch (error) {
      if (error.code === "ENOENT")
        return new _ModelConfig(/* @__PURE__ */ new Map());
      return new _ModelConfig(/* @__PURE__ */ new Map(), `Failed to load models.json: ${error instanceof Error ? error.message : error}

File: ${path2}`);
    }
    let parsed;
    try {
      parsed = JSON.parse(stripJsonComments(stripBom(content)));
    } catch (error) {
      return new _ModelConfig(/* @__PURE__ */ new Map(), `Failed to parse models.json: ${error instanceof Error ? error.message : error}

File: ${path2}`);
    }
    if (!validateModelsConfig.Check(parsed)) {
      const errors = validateModelsConfig.Errors(parsed).map((error) => `  - ${formatValidationPath(error)}: ${error.message}`).join("\n") || "Unknown schema error";
      return new _ModelConfig(/* @__PURE__ */ new Map(), `Invalid models.json schema:
${errors}

File: ${path2}`);
    }
    const config = parsed;
    const providers = /* @__PURE__ */ new Map();
    for (const [providerId, provider] of Object.entries(config.providers)) {
      providers.set(providerId, deepFreeze(structuredClone(provider)));
    }
    return new _ModelConfig(providers);
  }
  getProvider(providerId) {
    return this.providers.get(providerId);
  }
  getProviderIds() {
    return [...this.providers.keys()];
  }
  getError() {
    return this.error;
  }
};

// pi-dist/pi-coding-agent/core/models-store.js
import { join as join9 } from "node:path";
var sharedModelsFileReadState;
var InMemoryCodingAgentModelsStore = class {
  static {
    __name(this, "InMemoryCodingAgentModelsStore");
  }
  entries = /* @__PURE__ */ new Map();
  async read(providerId, options) {
    options?.signal?.throwIfAborted();
    const entry = this.entries.get(providerId);
    return entry ? structuredClone(entry) : void 0;
  }
  async write(providerId, entry, options) {
    options?.signal?.throwIfAborted();
    this.entries.set(providerId, structuredClone(entry));
  }
  async delete(providerId, options) {
    options?.signal?.throwIfAborted();
    this.entries.delete(providerId);
  }
};
var FileModelsStore = class {
  static {
    __name(this, "FileModelsStore");
  }
  storage;
  path;
  readState;
  constructor(path2 = join9(getAgentDir(), "models-store.json")) {
    this.path = normalizePath(path2);
    this.storage = new FileAuthStorageBackend(this.path);
    this.readState = sharedModelsFileReadState?.path === this.path ? sharedModelsFileReadState.readState : { data: {} };
    if (!sharedModelsFileReadState) {
      sharedModelsFileReadState = { path: this.path, readState: this.readState };
    }
  }
  parse(content) {
    return content ? JSON.parse(stripBom(content)) : {};
  }
  updateReadState(readState, data, revision) {
    readState.data = data;
    readState.revision = revision;
  }
  reloadFromStorage(readState, options) {
    return this.storage.withLockAsync(async (content) => {
      const data = this.parse(content);
      this.updateReadState(readState, data, getFileRevision(this.path));
      return { result: data };
    }, options);
  }
  async readLatest(readState, options) {
    options?.signal?.throwIfAborted();
    const revision = getFileRevision(this.path);
    if (revision !== void 0 && revision === readState.revision)
      return readState.data;
    if (!readState.reload) {
      const controller = new AbortController();
      const reload2 = {
        controller,
        promise: this.reloadFromStorage(readState, { signal: controller.signal }),
        readers: 0
      };
      readState.reload = reload2;
      void reload2.promise.then(() => {
        if (readState.reload === reload2)
          readState.reload = void 0;
      }, () => {
        if (readState.reload === reload2)
          readState.reload = void 0;
      });
    }
    const reload = readState.reload;
    reload.readers++;
    try {
      return await raceWithAbortSignal(reload.promise, options?.signal);
    } finally {
      reload.readers--;
      if (reload.readers === 0 && readState.reload === reload) {
        readState.reload = void 0;
        reload.controller.abort();
      }
    }
  }
  async read(providerId, options) {
    const entry = (await this.readLatest(this.readState, options))[providerId];
    options?.signal?.throwIfAborted();
    return entry ? structuredClone(entry) : void 0;
  }
  async write(providerId, entry, options) {
    let latest;
    await this.storage.withLockAsync(async (content) => {
      const current = this.parse(content);
      current[providerId] = structuredClone(entry);
      latest = current;
      return { result: void 0, next: JSON.stringify(current, null, 2) };
    }, options);
    if (latest)
      this.updateReadState(this.readState, latest);
  }
  async delete(providerId, options) {
    let latest;
    await this.storage.withLockAsync(async (content) => {
      const current = this.parse(content);
      delete current[providerId];
      latest = current;
      return { result: void 0, next: JSON.stringify(current, null, 2) };
    }, options);
    if (latest)
      this.updateReadState(this.readState, latest);
  }
};

// pi-dist/pi-coding-agent/core/remote-catalog-provider.js
var DEFAULT_CATALOG_BASE_URL = "https://pi-in-go.dev";
var REMOTE_CATALOG_ATTEMPT_TIMEOUT_MS = 4e3;
var REMOTE_CATALOG_REFRESH_INTERVAL_MS = 4 * 60 * 60 * 1e3;
function mergeModels(baseline, dynamic) {
  const merged = [...baseline];
  for (const model of dynamic) {
    const index = merged.findIndex((entry) => entry.id === model.id);
    if (index >= 0)
      merged[index] = model;
    else
      merged.push(model);
  }
  return merged;
}
__name(mergeModels, "mergeModels");
function parseCatalog(providerId, value) {
  const entries = Array.isArray(value) ? value : typeof value === "object" && value !== null && "models" in value && Array.isArray(value.models) ? value.models : typeof value === "object" && value !== null ? Object.values(value) : void 0;
  if (!entries)
    throw new Error(`Invalid model catalog for provider "${providerId}"`);
  return entries.filter((entry) => typeof entry === "object" && entry !== null && "id" in entry).map((model) => ({ ...model, provider: providerId }));
}
__name(parseCatalog, "parseCatalog");
function remoteModels(entry, localGeneratedAt) {
  if (!entry)
    return [];
  if (localGeneratedAt !== void 0 && (entry.lastModified === void 0 || entry.lastModified <= localGeneratedAt)) {
    return [];
  }
  return entry.models;
}
__name(remoteModels, "remoteModels");
function withRemoteCatalog(provider, catalogBaseUrl = DEFAULT_CATALOG_BASE_URL, localGeneratedAt) {
  let dynamicModels = [];
  return {
    ...provider,
    getModels: /* @__PURE__ */ __name(() => mergeModels(provider.getModels(), dynamicModels), "getModels"),
    refreshModels: /* @__PURE__ */ __name(async (context) => {
      const stored = context.stored;
      const restored = remoteModels(stored, localGeneratedAt).filter((model) => model.provider === provider.id);
      if (!await context.publish({
        update: /* @__PURE__ */ __name(() => {
          dynamicModels = restored;
        }, "update")
      })) {
        return;
      }
      if (!context.allowNetwork || context.signal.aborted)
        return;
      if (!context.force && stored?.checkedAt !== void 0 && stored.lastModified !== void 0 && Date.now() - stored.checkedAt < REMOTE_CATALOG_REFRESH_INTERVAL_MS) {
        return;
      }
      const validator = stored?.models.length ? stored.etag : void 0;
      const url = new URL(`/api/models/providers/${encodeURIComponent(provider.id)}`, catalogBaseUrl);
      const response = await fetchWithRetry(url, {
        headers: {
          accept: "application/json",
          "User-Agent": getPiUserAgent(VERSION),
          ...validator ? { "if-none-match": validator } : {}
        },
        signal: context.signal
      }, { attemptTimeoutMs: REMOTE_CATALOG_ATTEMPT_TIMEOUT_MS });
      if (context.signal.aborted)
        return;
      const checkedAt = Date.now();
      if (response.status === 304 && stored) {
        await context.publish({ persist: { ...stored, checkedAt } });
        return;
      }
      if (response.status === 404 || response.status === 501) {
        await context.publish({
          persist: {
            ...stored ?? { models: [] },
            checkedAt,
            lastModified: 0,
            etag: void 0
          }
        });
        return;
      }
      if (!response.ok) {
        await context.publish({ persist: { ...stored ?? { models: [] }, checkedAt } });
        throw new Error(`Model catalog request failed for ${provider.id}: ${response.status}`);
      }
      const refreshed = parseCatalog(provider.id, await response.json());
      const lastModified = Date.parse(response.headers.get("last-modified") ?? "");
      if (context.signal.aborted)
        return;
      const entry = {
        models: refreshed,
        checkedAt,
        lastModified: Number.isNaN(lastModified) ? 0 : lastModified,
        etag: response.headers.get("etag") ?? void 0
      };
      const published = remoteModels(entry, localGeneratedAt);
      await context.publish({
        persist: entry,
        update: /* @__PURE__ */ __name(() => {
          dynamicModels = published;
        }, "update")
      });
    }, "refreshModels")
  };
}
__name(withRemoteCatalog, "withRemoteCatalog");

// pi-dist/pi-coding-agent/core/runtime-credentials.js
var RuntimeCredentials = class {
  static {
    __name(this, "RuntimeCredentials");
  }
  store;
  overrides = /* @__PURE__ */ new Map();
  constructor(store) {
    this.store = store;
  }
  setRuntimeApiKey(providerId, apiKey) {
    this.overrides.set(providerId, apiKey);
  }
  removeRuntimeApiKey(providerId) {
    this.overrides.delete(providerId);
  }
  hasRuntimeApiKey(providerId) {
    return this.overrides.has(providerId);
  }
  async read(providerId, options) {
    options?.signal?.throwIfAborted();
    const override = this.overrides.get(providerId);
    return override ? { type: "api_key", key: override } : this.store.read(providerId, options);
  }
  async list(options) {
    const entries = new Map((await this.store.list(options)).map((entry) => [entry.providerId, entry]));
    options?.signal?.throwIfAborted();
    for (const providerId of this.overrides.keys()) {
      entries.set(providerId, { providerId, type: "api_key" });
    }
    return [...entries.values()];
  }
  modify(providerId, fn, options) {
    return this.store.modify(providerId, fn, options);
  }
  async delete(providerId, options) {
    options?.signal?.throwIfAborted();
    await this.store.delete(providerId, options);
    this.overrides.delete(providerId);
  }
};

// pi-dist/pi-coding-agent/core/model-runtime.js
var CredentialSynchronizationError = class extends Error {
  static {
    __name(this, "CredentialSynchronizationError");
  }
  providerId;
  operation;
  credential;
  constructor(providerId, operation, credential, options) {
    super(`Credential ${operation} committed for ${providerId}, but local synchronization failed`, options);
    this.name = "CredentialSynchronizationError";
    this.providerId = providerId;
    this.operation = operation;
    this.credential = credential;
  }
};
function mergeHeaders(base, override) {
  if (!base && !override)
    return void 0;
  const merged = { ...base };
  for (const [name, value] of Object.entries(override ?? {})) {
    const lowerName = name.toLowerCase();
    for (const existingName of Object.keys(merged)) {
      if (existingName.toLowerCase() === lowerName)
        delete merged[existingName];
    }
    merged[name] = value;
  }
  return merged;
}
__name(mergeHeaders, "mergeHeaders");
var ModelRuntime = class _ModelRuntime {
  static {
    __name(this, "ModelRuntime");
  }
  models;
  credentials;
  defaultBuiltins;
  builtins = /* @__PURE__ */ new Map();
  nativeExtensionProviders = /* @__PURE__ */ new Map();
  extensionProviders = /* @__PURE__ */ new Map();
  compositionErrors = /* @__PURE__ */ new Map();
  modelsPath;
  modelNetworkEnabled;
  config;
  snapshot = {
    all: [],
    available: [],
    configuredProviders: /* @__PURE__ */ new Set(),
    storedProviders: /* @__PURE__ */ new Set(),
    auth: /* @__PURE__ */ new Map()
  };
  availabilityRefreshSeq = 0;
  availabilityErrorSeq = 0;
  providerAvailabilitySeq = /* @__PURE__ */ new Map();
  availabilityError;
  credentialOperations = /* @__PURE__ */ new Map();
  constructor(credentials, config, modelsPath, modelsStore, providers, modelNetworkEnabled) {
    this.credentials = credentials;
    this.config = config;
    this.modelsPath = modelsPath;
    this.modelNetworkEnabled = modelNetworkEnabled;
    this.defaultBuiltins = new Map(providers.map((provider) => [provider.id, provider]));
    for (const [providerId, provider] of this.defaultBuiltins)
      this.builtins.set(providerId, provider);
    this.models = createModels({ credentials, modelsStore });
    this.rebuildProviders();
  }
  static async create(options = {}) {
    const credentials = new RuntimeCredentials(options.credentials ?? AuthStorage.create(options.authPath));
    const modelsPath = options.modelsPath === null ? void 0 : options.modelsPath ?? join10(getAgentDir(), "models.json");
    const config = await ModelConfig.load(modelsPath);
    const modelsStore = options.modelsStore ?? (modelsPath ? new FileModelsStore(options.modelsStorePath ?? join10(dirname7(modelsPath), "models-store.json")) : new InMemoryCodingAgentModelsStore());
    const builtinModelDataGeneratedAt = builtinProviderCatalog.getBuiltinModelDataGeneratedAt();
    const providers = builtinProviderCatalog.builtinProviders().map((provider) => provider.id === "radius" ? provider : withRemoteCatalog(provider, options.catalogBaseUrl, builtinModelDataGeneratedAt));
    const runtime = new _ModelRuntime(credentials, config, modelsPath, modelsStore, providers, process.env.PI_OFFLINE === void 0);
    runtime.configureRadiusProviders();
    runtime.rebuildProviders();
    const refreshFromNetwork = runtime.modelNetworkEnabled && options.allowModelNetwork === true;
    const controller = refreshFromNetwork && options.modelRefreshTimeoutMs !== void 0 ? new AbortController() : void 0;
    const timeout = controller ? setTimeout(() => controller.abort(), options.modelRefreshTimeoutMs) : void 0;
    const signal = controller ? options.signal ? AbortSignal.any([options.signal, controller.signal]) : controller.signal : options.signal;
    try {
      if (options.refreshOnCreate !== false) {
        await runtime.refresh({ allowNetwork: refreshFromNetwork, signal });
      }
    } finally {
      if (timeout)
        clearTimeout(timeout);
    }
    return runtime;
  }
  configureRadiusProviders() {
    this.builtins.clear();
    for (const [providerId, provider] of this.defaultBuiltins)
      this.builtins.set(providerId, provider);
    for (const providerId of this.config.getProviderIds()) {
      const config = this.config.getProvider(providerId);
      if (config?.oauth !== "radius" || !config.baseUrl)
        continue;
      this.builtins.set(providerId, builtinProviderCatalog.radiusProvider({
        id: providerId,
        name: config.name ?? providerId,
        gateway: config.baseUrl.replace(/\/v1\/?$/u, "")
      }));
    }
  }
  providerIds() {
    return /* @__PURE__ */ new Set([
      ...this.builtins.keys(),
      ...this.nativeExtensionProviders.keys(),
      ...this.config.getProviderIds(),
      ...this.extensionProviders.keys()
    ]);
  }
  recomposeProvider(providerId) {
    const base = this.nativeExtensionProviders.get(providerId) ?? this.builtins.get(providerId);
    const extension = this.extensionProviders.get(providerId);
    if (!base && !this.config.getProvider(providerId) && !extension) {
      this.models.deleteProvider(providerId);
      this.compositionErrors.delete(providerId);
      return;
    }
    if (base && !this.config.getProvider(providerId) && !extension) {
      this.models.setProvider(base);
      this.compositionErrors.delete(providerId);
      return;
    }
    try {
      this.models.setProvider(composeModelProvider(providerId, base, this.config, extension));
      this.compositionErrors.delete(providerId);
    } catch (error) {
      this.compositionErrors.set(providerId, error instanceof Error ? error.message : String(error));
      if (base)
        this.models.setProvider(base);
      else
        this.models.deleteProvider(providerId);
    }
  }
  rebuildProviders() {
    this.models.clearProviders();
    this.compositionErrors.clear();
    for (const providerId of this.providerIds())
      this.recomposeProvider(providerId);
    this.updateModelSnapshot();
  }
  updateModelSnapshot() {
    const all = [...this.models.getModels()];
    this.snapshot = {
      ...this.snapshot,
      all,
      available: all.filter((model) => this.snapshot.configuredProviders.has(model.provider))
    };
  }
  async runAvailabilityRefresh(seq, errorSeq, signal) {
    const providers = this.models.getProviders();
    const [available, checks, credentials] = await Promise.all([
      this.models.getAvailable(void 0, { signal }),
      Promise.all(providers.map(async (provider) => [
        provider.id,
        await this.models.checkAuth(provider.id, { signal })
      ])),
      this.credentials.list({ signal })
    ]);
    if (seq !== this.availabilityRefreshSeq)
      return;
    const auth = new Map(checks);
    const configuredProviders = new Set(checks.filter((entry) => entry[1] !== void 0).map(([providerId]) => providerId));
    this.snapshot = {
      all: [...this.models.getModels()],
      available: [...available],
      configuredProviders,
      storedProviders: new Set(credentials.map((entry) => entry.providerId)),
      auth
    };
    if (errorSeq === this.availabilityErrorSeq)
      this.availabilityError = void 0;
  }
  queueAvailabilityRefresh(signal) {
    const seq = ++this.availabilityRefreshSeq;
    for (const [providerId, providerSeq] of this.providerAvailabilitySeq) {
      this.providerAvailabilitySeq.set(providerId, providerSeq + 1);
    }
    const errorSeq = ++this.availabilityErrorSeq;
    const effectiveSignal = operationSignal(signal);
    return this.runAvailabilityRefresh(seq, errorSeq, effectiveSignal).catch((error) => {
      if (errorSeq === this.availabilityErrorSeq && !effectiveSignal.aborted) {
        this.availabilityError = error instanceof Error ? error.message : String(error);
      }
      throw error;
    });
  }
  async refreshProviderAvailability(providerId, signal) {
    ++this.availabilityRefreshSeq;
    const providerSeq = (this.providerAvailabilitySeq.get(providerId) ?? 0) + 1;
    this.providerAvailabilitySeq.set(providerId, providerSeq);
    const errorSeq = ++this.availabilityErrorSeq;
    try {
      const [available, auth, credential] = await Promise.all([
        this.models.getAvailable(providerId, { signal }),
        this.models.checkAuth(providerId, { signal }),
        this.credentials.read(providerId, { signal })
      ]);
      signal.throwIfAborted();
      if (this.providerAvailabilitySeq.get(providerId) !== providerSeq)
        return;
      const configuredProviders = new Set(this.snapshot.configuredProviders);
      const storedProviders = new Set(this.snapshot.storedProviders);
      const authByProvider = new Map(this.snapshot.auth);
      if (auth) {
        configuredProviders.add(providerId);
        authByProvider.set(providerId, auth);
      } else {
        configuredProviders.delete(providerId);
        authByProvider.delete(providerId);
      }
      if (credential)
        storedProviders.add(providerId);
      else
        storedProviders.delete(providerId);
      const all = [...this.models.getModels()];
      const availableById = new Map([...this.snapshot.available.filter((model) => model.provider !== providerId), ...available].map((model) => [
        `${model.provider}\0${model.id}`,
        model
      ]));
      this.snapshot = {
        all,
        available: all.flatMap((model) => availableById.get(`${model.provider}\0${model.id}`) ?? []),
        configuredProviders,
        storedProviders,
        auth: authByProvider
      };
      if (errorSeq === this.availabilityErrorSeq)
        this.availabilityError = void 0;
    } catch (error) {
      if (this.providerAvailabilitySeq.get(providerId) === providerSeq && errorSeq === this.availabilityErrorSeq && !signal.aborted) {
        this.availabilityError = error instanceof Error ? error.message : String(error);
      }
      throw error;
    }
  }
  getProviders() {
    return this.models.getProviders();
  }
  getProvider(providerId) {
    return this.models.getProvider(providerId);
  }
  getModels(providerId) {
    return this.models.getModels(providerId);
  }
  getModel(providerId, modelId) {
    return this.models.getModel(providerId, modelId);
  }
  async checkAuth(providerId, options) {
    return this.models.checkAuth(providerId, options);
  }
  async getAvailable(providerId, options) {
    if (providerId) {
      const errorSeq = ++this.availabilityErrorSeq;
      try {
        const available = await this.models.getAvailable(providerId, options);
        if (errorSeq === this.availabilityErrorSeq)
          this.availabilityError = void 0;
        return available;
      } catch (error) {
        if (errorSeq === this.availabilityErrorSeq && !options?.signal?.aborted) {
          this.availabilityError = error instanceof Error ? error.message : String(error);
        }
        throw error;
      }
    }
    await this.queueAvailabilityRefresh(options?.signal);
    return this.snapshot.available;
  }
  getAvailableSnapshot() {
    return this.snapshot.available;
  }
  getError() {
    const errors = [];
    const configError = this.config.getError();
    if (configError)
      errors.push(configError);
    for (const [providerId, error] of this.compositionErrors) {
      errors.push(`Provider "${providerId}": ${error}`);
    }
    if (this.availabilityError)
      errors.push(`Availability refresh: ${this.availabilityError}`);
    return errors.length > 0 ? errors.join("\n\n") : void 0;
  }
  getRegisteredProviderConfig(providerId) {
    return this.extensionProviders.get(providerId);
  }
  getRegisteredProviderIds() {
    return [.../* @__PURE__ */ new Set([...this.extensionProviders.keys(), ...this.nativeExtensionProviders.keys()])];
  }
  getRegisteredNativeProvider(providerId) {
    return this.nativeExtensionProviders.get(providerId);
  }
  /** @internal Compatibility fallback for ModelRegistry when provider auth is unconfigured. */
  getCompatibilityRequestConfig(model) {
    return resolveCompatibilityRequestConfig(model, this.config.getProvider(model.provider), this.extensionProviders.get(model.provider));
  }
  isUsingOAuth(providerId) {
    return this.snapshot.auth.get(providerId)?.type === "oauth";
  }
  isUsingSubscription(providerId) {
    return this.isUsingOAuth(providerId) && this.models.getProvider(providerId)?.auth.oauth?.isSubscription === true;
  }
  hasConfiguredAuth(providerId) {
    return this.snapshot.configuredProviders.has(providerId);
  }
  async getAuth(providerOrModel, overrides = {}) {
    if (typeof providerOrModel === "string")
      return this.models.getAuth(providerOrModel, overrides);
    const resolution = await this.models.getAuth(providerOrModel, overrides);
    if (!resolution)
      return void 0;
    const configuredHeaders2 = resolveConfiguredModelHeaders(providerOrModel, this.config.getProvider(providerOrModel.provider), this.extensionProviders.get(providerOrModel.provider), { ...resolution.env ?? {}, ...overrides.env ?? {} });
    return {
      ...resolution,
      auth: {
        ...resolution.auth,
        headers: mergeHeaders(resolution.auth.headers, configuredHeaders2)
      }
    };
  }
  enqueueCredentialOperation(providerId, signal, task) {
    const previous = this.credentialOperations.get(providerId) ?? Promise.resolve();
    let markStarted;
    const started = new Promise((resolve9) => {
      markStarted = resolve9;
    });
    const operation = (async () => {
      await previous.catch(() => {
      });
      signal.throwIfAborted();
      markStarted?.();
      return task();
    })();
    const tail = operation.catch(() => {
    });
    this.credentialOperations.set(providerId, tail);
    void tail.then(() => {
      if (this.credentialOperations.get(providerId) === tail)
        this.credentialOperations.delete(providerId);
    });
    return raceWithAbortSignal(started, signal).then(() => operation);
  }
  async synchronizeCredentialState(providerId, operation, credential, signal) {
    try {
      signal.throwIfAborted();
      this.recomposeProvider(providerId);
      const compositionError = this.compositionErrors.get(providerId);
      if (compositionError)
        throw new Error(compositionError);
      const result = await this.models.refresh({ allowNetwork: false, providers: [providerId], signal });
      if (result.aborted)
        signal.throwIfAborted();
      const refreshError = result.errors.get(providerId);
      if (refreshError)
        throw refreshError;
      this.updateModelSnapshot();
      await this.refreshProviderAvailability(providerId, signal);
    } catch (cause) {
      throw new CredentialSynchronizationError(providerId, operation, credential, { cause });
    }
  }
  setRuntimeApiKey(providerId, apiKey, options = {}) {
    const signal = operationSignal(options.signal);
    return this.enqueueCredentialOperation(providerId, signal, async () => {
      this.credentials.setRuntimeApiKey(providerId, apiKey);
      await this.synchronizeCredentialState(providerId, "setRuntimeApiKey", { type: "api_key", key: apiKey }, signal);
    });
  }
  removeRuntimeApiKey(providerId, options = {}) {
    const signal = operationSignal(options.signal);
    return this.enqueueCredentialOperation(providerId, signal, async () => {
      this.credentials.removeRuntimeApiKey(providerId);
      await this.synchronizeCredentialState(providerId, "removeRuntimeApiKey", void 0, signal);
    });
  }
  listCredentials(options) {
    return this.credentials.list(options);
  }
  getProviderAuthStatus(providerId) {
    if (this.credentials.hasRuntimeApiKey(providerId))
      return { configured: true, source: "runtime" };
    if (this.snapshot.storedProviders.has(providerId))
      return { configured: true, source: "stored" };
    const configured = configuredRequestAuthStatus(this.config.getProvider(providerId), this.extensionProviders.get(providerId));
    if (configured)
      return configured;
    const check = this.snapshot.auth.get(providerId);
    return check ? { configured: true, source: "environment", label: check.source } : { configured: false };
  }
  async prepareRequest(model, options) {
    const provider = this.models.getProvider(model.provider);
    if (!provider)
      throw new ModelsError("provider", `Unknown provider: ${model.provider}`);
    const resolution = await this.getAuth(model, {
      apiKey: options?.apiKey,
      env: options?.env,
      signal: options?.signal
    });
    if (!resolution)
      throw new ModelsError("auth", `Provider is not configured: ${model.provider}`);
    const { transformHeaders, ...rawProviderOptions } = options ?? {};
    const providerOptions = rawProviderOptions;
    let headers = mergeHeaders(resolution.auth.headers, providerOptions.headers);
    if (transformHeaders)
      headers = await transformHeaders(headers ?? {});
    const env = resolution.env || providerOptions.env ? { ...resolution.env ?? {}, ...providerOptions.env ?? {} } : void 0;
    return {
      provider,
      model: resolution.auth.baseUrl ? { ...model, baseUrl: resolution.auth.baseUrl } : model,
      options: {
        ...providerOptions,
        apiKey: providerOptions.apiKey ?? resolution.auth.apiKey,
        headers,
        env
      }
    };
  }
  stream(model, context, options) {
    const transcript = normalizeContext4(context);
    return lazyStream2(model, async () => {
      const prepared = await this.prepareRequest(model, options);
      return prepared.provider.stream(prepared.model, transcript, prepared.options);
    });
  }
  complete(model, context, options) {
    return this.stream(model, context, options).result();
  }
  streamSimple(model, context, options) {
    const transcript = normalizeContext4(context);
    return lazyStream2(model, async () => {
      const prepared = await this.prepareRequest(model, options);
      return prepared.provider.streamSimple(prepared.model, transcript, prepared.options);
    });
  }
  completeSimple(model, context, options) {
    return this.streamSimple(model, context, options).result();
  }
  streamDeferred(model, handle, options) {
    return lazyStream2(model, async () => {
      const prepared = await this.prepareRequest(model, options);
      if (!prepared.provider.fetchDeferred) {
        throw new ModelsError("provider", `Provider ${model.provider} does not support deferred responses`);
      }
      return prepared.provider.fetchDeferred(prepared.model, handle, prepared.options);
    });
  }
  async fetchDeferred(model, handle, options) {
    return this.streamDeferred(model, handle, options).result();
  }
  async cancelDeferred(model, handle, options) {
    const prepared = await this.prepareRequest(model, options);
    if (!prepared.provider.cancelDeferred) {
      throw new ModelsError("provider", `Provider ${model.provider} does not support deferred responses`);
    }
    await prepared.provider.cancelDeferred(prepared.model, handle, prepared.options);
  }
  login(providerId, type, interaction) {
    const signal = operationSignal(interaction.signal);
    return this.enqueueCredentialOperation(providerId, signal, async () => {
      const credential = await this.models.login(providerId, type, { ...interaction, signal });
      await this.synchronizeCredentialState(providerId, "login", credential, signal);
      return credential;
    });
  }
  logout(providerId, options = {}) {
    const signal = operationSignal(options.signal);
    return this.enqueueCredentialOperation(providerId, signal, async () => {
      await this.models.logout(providerId, { signal });
      await this.synchronizeCredentialState(providerId, "logout", void 0, signal);
    });
  }
  async refresh(options = {}) {
    this.config = await ModelConfig.load(this.modelsPath);
    this.configureRadiusProviders();
    if (options.providers) {
      for (const providerId of new Set(options.providers))
        this.recomposeProvider(providerId);
      this.updateModelSnapshot();
    } else {
      this.rebuildProviders();
    }
    const refreshOptions = {
      ...options,
      allowNetwork: options.allowNetwork ?? this.modelNetworkEnabled
    };
    const result = await this.models.refresh(refreshOptions) ?? {
      aborted: refreshOptions.signal?.aborted ?? false,
      errors: /* @__PURE__ */ new Map()
    };
    const errors = new Map(result.errors);
    this.updateModelSnapshot();
    if (options.providers) {
      await Promise.all([...new Set(options.providers)].map(async (providerId) => {
        try {
          await this.refreshProviderAvailability(providerId, operationSignal(options.signal));
        } catch (error) {
          if (!options.signal?.aborted) {
            errors.set(providerId, error instanceof Error ? error : new Error(String(error)));
          }
        }
      }));
    } else {
      try {
        await this.queueAvailabilityRefresh(options.signal);
      } catch {
      }
    }
    return { aborted: result.aborted || (options.signal?.aborted ?? false), errors };
  }
  registerNativeProvider(provider) {
    if (!provider.id.trim())
      throw new Error("Provider id must not be empty.");
    this.extensionProviders.delete(provider.id);
    this.nativeExtensionProviders.set(provider.id, provider);
    this.recomposeProvider(provider.id);
    this.updateModelSnapshot();
    void this.refresh({ allowNetwork: false });
  }
  registerProvider(providerId, config) {
    validateExtensionProvider(providerId, this.builtins.get(providerId), this.config.getProvider(providerId), config);
    this.nativeExtensionProviders.delete(providerId);
    const previous = this.extensionProviders.get(providerId);
    const effective = { ...previous };
    for (const [key, value] of Object.entries(config)) {
      if (value !== void 0)
        effective[key] = value;
    }
    this.extensionProviders.set(providerId, effective);
    this.recomposeProvider(providerId);
    this.updateModelSnapshot();
    if (this.snapshot.storedProviders.has(providerId) || configuredRequestAuthStatus(this.config.getProvider(providerId), effective)?.configured) {
      const configuredProviders = new Set(this.snapshot.configuredProviders).add(providerId);
      const auth = new Map(this.snapshot.auth);
      if (!auth.get(providerId)) {
        auth.set(providerId, {
          type: effective.oauth && !effective.apiKey ? "oauth" : "api_key",
          source: "configured provider"
        });
      }
      this.snapshot = {
        ...this.snapshot,
        auth,
        configuredProviders,
        available: this.snapshot.all.filter((model) => configuredProviders.has(model.provider))
      };
    }
    void this.refresh({ allowNetwork: false });
  }
  unregisterProvider(providerId) {
    this.extensionProviders.delete(providerId);
    this.nativeExtensionProviders.delete(providerId);
    this.recomposeProvider(providerId);
    this.updateModelSnapshot();
    void this.refresh({ allowNetwork: false });
  }
};

// pi-dist/pi-coding-agent/core/package-manager.js
import { createHash } from "node:crypto";
import { chmodSync, existsSync as existsSync8, globSync, mkdirSync as mkdirSync4, readdirSync as readdirSync5, readFileSync as readFileSync7, rmSync, statSync as statSync5, writeFileSync as writeFileSync5 } from "node:fs";
import { homedir } from "node:os";
import { basename as basename6, dirname as dirname8, join as join11, relative as relative2, resolve as resolve5, sep as sep3 } from "node:path";
import ignore2 from "../../../ignore/index.js";
import { minimatch as minimatch2 } from "../../../minimatch/dist/esm/index.js";
import { gt, maxSatisfying, rcompare, satisfies, valid, validRange } from "../../../semver/index.js";

// pi-dist/pi-coding-agent/utils/git.js
import hostedGitInfo from "../../../hosted-git-info/lib/index.js";
function splitRef(url) {
  const scpLikeMatch = url.match(/^git@([^:]+):(.+)$/);
  if (scpLikeMatch) {
    const pathWithMaybeRef2 = scpLikeMatch[2] ?? "";
    const refSeparator2 = pathWithMaybeRef2.indexOf("@");
    if (refSeparator2 < 0)
      return { repo: url };
    const repoPath2 = pathWithMaybeRef2.slice(0, refSeparator2);
    const ref2 = pathWithMaybeRef2.slice(refSeparator2 + 1);
    if (!repoPath2 || !ref2)
      return { repo: url };
    return {
      repo: `git@${scpLikeMatch[1] ?? ""}:${repoPath2}`,
      ref: ref2
    };
  }
  if (url.includes("://")) {
    try {
      const parsed = new URL(url);
      const pathWithMaybeRef2 = parsed.pathname.replace(/^\/+/, "");
      const refSeparator2 = pathWithMaybeRef2.indexOf("@");
      if (refSeparator2 < 0)
        return { repo: url };
      const repoPath2 = pathWithMaybeRef2.slice(0, refSeparator2);
      const ref2 = pathWithMaybeRef2.slice(refSeparator2 + 1);
      if (!repoPath2 || !ref2)
        return { repo: url };
      parsed.pathname = `/${repoPath2}`;
      return {
        repo: parsed.toString().replace(/\/$/, ""),
        ref: ref2
      };
    } catch {
      return { repo: url };
    }
  }
  const slashIndex = url.indexOf("/");
  if (slashIndex < 0) {
    return { repo: url };
  }
  const host = url.slice(0, slashIndex);
  const pathWithMaybeRef = url.slice(slashIndex + 1);
  const refSeparator = pathWithMaybeRef.indexOf("@");
  if (refSeparator < 0) {
    return { repo: url };
  }
  const repoPath = pathWithMaybeRef.slice(0, refSeparator);
  const ref = pathWithMaybeRef.slice(refSeparator + 1);
  if (!repoPath || !ref) {
    return { repo: url };
  }
  return {
    repo: `${host}/${repoPath}`,
    ref
  };
}
__name(splitRef, "splitRef");
function decodeForValidation(value) {
  try {
    return decodeURIComponent(value);
  } catch {
    return null;
  }
}
__name(decodeForValidation, "decodeForValidation");
function hasUnsafeGitInstallPart(value, allowSlash) {
  const decoded = decodeForValidation(value);
  if (decoded === null) {
    return true;
  }
  const candidates = [value, decoded];
  for (const candidate of candidates) {
    if (candidate.includes("\0") || candidate.includes("\\") || candidate.startsWith("/")) {
      return true;
    }
    if (!allowSlash && candidate.includes("/")) {
      return true;
    }
    if (candidate.split("/").includes("..")) {
      return true;
    }
  }
  return false;
}
__name(hasUnsafeGitInstallPart, "hasUnsafeGitInstallPart");
function buildGitSource(args) {
  if (args.path.startsWith("/")) {
    return null;
  }
  const normalizedPath = args.path.replace(/\.git$/, "").replace(/^\/+/, "");
  if (!args.host || !normalizedPath || normalizedPath.split("/").length < 2) {
    return null;
  }
  if (hasUnsafeGitInstallPart(args.host, false) || hasUnsafeGitInstallPart(normalizedPath, true)) {
    return null;
  }
  return {
    type: "git",
    repo: args.repo,
    host: args.host,
    path: normalizedPath,
    ref: args.ref,
    pinned: Boolean(args.ref)
  };
}
__name(buildGitSource, "buildGitSource");
function parseGenericGitUrl(url) {
  const { repo: repoWithoutRef, ref } = splitRef(url);
  let repo = repoWithoutRef;
  let host = "";
  let path2 = "";
  const scpLikeMatch = repoWithoutRef.match(/^git@([^:]+):(.+)$/);
  if (scpLikeMatch) {
    host = scpLikeMatch[1] ?? "";
    path2 = scpLikeMatch[2] ?? "";
  } else if (repoWithoutRef.startsWith("https://") || repoWithoutRef.startsWith("http://") || repoWithoutRef.startsWith("ssh://") || repoWithoutRef.startsWith("git://")) {
    try {
      const parsed = new URL(repoWithoutRef);
      host = parsed.hostname;
      path2 = parsed.pathname.replace(/^\/+/, "");
    } catch {
      return null;
    }
  } else {
    const slashIndex = repoWithoutRef.indexOf("/");
    if (slashIndex < 0) {
      return null;
    }
    host = repoWithoutRef.slice(0, slashIndex);
    path2 = repoWithoutRef.slice(slashIndex + 1);
    if (!host.includes(".") && host !== "localhost") {
      return null;
    }
    repo = `https://${repoWithoutRef}`;
  }
  return buildGitSource({ repo, host, path: path2, ref });
}
__name(parseGenericGitUrl, "parseGenericGitUrl");
function parseGitUrl(source) {
  const trimmed = source.trim();
  const hasGitPrefix = trimmed.startsWith("git:");
  const url = hasGitPrefix ? trimmed.slice(4).trim() : trimmed;
  if (!hasGitPrefix && !/^(https?|ssh|git):\/\//i.test(url)) {
    return null;
  }
  const split = splitRef(url);
  const hostedCandidates = [split.ref ? `${split.repo}#${split.ref}` : void 0, url].filter((value) => Boolean(value));
  for (const candidate of hostedCandidates) {
    const info = hostedGitInfo.fromUrl(candidate);
    if (info) {
      if (split.ref && info.project?.includes("@")) {
        continue;
      }
      const useHttpsPrefix = !split.repo.startsWith("http://") && !split.repo.startsWith("https://") && !split.repo.startsWith("ssh://") && !split.repo.startsWith("git://") && !split.repo.startsWith("git@");
      return buildGitSource({
        repo: useHttpsPrefix ? `https://${split.repo}` : split.repo,
        host: info.domain || "",
        path: `${info.user}/${info.project}`,
        ref: info.committish || split.ref || void 0
      });
    }
  }
  const httpsCandidates = [split.ref ? `https://${split.repo}#${split.ref}` : void 0, `https://${url}`].filter((value) => Boolean(value));
  for (const candidate of httpsCandidates) {
    const info = hostedGitInfo.fromUrl(candidate);
    if (info) {
      if (split.ref && info.project?.includes("@")) {
        continue;
      }
      return buildGitSource({
        repo: `https://${split.repo}`,
        host: info.domain || "",
        path: `${info.user}/${info.project}`,
        ref: info.committish || split.ref || void 0
      });
    }
  }
  return parseGenericGitUrl(url);
}
__name(parseGitUrl, "parseGitUrl");

// pi-dist/pi-coding-agent/core/output-guard.js
var stdoutTakeoverState;
var RAW_STDOUT_RETRY_DELAY_MS = 10;
var rawStdoutWriteTail = Promise.resolve();
function getRawStdoutWrite() {
  if (stdoutTakeoverState) {
    return stdoutTakeoverState.rawStdoutWrite;
  }
  return process.stdout.write.bind(process.stdout);
}
__name(getRawStdoutWrite, "getRawStdoutWrite");
async function writeRawStdoutChunk(text) {
  while (true) {
    try {
      await new Promise((resolve9, reject) => {
        try {
          getRawStdoutWrite()(text, (error) => {
            if (error)
              reject(error);
            else
              resolve9();
          });
        } catch (error) {
          reject(error instanceof Error ? error : new Error(String(error)));
        }
      });
      return;
    } catch (error) {
      const writeError = error instanceof Error ? error : new Error(String(error));
      const code = writeError.code;
      if (code !== "ENOBUFS" && code !== "EAGAIN" && code !== "EWOULDBLOCK") {
        throw writeError;
      }
      await new Promise((resolve9) => setTimeout(resolve9, RAW_STDOUT_RETRY_DELAY_MS));
    }
  }
}
__name(writeRawStdoutChunk, "writeRawStdoutChunk");
function takeOverStdout() {
  if (stdoutTakeoverState) {
    return;
  }
  const rawStdoutWrite = process.stdout.write.bind(process.stdout);
  const rawStderrWrite = process.stderr.write.bind(process.stderr);
  const originalStdoutWrite = process.stdout.write;
  process.stdout.write = ((chunk, encodingOrCallback, callback) => {
    if (typeof encodingOrCallback === "function") {
      return rawStderrWrite(String(chunk), encodingOrCallback);
    }
    return rawStderrWrite(String(chunk), callback);
  });
  stdoutTakeoverState = {
    rawStdoutWrite,
    rawStderrWrite,
    originalStdoutWrite
  };
}
__name(takeOverStdout, "takeOverStdout");
function restoreStdout() {
  if (!stdoutTakeoverState) {
    return;
  }
  process.stdout.write = stdoutTakeoverState.originalStdoutWrite;
  stdoutTakeoverState = void 0;
}
__name(restoreStdout, "restoreStdout");
function isStdoutTakenOver() {
  return stdoutTakeoverState !== void 0;
}
__name(isStdoutTakenOver, "isStdoutTakenOver");
function writeRawStdout(text) {
  if (text.length === 0) {
    return;
  }
  rawStdoutWriteTail = rawStdoutWriteTail.then(() => writeRawStdoutChunk(text));
  void rawStdoutWriteTail.catch(() => {
    process.exit(1);
  });
}
__name(writeRawStdout, "writeRawStdout");
async function waitForRawStdoutBackpressure() {
  while (true) {
    const tail = rawStdoutWriteTail;
    await tail;
    if (tail === rawStdoutWriteTail) {
      return;
    }
  }
}
__name(waitForRawStdoutBackpressure, "waitForRawStdoutBackpressure");
async function flushRawStdout() {
  await waitForRawStdoutBackpressure();
  await writeRawStdoutChunk("");
}
__name(flushRawStdout, "flushRawStdout");

// pi-dist/pi-coding-agent/core/package-manager.js
function getEnv() {
  if (process.platform !== "linux" || Object.keys(process.env).length > 0) {
    return process.env;
  }
  try {
    const data = readFileSync7("/proc/self/environ", "utf-8");
    const env = {};
    for (const entry of data.split("\0")) {
      const idx = entry.indexOf("=");
      if (idx > 0) {
        env[entry.slice(0, idx)] = entry.slice(idx + 1);
      }
    }
    return env;
  } catch {
    return process.env;
  }
}
__name(getEnv, "getEnv");
var NETWORK_TIMEOUT_MS = 1e4;
var UPDATE_CHECK_CONCURRENCY = 4;
var GIT_UPDATE_CONCURRENCY = 4;
function isOfflineModeEnabled() {
  const value = process.env.PI_OFFLINE;
  if (!value)
    return false;
  return value === "1" || value.toLowerCase() === "true" || value.toLowerCase() === "yes";
}
__name(isOfflineModeEnabled, "isOfflineModeEnabled");
function isExactNpmVersion(version2) {
  return valid(version2 ?? "") !== null;
}
__name(isExactNpmVersion, "isExactNpmVersion");
function getNpmVersionRange(version2) {
  return version2 ? validRange(version2) ?? void 0 : void 0;
}
__name(getNpmVersionRange, "getNpmVersionRange");
function resourcePrecedenceRank(m) {
  if (m.origin === "package")
    return 4;
  const scopeBase = m.scope === "project" ? 0 : 2;
  return scopeBase + (m.source === "local" ? 0 : 1);
}
__name(resourcePrecedenceRank, "resourcePrecedenceRank");
var RESOURCE_TYPES = ["extensions", "skills", "prompts", "themes"];
var FILE_PATTERNS = {
  extensions: /\.(ts|js)$/,
  skills: /\.md$/,
  prompts: /\.md$/,
  themes: /\.json$/
};
var IGNORE_FILE_NAMES2 = [".gitignore", ".ignore", ".fdignore"];
function toPosixPath2(p) {
  return p.split(sep3).join("/");
}
__name(toPosixPath2, "toPosixPath");
function getHomeDir() {
  return process.env.HOME || homedir();
}
__name(getHomeDir, "getHomeDir");
function getExtensionTempFolder(agentDir) {
  const tempFolder = join11(agentDir, "tmp", "extensions");
  mkdirSync4(tempFolder, { recursive: true, mode: 448 });
  chmodSync(tempFolder, 448);
  return tempFolder;
}
__name(getExtensionTempFolder, "getExtensionTempFolder");
function prefixIgnorePattern2(line, prefix) {
  const trimmed = line.trim();
  if (!trimmed)
    return null;
  if (trimmed.startsWith("#") && !trimmed.startsWith("\\#"))
    return null;
  let pattern = line;
  let negated = false;
  if (pattern.startsWith("!")) {
    negated = true;
    pattern = pattern.slice(1);
  } else if (pattern.startsWith("\\!")) {
    pattern = pattern.slice(1);
  }
  if (pattern.startsWith("/")) {
    pattern = pattern.slice(1);
  }
  const prefixed = prefix ? `${prefix}${pattern}` : pattern;
  return negated ? `!${prefixed}` : prefixed;
}
__name(prefixIgnorePattern2, "prefixIgnorePattern");
function addIgnoreRules2(ig, dir, rootDir) {
  const relativeDir = relative2(rootDir, dir);
  const prefix = relativeDir ? `${toPosixPath2(relativeDir)}/` : "";
  for (const filename of IGNORE_FILE_NAMES2) {
    const ignorePath = join11(dir, filename);
    if (!existsSync8(ignorePath))
      continue;
    try {
      const content = readFileSync7(ignorePath, "utf-8");
      const patterns = content.split(/\r?\n/).map((line) => prefixIgnorePattern2(line, prefix)).filter((line) => Boolean(line));
      if (patterns.length > 0) {
        ig.add(patterns);
      }
    } catch {
    }
  }
}
__name(addIgnoreRules2, "addIgnoreRules");
function isPattern(s) {
  return s.startsWith("!") || s.startsWith("+") || s.startsWith("-") || s.includes("*") || s.includes("?");
}
__name(isPattern, "isPattern");
function isOverridePattern(s) {
  return s.startsWith("!") || s.startsWith("+") || s.startsWith("-");
}
__name(isOverridePattern, "isOverridePattern");
function hasGlobPattern(s) {
  return s.includes("*") || s.includes("?");
}
__name(hasGlobPattern, "hasGlobPattern");
function expandPackageGlob(pattern, root) {
  return globSync(pattern, { cwd: root }).map((match) => resolve5(root, match)).filter((path2) => relative2(root, path2).split(sep3).every((segment) => segment === ".." || !segment.startsWith("."))).sort((a, b) => a < b ? -1 : a > b ? 1 : 0);
}
__name(expandPackageGlob, "expandPackageGlob");
function splitPatterns(entries) {
  const plain = [];
  const patterns = [];
  for (const entry of entries) {
    if (isPattern(entry)) {
      patterns.push(entry);
    } else {
      plain.push(entry);
    }
  }
  return { plain, patterns };
}
__name(splitPatterns, "splitPatterns");
function collectFiles(dir, filePattern, skipNodeModules = true, ignoreMatcher, rootDir) {
  const files = [];
  if (!existsSync8(dir))
    return files;
  const root = rootDir ?? dir;
  const ig = ignoreMatcher ?? ignore2();
  addIgnoreRules2(ig, dir, root);
  try {
    const entries = readdirSync5(dir, { withFileTypes: true });
    for (const entry of entries) {
      if (entry.name.startsWith("."))
        continue;
      if (skipNodeModules && entry.name === "node_modules")
        continue;
      const fullPath = join11(dir, entry.name);
      let isDir = entry.isDirectory();
      let isFile = entry.isFile();
      if (entry.isSymbolicLink()) {
        try {
          const stats = statSync5(fullPath);
          isDir = stats.isDirectory();
          isFile = stats.isFile();
        } catch {
          continue;
        }
      }
      const relPath = toPosixPath2(relative2(root, fullPath));
      const ignorePath = isDir ? `${relPath}/` : relPath;
      if (ig.ignores(ignorePath))
        continue;
      if (isDir) {
        files.push(...collectFiles(fullPath, filePattern, skipNodeModules, ig, root));
      } else if (isFile && filePattern.test(entry.name)) {
        files.push(fullPath);
      }
    }
  } catch {
  }
  return files;
}
__name(collectFiles, "collectFiles");
function collectSkillEntries(dir, mode, ignoreMatcher, rootDir) {
  const entries = [];
  if (!existsSync8(dir))
    return entries;
  const root = rootDir ?? dir;
  const ig = ignoreMatcher ?? ignore2();
  addIgnoreRules2(ig, dir, root);
  try {
    const dirEntries = readdirSync5(dir, { withFileTypes: true });
    for (const entry of dirEntries) {
      if (entry.name !== "SKILL.md") {
        continue;
      }
      const fullPath = join11(dir, entry.name);
      let isFile = entry.isFile();
      if (entry.isSymbolicLink()) {
        try {
          isFile = statSync5(fullPath).isFile();
        } catch {
          continue;
        }
      }
      const relPath = toPosixPath2(relative2(root, fullPath));
      if (isFile && !ig.ignores(relPath)) {
        entries.push(fullPath);
        return entries;
      }
    }
    for (const entry of dirEntries) {
      if (entry.name.startsWith("."))
        continue;
      if (entry.name === "node_modules")
        continue;
      const fullPath = join11(dir, entry.name);
      let isDir = entry.isDirectory();
      let isFile = entry.isFile();
      if (entry.isSymbolicLink()) {
        try {
          const stats = statSync5(fullPath);
          isDir = stats.isDirectory();
          isFile = stats.isFile();
        } catch {
          continue;
        }
      }
      const relPath = toPosixPath2(relative2(root, fullPath));
      const shouldIncludeMarkdownFile = isFile && entry.name.endsWith(".md") && !ig.ignores(relPath) && (mode === "pi" && dir === root || mode === "agents" && dir !== root);
      if (shouldIncludeMarkdownFile) {
        entries.push(fullPath);
        continue;
      }
      if (!isDir)
        continue;
      if (ig.ignores(`${relPath}/`))
        continue;
      entries.push(...collectSkillEntries(fullPath, mode, ig, root));
    }
  } catch {
  }
  return entries;
}
__name(collectSkillEntries, "collectSkillEntries");
function collectAutoSkillEntries(dir, mode) {
  return collectSkillEntries(dir, mode);
}
__name(collectAutoSkillEntries, "collectAutoSkillEntries");
function findGitRepoRoot(startDir) {
  let dir = resolve5(startDir);
  while (true) {
    if (existsSync8(join11(dir, ".git"))) {
      return dir;
    }
    const parent = dirname8(dir);
    if (parent === dir) {
      return null;
    }
    dir = parent;
  }
}
__name(findGitRepoRoot, "findGitRepoRoot");
function collectAncestorAgentsSkillDirs(startDir) {
  const skillDirs = [];
  const resolvedStartDir = resolve5(startDir);
  const gitRepoRoot = findGitRepoRoot(resolvedStartDir);
  let dir = resolvedStartDir;
  while (true) {
    skillDirs.push(join11(dir, ".agents", "skills"));
    if (gitRepoRoot && dir === gitRepoRoot) {
      break;
    }
    const parent = dirname8(dir);
    if (parent === dir) {
      break;
    }
    dir = parent;
  }
  return skillDirs;
}
__name(collectAncestorAgentsSkillDirs, "collectAncestorAgentsSkillDirs");
function collectAutoPromptEntries(dir) {
  const entries = [];
  if (!existsSync8(dir))
    return entries;
  const ig = ignore2();
  addIgnoreRules2(ig, dir, dir);
  try {
    const dirEntries = readdirSync5(dir, { withFileTypes: true });
    for (const entry of dirEntries) {
      if (entry.name.startsWith("."))
        continue;
      if (entry.name === "node_modules")
        continue;
      const fullPath = join11(dir, entry.name);
      let isFile = entry.isFile();
      if (entry.isSymbolicLink()) {
        try {
          isFile = statSync5(fullPath).isFile();
        } catch {
          continue;
        }
      }
      const relPath = toPosixPath2(relative2(dir, fullPath));
      if (ig.ignores(relPath))
        continue;
      if (isFile && entry.name.endsWith(".md")) {
        entries.push(fullPath);
      }
    }
  } catch {
  }
  return entries;
}
__name(collectAutoPromptEntries, "collectAutoPromptEntries");
function collectAutoThemeEntries(dir) {
  const entries = [];
  if (!existsSync8(dir))
    return entries;
  const ig = ignore2();
  addIgnoreRules2(ig, dir, dir);
  try {
    const dirEntries = readdirSync5(dir, { withFileTypes: true });
    for (const entry of dirEntries) {
      if (entry.name.startsWith("."))
        continue;
      if (entry.name === "node_modules")
        continue;
      const fullPath = join11(dir, entry.name);
      let isFile = entry.isFile();
      if (entry.isSymbolicLink()) {
        try {
          isFile = statSync5(fullPath).isFile();
        } catch {
          continue;
        }
      }
      const relPath = toPosixPath2(relative2(dir, fullPath));
      if (ig.ignores(relPath))
        continue;
      if (isFile && entry.name.endsWith(".json")) {
        entries.push(fullPath);
      }
    }
  } catch {
  }
  return entries;
}
__name(collectAutoThemeEntries, "collectAutoThemeEntries");
function resolveExtensionEntries2(dir) {
  const packageJsonPath = join11(dir, "package.json");
  if (existsSync8(packageJsonPath)) {
    const manifest = readPiManifest(packageJsonPath);
    if (manifest?.extensions?.length) {
      const entries = [];
      for (const extPath of manifest.extensions) {
        const resolvedExtPath = resolve5(dir, extPath);
        if (existsSync8(resolvedExtPath)) {
          entries.push(resolvedExtPath);
        }
      }
      if (entries.length > 0) {
        return entries;
      }
    }
  }
  const indexTs = join11(dir, "index.ts");
  const indexJs = join11(dir, "index.js");
  if (existsSync8(indexTs)) {
    return [indexTs];
  }
  if (existsSync8(indexJs)) {
    return [indexJs];
  }
  return null;
}
__name(resolveExtensionEntries2, "resolveExtensionEntries");
function collectAutoExtensionEntries(dir) {
  const entries = [];
  if (!existsSync8(dir))
    return entries;
  const rootEntries = resolveExtensionEntries2(dir);
  if (rootEntries) {
    return rootEntries;
  }
  const ig = ignore2();
  addIgnoreRules2(ig, dir, dir);
  try {
    const dirEntries = readdirSync5(dir, { withFileTypes: true });
    for (const entry of dirEntries) {
      if (entry.name.startsWith("."))
        continue;
      if (entry.name === "node_modules")
        continue;
      const fullPath = join11(dir, entry.name);
      let isDir = entry.isDirectory();
      let isFile = entry.isFile();
      if (entry.isSymbolicLink()) {
        try {
          const stats = statSync5(fullPath);
          isDir = stats.isDirectory();
          isFile = stats.isFile();
        } catch {
          continue;
        }
      }
      const relPath = toPosixPath2(relative2(dir, fullPath));
      const ignorePath = isDir ? `${relPath}/` : relPath;
      if (ig.ignores(ignorePath))
        continue;
      if (isFile && (entry.name.endsWith(".ts") || entry.name.endsWith(".js"))) {
        entries.push(fullPath);
      } else if (isDir) {
        const resolvedEntries = resolveExtensionEntries2(fullPath);
        if (resolvedEntries) {
          entries.push(...resolvedEntries);
        }
      }
    }
  } catch {
  }
  return entries;
}
__name(collectAutoExtensionEntries, "collectAutoExtensionEntries");
function collectResourceFiles(dir, resourceType) {
  if (resourceType === "skills") {
    return collectSkillEntries(dir, "pi");
  }
  if (resourceType === "extensions") {
    return collectAutoExtensionEntries(dir);
  }
  return collectFiles(dir, FILE_PATTERNS[resourceType]);
}
__name(collectResourceFiles, "collectResourceFiles");
function matchesAnyPattern(filePath, patterns, baseDir) {
  const rel = toPosixPath2(relative2(baseDir, filePath));
  const name = basename6(filePath);
  const filePathPosix = toPosixPath2(filePath);
  const isSkillFile = name === "SKILL.md";
  const parentDir = isSkillFile ? dirname8(filePath) : void 0;
  const parentRel = isSkillFile ? toPosixPath2(relative2(baseDir, parentDir)) : void 0;
  const parentName = isSkillFile ? basename6(parentDir) : void 0;
  const parentDirPosix = isSkillFile ? toPosixPath2(parentDir) : void 0;
  return patterns.some((pattern) => {
    const normalizedPattern = toPosixPath2(pattern);
    if (minimatch2(rel, normalizedPattern) || minimatch2(name, normalizedPattern) || minimatch2(filePathPosix, normalizedPattern)) {
      return true;
    }
    if (!isSkillFile)
      return false;
    return minimatch2(parentRel, normalizedPattern) || minimatch2(parentName, normalizedPattern) || minimatch2(parentDirPosix, normalizedPattern);
  });
}
__name(matchesAnyPattern, "matchesAnyPattern");
function normalizeExactPattern(pattern) {
  const normalized = pattern.startsWith("./") || pattern.startsWith(".\\") ? pattern.slice(2) : pattern;
  return toPosixPath2(normalized);
}
__name(normalizeExactPattern, "normalizeExactPattern");
function matchesAnyExactPattern(filePath, patterns, baseDir) {
  if (patterns.length === 0)
    return false;
  const rel = toPosixPath2(relative2(baseDir, filePath));
  const name = basename6(filePath);
  const filePathPosix = toPosixPath2(filePath);
  const isSkillFile = name === "SKILL.md";
  const parentDir = isSkillFile ? dirname8(filePath) : void 0;
  const parentRel = isSkillFile ? toPosixPath2(relative2(baseDir, parentDir)) : void 0;
  const parentDirPosix = isSkillFile ? toPosixPath2(parentDir) : void 0;
  return patterns.some((pattern) => {
    const normalized = normalizeExactPattern(pattern);
    if (normalized === rel || normalized === filePathPosix) {
      return true;
    }
    if (!isSkillFile)
      return false;
    return normalized === parentRel || normalized === parentDirPosix;
  });
}
__name(matchesAnyExactPattern, "matchesAnyExactPattern");
function getOverridePatterns(entries) {
  return entries.filter((pattern) => pattern.startsWith("!") || pattern.startsWith("+") || pattern.startsWith("-"));
}
__name(getOverridePatterns, "getOverridePatterns");
function isEnabledByOverrides(filePath, patterns, baseDir) {
  const overrides = getOverridePatterns(patterns);
  const excludes = overrides.filter((pattern) => pattern.startsWith("!")).map((pattern) => pattern.slice(1));
  const forceIncludes = overrides.filter((pattern) => pattern.startsWith("+")).map((pattern) => pattern.slice(1));
  const forceExcludes = overrides.filter((pattern) => pattern.startsWith("-")).map((pattern) => pattern.slice(1));
  let enabled = true;
  if (excludes.length > 0 && matchesAnyPattern(filePath, excludes, baseDir)) {
    enabled = false;
  }
  if (forceIncludes.length > 0 && matchesAnyExactPattern(filePath, forceIncludes, baseDir)) {
    enabled = true;
  }
  if (forceExcludes.length > 0 && matchesAnyExactPattern(filePath, forceExcludes, baseDir)) {
    enabled = false;
  }
  return enabled;
}
__name(isEnabledByOverrides, "isEnabledByOverrides");
function applyPatterns(allPaths, patterns, baseDir) {
  const includes = [];
  const excludes = [];
  const forceIncludes = [];
  const forceExcludes = [];
  for (const p of patterns) {
    if (p.startsWith("+")) {
      forceIncludes.push(p.slice(1));
    } else if (p.startsWith("-")) {
      forceExcludes.push(p.slice(1));
    } else if (p.startsWith("!")) {
      excludes.push(p.slice(1));
    } else {
      includes.push(p);
    }
  }
  let result;
  if (includes.length === 0) {
    result = [...allPaths];
  } else {
    result = allPaths.filter((filePath) => matchesAnyPattern(filePath, includes, baseDir));
  }
  if (excludes.length > 0) {
    result = result.filter((filePath) => !matchesAnyPattern(filePath, excludes, baseDir));
  }
  if (forceIncludes.length > 0) {
    for (const filePath of allPaths) {
      if (!result.includes(filePath) && matchesAnyExactPattern(filePath, forceIncludes, baseDir)) {
        result.push(filePath);
      }
    }
  }
  if (forceExcludes.length > 0) {
    result = result.filter((filePath) => !matchesAnyExactPattern(filePath, forceExcludes, baseDir));
  }
  return new Set(result);
}
__name(applyPatterns, "applyPatterns");
function applyAutoloadDisabledPatterns(allPaths, patterns, baseDir) {
  const result = /* @__PURE__ */ new Map();
  for (const pattern of patterns) {
    const target = pattern.slice(pattern.startsWith("+") || pattern.startsWith("-") || pattern.startsWith("!") ? 1 : 0);
    const enabled = !pattern.startsWith("-") && !pattern.startsWith("!");
    const exact = pattern.startsWith("+") || pattern.startsWith("-");
    for (const filePath of allPaths) {
      if (exact ? matchesAnyExactPattern(filePath, [target], baseDir) : matchesAnyPattern(filePath, [target], baseDir)) {
        result.set(filePath, enabled);
      }
    }
  }
  return result;
}
__name(applyAutoloadDisabledPatterns, "applyAutoloadDisabledPatterns");
var DefaultPackageManager = class {
  static {
    __name(this, "DefaultPackageManager");
  }
  cwd;
  agentDir;
  settingsManager;
  globalNpmRoot;
  globalNpmRootCommandKey;
  progressCallback;
  constructor(options) {
    this.cwd = resolvePath(options.cwd);
    this.agentDir = resolvePath(options.agentDir);
    this.settingsManager = options.settingsManager;
  }
  setProgressCallback(callback) {
    this.progressCallback = callback;
  }
  addSourceToSettings(source, options) {
    const scope = options?.local ? "project" : "user";
    const currentSettings = scope === "project" ? this.settingsManager.getProjectSettings() : this.settingsManager.getGlobalSettings();
    const currentPackages = currentSettings.packages ?? [];
    const normalizedSource = this.normalizePackageSourceForSettings(source, scope);
    const matchIndex = currentPackages.findIndex((existing) => this.packageSourcesMatch(existing, source, scope));
    if (matchIndex !== -1) {
      const existing = currentPackages[matchIndex];
      if (this.getPackageSourceString(existing) === normalizedSource) {
        return false;
      }
      const nextPackages2 = [...currentPackages];
      nextPackages2[matchIndex] = typeof existing === "string" ? normalizedSource : { ...existing, source: normalizedSource };
      if (scope === "project") {
        this.settingsManager.setProjectPackages(nextPackages2);
      } else {
        this.settingsManager.setPackages(nextPackages2);
      }
      return true;
    }
    const nextPackages = [...currentPackages, normalizedSource];
    if (scope === "project") {
      this.settingsManager.setProjectPackages(nextPackages);
    } else {
      this.settingsManager.setPackages(nextPackages);
    }
    return true;
  }
  removeSourceFromSettings(source, options) {
    const scope = options?.local ? "project" : "user";
    const currentSettings = scope === "project" ? this.settingsManager.getProjectSettings() : this.settingsManager.getGlobalSettings();
    const currentPackages = currentSettings.packages ?? [];
    const nextPackages = currentPackages.filter((existing) => !this.packageSourcesMatch(existing, source, scope));
    const changed = nextPackages.length !== currentPackages.length;
    if (!changed) {
      return false;
    }
    if (scope === "project") {
      this.settingsManager.setProjectPackages(nextPackages);
    } else {
      this.settingsManager.setPackages(nextPackages);
    }
    return true;
  }
  getInstalledPath(source, scope) {
    const parsed = this.parseSource(source);
    if (parsed.type === "npm") {
      const path2 = this.getNpmInstallPath(parsed, scope);
      return existsSync8(path2) ? path2 : void 0;
    }
    if (parsed.type === "git") {
      const path2 = this.getGitInstallPath(parsed, scope);
      return existsSync8(path2) ? path2 : void 0;
    }
    if (parsed.type === "local") {
      const baseDir = this.getBaseDirForScope(scope);
      const path2 = this.resolvePathFromBase(parsed.path, baseDir);
      return existsSync8(path2) ? path2 : void 0;
    }
    return void 0;
  }
  emitProgress(event) {
    this.progressCallback?.(event);
  }
  async withProgress(action, source, message, operation) {
    this.emitProgress({ type: "start", action, source, message });
    try {
      await operation();
      this.emitProgress({ type: "complete", action, source });
    } catch (error) {
      const errorMessage = error instanceof Error ? error.message : String(error);
      this.emitProgress({ type: "error", action, source, message: errorMessage });
      throw error;
    }
  }
  async resolve(onMissing) {
    const accumulator = this.createAccumulator();
    const globalSettings = this.settingsManager.getGlobalSettings();
    const projectSettings = this.settingsManager.getProjectSettings();
    const allPackages = [];
    for (const pkg of projectSettings.packages ?? []) {
      allPackages.push({ pkg, scope: "project" });
    }
    for (const pkg of globalSettings.packages ?? []) {
      allPackages.push({ pkg, scope: "user" });
    }
    const packageSources = this.dedupePackages(allPackages);
    await this.resolvePackageSources(packageSources, accumulator, onMissing);
    const globalBaseDir = this.agentDir;
    const projectBaseDir = join11(this.cwd, CONFIG_DIR_NAME);
    for (const resourceType of RESOURCE_TYPES) {
      const target = this.getTargetMap(accumulator, resourceType);
      const globalEntries = globalSettings[resourceType] ?? [];
      const projectEntries = projectSettings[resourceType] ?? [];
      this.resolveLocalEntries(projectEntries, resourceType, target, {
        source: "local",
        scope: "project",
        origin: "top-level"
      }, projectBaseDir);
      this.resolveLocalEntries(globalEntries, resourceType, target, {
        source: "local",
        scope: "user",
        origin: "top-level"
      }, globalBaseDir);
    }
    this.addAutoDiscoveredResources(accumulator, globalSettings, projectSettings, globalBaseDir, projectBaseDir);
    return this.toResolvedPaths(accumulator);
  }
  async resolveExtensionSources(sources, options) {
    const accumulator = this.createAccumulator();
    const scope = options?.temporary ? "temporary" : options?.local ? "project" : "user";
    const packageSources = sources.map((source) => ({ pkg: source, scope }));
    await this.resolvePackageSources(packageSources, accumulator);
    return this.toResolvedPaths(accumulator);
  }
  listConfiguredPackages() {
    const globalSettings = this.settingsManager.getGlobalSettings();
    const projectSettings = this.settingsManager.getProjectSettings();
    const configuredPackages = [];
    for (const pkg of globalSettings.packages ?? []) {
      const source = typeof pkg === "string" ? pkg : pkg.source;
      configuredPackages.push({
        source,
        scope: "user",
        filtered: typeof pkg === "object",
        installedPath: this.getInstalledPath(source, "user")
      });
    }
    for (const pkg of projectSettings.packages ?? []) {
      const source = typeof pkg === "string" ? pkg : pkg.source;
      configuredPackages.push({
        source,
        scope: "project",
        filtered: typeof pkg === "object",
        installedPath: this.getInstalledPath(source, "project")
      });
    }
    return configuredPackages;
  }
  async install(source, options) {
    const parsed = this.parseSource(source);
    const scope = options?.local ? "project" : "user";
    this.assertProjectTrustedForScope(scope);
    await this.withProgress("install", source, `Installing ${source}...`, async () => {
      if (parsed.type === "npm") {
        await this.installNpm(parsed, scope, false);
        return;
      }
      if (parsed.type === "git") {
        await this.installGit(parsed, scope);
        return;
      }
      if (parsed.type === "local") {
        const resolved = this.resolvePath(parsed.path);
        if (!existsSync8(resolved)) {
          throw new Error(`Path does not exist: ${resolved}`);
        }
        return;
      }
      throw new Error(`Unsupported install source: ${source}`);
    });
  }
  async installAndPersist(source, options) {
    await this.install(source, options);
    this.addSourceToSettings(source, options);
  }
  async remove(source, options) {
    const parsed = this.parseSource(source);
    const scope = options?.local ? "project" : "user";
    this.assertProjectTrustedForScope(scope);
    await this.withProgress("remove", source, `Removing ${source}...`, async () => {
      if (parsed.type === "npm") {
        await this.uninstallNpm(parsed, scope);
        return;
      }
      if (parsed.type === "git") {
        await this.removeGit(parsed, scope);
        return;
      }
      if (parsed.type === "local") {
        return;
      }
      throw new Error(`Unsupported remove source: ${source}`);
    });
  }
  async removeAndPersist(source, options) {
    await this.remove(source, options);
    return this.removeSourceFromSettings(source, options);
  }
  async update(source) {
    const globalSettings = this.settingsManager.getGlobalSettings();
    const projectSettings = this.settingsManager.getProjectSettings();
    const identity = source ? this.getPackageIdentity(source) : void 0;
    let matched = false;
    const updateSources = [];
    for (const pkg of globalSettings.packages ?? []) {
      const sourceStr = typeof pkg === "string" ? pkg : pkg.source;
      if (identity && this.getPackageIdentity(sourceStr, "user") !== identity)
        continue;
      matched = true;
      updateSources.push({ source: sourceStr, scope: "user" });
    }
    for (const pkg of projectSettings.packages ?? []) {
      const sourceStr = typeof pkg === "string" ? pkg : pkg.source;
      if (identity && this.getPackageIdentity(sourceStr, "project") !== identity)
        continue;
      matched = true;
      updateSources.push({ source: sourceStr, scope: "project" });
    }
    if (source && !matched) {
      throw new Error(this.buildNoMatchingPackageMessage(source, [
        ...globalSettings.packages ?? [],
        ...projectSettings.packages ?? []
      ]));
    }
    await this.updateConfiguredSources(updateSources);
  }
  async updateConfiguredSources(sources) {
    if (isOfflineModeEnabled() || sources.length === 0) {
      return;
    }
    const npmCandidates = [];
    const gitCandidates = [];
    for (const entry of sources) {
      const parsed = this.parseSource(entry.source);
      if (parsed.type === "npm") {
        if (!parsed.pinned) {
          npmCandidates.push({ ...entry, parsed });
        }
      } else if (parsed.type === "git") {
        gitCandidates.push({ ...entry, parsed });
      }
    }
    const npmCheckTasks = npmCandidates.map((entry) => async () => ({
      entry,
      shouldUpdate: await this.shouldUpdateNpmSource(entry.parsed, entry.scope)
    }));
    const npmCheckResults = await this.runWithConcurrency(npmCheckTasks, UPDATE_CHECK_CONCURRENCY);
    const userNpmUpdates = [];
    const projectNpmUpdates = [];
    for (const result of npmCheckResults) {
      if (!result.shouldUpdate) {
        continue;
      }
      if (result.entry.scope === "user") {
        userNpmUpdates.push(result.entry);
      } else {
        projectNpmUpdates.push(result.entry);
      }
    }
    const tasks = [];
    if (userNpmUpdates.length > 0) {
      tasks.push(this.updateNpmBatch(userNpmUpdates, "user"));
    }
    if (projectNpmUpdates.length > 0) {
      tasks.push(this.updateNpmBatch(projectNpmUpdates, "project"));
    }
    if (gitCandidates.length > 0) {
      const gitTasks = gitCandidates.map((entry) => async () => this.withProgress("update", entry.source, `Updating ${entry.source}...`, async () => {
        await this.updateGit(entry.parsed, entry.scope);
      }));
      tasks.push(this.runWithConcurrency(gitTasks, GIT_UPDATE_CONCURRENCY).then(() => {
      }));
    }
    await Promise.all(tasks);
  }
  async shouldUpdateNpmSource(source, scope) {
    const installedPath = this.getManagedNpmInstallPath(source, scope);
    const installedVersion = existsSync8(installedPath) ? this.getInstalledNpmVersion(installedPath) : void 0;
    if (!installedVersion) {
      return true;
    }
    try {
      const targetVersion = await this.getLatestNpmVersion(source.version ? source.spec : source.name, source.range);
      return gt(targetVersion, installedVersion);
    } catch {
      return true;
    }
  }
  async updateNpmBatch(sources, scope) {
    if (sources.length === 0) {
      return;
    }
    const sourceLabel = sources.length === 1 ? sources[0].source : `${scope} npm packages`;
    const message = sources.length === 1 ? `Updating ${sources[0].source}...` : `Updating ${scope} npm packages...`;
    const specs = sources.map((entry) => entry.parsed.version ? entry.parsed.spec : `${entry.parsed.name}@latest`);
    await this.withProgress("update", sourceLabel, message, async () => {
      await this.installNpmBatch(specs, scope);
    });
  }
  async installNpmBatch(specs, scope) {
    const installRoot = this.getNpmInstallRoot(scope, false);
    this.ensureNpmProject(installRoot);
    await this.runNpmCommand(this.getNpmInstallArgs(specs, installRoot));
  }
  async checkForAvailableUpdates() {
    if (isOfflineModeEnabled()) {
      return [];
    }
    const globalSettings = this.settingsManager.getGlobalSettings();
    const projectSettings = this.settingsManager.getProjectSettings();
    const allPackages = [];
    for (const pkg of projectSettings.packages ?? []) {
      allPackages.push({ pkg, scope: "project" });
    }
    for (const pkg of globalSettings.packages ?? []) {
      allPackages.push({ pkg, scope: "user" });
    }
    const packageSources = this.dedupePackages(allPackages);
    const checks = packageSources.filter((entry) => entry.scope !== "temporary").map((entry) => async () => {
      const source = typeof entry.pkg === "string" ? entry.pkg : entry.pkg.source;
      const parsed = this.parseSource(source);
      if (parsed.type === "local" || parsed.pinned) {
        return void 0;
      }
      if (parsed.type === "npm") {
        const installedPath2 = this.getNpmInstallPath(parsed, entry.scope);
        if (!existsSync8(installedPath2)) {
          return void 0;
        }
        const hasUpdate2 = await this.npmHasAvailableUpdate(parsed, installedPath2);
        if (!hasUpdate2) {
          return void 0;
        }
        return {
          source,
          displayName: parsed.name,
          type: "npm",
          scope: entry.scope
        };
      }
      const installedPath = this.getGitInstallPath(parsed, entry.scope);
      if (!existsSync8(installedPath)) {
        return void 0;
      }
      const hasUpdate = await this.gitHasAvailableUpdate(installedPath);
      if (!hasUpdate) {
        return void 0;
      }
      return {
        source,
        displayName: `${parsed.host}/${parsed.path}`,
        type: "git",
        scope: entry.scope
      };
    });
    const results = await this.runWithConcurrency(checks, UPDATE_CHECK_CONCURRENCY);
    return results.filter((result) => result !== void 0);
  }
  async resolvePackageSources(sources, accumulator, onMissing) {
    for (const { pkg, scope } of sources) {
      const sourceStr = typeof pkg === "string" ? pkg : pkg.source;
      const filter = typeof pkg === "object" ? pkg : void 0;
      const deltaBase = this.findAutoloadDeltaBase(pkg, scope, sources);
      const resolvedSource = deltaBase?.source ?? sourceStr;
      const resolvedScope = deltaBase?.scope ?? scope;
      const parsed = this.parseSource(resolvedSource);
      const metadata = { source: sourceStr, scope, origin: "package" };
      if (parsed.type === "local") {
        const baseDir = this.getBaseDirForScope(resolvedScope);
        this.resolveLocalExtensionSource(parsed, accumulator, filter, metadata, baseDir);
        continue;
      }
      const installMissing = /* @__PURE__ */ __name(async () => {
        if (isOfflineModeEnabled())
          return false;
        if (!onMissing) {
          await this.installParsedSource(parsed, resolvedScope);
          return true;
        }
        const action = await onMissing(resolvedSource);
        if (action === "skip")
          return false;
        if (action === "error")
          throw new Error(`Missing source: ${resolvedSource}`);
        await this.installParsedSource(parsed, resolvedScope);
        return true;
      }, "installMissing");
      if (parsed.type === "npm") {
        let installedPath = this.getNpmInstallPath(parsed, resolvedScope);
        const needsInstall = !existsSync8(installedPath) || !await this.installedNpmMatchesConfiguredVersion(parsed, installedPath);
        if (needsInstall) {
          const installed = await installMissing();
          if (!installed)
            continue;
          installedPath = this.getNpmInstallPath(parsed, resolvedScope);
        }
        metadata.baseDir = installedPath;
        this.collectPackageResources(installedPath, accumulator, filter, metadata);
        continue;
      }
      if (parsed.type === "git") {
        const installedPath = this.getGitInstallPath(parsed, resolvedScope);
        if (!existsSync8(installedPath)) {
          const installed = await installMissing();
          if (!installed)
            continue;
        } else if (resolvedScope === "temporary" && !parsed.pinned && !isOfflineModeEnabled()) {
          await this.refreshTemporaryGitSource(parsed, resolvedSource);
        }
        metadata.baseDir = installedPath;
        this.collectPackageResources(installedPath, accumulator, filter, metadata);
      }
    }
  }
  findAutoloadDeltaBase(pkg, scope, sources) {
    if (scope !== "project" || typeof pkg !== "object" || pkg.autoload !== false)
      return void 0;
    const identity = this.getPackageIdentity(pkg.source, scope);
    const userEntry = sources.find((entry) => entry.scope === "user" && this.getPackageIdentity(this.getPackageSourceString(entry.pkg), "user") === identity);
    return userEntry ? { source: this.getPackageSourceString(userEntry.pkg), scope: "user" } : void 0;
  }
  resolveLocalExtensionSource(source, accumulator, filter, metadata, baseDir) {
    const resolved = this.resolvePathFromBase(source.path, baseDir);
    if (!existsSync8(resolved)) {
      return;
    }
    try {
      const stats = statSync5(resolved);
      if (stats.isFile()) {
        metadata.baseDir = dirname8(resolved);
        this.addResource(accumulator.extensions, resolved, metadata, true);
        return;
      }
      if (stats.isDirectory()) {
        metadata.baseDir = resolved;
        const resources = this.collectPackageResources(resolved, accumulator, filter, metadata);
        if (!resources) {
          this.addResource(accumulator.extensions, resolved, metadata, true);
        }
      }
    } catch {
      return;
    }
  }
  async installParsedSource(parsed, scope) {
    if (parsed.type === "npm") {
      await this.installNpm(parsed, scope, scope === "temporary");
      return;
    }
    if (parsed.type === "git") {
      await this.installGit(parsed, scope);
      return;
    }
  }
  getPackageSourceString(pkg) {
    return typeof pkg === "string" ? pkg : pkg.source;
  }
  getSourceMatchKeyForInput(source) {
    const parsed = this.parseSource(source);
    if (parsed.type === "npm") {
      return `npm:${parsed.name}`;
    }
    if (parsed.type === "git") {
      return `git:${parsed.host}/${parsed.path}`;
    }
    return `local:${this.resolvePath(parsed.path)}`;
  }
  getSourceMatchKeyForSettings(source, scope) {
    const parsed = this.parseSource(source);
    if (parsed.type === "npm") {
      return `npm:${parsed.name}`;
    }
    if (parsed.type === "git") {
      return `git:${parsed.host}/${parsed.path}`;
    }
    const baseDir = this.getBaseDirForScope(scope);
    return `local:${this.resolvePathFromBase(parsed.path, baseDir)}`;
  }
  buildNoMatchingPackageMessage(source, configuredPackages) {
    const suggestion = this.findSuggestedConfiguredSource(source, configuredPackages);
    if (!suggestion) {
      return `No matching package found for ${source}`;
    }
    return `No matching package found for ${source}. Did you mean ${suggestion}?`;
  }
  findSuggestedConfiguredSource(source, configuredPackages) {
    const trimmedSource = source.trim();
    const suggestions = /* @__PURE__ */ new Set();
    for (const pkg of configuredPackages) {
      const sourceStr = this.getPackageSourceString(pkg);
      const parsed = this.parseSource(sourceStr);
      if (parsed.type === "npm") {
        if (trimmedSource === parsed.name || trimmedSource === parsed.spec) {
          suggestions.add(sourceStr);
        }
        continue;
      }
      if (parsed.type === "git") {
        const shorthand = `${parsed.host}/${parsed.path}`;
        const shorthandWithRef = parsed.ref ? `${shorthand}@${parsed.ref}` : void 0;
        if (trimmedSource === shorthand || shorthandWithRef && trimmedSource === shorthandWithRef) {
          suggestions.add(sourceStr);
        }
      }
    }
    return suggestions.values().next().value;
  }
  packageSourcesMatch(existing, inputSource, scope) {
    const left = this.getSourceMatchKeyForSettings(this.getPackageSourceString(existing), scope);
    const right = this.getSourceMatchKeyForInput(inputSource);
    return left === right;
  }
  normalizePackageSourceForSettings(source, scope) {
    const parsed = this.parseSource(source);
    if (parsed.type !== "local") {
      return source;
    }
    const baseDir = this.getBaseDirForScope(scope);
    const resolved = this.resolvePath(parsed.path);
    const rel = relative2(baseDir, resolved);
    return rel || ".";
  }
  parseSource(source) {
    if (source.startsWith("npm:")) {
      const spec = source.slice("npm:".length).trim();
      const { name, version: version2 } = this.parseNpmSpec(spec);
      return {
        type: "npm",
        spec,
        name,
        version: version2,
        range: getNpmVersionRange(version2),
        pinned: isExactNpmVersion(version2)
      };
    }
    if (isLocalPath(source)) {
      return { type: "local", path: source };
    }
    const gitParsed = parseGitUrl(source);
    if (gitParsed) {
      return gitParsed;
    }
    return { type: "local", path: source };
  }
  async installedNpmMatchesConfiguredVersion(source, installedPath) {
    const installedVersion = this.getInstalledNpmVersion(installedPath);
    if (!installedVersion) {
      return false;
    }
    return source.range ? satisfies(installedVersion, source.range) : true;
  }
  async npmHasAvailableUpdate(source, installedPath) {
    if (isOfflineModeEnabled()) {
      return false;
    }
    const installedVersion = this.getInstalledNpmVersion(installedPath);
    if (!installedVersion) {
      return false;
    }
    try {
      const targetVersion = await this.getLatestNpmVersion(source.version ? source.spec : source.name, source.range);
      return gt(targetVersion, installedVersion);
    } catch {
      return false;
    }
  }
  getInstalledNpmVersion(installedPath) {
    const packageJsonPath = join11(installedPath, "package.json");
    if (!existsSync8(packageJsonPath))
      return void 0;
    try {
      const content = readFileSync7(packageJsonPath, "utf-8");
      const pkg = JSON.parse(stripBom(content));
      return pkg.version;
    } catch {
      return void 0;
    }
  }
  async getLatestNpmVersion(packageSpec, range) {
    const npmCommand = this.getNpmCommand();
    const stdout = await this.runCommandCapture(npmCommand.command, [...npmCommand.args, "view", packageSpec, "version", "--json"], { cwd: this.cwd, timeoutMs: NETWORK_TIMEOUT_MS });
    const raw = stdout.trim();
    if (!raw)
      throw new Error("Empty response from npm view");
    const parsed = JSON.parse(raw);
    if (typeof parsed === "string") {
      return parsed;
    }
    if (Array.isArray(parsed)) {
      const versions = parsed.filter((value) => typeof value === "string" && value.length > 0);
      const latest = range ? maxSatisfying(versions, range) : [...versions].sort(rcompare)[0];
      if (latest)
        return latest;
    }
    throw new Error("Unexpected response from npm view");
  }
  async gitHasAvailableUpdate(installedPath) {
    if (isOfflineModeEnabled()) {
      return false;
    }
    try {
      const localHead = await this.runCommandCapture("git", ["rev-parse", "HEAD"], {
        cwd: installedPath,
        timeoutMs: NETWORK_TIMEOUT_MS
      });
      const remoteHead = await this.getRemoteGitHead(installedPath);
      return localHead.trim() !== remoteHead.trim();
    } catch {
      return false;
    }
  }
  async getRemoteGitHead(installedPath) {
    const upstreamRef = await this.getGitUpstreamRef(installedPath);
    if (upstreamRef) {
      const remoteHead2 = await this.runGitRemoteCommand(installedPath, ["ls-remote", "origin", upstreamRef]);
      const match2 = remoteHead2.match(/^([0-9a-f]{40})\s+/m);
      if (match2?.[1]) {
        return match2[1];
      }
    }
    const remoteHead = await this.runGitRemoteCommand(installedPath, ["ls-remote", "origin", "HEAD"]);
    const match = remoteHead.match(/^([0-9a-f]{40})\s+HEAD$/m);
    if (!match?.[1]) {
      throw new Error("Failed to determine remote HEAD");
    }
    return match[1];
  }
  async getLocalGitUpdateTarget(installedPath) {
    try {
      const upstream = await this.runCommandCapture("git", ["rev-parse", "--abbrev-ref", "@{upstream}"], {
        cwd: installedPath,
        timeoutMs: NETWORK_TIMEOUT_MS
      });
      const trimmedUpstream = upstream.trim();
      if (!trimmedUpstream.startsWith("origin/")) {
        throw new Error(`Unsupported upstream remote: ${trimmedUpstream}`);
      }
      const branch = trimmedUpstream.slice("origin/".length);
      if (!branch) {
        throw new Error("Missing upstream branch name");
      }
      const head = await this.runCommandCapture("git", ["rev-parse", "@{upstream}"], {
        cwd: installedPath,
        timeoutMs: NETWORK_TIMEOUT_MS
      });
      return {
        ref: "@{upstream}",
        head,
        fetchArgs: [
          "fetch",
          "--prune",
          "--no-tags",
          "origin",
          `+refs/heads/${branch}:refs/remotes/origin/${branch}`
        ]
      };
    } catch {
      await this.runCommand("git", ["remote", "set-head", "origin", "-a"], { cwd: installedPath }).catch(() => {
      });
      const head = await this.runCommandCapture("git", ["rev-parse", "origin/HEAD"], {
        cwd: installedPath,
        timeoutMs: NETWORK_TIMEOUT_MS
      });
      const originHeadRef = await this.runCommandCapture("git", ["symbolic-ref", "refs/remotes/origin/HEAD"], {
        cwd: installedPath,
        timeoutMs: NETWORK_TIMEOUT_MS
      }).catch(() => "");
      const branch = originHeadRef.trim().replace(/^refs\/remotes\/origin\//, "");
      if (branch) {
        return {
          ref: "origin/HEAD",
          head,
          fetchArgs: [
            "fetch",
            "--prune",
            "--no-tags",
            "origin",
            `+refs/heads/${branch}:refs/remotes/origin/${branch}`
          ]
        };
      }
      return {
        ref: "origin/HEAD",
        head,
        fetchArgs: ["fetch", "--prune", "--no-tags", "origin", "+HEAD:refs/remotes/origin/HEAD"]
      };
    }
  }
  async getGitUpstreamRef(installedPath) {
    try {
      const upstream = await this.runCommandCapture("git", ["rev-parse", "--abbrev-ref", "@{upstream}"], {
        cwd: installedPath,
        timeoutMs: NETWORK_TIMEOUT_MS
      });
      const trimmed = upstream.trim();
      if (!trimmed.startsWith("origin/")) {
        return void 0;
      }
      const branch = trimmed.slice("origin/".length);
      return branch ? `refs/heads/${branch}` : void 0;
    } catch {
      return void 0;
    }
  }
  runGitRemoteCommand(installedPath, args) {
    return this.runCommandCapture("git", args, {
      cwd: installedPath,
      timeoutMs: NETWORK_TIMEOUT_MS,
      env: {
        GIT_TERMINAL_PROMPT: "0"
      }
    });
  }
  async runWithConcurrency(tasks, limit) {
    if (tasks.length === 0) {
      return [];
    }
    const results = new Array(tasks.length);
    let nextIndex = 0;
    const workerCount = Math.max(1, Math.min(limit, tasks.length));
    const worker = /* @__PURE__ */ __name(async () => {
      while (true) {
        const index = nextIndex;
        nextIndex += 1;
        if (index >= tasks.length) {
          return;
        }
        results[index] = await tasks[index]();
      }
    }, "worker");
    await Promise.all(Array.from({ length: workerCount }, () => worker()));
    return results;
  }
  /**
   * Get a unique identity for a package, ignoring version/ref.
   * Used to detect when the same package is in both global and project settings.
   * For git packages, uses normalized host/path to ensure SSH and HTTPS URLs
   * for the same repository are treated as identical.
   */
  getPackageIdentity(source, scope) {
    const parsed = this.parseSource(source);
    if (parsed.type === "npm") {
      return `npm:${parsed.name}`;
    }
    if (parsed.type === "git") {
      return `git:${parsed.host}/${parsed.path}`;
    }
    if (scope) {
      const baseDir = this.getBaseDirForScope(scope);
      return `local:${this.resolvePathFromBase(parsed.path, baseDir)}`;
    }
    return `local:${this.resolvePath(parsed.path)}`;
  }
  /**
   * Dedupe packages: if same package identity appears in both global and project,
   * keep only the project one (project wins). A project entry with autoload=false
   * is a delta over the global entry, so both are kept (delta first).
   */
  dedupePackages(packages) {
    const result = [];
    const seen = /* @__PURE__ */ new Map();
    for (const entry of packages) {
      const identity = this.getPackageIdentity(this.getPackageSourceString(entry.pkg), entry.scope);
      const index = seen.get(identity);
      if (index === void 0) {
        seen.set(identity, result.length);
        result.push(entry);
        continue;
      }
      const existing = result[index];
      if (existing?.scope === "project" && entry.scope === "user") {
        if (typeof existing.pkg === "object" && existing.pkg.autoload === false)
          result.push(entry);
      } else if (entry.scope === "project") {
        result[index] = entry;
      }
    }
    return result;
  }
  parseNpmSpec(spec) {
    const match = spec.match(/^(@?[^@]+(?:\/[^@]+)?)(?:@(.+))?$/);
    if (!match) {
      return { name: spec };
    }
    const name = match[1] ?? spec;
    const version2 = match[2];
    return { name, version: version2 };
  }
  assertProjectTrustedForScope(scope) {
    if (scope === "project" && !this.settingsManager.isProjectTrusted()) {
      throw new Error("Project is not trusted; refusing to access project package storage");
    }
  }
  getNpmCommand() {
    const configuredCommand = this.settingsManager.getNpmCommand();
    if (!configuredCommand || configuredCommand.length === 0) {
      return { command: "npm", args: [] };
    }
    const [command, ...args] = configuredCommand;
    if (!command) {
      throw new Error("Invalid npmCommand: first array entry must be a non-empty command");
    }
    return { command, args };
  }
  getPackageManagerName() {
    const npmCommand = this.getNpmCommand();
    const commandParts = [npmCommand.command, ...npmCommand.args];
    const separatorIndex = commandParts.lastIndexOf("--");
    const packageManagerCommand = separatorIndex >= 0 ? commandParts[separatorIndex + 1] : npmCommand.command;
    return packageManagerCommand ? basename6(packageManagerCommand).replace(/\.(cmd|exe)$/i, "") : "";
  }
  async runNpmCommand(args, options) {
    const npmCommand = this.getNpmCommand();
    await this.runCommand(npmCommand.command, [...npmCommand.args, ...args], options);
  }
  getGitDependencyInstallArgs() {
    const configuredCommand = this.settingsManager.getNpmCommand();
    if (configuredCommand && configuredCommand.length > 0) {
      return ["install"];
    }
    return ["install", "--omit=dev"];
  }
  runNpmCommandSync(args) {
    const npmCommand = this.getNpmCommand();
    return this.runCommandSync(npmCommand.command, [...npmCommand.args, ...args]);
  }
  getNpmInstallArgs(specs, installRoot) {
    const packageManagerName = this.getPackageManagerName();
    if (packageManagerName === "bun") {
      return ["install", ...specs, "--cwd", installRoot, "--omit=peer"];
    }
    if (packageManagerName === "pnpm") {
      return [
        "install",
        ...specs,
        "--prefix",
        installRoot,
        "--config.auto-install-peers=false",
        "--config.strict-peer-dependencies=false",
        "--config.strict-dep-builds=false"
      ];
    }
    return ["install", ...specs, "--prefix", installRoot, "--legacy-peer-deps"];
  }
  async installNpm(source, scope, temporary) {
    const installRoot = this.getNpmInstallRoot(scope, temporary);
    this.ensureNpmProject(installRoot);
    await this.runNpmCommand(this.getNpmInstallArgs([source.spec], installRoot));
  }
  async uninstallNpm(source, scope) {
    const installRoot = this.getNpmInstallRoot(scope, false);
    if (!existsSync8(installRoot)) {
      return;
    }
    const packageManagerName = this.getPackageManagerName();
    if (packageManagerName === "bun") {
      await this.runNpmCommand(["uninstall", source.name, "--cwd", installRoot]);
      return;
    }
    const args = ["uninstall", source.name, "--prefix", installRoot];
    if (packageManagerName !== "pnpm") {
      args.push("--legacy-peer-deps");
    }
    await this.runNpmCommand(args);
  }
  async installGit(source, scope) {
    const targetDir = this.getGitInstallPath(source, scope);
    if (existsSync8(targetDir)) {
      if (source.ref) {
        await this.ensureGitRef(targetDir, ["fetch", "origin", source.ref], "FETCH_HEAD");
        return;
      }
      const target = await this.getLocalGitUpdateTarget(targetDir);
      await this.ensureGitRef(targetDir, target.fetchArgs, target.ref);
      return;
    }
    const gitRoot = this.getGitInstallRoot(scope);
    if (gitRoot) {
      this.ensureGitIgnore(gitRoot);
    }
    mkdirSync4(dirname8(targetDir), { recursive: true });
    rmSync(this.getGitUpdateMarkerPath(targetDir), { force: true });
    try {
      await this.runCommand("git", ["clone", source.repo, targetDir]);
      if (source.ref) {
        await this.runCommand("git", ["checkout", source.ref], { cwd: targetDir });
      }
      const packageJsonPath = join11(targetDir, "package.json");
      if (existsSync8(packageJsonPath)) {
        await this.runNpmCommand(this.getGitDependencyInstallArgs(), { cwd: targetDir });
      }
    } catch (error) {
      rmSync(targetDir, { recursive: true, force: true });
      this.pruneEmptyGitParents(targetDir, gitRoot);
      throw error;
    }
  }
  async updateGit(source, scope) {
    const targetDir = this.getGitInstallPath(source, scope);
    if (!existsSync8(targetDir)) {
      await this.installGit(source, scope);
      return;
    }
    if (source.ref) {
      await this.ensureGitRef(targetDir, ["fetch", "origin", source.ref], "FETCH_HEAD");
      return;
    }
    const target = await this.getLocalGitUpdateTarget(targetDir);
    await this.ensureGitRef(targetDir, target.fetchArgs, target.ref);
  }
  hasMissingGitDependencies(targetDir) {
    const packageJsonPath = join11(targetDir, "package.json");
    if (!existsSync8(packageJsonPath))
      return false;
    try {
      const manifest = JSON.parse(stripBom(readFileSync7(packageJsonPath, "utf-8")));
      if (!manifest.dependencies || typeof manifest.dependencies !== "object" || Array.isArray(manifest.dependencies)) {
        return false;
      }
      const nodeModulesDir = resolve5(targetDir, "node_modules");
      return Object.keys(manifest.dependencies).some((name) => {
        const dependencyPath = resolve5(nodeModulesDir, name);
        if (!dependencyPath.startsWith(`${nodeModulesDir}${sep3}`))
          return false;
        return !existsSync8(dependencyPath);
      });
    } catch {
      return false;
    }
  }
  async repairMissingGitDependencies(targetDir) {
    if (!this.hasMissingGitDependencies(targetDir))
      return;
    await this.runNpmCommand(this.getGitDependencyInstallArgs(), { cwd: targetDir });
  }
  getGitUpdateMarkerPath(targetDir) {
    return join11(dirname8(targetDir), `.${basename6(targetDir)}.pi-update-incomplete`);
  }
  async cleanAndInstallGitDependencies(targetDir, markerPath) {
    try {
      await this.runCommand("git", ["clean", "-fdx"], { cwd: targetDir });
    } catch (error) {
      await this.repairMissingGitDependencies(targetDir).catch(() => {
      });
      throw error;
    }
    const packageJsonPath = join11(targetDir, "package.json");
    if (existsSync8(packageJsonPath)) {
      await this.runNpmCommand(this.getGitDependencyInstallArgs(), { cwd: targetDir });
    }
    rmSync(markerPath, { force: true });
  }
  async ensureGitRef(targetDir, fetchArgs, ref) {
    await this.runCommand("git", fetchArgs, { cwd: targetDir });
    const localHead = await this.runCommandCapture("git", ["rev-parse", "HEAD"], {
      cwd: targetDir,
      timeoutMs: NETWORK_TIMEOUT_MS
    });
    const commitRef = `${ref}^{commit}`;
    const targetHead = await this.runCommandCapture("git", ["rev-parse", commitRef], {
      cwd: targetDir,
      timeoutMs: NETWORK_TIMEOUT_MS
    });
    const markerPath = this.getGitUpdateMarkerPath(targetDir);
    if (localHead.trim() === targetHead.trim()) {
      if (existsSync8(markerPath)) {
        await this.cleanAndInstallGitDependencies(targetDir, markerPath);
      } else {
        await this.repairMissingGitDependencies(targetDir);
      }
      return;
    }
    writeFileSync5(markerPath, "", "utf-8");
    await this.runCommand("git", ["reset", "--hard", commitRef], { cwd: targetDir });
    await this.cleanAndInstallGitDependencies(targetDir, markerPath);
  }
  async refreshTemporaryGitSource(source, sourceStr) {
    if (isOfflineModeEnabled()) {
      return;
    }
    try {
      await this.withProgress("pull", sourceStr, `Refreshing ${sourceStr}...`, async () => {
        await this.updateGit(source, "temporary");
      });
    } catch {
    }
  }
  async removeGit(source, scope) {
    const targetDir = this.getGitInstallPath(source, scope);
    rmSync(targetDir, { recursive: true, force: true });
    rmSync(this.getGitUpdateMarkerPath(targetDir), { force: true });
    this.pruneEmptyGitParents(targetDir, this.getGitInstallRoot(scope));
  }
  pruneEmptyGitParents(targetDir, installRoot) {
    if (!installRoot)
      return;
    const resolvedRoot = resolve5(installRoot);
    let current = dirname8(targetDir);
    while (current.startsWith(resolvedRoot) && current !== resolvedRoot) {
      if (!existsSync8(current)) {
        current = dirname8(current);
        continue;
      }
      const entries = readdirSync5(current);
      if (entries.length > 0) {
        break;
      }
      try {
        rmSync(current, { recursive: true, force: true });
      } catch {
        break;
      }
      current = dirname8(current);
    }
  }
  ensureNpmProject(installRoot) {
    if (!existsSync8(installRoot)) {
      mkdirSync4(installRoot, { recursive: true });
    }
    markPathIgnoredByCloudSync(installRoot);
    this.ensureGitIgnore(installRoot);
    const packageJsonPath = join11(installRoot, "package.json");
    if (!existsSync8(packageJsonPath)) {
      const pkgJson = { name: "pi-extensions", private: true };
      writeFileSync5(packageJsonPath, JSON.stringify(pkgJson, null, 2), "utf-8");
    }
  }
  ensureGitIgnore(dir) {
    if (!existsSync8(dir)) {
      mkdirSync4(dir, { recursive: true });
    }
    const ignorePath = join11(dir, ".gitignore");
    if (!existsSync8(ignorePath)) {
      writeFileSync5(ignorePath, "*\n!.gitignore\n", "utf-8");
    }
  }
  getNpmInstallRoot(scope, temporary) {
    if (temporary) {
      return this.getTemporaryDir("npm");
    }
    if (scope === "project") {
      this.assertProjectTrustedForScope(scope);
      return join11(this.cwd, CONFIG_DIR_NAME, "npm");
    }
    return join11(this.agentDir, "npm");
  }
  getGlobalNpmRoot() {
    const npmCommand = this.getNpmCommand();
    const commandKey = [npmCommand.command, ...npmCommand.args].join("\0");
    if (this.globalNpmRoot && this.globalNpmRootCommandKey === commandKey) {
      return this.globalNpmRoot;
    }
    if (this.getPackageManagerName() === "bun") {
      const binDir = this.runNpmCommandSync(["pm", "bin", "-g"]).trim();
      this.globalNpmRoot = join11(dirname8(binDir), "install", "global", "node_modules");
    } else {
      this.globalNpmRoot = this.runNpmCommandSync(["root", "-g"]).trim();
    }
    this.globalNpmRootCommandKey = commandKey;
    return this.globalNpmRoot;
  }
  getPnpmGlobalPackagePath(packageName) {
    if (this.getPackageManagerName() !== "pnpm") {
      return void 0;
    }
    const output = this.runNpmCommandSync(["list", "-g", "--depth", "0", "--json"]);
    const entries = JSON.parse(output);
    for (const entry of entries) {
      const path2 = entry.dependencies?.[packageName]?.path;
      if (path2)
        return path2;
    }
    return void 0;
  }
  getManagedNpmInstallPath(source, scope) {
    if (scope === "temporary") {
      return join11(this.getTemporaryDir("npm"), "node_modules", source.name);
    }
    if (scope === "project") {
      this.assertProjectTrustedForScope(scope);
      return join11(this.cwd, CONFIG_DIR_NAME, "npm", "node_modules", source.name);
    }
    return join11(this.agentDir, "npm", "node_modules", source.name);
  }
  getLegacyGlobalNpmInstallPath(source) {
    try {
      return this.getPnpmGlobalPackagePath(source.name) ?? join11(this.getGlobalNpmRoot(), source.name);
    } catch {
      return void 0;
    }
  }
  getNpmInstallPath(source, scope) {
    const managedPath = this.getManagedNpmInstallPath(source, scope);
    if (scope !== "user" || existsSync8(managedPath)) {
      return managedPath;
    }
    const legacyPath = this.getLegacyGlobalNpmInstallPath(source);
    return legacyPath && existsSync8(legacyPath) ? legacyPath : managedPath;
  }
  getGitInstallPath(source, scope) {
    if (scope === "temporary") {
      return this.getTemporaryDir(`git-${source.host}`, source.path);
    }
    const installRoot = this.getGitInstallRoot(scope);
    if (!installRoot) {
      throw new Error("Missing git install root");
    }
    return this.resolveManagedPath(installRoot, source.host, source.path);
  }
  getGitInstallRoot(scope) {
    if (scope === "temporary") {
      return void 0;
    }
    if (scope === "project") {
      this.assertProjectTrustedForScope(scope);
      return join11(this.cwd, CONFIG_DIR_NAME, "git");
    }
    return join11(this.agentDir, "git");
  }
  getTemporaryDir(prefix, suffix) {
    const root = this.resolveManagedPath(getExtensionTempFolder(this.agentDir), prefix);
    const hash = createHash("sha256").update(`${prefix}-${suffix ?? ""}`).digest("hex").slice(0, 8);
    return this.resolveManagedPath(root, hash, suffix ?? "");
  }
  resolveManagedPath(root, ...parts) {
    const resolvedRoot = resolve5(root);
    const resolvedPath = resolve5(resolvedRoot, ...parts);
    if (resolvedPath !== resolvedRoot && !resolvedPath.startsWith(`${resolvedRoot}${sep3}`)) {
      throw new Error(`Refusing to use path outside package install root: ${resolvedPath}`);
    }
    return resolvedPath;
  }
  getBaseDirForScope(scope) {
    if (scope === "project") {
      this.assertProjectTrustedForScope(scope);
      return join11(this.cwd, CONFIG_DIR_NAME);
    }
    if (scope === "user") {
      return this.agentDir;
    }
    return this.cwd;
  }
  resolvePath(input) {
    return resolvePath(input, this.cwd, { homeDir: getHomeDir(), trim: true });
  }
  resolvePathFromBase(input, baseDir) {
    return resolvePath(input, baseDir, { homeDir: getHomeDir(), trim: true });
  }
  collectPackageResources(packageRoot, accumulator, filter, metadata) {
    if (filter) {
      for (const resourceType of RESOURCE_TYPES) {
        const patterns = filter[resourceType];
        const target = this.getTargetMap(accumulator, resourceType);
        if (filter.autoload === false) {
          this.applyPackageDeltaFilter(packageRoot, patterns ?? [], resourceType, target, metadata);
        } else if (patterns !== void 0) {
          this.applyPackageFilter(packageRoot, patterns, resourceType, target, metadata);
        } else {
          this.collectDefaultResources(packageRoot, resourceType, target, metadata);
        }
      }
      return true;
    }
    const manifest = readPiManifest(join11(packageRoot, "package.json"));
    if (manifest) {
      for (const resourceType of RESOURCE_TYPES) {
        const entries = manifest[resourceType];
        this.addManifestEntries(entries, packageRoot, resourceType, this.getTargetMap(accumulator, resourceType), metadata);
      }
      return true;
    }
    let hasAnyDir = false;
    for (const resourceType of RESOURCE_TYPES) {
      const dir = join11(packageRoot, resourceType);
      if (existsSync8(dir)) {
        const files = collectResourceFiles(dir, resourceType);
        for (const f of files) {
          this.addResource(this.getTargetMap(accumulator, resourceType), f, metadata, true);
        }
        hasAnyDir = true;
      }
    }
    return hasAnyDir;
  }
  collectDefaultResources(packageRoot, resourceType, target, metadata) {
    const manifest = readPiManifest(join11(packageRoot, "package.json"));
    const entries = manifest?.[resourceType];
    if (entries) {
      this.addManifestEntries(entries, packageRoot, resourceType, target, metadata);
      return;
    }
    const dir = join11(packageRoot, resourceType);
    if (existsSync8(dir)) {
      const files = collectResourceFiles(dir, resourceType);
      for (const f of files) {
        this.addResource(target, f, metadata, true);
      }
    }
  }
  applyPackageFilter(packageRoot, userPatterns, resourceType, target, metadata) {
    const { allFiles } = this.collectManifestFiles(packageRoot, resourceType);
    if (userPatterns.length === 0) {
      for (const f of allFiles) {
        this.addResource(target, f, metadata, false);
      }
      return;
    }
    const enabledByUser = applyPatterns(allFiles, userPatterns, packageRoot);
    for (const f of allFiles) {
      const enabled = enabledByUser.has(f);
      this.addResource(target, f, metadata, enabled);
    }
  }
  applyPackageDeltaFilter(packageRoot, userPatterns, resourceType, target, metadata) {
    if (userPatterns.length === 0) {
      return;
    }
    const { allFiles } = this.collectManifestFiles(packageRoot, resourceType);
    const enabledByUser = applyAutoloadDisabledPatterns(allFiles, userPatterns, packageRoot);
    for (const [filePath, enabled] of enabledByUser) {
      this.addResource(target, filePath, metadata, enabled);
    }
  }
  /**
   * Collect all files from a package for a resource type, applying manifest patterns.
   * Returns { allFiles, enabledByManifest } where enabledByManifest is the set of files
   * that pass the manifest's own patterns.
   */
  collectManifestFiles(packageRoot, resourceType) {
    const manifest = readPiManifest(join11(packageRoot, "package.json"));
    const entries = manifest?.[resourceType];
    if (entries && entries.length > 0) {
      const allFiles2 = this.collectFilesFromManifestEntries(entries, packageRoot, resourceType);
      const manifestPatterns = entries.filter(isOverridePattern);
      const enabledByManifest = manifestPatterns.length > 0 ? applyPatterns(allFiles2, manifestPatterns, packageRoot) : new Set(allFiles2);
      return { allFiles: Array.from(enabledByManifest), enabledByManifest };
    }
    const conventionDir = join11(packageRoot, resourceType);
    if (!existsSync8(conventionDir)) {
      return { allFiles: [], enabledByManifest: /* @__PURE__ */ new Set() };
    }
    const allFiles = collectResourceFiles(conventionDir, resourceType);
    return { allFiles, enabledByManifest: new Set(allFiles) };
  }
  addManifestEntries(entries, root, resourceType, target, metadata) {
    if (!entries)
      return;
    const allFiles = this.collectFilesFromManifestEntries(entries, root, resourceType);
    const patterns = entries.filter(isOverridePattern);
    const enabledPaths = applyPatterns(allFiles, patterns, root);
    for (const f of allFiles) {
      if (enabledPaths.has(f)) {
        this.addResource(target, f, metadata, true);
      }
    }
  }
  collectFilesFromManifestEntries(entries, root, resourceType) {
    const sourceEntries = entries.filter((entry) => !isOverridePattern(entry));
    const resolved = sourceEntries.flatMap((entry) => {
      if (!hasGlobPattern(entry)) {
        return [resolve5(root, entry)];
      }
      return expandPackageGlob(entry, root);
    });
    return this.collectFilesFromPaths(resolved, resourceType);
  }
  resolveLocalEntries(entries, resourceType, target, metadata, baseDir) {
    if (entries.length === 0)
      return;
    const { plain, patterns } = splitPatterns(entries);
    const resolvedPlain = plain.map((p) => this.resolvePathFromBase(p, baseDir));
    const allFiles = this.collectFilesFromPaths(resolvedPlain, resourceType);
    const enabledPaths = applyPatterns(allFiles, patterns, baseDir);
    for (const f of allFiles) {
      this.addResource(target, f, metadata, enabledPaths.has(f));
    }
  }
  addAutoDiscoveredResources(accumulator, globalSettings, projectSettings, globalBaseDir, projectBaseDir) {
    const userMetadata = {
      source: "auto",
      scope: "user",
      origin: "top-level",
      baseDir: globalBaseDir
    };
    const projectMetadata = {
      source: "auto",
      scope: "project",
      origin: "top-level",
      baseDir: projectBaseDir
    };
    const userOverrides = {
      extensions: globalSettings.extensions ?? [],
      skills: globalSettings.skills ?? [],
      prompts: globalSettings.prompts ?? [],
      themes: globalSettings.themes ?? []
    };
    const projectOverrides = {
      extensions: projectSettings.extensions ?? [],
      skills: projectSettings.skills ?? [],
      prompts: projectSettings.prompts ?? [],
      themes: projectSettings.themes ?? []
    };
    const userDirs = {
      extensions: join11(globalBaseDir, "extensions"),
      skills: join11(globalBaseDir, "skills"),
      prompts: join11(globalBaseDir, "prompts"),
      themes: join11(globalBaseDir, "themes")
    };
    const projectDirs = {
      extensions: join11(projectBaseDir, "extensions"),
      skills: join11(projectBaseDir, "skills"),
      prompts: join11(projectBaseDir, "prompts"),
      themes: join11(projectBaseDir, "themes")
    };
    const userAgentsSkillsDir = join11(getHomeDir(), ".agents", "skills");
    const projectTrusted = this.settingsManager.isProjectTrusted();
    const projectAgentsSkillDirs = projectTrusted ? collectAncestorAgentsSkillDirs(this.cwd).filter((dir) => resolve5(dir) !== resolve5(userAgentsSkillsDir)) : [];
    const addResources = /* @__PURE__ */ __name((resourceType, paths, metadata, overrides, baseDir) => {
      const target = this.getTargetMap(accumulator, resourceType);
      for (const path2 of paths) {
        const enabled = isEnabledByOverrides(path2, overrides, baseDir);
        this.addResource(target, path2, metadata, enabled);
      }
    }, "addResources");
    if (projectTrusted) {
      addResources("extensions", collectAutoExtensionEntries(projectDirs.extensions), projectMetadata, projectOverrides.extensions, projectBaseDir);
      addResources("skills", collectAutoSkillEntries(projectDirs.skills, "pi"), projectMetadata, projectOverrides.skills, projectBaseDir);
    }
    for (const agentsSkillsDir of projectAgentsSkillDirs) {
      const agentsBaseDir = dirname8(agentsSkillsDir);
      const agentsMetadata = {
        ...projectMetadata,
        baseDir: agentsBaseDir
      };
      addResources("skills", collectAutoSkillEntries(agentsSkillsDir, "agents"), agentsMetadata, projectOverrides.skills, agentsBaseDir);
    }
    if (projectTrusted) {
      addResources("prompts", collectAutoPromptEntries(projectDirs.prompts), projectMetadata, projectOverrides.prompts, projectBaseDir);
      addResources("themes", collectAutoThemeEntries(projectDirs.themes), projectMetadata, projectOverrides.themes, projectBaseDir);
    }
    addResources("extensions", collectAutoExtensionEntries(userDirs.extensions), userMetadata, userOverrides.extensions, globalBaseDir);
    addResources("skills", collectAutoSkillEntries(userDirs.skills, "pi"), userMetadata, userOverrides.skills, globalBaseDir);
    const userAgentsBaseDir = dirname8(userAgentsSkillsDir);
    const userAgentsMetadata = {
      ...userMetadata,
      baseDir: userAgentsBaseDir
    };
    addResources("skills", collectAutoSkillEntries(userAgentsSkillsDir, "agents"), userAgentsMetadata, userOverrides.skills, userAgentsBaseDir);
    addResources("prompts", collectAutoPromptEntries(userDirs.prompts), userMetadata, userOverrides.prompts, globalBaseDir);
    addResources("themes", collectAutoThemeEntries(userDirs.themes), userMetadata, userOverrides.themes, globalBaseDir);
  }
  collectFilesFromPaths(paths, resourceType) {
    const files = [];
    for (const p of paths) {
      if (!existsSync8(p))
        continue;
      try {
        const stats = statSync5(p);
        if (stats.isFile()) {
          files.push(p);
        } else if (stats.isDirectory()) {
          files.push(...collectResourceFiles(p, resourceType));
        }
      } catch {
      }
    }
    return files;
  }
  getTargetMap(accumulator, resourceType) {
    switch (resourceType) {
      case "extensions":
        return accumulator.extensions;
      case "skills":
        return accumulator.skills;
      case "prompts":
        return accumulator.prompts;
      case "themes":
        return accumulator.themes;
      default:
        throw new Error(`Unknown resource type: ${resourceType}`);
    }
  }
  addResource(map, path2, metadata, enabled) {
    if (!path2)
      return;
    if (!map.has(path2)) {
      map.set(path2, { metadata, enabled });
    }
  }
  createAccumulator() {
    return {
      extensions: /* @__PURE__ */ new Map(),
      skills: /* @__PURE__ */ new Map(),
      prompts: /* @__PURE__ */ new Map(),
      themes: /* @__PURE__ */ new Map()
    };
  }
  toResolvedPaths(accumulator) {
    const mapToResolved = /* @__PURE__ */ __name((entries) => {
      const resolved = Array.from(entries.entries()).map(([path2, { metadata, enabled }]) => ({
        path: path2,
        enabled,
        metadata
      }));
      resolved.sort((a, b) => resourcePrecedenceRank(a.metadata) - resourcePrecedenceRank(b.metadata));
      const seen = /* @__PURE__ */ new Set();
      return resolved.filter((entry) => {
        const canonicalPath = canonicalizePath(entry.path);
        if (seen.has(canonicalPath))
          return false;
        seen.add(canonicalPath);
        return true;
      });
    }, "mapToResolved");
    return {
      extensions: mapToResolved(accumulator.extensions),
      skills: mapToResolved(accumulator.skills),
      prompts: mapToResolved(accumulator.prompts),
      themes: mapToResolved(accumulator.themes)
    };
  }
  spawnCommand(command, args, options) {
    const env = getEnv();
    return spawnProcess(command, args, {
      cwd: options?.cwd,
      stdio: isStdoutTakenOver() ? ["ignore", 2, 2] : "inherit",
      env
    });
  }
  spawnCaptureCommand(command, args, options) {
    const baseEnv = getEnv();
    const env = options?.env ? { ...baseEnv, ...options.env } : baseEnv;
    return spawnProcess(command, args, {
      cwd: options?.cwd,
      stdio: ["ignore", "pipe", "pipe"],
      env
    });
  }
  runCommandCapture(command, args, options) {
    return new Promise((resolvePromise, reject) => {
      const child = this.spawnCaptureCommand(command, args, options);
      let stdout = "";
      let stderr = "";
      let timedOut = false;
      const timeout = typeof options?.timeoutMs === "number" ? setTimeout(() => {
        timedOut = true;
        child.kill();
      }, options.timeoutMs) : void 0;
      child.stdout?.on("data", (data) => {
        stdout += data.toString();
      });
      child.stderr?.on("data", (data) => {
        stderr += data.toString();
      });
      child.once("error", (error) => {
        if (timeout)
          clearTimeout(timeout);
        reject(error);
      });
      child.once("close", (code, signal) => {
        if (timeout)
          clearTimeout(timeout);
        if (timedOut) {
          reject(new Error(`${command} ${args.join(" ")} timed out after ${options?.timeoutMs}ms`));
          return;
        }
        if (code === 0) {
          resolvePromise(stdout.trim());
          return;
        }
        const exitStatus = code === null ? `signal ${signal ?? "unknown"}` : `code ${code}`;
        reject(new Error(`${command} ${args.join(" ")} failed with ${exitStatus}: ${stderr || stdout}`));
      });
    });
  }
  runCommand(command, args, options) {
    return new Promise((resolvePromise, reject) => {
      const child = this.spawnCommand(command, args, options);
      child.on("error", reject);
      child.on("exit", (code) => {
        if (code === 0) {
          resolvePromise();
        } else {
          reject(new Error(`${command} ${args.join(" ")} failed with code ${code}`));
        }
      });
    });
  }
  runCommandSync(command, args) {
    const env = getEnv();
    const result = spawnProcessSync(command, args, {
      stdio: ["ignore", "pipe", "pipe"],
      encoding: "utf-8",
      env
    });
    if (result.error || result.status !== 0) {
      throw new Error(`Failed to run ${command} ${args.join(" ")}: ${result.error?.message || result.stderr || result.stdout}`);
    }
    return (result.stdout || result.stderr || "").trim();
  }
};

// pi-dist/pi-coding-agent/core/settings-manager.js
import { DEFAULT_MAX_AGENT_RETRY_DELAY_MS } from "../../pi-ai/sdk-bundle/index.js";
import { randomUUID as randomUUID2 } from "crypto";
import { existsSync as existsSync9, mkdirSync as mkdirSync5, readFileSync as readFileSync8, writeFileSync as writeFileSync6 } from "fs";
import { dirname as dirname9, join as join12 } from "path";
import lockfile2 from "../../../proper-lockfile.mjs";

// pi-dist/pi-coding-agent/core/http-dispatcher.js
import { EventEmitter as EventEmitter2 } from "node:events";
import * as undici from "../../../undici/index.js";
var DEFAULT_HTTP_IDLE_TIMEOUT_MS = 3e5;
var DEFAULT_AUTO_SELECT_FAMILY_ATTEMPT_TIMEOUT_MS = 2e3;
var HTTP_IDLE_TIMEOUT_CHOICES = [
  { label: "30 sec", timeoutMs: 3e4 },
  { label: "1 min", timeoutMs: 6e4 },
  { label: "2 min", timeoutMs: 12e4 },
  { label: "5 min", timeoutMs: 3e5 },
  { label: "disabled", timeoutMs: 0 }
];
var originalGlobalFetch = globalThis.fetch;
var installedGlobalFetch;
function parseHttpIdleTimeoutMs(value) {
  if (typeof value === "string") {
    const trimmed = value.trim();
    if (trimmed.toLowerCase() === "disabled") {
      return 0;
    }
    if (trimmed.length === 0) {
      return void 0;
    }
    return parseHttpIdleTimeoutMs(Number(trimmed));
  }
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0) {
    return void 0;
  }
  return Math.floor(value);
}
__name(parseHttpIdleTimeoutMs, "parseHttpIdleTimeoutMs");
function formatHttpIdleTimeoutMs(timeoutMs) {
  const choice = HTTP_IDLE_TIMEOUT_CHOICES.find((item) => item.timeoutMs === timeoutMs);
  if (choice) {
    return choice.label;
  }
  return `${timeoutMs / 1e3} sec`;
}
__name(formatHttpIdleTimeoutMs, "formatHttpIdleTimeoutMs");
function applyHttpProxySettings(httpProxy) {
  const proxy = httpProxy?.trim();
  if (!proxy)
    return;
  process.env.HTTP_PROXY ??= proxy;
  process.env.HTTPS_PROXY ??= proxy;
}
__name(applyHttpProxySettings, "applyHttpProxySettings");
var ignoreUndiciDispatcherError = /* @__PURE__ */ __name((_error) => {
}, "ignoreUndiciDispatcherError");
function withUndiciErrorListener(dispatcher) {
  if (dispatcher instanceof EventEmitter2) {
    EventEmitter2.prototype.on.call(dispatcher, "error", ignoreUndiciDispatcherError);
  }
  return dispatcher;
}
__name(withUndiciErrorListener, "withUndiciErrorListener");
function createUndiciClient(origin, options) {
  return withUndiciErrorListener(new undici.Client(origin, options));
}
__name(createUndiciClient, "createUndiciClient");
function createUndiciOriginDispatcher(origin, options) {
  const dispatcherOptions = options;
  if (dispatcherOptions.connections === 1) {
    return createUndiciClient(origin, dispatcherOptions);
  }
  return withUndiciErrorListener(new undici.Pool(origin, {
    ...dispatcherOptions,
    factory: createUndiciClient
  }));
}
__name(createUndiciOriginDispatcher, "createUndiciOriginDispatcher");
function configureHttpDispatcher(timeoutMs = DEFAULT_HTTP_IDLE_TIMEOUT_MS) {
  const normalizedTimeoutMs = parseHttpIdleTimeoutMs(timeoutMs);
  if (normalizedTimeoutMs === void 0) {
    throw new Error(`Invalid HTTP idle timeout: ${String(timeoutMs)}`);
  }
  const dispatcher = withUndiciErrorListener(new undici.EnvHttpProxyAgent({
    allowH2: false,
    // Keep HTTP origins on CONNECT tunnels as they were before Undici 8.7.
    proxyTunnel: true,
    bodyTimeout: normalizedTimeoutMs,
    connect: {
      autoSelectFamilyAttemptTimeout: DEFAULT_AUTO_SELECT_FAMILY_ATTEMPT_TIMEOUT_MS
    },
    headersTimeout: normalizedTimeoutMs,
    clientFactory: createUndiciClient,
    factory: createUndiciOriginDispatcher
  }));
  undici.setGlobalDispatcher(dispatcher);
  const shouldInstallGlobals = installedGlobalFetch === void 0 ? globalThis.fetch === originalGlobalFetch : globalThis.fetch === installedGlobalFetch;
  if (shouldInstallGlobals) {
    undici.install?.();
    installedGlobalFetch = globalThis.fetch;
  }
}
__name(configureHttpDispatcher, "configureHttpDispatcher");

// pi-dist/pi-coding-agent/core/settings-manager.js
var DEFAULT_COMPACTION_TOKEN_SETTINGS = {
  reserveTokens: 16384,
  keepRecentTokens: 2e4
};
var CACHE_WARMING_MODES = ["off", "streaming", "idle"];
function isMergeableObject(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
__name(isMergeableObject, "isMergeableObject");
function deepMergeObjects(base, overrides) {
  const result = { ...base };
  for (const key of Object.keys(overrides)) {
    const overrideValue = overrides[key];
    if (overrideValue === void 0) {
      continue;
    }
    const baseValue = base[key];
    result[key] = isMergeableObject(baseValue) && isMergeableObject(overrideValue) ? deepMergeObjects(baseValue, overrideValue) : overrideValue;
  }
  return result;
}
__name(deepMergeObjects, "deepMergeObjects");
function deepMergeSettings(base, overrides) {
  return deepMergeObjects(base, overrides);
}
__name(deepMergeSettings, "deepMergeSettings");
function parseTimeoutSetting(value, settingName) {
  const timeoutMs = parseHttpIdleTimeoutMs(value);
  if (timeoutMs !== void 0) {
    return timeoutMs;
  }
  if (value !== void 0) {
    throw new Error(`Invalid ${settingName} setting: ${String(value)}`);
  }
  return void 0;
}
__name(parseTimeoutSetting, "parseTimeoutSetting");
function toSettingsError(scope, error, path2) {
  return {
    scope,
    ...path2 ? { path: path2 } : {},
    error: error instanceof Error ? error : new Error(String(error))
  };
}
__name(toSettingsError, "toSettingsError");
var FileSettingsStorage = class {
  static {
    __name(this, "FileSettingsStorage");
  }
  globalSettingsPath;
  projectSettingsPath;
  constructor(cwd, agentDir) {
    const resolvedCwd = resolvePath(cwd);
    const resolvedAgentDir = resolvePath(agentDir);
    this.globalSettingsPath = join12(resolvedAgentDir, "settings.json");
    this.projectSettingsPath = join12(resolvedCwd, CONFIG_DIR_NAME, "settings.json");
  }
  acquireLockSyncWithRetry(path2) {
    const maxAttempts = 10;
    const delayMs = 20;
    let lastError;
    for (let attempt = 1; attempt <= maxAttempts; attempt++) {
      try {
        return lockfile2.lockSync(path2, { realpath: false });
      } catch (error) {
        const code = typeof error === "object" && error !== null && "code" in error ? String(error.code) : void 0;
        if (code !== "ELOCKED" || attempt === maxAttempts) {
          throw error;
        }
        lastError = error;
        const start = Date.now();
        while (Date.now() - start < delayMs) {
        }
      }
    }
    throw lastError ?? new Error("Failed to acquire settings lock");
  }
  withLock(scope, fn) {
    const path2 = scope === "global" ? this.globalSettingsPath : this.projectSettingsPath;
    const dir = dirname9(path2);
    let release2;
    try {
      const fileExists = existsSync9(path2);
      if (fileExists) {
        release2 = this.acquireLockSyncWithRetry(path2);
      }
      const current = fileExists ? readFileSync8(path2, "utf-8") : void 0;
      const next = fn(current);
      if (next !== void 0) {
        if (!existsSync9(dir)) {
          mkdirSync5(dir, { recursive: true });
        }
        if (!release2) {
          release2 = this.acquireLockSyncWithRetry(path2);
        }
        writeFileSync6(path2, next, "utf-8");
      }
    } finally {
      if (release2) {
        release2();
      }
    }
  }
};
var InMemorySettingsStorage = class {
  static {
    __name(this, "InMemorySettingsStorage");
  }
  global;
  project;
  withLock(scope, fn) {
    const current = scope === "global" ? this.global : this.project;
    const next = fn(current);
    if (next !== void 0) {
      if (scope === "global") {
        this.global = next;
      } else {
        this.project = next;
      }
    }
  }
};
var SettingsManager = class _SettingsManager {
  static {
    __name(this, "SettingsManager");
  }
  storage;
  globalSettings;
  projectSettings;
  settings;
  projectTrusted;
  modifiedFields = /* @__PURE__ */ new Set();
  // Track global fields modified during session
  modifiedNestedFields = /* @__PURE__ */ new Map();
  // Track global nested field modifications
  modifiedProjectFields = /* @__PURE__ */ new Set();
  // Track project fields modified during session
  modifiedProjectNestedFields = /* @__PURE__ */ new Map();
  // Track project nested field modifications
  globalSettingsLoadError = null;
  // Track if global settings file had parse errors
  projectSettingsLoadError = null;
  // Track if project settings file had parse errors
  writeQueue = Promise.resolve();
  errors;
  settingsPaths;
  constructor(storage, initialGlobal, initialProject, globalLoadError = null, projectLoadError = null, initialErrors = [], projectTrusted = true, settingsPaths = {}) {
    this.storage = storage;
    this.globalSettings = initialGlobal;
    this.projectSettings = initialProject;
    this.projectTrusted = projectTrusted;
    this.globalSettingsLoadError = globalLoadError;
    this.projectSettingsLoadError = projectLoadError;
    this.errors = [...initialErrors];
    this.settingsPaths = settingsPaths;
    this.settings = deepMergeSettings(this.globalSettings, this.projectSettings);
  }
  /** Create a SettingsManager that loads from files */
  static create(cwd, agentDir = getAgentDir(), options = {}) {
    const resolvedCwd = resolvePath(cwd);
    const resolvedAgentDir = resolvePath(agentDir);
    const storage = new FileSettingsStorage(resolvedCwd, resolvedAgentDir);
    return _SettingsManager.fromStorageWithPaths(storage, options, {
      global: join12(resolvedAgentDir, "settings.json"),
      project: join12(resolvedCwd, CONFIG_DIR_NAME, "settings.json")
    });
  }
  /** Create a SettingsManager from an arbitrary storage backend */
  static fromStorage(storage, options = {}) {
    return _SettingsManager.fromStorageWithPaths(storage, options);
  }
  /** Create a manager while retaining optional file paths for reported storage errors. */
  static fromStorageWithPaths(storage, options, settingsPaths = {}) {
    const projectTrusted = options.projectTrusted ?? true;
    const globalLoad = _SettingsManager.tryLoadFromStorage(storage, "global");
    const projectLoad = _SettingsManager.tryLoadFromStorage(storage, "project", projectTrusted);
    const initialErrors = [];
    if (globalLoad.error) {
      initialErrors.push(toSettingsError("global", globalLoad.error, settingsPaths.global));
    }
    if (projectLoad.error) {
      initialErrors.push(toSettingsError("project", projectLoad.error, settingsPaths.project));
    }
    return new _SettingsManager(storage, globalLoad.settings, projectLoad.settings, globalLoad.error, projectLoad.error, initialErrors, projectTrusted, settingsPaths);
  }
  /** Create an in-memory SettingsManager (no file I/O) */
  static inMemory(settings = {}, options = {}) {
    const storage = new InMemorySettingsStorage();
    const initialSettings = _SettingsManager.migrateSettings(structuredClone(settings));
    storage.withLock("global", () => JSON.stringify(initialSettings, null, 2));
    return _SettingsManager.fromStorage(storage, options);
  }
  static loadFromStorage(storage, scope, projectTrusted = true) {
    if (scope === "project" && !projectTrusted) {
      return {};
    }
    let content;
    storage.withLock(scope, (current) => {
      content = current;
      return void 0;
    });
    if (!content) {
      return {};
    }
    const settings = JSON.parse(stripBom(content));
    return _SettingsManager.migrateSettings(settings);
  }
  static tryLoadFromStorage(storage, scope, projectTrusted = true) {
    try {
      return { settings: _SettingsManager.loadFromStorage(storage, scope, projectTrusted), error: null };
    } catch (error) {
      return { settings: {}, error };
    }
  }
  /** Migrate old settings format to new format */
  static migrateSettings(settings) {
    if ("queueMode" in settings && !("steeringMode" in settings)) {
      settings.steeringMode = settings.queueMode;
      delete settings.queueMode;
    }
    if (!("transport" in settings) && typeof settings.websockets === "boolean") {
      settings.transport = settings.websockets ? "websocket" : "sse";
      delete settings.websockets;
    }
    if ("skills" in settings && typeof settings.skills === "object" && settings.skills !== null && !Array.isArray(settings.skills)) {
      const skillsSettings = settings.skills;
      if (skillsSettings.enableSkillCommands !== void 0 && settings.enableSkillCommands === void 0) {
        settings.enableSkillCommands = skillsSettings.enableSkillCommands;
      }
      if (Array.isArray(skillsSettings.customDirectories) && skillsSettings.customDirectories.length > 0) {
        settings.skills = skillsSettings.customDirectories;
      } else {
        delete settings.skills;
      }
    }
    if ("retry" in settings && typeof settings.retry === "object" && settings.retry !== null && !Array.isArray(settings.retry)) {
      const retrySettings = settings.retry;
      const providerSettings = typeof retrySettings.provider === "object" && retrySettings.provider !== null ? retrySettings.provider : void 0;
      if (typeof retrySettings.maxDelayMs === "number" && (providerSettings?.maxRetryDelayMs === void 0 || providerSettings?.maxRetryDelayMs === null)) {
        retrySettings.provider = {
          ...providerSettings ?? {},
          maxRetryDelayMs: retrySettings.maxDelayMs
        };
      }
      delete retrySettings.maxDelayMs;
    }
    return settings;
  }
  getGlobalSettings() {
    return structuredClone(this.globalSettings);
  }
  getProjectSettings() {
    return structuredClone(this.projectSettings);
  }
  isProjectTrusted() {
    return this.projectTrusted;
  }
  setProjectTrusted(trusted) {
    if (this.projectTrusted === trusted) {
      return;
    }
    this.projectTrusted = trusted;
    this.modifiedProjectFields.clear();
    this.modifiedProjectNestedFields.clear();
    if (!trusted) {
      this.projectSettings = {};
      this.projectSettingsLoadError = null;
      this.settings = deepMergeSettings(this.globalSettings, this.projectSettings);
      return;
    }
    const projectLoad = _SettingsManager.tryLoadFromStorage(this.storage, "project", trusted);
    this.projectSettings = projectLoad.settings;
    this.projectSettingsLoadError = projectLoad.error;
    if (projectLoad.error) {
      this.recordError("project", projectLoad.error);
    }
    this.settings = deepMergeSettings(this.globalSettings, this.projectSettings);
  }
  async reload() {
    await this.writeQueue;
    const globalLoad = _SettingsManager.tryLoadFromStorage(this.storage, "global");
    if (!globalLoad.error) {
      this.globalSettings = globalLoad.settings;
      this.globalSettingsLoadError = null;
    } else {
      this.globalSettingsLoadError = globalLoad.error;
      this.recordError("global", globalLoad.error);
    }
    this.modifiedFields.clear();
    this.modifiedNestedFields.clear();
    this.modifiedProjectFields.clear();
    this.modifiedProjectNestedFields.clear();
    const projectLoad = _SettingsManager.tryLoadFromStorage(this.storage, "project", this.projectTrusted);
    if (!projectLoad.error) {
      this.projectSettings = projectLoad.settings;
      this.projectSettingsLoadError = null;
    } else {
      this.projectSettingsLoadError = projectLoad.error;
      this.recordError("project", projectLoad.error);
    }
    this.settings = deepMergeSettings(this.globalSettings, this.projectSettings);
  }
  /** Apply additional overrides on top of current settings */
  applyOverrides(overrides) {
    this.settings = deepMergeSettings(this.settings, overrides);
  }
  /** Mark a global field as modified during this session */
  markModified(field, nestedKey) {
    this.modifiedFields.add(field);
    if (nestedKey) {
      if (!this.modifiedNestedFields.has(field)) {
        this.modifiedNestedFields.set(field, /* @__PURE__ */ new Set());
      }
      this.modifiedNestedFields.get(field).add(nestedKey);
    }
  }
  /** Mark a project field as modified during this session */
  markProjectModified(field, nestedKey) {
    this.modifiedProjectFields.add(field);
    if (nestedKey) {
      if (!this.modifiedProjectNestedFields.has(field)) {
        this.modifiedProjectNestedFields.set(field, /* @__PURE__ */ new Set());
      }
      this.modifiedProjectNestedFields.get(field).add(nestedKey);
    }
  }
  assertProjectTrustedForWrite() {
    if (!this.projectTrusted) {
      throw new Error("Project is not trusted; refusing to write project settings");
    }
  }
  recordError(scope, error) {
    this.errors.push(toSettingsError(scope, error, this.settingsPaths[scope]));
  }
  clearModifiedScope(scope) {
    if (scope === "global") {
      this.modifiedFields.clear();
      this.modifiedNestedFields.clear();
      return;
    }
    this.modifiedProjectFields.clear();
    this.modifiedProjectNestedFields.clear();
  }
  enqueueWrite(scope, task) {
    this.writeQueue = this.writeQueue.then(() => {
      if (scope === "project") {
        this.assertProjectTrustedForWrite();
      }
      task();
      this.clearModifiedScope(scope);
    }).catch((error) => {
      this.recordError(scope, error);
    });
  }
  cloneModifiedNestedFields(source) {
    const snapshot = /* @__PURE__ */ new Map();
    for (const [key, value] of source.entries()) {
      snapshot.set(key, new Set(value));
    }
    return snapshot;
  }
  persistScopedSettings(scope, snapshotSettings, modifiedFields, modifiedNestedFields) {
    this.storage.withLock(scope, (current) => {
      const currentFileSettings = current ? _SettingsManager.migrateSettings(JSON.parse(stripBom(current))) : {};
      const mergedSettings = { ...currentFileSettings };
      for (const field of modifiedFields) {
        const value = snapshotSettings[field];
        if (modifiedNestedFields.has(field) && typeof value === "object" && value !== null) {
          const nestedModified = modifiedNestedFields.get(field);
          const baseNested = currentFileSettings[field] ?? {};
          const inMemoryNested = value;
          const mergedNested = { ...baseNested };
          for (const nestedKey of nestedModified) {
            mergedNested[nestedKey] = inMemoryNested[nestedKey];
          }
          mergedSettings[field] = mergedNested;
        } else {
          mergedSettings[field] = value;
        }
      }
      return JSON.stringify(mergedSettings, null, 2);
    });
  }
  save() {
    this.settings = deepMergeSettings(this.globalSettings, this.projectSettings);
    if (this.globalSettingsLoadError) {
      return;
    }
    const snapshotGlobalSettings = structuredClone(this.globalSettings);
    const modifiedFields = new Set(this.modifiedFields);
    const modifiedNestedFields = this.cloneModifiedNestedFields(this.modifiedNestedFields);
    this.enqueueWrite("global", () => {
      this.persistScopedSettings("global", snapshotGlobalSettings, modifiedFields, modifiedNestedFields);
    });
  }
  saveProjectSettings(settings) {
    this.assertProjectTrustedForWrite();
    this.projectSettings = structuredClone(settings);
    this.settings = deepMergeSettings(this.globalSettings, this.projectSettings);
    if (this.projectSettingsLoadError) {
      return;
    }
    const snapshotProjectSettings = structuredClone(this.projectSettings);
    const modifiedFields = new Set(this.modifiedProjectFields);
    const modifiedNestedFields = this.cloneModifiedNestedFields(this.modifiedProjectNestedFields);
    this.enqueueWrite("project", () => {
      this.persistScopedSettings("project", snapshotProjectSettings, modifiedFields, modifiedNestedFields);
    });
  }
  updateProjectSettings(field, update) {
    this.assertProjectTrustedForWrite();
    const projectSettings = structuredClone(this.projectSettings);
    update(projectSettings);
    this.markProjectModified(field);
    this.saveProjectSettings(projectSettings);
  }
  async flush() {
    await this.writeQueue;
  }
  drainErrors() {
    const drained = [...this.errors];
    this.errors = [];
    return drained;
  }
  getLastChangelogVersion() {
    return this.settings.lastChangelogVersion;
  }
  setLastChangelogVersion(version2) {
    this.globalSettings.lastChangelogVersion = version2;
    this.markModified("lastChangelogVersion");
    this.save();
  }
  getSessionDir() {
    const sessionDir = this.settings.sessionDir;
    return sessionDir ? normalizePath(sessionDir) : sessionDir;
  }
  getDefaultProvider() {
    return this.settings.defaultProvider;
  }
  getDefaultModel() {
    return this.settings.defaultModel;
  }
  setDefaultProvider(provider) {
    this.globalSettings.defaultProvider = provider;
    this.markModified("defaultProvider");
    this.save();
  }
  setDefaultModel(modelId) {
    this.globalSettings.defaultModel = modelId;
    this.markModified("defaultModel");
    this.save();
  }
  setDefaultModelAndProvider(provider, modelId) {
    this.globalSettings.defaultProvider = provider;
    this.globalSettings.defaultModel = modelId;
    this.markModified("defaultProvider");
    this.markModified("defaultModel");
    this.save();
  }
  getSteeringMode() {
    return this.settings.steeringMode || "one-at-a-time";
  }
  setSteeringMode(mode) {
    this.globalSettings.steeringMode = mode;
    this.markModified("steeringMode");
    this.save();
  }
  getFollowUpMode() {
    return this.settings.followUpMode || "one-at-a-time";
  }
  setFollowUpMode(mode) {
    this.globalSettings.followUpMode = mode;
    this.markModified("followUpMode");
    this.save();
  }
  getThemeSetting() {
    const value = this.settings.theme;
    if (typeof value === "string")
      return value;
    return void 0;
  }
  getTheme() {
    const theme2 = this.getThemeSetting();
    return theme2?.includes("/") ? void 0 : theme2;
  }
  setTheme(theme2) {
    this.globalSettings.theme = theme2;
    this.markModified("theme");
    this.save();
  }
  getDefaultThinkingLevel() {
    return this.settings.defaultThinkingLevel;
  }
  setDefaultThinkingLevel(level) {
    this.globalSettings.defaultThinkingLevel = level;
    this.markModified("defaultThinkingLevel");
    this.save();
  }
  getModelThinkingLevel(provider, modelId) {
    return this.settings.modelThinkingLevels?.[`${provider}/${modelId}`];
  }
  getAllModelThinkingLevels() {
    return { ...this.settings.modelThinkingLevels ?? {} };
  }
  setModelThinkingLevel(provider, modelId, level) {
    if (!this.globalSettings.modelThinkingLevels) {
      this.globalSettings.modelThinkingLevels = {};
    }
    this.globalSettings.modelThinkingLevels[`${provider}/${modelId}`] = level;
    this.markModified("modelThinkingLevels");
    this.save();
  }
  removeModelThinkingLevel(provider, modelId) {
    if (!this.globalSettings.modelThinkingLevels)
      return;
    delete this.globalSettings.modelThinkingLevels[`${provider}/${modelId}`];
    if (Object.keys(this.globalSettings.modelThinkingLevels).length === 0) {
      delete this.globalSettings.modelThinkingLevels;
    }
    this.markModified("modelThinkingLevels");
    this.save();
  }
  getTransport() {
    return this.settings.transport ?? "auto";
  }
  setTransport(transport) {
    this.globalSettings.transport = transport;
    this.markModified("transport");
    this.save();
  }
  getCompactionEnabled() {
    return this.settings.compaction?.enabled ?? true;
  }
  setCompactionEnabled(enabled) {
    if (!this.globalSettings.compaction) {
      this.globalSettings.compaction = {};
    }
    this.globalSettings.compaction.enabled = enabled;
    this.markModified("compaction", "enabled");
    this.save();
  }
  getCompactionTokenSetting(field, model) {
    const compaction = this.settings.compaction;
    const ordinary = compaction?.[field];
    if (ordinary !== void 0 && (typeof ordinary !== "number" || !Number.isSafeInteger(ordinary) || ordinary < 0)) {
      throw new Error(`Invalid compaction.${field} setting: ${String(ordinary)}. Expected a non-negative safe integer.`);
    }
    const modelKey = model ? `${model.provider}/${model.id}` : void 0;
    const entry = modelKey !== void 0 ? compaction?.modelOverrides?.[modelKey] : void 0;
    if (entry !== void 0 && !isMergeableObject(entry)) {
      throw new Error(`Invalid compaction.modelOverrides["${modelKey}"] setting: ${String(entry)}. Expected an object.`);
    }
    const override = entry?.[field];
    if (override !== void 0 && (typeof override !== "number" || !Number.isSafeInteger(override) || override < 0)) {
      throw new Error(`Invalid compaction.modelOverrides["${modelKey}"].${field} setting: ${String(override)}. Expected a non-negative safe integer.`);
    }
    return override ?? ordinary ?? DEFAULT_COMPACTION_TOKEN_SETTINGS[field];
  }
  getCompactionReserveTokens(model) {
    return this.getCompactionTokenSetting("reserveTokens", model);
  }
  getCompactionKeepRecentTokens(model) {
    return this.getCompactionTokenSetting("keepRecentTokens", model);
  }
  /** Resolve each token setting through model override, ordinary setting, then built-in default. */
  getCompactionSettings(model) {
    return {
      enabled: this.getCompactionEnabled(),
      reserveTokens: this.getCompactionReserveTokens(model),
      keepRecentTokens: this.getCompactionKeepRecentTokens(model)
    };
  }
  getBranchSummarySettings() {
    return {
      reserveTokens: this.settings.branchSummary?.reserveTokens ?? 16384,
      skipPrompt: this.settings.branchSummary?.skipPrompt ?? false
    };
  }
  getBranchSummarySkipPrompt() {
    return this.settings.branchSummary?.skipPrompt ?? false;
  }
  getRetryEnabled() {
    return this.settings.retry?.enabled ?? true;
  }
  setRetryEnabled(enabled) {
    if (!this.globalSettings.retry) {
      this.globalSettings.retry = {};
    }
    this.globalSettings.retry.enabled = enabled;
    this.markModified("retry", "enabled");
    this.save();
  }
  getRetrySettings() {
    return {
      enabled: this.getRetryEnabled(),
      maxRetries: this.settings.retry?.maxRetries ?? 3,
      baseDelayMs: this.settings.retry?.baseDelayMs ?? 2e3,
      maxAgentDelayMs: this.settings.retry?.maxAgentDelayMs ?? DEFAULT_MAX_AGENT_RETRY_DELAY_MS
    };
  }
  getHttpIdleTimeoutMs() {
    return parseTimeoutSetting(this.settings.httpIdleTimeoutMs, "httpIdleTimeoutMs") ?? DEFAULT_HTTP_IDLE_TIMEOUT_MS;
  }
  setHttpIdleTimeoutMs(timeoutMs) {
    if (!Number.isFinite(timeoutMs) || timeoutMs < 0) {
      throw new Error(`Invalid httpIdleTimeoutMs setting: ${String(timeoutMs)}`);
    }
    this.globalSettings.httpIdleTimeoutMs = Math.floor(timeoutMs);
    this.markModified("httpIdleTimeoutMs");
    this.save();
  }
  /** Read from global settings only because warming costs money. */
  getCacheWarmingMode() {
    const mode = this.globalSettings.cacheWarming;
    return mode !== void 0 && CACHE_WARMING_MODES.includes(mode) ? mode : "streaming";
  }
  setCacheWarmingMode(mode) {
    this.globalSettings.cacheWarming = mode;
    this.markModified("cacheWarming");
    this.save();
  }
  getProviderRetrySettings() {
    return {
      timeoutMs: this.settings.retry?.provider?.timeoutMs,
      maxRetries: this.settings.retry?.provider?.maxRetries,
      maxRetryDelayMs: this.settings.retry?.provider?.maxRetryDelayMs ?? 6e4
    };
  }
  getWebSocketConnectTimeoutMs() {
    return parseTimeoutSetting(this.settings.websocketConnectTimeoutMs, "websocketConnectTimeoutMs");
  }
  getHideThinkingBlock() {
    return this.settings.hideThinkingBlock ?? false;
  }
  getShowCacheMissNotices() {
    return this.settings.showCacheMissNotices ?? false;
  }
  getExternalEditorCommand() {
    const configuredEditor = this.settings.externalEditor;
    if (typeof configuredEditor === "string" && configuredEditor.trim() !== "") {
      return configuredEditor;
    }
    const environmentEditor = process.env.VISUAL || process.env.EDITOR;
    if (environmentEditor) {
      return environmentEditor;
    }
    return process.platform === "win32" ? "notepad" : "nano";
  }
  setHideThinkingBlock(hide) {
    this.globalSettings.hideThinkingBlock = hide;
    this.markModified("hideThinkingBlock");
    this.save();
  }
  setShowCacheMissNotices(show) {
    this.globalSettings.showCacheMissNotices = show;
    this.markModified("showCacheMissNotices");
    this.save();
  }
  getShellPath() {
    const shellPath = this.settings.shellPath;
    return shellPath ? normalizePath(shellPath) : shellPath;
  }
  setShellPath(path2) {
    this.globalSettings.shellPath = path2;
    this.markModified("shellPath");
    this.save();
  }
  getQuietStartup() {
    return this.settings.quietStartup ?? false;
  }
  setQuietStartup(quiet) {
    this.globalSettings.quietStartup = quiet;
    this.markModified("quietStartup");
    this.save();
  }
  getDefaultProjectTrust() {
    const value = this.globalSettings.defaultProjectTrust;
    return value === "always" || value === "never" ? value : "ask";
  }
  setDefaultProjectTrust(defaultProjectTrust) {
    this.globalSettings.defaultProjectTrust = defaultProjectTrust;
    this.markModified("defaultProjectTrust");
    this.save();
  }
  getShellCommandPrefix() {
    return this.settings.shellCommandPrefix;
  }
  setShellCommandPrefix(prefix) {
    this.globalSettings.shellCommandPrefix = prefix;
    this.markModified("shellCommandPrefix");
    this.save();
  }
  getNpmCommand() {
    return this.settings.npmCommand ? [...this.settings.npmCommand] : void 0;
  }
  setNpmCommand(command) {
    this.globalSettings.npmCommand = command ? [...command] : void 0;
    this.markModified("npmCommand");
    this.save();
  }
  getCollapseChangelog() {
    return this.settings.collapseChangelog ?? false;
  }
  setCollapseChangelog(collapse) {
    this.globalSettings.collapseChangelog = collapse;
    this.markModified("collapseChangelog");
    this.save();
  }
  getEnableInstallTelemetry() {
    return this.settings.enableInstallTelemetry ?? true;
  }
  setEnableInstallTelemetry(enabled) {
    this.globalSettings.enableInstallTelemetry = enabled;
    this.markModified("enableInstallTelemetry");
    this.save();
  }
  getEnableAnalytics() {
    return this.settings.enableAnalytics ?? false;
  }
  getTrackingId() {
    return this.settings.trackingId;
  }
  /** Set the analytics opt-in preference; generates a tracking identifier on first opt-in */
  setEnableAnalytics(enabled) {
    this.globalSettings.enableAnalytics = enabled;
    this.markModified("enableAnalytics");
    if (enabled && !this.globalSettings.trackingId) {
      this.globalSettings.trackingId = randomUUID2();
      this.markModified("trackingId");
    }
    this.save();
  }
  getPackages() {
    return [...this.settings.packages ?? []];
  }
  setPackages(packages) {
    this.globalSettings.packages = packages;
    this.markModified("packages");
    this.save();
  }
  setProjectPackages(packages) {
    this.updateProjectSettings("packages", (settings) => {
      settings.packages = packages;
    });
  }
  getExtensionPaths() {
    return [...this.settings.extensions ?? []];
  }
  setExtensionPaths(paths) {
    this.globalSettings.extensions = paths;
    this.markModified("extensions");
    this.save();
  }
  setProjectExtensionPaths(paths) {
    this.updateProjectSettings("extensions", (settings) => {
      settings.extensions = paths;
    });
  }
  getSkillPaths() {
    return [...this.settings.skills ?? []];
  }
  setSkillPaths(paths) {
    this.globalSettings.skills = paths;
    this.markModified("skills");
    this.save();
  }
  setProjectSkillPaths(paths) {
    this.updateProjectSettings("skills", (settings) => {
      settings.skills = paths;
    });
  }
  getPromptTemplatePaths() {
    return [...this.settings.prompts ?? []];
  }
  setPromptTemplatePaths(paths) {
    this.globalSettings.prompts = paths;
    this.markModified("prompts");
    this.save();
  }
  setProjectPromptTemplatePaths(paths) {
    this.updateProjectSettings("prompts", (settings) => {
      settings.prompts = paths;
    });
  }
  getThemePaths() {
    return [...this.settings.themes ?? []];
  }
  setThemePaths(paths) {
    this.globalSettings.themes = paths;
    this.markModified("themes");
    this.save();
  }
  setProjectThemePaths(paths) {
    this.updateProjectSettings("themes", (settings) => {
      settings.themes = paths;
    });
  }
  getEnableSkillCommands() {
    return this.settings.enableSkillCommands ?? true;
  }
  setEnableSkillCommands(enabled) {
    this.globalSettings.enableSkillCommands = enabled;
    this.markModified("enableSkillCommands");
    this.save();
  }
  getThinkingBudgets() {
    return this.settings.thinkingBudgets;
  }
  getTerminalCapabilityOverrides() {
    const terminal = this.settings.terminal;
    const images = terminal?.images;
    return {
      ...images === "kitty" || images === "iterm2" ? { images } : images === false ? { images: null } : {},
      ...typeof terminal?.trueColor === "boolean" ? { trueColor: terminal.trueColor } : {},
      ...typeof terminal?.hyperlinks === "boolean" ? { hyperlinks: terminal.hyperlinks } : {}
    };
  }
  getShowImages() {
    return this.settings.terminal?.showImages ?? true;
  }
  setShowImages(show) {
    if (!this.globalSettings.terminal) {
      this.globalSettings.terminal = {};
    }
    this.globalSettings.terminal.showImages = show;
    this.markModified("terminal", "showImages");
    this.save();
  }
  getImageWidthCells() {
    const width = this.settings.terminal?.imageWidthCells;
    if (typeof width !== "number" || !Number.isFinite(width)) {
      return 60;
    }
    return Math.max(1, Math.floor(width));
  }
  setImageWidthCells(width) {
    if (!this.globalSettings.terminal) {
      this.globalSettings.terminal = {};
    }
    this.globalSettings.terminal.imageWidthCells = Math.max(1, Math.floor(width));
    this.markModified("terminal", "imageWidthCells");
    this.save();
  }
  getClearOnShrink() {
    if (this.settings.terminal?.clearOnShrink !== void 0) {
      return this.settings.terminal.clearOnShrink;
    }
    return process.env.PI_CLEAR_ON_SHRINK === "1";
  }
  setClearOnShrink(enabled) {
    if (!this.globalSettings.terminal) {
      this.globalSettings.terminal = {};
    }
    this.globalSettings.terminal.clearOnShrink = enabled;
    this.markModified("terminal", "clearOnShrink");
    this.save();
  }
  getShowTerminalProgress() {
    return this.settings.terminal?.showTerminalProgress ?? false;
  }
  setShowTerminalProgress(enabled) {
    if (!this.globalSettings.terminal) {
      this.globalSettings.terminal = {};
    }
    this.globalSettings.terminal.showTerminalProgress = enabled;
    this.markModified("terminal", "showTerminalProgress");
    this.save();
  }
  getTuiMode() {
    return this.settings.tuiMode === "fullscreen" ? "fullscreen" : "regular";
  }
  setTuiMode(mode) {
    this.globalSettings.tuiMode = mode;
    this.markModified("tuiMode");
    this.save();
  }
  getFullscreenExitOutput() {
    return this.settings.fullscreenExitOutput === "resume-hint" ? "resume-hint" : "transcript";
  }
  setFullscreenExitOutput(output) {
    this.globalSettings.fullscreenExitOutput = output;
    this.markModified("fullscreenExitOutput");
    this.save();
  }
  getFullscreenScrollbar() {
    const mode = this.settings.fullscreenScrollbar;
    return mode === "always" || mode === "hidden" ? mode : "auto";
  }
  setFullscreenScrollbar(mode) {
    this.globalSettings.fullscreenScrollbar = mode;
    this.markModified("fullscreenScrollbar");
    this.save();
  }
  getFullscreenCopyOnSelect() {
    return this.settings.fullscreenCopyOnSelect ?? true;
  }
  setFullscreenCopyOnSelect(enabled) {
    this.globalSettings.fullscreenCopyOnSelect = enabled;
    this.markModified("fullscreenCopyOnSelect");
    this.save();
  }
  getImageAutoResize() {
    return this.settings.images?.autoResize ?? true;
  }
  setImageAutoResize(enabled) {
    if (!this.globalSettings.images) {
      this.globalSettings.images = {};
    }
    this.globalSettings.images.autoResize = enabled;
    this.markModified("images", "autoResize");
    this.save();
  }
  getBlockImages() {
    return this.settings.images?.blockImages ?? false;
  }
  setBlockImages(blocked) {
    if (!this.globalSettings.images) {
      this.globalSettings.images = {};
    }
    this.globalSettings.images.blockImages = blocked;
    this.markModified("images", "blockImages");
    this.save();
  }
  getEnabledModels() {
    return this.settings.enabledModels;
  }
  getDefaultTools() {
    const tools = this.settings.defaultTools;
    return tools ? [...tools] : void 0;
  }
  setEnabledModels(patterns) {
    this.globalSettings.enabledModels = patterns;
    this.markModified("enabledModels");
    this.save();
  }
  getDoubleEscapeAction() {
    return this.settings.doubleEscapeAction ?? "tree";
  }
  setDoubleEscapeAction(action) {
    this.globalSettings.doubleEscapeAction = action;
    this.markModified("doubleEscapeAction");
    this.save();
  }
  getTreeFilterMode() {
    const mode = this.settings.treeFilterMode;
    const valid2 = ["default", "no-tools", "user-only", "labeled-only", "all"];
    return mode && valid2.includes(mode) ? mode : "default";
  }
  setTreeFilterMode(mode) {
    this.globalSettings.treeFilterMode = mode;
    this.markModified("treeFilterMode");
    this.save();
  }
  getShowHardwareCursor() {
    return this.settings.showHardwareCursor ?? process.env.PI_HARDWARE_CURSOR === "1";
  }
  setShowHardwareCursor(enabled) {
    this.globalSettings.showHardwareCursor = enabled;
    this.markModified("showHardwareCursor");
    this.save();
  }
  getEditorPaddingX() {
    return this.settings.editorPaddingX ?? 0;
  }
  setEditorPaddingX(padding) {
    this.globalSettings.editorPaddingX = Math.max(0, Math.min(3, Math.floor(padding)));
    this.markModified("editorPaddingX");
    this.save();
  }
  getOutputPad() {
    return this.settings.outputPad === 0 ? 0 : 1;
  }
  setOutputPad(padding) {
    this.globalSettings.outputPad = padding;
    this.markModified("outputPad");
    this.save();
  }
  getAutocompleteMaxVisible() {
    return this.settings.autocompleteMaxVisible ?? 5;
  }
  setAutocompleteMaxVisible(maxVisible) {
    this.globalSettings.autocompleteMaxVisible = Math.max(3, Math.min(20, Math.floor(maxVisible)));
    this.markModified("autocompleteMaxVisible");
    this.save();
  }
  getCodeBlockIndent() {
    return this.settings.markdown?.codeBlockIndent ?? "  ";
  }
  getMermaidRenderingMode() {
    const mode = this.settings.markdown?.mermaid;
    return mode === "off" || mode === "final" ? mode : "streaming";
  }
  setMermaidRenderingMode(mode) {
    this.globalSettings.markdown ??= {};
    this.globalSettings.markdown.mermaid = mode;
    this.markModified("markdown", "mermaid");
    this.save();
  }
  getWarnings() {
    return { ...this.settings.warnings ?? {} };
  }
  setWarnings(warnings) {
    this.globalSettings.warnings = { ...warnings };
    this.markModified("warnings");
    this.save();
  }
};

// pi-dist/pi-coding-agent/core/resource-loader.js
import { existsSync as existsSync11, readdirSync as readdirSync6, readFileSync as readFileSync10, statSync as statSync7 } from "node:fs";
import { basename as basename7, dirname as dirname11, join as join14, resolve as resolve7, sep as sep4 } from "node:path";
import chalk3 from "../../../chalk/source/index.js";

// pi-dist/pi-coding-agent/core/footer-data-provider.js
import { execFile, spawnSync as spawnSync2 } from "child_process";
import { existsSync as existsSync10, readFileSync as readFileSync9, statSync as statSync6, unwatchFile, watchFile } from "fs";
import { dirname as dirname10, join as join13, resolve as resolve6 } from "path";
function findGitPaths(cwd) {
  let dir = cwd;
  while (true) {
    const gitPath = join13(dir, ".git");
    if (existsSync10(gitPath)) {
      try {
        const stat2 = statSync6(gitPath);
        if (stat2.isFile()) {
          const content = readFileSync9(gitPath, "utf8").trim();
          if (content.startsWith("gitdir: ")) {
            const gitDir = resolve6(dir, content.slice(8).trim());
            const headPath = join13(gitDir, "HEAD");
            if (!existsSync10(headPath))
              return null;
            const commonDirPath = join13(gitDir, "commondir");
            const commonGitDir = existsSync10(commonDirPath) ? resolve6(gitDir, readFileSync9(commonDirPath, "utf8").trim()) : gitDir;
            return { repoDir: dir, commonGitDir, headPath };
          }
        } else if (stat2.isDirectory()) {
          const headPath = join13(gitPath, "HEAD");
          if (!existsSync10(headPath))
            return null;
          return { repoDir: dir, commonGitDir: gitPath, headPath };
        }
      } catch {
        return null;
      }
    }
    const parent = dirname10(dir);
    if (parent === dir)
      return null;
    dir = parent;
  }
}
__name(findGitPaths, "findGitPaths");
function resolveBranchWithGitSync(repoDir) {
  const result = spawnSync2("git", ["--no-optional-locks", "symbolic-ref", "--quiet", "--short", "HEAD"], {
    cwd: repoDir,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "ignore"]
  });
  const branch = result.status === 0 ? result.stdout.trim() : "";
  return branch || null;
}
__name(resolveBranchWithGitSync, "resolveBranchWithGitSync");
function resolveBranchWithGitAsync(repoDir) {
  return new Promise((resolvePromise) => {
    execFile("git", ["--no-optional-locks", "symbolic-ref", "--quiet", "--short", "HEAD"], {
      cwd: repoDir,
      encoding: "utf8"
    }, (error, stdout) => {
      if (error) {
        resolvePromise(null);
        return;
      }
      const branch = stdout.trim();
      resolvePromise(branch || null);
    });
  });
}
__name(resolveBranchWithGitAsync, "resolveBranchWithGitAsync");
function isWslEnvironment() {
  return process.platform === "linux" && !!(process.env.WSL_DISTRO_NAME || process.env.WSL_INTEROP);
}
__name(isWslEnvironment, "isWslEnvironment");
function isWindowsMountedRepoPath(repoDir) {
  return /^\/mnt\/[a-z](?:\/|$)/i.test(repoDir);
}
__name(isWindowsMountedRepoPath, "isWindowsMountedRepoPath");
function shouldPollGitHead(repoDir) {
  return isWslEnvironment() && isWindowsMountedRepoPath(repoDir);
}
__name(shouldPollGitHead, "shouldPollGitHead");
var FooterDataProvider = class _FooterDataProvider {
  static {
    __name(this, "FooterDataProvider");
  }
  cwd;
  static WATCH_DEBOUNCE_MS = 500;
  extensionStatuses = /* @__PURE__ */ new Map();
  cachedBranch = void 0;
  gitPaths = void 0;
  headWatcher = null;
  headWatchFilePath = null;
  headWatchFileListener = null;
  reftableWatcher = null;
  reftableTablesListWatcher = null;
  reftableTablesListPath = null;
  branchChangeCallbacks = /* @__PURE__ */ new Set();
  availableProviderCount = 0;
  refreshTimer = null;
  gitWatcherRetryTimer = null;
  refreshInFlight = false;
  refreshPending = false;
  disposed = false;
  constructor(cwd) {
    this.cwd = cwd;
    this.gitPaths = findGitPaths(cwd);
    this.setupGitWatcher();
  }
  /** Current git branch, null if not in repo, "detached" if detached HEAD */
  getGitBranch() {
    if (this.cachedBranch === void 0) {
      this.cachedBranch = this.resolveGitBranchSync();
    }
    return this.cachedBranch;
  }
  /** Extension status texts set via ctx.ui.setStatus() */
  getExtensionStatuses() {
    return this.extensionStatuses;
  }
  /** Subscribe to git branch changes. Returns unsubscribe function. */
  onBranchChange(callback) {
    this.branchChangeCallbacks.add(callback);
    return () => this.branchChangeCallbacks.delete(callback);
  }
  /** Internal: set extension status */
  setExtensionStatus(key, text) {
    if (text === void 0) {
      this.extensionStatuses.delete(key);
    } else {
      this.extensionStatuses.set(key, text);
    }
  }
  /** Internal: clear extension statuses */
  clearExtensionStatuses() {
    this.extensionStatuses.clear();
  }
  /** Number of unique providers with available models (for footer display) */
  getAvailableProviderCount() {
    return this.availableProviderCount;
  }
  /** Internal: update available provider count */
  setAvailableProviderCount(count) {
    this.availableProviderCount = count;
  }
  setCwd(cwd) {
    if (this.cwd === cwd) {
      return;
    }
    this.cwd = cwd;
    if (this.refreshTimer) {
      clearTimeout(this.refreshTimer);
      this.refreshTimer = null;
    }
    this.clearGitWatchers();
    this.cachedBranch = void 0;
    this.gitPaths = findGitPaths(cwd);
    this.setupGitWatcher();
    this.notifyBranchChange();
  }
  /** Internal: cleanup */
  dispose() {
    this.disposed = true;
    if (this.refreshTimer) {
      clearTimeout(this.refreshTimer);
      this.refreshTimer = null;
    }
    this.clearGitWatchers();
    this.branchChangeCallbacks.clear();
  }
  notifyBranchChange() {
    for (const cb of this.branchChangeCallbacks)
      cb();
  }
  scheduleRefresh() {
    if (this.disposed || this.refreshTimer)
      return;
    if (this.refreshInFlight) {
      this.refreshPending = true;
      return;
    }
    this.refreshTimer = setTimeout(() => {
      this.refreshTimer = null;
      void this.refreshGitBranchAsync();
    }, _FooterDataProvider.WATCH_DEBOUNCE_MS);
  }
  async refreshGitBranchAsync() {
    if (this.disposed)
      return;
    if (this.refreshInFlight) {
      this.refreshPending = true;
      return;
    }
    this.refreshInFlight = true;
    try {
      const nextBranch = await this.resolveGitBranchAsync();
      if (this.disposed)
        return;
      if (this.cachedBranch !== void 0 && this.cachedBranch !== nextBranch) {
        this.cachedBranch = nextBranch;
        this.notifyBranchChange();
        return;
      }
      this.cachedBranch = nextBranch;
    } finally {
      this.refreshInFlight = false;
      if (this.refreshPending && !this.disposed) {
        this.refreshPending = false;
        this.scheduleRefresh();
      }
    }
  }
  resolveGitBranchSync() {
    try {
      if (!this.gitPaths)
        return null;
      const content = readFileSync9(this.gitPaths.headPath, "utf8").trim();
      if (content.startsWith("ref: refs/heads/")) {
        const branch = content.slice(16);
        return branch === ".invalid" ? resolveBranchWithGitSync(this.gitPaths.repoDir) ?? "detached" : branch;
      }
      return "detached";
    } catch {
      return null;
    }
  }
  async resolveGitBranchAsync() {
    try {
      if (!this.gitPaths)
        return null;
      const content = readFileSync9(this.gitPaths.headPath, "utf8").trim();
      if (content.startsWith("ref: refs/heads/")) {
        const branch = content.slice(16);
        return branch === ".invalid" ? await resolveBranchWithGitAsync(this.gitPaths.repoDir) ?? "detached" : branch;
      }
      return "detached";
    } catch {
      return null;
    }
  }
  clearGitWatchers() {
    closeWatcher(this.headWatcher);
    this.headWatcher = null;
    if (this.headWatchFilePath && this.headWatchFileListener) {
      unwatchFile(this.headWatchFilePath, this.headWatchFileListener);
      this.headWatchFilePath = null;
      this.headWatchFileListener = null;
    }
    closeWatcher(this.reftableWatcher);
    this.reftableWatcher = null;
    closeWatcher(this.reftableTablesListWatcher);
    this.reftableTablesListWatcher = null;
    if (this.reftableTablesListPath) {
      unwatchFile(this.reftableTablesListPath);
      this.reftableTablesListPath = null;
    }
    if (this.gitWatcherRetryTimer) {
      clearTimeout(this.gitWatcherRetryTimer);
      this.gitWatcherRetryTimer = null;
    }
  }
  scheduleGitWatcherRetry() {
    if (this.disposed || this.gitWatcherRetryTimer) {
      return;
    }
    this.gitWatcherRetryTimer = setTimeout(() => {
      this.gitWatcherRetryTimer = null;
      this.setupGitWatcher();
    }, FS_WATCH_RETRY_DELAY_MS);
  }
  handleGitWatcherError() {
    this.clearGitWatchers();
    this.scheduleGitWatcherRetry();
  }
  setupGitWatcher() {
    this.clearGitWatchers();
    if (!this.gitPaths)
      return;
    const pollGitHead = shouldPollGitHead(this.gitPaths.repoDir);
    this.headWatcher = watchWithErrorHandler(dirname10(this.gitPaths.headPath), (_eventType, filename) => {
      if (!filename || filename === "HEAD") {
        this.scheduleRefresh();
      }
    }, () => this.handleGitWatcherError());
    if (pollGitHead) {
      this.headWatchFilePath = this.gitPaths.headPath;
      this.headWatchFileListener = (current, previous) => {
        if (current.mtimeMs !== previous.mtimeMs || current.ctimeMs !== previous.ctimeMs || current.size !== previous.size) {
          this.scheduleRefresh();
        }
      };
      watchFile(this.headWatchFilePath, { interval: 1e3 }, this.headWatchFileListener);
    }
    if (!this.headWatcher && !pollGitHead) {
      return;
    }
    const reftableDir = join13(this.gitPaths.commonGitDir, "reftable");
    if (existsSync10(reftableDir)) {
      this.reftableWatcher = watchWithErrorHandler(reftableDir, () => {
        this.scheduleRefresh();
      }, () => this.handleGitWatcherError());
      if (!this.reftableWatcher) {
        return;
      }
      const tablesListPath = join13(reftableDir, "tables.list");
      if (existsSync10(tablesListPath)) {
        this.reftableTablesListPath = tablesListPath;
        this.reftableTablesListWatcher = watchWithErrorHandler(tablesListPath, () => {
          this.scheduleRefresh();
        }, () => this.handleGitWatcherError());
        if (!this.reftableTablesListWatcher) {
          return;
        }
        watchFile(tablesListPath, { interval: 250 }, (current, previous) => {
          if (current.mtimeMs !== previous.mtimeMs || current.ctimeMs !== previous.ctimeMs || current.size !== previous.size) {
            this.scheduleRefresh();
          }
        });
      }
    }
  }
};

// pi-dist/pi-coding-agent/core/resource-loader.js
function resolvePromptInput(input, description) {
  if (!input) {
    return void 0;
  }
  if (existsSync11(input)) {
    try {
      return stripBom(readFileSync10(input, "utf-8"));
    } catch (error) {
      console.error(chalk3.yellow(`Warning: Could not read ${description} file ${input}: ${error}`));
      return input;
    }
  }
  return input;
}
__name(resolvePromptInput, "resolvePromptInput");
function loadContextFileFromDir(dir) {
  const candidates = ["AGENTS.override.md", "AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"];
  for (const filename of candidates) {
    const filePath = join14(dir, filename);
    if (existsSync11(filePath)) {
      try {
        if (!statSync7(filePath).isFile()) {
          continue;
        }
        return {
          path: filePath,
          content: stripBom(readFileSync10(filePath, "utf-8"))
        };
      } catch (error) {
        console.error(chalk3.yellow(`Warning: Could not read ${filePath}: ${error}`));
      }
    }
  }
  return null;
}
__name(loadContextFileFromDir, "loadContextFileFromDir");
function findShadowedContextFile(cwd) {
  const gitPaths = findGitPaths(cwd);
  if (!gitPaths)
    return void 0;
  const commonGitDir = canonicalizePath(gitPaths.commonGitDir);
  const worktreeRoot = canonicalizePath(gitPaths.repoDir);
  const mainRepoRoot = dirname11(commonGitDir);
  if (!worktreeRoot.startsWith(`${mainRepoRoot}${sep4}`))
    return void 0;
  if (canonicalizePath(join14(mainRepoRoot, ".git")) !== commonGitDir)
    return void 0;
  const worktreeContextFile = loadContextFileFromDir(worktreeRoot);
  return worktreeContextFile ? join14(mainRepoRoot, basename7(worktreeContextFile.path)) : void 0;
}
__name(findShadowedContextFile, "findShadowedContextFile");
function loadProjectContextFiles(options) {
  const resolvedCwd = resolvePath(options.cwd);
  const resolvedAgentDir = resolvePath(options.agentDir);
  const contextFiles = [];
  const seenPaths = /* @__PURE__ */ new Set();
  const globalContext = loadContextFileFromDir(resolvedAgentDir);
  if (globalContext) {
    contextFiles.push(globalContext);
    seenPaths.add(globalContext.path);
  }
  const ancestorContextFiles = [];
  const shadowedContextFile = findShadowedContextFile(resolvedCwd);
  let currentDir = resolvedCwd;
  while (true) {
    const contextFile = loadContextFileFromDir(currentDir);
    const isShadowed = shadowedContextFile !== void 0 && canonicalizePath(contextFile?.path ?? "") === shadowedContextFile;
    if (contextFile && !isShadowed && !seenPaths.has(contextFile.path)) {
      ancestorContextFiles.unshift(contextFile);
      seenPaths.add(contextFile.path);
    }
    const parentDir = dirname11(currentDir);
    if (parentDir === currentDir)
      break;
    currentDir = parentDir;
  }
  contextFiles.push(...ancestorContextFiles);
  return contextFiles;
}
__name(loadProjectContextFiles, "loadProjectContextFiles");
var DefaultResourceLoader = class {
  static {
    __name(this, "DefaultResourceLoader");
  }
  cwd;
  agentDir;
  settingsManager;
  eventBus;
  packageManager;
  additionalExtensionPaths;
  additionalSkillPaths;
  additionalPromptTemplatePaths;
  additionalThemePaths;
  extensionFactories;
  noExtensions;
  noSkills;
  noPromptTemplates;
  noThemes;
  noContextFiles;
  systemPromptSource;
  appendSystemPromptSource;
  extensionsOverride;
  skillsOverride;
  promptsOverride;
  themesOverride;
  agentsFilesOverride;
  systemPromptOverride;
  appendSystemPromptOverride;
  extensionsResult;
  skills;
  skillDiagnostics;
  prompts;
  promptDiagnostics;
  themes;
  themeDiagnostics;
  agentsFiles;
  systemPrompt;
  systemPromptSourcePath;
  appendSystemPrompt;
  appendSystemPromptSourcePaths;
  lastSkillPaths;
  extensionSkillSourceInfos;
  extensionPromptSourceInfos;
  extensionThemeSourceInfos;
  resourceMetadataByPath;
  lastPromptPaths;
  lastThemePaths;
  loaded;
  constructor(options) {
    this.cwd = resolvePath(options.cwd);
    this.agentDir = resolvePath(options.agentDir);
    this.settingsManager = options.settingsManager ?? SettingsManager.create(this.cwd, this.agentDir);
    this.eventBus = options.eventBus ?? createEventBus();
    this.packageManager = new DefaultPackageManager({
      cwd: this.cwd,
      agentDir: this.agentDir,
      settingsManager: this.settingsManager
    });
    this.additionalExtensionPaths = options.additionalExtensionPaths ?? [];
    this.additionalSkillPaths = options.additionalSkillPaths ?? [];
    this.additionalPromptTemplatePaths = options.additionalPromptTemplatePaths ?? [];
    this.additionalThemePaths = options.additionalThemePaths ?? [];
    this.extensionFactories = options.extensionFactories ?? [];
    this.noExtensions = options.noExtensions ?? false;
    this.noSkills = options.noSkills ?? false;
    this.noPromptTemplates = options.noPromptTemplates ?? false;
    this.noThemes = options.noThemes ?? false;
    this.noContextFiles = options.noContextFiles ?? false;
    this.systemPromptSource = options.systemPrompt;
    this.appendSystemPromptSource = options.appendSystemPrompt;
    this.extensionsOverride = options.extensionsOverride;
    this.skillsOverride = options.skillsOverride;
    this.promptsOverride = options.promptsOverride;
    this.themesOverride = options.themesOverride;
    this.agentsFilesOverride = options.agentsFilesOverride;
    this.systemPromptOverride = options.systemPromptOverride;
    this.appendSystemPromptOverride = options.appendSystemPromptOverride;
    this.extensionsResult = { extensions: [], errors: [], runtime: createExtensionRuntime() };
    this.skills = [];
    this.skillDiagnostics = [];
    this.prompts = [];
    this.promptDiagnostics = [];
    this.themes = [];
    this.themeDiagnostics = [];
    this.agentsFiles = [];
    this.appendSystemPrompt = [];
    this.appendSystemPromptSourcePaths = [];
    this.lastSkillPaths = [];
    this.extensionSkillSourceInfos = /* @__PURE__ */ new Map();
    this.extensionPromptSourceInfos = /* @__PURE__ */ new Map();
    this.extensionThemeSourceInfos = /* @__PURE__ */ new Map();
    this.resourceMetadataByPath = /* @__PURE__ */ new Map();
    this.lastPromptPaths = [];
    this.lastThemePaths = [];
    this.loaded = false;
  }
  getExtensions() {
    return this.extensionsResult;
  }
  getSkills() {
    return { skills: this.skills, diagnostics: this.skillDiagnostics };
  }
  getPrompts() {
    return { prompts: this.prompts, diagnostics: this.promptDiagnostics };
  }
  getThemes() {
    return { themes: this.themes, diagnostics: this.themeDiagnostics };
  }
  getAgentsFiles() {
    return { agentsFiles: this.agentsFiles };
  }
  getSystemPrompt() {
    return this.systemPrompt;
  }
  getSystemPromptSource() {
    return this.systemPromptSourcePath ? { path: this.systemPromptSourcePath } : void 0;
  }
  getAppendSystemPrompt() {
    return this.appendSystemPrompt;
  }
  getAppendSystemPromptSources() {
    return this.appendSystemPromptSourcePaths.map((path2) => ({ path: path2 }));
  }
  extendResources(paths) {
    const skillPaths = this.normalizeExtensionPaths(paths.skillPaths ?? []);
    const promptPaths = this.normalizeExtensionPaths(paths.promptPaths ?? []);
    const themePaths = this.normalizeExtensionPaths(paths.themePaths ?? []);
    for (const entry of skillPaths) {
      this.extensionSkillSourceInfos.set(entry.path, createSourceInfo(entry.path, entry.metadata));
    }
    for (const entry of promptPaths) {
      this.extensionPromptSourceInfos.set(entry.path, createSourceInfo(entry.path, entry.metadata));
    }
    for (const entry of themePaths) {
      this.extensionThemeSourceInfos.set(entry.path, createSourceInfo(entry.path, entry.metadata));
    }
    if (skillPaths.length > 0) {
      this.lastSkillPaths = this.mergePaths(this.lastSkillPaths, skillPaths.map((entry) => entry.path));
      this.updateSkillsFromPaths(this.lastSkillPaths, this.resourceMetadataByPath);
    }
    if (promptPaths.length > 0) {
      this.lastPromptPaths = this.mergePaths(this.lastPromptPaths, promptPaths.map((entry) => entry.path));
      this.updatePromptsFromPaths(this.lastPromptPaths, this.resourceMetadataByPath);
    }
    if (themePaths.length > 0) {
      this.lastThemePaths = this.mergePaths(this.lastThemePaths, themePaths.map((entry) => entry.path));
      this.updateThemesFromPaths(this.lastThemePaths, this.resourceMetadataByPath);
    }
  }
  async loadProjectTrustExtensions() {
    this.settingsManager.setProjectTrusted(false);
    await this.settingsManager.reload();
    return this.loadCurrentExtensionSet({ includeInlineFactories: true });
  }
  async reload(options) {
    resetTimings("extensions");
    if (this.loaded) {
      clearExtensionCache();
    }
    let preTrustExtensions;
    if (options?.resolveProjectTrust) {
      preTrustExtensions = await this.loadProjectTrustExtensions();
      const projectTrusted = await options.resolveProjectTrust({ extensionsResult: preTrustExtensions });
      this.settingsManager.setProjectTrusted(projectTrusted);
    }
    await this.settingsManager.reload();
    const resolvedPaths = await this.packageManager.resolve();
    const cliExtensionPaths = await this.packageManager.resolveExtensionSources(this.additionalExtensionPaths, {
      temporary: true
    });
    this.resourceMetadataByPath = /* @__PURE__ */ new Map();
    const metadataByPath = this.resourceMetadataByPath;
    this.extensionSkillSourceInfos = /* @__PURE__ */ new Map();
    this.extensionPromptSourceInfos = /* @__PURE__ */ new Map();
    this.extensionThemeSourceInfos = /* @__PURE__ */ new Map();
    const getEnabledResources = /* @__PURE__ */ __name((resources) => {
      for (const r of resources) {
        if (!metadataByPath.has(r.path)) {
          metadataByPath.set(r.path, r.metadata);
        }
      }
      return resources.filter((r) => r.enabled);
    }, "getEnabledResources");
    const getEnabledPaths = /* @__PURE__ */ __name((resources) => getEnabledResources(resources).map((r) => r.path), "getEnabledPaths");
    const enabledExtensions = getEnabledPaths(resolvedPaths.extensions);
    const enabledSkillResources = getEnabledResources(resolvedPaths.skills);
    const enabledPrompts = getEnabledPaths(resolvedPaths.prompts);
    const enabledThemes = getEnabledPaths(resolvedPaths.themes);
    const enabledSkills = enabledSkillResources.map((resource) => this.mapSkillPath(resource, metadataByPath));
    for (const r of cliExtensionPaths.extensions) {
      if (!metadataByPath.has(r.path)) {
        metadataByPath.set(r.path, { source: "cli", scope: "temporary", origin: "top-level" });
      }
    }
    for (const r of cliExtensionPaths.skills) {
      if (!metadataByPath.has(r.path)) {
        metadataByPath.set(r.path, { source: "cli", scope: "temporary", origin: "top-level" });
      }
    }
    const cliEnabledExtensions = getEnabledPaths(cliExtensionPaths.extensions);
    const cliEnabledSkills = getEnabledPaths(cliExtensionPaths.skills);
    const cliEnabledPrompts = getEnabledPaths(cliExtensionPaths.prompts);
    const cliEnabledThemes = getEnabledPaths(cliExtensionPaths.themes);
    const extensionPaths = this.noExtensions ? cliEnabledExtensions : this.mergePaths(cliEnabledExtensions, enabledExtensions);
    const extensionsResult = await this.loadFinalExtensionSet(extensionPaths, preTrustExtensions);
    for (const p of this.additionalExtensionPaths) {
      if (isLocalPath(p)) {
        const resolved = this.resolveResourcePath(p);
        if (!existsSync11(resolved)) {
          extensionsResult.errors.push({ path: resolved, error: `Extension path does not exist: ${resolved}` });
        }
      }
    }
    this.extensionsResult = this.extensionsOverride ? this.extensionsOverride(extensionsResult) : extensionsResult;
    this.applyExtensionSourceInfo(this.extensionsResult.extensions, metadataByPath);
    const skillPaths = this.noSkills ? this.mergePaths(cliEnabledSkills, this.additionalSkillPaths) : this.mergePaths([...cliEnabledSkills, ...enabledSkills], this.additionalSkillPaths);
    this.lastSkillPaths = skillPaths;
    this.updateSkillsFromPaths(skillPaths, metadataByPath);
    for (const p of this.additionalSkillPaths) {
      if (isLocalPath(p)) {
        const resolved = this.resolveResourcePath(p);
        if (!existsSync11(resolved) && !this.skillDiagnostics.some((d) => d.path === resolved)) {
          this.skillDiagnostics.push({ type: "error", message: "Skill path does not exist", path: resolved });
        }
      }
    }
    const promptPaths = this.noPromptTemplates ? this.mergePaths(cliEnabledPrompts, this.additionalPromptTemplatePaths) : this.mergePaths([...cliEnabledPrompts, ...enabledPrompts], this.additionalPromptTemplatePaths);
    this.lastPromptPaths = promptPaths;
    this.updatePromptsFromPaths(promptPaths, metadataByPath);
    for (const p of this.additionalPromptTemplatePaths) {
      if (isLocalPath(p)) {
        const resolved = this.resolveResourcePath(p);
        if (!existsSync11(resolved) && !this.promptDiagnostics.some((d) => d.path === resolved)) {
          this.promptDiagnostics.push({
            type: "error",
            message: "Prompt template path does not exist",
            path: resolved
          });
        }
      }
    }
    const themePaths = this.noThemes ? this.mergePaths(cliEnabledThemes, this.additionalThemePaths) : this.mergePaths([...cliEnabledThemes, ...enabledThemes], this.additionalThemePaths);
    this.lastThemePaths = themePaths;
    this.updateThemesFromPaths(themePaths, metadataByPath);
    for (const p of this.additionalThemePaths) {
      const resolved = this.resolveResourcePath(p);
      if (!existsSync11(resolved) && !this.themeDiagnostics.some((d) => d.path === resolved)) {
        this.themeDiagnostics.push({ type: "error", message: "Theme path does not exist", path: resolved });
      }
    }
    const agentsFiles = {
      agentsFiles: this.noContextFiles ? [] : loadProjectContextFiles({
        cwd: this.cwd,
        agentDir: this.agentDir
      })
    };
    const resolvedAgentsFiles = this.agentsFilesOverride ? this.agentsFilesOverride(agentsFiles) : agentsFiles;
    this.agentsFiles = resolvedAgentsFiles.agentsFiles;
    const systemPromptSource = this.systemPromptSource ?? this.discoverSystemPromptFile();
    const baseSystemPrompt = resolvePromptInput(systemPromptSource, "system prompt");
    this.systemPrompt = this.systemPromptOverride ? this.systemPromptOverride(baseSystemPrompt) : baseSystemPrompt;
    this.systemPromptSourcePath = systemPromptSource && existsSync11(systemPromptSource) ? resolvePath(systemPromptSource) : void 0;
    let appendSources = this.appendSystemPromptSource;
    if (!appendSources) {
      const discoveredAppendSystemPromptFile = this.discoverAppendSystemPromptFile();
      appendSources = discoveredAppendSystemPromptFile ? [discoveredAppendSystemPromptFile] : [];
    }
    const baseAppend = appendSources.map((s) => resolvePromptInput(s, "append system prompt")).filter((s) => s !== void 0);
    this.appendSystemPrompt = this.appendSystemPromptOverride ? this.appendSystemPromptOverride(baseAppend) : baseAppend;
    this.appendSystemPromptSourcePaths = appendSources.filter((source) => existsSync11(source)).map((source) => resolvePath(source));
    this.loaded = true;
  }
  async loadCurrentExtensionSet(options) {
    const resolvedPaths = await this.packageManager.resolve();
    const cliExtensionPaths = await this.packageManager.resolveExtensionSources(this.additionalExtensionPaths, {
      temporary: true
    });
    const enabledExtensions = resolvedPaths.extensions.filter((r) => r.enabled).map((r) => r.path);
    const cliEnabledExtensions = cliExtensionPaths.extensions.filter((r) => r.enabled).map((r) => r.path);
    const extensionPaths = this.noExtensions ? cliEnabledExtensions : this.mergePaths(cliEnabledExtensions, enabledExtensions);
    const extensionsResult = await loadExtensionsCached(extensionPaths, this.cwd, this.eventBus);
    if (!options.includeInlineFactories) {
      return extensionsResult;
    }
    const inlineExtensions = await this.loadExtensionFactories(extensionsResult.runtime);
    extensionsResult.extensions.push(...inlineExtensions.extensions);
    extensionsResult.errors.push(...inlineExtensions.errors);
    return extensionsResult;
  }
  resolveExtensionLoadPath(path2) {
    return resolvePath(path2, this.cwd, { normalizeUnicodeSpaces: true });
  }
  async loadFinalExtensionSet(extensionPaths, preTrustExtensions) {
    if (!preTrustExtensions) {
      const extensionsResult2 = await loadExtensionsCached(extensionPaths, this.cwd, this.eventBus);
      const inlineExtensions2 = await this.loadExtensionFactories(extensionsResult2.runtime);
      extensionsResult2.extensions.push(...inlineExtensions2.extensions);
      extensionsResult2.errors.push(...inlineExtensions2.errors);
      this.addExtensionConflictDiagnostics(extensionsResult2);
      return extensionsResult2;
    }
    const preloadedByPath = new Map(preTrustExtensions.extensions.filter((extension) => !extension.path.startsWith("<inline:")).map((extension) => [extension.resolvedPath, extension]));
    const failedPreloadPaths = new Set(preTrustExtensions.errors.map((error) => this.resolveExtensionLoadPath(error.path)));
    const remainingPaths = extensionPaths.filter((path2) => {
      const resolvedPath = this.resolveExtensionLoadPath(path2);
      return !preloadedByPath.has(resolvedPath) && !failedPreloadPaths.has(resolvedPath);
    });
    const remainingExtensions = await loadExtensionsCached(remainingPaths, this.cwd, this.eventBus, preTrustExtensions.runtime);
    const loadedByPath = new Map(preloadedByPath);
    for (const extension of remainingExtensions.extensions) {
      loadedByPath.set(extension.resolvedPath, extension);
    }
    const inlineExtensions = preTrustExtensions.extensions.filter((extension) => extension.path.startsWith("<inline:"));
    const orderedExtensions = extensionPaths.map((path2) => loadedByPath.get(this.resolveExtensionLoadPath(path2))).filter((extension) => extension !== void 0);
    orderedExtensions.push(...inlineExtensions);
    const extensionsResult = {
      extensions: orderedExtensions,
      errors: [...preTrustExtensions.errors, ...remainingExtensions.errors],
      runtime: preTrustExtensions.runtime
    };
    this.addExtensionConflictDiagnostics(extensionsResult);
    return extensionsResult;
  }
  addExtensionConflictDiagnostics(extensionsResult) {
    const conflicts = this.detectExtensionConflicts(extensionsResult.extensions);
    for (const conflict of conflicts) {
      extensionsResult.errors.push({ path: conflict.path, error: conflict.message });
    }
  }
  mapSkillPath(resource, metadataByPath) {
    if (resource.metadata.source !== "auto" && resource.metadata.origin !== "package") {
      return resource.path;
    }
    try {
      const stats = statSync7(resource.path);
      if (!stats.isDirectory()) {
        return resource.path;
      }
    } catch {
      return resource.path;
    }
    const skillFile = join14(resource.path, "SKILL.md");
    if (existsSync11(skillFile)) {
      if (!metadataByPath.has(skillFile)) {
        metadataByPath.set(skillFile, resource.metadata);
      }
      return skillFile;
    }
    return resource.path;
  }
  normalizeExtensionPaths(entries) {
    return entries.map((entry) => {
      const metadata = entry.metadata.baseDir ? { ...entry.metadata, baseDir: this.resolveResourcePath(entry.metadata.baseDir) } : entry.metadata;
      return {
        path: this.resolveResourcePath(entry.path),
        metadata
      };
    });
  }
  updateSkillsFromPaths(skillPaths, metadataByPath) {
    let skillsResult;
    if (this.noSkills && skillPaths.length === 0) {
      skillsResult = { skills: [], diagnostics: [] };
    } else {
      skillsResult = loadSkills({
        cwd: this.cwd,
        agentDir: this.agentDir,
        skillPaths,
        includeDefaults: false
      });
    }
    const resolvedSkills = this.skillsOverride ? this.skillsOverride(skillsResult) : skillsResult;
    this.skills = resolvedSkills.skills.map((skill) => ({
      ...skill,
      sourceInfo: this.findSourceInfoForPath(skill.filePath, this.extensionSkillSourceInfos, metadataByPath) ?? skill.sourceInfo ?? this.getDefaultSourceInfoForPath(skill.filePath)
    }));
    this.skillDiagnostics = resolvedSkills.diagnostics;
  }
  updatePromptsFromPaths(promptPaths, metadataByPath) {
    let promptsResult;
    if (this.noPromptTemplates && promptPaths.length === 0) {
      promptsResult = { prompts: [], diagnostics: [] };
    } else {
      const loaded = loadPromptTemplates({
        cwd: this.cwd,
        agentDir: this.agentDir,
        promptPaths,
        includeDefaults: false
      });
      const deduped = this.dedupePrompts(loaded.templates);
      promptsResult = {
        prompts: deduped.prompts,
        diagnostics: [...loaded.diagnostics, ...deduped.diagnostics]
      };
    }
    const resolvedPrompts = this.promptsOverride ? this.promptsOverride(promptsResult) : promptsResult;
    this.prompts = resolvedPrompts.prompts.map((prompt) => ({
      ...prompt,
      sourceInfo: this.findSourceInfoForPath(prompt.filePath, this.extensionPromptSourceInfos, metadataByPath) ?? prompt.sourceInfo ?? this.getDefaultSourceInfoForPath(prompt.filePath)
    }));
    this.promptDiagnostics = resolvedPrompts.diagnostics;
  }
  updateThemesFromPaths(themePaths, metadataByPath) {
    let themesResult;
    if (this.noThemes && themePaths.length === 0) {
      themesResult = { themes: [], diagnostics: [] };
    } else {
      const loaded = this.loadThemes(themePaths, false);
      const deduped = this.dedupeThemes(loaded.themes);
      themesResult = { themes: deduped.themes, diagnostics: [...loaded.diagnostics, ...deduped.diagnostics] };
    }
    const resolvedThemes = this.themesOverride ? this.themesOverride(themesResult) : themesResult;
    this.themes = resolvedThemes.themes.map((theme2) => {
      const sourcePath = theme2.sourcePath;
      theme2.sourceInfo = sourcePath ? this.findSourceInfoForPath(sourcePath, this.extensionThemeSourceInfos, metadataByPath) ?? theme2.sourceInfo ?? this.getDefaultSourceInfoForPath(sourcePath) : theme2.sourceInfo;
      return theme2;
    });
    this.themeDiagnostics = resolvedThemes.diagnostics;
  }
  applyExtensionSourceInfo(extensions, metadataByPath) {
    for (const extension of extensions) {
      extension.sourceInfo = this.findSourceInfoForPath(extension.path, void 0, metadataByPath) ?? this.getDefaultSourceInfoForPath(extension.path);
      for (const command of extension.commands.values()) {
        command.sourceInfo = extension.sourceInfo;
      }
      for (const tool of extension.tools.values()) {
        tool.sourceInfo = extension.sourceInfo;
      }
    }
  }
  findSourceInfoForPath(resourcePath, extraSourceInfos, metadataByPath) {
    if (!resourcePath) {
      return void 0;
    }
    if (resourcePath.startsWith("<")) {
      return this.getDefaultSourceInfoForPath(resourcePath);
    }
    const normalizedResourcePath = resolve7(resourcePath);
    if (extraSourceInfos) {
      for (const [sourcePath, sourceInfo] of extraSourceInfos.entries()) {
        const normalizedSourcePath = resolve7(sourcePath);
        if (normalizedResourcePath === normalizedSourcePath || normalizedResourcePath.startsWith(`${normalizedSourcePath}${sep4}`)) {
          return { ...sourceInfo, path: resourcePath };
        }
      }
    }
    if (metadataByPath) {
      const exact = metadataByPath.get(normalizedResourcePath) ?? metadataByPath.get(resourcePath);
      if (exact) {
        return createSourceInfo(resourcePath, exact);
      }
      for (const [sourcePath, metadata] of metadataByPath.entries()) {
        const normalizedSourcePath = resolve7(sourcePath);
        if (normalizedResourcePath === normalizedSourcePath || normalizedResourcePath.startsWith(`${normalizedSourcePath}${sep4}`)) {
          return createSourceInfo(resourcePath, metadata);
        }
      }
    }
    return void 0;
  }
  getDefaultSourceInfoForPath(filePath) {
    if (filePath.startsWith("<") && filePath.endsWith(">")) {
      return {
        path: filePath,
        source: filePath.slice(1, -1).split(":")[0] || "temporary",
        scope: "temporary",
        origin: "top-level"
      };
    }
    const normalizedPath = resolve7(filePath);
    const agentRoots = [
      join14(this.agentDir, "skills"),
      join14(this.agentDir, "prompts"),
      join14(this.agentDir, "themes"),
      join14(this.agentDir, "extensions")
    ];
    const projectRoots = [
      join14(this.cwd, CONFIG_DIR_NAME, "skills"),
      join14(this.cwd, CONFIG_DIR_NAME, "prompts"),
      join14(this.cwd, CONFIG_DIR_NAME, "themes"),
      join14(this.cwd, CONFIG_DIR_NAME, "extensions")
    ];
    for (const root of agentRoots) {
      if (this.isUnderPath(normalizedPath, root)) {
        return { path: filePath, source: "local", scope: "user", origin: "top-level", baseDir: root };
      }
    }
    for (const root of projectRoots) {
      if (this.isUnderPath(normalizedPath, root)) {
        return { path: filePath, source: "local", scope: "project", origin: "top-level", baseDir: root };
      }
    }
    return {
      path: filePath,
      source: "local",
      scope: "temporary",
      origin: "top-level",
      baseDir: statSync7(normalizedPath).isDirectory() ? normalizedPath : resolve7(normalizedPath, "..")
    };
  }
  mergePaths(primary, additional) {
    const merged = [];
    const seen = /* @__PURE__ */ new Set();
    for (const p of [...primary, ...additional]) {
      const resolved = this.resolveResourcePath(p);
      const canonicalPath = canonicalizePath(resolved);
      if (seen.has(canonicalPath))
        continue;
      seen.add(canonicalPath);
      merged.push(resolved);
    }
    return merged;
  }
  resolveResourcePath(p) {
    return resolvePath(p, this.cwd, { trim: true });
  }
  loadThemes(paths, includeDefaults = true) {
    const themes = [];
    const diagnostics = [];
    if (includeDefaults) {
      const defaultDirs = [join14(this.agentDir, "themes"), join14(this.cwd, CONFIG_DIR_NAME, "themes")];
      for (const dir of defaultDirs) {
        this.loadThemesFromDir(dir, themes, diagnostics);
      }
    }
    for (const p of paths) {
      const resolved = this.resolveResourcePath(p);
      if (!existsSync11(resolved)) {
        diagnostics.push({ type: "warning", message: "theme path does not exist", path: resolved });
        continue;
      }
      try {
        const stats = statSync7(resolved);
        if (stats.isDirectory()) {
          this.loadThemesFromDir(resolved, themes, diagnostics);
        } else if (stats.isFile() && resolved.endsWith(".json")) {
          this.loadThemeFromFile(resolved, themes, diagnostics);
        } else {
          diagnostics.push({ type: "warning", message: "theme path is not a json file", path: resolved });
        }
      } catch (error) {
        const message = error instanceof Error ? error.message : "failed to read theme path";
        diagnostics.push({ type: "warning", message, path: resolved });
      }
    }
    return { themes, diagnostics };
  }
  loadThemesFromDir(dir, themes, diagnostics) {
    if (!existsSync11(dir)) {
      return;
    }
    try {
      const entries = readdirSync6(dir, { withFileTypes: true });
      for (const entry of entries) {
        let isFile = entry.isFile();
        if (entry.isSymbolicLink()) {
          try {
            isFile = statSync7(join14(dir, entry.name)).isFile();
          } catch {
            continue;
          }
        }
        if (!isFile) {
          continue;
        }
        if (!entry.name.endsWith(".json")) {
          continue;
        }
        this.loadThemeFromFile(join14(dir, entry.name), themes, diagnostics);
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : "failed to read theme directory";
      diagnostics.push({ type: "warning", message, path: dir });
    }
  }
  loadThemeFromFile(filePath, themes, diagnostics) {
    try {
      themes.push(loadThemeFromPath(filePath));
    } catch (error) {
      const message = error instanceof Error ? error.message : "failed to load theme";
      diagnostics.push({ type: "warning", message, path: filePath });
    }
  }
  async loadExtensionFactories(runtime) {
    const extensions = [];
    const errors = [];
    for (const [index, input] of this.extensionFactories.entries()) {
      const isNamed = typeof input !== "function";
      const factory = isNamed ? input.factory : input;
      const extensionPath = `<inline:${isNamed ? input.name : index + 1}>`;
      try {
        const extension = await loadExtensionFromFactory(factory, this.cwd, this.eventBus, runtime, extensionPath);
        extension.hidden = isNamed && input.hidden;
        extensions.push(extension);
      } catch (error) {
        const message = error instanceof Error ? error.message : "failed to load extension";
        errors.push({ path: extensionPath, error: message });
      }
    }
    return { extensions, errors };
  }
  dedupePrompts(prompts) {
    const seen = /* @__PURE__ */ new Map();
    const diagnostics = [];
    for (const prompt of prompts) {
      const existing = seen.get(prompt.name);
      if (existing) {
        diagnostics.push({
          type: "collision",
          message: `name "/${prompt.name}" collision`,
          path: prompt.filePath,
          collision: {
            resourceType: "prompt",
            name: prompt.name,
            winnerPath: existing.filePath,
            loserPath: prompt.filePath
          }
        });
      } else {
        seen.set(prompt.name, prompt);
      }
    }
    return { prompts: Array.from(seen.values()), diagnostics };
  }
  dedupeThemes(themes) {
    const seen = /* @__PURE__ */ new Map();
    const diagnostics = [];
    for (const t of themes) {
      const name = t.name ?? "unnamed";
      const existing = seen.get(name);
      if (existing) {
        diagnostics.push({
          type: "collision",
          message: `name "${name}" collision`,
          path: t.sourcePath,
          collision: {
            resourceType: "theme",
            name,
            winnerPath: existing.sourcePath ?? "<builtin>",
            loserPath: t.sourcePath ?? "<builtin>"
          }
        });
      } else {
        seen.set(name, t);
      }
    }
    return { themes: Array.from(seen.values()), diagnostics };
  }
  discoverSystemPromptFile() {
    const projectPath = join14(this.cwd, CONFIG_DIR_NAME, "SYSTEM.md");
    if (this.settingsManager.isProjectTrusted() && existsSync11(projectPath)) {
      return projectPath;
    }
    const globalPath = join14(this.agentDir, "SYSTEM.md");
    if (existsSync11(globalPath)) {
      return globalPath;
    }
    return void 0;
  }
  discoverAppendSystemPromptFile() {
    const projectPath = join14(this.cwd, CONFIG_DIR_NAME, "APPEND_SYSTEM.md");
    if (this.settingsManager.isProjectTrusted() && existsSync11(projectPath)) {
      return projectPath;
    }
    const globalPath = join14(this.agentDir, "APPEND_SYSTEM.md");
    if (existsSync11(globalPath)) {
      return globalPath;
    }
    return void 0;
  }
  isUnderPath(target, root) {
    const normalizedRoot = resolve7(root);
    if (target === normalizedRoot) {
      return true;
    }
    const prefix = normalizedRoot.endsWith(sep4) ? normalizedRoot : `${normalizedRoot}${sep4}`;
    return target.startsWith(prefix);
  }
  detectExtensionConflicts(extensions) {
    const conflicts = [];
    const toolOwners = /* @__PURE__ */ new Map();
    const flagOwners = /* @__PURE__ */ new Map();
    for (const ext of extensions) {
      for (const toolName of ext.tools.keys()) {
        const existingOwner = toolOwners.get(toolName);
        if (existingOwner && existingOwner !== ext.path) {
          conflicts.push({
            path: ext.path,
            message: `Tool "${toolName}" conflicts with ${existingOwner}`
          });
        } else {
          toolOwners.set(toolName, ext.path);
        }
      }
      for (const flagName of ext.flags.keys()) {
        const existingOwner = flagOwners.get(flagName);
        if (existingOwner && existingOwner !== ext.path) {
          conflicts.push({
            path: ext.path,
            message: `Flag "--${flagName}" conflicts with ${existingOwner}`
          });
        } else {
          flagOwners.set(flagName, ext.path);
        }
      }
    }
    return conflicts;
  }
};

// pi-dist/pi-coding-agent/core/agent-session-services.js
import { join as join15 } from "node:path";
import { createAgentSession } from "../../../independent-session.mjs";
function applyExtensionFlagValues(resourceLoader, extensionFlagValues) {
  if (!extensionFlagValues) {
    return [];
  }
  const diagnostics = [];
  const extensionsResult = resourceLoader.getExtensions();
  const registeredFlags = /* @__PURE__ */ new Map();
  for (const extension of extensionsResult.extensions) {
    for (const [name, flag] of extension.flags) {
      registeredFlags.set(name, { type: flag.type });
    }
  }
  const unknownFlags = [];
  for (const [name, value] of extensionFlagValues) {
    const flag = registeredFlags.get(name);
    if (!flag) {
      unknownFlags.push(name);
      continue;
    }
    if (flag.type === "boolean") {
      extensionsResult.runtime.flagValues.set(name, true);
      continue;
    }
    if (typeof value === "string") {
      extensionsResult.runtime.flagValues.set(name, value);
      continue;
    }
    diagnostics.push({
      type: "error",
      message: `Extension flag "--${name}" requires a value`
    });
  }
  if (unknownFlags.length > 0) {
    diagnostics.push({
      type: "error",
      message: `Unknown option${unknownFlags.length === 1 ? "" : "s"}: ${unknownFlags.map((name) => `--${name}`).join(", ")}`
    });
  }
  return diagnostics;
}
__name(applyExtensionFlagValues, "applyExtensionFlagValues");
async function createAgentSessionServices(options) {
  const cwd = resolvePath(options.cwd);
  const agentDir = options.agentDir ? resolvePath(options.agentDir) : getAgentDir();
  const modelRuntime = options.modelRuntime ?? await ModelRuntime.create({
    authPath: join15(agentDir, "auth.json"),
    modelsPath: join15(agentDir, "models.json"),
    signal: options.modelRuntimeSignal
  });
  const settingsManager = options.settingsManager ?? SettingsManager.create(cwd, agentDir);
  const resourceLoader = new DefaultResourceLoader({
    ...options.resourceLoaderOptions ?? {},
    cwd,
    agentDir,
    settingsManager
  });
  await resourceLoader.reload(options.resourceLoaderReloadOptions);
  const diagnostics = [];
  const extensionsResult = resourceLoader.getExtensions();
  for (const { name, config, extensionPath } of extensionsResult.runtime.pendingProviderRegistrations) {
    try {
      modelRuntime.registerProvider(name, config);
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      diagnostics.push({
        type: "error",
        message: `Extension "${extensionPath}" error: ${message}`
      });
    }
  }
  extensionsResult.runtime.pendingProviderRegistrations = [];
  for (const { provider, extensionPath } of extensionsResult.runtime.pendingNativeProviderRegistrations) {
    try {
      modelRuntime.registerNativeProvider(provider);
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      diagnostics.push({
        type: "error",
        message: `Extension "${extensionPath}" error: ${message}`
      });
    }
  }
  extensionsResult.runtime.pendingNativeProviderRegistrations = [];
  await modelRuntime.refresh({ allowNetwork: false });
  diagnostics.push(...applyExtensionFlagValues(resourceLoader, options.extensionFlagValues));
  return {
    cwd,
    agentDir,
    modelRuntime,
    settingsManager,
    resourceLoader,
    diagnostics
  };
}
__name(createAgentSessionServices, "createAgentSessionServices");
async function createAgentSessionFromServices(options) {
  return createAgentSession({
    cwd: options.services.cwd,
    agentDir: options.services.agentDir,
    modelRuntime: options.services.modelRuntime,
    settingsManager: options.services.settingsManager,
    resourceLoader: options.services.resourceLoader,
    sessionManager: options.sessionManager,
    model: options.model,
    thinkingLevel: options.thinkingLevel,
    scopedModels: options.scopedModels,
    tools: options.tools,
    excludeTools: options.excludeTools,
    noTools: options.noTools,
    customTools: options.customTools,
    sessionStartEvent: options.sessionStartEvent
  });
}
__name(createAgentSessionFromServices, "createAgentSessionFromServices");

// pi-dist/pi-coding-agent/core/agent-session-runtime.js
import { constants, copyFileSync, existsSync as existsSync13, mkdirSync as mkdirSync6 } from "node:fs";
import { basename as basename8, join as join16, parse as parse2, resolve as resolve8 } from "node:path";

// pi-dist/pi-coding-agent/core/session-cwd.js
import { existsSync as existsSync12 } from "node:fs";
function getMissingSessionCwdIssue(sessionManager, fallbackCwd) {
  const sessionFile = sessionManager.getSessionFile();
  if (!sessionFile) {
    return void 0;
  }
  const sessionCwd = sessionManager.getCwd();
  if (!sessionCwd || existsSync12(sessionCwd)) {
    return void 0;
  }
  return {
    sessionFile,
    sessionCwd,
    fallbackCwd
  };
}
__name(getMissingSessionCwdIssue, "getMissingSessionCwdIssue");
function formatMissingSessionCwdError(issue) {
  const sessionFile = issue.sessionFile ? `
Session file: ${issue.sessionFile}` : "";
  return `Stored session working directory does not exist: ${issue.sessionCwd}${sessionFile}
Current working directory: ${issue.fallbackCwd}`;
}
__name(formatMissingSessionCwdError, "formatMissingSessionCwdError");
function formatMissingSessionCwdPrompt(issue) {
  return `cwd from session file does not exist
${issue.sessionCwd}

continue in current cwd
${issue.fallbackCwd}`;
}
__name(formatMissingSessionCwdPrompt, "formatMissingSessionCwdPrompt");
var MissingSessionCwdError = class extends Error {
  static {
    __name(this, "MissingSessionCwdError");
  }
  issue;
  constructor(issue) {
    super(formatMissingSessionCwdError(issue));
    this.name = "MissingSessionCwdError";
    this.issue = issue;
  }
};
function assertSessionCwdExists(sessionManager, fallbackCwd) {
  const issue = getMissingSessionCwdIssue(sessionManager, fallbackCwd);
  if (issue) {
    throw new MissingSessionCwdError(issue);
  }
}
__name(assertSessionCwdExists, "assertSessionCwdExists");

// pi-dist/pi-coding-agent/core/agent-session-runtime.js
var SessionImportFileNotFoundError = class extends Error {
  static {
    __name(this, "SessionImportFileNotFoundError");
  }
  filePath;
  constructor(filePath) {
    super(`File not found: ${filePath}`);
    this.name = "SessionImportFileNotFoundError";
    this.filePath = filePath;
  }
};
function extractUserMessageText(content) {
  if (typeof content === "string") {
    return content;
  }
  return content.filter((part) => part.type === "text" && typeof part.text === "string").map((part) => part.text).join("");
}
__name(extractUserMessageText, "extractUserMessageText");
var AgentSessionRuntime = class {
  static {
    __name(this, "AgentSessionRuntime");
  }
  rebindSession;
  beforeSessionInvalidate;
  _session;
  _services;
  createRuntime;
  _diagnostics;
  _modelFallbackMessage;
  constructor(_session, _services, createRuntime, _diagnostics = [], _modelFallbackMessage) {
    this._session = _session;
    this._services = _services;
    this.createRuntime = createRuntime;
    this._diagnostics = _diagnostics;
    this._modelFallbackMessage = _modelFallbackMessage;
  }
  get services() {
    return this._services;
  }
  get session() {
    return this._session;
  }
  get cwd() {
    return this._services.cwd;
  }
  get diagnostics() {
    return this._diagnostics;
  }
  get modelFallbackMessage() {
    return this._modelFallbackMessage;
  }
  setRebindSession(rebindSession) {
    this.rebindSession = rebindSession;
  }
  /**
   * Set a synchronous callback that runs after `session_shutdown` handlers finish
   * but before the current session is invalidated.
   *
   * This is for host-owned UI teardown that must not yield to the event loop,
   * such as detaching extension-provided TUI components before the old extension
   * context becomes stale.
   */
  setBeforeSessionInvalidate(beforeSessionInvalidate) {
    this.beforeSessionInvalidate = beforeSessionInvalidate;
  }
  async emitBeforeSwitch(reason, targetSessionFile) {
    const runner = this.session.extensionRunner;
    if (!runner.hasHandlers("session_before_switch")) {
      return { cancelled: false };
    }
    const result = await runner.emit({
      type: "session_before_switch",
      reason,
      targetSessionFile
    });
    return { cancelled: result?.cancel === true };
  }
  async emitBeforeFork(entryId, options) {
    const runner = this.session.extensionRunner;
    if (!runner.hasHandlers("session_before_fork")) {
      return { cancelled: false };
    }
    const result = await runner.emit({
      type: "session_before_fork",
      entryId,
      ...options
    });
    return { cancelled: result?.cancel === true };
  }
  async teardownCurrent(reason, targetSessionFile) {
    await this.session.abort();
    await emitSessionShutdownEvent(this.session.extensionRunner, {
      type: "session_shutdown",
      reason,
      targetSessionFile
    });
    this.beforeSessionInvalidate?.();
    this.session.dispose();
  }
  apply(result) {
    this._session = result.session;
    this._services = result.services;
    this._diagnostics = result.diagnostics;
    this._modelFallbackMessage = result.modelFallbackMessage;
  }
  async finishSessionReplacement(withSession) {
    if (this.rebindSession) {
      await this.rebindSession(this.session);
    }
    if (withSession) {
      await withSession(this.session.createReplacedSessionContext());
    }
  }
  async switchSession(sessionPath, options) {
    const beforeResult = await this.emitBeforeSwitch("resume", sessionPath);
    if (beforeResult.cancelled) {
      return beforeResult;
    }
    const previousSessionFile = this.session.sessionFile;
    const sessionManager = SessionManager.open(sessionPath, void 0, options?.cwdOverride);
    assertSessionCwdExists(sessionManager, this.cwd);
    await this.teardownCurrent("resume", sessionManager.getSessionFile());
    this.apply(await this.createRuntime({
      cwd: sessionManager.getCwd(),
      agentDir: this.services.agentDir,
      sessionManager,
      sessionStartEvent: { type: "session_start", reason: "resume", previousSessionFile },
      projectTrustContext: options?.projectTrustContextFactory?.(sessionManager.getCwd())
    }));
    await this.finishSessionReplacement(options?.withSession);
    return { cancelled: false };
  }
  async newSession(options) {
    const beforeResult = await this.emitBeforeSwitch("new");
    if (beforeResult.cancelled) {
      return beforeResult;
    }
    const previousSessionFile = this.session.sessionFile;
    const sessionDir = this.session.sessionManager.getSessionDir();
    const sessionManager = this.session.sessionManager.isPersisted() ? SessionManager.create(this.cwd, sessionDir) : SessionManager.inMemory(this.cwd);
    if (options?.parentSession) {
      sessionManager.newSession({ parentSession: options.parentSession });
    }
    await this.teardownCurrent("new", sessionManager.getSessionFile());
    this.apply(await this.createRuntime({
      cwd: this.cwd,
      agentDir: this.services.agentDir,
      sessionManager,
      sessionStartEvent: { type: "session_start", reason: "new", previousSessionFile }
    }));
    if (options?.setup) {
      await options.setup(this.session.sessionManager);
      this.session.refreshContext();
    }
    await this.finishSessionReplacement(options?.withSession);
    return { cancelled: false };
  }
  async fork(entryId, options) {
    const position = options?.position ?? "before";
    const beforeResult = await this.emitBeforeFork(entryId, { position });
    if (beforeResult.cancelled) {
      return { cancelled: true };
    }
    let targetLeafId;
    let selectedText;
    const selectedEntry = this.session.sessionManager.getEntry(entryId);
    if (!selectedEntry) {
      throw new Error("Invalid entry ID for forking");
    }
    if (position === "at") {
      targetLeafId = selectedEntry.id;
    } else {
      if (selectedEntry.type !== "message" || selectedEntry.message.role !== "user") {
        throw new Error("Invalid entry ID for forking");
      }
      targetLeafId = selectedEntry.parentId;
      selectedText = extractUserMessageText(selectedEntry.message.content);
    }
    const previousSessionFile = this.session.sessionFile;
    if (this.session.sessionManager.isPersisted()) {
      const currentSessionFile = this.session.sessionFile;
      if (!currentSessionFile) {
        throw new Error("Persisted session is missing a session file");
      }
      const sessionDir = this.session.sessionManager.getSessionDir();
      if (!targetLeafId) {
        const sessionManager3 = SessionManager.create(this.cwd, sessionDir);
        sessionManager3.newSession({ parentSession: currentSessionFile });
        await this.teardownCurrent("fork", sessionManager3.getSessionFile());
        this.apply(await this.createRuntime({
          cwd: this.cwd,
          agentDir: this.services.agentDir,
          sessionManager: sessionManager3,
          sessionStartEvent: { type: "session_start", reason: "fork", previousSessionFile }
        }));
        await this.finishSessionReplacement(options?.withSession);
        return { cancelled: false, selectedText };
      }
      if (!existsSync13(currentSessionFile)) {
        throw new Error("This session has not been saved yet. Wait for the first assistant response before cloning or forking it.");
      }
      const sessionManager2 = SessionManager.open(currentSessionFile, sessionDir);
      const forkedSessionPath = sessionManager2.createBranchedSession(targetLeafId);
      if (!forkedSessionPath) {
        throw new Error("Failed to create forked session");
      }
      await this.teardownCurrent("fork", sessionManager2.getSessionFile());
      this.apply(await this.createRuntime({
        cwd: sessionManager2.getCwd(),
        agentDir: this.services.agentDir,
        sessionManager: sessionManager2,
        sessionStartEvent: { type: "session_start", reason: "fork", previousSessionFile }
      }));
      await this.finishSessionReplacement(options?.withSession);
      return { cancelled: false, selectedText };
    }
    const sessionManager = this.session.sessionManager;
    await this.teardownCurrent("fork", sessionManager.getSessionFile());
    if (!targetLeafId) {
      sessionManager.newSession({ parentSession: previousSessionFile });
    } else {
      sessionManager.createBranchedSession(targetLeafId);
    }
    this.apply(await this.createRuntime({
      cwd: this.cwd,
      agentDir: this.services.agentDir,
      sessionManager,
      sessionStartEvent: { type: "session_start", reason: "fork", previousSessionFile }
    }));
    await this.finishSessionReplacement(options?.withSession);
    return { cancelled: false, selectedText };
  }
  /**
   * Import a session JSONL file and switch runtime state to the imported session.
   *
   * @returns `{ cancelled: true }` when cancelled by `session_before_switch`, otherwise `{ cancelled: false }`.
   * @throws {SessionImportFileNotFoundError} When the input path does not exist.
   * @throws {MissingSessionCwdError} When the imported session cwd cannot be resolved and no override is provided.
   */
  async importFromJsonl(inputPath, cwdOverride) {
    const resolvedPath = resolvePath(inputPath);
    if (!existsSync13(resolvedPath)) {
      throw new SessionImportFileNotFoundError(resolvedPath);
    }
    const sessionDir = this.session.sessionManager.getSessionDir();
    if (!existsSync13(sessionDir)) {
      mkdirSync6(sessionDir, { recursive: true });
    }
    let destinationPath = join16(sessionDir, basename8(resolvedPath));
    const sourceAlreadyStored = resolve8(destinationPath) === resolvedPath;
    if (!sourceAlreadyStored) {
      const { name, ext } = parse2(destinationPath);
      let suffix = 1;
      while (existsSync13(destinationPath)) {
        destinationPath = join16(sessionDir, `${name}-${suffix++}${ext}`);
      }
    }
    const beforeResult = await this.emitBeforeSwitch("resume", destinationPath);
    if (beforeResult.cancelled) {
      return beforeResult;
    }
    const previousSessionFile = this.session.sessionFile;
    if (!sourceAlreadyStored) {
      copyFileSync(resolvedPath, destinationPath, constants.COPYFILE_EXCL);
    }
    const sessionManager = SessionManager.open(destinationPath, sessionDir, cwdOverride);
    assertSessionCwdExists(sessionManager, this.cwd);
    await this.teardownCurrent("resume", sessionManager.getSessionFile());
    this.apply(await this.createRuntime({
      cwd: sessionManager.getCwd(),
      agentDir: this.services.agentDir,
      sessionManager,
      sessionStartEvent: { type: "session_start", reason: "resume", previousSessionFile }
    }));
    await this.finishSessionReplacement();
    return { cancelled: false };
  }
  async dispose() {
    await emitSessionShutdownEvent(this.session.extensionRunner, {
      type: "session_shutdown",
      reason: "quit"
    });
    this.beforeSessionInvalidate?.();
    this.session.dispose();
  }
};
async function createAgentSessionRuntime(createRuntime, options) {
  assertSessionCwdExists(options.sessionManager, options.cwd);
  const result = await createRuntime(options);
  return new AgentSessionRuntime(result.session, result.services, createRuntime, result.diagnostics, result.modelFallbackMessage);
}
__name(createAgentSessionRuntime, "createAgentSessionRuntime");

// pi-dist/pi-coding-agent/core/cache-warmer.js
import { calculateCost } from "../../pi-ai/sdk-bundle/index.js";
import { getProviderEnvValue } from "../../pi-ai/utils/provider-env.js";
var MAX_WARMING_AGE_MS = 60 * 6e4;
var MAX_IDLE_WARMING_AGE_MS = 30 * 6e4;
var CACHE_WARMING_MINIMUM_EXPECTED_SAVINGS = 0.05;
var IDLE_CONTINUATION_PROBABILITY = 0.15;
function getCacheWarmingDelayMs(ttlMs) {
  if (ttlMs <= 1e4)
    return void 0;
  return Math.max(1, Math.floor(Math.min(ttlMs * 0.9, ttlMs - 1e4)));
}
__name(getCacheWarmingDelayMs, "getCacheWarmingDelayMs");
function getPromptCacheTtlMs(model, options) {
  const retention = options?.cacheRetention ?? (getProviderEnvValue("PI_CACHE_RETENTION", options?.env) === "long" ? "long" : "short");
  if (retention === "none")
    return void 0;
  const seconds = model.promptCache?.[retention];
  return seconds === void 0 ? void 0 : seconds * 1e3;
}
__name(getPromptCacheTtlMs, "getPromptCacheTtlMs");
function isReplayable(model, options) {
  if (!options?.reasoning || model.api !== "anthropic-messages")
    return true;
  return model.compat?.forceAdaptiveThinking === true;
}
__name(isReplayable, "isReplayable");
function lastPromptTokens(entries) {
  for (let index = entries.length - 1; index >= 0; index--) {
    const entry = entries[index];
    if (entry.type === "message" && entry.message.role === "assistant") {
      const usage = entry.message.usage;
      return usage.input + usage.cacheRead + usage.cacheWrite;
    }
  }
  return 0;
}
__name(lastPromptTokens, "lastPromptTokens");
function price(model, tokens) {
  const usage = {
    input: 0,
    output: 0,
    cacheRead: 0,
    cacheWrite: 0,
    totalTokens: 0,
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
    ...tokens
  };
  return calculateCost(model, usage).total;
}
__name(price, "price");
var CacheWarmer = class {
  static {
    __name(this, "CacheWarmer");
  }
  run;
  inactive;
  models;
  sessionManager;
  getMode;
  /** Lets extensions override `event.action`; failures fall back to pi's decision. */
  decide;
  /** Called with the persisted usage entry after each successful refresh. */
  onWarmed;
  constructor(models, sessionManager, getMode, decide = async (event) => event.action) {
    this.models = models;
    this.sessionManager = sessionManager;
    this.getMode = getMode;
    this.decide = decide;
    this.inactive = { state: "inactive", reason: "waiting for first request" };
  }
  get status() {
    if (this.getMode() === "off")
      return { state: "inactive", reason: "cache warming disabled" };
    const run = this.run;
    if (!run)
      return this.inactive;
    if (!run.isCurrent())
      return { state: "inactive", reason: "conversation context changed" };
    const decision = this.evaluate(run);
    const refreshing = run.timer === void 0;
    if (!decision.economicsAvailable && !refreshing) {
      return { state: "inactive", reason: "cache economics unavailable" };
    }
    return {
      state: refreshing ? "refreshing" : "scheduled",
      nextWarmAt: run.nextWarmAt,
      decision,
      extensionOverride: run.extensionOverride
    };
  }
  /** Keep the prompt cache entry written by `request` warm while `isCurrent` holds. */
  start(request, isCurrent) {
    this.clearRun();
    const mode = this.getMode();
    if (mode === "off") {
      this.stop("cache warming disabled");
      return;
    }
    if (!isReplayable(request.model, request.options)) {
      this.stop("request cannot be replayed safely");
      return;
    }
    const ttlMs = getPromptCacheTtlMs(request.model, request.options);
    if (ttlMs === void 0) {
      this.stop(request.options.cacheRetention === "none" ? "request disabled prompt caching" : "cache lifetime unavailable");
      return;
    }
    const delayMs = getCacheWarmingDelayMs(ttlMs);
    if (delayMs === void 0) {
      this.stop("cache lifetime unavailable");
      return;
    }
    this.run = {
      ...request,
      isCurrent,
      ttlMs,
      delayMs,
      refreshDeadlineAt: 0,
      startedAt: Date.now(),
      controller: new AbortController(),
      phase: "streaming",
      nextWarmAt: 0,
      extensionOverride: false
    };
    this.schedule(this.run);
  }
  onAgentSettled() {
    const run = this.run;
    if (!run)
      return;
    if (this.getMode() === "streaming") {
      this.stop("agent run settled");
      return;
    }
    run.phase = "idle";
    const deadline = run.startedAt + MAX_IDLE_WARMING_AGE_MS;
    if (run.nextWarmAt > deadline || Date.now() >= deadline) {
      this.stop("30-minute idle safety limit reached");
    }
  }
  /** Reconcile an active run after the persisted warming mode changes. */
  onModeChanged() {
    const run = this.run;
    if (!run)
      return;
    const reason = this.getModeStopReason(run);
    if (reason)
      this.stop(reason);
  }
  cancel() {
    this.stop("inactive");
  }
  clearRun() {
    const run = this.run;
    if (!run)
      return;
    this.run = void 0;
    if (run.timer)
      clearTimeout(run.timer);
    run.controller.abort();
  }
  stop(reason, stopped) {
    this.clearRun();
    this.inactive = { state: "inactive", reason, ...stopped };
  }
  schedule(run) {
    run.extensionOverride = false;
    run.nextWarmAt = Date.now() + run.delayMs;
    run.refreshDeadlineAt = run.nextWarmAt + Math.floor((run.ttlMs - run.delayMs) / 2);
    const deadline = run.startedAt + (run.phase === "idle" ? MAX_IDLE_WARMING_AGE_MS : MAX_WARMING_AGE_MS);
    if (run.nextWarmAt > deadline || Date.now() >= deadline) {
      this.stop(run.phase === "idle" ? "30-minute idle safety limit reached" : "one-hour safety limit reached");
      return;
    }
    run.timer = setTimeout(() => void this.refresh(run), Math.max(0, run.nextWarmAt - Date.now()));
    run.timer.unref?.();
  }
  async refresh(run) {
    run.timer = void 0;
    if (!this.validateRun(run))
      return;
    if (this.refreshDeadlineMissed(run))
      return;
    const decision = this.evaluate(run);
    const { warmCost, missCost, continuationProbability } = decision;
    let action = decision.action;
    try {
      action = await this.decide({
        type: "cache_warming_decision",
        warmCost,
        missCost,
        continuationProbability,
        action
      });
    } catch {
    }
    if (!this.validateRun(run) || this.refreshDeadlineMissed(run))
      return;
    const extensionOverride = action !== decision.action;
    if (action === "stop") {
      const reason = extensionOverride ? "stopped by extension" : decision.economicsAvailable ? "expected savings below threshold" : "cache economics unavailable";
      this.stop(reason, { decision, extensionOverride });
      return;
    }
    run.extensionOverride = extensionOverride;
    try {
      const message = await this.models.streamSimple(run.model, run.context, {
        ...run.options,
        maxTokens: 1,
        maxRetries: 0,
        signal: run.controller.signal
      }).result();
      if (!this.validateRun(run))
        return;
      if (message.stopReason !== "error" && message.stopReason !== "aborted") {
        const entry = this.sessionManager.appendUsage("cache_warm", message.provider, message.responseModel ?? message.model, message.usage, extensionOverride ? "extension override" : void 0);
        this.onWarmed?.(entry);
      }
    } catch {
    }
    if (this.run === run)
      this.schedule(run);
  }
  refreshDeadlineMissed(run) {
    if (Date.now() <= run.refreshDeadlineAt)
      return false;
    this.stop("cache refresh deadline missed");
    return true;
  }
  validateRun(run) {
    if (this.run !== run)
      return false;
    const reason = this.getModeStopReason(run) ?? (!run.isCurrent() ? "conversation context changed" : void 0);
    if (!reason)
      return true;
    this.stop(reason);
    return false;
  }
  getModeStopReason(run) {
    const mode = this.getMode();
    if (mode === "off")
      return "cache warming disabled";
    if (mode === "streaming" && run.phase === "idle")
      return "agent run settled";
    return void 0;
  }
  evaluate(run) {
    const model = run.model;
    const promptTokens = lastPromptTokens(this.sessionManager.getBranch());
    const cacheHitCost = price(model, { cacheRead: promptTokens });
    const cacheMissCost = price(model, model.cost.cacheWrite > 0 ? { cacheWrite: promptTokens } : { input: promptTokens });
    const warmCost = price(model, { cacheRead: promptTokens, output: 1 });
    const missCost = Math.max(0, cacheMissCost - cacheHitCost);
    const continuationProbability = run.phase === "idle" ? IDLE_CONTINUATION_PROBABILITY : 1;
    const economicsAvailable = promptTokens > 0 && (cacheHitCost > 0 || cacheMissCost > 0);
    const expectedSavings = continuationProbability * missCost - warmCost;
    return {
      phase: run.phase,
      warmCost,
      missCost,
      continuationProbability,
      expectedSavings,
      economicsAvailable,
      action: expectedSavings >= CACHE_WARMING_MINIMUM_EXPECTED_SAVINGS ? "warm" : "stop"
    };
  }
};
function formatDollars(value) {
  return value < 0 ? `-$${Math.abs(value).toFixed(3)}` : `$${value.toFixed(3)}`;
}
__name(formatDollars, "formatDollars");
function formatCacheWarmingEconomics(decision) {
  if (!decision.economicsAvailable)
    return "cache economics unavailable";
  const probability = Math.round(decision.continuationProbability * 100);
  const probabilityText = decision.phase === "streaming" ? `${probability}% continuation probability while agent is running` : `${probability}% continuation probability`;
  const comparison = decision.action === "warm" ? ">=" : "<";
  return `${probabilityText}, expected savings ${formatDollars(decision.expectedSavings)} ${comparison} $${CACHE_WARMING_MINIMUM_EXPECTED_SAVINGS.toFixed(3)}`;
}
__name(formatCacheWarmingEconomics, "formatCacheWarmingEconomics");
function formatCacheWarmingDecisionTime(nextWarmAt, now) {
  if (nextWarmAt === void 0 || nextWarmAt <= now)
    return "Decision now";
  let remainingSeconds = Math.ceil((nextWarmAt - now) / 1e3);
  const hours = Math.floor(remainingSeconds / 3600);
  remainingSeconds %= 3600;
  const minutes = Math.floor(remainingSeconds / 60);
  const seconds = remainingSeconds % 60;
  const parts = [];
  if (hours > 0)
    parts.push(`${hours}h`);
  if (minutes > 0)
    parts.push(`${minutes}m`);
  if (seconds > 0 || parts.length === 0)
    parts.push(`${seconds}s`);
  return `Decision in ${parts.join(" ")}`;
}
__name(formatCacheWarmingDecisionTime, "formatCacheWarmingDecisionTime");
function formatCacheWarmingStatus(status, now = Date.now()) {
  const decision = status.decision;
  if (!decision || status.state === "inactive" && !decision.economicsAvailable && !status.extensionOverride) {
    return `Inactive (${status.reason ?? "unknown reason"})`;
  }
  const details = status.extensionOverride ? `extension override, ${formatCacheWarmingEconomics(decision)}` : `${formatCacheWarmingEconomics(decision)} -> ${decision.action}`;
  if (status.state === "inactive")
    return `Stopped (${details})`;
  if (status.state === "refreshing")
    return `Warming cache (${details})`;
  return `${formatCacheWarmingDecisionTime(status.nextWarmAt, now)} (${details})`;
}
__name(formatCacheWarmingStatus, "formatCacheWarmingStatus");
function formatCacheWarmingUsage(entry) {
  const note = entry.note ? ` (${entry.note})` : "";
  const cost = entry.usage.cost.total.toFixed(6).replace(/(\.\d{3}\d*?)0+$/, "$1");
  return `Cache warmed${note}: $${cost}`;
}
__name(formatCacheWarmingUsage, "formatCacheWarmingUsage");

// pi-dist/pi-coding-agent/core/telemetry.js
function isTruthyEnvFlag(value) {
  if (!value)
    return false;
  return value === "1" || value.toLowerCase() === "true" || value.toLowerCase() === "yes";
}
__name(isTruthyEnvFlag, "isTruthyEnvFlag");
function isInstallTelemetryEnabled(settingsManager, telemetryEnv = process.env.PI_TELEMETRY) {
  return telemetryEnv !== void 0 ? isTruthyEnvFlag(telemetryEnv) : settingsManager.getEnableInstallTelemetry();
}
__name(isInstallTelemetryEnabled, "isInstallTelemetryEnabled");

export {
  normalizeSessionName,
  parseArgs,
  printHelp,
  parseFrontmatter,
  stripFrontmatter,
  formatNoModelsAvailableMessage,
  getPiUserAgent,
  createCompactionSummaryMessage,
  createCustomMessage,
  convertToLlm,
  CURRENT_SESSION_VERSION,
  assertValidSessionId,
  migrateSessionEntries,
  parseSessionEntries,
  getLatestCompactionEntry,
  sessionEntryToContextMessages,
  buildContextEntries,
  buildSessionProjection,
  buildSessionContext,
  getDefaultSessionDir,
  SessionManager,
  serializeConversation,
  DEFAULT_COMPACTION_SETTINGS,
  calculateContextTokens,
  getLastAssistantUsage,
  shouldCompact,
  estimateTokens,
  findTurnStartIndex,
  findCutPoint,
  generateSummary,
  generateSummaryWithUsage,
  compact,
  BUG_REPORT_CUSTOM_ENTRY_TYPE,
  collectBugReportMetadata,
  collectBugReportDiagnostics,
  bugReportFiles,
  writeBugReportArchive,
  bugReportArchiveFileName,
  collectEntriesForBranchSummary,
  prepareBranchEntries,
  generateBranchSummary,
  DEFAULT_THINKING_LEVEL,
  THINKING_LEVEL_OPTIONS,
  exportFromFile,
  createEventBus,
  createSyntheticSourceInfo,
  resetTimings,
  time,
  printTimings,
  createExtensionRuntime,
  discoverAndLoadExtensions,
  loadSkillsFromDir,
  formatSkillsForPrompt,
  loadSkills,
  emitProjectTrustEvent,
  ExtensionRunner,
  defineTool,
  isBashToolResult,
  isPowerShellToolResult,
  isReadToolResult,
  isEditToolResult,
  isWriteToolResult,
  isGrepToolResult,
  isFindToolResult,
  isLsToolResult,
  isToolCallEventType,
  wrapRegisteredTool,
  wrapRegisteredTools,
  ModelRegistry,
  serializeSessionBranch,
  exportSessionToJsonl,
  createUsageTotals,
  addUsageToTotals,
  getUsageCostBreakdown,
  parseSkillBlock,
  AgentSession,
  raceWithAbortSignal,
  ReadOnlyAuthStorage,
  AuthStorage,
  readStoredCredential,
  defaultModelPerProvider,
  findExactModelReferenceMatch,
  resolveModelScopeFromModels,
  resolveModelScopeWithDiagnostics,
  resolveModelScope,
  resolveCliModel,
  findInitialModel,
  InMemoryCodingAgentModelsStore,
  CredentialSynchronizationError,
  ModelRuntime,
  parseGitUrl,
  takeOverStdout,
  restoreStdout,
  writeRawStdout,
  waitForRawStdoutBackpressure,
  flushRawStdout,
  DefaultPackageManager,
  FooterDataProvider,
  HTTP_IDLE_TIMEOUT_CHOICES,
  formatHttpIdleTimeoutMs,
  applyHttpProxySettings,
  configureHttpDispatcher,
  CACHE_WARMING_MODES,
  SettingsManager,
  loadProjectContextFiles,
  DefaultResourceLoader,
  getMissingSessionCwdIssue,
  formatMissingSessionCwdPrompt,
  MissingSessionCwdError,
  createAgentSessionServices,
  createAgentSessionFromServices,
  SessionImportFileNotFoundError,
  AgentSessionRuntime,
  createAgentSessionRuntime,
  CacheWarmer,
  formatCacheWarmingStatus,
  formatCacheWarmingUsage,
  isInstallTelemetryEnabled
};
