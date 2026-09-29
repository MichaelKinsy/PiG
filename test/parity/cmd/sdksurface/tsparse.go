package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// tsMember is one member of a TypeScript interface, class, or object literal
// type: a property or a method signature.
type tsMember struct {
	Name     string
	Optional bool
	Method   bool
	// Type is the property type, or for a method the full signature after
	// the name (type parameters, parameters and return type).
	Type string
}

// tsDecl is one top-level interface, class, or type alias declaration.
type tsDecl struct {
	Name    string
	Kind    string // "interface", "class", or "type"
	Extends []string
	Members []tsMember // interface and class bodies
	Alias   string     // type alias right-hand side
	File    string
}

// tsModule holds the declarations of the upstream files the surface reads.
type tsModule struct {
	decls map[string]*tsDecl
}

func newTSModule() *tsModule { return &tsModule{decls: map[string]*tsDecl{}} }

// load parses one TypeScript file. A name declared in an earlier file wins,
// so callers load types.ts first and the files it imports after it.
func (m *tsModule) load(path, label string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	src := stripTSComments(string(raw))
	for _, d := range parseTSDecls(src) {
		d.File = label
		if _, ok := m.decls[d.Name]; !ok {
			m.decls[d.Name] = d
		}
	}
	return nil
}

func (m *tsModule) decl(name string) (*tsDecl, error) {
	d, ok := m.decls[name]
	if !ok {
		return nil, fmt.Errorf("upstream declaration %s not found", name)
	}
	return d, nil
}

var declStartRE = regexp.MustCompile(`(?m)^(?:export\s+)?(?:declare\s+)?(?:abstract\s+)?(interface|class|type)\s+([A-Za-z_][A-Za-z0-9_]*)`)

// parseTSDecls extracts top-level interface, class and type alias
// declarations. It is intentionally narrow: upstream's declaration files use
// one consistent style, and the parser tests fail loudly if that changes.
func parseTSDecls(src string) []*tsDecl {
	var out []*tsDecl
	for _, loc := range declStartRE.FindAllStringSubmatchIndex(src, -1) {
		kind := src[loc[2]:loc[3]]
		name := src[loc[4]:loc[5]]
		rest := src[loc[1]:]
		d := &tsDecl{Name: name, Kind: kind}
		rest = skipTypeParams(rest)
		switch kind {
		case "type":
			eq := strings.Index(rest, "=")
			if eq < 0 {
				continue
			}
			d.Alias = strings.TrimSpace(readUntilTopLevel(rest[eq+1:], ';'))
		default:
			open := indexTopLevel(rest, '{')
			if open < 0 {
				continue
			}
			header := rest[:open]
			if _, after, ok := strings.Cut(header, "extends"); ok {
				ext := after
				if j := strings.Index(ext, "implements"); j >= 0 {
					ext = ext[:j]
				}
				for _, e := range splitTopLevel(ext, ',') {
					if e = strings.TrimSpace(e); e != "" {
						d.Extends = append(d.Extends, e)
					}
				}
			}
			body, ok := bracedBody(rest[open:])
			if !ok {
				continue
			}
			if kind == "class" {
				d.Members = classMembers(body)
			} else {
				d.Members = parseMembers(body)
			}
		}
		out = append(out, d)
	}
	return out
}

// parseMembers splits an interface or object-literal body into its members.
func parseMembers(body string) []tsMember {
	var out []tsMember
	for _, part := range splitMembers(body) {
		part = strings.TrimPrefix(strings.TrimSpace(part), "readonly ")
		name, rest := leadingIdent(part)
		if name == "" {
			continue
		}
		mem := tsMember{Name: name}
		if strings.HasPrefix(rest, "?") {
			mem.Optional = true
			rest = rest[1:]
		}
		rest = strings.TrimSpace(rest)
		switch {
		case strings.HasPrefix(rest, "(") || strings.HasPrefix(rest, "<"):
			mem.Method = true
			mem.Type = rest
		case strings.HasPrefix(rest, ":"):
			mem.Type = strings.TrimSpace(rest[1:])
		default:
			continue
		}
		out = append(out, mem)
	}
	return out
}

var classMemberRE = regexp.MustCompile(`(?m)^\t(?:(?:public|readonly|async|override)\s+)*(get\s+)?([A-Za-z_$][A-Za-z0-9_$]*)(\??)\s*([(<:=;])`)

// classMembers lists a class's public instance members: the declarations at
// the class body's first indentation level (upstream indents with tabs).
// Private, protected, static and constructor members are not part of the
// surface an extension sees.
func classMembers(body string) []tsMember {
	var out []tsMember
	seen := map[string]bool{}
	for _, sm := range classMemberRE.FindAllStringSubmatch(body, -1) {
		name := sm[2]
		if name == "constructor" || name == "private" || name == "protected" || name == "static" || name == "set" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, tsMember{Name: name, Optional: sm[3] == "?", Method: sm[1] == "" && (sm[4] == "(" || sm[4] == "<")})
	}
	return out
}

