// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// toolSource and baselineSource are the repository paths the selection reads at the merge base, to see which rows changed and which packages the tool's list gained.
const (
	toolSource     = "automation/ci/allocbase/main.go"
	baselineSource = "automation/ci/allocbase/baseline.toml"
)

// selected is a hot package a change makes the run measure, with the reasons in the order the selection found them.
type selected struct {
	Package string
	Reasons []string
}

// goGraph is what `go list -deps -test` says about the hot packages and everything they import.
type goGraph struct {
	// Module is the import path of the repository's module.
	Module string
	// Imports maps an import path to the import paths it imports, test variants merged into the package they test.
	Imports map[string][]string
	// Embeds maps a repository-relative file a package embeds to the import paths that embed it.
	Embeds map[string][]string
}

// selectionInput is everything selectChanged reads. The git and go list calls fill it in; the tests fill it in by hand.
type selectionInput struct {
	Hot     []string // the tool's package list now
	BaseHot []string // the tool's package list at the merge base
	Files   []string // repository-relative, slash-separated paths of the changed files
	// HoldsBenchmark says whether a changed _test.go file holds a func Benchmark now or held one at the merge base: a changed benchmark file, a deleted one and a rewritten one all change what the run finds.
	HoldsBenchmark func(file string) bool
	BaseRows, Rows []entry
	// Graph is called once, and only when a changed file can belong to a package.
	Graph func() (goGraph, error)
}

var funcBenchmark = regexp.MustCompile(`(?m)^func Benchmark`)

// importPath names the package in directory dir of the module.
func importPath(module, dir string) string {
	if dir == "." || dir == "" {
		return module
	}
	return module + "/" + dir
}

// selectChanged names the hot packages the change makes the run measure: the package of a changed benchmark file, of a changed non-test file (source, embedded or data file, or a file under its testdata), of a changed or removed baseline row, and one the tool's list gained; every hot package that imports (tests included) a package with a changed non-test file; and every hot package when go.mod, go.sum, go.work or go.work.sum changed. A changed _test.go file without a benchmark and a Markdown file select nothing, and a package outside the tool's list is never selected: the nightly does not run it either, so no baseline entry is owed for its benchmarks.
func selectChanged(in selectionInput) ([]selected, error) {
	reasons := map[string][]string{}
	add := func(pkg, why string) {
		if slices.Contains(in.Hot, pkg) && !slices.Contains(reasons[pkg], why) {
			reasons[pkg] = append(reasons[pkg], why)
		}
	}
	var sources []string
	for _, file := range in.Files {
		dir, base := path.Split(file)
		dir = strings.TrimSuffix(dir, "/")
		switch {
		case dir == "" && (base == "go.mod" || base == "go.sum" || base == "go.work" || base == "go.work.sum"):
			for _, pkg := range in.Hot {
				add(pkg, base+" changed")
			}
		case strings.HasSuffix(base, ".md"):
		case strings.HasSuffix(base, "_test.go"):
			if slices.Contains(in.Hot, "./"+dir) && in.HoldsBenchmark(file) {
				add("./"+dir, "benchmark file "+file)
			}
		case slices.Contains(strings.Split(dir, "/"), "testdata"):
			// A fixture belongs to the package whose directory holds the testdata directory, and does not change what imports that package.
			owner, _, _ := strings.Cut("/"+dir+"/", "/testdata/")
			add("."+owner, "data file "+file)
		default:
			sources = append(sources, file)
			kind := "data file "
			if strings.HasSuffix(base, ".go") {
				kind = "source file "
			}
			add("./"+dir, kind+file)
		}
	}
	recorded := map[string]int64{}
	for _, row := range in.BaseRows {
		recorded[row.key()] = row.Allocs
	}
	for _, row := range in.Rows {
		if old, ok := recorded[row.key()]; !ok || old != row.Allocs {
			add(row.Package, "baseline row "+row.Benchmark)
		}
		delete(recorded, row.key())
	}
	for key := range recorded {
		pkg, name, _ := strings.Cut(key, " ")
		add(pkg, "baseline row "+name+" removed")
	}
	for _, pkg := range in.Hot {
		if !slices.Contains(in.BaseHot, pkg) {
			add(pkg, "new in the tool's package list")
		}
	}
	if len(sources) > 0 {
		graph, err := in.Graph()
		if err != nil {
			return nil, err
		}
		changed := map[string]string{}
		for _, file := range sources {
			changed[importPath(graph.Module, path.Dir(file))] = file
			for _, owner := range graph.Embeds[file] {
				changed[owner] = file
			}
		}
		importers := map[string][]string{}
		for from, imports := range graph.Imports {
			for _, to := range imports {
				importers[to] = append(importers[to], from)
			}
		}
		// origin[p] is the changed package whose change reaches p.
		origin := map[string]string{}
		var queue []string
		for pkg := range changed {
			origin[pkg] = pkg
			queue = append(queue, pkg)
		}
		slices.Sort(queue)
		for len(queue) > 0 {
			next := queue[0]
			queue = queue[1:]
			for _, from := range importers[next] {
				if _, seen := origin[from]; !seen {
					origin[from] = origin[next]
					queue = append(queue, from)
				}
			}
		}
		for _, pkg := range in.Hot {
			ip := importPath(graph.Module, strings.TrimPrefix(pkg, "./"))
			if from, ok := origin[ip]; ok && from != ip {
				add(pkg, "imports changed package "+strings.TrimPrefix(strings.TrimPrefix(from, graph.Module), "/"))
			}
		}
	}
	var out []selected
	for _, pkg := range in.Hot {
		if why := reasons[pkg]; len(why) > 0 {
			out = append(out, selected{Package: pkg, Reasons: why})
		}
	}
	return out, nil
}

