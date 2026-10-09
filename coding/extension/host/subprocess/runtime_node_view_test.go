package subprocess

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// The Node runtime's view derivation (D107,
// docs/plan/extension-component-kit.md §9): while a D91 frontend draws, each
// extension surface frame carries the view its pi-tui components form next to
// the lines they rendered. Every derived view is checked by rebuilding the
// components it names, as the host's kit does, and rendering them: the rows
// must equal the lines byte for byte (§5.2).

// nodeViewPrelude is shared by the scripts below. It loads the runtime and
// the pinned pi-tui, installs a palette whose tokens are distinguishable
// (success deliberately shares accent's color), and defines rebuild (view to
// pi-tui components) and derive (render, derive, check reproduction).
const nodeViewPrelude = `
import assert from "node:assert/strict";
const RT = process.env.PIG_VIEW_RUNTIME;
const { Runtime } = await import(RT + "/runtime.mjs");
const { deriveView, surfaceTheme, VIEW_WALK_FIELDS } = await import(RT + "/view-walk.mjs");
const tui = await import(RT + "/shims/pi-tui.mjs");
const ca = await import(RT + "/shims/pi-dist/pi-coding-agent/sdk-bundle/index.js");
// The runtime's pi-coding-agent module, whose renderDiff the walk records.
const piCodingAgent = await import(RT + "/shims/pi-coding-agent.mjs");
const terminalImage = await import(RT + "/shims/pi-dist/pi-tui/terminal-image.js");
const { Container, Box, Text, TruncatedText, Markdown, Spacer, SelectList, SettingsList, Image, Loader, HStack, VStack, Input } = tui;
const { DynamicBorder, getMarkdownTheme, getSelectListTheme, getSettingsListTheme } = ca;
// The rows must not depend on the terminal running the test.
terminalImage.setCapabilities({ images: null, trueColor: true, hyperlinks: false });

const fg = {};
["accent", "border", "borderAccent", "borderMuted", "success", "error", "warning", "muted", "dim", "text",
 "mdHeading", "mdLink", "mdLinkUrl", "mdCode", "mdCodeBlock", "mdCodeBlockBorder", "mdQuote", "mdQuoteBorder", "mdHr", "mdListBullet",
 "syntaxComment", "syntaxKeyword", "syntaxFunction", "syntaxVariable", "syntaxString", "syntaxNumber", "syntaxType", "syntaxOperator", "syntaxPunctuation",
 "userMessageText", "thinkingText", "toolTitle", "toolOutput", "bashMode", "toolDiffAdded", "toolDiffRemoved", "toolDiffContext",
].forEach((token, i) => { fg[token] = "\x1b[38;5;" + (100 + i) + "m"; });
// Only the token a closure names tells success from accent.
fg.success = fg.accent;
const bg = { selectedBg: "\x1b[48;5;236m", customMessageBg: "\x1b[48;5;52m", userMessageBg: "\x1b[48;5;53m", toolPendingBg: "\x1b[48;5;54m", toolSuccessBg: "\x1b[48;5;55m", toolErrorBg: "\x1b[48;5;56m" };
const PNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==";
const PNG_REF = "6b7fa434f92a8b80aab02d9bf1a12e49ffcae424e4013a1c4f68b67e3d2bbcd0";
// The ui a conversation component asks to render again.
const fakeUi = { requestRender() {} };
// The built-in tool definitions by name (spec §2.1 "builtin").
const builtinTools = { read: ca.createReadToolDefinition, bash: ca.createBashToolDefinition, powershell: ca.createPowerShellToolDefinition,
  edit: ca.createEditToolDefinition, write: ca.createWriteToolDefinition, grep: ca.createGrepToolDefinition, find: ca.createFindToolDefinition, ls: ca.createLsToolDefinition };
const builtinDefinition = (name) => Object.hasOwn(builtinTools, name) ? builtinTools[name]("/", { operations: {} }) : {};

function newRuntime({ frontend = true, width = 60 } = {}) {
  const runtime = new Runtime("/ext/view.mjs");
  runtime.ready = { width, height: 24 };
  runtime.conn = { socket: {} };
  runtime.contextValues.mode = "tui";
  runtime.sent = [];
  runtime.fireAndForget = (method, args) => runtime.sent.push({ method, args: JSON.parse(JSON.stringify(args)), raw: JSON.stringify(args) });
  runtime.notify = runtime.fireAndForget;
  runtime.call = () => new Promise(() => {});
  runtime.ui.theme.setPalette({ foregrounds: fg, backgrounds: bg, mode: "256color" });
  runtime.setFrontend(frontend);
  return runtime;
}
const theme = () => globalThis[Symbol.for("@earendil-works/pi-coding-agent:theme")];
newRuntime();
const t = theme();

function rebuild(node, images) {
  const fgFn = (token) => token ? (s) => t.fg(token, s) : undefined;
  const bgFn = (token) => token ? (s) => t.bg(token, s) : undefined;
  const kids = () => (node.children ?? []).map((child) => rebuild(child, images));
  switch (node.kind) {
    case "container": { const c = new Container(); for (const k of kids()) c.addChild(k); return c; }
    case "box": { const c = new Box(node.paddingX ?? 1, node.paddingY ?? 1, bgFn(node.bg)); for (const k of kids()) c.addChild(k); return c; }
    case "text": return new Text(node.text ?? "", node.paddingX ?? 1, node.paddingY ?? 1, bgFn(node.bg));
    case "truncated-text": return new TruncatedText(node.text ?? "", node.paddingX ?? 0, node.paddingY ?? 0);
    case "markdown": {
      const s = node.defaultTextStyle;
      const style = s && { ...s, color: fgFn(s.color), bgColor: bgFn(s.bgColor) };
      return new Markdown(node.text ?? "", node.paddingX ?? 0, node.paddingY ?? 0, getMarkdownTheme(), style, node.renderLatex === false ? { renderLatex: false } : undefined);
    }
    case "spacer": return new Spacer(node.lines ?? 1);
    case "dynamic-border": return new DynamicBorder(fgFn(node.color ?? "border"));
    case "select-list": {
      const list = new SelectList(node.items.map((i) => ({ ...i })), node.maxVisible ?? 5, getSelectListTheme(), node.layout ?? {});
      if (node.filter !== undefined) list.setFilter(node.filter);
      if (node.selectedIndex !== undefined) list.setSelectedIndex(node.selectedIndex);
      return list;
    }
    case "settings-list": {
      const items = node.items.map((i) => ({ ...i, submenu: i.submenu ? () => new Text("sub") : undefined }));
      const list = new SettingsList(items, node.maxVisible, getSettingsListTheme(), () => {}, () => {}, { enableSearch: node.enableSearch });
      if (node.filter) for (const ch of node.filter) list.handleInput(ch);
      if (node.selectedIndex !== undefined) list.selectedIndex = node.selectedIndex;
      return list;
    }
    case "image": {
      const image = images.get(node.ref);
      assert.ok(image, "image " + node.ref + " has data");
      return new Image(image.data, node.mimeType, { fallbackColor: fgFn(node.fallbackColor) ?? ((s) => s) },
        { maxWidthCells: node.maxWidthCells, maxHeightCells: node.maxHeightCells, filename: node.filename, imageId: node.imageId });
    }
    case "loader": {
      const loader = new Loader(null, fgFn(node.spinnerColor) ?? ((s) => s), fgFn(node.messageColor) ?? ((s) => s), node.message ?? "Loading...", node.indicator);
      loader.stop();
      loader.currentFrame = node.frame ?? 0;
      loader.updateDisplay();
      return loader;
    }
    case "hstack": case "vstack": {
      const entries = (node.children ?? []).map((child) => ({ component: rebuild(child, images), ...(child.stack ?? {}) }));
      return new (node.kind === "hstack" ? HStack : VStack)(entries, { gap: node.gap, align: node.align });
    }
    case "lines": return { render: () => node.content ?? [], invalidate() {} };
    case "user-message": return new ca.UserMessageComponent(node.text ?? "", getMarkdownTheme(), node.outputPad ?? 1);
    case "assistant-message": {
      const c = new ca.AssistantMessageComponent(undefined, node.hideThinkingBlock ?? false, getMarkdownTheme(), node.hiddenThinkingLabel ?? "Thinking...", node.outputPad ?? 1);
      const m = node.message;
      if (m) c.updateContent({ ...m, content: m.content.map((b) => ({ type: b.type, text: b.text ?? "", thinking: b.thinking ?? "" })) }, node.isStreaming ?? false);
      return c;
    }
    case "tool-execution": {
      const definition = node.toolDefinition === "empty" ? {} : builtinDefinition(node.toolName);
      const c = new ca.ToolExecutionComponent(node.toolName, node.toolCallId ?? "", node.args ?? {}, { showImages: node.showImages, imageWidthCells: node.imageWidthCells }, definition, fakeUi, node.cwd ?? "");
      if (node.argsComplete) c.setArgsComplete();
      if (node.executionStarted) c.markExecutionStarted();
      const r = node.result;
      if (r) {
        const content = r.content.map((b) => b.type === "image" ? { type: "image", data: images.get(b.ref).data, mimeType: b.mimeType } : { type: "text", text: b.text ?? "" });
        c.updateResult({ content, isError: r.isError ?? false, details: r.details }, node.isPartial ?? true);
      }
      if (node.expanded) c.setExpanded(true);
      return c;
    }
    case "bash-execution": {
      const c = new ca.BashExecutionComponent(node.command ?? "", fakeUi, node.excludeFromContext ?? false);
      if (node.output) c.appendOutput(node.output);
      if (node.expanded) c.setExpanded(true);
      const done = node.complete;
      if (done) c.setComplete(done.exitCode, done.cancelled ?? false, done.truncated ? { truncated: true } : undefined, done.fullOutputPath);
      else { c.loader.stop(); c.loader.currentFrame = node.frame ?? 0; c.loader.updateDisplay(); }
      return c;
    }
    case "diff": return new Text(ca.renderDiff(node.diff ?? "", { filePath: node.filePath }), node.paddingX ?? 0, node.paddingY ?? 0);
  }
  throw new Error("unknown kind " + node.kind);
}

const imageStore = new Map();
function reproduces(view, lines) {
  for (const image of view.images ?? []) imageStore.set(image.ref, image);
  assert.deepEqual(rebuild(view.root, imageStore).render(view.width), lines, "view reproduces lines: " + JSON.stringify(view));
}
// derive renders component as a surface root: under its viewTheme, as the
// runtime renders one.
function derive(component, width = 60, options) {
  const surface = surfaceTheme(component);
  const run = () => {
    const lines = component.render(width).map(String);
    const derived = deriveView(component, lines, width, t, { ...options, viewTheme: surface?.theme });
    if (!derived) return { lines };
    for (const [ref, image] of derived.images) imageStore.set(ref, image);
    assert.equal(derived.view.width, width);
    reproduces(derived.view, lines);
    return { lines, view: derived.view, images: derived.images };
  };
  return surface?.colors ? t.withTokenColors(surface.colors, run) : run();
}
// opaque is a custom component: its own render(width), counted.
function opaque(label) {
  const component = { renders: 0, render(width) { this.renders++; return [label + ":" + width, label.toUpperCase()]; }, invalidate() {} };
  return component;
}
`

