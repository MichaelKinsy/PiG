// Services, Runtime, and Session form the public embedding API. Services owns
// shared configuration and credentials. Runtime creates and owns sessions. A
// Session owns one mutable conversation and its JSONL transcript.
//
// This follows upstream Pi's AgentSessionServices, AgentSessionRuntime, and
// AgentSession responsibilities while using Go ownership and error semantics.
package coding

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Settings is the merged user-settings view. Global settings are overlaid by
// project settings. The alias keeps the embedding API identical to the runtime
// representation.
type Settings = icodingagent.Settings

// SettingsManager is the live settings reader (re-exported alias). Use
// Services.Settings() to obtain the merged Settings; use
// Services.SettingsManager() if you need to call Reload() or watch for
// project-level changes.
type SettingsManager = icodingagent.SettingsManager

// ModelRegistry is the extension-facing synchronous facade over model lookup
// and the one Services-owned ModelRuntime.
type ModelRegistry struct {
	*icodingagent.ModelRegistry
	runtime *ModelRuntime
}

// Services is the lowest-level dependency container: auth storage,
// settings manager, and model registry. One Services instance is
// typically constructed per-process and shared by every Runtime/Session.
//
// Services is intentionally narrow. Anything that needs the auth file
// path, the merged settings, or model lookup goes through this type.
// Anything specific to a single conversation (history, tools, hooks)
// belongs on Session, not Services.
type Services struct {
	cwd          string
	agentDir     string
	auth         *ai.AuthStorage
	credentials  ai.CredentialStore
	settings     *icodingagent.SettingsManager
	registry     *ModelRegistry
	modelRuntime *ModelRuntime

	// Keep the deterministic provider's counters across request-level model reconstruction.
	testFauxProvider func() *ai.TestFauxProvider
}

// ServicesOptions configures NewServices. All fields are optional;
// empty values resolve to sensible defaults (current working directory,
// $PIG_HOME/agent or ~/.pig/agent).
type ServicesOptions struct {
	// CWD is the project directory used to discover per-project
	// settings (.pig/settings.json) and to anchor session storage.
	// Empty uses SessionManager.GetCwd when supplied, otherwise os.Getwd().
	CWD string

	// SessionManager supplies the CWD default when CWD is empty. It remains caller-owned; SessionOptions selects the log used by a Session.
	SessionManager *SessionManager

	// AgentDir is the global pig config directory (typically
	// ~/.pig/agent). Empty → DefaultAgentDir().
	AgentDir string

	// ProjectTrusted controls whether project-local settings are loaded. nil
	// preserves the SDK default of trusted project settings.
	ProjectTrusted *bool

	// SettingsManager supplies caller-owned file or memory settings instead of loading settings files.
	SettingsManager *SettingsManager
}

// NewServices constructs a Services container. Failure modes:
//
//   - cannot resolve CWD when none provided → wrapped os.Getwd error
//   - auth.json directory cannot be created/read → wrapped error
//
// Settings and registry constructors never fail; they fall back to
// defaults if their backing files are missing or malformed. The caller owns
// Services.Close; a Runtime or Session supplied with these Services does not close them.
func NewServices(opts ServicesOptions) (*Services, error) {
	cwd := opts.CWD
	if cwd == "" && opts.SessionManager != nil {
		cwd = opts.SessionManager.GetCwd()
	}
	if cwd == "" {
		c, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("coding.NewServices: resolve cwd: %w", err)
		}
		cwd = c
	}

	agentDir := opts.AgentDir
	if agentDir == "" {
		agentDir = DefaultAgentDir()
	}

	authPath := filepath.Join(agentDir, "auth.json")
	auth, err := ai.NewAuthStorage(authPath)
	if err != nil {
		return nil, fmt.Errorf("coding.NewServices: open auth storage at %s: %w", authPath, err)
	}

	projectTrusted := true
	if opts.ProjectTrusted != nil {
		projectTrusted = *opts.ProjectTrusted
	}
	// Ports packages/coding-agent/src/core/agent-session-services.ts:147.
	sm := opts.SettingsManager
	if sm == nil {
		sm = icodingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, projectTrusted)
	} else if opts.ProjectTrusted != nil {
		sm.SetProjectTrusted(projectTrusted)
	}
	reg := icodingagent.NewModelRegistry(agentDir)
	reg.SetAuthStorage(auth)
	// Mirrors ModelRuntime.create: restore stored dynamic catalogs (Radius)
	// without network access. Network refreshes run from the modes.
	reg.RefreshCatalogs(context.Background(), icodingagent.CatalogRefreshOptions{})

	services, err := newConfiguredServices(cwd, agentDir, auth, auth, sm, reg)
	if err == nil {
		_ = services.ModelRuntime().queueAvailabilityRefresh(context.Background())
	}
	return services, err
}

