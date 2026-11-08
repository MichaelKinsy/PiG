// Drives p2's own TypeScript components (apps/p2/src/tui/components of the pinned Pi mirror), never a translated oracle.
// stdin: [{scene, width, theme, mode}]; stdout: one entry per probe, the rendered rows after the scene's initial state and
// after each of its inputs. argv[2] is the mirror's apps/p2 directory, argv[3] the expected Pi package version.
// The scenes are the same, by name, as in oracle_scenes_test.go: a scene changes in both files or not at all.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";

const p2 = process.argv[2];
const src = (path) => import(pathToFileURL(`${p2}/src/${path}`).href);
const version = JSON.parse(readFileSync(`${p2}/../../packages/tui/package.json`, "utf8")).version;
if (version !== process.argv[3]) throw new Error(`Wrong Pi oracle version ${version}`);

// The same pi-tui module instance p2 imports (one keybinding manager): resolved through the mirror's workspace links.
const tui = await import(pathToFileURL(realpathSync(`${p2}/../../node_modules/@earendil-works/pi-tui/dist/index.js`)).href);
const { BUILTIN_THEMES, createTheme } = await src("tui/theme/theme.ts");
const { ThemeRef } = await src("tui/theme/ref.ts");
const list = await src("tui/components/list-overlay.ts");
const selectors = await src("tui/components/selectors.ts");
const tree = await src("tui/components/tree-selector.ts");
const userSelector = await src("tui/components/user-message-selector.ts");
const secret = await src("tui/components/secret-input.ts");
const status = await src("tui/components/status.ts");
const messages = await src("tui/components/messages.ts");
const toolExecution = await src("tui/components/tool-execution.ts");
const bash = await src("tui/components/bash-execution.ts");
const skill = await src("tui/components/skill-invocation.ts");
const { Container, Text } = tui;

const KEY_TEXT = { "tui.select.confirm": "enter", "tui.select.cancel": "escape", "app.interrupt": "esc", "app.expandTools": "ctrl+o" };
const APP_KEYS = { "app.interrupt": "\x1b", "app.models.save": "\x13", "app.thinking.save": "\x13", "app.tree.editLabel": "\x14", "app.session.delete": "\x04" };

const keys = { matches: (data, id) => (id.startsWith("tui.") ? tui.getKeybindings().matches(data, id) : APP_KEYS[id] === data) };
const ui = { requestRender() {} };

function context(themeName, mode, extra = {}) {
	const ref = new ThemeRef(createTheme(BUILTIN_THEMES[themeName], mode, { colors: {} }));
	return { theme: ref, keyText: (id) => KEY_TEXT[id] ?? "ctrl+o", requestRender() {}, keys, markdownTransformers: [], cwd: "/work", ...extra };
}

const noop = () => {};
const DOWN = "\x1b[B";
const UP = "\x1b[A";
const ENTER = "\r";
const ESC = "\x1b";
const PGDN = "\x1b[6~";
const PGUP = "\x1b[5~";

export const scenes = {};
const scene = (name, build) => {
	scenes[name] = build;
};

const manyRows = Array.from({ length: 30 }, (_, i) => ({
	key: `k${i}`,
	label: i % 7 === 3 ? `row ${i} with a very long label that has to be truncated to fit the narrow viewport at hand` : `row ${i} alpha`,
	detail: i % 3 === 0 ? `d${i}` : undefined,
	description: i % 5 === 0 ? `Description of row ${i} that is long enough to be cut off at narrow widths, with \x1b]52;c;evil\x07 control text` : undefined,
	color: i % 4 === 1 ? ["accent", "muted", "warning", "error", "success"][i % 5] : undefined,
}));
const wideRows = [
	{ key: "w1", label: "日本語のラベル和中文混合", detail: "詳細" },
	{ key: "w2", label: "emoji 👨‍👩‍👧‍👦 row\x1b[31mred\x1b[0m\ttab", detail: "x\ny" },
	{ key: "w3", label: "" },
];

