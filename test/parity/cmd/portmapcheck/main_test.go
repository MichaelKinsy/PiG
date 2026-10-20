package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// fixture writes a small repository: one Pi file (packages/fx/src/widget.ts) with two exported members, and the PORT_MAP row for it.
func fixture(t *testing.T, cell, status string, extra map[string]string, targets map[string][]string, dispositions map[string]string) string {
	t.Helper()
	root := t.TempDir()
	write := func(name, body string) {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	js := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	type m = map[string]any
	ids := []string{"pkg:fx/.#Widget", "pkg:fx/.#Widget::property:name"}
	var entries, maps []m
	for _, id := range ids {
		entries = append(entries, m{"id": id, "source": m{"path": "node_modules/@scope/fx/dist/widget.d.ts"}})
		disp := "ported"
		if d, ok := dispositions[id]; ok {
			disp = d
		}
		maps = append(maps, m{"id": id, "disposition": disp, "pigTargets": targets[id]})
	}
	write("test/parity/interfaces/upstream-v1.0.0.json", js(m{"packages": []m{{"key": "fx", "name": "@scope/fx"}}, "interfaces": entries}))
	write("test/parity/interfaces/mapping-v1.0.0.json", js(m{"mappings": maps}))
	write("lib/widget.go", "package lib\n\ntype Widget struct{ Name string }\n")
	write("lib/other.go", "package lib\n\nfunc Unrelated() {}\n")
	write("docs/parity/PORT_MAP.md", "| upstream | pig | status |\n|---|---|---|\n| `packages/fx/src/widget.ts` | `"+cell+"` | "+status+" |\n")
	for k, v := range extra {
		write(k, v)
	}
	return root
}

var goodTargets = map[string][]string{"pkg:fx/.#Widget": {"lib/widget.go#Widget"}, "pkg:fx/.#Widget::property:name": {"lib/widget.go#Widget.Name"}}

func derivation(t *testing.T, root string) result {
	t.Helper()
	res, err := check(root)
	if err != nil || len(res) != 1 {
		t.Fatalf("check: %v, %d rows", err, len(res))
	}
	return res[0]
}

// TestCorrectTickIsDerivedAndFalseTicksAreNot seeds a correct tick (must pass) and the false ticks the audit found: a tick that cites an
// unrelated Go file, a cited file that holds no mapped symbol, a pending member, a file with no export and no marker test.
func TestCorrectTickIsDerivedAndFalseTicksAreNot(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cell    string
		targets map[string][]string
		disp    map[string]string
		extra   map[string]string
		want    bool
		reason  string
	}{
		{"correct tick", "lib/widget.go", goodTargets, nil, nil, true, ""},
		{"a test file beside it is evidence, not a symbol", "lib/widget.go + lib/widget_test.go", goodTargets, nil, map[string]string{"lib/widget_test.go": "package lib\n"}, true, ""},
		{"a bare file name sits beside the file cited before it", "lib/widget.go + other.go", goodTargets, nil, nil, false, "ZERO-SYMBOL cited file lib/other.go"},
		{"tick cites an unrelated file", "lib/other.go", goodTargets, nil, nil, false, "WRONG-FILE"},
		{"a cited file holds no mapped symbol", "lib/widget.go + lib/other.go", goodTargets, nil, nil, false, "ZERO-SYMBOL"},
		{"a pending member", "lib/widget.go", goodTargets, map[string]string{"pkg:fx/.#Widget::property:name": "pending"}, nil, false, "LEDGER-GAP"},
		{"a designed-out member needs no Go symbol", "lib/widget.go", map[string][]string{"pkg:fx/.#Widget": {"lib/widget.go#Widget"}}, map[string]string{"pkg:fx/.#Widget::property:name": "designed-out"}, nil, true, ""},
		{"the cited Go file does not exist", "lib/missing.go", goodTargets, nil, nil, false, "does not exist"},
		{"no Go file cited", "n/a", goodTargets, nil, nil, false, "no Go file cited"},
	} {
		got := derivation(t, fixture(t, tc.cell, "✅", tc.extra, tc.targets, tc.disp))
		if got.Proven != tc.want || (tc.reason != "" && !strings.Contains(strings.Join(got.Reasons, ";"), tc.reason)) {
			t.Errorf("%s: proven=%v reasons=%v, want proven=%v %q", tc.name, got.Proven, got.Reasons, tc.want, tc.reason)
		}
	}
}

