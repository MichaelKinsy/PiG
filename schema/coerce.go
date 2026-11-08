package schema

// Ports packages/schema/src/coerce.ts
//
// Coercion: turns a loosely typed value (a model's tool call arguments, form input, query strings) into the value a schema most likely meant, before it is checked. Only a CoercingValidator has it, and it builds it on its first Coerce or Parse; the check path never runs it.
//
// Three passes, in this order:
//  1. Optional nulls: a null in an optional property whose schema rejects null is dropped, as if omitted.
//  2. Conversion rules ported from TypeBox 1.3.27's Value.Convert (MIT), keyed on JSON Schema structure (`$ref`, `const`, `enum`, `anyOf`/`oneOf`, `allOf`, `type`, `items`/`prefixItems`, `properties`/`patternProperties`) instead of TypeBox's kind marks: null becomes "null" for a string, "3.5" becomes 3 for an integer, a single value becomes a one-item array, and a union takes the first branch whose conversion passes.
//  3. Primitive coercion by `type` (also type arrays), as in Ajv's `coerceTypes`: what pass 2 left, for example a type array ["integer", "null"], or "" for null.
//
// A value that cannot be coerced is left as it is; the check then reports it.
//
// Every pass is copy-on-write: an object or array is copied only when one of its members changes, so an unchanged value comes back as the same reference and the input is never modified. All passes walk the same keywords for members: `properties`, then `patternProperties`, then `additionalProperties` for keys neither names; tuple items by `prefixItems` (or a draft-7 `items` array), the rest by `items`. Draft 7's `additionalItems` is not coerced.
//
// Go has no `undefined` and no bigint, so the places where Pi reads them have no counterpart: a member is absent or it has a value, and the bigint branches of the conversion helpers cannot be reached.

import (
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/schema/internal/ecmaregex"
)

// coercer coerces values for one compiled schema. It is safe for concurrent use: the state of one Coerce call is a run, and everything shared is built once and read afterwards, or guarded by mu.
type coercer struct {
	c      *compiler
	schema JsonSchema
	// serial reports that checks run one at a time: a schema with a bookended `$dynamicRef` tracks the dynamic scope in state a check mutates, and compiles its dynamic targets on first use.
	serial bool
	// mu guards the compiler and its resolver (compiling a node's check, resolving a reference, reading the indexed bases), which build their caches on demand, and runs the checks of a serial schema.
	mu sync.Mutex
	// infos maps a schema object (by identity) to its parsed keywords, built on the first visit.
	infos sync.Map
}

func newCoercer(c *compiler, schema JsonSchema) *coercer {
	// Indexing the schema now, before any concurrent use, makes the resolver's maps read-only for the bases the coercer reads. Pi indexes on the first reference; a schema that never reaches one never reads a base.
	c.r.ensureIndexed()
	return &coercer{c: c, schema: schema, serial: c.dynamic}
}

// nodeInfo is one schema object's keywords, parsed once.
type nodeInfo struct {
	node jsonObject
	r    *resolver
	// base is the base URI the object was indexed under; indexed is false for an object the index never reached, which keeps the base of the schema it was reached from.
	base    string
	indexed bool

	hasRef bool
	ref    string

	hasConst bool
	constant any

	isEnum bool
	enum   []any

	isAnyOf, isOneOf, isAllOf bool
	anyOf, oneOf, allOf       []any

	// typeAbsent is `node.type === undefined`; typeName holds a string `type`; any other `type` (an array, say) is neither.
	typeAbsent bool
	typeName   string
	typeString bool

	// Keyword presence, as `node.keyword !== undefined`.
	hasProperties, hasPatternProperties, hasAdditionalProperties bool
	hasItems, hasPrefixItems                                     bool

	items                 any
	itemsIsArray          bool
	itemsArray            []any
	prefixIsArray         bool
	prefixItems           []any
	typesOfSchema         []string
	typeObject, typeArray bool
	// declaresMembers is whether a union or intersection node also declares object members.
	declaresMembers bool

	members    atomic.Pointer[memberInfo]
	check      atomic.Pointer[checkEntry]
	resolution atomic.Pointer[refEntry]
}

// memberInfo is what an object value is walked by: `properties`, `patternProperties`, `additionalProperties` and `required`, parsed on the first object value that reaches the schema (a pattern that is not a regular expression fails the call there, as in Pi).
type memberInfo struct {
	properties    jsonObject
	hasProperties bool
	propKeys      []string
	propSchemas   []any
	propIsRef     []bool
	patterns      []patternMember
	additional    any
	hasAdditional bool
	required      []string
}

type patternMember struct {
	re     *ecmaregex.Regexp
	schema any
}

// checkEntry is a node's compiled check; ok is false for a node that cannot be compiled (Pi's `passes` is undefined for it).
type checkEntry struct {
	check Check
	ok    bool
}

// refEntry is a resolved `$ref`, for the base it was resolved from.
type refEntry struct {
	base   string
	target resolved
	ok     bool
}

