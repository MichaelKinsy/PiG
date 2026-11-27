// SPDX-License-Identifier: MIT

package cidag

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// ListPackage is the subset of `go list -e -test -deps -json` fields required
// to build the cross-GOOS package graph and file ownership index.
type ListPackage struct {
	ImportPath      string          `json:"ImportPath"`
	ForTest         string          `json:"ForTest"`
	Dir             string          `json:"Dir"`
	Module          *ModInfo        `json:"Module"`
	Imports         []string        `json:"Imports"`
	Deps            []string        `json:"Deps"`
	EmbedFiles      []string        `json:"EmbedFiles"`
	TestEmbedFiles  []string        `json:"TestEmbedFiles"`
	XTestEmbedFiles []string        `json:"XTestEmbedFiles"`
	GoFiles         []string        `json:"GoFiles"`
	CgoFiles        []string        `json:"CgoFiles"`
	TestGoFiles     []string        `json:"TestGoFiles"`
	XTestGoFiles    []string        `json:"XTestGoFiles"`
	IgnoredGoFiles  []string        `json:"IgnoredGoFiles"`
	Incomplete      bool            `json:"Incomplete"`
	Error           *PackageError   `json:"Error"`
	DepsErrors      []*PackageError `json:"DepsErrors"`
	// GOOS tags which `go list` invocation emitted this entry (populated by LoadGraph).
	GOOS string `json:"-"`
}

// PackageError captures Error / DepsErrors records emitted by `go list -e -json`.
type PackageError struct {
	ImportStack []string `json:"ImportStack"`
	Pos         string   `json:"Pos"`
	Err         string   `json:"Err"`
}

// ModInfo captures the Module.Path returned by `go list -json`.
type ModInfo struct {
	Path string `json:"Path"`
}

// Graph holds the unified cross-GOOS package dependency graph, embed index,
// source sets, OS-only test package sets, and parity maps.
type Graph struct {
	Root       string
	Module     string
	MainPkgs   map[string]bool
	ByDir      map[string]string          // repo-relative package dir ("." or "a/b") -> full import path
	Deps       map[string][]string        // full import path -> deduplicated transitive deps (for closure queries like cmd/pig)
	RevDeps    map[string][]string        // full import path -> sorted main-module reverse dependents (transitive)
	EmbedSrc   map[string][]string        // repo-relative embedded file -> packages that embed it in production code
	EmbedTest  map[string][]string        // repo-relative embedded file -> packages that embed it in tests
	SrcFiles   map[string][]string        // full import path -> absolute paths of production Go files
	TestFiles  map[string][]string        // full import path -> absolute paths of test Go files
	OSNarrow   map[string]bool            // main-module packages with direct OS-sensitive imports/files/runtime.GOOS
	OSBroad    map[string]bool            // OSNarrow U packages importing os or path/filepath
	OSTests    map[string]map[string]bool // goos ("windows","darwin") -> main-module packages with GOOS-only test files
	IgnoredAll map[string]bool            // repo-relative Go files that stayed in IgnoredGoFiles across every queried GOOS (excluding //go:build ignore)

	PortMapEdges   map[string][]string // upstream path -> Go file paths from PORT_MAP.md
	ScenarioCovers map[string][]string // scenario repo-relative path -> upstream covers paths
	ScenarioTexts  map[string]string   // scenario repo-relative path -> raw TOML content (for testdata fixture matching)

	sourceCache map[string]string
	readFile    func(absPath string) ([]byte, error)
}

// GraphInput bundles raw cross-GOOS `go list` outputs and optional parity metadata
// so BuildGraph is deterministic and unit-testable without invoking `go list`.
type GraphInput struct {
	Root           string
	Module         string
	PackagesByGOOS map[string][]ListPackage // goos -> packages
	PortMapEdges   map[string][]string
	ScenarioCovers map[string][]string
	ScenarioTexts  map[string]string
	ReadFile       func(absPath string) ([]byte, error)
}

