import { randomBytes } from "node:crypto";
import { constants, createWriteStream } from "node:fs";
import { access, mkdir, readFile, readdir, realpath, stat, writeFile } from "node:fs/promises";
import { homedir, tmpdir } from "node:os";
import { basename, dirname, isAbsolute, join, relative, resolve } from "node:path";
import { spawn } from "node:child_process";
import { Type } from "./typebox.mjs";

export const DEFAULT_MAX_LINES = 2000;
export const DEFAULT_MAX_BYTES = 50 * 1024;
export const GREP_MAX_LINE_LENGTH = 500;

function abortError() {
  return new Error("Operation aborted");
}

function throwIfAborted(signal) {
  if (signal?.aborted) throw abortError();
}

function splitLinesForCounting(content) {
  if (content.length === 0) return [];
  const lines = content.split("\n");
  if (content.endsWith("\n")) lines.pop();
  return lines;
}

export function formatSize(bytes) {
  if (bytes < 1024) return `${bytes}B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)}KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)}MB`;
}

function truncationResult(content, truncated, truncatedBy, totalLines, totalBytes, outputLines, outputBytes, options = {}) {
  return {
    content,
    truncated,
    truncatedBy,
    totalLines,
    totalBytes,
    outputLines,
    outputBytes,
    lastLinePartial: options.lastLinePartial ?? false,
    firstLineExceedsLimit: options.firstLineExceedsLimit ?? false,
    maxLines: options.maxLines ?? DEFAULT_MAX_LINES,
    maxBytes: options.maxBytes ?? DEFAULT_MAX_BYTES,
  };
}

export function truncateHead(content, options = {}) {
  const maxLines = options.maxLines ?? DEFAULT_MAX_LINES;
  const maxBytes = options.maxBytes ?? DEFAULT_MAX_BYTES;
  const totalBytes = Buffer.byteLength(content, "utf-8");
  const lines = splitLinesForCounting(content);
  const totalLines = lines.length;
  if (totalLines <= maxLines && totalBytes <= maxBytes) {
    return truncationResult(content, false, null, totalLines, totalBytes, totalLines, totalBytes, { maxLines, maxBytes });
  }
  if (Buffer.byteLength(lines[0] ?? "", "utf-8") > maxBytes) {
    return truncationResult("", true, "bytes", totalLines, totalBytes, 0, 0, { maxLines, maxBytes, firstLineExceedsLimit: true });
  }
  const output = [];
  let outputBytes = 0;
  let truncatedBy = "lines";
  for (let index = 0; index < lines.length && index < maxLines; index++) {
    const lineBytes = Buffer.byteLength(lines[index], "utf-8") + (index > 0 ? 1 : 0);
    if (outputBytes + lineBytes > maxBytes) {
      truncatedBy = "bytes";
      break;
    }
    output.push(lines[index]);
    outputBytes += lineBytes;
  }
  const value = output.join("\n");
  return truncationResult(value, true, truncatedBy, totalLines, totalBytes, output.length, Buffer.byteLength(value), { maxLines, maxBytes });
}

function validUTF8Tail(value, maxBytes) {
  const buffer = Buffer.from(value, "utf-8");
  if (buffer.length <= maxBytes) return value;
  let start = buffer.length - maxBytes;
  while (start < buffer.length && (buffer[start] & 0xc0) === 0x80) start++;
  return buffer.subarray(start).toString("utf-8");
}

export function truncateTail(content, options = {}) {
  const maxLines = options.maxLines ?? DEFAULT_MAX_LINES;
  const maxBytes = options.maxBytes ?? DEFAULT_MAX_BYTES;
  const totalBytes = Buffer.byteLength(content, "utf-8");
  const lines = splitLinesForCounting(content);
  const totalLines = lines.length;
  if (totalLines <= maxLines && totalBytes <= maxBytes) {
    return truncationResult(content, false, null, totalLines, totalBytes, totalLines, totalBytes, { maxLines, maxBytes });
  }
  const output = [];
  let outputBytes = 0;
  let truncatedBy = "lines";
  let lastLinePartial = false;
  for (let index = lines.length - 1; index >= 0 && output.length < maxLines; index--) {
    const lineBytes = Buffer.byteLength(lines[index], "utf-8") + (output.length > 0 ? 1 : 0);
    if (outputBytes + lineBytes > maxBytes) {
      truncatedBy = "bytes";
      if (output.length === 0) {
        const tail = validUTF8Tail(lines[index], maxBytes);
        output.unshift(tail);
        outputBytes = Buffer.byteLength(tail);
        lastLinePartial = true;
      }
      break;
    }
    output.unshift(lines[index]);
    outputBytes += lineBytes;
  }
  const value = output.join("\n");
  return truncationResult(value, true, truncatedBy, totalLines, totalBytes, output.length, Buffer.byteLength(value), { maxLines, maxBytes, lastLinePartial });
}

export function truncateLine(line, maxChars = GREP_MAX_LINE_LENGTH) {
  if (line.length <= maxChars) return { text: line, wasTruncated: false };
  return { text: `${line.slice(0, maxChars)}... [truncated]`, wasTruncated: true };
}

function expandPath(filePath) {
  let value = String(filePath ?? "").replace(/^@/, "").replace(/[\u00a0\u2002-\u200a\u202f\u205f\u3000]/g, " ");
  if (value === "~") return homedir();
  if (value.startsWith("~/")) value = join(homedir(), value.slice(2));
  return value;
}

function resolveToCwd(filePath, cwd) {
  const expanded = expandPath(filePath);
  return isAbsolute(expanded) ? resolve(expanded) : resolve(cwd, expanded);
}

async function exists(filePath) {
  try {
    await access(filePath, constants.F_OK);
    return true;
  } catch {
    return false;
  }
}

async function resolveReadPath(filePath, cwd) {
  const resolved = resolveToCwd(filePath, cwd);
  const variants = [
    resolved,
    resolved.replace(/ (AM|PM)\./gi, "\u202f$1."),
    resolved.normalize("NFD"),
    resolved.replaceAll("'", "’"),
    resolved.normalize("NFD").replaceAll("'", "’"),
  ];
  for (const variant of variants) {
    if (await exists(variant)) return variant;
  }
  return resolved;
}

