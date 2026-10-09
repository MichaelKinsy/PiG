package main

import (
	"encoding/json"
	"fmt"
	"go/token"
	"go/types"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/test/parity/interface-closure/autobind/rules"
)

// tri is the outcome of a rule: the rules either establish a property, refute it, or cannot decide. Cannot decide is a gap.
type tri int

const (
	yes tri = iota
	no
	unknown
)

type verdict struct {
	ok  tri
	why string
}

func yesV() verdict { return verdict{ok: yes} }

func noV(format string, args ...any) verdict { return verdict{no, fmt.Sprintf(format, args...)} }

func unknownV(format string, args ...any) verdict {
	return verdict{unknown, fmt.Sprintf(format, args...)}
}

// all combines verdicts: a refutation wins over an undecided rule, which wins over success.
func all(vs ...verdict) verdict {
	out := yesV()
	for _, v := range vs {
		switch {
		case v.ok == no:
			return v
		case v.ok == unknown && out.ok == yes:
			out = v
		}
	}
	return out
}

// checker applies the shape rules. renames maps an upstream type name to the Go type that documents it; generics are the type
// parameter names in scope for the row being judged.
// maxInstDepth bounds nested generic alias instantiation (A9), so a recursive alias stays undecided instead of expanding forever.
const maxInstDepth = 4

type checker struct {
	// instDepth counts the generic alias instances (A9) being expanded.
	instDepth int
	renames   map[string]string
	generics  map[string]bool
	aliases   aliasTable // upstream type alias bodies
	// plainContext reads an upstream chord Context parameter as an ordinary parameter (S1c fallback).
	plainContext bool
	pkg          string // upstream package of the row
	bags         func(name string) bool
	// resolve returns the Go type that an upstream interface, class or alias stands for under the name, rename and shape rules.
	resolve func(name string) *types.TypeName
	// propNames returns the property names of a named upstream interface or class.
	propNames func(name string) []string
	// reps are the documented Go representations (a type spelled as the Go compiler prints it) of upstream names, keyed pkg:Name.
	reps map[string]string
	// errUnionPinned is set when the Go declaration of the call pins the three outcomes of a boolean | string result as its error (S5s).
	errUnionPinned bool
	// forms are the reviewed Go types that stand for an upstream type besides its own Go form, keyed pkg:Name (T9t).
	forms map[string][]string
	// narrow reports whether a Go type is the verified consumer-owned narrow interface for an upstream class (T9n).
	narrow func(pkg, up string, t types.Type) bool
	// partial is set inside Partial<X>: X's members are optional, so their Go fields must be nilable (T9t).
	partial bool
	// props returns the properties of a named upstream interface or class.
	props func(name string) []propShape
	// ctxInScope is set while judging the arguments of a Go call that takes a context.Context.
	ctxInScope bool
	// typeParams returns the type parameter names of an upstream interface or class (T9g).
	typeParams func(name string) []string
	// ctxConsumers reports whether every Go call that takes a value of the type also takes a context.Context (T12s).
	ctxConsumers func(t types.Type) bool
	// omit and pick are the member names an enclosing Omit or Pick removes or keeps; the first object type judged applies them and
	// does not pass them to its member types.
	omit, pick map[string]bool
	// discriminates reports whether a Go type writes member key with the string literal lit: its JSON discriminator (M5) or a
	// constant pair in its encoder.
	discriminates func(tn *types.TypeName, key, lit string) bool
	depth         int
	// recv is the Go receiver of the accessor being judged; T3s reads a settled value from a sibling method.
	recv types.Type
}

func (c *checker) alias(name string) *string { return c.aliases.lookup(c.pkg, name) }

func (c *checker) sameName(up, goName string) bool {
	if nameRule(up, goName) != "" {
		return true
	}
	if original := c.aliases[importAsKey+c.pkg][up]; original != nil && nameRule(*original, goName) != "" {
		return true // `import { Name as Local }`: Local is the imported Name
	}
	return c.renames[up] != "" && c.renames[up] == goName
}

// agree applies the type rules T1-T12 to an upstream type and a Go type.
func (c *checker) agree(up string, t types.Type) verdict {
	return c.registeredType(up, t, c.agreeBuiltin(up, t))
}

func (c *checker) agreeBuiltin(up string, t types.Type) verdict {
	up = unparen(up) // `(C extends D ? X : Y)` as an intersection operand
	if c.narrow != nil && c.narrow(c.pkg, up, t) {
		return yesV() // T9n
	}
	if strings.TrimSpace(up) == "never" && emptyStruct(t) {
		return yesV() // T9v: `never` has no value; Go's zero-size struct{} is the element of a map that records keys only, as Record<string, never> holds no entries
	}
	if lits, ok := c.literalUtility(up); ok {
		up = lits // T18
	}
	if v, ok := literalTuple(up, t); ok {
		return v
	}
	if m := keyofRe.FindStringSubmatch(up); m != nil {
		if _, open := c.aliases[augmentedKey][m[1]]; open {
			if basicInfo(t)&types.IsString != 0 {
				return yesV() // U7o: keyof an interface other packages augment (`declare module`) is an open set of strings
			}
			return noV("U7o: keyof %s is an open set of strings, Go type is %s", m[1], typeLabel(t))
		}
	}
	if quotedStringLiteral(up) {
		if basicInfo(t)&types.IsString != 0 {
			return yesV() // T1q: one string literal type (a const's inferred type, which may hold quotes and newlines) is a Go string
		}
		return noV("T1q: upstream is a string literal, Go type is %s", typeLabel(t))
	}
	if isFuncType(up) {
		return c.single(up, t) // a function type's return union belongs to the function
	}
	if check, extends, yes, no, ok := splitConditional(up); ok {
		return c.conditional(check, extends, yes, no, t)
	}
	if rest, ok := syncOrAsync(up); ok {
		return c.agree(rest, t) // T3u: a value returned now or through a Promise is one blocking Go result
	}
	if nullableBoolean(up) && triStateBool(t) {
		return yesV() // T3n: `boolean | null` has three values; a Go enum of exactly False, True and Null holds each of them
	}
	if rest, ok := nullableUnion(up); ok {
		if arg := changeWrapperArg(t); arg != nil {
			return c.agree(rest, arg) // T3c: `T | null`, optional, has three states; Go's Change[T] with SetTo and Cleared holds each of them
		}
	}
	var kept []string
	for _, m := range splitTop(up, "|") {
		switch m = strings.TrimSpace(m); m {
		case "undefined", "null", "void", "":
		default:
			if !contains(kept, m) {
				kept = append(kept, m) // `Promise<T | undefined> | T | undefined` names T twice
			}
		}
	}
	if len(kept) == 0 {
		return yesV() // T0: undefined, null or void carry no type
	}
	t = deref(t)
	if isRawJSON(t) && c.allJSONish(kept) {
		return yesV() // T13: json.RawMessage is the Go form of an unparsed JSON value
	}
	if len(kept) > 1 {
		if c.allStringLike(kept, 0) {
			return c.stringKind(up, t) // T1
		}
		if c.allJSONish(kept) && isEmptyInterface(t) {
			return yesV() // T10c: a union of JSON shapes is the empty interface
		}
		if isEmptyInterface(t) && !slices.ContainsFunc(kept, func(m string) bool {
			return m != "string" && m != "number" && m != "boolean" && m != "bigint" && !byteArrayType(m)
		}) {
			return yesV() // T10p: Go has no sum of primitive types; a union of primitives and byte buffers is the empty interface
		}
		return c.union(up, kept, t)
	}
	if c.allStringLike(kept, 0) {
		if c.alias(kept[0]) != nil {
			return c.stringKind(up, t) // T1: an alias of string-like members
		}
	}
	if parts := splitTop(kept[0], "&"); len(parts) > 1 {
		// T15: an intersection is one Go type that satisfies every operand, as a struct that embeds or spreads the operands' members.
		vs := make([]verdict, 0, len(parts))
		for _, p := range parts {
			if phantomBrand(p) {
				continue // T15b: an object of computed keys only is a compile-time brand; Go brands by a named type
			}
			if signalOnly(p) && c.ctxConsumers != nil && c.ctxConsumers(t) {
				continue // T15s: an operand of AbortSignal members only is the context.Context every consumer of the Go type takes
			}
			if openRecord(p) {
				if _, ok := deref(t).Underlying().(*types.Struct); ok {
					continue // T15r: an open record operand is the extension slot of an options struct (LEAD-ANSWERS-ledger #2)
				}
			}
			v := c.agree(p, t)
			if v.ok != yes {
				if w, ok := c.inherited(strings.TrimSpace(p), t); ok && (w.ok == yes || v.ok == unknown) {
					v = w
				}
			}
			vs = append(vs, v)
		}
		return all(vs...)
	}
	return c.single(kept[0], t)
}

// splitConditional splits a conditional type `Check extends Extends ? Yes : No` at its top level.
func splitConditional(up string) (check, extends, yes, no string, ok bool) {
	depth, ext, q, branches := 0, -1, -1, 0
	for i := 0; i < len(up); i++ {
		switch c := up[i]; {
		case c == '>' && i > 0 && up[i-1] == '=':
		case strings.IndexByte("([{<", c) >= 0:
			depth++
		case strings.IndexByte(")]}>", c) >= 0:
			depth--
		case depth != 0:
		case ext < 0 && strings.HasPrefix(up[i:], " extends "):
			ext = i
		case c == '?' && ext >= 0:
			if q < 0 {
				q = i
			} else {
				branches++
			}
		case c == ':' && q >= 0:
			if branches == 0 {
				return strings.TrimSpace(up[:ext]), strings.TrimSpace(up[ext+len(" extends ") : q]), strings.TrimSpace(up[q+1 : i]),
					strings.TrimSpace(up[i+1:]), true
			}
			branches--
		}
	}
	return "", "", "", "", false
}

// mappedTypeRe matches the start of a mapped type's body, `[K in Keys]` with an optional readonly or -readonly modifier.
var mappedTypeRe = regexp.MustCompile(`^\s*[+-]?(?:readonly\s+)?\[\s*\w+\s+in\s+(?:keyof\s+(\w+)\b)?`)

var inferRe = regexp.MustCompile(`^infer\s+([A-Za-z_]\w*)$`)

// conditional applies T20: a conditional type is a Go type that holds both branches, since the Go type does not narrow by type
// argument; a never branch holds nothing. `X extends infer R ? Yes : No` is Yes with R bound to X.
func (c *checker) conditional(check, extends, yes, no string, t types.Type) verdict {
	if m := inferRe.FindStringSubmatch(extends); m != nil {
		bound := regexp.MustCompile(`\b`+m[1]+`\b`).ReplaceAllLiteralString(yes, check)
		return c.agree(bound, t)
	}
	inner := *c
	inner.depth++
	var vs []verdict
	for _, branch := range []string{yes, no} {
		if branch != "never" {
			vs = append(vs, inner.agree(branch, t))
		}
	}
	if len(vs) == 0 {
		return unknownV("T20: both branches of %s are never", truncate(check+" extends "+extends, 40))
	}
	return all(vs...)
}

// reduceSubtypes drops each union member that is a subtype of another member: an intersection (inline or through an alias) that has
// the other member as an operand, or an interface with every property of the other member, of the same type and optionality.
func (c *checker) reduceSubtypes(members []string) []string {
	var out []string
	for i, sub := range members {
		dropped := false
		for j, sup := range members {
			if sub == sup || !c.subtypeOf(strings.TrimSpace(sub), strings.TrimSpace(sup)) {
				continue
			}
			// Two members that are subtypes of each other name one shape: the first one stays.
			if j > i && c.subtypeOf(strings.TrimSpace(sup), strings.TrimSpace(sub)) {
				continue
			}
			dropped = true
			break
		}
		if !dropped {
			out = append(out, sub)
		}
	}
	return out
}

func (c *checker) subtypeOf(sub, sup string) bool {
	text := sub
	if body := c.alias(sub); body != nil {
		text = stripTypeNoise(*body)
	}
	if parts := splitTop(text, "&"); len(parts) > 1 {
		return slices.ContainsFunc(parts, func(p string) bool { return strings.TrimSpace(p) == sup })
	}
	if c.props == nil {
		return false
	}
	supProps, subProps := c.props(sup), c.props(sub)
	return len(supProps) > 0 && !slices.ContainsFunc(supProps, func(p propShape) bool {
		return !slices.ContainsFunc(subProps, func(q propShape) bool {
			return q.Name == p.Name && q.Type == p.Type && q.Optional == p.Optional
		})
	})
}

