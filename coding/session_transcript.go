package coding

import (
	"context"
	"slices"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
)

// restoreAgentMessages retains the current instruction baseline when a session branch or compaction context contains only conversation entries.
func restoreAgentMessages(a *agent.Agent, messages []agent.AgentMessage) {
	a.SetMessages(withInstructionBaseline(a.Messages(), messages))
}

// withInstructionBaseline supplies the legacy instruction baseline only for conversation-only projections. A retained system entry keeps its projected position, including after a compaction summary.
func withInstructionBaseline(current, messages []agent.AgentMessage) []agent.AgentMessage {
	if len(messages) == 0 || slices.ContainsFunc(messages, func(message agent.AgentMessage) bool { return message.System != nil }) {
		return messages
	}
	var systems []ai.Message
	for _, m := range current {
		if m.System != nil {
			systems = append(systems, *m.System)
		}
	}
	if baseline := ai.GetCurrentSystemMessage(systems); baseline != nil {
		messages = append([]agent.AgentMessage{{System: baseline}}, messages...)
	}
	return messages
}

// preparePrompt validates the Session's native model and applies its instruction
// baseline when the first user prompt runs. The agent adds active tool declarations.
func (s *Session) preparePrompt(_ context.Context, messages []agent.AgentMessage) ([]agent.AgentMessage, error) {
	if err := s.validatePromptModel(); err != nil {
		return nil, err
	}
	messages = append(messages, s.takeAgentStartMessages()...)
	if !slices.ContainsFunc(messages, func(message agent.AgentMessage) bool { return message.User != nil }) {
		return messages, nil
	}
	update, err := s.promptSectionUpdate(append(s.agent.Messages(), messages...))
	if err != nil {
		return nil, err
	}
	if update != nil {
		return append([]agent.AgentMessage{{System: update}}, messages...), nil
	}
	return messages, nil
}

// promptSectionUpdate records changed instructions before the next request without persisting a forced prompt projection.
func (s *Session) promptSectionUpdate(messages []agent.AgentMessage) (*ai.SystemMessage, error) {
	s.toolRegistryMu.RLock()
	defer s.toolRegistryMu.RUnlock()
	var systems []ai.Message
	for _, message := range messages {
		if message.System != nil {
			systems = append(systems, *message.System)
		}
	}
	current := ai.GetCurrentSystemMessage(systems)
	run := s.runSystemSections.Load()
	if current != nil && len(current.Sections) == 0 && !s.structuredSystemPrompt && (run == nil || len(*run) == 0) {
		return nil, nil
	}
	var previous ai.OrderedSections
	if current != nil {
		previous = current.Sections
	}
	desired, err := s.effectiveSystemSections()
	if err != nil {
		return nil, err
	}
	patch := prompts.DiffSystemPromptSections(previous, desired)
	if len(patch) == 0 {
		return nil, nil
	}
	return &ai.SystemMessage{Content: ai.SystemText(""), Sections: patch, Timestamp: time.Now().UnixMilli()}, nil
}

// effectiveSystemSections reads the run's live loadout without changing the base prompt. The caller holds toolRegistryMu.
func (s *Session) effectiveSystemSections() (ai.OrderedSections, error) {
	desired := s.baseSystemSections
	if run := s.runSystemSections.Load(); run != nil {
		if s.structuredSystemPrompt {
			desired = cloneSystemSections(desired)
			tools := s.buildToolSystemPromptSections(s.ActiveToolNames())
			for i, section := range desired {
				if section.Name != "tools" && section.Name != "rules" {
					continue
				}
				for _, replacement := range tools {
					if section.Name == replacement.Name {
						desired[i] = replacement
						break
					}
				}
			}
		}
		return prompts.ApplyCustomSystemPromptSections(desired, *run)
	}
	return desired, nil
}

// SystemPrompt returns the effective run prompt while running and the base prompt between runs, independently of the retained active loadout.
func (s *Session) SystemPrompt() string {
	return s.systemPrompt()
}

// validatePromptModel keeps native Session dispatch distinct from a bare Agent's
// injectable stream function, which can consume a descriptor without a runtime.
func (s *Session) validatePromptModel() error {
	if model := s.Model(); model == nil || model.Provider == nil {
		return agent.ErrNoModelSelected
	}
	return nil
}

