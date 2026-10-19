// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/automation/release/modulehash"
)

func writeTree(t *testing.T, root string, files map[string]int) {
	t.Helper()
	for name, size := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		data := []byte(strings.Repeat("x", size))
		if filepath.Base(name) == "go.mod" {
			data = []byte("module example.com/" + strings.ReplaceAll(filepath.ToSlash(filepath.Dir(name)), ".", "root") + "\n\ngo 1.26.0\n")
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMeasureCountsOnlyWhatGoPutsInTheModuleZip(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]int{
		"go.mod":              0,
		"main.go":             100,
		"pkg/big.json":        5000,
		"pkg/small.go":        10,
		"data/go.mod":         0,
		"data/ledger.json":    70000,
		"data/deep/more.json": 30000,
	})
	files, err := modulehash.ModuleFiles(root, ".", nil)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := measure(files)
	if err != nil {
		t.Fatal(err)
	}
	goMod, err := os.Stat(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if want := goMod.Size() + 100 + 5000 + 10; rep.total != want {
		t.Errorf("total = %d, want %d: the nested module data/ must not count", rep.total, want)
	}
	if rep.omitted != 3 {
		t.Errorf("omitted = %d, want 3 nested-module files", rep.omitted)
	}
	if got := rep.files[0]; got.path != "pkg/big.json" || got.size != 5000 {
		t.Errorf("largest file = %+v, want pkg/big.json 5000", got)
	}
	if got := rep.dirs[0]; got.path != "pkg/" {
		t.Errorf("largest directory = %+v, want pkg/", got)
	}
}

func TestOverBudgetListsTheLargestFilesAndDirectories(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]int{"go.mod": 0, "a/one.bin": 900, "b/two.bin": 300})
	files, err := modulehash.ModuleFiles(root, ".", nil)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := measure(files)
	if err != nil {
		t.Fatal(err)
	}
	msg := overBudget(rep, 500, 5)
	for _, want := range []string{"is 1", "over the 500-byte budget", "a/one.bin", "b/two.bin", "a/", "own go.mod"} {
		if !strings.Contains(msg, want) {
			t.Errorf("report lacks %q:\n%s", want, msg)
		}
	}
	if strings.Index(msg, "a/one.bin") > strings.Index(msg, "b/two.bin") {
		t.Errorf("files are not listed largest first:\n%s", msg)
	}
}

func TestReadBudget(t *testing.T) {
	dir := t.TempDir()
	for body, want := range map[string]int64{"# note\n\n123\n": 123, "7": 7} {
		file := filepath.Join(dir, "budget")
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := readBudget(file)
		if err != nil || got != want {
			t.Errorf("readBudget(%q) = %d, %v; want %d", body, got, err, want)
		}
	}
	for _, body := range []string{"", "# only a comment\n", "1\n2\n", "abc\n", "-5\n", "0\n"} {
		file := filepath.Join(dir, "bad")
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, err := readBudget(file); err == nil {
			t.Errorf("readBudget(%q) = %d, want an error", body, got)
		}
	}
}

func TestCommittedBudgetStaysFarBelowTheGoLimit(t *testing.T) {
	budget, err := readBudget(filepath.Join("..", "module-size-budget.txt"))
	if err != nil {
		t.Fatal(err)
	}
	const goLimit = 500 << 20
	if budget > goLimit/2 {
		t.Errorf("budget %d is above half of Go's %d-byte module zip limit", budget, goLimit)
	}
}