// oneOfStruct reports whether st is a one-of struct for the union (T10o): every exported field is nilable and every member, through
// aliases of unions, agrees with the type of a field. A member X[keyof X] of an interface X
// that declares no members is the slot apps fill by declaration merging (agent CustomAgentMessages); a map field with string keys
// carries it.
func (c *checker) oneOfStruct(members []string, st *types.Struct) bool {
	var fields []types.Type
	for f := range st.Fields() {
		if !f.Exported() {
			continue
		}
		if !nilable(f.Type()) {
			return false
		}
		fields = append(fields, f.Type())
	}
	inner := *c
	inner.depth++
	var holds func(m string, depth int) bool
	holds = func(m string, depth int) bool {
		m = strings.TrimSpace(m)
		if x := indexedRe.FindStringSubmatch(m); x != nil && strings.TrimSpace(x[2]) == "keyof "+x[1] && c.mergeSlot(x[1]) {
			return slices.ContainsFunc(fields, func(ft types.Type) bool {
				mp, ok := ft.Underlying().(*types.Map)
				return ok && basicInfo(mp.Key())&types.IsString != 0
			})
		}
		if body := c.alias(m); body != nil && depth < 2 {
			if parts := splitTop(stripTypeNoise(*body), "|"); len(parts) > 1 {
				for _, p := range parts {
					if !holds(p, depth+1) {
						return false
					}
				}
				return true
			}
		}
		return slices.ContainsFunc(fields, func(ft types.Type) bool { return inner.agree(m, ft).ok == yes })
	}
	for _, m := range members {
		if !holds(m, 0) {
			return false
		}
	}
	return true
}

// allNameKeys reports whether every member of a union is a name an upstream API accepts as a key: string, symbol, a string literal,
// keyof X, or a type parameter that stands for one. Go has no symbols, so its name type is a string type.
func (c *checker) allNameKeys(members []string) bool {
	for _, m := range members {
		m = strings.TrimSpace(m)
		switch {
		case m == "string" || m == "symbol" || stringLit.MatchString(m) || strings.HasPrefix(m, "keyof "):
		case c.generics[m]:
		default:
			return false
		}
	}
	return len(members) > 0
}

// mergeSlot reports whether name is an upstream interface the pinned sources declare with no members.
func (c *checker) mergeSlot(name string) bool {
	body := c.aliases.iface(c.pkg, name)
	return body != nil && strings.Join(strings.Fields(stripTypeNoise(*body)), "") == "{}"
}

