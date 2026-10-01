import {
  mcpNamespace,
  validateMcpServerConfig
} from "./chunk-F4GPDE7R.js";
import {
  CONFIG_DIR_NAME,
  canonicalizePath,
  resolvePath,
  stripBom
} from "./chunk-CBPXJ43O.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-coding-agent/core/trust-manager.js
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join } from "node:path";
import lockfile from "../../../proper-lockfile.mjs";
var TRUST_REQUIRING_PROJECT_CONFIG_RESOURCES = [
  "settings.json",
  "mcp.json",
  "extensions",
  "skills",
  "prompts",
  "themes",
  "SYSTEM.md",
  "APPEND_SYSTEM.md"
];
function normalizeCwd(cwd) {
  return canonicalizePath(resolvePath(cwd));
}
__name(normalizeCwd, "normalizeCwd");
function findNearestTrustEntry(data, cwd) {
  let currentDir = normalizeCwd(cwd);
  while (true) {
    const value = data[currentDir];
    if (value === true || value === false) {
      return { path: currentDir, decision: value };
    }
    const parentDir = dirname(currentDir);
    if (parentDir === currentDir) {
      return null;
    }
    currentDir = parentDir;
  }
}
__name(findNearestTrustEntry, "findNearestTrustEntry");
function getProjectTrustParentPath(cwd) {
  const trustPath = normalizeCwd(cwd);
  const parentDir = dirname(trustPath);
  return parentDir === trustPath ? void 0 : parentDir;
}
__name(getProjectTrustParentPath, "getProjectTrustParentPath");
function getProjectTrustOptions(cwd, options) {
  const trustPath = normalizeCwd(cwd);
  const trustOptions = [
    { label: "Trust", trusted: true, updates: [{ path: trustPath, decision: true }], savedPath: trustPath }
  ];
  const parentPath = getProjectTrustParentPath(cwd);
  if (parentPath !== void 0) {
    trustOptions.push({
      label: `Trust parent folder (${parentPath})`,
      trusted: true,
      updates: [
        { path: parentPath, decision: true },
        { path: trustPath, decision: null }
      ],
      savedPath: parentPath
    });
  }
  if (options?.includeSessionOnly) {
    trustOptions.push({ label: "Trust (this session only)", trusted: true, updates: [] });
  }
  trustOptions.push({
    label: "Do not trust",
    trusted: false,
    updates: [{ path: trustPath, decision: false }],
    savedPath: trustPath
  });
  if (options?.includeSessionOnly) {
    trustOptions.push({ label: "Do not trust (this session only)", trusted: false, updates: [] });
  }
  return trustOptions;
}
__name(getProjectTrustOptions, "getProjectTrustOptions");
function readTrustFile(path) {
  if (!existsSync(path)) {
    return {};
  }
  let parsed;
  try {
    parsed = JSON.parse(stripBom(readFileSync(path, "utf-8")));
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    throw new Error(`Failed to read trust store ${path}: ${message}`);
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    throw new Error(`Invalid trust store ${path}: expected an object`);
  }
  const data = {};
  for (const [key, value] of Object.entries(parsed)) {
    if (value !== true && value !== false && value !== null) {
      throw new Error(`Invalid trust store ${path}: value for ${JSON.stringify(key)} must be true, false, or null`);
    }
    data[key] = value;
  }
  return data;
}
__name(readTrustFile, "readTrustFile");
function writeTrustFile(path, data) {
  const sorted = {};
  for (const key of Object.keys(data).sort()) {
    const value = data[key];
    if (value === true || value === false || value === null) {
      sorted[key] = value;
    }
  }
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, `${JSON.stringify(sorted, null, 2)}
`, "utf-8");
}
__name(writeTrustFile, "writeTrustFile");
function acquireTrustLockSync(path) {
  const trustDir = dirname(path);
  mkdirSync(trustDir, { recursive: true });
  const maxAttempts = 10;
  const delayMs = 20;
  let lastError;
  for (let attempt = 1; attempt <= maxAttempts; attempt++) {
    try {
      return lockfile.lockSync(trustDir, { realpath: false, lockfilePath: `${path}.lock` });
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
  if (lastError instanceof Error) {
    throw lastError;
  }
  throw new Error("Failed to acquire trust store lock");
}
__name(acquireTrustLockSync, "acquireTrustLockSync");
function withTrustFileLock(path, fn) {
  const release = acquireTrustLockSync(path);
  try {
    return fn();
  } finally {
    release();
  }
}
__name(withTrustFileLock, "withTrustFileLock");
function hasTrustRequiringProjectResources(cwd) {
  const homeDir = canonicalizePath(resolvePath(process.env.HOME || homedir()));
  const userAgentsSkillsDir = join(homeDir, ".agents", "skills");
  let currentDir = canonicalizePath(resolvePath(cwd));
  const configDir = join(currentDir, CONFIG_DIR_NAME);
  if (TRUST_REQUIRING_PROJECT_CONFIG_RESOURCES.some((entry) => existsSync(join(configDir, entry)))) {
    return true;
  }
  while (true) {
    const agentsSkillsDir = join(currentDir, ".agents", "skills");
    if (agentsSkillsDir !== userAgentsSkillsDir && existsSync(agentsSkillsDir)) {
      return true;
    }
    const parentDir = dirname(currentDir);
    if (parentDir === currentDir) {
      return false;
    }
    currentDir = parentDir;
  }
}
__name(hasTrustRequiringProjectResources, "hasTrustRequiringProjectResources");
var ProjectTrustStore = class {
  static {
    __name(this, "ProjectTrustStore");
  }
  trustPath;
  constructor(agentDir) {
    this.trustPath = join(resolvePath(agentDir), "trust.json");
  }
  get(cwd) {
    return this.getEntry(cwd)?.decision ?? null;
  }
  getEntry(cwd) {
    return withTrustFileLock(this.trustPath, () => {
      const data = readTrustFile(this.trustPath);
      return findNearestTrustEntry(data, cwd);
    });
  }
  set(cwd, decision) {
    this.setMany([{ path: cwd, decision }]);
  }
  setMany(decisions) {
    withTrustFileLock(this.trustPath, () => {
      const data = readTrustFile(this.trustPath);
      for (const { path, decision } of decisions) {
        const key = normalizeCwd(path);
        if (decision === null) {
          delete data[key];
        } else {
          data[key] = decision;
        }
      }
      writeTrustFile(this.trustPath, data);
    });
  }
};

// pi-dist/pi-coding-agent/utils/open-browser.js
import { spawn } from "node:child_process";
function openBrowser(target) {
  const [cmd, args] = process.platform === "darwin" ? ["open", [target]] : process.platform === "win32" ? ["rundll32", ["url.dll,FileProtocolHandler", target]] : ["xdg-open", [target]];
  spawn(cmd, args, { stdio: "ignore", detached: true }).on("error", () => {
  }).unref();
}
__name(openBrowser, "openBrowser");

// pi-dist/pi-coding-agent/extensions/mcp/config.js
import { existsSync as existsSync2, mkdirSync as mkdirSync2, readFileSync as readFileSync2, writeFileSync as writeFileSync2 } from "node:fs";
import { dirname as dirname2, join as join2 } from "node:path";
function isRecord(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
__name(isRecord, "isRecord");
function readConfigFile(path, scope, state) {
  const { servers, errors } = state;
  if (!existsSync2(path))
    return;
  let parsed;
  try {
    parsed = JSON.parse(readFileSync2(path, "utf8"));
  } catch (error) {
    errors.push(`${path}: ${error instanceof Error ? error.message : String(error)}`);
    return;
  }
  if (!isRecord(parsed) || parsed.mcpServers !== void 0 && !isRecord(parsed.mcpServers)) {
    errors.push(`${path}: expected an object with an "mcpServers" object`);
    return;
  }
  if (typeof parsed.autoEnableCodemode === "boolean")
    state.autoEnableCodemode = parsed.autoEnableCodemode;
  else if (parsed.autoEnableCodemode !== void 0)
    errors.push(`${path}: autoEnableCodemode must be a boolean`);
  for (const [name, value] of Object.entries(parsed.mcpServers ?? {})) {
    const config = validateMcpServerConfig(name, value);
    if (typeof config === "string") {
      errors.push(`${path}: ${config}`);
      continue;
    }
    const clash = [...servers.keys()].find((other) => other !== name && mcpNamespace(other) === mcpNamespace(name));
    if (clash) {
      errors.push(`${path}: server "${name}" conflicts with "${clash}"`);
      continue;
    }
    if (scope === "project" && "url" in config && config.auth) {
      errors.push(`${path}: server "${name}": auth is only allowed in the global mcp.json`);
      continue;
    }
    servers.set(name, { name, config, source: path, scope });
  }
}
__name(readConfigFile, "readConfigFile");
function loadMcpConfig(options) {
  const state = { servers: /* @__PURE__ */ new Map(), errors: [] };
  readConfigFile(join2(options.agentDir, "mcp.json"), "global", state);
  if (options.projectTrusted)
    readConfigFile(join2(options.cwd, CONFIG_DIR_NAME, "mcp.json"), "project", state);
  return {
    servers: [...state.servers.values()],
    ...state.autoEnableCodemode === void 0 ? {} : { autoEnableCodemode: state.autoEnableCodemode },
    errors: state.errors
  };
}
__name(loadMcpConfig, "loadMcpConfig");
function updateMcpServerConfig(path, name, patch) {
  editMcpServers(path, (servers) => {
    const server = servers?.[name];
    if (!isRecord(server))
      throw new Error(`${path} does not define MCP server "${name}"`);
    if (patch.enabled !== void 0) {
      if (patch.enabled)
        delete server.enabled;
      else
        server.enabled = false;
    }
    if (patch.exposure !== void 0) {
      if (patch.exposure === "codemode")
        delete server.exposure;
      else
        server.exposure = patch.exposure;
    }
    return true;
  });
}
__name(updateMcpServerConfig, "updateMcpServerConfig");
function addMcpServerConfig(path, name, config) {
  let replaced = false;
  editMcpServers(path, (servers, parsed) => {
    const target = servers ?? {};
    replaced = target[name] !== void 0;
    target[name] = config;
    parsed.mcpServers = target;
    return true;
  });
  return replaced;
}
__name(addMcpServerConfig, "addMcpServerConfig");
function removeMcpServerConfig(path, name) {
  if (!existsSync2(path))
    return false;
  let removed = false;
  editMcpServers(path, (servers) => {
    if (!servers || servers[name] === void 0)
      return false;
    delete servers[name];
    removed = true;
    return true;
  });
  return removed;
}
__name(removeMcpServerConfig, "removeMcpServerConfig");
function editMcpServers(path, edit) {
  const text = existsSync2(path) ? readFileSync2(path, "utf8") : void 0;
  const parsed = text === void 0 ? {} : JSON.parse(text);
  if (!isRecord(parsed) || parsed.mcpServers !== void 0 && !isRecord(parsed.mcpServers)) {
    throw new Error(`${path}: expected an object with an "mcpServers" object`);
  }
  const servers = isRecord(parsed.mcpServers) ? parsed.mcpServers : void 0;
  if (!edit(servers, parsed))
    return;
  const indent = text && /^([ \t]+)\S/m.exec(text)?.[1] || "  ";
  mkdirSync2(dirname2(path), { recursive: true });
  writeFileSync2(path, `${JSON.stringify(parsed, null, indent)}
`);
}
__name(editMcpServers, "editMcpServers");

export {
  getProjectTrustOptions,
  hasTrustRequiringProjectResources,
  ProjectTrustStore,
  openBrowser,
  loadMcpConfig,
  updateMcpServerConfig,
  addMcpServerConfig,
  removeMcpServerConfig
};
