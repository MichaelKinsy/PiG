package boltruntime

// analysis is a module's tokens with what the decisions look up in them.
type analysis struct {
	src  string
	toks []token
	// match is the index of the bracket token that pairs with the one at i, or -1; encl is the index of the innermost bracket token that is open at i, or -1.
	match, encl []int
	declared    map[string]bool
}

func analyze(src string, toks []token) *analysis {
	a := &analysis{src: src, toks: toks, match: make([]int, len(toks)), encl: make([]int, len(toks))}
	var stack []int
	pop := func(i int) {
		if k := len(stack); k > 0 {
			a.match[i], a.match[stack[k-1]] = stack[k-1], i
			stack = stack[:k-1]
		}
	}
	for i := range toks {
		a.match[i] = -1
	}
	for i, t := range toks {
		a.encl[i] = -1
		if k := len(stack); k > 0 {
			a.encl[i] = stack[k-1]
		}
		switch {
		case t.kind == tkPunct && (t.text == "(" || t.text == "[" || t.text == "{"):
			stack = append(stack, i)
		case t.kind == tkPunct && (t.text == ")" || t.text == "]" || t.text == "}"):
			pop(i)
			a.encl[i] = -1
			if k := len(stack); k > 0 {
				a.encl[i] = stack[k-1]
			}
		case t.kind == tkTemplate:
			if t.closes {
				pop(i)
				a.encl[i] = -1
				if k := len(stack); k > 0 {
					a.encl[i] = stack[k-1]
				}
			}
			if t.opens {
				stack = append(stack, i)
			}
		}
	}
	return a
}

func (a *analysis) isPunct(i int, text string) bool {
	return i >= 0 && i < len(a.toks) && a.toks[i].kind == tkPunct && a.toks[i].text == text
}

func (a *analysis) isWord(i int, text string) bool {
	return i >= 0 && i < len(a.toks) && a.toks[i].kind == tkWord && a.toks[i].text == text
}

// callOfExpression returns the index of the `(` of the call of `import` or `require` at word i when the bundler leaves its specifier as it is: anything but one string
// literal (or a template literal without `${}`) followed by the end of the arguments, or by `,` and an import's options. -1 for a call of a string, which the bundler
// resolves, for `()`, and when no `(` follows.
//
// A specifier built from a string, `"./" + id + ".js"` or a template with a substitution, is an expression too: the bundler bundles neither, nor any file it may name,
// and leaves it to resolve as the program runs from the executable's own file system, where there is no such file.
func (a *analysis) callOfExpression(i int) int {
	open := i + 1
	if !a.isPunct(open, "(") || open+1 >= len(a.toks) || a.isPunct(open+1, ")") {
		return -1
	}
	if a.toks[open+1].kind != tkString {
		return open
	}
	if a.isPunct(open+2, ")") || a.isPunct(open+2, ",") {
		return -1
	}
	return open
}

var methodModifiers = setOf("async get set static public private protected override readonly")

// isMethod is whether the `(` at open, after the word at i, begins a method's parameters rather than a call's arguments (`async import(url) {`, `require(id): Module {`):
// after a modifier or a `*`, or followed by a body (`{`) or, where a member begins, a return type (`:`). Undecided (a `(` without its `)`) counts as a method.
func (a *analysis) isMethod(i, open int) bool {
	if i > 0 {
		p := a.toks[i-1]
		if p.kind == tkWord && methodModifiers[p.text] || a.isPunct(i-1, "*") {
			return true
		}
	}
	closeIdx := a.match[open]
	if closeIdx < 0 {
		return true
	}
	member := i > 0 && (a.isPunct(i-1, "{") || a.isPunct(i-1, "}") || a.isPunct(i-1, ";") || a.isPunct(i-1, ","))
	return a.isPunct(closeIdx+1, "{") || a.isPunct(closeIdx+1, ":") && member
}

type namePosition uint8

