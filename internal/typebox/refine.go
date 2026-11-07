package typebox

// pig divergence (D226): TypeBox's `Type.Refine` refinements run in Value.Check and Value.Errors, which the SDKs of Go, Rust, Python and Lisp serve through the host.
//
// A refinement of a schema node is a function of the extension process, which the host cannot call from here, so the walkers take a RefineHook that answers each call. The SDK-facing
// layer (internal/typebox/engine, value_check.go) answers the hook from a replay of the answers the extension already gave, and asks for the next one when the walk reaches a call
// that has none yet. The walk is deterministic, so a call is made once, in the order TypeBox makes it.
//
// RefineCheck ports build/schema/engine/schema.mjs CheckSchema (typebox 1.3.27) for Value.Check: keywords run in the engine's order and stop at the first failure (an && chain), the
// operands of allOf, anyOf and oneOf all run, and a node's `~refine` list runs last and only when every other keyword of the node passed. The leaf keywords (const, enum, format, pattern, lengths,
// numeric bounds, item and property counts, uniqueItems) call checkKeyword (keywords.go), as the Errors walker does.
// `$ref` resolves a "#" fragment pointer, an `$id` or an `$anchor` of the root schema; $recursiveRef, $dynamicRef and refs into other documents resolve to nothing.

import (
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// RefineKey is the member of a schema node that lists its refinements: the node's `~refine`, as the ids the hook is called with (float64 in a list).
const RefineKey = "~refine"

// RefineHook runs one refinement. id is the entry of the node's RefineKey list; message false asks `check(value)` (a bool), true asks `error(value)` (a string). A hook that cannot
// answer yet may panic: the walkers do not recover.
type RefineHook func(id int, message bool, value any) any

type checkSets struct {
	keys    map[string]bool
	indices map[int]bool
}

// checkContext is TypeBox's CheckContext: the keys and indices each level has evaluated, which unevaluatedItems and unevaluatedProperties read.
type checkContext struct{ stack []checkSets }

func newCheckContext() *checkContext {
	return &checkContext{stack: []checkSets{{keys: map[string]bool{}, indices: map[int]bool{}}}}
}

func (c *checkContext) push() bool {
	c.stack = append(c.stack, checkSets{keys: map[string]bool{}, indices: map[int]bool{}})
	return true
}

func (c *checkContext) pop() bool {
	c.stack = c.stack[:len(c.stack)-1]
	return true
}

func (c *checkContext) top() checkSets { return c.stack[len(c.stack)-1] }

func (c *checkContext) addKey(key string) bool {
	c.top().keys[key] = true
	return true
}

func (c *checkContext) addIndex(index int) bool {
	c.top().indices[index] = true
	return true
}

func (c *checkContext) merge(results []*checkContext) bool {
	for _, result := range results {
		for key := range result.top().keys {
			c.top().keys[key] = true
		}
		for index := range result.top().indices {
			c.top().indices[index] = true
		}
	}
	return true
}

// A walk of a schema that refers to itself with no progress into the value (`{"$ref":"#"}`, `{"$id":"A","$ref":"A"}`) never ends. TypeBox's own walk ends in V8's "Maximum call stack size
// exceeded" RangeError, and a walk here without a check would end in Go's fatal stack overflow, which no recover catches and which would take the host down. The walkers keep the
// (schema node, value) pairs of the frames on the current path; entering a pair that is already on it is such a cycle, the walk is deterministic, and the panic is a *StackOverflowError.
// A value is its own identity when it is a non-empty object or array; a scalar or an empty container has no children, so only one frame of the path can hold it.
//
// MaxFrames is the safety net for everything else: the frames a path may hold. V8's stack ends a TypeBox walk at about 3,000 frames (the deepest value typebox 1.3.27 checks, a
// cyclic linked list 937 levels deep, takes about 3 frames a level here), so a path of 20,000 frames is nearly seven times what V8 allows and one only Go's growable stack could finish;
// the net keeps a pathological schema from growing a goroutine's stack toward Go's 1 GB limit, at which Go ends the process.
const MaxFrames = 20_000

// StackOverflowError is the panic of a walk that cycled or went deeper than MaxFrames, what a JavaScript RangeError "Maximum call stack size exceeded" is.
type StackOverflowError struct{}

func (*StackOverflowError) Error() string { return "RangeError: Maximum call stack size exceeded" }

// frameKey identifies a frame of the path: the schema node and the value it checks.
type frameKey struct{ schema, value uintptr }

// walkPath is the frames of the current path of a walk.
type walkPath struct {
	frames map[frameKey]struct{}
	depth  int
}

// identity is the address that identifies a schema node or a container value; zero for a value that has none.
func identity(value any) uintptr {
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Pointer, reflect.Map:
		return reflected.Pointer()
	case reflect.Slice:
		if reflected.Len() > 0 {
			return reflected.Pointer()
		}
	}
	return 0
}