func (s *Session) systemPrompt() string {
	if prompt, present := s.agent.SystemPromptOverride(); present {
		return prompt
	}
	if s.runSystemSections.Load() != nil {
		s.toolRegistryMu.RLock()
		sections, err := s.effectiveSystemSections()
		s.toolRegistryMu.RUnlock()
		if err == nil {
			return ai.GetCurrentSystemPrompt([]ai.Message{ai.SystemMessage{Sections: sections}})
		}
	}
	if baseline := s.baseSystemPrompt.Load(); baseline != nil {
		return *baseline
	}
	return ""
}

func cloneSystemSections(sections ai.OrderedSections) ai.OrderedSections {
	out := slices.Clone(sections)
	for i, section := range out {
		if section.Value != nil {
			out[i].Value = new(*section.Value)
		}
	}
	return out
}

func (s *Session) initSystemPrompt(opts SessionOptions) {
	if opts.SystemPromptResources != nil {
		s.systemPromptResources.Store(new(*opts.SystemPromptResources))
	}
	s.structuredSystemPrompt = opts.SystemPromptSections != nil || opts.SystemPrompt == ""
	switch {
	case opts.SystemPromptSections != nil:
		s.baseSystemSections = cloneSystemSections(opts.SystemPromptSections)
	case opts.SystemPrompt != "":
		s.baseSystemSections = ai.OrderedSections{{Name: "preamble", Value: new(opts.SystemPrompt)}}
	default:
		s.defaultSystemPrompt = true
		names := make([]string, 0, len(s.agent.Tools()))
		for _, tool := range s.agent.Tools() {
			names = append(names, tool.Name())
		}
		s.baseSystemSections = s.buildToolSystemPromptSections(names)
	}
	s.baseSystemPrompt.Store(new(ai.GetCurrentSystemPrompt([]ai.Message{ai.SystemMessage{Sections: s.baseSystemSections}})))
	s.rebuildSystemPrompt(s.ActiveToolNames())
}

// projectedContext returns the canonical Session projection as the agent's
// loop context, keeping the current instruction baseline.
func (s *Session) projectedContext() []agent.AgentMessage {
	return withInstructionBaseline(s.agent.Messages(), s.inner.BuildSessionProjection().Messages)
}

// prepareRequest re-projects the Session before every provider request, so
// context edits and compactions written during a run reach the next request
// (agent-session.ts _installAgentRequestProjection).
func (s *Session) prepareRequest(_ context.Context, _ agent.PrepareRequestContext) *agent.AgentRequestUpdate {
	thinking := s.agent.ThinkingLevel()
	return &agent.AgentRequestUpdate{Context: s.projectedContext(), Model: s.agent.Model(), ThinkingLevel: &thinking}
}

// prepareNextTurn compacts before the next assistant response of a run when
// the projected context crosses the threshold, then continues from the
// projection (agent-session.ts _installAgentNextTurnRefresh and
// _compactBeforeNextAssistantResponse). It runs on the agent goroutine inside
// Send, which holds s.mu.
func (s *Session) prepareNextTurn(ctx context.Context, _ agent.PrepareNextTurnContext) (*agent.AgentLoopTurnUpdate, error) {
	if model := s.Model(); model != nil && model.Capabilities.ContextWindow > 0 {
		projection := s.inner.BuildSessionProjection()
		tokens := compaction.EstimateProjectedContextTokens(projection, s.currentBranch()).Tokens
		settings, err := s.compactionSettings()
		if err != nil {
			return nil, err
		}
		if compaction.ShouldCompact(tokens, model.Capabilities.ContextWindow, settings) {
			if _, err := s.runAutoCompaction(ctx, "threshold", false); err != nil {
				return nil, err
			}
		}
	}
	thinking := s.agent.ThinkingLevel()
	projected := s.projectedContext()
	update := &agent.AgentLoopTurnUpdate{Context: projected, Model: s.agent.Model(), ThinkingLevel: &thinking}
	system, err := s.promptSectionUpdate(projected)
	if err != nil {
		return nil, err
	}
	if system != nil {
		update.Messages = []agent.AgentMessage{{System: system}}
	}
	return update, nil
}