// info is the parsed keywords of a schema object.
func (co *coercer) info(o jsonObject) *nodeInfo {
	key := o.identity()
	if cached, ok := co.infos.Load(key); ok {
		return cached.(*nodeInfo)
	}
	info := &nodeInfo{node: o, r: co.c.r}
	co.mu.Lock()
	info.base, info.indexed = co.c.r.baseOf[key]
	co.mu.Unlock()
	info.ref, info.hasRef = o.stringMember("$ref")
	info.constant, info.hasConst = o.get("const")
	info.enum, info.isEnum = o.arrayMember("enum")
	info.anyOf, info.isAnyOf = o.arrayMember("anyOf")
	info.oneOf, info.isOneOf = o.arrayMember("oneOf")
	info.allOf, info.isAllOf = o.arrayMember("allOf")
	typeValue, hasType := o.get("type")
	info.typeAbsent = !hasType
	switch t := typeValue.(type) {
	case string:
		info.typeName, info.typeString = t, true
		info.typesOfSchema = []string{t}
	case []any:
		for _, item := range t {
			if name, ok := item.(string); ok {
				info.typesOfSchema = append(info.typesOfSchema, name)
			}
		}
	}
	info.typeObject = slices.Contains(info.typesOfSchema, "object")
	info.typeArray = slices.Contains(info.typesOfSchema, "array")
	info.hasProperties = o.has("properties")
	info.hasPatternProperties = o.has("patternProperties")
	info.hasAdditionalProperties = o.has("additionalProperties")
	info.items, info.hasItems = o.get("items")
	info.itemsArray, info.itemsIsArray = info.items.([]any)
	var prefix any
	prefix, info.hasPrefixItems = o.get("prefixItems")
	info.prefixItems, info.prefixIsArray = prefix.([]any)
	info.declaresMembers = info.hasProperties || info.hasPatternProperties || info.hasAdditionalProperties
	actual, _ := co.infos.LoadOrStore(key, info)
	return actual.(*nodeInfo)
}

// baseOr is the base URI of the node whose parent has base: the indexed one, else the one its `$id` gives, else base (Pi's `baseOf` reads
// `baseFor`; the schema is indexed by now, so reading it mutates nothing).
func (info *nodeInfo) baseOr(base string) string {
	if info.indexed {
		return info.base
	}
	// Pi main 2bd2c2d0a resolves the base of an object the index never reached through baseFor, not the parent's base.
	return info.r.baseFor(info.node, base)
}

// memberInfo parses the object members of the node. A pattern that is not a regular expression fails the compile-error way (the Coerce call returns it).
func (info *nodeInfo) memberInfo() *memberInfo {
	if m := info.members.Load(); m != nil {
		return m
	}
	m := &memberInfo{}
	if properties, ok := info.node.objectMember("properties"); ok {
		m.properties, m.hasProperties = properties, true
		m.propKeys = properties.keys()
		m.propSchemas = make([]any, len(m.propKeys))
		m.propIsRef = make([]bool, len(m.propKeys))
		for i, key := range m.propKeys {
			property := properties.value(key)
			m.propSchemas[i] = property
			if o, ok := isSchemaObject(property); ok {
				_, m.propIsRef[i] = o.stringMember("$ref")
			}
		}
	}
	if patterns, ok := info.node.objectMember("patternProperties"); ok {
		for _, pattern := range patterns.keys() {
			m.patterns = append(m.patterns, patternMember{re: sharedRegex(pattern), schema: patterns.value(pattern)})
		}
	}
	if additional, ok := info.node.objectMember("additionalProperties"); ok {
		m.additional, m.hasAdditional = objectValue(additional), true
	}
	if required, ok := info.node.arrayMember("required"); ok {
		for _, name := range required {
			if s, ok := name.(string); ok {
				m.required = append(m.required, s)
			}
		}
	}
	info.members.Store(m)
	return m
}

// hasProperty is `Object.hasOwn(properties, key)`, with a missing `properties` as `{}`.
func (m *memberInfo) hasProperty(key string) bool {
	return m.hasProperties && m.properties.has(key)
}

// property is the schema of a declared property.
func (m *memberInfo) property(key string) (any, bool) {
	if !m.hasProperties {
		return nil, false
	}
	return m.properties.get(key)
}

// itemSchemaAt is the schema of an array's item at index: a tuple's (`prefixItems`, or a draft-7 `items` array) for its prefix, `items` after `prefixItems`, or `items` for every item. Not found when nothing applies.
func (info *nodeInfo) itemSchemaAt(index int) (any, bool) {
	if info.itemsIsArray {
		if index < len(info.itemsArray) {
			return info.itemsArray[index], true
		}
		return nil, false
	}
	if info.prefixIsArray && index < len(info.prefixItems) {
		return info.prefixItems[index], true
	}
	return info.items, info.hasItems
}

