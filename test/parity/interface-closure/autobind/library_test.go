package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The P1(b) fixture: nothing is reachable from cmd/pig, so every member of Config and Widget can close only through the library
// route. The upstream package fx publishes an index that re-exports types.ts, and PORT_MAP records lib/lib.go as its port.

const fxLibTypes = "export type Mode = \"a\" | \"b\";\n" +
	"export interface Config {\n\tname: string;\n\tretries: number;\n}\n" +
	"export class Widget {\n\tconstructor(readonly name: string) {}\n}\n"

const fxLibIndex = "export * from \"./types.ts\";\n"

const fxLibPackage = `{"name": "fx", "main": "./dist/index.js", "exports": {".": {"types": "./dist/index.d.ts", "import": "./dist/index.js"}}}`

const fxLibPortMap = "| upstream | pig | status |\n|---|---|---|\n| `packages/fx/src/types.ts` | `lib/lib.go` | ✅ |\n"

// fxCitedTest asserts Config.name, Config.retries and Widget.name and cites the upstream declaration that names them.
const fxCitedTest = `
// TestLibraryMembers follows packages/fx/src/types.ts:2 (Config) and types.ts:6 (Widget).
func TestLibraryMembers(t *testing.T) {
	c := Config{Name: "x", Retries: 2}
	if c.Name != "x" || c.Retries != 2 {
		t.Error("config")
	}
	if NewWidget("w").Name() != "w" {
		t.Error("widget")
	}
}
`

var libraryMembers = []string{"pkg:fx/.#Config::property:name", "pkg:fx/.#Config::property:retries", "pkg:fx/.#Widget::property:name"}

// libraryFiles returns the P1(b) fixture with the edits applied (file -> old -> new, old must be present).
func libraryFiles(t *testing.T, edits ...[3]string) map[string]string {
	t.Helper()
	files := map[string]string{
		"lib/lib.go":      fxLib,
		"lib/lib_test.go": fxTest + fxCitedTest,
		"lib/use.go":      fxUse,
		".upstream/current/packages/fx/src/types.ts": fxLibTypes,
		".upstream/current/packages/fx/src/index.ts": fxLibIndex,
		".upstream/current/packages/fx/package.json": fxLibPackage,
		"docs/parity/PORT_MAP.md":                    fxLibPortMap,
	}
	for _, e := range edits {
		if e[1] == "" {
			files[e[0]] += e[2]
			continue
		}
		if !strings.Contains(files[e[0]], e[1]) {
			t.Fatalf("edit text %q missing from %s", e[1], e[0])
		}
		files[e[0]] = strings.Replace(files[e[0]], e[1], e[2], 1)
	}
	return files
}

func libraryRun(t *testing.T, reach map[string]bool, edits ...[3]string) map[string]*decision {
	t.Helper()
	if reach == nil {
		reach = map[string]bool{}
	}
	return fixtureRun(t, libraryFiles(t, edits...), nil, reach, nil)
}

// wantLibraryGap asserts that id stays the P1 gap and that rule P1(b) names the condition.
func wantLibraryGap(t *testing.T, ds map[string]*decision, id, cond string) {
	t.Helper()
	d := ds[id]
	if d == nil || !d.Gap || d.Reason != reasonExercise || !strings.Contains(d.Detail, "P1(b) "+cond+":") {
		t.Errorf("%s: want the P1 gap with P1(b) %s, got %+v", id, cond, d)
	}
}

// TestLibraryMemberClosesWithACitedTest: a library member that no production path reaches closes when an asserting test cites the
// upstream file:line, and its evidence is that test.
func TestLibraryMemberClosesWithACitedTest(t *testing.T) {
	ds := libraryRun(t, nil)
	for _, id := range libraryMembers {
		d := ds[id]
		if d == nil || d.Gap {
			t.Fatalf("%s: a cited library member must close, got %+v", id, d)
		}
		if d.Evidence != "test:lib/lib_test.go#TestLibraryMembers" || d.Info.Call != "" {
			t.Errorf("%s: evidence must be the citing test and no production call, got %q call %q", id, d.Evidence, d.Info.Call)
		}
	}
	if d := ds["pkg:fx/.#Config::property:timeoutMs"]; !d.Gap {
		t.Errorf("a member whose asserting test cites nothing stays a gap, got %+v", d)
	}
}

// TestLibraryMemberNeedsACitationOrTheOracle: L6. An uncited test, a line past the end of the cited file, and a cited file that does
// not name the member leave the gap; a test named for the Pi oracle closes without a citation.
func TestLibraryMemberNeedsACitationOrTheOracle(t *testing.T) {
	cite := "// TestLibraryMembers follows packages/fx/src/types.ts:2 (Config) and types.ts:6 (Widget).\n"
	for name, repl := range map[string]string{
		"uncited":       "// TestLibraryMembers checks the members.\n",
		"line past end": "// TestLibraryMembers follows packages/fx/src/types.ts:99.\n",
		"other file":    "// TestLibraryMembers follows packages/fx/src/index.ts:1.\n",
		"other package": "// TestLibraryMembers follows packages/other/src/types.ts:2.\n",
	} {
		t.Run(name, func(t *testing.T) {
			ds := libraryRun(t, nil, [3]string{"lib/lib_test.go", cite, repl})
			for _, id := range libraryMembers {
				wantLibraryGap(t, ds, id, "L6")
			}
		})
	}
	ds := libraryRun(t, nil, [3]string{"lib/lib_test.go", cite + "func TestLibraryMembers(", "// TestLibraryMembersPiOracle compares with the recorded Pi run.\nfunc TestLibraryMembersPiOracle("})
	for _, id := range libraryMembers {
		if d := ds[id]; d.Gap {
			t.Errorf("%s: an oracle test closes the member, got %+v", id, d)
		}
	}
}

