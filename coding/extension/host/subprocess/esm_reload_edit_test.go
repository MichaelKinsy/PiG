//go:build !pig_strip_node_extensions

package subprocess

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi imports an ES module extension through Node's own import, which keeps the module for the life of the process, so its /reload runs an edited ES module extension's old code (Pi 1.0.2, jiti 2.7.0). PiG evaluates an ES module extension again when a local module it imported was edited, its local imports included, and otherwise keeps the module and its state as Pi does; an installed package keeps its module either way (D93).
func TestHostReloadEvaluatesOnlyAnEditedESModuleExtensionAgain(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name, ext, packageJSON, isolation string
	}{
		{"mjs-packed", ".mjs", "", "shared-ok"},
		{"mjs-isolated", ".mjs", "", "isolated"},
		{"type-module-js-packed", ".js", `{"type":"module"}`, "shared-ok"},
		{"type-module-js-isolated", ".js", `{"type":"module"}`, "isolated"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			marker := filepath.Join(root, "counter")
			entry := filepath.Join(root, "counter"+fixture.ext)
			// lazy is imported after the factory returned, and late only from a module's second call on.
			files := map[string]string{
				"counter" + fixture.ext: fmt.Sprintf(`import { appendFileSync } from "node:fs";
import { randomUUID } from "node:crypto";
import { VERSION } from "./helper%[1]s";
import { id as dependency } from "dep";
const evaluation = randomUUID();
let count = 0;
export default function (pi) {
  pi.on("tool_call", async () => {
    count++;
    const { LAZY } = await import("./lazy%[1]s");
    const { LATE } = count >= 2 ? await import("./late%[1]s") : { LATE: "-" };
    appendFileSync(%[2]q, [evaluation, count, VERSION, LAZY, LATE, dependency].join(" ") + "\n");
    return { block: false };
  });
}
`, fixture.ext, marker),
				"helper" + fixture.ext:          "export const VERSION = \"v1\";\n",
				"lazy" + fixture.ext:            "export const LAZY = \"l1\";\n",
				"late" + fixture.ext:            "export const LATE = \"t1\";\n",
				"node_modules/dep/package.json": `{"name":"dep","type":"module","exports":"./index.js"}`,
				"node_modules/dep/index.js":     "import { randomUUID } from \"node:crypto\";\nexport const id = randomUUID();\n",
			}
			if fixture.packageJSON != "" {
				files["package.json"] = fixture.packageJSON
			}
			for name, content := range files {
				path := filepath.Join(root, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// edit rewrites a source file with a later modification time, so a file system with a coarse clock still records the edit.
			edit := func(name, old, replacement string) {
				t.Helper()
				path := filepath.Join(root, name)
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(strings.Replace(string(data), old, replacement, 1)), 0o600); err != nil {
					t.Fatal(err)
				}
				later := time.Now().Add(2 * time.Second)
				if err := os.Chtimes(path, later, later); err != nil {
					t.Fatal(err)
				}
			}

			config := ExtConfig{Name: "counter", Source: entry, Enabled: true, Isolation: fixture.isolation}
			host := NewHostWithConfigRoot(root, filepath.Join(root, "config"))
			host.SetConfigLoader(func() ([]ExtConfig, error) { return []ExtConfig{config}, nil })
			t.Cleanup(func() { host.Shutdown("test") })
			current, errs := host.LoadAll(t.Context(), []ExtConfig{config})
			if len(errs) != 0 || len(current) != 1 {
				t.Fatalf("startup loaded=%v errors=%v", current, errs)
			}
			calls := 0
			// emit sends one tool_call to the current generation and returns the line the extension logged for it.
			emit := func() []string {
				t.Helper()
				calls++
				if _, err := inproc.NewRunner(current, root).EmitToolCall(t.Context(), extension.CustomToolCallEvent{
					ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: fmt.Sprint("call-", calls)},
					ToolName:          "fixture",
					Input:             map[string]any{},
				}); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(marker)
				if err != nil {
					t.Fatal(err)
				}
				lines := strings.Split(strings.TrimSpace(string(data)), "\n")
				if len(lines) != calls {
					t.Fatalf("marker after call %d = %q", calls, data)
				}
				return strings.Fields(lines[calls-1])
			}
			reload := func() {
				t.Helper()
				reloaded, err := host.Reload(t.Context())
				if err != nil || len(reloaded) != 1 {
					t.Fatalf("reload = %v, %v", reloaded, err)
				}
				current = reloaded
			}
			want := func(step string, got []string, fields ...string) {
				t.Helper()
				if strings.Join(got, " ") != strings.Join(fields, " ") {
					t.Fatalf("%s: got %q, want %q", step, got, fields)
				}
			}
			reevaluated := func(step string, got []string, previous string) string {
				t.Helper()
				if len(got) != 6 || got[0] == previous {
					t.Fatalf("%s: got %q, want a new evaluation of the module %s", step, got, previous)
				}
				return got[0]
			}

			first := emit()
			if len(first) != 6 || first[1] != "1" || first[2] != "v1" || first[3] != "l1" || first[4] != "-" {
				t.Fatalf("first call = %q", first)
			}
			moduleA, dependency := first[0], first[5]

			// The unedited reload keeps the module, although lazy had no source stamp yet.
			reload()
			want("unedited reload", emit(), moduleA, "2", "v1", "l1", "t1", dependency)

			// late was imported after the last reload, so the reload finds its edit by its modification time.
			edit("late"+fixture.ext, "t1", "t2")
			reload()
			third := emit()
			moduleB := reevaluated("edited module imported since the last reload", third, moduleA)
			want("edited module imported since the last reload", third, moduleB, "1", "v1", "l1", "-", dependency)
			want("its second call", emit(), moduleB, "2", "v1", "l1", "t2", dependency)

			reload()
			want("unedited reload after an edit", emit(), moduleB, "3", "v1", "l1", "t2", dependency)

			edit("helper"+fixture.ext, "v1", "v2")
			reload()
			sixth := emit()
			moduleC := reevaluated("edited static import", sixth, moduleB)
			want("edited static import", sixth, moduleC, "1", "v2", "l1", "-", dependency)

			edit("lazy"+fixture.ext, "l1", "l2")
			reload()
			seventh := emit()
			moduleD := reevaluated("edited dynamic import", seventh, moduleC)
			want("edited dynamic import", seventh, moduleD, "1", "v2", "l2", "-", dependency)

			// An installed package keeps its module, as in Pi, even when its source changes.
			edit("node_modules/dep/index.js", "randomUUID()", "randomUUID() + \"\"")
			reload()
			want("edited installed package", emit(), moduleD, "2", "v2", "l2", "t2", dependency)
		})
	}
}

