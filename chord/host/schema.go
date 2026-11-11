package host

// Ports packages/chord/src/schema.ts.
//
// Forced Go differences. Upstream's `WeakMap<object, Validator>` of compiled validators holds each validator only while
// its schema object lives; Go has no ephemeron map (the validator refers to its schema, so a weak key would never be
// released), so validatorOf keeps a bounded cache and starts over when it is full. A value JavaScript holds as
// `undefined` is `absent{}`; the schema package reads it as a value of no type, as JavaScript's `typeof` does. A schema
// whose compilation fails makes the functions return the compile error where upstream's `compile` throws.

import (
	"maps"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	pischema "github.com/MichaelKinsy/PiG/schema"
)

// Schema is JSON Schema as data: pi-schema's builders, or any JSON Schema object (a nil Schema is `undefined`). The
// object keeps the order its members were declared in, as a JavaScript object does. Nested schemas are *Object, or
// map[string]any when a caller built them as maps.
type Schema = *pischema.Object

// schemaMap is a schema node as the fits and identity functions read it.
type schemaMap = map[string]any

// emptySchema is the schema `{}` that stands for a value that is no schema; it is never changed.
var emptySchema = chordjson.NewObject(0)

// falseSchema is `false` as the object that accepts nothing, `{ not: {} }`; it is never changed.
var falseSchema = chordjson.ObjectOf("not", chordjson.NewObject(0))

// asSchema is `schema !== null && typeof schema === "object" ? schema : schema === false ? { not: {} } : {}`: an
// object (of either Go kind) is itself, `false` accepts nothing, anything else accepts everything.
func asSchema(schema any) any {
	switch typed := schema.(type) {
	case *chordjson.Object:
		if typed != nil {
			return typed
		}
	case map[string]any:
		return typed
	case bool:
		if !typed {
			return falseSchema
		}
	}
	return emptySchema
}

// schemaMapOf reads a schema node as a map; the nested nodes stay as they are.
func schemaMapOf(schema any) schemaMap {
	switch typed := asSchema(schema).(type) {
	case *chordjson.Object:
		return maps.Collect(typed.All())
	case map[string]any:
		return typed
	}
	return schemaMap{}
}

// node is a JSON object of either Go kind, read and written in place: a schema, or a value being cleaned.
type node struct {
	object *chordjson.Object
	plain  map[string]any
}

// nodeOf reads value as an object; ok is false for anything else (an array and bytes are no object here).
func nodeOf(value any) (node, bool) {
	switch typed := value.(type) {
	case *chordjson.Object:
		return node{object: typed}, typed != nil
	case map[string]any:
		return node{plain: typed}, typed != nil
	}
	return node{}, false
}

func (n node) get(key string) (any, bool) {
	if n.object != nil {
		return n.object.Get(key)
	}
	value, ok := n.plain[key]
	return value, ok
}

// value is `node[key]`, `undefined` when it is absent.
func (n node) value(key string) any {
	if value, ok := n.get(key); ok {
		return value
	}
	return absent{}
}

func (n node) has(key string) bool {
	_, ok := n.get(key)
	return ok
}

func (n node) set(key string, value any) {
	if n.object != nil {
		n.object.Set(key, value)
		return
	}
	n.plain[key] = value
}

func (n node) delete(key string) {
	if n.object != nil {
		n.object.Delete(key)
		return
	}
	delete(n.plain, key)
}

// keys is Object.keys: the keys in own-key order.
func (n node) keys() []string {
	if n.object != nil {
		return n.object.Keys()
	}
	keys := make([]string, 0, len(n.plain))
	for key := range n.plain {
		keys = append(keys, key)
	}
	chordjson.SortOwnKeys(keys)
	return keys
}

// listOf is `Array.isArray(value)` for the list kinds a Go schema holds.
func listOf(value any) ([]any, bool) {
	switch typed := value.(type) {
	case []any:
		return typed, true
	case []*chordjson.Object:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = item
		}
		return out, true
	case []map[string]any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = item
		}
		return out, true
	}
	return nil, false
}