// TestLibraryRouteNeedsALibraryPackage: L1. Without a published index and for a Go file PORT_MAP does not record, the members stay
// gaps; a package that also ships the pi command is a library for the declarations its index publishes.
func TestLibraryRouteNeedsALibraryPackage(t *testing.T) {
	cases := map[string][3]string{
		"no index":       {".upstream/current/packages/fx/package.json", fxLibPackage, `{"name": "fx"}`},
		"not in the map": {"docs/parity/PORT_MAP.md", "`lib/lib.go`", "`elsewhere/other.go`"},
		"not exported":   {".upstream/current/packages/fx/src/index.ts", fxLibIndex, "export const x = 1;\n"},
	}
	for name, edit := range cases {
		ds := libraryRun(t, nil, edit)
		for _, id := range libraryMembers {
			d := ds[id]
			if d == nil || !d.Gap || !strings.Contains(d.Detail, "P1(b) L1:") {
				t.Errorf("%s: %s must stay the P1 gap with L1, got %+v", name, id, d)
			}
		}
	}
}

// TestLibraryOwnerAliasedIntoThePort: L1 holds for a member whose owner type is declared outside the port when the Go directory that
// ports the published file declares an exported alias of it (routing's `type RoutedServerPresentation =
// services.RoutedServerPresentation`, server types.ts:33). A defined type of the same underlying type, or an unexported alias, is no
// such re-export, and an owner the port does not alias stays L1.
func TestLibraryOwnerAliasedIntoThePort(t *testing.T) {
	moved := [3]string{"docs/parity/PORT_MAP.md", "`lib/lib.go`", "`port/port.go`"}
	port := func(decl string) [3]string {
		return [3]string{"port/port.go", "", "package port\n\nimport \"fixture/lib\"\n\n" + decl + "\n"}
	}
	const widget = "pkg:fx/.#Widget::property:name"
	ds := libraryRun(t, nil, moved, port("// Widget is the published widget.\ntype Widget = lib.Widget"))
	if d := ds[widget]; d == nil || d.Gap {
		t.Errorf("a member of an owner the port aliases passes L1, got %+v", d)
	}
	wantLibraryGap(t, ds, "pkg:fx/.#Config::property:name", "L1")
	wantLibraryGap(t, libraryRun(t, nil, moved, port("// Widget is a new type.\ntype Widget lib.Widget")), widget, "L1")
	wantLibraryGap(t, libraryRun(t, nil, moved, port("type widget = lib.Widget\n\nvar _ widget")), widget, "L1")
}

// TestLibraryDuplicateMemberStaysAGap: L3, mutation side one. A second exported field that folds to the same name, a method that only
// forwards to another exported member, and a same-named type elsewhere whose member is wired or exported keep the gap.
func TestLibraryDuplicateMemberStaysAGap(t *testing.T) {
	ds := libraryRun(t, nil, [3]string{"lib/lib.go", "\tRetries int    `json:\"retries\"`\n", "\tRetries int    `json:\"retries\"`\n\tRETRIES int\n"})
	wantLibraryGap(t, ds, "pkg:fx/.#Config::property:retries", "L3")
	if d := ds["pkg:fx/.#Config::property:name"]; d.Gap {
		t.Errorf("the duplicate leaves the other members alone, got %+v", d)
	}

	ds = libraryRun(t, nil, [3]string{"lib/lib.go", "func (w *Widget) Name() string { return w.n }",
		"func (w *Widget) Name() string { return w.Label() }\n\n// Label is the widget label.\nfunc (w *Widget) Label() string { return w.n }"})
	wantLibraryGap(t, ds, "pkg:fx/.#Widget::property:name", "L3")

	ds = libraryRun(t, map[string]bool{"lib/lib.go#Name": true}, [3]string{"lib/lib.go", "", "\n// Name repeats the method.\nfunc Name() string { return \"\" }\n"})
	wantLibraryGap(t, ds, "pkg:fx/.#Widget::property:name", "L3")

	other := "package other\n\n// Widget is a second widget.\ntype Widget struct{ n string }\n\n// Name reads it.\nfunc (w *Widget) Name() string { return w.n }\n\n" +
		"// Use is wired.\nfunc Use() string { return (&Widget{n: \"x\"}).Name() }\n"
	ds = libraryRun(t, map[string]bool{"other/other.go#Use": true}, [3]string{"other/other.go", "", other})
	wantLibraryGap(t, ds, "pkg:fx/.#Widget::property:name", "L3")
	if !strings.Contains(ds["pkg:fx/.#Widget::property:name"].Detail, "duplicates the wired other/other.go#Widget.Name") {
		t.Errorf("the reason names the wired duplicate, got %q", ds["pkg:fx/.#Widget::property:name"].Detail)
	}
	ds = libraryRun(t, nil, [3]string{"other/other.go", "", other})
	wantLibraryGap(t, ds, "pkg:fx/.#Widget::property:name", "L3")

	// A same-named type that PORT_MAP records for another upstream package is that package's own port, not a duplicate.
	ds = libraryRun(t, map[string]bool{"other/other.go#Use": true}, [3]string{"other/other.go", "", other},
		[3]string{"docs/parity/PORT_MAP.md", "", "| `packages/zz/src/widget.ts` | `other/other.go` | ✅ |\n"})
	if d := ds["pkg:fx/.#Widget::property:name"]; d.Gap {
		t.Errorf("another package's port is not a duplicate, got %+v", d)
	}
}

// TestLibraryConstantStubStaysAGap: L4, mutation side two. A test-only method that returns a constant keeps the gap even with a cited,
// asserting test; a constant is faithful only for an upstream literal type.
func TestLibraryConstantStubStaysAGap(t *testing.T) {
	stub := [3]string{"lib/lib.go", "func (w *Widget) Name() string { return w.n }", "func (w *Widget) Name() string { return \"w\" }"}
	ds := libraryRun(t, nil, stub)
	wantLibraryGap(t, ds, "pkg:fx/.#Widget::property:name", "L4")

	literal := func(inv []m) []m {
		for _, e := range inv {
			if e["id"] == "pkg:fx/.#Widget::property:name" {
				e["shape"] = m{"name": "name", "type": `"w"`}
			}
		}
		return inv
	}
	if d := fixtureRun(t, libraryFiles(t, stub), literal, map[string]bool{}, nil)["pkg:fx/.#Widget::property:name"]; d.Gap {
		t.Errorf("a constant method for an upstream literal type is faithful, got %+v", d)
	}
}

