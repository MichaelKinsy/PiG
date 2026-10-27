package engine

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// ---- engine/evaluate ----

const (
	compareEqual = iota
	compareDisjoint
	compareLeftInside
	compareRightInside
)

func (e *eng) compare(left, right any) int {
	l := e.extends(NewObj(), left, right)
	r := e.extends(NewObj(), right, left)
	switch {
	case isExtendsTrueLike(l) && isExtendsTrueLike(r):
		return compareEqual
	case isExtendsTrueLike(l) && isExtendsFalse(r):
		return compareLeftInside
	case isExtendsFalse(l) && isExtendsTrueLike(r):
		return compareRightInside
	}
	return compareDisjoint
}

func (e *eng) broadenFilter(t any, types []any) []any {
	result := []any{}
	for _, left := range types {
		switch e.compare(t, left) {
		case compareLeftInside, compareEqual:
			return types
		case compareDisjoint:
			result = append(result, left)
		}
	}
	return append(result, t)
}

func (e *eng) broaden(types []any) []any {
	result := []any{}
	for _, t := range types {
		evaluated := e.evaluateType(t)
		switch {
		case isAny(evaluated), isUnknown(evaluated):
			return flatten([]any{evaluated})
		case isNever(evaluated):
		case isObjectType(evaluated):
			result = append(slices.Clone(result), evaluated)
		default:
			result = e.broadenFilter(evaluated, result)
		}
	}
	return flatten(result)
}

func flatten(types []any) []any {
	result := []any{}
	for _, left := range types {
		if isUnion(left) {
			result = append(result, flatten(asArray(get(left, "anyOf"), "reduce"))...)
		} else {
			result = append(result, left)
		}
	}
	return result
}

func canComposite(t any) bool { return isObjectType(t) || isTuple(t) }

func (e *eng) compositeProperty(left, right any) any {
	isReadonlyP := isReadonly(left) && isReadonly(right)
	isOptionalP := isOptional(left) && isOptional(right)
	evaluated := e.evaluateIntersect([]any{left, right})
	property := removeAction(removeAction(evaluated, nil0(), "~optional"), nil0(), "~readonly")
	switch {
	case isReadonlyP && isOptionalP:
		return addReadonlyAction(addOptionalAction(property, nil0()), nil0())
	case isReadonlyP:
		return addReadonlyAction(property, nil0())
	case isOptionalP:
		return addOptionalAction(property, nil0())
	}
	return property
}

