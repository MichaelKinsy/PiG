package engine

import "slices"

// ---- extends/result ----

func extendsUnionResult(inferred any) *Obj {
	return kindOf("ExtendsUnion", NewObj("inferred", inferred), nil)
}
func extendsTrue(inferred any) *Obj { return kindOf("ExtendsTrue", NewObj("inferred", inferred), nil) }
func extendsFalse() *Obj            { return kindOf("ExtendsFalse", NewObj(), nil) }

func isExtendsResult(v any, kind string, needInferred bool) bool {
	if !isObject(v) || !hasKey(v, "~kind") {
		return false
	}
	if needInferred && !hasKey(v, "inferred") {
		return false
	}
	if !jsEqual(get(v, "~kind"), kind) {
		return false
	}
	return !needInferred || isObject(get(v, "inferred"))
}

func isExtendsUnion(v any) bool { return isExtendsResult(v, "ExtendsUnion", true) }
func isExtendsTrue(v any) bool  { return isExtendsResult(v, "ExtendsTrue", true) }
func isExtendsFalse(v any) bool {
	return isObject(v) && hasKey(v, "~kind") && jsEqual(get(v, "~kind"), "ExtendsFalse")
}
func isExtendsTrueLike(v any) bool { return isExtendsUnion(v) || isExtendsTrue(v) }

// match is Result.Match: the inferred members of a true-like result.
func match(result any) (any, bool) {
	if isExtendsTrueLike(result) {
		return get(result, "inferred"), true
	}
	return nil, false
}

// ---- extends/extends ----

func (e *eng) extendsCanonical(t any) any {
	switch {
	case isCyclic(t):
		return e.cyclicExtends(t)
	case isUnsafe(t):
		return tUnknown(nil)
	}
	return t
}

// extends is Extends(inferred, left, right).
func (e *eng) extends(inferred any, left, right any) any {
	l := e.extendsCanonical(left)
	r := e.extendsCanonical(right)
	return e.extendsLeft(inferred, l, r)
}

func (e *eng) extendsLeft(inferred, left, right any) any {
	switch {
	case isAny(left):
		return e.extendsAny(inferred, left, right)
	case isArrayType(left):
		return e.extendsArray(inferred, left, get(left, "items"), right)
	case isBigIntType(left):
		return e.extendsSimple(inferred, left, right, isBigIntType)
	case isBooleanType(left):
		return e.extendsSimple(inferred, left, right, isBooleanType)
	case isConstructor(left):
		return e.extendsConstructor(inferred, get(left, "parameters"), get(left, "instanceType"), right)
	case isDependent(left):
		return e.extendsDependent(inferred, get(left, "if"), get(left, "then"), get(left, "else"), right)
	case isEnum(left):
		return e.extendsLeft(inferred, e.evaluateEnum(get(left, "enum")), right)
	case isFunction(left):
		return e.extendsFunction(inferred, get(left, "parameters"), get(left, "returnType"), right)
	case isInteger(left):
		if isInteger(right) || isNumberType(right) {
			return extendsTrue(inferred)
		}
		return e.extendsRight(inferred, left, right)
	case isIntersect(left):
		return e.extendsLeft(inferred, e.evaluateIntersect(get(left, "allOf")), right)
	case isLiteral(left):
		return e.extendsLiteral(inferred, left, right)
	case isNever(left):
		if isInfer(right) {
			return e.extendsRight(inferred, left, right)
		}
		return extendsTrue(inferred)
	case isNull(left):
		return e.extendsSimple(inferred, left, right, isNull)
	case isNumberType(left):
		return e.extendsSimple(inferred, left, right, isNumberType)
	case isObjectType(left):
		return e.extendsObject(inferred, get(left, "properties"), right)
	case isRecord(left):
		return e.extendsRecord(inferred, recordPattern(left), recordValue(left), right)
	case isStringType(left):
		return e.extendsSimple(inferred, left, right, isStringType)
	case isSymbol(left):
		return e.extendsSimple(inferred, left, right, isSymbol)
	case isTemplateLiteral(left):
		return e.extendsLeft(inferred, e.evaluateTemplateLiteral(get(left, "pattern")), right)
	case isTuple(left):
		return e.extendsTuple(inferred, get(left, "items"), right)
	case isUndefinedType(left):
		if isVoid(right) || isUndefinedType(right) {
			return extendsTrue(inferred)
		}
		return e.extendsRight(inferred, left, right)
	case isUnion(left):
		return e.extendsUnion(inferred, get(left, "anyOf"), right)
	case isUnknown(left):
		return e.extendsUnknown(inferred, left, right)
	case isVoid(left):
		return e.extendsSimple(inferred, left, right, isVoid)
	}
	return extendsFalse()
}