// descend enters the frame that checks value against schema and returns the function that leaves it.
func (w errorWalker) descend(schema, value any) func() {
	path := w.path
	path.depth++
	if path.depth > MaxFrames {
		panic(&StackOverflowError{})
	}
	key := frameKey{identity(schema), identity(value)}
	if key.schema == 0 {
		return func() { path.depth-- }
	}
	if _, cycle := path.frames[key]; cycle {
		panic(&StackOverflowError{})
	}
	path.frames[key] = struct{}{}
	return func() {
		delete(path.frames, key)
		path.depth--
	}
}

// RefineCheck is Value.Check(schema, value) with the refinements of the schema run through hook.
func RefineCheck(schema, value any, hook RefineHook) bool {
	var orders map[uintptr][]string
	plain := orderedPlain(value, &orders)
	walker := errorWalker{root: schema, orders: orders, refine: hook, path: newWalkPath()}
	return walker.check(newCheckContext(), schema, plain)
}

// RefineErrors is Value.Errors(schema, value) with the refinements of the schema run through hook: a failing refinement adds an error of keyword `~refine`.
func RefineErrors(schema, value any, hook RefineHook) []ValueError {
	var orders map[uintptr][]string
	plain := orderedPlain(value, &orders)
	walker := errorWalker{root: schema, orders: orders, refine: hook, path: newWalkPath()}
	context := &errorContext{}
	walker.schema(context, nil, "", schema, plain)
	return context.errors
}

// valueOrdered is a value as the hook gets it: objects keep the member order the value arrived in (the walkers hold them as maps and a table of orders).
func (w errorWalker) valueOrdered(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := chordjson.NewObject(len(typed))
		for _, key := range w.keys(typed) {
			out.Set(key, w.valueOrdered(typed[key]))
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = w.valueOrdered(item)
		}
		return out
	}
	return value
}

// refinementIDs are the ids of a node's refinements, in the order they were added.
func refinementIDs(n node) []int {
	items, ok := n.list(RefineKey)
	if !ok {
		return nil
	}
	ids := make([]int, 0, len(items))
	for _, item := range items {
		if id, isNumber := item.(float64); isNumber {
			ids = append(ids, int(id))
		}
	}
	return ids
}

// runRefine asks the hook for the check of refinement id, on value.
func (w errorWalker) runRefine(id int, value any) bool {
	passed, _ := w.refine(id, false, w.valueOrdered(value)).(bool)
	return passed
}

func (w errorWalker) check(context *checkContext, schema, value any) bool {
	defer w.descend(schema, value)()
	if boolean, ok := schema.(bool); ok {
		return boolean
	}
	n, ok := asNode(schema)
	if !ok {
		return true
	}
	if typeValue, has := n.get("type"); has && !w.typeOK(typeValue, value) {
		return false
	}
	if object, isObject := value.(map[string]any); isObject && !w.checkObject(context, n, object) {
		return false
	}
	if items, isArray := value.([]any); isArray && !w.checkArray(context, n, items) {
		return false
	}
	if text, isString := value.(string); isString {
		for _, key := range []string{"maxLength", "minLength", "format", "pattern"} {
			if n.has(key) && !checkKeyword(n, key, text) {
				return false
			}
		}
	}
	if _, isNumber := value.(float64); isNumber {
		for _, key := range []string{"exclusiveMaximum", "exclusiveMinimum", "maximum", "minimum", "multipleOf"} {
			if n.has(key) && !checkKeyword(n, key, value) {
				return false
			}
		}
	}
	if ref, has := n.get("$ref"); has && !w.checkRef(context, ref, value) {
		return false
	}
	for _, key := range []string{"const", "enum"} {
		if n.has(key) && !checkKeyword(n, key, value) {
			return false
		}
	}
	if condition, has := n.get("if"); has {
		thenSchema, hasThen := n.get("then")
		if !hasThen {
			thenSchema = true
		}
		elseSchema, hasElse := n.get("else")
		if !hasElse {
			elseSchema = true
		}
		branch := elseSchema
		if w.check(context, condition, value) {
			branch = thenSchema
		}
		if !w.check(context, branch, value) {
			return false
		}
	}
	if negated, has := n.get("not"); has {
		next := newCheckContext()
		if w.check(next, negated, value) {
			return false
		}
		context.merge([]*checkContext{next})
	}
	for _, keyword := range []string{"allOf", "anyOf", "oneOf"} {
		operands, has := n.list(keyword)
		if !has {
			continue
		}
		var passed []*checkContext
		for _, operand := range operands {
			next := newCheckContext()
			if w.check(next, operand, value) {
				passed = append(passed, next)
			}
		}
		switch keyword {
		case "allOf":
			if len(passed) != len(operands) {
				return false
			}
		case "anyOf":
			if len(passed) == 0 {
				return false
			}
		default:
			if len(passed) != 1 {
				return false
			}
		}
		context.merge(passed)
	}
	if n.has("unevaluatedItems") {
		if items, isArray := value.([]any); isArray && !w.checkUnevaluatedItems(context, n, items) {
			return false
		}
	}
	if n.has("unevaluatedProperties") && !w.checkUnevaluatedProperties(context, n, value) {
		return false
	}
	if w.refine != nil {
		for _, id := range refinementIDs(n) {
			if !w.runRefine(id, value) {
				return false
			}
		}
	}
	return true
}

