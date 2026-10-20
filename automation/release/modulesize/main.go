// SPDX-License-Identifier: MIT

// Modulesize gates the root module zip that `go install github.com/MichaelKinsy/PiG/cmd/pig@<version>` downloads.
//
// It lists the tracked files with Go's module zip rules (golang.org/x/mod/zip, as automation/release/modulehash does), sums their uncompressed bytes, and fails above the committed budget with the largest files and directories. With -install it also builds the module zip, serves it through a file:// GOPROXY, and runs `go install ./cmd/pig` from that zip, so a missing go:embed target, a replace directive, or an oversized tree fails here and not after publication.
package main

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	modzip "golang.org/x/mod/zip"

	"github.com/MichaelKinsy/PiG/automation/release/modulehash"
)

const defaultBudgetFile = "automation/release/module-size-budget.txt"

type entry struct {
	path string
	size int64
}

// report is the measured shape of one module zip.
type report struct {
	total        int64
	files        []entry // valid files, largest first
	dirs         []entry // directories at most dirDepth deep, largest first
	omitted      int     // files the zip rules leave out, such as nested modules
	omittedBytes int64
}

const dirDepth = 3

func main() {
	var root, budgetFile string
	var top int
	var install bool
	flag.StringVar(&root, "root", ".", "repository root")
	flag.StringVar(&budgetFile, "budget-file", defaultBudgetFile, "budget file, relative to -root")
	flag.IntVar(&top, "top", 20, "number of largest files and directories to list")
	flag.BoolVar(&install, "install", false, "also run go install of cmd/pig from the module zip through a file:// GOPROXY")
	flag.Parse()
	if err := run(root, budgetFile, top, install); err != nil {
		fmt.Fprintln(os.Stderr, "module-size:", err)
		os.Exit(1)
	}
}

func run(root, budgetFile string, top int, install bool) error {
	if !filepath.IsAbs(budgetFile) {
		budgetFile = filepath.Join(root, filepath.FromSlash(budgetFile))
	}
	budget, err := readBudget(budgetFile)
	if err != nil {
		return err
	}
	files, err := modulehash.ModuleFiles(root, ".", nil)
	if err != nil {
		return err
	}
	rep, err := measure(files)
	if err != nil {
		return err
	}
	fmt.Printf("module-size: root module zip holds %d files, %d bytes uncompressed (budget %d bytes, Go limit %d bytes); %d nested-module files omitted (%d bytes)\n",
		len(rep.files), rep.total, budget, modzip.MaxZipFile, rep.omitted, rep.omittedBytes)
	if rep.total > budget {
		return errors.New(overBudget(rep, budget, top))
	}
	if !install {
		return nil
	}
	return proveInstall(root, files)
}

// readBudget parses the budget file: one decimal byte count, with blank lines and # comments ignored.
func readBudget(file string) (int64, error) {
	f, err := os.Open(file)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	var budget int64
	seen := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if seen {
			return 0, fmt.Errorf("%s: more than one budget line", file)
		}
		budget, err = strconv.ParseInt(line, 10, 64)
		if err != nil || budget <= 0 {
			return 0, fmt.Errorf("%s: budget %q is not a positive byte count", file, line)
		}
		seen = true
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	if !seen {
		return 0, fmt.Errorf("%s: no budget line", file)
	}
	return budget, nil
}

// measure applies Go's module zip rules to files and returns what the zip would hold. A file the rules reject makes the module unpublishable and is an error.
func measure(files []modzip.File) (report, error) {
	checked, _ := modzip.CheckFiles(files)
	if len(checked.Invalid) > 0 {
		var b strings.Builder
		for _, f := range checked.Invalid {
			fmt.Fprintf(&b, "\n  %s: %v", f.Path, f.Err)
		}
		return report{}, fmt.Errorf("the module zip rules reject tracked files:%s", b.String())
	}
	byPath := make(map[string]modzip.File, len(files))
	for _, f := range files {
		byPath[f.Path()] = f
	}
	var rep report
	dirs := map[string]int64{}
	for _, p := range checked.Valid {
		info, err := byPath[p].Lstat()
		if err != nil {
			return report{}, err
		}
		rep.total += info.Size()
		rep.files = append(rep.files, entry{p, info.Size()})
		parts := strings.Split(p, "/")
		for depth := 1; depth < len(parts) && depth <= dirDepth; depth++ {
			dirs[strings.Join(parts[:depth], "/")+"/"] += info.Size()
		}
	}
	for _, o := range checked.Omitted {
		rep.omitted++
		if info, err := byPath[o.Path].Lstat(); err == nil {
			rep.omittedBytes += info.Size()
		}
	}
	for d, size := range dirs {
		rep.dirs = append(rep.dirs, entry{d, size})
	}
	bySize := func(a, b entry) int { return cmp.Or(cmp.Compare(b.size, a.size), cmp.Compare(a.path, b.path)) }
	slices.SortFunc(rep.files, bySize)
	slices.SortFunc(rep.dirs, bySize)
	return rep, nil
}

