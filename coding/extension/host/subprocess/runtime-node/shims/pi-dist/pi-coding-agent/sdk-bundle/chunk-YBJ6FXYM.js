import {
  ProjectTrustStore,
  addMcpServerConfig,
  loadMcpConfig,
  openBrowser,
  questionTerminal,
  removeMcpServerConfig
} from "./chunk-KBAOTQS2.js";
import {
  ModelRuntime
} from "./chunk-H2BMMKZ4.js";
import "./chunk-PNTN457B.js";
import {
  McpOAuthCredentialStore,
  McpServerConnection,
  McpServerLog,
  McpSignInCancelledError,
  createDefaultTransport,
  signInMcpServer
} from "./chunk-KHPK5VRF.js";
import {
  getMcpToolExposure,
  validateMcpServerConfig
} from "./chunk-H3JRSATQ.js";
import "./chunk-BXLOEZ7M.js";
import {
  APP_NAME,
  CONFIG_DIR_NAME
} from "./chunk-JGRZB36T.js";
import {
  __name
} from "./chunk-FIOAHQVQ.js";

// pi-dist/pi-coding-agent/extensions/mcp/cli.js
import { existsSync } from "node:fs";
import { join } from "node:path";
import chalk from "../../../chalk/source/index.js";
var HELP = `${chalk.bold("Usage:")}
  ${APP_NAME} mcp add <server> [options] -- <command> [args...]
  ${APP_NAME} mcp add <server> [options] --url <url>
  ${APP_NAME} mcp remove <server> [-l]
  ${APP_NAME} mcp list [--json]
  ${APP_NAME} mcp login <server> [-l] [--timeout <seconds>]
  ${APP_NAME} mcp logout <server> [-l]

Configure and check MCP servers and sign in to OAuth servers without starting a session.
Reads ~/${CONFIG_DIR_NAME}/agent/mcp.json and, in trusted projects, ${CONFIG_DIR_NAME}/mcp.json.

Commands:
  add <server>            Add or replace a server in mcp.json
  remove <server>         Remove a server from mcp.json
  list                    Show state, tools, and errors (exits 1 on failure)
  login <server>          Sign in through the browser
  logout <server>         Delete the stored OAuth credentials

Options for add, remove, login, and logout:
  -l, --local             Use ${CONFIG_DIR_NAME}/mcp.json in the current project instead of the global file

Options for add:
  --url <url>             Streamable HTTP server URL (instead of a command)
  --env <KEY=VALUE>       Environment variable for a stdio server (repeatable)
  --cwd <dir>             Working directory for a stdio server
  --header <KEY=VALUE>    HTTP header (repeatable)
  --bearer-token-env-var <NAME>
                          Send "Authorization: Bearer \${NAME}"
  --oauth-client-id <id>  Pre-registered OAuth client id
  --oauth-client-secret <secret>
                          OAuth client secret (may be \${NAME} or !command)
  --oauth-callback-port <port>
                          Fixed OAuth callback port
  --oauth-client-name <name>
                          Client name sent when registering with the OAuth server
  --oauth-client-registration <mode>
                          auto (default), cimd, or dcr
  --exposure <mode>       codemode (default), deferred, direct, or hidden
  --description <text>    What the server offers, shown in the system prompt

Other options:
  --json                  Print the list as JSON
  --timeout <seconds>     Time limit for the whole sign-in (default: 300)`;
