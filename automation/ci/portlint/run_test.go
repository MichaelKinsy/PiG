// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBaselineRoundTripAndRatchet(t *testing.T) {
	hits := []Hit{
		{Check: "regexfold", File: "a/a.go", Line: 3, Func: "f", Snippet: `re := regexp.MustCompile("(?i)x")`},
		{Check: "regexfold", File: "a/a.go", Line: 9, Func: "f", Snippet: `re := regexp.MustCompile("(?i)x")`},
		{Check: "clock", File: "b/b.go", Line: 4, Func: "T.g", Snippet: "time.Now()"},
	}
	path := filepath.Join(t.TempDir(), "baseline.toml")
	if err := os.WriteFile(path, []byte(renderBaseline(hits)), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := loadBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(base) != 2 {
		t.Fatalf("baseline entries = %d, want 2 (identical hits share one counted entry)", len(base))
	}
	if unknown, stale := compare(hits, base); len(unknown) != 0 || len(stale) != 0 {
		t.Fatalf("unknown = %v, stale = %v; the baseline must cover exactly its own hits", unknown, stale)
	}
	// A third identical hit exceeds the count: the ratchet rejects it.
	more := append(append([]Hit{}, hits...), hits[0])
	if unknown, _ := compare(more, base); len(unknown) != 1 {
		t.Fatalf("unknown = %v, want the extra copy", unknown)
	}
	// A fixed hit leaves a stale entry that fails until it is deleted.
	if _, stale := compare(hits[:2], base); len(stale) != 1 || !strings.Contains(stale[0], "clock") {
		t.Fatalf("stale = %v, want the fixed clock entry", stale)
	}
	// Moving a line does not churn the baseline: the key ignores line numbers.
	moved := []Hit{{Check: "clock", File: "b/b.go", Line: 400, Func: "T.g", Snippet: "time.Now()"}, hits[0], hits[1]}
	if unknown, stale := compare(moved, base); len(unknown) != 0 || len(stale) != 0 {
		t.Fatalf("a moved line churned the baseline: %v %v", unknown, stale)
	}
}

func TestBaselineRejectsUnknownCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.toml")
	if err := os.WriteFile(path, []byte("[[hit]]\ncheck = \"nosuch\"\nfile = \"a.go\"\nfunc = \"f\"\nsnippet = \"x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadBaseline(path); err == nil || !strings.Contains(err.Error(), "unknown check") {
		t.Fatalf("err = %v, want unknown check", err)
	}
}

// The driver loads a module, runs every analyzer and honors the allow marker end to end.
func TestScanFindsAndAllows(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/m\n\ngo 1.26\n")
	write("m.go", `package m

import "regexp"

var bad = regexp.MustCompile("(?i)x")

//portlint:allow regexfold ASCII-only pattern
var ok = regexp.MustCompile("(?i)y")
`)
	hits, err := scan(dir, "", []string{"./..."})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Check != "regexfold" || hits[0].Line != 5 || hits[0].Func != "(decl)" {
		t.Fatalf("hits = %+v, want one regexfold hit on line 5", hits)
	}
}

// A scan loads the module a batch of packages at a time, at several batches at once, and finds what one load of the whole module finds: the
// plain package, its internal test variant and its external test package all report, once each, wherever the batch edges fall.
func TestScanBatchesFindWhatOneBatchFinds(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/m\n\ngo 1.26\n")
	for _, pkg := range []string{"a", "b", "c", "d", "e"} {
		write(pkg+"/"+pkg+".go", "package "+pkg+"\n\nimport \"regexp\"\n\nvar Bad = regexp.MustCompile(\"(?i)prod\")\n")
	}
	write("a/a_test.go", "package a\n\nimport \"regexp\"\n\nvar inTest = regexp.MustCompile(\"(?i)internal\")\n")
	write("b/b_test.go", "package b_test\n\nimport \"regexp\"\n\nvar outTest = regexp.MustCompile(\"(?i)external\")\n")
	oldBatch, oldWorkers := batchSize, scanWorkers
	t.Cleanup(func() { batchSize, scanWorkers = oldBatch, oldWorkers })

	batchSize, scanWorkers = 100, 1
	want, err := scan(dir, "", []string{"./..."})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]int{}
	for _, h := range want {
		files[h.File]++
	}
	for _, f := range []string{"a/a.go", "a/a_test.go", "b/b.go", "b/b_test.go", "c/c.go", "d/d.go", "e/e.go"} {
		if files[f] != 1 {
			t.Fatalf("one batch: %d hits in %s, want 1; hits = %+v", files[f], f, want)
		}
	}
	if len(want) != 7 {
		t.Fatalf("one batch: %d hits, want 7: %+v", len(want), want)
	}
	for _, c := range []struct{ batch, workers int }{{1, 1}, {2, 3}, {3, 2}, {4, 8}} {
		batchSize, scanWorkers = c.batch, c.workers
		got, err := scan(dir, "", []string{"./..."})
		if err != nil {
			t.Fatalf("batch %d, workers %d: %v", c.batch, c.workers, err)
		}
		if !slices.Equal(got, want) {
			t.Errorf("batch %d, workers %d: hits = %+v, want %+v", c.batch, c.workers, got, want)
		}
	}
}

// A package that does not load fails the default scan, whichever batch it falls in.
func TestScanBatchesReportALoadError(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":        "module example.com/m\n\ngo 1.26\n",
		"a/a.go":        "package a\n",
		"b/b.go":        "package b\n\nvar x int = \"not an int\"\n",
		"c/c.go":        "package c\n",
		"c/c_test.go":   "package c\n",
		"d/d.go":        "package d\n",
		"d/d_x_test.go": "package d_test\n",
	} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	oldBatch, oldWorkers := batchSize, scanWorkers
	t.Cleanup(func() { batchSize, scanWorkers = oldBatch, oldWorkers })
	for _, batch := range []int{1, 2, 100} {
		batchSize, scanWorkers = batch, 2
		if _, err := scan(dir, "", []string{"./..."}); err == nil || !strings.Contains(err.Error(), "load:") || !strings.Contains(err.Error(), "not an int") {
			t.Fatalf("batch %d: err = %v, want the load error of package b", batch, err)
		}
	}
}