// Packed ES module extensions that import one local module share its instance, in Pi and in PiG. A reload that evaluates one of them again because only its own source changed keeps the shared module's instance and state; an edit of the shared module evaluates it, and every extension that imports it, again, and they share the new instance (D93).
func TestHostReloadKeepsAnUneditedSharedModuleOfPackedESModuleExtensions(t *testing.T) {
	t.Parallel()
	nodeCellRequireNode(t)
	root := t.TempDir()
	marker := filepath.Join(root, "hits")
	source := func(name string) string {
		return fmt.Sprintf(`import { appendFileSync } from "node:fs";
import { randomUUID } from "node:crypto";
import { hit, SHARED } from "./shared.mjs";
const evaluation = randomUUID();
export default function (pi) {
  pi.on("tool_call", async () => {
    appendFileSync(%q, [%q, evaluation, hit(), SHARED].join(" ") + "\n");
    return { block: false };
  });
}
`, marker, name)
	}
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		later := time.Now().Add(2 * time.Second)
		if err := os.Chtimes(path, later, later); err != nil {
			t.Fatal(err)
		}
	}
	write("shared.mjs", "let hits = 0;\nexport function hit() { return ++hits; }\nexport const SHARED = \"s1\";\n")
	write("a.mjs", source("a"))
	write("b.mjs", source("b"))
	configs := []ExtConfig{
		{Name: "a", Source: filepath.Join(root, "a.mjs"), Enabled: true},
		{Name: "b", Source: filepath.Join(root, "b.mjs"), Enabled: true},
	}
	host := NewHostWithConfigRoot(root, filepath.Join(root, "config"))
	host.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	t.Cleanup(func() { host.Shutdown("test") })
	current, errs := host.LoadAll(t.Context(), configs)
	if len(errs) != 0 || len(current) != 2 {
		t.Fatalf("startup loaded=%v errors=%v", current, errs)
	}
	type line struct{ evaluation, hits, shared string }
	seen := 0
	calls := 0
	// emit sends one tool_call to both extensions and returns what each logged for it.
	emit := func() map[string]line {
		t.Helper()
		calls++
		if _, err := inproc.NewRunner(current, root).EmitToolCall(t.Context(), extension.CustomToolCallEvent{
			ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: fmt.Sprint("call-", calls)},
			ToolName:          "fixture",
			Input:             map[string]any{},
		}); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(marker)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(lines) != seen+2 {
			t.Fatalf("marker after call %d = %q", calls, data)
		}
		got := map[string]line{}
		for _, text := range lines[seen:] {
			fields := strings.Fields(text)
			if len(fields) != 4 {
				t.Fatalf("marker line %q", text)
			}
			got[fields[0]] = line{fields[1], fields[2], fields[3]}
		}
		seen = len(lines)
		return got
	}
	reload := func() {
		t.Helper()
		reloaded, err := host.Reload(t.Context())
		if err != nil || len(reloaded) != 2 {
			t.Fatalf("reload = %v, %v", reloaded, err)
		}
		current = reloaded
	}
	hits := func(got map[string]line) string {
		pair := []string{got["a"].hits, got["b"].hits}
		slices.Sort(pair)
		return strings.Join(pair, ",")
	}

	first := emit()
	if hits(first) != "1,2" || first["a"].shared != "s1" || first["b"].shared != "s1" {
		t.Fatalf("first call = %+v, want one shared instance counting 1,2", first)
	}

	write("a.mjs", source("a")+"// edited\n")
	reload()
	second := emit()
	if second["a"].evaluation == first["a"].evaluation || second["b"].evaluation != first["b"].evaluation {
		t.Fatalf("after an edit of a.mjs: %+v, want a evaluated again and b kept (before %+v)", second, first)
	}
	if hits(second) != "3,4" {
		t.Fatalf("after an edit of a.mjs: %+v, want the unedited shared module's instance counting on at 3,4", second)
	}

	write("shared.mjs", "let hits = 0;\nexport function hit() { return ++hits; }\nexport const SHARED = \"s2\";\n")
	reload()
	third := emit()
	if third["a"].evaluation == second["a"].evaluation || third["b"].evaluation == second["b"].evaluation {
		t.Fatalf("after an edit of shared.mjs: %+v, want both extensions evaluated again (before %+v)", third, second)
	}
	if hits(third) != "1,2" || third["a"].shared != "s2" || third["b"].shared != "s2" {
		t.Fatalf("after an edit of shared.mjs: %+v, want one new shared instance counting 1,2", third)
	}

	reload()
	if fourth := emit(); fourth["a"].evaluation != third["a"].evaluation || fourth["b"].evaluation != third["b"].evaluation || hits(fourth) != "3,4" {
		t.Fatalf("unedited reload: %+v, want both modules and the shared instance kept (before %+v)", fourth, third)
	}
}