const (
	posValue namePosition = iota
	// posShorthand is `{ __dirname }` in an object literal.
	posShorthand
	// posExported is a name an export statement mentions: `export { __dirname }`, `export { x as __dirname }`, `export { __dirname as d } from "./m"`, `export * as __dirname from "./m"`.
	posExported
)

// moduleListBrace is whether the bracket at i is the `{` of an export list or of an import's list (`import { a }`, `import type { a }`, `import d, { a }`).
func (a *analysis) moduleListBrace(i int) bool {
	if i < 0 || !a.isPunct(i, "{") {
		return false
	}
	if a.isWord(i-1, "export") || a.isWord(i-1, "type") && a.isWord(i-2, "export") {
		return true
	}
	for j, steps := i-1, 0; j >= 0 && steps < 8; j, steps = j-1, steps+1 {
		switch {
		case a.isWord(j, "import") && !a.toks[j].property:
			return true
		case a.toks[j].kind == tkWord || a.isPunct(j, ",") || a.isPunct(j, "*"):
		default:
			return false
		}
	}
	return false
}

// position is where the free name at word i stands in a module's export or import list or in an object literal, if it does.
func (a *analysis) position(i int) namePosition {
	if a.moduleListBrace(a.encl[i]) || a.isWord(i-1, "as") && a.isPunct(i-2, "*") {
		return posExported
	}
	inObject := a.isPunct(i-1, "{") || a.isPunct(i-1, ",")
	if !inObject {
		return posValue
	}
	brace := a.encl[i]
	if brace < 0 || !a.isPunct(brace, "{") || a.toks[brace].jsxBrace || !a.isPunct(i+1, ",") && !a.isPunct(i+1, "}") {
		return posValue
	}
	return posShorthand
}

type bodyKind uint8

const (
	bodyOther bodyKind = iota
	bodyClass
	bodyEnum
	bodyInterface
)

// bodyOf is what the `{` at brace opens when it follows the head of a class, an enum or an interface: the head is found by reading back, over parenthesized and angle-bracketed
// parts, to the keyword, and a `;`, `{`, `}`, `:`, `=` or `=>` on the way says there is none.
func (a *analysis) bodyOf(brace int) bodyKind {
	for j, steps := brace-1, 0; j >= 0 && steps < 256; j, steps = j-1, steps+1 {
		t := a.toks[j]
		switch {
		case t.kind == tkWord && !t.property:
			switch t.text {
			case "class":
				return bodyClass
			case "enum":
				return bodyEnum
			case "interface":
				return bodyInterface
			}
		case t.kind == tkPunct:
			switch t.text {
			case ")", "]":
				if a.match[j] < 0 {
					return bodyOther
				}
				j = a.match[j]
			case ">":
				depth := 0
			generic:
				for ; j >= 0; j-- {
					switch {
					case a.isPunct(j, ">"):
						depth++
					case a.isPunct(j, "<"):
						depth--
						if depth == 0 {
							break generic
						}
					case a.isPunct(j, ";") || a.isPunct(j, "{") || a.isPunct(j, "}"):
						return bodyOther
					}
				}
			case ";", "{", "}", ":", "=", "=>":
				return bodyOther
			}
		}
	}
	return bodyOther
}

var memberModifiers = setOf("public private protected readonly static abstract override declare accessor")

// isMemberName is whether the word at i names a member where a member begins: a field or a method of a class, a member of an enum or of an interface, a key of an object
// literal or of a type literal (`{ __dirname: string }`, `{ __dirname?: string }`). Such a name is no variable and no use of one. A member begins after `{`, `;`, `,` or `}`, and
// in a class or an interface on a new line after another member without a `;`.
func (a *analysis) isMemberName(i int) bool {
	k := i - 1
	for k >= 0 && a.toks[k].kind == tkWord && memberModifiers[a.toks[k].text] {
		k--
	}
	brace := a.encl[i]
	if brace < 0 || !a.isPunct(brace, "{") || a.toks[brace].jsxBrace {
		return false
	}
	body := a.bodyOf(brace)
	starts := a.isPunct(k, "{") || a.isPunct(k, ";") || a.isPunct(k, "}") || a.isPunct(k, ",")
	if !starts && (body == bodyClass || body == bodyInterface) && k >= 0 && k+1 < len(a.toks) {
		// Without a `;` a member ends at the end of its line: a value, a type or a decorator's call, then a new line.
		prev := a.toks[k]
		ends := prev.kind == tkWord || prev.kind == tkString || prev.kind == tkNumber || prev.kind == tkRegex || a.isPunct(k, "]") || a.isPunct(k, ")") || a.isPunct(k, ">")
		starts = ends && lineEnd(a.src, prev.end) < a.toks[k+1].start
	}
	if !starts {
		return false
	}
	switch body {
	case bodyClass, bodyEnum, bodyInterface:
		return true
	}
	next := i + 1
	return a.isPunct(next, ":") || (a.isPunct(next, "?") || a.isPunct(next, "!")) && a.isPunct(next+1, ":")
}

