package schema

// Ports packages/schema/src/compiler.ts
//
// The compiler: a schema to one check closure, built once. Keywords group by the type they constrain, so a value is classified once per schema, and the common shapes (declared-property objects, arrays of one item schema, tagged unions, bare types) get one specialized closure each.

import (
	"math"
	"slices"

	"github.com/MichaelKinsy/PiG/schema/internal/ecmaregex"
)

// restartSignal is what a compile without dynamic scope tracking panics with when it meets a bookended `$dynamicRef`: compile again with it.
type restartSignal struct{}

// compiler is one compile of a schema. Its caches and indexes are garbage once the root check is built.
type compiler struct {
	r       *resolver
	draft   Draft
	formats map[string]FormatCheck
	// dynamic reports whether checks track the dynamic scope (the schema has a bookended `$dynamicRef`).
	dynamic         bool
	schema          JsonSchema
	scope           *scopeStack
	cache           map[nodeKey]Check
	annotatingCache map[nodeKey]annotatingCheck
}

func newCompiler(schema JsonSchema, options CompileOptions, dynamic bool) *compiler {
	r := newResolver(schema, options)
	return &compiler{
		r:               r,
		draft:           r.draft,
		formats:         options.Formats,
		dynamic:         dynamic,
		schema:          schema,
		scope:           &scopeStack{},
		cache:           map[nodeKey]Check{},
		annotatingCache: map[nodeKey]annotatingCheck{},
	}
}

// reset forgets the compile state a failed lazy compile left behind: its caches hold indirections that were never filled, and the dynamic scope still holds the schemas that were being checked.
func (c *compiler) reset() {
	c.scope.uris = c.scope.uris[:0]
	clear(c.cache)
	clear(c.annotatingCache)
}

// root is the check of the root schema.
func (c *compiler) root() Check { return c.check(c.schema, c.r.rootBase) }

func notSchema(node any) {
	fail("A schema must be an object or a boolean, got %s", jsonStringify(node))
}

// check is the check of a schema (cached by schema object; a recursive reference goes through an indirection).
func (c *compiler) check(node any, base string) Check {
	if b, ok := node.(bool); ok {
		if b {
			return always
		}
		return never
	}
	o, ok := isSchemaObject(node)
	if !ok {
		notSchema(node)
	}
	id := o.identity()
	if cached, ok := c.cache[id]; ok {
		return cached
	}
	if bare := bareType(o); bare != nil {
		return bare
	}
	slot := &checkSlot{}
	c.cache[id] = indirect(slot)
	check := c.build(o, c.r.baseFor(o, base))
	slot.check = check
	c.cache[id] = check
	return check
}

func (c *compiler) build(node jsonObject, base string) Check {
	if c.draft == Draft7 {
		if ref, ok := node.stringMember("$ref"); ok {
			return c.ref(ref, base)
		}
	}
	if c.draft != Draft7 && (node.has("unevaluatedProperties") || node.has("unevaluatedItems")) {
		return withFreshAnnotations(c.annotatingObject(node, base))
	}
	check := c.plain(node, base)
	if c.dynamic && c.r.isResource(node, base) {
		return scopedCheck(c.scope, base, check)
	}
	return check
}

// bareType is the module-level check of a schema that is only a `type` (with annotations), always for one with no assertion at all; nil for every other schema. These are the most common subschemas and cost nothing to share.
func bareType(node jsonObject) Check {
	var typeValue any
	hasType := false
	for _, k := range node.keys() {
		if k == "type" {
			typeValue, hasType = node.value("type"), true
		} else if _, ok := neutralKeywords[k]; !ok {
			return nil
		}
	}
	if !hasType {
		return always
	}
	name, ok := typeValue.(string)
	if !ok {
		return nil
	}
	return typeChecks[name]
}