var HELP_HINT = chalk.dim(`Use "${APP_NAME} mcp --help" for usage.`);
var DEFAULT_LOGIN_TIMEOUT_SECONDS = 300;
function errorMessage(error) {
  return error instanceof Error ? error.message : String(error);
}
__name(errorMessage, "errorMessage");
function describeTransport(entry) {
  const { config } = entry;
  return "url" in config ? config.url : [config.command, ...config.args ?? []].join(" ");
}
__name(describeTransport, "describeTransport");
function getAuthProvider(entry) {
  return "url" in entry.config ? entry.config.auth?.provider : void 0;
}
__name(getAuthProvider, "getAuthProvider");
function createConnection(entry, options, credentials, providerToken) {
  return new McpServerConnection({
    entry,
    cwd: options.cwd,
    createTransport: createDefaultTransport,
    credentials,
    providerToken,
    log: new McpServerLog(join(options.agentDir, "mcp.log")),
    onTools: /* @__PURE__ */ __name(() => {
    }, "onTools")
  });
}
__name(createConnection, "createConnection");
function createProviderTokenReader(agentDir) {
  let runtime;
  return async (provider) => {
    runtime ??= ModelRuntime.create({
      authPath: join(agentDir, "auth.json"),
      modelsPath: join(agentDir, "models.json"),
      refreshOnCreate: false
    });
    try {
      return (await (await runtime).getAuth(provider))?.auth.apiKey;
    } catch {
      return void 0;
    }
  };
}
__name(createProviderTokenReader, "createProviderTokenReader");
var OPTION_ALIASES = /* @__PURE__ */ new Map([["-l", "--local"]]);
function parseOptions(args, known, error, maxPositionals = Number.POSITIVE_INFINITY) {
  const positional = [];
  const values = /* @__PURE__ */ new Map();
  const lists = /* @__PURE__ */ new Map();
  for (let index = 0; index < args.length; index++) {
    const typed = args[index];
    const arg = OPTION_ALIASES.get(typed) ?? typed;
    if (arg === "--" || positional.length >= maxPositionals) {
      positional.push(...args.slice(arg === "--" ? index + 1 : index));
      break;
    }
    if (!arg.startsWith("--")) {
      positional.push(arg);
      continue;
    }
    const name = arg.slice(2);
    const kind = known[name];
    if (!kind) {
      error(`Unknown option ${typed}.
${HELP_HINT}`);
      return void 0;
    }
    if (kind === "flag") {
      values.set(name, true);
      continue;
    }
    const value = args[++index];
    if (value === void 0) {
      error(`${arg} needs a value.`);
      return void 0;
    }
    if (kind === "list")
      lists.set(name, [...lists.get(name) ?? [], value]);
    else
      values.set(name, value);
  }
  return { positional, values, lists };
}
__name(parseOptions, "parseOptions");
async function runMcpCommand(args, options) {
  const log = options.log ?? ((line) => console.log(line));
  const error = options.error ?? ((line) => console.error(line));
  const [command, ...rest] = args;
  if (command === void 0 || command === "help" || args.includes("--help") || args.includes("-h")) {
    log(HELP);
    return 0;
  }
  const projectConfig = join(options.cwd, CONFIG_DIR_NAME, "mcp.json");
  if (command === "add" || command === "remove") {
    return command === "add" ? add(rest, projectConfig, options, log, error) : remove(rest, projectConfig, options, log, error);
  }
  const projectTrusted = new ProjectTrustStore(options.agentDir).get(options.cwd) === true;
  const loaded = loadMcpConfig({ agentDir: options.agentDir, cwd: options.cwd, projectTrusted });
  const untrustedNote = !projectTrusted && existsSync(projectConfig) ? `${projectConfig} is ignored because the project is not trusted. Start ${APP_NAME} in the project to trust it.` : void 0;
  const credentials = options.credentials ?? new McpOAuthCredentialStore();
  const providerToken = options.providerToken ?? createProviderTokenReader(options.agentDir);
  switch (command) {
    case "list": {
      const parsed = parseOptions(rest, { json: "flag" }, error);
      if (!parsed)
        return 1;
      if (parsed.positional.length > 0) {
        error(`Usage: ${APP_NAME} mcp list [--json]
${HELP_HINT}`);
        return 1;
      }
      return list(loaded, parsed.values.has("json"), untrustedNote, options, credentials, providerToken, log);
    }
    case "login":
    case "logout": {
      const parsed = parseOptions(rest, command === "login" ? { local: "flag", timeout: "value" } : { local: "flag" }, error);
      if (!parsed)
        return 1;
      const [name, ...extra] = parsed.positional;
      if (!name || extra.length > 0) {
        error(`Usage: ${APP_NAME} mcp ${command} <server> [-l]
${HELP_HINT}`);
        return 1;
      }
      const entry = loaded.servers.find((server) => server.name === name);
      if (!entry) {
        const names = loaded.servers.map((server) => server.name);
        const suggestion = names.find((candidate) => editDistance(candidate, name) <= 2);
        error(`No MCP server named "${name}".${suggestion ? ` Did you mean "${suggestion}"?` : ""}${untrustedNote ? ` ${untrustedNote}` : ""} Configured: ${names.join(", ") || "none"}.`);
        return 1;
      }
      if (parsed.values.has("local") && entry.scope !== "project") {
        error(`MCP server "${name}" is defined in ${entry.source}, not in the project; omit --local.`);
        return 1;
      }
      const authProvider = getAuthProvider(entry);
      if (authProvider) {
        error(`MCP server "${name}" uses the credentials of provider "${authProvider}". Run "${APP_NAME} auth ${command} ${authProvider}".`);
        return 1;
      }
      const connection = createConnection(entry, options, credentials, providerToken);
      const url = connection.oauthUrl;
      if (!url) {
        error(`MCP server "${name}" does not use OAuth. Only HTTP servers without an Authorization header do.`);
        return 1;
      }
      if (command === "logout") {
        const removed = credentials.remove(name, url);
        log(removed ? `Signed out of MCP server "${name}".` : `No stored credentials for MCP server "${name}".`);
        return 0;
      }
      const timeout = Number(parsed.values.get("timeout") ?? DEFAULT_LOGIN_TIMEOUT_SECONDS);
      if (!Number.isFinite(timeout) || timeout <= 0) {
        error("--timeout must be a positive number of seconds.");
        return 1;
      }
      try {
        return await login(entry, connection, url, timeout * 1e3, options, credentials, log, error);
      } finally {
        await connection.close();
      }
    }
    default:
      error(`Unknown mcp command "${command}".
${HELP_HINT}`);
      return 1;
  }
}
__name(runMcpCommand, "runMcpCommand");
function editDistance(a, b) {
  let previous = Array.from({ length: b.length + 1 }, (_, index) => index);
  for (let i = 1; i <= a.length; i++) {
    const current = [i];
    for (let j = 1; j <= b.length; j++) {
      current[j] = Math.min(previous[j] + 1, current[j - 1] + 1, previous[j - 1] + (a[i - 1] === b[j - 1] ? 0 : 1));
    }
    previous = current;
  }
  return previous[b.length];
}
__name(editDistance, "editDistance");
function parsePairs(option, pairs, error) {
  if (!pairs)
    return {};
  const record = {};
  for (const pair of pairs) {
    const separator = pair.indexOf("=");
    if (separator <= 0) {
      error(`--${option} expects KEY=VALUE, got "${pair}".`);
      return void 0;
    }
    record[pair.slice(0, separator)] = pair.slice(separator + 1);
  }
  return record;
}
__name(parsePairs, "parsePairs");
function add(args, projectConfig, options, log, error) {
  const usage = `Usage: ${APP_NAME} mcp add <server> [options] (--url <url> | -- <command> [args...])
${HELP_HINT}`;
  const parsed = parseOptions(args, {
    local: "flag",
    url: "value",
    env: "list",
    cwd: "value",
    header: "list",
    "bearer-token-env-var": "value",
    "oauth-client-id": "value",
    "oauth-client-secret": "value",
    "oauth-callback-port": "value",
    "oauth-client-name": "value",
    "oauth-client-registration": "value",
    exposure: "value",
    description: "value"
  }, error, 2);
  if (!parsed)
    return 1;
  const { positional, values, lists } = parsed;
  const [name, ...command] = positional;
  const url = values.get("url");
  if (!name || url === void 0 === (command.length === 0)) {
    error(usage);
    return 1;
  }
  const value = /* @__PURE__ */ __name((option) => {
    const found = values.get(option);
    return typeof found === "string" ? found : void 0;
  }, "value");
  const exposure = value("exposure");
  const httpOnly = [
    "header",
    "bearer-token-env-var",
    "oauth-client-id",
    "oauth-client-secret",
    "oauth-callback-port",
    "oauth-client-name",
    "oauth-client-registration"
  ];
  const stdioOnly = ["env", "cwd"];
  const misplaced = (url === void 0 ? httpOnly : stdioOnly).find((option) => values.has(option) || lists.has(option));
  if (misplaced) {
    error(`--${misplaced} only applies to ${url === void 0 ? "HTTP servers (--url)" : "stdio servers"}.`);
    return 1;
  }
  let config;
  if (typeof url === "string") {
    const headers = parsePairs("header", lists.get("header"), error);
    if (!headers)
      return 1;
    const bearer = value("bearer-token-env-var");
    if (bearer !== void 0)
      headers.Authorization = `Bearer \${${bearer}}`;
    const port = value("oauth-callback-port");
    const oauth = {
      ...value("oauth-client-id") === void 0 ? {} : { clientId: value("oauth-client-id") },
      ...value("oauth-client-secret") === void 0 ? {} : { clientSecret: value("oauth-client-secret") },
      ...port === void 0 ? {} : { callbackPort: Number(port) },
      ...value("oauth-client-name") === void 0 ? {} : { clientName: value("oauth-client-name") },
      ...value("oauth-client-registration") === void 0 ? {} : { clientRegistration: value("oauth-client-registration") }
    };
    config = {
      url,
      ...Object.keys(headers).length > 0 ? { headers } : {},
      ...Object.keys(oauth).length > 0 ? { oauth } : {}
    };
  } else {
    const env = parsePairs("env", lists.get("env"), error);
    if (!env)
      return 1;
    const [executable, ...commandArgs] = command;
    config = {
      command: executable,
      ...commandArgs.length > 0 ? { args: commandArgs } : {},
      ...Object.keys(env).length > 0 ? { env } : {},
      ...value("cwd") === void 0 ? {} : { cwd: value("cwd") }
    };
  }
  if (exposure !== void 0)
    config.exposure = exposure;
  const description = value("description");
  if (description !== void 0)
    config.description = description;
  const validated = validateMcpServerConfig(name, config);
  if (typeof validated === "string") {
    error(validated);
    return 1;
  }
  const project = values.has("local");
  const path = project ? projectConfig : join(options.agentDir, "mcp.json");
  const scope = project ? "project" : "global";
  let replaced;
  try {
    replaced = addMcpServerConfig(path, name, validated);
  } catch (addError) {
    error(`Could not update ${path}: ${errorMessage(addError)}`);
    return 1;
  }
  log(`${replaced ? "Replaced" : "Added"} ${scope} MCP server "${name}" in ${path}.`);
  if (project && new ProjectTrustStore(options.agentDir).get(options.cwd) !== true) {
    log(`The project is not trusted, so ${path} is ignored until you start ${APP_NAME} in the project and trust it.`);
  }
  const mayNeedSignIn = "url" in validated && !Object.keys(validated.headers ?? {}).some((header) => header.toLowerCase() === "authorization");
  log(`Check it with: ${APP_NAME} mcp list${mayNeedSignIn ? `. If it requires sign-in: ${APP_NAME} mcp login ${name}` : ""}`);
  return 0;
}
__name(add, "add");
function remove(args, projectConfig, options, log, error) {
  const parsed = parseOptions(args, { local: "flag" }, error);
  if (!parsed)
    return 1;
  const [name, ...extra] = parsed.positional;
  if (!name || extra.length > 0) {
    error(`Usage: ${APP_NAME} mcp remove <server> [-l]
${HELP_HINT}`);
    return 1;
  }
  const project = parsed.values.has("local");
  const globalConfig = join(options.agentDir, "mcp.json");
  const path = project ? projectConfig : globalConfig;
  const scope = project ? "project" : "global";
  let removed;
  try {
    removed = removeMcpServerConfig(path, name);
  } catch (removeError) {
    error(`Could not update ${path}: ${errorMessage(removeError)}`);
    return 1;
  }
  if (removed) {
    log(`Removed ${scope} MCP server "${name}" from ${path}.`);
    return 0;
  }
  const other = loadMcpConfig({ agentDir: options.agentDir, cwd: options.cwd, projectTrusted: true }).servers.find((server) => server.name === name && server.scope !== scope);
  error(`No ${scope} MCP server named "${name}" in ${path}.${other ? ` It is defined in ${other.source}${other.scope === "project" ? "; use --local" : "; omit --local"}.` : ""}`);
  return 1;
}
__name(remove, "remove");
async function list(loaded, json, untrustedNote, options, credentials, providerToken, log) {
  const reports = await Promise.all(loaded.servers.map(async (entry) => {
    const authProvider = getAuthProvider(entry);
    const report = {
      name: entry.name,
      scope: entry.scope ?? "global",
      source: entry.source,
      ...entry.override ? { override: entry.override } : {},
      enabled: entry.config.enabled !== false,
      exposure: entry.config.exposure ?? "codemode",
      transport: describeTransport(entry),
      ...authProvider ? { authProvider } : {},
      state: "disabled",
      tools: []
    };
    if (!report.enabled)
      return report;
    const connection = createConnection(entry, options, credentials, providerToken);
    try {
      await connection.getClient();
    } catch {
    }
    report.state = connection.state;
    report.tools = connection.tools.map((tool) => tool.name);
    const overrides = connection.tools.flatMap((tool) => {
      const exposure = getMcpToolExposure(entry.config, tool.name);
      return exposure === report.exposure ? [] : [[tool.name, exposure]];
    });
    if (overrides.length > 0)
      report.toolExposure = Object.fromEntries(overrides);
    if (connection.hasResources)
      report.resources = true;
    if (connection.state !== "connected" && connection.error)
      report.error = connection.error;
    await connection.close();
    return report;
  }));
  const failed = loaded.errors.length > 0 || reports.some((report) => report.enabled && report.state !== "connected");
  if (json) {
    log(JSON.stringify({ servers: reports, errors: loaded.errors, ...untrustedNote ? { note: untrustedNote } : {} }, null, 2));
    return failed ? 1 : 0;
  }
  if (reports.length === 0 && loaded.errors.length === 0) {
    log(`No MCP servers configured. Add them to ${join(options.agentDir, "mcp.json")} or .pi/mcp.json.`);
  }
  for (const report of reports) {
    const state = report.state === "connected" ? `connected, ${report.tools.length} tool${report.tools.length === 1 ? "" : "s"}` : report.state === "needs-auth" ? "needs sign-in" : report.state;
    log(`${report.name}: ${state} (${report.exposure}, ${report.scope})`);
    log(`  ${report.transport}`);
    if (report.override)
      log(`  project override: ${report.override}`);
    if (report.state === "needs-auth") {
      log(report.authProvider ? `  sign in with: /login ${report.authProvider} in ${APP_NAME}` : `  sign in with: ${APP_NAME} mcp login ${report.name}`);
    }
    if (report.tools.length > 0) {
      const tools = report.tools.map((tool) => {
        const exposure = report.toolExposure?.[tool];
        return exposure ? `${tool} [${exposure}]` : tool;
      });
      log(`  tools: ${tools.join(", ")}`);
    }
    if (report.resources)
      log("  offers resources");
    if (report.error)
      log(`  ${report.error.split("\n").join("\n  ")}`);
  }
  for (const configError of loaded.errors)
    log(`config error: ${configError}`);
  if (untrustedNote)
    log(untrustedNote);
  return failed ? 1 : 0;
}
__name(list, "list");
async function login(entry, connection, url, timeoutMs, options, credentials, log, error) {
  const { name } = entry;
  try {
    await connection.getClient();
    log(`Already signed in to MCP server "${name}" (${connection.tools.length} tools).`);
    return 0;
  } catch {
    if (connection.state !== "needs-auth") {
      error(`MCP server "${name}" failed to connect: ${connection.error ?? "unknown error"}`);
      return 1;
    }
  }
  const openUrl = options.openUrl ?? openBrowser;
  const interactive = process.stdin.isTTY === true && options.openUrl === void 0;
  try {
    await signInMcpServer({
      serverUrl: url,
      store: credentials.forServer(name, url),
      settings: connection.oauthSettings(),
      challenge: connection.challenge,
      prompt: {
        showAuthorizationUrl: /* @__PURE__ */ __name((authorizationUrl) => {
          log(`Sign in to MCP server "${name}" in your browser:
${authorizationUrl.href}`);
          openUrl(authorizationUrl.href);
        }, "showAuthorizationUrl"),
        promptForRedirectUrl: /* @__PURE__ */ __name((signal) => waitForRedirectUrl(signal, interactive), "promptForRedirectUrl")
      },
      signal: AbortSignal.timeout(timeoutMs)
    });
  } catch (signInError) {
    error(signInError instanceof McpSignInCancelledError ? `Sign-in to MCP server "${name}" was cancelled or not completed within ${Math.round(timeoutMs / 1e3)} seconds.` : `Sign-in to MCP server "${name}" failed: ${errorMessage(signInError)}`);
    return 1;
  }
  connection.challenge = void 0;
  try {
    await connection.reconnect();
  } catch (connectError) {
    error(`Signed in, but ${errorMessage(connectError)}`);
    return 1;
  }
  log(`Signed in to MCP server "${name}" (${connection.tools.length} tools).`);
  return 0;
}
__name(login, "login");
async function waitForRedirectUrl(signal, interactive) {
  if (!interactive) {
    if (!signal.aborted)
      await new Promise((resolve) => signal.addEventListener("abort", resolve, { once: true }));
    return void 0;
  }
  try {
    return await questionTerminal("If the browser cannot reach this machine, paste the URL it was redirected to: ", {
      signal,
      output: process.stderr
    });
  } catch {
    return void 0;
  }
}
__name(waitForRedirectUrl, "waitForRedirectUrl");
export {
  runMcpCommand
};