// splitMembers splits a body at top-level member boundaries: a semicolon, or
// the end of a line when brackets are balanced and the line does not continue
// the member.
func splitMembers(body string) []string {
	var parts []string
	depth := 0
	start := 0
	inStr := byte(0)
	for i := 0; i < len(body); i++ {
		c := body[i]
		if inStr != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == inStr {
				inStr = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			inStr = c
		case '(', '[', '{', '<':
			if c == '<' && !isTypeAngle(body, i) {
				continue
			}
			depth++
		case ')', ']', '}', '>':
			if c == '>' && (i > 0 && body[i-1] == '=') {
				continue // arrow
			}
			if c == '>' && !closesAngle(body, i) {
				continue
			}
			depth--
		case ';':
			if depth == 0 {
				parts = append(parts, body[start:i])
				start = i + 1
			}
		case '\n':
			if depth == 0 {
				line := strings.TrimSpace(body[start:i])
				next := strings.TrimSpace(nextLine(body, i+1))
				if line != "" && !continues(line, next) {
					parts = append(parts, body[start:i])
					start = i + 1
				}
			}
		}
	}
	parts = append(parts, body[start:])
	return parts
}

func nextLine(s string, from int) string {
	if from >= len(s) {
		return ""
	}
	if j := strings.IndexByte(s[from:], '\n'); j >= 0 {
		return s[from : from+j]
	}
	return s[from:]
}

// continues reports whether a member line continues on the next line: a
// trailing operator, or a next line that starts with a union bar or arrow.
func continues(line, next string) bool {
	for _, suffix := range []string{"|", "&", ":", ",", "=>", "=", "("} {
		if strings.HasSuffix(line, suffix) {
			return true
		}
	}
	return strings.HasPrefix(next, "|") || strings.HasPrefix(next, "&") || strings.HasPrefix(next, "=>")
}

// isTypeAngle reports whether '<' at i opens a type argument list: it follows
// an identifier character or starts a generic method signature.
func isTypeAngle(s string, i int) bool {
	if i == 0 {
		return false
	}
	p := s[i-1]
	return p == '_' || p == '$' || (p >= 'a' && p <= 'z') || (p >= 'A' && p <= 'Z') || (p >= '0' && p <= '9')
}

// closesAngle reports whether '>' at i closes a type argument list; '>' in
// "=>" is handled by the caller.
func closesAngle(s string, i int) bool {
	return i == 0 || s[i-1] != '='
}

func leadingIdent(s string) (string, string) {
	if s == "" {
		return "", s
	}
	if s[0] == '"' || s[0] == '\'' {
		end := strings.IndexByte(s[1:], s[0])
		if end < 0 {
			return "", s
		}
		return s[1 : end+1], s[end+2:]
	}
	i := 0
	for i < len(s) {
		c := s[i]
		if c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (i > 0 && c >= '0' && c <= '9') {
			i++
			continue
		}
		break
	}
	return s[:i], s[i:]
}

// skipTypeParams drops a leading <...> type parameter list.
func skipTypeParams(s string) string {
	t := strings.TrimLeft(s, " \t")
	if !strings.HasPrefix(t, "<") {
		return s
	}
	depth := 0
	for i := 0; i < len(t); i++ {
		switch t[i] {
		case '<':
			depth++
		case '>':
			if i > 0 && t[i-1] == '=' {
				continue
			}
			depth--
			if depth == 0 {
				return t[i+1:]
			}
		}
	}
	return s
}

// bracedBody returns the text between the '{' at s[0] and its match.
func bracedBody(s string) (string, bool) {
	depth := 0
	inStr := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == inStr {
				inStr = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			inStr = c
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[1:i], true
			}
		}
	}
	return "", false
}

// indexTopLevel finds c outside brackets and strings.
func indexTopLevel(s string, c byte) int {
	depth := 0
	inStr := byte(0)
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inStr != 0 {
			if ch == '\\' {
				i++
				continue
			}
			if ch == inStr {
				inStr = 0
			}
			continue
		}
		if ch == c && depth == 0 {
			return i
		}
		switch ch {
		case '"', '\'', '`':
			inStr = ch
		case '(', '[', '{':
			depth++
		case '<':
			if isTypeAngle(s, i) {
				depth++
			}
		case ')', ']', '}':
			depth--
		case '>':
			if i > 0 && s[i-1] != '=' && depth > 0 {
				depth--
			}
		}
	}
	return -1
}

func readUntilTopLevel(s string, c byte) string {
	if i := indexTopLevel(s, c); i >= 0 {
		return s[:i]
	}
	return s
}

// splitTopLevel splits s at c outside brackets and strings.
func splitTopLevel(s string, c byte) []string {
	var out []string
	for {
		i := indexTopLevel(s, c)
		if i < 0 {
			out = append(out, s)
			return out
		}
		out = append(out, s[:i])
		s = s[i+1:]
	}
}

