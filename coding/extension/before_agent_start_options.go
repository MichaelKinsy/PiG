package extension

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
)

type beforeAgentStartOptionsKey struct{}
type beforeAgentStartOptionsState struct {
	options     *BuildSystemPromptOptions
	rawTools    json.RawMessage
	nativeTools []string
}

// WithBeforeAgentStartOptions attaches the shared per-run options to a handler dispatch. Hosts use this without changing the value-typed BeforeAgentStartEvent.SystemPromptOptions API.
func WithBeforeAgentStartOptions(ctx context.Context, options *BuildSystemPromptOptions) context.Context {
	return context.WithValue(ctx, beforeAgentStartOptionsKey{}, &beforeAgentStartOptionsState{options: options})
}

// BeforeAgentStartOptions returns the shared per-run options during before_agent_start, or nil outside that dispatch. Assign collection replacements through this pointer; edits remain visible to later handlers even when the handler returns an error. The pointer never aliases the Session's base options.
func BeforeAgentStartOptions(ctx context.Context) *BuildSystemPromptOptions {
	state, _ := ctx.Value(beforeAgentStartOptionsKey{}).(*beforeAgentStartOptionsState)
	if state == nil {
		return nil
	}
	return state.options
}

// BeforeAgentStartSelectedTools returns the current wire value, retaining untyped edits until prompt admission. A native collection replacement supersedes the preceding wire value.
func BeforeAgentStartSelectedTools(ctx context.Context) json.RawMessage {
	state, _ := ctx.Value(beforeAgentStartOptionsKey{}).(*beforeAgentStartOptionsState)
	if state == nil {
		return nil
	}
	if state.rawTools != nil && reflect.DeepEqual(state.nativeTools, state.options.SelectedTools) {
		return state.rawTools
	}
	names := state.options.SelectedTools
	if names == nil {
		names = []string{}
	}
	raw, _ := json.Marshal(names)
	return raw
}

// SetBeforeAgentStartSelectedTools retains the complete foreign value for later handlers. Only string entries can name registered Go tools; malformed selections are rejected after the handler chain, not swallowed as handler failures.
func SetBeforeAgentStartSelectedTools(ctx context.Context, raw json.RawMessage) {
	state, _ := ctx.Value(beforeAgentStartOptionsKey{}).(*beforeAgentStartOptionsState)
	if state == nil {
		return
	}
	if len(raw) == 0 {
		raw = json.RawMessage(`null`)
	}
	state.rawTools = slices.Clone(raw)
	names, _, _ := selectedToolNames(raw, nil)
	state.options.SelectedTools = names
	state.nativeTools = slices.Clone(names)
}

// ResolveBeforeAgentStartSelectedTools compares the final selection before filtering non-string registry misses, as AgentSession.prompt does. Invalid lists reject prompt admission after every handler has had a chance to repair them.
func ResolveBeforeAgentStartSelectedTools(ctx context.Context, before []string) ([]string, bool, error) {
	state, _ := ctx.Value(beforeAgentStartOptionsKey{}).(*beforeAgentStartOptionsState)
	if state != nil && (state.rawTools == nil || !reflect.DeepEqual(state.nativeTools, state.options.SelectedTools)) {
		return state.options.SelectedTools, !slices.Equal(state.options.SelectedTools, before), nil
	}
	return selectedToolNames(BeforeAgentStartSelectedTools(ctx), before)
}

func selectedToolNames(raw json.RawMessage, before []string) ([]string, bool, error) {
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, true, errors.New("systemPromptOptions.selectedTools must be an array")
	}
	if values == nil {
		return nil, true, errors.New("Cannot read properties of null (reading 'length')")
	}
	names := make([]string, 0, len(values))
	edited := len(values) != len(before)
	for i, value := range values {
		var name string
		if len(value) == 0 || value[0] != '"' || json.Unmarshal(value, &name) != nil {
			edited = true
			continue
		}
		names = append(names, name)
		if i >= len(before) || name != before[i] {
			edited = true
		}
	}
	return names, edited, nil
}
