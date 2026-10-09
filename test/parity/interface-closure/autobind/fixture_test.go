package main

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixture is a one-package module with a small Pi interface inventory. The baseline must be NOT-A-GAP in full; each seed
// then breaks one thing and the detector must report the gap with the expected reason on the expected row.

const fxLib = `package lib

import "context"

// Config is the Go form of the upstream Config interface.
type Config struct {
	Name    string ` + "`json:\"name\"`" + `
	Retries int    ` + "`json:\"retries\"`" + `
	Timeout int    ` + "`json:\"timeoutMs\"`" + `
	OnEvent func(ctx context.Context, e string)
}

// Run is the Go form of the upstream run property.
func (c *Config) Run(ctx context.Context, x string) error { return nil }

// Add is the Go form of upstream add.
func Add(a, b int) int { return a + b }

// Widget is the Go form of the upstream Widget class.
type Widget struct{ n string }

// NewWidget is the constructor.
func NewWidget(name string) *Widget { return &Widget{n: name} }

// Name reads the widget name.
func (w *Widget) Name() string { return w.n }

// Describe is the Go form of upstream describe.
func Describe(w *Widget) string { return w.Name() }

// Mode is the Go form of the upstream Mode alias.
type Mode string

const (
	ModeA Mode = "a"
	ModeB Mode = "b"
)

// DefaultLimit is the Go form of the upstream DEFAULT_LIMIT value.
func DefaultLimit() int { return 3 }

// LimitName is the Go form of the upstream LIMIT_NAME literal.
const LimitName = "limit"

// Use is a production caller.
func Use() int { return Add(1, 2) }
`

// fxUse is the production code that reads, sets and calls the fixture's members; the baseline reach set names Members as reachable from cmd/pig.
const fxUse = `package lib

import "context"

// Members touches every member of Config and Widget the way a command would.
func Members() int {
	c := &Config{Name: "a", Retries: 1, Timeout: 2, OnEvent: func(context.Context, string) {}}
	c.OnEvent(context.Background(), c.Name)
	_ = c.Run(context.Background(), "x")
	w := NewWidget("w")
	_ = w.Name()
	return c.Retries + c.Timeout
}
`

// fxReach is the reach set of the baseline fixture: Members is reachable from cmd/pig.
var fxReach = map[string]bool{"lib/use.go#Members": true}

const fxTest = `package lib

import (
	"context"
	"testing"
)

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("add")
	}
}

func TestConfig(t *testing.T) {
	c := Config{Name: "x", Retries: 1}
	if c.Name != "x" || c.Retries != 1 {
		t.Error("fields")
	}
	if err := c.Run(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	c.Timeout = 5
	seen := ""
	c.OnEvent = func(_ context.Context, e string) { seen = e }
	c.OnEvent(context.Background(), "x")
	if c.Timeout != 5 || seen != "x" {
		t.Error("timeout or event")
	}
}

func TestDefaults(t *testing.T) {
	if DefaultLimit() != 3 || LimitName != "limit" {
		t.Error("defaults")
	}
}

func TestWidget(t *testing.T) {
	w := NewWidget("w")
	if Describe(w) != "w" || w.Name() != "w" {
		t.Error("widget")
	}
}

func TestMode(t *testing.T) {
	if ModeA == ModeB || len(string(ModeA)) != 1 {
		t.Error("mode")
	}
}
`

const fxAlias = "export type Mode = \"a\" | \"b\";\n"

func js(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

type m = map[string]any

func prm(name, typ string, optional bool) m {
	return m{"name": name, "type": typ, "optional": optional, "rest": false}
}

func fxInventory() []m {
	cfg := "pkg:fx/.#Config"
	wid := "pkg:fx/.#Widget"
	runCall := m{"parameters": []m{prm("x", "string", false), prm("signal", "AbortSignal", true)}, "returns": "Promise<void>"}
	return []m{
		{"id": "pkg:fx/.#add", "name": "add", "kind": "function", "shapeHash": "h", "shape": m{"type": "(a: number, b: number) => number",
			"calls": []m{{"parameters": []m{prm("a", "number", false), prm("b", "number", false)}, "returns": "number"}}}},
		{"id": "pkg:fx/.#add::call:0", "parentId": "pkg:fx/.#add", "role": "call-overload", "name": "add call 0", "kind": "call-overload",
			"shape": m{"parameters": []m{prm("a", "number", false), prm("b", "number", false)}, "returns": "number"}},
		{"id": cfg, "name": "Config", "kind": "interface", "shape": m{"type": "Config"}},
		{"id": cfg + "::property:name", "parentId": cfg, "role": "property", "name": "Config.name", "kind": "property", "shape": m{"name": "name", "type": "string"}},
		{"id": cfg + "::property:retries", "parentId": cfg, "role": "property", "name": "Config.retries", "kind": "property", "shape": m{"name": "retries", "type": "number"}},
		{"id": cfg + "::property:run", "parentId": cfg, "role": "property", "name": "Config.run", "kind": "property",
			"shape": m{"name": "run", "type": "(x: string, signal?: AbortSignal) => Promise<void>", "calls": []m{runCall}}},
		{"id": cfg + "::property:run::call:0", "parentId": cfg + "::property:run", "role": "call-overload", "name": "Config.run call 0", "kind": "call-overload", "shape": runCall},
		{"id": cfg + "::property:timeoutMs", "parentId": cfg, "role": "property", "name": "Config.timeoutMs", "kind": "property", "shape": m{"name": "timeoutMs", "type": "number"}},
		// An optional function property that mentions AbortSignal: the inventory records no call row for it.
		{"id": cfg + "::property:onEvent", "parentId": cfg, "role": "property", "name": "Config.onEvent", "kind": "property",
			"shape": m{"name": "onEvent", "optional": true, "type": "((e: string, signal?: AbortSignal) => void) | undefined"}},
		{"id": wid, "name": "Widget", "kind": "class", "shape": m{"type": "Widget"}},
		{"id": wid + "::construct:0", "parentId": wid, "role": "construct-overload", "name": "Widget constructor 0", "kind": "construct-overload",
			"shape": m{"parameters": []m{prm("name", "string", false)}, "returns": "Widget"}},
		{"id": wid + "::property:name", "parentId": wid, "role": "property", "name": "Widget.name", "kind": "property", "shape": m{"name": "name", "type": "string"}},
		{"id": "pkg:fx/.#describe", "name": "describe", "kind": "function", "shape": m{"type": "(w: Widget) => string",
			"calls": []m{{"parameters": []m{prm("w", "Widget", false)}, "returns": "string"}}}},
		{"id": "pkg:fx/.#describe::call:0", "parentId": "pkg:fx/.#describe", "role": "call-overload", "name": "describe call 0", "kind": "call-overload",
			"shape": m{"parameters": []m{prm("w", "Widget", false)}, "returns": "string"}},
		{"id": "pkg:fx/.#Mode", "name": "Mode", "kind": "type-alias", "shape": m{"type": "Mode", "aliasTarget": "Mode"}},
		{"id": "pkg:fx/.#DEFAULT_LIMIT", "name": "DEFAULT_LIMIT", "kind": "variable", "shape": m{"type": "number"}},
		{"id": "pkg:fx/.#LIMIT_NAME", "name": "LIMIT_NAME", "kind": "variable", "shape": m{"type": `"limit"`}},
	}
}

// fixtureLast is the detector and root of the most recent fixtureLedger run.
var fixtureLast struct {
	det  *detector
	root string
}

// fixtureRun writes the fixture and runs every ID through the detector.
func fixtureRun(t *testing.T, files map[string]string, tweak func(inv []m) []m, reach map[string]bool, renames renameTable) map[string]*decision {
	t.Helper()
	_, out := fixtureLedger(t, files, tweak, reach, renames)
	return out
}

// fixtureLedger writes the fixture and returns its ledger with every decision.
func fixtureLedger(t *testing.T, files map[string]string, tweak func(inv []m) []m, reach map[string]bool, renames renameTable) (*ledger, map[string]*decision) {
	t.Helper()
	root := t.TempDir()
	all := map[string]string{
		"go.mod":          "module fixture\n\ngo 1.26\n",
		"lib/lib.go":      fxLib,
		"lib/lib_test.go": fxTest,
		"lib/use.go":      fxUse,
		".upstream/current/packages/fx/src/types.ts": fxAlias,
	}
	maps.Copy(all, files)
	if reach == nil {
		reach = fxReach // a non-nil empty map means no function is reachable
	}
	for name, content := range all {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	inv := fxInventory()
	if tweak != nil {
		inv = tweak(inv)
	}
	// "_disposition" sets a row's ledger disposition (default pending); "_mapping" adds fields to the mapping row; "_mappingOnly" makes a mapping row with no inventory entry.
	mapping := make([]m, 0, len(inv))
	entries := make([]m, 0, len(inv))
	unreviewed := map[string]bool{}
	for _, e := range inv {
		disp, _ := e["_disposition"].(string)
		if disp == "" {
			disp = "pending"
		}
		row := m{"id": e["id"], "disposition": disp}
		if e["_unreviewed"] == true {
			unreviewed[e["id"].(string)] = true
		}
		if extra, ok := e["_mapping"].(m); ok {
			maps.Copy(row, extra)
		}
		mapping = append(mapping, row)
		if e["_mappingOnly"] == true {
			continue
		}
		entry := maps.Clone(e)
		delete(entry, "_disposition")
		delete(entry, "_unreviewed")
		entries = append(entries, entry)
	}
	inv = entries
	base := filepath.Join(root, "test/parity/interfaces")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "upstream-v0.0.0.json"), js(m{"interfaces": inv}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "mapping-v0.0.0.json"), js(m{"mappings": mapping}), 0o644); err != nil {
		t.Fatal(err)
	}
	// A designed-out or divergence row stands as a hand decision only when reviewed-decisions.json holds it.
	decided := m{}
	for _, row := range mapping {
		if disp, _ := row["disposition"].(string); isReviewedDisposition(disp) && !unreviewed[row["id"].(string)] {
			id, _ := row["id"].(string)
			decision := maps.Clone(row)
			delete(decision, "id")
			decision["upstreamShapeHash"] = "sha256:fixture"
			decided[id] = decision
		}
	}
	if len(decided) > 0 {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(reviewedDecisionsFile)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, reviewedDecisionsFile), js(decided), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	l, err := loadLedger(root, "0.0.0")
	if err != nil {
		t.Fatal(err)
	}
	dirSeeds["fx"] = []string{"lib"}
	if _, ok := all["lib2/lib2.go"]; ok {
		dirSeeds["fx"] = []string{"lib", "lib2"}
	}
	if _, ok := all["fxtest/lib.go"]; ok {
		dirSeeds["fx"] = []string{"fxtest"}
	}
	ix, err := buildIndex(root, reach, []string{"./..."})
	if err != nil {
		t.Fatal(err)
	}
	if renames == nil {
		renames = renameTable{}
	}
	out := map[string]*decision{}
	det := newDetector(ix, l, renames, tsAliases(root))
	if err := readJSON(filepath.Join(root, reviewedTypesFile), &det.reviewed); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := readJSON(filepath.Join(root, representationsFile), &det.reps); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := readJSON(filepath.Join(root, narrowFile), &det.narrow); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := readJSON(filepath.Join(root, placementsFile), &det.placements); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	fixtureLast.det, fixtureLast.root = det, root
	for _, d := range det.all() {
		out[d.ID] = d
	}
	return l, out
}

func gapsOf(ds map[string]*decision) map[string]string {
	out := map[string]string{}
	for id, d := range ds {
		if d.Gap {
			out[id] = d.Reason
		}
	}
	return out
}

func TestFixtureBaselineHasNoGap(t *testing.T) {
	ds := fixtureRun(t, nil, nil, nil, nil)
	if g := gapsOf(ds); len(g) != 0 {
		t.Fatalf("baseline fixture reports gaps: %v", g)
	}
	if len(ds) != len(fxInventory()) {
		t.Fatalf("decided %d IDs, inventory has %d", len(ds), len(fxInventory()))
	}
	for id, d := range ds {
		if d.Evidence == "" {
			t.Errorf("%s is not a gap but names no evidence", id)
		}
	}
}

func TestFixtureDeterministic(t *testing.T) {
	a := gapTSV(sortedDecisions(fixtureRun(t, nil, nil, nil, nil)), true)
	b := gapTSV(sortedDecisions(fixtureRun(t, nil, nil, nil, nil)), true)
	if a != b {
		t.Fatalf("two runs differ:\n%s\n--\n%s", a, b)
	}
}

func sortedDecisions(m map[string]*decision) []*decision {
	var out []*decision
	for _, d := range m {
		out = append(out, d)
	}
	sortByID(out)
	return out
}