// checkOf is the compiled check of a node, compiled on first use under the compiler's lock.
func (co *coercer) checkOf(info *nodeInfo, base string) *checkEntry {
	if entry := info.check.Load(); entry != nil {
		return entry
	}
	co.mu.Lock()
	defer co.mu.Unlock()
	if entry := info.check.Load(); entry != nil {
		return entry
	}
	entry := &checkEntry{}
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				if _, isCompileError := recovered.(compileError); !isCompileError {
					panic(recovered)
				}
				co.c.reset()
			}
		}()
		entry.check = co.c.check(objectValue(info.node), info.baseOr(base))
		entry.ok = true
	}()
	info.check.Store(entry)
	return entry
}

// resolveRef resolves the `$ref` of a node from base; ok is false when the reference cannot be resolved.
func (co *coercer) resolveRef(info *nodeInfo, base string) (resolved, bool) {
	if entry := info.resolution.Load(); entry != nil && entry.base == base {
		return entry.target, entry.ok
	}
	co.mu.Lock()
	defer co.mu.Unlock()
	entry := &refEntry{base: base}
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				if _, isCompileError := recovered.(compileError); !isCompileError {
					panic(recovered)
				}
			}
		}()
		entry.target, entry.ok = co.c.r.resolve(info.ref, base), true
	}()
	info.resolution.Store(entry)
	return entry.target, entry.ok
}

// coerce is value coerced: a copy along the changed paths only, or value itself when nothing changes. The error is a pattern of the schema that is not a regular expression, or a dynamic reference that cannot be compiled, reached by the value.
func (co *coercer) coerce(value any) (out any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = compileFailure(recovered)
		}
	}()
	var r run
	r.co, r.keys = co, borrowKeys()
	defer returnKeys(r.keys)
	base := co.c.r.rootBase
	withoutNulls := r.dropOptionalNulls(value, co.schema, base)
	return r.coerceTypes(r.convert(co.schema, base, withoutNulls), co.schema, base), nil
}

// convertOnly is pass 2 alone, as TypeBox's Value.Convert (the TypeBox conformance tests run it).
func (co *coercer) convertOnly(value any) (out any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = compileFailure(recovered)
		}
	}()
	var r run
	r.co, r.keys = co, borrowKeys()
	defer returnKeys(r.keys)
	return r.convert(co.schema, co.c.r.rootBase, value), nil
}

// run is the state of one Coerce call.
type run struct {
	co *coercer
	// entered holds the (schema node, value) pairs pass 2 is converting through a re-entry point, as a stack. Conversion can enter a node again with the same value only through a `$ref` or by wrapping a single value into an array (also for schemas that cycle through shared objects): a recursive schema that meets a value no branch accepts would convert it without end. A pair already on the stack (primitives by value, objects by reference) leaves the value unchanged. Only those two points push, so schemas without references or wraps pay nothing.
	entered []enteredPair
	// cuts counts how often a re-entry was cut short in this call; results computed across one are not memoized.
	cuts int
	// memo holds the results of `$ref` targets by (node, value) within one call: a recursive union whose branches all convert the same children (and all fail) would convert them once per branch, exponentially in the depth.
	memo map[memoKey]any
	// keys is the stack of the key lists of the objects being walked (see pushKeys).
	keys *keyBuffer
}

type enteredPair struct {
	node  nodeKey
	value valueKey
}

type memoKey struct {
	node  nodeKey
	value valueKey
}

// valueKey is the identity of a value for the re-entry stack and the memo: a primitive by value, an object or an array by reference. kind is kindNone for a value that has no identity (it never matches).
type valueKey struct {
	kind uint8
	n    int
	bits uint64
	ptr  nodeKey
	s    string
}

// arrayPointer is the identity of an array: where its first member is (none for an empty array).
func arrayPointer(list []any) nodeKey {
	if len(list) == 0 {
		return nil
	}
	return nodeKey(&list[0])
}

const (
	kindNone uint8 = iota
	kindNull
	kindBool
	kindNumber
	kindString
	kindObject
	kindArray
)

// identityOf is the key of value. mergeZeros selects SameValueZero (the comparison of a Map key: +0 and -0 are one key) over Object.is (the stack's comparison); NaN is one key in both.
func identityOf(value any, mergeZeros bool) valueKey {
	switch x := value.(type) {
	case nil:
		return valueKey{kind: kindNull}
	case bool:
		if x {
			return valueKey{kind: kindBool, n: 1}
		}
		return valueKey{kind: kindBool}
	case string:
		return valueKey{kind: kindString, s: x}
	case *Object:
		return valueKey{kind: kindObject, ptr: nodeKey(x)}
	case map[string]any:
		return valueKey{kind: kindObject, ptr: reflect.ValueOf(x).UnsafePointer()}
	case []any:
		return valueKey{kind: kindArray, n: len(x), ptr: arrayPointer(x)}
	}
	if n, ok := asNumber(value); ok {
		switch {
		case math.IsNaN(n):
			return valueKey{kind: kindNumber, bits: 0x7ff8000000000001}
		case n == 0 && mergeZeros:
			return valueKey{kind: kindNumber}
		}
		return valueKey{kind: kindNumber, bits: math.Float64bits(n)}
	}
	return valueKey{}
}

