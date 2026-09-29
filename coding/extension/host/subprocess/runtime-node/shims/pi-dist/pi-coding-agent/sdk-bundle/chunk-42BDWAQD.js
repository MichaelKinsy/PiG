import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-coding-agent/core/tools/bash.js
import { constants as constants2 } from "node:fs";
import { access as fsAccess } from "node:fs/promises";
import { constants as osConstants } from "node:os";
import { spawn as spawn2 } from "child_process";
import { Type } from "../../../typebox.mjs";

// pi-dist/pi-coding-agent/utils/child-process.js
import { spawn as nodeSpawn, spawnSync as nodeSpawnSync } from "node:child_process";
import crossSpawn from "../../../cross-spawn/index.js";
var EXIT_STDIO_GRACE_MS = 100;
function spawnProcess(command, args, options) {
  return process.platform === "win32" ? crossSpawn(command, args, options) : nodeSpawn(command, args, options);
}
__name(spawnProcess, "spawnProcess");
function spawnProcessSync(command, args, options) {
  return process.platform === "win32" ? crossSpawn.sync(command, args, options) : nodeSpawnSync(command, args, options);
}
__name(spawnProcessSync, "spawnProcessSync");
function waitForChildProcess(child) {
  return new Promise((resolve3, reject) => {
    let settled = false;
    let exited = false;
    let exitCode = null;
    let postExitTimer;
    let stdoutEnded = child.stdout === null;
    let stderrEnded = child.stderr === null;
    const cleanup = /* @__PURE__ */ __name(() => {
      if (postExitTimer) {
        clearTimeout(postExitTimer);
        postExitTimer = void 0;
      }
      child.removeListener("error", onError);
      child.removeListener("exit", onExit);
      child.removeListener("close", onClose);
      child.stdout?.removeListener("end", onStdoutEnd);
      child.stderr?.removeListener("end", onStderrEnd);
      child.stdout?.removeListener("data", onData);
      child.stderr?.removeListener("data", onData);
    }, "cleanup");
    const finalize = /* @__PURE__ */ __name((code) => {
      if (settled)
        return;
      settled = true;
      cleanup();
      child.stdout?.destroy();
      child.stderr?.destroy();
      resolve3(code);
    }, "finalize");
    const maybeFinalizeAfterExit = /* @__PURE__ */ __name(() => {
      if (!exited || settled)
        return;
      if (stdoutEnded && stderrEnded) {
        finalize(exitCode);
      }
    }, "maybeFinalizeAfterExit");
    const armIdleTimer = /* @__PURE__ */ __name(() => {
      if (postExitTimer)
        clearTimeout(postExitTimer);
      postExitTimer = setTimeout(() => finalize(exitCode), EXIT_STDIO_GRACE_MS);
    }, "armIdleTimer");
    const onData = /* @__PURE__ */ __name(() => {
      if (exited && !settled)
        armIdleTimer();
    }, "onData");
    const onStdoutEnd = /* @__PURE__ */ __name(() => {
      stdoutEnded = true;
      maybeFinalizeAfterExit();
    }, "onStdoutEnd");
    const onStderrEnd = /* @__PURE__ */ __name(() => {
      stderrEnded = true;
      maybeFinalizeAfterExit();
    }, "onStderrEnd");
    const onError = /* @__PURE__ */ __name((err) => {
      if (settled)
        return;
      settled = true;
      cleanup();
      reject(err);
    }, "onError");
    const onExit = /* @__PURE__ */ __name((code) => {
      exited = true;
      exitCode = code;
      maybeFinalizeAfterExit();
      if (!settled) {
        armIdleTimer();
      }
    }, "onExit");
    const onClose = /* @__PURE__ */ __name((code) => {
      finalize(code);
    }, "onClose");
    child.stdout?.once("end", onStdoutEnd);
    child.stderr?.once("end", onStderrEnd);
    child.stdout?.on("data", onData);
    child.stderr?.on("data", onData);
    child.once("error", onError);
    child.once("exit", onExit);
    child.once("close", onClose);
  });
}
__name(waitForChildProcess, "waitForChildProcess");

// pi-dist/pi-coding-agent/utils/shell.js
import { existsSync as existsSync2 } from "node:fs";
import { delimiter, join as join3 } from "node:path";
import { spawn, spawnSync } from "child_process";

// pi-dist/pi-coding-agent/config.js
import { accessSync, constants, existsSync, readFileSync, realpathSync as realpathSync2 } from "fs";
import { homedir as homedir2 } from "os";
import { basename, dirname, join as join2, resolve, sep as sep2, win32 } from "path";
import { fileURLToPath as fileURLToPath2 } from "url";