func runNodeViewScript(t *testing.T, name, script string, env ...string) []byte {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	dir, err := filepath.Abs("runtime-node")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", nodeViewPrelude+script)
	cmd.Env = append(append(os.Environ(), "PIG_VIEW_RUNTIME="+nodeModuleDirectoryURL(dir)), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s: %v\n%s%s", name, err, output, stderr.Bytes())
	}
	return output
}

// Each stock kind maps to its node with the upstream fields, and the view
// reproduces the rendered lines.
func TestNodeViewKinds(t *testing.T) {
	runNodeViewScript(t, "kinds", `
{
  const inner = new Container(); inner.addChild(new Text("inner", 0, 0)); inner.addChild(new Spacer(2));
  const root = new Container();
  root.addChild(new DynamicBorder()); root.addChild(inner); root.addChild(new DynamicBorder((s) => t.fg("accent", s)));
  assert.deepEqual(derive(root).view.root, { kind: "container", children: [
    { kind: "dynamic-border" },
    { kind: "container", children: [{ kind: "text", text: "inner", paddingX: 0, paddingY: 0 }, { kind: "spacer", lines: 2 }] },
    { kind: "dynamic-border", color: "accent" }] });
}
{
  const box = new Box(2, 0, (s) => t.bg("customMessageBg", s));
  box.addChild(new Text("hello world", 1, 1, (s) => t.bg("selectedBg", s)));
  box.addChild(new TruncatedText("trunc " + "x".repeat(80), 1, 0));
  assert.deepEqual(derive(box).view.root, { kind: "box", paddingX: 2, paddingY: 0, bg: "customMessageBg", children: [
    { kind: "text", text: "hello world", bg: "selectedBg" },
    { kind: "truncated-text", text: "trunc " + "x".repeat(80), paddingX: 1 }] });
  assert.deepEqual(derive(new Box()).view.root, { kind: "box" });
}
{
  const text = "# Title\n\n- **bold** item\n- `+"`code`"+`\n\n> quote";
  const md = new Markdown(text, 1, 1, getMarkdownTheme(), { color: (s) => t.fg("text", s), bgColor: (s) => t.bg("userMessageBg", s), italic: true });
  assert.deepEqual(derive(md, 40).view.root, { kind: "markdown", text, paddingX: 1, paddingY: 1, defaultTextStyle: { color: "text", bgColor: "userMessageBg", italic: true } });
  assert.deepEqual(derive(new Markdown("$x$", 0, 0, getMarkdownTheme(), undefined, { renderLatex: false })).view.root, { kind: "markdown", text: "$x$", renderLatex: false });
}
{
  const items = Array.from({ length: 7 }, (_, i) => ({ value: "k" + i, label: "Track " + i, description: "Artist " + i }));
  const list = new SelectList(items, 3, getSelectListTheme(), { minPrimaryColumnWidth: 12 });
  list.setSelectedIndex(4);
  const { view } = derive(list, 72, { focusRoot: true });
  assert.deepEqual(view, { root: { kind: "select-list", id: "select-list:0", items, maxVisible: 3, layout: { minPrimaryColumnWidth: 12 }, selectedIndex: 4 }, width: 72, focus: "select-list:0" });
  const fruit = new SelectList([{ value: "Apple" }, { value: "apricot" }, { value: "banana" }], 5, getSelectListTheme());
  fruit.setFilter("AP");
  fruit.setSelectedIndex(1);
  assert.deepEqual(derive(fruit).view.root, { kind: "select-list", id: "select-list:0", items: [{ value: "Apple" }, { value: "apricot" }, { value: "banana" }], filter: "ap", selectedIndex: 1 });
  fruit.setFilter("zz");
  assert.equal(derive(fruit).view.root.filter, "\uffff");
}
{
  const items = [
    { id: "a", label: "Alpha", currentValue: "on", values: ["on", "off"], description: "first" },
    { id: "b", label: "Beta", currentValue: "x", submenu: () => new Text("sub") },
  ];
  const list = new SettingsList(items, 5, getSettingsListTheme(), () => {}, () => {}, { enableSearch: true });
  list.handleInput("\x1b[B");
  const wire = [
    { id: "a", label: "Alpha", currentValue: "on", description: "first", values: ["on", "off"] },
    { id: "b", label: "Beta", currentValue: "x", submenu: { kind: "lines" } },
  ];
  assert.deepEqual(derive(list).view.root, { kind: "settings-list", id: "settings-list:0", items: wire, maxVisible: 5, enableSearch: true, selectedIndex: 1 });
  list.handleInput("b");
  assert.deepEqual(derive(list).view.root, { kind: "settings-list", id: "settings-list:0", items: wire, maxVisible: 5, enableSearch: true, filter: "b", selectedIndex: 0 });
}
{
  const loader = new Loader(null, (s) => t.fg("accent", s), (s) => t.fg("muted", s), "Working");
  loader.stop();
  loader.currentFrame = 3; loader.updateDisplay();
  assert.deepEqual(derive(loader).view.root, { kind: "loader", message: "Working", spinnerColor: "accent", messageColor: "muted", frame: 3 });
  const verbatim = new Loader(null, (s) => t.fg("accent", s), (s) => s, "Hi", { frames: ["a", "b"], intervalMs: 50 });
  verbatim.stop();
  assert.deepEqual(derive(verbatim).view.root, { kind: "loader", message: "Hi", indicator: { frames: ["a", "b"], intervalMs: 50 } });
}
{
  const image = new Image(PNG, "image/png", { fallbackColor: (s) => t.fg("muted", s) }, { filename: "cover.png", maxWidthCells: 20 });
  const { view, images } = derive(image);
  assert.deepEqual(view.root, { kind: "image", ref: PNG_REF, mimeType: "image/png", maxWidthCells: 20, filename: "cover.png", fallbackColor: "muted" });
  assert.deepEqual([...images], [[PNG_REF, { mimeType: "image/png", data: PNG }]]);
  // A kitty terminal draws the image with the id the render allocated.
  terminalImage.setCapabilities({ images: "kitty", trueColor: true, hyperlinks: false });
  try {
    const kitty = new Image(PNG, "image/png", { fallbackColor: (s) => s });
    const node = derive(kitty).view.root;
    assert.equal(node.imageId, kitty.imageId);
    assert.ok(Number.isInteger(node.imageId));
  } finally {
    terminalImage.setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  }
}
{
  const h = new HStack([{ component: new TruncatedText("left side"), grow: 1 }, { component: new TruncatedText("right"), grow: 1, minSize: 3 }], { gap: 1, align: "center" });
  const v = new VStack([new Text("top", 0, 0), { component: new Spacer(1), basis: 2 }]);
  const root = new Container(); root.addChild(h); root.addChild(v);
  assert.deepEqual(derive(root).view.root, { kind: "container", children: [
    { kind: "hstack", gap: 1, align: "center", children: [
      { kind: "truncated-text", text: "left side", stack: { grow: 1 } },
      { kind: "truncated-text", text: "right", stack: { grow: 1, minSize: 3 } }] },
    { kind: "vstack", children: [{ kind: "text", text: "top", paddingX: 0, paddingY: 0 }, { kind: "spacer", stack: { basis: 2 } }] }] });
}
`)
}

