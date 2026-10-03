var __defProp = Object.defineProperty;
var __name = (target, value) => __defProp(target, "name", { value, configurable: true });

// pi-dist/pi-tui/index.js
import { Marked as Marked2 } from "../../../marked/lib/marked.esm.js";

// pi-dist/pi-tui/autocomplete.js
import { spawn } from "child_process";
import { readdirSync, statSync } from "fs";
import { homedir } from "os";
import { basename, dirname, join } from "path";

// pi-dist/pi-tui/fuzzy.js
function fuzzyMatch(query, text) {
  const queryLower = query.toLowerCase();
  const textLower = text.toLowerCase();
  const matchQuery = /* @__PURE__ */ __name((normalizedQuery) => {
    if (normalizedQuery.length === 0) {
      return { matches: true, score: 0 };
    }
    if (normalizedQuery.length > textLower.length) {
      return { matches: false, score: 0 };
    }
    let queryIndex = 0;
    let score = 0;
    let lastMatchIndex = -1;
    let consecutiveMatches = 0;
    while (queryIndex < normalizedQuery.length) {
      const i = textLower.indexOf(normalizedQuery[queryIndex], lastMatchIndex + 1);
      if (i === -1)
        break;
      const isWordBoundary = i === 0 || /[\s\-_./:]/.test(textLower[i - 1]);
      if (lastMatchIndex === i - 1) {
        consecutiveMatches++;
        score -= consecutiveMatches * 5;
      } else {
        consecutiveMatches = 0;
        if (lastMatchIndex >= 0) {
          score += (i - lastMatchIndex - 1) * 2;
        }
      }
      if (isWordBoundary) {
        score -= 10;
      }
      score += i * 0.1;
      lastMatchIndex = i;
      queryIndex++;
    }
    if (queryIndex < normalizedQuery.length) {
      return { matches: false, score: 0 };
    }
    if (normalizedQuery === textLower) {
      score -= 100;
    }
    return { matches: true, score };
  }, "matchQuery");
  const primaryMatch = matchQuery(queryLower);
  if (primaryMatch.matches) {
    return primaryMatch;
  }
  const alphaNumericMatch = queryLower.match(/^(?<letters>[a-z]+)(?<digits>[0-9]+)$/);
  const numericAlphaMatch = queryLower.match(/^(?<digits>[0-9]+)(?<letters>[a-z]+)$/);
  const swappedQuery = alphaNumericMatch ? `${alphaNumericMatch.groups?.digits ?? ""}${alphaNumericMatch.groups?.letters ?? ""}` : numericAlphaMatch ? `${numericAlphaMatch.groups?.letters ?? ""}${numericAlphaMatch.groups?.digits ?? ""}` : "";
  if (!swappedQuery) {
    return primaryMatch;
  }
  const swappedMatch = matchQuery(swappedQuery);
  if (!swappedMatch.matches) {
    return primaryMatch;
  }
  return { matches: true, score: swappedMatch.score + 5 };
}
__name(fuzzyMatch, "fuzzyMatch");
function fuzzyFilter(items, query, getText) {
  if (!query.trim()) {
    return items;
  }
  const tokens = query.trim().split(/[\s/]+/).filter((t) => t.length > 0);
  if (tokens.length === 0) {
    return items;
  }
  const results = [];
  for (const item of items) {
    const text = getText(item);
    let totalScore = 0;
    let allMatch = true;
    for (const token of tokens) {
      const match = fuzzyMatch(token, text);
      if (match.matches) {
        totalScore += match.score;
      } else {
        allMatch = false;
        break;
      }
    }
    if (allMatch) {
      results.push({ item, totalScore });
    }
  }
  results.sort((a, b) => a.totalScore - b.totalScore);
  return results.map((r) => r.item);
}
__name(fuzzyFilter, "fuzzyFilter");

// pi-dist/pi-tui/autocomplete.js
import { autocompleteBoundaryRegex, autocompleteSeparatorRegex } from "../utils.js";
var PATH_DELIMITERS = /* @__PURE__ */ new Set([" ", "	", '"', "'", "="]);
var tokenStartRegex = new RegExp(`${autocompleteBoundaryRegex.source}$`, "u");
var PATH_WRAPPERS = { "(": ")", "[": "]", "{": "}", "<": ">", "`": "`" };
function toDisplayPath(value) {
  return value.replace(/\\/g, "/");
}
__name(toDisplayPath, "toDisplayPath");
function escapeRegex(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
__name(escapeRegex, "escapeRegex");
function buildFdPathQuery(query) {
  const normalized = toDisplayPath(query);
  if (!normalized.includes("/")) {
    return normalized;
  }
  const hasTrailingSeparator = normalized.endsWith("/");
  const trimmed = normalized.replace(/^\/+|\/+$/g, "");
  if (!trimmed) {
    return normalized;
  }
  const separatorPattern = "[\\\\/]";
  const segments = trimmed.split("/").filter(Boolean).map((segment) => escapeRegex(segment));
  if (segments.length === 0) {
    return normalized;
  }
  let pattern = segments.join(separatorPattern);
  if (hasTrailingSeparator) {
    pattern += separatorPattern;
  }
  return pattern;
}
__name(buildFdPathQuery, "buildFdPathQuery");
function findLastDelimiter(text) {
  let lastDelimiter = -1;
  let index = 0;
  for (const character of text) {
    index += character.length;
    if (PATH_DELIMITERS.has(character) || autocompleteSeparatorRegex.test(character)) {
      lastDelimiter = index - 1;
    }
  }
  return lastDelimiter;
}
__name(findLastDelimiter, "findLastDelimiter");
function stripLeadingWrappers(token) {
  let result = token;
  while (result.length > 0) {
    const closer = PATH_WRAPPERS[result[0]];
    if (!closer || result.includes(closer, 1)) {
      break;
    }
    result = result.slice(1);
  }
  return result;
}
__name(stripLeadingWrappers, "stripLeadingWrappers");
function findUnclosedQuoteStart(text) {
  let inQuotes = false;
  let quoteStart = -1;
  for (let i = 0; i < text.length; i += 1) {
    if (text[i] === '"') {
      inQuotes = !inQuotes;
      if (inQuotes) {
        quoteStart = i;
      }
    }
  }
  return inQuotes ? quoteStart : null;
}
__name(findUnclosedQuoteStart, "findUnclosedQuoteStart");
function isTokenStart(text, index) {
  let start = index;
  while (start > 0 && PATH_WRAPPERS[text[start - 1]]) {
    start -= 1;
  }
  return PATH_DELIMITERS.has(text[start - 1] ?? "") || tokenStartRegex.test(text.slice(0, start));
}
__name(isTokenStart, "isTokenStart");
function extractQuotedPrefix(text) {
  const quoteStart = findUnclosedQuoteStart(text);
  if (quoteStart === null) {
    return null;
  }
  if (quoteStart > 0 && text[quoteStart - 1] === "@") {
    if (!isTokenStart(text, quoteStart - 1)) {
      return null;
    }
    return text.slice(quoteStart - 1);
  }
  if (!isTokenStart(text, quoteStart)) {
    return null;
  }
  return text.slice(quoteStart);
}
__name(extractQuotedPrefix, "extractQuotedPrefix");
function parsePathPrefix(prefix) {
  if (prefix.startsWith('@"')) {
    return { rawPrefix: prefix.slice(2), isAtPrefix: true, isQuotedPrefix: true };
  }
  if (prefix.startsWith('"')) {
    return { rawPrefix: prefix.slice(1), isAtPrefix: false, isQuotedPrefix: true };
  }
  if (prefix.startsWith("@")) {
    return { rawPrefix: prefix.slice(1), isAtPrefix: true, isQuotedPrefix: false };
  }
  return { rawPrefix: prefix, isAtPrefix: false, isQuotedPrefix: false };
}
__name(parsePathPrefix, "parsePathPrefix");
function buildCompletionValue(path3, options) {
  const needsQuotes = options.isQuotedPrefix || autocompleteSeparatorRegex.test(path3);
  const prefix = options.isAtPrefix ? "@" : "";
  if (!needsQuotes) {
    return `${prefix}${path3}`;
  }
  const openQuote = `${prefix}"`;
  const closeQuote = '"';
  return `${openQuote}${path3}${closeQuote}`;
}
__name(buildCompletionValue, "buildCompletionValue");
async function walkDirectoryWithFd(baseDir, fdPath, query, maxResults, signal, maxDepth) {
  const args = [
    "--base-directory",
    baseDir,
    "--max-results",
    String(maxResults),
    "--type",
    "f",
    "--type",
    "d",
    "--follow",
    "--hidden",
    "--exclude",
    ".git",
    "--exclude",
    ".git/*",
    "--exclude",
    ".git/**"
  ];
  if (maxDepth !== void 0) {
    args.push("--max-depth", String(maxDepth));
  }
  if (toDisplayPath(query).includes("/")) {
    args.push("--full-path");
  }
  if (query) {
    args.push(buildFdPathQuery(query));
  }
  return await new Promise((resolve) => {
    if (signal.aborted) {
      resolve([]);
      return;
    }
    const child = spawn(fdPath, args, {
      stdio: ["ignore", "pipe", "pipe"]
    });
    let stdout = "";
    let resolved = false;
    const finish = /* @__PURE__ */ __name((results) => {
      if (resolved)
        return;
      resolved = true;
      signal.removeEventListener("abort", onAbort);
      resolve(results);
    }, "finish");
    const onAbort = /* @__PURE__ */ __name(() => {
      if (child.exitCode === null) {
        child.kill("SIGKILL");
      }
    }, "onAbort");
    signal.addEventListener("abort", onAbort, { once: true });
    child.stdout.setEncoding("utf-8");
    child.stdout.on("data", (chunk) => {
      stdout += chunk;
    });
    child.on("error", () => {
      finish([]);
    });
    child.on("close", (code) => {
      if (signal.aborted || code !== 0 || !stdout) {
        finish([]);
        return;
      }
      const lines = stdout.trim().split("\n").filter(Boolean);
      const results = [];
      for (const line of lines) {
        const displayLine = toDisplayPath(line);
        const hasTrailingSeparator = displayLine.endsWith("/");
        const normalizedPath = hasTrailingSeparator ? displayLine.slice(0, -1) : displayLine;
        if (normalizedPath === ".git" || normalizedPath.startsWith(".git/") || normalizedPath.includes("/.git/")) {
          continue;
        }
        results.push({
          path: displayLine,
          isDirectory: hasTrailingSeparator
        });
      }
      finish(results);
    });
  });
}
__name(walkDirectoryWithFd, "walkDirectoryWithFd");
var CombinedAutocompleteProvider = class {
  static {
    __name(this, "CombinedAutocompleteProvider");
  }
  commands;
  basePath;
  fdPath;
  constructor(commands = [], basePath, fdPath = null) {
    this.commands = commands;
    this.basePath = basePath;
    this.fdPath = fdPath;
  }
  async getSuggestions(lines, cursorLine, cursorCol, options) {
    const currentLine = lines[cursorLine] || "";
    const textBeforeCursor = currentLine.slice(0, cursorCol);
    const atPrefix = this.extractAtPrefix(textBeforeCursor);
    if (atPrefix) {
      const { rawPrefix, isQuotedPrefix } = parsePathPrefix(atPrefix);
      const suggestions2 = await this.getFuzzyFileSuggestions(rawPrefix, {
        isQuotedPrefix,
        signal: options.signal
      });
      if (suggestions2.length === 0)
        return null;
      return {
        items: suggestions2,
        prefix: atPrefix
      };
    }
    const commandText = textBeforeCursor.trimStart();
    if (!options.force && commandText.startsWith("/")) {
      const spaceIndex = commandText.indexOf(" ");
      if (spaceIndex === -1) {
        const prefix = commandText.slice(1);
        const commandItems = this.commands.map((cmd) => {
          const name = "name" in cmd ? cmd.name : cmd.value;
          const hint = "argumentHint" in cmd && cmd.argumentHint ? cmd.argumentHint : void 0;
          const desc = cmd.description ?? "";
          const fullDesc = hint ? desc ? `${hint} \u2014 ${desc}` : hint : desc;
          return {
            name,
            label: name,
            description: fullDesc || void 0
          };
        });
        const bareNameMatches = fuzzyFilter(commandItems, prefix, (item) => item.name.startsWith("skill:") ? item.name.slice("skill:".length) : item.name);
        const bareNameMatchSet = new Set(bareNameMatches);
        const fullNameOnlyMatches = fuzzyFilter(commandItems.filter((item) => item.name.startsWith("skill:") && !bareNameMatchSet.has(item)), prefix, (item) => item.name);
        const filtered = [...bareNameMatches, ...fullNameOnlyMatches].map((item) => ({
          value: item.name,
          label: item.label,
          ...item.description && { description: item.description }
        }));
        if (filtered.length === 0)
          return null;
        return {
          items: filtered,
          prefix: commandText
        };
      }
      const commandName = commandText.slice(1, spaceIndex);
      const argumentText = commandText.slice(spaceIndex + 1);
      const command = this.commands.find((cmd) => {
        const name = "name" in cmd ? cmd.name : cmd.value;
        return name === commandName;
      });
      if (!command || !("getArgumentCompletions" in command) || !command.getArgumentCompletions) {
        return null;
      }
      const argumentSuggestions = await command.getArgumentCompletions(argumentText);
      if (!Array.isArray(argumentSuggestions) || argumentSuggestions.length === 0) {
        return null;
      }
      return {
        items: argumentSuggestions,
        prefix: argumentText
      };
    }
    const pathMatch = this.extractPathPrefix(textBeforeCursor, options.force ?? false);
    if (pathMatch === null) {
      return null;
    }
    const suggestions = this.getFileSuggestions(pathMatch);
    if (suggestions.length === 0)
      return null;
    return {
      items: suggestions,
      prefix: pathMatch
    };
  }
  applyCompletion(lines, cursorLine, cursorCol, item, prefix) {
    const currentLine = lines[cursorLine] || "";
    const beforePrefix = currentLine.slice(0, cursorCol - prefix.length);
    const afterCursor = currentLine.slice(cursorCol);
    const isQuotedPrefix = prefix.startsWith('"') || prefix.startsWith('@"');
    const hasLeadingQuoteAfterCursor = afterCursor.startsWith('"');
    const hasTrailingQuoteInItem = item.value.endsWith('"');
    const adjustedAfterCursor = isQuotedPrefix && hasTrailingQuoteInItem && hasLeadingQuoteAfterCursor ? afterCursor.slice(1) : afterCursor;
    const isSlashCommand = prefix.startsWith("/") && beforePrefix.trim() === "" && !prefix.slice(1).includes("/");
    if (isSlashCommand) {
      const newLine2 = `${beforePrefix}/${item.value} ${adjustedAfterCursor}`;
      const newLines2 = [...lines];
      newLines2[cursorLine] = newLine2;
      return {
        lines: newLines2,
        cursorLine,
        cursorCol: beforePrefix.length + item.value.length + 2
        // +2 for "/" and space
      };
    }
    if (prefix.startsWith("@")) {
      const isDirectory2 = item.label.endsWith("/");
      const suffix = isDirectory2 ? "" : " ";
      const newLine2 = `${beforePrefix + item.value}${suffix}${adjustedAfterCursor}`;
      const newLines2 = [...lines];
      newLines2[cursorLine] = newLine2;
      const hasTrailingQuote2 = item.value.endsWith('"');
      const cursorOffset2 = isDirectory2 && hasTrailingQuote2 ? item.value.length - 1 : item.value.length;
      return {
        lines: newLines2,
        cursorLine,
        cursorCol: beforePrefix.length + cursorOffset2 + suffix.length
      };
    }
    const textBeforeCursor = currentLine.slice(0, cursorCol);
    if (textBeforeCursor.includes("/") && textBeforeCursor.includes(" ")) {
      const newLine2 = beforePrefix + item.value + adjustedAfterCursor;
      const newLines2 = [...lines];
      newLines2[cursorLine] = newLine2;
      const isDirectory2 = item.label.endsWith("/");
      const hasTrailingQuote2 = item.value.endsWith('"');
      const cursorOffset2 = isDirectory2 && hasTrailingQuote2 ? item.value.length - 1 : item.value.length;
      return {
        lines: newLines2,
        cursorLine,
        cursorCol: beforePrefix.length + cursorOffset2
      };
    }
    const newLine = beforePrefix + item.value + adjustedAfterCursor;
    const newLines = [...lines];
    newLines[cursorLine] = newLine;
    const isDirectory = item.label.endsWith("/");
    const hasTrailingQuote = item.value.endsWith('"');
    const cursorOffset = isDirectory && hasTrailingQuote ? item.value.length - 1 : item.value.length;
    return {
      lines: newLines,
      cursorLine,
      cursorCol: beforePrefix.length + cursorOffset
    };
  }
  // Extract @ prefix for fuzzy file suggestions
  extractAtPrefix(text) {
    const quotedPrefix = extractQuotedPrefix(text);
    if (quotedPrefix?.startsWith('@"')) {
      return quotedPrefix;
    }
    const lastDelimiterIndex = findLastDelimiter(text);
    const token = stripLeadingWrappers(lastDelimiterIndex === -1 ? text : text.slice(lastDelimiterIndex + 1));
    if (token.startsWith("@")) {
      return token;
    }
    return null;
  }
  // Extract a path-like prefix from the text before cursor
  extractPathPrefix(text, forceExtract = false) {
    const quotedPrefix = extractQuotedPrefix(text);
    if (quotedPrefix) {
      return quotedPrefix;
    }
    const lastDelimiterIndex = findLastDelimiter(text);
    const pathPrefix = stripLeadingWrappers(lastDelimiterIndex === -1 ? text : text.slice(lastDelimiterIndex + 1));
    if (forceExtract) {
      return pathPrefix;
    }
    if (pathPrefix.includes("/") || pathPrefix.startsWith(".") || pathPrefix.startsWith("~/")) {
      return pathPrefix;
    }
    if (pathPrefix === "" && text !== "" && tokenStartRegex.test(text)) {
      return pathPrefix;
    }
    return null;
  }
  // Expand home directory (~/) to actual home path
  expandHomePath(path3) {
    if (path3.startsWith("~/")) {
      const expandedPath = join(homedir(), path3.slice(2));
      return path3.endsWith("/") && !expandedPath.endsWith("/") ? `${expandedPath}/` : expandedPath;
    } else if (path3 === "~") {
      return homedir();
    }
    return path3;
  }
  resolveScopedFuzzyQuery(rawQuery) {
    const normalizedQuery = toDisplayPath(rawQuery);
    const slashIndex = normalizedQuery.lastIndexOf("/");
    if (slashIndex === -1) {
      return null;
    }
    const displayBase = normalizedQuery.slice(0, slashIndex + 1);
    const query = normalizedQuery.slice(slashIndex + 1);
    let baseDir;
    if (displayBase.startsWith("~/")) {
      baseDir = this.expandHomePath(displayBase);
    } else if (displayBase.startsWith("/")) {
      baseDir = displayBase;
    } else {
      baseDir = join(this.basePath, displayBase);
    }
    try {
      if (!statSync(baseDir).isDirectory()) {
        return null;
      }
    } catch {
      return null;
    }
    return { baseDir, query, displayBase };
  }
  scopedPathForDisplay(displayBase, relativePath) {
    const normalizedRelativePath = toDisplayPath(relativePath);
    if (displayBase === "/") {
      return `/${normalizedRelativePath}`;
    }
    return `${toDisplayPath(displayBase)}${normalizedRelativePath}`;
  }
  // Get file/directory suggestions for a given path prefix
  getFileSuggestions(prefix) {
    try {
      let searchDir;
      let searchPrefix;
      const { rawPrefix, isAtPrefix, isQuotedPrefix } = parsePathPrefix(prefix);
      let expandedPrefix = rawPrefix;
      if (expandedPrefix.startsWith("~")) {
        expandedPrefix = this.expandHomePath(expandedPrefix);
      }
      const isRootPrefix = rawPrefix === "" || rawPrefix === "./" || rawPrefix === "../" || rawPrefix === "~" || rawPrefix === "~/" || rawPrefix === "/" || isAtPrefix && rawPrefix === "";
      if (isRootPrefix) {
        if (rawPrefix.startsWith("~") || expandedPrefix.startsWith("/")) {
          searchDir = expandedPrefix;
        } else {
          searchDir = join(this.basePath, expandedPrefix);
        }
        searchPrefix = "";
      } else if (rawPrefix.endsWith("/")) {
        if (rawPrefix.startsWith("~") || expandedPrefix.startsWith("/")) {
          searchDir = expandedPrefix;
        } else {
          searchDir = join(this.basePath, expandedPrefix);
        }
        searchPrefix = "";
      } else {
        const dir = dirname(expandedPrefix);
        const file = basename(expandedPrefix);
        if (rawPrefix.startsWith("~") || expandedPrefix.startsWith("/")) {
          searchDir = dir;
        } else {
          searchDir = join(this.basePath, dir);
        }
        searchPrefix = file;
      }
      const entries = readdirSync(searchDir, { withFileTypes: true });
      const suggestions = [];
      for (const entry of entries) {
        if (!entry.name.toLowerCase().startsWith(searchPrefix.toLowerCase())) {
          continue;
        }
        let isDirectory = entry.isDirectory();
        if (!isDirectory && entry.isSymbolicLink()) {
          try {
            const fullPath = join(searchDir, entry.name);
            isDirectory = statSync(fullPath).isDirectory();
          } catch {
          }
        }
        let relativePath;
        const name = entry.name;
        const displayPrefix = rawPrefix;
        if (displayPrefix.endsWith("/")) {
          relativePath = displayPrefix + name;
        } else if (displayPrefix.includes("/") || displayPrefix.includes("\\")) {
          if (displayPrefix.startsWith("~/")) {
            const homeRelativeDir = displayPrefix.slice(2);
            const dir = dirname(homeRelativeDir);
            relativePath = `~/${dir === "." ? name : join(dir, name)}`;
          } else if (displayPrefix.startsWith("/")) {
            const dir = dirname(displayPrefix);
            if (dir === "/") {
              relativePath = `/${name}`;
            } else {
              relativePath = `${dir}/${name}`;
            }
          } else {
            relativePath = join(dirname(displayPrefix), name);
            if (displayPrefix.startsWith("./") && !relativePath.startsWith("./")) {
              relativePath = `./${relativePath}`;
            }
          }
        } else {
          if (displayPrefix.startsWith("~")) {
            relativePath = `~/${name}`;
          } else {
            relativePath = name;
          }
        }
        relativePath = toDisplayPath(relativePath);
        const pathValue = isDirectory ? `${relativePath}/` : relativePath;
        const value = buildCompletionValue(pathValue, {
          isDirectory,
          isAtPrefix,
          isQuotedPrefix
        });
        suggestions.push({
          value,
          label: name + (isDirectory ? "/" : "")
        });
      }
      suggestions.sort((a, b) => {
        const aIsDir = a.label.endsWith("/");
        const bIsDir = b.label.endsWith("/");
        if (aIsDir && !bIsDir)
          return -1;
        if (!aIsDir && bIsDir)
          return 1;
        return a.label.localeCompare(b.label);
      });
      return suggestions;
    } catch (_e) {
      return [];
    }
  }
  // Score an entry against the query (higher = better match)
  // isDirectory adds bonus to prioritize folders
  scoreEntry(filePath, query, isDirectory) {
    const fileName = basename(filePath);
    const lowerFileName = fileName.toLowerCase();
    const lowerQuery = query.toLowerCase();
    let score = 0;
    if (lowerFileName === lowerQuery)
      score = 100;
    else if (lowerFileName.startsWith(lowerQuery))
      score = 80;
    else if (lowerFileName.includes(lowerQuery))
      score = 50;
    else if (filePath.toLowerCase().includes(lowerQuery))
      score = 30;
    if (isDirectory && score > 0)
      score += 10;
    return score;
  }
  async getBaseDirSuggestions(baseDir, query, signal) {
    if (!this.fdPath || signal.aborted) {
      return [];
    }
    return await walkDirectoryWithFd(baseDir, this.fdPath, query, 100, signal, 1);
  }
  // Fuzzy file search using fd (fast, respects .gitignore)
  async getFuzzyFileSuggestions(query, options) {
    if (!this.fdPath || options.signal.aborted) {
      return [];
    }
    try {
      const scopedQuery = this.resolveScopedFuzzyQuery(query);
      const fdBaseDir = scopedQuery?.baseDir ?? this.basePath;
      const fdQuery = scopedQuery?.query ?? query;
      const baseDirEntries = await this.getBaseDirSuggestions(fdBaseDir, fdQuery, options.signal);
      const recursiveEntries = await walkDirectoryWithFd(fdBaseDir, this.fdPath, fdQuery, 100, options.signal);
      const seenPaths = new Set(baseDirEntries.map((entry) => entry.path));
      const entries = [
        ...baseDirEntries,
        ...recursiveEntries.filter((entry) => {
          if (seenPaths.has(entry.path))
            return false;
          seenPaths.add(entry.path);
          return true;
        })
      ];
      if (options.signal.aborted) {
        return [];
      }
      const scoredEntries = entries.map((entry) => ({
        ...entry,
        score: fdQuery ? this.scoreEntry(entry.path, fdQuery, entry.isDirectory) : 1
      })).filter((entry) => entry.score > 0);
      scoredEntries.sort((a, b) => {
        const scoreDiff = b.score - a.score;
        if (scoreDiff !== 0)
          return scoreDiff;
        const aDepth = toDisplayPath(a.path).split("/").filter(Boolean).length;
        const bDepth = toDisplayPath(b.path).split("/").filter(Boolean).length;
        const depthDiff = aDepth - bDepth;
        if (depthDiff !== 0)
          return depthDiff;
        const lengthDiff = a.path.length - b.path.length;
        if (lengthDiff !== 0)
          return lengthDiff;
        return a.path.localeCompare(b.path);
      });
      const topEntries = scoredEntries.slice(0, 20);
      const suggestions = [];
      for (const { path: entryPath, isDirectory } of topEntries) {
        const pathWithoutSlash = isDirectory ? entryPath.slice(0, -1) : entryPath;
        const displayPath = scopedQuery ? this.scopedPathForDisplay(scopedQuery.displayBase, pathWithoutSlash) : pathWithoutSlash;
        const entryName = basename(pathWithoutSlash);
        const completionPath = isDirectory ? `${displayPath}/` : displayPath;
        const value = buildCompletionValue(completionPath, {
          isDirectory,
          isAtPrefix: true,
          isQuotedPrefix: options.isQuotedPrefix
        });
        suggestions.push({
          value,
          label: entryName + (isDirectory ? "/" : ""),
          description: displayPath
        });
      }
      return suggestions;
    } catch {
      return [];
    }
  }
  // Check if we should trigger file completion (called on Tab key)
  shouldTriggerFileCompletion(lines, cursorLine, cursorCol) {
    const currentLine = lines[cursorLine] || "";
    const textBeforeCursor = currentLine.slice(0, cursorCol);
    if (textBeforeCursor.trim().startsWith("/") && !textBeforeCursor.trim().includes(" ")) {
      return false;
    }
    return true;
  }
};

// pi-dist/pi-tui/oklab.js
var multiply = /* @__PURE__ */ __name((m, [x, y, z]) => m.map((row) => row[0] * x + row[1] * y + row[2] * z), "multiply");
var LINEAR_SRGB_TO_LMS = [
  [0.4122214694707629, 0.5363325372617349, 0.0514459932675022],
  [0.2119034958178251, 0.6806995506452344, 0.1073969535369405],
  [0.0883024591900564, 0.2817188391361215, 0.6299787016738222]
];
var LMS_TO_LAB = [
  [0.210454268309314, 0.793617774702305, -0.0040720430116193],
  [1.9779985324311684, -2.42859224204858, 0.450593709617411],
  [0.0259040424655478, 0.7827717124575296, -0.8086757549230774]
];
var LAB_TO_LMS = [
  [1, 0.3963377773761749, 0.2158037573099136],
  [1, -0.1055613458156586, -0.0638541728258133],
  [1, -0.0894841775298119, -1.2914855480194092]
];
var LMS_TO_LINEAR_SRGB = [
  [4.076741636075958, -3.307711539258063, 0.2309699031821043],
  [-1.2684379732850315, 2.609757349287688, -0.341319376002657],
  [-0.0041960761386756, -0.7034186179359362, 1.7076146940746117]
];
var SATURATION_FIT = [
  [
    [-1.8817031, -0.80936501],
    [1.19086277, 1.76576728, 0.59662641, 0.75515197, 0.56771245]
  ],
  [
    [1.8144408, -1.19445267],
    [0.73956515, -0.45954404, 0.08285427, 0.12541073, -0.14503204]
  ],
  [
    [0.13110758, 1.81333971],
    [1.35733652, -915799e-8, -1.1513021, -0.50559606, 692167e-8]
  ]
];
var K1 = 0.206;
var K2 = 0.03;
var K3 = (1 + K1) / (1 + K2);
var oklabToOkhslLightness = /* @__PURE__ */ __name((x) => 0.5 * (K3 * x - K1 + Math.sqrt((K3 * x - K1) ** 2 + 4 * K2 * K3 * x)), "oklabToOkhslLightness");
var okhslToOklabLightness = /* @__PURE__ */ __name((x) => (x * x + K1 * x) / (K3 * (x + K2)), "okhslToOklabLightness");
var linearToSrgb = /* @__PURE__ */ __name((value) => value > 31308e-7 ? 1.055 * value ** (1 / 2.4) - 0.055 : 12.92 * value, "linearToSrgb");
var srgbToLinear = /* @__PURE__ */ __name((value) => value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4, "srgbToLinear");
function oklabToLinearSrgb(lab) {
  return multiply(LMS_TO_LINEAR_SRGB, multiply(LAB_TO_LMS, lab).map((value) => value ** 3));
}
__name(oklabToLinearSrgb, "oklabToLinearSrgb");
function linearSrgbToOklab(rgb) {
  return multiply(LMS_TO_LAB, multiply(LINEAR_SRGB_TO_LMS, rgb).map(Math.cbrt));
}
__name(linearSrgbToOklab, "linearSrgbToOklab");
function rgbToOklab({ r, g, b }) {
  return linearSrgbToOklab([r / 255, g / 255, b / 255].map(srgbToLinear));
}
__name(rgbToOklab, "rgbToOklab");
function linearSrgbToRgb(linear) {
  const [r, g, b] = linear.map((value) => Math.round(Math.min(1, Math.max(0, linearToSrgb(value))) * 255));
  return { r, g, b };
}
__name(linearSrgbToRgb, "linearSrgbToRgb");
function lmsSlopes(a, b) {
  return [LAB_TO_LMS[0], LAB_TO_LMS[1], LAB_TO_LMS[2]].map((row) => row[1] * a + row[2] * b);
}
__name(lmsSlopes, "lmsSlopes");
function maxSaturation(a, b) {
  const channel = SATURATION_FIT.findIndex(([[x, y]], index) => index === 2 || x * a + y * b > 1);
  const [k0, k1, k2, k3, k4] = SATURATION_FIT[channel][1];
  const weights = LMS_TO_LINEAR_SRGB[channel];
  const saturation = k0 + k1 * a + k2 * b + k3 * a * a + k4 * a * b;
  const slopes = lmsSlopes(a, b);
  const base = slopes.map((k) => 1 + saturation * k);
  const dot = /* @__PURE__ */ __name((values) => values.reduce((sum, value, index) => sum + weights[index] * value, 0), "dot");
  const f = dot(base.map((value) => value ** 3));
  const f1 = dot(base.map((value, index) => 3 * slopes[index] * value ** 2));
  const f2 = dot(base.map((value, index) => 6 * slopes[index] ** 2 * value));
  return saturation - f * f1 / (f1 * f1 - 0.5 * f * f2);
}
__name(maxSaturation, "maxSaturation");
function cusp(a, b) {
  const saturation = maxSaturation(a, b);
  const lightness = Math.cbrt(1 / Math.max(...oklabToLinearSrgb([1, saturation * a, saturation * b])));
  return [lightness, lightness * saturation];
}
__name(cusp, "cusp");
function maxChroma(a, b, lightness, [cuspL, cuspC]) {
  if (lightness <= cuspL)
    return cuspC * lightness / cuspL;
  const t = cuspC * (lightness - 1) / (cuspL - 1);
  const slopes = lmsSlopes(a, b);
  const lms = slopes.map((k) => lightness + t * k);
  const cubes = lms.map((value) => value ** 3);
  const first = lms.map((value, index) => 3 * slopes[index] * value ** 2);
  const second = lms.map((value, index) => 6 * slopes[index] ** 2 * value);
  const dot = /* @__PURE__ */ __name((row, values) => row[0] * values[0] + row[1] * values[1] + row[2] * values[2], "dot");
  const steps = LMS_TO_LINEAR_SRGB.map((row) => {
    const f = dot(row, cubes) - 1;
    const f1 = dot(row, first);
    const f2 = dot(row, second);
    const u = f1 / (f1 * f1 - 0.5 * f * f2);
    return u >= 0 ? -f * u : Number.MAX_VALUE;
  });
  return t + Math.min(...steps);
}
__name(maxChroma, "maxChroma");
function chromaStops(L, a, b) {
  const peak = cusp(a, b);
  const cMax = maxChroma(a, b, L, peak);
  const k = cMax / Math.min(L * (peak[1] / peak[0]), (1 - L) * (peak[1] / (1 - peak[0])));
  const midS = 0.11516993 + 1 / (7.4477897 + 4.1590124 * b + a * (-2.19557347 + 1.75198401 * b + a * (-2.13704948 - 10.02301043 * b + a * (-4.24894561 + 5.38770819 * b + 4.69891013 * a))));
  const midT = 0.11239642 + 1 / (1.6132032 - 0.68124379 * b + a * (0.40370612 + 0.90148123 * b + a * (-0.27087943 + 0.6122399 * b + a * (299215e-8 - 0.45399568 * b - 0.14661872 * a))));
  const cMid = 0.9 * k * Math.sqrt(Math.sqrt(1 / (1 / (L * midS) ** 4 + 1 / ((1 - L) * midT) ** 4)));
  const c0 = Math.sqrt(1 / (1 / (L * 0.4) ** 2 + 1 / ((1 - L) * 0.8) ** 2));
  return [c0, cMid, cMax];
}
__name(chromaStops, "chromaStops");
function okhslToRgb(hue, saturation, lightness) {
  const L = okhslToOklabLightness(lightness);
  let lab = [L, 0, 0];
  if (L > 0 && L < 1 && saturation > 0) {
    const angle = 2 * Math.PI * ((hue % 360 + 360) % 360) / 360;
    const a = Math.cos(angle);
    const b = Math.sin(angle);
    const [c0, cMid, cMax] = chromaStops(L, a, b);
    let chroma;
    if (saturation < 0.8) {
      const t = 1.25 * saturation;
      const k1 = 0.8 * c0;
      chroma = t * k1 / (1 - (1 - k1 / cMid) * t);
    } else {
      const t = 5 * (saturation - 0.8);
      const k1 = 0.2 * cMid ** 2 * 1.25 ** 2 / c0;
      chroma = cMid + t * k1 / (1 - (1 - k1 / (cMax - cMid)) * t);
    }
    lab = [L, chroma * a, chroma * b];
  }
  return linearSrgbToRgb(oklabToLinearSrgb(lab));
}
__name(okhslToRgb, "okhslToRgb");
function rgbToOkhsl(rgb) {
  const [L, labA, labB] = rgbToOklab(rgb);
  const chroma = Math.hypot(labA, labB);
  const lightness = oklabToOkhslLightness(L);
  if (chroma < 1e-9 || lightness <= 0 || lightness >= 1)
    return { h: 0, s: 0, l: lightness };
  const hue = (Math.atan2(labB, labA) * 180 / Math.PI + 360) % 360;
  const [c0, cMid, cMax] = chromaStops(L, labA / chroma, labB / chroma);
  let saturation;
  if (chroma < cMid) {
    const k1 = 0.8 * c0;
    saturation = 0.8 * (chroma / (k1 + (1 - k1 / cMid) * chroma));
  } else {
    const k1 = 0.2 * cMid ** 2 * 1.25 ** 2 / c0;
    const offset = chroma - cMid;
    saturation = 0.8 + 0.2 * (offset / (k1 + (1 - k1 / (cMax - cMid)) * offset));
  }
  return { h: hue, s: Math.min(1, Math.max(0, saturation)), l: lightness };
}
__name(rgbToOkhsl, "rgbToOkhsl");

// pi-dist/pi-tui/colors.js
function requireFinite(value, name) {
  if (!Number.isFinite(value))
    throw new Error(`${name} must be finite`);
}
__name(requireFinite, "requireFinite");
function indexedColor(index) {
  if (!Number.isInteger(index) || index < 0 || index > 255) {
    throw new Error(`ANSI color index must be an integer from 0 to 255: ${index}`);
  }
  return Object.freeze({ kind: "indexed", index });
}
__name(indexedColor, "indexedColor");
function rgbColor(r, g, b) {
  for (const [name, value] of [
    ["r", r],
    ["g", g],
    ["b", b]
  ]) {
    requireFinite(value, name);
    if (value < 0 || value > 255)
      throw new Error(`${name} must be between 0 and 255: ${value}`);
  }
  return Object.freeze({ kind: "rgb", r, g, b });
}
__name(rgbColor, "rgbColor");
function oklchColor(l, c, h) {
  requireFinite(l, "l");
  requireFinite(c, "c");
  requireFinite(h, "h");
  if (l < 0 || l > 1)
    throw new Error(`l must be between 0 and 1: ${l}`);
  if (c < 0)
    throw new Error(`c must not be negative: ${c}`);
  return Object.freeze({ kind: "oklch", l, c, h: (h % 360 + 360) % 360 });
}
__name(oklchColor, "oklchColor");
var NUMBER_PATTERN = String.raw`[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:e[+-]?\d+)?`;
var OKLCH_PATTERN = new RegExp(`^oklch\\(\\s*(${NUMBER_PATTERN})(%)?\\s+(${NUMBER_PATTERN})\\s+(${NUMBER_PATTERN})(?:deg)?\\s*\\)$`, "i");
var OKHSL_PATTERN = new RegExp(`^okhsl\\(\\s*(${NUMBER_PATTERN})(?:deg)?\\s+(${NUMBER_PATTERN})(%)?\\s+(${NUMBER_PATTERN})(%)?\\s*\\)$`, "i");
function okhslColor(h, s, l) {
  requireFinite(h, "h");
  requireFinite(s, "s");
  requireFinite(l, "l");
  if (s < 0 || s > 1)
    throw new Error(`s must be between 0 and 1: ${s}`);
  if (l < 0 || l > 1)
    throw new Error(`l must be between 0 and 1: ${l}`);
  const { r, g, b } = okhslToRgb(h, s, l);
  return rgbColor(r, g, b);
}
__name(okhslColor, "okhslColor");
function colorToOkhsl(color) {
  return rgbToOkhsl(colorToRgb(color));
}
__name(colorToOkhsl, "colorToOkhsl");
function parseColor(value) {
  if (typeof value === "number")
    return indexedColor(value);
  const hex = /^#([\da-f]{3}|[\da-f]{6})$/i.exec(value);
  if (hex) {
    const digits = hex[1].length === 3 ? [...hex[1]].map((digit) => digit + digit).join("") : hex[1];
    return rgbColor(Number.parseInt(digits.slice(0, 2), 16), Number.parseInt(digits.slice(2, 4), 16), Number.parseInt(digits.slice(4, 6), 16));
  }
  const oklch = OKLCH_PATTERN.exec(value);
  if (oklch) {
    const lightness = Number.parseFloat(oklch[1]) / (oklch[2] ? 100 : 1);
    return oklchColor(lightness, Number.parseFloat(oklch[3]), Number.parseFloat(oklch[4]));
  }
  const okhsl = OKHSL_PATTERN.exec(value);
  if (okhsl) {
    const saturation = Number.parseFloat(okhsl[2]) / (okhsl[3] ? 100 : 1);
    const lightness = Number.parseFloat(okhsl[4]) / (okhsl[5] ? 100 : 1);
    return okhslColor(Number.parseFloat(okhsl[1]), saturation, lightness);
  }
  throw new Error(`Invalid color value: ${value}`);
}
__name(parseColor, "parseColor");
var BASIC_COLORS = [
  { r: 0, g: 0, b: 0 },
  { r: 128, g: 0, b: 0 },
  { r: 0, g: 128, b: 0 },
  { r: 128, g: 128, b: 0 },
  { r: 0, g: 0, b: 128 },
  { r: 128, g: 0, b: 128 },
  { r: 0, g: 128, b: 128 },
  { r: 192, g: 192, b: 192 },
  { r: 128, g: 128, b: 128 },
  { r: 255, g: 0, b: 0 },
  { r: 0, g: 255, b: 0 },
  { r: 255, g: 255, b: 0 },
  { r: 0, g: 0, b: 255 },
  { r: 255, g: 0, b: 255 },
  { r: 0, g: 255, b: 255 },
  { r: 255, g: 255, b: 255 }
];
var CUBE_VALUES = [0, 95, 135, 175, 215, 255];
var GRAY_VALUES = Array.from({ length: 24 }, (_, index) => 8 + index * 10);
function indexedToRgb(index) {
  if (index < 16)
    return { ...BASIC_COLORS[index] };
  if (index < 232) {
    const cubeIndex = index - 16;
    return {
      r: CUBE_VALUES[Math.floor(cubeIndex / 36)],
      g: CUBE_VALUES[Math.floor(cubeIndex % 36 / 6)],
      b: CUBE_VALUES[cubeIndex % 6]
    };
  }
  const gray = 8 + (index - 232) * 10;
  return { r: gray, g: gray, b: gray };
}
__name(indexedToRgb, "indexedToRgb");
function isInSrgbGamut(linear) {
  const epsilon = 1e-7;
  return linear.every((channel) => channel >= -epsilon && channel <= 1 + epsilon);
}
__name(isInSrgbGamut, "isInSrgbGamut");
function oklchToRgb({ l, c, h }) {
  const radians = h * Math.PI / 180;
  const cos = Math.cos(radians);
  const sin = Math.sin(radians);
  const atChroma = /* @__PURE__ */ __name((chroma) => oklabToLinearSrgb([l, chroma * cos, chroma * sin]), "atChroma");
  const direct = atChroma(c);
  if (isInSrgbGamut(direct))
    return linearSrgbToRgb(direct);
  let linear = atChroma(0);
  let low = 0;
  let high = c;
  for (let index = 0; index < 20; index++) {
    const chroma = (low + high) / 2;
    const candidate = atChroma(chroma);
    if (isInSrgbGamut(candidate)) {
      low = chroma;
      linear = candidate;
    } else {
      high = chroma;
    }
  }
  return linearSrgbToRgb(linear);
}
__name(oklchToRgb, "oklchToRgb");
function colorToRgb(color) {
  switch (color.kind) {
    case "indexed":
      return indexedToRgb(color.index);
    case "rgb":
      return { r: color.r, g: color.g, b: color.b };
    case "oklch":
      return oklchToRgb(color);
  }
}
__name(colorToRgb, "colorToRgb");
function colorToOklch(color) {
  if (color.kind === "oklch")
    return { l: color.l, c: color.c, h: color.h };
  const [l, a, b] = rgbToOklab(colorToRgb(color));
  return { l, c: Math.hypot(a, b), h: (Math.atan2(b, a) * 180 / Math.PI + 360) % 360 };
}
__name(colorToOklch, "colorToOklch");
function colorToHex(color) {
  const { r, g, b } = colorToRgb(color);
  const channel = /* @__PURE__ */ __name((value) => Math.round(value).toString(16).padStart(2, "0"), "channel");
  return `#${channel(r)}${channel(g)}${channel(b)}`;
}
__name(colorToHex, "colorToHex");
function mixColors(first, second, amount, space = "oklch") {
  requireFinite(amount, "amount");
  if (amount < 0 || amount > 1)
    throw new Error(`amount must be between 0 and 1: ${amount}`);
  if (space === "srgb") {
    const a2 = colorToRgb(first);
    const b2 = colorToRgb(second);
    return rgbColor(a2.r + (b2.r - a2.r) * amount, a2.g + (b2.g - a2.g) * amount, a2.b + (b2.b - a2.b) * amount);
  }
  const a = colorToOklch(first);
  const b = colorToOklch(second);
  const firstHue = a.c < 1e-7 ? b.h : a.h;
  const secondHue = b.c < 1e-7 ? firstHue : b.h;
  const hueDelta = (secondHue - firstHue + 540) % 360 - 180;
  return oklchColor(a.l + (b.l - a.l) * amount, a.c + (b.c - a.c) * amount, firstHue + hueDelta * amount);
}
__name(mixColors, "mixColors");
function findClosest(values, target) {
  let closestIndex = 0;
  let closestDistance = Infinity;
  for (let index = 0; index < values.length; index++) {
    const distance = Math.abs(target - values[index]);
    if (distance < closestDistance) {
      closestIndex = index;
      closestDistance = distance;
    }
  }
  return closestIndex;
}
__name(findClosest, "findClosest");
function colorDistance(first, second) {
  const dr = first.r - second.r;
  const dg = first.g - second.g;
  const db = first.b - second.b;
  return dr * dr * 0.299 + dg * dg * 0.587 + db * db * 0.114;
}
__name(colorDistance, "colorDistance");
function rgbToAnsi256(color) {
  const rIndex = findClosest(CUBE_VALUES, color.r);
  const gIndex = findClosest(CUBE_VALUES, color.g);
  const bIndex = findClosest(CUBE_VALUES, color.b);
  const cubeColor = { r: CUBE_VALUES[rIndex], g: CUBE_VALUES[gIndex], b: CUBE_VALUES[bIndex] };
  const cubeIndex = 16 + 36 * rIndex + 6 * gIndex + bIndex;
  const gray = Math.round(0.299 * color.r + 0.587 * color.g + 0.114 * color.b);
  const grayOffset = findClosest(GRAY_VALUES, gray);
  const grayValue = GRAY_VALUES[grayOffset];
  const spread = Math.max(color.r, color.g, color.b) - Math.min(color.r, color.g, color.b);
  if (spread < 10 && colorDistance(color, { r: grayValue, g: grayValue, b: grayValue }) < colorDistance(color, cubeColor)) {
    return 232 + grayOffset;
  }
  return cubeIndex;
}
__name(rgbToAnsi256, "rgbToAnsi256");
function colorAnsi(color, mode, background) {
  if (color.kind === "indexed")
    return `\x1B[${background ? 48 : 38};5;${color.index}m`;
  const rgb = colorToRgb(color);
  if (mode === "truecolor") {
    return `\x1B[${background ? 48 : 38};2;${Math.round(rgb.r)};${Math.round(rgb.g)};${Math.round(rgb.b)}m`;
  }
  return `\x1B[${background ? 48 : 38};5;${rgbToAnsi256(rgb)}m`;
}
__name(colorAnsi, "colorAnsi");
function foregroundAnsi(color, mode) {
  return colorAnsi(color, mode, false);
}
__name(foregroundAnsi, "foregroundAnsi");
function backgroundAnsi(color, mode) {
  return colorAnsi(color, mode, true);
}
__name(backgroundAnsi, "backgroundAnsi");
function styleText(text, options, mode) {
  return styleTextWithAnsi(text, options.fg && foregroundAnsi(options.fg, mode), options.bg && backgroundAnsi(options.bg, mode), options);
}
__name(styleText, "styleText");
function styleTextWithAnsi(text, fgAnsi, bgAnsi, options) {
  let prefix = "";
  let suffix = "";
  if (fgAnsi) {
    prefix += fgAnsi;
    suffix = "\x1B[39m";
  }
  if (bgAnsi) {
    prefix += bgAnsi;
    suffix = `\x1B[49m${suffix}`;
  }
  if (options.bold)
    prefix += "\x1B[1m";
  if (options.dim)
    prefix += "\x1B[2m";
  if (options.bold || options.dim)
    suffix = `\x1B[22m${suffix}`;
  if (options.italic) {
    prefix += "\x1B[3m";
    suffix = `\x1B[23m${suffix}`;
  }
  if (options.underline) {
    prefix += "\x1B[4m";
    suffix = `\x1B[24m${suffix}`;
  }
  if (options.inverse) {
    prefix += "\x1B[7m";
    suffix = `\x1B[27m${suffix}`;
  }
  if (options.strikethrough) {
    prefix += "\x1B[9m";
    suffix = `\x1B[29m${suffix}`;
  }
  return `${prefix}${text}${suffix}`;
}
__name(styleTextWithAnsi, "styleTextWithAnsi");

// pi-dist/pi-tui/components/box.js
import { dispatchMouseEvent } from "../tui.js";
import { flattenLines, visibleWidth } from "../utils.js";
var Box = class {
  static {
    __name(this, "Box");
  }
  children = [];
  paddingX;
  paddingY;
  bgFn;
  // Cache for rendered output
  cache;
  mouseLayout;
  constructor(paddingX = 1, paddingY = 1, bgFn) {
    this.paddingX = paddingX;
    this.paddingY = paddingY;
    this.bgFn = bgFn;
  }
  addChild(component) {
    this.children.push(component);
    this.invalidateCache();
  }
  removeChild(component) {
    const index = this.children.indexOf(component);
    if (index !== -1) {
      this.children.splice(index, 1);
      this.invalidateCache();
    }
  }
  clear() {
    this.children = [];
    this.invalidateCache();
  }
  setBgFn(bgFn) {
    this.bgFn = bgFn;
  }
  invalidateCache() {
    this.cache = void 0;
  }
  matchCache(width, childLines, bgSample) {
    const cache = this.cache;
    return !!cache && cache.width === width && cache.bgSample === bgSample && cache.childLines.length === childLines.length && cache.childLines.every((line, i) => line === childLines[i]);
  }
  invalidate() {
    this.invalidateCache();
    for (const child of this.children) {
      child.invalidate?.();
    }
  }
  handleMouse(event) {
    const contentWidth = Math.max(1, event.width - this.paddingX * 2);
    const contentY = event.y - this.paddingY;
    const contentX = event.x - this.paddingX;
    if (contentY < 0 || contentX < 0 || contentX >= contentWidth)
      return void 0;
    const mouseChildren = this.mouseLayout?.width === contentWidth ? this.mouseLayout.children : this.children.map((component) => ({ component, height: component.render(contentWidth).length }));
    let childY = 0;
    for (const { component: child, height: childHeight } of mouseChildren) {
      if (contentY >= childY && contentY < childY + childHeight) {
        return dispatchMouseEvent(child, {
          ...event,
          x: contentX,
          y: contentY - childY,
          width: contentWidth,
          height: childHeight
        });
      }
      childY += childHeight;
    }
    return void 0;
  }
  render(width) {
    if (this.children.length === 0) {
      return [];
    }
    const contentWidth = Math.max(1, width - this.paddingX * 2);
    const leftPad = " ".repeat(this.paddingX);
    const childLines = [];
    const mouseChildren = [];
    for (const child of this.children) {
      const lines = child.render(contentWidth);
      mouseChildren.push({ component: child, height: lines.length });
      for (const line of lines) {
        childLines.push(line);
      }
    }
    this.mouseLayout = { width: contentWidth, children: mouseChildren };
    if (childLines.length === 0) {
      return [];
    }
    const bgSample = this.bgFn ? this.bgFn("test") : void 0;
    if (this.matchCache(width, childLines, bgSample)) {
      return this.cache.lines;
    }
    const result = [];
    for (let i = 0; i < this.paddingY; i++) {
      result.push(this.applyBg("", width));
    }
    for (const line of childLines) {
      result.push(this.applyBg(leftPad + line, width));
    }
    for (let i = 0; i < this.paddingY; i++) {
      result.push(this.applyBg("", width));
    }
    flattenLines(result);
    this.cache = { childLines, width, bgSample, lines: result };
    return result;
  }
  applyBg(line, width) {
    const visLen = visibleWidth(line);
    const padNeeded = Math.max(0, width - visLen);
    const padded = line + " ".repeat(padNeeded);
    return this.bgFn ? this.bgFn(padded) : padded;
  }
};

// pi-dist/pi-tui/components/cancellable-loader.js
import { getKeybindings } from "../keybindings.js";

// pi-dist/pi-tui/components/text.js
import { applyBackgroundToLine, flattenLines as flattenLines2, visibleWidth as visibleWidth2, wrapTextWithAnsi } from "../utils.js";
var Text = class {
  static {
    __name(this, "Text");
  }
  text;
  paddingX;
  // Left/right padding
  paddingY;
  // Top/bottom padding
  customBgFn;
  // Cache for rendered output
  cachedText;
  cachedWidth;
  cachedLines;
  constructor(text = "", paddingX = 1, paddingY = 1, customBgFn) {
    this.text = text;
    this.paddingX = paddingX;
    this.paddingY = paddingY;
    this.customBgFn = customBgFn;
  }
  setText(text) {
    this.text = text;
    this.cachedText = void 0;
    this.cachedWidth = void 0;
    this.cachedLines = void 0;
  }
  setCustomBgFn(customBgFn) {
    this.customBgFn = customBgFn;
    this.cachedText = void 0;
    this.cachedWidth = void 0;
    this.cachedLines = void 0;
  }
  invalidate() {
    this.cachedText = void 0;
    this.cachedWidth = void 0;
    this.cachedLines = void 0;
  }
  render(width) {
    if (this.cachedLines && this.cachedText === this.text && this.cachedWidth === width) {
      return this.cachedLines;
    }
    if (!this.text || this.text.trim() === "") {
      const result2 = [];
      this.cachedText = this.text;
      this.cachedWidth = width;
      this.cachedLines = result2;
      return result2;
    }
    const normalizedText = this.text.replace(/\t/g, "   ");
    const paddingX = Math.min(this.paddingX, Math.max(0, Math.floor((width - 1) / 2)));
    const contentWidth = Math.max(1, width - paddingX * 2);
    const wrappedLines = wrapTextWithAnsi(normalizedText, contentWidth);
    const leftMargin = " ".repeat(paddingX);
    const rightMargin = " ".repeat(paddingX);
    const contentLines = [];
    for (const line of wrappedLines) {
      const lineWithMargins = leftMargin + line + rightMargin;
      if (this.customBgFn) {
        contentLines.push(applyBackgroundToLine(lineWithMargins, width, this.customBgFn));
      } else {
        const visibleLen = visibleWidth2(lineWithMargins);
        const paddingNeeded = Math.max(0, width - visibleLen);
        contentLines.push(lineWithMargins + " ".repeat(paddingNeeded));
      }
    }
    const emptyLine = " ".repeat(width);
    const emptyLines = [];
    for (let i = 0; i < this.paddingY; i++) {
      const line = this.customBgFn ? applyBackgroundToLine(emptyLine, width, this.customBgFn) : emptyLine;
      emptyLines.push(line);
    }
    const result = [...emptyLines, ...contentLines, ...emptyLines];
    flattenLines2(result);
    this.cachedText = this.text;
    this.cachedWidth = width;
    this.cachedLines = result;
    return result.length > 0 ? result : [""];
  }
};

// pi-dist/pi-tui/components/loader.js
var DEFAULT_FRAMES = ["\u280B", "\u2819", "\u2839", "\u2838", "\u283C", "\u2834", "\u2826", "\u2827", "\u2807", "\u280F"];
var DEFAULT_INTERVAL_MS = 80;
var Loader = class extends Text {
  static {
    __name(this, "Loader");
  }
  frames = [...DEFAULT_FRAMES];
  intervalMs = DEFAULT_INTERVAL_MS;
  currentFrame = 0;
  intervalId = null;
  ui = null;
  renderIndicatorVerbatim = false;
  spinnerColorFn;
  messageColorFn;
  message = "Loading...";
  constructor(ui, spinnerColorFn, messageColorFn, message = "Loading...", indicator) {
    super("", 1, 0);
    this.ui = ui;
    this.spinnerColorFn = spinnerColorFn;
    this.messageColorFn = messageColorFn;
    this.message = message;
    this.setIndicator(indicator);
  }
  render(width) {
    return ["", ...super.render(width)];
  }
  start() {
    this.updateDisplay();
    this.restartAnimation();
  }
  stop() {
    if (this.intervalId) {
      clearInterval(this.intervalId);
      this.intervalId = null;
    }
  }
  setMessage(message) {
    this.message = message;
    this.updateDisplay();
  }
  invalidate() {
    super.invalidate();
    this.updateDisplay();
  }
  setIndicator(indicator) {
    this.renderIndicatorVerbatim = indicator !== void 0;
    this.frames = indicator?.frames !== void 0 ? [...indicator.frames] : [...DEFAULT_FRAMES];
    this.intervalMs = indicator?.intervalMs && indicator.intervalMs > 0 ? indicator.intervalMs : DEFAULT_INTERVAL_MS;
    this.currentFrame = 0;
    this.start();
  }
  restartAnimation() {
    this.stop();
    if (this.frames.length <= 1) {
      return;
    }
    this.intervalId = setInterval(() => {
      this.currentFrame = (this.currentFrame + 1) % this.frames.length;
      this.updateDisplay();
    }, this.intervalMs);
  }
  getRenderedIndicator() {
    const frame = this.frames[this.currentFrame] ?? "";
    return this.renderIndicatorVerbatim ? frame : this.spinnerColorFn(frame);
  }
  updateDisplay() {
    const renderedFrame = this.getRenderedIndicator();
    const indicator = renderedFrame.length > 0 ? `${renderedFrame} ` : "";
    this.setText(`${indicator}${this.messageColorFn(this.message)}`);
    if (this.ui) {
      this.ui.requestRender();
    }
  }
};

// pi-dist/pi-tui/components/cancellable-loader.js
var CancellableLoader = class extends Loader {
  static {
    __name(this, "CancellableLoader");
  }
  abortController = new AbortController();
  /** Called when user presses Escape */
  onAbort;
  /** AbortSignal that is aborted when user presses Escape */
  get signal() {
    return this.abortController.signal;
  }
  /** Whether the loader was aborted */
  get aborted() {
    return this.abortController.signal.aborted;
  }
  handleInput(data) {
    const kb = getKeybindings();
    if (kb.matches(data, "tui.select.cancel")) {
      this.abortController.abort();
      this.onAbort?.();
    }
  }
  dispose() {
    this.stop();
  }
};

// pi-dist/pi-tui/components/editor.js
import { getKeybindings as getKeybindings3 } from "../keybindings.js";
import { decodePrintableKey, matchesKey } from "../keys.js";

// pi-dist/pi-tui/kill-ring.js
var KillRing = class {
  static {
    __name(this, "KillRing");
  }
  ring = [];
  /**
   * Add text to the kill ring.
   *
   * @param text - The killed text to add
   * @param opts - Push options
   * @param opts.prepend - If accumulating, prepend (backward deletion) or append (forward deletion)
   * @param opts.accumulate - Merge with the most recent entry instead of creating a new one
   */
  push(text, opts) {
    if (!text)
      return;
    if (opts.accumulate && this.ring.length > 0) {
      const last = this.ring.pop();
      this.ring.push(opts.prepend ? text + last : last + text);
    } else {
      this.ring.push(text);
    }
  }
  /** Get most recent entry without modifying the ring. */
  peek() {
    return this.ring.length > 0 ? this.ring[this.ring.length - 1] : void 0;
  }
  /** Move last entry to front (for yank-pop cycling). */
  rotate() {
    if (this.ring.length > 1) {
      const last = this.ring.pop();
      this.ring.unshift(last);
    }
  }
  get length() {
    return this.ring.length;
  }
};

// pi-dist/pi-tui/components/editor.js
import { CURSOR_MARKER } from "../tui.js";

// pi-dist/pi-tui/undo-stack.js
var UndoStack = class {
  static {
    __name(this, "UndoStack");
  }
  stack = [];
  /** Push a deep clone of the given state onto the stack. */
  push(state) {
    this.stack.push(structuredClone(state));
  }
  /** Pop and return the most recent snapshot, or undefined if empty. */
  pop() {
    return this.stack.pop();
  }
  /** Remove all snapshots. */
  clear() {
    this.stack.length = 0;
  }
  get length() {
    return this.stack.length;
  }
};

// pi-dist/pi-tui/components/editor.js
import { autocompleteBoundaryRegex as autocompleteBoundaryRegex2, autocompleteSeparatorRegex as autocompleteSeparatorRegex2, cjkBreakRegex, getGraphemeSegmenter, getWordSegmenter as getWordSegmenter2, isWhitespaceChar as isWhitespaceChar2, sliceByColumn, visibleWidth as visibleWidth4 } from "../utils.js";

// pi-dist/pi-tui/word-navigation.js
import { getWordSegmenter, isWhitespaceChar, PUNCTUATION_REGEX } from "../utils.js";
import { wordSegmenter } from "../../../pi-tui-segmenters.mjs";
function findWordBackward(text, cursor, options) {
  if (cursor <= 0)
    return 0;
  const textBeforeCursor = text.slice(0, cursor);
  const segmentFn = options?.segment;
  const isAtomic = options?.isAtomicSegment;
  const segments = segmentFn ? [...segmentFn(textBeforeCursor)] : [...wordSegmenter.segment(textBeforeCursor)];
  let newCursor = cursor;
  while (segments.length > 0 && !isAtomic?.(segments[segments.length - 1]?.segment || "") && isWhitespaceChar(segments[segments.length - 1]?.segment || "")) {
    newCursor -= segments.pop()?.segment.length || 0;
  }
  if (segments.length === 0)
    return newCursor;
  const last = segments[segments.length - 1];
  if (isAtomic?.(last.segment)) {
    newCursor -= last.segment.length;
  } else if (last.isWordLike) {
    const segment = last.segment;
    const matches = [...segment.matchAll(new RegExp(PUNCTUATION_REGEX, "g"))];
    if (matches.length <= 0) {
      newCursor -= segment.length;
    } else {
      const lastMatch = matches[matches.length - 1];
      newCursor -= segment.length - (lastMatch.index + lastMatch[0].length);
    }
  } else {
    while (segments.length > 0 && !isAtomic?.(segments[segments.length - 1]?.segment || "") && !segments[segments.length - 1]?.isWordLike && !isWhitespaceChar(segments[segments.length - 1]?.segment || "")) {
      newCursor -= segments.pop()?.segment.length || 0;
    }
  }
  return newCursor;
}
__name(findWordBackward, "findWordBackward");
function findWordForward(text, cursor, options) {
  if (cursor >= text.length)
    return text.length;
  const textAfterCursor = text.slice(cursor);
  const segmentFn = options?.segment;
  const isAtomic = options?.isAtomicSegment;
  const segments = segmentFn ? segmentFn(textAfterCursor) : wordSegmenter.segment(textAfterCursor);
  const iterator = segments[Symbol.iterator]();
  let next = iterator.next();
  let newCursor = cursor;
  while (!next.done && !isAtomic?.(next.value.segment) && isWhitespaceChar(next.value.segment)) {
    newCursor += next.value.segment.length;
    next = iterator.next();
  }
  if (next.done)
    return newCursor;
  if (isAtomic?.(next.value.segment)) {
    newCursor += next.value.segment.length;
  } else if (next.value.isWordLike) {
    newCursor += PUNCTUATION_REGEX.exec(next.value.segment)?.index ?? next.value.segment.length;
  } else {
    while (!next.done && !isAtomic?.(next.value.segment) && !next.value.isWordLike && !isWhitespaceChar(next.value.segment)) {
      newCursor += next.value.segment.length;
      next = iterator.next();
    }
  }
  return newCursor;
}
__name(findWordForward, "findWordForward");

// pi-dist/pi-tui/components/select-list.js
import { getKeybindings as getKeybindings2 } from "../keybindings.js";
import { truncateToWidth, visibleWidth as visibleWidth3 } from "../utils.js";
var DEFAULT_PRIMARY_COLUMN_WIDTH = 32;
var PRIMARY_COLUMN_GAP = 2;
var MIN_DESCRIPTION_WIDTH = 10;
var normalizeToSingleLine = /* @__PURE__ */ __name((text) => text.replace(/[\r\n]+/g, " ").trim(), "normalizeToSingleLine");
var clamp = /* @__PURE__ */ __name((value, min, max) => Math.max(min, Math.min(value, max)), "clamp");
var SelectList = class {
  static {
    __name(this, "SelectList");
  }
  items = [];
  filteredItems = [];
  selectedIndex = 0;
  mousePressedIndex;
  maxVisible = 5;
  theme;
  layout;
  onSelect;
  onCancel;
  onSelectionChange;
  constructor(items, maxVisible, theme, layout = {}) {
    this.items = items;
    this.filteredItems = items;
    this.maxVisible = maxVisible;
    this.theme = theme;
    this.layout = layout;
  }
  setFilter(filter) {
    this.filteredItems = this.items.filter((item) => item.value.toLowerCase().startsWith(filter.toLowerCase()));
    this.selectedIndex = 0;
  }
  setSelectedIndex(index) {
    this.selectedIndex = Math.max(0, Math.min(index, this.filteredItems.length - 1));
  }
  invalidate() {
  }
  render(width) {
    const lines = [];
    if (this.filteredItems.length === 0) {
      lines.push(this.theme.noMatch("  No matching commands"));
      return lines;
    }
    const primaryColumnWidth = this.getPrimaryColumnWidth();
    const { startIndex, endIndex } = this.getVisibleRange();
    for (let i = startIndex; i < endIndex; i++) {
      const item = this.filteredItems[i];
      if (!item)
        continue;
      const isSelected = i === this.selectedIndex;
      const descriptionSingleLine = item.description ? normalizeToSingleLine(item.description) : void 0;
      lines.push(this.renderItem(item, isSelected, width, descriptionSingleLine, primaryColumnWidth));
    }
    if (startIndex > 0 || endIndex < this.filteredItems.length) {
      const scrollText = `  (${this.selectedIndex + 1}/${this.filteredItems.length})`;
      lines.push(this.theme.scrollInfo(truncateToWidth(scrollText, width - 2, "")));
    }
    return lines;
  }
  handleMouse(event) {
    if (this.filteredItems.length === 0)
      return void 0;
    if (event.type === "wheel" && event.wheelDelta) {
      const delta = event.wheelDelta < 0 ? -1 : 1;
      const previousIndex = this.selectedIndex;
      this.selectedIndex = Math.max(0, Math.min(this.filteredItems.length - 1, this.selectedIndex + delta));
      if (this.selectedIndex !== previousIndex)
        this.notifySelectionChange();
      return { handled: true, render: this.selectedIndex !== previousIndex };
    }
    if (event.button !== "left" || event.type !== "press" && event.type !== "click")
      return void 0;
    const { startIndex, endIndex } = this.getVisibleRange();
    const itemIndex = startIndex + event.y;
    if (itemIndex < startIndex || itemIndex >= endIndex)
      return void 0;
    if (event.type === "press") {
      this.mousePressedIndex = itemIndex;
      if (this.selectedIndex !== itemIndex) {
        this.selectedIndex = itemIndex;
        this.notifySelectionChange();
      }
      return { handled: true, focus: true };
    }
    if (event.type === "click") {
      const clickedIndex = this.mousePressedIndex ?? itemIndex;
      this.mousePressedIndex = void 0;
      const changed = this.selectedIndex !== clickedIndex;
      this.selectedIndex = clickedIndex;
      if (changed)
        this.notifySelectionChange();
      const selectedItem = this.filteredItems[this.selectedIndex];
      if (selectedItem)
        this.onSelect?.(selectedItem);
      return { handled: true };
    }
    return void 0;
  }
  handleInput(keyData) {
    const kb = getKeybindings2();
    if (kb.matches(keyData, "tui.select.up")) {
      this.selectedIndex = this.selectedIndex === 0 ? this.filteredItems.length - 1 : this.selectedIndex - 1;
      this.notifySelectionChange();
    } else if (kb.matches(keyData, "tui.select.down")) {
      this.selectedIndex = this.selectedIndex === this.filteredItems.length - 1 ? 0 : this.selectedIndex + 1;
      this.notifySelectionChange();
    } else if (kb.matches(keyData, "tui.select.confirm")) {
      const selectedItem = this.filteredItems[this.selectedIndex];
      if (selectedItem && this.onSelect) {
        this.onSelect(selectedItem);
      }
    } else if (kb.matches(keyData, "tui.select.cancel")) {
      if (this.onCancel) {
        this.onCancel();
      }
    }
  }
  getVisibleRange() {
    const startIndex = Math.max(0, Math.min(this.selectedIndex - Math.floor(this.maxVisible / 2), this.filteredItems.length - this.maxVisible));
    return {
      startIndex,
      endIndex: Math.min(startIndex + this.maxVisible, this.filteredItems.length)
    };
  }
  renderItem(item, isSelected, width, descriptionSingleLine, primaryColumnWidth) {
    const prefix = isSelected ? "\u2192 " : "  ";
    const prefixWidth = visibleWidth3(prefix);
    if (descriptionSingleLine && width > 40) {
      const effectivePrimaryColumnWidth = Math.max(1, Math.min(primaryColumnWidth, width - prefixWidth - 4));
      const maxPrimaryWidth = Math.max(1, effectivePrimaryColumnWidth - PRIMARY_COLUMN_GAP);
      const truncatedValue2 = this.truncatePrimary(item, isSelected, maxPrimaryWidth, effectivePrimaryColumnWidth);
      const truncatedValueWidth = visibleWidth3(truncatedValue2);
      const spacing = " ".repeat(Math.max(1, effectivePrimaryColumnWidth - truncatedValueWidth));
      const descriptionStart = prefixWidth + truncatedValueWidth + spacing.length;
      const remainingWidth = width - descriptionStart - 2;
      if (remainingWidth > MIN_DESCRIPTION_WIDTH) {
        const truncatedDesc = truncateToWidth(descriptionSingleLine, remainingWidth, "");
        if (isSelected) {
          return this.theme.selectedText(`${prefix}${truncatedValue2}${spacing}${truncatedDesc}`);
        }
        const descText = this.theme.description(spacing + truncatedDesc);
        return prefix + truncatedValue2 + descText;
      }
    }
    const maxWidth = width - prefixWidth - 2;
    const truncatedValue = this.truncatePrimary(item, isSelected, maxWidth, maxWidth);
    if (isSelected) {
      return this.theme.selectedText(`${prefix}${truncatedValue}`);
    }
    return prefix + truncatedValue;
  }
  getPrimaryColumnWidth() {
    const { min, max } = this.getPrimaryColumnBounds();
    const widestPrimary = this.filteredItems.reduce((widest, item) => {
      return Math.max(widest, visibleWidth3(this.getDisplayValue(item)) + PRIMARY_COLUMN_GAP);
    }, 0);
    return clamp(widestPrimary, min, max);
  }
  getPrimaryColumnBounds() {
    const rawMin = this.layout.minPrimaryColumnWidth ?? this.layout.maxPrimaryColumnWidth ?? DEFAULT_PRIMARY_COLUMN_WIDTH;
    const rawMax = this.layout.maxPrimaryColumnWidth ?? this.layout.minPrimaryColumnWidth ?? DEFAULT_PRIMARY_COLUMN_WIDTH;
    return {
      min: Math.max(1, Math.min(rawMin, rawMax)),
      max: Math.max(1, Math.max(rawMin, rawMax))
    };
  }
  truncatePrimary(item, isSelected, maxWidth, columnWidth) {
    const displayValue = this.getDisplayValue(item);
    const truncatedValue = this.layout.truncatePrimary ? this.layout.truncatePrimary({
      text: displayValue,
      maxWidth,
      columnWidth,
      item,
      isSelected
    }) : truncateToWidth(displayValue, maxWidth, "");
    return truncateToWidth(truncatedValue, maxWidth, "");
  }
  getDisplayValue(item) {
    return item.label || item.value;
  }
  notifySelectionChange() {
    const selectedItem = this.filteredItems[this.selectedIndex];
    if (selectedItem && this.onSelectionChange) {
      this.onSelectionChange(selectedItem);
    }
  }
  getSelectedItem() {
    const item = this.filteredItems[this.selectedIndex];
    return item || null;
  }
};

// pi-dist/pi-tui/components/editor.js
import { graphemeSegmenter } from "../../../pi-tui-segmenters.mjs";
import { wordSegmenter as wordSegmenter2 } from "../../../pi-tui-segmenters.mjs";
var PASTE_MARKER_REGEX = /\[paste #(\d+)( (\+\d+ lines|\d+ chars))?\]/g;
var PASTE_MARKER_SINGLE = /^\[paste #(\d+)( (\+\d+ lines|\d+ chars))?\]$/;
function isPasteMarker(segment) {
  return segment.length >= 10 && PASTE_MARKER_SINGLE.test(segment);
}
__name(isPasteMarker, "isPasteMarker");
function segmentWithMarkers(text, baseSegmenter, validIds) {
  if (validIds.size === 0 || !text.includes("[paste #")) {
    return baseSegmenter.segment(text);
  }
  const markers = [];
  for (const m of text.matchAll(PASTE_MARKER_REGEX)) {
    const id = Number.parseInt(m[1], 10);
    if (!validIds.has(id))
      continue;
    markers.push({ start: m.index, end: m.index + m[0].length });
  }
  if (markers.length === 0) {
    return baseSegmenter.segment(text);
  }
  const baseSegments = baseSegmenter.segment(text);
  const result = [];
  let markerIdx = 0;
  for (const seg of baseSegments) {
    while (markerIdx < markers.length && markers[markerIdx].end <= seg.index) {
      markerIdx++;
    }
    const marker = markerIdx < markers.length ? markers[markerIdx] : null;
    if (marker && seg.index >= marker.start && seg.index < marker.end) {
      if (seg.index === marker.start) {
        const markerText = text.slice(marker.start, marker.end);
        result.push({
          segment: markerText,
          index: marker.start,
          input: text
        });
      }
    } else {
      result.push(seg);
    }
  }
  return result;
}
__name(segmentWithMarkers, "segmentWithMarkers");
function wordWrapLine(line, maxWidth, preSegmented) {
  if (!line || maxWidth <= 0) {
    return [{ text: "", startIndex: 0, endIndex: 0 }];
  }
  const lineWidth = visibleWidth4(line);
  if (lineWidth <= maxWidth) {
    return [{ text: line, startIndex: 0, endIndex: line.length }];
  }
  const chunks = [];
  const segments = preSegmented ?? [...graphemeSegmenter.segment(line)];
  let currentWidth = 0;
  let chunkStart = 0;
  let wrapOppIndex = -1;
  let wrapOppWidth = 0;
  for (let i = 0; i < segments.length; i++) {
    const seg = segments[i];
    const grapheme = seg.segment;
    const gWidth = visibleWidth4(grapheme);
    const charIndex = seg.index;
    const isWs = !isPasteMarker(grapheme) && isWhitespaceChar2(grapheme);
    if (currentWidth + gWidth > maxWidth) {
      if (wrapOppIndex >= 0 && currentWidth - wrapOppWidth + gWidth <= maxWidth) {
        chunks.push({ text: line.slice(chunkStart, wrapOppIndex), startIndex: chunkStart, endIndex: wrapOppIndex });
        chunkStart = wrapOppIndex;
        currentWidth -= wrapOppWidth;
      } else if (chunkStart < charIndex) {
        chunks.push({ text: line.slice(chunkStart, charIndex), startIndex: chunkStart, endIndex: charIndex });
        chunkStart = charIndex;
        currentWidth = 0;
      }
      wrapOppIndex = -1;
    }
    if (gWidth > maxWidth) {
      const subChunks = wordWrapLine(grapheme, maxWidth);
      for (let j = 0; j < subChunks.length - 1; j++) {
        const sc = subChunks[j];
        chunks.push({ text: sc.text, startIndex: charIndex + sc.startIndex, endIndex: charIndex + sc.endIndex });
      }
      const last = subChunks[subChunks.length - 1];
      chunkStart = charIndex + last.startIndex;
      currentWidth = visibleWidth4(last.text);
      wrapOppIndex = -1;
      continue;
    }
    currentWidth += gWidth;
    const next = segments[i + 1];
    if (isWs && next && (isPasteMarker(next.segment) || !isWhitespaceChar2(next.segment))) {
      wrapOppIndex = next.index;
      wrapOppWidth = currentWidth;
    } else if (!isWs && next && !isWhitespaceChar2(next.segment)) {
      const isCjk = !isPasteMarker(grapheme) && cjkBreakRegex.test(grapheme);
      const nextIsCjk = !isPasteMarker(next.segment) && cjkBreakRegex.test(next.segment);
      if (isCjk || nextIsCjk) {
        wrapOppIndex = next.index;
        wrapOppWidth = currentWidth;
      }
    }
  }
  chunks.push({ text: line.slice(chunkStart), startIndex: chunkStart, endIndex: line.length });
  return chunks;
}
__name(wordWrapLine, "wordWrapLine");
var SLASH_COMMAND_SELECT_LIST_LAYOUT = {
  minPrimaryColumnWidth: 12,
  maxPrimaryColumnWidth: 32
};
var ATTACHMENT_AUTOCOMPLETE_DEBOUNCE_MS = 20;
var DEFAULT_AUTOCOMPLETE_TRIGGER_CHARACTERS = ["@", "#"];
var unquotedAutocompleteSuffixRegex = new RegExp(`(?:(?!${autocompleteSeparatorRegex2.source}).)*`, "u");
var autocompleteTokenStartSource = `${autocompleteBoundaryRegex2.source}[([{<\`]*`;
function escapeCharacterClass(value) {
  return value.replace(/[\\^$.*+?()[\]{}|-]/g, "\\$&");
}
__name(escapeCharacterClass, "escapeCharacterClass");
function buildTriggerPattern(triggerCharacters) {
  return new RegExp(`${autocompleteTokenStartSource}(?:@"[^"]*|[${triggerCharacters.map(escapeCharacterClass).join("")}]${unquotedAutocompleteSuffixRegex.source})$`, "u");
}
__name(buildTriggerPattern, "buildTriggerPattern");
function buildDebouncePattern(triggerCharacters) {
  const escapedWithoutAt = triggerCharacters.filter((character) => character !== "@").map(escapeCharacterClass);
  return new RegExp(`${autocompleteTokenStartSource}(?:@(?:"[^"]*|${unquotedAutocompleteSuffixRegex.source})|[${escapedWithoutAt.join("")}]${unquotedAutocompleteSuffixRegex.source})$`, "u");
}
__name(buildDebouncePattern, "buildDebouncePattern");
function createScrollBorder(direction, hiddenLineCount, width) {
  const availableWidth = Math.max(0, width);
  const label = ` ${direction} ${hiddenLineCount} more `;
  const labelWidth = visibleWidth4(label);
  if (labelWidth + 2 <= availableWidth) {
    const leftWidth = Math.floor((availableWidth - labelWidth) / 2);
    return "\u2500".repeat(leftWidth) + label + "\u2500".repeat(availableWidth - leftWidth - labelWidth);
  }
  const indicator = `\u2500\u2500\u2500 ${direction} ${hiddenLineCount} more `;
  const remaining = availableWidth - visibleWidth4(indicator);
  if (remaining >= 0)
    return indicator + "\u2500".repeat(remaining);
  const ellipsis = "...".slice(0, availableWidth);
  const indicatorWidth = availableWidth - visibleWidth4(ellipsis);
  return sliceByColumn(indicator, 0, indicatorWidth, true) + ellipsis;
}
__name(createScrollBorder, "createScrollBorder");
var Editor = class {
  static {
    __name(this, "Editor");
  }
  state = {
    lines: [""],
    cursorLine: 0,
    cursorCol: 0
  };
  /** Focusable interface - set by TUI when focus changes */
  focused = false;
  tui;
  theme;
  paddingX = 0;
  // Store last render geometry for cursor navigation and mouse hit-testing.
  lastWidth = 80;
  renderedVisibleLineCount = 1;
  renderedAutocompleteHeight = 0;
  // Vertical scrolling support
  scrollOffset = 0;
  // Border color (can be changed dynamically)
  borderColor;
  // Autocomplete support
  autocompleteProvider;
  autocompleteTriggerCharacters = [...DEFAULT_AUTOCOMPLETE_TRIGGER_CHARACTERS];
  autocompleteTriggerPattern = buildTriggerPattern(this.autocompleteTriggerCharacters);
  autocompleteDebouncePattern = buildDebouncePattern(this.autocompleteTriggerCharacters);
  autocompleteList;
  autocompleteState = null;
  autocompletePrefix = "";
  autocompleteMaxVisible = 5;
  autocompleteAbort;
  autocompleteDebounceTimer;
  autocompleteRequestTask = Promise.resolve();
  autocompleteStartToken = 0;
  autocompleteRequestId = 0;
  // Paste tracking for large pastes
  pastes = /* @__PURE__ */ new Map();
  pasteCounter = 0;
  // Bracketed paste mode buffering
  pasteBuffer = "";
  isInPaste = false;
  // Prompt history for up/down navigation
  history = [];
  historyIndex = -1;
  // -1 = not browsing, 0 = most recent, 1 = older, etc.
  historyDraft = null;
  // Kill ring for Emacs-style kill/yank operations
  killRing = new KillRing();
  lastAction = null;
  // Character jump mode
  jumpMode = null;
  // Preferred visual column for vertical cursor movement (sticky column)
  preferredVisualCol = null;
  // When the cursor is snapped to the start of an atomic segment, e.g. a
  // paste marker, cursorCol no longer reflects where the cursor would have
  // landed. This field stores the pre-snap cursorCol so that the next
  // vertical move can resolve it to a visual column on whatever VL it belongs
  // to.
  snappedFromCursorCol = null;
  // Undo support
  undoStack = new UndoStack();
  onSubmit;
  onChange;
  disableSubmit = false;
  constructor(tui, theme, options = {}) {
    this.tui = tui;
    this.theme = theme;
    this.borderColor = theme.borderColor;
    const paddingX = options.paddingX ?? 0;
    this.paddingX = Number.isFinite(paddingX) ? Math.max(0, Math.floor(paddingX)) : 0;
    const maxVisible = options.autocompleteMaxVisible ?? 5;
    this.autocompleteMaxVisible = Number.isFinite(maxVisible) ? Math.max(3, Math.min(20, Math.floor(maxVisible))) : 5;
  }
  /** Set of currently valid paste IDs, for marker-aware segmentation. */
  validPasteIds() {
    return new Set(this.pastes.keys());
  }
  /** Segment text with paste-marker awareness, only merging markers with valid IDs. */
  segment(text, mode) {
    return segmentWithMarkers(text, mode === "word" ? wordSegmenter2 : graphemeSegmenter, this.validPasteIds());
  }
  getPaddingX() {
    return this.paddingX;
  }
  setPaddingX(padding) {
    const newPadding = Number.isFinite(padding) ? Math.max(0, Math.floor(padding)) : 0;
    if (this.paddingX !== newPadding) {
      this.paddingX = newPadding;
      this.tui.requestRender();
    }
  }
  getAutocompleteMaxVisible() {
    return this.autocompleteMaxVisible;
  }
  setAutocompleteMaxVisible(maxVisible) {
    const newMaxVisible = Number.isFinite(maxVisible) ? Math.max(3, Math.min(20, Math.floor(maxVisible))) : 5;
    if (this.autocompleteMaxVisible !== newMaxVisible) {
      this.autocompleteMaxVisible = newMaxVisible;
      this.tui.requestRender();
    }
  }
  setAutocompleteProvider(provider) {
    this.cancelAutocomplete();
    this.autocompleteProvider = provider;
    this.setAutocompleteTriggerCharacters(provider.triggerCharacters ?? []);
  }
  /**
   * Add a prompt to history for up/down arrow navigation.
   * Called after successful submission.
   */
  addToHistory(text) {
    const trimmed = text.trim();
    if (!trimmed)
      return;
    if (this.history.length > 0 && this.history[0] === trimmed)
      return;
    this.history.unshift(trimmed);
    if (this.history.length > 100) {
      this.history.pop();
    }
  }
  isEditorEmpty() {
    return this.state.lines.length === 1 && this.state.lines[0] === "";
  }
  isOnFirstVisualLine() {
    const visualLines = this.buildVisualLineMap(this.lastWidth);
    const currentVisualLine = this.findCurrentVisualLine(visualLines);
    return currentVisualLine === 0;
  }
  isOnLastVisualLine() {
    const visualLines = this.buildVisualLineMap(this.lastWidth);
    const currentVisualLine = this.findCurrentVisualLine(visualLines);
    return currentVisualLine === visualLines.length - 1;
  }
  navigateHistory(direction) {
    this.lastAction = null;
    if (this.history.length === 0)
      return;
    const newIndex = this.historyIndex - direction;
    if (newIndex < -1 || newIndex >= this.history.length)
      return;
    if (this.historyIndex === -1 && newIndex >= 0) {
      this.pushUndoSnapshot();
      this.historyDraft = structuredClone(this.state);
    }
    this.historyIndex = newIndex;
    if (this.historyIndex === -1) {
      const draft = this.historyDraft;
      this.historyDraft = null;
      if (draft) {
        this.state = draft;
        this.preferredVisualCol = null;
        this.snappedFromCursorCol = null;
        this.scrollOffset = 0;
        if (this.onChange)
          this.onChange(this.getText());
      } else {
        this.setTextInternal("");
      }
    } else {
      this.setTextInternal(this.history[this.historyIndex] || "", direction === -1 ? "start" : "end");
    }
  }
  exitHistoryBrowsing() {
    this.historyIndex = -1;
    this.historyDraft = null;
  }
  /** Internal setText that doesn't reset history state - used by navigateHistory */
  setTextInternal(text, cursorPlacement = "end") {
    const lines = text.split("\n");
    this.state.lines = lines.length === 0 ? [""] : lines;
    this.state.cursorLine = cursorPlacement === "start" ? 0 : this.state.lines.length - 1;
    this.setCursorCol(cursorPlacement === "start" ? 0 : this.state.lines[this.state.cursorLine]?.length || 0);
    this.scrollOffset = 0;
    if (this.onChange) {
      this.onChange(this.getText());
    }
  }
  invalidate() {
  }
  renderTopBorder(width, hiddenLineCount) {
    const border = hiddenLineCount > 0 ? createScrollBorder("\u2191", hiddenLineCount, width) : "\u2500".repeat(width);
    return this.borderColor(border);
  }
  renderBottomBorder(width, hiddenLineCount) {
    const border = hiddenLineCount > 0 ? createScrollBorder("\u2193", hiddenLineCount, width) : "\u2500".repeat(width);
    return this.borderColor(border);
  }
  render(width) {
    const maxPadding = Math.max(0, Math.floor((width - 1) / 2));
    const paddingX = Math.min(this.paddingX, maxPadding);
    const contentWidth = Math.max(1, width - paddingX * 2);
    const layoutWidth = Math.max(1, contentWidth - (paddingX ? 0 : 1));
    this.lastWidth = layoutWidth;
    const layoutLines = this.layoutText(layoutWidth);
    const terminalRows = this.tui.terminal.rows;
    const maxVisibleLines = Math.max(5, Math.floor(terminalRows * 0.3));
    let cursorLineIndex = layoutLines.findIndex((line) => line.hasCursor);
    if (cursorLineIndex === -1)
      cursorLineIndex = 0;
    if (cursorLineIndex < this.scrollOffset) {
      this.scrollOffset = cursorLineIndex;
    } else if (cursorLineIndex >= this.scrollOffset + maxVisibleLines) {
      this.scrollOffset = cursorLineIndex - maxVisibleLines + 1;
    }
    const maxScrollOffset = Math.max(0, layoutLines.length - maxVisibleLines);
    this.scrollOffset = Math.max(0, Math.min(this.scrollOffset, maxScrollOffset));
    const visibleLines = layoutLines.slice(this.scrollOffset, this.scrollOffset + maxVisibleLines);
    this.renderedVisibleLineCount = visibleLines.length;
    const result = [];
    const leftPadding = " ".repeat(paddingX);
    const rightPadding = leftPadding;
    result.push(this.renderTopBorder(width, this.scrollOffset));
    const emitCursorMarker = this.focused;
    for (const layoutLine of visibleLines) {
      let displayText = layoutLine.text;
      let lineVisibleWidth = visibleWidth4(layoutLine.text);
      let cursorInPadding = false;
      if (layoutLine.hasCursor && layoutLine.cursorPos !== void 0) {
        const before = displayText.slice(0, layoutLine.cursorPos);
        const after = displayText.slice(layoutLine.cursorPos);
        const marker = emitCursorMarker ? CURSOR_MARKER : "";
        if (after.length > 0) {
          const afterGraphemes = [...this.segment(after, "grapheme")];
          const firstGrapheme = afterGraphemes[0]?.segment || "";
          const restAfter = after.slice(firstGrapheme.length);
          const cursor = `\x1B[7m${firstGrapheme}\x1B[0m`;
          displayText = before + marker + cursor + restAfter;
        } else {
          const cursor = "\x1B[7m \x1B[0m";
          displayText = before + marker + cursor;
          lineVisibleWidth = lineVisibleWidth + 1;
          if (lineVisibleWidth > contentWidth && paddingX > 0) {
            cursorInPadding = true;
          }
        }
      }
      const padding = " ".repeat(Math.max(0, contentWidth - lineVisibleWidth));
      const lineRightPadding = cursorInPadding ? rightPadding.slice(1) : rightPadding;
      result.push(`${leftPadding}${displayText}${padding}${lineRightPadding}`);
    }
    const linesBelow = layoutLines.length - (this.scrollOffset + visibleLines.length);
    result.push(this.renderBottomBorder(width, linesBelow));
    this.renderedAutocompleteHeight = 0;
    if (this.autocompleteState && this.autocompleteList) {
      const autocompleteResult = this.autocompleteList.render(contentWidth);
      this.renderedAutocompleteHeight = autocompleteResult.length;
      for (const line of autocompleteResult) {
        const lineWidth = visibleWidth4(line);
        const linePadding = " ".repeat(Math.max(0, contentWidth - lineWidth));
        result.push(`${leftPadding}${line}${linePadding}${rightPadding}`);
      }
    }
    return result;
  }
  handleMouse(event) {
    const autocompleteStartRow = this.renderedVisibleLineCount + 2;
    if (this.autocompleteState && this.autocompleteList && event.y >= autocompleteStartRow && event.y < autocompleteStartRow + this.renderedAutocompleteHeight) {
      const maxPadding2 = Math.max(0, Math.floor((event.width - 1) / 2));
      const paddingX2 = Math.min(this.paddingX, maxPadding2);
      const contentWidth = Math.max(1, event.width - paddingX2 * 2);
      const result = this.autocompleteList.handleMouse?.({
        ...event,
        x: event.x - paddingX2,
        y: event.y - autocompleteStartRow,
        width: contentWidth,
        height: this.renderedAutocompleteHeight
      });
      return result ? { ...result, focus: true } : void 0;
    }
    if (event.type !== "click" || event.button !== "left")
      return void 0;
    if (event.y <= 0 || event.y > this.renderedVisibleLineCount)
      return { handled: true, focus: true };
    const visualLines = this.buildVisualLineMap(this.lastWidth);
    const visualLineIndex = this.scrollOffset + event.y - 1;
    const visualLine = visualLines[visualLineIndex];
    if (!visualLine)
      return { handled: true, focus: true };
    const logicalLine = this.state.lines[visualLine.logicalLine] ?? "";
    const chunkEnd = visualLine.startCol + visualLine.length;
    const chunk = logicalLine.slice(visualLine.startCol, chunkEnd);
    const maxPadding = Math.max(0, Math.floor((event.width - 1) / 2));
    const paddingX = Math.min(this.paddingX, maxPadding);
    const targetColumn = Math.max(0, event.x - paddingX);
    let visibleColumn = 0;
    let targetIndex = chunk.length;
    let lastGraphemeIndex = 0;
    for (const grapheme of this.segment(chunk, "grapheme")) {
      const nextColumn = visibleColumn + visibleWidth4(grapheme.segment);
      lastGraphemeIndex = grapheme.index;
      if (targetColumn < nextColumn) {
        targetIndex = grapheme.index;
        break;
      }
      visibleColumn = nextColumn;
    }
    const isLastSegment = visualLineIndex === visualLines.length - 1 || visualLines[visualLineIndex + 1]?.logicalLine !== visualLine.logicalLine;
    if (!isLastSegment && targetIndex === chunk.length && chunk.length > 0)
      targetIndex = lastGraphemeIndex;
    this.state.cursorLine = visualLine.logicalLine;
    this.setCursorCol(visualLine.startCol + targetIndex);
    this.lastAction = null;
    this.exitHistoryBrowsing();
    if (this.autocompleteState)
      this.updateAutocomplete();
    return { handled: true, focus: true };
  }
  handleInput(data) {
    const kb = getKeybindings3();
    if (this.jumpMode !== null) {
      if (kb.matches(data, "tui.editor.jumpForward") || kb.matches(data, "tui.editor.jumpBackward")) {
        this.jumpMode = null;
        return;
      }
      const printable2 = decodePrintableKey(data) ?? (data.charCodeAt(0) >= 32 ? data : void 0);
      if (printable2 !== void 0) {
        const direction = this.jumpMode;
        this.jumpMode = null;
        this.jumpToChar(printable2, direction);
        return;
      }
      this.jumpMode = null;
    }
    if (data.includes("\x1B[200~")) {
      this.isInPaste = true;
      this.pasteBuffer = "";
      data = data.replace("\x1B[200~", "");
    }
    if (this.isInPaste) {
      this.pasteBuffer += data;
      const endIndex = this.pasteBuffer.indexOf("\x1B[201~");
      if (endIndex !== -1) {
        const pasteContent = this.pasteBuffer.substring(0, endIndex);
        if (pasteContent.length > 0) {
          this.handlePaste(pasteContent);
        }
        this.isInPaste = false;
        const remaining = this.pasteBuffer.substring(endIndex + 6);
        this.pasteBuffer = "";
        if (remaining.length > 0) {
          this.handleInput(remaining);
        }
        return;
      }
      return;
    }
    if (kb.matches(data, "tui.input.copy")) {
      return;
    }
    if (kb.matches(data, "tui.editor.undo")) {
      this.undo();
      return;
    }
    if (this.autocompleteState && this.autocompleteList) {
      if (kb.matches(data, "tui.select.cancel")) {
        this.cancelAutocomplete();
        return;
      }
      if (kb.matches(data, "tui.select.up") || kb.matches(data, "tui.select.down")) {
        this.autocompleteList.handleInput(data);
        return;
      }
      if (kb.matches(data, "tui.input.tab")) {
        const selected = this.autocompleteList.getSelectedItem();
        if (selected && this.autocompleteProvider) {
          this.pushUndoSnapshot();
          this.lastAction = null;
          const result = this.autocompleteProvider.applyCompletion(this.state.lines, this.state.cursorLine, this.state.cursorCol, selected, this.autocompletePrefix);
          this.state.lines = result.lines;
          this.state.cursorLine = result.cursorLine;
          this.setCursorCol(result.cursorCol);
          this.cancelAutocomplete();
          if (this.onChange)
            this.onChange(this.getText());
        }
        return;
      }
      if (kb.matches(data, "tui.select.confirm")) {
        const selected = this.autocompleteList.getSelectedItem();
        if (selected && this.autocompleteProvider) {
          this.pushUndoSnapshot();
          this.lastAction = null;
          const result = this.autocompleteProvider.applyCompletion(this.state.lines, this.state.cursorLine, this.state.cursorCol, selected, this.autocompletePrefix);
          this.state.lines = result.lines;
          this.state.cursorLine = result.cursorLine;
          this.setCursorCol(result.cursorCol);
          if (this.autocompletePrefix.startsWith("/")) {
            this.cancelAutocomplete();
          } else {
            this.cancelAutocomplete();
            if (this.onChange)
              this.onChange(this.getText());
            return;
          }
        }
      }
    }
    if (kb.matches(data, "tui.input.tab") && !this.autocompleteState) {
      this.handleTabCompletion();
      return;
    }
    if (kb.matches(data, "tui.editor.deleteToLineEnd")) {
      this.deleteToEndOfLine();
      return;
    }
    if (kb.matches(data, "tui.editor.deleteToLineStart")) {
      this.deleteToStartOfLine();
      return;
    }
    if (kb.matches(data, "tui.editor.deleteWordBackward")) {
      this.deleteWordBackwards();
      return;
    }
    if (kb.matches(data, "tui.editor.deleteWordForward")) {
      this.deleteWordForward();
      return;
    }
    if (kb.matches(data, "tui.editor.deleteCharBackward") || matchesKey(data, "shift+backspace")) {
      this.handleBackspace();
      return;
    }
    if (kb.matches(data, "tui.editor.deleteCharForward") || matchesKey(data, "shift+delete")) {
      this.handleForwardDelete();
      return;
    }
    if (kb.matches(data, "tui.editor.yank")) {
      this.yank();
      return;
    }
    if (kb.matches(data, "tui.editor.yankPop")) {
      this.yankPop();
      return;
    }
    if (kb.matches(data, "tui.editor.historyPrevious")) {
      this.cancelAutocomplete();
      this.navigateHistory(-1);
      return;
    }
    if (kb.matches(data, "tui.editor.historyNext")) {
      this.cancelAutocomplete();
      this.navigateHistory(1);
      return;
    }
    if (kb.matches(data, "tui.editor.cursorLineStart")) {
      this.moveToLineStart();
      return;
    }
    if (kb.matches(data, "tui.editor.cursorLineEnd")) {
      this.moveToLineEnd();
      return;
    }
    if (kb.matches(data, "tui.editor.cursorWordLeft")) {
      this.moveWordBackwards();
      return;
    }
    if (kb.matches(data, "tui.editor.cursorWordRight")) {
      this.moveWordForwards();
      return;
    }
    if (kb.matches(data, "tui.input.newLine") || data.charCodeAt(0) === 10 && data.length > 1 || data === "\x1B\r" || data === "\x1B[13;2~" || data.length > 1 && data.includes("\x1B") && data.includes("\r") || data === "\n" && data.length === 1) {
      if (this.shouldSubmitOnBackslashEnter(data, kb)) {
        this.handleBackspace();
        this.submitValue();
        return;
      }
      this.addNewLine();
      return;
    }
    if (kb.matches(data, "tui.input.submit")) {
      if (this.disableSubmit)
        return;
      const currentLine = this.state.lines[this.state.cursorLine] || "";
      if (this.state.cursorCol > 0 && currentLine[this.state.cursorCol - 1] === "\\") {
        this.handleBackspace();
        this.addNewLine();
        return;
      }
      this.submitValue();
      return;
    }
    if (kb.matches(data, "tui.editor.cursorUp")) {
      if (this.isOnFirstVisualLine() && (this.isEditorEmpty() || this.historyIndex > -1 || this.state.cursorCol === 0)) {
        this.navigateHistory(-1);
      } else if (this.isOnFirstVisualLine()) {
        this.moveToLineStart();
      } else {
        this.moveCursor(-1, 0);
      }
      return;
    }
    if (kb.matches(data, "tui.editor.cursorDown")) {
      if (this.historyIndex > -1 && this.isOnLastVisualLine()) {
        this.navigateHistory(1);
      } else if (this.isOnLastVisualLine()) {
        this.moveToLineEnd();
      } else {
        this.moveCursor(1, 0);
      }
      return;
    }
    if (kb.matches(data, "tui.editor.cursorRight")) {
      this.moveCursor(0, 1);
      return;
    }
    if (kb.matches(data, "tui.editor.cursorLeft")) {
      this.moveCursor(0, -1);
      return;
    }
    if (kb.matches(data, "tui.editor.pageUp")) {
      this.pageScroll(-1);
      return;
    }
    if (kb.matches(data, "tui.editor.pageDown")) {
      this.pageScroll(1);
      return;
    }
    if (kb.matches(data, "tui.editor.jumpForward")) {
      this.jumpMode = "forward";
      return;
    }
    if (kb.matches(data, "tui.editor.jumpBackward")) {
      this.jumpMode = "backward";
      return;
    }
    if (matchesKey(data, "shift+space")) {
      this.insertCharacter(" ");
      return;
    }
    const printable = decodePrintableKey(data);
    if (printable !== void 0) {
      this.insertCharacter(printable);
      return;
    }
    if (data.charCodeAt(0) >= 32) {
      this.insertCharacter(data);
    }
  }
  layoutText(contentWidth) {
    const layoutLines = [];
    if (this.state.lines.length === 0 || this.state.lines.length === 1 && this.state.lines[0] === "") {
      layoutLines.push({
        text: "",
        hasCursor: true,
        cursorPos: 0
      });
      return layoutLines;
    }
    for (let i = 0; i < this.state.lines.length; i++) {
      const line = this.state.lines[i] || "";
      const isCurrentLine = i === this.state.cursorLine;
      const lineVisibleWidth = visibleWidth4(line);
      if (lineVisibleWidth <= contentWidth) {
        if (isCurrentLine) {
          layoutLines.push({
            text: line,
            hasCursor: true,
            cursorPos: this.state.cursorCol
          });
        } else {
          layoutLines.push({
            text: line,
            hasCursor: false
          });
        }
      } else {
        const chunks = wordWrapLine(line, contentWidth, [...this.segment(line, "grapheme")]);
        for (let chunkIndex = 0; chunkIndex < chunks.length; chunkIndex++) {
          const chunk = chunks[chunkIndex];
          if (!chunk)
            continue;
          const cursorPos = this.state.cursorCol;
          const isLastChunk = chunkIndex === chunks.length - 1;
          let hasCursorInChunk = false;
          let adjustedCursorPos = 0;
          if (isCurrentLine) {
            if (isLastChunk) {
              hasCursorInChunk = cursorPos >= chunk.startIndex;
              adjustedCursorPos = cursorPos - chunk.startIndex;
            } else {
              hasCursorInChunk = cursorPos >= chunk.startIndex && cursorPos < chunk.endIndex;
              if (hasCursorInChunk) {
                adjustedCursorPos = cursorPos - chunk.startIndex;
                if (adjustedCursorPos > chunk.text.length) {
                  adjustedCursorPos = chunk.text.length;
                }
              }
            }
          }
          if (hasCursorInChunk) {
            layoutLines.push({
              text: chunk.text,
              hasCursor: true,
              cursorPos: adjustedCursorPos
            });
          } else {
            layoutLines.push({
              text: chunk.text,
              hasCursor: false
            });
          }
        }
      }
    }
    return layoutLines;
  }
  getText() {
    return this.state.lines.join("\n");
  }
  expandPasteMarkers(text) {
    let result = text;
    for (const [pasteId, pasteContent] of this.pastes) {
      const markerRegex = new RegExp(`\\[paste #${pasteId}( (\\+\\d+ lines|\\d+ chars))?\\]`, "g");
      result = result.replace(markerRegex, () => pasteContent);
    }
    return result;
  }
  /**
   * Get text with paste markers expanded to their actual content.
   * Use this when you need the full content (e.g., for external editor).
   */
  getExpandedText() {
    return this.expandPasteMarkers(this.state.lines.join("\n"));
  }
  getLines() {
    return [...this.state.lines];
  }
  getCursor() {
    return { line: this.state.cursorLine, col: this.state.cursorCol };
  }
  setText(text) {
    this.cancelAutocomplete();
    this.lastAction = null;
    this.exitHistoryBrowsing();
    const normalized = this.normalizeText(text);
    if (this.getText() !== normalized) {
      this.pushUndoSnapshot();
    }
    this.pastes.clear();
    this.pasteCounter = 0;
    this.setTextInternal(normalized);
  }
  /**
   * Insert text at the current cursor position.
   * Used for programmatic insertion (e.g., clipboard image markers).
   * This is atomic for undo - single undo restores entire pre-insert state.
   */
  insertTextAtCursor(text) {
    if (!text)
      return;
    this.cancelAutocomplete();
    this.pushUndoSnapshot();
    this.lastAction = null;
    this.exitHistoryBrowsing();
    this.insertTextAtCursorInternal(text);
  }
  /**
   * Normalize text for editor storage:
   * - Normalize line endings (\r\n and \r -> \n)
   * - Expand tabs to 4 spaces
   */
  normalizeText(text) {
    return text.replace(/\r\n/g, "\n").replace(/\r/g, "\n").replace(/\t/g, "    ");
  }
  /**
   * Internal text insertion at cursor. Handles single and multi-line text.
   * Does not push undo snapshots or trigger autocomplete - caller is responsible.
   * Normalizes line endings and calls onChange once at the end.
   */
  insertTextAtCursorInternal(text) {
    if (!text)
      return;
    const normalized = this.normalizeText(text);
    const insertedLines = normalized.split("\n");
    const currentLine = this.state.lines[this.state.cursorLine] || "";
    const beforeCursor = currentLine.slice(0, this.state.cursorCol);
    const afterCursor = currentLine.slice(this.state.cursorCol);
    if (insertedLines.length === 1) {
      this.state.lines[this.state.cursorLine] = beforeCursor + normalized + afterCursor;
      this.setCursorCol(this.state.cursorCol + normalized.length);
    } else {
      this.state.lines = [
        // All lines before current line
        ...this.state.lines.slice(0, this.state.cursorLine),
        // The first inserted line merged with text before cursor
        beforeCursor + insertedLines[0],
        // All middle inserted lines
        ...insertedLines.slice(1, -1),
        // The last inserted line with text after cursor
        insertedLines[insertedLines.length - 1] + afterCursor,
        // All lines after current line
        ...this.state.lines.slice(this.state.cursorLine + 1)
      ];
      this.state.cursorLine += insertedLines.length - 1;
      this.setCursorCol((insertedLines[insertedLines.length - 1] || "").length);
    }
    if (this.onChange) {
      this.onChange(this.getText());
    }
  }
  // All the editor methods from before...
  insertCharacter(char, skipUndoCoalescing) {
    this.exitHistoryBrowsing();
    if (!skipUndoCoalescing) {
      if (isWhitespaceChar2(char) || this.lastAction !== "type-word") {
        this.pushUndoSnapshot();
      }
      this.lastAction = "type-word";
    }
    const line = this.state.lines[this.state.cursorLine] || "";
    const before = line.slice(0, this.state.cursorCol);
    const after = line.slice(this.state.cursorCol);
    this.state.lines[this.state.cursorLine] = before + char + after;
    this.setCursorCol(this.state.cursorCol + char.length);
    if (this.onChange) {
      this.onChange(this.getText());
    }
    if (!this.autocompleteState) {
      if (char === "/" && this.isAtStartOfMessage()) {
        this.tryTriggerAutocomplete();
      } else if (this.autocompleteTriggerCharacters.includes(char)) {
        const currentLine = this.state.lines[this.state.cursorLine] || "";
        const textBeforeCursor = currentLine.slice(0, this.state.cursorCol);
        if (this.autocompleteTriggerPattern.test(textBeforeCursor)) {
          this.tryTriggerAutocomplete();
        }
      } else if (/[a-zA-Z0-9.\-_]/.test(char) || cjkBreakRegex.test(char)) {
        const currentLine = this.state.lines[this.state.cursorLine] || "";
        const textBeforeCursor = currentLine.slice(0, this.state.cursorCol);
        if (this.isInSlashCommandContext(textBeforeCursor)) {
          this.tryTriggerAutocomplete();
        } else if (this.autocompleteTriggerPattern.test(textBeforeCursor)) {
          this.tryTriggerAutocomplete();
        }
      }
    } else {
      this.updateAutocomplete();
    }
  }
  handlePaste(pastedText) {
    this.cancelAutocomplete();
    this.exitHistoryBrowsing();
    this.lastAction = null;
    this.pushUndoSnapshot();
    const decodedText = pastedText.replace(/\x1b\[(\d+);5u/g, (match, code) => {
      const cp = Number(code);
      if (cp >= 97 && cp <= 122)
        return String.fromCharCode(cp - 96);
      if (cp >= 65 && cp <= 90)
        return String.fromCharCode(cp - 64);
      return match;
    });
    const cleanText = this.normalizeText(decodedText);
    let filteredText = cleanText.split("").filter((char) => char === "\n" || char.charCodeAt(0) >= 32).join("");
    if (/^[/~.]/.test(filteredText)) {
      const currentLine = this.state.lines[this.state.cursorLine] || "";
      const charBeforeCursor = this.state.cursorCol > 0 ? currentLine[this.state.cursorCol - 1] : "";
      if (charBeforeCursor && /\w/.test(charBeforeCursor)) {
        filteredText = ` ${filteredText}`;
      }
    }
    const pastedLines = filteredText.split("\n");
    const totalChars = filteredText.length;
    if (pastedLines.length > 10 || totalChars > 1e3) {
      this.pasteCounter++;
      const pasteId = this.pasteCounter;
      this.pastes.set(pasteId, filteredText);
      const marker = pastedLines.length > 10 ? `[paste #${pasteId} +${pastedLines.length} lines]` : `[paste #${pasteId} ${totalChars} chars]`;
      this.insertTextAtCursorInternal(marker);
      return;
    }
    if (pastedLines.length === 1) {
      this.insertTextAtCursorInternal(filteredText);
      return;
    }
    this.insertTextAtCursorInternal(filteredText);
  }
  addNewLine() {
    this.cancelAutocomplete();
    this.exitHistoryBrowsing();
    this.lastAction = null;
    this.pushUndoSnapshot();
    const currentLine = this.state.lines[this.state.cursorLine] || "";
    const before = currentLine.slice(0, this.state.cursorCol);
    const after = currentLine.slice(this.state.cursorCol);
    this.state.lines[this.state.cursorLine] = before;
    this.state.lines.splice(this.state.cursorLine + 1, 0, after);
    this.state.cursorLine++;
    this.setCursorCol(0);
    if (this.onChange) {
      this.onChange(this.getText());
    }
  }
  shouldSubmitOnBackslashEnter(data, kb) {
    if (this.disableSubmit)
      return false;
    if (!matchesKey(data, "enter"))
      return false;
    const submitKeys = kb.getKeys("tui.input.submit");
    const hasShiftEnter = submitKeys.includes("shift+enter") || submitKeys.includes("shift+return");
    if (!hasShiftEnter)
      return false;
    const currentLine = this.state.lines[this.state.cursorLine] || "";
    return this.state.cursorCol > 0 && currentLine[this.state.cursorCol - 1] === "\\";
  }
  submitValue() {
    this.cancelAutocomplete();
    const result = this.expandPasteMarkers(this.state.lines.join("\n")).trim();
    this.state = { lines: [""], cursorLine: 0, cursorCol: 0 };
    this.pastes.clear();
    this.pasteCounter = 0;
    this.exitHistoryBrowsing();
    this.scrollOffset = 0;
    this.undoStack.clear();
    this.lastAction = null;
    if (this.onChange)
      this.onChange("");
    if (this.onSubmit)
      this.onSubmit(result);
  }
  handleBackspace() {
    this.exitHistoryBrowsing();
    this.lastAction = null;
    if (this.state.cursorCol > 0) {
      this.pushUndoSnapshot();
      let line = this.state.lines[this.state.cursorLine] || "";
      const beforeCursor = line.slice(0, this.state.cursorCol);
      const graphemes = [...this.segment(beforeCursor, "grapheme")];
      const lastGrapheme = graphemes[graphemes.length - 1];
      const graphemeLength = lastGrapheme ? lastGrapheme.segment.length : 1;
      const isPastedSegmented = PASTE_MARKER_SINGLE.exec(lastGrapheme.segment);
      if (isPastedSegmented) {
        const targetId = Number(isPastedSegmented[1]);
        this.pastes.delete(targetId);
        this.pasteCounter--;
        const higherIds = [...this.pastes.keys()].filter((id) => id > targetId).sort((a, b) => a - b);
        for (const id of higherIds) {
          this.pastes.set(id - 1, this.pastes.get(id));
          this.pastes.delete(id);
        }
        this.state.lines = this.state.lines.map((line2) => line2.replace(PASTE_MARKER_REGEX, (fullMatch, idGroup, suffixGroup) => {
          const x = Number(idGroup);
          if (x <= targetId)
            return fullMatch;
          return `[paste #${x - 1}${suffixGroup}]`;
        }));
      }
      line = this.state.lines[this.state.cursorLine] || "";
      const before = line.slice(0, this.state.cursorCol - graphemeLength);
      const after = line.slice(this.state.cursorCol);
      this.state.lines[this.state.cursorLine] = before + after;
      this.setCursorCol(this.state.cursorCol - graphemeLength);
    } else if (this.state.cursorLine > 0) {
      this.pushUndoSnapshot();
      const currentLine = this.state.lines[this.state.cursorLine] || "";
      const previousLine = this.state.lines[this.state.cursorLine - 1] || "";
      this.state.lines[this.state.cursorLine - 1] = previousLine + currentLine;
      this.state.lines.splice(this.state.cursorLine, 1);
      this.state.cursorLine--;
      this.setCursorCol(previousLine.length);
    }
    if (this.onChange) {
      this.onChange(this.getText());
    }
    if (this.autocompleteState) {
      this.updateAutocomplete();
    } else {
      const currentLine = this.state.lines[this.state.cursorLine] || "";
      const textBeforeCursor = currentLine.slice(0, this.state.cursorCol);
      if (this.isInSlashCommandContext(textBeforeCursor)) {
        this.tryTriggerAutocomplete();
      } else if (this.autocompleteTriggerPattern.test(textBeforeCursor)) {
        this.tryTriggerAutocomplete();
      }
    }
  }
  /**
   * Set cursor column and clear preferredVisualCol.
   * Use this for all non-vertical cursor movements to reset sticky column behavior.
   */
  setCursorCol(col) {
    this.state.cursorCol = col;
    this.preferredVisualCol = null;
    this.snappedFromCursorCol = null;
  }
  /**
   * Move cursor to a target visual line, applying sticky column logic.
   * Shared by moveCursor() and pageScroll().
   */
  moveToVisualLine(visualLines, currentVisualLine, targetVisualLine) {
    const currentVL = visualLines[currentVisualLine];
    const targetVL = visualLines[targetVisualLine];
    if (!(currentVL && targetVL))
      return;
    let currentVisualCol;
    if (this.snappedFromCursorCol !== null) {
      const vlIndex = this.findVisualLineAt(visualLines, currentVL.logicalLine, this.snappedFromCursorCol);
      currentVisualCol = this.snappedFromCursorCol - visualLines[vlIndex].startCol;
    } else {
      currentVisualCol = this.state.cursorCol - currentVL.startCol;
    }
    const isLastSourceSegment = currentVisualLine === visualLines.length - 1 || visualLines[currentVisualLine + 1]?.logicalLine !== currentVL.logicalLine;
    const sourceMaxVisualCol = isLastSourceSegment ? currentVL.length : Math.max(0, currentVL.length - 1);
    const isLastTargetSegment = targetVisualLine === visualLines.length - 1 || visualLines[targetVisualLine + 1]?.logicalLine !== targetVL.logicalLine;
    const targetMaxVisualCol = isLastTargetSegment ? targetVL.length : Math.max(0, targetVL.length - 1);
    const moveToVisualCol = this.computeVerticalMoveColumn(currentVisualCol, sourceMaxVisualCol, targetMaxVisualCol);
    this.state.cursorLine = targetVL.logicalLine;
    const targetCol = targetVL.startCol + moveToVisualCol;
    const logicalLine = this.state.lines[targetVL.logicalLine] || "";
    this.state.cursorCol = Math.min(targetCol, logicalLine.length);
    const segments = [...this.segment(logicalLine, "grapheme")];
    for (const seg of segments) {
      if (seg.index > this.state.cursorCol)
        break;
      if (seg.segment.length <= 1)
        continue;
      if (this.state.cursorCol < seg.index + seg.segment.length) {
        const isContinuation = seg.index < targetVL.startCol;
        const isMovingDown = targetVisualLine > currentVisualLine;
        if (isContinuation && isMovingDown) {
          const segEnd = seg.index + seg.segment.length;
          let next = targetVisualLine + 1;
          while (next < visualLines.length && visualLines[next].logicalLine === targetVL.logicalLine && visualLines[next].startCol < segEnd) {
            next++;
          }
          if (next < visualLines.length) {
            this.moveToVisualLine(visualLines, currentVisualLine, next);
            return;
          }
        }
        this.snappedFromCursorCol = this.state.cursorCol;
        this.state.cursorCol = seg.index;
        return;
      }
    }
    this.snappedFromCursorCol = null;
  }
  /**
   * Compute the target visual column for vertical cursor movement.
   * Implements the sticky column decision table:
   *
   * | P | S | T | U | Scenario                                             | Set Preferred | Move To     |
   * |---|---|---|---| ---------------------------------------------------- |---------------|-------------|
   * | 0 | * | 0 | - | Start nav, target fits                               | null          | current     |
   * | 0 | * | 1 | - | Start nav, target shorter                            | current       | target end  |
   * | 1 | 0 | 0 | 0 | Clamped, target fits preferred                       | null          | preferred   |
   * | 1 | 0 | 0 | 1 | Clamped, target longer but still can't fit preferred | keep          | target end  |
   * | 1 | 0 | 1 | - | Clamped, target even shorter                         | keep          | target end  |
   * | 1 | 1 | 0 | - | Rewrapped, target fits current                       | null          | current     |
   * | 1 | 1 | 1 | - | Rewrapped, target shorter than current               | current       | target end  |
   *
   * Where:
   * - P = preferred col is set
   * - S = cursor in middle of source line (not clamped to end)
   * - T = target line shorter than current visual col
   * - U = target line shorter than preferred col
   */
  computeVerticalMoveColumn(currentVisualCol, sourceMaxVisualCol, targetMaxVisualCol) {
    const hasPreferred = this.preferredVisualCol !== null;
    const cursorInMiddle = currentVisualCol < sourceMaxVisualCol;
    const targetTooShort = targetMaxVisualCol < currentVisualCol;
    if (!hasPreferred || cursorInMiddle) {
      if (targetTooShort) {
        this.preferredVisualCol = currentVisualCol;
        return targetMaxVisualCol;
      }
      this.preferredVisualCol = null;
      return currentVisualCol;
    }
    const targetCantFitPreferred = targetMaxVisualCol < this.preferredVisualCol;
    if (targetTooShort || targetCantFitPreferred) {
      return targetMaxVisualCol;
    }
    const result = this.preferredVisualCol;
    this.preferredVisualCol = null;
    return result;
  }
  moveToLineStart() {
    this.lastAction = null;
    this.setCursorCol(0);
  }
  moveToLineEnd() {
    this.lastAction = null;
    const currentLine = this.state.lines[this.state.cursorLine] || "";
    this.setCursorCol(currentLine.length);
  }
  deleteToStartOfLine() {
    this.exitHistoryBrowsing();
    const currentLine = this.state.lines[this.state.cursorLine] || "";
    if (this.state.cursorCol > 0) {
      this.pushUndoSnapshot();
      const deletedText = currentLine.slice(0, this.state.cursorCol);
      this.killRing.push(deletedText, { prepend: true, accumulate: this.lastAction === "kill" });
      this.lastAction = "kill";
      this.state.lines[this.state.cursorLine] = currentLine.slice(this.state.cursorCol);
      this.setCursorCol(0);
    } else if (this.state.cursorLine > 0) {
      this.pushUndoSnapshot();
      this.killRing.push("\n", { prepend: true, accumulate: this.lastAction === "kill" });
      this.lastAction = "kill";
      const previousLine = this.state.lines[this.state.cursorLine - 1] || "";
      this.state.lines[this.state.cursorLine - 1] = previousLine + currentLine;
      this.state.lines.splice(this.state.cursorLine, 1);
      this.state.cursorLine--;
      this.setCursorCol(previousLine.length);
    }
    if (this.onChange) {
      this.onChange(this.getText());
    }
  }
  deleteToEndOfLine() {
    this.exitHistoryBrowsing();
    const currentLine = this.state.lines[this.state.cursorLine] || "";
    if (this.state.cursorCol < currentLine.length) {
      this.pushUndoSnapshot();
      const deletedText = currentLine.slice(this.state.cursorCol);
      this.killRing.push(deletedText, { prepend: false, accumulate: this.lastAction === "kill" });
      this.lastAction = "kill";
      this.state.lines[this.state.cursorLine] = currentLine.slice(0, this.state.cursorCol);
    } else if (this.state.cursorLine < this.state.lines.length - 1) {
      this.pushUndoSnapshot();
      this.killRing.push("\n", { prepend: false, accumulate: this.lastAction === "kill" });
      this.lastAction = "kill";
      const nextLine = this.state.lines[this.state.cursorLine + 1] || "";
      this.state.lines[this.state.cursorLine] = currentLine + nextLine;
      this.state.lines.splice(this.state.cursorLine + 1, 1);
    }
    if (this.onChange) {
      this.onChange(this.getText());
    }
  }
  deleteWordBackwards() {
    this.exitHistoryBrowsing();
    const currentLine = this.state.lines[this.state.cursorLine] || "";
    if (this.state.cursorCol === 0) {
      if (this.state.cursorLine > 0) {
        this.pushUndoSnapshot();
        this.killRing.push("\n", { prepend: true, accumulate: this.lastAction === "kill" });
        this.lastAction = "kill";
        const previousLine = this.state.lines[this.state.cursorLine - 1] || "";
        this.state.lines[this.state.cursorLine - 1] = previousLine + currentLine;
        this.state.lines.splice(this.state.cursorLine, 1);
        this.state.cursorLine--;
        this.setCursorCol(previousLine.length);
      }
    } else {
      this.pushUndoSnapshot();
      const wasKill = this.lastAction === "kill";
      const oldCursorCol = this.state.cursorCol;
      this.moveWordBackwards();
      const deleteFrom = this.state.cursorCol;
      this.setCursorCol(oldCursorCol);
      const deletedText = currentLine.slice(deleteFrom, this.state.cursorCol);
      this.killRing.push(deletedText, { prepend: true, accumulate: wasKill });
      this.lastAction = "kill";
      this.state.lines[this.state.cursorLine] = currentLine.slice(0, deleteFrom) + currentLine.slice(this.state.cursorCol);
      this.setCursorCol(deleteFrom);
    }
    if (this.onChange) {
      this.onChange(this.getText());
    }
  }
  deleteWordForward() {
    this.exitHistoryBrowsing();
    const currentLine = this.state.lines[this.state.cursorLine] || "";
    if (this.state.cursorCol >= currentLine.length) {
      if (this.state.cursorLine < this.state.lines.length - 1) {
        this.pushUndoSnapshot();
        this.killRing.push("\n", { prepend: false, accumulate: this.lastAction === "kill" });
        this.lastAction = "kill";
        const nextLine = this.state.lines[this.state.cursorLine + 1] || "";
        this.state.lines[this.state.cursorLine] = currentLine + nextLine;
        this.state.lines.splice(this.state.cursorLine + 1, 1);
      }
    } else {
      this.pushUndoSnapshot();
      const wasKill = this.lastAction === "kill";
      const oldCursorCol = this.state.cursorCol;
      this.moveWordForwards();
      const deleteTo = this.state.cursorCol;
      this.setCursorCol(oldCursorCol);
      const deletedText = currentLine.slice(this.state.cursorCol, deleteTo);
      this.killRing.push(deletedText, { prepend: false, accumulate: wasKill });
      this.lastAction = "kill";
      this.state.lines[this.state.cursorLine] = currentLine.slice(0, this.state.cursorCol) + currentLine.slice(deleteTo);
    }
    if (this.onChange) {
      this.onChange(this.getText());
    }
  }
  handleForwardDelete() {
    this.exitHistoryBrowsing();
    this.lastAction = null;
    const currentLine = this.state.lines[this.state.cursorLine] || "";
    if (this.state.cursorCol < currentLine.length) {
      this.pushUndoSnapshot();
      const afterCursor = currentLine.slice(this.state.cursorCol);
      const graphemes = [...this.segment(afterCursor, "grapheme")];
      const firstGrapheme = graphemes[0];
      const graphemeLength = firstGrapheme ? firstGrapheme.segment.length : 1;
      const before = currentLine.slice(0, this.state.cursorCol);
      const after = currentLine.slice(this.state.cursorCol + graphemeLength);
      this.state.lines[this.state.cursorLine] = before + after;
    } else if (this.state.cursorLine < this.state.lines.length - 1) {
      this.pushUndoSnapshot();
      const nextLine = this.state.lines[this.state.cursorLine + 1] || "";
      this.state.lines[this.state.cursorLine] = currentLine + nextLine;
      this.state.lines.splice(this.state.cursorLine + 1, 1);
    }
    if (this.onChange) {
      this.onChange(this.getText());
    }
    if (this.autocompleteState) {
      this.updateAutocomplete();
    } else {
      const currentLine2 = this.state.lines[this.state.cursorLine] || "";
      const textBeforeCursor = currentLine2.slice(0, this.state.cursorCol);
      if (this.isInSlashCommandContext(textBeforeCursor)) {
        this.tryTriggerAutocomplete();
      } else if (this.autocompleteTriggerPattern.test(textBeforeCursor)) {
        this.tryTriggerAutocomplete();
      }
    }
  }
  /**
   * Build a mapping from visual lines to logical positions.
   * Returns an array where each element represents a visual line with:
   * - logicalLine: index into this.state.lines
   * - startCol: starting column in the logical line
   * - length: length of this visual line segment
   */
  buildVisualLineMap(width) {
    const visualLines = [];
    for (let i = 0; i < this.state.lines.length; i++) {
      const line = this.state.lines[i] || "";
      const lineVisWidth = visibleWidth4(line);
      if (line.length === 0) {
        visualLines.push({ logicalLine: i, startCol: 0, length: 0 });
      } else if (lineVisWidth <= width) {
        visualLines.push({ logicalLine: i, startCol: 0, length: line.length });
      } else {
        const chunks = wordWrapLine(line, width, [...this.segment(line, "grapheme")]);
        for (const chunk of chunks) {
          visualLines.push({
            logicalLine: i,
            startCol: chunk.startIndex,
            length: chunk.endIndex - chunk.startIndex
          });
        }
      }
    }
    return visualLines;
  }
  /**
   * Find the visual line index that contains the given logical position.
   */
  findVisualLineAt(visualLines, line, col) {
    for (let i = 0; i < visualLines.length; i++) {
      const vl = visualLines[i];
      if (!vl || vl.logicalLine !== line)
        continue;
      const offset = col - vl.startCol;
      const isLastSegmentOfLine = i === visualLines.length - 1 || visualLines[i + 1]?.logicalLine !== vl.logicalLine;
      if (offset >= 0 && (offset < vl.length || isLastSegmentOfLine && offset === vl.length)) {
        return i;
      }
    }
    return visualLines.length - 1;
  }
  /**
   * Find the visual line index for the current cursor position.
   */
  findCurrentVisualLine(visualLines) {
    return this.findVisualLineAt(visualLines, this.state.cursorLine, this.state.cursorCol);
  }
  moveCursor(deltaLine, deltaCol) {
    this.lastAction = null;
    const visualLines = this.buildVisualLineMap(this.lastWidth);
    const currentVisualLine = this.findCurrentVisualLine(visualLines);
    if (deltaLine !== 0) {
      const targetVisualLine = currentVisualLine + deltaLine;
      if (targetVisualLine >= 0 && targetVisualLine < visualLines.length) {
        this.moveToVisualLine(visualLines, currentVisualLine, targetVisualLine);
      }
    }
    if (deltaCol !== 0) {
      const currentLine = this.state.lines[this.state.cursorLine] || "";
      if (deltaCol > 0) {
        if (this.state.cursorCol < currentLine.length) {
          const afterCursor = currentLine.slice(this.state.cursorCol);
          const graphemes = [...this.segment(afterCursor, "grapheme")];
          const firstGrapheme = graphemes[0];
          this.setCursorCol(this.state.cursorCol + (firstGrapheme ? firstGrapheme.segment.length : 1));
        } else if (this.state.cursorLine < this.state.lines.length - 1) {
          this.state.cursorLine++;
          this.setCursorCol(0);
        } else {
          const currentVL = visualLines[currentVisualLine];
          if (currentVL) {
            this.preferredVisualCol = this.state.cursorCol - currentVL.startCol;
          }
        }
      } else {
        if (this.state.cursorCol > 0) {
          const beforeCursor = currentLine.slice(0, this.state.cursorCol);
          const graphemes = [...this.segment(beforeCursor, "grapheme")];
          const lastGrapheme = graphemes[graphemes.length - 1];
          this.setCursorCol(this.state.cursorCol - (lastGrapheme ? lastGrapheme.segment.length : 1));
        } else if (this.state.cursorLine > 0) {
          this.state.cursorLine--;
          const prevLine = this.state.lines[this.state.cursorLine] || "";
          this.setCursorCol(prevLine.length);
        }
      }
    }
    if (this.autocompleteState) {
      this.updateAutocomplete();
    }
  }
  /**
   * Scroll by a page (direction: -1 for up, 1 for down).
   * Moves cursor by the page size while keeping it in bounds.
   */
  pageScroll(direction) {
    this.lastAction = null;
    const terminalRows = this.tui.terminal.rows;
    const pageSize = Math.max(5, Math.floor(terminalRows * 0.3));
    const visualLines = this.buildVisualLineMap(this.lastWidth);
    const currentVisualLine = this.findCurrentVisualLine(visualLines);
    const targetVisualLine = Math.max(0, Math.min(visualLines.length - 1, currentVisualLine + direction * pageSize));
    this.moveToVisualLine(visualLines, currentVisualLine, targetVisualLine);
  }
  moveWordBackwards() {
    this.lastAction = null;
    const currentLine = this.state.lines[this.state.cursorLine] || "";
    if (this.state.cursorCol === 0) {
      if (this.state.cursorLine > 0) {
        this.state.cursorLine--;
        const prevLine = this.state.lines[this.state.cursorLine] || "";
        this.setCursorCol(prevLine.length);
      }
      return;
    }
    this.setCursorCol(findWordBackward(currentLine, this.state.cursorCol, {
      segment: /* @__PURE__ */ __name((text) => this.segment(text, "word"), "segment"),
      isAtomicSegment: isPasteMarker
    }));
  }
  /**
   * Yank (paste) the most recent kill ring entry at cursor position.
   */
  yank() {
    if (this.killRing.length === 0)
      return;
    this.pushUndoSnapshot();
    const text = this.killRing.peek();
    this.insertYankedText(text);
    this.lastAction = "yank";
  }
  /**
   * Cycle through kill ring (only works immediately after yank or yank-pop).
   * Replaces the last yanked text with the previous entry in the ring.
   */
  yankPop() {
    if (this.lastAction !== "yank" || this.killRing.length <= 1)
      return;
    this.pushUndoSnapshot();
    this.deleteYankedText();
    this.killRing.rotate();
    const text = this.killRing.peek();
    this.insertYankedText(text);
    this.lastAction = "yank";
  }
  /**
   * Insert text at cursor position (used by yank operations).
   */
  insertYankedText(text) {
    this.exitHistoryBrowsing();
    const lines = text.split("\n");
    if (lines.length === 1) {
      const currentLine = this.state.lines[this.state.cursorLine] || "";
      const before = currentLine.slice(0, this.state.cursorCol);
      const after = currentLine.slice(this.state.cursorCol);
      this.state.lines[this.state.cursorLine] = before + text + after;
      this.setCursorCol(this.state.cursorCol + text.length);
    } else {
      const currentLine = this.state.lines[this.state.cursorLine] || "";
      const before = currentLine.slice(0, this.state.cursorCol);
      const after = currentLine.slice(this.state.cursorCol);
      this.state.lines[this.state.cursorLine] = before + (lines[0] || "");
      for (let i = 1; i < lines.length - 1; i++) {
        this.state.lines.splice(this.state.cursorLine + i, 0, lines[i] || "");
      }
      const lastLineIndex = this.state.cursorLine + lines.length - 1;
      this.state.lines.splice(lastLineIndex, 0, (lines[lines.length - 1] || "") + after);
      this.state.cursorLine = lastLineIndex;
      this.setCursorCol((lines[lines.length - 1] || "").length);
    }
    if (this.onChange) {
      this.onChange(this.getText());
    }
  }
  /**
   * Delete the previously yanked text (used by yank-pop).
   * The yanked text is derived from killRing[end] since it hasn't been rotated yet.
   */
  deleteYankedText() {
    const yankedText = this.killRing.peek();
    if (!yankedText)
      return;
    const yankLines = yankedText.split("\n");
    if (yankLines.length === 1) {
      const currentLine = this.state.lines[this.state.cursorLine] || "";
      const deleteLen = yankedText.length;
      const before = currentLine.slice(0, this.state.cursorCol - deleteLen);
      const after = currentLine.slice(this.state.cursorCol);
      this.state.lines[this.state.cursorLine] = before + after;
      this.setCursorCol(this.state.cursorCol - deleteLen);
    } else {
      const startLine = this.state.cursorLine - (yankLines.length - 1);
      const startCol = (this.state.lines[startLine] || "").length - (yankLines[0] || "").length;
      const afterCursor = (this.state.lines[this.state.cursorLine] || "").slice(this.state.cursorCol);
      const beforeYank = (this.state.lines[startLine] || "").slice(0, startCol);
      this.state.lines.splice(startLine, yankLines.length, beforeYank + afterCursor);
      this.state.cursorLine = startLine;
      this.setCursorCol(startCol);
    }
    if (this.onChange) {
      this.onChange(this.getText());
    }
  }
  pushUndoSnapshot() {
    this.undoStack.push({ state: this.state, pastes: this.pastes, pasteCounter: this.pasteCounter });
  }
  undo() {
    this.exitHistoryBrowsing();
    const snapshot = this.undoStack.pop();
    if (!snapshot)
      return;
    Object.assign(this.state, snapshot.state);
    this.pastes = snapshot.pastes;
    this.pasteCounter = snapshot.pasteCounter;
    this.lastAction = null;
    this.preferredVisualCol = null;
    if (this.onChange) {
      this.onChange(this.getText());
    }
  }
  /**
   * Jump to the first occurrence of a character in the specified direction.
   * Multi-line search. Case-sensitive. Skips the current cursor position.
   */
  jumpToChar(char, direction) {
    this.lastAction = null;
    const isForward = direction === "forward";
    const lines = this.state.lines;
    const end = isForward ? lines.length : -1;
    const step = isForward ? 1 : -1;
    for (let lineIdx = this.state.cursorLine; lineIdx !== end; lineIdx += step) {
      const line = lines[lineIdx] || "";
      const isCurrentLine = lineIdx === this.state.cursorLine;
      const searchFrom = isCurrentLine ? isForward ? this.state.cursorCol + 1 : this.state.cursorCol - 1 : void 0;
      const idx = isForward ? line.indexOf(char, searchFrom) : line.lastIndexOf(char, searchFrom);
      if (idx !== -1) {
        this.state.cursorLine = lineIdx;
        this.setCursorCol(idx);
        return;
      }
    }
  }
  moveWordForwards() {
    this.lastAction = null;
    const currentLine = this.state.lines[this.state.cursorLine] || "";
    if (this.state.cursorCol >= currentLine.length) {
      if (this.state.cursorLine < this.state.lines.length - 1) {
        this.state.cursorLine++;
        this.setCursorCol(0);
      }
      return;
    }
    this.setCursorCol(findWordForward(currentLine, this.state.cursorCol, {
      segment: /* @__PURE__ */ __name((text) => this.segment(text, "word"), "segment"),
      isAtomicSegment: isPasteMarker
    }));
  }
  // Slash menu only allowed on the first line of the editor
  isSlashMenuAllowed() {
    return this.state.cursorLine === 0;
  }
  // Helper method to check if cursor is at start of message (for slash command detection)
  isAtStartOfMessage() {
    if (!this.isSlashMenuAllowed())
      return false;
    const currentLine = this.state.lines[this.state.cursorLine] || "";
    const beforeCursor = currentLine.slice(0, this.state.cursorCol);
    return beforeCursor.trim() === "" || beforeCursor.trim() === "/";
  }
  isInSlashCommandContext(textBeforeCursor) {
    return this.isSlashMenuAllowed() && textBeforeCursor.trimStart().startsWith("/");
  }
  // Autocomplete methods
  /**
   * Find the best autocomplete item index for the given prefix.
   * Returns -1 if no match is found.
   *
   * Match priority:
   * 1. Exact match (prefix === item.value) -> always selected
   * 2. Prefix match -> first item whose value starts with prefix
   * 3. No match -> -1 (keep default highlight)
   *
   * Matching is case-sensitive and checks item.value only.
   */
  getBestAutocompleteMatchIndex(items, prefix) {
    if (!prefix)
      return -1;
    let firstPrefixIndex = -1;
    for (let i = 0; i < items.length; i++) {
      const value = items[i].value;
      if (value === prefix) {
        return i;
      }
      if (firstPrefixIndex === -1 && value.startsWith(prefix)) {
        firstPrefixIndex = i;
      }
    }
    return firstPrefixIndex;
  }
  createAutocompleteList(prefix, items) {
    const layout = prefix.startsWith("/") ? SLASH_COMMAND_SELECT_LIST_LAYOUT : void 0;
    const list = new SelectList(items, this.autocompleteMaxVisible, this.theme.selectList, layout);
    list.onSelect = (selected) => {
      if (!this.autocompleteProvider)
        return;
      this.pushUndoSnapshot();
      this.lastAction = null;
      const result = this.autocompleteProvider.applyCompletion(this.state.lines, this.state.cursorLine, this.state.cursorCol, selected, this.autocompletePrefix);
      this.state.lines = result.lines;
      this.state.cursorLine = result.cursorLine;
      this.setCursorCol(result.cursorCol);
      this.cancelAutocomplete();
      this.onChange?.(this.getText());
    };
    return list;
  }
  tryTriggerAutocomplete(explicitTab = false) {
    this.requestAutocomplete({ force: false, explicitTab });
  }
  handleTabCompletion() {
    if (!this.autocompleteProvider)
      return;
    const currentLine = this.state.lines[this.state.cursorLine] || "";
    const beforeCursor = currentLine.slice(0, this.state.cursorCol);
    if (this.isInSlashCommandContext(beforeCursor) && !beforeCursor.trimStart().includes(" ")) {
      this.handleSlashCommandCompletion();
    } else {
      this.forceFileAutocomplete(true);
    }
  }
  handleSlashCommandCompletion() {
    this.requestAutocomplete({ force: false, explicitTab: true });
  }
  forceFileAutocomplete(explicitTab = false) {
    this.requestAutocomplete({ force: true, explicitTab });
  }
  requestAutocomplete(options) {
    if (!this.autocompleteProvider)
      return;
    if (options.force) {
      const shouldTrigger = !this.autocompleteProvider.shouldTriggerFileCompletion || this.autocompleteProvider.shouldTriggerFileCompletion(this.state.lines, this.state.cursorLine, this.state.cursorCol);
      if (!shouldTrigger) {
        return;
      }
    }
    this.cancelAutocompleteRequest();
    const startToken = ++this.autocompleteStartToken;
    const debounceMs = this.getAutocompleteDebounceMs(options);
    if (debounceMs > 0) {
      this.autocompleteDebounceTimer = setTimeout(() => {
        this.autocompleteDebounceTimer = void 0;
        void this.startAutocompleteRequest(startToken, options);
      }, debounceMs);
      return;
    }
    void this.startAutocompleteRequest(startToken, options);
  }
  async startAutocompleteRequest(startToken, options) {
    const previousTask = this.autocompleteRequestTask;
    this.autocompleteRequestTask = (async () => {
      await previousTask;
      if (startToken !== this.autocompleteStartToken || !this.autocompleteProvider) {
        return;
      }
      const controller = new AbortController();
      this.autocompleteAbort = controller;
      const requestId = ++this.autocompleteRequestId;
      const snapshotText = this.getText();
      const snapshotLine = this.state.cursorLine;
      const snapshotCol = this.state.cursorCol;
      await this.runAutocompleteRequest(requestId, controller, snapshotText, snapshotLine, snapshotCol, options);
    })();
    await this.autocompleteRequestTask;
  }
  setAutocompleteTriggerCharacters(triggerCharacters) {
    const next = [...DEFAULT_AUTOCOMPLETE_TRIGGER_CHARACTERS];
    for (const character of triggerCharacters) {
      if (character.length !== 1 || character === "/" || isWhitespaceChar2(character) || next.includes(character)) {
        continue;
      }
      next.push(character);
    }
    this.autocompleteTriggerCharacters = next;
    this.autocompleteTriggerPattern = buildTriggerPattern(next);
    this.autocompleteDebouncePattern = buildDebouncePattern(next);
  }
  getAutocompleteDebounceMs(options) {
    if (options.explicitTab || options.force) {
      return 0;
    }
    const currentLine = this.state.lines[this.state.cursorLine] || "";
    const textBeforeCursor = currentLine.slice(0, this.state.cursorCol);
    return this.autocompleteDebouncePattern.test(textBeforeCursor) ? ATTACHMENT_AUTOCOMPLETE_DEBOUNCE_MS : 0;
  }
  async runAutocompleteRequest(requestId, controller, snapshotText, snapshotLine, snapshotCol, options) {
    if (!this.autocompleteProvider)
      return;
    const suggestions = await this.autocompleteProvider.getSuggestions(this.state.lines, this.state.cursorLine, this.state.cursorCol, { signal: controller.signal, force: options.force });
    if (!this.isAutocompleteRequestCurrent(requestId, controller, snapshotText, snapshotLine, snapshotCol)) {
      return;
    }
    this.autocompleteAbort = void 0;
    if (!suggestions || !Array.isArray(suggestions.items) || suggestions.items.length === 0) {
      this.cancelAutocomplete();
      this.tui.requestRender();
      return;
    }
    if (options.force && options.explicitTab && suggestions.items.length === 1) {
      const item = suggestions.items[0];
      this.pushUndoSnapshot();
      this.lastAction = null;
      const result = this.autocompleteProvider.applyCompletion(this.state.lines, this.state.cursorLine, this.state.cursorCol, item, suggestions.prefix);
      this.state.lines = result.lines;
      this.state.cursorLine = result.cursorLine;
      this.setCursorCol(result.cursorCol);
      if (this.onChange)
        this.onChange(this.getText());
      this.tui.requestRender();
      return;
    }
    this.applyAutocompleteSuggestions(suggestions, options.force ? "force" : "regular");
    this.tui.requestRender();
  }
  isAutocompleteRequestCurrent(requestId, controller, snapshotText, snapshotLine, snapshotCol) {
    return !controller.signal.aborted && requestId === this.autocompleteRequestId && this.getText() === snapshotText && this.state.cursorLine === snapshotLine && this.state.cursorCol === snapshotCol;
  }
  applyAutocompleteSuggestions(suggestions, state) {
    this.autocompletePrefix = suggestions.prefix;
    this.autocompleteList = this.createAutocompleteList(suggestions.prefix, suggestions.items);
    const bestMatchIndex = this.getBestAutocompleteMatchIndex(suggestions.items, suggestions.prefix);
    if (bestMatchIndex >= 0) {
      this.autocompleteList.setSelectedIndex(bestMatchIndex);
    }
    this.autocompleteState = state;
  }
  cancelAutocompleteRequest() {
    this.autocompleteStartToken += 1;
    if (this.autocompleteDebounceTimer) {
      clearTimeout(this.autocompleteDebounceTimer);
      this.autocompleteDebounceTimer = void 0;
    }
    this.autocompleteAbort?.abort();
    this.autocompleteAbort = void 0;
  }
  clearAutocompleteUi() {
    this.autocompleteState = null;
    this.autocompleteList = void 0;
    this.autocompletePrefix = "";
  }
  cancelAutocomplete() {
    this.cancelAutocompleteRequest();
    this.clearAutocompleteUi();
  }
  isShowingAutocomplete() {
    return this.autocompleteState !== null;
  }
  updateAutocomplete() {
    if (!this.autocompleteState || !this.autocompleteProvider)
      return;
    this.requestAutocomplete({ force: this.autocompleteState === "force", explicitTab: false });
  }
};

// pi-dist/pi-tui/components/h-stack.js
import { compositeTuiLine } from "../tui.js";
import { visibleWidth as visibleWidth5 } from "../utils.js";

// pi-dist/pi-tui/layout-node.js
var LAYOUT_NODE = /* @__PURE__ */ Symbol.for("@earendil-works/pi-tui/layout-node");
function getLayoutNode(component) {
  const candidate = component;
  return typeof candidate[LAYOUT_NODE] === "function" ? candidate[LAYOUT_NODE]() : void 0;
}
__name(getLayoutNode, "getLayoutNode");

// pi-dist/pi-tui/components/stack.js
import { Container } from "../tui.js";
function isStackEntry(child) {
  return !("render" in child);
}
__name(isStackEntry, "isStackEntry");
function normalizeSize(value, fallback) {
  return value === void 0 || !Number.isFinite(value) ? fallback : Math.max(0, Math.floor(value));
}
__name(normalizeSize, "normalizeSize");
var Stack = class extends Container {
  static {
    __name(this, "Stack");
  }
  entries = [];
  gap;
  align;
  constructor(children = [], options = {}) {
    super();
    this.gap = normalizeSize(options.gap, 0);
    this.align = options.align ?? "stretch";
    for (const child of children) {
      if (isStackEntry(child))
        this.addChild(child.component, child);
      else
        this.addChild(child);
    }
  }
  addChild(component, options = {}) {
    super.addChild(component);
    this.entries.push({
      component,
      ...options.basis === void 0 ? {} : { basis: options.basis },
      ...options.grow === void 0 ? {} : { grow: normalizeSize(options.grow, 0) },
      ...options.shrink === void 0 ? {} : { shrink: normalizeSize(options.shrink, 1) },
      ...options.minSize === void 0 ? {} : { minSize: normalizeSize(options.minSize, 0) },
      ...options.maxSize === void 0 ? {} : { maxSize: normalizeSize(options.maxSize, Number.MAX_SAFE_INTEGER) },
      ...options.visible === void 0 ? {} : { visible: options.visible }
    });
  }
  removeChild(component) {
    super.removeChild(component);
    const index = this.entries.findIndex((entry) => entry.component === component);
    if (index !== -1)
      this.entries.splice(index, 1);
  }
  clear() {
    super.clear();
    this.entries.length = 0;
  }
  [LAYOUT_NODE]() {
    return {
      type: this.layoutType,
      entries: this.entries,
      gap: this.gap,
      align: this.align
    };
  }
};
function visibleStackEntries(entries, viewport) {
  return entries.filter((entry) => entry.visible?.(viewport) ?? true);
}
__name(visibleStackEntries, "visibleStackEntries");
function clampSize(size, entry) {
  const min = Math.max(0, Math.floor(entry.minSize ?? 0));
  const max = Math.max(min, Math.floor(entry.maxSize ?? Number.MAX_SAFE_INTEGER));
  return Math.max(min, Math.min(max, Math.max(0, Math.floor(size))));
}
__name(clampSize, "clampSize");
function distribute(sizes, entries, amount, mode) {
  let remaining = amount;
  while (remaining > 0) {
    const candidates = entries.map((entry, index) => ({ entry, index })).filter(({ entry, index }) => {
      if (mode === "grow") {
        return (entry.grow ?? 0) > 0 && sizes[index] < (entry.maxSize ?? Number.MAX_SAFE_INTEGER);
      }
      return (entry.shrink ?? 1) > 0 && sizes[index] > (entry.minSize ?? 0);
    });
    if (candidates.length === 0)
      return;
    const totalWeight = candidates.reduce((sum, { entry, index }) => {
      return sum + (mode === "grow" ? entry.grow ?? 0 : (entry.shrink ?? 1) * Math.max(1, sizes[index]));
    }, 0);
    let distributed = 0;
    for (const { entry, index } of candidates) {
      if (remaining <= 0)
        break;
      const weight = mode === "grow" ? entry.grow ?? 0 : (entry.shrink ?? 1) * Math.max(1, sizes[index]);
      const proposed = Math.max(1, Math.floor(remaining * weight / totalWeight));
      const capacity = mode === "grow" ? (entry.maxSize ?? Number.MAX_SAFE_INTEGER) - sizes[index] : sizes[index] - (entry.minSize ?? 0);
      const delta = Math.min(remaining, proposed, capacity);
      if (delta <= 0)
        continue;
      sizes[index] = sizes[index] + (mode === "grow" ? delta : -delta);
      remaining -= delta;
      distributed += delta;
    }
    if (distributed === 0)
      return;
  }
}
__name(distribute, "distribute");
function allocateStackSizes(entries, intrinsicSizes, availableSize, gap) {
  const sizes = entries.map((entry, index) => clampSize(entry.basis === void 0 || entry.basis === "auto" ? intrinsicSizes[index] ?? 0 : entry.basis, entry));
  if (availableSize === void 0)
    return sizes;
  const contentSize = Math.max(0, Math.floor(availableSize) - Math.max(0, entries.length - 1) * gap);
  const total = sizes.reduce((sum, size) => sum + size, 0);
  if (total < contentSize)
    distribute(sizes, entries, contentSize - total, "grow");
  else if (total > contentSize)
    distribute(sizes, entries, total - contentSize, "shrink");
  return sizes;
}
__name(allocateStackSizes, "allocateStackSizes");

// pi-dist/pi-tui/components/h-stack.js
var HStack = class extends Stack {
  static {
    __name(this, "HStack");
  }
  layoutType = "hstack";
  constructor(children = [], options = {}) {
    super(children, options);
  }
  render(width) {
    const safeWidth = Math.max(1, width);
    const viewport = { width: safeWidth, height: Number.MAX_SAFE_INTEGER };
    const entries = visibleStackEntries(this.entries, viewport);
    if (entries.length === 0)
      return [];
    const intrinsicWidths = entries.map((entry) => {
      const lines = entry.component.render(safeWidth);
      return lines.reduce((max, line) => Math.max(max, visibleWidth5(line)), 0);
    });
    const widths = allocateStackSizes(entries, intrinsicWidths, safeWidth, this.gap);
    const rendered = entries.map((entry, index) => widths[index] === 0 ? [] : entry.component.render(widths[index]));
    const height = rendered.reduce((max, lines) => Math.max(max, lines.length), 0);
    const result = Array.from({ length: height }, () => "");
    let x = 0;
    for (let index = 0; index < rendered.length; index++) {
      const lines = rendered[index];
      const childWidth = widths[index];
      let offset = 0;
      if (this.align === "center")
        offset = Math.floor((height - lines.length) / 2);
      else if (this.align === "end")
        offset = height - lines.length;
      for (let row = 0; row < lines.length; row++) {
        const target = row + offset;
        if (target < 0 || target >= result.length)
          continue;
        result[target] = compositeTuiLine(result[target], lines[row], x, childWidth, safeWidth);
      }
      x += childWidth + this.gap;
    }
    return result;
  }
};

// pi-dist/pi-tui/components/image.js
import { allocateImageId, getCapabilities, getCellDimensions, getImageDimensions, imageFallback, renderImage } from "../terminal-image.js";
import { truncateToWidth as truncateToWidth2 } from "../utils.js";
var Image = class {
  static {
    __name(this, "Image");
  }
  base64Data;
  mimeType;
  dimensions;
  theme;
  options;
  imageId;
  cachedLines;
  cachedWidth;
  constructor(base64Data, mimeType, theme, options = {}, dimensions) {
    this.base64Data = base64Data;
    this.mimeType = mimeType;
    this.theme = theme;
    this.options = options;
    this.dimensions = dimensions || getImageDimensions(base64Data, mimeType) || { widthPx: 800, heightPx: 600 };
    this.imageId = options.imageId;
  }
  /** Get the Kitty image ID used by this image (if any). */
  getImageId() {
    return this.imageId;
  }
  invalidate() {
    this.cachedLines = void 0;
    this.cachedWidth = void 0;
  }
  render(width) {
    if (this.cachedLines && this.cachedWidth === width) {
      return this.cachedLines;
    }
    const maxWidth = Math.max(1, Math.min(width - 2, this.options.maxWidthCells ?? 60));
    const cellDimensions = getCellDimensions();
    const defaultMaxHeight = Math.max(1, Math.ceil(maxWidth * cellDimensions.widthPx / cellDimensions.heightPx));
    const maxHeight = this.options.maxHeightCells ?? defaultMaxHeight;
    const caps = getCapabilities();
    let lines;
    if (caps.images) {
      if (caps.images === "kitty" && this.imageId === void 0) {
        this.imageId = allocateImageId();
      }
      const result = renderImage(this.base64Data, this.dimensions, {
        maxWidthCells: maxWidth,
        maxHeightCells: maxHeight,
        imageId: this.imageId,
        moveCursor: false
      });
      if (result) {
        if (result.imageId) {
          this.imageId = result.imageId;
        }
        if (caps.images === "kitty") {
          lines = [result.sequence];
          for (let i = 0; i < result.rows - 1; i++) {
            lines.push("");
          }
        } else {
          lines = [];
          for (let i = 0; i < result.rows - 1; i++) {
            lines.push("");
          }
          const rowOffset = result.rows - 1;
          const moveUp = rowOffset > 0 ? `\x1B[${rowOffset}A` : "";
          lines.push(moveUp + result.sequence);
        }
      } else {
        const fallback = imageFallback(this.mimeType, this.dimensions, this.options.filename);
        lines = [truncateToWidth2(this.theme.fallbackColor(fallback), width)];
      }
    } else {
      const fallback = imageFallback(this.mimeType, this.dimensions, this.options.filename);
      lines = [truncateToWidth2(this.theme.fallbackColor(fallback), width)];
    }
    this.cachedLines = lines;
    this.cachedWidth = width;
    return lines;
  }
};

// pi-dist/pi-tui/components/input.js
import { getKeybindings as getKeybindings4 } from "../keybindings.js";
import { decodeKittyPrintable } from "../keys.js";
import { CURSOR_MARKER as CURSOR_MARKER2 } from "../tui.js";
import { getGraphemeSegmenter as getGraphemeSegmenter2, isWhitespaceChar as isWhitespaceChar3, sliceByColumn as sliceByColumn2, truncateToWidth as truncateToWidth3, visibleWidth as visibleWidth6 } from "../utils.js";
import { graphemeSegmenter as segmenter } from "../../../pi-tui-segmenters.mjs";
var Input = class {
  static {
    __name(this, "Input");
  }
  value = "";
  cursor = 0;
  // Cursor position in the value
  prompt;
  placeholder;
  placeholderStyle;
  renderedStartColumn = 0;
  onSubmit;
  onEscape;
  /** Focusable interface - set by TUI when focus changes */
  focused = false;
  // Bracketed paste mode buffering
  pasteBuffer = "";
  isInPaste = false;
  // Kill ring for Emacs-style kill/yank operations
  killRing = new KillRing();
  lastAction = null;
  // Undo support
  undoStack = new UndoStack();
  constructor(options = {}) {
    this.prompt = options.prompt ?? "> ";
    this.placeholder = options.placeholder ?? "";
    this.placeholderStyle = options.placeholderStyle ?? ((text) => text);
  }
  getValue() {
    return this.value;
  }
  setValue(value) {
    this.value = value;
    this.cursor = Math.min(this.cursor, value.length);
  }
  handleInput(data) {
    if (data.includes("\x1B[200~")) {
      this.isInPaste = true;
      this.pasteBuffer = "";
      data = data.replace("\x1B[200~", "");
    }
    if (this.isInPaste) {
      this.pasteBuffer += data;
      const endIndex = this.pasteBuffer.indexOf("\x1B[201~");
      if (endIndex !== -1) {
        const pasteContent = this.pasteBuffer.substring(0, endIndex);
        this.handlePaste(pasteContent);
        this.isInPaste = false;
        const remaining = this.pasteBuffer.substring(endIndex + 6);
        this.pasteBuffer = "";
        if (remaining) {
          this.handleInput(remaining);
        }
      }
      return;
    }
    const kb = getKeybindings4();
    if (kb.matches(data, "tui.select.cancel")) {
      if (this.onEscape)
        this.onEscape();
      return;
    }
    if (kb.matches(data, "tui.editor.undo")) {
      this.undo();
      return;
    }
    if (kb.matches(data, "tui.input.submit") || data === "\n") {
      if (this.onSubmit)
        this.onSubmit(this.value);
      return;
    }
    if (kb.matches(data, "tui.editor.deleteCharBackward")) {
      this.handleBackspace();
      return;
    }
    if (kb.matches(data, "tui.editor.deleteCharForward")) {
      this.handleForwardDelete();
      return;
    }
    if (kb.matches(data, "tui.editor.deleteWordBackward")) {
      this.deleteWordBackwards();
      return;
    }
    if (kb.matches(data, "tui.editor.deleteWordForward")) {
      this.deleteWordForward();
      return;
    }
    if (kb.matches(data, "tui.editor.deleteToLineStart")) {
      this.deleteToLineStart();
      return;
    }
    if (kb.matches(data, "tui.editor.deleteToLineEnd")) {
      this.deleteToLineEnd();
      return;
    }
    if (kb.matches(data, "tui.editor.yank")) {
      this.yank();
      return;
    }
    if (kb.matches(data, "tui.editor.yankPop")) {
      this.yankPop();
      return;
    }
    if (kb.matches(data, "tui.editor.cursorLeft")) {
      this.lastAction = null;
      if (this.cursor > 0) {
        const beforeCursor = this.value.slice(0, this.cursor);
        const graphemes = [...segmenter.segment(beforeCursor)];
        const lastGrapheme = graphemes[graphemes.length - 1];
        this.cursor -= lastGrapheme ? lastGrapheme.segment.length : 1;
      }
      return;
    }
    if (kb.matches(data, "tui.editor.cursorRight")) {
      this.lastAction = null;
      if (this.cursor < this.value.length) {
        const afterCursor = this.value.slice(this.cursor);
        const graphemes = [...segmenter.segment(afterCursor)];
        const firstGrapheme = graphemes[0];
        this.cursor += firstGrapheme ? firstGrapheme.segment.length : 1;
      }
      return;
    }
    if (kb.matches(data, "tui.editor.cursorLineStart")) {
      this.lastAction = null;
      this.cursor = 0;
      return;
    }
    if (kb.matches(data, "tui.editor.cursorLineEnd")) {
      this.lastAction = null;
      this.cursor = this.value.length;
      return;
    }
    if (kb.matches(data, "tui.editor.cursorWordLeft")) {
      this.moveWordBackwards();
      return;
    }
    if (kb.matches(data, "tui.editor.cursorWordRight")) {
      this.moveWordForwards();
      return;
    }
    const kittyPrintable = decodeKittyPrintable(data);
    if (kittyPrintable !== void 0) {
      this.insertCharacter(kittyPrintable);
      return;
    }
    const hasControlChars = [...data].some((ch) => {
      const code = ch.charCodeAt(0);
      return code < 32 || code === 127 || code >= 128 && code <= 159;
    });
    if (!hasControlChars) {
      this.insertCharacter(data);
    }
  }
  handleMouse(event) {
    if (event.type !== "press" || event.button !== "left" || event.y !== 0)
      return void 0;
    const visibleColumn = Math.max(0, event.x - 2);
    const targetColumn = this.renderedStartColumn + visibleColumn;
    let currentColumn = 0;
    this.cursor = this.value.length;
    for (const grapheme of segmenter.segment(this.value)) {
      const nextColumn = currentColumn + visibleWidth6(grapheme.segment);
      if (targetColumn < nextColumn) {
        this.cursor = grapheme.index;
        break;
      }
      currentColumn = nextColumn;
    }
    this.lastAction = null;
    return { handled: true, focus: true };
  }
  insertCharacter(char) {
    if (isWhitespaceChar3(char) || this.lastAction !== "type-word") {
      this.pushUndo();
    }
    this.lastAction = "type-word";
    this.value = this.value.slice(0, this.cursor) + char + this.value.slice(this.cursor);
    this.cursor += char.length;
  }
  handleBackspace() {
    this.lastAction = null;
    if (this.cursor > 0) {
      this.pushUndo();
      const beforeCursor = this.value.slice(0, this.cursor);
      const graphemes = [...segmenter.segment(beforeCursor)];
      const lastGrapheme = graphemes[graphemes.length - 1];
      const graphemeLength = lastGrapheme ? lastGrapheme.segment.length : 1;
      this.value = this.value.slice(0, this.cursor - graphemeLength) + this.value.slice(this.cursor);
      this.cursor -= graphemeLength;
    }
  }
  handleForwardDelete() {
    this.lastAction = null;
    if (this.cursor < this.value.length) {
      this.pushUndo();
      const afterCursor = this.value.slice(this.cursor);
      const graphemes = [...segmenter.segment(afterCursor)];
      const firstGrapheme = graphemes[0];
      const graphemeLength = firstGrapheme ? firstGrapheme.segment.length : 1;
      this.value = this.value.slice(0, this.cursor) + this.value.slice(this.cursor + graphemeLength);
    }
  }
  deleteToLineStart() {
    if (this.cursor === 0)
      return;
    this.pushUndo();
    const deletedText = this.value.slice(0, this.cursor);
    this.killRing.push(deletedText, { prepend: true, accumulate: this.lastAction === "kill" });
    this.lastAction = "kill";
    this.value = this.value.slice(this.cursor);
    this.cursor = 0;
  }
  deleteToLineEnd() {
    if (this.cursor >= this.value.length)
      return;
    this.pushUndo();
    const deletedText = this.value.slice(this.cursor);
    this.killRing.push(deletedText, { prepend: false, accumulate: this.lastAction === "kill" });
    this.lastAction = "kill";
    this.value = this.value.slice(0, this.cursor);
  }
  deleteWordBackwards() {
    if (this.cursor === 0)
      return;
    const wasKill = this.lastAction === "kill";
    this.pushUndo();
    const oldCursor = this.cursor;
    this.moveWordBackwards();
    const deleteFrom = this.cursor;
    this.cursor = oldCursor;
    const deletedText = this.value.slice(deleteFrom, this.cursor);
    this.killRing.push(deletedText, { prepend: true, accumulate: wasKill });
    this.lastAction = "kill";
    this.value = this.value.slice(0, deleteFrom) + this.value.slice(this.cursor);
    this.cursor = deleteFrom;
  }
  deleteWordForward() {
    if (this.cursor >= this.value.length)
      return;
    const wasKill = this.lastAction === "kill";
    this.pushUndo();
    const oldCursor = this.cursor;
    this.moveWordForwards();
    const deleteTo = this.cursor;
    this.cursor = oldCursor;
    const deletedText = this.value.slice(this.cursor, deleteTo);
    this.killRing.push(deletedText, { prepend: false, accumulate: wasKill });
    this.lastAction = "kill";
    this.value = this.value.slice(0, this.cursor) + this.value.slice(deleteTo);
  }
  yank() {
    const text = this.killRing.peek();
    if (!text)
      return;
    this.pushUndo();
    this.value = this.value.slice(0, this.cursor) + text + this.value.slice(this.cursor);
    this.cursor += text.length;
    this.lastAction = "yank";
  }
  yankPop() {
    if (this.lastAction !== "yank" || this.killRing.length <= 1)
      return;
    this.pushUndo();
    const prevText = this.killRing.peek() || "";
    this.value = this.value.slice(0, this.cursor - prevText.length) + this.value.slice(this.cursor);
    this.cursor -= prevText.length;
    this.killRing.rotate();
    const text = this.killRing.peek() || "";
    this.value = this.value.slice(0, this.cursor) + text + this.value.slice(this.cursor);
    this.cursor += text.length;
    this.lastAction = "yank";
  }
  pushUndo() {
    this.undoStack.push({ value: this.value, cursor: this.cursor });
  }
  undo() {
    const snapshot = this.undoStack.pop();
    if (!snapshot)
      return;
    this.value = snapshot.value;
    this.cursor = snapshot.cursor;
    this.lastAction = null;
  }
  moveWordBackwards() {
    if (this.cursor === 0)
      return;
    this.lastAction = null;
    this.cursor = findWordBackward(this.value, this.cursor);
  }
  moveWordForwards() {
    if (this.cursor >= this.value.length)
      return;
    this.lastAction = null;
    this.cursor = findWordForward(this.value, this.cursor);
  }
  handlePaste(pastedText) {
    this.lastAction = null;
    this.pushUndo();
    const cleanText = pastedText.replace(/\r\n/g, "").replace(/\r/g, "").replace(/\n/g, "").replace(/\t/g, "    ");
    this.value = this.value.slice(0, this.cursor) + cleanText + this.value.slice(this.cursor);
    this.cursor += cleanText.length;
  }
  invalidate() {
  }
  render(width) {
    const availableWidth = width - visibleWidth6(this.prompt);
    if (availableWidth <= 0) {
      return [truncateToWidth3(this.prompt, width, "")];
    }
    if (this.value.length === 0 && this.placeholder) {
      const placeholder = truncateToWidth3(this.placeholder, availableWidth, "");
      const graphemes2 = [...segmenter.segment(placeholder)];
      const atCursor2 = graphemes2[0]?.segment ?? " ";
      const afterCursor2 = placeholder.slice(atCursor2.length);
      const marker2 = this.focused ? CURSOR_MARKER2 : "";
      const cursorChar2 = `\x1B[7m${this.placeholderStyle(atCursor2)}\x1B[27m`;
      const textWithCursor2 = marker2 + cursorChar2 + this.placeholderStyle(afterCursor2);
      const padding2 = " ".repeat(Math.max(0, availableWidth - visibleWidth6(textWithCursor2)));
      return [this.prompt + textWithCursor2 + padding2];
    }
    let visibleText = "";
    let cursorDisplay = this.cursor;
    this.renderedStartColumn = 0;
    const totalWidth = visibleWidth6(this.value);
    if (totalWidth < availableWidth) {
      visibleText = this.value;
    } else {
      const scrollWidth = this.cursor === this.value.length ? availableWidth - 1 : availableWidth;
      const cursorCol = visibleWidth6(this.value.slice(0, this.cursor));
      if (scrollWidth > 0) {
        const halfWidth = Math.floor(scrollWidth / 2);
        let startCol = 0;
        if (cursorCol < halfWidth) {
          startCol = 0;
        } else if (cursorCol > totalWidth - halfWidth) {
          startCol = Math.max(0, totalWidth - scrollWidth);
        } else {
          startCol = Math.max(0, cursorCol - halfWidth);
        }
        this.renderedStartColumn = startCol;
        visibleText = sliceByColumn2(this.value, startCol, scrollWidth, true);
        const beforeCursor2 = sliceByColumn2(this.value, startCol, Math.max(0, cursorCol - startCol), true);
        cursorDisplay = beforeCursor2.length;
      } else {
        visibleText = "";
        cursorDisplay = 0;
      }
    }
    const graphemes = [...segmenter.segment(visibleText.slice(cursorDisplay))];
    const cursorGrapheme = graphemes[0];
    const beforeCursor = visibleText.slice(0, cursorDisplay);
    const atCursor = cursorGrapheme?.segment ?? " ";
    const afterCursor = visibleText.slice(cursorDisplay + atCursor.length);
    const marker = this.focused ? CURSOR_MARKER2 : "";
    const cursorChar = `\x1B[7m${atCursor}\x1B[27m`;
    const textWithCursor = beforeCursor + marker + cursorChar + afterCursor;
    const visualLength = visibleWidth6(textWithCursor);
    const padding = " ".repeat(Math.max(0, availableWidth - visualLength));
    const line = this.prompt + textWithCursor + padding;
    return [line];
  }
};

// pi-dist/pi-tui/components/markdown.js
import { Marked, Tokenizer } from "../../../marked/lib/marked.esm.js";

// pi-dist/pi-tui/latex.js
import { visibleWidth as visibleWidth7 } from "../utils.js";
var SYMBOLS = {
  alpha: "\u03B1",
  beta: "\u03B2",
  gamma: "\u03B3",
  delta: "\u03B4",
  epsilon: "\u03F5",
  varepsilon: "\u03B5",
  zeta: "\u03B6",
  eta: "\u03B7",
  theta: "\u03B8",
  vartheta: "\u03D1",
  iota: "\u03B9",
  kappa: "\u03BA",
  varkappa: "\u03F0",
  lambda: "\u03BB",
  mu: "\u03BC",
  nu: "\u03BD",
  xi: "\u03BE",
  pi: "\u03C0",
  varpi: "\u03D6",
  rho: "\u03C1",
  varrho: "\u03F1",
  sigma: "\u03C3",
  varsigma: "\u03C2",
  tau: "\u03C4",
  upsilon: "\u03C5",
  phi: "\u03D5",
  varphi: "\u03C6",
  chi: "\u03C7",
  psi: "\u03C8",
  omega: "\u03C9",
  Gamma: "\u0393",
  Delta: "\u0394",
  Theta: "\u0398",
  Lambda: "\u039B",
  Xi: "\u039E",
  Pi: "\u03A0",
  Sigma: "\u03A3",
  Upsilon: "\u03A5",
  Phi: "\u03A6",
  Psi: "\u03A8",
  Omega: "\u03A9",
  pm: "\xB1",
  mp: "\u2213",
  times: "\xD7",
  div: "\xF7",
  cdot: "\xB7",
  ast: "\u2217",
  star: "\u22C6",
  circ: "\u2218",
  bullet: "\u2022",
  oplus: "\u2295",
  ominus: "\u2296",
  otimes: "\u2297",
  oslash: "\u2298",
  odot: "\u2299",
  bigcirc: "\u25CB",
  dagger: "\u2020",
  ddagger: "\u2021",
  amalg: "\u2A3F",
  uplus: "\u228E",
  sqcap: "\u2293",
  sqcup: "\u2294",
  bowtie: "\u22C8",
  Join: "\u22C8",
  ltimes: "\u22C9",
  rtimes: "\u22CA",
  leftouterjoin: "\u27D5",
  rightouterjoin: "\u27D6",
  fullouterjoin: "\u27D7",
  triangleleft: "\u25C1",
  triangleright: "\u25B7",
  wr: "\u2240",
  cap: "\u2229",
  cup: "\u222A",
  bigcap: "\u22C2",
  bigcup: "\u22C3",
  bigwedge: "\u22C0",
  bigvee: "\u22C1",
  bigsqcup: "\u2A06",
  biguplus: "\u2A04",
  bigoplus: "\u2A01",
  bigotimes: "\u2A02",
  bigodot: "\u2A00",
  setminus: "\u2216",
  in: "\u2208",
  notin: "\u2209",
  ni: "\u220B",
  subset: "\u2282",
  supset: "\u2283",
  subseteq: "\u2286",
  supseteq: "\u2287",
  sqsubset: "\u228F",
  sqsupset: "\u2290",
  sqsubseteq: "\u2291",
  sqsupseteq: "\u2292",
  prec: "\u227A",
  preceq: "\u227C",
  succ: "\u227B",
  succeq: "\u227D",
  ll: "\u226A",
  gg: "\u226B",
  le: "\u2264",
  leq: "\u2264",
  leqslant: "\u2264",
  ge: "\u2265",
  geq: "\u2265",
  geqslant: "\u2265",
  ne: "\u2260",
  neq: "\u2260",
  equiv: "\u2261",
  approx: "\u2248",
  sim: "\u223C",
  simeq: "\u2243",
  cong: "\u2245",
  asymp: "\u224D",
  doteq: "\u2250",
  propto: "\u221D",
  parallel: "\u2225",
  perp: "\u22A5",
  mid: "\u2223",
  vdash: "\u22A2",
  dashv: "\u22A3",
  models: "\u22A8",
  Vdash: "\u22A9",
  Vvdash: "\u22AA",
  nvdash: "\u22AC",
  nvDash: "\u22AD",
  forall: "\u2200",
  exists: "\u2203",
  nexists: "\u2204",
  neg: "\xAC",
  land: "\u2227",
  wedge: "\u2227",
  lor: "\u2228",
  vee: "\u2228",
  to: "\u2192",
  rightarrow: "\u2192",
  longrightarrow: "\u2192",
  leftarrow: "\u2190",
  longleftarrow: "\u2190",
  gets: "\u2190",
  leftrightarrow: "\u2194",
  longleftrightarrow: "\u2194",
  hookleftarrow: "\u21A9",
  hookrightarrow: "\u21AA",
  twoheadleftarrow: "\u219E",
  twoheadrightarrow: "\u21A0",
  leftharpoonup: "\u21BC",
  leftharpoondown: "\u21BD",
  rightharpoonup: "\u21C0",
  rightharpoondown: "\u21C1",
  rightleftharpoons: "\u21CC",
  leftrightharpoons: "\u21CB",
  nearrow: "\u2197",
  searrow: "\u2198",
  swarrow: "\u2199",
  nwarrow: "\u2196",
  rightsquigarrow: "\u21DD",
  leadsto: "\u21DD",
  Rightarrow: "\u21D2",
  Longrightarrow: "\u21D2",
  Leftarrow: "\u21D0",
  Longleftarrow: "\u21D0",
  Leftrightarrow: "\u21D4",
  Longleftrightarrow: "\u21D4",
  implies: "\u21D2",
  iff: "\u21D4",
  mapsto: "\u21A6",
  longmapsto: "\u21A6",
  uparrow: "\u2191",
  downarrow: "\u2193",
  partial: "\u2202",
  nabla: "\u2207",
  int: "\u222B",
  iint: "\u222C",
  iiint: "\u222D",
  oint: "\u222E",
  sum: "\u2211",
  prod: "\u220F",
  coprod: "\u2210",
  infty: "\u221E",
  emptyset: "\u2205",
  varnothing: "\u2205",
  angle: "\u2220",
  therefore: "\u2234",
  because: "\u2235",
  aleph: "\u2135",
  beth: "\u2136",
  gimel: "\u2137",
  daleth: "\u2138",
  top: "\u22A4",
  bot: "\u22A5",
  triangle: "\u25B3",
  square: "\u25A1",
  lozenge: "\u25CA",
  checkmark: "\u2713",
  complement: "\u2201",
  wp: "\u2118",
  prime: "\u2032",
  ldots: "\u2026",
  dots: "\u2026",
  cdots: "\u22EF",
  vdots: "\u22EE",
  ddots: "\u22F1",
  ell: "\u2113",
  hbar: "\u210F",
  Im: "\u2111",
  Re: "\u211C",
  langle: "\u27E8",
  rangle: "\u27E9",
  vert: "|",
  lvert: "|",
  rvert: "|",
  Vert: "\u2016",
  lVert: "\u2016",
  rVert: "\u2016",
  lbrace: "{",
  rbrace: "}",
  backslash: "\\",
  lfloor: "\u230A",
  rfloor: "\u230B",
  lceil: "\u2308",
  rceil: "\u2309",
  colon: ":"
};
var NAMED_OPERATORS = /* @__PURE__ */ new Set([
  "arccos",
  "arcsin",
  "arctan",
  "arg",
  "cos",
  "cosh",
  "cot",
  "coth",
  "csc",
  "deg",
  "det",
  "dim",
  "exp",
  "gcd",
  "hom",
  "inf",
  "ker",
  "lg",
  "lim",
  "liminf",
  "limsup",
  "ln",
  "log",
  "max",
  "min",
  "Pr",
  "sec",
  "sin",
  "sinh",
  "sup",
  "tan",
  "tanh"
]);
var LIMIT_OPERATORS = /* @__PURE__ */ new Set([
  "argmax",
  "argmin",
  "inf",
  "injlim",
  "lim",
  "liminf",
  "limsup",
  "max",
  "min",
  "projlim",
  "sup"
]);
var DISPLAY_LIMIT_SYMBOLS = /* @__PURE__ */ new Set([
  "bigcap",
  "bigcup",
  "bigodot",
  "bigoplus",
  "bigotimes",
  "bigsqcup",
  "biguplus",
  "bigvee",
  "bigwedge",
  "coprod",
  "int",
  "iint",
  "iiint",
  "oint",
  "prod",
  "sum"
]);
var RELATION_COMMANDS = /* @__PURE__ */ new Set([
  "Leftarrow",
  "Leftrightarrow",
  "Longleftarrow",
  "Longleftrightarrow",
  "Longrightarrow",
  "Rightarrow",
  "Join",
  "Vdash",
  "Vvdash",
  "approx",
  "asymp",
  "bowtie",
  "cong",
  "dashv",
  "fullouterjoin",
  "doteq",
  "downarrow",
  "equiv",
  "ge",
  "geq",
  "geqslant",
  "gets",
  "gg",
  "hookleftarrow",
  "hookrightarrow",
  "iff",
  "implies",
  "in",
  "leadsto",
  "le",
  "leftarrow",
  "leftharpoondown",
  "leftharpoonup",
  "leftrightarrow",
  "leftrightharpoons",
  "leftouterjoin",
  "leq",
  "leqslant",
  "ll",
  "longleftarrow",
  "longleftrightarrow",
  "longmapsto",
  "longrightarrow",
  "ltimes",
  "mapsto",
  "mid",
  "models",
  "ne",
  "nearrow",
  "neq",
  "ni",
  "notin",
  "nvdash",
  "nvDash",
  "nwarrow",
  "parallel",
  "perp",
  "prec",
  "preceq",
  "propto",
  "rightharpoondown",
  "rightharpoonup",
  "rightleftharpoons",
  "rightouterjoin",
  "rightarrow",
  "rightsquigarrow",
  "rtimes",
  "searrow",
  "sim",
  "simeq",
  "sqsubset",
  "sqsubseteq",
  "sqsupset",
  "sqsupseteq",
  "subset",
  "subseteq",
  "succ",
  "succeq",
  "supset",
  "supseteq",
  "swarrow",
  "to",
  "triangleleft",
  "triangleright",
  "twoheadleftarrow",
  "twoheadrightarrow",
  "uparrow",
  "vdash"
]);
var NEGATED_SYMBOLS = {
  "<": "\u226E",
  ">": "\u226F",
  "=": "\u2260",
  "\u2208": "\u2209",
  "\u220B": "\u220C",
  "\u2223": "\u2224",
  "\u2225": "\u2226",
  "\u223C": "\u2241",
  "\u2243": "\u2244",
  "\u2245": "\u2247",
  "\u2248": "\u2249",
  "\u2261": "\u2262",
  "\u2264": "\u2270",
  "\u2265": "\u2271",
  "\u227A": "\u2280",
  "\u227B": "\u2281",
  "\u2282": "\u2284",
  "\u2283": "\u2285",
  "\u2286": "\u2288",
  "\u2287": "\u2289",
  "\u22A2": "\u22AC",
  "\u22A8": "\u22AD",
  "\u2194": "\u21AE",
  "\u2190": "\u219A",
  "\u2192": "\u219B",
  "\u21D2": "\u21CF",
  "\u21D0": "\u21CD",
  "\u21D4": "\u21CE",
  "\u227C": "\u22E0",
  "\u227D": "\u22E1"
};
var BLACKBOARD = {
  C: "\u2102",
  H: "\u210D",
  N: "\u2115",
  P: "\u2119",
  Q: "\u211A",
  R: "\u211D",
  Z: "\u2124"
};
var SUPERSCRIPTS = {
  "0": "\u2070",
  "1": "\xB9",
  "2": "\xB2",
  "3": "\xB3",
  "4": "\u2074",
  "5": "\u2075",
  "6": "\u2076",
  "7": "\u2077",
  "8": "\u2078",
  "9": "\u2079",
  "+": "\u207A",
  "-": "\u207B",
  "=": "\u207C",
  "(": "\u207D",
  ")": "\u207E",
  a: "\u1D43",
  b: "\u1D47",
  c: "\u1D9C",
  d: "\u1D48",
  e: "\u1D49",
  f: "\u1DA0",
  g: "\u1D4D",
  h: "\u02B0",
  i: "\u2071",
  j: "\u02B2",
  k: "\u1D4F",
  l: "\u02E1",
  m: "\u1D50",
  n: "\u207F",
  o: "\u1D52",
  p: "\u1D56",
  r: "\u02B3",
  s: "\u02E2",
  t: "\u1D57",
  u: "\u1D58",
  v: "\u1D5B",
  w: "\u02B7",
  x: "\u02E3",
  y: "\u02B8",
  z: "\u1DBB"
};
var SUBSCRIPTS = {
  "0": "\u2080",
  "1": "\u2081",
  "2": "\u2082",
  "3": "\u2083",
  "4": "\u2084",
  "5": "\u2085",
  "6": "\u2086",
  "7": "\u2087",
  "8": "\u2088",
  "9": "\u2089",
  "+": "\u208A",
  "-": "\u208B",
  "=": "\u208C",
  "(": "\u208D",
  ")": "\u208E",
  a: "\u2090",
  e: "\u2091",
  h: "\u2095",
  i: "\u1D62",
  j: "\u2C7C",
  k: "\u2096",
  l: "\u2097",
  m: "\u2098",
  n: "\u2099",
  o: "\u2092",
  p: "\u209A",
  r: "\u1D63",
  s: "\u209B",
  t: "\u209C",
  u: "\u1D64",
  v: "\u1D65",
  x: "\u2093"
};
var SPACING_COMMANDS = /* @__PURE__ */ new Set([
  ",",
  ":",
  ";",
  " ",
  ">",
  "enspace",
  "enskip",
  "medspace",
  "quad",
  "qquad",
  "thickspace",
  "thinspace"
]);
var NEGATIVE_SPACING_COMMANDS = /* @__PURE__ */ new Set(["!", "negmedspace", "negthickspace", "negthinspace"]);
var NEGATIVE_SPACE = "\0";
var FONT_SWITCH_COMMANDS = /* @__PURE__ */ new Set(["bf", "cal", "it", "rm", "sf", "sl", "tt"]);
var IGNORED_COMMANDS = /* @__PURE__ */ new Set([
  "displaystyle",
  "limits",
  "nolimits",
  "scriptstyle",
  "scriptscriptstyle",
  "textstyle"
]);
var SIZE_COMMANDS = /* @__PURE__ */ new Set([
  "big",
  "Big",
  "bigg",
  "Bigg",
  "bigl",
  "Bigl",
  "biggl",
  "Biggl",
  "bigr",
  "Bigr",
  "biggr",
  "Biggr"
]);
var PLAIN_WRAPPERS = /* @__PURE__ */ new Set([
  "emph",
  "mathcal",
  "mathbf",
  "mathfrak",
  "mathit",
  "mathrm",
  "mathnormal",
  "mathscr",
  "mathsf",
  "mathtt",
  "mathup",
  "mbox",
  "overbrace",
  "pmb",
  "smash",
  "substack",
  "text",
  "textbf",
  "textit",
  "textmd",
  "textnormal",
  "textrm",
  "textsc",
  "textsf",
  "textsl",
  "texttt",
  "textup",
  "underbrace",
  "bm",
  "boldsymbol"
]);
var ACCENTS = {
  acute: "\u0301",
  bar: "\u0305",
  breve: "\u0306",
  check: "\u030C",
  ddot: "\u0308",
  dot: "\u0307",
  grave: "\u0300",
  hat: "\u0302",
  mathring: "\u030A",
  overleftarrow: "\u20D6",
  overleftrightarrow: "\u20E1",
  overline: "\u0305",
  overrightarrow: "\u20D7",
  tilde: "\u0303",
  underline: "\u0332",
  vec: "\u20D7",
  widehat: "\u0302",
  widetilde: "\u0303"
};
function replaceCharacters(value, replacements) {
  let result = "";
  for (const character of value) {
    const replacement = replacements[character];
    if (replacement === void 0) {
      return void 0;
    }
    result += replacement;
  }
  return result;
}
__name(replaceCharacters, "replaceCharacters");
function normalizeScriptValue(value) {
  return value.trim().replace(/\s*([=+-])\s*/g, "$1");
}
__name(normalizeScriptValue, "normalizeScriptValue");
function formatUnicodeScript(value, kind) {
  return replaceCharacters(normalizeScriptValue(value), kind === "sub" ? SUBSCRIPTS : SUPERSCRIPTS);
}
__name(formatUnicodeScript, "formatUnicodeScript");
function formatScript(value, kind) {
  value = normalizeScriptValue(value);
  const unicode = formatUnicodeScript(value, kind);
  if (unicode !== void 0) {
    return unicode;
  }
  const prefix = kind === "sub" ? "_" : "^";
  if (Array.from(value).length === 1 || kind === "sub" && /^[A-Za-z]+$/.test(value)) {
    return `${prefix}${value}`;
  }
  return `${prefix}(${value})`;
}
__name(formatScript, "formatScript");
function formatFraction(numerator, denominator) {
  numerator = numerator.trim();
  denominator = denominator.trim();
  const simpleNumerator = /^[\p{L}\p{N}.]+$/u.test(numerator);
  const simpleDenominator = /^[\p{N}.]+$/u.test(denominator) || Array.from(denominator).length === 1;
  return `${simpleNumerator ? numerator : `(${numerator})`}/${simpleDenominator ? denominator : `(${denominator})`}`;
}
__name(formatFraction, "formatFraction");
function formatRoot(value, symbol = "\u221A") {
  value = value.trim();
  return /^[\p{L}\p{N}.]+$/u.test(value) ? `${symbol}${value}` : `${symbol}(${value})`;
}
__name(formatRoot, "formatRoot");
var NAMED_OPERATOR_START = "\u{F0004}";
var NAMED_OPERATOR_END = "\u{F0005}";
var NAMED_OPERATOR_LEFT_SPACING_PATTERN = /(?<=[\p{L}\p{N})\]}\u{f0001}])\u{f0004}/gu;
var NAMED_OPERATOR_RIGHT_SPACING_PATTERN = /\u{f0005}(?=[\p{L}\p{N}√\u{f0000}])/gu;
function normalizeOutput(value) {
  return value.replace(NAMED_OPERATOR_LEFT_SPACING_PATTERN, " ").replaceAll(NAMED_OPERATOR_START, "").replace(NAMED_OPERATOR_RIGHT_SPACING_PATTERN, " ").replaceAll(NAMED_OPERATOR_END, "").split("\n").map((line) => line.replace(/[ \t]+/g, " ").trim()).filter((line, index, lines) => line.length > 0 || index > 0 && index < lines.length - 1).join("\n").trim();
}
__name(normalizeOutput, "normalizeOutput");
var LAYOUT_MARKER_START = "\u{F0000}";
var LAYOUT_MARKER_END = "\u{F0001}";
var LAYOUT_MARKER_PATTERN = /\u{f0000}(\d+)\u{f0001}/gu;
var TRAILING_LAYOUT_MARKER_PATTERN = /\u{f0000}(\d+)\u{f0001}$/u;
var PROTECTED_SPACE = "\u{F0002}";
function padLayoutLine(line, width, centered = false) {
  const padding = Math.max(0, width - visibleWidth7(line));
  const left = centered ? Math.floor(padding / 2) : 0;
  return `${" ".repeat(left)}${line}${" ".repeat(padding - left)}`;
}
__name(padLayoutLine, "padLayoutLine");
function joinLayouts(layouts) {
  if (layouts.length === 0) {
    return { lines: [""], width: 0, baseline: 0 };
  }
  const baseline = Math.max(...layouts.map((layout) => layout.baseline));
  const below = Math.max(...layouts.map((layout) => layout.lines.length - layout.baseline - 1));
  const lines = [];
  for (let row = 0; row <= baseline + below; row++) {
    let line = "";
    for (const layout of layouts) {
      const sourceRow = row - baseline + layout.baseline;
      line += sourceRow >= 0 && sourceRow < layout.lines.length ? padLayoutLine(layout.lines[sourceRow] ?? "", layout.width) : " ".repeat(layout.width);
    }
    lines.push(line.trimEnd());
  }
  return {
    lines,
    width: layouts.reduce((width, layout) => width + layout.width, 0),
    baseline
  };
}
__name(joinLayouts, "joinLayouts");
function renderLayout(source, nodes) {
  const renderedLines = [];
  let firstBaseline = 0;
  for (const sourceLine of source.split("\n")) {
    const layouts = [];
    let position = 0;
    let previousNode;
    for (const match of sourceLine.matchAll(LAYOUT_MARKER_PATTERN)) {
      const index = match.index;
      const node = nodes[Number(match[1])];
      if (!node) {
        continue;
      }
      if (index > position) {
        const sliced = sourceLine.slice(position, index);
        const trimmed = (previousNode ? sliced.trimStart() : sliced).trimEnd();
        const preserveLeadingSpace = previousNode?.type === "matrix" && /^\s/.test(sliced);
        const preserveTrailingSpace = node.type === "matrix" && /\s$/.test(sliced);
        const text = trimmed ? `${preserveLeadingSpace ? " " : ""}${trimmed}${preserveTrailingSpace ? " " : ""}` : preserveLeadingSpace || preserveTrailingSpace ? " " : "";
        layouts.push({ lines: [text], width: visibleWidth7(text), baseline: 0 });
      }
      if (node.type === "fraction") {
        const numerator = renderLayout(node.numerator, nodes);
        const denominator = renderLayout(node.denominator, nodes);
        const contentWidth = Math.max(numerator.width, denominator.width, 1);
        const width = contentWidth + 2;
        layouts.push({
          lines: [
            ...numerator.lines.map((line) => padLayoutLine(line, width, true)),
            ` ${"\u2500".repeat(contentWidth)} `,
            ...denominator.lines.map((line) => padLayoutLine(line, width, true))
          ],
          width,
          baseline: numerator.lines.length
        });
      } else if (node.type === "operator") {
        const contentWidth = Math.max(visibleWidth7(node.operator), node.lower === void 0 ? 0 : visibleWidth7(node.lower), node.upper === void 0 ? 0 : visibleWidth7(node.upper));
        const lines = [];
        if (node.upper !== void 0) {
          lines.push(`${padLayoutLine(node.upper, contentWidth, true)} `);
        }
        lines.push(`${padLayoutLine(node.operator, contentWidth, true)} `);
        if (node.lower !== void 0) {
          lines.push(`${padLayoutLine(node.lower, contentWidth, true)} `);
        }
        layouts.push({
          lines,
          width: contentWidth + 1,
          baseline: node.upper === void 0 ? 0 : 1
        });
      } else if (node.type === "script") {
        const upper = node.upper === void 0 ? void 0 : renderLayout(node.upper, nodes);
        const lower = node.lower === void 0 ? void 0 : renderLayout(node.lower, nodes);
        const width = Math.max(upper?.width ?? 0, lower?.width ?? 0);
        layouts.push({
          lines: [
            ...upper?.lines.map((line) => padLayoutLine(line, width)) ?? [],
            " ".repeat(width),
            ...lower?.lines.map((line) => padLayoutLine(line, width)) ?? []
          ],
          width,
          baseline: upper?.lines.length ?? 0
        });
      } else {
        const width = Math.max(0, ...node.lines.map((line) => visibleWidth7(line)));
        layouts.push({
          lines: node.lines.map((line) => padLayoutLine(line, width)),
          width,
          baseline: node.baseline
        });
      }
      position = index + match[0].length;
      previousNode = node;
    }
    if (position < sourceLine.length) {
      const sliced = sourceLine.slice(position);
      const trimmed = previousNode ? sliced.trimStart() : sliced;
      const text = previousNode?.type === "matrix" && /^\s/.test(sliced) ? ` ${trimmed}` : trimmed;
      layouts.push({ lines: [text], width: visibleWidth7(text), baseline: 0 });
    }
    const lineLayout = joinLayouts(layouts);
    if (renderedLines.length === 0) {
      firstBaseline = lineLayout.baseline;
    }
    renderedLines.push(...lineLayout.lines);
  }
  return {
    lines: renderedLines,
    width: Math.max(0, ...renderedLines.map((line) => visibleWidth7(line))),
    baseline: firstBaseline
  };
}
__name(renderLayout, "renderLayout");
var LatexParser = class _LatexParser {
  static {
    __name(this, "LatexParser");
  }
  source;
  layoutNodes;
  display;
  position = 0;
  supported = true;
  stackFractions = true;
  scriptDepth = 0;
  constructor(source, layoutNodes, display) {
    this.source = source;
    this.layoutNodes = layoutNodes;
    this.display = display;
  }
  render() {
    const rendered = this.parseSequence();
    if (!this.supported || this.position !== this.source.length) {
      return void 0;
    }
    return normalizeOutput(rendered);
  }
  parseSequence(endCharacter) {
    let result = "";
    while (this.position < this.source.length) {
      const character = this.source[this.position];
      if (endCharacter && character === endCharacter) {
        this.position++;
        return result;
      }
      if (character === "}") {
        this.supported = false;
        return result;
      }
      if (character === "{") {
        this.position++;
        result += this.parseSequence("}");
        continue;
      }
      if (character === "\\") {
        const command = this.parseCommand();
        if (command === NEGATIVE_SPACE) {
          result = result.trimEnd();
          if (result.endsWith(NAMED_OPERATOR_END)) {
            result = result.slice(0, -NAMED_OPERATOR_END.length);
          }
        } else {
          result += command;
        }
        continue;
      }
      if (character === "^" || character === "_") {
        this.position++;
        result = result.trimEnd();
        const script = this.parseScripts(character);
        if (result.endsWith(NAMED_OPERATOR_END)) {
          result = `${result.slice(0, -NAMED_OPERATOR_END.length)}${script}${NAMED_OPERATOR_END}`;
        } else {
          result += script;
        }
        continue;
      }
      if (/\s/.test(character)) {
        result += this.parseWhitespace();
        continue;
      }
      if (character === "=" || character === "<" || character === ">") {
        result = `${result.trimEnd()} ${character} `;
        this.position++;
        continue;
      }
      if (character === "&") {
        this.position++;
        continue;
      }
      if (character === "~") {
        this.position++;
        result += " ";
        continue;
      }
      if (character === ".") {
        const marker = TRAILING_LAYOUT_MARKER_PATTERN.exec(result);
        const node = marker ? this.layoutNodes[Number(marker[1])] : void 0;
        if (node?.type === "matrix") {
          const lastLine = node.lines.length - 1;
          node.lines[lastLine] = `${node.lines[lastLine] ?? ""}${character}`;
          this.position++;
          continue;
        }
      }
      result += character;
      this.position++;
    }
    if (endCharacter) {
      this.supported = false;
    }
    return result;
  }
  parseScripts(initialMarker) {
    const scripts = {};
    const order = [];
    const parse = /* @__PURE__ */ __name((marker) => {
      const kind = marker === "_" ? "sub" : "sup";
      this.scriptDepth++;
      try {
        scripts[kind] = this.parseRequiredArgument(false);
      } finally {
        this.scriptDepth--;
      }
      order.push(kind);
    }, "parse");
    parse(initialMarker);
    let nextPosition = this.position;
    while (nextPosition < this.source.length && /\s/.test(this.source[nextPosition] ?? "")) {
      nextPosition++;
    }
    const nextMarker = this.source[nextPosition];
    if ((nextMarker === "^" || nextMarker === "_") && nextMarker !== initialMarker) {
      this.position = nextPosition + 1;
      parse(nextMarker);
    }
    const subUnicode = scripts.sub === void 0 ? void 0 : formatUnicodeScript(scripts.sub, "sub");
    const supUnicode = scripts.sup === void 0 ? void 0 : formatUnicodeScript(scripts.sup, "sup");
    const canUseLayout = ![scripts.sub, scripts.sup].some((value) => value !== void 0 && (value.includes("/") || !value.includes(LAYOUT_MARKER_START) && Array.from(value).length > 1 && !/[A-Z*∗]/.test(value)));
    const needsLayout = this.display && canUseLayout && (this.scriptDepth > 0 || scripts.sub !== void 0 && subUnicode === void 0 || scripts.sup !== void 0 && supUnicode === void 0);
    if (!needsLayout) {
      return order.map((kind) => kind === "sub" ? subUnicode ?? formatScript(scripts.sub ?? "", kind) : supUnicode ?? formatScript(scripts.sup ?? "", kind)).join("");
    }
    const index = this.layoutNodes.push({
      type: "script",
      lower: scripts.sub === void 0 ? void 0 : normalizeOutput(scripts.sub),
      upper: scripts.sup === void 0 ? void 0 : normalizeOutput(scripts.sup)
    }) - 1;
    return `${LAYOUT_MARKER_START}${index}${LAYOUT_MARKER_END}`;
  }
  parseWhitespace() {
    while (this.position < this.source.length && /\s/.test(this.source[this.position] ?? "")) {
      this.position++;
    }
    return " ";
  }
  parseCommand() {
    this.position++;
    if (this.position >= this.source.length) {
      this.supported = false;
      return "";
    }
    let command = "";
    const first = this.source[this.position] ?? "";
    if (first === "\n" || first === "\r") {
      this.position++;
      if (first === "\r" && this.source[this.position] === "\n") {
        this.position++;
      }
      return " ";
    }
    if (/[A-Za-z]/.test(first)) {
      const start = this.position;
      while (this.position < this.source.length && /[A-Za-z]/.test(this.source[this.position] ?? "")) {
        this.position++;
      }
      command = this.source.slice(start, this.position);
    } else {
      command = first;
      this.position++;
    }
    if (command === "\\") {
      return "\n";
    }
    if (SPACING_COMMANDS.has(command)) {
      return " ";
    }
    if (NEGATIVE_SPACING_COMMANDS.has(command)) {
      return NEGATIVE_SPACE;
    }
    if (FONT_SWITCH_COMMANDS.has(command)) {
      while (this.position < this.source.length && /\s/.test(this.source[this.position] ?? "")) {
        this.position++;
      }
      return "";
    }
    if (IGNORED_COMMANDS.has(command)) {
      return "";
    }
    if (command === "{" || command === "}" || command === "$" || command === "%" || command === "#" || command === "_" || command === "&") {
      return command;
    }
    if (command === "|") {
      return "\u2016";
    }
    if (command === "not") {
      const value = this.parseRequiredArgument(false).trim();
      const negated = NEGATED_SYMBOLS[value];
      if (negated !== void 0) {
        return ` ${negated} `;
      }
      const characters = Array.from(value);
      if (characters.length === 0) {
        this.supported = false;
        return "";
      }
      return ` ${characters[0]}\u0338${characters.slice(1).join("")} `;
    }
    if (LIMIT_OPERATORS.has(command)) {
      return this.parseOperator(command, "bracket", true, true);
    }
    const symbol = SYMBOLS[command];
    if (symbol !== void 0) {
      if (DISPLAY_LIMIT_SYMBOLS.has(command)) {
        return this.parseOperator(symbol, "script", true);
      }
      return command === "cdot" || command === "times" || RELATION_COMMANDS.has(command) ? ` ${symbol} ` : symbol;
    }
    if (NAMED_OPERATORS.has(command)) {
      return `${NAMED_OPERATOR_START}${command}${NAMED_OPERATOR_END}`;
    }
    if (SIZE_COMMANDS.has(command)) {
      return "";
    }
    if (command === "left" || command === "middle" || command === "right") {
      if (this.source[this.position] === ".") {
        this.position++;
      }
      return "";
    }
    if (command === "frac" || command === "dfrac" || command === "tfrac") {
      const shouldStack = this.display && this.stackFractions && command !== "tfrac";
      const numerator = this.parseRequiredArgument(!shouldStack);
      const denominator = this.parseRequiredArgument(!shouldStack);
      if (shouldStack) {
        const index = this.layoutNodes.push({
          type: "fraction",
          numerator: normalizeOutput(numerator),
          denominator: normalizeOutput(denominator)
        }) - 1;
        return `${LAYOUT_MARKER_START}${index}${LAYOUT_MARKER_END}`;
      }
      return formatFraction(numerator, denominator);
    }
    if (command === "sqrt") {
      const degree = this.parseOptionalArgument()?.trim();
      const value = this.parseRequiredArgument();
      if (degree === void 0 || degree === "2") {
        return formatRoot(value);
      }
      if (degree === "3") {
        return formatRoot(value, "\u221B");
      }
      if (degree === "4") {
        return formatRoot(value, "\u221C");
      }
      return `${formatScript(degree, "sup")}${formatRoot(value)}`;
    }
    if (command === "boxed" || command === "fbox") {
      return `[${this.parseRequiredArgument().trim()}]`;
    }
    if (command === "binom" || command === "dbinom" || command === "tbinom") {
      return `(${this.parseRequiredArgument()} choose ${this.parseRequiredArgument()})`;
    }
    const accent = ACCENTS[command];
    if (accent !== void 0) {
      const value = this.parseRequiredArgument();
      return Array.from(value).length === 1 ? `${value}${accent}` : `${command}(${value})`;
    }
    if (command === "mathbb") {
      const value = this.parseRequiredArgument();
      return Array.from(value, (character) => BLACKBOARD[character] ?? character).join("");
    }
    if (command === "operatorname") {
      const starred = this.source[this.position] === "*";
      if (starred) {
        this.position++;
      }
      const operator = normalizeOutput(this.parseRequiredArgument()).trim();
      return this.parseOperator(operator, "bracket", starred, true);
    }
    if (command === "mod" || command === "bmod") {
      return " mod ";
    }
    if (command === "pmod" || command === "pod") {
      const value = this.parseRequiredArgument().trim();
      return command === "pmod" ? ` (mod ${value})` : ` (${value})`;
    }
    if (command === "overset" || command === "stackrel") {
      const upper = this.parseRequiredArgument();
      const value = this.parseRequiredArgument().trim();
      return `${value}${formatScript(upper, "sup")}`;
    }
    if (command === "underset") {
      const lower = this.parseRequiredArgument();
      const value = this.parseRequiredArgument().trim();
      return `${value}${formatScript(lower, "sub")}`;
    }
    if (PLAIN_WRAPPERS.has(command)) {
      const value = this.parseRequiredArgument();
      return command.startsWith("text") || command === "mbox" ? value : value.trim();
    }
    if (command === "begin") {
      return this.parseEnvironment();
    }
    if (command === "end") {
      this.supported = false;
      return "";
    }
    this.supported = false;
    return `\\${command}`;
  }
  parseOperator(operator, inlineLowerStyle, displayLimits, spaced = false) {
    let useDisplayLimits = displayLimits;
    let modifierPosition = this.position;
    while (modifierPosition < this.source.length && /[ \t]/.test(this.source[modifierPosition] ?? "")) {
      modifierPosition++;
    }
    const modifier = /^\\(limits|nolimits)(?![A-Za-z])/.exec(this.source.slice(modifierPosition));
    if (modifier) {
      useDisplayLimits = modifier[1] === "limits";
      this.position = modifierPosition + modifier[0].length;
    }
    let lower;
    let upper;
    while (true) {
      let scriptPosition = this.position;
      while (scriptPosition < this.source.length && /[ \t]/.test(this.source[scriptPosition] ?? "")) {
        scriptPosition++;
      }
      const kind = this.source[scriptPosition];
      if (kind !== "_" && kind !== "^") {
        break;
      }
      this.position = scriptPosition + 1;
      const value = normalizeOutput(this.parseRequiredArgument(false)).replaceAll(" ", "");
      if (kind === "_") {
        if (lower !== void 0) {
          this.supported = false;
        }
        lower = value;
      } else {
        if (upper !== void 0) {
          this.supported = false;
        }
        upper = value;
      }
    }
    if (this.display && useDisplayLimits && (lower !== void 0 || upper !== void 0)) {
      const index = this.layoutNodes.push({ type: "operator", operator, lower, upper }) - 1;
      return `${LAYOUT_MARKER_START}${index}${LAYOUT_MARKER_END}`;
    }
    let rendered = operator;
    if (lower !== void 0) {
      rendered += inlineLowerStyle === "bracket" ? `[${lower}]` : formatScript(lower, "sub");
    }
    if (upper !== void 0) {
      rendered += formatScript(upper, "sup");
    }
    return spaced ? ` ${rendered} ` : rendered;
  }
  parseRequiredArgument(stackFractions = true) {
    const previousStackFractions = this.stackFractions;
    this.stackFractions = previousStackFractions && stackFractions;
    const value = this.parseRequiredArgumentValue();
    this.stackFractions = previousStackFractions;
    return value;
  }
  parseRequiredArgumentValue() {
    while (this.position < this.source.length && /\s/.test(this.source[this.position] ?? "")) {
      this.position++;
    }
    if (this.position >= this.source.length) {
      this.supported = false;
      return "";
    }
    if (this.source[this.position] === "{") {
      this.position++;
      return this.parseSequence("}");
    }
    if (this.source[this.position] === "\\") {
      return this.parseCommand();
    }
    const value = this.source[this.position] ?? "";
    this.position++;
    return value;
  }
  parseOptionalArgument() {
    while (this.position < this.source.length && /[ \t]/.test(this.source[this.position] ?? "")) {
      this.position++;
    }
    if (this.source[this.position] !== "[") {
      return void 0;
    }
    const end = this.source.indexOf("]", this.position + 1);
    if (end < 0) {
      this.supported = false;
      return void 0;
    }
    const value = this.source.slice(this.position + 1, end);
    this.position = end + 1;
    return this.renderNested(value);
  }
  readRawGroup() {
    while (this.position < this.source.length && /[ \t]/.test(this.source[this.position] ?? "")) {
      this.position++;
    }
    if (this.source[this.position] !== "{") {
      this.supported = false;
      return void 0;
    }
    const start = ++this.position;
    let depth = 1;
    while (this.position < this.source.length) {
      const character = this.source[this.position];
      if (character === "\\") {
        this.position += 2;
        continue;
      }
      if (character === "{")
        depth++;
      if (character === "}")
        depth--;
      if (depth === 0) {
        const value = this.source.slice(start, this.position);
        this.position++;
        return value;
      }
      this.position++;
    }
    this.supported = false;
    return void 0;
  }
  splitEnvironmentRows(body) {
    return body.split(/\\\\(?:\[[^\]\n]*\])?/);
  }
  parseEnvironment() {
    const environment = this.readRawGroup();
    if (!environment) {
      return "";
    }
    const endMarker = `\\end{${environment}}`;
    const end = this.source.indexOf(endMarker, this.position);
    if (end < 0) {
      this.supported = false;
      return "";
    }
    const body = this.source.slice(this.position, end);
    this.position = end + endMarker.length;
    if (environment === "equation" || environment === "equation*" || environment === "displaymath") {
      return this.renderNested(body).trim();
    }
    if (environment === "aligned" || environment === "align" || environment === "align*" || environment === "alignedat" || environment === "alignat" || environment === "alignat*" || environment === "gather" || environment === "gathered" || environment === "multline" || environment === "multline*" || environment === "split") {
      const alignedAt = ["alignedat", "alignat", "alignat*"].includes(environment);
      const alignedBody = alignedAt ? body.replace(/^\s*\{[^}]*\}/, "") : body;
      return this.splitEnvironmentRows(alignedBody).map((row) => {
        const cells = row.split("&");
        const source = alignedAt ? Array.from({ length: Math.ceil(cells.length / 2) }, (_, index) => cells.slice(index * 2, index * 2 + 2).join("")).join(" ") : cells.join("");
        return this.renderNested(source).trim();
      }).filter(Boolean).join("\n");
    }
    if (environment === "cases" || environment === "cases*") {
      return this.renderCases(body);
    }
    if (["array", "matrix", "smallmatrix", "pmatrix", "bmatrix", "Bmatrix", "vmatrix", "Vmatrix"].includes(environment)) {
      const matrixBody = environment === "array" ? body.replace(/^\s*\{[^}]*\}/, "") : body;
      return this.renderMatrix(environment, matrixBody);
    }
    this.supported = false;
    return body;
  }
  renderCases(body) {
    const rows = this.splitEnvironmentRows(body).map((row) => row.split("&").map((cell) => this.renderNested(cell, false).trim())).filter((row) => row.some(Boolean));
    const valueWidth = Math.max(0, ...rows.map((row) => visibleWidth7((row[0] ?? "").replace(/,\s*$/, ""))));
    const contents = rows.map((row) => {
      const value = (row[0] ?? "").replace(/,\s*$/, "");
      const condition = row[1] ?? "";
      if (!condition) {
        return value;
      }
      const conditionPrefix = /^(?:if|when|for|otherwise)\b/i.test(condition) ? " " : " if ";
      return `${value}${PROTECTED_SPACE.repeat(valueWidth - visibleWidth7(value))}${conditionPrefix}${condition}`;
    });
    if (contents.length <= 1) {
      return contents.length === 0 ? "" : `\u23A7 ${contents[0]}`;
    }
    const middle = Math.floor(contents.length / 2);
    const visualRows = [...contents];
    if (contents.length % 2 === 0) {
      visualRows.splice(middle, 0, void 0);
    }
    const lines = visualRows.map((content, index2) => {
      const delimiter = index2 === 0 ? "\u23A7" : index2 === visualRows.length - 1 ? "\u23A9" : "\u23A8";
      return content === void 0 ? delimiter : `${delimiter} ${content}`;
    });
    const index = this.layoutNodes.push({ type: "matrix", lines, baseline: middle }) - 1;
    return `${LAYOUT_MARKER_START}${index}${LAYOUT_MARKER_END}`;
  }
  renderMatrix(environment, body) {
    const matrix = this.splitEnvironmentRows(body).map((row) => row.split("&").map((cell) => this.renderNested(cell, false).trim())).filter((row) => row.some(Boolean));
    const columnCount = Math.max(0, ...matrix.map((row) => row.length));
    const columnWidths = Array.from({ length: columnCount }, (_, column) => Math.max(0, ...matrix.map((row) => visibleWidth7(row[column] ?? ""))));
    const rows = matrix.map((row) => Array.from({ length: columnCount }, (_, column) => {
      const cell = row[column] ?? "";
      return `${cell}${PROTECTED_SPACE.repeat(Math.max(0, (columnWidths[column] ?? 0) - visibleWidth7(cell)))}`;
    }).join(" \u2502 "));
    let lines;
    if (environment === "array" || environment === "matrix" || environment === "smallmatrix") {
      lines = rows;
    } else {
      const delimiters = {
        pmatrix: ["\u239B", "\u239E", "\u239C", "\u239F", "\u239D", "\u23A0"],
        bmatrix: ["\u23A1", "\u23A4", "\u23A2", "\u23A5", "\u23A3", "\u23A6"],
        Bmatrix: ["\u23A7", "\u23AB", "\u23A8", "\u23AC", "\u23A9", "\u23AD"],
        vmatrix: ["\u2502", "\u2502", "\u2502", "\u2502", "\u2502", "\u2502"],
        Vmatrix: ["\u2551", "\u2551", "\u2551", "\u2551", "\u2551", "\u2551"]
      };
      const delimiter = delimiters[environment];
      if (!delimiter) {
        this.supported = false;
        return rows.join("\n");
      }
      lines = rows.map((row, index2) => {
        const left = index2 === 0 ? delimiter[0] : index2 === rows.length - 1 ? delimiter[4] : delimiter[2];
        const right = index2 === 0 ? delimiter[1] : index2 === rows.length - 1 ? delimiter[5] : delimiter[3];
        return `${left} ${row} ${right}`;
      });
    }
    if (lines.length <= 1) {
      return lines[0] ?? "";
    }
    const index = this.layoutNodes.push({ type: "matrix", lines, baseline: 0 }) - 1;
    return `${LAYOUT_MARKER_START}${index}${LAYOUT_MARKER_END}`;
  }
  renderNested(source, stackFractions = true) {
    const rendered = new _LatexParser(source, this.layoutNodes, this.display && stackFractions).render();
    if (rendered === void 0) {
      this.supported = false;
      return source;
    }
    return rendered;
  }
};
function renderLatex(source, options = {}) {
  const layoutNodes = [];
  const rendered = new LatexParser(source, layoutNodes, options.display === true).render();
  if (rendered === void 0) {
    return void 0;
  }
  if (layoutNodes.length === 0) {
    return rendered.replaceAll(PROTECTED_SPACE, " ");
  }
  const lines = renderLayout(rendered, layoutNodes).lines;
  const indentation = Math.min(...lines.filter((line) => line.trim()).map((line) => line.length - line.trimStart().length));
  return lines.map((line) => line.slice(indentation).trimEnd()).join("\n").trimEnd().replaceAll(PROTECTED_SPACE, " ");
}
__name(renderLatex, "renderLatex");

// pi-dist/pi-tui/components/markdown.js
import { getCapabilities as getCapabilities2, hyperlink, isImageLine } from "../terminal-image.js";
import { applyBackgroundToLine as applyBackgroundToLine2, flattenLines as flattenLines3, visibleWidth as visibleWidth8, wrapTextWithAnsi as wrapTextWithAnsi2 } from "../utils.js";
var STRICT_STRIKETHROUGH_REGEX = /^(~~)(?=[^\s~])((?:\\.|[^\\])*?(?:\\.|[^\s~\\]))\1(?=[^~]|$)/;
var StrictStrikethroughTokenizer = class extends Tokenizer {
  static {
    __name(this, "StrictStrikethroughTokenizer");
  }
  del(src) {
    const match = STRICT_STRIKETHROUGH_REGEX.exec(src);
    if (!match) {
      return void 0;
    }
    const text = match[2];
    return {
      type: "del",
      raw: match[0],
      text,
      tokens: this.lexer.inlineTokens(text)
    };
  }
};
function isEscaped(source, index) {
  let backslashes = 0;
  for (let position = index - 1; position >= 0 && source[position] === "\\"; position--) {
    backslashes++;
  }
  return backslashes % 2 === 1;
}
__name(isEscaped, "isEscaped");
function findClosingDelimiter(source, closing, start) {
  let index = source.indexOf(closing, start);
  while (index >= 0 && isEscaped(source, index)) {
    index = source.indexOf(closing, index + closing.length);
  }
  return index;
}
__name(findClosingDelimiter, "findClosingDelimiter");
function looksLikePendingDollarMath(source) {
  return /\\[A-Za-z]+|[_^=+*/<>()[\]|±≤≥≠≈∈→⇒∞∫∑√-]/.test(source);
}
__name(looksLikePendingDollarMath, "looksLikePendingDollarMath");
function tokenizeInlineLatex(source) {
  let opening = "";
  let closing = "";
  if (source.startsWith("$$")) {
    opening = "$$";
    closing = "$$";
  } else if (source.startsWith("\\(")) {
    opening = "\\(";
    closing = "\\)";
  } else if (source.startsWith("\\[")) {
    opening = "\\[";
    closing = "\\]";
  } else if (source.startsWith("$") && !/^\$\s/.test(source)) {
    opening = "$";
    closing = "$";
  } else {
    return void 0;
  }
  const closingIndex = findClosingDelimiter(source, closing, opening.length);
  if (closingIndex >= 0 && opening === "$" && (/\s$/.test(source.slice(opening.length, closingIndex)) || /^\d/.test(source.slice(closingIndex + 1)) || /^[A-Z_][A-Z0-9_]*(?:[^A-Za-z0-9_\s])?$/.test(source.slice(opening.length, closingIndex)) && /^[A-Za-z_][A-Za-z0-9_]*/.test(source.slice(closingIndex + 1)) || source.slice(opening.length, closingIndex).includes("`"))) {
    return void 0;
  }
  if (closingIndex < 0) {
    const pendingSource = source.slice(opening.length);
    if (opening.startsWith("\\") || looksLikePendingDollarMath(pendingSource)) {
      return { type: "latex", raw: source, text: pendingSource, pending: true };
    }
    return void 0;
  }
  const text = source.slice(opening.length, closingIndex);
  if (!text || text.includes("\n")) {
    return void 0;
  }
  const raw = source.slice(0, closingIndex + closing.length);
  return { type: "latex", raw, text };
}
__name(tokenizeInlineLatex, "tokenizeInlineLatex");
function tokenizeBlockLatex(source) {
  const dollarMatch = /^ {0,3}\$\$[ \t]*(?:\n)?([\s\S]*?)\$\$[ \t]*(?:\n|$)/.exec(source);
  if (dollarMatch?.[1]) {
    return { type: "latexBlock", raw: dollarMatch[0], text: dollarMatch[1].trim() };
  }
  const bracketMatch = /^ {0,3}\\\[[ \t]*(?:\n)?([\s\S]*?)\\\][ \t]*(?:\n|$)/.exec(source);
  if (bracketMatch?.[1]) {
    return { type: "latexBlock", raw: bracketMatch[0], text: bracketMatch[1].trim() };
  }
  const pendingBracket = /^ {0,3}\\\[[ \t]*(?:\n)?([\s\S]*)$/.exec(source);
  if (pendingBracket) {
    return { type: "latexBlock", raw: pendingBracket[0], text: pendingBracket[1], pending: true };
  }
  const pendingDollar = /^ {0,3}\$\$[ \t]*(?:\n)?([\s\S]*)$/.exec(source);
  if (pendingDollar?.[1] && looksLikePendingDollarMath(pendingDollar[1])) {
    return { type: "latexBlock", raw: pendingDollar[0], text: pendingDollar[1], pending: true };
  }
  return void 0;
}
__name(tokenizeBlockLatex, "tokenizeBlockLatex");
var LATEX_MARKDOWN_EXTENSIONS = [
  {
    name: "latexBlock",
    level: "block",
    start(source) {
      const match = /(?:^|\n) {0,3}(?:\$\$|\\\[)/.exec(source);
      return match ? match.index + (match[0].startsWith("\n") ? 1 : 0) : void 0;
    },
    tokenizer: tokenizeBlockLatex
  },
  {
    name: "latex",
    level: "inline",
    start(source) {
      const indices = [source.indexOf("$"), source.indexOf("\\("), source.indexOf("\\[")].filter((index) => index >= 0);
      return indices.length > 0 ? Math.min(...indices) : void 0;
    },
    tokenizer: tokenizeInlineLatex
  }
];
function trimPartialClosingFences(tokens) {
  const token = tokens[tokens.length - 1];
  if (token?.type === "list") {
    trimPartialClosingFences(token.items[token.items.length - 1]?.tokens ?? []);
    return;
  }
  if (token?.type === "blockquote") {
    trimPartialClosingFences(token.tokens ?? []);
    return;
  }
  if (token?.type !== "code") {
    return;
  }
  const marker = /^(`{3,}|~{3,})/.exec(token.raw)?.[1];
  const lastLine = token.raw.split("\n").pop();
  if (!marker || !lastLine || lastLine.length >= marker.length || lastLine !== marker[0]?.repeat(lastLine.length)) {
    return;
  }
  token.text = token.text.slice(0, -lastLine.length).replace(/\n$/, "");
}
__name(trimPartialClosingFences, "trimPartialClosingFences");
var markdownParser = new Marked();
markdownParser.setOptions({
  tokenizer: new StrictStrikethroughTokenizer()
});
markdownParser.use({ extensions: [...LATEX_MARKDOWN_EXTENSIONS] });
var Markdown = class {
  static {
    __name(this, "Markdown");
  }
  text;
  paddingX;
  // Left/right padding
  paddingY;
  // Top/bottom padding
  defaultTextStyle;
  theme;
  options;
  defaultStylePrefix;
  // Cache for rendered output
  cachedText;
  cachedWidth;
  cachedLines;
  // Parsed tokens depend only on the source, so they survive theme and width invalidation. Held weakly: a token tree is
  // about ten times the size of its source, and every message of a long transcript keeps a Markdown component. The
  // tokens survive a burst of re-renders, such as a theme preview, and are collected afterwards.
  cachedTokens;
  constructor(text, paddingX, paddingY, theme, defaultTextStyle, options) {
    this.text = text;
    this.paddingX = paddingX;
    this.paddingY = paddingY;
    this.theme = theme;
    this.defaultTextStyle = defaultTextStyle;
    this.options = options ? { ...options } : {};
  }
  setText(text) {
    this.text = text;
    this.invalidate();
  }
  invalidate() {
    this.cachedText = void 0;
    this.cachedWidth = void 0;
    this.cachedLines = void 0;
  }
  render(width) {
    if (this.cachedLines && this.cachedText === this.text && this.cachedWidth === width) {
      return this.cachedLines;
    }
    const contentWidth = Math.max(1, width - this.paddingX * 2);
    const text = this.options.transform?.(this.text, contentWidth) ?? this.text;
    if (!text || text.trim() === "") {
      const result2 = [];
      this.cachedText = this.text;
      this.cachedWidth = width;
      this.cachedLines = result2;
      return result2;
    }
    const normalizedText = text.replace(/\t/g, "   ");
    const cached = this.cachedTokens?.deref();
    let tokens = cached?.source === normalizedText ? cached.tokens : void 0;
    if (!tokens) {
      tokens = markdownParser.lexer(normalizedText);
      trimPartialClosingFences(tokens);
      this.cachedTokens = new WeakRef({ source: normalizedText, tokens });
    }
    const renderedLines = [];
    for (let i = 0; i < tokens.length; i++) {
      const token = tokens[i];
      const nextToken = tokens[i + 1];
      const tokenLines = this.renderToken(token, contentWidth, nextToken?.type);
      for (const tokenLine of tokenLines) {
        renderedLines.push(tokenLine);
      }
    }
    const wrappedLines = [];
    for (const line of renderedLines) {
      if (isImageLine(line)) {
        wrappedLines.push(line);
      } else {
        for (const wrappedLine of wrapTextWithAnsi2(line, contentWidth)) {
          wrappedLines.push(wrappedLine);
        }
      }
    }
    const leftMargin = " ".repeat(this.paddingX);
    const rightMargin = " ".repeat(this.paddingX);
    const bgFn = this.defaultTextStyle?.bgColor;
    const contentLines = [];
    for (const line of wrappedLines) {
      if (isImageLine(line)) {
        contentLines.push(line);
        continue;
      }
      const lineWithMargins = leftMargin + line + rightMargin;
      if (bgFn) {
        contentLines.push(applyBackgroundToLine2(lineWithMargins, width, bgFn));
      } else {
        const visibleLen = visibleWidth8(lineWithMargins);
        const paddingNeeded = Math.max(0, width - visibleLen);
        contentLines.push(lineWithMargins + " ".repeat(paddingNeeded));
      }
    }
    const emptyLine = " ".repeat(width);
    const emptyLines = [];
    for (let i = 0; i < this.paddingY; i++) {
      const line = bgFn ? applyBackgroundToLine2(emptyLine, width, bgFn) : emptyLine;
      emptyLines.push(line);
    }
    const result = emptyLines.concat(contentLines, emptyLines);
    flattenLines3(result);
    this.cachedText = this.text;
    this.cachedWidth = width;
    this.cachedLines = result;
    return result.length > 0 ? result : [""];
  }
  /**
   * Apply default text style to a string.
   * This is the base styling applied to all text content.
   * NOTE: Background color is NOT applied here - it's applied at the padding stage
   * to ensure it extends to the full line width.
   */
  applyDefaultStyle(text) {
    if (!this.defaultTextStyle) {
      return text;
    }
    let styled = text;
    if (this.defaultTextStyle.color) {
      styled = this.defaultTextStyle.color(styled);
    }
    if (this.defaultTextStyle.bold) {
      styled = this.theme.bold(styled);
    }
    if (this.defaultTextStyle.italic) {
      styled = this.theme.italic(styled);
    }
    if (this.defaultTextStyle.strikethrough) {
      styled = this.theme.strikethrough(styled);
    }
    if (this.defaultTextStyle.underline) {
      styled = this.theme.underline(styled);
    }
    return styled;
  }
  getDefaultStylePrefix() {
    if (!this.defaultTextStyle) {
      return "";
    }
    if (this.defaultStylePrefix !== void 0) {
      return this.defaultStylePrefix;
    }
    const sentinel = "\0";
    let styled = sentinel;
    if (this.defaultTextStyle.color) {
      styled = this.defaultTextStyle.color(styled);
    }
    if (this.defaultTextStyle.bold) {
      styled = this.theme.bold(styled);
    }
    if (this.defaultTextStyle.italic) {
      styled = this.theme.italic(styled);
    }
    if (this.defaultTextStyle.strikethrough) {
      styled = this.theme.strikethrough(styled);
    }
    if (this.defaultTextStyle.underline) {
      styled = this.theme.underline(styled);
    }
    const sentinelIndex = styled.indexOf(sentinel);
    this.defaultStylePrefix = sentinelIndex >= 0 ? styled.slice(0, sentinelIndex) : "";
    return this.defaultStylePrefix;
  }
  getStylePrefix(styleFn) {
    const sentinel = "\0";
    const styled = styleFn(sentinel);
    const sentinelIndex = styled.indexOf(sentinel);
    return sentinelIndex >= 0 ? styled.slice(0, sentinelIndex) : "";
  }
  getDefaultInlineStyleContext() {
    return {
      applyText: /* @__PURE__ */ __name((text) => this.applyDefaultStyle(text), "applyText"),
      stylePrefix: this.getDefaultStylePrefix()
    };
  }
  renderToken(token, width, nextTokenType, styleContext) {
    const lines = [];
    switch (token.type) {
      case "heading": {
        const headingLevel = token.depth;
        const headingPrefix = `${"#".repeat(headingLevel)} `;
        let headingStyleFn;
        if (headingLevel === 1) {
          headingStyleFn = /* @__PURE__ */ __name((text) => this.theme.heading(this.theme.bold(this.theme.underline(text))), "headingStyleFn");
        } else {
          headingStyleFn = /* @__PURE__ */ __name((text) => this.theme.heading(this.theme.bold(text)), "headingStyleFn");
        }
        const headingStyleContext = {
          applyText: headingStyleFn,
          stylePrefix: this.getStylePrefix(headingStyleFn)
        };
        const headingText = this.renderInlineTokens(token.tokens || [], headingStyleContext);
        const styledHeading = headingLevel >= 3 ? headingStyleFn(headingPrefix) + headingText : headingText;
        lines.push(styledHeading);
        if (nextTokenType && nextTokenType !== "space") {
          lines.push("");
        }
        break;
      }
      case "paragraph": {
        const paragraphText = this.renderInlineTokens(token.tokens || [], styleContext);
        lines.push(paragraphText);
        if (nextTokenType && nextTokenType !== "list" && nextTokenType !== "space") {
          lines.push("");
        }
        break;
      }
      case "text":
        lines.push(this.renderInlineTokens([token], styleContext));
        break;
      case "latexBlock": {
        const latexToken = token;
        const rendered = !latexToken.pending && this.options.renderLatex !== false ? renderLatex(latexToken.text, { display: true }) ?? latexToken.raw.trim() : latexToken.raw.trim();
        for (const line of rendered.split("\n")) {
          lines.push(this.applyDefaultStyle(line));
        }
        if (nextTokenType && nextTokenType !== "space") {
          lines.push("");
        }
        break;
      }
      case "code": {
        const indent = this.theme.codeBlockIndent ?? "  ";
        lines.push(this.theme.codeBlockBorder(`\`\`\`${token.lang || ""}`));
        if (this.theme.highlightCode) {
          const highlightedLines = this.theme.highlightCode(token.text, token.lang);
          for (const hlLine of highlightedLines) {
            lines.push(`${indent}${hlLine}`);
          }
        } else {
          const codeLines = token.text.split("\n");
          for (const codeLine of codeLines) {
            lines.push(`${indent}${this.theme.codeBlock(codeLine)}`);
          }
        }
        lines.push(this.theme.codeBlockBorder("```"));
        if (nextTokenType && nextTokenType !== "space") {
          lines.push("");
        }
        break;
      }
      case "list": {
        const listLines = this.renderList(token, 0, width, styleContext);
        lines.push(...listLines);
        break;
      }
      case "table": {
        const tableLines = this.renderTable(token, width, nextTokenType, styleContext);
        lines.push(...tableLines);
        break;
      }
      case "blockquote": {
        const quoteStyle = /* @__PURE__ */ __name((text) => this.theme.quote(this.theme.italic(text)), "quoteStyle");
        const quoteStylePrefix = this.getStylePrefix(quoteStyle);
        const applyQuoteStyle = /* @__PURE__ */ __name((line) => {
          if (!quoteStylePrefix) {
            return quoteStyle(line);
          }
          const lineWithReappliedStyle = line.replace(/\x1b\[0m/g, `\x1B[0m${quoteStylePrefix}`);
          return quoteStyle(lineWithReappliedStyle);
        }, "applyQuoteStyle");
        const quoteContentWidth = Math.max(1, width - 2);
        const quoteInlineStyleContext = {
          applyText: /* @__PURE__ */ __name((text) => text, "applyText"),
          stylePrefix: quoteStylePrefix
        };
        const quoteTokens = token.tokens || [];
        const renderedQuoteLines = [];
        for (let i = 0; i < quoteTokens.length; i++) {
          const quoteToken = quoteTokens[i];
          const nextQuoteToken = quoteTokens[i + 1];
          renderedQuoteLines.push(...this.renderToken(quoteToken, quoteContentWidth, nextQuoteToken?.type, quoteInlineStyleContext));
        }
        while (renderedQuoteLines.length > 0 && renderedQuoteLines[renderedQuoteLines.length - 1] === "") {
          renderedQuoteLines.pop();
        }
        for (const quoteLine of renderedQuoteLines) {
          const styledLine = applyQuoteStyle(quoteLine);
          const wrappedLines = wrapTextWithAnsi2(styledLine, quoteContentWidth);
          for (const wrappedLine of wrappedLines) {
            lines.push(this.theme.quoteBorder("\u2502 ") + wrappedLine);
          }
        }
        if (nextTokenType && nextTokenType !== "space") {
          lines.push("");
        }
        break;
      }
      case "hr":
        lines.push(this.theme.hr("\u2500".repeat(Math.min(width, 80))));
        if (nextTokenType && nextTokenType !== "space") {
          lines.push("");
        }
        break;
      case "html":
        if ("raw" in token && typeof token.raw === "string") {
          lines.push(this.applyDefaultStyle(token.raw.trim()));
        }
        break;
      case "space":
        lines.push("");
        break;
      default:
        if ("text" in token && typeof token.text === "string") {
          lines.push(token.text);
        }
    }
    return lines;
  }
  renderInlineTokens(tokens, styleContext) {
    let result = "";
    const resolvedStyleContext = styleContext ?? this.getDefaultInlineStyleContext();
    const { applyText, stylePrefix } = resolvedStyleContext;
    const applyTextWithNewlines = /* @__PURE__ */ __name((text) => {
      const segments = text.split("\n");
      return segments.map((segment) => applyText(segment)).join("\n");
    }, "applyTextWithNewlines");
    for (const token of tokens) {
      switch (token.type) {
        case "latex": {
          const latexToken = token;
          const rendered = !latexToken.pending && this.options.renderLatex !== false ? renderLatex(latexToken.text) ?? latexToken.raw : latexToken.raw;
          result += applyTextWithNewlines(rendered);
          break;
        }
        case "escape":
          result += applyTextWithNewlines(this.options.preserveBackslashEscapes ? token.raw : token.text);
          break;
        case "text":
          if (token.tokens && token.tokens.length > 0) {
            result += this.renderInlineTokens(token.tokens, resolvedStyleContext);
          } else {
            result += applyTextWithNewlines(token.text);
          }
          break;
        case "paragraph":
          result += this.renderInlineTokens(token.tokens || [], resolvedStyleContext);
          break;
        case "strong": {
          const boldContent = this.renderInlineTokens(token.tokens || [], resolvedStyleContext);
          result += this.theme.bold(boldContent) + stylePrefix;
          break;
        }
        case "em": {
          const italicContent = this.renderInlineTokens(token.tokens || [], resolvedStyleContext);
          result += this.theme.italic(italicContent) + stylePrefix;
          break;
        }
        case "codespan":
          result += this.theme.code(token.text) + stylePrefix;
          break;
        case "link": {
          const linkText = this.renderInlineTokens(token.tokens || [], resolvedStyleContext);
          const styledLink = this.theme.link(this.theme.underline(linkText));
          if (getCapabilities2().hyperlinks) {
            result += hyperlink(styledLink, token.href) + stylePrefix;
          } else {
            const hrefForComparison = token.href.startsWith("mailto:") ? token.href.slice(7) : token.href;
            if (token.text === token.href || token.text === hrefForComparison) {
              result += styledLink + stylePrefix;
            } else {
              result += styledLink + this.theme.linkUrl(` (${token.href})`) + stylePrefix;
            }
          }
          break;
        }
        case "br":
          result += "\n";
          break;
        case "del": {
          const delContent = this.renderInlineTokens(token.tokens || [], resolvedStyleContext);
          result += this.theme.strikethrough(delContent) + stylePrefix;
          break;
        }
        case "html":
          if ("raw" in token && typeof token.raw === "string") {
            result += applyTextWithNewlines(token.raw);
          }
          break;
        default:
          if ("text" in token && typeof token.text === "string") {
            result += applyTextWithNewlines(token.text);
          }
      }
    }
    while (stylePrefix && result.endsWith(stylePrefix)) {
      result = result.slice(0, -stylePrefix.length);
    }
    return result;
  }
  getOrderedListMarker(item) {
    const match = /^(?: {0,3})(\d{1,9}[.)])[ \t]+/.exec(item.raw);
    return match ? `${match[1]} ` : void 0;
  }
  getUnorderedListMarker(item) {
    const match = /^(?: {0,3})([-+*])(?:[ \t]+|(?=\r?\n|$))/.exec(item.raw);
    return match ? `${match[1]} ` : void 0;
  }
  /**
   * Render a list with proper nesting support
   */
  renderList(token, depth, width, styleContext) {
    const lines = [];
    const indent = "    ".repeat(depth);
    const startNumber = typeof token.start === "number" ? token.start : 1;
    for (let i = 0; i < token.items.length; i++) {
      const item = token.items[i];
      const isLastItem = i === token.items.length - 1;
      const bullet = token.ordered ? this.options.preserveOrderedListMarkers ? this.getOrderedListMarker(item) ?? `${startNumber + i}. ` : `${startNumber + i}. ` : this.options.preserveOrderedListMarkers ? this.getUnorderedListMarker(item) ?? "- " : "- ";
      const taskMarker = item.task ? `[${item.checked ? "x" : " "}] ` : "";
      const marker = bullet + taskMarker;
      const firstPrefix = indent + this.theme.listBullet(marker);
      const continuationPrefix = indent + " ".repeat(visibleWidth8(marker));
      const itemWidth = Math.max(1, width - visibleWidth8(firstPrefix));
      let renderedAnyLine = false;
      for (const itemToken of item.tokens) {
        if (itemToken.type === "list") {
          lines.push(...this.renderList(itemToken, depth + 1, width, styleContext));
          renderedAnyLine = true;
          continue;
        }
        const itemLines = this.renderToken(itemToken, itemWidth, void 0, styleContext);
        for (const line of itemLines) {
          for (const wrappedLine of wrapTextWithAnsi2(line, itemWidth)) {
            const linePrefix = renderedAnyLine ? continuationPrefix : firstPrefix;
            lines.push(linePrefix + wrappedLine);
            renderedAnyLine = true;
          }
        }
      }
      if (!renderedAnyLine) {
        lines.push(firstPrefix);
      }
      if (token.loose && !isLastItem) {
        lines.push("");
      }
    }
    return lines;
  }
  /**
   * Get the visible width of the longest word in a string.
   */
  getLongestWordWidth(text, maxWidth) {
    const words = text.split(/\s+/).filter((word) => word.length > 0);
    let longest = 0;
    for (const word of words) {
      longest = Math.max(longest, visibleWidth8(word));
    }
    if (maxWidth === void 0) {
      return longest;
    }
    return Math.min(longest, maxWidth);
  }
  /**
   * Wrap a table cell to fit into a column.
   *
   * Delegates to wrapTextWithAnsi() so ANSI codes + long tokens are handled
   * consistently with the rest of the renderer.
   */
  wrapCellText(text, maxWidth, stylePrefix = "") {
    const lines = wrapTextWithAnsi2(text, Math.max(1, maxWidth));
    return lines.map((line, index) => {
      const styleReset = index < lines.length - 1 ? "\x1B[22;23;24;25;27;28;29;39m" : "";
      return `${line}${styleReset}${stylePrefix}`;
    });
  }
  /**
   * Render a table with width-aware cell wrapping.
   * Cells that don't fit are wrapped to multiple lines.
   */
  renderTable(token, availableWidth, nextTokenType, styleContext) {
    const lines = [];
    const numCols = token.header.length;
    if (numCols === 0) {
      return lines;
    }
    const borderOverhead = 3 * numCols + 1;
    const availableForCells = availableWidth - borderOverhead;
    if (availableForCells < numCols) {
      const fallbackLines = token.raw ? wrapTextWithAnsi2(token.raw, availableWidth) : [];
      if (nextTokenType && nextTokenType !== "space") {
        fallbackLines.push("");
      }
      return fallbackLines;
    }
    const maxUnbrokenWordWidth = 30;
    const naturalWidths = [];
    const minWordWidths = [];
    for (let i = 0; i < numCols; i++) {
      const headerText = this.renderInlineTokens(token.header[i].tokens || [], styleContext);
      naturalWidths[i] = visibleWidth8(headerText);
      minWordWidths[i] = Math.max(1, this.getLongestWordWidth(headerText, maxUnbrokenWordWidth));
    }
    for (const row of token.rows) {
      for (let i = 0; i < row.length; i++) {
        const cellText = this.renderInlineTokens(row[i].tokens || [], styleContext);
        naturalWidths[i] = Math.max(naturalWidths[i] || 0, visibleWidth8(cellText));
        minWordWidths[i] = Math.max(minWordWidths[i] || 1, this.getLongestWordWidth(cellText, maxUnbrokenWordWidth));
      }
    }
    let minColumnWidths = minWordWidths;
    let minCellsWidth = minColumnWidths.reduce((a, b) => a + b, 0);
    if (minCellsWidth > availableForCells) {
      minColumnWidths = new Array(numCols).fill(1);
      const remaining = availableForCells - numCols;
      if (remaining > 0) {
        const totalWeight = minWordWidths.reduce((total, width) => total + Math.max(0, width - 1), 0);
        const growth = minWordWidths.map((width) => {
          const weight = Math.max(0, width - 1);
          return totalWeight > 0 ? Math.floor(weight / totalWeight * remaining) : 0;
        });
        for (let i = 0; i < numCols; i++) {
          minColumnWidths[i] += growth[i] ?? 0;
        }
        const allocated = growth.reduce((total, width) => total + width, 0);
        let leftover = remaining - allocated;
        for (let i = 0; leftover > 0 && i < numCols; i++) {
          minColumnWidths[i]++;
          leftover--;
        }
      }
      minCellsWidth = minColumnWidths.reduce((a, b) => a + b, 0);
    }
    const totalNaturalWidth = naturalWidths.reduce((a, b) => a + b, 0) + borderOverhead;
    let columnWidths;
    if (totalNaturalWidth <= availableWidth) {
      columnWidths = naturalWidths.map((width, index) => Math.max(width, minColumnWidths[index]));
    } else {
      const totalGrowPotential = naturalWidths.reduce((total, width, index) => {
        return total + Math.max(0, width - minColumnWidths[index]);
      }, 0);
      const extraWidth = Math.max(0, availableForCells - minCellsWidth);
      columnWidths = minColumnWidths.map((minWidth, index) => {
        const naturalWidth = naturalWidths[index];
        const minWidthDelta = Math.max(0, naturalWidth - minWidth);
        let grow = 0;
        if (totalGrowPotential > 0) {
          grow = Math.floor(minWidthDelta / totalGrowPotential * extraWidth);
        }
        return minWidth + grow;
      });
      const allocated = columnWidths.reduce((a, b) => a + b, 0);
      let remaining = availableForCells - allocated;
      while (remaining > 0) {
        let grew = false;
        for (let i = 0; i < numCols && remaining > 0; i++) {
          if (columnWidths[i] < naturalWidths[i]) {
            columnWidths[i]++;
            remaining--;
            grew = true;
          }
        }
        if (!grew) {
          break;
        }
      }
    }
    const topBorderCells = columnWidths.map((w) => "\u2500".repeat(w));
    lines.push(`\u250C\u2500${topBorderCells.join("\u2500\u252C\u2500")}\u2500\u2510`);
    const headerCellLines = token.header.map((cell, i) => {
      const text = this.renderInlineTokens(cell.tokens || [], styleContext);
      return this.wrapCellText(text, columnWidths[i], styleContext?.stylePrefix);
    });
    const headerLineCount = Math.max(...headerCellLines.map((c) => c.length));
    for (let lineIdx = 0; lineIdx < headerLineCount; lineIdx++) {
      const rowParts = headerCellLines.map((cellLines, colIdx) => {
        const text = cellLines[lineIdx] || "";
        const padded = text + " ".repeat(Math.max(0, columnWidths[colIdx] - visibleWidth8(text)));
        return this.theme.bold(padded);
      });
      lines.push(`\u2502 ${rowParts.join(" \u2502 ")} \u2502`);
    }
    const separatorCells = columnWidths.map((w) => "\u2500".repeat(w));
    const separatorLine = `\u251C\u2500${separatorCells.join("\u2500\u253C\u2500")}\u2500\u2524`;
    lines.push(separatorLine);
    for (let rowIndex = 0; rowIndex < token.rows.length; rowIndex++) {
      const row = token.rows[rowIndex];
      const rowCellLines = row.map((cell, i) => {
        const text = this.renderInlineTokens(cell.tokens || [], styleContext);
        return this.wrapCellText(text, columnWidths[i], styleContext?.stylePrefix);
      });
      const rowLineCount = Math.max(...rowCellLines.map((c) => c.length));
      for (let lineIdx = 0; lineIdx < rowLineCount; lineIdx++) {
        const rowParts = rowCellLines.map((cellLines, colIdx) => {
          const text = cellLines[lineIdx] || "";
          return text + " ".repeat(Math.max(0, columnWidths[colIdx] - visibleWidth8(text)));
        });
        lines.push(`\u2502 ${rowParts.join(" \u2502 ")} \u2502`);
      }
      if (rowIndex < token.rows.length - 1) {
        lines.push(separatorLine);
      }
    }
    const bottomBorderCells = columnWidths.map((w) => "\u2500".repeat(w));
    lines.push(`\u2514\u2500${bottomBorderCells.join("\u2500\u2534\u2500")}\u2500\u2518`);
    if (nextTokenType && nextTokenType !== "space") {
      lines.push("");
    }
    return lines;
  }
};

// pi-dist/pi-tui/components/mouse-region.js
import { dispatchMouseEvent as dispatchMouseEvent2 } from "../tui.js";
var MouseRegion = class {
  static {
    __name(this, "MouseRegion");
  }
  child;
  onMouse;
  constructor(child, onMouse) {
    this.child = child;
    this.onMouse = onMouse;
  }
  render(width) {
    return this.child.render(width);
  }
  handleMouse(event) {
    const childResult = dispatchMouseEvent2(this.child, event);
    return childResult ?? this.onMouse(event);
  }
  invalidate() {
    this.child.invalidate();
  }
};

// pi-dist/pi-tui/components/scroll-view.js
import { Container as Container2 } from "../tui.js";
var ScrollView = class extends Container2 {
  static {
    __name(this, "ScrollView");
  }
  child;
  followEnd;
  primary;
  overscroll;
  scrollbarTrackStyle;
  scrollbarThumbStyle;
  currentScrollbar;
  scrollbarHideDelayMs;
  currentScrollTop = 0;
  contentHeight = 0;
  currentViewportHeight = 0;
  followingEnd;
  followSuppressedAtEnd = false;
  requestRenderCallback;
  transientScrollbarVisible = false;
  scrollbarActive = false;
  scrollbarHideTimer;
  constructor(component, options = {}) {
    super();
    if (options.axis !== void 0 && options.axis !== "vertical") {
      throw new Error(`Unsupported ScrollView axis: ${options.axis}`);
    }
    this.child = component;
    this.children.push(component);
    this.followEnd = (options.follow ?? "none") === "end";
    this.followingEnd = this.followEnd;
    this.primary = options.primary ?? false;
    this.overscroll = options.overscroll ?? "chain";
    this.currentScrollbar = options.scrollbar ?? "hidden";
    this.scrollbarTrackStyle = options.scrollbarTrackStyle ?? ((text) => `\x1B[90m${text}\x1B[39m`);
    this.scrollbarThumbStyle = options.scrollbarThumbStyle ?? ((text) => `\x1B[37m${text}\x1B[39m`);
    this.scrollbarHideDelayMs = Math.max(0, Math.floor(options.scrollbarHideDelayMs ?? 1e3));
  }
  get scrollTop() {
    return this.currentScrollTop;
  }
  get isFollowingEnd() {
    return this.followingEnd;
  }
  get viewportHeight() {
    return this.currentViewportHeight;
  }
  get scrollbar() {
    return this.currentScrollbar;
  }
  get isScrollbarVisible() {
    if (this.scrollbar === "always")
      return this.currentViewportHeight > 0;
    return this.scrollbar === "auto" && this.contentHeight > this.currentViewportHeight && this.transientScrollbarVisible;
  }
  get isScrollbarActive() {
    return this.scrollbarActive;
  }
  setScrollbar(scrollbar) {
    if (scrollbar === this.currentScrollbar)
      return;
    this.currentScrollbar = scrollbar;
    if (scrollbar !== "auto")
      this.hideTransientScrollbar();
    else if (this.scrollbarActive)
      this.markScrollbarActivity();
    this.requestRenderCallback?.();
  }
  getContentWidth(width) {
    return this.scrollbar === "always" && width > 1 ? width - 1 : width;
  }
  markScrollbarActivity() {
    if (this.scrollbar !== "auto" || this.contentHeight <= this.currentViewportHeight)
      return;
    this.transientScrollbarVisible = true;
    if (this.scrollbarHideTimer) {
      clearTimeout(this.scrollbarHideTimer);
      this.scrollbarHideTimer = void 0;
    }
    if (this.scrollbarActive)
      return;
    this.scrollbarHideTimer = setTimeout(() => {
      this.scrollbarHideTimer = void 0;
      this.transientScrollbarVisible = false;
      this.requestRenderCallback?.();
    }, this.scrollbarHideDelayMs);
    this.scrollbarHideTimer.unref();
  }
  hideTransientScrollbar() {
    this.transientScrollbarVisible = false;
    if (!this.scrollbarHideTimer)
      return;
    clearTimeout(this.scrollbarHideTimer);
    this.scrollbarHideTimer = void 0;
  }
  setScrollbarActive(active) {
    if (active === this.scrollbarActive)
      return;
    this.scrollbarActive = active;
    this.markScrollbarActivity();
    this.requestRenderCallback?.();
  }
  scrollTo(scrollTop, options = {}) {
    const requested = Number.isFinite(scrollTop) ? Math.trunc(scrollTop) : this.currentScrollTop;
    const maxScrollTop = Math.max(0, this.contentHeight - this.currentViewportHeight);
    const next = Math.max(0, Math.min(maxScrollTop, requested));
    const nextFollowSuppressedAtEnd = options.disableFollow === true && next === maxScrollTop;
    const nextFollowingEnd = !nextFollowSuppressedAtEnd && this.followEnd && next === maxScrollTop;
    if (next === this.currentScrollTop && nextFollowingEnd === this.followingEnd && nextFollowSuppressedAtEnd === this.followSuppressedAtEnd) {
      return;
    }
    const moved = next !== this.currentScrollTop;
    this.currentScrollTop = next;
    this.followingEnd = nextFollowingEnd;
    this.followSuppressedAtEnd = nextFollowSuppressedAtEnd;
    if (moved)
      this.markScrollbarActivity();
    this.requestRenderCallback?.();
  }
  scrollBy(lines) {
    const requested = Number.isFinite(lines) ? Math.trunc(lines) : 0;
    if (requested === 0)
      return 0;
    const maxScrollTop = Math.max(0, this.contentHeight - this.currentViewportHeight);
    const start = this.followingEnd ? maxScrollTop : this.currentScrollTop;
    const next = Math.max(0, Math.min(maxScrollTop, start + requested));
    const moved = next - start;
    const wasFollowingEnd = this.followingEnd;
    this.currentScrollTop = next;
    this.followingEnd = this.followEnd && next === maxScrollTop;
    this.followSuppressedAtEnd = false;
    if (moved !== 0)
      this.markScrollbarActivity();
    if (moved !== 0 || this.followingEnd !== wasFollowingEnd)
      this.requestRenderCallback?.();
    return requested - moved;
  }
  scrollToStart() {
    const changed = this.currentScrollTop !== 0 || this.followingEnd !== (this.followEnd && this.contentHeight <= this.currentViewportHeight);
    this.currentScrollTop = 0;
    this.followingEnd = this.followEnd && this.contentHeight <= this.currentViewportHeight;
    this.followSuppressedAtEnd = false;
    if (changed) {
      this.markScrollbarActivity();
      this.requestRenderCallback?.();
    }
  }
  scrollToEnd() {
    const next = Math.max(0, this.contentHeight - this.currentViewportHeight);
    const changed = this.currentScrollTop !== next || this.followingEnd !== this.followEnd;
    this.currentScrollTop = next;
    this.followingEnd = this.followEnd;
    this.followSuppressedAtEnd = false;
    if (changed) {
      this.markScrollbarActivity();
      this.requestRenderCallback?.();
    }
  }
  updateLayout(contentHeight, viewportHeight, requestRender) {
    this.contentHeight = Math.max(0, Math.floor(contentHeight));
    this.currentViewportHeight = Math.max(0, Math.floor(viewportHeight));
    this.requestRenderCallback = requestRender;
    const maxScrollTop = Math.max(0, this.contentHeight - this.currentViewportHeight);
    if (this.followingEnd)
      this.currentScrollTop = maxScrollTop;
    else
      this.currentScrollTop = Math.max(0, Math.min(this.currentScrollTop, maxScrollTop));
    if (this.currentScrollTop < maxScrollTop)
      this.followSuppressedAtEnd = false;
    if (this.followEnd && this.currentScrollTop === maxScrollTop && !this.followSuppressedAtEnd) {
      this.followingEnd = true;
    }
    if (this.contentHeight <= this.currentViewportHeight)
      this.hideTransientScrollbar();
  }
  addChild(_component) {
    throw new Error("ScrollView has exactly one child");
  }
  removeChild(_component) {
    throw new Error("ScrollView child cannot be removed");
  }
  clear() {
    throw new Error("ScrollView child cannot be cleared");
  }
  render(width) {
    const contentWidth = this.getContentWidth(width);
    const lines = this.child.render(contentWidth);
    return contentWidth === width ? lines : lines.map((line) => `${line} `);
  }
  [LAYOUT_NODE]() {
    return { type: "scroll", component: this.child, state: this };
  }
};

// pi-dist/pi-tui/components/settings-list.js
import { getKeybindings as getKeybindings5 } from "../keybindings.js";
import { truncateToWidth as truncateToWidth4, visibleWidth as visibleWidth9, wrapTextWithAnsi as wrapTextWithAnsi3 } from "../utils.js";
var SettingsList = class {
  static {
    __name(this, "SettingsList");
  }
  items;
  filteredItems;
  theme;
  selectedIndex = 0;
  mousePressedIndex;
  maxVisible;
  onChange;
  onCancel;
  searchInput;
  searchEnabled;
  // Submenu state
  submenuComponent = null;
  submenuItemIndex = null;
  navigateAfterClose = null;
  constructor(items, maxVisible, theme, onChange, onCancel, options = {}) {
    this.items = items;
    this.filteredItems = items;
    this.maxVisible = maxVisible;
    this.theme = theme;
    this.onChange = onChange;
    this.onCancel = onCancel;
    this.searchEnabled = options.enableSearch ?? false;
    if (this.searchEnabled) {
      this.searchInput = new Input();
    }
  }
  /** Update an item's currentValue */
  updateValue(id, newValue) {
    const item = this.items.find((i) => i.id === id);
    if (item) {
      item.currentValue = newValue;
    }
  }
  /** Move selection to the item with the given id (no-op if not found). */
  selectItem(id) {
    const items = this.searchEnabled ? this.filteredItems : this.items;
    const index = items.findIndex((i) => i.id === id);
    if (index !== -1) {
      this.selectedIndex = index;
    }
  }
  invalidate() {
    this.submenuComponent?.invalidate?.();
  }
  render(width) {
    if (this.submenuComponent) {
      return this.submenuComponent.render(width);
    }
    return this.renderMainList(width);
  }
  renderMainList(width) {
    const lines = [];
    if (this.searchEnabled && this.searchInput) {
      lines.push(...this.searchInput.render(width));
      lines.push("");
    }
    if (this.items.length === 0) {
      lines.push(this.theme.hint("  No settings available"));
      if (this.searchEnabled) {
        this.addHintLine(lines, width);
      }
      return lines;
    }
    const displayItems = this.getDisplayItems();
    if (displayItems.length === 0) {
      lines.push(truncateToWidth4(this.theme.hint("  No matching settings"), width));
      this.addHintLine(lines, width);
      return lines;
    }
    const { startIndex, endIndex } = this.getVisibleRange(displayItems);
    const maxLabelWidth = Math.min(36, Math.max(...this.items.map((item) => visibleWidth9(item.label))));
    for (let i = startIndex; i < endIndex; i++) {
      const item = displayItems[i];
      if (!item)
        continue;
      const isSelected = i === this.selectedIndex;
      const prefix = isSelected ? this.theme.cursor : "  ";
      const prefixWidth = visibleWidth9(prefix);
      const labelPadded = item.label + " ".repeat(Math.max(0, maxLabelWidth - visibleWidth9(item.label)));
      const labelText = this.theme.label(labelPadded, isSelected);
      const separator = "  ";
      const usedWidth = prefixWidth + maxLabelWidth + visibleWidth9(separator);
      const valueMaxWidth = width - usedWidth - 2;
      const valueText = this.theme.value(truncateToWidth4(item.currentValue, valueMaxWidth, ""), isSelected);
      lines.push(truncateToWidth4(prefix + labelText + separator + valueText, width));
    }
    if (startIndex > 0 || endIndex < displayItems.length) {
      const scrollText = `  (${this.selectedIndex + 1}/${displayItems.length})`;
      lines.push(this.theme.hint(truncateToWidth4(scrollText, width - 2, "")));
    }
    const selectedItem = displayItems[this.selectedIndex];
    if (selectedItem?.description) {
      lines.push("");
      const wrappedDesc = wrapTextWithAnsi3(selectedItem.description, width - 4);
      for (const line of wrappedDesc) {
        lines.push(this.theme.description(`  ${line}`));
      }
    }
    this.addHintLine(lines, width);
    return lines;
  }
  handleMouse(event) {
    if (this.submenuComponent) {
      const result = this.submenuComponent.handleMouse?.(event);
      return result ? { ...result, focus: true } : void 0;
    }
    if (this.searchEnabled && this.searchInput) {
      if (event.y === 0) {
        const result = this.searchInput.handleMouse?.(event);
        return result ? { ...result, focus: true } : void 0;
      }
      if (event.y === 1)
        return void 0;
    }
    const displayItems = this.getDisplayItems();
    if (displayItems.length === 0)
      return void 0;
    if (event.type === "wheel" && event.wheelDelta) {
      const delta = event.wheelDelta < 0 ? -1 : 1;
      const previousIndex = this.selectedIndex;
      this.selectedIndex = Math.max(0, Math.min(displayItems.length - 1, this.selectedIndex + delta));
      return { handled: true, render: this.selectedIndex !== previousIndex };
    }
    if (event.button !== "left" || event.type !== "press" && event.type !== "click")
      return void 0;
    const rowOffset = this.searchEnabled ? 2 : 0;
    const { startIndex, endIndex } = this.getVisibleRange(displayItems);
    const itemIndex = startIndex + event.y - rowOffset;
    if (itemIndex < startIndex || itemIndex >= endIndex)
      return void 0;
    if (event.type === "press") {
      this.mousePressedIndex = itemIndex;
      this.selectedIndex = itemIndex;
      return { handled: true, focus: true };
    }
    if (event.type === "click") {
      this.selectedIndex = this.mousePressedIndex ?? itemIndex;
      this.mousePressedIndex = void 0;
      this.activateItem();
      return { handled: true };
    }
    return void 0;
  }
  handleInput(data) {
    if (this.submenuComponent) {
      this.submenuComponent.handleInput?.(data);
      return;
    }
    const kb = getKeybindings5();
    const displayItems = this.getDisplayItems();
    if (kb.matches(data, "tui.select.up")) {
      if (displayItems.length === 0)
        return;
      this.selectedIndex = this.selectedIndex === 0 ? displayItems.length - 1 : this.selectedIndex - 1;
    } else if (kb.matches(data, "tui.select.down")) {
      if (displayItems.length === 0)
        return;
      this.selectedIndex = this.selectedIndex === displayItems.length - 1 ? 0 : this.selectedIndex + 1;
    } else if (kb.matches(data, "tui.select.confirm") || data === " " && (!this.searchEnabled || this.searchInput?.getValue().length === 0)) {
      this.activateItem();
    } else if (kb.matches(data, "tui.select.cancel")) {
      this.onCancel();
    } else if (this.searchEnabled && this.searchInput) {
      this.searchInput.handleInput(data);
      this.applyFilter(this.searchInput.getValue());
    }
  }
  getDisplayItems() {
    return this.searchEnabled ? this.filteredItems : this.items;
  }
  getVisibleRange(displayItems) {
    const startIndex = Math.max(0, Math.min(this.selectedIndex - Math.floor(this.maxVisible / 2), displayItems.length - this.maxVisible));
    return { startIndex, endIndex: Math.min(startIndex + this.maxVisible, displayItems.length) };
  }
  activateItem() {
    const item = this.getDisplayItems()[this.selectedIndex];
    if (!item)
      return;
    if (item.submenu) {
      this.submenuItemIndex = this.selectedIndex;
      this.submenuComponent = item.submenu(item.currentValue, (selectedValue, options) => {
        if (selectedValue !== void 0) {
          item.currentValue = selectedValue;
          this.onChange(item.id, selectedValue);
        }
        if (options?.navigateTo) {
          this.navigateAfterClose = options.navigateTo;
        }
        this.closeSubmenu();
      });
    } else if (item.values && item.values.length > 0) {
      const currentIndex = item.values.indexOf(item.currentValue);
      const nextIndex = (currentIndex + 1) % item.values.length;
      const newValue = item.values[nextIndex];
      item.currentValue = newValue;
      this.onChange(item.id, newValue);
    }
  }
  closeSubmenu() {
    this.submenuComponent = null;
    if (this.navigateAfterClose !== null) {
      const id = this.navigateAfterClose;
      this.navigateAfterClose = null;
      this.submenuItemIndex = null;
      this.selectItem(id);
      this.activateItem();
    } else if (this.submenuItemIndex !== null) {
      this.selectedIndex = this.submenuItemIndex;
      this.submenuItemIndex = null;
    }
  }
  applyFilter(query) {
    this.filteredItems = fuzzyFilter(this.items, query, (item) => item.label);
    this.selectedIndex = 0;
  }
  addHintLine(lines, width) {
    lines.push("");
    lines.push(truncateToWidth4(this.theme.hint(this.searchEnabled ? "  Type to search \xB7 Enter/Space to change \xB7 Esc to cancel" : "  Enter/Space to change \xB7 Esc to cancel"), width));
  }
};

// pi-dist/pi-tui/components/spacer.js
var Spacer = class {
  static {
    __name(this, "Spacer");
  }
  lines;
  constructor(lines = 1) {
    this.lines = lines;
  }
  setLines(lines) {
    this.lines = lines;
  }
  invalidate() {
  }
  render(_width) {
    const result = [];
    for (let i = 0; i < this.lines; i++) {
      result.push("");
    }
    return result;
  }
};

// pi-dist/pi-tui/components/truncated-text.js
import { truncateToWidth as truncateToWidth5, visibleWidth as visibleWidth10 } from "../utils.js";
var TruncatedText = class {
  static {
    __name(this, "TruncatedText");
  }
  text;
  paddingX;
  paddingY;
  constructor(text, paddingX = 0, paddingY = 0) {
    this.text = text;
    this.paddingX = paddingX;
    this.paddingY = paddingY;
  }
  invalidate() {
  }
  render(width) {
    const result = [];
    const emptyLine = " ".repeat(width);
    for (let i = 0; i < this.paddingY; i++) {
      result.push(emptyLine);
    }
    const availableWidth = Math.max(1, width - this.paddingX * 2);
    let singleLineText = this.text;
    const newlineIndex = this.text.indexOf("\n");
    if (newlineIndex !== -1) {
      singleLineText = this.text.substring(0, newlineIndex);
    }
    const displayText = truncateToWidth5(singleLineText, availableWidth);
    const leftPadding = " ".repeat(this.paddingX);
    const rightPadding = " ".repeat(this.paddingX);
    const lineWithPadding = leftPadding + displayText + rightPadding;
    const lineVisibleWidth = visibleWidth10(lineWithPadding);
    const paddingNeeded = Math.max(0, width - lineVisibleWidth);
    const finalLine = lineWithPadding + " ".repeat(paddingNeeded);
    result.push(finalLine);
    for (let i = 0; i < this.paddingY; i++) {
      result.push(emptyLine);
    }
    return result;
  }
};

// pi-dist/pi-tui/components/v-stack.js
var VStack = class extends Stack {
  static {
    __name(this, "VStack");
  }
  layoutType = "vstack";
  constructor(children = [], options = {}) {
    super(children, options);
  }
  render(width) {
    const viewport = { width: Math.max(1, width), height: Number.MAX_SAFE_INTEGER };
    const entries = visibleStackEntries(this.entries, viewport);
    const rendered = entries.map((entry) => entry.component.render(viewport.width));
    const sizes = allocateStackSizes(entries, rendered.map((lines2) => lines2.length), void 0, this.gap);
    const lines = [];
    for (let index = 0; index < entries.length; index++) {
      if (index > 0) {
        for (let gap = 0; gap < this.gap; gap++)
          lines.push("");
      }
      const childLines = rendered[index].slice(0, sizes[index]);
      lines.push(...childLines);
      for (let padding = childLines.length; padding < sizes[index]; padding++)
        lines.push("");
    }
    return lines;
  }
};

// pi-dist/pi-tui/index.js
import { getKeybindings as getKeybindings8, KeybindingsManager, setKeybindings, TUI_KEYBINDINGS } from "../keybindings.js";
import { decodeKittyPrintable as decodeKittyPrintable2, isKeyRelease as isKeyRelease2, isKeyRepeat, isKittyProtocolActive, Key, matchesKey as matchesKey2, parseKey, setKittyProtocolActive as setKittyProtocolActive2 } from "../keys.js";
import { getNativeClipboard } from "../native-platform.js";

// pi-dist/pi-tui/stdin-buffer.js
import { EventEmitter } from "events";
var ESC = "\x1B";
var DEFAULT_SEQUENCE_TIMEOUT_MS = 50;
var DEFAULT_ESCAPE_TIMEOUT_MS = 10;
var BRACKETED_PASTE_START = "\x1B[200~";
var BRACKETED_PASTE_END = "\x1B[201~";
function isCompleteSequence(data) {
  if (!data.startsWith(ESC)) {
    return "not-escape";
  }
  if (data.length === 1) {
    return "incomplete";
  }
  const afterEsc = data.slice(1);
  if (afterEsc.startsWith("[")) {
    if (afterEsc.startsWith("[M")) {
      return data.length >= 6 ? "complete" : "incomplete";
    }
    return isCompleteCsiSequence(data);
  }
  if (afterEsc.startsWith("]")) {
    return isCompleteOscSequence(data);
  }
  if (afterEsc.startsWith("P")) {
    return isCompleteDcsSequence(data);
  }
  if (afterEsc.startsWith("_")) {
    return isCompleteApcSequence(data);
  }
  if (afterEsc.startsWith("O")) {
    return afterEsc.length >= 2 ? "complete" : "incomplete";
  }
  if (afterEsc.length === 1) {
    return "complete";
  }
  return "complete";
}
__name(isCompleteSequence, "isCompleteSequence");
function isCompleteCsiSequence(data) {
  if (!data.startsWith(`${ESC}[`)) {
    return "complete";
  }
  if (data.length < 3) {
    return "incomplete";
  }
  const payload = data.slice(2);
  const lastChar = payload[payload.length - 1];
  const lastCharCode = lastChar.charCodeAt(0);
  if (lastCharCode >= 64 && lastCharCode <= 126) {
    if (payload.startsWith("<")) {
      const mouseMatch = /^<\d+;\d+;\d+[Mm]$/.test(payload);
      if (mouseMatch) {
        return "complete";
      }
      if (lastChar === "M" || lastChar === "m") {
        const parts = payload.slice(1, -1).split(";");
        if (parts.length === 3 && parts.every((p) => /^\d+$/.test(p))) {
          return "complete";
        }
      }
      return "incomplete";
    }
    return "complete";
  }
  return "incomplete";
}
__name(isCompleteCsiSequence, "isCompleteCsiSequence");
function isCompleteOscSequence(data) {
  if (!data.startsWith(`${ESC}]`)) {
    return "complete";
  }
  if (data.endsWith(`${ESC}\\`) || data.endsWith("\x07")) {
    return "complete";
  }
  return "incomplete";
}
__name(isCompleteOscSequence, "isCompleteOscSequence");
function isCompleteDcsSequence(data) {
  if (!data.startsWith(`${ESC}P`)) {
    return "complete";
  }
  if (data.endsWith(`${ESC}\\`)) {
    return "complete";
  }
  return "incomplete";
}
__name(isCompleteDcsSequence, "isCompleteDcsSequence");
function isCompleteApcSequence(data) {
  if (!data.startsWith(`${ESC}_`)) {
    return "complete";
  }
  if (data.endsWith(`${ESC}\\`)) {
    return "complete";
  }
  return "incomplete";
}
__name(isCompleteApcSequence, "isCompleteApcSequence");
function parseUnmodifiedKittyPrintableCodepoint(sequence) {
  const match = sequence.match(/^\x1b\[(\d+)(?::\d*)?(?::\d+)?u$/);
  if (!match)
    return void 0;
  const codepoint = parseInt(match[1], 10);
  return codepoint >= 32 ? codepoint : void 0;
}
__name(parseUnmodifiedKittyPrintableCodepoint, "parseUnmodifiedKittyPrintableCodepoint");
function extractCompleteSequences(buffer) {
  const sequences = [];
  let pos = 0;
  while (pos < buffer.length) {
    const remaining = buffer.slice(pos);
    if (remaining.startsWith(ESC)) {
      let seqEnd = 1;
      while (seqEnd <= remaining.length) {
        const candidate = remaining.slice(0, seqEnd);
        const status = isCompleteSequence(candidate);
        if (status === "complete") {
          if (candidate === "\x1B\x1B") {
            const nextChar = remaining[seqEnd];
            if (nextChar === "[" || // CSI
            nextChar === "]" || // OSC
            nextChar === "O" || // SS3
            nextChar === "P" || // DCS
            nextChar === "_") {
              sequences.push(ESC);
              pos += 1;
              break;
            }
          }
          sequences.push(candidate);
          pos += seqEnd;
          break;
        } else if (status === "incomplete") {
          seqEnd++;
        } else {
          sequences.push(candidate);
          pos += seqEnd;
          break;
        }
      }
      if (seqEnd > remaining.length) {
        return { sequences, remainder: remaining };
      }
    } else {
      sequences.push(remaining[0]);
      pos++;
    }
  }
  return { sequences, remainder: "" };
}
__name(extractCompleteSequences, "extractCompleteSequences");
var StdinBuffer = class extends EventEmitter {
  static {
    __name(this, "StdinBuffer");
  }
  buffer = "";
  timeout = null;
  timeoutMs;
  escapeTimeoutMs;
  pasteMode = false;
  pasteBuffer = "";
  pendingKittyPrintableCodepoint;
  constructor(options = {}) {
    super();
    this.timeoutMs = options.timeout ?? DEFAULT_SEQUENCE_TIMEOUT_MS;
    this.escapeTimeoutMs = options.escapeTimeout ?? DEFAULT_ESCAPE_TIMEOUT_MS;
  }
  process(data) {
    if (this.timeout) {
      clearTimeout(this.timeout);
      this.timeout = null;
    }
    let str;
    if (Buffer.isBuffer(data)) {
      if (data.length === 1 && data[0] > 127) {
        const byte = data[0] - 128;
        str = `\x1B${String.fromCharCode(byte)}`;
      } else {
        str = data.toString();
      }
    } else {
      str = data;
    }
    if (str.length === 0 && this.buffer.length === 0) {
      this.emitDataSequence("");
      return;
    }
    this.buffer += str;
    if (this.pasteMode) {
      this.pasteBuffer += this.buffer;
      this.buffer = "";
      const endIndex = this.pasteBuffer.indexOf(BRACKETED_PASTE_END);
      if (endIndex !== -1) {
        const pastedContent = this.pasteBuffer.slice(0, endIndex);
        const remaining = this.pasteBuffer.slice(endIndex + BRACKETED_PASTE_END.length);
        this.pasteMode = false;
        this.pasteBuffer = "";
        this.pendingKittyPrintableCodepoint = void 0;
        this.emit("paste", pastedContent);
        if (remaining.length > 0) {
          this.process(remaining);
        }
      }
      return;
    }
    const startIndex = this.buffer.indexOf(BRACKETED_PASTE_START);
    if (startIndex !== -1) {
      if (startIndex > 0) {
        const beforePaste = this.buffer.slice(0, startIndex);
        const result2 = extractCompleteSequences(beforePaste);
        for (const sequence of result2.sequences) {
          this.emitDataSequence(sequence);
        }
      }
      this.pendingKittyPrintableCodepoint = void 0;
      this.buffer = this.buffer.slice(startIndex + BRACKETED_PASTE_START.length);
      this.pasteMode = true;
      this.pasteBuffer = this.buffer;
      this.buffer = "";
      const endIndex = this.pasteBuffer.indexOf(BRACKETED_PASTE_END);
      if (endIndex !== -1) {
        const pastedContent = this.pasteBuffer.slice(0, endIndex);
        const remaining = this.pasteBuffer.slice(endIndex + BRACKETED_PASTE_END.length);
        this.pasteMode = false;
        this.pasteBuffer = "";
        this.pendingKittyPrintableCodepoint = void 0;
        this.emit("paste", pastedContent);
        if (remaining.length > 0) {
          this.process(remaining);
        }
      }
      return;
    }
    const result = extractCompleteSequences(this.buffer);
    this.buffer = result.remainder;
    for (const sequence of result.sequences) {
      this.emitDataSequence(sequence);
    }
    if (this.buffer.length > 0) {
      const timeoutMs = this.buffer === ESC ? this.escapeTimeoutMs : this.timeoutMs;
      this.timeout = setTimeout(() => {
        const flushed = this.flush();
        for (const sequence of flushed) {
          this.emitDataSequence(sequence);
        }
      }, timeoutMs);
    }
  }
  emitDataSequence(sequence) {
    const rawCodepoint = sequence.length === 1 ? sequence.codePointAt(0) : void 0;
    if (rawCodepoint !== void 0 && rawCodepoint === this.pendingKittyPrintableCodepoint) {
      this.pendingKittyPrintableCodepoint = void 0;
      return;
    }
    this.pendingKittyPrintableCodepoint = parseUnmodifiedKittyPrintableCodepoint(sequence);
    this.emit("data", sequence);
  }
  flush() {
    if (this.timeout) {
      clearTimeout(this.timeout);
      this.timeout = null;
    }
    if (this.buffer.length === 0) {
      return [];
    }
    const sequences = [this.buffer];
    this.buffer = "";
    this.pendingKittyPrintableCodepoint = void 0;
    return sequences;
  }
  clear() {
    if (this.timeout) {
      clearTimeout(this.timeout);
      this.timeout = null;
    }
    this.buffer = "";
    this.pasteMode = false;
    this.pasteBuffer = "";
    this.pendingKittyPrintableCodepoint = void 0;
  }
  getBuffer() {
    return this.buffer;
  }
  destroy() {
    this.clear();
  }
};

// pi-dist/pi-tui/terminal.js
import * as fs from "node:fs";
import * as path from "node:path";
import { setKittyProtocolActive } from "../keys.js";

// pi-dist/pi-tui/native-modifiers.js
import { getNativePlatformHelper } from "../native-platform.js";
function isNativeModifierPressed(key) {
  const helper = getNativePlatformHelper();
  if (!helper?.isModifierPressed)
    return false;
  try {
    return helper.isModifierPressed(key) === true;
  } catch {
    return false;
  }
}
__name(isNativeModifierPressed, "isNativeModifierPressed");

// pi-dist/pi-tui/terminal.js
import { getNativePlatformHelper as getNativePlatformHelper2 } from "../native-platform.js";
var TERMINAL_PROGRESS_KEEPALIVE_MS = 1e3;
var TERMINAL_PROGRESS_ACTIVE_SEQUENCE = "\x1B]9;4;3\x07";
var TERMINAL_PROGRESS_CLEAR_SEQUENCE = "\x1B]9;4;0\x07";
var NATIVE_SHIFT_ENTER_SEQUENCE = "\x1B[13;2u";
var DESIRED_KITTY_KEYBOARD_PROTOCOL_FLAGS = 7;
var KEYBOARD_PROTOCOL_RESPONSE_FRAGMENT_TIMEOUT_MS = 150;
var KITTY_KEYBOARD_PROTOCOL_QUERY = `\x1B[>${DESIRED_KITTY_KEYBOARD_PROTOCOL_FLAGS}u\x1B[?u\x1B[c`;
function parseKeyboardProtocolNegotiationSequence(sequence) {
  const kittyFlags = sequence.match(/^\x1b\[\?(\d+)u$/);
  if (kittyFlags) {
    return { type: "kitty-flags", flags: Number.parseInt(kittyFlags[1], 10) };
  }
  if (/^\x1b\[\?[\d;]*c$/.test(sequence)) {
    return { type: "device-attributes" };
  }
  return void 0;
}
__name(parseKeyboardProtocolNegotiationSequence, "parseKeyboardProtocolNegotiationSequence");
function isKeyboardProtocolNegotiationSequencePrefix(sequence) {
  return sequence === "\x1B[" || /^\x1b\[\?[\d;]*$/.test(sequence);
}
__name(isKeyboardProtocolNegotiationSequencePrefix, "isKeyboardProtocolNegotiationSequencePrefix");
function isAppleTerminalSession() {
  return process.platform === "darwin" && process.env.TERM_PROGRAM === "Apple_Terminal";
}
__name(isAppleTerminalSession, "isAppleTerminalSession");
function refreshTerminalDimensions() {
  if (process.platform === "win32" || process.pid <= 0)
    return;
  try {
    process.kill(process.pid, "SIGWINCH");
  } catch {
  }
}
__name(refreshTerminalDimensions, "refreshTerminalDimensions");
function normalizeNativeShiftEnterInput(data, shouldDetectNativeShiftEnter, isShiftPressed) {
  if (shouldDetectNativeShiftEnter && data === "\r" && isShiftPressed)
    return NATIVE_SHIFT_ENTER_SEQUENCE;
  return data;
}
__name(normalizeNativeShiftEnterInput, "normalizeNativeShiftEnterInput");
var DEFAULT_ESCAPE_TIMEOUT_MS2 = 10;
var DEFAULT_SSH_ESCAPE_TIMEOUT_MS = 100;
function resolveEscapeTimeoutMs(env = process.env) {
  const configured = Number(env.PI_TUI_ESC_TIMEOUT);
  if (Number.isFinite(configured) && configured > 0) {
    return configured;
  }
  if (env.SSH_CONNECTION || env.SSH_TTY) {
    return DEFAULT_SSH_ESCAPE_TIMEOUT_MS;
  }
  return DEFAULT_ESCAPE_TIMEOUT_MS2;
}
__name(resolveEscapeTimeoutMs, "resolveEscapeTimeoutMs");
var ProcessTerminal = class {
  static {
    __name(this, "ProcessTerminal");
  }
  wasRaw = false;
  inputHandler;
  resizeHandler;
  _kittyProtocolActive = false;
  _modifyOtherKeysActive = false;
  keyboardProtocolPushed = false;
  /** DA1 replies owed to keyboard protocol queries. Later DA1 replies answer other queries and are forwarded. */
  pendingKeyboardProtocolDeviceAttributes = 0;
  keyboardProtocolNegotiationBuffer = "";
  keyboardProtocolBufferFlushTimer;
  stdinBuffer;
  stdinDataHandler;
  progressInterval;
  writeLogPath = (() => {
    const env = process.env.PI_TUI_WRITE_LOG || "";
    if (!env)
      return "";
    try {
      if (fs.statSync(env).isDirectory()) {
        const now = /* @__PURE__ */ new Date();
        const ts = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}_${String(now.getHours()).padStart(2, "0")}-${String(now.getMinutes()).padStart(2, "0")}-${String(now.getSeconds()).padStart(2, "0")}`;
        return path.join(env, `tui-${ts}-${process.pid}.log`);
      }
    } catch {
    }
    return env;
  })();
  get kittyProtocolActive() {
    return this._kittyProtocolActive;
  }
  get modifyOtherKeysActive() {
    return this._modifyOtherKeysActive;
  }
  start(onInput, onResize) {
    this.inputHandler = onInput;
    this.resizeHandler = onResize;
    this.wasRaw = process.stdin.isRaw || false;
    if (process.stdin.setRawMode) {
      process.stdin.setRawMode(true);
    }
    process.stdin.setEncoding("utf8");
    process.stdin.resume();
    process.stdout.write("\x1B[?2004h");
    process.stdout.on("resize", this.resizeHandler);
    refreshTerminalDimensions();
    this.enableWindowsVTInput();
    this.queryAndEnableKittyProtocol();
  }
  /**
   * Set up StdinBuffer to split batched input into individual sequences.
   * This ensures components receive single events, making matchesKey/isKeyRelease work correctly.
   *
   * Also watches for Kitty protocol response and enables it when detected.
   * This is done here (after stdinBuffer parsing) rather than on raw stdin
   * to handle the case where the response arrives split across multiple events.
   */
  setupStdinBuffer() {
    this.stdinBuffer = new StdinBuffer({ escapeTimeout: resolveEscapeTimeoutMs() });
    this.stdinBuffer.on("data", (sequence) => {
      const negotiation = this.readKeyboardProtocolNegotiationSequence(sequence);
      if (negotiation === "pending") {
        this.scheduleKeyboardProtocolNegotiationBufferFlush();
        return;
      }
      if (negotiation && this.handleKeyboardProtocolNegotiationSequence(negotiation.parsed)) {
        return;
      }
      this.forwardInputSequence(negotiation?.sequence ?? sequence);
    });
    this.stdinBuffer.on("paste", (content) => {
      if (this.inputHandler) {
        this.inputHandler(`\x1B[200~${content}\x1B[201~`);
      }
    });
    this.stdinDataHandler = (data) => {
      this.stdinBuffer.process(data);
    };
  }
  /**
   * Query terminal for Kitty keyboard protocol support and enable it if available.
   *
   * Kitty's progressive enhancement detection requires requesting the desired
   * flags before querying them. The trailing DA query is a sentinel supported by
   * terminals that do not know Kitty keyboard protocol; receiving DA before a
   * Kitty response enables modifyOtherKeys fallback without a startup timeout.
   *
   * The requested flags are:
   * - 1 = disambiguate escape codes
   * - 2 = report event types (press/repeat/release)
   * - 4 = report alternate keys (shifted key, base layout key)
   */
  queryAndEnableKittyProtocol() {
    this.setupStdinBuffer();
    process.stdin.on("data", this.stdinDataHandler);
    this.keyboardProtocolPushed = true;
    this.pendingKeyboardProtocolDeviceAttributes += 1;
    this.clearKeyboardProtocolNegotiationBuffer();
    process.stdout.write(KITTY_KEYBOARD_PROTOCOL_QUERY);
  }
  handleKeyboardProtocolNegotiationSequence(negotiationSequence) {
    this.clearKeyboardProtocolNegotiationBuffer();
    if (negotiationSequence.type === "device-attributes") {
      if (this.pendingKeyboardProtocolDeviceAttributes === 0)
        return false;
      this.pendingKeyboardProtocolDeviceAttributes -= 1;
    }
    if (negotiationSequence.type === "kitty-flags") {
      if (negotiationSequence.flags !== 0) {
        this.disableModifyOtherKeys();
        if (!this._kittyProtocolActive) {
          this._kittyProtocolActive = true;
          setKittyProtocolActive(true);
        }
      } else {
        this.enableModifyOtherKeys();
      }
      return true;
    }
    if (!this._kittyProtocolActive) {
      this.enableModifyOtherKeys();
    }
    return true;
  }
  /** Returns the parsed negotiation reply with its full (possibly reassembled) sequence. */
  readKeyboardProtocolNegotiationSequence(sequence) {
    if (this.keyboardProtocolNegotiationBuffer) {
      const bufferedSequence = this.keyboardProtocolNegotiationBuffer + sequence;
      const negotiationSequence2 = parseKeyboardProtocolNegotiationSequence(bufferedSequence);
      if (negotiationSequence2) {
        this.clearKeyboardProtocolNegotiationBuffer();
        return { parsed: negotiationSequence2, sequence: bufferedSequence };
      }
      if (isKeyboardProtocolNegotiationSequencePrefix(bufferedSequence)) {
        this.setKeyboardProtocolNegotiationBuffer(bufferedSequence);
        return "pending";
      }
      this.flushKeyboardProtocolNegotiationBufferAsInput();
    }
    const negotiationSequence = parseKeyboardProtocolNegotiationSequence(sequence);
    if (negotiationSequence)
      return { parsed: negotiationSequence, sequence };
    if (isKeyboardProtocolNegotiationSequencePrefix(sequence)) {
      this.setKeyboardProtocolNegotiationBuffer(sequence);
      return "pending";
    }
    return void 0;
  }
  setKeyboardProtocolNegotiationBuffer(sequence) {
    this.clearKeyboardProtocolNegotiationBufferFlushTimer();
    this.keyboardProtocolNegotiationBuffer = sequence;
  }
  clearKeyboardProtocolNegotiationBuffer() {
    this.clearKeyboardProtocolNegotiationBufferFlushTimer();
    this.keyboardProtocolNegotiationBuffer = "";
  }
  flushKeyboardProtocolNegotiationBufferAsInput() {
    if (!this.keyboardProtocolNegotiationBuffer)
      return;
    const sequence = this.keyboardProtocolNegotiationBuffer;
    this.clearKeyboardProtocolNegotiationBuffer();
    this.forwardInputSequence(sequence);
  }
  scheduleKeyboardProtocolNegotiationBufferFlush() {
    if (!this.keyboardProtocolNegotiationBuffer || this.keyboardProtocolBufferFlushTimer)
      return;
    this.keyboardProtocolBufferFlushTimer = setTimeout(() => {
      this.keyboardProtocolBufferFlushTimer = void 0;
      this.flushKeyboardProtocolNegotiationBufferAsInput();
    }, KEYBOARD_PROTOCOL_RESPONSE_FRAGMENT_TIMEOUT_MS);
  }
  clearKeyboardProtocolNegotiationBufferFlushTimer() {
    if (!this.keyboardProtocolBufferFlushTimer)
      return;
    clearTimeout(this.keyboardProtocolBufferFlushTimer);
    this.keyboardProtocolBufferFlushTimer = void 0;
  }
  forwardInputSequence(sequence) {
    if (!this.inputHandler)
      return;
    const shouldDetectNativeShiftEnter = sequence === "\r" && (isAppleTerminalSession() || process.platform === "win32");
    const input = normalizeNativeShiftEnterInput(sequence, shouldDetectNativeShiftEnter, shouldDetectNativeShiftEnter && isNativeModifierPressed("shift"));
    this.inputHandler(input);
  }
  enableModifyOtherKeys() {
    if (this._kittyProtocolActive || this._modifyOtherKeysActive)
      return;
    process.stdout.write("\x1B[>4;2m");
    this._modifyOtherKeysActive = true;
  }
  disableModifyOtherKeys() {
    if (!this._modifyOtherKeysActive)
      return;
    process.stdout.write("\x1B[>4;0m");
    this._modifyOtherKeysActive = false;
  }
  /**
   * On Windows, add ENABLE_VIRTUAL_TERMINAL_INPUT (0x0200) to the stdin
   * console handle so the terminal sends VT sequences for modified keys
   * (e.g. \x1b[Z for Shift+Tab). Without this, libuv's ReadConsoleInputW
   * discards modifier state and Shift+Tab arrives as plain \t.
   */
  enableWindowsVTInput() {
    if (process.platform !== "win32")
      return;
    try {
      getNativePlatformHelper2()?.enableVirtualTerminalInput?.();
    } catch {
    }
  }
  async drainInput(maxMs = 1e3, idleMs = 50) {
    const shouldDisableKittyProtocol = this.keyboardProtocolPushed || this._kittyProtocolActive;
    this.clearKeyboardProtocolNegotiationBuffer();
    if (shouldDisableKittyProtocol) {
      process.stdout.write("\x1B[<u");
      this.keyboardProtocolPushed = false;
      this._kittyProtocolActive = false;
      setKittyProtocolActive(false);
    }
    this.disableModifyOtherKeys();
    const previousHandler = this.inputHandler;
    this.inputHandler = void 0;
    let lastDataTime = Date.now();
    const onData = /* @__PURE__ */ __name(() => {
      lastDataTime = Date.now();
    }, "onData");
    process.stdin.on("data", onData);
    const endTime = Date.now() + maxMs;
    try {
      while (true) {
        const now = Date.now();
        const timeLeft = endTime - now;
        if (timeLeft <= 0)
          break;
        if (now - lastDataTime >= idleMs)
          break;
        await new Promise((resolve) => setTimeout(resolve, Math.min(idleMs, timeLeft)));
      }
    } finally {
      process.stdin.removeListener("data", onData);
      this.inputHandler = previousHandler;
    }
  }
  stop() {
    if (this.clearProgressInterval()) {
      process.stdout.write(TERMINAL_PROGRESS_CLEAR_SEQUENCE);
    }
    process.stdout.write("\x1B[?2004l");
    const shouldDisableKittyProtocol = this.keyboardProtocolPushed || this._kittyProtocolActive;
    this.clearKeyboardProtocolNegotiationBuffer();
    if (shouldDisableKittyProtocol) {
      process.stdout.write("\x1B[<u");
      this.keyboardProtocolPushed = false;
      this._kittyProtocolActive = false;
      setKittyProtocolActive(false);
    }
    this.disableModifyOtherKeys();
    if (this.stdinBuffer) {
      this.stdinBuffer.destroy();
      this.stdinBuffer = void 0;
    }
    if (this.stdinDataHandler) {
      process.stdin.removeListener("data", this.stdinDataHandler);
      this.stdinDataHandler = void 0;
    }
    this.inputHandler = void 0;
    if (this.resizeHandler) {
      process.stdout.removeListener("resize", this.resizeHandler);
      this.resizeHandler = void 0;
    }
    process.stdin.pause();
    if (process.stdin.setRawMode) {
      process.stdin.setRawMode(this.wasRaw);
    }
  }
  write(data) {
    process.stdout.write(data);
    if (this.writeLogPath) {
      try {
        fs.appendFileSync(this.writeLogPath, data, { encoding: "utf8" });
      } catch {
      }
    }
  }
  get columns() {
    return process.stdout.columns || Number(process.env.COLUMNS) || 80;
  }
  get rows() {
    return process.stdout.rows || Number(process.env.LINES) || 24;
  }
  moveBy(lines) {
    if (lines > 0) {
      process.stdout.write(`\x1B[${lines}B`);
    } else if (lines < 0) {
      process.stdout.write(`\x1B[${-lines}A`);
    }
  }
  hideCursor() {
    process.stdout.write("\x1B[?25l");
  }
  showCursor() {
    process.stdout.write("\x1B[?25h");
  }
  clearLine() {
    process.stdout.write("\x1B[K");
  }
  clearFromCursor() {
    process.stdout.write("\x1B[J");
  }
  clearScreen() {
    process.stdout.write("\x1B[2J\x1B[H");
  }
  setTitle(title) {
    process.stdout.write(`\x1B]0;${title}\x07`);
  }
  setProgress(active) {
    if (active) {
      process.stdout.write(TERMINAL_PROGRESS_ACTIVE_SEQUENCE);
      if (!this.progressInterval) {
        this.progressInterval = setInterval(() => {
          process.stdout.write(TERMINAL_PROGRESS_ACTIVE_SEQUENCE);
        }, TERMINAL_PROGRESS_KEEPALIVE_MS);
      }
    } else {
      this.clearProgressInterval();
      process.stdout.write(TERMINAL_PROGRESS_CLEAR_SEQUENCE);
    }
  }
  clearProgressInterval() {
    if (!this.progressInterval)
      return false;
    clearInterval(this.progressInterval);
    this.progressInterval = void 0;
    return true;
  }
};

// pi-dist/pi-tui/terminal-colors.js
var COLOR_SCHEME_REPORT_PATTERN = /^(?:\x1b\[\?997;(1|2)n)+$/;
function parseTerminalColorSchemeReport(data) {
  const match = data.match(COLOR_SCHEME_REPORT_PATTERN);
  if (!match) {
    return void 0;
  }
  return match[1] === "2" ? "light" : "dark";
}
__name(parseTerminalColorSchemeReport, "parseTerminalColorSchemeReport");

// pi-dist/pi-tui/index.js
import { allocateImageId as allocateImageId2, calculateImageRows, deleteAllKittyImages as deleteAllKittyImages2, deleteKittyImage as deleteKittyImage3, detectCapabilities, encodeITerm2, encodeKitty, getCapabilities as getCapabilities4, getCellDimensions as getCellDimensions2, getGifDimensions, getImageDimensions as getImageDimensions2, getJpegDimensions, getPngDimensions, getTerminalColorMode, getWebpDimensions, hyperlink as hyperlink2, imageFallback as imageFallback2, renderImage as renderImage2, resetCapabilitiesCache, setCapabilities as setCapabilities2, setCapabilityOverrides, setCellDimensions } from "../terminal-image.js";
import { Container as Container4, CURSOR_MARKER as CURSOR_MARKER5, compositeTuiLine as compositeTuiLine4, isFocusable, isViewportTUI } from "../tui.js";

// pi-dist/pi-tui/alt-screen-search.js
import { getKeybindings as getKeybindings6 } from "../keybindings.js";
import { getGraphemeSegmenter as getGraphemeSegmenter3, stripTerminalSequences, truncateToWidth as truncateToWidth6, visibleWidth as visibleWidth11 } from "../utils.js";
import { graphemeSegmenter as segmenter2 } from "../../../pi-tui-segmenters.mjs";
var PRINTABLE_ASCII = /^[\x20-\x7e]*$/;
function buildSearchCorpus(lines) {
  const chunks = [];
  const spans = [];
  let textLength = 0;
  let pendingSeparator = false;
  const appendSeparator = /* @__PURE__ */ __name(() => {
    if (!pendingSeparator)
      return;
    chunks.push(" ");
    textLength += 1;
    pendingSeparator = false;
  }, "appendSeparator");
  for (let row = 0; row < lines.length; row++) {
    const line = stripTerminalSequences(lines[row] ?? "");
    let column = 0;
    if (PRINTABLE_ASCII.test(line)) {
      let index = 0;
      while (index < line.length) {
        if (line.charCodeAt(index) === 32) {
          if (textLength > 0)
            pendingSeparator = true;
          column += 1;
          index += 1;
          continue;
        }
        let end = index + 1;
        while (end < line.length && line.charCodeAt(end) !== 32)
          end += 1;
        appendSeparator();
        const text = line.slice(index, end);
        chunks.push(text);
        spans.push({
          textStart: textLength,
          textEnd: textLength + text.length,
          row,
          startCol: column,
          endCol: column + text.length,
          linearColumns: true
        });
        textLength += text.length;
        column += text.length;
        index = end;
      }
    } else {
      for (const grapheme of segmenter2.segment(line)) {
        const text = grapheme.segment;
        const width = visibleWidth11(text);
        if (/^\s+$/u.test(text)) {
          if (textLength > 0)
            pendingSeparator = true;
          column += width;
          continue;
        }
        appendSeparator();
        chunks.push(text);
        spans.push({
          textStart: textLength,
          textEnd: textLength + text.length,
          row,
          startCol: column,
          endCol: column + width,
          linearColumns: false
        });
        textLength += text.length;
        column += width;
      }
    }
    if (textLength > 0)
      pendingSeparator = true;
  }
  return { text: chunks.join(""), spans };
}
__name(buildSearchCorpus, "buildSearchCorpus");
function normalizeQuery(query) {
  return query.replace(/\s+/gu, " ").trim();
}
__name(normalizeQuery, "normalizeQuery");
function escapeRegExp(text) {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
__name(escapeRegExp, "escapeRegExp");
function findSearchCorpusMatches(corpus, normalizedQuery) {
  if (!normalizedQuery)
    return [];
  const expression = new RegExp(escapeRegExp(normalizedQuery), "giu");
  const matches = [];
  let spanIndex = 0;
  for (const match of corpus.text.matchAll(expression)) {
    const start = match.index;
    const end = start + match[0].length;
    while (spanIndex < corpus.spans.length && corpus.spans[spanIndex].textEnd <= start)
      spanIndex += 1;
    const segments = [];
    for (let index = spanIndex; index < corpus.spans.length; index++) {
      const span = corpus.spans[index];
      if (span.textStart >= end)
        break;
      if (span.textEnd <= start)
        continue;
      const startCol = span.linearColumns ? span.startCol + Math.max(start, span.textStart) - span.textStart : span.startCol;
      const endCol = span.linearColumns ? span.startCol + Math.min(end, span.textEnd) - span.textStart : span.endCol;
      const previous = segments[segments.length - 1];
      if (previous && previous.row === span.row && startCol <= previous.endCol) {
        previous.endCol = Math.max(previous.endCol, endCol);
      } else {
        segments.push({ row: span.row, startCol, endCol });
      }
    }
    while (spanIndex < corpus.spans.length && corpus.spans[spanIndex].textEnd <= end)
      spanIndex += 1;
    if (segments.length > 0)
      matches.push({ segments });
  }
  return matches;
}
__name(findSearchCorpusMatches, "findSearchCorpusMatches");
var AltScreenSearchIndex = class {
  static {
    __name(this, "AltScreenSearchIndex");
  }
  sourceLines;
  corpus;
  normalizedQuery;
  matches = [];
  search(lines, query) {
    let sourceChanged = this.sourceLines?.length !== lines.length;
    if (!sourceChanged && this.sourceLines) {
      for (let index = 0; index < lines.length; index++) {
        if (this.sourceLines[index] === lines[index])
          continue;
        sourceChanged = true;
        break;
      }
    }
    if (sourceChanged || !this.corpus) {
      this.sourceLines = Array.from(lines);
      this.corpus = buildSearchCorpus(lines);
    }
    const normalizedQuery = normalizeQuery(query);
    const changed = sourceChanged || normalizedQuery !== this.normalizedQuery;
    if (changed) {
      this.normalizedQuery = normalizedQuery;
      this.matches = findSearchCorpusMatches(this.corpus, normalizedQuery);
    }
    return { matches: this.matches, changed };
  }
};
function getAltScreenSearchMatchKey(match) {
  const first = match.segments[0];
  const last = match.segments[match.segments.length - 1];
  return first && last ? `${first.row}:${first.startCol}:${last.row}:${last.endCol}` : "";
}
__name(getAltScreenSearchMatchKey, "getAltScreenSearchMatchKey");
var AltScreenSearchComponent = class {
  static {
    __name(this, "AltScreenSearchComponent");
  }
  input = new Input({
    prompt: " ",
    placeholder: "Find in transcript",
    placeholderStyle: /* @__PURE__ */ __name((text) => `\x1B[2m${text}\x1B[22m`, "placeholderStyle")
  });
  onQueryChange;
  navigationButtonStyle;
  resultCount = 0;
  resultIndex = -1;
  previousButtonStart = -1;
  previousButtonEnd = -1;
  nextButtonStart = -1;
  nextButtonEnd = -1;
  hoveredNavigationDirection;
  _focused = false;
  constructor(onQueryChange, navigationButtonStyle = (text) => text) {
    this.onQueryChange = onQueryChange;
    this.navigationButtonStyle = navigationButtonStyle;
  }
  get focused() {
    return this._focused;
  }
  set focused(value) {
    this._focused = value;
    this.input.focused = value;
  }
  setResult(index, count) {
    this.resultIndex = index;
    this.resultCount = count;
  }
  getNavigationDirectionAt(row, column) {
    if (row !== 2)
      return void 0;
    if (column >= this.previousButtonStart && column < this.previousButtonEnd)
      return -1;
    if (column >= this.nextButtonStart && column < this.nextButtonEnd)
      return 1;
    return void 0;
  }
  setHoveredNavigationDirection(direction) {
    if (direction === this.hoveredNavigationDirection)
      return false;
    this.hoveredNavigationDirection = direction;
    return true;
  }
  handleInput(data) {
    const previous = this.input.getValue();
    this.input.handleInput(data);
    const query = this.input.getValue();
    if (query !== previous)
      this.onQueryChange(query);
  }
  invalidate() {
    this.input.invalidate();
  }
  render(width) {
    const safeWidth = Math.max(1, width);
    const innerWidth = Math.max(0, safeWidth - 2);
    const formatKey = /* @__PURE__ */ __name((key) => key ? key.split("+").map((part) => {
      if (process.platform === "darwin" && part.toLowerCase() === "alt")
        return "Option";
      return part.charAt(0).toUpperCase() + part.slice(1);
    }).join("+") : "Unbound", "formatKey");
    const keybindings = getKeybindings6();
    const previousKey = formatKey(keybindings.getKeys("tui.altScreen.searchPrevious")[0]);
    const nextKey = formatKey(keybindings.getKeys("tui.altScreen.searchNext")[0]);
    const query = this.input.getValue();
    const result = !query ? "" : this.resultCount === 0 ? "No matches" : `${this.resultIndex + 1}/${this.resultCount}`;
    const resultSpace = Math.max(0, innerWidth - 3);
    const visibleResult = truncateToWidth6(result, resultSpace, "");
    const resultText = visibleResult ? `\x1B[2m ${visibleResult} \x1B[22m` : "";
    const inputWidth = Math.max(0, innerWidth - visibleWidth11(resultText));
    const inputLine = truncateToWidth6(this.input.render(Math.max(1, inputWidth))[0] ?? "", inputWidth, "");
    const inputPadding = " ".repeat(Math.max(0, inputWidth - visibleWidth11(inputLine)));
    const content = `${inputLine}${inputPadding}${resultText}`;
    let previousButton = `\u2191 ${previousKey}`;
    let nextButton = `\u2193 ${nextKey}`;
    let separator = " \xB7 ";
    const outerGapWidth = 1;
    const availableControlsWidth = Math.max(0, innerWidth - outerGapWidth * 2 - 1);
    let controlsWidth = visibleWidth11(previousButton) + visibleWidth11(separator) + visibleWidth11(nextButton);
    if (controlsWidth > availableControlsWidth) {
      previousButton = "\u2191";
      nextButton = "\u2193";
      separator = " ";
      controlsWidth = visibleWidth11(previousButton) + visibleWidth11(separator) + visibleWidth11(nextButton);
    }
    const showButtons = controlsWidth <= availableControlsWidth;
    const renderedButtons = showButtons ? this.navigationButtonStyle(previousButton, this.hoveredNavigationDirection === -1) + separator + this.navigationButtonStyle(nextButton, this.hoveredNavigationDirection === 1) : "";
    const outerGapsWidth = showButtons ? outerGapWidth * 2 : 0;
    const rightRuleWidth = renderedButtons && innerWidth > controlsWidth + outerGapsWidth ? 1 : 0;
    const leftRuleWidth = Math.max(0, innerWidth - (showButtons ? controlsWidth : 0) - outerGapsWidth - rightRuleWidth);
    const previousStart = 1 + leftRuleWidth + outerGapWidth;
    this.previousButtonStart = showButtons ? previousStart : -1;
    this.previousButtonEnd = showButtons ? previousStart + visibleWidth11(previousButton) : -1;
    this.nextButtonStart = showButtons ? this.previousButtonEnd + visibleWidth11(separator) : -1;
    this.nextButtonEnd = showButtons ? this.nextButtonStart + visibleWidth11(nextButton) : -1;
    if (safeWidth === 1)
      return ["\u250C", "\u2502", "\u2514"];
    return [
      `\u250C${"\u2500".repeat(innerWidth)}\u2510`,
      `\u2502${content}\u2502`,
      `\u2514${"\u2500".repeat(leftRuleWidth)}${renderedButtons ? " " : ""}${renderedButtons}${renderedButtons ? " " : ""}${"\u2500".repeat(rightRuleWidth)}\u2518`
    ];
  }
};

// pi-dist/pi-tui/components/alt-screen-flash.js
import { truncateToWidth as truncateToWidth7 } from "../utils.js";
var DEFAULT_DURATION_MS = 1e3;
var AltScreenFlashContainer = class {
  static {
    __name(this, "AltScreenFlashContainer");
  }
  entries = [];
  nextId = 0;
  requestRender;
  constructor(requestRender) {
    this.requestRender = requestRender;
  }
  flash(message, durationMs = DEFAULT_DURATION_MS) {
    const id = this.nextId++;
    const timer = setTimeout(() => {
      const index = this.entries.findIndex((entry) => entry.id === id);
      if (index === -1)
        return;
      this.entries.splice(index, 1);
      this.requestRender();
    }, Math.max(0, durationMs));
    timer.unref();
    this.entries.push({ id, message, timer });
    this.requestRender();
  }
  dispose() {
    for (const entry of this.entries)
      clearTimeout(entry.timer);
    this.entries.length = 0;
  }
  invalidate() {
  }
  render(width) {
    return this.entries.map((entry) => {
      const message = truncateToWidth7(` ${entry.message} `, width, "");
      return `\x1B[7m${message}\x1B[27m`;
    });
  }
};

// pi-dist/pi-tui/tui-alt-screen.js
import { getKeybindings as getKeybindings7 } from "../keybindings.js";
import { isKeyRelease } from "../keys.js";

// pi-dist/pi-tui/layout.js
import { cropKittyImageLine, getKittyImageMetadata, isImageLine as isImageLine2 } from "../terminal-image.js";
import { CURSOR_MARKER as CURSOR_MARKER3, compositeTuiLine as compositeTuiLine2 } from "../tui.js";
import { extractAnsiCode, getActiveBackgroundAnsi, getGraphemeCellRange, sliceByColumn as sliceByColumn3, visibleWidth as visibleWidth12 } from "../utils.js";
var OSC133_ZONE_PREFIX = /^(?:\x1b\]133;[ABC](?:\x07|\x1b\\))+/;
function intersect(a, b) {
  const x = Math.max(a.x, b.x);
  const y = Math.max(a.y, b.y);
  const right = Math.min(a.x + a.width, b.x + b.width);
  const bottom = Math.min(a.y + a.height, b.y + b.height);
  return { x, y, width: Math.max(0, right - x), height: Math.max(0, bottom - y) };
}
__name(intersect, "intersect");
function renderCached(context, component, width) {
  const safeWidth = Math.max(1, Math.floor(width));
  let widths = context.renderCache.get(component);
  if (!widths) {
    widths = /* @__PURE__ */ new Map();
    context.renderCache.set(component, widths);
  }
  let lines = widths.get(safeWidth);
  if (!lines) {
    lines = component.render(safeWidth);
    widths.set(safeWidth, lines);
  }
  return lines;
}
__name(renderCached, "renderCached");
function measureHeight(context, component, width) {
  return renderCached(context, component, width).length;
}
__name(measureHeight, "measureHeight");
function measureWidth(context, component, width) {
  return renderCached(context, component, width).reduce((max, line) => Math.max(max, visibleWidth12(line)), 0);
}
__name(measureWidth, "measureWidth");
function withParent(box, parent) {
  box.parent = parent;
  return box;
}
__name(withParent, "withParent");
function translateBox(box, deltaY) {
  box.rect.y += deltaY;
  for (const child of box.children)
    translateBox(child, deltaY);
}
__name(translateBox, "translateBox");
function updateClips(box, parentClip) {
  box.clip = intersect(parentClip, box.rect);
  for (const child of box.children)
    updateClips(child, box.clip);
}
__name(updateClips, "updateClips");
function layoutComponent(context, component, x, y, width, height, clip) {
  const safeWidth = Math.max(1, Math.floor(width));
  const node = getLayoutNode(component);
  if (!node) {
    const lines = renderCached(context, component, safeWidth);
    const allocatedHeight2 = height === void 0 ? lines.length : Math.max(0, Math.floor(height));
    let lineOffset = 0;
    if (lines.length > allocatedHeight2 && allocatedHeight2 > 0) {
      const cursorLine = lines.findIndex((line) => line.includes(CURSOR_MARKER3));
      if (cursorLine >= allocatedHeight2)
        lineOffset = cursorLine - allocatedHeight2 + 1;
    }
    return {
      component,
      rect: { x, y, width: safeWidth, height: allocatedHeight2 },
      clip: intersect(clip, { x, y, width: safeWidth, height: allocatedHeight2 }),
      children: [],
      lines,
      lineOffset,
      layer: 0
    };
  }
  if (node.type === "scroll") {
    const previousScrollTop = node.state.scrollTop;
    const contentWidth = node.state.getContentWidth(safeWidth);
    const childBox = layoutComponent(context, node.component, x, y - previousScrollTop, contentWidth, void 0, clip);
    const contentHeight = childBox.rect.height;
    const viewportHeight = height === void 0 ? contentHeight : Math.max(0, Math.floor(height));
    node.state.updateLayout(contentHeight, viewportHeight, context.requestRender);
    translateBox(childBox, previousScrollTop - node.state.scrollTop);
    const scrollView = node.state;
    if (node.state.primary || !context.primaryScrollView)
      context.primaryScrollView = scrollView;
    const rect2 = { x, y, width: safeWidth, height: viewportHeight };
    const childClip = intersect(clip, rect2);
    const box2 = {
      component,
      rect: rect2,
      clip: childClip,
      children: [childBox],
      scrollView,
      scrollContentLines: renderCached(context, node.component, contentWidth),
      layer: 0
    };
    childBox.parent = box2;
    updateClips(childBox, childClip);
    return box2;
  }
  const entries = visibleStackEntries(node.entries, context.viewport);
  const gapTotal = Math.max(0, entries.length - 1) * node.gap;
  if (node.type === "vstack") {
    const intrinsicHeights2 = entries.map((entry) => typeof entry.basis === "number" ? entry.basis : measureHeight(context, entry.component, safeWidth));
    const sizes = allocateStackSizes(entries, intrinsicHeights2, height, node.gap);
    const naturalHeight = sizes.reduce((sum, size) => sum + size, 0) + gapTotal;
    const allocatedHeight2 = height === void 0 ? naturalHeight : Math.max(0, Math.floor(height));
    const rect2 = { x, y, width: safeWidth, height: allocatedHeight2 };
    const box2 = {
      component,
      rect: rect2,
      clip: intersect(clip, rect2),
      children: [],
      layer: 0
    };
    let childY = y;
    for (let index = 0; index < entries.length; index++) {
      box2.children.push(withParent(layoutComponent(context, entries[index].component, x, childY, safeWidth, sizes[index], box2.clip), box2));
      childY += sizes[index] + node.gap;
    }
    return box2;
  }
  const intrinsicWidths = entries.map((entry) => typeof entry.basis === "number" ? entry.basis : measureWidth(context, entry.component, safeWidth));
  const widths = allocateStackSizes(entries, intrinsicWidths, safeWidth, node.gap);
  const intrinsicHeights = entries.map((entry, index) => measureHeight(context, entry.component, Math.max(1, widths[index])));
  const allocatedHeight = height === void 0 ? intrinsicHeights.reduce((max, childHeight) => Math.max(max, childHeight), 0) : Math.max(0, height);
  const rect = { x, y, width: safeWidth, height: allocatedHeight };
  const box = {
    component,
    rect,
    clip: intersect(clip, rect),
    children: [],
    layer: 0
  };
  let childX = x;
  for (let index = 0; index < entries.length; index++) {
    const naturalChildHeight = intrinsicHeights[index];
    const childHeight = node.align === "stretch" ? allocatedHeight : Math.min(allocatedHeight, naturalChildHeight);
    let childY = y;
    if (node.align === "center")
      childY += Math.floor((allocatedHeight - childHeight) / 2);
    else if (node.align === "end")
      childY += allocatedHeight - childHeight;
    const childWidth = widths[index];
    if (childWidth === 0) {
      box.children.push({
        component: entries[index].component,
        rect: { x: childX, y: childY, width: 0, height: childHeight },
        clip: { x: childX, y: childY, width: 0, height: 0 },
        children: [],
        parent: box,
        layer: 0
      });
    } else {
      box.children.push(withParent(layoutComponent(context, entries[index].component, childX, childY, childWidth, childHeight, box.clip), box));
    }
    childX += childWidth + node.gap;
  }
  return box;
}
__name(layoutComponent, "layoutComponent");
function replaceScrollbarCell(line, column, totalWidth, replacement, preserveTargetBackground) {
  if (isImageLine2(line))
    return line;
  const graphemeRange = getGraphemeCellRange(line, column);
  const start = graphemeRange?.start ?? column;
  const end = graphemeRange?.end ?? column + 1;
  const before = sliceByColumn3(line, 0, start, true);
  const target = sliceByColumn3(line, start, end - start, true);
  const after = sliceByColumn3(line, end, Math.max(0, totalWidth - end), true);
  let targetPrefix = "";
  let targetIndex = 0;
  while (targetIndex < target.length) {
    const ansi = extractAnsiCode(target, targetIndex);
    if (!ansi)
      break;
    targetPrefix += ansi.code;
    targetIndex += ansi.length;
  }
  const beforePadding = " ".repeat(Math.max(0, start - visibleWidth12(before)));
  const cellPaddingBefore = " ".repeat(Math.max(0, column - start));
  const cellPaddingAfter = " ".repeat(Math.max(0, end - column - 1));
  const targetStyle = `\x1B[0m\x1B]8;;\x07${preserveTargetBackground ? getActiveBackgroundAnsi(targetPrefix) : ""}`;
  return `${before}${beforePadding}${targetStyle}${cellPaddingBefore}${replacement}${cellPaddingAfter}${after}`;
}
__name(replaceScrollbarCell, "replaceScrollbarCell");
function getScrollbarGeometry(box, includeHiddenAuto = false) {
  if (!box.scrollView || box.rect.width <= 0 || box.rect.height <= 0)
    return void 0;
  const contentHeight = box.children[0]?.rect.height ?? box.scrollContentLines?.length ?? 0;
  const trackHeight = box.rect.height;
  const canRevealHiddenAuto = includeHiddenAuto && box.scrollView.scrollbar === "auto" && contentHeight > trackHeight;
  if (!box.scrollView.isScrollbarVisible && !canRevealHiddenAuto)
    return void 0;
  const minThumbHeight = Math.min(2, trackHeight);
  const thumbHeight = Math.max(minThumbHeight, Math.min(trackHeight, Math.round(trackHeight * trackHeight / contentHeight)));
  const maxScrollTop = Math.max(0, contentHeight - trackHeight);
  const maxThumbTop = trackHeight - thumbHeight;
  const thumbOffset = maxScrollTop === 0 ? 0 : Math.round(box.scrollView.scrollTop / maxScrollTop * maxThumbTop);
  const column = box.rect.x + box.rect.width - 1;
  if (column < box.clip.x || column >= box.clip.x + box.clip.width)
    return void 0;
  return {
    column,
    trackTop: box.rect.y,
    trackHeight,
    thumbTop: box.rect.y + thumbOffset,
    thumbHeight,
    maxScrollTop
  };
}
__name(getScrollbarGeometry, "getScrollbarGeometry");
function paintScrollbar(box, screen, totalWidth) {
  const geometry = getScrollbarGeometry(box);
  if (!geometry || !box.scrollView)
    return;
  for (let offset = 0; offset < geometry.trackHeight; offset++) {
    const row = geometry.trackTop + offset;
    if (row < box.clip.y || row >= box.clip.y + box.clip.height || row < 0 || row >= screen.length)
      continue;
    const isThumb = row >= geometry.thumbTop && row < geometry.thumbTop + geometry.thumbHeight;
    const replacement = isThumb ? box.scrollView.scrollbarThumbStyle(box.scrollView.isScrollbarActive ? "\u2588" : "\u2503") : box.scrollView.scrollbarTrackStyle("\u2502");
    screen[row] = replaceScrollbarCell(screen[row] ?? "", geometry.column, totalWidth, replacement, box.scrollView.scrollbar !== "always");
  }
}
__name(paintScrollbar, "paintScrollbar");
function paintBox(box, screen, totalWidth) {
  if (box.lines) {
    const offset = box.lineOffset ?? 0;
    const firstRow = Math.max(box.rect.y, box.clip.y, 0);
    const lastRow = Math.min(box.rect.y + box.rect.height, box.clip.y + box.clip.height, screen.length);
    for (let row = firstRow; row < lastRow; row++) {
      const sourceLine = box.lines[offset + row - box.rect.y];
      if (sourceLine === void 0)
        continue;
      let line = sourceLine.replace(OSC133_ZONE_PREFIX, "");
      const imageMetadata = getKittyImageMetadata(line);
      if (imageMetadata) {
        const clipBottom = Math.min(screen.length, box.clip.y + box.clip.height);
        const visibleRows = Math.min(imageMetadata.rows, clipBottom - row);
        if (visibleRows < imageMetadata.rows)
          line = cropKittyImageLine(line, 0, visibleRows);
      }
      if (box.rect.x === 0 && box.rect.width >= totalWidth && (isImageLine2(line) || !screen[row])) {
        screen[row] = line;
      } else {
        screen[row] = compositeTuiLine2(screen[row] ?? "", line, box.rect.x, box.rect.width, totalWidth);
      }
    }
  }
  for (const child of box.children)
    paintBox(child, screen, totalWidth);
  if (box.scrollView && box.scrollContentLines && box.scrollView.scrollTop > 0 && box.rect.height > 0) {
    for (let imageRow = box.scrollView.scrollTop - 1; imageRow >= 0; imageRow--) {
      const imageLine = box.scrollContentLines[imageRow] ?? "";
      const metadata = getKittyImageMetadata(imageLine);
      if (metadata) {
        const hiddenRows = box.scrollView.scrollTop - imageRow;
        if (hiddenRows < metadata.rows) {
          const visibleRows = Math.min(box.rect.height, metadata.rows - hiddenRows);
          const cropped = cropKittyImageLine(imageLine, hiddenRows, visibleRows);
          if (box.rect.x === 0 && box.rect.width >= totalWidth)
            screen[box.rect.y] = cropped;
        }
        break;
      }
      if (imageLine !== "")
        break;
    }
  }
  paintScrollbar(box, screen, totalWidth);
}
__name(paintBox, "paintBox");
function renderLayoutFrame(root, width, height, requestRender) {
  const safeWidth = Math.max(1, Math.floor(width));
  const safeHeight = Math.max(1, Math.floor(height));
  const context = {
    viewport: { width: safeWidth, height: safeHeight },
    renderCache: /* @__PURE__ */ new Map(),
    requestRender,
    primaryScrollView: void 0
  };
  const rootBox = layoutComponent(context, root, 0, 0, safeWidth, safeHeight, {
    x: 0,
    y: 0,
    width: safeWidth,
    height: safeHeight
  });
  const lines = Array.from({ length: safeHeight }, () => "");
  paintBox(rootBox, lines, safeWidth);
  return {
    root: rootBox,
    width: safeWidth,
    height: safeHeight,
    lines,
    ...context.primaryScrollView === void 0 ? {} : { primaryScrollView: context.primaryScrollView }
  };
}
__name(renderLayoutFrame, "renderLayoutFrame");
function containsPoint(rect, x, y) {
  return x >= rect.x && x < rect.x + rect.width && y >= rect.y && y < rect.y + rect.height;
}
__name(containsPoint, "containsPoint");
function getLayoutBoxesAt(frame, x, y) {
  const result = [];
  const visit = /* @__PURE__ */ __name((box, depth) => {
    if (!containsPoint(box.clip, x, y))
      return;
    result.push({ box, depth });
    for (const child of box.children)
      visit(child, depth + 1);
  }, "visit");
  visit(frame.root, 0);
  result.sort((a, b) => b.box.layer - a.box.layer || b.depth - a.depth);
  return result.map(({ box }) => box);
}
__name(getLayoutBoxesAt, "getLayoutBoxesAt");
function getScrollViewBox(frame, scrollView) {
  const visit = /* @__PURE__ */ __name((box) => {
    if (box.scrollView === scrollView)
      return box;
    for (const child of box.children) {
      const match = visit(child);
      if (match)
        return match;
    }
    return void 0;
  }, "visit");
  return visit(frame.root);
}
__name(getScrollViewBox, "getScrollViewBox");
function getScrollViewsAt(frame, x, y) {
  const result = [];
  const visit = /* @__PURE__ */ __name((box, depth) => {
    if (!containsPoint(box.clip, x, y))
      return;
    if (box.scrollView && containsPoint(box.rect, x, y))
      result.push({ scrollView: box.scrollView, depth });
    for (const child of box.children)
      visit(child, depth + 1);
  }, "visit");
  visit(frame.root, 0);
  result.sort((a, b) => b.depth - a.depth);
  return result.map((entry) => entry.scrollView);
}
__name(getScrollViewsAt, "getScrollViewsAt");

// pi-dist/pi-tui/tui-alt-screen.js
import { deleteAllKittyImages, deleteAllKittyPlacements, deleteKittyImage, getCapabilities as getCapabilities3, getKittyImagePlacement, isImageLine as isImageLine3, setCapabilities } from "../terminal-image.js";
import { Container as Container3, CURSOR_MARKER as CURSOR_MARKER4, compositeTuiLine as compositeTuiLine3, dispatchMouseEvent as dispatchMouseEvent3, retargetMouseEvent, TuiBase, VIEWPORT_TUI } from "../tui.js";
import { extractAnsiCode as extractAnsiCode2, getGraphemeCellRange as getGraphemeCellRange2, getOsc8LinkAtColumn, getWordSegmenter as getWordSegmenter3, sliceByColumn as sliceByColumn4, stripTerminalSequences as stripTerminalSequences2, truncateToWidth as truncateToWidth8, visibleWidth as visibleWidth13 } from "../utils.js";

// pi-dist/pi-tui/wheel-scroll.js
var BURST_GAP_MS = 5;
var GESTURE_GAP_MS = 200;
var REFERENCE_GAP_MS = 100;
var MAX_AUTO_LINES = 6;
function terminalAcceleratesWheel() {
  const env = process.env;
  return process.platform === "darwin" && env.SSH_CONNECTION === void 0 && env.SSH_CLIENT === void 0 && env.SSH_TTY === void 0;
}
__name(terminalAcceleratesWheel, "terminalAcceleratesWheel");
var WheelScrollAccelerator = class {
  static {
    __name(this, "WheelScrollAccelerator");
  }
  lines;
  accelerate;
  lastTime = Number.NEGATIVE_INFINITY;
  lastDirection = 0;
  averageGap;
  carry = 0;
  constructor(lines = "auto", accelerate = !terminalAcceleratesWheel()) {
    this.lines = lines;
    this.accelerate = accelerate;
  }
  setLines(lines) {
    this.lines = lines;
    this.reset();
  }
  /** Return the positive line count for a wheel event in `direction` at time `now` (milliseconds). */
  next(direction, now) {
    if (this.lines !== "auto")
      return Number.isFinite(this.lines) ? Math.max(1, Math.floor(this.lines)) : 1;
    if (!this.accelerate)
      return 1;
    const gap = now - this.lastTime;
    const sameGesture = direction === this.lastDirection && gap <= GESTURE_GAP_MS;
    this.lastTime = now;
    this.lastDirection = direction;
    if (!sameGesture) {
      this.averageGap = void 0;
      this.carry = 0;
      return 1;
    }
    if (gap < BURST_GAP_MS)
      return 1;
    this.averageGap = this.averageGap === void 0 ? gap : (this.averageGap + gap) / 2;
    const lines = Math.min(MAX_AUTO_LINES, Math.max(1, REFERENCE_GAP_MS / this.averageGap)) + this.carry;
    const whole = Math.floor(lines);
    this.carry = lines - whole;
    return whole;
  }
  reset() {
    this.lastTime = Number.NEGATIVE_INFINITY;
    this.lastDirection = 0;
    this.averageGap = void 0;
    this.carry = 0;
  }
};

// pi-dist/pi-tui/tui-alt-screen.js
import { wordSegmenter as wordSegmenter3 } from "../../../pi-tui-segmenters.mjs";
var ENTER_ALT_SCREEN = "\x1B[?1049h";
var EXIT_ALT_SCREEN = "\x1B[?1049l";
var DISABLE_AUTOWRAP = "\x1B[?7l";
var ENABLE_AUTOWRAP = "\x1B[?7h";
var ENABLE_BUTTON_MOTION_MOUSE = "\x1B[?1000h\x1B[?1002h\x1B[?1004h\x1B[?1006h";
var ENABLE_ALL_MOTION_MOUSE = "\x1B[?1000h\x1B[?1002h\x1B[?1003h\x1B[?1004h\x1B[?1006h";
var DISABLE_MOUSE = "\x1B[?1006l\x1B[?1004l\x1B[?1003l\x1B[?1002l\x1B[?1000l";
var FOCUS_IN = "\x1B[I";
var FOCUS_OUT = "\x1B[O";
var BEGIN_SYNCHRONIZED_OUTPUT = "\x1B[?2026h";
var END_SYNCHRONIZED_OUTPUT = "\x1B[?2026l";
var OSC133_ZONE_PREFIX2 = /^(?:\x1b\]133;[ABC](?:\x07|\x1b\\))+/;
var OSC133_PROMPT_START = /^\x1b\]133;A(?:\x07|\x1b\\)/;
var PAGE_SCROLL_OVERLAP = 4;
var ALT_WHEEL_SCROLL_MULTIPLIER = 5;
var MAX_CACHED_OFFSCREEN_KITTY_IMAGES = 16;
var MAX_CACHED_OFFSCREEN_KITTY_TRANSMISSION_BYTES = 32 * 1024 * 1024;
var MAX_CACHED_OFFSCREEN_KITTY_DECODED_BYTES = 64 * 1024 * 1024;
var DOUBLE_CLICK_INTERVAL_MS = 500;
var COPY_ERROR_FLASH_DURATION_MS = 5e3;
var TERMINAL_WORD_SELECTION_JOINERS = /* @__PURE__ */ new Set(["/", "-"]);
var TuiAltScreen = class extends TuiBase {
  static {
    __name(this, "TuiAltScreen");
  }
  mode = "fullscreen";
  [VIEWPORT_TUI] = true;
  previousScreen = [];
  lastDocument = [];
  previousScreenWidth = 0;
  previousScreenHeight = 0;
  layoutRoot;
  currentLayout;
  implicitDocument;
  implicitScrollView;
  flashes;
  altScreenActive = false;
  imageProtocol = null;
  savedCapabilities;
  uploadedKittyImages = /* @__PURE__ */ new Map();
  selectionAnchor;
  selectionFocus;
  selectionGranularity = "character";
  selectionInitialRange;
  lastClick;
  selectionDragPointer;
  selectionAutoScrollDirection = 0;
  selectionAutoScrollTimer;
  selectionPressActive = false;
  scrollbarDrag;
  scrollbarHover;
  scrollToEndIndicatorRect;
  activeSearch;
  pressedUrl;
  selectionDragged = false;
  mouseCapture;
  mousePressTarget;
  mousePressPoint;
  mousePressMoved = false;
  lastComponentClick;
  wheelScroll;
  mouseEnabled;
  searchMatchStyle;
  searchCurrentMatchStyle;
  searchNavigationButtonStyle;
  scrollToEndIndicator;
  openUrl;
  onRightClickPaste;
  copyOnSelect;
  copySelection;
  constructor(terminal, showHardwareCursor, logDirectory, options = {}) {
    super(terminal, showHardwareCursor, logDirectory);
    this.implicitDocument = {
      render: /* @__PURE__ */ __name((width) => super.render(width), "render"),
      handleMouse: /* @__PURE__ */ __name((event) => super.handleMouse(event), "handleMouse"),
      invalidate: /* @__PURE__ */ __name(() => {
        for (const child of this.children)
          child.invalidate();
      }, "invalidate")
    };
    this.implicitScrollView = new ScrollView(this.implicitDocument, { follow: "end", primary: true });
    this.flashes = new AltScreenFlashContainer(() => this.requestRender());
    this.wheelScroll = new WheelScrollAccelerator(options.wheelScrollLines ?? 1);
    this.mouseEnabled = options.mouse ?? true;
    this.searchMatchStyle = options.searchMatchStyle ?? ((text) => `\x1B[4m${text}\x1B[24m`);
    this.searchCurrentMatchStyle = options.searchCurrentMatchStyle ?? ((text) => `\x1B[1;7m${text}\x1B[22;27m`);
    this.searchNavigationButtonStyle = options.searchNavigationButtonStyle ?? ((text) => text);
    this.scrollToEndIndicator = options.scrollToEndIndicator;
    this.openUrl = options.openUrl;
    this.onRightClickPaste = options.onRightClickPaste;
    this.copyOnSelect = options.copyOnSelect ?? true;
    this.copySelection = options.copySelection;
    this.addInputListener((data) => this.handleViewportInput(data));
  }
  get viewportTop() {
    return this.getPrimaryScrollView().scrollTop;
  }
  get isFollowingOutput() {
    return this.getPrimaryScrollView().isFollowingEnd;
  }
  setWheelScrollLines(lines) {
    this.wheelScroll.setLines(lines);
  }
  getCopyOnSelect() {
    return this.copyOnSelect;
  }
  setCopyOnSelect(enabled) {
    this.copyOnSelect = enabled;
  }
  /** Whether the fullscreen viewport has a non-empty active text selection. */
  hasActiveSelection() {
    return this.getActiveSelectionText() !== void 0;
  }
  /** Copy the active fullscreen text selection, if any, using the configured selection clipboard path. */
  async copyActiveSelectionToClipboard() {
    const text = this.getActiveSelectionText();
    if (!text)
      return false;
    return this.copyTextToClipboard(text);
  }
  /** The lines of the last rendered frame, one per terminal row, as written to the terminal. */
  getScreenLines() {
    return [...this.previousScreen];
  }
  setLayoutRoot(component) {
    if (this.layoutRoot === component)
      return;
    this.layoutRoot = component;
    this.currentLayout = void 0;
    this.requestRender();
  }
  render(width) {
    return this.layoutRoot?.render(width) ?? super.render(width);
  }
  getMountedRoots() {
    return this.layoutRoot ? [this.layoutRoot] : this.children;
  }
  getPrimaryScrollView() {
    return this.currentLayout?.primaryScrollView ?? this.implicitScrollView;
  }
  beforeTerminalStart() {
    this.stopSelectionAutoScroll();
    this.selectionPressActive = false;
    this.stopScrollbarHover();
    this.stopScrollbarDrag();
    this.flashes.dispose();
    this.altScreenActive = true;
    const capabilities = getCapabilities3();
    this.imageProtocol = capabilities.images;
    this.uploadedKittyImages.clear();
    if (capabilities.images === "iterm2") {
      this.savedCapabilities = capabilities;
      setCapabilities({ ...capabilities, images: null });
      this.invalidate();
    }
    this.lastDocument = [];
    this.selectionAnchor = void 0;
    this.selectionFocus = void 0;
    this.selectionGranularity = "character";
    this.selectionInitialRange = void 0;
    this.lastClick = void 0;
    this.pressedUrl = void 0;
    this.selectionDragged = false;
    this.clearComponentMouseGesture();
    this.lastComponentClick = void 0;
    this.resetRenderState();
    const term = process.env.TERM?.toLowerCase() ?? "";
    const mouseSequence = process.env.TMUX !== void 0 || process.env.ZELLIJ !== void 0 || process.env.STY !== void 0 || term.startsWith("tmux") || term.startsWith("screen") ? ENABLE_BUTTON_MOTION_MOUSE : ENABLE_ALL_MOTION_MOUSE;
    this.terminal.write(`${ENTER_ALT_SCREEN}${DISABLE_AUTOWRAP}${this.mouseEnabled ? mouseSequence : ""}\x1B[2J\x1B[H\x1B[?25l`);
  }
  beforeTerminalStop(_options) {
    this.closeSearch();
    this.stopSelectionAutoScroll();
    this.selectionPressActive = false;
    this.stopScrollbarHover();
    this.stopScrollbarDrag();
    this.clearComponentMouseGesture();
    this.flashes.dispose();
    if (!this.altScreenActive)
      return;
    this.terminal.write(`${BEGIN_SYNCHRONIZED_OUTPUT}${this.deleteKittyImages()}${this.mouseEnabled ? DISABLE_MOUSE : ""}${ENABLE_AUTOWRAP}${END_SYNCHRONIZED_OUTPUT}`);
    this.uploadedKittyImages.clear();
  }
  afterTerminalStop(options) {
    if (!this.altScreenActive)
      return;
    this.altScreenActive = false;
    if (options.preserveScreen) {
      this.terminal.write(`${BEGIN_SYNCHRONIZED_OUTPUT}${EXIT_ALT_SCREEN}\x1B[?25h${END_SYNCHRONIZED_OUTPUT}`);
    } else {
      const width = Math.max(1, this.terminal.columns);
      const documentLines = this.render(width).map((line) => line.replace(OSC133_ZONE_PREFIX2, ""));
      this.lastDocument = this.applyLineResets(documentLines.map((line) => line.replaceAll(CURSOR_MARKER4, ""))).map((line) => isImageLine3(line) || visibleWidth13(line) <= width ? line : sliceByColumn4(line, 0, width, true));
      let buffer = `${BEGIN_SYNCHRONIZED_OUTPUT}${EXIT_ALT_SCREEN}${DISABLE_AUTOWRAP}`;
      for (let row = 0; row < this.lastDocument.length; row++) {
        if (row > 0)
          buffer += "\r\n";
        buffer += `\r\x1B[2K${this.lastDocument[row] ?? ""}`;
      }
      buffer += `\x1B[0m${ENABLE_AUTOWRAP}\r
\x1B[?25h${END_SYNCHRONIZED_OUTPUT}`;
      this.terminal.write(buffer);
    }
    if (this.savedCapabilities) {
      setCapabilities(this.savedCapabilities);
      this.savedCapabilities = void 0;
    }
  }
  deleteKittyImages() {
    return this.imageProtocol === "kitty" ? deleteAllKittyImages() : "";
  }
  prepareKittyScreen(screen) {
    const visibleImageIds = /* @__PURE__ */ new Set();
    const lines = screen.map((line) => {
      const placement = getKittyImagePlacement(line);
      if (!placement)
        return line;
      visibleImageIds.add(placement.imageId);
      const cachedImage = this.uploadedKittyImages.get(placement.imageId);
      const nextCachedImage = {
        transmissionGeneration: placement.transmissionGeneration,
        transmissionBytes: placement.transmissionBytes,
        estimatedDecodedBytes: placement.estimatedDecodedBytes
      };
      if (cachedImage)
        this.uploadedKittyImages.delete(placement.imageId);
      this.uploadedKittyImages.set(placement.imageId, nextCachedImage);
      return cachedImage?.transmissionGeneration === placement.transmissionGeneration ? placement.replacementLine : line;
    });
    let cachedOffscreenImageCount = 0;
    let cachedOffscreenTransmissionBytes = 0;
    let cachedOffscreenDecodedBytes = 0;
    for (const [imageId, cachedImage] of this.uploadedKittyImages) {
      if (visibleImageIds.has(imageId))
        continue;
      cachedOffscreenImageCount += 1;
      cachedOffscreenTransmissionBytes += cachedImage.transmissionBytes;
      cachedOffscreenDecodedBytes += cachedImage.estimatedDecodedBytes;
    }
    let evictedImageDeletion = "";
    for (const [imageId, cachedImage] of this.uploadedKittyImages) {
      if (cachedOffscreenImageCount <= MAX_CACHED_OFFSCREEN_KITTY_IMAGES && cachedOffscreenTransmissionBytes <= MAX_CACHED_OFFSCREEN_KITTY_TRANSMISSION_BYTES && cachedOffscreenDecodedBytes <= MAX_CACHED_OFFSCREEN_KITTY_DECODED_BYTES) {
        break;
      }
      if (visibleImageIds.has(imageId))
        continue;
      evictedImageDeletion += deleteKittyImage(imageId);
      this.uploadedKittyImages.delete(imageId);
      cachedOffscreenImageCount -= 1;
      cachedOffscreenTransmissionBytes -= cachedImage.transmissionBytes;
      cachedOffscreenDecodedBytes -= cachedImage.estimatedDecodedBytes;
    }
    return { lines, evictedImageDeletion };
  }
  resetRenderState() {
    this.previousScreen = [];
    this.previousScreenWidth = 0;
    this.previousScreenHeight = 0;
    this.currentLayout = void 0;
  }
  scrollBy(lines) {
    this.getPrimaryScrollView().scrollBy(lines);
    this.requestRender();
  }
  scrollToTop() {
    this.getPrimaryScrollView().scrollToStart();
    this.requestRender();
  }
  scrollToBottom() {
    this.getPrimaryScrollView().scrollToEnd();
    this.requestRender();
  }
  scrollToPrompt(direction) {
    if (!this.currentLayout)
      return;
    const scrollView = this.getPrimaryScrollView();
    const lines = getScrollViewBox(this.currentLayout, scrollView)?.scrollContentLines;
    if (!lines)
      return;
    for (let row = scrollView.scrollTop + direction; row >= 0 && row < lines.length; row += direction) {
      if (!OSC133_PROMPT_START.test(lines[row] ?? ""))
        continue;
      scrollView.scrollTo(row);
      this.requestRender();
      return;
    }
  }
  toggleSearch() {
    if (this.activeSearch) {
      this.closeSearch();
      return;
    }
    const component = new AltScreenSearchComponent((query) => this.updateSearchQuery(query), this.searchNavigationButtonStyle);
    const search = {
      component,
      index: new AltScreenSearchIndex(),
      query: "",
      matches: [],
      selectedIndex: -1,
      anchorRow: this.getPrimaryScrollView().scrollTop,
      selectionMode: "query"
    };
    this.activeSearch = search;
    search.overlay = this.showOverlay(component, {
      anchor: "top-right",
      width: "40%",
      minWidth: 32,
      margin: 1
    });
  }
  closeSearch() {
    const search = this.activeSearch;
    if (!search)
      return;
    this.activeSearch = void 0;
    search.overlay?.hide();
    this.requestRender();
  }
  updateSearchQuery(query) {
    const search = this.activeSearch;
    if (!search || query === search.query)
      return;
    const selected = search.matches[search.selectedIndex];
    search.anchorRow = selected?.segments[0]?.row ?? this.getPrimaryScrollView().scrollTop;
    search.query = query;
    search.selectionMode = "query";
    search.component.setResult(-1, 0);
    this.requestRender();
  }
  navigateSearch(direction) {
    const search = this.activeSearch;
    if (!search?.query)
      return;
    search.selectionMode = direction < 0 ? "previous" : "next";
    this.requestRender();
  }
  getSearchNavigationDirectionAt(x, y) {
    const search = this.activeSearch;
    const bounds = search?.overlay?.getBounds();
    if (!search || !bounds)
      return void 0;
    if (x < bounds.col || x >= bounds.col + bounds.width || y < bounds.row || y >= bounds.row + bounds.height) {
      return void 0;
    }
    return search.component.getNavigationDirectionAt(y - bounds.row, x - bounds.col);
  }
  handleSearchMouseEvent(event) {
    const search = this.activeSearch;
    if (!search)
      return false;
    const direction = this.getSearchNavigationDirectionAt(event.x, event.y);
    if (search.component.setHoveredNavigationDirection(direction))
      this.requestRender();
    if (direction === void 0 || event.release || (event.button & 32) !== 0 || (event.button & 3) !== 0) {
      return false;
    }
    this.navigateSearch(direction);
    return true;
  }
  refreshSearch(layout) {
    const search = this.activeSearch;
    if (!search)
      return false;
    const scrollView = layout.primaryScrollView ?? this.implicitScrollView;
    const box = getScrollViewBox(layout, scrollView);
    const lines = box?.scrollContentLines;
    if (!lines || !search.query.trim()) {
      search.matches = [];
      search.selectedIndex = -1;
      search.selectedKey = void 0;
      search.selectionMode = "retain";
      search.component.setResult(-1, 0);
      return false;
    }
    const shouldRevealSelection = search.selectionMode !== "retain";
    const result = search.index.search(lines, search.query);
    const matches = result.matches;
    search.matches = matches;
    if (!result.changed && search.selectionMode === "retain")
      return false;
    const exactIndex = result.changed ? search.selectedKey ? matches.findIndex((match) => getAltScreenSearchMatchKey(match) === search.selectedKey) : -1 : search.selectedIndex;
    let selectedIndex = -1;
    if (matches.length > 0) {
      if (search.selectionMode === "query") {
        let low = 0;
        let high = matches.length;
        while (low < high) {
          const middle = low + Math.floor((high - low) / 2);
          if ((matches[middle].segments[0]?.row ?? 0) < search.anchorRow)
            low = middle + 1;
          else
            high = middle;
        }
        selectedIndex = low < matches.length ? low : 0;
      } else if (search.selectionMode === "next") {
        const baseIndex = exactIndex >= 0 ? exactIndex : Math.min(search.selectedIndex, matches.length - 1);
        selectedIndex = baseIndex < 0 ? 0 : (baseIndex + 1) % matches.length;
      } else if (search.selectionMode === "previous") {
        const baseIndex = exactIndex >= 0 ? exactIndex : Math.min(search.selectedIndex, matches.length - 1);
        selectedIndex = baseIndex < 0 ? matches.length - 1 : (baseIndex - 1 + matches.length) % matches.length;
      } else {
        selectedIndex = exactIndex >= 0 ? exactIndex : Math.min(Math.max(0, search.selectedIndex), matches.length - 1);
      }
    }
    search.selectedIndex = selectedIndex;
    search.selectedKey = selectedIndex >= 0 ? getAltScreenSearchMatchKey(matches[selectedIndex]) : void 0;
    search.selectionMode = "retain";
    search.component.setResult(selectedIndex, matches.length);
    if (!shouldRevealSelection)
      return false;
    const selected = matches[selectedIndex];
    const firstSegment = selected?.segments[0];
    const lastSegment = selected?.segments[selected.segments.length - 1];
    if (!box || !firstSegment || !lastSegment || scrollView.viewportHeight <= 0)
      return false;
    const before = scrollView.scrollTop;
    const visibleBottom = before + scrollView.viewportHeight - 1;
    let target = before;
    if (firstSegment.row < before || lastSegment.row > visibleBottom) {
      target = firstSegment.row - Math.floor(scrollView.viewportHeight / 3);
    }
    scrollView.scrollTo(target, { disableFollow: true });
    return scrollView.scrollTop !== before;
  }
  /** Show a transient message in the alternate-screen flash stack. */
  flash(message, durationMs) {
    this.flashes.flash(message, durationMs);
  }
  shouldDeferViewportInputToOverlay() {
    return this.isOverlayFocused() && this.activeSearch?.overlay?.isFocused() !== true;
  }
  clearComponentMouseGesture() {
    this.mouseCapture = void 0;
    this.mousePressTarget = void 0;
    this.mousePressPoint = void 0;
    this.mousePressMoved = false;
  }
  handleViewportInput(data) {
    if (data === FOCUS_OUT) {
      const hadActiveSelection = this.selectionPressActive;
      const hadNonEmptyActiveSelection = hadActiveSelection && this.getSelectionBounds() !== void 0;
      this.selectionPressActive = false;
      this.stopSelectionAutoScroll();
      this.stopScrollbarHover();
      if (this.activeSearch?.component.setHoveredNavigationDirection(void 0))
        this.requestRender();
      this.stopScrollbarDrag();
      this.pressedUrl = void 0;
      this.selectionDragged = false;
      this.clearComponentMouseGesture();
      this.lastComponentClick = void 0;
      if (hadActiveSelection) {
        this.selectionAnchor = void 0;
        this.selectionFocus = void 0;
        this.selectionGranularity = "character";
        this.selectionInitialRange = void 0;
        if (hadNonEmptyActiveSelection)
          this.requestRender();
      }
      this.lastClick = void 0;
      return { consume: true };
    }
    if (data === FOCUS_IN)
      return { consume: true };
    const wheelEvent = this.parseWheelEvent(data);
    if (wheelEvent) {
      const lines = this.wheelScroll.next(wheelEvent.direction, performance.now());
      const wheelDelta = wheelEvent.direction * ((wheelEvent.button & 8) !== 0 ? lines * ALT_WHEEL_SCROLL_MULTIPLIER : lines);
      const event = this.createMouseEvent("wheel", wheelEvent.button, wheelEvent.x, wheelEvent.y, { wheelDelta });
      const overlay = this.dispatchMouseToOverlay(event);
      const result = overlay.result ?? (overlay.hit ? void 0 : this.dispatchMouseToLayout(event));
      if (result) {
        if (this.applyMouseDispatchResult(event, result))
          this.requestRender();
        return { consume: true };
      }
      if (this.shouldDeferViewportInputToOverlay())
        return void 0;
      this.routeWheel(wheelEvent, wheelDelta);
      return { consume: true };
    }
    const mouseEvent = this.parseSgrMouseEvent(data);
    if (mouseEvent) {
      this.handleMouseEvent(mouseEvent);
      return { consume: true };
    }
    if (this.isMouseSequence(data))
      return { consume: true };
    const keybindings = getKeybindings7();
    const isRelease = isKeyRelease(data);
    if (keybindings.matches(data, "tui.altScreen.search")) {
      if (!isRelease)
        this.toggleSearch();
      return { consume: true };
    }
    if (this.activeSearch?.overlay?.isFocused()) {
      if (keybindings.matches(data, "tui.altScreen.searchNext")) {
        if (!isRelease)
          this.navigateSearch(1);
        return { consume: true };
      }
      if (keybindings.matches(data, "tui.altScreen.searchPrevious")) {
        if (!isRelease)
          this.navigateSearch(-1);
        return { consume: true };
      }
      if (keybindings.matches(data, "tui.altScreen.searchClose")) {
        if (!isRelease)
          this.closeSearch();
        return { consume: true };
      }
    }
    if (this.shouldDeferViewportInputToOverlay())
      return void 0;
    if (keybindings.matches(data, "tui.altScreen.pageUp")) {
      if (!isRelease) {
        this.scrollBy(-Math.max(1, this.getPrimaryScrollView().viewportHeight - PAGE_SCROLL_OVERLAP));
      }
      return { consume: true };
    }
    if (keybindings.matches(data, "tui.altScreen.pageDown")) {
      if (!isRelease) {
        this.scrollBy(Math.max(1, this.getPrimaryScrollView().viewportHeight - PAGE_SCROLL_OVERLAP));
      }
      return { consume: true };
    }
    if (keybindings.matches(data, "tui.altScreen.halfPageUp")) {
      if (!isRelease)
        this.scrollBy(-Math.max(1, Math.floor(this.getPrimaryScrollView().viewportHeight / 2)));
      return { consume: true };
    }
    if (keybindings.matches(data, "tui.altScreen.halfPageDown")) {
      if (!isRelease)
        this.scrollBy(Math.max(1, Math.floor(this.getPrimaryScrollView().viewportHeight / 2)));
      return { consume: true };
    }
    if (keybindings.matches(data, "tui.altScreen.lineUp")) {
      if (!isRelease)
        this.scrollBy(-1);
      return { consume: true };
    }
    if (keybindings.matches(data, "tui.altScreen.lineDown")) {
      if (!isRelease)
        this.scrollBy(1);
      return { consume: true };
    }
    if (keybindings.matches(data, "tui.altScreen.previousPrompt")) {
      if (!isRelease)
        this.scrollToPrompt(-1);
      return { consume: true };
    }
    if (keybindings.matches(data, "tui.altScreen.nextPrompt")) {
      if (!isRelease)
        this.scrollToPrompt(1);
      return { consume: true };
    }
    if (keybindings.matches(data, "tui.altScreen.top")) {
      if (!isRelease)
        this.scrollToTop();
      return { consume: true };
    }
    if (keybindings.matches(data, "tui.altScreen.bottom")) {
      if (!isRelease)
        this.scrollToBottom();
      return { consume: true };
    }
    return void 0;
  }
  decodeMouseButton(button) {
    switch (button & 3) {
      case 0:
        return "left";
      case 1:
        return "middle";
      case 2:
        return "right";
      default:
        return "none";
    }
  }
  createMouseEvent(type, button, x, y, extra = {}) {
    return {
      type,
      button: type === "wheel" ? "none" : this.decodeMouseButton(button),
      x,
      y,
      screenX: x,
      screenY: y,
      width: Math.max(1, this.terminal.columns),
      height: Math.max(1, this.terminal.rows),
      shift: (button & 4) !== 0,
      alt: (button & 8) !== 0,
      ctrl: (button & 16) !== 0,
      ...extra.wheelDelta === void 0 ? {} : { wheelDelta: extra.wheelDelta },
      ...extra.clickCount === void 0 ? {} : { clickCount: extra.clickCount }
    };
  }
  dispatchMouseToLayout(event) {
    if (!this.currentLayout)
      return void 0;
    const visited = /* @__PURE__ */ new Set();
    const boxes = getLayoutBoxesAt(this.currentLayout, event.screenX, event.screenY);
    for (const box of boxes) {
      if (visited.has(box.component))
        continue;
      if (getLayoutNode(box.component) && box.component.handleMouse === Container3.prototype.handleMouse)
        continue;
      visited.add(box.component);
      const result = dispatchMouseEvent3(box.component, {
        ...event,
        x: event.screenX - box.rect.x,
        y: event.screenY - box.rect.y,
        width: box.rect.width,
        height: box.rect.height
      });
      if (result)
        return result;
    }
    return void 0;
  }
  applyMouseDispatchResult(event, result) {
    const focusTarget = this.resolveMouseFocusTarget(result.focusTarget ?? result.target.component);
    const focusChanged = result.focus === true && this.getFocusedComponent() !== focusTarget;
    if (result.focus)
      this.setFocus(focusTarget);
    if (result.capture)
      this.mouseCapture = result.target;
    return result.render ?? (focusChanged || event.type === "press" || event.type === "click" || event.type === "drag" || event.type === "wheel");
  }
  dispatchMouseToTarget(event, target) {
    return dispatchMouseEvent3(target.component, retargetMouseEvent(event, target));
  }
  getComponentClickCount(target, x, y) {
    const now = Date.now();
    const previous = this.lastComponentClick;
    const count = previous && now - previous.timestamp <= DOUBLE_CLICK_INTERVAL_MS && previous.component === target.component && previous.x === x && previous.y === y ? previous.count % 3 + 1 : 1;
    this.lastComponentClick = { timestamp: now, count, component: target.component, x, y };
    return count;
  }
  clearTextSelection() {
    this.stopSelectionAutoScroll();
    this.selectionPressActive = false;
    this.selectionAnchor = void 0;
    this.selectionFocus = void 0;
    this.selectionGranularity = "character";
    this.selectionInitialRange = void 0;
    this.pressedUrl = void 0;
    this.selectionDragged = false;
  }
  handleMouseEvent(raw) {
    const isMotion = (raw.button & 32) !== 0;
    const type = raw.release ? "release" : isMotion ? this.decodeMouseButton(raw.button) === "none" ? "move" : "drag" : "press";
    const event = this.createMouseEvent(type, raw.button, raw.x, raw.y);
    if (this.mouseCapture || this.mousePressTarget) {
      const target = this.mouseCapture ?? this.mousePressTarget;
      if (this.mousePressPoint && (raw.x !== this.mousePressPoint.x || raw.y !== this.mousePressPoint.y)) {
        this.mousePressMoved = true;
        this.lastComponentClick = void 0;
      }
      let render = false;
      const targetResult = this.dispatchMouseToTarget(event, target);
      if (targetResult)
        render = this.applyMouseDispatchResult(event, targetResult);
      if (raw.release) {
        if (!this.mousePressMoved && this.mousePressPoint?.x === raw.x && this.mousePressPoint.y === raw.y) {
          const clickEvent = this.createMouseEvent("click", raw.button, raw.x, raw.y, {
            clickCount: this.getComponentClickCount(target, raw.x, raw.y)
          });
          const clickResult = this.dispatchMouseToTarget(clickEvent, target);
          if (clickResult)
            render = this.applyMouseDispatchResult(clickEvent, clickResult) || render;
        }
        this.clearComponentMouseGesture();
      }
      if (render)
        this.requestRender();
      return;
    }
    if (this.handleSearchMouseEvent(raw))
      return;
    const overlay = this.dispatchMouseToOverlay(event);
    if (!overlay.hit) {
      if (this.handleScrollToEndIndicatorMouseEvent(raw))
        return;
      const scrollbarHandled = this.handleScrollbarMouseEvent(raw);
      if (!this.scrollbarDrag)
        this.updateScrollbarHover(raw.x, raw.y);
      if (scrollbarHandled)
        return;
    } else {
      this.stopScrollbarHover();
    }
    const result = overlay.result ?? (overlay.hit ? void 0 : this.dispatchMouseToLayout(event));
    if (result) {
      const render = this.applyMouseDispatchResult(event, result);
      if (type === "press") {
        this.clearTextSelection();
        this.mousePressTarget = result.target;
        this.mousePressPoint = { x: raw.x, y: raw.y };
        this.mousePressMoved = false;
      }
      if (render)
        this.requestRender();
      return;
    }
    if (this.handleRightClickPaste(raw))
      return;
    this.handleSelectionMouseEvent(raw);
  }
  parseWheelEvent(data) {
    const sgr = /^\x1b\[<(\d+);(\d+);(\d+)[Mm]$/.exec(data);
    if (sgr) {
      const button = Number.parseInt(sgr[1], 10);
      if ((button & 64) === 0)
        return void 0;
      const direction = button & 3;
      if (direction !== 0 && direction !== 1)
        return void 0;
      return {
        direction: direction === 0 ? -1 : 1,
        x: Number.parseInt(sgr[2], 10) - 1,
        y: Number.parseInt(sgr[3], 10) - 1,
        button
      };
    }
    if (data.length === 6 && data.startsWith("\x1B[M")) {
      const button = data.charCodeAt(3) - 32;
      if ((button & 64) === 0)
        return void 0;
      const direction = button & 3;
      if (direction !== 0 && direction !== 1)
        return void 0;
      return {
        direction: direction === 0 ? -1 : 1,
        x: data.charCodeAt(4) - 33,
        y: data.charCodeAt(5) - 33,
        button
      };
    }
    return void 0;
  }
  routeWheel(event, delta) {
    let remaining = delta;
    const seen = /* @__PURE__ */ new Set();
    for (const scrollView of this.currentLayout ? getScrollViewsAt(this.currentLayout, event.x, event.y) : []) {
      seen.add(scrollView);
      remaining = scrollView.scrollBy(remaining);
      if (remaining === 0 || scrollView.overscroll === "contain")
        break;
    }
    const primary = this.getPrimaryScrollView();
    if (remaining !== 0 && !seen.has(primary))
      primary.scrollBy(remaining);
    this.updateScrollbarHover(event.x, event.y);
    this.requestRender();
  }
  parseSgrMouseEvent(data) {
    const match = /^\x1b\[<(\d+);(\d+);(\d+)([Mm])$/.exec(data);
    if (!match)
      return void 0;
    return {
      button: Number.parseInt(match[1], 10),
      x: Number.parseInt(match[2], 10) - 1,
      y: Number.parseInt(match[3], 10) - 1,
      release: match[4] === "m"
    };
  }
  handleRightClickPaste(event) {
    if (!this.onRightClickPaste || process.platform !== "win32" || process.env.TERM_PROGRAM?.toLowerCase() === "vscode" || event.release || event.button !== 2) {
      return false;
    }
    try {
      this.onRightClickPaste();
    } catch {
    }
    return true;
  }
  handleScrollToEndIndicatorMouseEvent(event) {
    const rect = this.scrollToEndIndicatorRect;
    if (!rect || event.release || (event.button & 32) !== 0 || (event.button & 3) !== 0)
      return false;
    if (event.y !== rect.row || event.x < rect.column || event.x >= rect.column + rect.width)
      return false;
    this.scrollToBottom();
    return true;
  }
  getScrollbarTargetAt(x, y, includeHiddenAuto = false) {
    if (this.hasOverlay() || !this.currentLayout)
      return void 0;
    for (const scrollView of getScrollViewsAt(this.currentLayout, x, y)) {
      const box = getScrollViewBox(this.currentLayout, scrollView);
      const geometry = box ? getScrollbarGeometry(box, includeHiddenAuto) : void 0;
      if (geometry && x === geometry.column && y >= geometry.trackTop && y < geometry.trackTop + geometry.trackHeight) {
        return { scrollView, geometry };
      }
    }
    return void 0;
  }
  setScrollbarHover(scrollView) {
    if (scrollView === this.scrollbarHover)
      return;
    this.scrollbarHover?.setScrollbarActive(false);
    this.scrollbarHover = scrollView;
    this.scrollbarHover?.setScrollbarActive(true);
  }
  updateScrollbarHover(x, y) {
    this.setScrollbarHover(this.getScrollbarTargetAt(x, y, true)?.scrollView);
  }
  stopScrollbarHover() {
    this.setScrollbarHover(void 0);
  }
  scrollScrollbarToPointer(scrollView, geometry, pointerY, grabOffset) {
    const maxThumbOffset = geometry.trackHeight - geometry.thumbHeight;
    const thumbOffset = Math.max(0, Math.min(maxThumbOffset, pointerY - geometry.trackTop - grabOffset));
    const scrollTop = maxThumbOffset === 0 ? 0 : Math.round(thumbOffset / maxThumbOffset * geometry.maxScrollTop);
    scrollView.scrollTo(scrollTop);
  }
  handleScrollbarMouseEvent(event) {
    if (this.scrollbarDrag) {
      if (event.release) {
        this.stopScrollbarDrag();
        return true;
      }
      const box = this.currentLayout ? getScrollViewBox(this.currentLayout, this.scrollbarDrag.scrollView) : void 0;
      const geometry = box ? getScrollbarGeometry(box) : void 0;
      if (geometry) {
        this.scrollScrollbarToPointer(this.scrollbarDrag.scrollView, geometry, event.y, this.scrollbarDrag.grabOffset);
      }
      return true;
    }
    if (event.release || (event.button & 32) !== 0 || (event.button & 3) !== 0)
      return false;
    const target = this.getScrollbarTargetAt(event.x, event.y);
    if (!target)
      return false;
    this.stopSelectionAutoScroll();
    this.selectionPressActive = false;
    this.selectionAnchor = void 0;
    this.selectionFocus = void 0;
    this.selectionGranularity = "character";
    this.selectionInitialRange = void 0;
    this.lastClick = void 0;
    this.pressedUrl = void 0;
    this.selectionDragged = false;
    this.setScrollbarHover(target.scrollView);
    const onThumb = event.y >= target.geometry.thumbTop && event.y < target.geometry.thumbTop + target.geometry.thumbHeight;
    const grabOffset = onThumb ? event.y - target.geometry.thumbTop : Math.floor(target.geometry.thumbHeight / 2);
    if (!onThumb)
      this.scrollScrollbarToPointer(target.scrollView, target.geometry, event.y, grabOffset);
    this.scrollbarDrag = {
      scrollView: target.scrollView,
      grabOffset
    };
    return true;
  }
  stopScrollbarDrag() {
    this.scrollbarDrag = void 0;
  }
  getScrollSelectionPoint(scrollView, x, y) {
    if (!this.currentLayout)
      return void 0;
    const box = getScrollViewBox(this.currentLayout, scrollView);
    if (!box || box.rect.height <= 0 || box.clip.height <= 0)
      return void 0;
    const visibleTop = Math.max(0, box.rect.y, box.clip.y);
    const visibleBottom = Math.min(this.terminal.rows - 1, box.rect.y + box.rect.height - 1, box.clip.y + box.clip.height - 1);
    if (visibleBottom < visibleTop)
      return void 0;
    const pointerRow = Math.max(visibleTop, Math.min(visibleBottom, y));
    const maxContentRow = Math.max(0, (box.scrollContentLines?.length ?? 1) - 1);
    return {
      row: Math.max(0, Math.min(maxContentRow, scrollView.scrollTop + pointerRow - box.rect.y)),
      col: Math.max(0, Math.min(box.rect.width - 1, x - box.rect.x)),
      scrollView
    };
  }
  getSelectionPoint(event, scrollView) {
    if (scrollView) {
      const point = this.getScrollSelectionPoint(scrollView, event.x, event.y);
      if (point)
        return point;
    }
    return {
      row: Math.max(0, Math.min(this.terminal.rows - 1, event.y)),
      col: Math.max(0, Math.min(this.terminal.columns - 1, event.x))
    };
  }
  getSelectionSourceLine(point) {
    if (point.scrollView && this.currentLayout) {
      const lines = getScrollViewBox(this.currentLayout, point.scrollView)?.scrollContentLines;
      if (lines)
        return lines[point.row] ?? "";
    }
    return this.previousScreen[point.row] ?? "";
  }
  getWordSelection(point) {
    const line = stripTerminalSequences2(this.getSelectionSourceLine(point));
    const segments = [];
    let start = 0;
    for (const segment of wordSegmenter3.segment(line)) {
      const end = start + visibleWidth13(segment.segment);
      const joiner = TERMINAL_WORD_SELECTION_JOINERS.has(segment.segment);
      segments.push({ start, end, selectable: segment.isWordLike === true || joiner, joiner });
      start = end;
    }
    const clickedSegmentIndex = segments.findIndex((segment) => point.col >= segment.start && point.col < segment.end);
    if (clickedSegmentIndex < 0)
      return void 0;
    const canJoin = /* @__PURE__ */ __name((left, right) => left.selectable && right.selectable && (left.joiner || right.joiner), "canJoin");
    let selectionStart = segments[clickedSegmentIndex].start;
    let selectionEnd = segments[clickedSegmentIndex].end;
    for (let index = clickedSegmentIndex; index > 0 && canJoin(segments[index - 1], segments[index]); index--) {
      selectionStart = segments[index - 1].start;
    }
    for (let index = clickedSegmentIndex; index < segments.length - 1 && canJoin(segments[index], segments[index + 1]); index++) {
      selectionEnd = segments[index + 1].end;
    }
    return {
      start: { ...point, col: selectionStart },
      end: { ...point, col: selectionEnd, boundary: true }
    };
  }
  getLineSelection(point) {
    return {
      start: { ...point, col: 0 },
      end: { ...point, col: visibleWidth13(this.getSelectionSourceLine(point)), boundary: true }
    };
  }
  updateSelectionFocus(point) {
    if (this.selectionGranularity === "character" || !this.selectionInitialRange) {
      this.selectionFocus = point;
      return;
    }
    const range = this.selectionGranularity === "word" ? this.getWordSelection(point) : this.getLineSelection(point);
    if (!range)
      return;
    const initial = this.selectionInitialRange;
    const targetBeforeInitial = range.start.row < initial.start.row || range.start.row === initial.start.row && range.start.col < initial.start.col;
    if (targetBeforeInitial) {
      this.selectionAnchor = initial.end;
      this.selectionFocus = range.start;
    } else {
      this.selectionAnchor = initial.start;
      this.selectionFocus = range.end;
    }
  }
  getClickCount(point, word) {
    const now = Date.now();
    const previous = this.lastClick;
    const count = word && previous && now - previous.timestamp <= DOUBLE_CLICK_INTERVAL_MS && previous.row === point.row && previous.scrollView === point.scrollView && previous.wordStart === word.start.col && previous.wordEnd === word.end.col ? previous.count % 3 + 1 : 1;
    this.lastClick = word ? {
      timestamp: now,
      count,
      row: point.row,
      scrollView: point.scrollView,
      wordStart: word.start.col,
      wordEnd: word.end.col
    } : void 0;
    return count;
  }
  updateSelectionAutoScroll(event) {
    const scrollView = this.selectionAnchor?.scrollView;
    if (!scrollView || !this.currentLayout) {
      this.stopSelectionAutoScroll();
      return;
    }
    const box = getScrollViewBox(this.currentLayout, scrollView);
    if (!box || box.rect.height <= 0 || box.clip.height <= 0) {
      this.stopSelectionAutoScroll();
      return;
    }
    const visibleTop = Math.max(0, box.rect.y, box.clip.y);
    const visibleBottom = Math.min(this.terminal.rows - 1, box.rect.y + box.rect.height - 1, box.clip.y + box.clip.height - 1);
    this.selectionDragPointer = { x: event.x, y: event.y };
    this.selectionAutoScrollDirection = event.y <= visibleTop ? -1 : event.y >= visibleBottom ? 1 : 0;
    if (this.selectionAutoScrollDirection === 0) {
      this.stopSelectionAutoScroll();
      return;
    }
    if (this.selectionAutoScrollTimer)
      return;
    this.selectionAutoScrollTimer = setInterval(() => this.autoScrollSelection(), 50);
    this.selectionAutoScrollTimer.unref();
  }
  autoScrollSelection() {
    const scrollView = this.selectionAnchor?.scrollView;
    const pointer = this.selectionDragPointer;
    const direction = this.selectionAutoScrollDirection;
    if (!scrollView || !pointer || direction === 0) {
      this.stopSelectionAutoScroll();
      return;
    }
    const remaining = scrollView.scrollBy(direction);
    if (remaining === direction) {
      this.stopSelectionAutoScroll();
      return;
    }
    const point = this.getScrollSelectionPoint(scrollView, pointer.x, pointer.y);
    if (point)
      this.updateSelectionFocus(point);
    this.requestRender();
  }
  stopSelectionAutoScroll() {
    if (this.selectionAutoScrollTimer) {
      clearInterval(this.selectionAutoScrollTimer);
      this.selectionAutoScrollTimer = void 0;
    }
    this.selectionAutoScrollDirection = 0;
    this.selectionDragPointer = void 0;
  }
  handleSelectionMouseEvent(event) {
    const button = event.button & 3;
    if (button !== 0 && !(event.release && button === 3))
      return;
    const anchorScrollView = this.selectionAnchor?.scrollView;
    const point = this.getSelectionPoint(event, anchorScrollView);
    if (event.release) {
      if (!this.selectionPressActive)
        return;
      this.selectionPressActive = false;
      this.stopSelectionAutoScroll();
      if (!this.selectionAnchor)
        return;
      this.updateSelectionFocus(point);
      const isClick = !this.selectionDragged && this.selectionAnchor.scrollView === point.scrollView && this.selectionAnchor.row === point.row && this.selectionAnchor.col === point.col;
      const clickedUrl = isClick ? this.pressedUrl : void 0;
      this.pressedUrl = void 0;
      if (clickedUrl && this.openUrl) {
        this.selectionAnchor = void 0;
        this.selectionFocus = void 0;
        try {
          this.openUrl(clickedUrl);
        } catch {
        }
        this.requestRender();
        return;
      }
      if (isClick) {
        const clickEvent = this.createMouseEvent("click", event.button, event.x, event.y, {
          clickCount: this.lastClick?.count ?? 1
        });
        const overlay = this.dispatchMouseToOverlay(clickEvent);
        const result = overlay.result ?? (overlay.hit ? void 0 : this.dispatchMouseToLayout(clickEvent));
        if (result) {
          const render = this.applyMouseDispatchResult(clickEvent, result);
          this.clearTextSelection();
          if (render)
            this.requestRender();
          return;
        }
      }
      if (this.copyOnSelect)
        void this.copySelectionToClipboard();
      this.requestRender();
      return;
    }
    if ((event.button & 32) !== 0) {
      if (!this.selectionPressActive || !this.selectionAnchor)
        return;
      this.selectionDragged = true;
      this.lastClick = void 0;
      this.pressedUrl = void 0;
      this.updateSelectionFocus(point);
      this.updateSelectionAutoScroll(event);
      this.requestRender();
      return;
    }
    this.stopSelectionAutoScroll();
    this.selectionPressActive = true;
    const scrollView = !this.hasOverlay() && this.currentLayout ? getScrollViewsAt(this.currentLayout, event.x, event.y)[0] : void 0;
    const anchor = this.getSelectionPoint(event, scrollView);
    const word = this.getWordSelection(anchor);
    const clickCount = this.getClickCount(anchor, word);
    const range = clickCount === 2 ? word : clickCount === 3 ? this.getLineSelection(anchor) : void 0;
    this.selectionGranularity = range ? clickCount === 2 ? "word" : "line" : "character";
    this.selectionInitialRange = range;
    this.selectionAnchor = range?.start ?? anchor;
    this.selectionFocus = range?.end ?? anchor;
    this.selectionDragged = false;
    this.pressedUrl = range ? void 0 : getOsc8LinkAtColumn(this.previousScreen[Math.max(0, Math.min(this.terminal.rows - 1, event.y))] ?? "", Math.max(0, Math.min(this.terminal.columns - 1, event.x)));
    this.requestRender();
  }
  getSelectionBounds() {
    if (!this.selectionAnchor || !this.selectionFocus)
      return void 0;
    if (this.selectionAnchor.scrollView !== this.selectionFocus.scrollView)
      return void 0;
    const anchorBeforeFocus = this.selectionAnchor.row < this.selectionFocus.row || this.selectionAnchor.row === this.selectionFocus.row && this.selectionAnchor.col < this.selectionFocus.col;
    if (this.selectionAnchor.row === this.selectionFocus.row && this.selectionAnchor.col === this.selectionFocus.col) {
      return void 0;
    }
    return anchorBeforeFocus ? { start: this.selectionAnchor, end: this.selectionFocus } : { start: this.selectionFocus, end: this.selectionAnchor };
  }
  getSelectionColumns(line, row, selection, minColumn = 0, maxColumn = visibleWidth13(line)) {
    const lineWidth = visibleWidth13(line);
    let start = Math.max(0, minColumn);
    let end = Math.min(lineWidth, maxColumn);
    if (row === selection.start.row) {
      start = getGraphemeCellRange2(line, selection.start.col)?.start ?? Math.min(selection.start.col, lineWidth);
    }
    if (row === selection.end.row) {
      end = selection.end.boundary ? Math.min(selection.end.col, lineWidth) : getGraphemeCellRange2(line, selection.end.col)?.end ?? Math.min(selection.end.col + 1, lineWidth);
    }
    return { start: Math.max(minColumn, start), end: Math.min(maxColumn, end) };
  }
  getActiveSelectionText() {
    const selection = this.getSelectionBounds();
    if (!selection)
      return void 0;
    let sourceLines = this.previousScreen;
    if (selection.start.scrollView) {
      if (!this.currentLayout)
        return void 0;
      const box = getScrollViewBox(this.currentLayout, selection.start.scrollView);
      if (!box?.scrollContentLines)
        return void 0;
      sourceLines = box.scrollContentLines;
    }
    const lines = [];
    for (let row = selection.start.row; row <= selection.end.row; row++) {
      const line = sourceLines[row] ?? "";
      const columns = this.getSelectionColumns(line, row, selection);
      lines.push(stripTerminalSequences2(sliceByColumn4(line, columns.start, Math.max(0, columns.end - columns.start), true)).trimEnd());
    }
    const text = lines.join("\n");
    return text.length === 0 ? void 0 : text;
  }
  async copySelectionToClipboard() {
    const text = this.getActiveSelectionText();
    if (!text)
      return false;
    return this.copyTextToClipboard(text);
  }
  async copyTextToClipboard(text) {
    if (this.copySelection) {
      const result = await this.copySelection(text);
      const ok = result === true;
      this.flash(ok ? "Copied!" : typeof result === "string" ? result : "Copy failed", ok ? void 0 : COPY_ERROR_FLASH_DURATION_MS);
      return ok;
    }
    this.terminal.write(`\x1B]52;c;${Buffer.from(text).toString("base64")}\x07`);
    this.flash("Copied!");
    return true;
  }
  applySearchTextHighlight(text, current) {
    const style = current ? this.searchCurrentMatchStyle : this.searchMatchStyle;
    let result = "";
    let plainStart = 0;
    let index = 0;
    while (index < text.length) {
      const ansi = extractAnsiCode2(text, index);
      if (!ansi) {
        index += 1;
        continue;
      }
      if (index > plainStart)
        result += style(text.slice(plainStart, index));
      result += ansi.code;
      index += ansi.length;
      plainStart = index;
    }
    if (plainStart < text.length)
      result += style(text.slice(plainStart));
    return result;
  }
  applySearchHighlights(screen, layout) {
    const search = this.activeSearch;
    if (!search || search.selectedIndex < 0 || search.matches.length === 0)
      return screen;
    const scrollView = layout.primaryScrollView ?? this.implicitScrollView;
    const box = getScrollViewBox(layout, scrollView);
    if (!box)
      return screen;
    const rangesByRow = /* @__PURE__ */ new Map();
    const scrollbarColumn = getScrollbarGeometry(box)?.column;
    const minRow = Math.max(0, box.rect.y, box.clip.y);
    const maxRow = Math.min(screen.length, box.rect.y + box.rect.height, box.clip.y + box.clip.height);
    const minColumn = Math.max(0, box.rect.x, box.clip.x);
    const maxColumn = Math.min(this.terminal.columns, box.rect.x + box.rect.width, box.clip.x + box.clip.width, scrollbarColumn ?? Number.POSITIVE_INFINITY);
    const minContentRow = scrollView.scrollTop + minRow - box.rect.y;
    const maxContentRow = scrollView.scrollTop + maxRow - box.rect.y - 1;
    let low = 0;
    let high = search.matches.length;
    while (low < high) {
      const middle = low + Math.floor((high - low) / 2);
      const match = search.matches[middle];
      const lastRow = match.segments[match.segments.length - 1]?.row ?? -1;
      if (lastRow < minContentRow)
        low = middle + 1;
      else
        high = middle;
    }
    for (let matchIndex = low; matchIndex < search.matches.length; matchIndex++) {
      const match = search.matches[matchIndex];
      if ((match.segments[0]?.row ?? 0) > maxContentRow)
        break;
      for (const segment of match.segments) {
        const row = box.rect.y + segment.row - scrollView.scrollTop;
        if (row < minRow || row >= maxRow)
          continue;
        const startCol = Math.max(minColumn, box.rect.x + segment.startCol);
        const endCol = Math.min(maxColumn, box.rect.x + segment.endCol);
        if (endCol <= startCol)
          continue;
        const ranges = rangesByRow.get(row) ?? [];
        ranges.push({ startCol, endCol, current: matchIndex === search.selectedIndex });
        rangesByRow.set(row, ranges);
      }
    }
    const result = [...screen];
    for (const [row, ranges] of rangesByRow) {
      let line = result[row] ?? "";
      if (isImageLine3(line))
        continue;
      const lineWidth = visibleWidth13(line);
      for (const range of ranges.sort((a, b) => b.startCol - a.startCol)) {
        const startCol = Math.min(range.startCol, lineWidth);
        const endCol = Math.min(range.endCol, lineWidth);
        if (endCol <= startCol)
          continue;
        const before = sliceByColumn4(line, 0, startCol, true);
        const highlighted = sliceByColumn4(line, startCol, endCol - startCol, true);
        const after = sliceByColumn4(line, endCol, Math.max(0, lineWidth - endCol), true);
        line = `${before}${this.applySearchTextHighlight(highlighted, range.current)}${after}`;
      }
      result[row] = line;
    }
    return result;
  }
  applySelectionHighlight(text) {
    let result = "\x1B[7m";
    let index = 0;
    while (index < text.length) {
      const ansi = extractAnsiCode2(text, index);
      if (!ansi) {
        result += text[index];
        index += 1;
        continue;
      }
      result += ansi.code;
      if (ansi.code.endsWith("m"))
        result += "\x1B[7m";
      index += ansi.length;
    }
    return `${result}\x1B[27m`;
  }
  applySelection(screen, layout = this.currentLayout) {
    const selection = this.getSelectionBounds();
    if (!selection)
      return screen;
    let screenSelection = selection;
    let minRow = 0;
    let maxRow = screen.length - 1;
    let minColumn = 0;
    let maxColumn = this.terminal.columns;
    if (selection.start.scrollView) {
      if (!layout)
        return screen;
      const box = getScrollViewBox(layout, selection.start.scrollView);
      if (!box)
        return screen;
      minRow = Math.max(0, box.rect.y, box.clip.y);
      maxRow = Math.min(screen.length - 1, box.rect.y + box.rect.height - 1, box.clip.y + box.clip.height - 1);
      minColumn = Math.max(0, box.rect.x, box.clip.x);
      maxColumn = Math.min(this.terminal.columns, box.rect.x + box.rect.width, box.clip.x + box.clip.width);
      screenSelection = {
        start: {
          ...selection.start,
          row: box.rect.y + selection.start.row - selection.start.scrollView.scrollTop,
          col: box.rect.x + selection.start.col
        },
        end: {
          ...selection.end,
          row: box.rect.y + selection.end.row - selection.start.scrollView.scrollTop,
          col: box.rect.x + selection.end.col
        }
      };
    }
    return screen.map((line, row) => {
      if (row < minRow || row > maxRow || row < screenSelection.start.row || row > screenSelection.end.row || isImageLine3(line)) {
        return line;
      }
      const lineWidth = visibleWidth13(line);
      const columns = this.getSelectionColumns(line, row, screenSelection, minColumn, maxColumn);
      if (columns.end <= columns.start)
        return line;
      const before = sliceByColumn4(line, 0, columns.start, true);
      const selected = sliceByColumn4(line, columns.start, columns.end - columns.start, true);
      const after = sliceByColumn4(line, columns.end, Math.max(0, lineWidth - columns.end), true);
      return `${before}${this.applySelectionHighlight(selected)}${after}`;
    });
  }
  isMouseSequence(data) {
    return /^\x1b\[<\d+;\d+;\d+[Mm]$/.test(data) || data.length === 6 && data.startsWith("\x1B[M");
  }
  compositeScrollToEndIndicator(screen, layout, width) {
    this.scrollToEndIndicatorRect = void 0;
    const scrollView = layout.primaryScrollView ?? this.implicitScrollView;
    if (!this.scrollToEndIndicator || !scrollView.followEnd || scrollView.isFollowingEnd)
      return screen;
    const box = getScrollViewBox(layout, scrollView);
    const clip = box?.clip;
    if (!clip || clip.width <= 0 || clip.height <= 0)
      return screen;
    const row = clip.y + clip.height - 1;
    if (row >= screen.length || isImageLine3(screen[row] ?? ""))
      return screen;
    const scrollbarColumn = box ? getScrollbarGeometry(box)?.column : void 0;
    const label = truncateToWidth8(this.scrollToEndIndicator(), clip.width, "");
    const labelWidth = visibleWidth13(label);
    const column = clip.x + Math.floor((clip.width - labelWidth) / 2);
    const rightEdge = scrollbarColumn ?? clip.x + clip.width;
    const availableWidth = Math.max(0, rightEdge - column);
    const text = truncateToWidth8(label, availableWidth, "");
    const textWidth = visibleWidth13(text);
    if (textWidth === 0)
      return screen;
    const result = [...screen];
    result[row] = compositeTuiLine3(result[row] ?? "", text, column, textWidth, width);
    this.scrollToEndIndicatorRect = { row, column, width: textWidth };
    return result;
  }
  compositeFlashes(screen, width, height) {
    const flashLines = this.flashes.render(width).slice(-height);
    if (flashLines.length === 0)
      return screen;
    const result = [...screen];
    while (result.length < height)
      result.push("");
    for (let row = 0; row < flashLines.length; row++) {
      const line = flashLines[row];
      const flashWidth = visibleWidth13(line);
      if (flashWidth === 0)
        continue;
      result[row] = compositeTuiLine3(result[row] ?? "", line, width - flashWidth, flashWidth, width);
    }
    return result;
  }
  doRender() {
    if (this.stopped || !this.altScreenActive)
      return;
    const width = Math.max(1, this.terminal.columns);
    const height = Math.max(1, this.terminal.rows);
    const root = this.layoutRoot ?? this.implicitScrollView;
    let nextLayout = renderLayoutFrame(root, width, height, () => this.requestRender());
    if (this.refreshSearch(nextLayout)) {
      nextLayout = renderLayoutFrame(root, width, height, () => this.requestRender());
    }
    let screen = nextLayout.lines.map((line) => line.replace(OSC133_ZONE_PREFIX2, ""));
    screen = this.applySearchHighlights(screen, nextLayout);
    screen = this.compositeScrollToEndIndicator(screen, nextLayout, width);
    screen = this.compositeOverlays(screen, width, height);
    if (screen.length > height)
      screen = screen.slice(screen.length - height);
    screen = this.applySelection(screen, nextLayout);
    screen = this.compositeFlashes(screen, width, height);
    const cursorPos = this.extractCursorPosition(screen, height);
    screen = this.applyLineResets(screen).map((line) => {
      if (isImageLine3(line) || visibleWidth13(line) <= width)
        return line;
      return sliceByColumn4(line, 0, width, true);
    });
    const fullRedraw = this.previousScreen.length === 0 || this.previousScreenWidth !== width || this.previousScreenHeight !== height;
    const imagesNeedRedraw = screen.some((line, row) => line !== this.previousScreen[row] && (isImageLine3(line) || isImageLine3(this.previousScreen[row] ?? "")));
    const redrawImages = fullRedraw || imagesNeedRedraw;
    const hadUploadedKittyImages = this.uploadedKittyImages.size > 0;
    const preparedKittyScreen = redrawImages && this.imageProtocol === "kitty" ? this.prepareKittyScreen(screen) : { lines: screen, evictedImageDeletion: "" };
    let buffer = BEGIN_SYNCHRONIZED_OUTPUT;
    if (fullRedraw) {
      this.fullRedrawCount += 1;
      const clearImages = this.imageProtocol === "kitty" && hadUploadedKittyImages ? deleteAllKittyPlacements() : this.deleteKittyImages();
      buffer += `${clearImages}\x1B[2J`;
    } else if (imagesNeedRedraw) {
      if (this.imageProtocol === "iterm2")
        buffer += "\x1B[2J";
      else if (this.imageProtocol === "kitty")
        buffer += deleteAllKittyPlacements();
    }
    buffer += preparedKittyScreen.evictedImageDeletion;
    const clearRowsBeforeKittyImages = redrawImages && this.imageProtocol === "kitty" && screen.some(isImageLine3) && (Boolean(process.env.WEZTERM_PANE) || process.env.TERM_PROGRAM?.toLowerCase() === "wezterm");
    if (clearRowsBeforeKittyImages) {
      for (let row = 0; row < height; row++) {
        if (!fullRedraw && !imagesNeedRedraw && screen[row] === this.previousScreen[row])
          continue;
        buffer += `\x1B[${row + 1};1H\x1B[2K`;
      }
    }
    for (let row = 0; row < height; row++) {
      if (!fullRedraw && !imagesNeedRedraw && screen[row] === this.previousScreen[row])
        continue;
      buffer += `\x1B[${row + 1};1H${clearRowsBeforeKittyImages ? "" : "\x1B[2K"}${preparedKittyScreen.lines[row] ?? ""}`;
    }
    if (cursorPos) {
      buffer += `\x1B[${cursorPos.row + 1};${Math.min(width, cursorPos.col) + 1}H`;
      buffer += this.getShowHardwareCursor() ? "\x1B[?25h" : "\x1B[?25l";
    } else {
      buffer += "\x1B[?25l";
    }
    buffer += END_SYNCHRONIZED_OUTPUT;
    this.terminal.write(buffer);
    this.previousScreen = screen;
    this.previousScreenWidth = width;
    this.previousScreenHeight = height;
    this.currentLayout = nextLayout;
  }
};

// pi-dist/pi-tui/tui-main-screen.js
import * as fs2 from "node:fs";
import * as os from "node:os";
import * as path2 from "node:path";
import { deleteKittyImage as deleteKittyImage2, isImageLine as isImageLine4 } from "../terminal-image.js";
import { TuiBase as TuiBase2 } from "../tui.js";
import { visibleWidth as visibleWidth14 } from "../utils.js";
var KITTY_SEQUENCE_PREFIX = "\x1B_G";
var MAX_RENDER_WRITE_CHARS = 1024 * 1024;
var BoundedTerminalWriter = class {
  static {
    __name(this, "BoundedTerminalWriter");
  }
  buffer = "";
  writtenChars = 0;
  write;
  constructor(write) {
    this.write = write;
  }
  /**
   * Append terminal data, flushing full chunks as needed. Callers must call `flush()` after the final append.
   * @param value Terminal data to write in order; oversized values are split without splitting surrogate pairs.
   */
  append(value) {
    let offset = 0;
    while (offset < value.length) {
      const capacity = MAX_RENDER_WRITE_CHARS - this.buffer.length;
      if (capacity === 0) {
        this.flush();
        continue;
      }
      let end = Math.min(value.length, offset + capacity);
      if (end < value.length && value.charCodeAt(end - 1) >= 55296 && value.charCodeAt(end - 1) <= 56319 && value.charCodeAt(end) >= 56320 && value.charCodeAt(end) <= 57343) {
        end--;
      }
      if (end === offset) {
        this.flush();
        continue;
      }
      this.buffer += value.slice(offset, end);
      offset = end;
      if (this.buffer.length === MAX_RENDER_WRITE_CHARS) {
        this.flush();
      }
    }
  }
  /** Write the current chunk, if any, and retain only its character count for debug output. */
  flush() {
    if (!this.buffer)
      return;
    this.write(this.buffer);
    this.writtenChars += this.buffer.length;
    this.buffer = "";
  }
  get length() {
    return this.writtenChars + this.buffer.length;
  }
};
function parseKittyImageHeader(line) {
  const sequenceStart = line.indexOf(KITTY_SEQUENCE_PREFIX);
  if (sequenceStart === -1)
    return void 0;
  const paramsStart = sequenceStart + KITTY_SEQUENCE_PREFIX.length;
  const paramsEnd = line.indexOf(";", paramsStart);
  if (paramsEnd === -1)
    return void 0;
  const ids = [];
  let rows = 1;
  for (const param of line.slice(paramsStart, paramsEnd).split(",")) {
    const [key, value] = param.split("=", 2);
    if (value === void 0)
      continue;
    const numberValue = Number(value);
    if (!Number.isInteger(numberValue) || numberValue <= 0 || numberValue > 4294967295)
      continue;
    if (key === "i")
      ids.push(numberValue);
    else if (key === "r")
      rows = numberValue;
  }
  return { ids, rows };
}
__name(parseKittyImageHeader, "parseKittyImageHeader");
function extractKittyImageIds(line) {
  return parseKittyImageHeader(line)?.ids ?? [];
}
__name(extractKittyImageIds, "extractKittyImageIds");
function extractKittyImageRows(line) {
  return parseKittyImageHeader(line)?.rows ?? 1;
}
__name(extractKittyImageRows, "extractKittyImageRows");
function isTermuxSession() {
  return Boolean(process.env.TERMUX_VERSION);
}
__name(isTermuxSession, "isTermuxSession");
var TuiMainScreen = class extends TuiBase2 {
  static {
    __name(this, "TuiMainScreen");
  }
  mode = "regular";
  previousLines = [];
  previousKittyImageIds = /* @__PURE__ */ new Set();
  previousWidth = 0;
  previousHeight = 0;
  cursorRow = 0;
  hardwareCursorRow = 0;
  maxLinesRendered = 0;
  previousViewportTop = 0;
  captureRenderState() {
    return {
      previousLines: [...this.previousLines],
      previousWidth: this.previousWidth,
      previousHeight: this.previousHeight,
      cursorRow: this.cursorRow,
      hardwareCursorRow: this.hardwareCursorRow,
      maxLinesRendered: this.maxLinesRendered,
      previousViewportTop: this.previousViewportTop
    };
  }
  restoreRenderState(state) {
    this.previousLines = state.previousLines.map((line) => isImageLine4(line) ? "" : line);
    this.previousKittyImageIds = /* @__PURE__ */ new Set();
    this.previousWidth = state.previousWidth;
    this.previousHeight = state.previousHeight;
    this.cursorRow = state.cursorRow;
    this.hardwareCursorRow = state.hardwareCursorRow;
    this.maxLinesRendered = state.maxLinesRendered;
    this.previousViewportTop = state.previousViewportTop;
  }
  resetRenderState() {
    this.previousLines = [];
    this.previousWidth = -1;
    this.previousHeight = -1;
    this.cursorRow = 0;
    this.hardwareCursorRow = 0;
    this.maxLinesRendered = 0;
    this.previousViewportTop = 0;
  }
  beforeTerminalStop(options) {
    if (options.preserveScreen || this.previousLines.length === 0)
      return;
    this.terminal.write(" ");
    const targetRow = this.previousLines.length;
    const lineDiff = targetRow - this.hardwareCursorRow;
    if (lineDiff > 0)
      this.terminal.write(`\x1B[${lineDiff}B`);
    else if (lineDiff < 0)
      this.terminal.write(`\x1B[${-lineDiff}A`);
    this.terminal.write("\r\n");
  }
  collectKittyImageIds(lines) {
    const ids = /* @__PURE__ */ new Set();
    for (const line of lines) {
      for (const id of extractKittyImageIds(line)) {
        ids.add(id);
      }
    }
    return ids;
  }
  deleteKittyImages(ids) {
    let buffer = "";
    for (const id of ids) {
      buffer += deleteKittyImage2(id);
    }
    return buffer;
  }
  getKittyImageReservedRows(lines, index, maxIndex = lines.length - 1) {
    const rows = extractKittyImageRows(lines[index] ?? "");
    if (rows <= 1)
      return 1;
    const maxRows = Math.min(rows, maxIndex - index + 1, lines.length - index);
    let reservedRows = 1;
    while (reservedRows < maxRows) {
      const line = lines[index + reservedRows] ?? "";
      if (isImageLine4(line) || visibleWidth14(line) > 0)
        break;
      reservedRows++;
    }
    return reservedRows;
  }
  expandChangedRangeForKittyImages(firstChanged, lastChanged, newLines) {
    let expandedFirstChanged = firstChanged;
    let expandedLastChanged = lastChanged;
    const expandForLines = /* @__PURE__ */ __name((lines) => {
      for (let i = 0; i < lines.length; i++) {
        if (extractKittyImageIds(lines[i]).length === 0)
          continue;
        const blockEnd = i + this.getKittyImageReservedRows(lines, i) - 1;
        if (i >= firstChanged || i <= lastChanged && blockEnd >= firstChanged) {
          expandedFirstChanged = Math.min(expandedFirstChanged, i);
          expandedLastChanged = Math.max(expandedLastChanged, blockEnd);
        }
      }
    }, "expandForLines");
    expandForLines(this.previousLines);
    expandForLines(newLines);
    return { firstChanged: expandedFirstChanged, lastChanged: expandedLastChanged };
  }
  deleteChangedKittyImages(firstChanged, lastChanged) {
    if (firstChanged < 0 || lastChanged < firstChanged)
      return "";
    const ids = /* @__PURE__ */ new Set();
    const maxLine = Math.min(lastChanged, this.previousLines.length - 1);
    for (let i = firstChanged; i <= maxLine; i++) {
      for (const id of extractKittyImageIds(this.previousLines[i] ?? "")) {
        ids.add(id);
      }
    }
    return this.deleteKittyImages(ids);
  }
  doRender() {
    if (this.stopped)
      return;
    const width = this.terminal.columns;
    const height = this.terminal.rows;
    const widthChanged = this.previousWidth !== 0 && this.previousWidth !== width;
    const heightChanged = this.previousHeight !== 0 && this.previousHeight !== height;
    const previousBufferLength = this.previousHeight > 0 ? this.previousViewportTop + this.previousHeight : height;
    let prevViewportTop = heightChanged ? Math.max(0, previousBufferLength - height) : this.previousViewportTop;
    let viewportTop = prevViewportTop;
    let hardwareCursorRow = this.hardwareCursorRow;
    const computeLineDiff = /* @__PURE__ */ __name((targetRow) => {
      const currentScreenRow = hardwareCursorRow - prevViewportTop;
      const targetScreenRow = targetRow - viewportTop;
      return targetScreenRow - currentScreenRow;
    }, "computeLineDiff");
    let newLines = this.render(width);
    if (this.hasOverlayEntries) {
      newLines = this.compositeOverlays(newLines, width, height);
    }
    const cursorPos = this.extractCursorPosition(newLines, height);
    newLines = this.applyLineResets(newLines);
    const fullRender = /* @__PURE__ */ __name((clear) => {
      this.fullRedrawCount += 1;
      const output2 = new BoundedTerminalWriter((data) => this.terminal.write(data));
      output2.append("\x1B[?2026h");
      if (clear) {
        output2.append(this.deleteKittyImages(this.previousKittyImageIds));
        output2.append("\x1B[2J\x1B[H\x1B[3J");
      }
      for (let i = 0; i < newLines.length; i++) {
        if (i > 0)
          output2.append("\r\n");
        const line = newLines[i];
        const isImage = isImageLine4(line);
        const imageReservedRows = isImage ? this.getKittyImageReservedRows(newLines, i) : 1;
        if (imageReservedRows > 1 && imageReservedRows <= height) {
          for (let row = 1; row < imageReservedRows; row++) {
            output2.append("\r\n");
          }
          output2.append(`\x1B[${imageReservedRows - 1}A`);
          output2.append(line);
          output2.append(`\x1B[${imageReservedRows - 1}B`);
          i += imageReservedRows - 1;
          continue;
        }
        output2.append(line);
      }
      output2.append("\x1B[?2026l");
      output2.flush();
      this.cursorRow = Math.max(0, newLines.length - 1);
      this.hardwareCursorRow = this.cursorRow;
      if (clear) {
        this.maxLinesRendered = newLines.length;
      } else {
        this.maxLinesRendered = Math.max(this.maxLinesRendered, newLines.length);
      }
      const bufferLength = Math.max(height, newLines.length);
      this.previousViewportTop = Math.max(0, bufferLength - height);
      this.positionHardwareCursor(cursorPos, newLines.length);
      this.previousLines = newLines;
      this.previousKittyImageIds = this.collectKittyImageIds(newLines);
      this.previousWidth = width;
      this.previousHeight = height;
    }, "fullRender");
    const redrawLogDirectory = process.env.PI_TUI_DEBUG_REDRAW === "1" ? this.logDirectory : void 0;
    const logRedraw = /* @__PURE__ */ __name((reason) => {
      if (redrawLogDirectory === void 0)
        return;
      const logPath = path2.join(redrawLogDirectory, "pi-tui-debug.log");
      const msg = `[${(/* @__PURE__ */ new Date()).toISOString()}] fullRender: ${reason} (prev=${this.previousLines.length}, new=${newLines.length}, height=${height})
`;
      fs2.mkdirSync(path2.dirname(logPath), { recursive: true });
      fs2.appendFileSync(logPath, msg);
    }, "logRedraw");
    if (this.previousLines.length === 0 && !widthChanged && !heightChanged) {
      logRedraw("first render");
      fullRender(false);
      return;
    }
    if (widthChanged) {
      logRedraw(`terminal width changed (${this.previousWidth} -> ${width})`);
      fullRender(true);
      return;
    }
    if (heightChanged && !isTermuxSession()) {
      logRedraw(`terminal height changed (${this.previousHeight} -> ${height})`);
      fullRender(true);
      return;
    }
    if (this.getClearOnShrink() && newLines.length < this.maxLinesRendered && !this.hasOverlayEntries) {
      logRedraw(`clearOnShrink (maxLinesRendered=${this.maxLinesRendered})`);
      fullRender(true);
      return;
    }
    let firstChanged = -1;
    let lastChanged = -1;
    const maxLines = Math.max(newLines.length, this.previousLines.length);
    for (let i = 0; i < maxLines; i++) {
      const oldLine = i < this.previousLines.length ? this.previousLines[i] : "";
      const newLine = i < newLines.length ? newLines[i] : "";
      if (oldLine !== newLine) {
        if (firstChanged === -1) {
          firstChanged = i;
        }
        lastChanged = i;
      }
    }
    const appendedLines = newLines.length > this.previousLines.length;
    if (appendedLines) {
      if (firstChanged === -1) {
        firstChanged = this.previousLines.length;
      }
      lastChanged = newLines.length - 1;
    }
    if (firstChanged !== -1) {
      const expandedRange = this.expandChangedRangeForKittyImages(firstChanged, lastChanged, newLines);
      firstChanged = expandedRange.firstChanged;
      lastChanged = expandedRange.lastChanged;
    }
    const appendStart = appendedLines && firstChanged === this.previousLines.length && firstChanged > 0;
    if (firstChanged === -1) {
      this.positionHardwareCursor(cursorPos, newLines.length);
      this.previousViewportTop = prevViewportTop;
      this.previousHeight = height;
      return;
    }
    if (firstChanged >= newLines.length) {
      if (this.previousLines.length > newLines.length) {
        const output2 = new BoundedTerminalWriter((data) => this.terminal.write(data));
        output2.append("\x1B[?2026h");
        output2.append(this.deleteChangedKittyImages(firstChanged, lastChanged));
        const targetRow = Math.max(0, newLines.length - 1);
        if (targetRow < prevViewportTop) {
          logRedraw(`deleted lines moved viewport up (${targetRow} < ${prevViewportTop})`);
          fullRender(true);
          return;
        }
        const lineDiff2 = computeLineDiff(targetRow);
        if (lineDiff2 > 0)
          output2.append(`\x1B[${lineDiff2}B`);
        else if (lineDiff2 < 0)
          output2.append(`\x1B[${-lineDiff2}A`);
        output2.append("\r");
        const extraLines = this.previousLines.length - newLines.length;
        if (extraLines > height) {
          logRedraw(`extraLines > height (${extraLines} > ${height})`);
          fullRender(true);
          return;
        }
        const clearStartOffset = newLines.length === 0 ? 0 : 1;
        if (extraLines > 0 && clearStartOffset > 0) {
          output2.append(`\x1B[${clearStartOffset}B`);
        }
        for (let i = 0; i < extraLines; i++) {
          output2.append("\r\x1B[2K");
          if (i < extraLines - 1)
            output2.append("\x1B[1B");
        }
        const moveBack = Math.max(0, extraLines - 1 + clearStartOffset);
        if (moveBack > 0) {
          output2.append(`\x1B[${moveBack}A`);
        }
        output2.append("\x1B[?2026l");
        output2.flush();
        this.cursorRow = targetRow;
        this.hardwareCursorRow = targetRow;
      }
      this.positionHardwareCursor(cursorPos, newLines.length);
      this.previousLines = newLines;
      this.previousKittyImageIds = this.collectKittyImageIds(newLines);
      this.previousWidth = width;
      this.previousHeight = height;
      this.previousViewportTop = prevViewportTop;
      return;
    }
    if (firstChanged < prevViewportTop) {
      logRedraw(`firstChanged < viewportTop (${firstChanged} < ${prevViewportTop})`);
      fullRender(true);
      return;
    }
    const output = new BoundedTerminalWriter((data) => this.terminal.write(data));
    output.append("\x1B[?2026h");
    output.append(this.deleteChangedKittyImages(firstChanged, lastChanged));
    const prevViewportBottom = prevViewportTop + height - 1;
    const moveTargetRow = appendStart ? firstChanged - 1 : firstChanged;
    if (moveTargetRow > prevViewportBottom) {
      const currentScreenRow = Math.max(0, Math.min(height - 1, hardwareCursorRow - prevViewportTop));
      const moveToBottom = height - 1 - currentScreenRow;
      if (moveToBottom > 0) {
        output.append(`\x1B[${moveToBottom}B`);
      }
      const scroll = moveTargetRow - prevViewportBottom;
      output.append("\r\n".repeat(scroll));
      prevViewportTop += scroll;
      viewportTop += scroll;
      hardwareCursorRow = moveTargetRow;
    }
    const lineDiff = computeLineDiff(moveTargetRow);
    if (lineDiff > 0) {
      output.append(`\x1B[${lineDiff}B`);
    } else if (lineDiff < 0) {
      output.append(`\x1B[${-lineDiff}A`);
    }
    output.append(appendStart ? "\r\n" : "\r");
    const renderEnd = Math.min(lastChanged, newLines.length - 1);
    for (let i = firstChanged; i <= renderEnd; i++) {
      if (i > firstChanged)
        output.append("\r\n");
      const line = newLines[i];
      const isImage = isImageLine4(line);
      const imageReservedRows = isImage ? this.getKittyImageReservedRows(newLines, i, renderEnd) : 1;
      if (imageReservedRows > 1) {
        const imageStartScreenRow = i - viewportTop;
        if (imageStartScreenRow < 0 || imageStartScreenRow + imageReservedRows > height) {
          logRedraw(`kitty image pre-clear would scroll (${imageStartScreenRow} + ${imageReservedRows} > ${height})`);
          fullRender(true);
          return;
        }
        output.append("\x1B[2K");
        for (let row = 1; row < imageReservedRows; row++) {
          output.append("\r\n\x1B[2K");
        }
        output.append(`\x1B[${imageReservedRows - 1}A`);
        output.append(line);
        output.append(`\x1B[${imageReservedRows - 1}B`);
        i += imageReservedRows - 1;
        continue;
      }
      output.append("\x1B[2K");
      if (!isImage && visibleWidth14(line) > width) {
        const crashLogPath = path2.join(this.logDirectory ?? os.tmpdir(), "pi-tui-crash.log");
        const crashData = [
          `Crash at ${(/* @__PURE__ */ new Date()).toISOString()}`,
          `Terminal width: ${width}`,
          `Line ${i} visible width: ${visibleWidth14(line)}`,
          "",
          "=== All rendered lines ===",
          ...newLines.map((l, idx) => `[${idx}] (w=${visibleWidth14(l)}) ${l}`),
          ""
        ].join("\n");
        fs2.mkdirSync(path2.dirname(crashLogPath), { recursive: true });
        fs2.writeFileSync(crashLogPath, crashData);
        this.stop();
        const errorMsg = [
          `Rendered line ${i} exceeds terminal width (${visibleWidth14(line)} > ${width}).`,
          "",
          "This is likely caused by a custom TUI component not truncating its output.",
          "Use visibleWidth() to measure and truncateToWidth() to truncate lines.",
          "",
          `Debug log written to: ${crashLogPath}`
        ].join("\n");
        throw new Error(errorMsg);
      }
      output.append(line);
    }
    let finalCursorRow = renderEnd;
    if (this.previousLines.length > newLines.length) {
      if (renderEnd < newLines.length - 1) {
        const moveDown = newLines.length - 1 - renderEnd;
        output.append(`\x1B[${moveDown}B`);
        finalCursorRow = newLines.length - 1;
      }
      const extraLines = this.previousLines.length - newLines.length;
      for (let i = newLines.length; i < this.previousLines.length; i++) {
        output.append("\r\n\x1B[2K");
      }
      output.append(`\x1B[${extraLines}A`);
    }
    output.append("\x1B[?2026l");
    if (process.env.PI_TUI_DEBUG === "1") {
      const debugDir = "/tmp/tui";
      fs2.mkdirSync(debugDir, { recursive: true });
      const debugPath = path2.join(debugDir, `render-${Date.now()}-${Math.random().toString(36).slice(2)}.log`);
      const debugData = [
        `firstChanged: ${firstChanged}`,
        `viewportTop: ${viewportTop}`,
        `cursorRow: ${this.cursorRow}`,
        `height: ${height}`,
        `lineDiff: ${lineDiff}`,
        `hardwareCursorRow: ${hardwareCursorRow}`,
        `renderEnd: ${renderEnd}`,
        `finalCursorRow: ${finalCursorRow}`,
        `cursorPos: ${JSON.stringify(cursorPos)}`,
        `newLines.length: ${newLines.length}`,
        `previousLines.length: ${this.previousLines.length}`,
        "",
        "=== newLines ===",
        JSON.stringify(newLines, null, 2),
        "",
        "=== previousLines ===",
        JSON.stringify(this.previousLines, null, 2),
        "",
        "=== buffer ===",
        `[${output.length} chars written in bounded chunks]`
      ].join("\n");
      fs2.writeFileSync(debugPath, debugData);
    }
    output.flush();
    this.cursorRow = Math.max(0, newLines.length - 1);
    this.hardwareCursorRow = finalCursorRow;
    this.maxLinesRendered = Math.max(this.maxLinesRendered, newLines.length);
    this.previousViewportTop = Math.max(prevViewportTop, finalCursorRow - height + 1);
    this.positionHardwareCursor(cursorPos, newLines.length);
    this.previousLines = newLines;
    this.previousKittyImageIds = this.collectKittyImageIds(newLines);
    this.previousWidth = width;
    this.previousHeight = height;
  }
  /**
   * Position the hardware cursor for IME candidate window.
   * @param cursorPos The cursor position extracted from rendered output, or null
   * @param totalLines Total number of rendered lines
   */
  positionHardwareCursor(cursorPos, totalLines) {
    if (!cursorPos || totalLines <= 0) {
      this.terminal.hideCursor();
      return;
    }
    const targetRow = Math.max(0, Math.min(cursorPos.row, totalLines - 1));
    const targetCol = Math.max(0, cursorPos.col);
    const rowDelta = targetRow - this.hardwareCursorRow;
    let buffer = "";
    if (rowDelta > 0) {
      buffer += `\x1B[${rowDelta}B`;
    } else if (rowDelta < 0) {
      buffer += `\x1B[${-rowDelta}A`;
    }
    buffer += `\x1B[${targetCol + 1}G`;
    if (buffer) {
      this.terminal.write(buffer);
    }
    this.hardwareCursorRow = targetRow;
    if (this.getShowHardwareCursor()) {
      this.terminal.showCursor();
    } else {
      this.terminal.hideCursor();
    }
  }
};

// pi-dist/pi-tui/index.js
import { getOsc8LinkAtColumn as getOsc8LinkAtColumn2, sliceByColumn as sliceByColumn5, stripTerminalSequences as stripTerminalSequences3, truncateToWidth as truncateToWidth9, visibleWidth as visibleWidth15, wrapTextWithAnsi as wrapTextWithAnsi4 } from "../utils.js";
export {
  Box,
  CURSOR_MARKER5 as CURSOR_MARKER,
  CancellableLoader,
  CombinedAutocompleteProvider,
  Container4 as Container,
  Editor,
  HStack,
  Image,
  Input,
  Key,
  KeybindingsManager,
  Loader,
  Markdown,
  Marked2 as Marked,
  MouseRegion,
  ProcessTerminal,
  ScrollView,
  SelectList,
  SettingsList,
  Spacer,
  StdinBuffer,
  TUI_KEYBINDINGS,
  Text,
  TruncatedText,
  TuiAltScreen,
  TuiMainScreen,
  VStack,
  allocateImageId2 as allocateImageId,
  backgroundAnsi,
  calculateImageRows,
  colorToHex,
  colorToOkhsl,
  colorToOklch,
  colorToRgb,
  compositeTuiLine4 as compositeTuiLine,
  decodeKittyPrintable2 as decodeKittyPrintable,
  deleteAllKittyImages2 as deleteAllKittyImages,
  deleteKittyImage3 as deleteKittyImage,
  detectCapabilities,
  encodeITerm2,
  encodeKitty,
  foregroundAnsi,
  fuzzyFilter,
  fuzzyMatch,
  getCapabilities4 as getCapabilities,
  getCellDimensions2 as getCellDimensions,
  getGifDimensions,
  getImageDimensions2 as getImageDimensions,
  getJpegDimensions,
  getKeybindings8 as getKeybindings,
  getNativeClipboard,
  getOsc8LinkAtColumn2 as getOsc8LinkAtColumn,
  getPngDimensions,
  getTerminalColorMode,
  getWebpDimensions,
  hyperlink2 as hyperlink,
  imageFallback2 as imageFallback,
  indexedColor,
  isAppleTerminalSession,
  isFocusable,
  isKeyRelease2 as isKeyRelease,
  isKeyRepeat,
  isKittyProtocolActive,
  isViewportTUI,
  matchesKey2 as matchesKey,
  mixColors,
  okhslColor,
  oklabToOkhslLightness,
  oklchColor,
  parseColor,
  parseKey,
  parseTerminalColorSchemeReport,
  renderImage2 as renderImage,
  renderLatex,
  resetCapabilitiesCache,
  rgbColor,
  setCapabilities2 as setCapabilities,
  setCapabilityOverrides,
  setCellDimensions,
  setKeybindings,
  setKittyProtocolActive2 as setKittyProtocolActive,
  sliceByColumn5 as sliceByColumn,
  stripTerminalSequences3 as stripTerminalSequences,
  styleText,
  styleTextWithAnsi,
  truncateToWidth9 as truncateToWidth,
  visibleWidth15 as visibleWidth,
  wrapTextWithAnsi4 as wrapTextWithAnsi
};
