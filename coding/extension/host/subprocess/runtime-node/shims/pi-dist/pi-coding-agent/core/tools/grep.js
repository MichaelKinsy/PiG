import { readFile as fsReadFile, stat as fsStat } from "node:fs/promises";
import { createInterface } from "node:readline";
import { spawn } from "child_process";
import path from "path";
import { Type } from "../../../../typebox.mjs";
import { ensureTool } from "../../utils/tools-manager.js";
import { resolveToCwd } from "./path-utils.js";
import { grepRenderers } from "./renderers/grep.js";
import { wrapToolDefinition } from "./tool-definition-wrapper.js";
import { DEFAULT_MAX_BYTES, formatSize, GREP_MAX_LINE_LENGTH, truncateHead, truncateLine, } from "./truncate.js";
const grepSchema = Type.Object({
    pattern: Type.String({ description: "Search pattern (regex or literal string)" }),
    path: Type.Optional(Type.String({ description: "Directory or file to search (default: current directory)" })),
    glob: Type.Optional(Type.String({ description: "Filter files by glob pattern, e.g. '*.ts' or '**/*.spec.ts'" })),
    ignoreCase: Type.Optional(Type.Boolean({ description: "Case-insensitive search (default: false)" })),
    literal: Type.Optional(Type.Boolean({ description: "Treat pattern as literal string instead of regex (default: false)" })),
    context: Type.Optional(Type.Number({ description: "Number of lines to show before and after each match (default: 0)" })),
    limit: Type.Optional(Type.Number({ description: "Maximum number of matches to return (default: 100)" })),
});
export const grepToolSystemPromptContribution = {
    snippet: "Search file contents for patterns (respects .gitignore)",
    guidelines: [],
};
const DEFAULT_LIMIT = 100;
const defaultGrepOperations = {
    isDirectory: async (p) => (await fsStat(p)).isDirectory(),
    readFile: (p) => fsReadFile(p, "utf-8"),
};
export function createGrepToolDefinition(cwd, options) {
    const customOps = options?.operations;
    return {
        name: "grep",
        label: "grep",
        description: `Search file contents for a pattern. Returns matching lines with file paths and line numbers. Respects .gitignore. Output is truncated to ${DEFAULT_LIMIT} matches or ${DEFAULT_MAX_BYTES / 1024}KB (whichever is hit first). Long lines are truncated to ${GREP_MAX_LINE_LENGTH} chars.`,
        promptSnippet: grepToolSystemPromptContribution.snippet,
        parameters: grepSchema,
        async execute(_toolCallId, { pattern, path: searchDir, glob, ignoreCase, literal, context, limit, }, signal, _onUpdate, ctx) {
            return new Promise((resolve, reject) => {
                if (signal?.aborted) {
                    reject(new Error("Operation aborted"));
                    return;
                }
                let settled = false;
                const settle = (fn) => {
                    if (!settled) {
                        settled = true;
                        fn();
                    }
                };
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
                        }
                        catch {
                            settle(() => reject(new Error(`Path not found: ${searchPath}`)));
                            return;
                        }
                        const contextValue = context && context > 0 ? context : 0;
                        const effectiveLimit = Math.max(1, limit ?? DEFAULT_LIMIT);
                        const formatPath = (filePath) => {
                            if (isDirectory) {
                                const relative = path.relative(searchPath, filePath);
                                if (relative && !relative.startsWith("..")) {
                                    return relative.replace(/\\/g, "/");
                                }
                            }
                            return path.basename(filePath);
                        };
                        const fileCache = new Map();
                        const getFileLines = async (filePath) => {
                            let lines = fileCache.get(filePath);
                            if (!lines) {
                                try {
                                    const content = await ops.readFile(filePath);
                                    lines = content.replace(/\r\n/g, "\n").replace(/\r/g, "\n").split("\n");
                                }
                                catch {
                                    lines = [];
                                }
                                fileCache.set(filePath, lines);
                            }
                            return lines;
                        };
                        const args = ["--json", "--line-number", "--color=never", "--hidden"];
                        if (ignoreCase)
                            args.push("--ignore-case");
                        if (literal)
                            args.push("--fixed-strings");
                        if (glob)
                            args.push("--glob", glob);
                        args.push("--", pattern, searchPath);
                        const child = spawn(rgPath, args, { stdio: ["ignore", "pipe", "pipe"] });
                        const rl = createInterface({ input: child.stdout });
                        let stderr = "";
                        let matchCount = 0;
                        let matchLimitReached = false;
                        let linesTruncated = false;
                        let aborted = false;
                        let killedDueToLimit = false;
                        const outputLines = [];
                        const cleanup = () => {
                            rl.close();
                            signal?.removeEventListener("abort", onAbort);
                        };
                        const stopChild = (dueToLimit = false) => {
                            if (!child.killed) {
                                killedDueToLimit = dueToLimit;
                                child.kill();
                            }
                        };
                        const onAbort = () => {
                            aborted = true;
                            stopChild();
                        };
                        signal?.addEventListener("abort", onAbort, { once: true });
                        child.stderr?.on("data", (chunk) => {
                            stderr += chunk.toString();
                        });
                        const formatBlock = async (filePath, lineNumber) => {
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
                                // Truncate long lines so grep output stays compact.
                                const { text: truncatedText, wasTruncated } = truncateLine(sanitized);
                                if (wasTruncated)
                                    linesTruncated = true;
                                if (isMatchLine)
                                    block.push(`${relativePath}:${current}: ${truncatedText}`);
                                else
                                    block.push(`${relativePath}-${current}- ${truncatedText}`);
                            }
                            return block;
                        };
                        // Collect matches during streaming, then format them after rg exits.
                        const matches = [];
                        rl.on("line", (line) => {
                            if (!line.trim() || matchCount >= effectiveLimit)
                                return;
                            let event;
                            try {
                                event = JSON.parse(line);
                            }
                            catch {
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
                                settle(() => resolve({ content: [{ type: "text", text: "No matches found" }], details: undefined }));
                                return;
                            }
                            // Format matches after streaming finishes so custom readFile() backends can be async.
                            for (const match of matches) {
                                if (contextValue === 0 && match.lineText !== undefined) {
                                    const relativePath = formatPath(match.filePath);
                                    const sanitized = match.lineText
                                        .replace(/\r\n/g, "\n")
                                        .replace(/\r/g, "")
                                        .replace(/\n$/, "");
                                    const { text: truncatedText, wasTruncated } = truncateLine(sanitized);
                                    if (wasTruncated)
                                        linesTruncated = true;
                                    outputLines.push(`${relativePath}:${match.lineNumber}: ${truncatedText}`);
                                }
                                else {
                                    const block = await formatBlock(match.filePath, match.lineNumber);
                                    outputLines.push(...block);
                                }
                            }
                            const rawOutput = outputLines.join("\n");
                            // Apply byte truncation. There is no line limit here because the match limit already capped rows.
                            const truncation = truncateHead(rawOutput, { maxLines: Number.MAX_SAFE_INTEGER });
                            let output = truncation.content;
                            const details = {};
                            // Build actionable notices for truncation and match limits.
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
                                output += `\n\n[${notices.join(". ")}]`;
                            settle(() => resolve({
                                content: [{ type: "text", text: output }],
                                details: Object.keys(details).length > 0 ? details : undefined,
                            }));
                        });
                    }
                    catch (err) {
                        settle(() => reject(err));
                    }
                })();
            });
        },
        ...grepRenderers,
    };
}
export function createGrepTool(cwd, options) {
    return wrapToolDefinition(createGrepToolDefinition(cwd, options));
}
//# sourceMappingURL=grep.js.map