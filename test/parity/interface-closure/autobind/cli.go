package main

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// ruleCLI decides a command-line row. A flag is Pig's when the command's parser names the flag and every alias in one function of its parser file, and an asserting
// Go test of that parser's test files names the flag too. The command's parser file is the Go counterpart of the upstream file the inventory records.
const ruleCLI = "C1"

// cliFlag is one row of the command-line inventory (test/parity/interfaces/cli-v<version>.json).
type cliFlag struct {
	ID      string   `json:"id"`
	Command string   `json:"command"`
	Flag    string   `json:"flag"`
	Aliases []string `json:"aliases"`
}

// cliParsers maps an upstream command to the Go file that parses its flags, relative to the repository root.
var cliParsers = map[string]string{
	"pi":         "coding/cli/args.go",
	"pi-config":  "coding/cli/config_command.go",
	"pi-install": "coding/cli/package_commands.go",
	"pi-list":    "coding/cli/package_commands.go",
	"pi-remove":  "coding/cli/package_commands.go",
	"pi-update":  "coding/cli/package_commands.go",
}

// sharedParser reports whether more than one command is parsed by the file.
func sharedParser(file string) bool {
	n := 0
	for _, f := range cliParsers {
		if f == file {
			n++
		}
	}
	return n > 1
}

func loadCLI(root, version string) (map[string]*cliFlag, error) {
	var inv struct {
		Interfaces []*cliFlag `json:"interfaces"`
	}
	out := map[string]*cliFlag{}
	path := filepath.Join(root, "test/parity/interfaces/cli-v"+version+".json")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return out, nil // a ledger without a command-line inventory has no command-line rows
	}
	if err := readJSON(path, &inv); err != nil {
		return nil, err
	}
	for _, f := range inv.Interfaces {
		out[f.ID] = f
	}
	return out, nil
}

// stringLiterals returns, for each function declaration of a file, the string literals its body holds.
func stringLiterals(file *ast.File) map[*ast.FuncDecl]map[string]bool {
	out := map[*ast.FuncDecl]map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		lits := map[string]bool{}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if s, err := strconv.Unquote(lit.Value); err == nil {
					lits[s] = true
				}
			}
			return true
		})
		out[fn] = lits
	}
	return out
}

func namesFlag(lits map[string]bool, f *cliFlag) bool {
	for _, name := range append([]string{f.Flag}, f.Aliases...) {
		if !lits[name] {
			return false
		}
	}
	return true
}

// cliDecision applies C1 to a command-line row.
func (d *detector) cliDecision(id string) *decision {
	flag := d.l.cli[id]
	if flag == nil {
		return gap(reasonCLI, "command-line row: the command-line inventory has no such row", "")
	}
	parser, ok := cliParsers[flag.Command]
	if !ok {
		return gap(reasonCLI, "command-line row: no Go parser file is recorded for command "+flag.Command, "")
	}
	fset := token.NewFileSet()
	src, err := parser2(fset, filepath.Join(d.ix.root, parser))
	if err != nil {
		return gap(reasonCLI, "command-line row: "+err.Error(), "")
	}
	var fn *ast.FuncDecl
	var target *cliTarget
	for decl := range stringLiterals(src) {
		if got := clauseField(decl, flag); got != nil && (fn == nil || decl.Pos() < fn.Pos()) {
			fn, target = decl, got
		}
	}
	if fn == nil {
		return gap(reasonCLI, "command-line row: no case clause of "+parser+" names "+flag.Flag+" and its aliases and assigns a field", "")
	}
	pkg := loadCLISource(d.ix.root, filepath.Dir(parser))
	switch {
	case target.Prints:
	case target.Field != "":
		if !pkg.reads(target.Field, target.Clause, parser) {
			return gap(reasonCLI, "command-line row: no non-test code outside the case clause of "+flag.Flag+" reads field "+target.Field, "")
		}
	default:
		if !readsLocal(fn, target.Local, target.Clause) {
			return gap(reasonCLI, "command-line row: "+fn.Name.Name+" does not read variable "+target.Local+" outside the case clause of "+flag.Flag, "")
		}
	}
	helpFn, helpFile := pkg.helpFor(flag)
	if helpFn == "" && flag.Flag == "--help" {
		helpFn, helpFile = fn.Name.Name, parser // the clause prints the usage itself
	}
	if helpFn == "" {
		helpFn, helpFile = usageClause(fn, flag), parser
	}
	if helpFn == "" {
		return gap(reasonCLI, "command-line row: no help function of "+filepath.Dir(parser)+" prints text that names "+flag.Flag, "")
	}
	helpTests := d.cliHelpTests(filepath.ToSlash(filepath.Dir(helpFile)), helpFn)
	if len(helpTests) == 0 {
		return gap(reasonExercise, "no asserting test calls "+helpFile+"#"+helpFn+", the help text of "+flag.Flag, helpFile+"#"+helpFn)
	}
	owner := ""
	if fn.Recv != nil && len(fn.Recv.List) == 1 {
		owner = recvName(fn.Recv.List[0].Type)
	}
	s := &sym{Kind: "func", Name: fn.Name.Name, Owner: owner, Dir: filepath.ToSlash(filepath.Dir(parser)), File: parser, Tier: ruleCLI}
	if owner != "" {
		s.Kind = "method"
	}
	tests := d.cliTests(parser, flag)
	call := ""
	if d.ix.reach[s.File+"#"+s.Qualified()] {
		call = s.File + "#" + s.Qualified()
	}
	if len(tests) == 0 && call == "" {
		return gap(reasonExercise, "no asserting test of "+strings.TrimSuffix(filepath.Base(parser), ".go")+" names "+flag.Flag+" and "+s.Target()+" is not reachable from cmd/pig", s.Target())
	}
	tests = appendNew(tests, helpTests)
	return &decision{Sym: s, Evidence: ruleCLI, Info: &exerciseInfo{Tests: tests, Call: call}, Layers: cliLayers, Targets: []string{helpFile + "#" + helpFn}}
}