var (
	osNarrowImports = map[string]bool{
		"os/exec":   true,
		"os/signal": true,
		"syscall":   true,
		"os/user":   true,
		"net":       true,
		"unsafe":    true,
	}
	osNarrowPrefixes = []string{
		"golang.org/x/sys/",
		"golang.org/x/term",
		"github.com/creack/pty",
		"github.com/UserExistsError/conpty",
	}
	osBroadImports = map[string]bool{
		"path/filepath": true,
		"os":            true,
	}
	osFilenameRE        = regexp.MustCompile(`_(windows|darwin|unix|linux|android)(_[a-z0-9]+)?\.go$`)
	osSpecificNameRE    = regexp.MustCompile(`_(windows|darwin)(_[a-z0-9]+)?\.go$`)
	osSpecificBuildWord = regexp.MustCompile(`\b(windows|darwin)\b`)
	buildIgnoreRE       = regexp.MustCompile(`^//go:build\s+ignore(\s|$)`)
	namedDirectories    = map[string]bool{"changelog.d": true}
)

// stripVariant removes the " [pkg.test]" suffix `go list -test` attaches to test variants.
func stripVariant(importPath string) string {
	if base, _, ok := strings.Cut(importPath, " ["); ok {
		return base
	}
	return importPath
}

// ownerOf returns the main package import path that a test variant or external test belongs to.
func ownerOf(pkg ListPackage) string {
	if pkg.ForTest != "" {
		return pkg.ForTest
	}
	return stripVariant(pkg.ImportPath)
}

