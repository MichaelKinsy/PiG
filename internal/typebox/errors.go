package typebox

// Ports typebox 1.3.27 build/schema/errors.mjs, the Error* functions of build/schema/engine/*.mjs and
// build/system/locale/en_US.mjs: Value.Errors walks the schema keyword by keyword in the engine's order, collects at
// most maxErrors (8) errors in a context, and reports a failing anyOf, allOf, oneOf or $ref operand's errors before
// the keyword's own. Keyword checks that TypeBox delegates to its checker (const, enum, format, pattern, lengths,
// numeric bounds, not, contains) use Check on the keyword alone. unevaluatedItems and unevaluatedProperties, which
// need the evaluated-key tracking, use Check on the whole schema and report TypeBox's message when every other keyword passed; when another keyword failed too, TypeBox may add the unevaluated error after it and this walker does not. A schema object visits its members in its own order: a
// *chordjson.Object keeps the declaration order TypeBox follows, a Go map has none and visits them in JavaScript
// own-key order of the sorted names, so errors of several properties may come in another order than TypeBox's.

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

// maxErrors is TypeBox's Settings maxErrors default (system/settings/settings.mjs).
const maxErrors = 8

type errorContext struct{ errors []ValueError }

func (c *errorContext) atCapacity() bool { return len(c.errors) >= maxErrors }

// addError appends an error unless the context is full; it returns false as the engine's AddError does.
func (c *errorContext) addError(instancePath, message string) bool {
	if !c.atCapacity() {
		c.errors = append(c.errors, ValueError{InstancePath: instancePath, Message: message})
	}
	return false
}

func (c *errorContext) addAll(from *errorContext) {
	for _, failure := range from.errors {
		c.addError(failure.InstancePath, failure.Message)
	}
}

// Errors is Value.Errors(schema, value).
func Errors(schema, value any) []ValueError {
	walker := errorWalker{root: schema}
	context := &errorContext{}
	walker.schema(context, "", schema, Plain(value))
	return context.errors
}

type errorWalker struct{ root any }

// node is a schema object of either Go kind; keys are in the schema's own order (sorted for a Go map).
type node struct {
	plain   map[string]any
	ordered *chordjson.Object
}

func asNode(schema any) (node, bool) {
	switch typed := schema.(type) {
	case map[string]any:
		return node{plain: typed}, true
	case *chordjson.Object:
		return node{ordered: typed}, typed != nil
	}
	return node{}, false
}

func (n node) get(key string) (any, bool) {
	if n.ordered != nil {
		return n.ordered.Get(key)
	}
	value, ok := n.plain[key]
	return value, ok
}

func (n node) keys() []string {
	if n.ordered != nil {
		return n.ordered.Keys()
	}
	return chordjson.MapOwnKeys(n.plain)
}

func (n node) child(key string) (node, bool) {
	value, ok := n.get(key)
	if !ok {
		return node{}, false
	}
	return asNode(value)
}

func (n node) list(key string) ([]any, bool) {
	value, ok := n.get(key)
	items, isList := value.([]any)
	return items, ok && isList
}

func (n node) has(key string) bool { _, ok := n.get(key); return ok }

// keyword is a schema holding one keyword of n, for the checks TypeBox delegates to its checker.
func (n node) keyword(names ...string) map[string]any {
	out := map[string]any{}
	for _, name := range names {
		if value, ok := n.get(name); ok {
			out[name] = value
		}
	}
	return out
}

// valueKeys are a JSON object's own keys in JavaScript enumeration order.
func valueKeys(object map[string]any) []string { return chordjson.MapOwnKeys(object) }

func join(path, key string) string { return path + "/" + key }

func numberText(value any) string {
	if number, ok := Plain(value).(float64); ok {
		return jsnumber.String(number)
	}
	return ""
}

func stringsOf(value any) []string {
	items, _ := value.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, _ := item.(string)
		out = append(out, text)
	}
	return out
}

func typeMatches(typeName string, value any) bool {
	switch typeName {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && !math.IsInf(number, 0) && number == math.Trunc(number)
	case "number":
		number, ok := value.(float64)
		return ok && !math.IsInf(number, 0) && !math.IsNaN(number)
	case "null":
		return value == nil
	case "string":
		_, ok := value.(string)
		return ok
	case "bigint", "constructor", "function", "symbol", "undefined", "void":
		// No JSON value has these JavaScript types.
		return false
	}
	return true
}

