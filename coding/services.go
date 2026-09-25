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
	// Empty → os.Getwd().
	CWD string

	// AgentDir is the global pig config directory (typically
	// ~/.pig/agent). Empty → DefaultAgentDir().
	AgentDir string

	// ProjectTrusted controls whether project-local settings are loaded. nil
	// preserves the SDK default of trusted project settings.
	ProjectTrusted *bool
}

// NewServices constructs a Services container. Failure modes:
//
//   - cannot resolve CWD when none provided → wrapped os.Getwd error
//   - auth.json directory cannot be created/read → wrapped error
//
// Settings and registry constructors never fail; they fall back to
// defaults if their backing files are missing or malformed.
func NewServices(opts ServicesOptions) (*Services, error) {
	cwd := opts.CWD
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
	sm := icodingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, projectTrusted)
	reg := icodingagent.NewModelRegistry(agentDir)
	reg.SetAuthStorage(auth)
	// Mirrors ModelRuntime.create: restore stored dynamic catalogs (Radius)
	// without network access. Network refreshes run from the modes.
	reg.RefreshCatalogs(context.Background(), icodingagent.CatalogRefreshOptions{})

	services := &Services{
		cwd:      cwd,
		agentDir: agentDir,
		auth:     auth,
		settings: sm,
		testFauxProvider: sync.OnceValue(func() *ai.TestFauxProvider {
			return &ai.TestFauxProvider{Scenario: os.Getenv("PIG_TEST_FAUX_SCENARIO")}
		}),
	}
	modelRuntime, err := NewModelRuntime(services)
	if err != nil {
		return nil, err
	}
	services.modelRuntime = modelRuntime
	services.registry = &ModelRegistry{ModelRegistry: reg, runtime: modelRuntime}
	return services, nil
}

// CWD returns the working directory this Services container was
// constructed with.
func (s *Services) CWD() string { return s.cwd }

// AgentDir returns the global agent config directory (typically
// ~/.pig/agent or $PIG_HOME/agent).
func (s *Services) AgentDir() string { return s.agentDir }

// Auth returns the credential storage backing auth.json. Read-mostly;
// safe to share across goroutines (file lock serialises writes).
func (s *Services) Auth() *ai.AuthStorage { return s.auth }

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
	services *Services
	prepare  func(context.Context, *ai.Model, ai.StreamOptions) (*ai.Model, ai.Provider, ai.StreamOptions, error)
}

// NewModelRuntime constructs a runtime backed by services model configuration
// and credentials.
func NewModelRuntime(services *Services) (*ModelRuntime, error) {
	if services == nil {
		return nil, ErrNoServices
	}
	runtime := &ModelRuntime{services: services}
	runtime.prepare = runtime.prepareRequest
	return runtime, nil
}

// GetModels returns metadata snapshots for the complete static and explicitly registered model catalog.
func (runtime *ModelRuntime) GetModels() []*ai.Model {
	if runtime == nil || runtime.services == nil {
		return nil
	}
	generated := ai.ListModels("")
	models := make([]*ai.Model, 0, len(generated)+len(runtime.services.Registry().GetAll()))
	indices := make(map[string]int, cap(models))
	for i := range generated {
		if !runtime.services.Registry().HasGeneratedModel(generated[i].Provider, generated[i].ID) {
			continue
		}
		entry := runtime.services.Registry().ResolveGeneratedModel(generated[i].Provider, generated[i].ID, &generated[i])
		model := modelFromEntry(entry, nil)
		key := model.ProviderMeta.ProviderID + "\x00" + model.ID
		indices[key] = len(models)
		models = append(models, model)
	}
	for _, entry := range runtime.services.Registry().GetAll() {
		key := entry.ProviderID + "\x00" + entry.ModelID
		model, err := BuildModel(entry.ProviderID+"/"+entry.ModelID, runtime.services)
		if err != nil {
			continue
		}
		if index, exists := indices[key]; exists {
			models[index] = model
			continue
		}
		indices[key] = len(models)
		models = append(models, model)
	}
	return models
}

// GetModel returns a registered model by exact provider and model ID without synthesizing unknown identities.
func (runtime *ModelRuntime) GetModel(providerID, modelID string) *ai.Model {
	if runtime == nil || runtime.services == nil || providerID == "" || modelID == "" {
		return nil
	}
	_, generated := ai.LookupModelExact(providerID + "/" + modelID)
	if generated && !runtime.services.Registry().HasGeneratedModel(providerID, modelID) {
		return nil
	}
	if !generated && !runtime.services.Registry().HasModelDefinition(providerID, modelID) {
		return nil
	}
	model, err := BuildModel(providerID+"/"+modelID, runtime.services)
	if err != nil {
		return nil
	}
	return model
}

// SetChangeListener replaces the callback invoked after committed registry changes and returns a draining detach function.
func (runtime *ModelRuntime) SetChangeListener(listener func()) func() {
	if runtime == nil || runtime.services == nil {
		return func() {}
	}
	return runtime.services.Registry().SetChangeListener(listener)
}

