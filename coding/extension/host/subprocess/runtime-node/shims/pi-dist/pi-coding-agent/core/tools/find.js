import { createInterface } from "node:readline";
import { spawn } from "child_process";
import path from "path";
import { Type } from "../../../../typebox.mjs";
import { ensureTool } from "../../utils/tools-manager.js";
import { pathExists, resolveToCwd } from "./path-utils.js";
import { findRenderers } from "./renderers/find.js";
import { wrapToolDefinition } from "./tool-definition-wrapper.js";
import { DEFAULT_MAX_BYTES, formatSize, truncateHead } from "./truncate.js";
/** Relativize a find result against the search root and normalize it to posix separators. */
export function relativizeFindResultPath(resultPath, searchPath, pathModule = path) {
    const hadTrailingSeparator = resultPath.endsWith(pathModule.sep) || (pathModule.sep === "\\" && resultPath.endsWith("/"));
    const relativePath = pathModule.isAbsolute(resultPath) ? pathModule.relative(searchPath, resultPath) : resultPath;
    const posixPath = relativePath.split(pathModule.sep).join("/");
    return hadTrailingSeparator && !posixPath.endsWith("/") ? `${posixPath}/` : posixPath;
}
const findSchema = Type.Object({
    pattern: Type.String({
        description: "Glob pattern to match files, e.g. '*.ts', '**/*.json', or 'src/**/*.spec.ts'",
    }),
    path: Type.Optional(Type.String({ description: "Directory to search in (default: current directory)" })),
    limit: Type.Optional(Type.Number({ description: "Maximum number of results (default: 1000)" })),
});
export const findToolSystemPromptContribution = {
    snippet: "Find files by glob pattern (respects .gitignore)",
    guidelines: [],
};
const DEFAULT_LIMIT = 1000;
const defaultFindOperations = {
    exists: pathExists,
    // This is a placeholder. Actual fd execution happens in execute() when no custom glob is provided.
    glob: () => [],
};
export function createFindToolDefinition(cwd, options) {
    const customOps = options?.operations;
    return {
        name: "find",
        label: "find",
        description: `Search for files by glob pattern. Returns matching file paths relative to the search directory. Respects .gitignore. Output is truncated to ${DEFAULT_LIMIT} results or ${DEFAULT_MAX_BYTES / 1024}KB (whichever is hit first).`,
        promptSnippet: findToolSystemPromptContribution.snippet,
        parameters: findSchema,
        async execute(_toolCallId, { pattern, path: searchDir, limit }, signal, _onUpdate, ctx) {
            return new Promise((resolve, reject) => {
                if (signal?.aborted) {
                    reject(new Error("Operation aborted"));
                    return;
                }
                let settled = false;
                let stopChild;
                const settle = (fn) => {
                    if (settled)
                        return;
                    settled = true;
                    signal?.removeEventListener("abort", onAbort);
                    stopChild = undefined;
                    fn();
                };
                const onAbort = () => {
                    stopChild?.();
                    settle(() => reject(new Error("Operation aborted")));
                };
                signal?.addEventListener("abort", onAbort, { once: true });
                (async () => {
                    try {
                        const searchPath = resolveToCwd(searchDir || ".", ctx?.cwd || cwd);
                        const effectiveLimit = limit ?? DEFAULT_LIMIT;
                        const ops = customOps ?? defaultFindOperations;
                        // If custom operations provide glob(), use that instead of fd.
                        if (customOps?.glob) {
                            if (!(await ops.exists(searchPath))) {
                                settle(() => reject(new Error(`Path not found: ${searchPath}`)));
                                return;
                            }
                            if (signal?.aborted) {
                                settle(() => reject(new Error("Operation aborted")));
                                return;
                            }
                            const results = await ops.glob(pattern, searchPath, {
                                ignore: ["**/node_modules/**", "**/.git/**"],
                                limit: effectiveLimit,
                            });
                            if (signal?.aborted) {
                                settle(() => reject(new Error("Operation aborted")));
                                return;
                            }
                            if (results.length === 0) {
                                settle(() => resolve({
                                    content: [{ type: "text", text: "No files found matching pattern" }],
                                    details: undefined,
                                }));
                                return;
                            }
                            // Relativize paths against the search root for stable output.
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
                                resultOutput += `\n\n[${notices.join(". ")}]`;
                            }
                            settle(() => resolve({
                                content: [{ type: "text", text: resultOutput }],
                                details: Object.keys(details).length > 0 ? details : undefined,
                            }));
                            return;
                        }
                        // Default implementation uses fd.
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
                        // fd normally ignores .gitignore outside git repos, so keep --no-require-git
                        // there. Inside repos, use fd's default git-aware behavior so parent
                        // .gitignore rules stop at nested repo boundaries:
                        // https://github.com/earendil-works/pi/issues/5960
                        let insideGitRepo = false;
                        for (let current = searchPath;;) {
                            if (await pathExists(path.join(current, ".git"))) {
                                insideGitRepo = true;
                                break;
                            }
                            const parent = path.dirname(current);
                            if (parent === current)
                                break;
                            current = parent;
                        }
                        if (!insideGitRepo)
                            args.push("--no-require-git");
                        args.push("--max-results", String(effectiveLimit));
                        // fd --glob matches against the basename unless --full-path is set; in --full-path
                        // mode it matches against the absolute candidate path, so a path-containing
                        // pattern like 'src/**/*.spec.ts' needs a leading '**/' to match anything.
                        let effectivePattern = pattern;
                        if (pattern.includes("/")) {
                            args.push("--full-path");
                            if (!pattern.startsWith("/") && !pattern.startsWith("**/") && pattern !== "**") {
                                effectivePattern = `**/${pattern}`;
                            }
                            // fd matches full paths using native separators on Windows.
                            if (process.platform === "win32")
                                effectivePattern = effectivePattern.replaceAll("/", String.raw `[/\\]`);
                        }
                        args.push("--", effectivePattern, searchPath);
                        const child = spawn(fdPath, args, { stdio: ["ignore", "pipe", "pipe"] });
                        const rl = createInterface({ input: child.stdout });
                        let stderr = "";
                        const lines = [];
                        stopChild = () => {
                            if (!child.killed) {
                                child.kill();
                            }
                        };
                        const cleanup = () => {
                            rl.close();
                        };
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
                                settle(() => resolve({
                                    content: [{ type: "text", text: "No files found matching pattern" }],
                                    details: undefined,
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
                                resultOutput += `\n\n[${notices.join(". ")}]`;
                            }
                            settle(() => resolve({
                                content: [{ type: "text", text: resultOutput }],
                                details: Object.keys(details).length > 0 ? details : undefined,
                            }));
                        });
                    }
                    catch (e) {
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
        ...findRenderers,
    };
}
export function createFindTool(cwd, options) {
    return wrapToolDefinition(createFindToolDefinition(cwd, options));
}
//# sourceMappingURL=find.js.map