package engine

import (
	"math/big"
)

// Patterns TypeBox derives a record key's type from (build/type/types/{integer,number,string,bigint,never}.mjs).
const (
	bigIntPattern  = "-?(?:0|[1-9][0-9]*)n"
	integerPattern = "-?(?:0|[1-9][0-9]*)"
	numberPattern  = "-?(?:0|[1-9][0-9]*)(?:\\.[0-9]+)?"
	stringPattern  = ".*"
	neverPattern   = "(?!)"
	integerKey     = "^" + integerPattern + "$"
	numberKey      = "^" + numberPattern + "$"
	stringKey      = "^" + stringPattern + "$"
)

// ---- guards (build/type/types/*.mjs) ----

// isKind is IsKind: an object whose `~kind` is kind.
func isKind(value any, kind string) bool {
	return isObject(value) && hasKey(value, "~kind") && jsEqual(get(value, "~kind"), kind)
}

func isSchema(value any) bool { return isObject(value) }

func isAny(v any) bool             { return isKind(v, "Any") }
func isArrayType(v any) bool       { return isKind(v, "Array") }
func isBigIntType(v any) bool      { return isKind(v, "BigInt") }
func isBooleanType(v any) bool     { return isKind(v, "Boolean") }
func isCall(v any) bool            { return isKind(v, "Call") }
func isConstructor(v any) bool     { return isKind(v, "Constructor") }
func isCyclic(v any) bool          { return isKind(v, "Cyclic") }
func isDeferred(v any) bool        { return isKind(v, "Deferred") }
func isDependent(v any) bool       { return isKind(v, "Dependent") }
func isEnum(v any) bool            { return isKind(v, "Enum") }
func isFunction(v any) bool        { return isKind(v, "Function") }
func isGeneric(v any) bool         { return isKind(v, "Generic") }
func isInfer(v any) bool           { return isKind(v, "Infer") }
func isInteger(v any) bool         { return isKind(v, "Integer") }
func isIntersect(v any) bool       { return isKind(v, "Intersect") }
func isLiteral(v any) bool         { return isKind(v, "Literal") }
func isNever(v any) bool           { return isKind(v, "Never") }
func isNull(v any) bool            { return isKind(v, "Null") }
func isNumberType(v any) bool      { return isKind(v, "Number") }
func isObjectType(v any) bool      { return isKind(v, "Object") }
func isRecord(v any) bool          { return isKind(v, "Record") }
func isRef(v any) bool             { return isKind(v, "Ref") }
func isRest(v any) bool            { return isKind(v, "Rest") }
func isStringType(v any) bool      { return isKind(v, "String") }
func isSymbol(v any) bool          { return isKind(v, "Symbol") }
func isTemplateLiteral(v any) bool { return isKind(v, "TemplateLiteral") }
func isThis(v any) bool            { return isKind(v, "This") }
func isTuple(v any) bool           { return isKind(v, "Tuple") }
func isUndefinedType(v any) bool   { return isKind(v, "Undefined") }
func isUnion(v any) bool           { return isKind(v, "Union") }
func isUnknown(v any) bool         { return isKind(v, "Unknown") }
func isVoid(v any) bool            { return isKind(v, "Void") }

func isOptional(v any) bool  { return isSchema(v) && hasKey(v, "~optional") }
func isReadonly(v any) bool  { return isSchema(v) && hasKey(v, "~readonly") }
func isImmutable(v any) bool { return isSchema(v) && hasKey(v, "~immutable") }

// isUnsafe is IsUnsafe: an object (not an array) whose `~unsafe` is null.
func isUnsafe(v any) bool {
	return isObjectNotArray(v) && hasKey(v, "~unsafe") && get(v, "~unsafe") == nil
}

// isLiteralValue is IsLiteralValue.
func isLiteralValue(v any) bool { return isBigInt(v) || isBool(v) || isNumber(v) || isString(v) }

func isLiteralNumber(v any) bool { return isLiteral(v) && isNumber(get(v, "const")) }
func isLiteralString(v any) bool { return isLiteral(v) && isString(get(v, "const")) }

// ---- constructors ----