// plain is every keyword of node, not tracking annotations.
func (c *compiler) plain(node jsonObject, base string) Check {
	var parts []Check
	var types []any
	typeValue, hasType := node.get("type")
	if hasType {
		if list, ok := typeValue.([]any); ok {
			types = list
		} else {
			types = []any{typeValue}
		}
	}
	var single any
	if len(types) == 1 {
		single = types[0]
	}
	typeInGroup := false
	if single == "bytes" {
		min, _ := node.numberMember("minLength")
		max, hasMax := node.numberMember("maxLength")
		if !hasMax {
			max = math.Inf(1)
		}
		parts = append(parts, bytesCheck(min, max))
		typeInGroup = true
	} else {
		numberMode := 0
		switch single {
		case "number":
			numberMode = 1
		case "integer":
			numberMode = 2
		case "bigint":
			numberMode = 3
		}
		groups := []struct {
			check  Check
			strict bool
		}{
			{c.stringGroup(node, single == "string"), single == "string"},
			{c.numberGroup(node, numberMode), single == "number" || single == "integer" || single == "bigint"},
			{c.objectGroup(node, base, single == "object"), single == "object"},
			{c.arrayGroup(node, base, single == "array"), single == "array"},
		}
		for _, g := range groups {
			if g.check == nil {
				continue
			}
			parts = append(parts, g.check)
			if g.strict {
				typeInGroup = true
			}
		}
	}
	if hasType && !typeInGroup {
		parts = slices.Insert(parts, 0, typeCheck(types))
	}
	parts = c.inPlace(node, base, parts)
	return and(parts)
}

// inPlace appends the keywords that apply to the value itself, whatever its type.
func (c *compiler) inPlace(node jsonObject, base string, parts []Check) []Check {
	if list, ok := node.arrayMember("enum"); ok {
		parts = append(parts, enumCheck(list))
	}
	if value, ok := node.get("const"); ok {
		parts = append(parts, constCheck(value))
	}
	if list, ok := node.arrayMember("allOf"); ok {
		checks := make([]Check, len(list))
		for i, s := range list {
			checks[i] = c.check(s, base)
		}
		parts = append(parts, and(checks))
	}
	if list, ok := node.arrayMember("anyOf"); ok {
		parts = append(parts, c.union(list, base, false))
	}
	if list, ok := node.arrayMember("oneOf"); ok {
		parts = append(parts, c.union(list, base, true))
	}
	if not, ok := node.get("not"); ok {
		parts = append(parts, notCheck(c.check(not, base)))
	}
	if cond, ok := node.get("if"); ok {
		thenSchema, hasThen := node.get("then")
		elseSchema, hasElse := node.get("else")
		if hasThen || hasElse {
			then, otherwise := Check(always), Check(always)
			if hasThen {
				then = c.check(thenSchema, base)
			}
			if hasElse {
				otherwise = c.check(elseSchema, base)
			}
			parts = append(parts, ifCheck(c.check(cond, base), then, otherwise))
		}
	}
	if ref, ok := node.stringMember("$ref"); ok {
		parts = append(parts, c.ref(ref, base))
	}
	if c.draft != Draft7 {
		if ref, ok := node.stringMember("$dynamicRef"); ok {
			parts = append(parts, c.dynamicRef(ref, base))
		}
	}
	return parts
}

func (c *compiler) union(branches []any, base string, exactlyOne bool) Check {
	if check := c.constUnion(branches, exactlyOne); check != nil {
		return check
	}
	if check := c.tagged(branches, base); check != nil {
		return check
	}
	checks := make([]Check, len(branches))
	for i, s := range branches {
		checks[i] = c.check(s, base)
	}
	if exactlyOne {
		return oneCheck(checks)
	}
	return anyCheck(checks)
}

func (c *compiler) ref(ref, base string) Check {
	target := c.r.resolve(ref, base)
	check := c.check(target.node, target.base)
	if c.dynamic && target.base != base {
		return scopedCheck(c.scope, target.base, check)
	}
	return check
}

