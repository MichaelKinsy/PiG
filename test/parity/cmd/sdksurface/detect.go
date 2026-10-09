package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// symbolSet is the set of symbols one SDK exports, in that SDK's notation:
//
//	Go:     Name, Type.Method, Type.Field
//	Rust:   name, Type::method, Type.field
//	Python: name, Class.member
//	Node:   Class.member, api.key, api.events.key, read:obj.prop
type symbolSet map[string]bool

func (s symbolSet) has(sym string) bool { return s[sym] }

func (s symbolSet) sorted() []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sourceFiles lists a directory's files with the given suffix, skipping tests.
func sourceFiles(dir, suffix string, skip func(string) bool) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, suffix) || (skip != nil && skip(name)) {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	sort.Strings(out)
	return out, nil
}

func isGoTest(name string) bool { return strings.HasSuffix(name, "_test.go") }

// ── Go ──────────────────────────────────────────────────────────────────────

// goPackage is the parsed exported surface of a Go package: its symbols, its
// struct types' JSON field names, and its string constants and literals.
type goPackage struct {
	symbols  symbolSet
	jsonTags map[string][]string // struct type → JSON names (embedded structs flattened later)
	embeds   map[string][]string // struct type → embedded struct type names
	literals map[string]bool     // every string literal and JSON tag name
	aliases  []goAlias           // type X = pkg.Y declarations whose target lives in another package
}

// goAlias is a type alias of a type declared in another package of this module; its wire fields are the target's.
type goAlias struct {
	name       string // the alias, as declared in this package
	importPath string
	target     string
}

func parseGoPackage(dir string) (*goPackage, error) {
	files, err := sourceFiles(dir, ".go", isGoTest)
	if err != nil {
		return nil, err
	}
	pkg := &goPackage{symbols: symbolSet{}, jsonTags: map[string][]string{}, embeds: map[string][]string{}, literals: map[string]bool{}}
	fset := token.NewFileSet()
	for _, f := range files {
		file, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		imports := importNames(file)
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if !d.Name.IsExported() {
					continue
				}
				if d.Recv != nil && len(d.Recv.List) == 1 {
					recv := receiverName(d.Recv.List[0].Type)
					if recv != "" && ast.IsExported(recv) {
						pkg.symbols[recv+"."+d.Name.Name] = true
					}
					continue
				}
				pkg.symbols[d.Name.Name] = true
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						name := s.Name.Name
						if ast.IsExported(name) {
							pkg.symbols[name] = true
						}
						switch t := s.Type.(type) {
						case *ast.SelectorExpr:
							if pkgIdent, ok := t.X.(*ast.Ident); ok && s.Assign.IsValid() && imports[pkgIdent.Name] != "" {
								pkg.aliases = append(pkg.aliases, goAlias{name: name, importPath: imports[pkgIdent.Name], target: t.Sel.Name})
							}
						case *ast.StructType:
							for _, field := range t.Fields.List {
								tag := jsonTag(field)
								if len(field.Names) == 0 {
									if emb := receiverName(field.Type); emb != "" && (tag == "" || tag == ",inline") {
										pkg.embeds[name] = append(pkg.embeds[name], emb)
									}
								}
								for _, n := range field.Names {
									if n.IsExported() && ast.IsExported(name) {
										pkg.symbols[name+"."+n.Name] = true
									}
									if tag != "-" {
										jsonName := tag
										if jsonName == "" {
											jsonName = n.Name
										}
										pkg.jsonTags[name] = append(pkg.jsonTags[name], jsonName)
									}
								}
								if tag != "" && tag != "-" {
									pkg.literals[tag] = true
								}
							}
						case *ast.InterfaceType:
							for _, m := range t.Methods.List {
								for _, n := range m.Names {
									if n.IsExported() && ast.IsExported(name) {
										pkg.symbols[name+"."+n.Name] = true
									}
								}
							}
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if n.IsExported() {
								pkg.symbols[n.Name] = true
							}
						}
					}
				}
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.BasicLit:
				if x.Kind == token.STRING {
					if v, err := strconv.Unquote(x.Value); err == nil {
						pkg.literals[v] = true
					}
				}
			case *ast.StructType:
				// Anonymous decoding structs inside functions name wire
				// fields too.
				for _, field := range x.Fields.List {
					if tag := jsonTag(field); tag != "" && tag != "-" && tag != ",inline" {
						pkg.literals[tag] = true
					}
				}
			}
			return true
		})
	}
	return pkg, nil
}

// importNames maps a file's import names to their paths; an unnamed import is named by its last path element.
func importNames(file *ast.File) map[string]string {
	out := map[string]string{}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		out[name] = path
	}
	return out
}

