package main

import (
	"go/types"
	"sort"
	"strings"
	"unicode"

	"github.com/MichaelKinsy/PiG/test/parity/interface-closure/autobind/rules"
)

// memberShape judges one Go member against an upstream property: the type verdict of a data property or accessor, and the signature
// that its call overloads are judged against.
func (d *detector) memberShape(prop *upstreamEntry, m *sym) (verdict, *types.Signature) {
	chk := d.checker(prop)
	isFunc := len(prop.Shape.Calls) > 0 || optionalFuncType(prop.Shape.Type)
	switch obj := m.Obj.(type) {
	case *types.Func:
		sig := obj.Type().(*types.Signature)
		if sig.Recv() == nil && isFunc {
			sig = withoutReceiverParam(sig, prop.ParentID) // G1
		}
		if setter := interfaceSetterParam(obj, prop); setter != nil {
			// M2s: a Go interface carries no fields (and a class keeps its flag private), so the host assigns an upstream data property (focused, onSubmit) through the one-parameter
			// method Set<Name>; its parameter is the property's type.
			if isFunc {
				if s, ok := setter.Underlying().(*types.Signature); ok {
					return yesV(), s
				}
			} else {
				return chk.agree(prop.Shape.Type, setter), nil
			}
		}
		if !isFunc {
			if sig.Recv() != nil {
				chk.recv = sig.Recv().Type()
			}
			return d.reviewedVerdict(prop.ID, m.Target(), accessorVerdict(chk, prop.Shape.Type, sig)), sig // M2; a reviewed exception also closes a getter
		}
		if cb := callbackGetter(obj, prop); cb != nil {
			return yesV(), cb // M2f
		}
		return yesV(), sig
	case *types.Var:
		if isFunc {
			if s, ok := obj.Type().Underlying().(*types.Signature); ok {
				return yesV(), s
			}
			if s := singleMethod(obj.Type()); s != nil {
				return yesV(), s // M3b: a field of a one-method interface type is a function
			}
			return noV("M3: upstream property is a function, Go field is %s", typeLabel(obj.Type())), nil
		}
		if v, ok := lazySibling(chk, prop.Shape.Type, m, obj); ok {
			return v, nil
		}
		v := chk.agree(prop.Shape.Type, obj.Type())
		if v.ok != yes && isEmptyInterface(obj.Type()) && literalUnion(prop.Shape.Type, chk.alias) && d.sharedOwner(m) {
			v = yesV() // T10m
		}
		return d.reviewedVerdict(prop.ID, m.Target(), v), nil
	}
	return unknownV("member %s is neither field nor method", m.Name), nil
}

