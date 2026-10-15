import {
  getBinDir
} from "./chunk-YXY3JV6E.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-coding-agent/utils/shell.js
import { existsSync } from "node:fs";
import { delimiter, join } from "node:path";
import { spawn, spawnSync } from "child_process";
function isLegacyWslBashPath(path) {
  const normalized = path.replace(/\//g, "\\").toLowerCase();
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
        if (firstMatch && existsSync(firstMatch)) {
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
    if (existsSync(customShellPath)) {
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
    for (const path of paths) {
      if (existsSync(path)) {
        return getBashShellConfig(path);
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
  if (existsSync("/bin/bash")) {
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
function sanitizeBinaryOutput(str) {
  return str.replace(/[\x00-\x08\x0B\x0C\x0E-\x1F\uFFF9-\uFFFB]/g, "");
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
      const child = spawn(join(process.env.SystemRoot ?? "C:\\Windows", "System32", "taskkill.exe"), ["/F", "/T", "/PID", String(pid)], {
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

export {
  getShellConfig,
  getPowerShellConfig,
  getShellEnv,
  sanitizeBinaryOutput,
  trackDetachedChildPid,
  untrackDetachedChildPid,
  killTrackedDetachedChildren,
  killProcessTree
};