// BuildGraph constructs a Graph from pre-collected cross-GOOS `go list` entries.
func BuildGraph(in GraphInput) (*Graph, error) {
	if in.Module == "" {
		in.Module = DefaultModule
	}
	absRoot := in.Root
	if absRoot != "" {
		if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
			absRoot = resolved
		} else if a, err := filepath.Abs(absRoot); err == nil {
			absRoot = a
		}
	}
	readFn := in.ReadFile
	if readFn == nil {
		readFn = os.ReadFile
	}

	g := &Graph{
		Root:           absRoot,
		Module:         in.Module,
		MainPkgs:       map[string]bool{},
		ByDir:          map[string]string{},
		Deps:           map[string][]string{},
		RevDeps:        map[string][]string{},
		EmbedSrc:       map[string][]string{},
		EmbedTest:      map[string][]string{},
		SrcFiles:       map[string][]string{},
		TestFiles:      map[string][]string{},
		OSNarrow:       map[string]bool{},
		OSBroad:        map[string]bool{},
		OSTests:        map[string]map[string]bool{"windows": {}, "darwin": {}},
		IgnoredAll:     map[string]bool{},
		PortMapEdges:   in.PortMapEdges,
		ScenarioCovers: in.ScenarioCovers,
		ScenarioTexts:  in.ScenarioTexts,
		sourceCache:    map[string]string{},
		readFile:       readFn,
	}

	depsSet := map[string]map[string]bool{}
	embedSrcSet := map[string]map[string]bool{}
	embedTestSet := map[string]map[string]bool{}
	srcSet := map[string]map[string]bool{}
	testSet := map[string]map[string]bool{}
	activeGoFiles := map[string]bool{}
	ignoredGoFiles := map[string]string{}
	testsByGOOS := map[string]map[string]map[string]bool{}

	gooses := make([]string, 0, len(in.PackagesByGOOS))
	for goos := range in.PackagesByGOOS {
		gooses = append(gooses, goos)
	}
	slices.Sort(gooses)

	for _, goos := range gooses {
		pkgs := in.PackagesByGOOS[goos]
		if testsByGOOS[goos] == nil {
			testsByGOOS[goos] = map[string]map[string]bool{}
		}
		for _, pkg := range pkgs {
			owner := ownerOf(pkg)
			if pkg.Dir == "" || strings.HasSuffix(owner, ".test") {
				continue
			}
			rel, ok := relToRoot(absRoot, pkg.Dir)
			if !ok {
				continue
			}
			isMain := pkg.Module != nil && pkg.Module.Path == in.Module
			if isMain {
				g.MainPkgs[owner] = true
			}
			if _, exists := g.ByDir[rel]; !exists {
				g.ByDir[rel] = owner
			}

			addFile := func(target map[string]map[string]bool, abs string) {
				if target[owner] == nil {
					target[owner] = map[string]bool{}
				}
				target[owner][abs] = true
			}
			joinRel := func(name string) string {
				if rel == "." || rel == "" {
					return name
				}
				return rel + "/" + name
			}

			for _, name := range pkg.GoFiles {
				abs := filepath.Join(pkg.Dir, name)
				activeGoFiles[joinRel(name)] = true
				if strings.HasSuffix(name, "_test.go") {
					addFile(testSet, abs)
				} else {
					addFile(srcSet, abs)
				}
			}
			for _, name := range pkg.CgoFiles {
				abs := filepath.Join(pkg.Dir, name)
				activeGoFiles[joinRel(name)] = true
				if strings.HasSuffix(name, "_test.go") {
					addFile(testSet, abs)
				} else {
					addFile(srcSet, abs)
				}
			}
			for _, name := range pkg.TestGoFiles {
				abs := filepath.Join(pkg.Dir, name)
				activeGoFiles[joinRel(name)] = true
				addFile(testSet, abs)
			}
			for _, name := range pkg.XTestGoFiles {
				abs := filepath.Join(pkg.Dir, name)
				activeGoFiles[joinRel(name)] = true
				addFile(testSet, abs)
			}
			for _, name := range pkg.IgnoredGoFiles {
				abs := filepath.Join(pkg.Dir, name)
				ignoredGoFiles[joinRel(name)] = abs
				if strings.HasSuffix(name, "_test.go") {
					addFile(testSet, abs)
				} else {
					addFile(srcSet, abs)
				}
			}

			for _, name := range pkg.EmbedFiles {
				key := joinRel(name)
				if embedSrcSet[key] == nil {
					embedSrcSet[key] = map[string]bool{}
				}
				embedSrcSet[key][owner] = true
			}
			for _, name := range pkg.TestEmbedFiles {
				key := joinRel(name)
				if embedTestSet[key] == nil {
					embedTestSet[key] = map[string]bool{}
				}
				embedTestSet[key][owner] = true
			}
			for _, name := range pkg.XTestEmbedFiles {
				key := joinRel(name)
				if embedTestSet[key] == nil {
					embedTestSet[key] = map[string]bool{}
				}
				embedTestSet[key][owner] = true
			}

			if depsSet[owner] == nil {
				depsSet[owner] = map[string]bool{}
			}
			for _, dep := range pkg.Deps {
				depsSet[owner][stripVariant(dep)] = true
			}
			for _, imp := range pkg.Imports {
				depsSet[owner][stripVariant(imp)] = true
			}

			if pkg.ForTest == "" && isMain {
				if testsByGOOS[goos][owner] == nil {
					testsByGOOS[goos][owner] = map[string]bool{}
				}
				for _, f := range pkg.TestGoFiles {
					testsByGOOS[goos][owner][f] = true
				}
				for _, f := range pkg.XTestGoFiles {
					testsByGOOS[goos][owner][f] = true
				}

				narrow := len(pkg.IgnoredGoFiles) > 0
				broad := false
				for _, imp := range pkg.Imports {
					if osNarrowImports[imp] {
						narrow = true
					}
					for _, pfx := range osNarrowPrefixes {
						if strings.HasPrefix(imp, pfx) {
							narrow = true
						}
					}
					if osBroadImports[imp] {
						broad = true
					}
				}
				prodNames := make([]string, 0, len(pkg.GoFiles)+len(pkg.CgoFiles)+len(pkg.IgnoredGoFiles))
				prodNames = append(prodNames, pkg.GoFiles...)
				prodNames = append(prodNames, pkg.CgoFiles...)
				prodNames = append(prodNames, pkg.IgnoredGoFiles...)
				for _, name := range prodNames {
					if strings.HasSuffix(name, "_test.go") {
						continue
					}
					if osFilenameRE.MatchString(name) {
						narrow = true
					}
					if !narrow {
						if txt, ok := g.sourceText(filepath.Join(pkg.Dir, name)); ok && strings.Contains(txt, "runtime.GOOS") {
							narrow = true
						}
					}
				}
				if narrow {
					g.OSNarrow[owner] = true
					g.OSBroad[owner] = true
				} else if broad {
					g.OSBroad[owner] = true
				}
			}
		}
	}

	// Mark Go files ignored under every queried GOOS (excluding //go:build ignore),
	// per CI-DAG-DESIGN.md S4.
	for relPath, absPath := range ignoredGoFiles {
		if activeGoFiles[relPath] {
			continue
		}
		if txt, ok := g.sourceText(absPath); ok {
			if hasBuildIgnore(txt) {
				continue
			}
		}
		g.IgnoredAll[relPath] = true
	}

	// Compute OS-only test packages: packages with a test file that windows/darwin
	// builds and linux does not (same authority as windows-test-packages.sh).
	linuxTests := testsByGOOS["linux"]
	for _, goos := range []string{"windows", "darwin"} {
		for pkg, files := range testsByGOOS[goos] {
			lFiles := linuxTests[pkg]
			for f := range files {
				if !lFiles[f] {
					g.OSTests[goos][pkg] = true
					break
				}
			}
		}
	}

	for k, set := range embedSrcSet {
		g.EmbedSrc[k] = sortedKeys(set)
	}
	for k, set := range embedTestSet {
		g.EmbedTest[k] = sortedKeys(set)
	}
	for k, set := range srcSet {
		g.SrcFiles[k] = sortedKeys(set)
	}
	for k, set := range testSet {
		g.TestFiles[k] = sortedKeys(set)
	}

	// Direct reverse adjacency map plus explicit BFS closure so BuildGraph handles
	// both transitive go-list Deps and direct-only fixture graphs identically.
	directRev := map[string]map[string]bool{}
	for owner, deps := range depsSet {
		g.Deps[owner] = sortedKeys(deps)
		if !g.MainPkgs[owner] {
			continue
		}
		for dep := range deps {
			if dep == owner || !g.MainPkgs[dep] {
				continue
			}
			if directRev[dep] == nil {
				directRev[dep] = map[string]bool{}
			}
			directRev[dep][owner] = true
		}
	}
	for start := range g.MainPkgs {
		visited := map[string]bool{}
		queue := make([]string, 0, len(directRev[start]))
		for next := range directRev[start] {
			visited[next] = true
			queue = append(queue, next)
		}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for next := range directRev[cur] {
				if !visited[next] && next != start {
					visited[next] = true
					queue = append(queue, next)
				}
			}
		}
		if len(visited) > 0 {
			g.RevDeps[start] = sortedKeys(visited)
		}
	}

	return g, nil
}

