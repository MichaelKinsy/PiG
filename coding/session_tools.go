package coding

import (
	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// ActiveToolNames returns the names of the tools active for the next agent
// turn. Mirrors upstream AgentSession.getActiveToolNames.
func (s *Session) ActiveToolNames() []string {
	active := s.agent.Tools()
	names := make([]string, len(active))
	for i, tool := range active {
		names[i] = tool.Name()
	}
	return names
}

// SetActiveToolsByName activates the Session's tools named in names, in that
// order. Names the Session has no tool for are ignored. The change applies
// from the next provider request, which declares the new loadout, and the
// Session's default system prompt lists the active tools from the next prompt.
// Mirrors upstream AgentSession.setActiveToolsByName.
func (s *Session) SetActiveToolsByName(names []string) {
	var active []agent.AgentTool
	var valid []string
	for _, name := range names {
		for _, tool := range s.tools {
			if tool.Name() == name {
				active = append(active, tool)
				valid = append(valid, name)
				break
			}
		}
	}
	s.agent.SetTools(active)
	s.rebuildSystemPrompt(valid)
}

// rebuildSystemPrompt lists toolNames in the default system prompt the
// Session built itself (upstream _rebuildSystemPrompt). A prompt the caller
// supplied is left unchanged.
func (s *Session) rebuildSystemPrompt(toolNames []string) {
	if !s.defaultSystemPrompt {
		return
	}
	s.baseSystemSections = prompts.BuildSystemPromptSections(prompts.Options{Cwd: s.services.CWD(), Tools: toolNames, ToolHints: prompts.DefaultToolSnippets(), ToolGuidelines: tools.DefaultToolGuidelines()})
	s.baseSystemPrompt = ai.GetCurrentSystemPrompt([]ai.Message{ai.SystemMessage{Sections: s.baseSystemSections}})
}

// restoreToolsFromTranscript activates the tools the current branch's
// transcript declares, keeping only tools the Session has (upstream
// _restoreToolsFromTranscript). A branch without a system message keeps the
// current tools.
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