// compareUTF16 orders strings as JavaScript's default sort does: by UTF-16 code units.
func compareUTF16(left, right string) int {
	a, b := utf16.Encode([]rune(left)), utf16.Encode([]rune(right))
	return slices.Compare(a, b)
}

// objectEntries reads a JSON object of either Go kind with its keys in JavaScript enumeration order; ok is false for a
// non-object.
func objectEntries(value any) (keys []string, get func(string) any, ok bool) {
	object, ok := nodeOf(value)
	if !ok {
		return nil, nil, false
	}
	return object.keys(), object.value, true
}

func quoteJSON(text string) string { return string(jsstring.AppendString(nil, text)) }

// absent marks a member JavaScript would hold as undefined; canonicalJson drops it.
type absent struct{}

// CanonicalJSON is schema.ts canonicalJson: sorted object keys, undefined members dropped.
func CanonicalJSON(value any) string {
	var out strings.Builder
	writeCanonical(&out, value, false)
	return out.String()
}

// OrderedJSON is canonicalJson except that the members of an ordered object (*chordjson.Object) keep the order they were
// declared in, as JSON.stringify writes a schema; Go maps are written sorted, as CanonicalJSON writes them.
func OrderedJSON(value any) string {
	var out strings.Builder
	writeCanonical(&out, value, true)
	return out.String()
}

func writeCanonical(out *strings.Builder, value any, ordered bool) {
	switch typed := value.(type) {
	case nil, absent:
		out.WriteString("null")
	case bool:
		if typed {
			out.WriteString("true")
		} else {
			out.WriteString("false")
		}
	case string:
		out.WriteString(quoteJSON(typed))
	case []any:
		out.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				out.WriteByte(',')
			}
			writeCanonical(out, item, ordered)
		}
		out.WriteByte(']')
	case jsonMarshaler:
		writeCanonical(out, typed.jsonValue(), ordered)
	default:
		if number, ok := toFloat(value); ok {
			if math.IsNaN(number) || math.IsInf(number, 0) {
				out.WriteString("null")
				return
			}
			out.WriteString(string(jsnumber.JSON(number)))
			return
		}
		if list := reflect.ValueOf(value); list.Kind() == reflect.Slice {
			items := make([]any, list.Len())
			for index := range items {
				items[index] = list.Index(index).Interface()
			}
			writeCanonical(out, items, ordered)
			return
		}
		keys, get, ok := objectEntries(value)
		if !ok {
			out.WriteString("null")
			return
		}
		var present []string
		for _, key := range keys {
			if _, missing := get(key).(absent); !missing {
				present = append(present, key)
			}
		}
		if _, declared := value.(*chordjson.Object); !ordered || !declared {
			slices.SortFunc(present, compareUTF16)
		}
		out.WriteByte('{')
		for index, key := range present {
			if index > 0 {
				out.WriteByte(',')
			}
			out.WriteString(quoteJSON(key))
			out.WriteByte(':')
			writeCanonical(out, get(key), ordered)
		}
		out.WriteByte('}')
	}
}

// jsonMarshaler is a Chord value with a JSON form (a contract description) that canonicalJson writes.
type jsonMarshaler interface{ jsonValue() any }

// toFloat reads a Go number of any kind as the float64 JSON holds.
func toFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int8:
		return float64(typed), true
	case int16:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint8:
		return float64(typed), true
	case uint16:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	}
	return 0, false
}

// Hash64 is schema.ts hash64: a non-cryptographic 64-bit hash (two FNV-1a lanes) over UTF-16 code units, as 16 hex
// digits.
func Hash64(text string) string {
	a := uint32(0x811c9dc5)
	b := uint32(0x01000193 ^ 0x5bd1e995)
	for _, code := range utf16.Encode([]rune(text)) {
		a = (a ^ uint32(code)) * 0x01000193
		b = ((b ^ uint32(code)) * 0x5bd1e995) ^ (b >> 13)
	}
	const digits = "0123456789abcdef"
	var out [16]byte
	for index := range 8 {
		out[index] = digits[(a>>(28-4*index))&0xf]
		out[8+index] = digits[(b>>(28-4*index))&0xf]
	}
	return string(out[:])
}