// Pi's conversation components map to the conversation kinds (spec §2.1,
// §9) with the state each holds, and every other form of them is a lines
// range: another markdown theme, a transformer, a click's toggle, a tool
// definition that is neither the built-in one for the name nor empty, a
// subclass, or a renderDiff result the runtime did not record.
func TestNodeViewConversationKinds(t *testing.T) {
	runNodeViewScript(t, "conversation", `
// inner is the node of component as a container's child, where a lines
// range still leaves a view.
const inner = (component) => { const c = new Container(); c.addChild(component); return derive(c).view.root.children[0]; };
const markdownTheme = getMarkdownTheme();
{
  assert.deepEqual(derive(new ca.UserMessageComponent("Hi **there**\n\n- one", markdownTheme, 0)).view.root, { kind: "user-message", text: "Hi **there**\n\n- one", outputPad: 0 });
  assert.deepEqual(derive(new ca.UserMessageComponent("plain")).view.root, { kind: "user-message", text: "plain", outputPad: 1 });
  assert.equal(inner(new ca.UserMessageComponent("x", markdownTheme, 1, [(md) => md + "!"])).kind, "lines");
  assert.equal(inner(new ca.UserMessageComponent("x", { ...markdownTheme, bold: (s) => "<" + s + ">" })).kind, "lines");
  class Mine extends ca.UserMessageComponent {}
  assert.equal(inner(new Mine("x")).kind, "lines");
}
{
  const message = { content: [{ type: "thinking", thinking: "Plan *it*" }, { type: "text", text: "Done **now**" }, { type: "toolCall", id: "c1", name: "ls", arguments: {} }], stopReason: "toolUse" };
  const reply = new ca.AssistantMessageComponent(undefined, true, markdownTheme, "Musing...", 0);
  reply.updateContent(message, true);
  const first = derive(reply).view.root;
  assert.match(first.id, /^assistant-message:\d+$/);
  assert.deepEqual(first, { kind: "assistant-message", id: first.id,
    message: { content: [{ type: "thinking", thinking: "Plan *it*" }, { type: "text", text: "Done **now**" }, { type: "toolCall" }], stopReason: "toolUse" },
    hideThinkingBlock: true, hiddenThinkingLabel: "Musing...", outputPad: 0, isStreaming: true });
  // The kept component keeps its id, so the host keeps its instance.
  reply.updateContent({ content: [{ type: "text", text: "Final" }], stopReason: "error", errorMessage: "boom" }, false);
  assert.deepEqual(derive(reply).view.root, { kind: "assistant-message", id: first.id,
    message: { content: [{ type: "text", text: "Final" }], stopReason: "error", errorMessage: "boom" }, hideThinkingBlock: true, hiddenThinkingLabel: "Musing...", outputPad: 0 });
  const empty = derive(new ca.AssistantMessageComponent()).view.root;
  assert.notEqual(empty.id, first.id);
  assert.deepEqual(empty, { kind: "assistant-message", id: empty.id, outputPad: 1 });
  // A click's toggle is the Node component's own state.
  const toggled = new ca.AssistantMessageComponent(message);
  toggled.thinkingVisibilityOverrides.set(0, true);
  toggled.updateContent(message);
  assert.equal(inner(toggled).kind, "lines");
  assert.equal(inner(new ca.AssistantMessageComponent(message, false, markdownTheme, "Thinking...", 1, [(md) => md])).kind, "lines");
  assert.equal(inner(new ca.AssistantMessageComponent({ content: [{ type: "image", data: PNG, mimeType: "image/png" }] })).kind, "lines");
}
{
  const ls = new ca.ToolExecutionComponent("ls", "call-1", { path: "src" }, { showImages: false, imageWidthCells: 30 }, ca.createLsToolDefinition("/w"), fakeUi, "/w");
  ls.setArgsComplete();
  ls.markExecutionStarted();
  ls.updateResult({ content: [{ type: "text", text: "a.go\nb.go" }, { type: "image", data: PNG, mimeType: "image/png" }], details: { n: 1 }, isError: true }, false);
  ls.setExpanded(true);
  const { view, images } = derive(ls);
  assert.match(view.root.id, /^tool-execution:\d+$/);
  assert.deepEqual(view.root, { kind: "tool-execution", id: view.root.id, toolName: "ls", toolCallId: "call-1", args: { path: "src" }, cwd: "/w",
    showImages: false, imageWidthCells: 30, executionStarted: true, argsComplete: true, expanded: true,
    result: { content: [{ type: "text", text: "a.go\nb.go" }, { type: "image", ref: PNG_REF, mimeType: "image/png" }], isError: true, details: { n: 1 } }, isPartial: false });
  assert.deepEqual([...images], [[PNG_REF, { mimeType: "image/png", data: PNG }]]);
  const custom = new ca.ToolExecutionComponent("kit_tool", "call-2", { q: "x" }, {}, {}, fakeUi);
  custom.updateResult({ content: [{ type: "text", text: "partial 42" }] }, true);
  const node = derive(custom).view.root;
  assert.deepEqual(node, { kind: "tool-execution", id: node.id, toolName: "kit_tool", toolCallId: "call-2", args: { q: "x" }, toolDefinition: "empty",
    result: { content: [{ type: "text", text: "partial 42" }] }, isPartial: true });
  // A shell's renderers are made per definition: bash's map, powershell's
  // under the name bash do not.
  const bash = inner(new ca.ToolExecutionComponent("bash", "c", { command: "ls" }, {}, ca.createBashToolDefinition("/w", { operations: {} }), fakeUi, "/w"));
  assert.deepEqual(bash, { kind: "tool-execution", id: bash.id, toolName: "bash", toolCallId: "c", args: { command: "ls" }, cwd: "/w" });
  assert.equal(inner(new ca.ToolExecutionComponent("bash", "c", { command: "ls" }, {}, ca.createPowerShellToolDefinition("/w", { operations: {} }), fakeUi, "/w")).kind, "lines");
  // An extension's own renderers, another tool's built-in ones, and no
  // definition are not expressible.
  assert.equal(inner(new ca.ToolExecutionComponent("ls", "c", {}, {}, { renderCall: () => new Text("mine", 0, 0) }, fakeUi, "/w")).kind, "lines");
  assert.equal(inner(new ca.ToolExecutionComponent("grep", "c", {}, {}, ca.createLsToolDefinition("/w"), fakeUi, "/w")).kind, "lines");
  assert.equal(inner(new ca.ToolExecutionComponent("ls", "c", {}, {}, undefined, fakeUi, "/w")).kind, "lines");
}
{
  const running = new ca.BashExecutionComponent("sleep 9", fakeUi, true);
  running.appendOutput("x\r\ny");
  running.loader.stop();
  running.loader.currentFrame = 3;
  running.loader.updateDisplay();
  const node = derive(running).view.root;
  assert.match(node.id, /^bash-execution:\d+$/);
  assert.deepEqual(node, { kind: "bash-execution", id: node.id, command: "sleep 9", excludeFromContext: true, output: "x\ny", frame: 3 });
  const done = new ca.BashExecutionComponent("ls -la", fakeUi);
  done.appendOutput("a\n");
  done.appendOutput("b");
  done.setComplete(2, false, { truncated: true }, "/tmp/out");
  done.setExpanded(true);
  const finished = derive(done).view.root;
  assert.deepEqual(finished, { kind: "bash-execution", id: finished.id, command: "ls -la", output: "a\nb", expanded: true,
    complete: { exitCode: 2, truncated: true, fullOutputPath: "/tmp/out" } });
  const cancelled = new ca.BashExecutionComponent("yes", fakeUi);
  cancelled.setComplete(undefined, true);
  const stopped = derive(cancelled).view.root;
  assert.deepEqual(stopped, { kind: "bash-execution", id: stopped.id, command: "yes", complete: { cancelled: true } });
}
{
  const diff = " 1 keep\n-2 old line\n+2 new line\n 3 tail";
  const text = piCodingAgent.renderDiff(diff, { filePath: "kit.go" });
  assert.equal(text, ca.renderDiff(diff));
  assert.deepEqual(derive(new Text(text, 1, 0)).view.root, { kind: "diff", diff, filePath: "kit.go", paddingX: 1, paddingY: 0 });
  assert.deepEqual(derive(new Text(piCodingAgent.renderDiff("+1 x"), 0, 0)).view.root, { kind: "diff", diff: "+1 x", paddingX: 0, paddingY: 0 });
  // A result the runtime did not record, or one drawn on a background, is a text.
  assert.equal(derive(new Text(ca.renderDiff(" 9 other"), 0, 0)).view.root.kind, "text");
  assert.equal(derive(new Text(text, 0, 0, (s) => t.bg("selectedBg", s))).view.root.kind, "text");
}
`)
}

