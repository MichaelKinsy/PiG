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
// The same entries are applied to the installed Pi the parity tests compare with (`--packages name=dir ...`, from automation/pin/oracle.py), so the oracle is Pi with these fixes.
// The source mirror (.upstream/current) stays the pinned release, which the sync ledger's delta audit compares with the earlier pin; the
// tests that read the mirror's docs and schemas read them through `--read-mirror <mirror> <path>` (internal/upstreammirror.ReadPicked), the
// pinned file plus the entries of packageFileBackports and backportPatches that cover it.
// `--mirror-paths` lists the mirror files a backport covers (internal/upstreammirror.PickedPaths), so a test that reads many mirror files reads only those through `--read-mirror`.
// `--verify-packages` and `--verify-mirror` fail when an installed Pi does not carry each entry once, or a mirror file is not the pinned release's.
//
// Pi commits of the 0.8.0 list that are not here, and why:
//   49fe0a237 (Anthropic threads by endpoint), code part: pi-ai's anthropic-messages implementation is a bridge stub that runs in PiG's host; only its description text is backported below.
//   75ca6e7d0, 628d8ab52, 64c96d00d, d9d535b6c (durable, env), b48c675a9 (env daemon, Rust), 75d59639a, 65c43bb52 (chord): packages the Node runtime does not vendor.
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const note = (sha) => `// Pi ${sha} (post-v1.5.2), backported by automation/gen/pi-post-release-backports.mjs.\n`;

// 49fe0a237 (#11987): the supportsMessageThreads description of pi-ai's compat schema, which the models docs and the models.json schema repeat.
const messageThreadsDefault = ["Default: true for provider anthropic with a baseUrl on api.anthropic.com", "Default: true for a baseUrl on api.anthropic.com"];