// allTuples reports whether every member is a tuple type (`readonly [...]`) or an alias of a union of them.
func (c *checker) allTuples(members []string, depth int) bool {
	for _, m := range members {
		m = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(m), "readonly "))
		switch body := c.alias(m); {
		case m == "": // the bar before a union's first member
		case strings.HasPrefix(m, "[") && strings.HasSuffix(m, "]"):
		case body != nil && depth < 2:
			if !c.allTuples(splitTop(stripTypeNoise(*body), "|"), depth+1) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// wrapperArm applies T10x to a two-member union `X | W` (Pi tui stack.ts:17 `StackChild = Component | StackEntry`, StackEntry being
// { component: Component } plus optional layout members): when W's only required member has type X, a bare X is a W whose optional
// members take their defaults, so the Go form of W stands for the union. It reports false when no member is such a wrapper of the
// other or the Go type does not agree with the wrapper.
func (c *checker) wrapperArm(members []string, t types.Type) (verdict, bool) {
	if len(members) != 2 || c.props == nil {
		return verdict{}, false
	}
	for i := range 2 {
		arm, wrapper := strings.TrimSpace(members[i]), strings.TrimSpace(members[1-i])
		required, holds := 0, false
		for _, p := range c.props(wrapper) {
			if p.Optional {
				continue
			}
			required++
			holds = holds || strings.TrimSpace(p.Type) == arm
		}
		if required == 1 && holds {
			if v := c.agree(wrapper, t); v.ok == yes {
				return v, true
			}
		}
	}
	return verdict{}, false
}

// union applies T10: a closed union of upstream types is a Go interface with methods (a sealed sum). An empty interface, or any other
// Go type, does not say which members it holds, so the rule cannot decide.
func (c *checker) union(up string, members []string, t types.Type) verdict {
	if basicInfo(t)&types.IsString != 0 && c.allNameKeys(members) {
		return yesV() // T10n: an open name (string | symbol, event-name literals, keyof X, a type parameter) is a Go string type
	}
	for _, m := range members {
		if stringLit.MatchString(m) || numberLit.MatchString(m) {
			if c.literalFlags(members, t) {
				return yesV() // T10f
			}
			if isEmptyInterface(t) {
				return yesV() // T10m: Go has no sum of literals and types; the empty interface holds each member
			}
			return unknownV("T10: union %q mixes literals and types", up)
		}
	}
	if sl, ok := t.Underlying().(*types.Slice); ok && isEmptyInterface(sl.Elem()) && c.allTuples(members, 0) {
		return yesV() // T5u: Go has no sum of tuple types; a union of tuples is the JSON array []any
	}
	if reduced := c.reduceSubtypes(members); len(reduced) < len(members) {
		if len(reduced) == 1 {
			return c.agree(reduced[0], t) // T10w: TypeScript reduces a union of a type and its subtypes to that type
		}
		return c.union(up, reduced, t)
	}
	if it, ok := t.Underlying().(*types.Interface); ok && it.NumMethods() > 0 {
		return yesV()
	}
	if len(members) == 2 {
		one, many := members[0], strings.TrimPrefix(members[1], "readonly ")
		if strings.HasSuffix(one, "[]") {
			one, many = members[1], strings.TrimPrefix(members[0], "readonly ")
		}
		if sl, ok := t.Underlying().(*types.Slice); ok && many == one+"[]" {
			return c.agree(one, sl.Elem()) // T10s: one X or several (a readonly array is compile-time only) is a Go []X
		}
	}
	if st, ok := t.Underlying().(*types.Struct); ok {
		if v, ok := c.wrapperArm(members, t); ok {
			return v // T10x: `X | W` where W's one required member is an X is the Go form of W; a bare X is W with defaults
		}
		if c.oneOfStruct(members, st) {
			return yesV() // T10o: a struct of nilable fields, one per member, holds whichever member is set
		}
		if v, ok := c.taggedStruct(members, t, st); ok {
			return v // T10d: a union of object shapes is a Go struct holding the fields of every member
		}
		if c.codecUnion(members, t, st) {
			return yesV() // T10j
		}
	}
	return unknownV("T10: union %q against Go type %s (not an interface with methods)", up, typeLabel(t))
}

// literalFlags applies T10f and T10e to a union of string literals and types. T10f: a Go struct holds a bool field named after each
// literal and one field that agrees with each other member (tui WheelScrollLines{Auto, Lines} for `number | "auto"`). T10e: a Go
// integer type has a constant named <Type><Literal> for each literal, and <Type>True and <Type>False for a boolean member
// (QuietStartupTrue, QuietStartupFalse, QuietStartupHeader for `boolean | "header"`). Any other member leaves the rule silent.
func (c *checker) literalFlags(members []string, t types.Type) bool {
	var lits, others []string
	for _, m := range members {
		if stringLit.MatchString(m) {
			lits = append(lits, m[1:len(m)-1])
		} else {
			others = append(others, m)
		}
	}
	inner := *c
	inner.depth++
	if st, ok := t.Underlying().(*types.Struct); ok {
		used := map[int]bool{}
		for _, l := range lits {
			i := fieldIndex(st, func(f *types.Var) bool { return norm(f.Name()) == norm(l) && basicInfo(f.Type())&types.IsBoolean != 0 })
			if i < 0 {
				return false
			}
			used[i] = true
		}
		for _, o := range others {
			i := fieldIndex(st, func(f *types.Var) bool { return inner.agree(o, f.Type()).ok == yes })
			if i < 0 || used[i] {
				return false
			}
			used[i] = true
		}
		return true
	}
	n := namedOf(t)
	if n == nil || basicInfo(t)&types.IsInteger == 0 || len(others) > 1 || len(others) == 1 && others[0] != "boolean" {
		return false
	}
	names := map[string]bool{}
	for _, name := range n.Obj().Pkg().Scope().Names() {
		if cst, ok := n.Obj().Pkg().Scope().Lookup(name).(*types.Const); ok && types.Identical(cst.Type(), t) {
			names[norm(name)] = true
		}
	}
	want := lits
	if len(others) == 1 {
		want = append(want, "true", "false")
	}
	for _, l := range want {
		if !names[norm(n.Obj().Name()+l)] {
			return false
		}
	}
	return true
}

// fieldIndex returns the index of the first field of st that ok accepts, or -1.
func fieldIndex(st *types.Struct, ok func(*types.Var) bool) int {
	for i := 0; i < st.NumFields(); i++ {
		if ok(st.Field(i)) {
			return i
		}
	}
	return -1
}

// codecUnion applies T10j: an untagged union of JSON kinds (`boolean | { window?: ... }`, `string[] | { add?, remove? }`,
// `string | number`) is a Go struct with its own MarshalJSON and UnmarshalJSON that holds a field for every member: a field of the
// member's type for a boolean, string, number or array member, and the struct's own fields for an object member. At most one member
// may be an object, so that the members are told apart by JSON kind.
func (c *checker) codecUnion(members []string, t types.Type, st *types.Struct) bool {
	if !hasMethodNamed(t, "MarshalJSON") || !hasMethodNamed(types.NewPointer(t), "UnmarshalJSON") {
		return false
	}
	inner := *c
	inner.depth++
	objects := 0
	for _, m := range members {
		if strings.HasPrefix(stripTypeNoise(m), "{") {
			objects++
			if objects > 1 || inner.agree(m, t).ok != yes {
				return false
			}
			continue
		}
		if !strings.HasSuffix(m, "[]") && m != "boolean" && m != "string" && m != "number" && (!identRe.MatchString(m) || inner.alias(m) == nil) {
			return false // T10j: an alias member (KeyId) stands for its body; the field search below decides whether Go holds it
		}
		found := false
		for i := 0; i < st.NumFields() && !found; i++ {
			found = inner.agree(m, st.Field(i).Type()).ok == yes
		}
		if !found {
			return false
		}
	}
	return true
}

// hasMethodNamed reports whether the method set of t has a method called name.
func hasMethodNamed(t types.Type, name string) bool {
	ms := types.NewMethodSet(t)
	for method := range ms.Methods() {
		if method.Obj().Name() == name {
			return true
		}
	}
	return false
}

// isRawJSON reports whether t is json.RawMessage, which Go 1.27 declares as an alias of jsontext.Value.
func isRawJSON(t types.Type) bool {
	if a, ok := t.(*types.Alias); ok && a.Obj().Pkg() != nil && a.Obj().Pkg().Path() == "encoding/json" && a.Obj().Name() == "RawMessage" {
		return true
	}
	n := namedOf(t)
	if n == nil || n.Obj().Pkg() == nil {
		return false
	}
	path, name := n.Obj().Pkg().Path(), n.Obj().Name()
	return path == "encoding/json" && name == "RawMessage" || path == "encoding/json/jsontext" && name == "Value"
}

// nullableBoolean reports whether up is `boolean | null`, optionally with undefined: a boolean whose null is a value of its own.
func nullableBoolean(up string) bool {
	seen := map[string]bool{}
	for _, m := range splitTop(up, "|") {
		switch m = strings.TrimSpace(m); m {
		case "undefined":
		case "true", "false":
			seen["boolean"] = true
		default:
			seen[m] = true
		}
	}
	return len(seen) == 2 && seen["boolean"] && seen["null"]
}

// triStateBool reports whether t is a named integer type whose package declares exactly three constants of it, named ...False, ...True
// and ...Null.
// emptyStruct reports whether t is the empty struct type, possibly named.
func emptyStruct(t types.Type) bool {
	st, ok := types.Unalias(t).Underlying().(*types.Struct)
	return ok && st.NumFields() == 0
}

// nullableUnion reports whether up is a union with a null member (undefined members are ignored) and returns the union without null and undefined.
func nullableUnion(up string) (string, bool) {
	var kept []string
	hasNull := false
	for _, m := range splitTop(up, "|") {
		switch m = strings.TrimSpace(m); m {
		case "null":
			hasNull = true
		case "undefined", "":
		default:
			kept = append(kept, m)
		}
	}
	if !hasNull || len(kept) == 0 {
		return "", false
	}
	return strings.Join(kept, " | "), true
}

// changeWrapperArg returns T when t is `Name[T]`, an instantiation of a generic struct whose package declares generic functions SetTo[T](T) Name[T] and
// Cleared[T]() Name[T]: unset is the zero value, SetTo replaces, Cleared is upstream's null (T3c). Otherwise nil.
func changeWrapperArg(t types.Type) types.Type {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok || n.TypeArgs().Len() != 1 || n.Obj().Pkg() == nil {
		return nil
	}
	if _, isStruct := n.Underlying().(*types.Struct); !isStruct {
		return nil
	}
	returnsWrapper := func(name string, params int) bool {
		fn, ok := n.Obj().Pkg().Scope().Lookup(name).(*types.Func)
		if !ok {
			return false
		}
		sig := fn.Type().(*types.Signature)
		if sig.TypeParams().Len() != 1 || sig.Params().Len() != params || sig.Results().Len() != 1 {
			return false
		}
		res, ok := types.Unalias(sig.Results().At(0).Type()).(*types.Named)
		return ok && res.Origin() == n.Origin()
	}
	if !returnsWrapper("SetTo", 1) || !returnsWrapper("Cleared", 0) {
		return nil
	}
	return n.TypeArgs().At(0)
}

func triStateBool(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok || n.Obj().Pkg() == nil || basicInfo(n)&types.IsInteger == 0 {
		return false
	}
	found := map[string]bool{}
	scope := n.Obj().Pkg().Scope()
	for _, name := range scope.Names() {
		k, ok := scope.Lookup(name).(*types.Const)
		if !ok || !types.Identical(k.Type(), n) {
			continue
		}
		suffix := ""
		for _, s := range []string{"False", "True", "Null"} {
			if strings.HasSuffix(name, s) {
				suffix = s
			}
		}
		if suffix == "" || found[suffix] {
			return false
		}
		found[suffix] = true
	}
	return len(found) == 3
}

func isEmptyInterface(t types.Type) bool {
	it, ok := t.Underlying().(*types.Interface)
	return ok && it.NumMethods() == 0
}

// allStringLike reports whether every member is a string, a string literal, URL, an open string (string & {}) or an alias of those.
func (c *checker) allStringLike(members []string, depth int) bool {
	for _, m := range members {
		m = strings.TrimSpace(m)
		switch {
		case m == "string" || m == "URL" || stringLit.MatchString(m) || m == "(string & {})" || m == "string & {}":
		case identRe.MatchString(m) && depth < 3 && c.alias(m) != nil:
			if !c.allStringLike(splitTop(*c.alias(m), "|"), depth+1) {
				return false
			}
		default:
			return false
		}
	}
	return len(members) > 0
}

// allJSONish reports whether every member is a primitive, array, record or object shape, or an alias of one.
func (c *checker) allJSONish(members []string) bool {
	for _, m := range members {
		m = strings.TrimSpace(m)
		switch {
		case m == "string" || m == "number" || m == "boolean" || m == "null" || m == "unknown" || m == "any" || m == "object":
		case m == "true" || m == "false": // a boolean literal member (constrainedSampling?: false | Config) is a JSON value
		case arrayRe.MatchString(m) || strings.HasPrefix(m, "Record<") || strings.HasPrefix(m, "{") || strings.HasPrefix(m, "Array<"):
		case identRe.MatchString(m) && c.alias(m) != nil:
			for _, b := range splitTop(*c.alias(m), "|") {
				b = strings.TrimSpace(b)
				jsonShape := b == "string" || b == "number" || b == "boolean" || b == "null" || arrayRe.MatchString(b) ||
					strings.HasPrefix(b, "Record<") || strings.HasPrefix(b, "{") || identRe.MatchString(b) && c.alias(b) != nil
				if !jsonShape {
					return false
				}
			}
		default:
			return false
		}
	}
	return len(members) > 0
}

func (c *checker) stringKind(up string, t types.Type) verdict {
	if basicInfo(t)&types.IsString != 0 {
		return yesV()
	}
	return noV("T1: upstream %s is a string, Go type is %s", up, typeLabel(t))
}

func (c *checker) single(up string, t types.Type) verdict {
	for strings.HasPrefix(up, "(") && strings.HasSuffix(up, ")") && !isFuncType(up) && matchingParen(up) {
		up = strings.TrimSpace(up[1 : len(up)-1])
		if len(splitTop(up, "|")) > 1 || len(splitTop(up, "&")) > 1 {
			return c.agree(up, t) // T15p: a parenthesised union or intersection (`(A & B) | undefined`) is read as itself
		}
	}
	if _, ok := types.Unalias(t).(*types.TypeParam); ok {
		return yesV() // T11: a Go type parameter accepts any upstream type
	}
	if c.generics[up] {
		return yesV() // T11: an upstream type parameter accepts any Go type
	}
	switch {
	case up == "string" || stringLit.MatchString(up) || templateLit.MatchString(up):
		return c.stringKind(up, t) // T1t: a template literal type (`ctrl+${K}`) is a string
	case up == "number" || numberLit.MatchString(up):
		if basicInfo(t)&types.IsNumeric != 0 {
			return yesV() // T2
		}
		if numericConstraint(t) {
			return yesV() // T2c: a constraint whose type set holds only numeric types is the Go form of a family of branded numbers
		}
		return noV("T2: upstream number, Go type is %s", typeLabel(t))
	case up == "boolean" || up == "true" || up == "false":
		if basicInfo(t)&types.IsBoolean != 0 {
			return yesV() // T3
		}
		return noV("T3: upstream boolean, Go type is %s", typeLabel(t))
	case up == "any" || up == "unknown" || up == "object" || up == "{}":
		if types.IsInterface(t) || isByteSlice(t) {
			return yesV() // T4
		}
		return unknownV("T4: upstream %s, Go type is %s", up, typeLabel(t))
	case up == "AbortSignal":
		if isContext(t) {
			return yesV() // T8
		}
		return noV("T8: AbortSignal needs context.Context, Go type is %s", typeLabel(t))
	case up == "Headers":
		if n := namedOf(t); n != nil && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "net/http" && n.Obj().Name() == "Header" {
			return yesV() // T8h: the Fetch API Headers are net/http.Header
		}
		return unknownV("T8h: upstream Headers, Go type is %s", typeLabel(t))
	case up == "ProcessEnv" || up == "NodeJS.ProcessEnv":
		switch u := t.Underlying().(type) {
		case *types.Slice:
			if basicInfo(u.Elem())&types.IsString != 0 {
				return yesV() // T8e: an environment is os/exec's KEY=VALUE list
			}
		case *types.Map:
			if basicInfo(u.Key())&types.IsString != 0 && basicInfo(u.Elem())&types.IsString != 0 {
				return yesV() // T8e: or a map from name to value
			}
		}
		return noV("T8e: upstream ProcessEnv needs []string or map[string]string, Go type is %s", typeLabel(t))
	case up == "Date":
		if n := namedOf(t); n != nil && n.Obj().Name() == "Time" {
			return yesV()
		}
		return noV("T8: Date needs time.Time, Go type is %s", typeLabel(t))
	case up == "Error":
		if isErrorType(t) {
			return yesV()
		}
		return noV("T8: Error needs error, Go type is %s", typeLabel(t))
	case byteArrayType(up):
		if isByteSlice(t) {
			return yesV() // T5
		}
		return noV("T5: upstream %s needs []byte, Go type is %s", up, typeLabel(t))
	case strings.HasPrefix(up, "Promise<") && strings.HasSuffix(up, ">"):
		if ch, ok := t.Underlying().(*types.Chan); ok && ch.Dir() != types.SendOnly {
			return c.promiseChan(up[len("Promise<"):len(up)-1], ch.Elem())
		}
		return c.agree(up[len("Promise<"):len(up)-1], t)
	case isFuncType(up):
		return c.funcType(up, t)
	}
	if v, ok := c.indexed(up, t); ok {
		return v
	}
	if m := arrayRe.FindStringSubmatch(up); m != nil {
		return c.sliceOf(m[1], t)
	}
	if g := genericRe.FindStringSubmatch(up); g != nil {
		switch name := g[1]; {
		case name == "Extract" || name == "Exclude":
			if args := splitTop(g[2], ","); len(args) == 2 {
				inner := *c
				inner.depth++
				if v := inner.agree(args[0], t); v.ok == yes {
					return v // T19: Extract/Exclude narrow at compile time; the Go form of the wide type holds every value they allow
				}
				if sel, ok := c.selectMembers(args[0], args[1], name == "Extract"); ok {
					return inner.agree(sel, t) // T19s: the members of a named-type union that the object pattern selects
				}
			}
			return unknownV("T19: %s against Go type %s", truncate(up, 60), typeLabel(t))
		case (name == "Static" || name == "StaticType") && (isRawJSON(t) || isEmptyInterface(t)):
			args := splitTop(g[2], ",")
			if schema := strings.TrimSpace(args[len(args)-1]); c.generics[schema] {
				return yesV() // TB2: the value of a schema type parameter is the JSON it validates, unparsed or decoded to the caller's type
			}
		case name == "Array" || name == "ReadonlyArray":
			return c.sliceOf(g[2], t)
		case name == "Record" || name == "Map" || name == "ReadonlyMap":
			return c.mapOf(g[2], t)
		case name == "Set" || name == "ReadonlySet":
			return c.setOf(g[2], t)
		case name == "Iterable" || name == "AsyncIterable" || name == "IterableIterator" || name == "AsyncIterableIterator":
			if elem, ok := iterationElem(g[2]); ok {
				return c.iterableOf(elem, t)
			}
		case name == "Iterator" || name == "AsyncIterator" || name == "Generator" || name == "AsyncGenerator":
			if elem, ok := iterationElem(g[2]); ok {
				return c.iterableOf(elem, t) // T14i: the cursor of an iteration (T6i: the yielded type is the first argument); Go iterates by range over the sequence instead
			}
		case name == "Omit" || name == "Pick":
			args := splitTop(g[2], ",")
			keys, ok := stringLiteralUnion(strings.Join(args[1:], ","))
			if len(args) != 2 || !ok {
				return c.agree(args[0], t)
			}
			inner := *c // T16: Omit and Pick select the members of the operand's first object type that the rules judge
			set := map[string]bool{}
			for _, k := range keys {
				set[k] = true
			}
			if name == "Omit" {
				// An enclosing Omit still removes its members from the members this one keeps (TypedEntryDraft = Omit<EntryDraft, "kind" | "data">,
				// EntryDraft = Omit<EntryRecord, "id" | ...> & ...).
				maps.Copy(set, c.omit)
				inner.omit = set
			} else {
				var have []string
				if c.memberNames(args[0], 0, &have, map[string]map[string]bool{}) {
					for _, k := range keys {
						if !slices.Contains(have, k) {
							return unknownV("T16: Pick key %q is not a member of %s", k, truncate(args[0], 40))
						}
					}
				}
				inner.pick = set
			}
			return inner.agree(args[0], t)
		case name == "Partial":
			inner := *c
			inner.partial = true
			return inner.agree(splitTop(g[2], ",")[0], t)
		case wrapperGen[name]:
			return c.agree(splitTop(g[2], ",")[0], t)
		default:
			v := c.named(name, t)
			if v.ok == yes || c.instDepth >= maxInstDepth { // a generic alias judged by its unsubstituted body is a false verdict: instantiate first
				return v
			}
			if body, ok := c.aliases.instantiate(c.pkg, name, g[2]); ok {
				inner := *c
				inner.instDepth++
				return inner.agree(body, t) // A9: a generic alias instance is its body with the type arguments substituted
			}
			return v
		}
	}
	if strings.HasPrefix(up, "{") {
		if mm := mapLiteral(up); mm != "" {
			return c.mapOf("string, "+mm, t)
		}
		return c.objectLiteral(up, t)
	}
	if strings.HasPrefix(up, "[") {
		if elem, ok := nonEmptyArrayElem(up); ok {
			return c.agree(elem+"[]", t) // T5n
		}
		return unknownV("T12: tuple type %q", up)
	}
	if identRe.MatchString(up) {
		return c.named(up, t)
	}
	return unknownV("T12: type %q has no rule", up)
}

// matchingParen reports whether the first parenthesis of s closes at its end.
func matchingParen(s string) bool {
	depth := 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i == len(s)-1
			}
		}
	}
	return false
}

