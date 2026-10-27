package testfixture

import (
	"fmt"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func RejectInvalidToolSchema(ext *sdk.Extension) (rejected bool) {
	defer func() {
		failure, ok := recover().(error)
		rejected = ok && failure.Error() == fmt.Sprintf(`Tool "schema-invalid" registered by extension "%s" must define an object parameter schema.`, ext.Name())
	}()
	ext.Tool("schema-invalid", "Must not register", nil, func(sdk.Context, map[string]any) (any, error) { return nil, nil })
	return false
}

func ReferenceToolSchemaRejected() bool {
	return RejectInvalidToolSchema(sdk.New("reference"))
}

// registerTypeSchemaTool registers the tool whose parameters are built with pi-ai's Type (TestConformance_TypeSchemaToolParametersAgreeAcrossSDKs).
func registerTypeSchemaTool(ext *sdk.Extension) {
	parameters := sdk.Type.Object(sdk.NewOrderedObject(
		"query", sdk.Type.String(sdk.NewOrderedObject("description", "What to look up")),
		"limit", sdk.Type.Optional(sdk.Type.Integer(sdk.NewOrderedObject("minimum", 1, "maximum", 50))),
		"mode", sdk.Type.Union([]any{sdk.Type.Literal("fast"), sdk.Type.Literal("deep")}),
		"tags", sdk.Type.Optional(sdk.Type.Array(sdk.Type.String())),
		"filter", sdk.Type.Optional(sdk.Type.Record(sdk.Type.String(), sdk.Type.Number())),
	), sdk.NewOrderedObject("additionalProperties", false))
	ext.Tool("type-schema-tool", "Parameters built with Type", parameters.Schema(), func(sdk.Context, map[string]any) (any, error) { return "ok", nil })
}

// registerTypeEngineTool registers the tool whose parameters have one property per member of Type that the host's type engine serves
// (TestConformance_TypeEngineMembersAgreeAcrossSDKs); the Node fixture builds the same with Pi's own Type.
func registerTypeEngineTool(ext *sdk.Extension) {
	T := sdk.Type
	str, num, integer, boolean := T.String(), T.Number(), T.Integer(), T.Boolean()
	object := func(pairs ...any) *sdk.TSchema { return T.Object(sdk.NewOrderedObject(pairs...)) }
	base := object("id", str, "name", T.Optional(str), "n", integer, "tags", T.Array(str))
	same := object("a", str)
	key := T.Identifier("K")
	parameters := T.Object(sdk.NewOrderedObject(
		"picked", T.Pick(base, []string{"id", "name"}),
		"omitted", T.Omit(base, []string{"tags"}),
		"partial", T.Partial(base),
		"required", T.Required(object("a", T.Optional(str), "b", T.Optional(num))),
		"keys", T.KeyOf(base),
		"indexed", T.Index(base, T.Literal("n")),
		"readonlyObject", T.ReadonlyObject(base),
		"readonlyType", T.ReadonlyType(T.Array(str)),
		"evaluated", T.Evaluate(T.Intersect([]any{same, object("b", num)})),
		"excluded", T.Exclude(T.Union([]any{str, num, boolean}), str),
		"extracted", T.Extract(T.Union([]any{str, num, boolean}), T.Union([]any{str, num})),
		"nonNullable", T.NonNullable(T.Union([]any{str, T.Null()})),
		"uppercase", T.Uppercase(T.Literal("abc")),
		"lowercase", T.Lowercase(T.Literal("ABC")),
		"capitalize", T.Capitalize(T.Literal("abc")),
		"uncapitalize", T.Uncapitalize(T.Literal("ABC")),
		"parameters", T.Parameters(T.Function([]any{str, num}, boolean)),
		"constructorParameters", T.ConstructorParameters(T.Constructor([]any{str}, object())),
		"returnType", T.ReturnType(T.Function([]any{str}, num)),
		"instanceType", T.InstanceType(T.Constructor([]any{str}, same)),
		"fn", T.Function([]any{str}, num),
		"constructorType", T.Constructor([]any{str}, same),
		"tuple", T.Tuple([]any{str, num}),
		"template", T.TemplateLiteral("a${string}"),
		"conditional", T.Conditional(str, str, T.Literal("yes"), T.Literal("no")),
		"mapped", T.Mapped(key, T.Union([]any{T.Literal("a"), T.Literal("b")}), T.Ref("K"), str),
		"cyclic", T.Cyclic(sdk.NewOrderedObject("A", object("next", T.Optional(T.Ref("A")))), "A"),
		"module", T.Module(sdk.NewOrderedObject("A", str, "B", T.Array(T.Ref("A")))),
		"interface", T.Interface([]any{same}, sdk.NewOrderedObject("b", num)),
		"dependent", T.Dependent(str, num, boolean),
		"extends", T.Extends(nil, str, str),
		"instantiate", T.Instantiate(sdk.NewOrderedObject("A", str), T.Ref("A")),
		"call", T.Instantiate(sdk.NewOrderedObject("Id", T.Generic([]any{T.Parameter("T")}, T.Ref("T"))), T.Call(T.Ref("Id"), []any{str})),
		"script", T.Script("{ a: string; b?: number[] }"),
		"rest", T.Rest(T.Tuple([]any{str})),
		"this", T.This(),
		"infer", T.Infer("U", str),
		"identifier", key,
		"generic", T.Generic([]any{T.Parameter("T", str, str)}, T.Array(T.Ref("T"))),
		"recordKey", T.RecordKey(T.Record(str, num)),
		"recordValue", T.RecordValue(T.Record(str, num)),
		"recordPattern", T.String(sdk.NewOrderedObject("description", T.RecordPattern(T.Record(str, num)))),
		"enumKeyRecord", T.Record(T.Enum([]any{"x", "y"}), num),
	), sdk.NewOrderedObject("additionalProperties", false))
	ext.Tool("type-engine-tool", "Parameters built with the engine members of Type", parameters.Schema(), func(sdk.Context, map[string]any) (any, error) { return "ok", nil })
}