const mimeByExtension = new Map([
  [".jpg", "image/jpeg"], [".jpeg", "image/jpeg"], [".png", "image/png"],
  [".gif", "image/gif"], [".webp", "image/webp"], [".bmp", "image/bmp"],
]);

async function detectImageMimeType(filePath) {
  const lower = filePath.toLowerCase();
  for (const [suffix, mime] of mimeByExtension) {
    if (lower.endsWith(suffix)) return mime;
  }
  return null;
}

const defaultReadOperations = {
  readFile,
  access: (path) => access(path, constants.R_OK),
  detectImageMimeType,
};

const readSchema = Type.Object({
  path: Type.String({ description: "Path to the file to read (relative or absolute)" }),
  offset: Type.Optional(Type.Number({ description: "Line number to start reading from (1-indexed)" })),
  limit: Type.Optional(Type.Number({ description: "Maximum number of lines to read" })),
});

export function createReadTool(cwd, options = {}) {
  const operations = options.operations ?? defaultReadOperations;
  return {
    name: "read",
    label: "read",
    description: `Read the contents of a file. Supports text files and images (jpg, png, gif, webp, bmp). Images are sent as attachments. For text files, output is truncated to ${DEFAULT_MAX_LINES} lines or ${DEFAULT_MAX_BYTES / 1024}KB (whichever is hit first). Use offset/limit for large files. When you need the full file, continue with offset until complete.`,
    promptSnippet: "Read file contents",
    promptGuidelines: ["Use read to examine files instead of cat or sed."],
    parameters: readSchema,
    async execute(_id, { path, offset, limit }, signal, _onUpdate, ctx) {
      throwIfAborted(signal);
      const absolutePath = await resolveReadPath(path, cwd);
      await operations.access(absolutePath);
      throwIfAborted(signal);
      const mimeType = operations.detectImageMimeType ? await operations.detectImageMimeType(absolutePath) : null;
      const buffer = await operations.readFile(absolutePath);
      throwIfAborted(signal);
      if (mimeType) {
        let note = `Read image file [${mimeType}]`;
        if (ctx?.model && !ctx.model.input?.includes?.("image")) note += "\n[Current model does not support images. The image will be omitted from this request.]";
        return { content: [{ type: "text", text: note }, { type: "image", data: Buffer.from(buffer).toString("base64"), mimeType }], details: undefined };
      }
      const allLines = Buffer.from(buffer).toString("utf-8").split("\n");
      const startLine = offset ? Math.max(0, offset - 1) : 0;
      if (startLine >= allLines.length) throw new Error(`Offset ${offset} is beyond end of file (${allLines.length} lines total)`);
      const endLine = limit === undefined ? allLines.length : Math.min(startLine + limit, allLines.length);
      const selected = allLines.slice(startLine, endLine).join("\n");
      const truncation = truncateHead(selected);
      let output = truncation.content;
      let details;
      if (truncation.firstLineExceedsLimit) {
        const lineNumber = startLine + 1;
        output = `[Line ${lineNumber} is ${formatSize(Buffer.byteLength(allLines[startLine], "utf-8"))}, exceeds ${formatSize(DEFAULT_MAX_BYTES)} limit. Use bash: sed -n '${lineNumber}p' ${path} | head -c ${DEFAULT_MAX_BYTES}]`;
        details = { truncation };
      } else if (truncation.truncated) {
        const first = startLine + 1;
        const last = first + truncation.outputLines - 1;
        const size = truncation.truncatedBy === "bytes" ? ` (${formatSize(DEFAULT_MAX_BYTES)} limit)` : "";
        output += `\n\n[Showing lines ${first}-${last} of ${allLines.length}${size}. Use offset=${last + 1} to continue.]`;
        details = { truncation };
      } else if (limit !== undefined && endLine < allLines.length) {
        output += `\n\n[${allLines.length - endLine} more lines in file. Use offset=${endLine + 1} to continue.]`;
      }
      return { content: [{ type: "text", text: output }], details };
    },
  };
}

const fileMutationQueues = new Map();
let mutationRegistration = Promise.resolve();

async function mutationQueueKey(filePath) {
  const resolvedPath = resolve(filePath);
  try {
    return await realpath(resolvedPath);
  } catch (error) {
    if (error?.code === "ENOENT" || error?.code === "ENOTDIR") return resolvedPath;
    throw error;
  }
}

export async function withFileMutationQueue(filePath, fn) {
  const registration = mutationRegistration.then(async () => {
    const key = await mutationQueueKey(filePath);
    const currentQueue = fileMutationQueues.get(key) ?? Promise.resolve();
    let releaseNext;
    const nextQueue = new Promise((releaseNextQueue) => { releaseNext = releaseNextQueue; });
    const chainedQueue = currentQueue.then(() => nextQueue);
    fileMutationQueues.set(key, chainedQueue);
    return { key, currentQueue, chainedQueue, releaseNext };
  });
  mutationRegistration = registration.then(() => undefined, () => undefined);
  const { key, currentQueue, chainedQueue, releaseNext } = await registration;
  await currentQueue;
  try {
    return await fn();
  } finally {
    releaseNext();
    if (fileMutationQueues.get(key) === chainedQueue) fileMutationQueues.delete(key);
  }
}

const writeSchema = Type.Object({
  path: Type.String({ description: "Path to the file to write (relative or absolute)" }),
  content: Type.String({ description: "Content to write to the file" }),
});

const defaultWriteOperations = {
  writeFile: (path, content) => writeFile(path, content, "utf-8"),
  mkdir: (path) => mkdir(path, { recursive: true }).then(() => undefined),
};

export function createWriteTool(cwd, options = {}) {
  const operations = options.operations ?? defaultWriteOperations;
  return {
    name: "write",
    label: "write",
    description: "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories.",
    promptSnippet: "Create or overwrite files",
    promptGuidelines: ["Use write only for new files or complete rewrites."],
    parameters: writeSchema,
    async execute(_id, { path, content }, signal) {
      const absolutePath = resolveToCwd(path, cwd);
      return withFileMutationQueue(absolutePath, async () => {
        throwIfAborted(signal);
        await operations.mkdir(dirname(absolutePath));
        throwIfAborted(signal);
        await operations.writeFile(absolutePath, content);
        throwIfAborted(signal);
        return { content: [{ type: "text", text: `Successfully wrote to ${path}` }], details: undefined };
      });
    },
  };
}

