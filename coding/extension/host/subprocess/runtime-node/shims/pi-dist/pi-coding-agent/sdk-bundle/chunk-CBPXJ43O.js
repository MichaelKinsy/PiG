import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-coding-agent/utils/paths.js
import { realpathSync, statSync } from "node:fs";
import { homedir } from "node:os";
import { isAbsolute, join, resolve as nodeResolvePath, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";

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
  return new Promise((resolve2, reject) => {
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
      resolve2(code);
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

// pi-dist/pi-coding-agent/utils/paths.js
var UNICODE_SPACES = /[\u00A0\u2000-\u200A\u202F\u205F\u3000]/g;
function canonicalizePath(path2) {
  try {
    return realpathSync(path2);
  } catch {
    return path2;
  }
}
__name(canonicalizePath, "canonicalizePath");
function getFileRevision(path2) {
  try {
    const stats = statSync(path2, { bigint: true });
    return `${stats.dev}:${stats.ino}:${stats.size}:${stats.mtimeNs}:${stats.ctimeNs}`;
  } catch {
    return void 0;
  }
}
__name(getFileRevision, "getFileRevision");
function isLocalPath(value) {
  const trimmed = value.trim();
  if (trimmed.startsWith("npm:") || trimmed.startsWith("git:") || trimmed.startsWith("github:") || trimmed.startsWith("http:") || trimmed.startsWith("https:") || trimmed.startsWith("ssh:") || trimmed.startsWith("builtin:")) {
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
function markPathIgnoredByCloudSync(path2) {
  const attrs = process.platform === "darwin" ? ["com.dropbox.ignored", "com.apple.fileprovider.ignore#P"] : process.platform === "linux" ? ["user.com.dropbox.ignored"] : [];
  for (const attr of attrs) {
    if (process.platform === "darwin") {
      spawnProcessSync("xattr", ["-w", attr, "1", path2], { encoding: "utf-8", stdio: "ignore" });
    } else {
      spawnProcessSync("setfattr", ["-n", attr, "-v", "1", path2], { encoding: "utf-8", stdio: "ignore" });
    }
  }
}
__name(markPathIgnoredByCloudSync, "markPathIgnoredByCloudSync");

// pi-dist/pi-coding-agent/config.js
import { accessSync, constants, existsSync, readFileSync, realpathSync as realpathSync2 } from "fs";
import { homedir as homedir2 } from "os";
import { basename, dirname, join as join2, resolve, sep as sep2, win32 } from "path";
import { fileURLToPath as fileURLToPath2 } from "url";

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
  const path2 = process.platform === "win32" || packageDir.includes("\\") ? win32 : { basename, dirname };
  const parent = path2.dirname(packageDir);
  let root;
  if (path2.basename(parent).startsWith("@") && path2.basename(path2.dirname(parent)) === "node_modules") {
    root = path2.dirname(parent);
  } else if (path2.basename(parent) === "node_modules") {
    root = parent;
  }
  if (!root)
    return void 0;
  const rootParent = path2.dirname(root);
  if (path2.basename(rootParent) === "lib")
    return { root, prefix: path2.dirname(rootParent) };
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
function normalizeExistingPathForComparison(path2, resolveSymlinks) {
  const resolvedPath = resolve(path2);
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
function getPathComparisonCandidates(path2) {
  return Array.from(new Set([normalizeExistingPathForComparison(path2, false), normalizeExistingPathForComparison(path2, true)].filter((candidate) => !!candidate)));
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
var embeddedQuickJSWasmPath;
function getQuickJSWasmPath() {
  return embeddedQuickJSWasmPath ?? fileURLToPath2(new URL("../../quickjs-wasi/quickjs.wasm", new URL("../config.js", import.meta.url).href));
}
__name(getQuickJSWasmPath, "getQuickJSWasmPath");
function resolveCodemodeWorkerSpecifier(runtime, moduleUrl) {
  if (runtime === "bun-binary")
    return "./src/extensions/codemode/worker.ts";
  if (runtime === "bundled-node")
    return new URL("./extensions/codemode/worker.js", moduleUrl);
  return void 0;
}
__name(resolveCodemodeWorkerSpecifier, "resolveCodemodeWorkerSpecifier");
function getCodemodeWorkerSpecifier() {
  const runtime = isBunBinary ? "bun-binary" : isBundledNode ? "bundled-node" : "unbundled";
  return resolveCodemodeWorkerSpecifier(runtime, new URL("../config.js", import.meta.url).href);
}
__name(getCodemodeWorkerSpecifier, "getCodemodeWorkerSpecifier");
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
function expandTildePath(path2) {
  return normalizePath(path2);
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

// pi-dist/pi-coding-agent/modes/interactive/theme/theme.js
import * as fs from "node:fs";
import * as path from "node:path";
import { backgroundAnsi, colorToHex, colorToOklch as colorToOklch2, foregroundAnsi, indexedColor, mixColors, parseColor, rgbColor as rgbColor2, styleTextWithAnsi } from "../../pi-tui/colors.js";
import { getTerminalColorMode } from "../../pi-tui/terminal-image.js";
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
function watchWithErrorHandler(path2, listener, onError) {
  try {
    const watcher = watch(path2, listener);
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

// pi-dist/pi-coding-agent/modes/interactive/theme/system-theme.js
import { colorToOkhsl, colorToOklch, okhslColor, rgbColor } from "../../pi-tui/colors.js";
import { oklabToOkhslLightness } from "../../pi-tui/oklab.js";
var SYSTEM_THEME_NAME = "system";
var FAMILIES = {
  neutral: { hue: 231.49, saturation: { min: 0.02, max: 0.08 }, slot: 8 },
  blue: { hue: 231.49, saturation: { min: 0.1, max: 0.68 }, slot: 4 },
  green: { hue: 158.68, saturation: { min: 0.1, max: 0.76 }, slot: 2 },
  red: { hue: 20, saturation: { min: 0.1, max: 0.92 }, slot: 1 },
  yellow: { hue: 82.36, saturation: { min: 0.5, max: 1 }, slot: 3 },
  orange: { hue: 52, saturation: { min: 0.12, max: 0.85 }, slot: 3 },
  violet: { hue: 295, saturation: { min: 0.2, max: 0.6 }, slot: 5 },
  calamine: { hue: 202.43, saturation: { min: 0.1, max: 0.74 }, slot: 6 },
  thinkingSlate: { hue: 231.49, saturation: { min: 0.08, max: 0.2 }, slot: 4 },
  thinkingBlue: { hue: 231.49, saturation: { min: 0.2, max: 0.45 }, slot: 4 },
  thinkingPeriwinkle: { hue: 263.25, saturation: { min: 0.3, max: 0.6 }, slot: 6 },
  thinkingViolet: { hue: 295, saturation: { min: 0.4, max: 0.75 }, slot: 5 },
  thinkingMagenta: { hue: 337.5, saturation: { min: 0.5, max: 0.85 }, slot: 13 },
  thinkingRed: { hue: 20, saturation: { min: 0.95, max: 1 }, slot: 1 }
};
var TOKEN_FAMILIES = {
  selectedBg: "blue",
  searchMatchBg: "orange",
  userMessageBg: "blue",
  customMessageBg: "violet",
  toolPendingBg: "neutral",
  toolSuccessBg: "green",
  toolErrorBg: "red",
  text: "neutral",
  userMessageText: "neutral",
  customMessageText: "neutral",
  toolTitle: "neutral",
  syntaxOperator: "neutral",
  syntaxPunctuation: "neutral",
  muted: "neutral",
  dim: "neutral",
  thinkingText: "neutral",
  toolOutput: "neutral",
  mdLinkUrl: "neutral",
  mdQuote: "neutral",
  mdQuoteBorder: "neutral",
  mdHr: "neutral",
  mdCodeBlockBorder: "neutral",
  toolDiffContext: "neutral",
  syntaxComment: "neutral",
  scrollbarTrack: "neutral",
  scrollbarThumb: "neutral",
  searchMatchText: "neutral",
  borderMuted: "neutral",
  accent: "violet",
  borderAccent: "violet",
  customMessageLabel: "violet",
  mdCode: "violet",
  mdListBullet: "violet",
  syntaxType: "violet",
  border: "blue",
  mdLink: "blue",
  syntaxKeyword: "blue",
  syntaxVariable: "calamine",
  success: "green",
  mdCodeBlock: "green",
  toolDiffAdded: "green",
  bashMode: "green",
  syntaxNumber: "green",
  error: "red",
  toolDiffRemoved: "red",
  warning: "yellow",
  mdHeading: "yellow",
  syntaxFunction: "yellow",
  syntaxString: "orange",
  thinkingOff: "neutral",
  thinkingMinimal: "thinkingSlate",
  thinkingLow: "thinkingBlue",
  thinkingMedium: "thinkingPeriwinkle",
  thinkingHigh: "thinkingViolet",
  thinkingXhigh: "thinkingMagenta",
  thinkingMax: "thinkingRed"
};
var TOKEN_SLOTS = { syntaxString: 2, syntaxNumber: 5, searchMatchBg: 3 };
var LEVELS = {
  panel: {
    dark: { coefficients: [0.29131, -0.39746, 2.33185, -0.85524, -1.2076, 0.86276], reachable: [0, 0.979] },
    light: { coefficients: [-3.74073, 27.94549, -78.44258, 112.6798, -79.60015, 22.11277], reachable: [0.348, 1] }
  },
  track: {
    dark: { coefficients: [0.39028, -0.23015, 0.83573, 2.43829, -4.38292, 2.01582], reachable: [0, 0.946] },
    light: { coefficients: [-5.24921, 38.37322, -107.28833, 152.10005, -106.17127, 29.18061], reachable: [0.368, 1] }
  },
  thinking0: {
    dark: { coefficients: [0.52988, -0.05809, -0.30924, 4.63567, -6.52933, 2.89108], reachable: [0, 0.873] },
    light: {
      coefficients: [-28.27749, 182.85284, -469.62416, 603.15916, -384.59976, 97.35147],
      reachable: [0.51, 1]
    }
  },
  thinking1: {
    dark: { coefficients: [0.55278, -0.03667, -0.45659, 4.95347, -6.90265, 3.0706], reachable: [0, 0.858] },
    light: {
      coefficients: [-37.10484, 235.86282, -596.62344, 754.3633, -474.00763, 118.3551],
      reachable: [0.535, 1]
    }
  },
  thinking2: {
    dark: { coefficients: [0.57486, -0.01765, -0.58987, 5.25227, -7.27175, 3.25532], reachable: [0, 0.842] },
    light: {
      coefficients: [-59.89653, 377.05024, -945.07843, 1182.03145, -734.96375, 181.68658],
      reachable: [0.556, 1]
    }
  },
  thinking3: {
    dark: { coefficients: [0.59621, -62e-5, -0.71148, 5.53588, -7.6392, 3.44606], reachable: [0, 0.827] },
    light: {
      coefficients: [-72.07122, 445.84082, -1099.57352, 1353.88793, -829.53392, 202.26164],
      reachable: [0.58, 1]
    }
  },
  thinking4: {
    dark: { coefficients: [0.61691, 0.01462, -0.82288, 5.80651, -8.00641, 3.64333], reachable: [0, 0.811] },
    light: {
      coefficients: [-110.14338, 674.21488, -1645.75941, 2004.32367, -1215.15899, 293.3183],
      reachable: [0.6, 1]
    }
  },
  thinking5: {
    dark: { coefficients: [0.63702, 0.02826, -0.92498, 6.06465, -8.37246, 3.84651], reachable: [0, 0.795] },
    light: {
      coefficients: [-175.47701, 1063.54495, -2570.70594, 3098.80776, -1860.15527, 444.76392],
      reachable: [0.62, 1]
    }
  },
  thinking6: {
    dark: { coefficients: [0.65658, 0.04044, -1.01835, 6.30989, -8.73529, 4.05439], reachable: [0, 0.779] },
    light: {
      coefficients: [-183.81712, 1094.70055, -2602.68539, 3088.71276, -1826.91131, 430.75931],
      reachable: [0.643, 1]
    }
  },
  subtle: {
    dark: { coefficients: [0.56762, -0.02475, -0.5383, 5.12628, -7.10931, 3.17324], reachable: [0, 0.848] },
    light: {
      coefficients: [-232.85459, 1376.54473, -3249.11801, 3827.91186, -2248.29472, 526.55751],
      reachable: [0.657, 1]
    }
  },
  thumb: {
    dark: { coefficients: [0.60323, 278e-5, -0.73328, 5.57157, -7.68067, 3.46933], reachable: [0, 0.823] },
    light: {
      coefficients: [-82.89897, 511.01355, -1255.98095, 1540.76821, -940.68087, 228.58523],
      reachable: [0.586, 1]
    }
  },
  readable: {
    dark: { coefficients: [0.66937, 0.04704, -1.06871, 6.43941, -8.9332, 4.17229], reachable: [0, 0.77] },
    light: {
      coefficients: [-1554.52576, 8733.56817, -19604.93507, 21977.72696, -12300.99599, 2749.81288],
      reachable: [0.751, 1]
    }
  },
  emphasis: {
    dark: { coefficients: [0.7303, 0.07695, -1.31626, 7.1681, -10.14436, 4.92846], reachable: [0, 0.712] },
    light: {
      coefficients: [-4948.31942, 26870.91986, -58334.48399, 63280.17197, -34298.01053, 7430.30146],
      reachable: [0.811, 1]
    }
  },
  textOnPanel: {
    dark: { coefficients: [0.86713, 0.05232, -0.89428, 4.79014, -5.5432, 1.75023], reachable: [0, 0.542] },
    light: {
      coefficients: [-8570.89457, 43954.60805, -90084.00702, 92220.6791, -47152.15802, 9632.27113],
      reachable: [0.867, 1]
    }
  },
  text: {
    dark: { coefficients: [0.89242, 0.02311, -0.44862, 2.34417, -0.06084, -2.63844], reachable: [0, 0.5] },
    light: {
      coefficients: [-2004.67048, 6664.47299, -6060.70202, -1792.61209, 5133.82359, -1939.85583],
      reachable: [0.894, 1]
    }
  }
};
var TOOL_PANELS = ["toolPendingBg", "toolSuccessBg", "toolErrorBg"];
var MESSAGE_PANELS = ["userMessageBg", "customMessageBg"];
var PANELS = [
  "userMessageBg",
  "toolPendingBg",
  "toolSuccessBg",
  "toolErrorBg",
  "selectedBg",
  "searchMatchBg",
  "customMessageBg"
];
var THINKING = [
  "thinkingOff",
  "thinkingMinimal",
  "thinkingLow",
  "thinkingMedium",
  "thinkingHigh",
  "thinkingXhigh",
  "thinkingMax"
];
var THINKING_LEVELS = [
  "thinking0",
  "thinking1",
  "thinking2",
  "thinking3",
  "thinking4",
  "thinking5",
  "thinking6"
];
var each = /* @__PURE__ */ __name((tokens, on, level) => tokens.map((token) => ({ token, on, level })), "each");
var RULES = [
  ...each(PANELS, ["background"], "panel"),
  { token: "text", on: ["background"], level: "text" },
  { token: "text", on: ["selectedBg"], level: "textOnPanel" },
  { token: "userMessageText", on: ["userMessageBg"], level: "textOnPanel" },
  { token: "toolTitle", on: TOOL_PANELS, level: "textOnPanel" },
  ...each(["accent", "success", "error", "warning"], ["background", "selectedBg", ...TOOL_PANELS], "readable"),
  { token: "muted", on: ["background", "selectedBg", "customMessageBg", ...TOOL_PANELS], level: "readable" },
  { token: "dim", on: ["background", "selectedBg", "customMessageBg", ...TOOL_PANELS], level: "subtle" },
  { token: "thinkingText", on: ["background"], level: "readable" },
  { token: "customMessageText", on: ["customMessageBg", ...TOOL_PANELS], level: "readable" },
  {
    token: "customMessageLabel",
    on: ["background", "customMessageBg", "selectedBg", ...TOOL_PANELS],
    level: "readable"
  },
  { token: "toolOutput", on: ["background", ...TOOL_PANELS], level: "readable" },
  ...each(["mdHeading", "mdLink", "mdLinkUrl", "mdCode", "mdQuote", "mdCodeBlockBorder", "mdListBullet"], ["background", ...MESSAGE_PANELS], "readable"),
  { token: "mdCodeBlock", on: ["background", ...MESSAGE_PANELS, ...TOOL_PANELS], level: "readable" },
  ...each(["toolDiffAdded", "toolDiffRemoved", "toolDiffContext"], ["background", ...TOOL_PANELS], "readable"),
  ...each([
    "syntaxComment",
    "syntaxKeyword",
    "syntaxFunction",
    "syntaxVariable",
    "syntaxString",
    "syntaxNumber",
    "syntaxType",
    "syntaxOperator",
    "syntaxPunctuation"
  ], ["background", ...MESSAGE_PANELS, ...TOOL_PANELS], "readable"),
  { token: "searchMatchText", on: ["searchMatchBg"], level: "readable" },
  ...each(["bashMode", "border", "borderAccent"], ["background"], "readable"),
  { token: "borderMuted", on: ["background"], level: "subtle" },
  ...each(["mdQuoteBorder", "mdHr"], ["background", ...MESSAGE_PANELS, ...TOOL_PANELS], "readable"),
  { token: "scrollbarTrack", on: ["background"], level: "track" },
  { token: "scrollbarThumb", on: ["scrollbarTrack"], level: "thumb" },
  ...THINKING.map((token, index) => ({ token, on: ["background"], level: THINKING_LEVELS[index] }))
];
var READABLE_FLOOR = { dark: "readable", light: "subtle" };
var FOREGROUND_LEVEL = "emphasis";
var FOREGROUND_TOKENS = ["text", "userMessageText", "toolTitle"];
var TEXT_MINIMUM_WCAG_CONTRAST = 4.5;
var SOLVE_ORDER = (() => {
  const order = [];
  const visit = /* @__PURE__ */ __name((token) => {
    if (order.includes(token))
      return;
    for (const rule of RULES) {
      if (rule.token !== token)
        continue;
      for (const surface of rule.on)
        if (surface !== "background")
          visit(surface);
    }
    order.push(token);
  }, "visit");
  for (const rule of RULES)
    visit(rule.token);
  return order;
})();
function oklabLightness(color) {
  return colorToOklch(rgbColor(color.r, color.g, color.b)).l;
}
__name(oklabLightness, "oklabLightness");
function relativeLuminance({ r, g, b }) {
  const linear = /* @__PURE__ */ __name((channel) => {
    const value = channel / 255;
    return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
  }, "linear");
  return 0.2126 * linear(r) + 0.7152 * linear(g) + 0.0722 * linear(b);
}
__name(relativeLuminance, "relativeLuminance");
function wcagContrast(first, second) {
  const a = relativeLuminance(first);
  const b = relativeLuminance(second);
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
}
__name(wcagContrast, "wcagContrast");
function terminalAppearance(background, foreground) {
  const white = { r: 255, g: 255, b: 255 };
  const black = { r: 0, g: 0, b: 0 };
  const whiteContrast = wcagContrast(white, background);
  const blackContrast = wcagContrast(black, background);
  if (foreground) {
    const foregroundL = oklabLightness(foreground);
    const backgroundL = oklabLightness(background);
    if (Math.abs(foregroundL - backgroundL) > 0.05) {
      const appearance = foregroundL > backgroundL ? "dark" : "light";
      const best = appearance === "dark" ? whiteContrast : blackContrast;
      if (best >= TEXT_MINIMUM_WCAG_CONTRAST)
        return appearance;
    }
  }
  return whiteContrast >= blackContrast ? "dark" : "light";
}
__name(terminalAppearance, "terminalAppearance");
var clamp = /* @__PURE__ */ __name((value, min, max) => Math.min(max, Math.max(min, value)), "clamp");
function hexOf({ r, g, b }) {
  return `#${[r, g, b].map((channel) => Math.round(channel).toString(16).padStart(2, "0")).join("")}`;
}
__name(hexOf, "hexOf");
function bellWeight(lightness) {
  const gaussian = /* @__PURE__ */ __name((x) => Math.exp(-((x - 0.5) ** 2) / (2 * 0.25 ** 2)), "gaussian");
  return (gaussian(lightness) - gaussian(0)) / (1 - gaussian(0));
}
__name(bellWeight, "bellWeight");
function saturationCurve({ saturation: { min, max } }, lightness) {
  const floor = max > 0 ? min / max : 1;
  return floor + (1 - floor) * bellWeight(lightness);
}
__name(saturationCurve, "saturationCurve");
function levelTarget(level, appearance, surfaceL) {
  const curve = LEVELS[level][appearance];
  if (surfaceL < curve.reachable[0] || surfaceL > curve.reachable[1])
    return void 0;
  return curve.coefficients.reduce((sum, coefficient, power) => sum + coefficient * surfaceL ** power, 0);
}
__name(levelTarget, "levelTarget");
function generateSystemThemeColors(input) {
  const saturation = clamp(input.saturation ?? 1, 0, 1);
  const { background, foreground } = input;
  if (!background)
    return indexedColors(saturation, input.appearanceHint);
  const palette = input.palette?.length === 16 ? input.palette.map(okhslOf) : void 0;
  const appearance = terminalAppearance(background, foreground);
  const lighter = appearance === "dark";
  const extreme = lighter ? 1 : 0;
  const backgroundL = oklabLightness(background);
  const paint = /* @__PURE__ */ __name((token, oklabL) => {
    const lightness = oklabToOkhslLightness(oklabL);
    const family = FAMILIES[TOKEN_FAMILIES[token]];
    if (!palette) {
      const { min, max } = family.saturation;
      return okhslColor(family.hue, (min + (max - min) * bellWeight(lightness)) * saturation, lightness);
    }
    return anchored(palette[TOKEN_SLOTS[token] ?? family.slot], family, lightness, saturation);
  }, "paint");
  const target = /* @__PURE__ */ __name((level, surfaceL, t) => {
    const reached = levelTarget(level, appearance, surfaceL);
    if (reached === void 0 && t === 0)
      return void 0;
    const distance = (reached ?? extreme) - surfaceL;
    const floor = (levelTarget(READABLE_FLOOR[appearance], appearance, surfaceL) ?? extreme) - surfaceL;
    const compressed = Math.abs(distance) > Math.abs(floor) ? distance - (distance - floor) * Math.min(t, 1) : distance;
    return surfaceL + compressed * (1 - Math.max(0, t - 1));
  }, "target");
  const extremeText = lighter ? { r: 255, g: 255, b: 255 } : { r: 0, g: 0, b: 0 };
  const readable = /* @__PURE__ */ __name((color) => wcagContrast(extremeText, color) >= TEXT_MINIMUM_WCAG_CONTRAST, "readable");
  const limitPanel = /* @__PURE__ */ __name((token, l) => {
    const color = paint(token, l);
    if (readable(color))
      return color;
    let [low, high] = [backgroundL, l];
    for (let index = 0; index < 20; index++) {
      const middle = (low + high) / 2;
      if (readable(paint(token, middle)))
        low = middle;
      else
        high = middle;
    }
    return paint(token, low);
  }, "limitPanel");
  const solve = /* @__PURE__ */ __name((t) => {
    const colors2 = /* @__PURE__ */ new Map([["background", background]]);
    for (const token of SOLVE_ORDER) {
      const targets = [];
      for (const rule of RULES) {
        if (rule.token !== token)
          continue;
        for (const surface of rule.on) {
          const value = target(rule.level, oklabLightness(colors2.get(surface) ?? background), t);
          if (value === void 0 || value < 0 || value > 1)
            return void 0;
          targets.push(value);
        }
      }
      const l = lighter ? Math.max(...targets) : Math.min(...targets);
      colors2.set(token, PANELS.includes(token) ? limitPanel(token, l) : paint(token, l));
    }
    return colors2;
  }, "solve");
  let relaxation = 0;
  let colors = solve(0);
  if (!colors) {
    let [low, high] = [0, 2];
    colors = solve(high);
    for (let index = 0; index < 20; index++) {
      const middle = (low + high) / 2;
      const attempt = solve(middle);
      if (attempt)
        [high, colors] = [middle, attempt];
      else
        low = middle;
    }
    relaxation = high;
  }
  const solved = colors ?? /* @__PURE__ */ new Map();
  const surfacesOf = /* @__PURE__ */ __name((token) => RULES.filter((rule) => rule.token === token).flatMap((rule) => rule.on.map((surface) => solved.get(surface) ?? background)), "surfacesOf");
  const result = {};
  for (const token of Object.keys(TOKEN_FAMILIES)) {
    const color = solved.get(token);
    result[token] = color ? hexOf(color) : "";
  }
  for (const token of FOREGROUND_TOKENS) {
    const surfaces = surfacesOf(token);
    let text = solved.get(token);
    if (foreground) {
      const targets = surfaces.map((surface) => target(FOREGROUND_LEVEL, oklabLightness(surface), relaxation));
      if (targets.every((value) => value !== void 0 && value >= 0 && value <= 1)) {
        const needed = lighter ? Math.max(...targets) : Math.min(...targets);
        const foregroundL = oklabLightness(foreground);
        if (lighter ? foregroundL >= needed : foregroundL <= needed) {
          result[token] = "";
          continue;
        }
        text = anchored(okhslOf(foreground), FAMILIES.neutral, oklabToOkhslLightness(needed), saturation);
      }
    }
    if (text)
      result[token] = hexOf(withTextContrast(text, surfaces, lighter));
  }
  return { colors: result, dim: [], appearance };
}
__name(generateSystemThemeColors, "generateSystemThemeColors");
function okhslOf({ r, g, b }) {
  return colorToOkhsl(rgbColor(r, g, b));
}
__name(okhslOf, "okhslOf");
function anchored(source, family, lightness, saturation) {
  const anchor = saturationCurve(family, source.l);
  const falloff = anchor > 0 ? Math.min(1, saturationCurve(family, lightness) / anchor) : 1;
  return okhslColor(source.h, source.s * falloff * saturation, lightness);
}
__name(anchored, "anchored");
function withTextContrast(color, surfaces, lighter) {
  const meets = /* @__PURE__ */ __name((candidate) => surfaces.every((surface) => wcagContrast(candidate, surface) >= TEXT_MINIMUM_WCAG_CONTRAST), "meets");
  if (meets(color))
    return color;
  const { h, s, l } = okhslOf(color);
  const at = /* @__PURE__ */ __name((lightness) => okhslColor(h, s, lightness), "at");
  const extreme = lighter ? 1 : 0;
  if (!meets(at(extreme)))
    return at(extreme);
  let [low, high] = [l, extreme];
  for (let index = 0; index < 20; index++) {
    const middle = (low + high) / 2;
    if (meets(at(middle)))
      high = middle;
    else
      low = middle;
  }
  return at(high);
}
__name(withTextContrast, "withTextContrast");
function indexedColors(saturation, appearance) {
  const colors = {};
  const dim = [];
  for (const [token, familyName] of Object.entries(TOKEN_FAMILIES)) {
    if (PANELS.includes(token)) {
      colors[token] = "";
      continue;
    }
    const neutral = familyName === "neutral";
    colors[token] = !neutral && saturation > 0 ? TOKEN_SLOTS[token] ?? FAMILIES[familyName].slot : "";
    if (neutral && !FOREGROUND_TOKENS.includes(token))
      dim.push(token);
  }
  return { colors, dim, appearance };
}
__name(indexedColors, "indexedColors");

// pi-dist/pi-coding-agent/modes/interactive/theme/theme.js
var themeJsonValidator;
function setThemeJsonValidator(validator) {
  themeJsonValidator = validator;
}
__name(setThemeJsonValidator, "setThemeJsonValidator");
function resolveVarRefs(value, vars, visited = /* @__PURE__ */ new Set()) {
  if (typeof value === "number" || value === "" || value.startsWith("#") || /^ok(lch|hsl)\(/i.test(value)) {
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
var terminalColors = {};
var terminalColorsPending = false;
var terminalColorScheme;
function setTerminalColors(colors) {
  terminalColors = { ...colors };
  terminalColorsPending = false;
}
__name(setTerminalColors, "setTerminalColors");
function setTerminalColorScheme(scheme) {
  terminalColorScheme = scheme;
}
__name(setTerminalColorScheme, "setTerminalColorScheme");
function markTerminalColorsPending() {
  terminalColorsPending = true;
}
__name(markTerminalColorsPending, "markTerminalColorsPending");
var GUESSED_DEFAULT_COLORS = {
  dark: { foreground: parseColor("#e5e5e7"), background: parseColor("#000000") },
  light: { foreground: parseColor("#000000"), background: parseColor("#ffffff") }
};
function averageLightness(colors) {
  const fixed = colors.filter((color) => color.kind !== "indexed" || color.index >= 16);
  if (fixed.length === 0)
    return void 0;
  return fixed.reduce((sum, color) => sum + colorToOklch2(color).l, 0) / fixed.length;
}
__name(averageLightness, "averageLightness");
function detectAppearance(foregrounds, backgrounds) {
  const fg = averageLightness(foregrounds);
  const bg = averageLightness(backgrounds);
  if (fg !== void 0 && bg !== void 0)
    return bg < fg ? "dark" : "light";
  if (bg !== void 0)
    return bg < 0.5 ? "dark" : "light";
  if (fg !== void 0)
    return fg > 0.5 ? "dark" : "light";
  return void 0;
}
__name(detectAppearance, "detectAppearance");
var Theme = class {
  static {
    __name(this, "Theme");
  }
  name;
  sourcePath;
  sourceInfo;
  mode;
  // Precomputed escape sequences keep fg()/bg() on the render hot path to a lookup and concat.
  fgAnsi = /* @__PURE__ */ new Map();
  bgAnsi = /* @__PURE__ */ new Map();
  // Tokens set to "" have no color of their own; `colors` fills them from the terminal defaults.
  concreteColors = {};
  defaultForegroundTokens = [];
  defaultBackgroundTokens = [];
  // Foreground tokens rendered faint (SGR 2) on top of their color.
  dimTokens;
  ownAppearance;
  resolvedColors;
  constructor(fgColors, bgColors, mode, options = {}) {
    this.name = options.name;
    this.sourcePath = options.sourcePath;
    this.sourceInfo = options.sourceInfo;
    this.mode = mode;
    this.dimTokens = new Set(options.dim);
    const foregrounds = {
      ...fgColors,
      scrollbarTrack: fgColors.scrollbarTrack ?? fgColors.muted,
      scrollbarThumb: fgColors.scrollbarThumb ?? fgColors.text,
      thinkingMax: fgColors.thinkingMax ?? fgColors.thinkingXhigh,
      searchMatchText: fgColors.searchMatchText ?? fgColors.text
    };
    const backgrounds = { ...bgColors, searchMatchBg: bgColors.searchMatchBg ?? bgColors.selectedBg };
    const concreteForegrounds = [];
    const concreteBackgrounds = [];
    const addToken = /* @__PURE__ */ __name((token, value, isBackground) => {
      if (value === "") {
        (isBackground ? this.defaultBackgroundTokens : this.defaultForegroundTokens).push(token);
        return isBackground ? "\x1B[49m" : "\x1B[39m";
      }
      const color = parseColor(value);
      this.concreteColors[token] = color;
      (isBackground ? concreteBackgrounds : concreteForegrounds).push(color);
      return isBackground ? backgroundAnsi(color, mode) : foregroundAnsi(color, mode);
    }, "addToken");
    for (const [token, value] of Object.entries(foregrounds)) {
      this.fgAnsi.set(token, addToken(token, value, false));
    }
    for (const [token, value] of Object.entries(backgrounds)) {
      this.bgAnsi.set(token, addToken(token, value, true));
    }
    this.ownAppearance = options.appearance ?? detectAppearance(concreteForegrounds, concreteBackgrounds);
  }
  /**
   * The background the theme is designed for: declared in the theme JSON, detected from its colors,
   * or, for themes without usable colors, the terminal's appearance.
   */
  get appearance() {
    return this.ownAppearance ?? getTerminalTheme();
  }
  /**
   * Concrete colors for all tokens. Tokens set to "" (terminal default) use the terminal's reported
   * default colors, or a guess based on `appearance` when the terminal did not report them. Faint
   * tokens are approximated by mixing their color toward the background.
   */
  get colors() {
    const terminal = terminalColors;
    if (this.resolvedColors?.terminal !== terminal) {
      const guess = GUESSED_DEFAULT_COLORS[this.appearance];
      const toColor = /* @__PURE__ */ __name((rgb, fallback) => rgb ? rgbColor2(rgb.r, rgb.g, rgb.b) : fallback, "toColor");
      const foreground = toColor(terminal.foreground, guess.foreground);
      const background = toColor(terminal.background, guess.background);
      const colors = { ...this.concreteColors };
      for (const token of this.defaultForegroundTokens)
        colors[token] = foreground;
      for (const token of this.defaultBackgroundTokens)
        colors[token] = background;
      for (const token of this.dimTokens) {
        const color = colors[token];
        if (color)
          colors[token] = mixColors(color, background, 0.4);
      }
      this.resolvedColors = { terminal, colors: Object.freeze(colors) };
    }
    return this.resolvedColors.colors;
  }
  style(text, options) {
    const { fg, bg } = options;
    if (typeof fg === "string" && this.dimTokens.has(fg))
      options = { ...options, dim: true };
    return styleTextWithAnsi(text, fg === void 0 ? void 0 : typeof fg === "string" ? this.tokenAnsi(this.fgAnsi, fg) : foregroundAnsi(fg, this.mode), bg === void 0 ? void 0 : typeof bg === "string" ? this.tokenAnsi(this.bgAnsi, bg) : backgroundAnsi(bg, this.mode), options);
  }
  fg(color, text) {
    const ansi = this.tokenAnsi(this.fgAnsi, color);
    if (this.dimTokens.has(color))
      return `${ansi}\x1B[2m${text}\x1B[22;39m`;
    return `${ansi}${text}\x1B[39m`;
  }
  bg(color, text) {
    const ansi = this.tokenAnsi(this.bgAnsi, color);
    return `${ansi}${text}\x1B[49m`;
  }
  tokenAnsi(ansi, token) {
    const value = ansi.get(token);
    if (value === void 0)
      throw new Error(`Unknown theme color: ${token}`);
    return value;
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
  /** Opening escape sequence for a foreground token. Faint tokens include SGR 2, which `\x1b[22m` closes. */
  getFgAnsi(color) {
    const ansi = this.tokenAnsi(this.fgAnsi, color);
    return this.dimTokens.has(color) ? `${ansi}\x1B[2m` : ansi;
  }
  getBgAnsi(color) {
    return this.tokenAnsi(this.bgAnsi, color);
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
  addTheme({ name: SYSTEM_THEME_NAME, path: void 0 });
  for (const name of Object.keys(getBuiltinThemes())) {
    addTheme({ name, path: path.join(themesDir, `${name}.json`) });
  }
  for (const themeInfo of getCustomThemeInfos()) {
    addTheme(themeInfo);
  }
  for (const [name, theme2] of registeredThemes.entries()) {
    addTheme({ name, path: theme2.sourcePath });
  }
  return result.sort((a, b) => a.name === SYSTEM_THEME_NAME ? -1 : b.name === SYSTEM_THEME_NAME ? 1 : a.name.localeCompare(b.name));
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
var BACKGROUND_TOKENS = /* @__PURE__ */ new Set([
  "selectedBg",
  "searchMatchBg",
  "userMessageBg",
  "customMessageBg",
  "toolPendingBg",
  "toolSuccessBg",
  "toolErrorBg"
]);
function splitThemeColors(colors) {
  const fgColors = {};
  const bgColors = {};
  for (const [key, value] of Object.entries(colors)) {
    if (BACKGROUND_TOKENS.has(key)) {
      bgColors[key] = value;
    } else {
      fgColors[key] = value;
    }
  }
  return { fgColors, bgColors };
}
__name(splitThemeColors, "splitThemeColors");
function createTheme(themeJson, mode, sourcePath) {
  const colorMode = mode ?? getTerminalColorMode();
  const resolvedColors = resolveThemeColors(withThemeColorFallbacks(themeJson.colors), themeJson.vars);
  const { fgColors, bgColors } = splitThemeColors(resolvedColors);
  return new Theme(fgColors, bgColors, colorMode, {
    name: themeJson.name,
    sourcePath,
    appearance: themeJson.appearance
  });
}
__name(createTheme, "createTheme");
function createSystemTheme(mode) {
  const generated = generateSystemThemeColors({
    ...terminalColors,
    saturation: terminalColorsPending ? 0 : 1,
    appearanceHint: getTerminalTheme()
  });
  const { fgColors, bgColors } = splitThemeColors(generated.colors);
  return new Theme(fgColors, bgColors, mode ?? getTerminalColorMode(), {
    name: SYSTEM_THEME_NAME,
    appearance: generated.appearance,
    dim: generated.dim
  });
}
__name(createSystemTheme, "createSystemTheme");
function loadThemeFromPath(themePath, mode) {
  const content = fs.readFileSync(themePath, "utf-8");
  const themeJson = parseThemeJsonContent(themePath, content);
  return createTheme(themeJson, mode, themePath);
}
__name(loadThemeFromPath, "loadThemeFromPath");
function loadTheme(name, mode) {
  if (name === SYSTEM_THEME_NAME)
    return createSystemTheme(mode);
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
function detectColorFgBgTheme(env = process.env) {
  const bg = env.COLORFGBG?.split(";").at(-1)?.trim();
  if (!bg || !/^\d{1,2}$/.test(bg))
    return void 0;
  const index = Number(bg);
  if (index > 15)
    return void 0;
  return index <= 6 || index === 8 ? "dark" : "light";
}
__name(detectColorFgBgTheme, "detectColorFgBgTheme");
function detectTerminalTheme(colors = {}, reportedScheme, env = process.env) {
  const { background, foreground } = colors;
  if (background)
    return terminalAppearance(background, foreground);
  return reportedScheme ?? detectColorFgBgTheme(env) ?? "dark";
}
__name(detectTerminalTheme, "detectTerminalTheme");
function getTerminalTheme() {
  return detectTerminalTheme(terminalColors, terminalColorScheme);
}
__name(getTerminalTheme, "getTerminalTheme");
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
  const name = themeName ?? SYSTEM_THEME_NAME;
  currentThemeName = name;
  try {
    setGlobalTheme(loadTheme(name));
    if (enableWatcher) {
      startThemeWatcher();
    }
  } catch (_error) {
    currentThemeName = SYSTEM_THEME_NAME;
    setGlobalTheme(loadTheme(SYSTEM_THEME_NAME));
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
    currentThemeName = SYSTEM_THEME_NAME;
    setGlobalTheme(loadTheme(SYSTEM_THEME_NAME));
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
  if (!currentThemeName || currentThemeName === "dark" || currentThemeName === "light" || currentThemeName === SYSTEM_THEME_NAME) {
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
function getResolvedThemeColors(themeName) {
  const colors = loadTheme(themeName ?? currentThemeName ?? SYSTEM_THEME_NAME).colors;
  return Object.fromEntries(Object.entries(colors).map(([token, color]) => [token, colorToHex(color)]));
}
__name(getResolvedThemeColors, "getResolvedThemeColors");
function getThemeExportColors(themeName) {
  const name = themeName ?? currentThemeName ?? SYSTEM_THEME_NAME;
  if (name === SYSTEM_THEME_NAME)
    return {};
  try {
    const themeJson = loadThemeJson(name);
    const exportSection = themeJson.export;
    if (!exportSection)
      return {};
    const vars = themeJson.vars ?? {};
    const resolve2 = /* @__PURE__ */ __name((value) => {
      if (value === void 0)
        return void 0;
      const resolved = resolveVarRefs(value, vars);
      if (typeof resolved === "number")
        return colorToHex(indexedColor(resolved));
      if (resolved === "")
        return void 0;
      if (/^okhsl\(/i.test(resolved))
        return colorToHex(parseColor(resolved));
      return resolved;
    }, "resolve");
    return {
      pageBg: resolve2(exportSection.pageBg),
      cardBg: resolve2(exportSection.cardBg),
      infoBg: resolve2(exportSection.infoBg)
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
    strikethrough: /* @__PURE__ */ __name((text) => theme.strikethrough(text), "strikethrough"),
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

// pi-dist/pi-coding-agent/utils/shell.js
import { existsSync as existsSync3 } from "node:fs";
import { delimiter, join as join4 } from "node:path";
import { spawn, spawnSync } from "child_process";
function isLegacyWslBashPath(path2) {
  const normalized = path2.replace(/\//g, "\\").toLowerCase();
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
        if (firstMatch && existsSync3(firstMatch)) {
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
    if (existsSync3(customShellPath)) {
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
    for (const path2 of paths) {
      if (existsSync3(path2)) {
        return getBashShellConfig(path2);
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
  if (existsSync3("/bin/bash")) {
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
  return str2.replace(/[\x00-\x08\x0B\x0C\x0E-\x1F\uFFF9-\uFFFB]/g, "");
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
      const child = spawn(join4(process.env.SystemRoot ?? "C:\\Windows", "System32", "taskkill.exe"), ["/F", "/T", "/PID", String(pid)], {
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

// pi-dist/pi-coding-agent/modes/interactive/components/keybinding-hints.js
import { getKeybindings } from "../../../pi-tui.mjs";
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
import * as os from "node:os";
import { pathToFileURL } from "node:url";
import { getCapabilities, getImageDimensions, hyperlink, imageFallback } from "../../../pi-tui.mjs";
function shortenPath(path2) {
  if (typeof path2 !== "string")
    return "";
  const home = os.homedir();
  if (path2.startsWith(home)) {
    return `~${path2.slice(home.length)}`;
  }
  return path2;
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
function formatToolCallWithArgs(title, args, theme2, expanded) {
  const header = theme2.fg("toolTitle", theme2.bold(title));
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
${theme2.fg("muted", lines.join("\n"))}`;
  }
  const pairs = entries.map(([key, value]) => `${key}=${JSON.stringify(value) ?? String(value)}`).join(" ");
  const preview = pairs.length > COLLAPSED_ARGS_CHARS ? `${pairs.slice(0, COLLAPSED_ARGS_CHARS - 3)}...` : pairs;
  return `${header} ${theme2.fg("muted", preview)}`;
}
__name(formatToolCallWithArgs, "formatToolCallWithArgs");
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
  formatPathRelativeToCwdOrAbsolute,
  markPathIgnoredByCloudSync,
  splitBom,
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
  getQuickJSWasmPath,
  getCodemodeWorkerSpecifier,
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
  SYSTEM_THEME_NAME,
  setThemeJsonValidator,
  setTerminalColors,
  setTerminalColorScheme,
  markTerminalColorsPending,
  Theme,
  getAvailableThemes,
  getAvailableThemesWithPaths,
  loadThemeFromPath,
  getThemeByName,
  parseAutoThemeSetting,
  resolveThemeSetting,
  getTerminalTheme,
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
  stripAnsi,
  getShellConfig,
  getPowerShellConfig,
  getShellEnv,
  sanitizeBinaryOutput,
  trackDetachedChildPid,
  untrackDetachedChildPid,
  killTrackedDetachedChildren,
  killProcessTree,
  formatKeyText,
  keyText,
  keyDisplayText,
  keyHint,
  rawKeyHint,
  truncateToVisualLines,
  VisualLinePreview,
  shortenPath,
  str,
  replaceTabs,
  normalizeDisplayText,
  getTextOutput,
  formatToolCallWithArgs,
  invalidArgText,
  renderToolPath
};
