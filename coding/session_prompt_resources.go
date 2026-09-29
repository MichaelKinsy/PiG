// Ports packages/coding-agent/src/core/agent-session.ts.

package coding

import (
	"slices"

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
	if expanded, ok := icodingagent.ExpandPromptTemplate(text, loader.GetPrompts().Prompts); ok {
		text = expanded
	}
	return text
}
