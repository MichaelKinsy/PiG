// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Set-version synchronizes repository release pins without publishing anything.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"

	"github.com/MichaelKinsy/PiG/automation/release/modulehash"
)

type options struct {
	version, date  string
	dryRun         bool
	moveUnreleased bool
	renameCurrent  bool
	releaseModules bool
}

type plan struct {
	root          string
	before, after map[string][]byte
	modules       map[string]*localModule
	rootModule    string
	version       string
}

type localModule struct {
	dir            string
	file           *modfile.File
	visiting, done bool
}

func main() {
	var opt options
	var root string
	flag.StringVar(&root, "root", ".", "repository root")
	flag.StringVar(&opt.version, "version", "", "required release version x.y.z")
	flag.BoolVar(&opt.dryRun, "dry-run", false, "print the planned diff without writing or running checks")
	flag.BoolVar(&opt.moveUnreleased, "move-unreleased", false, "move Unreleased entries into the selected release")
	flag.BoolVar(&opt.renameCurrent, "rename-current", false, "rename the newest heading instead of adding one; only for an unpublished candidate")
	flag.BoolVar(&opt.releaseModules, "release-modules", false, "prepare unpublished module pins and hashes on a release branch; publish nested tags before merging to main")
	flag.Parse()
	opt.date = time.Now().UTC().Format(time.DateOnly)
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "set-version: unexpected positional arguments")
		os.Exit(1)
	}
	if err := run(root, opt, os.Stdout, checkConsistency); err != nil {
		fmt.Fprintln(os.Stderr, "set-version:", err)
		os.Exit(1)
	}
}

func run(root string, opt options, output io.Writer, check func(string, io.Writer) error) error {
	// Stock disposition: required substrate; repository-only release maintenance.
	p, err := prepare(root, opt)
	if err != nil {
		return err
	}
	paths := slices.Sorted(maps.Keys(p.after))
	for _, path := range paths {
		if bytes.Equal(p.before[path], p.after[path]) {
			continue
		}
		before, after := string(p.before[path]), string(p.after[path])
		edits := myers.ComputeEdits(span.URIFromPath(path), before, after)
		if _, err := fmt.Fprint(output, gotextdiff.ToUnified("a/"+path, "b/"+path, before, edits)); err != nil {
			return err
		}
	}
	if opt.dryRun {
		return nil
	}
	for _, path := range paths {
		if bytes.Equal(p.before[path], p.after[path]) {
			continue
		}
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(path)), p.after[path], 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	if err := check(root, output); err != nil {
		return fmt.Errorf("consistency checks failed (changes retained for inspection): %w", err)
	}
	return nil
}

func (p *plan) read(path string) ([]byte, error) {
	if data, ok := p.after[path]; ok {
		return data, nil
	}
	data, err := os.ReadFile(filepath.Join(p.root, filepath.FromSlash(path)))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	p.before[path], p.after[path] = data, data
	return data, nil
}

func prepare(root string, opt options) (*plan, error) {
	if !regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`).MatchString(opt.version) {
		return nil, fmt.Errorf("version must be x.y.z without a v prefix, prerelease, or build metadata: %q", opt.version)
	}
	p := &plan{root: root, before: map[string][]byte{}, after: map[string][]byte{}, modules: map[string]*localModule{}, version: "v" + opt.version}
	const pin = "internal/coding/pigversion/pigversion.go"
	data, err := p.read(pin)
	if err != nil {
		return nil, err
	}
	re := regexp.MustCompile(`(?m)^const PigVersion = "([^"]+)"$`)
	match := re.FindSubmatch(data)
	if len(match) != 2 || !semver.IsValid("v"+string(match[1])) {
		return nil, errors.New("cannot read PigVersion")
	}
	current := string(match[1])
	if semver.Compare(p.version, "v"+current) < 0 {
		return nil, fmt.Errorf("refusing lower version %s (current %s)", opt.version, current)
	}
	p.after[pin] = re.ReplaceAll(data, []byte(`const PigVersion = "`+opt.version+`"`))
	if opt.releaseModules {
		if err := p.updateModules(); err != nil {
			return nil, err
		}
	}
	const standard = "piglets/standard/pig-standard.yaml"
	data, err = p.read(standard)
	if err != nil {
		return nil, err
	}
	re = regexp.MustCompile(`(?m)^(release:\r?\n[ \t]+version: )[^\n]+`)
	if len(re.FindAll(data, -1)) != 1 {
		return nil, errors.New("expected one Standard release.version")
	}
	p.after[standard] = re.ReplaceAll(data, []byte("${1}"+opt.version+"-dev"))
	data, err = p.read("CHANGELOG.md")
	if err != nil {
		return nil, err
	}
	p.after["CHANGELOG.md"], err = updateChangelog(data, current, opt)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (p *plan) updateModules() error {
	paths, err := modulehash.TrackedFiles(p.root)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if filepath.Base(path) != "go.mod" {
			continue
		}
		data, err := p.read(path)
		if err != nil {
			return err
		}
		file, err := modfile.Parse(path, data, nil)
		if err != nil {
			return err
		}
		if file.Module == nil {
			return fmt.Errorf("%s has no module directive", path)
		}
		name := file.Module.Mod.Path
		if _, exists := p.modules[name]; exists {
			return fmt.Errorf("duplicate local module %s", name)
		}
		p.modules[name] = &localModule{dir: filepath.ToSlash(filepath.Dir(path)), file: file}
		if path == "go.mod" {
			p.rootModule = name
		}
	}
	if p.rootModule == "" {
		return errors.New("no tracked root go.mod")
	}
	for _, name := range slices.Sorted(maps.Keys(p.modules)) {
		m := p.modules[name]
		changed := false
		for _, req := range m.file.Require {
			if _, ok := p.modules[req.Mod.Path]; !ok {
				continue
			}
			if req.Mod.Version != p.version {
				if err := m.file.AddRequire(req.Mod.Path, p.version); err != nil {
					return err
				}
				changed = true
			}
		}
		if changed {
			data, err := m.file.Format()
			if err != nil {
				return err
			}
			p.after[filepath.ToSlash(filepath.Join(m.dir, "go.mod"))] = data
		}
	}
	for _, name := range slices.Sorted(maps.Keys(p.modules)) {
		if err := p.updateSum(name); err != nil {
			return err
		}
	}
	return nil
}