scene("list-basic", (ctx) => ({
	component: new list.ListOverlay(ctx, {
		title: "Pick a thing",
		rows: () => manyRows,
		onSelect: noop,
		onClose: noop,
		actions: [
			{ id: "app.session.delete", label: "delete", run: noop },
			{ key: "ctrl+t", label: "toggle", run: noop },
		],
		footer: () => ["A footer note that explains the status of things and wraps over several rows at narrow widths for sure ok", "second"],
	}),
	focus: true,
	inputs: [DOWN, DOWN, PGDN, PGDN, UP, PGUP, "a", "l", DOWN, "\x7f", ESC],
}));
scene("list-short", (ctx) => ({
	component: new list.ListOverlay(ctx, { title: () => "Wide 日本語 title", rows: () => wideRows, onClose: noop, height: 5 }),
	focus: true,
	inputs: [DOWN, DOWN, DOWN, "zzz"],
}));
scene("list-empty", (ctx) => ({
	component: new list.ListOverlay(ctx, { title: "Empty", rows: () => [], onClose: noop, selected: "none", footer: () => [] }),
	focus: false,
	inputs: [DOWN, ENTER],
}));
scene("list-follow", (ctx) => ({
	component: new list.ListOverlay(ctx, { title: "Log", rows: () => manyRows, onClose: noop, follow: true, selected: "k3", height: 6 }),
	focus: true,
	inputs: [UP, UP, DOWN, DOWN, DOWN],
}));
scene("list-preselect", (ctx) => ({
	component: new list.ListOverlay(ctx, { title: "Pre", rows: () => manyRows, onClose: noop, selected: "k20", height: 7, onSelect: noop }),
	focus: true,
	inputs: [DOWN],
}));
scene("confirm", (ctx) => ({
	component: new list.ConfirmOverlay(ctx, ["Delete the thing?", "It cannot be undone and this line is long enough to be truncated at narrow widths", "日本語"], noop),
	inputs: [],
}));
const viewerLines = (width) =>
	Array.from({ length: 60 }, (_, i) => (i % 9 === 0 ? `line ${i} ${"x".repeat(width)}` : `line ${i} \x1b[32mgreen\x1b[0m 日本語`));
scene("text-viewer", (ctx) => ({
	component: new list.TextViewer(ctx, { title: "File\x1b]0;t\x07 view 日本語", lines: viewerLines, onClose: noop, height: 8, line: 30 }),
	inputs: [DOWN, DOWN, PGDN, PGDN, PGDN, PGDN, PGDN, PGUP, UP, ESC],
}));
scene("text-viewer-short", (ctx) => ({
	component: new list.TextViewer(ctx, { title: "Short", lines: () => ["only", "two"], onClose: noop }),
	inputs: [DOWN, PGDN],
}));

scene("dynamic-border", (ctx) => ({ component: new selectors.DynamicBorder(ctx, "borderAccent"), inputs: [] }));
scene("secret-input", (ctx) => {
	const component = new secret.SecretInput(ctx);
	return { component, focus: true, inputs: ["a", "b", "日", "\x7f", "\x1b[200~se\ncret\x1b[201~", "x".repeat(40), "\x15", "p", "\x1b[200~par", "tial", "\x1b[201~", "q"] };
});
scene("secret-input-unfocused", (ctx) => {
	const component = new secret.SecretInput(ctx);
	return { component, focus: false, inputs: ["abc"] };
});