// unparen removes one pair of parentheses that encloses the whole of a type.
func unparen(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "(") && matchingParen(s) {
		return strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

// lazySibling applies T10v: a union of values and functions that produce them (`string[] | (() => Promise<string[]>)`) is a Go field
// for the values and a sibling field named <Field>Func for the functions (env ConnectionOptions.Command/CommandFunc).
func lazySibling(chk *checker, up string, m *sym, field *types.Var) (verdict, bool) {
	var values, funcs []string
	for _, part := range splitTop(up, "|") {
		switch part = unparen(part); {
		case isFuncType(part):
			funcs = append(funcs, part)
		default:
			values = append(values, part)
		}
	}
	owner, ok := field.Pkg().Scope().Lookup(m.Owner).(*types.TypeName)
	if !ok {
		return verdict{}, false
	}
	st, ok := owner.Type().Underlying().(*types.Struct)
	if !ok {
		return verdict{}, false
	}
	sibling := structField(st, m.Name+"Func", 0)
	if sibling == nil {
		return verdict{}, false
	}
	vs := []verdict{chk.agree(strings.Join(values, " | "), field.Type())}
	for _, f := range funcs {
		vs = append(vs, chk.agree(f, sibling.Type()))
	}
	return all(vs...), true
}

// callVerdict judges one call overload row against a Go signature. An overload whose parameters are a prefix of a wider overload that
// the Go signature satisfies is covered by it (S7: overloads collapse into optional parameters or a variadic tail).
func (d *detector) callVerdict(row *upstreamEntry, sig *types.Signature, errUnionPinned bool) verdict {
	chk := d.checker(row)
	chk.errUnionPinned = errUnionPinned
	own := callShape{TypeParameters: row.Shape.TypeParameters, Parameters: row.Shape.Parameters, Returns: row.Shape.Returns}
	v := chk.signature(own, sig)
	if v.ok == yes {
		return v
	}
	for _, other := range d.l.callRows(row.ParentID) {
		if other.ID == row.ID {
			continue
		}
		wide := callShape{Parameters: other.Shape.Parameters, Returns: other.Shape.Returns}
		if prefixOverload(own, wide) && d.checker(other).signature(wide, sig).ok == yes {
			return yesV()
		}
		if sig.Variadic() && rules.VariadicSpan(d.checker(other).env(), callShape{Parameters: other.Shape.Parameters, Returns: other.Shape.Returns}, own, sig) {
			return yesV() // S7v
		}
	}
	if w, ok := d.foldedSignature(row, own, sig); ok {
		return w // RP folds (rulings.go): a reviewed fold decides the call
	}
	return v
}

// prefixOverload is S7 (rules.PrefixOverload).
func prefixOverload(a, b callShape) bool { return rules.PrefixOverload(a, b) }

// typeFor resolves an upstream interface, class or alias name to the Go type it stands for: a documented rename, a name rule, or a unique
// shape match, in that order. It returns nil when the name does not resolve (or resolution is already in progress).
func (d *detector) typeFor(pkg, name string) *types.TypeName {
	var rows []*upstreamEntry
	for _, e := range d.l.entries {
		if e.Name == name && e.Role == "" && (e.Kind == "interface" || e.Kind == "class" || e.Kind == "type-alias") {
			rows = append(rows, e)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		pi, pj := parseID(rows[i].ID).Pkg == pkg, parseID(rows[j].ID).Pkg == pkg
		if pi != pj {
			return pi
		}
		return rows[i].ID < rows[j].ID
	})
	for _, e := range rows {
		if tn, ok := d.typeMemo[e.ID]; ok {
			if tn != nil {
				return tn
			}
			continue
		}
		if d.typeBusy[e.ID] {
			continue
		}
		d.typeBusy[e.ID] = true
		var found *types.TypeName
		parts := parseID(e.ID)
		cands := d.candidates(e, parts.Pkg, "type")
		if len(cands) == 0 {
			cands, _ = d.shapeCandidates(e, parts.Pkg)
		}
		if len(cands) > 0 {
			found, _ = cands[0].Obj.(*types.TypeName)
		}
		delete(d.typeBusy, e.ID)
		d.typeMemo[e.ID] = found
		if found != nil {
			return found
		}
	}
	return nil
}

// propNames lists the property names of the upstream interface or class called name.
func (d *detector) propNames(name string) []string {
	for _, e := range d.l.entries {
		if e.Name == name && e.Role == "" && (e.Kind == "interface" || e.Kind == "class") {
			var out []string
			for _, p := range d.l.properties(e.ID) {
				out = append(out, p.Shape.Name)
			}
			return out
		}
	}
	return nil
}

// propShapes lists the properties (name, type, optionality) of the upstream interface or class called name, in the package of the
// row when it declares one, else the first declaration.
func (d *detector) propShapes(pkg, name string) []propShape { return d.propShapesDepth(pkg, name, 0) }

func (d *detector) propShapesDepth(pkg, name string, depth int) []propShape {
	var first []propShape
	for _, e := range d.l.entries {
		if e.Name == name && e.Role == "" && (e.Kind == "interface" || e.Kind == "class") {
			var out []propShape
			for _, p := range d.l.properties(e.ID) {
				out = append(out, propShape{Name: p.Shape.Name, Type: p.Shape.Type, Optional: p.Shape.Optional})
			}
			if parseID(e.ID).Pkg == pkg {
				return out
			}
			if first == nil {
				first = out
			}
		}
	}
	if first == nil {
		return d.sourceProps(pkg, name, depth)
	}
	return first
}

// sourceProps reads the members of an interface the ledger does not list from the pinned sources: its member block and the members
// of the interfaces it extends. A part it cannot read yields no members.
func (d *detector) sourceProps(pkg, name string, depth int) []propShape {
	key := pkg + ":" + name
	if out, ok := d.srcProps[key]; ok && depth == 0 {
		return out
	}
	out := d.readSourceProps(pkg, name, depth)
	if depth == 0 {
		if d.srcProps == nil {
			d.srcProps = map[string][]propShape{}
		}
		d.srcProps[key] = out
	}
	return out
}

func (d *detector) readSourceProps(pkg, name string, depth int) []propShape {
	body := d.aliases.iface(pkg, name)
	if body == nil || depth > 3 {
		return nil
	}
	var out []propShape
	for _, part := range splitTop(stripTypeNoise(*body), "&") {
		part = strings.TrimSpace(part)
		if !strings.HasPrefix(part, "{") {
			inherited := d.propShapesDepth(pkg, part, depth+1)
			if len(inherited) == 0 {
				return nil
			}
			out = append(out, inherited...)
			continue
		}
		for _, member := range splitTop(strings.ReplaceAll(part[1:len(part)-1], ";", ","), ",") {
			n, typ, ok := objectMember(member)
			if !ok {
				continue
			}
			head, _, _ := strings.Cut(strings.TrimSpace(member), ":")
			out = append(out, propShape{Name: strings.Trim(n, `"'`), Type: typ, Optional: strings.HasSuffix(strings.TrimSpace(head), "?") || strings.Contains(head, "?(")})
		}
	}
	return out
}

// ruleShape is the name of the shape-matching rule.
const ruleShape = "N5"

// shapeCandidates finds the Go declarations that match an upstream declaration by shape alone (rule N5): for an interface or class,
// a struct or interface type with a compatible member for each of at least two upstream properties; for a function with distinctive
// parameters, a function whose signature satisfies every call overload. Only a unique match is a candidate; several are reported.
func (d *detector) shapeCandidates(e *upstreamEntry, pkg string) ([]*sym, []string) {
	if d.shapeBusy[e.ID] {
		return nil, nil
	}
	d.shapeBusy[e.ID] = true
	defer delete(d.shapeBusy, e.ID)
	var matches []*sym
	switch e.Kind {
	case "interface", "class":
		props := d.l.properties(e.ID)
		if len(props) < 2 {
			return nil, nil
		}
		for _, s := range d.shapeSyms(pkg) {
			tn, ok := s.Obj.(*types.TypeName)
			if !ok || tn.IsAlias() {
				continue
			}
			switch tn.Type().Underlying().(type) {
			case *types.Struct, *types.Interface:
			default:
				continue
			}
			if d.typeShapeMatches(props, tn, s) {
				c := *s
				c.Tier = ruleShape
				matches = append(matches, &c)
			}
		}
	case "function":
		rows := d.l.callRows(e.ID)
		if len(rows) == 0 {
			return nil, nil
		}
		for _, s := range d.shapeSyms(pkg) {
			sig, ok := s.Obj.Type().(*types.Signature)
			if s.Kind != "func" || !ok || !distinctive(sig) || !sharesWord(e.Name, s.Name) {
				continue
			}
			all := true
			for _, r := range rows {
				if d.callVerdict(r, sig, false).ok != yes {
					all = false
					break
				}
			}
			if all {
				c := *s
				c.Tier = ruleShape
				matches = append(matches, &c)
			}
		}
	}
	if len(matches) == 1 {
		return matches, nil
	}
	var names []string
	for _, m := range matches {
		names = append(names, m.Target())
	}
	return nil, names
}

func (d *detector) typeShapeMatches(props []*upstreamEntry, tn *types.TypeName, s *sym) bool {
	members := d.ix.members(tn, s.Dir, s.Rank)
	for _, p := range props {
		if strings.Contains(p.Shape.Type, "AbortSignal") && len(p.Shape.Calls) == 0 {
			continue
		}
		m := d.memberFor(p, members)
		if m == nil {
			return false
		}
		typeV, sig := d.memberShape(p, m)
		if typeV.ok != yes {
			return false
		}
		for _, cr := range d.l.callRows(p.ID) {
			if sig == nil || d.callVerdict(cr, sig, false).ok != yes {
				return false
			}
		}
	}
	return true
}

// sharesWord reports whether two identifiers share a word of at least four letters that is not a generic verb or noun: N5 matches
// a function by its signature only among functions that name the same thing, so an unrelated helper of a common signature
// (keyText against toolCWD) is not a candidate.
func sharesWord(a, b string) bool {
	have := map[string]bool{}
	for _, w := range identWords(a) {
		have[w] = true
	}
	for _, w := range identWords(b) {
		if have[w] {
			return true
		}
	}
	return false
}

// genericWords are identifier words too common to tie two functions together.
var genericWords = map[string]bool{"create": true, "make": true, "build": true, "from": true, "with": true, "into": true, "this": true,
	"value": true, "values": true, "result": true, "options": true, "data": true, "item": true, "items": true, "list": true, "get": true}

// identWords splits a camelCase, PascalCase or snake_case identifier into lower-case words of four or more letters, without
// generic words.
func identWords(name string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if w := strings.ToLower(string(cur)); len(w) >= 4 && !genericWords[w] {
			words = append(words, w)
		}
		cur = cur[:0]
	}
	rs := []rune(name)
	for i, r := range rs {
		switch {
		case r == '_' || r == '-':
			flush()
			continue
		case unicode.IsUpper(r) && len(cur) > 0 && (unicode.IsLower(cur[len(cur)-1]) || i+1 < len(rs) && unicode.IsLower(rs[i+1])):
			flush()
		}
		cur = append(cur, r)
	}
	flush()
	return words
}

