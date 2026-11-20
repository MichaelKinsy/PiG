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

// BEGIN backport 2c2a77f9f3ab
// 2c2a77f9f3ab (#12148): Mistral tools are sent non-strict unless the model's compat.supportsStrictMode is set (Mistral Large 4 goes silent for minutes before a strict tool call).
// The compat schema's new member, which the models docs repeat. PiG's models.json schema file (pi-coding-agent/schemas/models.schema.json) is the merged provider-level schema, which
// already holds supportsStrictMode from the other APIs (ProviderCompatPropertyOverrides), so the commit does not change it.
patch("2c2a77f9f3ab", "pi-ai/providers/compat-schema.js", [
    [`        description: "Whether the exact model accepts system messages after the conversation has started. When false, later system messages are folded into the leading system message.",
        default: false,
    }),
};
export const MistralConversationsCompatSchema`, `        description: "Whether the exact model accepts system messages after the conversation has started. When false, later system messages are folded into the leading system message.",
        default: false,
    }),
    ${note("2c2a77f9f3ab").trim()}
    supportsStrictMode: optionalCompatBoolean({
        description: "Whether the model supports strict (schema-constrained) function tools.",
        default: false,
    }),
};
export const MistralConversationsCompatSchema`],
]);
// END backport 2c2a77f9f3ab

// BEGIN backport 2c2a77f9f3ab
// 2c2a77f9f3ab, 2145e3f6099c: the models configuration reference lists the new Mistral compat member (Pi regenerated the page in a commit of its own).
patch("2c2a77f9f3ab", "pi-coding-agent/docs/models-configuration.md", [
    ["| `supportsMidConvoSystemMessages` | boolean | `false` | Whether the exact model accepts system messages after the conversation has started. When false, later system messages are folded into the leading system message. |\n",
     "| `supportsMidConvoSystemMessages` | boolean | `false` | Whether the exact model accepts system messages after the conversation has started. When false, later system messages are folded into the leading system message. |\n| `supportsStrictMode` | boolean | `false` | Whether the model supports strict (schema-constrained) function tools. |\n"],
]);
// END backport 2c2a77f9f3ab

// BEGIN backport 2c2a77f9f3ab
// 2c2a77f9f3ab (#12148), the behavior: buildChatPayload passes the model's compat.supportsStrictMode (default false) to toFunctionTools, which hands it to
// resolveJsonSchemaStrictSampling where it passed true. PiG's vendored runtime carries pi-ai's mistral-conversations as a bridge stub that runs in PiG's host
// (ai/mistral.go, which has the change), so the file is the installed Pi's only: the parity oracle is Pi with the fix.
// not carried: packages/ai/test/mistral-tool-schema.test.ts (a test of the TypeScript source) and packages/ai/CHANGELOG.md (Pi's changelog entry): the package ships neither.
// not carried: packages/ai/src/api/mistral-conversations.ts, packages/ai/src/providers/compat-schema.ts (TypeScript sources): the compiled output above carries the change.
packageFile("2c2a77f9f3ab", "pi-ai/api/mistral-conversations.js", [
    [`        payload.tools = toFunctionTools(currentTools);
`, `        ${note("2c2a77f9f3ab").trim()}
        payload.tools = toFunctionTools(currentTools, model.compat?.supportsStrictMode ?? false);
`],
    [`function toFunctionTools(tools) {
    return tools.map((tool) => {
        const strict = resolveJsonSchemaStrictSampling(tool, true);
`, `function toFunctionTools(tools, supportsStrictMode) {
    return tools.map((tool) => {
        const strict = resolveJsonSchemaStrictSampling(tool, supportsStrictMode);
`],
]);
// END backport 2c2a77f9f3ab

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

// BEGIN backport efbcd5adb
// efbcd5adb (#12200): the coding agent moves its exact pin of undici from 8.12.3 to 8.12.4 (GHSA-mqg3-3v4r-cm32, GHSA-7xg3-fphp-28hg, GHSA-5rm8-gv32-r954, GHSA-9v6h-8rgp-m69f,
// GHSA-3g9m-rv3r-w5w2, GHSA-r4pc-4jcp-7pqc). The lock entry is the one Pi's package-lock.json and packages/coding-agent/install-lock/package-lock.json hold at efbcd5adb; both locks
// change that entry (version, archive, integrity) and the coding agent's range, nothing else, and 8.12.4 declares no dependencies of its own, as 8.12.3 does not.
// The installed Pi bundles undici into its `pi` bundle (the oracle's bundle is made again from the installed copy), and vendor-pi-dist.sh copies the installed undici into
// the vendored runtime (shims/undici), so the oracle and the vendored runtime both run 8.12.4.
// not carried: pi-coding-agent/package.json (`"undici": "8.12.4"`): published ranges follow the pin until the re-pin; the lock the oracle installs names 8.12.4, the manifest on disk 8.12.3.
// not carried: apps/p2/package.json and the CHANGELOG: not part of the installed package.
dependency("efbcd5adb", "pi-coding-agent", {
    name: "undici",
    from: "8.12.3",
    to: "8.12.4",
    resolved: "https://registry.npmjs.org/undici/-/undici-8.12.4.tgz",
    integrity: "sha512-8Mx+uNKKER2bA4AdtedkqRbwUCIJFr2nJckXRe5yrDqzePqwLP7VNcctN8/XdmDSbF7mS+jJKIszlL+vbam1VA==",
    dependencies: {},
});
// END backport efbcd5adb

// BEGIN backport ac78a6068b38
// ac78a6068b38 (#12117, #11264): two direct terminal writes that drew over the TUI go. parseChangelog (startup notes of pi's and of extension packages' changelogs, /changelog) returns no entries
// for a changelog it cannot read without printing `Warning: Could not parse changelog`; core/model-resolver.js loses restoreModelFromSession (it printed with console.log and console.error, had no
// caller in the repository and is not exported from the package) and the chalk import only it used.
// PiG's vendored runtime carries both files, so the Node extension runtime and the oracle's `pi` run Pi's later behavior.
// not carried: pi-coding-agent/utils/changelog.d.ts, pi-coding-agent/core/model-resolver.d.ts (the removed declaration of restoreModelFromSession): declaration files; no backport table patches one, and extensions/sdk-ts/pi-pin.d.ts stays the pinned build's.
// not carried: packages/coding-agent/test/changelog.test.ts (a test of the TypeScript source), packages/coding-agent/CHANGELOG.md (Pi's changelog entry) and the TypeScript sources utils/changelog.ts, core/model-resolver.ts: the package ships none of them.
patch("ac78a6068b38", "pi-coding-agent/utils/changelog.js", [
    [`    catch (error) {
        console.error(\`Warning: Could not parse changelog: \${error}\`);
        return [];
    }
`, `    catch {
        ${note("ac78a6068b38").trim()}
        // Shown in the TUI (startup notes, /changelog), where printing would draw over the screen. An unreadable
        // changelog shows no notes instead.
        return [];
    }
`],
]);
patch("ac78a6068b38", "pi-coding-agent/core/model-resolver.js", [
    [`import chalk from "chalk";
`, ""],
    [`/**
 * Restore model from session, with fallback to available models
 */
export async function restoreModelFromSession(savedProvider, savedModelId, currentModel, shouldPrintMessages, modelRuntime) {
    const restoredModel = modelRuntime.getModel(savedProvider, savedModelId);
    // Check if restored model exists and still has auth configured
    const hasConfiguredAuth = restoredModel ? modelRuntime.hasConfiguredAuth(restoredModel.provider) : false;
    if (restoredModel && hasConfiguredAuth) {
        if (shouldPrintMessages) {
            console.log(chalk.dim(\`Restored model: \${savedProvider}/\${savedModelId}\`));
        }
        return { model: restoredModel, fallbackMessage: undefined };
    }
    // Model not found or no API key - fall back
    const reason = !restoredModel ? "model no longer exists" : "no auth configured";
    if (shouldPrintMessages) {
        console.error(chalk.yellow(\`Warning: Could not restore model \${savedProvider}/\${savedModelId} (\${reason}).\`));
    }
    // If we already have a model, use it as fallback
    if (currentModel) {
        if (shouldPrintMessages) {
            console.log(chalk.dim(\`Falling back to: \${currentModel.provider}/\${currentModel.id}\`));
        }
        return {
            model: currentModel,
            fallbackMessage: \`Could not restore model \${savedProvider}/\${savedModelId} (\${reason}). Using \${currentModel.provider}/\${currentModel.id}.\`,
        };
    }
    // Try to find any available model
    const availableModels = [...modelRuntime.getAvailableSnapshot()];
    if (availableModels.length > 0) {
        // Try to find a default model from known providers
        let fallbackModel;
        for (const provider of Object.keys(defaultModelPerProvider)) {
            const defaultId = defaultModelPerProvider[provider];
            const match = availableModels.find((m) => m.provider === provider && m.id === defaultId);
            if (match) {
                fallbackModel = match;
                break;
            }
        }
        // If no default found, use first available
        if (!fallbackModel) {
            fallbackModel = availableModels[0];
        }
        if (shouldPrintMessages) {
            console.log(chalk.dim(\`Falling back to: \${fallbackModel.provider}/\${fallbackModel.id}\`));
        }
        return {
            model: fallbackModel,
            fallbackMessage: \`Could not restore model \${savedProvider}/\${savedModelId} (\${reason}). Using \${fallbackModel.provider}/\${fallbackModel.id}.\`,
        };
    }
    // No models available
    return { model: undefined, fallbackMessage: undefined };
}
`, ""],
]);
// END backport ac78a6068b38