function normalizeToLF(value) {
  return value.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
}

function normalizeForFuzzyMatch(value) {
  return value.normalize("NFKC").split("\n").map((line) => line.trimEnd()).join("\n")
    .replace(/[\u2018\u2019\u201a\u201b]/g, "'")
    .replace(/[\u201c\u201d\u201e\u201f]/g, '"')
    .replace(/[\u2010-\u2015\u2212]/g, "-")
    .replace(/[\u00a0\u2002-\u200a\u202f\u205f\u3000]/g, " ");
}

function countOccurrences(content, target) {
  const normalizedContent = normalizeForFuzzyMatch(content);
  const normalizedTarget = normalizeForFuzzyMatch(target);
  return normalizedTarget ? normalizedContent.split(normalizedTarget).length - 1 : 0;
}

function splitLinesWithEndings(content) {
  return content.match(/[^\n]*\n|[^\n]+/g) ?? [];
}

function lineSpans(content) {
  let offset = 0;
  return splitLinesWithEndings(content).map((line) => {
    const span = { start: offset, end: offset + line.length };
    offset = span.end;
    return span;
  });
}

function replacementLineRange(lines, replacement) {
  const replacementEnd = replacement.at + replacement.length;
  const startLine = lines.findIndex((line) => replacement.at >= line.start && replacement.at < line.end);
  if (startLine === -1) throw new Error("Replacement range is outside the base content.");
  let endLine = startLine;
  while (endLine < lines.length && lines[endLine].end < replacementEnd) endLine++;
  if (endLine >= lines.length) throw new Error("Replacement range is outside the base content.");
  return { startLine, endLine: endLine + 1 };
}

function applyReplacements(content, replacements, offset = 0) {
  let output = content;
  for (let index = replacements.length - 1; index >= 0; index--) {
    const replacement = replacements[index];
    const at = replacement.at - offset;
    output = output.slice(0, at) + replacement.newText + output.slice(at + replacement.length);
  }
  return output;
}

function applyReplacementsPreservingUnchangedLines(original, base, replacements) {
  const originalLines = splitLinesWithEndings(original);
  const baseLines = lineSpans(base);
  if (originalLines.length !== baseLines.length) throw new Error("Cannot preserve unchanged lines because the base content has a different line count.");
  const groups = [];
  for (const replacement of [...replacements].sort((a, b) => a.at - b.at)) {
    const range = replacementLineRange(baseLines, replacement);
    const current = groups[groups.length - 1];
    if (current && range.startLine < current.endLine) {
      current.endLine = Math.max(current.endLine, range.endLine);
      current.replacements.push(replacement);
    } else {
      groups.push({ ...range, replacements: [replacement] });
    }
  }
  let originalLineIndex = 0;
  let output = "";
  for (const group of groups) {
    output += originalLines.slice(originalLineIndex, group.startLine).join("");
    const start = baseLines[group.startLine].start;
    const end = baseLines[group.endLine - 1].end;
    output += applyReplacements(base.slice(start, end), group.replacements, start);
    originalLineIndex = group.endLine;
  }
  return output + originalLines.slice(originalLineIndex).join("");
}

function findEdit(content, oldText) {
  const exactIndex = content.indexOf(oldText);
  if (exactIndex !== -1) return { found: true, at: exactIndex, length: oldText.length, fuzzy: false };
  const fuzzyContent = normalizeForFuzzyMatch(content);
  const fuzzyText = normalizeForFuzzyMatch(oldText);
  const fuzzyIndex = fuzzyContent.indexOf(fuzzyText);
  return fuzzyIndex === -1
    ? { found: false, at: -1, length: 0, fuzzy: false }
    : { found: true, at: fuzzyIndex, length: fuzzyText.length, fuzzy: true };
}

function applyEdits(content, edits, path) {
  const normalizedEdits = edits.map(({ oldText, newText }) => ({ oldText: normalizeToLF(oldText), newText: normalizeToLF(newText) }));
  for (let index = 0; index < normalizedEdits.length; index++) {
    if (!normalizedEdits[index].oldText) throw new Error(normalizedEdits.length === 1 ? `oldText must not be empty in ${path}.` : `edits[${index}].oldText must not be empty in ${path}.`);
  }
  const useFuzzyBase = normalizedEdits.map((edit) => findEdit(content, edit.oldText)).some((match) => match.fuzzy);
  const base = useFuzzyBase ? normalizeForFuzzyMatch(content) : content;
  const matches = normalizedEdits.map((edit, index) => {
    const match = findEdit(base, edit.oldText);
    if (!match.found) throw new Error(normalizedEdits.length === 1 ? `Could not find the exact text in ${path}. The old text must match exactly including all whitespace and newlines.` : `Could not find edits[${index}] in ${path}. The oldText must match exactly including all whitespace and newlines.`);
    const occurrences = countOccurrences(base, edit.oldText);
    if (occurrences > 1) throw new Error(normalizedEdits.length === 1 ? `Found ${occurrences} occurrences of the text in ${path}. The text must be unique. Please provide more context to make it unique.` : `Found ${occurrences} occurrences of edits[${index}] in ${path}. Each oldText must be unique. Please provide more context to make it unique.`);
    return { index, at: match.at, length: match.length, newText: edit.newText };
  }).sort((a, b) => a.at - b.at);
  for (let index = 1; index < matches.length; index++) {
    if (matches[index - 1].at + matches[index - 1].length > matches[index].at) throw new Error(`edits[${matches[index - 1].index}] and edits[${matches[index].index}] overlap in ${path}. Merge them into one edit or target disjoint regions.`);
  }
  const output = useFuzzyBase
    ? applyReplacementsPreservingUnchangedLines(content, base, matches)
    : applyReplacements(base, matches);
  if (output === content) throw new Error(normalizedEdits.length === 1 ? `No changes made to ${path}. The replacement produced identical content. This might indicate an issue with special characters or the text not existing as expected.` : `No changes made to ${path}. The replacements produced identical content.`);
  return output;
}