func (p *plan) updateSum(name string) error {
	m := p.modules[name]
	if m.done {
		return nil
	}
	if m.visiting {
		return fmt.Errorf("nested module checksum dependency cycle at %s", name)
	}
	m.visiting = true
	path := filepath.ToSlash(filepath.Join(m.dir, "go.sum"))
	data, err := p.read(path)
	if errors.Is(err, os.ErrNotExist) {
		// Replaced fixture dependencies do not need a go.sum. Refresh every
		// existing sum, but do not create checksum files in those fixtures.
		m.done = true
		return nil
	}
	if err != nil {
		return err
	}
	deps := map[string]bool{}
	for _, req := range m.file.Require {
		if req.Mod.Path != p.rootModule && p.modules[req.Mod.Path] != nil {
			deps[req.Mod.Path] = true
		}
	}
	var lines []string
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if fields[0] != p.rootModule && p.modules[fields[0]] != nil {
			deps[fields[0]] = true
		} else {
			lines = append(lines, line)
		}
	}
	for _, dep := range slices.Sorted(maps.Keys(deps)) {
		if err := p.updateSum(dep); err != nil {
			return err
		}
		zipHash, modHash, err := modulehash.Hashes(p.root, p.modules[dep].dir, module.Version{Path: dep, Version: p.version}, p.after)
		if err != nil {
			return err
		}
		lines = append(lines, dep+" "+p.version+" "+zipHash, dep+" "+p.version+"/go.mod "+modHash)
	}
	slices.SortFunc(lines, func(a, b string) int {
		af, bf := strings.Fields(a), strings.Fields(b)
		if len(af) < 2 || len(bf) < 2 {
			return strings.Compare(a, b)
		}
		if order := strings.Compare(af[0], bf[0]); order != 0 {
			return order
		}
		if order := semver.Compare(strings.TrimSuffix(af[1], "/go.mod"), strings.TrimSuffix(bf[1], "/go.mod")); order != 0 {
			return order
		}
		return strings.Compare(a, b)
	})
	p.after[path] = []byte(strings.Join(lines, "\n") + "\n")
	m.done = true
	return nil
}

func updateChangelog(data []byte, current string, opt options) ([]byte, error) {
	text := string(data)
	const unreleased = "## [Unreleased]\n"
	start := strings.Index(text, unreleased)
	if start < 0 || strings.Count(text, unreleased) != 1 {
		return nil, errors.New("expected one CHANGELOG Unreleased heading")
	}
	start += len(unreleased)
	next := strings.Index(text[start:], "\n## [")
	if next < 0 {
		return nil, errors.New("CHANGELOG has no previous release heading")
	}
	next += start + 1
	headingEnd := strings.IndexByte(text[next:], '\n')
	if headingEnd < 0 {
		return nil, errors.New("invalid CHANGELOG release heading")
	}
	headingEnd += next
	heading := text[next:headingEnd]
	target := "## [" + opt.version + "] - "
	isTarget := strings.HasPrefix(heading, target)
	if !isTarget && strings.Contains(text, target) {
		return nil, errors.New("selected version already occurs below the newest release")
	}
	if opt.renameCurrent && !isTarget && !strings.HasPrefix(heading, "## ["+current+"] - ") {
		return nil, errors.New("newest CHANGELOG heading does not match current PigVersion")
	}
	body := text[start:next]
	tail := text[next:]
	if opt.renameCurrent && !isTarget {
		tail = target + opt.date + text[headingEnd:]
	} else if !isTarget {
		tail = target + opt.date + "\n\n" + tail
	}
	if opt.moveUnreleased && strings.TrimSpace(body) != "" {
		end := strings.IndexByte(tail, '\n')
		tail = tail[:end+1] + "\n" + strings.TrimSpace(body) + "\n" + tail[end+1:]
		body = "\n"
	}
	return []byte(text[:start] + body + tail), nil
}

func checkConsistency(root string, output io.Writer) error {
	for _, args := range [][]string{
		{"test", "./test/gomodule", "-count=1"},
		{"test", "./internal/codingagent", "-run", "^TestParseChangelog_RealFile$", "-count=1"},
	} {
		cmd := exec.Command("go", args...)
		cmd.Dir, cmd.Stdout, cmd.Stderr = root, output, output
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("go %s: %w", strings.Join(args, " "), err)
		}
	}
	return nil
}
