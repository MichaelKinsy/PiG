// Command portmapskeleton writes one marker-test skeleton per Pi file that has no `// pi:` marker test yet.
//
// A PORT_MAP ✅ row whose Pi file has no exported ledger member is proven only by a Go test that carries `// pi: <Pi path>`, executes a
// cited Go file and is red under a mutation of that file (test/parity/cmd/portmapcheck). Writing that test is the work. This command removes
// the mechanical part: it reads the unproven rows (the `pi | reason` lines of `portmapcheck -report`), resolves each row's cited Go file from
// docs/parity/PORT_MAP.md, and writes `<dir of the cited file>/pi_<stem>_skeleton_test.go` next to the first cited file that declares a
// function with a body (a mutation needs a statement to break), holding
//
//   - a `portmap_skeleton` build tag, so the file compiles only on request and never changes a normal test run;
//   - a `// pi-unproven: <Pi path>` line, which portmapcheck does not read, so the row stays reported "no Go test carries" until a lane promotes it;
//   - the cited entry points (exported functions and constructors) and the Pi test files that exercise the Pi file, as the cases to port;
//   - one test that fails with `t.Fatal`, the red starting point.
//
// A lane fills in the assertions, deletes the build tag line, turns `pi-unproven` into `pi` and proves the test with a mutation. The output is
// deterministic: the same rows and tree produce the same bytes, and an existing file is never overwritten. `-skip` names Pi files that another
// branch already proves with a `// pi:` marker, which the tree's report cannot see yet.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

var (
	rowRe    = regexp.MustCompile("^\\|\\s*`([^`]+)`\\s*\\|\\s*(.*?)\\s*\\|\\s*(✅|🟡|⬜|⏸|🔴|n/a)\\s*\\|\\s*$")
	tokenRe  = regexp.MustCompile(`[A-Za-z0-9_@./*-]*(?:\{[^}]*\})?[A-Za-z0-9_@./*-]*`)
	reasonRe = regexp.MustCompile("no Go test carries `// pi: ")
)

// skeleton is one file to write.
type skeleton struct {
	Pi       string
	Path     string // repository-relative Go test file
	Cited    string // the cited Go file the test must execute
	Entries  []string
	PiTests  []string
	TestName string
}

func main() {
	root := flag.String("root", ".", "repository root")
	rows := flag.String("rows", "", "file of `pi | reason` lines (portmapcheck -report output); - reads stdin")
	skip := flag.String("skip", "", "file of Pi paths (first field of each line; # starts a comment) that another branch already marks or is marking")
	dry := flag.Bool("n", false, "list what would be written")
	flag.Parse()
	if *rows == "" {
		fmt.Fprintln(os.Stderr, "portmapskeleton: -rows is required")
		os.Exit(2)
	}
	pis, err := readRows(*rows)
	if err != nil {
		fmt.Fprintln(os.Stderr, "portmapskeleton:", err)
		os.Exit(2)
	}
	var skipped []string
	if *skip != "" {
		excluded, err := readSkip(*skip)
		if err != nil {
			fmt.Fprintln(os.Stderr, "portmapskeleton:", err)
			os.Exit(2)
		}
		var dropped []string
		pis, dropped = exclude(pis, excluded)
		skipped = append(skipped, dropped...)
	}
	plan, unresolved, err := build(*root, pis)
	skipped = append(skipped, unresolved...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "portmapskeleton:", err)
		os.Exit(2)
	}
	written := 0
	for _, s := range plan {
		if *dry {
			fmt.Println(s.Path, "<-", s.Pi)
			continue
		}
		ok, err := write(*root, s)
		if err != nil {
			fmt.Fprintln(os.Stderr, "portmapskeleton:", err)
			os.Exit(2)
		}
		if ok {
			written++
		} else {
			skipped = append(skipped, s.Pi+" | "+s.Path+" exists")
		}
	}
	slices.Sort(skipped)
	for _, s := range skipped {
		fmt.Println("SKIP", s)
	}
	fmt.Printf("portmapskeleton: %d rows, %d skeletons planned, %d written, %d skipped\n", len(pis), len(plan), written, len(skipped))
}

func readRows(file string) ([]string, error) {
	var f *os.File
	if file == "-" {
		f = os.Stdin
	} else {
		var err error
		if f, err = os.Open(file); err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
	}
	var pis []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		pi, reason, ok := strings.Cut(sc.Text(), " | ")
		if ok && reasonRe.MatchString(reason) && strings.HasPrefix(pi, "packages/") {
			pis = append(pis, pi)
		}
	}
	slices.Sort(pis)
	return slices.Compact(pis), sc.Err()
}

// readSkip reads the Pi paths a skeleton must not be written for: the rows another branch already proves with a `// pi:` marker.
func readSkip(file string) (map[string]bool, error) {
	body, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for line := range strings.SplitSeq(string(body), "\n") {
		line, _, _ = strings.Cut(line, "#")
		if f := strings.Fields(line); len(f) > 0 {
			out[f[0]] = true
		}
	}
	return out, nil
}