var declarationKeywords = setOf("const let var function class")

// declares is whether the module declares name itself, so that its own is left alone: a binding by const, let, var, function or class, an import, an assignment, a
// parameter (an arrow function's or a function's, or a constructor's parameter property) and a destructuring pattern. The decisions are made on tokens, so a comment or a
// string that spells a declaration is not one, and a use that merely looks like one (an argument, a condition, an object literal's shorthand property, a class field) is not.
func (a *analysis) declares(name string) bool {
	if a.declared == nil {
		a.declared = map[string]bool{}
		a.findDeclarations()
	}
	return a.declared[name]
}

var locationNames = setOf("__dirname __filename require")

func (a *analysis) findDeclarations() {
	toks := a.toks
	for i, t := range toks {
		if t.kind != tkWord {
			continue
		}
		if t.text == "import" && !t.property && !a.isPunct(i+1, "(") && !a.isPunct(i+1, ".") {
			a.importBindings(i)
			continue
		}
		if t.property || !locationNames[t.text] || a.declared[t.text] {
			continue
		}
		if a.declaresAt(i) {
			a.declared[t.text] = true
		}
	}
}

// importBindings marks each of the three names that an import statement binds: the name after `as` in `{ a as b }`, not the one before it.
func (a *analysis) importBindings(i int) {
	for j := i + 1; j < len(a.toks); j++ {
		t := a.toks[j]
		if t.kind == tkString || a.isPunct(j, ";") || a.isWord(j, "from") && j+1 < len(a.toks) && a.toks[j+1].kind == tkString {
			return
		}
		if t.kind == tkWord && !t.property && locationNames[t.text] && !a.isWord(j+1, "as") {
			a.declared[t.text] = true
		}
	}
}

var parameterModifiers = setOf("public private protected readonly override abstract")

// declaresAt is whether the word at i is where a binding of its name is made.
func (a *analysis) declaresAt(i int) bool {
	toks := a.toks
	if a.isMemberName(i) {
		return false
	}
	// `x: typeof __dirname = "a"`: the name is in a type, which no assignment or declaration of it can be.
	if a.isWord(i-1, "typeof") && !toks[i-1].property {
		return false
	}
	if i > 0 && toks[i-1].kind == tkWord && declarationKeywords[toks[i-1].text] {
		return true
	}
	// An assignment, `name = value`, but not `==`, `===` or `=>`.
	if a.isPunct(i+1, "=") && (!a.isPunct(i+2, "=") || toks[i+2].start != toks[i+1].end) {
		return true
	}
	// A parameter property, `constructor(private name: string)`, binds the name as a parameter.
	k := i - 1
	for k >= 0 && toks[k].kind == tkWord && parameterModifiers[toks[k].text] {
		k--
	}
	prevIsSeparator := a.isPunct(k, "(") || a.isPunct(k, ",")
	enclosing := a.encl[i]
	if enclosing < 0 {
		return false
	}
	switch {
	case a.isPunct(enclosing, "("):
		// A parameter: `(a, name)`, `(name = 1)`, `(name: string)`.
		next := i + 1
		return prevIsSeparator && (a.isPunct(next, ")") || a.isPunct(next, ",") || a.isPunct(next, "=") || a.isPunct(next, ":") || a.isPunct(next, "?")) && a.parameterList(enclosing)
	case a.isPunct(enclosing, "{") || a.isPunct(enclosing, "["):
		// An element of a destructuring pattern: `{ name }`, `{ a, name }`, `{ a: name }`, `[a, name]`.
		element := a.isPunct(i-1, "{") || a.isPunct(i-1, "[") || a.isPunct(i-1, ",") || a.isPunct(i-1, ":") && a.isPunct(enclosing, "{")
		next := i + 1
		return element && (a.isPunct(next, ",") || a.isPunct(next, "}") || a.isPunct(next, "]") || a.isPunct(next, "=")) && a.bindingPattern(enclosing)
	}
	return false
}