func (c *compiler) dynamicRef(ref, base string) Check {
	initial, name, ok := c.bookended(ref, base)
	if !ok {
		return c.ref(ref, base)
	}
	fallback := c.check(initial.node, initial.base)
	lookup := dynamicLookup(c, name, func(node any, b string) Check { return c.check(node, b) })
	scope := c.scope
	return func(v any) bool {
		for _, uri := range scope.uris {
			if check := lookup(uri); check != nil {
				return (*check)(v)
			}
		}
		return fallback(v)
	}
}

// bookended returns the initial target and anchor name of a `$dynamicRef` that resolves dynamically; it reports false for a plain ref.
func (c *compiler) bookended(ref, base string) (initial resolved, name string, ok bool) {
	initial = c.r.resolve(ref, base)
	uri := resolveUri(ref, base)
	name = uri[len(documentOf(uri))+min(len(uri)-len(documentOf(uri)), 1):]
	anchored := false
	if o, isObj := isSchemaObject(initial.node); isObj {
		anchor, has := o.stringMember("$dynamicAnchor")
		anchored = has && anchor == name
	}
	if name == "" || len(name) > 0 && name[0] == '/' || !anchored {
		return resolved{}, "", false
	}
	if !c.dynamic {
		panic(restartSignal{})
	}
	return initial, name, true
}

// dynamicLookup resolves at run time against the dynamic scope, so a schema with a bookended $dynamicRef keeps its compiler.
func dynamicLookup[T any](c *compiler, name string, compile func(node any, base string) T) func(uri string) *T {
	found := map[string]*T{}
	return func(uri string) *T {
		key := uri + "#" + name
		if hit, ok := found[key]; ok {
			return hit
		}
		var result *T
		if target, ok := c.r.dynamicAnchors[key]; ok {
			o, _ := asObject(target)
			base, hasBase := c.r.indexedBase(o)
			if !hasBase {
				base = uri
			}
			value := compile(target, base)
			result = &value
		}
		found[key] = result
		return result
	}
}

// tagged compiles anyOf/oneOf whose branches are all objects with a distinct required const on one key: the tag picks the branch.
func (c *compiler) tagged(branches []any, base string) Check {
	if len(branches) < 2 {
		return nil
	}
	resolvedBranches := make([]jsonObject, 0, len(branches))
	for _, branch := range branches {
		o, ok := isSchemaObject(branch)
		if !ok {
			return nil
		}
		node := o
		if ref, ok := o.stringMember("$ref"); ok && o.len() == 1 {
			target := c.r.resolve(ref, c.r.baseFor(o, base)).node
			node, ok = isSchemaObject(target)
			if !ok {
				return nil
			}
		}
		resolvedBranches = append(resolvedBranches, node)
	}
	first, ok := resolvedBranches[0].objectMember("properties")
	if !ok {
		return nil
	}
	for _, key := range first.keys() {
		tags := make([]any, len(resolvedBranches))
		usable := true
		for i, s := range resolvedBranches {
			tag, ok := tagOf(s, key)
			if !ok {
				usable = false
				break
			}
			tags[i] = tag
		}
		if !usable || !distinct(tags) {
			continue
		}
		checks := make([]Check, len(branches))
		for i, s := range branches {
			checks[i] = c.check(s, base)
		}
		return taggedCheck(key, tags, checks)
	}
	return nil
}

// distinct reports whether values hold no two SameValueZero-equal items.
func distinct(values []any) bool {
	for i := 1; i < len(values); i++ {
		for j := range i {
			if sameValueZero(values[i], values[j]) {
				return false
			}
		}
	}
	return true
}

func (c *compiler) stringGroup(node jsonObject, strict bool) Check {
	min, _ := node.numberMember("minLength")
	max, hasMax := node.numberMember("maxLength")
	if !hasMax {
		max = math.Inf(1)
	}
	var re *ecmaregex.Regexp
	if pattern, ok := node.stringMember("pattern"); ok {
		re = sharedRegex(pattern)
	}
	var format FormatCheck
	if c.formats != nil {
		if name, ok := node.stringMember("format"); ok {
			format = c.formats[name]
		}
	}
	if min == 0 && math.IsInf(max, 1) && re == nil && format == nil {
		return nil
	}
	return stringCheck(strict, min, max, re, format)
}