function diffLines(before, after) {
  const oldLines = before.split("\n");
  const newLines = after.split("\n");
  if (before.endsWith("\n")) oldLines.pop();
  if (after.endsWith("\n")) newLines.pop();
  const lengths = Array.from({ length: oldLines.length + 1 }, () => new Uint32Array(newLines.length + 1));
  for (let oldIndex = oldLines.length - 1; oldIndex >= 0; oldIndex--) {
    for (let newIndex = newLines.length - 1; newIndex >= 0; newIndex--) {
      lengths[oldIndex][newIndex] = oldLines[oldIndex] === newLines[newIndex]
        ? lengths[oldIndex + 1][newIndex + 1] + 1
        : Math.max(lengths[oldIndex + 1][newIndex], lengths[oldIndex][newIndex + 1]);
    }
  }
  const operations = [];
  let oldIndex = 0;
  let newIndex = 0;
  while (oldIndex < oldLines.length || newIndex < newLines.length) {
    if (oldIndex < oldLines.length && newIndex < newLines.length && oldLines[oldIndex] === newLines[newIndex]) {
      operations.push({ type: "equal", line: oldLines[oldIndex], oldLine: oldIndex + 1, newLine: newIndex + 1 });
      oldIndex++;
      newIndex++;
      continue;
    }
    if (oldIndex < oldLines.length && (newIndex === newLines.length || lengths[oldIndex + 1][newIndex] >= lengths[oldIndex][newIndex + 1])) {
      operations.push({ type: "remove", line: oldLines[oldIndex], oldLine: oldIndex + 1, newLine: newIndex + 1 });
      oldIndex++;
      continue;
    }
    operations.push({ type: "add", line: newLines[newIndex], oldLine: oldIndex + 1, newLine: newIndex + 1 });
    newIndex++;
  }
  return operations;
}

function changedGroups(operations, contextLines) {
  const changed = operations.map((operation, index) => operation.type === "equal" ? -1 : index).filter((index) => index >= 0);
  if (changed.length === 0) return [];
  const groups = [];
  let start = changed[0];
  let end = changed[0];
  for (const index of changed.slice(1)) {
    if (index - end <= contextLines * 2 + 1) {
      end = index;
      continue;
    }
    groups.push([start, end]);
    start = index;
    end = index;
  }
  groups.push([start, end]);
  return groups;
}

function displayDiff(before, after, contextLines = 4) {
  const operations = diffLines(before, after);
  const groups = changedGroups(operations, contextLines);
  const output = [];
  let firstChangedLine;
  for (let groupIndex = 0; groupIndex < groups.length; groupIndex++) {
    const [firstChange, lastChange] = groups[groupIndex];
    const first = Math.max(0, firstChange - contextLines);
    const last = Math.min(operations.length - 1, lastChange + contextLines);
    if (groupIndex > 0) output.push(" ...");
    for (const operation of operations.slice(first, last + 1)) {
      if (operation.type === "add") {
        firstChangedLine ??= operation.newLine;
        output.push(`+${operation.newLine} ${operation.line}`);
      } else if (operation.type === "remove") {
        firstChangedLine ??= operation.newLine;
        output.push(`-${operation.oldLine} ${operation.line}`);
      } else {
        output.push(` ${operation.oldLine} ${operation.line}`);
      }
    }
  }
  const width = String(Math.max(before.split("\n").length, after.split("\n").length)).length;
  return {
    diff: output.map((line) => line === " ..." ? ` ${"".padStart(width)} ...` : `${line[0]}${line.slice(1).replace(/^(\d+)/, (number) => number.padStart(width))}`).join("\n"),
    firstChangedLine,
  };
}

function formatRange(start, count) {
  if (count === 1) return String(start);
  if (count === 0) return `${Math.max(0, start - 1)},0`;
  return `${start},${count}`;
}

function unifiedPatch(path, before, after, contextLines = 4) {
  const operations = diffLines(before, after);
  const groups = changedGroups(operations, contextLines);
  if (groups.length === 0) return "";
  const lines = [`--- ${path}`, `+++ ${path}`];
  const oldTotal = before.endsWith("\n") ? before.split("\n").length - 1 : before.split("\n").length;
  const newTotal = after.endsWith("\n") ? after.split("\n").length - 1 : after.split("\n").length;
  const oldMissingNewline = before.length > 0 && !before.endsWith("\n");
  const newMissingNewline = after.length > 0 && !after.endsWith("\n");
  for (const [firstChange, lastChange] of groups) {
    const first = Math.max(0, firstChange - contextLines);
    const last = Math.min(operations.length - 1, lastChange + contextLines);
    const hunk = operations.slice(first, last + 1);
    const oldStart = hunk[0]?.oldLine ?? 1;
    const newStart = hunk[0]?.newLine ?? 1;
    const oldCount = hunk.filter((operation) => operation.type !== "add").length;
    const newCount = hunk.filter((operation) => operation.type !== "remove").length;
    lines.push(`@@ -${formatRange(oldStart, oldCount)} +${formatRange(newStart, newCount)} @@`);
    for (const operation of hunk) {
      const marker = operation.type === "add" ? "+" : operation.type === "remove" ? "-" : " ";
      lines.push(marker + operation.line);
      const marksOldEnd = oldMissingNewline && operation.type !== "add" && operation.oldLine === oldTotal;
      const marksNewEnd = newMissingNewline && operation.type !== "remove" && operation.newLine === newTotal;
      if (marksOldEnd || marksNewEnd) lines.push("\\ No newline at end of file");
    }
  }
  return lines.join("\n") + "\n";
}

const editSchema = Type.Object({
  path: Type.String({ description: "Path to the file to edit (relative or absolute)" }),
  edits: Type.Array(Type.Object({
    oldText: Type.String({ description: "Exact text for one targeted replacement. It must be unique in the original file and must not overlap with any other edits[].oldText in the same call." }),
    newText: Type.String({ description: "Replacement text for this targeted edit." }),
  }), { description: "One or more targeted replacements. Each edit is matched against the original file, not incrementally. Do not include overlapping or nested edits. If two changes touch the same block or nearby lines, merge them into one edit instead." }),
});

const defaultEditOperations = {
  readFile,
  writeFile: (path, content) => writeFile(path, content, "utf-8"),
  access: (path) => access(path, constants.R_OK | constants.W_OK),
};

