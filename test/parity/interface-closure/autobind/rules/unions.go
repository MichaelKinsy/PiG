package rules

import (
	"go/constant"
	"go/token"
	"go/types"
	"reflect"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// Union rules (family 3).
//
//	U1  a union of string or number literals is a named Go type whose underlying type is string or numeric, with one Go constant of that
//	    type for every literal; a bare string is stringly typed and refutes a closed union; extra constants are a note, not a refutation
//	U2  an open union (a string member or `string & {}`) is any Go string type
//	U3  a discriminated union of object types has one discriminator: a property present in every member whose type is one distinct
//	    string literal per member
//	U4  a discriminated union is a Go interface with at least one method plus one concrete struct per member, whose discriminator value
//	    a TagResolver reads from the code and which equals the member's literal
//	U5  a discriminated union is a tagged Go struct: a field for the discriminator whose type has a constant for every literal (U1),
//	    and a field for every property of every member
//	U6  the discriminator value of a Go struct is read from a method named after the discriminator that returns a constant, or from the
//	    string constants of its MarshalJSON together with the discriminator key (ASTTags)
//	U7  when no implementer's discriminator value is readable, the implementer names encode it: one prefix and suffix shared by every
//	    member (AgentStartEvent for "agent_start", InputEventResultContinue for "continue") name exactly one implementer per member

// LiteralUnion is a union of string or number literals, possibly open.
type LiteralUnion struct {
	// Strings are the string literals in declaration order.
	Strings []string
	// Numbers are the number literals in declaration order, as written.
	Numbers []string
	// Open is true when the union has a string member or `string & {}`.
	Open bool
}

// ParseLiteralUnion parses a union of literals. alias resolves a named alias to its body (nil when unknown); undefined and null
// members are skipped. ok is false when a member is neither a literal, an open string nor an alias of those.
func ParseLiteralUnion(up string, alias func(name string) *string) (LiteralUnion, bool) {
	var u LiteralUnion
	if !u.add(up, alias, 0) {
		return LiteralUnion{}, false
	}
	return u, len(u.Strings)+len(u.Numbers) > 0 || u.Open
}

func (u *LiteralUnion) add(up string, alias func(string) *string, depth int) bool {
	for _, m := range ouSplitTop(up, "|") {
		m = ouStripParens(m)
		switch {
		case m == "undefined" || m == "null" || m == "":
		case m == "string" || m == "string & {}" || m == "{} & string":
			u.Open = true
		case ouStringLit.MatchString(m):
			u.Strings = append(u.Strings, m[1:len(m)-1])
		case ouNumberLit.MatchString(m):
			u.Numbers = append(u.Numbers, m)
		case ouIdentRe.MatchString(m) && alias != nil && depth < 4:
			body := alias(m)
			if body == nil || !u.add(*body, alias, depth+1) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// StringUnionOptions tunes CheckLiteralUnion.
type StringUnionOptions struct {
	// AllowBareString accepts a bare Go string for a closed union. It is false by default: Go would accept any value where upstream
	// accepts only the literals.
	AllowBareString bool
}

// ConstsOf returns the constants of type t declared in the package that declares t, in name order. A type without a package (a
// predeclared type) has none.
func ConstsOf(t types.Type) []*types.Const {
	var obj *types.TypeName
	switch x := t.(type) {
	case *types.Alias:
		obj = x.Obj()
	case *types.Named:
		obj = x.Obj()
	}
	if obj == nil || obj.Pkg() == nil {
		return nil
	}
	var out []*types.Const
	scope := obj.Pkg().Scope()
	for _, name := range scope.Names() {
		if c, ok := scope.Lookup(name).(*types.Const); ok && types.Identical(c.Type(), t) {
			out = append(out, c)
		}
	}
	return out
}

// CheckLiteralUnion applies U1 and U2: t is the Go type that stands for the union and consts are its constants (see ConstsOf).
func CheckLiteralUnion(u LiteralUnion, t types.Type, consts []*types.Const, opts StringUnionOptions) Finding {
	t = types.Unalias(t)
	rule := "U1"
	if u.Open {
		rule = "U2"
	}
	basic, isBasic := t.Underlying().(*types.Basic)
	if !isBasic {
		return fNo(rule, "the Go type %s is not a string or number type", t)
	}
	isString := basic.Info()&types.IsString != 0
	isNumber := basic.Info()&types.IsNumeric != 0
	_, named := t.(*types.Named)
	switch {
	case len(u.Strings) > 0 && len(u.Numbers) > 0:
		return fUnknown("U1", "the union mixes string and number literals")
	case len(u.Numbers) > 0 && !isNumber:
		return fNo("U1", "the upstream union is numeric, the Go type is %s", t)
	case len(u.Strings) > 0 && !isString:
		return fNo("U1", "the upstream union is a union of strings, the Go type is %s", t)
	case u.Open && !isString:
		return fNo("U2", "the upstream union is an open string union, the Go type is %s", t)
	case u.Open && len(u.Numbers) == 0:
		return fYes("U2", "an open string union is any Go string type")
	}
	if !named {
		if opts.AllowBareString && isString {
			return fYes("U1", "bare Go string accepted for a closed union by option")
		}
		return fNo("U1", "a closed union of literals is the bare Go type %s, which accepts every value", t)
	}
	have := map[string]bool{}
	for _, c := range consts {
		have[c.Val().ExactString()] = true
	}
	var missing []string
	want := map[string]bool{}
	for _, l := range u.Strings {
		key := constant.MakeString(l).ExactString()
		want[key] = true
		if !have[key] {
			missing = append(missing, `"`+l+`"`)
		}
	}
	for _, l := range u.Numbers {
		v := constant.MakeFromLiteral(l, token.INT, 0)
		if v.Kind() == constant.Unknown {
			v = constant.MakeFromLiteral(l, token.FLOAT, 0)
		}
		found := false
		for _, c := range consts {
			if constant.Compare(c.Val(), token.EQL, v) {
				found = true
			}
		}
		want[v.ExactString()] = true
		if !found {
			missing = append(missing, l)
		}
	}
	if len(missing) > 0 {
		return fNo("U1", "the Go type %s has no constant for %s", t, strings.Join(missing, ", "))
	}
	v := fYes("U1", "every literal has a constant of %s", t)
	for _, c := range consts {
		if !want[c.Val().ExactString()] && (len(u.Numbers) == 0 || len(u.Strings) > 0) {
			v.Notes = append(v.Notes, "Go constant "+c.Name()+" has no upstream literal")
		}
	}
	return v
}

// UnionProp is a property of an upstream object type.
type UnionProp struct {
	Name     string
	Type     string
	Optional bool
}

// UnionMember is one member of a discriminated union.
type UnionMember struct {
	// Name is the upstream name of a named member; empty for an inline object type.
	Name string
	// Tag is the discriminator literal of the member.
	Tag   string
	Props []UnionProp
}

// DiscriminatedUnion is a union of object types with one discriminator property.
type DiscriminatedUnion struct {
	Discriminator string
	Members       []UnionMember
}

// discriminatorPreference orders the conventional discriminator names; any other candidate sorts after them by name.
var discriminatorPreference = []string{"type", "kind", "role", "tag", "_tag", "event", "op", "name"}

// ParseInlineProps parses the properties of an inline object type such as `{ type: "text"; text: string; id?: string }`.
func ParseInlineProps(obj string) ([]UnionProp, bool) {
	obj = strings.TrimSpace(obj)
	if !strings.HasPrefix(obj, "{") || !strings.HasSuffix(obj, "}") {
		return nil, false
	}
	var props []UnionProp
	for _, part := range ouSplitTop(strings.ReplaceAll(obj[1:len(obj)-1], ";", ","), ",") {
		if part == "" {
			continue
		}
		name, typ, ok := strings.Cut(part, ":")
		if !ok {
			return nil, false
		}
		name = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "readonly "))
		p := UnionProp{Type: strings.TrimSpace(typ)}
		if n, found := strings.CutSuffix(name, "?"); found {
			name, p.Optional = n, true
		}
		p.Name = strings.Trim(name, `"'`)
		props = append(props, p)
	}
	return props, len(props) > 0
}

// ParseDiscriminated applies U3. members are the members of the union; lookup returns the properties of a named object type (nil
// when unknown). The verdict is Yes with the discriminator in Why, or Unknown when the union is not discriminated.
func ParseDiscriminated(members []string, lookup func(name string) []UnionProp) (DiscriminatedUnion, Finding) {
	var parsed []UnionMember
	for _, m := range members {
		m = ouStripParens(m)
		if m == "undefined" || m == "null" {
			continue
		}
		var props []UnionProp
		var ok bool
		var name string
		if strings.HasPrefix(m, "{") {
			props, ok = ParseInlineProps(m)
		} else if ouIdentRe.MatchString(m) && lookup != nil {
			name = m
			props = lookup(m)
			ok = len(props) > 0
		}
		if !ok {
			return DiscriminatedUnion{}, fUnknown("U3", "union member %q is not an object type with known properties", m)
		}
		parsed = append(parsed, UnionMember{Name: name, Props: props})
	}
	if len(parsed) < 2 {
		return DiscriminatedUnion{}, fUnknown("U3", "a union needs at least two object members")
	}
	var candidates []string
	for _, p := range parsed[0].Props {
		if _, ok := tagsOf(parsed, p.Name); ok {
			candidates = append(candidates, p.Name)
		}
	}
	if len(candidates) == 0 {
		return DiscriminatedUnion{}, fUnknown("U3", "no property is a distinct string literal in every member")
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		pi, pj := slices.Index(discriminatorPreference, candidates[i]), slices.Index(discriminatorPreference, candidates[j])
		switch {
		case pi >= 0 && pj >= 0:
			return pi < pj
		case pi >= 0:
			return true
		case pj >= 0:
			return false
		}
		return candidates[i] < candidates[j]
	})
	d := DiscriminatedUnion{Discriminator: candidates[0], Members: parsed}
	tags, _ := tagsOf(parsed, d.Discriminator)
	for i := range d.Members {
		d.Members[i].Tag = tags[i]
	}
	v := fYes("U3", "discriminator %q", d.Discriminator)
	if len(candidates) > 1 {
		v.Notes = append(v.Notes, "other discriminator candidates: "+strings.Join(candidates[1:], ", "))
	}
	return d, v
}

// tagsOf returns the literal of property name in every member when each member declares it as one string literal and all differ.
func tagsOf(members []UnionMember, name string) ([]string, bool) {
	seen := map[string]bool{}
	var tags []string
	for _, m := range members {
		found := false
		for _, p := range m.Props {
			if p.Name != name || p.Optional {
				continue
			}
			typ := ouStripParens(p.Type)
			if !ouStringLit.MatchString(typ) || seen[typ] {
				return nil, false
			}
			seen[typ] = true
			tags = append(tags, typ[1:len(typ)-1])
			found = true
		}
		if !found {
			return nil, false
		}
	}
	return tags, true
}

// TagResolver reads the discriminator value of a concrete Go type from the code.
type TagResolver interface {
	// Tag returns the value that impl carries for the discriminator property and the evidence it was read from.
	Tag(impl *types.Named, discriminator string) (value, evidence string, ok bool)
}

// UnionOptions tunes the Go-side union rules.
type UnionOptions struct {
	// Resolver reads the discriminator of each implementer (U4). Without it a sealed interface is Unknown.
	Resolver TagResolver
	StringUnionOptions
}

// Implementers returns the struct types of the packages that implement the interface iface, by value or by pointer, in name order.
func Implementers(iface *types.Interface, pkgs ...*types.Package) []*types.Named {
	var out []*types.Named
	for _, pkg := range pkgs {
		scope := pkg.Scope()
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || tn.IsAlias() {
				continue
			}
			n, ok := tn.Type().(*types.Named)
			if !ok || types.IsInterface(n) {
				continue
			}
			if _, ok := n.Underlying().(*types.Struct); !ok {
				continue
			}
			if types.Implements(n, iface) || types.Implements(types.NewPointer(n), iface) {
				out = append(out, n)
			}
		}
	}
	return out
}