// DeepEqual is schema.ts deepEqual over canonical JSON.
func DeepEqual(a, b any) bool { return CanonicalJSON(a) == CanonicalJSON(b) }

// validatorCacheLimit bounds the compiled validators kept by schema object; a full cache starts over.
const validatorCacheLimit = 4096

var validators struct {
	sync.RWMutex
	bySchema map[*chordjson.Object]*pischema.Validator
}

// validatorOf is schema.ts validatorOf: the compiled validator of schema, compiled once per schema object; `format` is
// an annotation (no format check is compiled in).
func validatorOf(schema any) (*pischema.Validator, error) {
	object, cacheable := schema.(*chordjson.Object)
	cacheable = cacheable && object != nil
	if cacheable {
		validators.RLock()
		validator := validators.bySchema[object]
		validators.RUnlock()
		if validator != nil {
			return validator, nil
		}
	}
	validator, err := pischema.Compile(schema, pischema.CompileOptions{})
	if err != nil || !cacheable {
		return validator, err
	}
	validators.Lock()
	defer validators.Unlock()
	if validators.bySchema == nil || len(validators.bySchema) >= validatorCacheLimit {
		validators.bySchema = map[*chordjson.Object]*pischema.Validator{}
	}
	validators.bySchema[object] = validator
	return validator, nil
}

// IsValid is schema.ts isValid: whether value matches schema.
func IsValid(schema, value any) (bool, error) {
	validator, err := validatorOf(schema)
	if err != nil {
		return false, err
	}
	return validator.Check(value)
}

// assertValid is schema.ts assertValid: it returns invalid (with reason) and the first three schema errors when value
// does not match.
func assertValid(reason, what string, schema, value any) error {
	validator, err := validatorOf(schema)
	if err != nil {
		return err
	}
	valid, err := validator.Check(value)
	if err != nil || valid {
		return err
	}
	errs, err := validator.Errors(value)
	if err != nil {
		return err
	}
	if len(errs) > 3 {
		errs = errs[:3]
	}
	parts := make([]string, len(errs))
	details := make([]any, len(errs))
	for index, failure := range errs {
		path := failure.Path
		if path == "" {
			path = "/"
		}
		parts[index] = path + " " + failure.Message
		details[index] = chordjson.ObjectOf("path", failure.Path, "keyword", failure.Keyword, "message", failure.Message)
	}
	return Invalid(reason, what+": "+strings.Join(parts, "; "), details)
}

// Normalize is schema.ts normalize: it applies defaults, strips unknown fields and validates, returning a fresh value.
// A nil value is normalized as an empty object (value ?? {}).
func Normalize(schema Schema, value any, what string) (any, error) {
	if value == nil {
		value = map[string]any{}
	}
	cleaner := cleaner{}
	normalized := cleaner.clean(asSchema(schema), cleaner.defaults(asSchema(schema), cloneData(value)))
	if cleaner.err != nil {
		return nil, cleaner.err
	}
	if err := assertValid("input", what, schema, normalized); err != nil {
		return nil, err
	}
	if cleaner.holes {
		// A hole is JavaScript's `undefined` in a list, which JSON.stringify writes as null: the value a caller gets is JSON.
		normalized = holesAsNull(normalized)
	}
	return normalized, nil
}

// holesAsNull replaces the undefined elements of lists by null, in place.
func holesAsNull(value any) any {
	switch typed := value.(type) {
	case []any:
		for index, item := range typed {
			if _, undefined := item.(absent); undefined {
				typed[index] = nil
				continue
			}
			typed[index] = holesAsNull(item)
		}
	default:
		if object, ok := nodeOf(value); ok {
			for _, key := range object.keys() {
				object.set(key, holesAsNull(object.value(key)))
			}
		}
	}
	return value
}