// objectLiteral applies T12: an upstream object literal type needs a Go struct with a field for each member.
func (c *checker) objectLiteral(up string, t types.Type) verdict {
	if sig, ok := t.Underlying().(*types.Signature); ok {
		if v, ok := c.overloadSet(up, sig); ok {
			return v // T12o
		}
	}
	if it, ok := t.Underlying().(*types.Interface); ok && it.NumMethods() > 0 {
		return c.literalAgainstInterface(up, it) // T12b
	}
	st, ok := t.Underlying().(*types.Struct)
	if m := mappedTypeRe.FindStringSubmatch(strings.TrimPrefix(stripTypeNoise(up), "{")); m != nil {
		if c.generics[m[1]] && isEmptyInterface(t) {
			return yesV() // T11m: a mapped type over a type parameter's keys is as open as the parameter (T11)
		}
		if v, ok := c.callerKeyedMap(up, t); ok {
			return v
		}
		return unknownV("T12: mapped type %q against Go type %s", truncate(up, 50), typeLabel(t))
	}
	if mt, isMap := t.Underlying().(*types.Map); isMap && basicInfo(mt.Key())&types.IsString != 0 {
		if v, ok := c.keyTable(up, mt.Elem()); ok {
			return v
		}
	}
	if !ok && isEmptyInterface(t) && strings.Contains(up, ":") {
		return noV("T12: upstream object type %q, Go type is the empty interface, which has none of its members", truncate(up, 50))
	}
	if !ok {
		return unknownV("T12: object literal type %q against Go type %s", truncate(up, 50), typeLabel(t))
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(stripTypeNoise(up), "{"), "}")
	var vs []verdict
	sel := c.selection()
	c = &checker{}
	*c = *sel.rest
	for _, part := range splitTop(strings.ReplaceAll(inner, ";", ","), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, typ, ok := objectMember(part)
		if !ok {
			return unknownV("T12: object literal member %q", part)
		}
		if !sel.keeps(name) || strings.TrimSpace(typ) == "never" || c.aliases.isBrand(name) { // a unique-symbol brand exists only in the type system
			continue // T12n: a never member is one the value does not have; any Go field, or none, carries its absence
		}
		if isSignalType(typ) && structField(st, name, 0) == nil {
			if c.ctxInScope || c.ctxConsumers != nil && c.ctxConsumers(t) {
				continue // T12s: an AbortSignal member is the context.Context of the call that takes the value
			}
			vs = append(vs, unknownV("T12s: AbortSignal member %s outside a call that takes a context.Context", name))
			continue
		}
		if field := structField(st, name, 0); field != nil {
			vs = append(vs, c.agree(typ, field.Type()))
			continue
		}
		if field := funcField(st, name); field != nil && optionalFuncType(typ) {
			vs = append(vs, c.agree(typ, field.Type())) // T12f: a callback member is a Go func field that JSON skips
			continue
		}
		if m := methodFor(t, name); m != nil {
			sig := m.Type().(*types.Signature)
			switch {
			case isFuncType(typ):
				vs = append(vs, c.agree(typ, sig)) // T12m: a function member is a method of the Go type
				continue
			case sig.Params().Len() == 0 && sig.Results().Len() > 0 && !literalTypeRe.MatchString(typ):
				vs = append(vs, c.agree(typ, sig.Results().At(0).Type())) // T12a: a data member is a parameterless accessor (M2)
				continue
			}
		}
		if typ == "true" && strings.HasPrefix(name, "[") && strings.HasSuffix(name, "Brand]") {
			continue // T12k: `readonly [xBrand]: true` is a unique-symbol brand, a compile-time tag with no value for Go to carry
		}
		if lit := literalTypeRe.FindStringSubmatch(typ); lit != nil && c.discriminates != nil {
			if n := namedOf(deref(t)); n != nil && c.discriminates(n.Obj(), name, lit[1]) {
				continue // T12d: a string-literal member is a discriminator the Go type writes (M5 or its encoder)
			}
		}
		return noV("T12: Go struct %s has no field for %s", typeLabel(t), name)
	}
	return all(vs...)
}

// nilable reports whether a Go type can hold "absent": a pointer, slice, map, interface, function or channel.
func nilable(t types.Type) bool {
	switch t.Underlying().(type) {
	case *types.Pointer, *types.Slice, *types.Map, *types.Interface, *types.Signature, *types.Chan:
		return true
	}
	return false
}

var (
	keyofRe   = regexp.MustCompile(`^keyof\s+([A-Za-z_]\w*)$`)
	indexedRe = regexp.MustCompile(`^\(?([A-Za-z_]\w*)(?:<[^\[\]]*>)?\)?\[([^\]]+)\]$`)
)

// indexed applies T17 to type operators over a named upstream interface X: `keyof X` is the union of X's property names, `X["p"]`
// is the type of property p, and `X[K]` for a type parameter or `keyof X` is the union of every property type. The result is judged
// by the other rules; an X with no recorded properties leaves the rule silent.
func (c *checker) indexed(up string, t types.Type) (verdict, bool) {
	if c.props == nil || c.depth >= 3 {
		return verdict{}, false
	}
	inner := *c
	inner.depth++
	if base, ok := strings.CutSuffix(up, "[number]"); ok {
		if c.generics[strings.TrimSpace(base)] {
			return yesV(), true // T11: the element type of a type parameter is chosen by the caller, as the parameter is
		}
		if m := indexedRe.FindStringSubmatch(base); m != nil {
			if lit := literalTypeRe.FindStringSubmatch(strings.TrimSpace(m[2])); lit != nil {
				for _, p := range c.props(m[1]) {
					if a := arrayRe.FindStringSubmatch(strings.TrimSpace(p.Type)); p.Name == lit[1] && a != nil {
						return inner.agree(a[1], t), true // T17e: X["p"][number] is the element type of the array property p
					}
				}
			}
		}
		return verdict{}, false
	}
	m := indexedRe.FindStringSubmatch(up)
	if m == nil {
		return verdict{}, false
	}
	props := c.props(m[1])
	if len(props) == 0 {
		lit := literalTypeRe.FindStringSubmatch(strings.TrimSpace(m[2]))
		if lit == nil {
			return verdict{}, false
		}
		if typs, ok := c.aliasMemberTypes(m[1], lit[1], 0); ok && len(typs) > 0 {
			return inner.agree(strings.Join(typs, " | "), t), true // T17a: X["p"] of an alias is p's type in every object it is made of
		}
		return verdict{}, false
	}
	key := strings.TrimSpace(m[2])
	if lit := literalTypeRe.FindStringSubmatch(key); lit != nil {
		for _, p := range props {
			if p.Name == lit[1] {
				return inner.agree(p.Type, t), true
			}
		}
		return noV("T17: %s has no property %s", m[1], key), true
	}
	if body := c.alias(key); identRe.MatchString(key) && body != nil {
		key = strings.TrimSpace(*body) // an alias of the key type (ai ModelType = keyof ModelTypeMap)
	}
	if !c.generics[key] && !keyofRe.MatchString(key) {
		return verdict{}, false
	}
	var types []string
	for _, p := range props {
		if !contains(types, p.Type) {
			types = append(types, p.Type)
		}
	}
	return inner.agree(strings.Join(types, " | "), t), true
}

// literalUtility applies T18 to Exclude<A, B> and Extract<A, B> when A is, or names an alias of, a union of string literals and B is
// a union of string literals: it returns the remaining literals as a union. Any other operand leaves the type unchanged.
func (c *checker) literalUtility(up string) (string, bool) {
	m := genericRe.FindStringSubmatch(up)
	if m == nil || (m[1] != "Exclude" && m[1] != "Extract") {
		return "", false
	}
	args := splitTop(m[2], ",")
	if len(args) != 2 {
		return "", false
	}
	from := strings.TrimSpace(args[0])
	if body := c.alias(from); identRe.MatchString(from) && body != nil {
		from = *body
	}
	all, ok := stringLiteralUnion(from)
	drop, ok2 := stringLiteralUnion(args[1])
	if !ok || !ok2 {
		return "", false
	}
	var keep []string
	for _, l := range all {
		if contains(drop, l) == (m[1] == "Extract") {
			keep = append(keep, strconv.Quote(l))
		}
	}
	if len(keep) == 0 {
		return "never", true
	}
	return strings.Join(keep, " | "), true
}

// promiseChan applies T3c: a Promise<T> held as a value is a Go receive channel that delivers T once, either as the element itself or
// as a result struct of T and an error (the rejection). An empty struct only signals settlement, which is all a Promise<void>
// delivers; for another T it is judged as the element, so the value it drops stays a gap.
func (c *checker) promiseChan(up string, elem types.Type) verdict {
	st, ok := elem.Underlying().(*types.Struct)
	if !ok {
		return c.agree(up, elem)
	}
	if st.NumFields() == 0 && (up == "void" || up == "undefined") {
		return yesV()
	}
	if st.NumFields() == 0 && c.recv != nil {
		if v, ok := c.settledSibling(up); ok {
			return v
		}
	}
	if st.NumFields() == 2 {
		for i := range 2 {
			if isErrorType(st.Field(i).Type()) && !isErrorType(st.Field(1-i).Type()) {
				return c.agree(up, st.Field(1-i).Type()) // the value and the rejection
			}
		}
	}
	return c.agree(up, elem)
}

// settledSibling applies T3s: an accessor that returns a chan struct{} signals settlement, and another parameterless method of the
// same receiver whose single result agrees with T reads the settled value (Closed() and End(),
// Terminated() and TerminalError()).
func (c *checker) settledSibling(up string) (verdict, bool) {
	inner := *c
	inner.recv = nil
	ms := types.NewMethodSet(c.recv)
	if _, isPtr := c.recv.(*types.Pointer); !isPtr && !types.IsInterface(c.recv) {
		ms = types.NewMethodSet(types.NewPointer(c.recv))
	}
	for method := range ms.Methods() {
		fn, ok := method.Obj().(*types.Func)
		if !ok || !fn.Exported() {
			continue
		}
		sig := fn.Type().(*types.Signature)
		res := sig.Results()
		if sig.Params().Len() != 0 || res.Len() != 1 {
			continue
		}
		if v := inner.agree(up, res.At(0).Type()); v.ok == yes {
			return v, true
		}
	}
	return verdict{}, false
}

// literalTuple applies T5t: a (readonly) tuple of string literals, the type of an `as const` array, is a Go slice of a string type
// or an array of that length. Another tuple is left to the other rules.
func literalTuple(up string, t types.Type) (verdict, bool) {
	body, ok := strings.CutPrefix(up, "readonly ")
	if !ok {
		body = up
	}
	if !strings.HasPrefix(body, "[") || !strings.HasSuffix(body, "]") {
		return verdict{}, false
	}
	elems := splitTop(body[1:len(body)-1], ",")
	for _, e := range elems {
		if !stringLit.MatchString(strings.TrimSpace(e)) {
			return verdict{}, false
		}
	}
	switch u := t.Underlying().(type) {
	case *types.Slice:
		if basicInfo(u.Elem())&types.IsString != 0 {
			return yesV(), true
		}
	case *types.Array:
		if basicInfo(u.Elem())&types.IsString != 0 && u.Len() == int64(len(elems)) {
			return yesV(), true
		}
	}
	return noV("T5t: upstream is a tuple of %d string literals, Go type is %s", len(elems), typeLabel(t)), true
}

// quotedStringLiteral reports whether up is one double-quoted string literal, including one with escape sequences, which the
// plain literal pattern does not read.
func quotedStringLiteral(up string) bool {
	var s string
	return json.Unmarshal([]byte(up), &s) == nil
}