// distinctive reports whether a signature is specific enough to match by shape: at least three parameters and results together, one of
// them a type other than a builtin. A context.Context parameter and an error result are conventions the call rules map away (an
// upstream signal and a rejected Promise), so they neither count nor make a signature distinctive: otherwise every
// func(ctx, string) (string, error) would stand for any upstream (string) => string.
func distinctive(sig *types.Signature) bool {
	n := 0
	named := false
	check := func(t types.Type) {
		n++
		for {
			switch u := t.(type) {
			case *types.Pointer:
				t = u.Elem()
				continue
			case *types.Slice:
				t = u.Elem()
				continue
			}
			break
		}
		if nt := namedOf(t); nt != nil && nt.Obj().Pkg() != nil {
			named = true
		}
	}
	for v := range sig.Params().Variables() {
		if t := v.Type(); !isContext(t) {
			check(t)
		}
	}
	for v := range sig.Results().Variables() {
		if t := v.Type(); !types.Identical(t, types.Universe.Lookup("error").Type()) {
			check(t)
		}
	}
	return n >= 3 && named
}

// isReceiverParam reports whether t, a package function's first parameter, is the receiver of the upstream owner parentID: the named type
// with the owner's name, or a pointer to it (G1).
func isReceiverParam(t types.Type, parentID string) bool {
	owner := parentID[strings.LastIndex(parentID, "#")+1:]
	if i := strings.Index(owner, "::"); i >= 0 {
		owner = owner[:i]
	}
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	return ok && owner != "" && named.Obj().Name() == owner
}