function prepareEditArguments(input) {
  if (!input || typeof input !== "object") return input;
  const args = { ...input };
  if (typeof args.edits === "string") {
    try {
      const parsed = JSON.parse(args.edits);
      if (Array.isArray(parsed)) args.edits = parsed;
    } catch {}
  }
  if (typeof args.oldText !== "string" || typeof args.newText !== "string") return args;
  const edits = Array.isArray(args.edits) ? [...args.edits] : [];
  edits.push({ oldText: args.oldText, newText: args.newText });
  delete args.oldText;
  delete args.newText;
  return { ...args, edits };
}

export function createEditTool(cwd, options = {}) {
  const operations = options.operations ?? defaultEditOperations;
  return {
    name: "edit",
    label: "edit",
    description: "Edit a single file using exact text replacement. Every edits[].oldText must match a unique, non-overlapping region of the original file. If two changes affect the same block or nearby lines, merge them into one edit instead of emitting overlapping edits. Do not include large unchanged regions just to connect distant changes.",
    promptSnippet: "Make precise file edits with exact text replacement, including multiple disjoint edits in one call",
    promptGuidelines: [
      "Use edit for precise changes (edits[].oldText must match exactly)",
      "When changing multiple separate locations in one file, use one edit call with multiple entries in edits[] instead of multiple edit calls",
      "Each edits[].oldText is matched against the original file, not after earlier edits are applied. Do not emit overlapping or nested edits. Merge nearby changes into one edit.",
      "Keep edits[].oldText as small as possible while still being unique in the file. Do not pad with large unchanged regions.",
    ],
    parameters: editSchema,
    renderShell: "self",
    prepareArguments: prepareEditArguments,
    async execute(_id, input, signal) {
      const edits = input?.edits;
      if (!Array.isArray(edits) || edits.length === 0) throw new Error("Edit tool input is invalid. edits must contain at least one replacement.");
      const path = input.path;
      const absolutePath = resolveToCwd(path, cwd);
      return withFileMutationQueue(absolutePath, async () => {
        throwIfAborted(signal);
        try {
          await operations.access(absolutePath);
        } catch (error) {
          throw new Error(`Could not edit file: ${path}. ${error?.code ? `Error code: ${error.code}` : String(error)}.`);
        }
        throwIfAborted(signal);
        const raw = Buffer.from(await operations.readFile(absolutePath)).toString("utf-8");
        const bom = raw.startsWith("\ufeff") ? "\ufeff" : "";
        const text = bom ? raw.slice(1) : raw;
        const crlfIndex = text.indexOf("\r\n");
        const lfIndex = text.indexOf("\n");
        const lineEnding = lfIndex !== -1 && crlfIndex !== -1 && crlfIndex < lfIndex ? "\r\n" : "\n";
        const before = normalizeToLF(text);
        const after = applyEdits(before, edits, path);
        throwIfAborted(signal);
        await operations.writeFile(absolutePath, bom + (lineEnding === "\r\n" ? after.replaceAll("\n", "\r\n") : after));
        throwIfAborted(signal);
        const rendered = displayDiff(before, after);
        return { content: [{ type: "text", text: `Successfully replaced ${edits.length} block(s) in ${path}.` }], details: { diff: rendered.diff, patch: unifiedPatch(path, before, after), firstChangedLine: rendered.firstChangedLine } };
      });
    },
  };
}

const bashSchema = Type.Object({
  command: Type.String({ description: "Shell command to execute" }),
  timeout: Type.Optional(Type.Number({ description: "Timeout in seconds (optional, no default timeout)" })),
});