// Cleaned is schema.ts cleaned: a copy of value with only what schema knows, unknown object fields dropped,
// recursively (what TypeBox's `Clean` did).
func Cleaned(schema Schema, value any) (any, error) {
	cleaner := cleaner{}
	cleaned := cleaner.clean(asSchema(schema), cloneData(value))
	return cleaned, cleaner.err
}

// cloneData is schema.ts cloneData: a deep copy of plain data. Lists, bytes and objects are copied, a number is the
// float64 JSON holds, any other value is kept.
func cloneData(value any) any {
	if _, isFloat := value.(float64); !isFloat {
		if number, isNumber := toFloat(value); isNumber {
			return number
		}
	}
	switch typed := value.(type) {
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = cloneData(item)
		}
		return out
	case []byte:
		return slices.Clone(typed)
	case *chordjson.Object:
		if typed == nil {
			return typed
		}
		out := chordjson.NewObject(typed.Len())
		for key, item := range typed.All() {
			out.Set(key, cloneData(item))
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = cloneData(item)
		}
		return out
	}
	return value
}

// cleaner carries the first error of the validators and patterns a clean or a defaulting compiles; upstream's compile
// throws where it fails.
type cleaner struct {
	err error
	// holes is set when defaulting left a list element undefined (a tuple padded up to a later item).
	holes bool
}

// valid is isValid(schema, value) that remembers a compile failure.
func (c *cleaner) valid(schema, value any) bool {
	valid, err := IsValid(schema, value)
	if err != nil && c.err == nil {
		c.err = err
	}
	return valid
}

// mergedAllOf is schema.ts mergedAllOf: the object an `allOf` of object schemas evaluates to (TypeBox's `Evaluate`),
// or nil for any other `allOf`.
func mergedAllOf(schema node) *chordjson.Object {
	branches, ok := listOf(schema.value("allOf"))
	if !ok {
		return nil
	}
	nodes := make([]node, len(branches))
	for index, branch := range branches {
		branchNode, isNode := nodeOf(branch)
		if !isNode {
			return nil
		}
		if _, hasProperties := nodeOf(branchNode.value("properties")); !hasProperties {
			return nil
		}
		nodes[index] = branchNode
	}
	properties := chordjson.NewObject(0)
	var required []any
	for _, branch := range nodes {
		own, _ := nodeOf(branch.value("properties"))
		for _, key := range own.keys() {
			properties.Set(key, own.value(key))
		}
		if list, isList := listOf(branch.value("required")); isList {
			for _, name := range list {
				if !slices.Contains(required, name) {
					required = append(required, name)
				}
			}
		}
	}
	if required == nil {
		required = []any{}
	}
	merged := chordjson.ObjectOf("type", "object", "properties", properties, "required", required)
	if unevaluated, has := schema.get("unevaluatedProperties"); has {
		merged.Set("additionalProperties", unevaluated)
	}
	return merged
}

// recordOf is schema.ts recordOf: the pattern and value schema of a record (`patternProperties` with one pattern).
func recordOf(schema node) (match func(string) bool, value any, ok bool, err error) {
	patterns, isRecord := nodeOf(schema.value("patternProperties"))
	if !isRecord {
		return nil, nil, false, nil
	}
	if _, named := nodeOf(schema.value("properties")); named {
		return nil, nil, false, nil
	}
	keys := patterns.keys()
	if len(keys) == 0 {
		return nil, nil, false, nil
	}
	match, err = patternMatcher(keys[0])
	if err != nil {
		return nil, nil, false, err
	}
	return match, patterns.value(keys[0]), true, nil
}

// keepsUnknown is schema.ts keepsUnknown: whether additional (an object's `additionalProperties`) keeps an unknown
// field holding value.
func (c *cleaner) keepsUnknown(additional, value any) bool {
	if keep, ok := additional.(bool); ok {
		return keep
	}
	if _, isSchema := nodeOf(additional); isSchema {
		return c.valid(additional, value)
	}
	return false
}