func hasBuildIgnore(src string) bool {
	limit := min(len(src), 1024)
	for line := range strings.SplitSeq(src[:limit], "\n") {
		trimmed := strings.TrimSpace(line)
		if buildIgnoreRE.MatchString(trimmed) {
			return true
		}
		if strings.HasPrefix(trimmed, "package ") {
			return false
		}
	}
	return false
}

func relToRoot(absRoot, dir string) (string, bool) {
	if absRoot == "" {
		clean := normalizePath(dir)
		if clean == "" {
			return ".", true
		}
		return clean, true
	}
	resolved := dir
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		resolved = r
	} else if a, err := filepath.Abs(dir); err == nil {
		resolved = a
	}
	rel, err := filepath.Rel(absRoot, resolved)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	if rel == "" {
		return ".", true
	}
	return rel, true
}

func (g *Graph) sourceText(absPath string) (string, bool) {
	if txt, ok := g.sourceCache[absPath]; ok {
		return txt, true
	}
	data, err := g.readFile(absPath)
	if err != nil {
		return "", false
	}
	txt := string(data)
	g.sourceCache[absPath] = txt
	return txt, true
}

// ReverseClosure returns pkgs union every main-module package that transitively
// depends on any package in pkgs.
func (g *Graph) ReverseClosure(pkgs map[string]bool) map[string]bool {
	out := make(map[string]bool, len(pkgs))
	for p := range pkgs {
		out[p] = true
		for _, dep := range g.RevDeps[p] {
			out[dep] = true
		}
	}
	return out
}

