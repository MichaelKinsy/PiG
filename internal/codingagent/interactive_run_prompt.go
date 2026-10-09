package codingagent

import (
	"context"
	"errors"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
)

// interactiveRunPrompt is one run's before_agent_start inputs. gen is the runGen of the turn that installed it.
type interactiveRunPrompt struct {
	gen uint64
	run BeforeAgentStartRun
}

// prepareRunPrompt is the before_agent_start step of Pi's prompt() (agent-session.ts:1700-1748) for one turn. It emits the event with the base options, resolves the result with the Session's rule, applies an edited tool loadout, queues the returned messages and installs the run's prompt. An error rejects the prompt before the run starts.
func (m *InteractiveMode) prepareRunPrompt(ctx context.Context, runner *inproc.Runner, gen uint64, prompt string, images []ai.ImageContent) error {
	base := *m.currentSystemPromptOptions()
	var combined *extension.BeforeAgentStartCombinedResult
	if runner != nil {
		var err error
		combined, err = runner.EmitBeforeAgentStart(ctx, prompt, images, base)
		if err != nil {
			return err
		}
	}
	run, resolveErr := ResolveBeforeAgentStartRun(base, combined)
	// agent-session.ts:1409-1419 admits the loadout before buildSystemPromptSections rejects an invalid section name. The owner loop owns every tool change.
	if run.SelectedTools != nil || hasDuplicateTools(m.agent.Tools()) {
		if err := m.runOnMainAndWait(ctx, func() error {
			if run.SelectedTools != nil {
				m.applyRunToolLoadout(run.SelectedTools)
			} else {
				// An unedited selection retains the live tools, including caller-supplied tools.
				m.deduplicateRunTools()
			}
			return nil
		}); err != nil {
			return err
		}
	}
	if resolveErr != nil {
		return resolveErr
	}
	if err := m.beginRunPrompt(gen, run); err != nil {
		return err
	}
	if session, ok := m.opts.SessionHandle.(interface {
		QueueAgentStartMessages([]extension.CustomMessageRef)
	}); ok && len(run.Messages) > 0 {
		session.QueueAgentStartMessages(run.Messages)
	}
	return nil
}

func hasDuplicateTools(tools []agent.AgentTool) bool {
	seen := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if seen[tool.Name()] {
			return true
		}
		seen[tool.Name()] = true
	}
	return false
}

func (m *InteractiveMode) deduplicateRunTools() {
	seen := map[string]bool{}
	var selected []agent.AgentTool
	for _, tool := range m.agent.Tools() {
		if !seen[tool.Name()] {
			seen[tool.Name()] = true
			selected = append(selected, tool)
		}
	}
	m.agent.SetTools(selected)
}

// validatePromptModelAuth runs the Session's prompt check for a model and usable auth, the one the Session's own prompt path uses. A mode without that Session method checks only that a model is selected.
func (m *InteractiveMode) validatePromptModelAuth(ctx context.Context) error {
	if session, ok := m.opts.SessionHandle.(interface {
		ValidatePromptModelAuth(context.Context) error
	}); ok {
		return session.ValidatePromptModelAuth(ctx)
	}
	if m.agent == nil || m.agent.Model() == nil {
		return errors.New(FormatNoModelSelectedMessage())
	}
	return nil
}

// applyRunToolLoadout makes an edited selectedTools list the live loadout, as agent-session.ts:1409-1415 does. Like the Session it keeps the base options object that later before_agent_start events receive.
func (m *InteractiveMode) applyRunToolLoadout(names []string) {
	m.selectActiveToolsByName(mergeUniqueStrings(nil, names...))
}

// beginRunPrompt installs the run's prompt inputs and forces the rendered prompt on the agent.
func (m *InteractiveMode) beginRunPrompt(gen uint64, run BeforeAgentStartRun) error {
	m.runPromptMu.Lock()
	defer m.runPromptMu.Unlock()
	state := &interactiveRunPrompt{gen: gen, run: run}
	text, err := m.renderRunPrompt(state)
	if err != nil {
		return err
	}
	m.runPrompt = state
	m.setTurnSystemPrompt(text)
	if m.agent != nil {
		m.agent.SetSystemPrompt(text)
	}
	m.publishRunPrompt(&run)
	return nil
}

// publishRunPrompt hands the run to the Session, whose request hooks append the run's prompt sections to the transcript
// before the first request, as they do for a run the Session prepared. Nil ends the run. A mode without a Session has no
// transcript hooks.
func (m *InteractiveMode) publishRunPrompt(run *BeforeAgentStartRun) {
	if session, ok := m.opts.SessionHandle.(interface{ SetRunPrompt(*BeforeAgentStartRun) }); ok {
		session.SetRunPrompt(run)
	}
}

// endRunPrompt clears the inputs of the run gen installed and forces the base prompt again, as agent-session.ts:1485 clears _runSystemPromptOptions before agent_settled. The loadout stays, as Pi keeps agent.state.tools. A newer run's inputs are left alone.
func (m *InteractiveMode) endRunPrompt(gen uint64) {
	m.runPromptMu.Lock()
	defer m.runPromptMu.Unlock()
	if m.runPrompt == nil || m.runPrompt.gen != gen {
		return
	}
	m.runPrompt = nil
	m.publishRunPrompt(nil)
	m.clearTurnSystemPrompt()
	if m.agent != nil {
		m.agent.SetSystemPrompt(m.baseSystemPrompt())
	}
}

