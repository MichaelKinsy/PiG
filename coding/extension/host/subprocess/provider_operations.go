package subprocess

// Ports the image and classifier implementations of a provider (packages/coding-agent/src/core/extensions/types.ts:1896-1898 ProviderConfig.images and ProviderConfig.classifiers; packages/ai/src/models.ts createProvider) across the extension boundary.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// RequestProviderOperation runs an implementation of a provider config in its owning extension. The request carries the provider in Tool and {kind, ...} in Args. An image or classifier operation carries {kind, api, model, context, options} and its response is the AssistantImages or ClassifierResult; a refreshModels operation carries {kind, context} and its response is the model list refreshModels returned. A callback failure is the request's error.
const RequestProviderOperation = "provider_operation"

// The kinds of a provider operation.
const (
	ProviderOperationImages        = "images"
	ProviderOperationClassifiers   = "classifiers"
	ProviderOperationRefreshModels = "refreshModels"
)

// encodeImagesOptions is the wire form of the options an image API receives. It carries the caller's API key; the extension receives it as `apiKey`.
func encodeImagesOptions(o ai.ImagesOptions) map[string]any {
	values := map[string]any{}
	if o.APIKey != "" || o.APIKeySet {
		values["apiKey"] = o.APIKey
	}
	if o.Headers != nil {
		values["headers"] = o.Headers
	}
	if o.Env != nil {
		values["env"] = o.Env
	}
	if o.Metadata != nil {
		values["metadata"] = o.Metadata
	}
	if o.TimeoutMs != 0 {
		values["timeoutMs"] = o.TimeoutMs
	}
	if o.MaxRetries != nil {
		values["maxRetries"] = *o.MaxRetries
	}
	if o.MaxRetryDelayMs != nil {
		values["maxRetryDelayMs"] = *o.MaxRetryDelayMs
	}
	return values
}

// encodeClassifierOptions is the wire form of the options a classifier API receives, with the caller's API key as `apiKey`.
func encodeClassifierOptions(o ai.ClassifierOptions) map[string]any {
	values := map[string]any{}
	if o.APIKey != "" || o.APIKeySet {
		values["apiKey"] = o.APIKey
	}
	if o.Headers != nil {
		values["headers"] = o.Headers
	}
	if o.Env != nil {
		values["env"] = o.Env
	}
	if o.TimeoutMs != nil {
		values["timeoutMs"] = *o.TimeoutMs
	}
	if o.MaxRetries != nil {
		values["maxRetries"] = *o.MaxRetries
	}
	if o.MaxRetryDelayMs != nil {
		values["maxRetryDelayMs"] = *o.MaxRetryDelayMs
	}
	if o.Temperature != nil {
		values["temperature"] = *o.Temperature
	}
	return values
}

// attachProviderOperations wires the implementations a provider declaration names to the extension that owns them: the streamSimple callback, the image and classifier APIs, refreshModels and oauth.modifyModels.
// pig additive (D19): connection-owned callbacks carry the image and classifier implementations across the SDK boundary without changing registry scope.
func (h *Host) attachProviderOperations(me *managedExt, provider ProviderDecl, cfg *extension.ProviderConfig) {
	if provider.StreamSimple {
		cfg.StreamSimple = h.providerStreamCallback(me, provider.Name)
	}
	if len(provider.ImageAPIs) > 0 {
		cfg.Images = make(ai.ProviderImageAPIMap, len(provider.ImageAPIs))
		for _, api := range provider.ImageAPIs {
			cfg.Images[ai.ImageAPI(api)] = &ai.ProviderImages{GenerateImages: func(ctx context.Context, model *ai.ImageModel, request ai.ImagesContext, options ai.ImagesOptions) (ai.AssistantImages, error) {
				return remoteOperation[ai.AssistantImages](ctx, me, provider.Name, ProviderOperationImages, api, extension.ModelValue{Model: model}, request, encodeImagesOptions(options))
			}}
		}
	}
	if len(provider.ClassifierAPIs) > 0 {
		cfg.Classifiers = make(ai.ProviderClassifierMap, len(provider.ClassifierAPIs))
		for _, api := range provider.ClassifierAPIs {
			cfg.Classifiers[ai.ClassifierAPI(api)] = &ai.ProviderClassifier{Classify: func(ctx context.Context, model *ai.ClassifierModel, request ai.ClassifierContext, options ai.ClassifierOptions) (ai.ClassifierResult, error) {
				return remoteOperation[ai.ClassifierResult](ctx, me, provider.Name, ProviderOperationClassifiers, api, extension.ModelValue{Model: model}, request, encodeClassifierOptions(options))
			}}
		}
	}
	if provider.RefreshModels {
		cfg.RefreshModels = h.providerRefreshModels(me, provider.Name)
	}
	if cfg.OAuth != nil && oauthModifiesModels(provider.Config) {
		cfg.OAuth.ModifyModels = providerModifyModels(me, provider.Name)
	}
}

