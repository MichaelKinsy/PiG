// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"golang.org/x/tools/go/packages"
)

// finding is the sites of one pattern in one declaration. The baseline key has no line number, so unrelated edits do not churn it.
type finding struct {
	File    string `toml:"file"`
	Func    string `toml:"func"`
	Pattern string `toml:"pattern"`
	Detail  string `toml:"detail"`
	Count   int    `toml:"count"`
	// Class says how hot the site is: "cold", "hot-owned:<branch>" (another lane is fixing it) or "hot-kept:<reason>". Required for the patterns needsClass lists; carried over by -print.
	Class string `toml:"class,omitempty"`
	Lines []int  `toml:"-"`
}

func (f finding) key() string { return f.File + "|" + f.Func + "|" + f.Pattern + "|" + f.Detail }

type baselineFile struct {
	Hit []finding `toml:"hit"`
}

const sdkJSONPath = "github.com/MichaelKinsy/PiG/extensions/sdk/json"

func isJSONPkg(p string) bool {
	return p == "encoding/json" || p == "encoding/json/v2" || p == sdkJSONPath
}

// hotPackage reports whether rel is in a package where env, stat and loop-format sites count.
func hotPackage(rel string) bool {
	for _, prefix := range []string{"agent/", "ai/", "coding/", "extensions/sdk/", "internal/codingagent/"} {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return false
}

func isToolingPath(rel string) bool {
	for _, prefix := range []string{"test/", "tests/", "automation/", "tmp/", "examples/", "docs/", "third_party/", "wt/", "evals/", "internal/evals/", "parity/", "piglets/", "cmd/"} {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return false
}

func declarationName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	recv := fn.Recv.List[0].Type
	for {
		switch t := recv.(type) {
		case *ast.StarExpr:
			recv = t.X
			continue
		case *ast.IndexExpr:
			recv = t.X
			continue
		case *ast.IndexListExpr:
			recv = t.X
			continue
		case *ast.Ident:
			return t.Name + "." + fn.Name.Name
		}
		return fn.Name.Name
	}
}

type scanner struct {
	pkg     *packages.Package
	rel     string
	hot     bool
	allowed map[int]bool // lines covered by //perf:ok <reason>
	counts  map[string]*finding
}

func (s *scanner) add(owner, pattern, detail string, pos token.Pos) {
	line := s.pkg.Fset.Position(pos).Line
	if s.allowed[line] {
		return
	}
	f := finding{File: s.rel, Func: owner, Pattern: pattern, Detail: detail}
	e := s.counts[f.key()]
	if e == nil {
		f.Lines = nil
		e = &f
		s.counts[f.key()] = e
	}
	e.Count++
	e.Lines = append(e.Lines, line)
}

// callee returns the package path and name of the function or method a call invokes.
func (s *scanner) callee(call *ast.CallExpr) (string, string) {
	var id *ast.Ident
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		id = fun.Sel
	case *ast.Ident:
		id = fun
	case *ast.IndexExpr:
		switch x := fun.X.(type) {
		case *ast.Ident:
			id = x
		case *ast.SelectorExpr:
			id = x.Sel
		}
	}
	if id == nil {
		return "", ""
	}
	obj := s.pkg.TypesInfo.Uses[id]
	if obj == nil || obj.Pkg() == nil {
		return "", ""
	}
	return obj.Pkg().Path(), obj.Name()
}

func isMarshal(path, name string) bool {
	return isJSONPkg(path) && (name == "Marshal" || name == "MarshalIndent" || name == "MarshalWrite" || name == "MarshalEncode")
}

func isUnmarshal(path, name string) bool {
	return isJSONPkg(path) && (name == "Unmarshal" || name == "UnmarshalRead" || name == "UnmarshalDecode")
}