// A component whose render is not exactly a pinned base class's render is a
// lines range. Its rows come from the parent's mouseLayout without a second
// render; only where no layout exists (a vstack child) is it rendered again.
// Under an hstack a lines range cannot stand in for a width-dependent child,
// so the hstack itself becomes the range.
func TestNodeViewOpaqueComponents(t *testing.T) {
	runNodeViewScript(t, "opaque", `
class Fancy extends Text { render(width) { return ["*" + this.text + "*"]; } }
class Plain extends Text {}
{
  const custom = opaque("c");
  const root = new Container();
  root.addChild(new Fancy("hi", 0, 0)); root.addChild(new Plain("plain", 0, 0)); root.addChild(custom);
  const { view } = derive(root, 30);
  assert.deepEqual(view.root, { kind: "container", children: [
    { kind: "lines", content: ["*hi*"] },
    { kind: "text", text: "plain", paddingX: 0, paddingY: 0 },
    { kind: "lines", content: ["c:30", "C"] }] });
  assert.equal(custom.renders, 1, "rows from mouseLayout, not a second render");
}
{
  const custom = opaque("b");
  const outer = new Container(); const box = new Box(3, 1); box.addChild(new Text("x", 0, 0)); box.addChild(custom); outer.addChild(box);
  const nested = new Container(); nested.addChild(opaque("n")); outer.addChild(nested);
  const { view } = derive(outer, 30);
  assert.deepEqual(view.root.children[0], { kind: "box", paddingX: 3, children: [{ kind: "text", text: "x", paddingX: 0, paddingY: 0 }, { kind: "lines", content: ["b:24", "B"] }] });
  assert.deepEqual(view.root.children[1], { kind: "container", children: [{ kind: "lines", content: ["n:30", "N"] }] });
  assert.equal(custom.renders, 1, "box rows from its cache and mouseLayout");
  // A steady second render serves the Box from its cache; the rows still come from it.
  derive(outer, 30);
  assert.equal(custom.renders, 2);
}
{
  const custom = opaque("v");
  const v = new VStack([new Spacer(1), { component: custom, maxSize: 1 }]);
  const { view } = derive(v, 30);
  assert.deepEqual(view.root, { kind: "vstack", children: [{ kind: "spacer" }, { kind: "lines", content: ["v:30", "V"], stack: { maxSize: 1 } }] });
  assert.equal(custom.renders, 2, "a vstack has no layout: rendered again");
}
{
  const root = new Container();
  root.addChild(new Text("head", 0, 0));
  root.addChild(new HStack([{ component: new TruncatedText("a"), grow: 1 }, { component: opaque("h"), grow: 1 }]));
  const { view, lines } = derive(root, 30);
  assert.deepEqual(view.root.children[1], { kind: "lines", content: lines.slice(1) });
}
// A root without structure sends no view.
assert.equal(derive(new Fancy("x")).view, undefined);
assert.equal(derive(opaque("r")).view, undefined);
// Closure-only fields and foreign themes make the component a range.
const items = [{ value: "a", label: "A" }, { value: "b", label: "B" }];
for (const [name, component] of [
  ["truncatePrimary", new SelectList(items, 5, getSelectListTheme(), { truncatePrimary: ({ text }) => text })],
  ["select theme", new SelectList(items, 5, { ...getSelectListTheme(), selectedText: (s) => "[" + s + "]" })],
  ["markdown transform", new Markdown("# x", 0, 0, getMarkdownTheme(), undefined, { transform: (s) => s })],
  ["markdown theme", new Markdown("# x", 0, 0, { ...getMarkdownTheme(), heading: (s) => s.toUpperCase() })],
  ["stack visible", new VStack([{ component: new Text("x"), visible: () => true }])],
  ["unmapped color", new Text("x", 1, 1, (s) => "\x1b[41m" + s + "\x1b[49m")],
  ["identity border", new DynamicBorder((s) => s)],
  ["settings theme", new SettingsList([{ id: "a", label: "A", currentValue: "1" }], 5, { ...getSettingsListTheme(), cursor: "> " }, () => {}, () => {})],
]) {
  const root = new Container(); root.addChild(new Spacer(1)); root.addChild(component);
  const { view, lines } = derive(root, 50);
  assert.deepEqual(view.root.children[1], { kind: "lines", content: lines.slice(1) }, name);
}
{
  // An open submenu renders as the list's own frame.
  const list = new SettingsList([{ id: "a", label: "A", currentValue: "1", submenu: () => new Text("submenu") }], 5, getSettingsListTheme(), () => {}, () => {});
  list.handleInput("\r");
  const root = new Container(); root.addChild(list);
  assert.equal(derive(root).view.root.children[0].kind, "lines");
  // A search query drawn some other way than typed has no filter field.
  const search = new SettingsList([{ id: "a", label: "Alpha", currentValue: "1" }], 5, getSettingsListTheme(), () => {}, () => {}, { enableSearch: true });
  search.handleInput("a"); search.handleInput("l"); search.searchInput.cursor = 0;
  const host = new Container(); host.addChild(search);
  assert.equal(derive(host).view.root.children[0].kind, "lines");
}
`)
}

// Style closures map to the token they name, even when another token has the
// same color, and to the token with the same output otherwise.
func TestNodeViewThemeTokens(t *testing.T) {
	runNodeViewScript(t, "tokens", `
assert.equal(derive(new DynamicBorder((s) => t.fg("success", s))).view.root.color, "success");
assert.equal(derive(new DynamicBorder((s) => theme().fg("accent", s))).view.root.color, "accent");
// Bound before the walk: the output decides.
const bound = t.fg.bind(t, "warning");
assert.equal(derive(new DynamicBorder(bound)).view.root.color, "warning");
// The default closure reads coding-agent's shared theme.
assert.deepEqual(derive(new DynamicBorder()).view.root, { kind: "dynamic-border" });
// The walk leaves the theme as it found it.
assert.equal(Object.hasOwn(t, "fg"), false);
assert.equal(Object.hasOwn(t, "bg"), false);
`)
}

// Every carrier sends the view next to its lines while a frontend draws:
// widgets, header and footer, ui.custom frames (with focus when the
// component is the list) and the render_* results. Dedup compares lines and
// view together.
func TestNodeViewCarriers(t *testing.T) {
	runNodeViewScript(t, "carriers", `
const runtime = newRuntime({ width: 50 });
const last = (method) => runtime.sent.filter((m) => m.method === method).at(-1)?.args;
const count = (method) => runtime.sent.filter((m) => m.method === method).length;

const items = [{ value: "a", label: "A", description: "one" }, { value: "b", label: "B", description: "two" }];
const list = new SelectList(items, 5, getSelectListTheme());
runtime.setWidgetFactory("w", () => { const c = new Container(); c.addChild(new DynamicBorder()); c.addChild(list); return c; });
let widget = last("ui.setWidget");
assert.deepEqual(widget.view.root.children[1], { kind: "select-list", id: "select-list:0.1", items, selectedIndex: 0 });
assert.equal(widget.view.focus, undefined);
assert.equal(widget.view.width, 50);
reproduces(widget.view, widget.content);
// Same lines and view: no frame.
runtime.renderWidget("w");
assert.equal(count("ui.setWidget"), 1);
// Same lines, different view (a description not shown at width 40): a frame.
runtime.ready.width = 40;
runtime.renderWidget("w");
assert.equal(count("ui.setWidget"), 2);
items[0].description = "uno";
runtime.renderWidget("w");
assert.equal(count("ui.setWidget"), 3);
assert.deepEqual(runtime.sent.at(-1).args.content, runtime.sent.at(-2).args.content);
runtime.ready.width = 50;

// String-list widgets are a container of texts.
runtime.ui.setWidget("s", ["one", "two"]);
assert.deepEqual(last("ui.setWidget").view.root, { kind: "container", children: [{ kind: "text", text: "one", paddingY: 0 }, { kind: "text", text: "two", paddingY: 0 }] });

runtime.ui.setHeader(() => new Text("head", 1, 0));
assert.deepEqual(last("ui.setHeader").view, { root: { kind: "text", text: "head", paddingY: 0 }, width: 50 });
runtime.ui.setFooter(() => new TruncatedText("foot"));
assert.deepEqual(last("ui.setFooter").view, { root: { kind: "truncated-text", text: "foot" }, width: 50 });

void runtime.openCustomOverlay(() => new SelectList(items, 5, getSelectListTheme()));
for (let i = 0; i < 5 && !last("ui.custom.render"); i++) await Promise.resolve();
const frame = last("ui.custom.render");
assert.equal(frame.view.focus, "select-list:0");
reproduces(frame.view, frame.lines);
void runtime.openCustomOverlay(() => { const c = new Container(); c.addChild(new SelectList(items, 5, getSelectListTheme())); return c; });
for (let i = 0; i < 5 && last("ui.custom.render").key !== "custom-2"; i++) await Promise.resolve();
assert.equal(last("ui.custom.render").view.focus, undefined, "focus only when the component is the list");

const results = [];
runtime.respond = async (_id, result, error) => { if (error) throw error; results.push(result); };
runtime.renderers.set("note", () => new Text("message", 1, 0));
await runtime.handleRequest("1", { method: "render_message", tool: "note", args: { message: {}, options: {}, width: 30 } }, {});
assert.deepEqual(results.at(-1).view, { root: { kind: "text", text: "message", paddingY: 0 }, width: 30 });
reproduces(results.at(-1).view, results.at(-1).lines);
runtime.entryRenderers.set("entry", () => new Spacer(2));
await runtime.handleRequest("2", { method: "render_entry", tool: "entry", args: { entry: {}, options: {}, width: 30 } }, {});
assert.deepEqual(results.at(-1), { lines: ["", ""], view: { root: { kind: "spacer", lines: 2 }, width: 30 } });
runtime.renderers.set("raw", () => ["raw line"]);
await runtime.handleRequest("3", { method: "render_message", tool: "raw", args: { message: {}, options: {}, width: 30 } }, {});
assert.deepEqual(results.at(-1), { lines: ["raw line"] });
runtime.tools.set("tool", { renderCall: () => new TruncatedText("call"), renderResult: () => new Text("result", 0, 0) });
await runtime.handleRequest("4", { method: "render_tool", tool: "tool", args: { card: "c", phase: "call", args: {}, width: 30 } }, {});
assert.deepEqual(results.at(-1).view, { root: { kind: "truncated-text", text: "call" }, width: 30 });
await runtime.handleRequest("5", { method: "render_tool", tool: "tool", args: { card: "c", phase: "result", result: {}, width: 30 } }, {});
assert.deepEqual(results.at(-1).view, { root: { kind: "text", text: "result", paddingX: 0, paddingY: 0 }, width: 30 });
`)
}