// ForwardClosure returns start union every package that start transitively imports
// (used to determine which packages are compiled into cmd/pig for parity selection).
func (g *Graph) ForwardClosure(start string) map[string]bool {
	out := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, dep := range g.Deps[cur] {
			if !out[dep] {
				out[dep] = true
				queue = append(queue, dep)
			}
		}
	}
	return out
}

// OwnerDir walks upward from relPath's directory to find the nearest enclosing
// Go package directory other than the module root (matching changed-packages.py).
func (g *Graph) OwnerDir(relPath string) (string, bool) {
	probe := path.Dir(normalizePath(relPath))
	for probe != "" && probe != "." {
		if pkg, ok := g.ByDir[probe]; ok {
			return pkg, true
		}
		next := path.Dir(probe)
		if next == probe {
			break
		}
		probe = next
	}
	return "", false
}

// Referencing returns all main-module packages whose Go files in sourceMap
// spell relPath as a slash-separated string or filepath.Join quoted components,
// or its 3+-component parent directory, or a named glob directory (changelog.d).
// This mirrors automation/ci/changed-packages.py referencing() and path_pattern().
func (g *Graph) Referencing(relPath string, sourceMap map[string][]string) map[string]bool {
	clean := normalizePath(relPath)
	if clean == "" {
		return nil
	}
	parts := strings.Split(clean, "/")
	parent := path.Dir(clean)
	var parentParts []string
	if parent != "." && parent != "" {
		parentParts = strings.Split(parent, "/")
	}

	patterns := []string{pathPattern(parts)}
	literals := []string{parts[len(parts)-1]}

	if len(parentParts) > 0 && namedDirectories[parentParts[0]] {
		patterns = append(patterns, regexp.QuoteMeta(parentParts[0]))
		literals = append(literals, parentParts[0])
	}
	if len(parentParts) >= 3 {
		patterns = append(patterns, pathPattern(parentParts))
		literals = append(literals, parentParts[len(parentParts)-1])
	}

	needle, err := regexp.Compile(strings.Join(patterns, "|"))
	if err != nil {
		return nil
	}

	hits := map[string]bool{}
	for pkg, files := range sourceMap {
		if !g.MainPkgs[pkg] {
			continue
		}
		for _, abs := range files {
			txt, ok := g.sourceText(abs)
			if !ok {
				continue
			}
			hasLit := false
			for _, lit := range literals {
				if strings.Contains(txt, lit) {
					hasLit = true
					break
				}
			}
			if hasLit && needle.MatchString(txt) {
				hits[pkg] = true
				break
			}
		}
	}
	return hits
}

func pathPattern(parts []string) string {
	quotedParts := make([]string, len(parts))
	for i, part := range parts {
		quotedParts[i] = `"` + part + `"`
	}
	joined := regexp.QuoteMeta(strings.Join(quotedParts, ", "))
	if len(parts) == 1 {
		quoted := regexp.QuoteMeta(parts[0])
		return `"(?:\.\./)+` + quoted + `"|,\s*"` + quoted + `"`
	}
	return regexp.QuoteMeta(strings.Join(parts, "/")) + "|" + joined
}