// extendsSimple is the shape of the primitive cases: true when right is the same primitive, else ExtendsRight.
func (e *eng) extendsSimple(inferred, left, right any, same func(any) bool) any {
	if same(right) {
		return extendsTrue(inferred)
	}
	return e.extendsRight(inferred, left, right)
}

func (e *eng) extendsAny(inferred, left, right any) any {
	switch {
	case isInfer(right):
		return e.extendsRight(inferred, left, right)
	case isAny(right), isUnknown(right):
		return extendsTrue(inferred)
	}
	return extendsUnionResult(inferred)
}

func (e *eng) extendsUnknown(inferred, left, right any) any {
	switch {
	case isInfer(right):
		return e.extendsRight(inferred, left, right)
	case isAny(right), isUnknown(right):
		return extendsTrue(inferred)
	}
	return extendsFalse()
}

func (e *eng) extendsArray(inferred, arrayLeft, left, right any) any {
	if isArrayType(right) {
		il, ir := isImmutable(arrayLeft), isImmutable(right)
		if !il || ir {
			return e.extendsLeft(inferred, left, get(right, "items"))
		}
		return extendsFalse()
	}
	return e.extendsRight(inferred, arrayLeft, right)
}

func (e *eng) extendsDependent(inferred, ifT, thenT, elseT, right any) any {
	if _, ok := match(e.extendsLeft(inferred, ifT, right)); ok {
		return e.extendsLeft(inferred, thenT, right)
	}
	return e.extendsLeft(inferred, elseT, right)
}

func (e *eng) extendsFunction(inferred, parameters, returnType, right any) any {
	switch {
	case isAny(right), isUnknown(right):
		return extendsTrue(inferred)
	case isFunction(right):
		if inf, ok := match(e.extendsParameters(inferred, asArray(parameters, "length"), asArray(get(right, "parameters"), "length"))); ok {
			return e.extendsReturnType(inf, returnType, get(right, "returnType"))
		}
	}
	return extendsFalse()
}

func (e *eng) extendsConstructor(inferred, parameters, returnType, right any) any {
	switch {
	case isAny(right), isUnknown(right):
		return extendsTrue(inferred)
	case isConstructor(right):
		if inf, ok := match(e.extendsParameters(inferred, asArray(parameters, "length"), asArray(get(right, "parameters"), "length"))); ok {
			return e.extendsReturnType(inf, returnType, get(right, "instanceType"))
		}
	}
	return extendsFalse()
}

func (e *eng) extendsReturnType(inferred, left, right any) any {
	if isVoid(right) {
		return extendsTrue(inferred)
	}
	return e.extendsLeft(inferred, left, right)
}

func (e *eng) extendsParameters(inferred any, left, right []any) any {
	for len(left) > 0 {
		head, leftRest := left[0], left[1:]
		if len(right) == 0 {
			if isOptional(head) {
				return extendsTrue(inferred)
			}
			return extendsFalse()
		}
		rhead, rightRest := right[0], right[1:]
		checkLeft, checkRight := rhead, head
		if isInfer(rhead) {
			checkLeft, checkRight = head, rhead
		}
		isLeftOptional, isRightOptional := isOptional(head), isOptional(rhead)
		if !isLeftOptional && isRightOptional {
			return extendsFalse()
		}
		inf, ok := match(e.extendsLeft(inferred, checkLeft, checkRight))
		if !ok {
			return extendsFalse()
		}
		inferred, left, right = inf, leftRest, rightRest
	}
	return extendsTrue(inferred)
}

