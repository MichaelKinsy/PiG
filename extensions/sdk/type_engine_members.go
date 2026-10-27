package sdk

// The members of Type that the host's engine computes (see type_engine.go). Every one has Pi's name and argument order; the options argument is optional. What TypeBox throws panics
// with its message.

// engineWith appends the options of a call to its arguments when there are any.
func engineWith(args []any, options []TypeOptions) []any {
	if opts := firstOptions(options); opts != nil {
		args = append(args, opts)
	}
	return args
}

func engineCall(member string, args []any, options []TypeOptions) *TSchema {
	return engineSchema(typeEngineCall(member, engineWith(args, options)...))
}

// Call is `Type.Call(target, arguments)`: a generic called with arguments, instantiated.
func (typeNamespace) Call(target any, arguments []any) *TSchema {
	return engineSchema(typeEngineCall("Call", target, arguments))
}

// Capitalize, Lowercase, Uncapitalize and Uppercase are the string intrinsics: `Type.Capitalize(Type.Literal("ab"))` is the literal "Ab".
func (typeNamespace) Capitalize(schema any, options ...TypeOptions) *TSchema {
	return engineCall("Capitalize", []any{schema}, options)
}
func (typeNamespace) Lowercase(schema any, options ...TypeOptions) *TSchema {
	return engineCall("Lowercase", []any{schema}, options)
}
func (typeNamespace) Uncapitalize(schema any, options ...TypeOptions) *TSchema {
	return engineCall("Uncapitalize", []any{schema}, options)
}
func (typeNamespace) Uppercase(schema any, options ...TypeOptions) *TSchema {
	return engineCall("Uppercase", []any{schema}, options)
}

// Conditional is `Type.Conditional(left, right, trueType, falseType)`: TypeScript's `left extends right ? trueType : falseType`.
func (typeNamespace) Conditional(left, right, trueType, falseType any, options ...TypeOptions) *TSchema {
	return engineCall("Conditional", []any{left, right, trueType, falseType}, options)
}

// Constructor is `Type.Constructor(parameters, instanceType)`.
func (typeNamespace) Constructor(parameters []any, instanceType any, options ...TypeOptions) *TSchema {
	return engineCall("Constructor", []any{parameters, instanceType}, options)
}

// ConstructorParameters, Parameters, InstanceType and ReturnType are TypeScript's utility types of the same names.
func (typeNamespace) ConstructorParameters(schema any, options ...TypeOptions) *TSchema {
	return engineCall("ConstructorParameters", []any{schema}, options)
}
func (typeNamespace) Parameters(schema any, options ...TypeOptions) *TSchema {
	return engineCall("Parameters", []any{schema}, options)
}
func (typeNamespace) InstanceType(schema any, options ...TypeOptions) *TSchema {
	return engineCall("InstanceType", []any{schema}, options)
}
func (typeNamespace) ReturnType(schema any, options ...TypeOptions) *TSchema {
	return engineCall("ReturnType", []any{schema}, options)
}

// Cyclic is `Type.Cyclic(defs, ref)`: recursive definitions, each given its `$id`.
func (typeNamespace) Cyclic(defs *OrderedObject, ref string, options ...TypeOptions) *TSchema {
	return engineCall("Cyclic", []any{defs, ref}, options)
}

// Dependent is `Type.Dependent(if, then, else)`.
func (typeNamespace) Dependent(ifType, thenType, elseType any, options ...TypeOptions) *TSchema {
	return engineCall("Dependent", []any{ifType, thenType, elseType}, options)
}

// Evaluate is `Type.Evaluate(type)`: unions and intersections simplified.
func (typeNamespace) Evaluate(schema any, options ...TypeOptions) *TSchema {
	return engineCall("Evaluate", []any{schema}, options)
}

// Exclude and Extract are TypeScript's `Exclude<left, right>` and `Extract<left, right>`.
func (typeNamespace) Exclude(left, right any, options ...TypeOptions) *TSchema {
	return engineCall("Exclude", []any{left, right}, options)
}
func (typeNamespace) Extract(left, right any, options ...TypeOptions) *TSchema {
	return engineCall("Extract", []any{left, right}, options)
}

// Extends is `Type.Extends(inferred, left, right)`: whether left extends right, as an ExtendsTrue, ExtendsUnion or ExtendsFalse value; inferred holds the types an `infer` already
// captured (nil is none).
func (typeNamespace) Extends(inferred *OrderedObject, left, right any) *TSchema {
	if inferred == nil {
		inferred = NewOrderedObject()
	}
	return engineSchema(typeEngineCall("Extends", inferred, left, right))
}

// Function is `Type.Function(parameters, returnType)`.
func (typeNamespace) Function(parameters []any, returnType any, options ...TypeOptions) *TSchema {
	return engineCall("Function", []any{parameters, returnType}, options)
}

// Generic is `Type.Generic(parameters, expression)`.
func (typeNamespace) Generic(parameters []any, expression any) *TSchema {
	return engineSchema(typeEngineCall("Generic", parameters, expression))
}