const catalog = [
	{ provider: "anthropic", modelId: "claude-sonnet-5", name: "Claude Sonnet 5" },
	{ provider: "anthropic", modelId: "claude-opus-5-long-identifier-for-truncation", name: "Claude Opus 5 with a long display name" },
	{ provider: "openai", modelId: "gpt-6", name: "GPT 6" },
	{ provider: "openai", modelId: "gpt-6-mini", name: "GPT 6 mini" },
	{ provider: "google", modelId: "gemini-4", name: "Gemini 日本語" },
	{ provider: "faux", modelId: "faux-1", name: "Faux" },
	...Array.from({ length: 12 }, (_, i) => ({ provider: "bulk", modelId: `m${i}`, name: `Bulk model ${i}` })),
];
scene("model-selector", (ctx) => ({
	component: new selectors.ModelSelectorComponent(ctx, {
		models: catalog, scoped: [catalog[0], catalog[2], catalog[4]],
		current: { provider: "openai", modelId: "gpt-6" }, defaultModel: { provider: "anthropic", modelId: "claude-sonnet-5" },
		title: "Select model", onSelect: noop, onSelectAsDefault: noop, onCancel: noop,
	}),
	focus: true,
	inputs: [DOWN, DOWN, "\t", DOWN, "\x13", "g", "p", "t", "\x7f", PGDN, UP, ENTER, ESC],
}));
scene("model-selector-unscoped", (ctx) => ({
	component: new selectors.ModelSelectorComponent(ctx, { models: catalog, scoped: [], current: { provider: "faux", modelId: "faux-1" }, initialSearch: "gem", onSelect: noop, onCancel: noop }),
	focus: true,
	inputs: ["\t", DOWN, "x", "y", "z", UP, "\x15"],
}));
scene("model-selector-empty", (ctx) => ({
	component: new selectors.ModelSelectorComponent(ctx, { models: [], scoped: [], onSelect: noop, onCancel: noop }),
	focus: false,
	inputs: [DOWN],
}));
const levels = ["off", "minimal", "low", "medium", "high", "xhigh"];
scene("thinking-selector", (ctx) => ({
	component: new selectors.ThinkingSelectorComponent(ctx, { current: "low", levels, defaultLevel: "medium", onSelect: noop, onCancel: noop, onSelectAsDefault: noop }),
	focus: true,
	inputs: [DOWN, DOWN, "\x13", "h", "i", "\x7f", UP, ENTER, ESC],
}));
scene("thinking-selector-plain", (ctx) => ({
	component: new selectors.ThinkingSelectorComponent(ctx, { current: "high", levels: ["off", "high"], onSelect: noop, onCancel: noop }),
	focus: false,
	inputs: [DOWN, "q"],
}));
scene("scoped-models", (ctx) => ({
	component: new selectors.ScopedModelsSelectorComponent(ctx, {
		models: catalog, enabled: [{ provider: "openai", modelId: "gpt-6" }, { provider: "bulk", modelId: "m3" }], onSave: noop, onCancel: noop,
	}),
	focus: true,
	inputs: [DOWN, " ", DOWN, ENTER, "b", "u", "\x7f", PGDN, "\x13", ESC],
}));
scene("scoped-models-empty", (ctx) => ({
	component: new selectors.ScopedModelsSelectorComponent(ctx, { models: [], enabled: [], onSave: noop, onCancel: noop }),
	focus: false,
	inputs: [DOWN, " "],
}));

