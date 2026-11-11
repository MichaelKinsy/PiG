package main

// Rule T12u: an optional function member of the object type a Pi factory returns is absent from the Go type when the factory's value
// never has it. The rule proves that from the pinned Pi source: it follows the factory's returned value through the functions and
// constants it is built from (a returned object literal, a call of a function in the same package, a spread of a constant or of a
// call) and requires that no object literal on the way names the member, that no spread on the way is of a value the rule cannot
// read, and that no statement after the construction mutates the value. Anything the reader does not understand refuses.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// tsReader reads function and constant declarations from the pinned Pi sources of one package.
type tsReader struct {
	root  string // the Pi checkout (.upstream/current)
	pkg   string // the package directory name under packages/
	files map[string]*tsFile
	funcs map[string][]string // exported function name -> files that declare it
	built bool
}

// tsFile is one source file with its skeleton: the text with comments, strings, template literals and regular expression literals
// blanked, so that brackets and keywords of the skeleton are code.
type tsFile struct {
	path string
	text string
	skel string
}

func newTSReader(root, pkg string) *tsReader {
	return &tsReader{root: root, pkg: pkg, files: map[string]*tsFile{}, funcs: map[string][]string{}}
}

var exportedFuncRe = regexp.MustCompile(`(?m)^export\s+(?:async\s+)?function\s+([A-Za-z_$][\w$]*)`)

func (r *tsReader) src() string { return filepath.Join(r.root, "packages", r.pkg, "src") }

func (r *tsReader) build() {
	if r.built {
		return
	}
	r.built = true
	_ = filepath.WalkDir(r.src(), func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if e.IsDir() && e.Name() == "node_modules" {
			return filepath.SkipDir
		}
		if e.IsDir() || !strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".d.ts") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, m := range exportedFuncRe.FindAllStringSubmatch(string(data), -1) {
			r.funcs[m[1]] = append(r.funcs[m[1]], path)
		}
		return nil
	})
}

func (r *tsReader) file(path string) *tsFile {
	if f, ok := r.files[path]; ok {
		return f
	}
	data, err := os.ReadFile(path)
	if err != nil {
		r.files[path] = nil
		return nil
	}
	f := &tsFile{path: path, text: string(data), skel: skeleton(string(data))}
	if !balanced(f.skel) {
		f = nil // the scanner lost track of a string or a regular expression: no rule may read this file
	}
	r.files[path] = f
	return f
}

// factoryNeverSets reports whether the exported function factory of the package returns a value that never has the member.
func (r *tsReader) factoryNeverSets(factory, member string) bool {
	r.build()
	paths := r.funcs[factory]
	if len(paths) != 1 {
		return false
	}
	f := r.file(paths[0])
	if f == nil {
		return false
	}
	return (&unsetProof{r: r, member: member}).function(f, factory, nil, nil) == unsetYes
}

// skeleton blanks comments, string and template literals (with their substitutions) and regular expression literals, keeping every
// other character and every newline in place.
func skeleton(text string) string {
	out := []byte(text)
	blank := func(from, to int) {
		for i := from; i < to && i < len(out); i++ {
			if out[i] != '\n' {
				out[i] = ' '
			}
		}
	}
	var skipTemplate func(i int) int
	skipQuoted := func(i int) int {
		q := text[i]
		j := i + 1
		for j < len(text) && text[j] != q && text[j] != '\n' {
			if text[j] == '\\' {
				j++
			}
			j++
		}
		return min(j+1, len(text))
	}
	skipTemplate = func(i int) int {
		j := i + 1
		for j < len(text) && text[j] != '`' {
			switch {
			case text[j] == '\\':
				j += 2
			case text[j] == '$' && j+1 < len(text) && text[j+1] == '{':
				depth := 1
				j += 2
				for j < len(text) && depth > 0 {
					switch text[j] {
					case '{':
						depth++
					case '}':
						depth--
					case '`':
						j = skipTemplate(j) - 1
					case '"', '\'':
						j = skipQuoted(j) - 1
					}
					j++
				}
			default:
				j++
			}
		}
		return min(j+1, len(text))
	}
	prev := byte(0) // the last code character, for the regular expression heuristic
	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == '/' && i+1 < len(text) && text[i+1] == '/':
			j := strings.IndexByte(text[i:], '\n')
			if j < 0 {
				j = len(text) - i
			}
			blank(i, i+j)
			i += j
		case c == '/' && i+1 < len(text) && text[i+1] == '*':
			j := strings.Index(text[i+2:], "*/")
			end := len(text)
			if j >= 0 {
				end = i + 2 + j + 2
			}
			blank(i, end)
			i = end
		case c == '"' || c == '\'':
			end := skipQuoted(i)
			blank(i+1, end-1)
			i = end
			prev = 'a'
		case c == '`':
			end := skipTemplate(i)
			blank(i+1, end-1)
			i = end
			prev = 'a'
		case c == '/' && strings.IndexByte("(,=:[!&|?{};+-*%<>~^", prev) >= 0 || c == '/' && prev == 0:
			j := i + 1
			inClass := false
			for j < len(text) && text[j] != '\n' && (text[j] != '/' || inClass) {
				switch text[j] {
				case '\\':
					j++
				case '[':
					inClass = true
				case ']':
					inClass = false
				}
				j++
			}
			end := min(j+1, len(text))
			blank(i+1, end-1)
			i = end
			prev = 'a'
		default:
			if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
				prev = c
			}
			i++
		}
	}
	return string(out)
}

