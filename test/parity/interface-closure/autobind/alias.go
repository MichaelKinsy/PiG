package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	aliasStart = regexp.MustCompile(`(?m)^(export )?type (\w+)`)
	// declStart finds exported interfaces and classes, which shadow a same-named alias of another package.
	// constArrayStart finds `export const NAME = [` arrays whose element type an alias may read with (typeof NAME)[number].
	constArrayStart = regexp.MustCompile(`(?m)^export const (\w+)\s*=\s*\[`)
	typeofElemRe    = regexp.MustCompile(`^\(typeof (\w+)\)\[number\]$`)
	stringLitRe     = regexp.MustCompile(`^"[^"\\]*"$`)
	declStart       = regexp.MustCompile(`(?m)^export (?:declare )?(?:abstract )?(?:interface|class) (\w+)\b`)
	// interfaceStart finds interface declarations, exported or not, for the members of an interface the ledger does not list.
	interfaceStart = regexp.MustCompile(`(?m)^(?:export )?(?:declare )?interface (\w+)(?:<[^{]*?>)?(?:\s+extends\s+([^{]+?))?\s*\{`)
)

// aliasTable maps an upstream package directory to its type alias bodies. The key localPrefix+pkg holds the package's unexported
// aliases, which a member of an exported declaration may name (server ServerOperationErrorCode).
type aliasTable map[string]map[string]*string

const localPrefix = "local:"

// ifacePrefix keys the interfaces of a package, read as the intersection of their extended types and their member block
// (`interface B extends A { x: X }` reads `A & { x: X }`). Alias lookup never reads them.
const ifacePrefix = "iface:"

// satisfiesKey keys, per package, the declared type of each exported object constant written `{ ... } as const satisfies X`.
// Alias lookup never reads them.
const satisfiesKey = "satisfies:"

var (
	constObjectStart = regexp.MustCompile(`(?m)^export const (\w+)\s*=\s*\{\s*$`)
	satisfiesEnd     = regexp.MustCompile(`^\}\s*as const satisfies ([^;]+);\s*$`)
)

// readSatisfies records X for every `export const NAME = {` whose top-level closing line is `} as const satisfies X;`.
func readSatisfies(text string, out aliasTable, pkg string) {
	for _, m := range constObjectStart.FindAllStringSubmatchIndex(text, -1) {
		for line := range strings.SplitSeq(text[m[1]:], "\n") {
			if !strings.HasPrefix(line, "}") {
				continue
			}
			if s := satisfiesEnd.FindStringSubmatch(strings.TrimRight(line, "\r")); s != nil {
				if out[satisfiesKey+pkg] == nil {
					out[satisfiesKey+pkg] = map[string]*string{}
				}
				typ := strings.TrimSpace(s[1])
				out[satisfiesKey+pkg][text[m[2]:m[3]]] = &typ
			}
			break
		}
	}
}

// ctxImportKey holds, per package, whether the package's `Context` is the invocation Context of the chord package: a package that
// imports Context only from "@earendil-works/chord" (and chord itself). A package that also imports Context from pi-ai, as
// coding-agent does, is ambiguous per call and is left to the other rules.
const ctxImportKey = "ctximport:"

// brandKey holds the `declare const name: unique symbol` declarations of the upstream sources. A type member keyed by such a symbol
// (`readonly [transcriptContextBrand]: true`) is a compile-time brand: it exists only in the type system, so no Go field stands for it.
const brandKey = "brand:"

// importAsKey prefixes a package's `import { Name as Local }` renames: Local maps to Name, so a re-export `export type Name = Local` is Name itself.
const importAsKey = "importas:" //nolint:misspell // an internal map-key prefix naming the TypeScript `import { X as Y }` form, not prose

// extImportKey prefixes, per package, the names imported from a module outside the repository (`import type { Content } from "@google/genai"`):
// the name maps to its module, or to "" when the package imports it from more than one. Such a name is the module's type, not a
// same-named alias declared in the package, so a package alias never stands for it.
const extImportKey = "extimport:"