// describe renders a selection on one line, naming the first reasons.
func (s selected) describe() string {
	why := s.Reasons
	if len(why) > 3 {
		why = append(slices.Clone(why[:3]), fmt.Sprintf("and %d more", len(s.Reasons)-3))
	}
	return s.Package + ": " + strings.Join(why, "; ")
}

var packagesLiteral = regexp.MustCompile(`(?m)^var packages = \[\]string\{([^}]*)\}`)

// parseToolPackages reads the package list out of the tool's source, so the list at the merge base can be compared with the list now.
func parseToolPackages(source []byte) []string {
	match := packagesLiteral.FindSubmatch(source)
	if match == nil {
		return nil
	}
	var pkgs []string
	for _, field := range strings.Split(string(match[1]), ",") {
		if name := strings.Trim(strings.TrimSpace(field), `"`); name != "" {
			pkgs = append(pkgs, name)
		}
	}
	return pkgs
}

// output runs a program in root and returns its stdout; a warning on stderr never lands in a file list.
func output(root, program string, args ...string) ([]byte, error) {
	//nolint:gosec // G204 (class a): program is literal git or go; args are fixed subcommands, the caller's -changed-since ref and file names git itself listed.
	command := exec.Command(program, args...)
	command.Dir = root
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", program, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// changedFiles lists the files that differ from the merge base of HEAD and ref: the commits since it and, unless committedOnly, the index, the working tree and the untracked files (a CI job whose build steps leave files behind reads the commits only).
func changedFiles(root, base string, committedOnly bool) ([]string, error) {
	queries := [][]string{{"diff", "-z", "--name-only", "--no-renames", base, "HEAD"}}
	if !committedOnly {
		queries = append(queries, []string{"diff", "-z", "--name-only", "--no-renames", base}, []string{"ls-files", "-z", "--others", "--exclude-standard"})
	}
	var files []string
	for _, query := range queries {
		out, err := output(root, "git", query...)
		if err != nil {
			return nil, err
		}
		for _, name := range strings.Split(string(out), "\x00") {
			if name != "" && !slices.Contains(files, name) {
				files = append(files, name)
			}
		}
	}
	slices.Sort(files)
	return files, nil
}

// loadGraph runs one `go list -deps -test` over the hot packages. The module path is the one of the first hot package: in a workspace (go.work) `go list -m` names every module of it.
func loadGraph(root string, hot []string) (goGraph, error) {
	module, err := output(root, "go", "list", "-f", "{{.Module.Path}}", hot[0])
	if err != nil {
		return goGraph{}, err
	}
	out, err := output(root, "go", append([]string{"list", "-deps", "-test", "-json=ImportPath,Dir,Imports,EmbedFiles"}, hot...)...)
	if err != nil {
		return goGraph{}, err
	}
	graph := goGraph{Module: strings.TrimSpace(string(module)), Imports: map[string][]string{}, Embeds: map[string][]string{}}
	decoder := json.NewDecoder(bytes.NewReader(out))
	for decoder.More() {
		var listed struct {
			ImportPath, Dir     string
			Imports, EmbedFiles []string
		}
		if err := decoder.Decode(&listed); err != nil {
			return goGraph{}, fmt.Errorf("go list -deps -test: %w", err)
		}
		pkg := normalizeImportPath(listed.ImportPath)
		for _, imported := range listed.Imports {
			if to := normalizeImportPath(imported); to != pkg {
				graph.Imports[pkg] = append(graph.Imports[pkg], to)
			}
		}
		for _, file := range listed.EmbedFiles {
			rel, err := filepath.Rel(root, filepath.Join(listed.Dir, file))
			if err == nil && !strings.HasPrefix(rel, "..") {
				graph.Embeds[filepath.ToSlash(rel)] = append(graph.Embeds[filepath.ToSlash(rel)], pkg)
			}
		}
	}
	return graph, nil
}

// normalizeImportPath folds a test variant into the package it tests: "p [p.test]" and "p_test" are p, and "p.test" is p's test binary.
func normalizeImportPath(importPath string) string {
	if cut := strings.Index(importPath, " ["); cut >= 0 {
		importPath = importPath[:cut]
	}
	importPath = strings.TrimSuffix(importPath, ".test")
	return strings.TrimSuffix(importPath, "_test")
}

// changedPackages names the hot packages a change since ref makes the run measure, and how many files changed. root is the repository root; rows are the baseline entries the run compares with.
func changedPackages(root, ref string, committedOnly bool, hot []string, rows []entry) ([]selected, int, error) {
	if _, err := output(root, "git", "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
		return nil, 0, fmt.Errorf("-changed-since %q names no commit: %w", ref, err)
	}
	out, err := output(root, "git", "merge-base", "HEAD", ref)
	if err != nil {
		return nil, 0, err
	}
	base := strings.TrimSpace(string(out))
	files, err := changedFiles(root, base, committedOnly)
	if err != nil {
		return nil, 0, err
	}
	in := selectionInput{Hot: hot, Files: files, Rows: rows, BaseRows: baseRows(root, base)}
	if source, err := output(root, "git", "show", base+":"+toolSource); err == nil {
		in.BaseHot = parseToolPackages(source)
	}
	in.HoldsBenchmark = func(file string) bool {
		if source, err := output(root, "git", "show", base+":"+file); err == nil && funcBenchmark.Match(source) {
			return true
		}
		var source []byte
		if committedOnly {
			source, err = output(root, "git", "show", "HEAD:"+file)
		} else {
			source, err = os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		}
		return err == nil && funcBenchmark.Match(source)
	}
	in.Graph = func() (goGraph, error) { return loadGraph(root, hot) }
	selection, err := selectChanged(in)
	return selection, len(files), err
}

// baseRows reads the baseline entries as the merge base has them; none when the file is not there yet.
func baseRows(root, base string) []entry {
	out, err := output(root, "git", "show", base+":"+baselineSource)
	if err != nil {
		return nil
	}
	var file baselineFile
	if _, err := toml.Decode(string(out), &file); err != nil {
		return nil
	}
	return file.Benchmark
}
