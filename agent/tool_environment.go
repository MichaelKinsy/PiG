// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
)

// ToolEnvironment is the session state a tool may expose to the processes it
// starts. Upstream's bash tool exports it as PI_SESSION_ID, PI_SESSION_FILE,
// PI_PROVIDER, PI_MODEL, and PI_REASONING_LEVEL. Empty fields are not exported.
// Image metadata supplies the selected model profile to read tools.
type ToolEnvironment struct {
	SessionID      string
	SessionFile    string
	Provider       string
	Model          string
	ThinkingLevel  string
	InputLimits    *ai.ModelInputLimits
	SupportsImages *bool
}

type toolEnvironmentKey struct{}

// WithToolEnvironment returns ctx carrying env for the tools it executes.
func WithToolEnvironment(ctx context.Context, env ToolEnvironment) context.Context {
	return context.WithValue(ctx, toolEnvironmentKey{}, env)
}

// ToolEnvironmentFrom returns the environment the agent attached to a tool call.
func ToolEnvironmentFrom(ctx context.Context) (ToolEnvironment, bool) {
	env, ok := ctx.Value(toolEnvironmentKey{}).(ToolEnvironment)
	return env, ok
}

// toolEnvironment reports the session state for tools, as upstream's tool context does.
func (a *Agent) toolEnvironment(model *ai.Model, thinking ai.ThinkingLevel) ToolEnvironment {
	env := ToolEnvironment{SessionID: a.opts.SessionID, ThinkingLevel: string(thinking)}
	if a.opts.SessionFile != nil {
		env.SessionFile = a.opts.SessionFile()
	}
	if model != nil {
		env.Model = model.ID
		env.InputLimits = model.InputLimits.Clone()
		supportsImages := model.Capabilities.SupportsImages
		if model.Input != nil {
			supportsImages = slices.Contains(model.Input, "image")
		}
		env.SupportsImages = &supportsImages
		if model.Provider != nil {
			env.Provider = model.Provider.ID()
		}
	}
	return env
}
