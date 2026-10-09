package main

import (
	"go/types"
	"reflect"
	"regexp"
	"strings"
)

// Alias-body rules for an upstream `type X = <body>` whose body is not a string-literal union or one named type:
//
//	A6 an object literal body is a Go struct with a field for every member (T12), members of type never excluded
//	A7s an intersection of an AbortSignal-only operand and U is the Go type for U when every consumer takes a context.Context
//	A7 an intersection is one Go struct that has a field for every member of every operand, an operand being an object literal or a
//	   named upstream interface or class whose properties the inventory records; an operand with no recorded properties (a mapped or
//	   conditional type, a Record) leaves the row undecided
//	A8 a union of named upstream types is a Go interface with methods that the Go type of every member implements

// stripTSComments removes block and line comments outside string literals.
func stripTSComments(s string) string {
	var b strings.Builder
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			b.WriteByte(c)
			switch c {
			case '\\':
				if i+1 < len(s) {
					i++
					b.WriteByte(s[i])
				}
			case quote:
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
			b.WriteByte(c)
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return strings.TrimSpace(b.String())
			}
			i += end + 3
			b.WriteByte(' ')
		case c == '/' && i+1 < len(s) && s[i+1] == '/':
			for i < len(s) && s[i] != '\n' {
				i++
			}
			b.WriteByte('\n')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// literalMembers lists the members of an object literal type as name, type and whether the member is optional. Members whose type
// is never are absent by construction and are skipped. ok is false for a member the rule cannot read (a method, an index signature).
func literalMembers(lit string) (out []param, ok bool) {
	inner := strings.TrimSpace(lit)
	if !strings.HasPrefix(inner, "{") || !strings.HasSuffix(inner, "}") {
		return nil, false
	}
	inner = strings.TrimSuffix(strings.TrimPrefix(inner, "{"), "}")
	for _, part := range splitTop(strings.ReplaceAll(inner, ";", ","), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, typ, found := strings.Cut(part, ":")
		if !found || strings.ContainsAny(name, "([") {
			return nil, false
		}
		name = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "readonly "))
		optional := strings.HasSuffix(name, "?")
		name = strings.TrimSuffix(name, "?")
		if !identRe.MatchString(name) {
			return nil, false
		}
		typ = strings.TrimSpace(typ)
		if typ == "never" {
			continue
		}
		out = append(out, param{Name: name, Type: typ, Optional: optional})
	}
	return out, len(out) > 0
}

// goFieldFor finds the field of a Go struct, own or promoted through an embedded struct, that stands for an upstream member: a json
// tag of that name or a name the name rules accept.
func goFieldFor(st *types.Struct, name string, depth int) *types.Var {
	for i := range st.NumFields() {
		f := st.Field(i)
		tag, _, _ := strings.Cut(reflect.StructTag(st.Tag(i)).Get("json"), ",")
		if tag == name || nameRule(name, f.Name()) != "" {
			return f
		}
	}
	if depth >= 3 {
		return nil
	}
	for f := range st.Fields() {
		if f.Embedded() {
			if inner, ok := deref(f.Type()).Underlying().(*types.Struct); ok {
				if g := goFieldFor(inner, name, depth+1); g != nil {
					return g
				}
			}
		}
	}
	return nil
}

// structuralAlias applies A6-A8. ok is false when the body is none of those shapes.
func (d *detector) structuralAlias(body string, tn *types.TypeName, chk *checker) (verdict, bool) {
	body = strings.TrimSpace(stripTSComments(body))
	body = strings.TrimSpace(strings.TrimPrefix(body, "|"))
	if parts := splitTop(body, "&"); len(parts) > 1 {
		return d.intersectionAlias(parts, tn, chk), true
	}
	if parts := splitTop(body, "|"); len(parts) > 1 {
		return d.namedUnionAlias(parts, tn, chk)
	}
	if strings.HasPrefix(body, "keyof ") || mapLiteral(body) != "" {
		return chk.agree(body, tn.Type()), true // U7 keyof T; a record literal is a Go map (T6)
	}
	if strings.HasPrefix(body, "{") && strings.HasSuffix(body, "}") {
		if _, ok := literalMembers(body); !ok {
			return verdict{}, false
		}
		if _, ok := tn.Type().Underlying().(*types.Struct); !ok {
			return noV("A6: upstream alias is an object literal, Go type is %s", typeLabel(tn.Type())), true
		}
		return chk.agree(body, tn.Type()), true
	}
	return verdict{}, false
}