// TestSeededGapsAreCaught is trust check 2: each seed breaks one thing in the baseline and must produce exactly the expected gaps.
func TestSeededGapsAreCaught(t *testing.T) {
	type edit struct{ file, from, to string }
	apply := func(edits []edit) map[string]string {
		files := map[string]string{"lib/lib.go": fxLib, "lib/lib_test.go": fxTest, "lib/use.go": fxUse}
		for _, e := range edits {
			if !strings.Contains(files[e.file], e.from) {
				t.Fatalf("seed text %q missing from %s", e.from, e.file)
			}
			files[e.file] = strings.ReplaceAll(files[e.file], e.from, e.to)
		}
		return files
	}
	cfg := "pkg:fx/.#Config"
	seeds := []struct {
		name  string
		edits []edit
		want  map[string]string // ID -> reason
	}{
		{"remove a field", []edit{
			{"lib/lib.go", "\tRetries int    `json:\"retries\"`\n", ""},
			{"lib/lib_test.go", "c := Config{Name: \"x\", Retries: 1}\n\tif c.Name != \"x\" || c.Retries != 1 {", "c := Config{Name: \"x\"}\n\tif c.Name != \"x\" {"}},
			map[string]string{cfg + "::property:retries": reasonMember, cfg: reasonChild}},
		{"rename a method", []edit{{"lib/lib.go", "func (c *Config) Run(", "func (c *Config) Execute("}, {"lib/lib_test.go", "c.Run(", "c.Execute("}},
			map[string]string{cfg + "::property:run": reasonMember, cfg + "::property:run::call:0": reasonMember, cfg: reasonChild}},
		{"change a parameter type", []edit{{"lib/lib.go", "func Add(a, b int) int { return a + b }", "func Add(a, b string) int { return len(a + b) }"}, {"lib/lib.go", "Add(1, 2)", "Add(\"1\", \"2\")"}, {"lib/lib_test.go", "Add(1, 2)", "Add(\"1\", \"2\")"}},
			map[string]string{"pkg:fx/.#add::call:0": reasonSignature, "pkg:fx/.#add": reasonChild}},
		{"drop a parameter", []edit{{"lib/lib.go", "func Add(a, b int) int { return a + b }", "func Add(a int) int { return a }"}, {"lib/lib.go", "Add(1, 2)", "Add(1)"}, {"lib/lib_test.go", "Add(1, 2) != 3", "Add(3) != 3"}},
			map[string]string{"pkg:fx/.#add::call:0": reasonSignature, "pkg:fx/.#add": reasonChild}},
		{"drop the result", []edit{{"lib/lib.go", "func Add(a, b int) int { return a + b }", "func Add(a, b int) {}"}, {"lib/lib.go", "func Use() int { return Add(1, 2) }", "func Use() { Add(1, 2) }"}, {"lib/lib_test.go", "if Add(1, 2) != 3 {\n\t\tt.Fatal(\"add\")\n\t}", "Add(1, 2)\n\tt.Fatal(\"add\")"}},
			map[string]string{"pkg:fx/.#add::call:0": reasonSignature, "pkg:fx/.#add": reasonChild}},
		{"rename the function", []edit{{"lib/lib.go", "func Add(", "func Sum("}, {"lib/lib.go", "return Add(1, 2)", "return Sum(1, 2)"}, {"lib/lib_test.go", "Add(1, 2)", "Sum(1, 2)"}},
			map[string]string{"pkg:fx/.#add": reasonNoSymbol, "pkg:fx/.#add::call:0": reasonChild}},
		{"drop the test reference", []edit{{"lib/lib_test.go", "\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"add\")\n\t}\n", "\tt.Log(\"no reference\")\n"}},
			map[string]string{"pkg:fx/.#add": reasonExercise, "pkg:fx/.#add::call:0": reasonExercise}},
		{"a test that does not assert", []edit{{"lib/lib_test.go", "\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"add\")\n\t}\n", "\t_ = Add(1, 2)\n"}},
			map[string]string{"pkg:fx/.#add": reasonExercise, "pkg:fx/.#add::call:0": reasonExercise}},
		{"change a field type", []edit{{"lib/lib.go", "Retries int ", "Retries string "}, {"lib/lib_test.go", "Retries: 1", "Retries: \"1\""}, {"lib/lib_test.go", "c.Retries != 1", "c.Retries != \"1\""}},
			map[string]string{cfg + "::property:retries": reasonType, cfg: reasonChild}},
		{"an unrelated type for a named parameter", []edit{{"lib/lib.go", "func Describe(w *Widget) string { return w.Name() }", "type Gadget struct{ Widget }\n\nfunc Describe(w *Gadget) string { return w.Name() }"}, {"lib/lib_test.go", "Describe(w)", "Describe(&Gadget{*w})"}},
			map[string]string{"pkg:fx/.#describe::call:0": reasonUndecided, "pkg:fx/.#describe": reasonChild}},
		{"remove a literal constant", []edit{{"lib/lib.go", "\tModeB Mode = \"b\"\n", ""}, {"lib/lib_test.go", "ModeA == ModeB", "ModeA == Mode(\"b\")"}},
			map[string]string{"pkg:fx/.#Mode": reasonType}},
		{"rename the constructor", []edit{{"lib/lib.go", "func NewWidget(", "func MakeWidget("}, {"lib/lib_test.go", "NewWidget(", "MakeWidget("}, {"lib/use.go", "NewWidget(", "MakeWidget("}},
			map[string]string{"pkg:fx/.#Widget::construct:0": reasonNoSymbol, "pkg:fx/.#Widget": reasonChild}},
		{"remove a class accessor", []edit{{"lib/lib.go", "// Name reads the widget name.\nfunc (w *Widget) Name() string { return w.n }\n", "func (w *Widget) label() string { return w.n }\n"}, {"lib/lib.go", "return w.Name() }", "return w.label() }"}, {"lib/lib_test.go", " || w.Name() != \"w\"", ""}},
			map[string]string{"pkg:fx/.#Widget::property:name": reasonMember, "pkg:fx/.#Widget": reasonChild}},
		{"remove a function property that mentions AbortSignal", []edit{
			{"lib/lib.go", "\tOnEvent func(ctx context.Context, e string)\n", ""},
			{"lib/lib_test.go", "\tc.OnEvent = func(_ context.Context, e string) { seen = e }\n\tc.OnEvent(context.Background(), \"x\")\n", "\tseen = \"x\"\n"}},
			map[string]string{cfg + "::property:onEvent": reasonMember, cfg: reasonChild}},
		{"change the signature of a function property with no call row", []edit{
			{"lib/lib.go", "OnEvent func(ctx context.Context, e string)", "OnEvent func(ctx context.Context, e int)"},
			{"lib/lib_test.go", "c.OnEvent = func(_ context.Context, e string) { seen = e }", "c.OnEvent = func(_ context.Context, e int) { seen = \"x\" }"},
			{"lib/lib_test.go", "c.OnEvent(context.Background(), \"x\")", "c.OnEvent(context.Background(), 1)"}},
			map[string]string{cfg + "::property:onEvent": reasonSignature, cfg: reasonChild}},
		{"a value becomes a function that takes parameters", []edit{
			{"lib/lib.go", "func DefaultLimit() int { return 3 }", "func DefaultLimit(n int) int { return n }"},
			{"lib/lib_test.go", "DefaultLimit() != 3", "DefaultLimit(3) != 3"}},
			map[string]string{"pkg:fx/.#DEFAULT_LIMIT": reasonType}},
		{"change a literal constant value", []edit{
			{"lib/lib.go", "const LimitName = \"limit\"", "const LimitName = \"cap\""},
			{"lib/lib_test.go", "LimitName != \"limit\"", "LimitName != \"cap\""}},
			map[string]string{"pkg:fx/.#LIMIT_NAME": reasonType}},
		{"a data accessor takes a parameter", []edit{
			{"lib/lib.go", "func (w *Widget) Name() string { return w.n }", "func (w *Widget) Name(prefix string) string { return prefix + w.n }"},
			{"lib/lib.go", "return w.Name() }", "return w.Name(\"\") }"},
			{"lib/lib_test.go", "w.Name() != \"w\"", "w.Name(\"\") != \"w\""}},
			map[string]string{"pkg:fx/.#Widget::property:name": reasonType, "pkg:fx/.#Widget": reasonChild}},
		{"an unexported type fits better than the exported one", []edit{
			{"lib/lib.go", "\tRetries int    `json:\"retries\"`\n\tTimeout", "\tTimeout"},
			{"lib/lib.go", "// Use is a production caller.", "type config struct {\n\tName    string\n\tRetries int\n\tTimeout int `json:\"timeoutMs\"`\n\tOnEvent func(ctx context.Context, e string)\n}\n\nfunc (c *config) Run(ctx context.Context, x string) error { return nil }\n\n// Use is a production caller."},
			{"lib/lib_test.go", "c := Config{Name: \"x\", Retries: 1}\n\tif c.Name != \"x\" || c.Retries != 1 {", "c := Config{Name: \"x\"}\n\tif c.Name != \"x\" {"},
			{"lib/lib_test.go", "func TestDefaults(", "func TestInternalConfig(t *testing.T) {\n\tc := config{Name: \"x\", Retries: 1, Timeout: 2, OnEvent: func(context.Context, string) {}}\n\tc.OnEvent(context.Background(), \"x\")\n\tif c.Name != \"x\" || c.Retries != 1 || c.Timeout != 2 || c.Run(context.Background(), \"x\") != nil {\n\t\tt.Error(\"config\")\n\t}\n}\n\nfunc TestDefaults("}},
			map[string]string{cfg + "::property:retries": reasonMember, cfg: reasonChild}},
	}
	caught := 0
	for _, s := range seeds {
		t.Run(s.name, func(t *testing.T) {
			got := gapsOf(fixtureRun(t, apply(s.edits), nil, nil, nil))
			for id, reason := range s.want {
				if got[id] != reason {
					t.Errorf("%s: want gap %q, got %q (all gaps: %v)", id, reason, got[id], got)
				}
			}
			for id, reason := range got {
				if s.want[id] == "" {
					t.Errorf("unexpected gap %s (%s)", id, reason)
				}
			}
			if len(got) > 0 {
				caught++
			}
		})
	}
	t.Logf("seeded gaps caught: %d of %d", caught, len(seeds))
}

func TestReachableCallerExercisesWithoutATest(t *testing.T) {
	noTests := map[string]string{"lib/lib_test.go": "package lib\n"}
	ds := fixtureRun(t, noTests, func(inv []m) []m { return inv[:2] }, nil, nil)
	if d := ds["pkg:fx/.#add"]; !d.Gap || d.Reason != reasonExercise {
		t.Fatalf("with no test and no reachable caller want not-exercised, got %v", d)
	}
	ds = fixtureRun(t, noTests, func(inv []m) []m { return inv[:2] }, map[string]bool{"lib/lib.go#Use": true}, nil)
	if d := ds["pkg:fx/.#add"]; d.Gap || !strings.HasPrefix(d.Evidence, "call:lib/lib.go#Use") {
		t.Fatalf("a caller reachable from cmd/pig exercises Add, got %+v", d)
	}
}

func TestDocumentedRenameClosesTheGap(t *testing.T) {
	files := map[string]string{
		"lib/lib.go":      strings.NewReplacer("func Add(", "func Sum(", "return Add(1, 2)", "return Sum(1, 2)").Replace(fxLib),
		"lib/lib_test.go": strings.ReplaceAll(fxTest, "Add(1, 2)", "Sum(1, 2)"),
	}
	only := func(inv []m) []m { return inv[:2] }
	if d := fixtureRun(t, files, only, nil, nil)["pkg:fx/.#add"]; !d.Gap || d.Reason != reasonNoSymbol {
		t.Fatalf("without a rename want no-go-symbol, got %+v", d)
	}
	ds := fixtureRun(t, files, only, nil, renameTable{"pkg:fx/.#add": "lib/lib.go#Sum"})
	if g := gapsOf(ds); len(g) != 0 {
		t.Fatalf("a documented rename should resolve the symbol, gaps: %v", g)
	}
}

// A command-line row has no package symbol for a rule to find, so only a reviewed hand-written closure closes it: a ported row with Go
// targets, production reachability and evidence whose Go references still name declarations. A pending row, an incomplete row and a row
// whose reference names a removed declaration stay gaps.
func TestReviewedCLIRowClosure(t *testing.T) {
	files := map[string]string{"lib/lib.go": fxLib, "lib/lib_test.go": fxTest}
	closure := func(target string) m {
		return m{"pigTargets": []string{target}, "production": []string{"call:" + target}, "evidence": []string{"test:lib/lib_test.go#TestAdd"}}
	}
	ds := fixtureRun(t, files, func(inv []m) []m {
		row := func(id, disp string, extra m) m {
			return m{"id": id, "_mappingOnly": true, "_disposition": disp, "_mapping": extra}
		}
		return append(inv[:2:2],
			row("cli:pi/--pending", "pending", closure("lib/lib.go#Add")),
			row("cli:pi/--ported", "ported", closure("lib/lib.go#Add")),
			row("cli:pi/--no-evidence", "ported", m{"pigTargets": []string{"lib/lib.go#Add"}, "production": []string{"call:lib/lib.go#Add"}}),
			row("cli:pi/--removed", "ported", closure("lib/lib.go#Removed")),
			// No production reachability, and a production reference whose declaration was removed while the target stays.
			row("cli:pi/--no-production", "ported", m{"pigTargets": []string{"lib/lib.go#Add"}, "evidence": []string{"test:lib/lib_test.go#TestAdd"}}),
			row("cli:pi/--removed-production", "ported", m{"pigTargets": []string{"lib/lib.go#Add"}, "production": []string{"call:lib/lib.go#Removed"}, "evidence": []string{"test:lib/lib_test.go#TestAdd"}}),
		)
	}, nil, nil)
	if d := ds["cli:pi/--ported"]; d.Gap || d.Evidence != ruleCLIReviewed || d.Disp != "ported" || d.Pkg != "cli" {
		t.Errorf("a reviewed ported command-line row is not a gap, got %+v", d)
	}
	for _, id := range []string{"cli:pi/--pending", "cli:pi/--no-evidence", "cli:pi/--removed", "cli:pi/--no-production", "cli:pi/--removed-production"} {
		if d := ds[id]; !d.Gap || d.Reason != reasonCLI {
			t.Errorf("%s stays a cli-row gap, got %+v", id, d)
		}
	}
}

func TestDesignedOutAndCLIRows(t *testing.T) {
	// Add is renamed so that, without the designed-out decision, the rules would report no-go-symbol.
	files := map[string]string{
		"lib/lib.go":      strings.NewReplacer("func Add(", "func Sum(", "return Add(1, 2)", "return Sum(1, 2)").Replace(fxLib),
		"lib/lib_test.go": strings.ReplaceAll(fxTest, "Add(1, 2)", "Sum(1, 2)"),
	}
	ds := fixtureRun(t, files, func(inv []m) []m {
		out := []m{}
		for _, e := range inv[:2] {
			e = maps.Clone(e)
			e["_disposition"] = "designed-out"
			out = append(out, e)
		}
		return append(out, m{"id": "cli:pi/--flag", "_mappingOnly": true})
	}, nil, nil)
	if len(ds) != 3 {
		t.Fatalf("got %d decisions, want 3: %v", len(ds), ds)
	}
	for _, id := range []string{"pkg:fx/.#add", "pkg:fx/.#add::call:0"} {
		if d := ds[id]; d.Gap || d.Evidence != ruleDesignedOut || d.Disp != "designed-out" {
			t.Errorf("%s: a designed-out row is not a gap by the ledger decision, got %+v", id, d)
		}
	}
	if d := ds["cli:pi/--flag"]; !d.Gap || d.Reason != reasonCLI || d.Pkg != "cli" {
		t.Errorf("a command-line row is a cli-row gap, got %+v", d)
	}
}

// TestFewestGapsCandidateWins: two exported candidates in the package's seed directories; the one with no gap below it is chosen,
// whatever the seed order.
func TestFewestGapsCandidateWins(t *testing.T) {
	files := map[string]string{
		"lib/gizmo.go":      "package lib\n\n// Gizmo lacks the size field.\ntype Gizmo struct{ Label string }\n",
		"lib/gizmo_test.go": "package lib\n\nimport \"testing\"\n\nfunc TestGizmo(t *testing.T) {\n\tif (Gizmo{Label: \"g\"}).Label != \"g\" {\n\t\tt.Error(\"gizmo\")\n\t}\n}\n",
		"lib2/lib2.go":      "package lib2\n\n// Gizmo has the size field.\ntype Gizmo struct{ Size int }\n\n// Touch is production code that reads Size.\nfunc Touch() int { return Gizmo{Size: 1}.Size }\n",
		"lib2/lib2_test.go": "package lib2\n\nimport \"testing\"\n\nfunc TestGizmo(t *testing.T) {\n\tif (Gizmo{Size: 1}).Size != 1 {\n\t\tt.Error(\"gizmo\")\n\t}\n}\n",
	}
	ds := fixtureRun(t, files, func([]m) []m {
		return []m{
			{"id": "pkg:fx/.#Gizmo", "name": "Gizmo", "kind": "interface", "shape": m{"type": "Gizmo"}},
			{"id": "pkg:fx/.#Gizmo::property:size", "parentId": "pkg:fx/.#Gizmo", "role": "property", "name": "Gizmo.size", "kind": "property", "shape": m{"name": "size", "type": "number"}},
		}
	}, map[string]bool{"lib2/lib2.go#Touch": true}, nil)
	if g := gapsOf(ds); len(g) != 0 {
		t.Fatalf("the complete candidate should be chosen, gaps: %v", g)
	}
	if d := ds["pkg:fx/.#Gizmo"]; d.Candidate != "lib2/lib2.go#Gizmo" {
		t.Errorf("chose %s, want lib2/lib2.go#Gizmo", d.Candidate)
	}
}

// TestTypeNamedInASignatureIsUsed: a type named only in the parameter list of a function reachable from cmd/pig is exercised by
// that function (E2), although no function body names it.
func TestTypeNamedInASignatureIsUsed(t *testing.T) {
	files := map[string]string{
		"lib/opts.go":     "package lib\n\n// Hook carries progress.\ntype Hook struct{ n int }\n\n// Configure takes a hook.\nfunc Configure(_ Hook) {}\n",
		"lib/lib_test.go": "package lib\n",
	}
	only := func([]m) []m {
		return []m{{"id": "pkg:fx/.#Hook", "name": "Hook", "kind": "interface", "shape": m{"type": "Hook"}}}
	}
	if d := fixtureRun(t, files, only, nil, nil)["pkg:fx/.#Hook"]; !d.Gap || d.Reason != reasonExercise {
		t.Fatalf("with no reachable caller want not-exercised, got %+v", d)
	}
	d := fixtureRun(t, files, only, map[string]bool{"lib/opts.go#Configure": true}, nil)["pkg:fx/.#Hook"]
	if d.Gap || d.Evidence != "call:lib/opts.go#Configure" {
		t.Fatalf("a reachable function whose signature names Hook exercises it, got %+v", d)
	}
}

