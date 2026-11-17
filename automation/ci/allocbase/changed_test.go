// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const testModule = "example.test/m"

// testGraph is a small module: agent imports ai, cli imports agent and ai (cli's tests import cmd), ai imports ai/internal/data (which embeds ai/internal/data/models.json) and the cold package ai/cold.
func testGraph() goGraph {
	return goGraph{
		Module: testModule,
		Imports: map[string][]string{
			testModule + "/agent":            {testModule + "/ai"},
			testModule + "/coding/cli":       {testModule + "/agent", testModule + "/ai", testModule + "/cmd"},
			testModule + "/ai":               {testModule + "/ai/internal/data", testModule + "/ai/cold"},
			testModule + "/ai/internal/data": nil,
		},
		Embeds: map[string][]string{
			"ai/internal/data/models.json": {testModule + "/ai/internal/data"},
			"ai/assets/models.json":        {testModule + "/ai"},
			"ai/assets/sub/extra.json":     {testModule + "/ai"},
		},
		TestEmbeds: map[string][]string{"tui/golden/case.txt": {testModule + "/tui"}},
	}
}

var testHot = []string{"./agent", "./ai", "./coding/cli", "./tui"}

// selection runs selectChanged over files with the benchmark files named, and flattens the answer to "package: first reason" strings.
func selection(t *testing.T, files []string, benchmarks []string, mutate func(*selectionInput)) []string {
	t.Helper()
	in := selectionInput{
		Hot:     testHot,
		BaseHot: testHot,
		Files:   files,
		HoldsBenchmark: func(file string) bool {
			return slices.Contains(benchmarks, file)
		},
		Workspace: func() ([]string, error) { return []string{".", "nested"}, nil },
		Graph:     func() (goGraph, error) { return testGraph(), nil },
	}
	if mutate != nil {
		mutate(&in)
	}
	chosen, err := selectChanged(in)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range chosen {
		out = append(out, c.Package+": "+c.Reasons[0])
	}
	return out
}

func TestSelectionOfAChangedBenchmarkFileIsItsPackageOnly(t *testing.T) {
	got := selection(t, []string{"ai/stream_bench_test.go"}, []string{"ai/stream_bench_test.go"}, nil)
	if want := []string{"./ai: benchmark file ai/stream_bench_test.go"}; !slices.Equal(got, want) {
		t.Fatalf("selected %q, want %q: a benchmark file changes its own package's counts, not its importers'", got, want)
	}
}

func TestSelectionIgnoresATestFileWithoutBenchmarks(t *testing.T) {
	if got := selection(t, []string{"ai/stream_test.go", "agent/loop_test.go"}, nil, nil); len(got) != 0 {
		t.Fatalf("selected %q for test files holding no benchmark", got)
	}
}

// A benchmark file the change deletes holds a benchmark at the merge base, so its package is measured: the baseline rows of the deleted benchmarks must go in the same change.
func TestSelectionOfADeletedBenchmarkFile(t *testing.T) {
	got := selection(t, []string{"tui/old_bench_test.go"}, []string{"tui/old_bench_test.go"}, nil)
	if want := []string{"./tui: benchmark file tui/old_bench_test.go"}; !slices.Equal(got, want) {
		t.Fatalf("selected %q, want %q", got, want)
	}
}

func TestSelectionOfAChangedSourceFileIsItsPackageAndTheHotImporters(t *testing.T) {
	got := selection(t, []string{"ai/stream.go"}, nil, nil)
	want := []string{"./agent: imports changed package ai", "./ai: source file ai/stream.go", "./coding/cli: imports changed package ai"}
	if !slices.Equal(got, want) {
		t.Fatalf("selected %q, want %q", got, want)
	}
}

// A change in a package outside the tool's list reaches the hot packages that import it, through the packages between.
func TestSelectionFollowsImportsFromAColdPackage(t *testing.T) {
	got := selection(t, []string{"ai/cold/cold.go"}, nil, nil)
	want := []string{"./agent: imports changed package ai/cold", "./ai: imports changed package ai/cold", "./coding/cli: imports changed package ai/cold"}
	if !slices.Equal(got, want) {
		t.Fatalf("selected %q, want %q", got, want)
	}
}

func TestSelectionOfAnEmbeddedFileFollowsTheEmbeddingPackage(t *testing.T) {
	got := selection(t, []string{"ai/internal/data/models.json"}, nil, nil)
	want := []string{"./agent: imports changed package ai/internal/data", "./ai: imports changed package ai/internal/data", "./coding/cli: imports changed package ai/internal/data"}
	if !slices.Equal(got, want) {
		t.Fatalf("selected %q, want %q", got, want)
	}
}