const treeNodes = () => {
	const nodes = [
		{ entry: 1, parent: null, conversation: 1, role: "user", text: "start the work" },
		{ entry: 2, parent: 1, conversation: 1, role: "assistant", text: "Sure, here is the plan\nwith a second line\x1b]0;x\x07" },
		{ entry: 3, parent: 2, conversation: 1, role: "user", text: "continue with the first option" },
		{ entry: 4, parent: 3, conversation: 1, role: "assistant", text: "done with the first option" },
		{ entry: 5, parent: 2, conversation: 2, role: "user", text: "try the second option instead 日本語" },
		{ entry: 6, parent: 5, conversation: 2, role: "assistant", text: "second option answer" },
		{ entry: 7, parent: 5, conversation: 3, role: "user", text: "or a third thing entirely" },
		{ entry: 8, parent: 7, conversation: 3, role: "assistant", text: "a very long answer that has to be truncated at narrow widths because it keeps going and going" },
		{ entry: 9, parent: 4, conversation: 1, role: "user", text: "last on main" },
	];
	for (let i = 0; i < 20; i++) nodes.push({ entry: 10 + i, parent: i === 0 ? 9 : 9 + i, conversation: 1, role: i % 2 ? "user" : "assistant", text: `chain ${i}` });
	return nodes;
};
scene("tree-selector", (ctx) => ({
	component: new tree.TreeSelectorComponent(ctx, treeNodes(), 6, new Map([[1, "main"], [2, "alt branch"], [3, "日本語"]]), noop, noop, noop),
	focus: true,
	inputs: [DOWN, DOWN, DOWN, DOWN, "\x1b[C", "\x1b[C", "\x1b[D", UP, "\x14", "x", "y", "\x7f", ENTER, DOWN, "\x14", ESC, PGDN, PGUP, UP, UP, ENTER],
}));
scene("tree-selector-roots", (ctx) => ({
	component: new tree.TreeSelectorComponent(
		ctx,
		[
			{ entry: 1, parent: null, conversation: 1, role: "user", text: "first root" },
			{ entry: 2, parent: 1, conversation: 1, role: "assistant", text: "answer one" },
			{ entry: 3, parent: null, conversation: 2, role: "user", text: "second root" },
			{ entry: 4, parent: 3, conversation: 2, role: "assistant", text: "answer two" },
			{ entry: 5, parent: 3, conversation: 3, role: "user", text: "fork" },
			{ entry: 6, parent: 99, conversation: 4, role: "user", text: "orphan" },
		],
		null,
		new Map([[2, "second"]]),
		noop,
		noop,
		noop,
	),
	focus: false,
	inputs: [DOWN, DOWN, "\x14", "a", ESC, UP],
}));
const userItems = () => [
	{ id: 3, text: "first message" },
	{ id: 5, text: "a second message that is quite long and goes beyond the narrow widths of the viewport at hand" },
	{ id: 8, text: "multi\nline\tmessage\x1b]0;t\x07 with 日本語" },
	...Array.from({ length: 12 }, (_, i) => ({ id: 20 + i, text: `later message ${i}` })),
];
scene("user-message-selector", (ctx) => ({
	component: new userSelector.UserMessageSelectorComponent(ctx, userItems(), noop, noop, 5),
	focus: true,
	inputs: [DOWN, DOWN, DOWN, DOWN, UP, "\x1b[5~", "\x1b[6~", UP, ENTER, ESC],
}));
scene("user-message-selector-empty", (ctx) => ({
	component: new userSelector.UserMessageSelectorComponent(ctx, [], noop, noop, undefined),
	focus: false,
	inputs: [DOWN, ENTER],
}));
scene("user-message-selector-single", (ctx) => ({
	component: new userSelector.UserMessageSelectorComponent(ctx, userItems().slice(0, 1), noop, noop),
	focus: false,
	inputs: [DOWN],
}));

const editorUi = { requestRender() {}, terminal: { rows: 24, columns: 80 } };
// Loaders keep timers; the probe exits when it is done, so the scenes need no cleanup.
scene("status-working", (ctx) => ({ component: new status.WorkingStatusIndicator(ui, ctx, "Working 日本語 on a long message that wraps around at narrow widths"), inputs: [] }));
scene("status-working-color", (ctx) => ({ component: new status.WorkingStatusIndicator(ui, ctx, "Plain", (text) => `<${text}>`), inputs: [] }));
scene("status-retry", (ctx) => ({ component: new status.RetryStatusIndicator(ui, ctx, 2, 0, "rate limited\x1b]0;x\x07"), inputs: [] }));
scene("status-compaction-manual", (ctx) => ({ component: new status.CompactionStatusIndicator(ui, ctx, "manual"), inputs: [] }));
scene("status-compaction-overflow", (ctx) => ({ component: new status.CompactionStatusIndicator(ui, ctx, "overflow"), inputs: [] }));
scene("status-compaction-threshold", (ctx) => ({ component: new status.CompactionStatusIndicator(ui, ctx, "threshold"), inputs: [] }));
scene("status-compaction-retry", (ctx) => ({ component: new status.CompactionStatusIndicator(ui, ctx, "threshold", 1), inputs: [] }));
const pasted = (text) => `\x1b[200~${text}\x1b[201~`;
const longText = Array.from({ length: 10 }, (_, i) => `editor line ${i} 日本語`).join("\n");
const editorScene = (indicator, inputs) => (ctx) => {
	const editor = new status.CustomEditor(editorUi, ctx);
	if (indicator) editor.setWorkingStatusIndicator(indicator(ctx));
	return { component: editor, focus: true, inputs };
};
scene("editor-plain", editorScene(undefined, [pasted("hello"), pasted(longText), pasted(longText), "\x1b[A", "\x1b[A"]));
const ups = (n) => Array.from({ length: n }, () => "\x1b[A");
scene("editor-working", editorScene((ctx) => new status.WorkingStatusIndicator(ui, ctx, "Working..."), [pasted("hello"), pasted(longText), pasted(longText), ...ups(8)]));
scene("editor-working-long", editorScene((ctx) => new status.WorkingStatusIndicator(ui, ctx, "Working on a long message that does not fit the border at narrow widths 日本語"), [pasted(longText), pasted(longText), ...ups(10)]));
scene("editor-retry", editorScene((ctx) => new status.RetryStatusIndicator(ui, ctx, 1, 0, "overloaded"), [pasted("one\ntwo"), pasted(longText), pasted(longText)]));
scene("editor-compaction", editorScene((ctx) => new status.CompactionStatusIndicator(ui, ctx, "manual"), [pasted(longText), pasted(longText), ...ups(14)]));
const markdownText =
	"# Heading\n\nSome **bold** and *italic* text with `code` and a [link](https://example.com/very/long/path). 日本語のテキスト\n\n- item one\n- item two with a long line that wraps around the narrow widths of the terminal\n  - nested\n\n1. first\n2. second\n\n> quote line\n\n```\nplain code block\n  indented\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n---\n\nfinal paragraph\x1b]52;c;evil\x07";
