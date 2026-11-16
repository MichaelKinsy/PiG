// Post-release Pi fixes that the vendored Pi JavaScript (shims/pi-dist) and the parity oracle carry ahead of the pin.
//
// PiG pins one Pi build, and the Node extension runtime runs that package's compiled output: the SDK (createAgentSession, the built-in
// tools, the llama.cpp built-in extension), pi-tui components extensions render with, and the HTML export. Go ports of later Pi fixes do
// not change those files, so a Node path would keep the old behavior. Each entry below (a BEGIN/END block named by the Pi commit it
// carries) applies that commit's change to the compiled output with exact before/after strings, and the vendoring step fails when a
// `before` string is missing or ambiguous (a pin change cannot silently drop a backport). This is Pi's own later behavior, not a divergence.
//
// An entry lives only until the pin reaches its commit: `make pin-bump` (automation/pin/carried.py) classifies every entry against the
// target Pi's installed package and deletes the blocks the target already carries (`--classify`); a block the target does not carry must
// still apply, else the bump stops naming the entry and the file. A script with no block is a no-op: the mechanism stays for the next
// release cycle, and the oracle build does not apply or stamp it (oracle.backports_digest is "" then).
//
// Run through vendor-pi-dist.sh after the identity patches and before the SDK and library bundles are compiled.
// The same entries are applied to the installed Pi the parity tests compare with (`--packages name=dir ...`, from automation/pin/oracle.py), so the oracle is Pi with these fixes.
// `pi` itself runs the package's bundle (dist/bundle, Pi's esbuild output over the compiled output), which no entry can patch as text: the oracle build applies
// the entries to the source build's packages too (`--package-files` names the files, so the build can put them back) and has Pi's own bundler make the bundle again.
// The source mirror (.upstream/current) stays the pinned build, which the sync ledger's delta audit compares with the earlier pin; the
// tests that read the mirror's docs and schemas read them through `--read-mirror <mirror> <path>` (internal/upstreammirror.ReadPicked), the
// pinned file plus the entries of packageFileBackports and backportPatches that cover it, and the TypeScript sources a mirrorSource entry covers.
// `--mirror-paths` lists the mirror files a backport covers (internal/upstreammirror.PickedPaths), so a test that reads many mirror files reads only those through `--read-mirror`.
// `--verify-packages` and `--verify-mirror` fail when an installed Pi does not carry each entry once, or a mirror file is not the pinned build's.
// A commit that also moves a dependency a Pi package pins exactly carries that as a `dependency(...)` entry in a block of its own (oracle only; see `dependency` below).
// What a commit's blocks deliberately leave at the pin is said in them, one `// not carried: <file> (<what>): <reason>` line per file. The sync ledger rows of a followed
// commit answer for the oracle too (automation/pin/carried.py oracle_ledger_problems, which `pin.py check-oracle` runs).