func (w errorWalker) typeOK(typeValue, value any) bool {
	if names, ok := typeValue.([]any); ok {
		for _, name := range names {
			if text, _ := name.(string); typeMatches(text, value) {
				return true
			}
		}
		return false
	}
	name, _ := typeValue.(string)
	return typeMatches(name, value)
}

func (w errorWalker) checkPush(context *checkContext, schema, value any) bool {
	return context.push() && w.check(context, schema, value) && context.pop()
}

// additionalMatcher is additionalProperties.mjs GetPropertiesPattern: the keys that properties and patternProperties name.
func additionalMatcher(n node) *regexp.Regexp {
	var known []string
	if patterns, ok := n.child("patternProperties"); ok {
		known = append(known, patterns.keys()...)
	}
	if properties, ok := n.child("properties"); ok {
		for _, key := range properties.keys() {
			known = append(known, "^"+regexp.QuoteMeta(key)+"$")
		}
	}
	pattern := `[^\x00-\x{10FFFF}]`
	if len(known) > 0 {
		pattern = "(" + strings.Join(known, "|") + ")"
	}
	matcher, _ := CompilePattern(pattern)
	return matcher
}

func (w errorWalker) checkObject(context *checkContext, n node, object map[string]any) bool {
	if required, ok := n.list("required"); ok {
		for _, name := range stringsOf(required) {
			if _, exists := object[name]; !exists {
				return false
			}
		}
	}
	if additional, ok := n.get("additionalProperties"); ok {
		matcher := additionalMatcher(n)
		for _, key := range w.keys(object) {
			if matcher != nil && matcher.MatchString(key) {
				continue
			}
			if !w.checkPush(context, additional, object[key]) || !context.addKey(key) {
				return false
			}
		}
	}
	for _, keyword := range []string{"dependencies", "dependentRequired", "dependentSchemas"} {
		dependencies, ok := n.child(keyword)
		if !ok || len(object) == 0 {
			continue
		}
		for _, key := range dependencies.keys() {
			if _, exists := object[key]; !exists {
				continue
			}
			dependency, _ := dependencies.get(key)
			if names, isList := dependency.([]any); isList {
				for _, name := range stringsOf(names) {
					if _, exists := object[name]; !exists {
						return false
					}
				}
				continue
			}
			if keyword != "dependentRequired" && !w.check(context, dependency, object) {
				return false
			}
		}
	}
	if patterns, ok := n.child("patternProperties"); ok {
		for _, pattern := range patterns.keys() {
			matcher, err := CompilePattern(pattern)
			patternSchema, _ := patterns.get(pattern)
			for _, key := range w.keys(object) {
				if err == nil && matcher.MatchString(key) && (!w.checkPush(context, patternSchema, object[key]) || !context.addKey(key)) {
					return false
				}
			}
		}
	}
	if properties, ok := n.child("properties"); ok {
		for _, key := range properties.keys() {
			item, exists := object[key]
			if !exists {
				continue
			}
			propertySchema, _ := properties.get(key)
			if !w.checkPush(context, propertySchema, item) || !context.addKey(key) {
				return false
			}
		}
	}
	if names, ok := n.get("propertyNames"); ok {
		for _, key := range w.keys(object) {
			if !w.check(context, names, key) {
				return false
			}
		}
	}
	for _, key := range []string{"minProperties", "maxProperties"} {
		if n.has(key) && !checkKeyword(n, key, object) {
			return false
		}
	}
	return true
}