const image = { type: "image", data: "", mimeType: "image/png" };
const userItem = (fields) => ({ type: "user", key: "u", text: "hello **there**", images: [], files: [], ...fields });
const padded = (ctx, outputPad) => ({ ...ctx, outputPad });
scene("user-plain", (ctx) => ({ component: new messages.UserMessageComponent(userItem({ text: markdownText }), ctx), inputs: [] }));
scene("user-short", (ctx) => ({ component: new messages.UserMessageComponent(userItem({}), ctx), inputs: [] }));
scene("user-pad3", (ctx) => ({ component: new messages.UserMessageComponent(userItem({ author: { kind: "guest", id: "g", name: "Mallory 日本語" }, note: "3 chat lines passed", files: ["a.txt", "dir/b\x1b[31m.txt"] }), padded(ctx, 3)), inputs: [] }));
scene("user-guest", (ctx) => ({
	component: new messages.UserMessageComponent(
		userItem({ author: { kind: "guest", id: "g", name: "Alice\x1b]0;x\x07" }, replyTo: { entryId: 4, name: "Bob", excerpt: "an excerpt of the quoted message that is long" }, images: [image, image] }),
		ctx,
	),
	inputs: [],
}));
scene("user-peer", (ctx) => ({ component: new messages.UserMessageComponent(userItem({ peer: { id: "p", title: "Other session" }, text: "" , images: [image] }), ctx), inputs: [] }));
scene("user-chat", (ctx) => ({ component: new messages.UserMessageComponent(userItem({ chat: true, author: { kind: "owner", id: "owner", name: "Owner" } }), ctx), inputs: [] }));
scene("user-owner-note", (ctx) => ({ component: new messages.UserMessageComponent(userItem({ author: { kind: "owner", id: "owner", name: "Owner" }, note: "a note" , images: [image] }), ctx), inputs: [] }));
const skillText = '<skill name="deploy" location="/s/deploy.md">\n# Deploy\n\nStep one\nStep **two** with a long line that wraps over several rows at narrow widths\n</skill>\n\nplease go 日本語';
scene("user-skill", (ctx) => {
	const component = new messages.UserMessageComponent(userItem({ text: skillText }), ctx);
	return { component, inputs: ["@expand", "@collapse"], actions: { expand: () => component.setExpanded(true), collapse: () => component.setExpanded(false) } };
});
scene("user-skill-only", (ctx) => ({ component: new messages.UserMessageComponent(userItem({ text: '<skill name="x" location="/s">\nbody\n</skill>' }), ctx), inputs: [] }));
scene("user-skill-files", (ctx) => ({ component: new messages.UserMessageComponent(userItem({ text: '<skill name="x" location="/s">\nbody\n</skill>', files: ["f.txt"] }), ctx), inputs: [] }));
scene("skill-invocation", (ctx) => {
	const component = new skill.SkillInvocationComponent(skill.parseSkillBlock(skillText), ctx, 2);
	return { component, inputs: ["@expand", "@collapse"], actions: { expand: () => component.setExpanded(true), collapse: () => component.setExpanded(false) } };
});
const assistant = (content, stopReason = "stop", errorMessage = undefined) => ({ role: "assistant", content, api: "x", provider: "faux", model: "faux-1", usage: {}, stopReason, errorMessage, timestamp: 1 });
const text = (value) => ({ type: "text", text: value });
const thinking = (value) => ({ type: "thinking", thinking: value });
const toolCall = { type: "toolCall", id: "c1", name: "bash", arguments: {} };
const assistantScene = (message, hide = false, label = undefined) => (ctx) => {
	const component = new messages.AssistantMessageComponent(ctx, message, hide, label);
	return { component, inputs: ["@hide", "@show"], actions: { hide: () => component.setHideThinkingBlock(true), show: () => component.setHideThinkingBlock(false) } };
};
scene("assistant-markdown", assistantScene(assistant([thinking("private **reasoning**\n\nsecond"), thinking("more"), text(markdownText), thinking("trailing thought")])));
scene("assistant-hidden", assistantScene(assistant([thinking("private reasoning"), text("Answer"), thinking("again")]), true, "Pondering..."));
scene("assistant-empty-thinking", assistantScene(assistant([thinking("   "), text("   ")])));
scene("assistant-error", assistantScene(assistant([text("partial")], "error", "fixture error\x1b]0;x\x07")));
scene("assistant-error-unknown", assistantScene(assistant([], "error")));
scene("assistant-aborted", assistantScene(assistant([], "aborted")));
scene("assistant-aborted-message", assistantScene(assistant([text("x")], "aborted", "Stopped by the owner")));
scene("assistant-aborted-request", assistantScene(assistant([text("x")], "aborted", "Request was aborted")));
scene("assistant-length", assistantScene(assistant([text("cut")], "length")));
scene("assistant-tool-calls", assistantScene(assistant([text("running"), toolCall], "error", "tool failed")));
scene("assistant-none", (ctx) => ({ component: new messages.AssistantMessageComponent(ctx), inputs: [] }));
// One fenced block per language of the table: the code is highlighted by p2's lowlight (highlight.js 11) here.
const highlightSnippets = JSON.parse(readFileSync(new URL("./oracle_highlight.json", import.meta.url), "utf8"));
for (const [lang, code] of Object.entries(highlightSnippets))
	scene(`highlight-${lang}`, assistantScene(assistant([text(`\`\`\`${lang}\n${code}\n\`\`\``)])));