// exclude drops the listed Pi paths and reports each as skipped.
func exclude(pis []string, excluded map[string]bool) (kept, skipped []string) {
	for _, pi := range pis {
		if excluded[pi] {
			skipped = append(skipped, pi+" | listed in -skip")
			continue
		}
		kept = append(kept, pi)
	}
	return kept, skipped
}

// build resolves every Pi file to a skeleton. A row whose cited Go file cannot be found is skipped and reported.
func build(root string, pis []string) ([]skeleton, []string, error) {
	cells, err := readCells(root)
	if err != nil {
		return nil, nil, err
	}
	tests := piTestIndex(root)
	var plan []skeleton
	var skipped []string
	used := map[string]string{}
	for _, pi := range pis {
		cell, ok := cells[pi]
		if !ok {
			skipped = append(skipped, pi+" | no PORT_MAP row")
			continue
		}
		cited := citedGoFiles(root, cell)
		if len(cited) == 0 {
			skipped = append(skipped, pi+" | no existing non-test Go file cited")
			continue
		}
		target := mutableTarget(root, cited)
		dir := path.Dir(target)
		file := path.Join(dir, "pi_"+snake(strings.TrimSuffix(path.Base(pi), path.Ext(pi)))+"_skeleton_test.go")
		if other, dup := used[file]; dup {
			// Two Pi files with one stem in one package (index.ts, types.ts): the package path disambiguates.
			file = path.Join(dir, "pi_"+snake(strings.TrimPrefix(strings.TrimSuffix(pi, path.Ext(pi)), "packages/"))+"_skeleton_test.go")
			_ = other
		}
		used[file] = pi
		s := skeleton{Pi: pi, Path: file, Cited: target, TestName: "TestPi" + camel(strings.TrimPrefix(strings.TrimSuffix(pi, path.Ext(pi)), "packages/")) + "Skeleton"}
		for _, c := range cited {
			if path.Dir(c) == dir {
				s.Entries = append(s.Entries, entryPoints(root, c)...)
			}
		}
		slices.Sort(s.Entries)
		s.Entries = slices.Compact(s.Entries)
		s.PiTests = tests.forSource(pi)
		plan = append(plan, s)
	}
	return plan, skipped, nil
}

func readCells(root string) (map[string]string, error) {
	f, err := os.Open(filepath.Join(root, "docs/parity/PORT_MAP.md"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	cells := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if m := rowRe.FindStringSubmatch(sc.Text()); m != nil && m[3] == "✅" {
			cells[m[1]] = m[2]
		}
	}
	return cells, sc.Err()
}

// citedGoFiles lists the existing, non-generated, non-test Go files a PORT_MAP cell names, in cell order. A `dir/{a,b}.go` group expands to
// its members and a cited Go package directory contributes its Go files.
func citedGoFiles(root, cell string) []string {
	var out []string
	add := func(c string) {
		if strings.ContainsAny(c, "*{") || strings.HasSuffix(c, "_test.go") || strings.Contains(c, "generated") || !strings.HasSuffix(c, ".go") {
			return
		}
		if _, err := os.Stat(filepath.Join(root, c)); err == nil && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	for _, tok := range tokenRe.FindAllString(cell, -1) {
		if tok == "" {
			continue
		}
		if before, group, ok := strings.Cut(tok, "{"); ok {
			group, after, _ := strings.Cut(group, "}")
			for m := range strings.SplitSeq(group, ",") {
				add(before + m + after)
			}
			continue
		}
		if strings.HasSuffix(tok, ".go") {
			add(tok)
			continue
		}
		if fi, err := os.Stat(filepath.Join(root, tok)); err == nil && fi.IsDir() && strings.Contains(tok, "/") {
			files, _ := filepath.Glob(filepath.Join(root, tok, "*.go"))
			slices.Sort(files)
			for _, f := range files {
				rel, _ := filepath.Rel(root, f)
				add(filepath.ToSlash(rel))
			}
		}
	}
	return out
}

// mutableTarget returns the first cited file that declares a function with a body, falling back to the first cited file. A marker test is
// proven by mutating a covered statement of the file it targets, so a file of only types, constants or embed directives (embed.go before
// internal/codingagent/changelog.go) cannot be the target while a later cited file holds code.
func mutableTarget(root string, cited []string) string {
	for _, c := range cited {
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, c), nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil && len(fn.Body.List) > 0 {
				return c
			}
		}
	}
	return cited[0]
}

// entryPoints lists the exported functions, methods and types of a Go file, which are the calls a marker test can make.
func entryPoints(root, file string) []string {
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, file), nil, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	var out []string
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if !d.Name.IsExported() {
				continue
			}
			name := d.Name.Name
			if d.Recv != nil && len(d.Recv.List) == 1 {
				name = recvName(d.Recv.List[0].Type) + "." + name
			}
			out = append(out, name)
		case *ast.GenDecl:
			for _, s := range d.Specs {
				if t, ok := s.(*ast.TypeSpec); ok && t.Name.IsExported() {
					out = append(out, "type "+t.Name.Name)
				}
			}
		}
	}
	return out
}

