package sdk

import (
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// ModelRuntime is Pi's `ModelRuntime` class (core/model-runtime.ts): the configured model collection an extension builds over its own credential and model files, for models, provider auth, completions and the registrations a Session shares. The host owns the object for the extension's connection; this value names it. Models are Pi's Model objects as JSON maps.
//
// The Provider objects of Pi's class (GetProviders, GetProvider, GetRegisteredNativeProvider, RegisterNativeProvider) are not on this type: a Provider holds functions the host cannot run on the extension's behalf (the Provider surface D78 records). RegisterProvider takes a configuration without functions. StreamDeferred, FetchDeferred and CancelDeferred are not on it either.
type ModelRuntime struct{ object *graphObject }

// ModelRuntimeCreateOptions is Pi's CreateModelRuntimeOptions. A zero member is Pi's undefined. The credential store option of Pi (an object of the extension's own) is not served: AuthPath names the file.
type ModelRuntimeCreateOptions struct {
	// AuthPath is the credential file; empty is auth.json in the agent directory.
	AuthPath string
	// ModelsPath is the models.json file; nil is models.json in the agent directory.
	ModelsPath *string
	// NoModelsFile is `modelsPath: null`: no models.json, and an in-memory models store.
	NoModelsFile bool
	// ModelsStorePath is the models store file; empty is models-store.json next to the models file.
	ModelsStorePath string
	// AllowModelNetwork lets create refresh model catalogs over the network.
	AllowModelNetwork     bool
	ModelRefreshTimeoutMs *int
	CatalogBaseURL        string
	// RefreshOnCreate nil refreshes; false skips the initial refresh.
	RefreshOnCreate *bool
}

// AuthOverrides is Pi's ModelRuntimeAuthOverrides (the AbortSignal is the call's Context).
type AuthOverrides struct {
	APIKey             *string           `json:"apiKey,omitempty"`
	Env                map[string]string `json:"env,omitempty"`
	MinOAuthValidityMs *float64          `json:"minOAuthValidityMs,omitempty"`
}

// CredentialInfo is Pi's CredentialInfo, one stored credential.
type CredentialInfo struct {
	ProviderID string `json:"providerId"`
	Type       string `json:"type"`
}

// ResolveModelOptions is the options of Pi's resolveModel. Failed is the AssistantMessage of a retry and State the router state the caller stored.
type ResolveModelOptions struct {
	Reason        ModelRouteReason `json:"reason"`
	ThinkingLevel string           `json:"thinkingLevel"`
	Failed        map[string]any   `json:"failed,omitempty"`
	State         json.RawMessage  `json:"state,omitempty"`
}

// ModelRuntimeCreate is `ModelRuntime.create(options)`.
func ModelRuntimeCreate(c Context, options ModelRuntimeCreateOptions) (*ModelRuntime, error) {
	wire := map[string]any{}
	if options.AuthPath != "" {
		wire["authPath"] = options.AuthPath
	}
	if options.NoModelsFile {
		wire["modelsPath"] = nil
	} else if options.ModelsPath != nil {
		wire["modelsPath"] = *options.ModelsPath
	}
	if options.ModelsStorePath != "" {
		wire["modelsStorePath"] = options.ModelsStorePath
	}
	if options.AllowModelNetwork {
		wire["allowModelNetwork"] = true
	}
	if options.ModelRefreshTimeoutMs != nil {
		wire["modelRefreshTimeoutMs"] = *options.ModelRefreshTimeoutMs
	}
	if options.CatalogBaseURL != "" {
		wire["catalogBaseUrl"] = options.CatalogBaseURL
	}
	if options.RefreshOnCreate != nil {
		wire["refreshOnCreate"] = *options.RefreshOnCreate
	}
	object, err := newGraphObject(c, "ModelRuntime", "create", wire)
	if err != nil {
		return nil, err
	}
	return &ModelRuntime{object: object}, nil
}

// Handle is the host's name for the runtime, which an option that takes the runtime (createAgentSession) puts on the wire.
func (r *ModelRuntime) Handle() string { return r.object.Handle() }

func providerArg(provider []string) any {
	if len(provider) > 0 {
		return provider[0]
	}
	return nil
}

// GetModels lists the chat models of the runtime, or of one provider.
func (r *ModelRuntime) GetModels(provider ...string) ([]map[string]any, error) {
	return graphMethodCall[[]map[string]any](r.object, "getModels", providerArg(provider))
}

// GetModel is the chat model of provider and modelID, nil when there is none.
func (r *ModelRuntime) GetModel(provider, modelID string) (map[string]any, error) {
	return graphMethodCall[map[string]any](r.object, "getModel", provider, modelID)
}

// GetPhysicalModel is a catalog chat model that is not virtual, nil when there is none.
func (r *ModelRuntime) GetPhysicalModel(provider, modelID string) (map[string]any, error) {
	return graphMethodCall[map[string]any](r.object, "getPhysicalModel", provider, modelID)
}

// GetModelsOfType lists the models of a type, or of one provider.
func (r *ModelRuntime) GetModelsOfType(modelType ModelType, provider ...string) ([]map[string]any, error) {
	return graphMethodCall[[]map[string]any](r.object, "getModelsOfType", string(modelType), providerArg(provider))
}

// GetModelOfType is the model of a type, provider and id, nil when there is none.
func (r *ModelRuntime) GetModelOfType(modelType ModelType, provider, modelID string) (map[string]any, error) {
	return graphMethodCall[map[string]any](r.object, "getModelOfType", string(modelType), provider, modelID)
}

// GetAllModels lists the models of every type, or of one provider.
func (r *ModelRuntime) GetAllModels(provider ...string) ([]map[string]any, error) {
	return graphMethodCall[[]map[string]any](r.object, "getAllModels", providerArg(provider))
}

// GetAvailableOfType lists the models of a type whose provider has working credentials.
func (r *ModelRuntime) GetAvailableOfType(modelType ModelType, provider ...string) ([]map[string]any, error) {
	return graphMethodAsync[[]map[string]any](r.object, "getAvailableOfType", string(modelType), providerArg(provider))
}

// GetAllAvailable lists the available models of every type, or of one provider.
func (r *ModelRuntime) GetAllAvailable(provider ...string) ([]map[string]any, error) {
	return graphMethodAsync[[]map[string]any](r.object, "getAllAvailable", providerArg(provider))
}

// GetAvailable lists the available chat models after an availability refresh, or of one provider.
func (r *ModelRuntime) GetAvailable(provider ...string) ([]map[string]any, error) {
	return graphMethodAsync[[]map[string]any](r.object, "getAvailable", providerArg(provider))
}

// GetAvailableSnapshot lists the available chat models as of the last availability pass, without waiting.
func (r *ModelRuntime) GetAvailableSnapshot() ([]map[string]any, error) {
	return graphMethodCall[[]map[string]any](r.object, "getAvailableSnapshot")
}

// GetError is the runtime's configuration error, nil when there is none.
func (r *ModelRuntime) GetError() (*string, error) {
	return graphMethodCall[*string](r.object, "getError")
}

// CheckAuth reports the credential a provider's auth resolves to, nil when the provider is unconfigured.
func (r *ModelRuntime) CheckAuth(provider string) (*AuthCheck, error) {
	return graphMethodAsync[*AuthCheck](r.object, "checkAuth", provider)
}

// GetRegisteredProviderIDs lists the ids of the providers registered with RegisterProvider.
func (r *ModelRuntime) GetRegisteredProviderIDs() ([]string, error) {
	return graphMethodCall[[]string](r.object, "getRegisteredProviderIds")
}

// GetRegisteredProviderConfig is the effective configuration registered for provider, nil when there is none.
func (r *ModelRuntime) GetRegisteredProviderConfig(provider string) (map[string]any, error) {
	return graphMethodCall[map[string]any](r.object, "getRegisteredProviderConfig", provider)
}

// GetCompatibilityRequestConfig is the configured headers and authHeader flag of a chat model.
func (r *ModelRuntime) GetCompatibilityRequestConfig(model map[string]any) (map[string]any, error) {
	return graphMethodCall[map[string]any](r.object, "getCompatibilityRequestConfig", model)
}

// IsUsingOAuth reports whether provider's configured auth is OAuth.
func (r *ModelRuntime) IsUsingOAuth(provider string) (bool, error) {
	return graphMethodCall[bool](r.object, "isUsingOAuth", provider)
}

// IsUsingSubscription reports whether provider's configured auth is a subscription OAuth.
func (r *ModelRuntime) IsUsingSubscription(provider string) (bool, error) {
	return graphMethodCall[bool](r.object, "isUsingSubscription", provider)
}

// HasConfiguredAuth reports whether provider has configured auth.
func (r *ModelRuntime) HasConfiguredAuth(provider string) (bool, error) {
	return graphMethodCall[bool](r.object, "hasConfiguredAuth", provider)
}

// GetProviderAuthStatus reports where provider's credential comes from, without exposing it.
func (r *ModelRuntime) GetProviderAuthStatus(provider string) (ProviderAuthStatus, error) {
	return graphMethodCall[ProviderAuthStatus](r.object, "getProviderAuthStatus", provider)
}

// GetAuth resolves request auth for a provider id (a string) or a model object (a map), nil when it is unconfigured. The result is Pi's AuthResult: {"auth": {...}, "env"?, "source"?}.
func (r *ModelRuntime) GetAuth(providerOrModel any, overrides *AuthOverrides) (*AuthResult, error) {
	switch providerOrModel.(type) {
	case string, map[string]any:
	default:
		return nil, fmt.Errorf("GetAuth takes a provider id or a model object, not %T", providerOrModel)
	}
	return graphMethodAsync[*AuthResult](r.object, "getAuth", providerOrModel, overrides)
}

// SetRuntimeAPIKey sets an API key that lives for the runtime's lifetime and is not stored.
func (r *ModelRuntime) SetRuntimeAPIKey(provider, apiKey string) error {
	_, err := graphMethodAsync[graphDone](r.object, "setRuntimeApiKey", provider, apiKey)
	return err
}

// RemoveRuntimeAPIKey removes the runtime API key of provider.
func (r *ModelRuntime) RemoveRuntimeAPIKey(provider string) error {
	_, err := graphMethodAsync[graphDone](r.object, "removeRuntimeApiKey", provider)
	return err
}

// ListCredentials lists the stored credentials.
func (r *ModelRuntime) ListCredentials() ([]CredentialInfo, error) {
	return graphMethodAsync[[]CredentialInfo](r.object, "listCredentials")
}

// Login authenticates provider with the auth method of kind ("api_key" or "oauth"), asking the user through interaction (Prompt answers a login prompt, Notify receives a login event), and returns Pi's Credential. The interaction's Signal is not forwarded.
func (r *ModelRuntime) Login(provider, kind string, interaction AuthInteraction, options *LoginOptions) (*Credential, error) {
	if interaction.Prompt == nil {
		return nil, errors.New("Login needs an interaction with a Prompt")
	}
	c := r.object.context
	wire := map[string]any{"prompt": c.callbackID(func(args []json.RawMessage) (any, error) {
		var prompt map[string]any
		if len(args) > 0 {
			if err := json.Unmarshal(args[0], &prompt); err != nil {
				return nil, err
			}
		}
		return interaction.Prompt(prompt)
	})}
	if interaction.Notify != nil {
		wire["notify"] = c.callbackID(func(args []json.RawMessage) (any, error) {
			var event map[string]any
			if len(args) > 0 {
				if err := json.Unmarshal(args[0], &event); err != nil {
					return nil, err
				}
			}
			return nil, interaction.Notify(event)
		})
	}
	var loginOptions any
	if options != nil {
		wire := map[string]any{"agentName": options.AgentName}
		if options.GetDeviceID != nil {
			wire["getDeviceId"] = c.callbackID(func([]json.RawMessage) (any, error) { return options.GetDeviceID() })
		}
		loginOptions = wire
	}
	return graphMethodAsync[*Credential](r.object, "login", provider, kind, wire, loginOptions)
}

// Logout deletes the stored credential of provider.
func (r *ModelRuntime) Logout(provider string) error {
	_, err := graphMethodAsync[graphDone](r.object, "logout", provider)
	return err
}

// Refresh refreshes the model catalogs and availability.
func (r *ModelRuntime) Refresh(options ModelsRefreshOptions) (ModelsRefreshResult, error) {
	return graphMethodAsync[ModelsRefreshResult](r.object, "refresh", options)
}

// RegisterProvider registers a provider by a configuration without functions (name, baseUrl, apiKey, api, headers, authHeader, models). The configuration is merged over a previous registration of the provider.
func (r *ModelRuntime) RegisterProvider(provider string, config map[string]any) error {
	_, err := graphMethodCall[graphDone](r.object, "registerProvider", provider, config)
	return err
}

// UnregisterProvider removes a registered provider.
func (r *ModelRuntime) UnregisterProvider(provider string) error {
	_, err := graphMethodCall[graphDone](r.object, "unregisterProvider", provider)
	return err
}

// RegisterVirtualModel registers a virtual model; its Route runs in this process when a request resolves the model.
func (r *ModelRuntime) RegisterVirtualModel(model VirtualModel) error {
	if model.Route == nil {
		return fmt.Errorf("virtual model %s/%s must define a route", model.Provider, model.ID)
	}
	c := r.object.context
	route := c.callbackID(func(args []json.RawMessage) (any, error) {
		var request ModelRouteRequest
		if len(args) > 0 {
			if err := json.Unmarshal(args[0], &request); err != nil {
				return nil, err
			}
		}
		routed, err := model.Route(c, request)
		if err != nil {
			return nil, err
		}
		if routed.Model == nil {
			return nil, errors.New("virtual model " + model.Provider + "/" + model.ID + " routed to no model")
		}
		if raw, ok := routed.State.(json.RawMessage); ok && len(raw) == 0 {
			routed.State = nil
		}
		return struct {
			Model         map[string]any `json:"model"`
			ThinkingLevel string         `json:"thinkingLevel"`
			State         any            `json:"state,omitempty"`
		}{routed.Model, routed.ThinkingLevel, routed.State}, nil
	})
	_, err := graphMethodCall[graphDone](r.object, "registerVirtualModel", map[string]any{
		"provider": model.Provider, "id": model.ID, "name": model.Name, "thinkingLevels": model.ThinkingLevels,
		"contextWindow": model.ContextWindow, "maxTokens": model.MaxTokens, "input": model.Input, "route": route,
	})
	return err
}

// UnregisterVirtualModel removes a virtual model.
func (r *ModelRuntime) UnregisterVirtualModel(provider, id string) error {
	_, err := graphMethodCall[graphDone](r.object, "unregisterVirtualModel", provider, id)
	return err
}

// ResolveModel asks a virtual model's router for the physical model and thinking level of one request. messages are the conversation. The result is Pi's ModelRoute: {"model", "thinkingLevel", "state"?}.
func (r *ModelRuntime) ResolveModel(model map[string]any, messages []map[string]any, options ResolveModelOptions) (map[string]any, error) {
	return graphMethodAsync[map[string]any](r.object, "resolveModel", model, messages, options)
}

// Classify classifies structured state with request-time authentication. It never fails: a failure is a result with StopReason "error" or "aborted".
func (r *ModelRuntime) Classify(model map[string]any, request ClassifierContext, options *ClassifierOptions) ClassifierResult {
	result, err := graphMethodAsync[ClassifierResult](r.object, "classify", model, request, options)
	if err != nil {
		return classifierErrorResult(model, err, r.object.context.ctx != nil && r.object.context.ctx.Err() != nil)
	}
	if result.Answers == nil {
		result.Answers = NewOrderedObject()
	}
	return result
}

// GenerateImages generates images with request-time authentication. It never fails: a failure is a result with stopReason "error" or "aborted".
func (r *ModelRuntime) GenerateImages(model map[string]any, request map[string]any, options *ImagesOptions) map[string]any {
	result, err := graphMethodAsync[map[string]any](r.object, "generateImages", model, request, options)
	if err != nil {
		return imageErrorResult(model, err, r.object.context.ctx != nil && r.object.context.ctx.Err() != nil)
	}
	return result
}

func (r *ModelRuntime) registry() ModelRegistry { return ModelRegistry{context: r.object.context} }

// Stream streams a model response through the runtime, with the provider's own options (request and options as for [ModelRegistry.Stream]).
func (r *ModelRuntime) Stream(model, request, options map[string]any) *ModelEventStream {
	return r.registry().streamOn(r.Handle(), model, request, options, false)
}

// StreamSimple is Stream with the simple options of Pi's streamSimple.
func (r *ModelRuntime) StreamSimple(model, request, options map[string]any) *ModelEventStream {
	return r.registry().streamOn(r.Handle(), model, request, options, true)
}

// Complete is the final message of Stream.
func (r *ModelRuntime) Complete(model, request, options map[string]any) map[string]any {
	return r.Stream(model, request, options).Result()
}

// CompleteSimple is the final message of StreamSimple.
func (r *ModelRuntime) CompleteSimple(model, request, options map[string]any) map[string]any {
	return r.StreamSimple(model, request, options).Result()
}