func (w errorWalker) checkArray(context *checkContext, n node, items []any) bool {
	tuple, isTuple := n.list("items")
	if additional, ok := n.get("additionalItems"); ok && isTuple {
		for index, item := range items {
			if index >= len(tuple) && (!w.checkPush(context, additional, item) || !context.addIndex(index)) {
				return false
			}
		}
	}
	contains, hasContains := n.get("contains")
	if hasContains {
		minContains, hasMin := n.get("minContains")
		if !(hasMin && Plain(minContains) == float64(0)) {
			some := false
			for index, item := range items {
				if w.check(context, contains, item) && context.addIndex(index) {
					some = true
				}
			}
			if len(items) == 0 || !some {
				return false
			}
		}
	}
	if itemsSchema, ok := n.get("items"); ok {
		if isTuple {
			for index, schema := range tuple {
				if len(items) > index && (!w.checkPush(context, schema, items[index]) || !context.addIndex(index)) {
					return false
				}
			}
		} else {
			offset := 0
			if prefix, has := n.list("prefixItems"); has {
				offset = len(prefix)
			}
			for index := offset; index < len(items); index++ {
				if !w.checkPush(context, itemsSchema, items[index]) || !context.addIndex(index) {
					return false
				}
			}
		}
	}
	if hasContains && n.has("maxContains") {
		limit, _ := Plain(n.declared("maxContains")).(float64)
		count := 0
		for _, item := range items {
			if w.check(context, contains, item) {
				count++
			}
		}
		if float64(count) > limit {
			return false
		}
	}
	if n.has("maxItems") && !checkKeyword(n, "maxItems", items) {
		return false
	}
	if hasContains && n.has("minContains") {
		limit, _ := Plain(n.declared("minContains")).(float64)
		count := 0
		for index, item := range items {
			if w.check(context, contains, item) && context.addIndex(index) {
				count++
			}
		}
		if float64(count) < limit {
			return false
		}
	}
	if n.has("minItems") && !checkKeyword(n, "minItems", items) {
		return false
	}
	if prefix, ok := n.list("prefixItems"); ok && len(items) > 0 {
		for index, schema := range prefix {
			if len(items) > index && (!w.checkPush(context, schema, items[index]) || !context.addIndex(index)) {
				return false
			}
		}
	}
	if unique, _ := n.get("uniqueItems"); unique == true && !checkKeyword(n, "uniqueItems", items) {
		return false
	}
	return true
}

func (w errorWalker) checkRef(context *checkContext, ref, value any) bool {
	target := w.resolveRef(ref)
	if target == nil {
		return false
	}
	if _, isNode := asNode(target); !isNode {
		if _, isBool := target.(bool); !isBool {
			return false
		}
	}
	next := newCheckContext()
	if !w.check(next, target, value) {
		return false
	}
	context.merge([]*checkContext{next})
	return true
}

// resolveRef finds the target of a $ref: a "#" or "#/pointer" of the root schema, the node whose $id is the reference (TypeBox's Type.Ref and Cyclic name their definitions with
// an $id), or the node whose $anchor the fragment names.
func (w errorWalker) resolveRef(ref any) any {
	text, _ := ref.(string)
	if strings.HasPrefix(text, "#/") || text == "#" || text == "" {
		return w.resolve(ref)
	}
	if anchor, ok := strings.CutPrefix(text, "#"); ok {
		return findByKeyword(w.root, "$anchor", anchor)
	}
	return findByKeyword(w.root, "$id", text)
}

// findByKeyword is the first node of the schema, in document order, whose keyword equals want.
func findByKeyword(schema any, keyword, want string) any {
	if list, ok := schema.([]any); ok {
		for _, item := range list {
			if found := findByKeyword(item, keyword, want); found != nil {
				return found
			}
		}
		return nil
	}
	n, ok := asNode(schema)
	if !ok {
		return nil
	}
	if value, has := n.get(keyword); has {
		if text, _ := value.(string); text == want {
			return schema
		}
	}
	for _, key := range n.keys() {
		if key == "const" || key == "enum" {
			continue
		}
		member, _ := n.get(key)
		if found := findByKeyword(member, keyword, want); found != nil {
			return found
		}
	}
	return nil
}

func (w errorWalker) checkUnevaluatedItems(context *checkContext, n node, items []any) bool {
	indices := context.top().indices
	extra, _ := n.get("unevaluatedItems")
	for index, item := range items {
		if (!indices[index] && !w.check(context, extra, item)) || !context.addIndex(index) {
			return false
		}
	}
	return true
}

func (w errorWalker) checkUnevaluatedProperties(context *checkContext, n node, value any) bool {
	extra, _ := n.get("unevaluatedProperties")
	keys := context.top().keys
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range w.keys(typed) {
			if !keys[key] && (!w.check(context, extra, typed[key]) || !context.addKey(key)) {
				return false
			}
		}
	case []any:
		for index, item := range typed {
			key := strconv.Itoa(index)
			if !keys[key] && (!w.check(context, extra, item) || !context.addKey(key)) {
				return false
			}
		}
	}
	return true
}

func newWalkPath() *walkPath { return &walkPath{frames: map[frameKey]struct{}{}} }