// Identifier is `Type.Identifier(name)`.
func (typeNamespace) Identifier(name string) *TSchema {
	return engineSchema(typeEngineCall("Identifier", name))
}

// Index is `Type.Index(type, indexer)`: TypeScript's `type[indexer]`. indexer is a schema or a list of keys.
func (typeNamespace) Index(schema any, indexer any, options ...TypeOptions) *TSchema {
	return engineCall("Index", []any{schema, indexer}, options)
}

// Infer is `Type.Infer(name)` or `Type.Infer(name, extends)`: an `infer name` of a conditional's right side.
func (typeNamespace) Infer(name string, extends ...any) *TSchema {
	if len(extends) > 0 {
		return engineSchema(typeEngineCall("Infer", name, extends[0]))
	}
	return engineSchema(typeEngineCall("Infer", name))
}

// Instantiate is `Type.Instantiate(context, type)`: the references of type resolved in context, and the deferred actions it holds computed.
func (typeNamespace) Instantiate(context *OrderedObject, schema any) *TSchema {
	if context == nil {
		context = NewOrderedObject()
	}
	return engineSchema(typeEngineCall("Instantiate", context, schema))
}

// Interface is `Type.Interface(heritage, properties)`: TypeScript's `interface X extends heritage { properties }`.
func (typeNamespace) Interface(heritage []any, properties *OrderedObject, options ...TypeOptions) *TSchema {
	return engineCall("Interface", []any{heritage, properties}, options)
}

// KeyOf is `Type.KeyOf(type)`: TypeScript's `keyof type`.
func (typeNamespace) KeyOf(schema any, options ...TypeOptions) *TSchema {
	return engineCall("KeyOf", []any{schema}, options)
}

// Mapped is `Type.Mapped(identifier, type, as, property)`: TypeScript's `{ [identifier in type as as]: property }`.
func (typeNamespace) Mapped(identifier, schema, as, property any, options ...TypeOptions) *TSchema {
	return engineCall("Mapped", []any{identifier, schema, as, property}, options)
}

// Module is `Type.Module(declarations)`: the declarations, named types that may refer to one another, instantiated; the result is an object of the instantiated types.
func (typeNamespace) Module(declarations *OrderedObject, options ...TypeOptions) *TSchema {
	if declarations == nil {
		declarations = NewOrderedObject()
	}
	return engineCall("Module", []any{declarations}, options)
}

// NonNullable is TypeScript's `NonNullable<type>`.
func (typeNamespace) NonNullable(schema any, options ...TypeOptions) *TSchema {
	return engineCall("NonNullable", []any{schema}, options)
}

// Omit and Pick are TypeScript's `Omit<type, keys>` and `Pick<type, keys>`. keys is a list of keys ([]string or []any) or a schema of them.
func (typeNamespace) Omit(schema any, keys any, options ...TypeOptions) *TSchema {
	return engineCall("Omit", []any{schema, keys}, options)
}
func (typeNamespace) Pick(schema any, keys any, options ...TypeOptions) *TSchema {
	return engineCall("Pick", []any{schema, keys}, options)
}

// Parameter is `Type.Parameter(name)`, `Type.Parameter(name, extends)` or `Type.Parameter(name, extends, equals)`: a generic's parameter.
func (typeNamespace) Parameter(name string, rest ...any) *TSchema {
	return engineSchema(typeEngineCall("Parameter", append([]any{name}, rest...)...))
}

// Partial and Required are TypeScript's `Partial<type>` and `Required<type>`.
func (typeNamespace) Partial(schema any, options ...TypeOptions) *TSchema {
	return engineCall("Partial", []any{schema}, options)
}
func (typeNamespace) Required(schema any, options ...TypeOptions) *TSchema {
	return engineCall("Required", []any{schema}, options)
}

// ReadonlyObject and ReadonlyType are TypeScript's `Readonly<type>`.
func (typeNamespace) ReadonlyObject(schema any, options ...TypeOptions) *TSchema {
	return engineCall("ReadonlyObject", []any{schema}, options)
}
func (typeNamespace) ReadonlyType(schema any, options ...TypeOptions) *TSchema {
	return engineCall("ReadonlyType", []any{schema}, options)
}

// RecordKey, RecordPattern and RecordValue take a Record apart: the type of its keys, the pattern of its keys, and its value type.
func (typeNamespace) RecordKey(record any) *TSchema {
	return engineSchema(typeEngineCall("RecordKey", record))
}
func (typeNamespace) RecordPattern(record any) string {
	pattern, _ := typeEngineCall("RecordPattern", record).(string)
	return pattern
}
func (typeNamespace) RecordValue(record any) *TSchema {
	return engineSchema(typeEngineCall("RecordValue", record))
}

// Rest is `Type.Rest(type)`: a `...type` element of a tuple or parameter list.
func (typeNamespace) Rest(schema any) *TSchema { return engineSchema(typeEngineCall("Rest", schema)) }

