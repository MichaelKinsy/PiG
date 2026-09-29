import { existsSync, readdirSync, readFileSync, statSync } from "fs";
import { basename, dirname, join, resolve, sep } from "path";
import { CONFIG_DIR_NAME } from "../config.js";
import { parseFrontmatter } from "../utils/frontmatter.js";
import { resolvePath } from "../utils/paths.js";
import { createSyntheticSourceInfo } from "./source-info.js";
/**
 * Parse command arguments respecting quoted strings (bash-style)
 * Returns array of arguments
 */
export function parseCommandArgs(argsString) {
    const args = [];
    let current = "";
    let inQuote = null;
    for (let i = 0; i < argsString.length; i++) {
        const char = argsString[i];
        if (inQuote) {
            if (char === inQuote) {
                inQuote = null;
            }
            else {
                current += char;
            }
        }
        else if (char === '"' || char === "'") {
            inQuote = char;
        }
        else if (/\s/.test(char)) {
            if (current) {
                args.push(current);
                current = "";
            }
        }
        else {
            current += char;
        }
    }
    if (current) {
        args.push(current);
    }
    return args;
}
/**
 * Substitute argument placeholders in template content
 * Supports:
 * - $1, $2, ... for positional args
 * - $@ and $ARGUMENTS for all args
 * - ${N:-default} for positional arg N with default when missing/empty
 * - ${@:-default} and ${ARGUMENTS:-default} for all args with a default when empty
 * - ${@:N} for args from Nth onwards (bash-style slicing)
 * - ${@:N:L} for L args starting from Nth
 *
 * Note: Replacement happens on the template string only. Argument and default values
 * containing patterns like $1, $@, or $ARGUMENTS are NOT recursively substituted.
 */
export function substituteArgs(content, args) {
    const allArgs = args.join(" ");
    return content.replace(/\$\{(\d+|ARGUMENTS|@):-([^}]*)\}|\$\{@:(\d+)(?::(\d+))?\}|\$(ARGUMENTS|@|\d+)/g, (_match, defaultTarget, defaultValue, sliceStart, sliceLength, simple) => {
        if (defaultTarget) {
            const value = defaultTarget === "@" || defaultTarget === "ARGUMENTS" ? allArgs : args[parseInt(defaultTarget, 10) - 1];
            return value ? value : defaultValue;
        }
        if (sliceStart) {
            let start = parseInt(sliceStart, 10) - 1; // Convert to 0-indexed (user provides 1-indexed)
            // Treat 0 as 1 (bash convention: args start at 1)
            if (start < 0)
                start = 0;
            if (sliceLength) {
                const length = parseInt(sliceLength, 10);
                return args.slice(start, start + length).join(" ");
            }
            return args.slice(start).join(" ");
        }
        if (simple === "ARGUMENTS" || simple === "@") {
            return allArgs;
        }
        const index = parseInt(simple, 10) - 1;
        return args[index] ?? "";
    });
}
function loadTemplateFromFile(filePath, sourceInfo) {
    const diagnostics = [];
    let rawContent;
    try {
        rawContent = readFileSync(filePath, "utf-8");
    }
    catch (error) {
        const message = error instanceof Error ? error.message : "failed to read prompt template file";
        diagnostics.push({ type: "warning", message, path: filePath });
        return { template: null, diagnostics };
    }
    let frontmatter;
    let body;
    try {
        ({ frontmatter, body } = parseFrontmatter(rawContent));
    }
    catch (error) {
        const message = error instanceof Error ? error.message : "failed to parse prompt template file";
        diagnostics.push({ type: "warning", message, path: filePath });
        return { template: null, diagnostics };
    }
    const name = basename(filePath).replace(/\.md$/, "");
    // Get description from frontmatter or first non-empty line
    let description = typeof frontmatter.description === "string" ? frontmatter.description : "";
    if (!description) {
        const firstLine = body.split("\n").find((line) => line.trim());
        if (firstLine) {
            // Truncate if too long
            description = firstLine.slice(0, 60);
            if (firstLine.length > 60)
                description += "...";
        }
    }
    const argumentHint = typeof frontmatter["argument-hint"] === "string" ? frontmatter["argument-hint"] : undefined;
    return {
        template: {
            name,
            description,
            ...(argumentHint && { argumentHint }),
            content: body,
            sourceInfo,
            filePath,
        },
        diagnostics,
    };
}
/**
 * Scan a directory for .md files (non-recursive) and load them as prompt templates.
 */