func isMessageSlice(t types.Type) bool {
	if t == nil {
		return false
	}
	sl, ok := t.Underlying().(*types.Slice)
	if !ok {
		return false
	}
	n, ok := types.Unalias(sl.Elem()).(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return false
	}
	p := n.Obj().Pkg().Path()
	name := n.Obj().Name()
	return name == "AgentMessage" || (name == "Message" && (strings.HasSuffix(p, "/agent") || strings.HasSuffix(p, "/ai")))
}

func baseIdent(e ast.Expr) *ast.Ident {
	for {
		switch t := e.(type) {
		case *ast.Ident:
			return t
		case *ast.ParenExpr:
			e = t.X
		case *ast.CallExpr: // string(b), []byte(s)
			if len(t.Args) != 1 {
				return nil
			}
			e = t.Args[0]
		default:
			return nil
		}
	}
}

func (s *scanner) scanFunc(fn *ast.FuncDecl) {
	owner := declarationName(fn)
	info := s.pkg.TypesInfo
	marshalled := map[types.Object]bool{}
	var deepEqualPos []token.Pos
	var marshalPos []token.Pos
	var unmarshals []*ast.CallExpr
	loopDepth := 0
	fnHot := hotFunc(fn)
	// encoded maps a variable to the encoder whose JSON text it holds (decode-own-encode).
	encoded := map[types.Object]string{}
	var decodes []*ast.CallExpr
	// puts are the sync.Pool Puts of a growable value; capChecked is set by any cap read in the function (pool-uncapped).
	var puts []*ast.CallExpr
	capChecked := false
	// nestedMarshal is set in a MarshalJSON method, whose own Marshal calls re-encode what the outer encoder compacts again (nested-marshal).
	nestedMarshal := fn.Recv != nil && fn.Name.Name == "MarshalJSON"
	var receiver types.Object
	if nestedMarshal && len(fn.Recv.List) == 1 && len(fn.Recv.List[0].Names) == 1 {
		receiver = info.ObjectOf(fn.Recv.List[0].Names[0])
	}

	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			loopDepth++
			ast.Inspect(n, func(c ast.Node) bool {
				if c == n {
					return true
				}
				return visit(c)
			})
			loopDepth--
			return false
		case *ast.AssignStmt:
			if len(n.Rhs) == 1 {
				if call, ok := n.Rhs[0].(*ast.CallExpr); ok {
					p, name := s.callee(call)
					if isMarshal(p, name) && len(n.Lhs) > 0 {
						if id, ok := n.Lhs[0].(*ast.Ident); ok {
							if obj := info.ObjectOf(id); obj != nil {
								marshalled[obj] = true
							}
						}
					}
					if isEncodeName(name) && len(n.Lhs) > 0 {
						if id, ok := n.Lhs[0].(*ast.Ident); ok {
							if obj := info.ObjectOf(id); obj != nil {
								encoded[obj] = encoderLabel(p, name)
							}
						}
					}
				}
			}
			if n.Tok == token.ADD_ASSIGN && loopDepth > 0 && s.hot && len(n.Lhs) == 1 {
				if b, ok := info.TypeOf(n.Lhs[0]).(*types.Basic); ok && b.Info()&types.IsString != 0 {
					s.add(owner, "loop-format", "string+=", n.Pos())
				}
			}
		case *ast.BinaryExpr:
			if n.Op == token.EQL || n.Op == token.NEQ {
				l, r := baseIdent(n.X), baseIdent(n.Y)
				if l != nil && r != nil && marshalled[info.ObjectOf(l)] && marshalled[info.ObjectOf(r)] {
					s.add(owner, "marshal-compare", "==", n.Pos())
				}
			}
		case *ast.CallExpr:
			p, name := s.callee(n)
			if nestedMarshal && (isMarshal(p, name) || name == "MarshalJSON") && len(n.Args) <= 1 {
				onReceiver := false
				if len(n.Args) == 1 {
					if id := baseIdent(n.Args[0]); id != nil && receiver != nil && info.ObjectOf(id) == receiver {
						onReceiver = true
					}
				}
				if !onReceiver {
					s.add(owner, "nested-marshal", encoderLabel(p, name), n.Pos())
				}
			}
			if p != "" && isDecodeName(p, name) {
				decodes = append(decodes, n)
			}
			if isCapCheck(n) {
				capChecked = true
			}
			if s.isPoolPut(n) && growable(info.TypeOf(n.Args[0]), 0) {
				puts = append(puts, n)
			}
			if s.hot && !fnHot && loopDepth > 0 && timerCall(p, name) {
				s.add(owner, "timer-per-call", p+"."+name, n.Pos())
			}
			switch {
			case isMarshal(p, name):
				marshalPos = append(marshalPos, n.Pos())
			case isUnmarshal(p, name):
				unmarshals = append(unmarshals, n)
			case p == "bytes" && name == "Equal" && len(n.Args) == 2:
				l, r := baseIdent(n.Args[0]), baseIdent(n.Args[1])
				if l != nil && r != nil && marshalled[info.ObjectOf(l)] && marshalled[info.ObjectOf(r)] {
					s.add(owner, "marshal-compare", "bytes.Equal", n.Pos())
				}
			case p == "reflect" && name == "DeepEqual":
				deepEqualPos = append(deepEqualPos, n.Pos())
			case p == "regexp" && (name == "MustCompile" || name == "Compile" || name == "MustCompilePOSIX" || name == "CompilePOSIX") && fn.Name.Name != "init":
				s.add(owner, "compile-in-func", "regexp."+name, n.Pos())
			case (p == "text/template" || p == "html/template") && name == "Parse" && fn.Name.Name != "init":
				s.add(owner, "compile-in-func", "template.Parse", n.Pos())
			case p == "slices" && name == "Clone" && len(n.Args) == 1 && isMessageSlice(info.TypeOf(n.Args[0])):
				s.add(owner, "history-clone", "slices.Clone", n.Pos())
			case p == "":
				if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "append" && n.Ellipsis.IsValid() && len(n.Args) == 2 && isMessageSlice(info.TypeOf(n.Args[1])) {
					if conv, ok := n.Args[0].(*ast.CallExpr); ok && len(conv.Args) == 1 {
						if tv, ok := info.Types[conv.Args[0]]; ok && tv.IsNil() {
							s.add(owner, "history-clone", "append(nil,x...)", n.Pos())
						}
					}
				}
			case s.hot && p == "os" && (name == "Getenv" || name == "LookupEnv" || name == "Stat" || name == "Lstat") && fn.Name.Name != "init":
				s.add(owner, "env-stat", "os."+name, n.Pos())
			case s.hot && loopDepth > 0 && p == "fmt" && name == "Sprintf":
				s.add(owner, "loop-format", "fmt.Sprintf", n.Pos())
			}
		}
		return true
	}
	ast.Inspect(fn.Body, visit)

	for _, u := range unmarshals {
		if len(u.Args) == 0 {
			continue
		}
		if call, ok := u.Args[0].(*ast.CallExpr); ok {
			if p, name := s.callee(call); isMarshal(p, name) {
				s.add(owner, "json-roundtrip", "marshal+unmarshal", u.Pos())
				continue
			}
		}
		if id := baseIdent(u.Args[0]); id != nil && marshalled[info.ObjectOf(id)] {
			s.add(owner, "json-roundtrip", "marshal+unmarshal", u.Pos())
		}
	}
	if len(deepEqualPos) > 0 {
		for _, m := range marshalPos {
			if m > deepEqualPos[0] {
				s.add(owner, "marshal-compare", "DeepEqual then Marshal", deepEqualPos[0])
				break
			}
		}
	}
	for _, d := range decodes {
		dp, dname := s.callee(d)
		for _, arg := range d.Args {
			label := ""
			if call, ok := arg.(*ast.CallExpr); ok {
				if p, name := s.callee(call); isEncodeName(name) {
					label = encoderLabel(p, name)
				}
			}
			if label == "" {
				if id := baseIdent(arg); id != nil {
					label = encoded[info.ObjectOf(id)]
				}
			}
			if label == "" {
				continue
			}
			// json.Marshal into json.Unmarshal is json-roundtrip's.
			if isUnmarshal(dp, dname) && strings.HasPrefix(label, "json.") && label != "json.MarshalJSON" {
				break
			}
			s.add(owner, "decode-own-encode", label+"->"+shortPath(dp)+"."+dname, d.Pos())
			break
		}
	}
	if !capChecked {
		for _, put := range puts {
			s.add(owner, "pool-uncapped", "sync.Pool.Put", put.Pos())
		}
	}
	if fnHot {
		s.scanHot(owner, fn.Body)
	}
}

