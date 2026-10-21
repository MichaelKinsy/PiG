// SPDX-License-Identifier: MIT

package upgrade

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/format"
	"go/importer"
	"go/token"
	"go/types"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// maxPasses bounds the plan-apply-recheck loop. A rewrite can reveal a use the
// compiler could not type before, such as a field read on a value that only
// has a type once its getter returns two results.
const maxPasses = 4

// Rewrite is one change the plan makes to a file.
type Rewrite struct {
	Rule   string
	Symbol string
	Line   int
}

// FileChange is one rewritten file, or one the plan adds: Created is set for a
// file with no Before.
type FileChange struct {
	Path          string
	Before, After []byte
	Rewrites      []Rewrite
	Created       bool
}

// Skipped is a use of a rewritable change that needs a hand edit.
type Skipped struct {
	Rule   string
	Symbol string
	File   string
	Line   int
	Reason string
	// Suggest is the form to write in its place, when the rule has one.
	Suggest string
}

// Result is what an upgrade does to an extension's source, and what it leaves
// for the author. The lines that Skipped, Remaining and Errors name are lines of
// the source before the rewrites: Skipped and Remaining are uses the compiler
// still rejects, and an upgrade that does not build restores that source.
type Result struct {
	// Files are the rewritten files, in path order. Nothing is written to disk.
	Files []FileChange
	// Skipped are uses the rules recognize but cannot rewrite safely.
	Skipped []Skipped
	// Remaining is the SDK drift still in the source after the rewrites,
	// classified from the compiler's errors. It includes the changes that have
	// no mechanical rewrite.
	Remaining []Drift
	// Errors are the compiler errors that are not SDK drift.
	Errors []string
}

// Changed reports whether the plan rewrites any file.
func (r *Result) Changed() bool { return len(r.Files) > 0 }

// Session is a loaded extension: its packages and the importer that checks
// them again after a rewrite.
type Session struct {
	fset    *token.FileSet
	listed  []listedPackage
	roots   []string
	gc      types.ImporterFrom
	initial []*Package
	// sdkVersion is the SDK version the extension's module requires, such as
	// v0.4.1, or "" when the listing names none.
	sdkVersion string
}

// Open lists and checks the extension's packages. See [LoadOptions].
func Open(ctx context.Context, opts LoadOptions) (*Session, error) {
	listed, roots, exports, err := listPackages(ctx, opts)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	lookup := func(path string) (io.ReadCloser, error) {
		file, ok := exports[path]
		if !ok {
			return nil, fmt.Errorf("no export data for %q", path)
		}
		return os.Open(file)
	}
	gc, ok := importer.ForCompiler(fset, "gc", lookup).(types.ImporterFrom)
	if !ok {
		return nil, fmt.Errorf("the gc importer does not resolve imports from a directory")
	}
	session := &Session{fset: fset, listed: listed, roots: roots, gc: gc}
	for _, entry := range listed {
		if IsSDKPath(entry.ImportPath) && entry.Module != nil {
			session.sdkVersion = entry.Module.Version
		}
	}
	session.initial, err = session.check(nil, nil)
	if err != nil {
		return nil, err
	}
	return session, nil
}

// check type checks every package under the roots in dependency order. A
// source in overlay replaces the file on disk, and a file in added joins the
// package of its directory.
func (s *Session) check(overlay map[string][]byte, added map[string][]string) ([]*Package, error) {
	checked := map[string]*types.Package{}
	var packages []*Package
	for _, entry := range s.listed {
		if !entry.fromSource(s.roots) {
			continue
		}
		paths := make([]string, len(entry.GoFiles))
		for i, name := range entry.GoFiles {
			paths[i] = filepath.Join(entry.Dir, name)
		}
		paths = append(paths, added[entry.Dir]...)
		goVersion := ""
		if entry.Module != nil && entry.Module.GoVersion != "" {
			goVersion = "go" + entry.Module.GoVersion
		}
		imports := sourceImporter{gc: s.gc, checked: checked, importMap: entry.ImportMap}
		pkg, err := checkPackage(s.fset, entry.ImportPath, paths, imports, goVersion, overlay)
		if err != nil {
			return nil, err
		}
		checked[entry.ImportPath] = pkg.Types
		packages = append(packages, pkg)
	}
	return packages, nil
}

