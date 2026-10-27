package engine

import (
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

// eng carries what TypeBox keeps in module state: the instantiation counters of call/instantiate.mjs. One engine
// call has one, so concurrent calls do not share a count.
type eng struct{ depth, count int }

// maxInstantiationCount is Settings.maxInstantiationCount.
const maxInstantiationCount = 128

// state is the engine's State(callstack, visited).
type state struct{ callstack, visited []string }

func newState() state { return state{} }

func canInstantiate(types ...any) bool {
	return !slices.ContainsFunc(types, isRef)
}

func (e *eng) instantiateProperties(ctx *Obj, st state, properties any) *Obj {
	result := NewObj()
	for _, key := range ownKeys(properties) {
		result = with(result, key, e.instantiateType(ctx, st, get(properties, key)))
	}
	return result
}

func (e *eng) instantiateElements(ctx *Obj, st state, types any) []any {
	return restSpread(e.instantiateTypes(ctx, st, types))
}

func (e *eng) instantiateTypes(ctx *Obj, st state, types any) []any {
	return mapTypes(types, func(t any) any { return e.instantiateType(ctx, st, t) })
}

func withModifiers(typ, instantiated any) any {
	withOptional := instantiated
	if isOptional(typ) {
		withOptional = addOptionalAction(instantiated, NewObj())
	}
	withReadonly := withOptional
	if isReadonly(typ) {
		withReadonly = addReadonlyAction(withOptional, NewObj())
	}
	if isImmutable(typ) {
		return addImmutableAction(withReadonly, NewObj())
	}
	return withReadonly
}

// instantiateDeferred is InstantiateDeferred: the action named by a Deferred node, instantiated; an unknown action
// stays deferred.
func (e *eng) instantiateDeferred(ctx *Obj, st state, action any, parameters any, options any) any {
	p := func(i int) any { return get(parameters, fmt.Sprint(i)) }
	name, _ := action.(string)
	switch name {
	case "AddImmutable":
		return e.addImmutableInstantiate(ctx, st, p(0), options)
	case "RemoveImmutable":
		return e.removeInstantiate(ctx, st, p(0), options, "~immutable")
	case "AddReadonly":
		return e.addReadonlyInstantiate(ctx, st, p(0), options)
	case "RemoveReadonly":
		return e.removeInstantiate(ctx, st, p(0), options, "~readonly")
	case "AddOptional":
		return e.addOptionalInstantiate(ctx, st, p(0), options)
	case "RemoveOptional":
		return e.removeInstantiate(ctx, st, p(0), options, "~optional")
	case "Capitalize", "Lowercase", "Uncapitalize", "Uppercase":
		return e.intrinsicInstantiate(ctx, st, name, p(0), options)
	case "Conditional":
		return e.conditionalInstantiate(ctx, st, p(0), p(1), p(2), p(3), options)
	case "ConstructorParameters":
		return e.constructorParametersInstantiate(ctx, st, p(0), options)
	case "Evaluate":
		return e.evaluateInstantiate(ctx, st, p(0), options)
	case "Exclude":
		return e.excludeInstantiate(ctx, st, p(0), p(1), options)
	case "Extract":
		return e.extractInstantiate(ctx, st, p(0), p(1), options)
	case "Index":
		return e.indexInstantiate(ctx, st, p(0), p(1), options)
	case "InstanceType":
		return e.instanceTypeInstantiate(ctx, st, p(0), options)
	case "Interface":
		return e.interfaceInstantiate(ctx, st, p(0), p(1), options)
	case "KeyOf":
		return e.keyOfInstantiate(ctx, st, p(0), options)
	case "Mapped":
		return e.mappedInstantiate(ctx, st, p(0), p(1), p(2), p(3), options)
	case "Module":
		return e.moduleInstantiate(ctx, p(0), options)
	case "NonNullable":
		return e.nonNullableInstantiate(ctx, st, p(0), options)
	case "Pick":
		return e.pickInstantiate(ctx, st, p(0), p(1), options)
	case "Parameters":
		return e.parametersInstantiate(ctx, st, p(0), options)
	case "Partial":
		return e.partialInstantiate(ctx, st, p(0), options)
	case "Omit":
		return e.omitInstantiate(ctx, st, p(0), p(1), options)
	case "ReadonlyObject":
		return e.readonlyObjectInstantiate(ctx, st, p(0), options)
	case "Record":
		return e.recordInstantiate(ctx, st, p(0), p(1), options)
	case "Required":
		return e.requiredInstantiate(ctx, st, p(0), options)
	case "ReturnType":
		return e.returnTypeInstantiate(ctx, st, p(0), options)
	case "TemplateLiteral":
		return e.templateLiteralInstantiate(ctx, st, p(0), options)
	case "With":
		return e.withInstantiate(ctx, st, p(0), p(1))
	}
	return tDeferred0(action, parameters, options)
}

// tDeferred0 is Deferred(action, parameters, options) with the action as the value it came in as.
func tDeferred0(action, parameters, options any) *Obj {
	return kindOf("Deferred", NewObj("type", "deferred", "action", action, "parameters", parameters, "options", options), NewObj())
}

func (e *eng) instantiateImmediate(ctx *Obj, st state, t any) any {
	var inst any
	switch {
	case isRef(t):
		inst = e.refInstantiate(ctx, st, t, get(t, "$ref"))
	case isArrayType(t):
		inst = tArray(e.instantiateType(ctx, st, get(t, "items")), Discard(t, "~kind", "type", "items"))
	case isCall(t):
		inst = e.callInstantiate(ctx, st, get(t, "target"), get(t, "arguments"))
	case isConstructor(t):
		inst = tConstructor(e.instantiateTypes(ctx, st, get(t, "parameters")), e.instantiateType(ctx, st, get(t, "instanceType")), Discard(t, "~kind", "type", "parameters", "instanceType"))
	case isFunction(t):
		inst = tFunction(e.instantiateTypes(ctx, st, get(t, "parameters")), e.instantiateType(ctx, st, get(t, "returnType")), Discard(t, "~kind", "type", "parameters", "returnType"))
	case isDependent(t):
		inst = tDependent(e.instantiateType(ctx, st, get(t, "if")), e.instantiateType(ctx, st, get(t, "then")), e.instantiateType(ctx, st, get(t, "else")), Discard(t, "~kind", "if", "then", "else"))
	case isIntersect(t):
		inst = tIntersect(e.instantiateTypes(ctx, st, get(t, "allOf")), Discard(t, "~kind", "allOf"))
	case isObjectType(t):
		inst = tObject(e.instantiateProperties(ctx, st, get(t, "properties")), Discard(t, "~kind", "type", "properties", "required"))
	case isRecord(t):
		inst = createRecord(recordPattern(t), e.instantiateType(ctx, st, recordValue(t)))
	case isRest(t):
		inst = tRest(e.instantiateType(ctx, st, get(t, "items")))
	case isTuple(t):
		inst = tTuple(e.instantiateElements(ctx, st, get(t, "items")), Discard(t, "~kind", "type", "items", "minItems", "additionalItems"))
	case isUnion(t):
		inst = tUnion(e.instantiateTypes(ctx, st, get(t, "anyOf")), Discard(t, "~kind", "anyOf"))
	default:
		inst = t
	}
	return withModifiers(t, inst)
}

func (e *eng) instantiateType(ctx *Obj, st state, t any) any {
	if isDeferred(t) {
		return e.instantiateDeferred(ctx, st, get(t, "action"), get(t, "parameters"), get(t, "options"))
	}
	return e.instantiateImmediate(ctx, st, t)
}

// Instantiate is the `Instantiate(context, type)` of the Type namespace.
func (e *eng) instantiate(ctx *Obj, t any) any { return e.instantiateType(ctx, newState(), t) }

// ---- modifiers (engine/{optional,readonly,immutable}) ----

func addOptionalAction(t, options any) any {
	return Update(Update(t, NewObj("~optional", true), NewObj()), nil0(), options)
}
func addReadonlyAction(t, options any) any {
	return Update(Update(t, NewObj("~readonly", true), NewObj()), nil0(), options)
}
func addImmutableAction(t, options any) any {
	return Update(Update(t, NewObj("~immutable", true), NewObj()), nil0(), options)
}

// nil0 is the `{}` of `Memory.Update(x, {}, options)`.
func nil0() *Obj { return NewObj() }

func removeAction(t, options any, key string) any {
	return Update(Discard(t, key), nil0(), options)
}

func (e *eng) addOptionalInstantiate(ctx *Obj, st state, t, options any) any {
	return addOptionalAction(e.instantiateType(ctx, st, t), options)
}
func (e *eng) addReadonlyInstantiate(ctx *Obj, st state, t, options any) any {
	return addReadonlyAction(e.instantiateType(ctx, st, t), options)
}
func (e *eng) addImmutableInstantiate(ctx *Obj, st state, t, options any) any {
	return addImmutableAction(e.instantiateType(ctx, st, t), options)
}
func (e *eng) removeInstantiate(ctx *Obj, st state, t, options any, key string) any {
	return removeAction(e.instantiateType(ctx, st, t), options, key)
}

// ---- with ----

func withAction(t, options any) any {
	if canInstantiate(t) {
		return Update(t, nil0(), options)
	}
	return tDeferred("With", []any{t, options}, NewObj())
}

func (e *eng) withInstantiate(ctx *Obj, st state, t, options any) any {
	return withAction(e.instantiateType(ctx, st, t), options)
}

// ---- rest/spread ----

func restSpread(types []any) []any {
	result := []any{}
	for _, left := range types {
		result = append(result, spreadElement(left)...)
	}
	return result
}

func spreadElement(t any) []any {
	if isRest(t) {
		items := get(t, "items")
		switch {
		case isTuple(items):
			return restSpread(asArray(get(items, "items"), "reduce"))
		case isInfer(items), isRef(items):
			return []any{t}
		}
		return []any{tNever(nil)}
	}
	return []any{t}
}

// ---- ref ----

func (e *eng) refInstantiate(ctx *Obj, st state, t any, ref any) any {
	name := propKey(ref)
	if slices.Contains(st.visited, name) {
		return t
	}
	if hasKey(ctx, name) {
		next := state{callstack: st.callstack, visited: append(slices.Clone(st.visited), name)}
		return e.instantiateType(ctx, next, get(ctx, name))
	}
	return t
}

// ---- pick / omit / partial / required / keyof / indexed ----

func (e *eng) pickAction(t, indexer, options any) any {
	if canInstantiate(t, indexer) {
		return Update(e.pickFromType(t, indexer), nil0(), options)
	}
	return tDeferred("Pick", []any{t, indexer}, options)
}

func (e *eng) pickInstantiate(ctx *Obj, st state, t, indexer, options any) any {
	return e.pickAction(e.instantiateType(ctx, st, t), e.instantiateType(ctx, st, indexer), options)
}

func (e *eng) pickFromType(t, indexer any) any {
	indexable := e.toIndexable(t)
	keys := e.toIndexableKeys(indexer)
	applied := NewObj()
	for _, key := range ownKeys(indexable) {
		if slices.Contains(keys, key) {
			applied = Assign(applied, NewObj(key, get(indexable, key)))
		}
	}
	return tObject(applied, nil)
}

func (e *eng) omitAction(t, indexer, options any) any {
	if canInstantiate(t, indexer) {
		return Update(e.omitFromType(t, indexer), nil0(), options)
	}
	return tDeferred("Omit", []any{t, indexer}, options)
}

func (e *eng) omitInstantiate(ctx *Obj, st state, t, indexer, options any) any {
	return e.omitAction(e.instantiateType(ctx, st, t), e.instantiateType(ctx, st, indexer), options)
}

func (e *eng) omitFromType(t, indexer any) any {
	indexable := e.toIndexable(t)
	keys := e.toIndexableKeys(indexer)
	omitted := NewObj()
	for _, key := range ownKeys(indexable) {
		if !slices.Contains(keys, key) {
			omitted = with(omitted, key, get(indexable, key))
		}
	}
	return tObject(omitted, nil)
}

// partialLike is the shared walk of Partial and Required: member maps each property of an object, cyclic/dependent/
// intersect/union recurse, anything else is `Object({})`.
func (e *eng) partialLike(t any, member func(any) any) any {
	var from func(t any) any
	from = func(t any) any {
		switch {
		case isCyclic(t):
			defs, ref := get(t, "$defs"), get(t, "$ref")
			target := cyclicTarget(defs, ref)
			return tCyclic(Assign(defs, NewObj(propKey(ref), from(target))), ref, nil)
		case isDependent(t):
			return from(e.evaluateDependent(get(t, "if"), get(t, "then"), get(t, "else")))
		case isIntersect(t):
			return from(e.evaluateIntersect(get(t, "allOf")))
		case isUnion(t):
			return tUnion(mapTypes(get(t, "anyOf"), from), nil)
		case isObjectType(t):
			properties := get(t, "properties")
			mapped := NewObj()
			for _, left := range ownKeys(properties) {
				mapped = with(mapped, left, member(get(properties, left)))
			}
			return tObject(mapped, nil)
		}
		return tObject(NewObj(), nil)
	}
	return from(t)
}

func (e *eng) partialAction(t, options any) any {
	if canInstantiate(t) {
		return Update(e.partialLike(t, func(p any) any { return addOptionalAction(p, nil0()) }), nil0(), options)
	}
	return tDeferred("Partial", []any{t}, options)
}

func (e *eng) partialInstantiate(ctx *Obj, st state, t, options any) any {
	return e.partialAction(e.instantiateType(ctx, st, t), options)
}

func (e *eng) requiredAction(t, options any) any {
	if canInstantiate(t) {
		return Update(e.partialLike(t, func(p any) any { return removeAction(p, nil0(), "~optional") }), nil0(), options)
	}
	return tDeferred("Required", []any{t}, options)
}

func (e *eng) requiredInstantiate(ctx *Obj, st state, t, options any) any {
	return e.requiredAction(e.instantiateType(ctx, st, t), options)
}

// ---- readonly object ----

func (e *eng) readonlyObjectFromType(t any) any {
	switch {
	case isArrayType(t):
		return addImmutableAction(tArray(get(t, "items"), nil), nil0())
	case isCyclic(t):
		defs, ref := get(t, "$defs"), get(t, "$ref")
		target := cyclicTarget(defs, ref)
		return tCyclic(Assign(defs, NewObj(propKey(ref), e.readonlyObjectFromType(target))), ref, nil)
	case isDependent(t):
		return e.readonlyObjectFromType(e.evaluateDependent(get(t, "if"), get(t, "then"), get(t, "else")))
	case isIntersect(t):
		return e.readonlyObjectFromType(e.evaluateIntersect(get(t, "allOf")))
	case isObjectType(t):
		properties := get(t, "properties")
		mapped := NewObj()
		for _, left := range ownKeys(properties) {
			mapped = with(mapped, left, addReadonlyAction(get(properties, left), nil0()))
		}
		return tObject(mapped, nil)
	case isTuple(t):
		return addImmutableAction(tTuple(get(t, "items"), nil), nil0())
	case isUnion(t):
		return tUnion(mapTypes(get(t, "anyOf"), e.readonlyObjectFromType), nil)
	}
	return t
}

func (e *eng) readonlyObjectAction(t, options any) any {
	if canInstantiate(t) {
		return Update(e.readonlyObjectFromType(t), nil0(), options)
	}
	return tDeferred("ReadonlyObject", []any{t}, NewObj())
}

func (e *eng) readonlyObjectInstantiate(ctx *Obj, st state, t, options any) any {
	return e.readonlyObjectAction(e.instantiateType(ctx, st, t), options)
}

// ---- keyof ----

func (e *eng) keyOfNormalize(t any) any {
	if isCyclic(t) || isDependent(t) || isIntersect(t) || isUnion(t) {
		return e.collapseToObject(t)
	}
	return t
}

func (e *eng) keyOfAction(t, options any) any {
	if canInstantiate(t) {
		return Update(e.keyOfFromType(e.keyOfNormalize(t)), nil0(), options)
	}
	return tDeferred("KeyOf", []any{t}, options)
}

func (e *eng) keyOfInstantiate(ctx *Obj, st state, t, options any) any {
	return e.keyOfAction(e.instantiateType(ctx, st, t), options)
}

// convertToIntegerKey is ConvertToIntegerKey: `^(?:0|[1-9][0-9]*)$` keys become numbers.
func convertToIntegerKey(value any) any {
	text := toStringValue(value)
	if isIntegerKeyText(text) {
		return jsnumber.ParseInt(text)
	}
	return value
}

func isIntegerKeyText(text string) bool {
	if text == "0" {
		return true
	}
	if text == "" || text[0] < '1' || text[0] > '9' {
		return false
	}
	for i := 1; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}
	return true
}