function localBashOperations(options = {}) {
  return {
    exec(command, cwd, execution = {}) {
      return new Promise((resolveExecution, reject) => {
        throwIfAborted(execution.signal);
        const shell = options.shellPath || process.env.SHELL || (process.platform === "win32" ? "cmd.exe" : "bash");
        const args = process.platform === "win32" ? ["/d", "/s", "/c", command] : ["-lc", command];
        const child = spawn(shell, args, { cwd, env: execution.env, windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
        let timedOut = false;
        const timer = execution.timeout ? setTimeout(() => { timedOut = true; child.kill(); }, execution.timeout * 1000) : undefined;
        const onAbort = () => child.kill();
        execution.signal?.addEventListener("abort", onAbort, { once: true });
        child.stdout?.on("data", execution.onData);
        child.stderr?.on("data", execution.onData);
        child.on("error", reject);
        child.on("close", (code) => {
          if (timer) clearTimeout(timer);
          execution.signal?.removeEventListener("abort", onAbort);
          if (execution.signal?.aborted) reject(new Error("aborted"));
          else if (timedOut) reject(new Error(`timeout:${execution.timeout}`));
          else resolveExecution({ exitCode: code });
        });
      });
    },
  };
}

export function createLocalBashOperations(options = {}) {
  return localBashOperations(options);
}

function bashEnvironment(ctx, exposeSessionEnvironment) {
  const env = { ...process.env };
  for (const key of ["PI_SESSION_ID", "PI_SESSION_FILE", "PI_PROVIDER", "PI_MODEL", "PI_REASONING_LEVEL"]) delete env[key];
  if (!exposeSessionEnvironment || !ctx) return env;
  env.PI_SESSION_ID = ctx.sessionManager?.getSessionId?.() ?? "";
  const file = ctx.sessionManager?.getSessionFile?.();
  if (file) env.PI_SESSION_FILE = file;
  if (ctx.model) {
    env.PI_PROVIDER = typeof ctx.model.provider === "string" ? ctx.model.provider : ctx.model.provider?.id;
    env.PI_MODEL = ctx.model.id;
  }
  if (ctx.thinkingLevel) env.PI_REASONING_LEVEL = ctx.thinkingLevel;
  return env;
}

function outputTempPath(prefix) {
  return join(tmpdir(), `${prefix}-${randomBytes(8).toString("hex")}.log`);
}

class OutputAccumulator {
  constructor(options = {}) {
    this.maxLines = options.maxLines ?? DEFAULT_MAX_LINES;
    this.maxBytes = options.maxBytes ?? DEFAULT_MAX_BYTES;
    this.maxRollingBytes = Math.max(this.maxBytes * 2, 1);
    this.tempFilePrefix = options.tempFilePrefix ?? "pi-output";
    this.decoder = new TextDecoder();
    this.rawChunks = [];
    this.tailText = "";
    this.tailBytes = 0;
    this.tailStartsAtLineBoundary = true;
    this.totalRawBytes = 0;
    this.totalDecodedBytes = 0;
    this.completedLines = 0;
    this.totalLines = 0;
    this.currentLineBytes = 0;
    this.hasOpenLine = false;
    this.finished = false;
    this.tempFilePath = undefined;
    this.tempFileStream = undefined;
  }

  append(data) {
    if (this.finished) throw new Error("Cannot append to a finished output accumulator");
    const buffer = Buffer.from(data);
    this.totalRawBytes += buffer.length;
    this.appendDecodedText(this.decoder.decode(buffer, { stream: true }));
    if (this.tempFileStream || this.shouldUseTempFile()) {
      this.ensureTempFile();
      this.tempFileStream?.write(buffer);
    } else if (buffer.length > 0) {
      this.rawChunks.push(buffer);
    }
  }

  finish() {
    if (this.finished) return;
    this.finished = true;
    this.appendDecodedText(this.decoder.decode());
    if (this.shouldUseTempFile()) this.ensureTempFile();
  }

  snapshot(options = {}) {
    const tail = truncateTail(this.snapshotText(), { maxLines: this.maxLines, maxBytes: this.maxBytes });
    const truncated = this.totalLines > this.maxLines || this.totalDecodedBytes > this.maxBytes;
    const truncation = {
      ...tail,
      truncated,
      truncatedBy: truncated ? (tail.truncatedBy ?? (this.totalDecodedBytes > this.maxBytes ? "bytes" : "lines")) : null,
      totalLines: this.totalLines,
      totalBytes: this.totalDecodedBytes,
      maxLines: this.maxLines,
      maxBytes: this.maxBytes,
    };
    if (options.persistIfTruncated && truncation.truncated) this.ensureTempFile();
    return { content: truncation.content, truncation, fullOutputPath: this.tempFilePath };
  }

  async closeTempFile() {
    if (!this.tempFileStream) return;
    const stream = this.tempFileStream;
    this.tempFileStream = undefined;
    await new Promise((resolveClose, reject) => {
      const onError = (error) => { stream.off("finish", onFinish); reject(error); };
      const onFinish = () => { stream.off("error", onError); resolveClose(); };
      stream.once("error", onError);
      stream.once("finish", onFinish);
      stream.end();
    });
  }

  appendDecodedText(text) {
    if (!text) return;
    const bytes = Buffer.byteLength(text, "utf-8");
    this.totalDecodedBytes += bytes;
    this.tailText += text;
    this.tailBytes += bytes;
    if (this.tailBytes > this.maxRollingBytes * 2) this.trimTail();
    let newlines = 0;
    let lastNewline = -1;
    for (let index = text.indexOf("\n"); index !== -1; index = text.indexOf("\n", index + 1)) {
      newlines++;
      lastNewline = index;
    }
    if (newlines === 0) {
      this.currentLineBytes += bytes;
      this.hasOpenLine = true;
    } else {
      this.completedLines += newlines;
      const tail = text.slice(lastNewline + 1);
      this.currentLineBytes = Buffer.byteLength(tail, "utf-8");
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
    while (start < buffer.length && (buffer[start] & 0xc0) === 0x80) start++;
    this.tailStartsAtLineBoundary = start === 0 ? this.tailStartsAtLineBoundary : buffer[start - 1] === 0x0a;
    this.tailText = buffer.subarray(start).toString("utf-8");
    this.tailBytes = Buffer.byteLength(this.tailText, "utf-8");
  }

  snapshotText() {
    if (this.tailStartsAtLineBoundary) return this.tailText;
    const firstNewline = this.tailText.indexOf("\n");
    return firstNewline === -1 ? this.tailText : this.tailText.slice(firstNewline + 1);
  }

  getLastLineBytes() {
    return this.currentLineBytes;
  }

  shouldUseTempFile() {
    return this.totalRawBytes > this.maxBytes || this.totalDecodedBytes > this.maxBytes || this.totalLines > this.maxLines;
  }

  ensureTempFile() {
    if (this.tempFilePath) return;
    this.tempFilePath = outputTempPath(this.tempFilePrefix);
    this.tempFileStream = createWriteStream(this.tempFilePath);
    for (const chunk of this.rawChunks) this.tempFileStream.write(chunk);
    this.rawChunks = [];
  }
}

export function createBashTool(cwd, options = {}) {
  const operations = options.operations ?? localBashOperations({ shellPath: options.shellPath });
  const exposeSessionEnvironment = options.exposeSessionEnvironment ?? true;
  return {
    name: "bash",
    label: "bash",
    description: `Execute a bash command in the current working directory. Returns stdout and stderr. Output is truncated to last ${DEFAULT_MAX_LINES} lines or ${DEFAULT_MAX_BYTES / 1024}KB (whichever is hit first). If truncated, full output is saved to a temp file. Optionally provide a timeout in seconds.`,
    promptSnippet: "Execute bash commands (ls, grep, find, etc.)",
    promptGuidelines: exposeSessionEnvironment ? ["Inspect PI_* environment variables for current model and session details."] : undefined,
    parameters: bashSchema,
    async execute(_id, { command, timeout }, signal, onUpdate, ctx) {
      if (timeout !== undefined && (!Number.isFinite(timeout) || timeout <= 0)) throw new Error("Invalid timeout: must be a finite number of seconds");
      if (timeout !== undefined && timeout * 1000 > 2_147_483_647) throw new Error("Invalid timeout: maximum is 2147483.647 seconds");
      let spawnContext = { command: options.commandPrefix ? `${options.commandPrefix}\n${command}` : command, cwd, env: bashEnvironment(ctx, exposeSessionEnvironment) };
      if (options.spawnHook) spawnContext = options.spawnHook(spawnContext);
      const output = new OutputAccumulator({ tempFilePrefix: "pi-bash" });
      let acceptingOutput = true;
      let updateTimer;
      let updateDirty = false;
      let lastUpdateAt = 0;
      const emitUpdate = () => {
        if (!onUpdate || !updateDirty) return;
        updateDirty = false;
        lastUpdateAt = Date.now();
        const snapshot = output.snapshot({ persistIfTruncated: true });
        onUpdate({ content: [{ type: "text", text: snapshot.content || "" }], details: { truncation: snapshot.truncation.truncated ? snapshot.truncation : undefined, fullOutputPath: snapshot.fullOutputPath } });
      };
      const clearUpdateTimer = () => {
        if (updateTimer) clearTimeout(updateTimer);
        updateTimer = undefined;
      };
      const scheduleUpdate = () => {
        if (!onUpdate) return;
        updateDirty = true;
        const delay = 100 - (Date.now() - lastUpdateAt);
        if (delay <= 0) {
          clearUpdateTimer();
          emitUpdate();
        } else if (!updateTimer) {
          updateTimer = setTimeout(() => { updateTimer = undefined; emitUpdate(); }, delay);
        }
      };
      onUpdate?.({ content: [], details: undefined });
      const onData = (data) => {
        if (!acceptingOutput) return;
        output.append(Buffer.from(data));
        scheduleUpdate();
      };
      const finishOutput = async () => {
        acceptingOutput = false;
        output.finish();
        clearUpdateTimer();
        emitUpdate();
        const snapshot = output.snapshot({ persistIfTruncated: true });
        await output.closeTempFile();
        return snapshot;
      };
      const formatOutput = (snapshot, emptyText = "(no output)") => {
        let text = snapshot.content || emptyText;
        let details;
        if (snapshot.truncation.truncated) {
          details = { truncation: snapshot.truncation, fullOutputPath: snapshot.fullOutputPath };
          const endLine = snapshot.truncation.totalLines;
          if (snapshot.truncation.lastLinePartial) {
            text += `\n\n[Showing last ${formatSize(snapshot.truncation.outputBytes)} of line ${endLine} (line is ${formatSize(output.getLastLineBytes())}). Full output: ${snapshot.fullOutputPath}]`;
          } else {
            const firstLine = endLine - snapshot.truncation.outputLines + 1;
            const size = snapshot.truncation.truncatedBy === "bytes" ? ` (${formatSize(DEFAULT_MAX_BYTES)} limit)` : "";
            text += `\n\n[Showing lines ${firstLine}-${endLine} of ${endLine}${size}. Full output: ${snapshot.fullOutputPath}]`;
          }
        }
        return { text, details };
      };
      try {
        let exitCode;
        try {
          ({ exitCode } = await operations.exec(spawnContext.command, spawnContext.cwd, { onData, signal, timeout, env: spawnContext.env }));
        } catch (error) {
          const snapshot = await finishOutput();
          const { text } = formatOutput(snapshot, "");
          if (error?.message === "aborted") throw new Error(`${text ? `${text}\n\n` : ""}Command aborted`);
          if (error?.message?.startsWith("timeout:")) throw new Error(`${text ? `${text}\n\n` : ""}Command timed out after ${error.message.slice(8)} seconds`);
          throw error;
        }
        const snapshot = await finishOutput();
        const formatted = formatOutput(snapshot);
        if (exitCode !== 0 && exitCode !== null) throw new Error(`${formatted.text}\n\nCommand exited with code ${exitCode}`);
        return { content: [{ type: "text", text: formatted.text }], details: formatted.details };
      } finally {
        clearUpdateTimer();
      }
    },
  };
}

const findSchema = Type.Object({
  pattern: Type.String({ description: "Glob pattern to match files, e.g. '*.ts', '**/*.json', or 'src/**/*.spec.ts'" }),
  path: Type.Optional(Type.String({ description: "Directory to search in (default: current directory)" })),
  limit: Type.Optional(Type.Number({ description: "Maximum number of results (default: 1000)" })),
});

function runLines(command, args, signal) {
  return new Promise((resolveLines, reject) => {
    throwIfAborted(signal);
    const child = spawn(command, args, { stdio: ["ignore", "pipe", "pipe"] });
    const stdout = [];
    const stderr = [];
    const onAbort = () => child.kill();
    signal?.addEventListener("abort", onAbort, { once: true });
    child.stdout.on("data", (chunk) => stdout.push(chunk));
    child.stderr.on("data", (chunk) => stderr.push(chunk));
    child.on("error", reject);
    child.on("close", (code) => {
      signal?.removeEventListener("abort", onAbort);
      if (signal?.aborted) return reject(abortError());
      if (code !== 0 && code !== 1) return reject(new Error(Buffer.concat(stderr).toString().trim() || `${command} exited with code ${code}`));
      resolveLines(Buffer.concat(stdout).toString().split(/\r?\n/).filter(Boolean));
    });
  });
}

export function createFindTool(cwd, options = {}) {
  return {
    name: "find",
    label: "find",
    description: `Search for files by glob pattern. Returns matching file paths relative to the search directory. Respects .gitignore. Output is truncated to 1000 results or ${DEFAULT_MAX_BYTES / 1024}KB (whichever is hit first).`,
    promptSnippet: "Find files by glob pattern (respects .gitignore)",
    parameters: findSchema,
    async execute(_id, { pattern, path = ".", limit = 1000 }, signal) {
      const searchPath = resolveToCwd(path, cwd);
      let results;
      if (options.operations?.glob) {
        if (!(await options.operations.exists(searchPath))) throw new Error(`Path not found: ${searchPath}`);
        results = await options.operations.glob(pattern, searchPath, { ignore: ["**/node_modules/**", "**/.git/**"], limit });
      } else {
        const fd = process.env.PIG_FD_PATH || "fd";
        results = await runLines(fd, ["--glob", "--color=never", "--hidden", "--no-require-git", "--max-results", String(limit), "--", pattern, searchPath], signal);
      }
      throwIfAborted(signal);
      if (results.length === 0) return { content: [{ type: "text", text: "No files found matching pattern" }], details: undefined };
      const normalized = results.map((value) => (isAbsolute(value) ? relative(searchPath, value) : value).split("\\").join("/"));
      const reached = normalized.length >= limit;
      const truncation = truncateHead(normalized.join("\n"), { maxLines: Number.MAX_SAFE_INTEGER });
      const notices = [];
      const details = {};
      if (reached) { notices.push(`${limit} results limit reached`); details.resultLimitReached = limit; }
      if (truncation.truncated) { notices.push(`${formatSize(DEFAULT_MAX_BYTES)} limit reached`); details.truncation = truncation; }
      const output = truncation.content + (notices.length ? `\n\n[${notices.join(". ")}]` : "");
      return { content: [{ type: "text", text: output }], details: Object.keys(details).length ? details : undefined };
    },
  };
}

const grepSchema = Type.Object({
  pattern: Type.String({ description: "Search pattern (regex or literal string)" }),
  path: Type.Optional(Type.String({ description: "Directory or file to search (default: current directory)" })),
  glob: Type.Optional(Type.String({ description: "Filter files by glob pattern, e.g. '*.ts' or '**/*.spec.ts'" })),
  ignoreCase: Type.Optional(Type.Boolean({ description: "Case-insensitive search (default: false)" })),
  literal: Type.Optional(Type.Boolean({ description: "Treat pattern as literal string instead of regex (default: false)" })),
  context: Type.Optional(Type.Number({ description: "Number of lines to show before and after each match (default: 0)" })),
  limit: Type.Optional(Type.Number({ description: "Maximum number of matches to return (default: 100)" })),
});

export function createGrepTool(cwd, _options = {}) {
  return {
    name: "grep",
    label: "grep",
    description: `Search file contents for a pattern. Returns matching lines with file paths and line numbers. Respects .gitignore. Output is truncated to 100 matches or ${DEFAULT_MAX_BYTES / 1024}KB (whichever is hit first). Long lines are truncated to ${GREP_MAX_LINE_LENGTH} chars.`,
    promptSnippet: "Search file contents for patterns (respects .gitignore)",
    parameters: grepSchema,
    async execute(_id, { pattern, path = ".", glob, ignoreCase, literal, context = 0, limit = 100 }, signal) {
      const searchPath = resolveToCwd(path, cwd);
      const args = ["--line-number", "--color=never", "--hidden"];
      if (ignoreCase) args.push("--ignore-case");
      if (literal) args.push("--fixed-strings");
      if (context > 0) args.push("--context", String(context));
      if (glob) args.push("--glob", glob);
      args.push("--", pattern, searchPath);
      const allLines = await runLines(process.env.PIG_RG_PATH || "rg", args, signal);
      if (allLines.length === 0) return { content: [{ type: "text", text: "No matches found" }], details: undefined };
      let linesTruncated = false;
      const selected = allLines.slice(0, Math.max(1, limit)).map((line) => {
        const result = truncateLine(line);
        linesTruncated ||= result.wasTruncated;
        return result.text;
      });
      const matchLimitReached = allLines.length >= Math.max(1, limit);
      const truncation = truncateHead(selected.join("\n"), { maxLines: Number.MAX_SAFE_INTEGER });
      const notices = [];
      const details = {};
      if (matchLimitReached) { notices.push(`${Math.max(1, limit)} matches limit reached`); details.matchLimitReached = Math.max(1, limit); }
      if (truncation.truncated) { notices.push(`${formatSize(DEFAULT_MAX_BYTES)} limit reached`); details.truncation = truncation; }
      if (linesTruncated) { notices.push(`Some lines truncated to ${GREP_MAX_LINE_LENGTH} chars`); details.linesTruncated = true; }
      const output = truncation.content + (notices.length ? `\n\n[${notices.join(". ")}]` : "");
      return { content: [{ type: "text", text: output }], details: Object.keys(details).length ? details : undefined };
    },
  };
}

const lsSchema = Type.Object({
  path: Type.Optional(Type.String({ description: "Directory to list (default: current directory)" })),
  limit: Type.Optional(Type.Number({ description: "Maximum number of entries to return (default: 500)" })),
});

const defaultLsOperations = { exists, stat, readdir };

export function createLsTool(cwd, options = {}) {
  const operations = options.operations ?? defaultLsOperations;
  return {
    name: "ls",
    label: "ls",
    description: `List directory contents. Returns entries sorted alphabetically, with '/' suffix for directories. Includes dotfiles. Output is truncated to 500 entries or ${DEFAULT_MAX_BYTES / 1024}KB (whichever is hit first).`,
    promptSnippet: "List directory contents",
    parameters: lsSchema,
    async execute(_id, { path = ".", limit = 500 }, signal) {
      throwIfAborted(signal);
      const dirPath = resolveToCwd(path, cwd);
      if (!(await operations.exists(dirPath))) throw new Error(`Path not found: ${dirPath}`);
      if (!(await operations.stat(dirPath)).isDirectory()) throw new Error(`Not a directory: ${dirPath}`);
      const entries = await operations.readdir(dirPath);
      entries.sort((a, b) => a.toLowerCase().localeCompare(b.toLowerCase()));
      const results = [];
      for (const entry of entries) {
        if (results.length >= limit) break;
        throwIfAborted(signal);
        try {
          results.push(entry + ((await operations.stat(join(dirPath, entry))).isDirectory() ? "/" : ""));
        } catch {}
      }
      if (results.length === 0) return { content: [{ type: "text", text: "(empty directory)" }], details: undefined };
      const reached = results.length < entries.length;
      const truncation = truncateHead(results.join("\n"), { maxLines: Number.MAX_SAFE_INTEGER });
      const notices = [];
      const details = {};
      if (reached) { notices.push(`${limit} entries limit reached. Use limit=${limit * 2} for more`); details.entryLimitReached = limit; }
      if (truncation.truncated) { notices.push(`${formatSize(DEFAULT_MAX_BYTES)} limit reached`); details.truncation = truncation; }
      const output = truncation.content + (notices.length ? `\n\n[${notices.join(". ")}]` : "");
      return { content: [{ type: "text", text: output }], details: Object.keys(details).length ? details : undefined };
    },
  };
}

export function createTool(name, cwd, options = {}) {
  const factories = { read: createReadTool, bash: createBashTool, edit: createEditTool, write: createWriteTool, grep: createGrepTool, find: createFindTool, ls: createLsTool };
  const factory = factories[name];
  if (!factory) throw new Error(`Unknown tool name: ${name}`);
  return factory(cwd, options[name]);
}

export const allToolNames = new Set(["read", "bash", "edit", "write", "grep", "find", "ls"]);

export function createCodingTools(cwd, options = {}) {
  return [createReadTool(cwd, options.read), createBashTool(cwd, options.bash), createEditTool(cwd, options.edit), createWriteTool(cwd, options.write)];
}

export function createReadOnlyTools(cwd, options = {}) {
  return [createReadTool(cwd, options.read), createGrepTool(cwd, options.grep), createFindTool(cwd, options.find), createLsTool(cwd, options.ls)];
}

export function createAllTools(cwd, options = {}) {
  return Object.fromEntries([...allToolNames].map((name) => [name, createTool(name, cwd, options)]));
}