// Script is `Type.Script(script)`, `Type.Script(script, options)`, `Type.Script(context, script)` or `Type.Script(context, script, options)`: TypeScript type text, parsed and
// instantiated. context is an *OrderedObject of the named types the script refers to; options is a TypeOptions. Any other argument count panics as Pi's does.
func (typeNamespace) Script(args ...any) *TSchema {
	return engineSchema(typeEngineCall("Script", args...))
}

// TemplateLiteral is `Type.TemplateLiteral(input)`: template literal type text such as "a${string}", or a list of types.
func (typeNamespace) TemplateLiteral(input any, options ...TypeOptions) *TSchema {
	return engineCall("TemplateLiteral", []any{input}, options)
}

// This is `Type.This()`.
func (typeNamespace) This(options ...TypeOptions) *TSchema { return engineCall("This", nil, options) }

// Tuple is `Type.Tuple(types)`.
func (typeNamespace) Tuple(types []any, options ...TypeOptions) *TSchema {
	return engineCall("Tuple", []any{types}, options)
}

// IsCall, IsConstructor, ... are TypeBox's guards: the value is a schema of that kind.
func (t typeNamespace) IsCall(value any) bool            { return t.IsKind(value, "Call") }
func (t typeNamespace) IsConstructor(value any) bool     { return t.IsKind(value, "Constructor") }
func (t typeNamespace) IsCyclic(value any) bool          { return t.IsKind(value, "Cyclic") }
func (t typeNamespace) IsDependent(value any) bool       { return t.IsKind(value, "Dependent") }
func (t typeNamespace) IsFunction(value any) bool        { return t.IsKind(value, "Function") }
func (t typeNamespace) IsGeneric(value any) bool         { return t.IsKind(value, "Generic") }
func (t typeNamespace) IsIdentifier(value any) bool      { return t.IsKind(value, "Identifier") }
func (t typeNamespace) IsInfer(value any) bool           { return t.IsKind(value, "Infer") }
func (t typeNamespace) IsParameter(value any) bool       { return t.IsKind(value, "Parameter") }
func (t typeNamespace) IsRest(value any) bool            { return t.IsKind(value, "Rest") }
func (t typeNamespace) IsTemplateLiteral(value any) bool { return t.IsKind(value, "TemplateLiteral") }
func (t typeNamespace) IsThis(value any) bool            { return t.IsKind(value, "This") }
func (t typeNamespace) IsTuple(value any) bool           { return t.IsKind(value, "Tuple") }

// extendsResult is `Type.ExtendsResult`: the values Extends answers with and the guards and matcher over them. They compute nothing the host has to, so they run here.
type extendsResult struct{}

func extendsOutcome(kind string, inferred *OrderedObject) *TSchema {
	enumerable := []any{}
	if inferred != nil {
		enumerable = append(enumerable, "inferred", inferred)
	}
	return create(kind, enumerable, nil)
}

// ExtendsTrue is `Type.ExtendsResult.ExtendsTrue(inferred)`.
func (extendsResult) ExtendsTrue(inferred *OrderedObject) *TSchema {
	return extendsOutcome("ExtendsTrue", orEmpty(inferred))
}

// ExtendsUnion is `Type.ExtendsResult.ExtendsUnion(inferred)`.
func (extendsResult) ExtendsUnion(inferred *OrderedObject) *TSchema {
	return extendsOutcome("ExtendsUnion", orEmpty(inferred))
}

// ExtendsFalse is `Type.ExtendsResult.ExtendsFalse()`.
func (extendsResult) ExtendsFalse() *TSchema { return extendsOutcome("ExtendsFalse", nil) }

func orEmpty(inferred *OrderedObject) *OrderedObject {
	if inferred == nil {
		return NewOrderedObject()
	}
	return inferred
}

func (extendsResult) inferredObject(value any) bool {
	inferred, ok := memberValue(value, "inferred")
	return ok && Type.IsSchema(inferred)
}

// IsExtendsUnion, IsExtendsTrue, IsExtendsFalse and IsExtendsTrueLike are the guards of an Extends answer.
func (e extendsResult) IsExtendsUnion(value any) bool {
	return Type.IsKind(value, "ExtendsUnion") && e.inferredObject(value)
}
func (e extendsResult) IsExtendsTrue(value any) bool {
	return Type.IsKind(value, "ExtendsTrue") && e.inferredObject(value)
}
func (extendsResult) IsExtendsFalse(value any) bool { return Type.IsKind(value, "ExtendsFalse") }
func (e extendsResult) IsExtendsTrueLike(value any) bool {
	return e.IsExtendsUnion(value) || e.IsExtendsTrue(value)
}

// Match is `Type.ExtendsResult.Match(result, onTrue, onFalse)`: onTrue with the inferred types of a true-like result, onFalse otherwise.
func (e extendsResult) Match(result any, onTrue func(inferred any) any, onFalse func() any) any {
	if e.IsExtendsTrueLike(result) {
		inferred, _ := memberValue(result, "inferred")
		return onTrue(inferred)
	}
	return onFalse()
}