// resolveAliases gives every alias of a type declared in another package of this module that type's JSON fields: a type alias is the
// same type, so a field decoded through the alias is a field of the target (coding/extension re-exports markdowntransform.MarkdownTransformContext).
func (p *goPackage) resolveAliases(root, modulePath string) error {
	for _, a := range p.aliases {
		rel, ok := strings.CutPrefix(a.importPath, modulePath+"/")
		if !ok {
			continue
		}
		target, err := parseGoPackage(filepath.Join(root, rel))
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		p.jsonTags[a.name] = append(p.jsonTags[a.name], target.jsonTags[a.target]...)
		p.embeds[a.name] = append(p.embeds[a.name], target.embeds[a.target]...)
		for _, tag := range target.jsonTags[a.target] {
			p.literals[tag] = true
		}
	}
	return nil
}

// jsonFields returns a struct's JSON field names, with embedded structs'
// fields flattened in, as encoding/json does.
func (p *goPackage) jsonFields(typ string) map[string]bool {
	out := map[string]bool{}
	var walk func(string, int)
	walk = func(t string, depth int) {
		if depth > 8 {
			return
		}
		for _, n := range p.jsonTags[t] {
			out[n] = true
		}
		for _, e := range p.embeds[t] {
			walk(e, depth+1)
		}
	}
	walk(typ, 0)
	return out
}

func receiverName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return receiverName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return receiverName(t.X)
	case *ast.IndexListExpr:
		return receiverName(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	}
	return ""
}

func jsonTag(field *ast.Field) string {
	if field.Tag == nil {
		return ""
	}
	raw, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		return ""
	}
	tag := reflect.StructTag(raw).Get("json")
	if tag == "" {
		return ""
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" && strings.Contains(tag, "inline") {
		return ",inline"
	}
	return name
}

// ── Source scanning shared by Rust and JavaScript ──────────────────────────

// scanCode blanks comments and string/char literal contents (keeping their
// length and newlines) so brace matching and identifier regexps see only
// code. Template-literal substitutions stay code.
func scanCode(src string, lang string) string {
	b := []byte(src)
	out := make([]byte, len(b))
	copy(out, b)
	blank := func(i int) {
		if out[i] != '\n' {
			out[i] = ' '
		}
	}
	var tmplDepth []int // brace depth at each open template substitution
	depth := 0
	lastSig := byte(0)
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				blank(i)
				i++
			}
			continue
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			for i < len(b) && (b[i] != '*' || i+1 >= len(b) || b[i+1] != '/') {
				blank(i)
				i++
			}
			if i < len(b) {
				blank(i)
				if i+1 < len(b) {
					blank(i + 1)
				}
				i++
			}
			continue
		case lang == "js" && c == '/' && strings.IndexByte("(,=:[!&|?{};+-*%<>~^", lastSig) >= 0:
			// Regular expression literal.
			j := i + 1
			inClass := false
			for j < len(b) && b[j] != '\n' {
				if b[j] == '\\' {
					j += 2
					continue
				}
				stop := false
				switch b[j] {
				case '[':
					inClass = true
				case ']':
					inClass = false
				case '/':
					stop = !inClass
				}
				if stop {
					break
				}
				j++
			}
			for k := i + 1; k < j && k < len(b); k++ {
				blank(k)
			}
			i = j
			lastSig = '/'
			continue
		case c == '"' || (c == '\'' && lang == "js") || (c == '`' && lang == "js"):
			quote := c
			j := i + 1
			for j < len(b) {
				if b[j] == '\\' {
					blank(j)
					if j+1 < len(b) {
						blank(j + 1)
					}
					j += 2
					continue
				}
				if b[j] == quote {
					break
				}
				if quote == '`' && b[j] == '$' && j+1 < len(b) && b[j+1] == '{' {
					break
				}
				blank(j)
				j++
			}
			if quote == '`' && j < len(b) && b[j] == '$' {
				tmplDepth = append(tmplDepth, depth)
				depth++
				i = j + 1
				lastSig = '{'
				continue
			}
			i = j
			lastSig = 'a'
			continue
		case c == '\'' && lang == "rust":
			// Char literal ('x', '\n', '\u{..}') versus lifetime ('a).
			if i+2 < len(b) && b[i+2] == '\'' {
				blank(i + 1)
				i += 2
				continue
			}
			if i+1 < len(b) && b[i+1] == '\\' {
				j := i + 1
				for j < len(b) && b[j] != '\'' {
					blank(j)
					j++
				}
				i = j
				continue
			}
		case c == '{':
			depth++
		case c == '}':
			depth--
			if n := len(tmplDepth); n > 0 && tmplDepth[n-1] == depth {
				// Close of a template substitution: resume the template.
				tmplDepth = tmplDepth[:n-1]
				j := i + 1
				for j < len(b) {
					if b[j] == '\\' {
						blank(j)
						if j+1 < len(b) {
							blank(j + 1)
						}
						j += 2
						continue
					}
					if b[j] == '`' {
						break
					}
					if b[j] == '$' && j+1 < len(b) && b[j+1] == '{' {
						break
					}
					blank(j)
					j++
				}
				if j < len(b) && b[j] == '$' {
					tmplDepth = append(tmplDepth, depth)
					depth++
					i = j + 1
					continue
				}
				i = j
				lastSig = 'a'
				continue
			}
		}
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			lastSig = c
		}
	}
	return string(out)
}