// withoutReceiverParam is a package function's signature without the receiver of the generic upstream method it stands for (G1): its
// first parameter, or its second when the first is the context.Context that Go puts first (RuntimeEntry(ctx, runtime, token, id)). A
// signature without a receiver parameter is returned unchanged.
func withoutReceiverParam(sig *types.Signature, parentID string) *types.Signature {
	at := 0
	if sig.Params().Len() > 0 && isContext(sig.Params().At(0).Type()) {
		at = 1
	}
	if at >= sig.Params().Len() || !isReceiverParam(sig.Params().At(at).Type(), parentID) {
		return sig
	}
	var vars []*types.Var
	for i := range sig.Params().Len() {
		if i != at {
			vars = append(vars, sig.Params().At(i))
		}
	}
	// The type parameters stay free in the parameter types: a type parameter cannot be bound to a second signature.
	return types.NewSignatureType(nil, nil, nil, types.NewTuple(vars...), sig.Results(), sig.Variadic())
}

// sharedOwner applies T10m: the Go owner of field m is the documented Go form of two or more upstream types (ai.StreamOptions carries
// GoogleOptions, OpenAICodexResponsesOptions, SimpleStreamOptions, ... by json tag, LEAD-ANSWERS-ledger #2). Its empty-interface
// field then carries each provider's type for the same key, so one provider's union of literals is a value the field holds; T1's
// named string type would refuse the other providers' object forms.
func (d *detector) sharedOwner(m *sym) bool {
	if m.Owner == "" {
		return false
	}
	owner := m.File + "#" + m.Owner
	names := map[string]bool{}
	for id, target := range d.renames {
		if target == owner && !strings.Contains(id, "::") {
			names[parseID(id).Name] = true
		}
	}
	return len(names) >= 2
}