// TestVersionLeapWithUnchangedShapesKeepsEveryRowClosed: a leap carries each row whose shape hash is unchanged (the generator) and the
// detector re-derives it, so nothing closed is reset; even a generator that reset every row to pending gets the same rows back.
func TestVersionLeapWithUnchangedShapesKeepsEveryRowClosed(t *testing.T) {
	l, ds := fixtureLedger(t, nil, nil, nil, nil)
	pending := &mappingDoc{UpstreamVersion: "0.0.0"}
	for _, e := range l.entries {
		pending.Mappings = append(pending.Mappings, pendingRow(e.ID, e.ShapeHash))
	}
	st := syncMapping(l, pending, ds)
	if st.Derived != len(l.entries) || st.Underivable != 0 {
		t.Fatalf("first derivation: %+v for %d rows", st, len(l.entries))
	}
	first, err := encodeMapping(pending)
	if err != nil {
		t.Fatal(err)
	}
	for _, reset := range []bool{false, true} {
		leap := &mappingDoc{UpstreamVersion: "0.0.1"}
		if err := json.Unmarshal(first, leap); err != nil {
			t.Fatal(err)
		}
		if reset {
			for i, row := range leap.Mappings {
				leap.Mappings[i] = pendingRow(row.get("id"), row.get("upstreamShapeHash"))
			}
		}
		syncMapping(l, leap, ds)
		for _, row := range leap.Mappings {
			if row.get("disposition") != "ported" {
				t.Errorf("reset=%v: %s is %s after the leap", reset, row.get("id"), row.get("disposition"))
			}
		}
		again, err := encodeMapping(leap)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, again) {
			t.Errorf("reset=%v: the leap changed the derived ledger", reset)
		}
	}
}

// TestKeyRegistryPropertiesAreKeyConstants: M6 accepts a property of a key registry (an interface whose every property has the literal
// type true, such as pi-tui Keybindings) when the registry's Go key type has a constant whose value is the property name, and only then.
func TestKeyRegistryPropertiesAreKeyConstants(t *testing.T) {
	lib := fxLib + `
// Key is the Go form of the upstream Keys registry.
type Key = string

const (
	KeyUp   Key = "x.up"
	KeyDown Key = "x.down"
)

// Shape has no constants.
type Shape struct{ Up bool }
`
	use := fxUse + `
// Keys hands both keys to a command.
func Keys() []Key { return []Key{KeyUp, KeyDown} }
`
	test := fxTest + `
func TestKeys(t *testing.T) {
	if KeyUp != "x.up" || KeyDown != "x.down" || (Shape{Up: true}).Up != true {
		t.Fatal("keys")
	}
}
`
	keys := "pkg:fx/.#Keys"
	rows := func(extra ...m) func([]m) []m {
		return func(inv []m) []m {
			inv = append(inv,
				m{"id": keys, "name": "Keys", "kind": "interface", "shape": m{"type": "Keys"}},
				m{"id": keys + "::property:x.up", "parentId": keys, "role": "property", "name": "Keys.x.up", "kind": "property", "shape": m{"name": "x.up", "type": "true"}},
				m{"id": keys + "::property:x.down", "parentId": keys, "role": "property", "name": "Keys.x.down", "kind": "property", "shape": m{"name": "x.down", "type": "true"}},
			)
			return append(inv, extra...)
		}
	}
	files := map[string]string{"lib/lib.go": lib, "lib/lib_test.go": test, "lib/use.go": use}
	reach := map[string]bool{"lib/use.go#Members": true, "lib/use.go#Keys": true}
	to := func(target string) renameTable { return renameTable{keys: target} }
	if g := gapsOf(fixtureRun(t, files, rows(), reach, to("lib/lib.go#Key"))); len(g) != 0 {
		t.Fatalf("a registry whose every key is a constant of its Go key type is not a gap: %v", g)
	}
	// P1: a key constant that only a test uses is a stub; the asserting test does not close the member row.
	if d := fixtureRun(t, files, rows(), map[string]bool{"lib/use.go#Members": true}, to("lib/lib.go#Key"))[keys+"::property:x.up"]; !d.Gap || d.Reason != reasonExercise || !strings.Contains(d.Detail, "P1:") {
		t.Fatalf("a key constant with no production use stays the P1 gap, got %+v", d)
	}
	// A second constant for one key is a duplicate, whichever of the two sorts first.
	for _, dup := range []string{"KeyAUp", "KeyZUp"} {
		dupLib := map[string]string{"lib/lib.go": strings.Replace(lib, "\tKeyDown Key", "\t"+dup+" Key = \"x.up\"\n\tKeyDown Key", 1), "lib/lib_test.go": test, "lib/use.go": use}
		if d := fixtureRun(t, dupLib, rows(), reach, to("lib/lib.go#Key"))[keys+"::property:x.up"]; !d.Gap || d.Reason != reasonMember || !strings.Contains(d.Detail, "2 constants") {
			t.Fatalf("%s: a key with two constants stays a member gap, got %+v", dup, d)
		}
	}
	noDown := map[string]string{"lib/lib.go": strings.Replace(lib, "\tKeyDown Key = \"x.down\"\n", "", 1), "lib/lib_test.go": strings.Replace(test, "KeyDown != \"x.down\" || ", "", 1),
		"lib/use.go": strings.Replace(use, "KeyUp, KeyDown", "KeyUp", 1)}
	ds := fixtureRun(t, noDown, rows(), reach, to("lib/lib.go#Key"))
	if d := ds[keys+"::property:x.down"]; !d.Gap || d.Reason != reasonMember {
		t.Fatalf("a key with no constant stays a member gap, got %+v", d)
	}
	if d := ds[keys+"::property:x.up"]; d.Gap {
		t.Fatalf("the key that has a constant is not a gap, got %+v", d)
	}
	mixed := rows(m{"id": keys + "::property:flag", "parentId": keys, "role": "property", "name": "Keys.flag", "kind": "property", "shape": m{"name": "flag", "type": "boolean"}})
	if d := fixtureRun(t, files, mixed, nil, to("lib/lib.go#Key"))[keys+"::property:x.up"]; !d.Gap || d.Reason != reasonMember {
		t.Fatalf("an interface with a property that is not the literal true is no registry, got %+v", d)
	}
	if d := fixtureRun(t, files, rows(), nil, to("lib/lib.go#Shape"))[keys+"::property:x.down"]; !d.Gap || d.Reason != reasonMember {
		t.Fatalf("a Go form that is not a string type has no key constants, got %+v", d)
	}

	// A type map whose every value resolves to one Go type keeps only its keys (ai ApiOptionsMap, every options type StreamOptions).
	typed := func(downGo string) (func([]m) []m, renameTable) {
		return func(inv []m) []m {
			return append(inv,
				m{"id": "pkg:fx/.#UpOptions", "name": "UpOptions", "kind": "interface", "shape": m{"type": "UpOptions"}},
				m{"id": "pkg:fx/.#DownOptions", "name": "DownOptions", "kind": "interface", "shape": m{"type": "DownOptions"}},
				m{"id": keys, "name": "Keys", "kind": "interface", "shape": m{"type": "Keys"}},
				m{"id": keys + "::property:x.up", "parentId": keys, "role": "property", "name": "Keys.x.up", "kind": "property", "shape": m{"name": "x.up", "type": "UpOptions"}},
				m{"id": keys + "::property:x.down", "parentId": keys, "role": "property", "name": "Keys.x.down", "kind": "property", "shape": m{"name": "x.down", "type": "DownOptions"}},
			)
		}, renameTable{keys: "lib/lib.go#Key", "pkg:fx/.#UpOptions": "lib/lib.go#Shape", "pkg:fx/.#DownOptions": downGo}
	}
	inv, rn := typed("lib/lib.go#Shape")
	ds = fixtureRun(t, files, inv, reach, rn)
	for _, id := range []string{keys, keys + "::property:x.up", keys + "::property:x.down"} {
		if d := ds[id]; d.Gap {
			t.Fatalf("a type map whose values are one Go type is its key constants: %s %+v", id, d)
		}
	}
	// A Go alias of the value type is the same Go type (ai.AzureEndpointOptions = StreamOptions).
	aliased := map[string]string{"lib/lib.go": lib + "\n// ShapeAlias is another name of Shape.\ntype ShapeAlias = Shape\n", "lib/lib_test.go": test, "lib/use.go": use}
	inv, rn = typed("lib/lib.go#ShapeAlias")
	if d := fixtureRun(t, aliased, inv, reach, rn)[keys+"::property:x.down"]; d.Gap {
		t.Fatalf("a value type and its Go alias are one Go type, so the key constants stand for the map, got %+v", d)
	}
	inv, rn = typed("lib/lib.go#Config")
	if d := fixtureRun(t, files, inv, reach, rn)[keys+"::property:x.up"]; !d.Gap || d.Reason != reasonMember {
		t.Fatalf("a type map whose values are different Go types keeps them, so a key constant is not enough, got %+v", d)
	}
	// Values that are a Go alias of any carry no options type at all: a stub of every value type, not one shared Go type.
	opaque := map[string]string{"lib/lib.go": lib + "\n// Opaque stands for any value.\ntype Opaque = any\n", "lib/lib_test.go": test, "lib/use.go": use}
	inv, rn = typed("lib/lib.go#Opaque")
	rn["pkg:fx/.#UpOptions"] = "lib/lib.go#Opaque"
	if d := fixtureRun(t, opaque, inv, reach, rn)[keys+"::property:x.up"]; !d.Gap || d.Reason != reasonMember {
		t.Fatalf("a type map whose values are all a Go alias of any is no registry, got %+v", d)
	}
}

// TestJSONDiscriminatorIsMarshalledNotAField: M5 accepts a string-literal property that the Go type writes through MarshalJSON and a
// parameterless method returning that literal, and only then.
func TestJSONDiscriminatorIsMarshalledNotAField(t *testing.T) {
	lib := fxLib + `
type Shape struct{ Size int ` + "`json:\"size\"`" + ` }

func (Shape) kind() string { return "shape" }

// ShapeSize is production code that reads Size.
func ShapeSize() int { return Shape{Size: 1}.Size }

func (s Shape) MarshalJSON() ([]byte, error) { return []byte(` + "`{\"type\":\"` + s.kind() + `\"}`" + `), nil }
`
	test := fxTest + `
func TestShape(t *testing.T) {
	if (Shape{Size: 1}).kind() != "shape" {
		t.Fatal("kind")
	}
}
`
	row := func(lit string) func([]m) []m {
		return func([]m) []m {
			sh := "pkg:fx/.#Shape"
			return []m{
				{"id": sh, "name": "Shape", "kind": "interface", "shape": m{"type": "Shape"}},
				{"id": sh + "::property:type", "parentId": sh, "role": "property", "name": "Shape.type", "kind": "property", "shape": m{"name": "type", "type": lit}},
				{"id": sh + "::property:size", "parentId": sh, "role": "property", "name": "Shape.size", "kind": "property", "shape": m{"name": "size", "type": "number"}},
			}
		}
	}
	files := map[string]string{"lib/lib.go": lib, "lib/lib_test.go": test}
	if g := gapsOf(fixtureRun(t, files, row(`"shape"`), map[string]bool{"lib/lib.go#ShapeSize": true}, nil)); len(g) != 0 {
		t.Fatalf("a marshalled discriminator is not a gap: %v", g)
	}
	ds := fixtureRun(t, files, row(`"other"`), nil, nil)
	if d := ds["pkg:fx/.#Shape::property:type"]; !d.Gap || d.Reason != reasonMember {
		t.Fatalf("a literal no method returns stays a member gap, got %+v", d)
	}
	plain := map[string]string{"lib/lib.go": strings.Replace(lib, "func (s Shape) MarshalJSON", "func (s Shape) Marshal", 1), "lib/lib_test.go": test}
	ds = fixtureRun(t, plain, row(`"shape"`), nil, nil)
	if d := ds["pkg:fx/.#Shape::property:type"]; !d.Gap || d.Reason != reasonMember {
		t.Fatalf("a type that does not marshal itself has no discriminator, got %+v", d)
	}
}

// TestDiscriminatedUnionAliasIsASealedInterface: an alias of a union of object types is a Go interface with one concrete struct per
// member whose discriminator the code returns (U4); a missing member is a gap, not a pass.
func TestDiscriminatedUnionAliasIsASealedInterface(t *testing.T) {
	lib := fxLib + `
// Ev is the Go form of the upstream Ev union.
type Ev interface{ Type() string }

type EvA struct{ X int }

func (EvA) Type() string { return "a" }

type EvB struct{}

func (EvB) Type() string { return "b" }
`
	test := fxTest + `
func TestEv(t *testing.T) {
	var evs = []Ev{EvA{X: 1}, EvB{}}
	if evs[0].Type() != "a" || evs[1].Type() != "b" {
		t.Fatal("type")
	}
}
`
	ts := func(body string) map[string]string {
		return map[string]string{
			"lib/lib.go":      lib,
			"lib/lib_test.go": test,
			".upstream/current/packages/fx/src/ev.ts": "export type Ev = " + body + ";\n",
		}
	}
	row := func([]m) []m {
		return []m{{"id": "pkg:fx/.#Ev", "name": "Ev", "kind": "type-alias", "shape": m{"type": "Ev", "aliasTarget": "Ev"}}}
	}
	if g := gapsOf(fixtureRun(t, ts(`{ type: "a"; x: number } | { type: "b" }`), row, nil, renameTable{"pkg:fx/.#Ev": "lib/lib.go#Ev"})); len(g) != 0 {
		t.Fatalf("a sealed interface with one struct per member is not a gap: %v", g)
	}
	if g := gapsOf(fixtureRun(t, ts("| { readonly type: \"a\"; readonly x: number } // first\n | { readonly type: \"b\" }"), row, nil, renameTable{"pkg:fx/.#Ev": "lib/lib.go#Ev"})); len(g) != 0 {
		t.Fatalf("readonly members and comments do not hide the discriminator: %v", g)
	}
	d := fixtureRun(t, ts("| { readonly type: \"a\"; readonly x: number } | { readonly type: \"b\" } | { readonly type: \"c\" }"), row, nil, renameTable{"pkg:fx/.#Ev": "lib/lib.go#Ev"})["pkg:fx/.#Ev"]
	if !d.Gap || d.Reason != reasonType {
		t.Fatalf("U4 reads a readonly discriminator, so a member with no Go struct is a type gap, got %+v", d)
	}
	ds := fixtureRun(t, ts(`{ type: "a"; x: number } | { type: "b" } | { type: "c" }`), row, nil, renameTable{"pkg:fx/.#Ev": "lib/lib.go#Ev"})
	if d := ds["pkg:fx/.#Ev"]; !d.Gap || d.Reason != reasonType && d.Reason != reasonUndecided {
		t.Fatalf("a member with no Go struct stays a gap, got %+v", d)
	}
}

