// pig-specific: serves Pi's ModelRuntime (packages/coding-agent/src/core/model-runtime.ts) to the Go, Python and Rust extension SDKs as a host-owned object an extension names by handle. The object is this package's ModelRuntime, the one the Session uses, built over its own credential and model files. Every method answers Pi's result in the JSON shape Pi's types have. A Provider object (getProviders, getProvider, getRegisteredNativeProvider, registerNativeProvider) and a provider configuration with functions are not served: they hold code the host cannot run on the extension's behalf (the Provider surface D78 records).

package coding

import (
	"context"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// GraphModelRuntime is a ModelRuntime an extension constructed.
type GraphModelRuntime struct {
	runtime *ModelRuntime
	scope   subprocess.SessionGraphScope
}

var (
	_ subprocess.SessionGraphObject   = (*GraphModelRuntime)(nil)
	_ subprocess.SessionGraphStreamer = (*GraphModelRuntime)(nil)
	_ subprocess.SessionGraphReleaser = (*GraphModelRuntime)(nil)
)

func newGraphModelRuntime(ctx context.Context, scope subprocess.SessionGraphScope, op string, raw json.RawMessage) (*GraphModelRuntime, error) {
	if op != "create" {
		return nil, fmt.Errorf("ModelRuntime has no static factory %q", op)
	}
	args, err := icodingagent.ParseGraphArgs(raw)
	if err != nil {
		return nil, err
	}
	var options struct {
		Credentials           json.RawMessage `json:"credentials"`
		AuthPath              string          `json:"authPath"`
		ModelsPath            json.RawMessage `json:"modelsPath"`
		ModelsStorePath       string          `json:"modelsStorePath"`
		AllowModelNetwork     bool            `json:"allowModelNetwork"`
		ModelRefreshTimeoutMs *int            `json:"modelRefreshTimeoutMs"`
		CatalogBaseURL        string          `json:"catalogBaseUrl"`
		RefreshOnCreate       *bool           `json:"refreshOnCreate"`
	}
	if err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &options) }); err != nil {
		return nil, err
	}
	if len(options.Credentials) != 0 && string(options.Credentials) != "null" {
		return nil, errors.New("ModelRuntime.create: a credential store of the extension's own is not served by the host; pass authPath")
	}
	create := CreateModelRuntimeOptions{AuthPath: options.AuthPath, ModelsStorePath: options.ModelsStorePath, AllowModelNetwork: options.AllowModelNetwork, ModelRefreshTimeoutMs: options.ModelRefreshTimeoutMs, CatalogBaseURL: options.CatalogBaseURL, RefreshOnCreate: options.RefreshOnCreate}
	// modelsPath: absent selects the agent directory's models.json, null selects no file (an in-memory store), a string names the file.
	if len(options.ModelsPath) != 0 {
		if string(options.ModelsPath) == "null" {
			var none *string
			create.ModelsPath = &none
		} else {
			var path string
			if err := json.Unmarshal(options.ModelsPath, &path); err != nil {
				return nil, fmt.Errorf("ModelRuntime.create: modelsPath: %w", err)
			}
			resolved, err := icodingagent.ResolvePath(path, "")
			if err != nil {
				return nil, err
			}
			pointer := &resolved
			create.ModelsPath = &pointer
		}
	}
	if create.AuthPath != "" {
		resolved, err := icodingagent.ResolvePath(create.AuthPath, "")
		if err != nil {
			return nil, err
		}
		create.AuthPath = resolved
	}
	runtime, err := CreateModelRuntime(ctx, create)
	if err != nil {
		return nil, err
	}
	return &GraphModelRuntime{runtime: runtime, scope: scope}, nil
}

// Runtime is the model runtime the handle names. A session created from it shares it, as Pi's createAgentSession shares the modelRuntime it is given.
func (g *GraphModelRuntime) Runtime() *ModelRuntime { return g.runtime }

// Release implements subprocess.SessionGraphReleaser.
func (g *GraphModelRuntime) Release() { g.runtime.Close() }

