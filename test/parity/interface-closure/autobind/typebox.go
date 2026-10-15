package main

import (
	"regexp"
	"strings"
)

// TypeBox schemas. Pi declares many exported types as `export type X = Static<typeof XSchema>`, where XSchema is a TypeBox builder
// expression in the same file. TB1 rewrites such an alias body to the TypeScript type that TypeBox's Static gives the schema, so the
// type rules judge the Go type against the real shape. A schema expression outside the constructors below leaves the body unchanged.

var (
	schemaConstStart = regexp.MustCompile(`(?m)^(?:export )?const (\w+)\s*(?::[^=\n]*)?=\s*`)
	staticOfRe       = regexp.MustCompile(`^Static<typeof (\w+)>$`)
	typeBoxCallRe    = regexp.MustCompile(`^(Type\.\w+|StrictObject)(<[^(]*>)?\(`)
	constArrayRe     = regexp.MustCompile(`(?s)^\[.*\]\s+as const$`)
	helperStartRe    = regexp.MustCompile(`(?m)^(?:export )?function (\w+)\(([^)]*)\)\s*(?::[^{\n]*)?\{\s*return\s+`)
	optionsParamRe   = regexp.MustCompile(`^\s*(?:\w+\?:\s*TSchemaOptions\s*,?\s*)?$`)
	helperCallRe     = regexp.MustCompile(`^(\w+)\(`)
	propertyKeyRe    = regexp.MustCompile(`^[A-Za-z_$][\w$]*$`)
)

// typeBoxSchemas maps a name to its source expression, for one upstream package: a schema constant to its builder expression, a
// property-map constant to its object literal, a `[...] as const` list to its text, and a helper function `f` to the expression it returns
// under the key "f()". A helper qualifies only when its parameters are at most one optional TSchemaOptions, which Static ignores.
type typeBoxSchemas map[string]string

// readTypeBoxSchemas collects the schema, property-map and list constants and the qualifying helpers of one source file into schemas.
func readTypeBoxSchemas(text string, schemas typeBoxSchemas) {
	record := func(name, body string) {
		if _, dup := schemas[name]; dup {
			schemas[name] = "" // declared twice: the rule does not guess
		} else {
			schemas[name] = body
		}
	}
	for _, m := range schemaConstStart.FindAllStringSubmatchIndex(text, -1) {
		body := aliasBody(text[m[1]:])
		if typeBoxCallRe.MatchString(body) || helperCallRe.MatchString(body) || constArrayRe.MatchString(body) || strings.HasPrefix(body, "{") {
			record(text[m[2]:m[3]], body)
		}
	}
	for _, m := range helperStartRe.FindAllStringSubmatchIndex(text, -1) {
		if optionsParamRe.MatchString(text[m[4]:m[5]]) {
			record(text[m[2]:m[3]]+"()", aliasBody(text[m[1]:]))
		}
	}
}

// staticTypes rewrites every alias body `Static<typeof S>` of a package whose schema S translates (TB1). staticNames maps a schema
// constant to the exported alias that names its static type, so a nested schema keeps its upstream type name.
func staticTypes(aliases map[string]*string, schemas typeBoxSchemas) {
	staticNames := map[string]string{}
	for name, body := range aliases {
		if body == nil {
			continue
		}
		if m := staticOfRe.FindStringSubmatch(strings.TrimSpace(*body)); m != nil {
			staticNames[m[1]] = name
		}
	}
	for name, body := range aliases {
		if body == nil {
			continue
		}
		m := staticOfRe.FindStringSubmatch(strings.TrimSpace(*body))
		if m == nil {
			continue
		}
		tr := typeBoxTranslator{schemas: schemas, names: staticNames, self: m[1]}
		if text, _, ok := tr.translate(schemas[m[1]], 0); ok {
			aliases[name] = &text
		}
	}
}

type typeBoxTranslator struct {
	schemas typeBoxSchemas
	names   map[string]string // schema constant -> exported static type name
	self    string            // the schema being translated, which is not replaced by its own name
}

