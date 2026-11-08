package schema

// Ports packages/schema/src/explain.ts
//
// Errors of a failed value: a walk over the schema that reports each failing keyword at the value it failed on. It runs only for values that failed the check, using the compiler's checks for subschemas.

import (
	"math"
	"math/big"
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
	// memo holds the errors of `$ref` targets for container values within one errors call: a recursive union explains each branch to pick the one to blame, and every branch would explain the same children again, exponentially in the depth.
	memo map[nodeKey]map[valueKey]refErrors
}

// refErrors are the errors of one value at one path.
type refErrors struct {
	path   string
	errors []ValidationError
}

func newExplainer(c *compiler, schema any) *explainer {
	c.root()
	return &explainer{c: c, schema: schema, rests: map[nodeKey]*Object{}}
}

// errors returns the errors of value, at least one (value failed the check), or the error a part of the schema that was never compiled raises.
func (e *explainer) errors(value any) (result []ValidationError, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			e.c.reset()
			result, err = nil, compileFailure(r)
		}
	}()
	var out []ValidationError
	defer func() { e.memo = nil }()
	e.walk(e.schema, e.c.r.rootBase, value, "", &out)
	if len(out) == 0 {
		out = append(out, ValidationError{Path: "", Keyword: "schema", Message: "invalid"})
	}
	return out, nil
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
			e.walkRef(ref, b, v, path, out)
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
		pushHere("enum", "expected one of "+listValues(list))
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
	if _, isNumber := asNumber(v); isNumber || isBigInt(v) {
		e.walkNumber(o, v, pushHere)
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
		e.walkRef(ref, b, v, path, out)
	}
	if len(*out) == start && (o.has("unevaluatedProperties") || o.has("unevaluatedItems")) {
		e.walkUnevaluated(o, b, v, path, out)
	}
	if _, ok := o.stringMember("$dynamicRef"); ok && len(*out) == start {
		pushHere("$dynamicRef", "does not match the dynamically referenced schema")
	}
}

// walkRef explains v against the schema ref names. A reference that names nothing (UnresolvedRefsReject) is one error; the errors of a container value at one path are kept for the rest of the call.
func (e *explainer) walkRef(ref, base string, v any, path string, out *[]ValidationError) {
	target := e.c.r.resolve(ref, base)
	if target.node == false && e.c.r.isUnresolved(ref, base) {
		*out = append(*out, ValidationError{Path: path, Keyword: "$ref", Message: "unresolved reference " + ref})
		return
	}
	node, isNode := isSchemaObject(target.node)
	key, isContainerValue := explainKey(v)
	if !isNode || !isContainerValue {
		e.walk(target.node, target.base, v, path, out)
		return
	}
	if e.memo == nil {
		e.memo = map[nodeKey]map[valueKey]refErrors{}
	}
	values := e.memo[node.identity()]
	if known, ok := values[key]; ok && known.path == path {
		*out = append(*out, known.errors...)
		return
	}
	var errors []ValidationError
	e.walk(target.node, target.base, v, path, &errors)
	if values == nil {
		values = map[valueKey]refErrors{}
		e.memo[node.identity()] = values
	}
	values[key] = refErrors{path: path, errors: errors}
	*out = append(*out, errors...)
}

// explainKey is the identity of an object or array value, which the errors memo is keyed on; a primitive and an empty array have none and are explained again.
func explainKey(v any) (valueKey, bool) {
	key := identityOf(v, false)
	if key.kind == kindObject || key.kind == kindArray && key.ptr != nil {
		return key, true
	}
	return valueKey{}, false
}

// listValues is values as JSON, at most listedValues of them, then how many more there are: an enum of thousands stays readable.
func listValues(values []any) string {
	const listedValues = 20
	if len(values) <= listedValues {
		return jsonStringify(values)
	}
	listed := jsonStringify(values[:listedValues])
	return listed[:len(listed)-1] + ", ... and " + strconv.Itoa(len(values)-listedValues) + " more]"
}