// CheckSealedInterface applies U4: t is the Go interface for the union and impls its implementers (see Implementers).
func CheckSealedInterface(u DiscriminatedUnion, t types.Type, impls []*types.Named, opts UnionOptions) Finding {
	iface, ok := types.Unalias(t).Underlying().(*types.Interface)
	if !ok || iface.NumMethods() == 0 {
		return fUnknown("U4", "the Go type %s is not an interface with methods, so it does not say which members it holds", t)
	}
	if opts.Resolver == nil {
		return fUnknown("U4", "no TagResolver reads the discriminator of the implementers")
	}
	byTag := map[string][]*types.Named{}
	var notes []string
	for _, impl := range impls {
		val, _, ok := opts.Resolver.Tag(impl, u.Discriminator)
		if !ok {
			notes = append(notes, "no discriminator value readable for "+impl.Obj().Name())
			continue
		}
		byTag[val] = append(byTag[val], impl)
	}
	if len(byTag) == 0 {
		if prefix, suffix, ok := nameAffixes(u, impls); ok {
			v := fYes("U7", "implementer names encode the %q discriminator as %s<tag>%s", u.Discriminator, prefix, suffix)
			v.Notes = notes
			return v
		}
	}
	var missing []string
	for _, m := range u.Members {
		switch len(byTag[m.Tag]) {
		case 0:
			missing = append(missing, `"`+m.Tag+`"`)
		case 1:
		default:
			names := make([]string, len(byTag[m.Tag]))
			for i, n := range byTag[m.Tag] {
				names[i] = n.Obj().Name()
			}
			return fNo("U4", "several Go types carry the discriminator %q: %s", m.Tag, strings.Join(names, ", "))
		}
	}
	if len(missing) > 0 {
		if len(notes) > 0 {
			v := fUnknown("U4", "no Go type with discriminator %s; %s", strings.Join(missing, ", "), strings.Join(notes, "; "))
			v.Notes = notes
			return v
		}
		return fNo("U4", "no implementer of %s carries the discriminator %s", t, strings.Join(missing, ", "))
	}
	v := fYes("U4", "one Go struct per member carries the %q discriminator", u.Discriminator)
	v.Notes = notes
	for tag, ns := range byTag {
		if !slices.ContainsFunc(u.Members, func(m UnionMember) bool { return m.Tag == tag }) {
			v.Notes = append(v.Notes, "implementer "+ns[0].Obj().Name()+" has discriminator "+`"`+tag+`"`+" outside the union")
		}
	}
	sort.Strings(v.Notes)
	return v
}