// Plan rewrites the extension's source in memory until the compiler has no
// more drift a rule can rewrite, and reports the result. It never writes a
// file.
func (s *Session) Plan() (*Result, error) {
	overlay := map[string][]byte{}
	original := map[string][]byte{}
	rewrites := map[string][]Rewrite{}
	legacy := map[string]bool{}
	registries := map[string]*helperRegistry{}
	added := map[string][]string{}
	created := map[string]bool{}
	var skipped []Skipped
	packages := s.initial
	for pass := 0; pass < maxPasses; pass++ {
		edited := false
		skipped = nil
		for _, pkg := range packages {
			for _, file := range pkg.Files {
				planner := newFilePlanner(pkg, file)
				planner.sdkVersion = s.sdkVersion
				planner.legacy = legacy[file.Path]
				planner.registries = registries
				planner.run()
				for _, skip := range planner.skipped {
					if before, edited := original[file.Path]; edited {
						skip.Line = originalLine(before, file.Source, skip.Line)
					}
					skipped = append(skipped, skip)
				}
				if len(planner.accepted) == 0 {
					continue
				}
				after, err := planner.apply()
				if err != nil {
					return nil, fmt.Errorf("rewrite %s: %w", file.Path, err)
				}
				if _, seen := original[file.Path]; !seen {
					original[file.Path] = file.Source
				}
				overlay[file.Path] = after
				rewrites[file.Path] = append(rewrites[file.Path], planner.rewrites...)
				legacy[file.Path] = legacy[file.Path] || planner.wroteLegacy
				edited = true
			}
		}
		if !edited {
			break
		}
		for _, key := range slices.Sorted(maps.Keys(registries)) {
			registry := registries[key]
			if len(registry.used) == registry.written {
				continue
			}
			if registry.path == "" {
				registry.choosePath()
				added[registry.dir] = append(added[registry.dir], registry.path)
				created[registry.path] = true
			}
			source, err := registry.source()
			if err != nil {
				return nil, fmt.Errorf("write the getter helpers of %s: %w", registry.dir, err)
			}
			overlay[registry.path] = source
			registry.written = len(registry.used)
		}
		var err error
		if packages, err = s.check(overlay, added); err != nil {
			return nil, err
		}
	}
	result := &Result{Skipped: skipped}
	for path, after := range overlay {
		result.Files = append(result.Files, FileChange{Path: path, Before: original[path], After: after, Rewrites: rewrites[path], Created: created[path]})
	}
	slices.SortFunc(result.Files, func(a, b FileChange) int { return strings.Compare(a.Path, b.Path) })
	read := func(path string) ([]byte, error) {
		if data, ok := overlay[path]; ok {
			return data, nil
		}
		return os.ReadFile(path)
	}
	var diagnostics []Diagnostic
	for _, pkg := range packages {
		for _, typeErr := range pkg.Errors {
			position := pkg.Fset.Position(typeErr.Pos)
			diagnostics = append(diagnostics, Diagnostic{File: position.Filename, Line: position.Line, Col: position.Column, Message: typeErr.Msg})
		}
	}
	result.Remaining = Classify(diagnostics, read)
	for i, drift := range result.Remaining {
		if after, edited := overlay[drift.File]; edited {
			result.Remaining[i].Line = originalLine(original[drift.File], after, drift.Line)
		}
	}
	for _, diagnostic := range diagnostics {
		if _, _, ok := classifyDiagnostic(diagnostic, read, map[string]*parsedFile{}); !ok {
			line := diagnostic.Line
			if after, edited := overlay[diagnostic.File]; edited {
				line = originalLine(original[diagnostic.File], after, line)
			}
			result.Errors = append(result.Errors, fmt.Sprintf("%s:%d:%d: %s", diagnostic.File, line, diagnostic.Col, diagnostic.Message))
		}
	}
	return result, nil
}

// originalLine maps a line of after back to the line of before it comes from.
func originalLine(before, after []byte, line int) int {
	if lines := originalLines(before, after); line > 0 && line < len(lines) {
		return lines[line]
	}
	return line
}

