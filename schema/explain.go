package schema

// Ports packages/schema/src/explain.ts
//
// Errors of a failed value: a walk over the schema that reports each failing keyword at the value it failed on. It runs only for values that failed the check, using the compiler's checks for subschemas.

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// explainer explains failures of one compiled schema. The compiler's caches and dynamic scope are mutable state, so one explanation runs at a time.
type explainer struct {
	mu     sync.Mutex
	c      *compiler
	schema any
	// rests holds, per schema with `unevaluated*` keywords, the schema without them, so its annotating check is built once.
	rests map[nodeKey]*Object
}

func newExplainer(c *compiler, schema any) *explainer {
	c.root()
	return &explainer{c: c, schema: schema, rests: map[nodeKey]*Object{}}
}

// errors returns the errors of value, at least one (value failed the check).
func (e *explainer) errors(value any) []ValidationError {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []ValidationError
	e.walk(e.schema, e.c.r.rootBase, value, "", &out)
	if len(out) == 0 {
		out = append(out, ValidationError{Path: "", Keyword: "schema", Message: "invalid"})
	}
	return out
}

func (e *explainer) ok(node any, base string, v any) bool { return e.c.check(node, base)(v) }

type pusher func(keyword, message, at string)

// walk is approximate for $dynamicRef schemas: their checks run outside the original dynamic scope.
func (e *explainer) walk(node any, base string, v any, path string, out *[]ValidationError) {
	if b, ok := node.(bool); ok {
		if !b {
			*out = append(*out, ValidationError{Path: path, Keyword: "false", Message: "no value is allowed here"})
		}
		return
	}
	o, isObj := isSchemaObject(node)
	if !isObj {
		return
	}
	b := e.c.r.baseFor(o, base)
	if e.ok(node, b, v) {
		return
	}
	start := len(*out)
	push := func(keyword, message, at string) {
		*out = append(*out, ValidationError{Path: at, Keyword: keyword, Message: message})
	}
	pushHere := func(keyword, message string) { push(keyword, message, path) }
	if e.c.draft == Draft7 {
		if ref, ok := o.stringMember("$ref"); ok {
			target := e.c.r.resolve(ref, b)
			e.walk(target.node, target.base, v, path, out)
			return
		}
	}
	if typeValue, ok := o.get("type"); ok {
		types, isList := typeValue.([]any)
		if !isList {
			types = []any{typeValue}
		}
		passes := false
		for _, t := range types {
			check := Check(never)
			if name, ok := t.(string); ok {
				if known, ok := typeChecks[name]; ok {
					check = known
				}
			}
			if check(v) {
				passes = true
				break
			}
		}
		if !passes {
			joined := make([]string, len(types))
			for i, t := range types {
				joined[i] = jsToString(t)
			}
			pushHere("type", "expected "+strings.Join(joined, " or ")+", got "+typeName(v))
			return
		}
	}
	if list, ok := o.arrayMember("enum"); ok && !enumCheck(list)(v) {
		pushHere("enum", "expected one of "+jsonStringify(list))
	}
	if value, ok := o.get("const"); ok && !constCheck(value)(v) {
		pushHere("const", "expected "+jsonStringify(value))
	}
	typeIs := func(name string) bool { t, _ := o.get("type"); return t == name }
	if s, ok := v.(string); ok && !typeIs("bytes") {
		e.walkString(o, s, pushHere)
	}
	if bs, ok := v.([]byte); ok && typeIs("bytes") {
		e.walkBytes(o, bs, pushHere)
	}
	if n, ok := asNumber(v); ok {
		e.walkNumber(o, n, pushHere)
	}
	if vo, ok := asObject(v); ok {
		e.walkObject(o, b, vo, path, out, push, pushHere)
	}
	if list, ok := v.([]any); ok {
		e.walkArray(o, b, list, path, out, pushHere)
	}
	if list, ok := o.arrayMember("allOf"); ok {
		for _, s := range list {
			e.walk(s, b, v, path, out)
		}
	}
	if list, ok := o.arrayMember("anyOf"); ok {
		e.walkUnion(list, b, v, path, out, false)
	}
	if list, ok := o.arrayMember("oneOf"); ok {
		e.walkUnion(list, b, v, path, out, true)
	}
	if not, ok := o.get("not"); ok && e.ok(not, b, v) {
		pushHere("not", "must not match the schema in not")
	}
	if cond, ok := o.get("if"); ok {
		branchKey := "else"
		if e.ok(cond, b, v) {
			branchKey = "then"
		}
		if branch, ok := o.get(branchKey); ok {
			e.walk(branch, b, v, path, out)
		}
	}
	if ref, ok := o.stringMember("$ref"); ok {
		target := e.c.r.resolve(ref, b)
		e.walk(target.node, target.base, v, path, out)
	}
	if len(*out) == start && (o.has("unevaluatedProperties") || o.has("unevaluatedItems")) {
		e.walkUnevaluated(o, b, v, path, out)
	}
	if _, ok := o.stringMember("$dynamicRef"); ok && len(*out) == start {
		pushHere("$dynamicRef", "does not match the dynamically referenced schema")
	}
}