// Without a frontend nothing is walked and every frame is byte-identical to
// the frame before D107: no view member at all. The flag arrives with the
// ready state and every state_update.
func TestNodeViewFrontendOffSendsNoView(t *testing.T) {
	runNodeViewScript(t, "frontend off", `
const runtime = newRuntime({ frontend: false, width: 40 });
// Container.render reads its child's render once; a walk reads it again.
let reads = 0;
const child = new Text("x", 0, 0);
const probe = { get render() { reads++; return child.render.bind(child); }, invalidate() {} };
runtime.setWidgetFactory("w", () => { const c = new Container(); c.addChild(probe); return c; });
assert.equal(reads, 1, "no walk without a frontend");
assert.equal(runtime.sent.at(-1).raw, JSON.stringify({ key: "w", content: [("x" + " ".repeat(39))], options: {}, width: 40 }));
runtime.ui.setHeader(() => new Text("head", 1, 0));
assert.equal(runtime.sent.at(-1).raw, JSON.stringify({ clear: false, lines: [" head" + " ".repeat(35)], width: 40, surfaceId: 1, updateOnly: false }));
void runtime.openCustomOverlay(() => new Spacer(1));
for (let i = 0; i < 5 && runtime.sent.at(-1).method !== "ui.custom.render"; i++) await Promise.resolve();
assert.equal(runtime.sent.at(-1).raw, JSON.stringify({ key: "custom-1", lines: [""], width: 40, seq: 1 }));
const results = [];
runtime.respond = async (_id, result) => { results.push(JSON.stringify(result)); };
runtime.renderers.set("note", () => new Text("m", 0, 0));
await runtime.handleRequest("1", { method: "render_message", tool: "note", args: { message: {}, options: {}, width: 3 } }, {});
runtime.tools.set("tool", { renderCall: () => new Spacer(1) });
await runtime.handleRequest("2", { method: "render_tool", tool: "tool", args: { card: "c", phase: "call", args: {}, width: 3 } }, {});
assert.deepEqual(results, [JSON.stringify({ lines: ["m  "] }), JSON.stringify({ lines: [""] })]);
assert.ok(runtime.sent.every((m) => !("view" in m.args)));

// A frontend attaches: state_update sets the flag and the widget is drawn again with its view.
runtime.handleNotify({ method: "state_update", args: { state: { frontend: true } } });
assert.equal(runtime.state.frontend, true);
await new Promise((resolve) => setTimeout(resolve, 50));
const widget = runtime.sent.filter((m) => m.method === "ui.setWidget").at(-1).args;
assert.deepEqual(widget.view.root, { kind: "container", children: [{ kind: "lines", content: ["x" + " ".repeat(39)] }] });
assert.ok(reads > 2);
// It detaches: an omitted frontend is false.
runtime.handleNotify({ method: "state_update", args: { state: { hasUI: true } } });
assert.equal(runtime.state.frontend, false);
`)
}

// Image bytes travel once per connection: the first frame that references a
// ref carries them, later frames name the ref only, and ui.view.evicted makes
// the surfaces that name it send the data again.
func TestNodeViewImagesSendOnce(t *testing.T) {
	runNodeViewScript(t, "images", `
const runtime = newRuntime({ width: 40 });
const frames = (key) => runtime.sent.filter((m) => m.method === "ui.setWidget" && m.args.key === key).map((m) => m.args);
const caption = new Text("cover", 0, 0);
runtime.setWidgetFactory("a", () => { const c = new Container(); c.addChild(new Image(PNG, "image/png", { fallbackColor: (s) => s })); c.addChild(caption); return c; });
assert.deepEqual(frames("a")[0].view.images, [{ ref: PNG_REF, mimeType: "image/png", data: PNG }]);
reproduces(frames("a")[0].view, frames("a")[0].content);
caption.setText("cover 2");
runtime.renderWidget("a");
assert.equal(frames("a").length, 2);
assert.equal(frames("a")[1].view.images, undefined);
assert.equal(frames("a")[1].view.root.children[0].ref, PNG_REF);
// Another surface on the connection references the same bytes by ref only.
runtime.setWidgetFactory("b", () => new Image(PNG, "image/png", { fallbackColor: (s) => s }, { filename: "b.png" }));
assert.equal(frames("b")[0].view.images, undefined);
// An unrelated ref changes nothing.
runtime.handleNotify({ method: "ui.view.evicted", args: { refs: ["00"] } });
await new Promise((resolve) => setTimeout(resolve, 50));
assert.equal(frames("a").length, 2);
// Evicted: both surfaces send again, the first one with the data.
runtime.handleNotify({ method: "ui.view.evicted", args: JSON.stringify({ refs: [PNG_REF] }) });
await new Promise((resolve) => setTimeout(resolve, 50));
assert.equal(frames("a").length, 3);
assert.equal(frames("b").length, 2);
const resent = [frames("a")[2], frames("b")[1]].filter((frame) => frame.view.images);
assert.equal(resent.length, 1);
assert.deepEqual(resent[0].view.images, [{ ref: PNG_REF, mimeType: "image/png", data: PNG }]);
`)
}

// Under socket backpressure a widget replaced before its frame goes out is
// coalesced away (a rendered frame is a replaceable snapshot), and the frame
// that goes out after the drain is the final one, with its image. Only a frame
// that goes out marks its images sent, so the first frame that names a
// coalesced image later carries its bytes.
func TestNodeViewCoalescedFramesKeepImagesUnsent(t *testing.T) {
	runNodeViewScript(t, "coalesced-images", `
const { crc32, deflateSync } = await import("node:zlib");
const { createHash } = await import("node:crypto");
const chunk = (type, data) => {
  const body = Buffer.concat([Buffer.from(type, "latin1"), data]);
  const out = Buffer.alloc(body.length + 8);
  out.writeUInt32BE(data.length, 0);
  body.copy(out, 4);
  out.writeUInt32BE(crc32(body), body.length + 4);
  return out;
};
const png = (n) => Buffer.concat([Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]), chunk("IHDR", Buffer.from([0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0])), chunk("IDAT", deflateSync(Buffer.from([0, n, 0, 0x5f, 0xff]))), chunk("IEND", Buffer.alloc(0))]).toString("base64");
const image = (n) => ({ ref: createHash("sha256").update(Buffer.from(png(n), "base64")).digest("hex"), mimeType: "image/png", data: png(n) });
const runtime = newRuntime({ width: 40 });
const views = () => runtime.sent.filter((m) => m.method === "ui.setWidget" && m.args.view).map((m) => m.args.view);
const show = (n) => runtime.setWidgetFactory("w", () => new Image(png(n), "image/png", { fallbackColor: (s) => s }));
runtime.conn.socket.writableNeedDrain = true;
show(1); show(2); show(3);
assert.equal(views().length, 0);
// The drain: the connection's drain listener requests the frame again.
runtime.conn.socket.writableNeedDrain = false;
runtime.requestWidgetRender("w");
await new Promise((resolve) => setTimeout(resolve, 50));
assert.deepEqual(views().map((view) => view.images), [[image(3)]]);
show(2);
assert.deepEqual(views()[1].images, [image(2)]);
show(3);
assert.equal(views()[2].images, undefined);
`)
}

// nodeViewSurfacesPrelude adds what the theme and list tests share: a
// palette whose dim is faint, the escapes of an override color, a themed
// probe, and a table that annotates its rows as a list.
const nodeViewSurfacesPrelude = `
const colors = await import(RT + "/shims/pi-dist/pi-tui/colors.js");
const esc = (hex, background = false) => (background ? colors.backgroundAnsi : colors.foregroundAnsi)(colors.parseColor(hex), "256color");
const faintPalette = { foregrounds: { ...fg, dim: fg.dim + "\x1b[2m" }, backgrounds: bg, mode: "256color" };
// usePalette gives the runtime's theme and the shared one (t, which the
// closures below read) the same palette, as the host's palette does.
function usePalette(runtime, palette) {
  runtime.ui.theme.setPalette(palette);
  t.setPalette(palette);
}
function themedProbe(viewTheme) {
  const c = new Container();
  c.addChild(new DynamicBorder((s) => t.fg("accent", s)));
  c.addChild(new DynamicBorder((s) => t.fg("dim", s)));
  c.addChild(new Text("bg", 0, 0, (s) => t.bg("selectedBg", s)));
  if (viewTheme !== undefined) c.viewTheme = viewTheme;
  return c;
}
class Table {
  constructor(rows, items, selectedIndex = 1) { this.rows = rows; this.items = items; this.selectedIndex = selectedIndex; this.renders = 0; }
  render(width) { this.renders++; return this.rows.map((row) => row.padEnd(width)); }
  invalidate() {}
  get viewLines() { return { list: { items: this.items, selectedIndex: this.selectedIndex } }; }
}
const tracks = [{ label: "Blue in Green", detail: "Miles Davis", columns: ["5:37"] }, { label: "So What" }];
`