// translate returns the TypeScript type of a TypeBox expression and whether it is wrapped in Type.Optional.
func (tr typeBoxTranslator) translate(expr string, depth int) (text string, optional bool, ok bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" || depth > 6 {
		return "", false, false
	}
	if identRe.MatchString(expr) {
		if expr != tr.self {
			if n, ok := tr.names[expr]; ok {
				return n, false, true
			}
		}
		if body, ok := tr.schemas[expr]; ok && body != "" {
			return tr.translate(body, depth+1)
		}
		return "", false, false
	}
	m := typeBoxCallRe.FindStringSubmatch(expr)
	if m == nil {
		if h := helperCallRe.FindStringSubmatch(expr); h != nil && strings.HasSuffix(expr, ")") && closesAtEnd(expr[len(h[0])-1:]) {
			if body := tr.schemas[h[1]+"()"]; body != "" {
				return tr.translate(body, depth+1)
			}
		}
		return "", false, false
	}
	if !strings.HasSuffix(expr, ")") {
		return "", false, false
	}
	open := len(m[0]) - 1
	if !closesAtEnd(expr[open:]) {
		return "", false, false
	}
	args := splitTop(expr[open+1:len(expr)-1], ",")
	arg := func(i int) string {
		if i < len(args) {
			return strings.TrimSpace(args[i])
		}
		return ""
	}
	switch m[1] {
	case "Type.String":
		return "string", false, true
	case "Type.Number", "Type.Integer":
		return "number", false, true
	case "Type.Boolean":
		return "boolean", false, true
	case "Type.Null":
		return "null", false, true
	case "Type.Unknown", "Type.Any":
		return "unknown", false, true
	case "Type.Literal":
		if lit := arg(0); stringLit.MatchString(lit) || numberLit.MatchString(lit) || lit == "true" || lit == "false" {
			return lit, false, true
		}
	case "Type.Unsafe":
		if g := strings.TrimSpace(m[2]); g != "" {
			return strings.TrimSpace(g[1 : len(g)-1]), false, true
		}
	case "Type.Optional":
		if t, _, ok := tr.translate(arg(0), depth+1); ok {
			return t, true, true
		}
	case "Type.Array":
		if t, _, ok := tr.translate(arg(0), depth+1); ok {
			return "Array<" + t + ">", false, true
		}
	case "Type.Record":
		key, _, ok := tr.translate(arg(0), depth+1)
		if !ok {
			break
		}
		if t, _, ok := tr.translate(arg(1), depth+1); ok {
			return "Record<" + key + ", " + t + ">", false, true
		}
	case "Type.Partial":
		if t, _, ok := tr.translate(arg(0), depth+1); ok {
			return "Partial<" + t + ">", false, true
		}
	case "Type.Enum":
		if values, ok := tr.listValues(arg(0), 0); ok && len(values) > 0 {
			return strings.Join(values, " | "), false, true
		}
	case "Type.Union":
		list := arg(0)
		if !strings.HasPrefix(list, "[") || !strings.HasSuffix(list, "]") || len(args) > 2 {
			break
		}
		var parts []string
		for _, e := range splitTop(list[1:len(list)-1], ",") {
			if strings.TrimSpace(e) == "" {
				continue
			}
			t, _, ok := tr.translate(e, depth+1)
			if !ok {
				return "", false, false
			}
			parts = append(parts, t)
		}
		if len(parts) > 0 {
			return strings.Join(parts, " | "), false, true
		}
	case "Type.Object", "StrictObject":
		return tr.object(arg(0), depth)
	}
	return "", false, false
}

// object translates a TypeBox property map `{ key: schema, ... }`, or the name of a constant holding one, into an object literal type.
func (tr typeBoxTranslator) object(props string, depth int) (string, bool, bool) {
	entries, ok := tr.propertyEntries(props, 0)
	if !ok {
		return "", false, false
	}
	var members []string
	for _, p := range entries {
		key, val, ok := strings.Cut(p, ":")
		key = strings.Trim(strings.TrimSpace(key), `"'`)
		if !ok || !propertyKeyRe.MatchString(key) {
			return "", false, false
		}
		t, opt, ok := tr.translate(val, depth+1)
		if !ok {
			return "", false, false
		}
		if opt {
			key += "?"
		}
		members = append(members, key+": "+t)
	}
	return "{ " + strings.Join(members, "; ") + " }", false, true
}

// propertyEntries lists the `key: schema` entries of a property map, following a constant that names one and a `...Name` spread.
func (tr typeBoxTranslator) propertyEntries(props string, depth int) ([]string, bool) {
	props = stripTypeNoise(props)
	if identRe.MatchString(props) && depth < 6 {
		return tr.propertyEntries(tr.schemas[props], depth+1)
	}
	if !strings.HasPrefix(props, "{") || !strings.HasSuffix(props, "}") || depth > 6 {
		return nil, false
	}
	var entries []string
	for _, p := range splitTop(props[1:len(props)-1], ",") {
		p = strings.TrimSpace(p)
		switch {
		case p == "":
		case strings.HasPrefix(p, "..."):
			spread, ok := tr.propertyEntries(strings.TrimSpace(p[3:]), depth+1)
			if !ok {
				return nil, false
			}
			entries = append(entries, spread...)
		default:
			entries = append(entries, p)
		}
	}
	return entries, true
}

// listValues returns the string literals of a `["a", ...OTHER] as const` list, or of the constant that names one.
func (tr typeBoxTranslator) listValues(expr string, depth int) ([]string, bool) {
	expr = strings.TrimSpace(expr)
	if identRe.MatchString(expr) && depth < 6 {
		return tr.listValues(tr.schemas[expr], depth+1)
	}
	expr = strings.TrimSpace(strings.TrimSuffix(expr, "as const"))
	if !strings.HasPrefix(expr, "[") || !strings.HasSuffix(expr, "]") || depth > 6 {
		return nil, false
	}
	var values []string
	for _, e := range splitTop(expr[1:len(expr)-1], ",") {
		e = strings.TrimSpace(e)
		switch {
		case e == "":
		case strings.HasPrefix(e, "..."):
			spread, ok := tr.listValues(strings.TrimSpace(e[3:]), depth+1)
			if !ok {
				return nil, false
			}
			values = append(values, spread...)
		case stringLit.MatchString(e):
			values = append(values, e)
		default:
			return nil, false
		}
	}
	return values, true
}

// closesAtEnd reports whether the parenthesis that opens s closes at its end, skipping string literals.
func closesAtEnd(s string) bool {
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
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '(':
			depth++
		case c == ')':
			depth--
			if depth == 0 {
				return i == len(s)-1
			}
		}
	}
	return false
}