// brandRe matches the one member of a unique-symbol brand object: { readonly [xBrand]: ... }.
var brandRe = regexp.MustCompile(`^\{\s*readonly\s*\[\w+\]\s*:`)

// brandedPrimitive reports the primitive of a TypeScript nominal brand, P & { readonly [brand]: ... }. A unique-symbol brand is erased at
// run time, so the alias is the primitive.
func brandedPrimitive(parts []string) (string, bool) {
	if len(parts) != 2 {
		return "", false
	}
	prim, obj := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	if prim != "number" && prim != "string" {
		return "", false
	}
	if !brandRe.MatchString(obj) || !strings.HasSuffix(obj, "}") {
		return "", false
	}
	if len(splitTop(strings.TrimSuffix(strings.TrimPrefix(obj, "{"), "}"), ";")) > 1 {
		if rest := splitTop(strings.TrimSuffix(strings.TrimPrefix(obj, "{"), "}"), ";"); strings.TrimSpace(rest[len(rest)-1]) != "" {
			return "", false
		}
	}
	return prim, true
}

func (d *detector) intersectionAlias(parts []string, tn *types.TypeName, chk *checker) verdict {
	if prim, ok := brandedPrimitive(parts); ok {
		// A8b: a nominal brand is erased; the Go type is the primitive, or a constraint whose type set is that primitive's types.
		if it, ok := tn.Type().Underlying().(*types.Interface); ok && !it.IsMethodSet() {
			return chk.agree(prim, firstTerm(it))
		}
		return chk.agree(prim, tn.Type())
	}
	st, ok := tn.Type().Underlying().(*types.Struct)
	if !ok {
		if rest, ok := d.signalOperand(parts, chk); ok && (chk.ctxInScope || chk.ctxConsumers != nil && chk.ctxConsumers(tn.Type())) {
			// A7s: `{ signal?: AbortSignal } & U` travels as the context.Context argument of every consumer next to the Go type for U.
			return chk.agree(rest, tn.Type())
		}
		return noV("A7: upstream alias is an intersection, Go type is %s", typeLabel(tn.Type()))
	}
	var members []param
	for _, p := range parts {
		if omitOperandRe.MatchString(strings.TrimSpace(p)) {
			// The general rules judge an Omit operand first, member types and inherited members included; A7o lists its members only
			// where they leave the intersection undecided.
			if v := chk.agree(strings.Join(parts, " & "), tn.Type()); v.ok != unknown {
				return v
			}
		}
		ms, why := d.operandMembers(p, chk, 0)
		if why != "" {
			if openRecord(p) {
				return unknownV("A7: %s", why)
			}
			// A7d: the general rules read the operands A7 cannot list (source-only interfaces, Omit, conditionals, unions of
			// object literals) and judge member types, not only names.
			if v := chk.agree(strings.Join(parts, " & "), tn.Type()); v.ok != unknown {
				return v
			}
			return unknownV("A7: %s", why)
		}
		members = append(members, ms...)
	}
	for _, m := range members {
		if isSignalType(m.Type) && goFieldFor(st, m.Name, 0) == nil {
			if chk.ctxInScope || chk.ctxConsumers != nil && chk.ctxConsumers(tn.Type()) {
				continue // M4/T12s: a member of type AbortSignal is the context.Context argument of every consumer, not a field
			}
			return unknownV("T12s: AbortSignal member %s outside a call that takes a context.Context", m.Name)
		}
		if goFieldFor(st, m.Name, 0) == nil {
			return noV("A7: Go struct %s has no field for intersection member %s", typeLabel(tn.Type()), m.Name)
		}
	}
	return yesV()
}

var (
	pickOperandRe = regexp.MustCompile(`^Pick<\s*([A-Za-z_]\w*)\s*,\s*(.+)>$`)
	omitOperandRe = regexp.MustCompile(`^Omit<\s*(.+?)\s*,\s*((?:"[^"]*"|'[^']*')(?:\s*\|\s*(?:"[^"]*"|'[^']*'))*)\s*>$`)
	genericHeadRe = regexp.MustCompile(`^([A-Za-z_]\w*)\s*<.*>$`)
)