// TestFunctionAliasIsAOneMethodInterface: an upstream alias of a function type (mcp `type McpFetch = (input: string | URL, init?:
// RequestInit) => Promise<Response>`) may be a Go interface with one method (T7b), whose signature judges the call rows; a Go
// interface with two methods is no function type and stays the A0 gap.
func TestFunctionAliasIsAOneMethodInterface(t *testing.T) {
	test := fxTest + `
func TestFetch(t *testing.T) {
	var f Fetch = http.DefaultClient
	if f == nil {
		t.Fatal("fetch")
	}
}
`
	test = strings.Replace(test, "import (\n", "import (\n\t\"net/http\"\n", 1)
	build := func(iface string) map[string]string {
		lib := strings.Replace(fxLib, "package lib\n", "package lib\n\nimport \"net/http\"\n\nvar _ = http.MethodGet\n", 1) + "\n// Fetch sends one request.\ntype Fetch interface {\n" + iface + "}\n"
		return map[string]string{"lib/lib.go": lib, "lib/lib_test.go": test,
			".upstream/current/packages/fx/src/fetch.ts": "export type Fetch = (input: string | URL, init?: RequestInit) => Promise<Response>;\n"}
	}
	call := m{"typeParameters": []m{}, "parameters": []m{prm("input", "string | URL", false), prm("init", "RequestInit", true)}, "returns": "Promise<Response>"}
	rows := func([]m) []m {
		return []m{
			{"id": "pkg:fx/.#Fetch", "name": "Fetch", "kind": "type-alias", "shape": m{"type": "(input: string | URL, init?: RequestInit) => Promise<Response>", "calls": []m{call}}},
			{"id": "pkg:fx/.#Fetch::call:0", "parentId": "pkg:fx/.#Fetch", "role": "call-overload", "name": "Fetch call 0", "kind": "call-overload", "shape": call},
		}
	}
	rn := renameTable{"pkg:fx/.#Fetch": "lib/lib.go#Fetch", "pkg:fx/.#Fetch::call:0": "lib/lib.go#Fetch"}
	ds := fixtureRun(t, build("\tDo(*http.Request) (*http.Response, error)\n"), rows, nil, rn)
	for _, id := range []string{"pkg:fx/.#Fetch", "pkg:fx/.#Fetch::call:0"} {
		if d := ds[id]; d == nil || d.Gap && (d.Reason == reasonType || d.Reason == reasonSignature) {
			t.Errorf("%s: a one-method interface is the function alias, got %+v", id, d)
		}
	}
	d := fixtureRun(t, build("\tDo(*http.Request) (*http.Response, error)\n\tClose() error\n"), rows, nil, rn)["pkg:fx/.#Fetch"]
	if d == nil || !d.Gap || !strings.Contains(d.Detail, "A0") {
		t.Errorf("a two-method interface stays the A0 gap, got %+v", d)
	}
}

// TestAliasOfAnInlineObjectTypeIsAStruct: A6 judges an alias of an inline object type (comments and readonly ignored) against the Go struct's
// fields, and a member the Go struct lacks is a gap.
func TestAliasOfAnInlineObjectTypeIsAStruct(t *testing.T) {
	lib := fxLib + `
// Opts is the Go form of the upstream Opts alias.
type Opts struct {
	Name    string ` + "`json:\"name\"`" + `
	Retries int    ` + "`json:\"retries\"`" + `
}
`
	test := fxTest + `
func TestOpts(t *testing.T) {
	if (Opts{Name: "a", Retries: 1}).Retries != 1 {
		t.Fatal("retries")
	}
}
`
	build := func(body string) map[string]string {
		return map[string]string{"lib/lib.go": lib, "lib/lib_test.go": test, ".upstream/current/packages/fx/src/opts.ts": "export type Opts = " + body + ";\n"}
	}
	row := func([]m) []m {
		return []m{{"id": "pkg:fx/.#Opts", "name": "Opts", "kind": "type-alias", "shape": m{"type": "Opts", "aliasTarget": "Opts"}}}
	}
	ren := renameTable{"pkg:fx/.#Opts": "lib/lib.go#Opts"}
	ok := "{\n\t/** the name */\n\treadonly name: string;\n\tretries?: number;\n}"
	if g := gapsOf(fixtureRun(t, build(ok), row, nil, ren)); len(g) != 0 {
		t.Fatalf("an inline object alias with a matching struct is not a gap: %v", g)
	}
	bad := "{ name: string; retries?: number; timeout: number }"
	if d := fixtureRun(t, build(bad), row, nil, ren)["pkg:fx/.#Opts"]; !d.Gap {
		t.Fatalf("a member the struct lacks must stay a gap, got %+v", d)
	}
}

// TestAnyOtherAliasBodyIsJudgedByTheTypeRules: A7 judges an alias body that A1-A6 do not cover (an intersection, a union of named
// types) with the type rules; a body they cannot decide stays undecidable (A3).
func TestAnyOtherAliasBodyIsJudgedByTheTypeRules(t *testing.T) {
	lib := fxLib + `
// Base is the Go form of the upstream Base interface.
type Base struct {
	Name string ` + "`json:\"name\"`" + `
}

// Opts is the Go form of the upstream Opts alias.
type Opts struct {
	Base
	Retries int ` + "`json:\"retries\"`" + `
}
`
	test := fxTest + `
func TestOpts(t *testing.T) {
	if (Opts{Base: Base{Name: "a"}, Retries: 1}).Retries != 1 {
		t.Fatal("retries")
	}
}
`
	build := func(body string) map[string]string {
		return map[string]string{"lib/lib.go": lib, "lib/lib_test.go": test,
			".upstream/current/packages/fx/src/opts.ts": "export interface Base { name: string }\nexport type Opts = " + body + ";\n"}
	}
	row := func([]m) []m {
		return []m{{"id": "pkg:fx/.#Opts", "name": "Opts", "kind": "type-alias", "shape": m{"type": "Opts", "aliasTarget": "Opts"}},
			{"id": "pkg:fx/.#Base", "name": "Base", "kind": "interface", "shape": m{"type": "Base"}},
			{"id": "pkg:fx/.#Base::property:name", "parentId": "pkg:fx/.#Base", "role": "property", "name": "Base.name", "kind": "property", "shape": m{"name": "name", "type": "string"}}}
	}
	ren := renameTable{"pkg:fx/.#Opts": "lib/lib.go#Opts"}
	if d := fixtureRun(t, build("Base & { /** retries */ readonly retries?: number }"), row, nil, ren)["pkg:fx/.#Opts"]; d.Gap {
		t.Fatalf("an intersection the Go struct satisfies is not a gap: %+v", d)
	}
	if d := fixtureRun(t, build("Base & { retries?: number; timeout: number }"), row, nil, ren)["pkg:fx/.#Opts"]; !d.Gap || d.Reason == reasonUndecided {
		t.Fatalf("a member the struct lacks must be a decided gap, got %+v", d)
	}
	if d := fixtureRun(t, build("Base & [string, number]"), row, nil, ren)["pkg:fx/.#Opts"]; !d.Gap || d.Reason != reasonUndecided {
		t.Fatalf("a body the type rules cannot decide must stay undecidable, got %+v", d)
	}
}

// TestEncoderPairIsADiscriminator: T12d accepts a string-literal member that a method of the Go type writes as a constant pair, the
// form of a hand-written wire encoder; a different literal, or no such pair, stays a gap. TB1 supplies the shape from a TypeBox schema.
func TestEncoderPairIsADiscriminator(t *testing.T) {
	build := func(pair string) map[string]string {
		lib := fxLib + `
// Hello is the Go form of the upstream Hello alias.
type Hello struct{ Version float64 }

// Fields writes the wire object.
func (h Hello) Fields() [][2]any { return [][2]any{` + pair + `, {"version", h.Version}} }
`
		test := fxTest + `
func TestHello(t *testing.T) {
	if len((Hello{Version: 1}).Fields()) != 2 {
		t.Fatal("fields")
	}
}
`
		return map[string]string{"lib/lib.go": lib, "lib/lib_test.go": test,
			".upstream/current/packages/fx/src/hello.ts": "const HelloSchema = Type.Object({ type: Type.Literal(\"hello\"), version: Type.Integer() });\nexport type Hello = Static<typeof HelloSchema>;\n"}
	}
	row := func([]m) []m {
		return []m{{"id": "pkg:fx/.#Hello", "name": "Hello", "kind": "type-alias", "shape": m{"type": "Hello", "aliasTarget": "Hello"}}}
	}
	ren := renameTable{"pkg:fx/.#Hello": "lib/lib.go#Hello"}
	if d := fixtureRun(t, build(`{"type", "hello"}`), row, nil, ren)["pkg:fx/.#Hello"]; d.Gap {
		t.Fatalf("a discriminator the encoder writes is not a gap: %+v", d)
	}
	for _, pair := range []string{`{"type", "bye"}`, `{"kind", "hello"}`} {
		if d := fixtureRun(t, build(pair), row, nil, ren)["pkg:fx/.#Hello"]; !d.Gap || d.Reason == reasonUndecided {
			t.Fatalf("encoder pair %s must leave a decided gap, got %+v", pair, d)
		}
	}
}

// TestMemberNeedsProductionUse: P1 closes a field or method only when production code reachable from cmd/pig reads, sets or calls it; a
// test that asserts it is not enough, so a caller-free stub stays a gap. Types and functions keep the test route.
func TestMemberNeedsProductionUse(t *testing.T) {
	none := map[string]bool{} // non-nil and empty: nothing is reachable from cmd/pig
	ds := fixtureRun(t, nil, nil, none, nil)
	for _, id := range []string{"pkg:fx/.#Config::property:name", "pkg:fx/.#Config::property:retries", "pkg:fx/.#Widget::property:name"} {
		d := ds[id]
		if !d.Gap || d.Reason != reasonExercise || !strings.HasPrefix(d.Detail, "P1:") {
			t.Errorf("%s: a member only a test asserts must be a P1 gap, got %+v", id, d)
		}
	}
	if d := ds["pkg:fx/.#add"]; d.Gap {
		t.Errorf("a function keeps the test route to closing, got %+v", d)
	}
	if g := gapsOf(fixtureRun(t, nil, nil, nil, nil)); len(g) != 0 {
		t.Errorf("with production use the same members close: %v", g)
	}
}

// TestTestSupportPackageMembersCloseOnTests: a package whose directory ends in "test" (the Go form of an upstream `testing` subpath) is test
// support, so P1 does not ask for production use of the members of `testing` rows; a member no test uses stays a gap.
func TestTestSupportPackageMembersCloseOnTests(t *testing.T) {
	none := map[string]bool{}
	stub := "package lib\n"
	moved := map[string]string{
		"lib/lib.go": stub, "lib/lib_test.go": stub, "lib/use.go": stub,
		"fxtest/lib.go":      strings.Replace(fxLib, "package lib", "package fxtest", 1),
		"fxtest/lib_test.go": strings.Replace(fxTest, "package lib", "package fxtest", 1),
	}
	// The rows belong to the upstream testing subpath (pkg:fx/testing); the same members of pkg:fx/. are covered by
	// TestTestSupportMembersCloseOnlyForTestingRows.
	testingRows := func(inv []m) []m {
		for _, e := range inv {
			for _, k := range []string{"id", "parentId"} {
				if v, ok := e[k].(string); ok {
					e[k] = strings.Replace(v, "pkg:fx/.#", "pkg:fx/testing#", 1)
				}
			}
		}
		return inv
	}
	ds := fixtureRun(t, moved, testingRows, none, nil)
	for _, id := range []string{"pkg:fx/testing#Config::property:name", "pkg:fx/testing#Config::property:retries", "pkg:fx/testing#Widget::property:name"} {
		if d := ds[id]; d.Gap {
			t.Errorf("%s: a test-support member an asserting test uses closes without production use, got %+v", id, d)
		}
	}
	untested := maps.Clone(moved)
	untested["fxtest/lib_test.go"] = "package fxtest\n"
	for _, id := range []string{"pkg:fx/testing#Config::property:name", "pkg:fx/testing#Widget::property:name"} {
		if d := fixtureRun(t, untested, testingRows, none, nil)[id]; !d.Gap || d.Reason != reasonExercise {
			t.Errorf("%s: a test-support member no test uses must stay a gap, got %+v", id, d)
		}
	}
}

// TestErrorRuntimeMembersAreDesignedOut: L1 closes name, stack and an unset cause of a class that inherits its message from the TypeScript
// library as designed-out JavaScript mechanics; a cause that a constructor sets through ErrorOptions, a member of a class that does not
// extend Error, and a member that is not declared in the library stay gaps.
func TestErrorRuntimeMembersAreDesignedOut(t *testing.T) {
	prop := func(owner, name, typ, path string) m {
		return m{"id": owner + "::property:" + name, "parentId": owner, "role": "property", "name": owner + "." + name, "kind": "property",
			"shape": m{"name": name, "type": typ}, "source": m{"path": path}}
	}
	const lib5, lib22 = "node_modules/typescript/lib/lib.es5.d.ts", "node_modules/typescript/lib/lib.es2022.error.d.ts"
	files := map[string]string{"lib/errors.go": "package lib\n\ntype Plain struct{ Message string }\n\ntype WithOptions struct{ Message string }\n\ntype NotAnError struct{}\n"}
	tweak := func(inv []m) []m {
		plain, opts, other := "pkg:fx/.#Plain", "pkg:fx/.#WithOptions", "pkg:fx/.#NotAnError"
		inv = append(inv,
			m{"id": plain, "name": "Plain", "kind": "class", "shape": m{"type": "Plain"}},
			prop(plain, "message", "string", lib5), prop(plain, "name", "string", lib5), prop(plain, "stack", "string | undefined", lib5), prop(plain, "cause", "unknown", lib22),
			m{"id": opts, "name": "WithOptions", "kind": "class", "shape": m{"type": "WithOptions", "constructs": []m{{"parameters": []m{prm("message", "string", false), prm("options", "ErrorOptions", true)}, "returns": "WithOptions"}}}},
			prop(opts, "message", "string", lib5), prop(opts, "cause", "unknown", lib22),
			m{"id": other, "name": "NotAnError", "kind": "class", "shape": m{"type": "NotAnError"}},
			prop(other, "name", "string", lib5), prop(other, "stack", "string | undefined", "packages/fx/src/other.ts"))
		return inv
	}
	ds := fixtureRun(t, files, tweak, nil, nil)
	for _, id := range []string{"pkg:fx/.#Plain::property:name", "pkg:fx/.#Plain::property:stack", "pkg:fx/.#Plain::property:cause"} {
		if d := ds[id]; d.Gap || d.DesignedOut == "" {
			t.Errorf("%s: an Error runtime member must be designed out, got %+v", id, d)
		}
	}
	for _, id := range []string{"pkg:fx/.#WithOptions::property:cause", "pkg:fx/.#NotAnError::property:name", "pkg:fx/.#NotAnError::property:stack"} {
		if d := ds[id]; !d.Gap {
			t.Errorf("%s: must stay a gap, got %+v", id, d)
		}
	}
}

// TestSymbolKeyedMembersAreTheirPascalCaseMethods: N6 finds the Go method for `[Symbol.X]`: LAYOUT_NODE is LayoutNode, asyncDispose is Dispose,
// asyncIterator is Events. A type without that method keeps the member-missing gap.
func TestSymbolKeyedMembersAreTheirPascalCaseMethods(t *testing.T) {
	prop := func(owner, sym, typ string) m {
		name := "[Symbol." + sym + "]"
		return m{"id": owner + "::property:%5BSymbol." + sym + "%5D", "parentId": owner, "role": "property", "name": owner + "." + name, "kind": "property", "shape": m{"name": name, "type": typ}}
	}
	files := map[string]string{
		"lib/sym.go": "package lib\n\ntype Node struct{}\n\nfunc (Node) LayoutNode() string { return \"\" }\nfunc (Node) Dispose() {}\nfunc (Node) Events() []string { return nil }\n\ntype Bare struct{}\n",
	}
	tweak := func(inv []m) []m {
		node, bare := "pkg:fx/.#Node", "pkg:fx/.#Bare"
		return append(inv,
			m{"id": node, "name": "Node", "kind": "class", "shape": m{"type": "Node"}},
			prop(node, "LAYOUT_NODE", "() => string"), prop(node, "asyncDispose", "() => void"), prop(node, "asyncIterator", "() => string[]"),
			m{"id": bare, "name": "Bare", "kind": "class", "shape": m{"type": "Bare"}},
			prop(bare, "LAYOUT_NODE", "() => string"))
	}
	ds := fixtureRun(t, files, tweak, nil, nil)
	for _, id := range []string{"pkg:fx/.#Node::property:%5BSymbol.LAYOUT_NODE%5D", "pkg:fx/.#Node::property:%5BSymbol.asyncDispose%5D", "pkg:fx/.#Node::property:%5BSymbol.asyncIterator%5D"} {
		if d := ds[id]; d.Gap && d.Reason == reasonMember {
			t.Errorf("%s: the Go method for the symbol was not found: %+v", id, d)
		}
	}
	if d := ds["pkg:fx/.#Bare::property:%5BSymbol.LAYOUT_NODE%5D"]; !d.Gap || d.Reason != reasonMember {
		t.Errorf("a type without LayoutNode must stay member-missing, got %+v", d)
	}
}

