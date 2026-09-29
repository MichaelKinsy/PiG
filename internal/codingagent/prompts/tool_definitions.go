package prompts

import (
	"maps"
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// WithToolDefinitions applies registered definitions over built-in prompt metadata.
// An override with no snippet or guidelines removes the built-in contribution.
// Ports packages/coding-agent/src/core/agent-session.ts
func WithToolDefinitions(options Options, definitions []extension.RegisteredTool) Options {
	options.ToolHints = maps.Clone(options.ToolHints)
	if options.ToolHints == nil {
		options.ToolHints = map[string]string{}
	}
	options.ToolGuidelines = maps.Clone(options.ToolGuidelines)
	if options.ToolGuidelines == nil {
		options.ToolGuidelines = map[string][]string{}
	}
	for _, registered := range definitions {
		definition := registered.Definition
		options.ToolHints[definition.Name] = strings.Join(strings.FieldsFunc(definition.PromptSnippet, widthx.IsJSSpace), " ")
		var guidelines []string
		seen := map[string]bool{}
		for _, guideline := range definition.PromptGuidelines {
			guideline = widthx.JSTrim(guideline)
			if guideline != "" && !seen[guideline] {
				seen[guideline] = true
				guidelines = append(guidelines, guideline)
			}
		}
		options.ToolGuidelines[definition.Name] = guidelines
	}
	return options
}

// FromExtensionOptions converts the resource and tool inputs exposed to extensions into prompt-builder inputs.
func FromExtensionOptions(input extension.BuildSystemPromptOptions) Options {
	options := Options{Cwd: input.Cwd, Tools: input.SelectedTools, ToolHints: input.ToolSnippets, ToolGuidelines: input.ToolGuidelines, PromptGuidelines: input.PromptGuidelines, CustomPrompt: input.CustomPrompt, AppendSystemPrompt: input.AppendSystemPrompt}
	if input.CustomPrompt != "" {
		options.AppendMode = "replace"
	}
	for _, file := range input.ContextFiles {
		options.ContextFiles = append(options.ContextFiles, struct{ Path, Content string }{file.Path, file.Content})
	}
	for _, skill := range input.Skills {
		options.Skills = append(options.Skills, Skill{Name: skill.Name, Description: skill.Description, Path: skill.FilePath, DisableModelInvocation: skill.DisableModelInvocation})
	}
	return options
}