// A surface's root viewTheme (spec §13.3) restyles the terminal rows in
// every mode, escaped in the palette's color mode with a faint token kept
// faint, and rides as view.theme while a frontend draws. An invalid map is
// applied nowhere but still sent, so the host rejects the view.
func TestNodeViewThemeOverride(t *testing.T) {
	runNodeViewScript(t, "theme override", nodeViewSurfacesPrelude+`
const override = { accent: "#d75f00", dim: "#aaaaaa", selectedBg: "#102030" };
const off = newRuntime({ frontend: false, width: 20 });
usePalette(off, faintPalette);
const palette = t.foregrounds;
off.setWidgetFactory("w", () => themedProbe(override));
const terminal = off.sent.at(-1).args;
assert.equal(esc("#d75f00"), "\x1b[38;5;166m");
assert.ok(terminal.content[0].startsWith(esc("#d75f00") + "─"), JSON.stringify(terminal.content[0]));
assert.ok(terminal.content[1].startsWith(esc("#aaaaaa") + "\x1b[2m─"), "a faint token stays faint: " + JSON.stringify(terminal.content[1]));
assert.ok(terminal.content[1].endsWith("\x1b[22;39m"));
assert.ok(terminal.content[2].startsWith(esc("#102030", true)), JSON.stringify(terminal.content[2]));
assert.equal("view" in terminal, false, "no view without a frontend");
assert.equal(t.foregrounds, palette, "the palette is restored");
off.setWidgetFactory("plain", () => themedProbe());
assert.ok(off.sent.at(-1).args.content[0].startsWith(fg.accent + "─"));

const on = newRuntime({ width: 20 });
usePalette(on, faintPalette);
on.setWidgetFactory("w", () => themedProbe(override));
const widget = on.sent.at(-1).args;
assert.deepEqual(widget.content, terminal.content, "a frontend sees the rows the terminal draws");
assert.deepEqual(widget.view.theme, override);
assert.deepEqual(widget.view.root.children.map((c) => c.color ?? c.bg), ["accent", "dim", "selectedBg"]);
t.withTokenColors(Object.entries(override), () => reproduces(widget.view, widget.content));
// Every carrier: header, ui.custom frames and renderers.
on.ui.setHeader(() => themedProbe(override));
assert.deepEqual(on.sent.at(-1).args.lines, terminal.content);
assert.deepEqual(on.sent.at(-1).args.view.theme, override);
void on.openCustomOverlay(() => themedProbe(override));
for (let i = 0; i < 5 && on.sent.at(-1).method !== "ui.custom.render"; i++) await Promise.resolve();
assert.deepEqual(on.sent.at(-1).args.lines, terminal.content);
assert.deepEqual(on.sent.at(-1).args.view.theme, override);
const results = [];
on.respond = async (_id, result) => { results.push(result); };
on.renderers.set("note", () => themedProbe(override));
await on.handleRequest("1", { method: "render_message", tool: "note", args: { message: {}, options: {}, width: 20 } }, {});
assert.deepEqual(results.at(-1).lines, terminal.content);
assert.deepEqual(results.at(-1).view.theme, override);
// Mutating the map restyles the next frame.
const root = on.widgets.get("w").component;
root.viewTheme.accent = "#00af00";
on.renderWidget("w");
assert.ok(on.sent.at(-1).args.content[0].startsWith(esc("#00af00") + "─"));
assert.equal(on.sent.at(-1).args.view.theme.accent, "#00af00");

// Invalid maps: not applied, sent as set.
for (const invalid of [{ accent: "orange" }, { accent: "#d75f00", nope: "#000000" }, { accent: "#D75F0" }, "accent"]) {
  on.setWidgetFactory("bad", () => themedProbe(invalid));
  const frame = on.sent.at(-1).args;
  assert.ok(frame.content[0].startsWith(fg.accent + "─"), "not applied: " + JSON.stringify(invalid));
  assert.deepEqual(frame.view.theme, invalid);
}
on.setWidgetFactory("empty", () => themedProbe({}));
assert.equal("theme" in on.sent.at(-1).args.view, false);
assert.equal(t.foregrounds.accent, fg.accent);
`)
}

// A component whose rows are a lines range declares the frontend-only
// annotations with viewLines (spec §9): the list rides on its lines node,
// the image by ref with its bytes sent once, only while a frontend draws. A
// root lines range with annotations is a view of its own.
func TestNodeViewLinesAnnotations(t *testing.T) {
	runNodeViewScript(t, "lines annotations", nodeViewSurfacesPrelude+`
const on = newRuntime({ width: 20 });
const table = new Table(["Blue in Green", "So What"], tracks);
on.setWidgetFactory("root", () => table);
let frame = on.sent.at(-1).args;
assert.deepEqual(frame.view, { root: { kind: "lines", content: frame.content, list: { items: tracks, selectedIndex: 1 } }, width: 20 });
// Selection moves while the rows stay: a frame all the same.
table.selectedIndex = 0;
on.renderWidget("root");
assert.equal(on.sent.at(-1).args.view.root.list.selectedIndex, 0);
assert.notEqual(on.sent.at(-1), on.sent.at(-2));

const cover = { render: (w) => ["▀".repeat(4).padEnd(w)], invalidate() {}, viewLines: { image: { data: PNG, mimeType: "image/png" }, progress: { value: 30, max: 200 } } };
on.setWidgetFactory("nested", () => { const c = new Container(); c.addChild(new Text("Now playing", 0, 0)); c.addChild(cover); c.addChild(new Table(["a", "b"], [{ label: "a" }, { label: "b" }], -1)); return c; });
frame = on.sent.at(-1).args;
assert.deepEqual(frame.view.root.children[1], { kind: "lines", content: [frame.content[1]], image: { ref: PNG_REF }, progress: { value: 30, max: 200 } });
assert.deepEqual(frame.view.root.children[2].list, { items: [{ label: "a" }, { label: "b" }], selectedIndex: -1 });
assert.deepEqual(frame.view.images, [{ ref: PNG_REF, mimeType: "image/png", data: PNG }]);
reproduces(frame.view, frame.content);
on.renderWidget("nested");
cover.viewLines.progress = { value: 31, max: 200 };
on.renderWidget("nested");
assert.equal(on.sent.at(-1).args.view.root.children[1].progress.value, 31);
assert.equal(on.sent.at(-1).args.view.images, undefined, "image bytes go once");
// A list that does not match the rows is still sent: the host rejects it.
on.setWidgetFactory("mismatch", () => new Table(["a", "b"], [{ label: "a" }, { label: "b" }, { label: "c" }]));
assert.equal(on.sent.at(-1).args.view.root.list.items.length, 3);

// Without a frontend, nothing: the frame is the one before D107.
const off = newRuntime({ frontend: false, width: 20 });
const plain = new Table(["Blue in Green", "So What"], tracks);
off.setWidgetFactory("root", () => plain);
assert.equal(off.sent.at(-1).raw, JSON.stringify({ key: "root", content: plain.rows.map((r) => r.padEnd(20)), options: {}, width: 20 }));
assert.equal(plain.renders, 1);
// Plain lines roots still send no view.
assert.equal(derive({ render: () => ["x"], invalidate() {}, viewLines: {} }).view, undefined);
`)
}

