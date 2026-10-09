package durableagent

// Ports packages/coding-agent/src/experimental/durable/prompt.ts

import (
	"context"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// promptSectionKeys is pi's section order; BuildSystemPromptSections omits the ones without content.
var promptSectionKeys = []string{"preamble", "tools", "rules", "docs", "project_context", "skills", "cwd"}

// codingToolContributions are the prompt snippets and guidelines of the four tools durable/tools provides.
var codingToolContributions = []string{"read", "bash", "edit", "write"}

type directoryResources struct {
	contextFiles []codingagent.ContextFile
	skills       []prompts.Skill
}

// CreatePiPrompt is pi's system prompt as one extension: the sections of BuildSystemPromptSections for the request's tools and the conversation's directory. Context files and skills load once per directory, like pi at startup.
//
// Upstream builds the sections once per request and renders each key from that build. The Go section callback receives the input by value and has no identity to key the build on, so each key builds from the cached resources; the build is pure in them and the input, so the rendered text is the same.
func CreatePiPrompt(settings *codingagent.SettingsManager, fallbackCwd string) *durable.Extension {
	var mu sync.Mutex
	resources := map[string]*directoryResources{}
	load := func(cwd string) (*directoryResources, error) {
		mu.Lock()
		defer mu.Unlock()
		if found, ok := resources[cwd]; ok {
			return found, nil
		}
		agentDir := codingagent.AgentDir()
		loaded, err := codingagent.LoadSkills(codingagent.LoadSkillsOptions{CWD: cwd, AgentDir: agentDir, SkillPaths: settings.GetSkillPaths(), IncludeDefaults: true})
		if err != nil {
			return nil, err
		}
		found := &directoryResources{contextFiles: codingagent.LoadProjectContextFiles(cwd, agentDir)}
		for _, skill := range loaded.Skills {
			found.skills = append(found.skills, prompts.Skill{Name: skill.Name, Description: skill.Description, Path: skill.FilePath, DisableModelInvocation: skill.DisableModelInvocation})
		}
		resources[cwd] = found
		return found, nil
	}
	buildSections := func(input durable.PromptInput) (map[string]string, error) {
		cwd := fallbackCwd
		if input.Agent.Cwd != nil {
			cwd = *input.Agent.Cwd
		}
		if input.Env != nil {
			cwd = input.Env.Cwd()
		}
		selected := make([]string, len(input.Agent.Tools))
		snippets := prompts.DefaultToolSnippets()
		guidelines := tools.DefaultToolGuidelines()
		hints := map[string]string{}
		rules := map[string][]string{}
		for index, tool := range input.Agent.Tools {
			selected[index] = tool.Name
			if !slices.Contains(codingToolContributions, tool.Name) {
				continue
			}
			hints[tool.Name] = snippets[tool.Name]
			rules[tool.Name] = append([]string{}, guidelines[tool.Name]...)
		}
		found, err := load(cwd)
		if err != nil {
			return nil, err
		}
		options := prompts.Options{Cwd: cwd, Tools: selected, ToolHints: hints, ToolGuidelines: rules, Skills: found.skills}
		for _, file := range found.contextFiles {
			options.ContextFiles = append(options.ContextFiles, struct{ Path, Content string }{file.Path, file.Content})
		}
		built := map[string]string{}
		for _, section := range prompts.BuildSystemPromptSections(options) {
			if section.Value != nil {
				built[section.Name] = *section.Value
			}
		}
		return built, nil
	}
	sections := make([]*durable.PromptSection, len(promptSectionKeys))
	for index, key := range promptSectionKeys {
		// The built sections carry their own tags.
		sections[index] = harness.Section(key, func(_ context.Context, input durable.PromptInput) (*string, error) {
			built, err := buildSections(input)
			if err != nil {
				return nil, err
			}
			text, ok := built[key]
			if !ok {
				return nil, nil
			}
			return &text, nil
		}, harness.SectionOptions{Tag: new(false)})
	}
	return new(durable.Extension{Name: "pi-prompt", Sections: sections})
}