// reenter is node converted for value at a re-entry point: unchanged when that pair is already being converted.
func (r *run) reenter(node jsonObject, base string, value any, memoize bool) any {
	nodeID := node.identity()
	valueID := identityOf(value, false)
	if valueID.kind != kindNone {
		for i := range r.entered {
			if entry := &r.entered[i]; entry.node == nodeID && entry.value == valueID {
				r.cuts++
				return value
			}
		}
	}
	var memoID memoKey
	if memoize {
		memoID = memoKey{node: nodeID, value: identityOf(value, true)}
		if result, ok := r.memo[memoID]; ok && memoID.value.kind != kindNone {
			return result
		}
	}
	cuts := r.cuts
	r.entered = append(r.entered, enteredPair{node: nodeID, value: valueID})
	result := r.convert(objectValue(node), base, value)
	r.entered = r.entered[:len(r.entered)-1]
	if memoize && r.cuts == cuts && memoID.value.kind != kindNone {
		if r.memo == nil {
			r.memo = map[memoKey]any{}
		}
		r.memo[memoID] = result
	}
	return result
}

type tri uint8

const (
	// passUndefined is Pi's `undefined`: the node cannot be compiled.
	passUndefined tri = iota
	passTrue
	passFalse
)

// passes reports whether value passes the schema node; passUndefined when node cannot be compiled.
func (r *run) passes(node any, base string, value any) tri {
	if b, ok := node.(bool); ok {
		if b {
			return passTrue
		}
		return passFalse
	}
	o, ok := isSchemaObject(node)
	if !ok {
		return passUndefined
	}
	return r.passesInfo(r.co.info(o), base, value)
}

func (r *run) passesInfo(info *nodeInfo, base string, value any) tri {
	co := r.co
	entry := co.checkOf(info, base)
	if !entry.ok {
		return passUndefined
	}
	if co.serial {
		return co.serialCheck(entry.check, value)
	}
	if entry.check(value) {
		return passTrue
	}
	return passFalse
}

// serialCheck runs a check of a schema that tracks the dynamic scope, one at a time; a dynamic target that cannot be compiled makes the result undefined, as Pi's `passes` catches it.
func (co *coercer) serialCheck(check Check, value any) (result tri) {
	co.mu.Lock()
	defer co.mu.Unlock()
	defer func() {
		if recovered := recover(); recovered != nil {
			if _, isCompileError := recovered.(compileError); !isCompileError {
				panic(recovered)
			}
			co.c.reset()
			result = passUndefined
		}
	}()
	if check(value) {
		return passTrue
	}
	return passFalse
}

// ---------------------------------------------------------------------------------------------------------------
// Copy on write
// ---------------------------------------------------------------------------------------------------------------

// arrayCopy copies an array on the first member that changes.
type arrayCopy struct {
	src, dst []any
}

func (c *arrayCopy) set(index int, next any) {
	if identical(next, c.src[index]) {
		return
	}
	if c.dst == nil {
		c.dst = slices.Clone(c.src)
	}
	c.dst[index] = next
}

// result is the copy, or original (the value the array came in) when no member changed.
func (c *arrayCopy) result(original any) any {
	if c.dst != nil {
		return c.dst
	}
	return original
}

// objectCopy copies an object on the first member that changes; the copy keeps the member order.
type objectCopy struct {
	src, dst jsonObject
	copied   bool
}

func (c *objectCopy) current() jsonObject {
	if c.copied {
		return c.dst
	}
	return c.src
}

func (c *objectCopy) ensure() {
	if !c.copied {
		c.dst, c.copied = cloneObject(c.src), true
	}
}

// set stores next under key unless it is the member already there.
func (c *objectCopy) set(key string, next any) {
	if current, present := c.current().get(key); present && identical(next, current) {
		return
	}
	c.ensure()
	setMember(c.dst, key, next)
}

// assign stores next under key in the copy.
func (c *objectCopy) assign(key string, next any) {
	c.ensure()
	setMember(c.dst, key, next)
}

func (c *objectCopy) remove(key string) {
	c.ensure()
	if c.dst.ordered != nil {
		c.dst.ordered.Delete(key)
		return
	}
	delete(c.dst.plain, key)
}

func (c *objectCopy) result(original any) any {
	if c.copied {
		return objectValue(c.dst)
	}
	return original
}

func cloneObject(o jsonObject) jsonObject {
	if o.ordered != nil {
		return jsonObject{ordered: o.ordered.Clone()}
	}
	clone := maps.Clone(o.plain)
	if clone == nil {
		clone = map[string]any{}
	}
	return jsonObject{plain: clone}
}

func setMember(o jsonObject, key string, value any) {
	if o.ordered != nil {
		o.ordered.Set(key, value)
		return
	}
	o.plain[key] = value
}

// objectValue is the JSON value of an object view.
func objectValue(o jsonObject) any {
	if o.ordered != nil {
		return o.ordered
	}
	return o.plain
}