// Stream returns immediately. Context normalization happens before asynchronous
// setup; setup and auth failures terminate the returned stream with ErrorEvent.
func (runtime *ModelRuntime) Stream(ctx context.Context, model *ai.Model, request ai.Context, options ai.StreamOptions) *ai.AssistantMessageEventStream {
	transcript := ai.NormalizeContext(request)
	outer := ai.NewAssistantMessageEventStream()
	go runtime.start(ctx, model, transcript, options, outer)
	return outer
}

// Complete returns the exact terminal pointer produced by Stream.Result.
func (runtime *ModelRuntime) Complete(ctx context.Context, model *ai.Model, request ai.Context, options ai.StreamOptions) *ai.AssistantMessage {
	return runtime.Stream(ctx, model, request, options).Result()
}

// StreamSimple uses the same normalization and request preparation path as
// Stream. Go providers expose one typed Stream contract, so provider-neutral
// options lower directly into ai.StreamOptions.
func (runtime *ModelRuntime) StreamSimple(ctx context.Context, model *ai.Model, request ai.Context, options ai.StreamOptions) *ai.AssistantMessageEventStream {
	return runtime.Stream(ctx, model, request, options)
}

// CompleteSimple returns the exact terminal pointer produced by StreamSimple.
func (runtime *ModelRuntime) CompleteSimple(ctx context.Context, model *ai.Model, request ai.Context, options ai.StreamOptions) *ai.AssistantMessage {
	return runtime.StreamSimple(ctx, model, request, options).Result()
}

func (runtime *ModelRuntime) start(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions, outer *ai.AssistantMessageEventStream) {
	if ctx == nil {
		runtime.fail(outer, model, fmt.Errorf("model runtime: nil context"))
		return
	}
	preparedModel, provider, preparedOptions, err := runtime.prepare(ctx, model, options)
	if err != nil {
		runtime.fail(outer, model, err)
		return
	}
	if err := ctx.Err(); err != nil {
		runtime.fail(outer, preparedModel, err)
		return
	}
	inner, err := provider.Stream(ctx, transcript, preparedOptions)
	if err != nil {
		runtime.fail(outer, preparedModel, err)
		return
	}
	if inner == nil {
		runtime.fail(outer, preparedModel, fmt.Errorf("model runtime: provider %q returned a nil stream", provider.ID()))
		return
	}
	for event := range inner.Events(ctx) {
		if err := outer.Push(event); err != nil {
			runtime.fail(outer, preparedModel, err)
			return
		}
		switch event.(type) {
		case ai.DoneEvent, ai.ErrorEvent:
			return
		}
	}
	if err := ctx.Err(); err != nil {
		runtime.fail(outer, preparedModel, err)
		return
	}
	runtime.fail(outer, preparedModel, fmt.Errorf("model runtime: provider %q stream ended without a terminal event", provider.ID()))
}

func (runtime *ModelRuntime) prepareRequest(ctx context.Context, model *ai.Model, options ai.StreamOptions) (*ai.Model, ai.Provider, ai.StreamOptions, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, ai.StreamOptions{}, err
	}
	if model == nil {
		return nil, nil, ai.StreamOptions{}, fmt.Errorf("model runtime: model is nil")
	}
	providerID := model.ProviderMeta.ProviderID
	if providerID == "" && model.Provider != nil {
		providerID = model.Provider.ID()
	}
	if providerID == "" || model.Provider == nil {
		return nil, nil, ai.StreamOptions{}, fmt.Errorf("unknown provider: %s", providerID)
	}

	requestModel := model
	provider := model.Provider
	configuredHeaders := model.ProviderMeta.Headers
	var resolvedEnv map[string]string
	if model.ProviderMeta.ProviderID != "" {
		if modelRuntimeRequiresAuth(providerID) && !runtime.services.Registry().HasConfiguredAuth(providerID) {
			return nil, nil, ai.StreamOptions{}, fmt.Errorf("provider is not configured: %s", providerID)
		}
		resolved, err := BuildModel(providerID+"/"+model.ID, runtime.services)
		if err != nil {
			return nil, nil, ai.StreamOptions{}, err
		}
		requestModel = resolved
		provider = resolved.Provider
		configuredHeaders = resolved.ProviderMeta.Headers
		if entry, ok := runtime.services.Registry().Resolve(providerID, model.ID); ok {
			resolvedEnv = entry.Env
		}
	}

	prepared := options
	prepared.ModelCost = requestModel.CostRates()
	prepared.Headers = mergeRuntimeHeaders(configuredHeaders, options.Headers)
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
	if err := ctx.Err(); err != nil {
		return nil, nil, ai.StreamOptions{}, err
	}
	return requestModel, provider, prepared, nil
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

func (runtime *ModelRuntime) fail(stream *ai.AssistantMessageEventStream, model *ai.Model, err error) {
	message := &ai.AssistantMessage{
		Content:      []ai.AssistantContentBlock{},
		StopReason:   ai.StopReasonError,
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
	_ = stream.Push(ai.ErrorEvent{Reason: ai.StopReasonError, Error: message})
}
