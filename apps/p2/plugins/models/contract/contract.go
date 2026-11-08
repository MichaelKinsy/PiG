// Package contract ports apps/p2/plugins/models/contract.ts: the models and the configuration of the server and of the
// workers, as plugins see them (a leaf package other plugins import for the contracts).
package contract

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/schema"
)

func must(contract *contracts.Contract, err error) *contracts.Contract {
	if err != nil {
		panic(err)
	}
	return contract
}

var (
	c      = p2runtime.Contract
	str    = schema.Str
	num    = schema.Num
	nul    = schema.Nul
	prop   = schema.Prop
	opt    = schema.Opt
	method = func(sides ...schema.Schema) *contracts.Method {
		contractSides := make([]contracts.Schema, len(sides))
		for i, side := range sides {
			contractSides[i] = c(side)
		}
		return contracts.NewMethod(contractSides...)
	}
)

func obj(properties schema.Properties) schema.Schema { return schema.Obj(properties) }
func arr(item schema.Schema) schema.Schema           { return schema.Arr(item) }
func union(branches ...schema.Schema) schema.Schema  { return schema.Union(branches) }

// ModelInfoSchema is a model as the picker lists it.
var ModelInfoSchema = obj(schema.Properties{
	prop("provider", str()),
	prop("modelId", str()),
	prop("name", str()),
	prop("thinkingLevels", arr(str())),
	prop("contextWindow", num()),
})

// ModelInfo is a model as the picker lists it (ModelInfoSchema).
type ModelInfo struct {
	Provider       string
	ModelID        string
	Name           string
	ThinkingLevels []string
	ContextWindow  float64
}

// JSON is the model as state shows it.
func (model ModelInfo) JSON() *delta.JsonObject {
	levels := make([]any, len(model.ThinkingLevels))
	for i, level := range model.ThinkingLevels {
		levels[i] = level
	}
	return delta.JsonObjectOf("provider", model.Provider, "modelId", model.ModelID, "name", model.Name, "thinkingLevels", levels, "contextWindow", model.ContextWindow)
}

// ModelChoiceSchema is a model with a thinking level.
var ModelChoiceSchema = obj(schema.Properties{
	prop("provider", str(schema.MinLength(1))),
	prop("modelId", str(schema.MinLength(1))),
	prop("thinkingLevel", str(schema.MinLength(1))),
})

// ModelChoice is a model with a thinking level (ModelChoiceSchema).
type ModelChoice struct {
	Provider      string
	ModelID       string
	ThinkingLevel string
}

// JSON is the choice as state shows it.
func (choice ModelChoice) JSON() *delta.JsonObject {
	return delta.JsonObjectOf("provider", choice.Provider, "modelId", choice.ModelID, "thinkingLevel", choice.ThinkingLevel)
}

// ScopedModelSchema is a model of the scope with its level, when the pattern gave one.
var ScopedModelSchema = obj(schema.Properties{
	prop("provider", str()),
	prop("modelId", str()),
	opt("thinkingLevel", str()),
})

// EnabledModelsSchema is the `enabledModels` patterns (settings.json).
var EnabledModelsSchema = p2runtime.EnabledModelsSchema

// ModelSummarySchema is what the model picker needs of the configuration.
var ModelSummarySchema = obj(schema.Properties{
	prop("models", arr(ModelInfoSchema)),
	prop("defaultModel", union(ModelChoiceSchema, nul())),
	prop("errors", arr(str())),
})

// Models is the provider models for the picker and the default for new sessions (`p2.models`; server; owner only). 2:
// `enabledModels`, `scopedModels` and `setEnabledModels` (pi's model scope for cycling).
var Models = must(contracts.Remote("p2.models", contracts.RemoteDeclaration{
	Version:     2,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("owner", contracts.VariantDeclaration{
			State: []contracts.Property{
				p2runtime.StateProp("models", arr(ModelInfoSchema)),
				// Non-secret resource state for the faithful mobile picker and sign-in hint.
				p2runtime.StateProp("resources", schema.Unknown()),
				p2runtime.StateProp("defaultModel", union(ModelChoiceSchema, nul())),
				p2runtime.StateProp("errors", arr(str())),
				// settings.json `enabledModels` patterns (empty: no scope, cycling uses every model).
				p2runtime.StateProp("enabledModels", EnabledModelsSchema),
				// The patterns resolved against `models`, in pattern order (plugins/models/scope.ts).
				p2runtime.StateProp("scopedModels", arr(ScopedModelSchema)),
			},
			Methods: []contracts.MethodEntry{
				contracts.Meth("setDefault", method(ModelChoiceSchema)),
				// Saves the patterns to settings.json (an empty list removes the scope).
				contracts.Meth("setEnabledModels", method(obj(schema.Properties{prop("patterns", EnabledModelsSchema)}))),
			},
		}),
	},
}))

// ModelConfig is the server's configuration resources (`p2.model-config`; server, host-internal): `settings.json`,
// `models.json`, `auth.json`, watched and hot-reloaded. `summary` is what the model picker needs, `snapshot` everything
// without secrets (settings, signed-in providers, login methods, errors). The observer itself, for plugins that change
// the configuration (provider login), is `ModelResources`.
var ModelConfig = must(contracts.Local("p2.model-config", contracts.LocalDeclaration{
	Version: 2,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("summary", c(ModelSummarySchema)),
		contracts.Mem("snapshot", c(schema.Unknown())),
		contracts.Mem("setDefault", method(ModelChoiceSchema)),
		contracts.Mem("setEnabledModels", method(obj(schema.Properties{prop("patterns", EnabledModelsSchema)}))),
	},
}))