// selectMembers evaluates Extract (keep) or Exclude over a union of named interfaces and an object pattern of literal members
// (`{ type: "toolCall" }`): a member is selected when each pattern key is a property whose type is that literal. The union may be
// written out or be X["p"] or X["p"][number] of a recorded interface property.
func (c *checker) selectMembers(from, pattern string, keep bool) (string, bool) {
	from = strings.TrimSpace(from)
	if base, ok := strings.CutSuffix(from, "[number]"); ok {
		elem, ok := c.propertyType(base)
		if !ok {
			return "", false
		}
		a := arrayRe.FindStringSubmatch(elem)
		if a == nil {
			return "", false
		}
		from = a[1]
	} else if typ, ok := c.propertyType(from); ok {
		from = typ
	} else if body := c.alias(from); body != nil {
		from = stripTypeNoise(*body) // an alias of a union of object types (durable StorageWrite)
	}
	from = strings.TrimSpace(from)
	if strings.HasPrefix(from, "(") && strings.HasSuffix(from, ")") {
		from = from[1 : len(from)-1]
	}
	body := strings.TrimSpace(pattern)
	if !strings.HasPrefix(body, "{") || !strings.HasSuffix(body, "}") || c.props == nil {
		return "", false
	}
	want := map[string][]string{} // pattern member -> the string literals it accepts
	for _, part := range splitTop(strings.ReplaceAll(body[1:len(body)-1], ";", ","), ",") {
		if part = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(part), "readonly ")); part == "" {
			continue
		}
		name, typ, _ := objectMember(part)
		for _, lit := range splitTop(typ, "|") {
			if !stringLit.MatchString(lit) {
				return "", false
			}
			want[name] = append(want[name], lit)
		}
	}
	var kept []string
	for _, m := range splitTop(from, "|") {
		if m = strings.TrimSpace(m); m == "" {
			continue
		}
		props := c.props(m)
		if strings.HasPrefix(m, "{") {
			props = inlineProps(m)
		}
		if len(props) == 0 {
			return "", false
		}
		match := true
		for k, lits := range want {
			found := false
			for _, p := range props {
				found = found || p.Name == k && slices.Contains(lits, strings.TrimSpace(p.Type))
			}
			match = match && found
		}
		if match == keep {
			kept = append(kept, m)
		}
	}
	if len(kept) == 0 || len(want) == 0 {
		return "", false
	}
	return strings.Join(kept, " | "), true
}

// inlineProps reads the members of an inline object type.
func inlineProps(obj string) []propShape {
	var out []propShape
	body := strings.TrimSpace(obj)
	for _, part := range splitTop(strings.ReplaceAll(body[1:len(body)-1], ";", ","), ",") {
		name, typ, _ := objectMember(strings.TrimSpace(part))
		out = append(out, propShape{Name: name, Type: typ})
	}
	return out
}

// propertyType returns the recorded type of X["p"] for an upstream interface X.
func (c *checker) propertyType(up string) (string, bool) {
	m := indexedRe.FindStringSubmatch(up)
	if m == nil || c.props == nil {
		return "", false
	}
	lit := literalTypeRe.FindStringSubmatch(strings.TrimSpace(m[2]))
	if lit == nil {
		return "", false
	}
	for _, p := range c.props(m[1]) {
		if p.Name == lit[1] {
			return strings.TrimSpace(p.Type), true
		}
	}
	return "", false
}

// aliasMemberTypes collects the types of member p in every object type an upstream alias is made of, through unions,
// intersections, parentheses and nested aliases or recorded interfaces. It fails when a part is neither.
func (c *checker) aliasMemberTypes(name, p string, depth int) ([]string, bool) {
	body := c.alias(name)
	if body == nil || depth > 3 {
		return nil, false
	}
	var out []string
	var walk func(string) bool
	walk = func(text string) bool {
		text = stripTypeNoise(strings.TrimSpace(text))
		if _, _, yes, no, ok := splitConditional(text); ok {
			return (yes == "never" || walk(yes)) && (no == "never" || walk(no)) // T20: both branches
		}
		if parts := splitTop(text, "|"); len(parts) > 1 {
			for _, part := range parts {
				if strings.TrimSpace(part) != "" && !walk(part) {
					return false
				}
			}
			return true
		}
		if parts := splitTop(text, "&"); len(parts) > 1 {
			for _, part := range parts {
				if strings.TrimSpace(part) != "" && !walk(part) {
					return false
				}
			}
			return true
		}
		switch {
		case strings.HasPrefix(text, "(") && strings.HasSuffix(text, ")"):
			return walk(text[1 : len(text)-1])
		case strings.HasPrefix(text, "{") && strings.HasSuffix(text, "}"):
			for _, part := range splitTop(strings.ReplaceAll(text[1:len(text)-1], ";", ","), ",") {
				n, typ, ok := objectMember(strings.TrimPrefix(strings.TrimSpace(part), "readonly "))
				if ok && n == p && strings.TrimSpace(typ) != "never" && !contains(out, strings.TrimSpace(typ)) {
					out = append(out, strings.TrimSpace(typ))
				}
			}
			return true
		case identRe.MatchString(text):
			if props := c.props(text); len(props) > 0 {
				for _, pr := range props {
					if pr.Name == p && !contains(out, pr.Type) {
						out = append(out, pr.Type)
					}
				}
				return true
			}
			typs, ok := c.aliasMemberTypes(text, p, depth+1)
			for _, typ := range typs {
				if !contains(out, typ) {
					out = append(out, typ)
				}
			}
			return ok
		}
		return false
	}
	return out, walk(*body)
}

// signalOnly reports whether an intersection operand is an inline object type whose members are all AbortSignals.
func signalOnly(operand string) bool {
	body := plainObjectBody(operand)
	if body == "" {
		return false
	}
	n := 0
	for _, part := range splitTop(strings.ReplaceAll(body[1:len(body)-1], ";", ","), ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		if _, typ, _ := objectMember(strings.TrimSpace(part)); !isSignalType(typ) {
			return false
		}
		n++
	}
	return n > 0
}

// phantomBrand reports whether an intersection operand is an object type whose members all have computed keys
// (`{ readonly [idBrand]: {...} }`), the TypeScript idiom for a nominal brand that has no runtime value.
func phantomBrand(op string) bool {
	b := stripTypeNoise(op)
	if !strings.HasPrefix(b, "{") || !strings.HasSuffix(b, "}") {
		return false
	}
	parts := splitTop(strings.ReplaceAll(b[1:len(b)-1], ";", ","), ",")
	n := 0
	for _, part := range parts {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		if !strings.HasPrefix(part, "[") || strings.Contains(part[:strings.IndexByte(part+"]", ']')], ":") {
			return false // an index signature [k: string]: V is not a brand
		}
		n++
	}
	return n > 0
}

// numericConstraint reports whether t is an interface whose type set holds only numeric types (`interface{ ~int64 }`).
func numericConstraint(t types.Type) bool {
	it, ok := t.Underlying().(*types.Interface)
	if !ok || it.NumMethods() > 0 || it.NumEmbeddeds() == 0 {
		return false
	}
	for etyp := range it.EmbeddedTypes() {
		switch e := etyp.(type) {
		case *types.Union:
			for term := range e.Terms() {
				if basicInfo(term.Type())&types.IsNumeric == 0 {
					return false
				}
			}
		default:
			if basicInfo(e)&types.IsNumeric == 0 {
				return false
			}
		}
	}
	return true
}

// openRecord reports whether an intersection operand is `Record<string, unknown>` (or `any`): an index signature that admits extra
// keys. Pi uses it on per-provider options (`StreamOptions & Record<string, unknown>`, ai/src/types.ts:276-278 and 343); PiG carries each
// provider option as a typed StreamOptions field (LEAD-ANSWERS-ledger #2), so the slot adds no member a struct must have.
func openRecord(op string) bool {
	switch strings.Join(strings.Fields(op), " ") {
	case "Record<string, unknown>", "Record<string, any>":
		return true
	}
	return false
}

// inherited applies T15e to one named operand of an intersection: the Go struct satisfies it when it embeds a Go type that stands for
// the operand, or when every property of the upstream interface is a field or method of the struct (LEAD-ANSWERS-ledger #1: an
// inherited member is satisfied directly or through an embedded field). The operand's own rows judge the member types.
func (c *checker) inherited(op string, t types.Type) (verdict, bool) {
	name := op
	if g := genericRe.FindStringSubmatch(op); g != nil {
		name = g[1]
	}
	st, ok := deref(t).Underlying().(*types.Struct)
	if !ok || !identRe.MatchString(name) {
		return verdict{}, false
	}
	for f := range st.Fields() {
		if f.Embedded() && c.agree(op, f.Type()).ok == yes {
			return yesV(), true
		}
	}
	if c.propNames == nil {
		return verdict{}, false
	}
	props := c.propNames(name)
	if len(props) == 0 {
		return verdict{}, false
	}
	sel := c.selection()
	for _, p := range props {
		if sel.keeps(p) && structField(st, p, 0) == nil && methodFor(t, p) == nil {
			return noV("T15e: Go struct %s has no field or method for %s.%s", typeLabel(t), name, p), true
		}
	}
	return yesV(), true
}

// byteArrayType reports whether an upstream type is a byte buffer: Uint8Array, Buffer or ArrayBuffer, with or without the buffer type
// argument TypeScript 5.7 added (`Uint8Array<ArrayBufferLike>`), which does not change the element type.
func byteArrayType(up string) bool {
	name, _, _ := strings.Cut(up, "<")
	switch name {
	case "Uint8Array", "Buffer", "ArrayBuffer":
		return name == up || strings.HasSuffix(up, ">")
	}
	return false
}

// memberSelection is the Omit/Pick selection one object type applies, and the checker its member types use.
type memberSelection struct {
	omit, pick map[string]bool
	rest       *checker
}

func (s memberSelection) keeps(name string) bool {
	return !s.omit[name] && (s.pick == nil || s.pick[name])
}

// selection takes the pending Omit/Pick selection off the checker.
func (c *checker) selection() memberSelection {
	rest := *c
	rest.omit, rest.pick = nil, nil
	return memberSelection{omit: c.omit, pick: c.pick, rest: &rest}
}

// writesEvery reports whether a Go type writes member name with each of the string literals lits (T12d); an empty set is false.
func (c *checker) writesEvery(t types.Type, name string, lits map[string]bool) bool {
	n := namedOf(deref(t))
	if len(lits) == 0 || c.discriminates == nil || n == nil {
		return false
	}
	for lit := range lits {
		if !c.discriminates(n.Obj(), name, lit) {
			return false
		}
	}
	return true
}

// structField finds the Go field that stands for an upstream member: a field whose JSON name or Go name matches, directly or promoted
// from an embedded struct that encoding/json flattens (an embedded field without a JSON name), as TypeScript's `extends` and `&` do.
func structField(st *types.Struct, name string, depth int) *types.Var {
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		tag, _, _ := strings.Cut(reflect.StructTag(st.Tag(i)).Get("json"), ",")
		if tag == "-" {
			if _, isFunc := f.Type().Underlying().(*types.Signature); !isFunc || nameRule(name, f.Name()) == "" {
				continue
			}
			return f
		}
		if tag == name || nameRule(name, f.Name()) != "" {
			return f
		}
	}
	if depth >= 3 {
		return nil
	}
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		tag, _, _ := strings.Cut(reflect.StructTag(st.Tag(i)).Get("json"), ",")
		if !f.Embedded() || tag != "" {
			continue
		}
		if inner, ok := deref(f.Type()).Underlying().(*types.Struct); ok {
			if g := structField(inner, name, depth+1); g != nil {
				return g
			}
		}
	}
	return nil
}

// funcField returns the field of st, by name, whose type is a Go function and whose JSON tag is "-" (T12f). A function value never
// crosses JSON, so encoding/json's exclusion is how a Go struct carries a callback member of an options object.
func funcField(st *types.Struct, name string) *types.Var {
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		tag, _, _ := strings.Cut(reflect.StructTag(st.Tag(i)).Get("json"), ",")
		if _, ok := f.Type().Underlying().(*types.Signature); ok && tag == "-" && nameRule(name, f.Name()) != "" {
			return f
		}
	}
	return nil
}

// objectMember splits one member of an object literal type into its name and type. A method member `name?(params): R` has the
// function type `(params) => R`.
func objectMember(part string) (name, typ string, ok bool) {
	part = strings.TrimSpace(part)
	if open := strings.IndexByte(part, '('); open > 0 {
		if colon := strings.IndexByte(part, ':'); colon < 0 || open < colon {
			head := strings.TrimSuffix(strings.TrimSpace(part[:open]), "?")
			rest := part[open:]
			depth := 0
			for i, r := range rest {
				switch r {
				case '(':
					depth++
				case ')':
					depth--
					if depth == 0 {
						ret, has := strings.CutPrefix(strings.TrimSpace(rest[i+1:]), ":")
						if !has || !identRe.MatchString(head) {
							return "", "", false
						}
						return head, rest[:i+1] + " => " + strings.TrimSpace(ret), true
					}
				}
			}
			return "", "", false
		}
	}
	name, typ, ok = strings.Cut(part, ":")
	return strings.TrimSuffix(strings.TrimSpace(name), "?"), strings.TrimSpace(typ), ok
}