func (e *eng) keyOfFromType(t any) any {
	switch {
	case isAny(t):
		return tUnion([]any{tNumber(nil), tString(nil), tSymbol(nil)}, nil)
	case isArrayType(t):
		return tNumber(nil)
	case isObjectType(t):
		variants := []any{}
		for _, left := range ownKeys(get(t, "properties")) {
			variants = append(variants, tLiteral(convertToIntegerKey(left), nil))
		}
		return e.evaluateUnionFast(variants)
	case isRecord(t):
		return recordKey(t)
	case isTuple(t):
		items := asArray(get(t, "items"), "map")
		result := make([]any, len(items))
		for i := range items {
			result[i] = tLiteral(float64(i), nil)
		}
		return e.evaluateUnionFast(result)
	}
	return tNever(nil)
}

// ---- indexed ----

func (e *eng) indexAction(t, indexer, options any) any {
	if canInstantiate(t, indexer) {
		return Update(e.indexFromType(e.keyOfNormalize(t), indexer), nil0(), options)
	}
	return tDeferred("Index", []any{t, indexer}, options)
}

func (e *eng) indexInstantiate(ctx *Obj, st state, t, indexer, options any) any {
	return e.indexAction(e.instantiateType(ctx, st, t), e.instantiateType(ctx, st, indexer), options)
}

