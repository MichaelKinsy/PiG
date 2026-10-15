package extension

import (
	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Ports packages/coding-agent/src/core/model-resolver.ts.
// ScopedModel is a resolved member of the session's model scope. An absent thinking level supplies no per-scope override when cycling.
type ScopedModel struct {
	Model         *ai.Model
	ThinkingLevel agent.ThinkingLevel
}

// ScopedModels returns the current read-only scope through the callback captured when this context was created. An unbound callback returns an empty list. Do not mutate the returned slice or models.
// upstream: packages/coding-agent/src/core/extensions/runner.ts:createContext
func (c *Context) ScopedModels() ([]ScopedModel, error) {
	if err := c.assertActive(); err != nil {
		return nil, err
	}
	if c.actions.GetScopedModels == nil {
		return []ScopedModel{}, nil
	}
	return c.actions.GetScopedModels(), nil
}

// ThinkingLevel returns the current thinking level through the callback captured when this context was created. An
// unbound callback returns "", as upstream's optional `thinkingLevel` is absent when the session runtime provides none.
// upstream: packages/coding-agent/src/core/extensions/runner.ts:914 (get thinkingLevel)
func (c *Context) ThinkingLevel() (ThinkingLevel, error) {
	if err := c.assertActive(); err != nil {
		return "", err
	}
	if c.actions.GetThinkingLevel == nil {
		return "", nil
	}
	return c.actions.GetThinkingLevel(), nil
}