// BEGIN backport c47ad0432bb8
// c47ad0432bb8 (#12115, #11264): the diagnostics a session replacement's runtime factory returns are reported once: AgentSessionRuntime.takeUnreportedDiagnostics() returns the current diagnostics not returned yet (same type and message
// as one returned before: left out) and marks them; main.js takes the startup diagnostics with it; each mode calls it in its rebind callback (interactive: in the chat through showRuntimeDiagnostics; print, JSON and RPC: stderr, never stdout).
// Pi's commit moves main.ts's reportDiagnostics to the new file modes/diagnostics-stderr.ts. The backport table patches existing files only, so writeDiagnosticsToStderr is added to core/settings-diagnostics.js
// (main.js already imports deduplicateDiagnostics from it; the modes import from it too). The function, its output and its callers are Pi's.
// not carried: pi-coding-agent/modes/diagnostics-stderr.js (a new file; see above: its function is in core/settings-diagnostics.js) and the declarations (core/agent-session-runtime.d.ts takeUnreportedDiagnostics): declaration files; no backport table patches one, and extensions/sdk-ts/pi-pin.d.ts stays the pinned build's.
// not carried: packages/coding-agent/test/agent-session-runtime-diagnostics.test.ts, test/rpc-replacement-diagnostics.test.ts, test/suite/regressions/7829-invalid-settings-warning.test.ts (tests of the TypeScript source) and packages/coding-agent/CHANGELOG.md (Pi's changelog entry): the package ships none of them.
patch("c47ad0432bb8", "pi-coding-agent/core/agent-session-runtime.js", [
    [`    _modelFallbackMessage;
    constructor(_session, _services,`, `    _modelFallbackMessage;
    ${note("c47ad0432bb8").trim()}
    /** \`type\\0message\` of every diagnostic takeUnreportedDiagnostics() returned so far. */
    reportedDiagnosticKeys = new Set();
    constructor(_session, _services,`],
    [`    get modelFallbackMessage() {
        return this._modelFallbackMessage;
    }
`, `    get modelFallbackMessage() {
        return this._modelFallbackMessage;
    }
    /**
     * Return the current runtime's diagnostics that were not reported yet, and mark them reported.
     *
     * The host calls this at startup and after each session replacement (/new, /resume, /fork,
     * import), whose runtime collects its diagnostics again. A diagnostic with the same type and
     * message as one returned before is left out, so an unchanged settings warning is not repeated
     * on every replacement.
     */
    takeUnreportedDiagnostics() {
        return this._diagnostics.filter((diagnostic) => {
            const key = \`\${diagnostic.type}\\0\${diagnostic.message}\`;
            if (this.reportedDiagnosticKeys.has(key))
                return false;
            this.reportedDiagnosticKeys.add(key);
            return true;
        });
    }
`],
]);
patch("c47ad0432bb8", "pi-coding-agent/core/settings-diagnostics.js", [
    [`export function collectSettingsDiagnostics(settingsManager) {
`, `import chalk from "chalk";
${note("c47ad0432bb8").trim()}
/** Print each diagnostic to stderr as \`Error: ...\` (red), \`Warning: ...\` (yellow) or a dim info line. */
export function writeDiagnosticsToStderr(diagnostics) {
    for (const diagnostic of diagnostics) {
        const color = diagnostic.type === "error" ? chalk.red : diagnostic.type === "warning" ? chalk.yellow : chalk.dim;
        const prefix = diagnostic.type === "error" ? "Error: " : diagnostic.type === "warning" ? "Warning: " : "";
        console.error(color(\`\${prefix}\${diagnostic.message}\`));
    }
}
export function collectSettingsDiagnostics(settingsManager) {
`],
]);
patch("c47ad0432bb8", "pi-coding-agent/main.js", [
    ['import { collectSettingsDiagnostics, deduplicateDiagnostics } from "./core/settings-diagnostics.js";', 'import { collectSettingsDiagnostics, deduplicateDiagnostics, writeDiagnosticsToStderr } from "./core/settings-diagnostics.js";'],
    [`function reportDiagnostics(diagnostics) {
    for (const diagnostic of diagnostics) {
        const color = diagnostic.type === "error" ? chalk.red : diagnostic.type === "warning" ? chalk.yellow : chalk.dim;
        const prefix = diagnostic.type === "error" ? "Error: " : diagnostic.type === "warning" ? "Warning: " : "";
        console.error(color(\`\${prefix}\${diagnostic.message}\`));
    }
}
`, ""],
    [`    const startupDiagnostics = deduplicateDiagnostics([...startupSettingsDiagnostics, ...runtime.diagnostics]);
`, `    ${note("c47ad0432bb8").trim()}
    // Taking them marks them reported, so the modes do not report them again after a session replacement.
    const startupDiagnostics = deduplicateDiagnostics([
        ...startupSettingsDiagnostics,
        ...runtime.takeUnreportedDiagnostics(),
    ]);
`],
    [`    if (parsed.help) {
        reportDiagnostics(startupDiagnostics);
`, `    if (parsed.help) {
        writeDiagnosticsToStderr(startupDiagnostics);
`],
    [`    if (parsed.listModels !== undefined) {
        reportDiagnostics(startupDiagnostics);
`, `    if (parsed.listModels !== undefined) {
        writeDiagnosticsToStderr(startupDiagnostics);
`],
    [`    if (appMode !== "interactive" || hasRuntimeErrors) {
        reportDiagnostics(startupDiagnostics);
`, `    if (appMode !== "interactive" || hasRuntimeErrors) {
        writeDiagnosticsToStderr(startupDiagnostics);
`],
]);
patch("c47ad0432bb8", "pi-coding-agent/modes/print-mode.js", [
    ['import { killTrackedDetachedChildren } from "../utils/shell.js";\n', 'import { writeDiagnosticsToStderr } from "../core/settings-diagnostics.js";\nimport { killTrackedDetachedChildren } from "../utils/shell.js";\n'],
    [`    runtimeHost.setRebindSession(async () => {
        await rebindSession();
    });
`, `    runtimeHost.setRebindSession(async () => {
        await rebindSession();
        ${note("c47ad0432bb8").trim()}
        writeDiagnosticsToStderr(runtimeHost.takeUnreportedDiagnostics());
    });
`],
]);
patch("c47ad0432bb8", "pi-coding-agent/modes/rpc/rpc-mode.js", [
    ['import { killTrackedDetachedChildren } from "../../utils/shell.js";\n', 'import { writeDiagnosticsToStderr } from "../../core/settings-diagnostics.js";\nimport { killTrackedDetachedChildren } from "../../utils/shell.js";\n'],
    [`    runtimeHost.setRebindSession(async () => {
        await rebindSession();
    });
`, `    runtimeHost.setRebindSession(async () => {
        await rebindSession();
        ${note("c47ad0432bb8").trim()}
        // stderr, never stdout: stdout carries the RPC protocol.
        writeDiagnosticsToStderr(runtimeHost.takeUnreportedDiagnostics());
    });
`],
]);
patch("c47ad0432bb8", "pi-coding-agent/modes/interactive/interactive-mode.js", [
    [`            await this.rebindCurrentSession({ renderBeforeBind: true });
            this.themeController.applyFromSettings();
`, `            await this.rebindCurrentSession({ renderBeforeBind: true });
            this.themeController.applyFromSettings();
            ${note("c47ad0432bb8").trim()}
            this.showRuntimeDiagnostics(this.runtimeHost.takeUnreportedDiagnostics());
`],
    [`        for (const diagnostic of startupDiagnostics ?? []) {
            if (diagnostic.type === "error") {
                this.showError(diagnostic.message);
            }
            else if (diagnostic.type === "warning") {
                this.showWarning(diagnostic.message);
            }
            else {
                this.showStatus(diagnostic.message);
            }
        }
`, `        this.showRuntimeDiagnostics(startupDiagnostics ?? []);
`],
    [`    async rebindCurrentSession(options = {}) {
`, `    showRuntimeDiagnostics(diagnostics) {
        for (const diagnostic of diagnostics) {
            if (diagnostic.type === "error") {
                this.showError(diagnostic.message);
            }
            else if (diagnostic.type === "warning") {
                this.showWarning(diagnostic.message);
            }
            else {
                this.showStatus(diagnostic.message);
            }
        }
    }
    async rebindCurrentSession(options = {}) {
`],
]);
// END backport c47ad0432bb8

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

