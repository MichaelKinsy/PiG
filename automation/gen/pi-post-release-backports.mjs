// Post-v1.5.2 Pi fixes that the vendored Pi JavaScript (shims/pi-dist) must carry (0.8.0).
//
// PiG pins Pi v1.5.2, and the Node extension runtime runs that package's compiled output: the SDK
// (createAgentSession, the built-in tools, the llama.cpp built-in extension), pi-tui components
// extensions render with, and the HTML export. Go ports of later Pi fixes do not change those files,
// so a Node path would keep the old behavior. Each entry below applies one Pi commit's change to the
// compiled output with exact before/after strings, and the vendoring step fails when a `before`
// string is missing or ambiguous (a pin change cannot silently drop a backport). The Pi commit is named
// at each entry. This is Pi's own later behavior, not a divergence.
//
// Run through vendor-pi-dist.sh after the identity patches and before the SDK and library bundles are compiled.
//
// Pi commits of the 0.8.0 list that are not here, and why:
//   49fe0a237 (Anthropic threads by endpoint), code part: pi-ai's anthropic-messages implementation is a bridge stub that runs in PiG's host; only its description text is backported below.
//   75ca6e7d0, 628d8ab52, 64c96d00d, d9d535b6c (durable, env), b48c675a9 (env daemon, Rust), 75d59639a, 65c43bb52 (chord): packages the Node runtime does not vendor.
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const note = (sha) => `// Pi ${sha} (post-v1.5.2), backported by automation/gen/pi-post-release-backports.mjs.\n`;

export const backportPatches = {
  // b3c2e4a6bef8 (#12001): the llama.cpp default server port is 9931.
  "pi-coding-agent/extensions/llama/provider.js": [
    ['export const DEFAULT_LLAMA_SERVER_URL = "http://127.0.0.1:8080";', 'export const DEFAULT_LLAMA_SERVER_URL = "http://127.0.0.1:9931";'],
  ],
  // 49fe0a237 (#11987): Anthropic message threads default by endpoint, not provider id (the description pi-ai's compat schema and the models docs carry).
  "pi-ai/providers/compat-schema.js": [
    ["Default: true for provider anthropic with a baseUrl on api.anthropic.com", "Default: true for a baseUrl on api.anthropic.com"],
  ],
  "pi-coding-agent/docs/models-configuration.md": [
    ["Default: true for provider anthropic with a baseUrl on api.anthropic.com", "Default: true for a baseUrl on api.anthropic.com"],
  ],
  // be2744d95 (#12047): a Kitty fullscreen redraw keeps the uploaded images and skips the ED 2 clear.
  "pi-tui/tui-alt-screen.js": [
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
  ],
  // 5b4d7d3df (#12045): hard breaks after an inline Markdown image stay separate rows.
  "pi-tui/components/markdown.js": [
    ["        for (const line of this.renderToken(token, contentWidth, nextTokenType)) {\n",
      `${"        " + note("5b4d7d3df")}        // Image lines skip wrapping, so split them here: hard breaks after an image must stay separate rows.
        const tokenLines = this.renderToken(token, contentWidth, nextTokenType).flatMap((line) => isImageLine(line) ? line.split("\\n") : [line]);
        for (const line of tokenLines) {
`],
  ],
  // bbfc6c7ce (#12044): the find tool ignores a leading ./ in its pattern.
  "pi-coding-agent/core/tools/find.js": [
    ['                        let effectivePattern = process.platform === "win32" ? pattern.replaceAll("\\\\", "/") : pattern;\n',
      `                        let effectivePattern = process.platform === "win32" ? pattern.replaceAll("\\\\", "/") : pattern;
                        ${note("bbfc6c7ce").trim()}
                        // fd's full paths never contain "/./", so "./src/*.ts" would find nothing; drop the "./".
                        effectivePattern = effectivePattern.replace(/^(?:\\.\\/)+/, "");
`],
  ],
  // 0e1e3fc81 (#12052): OSC 8 hyperlinks in HTML-exported tool output become links.
  "pi-coding-agent/core/export-html/ansi-to-html.js": [
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
  ],
  "pi-coding-agent/core/export-html/tool-renderer.js": [
    ['import { ansiLinesToHtml } from "./ansi-to-html.js";', 'import { ansiLinesToHtml, stripAnsiEscapes } from "./ansi-to-html.js";'],
    ["const ANSI_ESCAPE_REGEX = /\\x1b\\[[\\d;]*m/g;\n", ""],
    ['    return line.replace(ANSI_ESCAPE_REGEX, "").trim().length === 0;', '    return stripAnsiEscapes(line).trim().length === 0;'],
  ],
  "pi-coding-agent/core/export-html/template.js": [
    ["class=\"tool-output expandable ansi-rendered\" onclick=\"if(window.getSelection().toString())return;this.classList.toggle('expanded')\">",
      "class=\"tool-output expandable ansi-rendered\" onclick=\"if(window.getSelection().toString()||event.target.closest('a'))return;this.classList.toggle('expanded')\">"],
  ],
  // 3345c2925 (#12054): hiding an extension's own tool keeps the restored MCP tools pending.
  "pi-coding-agent/core/agent-session.js": [
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
  ],
};

// patchBackports returns source, the text of file (relative to shims/pi-dist), with its backports applied.
export function patchBackports(file, source) {
  for (const [before, after] of backportPatches[file] ?? []) {
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

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) applyBackportPatches(process.argv[2]);