func (c *compiler) numberGroup(node jsonObject, mode int) Check {
	min, hasMin := node.boundMember("minimum")
	max, hasMax := node.boundMember("maximum")
	xmin, hasXmin := node.boundMember("exclusiveMinimum")
	xmax, hasXmax := node.boundMember("exclusiveMaximum")
	multipleOf, hasMultiple := node.boundMember("multipleOf")
	if !hasMin && !hasMax && !hasXmin && !hasXmax && !hasMultiple {
		return nil
	}
	if !hasMin {
		min = math.Inf(-1)
	}
	if !hasMax {
		max = math.Inf(1)
	}
	if !hasXmin {
		xmin = math.Inf(-1)
	}
	if !hasXmax {
		xmax = math.Inf(1)
	}
	if !hasMultiple {
		multipleOf = nil
	}
	return numberCheck(mode, newNumberBounds(min, max, xmin, xmax, multipleOf))
}

var objectExtras = []string{"propertyNames", "minProperties", "maxProperties", "dependentRequired", "dependentSchemas", "dependencies"}

func (c *compiler) objectGroup(node jsonObject, base string, strict bool) Check {
	onlyCore := true
	for _, k := range objectExtras {
		if node.has(k) {
			onlyCore = false
		}
	}
	// A schema with only the core object keywords gets one closure that also does the type guard.
	g := guardNone
	if onlyCore {
		g = guardLoose
		if strict {
			g = guardStrict
		}
	}
	core := c.objectCore(node, base, g)
	if core != nil && onlyCore {
		return core
	}
	var parts []Check
	if core != nil {
		parts = append(parts, core)
	}
	if names, ok := node.get("propertyNames"); ok {
		parts = append(parts, propertyNamesCheck(c.check(names, base)))
	}
	minProperties, hasMin := node.numberMember("minProperties")
	maxProperties, hasMax := node.numberMember("maxProperties")
	if hasMin || hasMax {
		if !hasMax {
			maxProperties = math.Inf(1)
		}
		parts = append(parts, propertyCountCheck(minProperties, maxProperties))
	}
	var dependentRequired []dependency
	var dependentSchemas []dependentCheck
	if c.draft != Draft7 {
		if m, ok := node.objectMember("dependentRequired"); ok {
			for _, k := range m.keys() {
				if list, ok := m.value(k).([]any); ok {
					dependentRequired = append(dependentRequired, dependency{k, list})
				}
			}
		}
		if m, ok := node.objectMember("dependentSchemas"); ok {
			for _, k := range m.keys() {
				dependentSchemas = append(dependentSchemas, dependentCheck{k, c.check(m.value(k), base)})
			}
		}
	}
	if m, ok := node.objectMember("dependencies"); ok {
		for _, k := range m.keys() {
			d := m.value(k)
			if list, ok := d.([]any); ok {
				dependentRequired = append(dependentRequired, dependency{k, list})
			} else {
				dependentSchemas = append(dependentSchemas, dependentCheck{k, c.check(d, base)})
			}
		}
	}
	if len(dependentRequired) > 0 {
		parts = append(parts, dependentRequiredCheck(dependentRequired))
	}
	if len(dependentSchemas) > 0 {
		parts = append(parts, dependentSchemasCheck(dependentSchemas))
	}
	if len(parts) == 0 {
		if strict {
			return isObject
		}
		return nil
	}
	return guardObject(and(parts), strict)
}