// 1397070024 (#12112, #11773): Qwen Token Plan uses the current gateways, per-plan key variables and the current Team and Individual model lists.
// Pi's four provider modules, its env-api-keys table, its help text and its providers doc carry the change as source; the provider data
// (providers/data/*.json, which Pi regenerates from models.dev at each build) carries it as the result of that generation over the release's
// data: the new base URL on every record, the Team lists without the records models.dev marks deprecated (catalog skipDeprecated), and the
// two records the Individual allowlist gains, which are the Team records of the same models.dev source under the Individual provider id.
const qwenGateways = {
  international: ["https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1", "https://token-plan.maas.qwencloudapi.com/compatible-mode/v1"],
  china: ["https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1", "https://token-plan.maas.qianwenaiapi.com/compatible-mode/v1"],
};
// The four providers: id, the gateway, the name before and after, the per-plan variable, the variable both plans of a region shared, and whether its catalog is the Individual allowlist.
const qwenTokenPlans = [
  { id: "qwen-token-plan", gateway: qwenGateways.international, name: "Qwen Token Plan", newName: "Qwen Token Plan Team", planVar: "QWEN_TOKEN_PLAN_TEAM_API_KEY", sharedVar: "QWEN_TOKEN_PLAN_API_KEY", individual: false },
  { id: "qwen-token-plan-cn", gateway: qwenGateways.china, name: "Qwen Token Plan CN", newName: "Qwen Token Plan Team CN", planVar: "QWEN_TOKEN_PLAN_TEAM_CN_API_KEY", sharedVar: "QWEN_TOKEN_PLAN_CN_API_KEY", individual: false },
  { id: "qwen-token-plan-individual", gateway: qwenGateways.international, name: "Qwen Token Plan Individual", newName: "Qwen Token Plan Individual", planVar: "QWEN_TOKEN_PLAN_INDIVIDUAL_API_KEY", sharedVar: "QWEN_TOKEN_PLAN_API_KEY", individual: true },
  { id: "qwen-token-plan-individual-cn", gateway: qwenGateways.china, name: "Qwen Token Plan Individual CN", newName: "Qwen Token Plan Individual CN", planVar: "QWEN_TOKEN_PLAN_INDIVIDUAL_CN_API_KEY", sharedVar: "QWEN_TOKEN_PLAN_CN_API_KEY", individual: true },
];
// The records of the release's Token Plan data by id, with the display name each record's text starts with (a patch needs it to name one record).
const qwenRecordNames = {
  "deepseek-v4-flash-0731": "DeepSeek V4 Flash 0731", "deepseek-v4-pro": "DeepSeek V4 Pro", "deepseek-v4-pro-0813": "DeepSeek V4 Pro 0813", "deepseek-v4.1-flash": "DeepSeek V4.1 Flash", "deepseek-v4.1-flash-1017": "DeepSeek V4.1 Flash 1017",
  "glm-5": "GLM-5", "glm-5.1": "GLM-5.1", "glm-5.2": "GLM-5.2", "glm-5.3": "GLM-5.3", "kimi-k2.5": "Kimi K2.5", "kimi-k2.6": "Kimi K2.6", "kimi-k2.7-code": "Kimi K2.7 Code", "MiniMax-M2.5": "MiniMax-M2.5",
  "qwen3.6-flash": "Qwen3.6 Flash", "qwen3.6-plus": "Qwen3.6 Plus", "qwen3.7-max": "Qwen3.7 Max", "qwen3.7-plus": "Qwen3.7 Plus", "qwen3.8-flash": "Qwen3.8 Flash", "qwen3.8-max": "Qwen3.8 Max",
};
// The Individual lists of the release, and the ids 1397070024 adds to them (catalog/providers/qwen-token-plan-individual.ts INDIVIDUAL_MODEL_IDS).
const qwenIndividualIds = ["deepseek-v4-flash-0731", "deepseek-v4-pro", "deepseek-v4-pro-0813", "glm-5.2", "glm-5.3", "qwen3.6-flash", "qwen3.7-max", "qwen3.7-plus", "qwen3.8-flash", "qwen3.8-max"];
const qwenIndividualAddedIds = ["deepseek-v4.1-flash", "deepseek-v4.1-flash-1017"];
const qwenRecordHead = (provider, baseUrl, id, name) => `"chat:${id}":{"id":"${id}","name":"${name}","api":"openai-completions","provider":"${provider}","baseUrl":"${baseUrl}",`;
const qwenCompat = (reasoningEffort) => `"compat":{"supportsStrictMode":true,"thinkingFormat":"qwen","supportsDeveloperRole":false,"supportsStore":false,"supportsReasoningEffort":${reasoningEffort}}`;
const qwenFreeCost = `"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}`;
// The two Team records models.dev marks deprecated, which skipDeprecated drops, as [id, name, the record's text after its base URL].
const qwenDeprecatedRecords = [
  ["deepseek-v3.2", "DeepSeek V3.2", `"reasoning":true,"input":["text"],${qwenFreeCost},"contextWindow":131072,"maxTokens":65536,${qwenCompat(false)},"type":"chat"},`],
  ["deepseek-v4-flash", "DeepSeek V4 Flash", `"reasoning":true,"input":["text"],${qwenFreeCost},"contextWindow":1000000,"maxTokens":384000,${qwenCompat(true)},"thinkingLevelMap":{"off":null,"minimal":null,"low":null,"medium":null,"high":"high","xhigh":null,"max":"max"},"type":"chat"},`],
];
// The text of the deepseek-v4.1-flash and deepseek-v4.1-flash-1017 records after their base URL, as the Team data of the release holds it.
const qwenDeepseek41FlashTail = `"reasoning":true,"input":["text","image"],${qwenFreeCost},"contextWindow":1000000,"maxTokens":384000,${qwenCompat(true)},"thinkingLevelMap":{"off":null,"minimal":null,"low":"low","medium":null,"high":"high","xhigh":null,"max":"max"},"inputLimits":{"images":{"resize":{"maxWidth":2000,"maxHeight":2000,"maxBytes":4718592,"jpegQuality":80}}},"type":"chat"},`;
const qwenDataPatches = ({ id: provider, gateway: [oldUrl, newUrl], individual }) => {
  const moved = (ids) => ids.map((id) => [qwenRecordHead(provider, oldUrl, id, qwenRecordNames[id]), qwenRecordHead(provider, newUrl, id, qwenRecordNames[id])]);
  if (!individual) return [...qwenDeprecatedRecords.map(([id, name, tail]) => [qwenRecordHead(provider, oldUrl, id, name) + tail, ""]), ...moved(Object.keys(qwenRecordNames))];
  // The added records go where the generator's order puts them: after deepseek-v4-pro-0813, before glm-5.2. One entry moves glm-5.2 and
  // puts them before it: an entry's `before` is text of the release's file, never the result of another entry.
  const next = "glm-5.2";
  const added = qwenIndividualAddedIds.map((id) => qwenRecordHead(provider, newUrl, id, qwenRecordNames[id]) + qwenDeepseek41FlashTail).join("");
  const [[nextBefore, nextAfter]] = moved([next]);
  return [...moved(qwenIndividualIds.filter((id) => id !== next)), [nextBefore, added + nextAfter]];
};
// The provider module's change in the compiled output (four-space indent, with the backport note) and in Pi's TypeScript source (tabs), which the source mirror holds.
const qwenModulePatches = ({ name, newName, gateway: [oldUrl, newUrl], planVar, sharedVar }, indent, marker) => [
  [`${indent}${indent}name: "${name}",
${indent}${indent}baseUrl: "${oldUrl}",
${indent}${indent}auth: { apiKey: envApiKeyAuth("${name} API key", ["${sharedVar}"]) },
`, `${marker ? `${indent}${indent}${note("1397070024")}` : ""}${indent}${indent}name: "${newName}",
${indent}${indent}baseUrl: "${newUrl}",
${indent}${indent}auth: {
${indent}${indent}${indent}apiKey: envApiKeyAuth("${newName} API key", [
${indent}${indent}${indent}${indent}"${planVar}",
${indent}${indent}${indent}${indent}"${sharedVar}",
${indent}${indent}${indent}]),
${indent}${indent}},
`],
];
const qwenTokenPlanBackports = Object.fromEntries(qwenTokenPlans.flatMap((plan) => [
  [`pi-ai/providers/${plan.id}.js`, qwenModulePatches(plan, "    ", true)],
  [`pi-ai/providers/data/${plan.id}.json`, qwenDataPatches(plan)],
]));

