package codingagent

import (
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
)

// BeforeAgentStartRun holds one prompt's per-run inputs after before_agent_start. The Session and interactive mode both derive their run from it.
type BeforeAgentStartRun struct {
	// Options are the options the handlers shared, or the base options when no handler changed them.
	Options extension.BuildSystemPromptOptions
	// SelectedTools is an explicit selectedTools edit, which becomes the run's tool loadout. Nil keeps the live active tools, including a handler's setActiveTools call.
	SelectedTools []string
	// Sections are the run's validated custom prompt sections, or nil.
	Sections ai.OrderedSections
	// SystemPrompt is the exact replacement a handler returned, or nil.
	SystemPrompt *string
	// Messages are the custom messages the handlers returned.
	Messages []extension.CustomMessageRef
}

// ResolveBeforeAgentStartRun applies agent-session.ts:1702-1714 to the options passed to before_agent_start and the combined result, which may be nil. A selectedTools list that differs from the base list is an explicit edit. Invalid section names fail as in system-prompt.ts buildSystemPromptSections; the returned run then carries only SelectedTools, because agent-session.ts:1409-1419 admits the loadout before it builds the sections, so the caller applies it before rejecting the prompt.
func ResolveBeforeAgentStartRun(base extension.BuildSystemPromptOptions, result *extension.BeforeAgentStartCombinedResult) (BeforeAgentStartRun, error) {
	run := BeforeAgentStartRun{Options: base}
	var sections ai.OrderedSections
	if base.Sections != nil {
		sections = *base.Sections
	}
	if result != nil {
		if options := result.SystemPromptOptions; options != nil {
			run.Options = *options
			if options.Sections != nil {
				sections = *options.Sections
			}
			if result.SelectedToolsEdited || !slices.Equal(options.SelectedTools, base.SelectedTools) {
				run.SelectedTools = append([]string{}, options.SelectedTools...)
			}
		}
		run.SystemPrompt = result.SystemPrompt
		run.Messages = result.Messages
	}
	if len(sections) > 0 {
		if _, err := prompts.ApplyCustomSystemPromptSections(nil, sections); err != nil {
			return BeforeAgentStartRun{SelectedTools: run.SelectedTools}, err
		}
		run.Sections = slices.Clone(sections)
		for i, section := range run.Sections {
			if section.Value != nil {
				run.Sections[i].Value = new(*section.Value)
			}
		}
	}
	return run, nil
}
