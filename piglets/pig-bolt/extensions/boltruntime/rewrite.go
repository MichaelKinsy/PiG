package boltruntime

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// A compiled executable has no directory of its own for the modules it holds: the bundler resolves `import.meta.url` once, to the executable, and so `import.meta.dir`,
// `__dirname` and the rest. An extension that finds its skills, prompts, locales or a data file beside its own module would look for them under /$bunfs. The build
// therefore compiles each file that names its location from a copy in which those expressions say what they are in the file the copy was made from: the extension as
// installed. A compiled-in extension runs only while its installed files are unchanged (the image's inputs), so those files are there.
//
// This is a token lexer and a set of decisions over its tokens, ported from the rules of Pi-Bolt's rewriteCopy (packages/coding-agent/src/gen-plugin-manifest.mjs, MIT; see
// THIRD_PARTY_NOTICES.md) and from the code it substitutes (requireFrom, resolveFrom, metaResolveFrom, the import.meta table), so that an extension compiled in here reads its
// own location as one that `pi-bolt compile` compiles does. It is not a line-by-line port and it does not give the same output in every case. The differences:
//   - A module's declarations of __dirname, __filename and require are decided from tokens, not from a regular expression over the text: a comment or a string that spells a
//     declaration is none, an argument is no parameter, an object literal's shorthand is no binding, a member of a class, an enum or an interface is no variable, and an
//     import binds the name after `as`, not the one before it.
//   - A `{ __dirname }` shorthand keeps its property name.
//   - `++` and `--` are one token, and after one that follows a value (`i++ / 2`) a `/` divides; a word after `.` or `?.` (`o.return / 2`) is a value, not a keyword.
//   - A line ends at LF, CR, U+2028 and U+2029 (comments, regular expressions, strings), and a backslash before CR LF in a string continues it.
//   - .js, .mjs and .cjs files are scanned for JSX as .jsx and .tsx files are; a `<` where an expression may begin is not valid JavaScript otherwise.
//   - TypeScript-only positions are left alone: the name after `typeof`, enum members, class fields and parameter properties, interface members, export names and the
//     names a re-export mentions. `(import.meta as T).dirname` is read like `import.meta.dirname`.
//   - A static require("fs") with a comment before the string stays the bundler's. Pi-Bolt's renaming of Pi's own module names (@mariozechner/...) is not ported: the build's
//     tsconfig covers it.
//
// The rewrite is a scanner, not a parser: it decides what is code from the tokens it reads, as the tools that bundle JavaScript commonly do, and it changes code only,
// never a comment, a string, the text of a template literal, a regular expression or JSX text.

// rewriteResult is a module as the build is to have it.
type rewriteResult struct {
	// Source is the module with each expression that names its location replaced by what it is for the original file.
	Source  string
	Changed bool
	// Unpinned names a way the module names its location that the rewrite cannot pin to the original file ("import.meta" used as a value); "" when there is none.
	Unpinned string
	// ReadsFiles is whether an unpinned module also reads files or starts a process or a worker by path: only then does the location matter.
	ReadsFiles bool
}

type tokKind uint8

const (
	tkWord tokKind = iota + 1
	tkPunct
	// tkString is a string literal, or a template literal without substitutions.
	tkString
	// tkTemplate is a part of a template literal that has substitutions: from a backtick or a `}` to the next `${` or the closing backtick.
	tkTemplate
	tkRegex
	tkNumber
)

type token struct {
	kind       tokKind
	start, end int
	// text is a word's or a punctuator's text.
	text string
	// property is a word that follows a `.`, as in `x.import`; a spread (`...x`) is not one.
	property bool
	// opens and closes are set on a template part that ends at `${` or begins at `}`.
	opens, closes bool
	// jsxBrace is the `{` of a JSX expression container.
	jsxBrace bool
}

type prevKind uint8

const (
	pvPunct prevKind = iota
	pvWord
	pvValue
)