// CheckTaggedStruct applies U5: st is the Go struct that stands for the whole union and consts the constants of its discriminator
// field's type (see ConstsOf).
func CheckTaggedStruct(u DiscriminatedUnion, st *types.Struct, opts StringUnionOptions) Finding {
	field := findField(st, u.Discriminator)
	if field < 0 {
		return fNo("U5", "the Go struct has no field for the discriminator %q", u.Discriminator)
	}
	var lits []string
	var props []string
	seen := map[string]bool{}
	for _, m := range u.Members {
		lits = append(lits, m.Tag)
		for _, p := range m.Props {
			if p.Name != u.Discriminator && !seen[p.Name] {
				seen[p.Name] = true
				props = append(props, p.Name)
			}
		}
	}
	ft := st.Field(field).Type()
	v := CheckLiteralUnion(LiteralUnion{Strings: lits}, ft, ConstsOf(types.Unalias(ft)), opts)
	if v.Result != Yes {
		v.Why = "discriminator field " + st.Field(field).Name() + ": " + v.Why
		v.Rule = "U5"
		return v
	}
	for _, p := range props {
		if findField(st, p) < 0 {
			return fNo("U5", "the Go struct has no field for the property %q of a union member", p)
		}
	}
	out := fYes("U5", "tagged struct: discriminator field %s and a field for each of %d properties", st.Field(field).Name(), len(props))
	out.Notes = v.Notes
	return out
}