// pi-dist/pi-coding-agent/utils/paths.js
import { realpathSync, statSync } from "node:fs";
import { homedir } from "node:os";
import { isAbsolute, join, resolve as nodeResolvePath, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";
var UNICODE_SPACES = /[\u00A0\u2000-\u200A\u202F\u205F\u3000]/g;
function canonicalizePath(path5) {
  try {
    return realpathSync(path5);
  } catch {
    return path5;
  }
}
__name(canonicalizePath, "canonicalizePath");
function getFileRevision(path5) {
  try {
    const stats = statSync(path5, { bigint: true });
    return `${stats.dev}:${stats.ino}:${stats.size}:${stats.mtimeNs}:${stats.ctimeNs}`;
  } catch {
    return void 0;
  }
}
__name(getFileRevision, "getFileRevision");
function isLocalPath(value) {
  const trimmed = value.trim();
  if (trimmed.startsWith("npm:") || trimmed.startsWith("git:") || trimmed.startsWith("github:") || trimmed.startsWith("http:") || trimmed.startsWith("https:") || trimmed.startsWith("ssh:")) {
    return false;
  }
  return true;
}
__name(isLocalPath, "isLocalPath");
function normalizeWindowsShellPath(filePath) {
  if (!filePath.startsWith("/") || filePath.startsWith("//") || filePath.includes("\\"))
    return filePath;
  const match = filePath.match(/^\/(?:mnt\/|cygdrive\/)?([a-z])(?:\/(.*))?$/i);
  if (!match)
    return filePath;
  const suffix = match[2]?.replaceAll("/", "\\");
  return `${match[1].toUpperCase()}:\\${suffix ?? ""}`;
}
__name(normalizeWindowsShellPath, "normalizeWindowsShellPath");
function normalizePath(input, options = {}) {
  let normalized = options.trim ? input.trim() : input;
  if (options.normalizeUnicodeSpaces) {
    normalized = normalized.replace(UNICODE_SPACES, " ");
  }
  if (options.stripAtPrefix && normalized.startsWith("@")) {
    normalized = normalized.slice(1);
  }
  if (process.platform === "win32") {
    normalized = normalizeWindowsShellPath(normalized);
  }
  if (options.expandTilde ?? true) {
    const home = options.homeDir ?? homedir();
    if (normalized === "~")
      return home;
    if (normalized.startsWith("~/") || process.platform === "win32" && normalized.startsWith("~\\")) {
      return join(home, normalized.slice(2));
    }
  }
  if (/^file:\/\//.test(normalized)) {
    return fileURLToPath(normalized);
  }
  return normalized;
}
__name(normalizePath, "normalizePath");
function resolvePath(input, baseDir = process.cwd(), options = {}) {
  const normalized = normalizePath(input, options);
  const normalizedBaseDir = normalizePath(baseDir);
  return isAbsolute(normalized) ? nodeResolvePath(normalized) : nodeResolvePath(normalizedBaseDir, normalized);
}
__name(resolvePath, "resolvePath");
function getCwdRelativePath(filePath, cwd) {
  const resolvedCwd = resolvePath(cwd);
  const resolvedPath = resolvePath(filePath, resolvedCwd);
  const relativePath = relative(resolvedCwd, resolvedPath);
  const isInsideCwd = relativePath === "" || relativePath !== ".." && !relativePath.startsWith(`..${sep}`) && !isAbsolute(relativePath);
  return isInsideCwd ? relativePath || "." : void 0;
}
__name(getCwdRelativePath, "getCwdRelativePath");
function formatPathRelativeToCwdOrAbsolute(filePath, cwd) {
  const absolutePath = resolvePath(filePath, cwd);
  return (getCwdRelativePath(absolutePath, cwd) ?? absolutePath).split(sep).join("/");
}
__name(formatPathRelativeToCwdOrAbsolute, "formatPathRelativeToCwdOrAbsolute");
function markPathIgnoredByCloudSync(path5) {
  const attrs = process.platform === "darwin" ? ["com.dropbox.ignored", "com.apple.fileprovider.ignore#P"] : process.platform === "linux" ? ["user.com.dropbox.ignored"] : [];
  for (const attr of attrs) {
    if (process.platform === "darwin") {
      spawnProcessSync("xattr", ["-w", attr, "1", path5], { encoding: "utf-8", stdio: "ignore" });
    } else {
      spawnProcessSync("setfattr", ["-n", attr, "-v", "1", path5], { encoding: "utf-8", stdio: "ignore" });
    }
  }
}
__name(markPathIgnoredByCloudSync, "markPathIgnoredByCloudSync");

// pi-dist/pi-coding-agent/utils/text.js
function splitBom(content) {
  return content.startsWith("\uFEFF") ? { bom: "\uFEFF", text: content.slice(1) } : { bom: "", text: content };
}
__name(splitBom, "splitBom");
function stripBom(content) {
  return splitBom(content).text;
}
__name(stripBom, "stripBom");

// pi-dist/pi-coding-agent/config.js
import { CONFIG_DIR_NAME } from "../../../pig-config.mjs";
import { ENV_AGENT_DIR } from "../../../pig-config.mjs";
import { CONFIG_DIR_NAME as CONFIG_DIR_NAME2, getAgentDir } from "../../../pig-config.mjs";
var __filename = fileURLToPath2(new URL("../config.js", import.meta.url).href);
var __dirname = dirname(__filename);
var isBunBinary = new URL("../config.js", import.meta.url).href.includes("$bunfs") || new URL("../config.js", import.meta.url).href.includes("~BUN") || new URL("../config.js", import.meta.url).href.includes("%7EBUN");
var isBunRuntime = !!process.versions.bun;
var isBundledNode = true;
function normalizeSelfUpdatePackageTarget(target) {
  if (typeof target === "string") {
    return { packageName: target, installSpec: target };
  }
  return { packageName: target.packageName, installSpec: target.installSpec ?? target.packageName };
}
__name(normalizeSelfUpdatePackageTarget, "normalizeSelfUpdatePackageTarget");
function makeSelfUpdateCommand(installStep, uninstallStep) {
  if (!uninstallStep)
    return installStep;
  return {
    ...installStep,
    display: `${uninstallStep.display} && ${installStep.display}`,
    steps: [uninstallStep, installStep]
  };
}
__name(makeSelfUpdateCommand, "makeSelfUpdateCommand");
function makeSelfUpdateCommandStep(command, args) {
  return {
    command,
    args,
    display: [command, ...args].map((arg) => /\s/.test(arg) ? `"${arg}"` : arg).join(" ")
  };
}
__name(makeSelfUpdateCommandStep, "makeSelfUpdateCommandStep");
function detectInstallMethod() {
  if (isBunBinary) {
    return "bun-binary";
  }
  const resolvedPath = `${__dirname}\0${process.execPath || ""}`.toLowerCase().replace(/\\/g, "/");
  if (resolvedPath.includes("/pnpm/") || resolvedPath.includes("/.pnpm/")) {
    return "pnpm";
  }
  if (resolvedPath.includes("/yarn/") || resolvedPath.includes("/.yarn/")) {
    return "yarn";
  }
  if (isBunRuntime || resolvedPath.includes("/install/global/node_modules/")) {
    return "bun";
  }
  if (resolvedPath.includes("/npm/") || resolvedPath.includes("/node_modules/")) {
    return "npm";
  }
  return "unknown";
}
__name(detectInstallMethod, "detectInstallMethod");
function getInferredNpmInstall() {
  const packageDir = getPackageDir();
  const path5 = process.platform === "win32" || packageDir.includes("\\") ? win32 : { basename, dirname };
  const parent = path5.dirname(packageDir);
  let root;
  if (path5.basename(parent).startsWith("@") && path5.basename(path5.dirname(parent)) === "node_modules") {
    root = path5.dirname(parent);
  } else if (path5.basename(parent) === "node_modules") {
    root = parent;
  }
  if (!root)
    return void 0;
  const rootParent = path5.dirname(root);
  if (path5.basename(rootParent) === "lib")
    return { root, prefix: path5.dirname(rootParent) };
  return void 0;
}
__name(getInferredNpmInstall, "getInferredNpmInstall");
function getSelfUpdateCommandForMethod(method, installedPackageName, updatePackageTarget = installedPackageName, npmCommand) {
  const target = normalizeSelfUpdatePackageTarget(updatePackageTarget);
  switch (method) {
    case "bun-binary":
      return void 0;
    case "pnpm": {
      const match = readCommandOutput("pnpm", ["root", "-g"]) ? void 0 : /^(.*[\\/]global[\\/][^\\/]+)[\\/]\.pnpm[\\/]/.exec(getPackageDir());
      const binDirArgs = match ? [`--config.global-bin-dir=${process.env.PNPM_HOME || dirname(dirname(match[1]))}`] : [];
      return makeSelfUpdateCommand(makeSelfUpdateCommandStep("pnpm", [
        "install",
        "-g",
        "--ignore-scripts",
        "--config.minimumReleaseAge=0",
        ...binDirArgs,
        target.installSpec
      ]), target.packageName === installedPackageName ? void 0 : makeSelfUpdateCommandStep("pnpm", ["remove", "-g", ...binDirArgs, installedPackageName]));
    }
    case "yarn":
      return makeSelfUpdateCommand(makeSelfUpdateCommandStep("yarn", ["global", "add", "--ignore-scripts", target.installSpec]), target.packageName === installedPackageName ? void 0 : makeSelfUpdateCommandStep("yarn", ["global", "remove", installedPackageName]));
    case "bun":
      return makeSelfUpdateCommand(makeSelfUpdateCommandStep("bun", [
        "install",
        "-g",
        "--ignore-scripts",
        "--minimum-release-age=0",
        target.installSpec
      ]), target.packageName === installedPackageName ? void 0 : makeSelfUpdateCommandStep("bun", ["uninstall", "-g", installedPackageName]));
    case "npm": {
      const [command = "npm", ...npmArgs] = npmCommand ?? [];
      const inferred = npmCommand?.length ? void 0 : getInferredNpmInstall();
      const prefixArgs = [...npmArgs, ...inferred ? ["--prefix", inferred.prefix] : []];
      const installStep = makeSelfUpdateCommandStep(command, [
        ...prefixArgs,
        "install",
        "-g",
        "--ignore-scripts",
        "--min-release-age=0",
        target.installSpec
      ]);
      const uninstallStep = target.packageName === installedPackageName ? void 0 : makeSelfUpdateCommandStep(command, [...prefixArgs, "uninstall", "-g", installedPackageName]);
      return makeSelfUpdateCommand(installStep, uninstallStep);
    }
    case "unknown":
      return void 0;
  }
}
__name(getSelfUpdateCommandForMethod, "getSelfUpdateCommandForMethod");
function readCommandOutput(command, args, options = {}) {
  const result = spawnProcessSync(command, args, {
    encoding: "utf-8",
    stdio: ["ignore", "pipe", "pipe"]
  });
  if (result.status === 0)
    return result.stdout.trim() || void 0;
  if (options.requireSuccess) {
    const reason = result.error?.message || result.stderr.trim() || `exit code ${result.status ?? "unknown"}`;
    throw new Error(`Failed to run ${[command, ...args].join(" ")}: ${reason}`);
  }
  return void 0;
}
__name(readCommandOutput, "readCommandOutput");
function getGlobalPackageRoots(method, _packageName, npmCommand) {
  switch (method) {
    case "npm": {
      const configured = !!npmCommand?.length;
      const [command = "npm", ...npmArgs] = npmCommand ?? [];
      if (configured && command === "bun") {
        const bunBin = readCommandOutput(command, [...npmArgs, "pm", "bin", "-g"], {
          requireSuccess: true
        });
        const roots = [join2(homedir2(), ".bun", "install", "global", "node_modules")];
        if (bunBin) {
          roots.push(join2(dirname(bunBin), "install", "global", "node_modules"));
        }
        return roots;
      }
      const root = readCommandOutput(command, [...npmArgs, "root", "-g"], {
        requireSuccess: configured
      });
      const inferred = configured ? void 0 : getInferredNpmInstall();
      return [root, inferred?.root].filter((x) => !!x);
    }
    case "pnpm": {
      const root = readCommandOutput("pnpm", ["root", "-g"]);
      if (root)
        return [root, dirname(root)];
      const match = /^(.*[\\/]global[\\/][^\\/]+)[\\/]\.pnpm[\\/]/.exec(getPackageDir());
      return match ? [match[1]] : [];
    }
    case "yarn": {
      const dir = readCommandOutput("yarn", ["global", "dir"]);
      return dir ? [dir, join2(dir, "node_modules")] : [];
    }
    case "bun": {
      const bunBin = readCommandOutput("bun", ["pm", "bin", "-g"]);
      const roots = [join2(homedir2(), ".bun", "install", "global", "node_modules")];
      if (bunBin) {
        roots.push(join2(dirname(bunBin), "install", "global", "node_modules"));
      }
      return roots;
    }
    case "bun-binary":
    case "unknown":
      return [];
  }
}
__name(getGlobalPackageRoots, "getGlobalPackageRoots");
function normalizeExistingPathForComparison(path5, resolveSymlinks) {
  const resolvedPath = resolve(path5);
  if (!existsSync(resolvedPath)) {
    return void 0;
  }
  let normalizedPath = resolvedPath;
  if (resolveSymlinks) {
    try {
      normalizedPath = realpathSync2(resolvedPath);
    } catch {
      return void 0;
    }
  }
  if (process.platform === "win32") {
    normalizedPath = normalizedPath.toLowerCase();
  }
  return normalizedPath;
}
__name(normalizeExistingPathForComparison, "normalizeExistingPathForComparison");
function getPathComparisonCandidates(path5) {
  return Array.from(new Set([normalizeExistingPathForComparison(path5, false), normalizeExistingPathForComparison(path5, true)].filter((candidate) => !!candidate)));
}
__name(getPathComparisonCandidates, "getPathComparisonCandidates");
function getEntrypointPackageDir() {
  const entrypoint = process.argv[1];
  if (!entrypoint)
    return void 0;
  let dir = dirname(entrypoint);
  while (dir !== dirname(dir)) {
    if (existsSync(join2(dir, "package.json"))) {
      return dir;
    }
    dir = dirname(dir);
  }
  return void 0;
}
__name(getEntrypointPackageDir, "getEntrypointPackageDir");
function isSelfUpdatePathWritable() {
  const packageDir = getPackageDir();
  try {
    accessSync(packageDir, constants.W_OK);
    accessSync(dirname(packageDir), constants.W_OK);
    return true;
  } catch {
    return false;
  }
}
__name(isSelfUpdatePathWritable, "isSelfUpdatePathWritable");
function isManagedByGlobalPackageManager(method, packageName, npmCommand) {
  const packageDirs = [getPackageDir(), getEntrypointPackageDir()].filter((dir) => !!dir);
  const packageDirCandidates = packageDirs.flatMap((dir) => getPathComparisonCandidates(dir));
  return getGlobalPackageRoots(method, packageName, npmCommand).some((root) => {
    return getPathComparisonCandidates(root).some((normalizedRoot) => {
      const rootPrefix = normalizedRoot.endsWith(sep2) ? normalizedRoot : `${normalizedRoot}${sep2}`;
      return packageDirCandidates.some((packageDir) => packageDir.startsWith(rootPrefix));
    });
  });
}
__name(isManagedByGlobalPackageManager, "isManagedByGlobalPackageManager");
function getSelfUpdateCommand(packageName, npmCommand, updatePackageTarget = packageName) {
  const method = detectInstallMethod();
  const command = getSelfUpdateCommandForMethod(method, packageName, updatePackageTarget, npmCommand);
  if (!command || !isManagedByGlobalPackageManager(method, packageName, npmCommand) || !isSelfUpdatePathWritable()) {
    return void 0;
  }
  return command;
}
__name(getSelfUpdateCommand, "getSelfUpdateCommand");
function getSelfUpdateUnavailableInstruction(packageName, npmCommand, updatePackageTarget = packageName) {
  const method = detectInstallMethod();
  const target = normalizeSelfUpdatePackageTarget(updatePackageTarget);
  if (method === "bun-binary") {
    return `Download from: https://github.com/earendil-works/pi/releases/latest`;
  }
  const command = getSelfUpdateCommandForMethod(method, packageName, target, npmCommand);
  if (command) {
    if (isManagedByGlobalPackageManager(method, packageName, npmCommand) && !isSelfUpdatePathWritable()) {
      return `This installation is managed by a global ${method} install, but the install path is not writable. Update it yourself with: ${command.display}`;
    }
    return `This installation is not managed by a global ${method} install. Update it with the package manager, wrapper, or source checkout that provides it.`;
  }
  return `Update ${target.installSpec} using the package manager, wrapper, or source checkout that provides this installation.`;
}
__name(getSelfUpdateUnavailableInstruction, "getSelfUpdateUnavailableInstruction");
function findNodePackageDir(startDir) {
  let dir = startDir;
  while (dir !== dirname(dir)) {
    if (existsSync(join2(dir, "package.json"))) {
      const parent = dirname(dir);
      if (basename(dir) === "dist" && existsSync(join2(parent, "package.json"))) {
        return parent;
      }
      return dir;
    }
    dir = dirname(dir);
  }
  return startDir;
}
__name(findNodePackageDir, "findNodePackageDir");
function getPackageDir() {
  const envDir = process.env.PI_PACKAGE_DIR;
  if (envDir) {
    return normalizePath(envDir);
  }
  if (isBunBinary) {
    return dirname(process.execPath);
  }
  return findNodePackageDir(__dirname);
}
__name(getPackageDir, "getPackageDir");
function getThemesDir() {
  if (isBunBinary) {
    return join2(getPackageDir(), "theme");
  }
  const packageDir = getPackageDir();
  const srcOrDist = ".";
  return join2(packageDir, srcOrDist, "modes", "interactive", "theme");
}
__name(getThemesDir, "getThemesDir");
function getExportTemplateDir() {
  if (isBunBinary) {
    return join2(getPackageDir(), "export-html");
  }
  const packageDir = getPackageDir();
  const srcOrDist = ".";
  return join2(packageDir, srcOrDist, "core", "export-html");
}
__name(getExportTemplateDir, "getExportTemplateDir");
function getPackageJsonPath() {
  return join2(getPackageDir(), "package.json");
}
__name(getPackageJsonPath, "getPackageJsonPath");
function getReadmePath() {
  return resolve(join2(getPackageDir(), "README.md"));
}
__name(getReadmePath, "getReadmePath");
function getDocsPath() {
  return resolve(join2(getPackageDir(), "docs"));
}
__name(getDocsPath, "getDocsPath");
function getExamplesPath() {
  return resolve(join2(getPackageDir(), "examples"));
}
__name(getExamplesPath, "getExamplesPath");
function getChangelogPath() {
  return resolve(join2(getPackageDir(), "CHANGELOG.md"));
}
__name(getChangelogPath, "getChangelogPath");
function getInteractiveAssetsDir() {
  if (isBunBinary) {
    return join2(getPackageDir(), "assets");
  }
  const packageDir = getPackageDir();
  const srcOrDist = ".";
  return join2(packageDir, srcOrDist, "modes", "interactive", "assets");
}
__name(getInteractiveAssetsDir, "getInteractiveAssetsDir");
function getBundledInteractiveAssetPath(name) {
  return join2(getInteractiveAssetsDir(), name);
}
__name(getBundledInteractiveAssetPath, "getBundledInteractiveAssetPath");
var pkg = {};
try {
  pkg = JSON.parse(stripBom(readFileSync(getPackageJsonPath(), "utf-8")));
} catch (e) {
  const err = e;
  if (err.code !== "ENOENT")
    throw e;
}
var piConfigName = pkg.piConfig?.name;
var PACKAGE_NAME = pkg.name || "@earendil-works/pi-coding-agent";
var APP_NAME = "pig";
var APP_TITLE = piConfigName ? APP_NAME : "\u03C0";
var VERSION = pkg.version || "0.0.0";
var ENV_SESSION_DIR = `${APP_NAME.toUpperCase()}_CODING_AGENT_SESSION_DIR`;
function expandTildePath(path5) {
  return normalizePath(path5);
}
__name(expandTildePath, "expandTildePath");
var DEFAULT_SHARE_VIEWER_URL = "https://pi.dev/session/";
function getShareViewerUrl(gistId) {
  const baseUrl = process.env.PI_SHARE_VIEWER_URL || DEFAULT_SHARE_VIEWER_URL;
  return `${baseUrl}#${gistId}`;
}
__name(getShareViewerUrl, "getShareViewerUrl");
function getCustomThemesDir() {
  return join2(getAgentDir(), "themes");
}
__name(getCustomThemesDir, "getCustomThemesDir");
function getAuthPath() {
  return join2(getAgentDir(), "auth.json");
}
__name(getAuthPath, "getAuthPath");
function getSettingsPath() {
  return join2(getAgentDir(), "settings.json");
}
__name(getSettingsPath, "getSettingsPath");
function getBinDir() {
  return join2(getAgentDir(), "bin");
}
__name(getBinDir, "getBinDir");
function getSessionsDir() {
  return join2(getAgentDir(), "sessions");
}
__name(getSessionsDir, "getSessionsDir");
function getDebugLogPath() {
  return join2(getAgentDir(), `${APP_NAME}-debug.log`);
}
__name(getDebugLogPath, "getDebugLogPath");

// pi-dist/pi-coding-agent/utils/shell.js
function isLegacyWslBashPath(path5) {
  const normalized = path5.replace(/\//g, "\\").toLowerCase();
  return /^[a-z]:\\windows\\(?:system32|sysnative)\\bash\.exe$/.test(normalized);
}
__name(isLegacyWslBashPath, "isLegacyWslBashPath");
function getBashShellConfig(shell) {
  return isLegacyWslBashPath(shell) ? { shell, args: ["-s"], commandTransport: "stdin" } : { shell, args: ["-c"] };
}
__name(getBashShellConfig, "getBashShellConfig");
function findExecutableOnPath(executable) {
  if (process.platform === "win32") {
    try {
      const result = spawnSync("where", [executable], {
        encoding: "utf-8",
        timeout: 5e3,
        windowsHide: true
      });
      if (result.status === 0 && result.stdout) {
        const firstMatch = result.stdout.trim().split(/\r?\n/)[0];
        if (firstMatch && existsSync2(firstMatch)) {
          return firstMatch;
        }
      }
    } catch {
    }
    return null;
  }
  try {
    const result = spawnSync("which", [executable], { encoding: "utf-8", timeout: 5e3 });
    if (result.status === 0 && result.stdout) {
      const firstMatch = result.stdout.trim().split(/\r?\n/)[0];
      if (firstMatch) {
        return firstMatch;
      }
    }
  } catch {
  }
  return null;
}
__name(findExecutableOnPath, "findExecutableOnPath");
function getShellConfig(customShellPath) {
  if (customShellPath) {
    if (existsSync2(customShellPath)) {
      return getBashShellConfig(customShellPath);
    }
    throw new Error(`Custom shell path not found: ${customShellPath}`);
  }
  if (process.platform === "win32") {
    const paths = [];
    const programFiles = process.env.ProgramFiles;
    if (programFiles) {
      paths.push(`${programFiles}\\Git\\bin\\bash.exe`);
    }
    const programFilesX86 = process.env["ProgramFiles(x86)"];
    if (programFilesX86) {
      paths.push(`${programFilesX86}\\Git\\bin\\bash.exe`);
    }
    for (const path5 of paths) {
      if (existsSync2(path5)) {
        return getBashShellConfig(path5);
      }
    }
    const bashOnPath2 = findExecutableOnPath("bash.exe");
    if (bashOnPath2) {
      return getBashShellConfig(bashOnPath2);
    }
    throw new Error(`No bash shell found. Options:
  1. Install Git for Windows: https://git-scm.com/download/win
  2. Add your bash to PATH (Cygwin, MSYS2, etc.)
  3. Set shellPath in settings.json

Searched Git Bash in:
${paths.map((p) => `  ${p}`).join("\n")}`);
  }
  if (existsSync2("/bin/bash")) {
    return getBashShellConfig("/bin/bash");
  }
  const bashOnPath = findExecutableOnPath("bash");
  if (bashOnPath) {
    return getBashShellConfig(bashOnPath);
  }
  return { shell: "sh", args: ["-c"] };
}
__name(getShellConfig, "getShellConfig");
var POWERSHELL_ARGS = ["-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command"];
function getPowerShellConfig() {
  if (process.platform !== "win32") {
    throw new Error("The powershell tool is only available on Windows.");
  }
  const shell = findExecutableOnPath("pwsh.exe") ?? findExecutableOnPath("powershell.exe");
  if (!shell) {
    throw new Error("No PowerShell executable found. Install PowerShell or add powershell.exe/pwsh.exe to PATH.");
  }
  return { shell, args: [...POWERSHELL_ARGS] };
}
__name(getPowerShellConfig, "getPowerShellConfig");
function getShellEnv() {
  const binDir = getBinDir();
  const pathKey = Object.keys(process.env).find((key) => key.toLowerCase() === "path") ?? "PATH";
  const currentPath = process.env[pathKey] ?? "";
  const pathEntries = currentPath.split(delimiter).filter(Boolean);
  const hasBinDir = pathEntries.includes(binDir);
  const updatedPath = hasBinDir ? currentPath : [binDir, currentPath].filter(Boolean).join(delimiter);
  return {
    ...process.env,
    [pathKey]: updatedPath
  };
}
__name(getShellEnv, "getShellEnv");
function sanitizeBinaryOutput(str2) {
  return Array.from(str2).filter((char) => {
    const code = char.codePointAt(0);
    if (code === void 0)
      return false;
    if (code === 9 || code === 10 || code === 13)
      return true;
    if (code <= 31)
      return false;
    if (code >= 65529 && code <= 65531)
      return false;
    return true;
  }).join("");
}
__name(sanitizeBinaryOutput, "sanitizeBinaryOutput");
var trackedDetachedChildPids = /* @__PURE__ */ new Set();
function trackDetachedChildPid(pid) {
  trackedDetachedChildPids.add(pid);
}
__name(trackDetachedChildPid, "trackDetachedChildPid");
function untrackDetachedChildPid(pid) {
  trackedDetachedChildPids.delete(pid);
}
__name(untrackDetachedChildPid, "untrackDetachedChildPid");
function killTrackedDetachedChildren() {
  for (const pid of trackedDetachedChildPids) {
    killProcessTree(pid);
  }
  trackedDetachedChildPids.clear();
}
__name(killTrackedDetachedChildren, "killTrackedDetachedChildren");
function killProcessTree(pid) {
  if (process.platform === "win32") {
    try {
      const child = spawn(join3(process.env.SystemRoot ?? "C:\\Windows", "System32", "taskkill.exe"), ["/F", "/T", "/PID", String(pid)], {
        stdio: "ignore",
        detached: true,
        windowsHide: true
      });
      child.once("error", () => {
      });
    } catch {
    }
  } else {
    try {
      process.kill(-pid, "SIGKILL");
    } catch {
      try {
        process.kill(pid, "SIGKILL");
      } catch {
      }
    }
  }
}
__name(killProcessTree, "killProcessTree");

// pi-dist/pi-coding-agent/core/tools/output-accumulator.js
import { randomBytes } from "node:crypto";
import { createWriteStream } from "node:fs";
import { tmpdir } from "node:os";
import { join as join4 } from "node:path";

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

// pi-dist/pi-coding-agent/core/tools/output-accumulator.js
function defaultTempFilePath(prefix) {
  const id = randomBytes(8).toString("hex");
  return join4(tmpdir(), `${prefix}-${id}.log`);
}
__name(defaultTempFilePath, "defaultTempFilePath");
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
    await new Promise((resolve3, reject) => {
      const onError = /* @__PURE__ */ __name((error) => {
        stream.off("finish", onFinish);
        reject(error);
      }, "onError");
      const onFinish = /* @__PURE__ */ __name(() => {
        stream.off("error", onError);
        resolve3();
      }, "onFinish");
      stream.once("error", onError);
      stream.once("finish", onFinish);
      stream.end();
    });
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
    this.tempFilePath = defaultTempFilePath(this.tempFilePrefix);
    this.tempFileStream = createWriteStream(this.tempFilePath);
    for (const chunk of this.rawChunks) {
      this.tempFileStream.write(chunk);
    }
    this.rawChunks = [];
  }
};

