package coding

// Ports packages/coding-agent/src/core/agent-session.ts.

import (
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// ActiveToolNames returns the names of the tools active for the next agent turn. Mirrors upstream AgentSession.getActiveToolNames.
func (s *Session) ActiveToolNames() []string {
	active := s.agent.Tools()
	names := make([]string, len(active))
	for i, tool := range active {
		names[i] = tool.Name()
	}
	return names
}

// SetActiveToolsByName activates registered tools in the requested order and ignores unknown names. The next provider request declares the loadout and rebuilt structured tool prompt. Opaque caller prompts and forced run prompts remain unchanged.
// Mirrors upstream AgentSession.setActiveToolsByName.
func (s *Session) SetActiveToolsByName(names []string) {
	s.toolRegistryMu.Lock()
	defer s.toolRegistryMu.Unlock()
	s.setActiveToolsByName(names)
}

func (s *Session) setActiveToolsByName(names []string) {
	var active []agent.AgentTool
	valid := []string{}
	registry := make(map[string]agent.AgentTool, len(s.tools))
	for _, tool := range s.tools {
		registry[tool.Name()] = tool
	}
	for _, name := range names {
		if tool := registry[name]; tool != nil {
			active = append(active, s.bindTool(tool))
			valid = append(valid, name)
		}
	}
	s.agent.SetTools(active)
	s.rebuildSystemPrompt(valid)
}

// rebuildSystemPrompt refreshes the tool-owned sections while retaining caller resource sections and custom preambles.
func (s *Session) rebuildSystemPrompt(toolNames []string) {
	defer func() { s.baseSystemPromptOptions.Store(s.buildSystemPromptOptions(toolNames)) }()
	if !s.structuredSystemPrompt {
		return
	}
	sections := s.buildToolSystemPromptSections(toolNames)
	if s.defaultSystemPrompt {
		s.baseSystemSections = sections
	} else {
		for i, section := range s.baseSystemSections {
			if section.Name != "tools" && section.Name != "rules" {
				continue
			}
			for _, replacement := range sections {
				if replacement.Name == section.Name {
					s.baseSystemSections[i] = replacement
					break
				}
			}
		}
	}
	s.baseSystemPrompt.Store(new(ai.GetCurrentSystemPrompt([]ai.Message{ai.SystemMessage{Sections: s.baseSystemSections}})))
}

// toolPromptMetadata returns the registry's normalized prompt snippets and guidelines, keeping only tools that have one (agent-session.ts:3178-3192).
func (s *Session) toolPromptMetadata() (map[string]string, map[string][]string) {
	hints := make(map[string]string)
	guidelines := make(map[string][]string)
	for _, entry := range s.toolRegistry.entries {
		definition := entry.registration.Definition
		name := definition.Name
		if snippet := strings.Join(strings.FieldsFunc(definition.PromptSnippet, widthx.IsJSSpace), " "); snippet != "" {
			hints[name] = snippet
		}
		var unique []string
		seen := make(map[string]struct{})
		for _, raw := range definition.PromptGuidelines {
			guideline := widthx.JSTrim(raw)
			if _, duplicate := seen[guideline]; guideline == "" || duplicate {
				continue
			}
			seen[guideline] = struct{}{}
			unique = append(unique, guideline)
		}
		if len(unique) > 0 {
			guidelines[name] = unique
		}
	}
	return hints, guidelines
}

// buildToolSystemPromptSections uses executable definitions' prompt metadata, including extension overrides.
func (s *Session) buildToolSystemPromptSections(names []string) ai.OrderedSections {
	hints, guidelines := s.toolPromptMetadata()
	options := prompts.Options{Cwd: s.services.CWD(), Tools: names, ToolHints: hints, ToolGuidelines: guidelines}
	var resources *SystemPromptResources
	if s.systemPromptResources.Load() == nil {
		resources = s.loaderSystemPromptResources()
	}
	if resources != nil {
		options.AppendMode = "append"
		if resources.CustomPrompt != "" {
			options.CustomPrompt = resources.CustomPrompt
			options.AppendMode = "replace"
		}
		options.AppendSystemPrompt = resources.AppendSystemPrompt
		for _, skill := range resources.Skills {
			options.Skills = append(options.Skills, prompts.Skill{Name: skill.Name, Description: skill.Description, Path: skill.FilePath, DisableModelInvocation: skill.DisableModelInvocation})
		}
		for _, file := range resources.ContextFiles {
			options.ContextFiles = append(options.ContextFiles, struct{ Path, Content string }{Path: file.Path, Content: file.Content})
		}
	}
	return prompts.BuildSystemPromptSections(options)
}

// SetSystemPromptSections replaces the caller-built structured prompt and resolves its tool-owned sections against the active registry. The next request records the change in the transcript.
func (s *Session) SetSystemPromptSections(sections ai.OrderedSections) {
	s.toolRegistryMu.Lock()
	defer s.toolRegistryMu.Unlock()
	s.structuredSystemPrompt = true
	s.defaultSystemPrompt = false
	s.baseSystemSections = cloneSystemSections(sections)
	s.rebuildSystemPrompt(s.ActiveToolNames())
	s.baseSystemPrompt.Store(new(ai.GetCurrentSystemPrompt([]ai.Message{ai.SystemMessage{Sections: s.baseSystemSections}})))
}

// restoreToolsFromTranscript activates the tools the current branch's transcript declares, keeping only tools the Session has (upstream _restoreToolsFromTranscript). A branch without a system message keeps the current tools.
func (s *Session) restoreToolsFromTranscript() {
	var systems []ai.Message
	for _, message := range s.inner.BuildSessionProjection().Messages {
		if message.System != nil {
			systems = append(systems, *message.System)
		}
	}
	current := ai.GetCurrentSystemMessage(systems)
	if current == nil {
		return
	}
	names := make([]string, len(current.ToolsAdded))
	for i, tool := range current.ToolsAdded {
		names[i] = tool.Name
	}
	s.SetActiveToolsByName(names)
}