func (e *eng) compositeProperties(left, right any) *Obj {
	var keys []string
	seen := map[string]bool{}
	for _, k := range append(ownKeys(left), ownKeys(right)...) {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	result := NewObj()
	for _, key := range keys {
		var v any
		switch {
		case hasKey(left, key):
			if hasKey(right, key) {
				v = e.compositeProperty(get(left, key), get(right, key))
			} else {
				v = get(left, key)
			}
		case hasKey(right, key):
			v = get(right, key)
		default:
			v = tNever(nil)
		}
		result = with(result, key, v)
	}
	return result
}

func getProperties(t any) any {
	switch {
	case isObjectType(t):
		return get(t, "properties")
	case isTuple(t):
		return tupleElementsToProperties(asArray(get(t, "items"), "reduceRight"))
	}
	return NewObj()
}

func (e *eng) composite(left, right any) any {
	return tObject(e.compositeProperties(getProperties(left), getProperties(right)), nil)
}

func (e *eng) narrowCompareRule(left, right any) any {
	switch e.compare(left, right) {
	case compareLeftInside:
		return left
	case compareRightInside, compareEqual:
		return right
	}
	return tNever(nil)
}

func (e *eng) narrow(left, right any) any {
	switch {
	case isNever(left), isAny(left):
		return left
	case isUnknown(left):
		return right
	case isNever(right), isAny(right):
		return right
	case isUnknown(right):
		return left
	}
	cl, cr := canComposite(left), canComposite(right)
	switch {
	case cl && cr:
		return e.composite(left, right)
	case cl:
		return left
	case cr:
		return right
	}
	return e.narrowCompareRule(left, right)
}

func (e *eng) distributeOperation(left, right any) any {
	el := e.evaluateType(left)
	er := e.evaluateType(right)
	if isUnion(el) || isUnion(er) {
		return e.evaluateIntersect([]any{el, er})
	}
	return e.narrow(el, er)
}

func (e *eng) distributeType(t any, types []any) []any {
	result := []any{}
	for _, left := range types {
		result = append(result, e.distributeOperation(left, t))
	}
	if len(result) == 0 {
		return []any{t}
	}
	return result
}

func (e *eng) distributeUnion(types []any, distribution []any) []any {
	result := []any{}
	for _, left := range types {
		result = append(result, e.distribute([]any{left}, distribution)...)
	}
	return result
}

func (e *eng) distribute(types []any, result []any) []any {
	for _, left := range types {
		if isUnion(left) {
			result = e.distributeUnion(asArray(get(left, "anyOf"), "reduce"), result)
		} else {
			result = e.distributeType(left, result)
		}
	}
	return result
}

func (e *eng) evaluateDependent(ifT, thenT, elseT any) any {
	intersected := e.evaluateIntersect([]any{ifT, thenT})
	excluded := e.excludeOperation(elseT, ifT)
	return e.evaluateUnion([]any{intersected, excluded})
}

func (e *eng) evaluateEnum(values any) any {
	result := []any{}
	for _, left := range asArray(values, "reduce") {
		result = append(result, tLiteral(left, nil))
	}
	return e.evaluateUnion(result)
}

func (e *eng) evaluateIntersect(types any) any {
	distribution := e.distribute(asArray(types, "reduce"), []any{})
	broadened := e.broaden(distribution)
	return e.evaluateUnion(broadened)
}

func (e *eng) evaluateTemplateLiteral(pattern any) any {
	return e.evaluateType(templateLiteralDecode(pattern))
}

func (e *eng) evaluateUnion(types []any) any {
	return e.evaluateUnionFast(e.broaden(types))
}

func (e *eng) evaluateType(t any) any {
	switch {
	case isDependent(t):
		return e.evaluateDependent(get(t, "if"), get(t, "then"), get(t, "else"))
	case isEnum(t):
		return e.evaluateEnum(get(t, "enum"))
	case isIntersect(t):
		return e.evaluateIntersect(get(t, "allOf"))
	case isTemplateLiteral(t):
		return e.evaluateTemplateLiteral(get(t, "pattern"))
	case isUnion(t):
		return e.evaluateUnion(asArray(get(t, "anyOf"), "reduce"))
	}
	return t
}

func (e *eng) evaluateUnionFast(types []any) any {
	switch len(types) {
	case 1:
		return types[0]
	case 0:
		return tNever(nil)
	}
	return tUnion(types, nil)
}

func (e *eng) evaluateAction(t, options any) any { return Update(e.evaluateType(t), nil0(), options) }

func (e *eng) evaluateInstantiate(ctx *Obj, st state, t, options any) any {
	return e.evaluateAction(e.instantiateType(ctx, st, t), options)
}

// ---- engine/object (CollapseToObject) ----

func (e *eng) collapseToObject(t any) any { return tObject(e.objectFromType(t), nil) }

func (e *eng) objectFromType(t any) any {
	switch {
	case isCyclic(t):
		return e.objectFromType(cyclicTarget(get(t, "$defs"), get(t, "$ref")))
	case isDependent(t):
		return e.objectFromType(e.evaluateDependent(get(t, "if"), get(t, "then"), get(t, "else")))
	case isIntersect(t):
		var result any = NewObj()
		for _, left := range asArray(get(t, "allOf"), "reduce") {
			result = e.collapseIntersectProperties(result, e.objectFromType(left))
		}
		return result
	case isUnion(t):
		types := asArray(get(t, "anyOf"), "length")
		if len(types) == 0 {
			unreachable()
		}
		result := e.objectFromType(types[0])
		for _, left := range types[1:] {
			result = e.collapseUnionProperties(result, e.objectFromType(left))
		}
		return result
	case isTuple(t):
		return e.objectFromType(tupleToObject(tTuple(get(t, "items"), nil)))
	case isObjectType(t):
		return get(t, "properties")
	}
	return NewObj()
}

func (e *eng) collapseIntersectProperties(left, right any) any {
	leftProps, rightProps, shared := NewObj(), NewObj(), NewObj()
	for _, key := range ownKeys(left) {
		if !hasKey(right, key) {
			leftProps = with(leftProps, key, get(left, key))
		}
	}
	for _, key := range ownKeys(right) {
		if !hasKey(left, key) {
			rightProps = with(rightProps, key, get(right, key))
		}
	}
	for _, key := range ownKeys(left) {
		if hasKey(right, key) {
			shared = with(shared, key, e.evaluateIntersect([]any{get(left, key), get(right, key)}))
		}
	}
	return Assign(Assign(leftProps, rightProps), shared)
}

func (e *eng) collapseUnionProperties(left, right any) any {
	result := NewObj()
	for _, key := range ownKeys(left) {
		if hasKey(right, key) {
			result = with(result, key, e.evaluateUnion([]any{get(left, key), get(right, key)}))
		}
	}
	return result
}

func tupleElementsToProperties(types []any) *Obj {
	result := NewObj()
	for index, typ := range slices.Backward(types) {
		result = Assign(NewObj(fmt.Sprint(index), typ), result)
	}
	return result
}

func tupleToObject(t any) any {
	return tObject(tupleElementsToProperties(asArray(get(t, "items"), "reduceRight")), nil)
}

// ---- engine/indexable ----

func (e *eng) toIndexable(t any) any {
	collapsed := e.collapseToObject(t)
	if isObjectType(collapsed) {
		return get(collapsed, "properties")
	}
	unreachable()
	return nil
}

func (e *eng) toIndexableKeys(t any) []string { return e.indexableFromType(t) }

func (e *eng) indexableFromType(t any) []string {
	switch {
	case isCyclic(t):
		return e.indexableFromType(cyclicTarget(get(t, "$defs"), get(t, "$ref")))
	case isDependent(t):
		return e.indexableFromType(e.evaluateDependent(get(t, "if"), get(t, "then"), get(t, "else")))
	case isEnum(t):
		return e.indexableFromType(e.evaluateEnum(get(t, "enum")))
	case isIntersect(t):
		return e.indexableFromType(e.evaluateIntersect(get(t, "allOf")))
	case isLiteral(t):
		return []string{toStringValue(get(t, "const"))}
	case isTemplateLiteral(t):
		return e.indexableFromType(e.evaluateTemplateLiteral(get(t, "pattern")))
	case isUnion(t):
		result := []string{}
		for _, left := range asArray(get(t, "anyOf"), "reduce") {
			result = append(result, e.indexableFromType(left)...)
		}
		return result
	}
	return []string{}
}

// ---- engine/this ----

func expandThis(properties, t any) any {
	var from func(any) any
	from = func(t any) any {
		switch {
		case isArrayType(t):
			return tArray(from(get(t, "items")), nil)
		case isConstructor(t):
			return tConstructor(mapTypes(get(t, "parameters"), from), from(get(t, "instanceType")), nil)
		case isFunction(t):
			return tFunction(mapTypes(get(t, "parameters"), from), from(get(t, "returnType")), nil)
		case isTuple(t):
			return tTuple(mapTypes(get(t, "items"), from), nil)
		case isUnion(t):
			return tUnion(mapTypes(get(t, "anyOf"), from), nil)
		case isIntersect(t):
			return tIntersect(mapTypes(get(t, "allOf"), from), nil)
		case isThis(t):
			return tObject(properties, nil)
		}
		return t
	}
	return from(t)
}

// ---- engine/cyclic ----

func cyclicTarget(defs, ref any) any {
	key := propKey(ref)
	if hasKey(defs, key) {
		target := get(defs, key)
		if isRef(target) {
			return cyclicTarget(defs, get(target, "$ref"))
		}
		return target
	}
	return tNever(nil)
}

func isInterfaceDeferred(v any) bool {
	return isSchema(v) && hasKey(v, "action") && jsEqual(get(v, "action"), "Interface")
}

func cyclicCheck(stack []string, context, t any) bool {
	var fromType func(stack []string, t any) bool
	fromTypes := func(stack []string, types []any) bool {
		for _, left := range types {
			if fromType(stack, left) {
				return true
			}
		}
		return false
	}
	fromProperties := func(stack []string, properties any) bool {
		return fromTypes(stack, objectValues(properties))
	}
	fromType = func(stack []string, t any) bool {
		switch {
		case isRef(t):
			ref := propKey(get(t, "$ref"))
			if slices.Contains(stack, ref) {
				return true
			}
			return fromType(append(slices.Clone(stack), ref), get(context, ref))
		case isArrayType(t):
			return fromType(stack, get(t, "items"))
		case isConstructor(t):
			return fromTypes(stack, append(slices.Clone(asArray(get(t, "parameters"), "concat")), get(t, "instanceType")))
		case isFunction(t):
			return fromTypes(stack, append(slices.Clone(asArray(get(t, "parameters"), "concat")), get(t, "returnType")))
		case isInterfaceDeferred(t):
			return fromProperties(stack, get(get(t, "parameters"), "1"))
		case isIntersect(t):
			return fromTypes(stack, asArray(get(t, "allOf"), "length"))
		case isObjectType(t):
			return fromProperties(stack, get(t, "properties"))
		case isUnion(t):
			return fromTypes(stack, asArray(get(t, "anyOf"), "length"))
		case isTuple(t):
			return fromTypes(stack, asArray(get(t, "items"), "length"))
		case isRecord(t):
			return fromType(stack, recordValue(t))
		}
		return false
	}
	return fromType(stack, t)
}

func cyclicCandidates(context any) []string {
	result := []string{}
	for _, left := range ownKeys(context) {
		if cyclicCheck([]string{left}, context, get(context, left)) {
			result = append(result, left)
		}
	}
	return result
}

func cyclicDependencies(context any, key string, t any) []string {
	var fromType func(t any, result []string) []string
	fromTypes := func(types []any, result []string) []string {
		for _, left := range types {
			result = fromType(left, result)
		}
		return result
	}
	fromType = func(t any, result []string) []string {
		switch {
		case isRef(t):
			ref := propKey(get(t, "$ref"))
			if slices.Contains(result, ref) {
				return result
			}
			if hasKey(context, ref) {
				return fromType(get(context, ref), append(slices.Clone(result), ref))
			}
			unreachable()
		case isArrayType(t):
			return fromType(get(t, "items"), result)
		case isConstructor(t):
			return fromTypes(append(slices.Clone(asArray(get(t, "parameters"), "concat")), get(t, "instanceType")), result)
		case isFunction(t):
			return fromTypes(append(slices.Clone(asArray(get(t, "parameters"), "concat")), get(t, "returnType")), result)
		case isInterfaceDeferred(t):
			return fromTypes(objectValues(get(get(t, "parameters"), "1")), result)
		case isIntersect(t):
			return fromTypes(asArray(get(t, "allOf"), "reduce"), result)
		case isObjectType(t):
			return fromTypes(objectValues(get(t, "properties")), result)
		case isUnion(t):
			return fromTypes(asArray(get(t, "anyOf"), "reduce"), result)
		case isTuple(t):
			return fromTypes(asArray(get(t, "items"), "reduce"), result)
		case isRecord(t):
			return fromType(recordValue(t), result)
		}
		return result
	}
	return fromType(t, []string{key})
}

func (e *eng) cyclicInterface(context any, heritage, properties any) any {
	h := e.instantiateTypes(asObj(context), newState(), heritage)
	p := e.instantiateProperties(NewObj(), newState(), properties)
	return e.evaluateIntersect(append(slices.Clone(h), tObject(p, nil)))
}

func (e *eng) instantiateCyclic(context any, ref string, t any) any {
	dependencies := cyclicDependencies(context, ref, t)
	definitions := NewObj()
	for _, key := range ownKeys(context) {
		if !slices.Contains(dependencies, key) {
			continue
		}
		ty := get(context, key)
		if isInterfaceDeferred(ty) {
			ty = e.cyclicInterface(context, get(get(ty, "parameters"), "0"), get(get(ty, "parameters"), "1"))
		}
		definitions = with(definitions, key, ty)
	}
	return tCyclic(definitions, ref, Undef)
}

// cyclicExtends is CyclicExtends: the type a cyclic stands for on either side of Extends, its references open as any.
func (e *eng) cyclicExtends(t any) any {
	defs, ref := get(t, "$defs"), propKey(get(t, "$ref"))
	if hasKey(defs, ref) {
		return e.cyclicExtendsFrom(get(defs, ref))
	}
	return tUnknown(nil)
}

func (e *eng) cyclicExtendsFrom(t any) any {
	fromTypes := func(types any) []any { return mapTypes(types, e.cyclicExtendsFrom) }
	switch {
	case isRef(t):
		return tAny(nil)
	case isArrayType(t):
		return tArray(e.cyclicExtendsFrom(get(t, "items")), Discard(t, "~kind", "type", "items"))
	case isConstructor(t):
		return tConstructor(fromTypes(get(t, "parameters")), e.cyclicExtendsFrom(get(t, "instanceType")), nil)
	case isFunction(t):
		return tFunction(fromTypes(get(t, "parameters")), e.cyclicExtendsFrom(get(t, "returnType")), nil)
	case isIntersect(t):
		return tIntersect(fromTypes(get(t, "allOf")), nil)
	case isObjectType(t):
		properties := get(t, "properties")
		result := NewObj()
		for _, key := range ownKeys(properties) {
			result = with(result, key, e.cyclicExtendsFrom(get(properties, key)))
		}
		return tObject(result, nil)
	case isRecord(t):
		return e.recordAction(recordKey(t), e.cyclicExtendsFrom(recordValue(t)), NewObj())
	case isUnion(t):
		return tUnion(fromTypes(get(t, "anyOf")), nil)
	case isTuple(t):
		return tTuple(fromTypes(get(t, "items")), nil)
	}
	return t
}

// ---- engine/module ----

func (e *eng) moduleInstantiate(context *Obj, declarations, options any) any {
	candidates := cyclicCandidates(declarations)
	declarationContext := Assign(context, declarations)
	cyclics, nonCyclics := NewObj(), NewObj()
	for _, key := range ownKeys(declarations) {
		if slices.Contains(candidates, key) {
			cyclics = with(cyclics, key, e.instantiateCyclic(declarationContext, key, get(declarations, key)))
		}
	}
	for _, key := range ownKeys(declarations) {
		if !slices.Contains(candidates, key) {
			nonCyclics = with(nonCyclics, key, e.instantiateType(declarationContext, newState(), get(declarations, key)))
		}
	}
	return Update(spread(cyclics, nonCyclics), nil0(), options)
}

// ---- exclude / extract / non-nullable ----

func (e *eng) excludeOperation(left, right any) any {
	evaluated := e.evaluateType(left)
	canonical := []any{evaluated}
	if isUnion(evaluated) {
		canonical = asArray(get(evaluated, "anyOf"), "reduce")
	}
	remaining := []any{}
	for _, head := range canonical {
		if !isExtendsTrueLike(e.extends(NewObj(), head, right)) {
			remaining = append(remaining, head)
		}
	}
	return e.evaluateUnion(remaining)
}

func (e *eng) extractOperation(left, right any) any {
	evaluated := e.evaluateType(left)
	canonical := []any{evaluated}
	if isUnion(evaluated) {
		canonical = asArray(get(evaluated, "anyOf"), "reduce")
	}
	remaining := []any{}
	for _, head := range canonical {
		if isExtendsTrueLike(e.extends(NewObj(), head, right)) {
			remaining = append(remaining, head)
		}
	}
	return e.evaluateUnion(remaining)
}

func (e *eng) excludeAction(left, right, options any) any {
	if canInstantiate(left, right) {
		return Update(e.excludeOperation(left, right), nil0(), options)
	}
	return tDeferred("Exclude", []any{left, right}, options)
}

func (e *eng) excludeInstantiate(ctx *Obj, st state, left, right, options any) any {
	return e.excludeAction(e.instantiateType(ctx, st, left), e.instantiateType(ctx, st, right), options)
}

func (e *eng) extractAction(left, right, options any) any {
	if canInstantiate(left, right) {
		return Update(e.extractOperation(left, right), nil0(), options)
	}
	return tDeferred("Extract", []any{left, right}, options)
}

func (e *eng) extractInstantiate(ctx *Obj, st state, left, right, options any) any {
	return e.extractAction(e.instantiateType(ctx, st, left), e.instantiateType(ctx, st, right), options)
}

func (e *eng) nonNullableAction(t, options any) any {
	if canInstantiate(t) {
		excluded := tUnion([]any{tNull(nil), tUndefined(nil)}, nil)
		return Update(e.excludeAction(t, excluded, nil0()), nil0(), options)
	}
	return tDeferred("NonNullable", []any{t}, options)
}

func (e *eng) nonNullableInstantiate(ctx *Obj, st state, t, options any) any {
	return e.nonNullableAction(e.instantiateType(ctx, st, t), options)
}

// ---- constructor parameters / parameters / instance type / return type ----

func (e *eng) constructorParametersAction(t, options any) any {
	if canInstantiate(t) {
		var parameters any = []any{}
		if isConstructor(t) {
			parameters = get(t, "parameters")
		}
		return Update(tTuple(e.instantiateElements(NewObj(), newState(), parameters), nil), nil0(), options)
	}
	return tDeferred("ConstructorParameters", []any{t}, options)
}

func (e *eng) constructorParametersInstantiate(ctx *Obj, st state, t, options any) any {
	return e.constructorParametersAction(e.instantiateType(ctx, st, t), options)
}

func (e *eng) parametersAction(t, options any) any {
	if canInstantiate(t) {
		var parameters any = []any{}
		if isFunction(t) {
			parameters = get(t, "parameters")
		}
		return Update(tTuple(e.instantiateElements(NewObj(), newState(), parameters), nil), nil0(), options)
	}
	return tDeferred("Parameters", []any{t}, options)
}

func (e *eng) parametersInstantiate(ctx *Obj, st state, t, options any) any {
	return e.parametersAction(e.instantiateType(ctx, st, t), options)
}

func (e *eng) instanceTypeAction(t, options any) any {
	if canInstantiate(t) {
		var r any = tNever(nil)
		if isConstructor(t) {
			r = get(t, "instanceType")
		}
		return Update(r, nil0(), options)
	}
	return tDeferred("InstanceType", []any{t}, options)
}

func (e *eng) instanceTypeInstantiate(ctx *Obj, st state, t, options any) any {
	return e.instanceTypeAction(e.instantiateType(ctx, st, t), defaultOptions(options))
}

func (e *eng) returnTypeAction(t, options any) any {
	if canInstantiate(t) {
		var r any = tNever(nil)
		if isFunction(t) {
			r = get(t, "returnType")
		}
		return Update(r, nil0(), options)
	}
	return tDeferred("ReturnType", []any{t}, options)
}

func (e *eng) returnTypeInstantiate(ctx *Obj, st state, t, options any) any {
	return e.returnTypeAction(e.instantiateType(ctx, st, t), defaultOptions(options))
}

// defaultOptions is the `options = {}` default parameter: it replaces undefined only.
func defaultOptions(options any) any {
	if isUndefined(options) {
		return NewObj()
	}
	return options
}

// ---- interface ----

func (e *eng) interfaceAction(heritage, properties, options any) any {
	if canInstantiate(asArray(heritage, "length")...) {
		return Update(e.evaluateIntersect(append(slices.Clone(asArray(heritage, "concat")), tObject(properties, nil))), nil0(), options)
	}
	return tDeferred("Interface", []any{heritage, properties}, options)
}

func (e *eng) interfaceInstantiate(ctx *Obj, st state, heritage, properties, options any) any {
	h := e.instantiateTypes(ctx, st, heritage)
	p := e.instantiateProperties(ctx, st, properties)
	return e.interfaceAction(h, p, options)
}

// ---- intrinsics ----

func capitalizeMapping(input string) string {
	units := jsstring.ToUTF16(input)
	if len(units) == 0 {
		throw("TypeError", "Cannot read properties of undefined (reading 'toUpperCase')")
	}
	return caseFirst(units, jsstring.ToUpper)
}

func uncapitalizeMapping(input string) string {
	units := jsstring.ToUTF16(input)
	if len(units) == 0 {
		throw("TypeError", "Cannot read properties of undefined (reading 'toLowerCase')")
	}
	return caseFirst(units, jsstring.ToLower)
}

// caseFirst maps the first UTF-16 unit of units and joins the rest on as units, so a surrogate pair split by the
// first-unit read is whole again.
func caseFirst(units []uint16, mapping func(string) string) string {
	if units[0] >= 0xd800 && units[0] < 0xe000 {
		return jsstring.FromUTF16(units) // a lone surrogate half has no case
	}
	first := jsstring.ToUTF16(mapping(jsstring.FromUTF16(units[:1])))
	return jsstring.FromUTF16(append(first, units[1:]...))
}

func intrinsicMapping(name string) func(string) string {
	switch name {
	case "Capitalize":
		return capitalizeMapping
	case "Lowercase":
		return jsstring.ToLower
	case "Uncapitalize":
		return uncapitalizeMapping
	}
	return jsstring.ToUpper
}

func (e *eng) intrinsicFromType(mapping func(string) string, t any) any {
	switch {
	case isLiteral(t):
		value := get(t, "const")
		if s, ok := value.(string); ok {
			return tLiteral(mapping(s), nil)
		}
		return tLiteral(value, nil)
	case isTemplateLiteral(t):
		return e.intrinsicFromType(mapping, e.evaluateTemplateLiteral(get(t, "pattern")))
	case isUnion(t):
		return tUnion(mapTypes(get(t, "anyOf"), func(x any) any { return e.intrinsicFromType(mapping, x) }), nil)
	}
	return t
}

func (e *eng) intrinsicAction(name string, t, options any) any {
	if canInstantiate(t) {
		return Update(e.intrinsicFromType(intrinsicMapping(name), t), nil0(), options)
	}
	return tDeferred(name, []any{t}, options)
}

func (e *eng) intrinsicInstantiate(ctx *Obj, st state, name string, t, options any) any {
	return e.intrinsicAction(name, e.instantiateType(ctx, st, t), options)
}

// ---- conditional ----

func (e *eng) conditionalOperation(ctx *Obj, st state, left, right, trueT, falseT any) any {
	result := e.extends(ctx, left, right)
	switch {
	case isExtendsUnion(result):
		return tUnion([]any{e.instantiateType(inferredOf(result), st, trueT), e.instantiateType(ctx, st, falseT)}, nil)
	case isExtendsTrue(result):
		return e.instantiateType(inferredOf(result), st, trueT)
	}
	return e.instantiateType(ctx, st, falseT)
}

func inferredOf(result any) *Obj {
	inferred, _ := get(result, "inferred").(*Obj)
	return inferred
}

func (e *eng) conditionalAction(ctx *Obj, st state, left, right, trueT, falseT, options any) any {
	if canInstantiate(left, right) {
		return Update(e.conditionalOperation(ctx, st, left, right, trueT, falseT), nil0(), options)
	}
	return tDeferred("Conditional", []any{left, right, trueT, falseT}, options)
}

func (e *eng) conditionalInstantiate(ctx *Obj, st state, left, right, trueT, falseT, options any) any {
	return e.conditionalAction(ctx, st, e.instantiateType(ctx, st, left), e.instantiateType(ctx, st, right), trueT, falseT, options)
}

// ---- mapped ----

func mappedVariants(t any, e *eng) []any {
	switch {
	case isEnum(t):
		return mappedVariants(e.evaluateEnum(get(t, "enum")), e)
	case isLiteral(t):
		value := get(t, "const")
		if isNumber(value) {
			return []any{tLiteral(toStringValue(value), nil)}
		}
		return []any{tLiteral(value, nil)}
	case isTemplateLiteral(t):
		return mappedVariants(e.evaluateTemplateLiteral(get(t, "pattern")), e)
	case isUnion(t):
		result := []any{}
		for _, left := range asArray(get(t, "anyOf"), "reduce") {
			result = append(result, mappedVariants(left, e)...)
		}
		return result
	}
	return []any{t}
}

func (e *eng) mappedOperation(ctx *Obj, st state, identifier, t, as, property any) any {
	variants := mappedVariants(t, e)
	objects := []any{}
	for _, variant := range variants {
		variantContext := Assign(ctx, NewObj(propKey(get(identifier, "name")), variant))
		instantiatedAs := e.instantiateType(variantContext, st, as)
		canonicalAs := instantiatedAs
		if isTemplateLiteral(instantiatedAs) {
			canonicalAs = e.evaluateTemplateLiteral(get(instantiatedAs, "pattern"))
		}
		instantiatedProperty := e.instantiateType(variantContext, st, property)
		props := NewObj()
		if isLiteralNumber(canonicalAs) || isLiteralString(canonicalAs) {
			props.Set(propKey(get(canonicalAs, "const")), instantiatedProperty)
		}
		objects = append(objects, props)
	}
	for i, p := range objects {
		objects[i] = tObject(p, nil)
	}
	return e.evaluateIntersect(objects)
}

func (e *eng) mappedAction(ctx *Obj, st state, identifier, t, as, property, options any) any {
	if canInstantiate(t) {
		return Update(e.mappedOperation(ctx, st, identifier, t, as, property), nil0(), options)
	}
	return tDeferred("Mapped", []any{identifier, t, as, property}, options)
}

func (e *eng) mappedInstantiate(ctx *Obj, st state, identifier, t, as, property, options any) any {
	return e.mappedAction(ctx, st, identifier, e.instantiateType(ctx, st, t), as, property, options)
}

// ---- record ----

func (e *eng) recordAction(key, value, options any) any {
	if canInstantiate(key) {
		return Update(e.recordFromKey(key, value), nil0(), options)
	}
	return tDeferred("Record", []any{key, value}, options)
}

func (e *eng) recordInstantiate(ctx *Obj, st state, key, value, options any) any {
	return e.recordAction(e.instantiateType(ctx, st, key), e.instantiateType(ctx, st, value), options)
}

func (e *eng) recordFromKey(key, value any) any {
	switch {
	case isAny(key):
		return createRecord(stringKey, value)
	case isBooleanType(key):
		return tObject(NewObj("true", value, "false", value), nil)
	case isEnum(key):
		return e.recordFromKey(e.evaluateEnum(get(key, "enum")), value)
	case isInteger(key):
		return createRecord(integerKey, value)
	case isIntersect(key):
		return e.recordFromKey(e.evaluateIntersect(get(key, "allOf")), value)
	case isLiteral(key):
		k := get(key, "const")
		switch {
		case isString(k) || isNumber(k):
			return tObject(NewObj(propKey(k), value), nil)
		case jsEqual(k, false):
			return tObject(NewObj("false", value), nil)
		case jsEqual(k, true):
			return tObject(NewObj("true", value), nil)
		}
		return tObject(NewObj(), nil)
	case isNumberType(key):
		return createRecord(numberKey, value)
	case isUnion(key):
		flattened := flatten(asArray(get(key, "anyOf"), "reduce"))
		for _, t := range flattened {
			if isStringType(t) || isNumberType(t) || isInteger(t) {
				return createRecord(stringKey, value)
			}
		}
		properties := NewObj()
		for _, left := range flattened {
			if isLiteral(left) && (isString(get(left, "const")) || isNumber(get(left, "const"))) {
				properties = with(properties, propKey(get(left, "const")), value)
			}
		}
		return tObject(properties, nil)
	case isStringType(key):
		if hasKey(key, "pattern") && isString(get(key, "pattern")) {
			return createRecord(get(key, "pattern"), value)
		}
		return createRecord(stringKey, value)
	case isTemplateLiteral(key):
		pattern := get(key, "pattern")
		types := parsePatternIntoTypes(pattern)
		if isTemplateLiteralFinite(types) {
			return e.recordFromKey(e.evaluateTemplateLiteral(pattern), value)
		}
		return createRecord(pattern, value)
	}
	return tObject(NewObj(), nil)
}

// ---- engine/template_literal ----

func templateLiteralCreate(pattern any) *Obj {
	return Create(NewObj("~kind", "TemplateLiteral"), NewObj("type", "string", "pattern", pattern), NewObj())
}

func isTemplateLiteralDeferred(v any) bool {
	return isSchema(v) && hasKey(v, "action") && jsEqual(get(v, "action"), "TemplateLiteral")
}

func isTemplateLiteralFinite(types []any) bool {
	var fromType func(t any) bool
	fromTypes := func(types []any) bool {
		if len(types) == 0 {
			return false
		}
		for _, left := range types {
			if !fromType(left) {
				return false
			}
		}
		return true
	}
	fromType = func(t any) bool {
		switch {
		case isUnion(t):
			return fromTypes(asArray(get(t, "anyOf"), "length"))
		case isLiteral(t):
			return true
		}
		return false
	}
	return fromTypes(types)
}

func parsePatternIntoTypes(pattern any) []any {
	parsed := scriptPattern(toStringValue(pattern))
	if parsed.ok {
		return asArray(parsed.value, "length")
	}
	return []any{}
}

func templateLiteralDecodeUnsafe(pattern any) any {
	types := parsePatternIntoTypes(pattern)
	if len(types) == 0 {
		return tString(nil)
	}
	if isTemplateLiteralFinite(types) {
		return decodeTypes(types)
	}
	return templateLiteralCreate(pattern)
}

func templateLiteralDecode(pattern any) any {
	decoded := templateLiteralDecodeUnsafe(pattern)
	if isTemplateLiteral(decoded) {
		return tString(nil)
	}
	return decoded
}

func decodeTypes(types []any) any {
	if len(types) == 0 {
		unreachable()
	}
	if len(types) == 1 && isLiteral(types[0]) {
		return types[0]
	}
	variants := []string{}
	var fromType func(variants []string, t any) []string
	fromType = func(variants []string, t any) []string {
		switch {
		case isUnion(t):
			result := []string{}
			for _, left := range asArray(get(t, "anyOf"), "reduce") {
				result = append(result, fromType(variants, left)...)
			}
			return result
		case isLiteral(t):
			value := toStringValue(get(t, "const"))
			if len(variants) == 0 {
				return []string{value}
			}
			out := make([]string, len(variants))
			for i, left := range variants {
				out[i] = left + value
			}
			return out
		}
		unreachable()
		return nil
	}
	for _, t := range types {
		variants = fromType(variants, t)
	}
	literals := make([]any, len(variants))
	for i, v := range variants {
		literals[i] = tLiteral(v, nil)
	}
	return tUnion(literals, nil)
}

func (e *eng) templateLiteralEncode(types any) *Obj {
	var encodeTypes func(types []any, pattern string) string
	var encodeType func(t any, right []any, pattern string) string
	encodeType = func(t any, right []any, pattern string) string {
		switch {
		case isEnum(t):
			return encodeType(e.evaluateEnum(get(t, "enum")), right, pattern)
		case isInteger(t):
			return encodeTypes(right, pattern+integerPattern)
		case isLiteral(t):
			return encodeTypes(right, pattern+toStringValue(get(t, "const")))
		case isBigIntType(t):
			return encodeTypes(right, pattern+bigIntPattern)
		case isBooleanType(t):
			return encodeType(tUnion([]any{tLiteral("false", nil), tLiteral("true", nil)}, nil), right, pattern)
		case isNumberType(t):
			return encodeTypes(right, pattern+numberPattern)
		case isStringType(t):
			return encodeTypes(right, pattern+stringPattern)
		case isTemplateLiteral(t):
			p := toStringValue(get(t, "pattern"))
			n := jsstring.Length(p)
			return encodeTypes(right, pattern+jsstring.Slice(p, 1, n-1))
		case isTemplateLiteralDeferred(t):
			templateLiteral := e.templateLiteralAction(get(get(t, "parameters"), "0"), NewObj())
			return encodeType(templateLiteral, right, pattern)
		case isUnion(t):
			result := []string{}
			for _, head := range asArray(get(t, "anyOf"), "reduce") {
				result = append(result, encodeType(head, nil, ""))
			}
			var joined strings.Builder
			for i, r := range result {
				if i > 0 {
					joined.WriteString("|")
				}
				joined.WriteString(r)
			}
			return encodeTypes(right, pattern+"("+joined.String()+")")
		}
		return neverPattern
	}
	encodeTypes = func(types []any, pattern string) string {
		if len(types) == 0 {
			return pattern
		}
		return encodeType(types[0], types[1:], pattern)
	}
	return templateLiteralCreate("^" + encodeTypes(asArray(types, "length"), "") + "$")
}

func (e *eng) templateLiteralAction(types, options any) any {
	if canInstantiate(asArray(types, "length")...) {
		return Update(e.templateLiteralEncode(types), nil0(), options)
	}
	return tDeferred("TemplateLiteral", []any{types}, options)
}

func (e *eng) templateLiteralInstantiate(ctx *Obj, st state, types, options any) any {
	return e.templateLiteralAction(e.instantiateTypes(ctx, st, types), options)
}

// ---- engine/call ----

func (e *eng) callAssert() {
	if e.count < maxInstantiationCount {
		return
	}
	throwError("Type instantiation is excessively deep and possibly infinite")
}

func (e *eng) callIncrement() {
	e.callAssert()
	e.count++
	e.depth++
}

func (e *eng) callDecrement() {
	e.depth--
	if e.depth == 0 {
		e.count = 0
	}
}

func collectDistributionNames(expression any, result []string) []string {
	if isDeferred(expression) && jsEqual(get(expression, "action"), "Conditional") {
		params := get(expression, "parameters")
		if isRef(idx(params, 0)) {
			result = append(slices.Clone(result), propKey(get(idx(params, 0), "$ref")))
		}
		return collectDistributionNames(idx(params, 2), collectDistributionNames(idx(params, 3), result))
	}
	if isDeferred(expression) && jsEqual(get(expression, "action"), "Mapped") {
		p1 := idx(get(expression, "parameters"), 1)
		if isDeferred(p1) && jsEqual(get(p1, "action"), "KeyOf") && isRef(idx(get(p1, "parameters"), 0)) {
			return append(slices.Clone(result), propKey(get(idx(get(p1, "parameters"), 0), "$ref")))
		}
	}
	return result
}

func canonicalArgument(e *eng, t any) any {
	switch {
	case isTemplateLiteral(t):
		return e.evaluateTemplateLiteral(get(t, "pattern"))
	case isEnum(t):
		return e.evaluateEnum(get(t, "enum"))
	}
	return t
}

func (e *eng) distributeArguments(parameters []any, arguments []any, expression any) [][]any {
	names := collectDistributionNames(expression, nil)
	distribution := make([]bool, len(parameters))
	for i, p := range parameters {
		distribution[i] = slices.Contains(names, propKey(get(p, "name")))
	}
	if !isDeferred(expression) || (!jsEqual(get(expression, "action"), "Conditional") && !jsEqual(get(expression, "action"), "Mapped")) {
		return [][]any{arguments}
	}
	result := [][]any{{}}
	for i := 0; i < len(arguments) && i < len(distribution); i++ {
		variants := []any{arguments[i]}
		if distribution[i] {
			canonical := canonicalArgument(e, arguments[i])
			if isUnion(canonical) {
				variants = slices.Clone(asArray(get(canonical, "anyOf"), "length"))
			} else {
				variants = []any{canonical}
			}
		}
		next := [][]any{}
		for _, left := range variants {
			for _, current := range result {
				next = append(next, append(slices.Clone(current), left))
			}
		}
		result = next
	}
	return result
}

func (e *eng) bindArgument(ctx *Obj, st state, name string, extendsT, t any) *Obj {
	instantiated := e.instantiateType(ctx, st, t)
	if !isInfer(instantiated) && !isCall(instantiated) && !isExtendsTrueLike(e.extends(NewObj(), instantiated, extendsT)) {
		throwError("Argument for parameter " + name + " does not satisfy constraint")
	}
	return Assign(ctx, NewObj(name, instantiated))
}

func (e *eng) resolveArgumentsContext(ctx *Obj, st state, parameters, arguments []any) *Obj {
	for _, p := range parameters {
		extendsT := e.instantiateType(ctx, st, get(p, "extends"))
		equalsT := e.instantiateType(ctx, st, get(p, "equals"))
		name := propKey(get(p, "name"))
		if len(arguments) > 0 {
			ctx = e.bindArgument(ctx, st, name, extendsT, arguments[0])
			arguments = arguments[1:]
		} else {
			ctx = e.bindArgument(ctx, st, name, extendsT, equalsT)
		}
	}
	return ctx
}

func (e *eng) callDispatch(ctx *Obj, st state, target any, parameters []any, expression any, arguments []any) any {
	e.callIncrement()
	defer e.callDecrement()
	argumentsContext := e.resolveArgumentsContext(ctx, st, parameters, arguments)
	next := state{callstack: append(slices.Clone(st.callstack), propKey(get(target, "$ref"))), visited: st.visited}
	returnType := e.instantiateType(argumentsContext, next, expression)
	return e.instantiateType(argumentsContext, newState(), returnType)
}

func (e *eng) callInstantiate(ctx *Obj, st state, target any, arguments any) any {
	instantiatedArguments := e.instantiateTypes(ctx, st, arguments)
	name, t := "(not-generic)", any(tNever(nil))
	// ResolveTarget
	if isGeneric(target) {
		name, t = "(anonymous)", tGeneric(get(target, "parameters"), get(target, "expression"))
	} else if isRef(target) {
		ref := propKey(get(target, "$ref"))
		if hasKey(ctx, ref) {
			c := get(ctx, ref)
			switch {
			case isGeneric(c):
				name, t = ref, tGeneric(get(c, "parameters"), get(c, "expression"))
			case isRef(c):
				// FromRef: a ref to a ref resolves again, under the name of the final target.
				name, t = e.resolveRef(ctx, get(c, "$ref"))
			}
		} else {
			name, t = "(not-resolvable)", tNever(nil)
		}
	}
	if !isGeneric(t) {
		return tCallConstruct(target, instantiatedArguments)
	}
	if len(st.callstack) > 0 && st.callstack[len(st.callstack)-1] == name {
		return tCallConstruct(tRef(name, nil), instantiatedArguments)
	}
	parameters := asArray(get(t, "parameters"), "reduce")
	expression := get(t, "expression")
	distributed := e.distributeArguments(parameters, instantiatedArguments, expression)
	returnTypes := make([]any, 0, len(distributed))
	for _, arguments := range distributed {
		returnTypes = append(returnTypes, e.callDispatch(ctx, st, tRef(name, nil), parameters, expression, arguments))
	}
	if len(returnTypes) == 1 {
		return returnTypes[0]
	}
	return e.evaluateUnion(returnTypes)
}

// resolveRef is ResolveTarget's FromRef for a reference met on the way: the name and generic it leads to.
func (e *eng) resolveRef(ctx *Obj, refValue any) (string, any) {
	ref := propKey(refValue)
	if !hasKey(ctx, ref) {
		return "(not-resolvable)", tNever(nil)
	}
	c := get(ctx, ref)
	switch {
	case isGeneric(c):
		return ref, tGeneric(get(c, "parameters"), get(c, "expression"))
	case isRef(c):
		return e.resolveRef(ctx, get(c, "$ref"))
	}
	return "(not-generic)", tNever(nil)
}