// shortPath is the last element of an import path.
func shortPath(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

// encoderLabel names an encoder call for a finding's detail: the package's last path element and the function or method name.
func encoderLabel(path, name string) string {
	if isJSONPkg(path) {
		return "json." + name
	}
	return shortPath(path) + "." + name
}

// allowedLines returns the lines a `//perf:ok <reason>` comment covers: its own line and the next.
func allowedLines(pkg *packages.Package, file *ast.File) map[int]bool {
	out := map[int]bool{}
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
			rest, ok := strings.CutPrefix(text, "perf:ok")
			if !ok || strings.TrimSpace(rest) == "" {
				continue
			}
			line := pkg.Fset.Position(c.Pos()).Line
			out[line] = true
			out[line+1] = true
		}
	}
	return out
}

// scan returns the findings in the non-test code of pkgs, with file names relative to root.
func scan(root string, pkgs []*packages.Package) []finding {
	counts := map[string]*finding{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			name := pkg.Fset.Position(file.Pos()).Filename
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			rel, err := filepath.Rel(root, name)
			if err != nil || strings.HasPrefix(rel, "..") {
				continue
			}
			rel = filepath.ToSlash(rel)
			if isToolingPath(rel) && !strings.HasPrefix(rel, "testdata/") {
				continue
			}
			s := &scanner{pkg: pkg, rel: rel, hot: hotPackage(rel), allowed: allowedLines(pkg, file), counts: counts}
			if sampleHot {
				s.hot = true
			}
			for _, decl := range file.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
					s.scanFunc(fn)
				}
			}
		}
	}
	found := make([]finding, 0, len(counts))
	for _, f := range counts {
		slices.Sort(f.Lines)
		found = append(found, *f)
	}
	slices.SortFunc(found, func(a, b finding) int { return strings.Compare(a.key(), b.key()) })
	return found
}