func newConfiguredServices(cwd, agentDir string, auth *ai.AuthStorage, credentials ai.CredentialStore, sm *icodingagent.SettingsManager, reg *icodingagent.ModelRegistry) (*Services, error) {
	services := &Services{
		cwd:         cwd,
		agentDir:    agentDir,
		auth:        auth,
		credentials: credentials,
		settings:    sm,
		registry:    &ModelRegistry{ModelRegistry: reg},
		testFauxProvider: sync.OnceValue(func() *ai.TestFauxProvider {
			return &ai.TestFauxProvider{Scenario: os.Getenv("PIG_TEST_FAUX_SCENARIO")}
		}),
	}
	modelRuntime, err := NewModelRuntime(services)
	if err != nil {
		return nil, err
	}
	services.modelRuntime = modelRuntime
	services.registry.runtime = modelRuntime
	reg.SetAvailabilityRefresh(func(ctx context.Context, providers []string) ai.ModelsRefreshResult {
		return modelRuntime.refreshAvailability(ctx, ai.ModelsRefreshOptions{Providers: providers}, ai.ModelsRefreshResult{})
	})
	reg.SetAvailabilitySnapshot(modelRuntime.configuredInSnapshot, modelRuntime.syncRegistration)
	return services, nil
}

// Close stops admission of model background work, cancels its lifetime, and drains admitted tasks. It does not close caller-owned credential or catalog storage. Call it after Sessions finish and outside model callbacks.
func (s *Services) Close() {
	if s != nil && s.registry != nil {
		s.registry.CloseModelTasks()
	}
}

// CWD returns the working directory this Services container was
// constructed with.
func (s *Services) CWD() string { return s.cwd }

// AgentDir returns the global agent config directory (typically
// ~/.pig/agent or $PIG_HOME/agent).
func (s *Services) AgentDir() string { return s.agentDir }

// Auth returns the credential storage backing auth.json. Read-mostly;
// safe to share across goroutines (file lock serialises writes).
// It is nil when the ModelRuntime was created with an injected CredentialStore; use Credentials for the store in effect.
func (s *Services) Auth() *ai.AuthStorage { return s.auth }

// Credentials returns the credential store backing model authentication: the auth.json store, or the CredentialStore passed to CreateModelRuntime.
func (s *Services) Credentials() ai.CredentialStore { return s.credentials }

// Settings returns the currently merged settings (global ⊕ project).
// This is a snapshot; call Settings() again to pick up changes after
// settings files are edited externally.
func (s *Services) Settings() Settings { return s.settings.Get() }

// SettingsManager returns the underlying live settings reader, useful
// when callers need to call Reload() or inspect global vs project
// layers separately.
func (s *Services) SettingsManager() *SettingsManager { return s.settings }

// Registry returns the model registry. Use Registry().Resolve(provider,
// model) to obtain a ModelEntry with credentials resolved through
// configvalue (env vars and !cmd shell prefixes).
func (s *Services) Registry() *ModelRegistry { return s.registry }

// ModelRuntime returns the one runtime shared by every Session and extension
// registry facade created from these Services.
func (s *Services) ModelRuntime() *ModelRuntime { return s.modelRuntime }