// pi-dist/pi-coding-agent/core/tools/renderers/bash.js
import { Container, Text as Text2, truncateToWidth } from "../../../pi-tui.mjs";

// pi-dist/pi-coding-agent/modes/interactive/components/keybinding-hints.js
import { getKeybindings } from "../../../pi-tui.mjs";

// pi-dist/pi-coding-agent/modes/interactive/theme/theme.js
import * as fs from "node:fs";
import * as path from "node:path";
import { getCapabilities } from "../../pi-tui/terminal-image.js";
import chalk from "../../../chalk/source/index.js";

// pi-dist/pi-coding-agent/utils/fs-watch.js
import { watch } from "node:fs";
var FS_WATCH_RETRY_DELAY_MS = 5e3;
function closeWatcher(watcher) {
  if (!watcher) {
    return;
  }
  try {
    watcher.close();
  } catch {
  }
}
__name(closeWatcher, "closeWatcher");
function watchWithErrorHandler(path5, listener, onError) {
  try {
    const watcher = watch(path5, listener);
    watcher.on("error", onError);
    return watcher;
  } catch {
    onError();
    return null;
  }
}
__name(watchWithErrorHandler, "watchWithErrorHandler");

// pi-dist/pi-coding-agent/modes/interactive/theme/theme.js
import { highlight, supportsLanguage } from "../../../syntax-highlight.mjs";
var themeJsonValidator;
function setThemeJsonValidator(validator) {
  themeJsonValidator = validator;
}
__name(setThemeJsonValidator, "setThemeJsonValidator");
function hexToRgb(hex) {
  const cleaned = hex.replace("#", "");
  if (cleaned.length !== 6) {
    throw new Error(`Invalid hex color: ${hex}`);
  }
  const r = parseInt(cleaned.substring(0, 2), 16);
  const g = parseInt(cleaned.substring(2, 4), 16);
  const b = parseInt(cleaned.substring(4, 6), 16);
  if (Number.isNaN(r) || Number.isNaN(g) || Number.isNaN(b)) {
    throw new Error(`Invalid hex color: ${hex}`);
  }
  return { r, g, b };
}
__name(hexToRgb, "hexToRgb");
var CUBE_VALUES = [0, 95, 135, 175, 215, 255];
var GRAY_VALUES = Array.from({ length: 24 }, (_, i) => 8 + i * 10);
function findClosestCubeIndex(value) {
  let minDist = Infinity;
  let minIdx = 0;
  for (let i = 0; i < CUBE_VALUES.length; i++) {
    const dist = Math.abs(value - CUBE_VALUES[i]);
    if (dist < minDist) {
      minDist = dist;
      minIdx = i;
    }
  }
  return minIdx;
}
__name(findClosestCubeIndex, "findClosestCubeIndex");
function findClosestGrayIndex(gray) {
  let minDist = Infinity;
  let minIdx = 0;
  for (let i = 0; i < GRAY_VALUES.length; i++) {
    const dist = Math.abs(gray - GRAY_VALUES[i]);
    if (dist < minDist) {
      minDist = dist;
      minIdx = i;
    }
  }
  return minIdx;
}
__name(findClosestGrayIndex, "findClosestGrayIndex");
function colorDistance(r1, g1, b1, r2, g2, b2) {
  const dr = r1 - r2;
  const dg = g1 - g2;
  const db = b1 - b2;
  return dr * dr * 0.299 + dg * dg * 0.587 + db * db * 0.114;
}
__name(colorDistance, "colorDistance");
function rgbTo256(r, g, b) {
  const rIdx = findClosestCubeIndex(r);
  const gIdx = findClosestCubeIndex(g);
  const bIdx = findClosestCubeIndex(b);
  const cubeR = CUBE_VALUES[rIdx];
  const cubeG = CUBE_VALUES[gIdx];
  const cubeB = CUBE_VALUES[bIdx];
  const cubeIndex = 16 + 36 * rIdx + 6 * gIdx + bIdx;
  const cubeDist = colorDistance(r, g, b, cubeR, cubeG, cubeB);
  const gray = Math.round(0.299 * r + 0.587 * g + 0.114 * b);
  const grayIdx = findClosestGrayIndex(gray);
  const grayValue = GRAY_VALUES[grayIdx];
  const grayIndex = 232 + grayIdx;
  const grayDist = colorDistance(r, g, b, grayValue, grayValue, grayValue);
  const maxC = Math.max(r, g, b);
  const minC = Math.min(r, g, b);
  const spread = maxC - minC;
  if (spread < 10 && grayDist < cubeDist) {
    return grayIndex;
  }
  return cubeIndex;
}
__name(rgbTo256, "rgbTo256");
function hexTo256(hex) {
  const { r, g, b } = hexToRgb(hex);
  return rgbTo256(r, g, b);
}
__name(hexTo256, "hexTo256");
function fgAnsi(color, mode) {
  if (color === "")
    return "\x1B[39m";
  if (typeof color === "number")
    return `\x1B[38;5;${color}m`;
  if (color.startsWith("#")) {
    if (mode === "truecolor") {
      const { r, g, b } = hexToRgb(color);
      return `\x1B[38;2;${r};${g};${b}m`;
    } else {
      const index = hexTo256(color);
      return `\x1B[38;5;${index}m`;
    }
  }
  throw new Error(`Invalid color value: ${color}`);
}
__name(fgAnsi, "fgAnsi");
function bgAnsi(color, mode) {
  if (color === "")
    return "\x1B[49m";
  if (typeof color === "number")
    return `\x1B[48;5;${color}m`;
  if (color.startsWith("#")) {
    if (mode === "truecolor") {
      const { r, g, b } = hexToRgb(color);
      return `\x1B[48;2;${r};${g};${b}m`;
    } else {
      const index = hexTo256(color);
      return `\x1B[48;5;${index}m`;
    }
  }
  throw new Error(`Invalid color value: ${color}`);
}
__name(bgAnsi, "bgAnsi");
function resolveVarRefs(value, vars, visited = /* @__PURE__ */ new Set()) {
  if (typeof value === "number" || value === "" || value.startsWith("#")) {
    return value;
  }
  if (visited.has(value)) {
    throw new Error(`Circular variable reference detected: ${value}`);
  }
  if (!(value in vars)) {
    throw new Error(`Variable reference not found: ${value}`);
  }
  visited.add(value);
  return resolveVarRefs(vars[value], vars, visited);
}
__name(resolveVarRefs, "resolveVarRefs");
function resolveThemeColors(colors, vars = {}) {
  const resolved = {};
  for (const [key, value] of Object.entries(colors)) {
    resolved[key] = resolveVarRefs(value, vars);
  }
  return resolved;
}
__name(resolveThemeColors, "resolveThemeColors");
function withThemeColorFallbacks(colors) {
  return {
    ...colors,
    scrollbarTrack: colors.scrollbarTrack ?? colors.muted,
    scrollbarThumb: colors.scrollbarThumb ?? colors.text,
    thinkingMax: colors.thinkingMax ?? colors.thinkingXhigh,
    searchMatchBg: colors.searchMatchBg ?? colors.selectedBg,
    searchMatchText: colors.searchMatchText ?? colors.text
  };
}
__name(withThemeColorFallbacks, "withThemeColorFallbacks");
var Theme = class {
  static {
    __name(this, "Theme");
  }
  name;
  sourcePath;
  sourceInfo;
  fgColors;
  bgColors;
  mode;
  constructor(fgColors, bgColors, mode, options = {}) {
    this.name = options.name;
    this.sourcePath = options.sourcePath;
    this.sourceInfo = options.sourceInfo;
    this.mode = mode;
    this.fgColors = /* @__PURE__ */ new Map();
    const colors = {
      ...fgColors,
      scrollbarTrack: fgColors.scrollbarTrack ?? fgColors.muted,
      scrollbarThumb: fgColors.scrollbarThumb ?? fgColors.text,
      thinkingMax: fgColors.thinkingMax ?? fgColors.thinkingXhigh,
      searchMatchText: fgColors.searchMatchText ?? fgColors.text
    };
    for (const [key, value] of Object.entries(colors)) {
      this.fgColors.set(key, fgAnsi(value, mode));
    }
    this.bgColors = /* @__PURE__ */ new Map();
    const backgrounds = {
      ...bgColors,
      searchMatchBg: bgColors.searchMatchBg ?? bgColors.selectedBg
    };
    for (const [key, value] of Object.entries(backgrounds)) {
      this.bgColors.set(key, bgAnsi(value, mode));
    }
  }
  fg(color, text) {
    const ansi = this.fgColors.get(color);
    if (!ansi)
      throw new Error(`Unknown theme color: ${color}`);
    return `${ansi}${text}\x1B[39m`;
  }
  bg(color, text) {
    const ansi = this.bgColors.get(color);
    if (!ansi)
      throw new Error(`Unknown theme background color: ${color}`);
    return `${ansi}${text}\x1B[49m`;
  }
  bold(text) {
    return chalk.bold(text);
  }
  italic(text) {
    return chalk.italic(text);
  }
  underline(text) {
    return chalk.underline(text);
  }
  inverse(text) {
    return chalk.inverse(text);
  }
  strikethrough(text) {
    return chalk.strikethrough(text);
  }
  getFgAnsi(color) {
    const ansi = this.fgColors.get(color);
    if (!ansi)
      throw new Error(`Unknown theme color: ${color}`);
    return ansi;
  }
  getBgAnsi(color) {
    const ansi = this.bgColors.get(color);
    if (!ansi)
      throw new Error(`Unknown theme background color: ${color}`);
    return ansi;
  }
  getColorMode() {
    return this.mode;
  }
  getThinkingBorderColor(level) {
    switch (level) {
      case "off":
        return (str2) => this.fg("thinkingOff", str2);
      case "minimal":
        return (str2) => this.fg("thinkingMinimal", str2);
      case "low":
        return (str2) => this.fg("thinkingLow", str2);
      case "medium":
        return (str2) => this.fg("thinkingMedium", str2);
      case "high":
        return (str2) => this.fg("thinkingHigh", str2);
      case "xhigh":
        return (str2) => this.fg("thinkingXhigh", str2);
      case "max":
        return (str2) => this.fg("thinkingMax", str2);
      default:
        return (str2) => this.fg("thinkingOff", str2);
    }
  }
  getBashModeBorderColor() {
    return (str2) => this.fg("bashMode", str2);
  }
};
var BUILTIN_THEMES;
function getBuiltinThemes() {
  if (!BUILTIN_THEMES) {
    const themesDir = getThemesDir();
    const darkPath = path.join(themesDir, "dark.json");
    const lightPath = path.join(themesDir, "light.json");
    BUILTIN_THEMES = {
      dark: JSON.parse(stripBom(fs.readFileSync(darkPath, "utf-8"))),
      light: JSON.parse(stripBom(fs.readFileSync(lightPath, "utf-8")))
    };
  }
  return BUILTIN_THEMES;
}
__name(getBuiltinThemes, "getBuiltinThemes");
function getAvailableThemes() {
  return getAvailableThemesWithPaths().map(({ name }) => name);
}
__name(getAvailableThemes, "getAvailableThemes");
function getAvailableThemesWithPaths() {
  const themesDir = getThemesDir();
  const result = [];
  const seen = /* @__PURE__ */ new Set();
  const addTheme = /* @__PURE__ */ __name((themeInfo) => {
    if (seen.has(themeInfo.name)) {
      return;
    }
    seen.add(themeInfo.name);
    result.push(themeInfo);
  }, "addTheme");
  for (const name of Object.keys(getBuiltinThemes())) {
    addTheme({ name, path: path.join(themesDir, `${name}.json`) });
  }
  for (const themeInfo of getCustomThemeInfos()) {
    addTheme(themeInfo);
  }
  for (const [name, theme2] of registeredThemes.entries()) {
    addTheme({ name, path: theme2.sourcePath });
  }
  return result.sort((a, b) => a.name.localeCompare(b.name));
}
__name(getAvailableThemesWithPaths, "getAvailableThemesWithPaths");
function getCustomThemeInfos() {
  const customThemesDir = getCustomThemesDir();
  const result = [];
  if (!fs.existsSync(customThemesDir)) {
    return result;
  }
  for (const file of fs.readdirSync(customThemesDir)) {
    if (!file.endsWith(".json")) {
      continue;
    }
    const themePath = path.join(customThemesDir, file);
    try {
      const customTheme = loadThemeFromPath(themePath);
      if (customTheme.name) {
        result.push({ name: customTheme.name, path: themePath });
      }
    } catch {
    }
  }
  return result;
}
__name(getCustomThemeInfos, "getCustomThemeInfos");
function assertThemeNameIsValid(name) {
  if (name.includes("/")) {
    throw new Error(`Invalid theme name "${name}": theme names cannot contain "/" because it is reserved for automatic light/dark theme settings.`);
  }
}
__name(assertThemeNameIsValid, "assertThemeNameIsValid");
function parseThemeJson(label, json) {
  if (themeJsonValidator)
    return themeJsonValidator(label, json);
  if (typeof json !== "object" || json === null || !("colors" in json)) {
    throw new Error(`Invalid theme "${label}": expected an object with a "colors" map.`);
  }
  return json;
}
__name(parseThemeJson, "parseThemeJson");
function parseThemeJsonContent(label, content) {
  let json;
  try {
    json = JSON.parse(stripBom(content));
  } catch (error) {
    throw new Error(`Failed to parse theme ${label}: ${error}`);
  }
  return parseThemeJson(label, json);
}
__name(parseThemeJsonContent, "parseThemeJsonContent");
function loadThemeJson(name) {
  const builtinThemes = getBuiltinThemes();
  if (name in builtinThemes) {
    return builtinThemes[name];
  }
  const registeredTheme = registeredThemes.get(name);
  if (registeredTheme?.sourcePath) {
    const content2 = fs.readFileSync(registeredTheme.sourcePath, "utf-8");
    return parseThemeJsonContent(registeredTheme.sourcePath, content2);
  }
  if (registeredTheme) {
    throw new Error(`Theme "${name}" does not have a source path for export`);
  }
  const customThemesDir = getCustomThemesDir();
  const themePath = path.join(customThemesDir, `${name}.json`);
  if (!fs.existsSync(themePath)) {
    throw new Error(`Theme not found: ${name}`);
  }
  const content = fs.readFileSync(themePath, "utf-8");
  return parseThemeJsonContent(name, content);
}
__name(loadThemeJson, "loadThemeJson");
function createTheme(themeJson, mode, sourcePath) {
  const colorMode = mode ?? (getCapabilities().trueColor ? "truecolor" : "256color");
  const resolvedColors = resolveThemeColors(withThemeColorFallbacks(themeJson.colors), themeJson.vars);
  const fgColors = {};
  const bgColors = {};
  const bgColorKeys = /* @__PURE__ */ new Set([
    "selectedBg",
    "searchMatchBg",
    "userMessageBg",
    "customMessageBg",
    "toolPendingBg",
    "toolSuccessBg",
    "toolErrorBg"
  ]);
  for (const [key, value] of Object.entries(resolvedColors)) {
    if (bgColorKeys.has(key)) {
      bgColors[key] = value;
    } else {
      fgColors[key] = value;
    }
  }
  return new Theme(fgColors, bgColors, colorMode, {
    name: themeJson.name,
    sourcePath
  });
}
__name(createTheme, "createTheme");
function loadThemeFromPath(themePath, mode) {
  const content = fs.readFileSync(themePath, "utf-8");
  const themeJson = parseThemeJsonContent(themePath, content);
  return createTheme(themeJson, mode, themePath);
}
__name(loadThemeFromPath, "loadThemeFromPath");
function loadTheme(name, mode) {
  const registeredTheme = registeredThemes.get(name);
  if (registeredTheme) {
    return registeredTheme;
  }
  const themeJson = loadThemeJson(name);
  return createTheme(themeJson, mode);
}
__name(loadTheme, "loadTheme");
function getThemeByName(name) {
  try {
    return loadTheme(name);
  } catch {
    return void 0;
  }
}
__name(getThemeByName, "getThemeByName");
function parseAutoThemeSetting(themeSetting) {
  if (!themeSetting)
    return void 0;
  const slashIndex = themeSetting.indexOf("/");
  if (slashIndex === -1 || themeSetting.indexOf("/", slashIndex + 1) !== -1) {
    return void 0;
  }
  const lightTheme = themeSetting.slice(0, slashIndex).trim();
  const darkTheme = themeSetting.slice(slashIndex + 1).trim();
  if (!lightTheme || !darkTheme) {
    return void 0;
  }
  return { lightTheme, darkTheme };
}
__name(parseAutoThemeSetting, "parseAutoThemeSetting");
function resolveThemeSetting(themeSetting, terminalTheme) {
  const autoTheme = parseAutoThemeSetting(themeSetting);
  if (autoTheme) {
    return terminalTheme === "light" ? autoTheme.lightTheme : autoTheme.darkTheme;
  }
  if (themeSetting?.includes("/"))
    return void 0;
  if (typeof themeSetting === "string")
    return themeSetting;
  return void 0;
}
__name(resolveThemeSetting, "resolveThemeSetting");
function getColorFgBgBackgroundIndex(colorfgbg) {
  const parts = colorfgbg.split(";");
  for (let i = parts.length - 1; i >= 0; i--) {
    const bg = parseInt(parts[i].trim(), 10);
    if (Number.isInteger(bg) && bg >= 0 && bg <= 255) {
      return bg;
    }
  }
  return void 0;
}
__name(getColorFgBgBackgroundIndex, "getColorFgBgBackgroundIndex");
function getRgbColorLuminance({ r, g, b }) {
  const toLinear = /* @__PURE__ */ __name((channel) => {
    const value = channel / 255;
    return value <= 0.03928 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
  }, "toLinear");
  return 0.2126 * toLinear(r) + 0.7152 * toLinear(g) + 0.0722 * toLinear(b);
}
__name(getRgbColorLuminance, "getRgbColorLuminance");
function getAnsiColorLuminance(index) {
  return getRgbColorLuminance(hexToRgb(ansi256ToHex(index)));
}
__name(getAnsiColorLuminance, "getAnsiColorLuminance");
function getThemeForRgbColor(rgb) {
  return getRgbColorLuminance(rgb) >= 0.5 ? "light" : "dark";
}
__name(getThemeForRgbColor, "getThemeForRgbColor");
function detectTerminalBackgroundFromEnv(options = {}) {
  const env = options.env ?? process.env;
  const colorfgbg = env.COLORFGBG || "";
  const bg = getColorFgBgBackgroundIndex(colorfgbg);
  if (bg !== void 0) {
    return {
      theme: getAnsiColorLuminance(bg) >= 0.5 ? "light" : "dark",
      source: "COLORFGBG",
      detail: `background color index ${bg}`,
      confidence: "high"
    };
  }
  return {
    theme: "dark",
    source: "fallback",
    detail: "no terminal background hint found",
    confidence: "low"
  };
}
__name(detectTerminalBackgroundFromEnv, "detectTerminalBackgroundFromEnv");
async function detectTerminalBackgroundTheme({ ui, timeoutMs, env }) {
  try {
    const rgb = await ui.queryTerminalBackgroundColor({ timeoutMs });
    if (rgb) {
      return {
        theme: getThemeForRgbColor(rgb),
        source: "terminal background",
        detail: `OSC 11 background rgb(${rgb.r}, ${rgb.g}, ${rgb.b})`,
        confidence: "high"
      };
    }
  } catch {
  }
  return detectTerminalBackgroundFromEnv({ env });
}
__name(detectTerminalBackgroundTheme, "detectTerminalBackgroundTheme");
async function detectTerminalThemeForAuto({ ui, timeoutMs, env }) {
  let colorSchemePromise;
  try {
    colorSchemePromise = ui.queryTerminalColorScheme?.({ timeoutMs });
  } catch {
  }
  const backgroundThemePromise = detectTerminalBackgroundTheme({ ui, timeoutMs, env });
  try {
    const colorScheme = await colorSchemePromise;
    if (colorScheme)
      return colorScheme;
  } catch {
  }
  return (await backgroundThemePromise).theme;
}
__name(detectTerminalThemeForAuto, "detectTerminalThemeForAuto");
function getDefaultTheme() {
  return detectTerminalBackgroundFromEnv().theme;
}
__name(getDefaultTheme, "getDefaultTheme");
var THEME_KEY = /* @__PURE__ */ Symbol.for("@earendil-works/pi-coding-agent:theme");
var THEME_KEY_OLD = /* @__PURE__ */ Symbol.for("@mariozechner/pi-coding-agent:theme");
var theme = new Proxy({}, {
  get(_target, prop) {
    const t = globalThis[THEME_KEY];
    if (!t)
      throw new Error("Theme not initialized. Call initTheme() first.");
    return t[prop];
  }
});
function setGlobalTheme(t) {
  globalThis[THEME_KEY] = t;
  globalThis[THEME_KEY_OLD] = t;
}
__name(setGlobalTheme, "setGlobalTheme");
var currentThemeName;
var themeWatcher;
var themeReloadTimer;
var onThemeChangeCallback;
var registeredThemes = /* @__PURE__ */ new Map();
function setRegisteredThemes(themes) {
  registeredThemes.clear();
  for (const theme2 of themes) {
    if (theme2.name) {
      assertThemeNameIsValid(theme2.name);
      registeredThemes.set(theme2.name, theme2);
    }
  }
}
__name(setRegisteredThemes, "setRegisteredThemes");
function initTheme(themeName, enableWatcher = false) {
  const name = themeName ?? getDefaultTheme();
  currentThemeName = name;
  try {
    setGlobalTheme(loadTheme(name));
    if (enableWatcher) {
      startThemeWatcher();
    }
  } catch (_error) {
    currentThemeName = "dark";
    setGlobalTheme(loadTheme("dark"));
  }
}
__name(initTheme, "initTheme");
function setTheme(name, enableWatcher = false) {
  currentThemeName = name;
  try {
    setGlobalTheme(loadTheme(name));
    if (enableWatcher) {
      startThemeWatcher();
    }
    if (onThemeChangeCallback) {
      onThemeChangeCallback();
    }
    return { success: true };
  } catch (error) {
    currentThemeName = "dark";
    setGlobalTheme(loadTheme("dark"));
    return {
      success: false,
      error: error instanceof Error ? error.message : String(error)
    };
  }
}
__name(setTheme, "setTheme");
function setThemeInstance(themeInstance) {
  setGlobalTheme(themeInstance);
  currentThemeName = "<in-memory>";
  stopThemeWatcher();
  if (onThemeChangeCallback) {
    onThemeChangeCallback();
  }
}
__name(setThemeInstance, "setThemeInstance");
function onThemeChange(callback) {
  onThemeChangeCallback = callback;
}
__name(onThemeChange, "onThemeChange");
function startThemeWatcher() {
  stopThemeWatcher();
  if (!currentThemeName || currentThemeName === "dark" || currentThemeName === "light") {
    return;
  }
  const customThemesDir = getCustomThemesDir();
  const watchedThemeName = currentThemeName;
  const watchedFileName = `${watchedThemeName}.json`;
  const themeFile = path.join(customThemesDir, watchedFileName);
  if (!fs.existsSync(themeFile)) {
    return;
  }
  const scheduleReload = /* @__PURE__ */ __name(() => {
    if (themeReloadTimer) {
      clearTimeout(themeReloadTimer);
    }
    themeReloadTimer = setTimeout(() => {
      themeReloadTimer = void 0;
      if (currentThemeName !== watchedThemeName) {
        return;
      }
      if (!fs.existsSync(themeFile)) {
        return;
      }
      try {
        const reloadedTheme = loadThemeFromPath(themeFile);
        registeredThemes.set(watchedThemeName, reloadedTheme);
        setGlobalTheme(reloadedTheme);
        if (onThemeChangeCallback) {
          onThemeChangeCallback();
        }
      } catch (_error) {
      }
    }, 100);
  }, "scheduleReload");
  themeWatcher = watchWithErrorHandler(customThemesDir, (_eventType, filename) => {
    if (currentThemeName !== watchedThemeName) {
      return;
    }
    if (!filename) {
      scheduleReload();
      return;
    }
    if (filename !== watchedFileName) {
      return;
    }
    scheduleReload();
  }, () => {
    closeWatcher(themeWatcher);
    themeWatcher = void 0;
  }) ?? void 0;
}
__name(startThemeWatcher, "startThemeWatcher");
function stopThemeWatcher() {
  if (themeReloadTimer) {
    clearTimeout(themeReloadTimer);
    themeReloadTimer = void 0;
  }
  closeWatcher(themeWatcher);
  themeWatcher = void 0;
}
__name(stopThemeWatcher, "stopThemeWatcher");
function ansi256ToHex(index) {
  const basicColors = [
    "#000000",
    "#800000",
    "#008000",
    "#808000",
    "#000080",
    "#800080",
    "#008080",
    "#c0c0c0",
    "#808080",
    "#ff0000",
    "#00ff00",
    "#ffff00",
    "#0000ff",
    "#ff00ff",
    "#00ffff",
    "#ffffff"
  ];
  if (index < 16) {
    return basicColors[index];
  }
  if (index < 232) {
    const cubeIndex = index - 16;
    const r = Math.floor(cubeIndex / 36);
    const g = Math.floor(cubeIndex % 36 / 6);
    const b = cubeIndex % 6;
    const toHex = /* @__PURE__ */ __name((n) => (n === 0 ? 0 : 55 + n * 40).toString(16).padStart(2, "0"), "toHex");
    return `#${toHex(r)}${toHex(g)}${toHex(b)}`;
  }
  const gray = 8 + (index - 232) * 10;
  const grayHex = gray.toString(16).padStart(2, "0");
  return `#${grayHex}${grayHex}${grayHex}`;
}
__name(ansi256ToHex, "ansi256ToHex");
function getResolvedThemeColors(themeName) {
  const name = themeName ?? currentThemeName ?? getDefaultTheme();
  const isLight = name === "light";
  const themeJson = loadThemeJson(name);
  const resolved = resolveThemeColors(withThemeColorFallbacks(themeJson.colors), themeJson.vars);
  const defaultText = isLight ? "#000000" : "#e5e5e7";
  const cssColors = {};
  for (const [key, value] of Object.entries(resolved)) {
    if (typeof value === "number") {
      cssColors[key] = ansi256ToHex(value);
    } else if (value === "") {
      cssColors[key] = defaultText;
    } else {
      cssColors[key] = value;
    }
  }
  return cssColors;
}
__name(getResolvedThemeColors, "getResolvedThemeColors");
function getThemeExportColors(themeName) {
  const name = themeName ?? currentThemeName ?? getDefaultTheme();
  try {
    const themeJson = loadThemeJson(name);
    const exportSection = themeJson.export;
    if (!exportSection)
      return {};
    const vars = themeJson.vars ?? {};
    const resolve3 = /* @__PURE__ */ __name((value) => {
      if (value === void 0)
        return void 0;
      const resolved = resolveVarRefs(value, vars);
      if (typeof resolved === "number")
        return ansi256ToHex(resolved);
      if (resolved === "")
        return void 0;
      return resolved;
    }, "resolve");
    return {
      pageBg: resolve3(exportSection.pageBg),
      cardBg: resolve3(exportSection.cardBg),
      infoBg: resolve3(exportSection.infoBg)
    };
  } catch {
    return {};
  }
}
__name(getThemeExportColors, "getThemeExportColors");
var cachedHighlightThemeFor;
var cachedCliHighlightTheme;
function buildCliHighlightTheme(t) {
  return {
    keyword: /* @__PURE__ */ __name((s) => t.fg("syntaxKeyword", s), "keyword"),
    built_in: /* @__PURE__ */ __name((s) => t.fg("syntaxType", s), "built_in"),
    literal: /* @__PURE__ */ __name((s) => t.fg("syntaxNumber", s), "literal"),
    number: /* @__PURE__ */ __name((s) => t.fg("syntaxNumber", s), "number"),
    regexp: /* @__PURE__ */ __name((s) => t.fg("syntaxString", s), "regexp"),
    string: /* @__PURE__ */ __name((s) => t.fg("syntaxString", s), "string"),
    comment: /* @__PURE__ */ __name((s) => t.fg("syntaxComment", s), "comment"),
    doctag: /* @__PURE__ */ __name((s) => t.fg("syntaxComment", s), "doctag"),
    meta: /* @__PURE__ */ __name((s) => t.fg("muted", s), "meta"),
    function: /* @__PURE__ */ __name((s) => t.fg("syntaxFunction", s), "function"),
    title: /* @__PURE__ */ __name((s) => t.fg("syntaxFunction", s), "title"),
    class: /* @__PURE__ */ __name((s) => t.fg("syntaxType", s), "class"),
    type: /* @__PURE__ */ __name((s) => t.fg("syntaxType", s), "type"),
    tag: /* @__PURE__ */ __name((s) => t.fg("syntaxPunctuation", s), "tag"),
    name: /* @__PURE__ */ __name((s) => t.fg("syntaxKeyword", s), "name"),
    attr: /* @__PURE__ */ __name((s) => t.fg("syntaxVariable", s), "attr"),
    variable: /* @__PURE__ */ __name((s) => t.fg("syntaxVariable", s), "variable"),
    params: /* @__PURE__ */ __name((s) => t.fg("syntaxVariable", s), "params"),
    operator: /* @__PURE__ */ __name((s) => t.fg("syntaxOperator", s), "operator"),
    punctuation: /* @__PURE__ */ __name((s) => t.fg("syntaxPunctuation", s), "punctuation"),
    emphasis: /* @__PURE__ */ __name((s) => t.italic(s), "emphasis"),
    strong: /* @__PURE__ */ __name((s) => t.bold(s), "strong"),
    link: /* @__PURE__ */ __name((s) => t.underline(s), "link"),
    addition: /* @__PURE__ */ __name((s) => t.fg("toolDiffAdded", s), "addition"),
    deletion: /* @__PURE__ */ __name((s) => t.fg("toolDiffRemoved", s), "deletion")
  };
}
__name(buildCliHighlightTheme, "buildCliHighlightTheme");
function getCliHighlightTheme(t) {
  if (cachedHighlightThemeFor !== t || !cachedCliHighlightTheme) {
    cachedHighlightThemeFor = t;
    cachedCliHighlightTheme = buildCliHighlightTheme(t);
  }
  return cachedCliHighlightTheme;
}
__name(getCliHighlightTheme, "getCliHighlightTheme");
function highlightCode(code, lang) {
  const validLang = lang && supportsLanguage(lang) ? lang : void 0;
  if (!validLang) {
    return code.split("\n").map((line) => theme.fg("mdCodeBlock", line));
  }
  const opts = {
    language: validLang,
    ignoreIllegals: true,
    theme: getCliHighlightTheme(theme)
  };
  try {
    return highlight(code, opts).split("\n");
  } catch {
    return code.split("\n");
  }
}
__name(highlightCode, "highlightCode");
function getLanguageFromPath(filePath) {
  const ext = filePath.split(".").pop()?.toLowerCase();
  if (!ext)
    return void 0;
  const extToLang = {
    ts: "typescript",
    tsx: "typescript",
    js: "javascript",
    jsx: "javascript",
    mjs: "javascript",
    cjs: "javascript",
    py: "python",
    rb: "ruby",
    rs: "rust",
    go: "go",
    java: "java",
    kt: "kotlin",
    swift: "swift",
    c: "c",
    h: "c",
    cpp: "cpp",
    cc: "cpp",
    cxx: "cpp",
    hpp: "cpp",
    cs: "csharp",
    php: "php",
    sh: "bash",
    bash: "bash",
    zsh: "bash",
    fish: "fish",
    ps1: "powershell",
    sql: "sql",
    html: "html",
    htm: "html",
    css: "css",
    scss: "scss",
    sass: "sass",
    less: "less",
    json: "json",
    yaml: "yaml",
    yml: "yaml",
    toml: "toml",
    xml: "xml",
    md: "markdown",
    markdown: "markdown",
    dockerfile: "dockerfile",
    makefile: "makefile",
    cmake: "cmake",
    lua: "lua",
    perl: "perl",
    r: "r",
    scala: "scala",
    clj: "clojure",
    ex: "elixir",
    exs: "elixir",
    erl: "erlang",
    hs: "haskell",
    ml: "ocaml",
    vim: "vim",
    graphql: "graphql",
    proto: "protobuf",
    tf: "hcl",
    hcl: "hcl"
  };
  return extToLang[ext];
}
__name(getLanguageFromPath, "getLanguageFromPath");
function getMarkdownTheme() {
  return {
    heading: /* @__PURE__ */ __name((text) => theme.fg("mdHeading", text), "heading"),
    link: /* @__PURE__ */ __name((text) => theme.fg("mdLink", text), "link"),
    linkUrl: /* @__PURE__ */ __name((text) => theme.fg("mdLinkUrl", text), "linkUrl"),
    code: /* @__PURE__ */ __name((text) => theme.fg("mdCode", text), "code"),
    codeBlock: /* @__PURE__ */ __name((text) => theme.fg("mdCodeBlock", text), "codeBlock"),
    codeBlockBorder: /* @__PURE__ */ __name((text) => theme.fg("mdCodeBlockBorder", text), "codeBlockBorder"),
    quote: /* @__PURE__ */ __name((text) => theme.fg("mdQuote", text), "quote"),
    quoteBorder: /* @__PURE__ */ __name((text) => theme.fg("mdQuoteBorder", text), "quoteBorder"),
    hr: /* @__PURE__ */ __name((text) => theme.fg("mdHr", text), "hr"),
    listBullet: /* @__PURE__ */ __name((text) => theme.fg("mdListBullet", text), "listBullet"),
    bold: /* @__PURE__ */ __name((text) => theme.bold(text), "bold"),
    italic: /* @__PURE__ */ __name((text) => theme.italic(text), "italic"),
    underline: /* @__PURE__ */ __name((text) => theme.underline(text), "underline"),
    strikethrough: /* @__PURE__ */ __name((text) => chalk.strikethrough(text), "strikethrough"),
    highlightCode: /* @__PURE__ */ __name((code, lang) => {
      const validLang = lang && supportsLanguage(lang) ? lang : void 0;
      if (!validLang) {
        return code.split("\n").map((line) => theme.fg("mdCodeBlock", line));
      }
      const opts = {
        language: validLang,
        ignoreIllegals: true,
        theme: getCliHighlightTheme(theme)
      };
      try {
        return highlight(code, opts).split("\n");
      } catch {
        return code.split("\n").map((line) => theme.fg("mdCodeBlock", line));
      }
    }, "highlightCode")
  };
}
__name(getMarkdownTheme, "getMarkdownTheme");
function getSelectListTheme() {
  return {
    selectedPrefix: /* @__PURE__ */ __name((text) => theme.fg("accent", text), "selectedPrefix"),
    selectedText: /* @__PURE__ */ __name((text) => theme.fg("accent", text), "selectedText"),
    description: /* @__PURE__ */ __name((text) => theme.fg("muted", text), "description"),
    scrollInfo: /* @__PURE__ */ __name((text) => theme.fg("muted", text), "scrollInfo"),
    noMatch: /* @__PURE__ */ __name((text) => theme.fg("muted", text), "noMatch")
  };
}
__name(getSelectListTheme, "getSelectListTheme");
function getEditorTheme() {
  return {
    borderColor: /* @__PURE__ */ __name((text) => theme.fg("borderMuted", text), "borderColor"),
    selectList: getSelectListTheme()
  };
}
__name(getEditorTheme, "getEditorTheme");
function getSettingsListTheme() {
  return {
    label: /* @__PURE__ */ __name((text, selected) => selected ? theme.fg("accent", text) : text, "label"),
    value: /* @__PURE__ */ __name((text, selected) => selected ? theme.fg("accent", text) : theme.fg("muted", text), "value"),
    description: /* @__PURE__ */ __name((text) => theme.fg("dim", text), "description"),
    cursor: theme.fg("accent", "\u2192 "),
    hint: /* @__PURE__ */ __name((text) => theme.fg("dim", text), "hint")
  };
}
__name(getSettingsListTheme, "getSettingsListTheme");

