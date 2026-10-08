package coding

// Ports packages/coding-agent/src/core/agent-session.ts.

import (
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// CycleModel selects the next available model. Empty direction means forward; only Persist changes global defaults.
func (s *Session) CycleModel(direction string, options ...ModelMutationOptions) (*ModelCycleResult, error) {
	if direction != "" && direction != "forward" && direction != "backward" {
		return nil, fmt.Errorf("unknown model cycle direction %q", direction)
	}
	available := s.modelRuntime.GetAvailableSnapshot()
	scoped := s.ScopedModels()
	isScoped := len(scoped) > 0
	candidates := make([]ScopedModel, 0, len(available))
	if isScoped {
		ids := make(map[string]bool, len(available))
		for _, model := range available {
			ids[providerID(model)+"\x00"+model.ID] = true
		}
		for _, entry := range scoped {
			if entry.Model != nil && ids[providerID(entry.Model)+"\x00"+entry.Model.ID] {
				candidates = append(candidates, entry)
			}
		}
	} else {
		for _, model := range available {
			candidates = append(candidates, ScopedModel{Model: model})
		}
	}
	if len(candidates) <= 1 {
		return nil, nil
	}
	current := s.Model()
	index := max(slices.IndexFunc(candidates, func(entry ScopedModel) bool {
		return current != nil && entry.Model.ID == current.ID && providerID(entry.Model) == providerID(current)
	}), 0)
	delta := 1
	if direction == "backward" {
		delta = -1
	}
	next := candidates[(index+delta+len(candidates))%len(candidates)]
	var explicit *ai.ThinkingLevel
	if next.ThinkingLevel != "" {
		explicit = new(next.ThinkingLevel)
	}
	if err := s.setModelWithThinking(next.Model, extension.ModelSelectSourceCycle, explicit, options...); err != nil {
		return nil, err
	}
	return &ModelCycleResult{Model: next.Model, ThinkingLevel: s.ThinkingLevel(), IsScoped: isScoped}, nil
}

// CycleThinkingLevel advances through supported levels, returning an empty level for a non-reasoning model.
func (s *Session) CycleThinkingLevel(options ...ModelMutationOptions) (ai.ThinkingLevel, error) {
	model := s.Model()
	if model == nil || model.Capabilities.MaxThinking == "" && !model.ProviderMeta.Reasoning {
		return "", nil
	}
	levels := s.AvailableThinkingLevels()
	next := levels[(slices.Index(levels, s.ThinkingLevel())+1)%len(levels)]
	if err := s.SetThinkingLevel(next, options...); err != nil {
		return "", err
	}
	return next, nil
}
