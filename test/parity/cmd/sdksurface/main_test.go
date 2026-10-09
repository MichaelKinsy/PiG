package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

const repoRoot = "../../../.."

// TestExtensionSDKSurfaceMatrixIsCurrent regenerates the matrix from Pi's
// types.ts and every runtime's source and fails when the checked-in copy has
// drifted, when a missing cell has no reviewed exception, or when an
// exception or map entry no longer names a live surface or missing cell.
func TestExtensionSDKSurfaceMatrixIsCurrent(t *testing.T) {
	m, err := buildMatrix(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	got := m.render()
	want, err := os.ReadFile(filepath.Join(repoRoot, matrixPath))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is stale: run `go run ./test/parity/cmd/sdksurface`", matrixPath)
	}
	if err := m.validate(); err != nil {
		t.Fatal(err)
	}
}

// TestSurfaceCoversPiExtensionAPI pins the rows the generator must derive from
// types.ts, so a parser regression that silently drops a declaration fails.
func TestSurfaceCoversPiExtensionAPI(t *testing.T) {
	m, err := buildMatrix(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, r := range m.rows {
		keys[r.Key] = true
	}
	for _, want := range []string{
		"pi.registerTool", "pi.sendMessage", "pi.sendUserMessage", "pi.appendEntry", "pi.setLabel",
		"pi.exec", "pi.registerProvider", "pi.events.emit", "pi.events.on",
		`pi.on("session_start")`, `pi.on("tool_call")`, `pi.on("input")`, `pi.on("agent_before_settle")`,
		"session_before_switch return.cancel", "tool_call event.input", "input return.action",
		"ctx.ui.select", "ctx.ui.theme", "ctx.ui.theme.fg", "ctx.ui.custom(options.overlayOptions)",
		"ctx.newSession", "ctx.reload", "withSession ctx.sendMessage",
		"ctx.sessionManager.getBranch", "ctx.modelRegistry.find",
		"tool.promptSnippet", "tool.renderCall", "tool render context.invalidate",
		"command.getArgumentCompletions", "provider.oauth.login", "provider model.contextWindow",
	} {
		if !keys[want] {
			t.Errorf("surface lacks %q", want)
		}
	}
	events := 0
	for _, r := range m.rows {
		if r.Kind == kindEvent {
			events++
		}
	}
	// The denominator is every `on(event: "<name>"` overload of the ExtensionAPI
	// interface, counted straight from the pinned types.ts.
	source, err := os.ReadFile(filepath.Join(repoRoot, ".upstream", "v"+coding.UpstreamVersion, "packages/coding-agent/src/core/extensions/types.ts"))
	if err != nil {
		t.Fatal(err)
	}
	_, api, ok := strings.Cut(string(source), "export interface ExtensionAPI {")
	if !ok {
		t.Fatal("types.ts declares no ExtensionAPI")
	}
	// The interface body ends at the first closing brace in column 0; later
	// declarations in the file must not enter the denominator.
	if api, _, ok = strings.Cut(api, "\n}\n"); !ok {
		t.Fatal("types.ts ExtensionAPI has no closing brace")
	}
	declared := map[string]bool{}
	for _, match := range regexp.MustCompile(`\bon\(\s*event:\s*"([^"]+)"`).FindAllStringSubmatch(api, -1) {
		declared[match[1]] = true
	}
	if events != len(declared) {
		t.Errorf("types.ts ExtensionAPI.on declares %d events, surface has %d", len(declared), events)
	}
}

func TestTypeScriptFieldExpansion(t *testing.T) {
	m := newTSModule()
	for _, d := range parseTSDecls(stripTSComments(`
export interface Base { a: string; b?: number }
export interface Child extends Base {
	/** doc */
	c(x: string): void;
	d: (y: number) => string;
}
export type U = { k: "x" } | { k: "y"; extra: boolean };
export type P = Pick<Child, "a" | "c">;
export type O = Omit<Child, "a">;
class Hidden { }
export class Klass {
	public name: string;
	private secret = 1;
	get label(): string { return ""; }
	method(arg: string): void {
		if (arg) { return; }
	}
	static make(): Klass { return new Klass(); }
}
`)) {
		m.decls[d.Name] = d
	}
	names := func(expr string) []string {
		fs, err := m.fields(expr)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, f := range fs {
			out = append(out, f.Name)
		}
		return out
	}
	for expr, want := range map[string][]string{
		"Child":                   {"c", "d", "a", "b"},
		"U":                       {"k", "extra"},
		"P":                       {"c", "a"},
		"O":                       {"c", "d", "b"},
		"Klass":                   {"name", "label", "method"},
		"{ x?: number; y: T }":    {"x", "y"},
		`Pick<Base, "b">`:         {"b"},
		"Child | undefined":       {"c", "d", "a", "b"},
		"(Base) | { z: string }":  {"a", "b", "z"},
		"Base & { extra: never }": {"a", "b", "extra"},
	} {
		if got := names(expr); !slices.Equal(got, want) {
			t.Errorf("fields(%q) = %v, want %v", expr, got, want)
		}
	}
	child := m.decls["Child"]
	if !child.Members[0].Method || child.Members[1].Method || !isCallback(child.Members[1]) {
		t.Errorf("Child members classified wrong: %+v", child.Members)
	}
}

func TestRuntimeSymbolScanners(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("lib.rs", `
pub const EVENT_A: &str = "a { }";
pub struct Thing {
    pub field_one: String,
    hidden: u8,
}
impl Thing {
    pub fn method(&self) -> char { '{' }
    fn private(&self) {}
}
pub mod events {
    pub const INNER: &str = "x";
}
pub trait Hook {
    fn call(&self);
}
`)
	rs, err := parseRust(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"EVENT_A", "Thing", "Thing.field_one", "Thing::method", "events::INNER", "Hook::call"} {
		if !rs.has(want) {
			t.Errorf("rust scanner lacks %s in %v", want, rs.sorted())
		}
	}
	for _, not := range []string{"Thing.hidden", "Thing::private"} {
		if rs.has(not) {
			t.Errorf("rust scanner recorded private %s", not)
		}
	}

	pyDir := filepath.Join(dir, "py")
	if err := os.Mkdir(pyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pyDir, "__init__.py"), []byte(`
EVENT_A = "a"
class Thing:
    """Doc."""

    field: int = 0

    def method(self, a, *, keyword=None,
               other: dict[str, int] | None = None) -> None:
        self.attr = 1
`), 0o644); err != nil {
		t.Fatal(err)
	}
	py, err := parsePython(pyDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"EVENT_A", "Thing", "Thing.field", "Thing.method", "Thing.method(a)", "Thing.method(keyword)", "Thing.method(other)", "Thing.attr"} {
		if !py.has(want) {
			t.Errorf("python scanner lacks %s in %v", want, py.sorted())
		}
	}

	js := write("runtime.mjs", "const re = /[{]/g;\nconst s = `${tool.label} {`;\nfunction f(cmd) { return cmd?.getArgumentCompletions; }\n")
	node, err := parseNodeReads([]string{js})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"read:tool.label", "read:cmd.getArgumentCompletions"} {
		if !node.has(want) {
			t.Errorf("node read scanner lacks %s in %v", want, node.sorted())
		}
	}
}