// prefixOf is `Array.isArray(schema.items) ? schema.items : Array.isArray(schema.prefixItems) ? schema.prefixItems :
// undefined`.
func prefixOf(schema node) ([]any, bool) {
	if items, ok := listOf(schema.value("items")); ok {
		return items, true
	}
	return listOf(schema.value("prefixItems"))
}

// clean is schema.ts cleanFrom: it mutates value (a fresh copy), as TypeBox's `Clean` does.
func (c *cleaner) clean(schema, value any) any {
	schemaObject, _ := nodeOf(schema)
	if merged := mergedAllOf(schemaObject); merged != nil {
		return c.clean(merged, value)
	}
	if branches, ok := listOf(schemaObject.value("anyOf")); ok {
		for _, branch := range branches {
			candidate := c.clean(asSchema(branch), cloneData(value))
			if c.valid(asSchema(branch), candidate) {
				return candidate
			}
		}
		return value
	}
	if list, ok := value.([]any); ok {
		if prefix, tuple := prefixOf(schemaObject); tuple {
			length := min(len(list), len(prefix))
			for index := range length {
				list[index] = c.clean(asSchema(prefix[index]), list[index])
			}
			if len(list) > length {
				return list[:length:length]
			}
			return list
		}
		items := schemaObject.value("items")
		if _, isSchema := nodeOf(items); isSchema {
			out := make([]any, len(list))
			for index, item := range list {
				out[index] = c.clean(items, item)
			}
			return out
		}
		return list
	}
	object, isObject := nodeOf(value)
	if !isObject {
		return value
	}
	match, recordValue, isRecord, err := recordOf(schemaObject)
	if err != nil {
		c.fail(err)
		return value
	}
	properties, named := nodeOf(schemaObject.value("properties"))
	additional := schemaObject.value("additionalProperties")
	_, mapped := nodeOf(additional)
	// An object schema names its fields, a record its key pattern, a map (pi-schema's) its value schema.
	if !named && !isRecord && !mapped {
		return value
	}
	for _, key := range object.keys() {
		var matched any = absent{}
		if named {
			if known, has := properties.get(key); has && known != nil {
				matched = known
			}
		}
		if _, undefined := matched.(absent); undefined && isRecord && match(key) {
			matched = recordValue
		}
		item := object.value(key)
		switch {
		case !isUndefined(matched):
			object.set(key, c.clean(asSchema(matched), item))
		case c.keepsUnknown(additional, item):
			object.set(key, c.clean(asSchema(additional), item))
		default:
			object.delete(key)
		}
	}
	return value
}

func isUndefined(value any) bool {
	_, undefined := value.(absent)
	return undefined
}

func (c *cleaner) fail(err error) {
	if c.err == nil {
		c.err = err
	}
}

