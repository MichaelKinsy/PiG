package main

import (
	"go/types"
	"reflect"
	"regexp"
	"strings"
)

// integerSuffixes are the upstream property-name endings whose numbers are whole counts, sizes or durations in milliseconds.
// Every other number is a float64: Pi's number is a double, and a name without one of these endings gives no proof it is whole.
var integerSuffixes = []string{"Ms", "Count", "Tokens", "Index", "Length", "Bytes", "Lines", "Port", "Retries", "Limit", "Size", "Width", "Height", "Seconds", "Attempts", "Depth"}

var (
	stringLitSingle = regexp.MustCompile(`^"[^"]*"$`)
	stringLitUnion  = regexp.MustCompile(`^(?:"[^"]*"\s*\|\s*)*"[^"]*"$`)
	identRe         = regexp.MustCompile(`^[A-Za-z_]\w*$`)
	arrayRe         = regexp.MustCompile(`^(?:readonly\s+)?(.+)\[\]$`)
	recordRe        = regexp.MustCompile(`^Record<\s*string\s*,\s*(.+?)\s*>$`)
)

func splitTop(s, sep string) []string {
	var parts []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			for i++; i < len(s) && s[i] != '"'; i++ {
			}
		case strings.ContainsRune("([{<", rune(c)):
			depth++
		case c == '>' && i > 0 && s[i-1] == '=':
		case strings.ContainsRune(")]}>", rune(c)):
			depth--
		case depth == 0 && strings.HasPrefix(s[i:], sep):
			parts = append(parts, strings.TrimSpace(s[start:i]))
			start = i + len(sep)
			i = start - 1
		}
	}
	return append(parts, strings.TrimSpace(s[start:]))
}

// fieldFor decides the Go field for one upstream property of an interface, or says why it leaves the property to a person.
// Rules, in order:
//
//	F1 the name is the upstream name, exported, with initialisms in capitals; a name that the owner already has, by a field or method
//	   or through a struct-typed field that has it, is not added (the detector finds that member itself);
//	F2 the type is the Go form of the upstream type: string, bool, a float64 or int (integerSuffixes), []T, map[string]T, any, a
//	   named type the owner's package declares, or string for a union of string literals;
//	F3 an optional property, or one that is `| null`, is a pointer for string, bool, numbers and structs, so unset and zero stay
//	   different; a slice, map, any or a named string type (an enumeration, whose empty string is no member) is empty when unset;
//	F4 the tag is the upstream name, with omitempty when the property is optional; a type with methods behind an interface, or a
//	   channel, has the tag "-" (it is not wire data), and a function type is not a field (it is behaviour).
func fieldFor(inv map[string]*entry, e, parent *entry, pkg *types.Package, owner *types.TypeName, st *types.Struct, taken map[string]bool) (insertion, string) {
	name := goName(e.Shape.Name)
	if taken[name] {
		return insertion{}, "another property of the same owner takes the Go name " + name
	}
	if !isWireStruct(owner, st) {
		return insertion{}, owner.Name() + " is not a wire type (no JSON-tagged field and no MarshalJSON): an option struct's members are set and read by code, so a person ports them with their consumer"
	}
	obj, _, _ := types.LookupFieldOrMethod(types.NewPointer(owner.Type()), true, pkg, name)
	if obj != nil {
		return insertion{}, "the owner already has " + name
	}
	if existing := foldedDuplicate(st, e.Shape.Name); existing != "" {
		return insertion{}, "the owner already carries this property as " + existing + ": document a rename instead of adding a duplicate"
	}
	for field := range st.Fields() {
		if inner, ok := namedStruct(field.Type()); ok {
			if o, _, _ := types.LookupFieldOrMethod(types.NewPointer(inner), true, pkg, name); o != nil {
				return insertion{}, "the owner holds " + field.Name() + ", which has " + name
			}
		}
	}
	up := strings.TrimSpace(e.Shape.Type)
	if stringLitSingle.MatchString(up) {
		return insertion{}, "discriminator " + up + ": a union member is a Go type, decided by the unions rules"
	}
	optional := e.Shape.Optional
	var kept []string
	for _, m := range splitTop(up, "|") {
		switch m {
		case "undefined", "null":
			optional = true
		default:
			kept = append(kept, m)
		}
	}
	if len(kept) == 0 {
		return insertion{}, "type " + truncate(up) + " carries no value"
	}
	member := strings.Join(kept, " | ")
	if len(kept) > 1 && !stringLitUnion.MatchString(member) {
		return insertion{}, "union type " + truncate(up)
	}
	goType, why := mapType(inv, member, pkg, e.Shape.Name, 0)
	if why != "" {
		return insertion{}, why
	}
	hidden := false
	if tn, ok := pkg.Scope().Lookup(strings.TrimPrefix(strings.TrimPrefix(goType, "[]"), "map[string]")).(*types.TypeName); ok {
		switch u := tn.Type().Underlying().(type) {
		case *types.Signature:
			return insertion{}, "function-typed property " + tn.Name() + ": behaviour, a person ports it"
		case *types.Chan:
			hidden = true
		case *types.Interface:
			hidden = u.NumMethods() > 0
		}
	}
	if optional && pointerWhenOptional(pkg, goType) {
		goType = "*" + goType
	}
	tag := e.Shape.Name
	switch {
	case hidden:
		tag = "-" // an interface with methods or a channel is not wire data
	case optional:
		tag += ",omitempty"
	}
	return insertion{
		owner: owner.Name(), id: e.ID, goName: name, goType: goType, tag: tag,
		comment: "",
	}, ""
}