// ShortPkg converts a full module import path to "./..." notation.
func (g *Graph) ShortPkg(importPath string) string {
	if importPath == g.Module {
		return "."
	}
	if rest, ok := strings.CutPrefix(importPath, g.Module+"/"); ok {
		return "./" + rest
	}
	return importPath
}

// LoadGraph executes `go list -e -test -deps -tags=... -json=... ./...` across
// DefaultGOOSList in root, parses PORT_MAP.md and parity scenario covers, and
// builds the unified Graph.
func LoadGraph(ctx context.Context, root string, goosList []string, tags string) (*Graph, error) {
	if len(goosList) == 0 {
		goosList = DefaultGOOSList
	}
	if tags == "" {
		tags = DefaultBuildTags
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = resolved
	}

	modulePath, err := detectMainModule(ctx, absRoot)
	if err != nil {
		return nil, err
	}

	const fields = "ImportPath,ForTest,Dir,Module,Imports,Deps,EmbedFiles,TestEmbedFiles,XTestEmbedFiles,GoFiles,TestGoFiles,XTestGoFiles,IgnoredGoFiles,CgoFiles,Error,Incomplete,DepsErrors"
	pkgsByGOOS := make(map[string][]ListPackage, len(goosList))
	for _, goos := range goosList {
		//nolint:gosec // go binary and fixed flags; GOOS comes from validated DefaultGOOSList or caller config.
		cmd := exec.CommandContext(ctx, "go", "list", "-e", "-test", "-deps", "-tags="+tags, "-json="+fields, "./...")
		cmd.Dir = absRoot
		cmd.Env = withEnv(os.Environ(), "GOOS="+goos, "GOFLAGS=")
		out, err := cmd.Output()
		if err != nil {
			if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
				return nil, fmt.Errorf("go list (GOOS=%s): %w: %s", goos, err, strings.TrimSpace(string(exitErr.Stderr)))
			}
			return nil, fmt.Errorf("go list (GOOS=%s): %w", goos, err)
		}
		pkgs, err := decodeListPackages(out, goos)
		if err != nil {
			return nil, fmt.Errorf("decode go list (GOOS=%s): %w", goos, err)
		}
		pkgsByGOOS[goos] = pkgs
	}

	portEdges, err := LoadPortMapEdges(filepath.Join(absRoot, "docs", "parity", "PORT_MAP.md"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("parse PORT_MAP.md: %w", err)
	}
	covers, texts, err := LoadScenarioCovers(absRoot, filepath.Join(absRoot, "test", "parity", "scenarios"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("parse parity scenarios: %w", err)
	}

	return BuildGraph(GraphInput{
		Root:           absRoot,
		Module:         modulePath,
		PackagesByGOOS: pkgsByGOOS,
		PortMapEdges:   portEdges,
		ScenarioCovers: covers,
		ScenarioTexts:  texts,
	})
}

func detectMainModule(ctx context.Context, absRoot string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-f", "{{.Path}}\t{{.Dir}}")
	cmd.Dir = absRoot
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("detect main module: %w", err)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		modPath, dir, ok := strings.Cut(line, "\t")
		if !ok || dir == "" {
			continue
		}
		resolved := dir
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			resolved = r
		} else if a, err := filepath.Abs(dir); err == nil {
			resolved = a
		}
		if resolved == absRoot {
			return modPath, nil
		}
	}
	return "", fmt.Errorf("no Go module rooted at %s", absRoot)
}

func isPlatformExclusionError(e *PackageError) bool {
	if e == nil {
		return false
	}
	msg := strings.ToLower(e.Err)
	return strings.Contains(msg, "build constraints exclude all go files") ||
		strings.Contains(msg, "no buildable go source files")
}