// TestLibraryFieldNeedsAUseInItsPackage: L5. A field that only tests read or set stays a gap.
func TestLibraryFieldNeedsAUseInItsPackage(t *testing.T) {
	ds := libraryRun(t, nil,
		[3]string{"lib/use.go", "c := &Config{Name: \"a\", Retries: 1, Timeout: 2,", "c := &Config{Name: \"a\", Timeout: 2,"},
		[3]string{"lib/use.go", "return c.Retries + c.Timeout", "return c.Timeout"})
	wantLibraryGap(t, ds, "pkg:fx/.#Config::property:retries", "L5")
	if d := ds["pkg:fx/.#Config::property:name"]; d.Gap {
		t.Errorf("a field its package reads still closes, got %+v", d)
	}
}

// TestProductionReachStillClosesFirst: rule P1(a) is unchanged; a reachable use closes the member whatever the library conditions say.
func TestProductionReachStillClosesFirst(t *testing.T) {
	ds := fixtureRun(t, libraryFiles(t, [3]string{".upstream/current/packages/fx/package.json", fxLibPackage, `{"name": "fx"}`}), nil, nil, nil)
	for _, id := range libraryMembers {
		if d := ds[id]; d.Gap || d.Info == nil || d.Info.Call == "" {
			t.Errorf("%s: production reach closes the member with its call, got %+v", id, d)
		}
	}
}

// TestLibraryMemberClosesOnANamedUpstreamCase: L6 also accepts a test whose file declares the upstream test file it ports and whose
// t.Run subtest carries the exact title of one of that file's cases, when that file names the member. A title the file does not
// have, a ported file that never names the member, and a file with no Ports declaration leave the gap.
func TestLibraryMemberClosesOnANamedUpstreamCase(t *testing.T) {
	const id = "pkg:fx/.#Widget::property:name"
	uncite := [3]string{"lib/lib_test.go", "// TestLibraryMembers follows packages/fx/src/types.ts:2 (Config) and types.ts:6 (Widget).\n", "// TestLibraryMembers checks the members.\n"}
	ports := [3]string{"lib/lib_test.go", "package lib\n", "// Ports packages/fx/test/widget.test.ts\npackage lib\n"}
	named := func(title string) [3]string {
		return [3]string{"lib/lib_test.go", "", "\nfunc TestWidgetCases(t *testing.T) {\n\tt.Run(\"" + title + "\", func(t *testing.T) {\n\t\tif NewWidget(\"w\").Name() != \"w\" {\n\t\t\tt.Fatal(\"name\")\n\t\t}\n\t})\n}\n"}
	}
	piTest := func(body string) [3]string {
		return [3]string{".upstream/current/packages/fx/test/widget.test.ts", "", body}
	}
	const upstream = "describe(\"Pi Widget\", () => {\n\tit(\"reads it\", () => {\n\t\texpect(new Widget(\"w\").name).toBe(\"w\");\n\t});\n});\n"
	if d := libraryRun(t, nil, uncite, ports, named("reads it"), piTest(upstream))[id]; d == nil || d.Gap || d.Evidence != "test:lib/lib_test.go#TestWidgetCases" {
		t.Errorf("a subtest titled as the ported upstream case closes the member, got %+v", d)
	}
	wantLibraryGap(t, libraryRun(t, nil, uncite, ports, named("reads one"), piTest(upstream)), id, "L6")
	wantLibraryGap(t, libraryRun(t, nil, uncite, ports, named("reads it"), piTest(strings.ReplaceAll(upstream, ".name", ".label"))), id, "L6")
	wantLibraryGap(t, libraryRun(t, nil, uncite, named("reads it"), piTest(upstream)), id, "L6")

	// A test function named Test + describe title + case title (letters and digits) names the case too.
	fn := func(name string) [3]string {
		return [3]string{"lib/lib_test.go", "", "\nfunc " + name + "(t *testing.T) {\n\tif NewWidget(\"w\").Name() != \"w\" {\n\t\tt.Fatal(\"name\")\n\t}\n}\n"}
	}
	if d := libraryRun(t, nil, uncite, ports, fn("TestWidgetReadsIt"), piTest(upstream))[id]; d == nil || d.Gap || d.Evidence != "test:lib/lib_test.go#TestWidgetReadsIt" {
		t.Errorf("a test function named after the ported upstream case closes the member, got %+v", d)
	}
	wantLibraryGap(t, libraryRun(t, nil, uncite, ports, fn("TestWidgetReadsOther"), piTest(upstream)), id, "L6")
}