func (e *explainer) walkString(node jsonObject, v string, push func(keyword, message string)) {
	n := float64(codePoints(v))
	if min, ok := node.numberMember("minLength"); ok && n < min {
		push("minLength", "expected at least "+numberString(min)+" characters")
	}
	if max, ok := node.numberMember("maxLength"); ok && n > max {
		push("maxLength", "expected at most "+numberString(max)+" characters")
	}
	if pattern, ok := node.stringMember("pattern"); ok && !sharedRegex(pattern).MatchString(v) {
		push("pattern", "expected to match "+pattern)
	}
	if e.c.formats != nil {
		if name, ok := node.stringMember("format"); ok {
			if format, ok := e.c.formats[name]; ok && !format(v) {
				push("format", "expected format "+name)
			}
		}
	}
}

func (e *explainer) walkBytes(node jsonObject, v []byte, push func(keyword, message string)) {
	if min, ok := node.numberMember("minLength"); ok && float64(len(v)) < min {
		push("minLength", "expected at least "+numberString(min)+" bytes")
	}
	if max, ok := node.numberMember("maxLength"); ok && float64(len(v)) > max {
		push("maxLength", "expected at most "+numberString(max)+" bytes")
	}
}

func (e *explainer) walkNumber(node jsonObject, v float64, push func(keyword, message string)) {
	if min, ok := node.numberMember("minimum"); ok && v < min {
		push("minimum", "expected >= "+numberString(min))
	}
	if max, ok := node.numberMember("maximum"); ok && v > max {
		push("maximum", "expected <= "+numberString(max))
	}
	if min, ok := node.numberMember("exclusiveMinimum"); ok && v <= min {
		push("exclusiveMinimum", "expected > "+numberString(min))
	}
	if max, ok := node.numberMember("exclusiveMaximum"); ok && v >= max {
		push("exclusiveMaximum", "expected < "+numberString(max))
	}
	if d, ok := node.numberMember("multipleOf"); ok && !isMultiple(v, d) {
		push("multipleOf", "expected a multiple of "+numberString(d))
	}
}