// pushKeys pushes the keys of an object in own-key order, as Object.keys does, and returns them; the caller pops them with popKeys when it is done.
func (r *run) pushKeys(o jsonObject) []string {
	buffer := r.keys
	start := len(buffer.stack)
	if o.ordered != nil {
		buffer.stack = o.ordered.AppendKeys(buffer.stack)
	} else {
		buffer.stack = append(buffer.stack, chordjson.MapOwnKeys(o.plain)...)
	}
	return buffer.stack[start:len(buffer.stack):len(buffer.stack)]
}

func (r *run) popKeys(keys []string) {
	buffer := r.keys
	end := len(buffer.stack)
	clear(buffer.stack[end-len(keys) : end])
	buffer.stack = buffer.stack[:end-len(keys)]
}

// keyBuffer is a run's stack of key lists. Runs borrow one from keyBuffers, so walking objects allocates nothing once the pool is warm.
type keyBuffer struct{ stack []string }

var keyBuffers = sync.Pool{New: func() any { return new(keyBuffer) }}

// borrowKeys returns an empty key stack; the caller gives it back with returnKeys.
func borrowKeys() *keyBuffer { return keyBuffers.Get().(*keyBuffer) }

// maxPooledKeyStack bounds the key stack a run keeps pooled, in keys (golang/go#23199): one object with a huge key list does not stay pooled.
const maxPooledKeyStack = 1 << 10

func returnKeys(buffer *keyBuffer) {
	clear(buffer.stack)
	buffer.stack = buffer.stack[:0]
	if cap(buffer.stack) > maxPooledKeyStack {
		return
	}
	keyBuffers.Put(buffer)
}

// identical is `a === b`: primitives by value (NaN is never identical to itself), objects and arrays by reference.
func identical(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	case *Object:
		y, ok := b.(*Object)
		return ok && x == y
	case map[string]any:
		y, ok := b.(map[string]any)
		return ok && reflect.ValueOf(x).UnsafePointer() == reflect.ValueOf(y).UnsafePointer()
	case []any:
		y, ok := b.([]any)
		return ok && len(x) == len(y) && (len(x) == 0 || &x[0] == &y[0])
	case []byte:
		y, ok := b.([]byte)
		return ok && len(x) == len(y) && (len(x) == 0 || &x[0] == &y[0])
	}
	if x, ok := asNumber(a); ok {
		y, ok := asNumber(b)
		return ok && x == y
	}
	return false
}

// ---------------------------------------------------------------------------------------------------------------
// Pass 1: optional nulls
// ---------------------------------------------------------------------------------------------------------------

func (r *run) dropOptionalNulls(value any, schema any, base string) any {
	switch value.(type) {
	case []any, *Object, map[string]any:
	default:
		return value
	}
	node, ok := isSchemaObject(schema)
	if !ok {
		return value
	}
	info := r.co.info(node)
	b := info.baseOr(base)
	if list, ok := value.([]any); ok {
		copied := arrayCopy{src: list}
		for i, item := range list {
			if itemSchema, found := info.itemSchemaAt(i); found {
				copied.set(i, r.dropOptionalNulls(item, itemSchema, b))
			}
		}
		return copied.result(value)
	}
	object, _ := asObject(value)
	m := info.memberInfo()
	if len(m.patterns) == 0 && !m.hasAdditional && !m.hasProperties {
		return value
	}
	copied := objectCopy{src: object}
	for i, key := range m.propKeys {
		current, present := object.get(key)
		if !present {
			continue
		}
		property := m.propSchemas[i]
		if current == nil && !slices.Contains(m.required, key) && !m.propIsRef[i] && r.passes(property, b, nil) == passFalse {
			copied.remove(key)
			continue
		}
		copied.set(key, r.dropOptionalNulls(current, property, b))
	}
	// Values of pattern and additional properties are walked for their own optional nulls; the keys themselves are not optional properties, so they stay.
	if len(m.patterns) > 0 || m.hasAdditional {
		keys := r.pushKeys(object)
		for _, key := range keys {
			original, _ := object.get(key)
			matched := m.hasProperty(key)
			for _, pattern := range m.patterns {
				if !pattern.re.MatchString(key) {
					continue
				}
				matched = true
				if current, present := copied.current().get(key); present {
					copied.set(key, r.dropOptionalNulls(current, pattern.schema, b))
				}
			}
			if !matched && m.hasAdditional {
				copied.set(key, r.dropOptionalNulls(original, m.additional, b))
			}
		}
		r.popKeys(keys)
	}
	return copied.result(value)
}

// ---------------------------------------------------------------------------------------------------------------
// Pass 2: TypeBox's conversion rules
// ---------------------------------------------------------------------------------------------------------------

func (r *run) convert(node any, base string, value any) any {
	o, ok := isSchemaObject(node)
	if !ok {
		return value
	}
	info := r.co.info(o)
	b := info.baseOr(base)
	if info.hasRef {
		target, ok := r.co.resolveRef(info, b)
		if !ok {
			return value
		}
		targetNode, isObject := isSchemaObject(target.node)
		if !isObject {
			return value
		}
		return r.reenter(targetNode, target.base, value, true)
	}
	return r.convertNode(info, b, value)
}