// sampleHot makes every package hot; the tests set it for their sample module.
var sampleHot bool

func load(root string, patterns []string) ([]*packages.Package, error) {
	cfg := &packages.Config{
		Dir:  root,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, err
	}
	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			return nil, fmt.Errorf("load %s: %w", pkg.PkgPath, pkg.Errors[0])
		}
	}
	return pkgs, nil
}

func entry(f finding) string {
	text := fmt.Sprintf("[[hit]]\nfile = %q\nfunc = %q\npattern = %q\ndetail = %q\ncount = %d\n", f.File, f.Func, f.Pattern, f.Detail, f.Count)
	if f.Class != "" {
		text += fmt.Sprintf("class = %q\n", f.Class)
	}
	return text
}

// needsClass is true for the patterns whose baseline entries must say how hot the site is.
func needsClass(pattern string) bool {
	switch pattern {
	case "json-roundtrip", "history-clone", "chan-hop", "decode-own-encode", "nested-marshal", "open-per-call", "pool-uncapped", "timer-per-call":
		return true
	}
	return false
}

func validClass(class string) bool {
	return class == "cold" || strings.HasPrefix(class, "cold:") || strings.HasPrefix(class, "hot-owned:") || strings.HasPrefix(class, "hot-kept:")
}

func compare(found []finding, baseline []finding) (grew, stale []string) {
	allowed := map[string]finding{}
	for _, b := range baseline {
		allowed[b.key()] = b
	}
	seen := map[string]bool{}
	for _, f := range found {
		seen[f.key()] = true
		b, ok := allowed[f.key()]
		where := fmt.Sprintf("%s:%v %s [%s %s]", f.File, f.Lines, f.Func, f.Pattern, f.Detail)
		switch {
		case !ok:
			grew = append(grew, fmt.Sprintf("%s: %d new", where, f.Count))
		case f.Count > b.Count:
			grew = append(grew, fmt.Sprintf("%s: went from %d to %d", where, b.Count, f.Count))
		case f.Count < b.Count:
			stale = append(stale, fmt.Sprintf("%s: fell from %d to %d; lower the baseline entry", where, b.Count, f.Count))
		}
	}
	for _, b := range baseline {
		if !seen[b.key()] {
			stale = append(stale, fmt.Sprintf("%s %s [%s %s] is gone; delete the baseline entry", b.File, b.Func, b.Pattern, b.Detail))
		}
	}
	return grew, stale
}