// pi-dist/pi-coding-agent/modes/interactive/components/keybinding-hints.js
function formatKeyPart(part, options) {
  const displayPart = process.platform === "darwin" && part.toLowerCase() === "alt" ? "option" : part;
  return options.capitalize ? displayPart.charAt(0).toUpperCase() + displayPart.slice(1) : displayPart;
}
__name(formatKeyPart, "formatKeyPart");
function formatKeyText(key, options = {}) {
  return key.split("/").map((k) => k.split("+").map((part) => formatKeyPart(part, options)).join("+")).join("/");
}
__name(formatKeyText, "formatKeyText");
function formatKeys(keys, options = {}) {
  if (keys.length === 0)
    return "";
  return formatKeyText(keys.join("/"), options);
}
__name(formatKeys, "formatKeys");
function keyText(keybinding) {
  return formatKeys(getKeybindings().getKeys(keybinding));
}
__name(keyText, "keyText");
function keyDisplayText(keybinding) {
  return formatKeys(getKeybindings().getKeys(keybinding), { capitalize: true });
}
__name(keyDisplayText, "keyDisplayText");
function keyHint(keybinding, description) {
  return theme.fg("dim", keyText(keybinding)) + theme.fg("muted", ` ${description}`);
}
__name(keyHint, "keyHint");
function rawKeyHint(key, description) {
  return theme.fg("dim", formatKeyText(key)) + theme.fg("muted", ` ${description}`);
}
__name(rawKeyHint, "rawKeyHint");

