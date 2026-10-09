// Package durableagent holds the pi setup of a coding agent over the pi-durable Harness: the coding registry, the
// Harness settings, the execution environments, the HTTP setup and the initial model.
package durableagent

// Ports packages/coding-agent/src/experimental/durable/harness-setup.ts

import (
	"context"
	"errors"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	nodeenv "github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/tools"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// maxTimerDelayMs is the largest delay a JavaScript timer takes (2^31-1), the stream timeout that stands for a disabled idle timeout.
const maxTimerDelayMs = 2147483647

// ConfigureHarnessHTTP is pi's HTTP setup: proxy and idle timeouts. Without it, some provider streams break off.
func ConfigureHarnessHTTP(settings *codingagent.SettingsManager) error {
	if err := ai.ApplyHTTPProxySettings(settings.GetGlobalSettings().HTTPProxy); err != nil {
		return err
	}
	idle, err := settings.GetHttpIdleTimeoutMs()
	if err != nil {
		return err
	}
	return ai.ConfigureHTTPDispatcher(idle)
}

// CreateHarnessSettings returns the Harness settings, read at every use from pi's settings as loaded at startup. A setting that does not parse fails the read as its getter throws upstream: the read panics, and the Harness fails the operation that made it.
func CreateHarnessSettings(settings *codingagent.SettingsManager) func() *harness.HarnessSettings {
	return func() *harness.HarnessSettings {
		return &harness.HarnessSettings{
			Stream:       streamSettings(settings),
			Compaction:   compactionSettings(settings),
			Retry:        retrySettings(settings),
			SteeringMode: durable.QueueMode(settings.GetSteeringMode()),
			FollowUpMode: durable.QueueMode(settings.GetFollowUpMode()),
		}
	}
}

// streamSettings is the stream getter of createHarnessSettings. An explicit provider timeout wins over the idle timeout, which stands as the largest timer delay when disabled; maxRetries is present only when set.
func streamSettings(settings *codingagent.SettingsManager) *durable.ConversationStreamOptions {
	var provider codingagent.ProviderRetrySettings
	if retry := settings.Get().Retry; retry != nil && retry.Provider != nil {
		provider = *retry.Provider
	}
	idle, err := settings.GetHttpIdleTimeoutMs()
	if err != nil {
		panic(err)
	}
	stream := &durable.ConversationStreamOptions{TimeoutMs: provider.TimeoutMs, MaxRetries: provider.MaxRetries, MaxRetryDelayMs: provider.MaxRetryDelayMs}
	if stream.TimeoutMs == nil {
		stream.TimeoutMs = new(idle)
		if idle == 0 {
			stream.TimeoutMs = new(maxTimerDelayMs)
		}
	}
	if stream.MaxRetryDelayMs == nil {
		stream.MaxRetryDelayMs = new(settings.GetProviderRetrySettings().MaxRetryDelayMs)
	}
	return stream
}

func compactionSettings(settings *codingagent.SettingsManager) *harness.CompactionPolicyPatch {
	compaction, err := settings.GetCompactionSettings()
	if err != nil {
		panic(err)
	}
	return &harness.CompactionPolicyPatch{Enabled: &compaction.Enabled, ReserveTokens: &compaction.ReserveTokens, KeepRecentTokens: &compaction.KeepRecentTokens}
}

func retrySettings(settings *codingagent.SettingsManager) *harness.RetryPolicyPatch {
	retry := settings.GetRetrySettings()
	return &harness.RetryPolicyPatch{Enabled: &retry.Enabled, MaxRetries: &retry.MaxRetries, BaseDelayMs: &retry.BaseDelayMs, MaxAgentDelayMs: &retry.MaxAgentDelayMs}
}

// CreateCodingRegistry is a registry with pi's coding tools and system prompt.
func CreateCodingRegistry(settings *codingagent.SettingsManager, cwd string) (harness.Registry, error) {
	registry := harness.CreateRegistry()
	if err := registry.Install(tools.CodingTools); err != nil {
		return nil, err
	}
	if err := registry.Install(CreatePiPrompt(settings, cwd)); err != nil {
		return nil, err
	}
	return registry, nil
}

// ExecutionEnvs holds one execution environment per directory, shared by every conversation in it.
type ExecutionEnvs struct {
	defaultCwd string
	mu         sync.Mutex
	envs       map[string]*nodeenv.NodeExecutionEnv
	order      []string
}

// NewExecutionEnvs returns environments whose default directory is defaultCwd.
func NewExecutionEnvs(defaultCwd string) *ExecutionEnvs {
	return &ExecutionEnvs{defaultCwd: defaultCwd, envs: map[string]*nodeenv.NodeExecutionEnv{}}
}

// Env is HarnessOptions.Env: the environment of the target's directory, the default directory when the target has none.
func (envs *ExecutionEnvs) Env(_ context.Context, target harness.EnvTarget) (env.ExecutionEnv, error) {
	cwd := envs.defaultCwd
	if target.Cwd != nil {
		cwd = *target.Cwd
	}
	envs.mu.Lock()
	defer envs.mu.Unlock()
	executionEnv, ok := envs.envs[cwd]
	if !ok {
		executionEnv = nodeenv.NewNodeExecutionEnv(nodeenv.NodeExecutionEnvOptions{Cwd: cwd})
		envs.envs[cwd] = executionEnv
		envs.order = append(envs.order, cwd)
	}
	return executionEnv, nil
}

// Cleanup forgets every environment, then cleans each in creation order. The first failure stops the cleaning, as the awaited loop of upstream does.
func (envs *ExecutionEnvs) Cleanup(ctx context.Context) error {
	envs.mu.Lock()
	cleaning := make([]*nodeenv.NodeExecutionEnv, 0, len(envs.order))
	for _, cwd := range envs.order {
		cleaning = append(cleaning, envs.envs[cwd])
	}
	clear(envs.envs)
	envs.order = nil
	envs.mu.Unlock()
	for _, executionEnv := range cleaning {
		if err := executionEnv.Cleanup(ctx); err != nil {
			return err
		}
	}
	return nil
}

// InitialModel is the model and thinking level a new root conversation starts with.
type InitialModel struct {
	Model           *durable.ModelRef
	ThinkingLevel   string
	FallbackMessage string
}

// DefaultModelSettings is the saved default model selection of pi's settings.
type DefaultModelSettings interface {
	GetDefaultProvider() string
	GetDefaultModel() string
	GetDefaultThinkingLevel() ai.ThinkingLevel
}

// FindInitialAgentModel is the model a new root conversation starts with: an explicit --provider/--model, or pi's default resolution (harness-setup.ts:93-124).
func FindInitialAgentModel(selection ModelSelection, settings DefaultModelSettings, provider, model string) (InitialModel, error) {
	if model != "" {
		resolved := codingagent.ResolveCliModel(provider, model, "", selection)
		if resolved.Error != "" || resolved.Model == nil {
			message := resolved.Error
			if message == "" {
				message = model
			}
			return InitialModel{}, errors.New("Could not resolve model: " + message)
		}
		return InitialModel{Model: &durable.ModelRef{Provider: resolved.Model.Provider, ModelId: resolved.Model.ID}, ThinkingLevel: string(resolved.ThinkingLevel)}, nil
	}
	initial, err := codingagent.FindInitialModel(codingagent.FindInitialModelOptions{
		ModelRuntime: selection, IsContinuing: false,
		DefaultProvider: settings.GetDefaultProvider(), DefaultModelId: settings.GetDefaultModel(),
		DefaultThinkingLevel: string(settings.GetDefaultThinkingLevel()),
	})
	if err != nil {
		return InitialModel{}, err
	}
	result := InitialModel{FallbackMessage: initial.FallbackMessage}
	if initial.Model != nil {
		result.Model = &durable.ModelRef{Provider: initial.Model.Provider, ModelId: initial.Model.ID}
		result.ThinkingLevel = initial.ThinkingLevel
	}
	return result, nil
}

// ModelSelection projects only resolver metadata while retaining every native model field through the non-wire attachment. Availability comes from the runtime's own snapshot.
type ModelSelection struct {
	Runtime        *coding.ModelRuntime
	ConfiguredAuth func(string) bool
}

func modelMetadata(model *ai.Model) codingagent.RuntimeModel {
	return codingagent.RuntimeModel{NativeModel: model, Provider: model.ProviderMeta.ProviderID, ID: model.ID, Name: model.DisplayName, Reasoning: model.ProviderMeta.Reasoning, Headers: ai.ProviderHeadersFromStrings(model.ProviderMeta.Headers)}
}

func (view ModelSelection) GetModels(provider string) []codingagent.RuntimeModel {
	result := []codingagent.RuntimeModel{}
	for _, model := range view.Runtime.GetModels() {
		if provider == "" || model.ProviderMeta.ProviderID == provider {
			result = append(result, modelMetadata(model))
		}
	}
	return result
}

func (view ModelSelection) HasConfiguredAuth(provider string) bool {
	return view.ConfiguredAuth(provider)
}

func (view ModelSelection) GetModel(provider, id string) *codingagent.RuntimeModel {
	model := view.Runtime.GetModel(provider, id)
	if model == nil {
		return nil
	}
	return new(modelMetadata(model))
}

func (view ModelSelection) GetAvailableSnapshot() []codingagent.RuntimeModel {
	models := view.Runtime.GetAvailableSnapshot()
	result := make([]codingagent.RuntimeModel, len(models))
	for index, model := range models {
		result[index] = modelMetadata(model)
	}
	return result
}
