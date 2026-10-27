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