// methodFor returns the method of a Go type (or its pointer) whose name stands for an upstream member name.
func methodFor(t types.Type, name string) *types.Func {
	mset := types.NewMethodSet(types.NewPointer(deref(t)))
	for method := range mset.Methods() {
		if f, ok := method.Obj().(*types.Func); ok && f.Exported() && nameRule(name, f.Name()) != "" {
			return f
		}
	}
	return nil
}

// memberName is the name an object-literal member declares: the identifier before its optional marker, type parameters or the
// parameter list of a method member (`release(context: Context): void` declares release).
func memberName(member string) string {
	member = strings.TrimSpace(member)
	if i := strings.IndexAny(member, "?(<"); i >= 0 {
		member = member[:i]
	}
	return strings.TrimSpace(member)
}

// literalAgainstInterface applies T12b: each member of an upstream object literal is a method of the Go interface (an accessor).
func (c *checker) literalAgainstInterface(up string, it *types.Interface) verdict {
	inner := strings.TrimSuffix(strings.TrimPrefix(stripTypeNoise(up), "{"), "}")
	for _, part := range splitTop(strings.ReplaceAll(inner, ";", ","), ",") {
		name, _, ok := strings.Cut(strings.TrimSpace(part), ":")
		if !ok && !strings.Contains(part, "(") {
			continue
		}
		name = memberName(name)
		found := false
		for i := 0; i < it.NumMethods() && !found; i++ {
			found = nameRule(name, it.Method(i).Name()) != "" || name == "isDirectory" && it.Method(i).Name() == "IsDir" // Node fs.Stats and io/fs.FileInfo
		}
		if !found {
			return unknownV("T12b: Go interface has no method for object member %s", name)
		}
	}
	return yesV()
}

func (c *checker) sliceOf(elem string, t types.Type) verdict {
	switch u := t.Underlying().(type) {
	case *types.Slice:
		return c.agree(elem, u.Elem())
	case *types.Array:
		return c.agree(elem, u.Elem())
	case *types.Map:
		if st, ok := u.Elem().Underlying().(*types.Struct); ok && st.NumFields() == 0 {
			return c.agree(elem, u.Key()) // T5s: a Go set (map[K]struct{}) stands for a list of K read for membership
		}
	}
	if types.IsInterface(t) {
		return unknownV("T5: array of %s against %s", elem, typeLabel(t))
	}
	return noV("T5: upstream array, Go type is %s", typeLabel(t))
}

// setOf applies T6s: a Set<K> is a Go map keyed by K (struct{} or bool values) or a slice of K.
func (c *checker) setOf(key string, t types.Type) verdict {
	switch u := t.Underlying().(type) {
	case *types.Map:
		return c.agree(key, u.Key())
	case *types.Slice:
		return c.agree(key, u.Elem())
	}
	return noV("T6s: upstream Set, Go type is %s", typeLabel(t))
}

// iterableOf applies T14: an Iterable<T> is a Go slice or channel of T, a range-over-func sequence of T (iter.Seq[T]), or a type
// with a method that takes nothing or a context.Context and returns such a sequence (T14m: an event stream's Events(ctx)).
// iterationElem returns the element type of an iteration's type arguments <T, TReturn, TNext>. A range loop carries only T, so the
// return and next types must carry nothing (T14r): any, unknown, undefined or void.
func iterationElem(args string) (string, bool) {
	parts := splitTop(args, ",")
	for _, p := range parts[1:] {
		switch strings.TrimSpace(p) {
		case "any", "unknown", "undefined", "void":
		default:
			return "", false
		}
	}
	return strings.TrimSpace(parts[0]), true
}

func (c *checker) iterableOf(elem string, t types.Type) verdict {
	switch u := t.Underlying().(type) {
	case *types.Slice:
		return c.agree(elem, u.Elem())
	case *types.Chan:
		return c.agree(elem, u.Elem())
	case *types.Signature:
		if e := seqElem(u); e != nil {
			return c.agree(elem, e)
		}
	}
	ms := types.NewMethodSet(t)
	if _, isPtr := t.(*types.Pointer); !isPtr && !types.IsInterface(t) {
		ms = types.NewMethodSet(types.NewPointer(t))
	}
	for method := range ms.Methods() {
		sig, ok := method.Obj().Type().(*types.Signature)
		if !ok || !method.Obj().Exported() || sig.Results().Len() != 1 {
			continue
		}
		if p := sig.Params(); p.Len() > 1 || (p.Len() == 1 && !isContext(p.At(0).Type())) {
			continue
		}
		if fn, ok := sig.Results().At(0).Type().Underlying().(*types.Signature); ok {
			if e := seqElem(fn); e != nil {
				return c.agree(elem, e) // T14m
			}
		}
	}
	return unknownV("T14: upstream Iterable<%s> against Go type %s", elem, typeLabel(t))
}

// seqElem returns E when sig is func(yield func(E) bool), the shape of iter.Seq[E].
func seqElem(sig *types.Signature) types.Type {
	if sig.Params().Len() != 1 || sig.Results().Len() != 0 {
		return nil
	}
	yield, ok := sig.Params().At(0).Type().Underlying().(*types.Signature)
	if !ok || yield.Params().Len() != 1 || yield.Results().Len() != 1 || basicInfo(yield.Results().At(0).Type())&types.IsBoolean == 0 {
		return nil
	}
	return yield.Params().At(0).Type()
}

func (c *checker) mapOf(args string, t types.Type) verdict {
	m, ok := t.Underlying().(*types.Map)
	if !ok {
		value := orderedPairValue(t)
		if value == nil {
			value = orderedObjectValue(t)
		}
		if value != nil {
			// T6o: a TypeScript object keeps its keys in insertion order and a Go map does not, so an order that callers observe is a slice of
			// (key, value) pairs.
			if parts := splitTop(args, ","); len(parts) == 2 {
				return c.agree(parts[1], value)
			}
			return unknownV("T6o: record arguments %q are not <key, value>", args)
		}
		if value := orderedRecordValue(t); value != nil {
			// T6r: an insertion-ordered object type (keyed lookup plus an All iterator over (key, value) pairs in insertion order) is a
			// TypeScript object with its own-key order, e.g. chord/delta.JsonObject.
			if parts := splitTop(args, ","); len(parts) == 2 {
				return c.agree(parts[1], value)
			}
			return unknownV("T6r: record arguments %q are not <key, value>", args)
		}
		if types.IsInterface(t) {
			return unknownV("T6: record against %s", typeLabel(t))
		}
		if st, isStruct := t.Underlying().(*types.Struct); isStruct && st.NumFields() == 0 {
			if parts := splitTop(args, ","); len(parts) == 2 && strings.TrimSpace(parts[1]) == "never" {
				return yesV() // T6n: Record<string, never> admits no key, the empty object (durable generation.ts:49 GenerationInput), which is the Go struct{}
			}
		}
		return noV("T6: upstream record, Go type is %s", typeLabel(t))
	}
	if parts := splitTop(args, ","); len(parts) == 2 {
		return c.agree(parts[1], m.Elem())
	}
	return yesV()
}

// orderedRecordValue returns V when t is a struct, or a pointer to one, whose method set has All() func(yield func(string, V) bool) and
// Get(string) (V, bool): an ordered object keyed by string. Either method alone is not a record: All alone is an iterable and Get alone
// a lookup.
func orderedRecordValue(t types.Type) types.Type {
	ptr, ok := types.Unalias(t).(*types.Pointer)
	if !ok {
		ptr = types.NewPointer(t) // the caller may already have looked through the pointer
	}
	if _, isStruct := ptr.Elem().Underlying().(*types.Struct); !isStruct {
		return nil
	}
	ms := types.NewMethodSet(ptr)
	var all, got types.Type
	for method := range ms.Methods() {
		sig, isSig := method.Obj().Type().(*types.Signature)
		if !isSig {
			continue
		}
		switch method.Obj().Name() {
		case "All":
			if sig.Params().Len() != 0 || sig.Results().Len() != 1 {
				continue
			}
			seq, isSeq := sig.Results().At(0).Type().Underlying().(*types.Signature)
			if !isSeq || seq.Params().Len() != 1 || seq.Results().Len() != 0 {
				continue
			}
			yield, isYield := seq.Params().At(0).Type().Underlying().(*types.Signature)
			if !isYield || yield.Params().Len() != 2 || yield.Results().Len() != 1 || basicInfo(yield.Results().At(0).Type())&types.IsBoolean == 0 {
				continue
			}
			if b, isBasic := yield.Params().At(0).Type().Underlying().(*types.Basic); isBasic && b.Kind() == types.String {
				all = yield.Params().At(1).Type()
			}
		case "Get":
			if sig.Params().Len() != 1 || sig.Results().Len() != 2 || basicInfo(sig.Results().At(1).Type())&types.IsBoolean == 0 {
				continue
			}
			if b, isBasic := sig.Params().At(0).Type().Underlying().(*types.Basic); isBasic && b.Kind() == types.String {
				got = sig.Results().At(0).Type()
			}
		}
	}
	if all == nil || got == nil || !types.Identical(all, got) {
		return nil
	}
	return all
}

// orderedPairValue returns the value type of t when t is a slice of structs with exactly two fields, the first a string key: the Go form of
// an insertion-ordered record. It returns nil otherwise.
func orderedPairValue(t types.Type) types.Type {
	sl, ok := t.Underlying().(*types.Slice)
	if !ok {
		return nil
	}
	st, ok := sl.Elem().Underlying().(*types.Struct)
	if !ok || st.NumFields() != 2 {
		return nil
	}
	if b, ok := st.Field(0).Type().Underlying().(*types.Basic); !ok || b.Kind() != types.String {
		return nil
	}
	return st.Field(1).Type()
}

// orderedObjectValue returns the value type of t when t is an insertion-ordered JSON object type: a (pointer to a) named type whose
// method set has Get(key string) (V, bool), Set(key string, value V) and Keys() []string (chord's JsonObject, which keeps JavaScript's
// property order). It returns nil otherwise.
func orderedObjectValue(t types.Type) types.Type {
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = p.Elem()
	}
	if namedOf(t) == nil {
		return nil
	}
	ms := types.NewMethodSet(types.NewPointer(t))
	sig := func(name string) *types.Signature {
		if sel := ms.Lookup(nil, name); sel != nil {
			if s, ok := sel.Type().(*types.Signature); ok {
				return s
			}
		}
		return nil
	}
	isString := func(v *types.Var) bool {
		b, ok := v.Type().Underlying().(*types.Basic)
		return ok && b.Kind() == types.String
	}
	get, set, keys := sig("Get"), sig("Set"), sig("Keys")
	if get == nil || set == nil || keys == nil || get.Params().Len() != 1 || !isString(get.Params().At(0)) || get.Results().Len() != 2 {
		return nil
	}
	if b, ok := get.Results().At(1).Type().Underlying().(*types.Basic); !ok || b.Kind() != types.Bool {
		return nil
	}
	if set.Params().Len() != 2 || !isString(set.Params().At(0)) || !types.Identical(set.Params().At(1).Type(), get.Results().At(0).Type()) {
		return nil
	}
	if sl, ok := keys.Results().At(0).Type().Underlying().(*types.Slice); keys.Params().Len() != 0 || keys.Results().Len() != 1 || !ok || !isString(types.NewVar(0, nil, "", sl.Elem())) {
		return nil
	}
	return get.Results().At(0).Type()
}