// pi-dist/pi-coding-agent/modes/interactive/components/visual-truncate.js
import { Text } from "../../../pi-tui.mjs";
function truncateToVisualLines(text, maxVisualLines, width, paddingX = 0) {
  if (!text) {
    return { visualLines: [], skippedCount: 0 };
  }
  const tempText = new Text(text, paddingX, 0);
  const allVisualLines = tempText.render(width);
  if (allVisualLines.length <= maxVisualLines) {
    return { visualLines: allVisualLines, skippedCount: 0 };
  }
  const truncatedLines = allVisualLines.slice(-maxVisualLines);
  const skippedCount = allVisualLines.length - maxVisualLines;
  return { visualLines: truncatedLines, skippedCount };
}
__name(truncateToVisualLines, "truncateToVisualLines");

// pi-dist/pi-coding-agent/core/tools/render-utils.js
import * as os from "node:os";
import { pathToFileURL } from "node:url";
import { getCapabilities as getCapabilities2, getImageDimensions, hyperlink, imageFallback } from "../../../pi-tui.mjs";

// pi-dist/pi-coding-agent/utils/ansi.js
function ansiRegex({ onlyFirst = false } = {}) {
  const ST = "(?:\\u0007|\\u001B\\u005C|\\u009C)";
  const osc = `(?:\\u001B\\][\\s\\S]*?${ST})`;
  const csi = "[\\u001B\\u009B][[\\]()#;?]*(?:\\d{1,4}(?:[;:]\\d{0,4})*)?[\\dA-PR-TZcf-nq-uy=><~]";
  const pattern = `${osc}|${csi}`;
  return new RegExp(pattern, onlyFirst ? void 0 : "g");
}
__name(ansiRegex, "ansiRegex");
var regex = ansiRegex();
function stripAnsi(value) {
  if (typeof value !== "string") {
    throw new TypeError(`Expected a \`string\`, got \`${typeof value}\``);
  }
  if (!value.includes("\x1B") && !value.includes("\x9B")) {
    return value;
  }
  return value.replace(regex, "");
}
__name(stripAnsi, "stripAnsi");