// DefaultAgentDir returns the default global agent config directory:
// $PIG_HOME/agent if PIG_HOME is set, else ~/.pig/agent. Mirrors
// the path-resolution logic the pig binary uses internally.
func DefaultAgentDir() string {
	return icodingagent.DefaultAgentDir()
}

// ErrNoServices is returned by APIs that require a Services instance
// when the caller passed nil.
var ErrNoServices = errors.New("coding: nil Services")

// ModelRuntime is the mode-independent model execution path owned by Services
// and shared by every Session and extension ModelRegistry facade.
type ModelRuntime struct {
	modelNetworkEnabled bool
	services            *Services
	availability        modelAvailability
	prepare             func(context.Context, *ai.Model, ai.StreamOptions) (*ai.Model, ai.Provider, ai.StreamOptions, error)
}

// NewModelRuntime constructs a runtime backed by services model configuration
// and credentials.
func NewModelRuntime(services *Services) (*ModelRuntime, error) {
	if services == nil {
		return nil, ErrNoServices
	}
	runtime := &ModelRuntime{services: services, modelNetworkEnabled: icodingagent.ModelNetworkEnabled()}
	runtime.prepare = runtime.prepareRequest
	runtime.availability = modelAvailability{
		providerSeq:     make(map[string]uint64),
		listCredentials: services.Registry().ListCredentials,
		readCredential:  services.Registry().ReadCredential,
		getAvailable:    runtime.loadAvailableModels,
	}
	return runtime, nil
}

// Close cancels and drains the Services-owned model task group. It also gives callers of CreateModelRuntime an explicit lifetime endpoint; Session Runtime.Close does not close caller-supplied Services.
func (runtime *ModelRuntime) Close() {
	if runtime != nil {
		runtime.services.Close()
	}
}

// startBackground schedules maintenance under the same owner used by availability siblings.
func (runtime *ModelRuntime) startBackground(work func(context.Context)) bool {
	return runtime.services.Registry().StartModelTask(context.Background(), work)
}

// GetModels returns metadata snapshots for the complete static and explicitly registered model catalog.
func (runtime *ModelRuntime) GetModels() []*ai.Model {
	if runtime == nil || runtime.services == nil {
		return nil
	}
	models := runtime.services.Registry().GetAllModelData()
	for i, model := range models {
		models[i] = runtime.bindCatalogModel(model)
	}
	return models
}

// GetModel returns a registered model by exact provider and model ID without synthesizing unknown identities.
func (runtime *ModelRuntime) GetModel(providerID, modelID string) *ai.Model {
	if runtime == nil || runtime.services == nil || providerID == "" || modelID == "" {
		return nil
	}
	for _, model := range runtime.services.Registry().GetProviderModelData(providerID) {
		if model.ID == modelID {
			return runtime.bindCatalogModel(model)
		}
	}
	return nil
}

// CheckAuth checks the provider's current authentication contract.
func (runtime *ModelRuntime) CheckAuth(ctx context.Context, providerID string) (*ai.AuthCheck, error) {
	runtime.services.Registry().StartRegistrationRefresh(ctx)
	return runtime.services.Registry().CheckRegistryAuth(ctx, providerID)
}

// SetChangeListener replaces the callback invoked after committed registry changes and returns a draining detach function.
func (runtime *ModelRuntime) SetChangeListener(listener func()) func() {
	if runtime == nil || runtime.services == nil {
		return func() {}
	}
	return runtime.services.Registry().SetChangeListener(listener)
}

// Stream returns immediately. Context normalization happens before asynchronous setup. Setup failures are errors; provider cancellation is aborted, and an established stream owns its terminal event.
func (runtime *ModelRuntime) Stream(ctx context.Context, model *ai.Model, request ai.Context, options ai.StreamOptions) *ai.AssistantMessageEventStream {
	if model != nil && registryOwnsModelBackend(model.Provider) && runtime.services.Registry().GetProvider(model.ProviderMeta.ProviderID) != nil {
		return runtime.services.Registry().NativeModels().Stream(ctx, model, request, options)
	}
	transcript := ai.NormalizeContext(request)
	outer := ai.NewAssistantMessageEventStream()
	ctx = outer.ObservationContext(ctx)
	go runtime.start(ctx, model, transcript, options, outer, false)
	return outer
}