// edit replaces src[start:end].
type edit struct {
	start, end int
	text       string
}

// site is one rewrite: edits that apply together or not at all.
type site struct {
	rewrites []Rewrite
	edits    []edit
	// legacy is set when the rewrite shows the file predates SDK 0.3.0.
	legacy bool
	// helper names the getter whose generated helper the rewrite calls.
	helper string
}

// filePlanner plans the rewrites of one file.
type filePlanner struct {
	pkg     *Package
	file    *File
	tokens  *token.File
	errors  []types.Error
	sdk     string
	used    map[string]bool
	sites   []site
	skipped []Skipped
	// accepted and rewrites are the sites that do not overlap an earlier site.
	accepted []site
	rewrites []Rewrite
	// legacy is set when an earlier pass rewrote a getter whose call shows
	// the file predates SDK 0.3.0, and wroteLegacy when this pass does.
	legacy, wroteLegacy bool
	// sdkVersion is the SDK version the module requires.
	sdkVersion string
	// registries are the getter helpers each package needs, by import path.
	registries map[string]*helperRegistry
}

func newFilePlanner(pkg *Package, file *File) *filePlanner {
	planner := &filePlanner{pkg: pkg, file: file, tokens: pkg.Fset.File(file.Syntax.Pos()), used: map[string]bool{}}
	for _, typeErr := range pkg.Errors {
		if pkg.Fset.Position(typeErr.Pos).Filename == file.Path {
			planner.errors = append(planner.errors, typeErr)
		}
	}
	for name := range sdkImportNames(file.Syntax) {
		if planner.sdk == "" || name == "sdk" {
			planner.sdk = name
		}
	}
	for _, spec := range file.Syntax.Imports {
		if spec.Name != nil {
			planner.used[spec.Name.Name] = true
		}
	}
	ast.Inspect(file.Syntax, func(node ast.Node) bool {
		if ident, ok := node.(*ast.Ident); ok {
			planner.used[ident.Name] = true
		}
		return true
	})
	for _, name := range pkg.Types.Scope().Names() {
		planner.used[name] = true
	}
	return planner
}

func (p *filePlanner) run() {
	// A getter spread into a variadic call compiles, so a file without errors
	// still has its getters planned.
	p.planGetters()
	if len(p.errors) > 0 || p.legacy {
		p.planOptionalBools()
		p.planContextUsage(p.legacy)
	}
	p.accept()
}

func (p *filePlanner) offset(pos token.Pos) int { return p.tokens.Offset(pos) }

func (p *filePlanner) text(from, to token.Pos) string {
	return string(p.file.Source[p.offset(from):p.offset(to)])
}

func (p *filePlanner) line(pos token.Pos) int { return p.tokens.Line(pos) }

func (p *filePlanner) skip(rule, symbol string, pos token.Pos, reason string) {
	p.skipped = append(p.skipped, Skipped{Rule: rule, Symbol: symbol, File: p.file.Path, Line: p.line(pos), Reason: reason})
}

// accept keeps the sites that do not overlap an earlier site; an overlapping
// site waits for the next pass.
func (p *filePlanner) accept() {
	var taken []edit
	for _, candidate := range p.sites {
		clash := false
		for _, e := range candidate.edits {
			for _, other := range taken {
				if e.start < other.end && other.start < e.end {
					clash = true
				}
			}
		}
		if clash {
			continue
		}
		taken = append(taken, candidate.edits...)
		p.accepted = append(p.accepted, candidate)
		p.rewrites = append(p.rewrites, candidate.rewrites...)
		p.wroteLegacy = p.wroteLegacy || candidate.legacy
		if candidate.helper != "" {
			p.registry().used[candidate.helper] = true
		}
	}
}

// apply splices the accepted edits into the source, then formats the file when
// it was formatted before, so an author's own layout stays.
func (p *filePlanner) apply() ([]byte, error) {
	var edits []edit
	for _, accepted := range p.accepted {
		edits = append(edits, accepted.edits...)
	}
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	var out bytes.Buffer
	cursor := 0
	for _, e := range edits {
		if e.start < cursor {
			return nil, fmt.Errorf("overlapping edits at offset %d", e.start)
		}
		out.Write(p.file.Source[cursor:e.start])
		out.WriteString(e.text)
		cursor = e.end
	}
	out.Write(p.file.Source[cursor:])
	result := out.Bytes()
	if formatted, err := format.Source(result); err != nil {
		return nil, fmt.Errorf("the rewrite does not parse: %w", err)
	} else if wasFormatted(p.file.Source) {
		result = formatted
	}
	return result, nil
}

