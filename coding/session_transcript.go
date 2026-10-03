package coding

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	codingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
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
	run := s.runPrompt.Load()
	if current != nil && len(current.Sections) == 0 && !s.structuredSystemPrompt && (run == nil || len(run.Sections) == 0) {
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

// effectiveSystemSections reads the run's live loadout without changing the base prompt. A structured prompt is built from the run's before_agent_start options, as agent-session.ts:1669-1683 builds it for every mode, also when the caller's structured prompt is the rendering of the base options; any other caller-owned structured prompt takes only the tools and rules sections from them. The caller holds toolRegistryMu.
func (s *Session) effectiveSystemSections() (ai.OrderedSections, error) {
	desired := s.baseSystemSections
	run := s.runPrompt.Load()
	if run == nil {
		return desired, nil
	}
	if !s.structuredSystemPrompt {
		return prompts.ApplyCustomSystemPromptSections(desired, run.Sections)
	}
	active, hidden := s.ActiveToolNames(), s.hiddenDeclarations()
	built := run.BaseSections(active, hidden)
	if s.defaultSystemPrompt || s.baseSectionsRenderBaseOptions(active, hidden) {
		return prompts.ApplyCustomSystemPromptSections(built, run.Sections)
	}
	desired = cloneSystemSections(desired)
	for i, section := range desired {
		if section.Name != "tools" && section.Name != "rules" {
			continue
		}
		for _, replacement := range built {
			if section.Name == replacement.Name {
				desired[i] = replacement
				break
			}
		}
	}
	return prompts.ApplyCustomSystemPromptSections(desired, run.Sections)
}

// baseSectionsRenderBaseOptions reports that the caller's structured prompt is exactly the rendering of the Session's base options, as the print, JSON and RPC modes build it. Pi builds every mode's run prompt from the handlers' options (agent-session.ts:1669-1683, 2032), so such a prompt is rebuilt from them whole; any other caller-owned prompt keeps its own sections. The caller holds toolRegistryMu.
func (s *Session) baseSectionsRenderBaseOptions(active []string, hidden map[string]struct{}) bool {
	base := s.baseSystemPromptOptions.Load()
	if base == nil {
		return false
	}
	rendered := codingagent.BeforeAgentStartRun{Options: *base}.BaseSections(active, hidden)
	return slices.EqualFunc(rendered, s.baseSystemSections, func(a, b ai.PromptSection) bool {
		return a.Name == b.Name && (a.Value == nil) == (b.Value == nil) && (a.Value == nil || *a.Value == *b.Value)
	})
}

// hiddenDeclarations returns the tools whose declarations the active loadout hides (agent-session.ts:1674-1677).
func (s *Session) hiddenDeclarations() map[string]struct{} {
	if hidden := s.loadout.hidden.Load(); hidden != nil {
		return *hidden
	}
	return nil
}

// HiddenDeclarations returns the tools whose declarations the loadout hides from every request. A prompt built outside the Session lists no hidden tool.
func (s *Session) HiddenDeclarations() map[string]struct{} {
	return maps.Clone(s.hiddenDeclarations())
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
	if s.runPrompt.Load() != nil {
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
	return s.contextFromProjection(s.inner.BuildSessionProjection())
}

// contextFromProjection is projectedContext over a projection the caller already built.
func (s *Session) contextFromProjection(projection codingagent.SessionProjection) []agent.AgentMessage {
	return withInstructionBaseline(s.agent.Messages(), projection.Messages)
}

// prepareRequest re-projects the Session before every provider request, so
// context edits and compactions written during a run reach the next request
// (agent-session.ts _installAgentRequestProjection).
func (s *Session) prepareRequest(ctx context.Context, _ agent.PrepareRequestContext) (*agent.AgentRequestUpdate, error) {
	failed := s.failedResponse.Swap(nil)
	thinking := s.agent.ThinkingLevel()
	model := s.agent.Model()
	projection := s.inner.BuildSessionProjection()
	projected := s.contextFromProjection(projection)
	if !IsVirtualModel(model) {
		return &agent.AgentRequestUpdate{Context: projected, Model: model, ThinkingLevel: &thinking}, nil
	}
	// A routing failure rejects, which ends the run with an error response.
	route, err := s.routeRequest(ctx, model, thinking, projected, failed)
	if err != nil {
		return nil, err
	}
	// The route stands: the router already decided this request. The state entry does not change the projection.
	exceeds, err := s.exceedsCompactionThreshold(route.Model, projection)
	if err != nil {
		return nil, err
	}
	if exceeds {
		if _, err := s.runAutoCompaction(ctx, "threshold", false); err != nil {
			return nil, err
		}
		projected = s.projectedContext()
	}
	return &agent.AgentRequestUpdate{Context: projected, Model: route.Model, ThinkingLevel: &route.ThinkingLevel}, nil
}

// exceedsCompactionThreshold reports whether projection, the current session projection, exceeds the compaction threshold of model.
//
// upstream: agent-session.ts:732-740 (_exceedsCompactionThreshold)
func (s *Session) exceedsCompactionThreshold(model *ai.Model, projection codingagent.SessionProjection) (bool, error) {
	if model == nil || model.Capabilities.ContextWindow <= 0 {
		return false, nil
	}
	tokens := compaction.EstimateProjectedContextTokens(projection, s.currentBranch()).Tokens
	settings, err := s.compactionSettings()
	if err != nil {
		return false, err
	}
	return compaction.ShouldCompact(tokens, model.Capabilities.ContextWindow, settings), nil
}

// prepareNextTurn compacts before the next assistant response of a run when
// the projected context crosses the threshold, then continues from the
// projection (agent-session.ts _installAgentNextTurnRefresh and
// _compactBeforeNextAssistantResponse). It runs on the agent goroutine inside
// Send, which holds s.mu.
func (s *Session) prepareNextTurn(ctx context.Context, _ agent.PrepareNextTurnContext) (*agent.AgentLoopTurnUpdate, error) {
	projection := s.inner.BuildSessionProjection()
	// A virtual selection is checked in prepareRequest, against the model the request is routed to.
	if model := s.Model(); model != nil && !IsVirtualModel(model) {
		exceeds, err := s.exceedsCompactionThreshold(model, projection)
		if err != nil {
			return nil, err
		}
		if exceeds {
			if _, err := s.runAutoCompaction(ctx, "threshold", false); err != nil {
				return nil, err
			}
			projection = s.inner.BuildSessionProjection()
		}
	}
	thinking := s.agent.ThinkingLevel()
	projected := s.contextFromProjection(projection)
	update := &agent.AgentLoopTurnUpdate{Context: projected, Model: s.agent.Model(), ThinkingLevel: &thinking}
	// agent-session.ts:697-709 refreshes the run's options before each later turn and keeps them.
	if run := s.runPrompt.Load(); run != nil {
		next := *run
		next.Options = run.NextTurnOptions(*s.GetSystemPromptOptions())
		s.runPrompt.CompareAndSwap(run, &next)
	}
	system, err := s.promptSectionUpdate(projected)
	if err != nil {
		return nil, err
	}
	if system != nil {
		update.Messages = []agent.AgentMessage{{System: system}}
	}
	return update, nil
}