func overBudget(rep report, budget int64, top int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "the root module zip is %d bytes, %d over the %d-byte budget in %s.", rep.total, rep.total-budget, budget, defaultBudgetFile)
	fmt.Fprintf(&b, " `go install` fails above %d bytes, and every user downloads the whole zip.", modzip.MaxZipFile)
	b.WriteString(" Move the bulky data out of the root module, for example into a directory with its own go.mod, or shrink it. Raise the budget never.")
	fmt.Fprintf(&b, "\nlargest directories (depth <= %d):", dirDepth)
	for _, e := range rep.dirs[:min(top, len(rep.dirs))] {
		fmt.Fprintf(&b, "\n  %12d  %s", e.size, e.path)
	}
	b.WriteString("\nlargest files:")
	for _, e := range rep.files[:min(top, len(rep.files))] {
		fmt.Fprintf(&b, "\n  %12d  %s", e.size, e.path)
	}
	return b.String()
}

// proveInstall serves the module zip of root through a file:// GOPROXY and runs `go install` of cmd/pig at that version outside any workspace.
func proveInstall(root string, files []modzip.File) error {
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return err
	}
	modPath := modfile.ModulePath(goMod)
	if modPath == "" {
		return errors.New("go.mod has no module path")
	}
	work, err := os.MkdirTemp("", "pig-module-size-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	escaped, err := module.EscapePath(modPath)
	if err != nil {
		return err
	}
	versionDir := filepath.Join(work, "proxy", filepath.FromSlash(escaped), "@v")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		return err
	}
	// A fresh version per run keeps the module cache from serving stale source; removeCached deletes the run's entries.
	version := "v0.0.0-modulesize-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := writeZip(filepath.Join(versionDir, version+".zip"), module.Version{Path: modPath, Version: version}, files); err != nil {
		return err
	}
	info, err := json.Marshal(struct{ Version, Time string }{version, time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	for name, data := range map[string][]byte{version + ".mod": goMod, version + ".info": info} {
		if err := os.WriteFile(filepath.Join(versionDir, name), data, 0o644); err != nil {
			return err
		}
	}
	upstreamProxy, err := goEnv(work, "GOPROXY")
	if err != nil {
		return err
	}
	modCache, err := goEnv(work, "GOMODCACHE")
	if err != nil {
		return err
	}
	// Remove this run's cache entries so repeated runs do not fill the module cache with 300 MB trees.
	defer removeCached(modCache, escaped, version)

	bin := filepath.Join(work, "bin")
	scratch := filepath.Join(work, "scratch")
	for _, dir := range []string{bin, scratch} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	target := modPath + "/cmd/pig@" + version
	// The file proxy serves only this module; every other module falls through to the configured proxy. GONOSUMDB applies to the module under test, which no checksum database lists.
	env := append(os.Environ(),
		"GOPROXY="+fileProxyURL(filepath.Join(work, "proxy"))+","+upstreamProxy,
		"GONOSUMDB="+modPath,
		"GOWORK=off",
		"GOFLAGS=",
		"GOBIN="+bin,
	)
	fmt.Printf("module-size: go install %s from a file:// GOPROXY\n", target)
	cmd := exec.Command("go", "install", "-modcacherw", target)
	cmd.Dir = work
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go install %s failed: %w", target, err)
	}
	exe := filepath.Join(bin, "pig")
	if _, err := os.Stat(exe); err != nil {
		exe += ".exe"
	}
	probe := exec.Command(exe, "--version")
	probe.Dir = work
	probe.Env = append(os.Environ(),
		"HOME="+scratch, "USERPROFILE="+scratch,
		"PIG_HOME="+scratch, "PI_HOME="+scratch,
		"PIG_CODING_AGENT_DIR="+filepath.Join(scratch, "agent"), "PI_CODING_AGENT_DIR="+filepath.Join(scratch, "agent"),
	)
	out, err := probe.CombinedOutput()
	if err != nil {
		return fmt.Errorf("the installed pig does not run: %w\n%s", err, out)
	}
	fmt.Printf("module-size: installed pig runs: %s\n", bytes.TrimSpace(out))
	return nil
}

func fileProxyURL(dir string) string {
	slash := filepath.ToSlash(dir)
	if !strings.HasPrefix(slash, "/") {
		slash = "/" + slash
	}
	return "file://" + slash
}

func goEnv(dir, key string) (string, error) {
	cmd := exec.Command("go", "env", key)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go env %s: %w", key, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// writeZip builds the module zip at file.
func writeZip(file string, mod module.Version, files []modzip.File) error {
	f, err := os.Create(file)
	if err != nil {
		return err
	}
	zipErr := modzip.Create(f, mod, files)
	if err := errors.Join(zipErr, f.Close()); err != nil {
		return fmt.Errorf("build module zip: %w", err)
	}
	return nil
}

func removeCached(modCache, escapedPath, version string) {
	cached, _ := filepath.Glob(filepath.Join(modCache, "cache", "download", filepath.FromSlash(escapedPath), "@v", version+".*"))
	for _, f := range cached {
		_ = os.Remove(f)
	}
	_ = os.RemoveAll(filepath.Join(modCache, filepath.FromSlash(escapedPath)+"@"+version))
}