func remoteOperation[T any](ctx context.Context, me *managedExt, provider, kind, api string, model extension.ModelValue, request any, options map[string]any) (T, error) {
	var result T
	args, err := json.Marshal(map[string]any{"kind": kind, "api": api, "model": model, "context": request, "options": options})
	if err != nil {
		return result, err
	}
	conn := me.connection()
	if conn == nil {
		return result, fmt.Errorf("provider %q extension is disconnected", provider)
	}
	raw, err := providerRequest(ctx, conn, provider, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: RequestProviderOperation, Tool: provider, Args: args}})
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(raw, &result)
	return result, err
}

// providerRequest sends a provider's request and returns its result; the extension's error is the request's error.
func providerRequest(ctx context.Context, conn *Conn, provider string, request *Envelope) (json.RawMessage, error) {
	response, err := conn.Request(ctx, request)
	if err != nil {
		return nil, err
	}
	if response == nil || response.Response == nil {
		return nil, fmt.Errorf("provider %q returned no response", provider)
	}
	if response.Response.Error != nil {
		return nil, response.Response.Error.ToError()
	}
	return response.Response.Result, nil
}

// connectionEnded is the error of a request that ran under the connection's lifetime: when the lifetime ended because the connection did, the error is the connection's own, as it is for a request waiting on a connection that ends, not the lifetime's cancellation: the failure that stopped it, or what Close answers a pending request ("connection closed", the same remote error value).
func connectionEnded(conn *Conn, err error) error {
	if err == nil || !errors.Is(err, context.Canceled) || conn.lifetime.Err() == nil {
		return err
	}
	if failure := conn.failureError(); failure != nil {
		return failure
	}
	return (&ErrorInfo{Message: "connection closed"}).ToError()
}

// configRefreshKey names one running config-form refreshModels request: request ids are unique per connection.
type configRefreshKey struct {
	conn    *Conn
	request string
}

// configRefreshPublication is the publish callback of the config-form refreshModels request the extension's provider.callback call belongs to.
func (h *Host) configRefreshPublication(conn *Conn, request string) (nativeProviderCallback, bool) {
	value, ok := h.configRefreshes.Load(configRefreshKey{conn: conn, request: request})
	if !ok {
		return nil, false
	}
	return value.(nativeProviderCallback), true
}

// refreshModelsRequest is the Args of a refreshModels provider operation: {kind, context}, in that order.
type refreshModelsRequest struct {
	Kind    string            `json:"kind"`
	Context refreshModelsWire `json:"context"`
}

// providerSyncUpdateArgs is the Args of the provider_sync request that runs a publication's update in the extension: {method: "update", params: {token}}.
type providerSyncUpdateArgs struct {
	Method string `json:"method"`
	Params struct {
		Token string `json:"token"`
	} `json:"params"`
}