// externalModule reports whether an import specifier names a module outside the repository's packages and the Node builtins.
func externalModule(spec string) bool {
	return !strings.HasPrefix(spec, ".") && !strings.HasPrefix(spec, "@earendil-works/") && !strings.HasPrefix(spec, "node:")
}

var uniqueSymbolRe = regexp.MustCompile(`(?m)^\s*(?:export\s+)?declare\s+const\s+(\w+)\s*:\s*unique\s+symbol`)

// isBrand reports whether a member name is a computed key naming a unique-symbol brand.
func (t aliasTable) isBrand(name string) bool {
	inner, ok := strings.CutSuffix(strings.TrimPrefix(strings.TrimSpace(name), "["), "]")
	return ok && strings.HasPrefix(strings.TrimSpace(name), "[") && t[brandKey][strings.TrimSpace(inner)] != nil
}

var importRe = regexp.MustCompile(`import\s+(?:type\s+)?\{([^}]*)\}\s+from\s+"([^"]+)"`)
var contextWordRe = regexp.MustCompile(`\bContext\b`)

// invocationContext reports whether an upstream parameter type `Context` in package pkg is chord's invocation Context (S1c): it
// carries the abort signal and request-scoped values, which a Go call passes as its context.Context.
func (t aliasTable) invocationContext(pkg string) bool {
	pkg, _, _ = strings.Cut(pkg, "/") // durable/env is part of durable
	if pkg == "chord" {
		return true
	}
	m := t[ctxImportKey+pkg]
	return m != nil && m["chord"] != nil && m["other"] == nil
}

// paramsPrefix keys the type parameter names of a package's generic aliases, comma-separated in declaration order
// (`type R<T, U = X> = ...` records "T,U"). Alias lookup never reads them.
const paramsPrefix = "params:"

// wordRe matches an identifier in a type expression.
var wordRe = regexp.MustCompile(`[A-Za-z_$][\w$]*`)

// lookup returns the body of alias name declared in package pkg, the only declaration in the other packages, or else an unexported
// alias of pkg.
func (t aliasTable) lookup(pkg, name string) *string {
	_, b := t.lookupKey(pkg, name)
	return b
}

// lookupKey is lookup that also returns the table key the alias was found under.
func (t aliasTable) lookupKey(pkg, name string) (string, *string) {
	if b, ok := t[pkg][name]; ok {
		return pkg, b
	}
	for _, dep := range []string{"ai", "agent", "tui", "mcp", "codemode", "coding-agent"} {
		if dep != pkg {
			if b, ok := t[dep][name]; ok {
				return dep, b // the alias of the nearest package in dependency order
			}
		}
	}
	var found *string
	foundKey, n := "", 0
	for key, m := range t {
		if strings.HasPrefix(key, localPrefix) || strings.HasPrefix(key, ifacePrefix) || strings.HasPrefix(key, ctxImportKey) || strings.HasPrefix(key, brandKey) || strings.HasPrefix(key, paramsPrefix) || strings.HasPrefix(key, satisfiesKey) || strings.HasPrefix(key, importAsKey) || strings.HasPrefix(key, extImportKey) {
			continue
		}
		if b, ok := m[name]; ok {
			found, foundKey, n = b, key, n+1
		}
	}
	if n == 1 {
		return foundKey, found
	}
	if n == 0 {
		if t[extImportKey+pkg][name] != nil {
			return "", nil // the package imports this name from an external module: its unexported alias of the same name is another type
		}
		return localPrefix + pkg, t[localPrefix+pkg][name] // an unexported alias of the row's package, used only when no exported alias has the name
	}
	return "", nil
}