// defaults is schema.ts withDefaults: it mutates value (a fresh copy), as TypeBox's `Default` does: a missing value
// takes its schema's default, a missing optional field never does.
func (c *cleaner) defaults(schema, value any) any {
	schemaObject, _ := nodeOf(schema)
	defaulted := value
	if fallback, has := schemaObject.get("default"); has && isUndefined(value) {
		defaulted = cloneData(fallback)
	}
	if merged := mergedAllOf(schemaObject); merged != nil {
		return c.defaults(merged, defaulted)
	}
	if branches, ok := listOf(schemaObject.value("anyOf")); ok {
		for _, branch := range branches {
			candidate := c.defaults(asSchema(branch), cloneData(defaulted))
			if c.valid(asSchema(branch), candidate) {
				return candidate
			}
		}
		return defaulted
	}
	if list, ok := defaulted.([]any); ok {
		if prefix, tuple := prefixOf(schemaObject); tuple {
			for index := range prefix {
				var existing any = absent{}
				if index < len(list) {
					existing = list[index]
				}
				item := c.defaults(asSchema(prefix[index]), existing)
				if index < len(list) {
					list[index] = item
				} else if !isUndefined(item) {
					for len(list) < index {
						list = append(list, absent{})
						c.holes = true
					}
					list = append(list, item)
				}
			}
			return list
		}
		items := schemaObject.value("items")
		if _, isSchema := nodeOf(items); isSchema {
			out := make([]any, len(list))
			for index, item := range list {
				out[index] = c.defaults(items, item)
			}
			return out
		}
		return list
	}
	object, isObject := nodeOf(defaulted)
	if !isObject {
		return defaulted
	}
	properties, named := nodeOf(schemaObject.value("properties"))
	match, recordValue, isRecord, err := recordOf(schemaObject)
	if err != nil {
		c.fail(err)
		return defaulted
	}
	if named {
		required, _ := listOf(schemaObject.value("required"))
		for _, key := range properties.keys() {
			property := properties.value(key)
			item := c.defaults(asSchema(property), object.value(key))
			if isUndefined(item) {
				if !slices.Contains(required, any(key)) || !schemaNodeHas(asSchema(property), "default") {
					continue
				}
			}
			object.set(key, item)
		}
	} else if recordNode, isSchema := nodeOf(recordValue); isRecord && isSchema && recordNode.has("default") {
		for _, key := range object.keys() {
			if match(key) {
				object.set(key, c.defaults(recordValue, object.value(key)))
			}
		}
	}
	additional := schemaObject.value("additionalProperties")
	if _, isSchema := nodeOf(additional); !isSchema {
		return defaulted
	}
	for _, key := range object.keys() {
		known := named && properties.has(key)
		if !known && (!isRecord || !match(key)) {
			object.set(key, c.defaults(additional, object.value(key)))
		}
	}
	return defaulted
}

func schemaNodeHas(schema any, keyword string) bool {
	object, ok := nodeOf(schema)
	return ok && object.has(keyword)
}

// patternMatcher is `new RegExp(pattern, "u")`'s `test`: whether a string matches the pattern anywhere, compiled as
// pi-schema compiles a `pattern` keyword.
func patternMatcher(pattern string) (func(string) bool, error) {
	patternMatchers.RLock()
	entry, ok := patternMatchers.byText[pattern]
	patternMatchers.RUnlock()
	if ok {
		return entry.match, entry.err
	}
	return compilePatternMatcher(pattern)
}

// compilePatternMatcher compiles pattern and keeps it, once per pattern text.
func compilePatternMatcher(pattern string) (func(string) bool, error) {
	patternMatchers.Lock()
	defer patternMatchers.Unlock()
	if entry, ok := patternMatchers.byText[pattern]; ok {
		return entry.match, entry.err
	}
	var entry compiledPattern
	validator, err := pischema.Compile(chordjson.ObjectOf("type", "string", "pattern", pattern), pischema.CompileOptions{})
	switch {
	case err != nil:
		entry.err = err
	default:
		entry.match = func(text string) bool {
			valid, err := validator.Check(text)
			return err == nil && valid
		}
	}
	if patternMatchers.byText == nil || len(patternMatchers.byText) >= validatorCacheLimit {
		patternMatchers.byText = map[string]compiledPattern{}
	}
	patternMatchers.byText[pattern] = entry
	return entry.match, entry.err
}

type compiledPattern struct {
	match func(string) bool
	err   error
}

var patternMatchers struct {
	sync.RWMutex
	byText map[string]compiledPattern
}

// CanonicalIdentity is schema.ts canonicalIdentity: the canonical JSON of a normalized value with fields equal to their
// schema default omitted.
func CanonicalIdentity(schema Schema, value any) string {
	return CanonicalJSON(stripDefaults(asSchema(schema), value))
}

