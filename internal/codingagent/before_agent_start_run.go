package codingagent

import (
	"maps"
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
		// agent-session.ts:1724 forces the run options' forceSystemPrompt, which a handler may set or clear directly as well as through a returned systemPrompt (runner.ts:1445-1447).
		if options := result.SystemPromptOptions; options != nil {
			run.SystemPrompt = nil
			if options.ForceSystemPrompt != nil {
				run.SystemPrompt = new(*options.ForceSystemPrompt)
			}
		}
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

// BaseSections builds the run's structured prompt sections from the run's options, as _preparePromptAndToolLoadout does for every mode (agent-session.ts:1669-1683), before its custom sections apply. selectedTools are the live active tools. The hidden declarations leave the tool list and rules out, so they match the declarations the request carries (agent-session.ts _preparePromptAndToolLoadout).
func (r BeforeAgentStartRun) BaseSections(selectedTools []string, hidden map[string]struct{}) ai.OrderedSections {
	options := r.Options
	options.SelectedTools = selectedTools
	options.HiddenTools = HiddenToolNames(hidden)
	return prompts.BuildSystemPromptSections(prompts.FromExtensionOptions(options))
}

// PromptSections is BaseSections with the run's validated custom sections applied.
func (r BeforeAgentStartRun) PromptSections(selectedTools []string, hidden map[string]struct{}) (ai.OrderedSections, error) {
	return prompts.ApplyCustomSystemPromptSections(r.BaseSections(selectedTools, hidden), r.Sections)
}

// HiddenToolNames lists the tools whose declarations the loadout hides, sorted (agent-session.ts hiddenTools).
func HiddenToolNames(hidden map[string]struct{}) []string {
	return slices.Sorted(maps.Keys(hidden))
}

// NextTurnOptions refreshes a run's options before a later turn: the live tools, and the base snippets and guidelines under the run's (agent-session.ts:697-706), so a tool registered during the run is listed and an edited entry wins.
func (r BeforeAgentStartRun) NextTurnOptions(base extension.BuildSystemPromptOptions) extension.BuildSystemPromptOptions {
	options := r.Options
	options.ToolSnippets = mergeMaps(base.ToolSnippets, options.ToolSnippets)
	options.ToolGuidelines = mergeMaps(base.ToolGuidelines, options.ToolGuidelines)
	return options
}

func mergeMaps[V any](base, over map[string]V) map[string]V {
	merged := maps.Clone(base)
	if merged == nil {
		merged = map[string]V{}
	}
	maps.Copy(merged, over)
	return merged
}