var (
	blockCommentRE = regexp.MustCompile(`(?s)/\*.*?\*/`)
	lineCommentRE  = regexp.MustCompile(`(?m)(^|[^:"'` + "`" + `])//.*$`)
)

func stripTSComments(s string) string {
	s = blockCommentRE.ReplaceAllString(s, "")
	return lineCommentRE.ReplaceAllString(s, "$1")
}

// ── Type expansion ──────────────────────────────────────────────────────────

var pickRE = regexp.MustCompile(`^(Pick|Omit)<\s*([A-Za-z_][A-Za-z0-9_]*)(?:<[^>]*>)?\s*,\s*([^>]*)>$`)

// fields returns the members of a type expression: an inline object literal,
// a Pick<>, a named interface or class (with inherited members), a union of
// any of those (members merged, first occurrence wins), or an intersection.
func (m *tsModule) fields(expr string) ([]tsMember, error) {
	expr = strings.TrimSpace(expr)
	expr = strings.TrimSuffix(expr, "| undefined")
	expr = strings.TrimSpace(expr)
	if strings.HasPrefix(expr, "|") {
		expr = strings.TrimSpace(expr[1:])
	}
	if parts := splitTopLevel(expr, '|'); len(parts) > 1 {
		return m.mergeFields(parts)
	}
	if parts := splitTopLevel(expr, '&'); len(parts) > 1 {
		return m.mergeFields(parts)
	}
	if strings.HasPrefix(expr, "{") {
		body, ok := bracedBody(expr)
		if !ok {
			return nil, fmt.Errorf("unbalanced object type %q", expr)
		}
		return parseMembers(body), nil
	}
	if strings.HasPrefix(expr, "(") && strings.HasSuffix(expr, ")") {
		return m.fields(expr[1 : len(expr)-1])
	}
	if sm := pickRE.FindStringSubmatch(expr); sm != nil {
		base, err := m.fields(sm[2])
		if err != nil {
			return nil, err
		}
		named := map[string]bool{}
		for _, k := range splitTopLevel(sm[3], '|') {
			named[strings.Trim(strings.TrimSpace(k), `"'`)] = true
		}
		keep := sm[1] == "Pick"
		var out []tsMember
		for _, f := range base {
			if named[f.Name] == keep {
				out = append(out, f)
			}
		}
		return out, nil
	}
	name, _ := leadingIdent(expr)
	if name == "" {
		return nil, nil
	}
	d, ok := m.decls[name]
	if !ok {
		return nil, nil // primitive or foreign type: no members
	}
	if d.Kind == "type" {
		return m.fields(d.Alias)
	}
	var out []tsMember
	seen := map[string]bool{}
	for _, f := range d.Members {
		if !seen[f.Name] {
			seen[f.Name] = true
			out = append(out, f)
		}
	}
	for _, ext := range d.Extends {
		inherited, err := m.fields(ext)
		if err != nil {
			return nil, err
		}
		for _, f := range inherited {
			if !seen[f.Name] {
				seen[f.Name] = true
				out = append(out, f)
			}
		}
	}
	return out, nil
}

func (m *tsModule) mergeFields(parts []string) ([]tsMember, error) {
	var out []tsMember
	seen := map[string]bool{}
	for _, p := range parts {
		fs, err := m.fields(p)
		if err != nil {
			return nil, err
		}
		for _, f := range fs {
			if !seen[f.Name] {
				seen[f.Name] = true
				out = append(out, f)
			}
		}
	}
	return out, nil
}

// tsParam is one parameter of a method signature.
type tsParam struct {
	Name     string
	Optional bool
	Type     string
}

// methodParams parses the parameter list of a method signature such as
// "<T>(a: string, b?: {x: number}): void".
func methodParams(sig string) []tsParam {
	sig = strings.TrimSpace(skipTypeParams(sig))
	if !strings.HasPrefix(sig, "(") {
		return nil
	}
	depth := 0
	end := -1
	for i := 0; i < len(sig); i++ {
		switch sig[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return nil
	}
	var out []tsParam
	for _, p := range splitTopLevel(sig[1:end], ',') {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		name, rest := leadingIdent(p)
		param := tsParam{Name: name}
		if strings.HasPrefix(rest, "?") {
			param.Optional = true
			rest = rest[1:]
		}
		param.Type = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), ":"))
		out = append(out, param)
	}
	return out
}

// typeArgs splits the type arguments of a generic reference such as
// "ExtensionHandler<A, B>".
func typeArgs(expr string) []string {
	i := strings.IndexByte(expr, '<')
	j := strings.LastIndexByte(expr, '>')
	if i < 0 || j < i {
		return nil
	}
	var out []string
	for _, a := range splitTopLevel(expr[i+1:j], ',') {
		out = append(out, strings.TrimSpace(a))
	}
	return out
}