func (e *explainer) walkObject(node jsonObject, b string, v jsonObject, path string, out *[]ValidationError, push pusher, pushHere func(keyword, message string)) {
	properties, _ := node.objectMember("properties")
	required, _ := node.arrayMember("required")
	for _, k := range required {
		if name, ok := k.(string); ok && !v.has(name) {
			push("required", "missing", pointer(path, name))
		}
	}
	for _, k := range properties.keys() {
		if x, ok := v.get(k); ok {
			e.walk(properties.value(k), b, x, pointer(path, k), out)
		}
	}
	patterns, _ := node.objectMember("patternProperties")
	patternKeys := patterns.keys()
	additional, hasAdditional := node.get("additionalProperties")
	names, hasNames := node.get("propertyNames")
	for _, k := range v.keys() {
		matched := properties.has(k)
		x := v.value(k)
		for _, p := range patternKeys {
			if sharedRegex(p).MatchString(k) {
				matched = true
				e.walk(patterns.value(p), b, x, pointer(path, k), out)
			}
		}
		if !matched && hasAdditional {
			if additional == false {
				push("additionalProperties", "unknown field", pointer(path, k))
			} else {
				e.walk(additional, b, x, pointer(path, k), out)
			}
		}
		if hasNames && !e.ok(names, b, k) {
			push("propertyNames", "invalid property name", pointer(path, k))
		}
	}
	count := float64(v.len())
	if min, ok := node.numberMember("minProperties"); ok && count < min {
		pushHere("minProperties", "expected at least "+numberString(min)+" properties")
	}
	if max, ok := node.numberMember("maxProperties"); ok && count > max {
		pushHere("maxProperties", "expected at most "+numberString(max)+" properties")
	}
	// `{ ...dependentRequired, ...dependencies }`: a later object overrides the value of an earlier key and keeps its position.
	var dependencyKeys []string
	dependencies := map[string]any{}
	add := func(m jsonObject) {
		for _, k := range m.keys() {
			if _, seen := dependencies[k]; !seen {
				dependencyKeys = append(dependencyKeys, k)
			}
			dependencies[k] = m.value(k)
		}
	}
	if e.c.draft != Draft7 {
		if m, ok := node.objectMember("dependentRequired"); ok {
			add(m)
		}
	}
	if m, ok := node.objectMember("dependencies"); ok {
		add(m)
	}
	for _, k := range dependencyKeys {
		if !v.has(k) {
			continue
		}
		d := dependencies[k]
		list, isList := d.([]any)
		if !isList {
			e.walk(d, b, v.source(), path, out)
			continue
		}
		for _, r := range list {
			if !v.has(propertyKey(r)) {
				push("dependentRequired", "required when "+k+" is present", pointer(path, propertyKey(r)))
			}
		}
	}
	if e.c.draft != Draft7 {
		if m, ok := node.objectMember("dependentSchemas"); ok {
			for _, k := range m.keys() {
				if v.has(k) {
					e.walk(m.value(k), b, v.source(), path, out)
				}
			}
		}
	}
}

// source is the object as a value.
func (o jsonObject) source() any {
	if o.ordered != nil {
		return o.ordered
	}
	return o.plain
}

func (e *explainer) walkArray(node jsonObject, b string, v []any, path string, out *[]ValidationError, push func(keyword, message string)) {
	var prefix []any
	var rest any
	hasRest := false
	if items, ok := node.arrayMember("items"); ok {
		prefix = items
		rest, hasRest = node.get("additionalItems")
	} else {
		if e.c.draft != Draft7 {
			if list, ok := node.arrayMember("prefixItems"); ok {
				prefix = list
			}
		}
		rest, hasRest = node.get("items")
	}
	for i, item := range v {
		s, has := rest, hasRest
		if i < len(prefix) {
			s, has = prefix[i], true
		}
		if has {
			e.walk(s, b, item, path+"/"+strconv.Itoa(i), out)
		}
	}
	n := float64(len(v))
	if min, ok := node.numberMember("minItems"); ok && n < min {
		push("minItems", "expected at least "+numberString(min)+" items")
	}
	if max, ok := node.numberMember("maxItems"); ok && n > max {
		push("maxItems", "expected at most "+numberString(max)+" items")
	}
	if node.value("uniqueItems") == true && !uniqueCheck(v) {
		push("uniqueItems", "expected unique items")
	}
	if contains, ok := node.get("contains"); ok {
		min, max := 1.0, math.Inf(1)
		if e.c.draft != Draft7 {
			if x, ok := node.numberMember("minContains"); ok {
				min = x
			}
			if x, ok := node.numberMember("maxContains"); ok {
				max = x
			}
		}
		if !containsCheck(e.c.check(contains, b), min, max)(v) {
			push("contains", "expected between "+numberString(min)+" and "+numberString(max)+" matching items")
		}
	}
}