// The host keeps a Node view with a valid override or a matching list, and
// rejects one whose override is invalid or whose list does not match its
// rows (spec §5.2, §6).
func TestNodeViewThemeAndListOnHost(t *testing.T) {
	theme := tui.ActiveTheme()
	foregrounds, backgrounds := theme.ANSIPalette()
	palette, err := json.Marshal(map[string]any{"foregrounds": foregrounds, "backgrounds": backgrounds, "mode": string(theme.GetColorMode())})
	if err != nil {
		t.Fatal(err)
	}
	output := runNodeViewScript(t, "theme and list on host", nodeViewSurfacesPrelude+`
const runtime = newRuntime({ width: 40 });
usePalette(runtime, JSON.parse(process.env.PIG_VIEW_PALETTE));
const frames = [];
const widget = (name, build) => {
  runtime.setWidgetFactory(name, build);
  const frame = runtime.sent.at(-1).args;
  assert.ok(frame.view, name + " has a view");
  frames.push({ name, lines: frame.content, view: frame.view });
};
widget("theme", () => themedProbe({ accent: "#d75f00", dim: "#aaaaaa", selectedBg: "#102030", customMessageBg: "#203040" }));
widget("list", () => { const c = new Container(); c.addChild(new Text("Queue", 0, 0)); c.addChild(new Table(["Blue in Green", "So What"], tracks)); return c; });
widget("bad color", () => themedProbe({ accent: "orange" }));
widget("bad token", () => themedProbe({ nope: "#000000" }));
widget("list mismatch", () => new Table(["a", "b"], [{ label: "a" }, { label: "b" }, { label: "c" }]));
widget("list selection", () => new Table(["a", "b"], [{ label: "a" }, { label: "b" }], 2));
console.log(JSON.stringify(frames));
`, "PIG_VIEW_PALETTE="+string(palette))
	var frames []struct {
		Name  string          `json:"name"`
		Lines []string        `json:"lines"`
		View  json.RawMessage `json:"view"`
	}
	if err := json.Unmarshal(output, &frames); err != nil {
		t.Fatalf("decode frames: %v\n%s", err, output)
	}
	rejected := map[string]string{
		"bad color":      `theme override accent="orange" is not #rrggbb`,
		"bad token":      `theme token "nope" cannot be overridden`,
		"list mismatch":  "list annotation has 3 items for 2 rows",
		"list selection": "list annotation selects 2 of 2 items",
	}
	if len(frames) != 6 {
		t.Fatalf("got %d frames, want 6", len(frames))
	}
	for _, frame := range frames {
		s := newViewSurface("", newViewImageStore())
		err := s.accept(frame.View, frame.Lines)
		if want, ok := rejected[frame.Name]; ok {
			if err == nil || err.Error() != want {
				t.Errorf("%s: accept = %v, want %q", frame.Name, err, want)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: host rejected the view: %v\n%s", frame.Name, err, frame.View)
		}
		view := s.FrontendView(40)
		if view == nil {
			t.Fatalf("%s: the host dropped the view: its rows differ from Node's\n%s\n%q", frame.Name, frame.View, frame.Lines)
		}
		switch frame.Name {
		case "theme":
			if view.Theme["accent"] != "#d75f00" || view.Theme["customMessageBg"] != "#203040" || len(view.Theme) != 4 {
				t.Errorf("theme: frontend theme %v", view.Theme)
			}
		case "list":
			list := view.Root.Children[1].List
			if list == nil || list.Selected != 1 || len(list.Items) != 2 || list.Items[0].Detail != "Miles Davis" || list.Items[0].Columns[0] != "5:37" {
				t.Errorf("list: frontend list %+v", list)
			}
		}
	}
}

// The spec's bounds: past 4096 nodes no view is sent; past depth 64 the deep
// part is a lines range.
func TestNodeViewBounds(t *testing.T) {
	runNodeViewScript(t, "bounds", `
const wide = new Container();
for (let i = 0; i < 4100; i++) wide.addChild(new Spacer(0));
assert.equal(derive(wide).view, undefined);
let deep = new Text("leaf", 0, 0);
for (let i = 0; i < 70; i++) { const c = new Container(); c.addChild(deep); deep = c; }
let node = derive(deep, 20).view.root;
let depth = 0;
while (node.kind === "container") { node = node.children[0]; depth++; }
assert.equal(depth, 64);
assert.deepEqual(node, { kind: "lines", content: ["leaf" + " ".repeat(16)] });
`)
}

// The walk reads fields pi-tui marks private. This fails when the pinned
// pi-tui (or coding-agent's DynamicBorder and conversation components)
// renames one.
func TestNodeViewPinnedPrivateFields(t *testing.T) {
	runNodeViewScript(t, "pin", `
const loader = new Loader(null, (s) => s, (s) => s); loader.stop();
const box = new Box(); box.addChild(new Text("x"));
const bash = new ca.BashExecutionComponent("x", fakeUi); bash.loader.stop();
// Fresh instances: only the class declares the fields (no method has run).
const instances = {
  Container: new Container(), Box: new Box(), Text: new Text("x"), TruncatedText: new TruncatedText("x"),
  Markdown: new Markdown("x", 0, 0, getMarkdownTheme()), Spacer: new Spacer(), DynamicBorder: new DynamicBorder(),
  SelectList: new SelectList([], 5, getSelectListTheme()), SettingsList: new SettingsList([], 5, getSettingsListTheme(), () => {}, () => {}),
  Input: new Input(), Image: new Image(PNG, "image/png", { fallbackColor: (s) => s }), Loader: loader, HStack: new HStack(), VStack: new VStack(),
  UserMessageComponent: new ca.UserMessageComponent("x"), AssistantMessageComponent: new ca.AssistantMessageComponent(),
  ToolExecutionComponent: new ca.ToolExecutionComponent("x", "c", {}, {}, {}, fakeUi), BashExecutionComponent: bash,
};
assert.deepEqual(Object.keys(instances).sort(), Object.keys(VIEW_WALK_FIELDS).sort());
for (const [name, fields] of Object.entries(VIEW_WALK_FIELDS)) {
  for (const field of fields) assert.ok(Object.hasOwn(instances[name], field), name + "." + field);
}
box.render(10);
assert.deepEqual(Object.keys(box.mouseLayout), ["width", "children"]);
assert.deepEqual(Object.keys(box.mouseLayout.children[0]), ["component", "height"]);
assert.ok(Array.isArray(box.cache.childLines) && box.cache.width === 10);
const container = new Container(); container.addChild(new Text("x")); container.render(10);
assert.deepEqual(container.mouseLayout, { width: 10, children: [{ component: container.children[0], height: 3 }] });
const stack = new VStack([{ component: new Text("x"), grow: 1, basis: 2, visible: () => true }]);
assert.deepEqual(Object.keys(stack.entries[0]).sort(), ["basis", "component", "grow", "visible"]);
assert.equal(typeof new SettingsList([], 1, getSettingsListTheme(), () => {}, () => {}, { enableSearch: true }).searchInput.value, "string");
// The kinds are recognized by their own render: each pinned class still has
// one. The conversation kinds are recognized by their class.
const byClass = new Set(["Input", "UserMessageComponent", "AssistantMessageComponent", "ToolExecutionComponent", "BashExecutionComponent"]);
for (const name of Object.keys(VIEW_WALK_FIELDS)) {
  if (byClass.has(name)) continue;
  assert.ok(Object.hasOwn((name === "DynamicBorder" ? ca : tui)[name].prototype, "render"), name + ".prototype.render");
}
// A bash command's border is its second child and its loader a Loader.
assert.ok(bash.children[1] instanceof ca.DynamicBorder);
assert.ok(bash.loader instanceof Loader);
assert.deepEqual(bash.outputLines, []);
assert.equal(bash.status, "running");
assert.ok(new ca.AssistantMessageComponent().thinkingVisibilityOverrides instanceof Map);
// The walk compares a tool's renderers with its built-in definition's: a
// module's (or, for a shell, the definition's) renderCall and renderResult,
// and edit's renderShell. A shell's renderCall returns a Text of its prompt.
for (const name of ["read", "bash", "powershell", "edit", "write", "grep", "find", "ls"]) {
  const definition = builtinDefinition(name);
  assert.equal(typeof definition.renderCall, "function", name + ".renderCall");
  assert.equal(typeof definition.renderResult, "function", name + ".renderResult");
  assert.equal(definition.renderShell, name === "edit" ? "self" : undefined, name + ".renderShell");
}
const shellCall = (name) => builtinDefinition(name).renderCall({ command: "x" }, t, { state: {}, executionStarted: false }).text;
assert.notEqual(shellCall("bash"), shellCall("powershell"));
assert.equal(String(builtinDefinition("bash").renderCall), String(builtinDefinition("powershell").renderCall));
// The runtime's pi-coding-agent module records renderDiff results.
assert.notEqual(piCodingAgent.renderDiff, ca.renderDiff);
`)
}

// BenchmarkNodeViewWalk measures one walk of a representative ui.custom tree
// (borders, padded text, markdown, a 20-item select list, an hstack, a box of
// texts and an opaque component) in the Node runtime: ns/op and the heap the
// walk allocates per op (no GC runs during the timed loop).
func BenchmarkNodeViewWalk(b *testing.B) {
	if _, err := exec.LookPath("node"); err != nil {
		b.Skip("node not installed")
	}
	dir, err := filepath.Abs("runtime-node")
	if err != nil {
		b.Fatal(err)
	}
	script := nodeViewPrelude + `
const N = Number(process.env.PIG_VIEW_N);
const root = new Container();
root.addChild(new DynamicBorder((s) => t.fg("accent", s)));
root.addChild(new Text("Pick a track", 2, 0, (s) => t.bg("customMessageBg", s)));
root.addChild(new Markdown("Now playing **Blue in Green**\n\n- Miles Davis\n- Kind of Blue", 1, 0, getMarkdownTheme()));
root.addChild(new HStack([{ component: new TruncatedText("left"), grow: 1 }, { component: new TruncatedText("right"), grow: 1 }]));
root.addChild(new Spacer(1));
root.addChild(new SelectList(Array.from({ length: 20 }, (_, i) => ({ value: "t" + i, label: "Track " + i, description: "Artist " + i })), 10, getSelectListTheme()));
const box = new Box(1, 0); box.addChild(new Text("a", 0, 0)); box.addChild(new Text("b", 0, 0)); root.addChild(box);
root.addChild(opaque("bar"));
root.addChild(new DynamicBorder((s) => t.fg("accent", s)));
const lines = root.render(80);
for (let i = 0; i < 200; i++) deriveView(root, lines, 80, t);
const { PerformanceObserver, performance } = await import("node:perf_hooks");
const gcStarts = [];
const observer = new PerformanceObserver((list) => { for (const entry of list.getEntries()) gcStarts.push(entry.startTime); });
observer.observe({ entryTypes: ["gc"] });
globalThis.gc();
const before = process.memoryUsage().heapUsed;
const t0 = performance.now();
const start = process.hrtime.bigint();
for (let i = 0; i < N; i++) deriveView(root, lines, 80, t);
const ns = Number(process.hrtime.bigint() - start);
const t1 = performance.now();
const after = process.memoryUsage().heapUsed;
await new Promise((resolve) => setTimeout(resolve, 20));
observer.disconnect();
const gcs = gcStarts.filter((at) => at >= t0 && at <= t1).length;
console.log(JSON.stringify({ nsPerOp: ns / N, bytesPerOp: gcs === 0 ? (after - before) / N : -1, gcs }));
`
	n := max(b.N, 1000)
	cmd := exec.CommandContext(b.Context(), "node", "--expose-gc", "--min-semi-space-size=256", "--max-semi-space-size=256", "--input-type=module", "--eval", script)
	cmd.Env = append(os.Environ(), "PIG_VIEW_RUNTIME="+nodeModuleDirectoryURL(dir), "PIG_VIEW_N="+strconv.Itoa(n))
	b.ResetTimer()
	output, err := cmd.Output()
	b.StopTimer()
	if err != nil {
		b.Fatalf("walk benchmark: %v\n%s", err, output)
	}
	var cost struct {
		NsPerOp    float64 `json:"nsPerOp"`
		BytesPerOp float64 `json:"bytesPerOp"`
		GCs        int     `json:"gcs"`
	}
	if err := json.Unmarshal(output, &cost); err != nil {
		b.Fatalf("walk benchmark output %q: %v", output, err)
	}
	if cost.GCs != 0 {
		b.Fatalf("a GC ran during the timed loop (%d); allocations unmeasured", cost.GCs)
	}
	// The walk's own cost replaces the default ns/op, which would count the
	// Node process's startup.
	b.ReportMetric(cost.NsPerOp, "ns/op")
	b.ReportMetric(cost.BytesPerOp, "walk-B/op")
}

// The views Node derives survive the host's validation (spec §5.2): the
// host's kit, built from PiG's tui ports with the host's active theme and
// terminal capabilities, reproduces Node's lines byte for byte, so it keeps
// the view. The trees are the conformance probe's (§10), the other stock
// kinds and Pi's conversation components, at the probe's widths, under each
// image protocol.
func TestNodeViewsSurviveHostValidation(t *testing.T) {
	theme := tui.ActiveTheme()
	foregrounds, backgrounds := theme.ANSIPalette()
	palette, err := json.Marshal(map[string]any{"foregrounds": foregrounds, "backgrounds": backgrounds, "mode": string(theme.GetColorMode())})
	if err != nil {
		t.Fatal(err)
	}
	previous := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(previous) })
	for _, images := range []tui.ImageProtocol{"", tui.ImageProtocolKitty, tui.ImageProtocolITerm2} {
		t.Run("images="+string(images), func(t *testing.T) { checkNodeViewsOnHost(t, palette, images) })
	}
}