// tsAliases reads the bodies of `export type Name = ...;` declarations from the pinned upstream sources. A name declared more than
// once maps to nil: the rules do not guess between declarations.
func tsAliases(root string) aliasTable {
	out := aliasTable{}
	schemas := map[string]typeBoxSchemas{}
	consts := map[string]map[string]string{}
	shadows := map[string]bool{}
	base := filepath.Join(root, ".upstream/current/packages")
	_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		// examples hold sample extensions that redeclare names such as readSchema; they are not part of a package's declared types.
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == "test" || d.Name() == "dist" || d.Name() == "examples") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".d.ts") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		text := string(data)
		pkg, _, _ := strings.Cut(strings.TrimPrefix(filepath.ToSlash(path), filepath.ToSlash(base)+"/"), "/")
		for _, m := range uniqueSymbolRe.FindAllStringSubmatch(text, -1) {
			if out[brandKey] == nil {
				out[brandKey] = map[string]*string{}
			}
			kind := "brand"
			out[brandKey][m[1]] = &kind
		}
		for _, m := range importRe.FindAllStringSubmatch(text, -1) {
			for spec := range strings.SplitSeq(m[1], ",") {
				if name := strings.TrimPrefix(strings.TrimSpace(spec), "type "); externalModule(m[2]) && name != "" && !strings.Contains(name, " as ") {
					if out[extImportKey+pkg] == nil {
						out[extImportKey+pkg] = map[string]*string{}
					}
					module := m[2]
					if previous, seen := out[extImportKey+pkg][name]; seen && (previous == nil || *previous != module) {
						module = ""
					}
					out[extImportKey+pkg][name] = &module
				}
				if from, local, ok := strings.Cut(strings.TrimSpace(spec), " as "); ok {
					if out[importAsKey+pkg] == nil {
						out[importAsKey+pkg] = map[string]*string{}
					}
					// `import { type Name as Local }` renames the same way.
					name := strings.TrimPrefix(strings.TrimSpace(from), "type ")
					out[importAsKey+pkg][strings.TrimSpace(local)] = &name
				}
			}
			if contextWordRe.MatchString(m[1]) {
				kind := "other"
				if m[2] == "@earendil-works/chord" {
					kind = "chord"
				}
				if out[ctxImportKey+pkg] == nil {
					out[ctxImportKey+pkg] = map[string]*string{}
				}
				out[ctxImportKey+pkg][kind] = &kind
			}
		}
		if schemas[pkg] == nil {
			schemas[pkg] = typeBoxSchemas{}
		}
		if !strings.Contains(filepath.ToSlash(path), "/examples/") {
			// An example repeats the schema names of the source it overrides (coding-agent examples/extensions/tool-override.ts readSchema); it is no declaration of the package.
			readTypeBoxSchemas(text, schemas[pkg])
		}
		if consts[pkg] == nil {
			consts[pkg] = map[string]string{}
		}
		readConstArrays(text, consts[pkg])
		for _, m := range aliasStart.FindAllStringSubmatchIndex(text, -1) {
			name := text[m[4]:m[5]]
			start, ok := aliasBodyStart(text, m[1])
			if !ok {
				continue
			}
			body := aliasBody(text[start:])
			key := pkg
			if m[2] < 0 {
				key = localPrefix + pkg
			}
			if out[key] == nil {
				out[key] = map[string]*string{}
			}
			if _, dup := out[key][name]; dup {
				out[key][name] = nil
			} else {
				out[key][name] = &body
			}
			if params := typeParamNames(text[m[1]:start]); params != "" {
				if out[paramsPrefix+key] == nil {
					out[paramsPrefix+key] = map[string]*string{}
				}
				out[paramsPrefix+key][name] = &params
			}
		}
		readInterfaces(text, out, ifacePrefix+pkg)
		readAugmented(text, out)
		readSatisfies(text, out, pkg)
		for _, m := range declStart.FindAllStringSubmatch(text, -1) {
			if out[pkg] == nil {
				out[pkg] = map[string]*string{}
			}
			if _, ok := out[pkg][m[1]]; !ok {
				shadows[pkg+":"+m[1]] = true
			}
		}
		return nil
	})
	for key := range shadows {
		pkg, name, _ := strings.Cut(key, ":")
		if _, isAlias := out[pkg][name]; !isAlias {
			out[pkg][name] = nil // an interface or class of the row's own package is not another package's alias
		}
	}
	for key, aliases := range out {
		pkg := strings.TrimPrefix(key, localPrefix)
		constArrayElements(aliases, consts[pkg])
		staticTypes(aliases, schemas[pkg])
	}
	return out
}