// TestFileWithoutExportsNeedsAMarkerTest: a Pi file with no ledger member (timings.ts-style) needs a Go test that carries
// `// pi: <path>`, and the marker alone does not prove it.
func TestFileWithoutExportsNeedsAMarkerTest(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra map[string]string
		want  bool
	}{
		{"no marker", nil, false},
		// A marker comment alone does not prove the row (L7-FAKE): coverage and mutation evidence are still required.
		{"marker test without coverage evidence", map[string]string{"lib/widget_test.go": "package lib\n\n// pi: packages/fx/src/widget.ts\nfunc TestWidget() {}\n"}, false},
		{"marker for another file", map[string]string{"lib/widget_test.go": "package lib\n\n// pi: packages/fx/src/other.ts\nfunc TestWidget() {}\n"}, false},
		{"marker in production code is not a test", map[string]string{"lib/other.go": "package lib\n\n// pi: packages/fx/src/widget.ts\n"}, false},
	} {
		root := fixture(t, "lib/widget.go", "✅", tc.extra, nil, nil)
		// Drop the ledger members so the file has no exported member.
		if err := os.WriteFile(filepath.Join(root, "test/parity/interfaces/upstream-v1.0.0.json"), []byte(`{"packages":[],"interfaces":[]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := derivation(t, root); got.Proven != tc.want {
			t.Errorf("%s: proven=%v %v, want %v", tc.name, got.Proven, got.Reasons, tc.want)
		}
	}
}

// TestReviewedRenameNamesTheImplementingSymbol: a renamed implementation is accepted only when the reviewed entry names a symbol that is
// defined in a cited file.
func TestReviewedRenameNamesTheImplementingSymbol(t *testing.T) {
	moved := map[string][]string{"pkg:fx/.#Widget": {"lib/widget.go#Widget"}, "pkg:fx/.#Widget::property:name": {"lib/other.go#Elsewhere"}}
	for _, tc := range []struct {
		name     string
		reviewed string
		want     bool
	}{
		{"no reviewed entry", "", false},
		{"symbol defined in a cited file", `{"packages/fx/src/widget.ts":{"reason":"r","members":{"pkg:fx/.#Widget::property:name":"lib/widget.go#Widget.Name"}}}`, true},
		{"symbol not defined there", `{"packages/fx/src/widget.ts":{"reason":"r","members":{"pkg:fx/.#Widget::property:name":"lib/widget.go#Nope"}}}`, false},
		{"symbol in an uncited file", `{"packages/fx/src/widget.ts":{"reason":"r","members":{"pkg:fx/.#Widget::property:name":"lib/other.go#Unrelated"}}}`, false},
	} {
		extra := map[string]string{}
		if tc.reviewed != "" {
			extra[reviewedFile] = tc.reviewed
		}
		if got := derivation(t, fixture(t, "lib/widget.go", "✅", extra, moved, nil)); got.Proven != tc.want {
			t.Errorf("%s: proven=%v %v, want %v", tc.name, got.Proven, got.Reasons, tc.want)
		}
	}
}

// TestBaselineOnlyShrinks: a new unproven tick fails, a baseline entry for a proven or vanished row fails, and a listed unproven tick
// passes.
func TestBaselineOnlyShrinks(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cell     string
		baseline string
		wantFail string
	}{
		{"new false tick", "lib/other.go", "", "is ✅ but the ledger does not derive it"},
		{"listed false tick", "lib/other.go", "packages/fx/src/widget.ts\n", ""},
		{"stale entry for a proven row", "lib/widget.go", "packages/fx/src/widget.ts\n", "now proven"},
		{"entry for a row that is no longer ✅", "lib/widget.go", "packages/fx/src/gone.ts\n", "no longer a ✅ row"},
		{"correct tick, empty baseline", "lib/widget.go", "", ""},
	} {
		root := fixture(t, tc.cell, "✅", map[string]string{baselineFile: tc.baseline}, goodTargets, nil)
		res, _ := check(root)
		problems, _ := compareBaseline(root, res)
		joined := strings.Join(problems, "\n")
		if tc.wantFail == "" && len(problems) != 0 || tc.wantFail != "" && !strings.Contains(joined, tc.wantFail) {
			t.Errorf("%s: problems = %q, want %q", tc.name, joined, tc.wantFail)
		}
	}
}

// TestGenerateDemotesAnUnprovenTick: -generate rewrites the status column from the derivation.
func TestGenerateDemotesAnUnprovenTick(t *testing.T) {
	root := fixture(t, "lib/other.go", "✅", nil, goodTargets, nil)
	res, _ := check(root)
	if err := generateStatus(root, res); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, portMapFile))
	if !strings.Contains(string(b), "| 🟡 |") || strings.Contains(string(b), "✅") {
		t.Fatalf("PORT_MAP after generate:\n%s", b)
	}
}

// coverageFixture is fixture() plus a Go module, ledger evidence naming TestWidget, and the test file.
func coverageFixture(t *testing.T, testBody string) string {
	t.Helper()
	root := fixture(t, "lib/widget.go, lib/widget_test.go", "✅", map[string]string{
		"go.mod":             "module fixture\n\ngo 1.26\n",
		"lib/widget.go":      "package lib\n\ntype Widget struct{ Name string }\n\nfunc (w Widget) Hello() string { return \"hello \" + w.Name }\n",
		"lib/widget_test.go": testBody,
	}, goodTargets, nil)
	mapping := map[string]any{"mappings": []map[string]any{
		{"id": "pkg:fx/.#Widget", "disposition": "ported", "pigTargets": []string{"lib/widget.go#Widget"}, "evidence": []string{"test:lib/widget_test.go#TestWidget"}},
		{"id": "pkg:fx/.#Widget::property:name", "disposition": "ported", "pigTargets": []string{"lib/widget.go#Widget.Name"}, "evidence": []string{"test:lib/widget_test.go#TestWidget"}},
	}}
	b, _ := json.Marshal(mapping)
	if err := os.WriteFile(filepath.Join(root, "test/parity/interfaces/mapping-v1.0.0.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestEvidenceTestsMustExecuteTheCitedFile (rule 3): a tick whose named tests never run the cited code fails, as does one whose tests are
// red, whose coverage record is missing, or whose cited file changed after the record.
func TestEvidenceTestsMustExecuteTheCitedFile(t *testing.T) {
	const runs = "package lib\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {\n\tif (Widget{Name: \"a\"}).Hello() != \"hello a\" {\n\t\tt.Fatal(\"hello\")\n\t}\n}\n"
	const never = "package lib\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {}\n"
	const red = "package lib\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) { t.Fatal(\"red\") }\n"
	for _, tc := range []struct {
		name   string
		body   string
		record bool
		edit   bool
		want   string // "" means proven
	}{
		{"tests execute the cited file", runs, true, false, ""},
		{"tests never touch the cited file", never, true, false, "execute no statement of lib/widget.go"},
		{"tests are red", red, true, false, "evidence tests of lib fail"},
		{"the cited file changed after a passing run: the tests run again", runs, true, true, "evidence tests of lib fail"},
	} {
		root := coverageFixture(t, tc.body)
		if tc.record {
			if res, err := checkWith(root, true); err != nil || !res[0].Proven && tc.want == "" {
				t.Fatalf("%s: first run: %v %v", tc.name, err, res)
			}
		}
		if tc.edit {
			if err := os.WriteFile(filepath.Join(root, "lib/widget.go"), []byte("package lib\n\ntype Widget struct{ Name string }\n\nfunc (w Widget) Hello() string { return w.Name }\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		res, err := checkWith(root, true)
		if err != nil || len(res) != 1 {
			t.Fatalf("%s: %v %d", tc.name, err, len(res))
		}
		got := strings.Join(res[0].Reasons, ";")
		if (tc.want == "") != res[0].Proven || (tc.want != "" && !strings.Contains(got, tc.want)) {
			t.Errorf("%s: proven=%v reasons=%q, want %q", tc.name, res[0].Proven, got, tc.want)
		}
	}
}

// TestADeclarationOnlyCitedFileNeedsOnlyGreenEvidence: Go coverage instruments only function bodies, so a cited file of types,
// constants and embedded data can never show an executed statement. Rule 3 then asks only that the evidence tests pass.
func TestADeclarationOnlyCitedFileNeedsOnlyGreenEvidence(t *testing.T) {
	const never = "package lib\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {}\n"
	const red = "package lib\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) { t.Fatal(\"red\") }\n"
	for _, tc := range []struct {
		name, source, body, want string // want "" means proven
	}{
		{"declarations only, green tests", "package lib\n\ntype Widget struct{ Name string }\n\nconst Limit = 3\n", never, ""},
		{"declarations only, red tests", "package lib\n\ntype Widget struct{ Name string }\n", red, "evidence tests of lib fail"},
		{"a function literal is a statement", "package lib\n\ntype Widget struct{ Name string }\n\nvar Hello = func(w Widget) string { return w.Name }\n", never, "execute no statement of lib/widget.go"},
	} {
		root := coverageFixture(t, tc.body)
		if err := os.WriteFile(filepath.Join(root, "lib/widget.go"), []byte(tc.source), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := checkWith(root, true)
		if err != nil || len(res) != 1 {
			t.Fatalf("%s: %v %d", tc.name, err, len(res))
		}
		got := strings.Join(res[0].Reasons, ";")
		if (tc.want == "") != res[0].Proven || (tc.want != "" && !strings.Contains(got, tc.want)) {
			t.Errorf("%s: proven=%v reasons=%q, want %q", tc.name, res[0].Proven, got, tc.want)
		}
	}
}

// TestARedEvidenceRunIsNotCached: a failing evidence run can come from the environment (a Pi oracle's missing Node dependency), which the
// digest does not see; the row is proven by the next check once the environment is fixed, with no edit to a cited file or test.
func TestARedEvidenceRunIsNotCached(t *testing.T) {
	flag := filepath.Join(t.TempDir(), "oracle-ready")
	body := "package lib\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestWidget(t *testing.T) {\n\tif _, err := os.Stat(" + strconv.Quote(flag) + "); err != nil {\n\t\tt.Fatal(\"oracle dependency missing\")\n\t}\n\tif (Widget{Name: \"a\"}).Hello() != \"hello a\" {\n\t\tt.Fatal(\"hello\")\n\t}\n}\n"
	root := coverageFixture(t, body)
	res, err := checkWith(root, true)
	if err != nil || res[0].Proven || !strings.Contains(strings.Join(res[0].Reasons, ";"), "evidence tests of lib fail") {
		t.Fatalf("the first run is red, got %v %+v", err, res)
	}
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if res, err = checkWith(root, true); err != nil || !res[0].Proven {
		t.Fatalf("after the environment is fixed the row is proven, got %v %+v", err, res[0].Reasons)
	}
}

// TestATickWithNoEvidenceTestFails: a ledger-proven row whose members name no test has nothing that executes the cited file.
func TestATickWithNoEvidenceTestFails(t *testing.T) {
	root := coverageFixture(t, "package lib\n")
	b, _ := json.Marshal(map[string]any{"mappings": []map[string]any{
		{"id": "pkg:fx/.#Widget", "disposition": "ported", "pigTargets": []string{"lib/widget.go#Widget"}},
		{"id": "pkg:fx/.#Widget::property:name", "disposition": "ported", "pigTargets": []string{"lib/widget.go#Widget.Name"}},
	}})
	if err := os.WriteFile(filepath.Join(root, "test/parity/interfaces/mapping-v1.0.0.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	res, _ := checkWith(root, true)
	if res[0].Proven || !strings.Contains(strings.Join(res[0].Reasons, ";"), "no evidence test") {
		t.Fatalf("proven=%v %v", res[0].Proven, res[0].Reasons)
	}
}

// TestSymbolInReadsAMethodOfAMultiParameterGenericType: a reviewed rename naming a method of `Pair[K, V]` is found (its receiver is an
// IndexListExpr).
func TestSymbolInReadsAMethodOfAMultiParameterGenericType(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "package lib\n\ntype Pair[K comparable, V any] struct{ k K; v V }\n\nfunc (p *Pair[K, V]) Key() K { return p.k }\n"
	if err := os.WriteFile(filepath.Join(root, "lib/pair.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cited := map[string]bool{"lib/pair.go": true}
	if !symbolIn(root, "lib/pair.go#Pair.Key", cited) || symbolIn(root, "lib/pair.go#Pair.Missing", cited) {
		t.Fatal("a method of a two-parameter generic type must be found, and a missing one must not")
	}
}

// TestTheInventoryIsThePinnedVersion: a later-sorting upstream-v*.json does not replace the pinned version's inventory.
func TestTheInventoryIsThePinnedVersion(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "test/parity/interfaces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pinned := `{"packages":[{"key":"fx","name":"@scope/fx"}],"interfaces":[{"id":"pkg:fx/.#A","source":{"path":"node_modules/@scope/fx/dist/a.d.ts"}}]}`
	other := `{"packages":[],"interfaces":[]}`
	for name, body := range map[string]string{"upstream-v" + pigversion.UpstreamVersion + ".json": pinned, "upstream-v9.9.9.json": other, "mapping-v" + pigversion.UpstreamVersion + ".json": `{"mappings":[]}`, "mapping-v9.9.9.json": `{"mappings":[]}`} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	inv, err := readInventory(root)
	if err != nil || len(inv.membersOf("packages/fx/src/a.ts")) != 1 {
		t.Fatalf("pinned inventory not read: %v %v", err, inv)
	}
}

// TestEvidenceTestsRunWithScratchAgentDirectories: rule 3 runs arbitrary package tests from a lane shell or CI; each agent-directory
// variable they see points into a scratch tree, never at the caller's directory.
func TestEvidenceTestsRunWithScratchAgentDirectories(t *testing.T) {
	caller := t.TempDir()
	for _, name := range agentDirVariables {
		t.Setenv(name, caller)
	}
	body := "package lib\n\nimport (\n\t\"os\"\n\t\"strings\"\n\t\"testing\"\n)\n\nfunc TestWidget(t *testing.T) {\n" +
		"\tfor _, name := range []string{\"PIG_CODING_AGENT_DIR\", \"PI_CODING_AGENT_DIR\", \"PIG_HOME\", \"PI_HOME\", \"HOME\"} {\n" +
		"\t\tif v := os.Getenv(name); v == \"\" || strings.HasPrefix(v, " + strconv.Quote(caller) + ") {\n\t\t\tt.Fatalf(\"%s = %q\", name, v)\n\t\t}\n\t}\n" +
		"\tif (Widget{Name: \"a\"}).Hello() != \"hello a\" {\n\t\tt.Fatal(\"hello\")\n\t}\n}\n"
	res, err := checkWith(coverageFixture(t, body), true)
	if err != nil || len(res) != 1 || !res[0].Proven {
		t.Fatalf("err=%v res=%+v: the evidence test saw the caller's agent directories", err, res)
	}
}

// TestASourceFileBesideTheEvidenceTestsInvalidatesTheCache: the digest covers every Go file of the evidence package, not only the cited
// files, so a change to a helper the cited file calls re-runs the tests instead of reusing a green record.
func TestASourceFileBesideTheEvidenceTestsInvalidatesTheCache(t *testing.T) {
	const body = "package lib\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {\n\tif (Widget{Name: \"a\"}).Hello() != \"hello a\" {\n\t\tt.Fatal(\"hello\")\n\t}\n}\n"
	root := coverageFixture(t, body)
	helper := filepath.Join(root, "lib/helper.go")
	if err := os.WriteFile(filepath.Join(root, "lib/widget.go"), []byte("package lib\n\ntype Widget struct{ Name string }\n\nfunc (w Widget) Hello() string { return greeting() + w.Name }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, []byte("package lib\n\nfunc greeting() string { return \"hello \" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res, err := checkWith(root, true); err != nil || !res[0].Proven {
		t.Fatalf("first run: %v %+v", err, res)
	}
	if err := os.WriteFile(helper, []byte("package lib\n\nfunc greeting() string { return \"bye \" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res, _ := checkWith(root, true); res[0].Proven {
		t.Fatal("a changed helper beside the cited file must re-run the tests (they now fail)")
	}
}

// TestInstallingTheOracleInvalidatesTheCache: an evidence test that needs the Pi oracle fails while it is absent; installing it re-runs the
// test instead of reusing the red record.
func TestInstallingTheOracleInvalidatesTheCache(t *testing.T) {
	body := "package lib\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestWidget(t *testing.T) {\n" +
		"\tif _, err := os.Stat(\"../" + oracleInstall + "\"); err != nil {\n\t\tt.Fatal(err)\n\t}\n" +
		"\tif (Widget{Name: \"a\"}).Hello() != \"hello a\" {\n\t\tt.Fatal(\"hello\")\n\t}\n}\n"
	root := coverageFixture(t, body)
	if res, _ := checkWith(root, true); res[0].Proven {
		t.Fatal("the evidence test passed without the oracle")
	}
	lock := filepath.Join(root, oracleInstall)
	if err := os.MkdirAll(filepath.Dir(lock), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res, err := checkWith(root, true); err != nil || !res[0].Proven {
		t.Fatalf("installing the oracle must re-run the evidence tests: %v %+v", err, res)
	}
}

// markerFixture is a Pi file with no ledger member, a Go test that carries `// pi:` and runs the cited code, and a mutants file.
func markerFixture(t *testing.T, testBody, mutants string) string {
	t.Helper()
	root := fixture(t, "lib/widget.go, lib/widget_test.go", "✅", map[string]string{
		"go.mod":             "module fixture\n\ngo 1.26\n",
		"lib/widget.go":      "package lib\n\ntype Widget struct{ Name string }\n\nfunc (w Widget) Hello() string { return \"hello \" + w.Name }\n",
		"lib/widget_test.go": testBody,
		mutantsFile:          mutants,
	}, nil, nil)
	if err := os.WriteFile(filepath.Join(root, "test/parity/interfaces/upstream-v1.0.0.json"), []byte(`{"packages":[],"interfaces":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestMarkerRowIsProvenByCoverageAndAKilledMutant (rule 2 red-proof): a `// pi:` test proves a member-less Pi file only when its tests
// execute the cited file and a reviewed mutant of that file makes them fail.
func TestMarkerRowIsProvenByCoverageAndAKilledMutant(t *testing.T) {
	const marked = "package lib\n\nimport \"testing\"\n\n// pi: packages/fx/src/widget.ts\nfunc TestWidget(t *testing.T) {\n\tif (Widget{Name: \"a\"}).Hello() != \"hello a\" {\n\t\tt.Fatal(\"hello\")\n\t}\n}\n"
	const slack = "package lib\n\nimport \"testing\"\n\n// pi: packages/fx/src/widget.ts\nfunc TestWidget(t *testing.T) { _ = Widget{Name: \"a\"}.Hello() }\n"
	twice := `{"packages/fx/src/widget.ts":[{"file":"lib/widget.go","old":"\"hello \" + w.Name","new":"\"bye \" + w.Name"}]}`
	killer := `{"packages/fx/src/widget.ts":[{"file":"lib/widget.go","old":"\"hello \" + w.Name","new":"\"bye \" + w.Name"}]}`
	for _, tc := range []struct {
		name, body, mutants, want string
	}{
		{"coverage and a killed mutant", marked, killer, ""},
		{"no mutant listed", marked, `{}`, "names no mutant"},
		{"the old text is ambiguous (two sites)", marked, twice, "no listed mutant makes"},
		{"the tests assert nothing: the mutant survives", slack, killer, "no listed mutant makes the marker tests fail"},
		{"the mutant text is absent from the file", marked, `{"packages/fx/src/widget.ts":[{"file":"lib/widget.go","old":"nope","new":"x"}]}`, "no listed mutant makes"},
		{"the mutant does not compile", marked, `{"packages/fx/src/widget.ts":[{"file":"lib/widget.go","old":"\"hello \" + w.Name","new":"undefined"}]}`, "no listed mutant makes"},
		{"the mutant panics: the slack test only executes the code", slack, `{"packages/fx/src/widget.ts":[{"file":"lib/widget.go","old":"\"hello \" + w.Name","new":"func() string { panic(w.Name) }()"}]}`, "no listed mutant makes"},
		{"the mutant edits the evidence test", slack, `{"packages/fx/src/widget.ts":[{"file":"lib/widget_test.go","old":"_ = Widget","new":"t.Fatal(); _ = Widget"}]}`, "is a test file"},
		{"the mutant edits an uncited file", marked, `{"packages/fx/src/widget.ts":[{"file":"lib/other.go","old":"Unrelated","new":"X"}]}`, "is not a cited file"},
	} {
		root := markerFixture(t, tc.body, tc.mutants)
		if strings.HasPrefix(tc.name, "the old text is ambiguous") {
			dup := "package lib\n\ntype Widget struct{ Name string }\n\nfunc (w Widget) Hello() string { return \"hello \" + w.Name }\n\nfunc (w Widget) Hello2() string { return \"hello \" + w.Name }\n"
			if err := os.WriteFile(filepath.Join(root, "lib/widget.go"), []byte(dup), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		res, err := checkWith(root, true)
		if err != nil || len(res) != 1 {
			t.Fatalf("%s: %v %d", tc.name, err, len(res))
		}
		got := strings.Join(res[0].Reasons, ";")
		if (tc.want == "") != res[0].Proven || (tc.want != "" && !strings.Contains(got, tc.want)) {
			t.Errorf("%s: proven=%v reasons=%q, want %q", tc.name, res[0].Proven, got, tc.want)
		}
	}
}

// TestEvidenceEnvKeepsToolchainHomes: HOME is scratch, but the toolchains that resolve their installation from it (Cargo, Rustup, mise's
// XDG directories) keep their real locations, so evidence tests that build Rust or Python fixtures still run.
func TestEvidenceEnvKeepsToolchainHomes(t *testing.T) {
	real := t.TempDir()
	t.Setenv("HOME", real)
	for _, th := range toolchainHomes {
		t.Setenv(th.name, "")
	}
	env := map[string]string{}
	for _, kv := range evidenceEnv(t.TempDir()) {
		name, value, _ := strings.Cut(kv, "=")
		env[name] = value
	}
	if strings.HasPrefix(env["HOME"], real) {
		t.Fatalf("HOME = %q, want a scratch directory", env["HOME"])
	}
	for _, th := range []struct{ name, rel string }{{"CARGO_HOME", ".cargo"}, {"RUSTUP_HOME", ".rustup"}, {"XDG_DATA_HOME", ".local/share"}, {"XDG_CONFIG_HOME", ".config"}} {
		if want := filepath.Join(real, filepath.FromSlash(th.rel)); env[th.name] != want {
			t.Errorf("%s = %q, want %q", th.name, env[th.name], want)
		}
	}
	t.Setenv("CARGO_HOME", "/custom/cargo")
	for _, kv := range evidenceEnv(t.TempDir()) {
		if name, value, _ := strings.Cut(kv, "="); name == "CARGO_HOME" && value != "/custom/cargo" {
			t.Errorf("an explicit CARGO_HOME must be kept, got %q", value)
		}
	}
}

// TestInventoryReadsEverySourcePathForm: the inventory records a declaration by a published package's node_modules path, by a path relative
// to a package built in this repository (`dist/x.d.ts`, the package named by the ID), or by the TypeScript source of an unpublished package.
// A reader that knows only the first leaves the others without ledger members.
func TestInventoryReadsEverySourcePathForm(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "test/parity/interfaces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	inv := `{"packages":[{"key":"fx","name":"@scope/fx"}],"interfaces":[` +
		`{"id":"pkg:fx/.#A","source":{"path":"node_modules/@scope/fx/dist/a.d.ts"}},` +
		`{"id":"pkg:coding-agent/.#B","source":{"path":"dist/core/b.d.ts"}},` +
		`{"id":"pkg:durable/.#C","source":{"path":"packages/durable/src/c.ts"}},` +
		`{"id":"pkg:durable/.#D","source":{"path":"node_modules/typescript/lib/lib.d.ts"}}]}`
	for name, body := range map[string]string{"upstream-v" + pigversion.UpstreamVersion + ".json": inv, "mapping-v" + pigversion.UpstreamVersion + ".json": `{"mappings":[]}`} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := readInventory(root)
	if err != nil {
		t.Fatal(err)
	}
	for pi, want := range map[string]string{"packages/fx/src/a.ts": "pkg:fx/.#A", "packages/coding-agent/src/core/b.ts": "pkg:coding-agent/.#B", "packages/durable/src/c.ts": "pkg:durable/.#C"} {
		if ids := got.membersOf(pi); len(ids) != 1 || ids[0] != want {
			t.Errorf("%s members = %v, want [%s]", pi, ids, want)
		}
	}
	if ids := got.membersOf("packages/durable/src/lib.ts"); len(ids) != 0 {
		t.Errorf("a TypeScript library declaration is no Pi source file: %v", ids)
	}
}

// TestFixCellOnlyAddsWrongFileTargets: -fix-cells appends the files Pi members map to and never removes a citation or edits the prose,
// whatever the cell looks like (the agent-loop.ts cell with its '+' list and prose is left exactly as it is).
func TestFixCellOnlyAddsWrongFileTargets(t *testing.T) {
	agentLoop := "agent/agent_loop.go + agent/standalone_loop.go (all 35 agent-loop.test.ts cases; shallow stream views use ai/assistant_observation.go and ai/x.go)"
	cases := []struct {
		name, cell string
		add        []string
		want       string
	}{
		{"add a missing target", "`a/x.go`", []string{"b/z.go"}, "`a/x.go`, `b/z.go`"},
		{"already cited is not added twice", "`a/x.go`", []string{"a/x.go", "b/z.go", "b/z.go"}, "`a/x.go`, `b/z.go`"},
		{"a bare name beside the previous path counts as cited", "`a/x.go`, y.go", []string{"a/y.go"}, "`a/x.go`, y.go"},
		{"nothing to add leaves the cell byte for byte", agentLoop, nil, agentLoop},
		{"the prose and every cited file survive an add", agentLoop, []string{"agent/agent.go"}, agentLoop + ", `agent/agent.go`"},
	}
	for _, c := range cases {
		if got := fixCell(c.cell, c.add); got != c.want {
			t.Errorf("%s: fixCell(%q, %v) = %q, want %q", c.name, c.cell, c.add, got, c.want)
		}
	}
}

// TestAPortsClaimMakesAPrivateImplementationFileALegitimateCitation: a cited Go file that holds no mapped symbol but declares
// `// Ports <this row's Pi file>` (agent/agent_loop.go for agent-loop.ts, ai/anthropic_stream.go for anthropic-messages.ts) is the private
// implementation of the row, not a ZERO-SYMBOL false citation. A claim for another Pi file does not count, and dropping it is not the fix.
func TestAPortsClaimMakesAPrivateImplementationFileALegitimateCitation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra map[string]string
		want  bool
	}{
		{"agent-loop.ts style: a Ports claim for this row", map[string]string{"lib/other.go": "// Ports packages/fx/src/widget.ts\npackage lib\n\nfunc Unrelated() {}\n"}, true},
		{"anthropic-messages.ts style: the claim sits in the file doc", map[string]string{"lib/other.go": "package lib\n\n// Ports packages/fx/src/widget.ts: the private stream.\nfunc Unrelated() {}\n"}, true},
		{"a claim for another Pi file does not count", map[string]string{"lib/other.go": "// Ports packages/fx/src/gadget.ts\npackage lib\n\nfunc Unrelated() {}\n"}, false},
		{"no claim at all", nil, false},
	} {
		got := derivation(t, fixture(t, "lib/widget.go + lib/other.go", "✅", tc.extra, goodTargets, nil))
		if got.Proven != tc.want {
			t.Errorf("%s: proven=%v reasons=%v, want %v", tc.name, got.Proven, got.Reasons, tc.want)
		}
	}
}

// sharedFixture is one Go package, lib, that three marker rows cite: a's test asserts and its mutant is killed, b's test asserts nothing
// so its mutant survives, c's test is red. The upstream inventory is empty, so every row is a member-less Pi file proven by its marker.
func sharedFixture(t *testing.T) string {
	t.Helper()
	src := func(name string) string {
		return "package lib\n\nfunc " + name + "() string { return \"" + name + "\" }\n"
	}
	asserts := func(name, pi string) string {
		return "package lib\n\nimport \"testing\"\n\n// pi: " + pi + "\nfunc Test" + name + "(t *testing.T) {\n\tif " + name + "() != \"" + name + "\" {\n\t\tt.Fatal(\"" + name + "\")\n\t}\n}\n"
	}
	mutant := func(name string) string {
		return `{"file":"lib/` + strings.ToLower(name) + `.go","old":"return \"` + name + `\"","new":"return \"mutant\""}`
	}
	root := t.TempDir()
	files := map[string]string{
		"go.mod":        "module fixture\n\ngo 1.26\n",
		"lib/a.go":      src("A"),
		"lib/b.go":      src("B"),
		"lib/c.go":      src("C"),
		"lib/a_test.go": asserts("A", "packages/fx/src/a.ts"),
		"lib/b_test.go": "package lib\n\nimport \"testing\"\n\n// pi: packages/fx/src/b.ts\nfunc TestB(t *testing.T) { _ = B() }\n",
		"lib/c_test.go": "package lib\n\nimport \"testing\"\n\n// pi: packages/fx/src/c.ts\nfunc TestC(t *testing.T) { _ = C(); t.Fatal(\"red\") }\n",
		"test/parity/interfaces/upstream-v1.0.0.json": `{"packages":[],"interfaces":[]}`,
		"test/parity/interfaces/mapping-v1.0.0.json":  `{"mappings":[]}`,
		mutantsFile: `{"packages/fx/src/a.ts":[` + mutant("A") + `],"packages/fx/src/b.ts":[` + mutant("B") + `],"packages/fx/src/c.ts":[` + mutant("C") + `]}`,
		"docs/parity/PORT_MAP.md": "| upstream | pig | status |\n|---|---|---|\n" +
			"| `packages/fx/src/a.ts` | `lib/a.go, lib/a_test.go` | ✅ |\n" +
			"| `packages/fx/src/b.ts` | `lib/b.go, lib/b_test.go` | ✅ |\n" +
			"| `packages/fx/src/c.ts` | `lib/c.go, lib/c_test.go` | ✅ |\n",
	}
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// verdicts maps each row of res to "proven" or its reasons, in a form a test can compare.
func verdicts(res []result) map[string]string {
	out := map[string]string{}
	for _, r := range res {
		switch {
		case r.Skipped:
			out[r.Row.Pi] = "skipped"
		case r.Proven:
			out[r.Row.Pi] = "proven"
		default:
			out[r.Row.Pi] = strings.Join(r.Reasons, ";")
		}
	}
	return out
}

// TestRowsSharingAPackageKeepTheirOwnVerdicts: the rows of one package share a test binary, but each row is still proven only by its own
// tests and mutant. A passing row beside a red row, and a killed mutant beside a surviving one, do not leak into each other.
func TestRowsSharingAPackageKeepTheirOwnVerdicts(t *testing.T) {
	root := sharedFixture(t)
	before := evidenceBuilds.Load()
	res, err := checkWith(root, true)
	if err != nil || len(res) != 3 {
		t.Fatalf("%v %d", err, len(res))
	}
	got := verdicts(res)
	if got["packages/fx/src/a.ts"] != "proven" {
		t.Errorf("a: %q", got["packages/fx/src/a.ts"])
	}
	if !strings.Contains(got["packages/fx/src/b.ts"], "no listed mutant makes the marker tests fail") {
		t.Errorf("b survives its mutant, got %q", got["packages/fx/src/b.ts"])
	}
	if !strings.Contains(got["packages/fx/src/c.ts"], "evidence tests of lib fail") {
		t.Errorf("c is red, got %q", got["packages/fx/src/c.ts"])
	}
	if n := evidenceBuilds.Load() - before; n != 1 {
		t.Errorf("three rows of one package built %d test binaries, want 1", n)
	}
}

// TestAKilledMutantIsCachedWithItsRecord: the verdict of a mutant replay is part of the row's cached record, keyed by the mutant's text,
// so an unchanged marker row is not replayed, and a row whose mutant was edited is.
func TestAKilledMutantIsCachedWithItsRecord(t *testing.T) {
	root := sharedFixture(t)
	if res, err := checkWith(root, true); err != nil || verdicts(res)["packages/fx/src/a.ts"] != "proven" {
		t.Fatalf("first run: %v %v", err, verdicts(res))
	}
	builds, replays := evidenceBuilds.Load(), mutantReplays.Load()
	res, err := checkWith(root, true)
	if err != nil || verdicts(res)["packages/fx/src/a.ts"] != "proven" {
		t.Fatalf("second run: %v %v", err, verdicts(res))
	}
	// b (mutant survives) and c (red) are never cached, so their mutants and tests run again; a's must not.
	if got := mutantReplays.Load() - replays; got != 1 {
		t.Errorf("second run replayed %d mutants, want only b's", got)
	}
	if got := evidenceBuilds.Load() - builds; got != 1 {
		t.Errorf("second run built %d test binaries, want 1 (b and c rerun)", got)
	}
	// Editing a's mutant to one that survives must not reuse the record that its old mutant was killed.
	surviving := `{"packages/fx/src/a.ts":[{"file":"lib/a.go","old":"return \"A\"","new":"return \"A\""}]}`
	if err := os.WriteFile(filepath.Join(root, mutantsFile), []byte(surviving), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = checkWith(root, true)
	if err != nil || !strings.Contains(verdicts(res)["packages/fx/src/a.ts"], "no listed mutant makes the marker tests fail") {
		t.Fatalf("a's mutant changed to one that survives: %v %v", err, verdicts(res)["packages/fx/src/a.ts"])
	}
}

// TestShardsJudgeEveryRowOnce: with -shard N/M each row is judged by exactly one shard and skipped by the rest, with the verdict the
// unsharded run gives.
func TestShardsJudgeEveryRowOnce(t *testing.T) {
	root := sharedFixture(t)
	want, err := checkWith(root, true)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdicts := verdicts(want)
	judged := map[string]int{}
	for i := range 2 {
		res, err := checkShard(root, true, shardSpec{index: i, total: 2})
		if err != nil || len(res) != 3 {
			t.Fatalf("shard %d: %v %d", i+1, err, len(res))
		}
		for pi, v := range verdicts(res) {
			if v == "skipped" {
				continue
			}
			judged[pi]++
			if v != wantVerdicts[pi] {
				t.Errorf("shard %d judged %s as %q, the whole run says %q", i+1, pi, v, wantVerdicts[pi])
			}
		}
	}
	for pi := range wantVerdicts {
		if judged[pi] != 1 {
			t.Errorf("%s was judged by %d shards, want 1", pi, judged[pi])
		}
	}
}

// TestASkippedRowIsNotAProblem: a row another shard judges is neither a new unproven tick nor a stale baseline entry here.
func TestASkippedRowIsNotAProblem(t *testing.T) {
	root := sharedFixture(t)
	res := []result{
		{Row: row{Pi: "packages/fx/src/a.ts"}, Skipped: true},
		{Row: row{Pi: "packages/fx/src/b.ts"}, Proven: true},
	}
	if err := os.MkdirAll(filepath.Join(root, "test/parity"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, baselineFile), []byte("packages/fx/src/a.ts\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	problems, err := compareBaseline(root, res)
	if err != nil || len(problems) != 0 {
		t.Fatalf("%v %v", err, problems)
	}
}

func TestParseShard(t *testing.T) {
	for in, want := range map[string]shardSpec{"": {}, "1/1": {0, 1}, "2/4": {1, 4}, "4/4": {3, 4}, "64/64": {63, 64}, "01/03": {0, 3}} {
		if got, err := parseShard(in); err != nil || got != want {
			t.Errorf("%q: %+v %v, want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{
		"0/4", "5/4", "1", "a/b", "1/0", "-1/4", "0/0", "1/-3", "-2/-1", // index or count out of range
		"1/65", "65/65", "1/100", "1/4294967296", "1/4294967297", // above the bound, and past what a uint32 holds
		"1/9223372036854775807", "9223372036854775807/9223372036854775807", "1/9223372036854775808", "1/99999999999999999999999", // up to and past an int
		"1/", "/4", "/", " 1/4", "1/4 ", "1/ 4", "+1/4", "1.5/4", "1/4.0", "0x1/0x4", "1e1/4", "1//4", "1/4/4", // not a plain N/M
	} {
		if got, err := parseShard(in); err == nil {
			t.Errorf("%q must be rejected, got %+v", in, got)
		}
	}
}

// TestPartitionRefusesAnOutOfRangeShardCount: partition is only reached with a count parseShard accepted, and refuses any other.
func TestPartitionRefusesAnOutOfRangeShardCount(t *testing.T) {
	for _, total := range []int{0, -1, maxShards + 1, 1 << 40} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("partition accepted %d shards", total)
				}
			}()
			partition([]result{{Row: row{Pi: "x"}}}, total)
		}()
	}
}

// TestPartitionAssignsEveryRowToOneBalancedShard: the plan is a pure function of the derived rows, keeps a package's rows together when
// it fits a shard, spreads a package heavier than a shard's share, and gives each row without an evidence run to exactly one shard.
func TestPartitionAssignsEveryRowToOneBalancedShard(t *testing.T) {
	var res []result
	add := func(pi string, proven bool, tests ...string) {
		res = append(res, result{Row: row{Pi: pi}, Proven: proven, Tests: tests})
	}
	for i := range 40 { // one package far heavier than a shard's share
		add("big"+strconv.Itoa(i), true, "big#T1", "big#T2", "big#T3", "big#T4", "big#T5", "big#T6", "big#T7", "big#T8", "big#T9", "big#T10")
	}
	for _, dir := range []string{"x", "y", "z", "w"} {
		for i := range 5 {
			add(dir+strconv.Itoa(i), true, dir+"#T1", dir+"#T2")
		}
	}
	add("unproven", false)
	add("untested", true)
	for _, total := range []int{1, 2, 3, 4, 7} {
		a, b := partition(res, total), partition(res, total)
		if !slices.Equal(a, b) {
			t.Fatalf("%d shards: the plan is not deterministic", total)
		}
		load := make([]int, total)
		dirs := map[string]map[int]bool{}
		for i, s := range a {
			if s < 0 || s >= total {
				t.Fatalf("%d shards: row %d in shard %d", total, i, s)
			}
			load[s] += rowWeight(res[i])
			if res[i].Proven && len(res[i].Tests) > 0 {
				d := primaryDir(res[i])
				if dirs[d] == nil {
					dirs[d] = map[int]bool{}
				}
				dirs[d][s] = true
			}
		}
		for d, shards := range dirs {
			if d != "big" && len(shards) != 1 {
				t.Errorf("%d shards: package %s, which fits one shard, is split over %d", total, d, len(shards))
			}
		}
		if total > 1 && len(dirs["big"]) < 2 {
			t.Errorf("%d shards: the heavy package was not spread", total)
		}
		hi, lo := slices.Max(load), slices.Min(load)
		if total > 1 && total <= 4 && hi > lo*3/2+20 {
			t.Errorf("%d shards: loads %v are unbalanced", total, load)
		}
	}
}

// TestAChildOfTheEvidenceBinaryHasACoverageDirectory: a test that re-runs its own binary as a helper process (a stdio MCP server fixture
// that exits at startup) must see what it sees under `go test -cover`, where the child inherits GOCOVERDIR. Without it the instrumented
// child warns on stderr as it exits, which a test that asserts on the child's stderr reads as a failure.
func TestAChildOfTheEvidenceBinaryHasACoverageDirectory(t *testing.T) {
	const body = `package lib

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("WIDGET_CHILD") == "1" {
		os.Exit(3)
	}
	os.Exit(m.Run())
}

func TestWidget(t *testing.T) {
	child := exec.Command(os.Args[0], "-test.run=^$")
	child.Env = append(os.Environ(), "WIDGET_CHILD=1")
	out, _ := child.CombinedOutput()
	if strings.Contains(string(out), "GOCOVERDIR") {
		t.Fatalf("the child wrote %q", out)
	}
	if (Widget{Name: "a"}).Hello() != "hello a" {
		t.Fatal("hello")
	}
}
`
	res, err := checkWith(coverageFixture(t, body), true)
	if err != nil || len(res) != 1 || !res[0].Proven {
		t.Fatalf("err=%v res=%+v", err, res)
	}
}