// BEGIN backport 6aab551a79ba
// 6aab551a79ba (#12232): print mode (text and --mode json) subscribes the event writer and the stdout backpressure before bindExtensions(), so the events session_start handlers emit
// (entry_appended for pi.appendEntry, session_info_changed, a custom message's message_start/message_end) reach the JSON output at startup and after an extension replaces the session.
// not carried: packages/coding-agent/test/suite/print-mode-session-start-events.test.ts and packages/coding-agent/CHANGELOG.md: the package ships neither.
patch("6aab551a79ba", "pi-coding-agent/modes/print-mode.js", [
    [`        session = runtimeHost.session;
        await session.bindExtensions({`, `        session = runtimeHost.session;
        ${note("6aab551a79ba").trim()}
        // Subscribe before binding so events emitted by session_start handlers (e.g. appended entries) reach the JSON output.
        unsubscribe?.();
        unsubscribeBackpressure?.();
        unsubscribe = session.subscribe((event) => {
            if (mode === "json") {
                writeRawStdout(\`\${JSON.stringify(toJsonEvent(event))}\\n\`);
            }
        });
        unsubscribeBackpressure =
            mode === "json"
                ? session.agent.subscribe(async () => {
                    await waitForRawStdoutBackpressure();
                })
                : undefined;
        await session.bindExtensions({`],
    [`        });
        unsubscribe?.();
        unsubscribeBackpressure?.();
        unsubscribe = session.subscribe((event) => {
            if (mode === "json") {
                writeRawStdout(\`\${JSON.stringify(toJsonEvent(event))}\\n\`);
            }
        });
        unsubscribeBackpressure =
            mode === "json"
                ? session.agent.subscribe(async () => {
                    await waitForRawStdoutBackpressure();
                })
                : undefined;
    };
`, `        });
    };
`],
]);
// END backport 6aab551a79ba

// BEGIN backport e03dca7a3843
// e03dca7a3843 (#12231): AgentSession.reload() restarts the extensions (session_start with reason reload) when the host called bindExtensions(), even bindExtensions({}), not only when it set a UI context, command actions, a shutdown handler or an error listener.
// not carried: pi-coding-agent/core/agent-session.d.ts (the private field _extensionsBound): declarations follow the pin. packages/coding-agent/test/agent-session-runtime-events.test.ts and packages/coding-agent/CHANGELOG.md: the package ships neither.
patch("e03dca7a3843", "pi-coding-agent/core/agent-session.js", [
    ["    _extensionErrorUnsubscriber;\n", `    _extensionErrorUnsubscriber;
    ${note("e03dca7a3843").trim()}
    // Set by bindExtensions(): reload() restarts extensions only if the host started them.
    _extensionsBound = false;
`],
    [`        this._applyExtensionBindings(this._extensionRunner);
        await this._extensionRunner.emit(this._sessionStartEvent);`, `        this._extensionsBound = true;
        this._applyExtensionBindings(this._extensionRunner);
        await this._extensionRunner.emit(this._sessionStartEvent);`],
    [`        const hasBindings = this._extensionUIContext ||
            this._extensionCommandContextActions ||
            this._extensionShutdownHandler ||
            this._extensionErrorListener;
        if (hasBindings) {`, `        if (this._extensionsBound) {`],
]);
// END backport e03dca7a3843

// BEGIN backport bb1a2b3542b5
// bb1a2b3542b5 (#12229, closes #12124): the Codex WebSocket 60-minute connection limit, as the code `websocket_connection_limit_reached` or as the text "Responses websocket
// connection limit reached", is a retryable provider error (the classifier that pi-ai's SDK, the coding agent, durable and compaction and the oracle test of ai.IsRetryableAssistantError run).
patch("bb1a2b3542b5", "pi-ai/utils/retry.js", [
    [`    "websocket.?error",
`, `    "websocket.?error",
    ${note("bb1a2b3542b5").trim()}
    // Codex 60-minute WebSocket lifetime limit, as code \`websocket_connection_limit_reached\` or as
    // text "Responses websocket connection limit reached". A retry opens a fresh connection (#12124).
    "websocket.?connection.?limit.?reached",
`],
]);
// END backport bb1a2b3542b5

// BEGIN backport bb1a2b3542b5
// bb1a2b3542b5 (#12229, closes #12124): new requests stop reusing a cached Codex WebSocket after 45 minutes instead of 55. PiG's vendored runtime carries pi-ai's
// openai-codex-responses as a bridge stub that runs in PiG's host (ai/openai_codex_websocket.go codexWebSocketMaxAge, which has the change), so the file is the installed Pi's only.
packageFile("bb1a2b3542b5", "pi-ai/api/openai-codex-responses.js", [
    [`const SESSION_WEBSOCKET_MAX_AGE_MS = 55 * 60 * 1000;
`, `// The server closes a socket after 60 minutes, even mid-response, and the in-flight response is lost.
// New requests open a fresh socket after 45 minutes, leaving 15 minutes for long responses.
${note("bb1a2b3542b5").trim()}
const SESSION_WEBSOCKET_MAX_AGE_MS = 45 * 60 * 1000;
`],
]);
// END backport bb1a2b3542b5

// BEGIN backport bb1a2b3542b5
// bb1a2b3542b5: Pi's TypeScript source of the Codex provider, which the divergence guard reads from the source mirror, with the same change as Pi's commit makes to it.
mirrorSource("bb1a2b3542b5", "packages/ai/src/api/openai-codex-responses.ts", [
    [`const SESSION_WEBSOCKET_MAX_AGE_MS = 55 * 60 * 1000;
`, `// The server closes a socket after 60 minutes, even mid-response, and the in-flight response is lost.
// New requests open a fresh socket after 45 minutes, leaving 15 minutes for long responses.
const SESSION_WEBSOCKET_MAX_AGE_MS = 45 * 60 * 1000;
`],
]);
// END backport bb1a2b3542b5