func (e *eng) extendsRight(inferred, left, right any) any {
	switch {
	case isAny(right):
		return extendsTrue(inferred)
	case isDependent(right):
		ifT, thenT, elseT := get(right, "if"), get(right, "then"), get(right, "else")
		if inf, ok := match(e.extendsLeft(inferred, left, ifT)); ok {
			if inf2, ok := match(e.extendsLeft(inf, left, thenT)); ok {
				return extendsTrue(inf2)
			}
			return extendsFalse()
		}
		if inf, ok := match(e.extendsLeft(inferred, left, elseT)); ok {
			return extendsTrue(inf)
		}
		return extendsFalse()
	case isEnum(right):
		return e.extendsLeft(inferred, left, e.evaluateEnum(get(right, "enum")))
	case isInfer(right):
		if check, ok := match(e.extendsLeft(inferred, left, get(right, "extends"))); ok {
			return extendsTrue(Assign(Assign(inferred, check), NewObj(propKey(get(right, "name")), left)))
		}
		return extendsFalse()
	case isIntersect(right):
		for _, head := range asArray(get(right, "allOf"), "length") {
			inf, ok := match(e.extendsLeft(inferred, left, head))
			if !ok {
				return extendsFalse()
			}
			inferred = inf
		}
		return extendsTrue(inferred)
	case isTemplateLiteral(right):
		return e.extendsLeft(inferred, left, e.evaluateTemplateLiteral(get(right, "pattern")))
	case isUnion(right):
		for _, head := range asArray(get(right, "anyOf"), "length") {
			if inf, ok := match(e.extendsLeft(inferred, left, head)); ok {
				return extendsTrue(inf)
			}
		}
		return extendsFalse()
	case isUnknown(right):
		return extendsTrue(inferred)
	}
	return extendsFalse()
}

func (e *eng) extendsLiteral(inferred, left, right any) any {
	value := get(left, "const")
	var sameKind func(any) bool
	switch {
	case isBigInt(value):
		sameKind = isBigIntType
	case isBool(value):
		sameKind = isBooleanType
	case isNumber(value):
		sameKind = isNumberType
	case isString(value):
		sameKind = isStringType
	default:
		unreachable()
	}
	switch {
	case isLiteral(right):
		if jsEqual(value, get(right, "const")) {
			return extendsTrue(inferred)
		}
		return extendsFalse()
	case sameKind(right):
		return extendsTrue(inferred)
	}
	return e.extendsRight(inferred, tLiteral(value, nil), right)
}

// ---- inference ----

func inferrable(name, t any) *Obj { return kindOf("Inferrable", NewObj("name", name, "type", t), nil) }

func isInferable(v any) bool {
	return isObject(v) && hasKey(v, "~kind") && hasKey(v, "name") && hasKey(v, "type") && jsEqual(get(v, "~kind"), "Inferrable") && isString(get(v, "name")) && isObject(get(v, "type"))
}

func tryRestInferable(t any) any {
	if !isRest(t) {
		return Undef
	}
	items := get(t, "items")
	if !isInfer(items) {
		unreachable()
	}
	ext := get(items, "extends")
	switch {
	case isArrayType(ext):
		return inferrable(get(items, "name"), get(ext, "items"))
	case isUnknown(ext):
		return inferrable(get(items, "name"), ext)
	}
	return Undef
}

func tryInferable(t any) any {
	if isInfer(t) {
		return inferrable(get(t, "name"), get(t, "extends"))
	}
	return Undef
}

