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
