package runtime

import (
	"context"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/ai"
)

// Ports packages/agent/src/harness/runtime/harness.ts (getConfig, setConfig).

func defaultProviderMessages(_ context.Context, messages []agent.AgentMessage) ([]ai.Message, error) {
	return harness.ConvertToLlm(messages), nil
}

func getConfig[T any](owner *Harness, selectValue func(Config) T) (T, error) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	var zero T
	if owner.faultError != nil {
		return zero, owner.faultError
	}
	if owner.closedError != nil {
		return zero, owner.closedError
	}
	return selectValue(owner.config), nil
}

func setConfig[T any](ctx context.Context, owner *Harness, property string, value T, selectField func(*Config) *T, carriesValue bool) error {
	owner.mu.Lock()
	if owner.faultError != nil {
		err := owner.faultError
		owner.mu.Unlock()
		return err
	}
	if owner.closedError != nil {
		err := owner.closedError
		owner.mu.Unlock()
		return err
	}
	next := owner.config
	previous := *selectField(&next)
	*selectField(&next) = value
	owner.config = next
	payload := agentharness.ConfigUpdatePayload{Property: property}
	if carriesValue {
		payload.Previous, payload.Value = previous, value
	}
	delivery := owner.events.PrepareBatch(ctx, []agentharness.HarnessEvent{{Payload: payload}})
	owner.mu.Unlock()
	delivery()
	return nil
}

// GetTools returns the current tool definitions.
func (owner *Harness) GetTools(context.Context) ([]harness.AgentHarnessTool, error) {
	return getConfig(owner, func(config Config) []harness.AgentHarnessTool { return config.Tools })
}

// SetTools validates unique names, replaces the definitions and delivers config_update.
func (owner *Harness) SetTools(ctx context.Context, tools []harness.AgentHarnessTool) error {
	if err := harness.ValidateToolNames(tools); err != nil {
		return err
	}
	return setConfig(ctx, owner, agentharness.ConfigTools, tools, func(config *Config) *[]harness.AgentHarnessTool { return &config.Tools }, false)
}

// GetResources returns the current skills and templates.
func (owner *Harness) GetResources(context.Context) (agentharness.Resources, error) {
	return getConfig(owner, func(config Config) agentharness.Resources { return config.Resources })
}

// SetResources replaces skills and templates and delivers config_update.
func (owner *Harness) SetResources(ctx context.Context, resources agentharness.Resources) error {
	return setConfig(ctx, owner, agentharness.ConfigResources, resources, func(config *Config) *agentharness.Resources { return &config.Resources }, false)
}

// GetStreamOptions returns the current provider request options.
func (owner *Harness) GetStreamOptions(context.Context) (harness.AgentHarnessStreamOptions, error) {
	return getConfig(owner, func(config Config) harness.AgentHarnessStreamOptions { return config.StreamOptions })
}

// SetStreamOptions replaces request options and publishes their previous and current values.
func (owner *Harness) SetStreamOptions(ctx context.Context, options harness.AgentHarnessStreamOptions) error {
	return setConfig(ctx, owner, agentharness.ConfigStreamOptions, options, func(config *Config) *harness.AgentHarnessStreamOptions { return &config.StreamOptions }, true)
}

// GetRetryPolicy returns the current retry policy.
func (owner *Harness) GetRetryPolicy(context.Context) (ai.RetryPolicy, error) {
	return getConfig(owner, func(config Config) ai.RetryPolicy { return config.RetryPolicy })
}

// SetRetryPolicy validates and replaces the retry policy, then delivers config_update.
func (owner *Harness) SetRetryPolicy(ctx context.Context, policy ai.RetryPolicy) error {
	if err := harness.ValidateRetryPolicy(policy); err != nil {
		return err
	}
	return setConfig(ctx, owner, agentharness.ConfigRetryPolicy, policy, func(config *Config) *ai.RetryPolicy { return &config.RetryPolicy }, true)
}

// GetCompactionSettings returns the current compaction settings.
func (owner *Harness) GetCompactionSettings(context.Context) (harness.CompactionSettings, error) {
	return getConfig(owner, func(config Config) harness.CompactionSettings { return config.Compaction })
}

// SetCompactionSettings validates and replaces compaction settings, then delivers config_update.
func (owner *Harness) SetCompactionSettings(ctx context.Context, settings harness.CompactionSettings) error {
	if err := harness.ValidateCompactionSettings(settings); err != nil {
		return err
	}
	return setConfig(ctx, owner, agentharness.ConfigCompactionSettings, settings, func(config *Config) *harness.CompactionSettings { return &config.Compaction }, true)
}

// GetSteeringMode returns the current steering selection mode.
func (owner *Harness) GetSteeringMode(context.Context) (agent.QueueMode, error) {
	return getConfig(owner, func(config Config) agent.QueueMode { return config.SteeringMode })
}

// SetSteeringMode replaces the steering mode and delivers config_update.
func (owner *Harness) SetSteeringMode(ctx context.Context, mode agent.QueueMode) error {
	return setConfig(ctx, owner, agentharness.ConfigSteeringMode, mode, func(config *Config) *agent.QueueMode { return &config.SteeringMode }, true)
}

// GetFollowUpMode returns the current follow-up selection mode.
func (owner *Harness) GetFollowUpMode(context.Context) (agent.QueueMode, error) {
	return getConfig(owner, func(config Config) agent.QueueMode { return config.FollowUpMode })
}

// SetFollowUpMode replaces the follow-up mode and delivers config_update.
func (owner *Harness) SetFollowUpMode(ctx context.Context, mode agent.QueueMode) error {
	return setConfig(ctx, owner, agentharness.ConfigFollowUpMode, mode, func(config *Config) *agent.QueueMode { return &config.FollowUpMode }, true)
}