func validateListPackage(pkg ListPackage, goos string) error {
	if pkg.Error != nil && !isPlatformExclusionError(pkg.Error) {
		return fmt.Errorf("package %s (GOOS=%s): %s", pkg.ImportPath, goos, pkg.Error.Err)
	}
	for _, de := range pkg.DepsErrors {
		if de != nil && !isPlatformExclusionError(de) {
			return fmt.Errorf("package %s dep error (GOOS=%s): %s", pkg.ImportPath, goos, de.Err)
		}
	}
	if pkg.Incomplete && (pkg.Error == nil || !isPlatformExclusionError(pkg.Error)) {
		return fmt.Errorf("package %s (GOOS=%s): incomplete package metadata", pkg.ImportPath, goos)
	}
	return nil
}

func decodeListPackages(data []byte, goos string) ([]ListPackage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var pkgs []ListPackage
	for {
		var pkg ListPackage
		if err := dec.Decode(&pkg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		pkg.GOOS = goos
		if err := validateListPackage(pkg, goos); err != nil {
			return nil, err
		}
		pkgs = append(pkgs, pkg)
	}
	return pkgs, nil
}

func withEnv(base []string, overrides ...string) []string {
	out := make([]string, 0, len(base)+len(overrides))
	overrideKeys := make(map[string]bool, len(overrides))
	for _, ov := range overrides {
		if k, _, ok := strings.Cut(ov, "="); ok {
			overrideKeys[k] = true
		}
	}
	for _, kv := range base {
		if k, _, ok := strings.Cut(kv, "="); ok && overrideKeys[k] {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, overrides...)
	return out
}

var (
	portMapGoFileRE = regexp.MustCompile(`[\w./-]+\.go`)
	coversBlockRE   = regexp.MustCompile(`(?ms)^covers\s*=\s*\[(.*?)\]`)
	quotedStringRE  = regexp.MustCompile(`"([^"]+)"`)
)

// LoadPortMapEdges parses docs/parity/PORT_MAP.md into upstream path -> Go files.
func LoadPortMapEdges(portMapPath string) (map[string][]string, error) {
	data, err := os.ReadFile(portMapPath)
	if err != nil {
		return nil, err
	}
	edgesSet := map[string]map[string]bool{}
	for line := range strings.SplitSeq(string(data), "\n") {
		cols := strings.Split(line, "|")
		if len(cols) < 4 {
			continue
		}
		col1 := strings.TrimSpace(cols[1])
		if !strings.HasPrefix(col1, "`packages/") {
			continue
		}
		up := strings.Trim(col1, "`")
		for _, goFile := range portMapGoFileRE.FindAllString(cols[2], -1) {
			if edgesSet[up] == nil {
				edgesSet[up] = map[string]bool{}
			}
			edgesSet[up][normalizePath(goFile)] = true
		}
	}
	out := make(map[string][]string, len(edgesSet))
	for up, set := range edgesSet {
		out[up] = sortedKeys(set)
	}
	return out, nil
}

// LoadScenarioCovers walks test/parity/scenarios/*.toml and extracts each scenario's
// covers = [...] upstream paths and raw TOML text.
func LoadScenarioCovers(absRoot, scenariosDir string) (map[string][]string, map[string]string, error) {
	if _, err := os.Stat(scenariosDir); err != nil {
		return nil, nil, err
	}
	covers := map[string][]string{}
	texts := map[string]string{}
	err := filepath.WalkDir(scenariosDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".toml") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(absRoot, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		body := string(data)
		texts[rel] = body
		var list []string
		if m := coversBlockRE.FindStringSubmatch(body); m != nil {
			for _, sm := range quotedStringRE.FindAllStringSubmatch(m[1], -1) {
				list = append(list, sm[1])
			}
		}
		covers[rel] = list
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return covers, texts, nil
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
