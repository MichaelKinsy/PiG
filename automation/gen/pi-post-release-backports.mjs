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
// pinned file plus the entries of packageFileBackports and backportPatches that cover it.
// `--verify-packages` and `--verify-mirror` fail when an installed Pi does not carry each entry once, or a mirror file is not the pinned build's.

import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const note = (sha) => `// Pi ${sha} (post-v1.5.2), backported by automation/gen/pi-post-release-backports.mjs.\n`;

// 49fe0a237 (#11987): the supportsMessageThreads description of pi-ai's compat schema, which the models docs and the models.json schema repeat.
const messageThreadsDefault = ["Default: true for provider anthropic with a baseUrl on api.anthropic.com", "Default: true for a baseUrl on api.anthropic.com"];

const blocks = [];
const patch = (id, key, patches) => blocks.push({ id, key, patches, table: "dist" });
const packageFile = (id, key, patches) => blocks.push({ id, key, patches, table: "package" });

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

// BEGIN backport 49fe0a237
// 49fe0a237 (#11987): the models.json schema Pi generates from compat-schema.ts.
packageFile("49fe0a237", "pi-coding-agent/schemas/models.schema.json", [messageThreadsDefault]);
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

// backportPatches keys the compiled output of a Pi package; packageFileBackports the files that are not compiled output and that the vendored runtime
// does not carry, but that the installed Pi package (the parity oracle) and the source mirror hold, with the same text. Both are keyed by package and path.
// Blocks of several Pi commits can name one file: their patches add up, in the order of the blocks in this script.
const tableOf = (name) => {
  const table = {};
  for (const b of blocks) if (b.table === name) table[b.key] = [...(table[b.key] ?? []), ...b.patches];
  return table;
};
export const backportPatches = tableOf("dist");
export const packageFileBackports = tableOf("package");

const patchesOf = (file) => backportPatches[file] ?? packageFileBackports[file] ?? [];

// patchBackports returns source, the text of file (a key of backportPatches or packageFileBackports), with its backports applied.
export function patchBackports(file, source) {
  for (const [before, after] of patchesOf(file)) {
    if (source.split(before).length !== 2) throw new Error(`Pi source changed in ${file}, a post-v1.5.2 backport no longer applies: ${before}`);
    source = source.replace(before, () => after);
  }
  return source;
}

// applyBackportPatches rewrites the vendored files under dist (shims/pi-dist).
export function applyBackportPatches(dist) {
  for (const file of Object.keys(backportPatches)) {
    const path = join(dist, file);
    writeFileSync(path, patchBackports(file, readFileSync(path, "utf8")));
  }
}

// packageLocation is where the file of package `name` with the path `rest` (the segments of its backport key after the package)
// lives in an installed Pi package: the compiled output under dist/, the docs and schemas at the package root.
const packageLocation = (name, rest) => (name === "pi-coding-agent" && (rest[0] === "docs" || rest[0] === "schemas") ? rest : ["dist", ...rest]).join("/");

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

// classifyBackports says, for every block of a package in `packages`, whether the installed package is still the one the block was written for
// ("pending": each patch's before-text occurs once and its after-text not at all), already carries the block's change ("carried": each
// after-text occurs once, a removal's before-text not at all), or is neither ("conflict", with the file and the first differing line).
// Pi's own version of the change need not be the backport's text, so the caller decides a conflict whose before-text is gone (beforeGone) by
// asking whether the target contains the Pi commit.
export function classifyBackports(packages) {
  const results = [];
  for (const { id, key, patches } of blocks) {
    const [name, ...rest] = key.split("/");
    if (!(name in packages)) continue;
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

// mirrorKey is the backport key of a file of the Pi repository the source mirror holds (packages/coding-agent/docs/x.md), or undefined
// for a file no backport table covers: the mirror's TypeScript sources stay Pi's, whose delta from the pin the sync ledger accounts for.
const mirrorKey = (path) => {
  const match = /^packages\/coding-agent\/((?:docs|schemas)\/.+)$/.exec(path);
  return match && `pi-coding-agent/${match[1]}`;
};

// readMirrorFile returns the text of `path` (relative to the Pi repository) in the source mirror `mirror` with the backports applied:
// the file of the pinned release plus the fixes the release line carries. The mirror itself stays the pinned release.
export function readMirrorFile(mirror, path) {
  const source = readFileSync(join(mirror, path), "utf8");
  const key = mirrorKey(path);
  return key === undefined ? source : patchBackports(key, source);
}

// verifyMirrorBackports throws when a mirror file a backport covers is not the pinned release's (the backport no longer applies once).
export function verifyMirrorBackports(mirror) {
  for (const key of packageBackportKeys()) {
    const path = key.replace(/^pi-coding-agent\//, "packages/coding-agent/");
    if (mirrorKey(path) === key) readMirrorFile(mirror, path);
  }
}

const packagesOf = (args) => Object.fromEntries(args.map((arg) => arg.split(/=(.*)/s).slice(0, 2)));

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  const [mode, ...args] = process.argv.slice(2);
  if (mode === "--packages") applyPackageBackports(packagesOf(args));
  else if (mode === "--package-files") process.stdout.write(JSON.stringify(packageBackportFiles(packagesOf(args))));
  else if (mode === "--verify-packages") verifyPackageBackports(packagesOf(args));
  else if (mode === "--classify") process.stdout.write(JSON.stringify(classifyBackports(packagesOf(args))));
  else if (mode === "--read-mirror") process.stdout.write(readMirrorFile(args[0], args[1]));
  else if (mode === "--verify-mirror") verifyMirrorBackports(args[0]);
  else applyBackportPatches(mode);
}