scene("compaction-collapsed", (ctx) => {
	const component = new messages.CompactionSummaryComponent("Summary of **earlier** work\n\n- a\n- b", false, ctx);
	return { component, inputs: ["@expand", "@collapse"], actions: { expand: () => component.setExpanded(true), collapse: () => component.setExpanded(false) } };
});
scene("compaction-options", (ctx) => ({ component: new messages.CompactionSummaryComponent("   ", true, ctx, { label: "[branch summary]", collapsed: "Branch summarized" }), inputs: [] }));
scene("compaction-pad", (ctx) => ({ component: new messages.CompactionSummaryComponent("Expanded summary", true, padded(ctx, 4)), inputs: [] }));
for (const color of ["muted", "error", "warning"])
	scene(`notice-${color}`, (ctx) => {
		const component = new messages.NoticeComponent("The model could not answer.\x1b]0;x\x07 日本語 and a very long text that wraps at narrow widths", color, ctx);
		return { component, inputs: ["@update"], actions: { update: () => component.update("Updated") } };
	});

const argsValues = { path: "src/app.ts", content: "line one\nline two\ttab\x1b[31mred\x1b[0m", nested: { a: [1, 2.5, true, null, "s"], b: { c: "日本語" } }, long: "x".repeat(300), count: 42 };
const toolView = (fields) => ({ id: "call", name: "mcp_thing", args: argsValues, status: "done", output: "", images: [], ...fields });
const outputLines = (n) => Array.from({ length: n }, (_, i) => `output-${i} \x1b[32mgreen\x1b[0m 日本語 ${"w".repeat(i * 3)}`).join("\n");
const toolScene = (view, argsComplete = true, pad = undefined) => (ctx) => {
	const base = { ...ctx, cwd: "/work" };
	const component = new toolExecution.ToolExecutionComponent(view, undefined, pad === undefined ? base : { ...base, outputPad: pad }, argsComplete);
	return { component, inputs: ["@expand", "@collapse", "@update"], actions: { expand: () => component.setExpanded(true), collapse: () => component.setExpanded(false), update: () => component.update({ ...view, status: "error", output: "failed\nbadly" }) } };
};
scene("tool-done-long", toolScene(toolView({ output: outputLines(15) })));
scene("tool-done-short", toolScene(toolView({ output: "ok", durationMs: 1234 })));
scene("tool-done-exactly-ten", toolScene(toolView({ output: outputLines(10) })));
scene("tool-pending", toolScene(toolView({ status: "pending" }), false));
scene("tool-running", toolScene(toolView({ status: "running", output: "partial\noutput" })));
scene("tool-error", toolScene(toolView({ status: "error", output: "boom: failed\n  at line 3" }), true, 3));
scene("tool-interrupted", toolScene(toolView({ status: "interrupted" })));
scene("tool-images", toolScene(toolView({ output: "see image", images: [image, image] })));
scene("tool-empty-args", toolScene(toolView({ args: {}, output: "x" })));
scene("tool-nested-calls", toolScene(toolView({ status: "pending", calls: [{ id: "n", name: "read", args: { path: "a" }, status: "running", depth: 0 }], output: "" })));