// providerRefreshModels is ProviderConfig.refreshModels of a provider config registered by an extension (types.ts:1930): the callback runs in the extension with the
// refresh context minus its callbacks, its returned model definitions are the result, and its context.publish calls reach the refresh's Publish while the request is in flight
// (provider.callback calls of the request), a publication's update running in the extension by token as a provider object's does. The refresh's Signal is Pi's
// RefreshModelsContext.signal, which the runtime always sets ("Always present", models.ts:97-98): aborting it cancels the request, and the extension sees its signal abort. A
// context built without one runs under the connection's lifetime, as there is no caller to cancel it.
func (h *Host) providerRefreshModels(me *managedExt, provider string) func(extension.RefreshModelsContext) ([]extension.ProviderModelConfig, error) {
	return func(refresh extension.RefreshModelsContext) ([]extension.ProviderModelConfig, error) {
		conn := me.connection()
		if conn == nil {
			return nil, fmt.Errorf("provider %q extension is disconnected", provider)
		}
		signal := refresh.Signal
		if signal == nil {
			signal = conn.lifetime
		}
		args, err := json.Marshal(refreshModelsRequest{Kind: ProviderOperationRefreshModels, Context: refreshModelsParams(refresh)})
		if err != nil {
			return nil, err
		}
		id := "r" + strconv.FormatUint(conn.nextID.Add(1), 10)
		key := configRefreshKey{conn: conn, request: id}
		h.configRefreshes.Store(key, refreshPublication(refresh, func(token string) error {
			body := providerSyncUpdateArgs{Method: "update"}
			body.Params.Token = token
			update, err := json.Marshal(body)
			if err != nil {
				return err
			}
			_, err = providerRequest(signal, conn, provider, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: MethodProviderSync, Tool: provider, Args: update}})
			return err
		}))
		defer h.configRefreshes.Delete(key)
		raw, err := providerRequest(signal, conn, provider, &Envelope{Type: MsgRequest, ID: id, Request: &RequestPayload{Method: RequestProviderOperation, Tool: provider, Args: args}})
		if refresh.Signal == nil {
			err = connectionEnded(conn, err)
		}
		if err != nil {
			return nil, err
		}
		// A callback that returned nothing (JavaScript undefined) leaves the extension's models as they are.
		if len(raw) == 0 {
			return nil, nil
		}
		var models []extension.ProviderModelConfig
		if err := json.Unmarshal(raw, &models); err != nil {
			return nil, fmt.Errorf("provider %q refreshModels returned invalid models: %w", provider, err)
		}
		return models, nil
	}
}

// oauthModifiesModels reports whether a provider config's oauth descriptor declares modifyModels.
func oauthModifiesModels(config json.RawMessage) bool {
	var probe struct {
		OAuth *ProviderOAuthConfig `json:"oauth"`
	}
	return json.Unmarshal(config, &probe) == nil && probe.OAuth != nil && probe.OAuth.HasModifyModels
}

// providerModifyModels is oauth.modifyModels of a provider config registered by an extension (types.ts:2034): the callback runs in the extension with the chat models
// and the stored OAuth credential, and the models it returns are the result. Pi's callback is synchronous and its exception reaches the caller of the model listing,
// so a failed request is the error of the composer's model listing. The callback has no AbortSignal in Pi (types.ts:2034 takes (models, credentials); provider-composer.ts:566-575
// calls it inside the synchronous getAllModels), so no caller can cancel it and the request runs under the context its connection owns: it ends with the extension's connection.
func providerModifyModels(me *managedExt, provider string) func([]extension.Model, extension.OAuthCredentials) ([]extension.Model, error) {
	return func(models []extension.Model, creds extension.OAuthCredentials) ([]extension.Model, error) {
		conn := me.connection()
		if conn == nil {
			return nil, errors.New("extension not connected")
		}
		encoded, err := extension.MarshalModelObjects(models)
		if err != nil {
			return nil, err
		}
		credential, err := storedOAuthCredential(creds)
		if err != nil {
			return nil, err
		}
		args, err := json.Marshal(OAuthModifyModelsArgs{Models: encoded, Credentials: credential})
		if err != nil {
			return nil, err
		}
		raw, err := providerRequest(conn.lifetime, conn, provider, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: MethodOAuthModifyModels, Tool: provider, Args: args}})
		err = connectionEnded(conn, err)
		if err != nil {
			return nil, err
		}
		var result OAuthModifyModelsResult
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, err
		}
		decoded, err := decodeProviderModels(provider, result.Models)
		if err != nil {
			return nil, err
		}
		return chatModels(decoded), nil
	}
}

// generateImages runs the provider object's generateImages in its extension.
func (p *nativeProviderProxy) generateImages(ctx context.Context, model *ai.ImageModel, request ai.ImagesContext, options ai.ImagesOptions) (ai.AssistantImages, error) {
	return nativeObjectValue[ai.AssistantImages](p, ctx, "generateImages", map[string]any{"model": extension.ModelValue{Model: model}, "context": request, "options": encodeImagesOptions(options)}, nil)
}

