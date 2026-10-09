// Ports packages/coding-agent/src/core/agent-session.ts.

package coding

import (
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// PromptTemplate is a resolved file-based prompt template.
type PromptTemplate = icodingagent.PromptTemplate

// Skill is a resolved skill supplied by the resource owner.
type Skill = icodingagent.SkillDef

// ResourceLoader returns the loader that supplies this Session's resources (agent-session.ts resourceLoader).
func (s *Session) ResourceLoader() ResourceLoader {
	if ref := s.resourceLoader.Load(); ref != nil {
		return ref.loader
	}
	return NoResources
}

// PromptTemplates returns the file-based prompt templates the Session's resource loader resolved (agent-session.ts promptTemplates).
func (s *Session) PromptTemplates() []PromptTemplate {
	return s.ResourceLoader().GetPrompts().Prompts
}

// SetPromptResources replaces the skills and prompt templates this Session reads with the resource owner's current resolved collections, keeping the loader's context files and system prompt text. It does not discover resources or change their configuration, and it does not rebuild the system prompt.
func (s *Session) SetPromptResources(templates []PromptTemplate, skills []*Skill) {
	ref := s.resourceLoader.Load()
	if ref == nil {
		ref = &resourceLoaderRef{loader: NoResources}
	}
	overlay := promptResourcesOverlay{ResourceLoader: ref.loader, static: &staticResourceLoader{templates: slices.Clone(templates), skills: slices.Clone(skills)}}
	s.resourceLoader.Store(&resourceLoaderRef{loader: overlay, promptFromLoader: ref.promptFromLoader})
}

func (s *Session) expandPromptText(text string) string {
	loader := s.ResourceLoader()
	if expanded, ok, err := icodingagent.ExpandSkillCommand(text, loader.GetSkills().Skills); ok {
		text = expanded
	} else if err != nil {
		if runner := s.currentRunner(); runner != nil {
			runner.EmitError(err)
		}
	}
	if expanded, ok := icodingagent.ExpandPromptTemplate(text, s.PromptTemplates()); ok {
		text = expanded
	}
	return text
}

// SlashCommandsFor is the getCommands action agent-session.ts _bindExtensionCore binds to the runner it is given: the runner's extension commands, then the prompt templates, then the skills, each with the source info its resolver recorded.
func (s *Session) SlashCommandsFor(runner *inproc.Runner) []extension.SlashCommandInfo {
	commands := []extension.SlashCommandInfo{}
	if runner != nil {
		for _, command := range runner.Commands() {
			commands = append(commands, extension.SlashCommandInfo{
				Name: strings.TrimPrefix(command.InvocationName, "/"), Description: command.Description,
				Source: "extension", SourceInfo: icodingagent.PiSourceInfoValue(command.SourceInfo),
			})
		}
	}
	for _, template := range s.PromptTemplates() {
		commands = append(commands, extension.SlashCommandInfo{Name: template.Name, Description: template.Description, Source: "prompt", SourceInfo: template.SourceInfo})
	}
	for _, skill := range s.ResourceLoader().GetSkills().Skills {
		commands = append(commands, extension.SlashCommandInfo{Name: "skill:" + skill.Name, Description: skill.Description, Source: "skill", SourceInfo: skill.SourceInfo})
	}
	return commands
}
