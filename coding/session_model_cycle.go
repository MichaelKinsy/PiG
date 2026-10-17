package coding

// Ports packages/coding-agent/src/core/agent-session.ts.

import (
	"context"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// CycleModel selects the next available model. Empty direction means forward; only Persist changes global defaults.
func (s *Session) CycleModel(direction string, options ...ModelMutationOptions) (*ModelCycleResult, error) {
	result, complete, err := s.BeginModelCycle(context.Background(), direction, options...)
	if complete != nil {
		complete()
	}
	if result != nil {
		// agent-session.ts:2567 reads thinkingLevel after awaiting model_select, so a handler's change shows.
		result.ThinkingLevel = s.ThinkingLevel()
	}
	return result, err
}

// BeginModelCycle is [Session.CycleModel] split at its await: it applies the model and thinking change, returns the result, and returns the model_select notification as a completion the caller invokes once, or nil when none is pending. RPC admits its next command after this prefix. A nil result means there is nothing to cycle to.
func (s *Session) BeginModelCycle(ctx context.Context, direction string, options ...ModelMutationOptions) (*ModelCycleResult, func(), error) {
	return s.beginModelCycle(ctx, direction, false, options...)
}

// BeginModelCycleOnMain is [Session.BeginModelCycle] for an owner loop, as [Session.SetModelOnMain] is for SetModel: the model and
// thinking change apply on the caller, and the completion runs the thinking_level_select and model_select notifications, which admit
// extension handlers and must not run on the loop. The completion then records in the result the thinking level a handler left
// (agent-session.ts:2567). The caller invokes a non-nil completion once, off the loop. A nil result means there is nothing to cycle to.
// The notifications end when ctx or the Session ends, as [Session.SetModelOnMain]'s do.
func (s *Session) BeginModelCycleOnMain(ctx context.Context, direction string, options ModelMutationOptions) (*ModelCycleResult, func(), error) {
	ctx, release := s.notificationContext(ctx)
	result, complete, err := s.beginModelCycle(ctx, direction, true, options)
	if result == nil || err != nil {
		release()
		return result, complete, err
	}
	return result, func() {
		defer release()
		if complete != nil {
			complete()
		}
		result.ThinkingLevel = s.ThinkingLevel()
	}, nil
}

func (s *Session) beginModelCycle(ctx context.Context, direction string, deferThinking bool, options ...ModelMutationOptions) (*ModelCycleResult, func(), error) {
	if direction != "" && direction != "forward" && direction != "backward" {
		return nil, nil, fmt.Errorf("unknown model cycle direction %q", direction)
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
		return nil, nil, nil
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
	var explicit *ai.ModelThinkingLevel
	if next.ThinkingLevel != "" {
		explicit = new(next.ThinkingLevel)
	}
	complete, err := s.beginModelChangeState(ctx, next.Model, extension.ModelSelectSourceCycle, explicit, deferThinking, options...)
	if err != nil {
		return nil, nil, err
	}
	return &ModelCycleResult{Model: next.Model, ThinkingLevel: s.ThinkingLevel(), IsScoped: isScoped}, complete, nil
}

// CycleThinkingLevel advances through supported levels, returning an empty level for a non-reasoning model.
func (s *Session) CycleThinkingLevel(options ...ModelMutationOptions) (ai.ModelThinkingLevel, error) {
	if !s.SupportsThinking() {
		return "", nil
	}
	levels := s.AvailableThinkingLevels()
	next := levels[(slices.Index(levels, s.ThinkingLevel())+1)%len(levels)]
	if err := s.SetThinkingLevel(next, options...); err != nil {
		return "", err
	}
	return next, nil
}
