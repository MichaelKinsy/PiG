package coding

// Ports packages/coding-agent/src/core/agent-session.ts.

import (
	"slices"
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

// SetActiveToolsByName activates registered tools in the requested order and ignores unknown and hidden names. The next provider request declares the loadout and rebuilt structured tool prompt. Opaque caller prompts and forced run prompts remain unchanged.
// Mirrors upstream AgentSession.setActiveToolsByName.
func (s *Session) SetActiveToolsByName(names []string) {
	s.loadout.applyMu.Lock()
	defer s.loadout.applyMu.Unlock()
	s.toolRegistryMu.Lock()
	defer s.toolRegistryMu.Unlock()
	s.setActiveToolsByName(names)
}

func (s *Session) setActiveToolsByName(names []string) {
	declared := s.applyToolLoadout(names)
	valid := make([]string, len(declared))
	for i, tool := range declared {
		valid[i] = tool.Name()
	}
	s.agent.SetTools(declared)
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

// toolPromptMetadata returns the registry's normalized prompt snippets and guidelines, keeping only tools that have one (agent-session.ts:3178-3192). Tools whose declarations the loadout hides are not listed: they are only callable through another tool, and the list must match the declarations the request carries (agent-session.ts:1634-1638, 1671-1677).
func (s *Session) toolPromptMetadata() (map[string]string, map[string][]string) {
	hints := make(map[string]string)
	guidelines := make(map[string][]string)
	var hidden map[string]struct{}
	if hiddenSet := s.loadout.hidden.Load(); hiddenSet != nil {
		hidden = *hiddenSet
	}
	for _, entry := range s.toolRegistry.entries {
		definition := entry.registration.Definition
		name := definition.Name
		if snippet := strings.Join(strings.FieldsFunc(definition.PromptSnippet, widthx.IsJSSpace), " "); snippet != "" {
			if _, isHidden := hidden[name]; !isHidden {
				hints[name] = snippet
			}
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

// DiscardAddedDefaultTools drops the tools ReloadSettings staged for the next RefreshTools. A reload that stops before it rebuilds the runtime calls it, because upstream's list is local to one reload() call and a later registry refresh must not activate it (agent-session.ts:3592-3609).
func (s *Session) DiscardAddedDefaultTools() {
	s.toolRegistryMu.Lock()
	s.toolRegistry.addedDefaultTools = nil
	s.toolRegistryMu.Unlock()
}

// ReloadSettings reloads the settings files. When the initial tools came from the `defaultTools` setting, the next RefreshTools activates the tools the reload newly added to it, replacing the list an earlier reload staged (each reload() call computes its own, agent-session.ts:3598-3601); removed tools stay active and tools disabled during the session stay disabled unless the setting newly adds them.
// upstream: agent-session.ts:3592-3609
func (s *Session) ReloadSettings() {
	settings := s.services.SettingsManager()
	s.toolRegistryMu.Lock()
	usesDefaultTools := s.toolRegistry.usesDefaultTools
	s.toolRegistryMu.Unlock()
	var previous []string
	if usesDefaultTools {
		previous = settings.ResolvedDefaultTools()
	}
	settings.Reload()
	// upstream: agent-session.ts:3596 syncQueueModesFromSettings
	s.agent.SetSteeringMode(agent.QueueMode(settings.GetSteeringMode()))
	s.agent.SetFollowUpMode(agent.QueueMode(settings.GetFollowUpMode()))
	if !usesDefaultTools {
		return
	}
	var added []string
	for _, name := range settings.ResolvedDefaultTools() {
		if !slices.Contains(previous, name) {
			added = append(added, name)
		}
	}
	s.toolRegistryMu.Lock()
	s.toolRegistry.addedDefaultTools = added
	s.toolRegistryMu.Unlock()
}
