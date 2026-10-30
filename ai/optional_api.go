package ai

import (
	"cmp"
	"fmt"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// optionalAPI describes a built-in API whose provider lives in a package under ai/providers instead of in ai, so a program that does not import that package does not link the provider's dependencies (the AWS SDK for bedrock-converse-stream).
type optionalAPI struct {
	// provider is the catalog provider the API's error messages name when the model carries no provider of its own.
	provider string
	// importPath is the package whose import registers the API's provider.
	importPath string
	// registration is the provider the imported package registered, nil while it is not linked.
	registration *OptionalAPIRegistration
}

// optionalAPIs lists the built-in APIs implemented outside ai. Importing the package of an entry (ai/all imports them all) registers the entry's provider from init.
var optionalAPIs = map[API]*optionalAPI{
	APIBedrockConverseStream: {provider: "amazon-bedrock", importPath: "github.com/MichaelKinsy/PiG/ai/providers/bedrock"},
}

// responseErrorStatuses holds the status extractors the registered providers contributed, in registration order.
var responseErrorStatuses []func(error) (int, bool)

// OptionalAPIRegistration is what a package under ai/providers hands RegisterOptionalAPI from init.
type OptionalAPIRegistration struct {
	// API is the API the package implements; it must be one of the optional built-in APIs.
	API API
	// New builds the provider of a model on API, the provider every model-to-provider factory uses.
	New func(model Model) (Provider, error)
	// Factory is the ProviderFactory LookupBuiltInProvider and RegisteredAPIs offer for API.
	Factory ProviderFactory
	// ResponseErrorStatus reports the HTTP status of the provider SDK's response error in an error chain, for NormalizeProviderError. Nil when the provider has no such error.
	ResponseErrorStatus func(error) (int, bool)
}

// RegisterOptionalAPI makes the provider of an optional built-in API available to every factory of this process. A provider package calls it from init, so importing the package is what links the provider. It panics on an API that is not optional, on a missing constructor and on a second registration.
func RegisterOptionalAPI(registration OptionalAPIRegistration) {
	optional, ok := optionalAPIs[registration.API]
	switch {
	case !ok:
		panic(fmt.Sprintf("ai: RegisterOptionalAPI: %s is not an optional built-in API", registration.API))
	case registration.New == nil || registration.Factory == nil:
		panic(fmt.Sprintf("ai: RegisterOptionalAPI: %s has no constructor", registration.API))
	case optional.registration != nil:
		panic(fmt.Sprintf("ai: RegisterOptionalAPI: %s is registered twice", registration.API))
	}
	optional.registration = &registration
	builtInProviders[registration.API] = registration.Factory
	if registration.ResponseErrorStatus != nil {
		responseErrorStatuses = append(responseErrorStatuses, registration.ResponseErrorStatus)
	}
}

// NewOptionalAPIProvider builds the provider of model on an optional built-in API (bedrock-converse-stream). It fails with the strip error when a Piglet strips the API, and when no package registered the API's provider it names the package to import.
func NewOptionalAPIProvider(api API, model Model) (Provider, error) {
	optional, ok := optionalAPIs[api]
	if !ok {
		return nil, fmt.Errorf("API %s is not an optional built-in API", api)
	}
	// pig additive (D92): a Piglet's strip.apis disables the API at runtime the way a pig_strip_<api> build compiles it out.
	if pigstrip.Has(pigstrip.ListAPIs, string(api)) {
		return nil, strippedAPIError(cmp.Or(modelProviderID(&model), optional.provider), api)
	}
	if optional.registration == nil {
		return nil, fmt.Errorf("Provider %s: API %s is not linked into this program; import %s", cmp.Or(modelProviderID(&model), optional.provider), api, optional.importPath)
	}
	return optional.registration.New(model)
}

// responseErrorStatus reports the HTTP status of a provider SDK's response error in err's chain.
func responseErrorStatus(err error) (int, bool) {
	for _, extract := range responseErrorStatuses {
		if status, ok := extract(err); ok {
			return status, true
		}
	}
	return 0, false
}
