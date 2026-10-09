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
)

// typeBoxSchemas maps a schema constant name to its builder expression, for the constants of one upstream package.
type typeBoxSchemas map[string]string

// readTypeBoxSchemas collects `const Name = <TypeBox expression>;` declarations from one source file into schemas.
func readTypeBoxSchemas(text string, schemas typeBoxSchemas) {
	for _, m := range schemaConstStart.FindAllStringSubmatchIndex(text, -1) {
		body := aliasBody(text[m[1]:])
		if typeBoxCallRe.MatchString(body) {
			name := text[m[2]:m[3]]
			if _, dup := schemas[name]; dup {
				schemas[name] = "" // declared twice: the rule does not guess
			} else {
				schemas[name] = body
			}
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
	if m == nil || !strings.HasSuffix(expr, ")") {
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
		if t, _, ok := tr.translate(arg(1), depth+1); ok {
			return "Record<string, " + t + ">", false, true
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

// object translates a TypeBox property map `{ key: schema, ... }` into an object literal type.
func (tr typeBoxTranslator) object(props string, depth int) (string, bool, bool) {
	props = stripTypeNoise(props)
	if !strings.HasPrefix(props, "{") || !strings.HasSuffix(props, "}") {
		return "", false, false
	}
	var members []string
	for _, p := range splitTop(props[1:len(props)-1], ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		key, val, ok := strings.Cut(p, ":")
		key = strings.Trim(strings.TrimSpace(key), `"'`)
		if !ok || !identRe.MatchString(key) {
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