func wasFormatted(source []byte) bool {
	formatted, err := format.Source(source)
	return err == nil && bytes.Equal(formatted, source)
}

// qualifier names a package the way the file does.
func (p *filePlanner) qualifier(pkg *types.Package) string {
	if pkg == p.pkg.Types {
		return ""
	}
	for _, spec := range p.file.Syntax.Imports {
		path := strings.Trim(spec.Path.Value, "`\"")
		if path != pkg.Path() {
			continue
		}
		if spec.Name != nil && spec.Name.Name != "_" && spec.Name.Name != "." {
			return spec.Name.Name
		}
	}
	return pkg.Name()
}

// fresh returns a name the file does not use.
func (p *filePlanner) fresh(base string) string {
	name := base
	for n := 2; p.used[name]; n++ {
		name = fmt.Sprintf("%s%d", base, n)
	}
	p.used[name] = true
	return name
}

// indentAt returns the leading whitespace of the line holding pos.
func (p *filePlanner) indentAt(pos token.Pos) string {
	start := p.offset(p.tokens.LineStart(p.line(pos)))
	end := start
	for end < len(p.file.Source) && (p.file.Source[end] == ' ' || p.file.Source[end] == '\t') {
		end++
	}
	return string(p.file.Source[start:end])
}

// snippet formats statements the way gofmt lays them out, one line each.
func snippet(statements string) ([]string, error) {
	source := "package p\n\nfunc _() {\n" + statements + "\n}\n"
	formatted, err := format.Source([]byte(source))
	if err != nil {
		return nil, err
	}
	body := strings.TrimSuffix(strings.TrimPrefix(string(formatted), "package p\n\nfunc _() {\n"), "}\n")
	var lines []string
	for line := range strings.SplitSeq(strings.TrimRight(body, "\n"), "\n") {
		lines = append(lines, strings.TrimPrefix(line, "\t"))
	}
	return lines, nil
}

// insertion is the text placed before a statement: the lines at the
// statement's indentation, and a line break that leaves the statement where it
// was.
func insertion(lines []string, indent string) string {
	return strings.Join(lines, "\n"+indent) + "\n" + indent
}

// Drift lists every SDK change the extension uses: those the plan rewrites,
// those it lists for the author, and those the compiler still rejects. A change
// is listed once, in symbol order, at its first use.
func (r *Result) Drift() []Drift {
	byRule := map[string]Drift{}
	note := func(rule, symbol, file string, line, count int) {
		drift, seen := byRule[symbol]
		if !seen {
			ruleInfo, _ := RuleByID(rule)
			change, _ := changeFor(symbol)
			drift = Drift{Rule: rule, Symbol: symbol, Old: change.Old, New: change.New, Since: change.Since, Rewrites: ruleInfo.Rewrites, File: file, Line: line}
		}
		drift.Count += count
		byRule[symbol] = drift
	}
	for _, change := range r.Files {
		for _, rewrite := range change.Rewrites {
			note(rewrite.Rule, rewrite.Symbol, change.Path, rewrite.Line, 1)
		}
	}
	remaining := map[string]bool{}
	for _, drift := range r.Remaining {
		remaining[drift.Symbol] = true
		note(drift.Rule, drift.Symbol, drift.File, drift.Line, drift.Count)
	}
	for _, skipped := range r.Skipped {
		if !remaining[skipped.Symbol] {
			note(skipped.Rule, skipped.Symbol, skipped.File, skipped.Line, 1)
		}
		remaining[skipped.Symbol] = true
	}
	// A change with a use left for the author needs a manual edit.
	for symbol := range remaining {
		drift := byRule[symbol]
		drift.Rewrites = false
		byRule[symbol] = drift
	}
	return slices.SortedFunc(maps.Values(byRule), func(a, b Drift) int { return strings.Compare(a.Symbol, b.Symbol) })
}