// refreshForcedPrompt re-renders the prompt forced on the agent after the live tools change. During a run it rebuilds the run's prompt with the live tools, as agent-session.ts:700-709 does before the next turn; outside a run it forces the rebuilt base prompt.
func (m *InteractiveMode) refreshForcedPrompt() {
	if m.agent == nil {
		return
	}
	m.runPromptMu.Lock()
	defer m.runPromptMu.Unlock()
	text := m.baseSystemPrompt()
	if m.runPrompt != nil {
		m.runPrompt.run.Options = m.runPrompt.run.NextTurnOptions(*m.currentSystemPromptOptions())
		rendered, err := m.renderRunPrompt(m.runPrompt)
		if err != nil {
			return
		}
		text = rendered
		m.setTurnSystemPrompt(text)
	}
	m.agent.SetSystemPrompt(text)
}

// installRunPromptTurnRefresh wraps the agent's next-turn hook once per agent. After the Session's hook has compacted, each later turn of a run refreshes the run's options and forces the re-rendered prompt, as agent-session.ts:864-883 (_installAgentNextTurnRefresh) rebuilds the run options before every later turn. A failing earlier hook stops the turn before the refresh, as there.
func (m *InteractiveMode) installRunPromptTurnRefresh() {
	target := m.agent
	if target == nil || m.runPromptTurnAgent == target {
		return
	}
	m.runPromptTurnAgent = target
	previous := target.PrepareNextTurnWithContextHook()
	target.SetPrepareNextTurnWithContext(func(ctx context.Context, turn agent.PrepareNextTurnContext) (*agent.AgentLoopTurnUpdate, error) {
		var update *agent.AgentLoopTurnUpdate
		if previous != nil {
			var err error
			if update, err = previous(ctx, turn); err != nil {
				return update, err
			}
		}
		m.refreshRunPromptForNextTurn(target)
		return update, nil
	})
}

// refreshRunPromptForNextTurn merges the base snippets and guidelines under the run's and forces the run's prompt rendered with the live tools on target. Outside a run the base prompt stays.
func (m *InteractiveMode) refreshRunPromptForNextTurn(target *agent.Agent) {
	m.runPromptMu.Lock()
	defer m.runPromptMu.Unlock()
	if m.runPrompt == nil {
		return
	}
	m.runPrompt.run.Options = m.runPrompt.run.NextTurnOptions(*m.currentSystemPromptOptions())
	text, err := m.renderRunPrompt(m.runPrompt)
	if err != nil {
		return
	}
	m.setTurnSystemPrompt(text)
	target.SetSystemPrompt(text)
}

// renderRunPrompt renders the run's prompt with the live active tools through the builder every mode shares (BeforeAgentStartRun.PromptSections, agent-session.ts:1669-1683). A returned systemPrompt is exact. An opaque caller prompt is the preamble of the run's sections.
func (m *InteractiveMode) renderRunPrompt(state *interactiveRunPrompt) (string, error) {
	run := state.run
	if run.SystemPrompt != nil {
		return *run.SystemPrompt, nil
	}
	if m.structuredSystemPrompt() {
		sections, err := run.PromptSections(m.activeToolNames(), m.hiddenDeclarations())
		if err != nil {
			return "", err
		}
		return ai.GetCurrentSystemPrompt([]ai.Message{ai.SystemMessage{Content: ai.SystemText(""), Sections: sections}}), nil
	}
	text := m.baseSystemPrompt()
	if len(run.Sections) == 0 {
		return text, nil
	}
	sections, err := prompts.ApplyCustomSystemPromptSections(ai.OrderedSections{{Name: "preamble", Value: new(text)}}, run.Sections)
	if err != nil {
		return "", err
	}
	return ai.GetCurrentSystemPrompt([]ai.Message{ai.SystemMessage{Content: ai.SystemText(""), Sections: sections}}), nil
}

// hiddenDeclarations returns the tools whose declarations the Session's loadout hides from every request. A mode without a Session hides none.
// hiddenDeclarationNames lists the hidden declarations in the order the Session's hooks hid them.
func (m *InteractiveMode) hiddenDeclarationNames() []string {
	if session, ok := m.opts.SessionHandle.(interface{ HiddenDeclarationNames() []string }); ok {
		return session.HiddenDeclarationNames()
	}
	return HiddenToolNames(m.hiddenDeclarations())
}

func (m *InteractiveMode) hiddenDeclarations() map[string]struct{} {
	if session, ok := m.opts.SessionHandle.(interface{ HiddenDeclarations() map[string]struct{} }); ok {
		return session.HiddenDeclarations()
	}
	return nil
}

// baseSystemPrompt returns the base prompt without a run's replacement.
func (m *InteractiveMode) baseSystemPrompt() string {
	m.turnSystemPromptMu.RLock()
	defer m.turnSystemPromptMu.RUnlock()
	return m.opts.SystemPrompt
}
