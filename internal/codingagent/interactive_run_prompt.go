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
	text := m.baseSystemPrompt()
	var combined *extension.BeforeAgentStartCombinedResult
	if runner != nil {
		var err error
		combined, err = runner.EmitBeforeAgentStart(ctx, prompt, extensionImages(images), text, base)
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
	return nil
}

// endRunPrompt clears the inputs of the run gen installed and forces the base prompt again, as agent-session.ts:1485 clears _runSystemPromptOptions before agent_settled. The loadout stays, as Pi keeps agent.state.tools. A newer run's inputs are left alone.
func (m *InteractiveMode) endRunPrompt(gen uint64) {
	m.runPromptMu.Lock()
	defer m.runPromptMu.Unlock()
	if m.runPrompt == nil || m.runPrompt.gen != gen {
		return
	}
	m.runPrompt = nil
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
		rendered, err := m.renderRunPrompt(m.runPrompt)
		if err != nil {
			return
		}
		text = rendered
		m.setTurnSystemPrompt(text)
	}
	m.agent.SetSystemPrompt(text)
}

// renderRunPrompt renders the run's prompt with the live active tools, as agent-session.ts:1409-1419 builds it from the run options. A returned systemPrompt is exact. An opaque caller prompt is the preamble of the run's sections.
func (m *InteractiveMode) renderRunPrompt(state *interactiveRunPrompt) (string, error) {
	run := state.run
	if run.SystemPrompt != nil {
		return *run.SystemPrompt, nil
	}
	var base ai.OrderedSections
	if m.structuredSystemPrompt() {
		options := run.Options
		options.SelectedTools = m.activeToolNames()
		base = prompts.BuildSystemPromptSections(prompts.FromExtensionOptions(options))
	} else {
		text := m.baseSystemPrompt()
		if len(run.Sections) == 0 {
			return text, nil
		}
		base = ai.OrderedSections{{Name: "preamble", Value: new(text)}}
	}
	sections, err := prompts.ApplyCustomSystemPromptSections(base, run.Sections)
	if err != nil {
		return "", err
	}
	return ai.GetCurrentSystemPrompt([]ai.Message{ai.SystemMessage{Content: ai.SystemText(""), Sections: sections}}), nil
}

// baseSystemPrompt returns the base prompt without a run's replacement.
func (m *InteractiveMode) baseSystemPrompt() string {
	m.turnSystemPromptMu.RLock()
	defer m.turnSystemPromptMu.RUnlock()
	return m.opts.SystemPrompt
}