// convertNode is one schema node's conversion: its first keyword among `const`, `enum`, the unions, `allOf` and `type`.
func (r *run) convertNode(info *nodeInfo, b string, value any) any {
	switch {
	case info.hasConst:
		return convertLiteral(info.constant, value)
	case info.isEnum:
		return r.convertEnum(info, b, value)
	// A union or intersection node may also declare members (plain JSON Schema; TypeBox's never do): they convert after the branches.
	case info.isAnyOf:
		return r.convertUnion(info, info.anyOf, b, value)
	case info.isOneOf:
		return r.convertUnion(info, info.oneOf, b, value)
	case info.isAllOf:
		next := value
		for _, member := range info.allOf {
			next = r.convert(member, b, next)
		}
		return r.convertMembers(info, b, next)
	case info.typeAbsent:
		if info.hasProperties || info.hasPatternProperties {
			return r.convertObject(info, b, value)
		}
		if info.hasItems || info.hasPrefixItems {
			return r.convertArray(info, b, value)
		}
		return value
	case !info.typeString:
		return value
	}
	switch info.typeName {
	case "string":
		if _, isString := value.(string); isString {
			return value
		}
		if text, ok := tryString(value); ok {
			return text
		}
	case "number":
		if _, isNumber := asNumber(value); isNumber {
			return value
		}
		if n, ok := tryNumber(value); ok {
			return n
		}
	case "integer":
		if n, isNumber := asNumber(value); isNumber {
			if truncated := math.Trunc(n); truncated != n && !math.IsNaN(n) && !math.IsInf(n, 0) {
				return truncated
			}
			return value
		}
		if n, ok := tryNumber(value); ok {
			return math.Trunc(n)
		}
	case "boolean":
		if _, isBool := value.(bool); isBool {
			return value
		}
		if b, ok := tryBoolean(value); ok {
			return b
		}
	case "null":
		if value == nil {
			return value
		}
		if tryNull(value) {
			return nil
		}
	case "array":
		return r.convertArray(info, b, value)
	case "object":
		return r.convertObject(info, b, value)
	}
	return value
}

// convertEnum converts to an enum, a union of its values: a member stays, else the first value the input converts to.
func (r *run) convertEnum(info *nodeInfo, base string, value any) any {
	for _, member := range info.enum {
		if identical(member, value) {
			return value
		}
	}
	for _, member := range info.enum {
		candidate := convertLiteral(member, value)
		if !identical(candidate, value) && r.passesInfo(info, base, candidate) == passTrue {
			return candidate
		}
	}
	return value
}

// convertUnion: a value one branch accepts stays; else the first branch's conversion that the whole union accepts. Branches are converted in order and only until one is accepted, so a later recursive branch is not entered needlessly. A node that also declares members (plain JSON Schema; TypeBox's never do) converts them into each candidate before it is checked, so a branch is not discarded for a sibling property that is converted after it.
func (r *run) convertUnion(info *nodeInfo, branches []any, base string, value any) any {
	for _, branch := range branches {
		if r.passes(branch, base, value) == passTrue {
			return r.convertMembers(info, base, value)
		}
	}
	for _, branch := range branches {
		candidate := r.convertMembers(info, base, r.convert(branch, base, value))
		if r.passesInfo(info, base, candidate) == passTrue {
			return candidate
		}
	}
	return r.convertMembers(info, base, value)
}

// convertMembers converts the object members of a union or intersection node, when it declares any.
func (r *run) convertMembers(info *nodeInfo, base string, value any) any {
	if info.declaresMembers {
		return r.convertObject(info, base, value)
	}
	return value
}

func (r *run) convertArray(info *nodeInfo, base string, value any) any {
	var prefix []any
	hasPrefix := false
	if info.itemsIsArray {
		prefix, hasPrefix = info.itemsArray, true
	} else if info.prefixIsArray {
		prefix, hasPrefix = info.prefixItems, true
	}
	list, isArray := value.([]any)
	if hasPrefix {
		// A tuple converts its items and never wraps a single value.
		if !isArray {
			return value
		}
		rest, hasRest := info.items, info.hasItems && !info.itemsIsArray
		copied := arrayCopy{src: list}
		for i, item := range list {
			schema, found := rest, hasRest
			if i < len(prefix) {
				schema, found = prefix[i], true
			}
			if found {
				copied.set(i, r.convert(schema, base, item))
			}
		}
		return copied.result(value)
	}
	if isArray {
		if !info.hasItems {
			return value
		}
		copied := arrayCopy{src: list}
		for i, item := range list {
			copied.set(i, r.convert(info.items, base, item))
		}
		return copied.result(value)
	}
	// The wrapped single value is converted by the item schema with the same value: a re-entry point.
	if item, ok := isSchemaObject(info.items); ok {
		return []any{r.reenter(item, base, value, false)}
	}
	return []any{value}
}