// The component kit (D107) lives in a module of each SDK; its symbols are
// qualified by that module, and an SDK without one yet adds none.
func TestKitSymbolsAreModuleQualified(t *testing.T) {
	root := t.TempDir()
	for path, src := range map[string]string{
		"extensions/sdk/kit/kit.go":          "package kit\n\ntype View struct{ Focus string }\n\nfunc NewContainer() {}\n",
		"extensions/sdk-rs/src/kit/mod.rs":   "pub struct Container {}\nimpl Container {\n    pub fn new() -> Self { Container {} }\n}\n",
		"extensions/sdk-py/pig_sdk/kit.py":   "class Container:\n    def view(self, width):\n        pass\n\n\nclass Stub(Protocol):\n    def view(self, width: int) -> View: ...\n\n\nclass Next(Protocol):\n    def handle(self, event: Event) -> Any: ...\n",
		"extensions/sdk-py/pig_sdk/other.py": "class Other:\n    pass\n",
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	goSyms, rs, py := symbolSet{}, symbolSet{}, symbolSet{}
	if err := addKitSymbols(root, goSyms, rs, py); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		set  symbolSet
		want []string
	}{
		{goSyms, []string{"kit.NewContainer", "kit.View", "kit.View.Focus"}},
		{rs, []string{"kit::Container", "kit::Container::new"}},
		{py, []string{"kit.Container", "kit.Container.view", "kit.Container.view(width)", "kit.Stub.view(width)", "kit.Next", "kit.Next.handle(event)"}},
	} {
		for _, sym := range check.want {
			if !check.set.has(sym) {
				t.Errorf("kit symbols lack %s in %v", sym, check.set.sorted())
			}
		}
	}
	if py.has("kit.Other") || py.has("Container") {
		t.Errorf("python kit symbols are not limited to the qualified kit module: %v", py.sorted())
	}
	empty := symbolSet{}
	if err := addKitSymbols(t.TempDir(), empty, empty, empty); err != nil || len(empty) != 0 {
		t.Errorf("an SDK without a kit added %v (err %v)", empty.sorted(), err)
	}
}

func TestNamingRules(t *testing.T) {
	for in, want := range map[string]string{"getLeafId": "get_leaf_id", "isUsingOAuth": "is_using_oauth", "getApiKeyAndHeaders": "get_api_key_and_headers", "hasUI": "has_ui"} {
		if got := snake(in); got != want {
			t.Errorf("snake(%q) = %q, want %q", in, got, want)
		}
	}
	if got := goNames("getLeafId"); !slices.Contains(got, "GetLeafID") {
		t.Errorf("goNames(getLeafId) = %v", got)
	}
	if got := goNames("Event" + pascalWords("ui_prompt_start")); !slices.Contains(got, "EventUIPromptStart") {
		t.Errorf("event constant names = %v", got)
	}
	if !strings.Contains(renderCell(cell{Status: statusMissing}, true), "exception") {
		t.Error("an excepted missing cell must link its exception")
	}
}