// cliLayers are the layers a derived command-line row claims complete.
var cliLayers = map[string]string{"parser": "complete", "help": "complete", "consumer": "complete", "production": "complete", "behavior": "complete"}

// cliTarget is what a case clause of a parser does with its flag: it assigns a field of a value (Field), assigns a local variable of the parser function (Local),
// or, for --help, runs code that prints the usage (Prints).
type cliTarget struct {
	Clause *ast.CaseClause
	Field  string
	Local  string
	Prints bool
}

// clauseField returns the case clause of fn that names the flag and every alias together with its target: the field the clause assigns (a statement of the clause,
// nested or not, that assigns a selector), else the local variable it assigns, else for --help the call that prints the usage. It returns nil when no clause does both.
func clauseField(fn *ast.FuncDecl, flag *cliFlag) *cliTarget {
	var found *cliTarget
	var field, local string
	var prints bool
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		clause, ok := n.(*ast.CaseClause)
		if !ok || found != nil {
			return found == nil
		}
		lits := map[string]bool{}
		for _, expr := range clause.List {
			ast.Inspect(expr, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if s, err := strconv.Unquote(lit.Value); err == nil {
						lits[s] = true
					}
				}
				return true
			})
		}
		if !namesFlag(lits, flag) {
			return true
		}
		for _, stmt := range clause.Body {
			ast.Inspect(stmt, func(n ast.Node) bool {
				assign, ok := n.(*ast.AssignStmt)
				if !ok || field != "" || len(assign.Lhs) == 0 {
					return field == ""
				}
				switch lhs := assign.Lhs[0].(type) {
				case *ast.SelectorExpr:
					field = lhs.Sel.Name
				case *ast.IndexExpr:
					if sel, ok := lhs.X.(*ast.SelectorExpr); ok {
						field = sel.Sel.Name
					}
				case *ast.Ident:
					if local == "" && lhs.Name != "_" {
						local = lhs.Name
					}
				}
				return field == ""
			})
			if flag.Flag == "--help" {
				ast.Inspect(stmt, func(n ast.Node) bool {
					if _, ok := n.(*ast.CallExpr); ok {
						prints = true
					}
					return !prints
				})
			}
		}
		if field != "" || local != "" || prints {
			found = &cliTarget{Clause: clause, Field: field, Local: local, Prints: prints && field == "" && local == ""}
		}
		return found == nil
	})
	if found == nil && flag.Flag == "--help" {
		// A parser that tests for help anywhere before its loop: an if whose condition names --help and every alias and whose body prints the usage.
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			stmt, ok := n.(*ast.IfStmt)
			if !ok || found != nil {
				return found == nil
			}
			lits := map[string]bool{}
			ast.Inspect(stmt.Cond, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if s, err := strconv.Unquote(lit.Value); err == nil {
						lits[s] = true
					}
				}
				return true
			})
			if !namesFlag(lits, flag) {
				return true
			}
			ast.Inspect(stmt.Body, func(n ast.Node) bool {
				if _, ok := n.(*ast.CallExpr); ok {
					found = &cliTarget{Prints: true}
				}
				return found == nil
			})
			return found == nil
		})
	}
	return found
}

