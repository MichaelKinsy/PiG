package subprocess_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

// pinnedPiPackages is where `npm ci` in extensions/sdk-ts installs the Pi
// release named by coding.UpstreamVersion.
var pinnedPiPackages = filepath.Join("..", "..", "..", "..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent")

func readPinned(t *testing.T, parts ...string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(append([]string{pinnedPiPackages}, parts...)...))
	if err != nil {
		t.Fatalf("read the pinned Pi package (run npm ci in extensions/sdk-ts): %v", err)
	}
	return data
}

// The bundled TypeBox must be the release the pinned Pi depends on.
func TestVendoredTypeBoxMatchesThePinnedDependency(t *testing.T) {
	var manifest struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(readPinned(t, "package.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "automation", "gen", "vendor-typebox.sh"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^version="([^"]+)"$`).FindSubmatch(script)
	if match == nil {
		t.Fatal("automation/gen/vendor-typebox.sh does not declare version=")
	}
	if want := manifest.Dependencies["typebox"]; string(match[1]) != want {
		t.Fatalf("vendored TypeBox %s, pinned Pi depends on typebox %s: update automation/gen/vendor-typebox.sh and rerun it", match[1], want)
	}
}

// vendoredImportRewrites lists, per vendored file, the one import line
// automation/gen/vendor-pi-dist.sh points at a vendored third-party copy.
// Every other vendored Pi file is the pinned release byte for byte.
var vendoredImportRewrites = map[string][2]string{
	"pi-tui/utils.js": {`import { eastAsianWidth } from "get-east-asian-width";`,
		`import { eastAsianWidth } from "../../get-east-asian-width/index.js";`},
	"pi-ai/utils/json-parse.js": {`import { parse as partialParse } from "partial-json";`,
		`import { parse as partialParse } from "../../../partial-json/dist/index.js";`},
	"pi-ai/utils/typebox-helpers.js": {`import { Type } from "typebox";`,
		`import { Type } from "../../../typebox.mjs";`},
	"pi-coding-agent/utils/frontmatter.js": {`import { parse } from "yaml";`,
		`import { parse } from "../../../yaml/index.js";`},
}

// validation.js rewrites two imports.
var vendoredValidationRewrites = [][2]string{
	{`import { Compile } from "typebox/compile";`, `import { Compile } from "../../../typebox-compile.mjs";`},
	{`import { Value } from "typebox/value";`, `import { Value } from "../../../typebox-value.mjs";`},
}

// pinnedPackageDist is where each vendored package's dist/ lives in the
// pinned install, relative to pinnedPiPackages.
var pinnedPackageDist = map[string][]string{
	"pi-tui":          {"node_modules", "@earendil-works", "pi-tui", "dist"},
	"pi-ai":           {"node_modules", "@earendil-works", "pi-ai", "dist"},
	"pi-coding-agent": {"dist"},
}

// The runtime's Pi modules re-export Pi's own code copied from the pinned
// release: shims/pi-dist mirrors each package's dist/, and shims/yaml,
// shims/get-east-asian-width and shims/partial-json are the third-party
// releases Pi depends on. A pin change must re-vendor them.
func TestVendoredPiDistMatchesThePinnedPackage(t *testing.T) {
	for pkg, dist := range pinnedPackageDist {
		var manifest struct{ Version string }
		if err := json.Unmarshal(readPinned(t, append(dist[:len(dist)-1:len(dist)-1], "package.json")...), &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.Version != coding.UpstreamVersion {
			t.Fatalf("installed %s %s, pinned Pi %s: run npm ci in extensions/sdk-ts", pkg, manifest.Version, coding.UpstreamVersion)
		}
	}
	shims := filepath.Join("runtime-node", "shims")
	root := filepath.Join(shims, "pi-dist")
	vendored := map[string]bool{}
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, relErr := filepath.Rel(root, path)
			vendored[filepath.ToSlash(rel)] = true
			return relErr
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(vendored) < 50 {
		t.Fatalf("shims/pi-dist holds %d files: run automation/gen/vendor-pi-dist.sh", len(vendored))
	}
	for rel := range vendored {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if rel == "package.json" {
			if string(got) != "{\n  \"type\": \"module\"\n}\n" {
				t.Errorf("shims/pi-dist/package.json = %q", got)
			}
			continue
		}
		if rel == "pi-coding-agent/core/session-manager.js" {
			continue // a section, checked below
		}
		pkg, file, _ := strings.Cut(rel, "/")
		dist, ok := pinnedPackageDist[pkg]
		if !ok {
			t.Errorf("shims/pi-dist/%s is not under a vendored Pi package", rel)
			continue
		}
		want := readPinned(t, append(append([]string{}, dist...), strings.Split(file, "/")...)...)
		rewrites := [][2]string{}
		if rw, ok := vendoredImportRewrites[rel]; ok {
			rewrites = append(rewrites, rw)
		}
		if rel == "pi-ai/utils/validation.js" {
			rewrites = append(rewrites, vendoredValidationRewrites...)
		}
		for _, rw := range rewrites {
			line := []byte(rw[0] + "\n")
			if !bytes.Contains(want, line) {
				t.Errorf("pinned %s lacks %q: update automation/gen/vendor-pi-dist.sh", rel, rw[0])
			}
			want = bytes.Replace(want, line, []byte(rw[1]+"\n"), 1)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("shims/pi-dist/%s differs from the pinned release: run automation/gen/vendor-pi-dist.sh", rel)
		}
	}

	// session-manager.js keeps the pure section and the imports it uses.
	session, err := os.ReadFile(filepath.Join(root, "pi-coding-agent", "core", "session-manager.js"))
	if err != nil {
		t.Fatalf("%v: run automation/gen/vendor-pi-dist.sh", err)
	}
	pinnedSession := readPinned(t, "dist", "core", "session-manager.js")
	start := bytes.Index(pinnedSession, []byte("export const CURRENT_SESSION_VERSION"))
	fn := bytes.Index(pinnedSession, []byte("export function buildSessionContext"))
	if start < 0 || fn < start {
		t.Fatal("pinned session-manager.js lacks the vendored section")
	}
	end := fn + bytes.Index(pinnedSession[fn:], []byte("\n}\n")) + len("\n}\n")
	imports, body, found := bytes.Cut(session, []byte("export const CURRENT_SESSION_VERSION"))
	if !found || !bytes.Equal(append([]byte("export const CURRENT_SESSION_VERSION"), body...), pinnedSession[start:end]) {
		t.Error("shims/pi-dist/pi-coding-agent/core/session-manager.js section differs from the pinned release: run automation/gen/vendor-pi-dist.sh")
	}
	for line := range bytes.SplitSeq(bytes.TrimSpace(imports), []byte("\n")) {
		line = bytes.Replace(line, []byte(`from "../../../pi-ai.mjs";`), []byte(`from "@earendil-works/pi-ai";`), 1)
		if !bytes.Contains(pinnedSession, append(line, '\n')) {
			t.Errorf("session-manager.js import %q is not in the pinned release", line)
		}
	}

	// Third-party packages, file for file.
	nodeModules := filepath.Join(pinnedPiPackages, "node_modules")
	yamlFiles := map[string]string{
		"index.js":     filepath.Join(nodeModules, "yaml", "browser", "index.js"),
		"package.json": filepath.Join(nodeModules, "yaml", "browser", "package.json"),
		"LICENSE":      filepath.Join(nodeModules, "yaml", "LICENSE"),
	}
	distRoot := filepath.Join(nodeModules, "yaml", "browser", "dist")
	if err := filepath.WalkDir(distRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(distRoot, path)
		yamlFiles[filepath.ToSlash(filepath.Join("dist", rel))] = path
		return err
	}); err != nil {
		t.Fatalf("read the pinned yaml build (run npm ci in extensions/sdk-ts): %v", err)
	}
	eaw := filepath.Join(nodeModules, "get-east-asian-width")
	partial := filepath.Join(nodeModules, "partial-json")
	for dir, files := range map[string]map[string]string{
		"yaml": yamlFiles,
		"get-east-asian-width": {
			"index.js": filepath.Join(eaw, "index.js"), "lookup.js": filepath.Join(eaw, "lookup.js"),
			"lookup-data.js": filepath.Join(eaw, "lookup-data.js"), "utilities.js": filepath.Join(eaw, "utilities.js"),
			"license": filepath.Join(eaw, "license"), "package.json": filepath.Join(eaw, "package.json"),
		},
		"partial-json": {
			"dist/index.js": filepath.Join(partial, "dist", "index.js"), "dist/options.js": filepath.Join(partial, "dist", "options.js"),
			"LICENSE": filepath.Join(partial, "LICENSE"), "package.json": filepath.Join(partial, "package.json"),
		},
	} {
		count := 0
		if err := filepath.WalkDir(filepath.Join(shims, dir), func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				count++
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if count != len(files) {
			t.Errorf("shims/%s has %d files, want %d: run automation/gen/vendor-pi-dist.sh", dir, count, len(files))
		}
		for rel, path := range files {
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read the pinned %s (run npm ci in extensions/sdk-ts): %v", dir, err)
			}
			got, err := os.ReadFile(filepath.Join(shims, dir, filepath.FromSlash(rel)))
			if err != nil || !bytes.Equal(got, want) {
				t.Errorf("shims/%s/%s differs from the pinned dependency: run automation/gen/vendor-pi-dist.sh", dir, rel)
			}
		}
	}
}

// runPinnedComparison runs script under Node with the pinned package's entry
// point and the runtime's module as process.argv[1] and [2]. The script
// prints a difference and exits non-zero when the two disagree.
func runPinnedComparison(t *testing.T, pinnedEntry []string, shim, script string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required: %v", err)
	}
	readPinned(t, pinnedEntry...)
	pinned, err := filepath.Abs(filepath.Join(append([]string{pinnedPiPackages}, pinnedEntry...)...))
	if err != nil {
		t.Fatal(err)
	}
	shimPath, err := filepath.Abs(filepath.Join("runtime-node", "shims", shim))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), node, "--input-type=module", "--eval", script,
		"file://"+filepath.ToSlash(pinned), "file://"+filepath.ToSlash(shimPath))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}

// The runtime's pi-tui components render and handle input byte-for-byte like
// the pinned package's: extensions (pi-mcp-adapter, pi-rtk-optimizer,
// pi-web-access) compose panels from Box, Container, Text, Spacer and
// SettingsList, and prompt with Input, Editor and SelectList, resolving keys
// through the keybindings manager.
func TestPiTuiComponentsMatchThePinnedPackage(t *testing.T) {
	runPinnedComparison(t, []string{"node_modules", "@earendil-works", "pi-tui", "dist", "index.js"}, "pi-tui.mjs", `
const [pi, pig] = await Promise.all([import(process.argv[1]), import(process.argv[2])]);
const bg = (s) => "\x1b[44m" + s + "\x1b[49m";
const bold = (s) => "\x1b[1m" + s + "\x1b[22m";
const dim = (s) => "\x1b[2m" + s + "\x1b[22m";
const keys = {
  up: "\x1b[A", down: "\x1b[B", right: "\x1b[C", left: "\x1b[D", home: "\x1b[H", end: "\x1b[F",
  pageDown: "\x1b[6~", enter: "\r", escape: "\x1b", backspace: "\x7f", del: "\x1b[3~",
  ctrlA: "\x01", ctrlE: "\x05", ctrlK: "\x0b", ctrlU: "\x15", ctrlW: "\x17", ctrlY: "\x19",
  altB: "\x1bb", altF: "\x1bf", altD: "\x1bd", undo: "\x1f", newline: "\n", shiftEnter: "\x1b[13;2u",
  paste: (s) => "\x1b[200~" + s + "\x1b[201~",
};
async function scene(m) {
  const out = [];
  const log = (...v) => out.push(v);

  // Layout components.
  const box = new m.Box(2, 1, bg);
  box.addChild(new m.Text("Hello, \x1b[1mbold\x1b[22m wrapped text that runs past the width", 1, 0));
  box.addChild(new m.Spacer(2));
  const text = new m.Text("second", 0, 0);
  text.setCustomBgFn(bg);
  box.addChild(text);
  log(box.render(24));
  box.setBgFn(undefined);
  log(box.render(24));
  const spacer = new m.Spacer();
  spacer.setLines(3);
  const container = new m.Container();
  container.addChild(box);
  container.addChild(spacer);
  container.addChild(new m.TruncatedText("a line that is far too long for the width", 1, 0));
  log(container.render(30));
  container.clear();
  box.clear();
  log(container.render(30), box.render(30));
  log(new m.HStack([new m.Text("left", 0, 0), { component: new m.Text("right side", 0, 0), grow: 1 }], { gap: 1 }).render(20));
  log(new m.VStack([new m.Text("top", 0, 0), new m.Text("bottom", 0, 0)], { gap: 1 }).render(10));

  // KeybindingsManager.
  const kb = new m.KeybindingsManager(m.TUI_KEYBINDINGS, { "tui.select.up": ["k", "up"], "tui.select.down": "k", "mcp.panel.save": "ctrl+s" });
  log(kb.matches("k", "tui.select.up"), kb.matches(keys.up, "tui.select.up"), kb.matches(keys.down, "tui.select.down"),
    kb.getKeys("tui.select.up"), kb.getDefinition("tui.input.submit"), kb.getConflicts(), kb.getUserBindings(),
    kb.getResolvedBindings());
  kb.setUserBindings({});
  log(kb.matches(keys.down, "tui.select.down"), kb.getResolvedBindings()["tui.select.cancel"]);
  log(m.getKeybindings() instanceof m.KeybindingsManager, m.getKeybindings().matches(keys.escape, "tui.select.cancel"));
  log(m.fuzzyMatch("ctl", "control"), m.fuzzyFilter(["alpha", "beta", "alphabet"], "alp", (s) => s));

  // SelectList.
  const theme = { selectedPrefix: bold, selectedText: bold, description: dim, scrollInfo: dim, noMatch: dim };
  const items = Array.from({ length: 9 }, (_, i) => ({ value: "item" + i, label: "Item " + i, description: i % 2 ? "odd description " + i : undefined }));
  const list = new m.SelectList(items, 4, theme, { minPrimaryColumnWidth: 8 });
  list.onSelect = (item) => log("select", item.value);
  list.onCancel = () => log("cancel");
  list.onSelectionChange = (item) => log("change", item.value);
  log(list.render(40));
  for (const k of [keys.down, keys.down, keys.up, keys.pageDown, keys.down, keys.down, keys.down, keys.down, keys.down]) {
    list.handleInput(k);
    log(list.getSelectedItem()?.value, list.render(40));
  }
  list.handleInput(keys.enter);
  list.setFilter("item1");
  log(list.render(40));
  list.setFilter("zzz");
  log(list.render(40), list.getSelectedItem());
  list.setFilter("");
  list.setSelectedIndex(6);
  log(list.render(24));
  list.handleInput(keys.escape);

  // Input.
  const input = new m.Input();
  input.focused = true;
  input.onSubmit = (v) => log("submit", v);
  input.onEscape = () => log("escape");
  for (const ch of "hello brave new world") input.handleInput(ch);
  log(input.getValue(), input.render(12));
  for (const k of [keys.altB, keys.altB, keys.ctrlW, keys.left, "X", keys.ctrlA, keys.del, keys.altF, keys.altD, keys.ctrlE,
    keys.ctrlU, keys.ctrlY, keys.undo, keys.paste("pasted\ntext"), keys.home, keys.ctrlK, keys.ctrlY, keys.backspace, keys.right]) {
    input.handleInput(k);
    log(input.getValue(), input.render(12), input.render(40));
  }
  input.setValue("set value");
  input.focused = false;
  log(input.render(20));
  input.handleInput(keys.enter);
  input.handleInput(keys.escape);

  // Editor.
  const tui = { requestRender() {}, terminal: { rows: 24, columns: 80 } };
  const editor = new m.Editor(tui, { borderColor: dim, selectList: theme }, { paddingX: 1 });
  editor.focused = true;
  editor.onSubmit = (v) => log("editor submit", v);
  editor.onChange = (v) => log("editor change", v);
  for (const ch of "first line") editor.handleInput(ch);
  editor.handleInput(keys.shiftEnter);
  for (const ch of "second line that is long enough to wrap") editor.handleInput(ch);
  editor.handleInput(keys.newline);
  editor.handleInput(keys.paste("a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl"));
  log(editor.getText(), editor.getExpandedText(), editor.getLines(), editor.getCursor(), editor.render(24));
  for (const k of [keys.up, keys.up, keys.ctrlA, keys.altF, keys.ctrlK, keys.down, keys.ctrlE, keys.backspace, keys.ctrlW, keys.ctrlY, keys.undo]) {
    editor.handleInput(k);
    log(editor.getText(), editor.getCursor(), editor.render(24));
  }
  editor.insertTextAtCursor("inserted");
  log(editor.getText(), editor.getCursor());
  editor.addToHistory("older prompt");
  editor.addToHistory("newer prompt");
  editor.setText("");
  editor.handleInput(keys.up);
  log(editor.getText());
  editor.handleInput(keys.up);
  log(editor.getText(), editor.render(30));
  editor.handleInput(keys.enter);
  log(editor.getText());
  editor.setText("line one\nline two");
  log(editor.getLines(), editor.getCursor(), editor.render(20));

  // SettingsList with search: filtering goes through Input and fuzzyFilter.
  const settings = new m.SettingsList(
    [{ id: "a", label: "Alpha", currentValue: "on", values: ["on", "off"], description: "The alpha setting" },
     { id: "b", label: "Beta", currentValue: "x", values: ["x", "y", "z"] },
     { id: "c", label: "Gamma", currentValue: "1" }],
    5, { label: (s, sel) => (sel ? bold(s) : s), value: (s) => s, description: dim, cursor: "> ", hint: dim },
    (id, value) => log("setting", id, value), () => log("settings cancel"), { enableSearch: true });
  log(settings.render(40));
  for (const k of [keys.down, keys.enter, " ", "g", "a", keys.backspace, keys.backspace, keys.up, keys.enter, keys.escape]) {
    settings.handleInput(k);
    log(settings.render(40));
  }

  // StdinBuffer splits raw input into key sequences and pastes.
  const stdin = new m.StdinBuffer();
  stdin.on("data", (d) => log("data", d));
  stdin.on("paste", (d) => log("paste", d));
  stdin.process("a\x1b[A\x1b[13;2ub" + keys.paste("pasted") + "\x1bb");
  stdin.destroy();

  // CombinedAutocompleteProvider slash-command suggestions.
  const provider = new m.CombinedAutocompleteProvider([{ name: "help", description: "Show help" }, { name: "hello" }], "/");
  log(await provider.getSuggestions(["/he"], 0, 3, { signal: new AbortController().signal }));
  return JSON.stringify(out);
}
const want = await scene(pi), got = await scene(pig);
if (want !== got) { console.log("pinned: " + want + "\npig:    " + got); process.exit(1); }
`)
}

// The runtime's pi-ai utilities behave exactly like the pinned package's.
func TestPiAiUtilitiesMatchThePinnedPackage(t *testing.T) {
	runPinnedComparison(t, []string{"node_modules", "@earendil-works", "pi-ai", "dist", "index.js"}, "pi-ai.mjs", `
const [pi, pig] = await Promise.all([import(process.argv[1]), import(process.argv[2])]);
const model = { id: "m", provider: "p", api: "anthropic-messages", reasoning: true, thinkingLevelMap: { xhigh: "x", minimal: null },
  cost: { input: 3, output: 15, cacheRead: 0.3, cacheWrite: 3.75, tiers: [{ inputTokensAbove: 200000, input: 6, output: 22.5, cacheRead: 0.6, cacheWrite: 7.5 }] } };
const usage = (input) => ({ input, output: 1000, cacheRead: 500, cacheWrite: 300, cacheWrite1h: 100, cost: {} });
const err = new Error("boom", { cause: new TypeError("inner") });
err.status = 503;
async function scene(m) {
  const out = [];
  const log = (...v) => out.push(v);
  const safe = (fn) => { try { return fn(); } catch (e) { return "throws " + e.message; } };
  for (const s of ['{"a":1', '{"a": "x\ny"}', "{'a': 1,}", '[1, 2, {"b": tr', "", "not json"]) {
    log(safe(() => m.repairJson(s)), safe(() => m.parseJsonWithRepair(s)), safe(() => m.parseStreamingJson(s)));
  }
  log(m.calculateCost(model, usage(1000)), m.calculateCost(model, usage(300000)));
  log(m.getSupportedThinkingLevels(model), m.getSupportedThinkingLevels({ ...model, reasoning: false }),
    ["off", "minimal", "max", "bogus"].map((l) => m.clampThinkingLevel(model, l)));
  log(m.modelsAreEqual(model, { id: "m", provider: "p" }), m.modelsAreEqual(model, undefined), m.hasApi(model, "anthropic-messages"));
  const failed = (errorMessage, stopReason = "error") => ({ role: "assistant", content: [], stopReason, errorMessage,
    usage: { input: 190000, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 190000 } });
  for (const msg of [failed("prompt is too long: 250000 tokens > 200000 maximum"), failed("Overloaded"), failed("rate limit exceeded (429)"),
    failed("", "length"), failed("fetch failed: ECONNRESET"), failed("invalid api key")]) {
    log(m.isContextOverflow(msg, 200000), m.isRecoverableLength(msg, 8192), m.isRetryableAssistantError(msg));
  }
  log(m.getOverflowPatterns().map(String), m.DEFAULT_MAX_AGENT_RETRY_DELAY_MS);
  for (const attempt of [1, 2, 5, 12]) log(m.retryDelayMs({ baseDelayMs: 1000, maxDelayMs: 30000 }, attempt));
  log(m.formatThrownValue(err), m.formatThrownValue("plain"), m.formatThrownValue({ x: 1 }), m.extractDiagnosticError(err));
  const diag = m.createAssistantMessageDiagnostic("provider_error", err, { attempt: 2 });
  delete diag.timestamp;
  const message = m.fauxAssistantMessage([m.fauxText("hi"), m.fauxThinking("hmm"), m.fauxToolCall("read", { path: "a" }, { id: "t1" })],
    { timestamp: 1, stopReason: "toolUse" });
  m.appendAssistantMessageDiagnostic(message, diag);
  log(diag, message, m.fauxAssistantMessage("text", { timestamp: 2 }));

  const stream = m.createAssistantMessageEventStream();
  const partial = m.fauxAssistantMessage([], { timestamp: 3 });
  stream.push({ type: "start", partial });
  stream.push({ type: "text_start", contentIndex: 0, partial });
  stream.push({ type: "text_delta", contentIndex: 0, delta: "hel", partial });
  const done = m.fauxAssistantMessage("hello", { timestamp: 3 });
  stream.push({ type: "done", reason: "stop", message: done });
  const events = [];
  for await (const e of stream) events.push(e.type);
  log(events, await stream.result(), stream instanceof m.AssistantMessageEventStream, stream instanceof m.EventStream);

  const encoder = new m.AssistantMessageFrameEncoder();
  const frames = [];
  const at = (content, stopReason = "pending") => ({ ...m.fauxAssistantMessage(content, { timestamp: 4 }), stopReason });
  const call = (args) => m.fauxToolCall("read", args, { id: "t2" });
  for (const e of [
    { type: "start", partial: at([]) },
    { type: "text_start", contentIndex: 0, partial: at([m.fauxText("")]) },
    { type: "text_delta", contentIndex: 0, delta: "rea", partial: at([m.fauxText("rea")]) },
    { type: "text_end", contentIndex: 0, content: "reading", partial: at([m.fauxText("reading")]) },
    { type: "toolcall_start", contentIndex: 1, partial: at([m.fauxText("reading"), call({})]) },
    { type: "toolcall_delta", contentIndex: 1, delta: '{"path":"a', partial: at([m.fauxText("reading"), call({ path: "a" })]) },
    { type: "toolcall_end", contentIndex: 1, toolCall: call({ path: "a.go" }), partial: at([m.fauxText("reading"), call({ path: "a.go" })]) },
    { type: "done", reason: "toolUse", message: at([m.fauxText("reading"), call({ path: "a.go" })], "toolUse") },
  ]) {
    frames.push(safe(() => encoder.encode(e)));
  }
  log(frames, safe(() => m.reduceAssistantMessageFrames(frames.flat().filter(Boolean))));

  const tool = { name: "read", description: "Read", parameters: { type: "object", properties: { path: { type: "string" }, limit: { type: "integer" } }, required: ["path"] } };
  for (const args of [{ path: "a" }, { path: "a", limit: "5" }, { limit: 1 }, { path: 1 }]) {
    const call = { type: "toolCall", id: "c", name: "read", arguments: args };
    log(safe(() => m.validateToolArguments(tool, call)), safe(() => m.validateToolCall([tool], call)));
  }
  log(safe(() => m.validateToolCall([tool], { type: "toolCall", id: "c", name: "missing", arguments: {} })));
  log(JSON.stringify(m.StringEnum(["a", "b"], { description: "d", default: "a", title: "t" })), /^[0-9a-f-]{36}$/.test(m.uuidv7(5)), m.uuidv7(5).slice(0, 14));
  return JSON.stringify(out);
}
const want = await scene(pi), got = await scene(pig);
if (want !== got) { console.log("pinned: " + want + "\npig:    " + got); process.exit(1); }
`)
}