// iface returns the interface name of package pkg read as an intersection, or else the only interface of that name in any package.
func (t aliasTable) iface(pkg, name string) *string {
	if b, ok := t[ifacePrefix+pkg][name]; ok {
		return b
	}
	var found *string
	n := 0
	for key, m := range t {
		if b, ok := m[name]; ok && strings.HasPrefix(key, ifacePrefix) {
			found, n = b, n+1
		}
	}
	if n == 1 {
		return found
	}
	return nil
}

// readInterfaces records each interface declaration of text under key as `Extended & { members }`; a name declared twice records nil.
func readInterfaces(text string, out aliasTable, key string) {
	for _, m := range interfaceStart.FindAllStringSubmatchIndex(text, -1) {
		open := m[1] - 1
		end := closingBrace(text[open:])
		if end < 0 {
			continue
		}
		body := text[open : open+end+1]
		if m[4] >= 0 {
			body = strings.Join(splitTop(text[m[4]:m[5]], ","), " & ") + " & " + body
		}
		if out[key] == nil {
			out[key] = map[string]*string{}
		}
		name := text[m[2]:m[3]]
		if _, dup := out[key][name]; dup {
			out[key][name] = nil
		} else {
			out[key][name] = &body
		}
	}
}

// augmentedKey keys the interfaces a `declare module` block augments. Declaration merging adds members to such an interface from
// another package (coding-agent core/keybindings.ts:69-71 extends pi-tui Keybindings), so its property set is open.
const augmentedKey = "augmented:"

var (
	declareModuleRe  = regexp.MustCompile(`declare\s+module\s+["'][^"']+["']\s*\{`)
	augmentedIfaceRe = regexp.MustCompile(`(?m)^\s*(?:export\s+)?interface\s+(\w+)`)
)

// readAugmented records under augmentedKey each interface declared inside a `declare module "..." { ... }` block of text.
func readAugmented(text string, out aliasTable) {
	for _, m := range declareModuleRe.FindAllStringIndex(text, -1) {
		end := closingBrace(text[m[1]-1:])
		if end < 0 {
			continue
		}
		for _, im := range augmentedIfaceRe.FindAllStringSubmatch(text[m[1]:m[1]-1+end], -1) {
			if out[augmentedKey] == nil {
				out[augmentedKey] = map[string]*string{}
			}
			out[augmentedKey][im[1]] = nil
		}
	}
}

// closingBrace returns the index of the brace that closes the one s starts with, skipping strings and comments, or -1.
func closingBrace(s string) int {
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return -1
			}
			i += end + 3
		case c == '/' && i+1 < len(s) && s[i+1] == '/':
			end := strings.IndexByte(s[i:], '\n')
			if end < 0 {
				return -1
			}
			i += end
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '{':
			depth++
		case c == '}':
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return -1
}

// readConstArrays records, for each `export const NAME = [...] as const` whose elements are all string literals, the union of
// those literals. A name declared twice records "" so that no alias reads either declaration.
func readConstArrays(text string, consts map[string]string) {
	for _, m := range constArrayStart.FindAllStringSubmatchIndex(text, -1) {
		name := text[m[2]:m[3]]
		if _, dup := consts[name]; dup {
			consts[name] = ""
			continue
		}
		consts[name] = ""
		end := strings.IndexByte(text[m[1]:], ']')
		if end < 0 || !strings.HasPrefix(strings.TrimSpace(text[m[1]+end+1:]), "as const") {
			continue
		}
		var lits []string
		for el := range strings.SplitSeq(text[m[1]:m[1]+end], ",") {
			el = strings.TrimSpace(el)
			if el == "" {
				continue
			}
			if !stringLitRe.MatchString(el) {
				el = stringConst(text, el) // `[LATEST_PROTOCOL_VERSION, "2025-06-18"] as const` (mcp protocol/types.ts:9)
			}
			if el == "" {
				lits = nil
				break
			}
			lits = append(lits, el)
		}
		if len(lits) > 0 {
			consts[name] = strings.Join(lits, " | ")
		}
	}
}