// anyModel is the runtime's model a model object an extension passed names, or a bare model of that provider and id when the runtime lists none (Pi's methods take any Model object).
func (g *GraphModelRuntime) anyModel(raw json.RawMessage) (ai.AnyModel, error) {
	var wire struct {
		Type     ai.ModelType `json:"type"`
		Provider string       `json:"provider"`
		ID       string       `json:"id"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("model: %w", err)
	}
	if wire.Type == "" || wire.Type == ai.ModelTypeChat {
		if model := g.runtime.GetModel(wire.Provider, wire.ID); model != nil {
			return model, nil
		}
		if model, err := icodingagent.GraphAPIModel(raw); err == nil {
			return model, nil
		}
		return &ai.Model{ID: wire.ID, ProviderMeta: ai.ProviderMetadata{ProviderID: wire.Provider}}, nil
	}
	if model := g.runtime.GetModelOfType(wire.Type, wire.Provider, wire.ID); model != nil {
		return model, nil
	}
	return nil, fmt.Errorf("model %s/%s of type %s is not in the runtime", wire.Provider, wire.ID, wire.Type)
}

func (g *GraphModelRuntime) chatModel(raw json.RawMessage) (*ai.Model, error) {
	model, err := g.anyModel(raw)
	if err != nil {
		return nil, err
	}
	chat, ok := model.(*ai.Model)
	if !ok {
		return nil, errors.New("the model is not a chat model")
	}
	return chat, nil
}

func graphModelList[M ai.AnyModel](models []M) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(models))
	for _, model := range models {
		object, err := extension.ModelObject(model)
		if err != nil {
			return nil, err
		}
		out = append(out, object)
	}
	return out, nil
}

func graphModelObject(model ai.AnyModel) (json.RawMessage, error) {
	if model == nil {
		return nil, nil
	}
	return extension.ModelObject(model)
}

// authOverrides decodes Pi's ModelRuntimeAuthOverrides (the AbortSignal is the call's context).
func graphAuthOverrides(args icodingagent.GraphArgs, i int) (ai.AuthResolutionOverrides, error) {
	var wire struct {
		APIKey             *string           `json:"apiKey"`
		Env                map[string]string `json:"env"`
		MinOAuthValidityMs *float64          `json:"minOAuthValidityMs"`
	}
	if err := args.Unmarshal(i, func(raw json.RawMessage) error { return json.Unmarshal(raw, &wire) }); err != nil {
		return ai.AuthResolutionOverrides{}, err
	}
	return ai.AuthResolutionOverrides{APIKey: wire.APIKey, Env: wire.Env, MinOAuthValidityMs: wire.MinOAuthValidityMs}, nil
}

func graphAuthResult(result *ai.AuthResult) any {
	if result == nil {
		return nil
	}
	return icodingagent.AuthResultJSON(result)
}

func graphRefreshResult(result ai.ModelsRefreshResult) map[string]any {
	errs := map[string]string{}
	for id, err := range result.Errors {
		errs[id] = err.Error()
	}
	return map[string]any{"aborted": result.Aborted, "errors": errs}
}

func graphAuthStatus(status ai.AuthStatus) map[string]any {
	out := map[string]any{"configured": status.Configured}
	if status.Source != "" {
		out["source"] = string(status.Source)
	}
	if status.Label != "" {
		out["label"] = status.Label
	}
	return out
}

// graphProviderObjectError names why a Provider object is not served.
func graphProviderObjectError(method string) error {
	return fmt.Errorf("ModelRuntime.%s: a Provider object holds functions the host cannot run for the extension (the Provider surface D78 records); register a provider declaratively with registerProvider", method)
}

// Call runs a ModelRuntime method.
func (g *GraphModelRuntime) Call(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	args, err := icodingagent.ParseGraphArgs(raw)
	if err != nil {
		return nil, err
	}
	runtime := g.runtime
	switch method {
	case "getProviders", "getProvider", "getRegisteredNativeProvider", "registerNativeProvider":
		return nil, graphProviderObjectError(method)
	case "getModels", "getAllModels":
		provider, err := args.String(0)
		if err != nil {
			return nil, err
		}
		var providers []string
		if args.Has(0) {
			providers = []string{provider}
		}
		if method == "getAllModels" {
			return graphModelList(runtime.GetAllModels(providers...))
		}
		return graphModelList(runtime.GetModels(providers...))
	case "getModel":
		provider, err := args.String(0)
		if err != nil {
			return nil, err
		}
		id, err := args.String(1)
		if err != nil {
			return nil, err
		}
		model := runtime.GetModel(provider, id)
		if model == nil {
			return nil, nil
		}
		return graphModelObject(model)
	case "getPhysicalModel":
		provider, err := args.String(0)
		if err != nil {
			return nil, err
		}
		id, err := args.String(1)
		if err != nil {
			return nil, err
		}
		model := runtime.GetPhysicalModel(provider, id)
		if model == nil {
			return nil, nil
		}
		return graphModelObject(model)
	case "getModelsOfType", "getAvailableOfType":
		modelType, err := args.String(0)
		if err != nil {
			return nil, err
		}
		provider, err := args.String(1)
		if err != nil {
			return nil, err
		}
		var providers []string
		if args.Has(1) {
			providers = []string{provider}
		}
		if method == "getModelsOfType" {
			return graphModelList(runtime.GetModelsOfType(ai.ModelType(modelType), providers...))
		}
		models, err := runtime.GetAvailableOfType(ctx, ai.ModelType(modelType), providers...)
		if err != nil {
			return nil, err
		}
		return graphModelList(models)
	case "getModelOfType":
		modelType, err := args.String(0)
		if err != nil {
			return nil, err
		}
		provider, err := args.String(1)
		if err != nil {
			return nil, err
		}
		id, err := args.String(2)
		if err != nil {
			return nil, err
		}
		model := runtime.GetModelOfType(ai.ModelType(modelType), provider, id)
		if model == nil {
			return nil, nil
		}
		return graphModelObject(model)
	case "getAvailable":
		provider, err := args.String(0)
		if err != nil {
			return nil, err
		}
		var providers []string
		if args.Has(0) && provider != "" {
			providers = []string{provider}
		}
		models, err := runtime.GetAvailable(ctx, providers...)
		if err != nil {
			return nil, err
		}
		return graphModelList(models)
	case "getAllAvailable":
		provider, err := args.String(0)
		if err != nil {
			return nil, err
		}
		var providers []string
		if args.Has(0) {
			providers = []string{provider}
		}
		models, err := runtime.GetAllAvailable(ctx, providers...)
		if err != nil {
			return nil, err
		}
		return graphModelList(models)
	case "getAvailableSnapshot":
		return graphModelList(runtime.GetAvailableSnapshot())
	case "getError":
		if message := runtime.GetError(); message != "" {
			return message, nil
		}
		return nil, nil
	case "checkAuth":
		provider, err := args.String(0)
		if err != nil {
			return nil, err
		}
		check, err := runtime.CheckAuth(ctx, provider)
		if err != nil || check == nil {
			return nil, err
		}
		out := map[string]any{"type": string(check.Type)}
		if check.Source != "" {
			out["source"] = check.Source
		}
		return out, nil
	case "getRegisteredProviderIds":
		return append([]string{}, runtime.GetRegisteredProviderIds()...), nil
	case "getRegisteredProviderConfig":
		provider, err := args.String(0)
		if err != nil {
			return nil, err
		}
		config, err := icodingagent.GraphProviderConfig(runtime.GetRegisteredProviderConfig(provider))
		if config == nil {
			return nil, err
		}
		return config, err
	case "getCompatibilityRequestConfig":
		model, err := g.chatModel(args.Raw(0))
		if err != nil {
			return nil, err
		}
		config, err := runtime.GetCompatibilityRequestConfig(model)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"authHeader": config.AuthHeader}
		if config.Headers != nil {
			out["headers"] = config.Headers
		}
		return out, nil
	case "isUsingOAuth", "isUsingSubscription", "hasConfiguredAuth":
		provider, err := args.String(0)
		if err != nil {
			return nil, err
		}
		switch method {
		case "isUsingOAuth":
			return runtime.IsUsingOAuth(provider), nil
		case "isUsingSubscription":
			return runtime.IsUsingSubscription(provider), nil
		}
		return runtime.HasConfiguredAuth(provider), nil
	case "getProviderAuthStatus":
		provider, err := args.String(0)
		if err != nil {
			return nil, err
		}
		return graphAuthStatus(runtime.GetProviderAuthStatus(provider)), nil
	case "getAuth":
		overrides, err := graphAuthOverrides(args, 1)
		if err != nil {
			return nil, err
		}
		// A string names a provider; an object is a model.
		var provider string
		if json.Unmarshal(args.Raw(0), &provider) == nil {
			result, err := runtime.GetAuth(ctx, provider, overrides)
			return graphAuthResult(result), err
		}
		model, err := g.anyModel(args.Raw(0))
		if err != nil {
			return nil, err
		}
		result, err := runtime.GetModelAuth(ctx, model, overrides)
		return graphAuthResult(result), err
	case "setRuntimeApiKey":
		provider, err := args.String(0)
		if err != nil {
			return nil, err
		}
		key, err := args.String(1)
		if err != nil {
			return nil, err
		}
		return nil, runtime.SetRuntimeApiKey(ctx, provider, key)
	case "removeRuntimeApiKey":
		provider, err := args.String(0)
		if err != nil {
			return nil, err
		}
		return nil, runtime.RemoveRuntimeApiKey(ctx, provider)
	case "listCredentials":
		credentials, err := runtime.ListCredentials(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(credentials))
		for _, credential := range credentials {
			out = append(out, map[string]any{"providerId": credential.ProviderID, "type": string(credential.Type)})
		}
		return out, nil
	case "logout":
		provider, err := args.String(0)
		if err != nil {
			return nil, err
		}
		return nil, runtime.Logout(ctx, provider)
	case "login":
		return g.login(ctx, args)
	case "refresh":
		var options struct {
			AllowNetwork *bool    `json:"allowNetwork"`
			Providers    []string `json:"providers"`
			Force        *bool    `json:"force"`
		}
		if err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &options) }); err != nil {
			return nil, err
		}
		return graphRefreshResult(runtime.Refresh(ctx, ai.ModelsRefreshOptions{AllowNetwork: options.AllowNetwork, Providers: options.Providers, Force: options.Force})), nil
	case "registerProvider":
		id, err := args.String(0)
		if err != nil {
			return nil, err
		}
		input, err := icodingagent.GraphProviderInput(id, args.Raw(1))
		if err != nil {
			return nil, err
		}
		return nil, runtime.RegisterProvider(id, input)
	case "unregisterProvider":
		id, err := args.String(0)
		if err != nil {
			return nil, err
		}
		runtime.UnregisterProvider(id)
		return nil, nil
	case "registerVirtualModel":
		var definition struct {
			Provider       string   `json:"provider"`
			ID             string   `json:"id"`
			Name           string   `json:"name"`
			ThinkingLevels []string `json:"thinkingLevels"`
			ContextWindow  int      `json:"contextWindow"`
			MaxTokens      int      `json:"maxTokens"`
			Input          []string `json:"input"`
			Route          string   `json:"route"`
		}
		if err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &definition) }); err != nil {
			return nil, err
		}
		levels := make([]ai.ModelThinkingLevel, len(definition.ThinkingLevels))
		for i, level := range definition.ThinkingLevels {
			levels[i] = ai.ModelThinkingLevel(level)
		}
		return nil, runtime.RegisterVirtualModel(extension.VirtualModelDefinition{
			Provider: definition.Provider, ID: definition.ID, Name: definition.Name, ThinkingLevels: levels, ContextWindow: definition.ContextWindow, MaxTokens: definition.MaxTokens, Input: definition.Input,
			Route: subprocess.GraphVirtualModelRoute(g.scope, definition.Route),
		})
	case "unregisterVirtualModel":
		provider, err := args.String(0)
		if err != nil {
			return nil, err
		}
		id, err := args.String(1)
		if err != nil {
			return nil, err
		}
		runtime.UnregisterVirtualModel(provider, id)
		return nil, nil
	case "resolveModel":
		return g.resolveModel(ctx, args)
	case "classify":
		model, err := g.anyModel(args.Raw(0))
		if err != nil {
			return nil, err
		}
		modelObject, err := extension.ModelObject(model)
		if err != nil {
			return nil, err
		}
		return icodingagent.GraphClassify(ctx, runtime.Classify, modelObject, args.Raw(1), args.Raw(2)), nil
	case "generateImages":
		model, err := g.anyModel(args.Raw(0))
		if err != nil {
			return nil, err
		}
		modelObject, err := extension.ModelObject(model)
		if err != nil {
			return nil, err
		}
		return icodingagent.GraphGenerateImages(ctx, runtime.GenerateImages, modelObject, args.Raw(1), args.Raw(2)), nil
	case "stream", "streamSimple", "complete", "completeSimple", "streamDeferred", "fetchDeferred", "cancelDeferred":
		return nil, fmt.Errorf("ModelRuntime.%s is not a call: a model stream runs as a modelStream call that names the runtime", method)
	}
	return nil, fmt.Errorf("ModelRuntime has no method %s", method)
}

// resolveModel is ModelRuntime.resolveModel(model, messages, options).
func (g *GraphModelRuntime) resolveModel(ctx context.Context, args icodingagent.GraphArgs) (any, error) {
	model, err := g.chatModel(args.Raw(0))
	if err != nil {
		return nil, err
	}
	messages, err := icodingagent.GraphMessages(args.Raw(1))
	if err != nil {
		return nil, err
	}
	var options struct {
		Reason        ModelRouteReason      `json:"reason"`
		ThinkingLevel ai.ModelThinkingLevel `json:"thinkingLevel"`
		Failed        *ai.AssistantMessage  `json:"failed"`
		State         json.RawMessage       `json:"state"`
	}
	if err := args.Unmarshal(2, func(raw json.RawMessage) error { return json.Unmarshal(raw, &options) }); err != nil {
		return nil, err
	}
	route, err := g.runtime.ResolveModel(ctx, model, messages, ResolveModelOptions{Reason: options.Reason, ThinkingLevel: options.ThinkingLevel, Failed: options.Failed, State: options.State})
	if err != nil {
		return nil, err
	}
	object, err := extension.ModelObject(route.Model)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"model": object, "thinkingLevel": string(route.ThinkingLevel)}
	if len(route.State) != 0 {
		out["state"] = route.State
	}
	return out, nil
}

// login is ModelRuntime.login(providerId, type, interaction, options): the interaction's prompt and notify are functions of the extension, named by callback id.
func (g *GraphModelRuntime) login(ctx context.Context, args icodingagent.GraphArgs) (any, error) {
	provider, err := args.String(0)
	if err != nil {
		return nil, err
	}
	kind, err := args.String(1)
	if err != nil {
		return nil, err
	}
	var interaction struct {
		Prompt string `json:"prompt"`
		Notify string `json:"notify"`
	}
	if err := args.Unmarshal(2, func(raw json.RawMessage) error { return json.Unmarshal(raw, &interaction) }); err != nil {
		return nil, err
	}
	var options struct {
		AgentName   *string `json:"agentName"`
		GetDeviceID string  `json:"getDeviceId"`
	}
	if err := args.Unmarshal(3, func(raw json.RawMessage) error { return json.Unmarshal(raw, &options) }); err != nil {
		return nil, err
	}
	loginOptions := ai.LoginOptions{AgentName: options.AgentName}
	if options.GetDeviceID != "" {
		loginOptions.GetDeviceID = func() string {
			answer, err := g.scope.Callback(ctx, options.GetDeviceID, json.RawMessage("[]"))
			var id string
			if err != nil || json.Unmarshal(answer, &id) != nil {
				return ""
			}
			return id
		}
	}
	credential, err := g.runtime.Login(ctx, provider, ai.AuthType(kind), ai.AuthInteraction{
		Prompt: func(ctx context.Context, prompt ai.AuthPrompt) (string, error) {
			encoded, err := json.Marshal([]ai.AuthPrompt{prompt})
			if err != nil {
				return "", err
			}
			answer, err := g.scope.Callback(ctx, interaction.Prompt, encoded)
			if err != nil {
				return "", err
			}
			var text string
			if err := json.Unmarshal(answer, &text); err != nil {
				return "", errors.New("the prompt answered something other than a string")
			}
			return text, nil
		},
		Notify: func(event ai.AuthEvent) {
			if interaction.Notify == "" {
				return
			}
			encoded, err := json.Marshal([]ai.AuthEvent{event})
			if err == nil {
				_, _ = g.scope.Callback(ctx, interaction.Notify, encoded)
			}
		},
	}, loginOptions)
	if err != nil {
		return nil, err
	}
	return credential, nil
}

// StreamModel implements subprocess.SessionGraphStreamer: Pi's ModelRuntime.stream and streamSimple of the model object and request the extension sent.
func (g *GraphModelRuntime) StreamModel(ctx context.Context, model map[string]any, request map[string]any) (*ai.AssistantMessageEventStream, error) {
	//portlint:allow mapkeyorder the model object is decoded into an ai.Model by chatModel at once; no one observes the member order of this copy
	encoded, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	chat, err := g.chatModel(encoded)
	if err != nil {
		return nil, err
	}
	bridgeRequest := extension.ModelStreamRequestFromContext(ctx)
	ctx, aiRequest, options, err := icodingagent.GraphModelStreamRequest(ctx, chat, request, bridgeRequest)
	if err != nil {
		return nil, err
	}
	if extension.ProviderStreamSimpleSelected(ctx) {
		return g.runtime.StreamSimple(ctx, chat, aiRequest, options), nil
	}
	return g.runtime.Stream(ctx, chat, aiRequest, options), nil
}