func (e *eng) tryInferResults(rest []any, right any) ([]any, bool) {
	result := []any{}
	for _, head := range rest {
		if _, ok := match(e.extendsLeft(NewObj(), head, right)); !ok {
			return nil, false
		}
		result = append(result, head)
	}
	return result, true
}

func (e *eng) inferResult(inferred any, name any, left []any, right any, build func([]any) any) any {
	if results, ok := e.tryInferResults(left, right); ok {
		return extendsTrue(Assign(inferred, NewObj(propKey(name), build(results))))
	}
	return extendsFalse()
}

func (e *eng) inferTupleResult(inferred, name any, left []any, right any) any {
	return e.inferResult(inferred, name, left, right, func(r []any) any { return tTuple(r, nil) })
}

func (e *eng) inferUnionResult(inferred, name any, left []any, right any) any {
	return e.inferResult(inferred, name, left, right, func(r []any) any { return tUnion(r, nil) })
}

// ---- tuple ----

func (e *eng) extendsTuple(inferred, left, right any) any {
	instantiatedLeft := e.instantiateElements(asObj(inferred), newState(), left)
	switch {
	case isTuple(right):
		return e.extendsTupleToTuple(inferred, instantiatedLeft, asArray(get(right, "items"), "length"))
	case isArrayType(right):
		return e.extendsTupleToArray(inferred, instantiatedLeft, get(right, "items"))
	}
	return e.extendsRight(inferred, tTuple(instantiatedLeft, nil), right)
}

func asObj(v any) *Obj {
	o, _ := v.(*Obj)
	return o
}

func applyReverse(types []any, reversed bool) []any {
	if reversed {
		out := slices.Clone(types)
		slices.Reverse(out)
		return out
	}
	return types
}

func (e *eng) extendsTupleToTuple(inferred any, left, right []any) any {
	instantiatedRight := e.instantiateElements(asObj(inferred), newState(), right)
	var first any = Undef
	if len(instantiatedRight) > 0 {
		first = instantiatedRight[0]
	}
	var inf any = Undef
	if isSchema(first) {
		inf = tryRestInferable(first)
	}
	reversed := isSchema(inf)
	return e.tupleElements(inferred, reversed, applyReverse(left, reversed), applyReverse(instantiatedRight, reversed))
}

func (e *eng) tupleElements(inferred any, reversed bool, leftRest, rightRest []any) any {
	if len(rightRest) == 0 {
		if len(leftRest) == 0 {
			return extendsTrue(inferred)
		}
		return extendsFalse()
	}
	right, tail := rightRest[0], rightRest[1:]
	infable := tryRestInferable(right)
	if isInferable(infable) {
		return e.inferTupleResult(inferred, get(infable, "name"), applyReverse(leftRest, reversed), get(infable, "type"))
	}
	if len(leftRest) == 0 {
		return extendsFalse()
	}
	head, leftTail := leftRest[0], leftRest[1:]
	if check, ok := match(e.extendsLeft(inferred, head, right)); ok {
		return e.tupleElements(check, reversed, leftTail, tail)
	}
	return extendsFalse()
}

func (e *eng) extendsTupleToArray(inferred any, left []any, right any) any {
	infable := tryInferable(right)
	if isInferable(infable) {
		return e.inferUnionResult(inferred, get(infable, "name"), left, get(infable, "type"))
	}
	for _, head := range left {
		inf, ok := match(e.extendsLeft(inferred, head, right))
		if !ok {
			return extendsFalse()
		}
		inferred = inf
	}
	return extendsTrue(inferred)
}

// ---- union ----

func (e *eng) extendsUnion(inferred, left, right any) any {
	infable := tryInferable(right)
	if isInferable(infable) {
		return e.inferUnionResult(inferred, get(infable, "name"), asArray(left, "length"), get(infable, "type"))
	}
	var rights []any
	if isUnion(right) {
		rights = asArray(get(right, "anyOf"), "length")
	} else {
		rights = []any{right}
	}
	for _, head := range asArray(left, "length") {
		found := false
		for _, r := range rights {
			if inf, ok := match(e.extendsLeft(inferred, head, r)); ok {
				inferred, found = inf, true
				break
			}
		}
		if !found {
			return extendsFalse()
		}
	}
	return extendsTrue(inferred)
}