// schema is engine ErrorSchema: every applicable keyword runs, in this order, and the schema fails if any did.
func (w errorWalker) schema(context *errorContext, path string, schema, value any) bool {
	if context.atCapacity() {
		return false
	}
	if boolean, ok := schema.(bool); ok {
		return boolean || context.addError(path, "schema is false")
	}
	n, ok := asNode(schema)
	if !ok {
		return true
	}
	valid := true
	step := func(result bool) { valid = result && valid }
	if typeValue, ok := n.get("type"); ok {
		step(w.typeKeyword(context, path, typeValue, value))
	}
	if object, ok := value.(map[string]any); ok {
		step(w.object(context, path, n, object))
	}
	if items, ok := value.([]any); ok {
		step(w.array(context, path, n, items))
	}
	if _, ok := value.(string); ok {
		for _, rule := range []struct{ key, message string }{
			{"maxLength", "must not have more than " + numberText(n.keyword("maxLength")["maxLength"]) + " characters"},
			{"minLength", "must not have fewer than " + numberText(n.keyword("minLength")["minLength"]) + " characters"},
			{"format", `must match format "` + textOf(n, "format") + `"`},
			{"pattern", `must match pattern "` + textOf(n, "pattern") + `"`},
		} {
			if n.has(rule.key) {
				step(Check(n.keyword(rule.key), value) || context.addError(path, rule.message))
			}
		}
	}
	if _, ok := value.(float64); ok {
		for _, rule := range []struct{ key, comparison string }{
			{"exclusiveMaximum", "<"}, {"exclusiveMinimum", ">"}, {"maximum", "<="}, {"minimum", ">="},
		} {
			if n.has(rule.key) {
				limit := n.keyword(rule.key)[rule.key]
				step(Check(n.keyword(rule.key), value) || context.addError(path, "must be "+rule.comparison+" "+numberText(limit)))
			}
		}
		if n.has("multipleOf") {
			step(Check(n.keyword("multipleOf"), value) || context.addError(path, "must be multiple of "+numberText(n.keyword("multipleOf")["multipleOf"])))
		}
	}
	if ref, ok := n.get("$ref"); ok {
		step(w.ref(context, path, ref, value))
	}
	if n.has("const") {
		step(Check(n.keyword("const"), value) || context.addError(path, "must be equal to constant"))
	}
	if n.has("enum") {
		step(Check(n.keyword("enum"), value) || context.addError(path, "must be equal to one of the allowed values"))
	}
	if condition, ok := n.get("if"); ok {
		step(w.ifKeyword(context, path, n, condition, value))
	}
	if n.has("not") {
		step(Check(n.keyword("not"), value) || context.addError(path, "must not be valid"))
	}
	if operands, ok := n.list("allOf"); ok {
		step(w.combinator(context, path, operands, value, "allOf"))
	}
	if operands, ok := n.list("anyOf"); ok {
		step(w.combinator(context, path, operands, value, "anyOf"))
	}
	if operands, ok := n.list("oneOf"); ok {
		step(w.combinator(context, path, operands, value, "oneOf"))
	}
	if n.has("unevaluatedItems") || n.has("unevaluatedProperties") {
		// The other keywords passed, so a failing Check is the unevaluated keyword's: report it as TypeBox does.
		if !Check(schema, value) {
			_, isArray := value.([]any)
			if valid && isArray && n.has("unevaluatedItems") {
				context.addError(path, "must not have unevaluated items")
			} else if valid && !isArray && n.has("unevaluatedProperties") {
				context.addError(path, "must not have unevaluated properties")
			}
			step(false)
		}
	}
	return valid
}

func textOf(n node, key string) string {
	value, _ := n.get(key)
	text, _ := value.(string)
	return text
}

func (w errorWalker) typeKeyword(context *errorContext, path string, typeValue, value any) bool {
	if names, ok := typeValue.([]any); ok {
		for _, name := range names {
			if text, _ := name.(string); typeMatches(text, value) {
				return true
			}
		}
		return context.addError(path, "must be either "+strings.Join(stringsOf(names), " or "))
	}
	name, _ := typeValue.(string)
	return typeMatches(name, value) || context.addError(path, "must be "+name)
}