// cliSource is the parsed non-test source of one directory.
type cliSource struct {
	root, dir string
	files     map[string]*ast.File // by repository-relative path
}

var cliSources = map[string]*cliSource{}

func loadCLISource(root, dir string) *cliSource {
	key := root + "\x00" + dir
	if src := cliSources[key]; src != nil {
		return src
	}
	src := &cliSource{root: root, dir: dir, files: map[string]*ast.File{}}
	entries, _ := os.ReadDir(filepath.Join(root, dir))
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, dir, name), nil, parser.ParseComments); err == nil {
			src.files[filepath.ToSlash(filepath.Join(dir, name))] = file
		}
	}
	cliSources[key] = src
	return src
}

// readsLocal reports whether fn uses the local variable outside the case clause other than as the left side of an assignment.
func readsLocal(fn *ast.FuncDecl, name string, clause *ast.CaseClause) bool {
	skip := map[ast.Node]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if assign, ok := n.(*ast.AssignStmt); ok && assign.Tok == token.ASSIGN {
			for _, lhs := range assign.Lhs {
				skip[lhs] = true
			}
		}
		return true
	})
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name && !skip[id] && (id.Pos() < clause.Pos() || id.End() > clause.End()) {
			found = true
		}
		return !found
	})
	return found
}

// usageClause returns the name of fn when a case clause of fn that names --help holds a string literal that names the flag as a word, which is how a command prints its usage inline.
func usageClause(fn *ast.FuncDecl, flag *cliFlag) string {
	help := &cliFlag{Flag: "--help"}
	word := flagWord(flag.Flag)
	name := ""
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		clause, ok := n.(*ast.CaseClause)
		if !ok || name != "" {
			return name == ""
		}
		lits := map[string]bool{}
		for _, expr := range clause.List {
			ast.Inspect(expr, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if s, err := strconv.Unquote(lit.Value); err == nil {
						lits[s] = true
					}
				}
				return true
			})
		}
		if !namesFlag(lits, help) {
			return true
		}
		for _, stmt := range clause.Body {
			ast.Inspect(stmt, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if text, err := strconv.Unquote(lit.Value); err == nil && word.MatchString(text) {
						name = fn.Name.Name
					}
				}
				return name == ""
			})
		}
		return name == ""
	})
	return name
}

func flagWord(flag string) *regexp.Regexp {
	return regexp.MustCompile(`(^|[\s\[,|])` + regexp.QuoteMeta(flag) + `($|[\s\]<,=|])`)
}

// reads reports whether code reads a selector named field outside the case clause: any use that is not the left side of an assignment, in any function of the package.
func (s *cliSource) reads(field string, clause *ast.CaseClause, clauseFile string) bool {
	for path, file := range s.files {
		skip := map[ast.Node]bool{}
		ast.Inspect(file, func(n ast.Node) bool {
			if assign, ok := n.(*ast.AssignStmt); ok && assign.Tok == token.ASSIGN {
				for _, lhs := range assign.Lhs {
					skip[lhs] = true
				}
			}
			return true
		})
		found := false
		ast.Inspect(file, func(n ast.Node) bool {
			if found {
				return false
			}
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == field && !skip[sel] {
				if path != clauseFile || sel.Pos() < clause.Pos() || sel.End() > clause.End() {
					found = true
				}
			}
			return !found
		})
		if found {
			return true
		}
	}
	return false
}

// topLevelStrings maps each package-level constant or variable of the package to the string literals of its value, or the text of the file it embeds.
func (s *cliSource) topLevelStrings() map[string][]string {
	out := map[string][]string{}
	for _, file := range s.files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST && gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				var texts []string
				if gen.Doc != nil {
					for _, comment := range gen.Doc.List {
						if name, ok := strings.CutPrefix(comment.Text, "//go:embed "); ok {
							if data, err := os.ReadFile(filepath.Join(s.root, s.dir, strings.TrimSpace(name))); err == nil {
								texts = append(texts, string(data))
							}
						}
					}
				}
				ast.Inspect(value, func(n ast.Node) bool {
					if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if text, err := strconv.Unquote(lit.Value); err == nil {
							texts = append(texts, text)
						}
					}
					return true
				})
				for _, name := range value.Names {
					out[name.Name] = texts
				}
			}
		}
	}
	return out
}