func (r *run) convertObject(info *nodeInfo, base string, value any) any {
	object, ok := asObject(value)
	if !ok {
		return value
	}
	m := info.memberInfo()
	copied := objectCopy{src: object}
	keys := r.pushKeys(object)
	for _, key := range keys {
		original, _ := object.get(key)
		next := original
		property, matched := m.property(key)
		if matched {
			next = r.convert(property, base, next)
		}
		for _, pattern := range m.patterns {
			if !pattern.re.MatchString(key) {
				continue
			}
			matched = true
			next = r.convert(pattern.schema, base, next)
		}
		if !matched && m.hasAdditional {
			next = r.convert(m.additional, base, next)
		}
		if !identical(next, original) {
			copied.assign(key, next)
		}
	}
	r.popKeys(keys)
	return copied.result(value)
}

// ---------------------------------------------------------------------------------------------------------------
// Pass 3: primitive coercion by type
// ---------------------------------------------------------------------------------------------------------------

func (r *run) coerceTypes(value any, schema any, base string) any {
	node, ok := isSchemaObject(schema)
	if !ok {
		return value
	}
	info := r.co.info(node)
	b := info.baseOr(base)
	next := value
	if info.isAllOf {
		for _, member := range info.allOf {
			next = r.coerceTypes(next, member, b)
		}
	}
	if info.isAnyOf {
		next = r.coerceUnion(next, info.anyOf, b)
	}
	if info.isOneOf {
		next = r.coerceUnion(next, info.oneOf, b)
	}
	types := info.typesOfSchema
	if len(types) > 0 && (len(types) == 1 || !matchesAnyType(next, types)) {
		for _, name := range types {
			if candidate := coercePrimitive(next, name); !identical(candidate, next) {
				next = candidate
				break
			}
		}
	}
	if info.typeObject {
		if object, ok := asObject(next); ok {
			next = r.coerceProperties(next, object, info, b)
		}
	}
	if info.typeArray {
		if list, ok := next.([]any); ok {
			next = r.coerceItems(next, list, info, b)
		}
	}
	return next
}

func (r *run) coerceUnion(value any, branches []any, base string) any {
	for _, branch := range branches {
		if r.passes(branch, base, value) == passTrue {
			return value
		}
	}
	for _, branch := range branches {
		coerced := r.coerceTypes(value, branch, base)
		if r.passes(branch, base, coerced) == passTrue {
			return coerced
		}
	}
	return value
}

func (r *run) coerceProperties(value any, object jsonObject, info *nodeInfo, base string) any {
	m := info.memberInfo()
	copied := objectCopy{src: object}
	for i, key := range m.propKeys {
		if current, present := object.get(key); present {
			copied.set(key, r.coerceTypes(current, m.propSchemas[i], base))
		}
	}
	if len(m.patterns) > 0 || m.hasAdditional {
		keys := r.pushKeys(object)
		for _, key := range keys {
			original, _ := object.get(key)
			matched := m.hasProperty(key)
			for _, pattern := range m.patterns {
				if !pattern.re.MatchString(key) {
					continue
				}
				matched = true
				if current, present := copied.current().get(key); present {
					copied.set(key, r.coerceTypes(current, pattern.schema, base))
				}
			}
			if !matched && m.hasAdditional {
				copied.set(key, r.coerceTypes(original, m.additional, base))
			}
		}
		r.popKeys(keys)
	}
	return copied.result(value)
}

func (r *run) coerceItems(value any, list []any, info *nodeInfo, base string) any {
	copied := arrayCopy{src: list}
	for i, item := range list {
		if itemSchema, found := info.itemSchemaAt(i); found {
			copied.set(i, r.coerceTypes(item, itemSchema, base))
		}
	}
	return copied.result(value)
}

// matchesAnyType reports whether value has one of the JSON Schema types.
func matchesAnyType(value any, types []string) bool {
	for _, name := range types {
		if matchesType(value, name) {
			return true
		}
	}
	return false
}

func matchesType(value any, name string) bool {
	switch name {
	case "number":
		// a number is finite (Pi main 55a5c6914).
		n, ok := asNumber(value)
		return ok && isFinite(n)
	case "integer":
		n, ok := asNumber(value)
		return ok && isInteger(n)
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "null":
		return value == nil
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		return isObject(value)
	}
	return false
}

// coercePrimitive is value coerced to one type, as Ajv's coerceTypes does: value itself when the type does not apply.
func coercePrimitive(value any, name string) any {
	switch name {
	case "number", "integer":
		if value == nil {
			return 0.0
		}
		if text, ok := value.(string); ok && jsstring.Trim(text) != "" {
			parsed := jsnumber.Parse(text)
			if name == "number" && isFinite(parsed) || name == "integer" && isInteger(parsed) {
				return parsed
			}
		}
		if b, ok := value.(bool); ok {
			if b {
				return 1.0
			}
			return 0.0
		}
	case "boolean":
		if value == nil {
			return false
		}
		if text, ok := value.(string); ok {
			switch text {
			case "true":
				return true
			case "false":
				return false
			}
		} else if n, ok := asNumber(value); ok {
			switch n {
			case 1:
				return true
			case 0:
				return false
			}
		}
	case "string":
		if value == nil {
			return ""
		}
		if n, ok := asNumber(value); ok {
			return jsnumber.String(n)
		}
		if b, ok := value.(bool); ok {
			return strconv.FormatBool(b)
		}
	case "null":
		switch x := value.(type) {
		case string:
			if x == "" {
				return nil
			}
		case bool:
			if !x {
				return nil
			}
		default:
			if n, ok := asNumber(value); ok && n == 0 {
				return nil
			}
		}
	}
	return value
}