// named applies T9: a named upstream type stands for a Go named type of the same (or documented) name.
func (c *checker) named(name string, t types.Type) verdict {
	short := name
	if i := strings.LastIndex(name, "."); i >= 0 {
		short = name[i+1:]
	}
	if a, ok := t.(*types.Alias); ok && c.sameName(short, a.Obj().Name()) && !isEmptyInterface(t) {
		return yesV() // a same-named alias of `any` (type X = any) carries none of X's shape, so the name alone does not decide it
	}
	if n := namedOf(t); n != nil && c.sameName(short, n.Obj().Name()) {
		return yesV()
	}
	if c.resolve != nil && c.depth < 3 {
		if tn := c.resolve(short); tn != nil && (sameNamedType(t, tn) || types.Identical(tn.Type().Underlying(), t)) {
			return yesV() // T9a: the upstream type resolves, by documented rename or unique shape, to this Go type (or its underlying type)
		}
	}
	if module := c.aliases[extImportKey+c.pkg][short]; module != nil && *module != "" {
		if rep, ok := c.reps[*module+":"+short]; ok && rep == typeLabel(t) {
			return yesV() // T9x: the package imports the name from an external module, whose type has a documented Go representation
		}
	}
	if rep, ok := c.reps[c.pkg+":"+short]; ok && rep == typeLabel(t) {
		return yesV() // T9c: the upstream name has a documented Go representation that is not a named type of its own
	}
	if sealedMember(c.memberType(short, t), t) {
		return yesV() // T9s: Go has no covariant method results; the sealed union interface the Go type implements stands for it
	}
	if erasedGeneric(short, t, c.sameName) {
		return yesV() // T9e: Go methods take no type parameters, so a generic upstream type is passed as its erased Any interface
	}
	if body := c.alias(short); body != nil && c.depth < 3 {
		inner := *c
		inner.depth++
		if v := inner.agree(*body, t); v.ok != unknown || strings.Contains(v.why, "T9b") {
			return v // T9b: an upstream alias stands for its body
		}
	}
	if v, ok := c.structural(short, t); ok {
		return v
	}
	return unknownV("T9: upstream type %s against Go type %s", short, typeLabel(t))
}

// taggedSingleMember returns the one data member of an interface whose other member is a required `type` tag of one string literal
// (MessageEndEvent is { type: "message_end"; message: AgentMessage }).
func taggedSingleMember(props []propShape) (propShape, bool) {
	if len(props) != 2 {
		return propShape{}, false
	}
	for i, p := range props {
		if p.Name == "type" && !p.Optional && quotedStringLiteral(strings.TrimSpace(p.Type)) {
			other := props[1-i]
			return other, !other.Optional && len(other.Calls) == 0
		}
	}
	return propShape{}, false
}

// structural applies T9t, TypeScript's structural typing: a Go type of another name stands for an upstream interface or class when it
// has a field or method for every upstream property (optional ones included: a Go caller must be able to read them) and each type
// agrees. A Go basic type has no members, so it cannot stand for an interface with properties.
func (c *checker) structural(name string, t types.Type) (verdict, bool) {
	if c.props == nil || c.depth >= 2 {
		return verdict{}, false
	}
	props := c.props(name)
	if len(props) == 0 {
		return verdict{}, false
	}
	if data, ok := taggedSingleMember(props); ok && len(c.pick) == 0 && len(c.omit) == 0 {
		inner := *c
		inner.depth++
		if v := inner.agree(data.Type, t); v.ok == yes {
			return v, true // T9w: an event whose members are a literal `type` tag and one datum is passed as the datum; the tag is constant
		}
	}
	if _, basic := deref(t).Underlying().(*types.Basic); basic {
		if len(c.pick) == 1 {
			for _, p := range props {
				if c.pick[p.Name] {
					inner := *c
					inner.depth++
					inner.omit, inner.pick = nil, nil
					return inner.agree(p.Type, t), true // T9p: Pick<X, "m"> is the value of its one member
				}
			}
		}
		return noV("T9t: upstream %s has members, Go type %s is a basic type", name, typeLabel(t)), true
	}
	if c.resolve != nil {
		if own := c.resolve(name); own != nil {
			if iface, isIface := own.Type().Underlying().(*types.Interface); isIface && iface.NumMethods() > 0 && (types.Implements(t, iface) || types.Implements(types.NewPointer(t), iface)) {
				return yesV(), true // T9i: a Go type that implements the interface that is the upstream type's own Go form stands for it
			}
		}
	}
	st, ok := deref(t).Underlying().(*types.Struct)
	if !ok {
		return verdict{}, false
	}
	partial, sel := c.partial, c.selection()
	inner := *c
	inner.depth++
	inner.omit, inner.pick, inner.partial = nil, nil, false
	if c.typeParams != nil {
		inner.generics = maps.Clone(c.generics)
		if inner.generics == nil {
			inner.generics = map[string]bool{}
		}
		for _, tp := range c.typeParams(name) {
			inner.generics[tp] = true // T9g: X's members name X's own type parameters
		}
	}
	var vs []verdict
	for _, p := range props {
		if sel.keeps(p.Name) && isSignalType(p.Type) && !c.ctxInScope && structField(st, p.Name, 0) == nil {
			vs = append(vs, unknownV("T12s: AbortSignal member %s.%s outside a call that takes a context.Context", name, p.Name))
			continue
		}
		if isSignalType(p.Type) || !sel.keeps(p.Name) || strings.HasPrefix(p.Name, "[") {
			// An AbortSignal member is the context.Context of the calls that take the value, and a computed member such as
			// [Symbol.asyncIterator] is a JavaScript protocol Go spells as a method or channel; their own rows judge them.
			continue
		}
		if f := structField(st, p.Name, 0); f != nil {
			if partial && !nilable(f.Type()) {
				// Whether the zero value stands for an omitted member depends on how the value is read, not on its shape.
				vs = append(vs, unknownV("T9t: Partial<%s> member %s has a Go field %s of non-nilable type %s", name, p.Name, f.Name(), typeLabel(f.Type())))
				continue
			}
			vs = append(vs, inner.agree(p.Type, f.Type()))
			continue
		}
		m := methodFor(t, p.Name)
		if m == nil {
			return noV("T9t: Go type %s has no field or method for %s.%s", typeLabel(t), name, p.Name), true
		}
		sig := m.Type().(*types.Signature)
		switch {
		case len(p.Calls) > 0:
			vs = append(vs, inner.signature(p.Calls[0], sig))
		case isFuncType(p.Type) && inner.agree(p.Type, sig).ok == yes:
			// T9m: a method member such as `getBashModeBorderColor(): (str: string) => string` (theme.ts:434) is the Go method of the
			// same signature. A data member of function type falls through to the accessor case below.
			vs = append(vs, yesV())
		case sig.Params().Len() == 0 && sig.Results().Len() > 0:
			vs = append(vs, inner.agree(p.Type, sig.Results().At(0).Type()))
		default:
			vs = append(vs, unknownV("T9t: method %s for data property %s.%s", m.Name(), name, p.Name))
		}
	}
	v := all(vs...)
	reviewed := slices.Contains(c.forms[c.pkg+":"+name], typeLabel(deref(t)))
	if v.ok == unknown && reviewed && partial {
		return yesV(), true // a reviewed form whose non-nilable fields' zero values are the defaults Pi applies to omitted members
	}
	if v.ok == unknown {
		return v, false
	}
	if v.ok == yes && !partial && sel.omit == nil && sel.pick == nil && c.resolve != nil && !reviewed {
		if own := c.resolve(name); own != nil && !sameNamedType(t, own) {
			// The upstream type has a Go form of its own, and this API uses another type with the same members: whether that is
			// a faithful choice is not a shape question.
			return unknownV("T9t: Go type %s has every member of %s, whose Go form is %s", typeLabel(t), name, own.Name()), true
		}
	}
	return v, true
}

// funcType applies T7: parameter count, parameter types and result of a function type.
func (c *checker) funcType(up string, t types.Type) verdict {
	sig, ok := t.Underlying().(*types.Signature)
	if !ok {
		sig = singleMethod(t) // T7b: a Go interface with one method is a function type
		if sig == nil {
			sig = identityListener(t) // T7w: a pointer to a listener wrapper stands for the callback it wraps
		}
		if sig == nil {
			return unknownV("T7: function type %q against Go type %s", up, typeLabel(t))
		}
	}
	call, ok := parseFuncType(up)
	if !ok {
		return unknownV("T7: cannot parse function type %q", up)
	}
	return c.signature(call, sig)
}

// parseFuncType reads `(a: A, b?: B) => R` into a call shape.
func parseFuncType(up string) (callShape, bool) { return rules.ParseFuncType(up) }

// env hands the function rules (rules/functions.go) the checker's type agreement and options-bag knowledge.
func (c *checker) env() rules.FuncEnv {
	env := rules.FuncEnv{
		Agree:            func(up string, t types.Type) rules.Verdict { return toRules(c.agree(up, t)) },
		Bag:              c.bagMembers,
		ClosedKey:        c.closedKey,
		ErrorUnionPinned: c.errUnionPinned,
		Alias: func(name string) (string, bool) {
			if b := c.alias(name); b != nil {
				return *b, true
			}
			return "", false
		},
	}
	env.SignalBag = func(name string) bool {
		if name == "Context" && !c.plainContext && c.aliases.invocationContext(c.pkg) {
			return true // S1c
		}
		if c.pkg == "coding-agent" && (name == "ExtensionContext" || name == "ExtensionCommandContext" || name == "ExtensionToolContext") {
			// S1e: the extension context a handler receives as its last argument (and the command and tool contexts that extend it)
			// travels in Go's context.Context (extension.ContextFromContext).
			return true
		}
		return c.bags != nil && c.bags(name)
	}
	return env
}

// closedKey reports whether an upstream parameter type is `keyof X`, a string-literal union, or an alias of either (S6k).
func (c *checker) closedKey(typ string) bool {
	if body := c.alias(typ); identRe.MatchString(typ) && body != nil {
		typ = strings.TrimSpace(*body)
	}
	if keyofRe.MatchString(typ) {
		return true
	}
	_, ok := stringLiteralUnion(typ)
	return ok
}

// signature applies the call rules S1-S9 (rules.Signature) to an upstream call and a Go signature.
func (c *checker) signature(up callShape, sig *types.Signature) verdict {
	if !c.ctxInScope {
		for v := range sig.Params().Variables() {
			if isContext(v.Type()) {
				inner := *c
				inner.ctxInScope = true // T12s: the call carries a context.Context for AbortSignal members of its arguments
				c = &inner
				break
			}
		}
	}
	v := c.registeredSignature(up, sig, fromRules(rules.Signature(c.env(), up, sig)))
	if v.ok == yes || !c.aliases.invocationContext(c.pkg) || c.plainContext {
		return v
	}
	for _, p := range up.Parameters {
		if strings.TrimSpace(p.Type) == "Context" {
			// S1c fallback: the chord Context is the leading context.Context only when that reading makes the signature agree; a Go
			// function that takes a value of its own for it (withAbortSignal(signal, context) is WithAbortSignal(parent, signal)) reads it
			// as an ordinary parameter.
			plain := *c
			plain.plainContext = true
			if w := plain.registeredSignature(up, sig, fromRules(rules.Signature(plain.env(), up, sig))); w.ok == yes {
				return w
			}
			break
		}
	}
	return v
}

// sameNamedType reports whether t is the named type tn, comparing package path and name so that go/packages' variants agree.
func sameNamedType(t types.Type, tn *types.TypeName) bool {
	var obj *types.TypeName
	if a, ok := t.(*types.Alias); ok {
		obj = a.Obj()
		if obj.Pkg() != nil && tn.Pkg() != nil && obj.Pkg().Path() == tn.Pkg().Path() && obj.Name() == tn.Name() {
			return true
		}
	}
	if n := namedOf(t); n != nil {
		obj = n.Obj()
	}
	return obj != nil && obj.Pkg() != nil && tn.Pkg() != nil && obj.Pkg().Path() == tn.Pkg().Path() && obj.Name() == tn.Name()
}

// identityListener returns the callback signature a listener wrapper carries: t is *X for a named struct X with exactly one function
// field, and X's package declares New<X>(f) *X whose only parameter has that field's type. JavaScript removes a listener by the
// function's identity (Set.delete, EventEmitter.off); Go functions are not comparable, so the port registers the callback through
// a pointer that gives it one.
func identityListener(t types.Type) *types.Signature {
	ptr, ok := t.(*types.Pointer)
	if !ok {
		return nil
	}
	n := namedOf(ptr.Elem())
	if n == nil || n.Obj().Pkg() == nil {
		return nil
	}
	st, ok := n.Underlying().(*types.Struct)
	if !ok {
		return nil
	}
	var field *types.Signature
	for f := range st.Fields() {
		if sig, ok := f.Type().(*types.Signature); ok {
			if field != nil {
				return nil
			}
			field = sig
		}
	}
	ctor, ok := n.Obj().Pkg().Scope().Lookup("New" + n.Obj().Name()).(*types.Func)
	if field == nil || !ok {
		return nil
	}
	sig := ctor.Type().(*types.Signature)
	if sig.Params().Len() != 1 || sig.Results().Len() != 1 || !types.Identical(sig.Params().At(0).Type(), field) ||
		!types.Identical(sig.Results().At(0).Type(), t) {
		return nil
	}
	return field
}