// Complete returns the exact terminal pointer produced by Stream.Result.
func (runtime *ModelRuntime) Complete(ctx context.Context, model *ai.Model, request ai.Context, options ai.StreamOptions) *ai.AssistantMessage {
	return runtime.Stream(ctx, model, request, options).Result()
}

// StreamSimple shares normalization and auth preparation with Stream. Native and caller-owned callbacks receive source options; only registry-built stock API leaves lower simple options.
func (runtime *ModelRuntime) StreamSimple(ctx context.Context, model *ai.Model, request ai.Context, options ai.StreamOptions) *ai.AssistantMessageEventStream {
	if model != nil && registryOwnsModelBackend(model.Provider) && runtime.services.Registry().GetProvider(model.ProviderMeta.ProviderID) != nil {
		return runtime.services.Registry().NativeModels().StreamSimple(ctx, model, request, options)
	}
	transcript := ai.NormalizeContext(request)
	outer := ai.NewAssistantMessageEventStream()
	ctx = outer.ObservationContext(ctx)
	go runtime.start(ctx, model, transcript, options, outer, true)
	return outer
}

// CompleteSimple returns the exact terminal pointer produced by StreamSimple.
func (runtime *ModelRuntime) CompleteSimple(ctx context.Context, model *ai.Model, request ai.Context, options ai.StreamOptions) *ai.AssistantMessage {
	return runtime.StreamSimple(ctx, model, request, options).Result()
}

func (runtime *ModelRuntime) start(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions, outer *ai.AssistantMessageEventStream, simple bool) {
	if ctx == nil {
		runtime.fail(ctx, outer, model, fmt.Errorf("model runtime: nil context"))
		return
	}
	preparedModel, provider, preparedOptions, err := runtime.prepare(ctx, model, options)
	if err != nil {
		failureCtx := ctx
		if !extension.ModelStreamRequestFromContext(ctx).API {
			failureCtx = context.WithoutCancel(ctx)
		}
		runtime.fail(failureCtx, outer, model, err)
		return
	}
	apiRequest := extension.ModelStreamRequestFromContext(ctx).API
	if apiRequest {
		defer func() { _ = provider.Close() }()
	}
	if err := ctx.Err(); err != nil {
		runtime.fail(ctx, outer, preparedModel, err)
		return
	}
	leaf := provider
	attributed, registryBuilt := leaf.(*providerAttributionProvider)
	if registryBuilt {
		leaf = attributed.Provider
	}
	_, registeredCallback := leaf.(*registeredStreamProvider)
	if native, ok := leaf.(*nativeModelProvider); ok {
		native.simple = simple
	} else if simple && registryBuilt && !registeredCallback {
		if preparedOptions.Thinking == "" {
			preparedOptions.Thinking = ai.ThinkingOff
		}
		maxTokens := preparedOptions.MaxTokens
		if maxTokens == 0 {
			maxTokens = preparedModel.Capabilities.MaxOutputTokens
		}
		preparedOptions.MaxTokens = ai.ClampMaxTokensToContext(preparedModel, transcript, maxTokens)
	}
	if registryBuilt {
		if !registeredCallback {
			provider = &apiStreamProvider{Provider: provider, model: preparedModel}
		}
		provider = &composedStreamProvider{Provider: provider, model: preparedModel}
	}
	inner, err := provider.Stream(ctx, transcript, preparedOptions)
	if err != nil {
		runtime.fail(ctx, outer, preparedModel, err)
		return
	}
	if inner == nil {
		runtime.fail(ctx, outer, preparedModel, fmt.Errorf("model runtime: provider %q returned a nil stream", provider.ID()))
		return
	}
	// Pi lazy.ts:forwardStream drains the provider's iterator and awaits its result without observing a partial snapshot.
	if err := outer.ForwardStream(ctx, inner); err != nil {
		runtime.fail(ctx, outer, preparedModel, err)
	}
}