// TestStaleMemberRenameFallsBackToTheNameRules: a documented member rename whose Go owner no longer exists must not hide the member that the
// name rules find on the type the row resolves to.
func TestStaleMemberRenameFallsBackToTheNameRules(t *testing.T) {
	stale := renameTable{"pkg:fx/.#Config::property:name": "lib/lib.go#Gone.Name"}
	if d := fixtureRun(t, nil, nil, nil, stale)["pkg:fx/.#Config::property:name"]; d.Gap {
		t.Fatalf("a stale rename hid Config.Name: %+v", d)
	}
	for _, id := range []string{"pkg:fx/.#add", "pkg:fx/.#Mode"} {
		ren := renameTable{id: "lib/lib.go#Gone"}
		if d := fixtureRun(t, nil, nil, nil, ren)[id]; d.Gap && d.Reason == reasonNoSymbol {
			t.Errorf("%s: a rename to a missing Go symbol hid the declaration the name rules find: %+v", id, d)
		}
	}
}

// TestConstArrayAliasReadsAGoValueList: A1 accepts the literals of `(typeof PHASES)[number]` from a package-level Go slice of the
// type (the Go form of the `as const` array); a literal neither a constant nor in that list stays a gap.
func TestConstArrayAliasReadsAGoValueList(t *testing.T) {
	build := func(list string) map[string]string {
		lib := fxLib + `
// Phase is the Go form of the upstream Phase alias.
type Phase string

// PhaseOn is the default phase.
const PhaseOn Phase = "on"

// Phases lists the phases in upstream order.
var Phases = []Phase{` + list + `}

// Labels names the phases for display; a list of another type does not supply the alias literals.
var Labels = []string{"off", "on"}
`
		test := fxTest + `
func TestPhases(t *testing.T) {
	if len(Phases) == 0 || PhaseOn != "on" || len(Labels) != 2 {
		t.Fatal("phases")
	}
}
`
		return map[string]string{"lib/lib.go": lib, "lib/lib_test.go": test,
			".upstream/current/packages/fx/src/phase.ts": "export const PHASES = [\"off\", \"on\"] as const;\nexport type Phase = (typeof PHASES)[number];\n"}
	}
	row := func([]m) []m {
		return []m{{"id": "pkg:fx/.#Phase", "name": "Phase", "kind": "type-alias", "shape": m{"type": "Phase", "aliasTarget": "Phase"}}}
	}
	if d := fixtureRun(t, build(`"off", PhaseOn`), row, nil, nil)["pkg:fx/.#Phase"]; d.Gap {
		t.Fatalf("every literal is a constant or a listed value, got %+v: %s", d, d.Detail)
	}
	if d := fixtureRun(t, build(`PhaseOn`), row, nil, nil)["pkg:fx/.#Phase"]; !d.Gap || d.Reason == reasonUndecided {
		t.Fatalf("a literal missing from the Go form must be a decided gap, got %+v", d)
	}
}

// TestExcludeAliasNeedsEveryRemainingConstant: T18 turns Exclude over string literals into the remaining literals, which A1 then
// requires as Go constants; the excluded literal is not required.
func TestExcludeAliasNeedsEveryRemainingConstant(t *testing.T) {
	build := func(consts string) map[string]string {
		lib := fxLib + `
// Level is the Go form of the upstream Level alias.
type Level string

// The levels.
const (
` + consts + `
)
`
		test := fxTest + `
func TestLevel(t *testing.T) {
	if Level("x") == "" {
		t.Fatal("level")
	}
}
`
		return map[string]string{"lib/lib.go": lib, "lib/lib_test.go": test,
			".upstream/current/packages/fx/src/level.ts": "export type AllLevels = \"low\" | \"high\" | \"max\";\nexport type Level = Exclude<AllLevels, \"max\">;\n"}
	}
	row := func([]m) []m {
		return []m{{"id": "pkg:fx/.#Level", "name": "Level", "kind": "type-alias", "shape": m{"type": "Level", "aliasTarget": "Level"}}}
	}
	if d := fixtureRun(t, build("\tLevelLow Level = \"low\"\n\tLevelHigh Level = \"high\""), row, nil, nil)["pkg:fx/.#Level"]; d.Gap {
		t.Fatalf("every remaining literal has a constant, got %+v: %s", d, d.Detail)
	}
	if d := fixtureRun(t, build("\tLevelLow Level = \"low\""), row, nil, nil)["pkg:fx/.#Level"]; !d.Gap || d.Reason == reasonUndecided {
		t.Fatalf("a remaining literal without a constant must be a decided gap, got %+v: %s", d, d.Detail)
	}
}

// TestSettledSiblingAccessor: T3s reads a Promise property from the receiver of its chan struct{} accessor: Closed() signals and
// End() returns the value. Without End the value is lost and the property is a decided gap.
func TestSettledSiblingAccessor(t *testing.T) {
	build := func(end string) map[string]string {
		lib := fxLib + `
// Watch is the Go form of the upstream Watch interface.
type Watch struct{ done chan struct{} }

// Closed is closed when the watch ends.
func (w *Watch) Closed() <-chan struct{} { return w.done }
` + end
		test := fxTest + `
func TestWatch(t *testing.T) {
	w := &Watch{done: make(chan struct{})}
	if w.Closed() == nil {
		t.Fatal("closed")
	}
}
`
		return map[string]string{"lib/lib.go": lib, "lib/lib_test.go": test,
			".upstream/current/packages/fx/src/watch.ts": "export interface Watch {\n\treadonly closed: Promise<string>;\n}\n"}
	}
	row := func([]m) []m {
		return []m{{"id": "pkg:fx/.#Watch", "name": "Watch", "kind": "interface", "shape": m{"type": "Watch"}},
			{"id": "pkg:fx/.#Watch::property:closed", "parentId": "pkg:fx/.#Watch", "role": "property", "name": "Watch.closed", "kind": "property", "shape": m{"name": "closed", "type": "Promise<string>"}}}
	}
	id := "pkg:fx/.#Watch::property:closed"
	if d := fixtureRun(t, build("\n// End returns the reason once Closed is closed.\nfunc (w *Watch) End() string { return \"\" }\n"), row, nil, nil)[id]; d.Reason == reasonType || d.Reason == reasonUndecided {
		t.Fatalf("End reads the settled value, got %+v: %s", d, d.Detail)
	}
	if d := fixtureRun(t, build(""), row, nil, nil)[id]; d.Reason != reasonType {
		t.Fatalf("a chan struct{} without a reader drops the value, got %+v: %s", d, d.Detail)
	}
}

// TestShapeMatchNeedsASharedWord: N5 closes an upstream function by signature only against a Go function that names the same thing;
// an unrelated function of the same shape leaves the row without a Go symbol.
func TestShapeMatchNeedsASharedWord(t *testing.T) {
	build := func(goName string) map[string]string {
		lib := fxLib + `
// Gadget is the Go form of the upstream Gadget interface.
type Gadget struct{ Name string }

// ` + goName + ` describes a gadget.
func ` + goName + `(w *Gadget, prefix string) string { return prefix + w.Name }
`
		test := fxTest + `
func TestGadget(t *testing.T) {
	if ` + goName + `(&Gadget{Name: "a"}, "p") != "pa" {
		t.Fatal("gadget")
	}
}
`
		return map[string]string{"lib/lib.go": lib, "lib/lib_test.go": test,
			".upstream/current/packages/fx/src/gadget.ts": "export interface Gadget { name: string }\nexport function describeGadgetLabel(w: Gadget, prefix: string): string { return prefix + w.name; }\n"}
	}
	row := func([]m) []m {
		return []m{{"id": "pkg:fx/.#describeGadgetLabel", "name": "describeGadgetLabel", "kind": "function", "shape": m{"type": "(w: Gadget, prefix: string) => string", "calls": []m{{"parameters": []m{prm("w", "Gadget", false), prm("prefix", "string", false)}, "returns": "string"}}}},
			{"id": "pkg:fx/.#describeGadgetLabel::call:0", "parentId": "pkg:fx/.#describeGadgetLabel", "role": "call-overload", "name": "describeGadgetLabel call 0", "kind": "call-overload",
				"shape": m{"parameters": []m{prm("w", "Gadget", false), prm("prefix", "string", false)}, "returns": "string"}}}
	}
	id := "pkg:fx/.#describeGadgetLabel"
	if d := fixtureRun(t, build("RenderLabel"), row, nil, nil)[id]; d.Reason == reasonNoSymbol || !strings.HasSuffix(d.Candidate, "#RenderLabel") {
		t.Fatalf("a Go function sharing the word label is the N5 candidate, got %+v: %s (%s)", d, d.Detail, d.Candidate)
	}
	if d := fixtureRun(t, build("RenderTitle"), row, nil, nil)[id]; !d.Gap || d.Reason != reasonNoSymbol {
		t.Fatalf("an unrelated function of the same shape must not be the Go form, got %+v: %s", d, d.Detail)
	}
}

// TestSignalMemberCarriedByEveryConsumersContext: T12s accepts an AbortSignal member of a type that no Go field holds when every Go
// function, method or function field that takes the Go type also takes a context.Context; one consumer without a context keeps it
// undecided.
func TestSignalMemberCarriedByEveryConsumersContext(t *testing.T) {
	consumers := `
// Runner runs an interaction.
type Runner struct {
	Run func(ctx context.Context, i *Interaction) error
}

// Start starts an interaction.
func Start(ctx context.Context, i Interaction) error { return ctx.Err() }

// Hook is a package-level function value; only functions, methods and fields count as consumers.
var Hook func(i Interaction) string
`
	build := func(extra string) map[string]string {
		lib := fxLib + `
// Interaction is the Go form of the upstream Interaction interface.
type Interaction struct {
	Name string ` + "`json:\"name\"`" + `
}
` + extra
		test := fxTest + `
func TestInteraction(t *testing.T) {
	if (Interaction{Name: "a"}).Name != "a" {
		t.Fatal("interaction")
	}
}
`
		return map[string]string{"lib/lib.go": lib, "lib/lib_test.go": test,
			".upstream/current/packages/fx/src/interaction.ts": "export interface Interaction { name: string }\nexport type ProviderInteraction = Interaction & { signal: AbortSignal };\nexport type SignalledInteraction = { signal?: AbortSignal } & (Interaction);\n"}
	}
	row := func([]m) []m {
		return []m{{"id": "pkg:fx/.#SignalledInteraction", "name": "SignalledInteraction", "kind": "type-alias", "shape": m{"type": "SignalledInteraction", "aliasTarget": "SignalledInteraction"}},
			{"id": "pkg:fx/.#ProviderInteraction", "name": "ProviderInteraction", "kind": "type-alias", "shape": m{"type": "ProviderInteraction", "aliasTarget": "ProviderInteraction"}},
			{"id": "pkg:fx/.#Interaction", "name": "Interaction", "kind": "interface", "shape": m{"type": "Interaction"}},
			{"id": "pkg:fx/.#Interaction::property:name", "parentId": "pkg:fx/.#Interaction", "role": "property", "name": "Interaction.name", "kind": "property", "shape": m{"name": "name", "type": "string"}}}
	}
	ren := renameTable{"pkg:fx/.#ProviderInteraction": "lib/lib.go#Interaction", "pkg:fx/.#SignalledInteraction": "lib/lib.go#Interaction"}
	for _, id := range []string{"pkg:fx/.#ProviderInteraction", "pkg:fx/.#SignalledInteraction"} {
		if d := fixtureRun(t, build(consumers+"\n// Inspect names an interaction.\nfunc Inspect(i *Interaction) string { return i.Name }\n"), row, nil, ren)[id]; d.Reason != reasonUndecided {
			t.Fatalf("a consumer without a context leaves the signal unaccounted, got %+v: %s", d, d.Detail)
		}
		if d := fixtureRun(t, build(consumers), row, nil, ren)[id]; d.Reason == reasonUndecided || d.Reason == reasonType {
			t.Fatalf("every consumer takes a context, got %+v: %s", d, d.Detail)
		}
		if d := fixtureRun(t, build(""), row, nil, ren)[id]; d.Reason != reasonUndecided {
			t.Fatalf("without a consumer nothing carries the signal, got %+v: %s", d, d.Detail)
		}
	}
}

// TestLazySiblingField: T10v reads `V | (() => Promise<V>)` as a Go field of V and a sibling <Field>Func that produces V; a missing
// sibling leaves the property undecided and a sibling of another type is a type gap.
func TestLazySiblingField(t *testing.T) {
	lib := fxLib + `
// Loader is the Go form of the upstream Loader interface.
type Loader struct {
	Items     []string
	ItemsFunc func(ctx context.Context) ([]string, error)
}

// Plain is the Go form of the upstream Plain interface.
type Plain struct {
	Items []string
}

// Wrong is the Go form of the upstream Wrong interface.
type Wrong struct {
	Items     []string
	ItemsFunc func() int
}

// Values is the Go form of the upstream Values interface.
type Values struct {
	Items     []int
	ItemsFunc func(ctx context.Context) ([]string, error)
}
`
	test := fxTest + `
func TestLoaders(t *testing.T) {
	l, p, w, v := Loader{Items: []string{"a"}}, Plain{Items: []string{"a"}}, Wrong{Items: []string{"a"}}, Values{Items: []int{1}}
	if l.ItemsFunc != nil || len(l.Items)+len(p.Items)+len(w.Items)+len(v.Items) != 4 || w.ItemsFunc != nil || v.ItemsFunc != nil {
		t.Fatal("loaders")
	}
}
`
	const lazy = "| readonly string[] | (() => Promise<readonly string[]>) | undefined"
	types := map[string]string{"Loader": lazy, "Plain": lazy, "Wrong": lazy, "Values": lazy}
	row := func([]m) []m {
		var rows []m
		for _, n := range []string{"Loader", "Plain", "Wrong", "Values"} {
			id := "pkg:fx/.#" + n
			rows = append(rows, m{"id": id, "name": n, "kind": "interface", "shape": m{"type": n}},
				m{"id": id + "::property:items", "parentId": id, "role": "property", "name": n + ".items", "kind": "property", "shape": m{"name": "items", "type": types[n], "optional": true}})
		}
		return rows
	}
	files := map[string]string{"lib/lib.go": lib, "lib/lib_test.go": test,
		".upstream/current/packages/fx/src/loader.ts": ""}
	for _, n := range []string{"Loader", "Plain", "Wrong", "Values"} {
		files[".upstream/current/packages/fx/src/loader.ts"] += "export interface " + n + " { items?: " + types[n] + " }\n"
	}
	out := fixtureRun(t, files, row, nil, nil)
	if d := out["pkg:fx/.#Loader::property:items"]; d.Reason == reasonUndecided || d.Reason == reasonType {
		t.Errorf("Loader.items with ItemsFunc: got %+v: %s", d, d.Detail)
	}
	if d := out["pkg:fx/.#Plain::property:items"]; d.Reason != reasonUndecided {
		t.Errorf("Plain.items without a sibling: got %+v: %s", d, d.Detail)
	}
	for _, n := range []string{"Wrong", "Values"} {
		if d := out["pkg:fx/.#"+n+"::property:items"]; d.Reason != reasonType {
			t.Errorf("%s.items with a field or sibling of another type: got %+v: %s", n, d, d.Detail)
		}
	}
}

