package codingagent

import (
	"context"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
)

const anthropicSubscriptionAuthWarning = "Anthropic subscription auth is active. " +
	"Third-party harness usage draws from extra usage and is billed per token, not your Claude plan limits. " +
	"Manage extra usage at https://claude.ai/settings/usage. Disable this warning in /settings."

func anthropicExtraUsageWarningEnabled(sm *SettingsManager) bool {
	if sm == nil {
		return false
	}
	warnings := sm.GetWarnings()
	return warnings.AnthropicExtraUsage
}

// isAnthropicSubscriptionAuthKey mirrors upstream (interactive-mode.ts:158).
func isAnthropicSubscriptionAuthKey(apiKey string) bool {
	return strings.HasPrefix(apiKey, "sk-ant-oat")
}

// maybeWarnAboutAnthropicSubscriptionAuth mirrors upstream
// (interactive-mode.ts:5101-5128). The blocking form is used by tests like
// upstream's awaited method; production starts it through the owned async
// wrapper below.
func (m *InteractiveMode) maybeWarnAboutAnthropicSubscriptionAuth(ctx context.Context) {
	if !m.canWarnAboutAnthropicSubscriptionAuth() || !m.hasAnthropicSubscriptionAuth(ctx) {
		return
	}
	m.anthropicSubWarningShown = true
	m.showWarning(anthropicSubscriptionAuthWarning)
}

func (m *InteractiveMode) canWarnAboutAnthropicSubscriptionAuth() bool {
	if m.anthropicSubWarningShown || !anthropicExtraUsageWarningEnabled(m.opts.SettingsManager) || m.opts.RequestAuthRuntime == nil {
		return false
	}
	model := m.opts.Model
	return model != nil && model.Provider != nil && model.Provider.ID() == "anthropic"
}

func (m *InteractiveMode) hasAnthropicSubscriptionAuth(ctx context.Context) bool {
	check, err := m.opts.RequestAuthRuntime.CheckAuth(ctx, "anthropic")
	if err != nil {
		return false
	}
	if check != nil && check.Type == ai.CredentialOAuth {
		return true
	}
	resolved, err := m.opts.RequestAuthRuntime.GetAuth(ctx, "anthropic", ai.AuthResolutionOverrides{})
	return err == nil && resolved != nil && isAnthropicSubscriptionAuthKey(resolved.Auth.APIKey)
}

func (m *InteractiveMode) maybeWarnAboutAnthropicSubscriptionAuthAsync() {
	if !m.canWarnAboutAnthropicSubscriptionAuth() || m.backgroundCtx == nil {
		return
	}
	ctx := m.backgroundCtx
	m.backgroundTasks.Go(func() {
		if !m.hasAnthropicSubscriptionAuth(ctx) || ctx.Err() != nil {
			return
		}
		m.runOnMain(ctx, func() {
			if ctx.Err() == nil && m.canWarnAboutAnthropicSubscriptionAuth() {
				m.anthropicSubWarningShown = true
				m.showWarning(anthropicSubscriptionAuthWarning)
			}
		})
	})
}