// A file a hot package embeds below its directory is part of that package's binary: the package is measured, not only what imports it.
func TestSelectionOfAnEmbeddedFileBelowAHotPackageSelectsTheEmbeddingPackage(t *testing.T) {
	for _, file := range []string{"ai/assets/models.json", "ai/assets/sub/extra.json"} {
		got := selection(t, []string{file}, nil, nil)
		want := []string{"./agent: imports changed package ai", "./ai: embedded file " + file, "./coding/cli: imports changed package ai"}
		if !slices.Equal(got, want) {
			t.Fatalf("%s: selected %q, want %q", file, got, want)
		}
	}
}

// A file only a package's tests embed changes that package's tests and benchmarks, and nothing that imports it.
func TestSelectionOfATestEmbeddedFileIsItsPackageOnly(t *testing.T) {
	got := selection(t, []string{"tui/golden/case.txt"}, nil, nil)
	if want := []string{"./tui: embedded file tui/golden/case.txt"}; !slices.Equal(got, want) {
		t.Fatalf("selected %q, want %q", got, want)
	}
}

func TestSelectionOfADataFileOfAHotPackage(t *testing.T) {
	got := selection(t, []string{"tui/theme.json"}, nil, nil)
	if want := []string{"./tui: data file tui/theme.json"}; !slices.Equal(got, want) {
		t.Fatalf("selected %q, want %q", got, want)
	}
}

// A fixture under testdata is read by its package's tests and benchmarks; no other package imports it.
func TestSelectionOfAFixtureIsItsPackageOnly(t *testing.T) {
	got := selection(t, []string{"ai/testdata/sdk/case.json"}, nil, func(in *selectionInput) {
		in.Graph = func() (goGraph, error) { t.Fatal("a fixture needs no import graph"); return goGraph{}, nil }
	})
	if want := []string{"./ai: data file ai/testdata/sdk/case.json"}; !slices.Equal(got, want) {
		t.Fatalf("selected %q, want %q", got, want)
	}
}

// A documentation-only change selects nothing, and never pays for a go list.
func TestSelectionOfDocumentationOnlyChangeIsNothing(t *testing.T) {
	got := selection(t, []string{"README.md", "docs/guide.md", "ai/README.md", "CHANGELOG.md"}, nil, func(in *selectionInput) {
		in.Graph = func() (goGraph, error) {
			t.Fatal("a documentation-only change needs no import graph")
			return goGraph{}, nil
		}
	})
	if len(got) != 0 {
		t.Fatalf("selected %q for documentation only", got)
	}
}

// A file outside every package (CI scripts, workflow files) changes no package and reaches no hot package.
func TestSelectionOfAFileOutsideEveryPackage(t *testing.T) {
	if got := selection(t, []string{".github/workflows/ci.yml", "automation/ci/x.py"}, nil, nil); len(got) != 0 {
		t.Fatalf("selected %q", got)
	}
}

// A benchmark in a package outside the tool's list is not in the nightly's run, so it owes no row.
func TestSelectionLeavesAPackageOutsideTheListAlone(t *testing.T) {
	got := selection(t, []string{"internal/cold/bench_test.go", "internal/cold/x.go"}, []string{"internal/cold/bench_test.go"}, nil)
	if len(got) != 0 {
		t.Fatalf("selected %q for a package outside the tool's list that no hot package imports", got)
	}
}

func TestSelectionOfModuleFilesIsEveryHotPackage(t *testing.T) {
	got := selection(t, []string{"go.sum"}, nil, func(in *selectionInput) {
		in.Graph = func() (goGraph, error) { t.Fatal("go.sum needs no import graph"); return goGraph{}, nil }
	})
	if len(got) != len(testHot) {
		t.Fatalf("selected %q, want every hot package", got)
	}
}