// operandMembers lists the members of one operand of an intersection: an object literal, Pick<T, "k" | ...> of a named interface, a
// named interface or class with recorded properties, or an alias of those (generic arguments are dropped: the members of Box<T> are the
// members of Box). why is non-empty when the operand is any other shape (a mapped or conditional type, Omit, Record, a union).
func (d *detector) operandMembers(p string, chk *checker, depth int) (out []param, why string) {
	p = strings.TrimSpace(p)
	for strings.HasPrefix(p, "(") && strings.HasSuffix(p, ")") && matchingParen(p) {
		p = strings.TrimSpace(p[1 : len(p)-1])
	}
	if depth > 6 {
		return nil, "intersection operands nest too deeply"
	}
	switch {
	case strings.HasPrefix(p, "{"):
		ms, ok := literalMembers(p)
		if !ok {
			return nil, "intersection operand " + `"` + truncate(p, 50) + `"` + " is not a plain object literal"
		}
		return ms, ""
	case pickOperandRe.MatchString(p):
		m := pickOperandRe.FindStringSubmatch(p)
		have := map[string]bool{}
		for _, n := range chk.propNames(m[1]) {
			have[n] = true
		}
		for key := range strings.SplitSeq(m[2], "|") {
			key = strings.Trim(strings.TrimSpace(key), `"'`)
			if key == "" || !have[key] {
				return nil, "Pick operand " + `"` + truncate(p, 50) + `"` + " names a member its type does not record"
			}
			out = append(out, param{Name: key, Type: d.propType(m[1], key)})
		}
		return out, ""
	}
	if branches, ok := conditionalBranches(p); ok {
		// A7k: a conditional type `[D] extends [never] ? { data?: never } : { data: D }` lists the members of its second branch; which branch
		// applies is the type argument's, and the Go field carries the type parameter.
		second, why := d.operandMembers(branches[1], chk, depth+1)
		if why != "" {
			return nil, why
		}
		// The branch for a type argument that carries no data lists its members as `never`: they are absent there and present in the
		// other branch, which names every member the Go type must hold. Any other first branch is not listed.
		if !neverOnlyLiteral(branches[0]) {
			return nil, "conditional operand " + `"` + truncate(p, 50) + `"` + " has a first branch that is not a literal of never members"
		}
		return second, ""
	}
	if m := omitOperandRe.FindStringSubmatch(p); m != nil {
		// A7o: Omit<T, "k" | ...> lists the members of T (an interface, an alias, or another Omit) except the omitted keys.
		inner, why := d.operandMembers(m[1], chk, depth+1)
		if why != "" {
			return nil, why
		}
		omitted := map[string]bool{}
		for key := range strings.SplitSeq(m[2], "|") {
			omitted[strings.Trim(strings.TrimSpace(key), `"'`)] = true
		}
		for _, member := range inner {
			if !omitted[member.Name] {
				out = append(out, member)
			}
		}
		return out, ""
	}
	name := p
	if g := genericHeadRe.FindStringSubmatch(p); g != nil {
		name = g[1]
	}
	if !identRe.MatchString(name) {
		return nil, "intersection operand " + `"` + truncate(p, 50) + `"` + " has no recorded properties"
	}
	if chk.propNames != nil {
		if names := chk.propNames(name); len(names) > 0 {
			for _, n := range names {
				out = append(out, param{Name: n, Type: d.propType(name, n)})
			}
			return out, ""
		}
	}
	if body := chk.alias(name); body != nil {
		inner := strings.TrimSpace(*body)
		if ps := splitTop(inner, "&"); len(ps) > 1 {
			for _, q := range ps {
				ms, why := d.operandMembers(q, chk, depth+1)
				if why != "" {
					return nil, why
				}
				out = append(out, ms...)
			}
			return out, ""
		}
		if strings.HasPrefix(inner, "{") && len(splitTop(inner, "|")) == 1 {
			return d.operandMembers(inner, chk, depth+1)
		}
	}
	return nil, "intersection operand " + `"` + truncate(p, 50) + `"` + " has no recorded properties"
}

// propType returns the recorded type of property prop of the named upstream interface or class.
func (d *detector) propType(owner, prop string) string {
	for _, e := range d.l.entries {
		if e.Name == owner && e.Role == "" && (e.Kind == "interface" || e.Kind == "class") {
			for _, p := range d.l.properties(e.ID) {
				if p.Shape.Name == prop {
					return p.Shape.Type
				}
			}
		}
	}
	return ""
}