var conditionKeywords = setOf("if while for switch with")

// parameterList is whether the `(` at open begins a function's parameters: its `)` is followed by `=>` or a body, or by a return type of a function declaration or
// expression, and it does not belong to a condition.
func (a *analysis) parameterList(open int) bool {
	if open > 0 && a.toks[open-1].kind == tkWord && !a.toks[open-1].property && conditionKeywords[a.toks[open-1].text] {
		return false
	}
	closeIdx := a.match[open]
	if closeIdx < 0 {
		return false
	}
	switch {
	case a.isPunct(closeIdx+1, "=>"), a.isPunct(closeIdx+1, "{"):
		return true
	case a.isPunct(closeIdx+1, ":"):
		// `function f(a): T {` and `function (a): T {`: a typed return of a function, which a conditional expression's `:` is not.
		isFunction := a.isWord(open-1, "function") || a.isWord(open-2, "function") || a.isPunct(open-2, "*") && a.isWord(open-3, "function")
		if !isFunction {
			return false
		}
		for j := closeIdx + 2; j < len(a.toks); j++ {
			switch {
			case a.isPunct(j, "{"):
				return true
			case a.isPunct(j, ";") || a.isPunct(j, "="):
				return false
			}
		}
	}
	return false
}

// bindingPattern is whether the bracket at open begins a destructuring pattern rather than an object or array literal.
func (a *analysis) bindingPattern(open int) bool {
	if open > 0 && a.toks[open-1].kind == tkWord && (a.toks[open-1].text == "const" || a.toks[open-1].text == "let" || a.toks[open-1].text == "var") {
		return true
	}
	if closeIdx := a.match[open]; closeIdx >= 0 {
		if a.isPunct(closeIdx+1, "=") && (!a.isPunct(closeIdx+2, "=") || a.toks[closeIdx+2].start != a.toks[closeIdx+1].end) || a.isWord(closeIdx+1, "of") || a.isWord(closeIdx+1, "in") {
			return true
		}
	}
	parent := a.encl[open]
	if parent < 0 {
		return false
	}
	switch {
	case a.isPunct(parent, "("):
		return (a.isPunct(open-1, "(") || a.isPunct(open-1, ",")) && a.parameterList(parent)
	case a.isPunct(parent, "{") || a.isPunct(parent, "["):
		nested := a.isPunct(open-1, ",") || a.isPunct(open-1, "[") || a.isPunct(open-1, "{") || a.isPunct(open-1, ":") && a.isPunct(parent, "{")
		return nested && a.bindingPattern(parent)
	}
	return false
}

var fileAccessNames = setOf("readFileSync readFile readdirSync readdir existsSync statSync lstatSync accessSync createReadStream openSync opendirSync cpSync spawn spawnSync execFile execFileSync fork")

// readsFiles is whether the module reads the file system, or starts a process or a worker by path.
func (a *analysis) readsFiles() bool {
	for i, t := range a.toks {
		if t.kind != tkWord || !a.isPunct(i+1, "(") {
			continue
		}
		switch {
		case fileAccessNames[t.text]:
			return true
		case t.text == "Worker" && a.isWord(i-1, "new"):
			return true
		case t.text == "file" && t.property && a.isWord(i-2, "Bun"):
			return true
		}
	}
	return false
}