// findField returns the index of the field of st that stands for the property name: the json tag name, or the name folded.
func findField(st *types.Struct, name string) int {
	for i := 0; i < st.NumFields(); i++ {
		tag, _, _ := strings.Cut(reflect.StructTag(st.Tag(i)).Get("json"), ",")
		if tag == name || ouFoldName(st.Field(i).Name()) == ouFoldName(name) {
			return i
		}
	}
	return -1
}

// ouFoldName lower-cases a name and drops everything but letters and digits, so Url, URL and url compare equal.
// nameAffixes finds the one folded prefix and suffix with which every member's folded tag names an implementer (U7). Implementer
// names are unique, so each member names exactly one.
func nameAffixes(u DiscriminatedUnion, impls []*types.Named) (prefix, suffix string, ok bool) {
	count := map[[2]string]int{}
	for _, m := range u.Members {
		seen := map[[2]string]bool{}
		tag := ouFoldName(m.Tag)
		for _, impl := range impls {
			name := ouFoldName(impl.Obj().Name())
			if before, after, ok := strings.Cut(name, tag); ok {
				seen[[2]string{before, after}] = true
			}
		}
		for a := range seen {
			count[a]++
		}
	}
	var found [][2]string
	for a, n := range count {
		if n == len(u.Members) {
			found = append(found, a)
		}
	}
	if len(found) != 1 {
		return "", "", false
	}
	return found[0][0], found[0][1], true
}

func ouFoldName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// literalUnionType is rule U1t: an upstream union of string or number literals agrees with a Go type when CheckLiteralUnion proves it
// (a named type with a constant for every literal) or refutes it (a missing constant, a wrong kind of type, a bare string). It
// stays silent for any other upstream type and where CheckLiteralUnion cannot decide.
type literalUnionType struct{}

func (literalUnionType) Name() string { return "U1t" }

func (literalUnionType) Type(env Env, up string, t types.Type) (Verdict, bool) {
	u, ok := ParseLiteralUnion(up, func(name string) *string {
		if body, ok := env.AliasBody(name); ok {
			return &body
		}
		return nil
	})
	if !ok || len(u.Strings)+len(u.Numbers) == 0 {
		return Verdict{}, false
	}
	f := CheckLiteralUnion(u, t, ConstsOf(types.Unalias(t)), StringUnionOptions{})
	if f.Result == Unknown {
		return Verdict{}, false
	}
	return f.Verdict(), true
}

func init() { RegisterType(Unions, literalUnionType{}) }
