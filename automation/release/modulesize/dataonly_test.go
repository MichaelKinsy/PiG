// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/automation/release/modulehash"
)

// dataOnlyMark opens the go.mod of a directory that holds data only: the file keeps the directory out of the root module zip.
const dataOnlyMark = "// This directory holds data only."

// goSourcesKeptAsData are the data-only directories that hold Go files on purpose: fixture sources that tests read by path and never build as part of a module. The list only shrinks: a Go file that belongs to a build lives in the module that builds it.
var goSourcesKeptAsData = []string{
	"coding/cli/testdata",
}

// dataOnlyFindings checks the cure module-size-budget.txt names. A go.mod takes its directory out of the module above it, so `go build ./...` and `go test ./...` skip a Go file there without a word, and a //go:embed cannot reach a file there. It returns one line for every Go file whose nearest go.mod is a data-only one in a directory goSourcesKeptAsData does not list, and for every //go:embed of a module's source whose pattern names a data-only directory, a path inside one, or a directory that holds one. files are slash-separated paths relative to the repository root; read returns a file's content.
func dataOnlyFindings(files []string, read func(file string) ([]byte, error)) ([]string, error) {
	modules := map[string]bool{} // module directory -> its go.mod is data-only
	var dataOnly []string
	for _, file := range files {
		if path.Base(file) != "go.mod" || file == "go.mod" {
			continue
		}
		content, err := read(file)
		if err != nil {
			return nil, err
		}
		dir := path.Dir(file)
		modules[dir] = strings.HasPrefix(string(content), dataOnlyMark)
		if modules[dir] {
			dataOnly = append(dataOnly, dir)
		}
	}
	// owner is the directory of the nearest go.mod below the repository root, "" for a file of the root module.
	owner := func(file string) string {
		for dir := path.Dir(file); dir != "."; dir = path.Dir(dir) {
			if _, isModule := modules[dir]; isModule {
				return dir
			}
		}
		return ""
	}
	var findings, data, sources []string
	for _, file := range files {
		dir := owner(file)
		switch {
		case !modules[dir]:
			if strings.HasSuffix(file, ".go") {
				sources = append(sources, file)
			}
		case strings.HasSuffix(file, ".go") && !slices.Contains(goSourcesKeptAsData, dir):
			findings = append(findings, file+": a Go file under the data-only go.mod of "+dir+": no build compiles or tests it")
		default:
			data = append(data, file)
		}
	}
	for _, source := range sources {
		content, err := read(source)
		if err != nil {
			return nil, err
		}
		for line := range strings.SplitSeq(string(content), "\n") {
			patterns, found := strings.CutPrefix(line, "//go:embed ")
			if !found {
				continue
			}
			for pattern := range strings.FieldsSeq(patterns) {
				target := path.Join(path.Dir(source), strings.TrimPrefix(strings.Trim(pattern, "\"`"), "all:"))
				if dir := embeddedDataOnly(target, modules[target], owner(target), modules, dataOnly, data); dir != "" {
					findings = append(findings, source+": //go:embed "+pattern+" names the data-only directory "+dir+": a file under its go.mod is not part of this module")
				}
			}
		}
	}
	return findings, nil
}

// embeddedDataOnly returns the data-only directory an embed target reaches: the target is the directory (isDataOnly), its nearest go.mod is a data-only one (targetOwner), it is a directory that holds one, or it is a glob that matches one or a file of one.
func embeddedDataOnly(target string, isDataOnly bool, targetOwner string, modules map[string]bool, dataOnly, data []string) string {
	if isDataOnly {
		return target
	}
	if modules[targetOwner] {
		return targetOwner
	}
	deepest := ""
	for _, dir := range dataOnly {
		matched, err := path.Match(target, dir)
		if (err == nil && matched || strings.HasPrefix(dir, target+"/")) && len(dir) > len(deepest) {
			deepest = dir
		}
	}
	if deepest != "" || !strings.ContainsAny(target, "*?[") {
		return deepest
	}
	for _, file := range data {
		if matched, err := path.Match(target, file); err == nil && matched {
			return path.Dir(file)
		}
	}
	return ""
}