func (c *compiler) objectCore(node jsonObject, base string, g guard) Check {
	properties, _ := node.objectMember("properties")
	patterns, _ := node.objectMember("patternProperties")
	var required []string
	if list, ok := node.arrayMember("required"); ok {
		for _, k := range list {
			if s, ok := k.(string); ok {
				required = append(required, s)
			}
		}
	}
	additional, hasAdditional := node.get("additionalProperties")
	keys := properties.keys()
	patternKeys := patterns.keys()
	additionalOpen := !hasAdditional || additional == true
	if len(keys)+len(required)+len(patternKeys) == 0 && additionalOpen {
		return nil
	}
	entries := make([]property, 0, len(keys))
	for _, k := range keys {
		entries = append(entries, property{key: k, check: c.check(properties.value(k), base), required: slices.Contains(required, k)})
	}
	var extraRequired []string
	for _, k := range required {
		if !properties.has(k) {
			extraRequired = append(extraRequired, k)
		}
	}
	pats := make([]patternCheck, len(patternKeys))
	for i, p := range patternKeys {
		pats[i] = patternCheck{re: sharedRegex(p), check: c.check(patterns.value(p), base)}
	}
	var extra Check
	if !additionalOpen {
		extra = c.check(additional, base)
	}
	if len(pats) == 0 && len(extraRequired) == 0 && (extra == nil || additional == false) {
		return declaredObject(entries, additional == false, g)
	}
	return openObject(entries, keys, extraRequired, pats, extra, g)
}

func (c *compiler) arrayGroup(node jsonObject, base string, strict bool) Check {
	var prefix []any
	var rest any
	hasRest := false
	if items, ok := node.arrayMember("items"); ok {
		prefix = items
		rest, hasRest = node.get("additionalItems")
	} else {
		if c.draft != Draft7 {
			if list, ok := node.arrayMember("prefixItems"); ok {
				prefix = list
			}
		}
		rest, hasRest = node.get("items")
	}
	prefixChecks := make([]Check, len(prefix))
	for i, s := range prefix {
		prefixChecks[i] = c.check(s, base)
	}
	var restCheck Check
	if hasRest && rest != true {
		restCheck = c.check(rest, base)
	}
	minItems, hasMin := node.numberMember("minItems")
	maxItems, hasMax := node.numberMember("maxItems")
	inf := math.Inf(1)
	if !hasMax {
		maxItems = inf
	}
	if !node.has("contains") && node.value("uniqueItems") != true && (len(prefixChecks) > 0 || restCheck != nil) {
		// The common array: one closure for the guard, the length bounds and the items.
		g := guardLoose
		if strict {
			g = guardStrict
		}
		return listCheck(g, minItems, maxItems, prefixChecks, restCheck)
	}
	var parts []Check
	if len(prefixChecks) > 0 || restCheck != nil {
		parts = append(parts, itemsCheck(prefixChecks, restCheck))
	}
	if hasMin || hasMax {
		parts = append(parts, lengthCheck(minItems, maxItems))
	}
	if contains, ok := node.get("contains"); ok {
		min, max := 1.0, inf
		if c.draft != Draft7 {
			if v, ok := node.numberMember("minContains"); ok {
				min = v
			}
			if v, ok := node.numberMember("maxContains"); ok {
				max = v
			}
		}
		parts = append(parts, containsCheck(c.check(contains, base), min, max))
	}
	if node.value("uniqueItems") == true {
		parts = append(parts, uniqueCheck)
	}
	if len(parts) == 0 {
		if strict {
			return isArray
		}
		return nil
	}
	return guardArray(and(parts), strict)
}

// annotating is the annotating check of a schema evaluated in place (only below `unevaluatedProperties`/`unevaluatedItems`).
func (c *compiler) annotating(node any, base string) annotatingCheck {
	if b, ok := node.(bool); ok {
		if b {
			return annotatingTrue
		}
		return annotatingFalse
	}
	o, ok := isSchemaObject(node)
	if !ok {
		notSchema(node)
	}
	return c.annotatingObject(o, base)
}

func (c *compiler) annotatingObject(o jsonObject, base string) annotatingCheck {
	id := o.identity()
	if cached, ok := c.annotatingCache[id]; ok {
		return cached
	}
	slot := &annotatingSlot{}
	c.annotatingCache[id] = indirectAnnotating(slot)
	check := c.buildAnnotating(o, c.r.baseFor(o, base))
	slot.check = check
	c.annotatingCache[id] = check
	return check
}