func (w errorWalker) object(context *errorContext, path string, n node, object map[string]any) bool {
	valid := true
	step := func(result bool) { valid = result && valid }
	properties, hasProperties := n.child("properties")
	patterns, hasPatterns := n.child("patternProperties")
	if required, ok := n.list("required"); ok {
		var missing []string
		for _, name := range stringsOf(required) {
			if _, exists := object[name]; !exists {
				missing = append(missing, name)
			}
		}
		step(len(missing) == 0 || context.addError(path, "must have required properties "+strings.Join(missing, ", ")))
	}
	if additional, ok := n.get("additionalProperties"); ok {
		var known []string
		if hasPatterns {
			known = append(known, patterns.keys()...)
		}
		if hasProperties {
			for _, key := range properties.keys() {
				known = append(known, "^"+regexp.QuoteMeta(key)+"$")
			}
		}
		pattern := `[^\x00-\x{10FFFF}]`
		if len(known) > 0 {
			pattern = "(" + strings.Join(known, "|") + ")"
		}
		matcher, _ := CompilePattern(pattern)
		every := true
		for _, key := range valueKeys(object) {
			if matcher != nil && matcher.MatchString(key) {
				continue
			}
			if !w.schema(context, join(path, key), additional, object[key]) {
				every = false
			}
		}
		step(every || context.addError(path, "must not have additional properties"))
	}
	for _, keyword := range []string{"dependencies", "dependentRequired", "dependentSchemas"} {
		dependencies, ok := n.child(keyword)
		if !ok {
			continue
		}
		every := true
		for _, key := range dependencies.keys() {
			if _, exists := object[key]; !exists {
				continue
			}
			dependency, _ := dependencies.get(key)
			if names, isList := dependency.([]any); isList {
				for _, name := range stringsOf(names) {
					if _, exists := object[name]; !exists {
						every = context.addError(path, "must have properties "+strings.Join(stringsOf(names), ", ")+" when property "+key+" is present") && every
						if keyword == "dependencies" {
							break
						}
					}
				}
				continue
			}
			every = w.schema(context, path, dependency, object) && every
		}
		step(len(object) == 0 || every)
	}
	if hasPatterns {
		for _, pattern := range patterns.keys() {
			matcher, err := CompilePattern(pattern)
			patternSchema, _ := patterns.get(pattern)
			for _, key := range valueKeys(object) {
				if err == nil && matcher.MatchString(key) {
					step(w.schema(context, join(path, key), patternSchema, object[key]))
				}
			}
		}
	}
	if hasProperties {
		for _, key := range properties.keys() {
			if item, exists := object[key]; exists {
				propertySchema, _ := properties.get(key)
				step(w.schema(context, join(path, key), propertySchema, item))
			}
		}
	}
	if names, ok := n.get("propertyNames"); ok {
		var invalid []string
		for _, key := range valueKeys(object) {
			if !w.schema(context, join(path, key), names, key) {
				invalid = append(invalid, key)
			}
		}
		step(len(invalid) == 0 || context.addError(path, "property names "+strings.Join(invalid, ", ")+" are invalid"))
	}
	for _, rule := range []struct{ key, message string }{
		{"minProperties", "must not have fewer than %s properties"},
		{"maxProperties", "must not have more than %s properties"},
	} {
		if n.has(rule.key) {
			step(Check(n.keyword(rule.key), object) || context.addError(path, strings.Replace(rule.message, "%s", numberText(n.keyword(rule.key)[rule.key]), 1)))
		}
	}
	return valid
}