// ---------------------------------------------------------------------------------------------------------------
// TypeBox's conversions of one value (value/convert/try in TypeBox 1.3.27): a result, or none
// ---------------------------------------------------------------------------------------------------------------

func isFinite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }

// convertLiteral is a literal (`const`, an enum value) taken from a value of another type that converts to exactly it.
func convertLiteral(literal, value any) any {
	if identical(literal, value) {
		return value
	}
	switch want := literal.(type) {
	case string:
		if text, ok := tryString(value); ok && text == want {
			return text
		}
	case bool:
		if b, ok := tryBoolean(value); ok && b == want {
			return b
		}
	default:
		if want, isNumber := asNumber(literal); isNumber {
			if n, ok := tryNumber(value); ok && n == want {
				return n
			}
		}
	}
	return value
}

func tryString(value any) (string, bool) {
	switch x := value.(type) {
	case nil:
		return "null", true
	case bool:
		return strconv.FormatBool(x), true
	case string:
		return x, true
	}
	if n, ok := asNumber(value); ok && isFinite(n) {
		return jsnumber.String(n), true
	}
	return "", false
}

func tryNumber(value any) (float64, bool) {
	switch x := value.(type) {
	case nil:
		return 0, true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		return tryNumberOfString(x)
	}
	if n, ok := asNumber(value); ok && isFinite(n) {
		return n, true
	}
	return 0, false
}

func tryNumberOfString(value string) (float64, bool) {
	if coerced := jsnumber.Parse(value); isFinite(coerced) {
		return coerced, true
	}
	switch {
	case jsstring.LowerEquals(value, "false"):
		return 0, true
	case jsstring.LowerEquals(value, "true"):
		return 1, true
	}
	return safeBigInt(value)
}

// safeBigInt is TypeBox's TryBigInt of a string, as a number when it is a safe integer: `12n`, `12.5` (truncated) or `12`.
func safeBigInt(value string) (float64, bool) {
	text, negative := value, false
	if rest, ok := strings.CutPrefix(text, "-"); ok {
		text, negative = rest, true
	}
	var digits string
	switch {
	case strings.HasSuffix(text, "n"):
		digits = text[:len(text)-1]
	case strings.Contains(text, "."):
		whole, fraction, _ := strings.Cut(text, ".")
		if fraction == "" || !asciiDigits(fraction) {
			return 0, false
		}
		digits = whole
	default:
		digits = text
	}
	//portlint:allow utf16units digits passed canonicalInteger, so it is ASCII and its byte length is its code-unit length; 16 digits or fewer, and at most 9007199254740991, is coerce.ts tryNumber's big <= MAX_BIGINT && big >= MIN_BIGINT with MAX_BIGINT = BigInt(Number.MAX_SAFE_INTEGER)
	if !canonicalInteger(digits) || len(digits) > 16 || len(digits) == 16 && digits > "9007199254740991" {
		return 0, false
	}
	//portlint:allow numbers digits is 1 to 16 ASCII digits without a leading zero (checked above), so ParseUint is BigInt(digits) of coerce.ts bigIntOfString and cannot overflow; its 53-bit size is MAX_SAFE_INTEGER, which the length check above already enforces
	magnitude, err := strconv.ParseUint(digits, 10, 53)
	if err != nil {
		return 0, false
	}
	if negative && magnitude != 0 {
		return -float64(magnitude), true
	}
	return float64(magnitude), true
}

// canonicalInteger matches `0|[1-9]\d*`.
func canonicalInteger(text string) bool {
	return text == "0" || text != "" && text[0] != '0' && asciiDigits(text)
}

func asciiDigits(text string) bool {
	for i := range len(text) {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}
	return text != ""
}

func tryBoolean(value any) (bool, bool) {
	switch x := value.(type) {
	case nil:
		return false, true
	case bool:
		return x, true
	case string:
		switch {
		case x == "0" || jsstring.LowerEquals(x, "false"):
			return false, true
		case x == "1" || jsstring.LowerEquals(x, "true"):
			return true, true
		}
		return false, false
	}
	if n, ok := asNumber(value); ok && isFinite(n) {
		switch n {
		case 0:
			return false, true
		case 1:
			return true, true
		}
	}
	return false, false
}

// tryNull reports whether value converts to null.
func tryNull(value any) bool {
	switch x := value.(type) {
	case nil:
		return true
	case bool:
		return !x
	case string:
		return x == "" || x == "0" || jsstring.LowerEquals(x, "undefined") || jsstring.LowerEquals(x, "null")
	}
	n, ok := asNumber(value)
	return ok && n == 0
}