// TestReviewedTypesInTheDetector: a reviewed exception closes an undecided property (a field or a getter method) and an undecided
// alias for their recorded Go targets and nothing else.
func TestReviewedTypesInTheDetector(t *testing.T) {
	lib := fxLib + `
// Pick holds a picked size.
type Pick struct {
	Basis *int
	Price func(tier string) string
}

// Size returns the picked size.
func (p Pick) Size() *int { return p.Basis }

// Name is the Go form of the upstream Name alias.
type Name = string
`
	test := fxTest + `
func TestPick(t *testing.T) {
	var n Name = "a"
	if (Pick{}).Basis != nil || (Pick{}).Size() != nil || n != "a" {
		t.Fatal("pick")
	}
}
`
	row := func([]m) []m {
		return []m{{"id": "pkg:fx/.#Pick", "name": "Pick", "kind": "interface", "shape": m{"type": "Pick"}},
			{"id": "pkg:fx/.#Pick::property:basis", "parentId": "pkg:fx/.#Pick", "role": "property", "name": "Pick.basis", "kind": "property",
				"shape": m{"name": "basis", "type": `number | "auto" | undefined`, "optional": true}},
			{"id": "pkg:fx/.#Pick::property:size", "parentId": "pkg:fx/.#Pick", "role": "property", "name": "Pick.size", "kind": "property",
				"shape": m{"name": "size", "type": `number | "auto"`, "readonly": true}},
			{"id": "pkg:fx/.#Name", "name": "Name", "kind": "type-alias", "shape": m{"type": "Name", "aliasTarget": "Name"}}}
	}
	files := func(reviewed string) map[string]string {
		f := map[string]string{"lib/lib.go": lib, "lib/lib_test.go": test,
			".upstream/current/packages/fx/src/pick.ts": "export interface Pick { basis?: number | \"auto\" }\nexport type Name = Base | Mod<Base>;\n"}
		if reviewed != "" {
			f[reviewedTypesFile] = reviewed
		}
		return f
	}
	ids := []string{"pkg:fx/.#Pick::property:basis", "pkg:fx/.#Pick::property:size", "pkg:fx/.#Name"}
	without := fixtureRun(t, files(""), row, nil, nil)
	for _, id := range ids {
		if d := without[id]; d.Reason != reasonUndecided {
			t.Fatalf("%s without an exception: got %+v: %s", id, d, d.Detail)
		}
	}
	with := fixtureRun(t, files(`{"pkg:fx/.#Pick::property:basis": {"go": "lib/lib.go#Pick.Basis", "reason": "r"}, "pkg:fx/.#Pick::property:size": {"go": "lib/lib.go#Pick.Size", "reason": "r"}, "pkg:fx/.#Name": {"go": "lib/lib.go#Name", "reason": "r"}}`), row, nil, nil)
	for _, id := range ids {
		if d := with[id]; d.Reason == reasonUndecided || d.Reason == reasonType {
			t.Errorf("%s with an exception: got %+v: %s", id, d, d.Detail)
		}
	}
	elsewhere := fixtureRun(t, files(`{"pkg:fx/.#Pick::property:basis": {"go": "lib/lib.go#Other.Basis", "reason": "r"}}`), row, nil, nil)
	if d := elsewhere["pkg:fx/.#Pick::property:basis"]; d.Reason != reasonUndecided {
		t.Errorf("an exception for another Go target does not apply, got %+v", d)
	}
}

// TestMutantKillsOnlyATestThatChecksTheMethod: `autobind -mutate` replaces a library method body with its zero results and records a
// test only when it passes on the original and fails on the mutant. A test that merely calls the method, and a test that
// does not pass on the original, record nothing.
func TestMutantKillsOnlyATestThatChecksTheMethod(t *testing.T) {
	files := map[string]string{"lib/lib_test.go": fxTest + `
func TestWidgetNameChecks(t *testing.T) {
	if NewWidget("a").Name() != "a" {
		t.Fatal("name")
	}
}

func TestWidgetNameOnlyCalls(t *testing.T) {
	_ = NewWidget("a").Name()
}

func TestWidgetNameFailsAlready(t *testing.T) {
	t.Fatal("broken before the mutant")
}
`}
	fixtureRun(t, files, nil, nil, nil)
	det, root := fixtureLast.det, fixtureLast.root
	s := &sym{Kind: "method", Name: "Name", Owner: "Widget", Dir: "lib", File: "lib/lib.go"}
	lib := newLibrary(root)
	killed := lib.mutantKills(det, s, []string{"test:lib/lib_test.go#TestWidgetNameOnlyCalls", "test:lib/lib_test.go#TestWidgetNameFailsAlready", "test:lib/lib_test.go#TestWidgetNameChecks"}, t.TempDir())
	if len(killed) != 1 || killed[0] != "test:lib/lib_test.go#TestWidgetNameChecks" {
		t.Fatalf("kills = %v, want only TestWidgetNameChecks", killed)
	}
	src, ok := lib.mutantSource(det, s)
	if !ok || !strings.Contains(string(src), "return *new(string)") || strings.Contains(string(src), "return w.n") {
		t.Fatalf("mutant source did not replace the body with the zero result: %v\n%s", ok, src)
	}
}

// TestCallRowTypeParameters: a call row is judged with its own type parameters, so a lookup by a type-parameter key may return the
// Go pair (T, bool) (S6k).
func TestCallRowTypeParameters(t *testing.T) {
	lib := fxLib + `
// Lookup finds a mode by id.
func Lookup(id string) (Mode, bool) { return "", id != "" }
`
	test := fxTest + `
func TestLookup(t *testing.T) {
	if _, ok := Lookup("a"); !ok {
		t.Fatal("lookup")
	}
}
`
	call := func(typeParams bool) m {
		c := m{"parameters": []m{prm("id", "TId", false)}, "returns": "Mode"}
		if typeParams {
			c["typeParameters"] = []m{{"name": "TId", "constraint": "string", "default": ""}}
		}
		return c
	}
	for _, typeParams := range []bool{true, false} {
		row := func([]m) []m {
			return []m{{"id": "pkg:fx/.#lookup", "name": "lookup", "kind": "function", "shape": m{"type": "<TId extends string>(id: TId) => Mode", "calls": []m{call(typeParams)}}},
				{"id": "pkg:fx/.#lookup::call:0", "parentId": "pkg:fx/.#lookup", "role": "call-overload", "name": "lookup call 0", "kind": "call-overload", "shape": call(typeParams)}}
		}
		d := fixtureRun(t, map[string]string{"lib/lib.go": lib, "lib/lib_test.go": test}, row, nil, nil)["pkg:fx/.#lookup::call:0"]
		if closed := !d.Gap; closed != typeParams {
			t.Errorf("type parameters recorded = %v: got %+v: %s", typeParams, d, d.Detail)
		}
	}
}

// TestTestSupportMembersCloseOnlyForTestingRows: P1 is waived for a member of a test-support package (a directory whose name ends in
// "test") only when the row belongs to an upstream `testing` subpath. The same Go member bound to a row of another entry point keeps P1.
func TestTestSupportMembersCloseOnlyForTestingRows(t *testing.T) {
	files := map[string]string{
		"lib/libtest/harness.go":      "package libtest\n\n// Harness drives a conformance run.\ntype Harness struct{ n int }\n\n// Run runs it.\nfunc (h *Harness) Run() int { return h.n }\n",
		"lib/libtest/harness_test.go": "package libtest\n\nimport \"testing\"\n\nfunc TestHarnessRun(t *testing.T) {\n\tif (&Harness{n: 2}).Run() != 2 {\n\t\tt.Fatal(\"run\")\n\t}\n}\n",
	}
	run := m{"returns": "number"}
	rows := func(inv []m) []m {
		for _, id := range []string{"pkg:fx/testing#Harness", "pkg:fx/.#Probe"} {
			inv = append(inv,
				m{"id": id, "name": "Harness", "kind": "interface", "shape": m{"type": "Harness"}},
				m{"id": id + "::property:run", "parentId": id, "role": "property", "name": "Harness.run", "kind": "property",
					"shape": m{"name": "run", "type": "() => number", "calls": []m{run}}},
				m{"id": id + "::property:run::call:0", "parentId": id + "::property:run", "role": "call-overload", "name": "Harness.run call 0", "kind": "call-overload", "shape": run},
			)
		}
		return inv
	}
	renames := renameTable{"pkg:fx/testing#Harness": "lib/libtest/harness.go#Harness", "pkg:fx/.#Probe": "lib/libtest/harness.go#Harness"}
	ds := fixtureRun(t, files, rows, nil, renames)
	if d := ds["pkg:fx/testing#Harness::property:run"]; d == nil || d.Gap || d.Evidence != "test:lib/libtest/harness_test.go#TestHarnessRun" {
		t.Fatalf("a testing-subpath member closes on its asserting test, got %+v", d)
	}
	if d := ds["pkg:fx/.#Probe::property:run"]; d == nil || !d.Gap || d.Reason != reasonExercise || !strings.Contains(d.Detail, "P1:") {
		t.Fatalf("a member of another entry point keeps P1 even in a test-support package, got %+v", d)
	}
}

// TestRenamedCallOverloadIsJudgedAgainstItsOwnMethod (OV1): Pi's run has a second overload (x, y) that Go spells as another method,
// RunWith; documenting that rename judges the overload against RunWith, while the first overload keeps the property's own Run. Without
// the rename the second overload is a signature gap against Run.
func TestRenamedCallOverloadIsJudgedAgainstItsOwnMethod(t *testing.T) {
	cfg := "pkg:fx/.#Config"
	files := map[string]string{
		"lib/lib.go": strings.Replace(fxLib, "// Add is the Go form", "// RunWith is the second overload of run.\nfunc (c *Config) RunWith(ctx context.Context, x, y string) error { return nil }\n\n// Add is the Go form", 1),
	}
	second := m{"parameters": []m{prm("x", "string", false), prm("y", "string", false), prm("signal", "AbortSignal", true)}, "returns": "Promise<void>"}
	tweak := func(inv []m) []m {
		return append(inv, m{"id": cfg + "::property:run::call:1", "parentId": cfg + "::property:run", "role": "call-overload", "name": "Config.run call 1", "kind": "call-overload", "shape": second})
	}
	before := fixtureRun(t, files, tweak, nil, nil)[cfg+"::property:run::call:1"]
	if before == nil || before.Reason != reasonSignature {
		t.Fatalf("without a rename the second overload is judged against Run and is a signature gap, got %+v", before)
	}
	after := fixtureRun(t, files, tweak, nil, renameTable{cfg + "::property:run::call:1": "lib/lib.go#Config.RunWith"})
	if d := after[cfg+"::property:run::call:1"]; d.Reason == reasonSignature {
		t.Fatalf("with the rename the overload is judged against RunWith, got %+v", d)
	}
	if d := after[cfg+"::property:run::call:0"]; d.Reason == reasonSignature {
		t.Fatalf("the first overload keeps Run, got %+v", d)
	}
}

// TestSatisfiesConstIsJudgedByItsContract: V3 judges `export const X = { ... } as const satisfies T` against T, since `as const`
// narrows its literals at compile time only; without the clause the narrowed literal type is judged.
func TestSatisfiesConstIsJudgedByItsContract(t *testing.T) {
	ts := fxAlias + "export type KeyDef = { keys: string | string[] };\nexport type KeyDefs = Record<string, KeyDef>;\n" +
		"export const KEYS = {\n\t\"fx.a\": { keys: \"up\" },\n} as const satisfies KeyDefs;\n"
	goSrc := fxLib + "\n// KeyDef is one key binding.\ntype KeyDef struct {\n\tKeys []string `json:\"keys\"`\n}\n\n// Keys is KEYS.\nvar Keys = map[string]KeyDef{\"fx.a\": {Keys: []string{\"up\"}}}\n"
	row := func(inv []m) []m {
		return append(inv, m{"id": "pkg:fx/.#KEYS", "name": "KEYS", "kind": "variable", "shape": m{"type": `{ readonly "fx.a": { readonly keys: "up"; }; }`}})
	}
	for _, tc := range []struct {
		name, ts string
		mismatch bool
	}{{"satisfies", ts, false}, {"no clause", strings.Replace(ts, " as const satisfies KeyDefs", "", 1), true},
		{"a later constant's clause", strings.Replace(ts, " as const satisfies KeyDefs", "", 1) + "export const MORE = {\n\t\"fx.b\": { keys: \"down\" },\n} as const satisfies KeyDefs;\n", true}} {
		out := fixtureRun(t, map[string]string{".upstream/current/packages/fx/src/types.ts": tc.ts, "lib/lib.go": goSrc}, row, nil, nil)
		d := out["pkg:fx/.#KEYS"]
		if d == nil || (d.Reason == reasonType) != tc.mismatch {
			t.Errorf("%s: KEYS = %+v, want type mismatch %v", tc.name, d, tc.mismatch)
		}
	}
	root := t.TempDir()
	src := filepath.Join(root, ".upstream/current/packages/fx/src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "types.ts"), []byte(ts), 0o644); err != nil {
		t.Fatal(err)
	}
	if tab := tsAliases(root); tab[satisfiesKey+"fx"]["KEYS"] == nil || tab.lookup("zz", "KEYS") != nil {
		t.Errorf("the satisfies table records KEYS and alias lookup never reads it: %v", tab[satisfiesKey+"fx"])
	}
}

// TestGenericMethodIsAPackageFunctionWithTheReceiverFirst (G1): Go methods cannot have type parameters, so a documented rename may point a
// property at a package function whose first parameter is the receiver. The receiver is not a parameter of the upstream call.
func TestGenericMethodIsAPackageFunctionWithTheReceiverFirst(t *testing.T) {
	cfg := "pkg:fx/.#Config"
	files := map[string]string{
		"lib/lib.go": strings.Replace(fxLib, "// Add is the Go form", "// RunOn is run as a package function over the receiver.\nfunc RunOn(c *Config, ctx context.Context, x string) error { return nil }\n\n// Add is the Go form", 1),
	}
	rn := func(target string) renameTable {
		return renameTable{cfg + "::property:run": target}
	}
	with := fixtureRun(t, files, nil, nil, rn("lib/lib.go#RunOn"))
	for _, id := range []string{cfg + "::property:run", cfg + "::property:run::call:0"} {
		if d := with[id]; d.Reason == reasonMember || d.Reason == reasonSignature {
			t.Fatalf("%s: a package function with the receiver first stands for the method, got %+v", id, d)
		}
	}
	if d := fixtureRun(t, files, nil, nil, nil)[cfg+"::property:run"]; d.Reason == reasonMember || d.Reason == reasonSignature {
		t.Fatalf("the unrenamed method Run is unaffected, got %+v", d)
	}
	bad := strings.Replace(files["lib/lib.go"], "func RunOn(c *Config, ctx context.Context, x string)", "func RunOn(c *Config, ctx context.Context)", 1)
	if d := fixtureRun(t, map[string]string{"lib/lib.go": bad}, nil, nil, rn("lib/lib.go#RunOn"))[cfg+"::property:run::call:0"]; d.Reason != reasonSignature {
		t.Fatalf("a package function that drops a parameter is a signature gap, got %+v", d)
	}
	// A first parameter of another type is a real parameter, not the receiver: RunWith(label string, ctx, x) takes one more than run.
	other := strings.Replace(files["lib/lib.go"], "func RunOn(c *Config, ctx context.Context, x string)", "func RunOn(label string, ctx context.Context, x string)", 1)
	if d := fixtureRun(t, map[string]string{"lib/lib.go": other}, nil, nil, rn("lib/lib.go#RunOn"))[cfg+"::property:run::call:0"]; d.Reason != reasonSignature {
		t.Fatalf("a package function whose first parameter is not the receiver keeps it, got %+v", d)
	}
}