func containsMessage(min, max float64) string {
	plural := func(n float64) string {
		if n == 1 {
			return "item"
		}
		return "items"
	}
	if math.IsInf(max, 1) {
		return "expected at least " + numberString(min) + " matching " + plural(min)
	}
	if min == 0 {
		return "expected at most " + numberString(max) + " matching " + plural(max)
	}
	return "expected between " + numberString(min) + " and " + numberString(max) + " matching items"
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

// walkNumber is the number keywords, for numbers and bigints alike: bounds of either kind compare with values of either kind.
func (e *explainer) walkNumber(node jsonObject, v any, push func(keyword, message string)) {
	if min, ok := node.boundMember("minimum"); ok && isBelow(v, min) {
		push("minimum", "expected >= "+boundString(min))
	}
	if max, ok := node.boundMember("maximum"); ok && isBelow(max, v) {
		push("maximum", "expected <= "+boundString(max))
	}
	if min, ok := node.boundMember("exclusiveMinimum"); ok && isBelowOrEqual(v, min) {
		push("exclusiveMinimum", "expected > "+boundString(min))
	}
	if max, ok := node.boundMember("exclusiveMaximum"); ok && isBelowOrEqual(max, v) {
		push("exclusiveMaximum", "expected < "+boundString(max))
	}
	if d, ok := node.boundMember("multipleOf"); ok && !isMultipleOf(v, d) {
		push("multipleOf", "expected a multiple of "+boundString(d))
	}
}

// isBelow is `a < b`, false when either is NaN.
func isBelow(a, b any) bool {
	order, ok := compareNumeric(a, b)
	return ok && order < 0
}

// isBelowOrEqual is `a <= b`, false when either is NaN.
func isBelowOrEqual(a, b any) bool {
	order, ok := compareNumeric(a, b)
	return ok && order <= 0
}

// boundString is String(bound): a number as JavaScript writes it, a bigint in decimal.
func boundString(bound any) string {
	if i, ok := bound.(*big.Int); ok {
		return i.String()
	}
	n, _ := asNumber(bound)
	return numberString(n)
}

func (e *explainer) walkObject(node jsonObject, b string, v jsonObject, path string, out *[]ValidationError, push pusher, pushHere func(keyword, message string)) {
	properties, _ := node.objectMember("properties")
	required, _ := node.arrayMember("required")
	for _, k := range required {
		name, ok := k.(string)
		if !ok {
			continue
		}
		// Present and holding undefined: missing unless its schema takes undefined (see declaredObject).
		x, present := v.get(name)
		accepted := present && isUndefined(x) && properties.has(name) && e.ok(properties.value(name), b, Undefined)
		if !present || (isUndefined(x) && !accepted) {
			push("required", "missing", pointer(path, name))
		}
	}
	for _, k := range properties.keys() {
		if x, ok := v.get(k); ok && !isUndefined(x) {
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
			push("contains", containsMessage(min, max))
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
	// A tagged union reports the branch its tag selects; otherwise an object is blamed on the branch that declares most of its properties (the one it was most likely meant for), then on the branch whose first error is deepest.
	if key, values, ok := e.tagOf(branches, b); ok {
		if vo, isObj := asObject(v); isObj {
			index := slices.IndexFunc(values, func(x any) bool { return strictEquals(x, vo.value(key)) && vo.has(key) })
			if index < 0 {
				*out = append(*out, ValidationError{Path: pointer(path, key), Keyword: keyword, Message: "expected one of " + listValues(values)})
			} else {
				e.walk(branches[index], b, v, path, out)
			}
			return
		}
	}
	var best []ValidationError
	bestDeclared := 0
	vo, isObj := asObject(v)
	for _, s := range branches {
		var errors []ValidationError
		e.walk(s, b, v, path, &errors)
		if len(errors) == 0 {
			continue
		}
		declared := 0
		if isObj {
			declared = e.declaredKeys(s, b, vo)
		}
		if best == nil || declared > bestDeclared || (declared == bestDeclared && utf16Len(errors[0].Path) > utf16Len(best[0].Path)) {
			best, bestDeclared = errors, declared
		}
	}
	if best == nil {
		best = []ValidationError{{Path: path, Keyword: keyword, Message: "matches no alternative"}}
	}
	*out = append(*out, best...)
}

// declaredKeys is how many of the keys of v the branch s (through a `$ref`) declares in `properties`.
func (e *explainer) declaredKeys(s any, b string, v jsonObject) int {
	node := s
	if o, ok := isSchemaObject(s); ok {
		if ref, ok := o.stringMember("$ref"); ok {
			// the branch's own base, as its `$id` sets it (Pi main 2bd2c2d0a).
			node = e.c.r.resolve(ref, e.c.r.baseFor(o, b)).node
		}
	}
	o, ok := isSchemaObject(node)
	if !ok {
		return 0
	}
	properties, ok := o.objectMember("properties")
	if !ok {
		return 0
	}
	n := 0
	for _, k := range v.keys() {
		if properties.has(k) {
			n++
		}
	}
	return n
}

// tagOf is the key and tag values of a union whose branches carry a distinct primitive const on one property of the first branch.
func (e *explainer) tagOf(branches []any, b string) (string, []any, bool) {
	resolvedBranches := make([]any, len(branches))
	for i, s := range branches {
		resolvedBranches[i] = s
		if o, ok := isSchemaObject(s); ok {
			if ref, ok := o.stringMember("$ref"); ok {
				// the branch's own base, as its `$id` sets it (Pi main 2bd2c2d0a).
				resolvedBranches[i] = e.c.r.resolve(ref, e.c.r.baseFor(o, b)).node
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
			// b already applies the node's `$id`; the copy must not apply it again (Pi main 2bd2c2d0a).
			if k != "unevaluatedProperties" && k != "unevaluatedItems" && k != "$id" {
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
		// NaN and Infinity are named as String(v) names them (Pi main 55a5c6914).
		if !isFinite(n) {
			return jsnumberName(n)
		}
		return "number"
	}
	switch v.(type) {
	case []any:
		return "array"
	case []byte:
		return "bytes"
	case *big.Int:
		return "bigint"
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

// jsnumberName is String(n) for a NaN or an infinity.
func jsnumberName(n float64) string {
	switch {
	case math.IsNaN(n):
		return "NaN"
	case n > 0:
		return "Infinity"
	}
	return "-Infinity"
}