// classify runs the provider object's classify in its extension.
func (p *nativeProviderProxy) classify(ctx context.Context, model *ai.ClassifierModel, request ai.ClassifierContext, options ai.ClassifierOptions) (ai.ClassifierResult, error) {
	return nativeObjectValue[ai.ClassifierResult](p, ctx, "classify", map[string]any{"model": extension.ModelValue{Model: model}, "context": request, "options": encodeClassifierOptions(options)}, nil)
}

// operations wires the image and classifier methods a provider object declares.
func (p *nativeProviderProxy) operations(carrier *extension.NativeProvider) {
	if slices.Contains(p.declaration.Methods, "generateImages") {
		carrier.GenerateImages = p.generateImages
	}
	if slices.Contains(p.declaration.Methods, "classify") {
		carrier.Classify = p.classify
	}
	if slices.Contains(p.declaration.Methods, "getModels") {
		carrier.GetModels = p.getModels
	}
	if slices.Contains(p.declaration.Methods, "getAllModels") {
		carrier.GetAllModels = p.allModels
	}
	if slices.Contains(p.declaration.Methods, "filterAllModels") {
		carrier.FilterAllModels = p.filterAllModels
	}
	if slices.Contains(p.declaration.Methods, "fetchDeferred") {
		carrier.FetchDeferred = p.fetchDeferred
	}
	if slices.Contains(p.declaration.Methods, "cancelDeferred") {
		carrier.CancelDeferred = p.cancelDeferred
	}
}

// queueLoadedProvider queues the registration an extension declared while it loaded, as Pi's loader queues it (loader.ts:217-218): the registry is not asked until the services or the runner that binds it apply the queue, after every extension of the load finished, and the registry's rejection is the diagnostic `Extension "<path>" error: <message>` of that binding (agent-session-services.ts:157-169, runner.ts:471-487), not a failed load. What the host itself keeps for the extension (the replicas' record, the ownership of the provider's callbacks, the bridged login) is recorded at once: a reload decides which providers its replacement keeps from it, and a host that binds no registry (the login and inspection commands) lists the login from the load. A callback registration takes effect whether or not a registry was bound before the load. Native Provider objects register through [Host.registerNativeProvider] instead.
//
// upstream: loader.ts:217-218, agent-session-services.ts:157-169
func (h *Host) queueLoadedProvider(me *managedExt, conn *Conn, provider ProviderDecl, cfg extension.ProviderConfig) error {
	// Model-provider registration and OAuth registration are independent concerns: an extension may contribute an OAuth login with no model-provider callback wired on the host, so gate only the model-provider hook, never the OAuth registration.
	h.attachProviderOperations(me, provider, &cfg)
	var withdrawRecord func()
	if h.uiBridge != nil {
		withdrawRecord = h.uiBridge.RecordProviderRegistration(provider.Name, provider.Config)
	}
	h.mu.Lock()
	notifySuperseded, releaseClaim := h.claimProviderOwnershipLocked(me, conn, provider.Name)
	h.mu.Unlock()
	undoOAuth, err := h.registerOAuthProviderUndoable(me, provider.Name, provider.Config)
	if err != nil {
		return err
	}
	// Pi's registry keeps nothing of a registration it refuses (model-runtime.ts registerProvider validates before it stores), so what the host recorded for this one is given back, and the providers this claim superseded are not told they were.
	h.providerRuntime.QueueProviderRegistration(provider.Name, cfg, extConfigOrigin(me.config), func(err error) {
		if err == nil {
			notifySuperseded()
			return
		}
		undoOAuth()
		h.mu.Lock()
		releaseClaim()
		// A later claim may have inherited the login this registration made; with the login gone, no extension owns one.
		if _, ok := ai.GetOAuthProvider(provider.Name); !ok {
			for _, other := range h.exts {
				other.oauthProviderNames = slices.DeleteFunc(other.oauthProviderNames, func(name string) bool { return name == provider.Name })
			}
		}
		h.mu.Unlock()
		if withdrawRecord != nil {
			withdrawRecord()
		}
	})
	return nil
}