// go.work resolves one build list for every member, so a member's manifest selects every hot package; a go.mod of a module that is no member (a fixture, or a nested directory outside go.work) does not.
func TestSelectionOfAWorkspaceMemberManifestIsEveryHotPackage(t *testing.T) {
	for _, file := range []string{"nested/go.mod", "nested/go.sum"} {
		got := selection(t, []string{file}, nil, nil)
		if len(got) != len(testHot) || got[0] != "./agent: "+file+" changed" {
			t.Fatalf("%s: selected %q, want every hot package", file, got)
		}
	}
	if got := selection(t, []string{"tools/other/go.mod"}, nil, nil); len(got) != 0 {
		t.Fatalf("the go.mod of a module outside go.work selected %q", got)
	}
	got := selection(t, []string{"ai/testdata/mod/go.mod"}, nil, nil)
	if want := []string{"./ai: data file ai/testdata/mod/go.mod"}; !slices.Equal(got, want) {
		t.Fatalf("a fixture module's go.mod selected %q, want %q", got, want)
	}
}

func TestSelectionOfABaselineRowChange(t *testing.T) {
	base := []entry{{"./ai", "BenchmarkA", 100}, {"./agent", "BenchmarkB", 5}, {"./tui", "BenchmarkGone", 7}}
	head := []entry{{"./ai", "BenchmarkA", 90}, {"./agent", "BenchmarkB", 5}, {"./coding/cli", "BenchmarkNew", 12}}
	got := selection(t, []string{"automation/ci/allocbase/baseline.toml"}, nil, func(in *selectionInput) { in.BaseRows, in.Rows = base, head })
	want := []string{"./ai: baseline row BenchmarkA", "./coding/cli: baseline row BenchmarkNew", "./tui: baseline row BenchmarkGone removed"}
	if !slices.Equal(got, want) {
		t.Fatalf("selected %q, want %q", got, want)
	}
}

func TestSelectionOfAPackageNewInTheToolList(t *testing.T) {
	got := selection(t, []string{"automation/ci/allocbase/main.go"}, nil, func(in *selectionInput) { in.BaseHot = []string{"./agent", "./ai", "./coding/cli"} })
	if want := []string{"./tui: new in the tool's package list"}; !slices.Equal(got, want) {
		t.Fatalf("selected %q, want %q", got, want)
	}
}

func TestParseToolPackages(t *testing.T) {
	source := []byte("package main\n\n// packages are the hot paths.\nvar packages = []string{\"./agent\", \"./ai/providers/bedrock\", \"./tui\"}\n\nvar skipped = map[string]string{}\n")
	if got, want := parseToolPackages(source), []string{"./agent", "./ai/providers/bedrock", "./tui"}; !slices.Equal(got, want) {
		t.Fatalf("parsed %q, want %q", got, want)
	}
}