func truncate(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}

func namedStruct(t types.Type) (types.Type, bool) {
	for {
		p, ok := t.(*types.Pointer)
		if !ok {
			break
		}
		t = p.Elem()
	}
	if n, ok := t.(*types.Named); ok {
		if _, ok := n.Underlying().(*types.Struct); ok {
			return n, true
		}
	}
	return nil, false
}

func mapType(inv map[string]*entry, up string, pkg *types.Package, prop string, depth int) (string, string) {
	up = strings.TrimSpace(up)
	switch {
	case depth > 3:
		return "", "type nests too deeply: " + truncate(up)
	case up == "string":
		return "string", ""
	case up == "boolean":
		return "bool", ""
	case up == "number":
		for _, s := range integerSuffixes {
			if strings.HasSuffix(prop, s) {
				return "int", ""
			}
		}
		return "float64", ""
	case up == "unknown" || up == "any" || up == "object":
		return "any", ""
	case stringLitUnion.MatchString(up):
		return "string", ""
	case strings.Contains(up, "=>") || strings.Contains(up, "extends") || strings.Contains(up, "keyof") || strings.Contains(up, "typeof"):
		return "", "function, conditional or mapped type: " + truncate(up)
	}
	if m := arrayRe.FindStringSubmatch(up); m != nil && !strings.Contains(m[1], "|") {
		inner, why := mapType(inv, m[1], pkg, prop, depth+1)
		if why != "" {
			return "", why
		}
		return "[]" + inner, ""
	}
	if m := recordRe.FindStringSubmatch(up); m != nil {
		inner, why := mapType(inv, m[1], pkg, prop, depth+1)
		if why != "" {
			return "", why
		}
		return "map[string]" + inner, ""
	}
	if identRe.MatchString(up) {
		if tn, ok := pkg.Scope().Lookup(up).(*types.TypeName); ok {
			return tn.Name(), ""
		}
		return "", "named type " + up + " is not declared in package " + pkg.Name()
	}
	return "", "no rule for type " + truncate(up)
}

// pointerWhenOptional reports whether an optional field of the Go type is a pointer.
func pointerWhenOptional(pkg *types.Package, goType string) bool {
	switch goType {
	case "string", "bool", "int", "float64":
		return true
	}
	if tn, ok := pkg.Scope().Lookup(goType).(*types.TypeName); ok {
		_, isStruct := tn.Type().Underlying().(*types.Struct)
		return isStruct
	}
	return false
}

// isWireStruct reports whether the struct is marshalled as a whole: some field has a JSON name, or the type marshals itself.
func isWireStruct(owner *types.TypeName, st *types.Struct) bool {
	for i := 0; i < st.NumFields(); i++ {
		if name, _, _ := strings.Cut(reflect.StructTag(st.Tag(i)).Get("json"), ","); name != "" && name != "-" {
			return true
		}
	}
	for _, method := range []string{"MarshalJSON"} {
		if obj, _, _ := types.LookupFieldOrMethod(types.NewPointer(owner.Type()), true, owner.Pkg(), method); obj != nil {
			return true
		}
	}
	return false
}

// foldedDuplicate returns the existing field that carries the property under its JSON name or a name equal after folding case and separators ("" when none does).
func foldedDuplicate(st *types.Struct, prop string) string {
	want := foldName(prop)
	for i := 0; i < st.NumFields(); i++ {
		jsonName, _, _ := strings.Cut(reflect.StructTag(st.Tag(i)).Get("json"), ",")
		if jsonName == prop || foldName(st.Field(i).Name()) == want || jsonName != "" && foldName(jsonName) == want {
			return st.Field(i).Name()
		}
	}
	return ""
}

func foldName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