// blockScope describes the brace scope a declaration opens.
type blockScope struct {
	name  string // type, class or module name; "" for anonymous blocks
	kind  string // impl, struct, trait, mod, enum, class, fn, other
	depth int    // brace depth inside the block
}

// ── Rust ────────────────────────────────────────────────────────────────────

var (
	rsImplRE   = regexp.MustCompile(`^\s*impl(?:\s*<[^{]*?>)?\s+(?:[A-Za-z_:<>, ]+?\s+for\s+)?([A-Za-z_][A-Za-z0-9_]*)`)
	rsItemRE   = regexp.MustCompile(`^\s*pub(?:\([a-z]+\))?\s+(?:async\s+)?(fn|struct|enum|trait|mod|type|const|static)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	rsTraitFn  = regexp.MustCompile(`^\s*(?:async\s+)?fn\s+([A-Za-z_][A-Za-z0-9_]*)`)
	rsFieldRE  = regexp.MustCompile(`^\s*pub\s+([a-z_][A-Za-z0-9_]*)\s*:`)
	rsVariant  = regexp.MustCompile(`^\s*([A-Z][A-Za-z0-9_]*)\s*[,({]?`)
	rsReexport = regexp.MustCompile(`pub use [^;]*;`)
)

func parseRust(dir string) (symbolSet, error) {
	files, err := sourceFiles(dir, ".rs", nil)
	if err != nil {
		return nil, err
	}
	syms := symbolSet{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		code := scanCode(string(raw), "rust")
		code = rsReexport.ReplaceAllString(code, "")
		var stack []blockScope
		depth := 0
		pending := blockScope{kind: "other"}
		for line := range strings.SplitSeq(code, "\n") {
			top := blockScope{kind: "file"}
			if len(stack) > 0 {
				top = stack[len(stack)-1]
			}
			// Only declarations directly inside the enclosing scope count.
			direct := (len(stack) == 0 && depth == 0) || (len(stack) > 0 && depth == top.depth)
			if direct {
				switch top.kind {
				case "impl", "trait":
					if sm := rsItemRE.FindStringSubmatch(line); sm != nil && sm[1] == "fn" {
						syms[top.name+"::"+sm[2]] = true
					} else if sm := rsTraitFn.FindStringSubmatch(line); sm != nil && top.kind == "trait" {
						syms[top.name+"::"+sm[1]] = true
					}
				case "struct":
					if sm := rsFieldRE.FindStringSubmatch(line); sm != nil {
						syms[top.name+"."+sm[1]] = true
					}
				case "enum":
					if sm := rsVariant.FindStringSubmatch(line); sm != nil {
						syms[top.name+"::"+sm[1]] = true
					}
				default:
					if sm := rsItemRE.FindStringSubmatch(line); sm != nil {
						name := sm[2]
						if top.kind == "mod" {
							name = top.name + "::" + name
						}
						syms[name] = true
					}
				}
			}
			// Decide what the next '{' on this line opens.
			if sm := rsImplRE.FindStringSubmatch(line); sm != nil && direct {
				pending = blockScope{name: sm[1], kind: "impl"}
			} else if sm := rsItemRE.FindStringSubmatch(line); sm != nil && direct {
				switch sm[1] {
				case "struct", "enum", "trait", "mod":
					pending = blockScope{name: sm[2], kind: sm[1]}
				default:
					pending = blockScope{kind: "other"}
				}
			}
			for i := 0; i < len(line); i++ {
				switch line[i] {
				case '{':
					depth++
					stack = append(stack, blockScope{name: pending.name, kind: pending.kind, depth: depth})
					pending = blockScope{kind: "other"}
				case '}':
					depth--
					if len(stack) > 0 {
						stack = stack[:len(stack)-1]
					}
				case ';':
					if pending.kind == "struct" {
						pending = blockScope{kind: "other"} // unit or tuple struct
					}
				}
			}
		}
	}
	return syms, nil
}

// ── Python ──────────────────────────────────────────────────────────────────

var (
	pyClassRE  = regexp.MustCompile(`^class\s+([A-Za-z_][A-Za-z0-9_]*)`)
	pyDefRE    = regexp.MustCompile(`^(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)`)
	pyAssignRE = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*(?::[^=]*)?=`)
	pyMemberRE = regexp.MustCompile(`^    (?:(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)|([A-Za-z_][A-Za-z0-9_]*)\s*:)`)
	pySelfRE   = regexp.MustCompile(`^\s+self\.([A-Za-z_][A-Za-z0-9_]*)\s*(?::[^=]*)?=`)
	// A signature ends at its colon, or at a one-line stub body (`: ...`).
	pySigEndRE = regexp.MustCompile(`\)\s*(?:->[^:]*)?:\s*(?:\.\.\.\s*)?(?:#.*)?$`)
	pyParamRE  = regexp.MustCompile(`^\*{0,2}([A-Za-z_][A-Za-z0-9_]*)`)
)

// pyParams lists the parameter names of a def signature, without self.
func pyParams(sig string) []string {
	open := strings.IndexByte(sig, '(')
	end := strings.LastIndex(sig, ")")
	if open < 0 || end < open {
		return nil
	}
	var out []string
	depth := 0
	start := open + 1
	inner := sig[:end]
	for i := open + 1; i <= len(inner); i++ {
		if i == len(inner) || (inner[i] == ',' && depth == 0) {
			part := strings.TrimSpace(inner[start:i])
			if sm := pyParamRE.FindStringSubmatch(part); sm != nil && sm[1] != "self" && sm[1] != "cls" {
				out = append(out, sm[1])
			}
			start = i + 1
			continue
		}
		switch inner[i] {
		case '[', '(', '{':
			depth++
		case ']', ')', '}':
			depth--
		}
	}
	return out
}

func parsePython(dir string) (symbolSet, error) {
	files, err := sourceFiles(dir, ".py", nil)
	if err != nil {
		return nil, err
	}
	return parsePythonFiles(files)
}

// parsePythonFiles reads the classes, members and parameters of files.
func parsePythonFiles(files []string) (symbolSet, error) {
	syms := symbolSet{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		class := ""
		inDoc := false
		lines := strings.Split(string(raw), "\n")
		for i := 0; i < len(lines); i++ {
			line := lines[i]
			trimmed := strings.TrimSpace(line)
			if n := strings.Count(trimmed, `"""`); n == 1 {
				inDoc = !inDoc
				continue
			}
			if inDoc || trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
				class = ""
				if sm := pyClassRE.FindStringSubmatch(line); sm != nil {
					class = sm[1]
					syms[class] = true
				} else if sm := pyDefRE.FindStringSubmatch(line); sm != nil {
					syms[sm[1]] = true
				} else if sm := pyAssignRE.FindStringSubmatch(line); sm != nil {
					syms[sm[1]] = true
				}
				continue
			}
			if class == "" {
				continue
			}
			if sm := pyMemberRE.FindStringSubmatch(line); sm != nil {
				name := sm[1]
				if name == "" {
					name = sm[2]
				} else {
					// Record each parameter as Class.method(param): keyword
					// arguments are how Python spells upstream option fields.
					sig := line
					for !pySigEndRE.MatchString(sig) && i+1 < len(lines) {
						i++
						sig += " " + strings.TrimSpace(lines[i])
					}
					for _, p := range pyParams(sig) {
						syms[class+"."+name+"("+p+")"] = true
					}
				}
				syms[class+"."+name] = true
			} else if sm := pySelfRE.FindStringSubmatch(line); sm != nil {
				syms[class+"."+sm[1]] = true
			}
		}
	}
	return syms, nil
}

// ── Node runtime (JavaScript) ──────────────────────────────────────────────

var jsReadRE = regexp.MustCompile(`\b([A-Za-z_$][A-Za-z0-9_$]*)(?:\?\.|\.)([A-Za-z_$][A-Za-z0-9_$]*)`)

// parseNodeReads collects every obj.prop the Node runtime's source reads, as
// read:obj.prop: how it consumes the definitions an extension hands it (a
// tool's promptSnippet, a command's getArgumentCompletions). The runtime's own
// objects come from the runtime probe instead.
func parseNodeReads(files []string) (symbolSet, error) {
	syms := symbolSet{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		code := scanCode(string(raw), "js")
		for _, sm := range jsReadRE.FindAllStringSubmatch(code, -1) {
			syms["read:"+sm[1]+"."+sm[2]] = true
		}
	}
	return syms, nil
}