export const backportPatches = {
  // bacd45b9c (#11973, #11264): the model scope warnings are diagnostics of the runtime the factory returns, not lines it prints on every session replacement.
  "pi-coding-agent/main.js": [
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
  ],
  // bacd45b9c: resolveModelScope, which printed each warning with console.warn, has no caller left and is removed.
  "pi-coding-agent/core/model-resolver.js": [
    [`export async function resolveModelScope(patterns, modelRuntime, options) {
    const { scopedModels, diagnostics } = await resolveModelScopeWithDiagnostics(patterns, modelRuntime, options);
    for (const diagnostic of diagnostics) {
        console.warn(chalk.yellow(\`Warning: \${diagnostic.message}\`));
    }
    return scopedModels;
}
`, ""],
  ],
  // b3c2e4a6bef8 (#12001): the llama.cpp default server port is 9931.
  "pi-coding-agent/extensions/llama/provider.js": [
    ['export const DEFAULT_LLAMA_SERVER_URL = "http://127.0.0.1:8080";', 'export const DEFAULT_LLAMA_SERVER_URL = "http://127.0.0.1:9931";'],
  ],
  // 49fe0a237 (#11987): Anthropic message threads default by endpoint, not provider id (the description pi-ai's compat schema and the models docs carry).
  "pi-ai/providers/compat-schema.js": [messageThreadsDefault],
  "pi-coding-agent/docs/models-configuration.md": [messageThreadsDefault],
  // 9b88b94e2 (#12093, #12064): a provider error reading "Connection closed." is retried (the classifier that pi-ai's SDK, the coding agent and the oracle test of ai.IsRetryableAssistantError run).
  "pi-ai/utils/retry.js": [
    [`    "connection.?lost",
    "other side closed",
`, `    "connection.?lost",
    ${note("9b88b94e2").trim()}
    // Gateway mid-stream drop relayed as an Anthropic-style \`api_error\` event,
    // e.g. {"type":"error","error":{"type":"api_error","message":"Connection closed."}} (#12064).
    "connection.?closed",
    "other side closed",
`],
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
    // 282b01c80 (#12116, #12066): a relative path pattern is anchored at the search path, like grep's glob, instead of matching through the folders above it.
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
  ],
  // 282b01c80: grep's glob description, in the wording find's pattern now shares.
  "pi-coding-agent/core/tools/grep.js": [
    [`"Filter files by glob pattern, relative to path, e.g. '*.ts', 'src/**/*.ts' or '**/*.spec.ts'. A pattern without '/' matches file names at any depth"`,
      `"Filter files by glob pattern relative to path, e.g. 'src/**/*.ts'. Without '/', e.g. '*.ts', it matches file names at any depth"`],
  ],
  // 282b01c80: the gondolin example's glob for find and grep anchors too and drops a leading "./".
  "pi-coding-agent/examples/extensions/gondolin/index.ts": [
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
  ],
  // 935f0ac0ccfc (#12092): the bash tool keeps the end of the last line when the retained tail is only that over-long line.
  "pi-coding-agent/core/tools/output-accumulator.js": [
    ["        const tailTruncation = truncateTail(this.getSnapshotText(), {\n",
      `        ${note("935f0ac0ccfc").trim()}
        // Clipping drops the partial first line of the retained tail. When the tail is only the end of one over-long line,
        // that is everything, so show the end of that line instead.
        const snapshotText = this.getSnapshotText() || this.tailText;
        const tailTruncation = truncateTail(snapshotText, {
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
  // a83b75a07 (#12089): a command a signal ended reports 128 + the signal number as its code, not 0.
  "pi-coding-agent/core/exec.js": [
    ['import { spawn } from "node:child_process";\n', 'import { spawn } from "node:child_process";\nimport { constants as osConstants } from "node:os";\n'],
    ["    const exitCode = code ?? 0;\n",
      `    ${note("a83b75a07").trim()}
    // A signal-killed command has no exit code. Use the shell convention, as the bash tool does, so a
    // timeout, an abort or a crash does not read as success (#12088).
    const exitCode = code ?? (proc.signalCode ? 128 + (osConstants.signals[proc.signalCode] ?? 0) : 1);
`],
  ],
  // a83b75a07 (#12089): the pi.exec docs name the code of a command a signal ended.
  "pi-coding-agent/docs/extensions.md": [
    ["A command that ignores SIGTERM gets SIGKILL after 5 seconds.\n",
      "A command that ignores SIGTERM gets SIGKILL after 5 seconds. A command terminated by a signal, by pi or otherwise, reports 128 + the signal number as `code`, like a shell: 143 for SIGTERM, 137 for SIGKILL.\n"],
  ],
  // 0e1e3fc81 (#12052): a rendered line is blank once every escape sequence the converter handles is stripped.
  "pi-coding-agent/core/export-html/tool-renderer.js": [
    ['import { ansiLinesToHtml } from "./ansi-to-html.js";', 'import { ansiLinesToHtml, stripAnsiEscapes } from "./ansi-to-html.js";'],
    ["const ANSI_ESCAPE_REGEX = /\\x1b\\[[\\d;]*m/g;\n", ""],
    ['    return line.replace(ANSI_ESCAPE_REGEX, "").trim().length === 0;', '    return stripAnsiEscapes(line).trim().length === 0;'],
  ],
  // 0e1e3fc81 (#12052): a click on a link in an expandable tool output does not toggle it.
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
  // 406e719a6bff (#12049): a tool schema with a node that has no type, enum or anyOf is not sent strict (OpenAI strict mode rejects it).
  "pi-ai/api/constrained-sampling.js": [
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
  ],
  // 1397070024 (#12112, #11773): the Qwen Token Plan provider modules and provider data (the gateways, the names, the key variables, the model lists).
  ...qwenTokenPlanBackports,
  // 1397070024: each Qwen Token Plan provider reads its per-plan variable first, then the region variable both plans used to share.
  "pi-ai/env-api-keys.js": [
    [`        "ant-ling": "ANT_LING_API_KEY",
${qwenTokenPlans.map((plan) => `        "${plan.id}": "${plan.sharedVar}",\n`).join("")}`,
      `        "ant-ling": "ANT_LING_API_KEY",
        ${note("1397070024").trim()}
        // Qwen Token Plan: the per-plan variable first, then the region variable both plans used to share.
${qwenTokenPlans.map((plan) => `        "${plan.id}": ["${plan.planVar}", "${plan.sharedVar}"],\n`).join("")}`],
    ["    const envVar = envMap[provider];\n    return envVar ? [envVar] : undefined;\n", '    const envVars = envMap[provider];\n    return typeof envVars === "string" ? [envVars] : envVars;\n'],
  ],
  // 1397070024: --help lists the four per-plan variables.
  "pi-coding-agent/cli/args.js": [
    [`  QWEN_TOKEN_PLAN_API_KEY          - Qwen Token Plan API key (international region)
  QWEN_TOKEN_PLAN_CN_API_KEY       - Qwen Token Plan API key (China region)
`, `  QWEN_TOKEN_PLAN_TEAM_API_KEY     - Qwen Token Plan Team API key (international region)
  QWEN_TOKEN_PLAN_TEAM_CN_API_KEY  - Qwen Token Plan Team API key (China region)
  QWEN_TOKEN_PLAN_INDIVIDUAL_API_KEY - Qwen Token Plan Individual API key (international region)
  QWEN_TOKEN_PLAN_INDIVIDUAL_CN_API_KEY - Qwen Token Plan Individual API key (China region)
`],
  ],
  // 1397070024: the providers doc lists the four per-plan variables.
  "pi-coding-agent/docs/providers.md": [
    ["| Qwen Token Plan and Individual | `QWEN_TOKEN_PLAN_API_KEY` |\n| Qwen Token Plan (China) and Individual (China) | `QWEN_TOKEN_PLAN_CN_API_KEY` |\n",
      "| Qwen Token Plan Team | `QWEN_TOKEN_PLAN_TEAM_API_KEY` |\n| Qwen Token Plan Team (China) | `QWEN_TOKEN_PLAN_TEAM_CN_API_KEY` |\n| Qwen Token Plan Individual | `QWEN_TOKEN_PLAN_INDIVIDUAL_API_KEY` |\n| Qwen Token Plan Individual (China) | `QWEN_TOKEN_PLAN_INDIVIDUAL_CN_API_KEY` |\n"],
  ],
};

// Files of a Pi package that are not compiled output and that the vendored runtime does not carry, but that the installed Pi package
// (the parity oracle) and the source mirror hold, with the same text: keyed like backportPatches, by package and path in the package.
export const packageFileBackports = {
  // 49fe0a237 (#11987): the models.json schema Pi generates from compat-schema.ts.
  "pi-coding-agent/schemas/models.schema.json": [messageThreadsDefault],
};

// Pi's TypeScript sources that tests read from the source mirror and that a backport of their compiled output changes, by path in the Pi
// repository, with the same change as Pi's commit makes to the source.
export const mirrorSourceBackports = Object.fromEntries(qwenTokenPlans.map((plan) => [`packages/ai/src/providers/${plan.id}.ts`, qwenModulePatches(plan, "\t", false)]));

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
// mirrorSourceBackports. Every other mirror file is the pinned release's, whose delta from the pin the sync ledger accounts for.
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
  else if (mode === "--verify-packages") verifyPackageBackports(packagesOf(args));
  else if (mode === "--read-mirror") process.stdout.write(readMirrorFile(args[0], args[1]));
  else if (mode === "--verify-mirror") verifyMirrorBackports(args[0]);
  else if (mode === "--mirror-paths") process.stdout.write(mirrorBackportPaths().map((path) => `${path}\n`).join(""));
  else applyBackportPatches(mode);
}
