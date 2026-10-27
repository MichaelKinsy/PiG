package typebox

// Ports typebox 1.3.27 build/schema/errors.mjs, the Error* functions of build/schema/engine/*.mjs and
// build/system/locale/en_US.mjs: Value.Errors walks the schema keyword by keyword in the engine's order, collects at
// most maxErrors (8) errors in a context, and reports a failing anyOf, allOf, oneOf or $ref operand's errors before
// the keyword's own. Keyword checks that TypeBox delegates to its checker (const, enum, format, pattern, lengths,
// numeric bounds, not, contains) use Check on the keyword alone. unevaluatedItems and unevaluatedProperties, which
// need the evaluated-key tracking, use Check on the whole schema and report TypeBox's message when every other keyword passed; when another keyword failed too, TypeBox may add the unevaluated error after it and this walker does not. A schema object visits its members in its own order: a
// *chordjson.Object keeps the declaration order TypeBox follows, a Go map has none and visits them in JavaScript
// own-key order of the sorted names. Members of the value are visited in the order they arrived in when it is a *chordjson.Object (Errors records each object's member order), and sorted when it is a Go map.

import (
	"math"
	"reflect"
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
func (c *errorContext) addError(keyword string, p errParams, spath *pathSeg, instancePath, message string) bool {
	if !c.atCapacity() {
		c.errors = append(c.errors, ValueError{Keyword: keyword, SchemaPath: spath.String(), InstancePath: instancePath, Message: message, params: p})
	}
	return false
}

// params is an error's params: up to two member values. Their names follow from the keyword (ValueError.ParamsObject), so
// an error holds two values and no names or object; a list of names is kept as a []string and becomes the JSON list only
// when the params object is asked for.
func params1(v any) errParams { return errParams{val1: v} }

func params2(v1, v2 any) errParams { return errParams{val1: v1, val2: v2} }

// errParams are the params of one error: the values of its first and second member.
type errParams struct{ val1, val2 any }

// pathSeg is one step of a schema path, linked to its parent so the path string is built only for an error that is
// emitted. A nil *pathSeg is the root, "#"; a $ref restarts at it.
type pathSeg struct {
	parent *pathSeg
	a, b   string
	hasB   bool
}

func (p *pathSeg) String() string {
	if p == nil {
		return "#"
	}
	size := 1
	for q := p; q != nil; q = q.parent {
		size += 1 + len(q.a)
		if q.hasB {
			size += 1 + len(q.b)
		}
	}
	var out strings.Builder
	out.Grow(size)
	out.WriteByte('#')
	p.write(&out)
	return out.String()
}

// write appends the path from the root down to p, each step after a slash.
func (p *pathSeg) write(out *strings.Builder) {
	if p.parent != nil {
		p.parent.write(out)
	}
	out.WriteByte('/')
	out.WriteString(p.a)
	if p.hasB {
		out.WriteByte('/')
		out.WriteString(p.b)
	}
}

func (w errorWalker) seg(parent *pathSeg, a string) *pathSeg {
	return &pathSeg{parent: parent, a: a}
}

func (w errorWalker) seg2(parent *pathSeg, a, b string) *pathSeg {
	return &pathSeg{parent: parent, a: a, b: b, hasB: true}
}

func (c *errorContext) addAll(from *errorContext) {
	for _, failure := range from.errors {
		if !c.atCapacity() {
			c.errors = append(c.errors, failure)
		}
	}
}

// Errors is Value.Errors(schema, value).
func Errors(schema, value any) []ValueError {
	var orders map[uintptr][]string
	plain := orderedPlain(value, &orders)
	walker := errorWalker{root: schema, orders: orders}
	context := &errorContext{}
	walker.schema(context, nil, "", schema, plain)
	return context.errors
}

type errorWalker struct {
	root any
	// orders is the member order of each object of the value being walked, by the address of its map: a map has no
	// order of its own, and TypeBox lists errors and params in the instance's insertion order.
	orders map[uintptr][]string
}

// keys are the members of object in the order TypeBox visits them: the order the value arrived in (a
// *chordjson.Object keeps it), or the sorted names for a value that was a Go map.
func (w errorWalker) keys(object map[string]any) []string {
	if keys, ok := w.orders[reflect.ValueOf(object).Pointer()]; ok {
		return keys
	}
	return valueKeys(object)
}

// orderedPlain is Plain that also records the member order of every *chordjson.Object it converts whose order a sorted
// walk would not reproduce; the table is made only when one is found.
func orderedPlain(value any, orders *map[uintptr][]string) any {
	switch typed := value.(type) {
	case *chordjson.Object:
		if typed == nil {
			return nil
		}
		out := make(map[string]any, typed.Len())
		for key, item := range typed.All() {
			out[key] = orderedPlain(item, orders)
		}
		if keys := typed.Keys(); !sortedKeys(keys) {
			if *orders == nil {
				*orders = map[uintptr][]string{}
			}
			(*orders)[reflect.ValueOf(out).Pointer()] = keys
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = orderedPlain(item, orders)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = orderedPlain(item, orders)
		}
		return out
	default:
		return Plain(value)
	}
}

// sortedKeys reports whether keys are already in the order a Go map's members are visited in (MapOwnKeys): ascending
// ASCII names with no integer-like name, which JavaScript lists first.
func sortedKeys(keys []string) bool {
	for index, key := range keys {
		if key == "" || key[0] >= '0' && key[0] <= '9' || index > 0 && keys[index-1] >= key {
			return false
		}
		for position := 0; position < len(key); position++ {
			if key[position] >= 0x80 {
				return false
			}
		}
	}
	return true
}

// node is a schema object of either Go kind; keys are in the schema's own order (sorted for a Go map).
type node struct {
	plain   map[string]any
	ordered *chordjson.Object
	decl    *Ordered
}

func asNode(schema any) (node, bool) {
	switch typed := schema.(type) {
	case map[string]any:
		return node{plain: typed}, true
	case *Ordered:
		return node{decl: typed}, typed != nil
	case *chordjson.Object:
		return node{ordered: typed}, typed != nil
	}
	return node{}, false
}

func (n node) get(key string) (any, bool) {
	if n.decl != nil {
		return n.decl.Get(key)
	}
	if n.ordered != nil {
		return n.ordered.Get(key)
	}
	value, ok := n.plain[key]
	return value, ok
}

func (n node) keys() []string {
	if n.decl != nil {
		return n.decl.Keys()
	}
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
func (w errorWalker) schema(context *errorContext, spath *pathSeg, path string, schema, value any) bool {
	if context.atCapacity() {
		return false
	}
	if boolean, ok := schema.(bool); ok {
		return boolean || context.addError("boolean", errParams{}, spath, path, "schema is false")
	}
	n, ok := asNode(schema)
	if !ok {
		return true
	}
	valid := true
	step := func(result bool) { valid = result && valid }
	if typeValue, ok := n.get("type"); ok {
		step(w.typeKeyword(context, spath, path, typeValue, value))
	}
	if object, ok := value.(map[string]any); ok {
		step(w.object(context, spath, path, n, object))
	}
	if items, ok := value.([]any); ok {
		step(w.array(context, spath, path, n, items))
	}
	if _, ok := value.(string); ok {
		for _, rule := range []struct{ key, message string }{
			{"maxLength", "must not have more than " + numberText(n.keyword("maxLength")["maxLength"]) + " characters"},
			{"minLength", "must not have fewer than " + numberText(n.keyword("minLength")["minLength"]) + " characters"},
			{"format", `must match format "` + textOf(n, "format") + `"`},
			{"pattern", `must match pattern "` + textOf(n, "pattern") + `"`},
		} {
			if n.has(rule.key) {
				limit, _ := n.get(rule.key)
				step(Check(n.keyword(rule.key), value) || context.addError(rule.key, params1(Plain(limit)), spath, path, rule.message))
			}
		}
	}
	if _, ok := value.(float64); ok {
		for _, rule := range []struct{ key, comparison string }{
			{"exclusiveMaximum", "<"}, {"exclusiveMinimum", ">"}, {"maximum", "<="}, {"minimum", ">="},
		} {
			if n.has(rule.key) {
				limit := n.keyword(rule.key)[rule.key]
				step(Check(n.keyword(rule.key), value) || context.addError(rule.key, params2(rule.comparison, Plain(limit)), spath, path, "must be "+rule.comparison+" "+numberText(limit)))
			}
		}
		if n.has("multipleOf") {
			step(Check(n.keyword("multipleOf"), value) || context.addError("multipleOf", params1(Plain(n.keyword("multipleOf")["multipleOf"])), spath, path, "must be multiple of "+numberText(n.keyword("multipleOf")["multipleOf"])))
		}
	}
	if ref, ok := n.get("$ref"); ok {
		step(w.ref(context, path, ref, value))
	}
	if n.has("const") {
		step(Check(n.keyword("const"), value) || context.addError("const", params1(Plain(n.keyword("const")["const"])), spath, path, "must be equal to constant"))
	}
	if n.has("enum") {
		step(Check(n.keyword("enum"), value) || context.addError("enum", params1(Plain(n.keyword("enum")["enum"])), spath, path, "must be equal to one of the allowed values"))
	}
	if condition, ok := n.get("if"); ok {
		step(w.ifKeyword(context, spath, path, n, condition, value))
	}
	if n.has("not") {
		step(Check(n.keyword("not"), value) || context.addError("not", errParams{}, spath, path, "must not be valid"))
	}
	if operands, ok := n.list("allOf"); ok {
		step(w.combinator(context, spath, path, operands, value, "allOf"))
	}
	if operands, ok := n.list("anyOf"); ok {
		step(w.combinator(context, spath, path, operands, value, "anyOf"))
	}
	if operands, ok := n.list("oneOf"); ok {
		step(w.combinator(context, spath, path, operands, value, "oneOf"))
	}
	if n.has("unevaluatedItems") || n.has("unevaluatedProperties") {
		// The other keywords passed, so a failing Check is the unevaluated keyword's: report it as TypeBox does.
		if !Check(schema, value) {
			_, isArray := value.([]any)
			if valid && isArray && n.has("unevaluatedItems") {
				context.addError("unevaluatedItems", params1(w.unevaluatedItems(n, value.([]any))), spath, path, "must not have unevaluated items")
			} else if valid && !isArray && n.has("unevaluatedProperties") {
				context.addError("unevaluatedProperties", params1(w.unevaluatedProperties(n, value)), spath, path, "must not have unevaluated properties")
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

func (w errorWalker) typeKeyword(context *errorContext, spath *pathSeg, path string, typeValue, value any) bool {
	if names, ok := typeValue.([]any); ok {
		for _, name := range names {
			if text, _ := name.(string); typeMatches(text, value) {
				return true
			}
		}
		return context.addError("type", params1(typeValue), spath, path, "must be either "+strings.Join(stringsOf(names), " or "))
	}
	name, _ := typeValue.(string)
	return typeMatches(name, value) || context.addError("type", params1(typeValue), spath, path, "must be "+name)
}

func (w errorWalker) object(context *errorContext, spath *pathSeg, path string, n node, object map[string]any) bool {
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
		step(len(missing) == 0 || context.addError("required", params1(missing), spath, path, "must have required properties "+strings.Join(missing, ", ")))
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
		var rejected []string
		for _, key := range w.keys(object) {
			if matcher != nil && matcher.MatchString(key) {
				continue
			}
			if !w.schema(context, w.seg(spath, "additionalProperties"), join(path, key), additional, object[key]) {
				rejected = append(rejected, key)
			}
		}
		step(len(rejected) == 0 || context.addError("additionalProperties", params1(rejected), spath, path, "must not have additional properties"))
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
						every = context.addError(keyword, params2(key, dependency), spath, path, "must have properties "+strings.Join(stringsOf(names), ", ")+" when property "+key+" is present") && every
						if keyword == "dependencies" {
							break
						}
					}
				}
				continue
			}
			every = w.schema(context, w.seg2(spath, keyword, key), path, dependency, object) && every
		}
		step(len(object) == 0 || every)
	}
	if hasPatterns {
		for _, pattern := range patterns.keys() {
			matcher, err := CompilePattern(pattern)
			patternSchema, _ := patterns.get(pattern)
			for _, key := range w.keys(object) {
				if err == nil && matcher.MatchString(key) {
					step(w.schema(context, w.seg2(spath, "patternProperties", pattern), join(path, key), patternSchema, object[key]))
				}
			}
		}
	}
	if hasProperties {
		for _, key := range properties.keys() {
			if item, exists := object[key]; exists {
				propertySchema, _ := properties.get(key)
				step(w.schema(context, w.seg2(spath, "properties", key), join(path, key), propertySchema, item))
			}
		}
	}
	if names, ok := n.get("propertyNames"); ok {
		var invalid []string
		for _, key := range w.keys(object) {
			if !w.schema(context, w.seg(spath, "propertyNames"), join(path, key), names, key) {
				invalid = append(invalid, key)
			}
		}
		step(len(invalid) == 0 || context.addError("propertyNames", params1(invalid), spath, path, "property names "+strings.Join(invalid, ", ")+" are invalid"))
	}
	for _, rule := range []struct{ key, message string }{
		{"minProperties", "must not have fewer than %s properties"},
		{"maxProperties", "must not have more than %s properties"},
	} {
		if n.has(rule.key) {
			step(Check(n.keyword(rule.key), object) || context.addError(rule.key, params1(Plain(n.keyword(rule.key)[rule.key])), spath, path, strings.Replace(rule.message, "%s", numberText(n.keyword(rule.key)[rule.key]), 1)))
		}
	}
	return valid
}

func (w errorWalker) array(context *errorContext, spath *pathSeg, path string, n node, items []any) bool {
	valid := true
	step := func(result bool) { valid = result && valid }
	index := func(i int) string { return join(path, strconv.Itoa(i)) }
	tuple, isTuple := n.list("items")
	if additional, ok := n.get("additionalItems"); ok && isTuple {
		// additionalItems.mjs uses Every: it stops at the first failing extra element.
		for i := len(tuple); i < len(items); i++ {
			if !w.schema(context, w.seg(spath, "additionalItems"), index(i), additional, items[i]) {
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
		step(matching > 0 || context.addError("contains", params1(float64(1)), spath, path, "must contain at least 1 valid item"))
	}
	if itemsSchema, ok := n.get("items"); ok {
		if isTuple {
			for i := range min(len(tuple), len(items)) {
				step(w.schema(context, w.seg2(spath, "items", strconv.Itoa(i)), index(i), tuple[i], items[i]))
			}
		} else {
			offset := 0
			if prefix, ok := n.list("prefixItems"); ok {
				offset = len(prefix)
			}
			for i := offset; i < len(items); i++ {
				step(w.schema(context, w.seg(spath, "items"), index(i), itemsSchema, items[i]))
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
			minContains := any(float64(1))
			if declared, has := n.get("minContains"); has {
				minContains = Plain(declared)
			}
			step(!hasContains || !ok || float64(matching) <= limit || context.addError("contains", params2(minContains, Plain(n.keyword(rule.key)[rule.key])), spath, path, "must contain at least 1 valid item"))
		case "minContains":
			step(!hasContains || !ok || float64(matching) >= limit || context.addError("contains", params1(Plain(n.keyword(rule.key)[rule.key])), spath, path, "must contain at least 1 valid item"))
		default:
			step(Check(n.keyword(rule.key), items) || context.addError(rule.key, params1(Plain(n.keyword(rule.key)[rule.key])), spath, path, strings.Replace(rule.message, "%s", numberText(limit), 1)))
		}
	}
	if prefix, ok := n.list("prefixItems"); ok && len(items) > 0 {
		for i := range min(len(prefix), len(items)) {
			step(w.schema(context, w.seg2(spath, "prefixItems", strconv.Itoa(i)), index(i), prefix[i], items[i]))
		}
	}
	if unique, _ := n.get("uniqueItems"); unique == true {
		step(Check(n.keyword("uniqueItems"), items) || context.addError("uniqueItems", params1(duplicateIndices(items)), spath, path, "must not have duplicate items"))
	}
	return valid
}

func (w errorWalker) ref(context *errorContext, path string, ref, value any) bool {
	target := w.resolve(ref)
	next := &errorContext{}
	if target == nil {
		return false
	}
	result := w.schema(next, nil, path, target, value)
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

func (w errorWalker) ifKeyword(context *errorContext, spath *pathSeg, path string, n node, condition, value any) bool {
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
	if w.schema(trueContext, w.seg(spath, "if"), path, condition, value) {
		return w.schema(trueContext, w.seg(spath, "then"), path, thenSchema, value) || context.addError("if", params1("then"), spath, path, `must match "then" schema`)
	}
	return w.schema(context, w.seg(spath, "else"), path, elseSchema, value) || context.addError("if", params1("else"), spath, path, `must match "else" schema`)
}

// combinator is allOf.mjs, anyOf.mjs and oneOf.mjs: each operand runs in a context of its own; the failing
// operands' errors reach the caller's context when the keyword fails (for oneOf, only when no operand passed).
func (w errorWalker) combinator(context *errorContext, spath *pathSeg, path string, operands []any, value any, keyword string) bool {
	var failed []*errorContext
	var passing []any
	for index, operand := range operands {
		next := &errorContext{}
		if w.schema(next, w.seg2(spath, keyword, strconv.Itoa(index)), path, operand, value) {
			passing = append(passing, float64(index))
		} else {
			failed = append(failed, next)
		}
	}
	passed := len(passing)
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
		return context.addError("anyOf", errParams{}, spath, path, "must match a schema in anyOf")
	default:
		return context.addError("oneOf", params1(append([]any{}, passing...)), spath, path, "must match exactly one schema in oneOf")
	}
}

// duplicateIndices are the indices of the items that repeat an earlier one, as uniqueItems.mjs lists them.
func duplicateIndices(items []any) []any {
	out := []any{}
	for index, item := range items {
		for _, earlier := range items[:index] {
			if reflect.DeepEqual(Plain(earlier), Plain(item)) {
				out = append(out, float64(index))
				break
			}
		}
	}
	return out
}

// unevaluatedProperties lists the members of value that no other keyword of n evaluated and that the
// unevaluatedProperties schema rejects, as unevaluatedProperties.mjs lists them.
func (w errorWalker) unevaluatedProperties(n node, value any) []string {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	evaluated := map[string]bool{}
	w.evaluatedProperties(n, object, evaluated)
	extra, _ := n.get("unevaluatedProperties")
	var out []string
	for _, key := range w.keys(object) {
		if !evaluated[key] && !Check(extra, object[key]) {
			out = append(out, key)
		}
	}
	return out
}

// evaluatedProperties adds the members of object that n's keywords evaluate: properties, matching patterns,
// additionalProperties, and what the passing operands of allOf, anyOf, oneOf, if, then, else and $ref evaluate.
func (w errorWalker) evaluatedProperties(n node, object map[string]any, evaluated map[string]bool) {
	if properties, ok := n.child("properties"); ok {
		for _, key := range properties.keys() {
			if _, exists := object[key]; exists {
				evaluated[key] = true
			}
		}
	}
	if patterns, ok := n.child("patternProperties"); ok {
		for _, pattern := range patterns.keys() {
			if matcher, err := CompilePattern(pattern); err == nil {
				for key := range object {
					if matcher.MatchString(key) {
						evaluated[key] = true
					}
				}
			}
		}
	}
	if n.has("additionalProperties") {
		for key := range object {
			evaluated[key] = true
		}
	}
	nested := func(schema any) {
		if operand, ok := asNode(schema); ok && Check(schema, object) {
			w.evaluatedProperties(operand, object, evaluated)
		}
	}
	for _, keyword := range []string{"allOf", "anyOf", "oneOf"} {
		if operands, ok := n.list(keyword); ok {
			for _, operand := range operands {
				nested(operand)
			}
		}
	}
	if ref, ok := n.get("$ref"); ok {
		nested(w.resolve(ref))
	}
	if condition, ok := n.get("if"); ok {
		if Check(condition, object) {
			nested(condition)
			if thenSchema, has := n.get("then"); has {
				nested(thenSchema)
			}
		} else if elseSchema, has := n.get("else"); has {
			nested(elseSchema)
		}
	}
}

// unevaluatedItems lists the indices of items that no other keyword of n evaluated and that the unevaluatedItems
// schema rejects.
func (w errorWalker) unevaluatedItems(n node, items []any) []any {
	evaluated := map[int]bool{}
	w.evaluatedItems(n, items, evaluated)
	extra, _ := n.get("unevaluatedItems")
	out := []any{}
	for index, item := range items {
		if !evaluated[index] && !Check(extra, item) {
			out = append(out, float64(index))
		}
	}
	return out
}

func (w errorWalker) evaluatedItems(n node, items []any, evaluated map[int]bool) {
	if prefix, ok := n.list("prefixItems"); ok {
		for index := range min(len(prefix), len(items)) {
			evaluated[index] = true
		}
	}
	if tuple, ok := n.list("items"); ok {
		for index := range min(len(tuple), len(items)) {
			evaluated[index] = true
		}
	} else if n.has("items") {
		for index := range items {
			evaluated[index] = true
		}
	}
	if contains, ok := n.get("contains"); ok {
		for index, item := range items {
			if Check(contains, item) {
				evaluated[index] = true
			}
		}
	}
	nested := func(schema any) {
		if operand, ok := asNode(schema); ok && Check(schema, items) {
			w.evaluatedItems(operand, items, evaluated)
		}
	}
	for _, keyword := range []string{"allOf", "anyOf", "oneOf"} {
		if operands, ok := n.list(keyword); ok {
			for _, operand := range operands {
				nested(operand)
			}
		}
	}
	if ref, ok := n.get("$ref"); ok {
		nested(w.resolve(ref))
	}
}