// pi-dist/pi-coding-agent/core/tools/render-utils.js
function shortenPath(path5) {
  if (typeof path5 !== "string")
    return "";
  const home = os.homedir();
  if (path5.startsWith(home)) {
    return `~${path5.slice(home.length)}`;
  }
  return path5;
}
__name(shortenPath, "shortenPath");
function linkPath(styledText, rawPath, cwd) {
  if (!getCapabilities2().hyperlinks)
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
  const caps = getCapabilities2();
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
function invalidArgText(theme2) {
  return theme2.fg("error", "[invalid arg]");
}
__name(invalidArgText, "invalidArgText");
function renderToolPath(rawPath, theme2, cwd, options) {
  if (rawPath === null)
    return invalidArgText(theme2);
  const value = rawPath || options?.emptyFallback;
  if (!value)
    return theme2.fg("toolOutput", "...");
  return linkPath(theme2.fg("accent", shortenPath(value)), value, cwd);
}
__name(renderToolPath, "renderToolPath");

// pi-dist/pi-coding-agent/core/tools/renderers/bash.js
var BASH_PREVIEW_LINES = 5;
var BASH_UPDATE_THROTTLE_MS = 100;
var BashResultRenderComponent = class extends Container {
  static {
    __name(this, "BashResultRenderComponent");
  }
  state = {
    cachedWidth: void 0,
    cachedLines: void 0,
    cachedSkipped: void 0
  };
};
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
  const state = component.state;
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
      component.addChild(new Text2(`
${styledOutput}`, 0, 0));
    } else {
      component.addChild({
        render: /* @__PURE__ */ __name((width) => {
          if (state.cachedLines === void 0 || state.cachedWidth !== width) {
            const preview = truncateToVisualLines(styledOutput, BASH_PREVIEW_LINES, width);
            state.cachedLines = preview.visualLines;
            state.cachedSkipped = preview.skippedCount;
            state.cachedWidth = width;
          }
          if (state.cachedSkipped && state.cachedSkipped > 0) {
            const hint = theme.fg("muted", `... (${state.cachedSkipped} earlier lines,`) + ` ${keyHint("app.tools.expand", "to expand")}${theme.fg("muted", ")")}`;
            return ["", truncateToWidth(hint, width, "..."), ...state.cachedLines ?? []];
          }
          return ["", ...state.cachedLines ?? []];
        }, "render"),
        invalidate: /* @__PURE__ */ __name(() => {
          state.cachedWidth = void 0;
          state.cachedLines = void 0;
          state.cachedSkipped = void 0;
        }, "invalidate")
      });
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
    component.addChild(new Text2(`
${theme.fg("warning", `[${warnings.join(". ")}]`)}`, 0, 0));
  }
  if (startedAt !== void 0) {
    const label = options.isPartial ? "Elapsed" : "Took";
    const endTime = endedAt ?? Date.now();
    component.addChild(new Text2(`
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
      const text = context.lastComponent ?? new Text2("", 0, 0);
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
      const component = context.lastComponent ?? new BashResultRenderComponent();
      rebuildBashResultRenderComponent(component, result, options, context.showImages, state.startedAt, state.endedAt);
      component.invalidate();
      return component;
    }
  };
}
__name(createShellRenderers, "createShellRenderers");

// pi-dist/pi-coding-agent/core/tools/tool-definition-wrapper.js
function wrapToolDefinition(definition, ctxFactory) {
  return {
    name: definition.name,
    label: definition.label,
    description: definition.description,
    parameters: definition.parameters,
    constrainedSampling: definition.constrainedSampling,
    prepareArguments: definition.prepareArguments,
    executionMode: definition.executionMode,
    execute: /* @__PURE__ */ __name((toolCallId, params, signal, onUpdate, ctx) => definition.execute(toolCallId, params, signal, onUpdate, ctx ?? ctxFactory?.()), "execute")
  };
}
__name(wrapToolDefinition, "wrapToolDefinition");
function createToolDefinitionFromAgentTool(tool) {
  return {
    name: tool.name,
    label: tool.label,
    description: tool.description,
    parameters: tool.parameters,
    constrainedSampling: tool.constrainedSampling,
    prepareArguments: tool.prepareArguments,
    executionMode: tool.executionMode,
    execute: /* @__PURE__ */ __name(async (toolCallId, params, signal, onUpdate) => tool.execute(toolCallId, params, signal, onUpdate), "execute")
  };
}
__name(createToolDefinitionFromAgentTool, "createToolDefinitionFromAgentTool");

// pi-dist/pi-coding-agent/core/tools/bash.js
var MAX_TIMEOUT_MS = 2147483647;
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
function createLocalShellOperations(shellName, resolveShellConfig) {
  return {
    exec: /* @__PURE__ */ __name(async (command, cwd, { onData, signal, timeout, env }) => {
      const timeoutMs = resolveTimeoutMs(timeout);
      if (signal?.aborted) {
        throw new Error("aborted");
      }
      const shellConfig = resolveShellConfig();
      try {
        await fsAccess(cwd, constants2.F_OK);
      } catch {
        throw new Error(`Working directory does not exist: ${cwd}
Cannot execute ${shellName} commands.`);
      }
      const commandFromStdin = shellConfig.commandTransport === "stdin";
      const child = spawn2(shellConfig.shell, commandFromStdin ? shellConfig.args : [...shellConfig.args, command], {
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
        if (exitCode !== 0) {
          throw new Error(appendStatus(outputText, `Command exited with code ${exitCode}`));
        }
        return { content: [{ type: "text", text: outputText }], details };
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
import { constants as constants5 } from "fs";
import { access as fsAccess2, readFile as fsReadFile, writeFile as fsWriteFile } from "fs/promises";
import { Type as Type2 } from "../../../typebox.mjs";

// pi-dist/pi-coding-agent/core/tools/edit-diff.js
import * as Diff from "../../../diff/libesm/index.js";
import { constants as constants4 } from "fs";
import { access as access2, readFile } from "fs/promises";

// pi-dist/pi-coding-agent/core/tools/path-utils.js
import { accessSync as accessSync2, constants as constants3 } from "node:fs";
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
    accessSync2(filePath, constants3.F_OK);
    return true;
  } catch {
    return false;
  }
}
__name(fileExists, "fileExists");
async function pathExists(filePath) {
  try {
    await access(filePath, constants3.F_OK);
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
function getNotFoundError(path5, editIndex, totalEdits) {
  if (totalEdits === 1) {
    return new Error(`Could not find the exact text in ${path5}. The old text must match exactly including all whitespace and newlines.`);
  }
  return new Error(`Could not find edits[${editIndex}] in ${path5}. The oldText must match exactly including all whitespace and newlines.`);
}
__name(getNotFoundError, "getNotFoundError");
function getDuplicateError(path5, editIndex, totalEdits, occurrences) {
  if (totalEdits === 1) {
    return new Error(`Found ${occurrences} occurrences of the text in ${path5}. The text must be unique. Please provide more context to make it unique.`);
  }
  return new Error(`Found ${occurrences} occurrences of edits[${editIndex}] in ${path5}. Each oldText must be unique. Please provide more context to make it unique.`);
}
__name(getDuplicateError, "getDuplicateError");
function getEmptyOldTextError(path5, editIndex, totalEdits) {
  if (totalEdits === 1) {
    return new Error(`oldText must not be empty in ${path5}.`);
  }
  return new Error(`edits[${editIndex}].oldText must not be empty in ${path5}.`);
}
__name(getEmptyOldTextError, "getEmptyOldTextError");
function getNoChangeError(path5, totalEdits) {
  if (totalEdits === 1) {
    return new Error(`No changes made to ${path5}. The replacement produced identical content. This might indicate an issue with special characters or the text not existing as expected.`);
  }
  return new Error(`No changes made to ${path5}. The replacements produced identical content.`);
}
__name(getNoChangeError, "getNoChangeError");
function applyEditsToNormalizedContent(normalizedContent, edits, path5) {
  const normalizedEdits = edits.map((edit) => ({
    oldText: normalizeToLF(edit.oldText),
    newText: normalizeToLF(edit.newText)
  }));
  for (let i = 0; i < normalizedEdits.length; i++) {
    if (normalizedEdits[i].oldText.length === 0) {
      throw getEmptyOldTextError(path5, i, normalizedEdits.length);
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
      throw getNotFoundError(path5, i, normalizedEdits.length);
    }
    const occurrences = countOccurrences(replacementBaseContent, edit.oldText);
    if (occurrences > 1) {
      throw getDuplicateError(path5, i, normalizedEdits.length, occurrences);
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
      throw new Error(`edits[${previous.editIndex}] and edits[${current.editIndex}] overlap in ${path5}. Merge them into one edit or target disjoint regions.`);
    }
  }
  const baseContent = normalizedContent;
  const newContent = usedFuzzyMatch ? applyReplacementsPreservingUnchangedLines(normalizedContent, replacementBaseContent, matchedEdits) : applyReplacements(replacementBaseContent, matchedEdits);
  if (baseContent === newContent) {
    throw getNoChangeError(path5, normalizedEdits.length);
  }
  return { baseContent, newContent };
}
__name(applyEditsToNormalizedContent, "applyEditsToNormalizedContent");
function generateUnifiedPatch(path5, oldContent, newContent, contextLines = 4) {
  return Diff.createTwoFilesPatch(path5, path5, oldContent, newContent, void 0, void 0, {
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
async function computeEditsDiff(path5, edits, cwd) {
  const absolutePath = resolveToCwd(path5, cwd);
  try {
    try {
      await access2(absolutePath, constants4.R_OK);
    } catch (error) {
      const errorMessage = error instanceof Error && "code" in error ? `Error code: ${error.code}` : String(error);
      return { error: `Could not edit file: ${path5}. ${errorMessage}.` };
    }
    const rawContent = await readFile(absolutePath, "utf-8");
    const { text: content } = splitBom(rawContent);
    const normalizedContent = normalizeToLF(content);
    const { baseContent, newContent } = applyEditsToNormalizedContent(normalizedContent, edits, path5);
    return generateDiffString(baseContent, newContent);
  } catch (err) {
    return { error: err instanceof Error ? err.message : String(err) };
  }
}
__name(computeEditsDiff, "computeEditsDiff");

// pi-dist/pi-coding-agent/core/tools/file-mutation-queue.js
import { realpath } from "node:fs/promises";
import { resolve as resolve2 } from "node:path";
var fileMutationQueues = /* @__PURE__ */ new Map();
var registrationQueue = Promise.resolve();
function isMissingPathError(error) {
  return typeof error === "object" && error !== null && "code" in error && (error.code === "ENOENT" || error.code === "ENOTDIR");
}
__name(isMissingPathError, "isMissingPathError");
async function getMutationQueueKey(filePath) {
  const resolvedPath = resolve2(filePath);
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
import { Box, Container as Container2, Spacer, Text as Text3 } from "../../../pi-tui.mjs";

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
  const path5 = typeof args.path === "string" ? args.path : typeof args.file_path === "string" ? args.file_path : null;
  if (!path5) {
    return null;
  }
  if (Array.isArray(args.edits) && args.edits.length > 0 && args.edits.every((edit) => typeof edit?.oldText === "string" && typeof edit?.newText === "string")) {
    return { path: path5, edits: args.edits };
  }
  if (typeof args.oldText === "string" && typeof args.newText === "string") {
    return { path: path5, edits: [{ oldText: args.oldText, newText: args.newText }] };
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
  component.addChild(new Text3(formatEditCall(args, theme2, cwd), 0, 0));
  if (!component.preview) {
    return component;
  }
  const body = "error" in component.preview ? theme2.fg("error", component.preview.error) : renderDiff(component.preview.diff);
  component.addChild(new Spacer(1));
  component.addChild(new Text3(body, 0, 0));
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
    component.addChild(new Spacer(1));
    component.addChild(new Text3(output, 1, 0));
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
  readFile: /* @__PURE__ */ __name((path5) => fsReadFile(path5), "readFile"),
  writeFile: /* @__PURE__ */ __name((path5, content) => fsWriteFile(path5, content, "utf-8"), "writeFile"),
  access: /* @__PURE__ */ __name((path5) => fsAccess2(path5, constants5.R_OK | constants5.W_OK), "access")
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
      const { path: path5, edits } = validateEditInput(input);
      const absolutePath = resolveToCwd(path5, ctx?.cwd || cwd);
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
          throw new Error(`Could not edit file: ${path5}. ${errorMessage}.`);
        }
        throwIfAborted();
        const buffer = await ops.readFile(absolutePath);
        const rawContent = buffer.toString("utf-8");
        throwIfAborted();
        const { bom, text: content } = splitBom(rawContent);
        const originalEnding = detectLineEnding(content);
        const normalizedContent = normalizeToLF(content);
        const { baseContent, newContent } = applyEditsToNormalizedContent(normalizedContent, edits, path5);
        throwIfAborted();
        const finalContent = bom + restoreLineEndings(newContent, originalEnding);
        await ops.writeFile(absolutePath, finalContent);
        throwIfAborted();
        const diffResult = generateDiffString(baseContent, newContent);
        const patch = generateUnifiedPatch(path5, baseContent, newContent);
        return {
          content: [
            {
              type: "text",
              text: `Successfully replaced ${edits.length} block(s) in ${path5}.`
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
import { spawn as spawn3 } from "child_process";
import path2 from "path";
import { Type as Type3 } from "../../../typebox.mjs";

// pi-dist/pi-coding-agent/utils/tools-manager.js
import { spawnSync as spawnSync2 } from "child_process";
import { chmodSync, createWriteStream as createWriteStream2, existsSync as existsSync4, mkdirSync, readdirSync as readdirSync2, renameSync, rmSync } from "fs";
import { arch, platform } from "os";
import { join as join6 } from "path";
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
    const result = spawnSync2(cmd, ["--version"], { stdio: "pipe" });
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
  const localPath = join6(TOOLS_DIR, config.binaryName + (platform() === "win32" ? ".exe" : ""));
  if (existsSync4(localPath)) {
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
  const fileStream = createWriteStream2(dest);
  await pipeline(Readable.fromWeb(response.body), fileStream);
}
__name(downloadFile, "downloadFile");
function findBinaryRecursively(rootDir, binaryFileName) {
  const stack = [rootDir];
  while (stack.length > 0) {
    const currentDir = stack.pop();
    if (!currentDir)
      continue;
    const entries = readdirSync2(currentDir, { withFileTypes: true });
    for (const entry of entries) {
      const fullPath = join6(currentDir, entry.name);
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
  const result = spawnSync2(command, args, { stdio: "pipe" });
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
    const systemTar = join6(systemRoot, "System32", "tar.exe");
    if (existsSync4(systemTar)) {
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
  const archivePath = join6(TOOLS_DIR, assetName);
  const binaryExt = plat === "win32" ? ".exe" : "";
  const binaryPath = join6(TOOLS_DIR, config.binaryName + binaryExt);
  await downloadFile(downloadUrl, archivePath);
  const extractDir = join6(TOOLS_DIR, `extract_tmp_${config.binaryName}_${process.pid}_${Date.now()}_${Math.random().toString(36).slice(2, 10)}`);
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
    const extractedDir = join6(extractDir, assetName.replace(/\.(tar\.gz|zip)$/, ""));
    const extractedBinaryCandidates = [join6(extractedDir, binaryFileName), join6(extractDir, binaryFileName)];
    let extractedBinary = extractedBinaryCandidates.find((candidate) => existsSync4(candidate));
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
    const path5 = await downloadTool(tool);
    onStatus?.({ type: "info", message: `${config.name} installed to ${path5}` });
    return path5;
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
import { Text as Text4 } from "../../../pi-tui.mjs";
function formatFindCall(args, theme2) {
  const pattern = str(args?.pattern);
  const rawPath = str(args?.path);
  const path5 = rawPath !== null ? shortenPath(rawPath || ".") : null;
  const limit = args?.limit;
  const invalidArg = invalidArgText(theme2);
  let text = theme2.fg("toolTitle", theme2.bold("find")) + " " + (pattern === null ? invalidArg : theme2.fg("accent", pattern || "")) + theme2.fg("toolOutput", ` in ${path5 === null ? invalidArg : path5}`);
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
    const text = context.lastComponent ?? new Text4("", 0, 0);
    text.setText(formatFindCall(args, theme2));
    return text;
  },
  renderResult(result, options, theme2, context) {
    const text = context.lastComponent ?? new Text4("", 0, 0);
    text.setText(formatFindResult(result, options, theme2, context.showImages));
    return text;
  }
};

// pi-dist/pi-coding-agent/core/tools/find.js
function relativizeFindResultPath(resultPath, searchPath, pathModule = path2) {
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
      return new Promise((resolve3, reject) => {
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
                settle(() => resolve3({
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
              settle(() => resolve3({
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
              if (await pathExists(path2.join(current, ".git"))) {
                insideGitRepo = true;
                break;
              }
              const parent = path2.dirname(current);
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
            const child = spawn3(fdPath, args, { stdio: ["ignore", "pipe", "pipe"] });
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
                settle(() => resolve3({
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
              settle(() => resolve3({
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
import { spawn as spawn4 } from "child_process";
import path3 from "path";
import { Type as Type4 } from "../../../typebox.mjs";

// pi-dist/pi-coding-agent/core/tools/renderers/grep.js
import { Text as Text5 } from "../../../pi-tui.mjs";
function formatGrepCall(args, theme2) {
  const pattern = str(args?.pattern);
  const rawPath = str(args?.path);
  const path5 = rawPath !== null ? shortenPath(rawPath || ".") : null;
  const glob = str(args?.glob);
  const limit = args?.limit;
  const invalidArg = invalidArgText(theme2);
  let text = theme2.fg("toolTitle", theme2.bold("grep")) + " " + (pattern === null ? invalidArg : theme2.fg("accent", `/${pattern || ""}/`)) + theme2.fg("toolOutput", ` in ${path5 === null ? invalidArg : path5}`);
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
    const text = context.lastComponent ?? new Text5("", 0, 0);
    text.setText(formatGrepCall(args, theme2));
    return text;
  },
  renderResult(result, options, theme2, context) {
    const text = context.lastComponent ?? new Text5("", 0, 0);
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
      return new Promise((resolve3, reject) => {
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
                const relative3 = path3.relative(searchPath, filePath);
                if (relative3 && !relative3.startsWith("..")) {
                  return relative3.replace(/\\/g, "/");
                }
              }
              return path3.basename(filePath);
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
            const child = spawn4(rgPath, args, { stdio: ["ignore", "pipe", "pipe"] });
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
                settle(() => resolve3({ content: [{ type: "text", text: "No matches found" }], details: void 0 }));
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
              settle(() => resolve3({
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
import { Text as Text6 } from "../../../pi-tui.mjs";
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
    const text = context.lastComponent ?? new Text6("", 0, 0);
    text.setText(formatLsCall(args, theme2, context.cwd));
    return text;
  },
  renderResult(result, options, theme2, context) {
    const text = context.lastComponent ?? new Text6("", 0, 0);
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
    async execute(_toolCallId, { path: path5, limit }, signal, _onUpdate, ctx) {
      return new Promise((resolve3, reject) => {
        if (signal?.aborted) {
          reject(new Error("Operation aborted"));
          return;
        }
        const onAbort = /* @__PURE__ */ __name(() => reject(new Error("Operation aborted")), "onAbort");
        signal?.addEventListener("abort", onAbort, { once: true });
        (async () => {
          try {
            const dirPath = resolveToCwd(path5 || ".", ctx?.cwd || cwd);
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
              resolve3({ content: [{ type: "text", text: "(empty directory)" }], details: void 0 });
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
            resolve3({
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
import { constants as constants6 } from "fs";
import { access as fsAccess3, readFile as fsReadFile3 } from "fs/promises";
import { Type as Type6 } from "../../../typebox.mjs";

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
import * as path4 from "path";
import { fileURLToPath as fileURLToPath3 } from "url";
var require2 = createRequire(new URL("../utils/photon.js", import.meta.url).href);
var fs2 = require2("fs");
var WASM_FILENAME = "photon_rs_bg.wasm";
var photonModule = null;
var loadPromise = null;
function pathOrNull(file) {
  if (typeof file === "string") {
    return file;
  }
  if (file instanceof URL) {
    return fileURLToPath3(file);
  }
  return null;
}
__name(pathOrNull, "pathOrNull");
function getFallbackWasmPaths() {
  const execDir = path4.dirname(process.execPath);
  return [
    path4.join(execDir, WASM_FILENAME),
    path4.join(execDir, "photon", WASM_FILENAME),
    path4.join(process.cwd(), WASM_FILENAME)
  ];
}
__name(getFallbackWasmPaths, "getFallbackWasmPaths");
function patchPhotonWasmRead() {
  const originalReadFileSync = fs2.readFileSync.bind(fs2);
  const fallbackPaths = getFallbackWasmPaths();
  const mutableFs = fs2;
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
          if (!fs2.existsSync(fallbackPath)) {
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
    Object.defineProperty(fs2, "readFileSync", {
      value: patchedReadFileSync,
      writable: true,
      configurable: true
    });
  }
  return () => {
    try {
      mutableFs.readFileSync = originalReadFileSync;
    } catch {
      Object.defineProperty(fs2, "readFileSync", {
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
async function convertImageBytesToPng(bytes) {
  const photon = await loadPhoton();
  if (!photon) {
    return null;
  }
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
    return await new Promise((resolve3, reject) => {
      let settled = false;
      const settle = /* @__PURE__ */ __name((result) => {
        if (settled)
          return;
        settled = true;
        resolve3(result);
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
import { open } from "node:fs/promises";
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
  const fileHandle = await open(filePath, "r");
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
import { basename as basename2, dirname as dirname3, isAbsolute as isAbsolute2, relative as relative2, resolve as resolvePath2, sep as sep3 } from "node:path";
import { Text as Text7 } from "../../../pi-tui.mjs";
var COMPACT_RESOURCE_FILE_NAMES = /* @__PURE__ */ new Set(["AGENTS.override.md", "AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"]);
function formatReadLineRange(args, theme2) {
  if (args?.offset === void 0 && args?.limit === void 0)
    return "";
  const startLine = args.offset ?? 1;
  const endLine = args.limit !== void 0 ? startLine + args.limit - 1 : "";
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
  return filePath.split(sep3).join("/");
}
__name(toPosixPath, "toPosixPath");
function getPiDocsClassification(absolutePath) {
  const packageRoot = dirname3(getReadmePath());
  const relativePath = relative2(resolvePath2(packageRoot), resolvePath2(absolutePath));
  if (relativePath === "" || relativePath === ".." || relativePath.startsWith(`..${sep3}`) || isAbsolute2(relativePath)) {
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
  const fileName = basename2(absolutePath);
  if (fileName === "SKILL.md") {
    return { kind: "skill", label: basename2(dirname3(absolutePath)) || fileName };
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
    const text = context.lastComponent ?? new Text7("", 0, 0);
    const classification = !context.expanded ? getCompactReadClassification(args, context.cwd) : void 0;
    text.setText(classification ? formatCompactReadCall(classification, args, theme2) : formatReadCall(args, theme2, context.cwd));
    return text;
  },
  renderResult(result, options, theme2, context) {
    const text = context.lastComponent ?? new Text7("", 0, 0);
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
  readFile: /* @__PURE__ */ __name((path5) => fsReadFile3(path5), "readFile"),
  access: /* @__PURE__ */ __name((path5) => fsAccess3(path5, constants6.R_OK), "access"),
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
    async execute(_toolCallId, { path: path5, offset, limit }, signal, _onUpdate, ctx) {
      return new Promise((resolve3, reject) => {
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
            const absolutePath = await resolveReadPathAsync(path5, ctx?.cwd || cwd);
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
                outputText = `[Line ${startLineDisplay} is ${firstLineSize}, exceeds ${formatSize(DEFAULT_MAX_BYTES)} limit. Use bash: sed -n '${startLineDisplay}p' ${path5} | head -c ${DEFAULT_MAX_BYTES}]`;
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
            resolve3({ content, details });
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
import { dirname as dirname4 } from "path";
import { Type as Type7 } from "../../../typebox.mjs";

// pi-dist/pi-coding-agent/core/tools/renderers/write.js
import { Container as Container3, Text as Text8 } from "../../../pi-tui.mjs";
var WriteCallRenderComponent = class extends Text8 {
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
    const text = context.lastComponent ?? new Text8("", 0, 0);
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
  writeFile: /* @__PURE__ */ __name((path5, content) => fsWriteFile2(path5, content, "utf-8"), "writeFile"),
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
    async execute(_toolCallId, { path: path5, content }, signal, _onUpdate, ctx) {
      const absolutePath = resolveToCwd(path5, ctx?.cwd || cwd);
      const dir = dirname4(absolutePath);
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
          content: [{ type: "text", text: `Successfully wrote to ${path5}` }],
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
  spawnProcess,
  spawnProcessSync,
  waitForChildProcess,
  canonicalizePath,
  getFileRevision,
  isLocalPath,
  normalizePath,
  resolvePath,
  getCwdRelativePath,
  markPathIgnoredByCloudSync,
  stripBom,
  isBunBinary,
  isBundledNode,
  detectInstallMethod,
  getSelfUpdateCommand,
  getSelfUpdateUnavailableInstruction,
  getPackageDir,
  getExportTemplateDir,
  getReadmePath,
  getDocsPath,
  getExamplesPath,
  getChangelogPath,
  getBundledInteractiveAssetPath,
  PACKAGE_NAME,
  APP_NAME,
  APP_TITLE,
  VERSION,
  ENV_SESSION_DIR,
  expandTildePath,
  getShareViewerUrl,
  getAgentDir,
  getAuthPath,
  getSettingsPath,
  getBinDir,
  getSessionsDir,
  getDebugLogPath,
  CONFIG_DIR_NAME,
  ENV_AGENT_DIR,
  FS_WATCH_RETRY_DELAY_MS,
  closeWatcher,
  watchWithErrorHandler,
  setThemeJsonValidator,
  Theme,
  getAvailableThemes,
  getAvailableThemesWithPaths,
  loadThemeFromPath,
  getThemeByName,
  parseAutoThemeSetting,
  resolveThemeSetting,
  detectTerminalBackgroundFromEnv,
  detectTerminalBackgroundTheme,
  detectTerminalThemeForAuto,
  theme,
  setRegisteredThemes,
  initTheme,
  setTheme,
  setThemeInstance,
  onThemeChange,
  stopThemeWatcher,
  getResolvedThemeColors,
  getThemeExportColors,
  highlightCode,
  getLanguageFromPath,
  getMarkdownTheme,
  getSelectListTheme,
  getEditorTheme,
  getSettingsListTheme,
  loadPhoton,
  convertToPng,
  resizeImage,
  formatDimensionNote,
  processImage,
  stripAnsi,
  getShellConfig,
  getPowerShellConfig,
  sanitizeBinaryOutput,
  killTrackedDetachedChildren,
  DEFAULT_MAX_LINES,
  DEFAULT_MAX_BYTES,
  formatSize,
  truncateHead,
  truncateTail,
  truncateLine,
  wrapToolDefinition,
  createToolDefinitionFromAgentTool,
  formatKeyText,
  keyText,
  keyDisplayText,
  keyHint,
  rawKeyHint,
  truncateToVisualLines,
  getTextOutput,
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