// BEGIN backport c8c456c85c6e
// c8c456c85c6e (#12234): a fallback to another model ("Could not restore model x/y. Using a/b") is a warning among the runtime's diagnostics, so every mode reports it at startup and
// after each replacement. Without any model the message is login guidance, which interactive mode still receives as its option; with a model it does not, so a fallback is shown once.
patch("c8c456c85c6e", "pi-coding-agent/main.js", [
    [`            created.session.setThinkingLevel(created.session.thinkingLevel);
        }
        return {
            ...created,
`, `            created.session.setThinkingLevel(created.session.thinkingLevel);
        }
        ${note("c8c456c85c6e").trim()}
        // "Could not restore model x/y. Using a/b." is set again on /resume and /fork. As a diagnostic, every
        // mode reports it at startup and after each replacement. Without any model, the message is login
        // guidance instead: interactive mode shows it at startup, the other modes exit with it below.
        if (created.modelFallbackMessage && created.session.model) {
            diagnostics.push({ type: "warning", message: created.modelFallbackMessage });
        }
        return {
            ...created,
`],
    [`            startupDiagnostics,
            modelFallbackMessage,
`, `            startupDiagnostics,
            // A fallback to another model is already in startupDiagnostics.
            modelFallbackMessage: session.model ? undefined : modelFallbackMessage,
`],
]);
// END backport c8c456c85c6e
// not carried: pi-coding-agent/utils/deprecation.js (deleted file): warnDeprecation had no caller and the package does not export it; a backport table patches files and cannot delete one, and nothing observable reads it.

// BEGIN backport 18e99051be24
// 18e99051be24 (#12233, #11264): a throwing pi.events handler is reported like other extension errors (runtime.reportError, which the runner binds in its constructor and which drops the error once the runner is stale), not printed with console.error over the interactive TUI.
// not carried: pi-coding-agent/core/extensions/types.d.ts (ExtensionRuntimeState.reportError): declarations follow the pin. packages/coding-agent/test/suite/regressions/11264-pi-events-handler-errors.test.ts and packages/coding-agent/CHANGELOG.md: the package ships neither.
patch("18e99051be24", "pi-coding-agent/core/extensions/loader.js", [
    [`            eventBusUnsubscribers.add(trackedUnsubscribe);
            return trackedUnsubscribe;
        },
`, `            eventBusUnsubscribers.add(trackedUnsubscribe);
            return trackedUnsubscribe;
        },
        ${note("18e99051be24").trim()}
        // Nothing can show an error yet. The runner replaces this when it is created.
        reportError: () => { },
`],
    [`                const unsubscribe = runtime.trackEventBusSubscription(eventBus.on(channel, handler));`, `                // Report a throwing handler like other extension errors. The event bus would print it with console.error,
                // which draws over the interactive TUI (#11264).
                const reportingHandler = async (data) => {
                    try {
                        await handler(data);
                    }
                    catch (error) {
                        runtime.reportError({
                            extensionPath: extension.path,
                            event: \`events:\${channel}\`,
                            error: error instanceof Error ? error.message : String(error),
                            stack: error instanceof Error ? error.stack : undefined,
                        });
                    }
                };
                const unsubscribe = runtime.trackEventBusSubscription(eventBus.on(channel, reportingHandler));`],
]);
patch("18e99051be24", "pi-coding-agent/core/extensions/runner.js", [
    [`        this.runtime = runtime;
        this.uiContext = noOpUIContext;`, `        this.runtime = runtime;
        ${note("18e99051be24").trim()}
        // A handler still running when its session is replaced must not report into the replacement's UI.
        this.runtime.reportError = (error) => {
            if (!this.staleMessage)
                this.emitError(error);
        };
        this.uiContext = noOpUIContext;`],
]);
// END backport 18e99051be24