// stringConst returns the string literal of `const NAME = "literal"`, declared exactly once in text with no type annotation (an
// annotation such as `: string` widens the literal type), or "" for any other element.
func stringConst(text, name string) string {
	if !identRe.MatchString(name) || len(regexp.MustCompile(`\bconst `+regexp.QuoteMeta(name)+`\b`).FindAllStringIndex(text, -1)) != 1 {
		return ""
	}
	m := regexp.MustCompile(`(?m)^(?:export )?const ` + regexp.QuoteMeta(name) + `\s*=\s*("[^"\\]*")\s*(?:as const\s*)?;`).FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return m[1]
}

// constArrayElements rewrites an alias body `(typeof NAME)[number]` to the literal union recorded for NAME in the same package.
func constArrayElements(aliases map[string]*string, consts map[string]string) {
	for name, body := range aliases {
		if body == nil {
			continue
		}
		if m := typeofElemRe.FindStringSubmatch(*body); m != nil && consts[m[1]] != "" {
			u := consts[m[1]]
			aliases[name] = &u
		}
	}
}

// aliasBodyStart returns the offset after the `=` of a type alias whose name ends at i, skipping a type parameter list, which may
// hold defaults (`<T = JsonValue>`) and nested angle brackets.
func aliasBodyStart(text string, i int) (int, bool) {
	if i < len(text) && text[i] == '<' {
		depth := 0
		for ; i < len(text); i++ {
			switch text[i] {
			case '<':
				depth++
			case '>':
				if text[i-1] != '=' {
					depth--
				}
			}
			if depth == 0 {
				i++
				break
			}
		}
	}
	rest := strings.TrimLeft(text[i:], " \t\r\n")
	if !strings.HasPrefix(rest, "=") {
		return 0, false
	}
	return len(text) - len(rest) + 1, true
}

// aliasBody returns the text up to the semicolon that ends the declaration.
func aliasBody(s string) string {
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return cleanBody(s[:i])
			}
			i += end + 3 // a comment's apostrophes and brackets are not code
		case c == '/' && i+1 < len(s) && s[i+1] == '/':
			end := strings.IndexByte(s[i:], '\n')
			if end < 0 {
				return cleanBody(s[:i])
			}
			i += end
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '(' || c == '[' || c == '{' || c == '<':
			depth++
		case c == ')' || c == ']' || c == '}' || (c == '>' && (i == 0 || s[i-1] != '=')):
			depth--
		case c == ';' && depth <= 0:
			return cleanBody(s[:i])
		}
	}
	return cleanBody(s)
}

// cleanBody trims a leading union bar.
func cleanBody(s string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(stripTSComments(s)), "|"))
}

// typeParamNames returns the comma-separated names of a type parameter list `<T extends X, U = Y> =`, or "" when there is none.
func typeParamNames(decl string) string {
	decl = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(decl), "="))
	if !strings.HasPrefix(decl, "<") {
		return ""
	}
	var names []string
	for _, p := range splitTop(decl[1:len(decl)-1], ",") {
		f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(p), "const "))
		if len(f) == 0 {
			continue // `<T,>`: a trailing comma
		}
		names = append(names, f[0])
	}
	return strings.Join(names, ",")
}

// instantiate returns the body of the generic alias name with its type parameters replaced by args (A9), when lookup resolves the
// alias and args supplies every parameter (defaults are not applied).
func (t aliasTable) instantiate(pkg, name, args string) (string, bool) {
	key, body := t.lookupKey(pkg, name)
	params := t[paramsPrefix+key][name]
	if body == nil || params == nil {
		return "", false
	}
	names, vals := strings.Split(*params, ","), splitTop(args, ",")
	if len(names) != len(vals) {
		return "", false
	}
	sub := make(map[string]string, len(names))
	for i, n := range names {
		v := strings.TrimSpace(vals[i])
		if !identRe.MatchString(v) && !genericRe.MatchString(v) {
			v = "(" + v + ")" // a compound argument keeps its grouping; an identifier or a generic instance is already one operand
		}
		sub[n] = v
	}
	return wordRe.ReplaceAllStringFunc(*body, func(w string) string {
		if v, ok := sub[w]; ok {
			return v
		}
		return w
	}), true
}