function loadTemplatesFromDir(dir, getSourceInfo) {
    const templates = [];
    const diagnostics = [];
    if (!existsSync(dir)) {
        return { templates, diagnostics };
    }
    try {
        const entries = readdirSync(dir, { withFileTypes: true });
        for (const entry of entries) {
            const fullPath = join(dir, entry.name);
            // For symlinks, check if they point to a file
            let isFile = entry.isFile();
            if (entry.isSymbolicLink()) {
                try {
                    const stats = statSync(fullPath);
                    isFile = stats.isFile();
                }
                catch {
                    // Broken symlink, skip it
                    continue;
                }
            }
            if (isFile && entry.name.endsWith(".md")) {
                const result = loadTemplateFromFile(fullPath, getSourceInfo(fullPath));
                if (result.template) {
                    templates.push(result.template);
                }
                diagnostics.push(...result.diagnostics);
            }
        }
    }
    catch {
        return { templates, diagnostics };
    }
    return { templates, diagnostics };
}
/**
 * Load all prompt templates from:
 * 1. Global: agentDir/prompts/
 * 2. Project: cwd/{CONFIG_DIR_NAME}/prompts/
 * 3. Explicit prompt paths
 */
export function loadPromptTemplates(options) {
    const resolvedCwd = resolvePath(options.cwd);
    const resolvedAgentDir = resolvePath(options.agentDir);
    const promptPaths = options.promptPaths;
    const includeDefaults = options.includeDefaults;
    const templates = [];
    const diagnostics = [];
    const addResult = (result) => {
        templates.push(...result.templates);
        diagnostics.push(...result.diagnostics);
    };
    const globalPromptsDir = join(resolvedAgentDir, "prompts");
    const projectPromptsDir = resolve(resolvedCwd, CONFIG_DIR_NAME, "prompts");
    const isUnderPath = (target, root) => {
        const normalizedRoot = resolve(root);
        if (target === normalizedRoot) {
            return true;
        }
        const prefix = normalizedRoot.endsWith(sep) ? normalizedRoot : `${normalizedRoot}${sep}`;
        return target.startsWith(prefix);
    };
    const getSourceInfo = (resolvedPath) => {
        if (isUnderPath(resolvedPath, globalPromptsDir)) {
            return createSyntheticSourceInfo(resolvedPath, {
                source: "local",
                scope: "user",
                baseDir: globalPromptsDir,
            });
        }
        if (isUnderPath(resolvedPath, projectPromptsDir)) {
            return createSyntheticSourceInfo(resolvedPath, {
                source: "local",
                scope: "project",
                baseDir: projectPromptsDir,
            });
        }
        return createSyntheticSourceInfo(resolvedPath, {
            source: "local",
            baseDir: statSync(resolvedPath).isDirectory() ? resolvedPath : dirname(resolvedPath),
        });
    };
    if (includeDefaults) {
        addResult(loadTemplatesFromDir(globalPromptsDir, getSourceInfo));
        addResult(loadTemplatesFromDir(projectPromptsDir, getSourceInfo));
    }
    // 3. Load explicit prompt paths
    for (const rawPath of promptPaths) {
        const resolvedPath = resolvePath(rawPath, resolvedCwd, { trim: true });
        if (!existsSync(resolvedPath)) {
            continue;
        }
        try {
            const stats = statSync(resolvedPath);
            if (stats.isDirectory()) {
                addResult(loadTemplatesFromDir(resolvedPath, getSourceInfo));
            }
            else if (stats.isFile() && resolvedPath.endsWith(".md")) {
                const result = loadTemplateFromFile(resolvedPath, getSourceInfo(resolvedPath));
                if (result.template) {
                    templates.push(result.template);
                }
                diagnostics.push(...result.diagnostics);
            }
        }
        catch (error) {
            const message = error instanceof Error ? error.message : "failed to read prompt template path";
            diagnostics.push({ type: "warning", message, path: resolvedPath });
        }
    }
    return { templates, diagnostics };
}
/**
 * Expand a prompt template if it matches a template name.
 * Returns the expanded content or the original text if not a template.
 */
export function expandPromptTemplate(text, templates) {
    if (!text.startsWith("/"))
        return text;
    const match = text.match(/^\/([^\s]+)(?:\s+([\s\S]*))?$/);
    if (!match)
        return text;
    const templateName = match[1];
    const argsString = match[2] ?? "";
    const template = templates.find((t) => t.name === templateName);
    if (template) {
        const args = parseCommandArgs(argsString);
        return substituteArgs(template.content, args);
    }
    return text;
}
//# sourceMappingURL=prompt-templates.js.map