func stripDefaults(schema, value any) any {
	node, _ := nodeOf(schema)
	if items, ok := value.([]any); ok {
		out := make([]any, len(items))
		if positional, tuple := listOf(node.value("items")); tuple {
			for index, item := range items {
				var itemSchema any
				if index < len(positional) {
					itemSchema = positional[index]
				}
				out[index] = stripDefaults(asSchema(itemSchema), item)
			}
			return out
		}
		for index, item := range items {
			out[index] = stripDefaults(asSchema(node.value("items")), item)
		}
		return out
	}
	object, ok := nodeOf(value)
	if !ok {
		return value
	}
	keys := object.keys()
	properties, _ := nodeOf(asSchema(node.value("properties")))
	result := chordjson.NewObject(len(keys))
	for _, key := range keys {
		item := object.value(key)
		property := asSchema(properties.value(key))
		if propertyNode, _ := nodeOf(property); propertyNode.has("default") && DeepEqual(propertyNode.value("default"), item) {
			continue
		}
		result.Set(key, stripDefaults(property, item))
	}
	return result
}

var constraints = []string{
	"minLength", "maxLength", "pattern", "format", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum",
	"multipleOf", "minItems", "maxItems", "uniqueItems", "minProperties", "maxProperties",
}

func typesOf(schema schemaMap) ([]string, bool) {
	switch typed := schema["type"].(type) {
	case string:
		return []string{typed}, true
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out, true
	case []string:
		return typed, true
	}
	return nil, false
}

// known are the keywords fits reasons about. Any other validation keyword in the provider's schema forces exact
// equality.
var known = func() map[string]bool {
	set := map[string]bool{}
	for _, key := range append(slices.Clone(constraints), "type", "properties", "required", "additionalProperties", "patternProperties", "items", "additionalItems", "const", "enum", "anyOf", "parameters", "returnType") {
		set[key] = true
	}
	return set
}()

// annotations have no effect on validity.
var annotations = map[string]bool{
	"title": true, "description": true, "default": true, "examples": true, "$id": true, "$schema": true,
	"$comment": true, "readOnly": true, "writeOnly": true, "deprecated": true,
}

func withoutAnnotations(schema schemaMap) schemaMap {
	out := schemaMap{}
	for key, value := range schema {
		if !annotations[key] {
			out[key] = value
		}
	}
	return out
}

func stringList(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	}
	return nil
}

// Fits is schema.ts fits: conservative structural subtyping. It is true only if every value valid under sub is valid
// under sup, where object fields missing from sub but optional in sup count as fine. A provider schema using a keyword
// this function does not model, or an additionalProperties schema, only fits an identical consumer schema.
func Fits(subSchema, supSchema any) bool {
	sub := withoutAnnotations(schemaMapOf(subSchema))
	sup := withoutAnnotations(schemaMapOf(supSchema))
	if len(sup) == 0 {
		return true
	}
	if DeepEqual(sub, sup) {
		return true
	}
	for key := range sup {
		if !known[key] {
			return false
		}
	}
	if additional, ok := sup["additionalProperties"]; ok {
		if _, boolean := additional.(bool); !boolean {
			return false
		}
	}
	if branches, ok := listOf(sub["anyOf"]); ok {
		for _, branch := range branches {
			if !Fits(branch, sup) {
				return false
			}
		}
		return true
	}
	if branches, ok := listOf(sup["anyOf"]); ok {
		for _, branch := range branches {
			if Fits(sub, branch) {
				return true
			}
		}
		return false
	}
	if constant, ok := sup["const"]; ok {
		subConstant, has := sub["const"]
		return has && DeepEqual(subConstant, constant)
	}
	if allowed, ok := listOf(sup["enum"]); ok {
		var values []any
		if constant, has := sub["const"]; has {
			values = []any{constant}
		} else if enum, has := listOf(sub["enum"]); has {
			values = enum
		} else {
			return false
		}
		for _, item := range values {
			if !slices.ContainsFunc(allowed, func(candidate any) bool { return DeepEqual(candidate, item) }) {
				return false
			}
		}
		return true
	}
	for _, key := range constraints {
		if wanted, ok := sup[key]; ok && !DeepEqual(valueOrAbsent(sub, key), wanted) {
			return false
		}
	}
	supTypes, supOK := typesOf(sup)
	subTypes, subOK := typesOf(sub)
	if !supOK || !subOK {
		return false
	}
	for _, typeName := range subTypes {
		if !slices.Contains(supTypes, typeName) && (typeName != "integer" || !slices.Contains(supTypes, "number")) {
			return false
		}
	}
	if _, ok := sub["const"]; ok {
		return true
	}
	if _, ok := listOf(sub["enum"]); ok {
		return true
	}
	if slices.Contains(subTypes, "object") && !objectFits(sub, sup) {
		return false
	}
	if slices.Contains(subTypes, "array") && !arrayFits(sub, sup) {
		return false
	}
	if slices.Contains(subTypes, "function") {
		return functionFits(sub, sup)
	}
	return true
}