// ---- object / record ----

func (e *eng) extendsObject(inferred, left, right any) any {
	switch {
	case isRecord(right):
		return e.extendsObjectToRecord(inferred, left, recordValue(right))
	case isObjectType(right):
		return e.extendsProperties(inferred, left, get(right, "properties"))
	}
	return e.extendsRight(inferred, tObject(left, nil), right)
}

func (e *eng) extendsProperty(inferred, left, right any) any {
	if isInfer(right) && isNever(get(right, "extends")) {
		return extendsFalse()
	}
	if inf, ok := match(e.extendsLeft(inferred, left, right)); ok {
		if isOptional(left) {
			if isOptional(right) {
				return extendsTrue(inf)
			}
			return extendsFalse()
		}
		return extendsTrue(inf)
	}
	return extendsFalse()
}

func (e *eng) extendsProperties(inferred, left, right any) any {
	properties := NewObj()
	for _, rightKey := range ownKeys(right) {
		var r any
		switch {
		case hasKey(left, rightKey):
			r = e.extendsProperty(NewObj(), get(left, rightKey), get(right, rightKey))
		case isOptional(get(right, rightKey)):
			if isInfer(get(right, rightKey)) {
				r = extendsTrue(Assign(inferred, NewObj(propKey(get(get(right, rightKey), "name")), get(get(right, rightKey), "extends"))))
			} else {
				r = extendsTrue(inferred)
			}
		default:
			r = extendsFalse()
		}
		properties.Set(rightKey, r)
	}
	checked := true
	for _, r := range objectValues(properties) {
		if !isExtendsTrueLike(r) {
			checked = false
		}
	}
	if !checked {
		return extendsFalse()
	}
	extracted := NewObj()
	for _, key := range ownKeys(properties) {
		if !hasKey(properties, key) {
			unreachable()
		}
		r := get(properties, key)
		if !isExtendsTrueLike(r) {
			unreachable()
		}
		extracted = spread(extracted, get(r, "inferred"))
	}
	return extendsTrue(Assign(inferred, extracted))
}

func recordMergeInferred(left, right any) any {
	result := left
	for _, key := range ownKeys(right) {
		var v any
		if hasKey(left, key) {
			if isUnion(get(result, key)) {
				v = tUnion(append(slices.Clone(asArray(get(get(result, key), "anyOf"), "length")), get(right, key)), nil)
			} else {
				v = tUnion([]any{get(left, key), get(right, key)}, nil)
			}
		} else {
			v = get(right, key)
		}
		result = with(result, key, v)
	}
	return result
}

func (e *eng) extendsObjectToRecord(inferred, properties, value any) any {
	result := inferred
	for _, left := range ownKeys(properties) {
		inf, ok := match(e.extendsLeft(NewObj(), get(properties, left), value))
		if !ok {
			return extendsFalse()
		}
		result = recordMergeInferred(result, inf)
	}
	return extendsTrue(result)
}

func (e *eng) extendsRecord(inferred, leftPattern, leftValue, right any) any {
	if isRecord(right) {
		// FromRecord is called with both keys as types, which are evaluated whether or not they are used.
		recordPatternToType(leftPattern)
		recordPatternToType(recordPattern(right))
	}
	switch {
	case isRecord(right):
		return e.extendsLeft(inferred, leftValue, recordValue(right))
	case isObjectType(right):
		if len(ownKeys(get(right, "properties"))) == 0 {
			return extendsTrue(inferred)
		}
		return extendsFalse()
	case isAny(right), isUnknown(right):
		return extendsTrue(inferred)
	}
	return extendsFalse()
}