// TestGenericMethodReceiverFollowsTheLeadingContext (G1): Go puts the context first, so a package function standing for a generic method takes
// (ctx, receiver, ...): the context is the upstream signal and the receiver after it is not an upstream parameter. A second parameter that is
// not the owner is a real parameter.
func TestGenericMethodReceiverFollowsTheLeadingContext(t *testing.T) {
	cfg := "pkg:fx/.#Config"
	files := map[string]string{
		"lib/lib.go": strings.Replace(fxLib, "// Add is the Go form", "// RunCtxOn is run as a package function over the receiver, context first.\nfunc RunCtxOn(ctx context.Context, c *Config, x string) error { return nil }\n\n// Add is the Go form", 1),
	}
	rn := renameTable{cfg + "::property:run": "lib/lib.go#RunCtxOn"}
	for _, id := range []string{cfg + "::property:run", cfg + "::property:run::call:0"} {
		if d := fixtureRun(t, files, nil, nil, rn)[id]; d.Reason == reasonMember || d.Reason == reasonSignature {
			t.Fatalf("%s: (ctx, receiver, x) stands for the method, got %+v", id, d)
		}
	}
	other := strings.Replace(files["lib/lib.go"], "func RunCtxOn(ctx context.Context, c *Config, x string)", "func RunCtxOn(ctx context.Context, label string, x string)", 1)
	if d := fixtureRun(t, map[string]string{"lib/lib.go": other}, nil, nil, rn)[cfg+"::property:run::call:0"]; d.Reason != reasonSignature {
		t.Fatalf("a second parameter that is not the receiver is a real parameter, got %+v", d)
	}
}

// TestInsertionOrderedObjectIsTheRecord (T6m): `{ [key: string]: V }` keeps JavaScript's property order, which a Go map loses, so a Go type
// whose method set has Get(key) (V, bool), Set(key, V) and Keys() []string (chord's JsonObject) is its Go form, and V has to agree with
// the record's value. A type without Set, or with another value type, stays a mismatch.
func TestInsertionOrderedObjectIsTheRecord(t *testing.T) {
	holder := "pkg:fx/.#Holder"
	rows := func([]m) []m {
		return []m{
			{"id": holder, "name": "Holder", "kind": "interface", "shape": m{"type": "Holder"}},
			{"id": holder + "::property:attrs", "parentId": holder, "role": "property", "name": "Holder.attrs", "kind": "property", "shape": m{"name": "attrs", "type": "Record<string, string>"}},
		}
	}
	lib := func(ordered string) map[string]string {
		return map[string]string{"lib/lib.go": strings.Replace(fxLib, "// Add is the Go form", ordered+"\n// Holder carries attrs.\ntype Holder struct {\n\tAttrs *Ordered `json:\"attrs\"`\n}\n\n// Add is the Go form", 1)}
	}
	const full = `// Ordered keeps insertion order.
type Ordered struct{ keys []string }

// Get reads a key.
func (o *Ordered) Get(key string) (string, bool) { return "", false }

// Set writes a key.
func (o *Ordered) Set(key string, value string) { o.keys = append(o.keys, key) }

// Keys lists the keys in order.
func (o *Ordered) Keys() []string { return o.keys }
`
	rn := renameTable{holder: "lib/lib.go#Holder"}
	decide := func(ordered string) *decision {
		return fixtureRun(t, lib(ordered), rows, nil, rn)[holder+"::property:attrs"]
	}
	// A decided type shows as a not-exercised gap here: Holder is not reached by production.
	if d := decide(full); d == nil || d.Reason == reasonType {
		t.Fatalf("an insertion-ordered string object is Record<string, string>, got %+v", d)
	}
	noSet := strings.Replace(full, "// Set writes a key.\nfunc (o *Ordered) Set(key string, value string) { o.keys = append(o.keys, key) }\n", "", 1)
	if d := decide(noSet); d == nil || d.Reason != reasonType {
		t.Fatalf("an object without Set is not the ordered record, got %+v", d)
	}
	otherValue := strings.NewReplacer("(string, bool)", "(int, bool)", "return \"\", false", "return 0, false", "value string", "value int").Replace(full)
	if d := decide(otherValue); d == nil || d.Reason != reasonType {
		t.Fatalf("a value type that is not the record's value is a mismatch, got %+v", d)
	}
}

// TestExternallyImportedNameIsTheModulesType (T9x): a package that imports Content from an external module (google-shared.ts imports it from
// "@google/genai") means that module's type, not a same-named unexported alias another file of the package declares (utils/text.ts's
// `type Content = TextContent | ...`). The alias is not read for the name, and the module's documented Go representation (representations.json,
// keyed module:Name) stands for it.
func TestExternallyImportedNameIsTheModulesType(t *testing.T) {
	holder := "pkg:fx/.#Holder"
	rows := func([]m) []m {
		return []m{
			{"id": holder, "name": "Holder", "kind": "interface", "shape": m{"type": "Holder"}},
			{"id": holder + "::property:items", "parentId": holder, "role": "property", "name": "Holder.items", "kind": "property", "shape": m{"name": "items", "type": "Content[]"}},
		}
	}
	files := func(reps string) map[string]string {
		out := map[string]string{
			"lib/lib.go":                             strings.Replace(fxLib, "// Add is the Go form", "// Wire is the external module's Content.\ntype Wire struct{ Raw string }\n\n// Holder carries items.\ntype Holder struct {\n\tItems []Wire `json:\"items\"`\n}\n\n// Add is the Go form", 1),
			".upstream/current/packages/fx/src/a.ts": "import type { Content } from \"@x/ext\";\nexport type Use = Content;\n",
			".upstream/current/packages/fx/src/b.ts": "type Content = { raw: string };\n",
		}
		if reps != "" {
			out["test/parity/interface-closure/autobind/representations.json"] = reps
		}
		return out
	}
	rn := renameTable{holder: "lib/lib.go#Holder"}
	// The member's type verdict is what is judged: Holder is not reached by production here, so a decided type shows as a not-exercised gap.
	if d := fixtureRun(t, files(`{"@x/ext:Content": "lib.Wire"}`), rows, nil, rn)[holder+"::property:items"]; d == nil || d.Reason == reasonUndecided {
		t.Fatalf("an externally imported Content with a documented representation agrees with it, got %+v", d)
	}
	if d := fixtureRun(t, files(""), rows, nil, rn)[holder+"::property:items"]; d == nil || d.Reason != reasonUndecided {
		t.Fatalf("without the representation the module's type is undecided; the package's local alias {raw: string} must not stand for it, got %+v", d)
	}
}

// TestJsonRepresentationIsItsArgument (T9j): chord types.ts:26 JsonRepresentation<T> is T as strict JSON, which Go carries as the concrete Go type of T
// (the lead's ruling of 2026-10-07 15:20 MDT), so a member typed JsonRepresentation<string[]> agrees with a Go []string and not with an int.
func TestJsonRepresentationIsItsArgument(t *testing.T) {
	holder := "pkg:fx/.#Holder"
	rows := func([]m) []m {
		return []m{
			{"id": holder, "name": "Holder", "kind": "interface", "shape": m{"type": "Holder"}},
			{"id": holder + "::property:items", "parentId": holder, "role": "property", "name": "Holder.items", "kind": "property", "shape": m{"name": "items", "type": "JsonRepresentation<string[]>"}},
		}
	}
	lib := func(field string) map[string]string {
		return map[string]string{"lib/lib.go": strings.Replace(fxLib, "// Add is the Go form", "// Holder carries items.\ntype Holder struct {\n\tItems "+field+" `json:\"items\"`\n}\n\n// Add is the Go form", 1)}
	}
	rn := renameTable{holder: "lib/lib.go#Holder"}
	if d := fixtureRun(t, lib("[]string"), rows, nil, rn)[holder+"::property:items"]; d.Reason == reasonMember || d.Reason == reasonType {
		t.Fatalf("JsonRepresentation<string[]> against []string agrees, got %+v", d)
	}
	if d := fixtureRun(t, lib("int"), rows, nil, rn)[holder+"::property:items"]; d.Reason != reasonType {
		t.Fatalf("JsonRepresentation<string[]> against int is a type gap, got %+v", d)
	}
}

// TestStringAnyMapCarriesEveryOptionalMember (M7): an upstream interface of optional members that Go declares as map[string]any is a bag of
// keys, so each optional member is carried; a required member or a struct without the field is still judged by the member rules.
func TestStringAnyMapCarriesEveryOptionalMember(t *testing.T) {
	opts := "pkg:fx/.#Bag"
	prop := func(optional bool) []m {
		return []m{
			{"id": opts, "name": "Bag", "kind": "interface", "shape": m{"type": "Bag"}},
			{"id": opts + "::property:only", "parentId": opts, "role": "property", "name": "Bag.only", "kind": "property", "shape": m{"name": "only", "optional": optional, "type": "string[]"}},
		}
	}
	files := map[string]string{
		"lib/lib.go": strings.Replace(fxLib, "// Add is the Go form", "// Bag is a bag sent verbatim.\ntype Bag = map[string]any\n\n// Add is the Go form", 1),
	}
	rn := renameTable{opts: "lib/lib.go#Bag"}
	if d := fixtureRun(t, files, func([]m) []m { return prop(true) }, nil, rn)[opts+"::property:only"]; d.Reason == reasonMember {
		t.Fatalf("an optional member of a map[string]any bag is a key, got %+v", d)
	}
	if d := fixtureRun(t, files, func([]m) []m { return prop(false) }, nil, rn)[opts+"::property:only"]; d.Reason != reasonMember {
		t.Fatalf("a required member is not carried by the map, got %+v", d)
	}
}

// TestRenamedFunctionOverloadIsJudgedAgainstItsOwnFunction (OV1): an overloaded top-level upstream function whose second overload is a
// separate Go function (replicatedState(source, options) and replicatedState(initial)) judges that call row against its own function.
func TestRenamedFunctionOverloadIsJudgedAgainstItsOwnFunction(t *testing.T) {
	add := "pkg:fx/.#add"
	files := map[string]string{"lib/lib.go": strings.Replace(fxLib, "// Add is the Go form", "// AddOne is the one-argument overload of add.\nfunc AddOne(a int) int { return a }\n\n// Add is the Go form", 1)}
	tweak := func(inv []m) []m {
		return append(inv, m{"id": add + "::call:1", "parentId": add, "role": "call-overload", "name": "add call 1", "kind": "call-overload",
			"shape": m{"parameters": []m{prm("a", "number", false)}, "returns": "number"}})
	}
	if d := fixtureRun(t, files, tweak, nil, nil)[add+"::call:1"]; d == nil || d.Reason != reasonSignature {
		t.Fatalf("without a rename the one-argument overload is judged against Add and is a signature gap, got %+v", d)
	}
	after := fixtureRun(t, files, tweak, nil, renameTable{add + "::call:1": "lib/lib.go#AddOne"})
	if d := after[add+"::call:1"]; d == nil || d.Reason == reasonSignature {
		t.Fatalf("with the rename the overload is judged against AddOne, got %+v", d)
	}
	if d := after[add+"::call:0"]; d == nil || d.Reason == reasonSignature {
		t.Fatalf("the first overload keeps Add, got %+v", d)
	}
}

// TestCallbackPropertyIsAGetterSetterPair (M2f): a writable upstream callback property (tui `onDebug?: () => void`, tui.ts:457) that
// Go keeps behind the getter OnDone() func() and the setter SetOnDone(func()) is the getter's result, so the property is
// judged against func(), not against the getter (which returns a value the callback does not). Without the setter, with a setter of another function type, or
// for a readonly property, the getter is judged as the callback itself.
func TestCallbackPropertyIsAGetterSetterPair(t *testing.T) {
	cfg := "pkg:fx/.#Config"
	prop := cfg + "::property:onDone"
	run := func(methods string, readonly bool) *decision {
		files := map[string]string{"lib/lib.go": strings.Replace(fxLib, "// Add is the Go form", methods+"\n// Add is the Go form", 1)}
		return fixtureRun(t, files, func(inv []m) []m {
			return append(inv, m{"id": prop, "parentId": cfg, "role": "property", "name": "Config.onDone", "kind": "property",
				"shape": m{"name": "onDone", "type": "(() => void) | undefined", "optional": true, "readonly": readonly}})
		}, nil, renameTable{prop: "lib/lib.go#Config.OnDone"})[prop]
	}
	const getter = "// OnDone returns the stored callback.\nfunc (c *Config) OnDone() func() { return nil }\n"
	const setter = "\n// SetOnDone stores the callback.\nfunc (c *Config) SetOnDone(fn func()) {}\n"
	if d := run(getter+setter, false); d == nil || d.Reason == reasonSignature || d.Reason == reasonType {
		t.Errorf("the getter/setter pair carries the callback, got %+v", d)
	}
	for name, methods := range map[string]string{
		"no setter":    getter,
		"other setter": getter + "\n// SetOnDone stores another callback.\nfunc (c *Config) SetOnDone(fn func(string)) {}\n",
	} {
		if d := run(methods, false); d == nil || d.Reason != reasonSignature {
			t.Errorf("%s: the getter alone is judged as the callback, got %+v", name, d)
		}
	}
	if d := run(getter+setter, true); d == nil || d.Reason != reasonSignature {
		t.Errorf("readonly: a readonly property has no setter pair, got %+v", d)
	}
}

// TestSharedOptionsFieldCarriesALiteralUnion (T10m): when two upstream option types are documented as one Go struct (Pi's per-provider
// options as ai.StreamOptions, LEAD-ANSWERS-ledger #2), the struct's empty-interface field carries each provider's type for its key,
// so a union of string literals agrees with it. A Go struct that is the form of one upstream type only keeps T1's demand for a named
// string type, and a type that is not a literal union gains nothing from the shared field.
func TestSharedOptionsFieldCarriesALiteralUnion(t *testing.T) {
	files := map[string]string{"lib/lib.go": strings.Replace(fxLib, "\tOnEvent func(ctx context.Context, e string)\n",
		"\tOnEvent func(ctx context.Context, e string)\n\tChoice  any `json:\"choice,omitempty\"`\n\tPicked  int `json:\"picked,omitempty\"`\n", 1),
		".upstream/current/packages/fx/src/choice.ts": "export type ToolChoice = \"auto\" | \"none\";\nexport type When = Date;\n"}
	run := func(key, typ string, owners ...string) *decision {
		rn := renameTable{}
		var rows []m
		for _, o := range owners {
			rn["pkg:fx/.#"+o] = "lib/lib.go#Config"
			rows = append(rows, m{"id": "pkg:fx/.#" + o, "name": o, "kind": "interface", "shape": m{"type": o}})
		}
		prop := "pkg:fx/.#" + owners[0] + "::property:" + key
		rows = append(rows, m{"id": prop, "parentId": "pkg:fx/.#" + owners[0], "role": "property", "name": owners[0] + "." + key, "kind": "property",
			"shape": m{"name": key, "type": typ, "optional": true}})
		return fixtureRun(t, files, func(inv []m) []m { return append(inv, rows...) }, nil, rn)[prop]
	}
	const literals = `"auto" | "none" | "any" | undefined`
	if d := run("choice", literals, "GoogleOpts", "CodexOpts"); d == nil || d.Reason == reasonType {
		t.Errorf("a shared options struct's any field carries a literal union, got %+v", d)
	}
	if d := run("choice", literals, "GoogleOpts"); d == nil || d.Reason != reasonType {
		t.Errorf("an options struct of one upstream type keeps T1, got %+v", d)
	}
	if d := run("choice", "Date | undefined", "GoogleOpts", "CodexOpts"); d == nil || d.Reason != reasonType {
		t.Errorf("a shared field does not make a non-literal type agree with any, got %+v", d)
	}
	// A member naming an alias reads its body (ai types.ts:88 `type ToolChoice = "auto" | "none"`, SimpleStreamOptions.toolChoice).
	if d := run("choice", "ToolChoice | undefined", "GoogleOpts", "CodexOpts"); d == nil || d.Reason == reasonType {
		t.Errorf("an alias of a literal union is a literal union, got %+v", d)
	}
	if d := run("choice", "ToolChoice | undefined", "GoogleOpts"); d == nil || d.Reason != reasonType {
		t.Errorf("an alias of a literal union on an options struct of one upstream type keeps U1, got %+v", d)
	}
	if d := run("choice", "When | undefined", "GoogleOpts", "CodexOpts"); d == nil || d.Reason != reasonType {
		t.Errorf("an alias of a non-literal type is no literal union, got %+v", d)
	}
	if d := run("picked", literals, "GoogleOpts", "CodexOpts"); d == nil || d.Reason != reasonType {
		t.Errorf("only an empty-interface field carries every provider's type, got %+v", d)
	}
}