// L6 does not count a test that skips by an environment variable: it does not run in a default go test.
func TestLibraryEvidenceSkipsEnvironmentGatedTests(t *testing.T) {
	root := t.TempDir()
	src := `package x

import (
	"os"
	"runtime"
	"testing"
)

func TestGated(t *testing.T) {
	if os.Getenv("PI_ENV_SSH_HOST") == "" {
		t.Skip("no server")
	}
}

func TestLookup(t *testing.T) {
	if _, ok := os.LookupEnv("X"); !ok {
		t.SkipNow()
	}
}

func TestPlatform(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no FIFOs")
	}
	if os.Getenv("HOME") == "" {
		t.Log("no home")
	}
}
`
	if err := os.MkdirAll(filepath.Join(root, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "x", "x_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	lr := &library{root: root, fset: token.NewFileSet(), tests: map[string]*ast.File{}}
	for key, want := range map[string]bool{"x/x_test.go#TestGated": true, "x/x_test.go#TestLookup": true, "x/x_test.go#TestPlatform": false, "x/x_test.go#TestMissing": false} {
		if got := lr.envGated(key); got != want {
			t.Errorf("envGated(%s) = %v, want %v", key, got, want)
		}
	}
}

// TestLibraryGatesOfL1L2AndTheEnvironment: L1 a package that is not a published library, L2 an unexported Go member, and L6 an
// asserting test that skips unless an environment variable is set each leave the member a gap.
func TestLibraryGatesOfL1L2AndTheEnvironment(t *testing.T) {
	gated := libraryRun(t, nil,
		[3]string{"lib/lib_test.go", "import (\n\t\"context\"\n", "import (\n\t\"context\"\n\t\"os\"\n"},
		[3]string{"lib/lib_test.go", "func TestLibraryMembers(t *testing.T) {\n", "func TestLibraryMembers(t *testing.T) {\n\tif os.Getenv(\"FX_HOST\") == \"\" {\n\t\tt.Skip(\"no host\")\n\t}\n"})
	for _, id := range libraryMembers {
		wantLibraryGap(t, gated, id, "L6")
	}
	shipsPi := libraryRun(t, nil, [3]string{".upstream/current/packages/fx/package.json", `"main": "./dist/index.js", `, `"bin": {"pi": "./dist/cli.js"}, "main": "./dist/index.js", `})
	for _, id := range libraryMembers {
		if d := shipsPi[id]; d == nil || d.Gap {
			t.Errorf("%s: the package that ships the pi command is a library for its published declarations, got %+v", id, d)
		}
	}
	unexported := libraryRun(t, nil,
		[3]string{"lib/lib.go", "func (w *Widget) Name() string { return w.n }", "func (w *Widget) name() string { return w.n }"},
		[3]string{"lib/lib_test.go", `NewWidget("w").Name()`, `NewWidget("w").name()`})
	wantLibraryGap(t, unexported, "pkg:fx/.#Widget::property:name", "L2")
}

// TestLibraryRecordedKillNeedsAFileCitation: L6 also accepts a test that `autobind -mutate` recorded as killing a zero-body mutant of
// the member when the test's file cites the upstream file:line; the kill alone, or the file citation alone, leaves the gap.
func TestLibraryRecordedKillNeedsAFileCitation(t *testing.T) {
	uncite := [3]string{"lib/lib_test.go", "// TestLibraryMembers follows packages/fx/src/types.ts:2 (Config) and types.ts:6 (Widget).\n", "// TestLibraryMembers checks the members.\n"}
	fileCite := [3]string{"lib/lib_test.go", "package lib\n", "// Package lib tests follow packages/fx/src/types.ts:6 (Widget).\npackage lib\n"}
	kill := [3]string{mutationCheckedFile, "", `{"lib/lib.go#Widget.Name": ["test:lib/lib_test.go#TestWidget"]}`}
	const id = "pkg:fx/.#Widget::property:name"
	if d := libraryRun(t, nil, uncite, fileCite, kill)[id]; d.Gap || d.Evidence != "test:lib/lib_test.go#TestWidget" {
		t.Errorf("a recorded kill by a test whose file cites the Pi source closes the member, got %+v", d)
	}
	wantLibraryGap(t, libraryRun(t, nil, uncite, kill), id, "L6")
	wantLibraryGap(t, libraryRun(t, nil, uncite, fileCite), id, "L6")
}

// TestLibraryMemberClosesInAPortedPiSuite: L6, suite. A published upstream suite (such as durable's `./testing` conformance cases)
// that names the member, ported to non-test Go code that uses it, closes the member when a test runs the suite with its *testing.T
// within three calls. A test that hands the suite another reporter, a suite file PORT_MAP does not record, a suite the index does
// not publish, a Pi suite that does not name the member, and a constant stub or a duplicate of the member leave the gap.
func TestLibraryMemberClosesInAPortedPiSuite(t *testing.T) {
	const id = "pkg:fx/.#Widget::property:name"
	uncite := [3]string{"lib/lib_test.go", "// TestLibraryMembers follows packages/fx/src/types.ts:2 (Config) and types.ts:6 (Widget).\n", "// TestLibraryMembers checks the members.\n"}
	suiteGo := [3]string{"lib/suite.go", "", "package lib\n\n// Reporter receives suite failures.\ntype Reporter interface{ Error(args ...any) }\n\n" +
		"// RunWidgetSuite runs the ported Pi widget suite.\nfunc RunWidgetSuite(r Reporter) { widgetCase(r) }\n\n" +
		"func widgetCase(r Reporter) {\n\tif NewWidget(\"w\").Name() != \"w\" {\n\t\tr.Error(\"name\")\n\t}\n}\n"}
	piSuite := func(body string) [3]string { return [3]string{".upstream/current/packages/fx/src/suite.ts", "", body} }
	const upstream = "export function runWidgetSuite(expect) {\n\texpect(new Widget(\"w\").name).toBe(\"w\");\n}\n"
	published := [3]string{".upstream/current/packages/fx/src/index.ts", "", "export * from \"./suite.ts\";\n"}
	mapped := [3]string{"docs/parity/PORT_MAP.md", "", "| `packages/fx/src/suite.ts` | `lib/suite.go` | ✅ |\n"}
	runs := func(arg string) [3]string {
		return [3]string{"lib/lib_test.go", "", "\ntype quiet struct{}\n\nfunc (quiet) Error(...any) {}\n\nfunc TestWidgetSuite(t *testing.T) {\n\tRunWidgetSuite(" + arg + ")\n}\n"}
	}
	if d := libraryRun(t, nil, uncite, suiteGo, piSuite(upstream), published, mapped, runs("t"))[id]; d == nil || d.Gap || d.Evidence != "test:lib/lib_test.go#TestWidgetSuite" {
		t.Errorf("a test running the ported Pi suite with its *testing.T closes the member, got %+v", d)
	}
	wantLibraryGap(t, libraryRun(t, nil, uncite, suiteGo, piSuite(upstream), published, mapped, runs("quiet{}")), id, "L6")
	wantLibraryGap(t, libraryRun(t, nil, uncite, suiteGo, piSuite(upstream), published, runs("t")), id, "L6")
	wantLibraryGap(t, libraryRun(t, nil, uncite, suiteGo, piSuite(upstream), mapped, runs("t")), id, "L6")
	wantLibraryGap(t, libraryRun(t, nil, uncite, suiteGo, piSuite(strings.ReplaceAll(upstream, ".name", ".label")), published, mapped, runs("t")), id, "L6")
	stub := [3]string{"lib/lib.go", "func (w *Widget) Name() string { return w.n }", "func (w *Widget) Name() string { return \"w\" }"}
	wantLibraryGap(t, libraryRun(t, nil, uncite, suiteGo, piSuite(upstream), published, mapped, runs("t"), stub), id, "L4")
	forward := [3]string{"lib/lib.go", "func (w *Widget) Name() string { return w.n }",
		"func (w *Widget) Name() string { return w.Label() }\n\n// Label is the widget label.\nfunc (w *Widget) Label() string { return w.n }"}
	wantLibraryGap(t, libraryRun(t, nil, uncite, suiteGo, piSuite(upstream), published, mapped, runs("t"), forward), id, "L3")
}

// TestLibraryCitationOfAPinnedMirror: L6. A file:line in the current mirror (.upstream/current/packages/...) is an ordinary
// citation. A file:line in a pinned mirror (.upstream/<version>/packages/...) leaves the gap even when that mirror is present and the
// cited line is unchanged in the current file: CI checks out only the current mirror and the pinned version, so the decision must
// not depend on another mirror. A path that leaves .upstream leaves the gap too.
func TestLibraryCitationOfAPinnedMirror(t *testing.T) {
	cite := "// TestLibraryMembers follows packages/fx/src/types.ts:2 (Config) and types.ts:6 (Widget).\n"
	at := func(version string, line int) [3]string {
		path := fmt.Sprintf(".upstream/%s/packages/fx/src/types.ts:", version)
		return [3]string{"lib/lib_test.go", cite, fmt.Sprintf("// TestLibraryMembers follows %s%d and %s%d.\n", path, line, path, line+4)}
	}
	mirror := [3]string{".upstream/v1.0.0/packages/fx/src/types.ts", "", fxLibTypes}
	for name, c := range map[string]struct {
		edits  [][3]string
		closes bool
	}{
		"current mirror":        {[][3]string{at("current", 2)}, true},
		"current, past the end": {[][3]string{at("current", 200)}, false},
		"pinned, line survives": {[][3]string{at("v1.0.0", 2), mirror}, false},
		"pinned, no mirror":     {[][3]string{at("v1.0.0", 2)}, false},
		"outside":               {[][3]string{at("..", 2), {"packages/fx/src/types.ts", "", fxLibTypes}}, false},
	} {
		t.Run(name, func(t *testing.T) {
			ds := libraryRun(t, nil, c.edits...)
			for _, id := range libraryMembers {
				if c.closes {
					if d := ds[id]; d == nil || d.Gap {
						t.Errorf("%s: want closed, got %+v", id, d)
					}
					continue
				}
				wantLibraryGap(t, ds, id, "L6")
			}
		})
	}
}

// TestLibraryWordMatchesSymbolKeyedMembers: L6 and L1 read whether a Pi file names a member. A symbol-keyed member
// ([Symbol.asyncIterator]) is named next to punctuation, so word boundaries apply only at its word-character ends, while an
// identifier stays a whole word.
func TestLibraryWordMatchesSymbolKeyedMembers(t *testing.T) {
	lr := &library{words: map[string]*regexp.Regexp{}}
	for _, c := range []struct {
		member, text string
		want         bool
	}{
		{"[Symbol.asyncIterator]", "\tasync *[Symbol.asyncIterator](): AsyncIterator<T> {", true},
		{"[Symbol.asyncIterator]", "const iterator = stream[Symbol.asyncIterator]();", true},
		{"[Symbol.asyncIterator]", "for await (const event of stream) {", false},
		{"push", "stream.push(event);", true},
		{"push", "stream.pushAll(events);", false},
		{"push", "stream.repush(event);", false},
	} {
		if got := lr.word(c.member).MatchString(c.text); got != c.want {
			t.Errorf("word(%q) on %q = %v, want %v", c.member, c.text, got, c.want)
		}
	}
}

// TestLibraryDataSchemaIsReadThroughItsBuilder (L5t): a data-only schema struct (no methods) closes its JSON-tagged fields when
// non-test code of the package consumes a type that holds it, as telemetry's typed builders consume TelemetrySchemaDefinition
// (LEAD-RULINGS-1520). A struct with a method, a struct no consumer holds, and a field without a JSON tag stay P1 gaps.
func TestLibraryDataSchemaIsReadThroughItsBuilder(t *testing.T) {
	const spec = "pkg:fx/.#Spec::property:label"
	types := "export interface Spec {\n\tlabel: string;\n}\nexport interface Holder {\n\tdone: string;\n}\n"
	goSrc := "\n// Spec is one schema entry.\ntype Spec struct {\n\tLabel string `json:\"label\"`\n}\n\n// Holder holds specs.\ntype Holder struct {\n\tItems map[string]Spec `json:\"items\"`\n}\n\n" +
		"// Build is the typed builder: it takes the holder.\nfunc Build(h *Holder) int { return len(h.Items) }\n"
	goTest := "\n// TestSpecLabel follows packages/fx/src/types.ts:8 (Spec).\nfunc TestSpecLabel(t *testing.T) {\n\ts := Spec{Label: \"a\"}\n\tif s.Label != \"a\" || Build(&Holder{Items: map[string]Spec{\"k\": s}}) != 1 {\n\t\tt.Error(\"spec\")\n\t}\n}\n"
	run := func(edits ...[3]string) *decision {
		files := libraryFiles(t, append([][3]string{
			{"lib/lib.go", "", goSrc}, {"lib/lib_test.go", "", goTest},
			{".upstream/current/packages/fx/src/types.ts", "", types},
		}, edits...)...)
		return fixtureRun(t, files, func(inv []m) []m { return append(inv, ifaceRow("Spec", "label")...) }, map[string]bool{}, nil)[spec]
	}
	if d := run(); d == nil || d.Gap {
		t.Fatalf("a data-only schema struct held by a consumed type closes, got %+v", d)
	}
	if d := run([3]string{"lib/lib.go", "func Build(h *Holder) int { return len(h.Items) }", ""}, [3]string{"lib/lib_test.go", "Build(&Holder{Items: map[string]Spec{\"k\": s}}) != 1", "false"}); d == nil || !d.Gap {
		t.Errorf("without a consumer of the holder the field stays a gap, got %+v", d)
	}
	if d := run([3]string{"lib/lib.go", "", "\n// Tag is a method that reads no field.\nfunc (s Spec) Tag() string { return \"x\" }\n"}); d == nil || !d.Gap {
		t.Errorf("a struct with a method is not a data-only schema, got %+v", d)
	}
	if d := run([3]string{"lib/lib.go", "Label string `json:\"label\"`", "Label string"}); d == nil || !d.Gap {
		t.Errorf("a field without a JSON tag is not read by the encoder, got %+v", d)
	}
}

// TestLibraryMemberOfATestDoubleOnlyInterface (L7): extension.API is implemented only by extensiontest.Fake, so a test that drives the
// double proves nothing about the production implementation. The member closes on a conformance test, or once a non-double type
// implements the interface.
func TestLibraryMemberOfATestDoubleOnlyInterface(t *testing.T) {
	const bus = "pkg:fx/.#Bus::property:label"
	types := "export interface Bus {\n\tlabel: string;\n}\n"
	goSrc := "\n// Bus is the Go form of the upstream Bus interface.\ntype Bus interface{ Label() string }\n\n// FakeBus is the test double of Bus.\ntype FakeBus struct{}\n\n// Label is the double's name.\nfunc (FakeBus) Label() string { return \"fake\" + string(rune('0'+len(\"x\"))) }\n"
	goTest := "\n// TestBusLabel follows packages/fx/src/types.ts:8 (Bus).\nfunc TestBusLabel(t *testing.T) {\n\tvar b Bus = FakeBus{}\n\tif b.Label() != \"fake1\" {\n\t\tt.Error(\"bus\")\n\t}\n}\n"
	run := func(edits ...[3]string) *decision {
		files := libraryFiles(t, append([][3]string{
			{"lib/lib.go", "", goSrc}, {"lib/lib_test.go", "", goTest},
			{".upstream/current/packages/fx/src/types.ts", "", types},
		}, edits...)...)
		return fixtureRun(t, files, func(inv []m) []m { return append(inv, ifaceRow("Bus", "label")...) }, map[string]bool{}, nil)[bus]
	}
	if d := run(); d == nil || !d.Gap || !strings.Contains(d.Detail, "L7") {
		t.Fatalf("a Fake-only interface member stays an L7 gap, got %+v", d)
	}
	if d := run([3]string{"lib/lib.go", "", "\n// Real is the production Bus.\ntype Real struct{}\n\n// Label is the production name.\nfunc (Real) Label() string { return \"real\" + string(rune('0'+len(\"x\"))) }\n"}); d == nil || d.Gap {
		t.Errorf("an interface with a production implementation is not a double-only interface, got %+v", d)
	}
	if d := run([3]string{"lib/lib_test.go", "func TestBusLabel(", "func TestConformance_BusName("}); d == nil || d.Gap {
		t.Errorf("a conformance test closes a double-only member, got %+v", d)
	}
	conf := "package conformance\n\nimport \"testing\"\n\n// TestBusLabelAcrossSDKs follows packages/fx/src/types.ts:8 (Bus): every SDK declares pi.label.\nfunc TestBusLabelAcrossSDKs(t *testing.T) {\n\tif \"label\" == \"\" {\n\t\tt.Error(\"label\")\n\t}\n}\n"
	if d := run([3]string{"test/extension-conformance/bus_test.go", "", conf}); d == nil || d.Gap {
		t.Errorf("a conformance test that names the member and cites Pi closes a double-only member, got %+v", d)
	}
	uncited := strings.Replace(conf, "follows packages/fx/src/types.ts:8 (Bus): ", "", 1)
	if d := run([3]string{"test/extension-conformance/bus_test.go", "", uncited}); d == nil || !d.Gap {
		t.Errorf("a conformance test without a Pi citation does not close it, got %+v", d)
	}
	other := strings.ReplaceAll(conf, "label", "other")
	if d := run([3]string{"test/extension-conformance/bus_test.go", "", other}); d == nil || !d.Gap {
		t.Errorf("a conformance test that does not name the member does not close it, got %+v", d)
	}
	commentOnly := strings.ReplaceAll(conf, `"label"`, `"x"`)
	if d := run([3]string{"test/extension-conformance/bus_test.go", "", commentOnly}); d == nil || !d.Gap {
		t.Errorf("a member named only in a comment is not used by the conformance test, got %+v", d)
	}
	// An interface that nothing implements has no double either: L7 needs at least one implementation, every one a double.
	noImpl := run(
		[3]string{"lib/lib.go", "func (FakeBus) Label() string", "func label() string"},
		[3]string{"lib/lib_test.go", "var b Bus = FakeBus{}", "var b Bus"},
	)
	if noImpl != nil && strings.Contains(noImpl.Detail, "L7") {
		t.Errorf("an interface with no implementation is not a double-only interface, got %+v", noImpl)
	}
}

// TestPromotedOntoExportedOwner: L2 accepts an exported method or field of an unexported type when embedding promotes it onto the
// exported Go type named after the upstream owner (Pi `class NodeSqliteDatabase extends NodeSqliteExecutor`). A member the owner
// shadows, an unexported member, an owner that does not embed the type and an unexported owner stay L2 gaps.
func TestPromotedOntoExportedOwner(t *testing.T) {
	gt := goTypes(t, `
type executor struct{ Limit int }

func (*executor) Run() error   { return nil }
func (*executor) Get() error   { return nil }
func (*executor) close() error { return nil }

type Database struct{ *executor }

func (*Database) Get() error { return nil }

type Other struct{}

type hidden struct{ *executor }`)
	exec := gt["executor"].(*types.Named)
	pkg := exec.Obj().Pkg()
	member := func(name string) *sym {
		obj, _, _ := types.LookupFieldOrMethod(types.NewPointer(exec), true, pkg, name)
		if obj == nil {
			t.Fatalf("no executor member %s", name)
		}
		return &sym{Kind: "method", Name: name, Owner: "executor", Obj: obj}
	}
	for _, tc := range []struct {
		member, owner string
		want          bool
	}{
		{"Run", "Database", true}, {"Limit", "Database", true},
		{"Get", "Database", false}, {"close", "Database", false}, {"Run", "Other", false}, {"Run", "hidden", false}, {"Run", "Missing", false},
	} {
		if got := promotedOnto(member(tc.member), tc.owner); got != tc.want {
			t.Errorf("promotedOnto(executor.%s, %s) = %v, want %v", tc.member, tc.owner, got, tc.want)
		}
	}
}

// TestLibraryMemberPromotedThroughEmbedding: L2. Widget's name lives on an unexported embedded base (TS `class Widget extends
// Base`); embedding promotes it onto the exported Widget, so the cited test closes the row.
func TestLibraryMemberPromotedThroughEmbedding(t *testing.T) {
	ds := libraryRun(t, nil,
		[3]string{"lib/lib.go", "type Widget struct{ n string }", "type widgetBase struct{ n string }\n\ntype Widget struct{ *widgetBase }"},
		[3]string{"lib/lib.go", "return &Widget{n: name}", "return &Widget{&widgetBase{n: name}}"},
		[3]string{"lib/lib.go", "func (w *Widget) Name() string { return w.n }", "func (w *widgetBase) Name() string { return w.n }"})
	d := ds["pkg:fx/.#Widget::property:name"]
	if d == nil || d.Gap || d.Evidence != "test:lib/lib_test.go#TestLibraryMembers" {
		t.Fatalf("a promoted library member must close on its cited test, got %+v", d)
	}

	// An unexported method is not library API, embedded or not.
	ds = libraryRun(t, nil,
		[3]string{"lib/lib.go", "type Widget struct{ n string }", "type widgetBase struct{ n string }\n\ntype Widget struct{ *widgetBase }"},
		[3]string{"lib/lib.go", "return &Widget{n: name}", "return &Widget{&widgetBase{n: name}}"},
		[3]string{"lib/lib.go", "func (w *Widget) Name() string { return w.n }", "func (w *widgetBase) name() string { return w.n }"},
		[3]string{"lib/lib.go", "func Describe(w *Widget) string { return w.Name() }", "func Describe(w *Widget) string { return w.name() }"},
		[3]string{"lib/lib_test.go", "NewWidget(\"w\").Name() != \"w\"", "NewWidget(\"w\").name() != \"w\""})
	wantLibraryGap(t, ds, "pkg:fx/.#Widget::property:name", "L2")
}

// TestLibraryConstantFixedUpstreamIsNotAStandIn: L4. A Go method returning one constant is the faithful port when the upstream
// class fixes the member to that constant: a `readonly name = "w"` initializer, a constructor passing the literal message to super,
// and a super call without options (cause stays undefined, Go nil), and a nil Flush for an `async flush(): Promise<void>` that
// awaits a queue whose every link ends in a catch that throws nothing. A different literal, an assignment in the constructor, a
// super call that passes options, or a queue that can reject leaves the L4 stand-in gap.
func TestLibraryConstantFixedUpstreamIsNotAStandIn(t *testing.T) {
	widget := "export class Widget {\n\tconstructor(readonly name: string) {}\n}\n"
	class := func(body string) [3]string {
		return [3]string{".upstream/current/packages/fx/src/types.ts", widget, "export class Widget extends Error {\n" + body + "}\n"}
	}
	// goMember renames Widget.Name to the member's Go method returning one constant of type typ, and its callers.
	goMember := func(method, typ, value string) [][3]string {
		zero := `"w"`
		if typ == "error" {
			zero = "nil"
		}
		return [][3]string{
			{"lib/lib.go", "func (w *Widget) Name() string { return w.n }", "func (w *Widget) " + method + "() " + typ + " { return " + value + " }"},
			{"lib/lib.go", "func Describe(w *Widget) string { return w.Name() }", "func Describe(w *Widget) string { return w.n }"},
			{"lib/use.go", "_ = w.Name()", "_ = w." + method + "()"},
			{"lib/lib_test.go", "w.Name() != \"w\"", "w." + method + "() != " + zero},
			{"lib/lib_test.go", "NewWidget(\"w\").Name() != \"w\"", "NewWidget(\"w\")." + method + "() != " + zero},
		}
	}
	as := func(member, typ string) func(inv []m) []m {
		return func(inv []m) []m {
			for _, e := range inv {
				if e["id"] == "pkg:fx/.#Widget::property:name" {
					e["shape"] = m{"name": member, "type": typ}
				}
			}
			return inv
		}
	}
	const id = "pkg:fx/.#Widget::property:name"
	for _, tc := range []struct {
		name, member, method, typ, body, want string
	}{
		{"readonly initializer", "name", "Name", "string", "\treadonly name: string = \"w\";\n", "closed"},
		{"other initializer", "name", "Name", "string", "\treadonly name: string = \"v\";\n", "L4"},
		{"literal message", "message", "Message", "string", "\tconstructor() {\n\t\tsuper(\"w\");\n\t}\n", "L6"},
		{"other message", "message", "Message", "string", "\tconstructor() {\n\t\tsuper(\"v\");\n\t}\n", "L4"},
		{"assigned message", "message", "Message", "string", "\tconstructor() {\n\t\tsuper(\"w\");\n\t\tthis.message = \"v\";\n\t}\n", "L4"},
		{"no cause", "cause", "Cause", "error", "\tconstructor() {\n\t\tsuper(\"w\");\n\t}\n", "L6"},
		{"cause option", "cause", "Cause", "error", "\tconstructor(cause: Error) {\n\t\tsuper(\"w\", { cause });\n\t}\n", "L4"},
		{"assigned cause", "cause", "Cause", "error", "\tconstructor(e: Error) {\n\t\tsuper(\"w\");\n\t\tthis.cause = e;\n\t}\n", "L4"},
		{"never-rejecting queue", "flush", "Flush", "error", "\tprivate q: Promise<void> = Promise.resolve();\n\tsave(): void {\n\t\tthis.q = this.q\n\t\t\t.then(() => {\n\t\t\t\twrite();\n\t\t\t})\n\t\t\t.catch((error) => {\n\t\t\t\trecord(error);\n\t\t\t});\n\t}\n\tasync flush(): Promise<void> {\n\t\tawait this.q;\n\t}\n", "closed"},
		{"rejected start", "flush", "Flush", "error", "\tprivate q: Promise<void> = Promise.reject(new Error(\"x\"));\n\tsave(): void {\n\t\tthis.q = this.q\n\t\t\t.then(() => {\n\t\t\t\twrite();\n\t\t\t})\n\t\t\t.catch((error) => {\n\t\t\t\trecord(error);\n\t\t\t});\n\t}\n\tasync flush(): Promise<void> {\n\t\tawait this.q;\n\t}\n", "L4"},
		{"uncaught queue", "flush", "Flush", "error", "\tprivate q: Promise<void> = Promise.resolve();\n\tsave(): void {\n\t\tthis.q = this.q\n\t\t\t.then(() => {\n\t\t\t\twrite();\n\t\t\t});\n\t}\n\tasync flush(): Promise<void> {\n\t\tawait this.q;\n\t}\n", "L4"},
		{"rethrowing catch", "flush", "Flush", "error", "\tprivate q: Promise<void> = Promise.resolve();\n\tsave(): void {\n\t\tthis.q = this.q\n\t\t\t.then(() => {\n\t\t\t\twrite();\n\t\t\t})\n\t\t\t.catch((error) => {\n\t\t\t\tthrow error;\n\t\t\t});\n\t}\n\tasync flush(): Promise<void> {\n\t\tawait this.q;\n\t}\n", "L4"},
		{"call after the catch", "flush", "Flush", "error", "\tprivate q: Promise<void> = Promise.resolve();\n\tsave(): void {\n\t\tthis.q = this.q\n\t\t\t.then(() => {\n\t\t\t\twrite();\n\t\t\t})\n\t\t\t.catch(() => {})\n\t\t\t.then(() => write());\n\t}\n\tasync flush(): Promise<void> {\n\t\tawait this.q;\n\t}\n", "L4"},
	} {
		value := `"w"`
		if tc.typ == "error" {
			value = "nil"
		}
		edits := append(goMember(tc.method, tc.typ, value), class(tc.body))
		upType := "string"
		if tc.typ == "error" {
			upType = "unknown"
		}
		d := fixtureRun(t, libraryFiles(t, edits...), as(tc.member, upType), map[string]bool{}, nil)[id]
		got := "closed"
		switch {
		case d == nil:
			got = "no decision"
		case d.Gap:
			got = d.Detail
			if i := strings.Index(d.Detail, "P1(b) L"); i >= 0 {
				got = d.Detail[i+6 : i+8]
			}
		}
		if got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

// TestLibraryDataSchemaIsReadThroughABuilderThatTakesIt (L5t): telemetry's defineTelemetrySchema takes and returns the schema struct
// itself, so the builder is the consumer of a data-only struct whose every field is JSON-tagged. Without the builder, with a method on
// the struct or with an untagged field the member stays a P1 gap.
func TestLibraryDataSchemaIsReadThroughABuilderThatTakesIt(t *testing.T) {
	const spec = "pkg:fx/.#Spec::property:label"
	types := "export interface Spec {\n\tlabel: string;\n}\n"
	goSrc := "\n// Spec is one schema entry.\ntype Spec struct {\n\tLabel string `json:\"label\"`\n}\n\n// Define is the identity builder of the schema.\nfunc Define(s *Spec) *Spec { return s }\n"
	goTest := "\n// TestSpecLabel follows packages/fx/src/types.ts:8 (Spec).\nfunc TestSpecLabel(t *testing.T) {\n\ts := Spec{Label: \"a\"}\n\tif Define(&s).Label != \"a\" {\n\t\tt.Error(\"label\")\n\t}\n}\n"
	run := func(edits ...[3]string) *decision {
		files := libraryFiles(t, append([][3]string{
			{"lib/lib.go", "", goSrc}, {"lib/lib_test.go", "", goTest},
			{".upstream/current/packages/fx/src/types.ts", "", types},
		}, edits...)...)
		return fixtureRun(t, files, func(inv []m) []m { return append(inv, ifaceRow("Spec", "label")...) }, map[string]bool{}, nil)[spec]
	}
	if d := run(); d == nil || d.Gap {
		t.Fatalf("a builder that takes a data-only schema struct closes its fields, got %+v", d)
	}
	if d := run([3]string{"lib/lib.go", "func Define(s *Spec) *Spec { return s }", ""}, [3]string{"lib/lib_test.go", "Define(&s).Label", "s.Label"}); d == nil || !d.Gap {
		t.Errorf("without a builder the field stays a gap, got %+v", d)
	}
	if d := run([3]string{"lib/lib.go", "", "\n// Tag is a method that reads no field.\nfunc (s Spec) Tag() string { return \"x\" }\n"}); d == nil || !d.Gap {
		t.Errorf("a struct with a method is not a data-only schema, got %+v", d)
	}
	if d := run([3]string{"lib/lib.go", "Label string `json:\"label\"`", "Label string"}); d == nil || !d.Gap {
		t.Errorf("a field without a JSON tag is not serializable data, got %+v", d)
	}
	if d := run([3]string{"lib/lib.go", "Label string `json:\"label\"`", "Label string `json:\"label\"`\n\tNote  string"}); d == nil || !d.Gap {
		t.Errorf("a struct with an untagged sibling field is not wholly serializable data, got %+v", d)
	}
}