import { createHash } from "node:crypto";
import { existsSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const note = (sha) => `// Pi ${sha} (post-v1.5.2), backported by automation/gen/pi-post-release-backports.mjs.\n`;

// 49fe0a237 (#11987): the supportsMessageThreads description of pi-ai's compat schema, which the models docs and the models.json schema repeat.
const messageThreadsDefault = ["Default: true for provider anthropic with a baseUrl on api.anthropic.com", "Default: true for a baseUrl on api.anthropic.com"];

const blocks = [];
const patch = (id, key, patches) => blocks.push({ id, key, patches, table: "dist" });
// dependency carries a dependency change of a Pi commit: the Pi package `pkg` (pi-ai) pins `dep.name` exactly, at `dep.from` in the pinned build and at `dep.to` from
// the commit on. `resolved`, `integrity` and `dependencies` are the entry of `dep.to` in Pi's own package-lock.json at the commit (read from the Pi git, never from the
// registry). The oracle build (automation/pin/oracle.py carried_closure) installs the frozen closure with this lock entry in place of the pinned one, so `npm ci`
// verifies the archive against the integrity, and has Pi's bundler read the installed copy. No file of the installed package is rewritten (its package.json keeps the
// pin's range: published ranges follow the pin), and the vendored runtime installs no carried dependency. `--classify` reads the package's package.json (classifyDependency).
const dependency = (id, pkg, dep) => blocks.push({ id, key: `${pkg}/package.json`, table: "dependency", dependency: { id, package: pkg, ...dep } });
const packageFile = (id, key, patches) => blocks.push({ id, key, patches, table: "package" });
// A Pi TypeScript source that tests read from the source mirror and that a backport of its compiled output changes, by path in the Pi
// repository, with the same change as Pi's commit makes to the source. It stands in a block of its commit, so the bump that retires the
// commit's entries retires it with them.
const mirrorSource = (id, path, patches) => blocks.push({ id, key: path, patches, table: "mirror" });

// BEGIN backport bacd45b9c
// bacd45b9c (#11973, #11264): the model scope warnings are diagnostics of the runtime the factory returns, not lines it prints on every session replacement.
patch("bacd45b9c", "pi-coding-agent/main.js", [
    ['import { resolveCliModel, resolveModelScope } from "./core/model-resolver.js";', 'import { resolveCliModel, resolveModelScopeWithDiagnostics } from "./core/model-resolver.js";'],
    [`        const scopedModels = modelPatterns && modelPatterns.length > 0
            ? await resolveModelScope(modelPatterns, modelRuntime, { signal: AbortSignal.timeout(15_000) })
            : [];
`, `        ${note("bacd45b9c").trim()}
        const { scopedModels, diagnostics: modelScopeDiagnostics } = modelPatterns && modelPatterns.length > 0
            ? await resolveModelScopeWithDiagnostics(modelPatterns, modelRuntime, {
                signal: AbortSignal.timeout(15_000),
            })
            : { scopedModels: [], diagnostics: [] };
        // Returned as diagnostics instead of printed: this factory runs again on /new, /resume and /fork,
        // while the interactive TUI draws on the terminal (#11264).
        diagnostics.push(...modelScopeDiagnostics);
`],
]);
// END backport bacd45b9c

// BEGIN backport bacd45b9c
// bacd45b9c: resolveModelScope, which printed each warning with console.warn, has no caller left and is removed.
patch("bacd45b9c", "pi-coding-agent/core/model-resolver.js", [
    [`export async function resolveModelScope(patterns, modelRuntime, options) {
    const { scopedModels, diagnostics } = await resolveModelScopeWithDiagnostics(patterns, modelRuntime, options);
    for (const diagnostic of diagnostics) {
        console.warn(chalk.yellow(\`Warning: \${diagnostic.message}\`));
    }
    return scopedModels;
}
`, ""],
]);
// END backport bacd45b9c

// BEGIN backport b3c2e4a6bef8
// b3c2e4a6bef8 (#12001): the llama.cpp default server port is 9931.
patch("b3c2e4a6bef8", "pi-coding-agent/extensions/llama/provider.js", [
    ['export const DEFAULT_LLAMA_SERVER_URL = "http://127.0.0.1:8080";', 'export const DEFAULT_LLAMA_SERVER_URL = "http://127.0.0.1:9931";'],
]);
// END backport b3c2e4a6bef8

// BEGIN backport 9b88b94e2
// 9b88b94e2 (#12093, #12064): a provider error reading "Connection closed." is retried (the classifier that pi-ai's SDK, the coding agent and the oracle test of ai.IsRetryableAssistantError run).
patch("9b88b94e2", "pi-ai/utils/retry.js", [
    [`    "connection.?lost",
    "other side closed",
`, `    "connection.?lost",
    ${note("9b88b94e2").trim()}
    // Gateway mid-stream drop relayed as an Anthropic-style \`api_error\` event,
    // e.g. {"type":"error","error":{"type":"api_error","message":"Connection closed."}} (#12064).
    "connection.?closed",
    "other side closed",
`],
]);
// END backport 9b88b94e2

// BEGIN backport 4699b5b26
// 4699b5b26 (#12136, #12128): a provider error that reports only a Node socket error code ("read ECONNRESET", "write EPIPE", "connect ETIMEDOUT 1.2.3.4:443") or "connection reset by peer" is retried (the classifier that pi-ai's SDK, the coding agent and the oracle test of ai.IsRetryableAssistantError run).
patch("4699b5b26", "pi-ai/utils/retry.js", [
    [`    "getaddrinfo",
    "ENOTFOUND",
`, `    "getaddrinfo",
    ${note("4699b5b26").trim()}
    // Bare Node socket error codes, e.g. "read ECONNRESET", "write EPIPE", or
    // "connect ETIMEDOUT 1.2.3.4:443" when a transport reports only the code (#12128).
    // The codes must not be part of a longer identifier.
    "(?<![a-z0-9_])(?:ECONNRESET|ECONNABORTED|ECONNREFUSED|ETIMEDOUT|EPIPE)(?![a-z0-9_])",
    "connection reset by peer",
    "ENOTFOUND",
`],
]);
// END backport 4699b5b26

// BEGIN backport 49fe0a237
// 49fe0a237 (#11987): Anthropic message threads default by endpoint, not provider id (the description pi-ai's compat schema and the models docs carry).
patch("49fe0a237", "pi-ai/providers/compat-schema.js", [messageThreadsDefault]);
// END backport 49fe0a237

// BEGIN backport 49fe0a237
patch("49fe0a237", "pi-coding-agent/docs/models-configuration.md", [messageThreadsDefault]);
// END backport 49fe0a237

// BEGIN backport be2744d95
// be2744d95 (#12047): a Kitty fullscreen redraw keeps the uploaded images and skips the ED 2 clear.
patch("be2744d95", "pi-tui/tui-alt-screen.js", [
    [`            const clearImages = this.imageProtocol === "kitty" && hadUploadedKittyImages
                ? deleteAllKittyPlacements()
                : this.deleteKittyImages();
            buffer += \`\${clearImages}\\x1b[2J\`;
`,
      `            ${note("be2744d95").trim()}
            if (this.imageProtocol === "kitty" && hadUploadedKittyImages) {
                // Kitty frees image data without placements on ED 2, which would break placement-only reuse.
                // Every row is cleared with EL 2 below, so keep the uploads and skip the screen clear.
                buffer += deleteAllKittyPlacements();
            }
            else {
                buffer += \`\${this.deleteKittyImages()}\\x1b[2J\`;
            }
`],
]);
// END backport be2744d95

// BEGIN backport 5b4d7d3df
// 5b4d7d3df (#12045): hard breaks after an inline Markdown image stay separate rows.
patch("5b4d7d3df", "pi-tui/components/markdown.js", [
    ["        for (const line of this.renderToken(token, contentWidth, nextTokenType)) {\n",
      `${"        " + note("5b4d7d3df")}        // Image lines skip wrapping, so split them here: hard breaks after an image must stay separate rows.
        const tokenLines = this.renderToken(token, contentWidth, nextTokenType).flatMap((line) => isImageLine(line) ? line.split("\\n") : [line]);
        for (const line of tokenLines) {
`],
]);
// END backport 5b4d7d3df

// BEGIN backport 787005a40
// 787005a40 (#12109): an overlay hides the Kitty and iTerm2 images it covers, and they are drawn again once it no longer covers them.
patch("787005a40", "pi-tui/terminal-image.js", [
    ['import { pathToFileURL } from "node:url";\n', `import { pathToFileURL } from "node:url";\n${note("787005a40")}import { visibleWidth } from "./utils.js";\n`],
    [`    return line.includes(KITTY_PREFIX) || line.includes(ITERM2_PREFIX);
}
`, `    return line.includes(KITTY_PREFIX) || line.includes(ITERM2_PREFIX);
}
${note("787005a40")}/**
 * Return the cells an image line draws over. Kitty places the image at the line and extends it
 * downward; iTerm2 lines move the cursor up before drawing, so the image ends at the line.
 */
export function getImageLineExtent(line) {
    const kittyStart = line.indexOf(KITTY_PREFIX);
    if (kittyStart !== -1) {
        const controls = /\\x1b_G([^;]*);/.exec(line)?.[1] ?? "";
        const columnValue = /(?:^|,)c=(\\d+)(?:,|$)/.exec(controls)?.[1];
        const columns = columnValue === undefined
            ? getRegisteredKittyImageMetadataFromControls(controls)?.columns
            : Number.parseInt(columnValue, 10);
        const startCol = visibleWidth(line.slice(0, kittyStart));
        const rows = getKittyImagePlacementRows(line) ?? 1;
        return { firstRow: 0, lastRow: rows - 1, startCol, endCol: startCol + (columns ?? Infinity) };
    }
    const iterm2Start = line.indexOf(ITERM2_PREFIX);
    if (iterm2Start === -1)
        return undefined;
    const before = line.slice(0, iterm2Start);
    const moveUp = /\\x1b\\[(\\d+)A$/.exec(before);
    // Only a plain cell count is a known width; \`auto\`, pixel and percent widths depend on the image.
    const columnValue = /[;=]width=(\\d+)[;:]/.exec(line.slice(iterm2Start))?.[1];
    // visibleWidth counts the cursor-up sequence as text, so measure only what precedes it.
    const startCol = visibleWidth(moveUp ? before.slice(0, moveUp.index) : before);
    return {
        firstRow: moveUp ? -Number.parseInt(moveUp[1], 10) : 0,
        lastRow: 0,
        startCol,
        endCol: startCol + (columnValue === undefined ? Infinity : Number.parseInt(columnValue, 10)),
    };
}
/** Remove Kitty and iTerm2 image sequences, including iTerm2's cursor-up prefix, keeping the text. */
export function stripImageSequences(line) {
    return line.replace(/\\x1b_G[^\\x1b]*\\x1b\\\\/g, "").replace(/(?:\\x1b\\[\\d+A)?\\x1b\\]1337;File=[^\\x07]*\\x07/g, "");
}
`],
]);
// END backport 787005a40

// BEGIN backport 787005a40
patch("787005a40", "pi-tui/tui.js", [
    ['import { getCapabilities, isImageLine, setCellDimensions } from "./terminal-image.js";\n',
      `${note("787005a40")}import { getCapabilities, getImageLineExtent, getMaxKittyImageRows, isImageLine, setCellDimensions, stripImageSequences, } from "./terminal-image.js";\n`],
    ['}\nexport const VIEWPORT_TUI = Symbol.for("@earendil-works/pi-tui/viewport");\n',
      `}\n${note("787005a40")}/**
 * Drop image placements that intersect an overlay rectangle (\`row\` is viewport-relative).
 * Terminals draw images above text, so an overlay cannot paint over them. The renderers delete a
 * placement once its line changes and draw it again when the overlay no longer covers it.
 * Images beside the overlay stay; on the row holding such an image's sequence the overlay is not
 * drawn, because \`compositeTuiLine\` passes image lines through.
 */
function hideImagesUnderOverlay(lines, overlay, viewportStart) {
    if (overlay.height === 0)
        return;
    // Kitty images extend downward, so an image line above the viewport can still reach into it.
    for (let index = Math.max(0, viewportStart - getMaxKittyImageRows()); index < lines.length; index++) {
        const line = lines[index];
        const extent = getImageLineExtent(line);
        if (!extent)
            continue;
        const firstRow = index + extent.firstRow - viewportStart;
        const lastRow = index + extent.lastRow - viewportStart;
        if (firstRow < overlay.row + overlay.height &&
            lastRow >= overlay.row &&
            extent.startCol < overlay.col + overlay.width &&
            extent.endCol > overlay.col) {
            lines[index] = stripImageSequences(line);
        }
    }
}
export const VIEWPORT_TUI = Symbol.for("@earendil-works/pi-tui/viewport");\n`],
    ["        // Composite each overlay\n        for (const { overlayLines, row, col, w } of rendered) {\n",
      `        ${note("787005a40").trim()}
        // Composite each overlay, back to front. Hiding images per overlay also hides images drawn by overlays below it.
        for (const { overlayLines, row, col, w } of rendered) {
            hideImagesUnderOverlay(result, { row, col, width: w, height: overlayLines.length }, viewportStart);
`],
]);
// END backport 787005a40

// BEGIN backport bbfc6c7ce
// bbfc6c7ce (#12044): the find tool ignores a leading ./ in its pattern.
patch("bbfc6c7ce", "pi-coding-agent/core/tools/find.js", [
    ['                        let effectivePattern = process.platform === "win32" ? pattern.replaceAll("\\\\", "/") : pattern;\n',
      `                        let effectivePattern = process.platform === "win32" ? pattern.replaceAll("\\\\", "/") : pattern;
                        ${note("bbfc6c7ce").trim()}
                        // fd's full paths never contain "/./", so "./src/*.ts" would find nothing; drop the "./".
                        effectivePattern = effectivePattern.replace(/^(?:\\.\\/)+/, "");
`],
]);
// END backport bbfc6c7ce

// BEGIN backport 282b01c80
// 282b01c80 (#12116, #12066): a relative path pattern is anchored at the search path, like grep's glob, instead of matching through the folders above it.
patch("282b01c80", "pi-coding-agent/core/tools/find.js", [
    [`        description: "Glob pattern to match files, e.g. '*.ts', '**/*.json', or 'src/**/*.spec.ts'",
`,
      `        description: "Glob pattern relative to the search directory, e.g. 'src/**/*.ts'. Without '/', e.g. '*.ts', it matches file names at any depth",
`],
    [`                        // mode it matches against the absolute candidate path, so a path-containing
                        // pattern like 'src/**/*.spec.ts' needs a leading '**/' to match anything.
`,
      `                        // mode it matches against the absolute candidate path.
`],
    [`                        const isPathPattern = effectivePattern.includes("/");
                        if (isPathPattern &&
                            !effectivePattern.startsWith("/") &&
                            !effectivePattern.startsWith("**/") &&
                            effectivePattern !== "**") {
                            effectivePattern = \`**/\${effectivePattern}\`;
                        }
                        // fd before 10.5.1 never matches --full-path globs on Windows, and workarounds for that
                        // break on fd 10.5.1 (sharkdp/fd#2067). pi cannot rely on either fd version, so on
                        // Windows fd lists every entry and minimatch matches path patterns. The options keep
                        // fd's glob behavior: \`*\` matches dotfiles, \`+(a|b)\` is literal, and an all-lowercase
                        // pattern matches case-insensitively (smart case). A trailing '/**' becomes '/**/*'
                        // because minimatch's 'src/**' also matches 'src' itself and fd's does not.
`,
      `                        const isPathPattern = effectivePattern.includes("/");
                        ${note("282b01c80").trim()}
                        // fd's smart case: an all-lowercase pattern matches case-insensitively.
                        const ignoreCase = effectivePattern === effectivePattern.toLowerCase();
                        // A relative path pattern is anchored at the search path, like grep's glob (#12066):
                        // 'src/*.ts' finds 'src/a.ts' but not 'pkg/src/b.ts'; '**/src/*.ts' finds both.
                        // Absolute patterns are matched as given.
                        const isRelativePathPattern = isPathPattern && !path.isAbsolute(effectivePattern);
                        // fd before 10.5.1 never matches --full-path globs on Windows, and workarounds for that
                        // break on fd 10.5.1 (sharkdp/fd#2067). pi cannot rely on either fd version, so on
                        // Windows fd lists every entry and minimatch matches path patterns against the path
                        // relative to the search path, or the absolute path for absolute patterns. The options
                        // keep fd's glob behavior: \`*\` matches dotfiles, \`+(a|b)\` is literal, and smart case.
                        // A trailing '/**' becomes '/**/*' because minimatch's 'src/**' also matches 'src'
                        // itself and fd's does not.
`],
    [`                                nocase: effectivePattern === effectivePattern.toLowerCase(),
`,
      `                                nocase: ignoreCase,
`],
    [`                            if (isPathPattern)
                                args.push("--full-path");
                            args.push("--", effectivePattern, searchPath);
`,
      `                            if (isPathPattern)
                                args.push("--full-path");
                            if (isRelativePathPattern) {
                                // fd matches the absolute path, so the pattern is prefixed with the glob-escaped
                                // search path. Smart case is decided by the user's pattern, not by the search
                                // path's letters, so it is set explicitly.
                                const escapedRoot = searchPath.replace(/\\/+$/, "").replace(/[\\\\*?[\\]{}]/g, "\\\\$&");
                                effectivePattern = \`\${escapedRoot}/\${effectivePattern}\`;
                                if (ignoreCase)
                                    args.push("--ignore-case");
                            }
                            args.push("--", effectivePattern, searchPath);
`],
    [`                            if (windowsPathMatcher && !windowsPathMatcher.match(line))
                                return;
                            if (resultCount === effectiveLimit) {
                                resultLimitReached = true;
                                // Without --max-results, fd keeps listing after the limit.
                                if (windowsPathMatcher)
                                    stopChild?.();
                                return;
                            }
                            const result = relativizeFindResultPath(line, searchPath);
`,
      `                            const result = relativizeFindResultPath(line, searchPath);
                            if (windowsPathMatcher && !windowsPathMatcher.match(isRelativePathPattern ? result : line))
                                return;
                            if (resultCount === effectiveLimit) {
                                resultLimitReached = true;
                                // Without --max-results, fd keeps listing after the limit.
                                if (windowsPathMatcher)
                                    stopChild?.();
                                return;
                            }
`],
]);
// END backport 282b01c80

// BEGIN backport 282b01c80
// 282b01c80: grep's glob description, in the wording find's pattern now shares.
patch("282b01c80", "pi-coding-agent/core/tools/grep.js", [
    [`"Filter files by glob pattern, relative to path, e.g. '*.ts', 'src/**/*.ts' or '**/*.spec.ts'. A pattern without '/' matches file names at any depth"`,
      `"Filter files by glob pattern relative to path, e.g. 'src/**/*.ts'. Without '/', e.g. '*.ts', it matches file names at any depth"`],
]);
// END backport 282b01c80

// BEGIN backport 282b01c80
// 282b01c80: the gondolin example's glob for find and grep anchors too and drops a leading "./".
patch("282b01c80", "pi-coding-agent/examples/extensions/gondolin/index.ts", [
    [`function matchesToolGlob(relativePath: string, pattern: string): boolean {
	const normalizedPattern = toPosix(pattern);
	if (normalizedPattern.includes("/")) {
		return (
			path.posix.matchesGlob(relativePath, normalizedPattern) ||
			path.posix.matchesGlob(relativePath, \`**/\${normalizedPattern}\`)
		);
	}
`,
      `// Like the built-in find and grep: a pattern with '/' is relative to the search root, one without
// '/' matches file names at any depth.
function matchesToolGlob(relativePath: string, pattern: string): boolean {
	const normalizedPattern = toPosix(pattern).replace(/^(?:\\.\\/)+/, "");
	if (normalizedPattern.includes("/")) return path.posix.matchesGlob(relativePath, normalizedPattern);
`],
]);
// END backport 282b01c80

// BEGIN backport 935f0ac0ccfc
// 935f0ac0ccfc (#12092): the bash tool keeps the end of the last line when the retained tail is only that over-long line.
patch("935f0ac0ccfc", "pi-coding-agent/core/tools/output-accumulator.js", [
    ["        const tailTruncation = truncateTail(this.getSnapshotText(), {\n",
      `        ${note("935f0ac0ccfc").trim()}
        // Clipping drops the partial first line of the retained tail. When the tail is only the end of one over-long line,
        // that is everything, so show the end of that line instead.
        const snapshotText = this.getSnapshotText() || this.tailText;
        const tailTruncation = truncateTail(snapshotText, {
`],
]);
// END backport 935f0ac0ccfc

// BEGIN backport 0e1e3fc81
// 0e1e3fc81 (#12052): OSC 8 hyperlinks in HTML-exported tool output become links.
patch("0e1e3fc81", "pi-coding-agent/core/export-html/ansi-to-html.js", [
    [` * - Reset (0)
 */`, ` * - Reset (0)
 * - OSC 8 hyperlinks with http, https, or mailto URLs (other CSI, OSC, and APC sequences and stray ESC characters are dropped)
 */`],
    [`// Match ANSI escape sequences: ESC[ followed by params and ending with 'm'
const ANSI_REGEX = /\\x1b\\[([\\d;]*)m/g;
`, `${note("0e1e3fc81")}// Match escape sequences: SGR (ESC[...m), other CSI sequences, OSC sequences, and APC sequences (terminated by BEL or ST)
const ANSI_REGEX = /\\x1b\\[([\\d;]*)m|\\x1b\\[[0-?]*[ -/]*[@-~]|\\x1b\\]([^\\x07\\x1b]*)(?:\\x07|\\x1b\\\\)|\\x1b_[^\\x07\\x1b]*(?:\\x07|\\x1b\\\\)/g;
// OSC 8 hyperlink payload: "8;params;url"
const OSC8_REGEX = /^8;[^;]*;(.*)$/s;
const SAFE_LINK_REGEX = /^(?:https?:|mailto:)/i;
/**
 * Remove all escape sequences matched by the converter, plus stray ESC characters.
 */
export function stripAnsiEscapes(text) {
    return text.replace(ANSI_REGEX, "").replace(/\\x1b/g, "");
}
`],
    [`    let inSpan = false;
    // Reset regex state`, `    let inSpan = false;
    let inLink = false;
    const closeSpan = () => {
        if (inSpan) {
            result += "</span>";
            inSpan = false;
        }
    };
    const openSpan = () => {
        if (hasStyle(style)) {
            result += \`<span style="\${styleToInlineCSS(style)}">\`;
            inSpan = true;
        }
    };
    // Reset regex state`],
    [`            result += escapeHtml(beforeText);
        }
        // Parse SGR parameters
        const paramStr = match[1];
        const params = paramStr ? paramStr.split(";").map((p) => parseInt(p, 10) || 0) : [0];
        // Close existing span if we have one
        if (inSpan) {
            result += "</span>";
            inSpan = false;
        }
        // Apply the codes
        applySgrCode(params, style);
        // Open new span if we have any styling
        if (hasStyle(style)) {
            result += \`<span style="\${styleToInlineCSS(style)}">\`;
            inSpan = true;
        }
`, `            result += escapeHtml(beforeText.replace(/\\x1b/g, ""));
        }
        if (match[1] !== undefined) {
            // Parse SGR parameters
            const paramStr = match[1];
            const params = paramStr ? paramStr.split(";").map((p) => parseInt(p, 10) || 0) : [0];
            closeSpan();
            applySgrCode(params, style);
            openSpan();
        }
        else if (match[2] !== undefined) {
            const hyperlink = OSC8_REGEX.exec(match[2]);
            if (hyperlink) {
                // Keep spans nested inside the link: close and reopen the current style around link changes
                // Browsers strip C0 controls from URLs, so strip them before checking the scheme
                const url = hyperlink[1].trim().replace(/[\\x00-\\x1f\\x7f]/g, "");
                closeSpan();
                if (inLink) {
                    result += "</a>";
                    inLink = false;
                }
                if (url && SAFE_LINK_REGEX.test(url)) {
                    result += \`<a href="\${escapeHtml(url)}">\`;
                    inLink = true;
                }
                openSpan();
            }
            // Other OSC sequences are dropped
        }
        // Other CSI and APC sequences are dropped
`],
    [`        result += escapeHtml(remainingText);
    }
    // Close any open span
    if (inSpan) {
        result += "</span>";
    }
`, `        result += escapeHtml(remainingText.replace(/\\x1b/g, ""));
    }
    // Close any open span and link
    closeSpan();
    if (inLink) {
        result += "</a>";
    }
`],
]);
// END backport 0e1e3fc81

// BEGIN backport a83b75a07
// a83b75a07 (#12089): a command a signal ended reports 128 + the signal number as its code, not 0.
patch("a83b75a07", "pi-coding-agent/core/exec.js", [
    ['import { spawn } from "node:child_process";\n', 'import { spawn } from "node:child_process";\nimport { constants as osConstants } from "node:os";\n'],
    ["    const exitCode = code ?? 0;\n",
      `    ${note("a83b75a07").trim()}
    // A signal-killed command has no exit code. Use the shell convention, as the bash tool does, so a
    // timeout, an abort or a crash does not read as success (#12088).
    const exitCode = code ?? (proc.signalCode ? 128 + (osConstants.signals[proc.signalCode] ?? 0) : 1);
`],
]);
// END backport a83b75a07

// BEGIN backport a83b75a07
// a83b75a07 (#12089): the pi.exec docs name the code of a command a signal ended.
patch("a83b75a07", "pi-coding-agent/docs/extensions.md", [
    ["A command that ignores SIGTERM gets SIGKILL after 5 seconds.\n",
      "A command that ignores SIGTERM gets SIGKILL after 5 seconds. A command terminated by a signal, by pi or otherwise, reports 128 + the signal number as `code`, like a shell: 143 for SIGTERM, 137 for SIGKILL.\n"],
]);
// END backport a83b75a07

// BEGIN backport 0e1e3fc81
// 0e1e3fc81 (#12052): a rendered line is blank once every escape sequence the converter handles is stripped.
patch("0e1e3fc81", "pi-coding-agent/core/export-html/tool-renderer.js", [
    ['import { ansiLinesToHtml } from "./ansi-to-html.js";', 'import { ansiLinesToHtml, stripAnsiEscapes } from "./ansi-to-html.js";'],
    ["const ANSI_ESCAPE_REGEX = /\\x1b\\[[\\d;]*m/g;\n", ""],
    ['    return line.replace(ANSI_ESCAPE_REGEX, "").trim().length === 0;', '    return stripAnsiEscapes(line).trim().length === 0;'],
]);
// END backport 0e1e3fc81

// BEGIN backport 0e1e3fc81
// 0e1e3fc81 (#12052): a click on a link in an expandable tool output does not toggle it.
patch("0e1e3fc81", "pi-coding-agent/core/export-html/template.js", [
    ["class=\"tool-output expandable ansi-rendered\" onclick=\"if(window.getSelection().toString())return;this.classList.toggle('expanded')\">",
      "class=\"tool-output expandable ansi-rendered\" onclick=\"if(window.getSelection().toString()||event.target.closest('a'))return;this.classList.toggle('expanded')\">"],
]);
// END backport 0e1e3fc81

// BEGIN backport 3345c2925
// 3345c2925 (#12054): hiding an extension's own tool keeps the restored MCP tools pending.
patch("3345c2925", "pi-coding-agent/core/agent-session.js", [
    ["    _pendingToolNames = new Set();\n",
      "    _pendingToolNames = new Set();\n    /** The restored or reloaded loadout that `_pendingToolNames` belongs to. */\n    _restoredToolNames = new Set();\n"],
    [`        // A loadout that deactivates a tool replaces the restored one, whose pending tools are dropped.
        // One that only adds tools, like activating tool_search, keeps them.
        const active = new Set(this.getActiveToolNames());
        if (previous.some((name) => !active.has(name)))
            this._pendingToolNames.clear();
`, `        // A loadout that deactivates a tool of the restored one replaces it, and its pending tools are
        // dropped. One that only adds tools, like activating tool_search, keeps them, and so does one
        // that only deactivates tools outside the restored loadout, such as an extension's own tool that
        // the restore or reload activated as newly registered.
        const active = new Set(this.getActiveToolNames());
        if (previous.some((name) => !active.has(name) && this._restoredToolNames.has(name))) {
            this._pendingToolNames.clear();
        }
`],
    ["    _restoreToolsFromTranscript(modifiers = []) {\n        this._pendingToolNames.clear();\n",
      "    _restoreToolsFromTranscript(modifiers = []) {\n        this._pendingToolNames.clear();\n        this._restoredToolNames.clear();\n"],
    [`        this._pendingToolNames = new Set(names.filter((name) => this._isAllowedTool(name)));
        // Ignores names that are not registered.
`, `        this._pendingToolNames = new Set(names.filter((name) => this._isAllowedTool(name)));
        this._restoredToolNames = new Set(applyToolModifiers(getCurrentTools(systemMessages).map((tool) => tool.name), modifiers));
        // Ignores names that are not registered.
`],
    [`        for (const name of this.getActiveToolNames())
            this._pendingToolNames.add(name);
        this._buildRuntime({`, `        for (const name of this.getActiveToolNames())
            this._pendingToolNames.add(name);
        this._restoredToolNames = new Set(this.getActiveToolNames());
        this._buildRuntime({`],
]);
// END backport 3345c2925

// BEGIN backport a635f3901
// a635f3901 (#12111): the /resume picker keeps its selection while the session list reloads progressively.
patch("a635f3901", "pi-coding-agent/modes/interactive/components/session-selector.js", [
    ["    selectionTouched = false;\n    searchInput;\n",
      `    selectionTouched = false;
    ${note("a635f3901").trim()}
    // Session to select once it appears, while a progressive reload has not yet delivered it
    pendingSelectedPath;
    searchInput;
`],
    [`    setSessions(sessions, showCwd) {
        const selectedPath = this.selectionTouched ? this.getSelectedSessionPath() : undefined;
        this.allSessions = sessions;
        this.showCwd = showCwd;
        this.filterSessions(this.searchInput.getValue());
        if (!this.selectionTouched) {
            this.selectedIndex = 0;
        }
        else if (selectedPath) {
            const selectedIndex = this.filteredSessions.findIndex((node) => node.session.path === selectedPath);
            if (selectedIndex >= 0)
                this.selectedIndex = selectedIndex;
        }
    }
`, `    ${note("a635f3901").trim()}
    /**
     * @param partial true when more sessions are still loading; a selected session missing from a
     * partial list stays the selection target until it arrives
     */
    setSessions(sessions, showCwd, partial = false) {
        const pendingPath = this.selectionTouched ? this.pendingSelectedPath : undefined;
        const selectedPath = this.selectionTouched ? (pendingPath ?? this.getSelectedSessionPath()) : undefined;
        this.allSessions = sessions;
        this.showCwd = showCwd;
        this.filterSessions(this.searchInput.getValue());
        if (!this.selectionTouched) {
            this.selectedIndex = 0;
        }
        else if (selectedPath) {
            const selectedIndex = this.filteredSessions.findIndex((node) => node.session.path === selectedPath);
            if (selectedIndex >= 0) {
                this.selectedIndex = selectedIndex;
                this.pendingSelectedPath = undefined;
            }
            else if (partial) {
                // The selected session may not have loaded yet: keep it as the target instead of
                // adopting whichever session the index now points to.
                this.pendingSelectedPath = selectedPath;
            }
            else {
                this.pendingSelectedPath = undefined;
            }
        }
    }
`],
    ["        this.selectionTouched = true;\n        // Up arrow\n",
      `        this.selectionTouched = true;
        ${note("a635f3901").trim()}
        this.pendingSelectedPath = undefined;
        // Up arrow
`],
    ["                if (scope === this.scope)\n                    this.sessionList.setSessions(sessions, showCwd);\n",
      `                ${note("a635f3901").trim()}
                if (scope === this.scope)
                    this.sessionList.setSessions(sessions, showCwd, true);
`],
    ['        this.sessionList.setSessions(sessions ?? [], this.scope === "all");\n',
      `        ${note("a635f3901").trim()}
        this.sessionList.setSessions(sessions ?? [], this.scope === "all", loading || sessions === null);
`],
]);
// END backport a635f3901

// BEGIN backport 406e719a6bff
// 406e719a6bff (#12049): a tool schema with a node that has no type, enum or anyOf is not sent strict (OpenAI strict mode rejects it).
patch("406e719a6bff", "pi-ai/api/constrained-sampling.js", [
    [`        }
    }
    if (schema.anyOf !== undefined) {
`, `        }
    }
    ${note("406e719a6bff").trim()}
    // OpenAI strict mode rejects nodes without a type; a bare enum counts as typed, a bare const does not.
    if (schema.type === undefined && schema.anyOf === undefined && schema.enum === undefined) {
        throw new UnsupportedStrictJsonSchemaError("schemas without a type are unsupported");
    }
    if (schema.anyOf !== undefined) {
`],
]);
// END backport 406e719a6bff

// BEGIN backport 1397070024
// 1397070024 (#12112, #11773): Qwen Token Plan uses the current gateways, per-plan key variables and the current Team and Individual model lists.
// Pi's four provider modules, its env-api-keys table, its help text and its providers doc carry the change as source. The provider data
// (providers/data/*.json) is NOT carried here: Pi regenerates it from models.dev at each build, and the oracle's data is what Pi's own
// generator makes from the committed recording (automation/gen/catalog-source, automation/pin/oracle.py) with this commit's catalog files
// (pi-overlay/catalog/providers/qwen-token-plan*.ts) in place of the pin's.
patch("1397070024", "pi-ai/providers/qwen-token-plan.js", [
    [`        name: "Qwen Token Plan",
        baseUrl: "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1",
        auth: { apiKey: envApiKeyAuth("Qwen Token Plan API key", ["QWEN_TOKEN_PLAN_API_KEY"]) },
`, `        ${note("1397070024").trim()}
        name: "Qwen Token Plan Team",
        baseUrl: "https://token-plan.maas.qwencloudapi.com/compatible-mode/v1",
        auth: {
            apiKey: envApiKeyAuth("Qwen Token Plan Team API key", [
                "QWEN_TOKEN_PLAN_TEAM_API_KEY",
                "QWEN_TOKEN_PLAN_API_KEY",
            ]),
        },
`],
]);
// END backport 1397070024

// BEGIN backport 1397070024
patch("1397070024", "pi-ai/providers/qwen-token-plan-cn.js", [
    [`        name: "Qwen Token Plan CN",
        baseUrl: "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
        auth: { apiKey: envApiKeyAuth("Qwen Token Plan CN API key", ["QWEN_TOKEN_PLAN_CN_API_KEY"]) },
`, `        ${note("1397070024").trim()}
        name: "Qwen Token Plan Team CN",
        baseUrl: "https://token-plan.maas.qianwenaiapi.com/compatible-mode/v1",
        auth: {
            apiKey: envApiKeyAuth("Qwen Token Plan Team CN API key", [
                "QWEN_TOKEN_PLAN_TEAM_CN_API_KEY",
                "QWEN_TOKEN_PLAN_CN_API_KEY",
            ]),
        },
`],
]);
// END backport 1397070024

// BEGIN backport 1397070024
patch("1397070024", "pi-ai/providers/qwen-token-plan-individual.js", [
    [`        name: "Qwen Token Plan Individual",
        baseUrl: "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1",
        auth: { apiKey: envApiKeyAuth("Qwen Token Plan Individual API key", ["QWEN_TOKEN_PLAN_API_KEY"]) },
`, `        ${note("1397070024").trim()}
        name: "Qwen Token Plan Individual",
        baseUrl: "https://token-plan.maas.qwencloudapi.com/compatible-mode/v1",
        auth: {
            apiKey: envApiKeyAuth("Qwen Token Plan Individual API key", [
                "QWEN_TOKEN_PLAN_INDIVIDUAL_API_KEY",
                "QWEN_TOKEN_PLAN_API_KEY",
            ]),
        },
`],
]);
// END backport 1397070024

// BEGIN backport 1397070024
patch("1397070024", "pi-ai/providers/qwen-token-plan-individual-cn.js", [
    [`        name: "Qwen Token Plan Individual CN",
        baseUrl: "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
        auth: { apiKey: envApiKeyAuth("Qwen Token Plan Individual CN API key", ["QWEN_TOKEN_PLAN_CN_API_KEY"]) },
`, `        ${note("1397070024").trim()}
        name: "Qwen Token Plan Individual CN",
        baseUrl: "https://token-plan.maas.qianwenaiapi.com/compatible-mode/v1",
        auth: {
            apiKey: envApiKeyAuth("Qwen Token Plan Individual CN API key", [
                "QWEN_TOKEN_PLAN_INDIVIDUAL_CN_API_KEY",
                "QWEN_TOKEN_PLAN_CN_API_KEY",
            ]),
        },
`],
]);
// END backport 1397070024

// BEGIN backport 1397070024
// 1397070024: each Qwen Token Plan provider reads its per-plan variable first, then the region variable both plans used to share.
patch("1397070024", "pi-ai/env-api-keys.js", [
    [`        "ant-ling": "ANT_LING_API_KEY",
        "qwen-token-plan": "QWEN_TOKEN_PLAN_API_KEY",
        "qwen-token-plan-cn": "QWEN_TOKEN_PLAN_CN_API_KEY",
        "qwen-token-plan-individual": "QWEN_TOKEN_PLAN_API_KEY",
        "qwen-token-plan-individual-cn": "QWEN_TOKEN_PLAN_CN_API_KEY",
`, `        "ant-ling": "ANT_LING_API_KEY",
        ${note("1397070024").trim()}
        // Qwen Token Plan: the per-plan variable first, then the region variable both plans used to share.
        "qwen-token-plan": ["QWEN_TOKEN_PLAN_TEAM_API_KEY", "QWEN_TOKEN_PLAN_API_KEY"],
        "qwen-token-plan-cn": ["QWEN_TOKEN_PLAN_TEAM_CN_API_KEY", "QWEN_TOKEN_PLAN_CN_API_KEY"],
        "qwen-token-plan-individual": ["QWEN_TOKEN_PLAN_INDIVIDUAL_API_KEY", "QWEN_TOKEN_PLAN_API_KEY"],
        "qwen-token-plan-individual-cn": ["QWEN_TOKEN_PLAN_INDIVIDUAL_CN_API_KEY", "QWEN_TOKEN_PLAN_CN_API_KEY"],
`],
    [`    const envVar = envMap[provider];
    return envVar ? [envVar] : undefined;
`, `    const envVars = envMap[provider];
    return typeof envVars === "string" ? [envVars] : envVars;
`],
]);
// END backport 1397070024

// BEGIN backport 1397070024
// 1397070024: --help lists the four per-plan variables.
patch("1397070024", "pi-coding-agent/cli/args.js", [
    [`  QWEN_TOKEN_PLAN_API_KEY          - Qwen Token Plan API key (international region)
  QWEN_TOKEN_PLAN_CN_API_KEY       - Qwen Token Plan API key (China region)
`, `  QWEN_TOKEN_PLAN_TEAM_API_KEY     - Qwen Token Plan Team API key (international region)
  QWEN_TOKEN_PLAN_TEAM_CN_API_KEY  - Qwen Token Plan Team API key (China region)
  QWEN_TOKEN_PLAN_INDIVIDUAL_API_KEY - Qwen Token Plan Individual API key (international region)
  QWEN_TOKEN_PLAN_INDIVIDUAL_CN_API_KEY - Qwen Token Plan Individual API key (China region)
`],
]);
// END backport 1397070024

// BEGIN backport 1397070024
// 1397070024: the providers doc lists the four per-plan variables.
patch("1397070024", "pi-coding-agent/docs/providers.md", [
    [`| Qwen Token Plan and Individual | \`QWEN_TOKEN_PLAN_API_KEY\` |
| Qwen Token Plan (China) and Individual (China) | \`QWEN_TOKEN_PLAN_CN_API_KEY\` |
`, `| Qwen Token Plan Team | \`QWEN_TOKEN_PLAN_TEAM_API_KEY\` |
| Qwen Token Plan Team (China) | \`QWEN_TOKEN_PLAN_TEAM_CN_API_KEY\` |
| Qwen Token Plan Individual | \`QWEN_TOKEN_PLAN_INDIVIDUAL_API_KEY\` |
| Qwen Token Plan Individual (China) | \`QWEN_TOKEN_PLAN_INDIVIDUAL_CN_API_KEY\` |
`],
]);
// END backport 1397070024

// BEGIN backport 1397070024
// 1397070024: Pi's TypeScript sources of the four provider modules, which tests read from the source mirror (`--read-mirror`), by path in the Pi
// repository, with the same change as Pi's commit makes to the source.
mirrorSource("1397070024", "packages/ai/src/providers/qwen-token-plan.ts", [
    [`		name: "Qwen Token Plan",
		baseUrl: "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1",
		auth: { apiKey: envApiKeyAuth("Qwen Token Plan API key", ["QWEN_TOKEN_PLAN_API_KEY"]) },
`, `		name: "Qwen Token Plan Team",
		baseUrl: "https://token-plan.maas.qwencloudapi.com/compatible-mode/v1",
		auth: {
			apiKey: envApiKeyAuth("Qwen Token Plan Team API key", [
				"QWEN_TOKEN_PLAN_TEAM_API_KEY",
				"QWEN_TOKEN_PLAN_API_KEY",
			]),
		},
`],
]);
// END backport 1397070024

// BEGIN backport 1397070024
mirrorSource("1397070024", "packages/ai/src/providers/qwen-token-plan-cn.ts", [
    [`		name: "Qwen Token Plan CN",
		baseUrl: "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
		auth: { apiKey: envApiKeyAuth("Qwen Token Plan CN API key", ["QWEN_TOKEN_PLAN_CN_API_KEY"]) },
`, `		name: "Qwen Token Plan Team CN",
		baseUrl: "https://token-plan.maas.qianwenaiapi.com/compatible-mode/v1",
		auth: {
			apiKey: envApiKeyAuth("Qwen Token Plan Team CN API key", [
				"QWEN_TOKEN_PLAN_TEAM_CN_API_KEY",
				"QWEN_TOKEN_PLAN_CN_API_KEY",
			]),
		},
`],
]);
// END backport 1397070024

// BEGIN backport 1397070024
mirrorSource("1397070024", "packages/ai/src/providers/qwen-token-plan-individual.ts", [
    [`		name: "Qwen Token Plan Individual",
		baseUrl: "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1",
		auth: { apiKey: envApiKeyAuth("Qwen Token Plan Individual API key", ["QWEN_TOKEN_PLAN_API_KEY"]) },
`, `		name: "Qwen Token Plan Individual",
		baseUrl: "https://token-plan.maas.qwencloudapi.com/compatible-mode/v1",
		auth: {
			apiKey: envApiKeyAuth("Qwen Token Plan Individual API key", [
				"QWEN_TOKEN_PLAN_INDIVIDUAL_API_KEY",
				"QWEN_TOKEN_PLAN_API_KEY",
			]),
		},
`],
]);
// END backport 1397070024

// BEGIN backport 1397070024
mirrorSource("1397070024", "packages/ai/src/providers/qwen-token-plan-individual-cn.ts", [
    [`		name: "Qwen Token Plan Individual CN",
		baseUrl: "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
		auth: { apiKey: envApiKeyAuth("Qwen Token Plan Individual CN API key", ["QWEN_TOKEN_PLAN_CN_API_KEY"]) },
`, `		name: "Qwen Token Plan Individual CN",
		baseUrl: "https://token-plan.maas.qianwenaiapi.com/compatible-mode/v1",
		auth: {
			apiKey: envApiKeyAuth("Qwen Token Plan Individual CN API key", [
				"QWEN_TOKEN_PLAN_INDIVIDUAL_CN_API_KEY",
				"QWEN_TOKEN_PLAN_CN_API_KEY",
			]),
		},
`],
]);
// END backport 1397070024

// BEGIN backport 4be4cb4f5e9c
// 4be4cb4f5e9c (#12139): read's text no longer says that the selected model takes no images (the text is stored and replayed after a switch to a
// vision model); the tool names such a model in details.imageUnsupportedByModel instead, which the TUI shows and the model never gets.
patch("4be4cb4f5e9c", "pi-coding-agent/core/tools/read.js", [
    [`function getNonVisionImageNote(model) {
    if (!model || model.input.includes("image")) {
        return undefined;
    }
    return "[Current model does not support images. The image will be omitted from this request.]";
}
`, ""],
    ["                        const nonVisionImageNote = getNonVisionImageNote(ctx?.model);\n", ""],
    [`                                let textNote = \`Read image file [\${mimeType}]\\n\${processed.message}\`;
                                if (nonVisionImageNote)
                                    textNote += \`\\n\${nonVisionImageNote}\`;
                                content = [{ type: "text", text: textNote }];
`, `                                const textNote = \`Read image file [\${mimeType}]\\n\${processed.message}\`;
                                content = [{ type: "text", text: textNote }];
`],
    [`                                if (nonVisionImageNote)
                                    textNote += \`\\n\${nonVisionImageNote}\`;
                                content = [
                                    { type: "text", text: textNote },
                                    { type: "image", data: processed.data, mimeType: processed.mimeType },
                                ];
`, `                                content = [
                                    { type: "text", text: textNote },
                                    { type: "image", data: processed.data, mimeType: processed.mimeType },
                                ];
                                ${note("4be4cb4f5e9c").trim()}
                                if (ctx?.model && !ctx.model.input.includes("image")) {
                                    details = { imageUnsupportedByModel: ctx.model.id };
                                }
`],
]);
// END backport 4be4cb4f5e9c

// BEGIN backport 4be4cb4f5e9c
// 4be4cb4f5e9c (#12139): the read card draws the warning from details.imageUnsupportedByModel, collapsed and expanded, naming the model that read the image.
patch("4be4cb4f5e9c", "pi-coding-agent/core/tools/renderers/read.js", [
    [`function formatReadResult(args, result, options, theme, showImages, _cwd, isError) {
    if (!options.expanded && !isError) {
        return "";
    }
`, `function formatReadResult(args, result, options, theme, showImages, _cwd, isError) {
    ${note("4be4cb4f5e9c").trim()}
    const unsupportedBy = result.details?.imageUnsupportedByModel;
    const imageWarning = unsupportedBy
        ? \`\\n\${theme.fg("warning", \`[\${unsupportedBy} does not support images; it saw a placeholder instead]\`)}\`
        : "";
    if (!options.expanded && !isError) {
        return imageWarning;
    }
`],
    [`    return text;
}
export const readRenderers = {`, `    return text + imageWarning;
}
export const readRenderers = {`],
]);
// END backport 4be4cb4f5e9c

// BEGIN backport 4438c58ba
// 4438c58ba (#9986): an aborted tool batch answers every call. The sequential and the parallel loop no longer stop at an aborted signal, and prepareToolCall
// answers "Operation aborted" before the tool lookup and beforeToolCall, so each call the loop had not reached gets its start event, its end event and an error
// result, and the hooks are not asked about a call that will not run. runToolCall checks the signal first too (its doc comment says so).
// not carried: pi-agent-core/agent-loop.d.ts (the doc comment of runToolCall): a declaration file; no backport table patches one, and nothing runs it.
patch("4438c58ba", "pi-agent-core/agent-loop.js", [
    [`        finalizedCalls.push(finalized);
        messages.push(toolResultMessage);
        if (signal?.aborted) {
            break;
        }
    }
`, `        finalizedCalls.push(finalized);
        messages.push(toolResultMessage);
    }
`],
    [`            await emitToolExecutionEnd(finalized, emit);
            finalizedCalls.push(finalized);
            if (signal?.aborted) {
                break;
            }
            continue;
`, `            await emitToolExecutionEnd(finalized, emit);
            finalizedCalls.push(finalized);
            continue;
`],
    [`            await emitToolExecutionEnd(finalized, emit);
            return finalized;
        });
        if (signal?.aborted) {
            break;
        }
    }
`, `            await emitToolExecutionEnd(finalized, emit);
            return finalized;
        });
    }
`],
    [`async function prepareToolCall(currentContext, assistantMessage, toolCall, config, signal, tools = currentContext.tools ?? []) {
    const tool = tools.find((t) => t.name === toolCall.name);
`, `async function prepareToolCall(currentContext, assistantMessage, toolCall, config, signal, tools = currentContext.tools ?? []) {
    ${note("4438c58ba").trim()}
    // Once aborted, skip preparation and the hooks; callers still record this result, so no call
    // is left without one (#9986).
    if (signal?.aborted) {
        return {
            kind: "immediate",
            result: createErrorToolResult("Operation aborted"),
            isError: true,
        };
    }
    const tool = tools.find((t) => t.name === toolCall.name);
`],
    [`            }
        }
        if (signal?.aborted) {
            return {
                kind: "immediate",
                result: createErrorToolResult("Operation aborted"),
                isError: true,
            };
        }
        return {
            kind: "prepared",
`, `            }
        }
        return {
            kind: "prepared",
`],
    [` * Never rejects for tool failures: unknown tools, validation errors, blocked calls, and thrown
 * errors come back as \`isError: true\`.
`, ` * Never rejects for tool failures: an aborted \`signal\`, unknown tools, validation errors, blocked
 * calls, and thrown errors come back as \`isError: true\`. An aborted \`signal\` is checked first,
 * before the hooks run.
`],
]);
// END backport 4438c58ba

// BEGIN backport 49fe0a237
// 49fe0a237 (#11987): the models.json schema Pi generates from compat-schema.ts.
packageFile("49fe0a237", "pi-coding-agent/schemas/models.schema.json", [messageThreadsDefault]);
// END backport 49fe0a237

// BEGIN backport 49fe0a237
// 49fe0a237 (#11987), the behavior its three description entries describe: any provider with a baseUrl on api.anthropic.com uses message threads by
// default, not only the provider named anthropic. The installed Pi only, as for aa47cedce below: ai/anthropic.go (supportsMessageThreads) has the change.
packageFile("49fe0a237", "pi-ai/api/anthropic-messages.js", [
    [`        supportsMessageThreads: model.compat?.supportsMessageThreads ??
            (model.provider === "anthropic" && model.baseUrl.includes("api.anthropic.com")),
`, `        ${note("49fe0a237").trim()}
        supportsMessageThreads: model.compat?.supportsMessageThreads ?? model.baseUrl.includes("api.anthropic.com"),
`],
]);
// END backport 49fe0a237

// BEGIN backport aa47cedce
// aa47cedce (#12100): a non-strict Anthropic tool schema keeps its root additionalProperties (false, true or a schema). PiG's vendored runtime carries pi-ai's
// anthropic-messages as a bridge stub that runs in PiG's host (ai/anthropic.go, which has the change), so the file is the installed Pi's only: the parity oracle
// (the request body of test/pipeline-diff's tool schema comparison) is Pi with the fix.
packageFile("aa47cedce", "pi-ai/api/anthropic-messages.js", [
    [`        // Keep shared definitions so property $refs still resolve. Other root keywords stay out:
        // Anthropic rejects root anyOf/oneOf/allOf with a 400.
        const legacyInputSchema = {
            type: "object",
            properties: schema.properties ?? {},
            required: schema.required ?? [],
`, `        // Keep shared definitions so property $refs still resolve, and additionalProperties so the
        // model knows whether extra arguments are allowed. Other root keywords stay out:
        // Anthropic rejects root anyOf/oneOf/allOf with a 400.
        ${note("aa47cedce").trim()}
        const legacyInputSchema = {
            type: "object",
            properties: schema.properties ?? {},
            required: schema.required ?? [],
            ...(schema.additionalProperties === undefined ? {} : { additionalProperties: schema.additionalProperties }),
`],
]);
// END backport aa47cedce

// BEGIN backport f72f799a4
// f72f799a4 (#12103; 22f456d73 #12108 after it changes Pi's changelog and a test only): the Google adapters hand the caller's fetch to @google/genai, which takes one
// (httpOptions.fetch) from 2.32.0 on, so the commit moves pi-ai's exact pin of the SDK. The lock entry below is the one Pi's package-lock.json and
// packages/coding-agent/install-lock/package-lock.json hold at f72f799a4; both locks change nothing else but pi-ai's range, and the entry's own dependencies are those of 2.21.0.
// Oracle only: the vendored runtime's Google adapters are bridge stubs that run PiG's Go adapters (D74), which carry the behaviour, and it installs no SDK. PiG has no
// @google/genai at all: its Go body encoder mirrors the SDK's converters (ai/google_wire.go, which names the mirrored version). The oracle is Pi and installs the SDK.
// The oracle carries the commit's BEHAVIOUR (the SDK, the three adapters, the bundle of both). What authors and inventories derive from the installed package stays the pin's:
// not carried: pi-ai/package.json (`"@google/genai": "2.32.0"`): published ranges follow the pin until the re-pin (extensions/sdk-ts/package.json derives its peer range from the installed manifest); the lock the oracle installs names 2.32.0, the manifest on disk 2.21.0. Re-pin worklist: arrives with the pin.
// not carried: pi-ai/api/google-shared.d.ts (drops `mapStopReasonString`, which google-shared.js below no longer exports): declarations follow the pin until the re-pin (extensions/sdk-ts/pi-pin.d.ts and test/parity/interfaces derive from the installed declarations). Re-pin worklist: arrives with the pin.
// not carried: pi-ai/api/google-{generative-ai,shared,vertex}.js.map and .d.ts.map: nothing imports through a source map and Pi's bundler runs with source maps off.
dependency("f72f799a4", "pi-ai", {
    name: "@google/genai",
    from: "2.21.0",
    to: "2.32.0",
    resolved: "https://registry.npmjs.org/@google/genai/-/genai-2.32.0.tgz",
    integrity: "sha512-60c79uXpR0a47bmGqDxbXEBuQUalNmwo5mx5kqb91jIW4FoqpsLSt86DeVhRhtH5+s/16Wqxt3sXzFQn2U1OHQ==",
    dependencies: { "google-auth-library": "^10.3.0", "p-retry": "^4.6.2", protobufjs: "^7.5.4", ws: "^8.18.0" },
});
// END backport f72f799a4

// BEGIN backport f72f799a4
// The compiled output of the three adapters, byte for byte what Pi's build of f72f799a4 emits (no note line): Pi built at the commit and at its parent differs in
// these three files, google-shared.d.ts and their source maps only, and the pinned build equals the parent's in all of them.
packageFile("f72f799a4", "pi-ai/api/google-generative-ai.js", [
    [`            if (options?.fetch && options.fetch !== globalThis.fetch) {
                throw new Error("Custom fetch is not supported by the Google Generative AI adapter");
            }
`,
      ``],
    [`            const client = createClient(model, apiKey, options?.headers, options?.identity);
`,
      `            const client = createClient(model, apiKey, options?.headers, options?.identity, options?.fetch);
`],
    [`function createClient(model, apiKey, optionsHeaders, identity) {
    const httpOptions = {};
`,
      `function createClient(model, apiKey, optionsHeaders, identity, fetch) {
    // Always pass fetch: without one, @google/genai adds a per-request dispatcher that disables
    // undici's header and body timeouts, bypassing the idle timeouts of the global dispatcher.
    const httpOptions = { fetch: fetch ?? globalThis.fetch };
`],
    [`    return new GoogleGenAI({
        apiKey,
        httpOptions: Object.keys(httpOptions).length > 0 ? httpOptions : undefined,
    });
`,
      `    return new GoogleGenAI({ apiKey, httpOptions });
`],
    [`        }),
    };
`,
      `        }),
        // Off for now, until we have evaluated the implications of the SDK's automatic continuation
        // (extra requests and stream resumes inside the SDK). A CONTINUATION finish ends the turn as "length".
        automaticContinuation: false,
    };
`],
]);
// END backport f72f799a4

// BEGIN backport f72f799a4
packageFile("f72f799a4", "pi-ai/api/google-shared.js", [
    [`        case FinishReason.MAX_TOKENS:
            return "length";
`,
      `        case FinishReason.MAX_TOKENS:
        // Per-request token limit reached before generation completed; pi does not resume with the continuation token.
        case FinishReason.CONTINUATION:
            return "length";
`],
    [`    }
}
/**
 * Map string finish reason to our StopReason (for raw API responses).
 */
export function mapStopReasonString(reason) {
    switch (reason) {
        case "STOP":
            return "stop";
        case "MAX_TOKENS":
            return "length";
        default:
            return "error";
`,
      ``],
]);
// END backport f72f799a4

// BEGIN backport f72f799a4
packageFile("f72f799a4", "pi-ai/api/google-vertex.js", [
    [`            if (options?.fetch && options.fetch !== globalThis.fetch) {
                throw new Error("Custom fetch is not supported by the Google Vertex adapter");
            }
`,
      ``],
    [`                ? createClientWithApiKey(model, apiKey, options?.headers, options?.identity)
                : createClient(model, resolveProject(options), resolveLocation(options), options?.headers, options?.env, options?.identity);
`,
      `                ? createClientWithApiKey(model, apiKey, options?.headers, options?.identity, options?.fetch)
                : createClient(model, resolveProject(options), resolveLocation(options), options?.headers, options?.env, options?.identity, options?.fetch);
`],
    [`function createClient(model, project, location, optionsHeaders, env, identity) {
`,
      `function createClient(model, project, location, optionsHeaders, env, identity, fetch) {
`],
    [`        ...(googleAuthOptions ? { googleAuthOptions } : {}),
        httpOptions: buildHttpOptions(model, optionsHeaders, identity),
    });
`,
      `        ...(googleAuthOptions ? { googleAuthOptions } : {}),
        httpOptions: buildHttpOptions(model, optionsHeaders, identity, fetch),
    });
`],
    [`function createClientWithApiKey(model, apiKey, optionsHeaders, identity) {
`,
      `function createClientWithApiKey(model, apiKey, optionsHeaders, identity, fetch) {
`],
    [`        apiVersion: API_VERSION,
        httpOptions: buildHttpOptions(model, optionsHeaders, identity),
    });
`,
      `        apiVersion: API_VERSION,
        httpOptions: buildHttpOptions(model, optionsHeaders, identity, fetch),
    });
`],
    [`function buildHttpOptions(model, optionsHeaders, identity) {
    const httpOptions = {};
`,
      `function buildHttpOptions(model, optionsHeaders, identity, fetch) {
    // Always pass fetch: without one, @google/genai adds a per-request dispatcher that disables
    // undici's header and body timeouts, bypassing the idle timeouts of the global dispatcher.
    const httpOptions = { fetch: fetch ?? globalThis.fetch };
`],
    [`    return Object.keys(httpOptions).length > 0 ? httpOptions : undefined;
`,
      `    return httpOptions;
`],
    [`        }),
    };
`,
      `        }),
        // Off for now, until we have evaluated the implications of the SDK's automatic continuation
        // (extra requests and stream resumes inside the SDK). A CONTINUATION finish ends the turn as "length".
        automaticContinuation: false,
    };
`],
]);
// END backport f72f799a4

// BEGIN backport ede360e4d
// ede360e4d (#12121): getLastAssistantText (/copy, RPC get_last_assistant_text) skips an assistant message without text whose turn did not end normally.
patch("ede360e4d", "pi-coding-agent/core/agent-session.js", [
    [`     * Get text content of last assistant message.
     * Useful for /copy command.
     * @returns Text content, or undefined if no assistant message exists
     */
    getLastAssistantText() {
        const lastAssistant = this.messages
            .slice()
            .reverse()
            .find((m) => {
            if (m.role !== "assistant")
                return false;
            const msg = m;
            // Skip aborted messages with no content
            if (msg.stopReason === "aborted" && msg.content.length === 0)
                return false;
`, `     * Get text content of the last assistant reply.
     * Useful for /copy command.
     * @returns Text content, or undefined if no assistant message exists or the last reply has no text
     */
    getLastAssistantText() {
        const lastAssistant = this.messages
            .slice()
            .reverse()
            .find((m) => {
            if (m.role !== "assistant")
                return false;
            const msg = m;
            // Skip messages without text from a turn that did not end normally: aborted, failed or only
            // calling tools (#12119). A reply that ended normally stays the last reply even without text.
            ${note("ede360e4d").trim()}
            if (msg.stopReason !== "stop" && !msg.content.some((c) => c.type === "text" && c.text.trim()))
                return false;
`],
]);
// END backport ede360e4d

// BEGIN backport ede360e4d
// ede360e4d (#12121): the RPC docs of get_last_assistant_text name the skipped messages.
patch("ede360e4d", "pi-coding-agent/docs/rpc-commands.md", [
    ["Get the text content of the last assistant message.\n",
      "Get the text content of the last assistant message. Messages without text from an aborted or failed turn, or that only call tools, are skipped.\n"],
]);
// END backport ede360e4d

// BEGIN backport 5b5be2c316e0
// 5b5be2c316e0 (#12145): the rows of a tool that draws itself (renderShell "self", which includes the built-in edit) are appended in a loop. The spread of hundreds of
// thousands of wrapped rows exceeded V8's argument limit (RangeError: Maximum call stack size exceeded) on the render and on every resume of the session. The vendored
// runtime exports ToolExecutionComponent to Node extensions, and the parity oracle resumes the generated session of interactive-rendering/72-resume-very-large-edit-diff.
patch("5b5be2c316e0", "pi-coding-agent/modes/interactive/components/tool-execution.js", [
    [`                lines.push("");
                lines.push(...contentLines);
`, `                lines.push("");
                ${note("5b5be2c316e0").trim()}
                // Append in a loop: spreading hundreds of thousands of wrapped lines exceeds V8's argument limit.
                for (const line of contentLines)
                    lines.push(line);
`],
]);
// END backport 5b5be2c316e0

// BEGIN backport 86918f9eeb
// 86918f9eeb (#12110, closes #12019): GitHub Copilot's chat-completions models send reasoning_effort, and Copilot's reasoning count is read.
// parseChunkUsage reads the top-level `usage.reasoning_tokens` when `usage.completion_tokens_details.reasoning_tokens` is missing or falsy
// (every openai-completions provider; Copilot reports the count there). ai/openai.go parseChunkUsage and ai/openai_chunk_jsread.go have the change.
// The data side of this commit (Copilot's model catalog) is not carried here either: pi-overlay/catalog/providers/github-copilot.ts is part of the generator the oracle runs over the recording.
patch("86918f9eeb", "pi-ai/api/openai-completions.js", [
    [`        reasoning: rawUsage.completion_tokens_details?.reasoning_tokens || 0,
`, `        ${note("86918f9eeb").trim()}
        reasoning: rawUsage.completion_tokens_details?.reasoning_tokens || rawUsage.reasoning_tokens || 0,
`],
]);
// END backport 86918f9eeb

// BEGIN backport 3567987250
// 3567987250: pi-mcp exports mcpServerSummary, the one-line server summary the coding agent's mcp_servers section reads (an extension cannot import pi-mcp; the vendored coding agent and the oracle's `pi` do).
// not carried: pi-mcp/naming.d.ts and pi-mcp/index.d.ts (the declaration of mcpServerSummary): declaration files; no backport table patches one, and nothing runs them.
// not carried: packages/mcp/test/naming.test.ts (a test of the TypeScript source) and packages/mcp/CHANGELOG.md (Pi's changelog entry): the package ships neither.
patch("3567987250", "pi-mcp/naming.js", [
    [` * Names of MCP tools and of their servers' namespaces as models and code mode scripts see them.
 * Applications that name tools the same way rank and group them the same way in tool search and
 * code mode.
 */`, ` * Names of MCP tools and of their servers' namespaces, and the servers' one-line summaries, as models
 * and code mode scripts see them. Applications that name tools the same way rank and group them the
 * same way in tool search and code mode.
 */`],
    [`export function mcpNamespace(server) {
    return \`mcp__\${server.replace(/-/g, "_")}\`;
}
`, `export function mcpNamespace(server) {
    return \`mcp__\${server.replace(/-/g, "_")}\`;
}
${note("3567987250").trim()}
/**
 * A server's one-line summary for the system prompt: the first line of its configured \`description\`,
 * else of the instructions it sent in \`initialize\`; empty when it has neither.
 */
export function mcpServerSummary(description, instructions) {
    const text = description?.trim() || instructions?.trim() || "";
    return (text.split("\\n", 1)[0] ?? "").trim();
}
`],
]);
// END backport 3567987250

// BEGIN backport 3567987250
patch("3567987250", "pi-mcp/index.js", [
    ['export { createMcpToolName, mcpNamespace } from "./naming.js";', 'export { createMcpToolName, mcpNamespace, mcpServerSummary } from "./naming.js";'],
]);
// END backport 3567987250

// BEGIN backport 3567987250
// 3567987250: renderServersSection reads the summary through mcpServerSummary; the local serverSummary, which did not trim the instructions, is deleted.
patch("3567987250", "pi-coding-agent/extensions/mcp/index.js", [
    ['import { createMcpToolName, mcpNamespace } from "@earendil-works/pi-mcp";', 'import { createMcpToolName, mcpNamespace, mcpServerSummary } from "@earendil-works/pi-mcp";'],
    [`/** First line of the configured description, or of the server instructions once connected. */
function serverSummary(server) {
    const text = server.entry.config.description?.trim() || server.connection?.instructions || "";
    return (text.split("\\n", 1)[0] ?? "").trim();
}
`, ""],
    ['        const summary = perServer > 0 ? truncate(serverSummary(server), perServer) : "";\n', `        const summary = perServer > 0
            ? truncate(mcpServerSummary(server.entry.config.description, server.connection?.instructions), perServer)
            : "";
`],
]);
// END backport 3567987250

// BEGIN backport beffcde92dea
// beffcde92dea (#12230, #10470): RPC mode rebinds a replacement Session once, through the callback the runtime calls (setRebindSession), and subscribes to the Session's events
// before bindExtensions, so the events session_start handlers emit (an appended entry's entry_appended) reach the client, at startup and on every replacement.
// not carried: packages/coding-agent/test/suite/regressions/10470-rpc-session-replacement-start-once.test.ts (a test of the TypeScript source) and packages/coding-agent/CHANGELOG.md (Pi's changelog entry): the package ships neither.
// not carried: packages/coding-agent/src/modes/rpc/rpc-mode.ts (the TypeScript source): the compiled output below carries the change.
patch("beffcde92dea", "pi-coding-agent/modes/rpc/rpc-mode.js", [
    [`        });
        unsubscribe?.();
        unsubscribeBackpressure?.();
        unsubscribe = session.subscribe((event) => {
            output(toJsonEvent(event));
            if (event.type === "agent_settled") {
                checkShutdownRequested();
            }
        });
        unsubscribeBackpressure = session.agent.subscribe(async () => {
            await waitForRawStdoutBackpressure();
        });
    };
`, `        });
    };
`],
    [`    const rebindSession = async () => {
        session = runtimeHost.session;
        await session.bindExtensions({
`, `    const rebindSession = async () => {
        session = runtimeHost.session;
        ${note("beffcde92dea").trim()}
        // Subscribe before binding so events emitted by session_start handlers (e.g. appended entries) reach the client.
        unsubscribe?.();
        unsubscribeBackpressure?.();
        unsubscribe = session.subscribe((event) => {
            output(toJsonEvent(event));
            if (event.type === "agent_settled") {
                checkShutdownRequested();
            }
        });
        unsubscribeBackpressure = session.agent.subscribe(async () => {
            await waitForRawStdoutBackpressure();
        });
        await session.bindExtensions({
`],
    [`                const result = await runtimeHost.newSession(options);
                if (!result.cancelled) {
                    await rebindSession();
                }
`, `                const result = await runtimeHost.newSession(options);
`],
    [`                const result = await runtimeHost.switchSession(command.sessionPath);
                if (!result.cancelled) {
                    await rebindSession();
                }
`, `                const result = await runtimeHost.switchSession(command.sessionPath);
`],
    [`                const result = await runtimeHost.fork(command.entryId);
                if (!result.cancelled) {
                    await rebindSession();
                }
`, `                const result = await runtimeHost.fork(command.entryId);
`],
    [`                const result = await runtimeHost.fork(leafId, { position: "at" });
                if (!result.cancelled) {
                    await rebindSession();
                }
`, `                const result = await runtimeHost.fork(leafId, { position: "at" });
`],
]);
// END backport beffcde92dea

// backportPatches keys the compiled output of a Pi package; packageFileBackports the files that are not compiled output and that the vendored runtime
// does not carry, but that the installed Pi package (the parity oracle) and the source mirror hold, with the same text. Both are keyed by package and path.
// Blocks of several Pi commits can name one file: their patches add up, in the order of the blocks in this script.
const tableOf = (name) => {
  // The table of the blocks whose table is `name`: key -> patches. The patches of every block of a key are joined in the order the blocks
  // are written (two Pi commits can change one file); classification stays per block. Blocks of other tables are not read, so a block of
  // another kind need not have patches. What cannot work fails here, when the script loads, naming both blocks: two patches that replace
  // the same text of a key (the second would find nothing to replace), and a key that one block names with patch and another with
  // packageFile (patchesOf reads one of those two tables).
  const table = {};
  const owners = new Map();
  const twin = name === "dist" ? "package" : name === "package" ? "dist" : undefined;
  for (const block of blocks.filter((b) => b.table === name)) {
    const { id, key, patches } = block;
    const other = blocks.find((b) => b.table === twin && b.key === key);
    if (other !== undefined) {
      const [first, second] = blocks.indexOf(other) < blocks.indexOf(block) ? [other.id, id] : [id, other.id];
      throw new Error(`backport blocks ${first} and ${second} disagree on ${key}: one names it with patch (compiled output), the other with packageFile`);
    }
    for (const [before] of patches) {
      const place = `${key}\n${before}`;
      if (owners.has(place)) {
        throw new Error(`backport blocks ${owners.get(place)} and ${id} both replace the same text of ${key}: ${JSON.stringify(before.slice(0, 80))}`);
      }
      owners.set(place, id);
    }
    (table[key] ??= []).push(...patches);
  }
  return table;
};
export const backportPatches = tableOf("dist");
export const packageFileBackports = tableOf("package");
// mirrorSourceBackports keys the mirrorSource entries by the path of the source in the Pi repository (packages/ai/src/providers/x.ts).
export const mirrorSourceBackports = tableOf("mirror");
// carriedDependencies lists the `dependency` entries: {id, package, name, from, to, resolved, integrity, dependencies}.
export const carriedDependencies = blocks.filter((b) => b.dependency).map((b) => b.dependency);

const patchesOf = (file) => backportPatches[file] ?? packageFileBackports[file] ?? [];

// patchBackports returns source, the text of file (a key of backportPatches or packageFileBackports), with its backports applied.
export function patchBackports(file, source) {
  for (const [before, after] of patchesOf(file)) {
    if (source.split(before).length !== 2) throw new Error(`Pi source changed in ${file}, a post-v1.5.2 backport no longer applies: ${before}`);
    source = source.replace(before, () => after);
  }
  return source;
}

// checkProviderDataManifest throws when a data file of `dir` (pi-ai's providers/data) is not the one its hidden manifest records, or the manifest names a file the
// directory lacks: Pi writes the data and .manifest.json in one generation, so a backport that changes a data file must change its manifest entry in the same
// run (a block for pi-ai/providers/data/.manifest.json), or check-model-data fails on the result. The structure hash is the Go validator's to check
// (cmd/check-model-data, TestPublishedModelDataValidates). A directory with no manifest is not checked.
export function checkProviderDataManifest(dir) {
  const manifestPath = join(dir, ".manifest.json");
  if (!existsSync(manifestPath)) return;
  const files = JSON.parse(readFileSync(manifestPath, "utf8")).files;
  const problems = [];
  for (const name of readdirSync(dir).filter((name) => name.endsWith(".json") && !name.startsWith(".")).sort()) {
    const recorded = files[name];
    if (recorded === undefined) problems.push(`${name} is not in the manifest`);
    else if (createHash("sha256").update(readFileSync(join(dir, name))).digest("hex") !== recorded) problems.push(`${name} does not match its manifest hash`);
  }
  for (const name of Object.keys(files)) if (!existsSync(join(dir, name))) problems.push(`${name} is in the manifest and not in the directory`);
  if (problems.length > 0) throw new Error(`${dir}: the provider data and its manifest differ after the backports (add a pi-ai/providers/data/.manifest.json block with the hashes of the changed files):\n${problems.join("\n")}`);
}

// applyBackportPatches rewrites the vendored files under dist (shims/pi-dist).
export function applyBackportPatches(dist) {
  for (const file of Object.keys(backportPatches)) {
    const path = join(dist, file);
    writeFileSync(path, patchBackports(file, readFileSync(path, "utf8")));
  }
  checkProviderDataManifest(join(dist, "pi-ai", "providers", "data"));
}

// packageLocation is where the file of package `name` with the path `rest` (the segments of its backport key after the package)
// lives in an installed Pi package: the compiled output under dist/, the docs, schemas and examples at the package root.
const packageLocation = (name, rest) => (name === "pi-coding-agent" && (rest[0] === "docs" || rest[0] === "schemas" || rest[0] === "examples") ? rest : ["dist", ...rest]).join("/");

const packageBackportKeys = () => [...Object.keys(backportPatches), ...Object.keys(packageFileBackports)];

// applyPackageBackports applies the backports of each package in `packages` (a short name such as "pi-tui" and the root directory
// of that Pi package, as `npm ci` installs it) in place. The oracle the parity tests run (automation/pin/oracle.py) is that installed
// Pi, so it carries what the Go port carries.
export function applyPackageBackports(packages) {
  for (const file of packageBackportKeys()) {
    const [name, ...rest] = file.split("/");
    if (!(name in packages)) continue;
    const path = join(packages[name], packageLocation(name, rest));
    writeFileSync(path, patchBackports(file, readFileSync(path, "utf8")));
  }
  if ("pi-ai" in packages) checkProviderDataManifest(join(packages["pi-ai"], "dist", "providers", "data"));
}

// packageBackportFiles lists the files applyPackageBackports rewrites under `packages`.
export function packageBackportFiles(packages) {
  return packageBackportKeys().flatMap((file) => {
    const [name, ...rest] = file.split("/");
    return name in packages ? [join(packages[name], packageLocation(name, rest))] : [];
  });
}

const count = (text, part) => text.split(part).length - 1;

// verifyPackageBackports throws when a file of `packages` does not carry each of its backports once: an installed Pi that was not
// patched (a reader would compare the port with the pinned release, not with the release plus its fixes) or was patched twice.
export function verifyPackageBackports(packages) {
  const problems = [];
  for (const file of packageBackportKeys()) {
    const [name, ...rest] = file.split("/");
    if (!(name in packages)) continue;
    const text = readFileSync(join(packages[name], packageLocation(name, rest)), "utf8");
    for (const [before, after] of patchesOf(file)) {
      // A backport that only removes text leaves nothing to count but the text it removed.
      const [part, want] = after === "" ? [before, 0] : [after, 1];
      const n = count(text, part);
      if (n !== want) problems.push(`${file} holds ${n} copies of a backport's ${after === "" ? "removed" : "added"} text, want ${want}: ${part.slice(0, 80)}`);
    }
  }
  if (problems.length > 0) throw new Error(problems.join("\n"));
}

const commonPrefix = (a, b) => {
  let n = 0;
  while (n < a.length && n < b.length && a[n] === b[n]) n++;
  return n;
};

// firstDifference says where the text of an entry (`before`) first differs from the file `text`: the file's line that shares the longest prefix with
// the entry's first line, then the first later line that differs, so a maintainer reads the change without diffing by hand.
function firstDifference(before, text) {
  const want = before.split("\n");
  const have = text.split("\n");
  let at = 0;
  let best = -1;
  have.forEach((line, i) => {
    const n = commonPrefix(want[0], line);
    if (n > best) [best, at] = [n, i];
  });
  const show = (line) => JSON.stringify((line ?? "<end of file>").slice(0, 140));
  for (let i = 0; i < want.length; i++) {
    if (have[at + i] !== want[i]) return `entry text line ${i + 1} reads ${show(want[i])}, the file's line ${at + i + 1} reads ${show(have[at + i])}`;
  }
  return `the entry's text occurs ${count(text, before)} times in the file`;
}

// classifyDependency says whether the Pi package installed at `root` still pins a carried dependency at the version the entry replaces ("pending"), pins the carried
// version ("carried": the target holds the commit and the entry must go), or pins anything else ("conflict" with its old pin gone, which the caller settles with the
// commit's ancestry: a target past the commit pins a later version).
function classifyDependency({ id, key, dependency: dep }, root) {
  const pinned = JSON.parse(readFileSync(join(root, "package.json"), "utf8")).dependencies?.[dep.name];
  const state = pinned === dep.from ? "pending" : pinned === dep.to ? "carried" : "conflict";
  const detail = state === "conflict" ? `conflict: the package pins ${dep.name} ${pinned ?? "nowhere"}, the entry moves ${dep.from} to ${dep.to}` : "";
  return { id, key, state, beforeGone: state !== "pending", detail };
}

// classifyBackports says, for every block of a package in `packages`, whether the installed package is still the one the block was written for
// ("pending": each patch's before-text occurs once and its after-text not at all), already carries the block's change ("carried": each
// after-text occurs once, a removal's before-text not at all), or is neither ("conflict", with the file and the first differing line).
// Pi's own version of the change need not be the backport's text, so the caller decides a conflict whose before-text is gone (beforeGone) by
// asking whether the target contains the Pi commit.
export function classifyBackports(packages) {
  const results = [];
  // A mirror source is no file of an installed package: the compiled files of its commit decide the commit.
  for (const block of blocks.filter((block) => block.table !== "mirror")) {
    const { id, key, patches } = block;
    const [name, ...rest] = key.split("/");
    if (!(name in packages)) continue;
    if (block.dependency) {
      results.push(classifyDependency(block, packages[name]));
      continue;
    }
    const text = readFileSync(join(packages[name], packageLocation(name, rest)), "utf8");
    const states = patches.map(([before, after]) => {
      const nb = count(text, before);
      const na = after === "" ? 0 : count(text, after);
      if (after === "" ? nb === 0 : na === 1 && (nb === 0 || after.includes(before))) return "carried";
      if (nb === 1 && na === 0) return "pending";
      return `conflict: ${firstDifference(before, text)}`;
    });
    const bad = states.find((state) => state.startsWith("conflict"));
    const state = bad ? "conflict" : states.every((x) => x === "carried") ? "carried" : states.every((x) => x === "pending") ? "pending" : "conflict";
    // beforeGone: no patch's before-text is in the file any more. Pi's own version of a change rarely equals the backport's after-text
    // (the backport adds a note line), so a target that holds the Pi commit shows as "conflict" here with its before-text gone; the caller
    // settles that with the commit's ancestry.
    const beforeGone = patches.every(([before]) => count(text, before) === 0);
    results.push({ id, key, state, beforeGone, detail: bad ?? (state === "conflict" ? "some patches of the entry are carried and some are not" : "") });
  }
  return results;
}

// mirrorKey is the backport key of a file of the Pi repository the source mirror holds with the text of the installed package
// (packages/coding-agent/docs/x.md, packages/ai/src/providers/data/x.json), or undefined for a file no backport table covers.
const mirrorKey = (path) => {
  const match = /^packages\/coding-agent\/((?:docs|schemas)\/.+)$/.exec(path);
  if (match) return `pi-coding-agent/${match[1]}`;
  const data = /^packages\/ai\/src\/(providers\/data\/.+\.json)$/.exec(path);
  return data ? `pi-ai/${data[1]}` : undefined;
};
const mirrorPathOf = (key) => key.replace(/^pi-coding-agent\//, "packages/coding-agent/").replace(/^pi-ai\//, "packages/ai/src/");

// mirrorBackportPaths are the files of the source mirror a backport covers: those mirrorKey names, and the TypeScript sources of
// mirrorSourceBackports. Every other mirror file is the pinned build's, whose delta from the pin the sync ledger accounts for.
export const mirrorBackportPaths = () => [...packageBackportKeys().map(mirrorPathOf).filter((path) => mirrorKey(path) !== undefined && patchesOf(mirrorKey(path)).length > 0), ...Object.keys(mirrorSourceBackports)];

// readMirrorFile returns the text of `path` (relative to the Pi repository) in the source mirror `mirror` with the backports applied:
// the file of the pinned release plus the fixes the release line carries. The mirror itself stays the pinned release.
export function readMirrorFile(mirror, path) {
  let source = readFileSync(join(mirror, path), "utf8");
  for (const [before, after] of mirrorSourceBackports[path] ?? []) {
    if (source.split(before).length !== 2) throw new Error(`Pi source changed in ${path}, a post-v1.5.2 backport no longer applies: ${before}`);
    source = source.replace(before, () => after);
  }
  const key = mirrorKey(path);
  return key === undefined ? source : patchBackports(key, source);
}

// verifyMirrorBackports throws when a mirror file a backport covers is not the pinned release's (the backport no longer applies once).
export function verifyMirrorBackports(mirror) {
  for (const path of mirrorBackportPaths()) readMirrorFile(mirror, path);
}

const packagesOf = (args) => Object.fromEntries(args.map((arg) => arg.split(/=(.*)/s).slice(0, 2)));

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  const [mode, ...args] = process.argv.slice(2);
  if (mode === "--packages") applyPackageBackports(packagesOf(args));
  else if (mode === "--package-files") process.stdout.write(JSON.stringify(packageBackportFiles(packagesOf(args))));
  else if (mode === "--verify-packages") verifyPackageBackports(packagesOf(args));
  else if (mode === "--classify") process.stdout.write(JSON.stringify(classifyBackports(packagesOf(args))));
  else if (mode === "--dependencies") process.stdout.write(JSON.stringify(carriedDependencies));
  else if (mode === "--read-mirror") process.stdout.write(readMirrorFile(args[0], args[1]));
  else if (mode === "--verify-mirror") verifyMirrorBackports(args[0]);
  else if (mode === "--mirror-paths") process.stdout.write(mirrorBackportPaths().map((path) => `${path}\n`).join(""));
  else applyBackportPatches(mode);
}