func valueOrAbsent(schema schemaMap, key string) any {
	if value, ok := schema[key]; ok {
		return value
	}
	return absent{}
}

func objectFits(sub, sup schemaMap) bool {
	subProperties := schemaMapOf(sub["properties"])
	supProperties := schemaMapOf(sup["properties"])
	subRequired := stringList(sub["required"])
	for _, key := range stringList(sup["required"]) {
		if !slices.Contains(subRequired, key) {
			return false
		}
	}
	subAdditional := sub["additionalProperties"]
	_, subAdditionalSchema := nodeOf(subAdditional)
	for key, property := range supProperties {
		if subProperty, ok := subProperties[key]; ok {
			if !Fits(subProperty, property) {
				return false
			}
		} else if subAdditionalSchema && !Fits(subAdditional, property) {
			// The consumer may send this name with any value its additionalProperties allows. Open consumer objects
			// are fine: unknown names are stripped with the consumer's own schema before the call.
			return false
		}
	}
	if sup["additionalProperties"] == false {
		if sub["additionalProperties"] != false {
			return false
		}
		for key := range subProperties {
			if _, ok := supProperties[key]; !ok {
				return false
			}
		}
	}
	supPatterns := schemaMapOf(sup["patternProperties"])
	if len(supPatterns) > 0 {
		// Every value the consumer may put under a matching name must fit, and the consumer must not allow arbitrary
		// extra names (it would then allow any value under a matching one).
		subPatterns := schemaMapOf(sub["patternProperties"])
		if sub["additionalProperties"] != false && len(subPatterns) == 0 {
			return false
		}
		for pattern, property := range supPatterns {
			match, err := patternMatcher(pattern)
			if err != nil {
				return false
			}
			for key, subProperty := range subProperties {
				if _, inSup := supProperties[key]; !inSup && match(key) && !Fits(subProperty, property) {
					return false
				}
			}
			if len(subPatterns) > 0 {
				subPattern, ok := subPatterns[pattern]
				if !ok || !Fits(subPattern, property) {
					return false
				}
			}
		}
		for pattern := range subPatterns {
			if _, ok := supPatterns[pattern]; !ok {
				return false
			}
		}
	}
	return true
}

func arrayFits(sub, sup schemaMap) bool {
	if supItems, ok := listOf(sup["items"]); ok {
		subItems, tuple := listOf(sub["items"])
		if !tuple || len(subItems) != len(supItems) {
			return false
		}
		for index, item := range subItems {
			if !Fits(item, supItems[index]) {
				return false
			}
		}
		return true
	}
	if subItems, ok := listOf(sub["items"]); ok {
		for _, item := range subItems {
			if !Fits(item, sup["items"]) {
				return false
			}
		}
		return true
	}
	return Fits(sub["items"], sup["items"])
}

func functionFits(sub, sup schemaMap) bool {
	subParameters, _ := listOf(sub["parameters"])
	supParameters, _ := listOf(sup["parameters"])
	// A function accepting fewer parameters is fine; each shared parameter is contravariant.
	if len(subParameters) > len(supParameters) {
		return false
	}
	for index, parameter := range subParameters {
		if !Fits(supParameters[index], parameter) {
			return false
		}
	}
	return Fits(sub["returnType"], sup["returnType"])
}