func (runtime *ModelRuntime) prepareRequest(ctx context.Context, model *ai.Model, options ai.StreamOptions) (*ai.Model, ai.Provider, ai.StreamOptions, error) {
	if model == nil {
		return nil, nil, ai.StreamOptions{}, fmt.Errorf("model runtime: model is nil")
	}
	if extension.ModelStreamRequestFromContext(ctx).API {
		return runtime.prepareAPIRequest(ctx, model, options)
	}
	providerID := model.ProviderMeta.ProviderID
	if providerID == "" && model.Provider != nil {
		providerID = model.Provider.ID()
	}
	if providerID == "" || model.Provider == nil {
		return nil, nil, ai.StreamOptions{}, fmt.Errorf("unknown provider: %s", providerID)
	}

	if native := runtime.services.Registry().NativeProvider(providerID); native != nil && registryOwnsModelBackend(model.Provider) {
		return runtime.prepareNativeRequest(ctx, model, options, native)
	}
	requestModel := model
	provider := model.Provider
	configuredHeaders := model.ProviderMeta.Headers
	var resolvedEnv map[string]string
	var nativeAuth *ai.AuthResult
	// Native Go providers expose Pi's auth property through Auth().
	if native, ok := provider.(interface{ Auth() ai.ProviderAuth }); ok {
		overrides := ai.AuthResolutionOverrides{Env: options.Env}
		if options.APIKey != "" {
			overrides.APIKey = &options.APIKey
		}
		resolved, err := ai.ResolveProviderAuth(ctx, providerID, native.Auth(), runtime.services.Credentials(), ai.DefaultProviderAuthContext(), overrides)
		if err != nil {
			return nil, nil, ai.StreamOptions{}, err
		}
		if resolved == nil || (resolved.Auth.APIKey == "" && resolved.Auth.Headers == nil) {
			return nil, nil, ai.StreamOptions{}, &modelRuntimeAuthMissingError{provider: providerID}
		}
		nativeAuth = resolved
		resolvedEnv = resolved.Env
		if resolved.Auth.BaseURL != "" {
			cloned := *model
			cloned.ProviderMeta.BaseURL = resolved.Auth.BaseURL
			requestModel = &cloned
		}
	} else if model.ProviderMeta.ProviderID != "" && registryOwnsModelBackend(provider) {
		overrides := ai.AuthResolutionOverrides{Env: options.Env}
		if options.APIKey != "" {
			overrides.APIKey = &options.APIKey
		}
		registry := runtime.services.Registry()
		if refreshed := runtime.GetModel(providerID, model.ID); refreshed != nil {
			requestModel = refreshed
		}
		auth, err := registry.ResolveRegistryModelAuth(ctx, requestModel, overrides)
		if err != nil {
			return nil, nil, ai.StreamOptions{}, err
		}
		if auth == nil {
			if options.APIKey == "" && modelRuntimeRequiresAuth(providerID) && registry.GetProvider(providerID) == nil && !registry.HasConfiguredAuth(providerID) {
				return nil, nil, ai.StreamOptions{}, &modelRuntimeAuthMissingError{provider: providerID}
			}
			resolved, err := buildModel(providerID+"/"+model.ID, runtime.services, options.APIKey)
			if err != nil {
				return nil, nil, ai.StreamOptions{}, err
			}
			requestModel, provider = resolved, resolved.Provider
			configuredHeaders = resolved.ProviderMeta.Headers
		} else {
			nativeAuth = new(*auth)
			nativeAuth.Auth.Headers = registry.RequestAuthHeaders(requestModel, auth.Auth.Headers)
			resolvedEnv = auth.Env
			entry := icodingagent.NativeModelEntry(requestModel)
			entry.Headers = nil // Auth resolution has already assembled model and provider headers.
			entry.Env = auth.Env
			entry.Insecure = registry.ProviderInsecure(providerID)
			if auth.Auth.BaseURL != "" {
				entry.BaseURL = auth.Auth.BaseURL
				copy := *requestModel
				copy.ProviderMeta.BaseURL = auth.Auth.BaseURL
				requestModel = &copy
			}
			provider, err = buildProviderForEntry(providerID, model.ID, requestModel.ProviderMeta.API, entry, runtime.services, auth.Auth.APIKey, auth.Auth.APIKey == "")
			if err != nil {
				return nil, nil, ai.StreamOptions{}, err
			}
			provider = newProviderAttributionProvider(provider, providerID, entry.BaseURL, func() bool {
				return runtime.services.settings != nil && runtime.services.settings.IsInstallTelemetryEnabled()
			}, nil)
			configuredHeaders = nil
		}
	}

	prepared := options
	prepared.APIKey = ""
	// The selected model can enable reasoning for a custom ID even when the provider's fallback catalog entry does not.
	prepared.IsReasoning = options.IsReasoning || model.ProviderMeta.Reasoning || (model.Capabilities.MaxThinking != "" && model.Capabilities.MaxThinking != ai.ThinkingOff) || requestModel.ProviderMeta.Reasoning || (requestModel.Capabilities.MaxThinking != "" && requestModel.Capabilities.MaxThinking != ai.ThinkingOff)
	prepared.ModelCost = requestModel.CostRates()
	prepared.Headers = mergeRuntimeHeaders(configuredHeaders, options.Headers)
	if nativeAuth != nil {
		prepared.APIKey = nativeAuth.Auth.APIKey
		prepared.Headers = ai.MergeProviderHeaders(nativeAuth.Auth.Headers, prepared.Headers)
	}
	if options.TransformHeaders != nil {
		transformed, err := options.TransformHeaders(ctx, maps.Clone(prepared.Headers))
		if err != nil {
			return nil, nil, ai.StreamOptions{}, err
		}
		prepared.Headers = transformed
	}
	prepared.TransformHeaders = nil
	if len(resolvedEnv) > 0 || len(options.Env) > 0 {
		prepared.Env = make(ai.ProviderEnv, len(resolvedEnv)+len(options.Env))
		maps.Copy(prepared.Env, resolvedEnv)
		maps.Copy(prepared.Env, options.Env)
	}
	return requestModel, provider, prepared, nil
}

