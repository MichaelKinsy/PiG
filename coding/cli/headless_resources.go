package cli

import (
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// extensionPromptSkills is the Skill shape Pi hands extensions in systemPromptOptions.skills (skills.ts Skill).
func extensionPromptSkills(skills []*codingagent.SkillDef) []extension.SystemPromptSkill {
	return codingagent.SystemPromptSkills(skills)
}

// sessionPromptResources is the resource-loader state the Session reports as before_agent_start systemPromptOptions.
func sessionPromptResources(resolved resolvedPromptInputs, contextFiles []codingagent.ContextFile, skills []*codingagent.SkillDef) *coding.SystemPromptResources {
	files := make([]extension.SystemPromptContextFile, 0, len(contextFiles))
	for _, file := range contextFiles {
		files = append(files, extension.SystemPromptContextFile{Path: file.Path, Content: file.Content})
	}
	return &coding.SystemPromptResources{CustomPrompt: resolved.custom, CustomPromptSet: resolved.customSet, AppendSystemPrompt: resolved.append, ContextFiles: files, Skills: extensionPromptSkills(skills)}
}