// literalUnion reports whether up (undefined and null aside) is a union of string literals and primitives. A member that names an
// alias reads the alias body once (`toolChoice?: ToolChoice` with `type ToolChoice = "auto" | "none"`, ai types.ts:88).
func literalUnion(up string, alias func(string) *string) bool {
	n := 0
	for _, part := range splitTop(up, "|") {
		part = strings.TrimSpace(part)
		if identRe.MatchString(part) {
			if body := alias(part); body != nil {
				if !literalUnion(*body, func(string) *string { return nil }) {
					return false
				}
				n++
				continue
			}
		}
		switch {
		case part == "undefined" || part == "null":
		case part == "string" || part == "number" || part == "boolean" || part == "true" || part == "false", stringLitRe.MatchString(part):
			n++
		default:
			return false
		}
	}
	return n > 0
}

// callbackGetter applies M2f: a writable upstream callback property (`onDebug?: () => void`) is the Go getter <Name>() returning the
// function, paired with the setter Set<Name> that takes the same function type (tui tuiBase.OnDebug/SetOnDebug). It returns the
// callback's signature, which the property's call rows are judged against, or nil when fn is not such a getter.
func callbackGetter(fn *types.Func, prop *upstreamEntry) *types.Signature {
	sig := fn.Type().(*types.Signature)
	if sig.Recv() == nil || prop.Shape.Readonly || fn.Name() != upperFirst(prop.Shape.Name) || sig.Params().Len() != 0 || sig.Results().Len() != 1 {
		return nil
	}
	result := sig.Results().At(0).Type()
	cb, ok := result.Underlying().(*types.Signature)
	if !ok {
		return nil
	}
	obj, _, _ := types.LookupFieldOrMethod(sig.Recv().Type(), true, fn.Pkg(), "Set"+upperFirst(prop.Shape.Name))
	set, ok := obj.(*types.Func)
	if !ok {
		return nil
	}
	ss := set.Type().(*types.Signature)
	if ss.Params().Len() != 1 || ss.Results().Len() != 0 || !types.Identical(ss.Params().At(0).Type(), result) {
		return nil
	}
	return cb
}

// interfaceSetterParam returns the parameter type of fn when fn is a Set method (named for the property by the name rules or a documented rename) of its receiver, taking one parameter and returning
// nothing, for the writable upstream property prop; otherwise nil.
func interfaceSetterParam(fn *types.Func, prop *upstreamEntry) types.Type {
	sig := fn.Type().(*types.Signature)
	if sig.Recv() == nil || prop.Shape.Readonly || !strings.HasPrefix(fn.Name(), "Set") || strings.EqualFold(fn.Name(), prop.Shape.Name) || sig.Params().Len() != 1 || sig.Results().Len() != 0 {
		return nil
	}
	return sig.Params().At(0).Type()
}
