package subprocess_test

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
)

// An editor component an extension installs with ctx.ui.setEditorComponent is
// Pi's own CustomEditor running in the extension process, wired the way Pi's
// setCustomEditorComponent wires it: the factory receives the TUI, Pi's editor
// theme and Pi's keybindings (the host's table); the editor renders exactly as
// Pi's CustomEditor does; its onChange, onSubmit, onEscape, onCtrlD and app
// action handlers and onExtensionShortcut reach the host as editor notifies;
// the host's keys and text operations reach the editor; its autocomplete asks
// the host; and it drives the terminal cursor through tui.terminal.write.
func TestNodeEditorComponentIsPisCustomEditor(t *testing.T) {
	abs := func(rel string) string {
		path, err := filepath.Abs(rel)
		if err != nil {
			t.Fatal(err)
		}
		return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
	}
	readPinned(t, "dist", "core", "keybindings.js")
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
const { Runtime } = await import(%q);
const pigAgent = await import(%q);
const pigTui = await import(%q);
const piKeybindings = await import(%q);
const piCustomEditor = await import(%q);
const piTui = await import(%q);
const piStatus = await import(%q);

// The host's table: Pi's KEYBINDINGS with a user override.
const definitions = {};
for (const [id, definition] of Object.entries(piKeybindings.KEYBINDINGS)) {
  definitions[id] = { defaultKeys: [definition.defaultKeys].flat(), description: definition.description };
}
const runtime = new Runtime("/ext/modal.mjs");
runtime.ready = { width: 60, height: 24, cwd: process.cwd() };
const sent = [];
runtime.notify = (method, args) => sent.push({ method, args });
const calls = [];
runtime.call = async (method, args) => {
  calls.push({ method, args });
  return { prefix: "/", items: [{ value: "model", label: "model", description: "Select model" }] };
};
runtime.callSync = (method,args) => {
  assert.equal(method,"ui.autocomplete.current");
  return {id:"base",triggerCharacters:[],hasFileTrigger:false};
};
runtime.fireAndForget = () => {};
runtime.applyState({ thinkingLevel: "high", keybindings: { definitions, userBindings: { "app.model.select": ["ctrl+y"] } } });
runtime.ui.theme.setPalette({ name: "t", foregrounds: { borderMuted: "\x1b[31m", thinkingHigh: "\x1b[32m", bashMode: "\x1b[33m", accent: "\x1b[34m", muted: "\x1b[35m", thinkingOff: "\x1b[36m" }, backgrounds: {}, modifiers: true, mode: "truecolor" });

class Modal extends pigAgent.CustomEditor {
  handleInput(data) {
    if (data === "!") { this.mode = "normal"; return; }
    super.handleInput(data);
  }
}
let factoryArgs;
runtime.ui.setEditorComponent((tui, theme, keybindings) => {
  factoryArgs = { tui, theme, keybindings };
  return new Modal(tui, theme, keybindings);
});
const take = (method) => sent.filter((m) => m.method === method);
const install = take("ui.editor.install")[0];
assert.ok(install, "install notify");
const key = install.args.key;
const editor = runtime.editorHost.session.component;
assert.ok(editor instanceof pigTui.Editor);
assert.equal(factoryArgs.keybindings, pigTui.getKeybindings());
assert.ok(factoryArgs.keybindings.matches("\x19", "app.model.select"), "user override");
assert.ok(factoryArgs.keybindings.matches("\x1b[Z", "app.thinking.cycle"), "app definitions");
assert.equal(factoryArgs.theme.borderColor("x"), "\x1b[31mx\x1b[39m");
assert.equal(editor.borderColor("x"), "\x1b[32mx\x1b[39m", "thinking border color");
assert.equal(editor.focused, true);
assert.equal(take("ui.editor.render").length, 0, "no frame before the host configures");

runtime.handleNotify({ method: "ui.editor.configure", args: { key, paddingX: 1, autocompleteMaxVisible: 7, focused: true, thinkingLevel: "high", shortcuts: ["ctrl+k"] } });
runtime.handleNotify({ method: "ui.editor.setText", args: { key, text: "draft" } });
let frames = take("ui.editor.render");
assert.ok(frames.length >= 1);
let frame = frames.at(-1).args;
assert.equal(frame.width, 60);

// Pi's CustomEditor in the same state renders the same rows.
const piTheme = { borderColor: (s) => "\x1b[32m" + s + "\x1b[39m", selectList: factoryArgs.theme.selectList };
const piEditor = new piCustomEditor.CustomEditor({ requestRender() {}, terminal: { rows: 24, columns: 60 } }, piTheme, factoryArgs.keybindings);
piEditor.focused = true;
piEditor.setPaddingX(1);
piEditor.setAutocompleteMaxVisible(7);
piEditor.setText("draft");
assert.deepEqual(frame.lines, piEditor.render(60));
assert.ok(frame.lines.some((line) => line.includes(pigTui.CURSOR_MARKER)));

// Keys the host forwards reach handleInput; text changes reach the host.
runtime.handleNotify({ method: "ui.editor.input", args: { key, data: "s" } });
const changes = take("ui.editor.change");
assert.deepEqual(changes.at(-1).args, { key, text: "drafts", expanded: "drafts" });

// Enter submits through onSubmit; the promise settles when the host is done.
let settled = false;
const submitted = editor.onSubmit("run it").then(() => { settled = true; });
const submit = take("ui.editor.submit").at(-1).args;
assert.deepEqual(submit, { key, id: submit.id, text: "run it" });
await Promise.resolve();
assert.equal(settled, false);
runtime.handleNotify({ method: "ui.editor.submitted", args: { key, id: submit.id } });
await submitted;
runtime.handleNotify({ method: "ui.editor.setText", args: { key, text: "" } });
runtime.handleNotify({ method: "ui.editor.input", args: { key, data: "h" } });
runtime.handleNotify({ method: "ui.editor.input", args: { key, data: "\r" } });
assert.deepEqual(take("ui.editor.submit").at(-1).args.text, "h");

// Pi's handleCtrlC -> clearEditor calls setText before CustomEditor returns.
// A subsequent key must survive even before the host processes the action.
runtime.handleNotify({ method: "ui.editor.setText", args: { key, text: "seed" } });
runtime.handleNotify({ method: "ui.editor.input", args: { key, data: "\x03" } });
assert.equal(editor.getText(), "", "app.clear completes synchronously");
runtime.handleNotify({ method: "ui.editor.input", args: { key, data: "next" } });
assert.equal(editor.getText(), "next", "clear does not erase the next input");
runtime.handleNotify({ method: "ui.editor.setText", args: { key, text: "" } });
sent.length = 0;

runtime.editorHost.lastSigintTime = 0;
// App keys reach Pi's handlers through the editor's CustomEditor.
const actions = () => take("ui.editor.action").map((m) => m.args.action);
runtime.handleNotify({ method: "ui.editor.input", args: { key, data: "\x1b[Z" } });
runtime.handleNotify({ method: "ui.editor.input", args: { key, data: "\x19" } });
runtime.handleNotify({ method: "ui.editor.input", args: { key, data: "\x03" } });
runtime.handleNotify({ method: "ui.editor.input", args: { key, data: "\x1b" } });
runtime.handleNotify({ method: "ui.editor.input", args: { key, data: "\x04" } });
assert.deepEqual(actions(), ["app.thinking.cycle", "app.model.select", "app.clear", "app.interrupt", "app.exit"]);
runtime.handleNotify({ method: "ui.editor.input", args: { key, data: "\x0b" } });
assert.deepEqual(take("ui.editor.shortcut").at(-1).args, { key, data: "\x0b" });

// A leading "!" is bash mode (Pi's defaultEditor.onChange), recoloring the border.
runtime.handleNotify({ method: "ui.editor.setText", args: { key, text: "!ls" } });
assert.equal(editor.borderColor("x"), "\x1b[33mx\x1b[39m");
runtime.handleNotify({ method: "ui.editor.setText", args: { key, text: "" } });
runtime.handleNotify({ method: "ui.editor.configure", args: { key, paddingX: 1, autocompleteMaxVisible: 7, focused: false, thinkingLevel: "off", shortcuts: [] } });
assert.equal(editor.borderColor("x"), "\x1b[36mx\x1b[39m", "thinking level change");
assert.equal(editor.focused, false);

// Autocomplete asks the host and shows its items.
runtime.handleNotify({ method: "ui.editor.input", args: { key, data: "/" } });
for (let i = 0; i < 50 && !take("ui.editor.render").at(-1).args.lines.some((l) => l.includes("Select model")); i++) {
  await new Promise((resolve) => setTimeout(resolve, 10));
}
assert.equal(calls[0].method, "ui.autocomplete.invoke");
assert.match(calls[0].args.queryId,/^[0-9a-f-]{36}$/);
assert.deepEqual(calls[0].args, { id:"base",operation:"getSuggestions",queryId:calls[0].args.queryId, lines: ["/"], cursorLine: 0, cursorCol: 1, force: false });
assert.ok(take("ui.editor.render").at(-1).args.lines.some((l) => l.includes("Select model")));

// The editor drives the terminal cursor through its TUI.
factoryArgs.tui.terminal.write("\x1b[5 q");
factoryArgs.tui.setShowHardwareCursor(true);
assert.deepEqual(take("ui.terminal.write").at(-1).args, { key, data: "\x1b[5 q" });
assert.deepEqual(take("ui.setShowHardwareCursor").at(-1).args, { key, enabled: true });
assert.equal(factoryArgs.tui.getShowHardwareCursor(), true);
assert.equal(runtime.ui.getEditorComponent() !== undefined, true);

// A replaced editor no longer receives the host's keys.
runtime.handleNotify({ method: "ui.editor.closed", args: { key } });
const before = editor.getText();
runtime.handleNotify({ method: "ui.editor.input", args: { key, data: "z" } });
assert.equal(editor.getText(), before);
runtime.ui.setEditorComponent(undefined);
assert.equal(take("ui.editor.clear").length, 1);
assert.equal(runtime.ui.getEditorComponent(), undefined);

// Pi's isWorkingStatusEditor requires both the explicit opt-in and the setter.
assert.equal(install.args.embedWorkingStatus, false);
runtime.ui.setEditorComponent((tui, theme, keys) => new pigAgent.CustomEditor(tui, theme, keys, { embedWorkingStatus: true }));
const embeddedKey = take("ui.editor.install").at(-1).args.key;
assert.equal(take("ui.editor.install").at(-1).args.embedWorkingStatus, true);
const statusConfig = { key: embeddedKey, paddingX: 1, focused: true, thinkingLevel: "off", shortcuts: [],
  workingStatus: { kind: "working", message: "Indexing wide 文本", frames: ["A", "B"], frame: 5,
    spinnerColor: "\x1b[34m", messageColor: "\x1b[35m", indicatorVerbatim: false } };
runtime.handleNotify({ method: "ui.editor.configure", args: statusConfig });
assert.match(take("ui.editor.render").at(-1).args.lines[0], /── .*B.*Indexing wide 文本/);
assert.equal(runtime.editorHost.session.component.workingStatusIndicator.intervalId, null, "only the host owns animation");
const oracleEditor = new piCustomEditor.CustomEditor({ requestRender() {}, terminal: { rows: 24, columns: 60 } },
  { ...piTheme, borderColor: (text) => "\x1b[36m" + text + "\x1b[39m" }, factoryArgs.keybindings, { embedWorkingStatus: true });
oracleEditor.setPaddingX(1);
oracleEditor.focused = true;
const oracleStatus = new piStatus.StatusIndicator("working", undefined,
  (text) => "\x1b[34m" + text + "\x1b[0m", (text) => "\x1b[35m" + text + "\x1b[0m",
  "Indexing wide 文本", { frames: [] });
oracleStatus.frames = ["A", "B"];
oracleStatus.currentFrame = 1;
oracleStatus.renderIndicatorVerbatim = false;
oracleStatus.invalidate();
oracleEditor.setWorkingStatusIndicator(oracleStatus);
for (const text of ["", Array.from({ length: 40 }, (_, i) => "line " + i).join("\n")]) {
  oracleEditor.setText(text);
  runtime.handleNotify({ method: "ui.editor.setText", args: { key: embeddedKey, text } });
  for (const width of [4, 12, 40, 60]) {
    runtime.ready.width = width;
    runtime.handleNotify({ method: "ui.editor.configure", args: statusConfig });
    assert.deepEqual(take("ui.editor.render").at(-1).args.lines, oracleEditor.render(width), "Pi border rows at width " + width);
  }
}
runtime.handleNotify({ method: "ui.editor.setText", args: { key: embeddedKey, text: "" } });
statusConfig.workingStatus.frame = 0;
statusConfig.workingStatus.message = "Compacting context... (escape to cancel)";
runtime.handleNotify({ method: "ui.editor.configure", args: statusConfig });
assert.match(take("ui.editor.render").at(-1).args.lines[0], /── .*A.*Compacting context/);
statusConfig.workingStatus.frames = [];
runtime.handleNotify({ method: "ui.editor.configure", args: statusConfig });
assert.match(take("ui.editor.render").at(-1).args.lines[0], /── .*Compacting context/);
statusConfig.workingStatus = null;
runtime.handleNotify({ method: "ui.editor.configure", args: statusConfig });
assert.doesNotMatch(take("ui.editor.render").at(-1).args.lines[0], /Working|Indexing|Compacting/);
runtime.ui.setEditorComponent((tui, theme, keys) => {
  const component = new pigAgent.CustomEditor(tui, theme, keys, { embedWorkingStatus: true });
  component.setWorkingStatusIndicator = undefined;
  return component;
});
assert.equal(take("ui.editor.install").at(-1).args.embedWorkingStatus, false);
runtime.ui.setEditorComponent(undefined);
`, abs("runtime-node/runtime.mjs"), abs("runtime-node/shims/pi-coding-agent.mjs"), abs("runtime-node/shims/pi-tui.mjs"),
		abs(filepath.Join(pinnedPiPackages, "dist", "core", "keybindings.js")),
		abs(filepath.Join(pinnedPiPackages, "dist", "modes", "interactive", "components", "custom-editor.js")),
		abs(filepath.Join(pinnedPiPackages, "node_modules", "@earendil-works", "pi-tui", "dist", "index.js")),
		abs(filepath.Join(pinnedPiPackages, "dist", "modes", "interactive", "components", "status-indicator.js")))
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("editor component: %v\n%s", err, output)
	}
}