type modelRuntimeAuthMissingError struct{ provider string }

func (e *modelRuntimeAuthMissingError) Error() string {
	return "provider is not configured: " + e.provider
}

func mergeRuntimeHeaders(base map[string]string, override ai.ProviderHeaders) ai.ProviderHeaders {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	merged := ai.ProviderHeadersFromStrings(base)
	if merged == nil {
		merged = make(ai.ProviderHeaders)
	}
	for name, value := range override {
		for existing := range merged {
			if strings.EqualFold(existing, name) {
				delete(merged, existing)
			}
		}
		merged[name] = value
	}
	return merged
}

func modelRuntimeRequiresAuth(providerID string) bool {
	switch providerID {
	case "ollama", "amazon-bedrock", "test-faux":
		return false
	default:
		return true
	}
}

func (runtime *ModelRuntime) fail(ctx context.Context, stream *ai.AssistantMessageEventStream, model *ai.Model, err error) {
	reason := ai.StopReasonError
	if ctx != nil && ctx.Err() != nil {
		reason = ai.StopReasonAborted
	}
	message := &ai.AssistantMessage{
		Content:      []ai.AssistantContentBlock{},
		StopReason:   reason,
		ErrorMessage: err.Error(),
		Timestamp:    time.Now().UnixMilli(),
	}
	if model != nil {
		message.API = model.ProviderMeta.API
		message.Provider = model.ProviderMeta.ProviderID
		if message.Provider == "" && model.Provider != nil {
			message.Provider = model.Provider.ID()
		}
		message.Model = model.ID
	}
	_ = stream.Push(ai.ErrorEvent{Reason: reason, Error: message})
}