// BEGIN backport b7bb7ca905b8
// b7bb7ca905b8 (#11968, refs #9549): fullscreen scroll inputs (wheel, page and line keys, top and bottom, prompt jumps, scrollbar drag, selection auto-scroll) ask for a view-only frame that reuses the previous frame's component output, and Container.render and Box.render return the same array while their children's lines are unchanged; the components that edited a Container result (the user and assistant messages, the Radius login menu) copy it first.
// not carried: the .d.ts declarations (Container.render returns a readonly array, TUI.requestViewRender is protected): declarations follow the pin. packages/tui/test and packages/coding-agent/test (tests of the TypeScript source) and the packages' CHANGELOG.md: the packages ship none of them.
// not carried: the TypeScript sources of packages/tui and packages/coding-agent: the compiled output below carries the change.
patch("b7bb7ca905b8", "pi-tui/utils.js", [
    [`    return pad ? result + " ".repeat(Math.max(0, maxWidth - visibleWidth)) : result;
}
/**
 * Calculate the terminal width of a single grapheme cluster.
 * Based on code from the string-width library, but includes a possible-emoji
 * check to avoid running the RGI_Emoji regex unnecessarily.
 */
function graphemeWidth(segment) {
    if (segment.length === 1) {
        const code = segment.charCodeAt(0);
        if (code >= 0x20 && code <= 0x7e)
            return 1;
        if (code === 0x09)
            return 3;
    }
    // Some marks occupy cells even without a base character.
    if (terminalSpacingMarkRegex.test(segment)) {
        return [...segment].length;
`, `    return pad ? result + " ".repeat(Math.max(0, maxWidth - visibleWidth)) : result;
}
/**
 ${note("b7bb7ca905b8").trim()}
 * Whether a UTF-16 unit always forms a grapheme cluster on its own: printable ASCII, Latin,
 * punctuation, arrows, box drawing, symbols, kana, CJK ideographs and fullwidth forms. These ranges
 * hold no combining marks, joiners, variation selectors, Hangul jamo or surrogates, so no Unicode
 * grapheme rule can join two of them.
 */
function isStandaloneGraphemeUnit(code) {
    if (code >= 0x20 && code <= 0x7e)
        return true;
    if (code >= 0xa0 && code <= 0x2ff)
        return code !== 0xad;
    if (code >= 0x2010 && code <= 0x2bff) {
        // Line and bidi controls, invisible operators and combining marks for symbols.
        return (!(code >= 0x2028 && code <= 0x202e) &&
            !(code >= 0x2060 && code <= 0x206f) &&
            !(code >= 0x20d0 && code <= 0x20ff));
    }
    return ((code >= 0x3040 && code <= 0x3096) ||
        (code >= 0x30a0 && code <= 0x30ff) ||
        (code >= 0x4e00 && code <= 0x9fff) ||
        (code >= 0xff01 && code <= 0xff60));
}
function isStandaloneGraphemeRun(text) {
    for (let i = 0; i < text.length; i++) {
        if (!isStandaloneGraphemeUnit(text.charCodeAt(i)))
            return false;
    }
    return true;
}
/**
 * Segment a run that contains complex units. A standalone unit whose neighbours are standalone too
 * is its own cluster, since no grapheme rule joins two standalone units. Only the stretches between
 * such units go through Intl.Segmenter; cutting there is safe because both cut edges are boundaries.
 */
function* segmentGraphemes(text) {
    const standalone = (index) => index < 0 || index >= text.length || isStandaloneGraphemeUnit(text.charCodeAt(index));
    let complexStart = -1;
    for (let i = 0; i < text.length; i++) {
        if (standalone(i) && standalone(i - 1) && standalone(i + 1)) {
            if (complexStart >= 0) {
                for (const { segment } of graphemeSegmenter.segment(text.slice(complexStart, i)))
                    yield segment;
                complexStart = -1;
            }
            yield text[i];
        }
        else if (complexStart < 0) {
            complexStart = i;
        }
    }
    if (complexStart >= 0) {
        for (const { segment } of graphemeSegmenter.segment(text.slice(complexStart)))
            yield segment;
    }
}
/**
 * Grapheme clusters of a text run without ANSI codes. Standalone units are split per character:
 * Intl.Segmenter costs about 15x more than the slicing it serves, which made one non-ASCII
 * character slow down every row of a fullscreen frame.
 */
function graphemesOf(text) {
    return isStandaloneGraphemeRun(text) ? text : segmentGraphemes(text);
}
// Widths of single non-ASCII BMP characters; bounded by the 64K code units.
const singleUnitWidths = new Map();
/**
 * Calculate the terminal width of a single grapheme cluster.
 * Based on code from the string-width library, but includes a possible-emoji
 * check to avoid running the RGI_Emoji regex unnecessarily.
 */
function graphemeWidth(segment) {
    if (segment.length !== 1)
        return clusterWidth(segment);
    const code = segment.charCodeAt(0);
    if (code >= 0x20 && code <= 0x7e)
        return 1;
    if (code === 0x09)
        return 3;
    let width = singleUnitWidths.get(code);
    if (width === undefined) {
        width = clusterWidth(segment);
        singleUnitWidths.set(code, width);
    }
    return width;
}
function clusterWidth(segment) {
    // Some marks occupy cells even without a base character.
    if (terminalSpacingMarkRegex.test(segment)) {
        return [...segment].length;
`],
    [`        let textEnd = i;
        while (textEnd < line.length && !extractAnsiCode(line, textEnd))
            textEnd++;
        for (const { segment } of graphemeSegmenter.segment(line.slice(i, textEnd))) {
            const width = graphemeWidth(segment);
            if (width > 0 && column >= currentCol && column < currentCol + width) {
                return { start: currentCol, end: currentCol + width };
`, `        let textEnd = i;
        while (textEnd < line.length && !extractAnsiCode(line, textEnd))
            textEnd++;
        const text = line.slice(i, textEnd);
        // Printable ASCII is one cell per character.
        if (isPrintableAscii(text)) {
            if (column >= currentCol && column < currentCol + text.length)
                return { start: column, end: column + 1 };
            currentCol += text.length;
            i = textEnd;
            continue;
        }
        for (const segment of graphemesOf(text)) {
            const width = graphemeWidth(segment);
            if (width > 0 && column >= currentCol && column < currentCol + width) {
                return { start: currentCol, end: currentCol + width };
`],
    [`        let textEnd = i;
        while (textEnd < line.length && !extractAnsiCode(line, textEnd))
            textEnd++;
        for (const { segment } of graphemeSegmenter.segment(line.slice(i, textEnd))) {
            const w = graphemeWidth(segment);
            const inRange = currentCol >= startCol && currentCol < endCol;
            const fits = !strict || currentCol + w <= endCol;
`, `        let textEnd = i;
        while (textEnd < line.length && !extractAnsiCode(line, textEnd))
            textEnd++;
        const text = line.slice(i, textEnd);
        // Printable ASCII is one cell per character.
        if (isPrintableAscii(text)) {
            const from = Math.max(0, startCol - currentCol);
            const to = Math.min(text.length, endCol - currentCol);
            if (to > from) {
                result += pendingAnsi + text.slice(from, to);
                pendingAnsi = "";
                resultWidth += to - from;
            }
            currentCol += text.length;
            i = textEnd;
            if (currentCol >= endCol)
                break;
            continue;
        }
        for (const segment of graphemesOf(text)) {
            const w = graphemeWidth(segment);
            const inRange = currentCol >= startCol && currentCol < endCol;
            const fits = !strict || currentCol + w <= endCol;
`],
]);
patch("b7bb7ca905b8", "pi-tui/tui.js", [
    [`    }
    return undefined;
}
/**
 * Container - a component that contains other components
 */
export class Container {
    children = [];
    mouseLayout;
    addChild(component) {
        this.children.push(component);
    }
`, `    }
    return undefined;
}
${note("b7bb7ca905b8").trim()}
function sameLines(a, b) {
    if (a === b)
        return true;
    if (a.length !== b.length)
        return false;
    for (let index = 0; index < a.length; index++) {
        if (a[index] !== b[index])
            return false;
    }
    return true;
}
/**
 * Render children and concatenate their lines.
 *
 * Returns \`previous\` unchanged when the children and their lines are unchanged. Unchanged subtrees
 * then keep array identity, so every ancestor container skips its copy too. Without this, each
 * frame of a long fullscreen transcript copied every rendered line once per nesting level.
 * Callers must treat the returned lines as read-only, like any component's cached render output.
 */
export function renderChildLines(children, width, previous) {
    const parts = children.map((child) => child.render(width));
    if (previous?.width === width &&
        previous.children.length === children.length &&
        children.every((child, index) => child === previous.children[index] && sameLines(parts[index], previous.parts[index]))) {
        return previous;
    }
    const lines = [];
    for (const part of parts) {
        for (const line of part)
            lines.push(line);
    }
    return { width, children: [...children], parts, lines };
}
/** Dispatch a mouse event to the child of a vertical child layout that covers its row. */
export function dispatchMouseToChildLines(layout, event) {
    let childY = 0;
    for (let index = 0; index < layout.children.length; index++) {
        const childHeight = layout.parts[index].length;
        if (event.y >= childY && event.y < childY + childHeight) {
            return dispatchMouseEvent(layout.children[index], { ...event, y: event.y - childY, height: childHeight });
        }
        childY += childHeight;
    }
    return undefined;
}
/**
 * Container - a component that contains other components
 */
export class Container {
    children = [];
    childLines;
    addChild(component) {
        this.children.push(component);
    }
`],
    [`    handleMouse(event) {
        if (event.y < 0 || event.y >= event.height)
            return undefined;
        const mouseChildren = this.mouseLayout?.width === event.width
            ? this.mouseLayout.children
            : this.children.map((component) => ({ component, height: component.render(event.width).length }));
        let childY = 0;
        for (const { component: child, height: childHeight } of mouseChildren) {
            if (event.y >= childY && event.y < childY + childHeight) {
                const result = dispatchMouseEvent(child, {
                    ...event,
                    y: event.y - childY,
                    height: childHeight,
                });
                if (result?.focus && this.handleInput)
                    return { ...result, focusTarget: this };
                return result;
            }
            childY += childHeight;
        }
        return undefined;
    }
    render(width) {
        const lines = [];
        const mouseChildren = [];
        for (const child of this.children) {
            const childLines = child.render(width);
            mouseChildren.push({ component: child, height: childLines.length });
            for (const line of childLines) {
                lines.push(line);
            }
        }
        this.mouseLayout = { width, children: mouseChildren };
        return lines;
    }
}
/**
`, `    handleMouse(event) {
        if (event.y < 0 || event.y >= event.height)
            return undefined;
        const layout = this.childLines?.width === event.width
            ? this.childLines
            : renderChildLines(this.children, event.width, undefined);
        const result = dispatchMouseToChildLines(layout, event);
        if (result?.focus && this.handleInput)
            return { ...result, focusTarget: this };
        return result;
    }
    render(width) {
        this.childLines = renderChildLines(this.children, width, this.childLines);
        return this.childLines.lines;
    }
}
/**
`],
    [`    /** Global callback for debug key (Shift+Ctrl+D). Called before input is forwarded to focused component. */
    onDebug;
    renderRequested = false;
    immediateRenderScheduled = false;
    renderTimer;
    lastRenderAt = 0;
`, `    /** Global callback for debug key (Shift+Ctrl+D). Called before input is forwarded to focused component. */
    onDebug;
    renderRequested = false;
    /**
     * Whether a render request since the last frame may have changed component output. Only
     * \`requestViewRender()\` leaves it false, so a renderer may reuse the previous frame's output.
     */
    contentChanged = true;
    immediateRenderScheduled = false;
    renderTimer;
    lastRenderAt = 0;
`],
    [`        this.setFocusInternal({ component, overlayFocusRestore: "clear" });
    }
    setFocusInternal({ component, overlayFocusRestore, }) {
        const previousFocus = this.focusedComponent;
        let nextFocus = component;
        const previousFocusedOverlay = previousFocus
`, `        this.setFocusInternal({ component, overlayFocusRestore: "clear" });
    }
    setFocusInternal({ component, overlayFocusRestore, }) {
        // Focused components draw a cursor, so a focus change must not reuse earlier output.
        this.contentChanged = true;
        const previousFocus = this.focusedComponent;
        let nextFocus = component;
        const previousFocusedOverlay = previousFocus
`],
    [`    renderNow(force = false) {
        if (force)
            this.resetRenderState();
        this.renderRequested = false;
        this.cancelRenderTimer();
        this.lastRenderAt = performance.now();
`, `    renderNow(force = false) {
        if (force)
            this.resetRenderState();
        this.contentChanged = true;
        this.renderRequested = false;
        this.cancelRenderTimer();
        this.lastRenderAt = performance.now();
`],
    [`            this.requestImmediateRender();
            return;
        }
        if (this.renderRequested)
            return;
        this.renderRequested = true;
        process.nextTick(() => this.scheduleRender());
    }
    requestImmediateRender() {
        this.cancelRenderTimer();
        this.renderRequested = true;
        if (this.immediateRenderScheduled)
`, `            this.requestImmediateRender();
            return;
        }
        this.contentChanged = true;
        this.requestViewRender();
    }
    /** Request a frame for a change that leaves component output unchanged, such as a scroll position. */
    requestViewRender() {
        if (this.renderRequested)
            return;
        this.renderRequested = true;
        process.nextTick(() => this.scheduleRender());
    }
    requestImmediateRender() {
        this.contentChanged = true;
        this.cancelRenderTimer();
        this.renderRequested = true;
        if (this.immediateRenderScheduled)
`],
]);
patch("b7bb7ca905b8", "pi-tui/layout.js", [
    [`    }
    let lines = widths.get(safeWidth);
    if (!lines) {
        lines = component.render(safeWidth);
        widths.set(safeWidth, lines);
    }
    return lines;
`, `    }
    let lines = widths.get(safeWidth);
    if (!lines) {
        ${note("b7bb7ca905b8").trim()}
        lines = context.reusableOutput?.get(component)?.get(safeWidth) ?? component.render(safeWidth);
        widths.set(safeWidth, lines);
    }
    return lines;
`],
    [`    }
    paintScrollbar(box, screen, totalWidth);
}
export function renderLayoutFrame(root, width, height, requestRender) {
    const safeWidth = Math.max(1, Math.floor(width));
    const safeHeight = Math.max(1, Math.floor(height));
    const context = {
        viewport: { width: safeWidth, height: safeHeight },
        renderCache: new Map(),
        requestRender,
        primaryScrollView: undefined,
    };
`, `    }
    paintScrollbar(box, screen, totalWidth);
}
/**
 * Lay out and paint one frame.
 *
 * \`reusableOutput\` is an earlier frame's \`renderCache\`; its output is used instead of calling
 * \`render()\`. Pass it only when no component changed since that frame, e.g. when only a scroll
 * position moved: the frame then costs O(viewport) instead of re-rendering the whole scrollable
 * document. Each frame keeps only the output it used, so reuse never accumulates across frames.
 */
export function renderLayoutFrame(root, width, height, requestRender, reusableOutput) {
    const safeWidth = Math.max(1, Math.floor(width));
    const safeHeight = Math.max(1, Math.floor(height));
    const renderCache = new Map();
    const context = {
        viewport: { width: safeWidth, height: safeHeight },
        renderCache,
        reusableOutput,
        requestRender,
        primaryScrollView: undefined,
    };
`],
    [`        height: safeHeight,
        lines,
        ...(context.primaryScrollView === undefined ? {} : { primaryScrollView: context.primaryScrollView }),
    };
}
function containsPoint(rect, x, y) {
`, `        height: safeHeight,
        lines,
        ...(context.primaryScrollView === undefined ? {} : { primaryScrollView: context.primaryScrollView }),
        renderCache,
    };
}
function containsPoint(rect, x, y) {
`],
]);
patch("b7bb7ca905b8", "pi-tui/tui-alt-screen.js", [
    [`    }
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
`, `    }
    scrollBy(lines) {
        this.getPrimaryScrollView().scrollBy(lines);
        ${note("b7bb7ca905b8").trim()}
        this.requestViewRender();
    }
    scrollToTop() {
        this.getPrimaryScrollView().scrollToStart();
        this.requestViewRender();
    }
    scrollToBottom() {
        this.getPrimaryScrollView().scrollToEnd();
        this.requestViewRender();
    }
    scrollToPrompt(direction) {
        if (!this.currentLayout)
`],
    [`            if (!OSC133_PROMPT_START.test(lines[row] ?? ""))
                continue;
            scrollView.scrollTo(row);
            this.requestRender();
            return;
        }
    }
`, `            if (!OSC133_PROMPT_START.test(lines[row] ?? ""))
                continue;
            scrollView.scrollTo(row);
            this.requestViewRender();
            return;
        }
    }
`],
    [`        if (remaining !== 0 && !seen.has(primary))
            primary.scrollBy(remaining);
        this.updateScrollbarHover(event.x, event.y);
        this.requestRender();
    }
    parseSgrMouseEvent(data) {
        const match = /^\\x1b\\[<(\\d+);(\\d+);(\\d+)([Mm])$/.exec(data);
`, `        if (remaining !== 0 && !seen.has(primary))
            primary.scrollBy(remaining);
        this.updateScrollbarHover(event.x, event.y);
        // Scrolling moves the viewport only. Reusing the previous component output keeps a wheel
        // frame O(viewport); re-rendering a long transcript on every wheel event saturated a core.
        this.requestViewRender();
    }
    parseSgrMouseEvent(data) {
        const match = /^\\x1b\\[<(\\d+);(\\d+);(\\d+)([Mm])$/.exec(data);
`],
    [`        const point = this.getScrollSelectionPoint(scrollView, pointer.x, pointer.y);
        if (point)
            this.updateSelectionFocus(point);
        this.requestRender();
    }
    stopSelectionAutoScroll() {
        if (this.selectionAutoScrollTimer) {
`, `        const point = this.getScrollSelectionPoint(scrollView, pointer.x, pointer.y);
        if (point)
            this.updateSelectionFocus(point);
        // Selection is painted onto the screen after layout, so it needs no new component output.
        this.requestViewRender();
    }
    stopSelectionAutoScroll() {
        if (this.selectionAutoScrollTimer) {
`],
    [`        const width = Math.max(1, this.terminal.columns);
        const height = Math.max(1, this.terminal.rows);
        const root = this.layoutRoot ?? this.implicitScrollView;
        let nextLayout = renderLayoutFrame(root, width, height, () => this.requestRender());
        if (this.refreshSearch(nextLayout)) {
            nextLayout = renderLayoutFrame(root, width, height, () => this.requestRender());
        }
        // Resolve fake cursors before highlighting and compositing, which only track SGR codes
        let screen = this.resolveFakeCursors(nextLayout.lines.map((line) => line.replace(OSC133_ZONE_PREFIX, "")));
`, `        const width = Math.max(1, this.terminal.columns);
        const height = Math.max(1, this.terminal.rows);
        const root = this.layoutRoot ?? this.implicitScrollView;
        const requestViewRender = () => this.requestViewRender();
        const reusableOutput = this.contentChanged ? undefined : this.currentLayout?.renderCache;
        this.contentChanged = false;
        let nextLayout = renderLayoutFrame(root, width, height, requestViewRender, reusableOutput);
        if (this.refreshSearch(nextLayout)) {
            nextLayout = renderLayoutFrame(root, width, height, requestViewRender, nextLayout.renderCache);
        }
        // Resolve fake cursors before highlighting and compositing, which only track SGR codes
        let screen = this.resolveFakeCursors(nextLayout.lines.map((line) => line.replace(OSC133_ZONE_PREFIX, "")));
`],
]);
patch("b7bb7ca905b8", "pi-tui/components/box.js", [
    [`import { dispatchMouseEvent } from "../tui.js";
import { flattenLines, visibleWidth } from "../utils.js";
/**
 * Box component - a container that applies padding and background to all children
`, `${note("b7bb7ca905b8").trim()}
import { dispatchMouseToChildLines, renderChildLines, } from "../tui.js";
import { flattenLines, visibleWidth } from "../utils.js";
/**
 * Box component - a container that applies padding and background to all children
`],
    [`    bgFn;
    // Cache for rendered output
    cache;
    mouseLayout;
    constructor(paddingX = 1, paddingY = 1, bgFn) {
        this.paddingX = paddingX;
        this.paddingY = paddingY;
`, `    bgFn;
    // Cache for rendered output
    cache;
    childLines;
    constructor(paddingX = 1, paddingY = 1, bgFn) {
        this.paddingX = paddingX;
        this.paddingY = paddingY;
`],
    [`    }
    invalidateCache() {
        this.cache = undefined;
    }
    matchCache(width, childLines, bgSample) {
        const cache = this.cache;
        return (!!cache &&
            cache.width === width &&
            cache.bgSample === bgSample &&
            cache.childLines.length === childLines.length &&
            cache.childLines.every((line, i) => line === childLines[i]));
    }
    invalidate() {
        this.invalidateCache();
`, `    }
    invalidateCache() {
        this.cache = undefined;
        this.childLines = undefined;
    }
    invalidate() {
        this.invalidateCache();
`],
    [`        const contentX = event.x - this.paddingX;
        if (contentY < 0 || contentX < 0 || contentX >= contentWidth)
            return undefined;
        const mouseChildren = this.mouseLayout?.width === contentWidth
            ? this.mouseLayout.children
            : this.children.map((component) => ({ component, height: component.render(contentWidth).length }));
        let childY = 0;
        for (const { component: child, height: childHeight } of mouseChildren) {
            if (contentY >= childY && contentY < childY + childHeight) {
                return dispatchMouseEvent(child, {
                    ...event,
                    x: contentX,
                    y: contentY - childY,
                    width: contentWidth,
                    height: childHeight,
                });
            }
            childY += childHeight;
        }
        return undefined;
    }
    render(width) {
        if (this.children.length === 0) {
            return [];
        }
        const contentWidth = Math.max(1, width - this.paddingX * 2);
        const leftPad = " ".repeat(this.paddingX);
        // Render all children. Keep the child lines unpadded: children usually return the same string
        // objects every frame, so the cache check below is a cheap identity comparison per line.
        // Padding here would create new strings that must be compared character by character.
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
        // Check if bgFn output changed by sampling
        const bgSample = this.bgFn ? this.bgFn("test") : undefined;
        // Check cache validity
        if (this.matchCache(width, childLines, bgSample)) {
            return this.cache.lines;
        }
        // Apply background and padding
        const result = [];
`, `        const contentX = event.x - this.paddingX;
        if (contentY < 0 || contentX < 0 || contentX >= contentWidth)
            return undefined;
        const layout = this.childLines?.width === contentWidth
            ? this.childLines
            : renderChildLines(this.children, contentWidth, undefined);
        return dispatchMouseToChildLines(layout, { ...event, x: contentX, y: contentY, width: contentWidth });
    }
    render(width) {
        if (this.children.length === 0) {
            return [];
        }
        const contentWidth = Math.max(1, width - this.paddingX * 2);
        const leftPad = " ".repeat(this.paddingX);
        // Keep the child lines unpadded: unchanged children keep their line array, so the cache check
        // below is one identity comparison. Padding here would create new strings every frame.
        this.childLines = renderChildLines(this.children, contentWidth, this.childLines);
        const childLines = this.childLines.lines;
        if (childLines.length === 0) {
            return [];
        }
        // Check if bgFn output changed by sampling
        const bgSample = this.bgFn ? this.bgFn("test") : undefined;
        // Check cache validity
        const cache = this.cache;
        if (cache?.childLines === childLines && cache.width === width && cache.bgSample === bgSample) {
            return cache.lines;
        }
        // Apply background and padding
        const result = [];
`],
]);
patch("b7bb7ca905b8", "pi-coding-agent/modes/interactive/components/assistant-message.js", [
    [`    timestamp;
    thinkingVisibilityOverrides = new Map();
    markdownSlots = [];
    constructor(message, hideThinkingBlock = false, markdownTheme = getMarkdownTheme(), hiddenThinkingLabel = "Thinking...", outputPad = 1, markdownTransformers = [], messageId, timestamp) {
        super();
        this.hideThinkingBlock = hideThinkingBlock;
`, `    timestamp;
    thinkingVisibilityOverrides = new Map();
    markdownSlots = [];
    ${note("b7bb7ca905b8").trim()}
    zoneLines;
    constructor(message, hideThinkingBlock = false, markdownTheme = getMarkdownTheme(), hiddenThinkingLabel = "Thinking...", outputPad = 1, markdownTransformers = [], messageId, timestamp) {
        super();
        this.hideThinkingBlock = hideThinkingBlock;
`],
    [`        if (this.hasToolCalls || lines.length === 0) {
            return lines;
        }
        lines[0] = OSC133_ZONE_START + lines[0];
        lines[lines.length - 1] = OSC133_ZONE_END + OSC133_ZONE_FINAL + lines[lines.length - 1];
        return lines;
    }
    updateContent(message, isStreaming = this.isStreaming) {
        this.lastMessage = message;
`, `        if (this.hasToolCalls || lines.length === 0) {
            return lines;
        }
        // Container output is shared across frames; copy it once per change instead of mutating it.
        if (this.zoneLines?.source !== lines) {
            const zoned = [...lines];
            zoned[0] = OSC133_ZONE_START + zoned[0];
            zoned[zoned.length - 1] = OSC133_ZONE_END + OSC133_ZONE_FINAL + zoned[zoned.length - 1];
            this.zoneLines = { source: lines, lines: zoned };
        }
        return this.zoneLines.lines;
    }
    updateContent(message, isStreaming = this.isStreaming) {
        this.lastMessage = message;
`],
]);
patch("b7bb7ca905b8", "pi-coding-agent/modes/interactive/components/user-message.js", [
    [`    markdownTransformers;
    messageId;
    timestamp;
    constructor(text, markdownTheme = getMarkdownTheme(), outputPad = 1, markdownTransformers = [], messageId, timestamp) {
        super();
        this.text = text;
`, `    markdownTransformers;
    messageId;
    timestamp;
    ${note("b7bb7ca905b8").trim()}
    zoneLines;
    constructor(text, markdownTheme = getMarkdownTheme(), outputPad = 1, markdownTransformers = [], messageId, timestamp) {
        super();
        this.text = text;
`],
    [`        if (lines.length === 0) {
            return lines;
        }
        lines[0] = OSC133_ZONE_START + lines[0];
        lines[lines.length - 1] = OSC133_ZONE_END + OSC133_ZONE_FINAL + lines[lines.length - 1];
        return lines;
    }
}
`, `        if (lines.length === 0) {
            return lines;
        }
        // Container output is shared across frames; copy it once per change instead of mutating it.
        if (this.zoneLines?.source !== lines) {
            const zoned = [...lines];
            zoned[0] = OSC133_ZONE_START + zoned[0];
            zoned[zoned.length - 1] = OSC133_ZONE_END + OSC133_ZONE_FINAL + zoned[zoned.length - 1];
            this.zoneLines = { source: lines, lines: zoned };
        }
        return this.zoneLines.lines;
    }
}
`],
]);
patch("b7bb7ca905b8", "pi-coding-agent/modes/interactive/components/radius-login-selector.js", [
    [`        this.stopAnimationTimer = startBrandAnimation(tui, () => this.animating);
    }
    render(width) {
        const lines = super.render(width);
        const { label, text } = this.radiusOption;
        const selectedLine = new Text(theme.fg("accent", "→ ") + theme.fg("accent", label), 1, 0).render(width)[0];
        const index = selectedLine === undefined ? -1 : lines.indexOf(selectedLine);
`, `        this.stopAnimationTimer = startBrandAnimation(tui, () => this.animating);
    }
    render(width) {
        ${note("b7bb7ca905b8").trim()}
        // Container output is shared across frames; copy before replacing the animated line.
        const lines = [...super.render(width)];
        const { label, text } = this.radiusOption;
        const selectedLine = new Text(theme.fg("accent", "→ ") + theme.fg("accent", label), 1, 0).render(width)[0];
        const index = selectedLine === undefined ? -1 : lines.indexOf(selectedLine);
`],
]);
// END backport b7bb7ca905b8