// TestPropertyIsItsSetter (M2s): a writable upstream data property that Go reaches through the method Set<Name> agrees when the setter's
// parameter has the property's type; a different type or a readonly property is not carried by the setter.
func TestPropertyIsItsSetter(t *testing.T) {
	cfg := "pkg:fx/.#Config"
	prop := cfg + "::property:focused"
	files := map[string]string{"lib/lib.go": strings.Replace(fxLib, "// Add is the Go form", "// SetFocused assigns focused.\nfunc (c *Config) SetFocused(focused bool) {}\n\n// Add is the Go form", 1)}
	rn := renameTable{prop: "lib/lib.go#Config.SetFocused"}
	run := func(typ string, readonly bool) *decision {
		return fixtureRun(t, files, func(inv []m) []m {
			return append(inv, m{"id": prop, "parentId": cfg, "role": "property", "name": "Config.focused", "kind": "property",
				"shape": m{"name": "focused", "type": typ, "readonly": readonly}})
		}, nil, rn)[prop]
	}
	if d := run("boolean", false); d == nil || d.Reason == reasonType || d.Reason == reasonSignature || d.Reason == reasonMember {
		t.Fatalf("a boolean property is carried by SetFocused(bool), got %+v", d)
	}
	if d := run("string", false); d == nil || d.Reason != reasonType {
		t.Fatalf("a string property is not carried by SetFocused(bool), got %+v", d)
	}
	if d := run("boolean", true); d == nil || d.Reason != reasonType {
		t.Fatalf("a readonly property is not assigned through a setter, got %+v", d)
	}
	// A property that is itself named Set<Name> is the method, never its own setter.
	self := cfg + "::property:setFocused"
	d := fixtureRun(t, files, func(inv []m) []m {
		return append(inv, m{"id": self, "parentId": cfg, "role": "property", "name": "Config.setFocused", "kind": "property",
			"shape": m{"name": "setFocused", "type": "boolean"}})
	}, nil, renameTable{self: "lib/lib.go#Config.SetFocused"})[self]
	if d == nil || d.Reason != reasonType {
		t.Fatalf("a data property named setFocused is not carried by SetFocused(bool), got %+v", d)
	}
}

// TestEventEmitterMembersAreDesignedOut (L1e): a member the inventory records in Node's events.d.ts is inherited EventEmitter machinery the
// Go type does not carry and is designed out, including its call rows; a member the class declares itself in its own file is judged normally.
func TestEventEmitterMembersAreDesignedOut(t *testing.T) {
	files := map[string]string{"lib/emit.go": "package lib\n\ntype Buf struct{}\n"}
	buf := "pkg:fx/.#Buf"
	tweak := func(inv []m) []m {
		prop := func(name, path string) m {
			return m{"id": buf + "::property:" + name, "parentId": buf, "role": "property", "name": "Buf." + name, "kind": "property",
				"shape": m{"name": name, "type": "() => void"}, "source": m{"path": path}}
		}
		return append(inv, m{"id": buf, "name": "Buf", "kind": "class", "shape": m{"type": "Buf"}},
			prop("on", "node_modules/@types/node/events.d.ts"), prop("process", "packages/fx/src/buf.ts"))
	}
	ds := fixtureRun(t, files, tweak, nil, nil)
	if d := ds[buf+"::property:on"]; d == nil || d.Gap || d.DesignedOut == "" {
		t.Fatalf("an inherited EventEmitter member is designed out, got %+v", d)
	}
	if d := ds[buf+"::property:process"]; d == nil || !d.Gap || d.Reason != reasonMember {
		t.Fatalf("a member the class declares stays member-missing, got %+v", d)
	}
}

// TestFunctionAliasIsAGoFuncOrOneMethodInterface (A0 with S11): an upstream alias of a function type is a Go func type or an interface
// with the one method of that signature (mcp McpFetch is satisfied by *http.Client.Do); an interface with more methods, or a struct,
// stays an A0 gap.
func TestFunctionAliasIsAGoFuncOrOneMethodInterface(t *testing.T) {
	files := map[string]string{"lib/cb.go": "package lib\n\ntype Cb func(a string) error\n\ntype CbDo interface{ Do(a string) error }\n\ntype CbTwo interface {\n\tDo(a string) error\n\tClose()\n}\n\ntype CbStruct struct{}\n\ntype CbListener struct{ call func(a string) error }\n\nfunc NewCbListener(call func(a string) error) *CbListener { return &CbListener{call: call} }\n\ntype CbBare struct{ call func(a string) error }\n"}
	call := m{"parameters": []m{prm("a", "string", false)}, "returns": "void"}
	row := func(name string) []m {
		id := "pkg:fx/.#" + name
		return []m{{"id": id, "name": name, "kind": "type-alias", "shape": m{"type": name, "aliasTarget": name, "calls": []m{call}}},
			{"id": id + "::call:0", "parentId": id, "role": "call-overload", "name": name + " call 0", "kind": "call-overload", "shape": call}}
	}
	rn := renameTable{"pkg:fx/.#Cb": "lib/cb.go#Cb", "pkg:fx/.#CbDo": "lib/cb.go#CbDo", "pkg:fx/.#CbTwo": "lib/cb.go#CbTwo", "pkg:fx/.#CbListener": "lib/cb.go#CbListener", "pkg:fx/.#CbBare": "lib/cb.go#CbBare", "pkg:fx/.#CbStruct": "lib/cb.go#CbStruct"}
	var inv []m
	for _, n := range []string{"Cb", "CbDo", "CbTwo", "CbStruct", "CbListener", "CbBare"} {
		inv = append(inv, row(n)...)
	}
	ds := fixtureRun(t, files, func([]m) []m { return inv }, nil, rn)
	for _, n := range []string{"Cb", "CbDo", "CbListener"} {
		if d := ds["pkg:fx/.#"+n]; d == nil || (d.Gap && d.Reason == reasonType) {
			t.Errorf("%s: a func type or one-method interface carries the function alias, got %+v", n, d)
		}
	}
	for _, n := range []string{"CbTwo", "CbStruct", "CbBare"} {
		if d := ds["pkg:fx/.#"+n]; d == nil || !d.Gap || d.Reason != reasonType {
			t.Errorf("%s: must stay an A0 gap, got %+v", n, d)
		}
	}
}

// TestParityHarnessProbeIsNotProductionUse: a member whose only reachable user is a parity-harness probe (it runs only under
// PIG_PARITY_HARNESS=1) has no production use, so P1 keeps it a gap even when the caller's reach set lists the probe.
func TestParityHarnessProbeIsNotProductionUse(t *testing.T) {
	probe := map[string]bool{"lib/parity_harness.go#Members": true}
	ds := fixtureRun(t, map[string]string{"lib/use.go": "package lib\n", "lib/parity_harness.go": fxUse}, nil, probe, nil)
	for _, id := range []string{"pkg:fx/.#Config::property:name", "pkg:fx/.#Widget::property:name"} {
		if d := ds[id]; !d.Gap || d.Reason != reasonExercise || !strings.HasPrefix(d.Detail, "P1:") {
			t.Errorf("%s: a member only a parity-harness probe uses must be a P1 gap, got %+v", id, d)
		}
	}
	if g := gapsOf(fixtureRun(t, map[string]string{"lib/use.go": "package lib\n", "lib/production.go": fxUse}, nil, map[string]bool{"lib/production.go#Members": true}, nil)); len(g) != 0 {
		t.Errorf("the same use from a production file closes them: %v", g)
	}
}

// TestDesignedOutRowOutsideTheDecisionsFileIsNotADecision: only reviewed-decisions.json makes a designed-out row a decision, so a row the
// rules once derived (or a hand row that never reached the file) is judged by the rules again instead of standing as one.
func TestDesignedOutRowOutsideTheDecisionsFileIsNotADecision(t *testing.T) {
	files := map[string]string{
		"lib/lib.go":      strings.NewReplacer("func Add(", "func Sum(", "return Add(1, 2)", "return Sum(1, 2)").Replace(fxLib),
		"lib/lib_test.go": strings.ReplaceAll(fxTest, "Add(1, 2)", "Sum(1, 2)"),
	}
	for _, reviewed := range []bool{true, false} {
		ds := fixtureRun(t, files, func(inv []m) []m {
			e := maps.Clone(inv[0])
			e["_disposition"] = "designed-out"
			e["_unreviewed"] = !reviewed
			return append([]m{e}, inv[1:]...)
		}, nil, nil)
		d := ds["pkg:fx/.#add"]
		if d == nil || d.Gap == reviewed {
			t.Errorf("reviewed=%v: want a decision (not a gap) only when the file holds the row, got %+v", reviewed, d)
		}
	}
}

// TestSignalOperandOfAnIntersection: A7s reads `{ signal?: AbortSignal } & U` (Pi ai/src/auth/types.ts:125 AuthPrompt) as the Go type
// for U when every Go consumer takes a context.Context, as T12s does for a member. A consumer without a context, no consumer, two
// signal operands, an operand that holds more than a signal and a U that disagrees with the Go type are not accepted.
func TestSignalOperandOfAnIntersection(t *testing.T) {
	build := func(extra string) map[string]string {
		lib := fxLib + `
// Mode is the Go form of the upstream Mode union.
type Mode string

// The modes.
const (
	ModeA Mode = "a"
	ModeB Mode = "b"
)
` + extra
		test := fxTest + `
func TestMode(t *testing.T) {
	if ModeA == ModeB {
		t.Fatal("mode")
	}
}
`
		return map[string]string{"lib/lib.go": lib, "lib/lib_test.go": test, ".upstream/current/packages/fx/src/mode.ts": `export type Mode = "a" | "b";
export type Signalled = { signal?: AbortSignal } & ("a" | "b");
export type TwoSignals = { signal?: AbortSignal } & { stop?: AbortSignal } & ("a" | "b");
export type Mixed = { signal?: AbortSignal; id: string } & ("a" | "b");
export type Other = { signal?: AbortSignal } & number;
`}
	}
	names := []string{"Signalled", "TwoSignals", "Mixed", "Other"}
	row := func([]m) []m {
		var rows []m
		for _, n := range names {
			rows = append(rows, m{"id": "pkg:fx/.#" + n, "name": n, "kind": "type-alias", "shape": m{"type": n, "aliasTarget": n}})
		}
		return rows
	}
	ren := renameTable{}
	for _, n := range names {
		ren["pkg:fx/.#"+n] = "lib/lib.go#Mode"
	}
	accepted := func(d *decision) bool { return d != nil && d.Reason != reasonUndecided && d.Reason != reasonType }
	withCtx := "\n// Start starts a mode.\nfunc Start(ctx context.Context, m Mode) error { return ctx.Err() }\n"
	got := fixtureRun(t, build(withCtx), row, nil, ren)
	if d := got["pkg:fx/.#Signalled"]; !accepted(d) {
		t.Fatalf("every consumer takes a context, got %+v: %s", d, d.Detail)
	}
	for _, n := range names[1:] {
		if d := got["pkg:fx/.#"+n]; accepted(d) {
			t.Fatalf("%s is not a signal operand next to the Go type, got %+v: %s", n, d, d.Detail)
		}
	}
	for _, extra := range []string{withCtx + "\n// Name names a mode.\nfunc Name(m Mode) string { return string(m) }\n", ""} {
		if d := fixtureRun(t, build(extra), row, nil, ren)["pkg:fx/.#Signalled"]; accepted(d) {
			t.Fatalf("nothing carries the signal for every consumer, got %+v: %s", d, d.Detail)
		}
	}
}

// TestUnionMemberInterfaceIsOfTheAliasPackage (U7): the members of a discriminated union alias named by their interface are read from
// the alias's own package, not from the first package that declares an interface of that name (mcp ContentBlock = TextContent | ..., ai also
// declares a TextContent with a textSignature the mcp block does not carry). The Go tagged struct has no Sig field, so reading the other
// package's TextC reports a missing property.
func TestUnionMemberInterfaceIsOfTheAliasPackage(t *testing.T) {
	lib := fxLib + `
type BlkType string

const (
	BlkText  BlkType = "text"
	BlkImage BlkType = "image"
)

// Blk is the Go form of the upstream Blk union.
type Blk struct {
	Type BlkType
	Text string
}
`
	files := map[string]string{
		"lib/lib.go":      lib,
		"lib/lib_test.go": fxTest,
		".upstream/current/packages/fx/src/blk.ts": "export type Blk = TextC | ImgC;\n",
	}
	iface := func(pkg, name string, props ...m) []m {
		id := "pkg:" + pkg + "/.#" + name
		out := []m{{"id": id, "name": name, "kind": "interface", "shape": m{"name": name}}}
		for _, p := range props {
			out = append(out, m{"id": id + "::property:" + p["name"].(string), "parentId": id, "role": "property", "name": name + "." + p["name"].(string),
				"kind": "property", "shape": p})
		}
		return out
	}
	tag := func(v string) m { return m{"name": "type", "type": `"` + v + `"`} }
	row := func(inv []m) []m {
		// The foreign TextC comes first and carries a property the Go struct lacks.
		inv = append(inv, iface("other", "TextC", tag("text"), m{"name": "text", "type": "string"}, m{"name": "sig", "type": "string", "optional": true})...)
		inv = append(inv, iface("fx", "TextC", tag("text"), m{"name": "text", "type": "string"})...)
		inv = append(inv, iface("fx", "ImgC", tag("image"))...)
		return append(inv, m{"id": "pkg:fx/.#Blk", "name": "Blk", "kind": "type-alias", "shape": m{"type": "Blk", "aliasTarget": "Blk"}})
	}
	d := fixtureRun(t, files, row, nil, renameTable{"pkg:fx/.#Blk": "lib/lib.go#Blk"})["pkg:fx/.#Blk"]
	if d == nil || d.Reason == reasonType {
		t.Fatalf("a union member is the interface of the alias's package, got %+v", d)
	}
}

// A constructor row takes a reviewed type exception as a call row does (evalConstruct): Pi's constructor parameter type that the checker cannot
// decide against the Go constructor stays undecided without the exception and closes with it; an exception for another Go target does not apply.
func TestReviewedTypeClosesAConstructorRow(t *testing.T) {
	row := func(inv []m) []m {
		for _, r := range inv {
			if r["id"] == "pkg:fx/.#Widget::construct:0" {
				r["shape"] = m{"parameters": []m{prm("name", "WidgetHost", false)}, "returns": "Widget"}
			}
		}
		return inv
	}
	files := func(reviewed string) map[string]string {
		f := map[string]string{"lib/lib.go": fxLib, "lib/lib_test.go": fxTest}
		if reviewed != "" {
			f[reviewedTypesFile] = reviewed
		}
		return f
	}
	const id = "pkg:fx/.#Widget::construct:0"
	without := fixtureRun(t, files(""), row, nil, nil)
	if d := without[id]; d.Reason != reasonUndecided {
		t.Fatalf("without an exception: got %+v: %s", d, d.Detail)
	}
	with := fixtureRun(t, files(`{"`+id+`": {"go": "lib/lib.go#NewWidget", "reason": "r"}}`), row, nil, nil)
	if d := with[id]; d.Reason == reasonUndecided || d.Reason == reasonType || d.Reason == reasonSignature {
		t.Fatalf("with an exception: got %+v: %s", d, d.Detail)
	}
	elsewhere := fixtureRun(t, files(`{"`+id+`": {"go": "lib/lib.go#Other", "reason": "r"}}`), row, nil, nil)
	if d := elsewhere[id]; d.Reason != reasonUndecided {
		t.Fatalf("an exception for another Go target applied: got %+v", d)
	}
}