func (d *detector) namedUnionAlias(parts []string, tn *types.TypeName, chk *checker) (verdict, bool) {
	var members []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if !identRe.MatchString(p) || chk.resolve == nil {
			return verdict{}, false
		}
		members = append(members, p)
	}
	it, ok := tn.Type().Underlying().(*types.Interface)
	if !ok || it.NumMethods() == 0 {
		if v := chk.agree(strings.Join(members, " | "), tn.Type()); v.ok == yes {
			// A8s: the union rules judge a Go type that is not an interface with methods: one struct holding every member's properties
			// (T10d, T10o), or T | null as *T (S6) when the members are keyword types.
			return v, true
		}
		return noV("A8: upstream alias is a union of named types, Go type %s is not an interface with methods", typeLabel(tn.Type())), true
	}
	for _, m := range members {
		mt := chk.resolve(m)
		if mt == nil && tn.Pkg() != nil {
			// A8n: a member Pi does not export publicly has no ledger row; its Go type is the same name in the Go package of the union.
			mt, _ = tn.Pkg().Scope().Lookup(m).(*types.TypeName)
		}
		if mt == nil {
			return unknownV("A8: union member %s has no Go type", m), true
		}
		if !types.Implements(mt.Type(), it) && !types.Implements(types.NewPointer(mt.Type()), it) {
			return noV("A8: Go type %s of union member %s does not implement %s", typeLabel(mt.Type()), m, typeLabel(tn.Type())), true
		}
	}
	return yesV(), true
}

// firstTerm returns the first type of an interface's type set, or the interface itself when it has none.
func firstTerm(it *types.Interface) types.Type {
	if it.NumEmbeddeds() == 0 {
		return it
	}
	first := it.EmbeddedType(0)
	if u, ok := first.(*types.Union); ok && u.Len() > 0 {
		return u.Term(0).Type()
	}
	return first
}

// signalOperand finds the one intersection operand whose members are all AbortSignal values and returns the other operands as one
// intersection. It reports false when no operand or more than one is such a signal operand, or nothing else remains.
func (d *detector) signalOperand(parts []string, chk *checker) (string, bool) {
	var rest []string
	signals := 0
	for _, p := range parts {
		ms, why := d.operandMembers(p, chk, 0)
		signal := why == "" && len(ms) > 0
		for _, m := range ms {
			signal = signal && isSignalType(m.Type)
		}
		if signal {
			signals++
			continue
		}
		rest = append(rest, p)
	}
	if signals != 1 || len(rest) == 0 {
		return "", false
	}
	return strings.Join(rest, " & "), true
}

// overloadSetAlias applies A10: `UnionToIntersection<{ [Name in Names]: Starter<Name> }[Names]>` is a TypeScript overload set that
// types one call per span name; the function behind it takes the name as a value (Pi telemetry bindTypedSpanStarter's startSpan(name,
// attributes, callback)), so the Go form is a function type whose first parameter is a string and whose last is the callback.
func overloadSetAlias(body string, t types.Type) (verdict, bool) {
	if !strings.HasPrefix(strings.TrimSpace(body), "UnionToIntersection<") {
		return verdict{}, false
	}
	sig, ok := t.Underlying().(*types.Signature)
	if !ok {
		return noV("A10: upstream alias is an overload set, Go type is %s", typeLabel(t)), true
	}
	params := sig.Params()
	if params.Len() < 3 || basicInfo(params.At(0).Type())&types.IsString == 0 {
		return noV("A10: the Go function takes no leading name string and a callback"), true
	}
	if _, ok := params.At(params.Len() - 1).Type().Underlying().(*types.Signature); !ok {
		return noV("A10: the last parameter of the Go function is not the callback"), true
	}
	return yesV(), true
}

// conditionalBranches splits `T extends U ? A : B` at its top-level `?` and `:` into A and B.
func conditionalBranches(p string) ([2]string, bool) {
	if !strings.Contains(p, " extends ") {
		return [2]string{}, false
	}
	parts := splitTop(p, "?")
	if len(parts) != 2 {
		return [2]string{}, false
	}
	branches := splitTop(parts[1], ":")
	if len(branches) != 2 {
		return [2]string{}, false
	}
	return [2]string{strings.TrimSpace(branches[0]), strings.TrimSpace(branches[1])}, true
}

// neverOnlyLiteral reports whether lit is an object literal whose every member has type never (`{ readonly data?: never }`).
func neverOnlyLiteral(lit string) bool {
	inner := strings.TrimSpace(lit)
	if !strings.HasPrefix(inner, "{") || !strings.HasSuffix(inner, "}") {
		return false
	}
	seen := false
	for _, part := range splitTop(strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(inner, "{"), "}"), ";", ","), ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		_, typ, found := strings.Cut(part, ":")
		if !found || strings.TrimSpace(typ) != "never" {
			return false
		}
		seen = true
	}
	return seen
}
