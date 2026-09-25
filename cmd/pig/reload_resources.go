package main

import (
	"path/filepath"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

func reloadResourceSnapshotProvider(cwd, agentDir string, sm *codingagent.SettingsManager, flags CLIFlags, skillScopes *[]string) func() codingagent.ReloadResourceSnapshot {
	return func() codingagent.ReloadResourceSnapshot {
		promptPaths := collectPromptPaths(cwd, agentDir, sm, flags, sm.IsProjectTrusted())
		themePaths := collectThemePaths(cwd, agentDir, sm, flags, sm.IsProjectTrusted())
		skillPaths := collectSkillInputs(cwd, agentDir, sm, flags, skillScopes)
		contextFiles := loadContextFiles(cwd, agentDir, flags.NoContextFiles)
		infos := resourceSourceInfoProvider(cwd, agentDir, sm, flags)()
		return codingagent.ReloadResourceSnapshot{
			PromptPaths:        promptPaths,
			ThemePaths:         themePaths,
			SkillPaths:         skillPaths,
			ContextFiles:       contextFiles,
			ResourceSourceInfo: infos,
		}
	}
}

func systemPromptRebuilder(cwd, agentDir string, projectTrusted bool, flags CLIFlags, startupToolNames []string) func(skills []*codingagent.SkillDef, contextFiles []codingagent.ContextFile) (string, extension.BuildSystemPromptOptions) {
	toolNames := append([]string(nil), startupToolNames...)
	return func(skills []*codingagent.SkillDef, contextFiles []codingagent.ContextFile) (string, extension.BuildSystemPromptOptions) {
		toolHints := prompts.DefaultToolSnippets()
		toolGuidelines := tools.DefaultToolGuidelines()
		promptSkills := make([]prompts.Skill, 0, len(skills))
		for _, skill := range skills {
			promptSkills = append(promptSkills, prompts.Skill{
				Name: skill.Name, Description: skill.Description, Path: skill.Path,
				DisableModelInvocation: skill.DisableModelInvocation,
			})
		}
		promptCtxFiles := toPromptContextFiles(contextFiles)
		resolvedPrompts := resolvePromptInputs(cwd, agentDir, flags, projectTrusted)
		options := prompts.Options{
			Cwd: cwd, Tools: toolNames, ToolHints: toolHints, ToolGuidelines: toolGuidelines,
			Skills: promptSkills, PigDocsPath: filepath.Join(codingagent.ConfigRoot(), "docs"),
			AppendMode: "append", ContextFiles: promptCtxFiles,
			CustomPrompt: resolvedPrompts.custom, AppendSystemPrompt: resolvedPrompts.append,
		}
		if resolvedPrompts.custom != "" {
			options.AppendMode = "replace"
		}
		systemPrompt := prompts.BuildDefaultPrompt(options)

		extContextFiles := make([]extension.SystemPromptContextFile, 0, len(promptCtxFiles))
		for _, contextFile := range promptCtxFiles {
			extContextFiles = append(extContextFiles, extension.SystemPromptContextFile{Path: contextFile.Path, Content: contextFile.Content})
		}
		extSkills := make([]extension.SystemPromptSkill, 0, len(skills))
		for _, skill := range skills {
			extSkills = append(extSkills, extension.SystemPromptSkill{
				Name: skill.Name, Description: skill.Description, FilePath: skill.Path,
				DisableModelInvocation: skill.DisableModelInvocation,
			})
		}
		var flatToolGuidelines []string
		for _, name := range toolNames {
			flatToolGuidelines = append(flatToolGuidelines, toolGuidelines[name]...)
		}
		return systemPrompt, extension.BuildSystemPromptOptions{
			CustomPrompt: resolvedPrompts.custom, SelectedTools: append([]string(nil), toolNames...),
			ToolSnippets: toolHints, PromptGuidelines: flatToolGuidelines,
			AppendSystemPrompt: resolvedPrompts.append, Cwd: cwd,
			ContextFiles: extContextFiles, Skills: extSkills,
		}
	}
}