// helpFor returns the first help function (a function whose name contains "help") whose body holds, or names a package-level constant or variable that holds, a string literal that names the flag as a word, in file order.
func (s *cliSource) helpFor(flag *cliFlag) (fn, file string) {
	word := regexp.MustCompile(`(^|[\s\[,|])` + regexp.QuoteMeta(flag.Flag) + `($|[\s\]<,=|])`)
	top := s.topLevelStrings()
	for _, path := range slices.Sorted(maps.Keys(s.files)) {
		for _, decl := range s.files[path].Decls {
			f, ok := decl.(*ast.FuncDecl)
			if !ok || f.Body == nil || !strings.Contains(strings.ToLower(f.Name.Name), "help") {
				continue
			}
			found := false
			ast.Inspect(f.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.BasicLit:
					if n.Kind == token.STRING {
						if text, err := strconv.Unquote(n.Value); err == nil && word.MatchString(text) {
							found = true
						}
					}
				case *ast.Ident:
					for _, text := range top[n.Name] {
						if word.MatchString(text) {
							found = true
						}
					}
				}
				return !found
			})
			if found {
				return f.Name.Name, path
			}
		}
	}
	return "", ""
}

// callersWithin returns the names of the functions of the package that call fn directly or through up to two further functions of the package, and fn itself.
func (s *cliSource) callersWithin(fn string) map[string]bool {
	calls := map[string]map[string]bool{} // function -> package functions it calls by name
	for _, file := range s.files {
		for _, decl := range file.Decls {
			f, ok := decl.(*ast.FuncDecl)
			if !ok || f.Body == nil {
				continue
			}
			callees := map[string]bool{}
			ast.Inspect(f.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if id, ok := call.Fun.(*ast.Ident); ok {
						callees[id.Name] = true
					}
				}
				return true
			})
			calls[f.Name.Name] = callees
		}
	}
	out := map[string]bool{fn: true}
	for range 3 {
		for caller, callees := range calls {
			for callee := range callees {
				if out[callee] {
					out[caller] = true
				}
			}
		}
	}
	return out
}

// cliHelpTests lists the asserting tests, at most two, that use the help function or a function of the package that reaches it.
func (d *detector) cliHelpTests(dir, helpFn string) []string {
	info := d.ix.pkgs[dir]
	if info == nil {
		return nil
	}
	var keys []string
	for name := range loadCLISource(d.ix.root, dir).callersWithin(helpFn) {
		if obj := info.Types.Scope().Lookup(name); obj != nil {
			keys = append(keys, d.ix.declKey(obj))
		}
	}
	slices.Sort(keys)
	var out []string
	for _, test := range d.ix.testsFor(keys) {
		if test.Asserts && len(out) < 2 {
			out = append(out, test.Ref)
		}
	}
	return out
}

func parser2(fset *token.FileSet, path string) (*ast.File, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	return parser.ParseFile(fset, path, nil, 0)
}

func recvName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// cliTests lists the asserting test functions, at most three, of the test files that belong to a parser file (its name, then "_" or "_test") that name the flag, and the subcommand when the flag belongs to one.
func (d *detector) cliTests(parserFile string, flag *cliFlag) []string {
	dir := filepath.Join(d.ix.root, filepath.Dir(parserFile))
	stem := strings.TrimSuffix(filepath.Base(parserFile), ".go")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, "_test.go") || name != stem+"_test.go" && !strings.HasPrefix(name, stem+"_") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			continue
		}
		rel := filepath.ToSlash(filepath.Join(filepath.Dir(parserFile), name))
		for fn, lits := range stringLiterals(file) {
			if !strings.HasPrefix(fn.Name.Name, "Test") || fn.Recv != nil || !lits[flag.Flag] {
				continue
			}
			if word, sub := strings.CutPrefix(flag.Command, "pi-"); sub && sharedParser(parserFile) && !lits[word] {
				continue // a flag of one subcommand of a shared parser is exercised by a test that names the subcommand too
			}
			key := rel + "#" + fn.Name.Name
			if d.ix.asserts(key, 0, map[string]bool{}) {
				out = append(out, "test:"+key)
			}
		}
	}
	slices.Sort(out)
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

// appendNew appends the elements of extra that list does not hold yet, so a test that covers both the parser and the help text is
// one evidence entry.
func appendNew(list, extra []string) []string {
	for _, e := range extra {
		if !slices.Contains(list, e) {
			list = append(list, e)
		}
	}
	return list
}