func (e *eng) indexFromType(t, indexer any) any {
	switch {
	case isArrayType(t):
		return e.indexFromArray(get(t, "items"), indexer)
	case isObjectType(t):
		return e.indexFromObject(get(t, "properties"), indexer)
	case isTuple(t):
		return e.indexFromTuple(asArray(get(t, "items"), "reduceRight"), indexer)
	}
	return tNever(nil)
}

func normalizeIndexer(t any) any {
	switch {
	case isIntersect(t):
		return tIntersect(mapTypes(get(t, "allOf"), normalizeIndexer), nil)
	case isUnion(t):
		return tUnion(mapTypes(get(t, "anyOf"), normalizeIndexer), nil)
	case isLiteral(t):
		return tLiteral(convertToIntegerKey(get(t, "const")), nil)
	}
	return t
}

func (e *eng) indexFromArray(items, indexer any) any {
	normalized := normalizeIndexer(indexer)
	check := e.extends(NewObj(), normalized, tNumber(nil))
	switch {
	case isExtendsTrueLike(check):
		return items
	case isLiteral(indexer) && jsEqual(get(indexer, "const"), "length"):
		return tNumber(nil)
	}
	return tNever(nil)
}

func (e *eng) indexProperty(properties any, key string) any {
	var selected any
	if hasKey(properties, key) {
		selected = get(properties, key)
	} else {
		selected = tNever(nil)
	}
	return expandThis(properties, selected)
}

