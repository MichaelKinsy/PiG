import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { getAgentDir, VERSION } from "../config.js";
const MAX_CRASH_RECORDS = 5;
const MAX_AGE = 7 * 24 * 60 * 60 * 1000;
function crashLogPath(agentDir = getAgentDir()) {
    return join(agentDir, "crashes.json");
}
export function readCrashLog(path = crashLogPath()) {
    try {
        const records = JSON.parse(readFileSync(path, "utf8"));
        return Array.isArray(records)
            ? records.filter((record) => typeof record === "object" &&
                record !== null &&
                typeof record.timestamp === "string" &&
                typeof record.message === "string")
            : [];
    }
    catch {
        return [];
    }
}
function writeCrashLog(records, path) {
    mkdirSync(dirname(path), { recursive: true });
    writeFileSync(path, `${JSON.stringify(records, null, 2)}\n`);
}
function normalizeStackPath(value) {
    return value.replace(/\\/g, "/").replace(/\/+$/u, "");
}
function stackContainsPath(stack, targetPath, includeDescendants) {
    const target = normalizeStackPath(targetPath);
    if (!target || target.startsWith("<"))
        return false;
    const caseInsensitive = /^[a-z]:\//iu.test(target);
    const haystack = caseInsensitive ? stack.toLowerCase() : stack;
    const needle = caseInsensitive ? target.toLowerCase() : target;
    if (includeDescendants)
        return haystack.includes(`${needle}/`);
    let index = haystack.indexOf(needle);
    while (index !== -1) {
        const next = haystack[index + needle.length];
        if (next === undefined || next === ":" || next === ")" || /\s/u.test(next))
            return true;
        index = haystack.indexOf(needle, index + needle.length);
    }
    return false;
}
/** Find loaded extensions with source files in a stack trace. */
export function findExtensionStackMatches(stack, extensions) {
    if (!stack)
        return [];
    const normalizedStack = stack
        .split("\n")
        .slice(1)
        .filter((line) => /^\s+at\s/u.test(line))
        .map((line) => {
        try {
            return decodeURI(line);
        }
        catch {
            return line;
        }
    })
        .join("\n")
        .replace(/\\/g, "/");
    const matches = [];
    const seen = new Set();
    for (const extension of extensions) {
        const resolvedPath = normalizeStackPath(extension.resolvedPath);
        const singleFilePackage = extension.sourceInfo.origin === "package" &&
            !/^(?:npm:|git:|https?:\/\/|ssh:\/\/)/u.test(extension.sourceInfo.source) &&
            /\.[cm]?[jt]s$/u.test(extension.sourceInfo.source);
        const packageRoot = extension.sourceInfo.origin === "package" && !singleFilePackage && extension.sourceInfo.baseDir
            ? extension.sourceInfo.baseDir
            : undefined;
        const slashIndex = resolvedPath.lastIndexOf("/");
        const directoryEntry = /\/index\.[cm]?[jt]s$/u.test(resolvedPath);
        const matched = packageRoot
            ? stackContainsPath(normalizedStack, packageRoot, true)
            : directoryEntry && slashIndex !== -1
                ? stackContainsPath(normalizedStack, resolvedPath.slice(0, slashIndex), true)
                : stackContainsPath(normalizedStack, resolvedPath, false);
        if (!matched)
            continue;
        const label = extension.sourceInfo.origin === "package" && extension.sourceInfo.source
            ? extension.sourceInfo.source
            : extension.path;
        if (!seen.has(label)) {
            seen.add(label);
            matches.push(label);
        }
    }
    return matches;
}
/** Best-effort persistence for callers that are already crashing. */
export function recordCrash(crash, path = crashLogPath()) {
    try {
        const { error } = crash;
        const record = {
            timestamp: new Date().toISOString(),
            version: VERSION,
            kind: crash.kind,
            message: error instanceof Error ? error.message || error.name : String(error),
            stack: error instanceof Error && error.stack ? error.stack : null,
            sessionFile: crash.sessionFile ?? null,
            cwd: crash.cwd,
        };
        writeCrashLog([...readCrashLog(path), record].slice(-MAX_CRASH_RECORDS), path);
        return record;
    }
    catch {
        return undefined;
    }
}
/** Return the newest recent crash, marking pending records as announced. */
export function takeUnnotifiedCrash(path = crashLogPath(), now = Date.now()) {
    const records = readCrashLog(path);
    const crash = [...records]
        .reverse()
        .find((record) => !record.notified && now - Date.parse(record.timestamp) <= MAX_AGE);
    if (!crash)
        return undefined;
    try {
        writeCrashLog(records.map((record) => (record.notified ? record : { ...record, notified: true })), path);
    }
    catch {
        // Showing the notice again is harmless.
    }
    return crash;
}
export function clearCrashLog(path = crashLogPath()) {
    try {
        rmSync(path, { force: true });
    }
    catch {
        // The records can be attached again if cleanup fails.
    }
}
//# sourceMappingURL=crash-log.js.map