// BEGIN backport b513b64d8ac8
// b513b64d8ac8 (#11264): interactive mode installs and refreshes packages with git and npm output captured (a failure carries the last 8000 characters; git and ssh never prompt) and announces each install on stderr until the TUI starts, so no command output draws over it.
// not carried: pi-coding-agent/core/package-manager.d.ts and pi-coding-agent/core/resource-loader.d.ts (PackageCommandOutput, packageCommandOutput, onPackageProgress): declarations follow the pin. packages/coding-agent/test/package-manager.test.ts and packages/coding-agent/CHANGELOG.md: the package ships neither.
patch("b513b64d8ac8", "pi-coding-agent/core/package-manager.js", [
    [`import { spawnProcess, spawnProcessSync } from "../utils/child-process.js";`, `import { spawnProcess, spawnProcessSync, waitForChildProcess } from "../utils/child-process.js";`],
    [`const NETWORK_TIMEOUT_MS = 10000;
`, `const NETWORK_TIMEOUT_MS = 10000;
${note("b513b64d8ac8").trim()}
/** End of a captured command's output kept for its error message. */
const CAPTURED_OUTPUT_TAIL_CHARS = 8000;
/** Output of git, or of ssh run by git, that failed because it could not ask for credentials. */
const GIT_PROMPT_FAILURE = /terminal prompts disabled|could not read (Username|Password)|Permission denied \\(publickey|Host key verification failed|passphrase/i;
const GIT_CREDENTIALS_HINT = "git cannot ask for a password or passphrase while pi runs. Set up a git credential helper or an ssh agent, or install the package once from the shell with \`pi install <source>\`.";
/** Make git and ssh fail instead of prompting on the terminal, which a captured command cannot answer. */
function getNonInteractiveGitEnv(env) {
    return {
        ...env,
        GIT_TERMINAL_PROMPT: "0",
        // A user-defined GIT_SSH_COMMAND is kept: it may be needed to reach the host at all.
        GIT_SSH_COMMAND: env.GIT_SSH_COMMAND ?? "ssh -o BatchMode=yes",
    };
}
`],
    [`    progressCallback;
    constructor(options) {`, `    progressCallback;
    commandOutput;
    constructor(options) {`],
    [`        this.builtinExtensions = options.builtinExtensions ?? [];
    }
    setProgressCallback(callback) {`, `        this.builtinExtensions = options.builtinExtensions ?? [];
        this.commandOutput = options.commandOutput ?? "inherit";
    }
    setProgressCallback(callback) {`],
    [`                    await this.installParsedSource(parsed, resolvedScope);
                    return true;`, `                    await this.withProgress("install", resolvedSource, \`Installing \${resolvedSource}...\`, () => this.installParsedSource(parsed, resolvedScope));
                    return true;`],
    [`    runCommand(command, args, options) {
        return new Promise((resolvePromise, reject) => {`, `    runCommand(command, args, options) {
        if (this.commandOutput === "capture") {
            return this.runCommandCaptured(command, args, options);
        }
        return new Promise((resolvePromise, reject) => {`],
    [`    runCommandSync(command, args) {
        const env = getEnv();`, `    /**
     * Run a command without touching the terminal: output is piped and git and ssh cannot prompt.
     * Only the end of the output is kept, for the error message.
     */
    async runCommandCaptured(command, args, options) {
        const child = spawnProcess(command, args, {
            cwd: options?.cwd,
            stdio: ["ignore", "pipe", "pipe"],
            env: getNonInteractiveGitEnv(getEnv()),
        });
        let output = "";
        const append = (chunk) => {
            output = (output + chunk.toString()).slice(-CAPTURED_OUTPUT_TAIL_CHARS);
        };
        child.stdout.on("data", append);
        child.stderr.on("data", append);
        // Waits for the pipes too, but not for a background process an npm script left holding them.
        const code = await waitForChildProcess(child);
        if (code === 0)
            return;
        const tail = output.trim();
        const hint = command === "git" && GIT_PROMPT_FAILURE.test(tail) ? \`\\n\${GIT_CREDENTIALS_HINT}\` : "";
        throw new Error(\`\${command} \${args.join(" ")} failed with code \${code}\${tail ? \`:\\n\${tail}\` : ""}\${hint}\`);
    }
    runCommandSync(command, args) {
        const env = getEnv();`],
]);
patch("b513b64d8ac8", "pi-coding-agent/core/resource-loader.js", [
    [`            builtinExtensions: [...this.builtinExtensions.keys()],
        });
        this.additionalExtensionPaths`, `            builtinExtensions: [...this.builtinExtensions.keys()],
            commandOutput: options.packageCommandOutput,
        });
        ${note("b513b64d8ac8").trim()}
        this.packageManager.setProgressCallback(options.onPackageProgress);
        this.additionalExtensionPaths`],
]);
patch("b513b64d8ac8", "pi-coding-agent/main.js", [
    [`    const resolvedThemePaths = resolveCliPaths(cwd, parsed.themes);
    const createRuntime = async (`, `    const resolvedThemePaths = resolveCliPaths(cwd, parsed.themes);
    ${note("b513b64d8ac8").trim()}
    // Interactive mode installs and refreshes packages again on /reload, /new, /resume and /fork while the TUI
    // draws on the terminal, where git and npm output would corrupt its screen (#11264). Their output is
    // captured, and until the TUI starts each install is announced on stderr instead.
    const packageCommandOutput = appMode === "interactive" ? "capture" : "inherit";
    let announcePackageInstalls = packageCommandOutput === "capture";
    const onPackageProgress = (event) => {
        if (announcePackageInstalls && event.type === "start" && event.message) {
            console.error(chalk.dim(event.message));
        }
    };
    const createRuntime = async (`],
    [`            resourceLoaderOptions: {
                additionalExtensionPaths: resolvedExtensionPaths,`, `            resourceLoaderOptions: {
                packageCommandOutput,
                onPackageProgress,
                additionalExtensionPaths: resolvedExtensionPaths,`],
    [`    else if (appMode === "interactive") {
        const interactiveMode = new InteractiveMode(runtime, {`, `    else if (appMode === "interactive") {
        announcePackageInstalls = false;
        const interactiveMode = new InteractiveMode(runtime, {`],
]);
// END backport b513b64d8ac8

