package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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