func checkNodeViewsOnHost(t *testing.T, palette []byte, images tui.ImageProtocol) {
	tui.SetCapabilities(tui.TerminalCapabilities{Images: images, TrueColor: true})
	kitTestToolCards(t)
	output := runNodeViewScript(t, "host validation", `
// The prelude's theme, capabilities and keybindings take the host's (Pi's
// table), as a state does.
const { KEYBINDINGS } = await import(RT + "/shims/pi-dist/pi-coding-agent/core/keybindings.js");
const definitions = {};
for (const [id, definition] of Object.entries(KEYBINDINGS)) definitions[id] = { defaultKeys: [definition.defaultKeys].flat(), description: definition.description };
newRuntime().applyKeybindings({ definitions });
t.setPalette(JSON.parse(process.env.PIG_VIEW_PALETTE));
terminalImage.setCapabilities({ images: process.env.PIG_VIEW_IMAGES || null, trueColor: true, hyperlinks: false });
const tracks = Array.from({ length: 5 }, (_, i) => ({ value: "k" + i, label: "Track " + i, description: "Artist " + i }));
const trees = {
  probe: () => {
    const list = new SelectList(tracks, 3, getSelectListTheme());
    list.setSelectedIndex(2);
    const c = new Container();
    c.addChild(new DynamicBorder((s) => t.fg("accent", s)));
    c.addChild(new Text("Kit probe", 2, 0, (s) => t.bg("customMessageBg", s)));
    c.addChild(new Markdown("- one\n- **two**", 1, 0, getMarkdownTheme()));
    c.addChild(new HStack([{ component: new TruncatedText("left side"), grow: 1 }, { component: new TruncatedText("right"), grow: 1 }], { gap: 1 }));
    c.addChild(new Spacer(1));
    c.addChild(list);
    c.viewTheme = { accent: "#d75f00" };
    return c;
  },
  box: () => {
    const box = new Box(2, 1, (s) => t.bg("selectedBg", s));
    box.addChild(new Text("boxed text that wraps across several rows at narrow widths", 1, 0));
    box.addChild(opaque("custom"));
    const c = new Container(); c.addChild(box); c.addChild(new DynamicBorder());
    return c;
  },
  settings: () => {
    const list = new SettingsList([
      { id: "a", label: "Alpha", currentValue: "on", values: ["on", "off"], description: "The first setting" },
      { id: "b", label: "Beta", currentValue: "x", submenu: () => new Text("sub") },
      { id: "c", label: "Gamma", currentValue: "2" },
    ], 2, getSettingsListTheme(), () => {}, () => {});
    list.handleInput("\x1b[B");
    return list;
  },
  search: () => {
    const list = new SettingsList([
      { id: "a", label: "Alpha", currentValue: "on", description: "The first setting" },
      { id: "b", label: "Alpine", currentValue: "x" },
      { id: "c", label: "Gamma", currentValue: "2" },
    ], 3, getSettingsListTheme(), () => {}, () => {}, { enableSearch: true });
    for (const ch of "al") list.handleInput(ch);
    return list;
  },
  emphasis: () => new Markdown("Some *emphasis with **strong** inside* text\n# Heading\nmore\n***\nend\n\nsetext\n---\nafter\n> a *quoted* line\n> and **more**", 1, 0, getMarkdownTheme(), { color: (s) => t.fg("text", s) }),
  italic: () => new Markdown("Some *emphasis with **strong** inside* text\n> a *quoted* line", 1, 0, getMarkdownTheme(), { color: (s) => t.fg("text", s), italic: true }),
  loader: () => {
    const loader = new Loader(null, (s) => t.fg("accent", s), (s) => t.fg("muted", s), "Working");
    loader.stop(); loader.currentFrame = 4; loader.updateDisplay();
    return loader;
  },
  stacks: () => {
    const c = new Container();
    c.addChild(new VStack([new Text("top", 0, 0), { component: opaque("v"), maxSize: 1 }, new Spacer(1)], { gap: 1 }));
    c.addChild(new Markdown("# Heading\n\n1. **bold** item\n2. plain\n\n> quoted", 0, 1, getMarkdownTheme(), { color: (s) => t.fg("text", s) }));
    return c;
  },
  image: () => new Image(PNG, "image/png", { fallbackColor: (s) => t.fg("muted", s) }, { filename: "cover.png" }),
  // kit-conversation's components (§10), with the tool card the host's
  // test cards draw: a definition without renderers.
  conversation: () => {
    const c = new Container();
    c.addChild(new ca.UserMessageComponent("Fix **the** kit build\n\n- one\n- two"));
    const streaming = new ca.AssistantMessageComponent();
    streaming.updateContent({ content: [{ type: "thinking", thinking: "Reading the *kit* file" }, { type: "text", text: "Done. **Bold** reply\n\n1. a\n2. b" }] }, true);
    c.addChild(streaming);
    const failed = new ca.AssistantMessageComponent({ content: [{ type: "thinking", thinking: "secret" }, { type: "text", text: "Visible kit" }], stopReason: "error", errorMessage: "kit-boom-7" });
    failed.setHideThinkingBlock(true);
    failed.setHiddenThinkingLabel("Pondering kit...");
    failed.setOutputPad(0);
    c.addChild(failed);
    const tool = new ca.ToolExecutionComponent("kit_tool", "call-4", { q: "x" }, {}, {}, fakeUi, "/work/kit");
    tool.setArgsComplete();
    tool.markExecutionStarted();
    tool.updateResult({ content: [{ type: "text", text: "answer 42" }] }, false);
    c.addChild(tool);
    const exited = new ca.BashExecutionComponent("ls -la", fakeUi);
    exited.appendOutput("a.txt\n");
    exited.appendOutput("b.txt");
    exited.setComplete(2, false);
    c.addChild(exited);
    const seq = new ca.BashExecutionComponent("seq 25", fakeUi, true);
    seq.appendOutput(Array.from({ length: 25 }, (_, i) => String(i + 1)).join("\n"));
    seq.setComplete(0, false);
    c.addChild(seq);
    c.addChild(new Text(piCodingAgent.renderDiff(" 1 keep\n-2 old kit line\n+2 new kit line\n 3 tail", { filePath: "kit.go" }), 0, 0));
    return c;
  },
  running: () => {
    const running = new ca.BashExecutionComponent("sleep 9", fakeUi);
    running.appendOutput("waiting");
    running.loader.stop();
    running.loader.currentFrame = 5;
    running.loader.updateDisplay();
    return running;
  },
};
const frames = [];
for (const [name, build] of Object.entries(trees)) {
  for (const width of [30, 72, 120]) {
    const derived = derive(build(), width);
    assert.ok(derived.view, name + " has a view");
    if (name === "conversation") assert.deepEqual(derived.view.root.children.map((node) => node.kind),
      ["user-message", "assistant-message", "assistant-message", "tool-execution", "bash-execution", "bash-execution", "diff"]);
    if (name === "running") assert.equal(derived.view.root.kind, "bash-execution");
    const images = [...derived.images].map(([ref, image]) => ({ ref, ...image }));
    frames.push({ name, width, lines: derived.lines, view: images.length ? { ...derived.view, images } : derived.view });
  }
}
console.log(JSON.stringify(frames));
`, "PIG_VIEW_PALETTE="+string(palette), "PIG_VIEW_IMAGES="+string(images))
	var frames []struct {
		Name  string          `json:"name"`
		Width int             `json:"width"`
		Lines []string        `json:"lines"`
		View  json.RawMessage `json:"view"`
	}
	if err := json.Unmarshal(output, &frames); err != nil {
		t.Fatalf("decode frames: %v\n%s", err, output)
	}
	if len(frames) != 33 { // eleven trees at three widths
		t.Fatalf("got %d frames, want 33", len(frames))
	}
	for _, frame := range frames {
		s := newViewSurface("", newViewImageStore())
		if err := s.accept(frame.View, frame.Lines); err != nil {
			t.Fatalf("%s at %d: host rejected the view: %v\n%s", frame.Name, frame.Width, err, frame.View)
		}
		if view := s.FrontendView(frame.Width); view != nil {
			if frame.Name == "probe" && view.Theme["accent"] != "#d75f00" {
				t.Errorf("probe at %d: frontend theme %v, want the accent override", frame.Width, view.Theme)
			}
			continue
		}
		// Show the host's own rows for the view next to Node's.
		authoritative := newViewSurface("", newViewImageStore())
		host := []string{"(the view does not render authoritatively)"}
		if err := authoritative.accept(frame.View, nil); err == nil {
			host = authoritative.Render(frame.Width)
		}
		t.Errorf("%s at %d: the host dropped the view, its rows differ from Node's\nview: %s\nnode: %q\nhost: %q", frame.Name, frame.Width, frame.View, frame.Lines, host)
	}
}

// nodeModuleDirectoryURL is dir as the file: URL an ES module import needs. A Windows path such as D:\a\runtime-node
// is read as the unsupported URL scheme "d:", so the scripts import runtime-node through its file URL.
func nodeModuleDirectoryURL(dir string) string {
	return (&url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(dir), "/")}).String()
}