// balanced reports whether the brackets of a skeleton nest and close.
func balanced(skel string) bool {
	var stack []byte
	for i := range len(skel) {
		switch skel[i] {
		case '(', '[', '{':
			stack = append(stack, skel[i])
		case ')', ']', '}':
			want := map[byte]byte{')': '(', ']': '[', '}': '{'}[skel[i]]
			if len(stack) == 0 || stack[len(stack)-1] != want {
				return false
			}
			stack = stack[:len(stack)-1]
		}
	}
	return len(stack) == 0
}

// matching returns the index of the bracket that closes the one at skel[open], or -1.
func matching(skel string, open int) int {
	depth := 0
	for i := open; i < len(skel); i++ {
		switch skel[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// topLevel splits the skeleton range [from, to) at the separator byte outside any bracket; each part is its [start, end).
func topLevel(skel string, from, to int, sep byte) [][2]int {
	var parts [][2]int
	depth, start := 0, from
	for i := from; i < to; i++ {
		switch skel[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case sep:
			if depth == 0 {
				parts = append(parts, [2]int{start, i})
				start = i + 1
			}
		}
	}
	if strings.TrimSpace(skel[start:to]) != "" {
		parts = append(parts, [2]int{start, to})
	}
	return parts
}

type unsetResult int

const (
	unsetYes     unsetResult = iota // the value has no such member
	unsetNo                         // the value may have it
	unsetUnknown                    // the reader does not understand the source: refuse
)

// binding is an expression of a caller (its text range in a file and the scope that gives its names meaning) bound to a parameter.
type binding struct {
	file     *tsFile
	from, to int
	scope    *scope
}

// scope resolves the names of one function body: its parameters bound to caller expressions, and its body range for local constants.
type scope struct {
	file       *tsFile
	body       [2]int
	params     map[string]*binding
	paramNames []string
}

// unsetProof is the state of one proof: the member and the recursion budget.
type unsetProof struct {
	r      *tsReader
	member string
	depth  int
}

var (
	identRe2    = regexp.MustCompile(`^[A-Za-z_$][\w$]*$`)
	asRe        = regexp.MustCompile(`\s(?:as|satisfies)\s`)
	callRe      = regexp.MustCompile(`^([A-Za-z_$][\w$]*)\s*\(`)
	importLineR = regexp.MustCompile(`(?s)import\s+(?:type\s+)?\{([^}]*)\}\s*from\s*"([^"]+)"`)
)

// function judges what the function named name in file f returns when called with the argument bindings.
func (p *unsetProof) function(f *tsFile, name string, args []*binding, _ *scope) unsetResult {
	if p.depth > 12 {
		return unsetUnknown
	}
	p.depth++
	defer func() { p.depth-- }()
	declRe := regexp.MustCompile(`(?m)^(?:export\s+)?(?:async\s+)?function\s+` + regexp.QuoteMeta(name) + `\s*(?:<[^(]*>)?\s*\(`)
	loc := declRe.FindStringIndex(f.skel)
	if loc == nil {
		return unsetUnknown
	}
	open := loc[1] - 1
	closeParen := matching(f.skel, open)
	if closeParen < 0 {
		return unsetUnknown
	}
	bodyOpen := functionBodyStart(f.skel, closeParen+1)
	if bodyOpen < 0 {
		return unsetUnknown
	}
	bodyClose := matching(f.skel, bodyOpen)
	if bodyClose < 0 {
		return unsetUnknown
	}
	sc := &scope{file: f, body: [2]int{bodyOpen + 1, bodyClose}, params: map[string]*binding{}}
	for i, part := range paramList(f.skel, open+1, closeParen) {
		param := paramName(f.skel[part[0]:part[1]])
		if param == "" {
			return unsetUnknown
		}
		sc.paramNames = append(sc.paramNames, param)
		if i < len(args) && args[i] != nil {
			sc.params[param] = args[i]
		}
	}
	for _, param := range sc.paramNames {
		if p.constMutated(&tsFile{skel: f.skel[bodyOpen : bodyClose+1]}, param) {
			return unsetUnknown // the function writes to a parameter: what it returns is no longer what its argument was
		}
	}
	returns, ok := p.returns(sc)
	if !ok || len(returns) == 0 {
		return unsetUnknown
	}
	result := unsetYes
	for _, ret := range returns {
		switch p.expr(sc, ret[0], ret[1]) {
		case unsetNo:
			return unsetNo
		case unsetUnknown:
			result = unsetUnknown
		}
	}
	return result
}

// functionBodyStart finds the `{` that opens a function body after its parameter list, skipping a return type annotation.
func functionBodyStart(skel string, from int) int {
	depth, angle := 0, 0
	for i := from; i < len(skel); i++ {
		switch skel[i] {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case '<':
			angle++
		case '>':
			if i > 0 && skel[i-1] == '=' {
				continue
			}
			angle--
		case '{':
			if depth == 0 && angle <= 0 {
				return i
			}
			if depth == 0 || angle > 0 {
				if end := matching(skel, i); end > 0 {
					i = end
				}
			}
		case ';':
			if depth == 0 && angle <= 0 {
				return -1
			}
		}
	}
	return -1
}

// paramList splits a parameter list at the commas outside brackets and outside the angle brackets of a type.
func paramList(skel string, from, to int) [][2]int {
	var parts [][2]int
	depth, start := 0, from
	for i := from; i < to; i++ {
		switch skel[i] {
		case '(', '[', '{', '<':
			depth++
		case ')', ']', '}':
			depth--
		case '>':
			if i == 0 || skel[i-1] != '=' {
				depth--
			}
		case ',':
			if depth == 0 {
				parts = append(parts, [2]int{start, i})
				start = i + 1
			}
		}
	}
	if strings.TrimSpace(skel[start:to]) != "" {
		parts = append(parts, [2]int{start, to})
	}
	return parts
}

// paramName is the identifier a parameter declares (`cwd: string`, `options?: X`, `definition: ToolDefinition<...>`).
func paramName(param string) string {
	param = strings.TrimSpace(param)
	end := strings.IndexAny(param, ":?=")
	if end >= 0 {
		param = param[:end]
	}
	param = strings.TrimSpace(param)
	if !identRe2.MatchString(param) {
		return ""
	}
	return param
}

var returnRe = regexp.MustCompile(`(?:^|[;{}\s])return\b`)

// returns lists the expressions the function body returns at its own level. Control flow is not followed: a return in any other
// statement (a block, or a braceless `if (c) return x;`) refuses, since the rule reads only straight-line factories.
func (p *unsetProof) returns(sc *scope) ([][2]int, bool) {
	skel := sc.file.skel
	var out [][2]int
	for _, stmt := range topLevel(skel, sc.body[0], sc.body[1], ';') {
		text := strings.TrimSpace(skel[stmt[0]:stmt[1]])
		if keyword(text, "return") {
			start := stmt[0] + strings.Index(skel[stmt[0]:stmt[1]], "return") + len("return")
			out = append(out, [2]int{start, stmt[1]})
			continue
		}
		if returnRe.MatchString(skel[stmt[0]:stmt[1]]) && !strings.HasPrefix(text, "const ") && !strings.HasPrefix(text, "Object.assign(") {
			return nil, false // a nested block may return: refuse
		}
	}
	return out, true
}

func keyword(text, word string) bool {
	rest, ok := strings.CutPrefix(text, word)
	return ok && (rest == "" || !isIdentByte(rest[0]))
}

// expr judges the expression at skeleton range [from, to) of the scope's file.
func (p *unsetProof) expr(sc *scope, from, to int) unsetResult {
	if p.depth > 24 {
		return unsetUnknown
	}
	p.depth++
	defer func() { p.depth-- }()
	skel := sc.file.skel
	text := strings.TrimSpace(skel[from:to])
	from += strings.Index(skel[from:to], text)
	if at := topLevelAs(text); at >= 0 {
		text = strings.TrimSpace(text[:at])
	}
	switch {
	case strings.HasPrefix(text, "{") && matching(skel, from) == from+len(text)-1:
		return p.literal(sc, from+1, from+len(text)-1)
	case identRe2.MatchString(text):
		return p.identifier(sc, text)
	}
	if m := callRe.FindStringSubmatch(text); m != nil {
		open := from + len(m[0]) - 1
		if matching(skel, open) != from+len(text)-1 {
			return unsetUnknown
		}
		return p.call(sc, m[1], open, from+len(text)-1)
	}
	return unsetUnknown
}

// topLevelAs returns the index of the ` as T` or ` satisfies T` cast that ends the expression, outside every bracket, or -1.
func topLevelAs(text string) int {
	for _, loc := range asRe.FindAllStringIndex(text, -1) {
		depth := 0
		for _, c := range text[:loc[0]] {
			switch c {
			case '(', '[', '{':
				depth++
			case ')', ']', '}':
				depth--
			}
		}
		if depth == 0 {
			return loc[0]
		}
	}
	return -1
}

// literal judges an object literal body: it must not name the member, and every spread must be a value that has none.
func (p *unsetProof) literal(sc *scope, from, to int) unsetResult {
	skel := sc.file.skel
	result := unsetYes
	for _, member := range topLevel(skel, from, to, ',') {
		text := strings.TrimSpace(skel[member[0]:member[1]])
		start := member[0] + strings.Index(skel[member[0]:member[1]], text)
		if rest, ok := strings.CutPrefix(text, "..."); ok {
			switch p.expr(sc, start+3+strings.Index(rest, strings.TrimSpace(rest)), member[1]) {
			case unsetNo:
				return unsetNo
			case unsetUnknown:
				result = unsetUnknown
			}
			continue
		}
		name := literalMemberName(text)
		if name == "" {
			result = unsetUnknown // a computed key, an accessor or a form the reader does not know
			continue
		}
		if name == p.member {
			if value, isProp := strings.CutPrefix(strings.TrimSpace(text[len(name):]), ":"); isProp && p.passesThrough(sc, strings.TrimSpace(value)) {
				continue
			}
			return unsetNo
		}
	}
	return result
}

// passesThrough reports whether a member's value is `<parameter>.<member>` of a parameter whose bound argument has no such member:
// the wrapper copies what its argument has.
func (p *unsetProof) passesThrough(sc *scope, value string) bool {
	param, prop, ok := strings.Cut(value, ".")
	if !ok || prop != p.member || !identRe2.MatchString(param) {
		return false
	}
	b := sc.params[param]
	if b == nil {
		return false
	}
	return p.expr(b.scope, b.from, b.to) == unsetYes
}

// literalMemberName is the key of an object literal member: `name: value`, `name(...) {...}`, `async name(...)` or a shorthand `name`.
func literalMemberName(member string) string {
	member = strings.TrimSpace(member)
	member = strings.TrimPrefix(member, "async ")
	member = strings.TrimSpace(strings.TrimPrefix(member, "*"))
	end := strings.IndexAny(member, ":(<")
	if end < 0 {
		end = len(member)
	}
	name := strings.TrimSpace(member[:end])
	if name == "get" || name == "set" || !identRe2.MatchString(name) {
		return ""
	}
	return name
}

// identifier judges a name: a parameter (its bound argument), a constant of the function body, a constant of the file or a constant
// the file imports from a relative module.
func (p *unsetProof) identifier(sc *scope, name string) unsetResult {
	if b := sc.params[name]; b != nil {
		return p.expr(b.scope, b.from, b.to)
	}
	if from, to, ok := p.constant(sc.file, sc.body, name); ok {
		if !p.unmutated(sc, name, from) {
			return unsetUnknown
		}
		return p.expr(sc, from, to)
	}
	whole := [2]int{0, len(sc.file.skel)}
	if from, to, ok := p.constant(sc.file, whole, name); ok {
		if !p.constMutated(sc.file, name) {
			return p.expr(&scope{file: sc.file, body: [2]int{0, 0}, params: map[string]*binding{}}, from, to)
		}
		return unsetUnknown
	}
	if target := p.importOf(sc.file, name); target != nil {
		from, to, ok := p.constant(target, [2]int{0, len(target.skel)}, name)
		if !ok || p.constMutated(target, name) {
			return unsetUnknown
		}
		return p.expr(&scope{file: target, params: map[string]*binding{}}, from, to)
	}
	return unsetUnknown
}

// constant locates `const name = <expr>` at the top level of the range and returns the skeleton range of <expr>.
func (p *unsetProof) constant(f *tsFile, within [2]int, name string) (from, to int, ok bool) {
	re := regexp.MustCompile(`(?:^|[;{}\s])(?:export\s+)?const\s+` + regexp.QuoteMeta(name) + `\b`)
	for _, stmt := range topLevel(f.skel, within[0], within[1], ';') {
		seg := f.skel[stmt[0]:stmt[1]]
		loc := re.FindStringIndex(seg)
		if loc == nil || strings.TrimSpace(seg[:loc[0]+1]) != "" && !strings.HasSuffix(strings.TrimSpace(seg[:loc[0]+1]), "}") && within[0] != 0 {
			continue
		}
		rest := seg[loc[1]:]
		eq := declarationEquals(rest)
		if eq < 0 {
			return 0, 0, false
		}
		return stmt[0] + loc[1] + eq + 1, stmt[1], true
	}
	return 0, 0, false
}

// declarationEquals finds the `=` of a declaration after its name and optional type annotation.
func declarationEquals(rest string) int {
	depth := 0
	for i := range len(rest) {
		switch rest[i] {
		case '(', '[', '{', '<':
			depth++
		case ')', ']', '}':
			depth--
		case '>':
			if i > 0 && rest[i-1] == '=' {
				continue
			}
			depth--
		case '=':
			if depth == 0 && (i+1 >= len(rest) || rest[i+1] != '>' && rest[i+1] != '=') {
				return i
			}
		}
	}
	return -1
}

// unmutated reports whether the function body uses the local constant only in its declaration and in `return name`, or in
// `Object.assign(name, {...})` with a literal that does not name the member and has no spread.
func (p *unsetProof) unmutated(sc *scope, name string, declAt int) bool {
	skel := sc.file.skel
	wordRe := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
	for _, stmt := range topLevel(skel, sc.body[0], sc.body[1], ';') {
		seg := skel[stmt[0]:stmt[1]]
		if stmt[0] <= declAt && declAt <= stmt[1] || !wordRe.MatchString(seg) {
			continue
		}
		text := strings.TrimSpace(seg)
		if onlyPropertyReads(seg, name) || p.passedToReader(sc, seg, stmt[0], name) {
			continue
		}
		if text == "return "+name || strings.HasPrefix(text, "return ") && strings.TrimSpace(text[len("return "):]) == name {
			continue
		}
		if rest, ok := strings.CutPrefix(text, "Object.assign("); ok && strings.HasPrefix(rest, name) {
			open := stmt[0] + strings.Index(seg, "Object.assign(") + len("Object.assign")
			end := matching(skel, open)
			args := topLevel(skel, open+1, end, ',')
			if end < 0 || len(args) != 2 || strings.TrimSpace(skel[args[0][0]:args[0][1]]) != name || end != stmt[1]-1 && strings.TrimSpace(skel[end+1:stmt[1]]) != "" {
				return false
			}
			lit := strings.TrimSpace(skel[args[1][0]:args[1][1]])
			at := args[1][0] + strings.Index(skel[args[1][0]:args[1][1]], lit)
			if !strings.HasPrefix(lit, "{") || matching(skel, at) != at+len(lit)-1 || p.literal(sc, at+1, at+len(lit)-1) != unsetYes {
				return false
			}
			continue
		}
		return false
	}
	return true
}

// onlyPropertyReads reports whether every use of name in the statement reads or calls a property of it (`name.a.b`) and none
// assigns one.
func onlyPropertyReads(seg, name string) bool {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b(\s*\.\s*[A-Za-z_$][\w$]*)*`)
	for _, loc := range re.FindAllStringSubmatchIndex(seg, -1) {
		chain := seg[loc[0]:loc[1]]
		if chain == name {
			return false
		}
		if assignOp.MatchString(strings.TrimLeft(seg[loc[1]:], " \t\n\r")) {
			return false
		}
		if before := strings.TrimRight(seg[:loc[0]], " \t\n\r"); strings.HasSuffix(before, "++") || strings.HasSuffix(before, "--") || strings.HasSuffix(before, "delete") {
			return false
		}
	}
	return true
}

var assignOp = regexp.MustCompile(`^(?:[-+*/%&|^?]{0,2}<{0,2}>{0,3}=(?:[^=>]|$)|\+\+|--)`)

var callStmtRe = regexp.MustCompile(`^(?:const\s+[A-Za-z_$][\w$]*\s*=\s*)?([A-Za-z_$][\w$]*)\s*\(`)

// passedToReader reports whether the statement is a call (optionally the initializer of a constant) whose arguments are the name as a
// whole or expressions without it, and the callee only reads the properties of the parameter that receives it.
func (p *unsetProof) passedToReader(sc *scope, seg string, at int, name string) bool {
	text := strings.TrimSpace(seg)
	m := callStmtRe.FindStringSubmatch(text)
	if m == nil {
		return false
	}
	skel := sc.file.skel
	open := at + strings.Index(seg, text) + len(m[0]) - 1
	end := matching(skel, open)
	if end < 0 || strings.TrimSpace(skel[end+1:at+len(seg)]) != "" {
		return false
	}
	target := sc.file
	if !regexp.MustCompile(`(?m)^(?:export\s+)?(?:async\s+)?function\s+` + regexp.QuoteMeta(m[1]) + `\b`).MatchString(sc.file.skel) {
		if target = p.importOf(sc.file, m[1]); target == nil {
			return false
		}
	}
	decl := regexp.MustCompile(`(?m)^(?:export\s+)?(?:async\s+)?function\s+` + regexp.QuoteMeta(m[1]) + `\s*(?:<[^(]*>)?\s*\(`).FindStringIndex(target.skel)
	if decl == nil {
		return false
	}
	closeParen := matching(target.skel, decl[1]-1)
	bodyOpen := functionBodyStart(target.skel, closeParen+1)
	if closeParen < 0 || bodyOpen < 0 || matching(target.skel, bodyOpen) < 0 {
		return false
	}
	body := target.skel[bodyOpen : matching(target.skel, bodyOpen)+1]
	params := paramList(target.skel, decl[1], closeParen)
	for i, a := range topLevel(skel, open+1, end, ',') {
		arg := strings.TrimSpace(skel[a[0]:a[1]])
		if !regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`).MatchString(arg) {
			continue
		}
		if arg != name || i >= len(params) {
			return false
		}
		param := paramName(target.skel[params[i][0]:params[i][1]])
		if param == "" || !onlyPropertyReads(body, param) {
			return false
		}
	}
	return true
}

// constMutated reports whether the file assigns a member of the constant or passes it to Object.assign.
func (p *unsetProof) constMutated(f *tsFile, name string) bool {
	q := regexp.QuoteMeta(name)
	return regexp.MustCompile(`Object\.assign\(\s*` + q + `\b|\b` + q + `\s*(?:\.[\w$]+|\[[^\]]*\])\s*=[^=]`).MatchString(f.skel)
}

// importOf resolves a name imported from a relative module to that module's file.
func (p *unsetProof) importOf(f *tsFile, name string) *tsFile {
	for _, m := range importLineR.FindAllStringSubmatch(f.text, -1) {
		if !strings.HasPrefix(m[2], ".") {
			continue
		}
		for spec := range strings.SplitSeq(m[1], ",") {
			spec = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(spec), "type "))
			if spec != name {
				continue
			}
			return p.r.file(filepath.Join(filepath.Dir(f.path), m[2]))
		}
	}
	return nil
}

// call judges a call of a function declared in this file or imported from a relative module.
func (p *unsetProof) call(sc *scope, callee string, open, closeParen int) unsetResult {
	target := sc.file
	if !regexp.MustCompile(`(?m)^(?:export\s+)?(?:async\s+)?function\s+` + regexp.QuoteMeta(callee) + `\b`).MatchString(sc.file.skel) {
		target = p.importOf(sc.file, callee)
		if target == nil {
			return unsetUnknown
		}
	}
	var args []*binding
	for _, a := range topLevel(sc.file.skel, open+1, closeParen, ',') {
		args = append(args, &binding{file: sc.file, from: a[0], to: a[1], scope: sc})
	}
	return p.function(target, callee, args, sc)
}