// A Session replacement calls each factory again without a reload pass (Pi's resource loader keeps or clears its factory cache by cwd and re-imports through jiti, which Node's ES module cache answers), so it never evaluates an edited ES module extension again; the next /reload does (D93).
func TestSessionReplacementKeepsAnEditedESModuleUntilAReload(t *testing.T) {
	nodeCellRequireNode(t)
	root := t.TempDir()
	mjs, log := retainedNodeExtension(t, root, "replaced.mjs", false)
	configs := []ExtConfig{mjs}
	retention := NewRuntimeRetention()
	t.Cleanup(retention.Close)
	first := NewHost(t.TempDir())
	first.SetRuntimeRetention(retention)
	t.Cleanup(func() { first.Shutdown("test done") })
	first.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	if _, errs := first.LoadAll(t.Context(), configs); len(errs) > 0 {
		t.Fatal(errs)
	}
	// A reload before the edit leaves the runtime in a reload pass, so the replacement's admission is the first one outside it.
	if _, err := first.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	before := retainedProbeOf(t, first, mjs.Name)

	data, err := os.ReadFile(mjs.Source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mjs.Source, append(data, "// edited\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(mjs.Source, later, later); err != nil {
		t.Fatal(err)
	}
	first.Retain()
	first.Shutdown("session replaced")

	// The replacement Host has another cwd, so the runtime clears its factory cache and imports the entry again outside a reload.
	second := NewHost(t.TempDir())
	second.SetRuntimeRetention(retention)
	t.Cleanup(func() { second.Shutdown("test done") })
	second.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	if _, errs := second.LoadAll(t.Context(), configs); len(errs) > 0 {
		t.Fatal(errs)
	}
	if after := retainedProbeOf(t, second, mjs.Name); after.Pid != before.Pid || after.Calls != 3 {
		t.Fatalf("replacement probe = %+v, want the parked process %d with its module (3 factory calls)", after, before.Pid)
	}
	if modules, factories := retainedLogCounts(t, log); modules != 1 || factories != 3 {
		t.Fatalf("module evaluated %d times with %d factory calls after the replacement, want 1 and 3", modules, factories)
	}

	if _, err := second.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reloaded := retainedProbeOf(t, second, mjs.Name); reloaded.Calls != 1 {
		t.Fatalf("reload probe = %+v, want the edited module evaluated again (1 factory call)", reloaded)
	}
	if modules, factories := retainedLogCounts(t, log); modules != 2 || factories != 4 {
		t.Fatalf("module evaluated %d times with %d factory calls after the reload, want 2 and 4", modules, factories)
	}
}

// esmReloadFixture loads extensions that log one line per tool_call, "<name> <fields...>", into one marker file, in one packed Node process.
type esmReloadFixture struct {
	t       *testing.T
	root    string
	marker  string
	host    *Host
	current []extension.Extension
	seen    int
	calls   int
}

func newESMReloadFixture(t *testing.T, files map[string]string, entries ...string) *esmReloadFixture {
	t.Helper()
	nodeCellRequireNode(t)
	f := &esmReloadFixture{t: t, root: t.TempDir()}
	f.marker = filepath.Join(f.root, "marker")
	for name, content := range files {
		f.write(name, strings.ReplaceAll(content, "MARKER", strconv.Quote(f.marker)))
	}
	var configs []ExtConfig
	for _, entry := range entries {
		configs = append(configs, ExtConfig{Name: strings.TrimSuffix(entry, filepath.Ext(entry)), Source: filepath.Join(f.root, entry), Enabled: true})
	}
	f.host = NewHostWithConfigRoot(f.root, filepath.Join(f.root, "config"))
	f.host.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	t.Cleanup(func() { f.host.Shutdown("test") })
	current, errs := f.host.LoadAll(t.Context(), configs)
	if len(errs) != 0 || len(current) != len(configs) {
		t.Fatalf("startup loaded=%v errors=%v", current, errs)
	}
	f.current = current
	return f
}

// write writes a source file with a later modification time, so a file system with a coarse clock still records the edit.
func (f *esmReloadFixture) write(name, content string) {
	f.t.Helper()
	path := filepath.Join(f.root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		f.t.Fatal(err)
	}
}

// reload reloads every extension and returns the reload's issues.
func (f *esmReloadFixture) reload() []string {
	f.t.Helper()
	reloaded, err := f.host.Reload(f.t.Context())
	if err != nil {
		f.t.Fatal(err)
	}
	f.current = reloaded
	return f.host.LastReloadReport().Issues
}

// emit sends one tool_call and returns each loaded extension's logged fields by name.
func (f *esmReloadFixture) emit() map[string][]string {
	f.t.Helper()
	f.calls++
	if _, err := inproc.NewRunner(f.current, f.root).EmitToolCall(f.t.Context(), extension.CustomToolCallEvent{
		ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: fmt.Sprint("call-", f.calls)},
		ToolName:          "fixture",
		Input:             map[string]any{},
	}); err != nil {
		f.t.Fatal(err)
	}
	data, err := os.ReadFile(f.marker)
	if err != nil {
		f.t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != f.seen+len(f.current) {
		f.t.Fatalf("marker after call %d with %d extensions = %q", f.calls, len(f.current), data)
	}
	got := map[string][]string{}
	for _, line := range lines[f.seen:] {
		fields := strings.Fields(line)
		got[fields[0]] = fields[1:]
	}
	f.seen = len(lines)
	return got
}

// esmLogger is an extension entry that logs its name, its evaluation, its call count and the given expressions on each tool_call.
func esmLogger(name, imports, fields string) string {
	return fmt.Sprintf(`import { appendFileSync } from "node:fs";
import { randomUUID } from "node:crypto";
%s
const evaluation = randomUUID();
let count = 0;
export default function (pi) {
  pi.on("tool_call", async () => {
    count++;
    appendFileSync(MARKER, [%q, evaluation, count, %s].join(" ") + "\n");
    return { block: false };
  });
}
`, imports, name, fields)
}

const esmSharedCounter = "let hits = 0;\nexport function hit() { return ++hits; }\nexport const SHARED = %q;\n"

// A reload decides once which modules to evaluate again, from the source each module's current evaluation loaded: when one importer of an edited shared module fails to load and is fixed by the next reload, it joins the evaluation its sibling already shares (D93).
func TestHostReloadKeepsOneSharedEvaluationAcrossAFailedImporter(t *testing.T) {
	t.Parallel()
	shared := `import { hit, SHARED } from "./shared.mjs";`
	f := newESMReloadFixture(t, map[string]string{
		"shared.mjs": fmt.Sprintf(esmSharedCounter, "s1"),
		"a.mjs":      esmLogger("a", shared, "hit(), SHARED"),
		"b.mjs":      esmLogger("b", shared, "hit(), SHARED"),
	}, "a.mjs", "b.mjs")
	f.emit()

	f.write("shared.mjs", fmt.Sprintf(esmSharedCounter, "s2"))
	f.write("b.mjs", "export default function (pi) { pi.on(\"tool_call\", () => {\n")
	if issues := f.reload(); len(issues) != 1 {
		t.Fatalf("reload with b broken: issues = %q, want b's load failure", issues)
	}
	if got := f.emit(); got["a"][2] != "1" || got["a"][3] != "s2" {
		t.Fatalf("after the edit of shared.mjs: a = %q, want the new shared module's first hit", got["a"])
	}

	f.write("b.mjs", strings.ReplaceAll(esmLogger("b", shared, "hit(), SHARED"), "MARKER", strconv.Quote(f.marker)))
	if issues := f.reload(); len(issues) != 0 {
		t.Fatalf("reload after fixing b: issues = %q", issues)
	}
	got := f.emit()
	pair := []string{got["a"][2], got["b"][2]}
	slices.Sort(pair)
	if strings.Join(pair, ",") != "2,3" || got["b"][3] != "s2" {
		t.Fatalf("after fixing b: %q, want b sharing a's evaluation of shared.mjs at hits 2,3", got)
	}
}

// An evaluation's imports decide what it depends on: after an edit removes an import, an edit of the module it no longer imports leaves it, and its state, alone (D93).
func TestHostReloadForgetsAnImportTheEditRemoved(t *testing.T) {
	t.Parallel()
	f := newESMReloadFixture(t, map[string]string{
		"helper.mjs": "export const HELPER = \"h1\";\n",
		"e.mjs":      esmLogger("e", `import { HELPER } from "./helper.mjs";`, "HELPER"),
	}, "e.mjs")
	first := f.emit()

	f.write("e.mjs", strings.ReplaceAll(esmLogger("e", "", `"none"`), "MARKER", strconv.Quote(f.marker)))
	f.reload()
	second := f.emit()
	if second["e"][0] == first["e"][0] || second["e"][2] != "none" {
		t.Fatalf("after removing the import: %q, want e evaluated again without helper", second["e"])
	}

	f.write("helper.mjs", "export const HELPER = \"h2\";\n")
	f.reload()
	if third := f.emit(); third["e"][0] != second["e"][0] || third["e"][1] != "2" {
		t.Fatalf("after an edit of the module e no longer imports: %q, want e kept at call 2", third["e"])
	}
}

// A TypeScript extension that imports a local .mjs module gets it through Node's import, as an ES module extension does; after an edit of that module, both get its one new evaluation in the same reload, whatever their admission order (D93).
func TestHostReloadGivesTypeScriptAndESModuleImportersOneNewEvaluation(t *testing.T) {
	t.Parallel()
	shared := `import { hit, SHARED } from "./shared.mjs";`
	f := newESMReloadFixture(t, map[string]string{
		"shared.mjs": fmt.Sprintf(esmSharedCounter, "s1"),
		"t.ts":       esmLogger("t", shared, "hit(), SHARED"),
		"e.mjs":      esmLogger("e", shared, "hit(), SHARED"),
	}, "t.ts", "e.mjs")
	f.emit()

	f.write("shared.mjs", fmt.Sprintf(esmSharedCounter, "s2"))
	f.reload()
	got := f.emit()
	pair := []string{got["t"][2], got["e"][2]}
	slices.Sort(pair)
	if strings.Join(pair, ",") != "1,2" || got["t"][3] != "s2" || got["e"][3] != "s2" {
		t.Fatalf("after an edit of shared.mjs: %q, want t and e on one new evaluation at hits 1,2", got)
	}
}

// Node keeps a CommonJS module in its require cache, so an edit of a .cjs dependency is not evaluated again, and its ES module importer keeps its module and its state (D93).
func TestHostReloadKeepsAnESModuleWhoseCommonJSDependencyChanged(t *testing.T) {
	t.Parallel()
	f := newESMReloadFixture(t, map[string]string{
		"dep.cjs": "module.exports = { DEP: \"c1\" };\n",
		"e.mjs":   esmLogger("e", `import dep from "./dep.cjs";`, "dep.DEP"),
	}, "e.mjs")
	first := f.emit()

	f.write("dep.cjs", "module.exports = { DEP: \"c2\" };\n")
	f.reload()
	if second := f.emit(); second["e"][0] != first["e"][0] || second["e"][1] != "2" || second["e"][2] != "c1" {
		t.Fatalf("after an edit of dep.cjs: %q, want e kept at call 2 with the cached c1", second["e"])
	}
}