// BEGIN backport e56d35ce5ece
// e56d35ce5ece: GPT-6.2 Sol is the default model of the openai, azure and openai-codex providers (defaultModelPerProvider). The GPT-6.2 models themselves are catalog data: the recording and the pi-overlay of the catalog files make the provider data.
// not carried: pi-coding-agent/core/model-resolver.d.ts: no declaration changes. packages/ai catalog (families/openai.ts, providers/azure.ts, openai.ts, openai-codex.ts) and the ai and coding-agent tests and CHANGELOGs: the package ships none of them (the generator and the tests are not in the package); the generated data carries the catalog change.
patch("e56d35ce5ece", "pi-coding-agent/core/model-resolver.js", [
    [`    openai: "gpt-6.1-sol",
    azure: "gpt-6.1-sol",
    "openai-codex": "gpt-6.1-sol",
`, `    ${note("e56d35ce5ece").trim()}
    openai: "gpt-6.2-sol",
    azure: "gpt-6.2-sol",
    "openai-codex": "gpt-6.2-sol",
`],
]);
// The same change to Pi's TypeScript source of the table, which the pinned-table test of coding/cli reads from the source mirror.
mirrorSource("e56d35ce5ece", "packages/coding-agent/src/core/model-resolver.ts", [
    [`	openai: "gpt-6.1-sol",
	azure: "gpt-6.1-sol",
	"openai-codex": "gpt-6.1-sol",
`, `	openai: "gpt-6.2-sol",
	azure: "gpt-6.2-sol",
	"openai-codex": "gpt-6.2-sol",
`],
]);
// END backport e56d35ce5ece

// BEGIN backport cead615b48b8
// cead615b48b8: the Codex context windows of GPT-6.2 (Sol 500000, Astra 272000) are catalog data: the pi-overlay file catalog/providers/openai-codex.ts (Pi's, at this commit) makes the oracle's openai-codex provider data. No compiled file of the package changes.
// not carried: packages/ai catalog/providers/openai-codex.ts, packages/ai test and CHANGELOG: the package ships none of them; the generated data carries the change.
// END backport cead615b48b8

// BEGIN backport e5c01ca6cb9d
// e5c01ca6cb9d (#12329, closes #12286): the strict flag of the GitHub Copilot Responses models is catalog data: the pi-overlay file catalog/providers/github-copilot.ts (Pi's, at this commit) makes the oracle's github-copilot provider data. No compiled file of the package changes.
// not carried: packages/ai catalog/providers/github-copilot.ts, packages/ai test and CHANGELOG: the package ships none of them; the generated data carries the change.
// END backport e5c01ca6cb9d

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
