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
function canonicalizePath(path) {
  try {
    return realpathSync(path);
  } catch {
    return path;
  }
}
__name(canonicalizePath, "canonicalizePath");
function getFileRevision(path) {
  try {
    const stats = statSync(path, { bigint: true });
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
function markPathIgnoredByCloudSync(path) {
  const attrs = process.platform === "darwin" ? ["com.dropbox.ignored", "com.apple.fileprovider.ignore#P"] : process.platform === "linux" ? ["user.com.dropbox.ignored"] : [];
  for (const attr of attrs) {
    if (process.platform === "darwin") {
      spawnProcessSync("xattr", ["-w", attr, "1", path], { encoding: "utf-8", stdio: "ignore" });
    } else {
      spawnProcessSync("setfattr", ["-n", attr, "-v", "1", path], { encoding: "utf-8", stdio: "ignore" });
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
  const path = process.platform === "win32" || packageDir.includes("\\") ? win32 : { basename, dirname };
  const parent = path.dirname(packageDir);
  let root;
  if (path.basename(parent).startsWith("@") && path.basename(path.dirname(parent)) === "node_modules") {
    root = path.dirname(parent);
  } else if (path.basename(parent) === "node_modules") {
    root = parent;
  }
  if (!root)
    return void 0;
  const rootParent = path.dirname(root);
  if (path.basename(rootParent) === "lib")
    return { root, prefix: path.dirname(rootParent) };
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
function normalizeExistingPathForComparison(path, resolveSymlinks) {
  const resolvedPath = resolve(path);
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
function getPathComparisonCandidates(path) {
  return Array.from(new Set([normalizeExistingPathForComparison(path, false), normalizeExistingPathForComparison(path, true)].filter((candidate) => !!candidate)));
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
var quickJSWasmPath;
function getQuickJSWasmPath() {
  quickJSWasmPath ??= fileURLToPath2(new URL("../../quickjs-wasi/quickjs.wasm", new URL("../config.js", import.meta.url).href));
  return quickJSWasmPath;
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
  const specifier = resolveCodemodeWorkerSpecifier(runtime, new URL("../config.js", import.meta.url).href);
  if (runtime !== "bundled-node" || !(specifier instanceof URL))
    return specifier;
  return specifier;
}
__name(getCodemodeWorkerSpecifier, "getCodemodeWorkerSpecifier");
function detectInstallChange(packageJsonPath = startupPackageJsonPath) {
  if (isBunBinary || !packageJsonPath)
    return void 0;
  let installed;
  try {
    installed = JSON.parse(stripBom(readFileSync(packageJsonPath, "utf-8")));
  } catch (error) {
    return error.code === "ENOENT" ? { kind: "removed" } : void 0;
  }
  return installed.version && installed.version !== VERSION ? { kind: "updated", version: installed.version } : void 0;
}
__name(detectInstallChange, "detectInstallChange");
var pkg = {};
var startupPackageJsonPath;
try {
  const packageJsonPath = getPackageJsonPath();
  pkg = JSON.parse(stripBom(readFileSync(packageJsonPath, "utf-8")));
  startupPackageJsonPath = packageJsonPath;
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
function expandTildePath(path) {
  return normalizePath(path);
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
  getThemesDir,
  getExportTemplateDir,
  getReadmePath,
  getDocsPath,
  getExamplesPath,
  getChangelogPath,
  getBundledInteractiveAssetPath,
  getQuickJSWasmPath,
  getCodemodeWorkerSpecifier,
  detectInstallChange,
  PACKAGE_NAME,
  APP_NAME,
  APP_TITLE,
  VERSION,
  ENV_SESSION_DIR,
  expandTildePath,
  getShareViewerUrl,
  getAgentDir,
  getCustomThemesDir,
  getAuthPath,
  getSettingsPath,
  getBinDir,
  getSessionsDir,
  getDebugLogPath,
  CONFIG_DIR_NAME,
  ENV_AGENT_DIR
};