// prevToken is the last token, for the guesses: a word (its text), a value, or a punctuator (its text). expr on a `)` or a `}` overrides whether an expression may
// begin after it.
type prevToken struct {
	kind    prevKind
	text    string
	hasExpr bool
	expr    bool
}

type braceKind uint8

const (
	brBlock braceKind = iota
	brObject
	brTemplate
	brJSX
)

type jsxElement struct {
	open    int
	closing bool
}

type braceEntry struct {
	kind braceKind
	el   *jsxElement
	mode jsxMode
}

type jsxMode uint8

const (
	jsxChildren jsxMode = iota
	jsxTag
)

// Words after which a `/` begins a regular expression rather than dividing, and a `{` an object rather than a block.
var beforeExpression = setOf("return typeof instanceof in of new delete void throw case do else yield await extends")

// Words whose `(...)` is followed by a statement, so a `/` after the `)` begins a regular expression.
var beforeCondition = setOf("if", "while", "for", "with")

// Punctuators after which a `{` is an object literal rather than a block.
var beforeObject = setOf("( [ , = ? : ! & | + - * % ^ ~ < >")

func setOf(words ...string) map[string]bool {
	set := map[string]bool{}
	for _, w := range words {
		for _, f := range strings.Fields(w) {
			set[f] = true
		}
	}
	return set
}