// WorkerModels is the worker's own configuration observer (`p2.worker-models`; worker, host-internal): its current,
// valid `settings.json`, hot-reloaded.
var WorkerModels = must(contracts.Local("p2.worker-models", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{contracts.Mem("settings", c(schema.Unknown()))},
}))

// ResourceError is a configuration file that failed to load.
type ResourceError struct {
	// Resource is "settings", "models" or "credentials".
	Resource string
	Message  string
}

// LoginMethod is a way to sign in to a provider.
type LoginMethod struct {
	// Type is "api_key" or "oauth".
	Type  string
	Label string
}

// ProviderInfo is a provider with its login and auth methods.
type ProviderInfo struct {
	ID           string
	Name         string
	AuthMethods  []string
	LoginMethods []LoginMethod
}

// CredentialInfo is a signed-in provider (never its secret).
type CredentialInfo struct {
	ProviderID string
	Type       string
}

// ResourceSnapshot is everything the configuration holds, without secrets (`ResourceSnapshot` upstream).
type ResourceSnapshot struct {
	Version     int
	Settings    p2runtime.Settings
	Credentials []CredentialInfo
	Providers   []ProviderInfo
	Models      []ModelInfo
	Errors      []ResourceError
}

func strings(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}

// JSON is the snapshot as state shows it, in upstream's key order.
func (snapshot ResourceSnapshot) JSON() *delta.JsonObject {
	credentials := make([]any, len(snapshot.Credentials))
	for i, credential := range snapshot.Credentials {
		credentials[i] = delta.JsonObjectOf("providerId", credential.ProviderID, "type", credential.Type)
	}
	errors := make([]any, len(snapshot.Errors))
	for i, each := range snapshot.Errors {
		errors[i] = delta.JsonObjectOf("resource", each.Resource, "message", each.Message)
	}
	providers := make([]any, len(snapshot.Providers))
	for i, provider := range snapshot.Providers {
		methods := make([]any, len(provider.LoginMethods))
		for j, method := range provider.LoginMethods {
			methods[j] = delta.JsonObjectOf("type", method.Type, "label", method.Label)
		}
		providers[i] = delta.JsonObjectOf("id", provider.ID, "name", provider.Name, "loginMethods", methods, "authMethods", strings(provider.AuthMethods))
	}
	models := make([]any, len(snapshot.Models))
	for i, model := range snapshot.Models {
		models[i] = model.JSON()
	}
	settings := snapshot.Settings
	if settings == nil {
		settings = delta.NewJsonObject(0)
	}
	return delta.JsonObjectOf("version", float64(snapshot.Version), "settings", settings, "credentials", credentials, "errors", errors, "providers", providers, "models", models)
}

// Resources is the configuration observer (settings.json, models.json, auth.json), one per process, watched and
// hot-reloaded. Other plugins use it as this type; the models plugin implements it.
type Resources interface {
	// Directory is the data directory holding the configuration files.
	Directory() string
	// Credentials is the credential store (auth.json).
	Credentials() ai.CredentialStore
	// Models is the current configuration generation of the models. Read it at each use: a reload replaces it.
	Models() *ai.Models
	// Durable is Durable's view of the models: it binds each call to the generation current when the call starts.
	Durable() durable.Models
	// Settings is a copy of the current, valid settings.json.
	Settings() p2runtime.Settings
	// Snapshot is the configuration's state now.
	Snapshot() ResourceSnapshot
	// Subscribe calls listener after each change of the configuration; the result stops it.
	Subscribe(listener func()) (unsubscribe func())
	// Reload loads the configuration files again now and returns once loaded.
	Reload(ctx context.Context) error
	// Publish tells the listeners the models changed (a login, a refresh).
	Publish()
	// RefreshModels refreshes providerID's models from the network.
	RefreshModels(ctx context.Context, providerID string) error
	// SaveSettings writes settings.json and returns after the reload that follows.
	SaveSettings(ctx context.Context, value any) error
	// Close stops watching and waits for the writes in progress.
	Close(ctx context.Context) error
}

// ModelResourcesValue is what `ModelConfig` contributes to ModelResources once open: read Resources at each use.
type ModelResourcesValue struct {
	Resources func() Resources
}

// ModelResources is the server's configuration observer (`p2.model-resources`): `ModelConfig` contributes it once open.
var ModelResources = func() registries.Registry[any, any] {
	registry, err := registries.DefineRegistry[any, any]("p2.model-resources", registries.DefineRegistryOptions{Version: 1})
	if err != nil {
		panic(err)
	}
	return registry
}()

// ServeModelResources serves the registry on a server facet: its value is the first contribution, nil while there is
// none.
func ServeModelResources(facet host.FacetHost) error {
	return facet.Serve(ModelResources, registries.RegistryProvider[*ModelResourcesValue, *ModelResourcesValue]{
		Compose: func(table registries.RegistryTable[*ModelResourcesValue], _ registries.ComposeContext) (*ModelResourcesValue, error) {
			if len(table) == 0 {
				return nil, nil
			}
			return table[0].Value, nil
		},
	}.Erase(), host.ServeOptions{})
}