const bashScene = (command, options, steps) => (ctx) => {
	const component = new bash.BashExecutionComponent(command, ctx, { ...options, tui: options?.tui ? ui : undefined });
	const actions = steps(component);
	return { component, inputs: Object.keys(actions).map((name) => `@${name}`), actions };
};
const bashLines = (n) => Array.from({ length: n }, (_, i) => `line-${i} ${"c".repeat(i % 7)} 日本語\x1b[31m`).join("\r\n");
scene("bash-running", bashScene("sleep 5 && echo 日本語", { tui: true }, (c) => ({ out: () => c.setOutput("partial\r\nline", false), done: () => c.setComplete(0, false) })));
scene("bash-long", bashScene("seq 1 45", {}, (c) => ({ out: () => c.setOutput(bashLines(45), false), done: () => c.setComplete(0, false), expand: () => c.setExpanded(true), collapse: () => c.setExpanded(false) })));
scene("bash-truncated", bashScene("cat big", { excludeFromContext: true, outputPad: 2, by: "Alice (guest)" }, (c) => ({ out: () => c.setOutput(bashLines(30), true), done: () => c.setComplete(0, false, { truncated: true, fullOutputPath: "/tmp/full.log" }) })));
scene("bash-fail", bashScene("false", {}, (c) => ({ out: () => c.setOutput("nope", false), done: () => c.setComplete(2, false) })));
scene("bash-cancel", bashScene("sleep 9", { tui: true }, (c) => ({ out: () => c.setOutput("x", false), done: () => c.setComplete(undefined, true) })));
scene("bash-error", bashScene("weird", {}, (c) => ({ done: () => c.setComplete(undefined, false, { error: "spawn failed\x1b]0;x\x07" }) })));
scene("bash-no-output", bashScene("true", {}, (c) => ({ done: () => c.setComplete(0, false), out: () => c.setOutput("", false) })));
scene("bash-huge", bashScene("yes", {}, (c) => ({ out: () => c.setOutput(Array.from({ length: 3000 }, (_, i) => `y${i}${"z".repeat(30)}`).join("\n"), false), done: () => c.setComplete(0, false), expand: () => c.setExpanded(true) })));

export function run(probes) {
	return probes.map((probe) => {
		const ctx = context(probe.theme, probe.mode);
		const built = scenes[probe.scene](ctx);
		const component = built.component;
		if (built.focus !== undefined && "focused" in component) component.focused = built.focus;
		const frames = [component.render(probe.width)];
		for (const input of built.inputs ?? []) {
			// "@name" runs a scene action (a setter), anything else is typed.
			if (input.startsWith("@")) built.actions[input.slice(1)]();
			else component.handleInput?.(input);
			frames.push(component.render(probe.width));
		}
		return frames;
	});
}

let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const out = run(JSON.parse(input));
// Components keep timers alive (loaders): exit once the output is flushed.
process.stdout.write(JSON.stringify(out), () => process.exit(0));