func isASCIIWordByte(c byte) bool {
	return c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// A byte of 0x80 or more continues a word, as `[\w$\u0080-\uffff]` does in Pi-Bolt's scanner: every code unit above ASCII is a word character, so are the bytes of its
// UTF-8 encoding.
func isWordByte(c byte) bool { return isASCIIWordByte(c) || c >= 0x80 }

func isWordStartByte(c byte) bool { return isWordByte(c) && (c < '0' || c > '9') }

// isWordAt is whether s[i] continues a word: a word byte that is no white space (a no-break space or U+2028 is two or three bytes of 0x80 or more, and ends the word).
func isWordAt(s string, i int) bool { return isWordByte(s[i]) && spaceWidth(s, i) == 0 }

// spaceWidth returns the byte length of the JavaScript white space at s[i] (`\s`), or 0.
func spaceWidth(s string, i int) int {
	switch c := s[i]; {
	case c == ' ' || c >= '\t' && c <= '\r':
		return 1
	case c < 0x80:
		return 0
	}
	r, n := utf8.DecodeRuneInString(s[i:])
	if r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff {
		return n
	}
	return 0
}

// lineTerminatorWidth returns the byte length of the line terminator at s[i] (LF, CR, U+2028, U+2029), or 0. CR LF is two terminators here, which ends a line all the same.
func lineTerminatorWidth(s string, i int) int {
	switch c := s[i]; {
	case c == '\n' || c == '\r':
		return 1
	case c == 0xe2 && i+2 < len(s) && s[i+1] == 0x80 && (s[i+2] == 0xa8 || s[i+2] == 0xa9):
		return 3
	}
	return 0
}

// lineEnd returns the index of the first line terminator of s at or after i, or len(s).
func lineEnd(s string, i int) int {
	for ; i < len(s); i++ {
		if lineTerminatorWidth(s, i) > 0 {
			return i
		}
	}
	return len(s)
}

type lexer struct {
	src    string
	jsx    bool
	i      int
	toks   []token
	prev   prevToken
	parens []bool
	braces []braceEntry
}

func lex(src string, jsx bool) []token {
	l := &lexer{src: src, jsx: jsx, prev: prevToken{kind: pvPunct}}
	l.run()
	return l.toks
}

func (l *lexer) emit(kind tokKind, start int, text string) *token {
	l.toks = append(l.toks, token{kind: kind, start: start, end: l.i, text: text})
	return &l.toks[len(l.toks)-1]
}

func (l *lexer) expressionMayBegin() bool {
	switch l.prev.kind {
	case pvWord:
		return beforeExpression[l.prev.text]
	case pvPunct:
		if l.prev.hasExpr {
			return l.prev.expr
		}
		return l.prev.text != ")" && l.prev.text != "]" && l.prev.text != "}"
	}
	return false
}

// skipQuoted reads a string from the quote at l.i. A string ends at the end of its line at the latest, so a wrong guess cannot hide the rest of the file. A backslash before
// a line terminator continues the string over the line (CR LF counts as one terminator there).
func (l *lexer) skipQuoted(quote byte) {
	for l.i++; l.i < len(l.src); l.i++ {
		switch l.src[l.i] {
		case '\\':
			l.i++
			if l.i+1 < len(l.src) && l.src[l.i] == '\r' && l.src[l.i+1] == '\n' {
				l.i++
			}
		case '\n', '\r':
			return
		case quote:
			l.i++
			return
		}
	}
	if l.i > len(l.src) {
		l.i = len(l.src)
	}
}

// skipTemplateText reads the text of a template literal from l.i; true when it stops at a `${`.
func (l *lexer) skipTemplateText() bool {
	for ; l.i < len(l.src); l.i++ {
		switch c := l.src[l.i]; {
		case c == '\\':
			l.i++
		case c == '`':
			l.i++
			return false
		case c == '$' && l.i+1 < len(l.src) && l.src[l.i+1] == '{':
			l.i += 2
			return true
		}
	}
	if l.i > len(l.src) {
		l.i = len(l.src)
	}
	return false
}

// skipRegExp reads a regular expression from the `/` at l.i; false (and l.i unmoved) when the line ends first.
func (l *lexer) skipRegExp() bool {
	src := l.src
	j := l.i + 1
	inClass := false
scan:
	for ; j < len(src); j++ {
		if lineTerminatorWidth(src, j) > 0 {
			return false
		}
		switch src[j] {
		case '\\':
			j++
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '/':
			if !inClass {
				break scan
			}
		}
	}
	if j >= len(src) {
		return false
	}
	for j++; j < len(src) && isWordAt(src, j); j++ {
	}
	l.i = j
	return true
}

// jsxBegins is whether the `<` at l.i begins JSX: `<>`, or `<name` that is not `<T,` or `<T extends`.
func (l *lexer) jsxBegins() bool {
	src := l.src
	if l.i+1 < len(src) && src[l.i+1] == '>' {
		return true
	}
	if l.i+1 >= len(src) || !isWordStartByte(src[l.i+1]) {
		return false
	}
	j := l.i + 2
	for j < len(src) && isWordAt(src, j) {
		j++
	}
	for j < len(src) && spaceWidth(src, j) > 0 {
		j += spaceWidth(src, j)
	}
	if j < len(src) && src[j] == ',' {
		return false
	}
	return !strings.HasPrefix(src[j:], "extends") || j+7 < len(src) && isWordByte(src[j+7])
}

// scanJSX reads JSX from l.i for el (how many tags are open, whether the one being read closes) in mode: inside a tag, or between tags (children). It stops when the
// outermost element ends (the JSX is then a value), or at a `{`, whose code the main loop reads; its `}` comes back here.
func (l *lexer) scanJSX(el *jsxElement, mode jsxMode) {
	src := l.src
loop:
	for l.i < len(src) {
		c := src[l.i]
		switch {
		case mode == jsxChildren:
			if c == '{' {
				break loop
			}
			if c == '<' {
				el.closing = l.i+1 < len(src) && src[l.i+1] == '/'
				if !el.closing {
					el.open++
					l.i++
				} else {
					l.i += 2
				}
				mode = jsxTag
			} else {
				l.i++
			}
		case c == '{':
			break loop
		case c == '"' || c == '\'':
			// An attribute's string: no escapes, and it may run over lines.
			end := strings.IndexByte(src[l.i+1:], c)
			if end < 0 {
				l.i = len(src)
			} else {
				l.i += end + 2
			}
		case c == '/' && l.i+1 < len(src) && src[l.i+1] == '>':
			l.i += 2
			el.open--
			if el.open == 0 {
				break loop
			}
			mode = jsxChildren
		case c == '>':
			l.i++
			if el.closing {
				el.open--
				if el.open == 0 {
					break loop
				}
			}
			mode = jsxChildren
		default:
			l.i++
		}
	}
	if l.i < len(src) && src[l.i] == '{' {
		l.braces = append(l.braces, braceEntry{kind: brJSX, el: el, mode: mode})
		l.i++
		l.emit(tkPunct, l.i-1, "{").jsxBrace = true
		l.prev = prevToken{kind: pvPunct, text: "{"}
		return
	}
	l.prev = prevToken{kind: pvValue}
}

func (l *lexer) run() {
	src := l.src
	n := len(src)
	if strings.HasPrefix(src, "#!") {
		l.i = lineEnd(src, 0)
	}
	for l.i < n {
		c := src[l.i]
		var next byte
		if l.i+1 < n {
			next = src[l.i+1]
		}
		switch {
		case c == '/' && next == '/':
			l.i = lineEnd(src, l.i)
		case c == '/' && next == '*':
			if end := strings.Index(src[l.i+2:], "*/"); end >= 0 {
				l.i += 2 + end + 2
			} else {
				l.i = n
			}
		case spaceWidth(src, l.i) > 0:
			l.i += spaceWidth(src, l.i)
		case c == '\'' || c == '"':
			start := l.i
			l.skipQuoted(c)
			l.emit(tkString, start, string(c))
			l.prev = prevToken{kind: pvValue}
		case c == '`':
			start := l.i
			l.i++
			if l.skipTemplateText() {
				l.braces = append(l.braces, braceEntry{kind: brTemplate})
				l.emit(tkTemplate, start, "`").opens = true
				l.prev = prevToken{kind: pvPunct, text: "{"}
			} else {
				l.emit(tkString, start, "`")
				l.prev = prevToken{kind: pvValue}
			}
		case c == '(':
			l.parens = append(l.parens, l.prev.kind == pvWord && beforeCondition[l.prev.text])
			l.i++
			l.emit(tkPunct, l.i-1, "(")
			l.prev = prevToken{kind: pvPunct, text: "("}
		case c == ')':
			condition := false
			if k := len(l.parens); k > 0 {
				condition = l.parens[k-1]
				l.parens = l.parens[:k-1]
			}
			l.i++
			l.emit(tkPunct, l.i-1, ")")
			l.prev = prevToken{kind: pvPunct, text: ")", hasExpr: true, expr: condition}
		case c == '{':
			object := false
			switch l.prev.kind {
			case pvWord:
				object = beforeExpression[l.prev.text] && l.prev.text != "else" && l.prev.text != "do"
			case pvPunct:
				object = beforeObject[l.prev.text]
			}
			kind := brBlock
			if object {
				kind = brObject
			}
			l.braces = append(l.braces, braceEntry{kind: kind})
			l.i++
			l.emit(tkPunct, l.i-1, "{")
			l.prev = prevToken{kind: pvPunct, text: "{"}
		case c == '}':
			start := l.i
			l.i++
			var brace braceEntry
			haveBrace := false
			if k := len(l.braces); k > 0 {
				brace, haveBrace = l.braces[k-1], true
				l.braces = l.braces[:k-1]
			}
			switch {
			case haveBrace && brace.kind == brTemplate:
				sub := l.skipTemplateText()
				t := l.emit(tkTemplate, start, "`")
				t.closes, t.opens = true, sub
				if sub {
					l.braces = append(l.braces, braceEntry{kind: brTemplate})
					l.prev = prevToken{kind: pvPunct, text: "{"}
				} else {
					l.prev = prevToken{kind: pvValue}
				}
			case haveBrace && brace.kind == brJSX:
				l.emit(tkPunct, start, "}")
				l.scanJSX(brace.el, brace.mode)
			default:
				l.emit(tkPunct, start, "}")
				l.prev = prevToken{kind: pvPunct, text: "}", hasExpr: true, expr: !haveBrace || brace.kind != brObject}
			}
		case c == '/' && l.expressionMayBegin() && l.regexpAhead():
			l.prev = prevToken{kind: pvValue}
		case c == '<' && l.jsx && l.expressionMayBegin() && l.jsxBegins():
			l.scanJSX(&jsxElement{}, jsxChildren)
		case c == '=' && next == '>':
			l.i += 2
			l.emit(tkPunct, l.i-2, "=>")
			l.prev = prevToken{kind: pvPunct, text: "=>"}
		case (c == '+' || c == '-') && next == c:
			// `++` and `--` are one token. After a value they are postfix (`i++ / 2`: the `/` divides); where an expression may begin they are prefix (`++i`).
			postfix := !l.expressionMayBegin()
			l.i += 2
			l.emit(tkPunct, l.i-2, src[l.i-2:l.i])
			if postfix {
				l.prev = prevToken{kind: pvValue}
			} else {
				l.prev = prevToken{kind: pvPunct, text: src[l.i-2 : l.i]}
			}
		case c == '!' && !l.expressionMayBegin():
			// TypeScript's non-null assertion, `x! / 2`: a postfix operator, so the value goes on.
			l.i++
			l.emit(tkPunct, l.i-1, "!")
			l.prev = prevToken{kind: pvValue}
		case isWordStartByte(c):
			start := l.i
			for l.i++; l.i < n && isWordAt(src, l.i); l.i++ {
			}
			word := src[start:l.i]
			t := l.emit(tkWord, start, word)
			t.property = l.afterDot(len(l.toks) - 1)
			if t.property {
				// `o.return / 2`: a member's name is a value whatever it spells.
				l.prev = prevToken{kind: pvValue}
			} else {
				l.prev = prevToken{kind: pvWord, text: word}
			}
		case c >= '0' && c <= '9':
			start := l.i
			for l.i++; l.i < n && (isASCIIWordByte(src[l.i]) && src[l.i] != '$' || src[l.i] == '.'); l.i++ {
			}
			l.emit(tkNumber, start, "")
			l.prev = prevToken{kind: pvValue}
		default:
			l.i++
			l.emit(tkPunct, l.i-1, string(c))
			l.prev = prevToken{kind: pvPunct, text: string(c)}
		}
	}
}

// regexpAhead reads a regular expression at l.i, as a token, if one is there.
func (l *lexer) regexpAhead() bool {
	start := l.i
	if !l.skipRegExp() {
		return false
	}
	l.emit(tkRegex, start, "")
	return true
}

// afterDot is whether the word at toks[k] follows a `.` that is not part of a spread.
func (l *lexer) afterDot(k int) bool {
	t := l.toks
	if k == 0 || t[k-1].kind != tkPunct || t[k-1].text != "." {
		return false
	}
	// `...x`: three adjacent dots.
	if k >= 3 && isAdjacentDot(t[k-2], t[k-1]) && isAdjacentDot(t[k-3], t[k-2]) {
		return false
	}
	return true
}

func isAdjacentDot(a, b token) bool {
	return a.kind == tkPunct && a.text == "." && b.kind == tkPunct && b.text == "." && a.end == b.start
}

// importMetaValue is what each `import.meta` property the bundler resolves to the output file is for a module at file.
var importMetaValue = map[string]func(file string) string{
	"url":      fileURL,
	"dir":      filepath.Dir,
	"dirname":  filepath.Dir,
	"filename": func(file string) string { return file },
	"path":     func(file string) string { return file },
	"file":     filepath.Base,
}

// piModule is Pi's names for its own modules, which Pi gives an extension whatever it has installed.
const piModule = `/^(?:typebox|@sinclair\/typebox|@(?:earendil-works|mariozechner)\/pi-(?:agent-core|ai|coding-agent|tui))(?:\/|$)/`

// jsLiteral is text as a JavaScript string literal. inCode writes the characters that can end a script or a line as escapes, as Pi-Bolt's jsStringLiteral does for code
// it makes; a literal that replaces an expression is plain JSON.
func jsLiteral(text string, inCode bool) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(text)
	out := strings.TrimSuffix(buf.String(), "\n")
	if !inCode {
		return out
	}
	var b strings.Builder
	for _, r := range out {
		switch r {
		case '<', '>', '/', '\u2028', '\u2029':
			b.WriteString(`\u`)
			b.WriteString(strings.Repeat("0", 4-len(hex4(r))))
			b.WriteString(hex4(r))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func hex4(r rune) string {
	const digits = "0123456789abcdef"
	var out []byte
	for v := int(r); v > 0; v >>= 4 {
		out = append([]byte{digits[v&15]}, out...)
	}
	return string(out)
}

// requireFrom is code for `require` as in the module at file: createRequire for that file.
func requireFrom(file string) string {
	return `process.getBuiltinModule("node:module").createRequire(` + jsLiteral(file, true) + `)`
}

// resolveFrom is code for a function that resolves an import()'s specifier as from the module at file: a path, or a package installed there, to its file; Pi's own
// modules, a URL, a built-in, and what does not resolve, as they are (the import() then finds those as it did).
func resolveFrom(file string) string {
	return `((s) => { if (typeof s !== "string" || ` + piModule + `.test(s) || /^[a-z][a-z0-9+.-]*:/i.test(s)) return s; try { return Bun.resolveSync(s, ` + jsLiteral(filepath.Dir(file), true) + `); } catch { return s; } })`
}

// metaResolveFrom is code for `import.meta.resolve` of the module at file: the URL of what a specifier resolves to from there.
func metaResolveFrom(file string) string {
	return `((s) => { const p = Bun.resolveSync(s, ` + jsLiteral(filepath.Dir(file), true) + `); return p.startsWith("/") ? Bun.pathToFileURL(p).href : p; })`
}

// fileURL is what Node's pathToFileURL gives for an absolute path: the path's bytes percent-encoded but for letters, digits and -._!$&'()*+,/:;=@.
func fileURL(path string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	b.WriteString("file://")
	if !strings.HasPrefix(path, "/") {
		b.WriteByte('/')
		path = strings.ReplaceAll(path, `\`, "/")
	}
	for i := 0; i < len(path); i++ {
		c := path[i]
		if isASCIIWordByte(c) && c != '$' || strings.IndexByte("-._!$&'()*+,/:;=@", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexDigits[c>>4])
		b.WriteByte(hexDigits[c&15])
	}
	return b.String()
}

type edit struct {
	start, end int
	code       string
	breaks     int
}

// isTypeScriptFile is whether file is TypeScript without JSX: there a `<` where an expression may begin is a type assertion, not an element.
func isTypeScriptFile(file string) bool {
	switch filepath.Ext(file) {
	case ".ts", ".mts", ".cts":
		return true
	}
	return false
}

// rewriteModule returns source, the module at file, as the build is to have it: each `import.meta.<property>` that the bundler would resolve to the output file replaced
// by its value for the module at file, as a string literal; `__dirname`, `__filename`, `require.resolve`, `import.meta.resolve` and each require() or import() of an
// expression replaced by code for what they are at file. A module that declares __dirname, __filename or require itself keeps its own. Line numbers do not change.
//
// It is a scanner, not a parser, and guesses where a parser would know:
//   - A `/` begins a regular expression or divides, by the token before it. After `)` it divides, unless the `(` came after `if`, `while`, `for` or `with`; after `}` it divides
//     when the `{` began an object literal (after `=`, `(`, `,`, `:`, `return` and the like), and otherwise begins a regular expression; after a postfix `++`, `--` or `!`
//     and after a member's name it divides.
//   - Outside .ts, .mts and .cts files a `<` where an expression may begin, followed by a name or `>`, begins JSX, unless it reads `<T,` or `<T extends`, a generic arrow
//     function.
//   - A string or regular expression ends at the end of its line at the latest, so a wrong guess cannot hide the rest of the file.
//
// A wrong guess either leaves an `import.meta.url` in code as it was, which is what bundling gave it before, or changes one in text.
func rewriteModule(source, file string) rewriteResult {
	toks := lex(source, !isTypeScriptFile(file))
	a := analyze(source, toks)
	var edits []edit
	unpinned := ""
	replace := func(start, end int, code string) {
		edits = append(edits, edit{start: start, end: end, code: code, breaks: strings.Count(source[start:end], "\n")})
	}
	n := len(toks)
	for i := 0; i < n; i++ {
		t := toks[i]
		if t.kind != tkWord || t.property {
			continue
		}
		// `typeof __dirname` is a string whatever the name stands for, and in a type (`x: typeof __dirname`) it is no expression to replace.
		if i > 0 && a.isWord(i-1, "typeof") && !toks[i-1].property {
			continue
		}
		switch t.text {
		case "import":
			// A TypeScript cast, `(import.meta as any).dirname`, for members the types of import.meta lack. (Not in Pi-Bolt's scanner, which leaves it.)
			if a.castImportMeta(i) {
				if value, ok := importMetaValue[toks[i+7].text]; ok {
					replace(toks[i-1].start, toks[i+7].end, jsLiteral(value(file), false))
					i += 7
					continue
				}
				if toks[i+7].text == "resolve" {
					replace(toks[i-1].start, toks[i+7].end, metaResolveFrom(file))
					i += 7
					continue
				}
			}
			if a.isPunct(i+1, ".") && a.isWord(i+2, "meta") {
				if !a.isPunct(i+3, ".") || i+4 >= n || toks[i+4].kind != tkWord {
					unpinned = "import.meta"
					continue
				}
				prop := toks[i+4].text
				if value, ok := importMetaValue[prop]; ok {
					replace(t.start, toks[i+4].end, jsLiteral(value(file), false))
					i += 4
				} else if prop == "resolve" {
					replace(t.start, toks[i+4].end, metaResolveFrom(file))
					i += 4
				}
				continue
			}
			// An import() of anything but a string: from the installed file, not from the executable's file system.
			if open := a.callOfExpression(i); open >= 0 && !a.isMethod(i, open) {
				replace(t.start, toks[open].end, "((s, o) => import("+resolveFrom(file)+"(s), o))(")
			}
		case "__dirname", "__filename":
			if a.declares(t.text) || a.isPunct(i+1, "(") || a.isMemberName(i) {
				continue
			}
			value := jsLiteral(file, false)
			if t.text == "__dirname" {
				value = jsLiteral(filepath.Dir(file), false)
			}
			switch a.position(i) {
			case posExported:
				continue
			case posShorthand:
				// `{ __dirname }` in an object literal: the property keeps its name.
				value = t.text + ": " + value
			}
			replace(t.start, t.end, value)
		case "require":
			if a.declares("require") {
				continue
			}
			if a.isPunct(i+1, ".") && a.isWord(i+2, "resolve") {
				replace(t.start, toks[i+2].end, requireFrom(file)+".resolve")
				i += 2
				continue
			}
			if open := a.callOfExpression(i); open >= 0 && !a.isMethod(i, open) {
				replace(t.start, toks[open].end, requireFrom(file)+"(")
			}
		}
	}
	res := rewriteResult{Source: source, Unpinned: unpinned}
	if !utf8.ValidString(file) && (unpinned != "" || len(edits) > 0) {
		// No string literal names the path: nothing can be pinned.
		return rewriteResult{Source: source, Unpinned: "a path that is not UTF-8", ReadsFiles: a.readsFiles()}
	}
	if unpinned != "" {
		res.ReadsFiles = a.readsFiles()
	}
	if len(edits) == 0 {
		return res
	}
	var out strings.Builder
	at := 0
	for _, e := range edits {
		out.WriteString(source[at:e.start])
		out.WriteString(e.code)
		out.WriteString(strings.Repeat("\n", e.breaks))
		at = e.end
	}
	out.WriteString(source[at:])
	res.Source, res.Changed = out.String(), true
	return res
}