func run(root, baselinePath string, patterns []string, report, printBaseline bool) int {
	pkgs, err := load(root, patterns)
	if err != nil {
		fmt.Fprintln(os.Stderr, "perf-lint:", err)
		return 2
	}
	found := scan(root, pkgs)
	if printBaseline {
		var previous baselineFile
		_, _ = toml.DecodeFile(baselinePath, &previous)
		classes := map[string]string{}
		for _, b := range previous.Hit {
			classes[b.key()] = b.Class
		}
		for i := range found {
			found[i].Class = classes[found[i].key()]
		}
		// REUSE-IgnoreStart
		fmt.Print("# SPDX-License-Identifier: MIT\n#\n# Perf-lint ratchet (automation/ci/perflint, `make perf-lint`). Counts may only shrink: a fix lowers or deletes its entry in the same change; never add an entry for new code (fix it, or annotate a proven cold site `//perf:ok <reason>`).\n# Regenerate with `go run ./automation/ci/perflint -print` only when lowering counts.\n\n")
		// REUSE-IgnoreEnd
		for _, f := range found {
			fmt.Println(entry(f))
		}
		return 0
	}
	if report {
		byPattern := map[string]int{}
		for _, f := range found {
			byPattern[f.Pattern] += f.Count
			for _, l := range f.Lines {
				fmt.Printf("%s  %s:%d  %s  %s\n", f.Pattern, f.File, l, f.Func, f.Detail)
			}
		}
		for p, n := range byPattern {
			fmt.Printf("TOTAL %s %d\n", p, n)
		}
		return 0
	}
	var baseline baselineFile
	if _, err := toml.DecodeFile(baselinePath, &baseline); err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "perf-lint:", err)
		return 2
	}
	grew, stale := compare(found, baseline.Hit)
	for _, b := range baseline.Hit {
		if needsClass(b.Pattern) && !validClass(b.Class) {
			grew = append(grew, fmt.Sprintf("%s %s [%s]: baseline entry needs class = \"cold\", \"hot-owned:<branch>\" or \"hot-kept:<reason>\"", b.File, b.Func, b.Pattern))
		}
	}
	for _, line := range grew {
		fmt.Fprintln(os.Stderr, "perf-lint: new antipattern:", line)
	}
	for _, line := range stale {
		fmt.Fprintln(os.Stderr, "perf-lint: stale baseline:", line)
	}
	if len(grew)+len(stale) > 0 {
		fmt.Fprintln(os.Stderr, "perf-lint: fix the site, or annotate a cold one `//perf:ok <reason>`; lower the baseline with `go run ./automation/ci/perflint -print` when a site is removed")
		return 1
	}
	return 0
}
