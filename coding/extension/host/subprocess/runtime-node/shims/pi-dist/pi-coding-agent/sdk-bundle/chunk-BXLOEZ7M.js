import {
  getBinDir
} from "./chunk-JGRZB36T.js";
import {
  __name
} from "./chunk-FIOAHQVQ.js";

// pi-dist/pi-coding-agent/utils/shell.js
import { accessSync, constants } from "node:fs";
import { delimiter, join, win32 } from "node:path";
import { spawn, spawnSync } from "child_process";
function isLegacyWslBashPath(path) {
  const normalized = win32.normalize(path).toLowerCase();
  const systemRoot = process.env.SystemRoot;
  if (systemRoot) {
    if (!/^[a-z]:[\\/]/i.test(systemRoot))
      return false;
    return ["System32", "Sysnative"].some((directory) => normalized === win32.join(systemRoot, directory, "bash.exe").toLowerCase());
  }
  return /^[a-z]:\\windows\\(?:system32|sysnative)\\bash\.exe$/.test(normalized);
}
__name(isLegacyWslBashPath, "isLegacyWslBashPath");
function getBashShellConfig(shell) {
  return isLegacyWslBashPath(shell) ? { shell, args: ["-s"], commandTransport: "stdin" } : { shell, args: ["-c"] };
}
__name(getBashShellConfig, "getBashShellConfig");
function shellPathExists(path) {
  try {
    accessSync(path, constants.F_OK);
    return true;
  } catch {
    return false;
  }
}
__name(shellPathExists, "shellPathExists");
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
        if (firstMatch && shellPathExists(firstMatch)) {
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
    if (shellPathExists(customShellPath)) {
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
      if (shellPathExists(path)) {
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
  if (shellPathExists("/bin/bash")) {
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
  let shell = findExecutableOnPath("pwsh.exe");
  const programFiles = process.env.ProgramFiles;
  if (!shell && programFiles) {
    const pwsh7 = win32.join(programFiles, "PowerShell", "7", "pwsh.exe");
    if (shellPathExists(pwsh7))
      shell = pwsh7;
  }
  shell ??= findExecutableOnPath("powershell.exe");
  const systemRoot = process.env.SystemRoot;
  if (!shell && systemRoot) {
    const windowsPowerShell = win32.join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe");
    if (shellPathExists(windowsPowerShell))
      shell = windowsPowerShell;
  }
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
      const child = spawn(join(process.env.SystemRoot || "C:\\Windows", "System32", "taskkill.exe"), ["/F", "/T", "/PID", String(pid)], {
        stdio: "ignore",
        detached: true,
        windowsHide: true
      });
      child.once("error", () => {
      });
    } catch {
    }
  } else {
    if (process.platform === "darwin") {
      try {
        process.kill(-pid, "SIGSTOP");
      } catch {
      }
    }
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
function truncateStringToBytesFromEnd(str, maxBytes) {
  const buf = Buffer.from(str, "utf-8");
  if (buf.length <= maxBytes) {
    return str;
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
  return { text: `${sliceHead(line, maxChars)}... [truncated]`, wasTruncated: true };
}
__name(truncateLine, "truncateLine");
function sliceHead(text, maxUnits) {
  const end = Math.max(0, maxUnits);
  return isHighSurrogate(text.charCodeAt(end - 1)) && isLowSurrogate(text.charCodeAt(end)) ? text.slice(0, end - 1) : text.slice(0, end);
}
__name(sliceHead, "sliceHead");
function sliceTail(text, maxUnits) {
  if (maxUnits <= 0)
    return "";
  const start = Math.max(0, text.length - maxUnits);
  return isLowSurrogate(text.charCodeAt(start)) && isHighSurrogate(text.charCodeAt(start - 1)) ? text.slice(start + 1) : text.slice(start);
}
__name(sliceTail, "sliceTail");
function isHighSurrogate(code) {
  return code >= 55296 && code <= 56319;
}
__name(isHighSurrogate, "isHighSurrogate");
function isLowSurrogate(code) {
  return code >= 56320 && code <= 57343;
}
__name(isLowSurrogate, "isLowSurrogate");

export {
  getShellConfig,
  getPowerShellConfig,
  getShellEnv,
  sanitizeBinaryOutput,
  trackDetachedChildPid,
  untrackDetachedChildPid,
  killTrackedDetachedChildren,
  killProcessTree,
  DEFAULT_MAX_LINES,
  DEFAULT_MAX_BYTES,
  GREP_MAX_LINE_LENGTH,
  formatSize,
  truncateHead,
  truncateTail,
  truncateLine,
  sliceHead,
  sliceTail
};
