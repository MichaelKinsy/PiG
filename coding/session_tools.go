package coding

// Ports packages/coding-agent/src/core/agent-session.ts.

import (
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
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
	previous := s.ActiveToolNames()
	s.setActiveToolsByName(names)
	// A loadout that deactivates a tool replaces the restored one, whose pending tools are dropped. One that only adds tools, like activating tool_search, keeps them.
	// upstream: agent-session.ts setActiveToolsByName
	active := s.ActiveToolNames()
	for _, name := range previous {
		if !slices.Contains(active, name) {
			s.pendingToolNames = nil
			break
		}
	}
}

// ReapplyActiveTools applies the Session's own selection again after a mode replaced the agent's tool list, as interactive mode does when it rebuilds its tools on reload. Unlike SetActiveToolsByName it is not a new loadout, so the pending tools of a restored or reloaded loadout stay pending.
func (s *Session) ReapplyActiveTools(names []string) {
	s.loadout.applyMu.Lock()
	defer s.loadout.applyMu.Unlock()
	s.toolRegistryMu.Lock()
	defer s.toolRegistryMu.Unlock()
	s.setActiveToolsByName(names)
}

// setActiveToolsByName is upstream's _setActiveTools: it applies the loadout and drops the declared tools from the pending ones. The caller holds applyMu and toolRegistryMu.
func (s *Session) setActiveToolsByName(names []string) {
	declared := s.applyToolLoadout(names)
	valid := make([]string, len(declared))
	for i, tool := range declared {
		valid[i] = tool.Name()
	}
	s.pendingToolNames = slices.DeleteFunc(s.pendingToolNames, func(name string) bool { return slices.Contains(valid, name) })
	s.agent.SetTools(declared)
	s.rebuildSystemPrompt(valid)
}

// isAllowedTool reports whether the allowlist and the denylist admit a tool name.
//
// upstream: agent-session.ts _isAllowedTool
func (r *sessionToolRegistry) isAllowedTool(name string) bool {
	return r.filter.Allows(name)
}

// addPendingTools adds names to the pending tools, each once, keeping insertion order. The caller holds toolRegistryMu.
func (s *Session) addPendingTools(names []string) {
	for _, name := range names {
		if !slices.Contains(s.pendingToolNames, name) {
			s.pendingToolNames = append(s.pendingToolNames, name)
		}
	}
}

// clearPendingTools drops the pending tools when an agent run starts: the run records the loadout in the transcript, so a restored tool that never registers does not stay pending.
//
// upstream: agent-session.ts _runAgentPrompt
func (s *Session) clearPendingTools() {
	s.toolRegistryMu.Lock()
	s.pendingToolNames = nil
	s.toolRegistryMu.Unlock()
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

// toolPromptMetadata returns the registry's normalized prompt snippets and guidelines, keeping only tools that have one (agent-session.ts _rebuildSystemPrompt). Tools whose declarations the loadout hides stay in it: the prompt builder leaves them out through the options' hiddenTools.
func (s *Session) toolPromptMetadata() (map[string]string, map[string][]string) {
	hints := make(map[string]string)
	guidelines := make(map[string][]string)
	for _, entry := range s.toolRegistry.entries {
		definition := entry.registration.Definition
		name := definition.Name
		if snippet := strings.Join(strings.FieldsFunc(definition.PromptSnippet, widthx.IsJSSpace), " "); snippet != "" {
			hints[name] = snippet
		}
		unique := normalizePromptGuidelines(definition.PromptGuidelines)
		if len(unique) > 0 {
			guidelines[name] = unique
		}
	}
	return hints, guidelines
}

// normalizePromptGuidelines trims each guideline and drops the empty and repeated ones.
//
// upstream: agent-session.ts _normalizePromptGuidelines
func normalizePromptGuidelines(guidelines []string) []string {
	var unique []string
	seen := make(map[string]struct{})
	for _, raw := range guidelines {
		guideline := widthx.JSTrim(raw)
		if _, duplicate := seen[guideline]; guideline == "" || duplicate {
			continue
		}
		seen[guideline] = struct{}{}
		unique = append(unique, guideline)
	}
	return unique
}

// buildToolSystemPromptSections uses executable definitions' prompt metadata, including extension overrides.
func (s *Session) buildToolSystemPromptSections(names []string) ai.OrderedSections {
	hints, guidelines := s.toolPromptMetadata()
	options := prompts.Options{Cwd: s.services.CWD(), Tools: names, HiddenTools: s.HiddenDeclarationNames(), ToolHints: hints, ToolGuidelines: guidelines}
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

// restoreToolsFromTranscript activates the tools the current branch's transcript declares (upstream _restoreToolsFromTranscript). Declared tools the Session does not have yet, such as tools of MCP servers that are still connecting, stay pending until they register, if the allowlist and denylist admit them. A branch without a system message keeps the current tools.
func (s *Session) restoreToolsFromTranscript() {
	// The transcript is read before the loadout locks, so they are not held while the whole branch is projected.
	var systems []ai.Message
	for _, message := range s.inner.BuildSessionProjection().Messages {
		if message.System != nil {
			systems = append(systems, *message.System)
		}
	}
	current := ai.GetCurrentSystemMessage(systems)
	s.loadout.applyMu.Lock()
	defer s.loadout.applyMu.Unlock()
	s.toolRegistryMu.Lock()
	defer s.toolRegistryMu.Unlock()
	s.pendingToolNames = nil
	if current == nil {
		return
	}
	names := make([]string, len(current.ToolsAdded))
	for i, tool := range current.ToolsAdded {
		names[i] = tool.Name
	}
	var pending []string
	for _, name := range names {
		if s.toolRegistry.isAllowedTool(name) {
			pending = append(pending, name)
		}
	}
	s.addPendingTools(pending)
	s.setActiveToolsByName(names)
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
	settings := s.SettingsManager()
	s.toolRegistryMu.Lock()
	usesDefaultTools := s.toolRegistry.usesDefaultTools
	modifiers := s.toolRegistry.defaultToolModifiers
	s.toolRegistryMu.Unlock()
	// upstream: agent-session.ts:3665-3671 getDefaultTools applies the session's --tools +name/-name entries to the setting.
	defaultTools := func() []string { return icodingagent.ApplyToolModifiers(settings.ResolvedDefaultTools(), modifiers) }
	var previous []string
	if usesDefaultTools {
		previous = defaultTools()
	}
	settings.Reload()
	// upstream: agent-session.ts:3596 syncQueueModesFromSettings
	s.agent.SetSteeringMode(agent.QueueMode(settings.GetSteeringMode()))
	s.agent.SetFollowUpMode(agent.QueueMode(settings.GetFollowUpMode()))
	if !usesDefaultTools {
		return
	}
	var added []string
	for _, name := range defaultTools() {
		if !slices.Contains(previous, name) {
			added = append(added, name)
		}
	}
	s.toolRegistryMu.Lock()
	s.toolRegistry.addedDefaultTools = added
	s.toolRegistryMu.Unlock()
}