// singleMethod returns the signature of the only method of an interface type.
func singleMethod(t types.Type) *types.Signature {
	if it, ok := t.Underlying().(*types.Interface); ok && it.NumMethods() == 1 {
		if sig, ok := it.Method(0).Type().(*types.Signature); ok {
			return sig
		}
	}
	return nil
}

// taggedStruct applies T10d: every property of every member of a union of object types is a field of the Go struct.
// memberNames collects the property names of an upstream object type, and the string literals it gives them, into names and literals:
// an inline object, an interface the ledger lists, and through intersections, unions, aliases and interfaces read from the pinned
// sources (T10d). It fails on any other type.
func (c *checker) memberNames(m string, depth int, names *[]string, literals map[string]map[string]bool) bool {
	text := unparen(stripTypeNoise(m))
	if depth > 4 {
		return false
	}
	for _, op := range []string{"|", "&"} {
		if parts := splitTop(text, op); len(parts) > 1 {
			for _, p := range parts {
				if strings.TrimSpace(p) != "" && !c.memberNames(p, depth+1, names, literals) {
					return false
				}
			}
			return true
		}
	}
	if g := genericRe.FindStringSubmatch(text); g != nil && (g[1] == "Omit" || g[1] == "Pick") {
		args := splitTop(g[2], ",")
		keys, ok := stringLiteralUnion(strings.Join(args[1:], ","))
		var all []string
		if !ok || !c.memberNames(args[0], depth+1, &all, literals) {
			return false
		}
		for _, n := range all {
			if slices.Contains(keys, n) == (g[1] == "Pick") {
				*names = append(*names, n) // T16: the selected members
			}
		}
		return true
	}
	switch {
	case strings.HasPrefix(text, "{"):
		inner := strings.TrimSuffix(strings.TrimPrefix(text, "{"), "}")
		for _, part := range splitTop(strings.ReplaceAll(inner, ";", ","), ",") {
			if name, typ, ok := objectMember(part); ok && typ != "never" {
				*names = append(*names, name)
				if lit := literalTypeRe.FindStringSubmatch(typ); lit != nil {
					if literals[name] == nil {
						literals[name] = map[string]bool{}
					}
					literals[name][lit[1]] = true
				}
			}
		}
		return true
	case c.propNames != nil && len(c.propNames(text)) > 0:
		*names = append(*names, c.propNames(text)...)
		return true
	}
	if body := c.aliases.iface(c.pkg, text); body != nil {
		return c.memberNames(*body, depth+1, names, literals)
	}
	if body := c.alias(text); body != nil {
		return c.memberNames(*body, depth+1, names, literals)
	}
	return false
}

func (c *checker) taggedStruct(members []string, t types.Type, st *types.Struct) (verdict, bool) {
	var names []string
	literals := map[string]map[string]bool{} // member name -> the string literals union members give it
	for _, m := range members {
		if !c.memberNames(m, 0, &names, literals) {
			return verdict{}, false
		}
	}
	sel := c.selection()
	for _, name := range names {
		if !sel.keeps(name) {
			continue
		}
		if structField(st, name, 0) == nil && (len(literals[name]) > 0 || methodFor(t, name) == nil) && !c.writesEvery(t, name, literals[name]) {
			return noV("T10d: Go struct has no field for union member property %s", name), true
		}
	}
	return yesV(), len(names) > 0
}

// callerKeyedMapRe matches a mapped type `{ [P in S] : V }` or `{ [P in S["k"]]: V }`, capturing P, S and V.
var callerKeyedMapRe = regexp.MustCompile(`^\{\s*(?:readonly\s+)?\[\s*(\w+)\s+in\s+(\w+)(?:\["\w+"\])?\s*\]\s*:\s*(.+?)\s*[;,]?\s*\}$`)

// callerKeyedMap applies T11k: a mapped type whose keys come from a type parameter S (S itself or a string member S["k"]) has keys
// the caller chooses, so it is a Go map from string to the value type, read with P and S as type parameters. Its exhaustiveness
// over S is compile-time only.
func (c *checker) callerKeyedMap(up string, t types.Type) (verdict, bool) {
	m := callerKeyedMapRe.FindStringSubmatch(stripTypeNoise(up))
	if m == nil || !c.generics[m[2]] {
		return verdict{}, false
	}
	mt, ok := t.Underlying().(*types.Map)
	if !ok || basicInfo(mt.Key())&types.IsString == 0 {
		return verdict{}, false
	}
	inner := *c
	inner.generics = maps.Clone(c.generics)
	inner.generics[m[1]] = true
	return inner.agree(m[3], mt.Elem()), true
}

// nonEmptyArrayRe matches the tuple `[T, ...T[]]`: an array with at least one element.
var nonEmptyArrayRe = regexp.MustCompile(`^\[\s*(.+?)\s*,\s*\.\.\.\s*(.+?)\[\]\s*\]$`)

// nonEmptyArrayElem reports T5n: `[T, ...T[]]` is a Go slice of T. The at-least-one guarantee is a static property that a Go slice
// cannot carry; the element type is what has to agree.
func nonEmptyArrayElem(up string) (string, bool) {
	m := nonEmptyArrayRe.FindStringSubmatch(strings.TrimSpace(up))
	if m == nil || strings.TrimSpace(m[1]) != strings.TrimSpace(m[2]) {
		return "", false
	}
	return strings.TrimSpace(m[1]), true
}

// syncOrAsync drops the Promise members of a union whose resolved members all appear unwrapped in the union (`T | undefined |
// Promise<T | undefined>`): the caller awaits either form, so the Go result is the value. It reports false when there is no such
// Promise member or one resolves to a member the union lacks (a union of Promises alone has no unwrapped member).
func syncOrAsync(up string) (string, bool) {
	parts := splitTop(up, "|")
	plain := map[string]bool{}
	var kept, promised []string
	for _, p := range parts {
		p = unparen(strings.TrimSpace(p))
		if g := genericRe.FindStringSubmatch(p); g != nil && g[1] == "Promise" {
			promised = append(promised, g[2])
			continue
		}
		plain[p] = true
		kept = append(kept, p)
	}
	if len(promised) == 0 {
		return "", false
	}
	for _, inner := range promised {
		for _, m := range splitTop(inner, "|") {
			if !plain[unparen(strings.TrimSpace(m))] {
				return "", false
			}
		}
	}
	return strings.Join(kept, " | "), true
}

// erasedGeneric reports whether the Go type is the interface Any<X> of the upstream generic type X: its package declares a generic
// type X (or XOf) that implements it when instantiated with its constraints, so every typed value converts to the erased form.
func erasedGeneric(up string, t types.Type, sameName func(up, goName string) bool) bool {
	n := namedOf(t)
	if n == nil || n.Obj().Pkg() == nil || !types.IsInterface(n) {
		return false
	}
	short, ok := strings.CutPrefix(n.Obj().Name(), "Any")
	if !ok || !sameName(up, short) {
		return false
	}
	iface := n.Underlying().(*types.Interface)
	if iface.NumMethods() == 0 {
		return false // an Any<X> with no methods is `any`: it erases every type, not the generic X
	}
	scope := n.Obj().Pkg().Scope()
	for _, name := range []string{short, short + "Of"} {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		g, ok := tn.Type().(*types.Named)
		if !ok || g.TypeParams().Len() == 0 {
			continue
		}
		args := make([]types.Type, g.TypeParams().Len())
		for i := range args {
			args[i] = g.TypeParams().At(i).Constraint()
		}
		inst, err := types.Instantiate(nil, g, args, true)
		if err == nil && (types.Implements(inst, iface) || types.Implements(types.NewPointer(inst), iface)) {
			return true
		}
	}
	return false
}

// memberType returns the Go type of upstream name: the resolved type, or else, for an upstream type with no ledger row (an
// unexported declaration), the type of the same name in the package of the Go type t.
func (c *checker) memberType(name string, t types.Type) *types.TypeName {
	if c.resolve != nil {
		if tn := c.resolve(name); tn != nil {
			return tn
		}
	}
	if n := namedOf(t); n != nil && n.Obj().Pkg() != nil {
		if tn, ok := n.Obj().Pkg().Scope().Lookup(name).(*types.TypeName); ok {
			return tn
		}
	}
	return nil
}

// sealedMember reports whether the Go type t is a sealed union interface (every method unexported, so only its package implements
// it) that tn, the Go type of an upstream union member, implements by value or pointer.
func sealedMember(tn *types.TypeName, t types.Type) bool {
	n := namedOf(t)
	if tn == nil || n == nil || !types.IsInterface(n) {
		return false
	}
	iface := n.Underlying().(*types.Interface)
	if iface.NumMethods() == 0 {
		return false
	}
	for m := range iface.Methods() {
		if m.Exported() {
			return false
		}
	}
	return types.Implements(tn.Type(), iface) || types.Implements(types.NewPointer(tn.Type()), iface)
}

// keyTable applies T12q: an object type whose every key is a quoted name no Go field can spell (`"tui.editor.cursorUp"`) is a Go
// map from string to a type every member type agrees with. It reports false when any key could be a field name.
func (c *checker) keyTable(up string, elem types.Type) (verdict, bool) {
	inner := strings.TrimSuffix(strings.TrimPrefix(stripTypeNoise(up), "{"), "}")
	var vs []verdict
	for _, part := range splitTop(strings.ReplaceAll(inner, ";", ","), ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		name, typ, ok := objectMember(part)
		key, err := strconv.Unquote(name)
		if !ok || err != nil || token.IsIdentifier(key) {
			return verdict{}, false
		}
		vs = append(vs, c.agree(typ, elem))
	}
	if len(vs) == 0 {
		return verdict{}, false
	}
	return all(vs...), true
}

// overloadSet applies T12o: an upstream object literal made only of call signatures, `{ (): A; (id: string): B | undefined }`, is the
// overload set of one function. Go has no overloading, so the one Go function is variadic: the parameters before the variadic one are
// every overload's leading parameters, the variadic element takes each overload's remaining ones, and its single result agrees with every
// overload's result. It reports ok false for any other object literal.
func (c *checker) overloadSet(up string, sig *types.Signature) (verdict, bool) {
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(stripTypeNoise(up), "{"), "}"))
	if !strings.HasPrefix(inner, "(") {
		return verdict{}, false
	}
	var calls []callShape
	for _, part := range splitTop(strings.ReplaceAll(inner, ";", ","), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		depth, end := 0, -1
		for i := 0; i < len(part) && end < 0; i++ {
			switch part[i] {
			case '(':
				depth++
			case ')':
				if depth--; depth == 0 {
					end = i
				}
			}
		}
		if !strings.HasPrefix(part, "(") || end < 0 || !strings.HasPrefix(strings.TrimSpace(part[end+1:]), ":") {
			return verdict{}, false
		}
		call, ok := parseFuncType(part[:end+1] + " =>" + strings.TrimSpace(part[end+1:])[1:])
		if !ok {
			return unknownV("T12o: cannot parse overload %q", truncate(part, 50)), true
		}
		calls = append(calls, call)
	}
	if len(calls) < 2 {
		return verdict{}, false
	}
	if !sig.Variadic() || sig.Results().Len() != 1 {
		return unknownV("T12o: overload set %q against Go function %s, which is not variadic with one result", truncate(up, 50), typeLabel(sig)), true
	}
	fixed := sig.Params().Len() - 1
	elem, ok := sig.Params().At(fixed).Type().(*types.Slice)
	if !ok {
		return unknownV("T12o: variadic parameter of %s is not a slice", typeLabel(sig)), true
	}
	var vs []verdict
	for _, call := range calls {
		if len(call.Parameters) < fixed {
			return noV("T12o: overload %q has fewer parameters than the Go function's %d fixed ones", truncate(call.Returns, 30), fixed), true
		}
		for i, p := range call.Parameters {
			want := elem.Elem()
			if i < fixed {
				want = sig.Params().At(i).Type()
			}
			vs = append(vs, c.agree(p.Type, want))
		}
		vs = append(vs, c.agree(call.Returns, sig.Results().At(0).Type()))
	}
	return all(vs...), true
}