func (w errorWalker) array(context *errorContext, path string, n node, items []any) bool {
	valid := true
	step := func(result bool) { valid = result && valid }
	index := func(i int) string { return join(path, strconv.Itoa(i)) }
	tuple, isTuple := n.list("items")
	if additional, ok := n.get("additionalItems"); ok && isTuple {
		// additionalItems.mjs uses Every: it stops at the first failing extra element.
		for i := len(tuple); i < len(items); i++ {
			if !w.schema(context, index(i), additional, items[i]) {
				step(false)
				break
			}
		}
	}
	contains, hasContains := n.get("contains")
	matching := 0
	if hasContains {
		for _, item := range items {
			if Check(contains, item) {
				matching++
			}
		}
		step(matching > 0 || context.addError(path, "must contain at least 1 valid item"))
	}
	if itemsSchema, ok := n.get("items"); ok {
		if isTuple {
			for i := range min(len(tuple), len(items)) {
				step(w.schema(context, index(i), tuple[i], items[i]))
			}
		} else {
			offset := 0
			if prefix, ok := n.list("prefixItems"); ok {
				offset = len(prefix)
			}
			for i := offset; i < len(items); i++ {
				step(w.schema(context, index(i), itemsSchema, items[i]))
			}
		}
	}
	for _, rule := range []struct{ key, message string }{
		{"maxContains", ""}, {"maxItems", "must not have more than %s items"}, {"minContains", ""}, {"minItems", "must not have fewer than %s items"},
	} {
		limit, ok := Plain(n.keyword(rule.key)[rule.key]).(float64)
		if !n.has(rule.key) {
			continue
		}
		switch rule.key {
		case "maxContains":
			// maxContains.mjs and minContains.mjs count only with a contains schema (IsValid); their keyword is
			// contains.
			step(!hasContains || !ok || float64(matching) <= limit || context.addError(path, "must contain at least 1 valid item"))
		case "minContains":
			step(!hasContains || !ok || float64(matching) >= limit || context.addError(path, "must contain at least 1 valid item"))
		default:
			step(Check(n.keyword(rule.key), items) || context.addError(path, strings.Replace(rule.message, "%s", numberText(limit), 1)))
		}
	}
	if prefix, ok := n.list("prefixItems"); ok && len(items) > 0 {
		for i := range min(len(prefix), len(items)) {
			step(w.schema(context, index(i), prefix[i], items[i]))
		}
	}
	if unique, _ := n.get("uniqueItems"); unique == true {
		step(Check(n.keyword("uniqueItems"), items) || context.addError(path, "must not have duplicate items"))
	}
	return valid
}

func (w errorWalker) ref(context *errorContext, path string, ref, value any) bool {
	target := w.resolve(ref)
	next := &errorContext{}
	if target == nil {
		return false
	}
	result := w.schema(next, path, target, value)
	if !result {
		context.addAll(next)
	}
	return result
}

// resolve finds a "#/..." reference in the root schema; any other reference resolves to nothing.
func (w errorWalker) resolve(ref any) any {
	text, _ := ref.(string)
	fragment, ok := strings.CutPrefix(text, "#")
	if !ok {
		return nil
	}
	current := w.root
	for part := range strings.SplitSeq(strings.TrimPrefix(fragment, "/"), "/") {
		if part == "" {
			continue
		}
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		if list, ok := current.([]any); ok {
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(list) {
				return nil
			}
			current = list[index]
			continue
		}
		n, ok := asNode(current)
		if !ok {
			return nil
		}
		if current, ok = n.get(part); !ok {
			return nil
		}
	}
	return current
}

func (w errorWalker) ifKeyword(context *errorContext, path string, n node, condition, value any) bool {
	thenSchema, ok := n.get("then")
	if !ok {
		thenSchema = true
	}
	elseSchema, ok := n.get("else")
	if !ok {
		elseSchema = true
	}
	// if.mjs: the condition and "then" run in a context of their own whose errors are dropped; "else" runs in the
	// caller's.
	trueContext := &errorContext{}
	if w.schema(trueContext, path, condition, value) {
		return w.schema(trueContext, path, thenSchema, value) || context.addError(path, `must match "then" schema`)
	}
	return w.schema(context, path, elseSchema, value) || context.addError(path, `must match "else" schema`)
}

// combinator is allOf.mjs, anyOf.mjs and oneOf.mjs: each operand runs in a context of its own; the failing
// operands' errors reach the caller's context when the keyword fails (for oneOf, only when no operand passed).
func (w errorWalker) combinator(context *errorContext, path string, operands []any, value any, keyword string) bool {
	var failed []*errorContext
	passed := 0
	for _, operand := range operands {
		next := &errorContext{}
		if w.schema(next, path, operand, value) {
			passed++
		} else {
			failed = append(failed, next)
		}
	}
	var valid bool
	switch keyword {
	case "allOf":
		valid = passed == len(operands)
	case "anyOf":
		valid = passed > 0
	default:
		valid = passed == 1
	}
	if !valid && (keyword != "oneOf" || passed == 0) {
		for _, next := range failed {
			context.addAll(next)
		}
	}
	switch {
	case valid || keyword == "allOf":
		return valid
	case keyword == "anyOf":
		return context.addError(path, "must match a schema in anyOf")
	default:
		return context.addError(path, "must match exactly one schema in oneOf")
	}
}