func (e *explainer) walkUnion(branches []any, b string, v any, path string, out *[]ValidationError, exactlyOne bool) {
	matches := 0
	for _, s := range branches {
		if e.ok(s, b, v) {
			matches++
		}
	}
	if matches == 1 || (!exactlyOne && matches > 0) {
		return
	}
	keyword := "anyOf"
	if exactlyOne {
		keyword = "oneOf"
	}
	if matches > 1 {
		*out = append(*out, ValidationError{Path: path, Keyword: keyword, Message: "matches more than one alternative"})
		return
	}
	// A tagged union reports the branch its tag selects; otherwise the branch whose first error is deepest.
	if key, values, ok := e.tagOf(branches, b); ok {
		if vo, isObj := asObject(v); isObj {
			index := slices.IndexFunc(values, func(x any) bool { return strictEquals(x, vo.value(key)) && vo.has(key) })
			if index < 0 {
				*out = append(*out, ValidationError{Path: pointer(path, key), Keyword: keyword, Message: "expected one of " + jsonStringify(values)})
			} else {
				e.walk(branches[index], b, v, path, out)
			}
			return
		}
	}
	var best []ValidationError
	for _, s := range branches {
		var errors []ValidationError
		e.walk(s, b, v, path, &errors)
		if len(errors) > 0 && (best == nil || utf16Len(errors[0].Path) > utf16Len(best[0].Path)) {
			best = errors
		}
	}
	if best == nil {
		best = []ValidationError{{Path: path, Keyword: keyword, Message: "matches no alternative"}}
	}
	*out = append(*out, best...)
}

// tagOf is the key and tag values of a union whose branches carry a distinct primitive const on one property of the first branch.
func (e *explainer) tagOf(branches []any, b string) (string, []any, bool) {
	resolvedBranches := make([]any, len(branches))
	for i, s := range branches {
		resolvedBranches[i] = s
		if o, ok := isSchemaObject(s); ok {
			if ref, ok := o.stringMember("$ref"); ok {
				resolvedBranches[i] = e.c.r.resolve(ref, b).node
			}
		}
	}
	first, ok := isSchemaObject(resolvedBranches[0])
	if !ok {
		return "", nil, false
	}
	firstProperties, ok := first.objectMember("properties")
	if !ok {
		return "", nil, false
	}
	for _, key := range firstProperties.keys() {
		values := make([]any, len(resolvedBranches))
		primitive := true
		for i, s := range resolvedBranches {
			var tagValue any
			present := false
			if so, ok := isSchemaObject(s); ok {
				if properties, ok := so.objectMember("properties"); ok {
					if tag, ok := properties.objectMember(key); ok {
						tagValue, present = tag.get("const")
					}
				}
			}
			values[i] = tagValue
			if !present || isContainer(tagValue) {
				primitive = false
			}
		}
		if primitive && distinct(values) {
			return key, values, true
		}
	}
	return "", nil, false
}

func (e *explainer) walkUnevaluated(node jsonObject, b string, v any, path string, out *[]ValidationError) {
	id := node.identity()
	rest, ok := e.rests[id]
	if !ok {
		rest = &Object{}
		for _, k := range node.keys() {
			if k != "unevaluatedProperties" && k != "unevaluatedItems" {
				rest.Set(k, node.value(k))
			}
		}
		e.rests[id] = rest
	}
	a := &evaluated{}
	e.c.annotating(rest, b)(v, a)
	if vo, ok := asObject(v); ok {
		if unevaluated, has := node.get("unevaluatedProperties"); has && !a.allProps {
			for _, k := range vo.keys() {
				if _, seen := a.props[k]; seen {
					continue
				}
				if unevaluated == false {
					*out = append(*out, ValidationError{Path: pointer(path, k), Keyword: "unevaluatedProperties", Message: "unknown field"})
				} else {
					e.walk(unevaluated, b, vo.value(k), pointer(path, k), out)
				}
			}
		}
	}
	if list, ok := v.([]any); ok {
		if unevaluated, has := node.get("unevaluatedItems"); has && !a.allItems {
			for i := a.items; i < len(list); i++ {
				if _, seen := a.indexes[i]; seen {
					continue
				}
				if unevaluated == false {
					*out = append(*out, ValidationError{Path: path + "/" + strconv.Itoa(i), Keyword: "unevaluatedItems", Message: "unexpected item"})
				} else {
					e.walk(unevaluated, b, list[i], path+"/"+strconv.Itoa(i), out)
				}
			}
		}
	}
}

// pointer appends key to a JSON Pointer path, escaping ~ and /.
func pointer(path, key string) string {
	return path + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

func typeName(v any) string {
	if v == nil {
		return "null"
	}
	if n, ok := asNumber(v); ok {
		if isInteger(n) {
			return "integer"
		}
		return "number"
	}
	switch v.(type) {
	case []any:
		return "array"
	case []byte:
		return "bytes"
	case string:
		return "string"
	case bool:
		return "boolean"
	}
	if isObject(v) {
		return "object"
	}
	return "undefined"
}