func (e *eng) indexProperties(properties any, keys []string) []any {
	out := []any{}
	for _, k := range keys {
		out = append(out, e.indexProperty(properties, k))
	}
	return out
}

func (e *eng) indexFromObject(properties, indexer any) any {
	if isNumberType(indexer) {
		numeric := []string{}
		for _, k := range ownKeys(properties) {
			if isIntegerKeyText(k) || isNegativeIntegerKey(k) {
				numeric = append(numeric, k)
			}
		}
		return e.evaluateUnion(e.indexProperties(properties, numeric))
	}
	keys := e.toIndexableKeys(indexer)
	return e.evaluateUnion(e.indexProperties(properties, keys))
}

// isNegativeIntegerKey completes IntegerKey (`^-?(?:0|[1-9][0-9]*)$`) for the leading minus.
func isNegativeIntegerKey(text string) bool {
	return len(text) > 1 && text[0] == '-' && isIntegerKeyText(text[1:])
}

func (e *eng) indexFromTuple(types []any, indexer any) any {
	switch {
	case isLiteral(indexer) && jsEqual(get(indexer, "const"), "length"):
		return tLiteral(float64(len(types)), nil)
	case isNumberType(indexer) || isInteger(indexer):
		return e.evaluateUnionFast(slices.Clone(types))
	}
	formatted := formatArrayIndexer(indexer)
	elements := []any{}
	for index, typ := range slices.Backward(types) {
		check := e.extends(NewObj(), tLiteral(float64(index), nil), formatted)
		if isExtendsTrueLike(check) {
			elements = append([]any{typ}, elements...)
		}
	}
	return e.evaluateUnionFast(elements)
}

func formatArrayIndexer(t any) any {
	switch {
	case isIntersect(t):
		return tIntersect(mapTypes(get(t, "allOf"), formatArrayIndexer), nil)
	case isUnion(t):
		return tUnion(mapTypes(get(t, "anyOf"), formatArrayIndexer), nil)
	case isLiteral(t):
		return tLiteral(convertToIntegerKey(get(t, "const")), nil)
	}
	return t
}

// toStringValue is `${value}` for the values a literal holds.
func toStringValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case float64:
		return jsnumber.String(v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	}
	if b, ok := value.(interface{ String() string }); ok {
		return b.String()
	}
	return toDisplay(value)
}