func recvName(x ast.Expr) string {
	switch t := x.(type) {
	case *ast.StarExpr:
		return recvName(t.X)
	case *ast.IndexExpr:
		return recvName(t.X)
	case *ast.IndexListExpr:
		return recvName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return "?"
}

// piTests indexes the Pi `*.test.ts` files and the source file stems of the pinned mirror by package.
type piTests struct {
	tests   map[string][]string
	sources map[string]map[string]bool
}

func piTestIndex(root string) piTests {
	idx := piTests{tests: map[string][]string{}, sources: map[string]map[string]bool{}}
	base := filepath.Join(root, ".upstream/current/packages")
	pkgs, _ := os.ReadDir(base)
	for _, p := range pkgs {
		idx.sources[p.Name()] = map[string]bool{}
		_ = filepath.WalkDir(filepath.Join(base, p.Name()), func(file string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() && d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			switch {
			case d.IsDir():
			case strings.HasSuffix(file, ".test.ts"):
				rel, _ := filepath.Rel(filepath.Join(root, ".upstream/current"), file)
				idx.tests[p.Name()] = append(idx.tests[p.Name()], filepath.ToSlash(rel))
			case strings.HasSuffix(file, ".ts") && !strings.HasSuffix(file, ".d.ts"):
				idx.sources[p.Name()][strings.TrimSuffix(d.Name(), ".ts")] = true
			}
			return nil
		})
	}
	for _, v := range idx.tests {
		slices.Sort(v)
	}
	return idx
}

// forSource returns the Pi tests of the package that name the source's stem (ssh.ts: ssh.test.ts, ssh-external.test.ts). A test belongs to
// the longest source stem it names, so agent-session-runtime.test.ts tests agent-session-runtime.ts and is not listed for agent-session.ts.
func (t piTests) forSource(pi string) []string {
	parts := strings.SplitN(pi, "/", 3)
	if len(parts) < 3 {
		return nil
	}
	stem := strings.TrimSuffix(path.Base(pi), path.Ext(pi))
	var out []string
	for _, f := range t.tests[parts[1]] {
		tb := strings.TrimSuffix(path.Base(f), ".test.ts")
		if names(tb, stem) && !claimedByLonger(tb, stem, t.sources[parts[1]]) {
			out = append(out, f)
		}
	}
	return out
}

// names reports whether test stem tb names source stem s: equal, or s followed by `-` or `.`.
func names(tb, s string) bool {
	return tb == s || strings.HasPrefix(tb, s+"-") || strings.HasPrefix(tb, s+".")
}

// claimedByLonger reports whether test stem tb names a source stem longer than stem, which then owns the test.
func claimedByLonger(tb, stem string, sources map[string]bool) bool {
	for s := range sources {
		if len(s) > len(stem) && names(tb, s) {
			return true
		}
	}
	return false
}

func write(root string, s skeleton) (bool, error) {
	full := filepath.Join(root, s.Path)
	if _, err := os.Stat(full); err == nil {
		return false, nil
	}
	pkg, err := packageOf(root, s.Cited)
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(full, render(s, pkg), 0o644)
}

func packageOf(root, file string) (string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, file), nil, parser.PackageClauseOnly)
	if err != nil {
		return "", err
	}
	return f.Name.Name, nil
}

func render(s skeleton, pkg string) []byte {
	var b strings.Builder
	b.WriteString("//go:build portmap_skeleton\n\n")
	fmt.Fprintf(&b, "package %s\n\n", pkg)
	b.WriteString("import \"testing\"\n\n")
	fmt.Fprintf(&b, "// pi-unproven: %s\n", s.Pi)
	fmt.Fprintf(&b, "//\n// Skeleton for the marker test of %s. Port its cases against %s: call the entry point, assert the Pi result, then\n", s.Pi, s.Cited)
	b.WriteString("// delete the build tag line, change `pi-unproven` to `pi`, and prove the test red by mutating a covered line of the cited file.\n")
	if len(s.PiTests) > 0 {
		b.WriteString("//\n// Pi tests of this file (port these cases):\n")
		for _, t := range s.PiTests {
			fmt.Fprintf(&b, "//   - %s\n", t)
		}
	}
	if len(s.Entries) > 0 {
		b.WriteString("//\n// Entry points in the cited file:\n")
		for _, e := range s.Entries {
			fmt.Fprintf(&b, "//   - %s\n", e)
		}
	}
	fmt.Fprintf(&b, "func %s(t *testing.T) {\n\tt.Fatal(\"skeleton: port the %s cases against %s\")\n}\n", s.TestName, path.Base(s.Pi), s.Cited)
	return []byte(b.String())
}

// snake turns a path or file stem into a Go file-name fragment.
func snake(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// camel turns a path into a Go identifier fragment.
func camel(s string) string {
	var b strings.Builder
	up := true
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			up = true
			continue
		}
		if up {
			r = unicode.ToUpper(r)
			up = false
		}
		b.WriteRune(r)
	}
	return b.String()
}