func (c *compiler) buildAnnotating(node jsonObject, base string) annotatingCheck {
	rest := &Object{}
	annotates := false
	for _, k := range node.keys() {
		if _, ok := annotatingKeywords[k]; ok {
			annotates = true
		} else {
			rest.Set(k, node.value(k))
		}
	}
	plain := c.plain(jsonObject{ordered: rest}, base)
	var parts []annotatingCheck
	if annotates {
		parts = c.annotatingParts(node, base)
	}
	var all annotatingCheck
	if len(parts) == 0 {
		all = ignoreAnnotations(plain)
	} else {
		all = annotateAll(plain, parts)
	}
	own := all
	if node.has("unevaluatedProperties") || node.has("unevaluatedItems") {
		own = isolateAnnotations(all)
	}
	if c.dynamic && c.r.isResource(node, base) {
		return scopedAnnotating(c.scope, base, own)
	}
	return own
}

func (c *compiler) annotatingParts(node jsonObject, base string) []annotatingCheck {
	var parts []annotatingCheck
	if node.has("properties") || node.has("patternProperties") || node.has("additionalProperties") {
		var props []namedCheck
		if m, ok := node.objectMember("properties"); ok {
			for _, k := range m.keys() {
				props = append(props, namedCheck{k, c.check(m.value(k), base)})
			}
		}
		var pats []patternCheck
		if m, ok := node.objectMember("patternProperties"); ok {
			for _, p := range m.keys() {
				pats = append(pats, patternCheck{sharedRegex(p), c.check(m.value(p), base)})
			}
		}
		var extra Check
		if additional, ok := node.get("additionalProperties"); ok {
			extra = c.check(additional, base)
		}
		parts = append(parts, annotateObject(props, pats, extra))
	}
	if node.has("prefixItems") || node.has("items") || node.has("contains") {
		var prefix []any
		var rest any
		hasRest := false
		items, itemsIsArray := node.arrayMember("items")
		if list, ok := node.arrayMember("prefixItems"); ok {
			prefix = list
		} else if itemsIsArray {
			prefix = items
		}
		if itemsIsArray {
			rest, hasRest = node.get("additionalItems")
		} else {
			rest, hasRest = node.get("items")
		}
		prefixChecks := make([]Check, len(prefix))
		for i, s := range prefix {
			prefixChecks[i] = c.check(s, base)
		}
		var restCheck, containsCheck Check
		if hasRest {
			restCheck = c.check(rest, base)
		}
		if contains, ok := node.get("contains"); ok {
			containsCheck = c.check(contains, base)
		}
		minContains, ok := node.numberMember("minContains")
		if !ok {
			minContains = 1
		}
		maxContains, ok := node.numberMember("maxContains")
		if !ok {
			maxContains = math.Inf(1)
		}
		parts = append(parts, annotateArray(prefixChecks, restCheck, containsCheck, minContains, maxContains))
	}
	if list, ok := node.arrayMember("allOf"); ok {
		for _, s := range list {
			parts = append(parts, c.annotating(s, base))
		}
	}
	for _, branch := range []struct {
		keyword    string
		exactlyOne bool
	}{{"anyOf", false}, {"oneOf", true}} {
		if list, ok := node.arrayMember(branch.keyword); ok {
			checks := make([]annotatingCheck, len(list))
			for i, s := range list {
				checks[i] = c.annotating(s, base)
			}
			parts = append(parts, annotateAny(checks, branch.exactlyOne))
		}
	}
	if cond, ok := node.get("if"); ok {
		var then, otherwise annotatingCheck
		if s, ok := node.get("then"); ok {
			then = c.annotating(s, base)
		}
		if s, ok := node.get("else"); ok {
			otherwise = c.annotating(s, base)
		}
		parts = append(parts, annotateIf(c.annotating(cond, base), then, otherwise))
	}
	if ref, ok := node.stringMember("$ref"); ok {
		parts = append(parts, c.annotatingRef(ref, base))
	}
	if ref, ok := node.stringMember("$dynamicRef"); ok {
		parts = append(parts, c.annotatingDynamicRef(ref, base))
	}
	if m, ok := node.objectMember("dependentSchemas"); ok {
		for _, k := range m.keys() {
			parts = append(parts, annotateDependent(k, c.annotating(m.value(k), base)))
		}
	}
	if s, ok := node.get("unevaluatedProperties"); ok {
		parts = append(parts, unevaluatedProperties(c.check(s, base)))
	}
	if s, ok := node.get("unevaluatedItems"); ok {
		parts = append(parts, unevaluatedItems(c.check(s, base)))
	}
	return parts
}