func tAny(options any) *Obj       { return kindOf("Any", NewObj(), options) }
func tUnknown(options any) *Obj   { return kindOf("Unknown", NewObj(), options) }
func tString(options any) *Obj    { return kindOf("String", NewObj("type", "string"), options) }
func tNumber(options any) *Obj    { return kindOf("Number", NewObj("type", "number"), options) }
func tInteger(options any) *Obj   { return kindOf("Integer", NewObj("type", "integer"), options) }
func tBigInt(options any) *Obj    { return kindOf("BigInt", NewObj("type", "bigint"), options) }
func tBoolean(options any) *Obj   { return kindOf("Boolean", NewObj("type", "boolean"), options) }
func tNull(options any) *Obj      { return kindOf("Null", NewObj("type", "null"), options) }
func tSymbol(options any) *Obj    { return kindOf("Symbol", NewObj("type", "symbol"), options) }
func tUndefined(options any) *Obj { return kindOf("Undefined", NewObj("type", "undefined"), options) }
func tVoid(options any) *Obj      { return kindOf("Void", NewObj("type", "void"), options) }
func tNever(options any) *Obj     { return kindOf("Never", NewObj("not", NewObj()), options) }
func tRef(ref any, options any) *Obj {
	return kindOf("Ref", NewObj("$ref", ref), options)
}
func tThis(options any) *Obj { return kindOf("This", NewObj("$ref", "#"), options) }
func tArray(items, options any) *Obj {
	return kindOf("Array", NewObj("type", "array", "items", items), options)
}
func tUnion(anyOf any, options any) *Obj { return kindOf("Union", NewObj("anyOf", anyOf), options) }
func tIntersect(types any, options any) *Obj {
	return kindOf("Intersect", NewObj("allOf", types), options)
}
func tRest(items any) *Obj {
	return kindOf("Rest", NewObj("type", "rest", "items", items), NewObj())
}
func tTuple(types any, options any) *Obj {
	return kindOf("Tuple", NewObj("type", "array", "additionalItems", false, "items", types, "minItems", get(types, "length")), options)
}
func tFunction(parameters, returnType, options any) *Obj {
	return kindOf("Function", NewObj("type", "function", "parameters", parameters, "returnType", returnType), options)
}
func tConstructor(parameters, instanceType, options any) *Obj {
	return kindOf("Constructor", NewObj("type", "constructor", "parameters", parameters, "instanceType", instanceType), options)
}
func tDependent(ifT, thenT, elseT, options any) *Obj {
	return kindOf("Dependent", NewObj("if", ifT, "then", thenT, "else", elseT), options)
}
func tGeneric(parameters, expression any) *Obj {
	return kindOf("Generic", NewObj("type", "generic", "parameters", parameters, "expression", expression), nil)
}
func tIdentifier(name any) *Obj { return kindOf("Identifier", NewObj("name", name), nil) }
func tInfer(name, extends any) *Obj {
	return kindOf("Infer", NewObj("type", "infer", "name", name, "extends", extends), NewObj())
}
func tParameter(name, extends, equals any) *Obj {
	return kindOf("Parameter", NewObj("name", name, "extends", extends, "equals", equals), NewObj())
}
func tDeferred(action string, parameters any, options any) *Obj {
	return kindOf("Deferred", NewObj("type", "deferred", "action", action, "parameters", parameters, "options", options), NewObj())
}
func tCallConstruct(target, arguments any) *Obj {
	return kindOf("Call", NewObj("type", "call", "target", target, "arguments", arguments), NewObj())
}

// tObject is Object(properties, options): `required` lists the properties that are not optional.
func tObject(properties any, options any) *Obj {
	required := requiredArray(properties)
	enumerable := NewObj("type", "object")
	if len(required) > 0 {
		enumerable.Set("required", required)
	}
	enumerable.Set("properties", properties)
	return kindOf("Object", enumerable, options)
}

func requiredArray(properties any) []any {
	out := []any{}
	for _, k := range ownKeys(properties) {
		if !isOptional(get(properties, k)) {
			out = append(out, k)
		}
	}
	return out
}

// tLiteral is Literal(value, options).
func tLiteral(value any, options any) *Obj {
	var name string
	switch {
	case isBigInt(value):
		name = "bigint"
	case isBool(value):
		name = "boolean"
	case isNumber(value):
		name = "number"
	case isString(value):
		name = "string"
	default:
		throwError("Invalid Literal value")
	}
	return kindOf("Literal", NewObj("type", name, "const", value), options)
}

// tCyclic is Cyclic($defs, $ref, options): each definition gets its own `$id`.
func tCyclic(defs any, ref any, options any) *Obj {
	result := NewObj()
	for _, key := range ownKeys(defs) {
		result = with(result, key, Update(get(defs, key), nil0(), NewObj("$id", key)))
	}
	return kindOf("Cyclic", NewObj("$defs", result, "$ref", ref), options)
}

// createRecord is CreateRecord(key, value).
func createRecord(key any, value any) *Obj {
	return Create(NewObj("~kind", "Record"), NewObj("type", "object", "patternProperties", NewObj(propKey(key), value)), nil)
}

func recordPattern(t any) any {
	keys := ownKeys(get(t, "patternProperties"))
	if len(keys) == 0 {
		return Undef
	}
	return keys[0]
}

func recordValue(t any) any {
	return get(get(t, "patternProperties"), propKey(recordPattern(t)))
}

// propKey is the property name a computed key `[value]` makes.
func propKey(value any) string { return toStringValue(value) }

// recordPatternToType is RecordPatternToType.
func recordPatternToType(pattern any) any {
	switch pattern {
	case stringKey:
		return tString(nil)
	case integerKey:
		return tInteger(nil)
	case numberKey:
		return tNumber(nil)
	}
	return templateLiteralDecodeUnsafe(pattern)
}

func recordKey(t any) any { return recordPatternToType(recordPattern(t)) }

// bigFromString is BigInt(text) for the digits the script tokenizer produced.
func bigFromString(text string) *big.Int {
	n, ok := new(big.Int).SetString(text, 10)
	if !ok {
		throw("SyntaxError", "Cannot convert %s to a BigInt", text)
	}
	return n
}