func TestDataOnlyFindingsNameAGoFileAndAnEmbedUnderADataOnlyModule(t *testing.T) {
	mod := dataOnlyMark + " Its own go.mod keeps it out of the root module zip.\nmodule example.com/m/x\n"
	tree := map[string]string{
		"go.mod":                             "module example.com/m\n",
		"pkg/pkg.go":                         "package pkg\n",
		"pkg/testdata/go.mod":                mod,
		"pkg/testdata/case.json":             "{}",
		"pkg/testdata/fixture/go.mod":        "module fixture\n",
		"pkg/testdata/fixture/main.go":       "package main\n\n//go:embed asset.txt\nvar asset []byte\n",
		"pkg/testdata/fixture/asset.txt":     "a",
		"pkg/assets/logo.png":                "png",
		"coding/cli/testdata/go.mod":         mod,
		"coding/cli/testdata/source.go":      "package fixture\n",
		"coding/cli/testdata/notes/go.mod":   mod,
		"coding/cli/testdata/notes/notes.md": "notes",
	}
	cases := []struct {
		name string
		add  map[string]string
		want []string
	}{
		{name: "a clean tree: a module of its own inside a data-only directory that embeds its own file, a listed directory, an embed beside a data-only directory", add: map[string]string{"pkg/embed.go": "package pkg\n\n//go:embed assets/logo.png\nvar logo []byte\n"}, want: nil},
		{name: "a Go file under a data-only go.mod", add: map[string]string{"pkg/testdata/helper.go": "package testdata\n"}, want: []string{"pkg/testdata/helper.go: a Go file under the data-only go.mod of pkg/testdata"}},
		{name: "a Go file under a data-only go.mod inside a listed directory", add: map[string]string{"coding/cli/testdata/notes/tool.go": "package notes\n"}, want: []string{"coding/cli/testdata/notes/tool.go: a Go file under the data-only go.mod of coding/cli/testdata/notes"}},
		{name: "an embed of a file in a data-only directory", add: map[string]string{"pkg/embed.go": "package pkg\n\n//go:embed testdata/case.json\nvar c []byte\n"}, want: []string{"pkg/embed.go: //go:embed testdata/case.json names the data-only directory pkg/testdata"}},
		{name: "an embed of the data-only directory itself, with all:", add: map[string]string{"pkg/embed.go": "package pkg\n\n//go:embed assets/logo.png all:testdata\nvar fs embed.FS\n"}, want: []string{"pkg/embed.go: //go:embed all:testdata names the data-only directory pkg/testdata"}},
		{name: "an embed of a directory that holds a data-only directory", add: map[string]string{"embed.go": "package m\n\n//go:embed pkg\nvar fs embed.FS\n"}, want: []string{"embed.go: //go:embed pkg names the data-only directory pkg/testdata"}},
		{name: "a glob that matches a file in a data-only directory", add: map[string]string{"pkg/embed.go": "package pkg\n\n//go:embed testdata/*.json\nvar fs embed.FS\n"}, want: []string{"pkg/embed.go: //go:embed testdata/*.json names the data-only directory pkg/testdata"}},
		{name: "an embed in a module of its own that names a data-only directory below it", add: map[string]string{"pkg/testdata/fixture/embed.go": "package main\n\n//go:embed data/x.txt\nvar x []byte\n", "pkg/testdata/fixture/data/go.mod": mod, "pkg/testdata/fixture/data/x.txt": "x"}, want: []string{"pkg/testdata/fixture/embed.go: //go:embed data/x.txt names the data-only directory pkg/testdata/fixture/data"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := make([]string, 0, len(tree)+len(c.add))
			for file := range tree {
				files = append(files, file)
			}
			for file := range c.add {
				files = append(files, file)
			}
			slices.Sort(files)
			got, err := dataOnlyFindings(files, func(file string) ([]byte, error) {
				if content, added := c.add[file]; added {
					return []byte(content), nil
				}
				return []byte(tree[file]), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("findings = %q, want %q", got, c.want)
			}
			for i, want := range c.want {
				if !strings.HasPrefix(got[i], want) {
					t.Errorf("finding %d = %q, want it to start with %q", i, got[i], want)
				}
			}
		})
	}
}

// TestDataOnlyDirectoriesHoldNoGoFileAndNoEmbeddedFile runs the check on the tracked tree, so a commit cannot lose a Go file or an embedded file from a build by placing it under a data-only go.mod, or by marking its directory data-only.
func TestDataOnlyDirectoriesHoldNoGoFileAndNoEmbeddedFile(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	files, err := modulehash.TrackedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	marked := 0
	findings, err := dataOnlyFindings(files, func(file string) ([]byte, error) {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err == nil && path.Base(file) == "go.mod" && strings.HasPrefix(string(content), dataOnlyMark) {
			marked++
		}
		return content, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if marked == 0 {
		t.Fatalf("no go.mod opens with %q: the mark of a data-only directory changed and this test checks nothing", dataOnlyMark)
	}
	for _, finding := range findings {
		t.Error(finding)
	}
	t.Logf("%d data-only directories", marked)
}

// TestGeneratedParityLedgersLiveOutsideTheRootModule keeps the two generated parity ledgers where they were moved so they stay out of the root module zip: test/parity/generated/ has a data-only go.mod and holds them, and neither exists at its old path under test/parity, tracked or on disk (a generator that writes the old path again fails here once it has run).
func TestGeneratedParityLedgersLiveOutsideTheRootModule(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	files, err := modulehash.TrackedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := os.ReadFile(filepath.Join(root, "test", "parity", "generated", "go.mod"))
	if err != nil || !strings.HasPrefix(string(mod), dataOnlyMark) {
		t.Fatalf("test/parity/generated/go.mod must exist and open with %q (err %v): the generated ledgers leave the root module zip through it", dataOnlyMark, err)
	}
	for _, name := range []string{"coverage.md", "normalization-inventory.json"} {
		old := "test/parity/" + name
		if slices.Contains(files, old) {
			t.Errorf("%s is tracked: the generated ledger belongs at test/parity/generated/%s", old, name)
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(old))); err == nil {
			t.Errorf("%s exists: a generator wrote the old path; it writes test/parity/generated/%s", old, name)
		}
		if !slices.Contains(files, "test/parity/generated/"+name) {
			t.Errorf("test/parity/generated/%s is not tracked", name)
		}
	}
}

// TestCuratedParityLedgersLiveOutsideTheRootModule keeps the moved curated parity ledgers in their data-only directories so they stay out of the root module zip and no writer recreates them at their old paths.
func TestCuratedParityLedgersLiveOutsideTheRootModule(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	files, err := modulehash.TrackedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		dir    string
		oldDir string
		names  []string
	}{
		{
			dir:    "test/parity/ledgers",
			oldDir: "test/parity",
			names:  []string{"async-contracts.toml"},
		},
		{
			dir:    "test/parity/interface-closure/autobind/reviewed",
			oldDir: "test/parity/interface-closure/autobind",
			names:  []string{"reviewed-decisions.json", "reviewed-types.json", "renames.json"},
		},
	}
	for _, tc := range cases {
		mod, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(tc.dir), "go.mod"))
		if err != nil || !strings.HasPrefix(string(mod), dataOnlyMark) {
			t.Fatalf("%s/go.mod must exist and open with %q (err %v)", tc.dir, dataOnlyMark, err)
		}
		for _, name := range tc.names {
			old := tc.oldDir + "/" + name
			cur := tc.dir + "/" + name
			if slices.Contains(files, old) {
				t.Errorf("%s is tracked: moved to %s", old, cur)
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(old))); err == nil {
				t.Errorf("%s exists on disk: moved to %s", old, cur)
			}
			if !slices.Contains(files, cur) {
				t.Errorf("%s is not tracked", cur)
			}
		}
	}
}
