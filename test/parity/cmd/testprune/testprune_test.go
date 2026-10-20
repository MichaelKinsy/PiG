package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestIsGoldenFile(t *testing.T) {
	for path, want := range map[string]bool{
		"tui/testdata/goldens/x.txt":                           true,
		"coding/piglogin/testdata/head-pink.golden":            true,
		"mcp/testdata/upstream-golden.json":                    true,
		"pkg/testdata/__snapshots__/a.snap":                    true,
		"pkg/golden_test.go":                                   false,
		"pkg/testdata/golden-gen.mjs":                          false,
		"pkg/golden.json":                                      false,
		"x/node_modules/y/testdata/golden.json":                false,
		"test/parity/scenarios/f/01-event-snapshots.toml":      false,
		"third_party/jsonschema/testdata/lazy_meta_golden.txt": false,
		"pkg/testdata/corpus.txt":                              false,
	} {
		if got := isGoldenFile(path); got != want {
			t.Errorf("isGoldenFile(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestCheckProvenanceRejectsUndeclaredAndIncompleteGoldens(t *testing.T) {
	tracked := []string{"a/testdata/x-golden.json", "a/testdata/gen.mjs", "b/testdata/y.golden", "c/testdata/z.golden"}
	known := func(name string) bool { return name == "TestRule" }
	cases := []struct {
		name string
		prov provenance
		want string
	}{
		{"undeclared", provenance{}, "x-golden.json: golden or snapshot file without provenance"},
		{"pi without generator", provenance{Golden: []goldenEntry{{Paths: []string{"a/testdata/x-golden.json"}, Owner: "pi"}}}, "names the generator"},
		{"pi with missing generator", provenance{Golden: []goldenEntry{{Paths: []string{"a/testdata/x-golden.json"}, Owner: "pi", Generator: "a/nope.mjs"}}}, "is not a tracked file"},
		{"pig without invariant", provenance{Golden: []goldenEntry{{Paths: []string{"b/testdata/y.golden"}, Owner: "pig"}}}, "names the invariant test"},
		{"pig with unknown invariant", provenance{Golden: []goldenEntry{{Paths: []string{"b/testdata/y.golden"}, Owner: "pig", Invariant: "TestMissing", Why: "w"}}}, "does not exist"},
		{"entry matching nothing", provenance{Golden: []goldenEntry{{Paths: []string{"zzz/*"}, Owner: "pi", Generator: "a/testdata/gen.mjs"}}}, "matches no tracked file"},
		{"bad owner", provenance{Golden: []goldenEntry{{Paths: []string{"c/testdata/z.golden"}, Owner: "me"}}}, "owner must be pi or pig"},
	}
	for _, tc := range cases {
		problems := checkProvenance("", tracked, tc.prov, known)
		if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
			t.Errorf("%s: problems %q lack %q", tc.name, problems, tc.want)
		}
	}
	complete := provenance{Golden: []goldenEntry{
		{Paths: []string{"a/testdata/x-golden.json"}, Owner: "pi", Generator: "a/testdata/gen.mjs"},
		{Paths: []string{"b/testdata/*.golden", "c/testdata/z.golden"}, Owner: "pig", Invariant: "TestRule", Why: "w"},
	}}
	if problems := checkProvenance("", tracked, complete, known); len(problems) != 0 {
		t.Errorf("declared goldens reported: %q", problems)
	}
}

func TestValueEditsCountsChangedLiteralsNotRenames(t *testing.T) {
	lits := func(line string) map[string]int {
		m := map[string]int{}
		for _, l := range literalRE.FindAllString(line, -1) {
			m[l]++
		}
		return m
	}
	if got := valueEdits(lits(`if got != "old value" {`), lits(`if result != "new value" {`)); got != 1 {
		t.Errorf("changed expectation counted %d, want 1", got)
	}
	if got := valueEdits(lits(`if got != "same value" {`), lits(`if result != "same value" {`)); got != 0 {
		t.Errorf("a renamed identifier counted %d, want 0", got)
	}
	if got := valueEdits(nil, lits(`want := "added"`)); got != 0 {
		t.Errorf("a pure addition counted %d, want 0", got)
	}
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckCoEditsRequiresAPiCitation(t *testing.T) {
	piNames := map[string]bool{"a.ts": true, "agent-loop.test.ts": true}
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "pkg/a.go", "package pkg\n\nfunc A() string { return \"first\" }\n")
	write(t, dir, "pkg/a_test.go", "package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n\tif A() != \"first\" {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "base")
	base := gitIn(t, dir, "rev-parse", "HEAD")

	edit := func(value, message string) {
		write(t, dir, "pkg/a.go", "package pkg\n\nfunc A() string { return \""+value+"\" }\n")
		write(t, dir, "pkg/a_test.go", "package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n\tif A() != \""+value+"\" {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
		gitIn(t, dir, "add", ".")
		gitIn(t, dir, "commit", "-q", "-m", message)
	}

	edit("second", "make the test pass")
	problems, err := checkCoEdits(dir, base+"..HEAD", time.Time{}, piNames, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "pkg/a_test.go") || !strings.Contains(problems[0], "Pi-Behavior-Change") {
		t.Fatalf("an expectation edited with its code passed review: %q", problems)
	}

	if problems, err := checkCoEdits(dir, base+"..HEAD", time.Now().Add(time.Hour), piNames, nil); err != nil || len(problems) != 0 {
		t.Fatalf("a commit authored before the ratchet date was reviewed: %v %q", err, problems)
	}

	mid := gitIn(t, dir, "rev-parse", "HEAD")
	edit("third", "follow Pi\n\nPi-Behavior-Change: packages/coding-agent/src/core/a.ts:42")
	problems, err = checkCoEdits(dir, mid+"..HEAD", time.Time{}, piNames, nil)
	if err != nil || len(problems) != 0 {
		t.Fatalf("a Pi-cited change was rejected: %v %q", err, problems)
	}

	tip := gitIn(t, dir, "rev-parse", "HEAD")
	write(t, dir, "pkg/a_test.go", "package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n\tif a := A(); a != \"third\" {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	write(t, dir, "pkg/a.go", "package pkg\n\nfunc A() string { return \"third\" }\n\nfunc unrelated() {}\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "refactor")
	problems, err = checkCoEdits(dir, tip+"..HEAD", time.Time{}, piNames, nil)
	if err != nil || len(problems) != 0 {
		t.Fatalf("a refactor that keeps every expected value was rejected: %v %q", err, problems)
	}

	// A trailer or a test-line citation must name a Pi file: a PiG TypeScript file or an invented name is not one.
	for i, message := range []string{"follow nobody\n\nPi-Behavior-Change: extensions/sdk-ts/src/index.ts:3", "follow nobody\n\nPi-Behavior-Change: invented.ts:1"} {
		before := gitIn(t, dir, "rev-parse", "HEAD")
		edit(fmt.Sprintf("uncited-%d", i), message)
		if problems, err := checkCoEdits(dir, before+"..HEAD", time.Time{}, piNames, nil); err != nil || len(problems) != 1 {
			t.Fatalf("%q passed as a Pi citation: %v %q", message, err, problems)
		}
	}
	editWithComment := func(value, comment string) []string {
		before := gitIn(t, dir, "rev-parse", "HEAD")
		write(t, dir, "pkg/a.go", "package pkg\n\nfunc A() string { return \""+value+"\" }\n")
		write(t, dir, "pkg/a_test.go", "package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n\t// "+comment+"\n\tif A() != \""+value+"\" {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
		gitIn(t, dir, "add", ".")
		gitIn(t, dir, "commit", "-q", "-m", "change "+value)
		problems, err := checkCoEdits(dir, before+"..HEAD", time.Time{}, piNames, nil)
		if err != nil {
			t.Fatal(err)
		}
		return problems
	}
	if problems := editWithComment("fourth", "Pi asserts this in agent-loop.test.ts:12"); len(problems) != 0 {
		t.Fatalf("a Pi test line cited on a changed test line was rejected: %q", problems)
	}
	if problems := editWithComment("fifth", "see helper.ts:4"); len(problems) != 1 {
		t.Fatalf("a citation of a file Pi does not have passed: %q", problems)
	}
	// A literal that names a path is not an expected value: moving code changes it without changing behavior.
	before := gitIn(t, dir, "rev-parse", "HEAD")
	write(t, dir, "pkg/a.go", "package pkg\n\nfunc A() string { return \"fifth\" }\n\nconst where = \"pkg/new/place\"\n")
	write(t, dir, "pkg/a_test.go", "package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n\t// see helper.ts:4\n\tif A() != \"fifth\" || where != \"pkg/new/place\" {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "add a path")
	write(t, dir, "pkg/a.go", "package pkg\n\nfunc A() string { return \"fifth\" }\n\nconst where = \"pkg/moved/place\"\n")
	write(t, dir, "pkg/a_test.go", "package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n\t// see helper.ts:4\n\tif A() != \"fifth\" || where != \"pkg/moved/place\" {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "move a path")
	if problems, err := checkCoEdits(dir, before+"..HEAD", time.Time{}, piNames, nil); err != nil || len(problems) != 0 {
		t.Fatalf("a moved path counted as a changed expected value: %v %q", err, problems)
	}
}

func TestLostBlocksNeedsAnotherCoveringRun(t *testing.T) {
	base := profile{"a.go:1.1,2.2 1": true, "a.go:3.1,4.2 1": true, "a.go:5.1,6.2 1": false}
	pruned := profile{"a.go:1.1,2.2 1": true, "a.go:3.1,4.2 1": false}
	lost := lostBlocks(base, pruned)
	if len(lost) != 1 || lost[0] != "a.go:3.1,4.2 1" {
		t.Fatalf("lost = %q, want only the block the pruned run stopped covering", lost)
	}
	if lost := lostBlocks(base, pruned, profile{"a.go:3.1,4.2 1": true}); len(lost) != 0 {
		t.Fatalf("a block covered by another suite was reported lost: %q", lost)
	}
}

func TestBlockRangesMapsImportPathsToFiles(t *testing.T) {
	r := blockRanges("github.com/MichaelKinsy/PiG/tui/a.go:10.2,12.5 3\nexample.com/other/b.go:1.1,2.2 1\n")
	if got := r["tui/a.go"]; len(got) != 1 || got[0] != (lineRange{10, 12}) {
		t.Fatalf("ranges = %v", r)
	}
	if len(r) != 1 {
		t.Fatalf("a foreign import path was mapped: %v", r)
	}
}

func TestClassifyRecognizesTheClasses(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "x_test.go", `package x

import "testing"

func TestRunsPi(t *testing.T) {
	out := runPiOracle(t)
	if out != "x" {
		t.Fatal(out)
	}
}

func TestGoldenFile(t *testing.T) {
	checkGolden(t, "name", render())
}

func TestShape(t *testing.T) {
	if _, ok := reflect.TypeFor[T]().MethodByName("M"); !ok {
		t.Fatal("no M")
	}
}

func TestExample(t *testing.T) {
	if f(2) != 4 {
		t.Fatal("f")
	}
}

func FuzzParse(f *testing.F) {}
`)
	funcs, err := inventoryFiles(dir, []string{"x_test.go"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Class{
		"TestRunsPi": ClassOracle, "TestGoldenFile": ClassSelfRef, "TestShape": ClassShape,
		"TestExample": ClassUnclassified, "FuzzParse": ClassInvariant,
	}
	for _, f := range funcs {
		if f.Class != want[f.Name] {
			t.Errorf("%s classified %s, want %s", f.Name, f.Class, want[f.Name])
		}
		delete(want, f.Name)
	}
	if len(want) != 0 {
		t.Errorf("functions not inventoried: %v", want)
	}
}

func TestCheckProvenanceCoversTheDataOfAGoldenWritingTest(t *testing.T) {
	root := t.TempDir()
	write(t, root, "w/w_test.go", "package w\n\nimport \"os\"\n\nvar _ = os.Getenv(\"UPDATE_GOLDEN\")\n")
	tracked := []string{"w/w_test.go", "w/testdata/case.in", "w/testdata/case.out", "w/testdata/helper.go", "z/testdata/other.txt"}
	known := func(string) bool { return true }
	problems := strings.Join(checkProvenance(root, tracked, provenance{}, known), "\n")
	for _, want := range []string{"w/testdata/case.in", "w/testdata/case.out"} {
		if !strings.Contains(problems, want) {
			t.Errorf("%s in a golden-writing directory passed unchecked: %q", want, problems)
		}
	}
	for _, unwanted := range []string{"helper.go", "z/testdata/other.txt"} {
		if strings.Contains(problems, unwanted) {
			t.Errorf("%s was reported: %q", unwanted, problems)
		}
	}
	prov := provenance{
		Golden: []goldenEntry{{Paths: []string{"w/testdata/*.out"}, Owner: "pig", Invariant: "TestRule", Why: "pins bytes"}},
		Input:  []inputEntry{{Paths: []string{"w/testdata/*.in"}}},
	}
	if problems := checkProvenance(root, tracked, prov, known); len(problems) != 0 {
		t.Errorf("declared files reported: %q", problems)
	}
}

func TestMutantsForListsOperatorConditionConstantAndBoolSites(t *testing.T) {
	src := []byte("package p\n\nfunc f(a, b int) bool {\n\tif a == b && a > 2 {\n\t\treturn true\n\t}\n\treturn false\n}\n")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, m := range mutantsFor(fset, "p.go", src, file, nil) {
		got[m.ID] = m.Repl
	}
	for id, repl := range map[string]string{
		"p.go:4:7:op==":  "!=",
		"p.go:4:12:op&&": "||",
		"p.go:4:17:op>":  "<=",
		"p.go:4:19:int":  "3",
		"p.go:5:10:bool": "false",
		"p.go:7:9:bool":  "true",
	} {
		if got[id] != repl {
			t.Errorf("mutant %s = %q, want %q (all: %v)", id, got[id], repl, got)
		}
	}
	if repl := got["p.go:4:5:negate"]; repl != "!(a == b && a > 2)" {
		t.Errorf("the if condition mutant is %q", repl)
	}
	if lines := mutantsFor(fset, "p.go", src, file, []lineRange{{7, 7}}); len(lines) != 1 {
		t.Errorf("restricting to line 7 left %d mutants, want the one return", len(lines))
	}
}

func TestMutCompareRefusesUnpairedSamples(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "before.tsv", "a.go:1:1:op==\tkilled\nb.go:2:2:negate\tsurvived\n")
	write(t, dir, "same.tsv", "a.go:1:1:op==\tkilled\nb.go:2:2:negate\tsurvived\n")
	write(t, dir, "weaker.tsv", "a.go:1:1:op==\tsurvived\nb.go:2:2:negate\tsurvived\n")
	write(t, dir, "partial.tsv", "a.go:1:1:op==\tkilled\n")
	b := filepath.Join(dir, "before.tsv")
	if err := runMutantCompare([]string{b, filepath.Join(dir, "same.tsv")}); err != nil {
		t.Errorf("equal kill sets rejected: %v", err)
	}
	if err := runMutantCompare([]string{b, filepath.Join(dir, "weaker.tsv")}); err == nil || !strings.Contains(err.Error(), "survive after") {
		t.Errorf("a killed mutant that survives was accepted: %v", err)
	}
	if err := runMutantCompare([]string{b, filepath.Join(dir, "partial.tsv")}); err == nil || !strings.Contains(err.Error(), "not paired") {
		t.Errorf("an unpaired sample was accepted: %v", err)
	}
}

// The Pi file names come from the tracked inventories: PORT_MAP's sources and the test mapping's tests.
func TestPiSourceNamesReadsTheTrackedInventories(t *testing.T) {
	names, err := piSourceNames(testenv.ModuleRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"agent-loop.ts", "agent-loop.test.ts", "interactive-mode.ts"} {
		if !names[want] {
			t.Errorf("%s is not a Pi file name", want)
		}
	}
	if names["index.d.ts"] || names["helper.ts"] {
		t.Errorf("names hold a file Pi does not have")
	}
	if _, err := piSourceNames(t.TempDir()); err == nil {
		t.Error("an empty inventory produced no error")
	}
}

// A test whose blocks another test or the journeys also reach is redundant; the
// last cover of a block never is.
func TestSubsumedCountsJourneyCoverageAndKeepsTheLastCover(t *testing.T) {
	all := []testBlocks{
		{name: "TestA", blocks: map[string]bool{"x": true, "y": true}},
		{name: "TestB", blocks: map[string]bool{"y": true}},
		{name: "TestC", blocks: map[string]bool{"z": true}},
	}
	any := func(string) bool { return true }
	if got := subsumed(all, nil, any); !slices.Equal(got, []string{"TestB"}) {
		t.Fatalf("without journeys got %v, want only TestB (A is the last cover of x, C of z)", got)
	}
	if got := subsumed(all, map[string]bool{"z": true}, any); !slices.Equal(got, []string{"TestB", "TestC"}) {
		t.Fatalf("with journeys covering z got %v, want TestB and TestC", got)
	}
	if got := subsumed(all, map[string]bool{"z": true, "x": true, "y": true}, any); !slices.Equal(got, []string{"TestA", "TestB", "TestC"}) {
		t.Fatalf("with journeys covering everything got %v", got)
	}
}

// A journey profile lists every block of the module, reached or not. Only a
// reached block covers anything; counting the unreached ones would make nearly
// every test a deletion candidate.
func TestSubsumedIgnoresBlocksTheJourneysDidNotReach(t *testing.T) {
	all := []testBlocks{
		{name: "TestA", blocks: map[string]bool{"x": true}},
		{name: "TestB", blocks: map[string]bool{"y": true}},
	}
	keep := func(string) bool { return true }
	if got := subsumed(all, map[string]bool{"x": false, "y": true}, keep); !slices.Equal(got, []string{"TestB"}) {
		t.Fatalf("got %v, want only TestB (the journeys did not reach x)", got)
	}
	if got := notInJourneys(map[string]bool{"x": true, "y": true, "a": true}, map[string]bool{"x": false, "y": true}); !slices.Equal(got, []string{"a", "x"}) {
		t.Fatalf("notInJourneys = %v, want [a x]", got)
	}
}

func TestCandidateFilterHonoursProtectedAndOnlyClasses(t *testing.T) {
	classOf := map[string]Class{
		"TestShape":       ClassShape,
		"TestExample":     ClassUnclassified,
		"TestCited":       ClassPiCited,
		"TestAbortsEarly": ClassUnclassified,
	}
	protected := classSet("A-oracle,A-cited,B-invariant")
	all := candidateFilter(classOf, protected, nil)
	for name, want := range map[string]bool{"TestShape": true, "TestExample": true, "TestCited": false, "TestAbortsEarly": false} {
		if got := all(name); got != want {
			t.Errorf("without -only-classes, %s candidate = %v, want %v", name, got, want)
		}
	}
	shapes := candidateFilter(classOf, protected, classSet("D-shape"))
	for name, want := range map[string]bool{"TestShape": true, "TestExample": false, "TestCited": false} {
		if got := shapes(name); got != want {
			t.Errorf("with -only-classes D-shape, %s candidate = %v, want %v", name, got, want)
		}
	}
	if got := classSet(""); len(got) != 0 {
		t.Errorf("classSet(\"\") = %v, want empty", got)
	}
}

// A test with no block in its package (a reflection guard) is never reported
// as covered by the journeys, and neither is a protected test.
func TestJourneyVerdictNeverStandsInForGuardsOrProtectedTests(t *testing.T) {
	protected := classSet("A-oracle,A-cited,B-invariant")
	cases := []struct {
		name            string
		class           Class
		blocks, missing int
		want            string
	}{
		{"TestReflectGuard", ClassShape, 0, 0, "no_in_package_coverage"},
		{"TestCitesPi", ClassPiCited, 3, 0, "protected"},
		{"TestCancelStops", ClassUnclassified, 3, 0, "protected"},
		{"TestHelper", ClassUnclassified, 3, 0, "journeys_cover_all_blocks"},
		{"TestHelperEdge", ClassUnclassified, 3, 1, "keeps_blocks"},
	}
	for _, c := range cases {
		if got := journeyVerdict(c.name, c.class, c.blocks, c.missing, protected); got != c.want {
			t.Errorf("%s: verdict %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCheckCoEditsLedger(t *testing.T) {
	piNames := map[string]bool{"a.ts": true}
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "pkg/a.go", "package pkg\n\nfunc A() string { return \"first\" }\n")
	write(t, dir, "pkg/a_test.go", "package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n\tif A() != \"first\" {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "base")
	base := gitIn(t, dir, "rev-parse", "HEAD")
	write(t, dir, "pkg/a.go", "package pkg\n\nfunc A() string { return \"second\" }\n")
	write(t, dir, "pkg/a_test.go", "package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n\tif A() != \"second\" {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "change")
	tip := gitIn(t, dir, "rev-parse", "HEAD")
	run := func(l ...reviewEntry) []string {
		p, err := checkCoEdits(dir, base+"..HEAD", time.Time{}, piNames, l)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	good := reviewEntry{Path: "pkg/a_test.go", Commit: tip[:10], Class: "CITE", Cite: "packages/x/a.ts:12", Reason: "r"}
	if p := run(good); len(p) != 0 {
		t.Fatalf("matching entry rejected: %v", p)
	}
	if p := run(); len(p) != 1 {
		t.Fatalf("no entry must leave the finding: %v", p)
	}
	other := good
	other.Commit = base[:10]
	if p := run(other); len(p) != 1 || strings.Contains(p[0], "stale") {
		t.Fatalf("entry for another commit must not apply and, outside the range, must not fail: %v", p)
	}
	other = good
	other.Path = "pkg/b_test.go"
	if p := run(other); len(p) != 2 {
		t.Fatalf("entry for another path must not apply: %v", p)
	}
	bad := good
	bad.Cite = "packages/x/unknown.ts:12"
	if p := run(bad); len(p) != 1 || !strings.Contains(p[0], "does not name a Pi file") {
		t.Fatalf("unknown Pi file accepted: %v", p)
	}
	if p := run(good, reviewEntry{Path: "pkg/gone_test.go", Commit: tip[:10], Class: "NOBEHAVIOR", Reason: "r"}); len(p) != 1 || !strings.Contains(p[0], "stale") {
		t.Fatalf("stale entry accepted: %v", p)
	}
}

func TestLedgerSurvivesSquashMerge(t *testing.T) {
	piNames := map[string]bool{"a.ts": true}
	const src = "package pkg\n\nfunc A() string { return \"%s\" }\n"
	const tst = "package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n\tif A() != \"%s\" {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n"
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "pkg/a.go", fmt.Sprintf(src, "first"))
	write(t, dir, "pkg/a_test.go", fmt.Sprintf(tst, "first"))
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "base")
	base := gitIn(t, dir, "rev-parse", "HEAD")

	gitIn(t, dir, "checkout", "-q", "-b", "feature")
	write(t, dir, "pkg/a.go", fmt.Sprintf(src, "second"))
	write(t, dir, "pkg/a_test.go", fmt.Sprintf(tst, "second"))
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "change")
	tip := gitIn(t, dir, "rev-parse", "HEAD")
	entry := reviewEntry{Path: "pkg/a_test.go", Commit: tip[:10], Class: "CITE", Cite: "packages/x/a.ts:12", Reason: "r"}

	lint := func(rangeSpec string) (problems, prunable []string) {
		t.Helper()
		problems, err := checkCoEdits(dir, rangeSpec, time.Time{}, piNames, []reviewEntry{entry})
		if err != nil {
			t.Fatal(err)
		}
		prunable, err = prunableEntries(dir, rangeSpec, []reviewEntry{entry})
		if err != nil {
			t.Fatal(err)
		}
		return problems, prunable
	}

	if p, pr := lint(base + "..HEAD"); len(p) != 0 || len(pr) != 0 {
		t.Fatalf("branch with its reviewed entry: problems %v prunable %v", p, pr)
	}

	// Squash merge: a new commit on main carries the change; the reviewed SHA is no longer reachable.
	gitIn(t, dir, "checkout", "-q", "main")
	gitIn(t, dir, "merge", "-q", "--squash", "feature")
	gitIn(t, dir, "commit", "-q", "-m", "change (#1)")
	gitIn(t, dir, "branch", "-q", "-D", "feature")
	squashed := gitIn(t, dir, "rev-parse", "HEAD")
	gitIn(t, dir, "reflog", "expire", "--expire=now", "--all")
	gitIn(t, dir, "gc", "-q", "--prune=now")

	if p, pr := lint("HEAD..HEAD"); len(p) != 0 || len(pr) != 1 || !strings.Contains(pr[0], tip[:10]) || !strings.Contains(pr[0], "pkg/a_test.go") {
		t.Fatalf("main after squash: problems %v prunable %v", p, pr)
	}

	gitIn(t, dir, "checkout", "-q", "-b", "later")
	write(t, dir, "README.md", "later\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "unrelated")
	if p, pr := lint(squashed + "..HEAD"); len(p) != 0 || len(pr) != 1 {
		t.Fatalf("later PR after squash: problems %v prunable %v", p, pr)
	}

	// An entry whose commit is in range and matches no finding still fails.
	entry = reviewEntry{Path: "README.md", Commit: gitIn(t, dir, "rev-parse", "HEAD")[:10], Class: "CITE", Cite: "packages/x/a.ts:12", Reason: "r"}
	if p, pr := lint(squashed + "..HEAD"); len(p) != 1 || !strings.Contains(p[0], "stale") || len(pr) != 0 {
		t.Fatalf("in-range entry matching nothing: problems %v prunable %v", p, pr)
	}
}