func TestNormalizeImportPathFoldsTestVariants(t *testing.T) {
	for in, want := range map[string]string{
		"m/ai":                  "m/ai",
		"m/ai [m/ai.test]":      "m/ai",
		"m/ai_test [m/ai.test]": "m/ai",
		"m/ai.test":             "m/ai",
		"m/ai_test":             "m/ai",
	} {
		if got := normalizeImportPath(in); got != want {
			t.Errorf("normalizeImportPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// git runs git in dir with no configuration but its own flags, so the host's signing and hooks never apply.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-c", "core.hooksPath=" + os.DevNull}, args...)...)
	command.Dir = dir
	command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull, "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test")
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The whole path, on a throwaway module: the merge base with a ref, the committed and the uncommitted changes, the rows at the merge base, a real go list. a is hot and imports b; c is hot and imports nothing.
func TestChangedPackagesOnARepository(t *testing.T) {
	root := t.TempDir()
	hot := []string{"./a", "./c"}
	baseFiles := map[string]string{
		"go.mod":                                "module example.test/m\n\ngo 1.24\n",
		"go.work":                               "go 1.24\n\nuse (\n\t.\n\t./nested\n)\n",
		"nested/go.mod":                         "module example.test/m/nested\n\ngo 1.24\n",
		"nested/n/n.go":                         "package n\n",
		"a/a.go":                                "package a\n\nimport (\n\t_ \"embed\"\n\t_ \"example.test/m/b\"\n\t_ \"example.test/m/nested/n\"\n)\n\n//go:embed assets/m.json\nvar models string\n",
		"a/assets/m.json":                       "{}\n",
		"c/data/fixture.txt":                    "one\n",
		"a/a_test.go":                           "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
		"b/b.go":                                "package b\n",
		"c/c.go":                                "package c\n",
		"c/c_test.go":                           "package c\n\nimport (\n\t_ \"embed\"\n\t\"testing\"\n)\n\n//go:embed data/fixture.txt\nvar fixture string\n\nfunc BenchmarkC(b *testing.B) {}\n",
		"README.md":                             "docs\n",
		"automation/ci/allocbase/main.go":       "package main\n\nvar packages = []string{\"./a\", \"./c\"}\n",
		"automation/ci/allocbase/baseline.toml": "[[benchmark]]\npackage = \"./c\"\nbenchmark = \"BenchmarkC\"\nallocs = 3\n",
	}
	writeTree(t, root, baseFiles)
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "base")
	git(t, root, "checkout", "-q", "-b", "topic")
	rows := []entry{{"./c", "BenchmarkC", 3}}

	run := func(committedOnly bool) (string, int) {
		t.Helper()
		chosen, files, err := changedPackages(root, "main", committedOnly, hot, rows)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, c := range chosen {
			names = append(names, c.Package+": "+c.Reasons[0])
		}
		return strings.Join(names, " | "), files
	}

	if got, files := run(false); got != "" || files != 0 {
		t.Fatalf("an unchanged branch selected %q from %d files", got, files)
	}
	writeTree(t, root, map[string]string{"README.md": "more docs\n"})
	git(t, root, "commit", "-q", "-am", "docs")
	if got, files := run(false); got != "" || files != 1 {
		t.Fatalf("a documentation commit selected %q from %d files", got, files)
	}
	// A workspace lists every module for `go list -m`, and a package of a nested module is still a package of the repository.
	writeTree(t, root, map[string]string{"nested/n/n.go": "package n\n\nvar N = 1\n"})
	git(t, root, "commit", "-q", "-am", "nested")
	if got, want := func() string { g, _ := run(true); return g }(), "./a: imports changed package nested/n"; got != want {
		t.Fatalf("a change in the nested module selected %q, want %q", got, want)
	}
	writeTree(t, root, map[string]string{"b/b.go": "package b\n\nvar B = 1\n"})
	git(t, root, "commit", "-q", "-am", "b")
	if got, want := func() string { g, _ := run(true); return g }(), "./a: imports changed package b"; got != want {
		t.Fatalf("a change in b selected %q, want %q", got, want)
	}
	// An uncommitted benchmark file counts unless the run reads the commits only.
	writeTree(t, root, map[string]string{"c/d_test.go": "package c\n\nimport \"testing\"\n\nfunc BenchmarkD(b *testing.B) {}\n"})
	if got, _ := run(true); got != "./a: imports changed package b" {
		t.Fatalf("-committed selected %q from an uncommitted file", got)
	}
	if got, _ := run(false); got != "./a: imports changed package b | ./c: benchmark file c/d_test.go" {
		t.Fatalf("the working tree selected %q", got)
	}
	// A file a hot package embeds below its directory selects that package; a file only its tests embed selects it too.
	writeTree(t, root, map[string]string{"a/assets/m.json": "{\"a\":1}\n"})
	git(t, root, "commit", "-q", "-am", "embedded asset")
	if got, want := func() string { g, _ := run(true); return g }(), "./a: embedded file a/assets/m.json"; got != want {
		t.Fatalf("a changed embedded file selected %q, want %q", got, want)
	}
	writeTree(t, root, map[string]string{"c/data/fixture.txt": "two\n"})
	git(t, root, "commit", "-q", "-am", "test embedded file")
	if got, want := func() string { g, _ := run(true); return g }(), "./a: embedded file a/assets/m.json | ./c: embedded file c/data/fixture.txt"; got != want {
		t.Fatalf("a changed test-embedded file selected %q, want %q", got, want)
	}
	// A go.work member's manifest changes every package; a go.mod outside go.work changes none.
	writeTree(t, root, map[string]string{"nested/go.mod": "module example.test/m/nested\n\ngo 1.24\n\n// edit\n", "fixture/go.mod": "module example.test/fixture\n\ngo 1.24\n"})
	git(t, root, "add", "nested/go.mod", "fixture/go.mod")
	git(t, root, "commit", "-q", "-m", "manifests")
	chosen, _, err := changedPackages(root, "main", true, hot, rows)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chosen {
		if !slices.Contains(c.Reasons, "nested/go.mod changed") {
			t.Errorf("%s: reasons %q lack the nested member's go.mod", c.Package, c.Reasons)
		}
		if slices.Contains(c.Reasons, "fixture/go.mod changed") {
			t.Errorf("%s: selected by the go.mod of a module outside go.work", c.Package)
		}
	}
	if _, _, err := changedPackages(root, "no-such-ref", false, hot, rows); err == nil {
		t.Fatal("a ref that names no commit selected without an error")
	}
}