func (c *compiler) annotatingRef(ref, base string) annotatingCheck {
	target := c.r.resolve(ref, base)
	check := c.annotating(target.node, target.base)
	if c.dynamic && target.base != base {
		return scopedAnnotating(c.scope, target.base, check)
	}
	return check
}

func (c *compiler) annotatingDynamicRef(ref, base string) annotatingCheck {
	initial, name, ok := c.bookended(ref, base)
	if !ok {
		return c.annotatingRef(ref, base)
	}
	fallback := c.annotating(initial.node, initial.base)
	lookup := dynamicLookup(c, name, func(node any, b string) annotatingCheck { return c.annotating(node, b) })
	scope := c.scope
	return func(v any, a *evaluated) bool {
		for _, uri := range scope.uris {
			if check := lookup(uri); check != nil {
				return (*check)(v, a)
			}
		}
		return fallback(v, a)
	}
}

// neutralKeywords never affect a check.
var neutralKeywords = setOf("$schema", "$comment", "title", "description", "default", "examples", "deprecated", "readOnly", "writeOnly", "$defs", "definitions", "contentEncoding", "contentMediaType", "minContains", "maxContains", "$vocabulary")

// annotatingKeywords are the keywords an annotating check handles itself; every other keyword compiles into its plain part.
var annotatingKeywords = setOf("properties", "patternProperties", "additionalProperties", "prefixItems", "items", "additionalItems", "contains", "minContains", "maxContains", "allOf", "anyOf", "oneOf", "if", "then", "else", "$ref", "$dynamicRef", "dependentSchemas", "unevaluatedProperties", "unevaluatedItems")

func setOf(keys ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		set[k] = struct{}{}
	}
	return set
}

// constUnion is anyOf/oneOf whose branches are all `{ const: primitive }`: one shared enum (distinct values make oneOf the same).
func (c *compiler) constUnion(branches []any, exactlyOne bool) Check {
	values := make([]any, 0, len(branches))
	for _, branch := range branches {
		o, ok := isSchemaObject(branch)
		if !ok || !o.has("const") {
			return nil
		}
		for _, k := range o.keys() {
			if _, neutral := neutralKeywords[k]; k != "const" && !neutral {
				return nil
			}
		}
		value := o.value("const")
		if isContainer(value) {
			return nil
		}
		if exactlyOne && slices.ContainsFunc(values, func(x any) bool { return sameValueZero(x, value) }) {
			return nil
		}
		values = append(values, value)
	}
	return enumCheck(values)
}

// tagOf is the tag of a tagged-union branch: a primitive const on a required property of an object schema.
func tagOf(branch jsonObject, key string) (any, bool) {
	if typeName, _ := branch.get("type"); typeName != "object" {
		return nil, false
	}
	required, ok := branch.arrayMember("required")
	if !ok || !slices.ContainsFunc(required, func(x any) bool { return x == key }) {
		return nil, false
	}
	properties, ok := branch.objectMember("properties")
	if !ok {
		return nil, false
	}
	tag, ok := properties.objectMember(key)
	if !ok || !tag.has("const") {
		return nil, false
	}
	value := tag.value("const")
	if isContainer(value) {
		return nil, false
	}
	return value, true
}
