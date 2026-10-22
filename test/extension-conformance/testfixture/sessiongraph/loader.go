package sessiongraph

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func diagnosticTexts(list []sdk.ResourceDiagnostic) []string {
	out := []string{}
	for _, diagnostic := range list {
		out = append(out, diagnostic.Type+": "+diagnostic.Message)
	}
	return out
}

func skillReport(result sdk.SkillsResult) map[string]any {
	skills := []map[string]any{}
	for _, skill := range result.Skills {
		skills = append(skills, map[string]any{
			"name": skill.Name, "description": skill.Description, "disable": skill.DisableModelInvocation, "file": filepath.Base(skill.FilePath),
			"baseDir": filepath.Base(skill.BaseDir), "scope": skill.SourceInfo.Scope, "origin": skill.SourceInfo.Origin, "source": skill.SourceInfo.Source,
		})
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i]["name"].(string) < skills[j]["name"].(string) })
	return map[string]any{"skills": skills, "diagnostics": diagnosticTexts(result.Diagnostics)}
}

func promptReport(result sdk.PromptsResult) map[string]any {
	prompts := []map[string]any{}
	for _, prompt := range result.Prompts {
		prompts = append(prompts, map[string]any{
			"name": prompt.Name, "description": prompt.Description, "argumentHint": prompt.ArgumentHint, "content": prompt.Content, "file": filepath.Base(prompt.FilePath), "scope": prompt.SourceInfo.Scope,
		})
	}
	sort.Slice(prompts, func(i, j int) bool { return prompts[i]["name"].(string) < prompts[j]["name"].(string) })
	return map[string]any{"prompts": prompts, "diagnostics": diagnosticTexts(result.Diagnostics)}
}

func resourceReport(loader *sdk.DefaultResourceLoader) map[string]any {
	extensions := ok(loader.GetExtensions())
	themes := ok(loader.GetThemes())
	agents := ok(loader.GetAgentsFiles())
	files := []map[string]any{}
	for _, file := range agents.AgentsFiles {
		files = append(files, map[string]any{"path": file.Path, "content": file.Content})
	}
	source := ok(loader.GetSystemPromptSource())
	sources := []string{}
	for _, item := range ok(loader.GetAppendSystemPromptSources()) {
		sources = append(sources, item.Path)
	}
	return map[string]any{
		"extensions": len(extensions.Extensions), "extensionErrors": len(extensions.Errors), "themes": len(themes.Themes),
		"skills": skillReport(ok(loader.GetSkills())), "prompts": promptReport(ok(loader.GetPrompts())), "agentsFiles": files,
		"systemPrompt": orNil(ok(loader.GetSystemPrompt())), "systemPromptSource": func() any {
			if source == nil {
				return nil
			}
			return source.Path
		}(),
		"appendSystemPrompt": ok(loader.GetAppendSystemPrompt()), "appendSources": sources,
	}
}

func loaderScript(ctx sdk.Context, args map[string]any) map[string]any {
	root, cwd, agentDir := fmt.Sprint(args["root"]), fmt.Sprint(args["cwd"]), fmt.Sprint(args["loaderAgentDir"])
	extraSkills, extraPrompts := filepath.Join(root, "extra-skills"), filepath.Join(root, "extra-prompts")
	r := map[string]any{}

	plain := ok(sdk.NewDefaultResourceLoader(ctx, sdk.DefaultResourceLoaderOptions{Cwd: cwd, AgentDir: agentDir}))
	check(plain.Reload(nil))
	r["plain"] = resourceReport(plain)

	inline := "inline prompt"
	overridden := ok(sdk.NewDefaultResourceLoader(ctx, sdk.DefaultResourceLoaderOptions{
		Cwd: cwd, AgentDir: agentDir, NoSkills: true, AdditionalSkillPaths: []string{extraSkills}, NoPromptTemplates: true, AdditionalPromptTemplatePaths: []string{extraPrompts},
		NoContextFiles: true, SystemPrompt: &inline, AppendSystemPrompt: []string{"a1", "a2"},
		SystemPromptOverride: func(base *string) *string {
			if base == nil {
				return nil
			}
			return new(*base + "!")
		},
		AppendSystemPromptOverride: func(base []string) []string { return append(append([]string{}, base...), "z") },
		SkillsOverride: func(base sdk.SkillsResult) sdk.SkillsResult {
			kept := []sdk.Skill{}
			for _, skill := range base.Skills {
				if skill.Name != "beta" {
					kept = append(kept, skill)
				}
			}
			return sdk.SkillsResult{Skills: kept, Diagnostics: base.Diagnostics}
		},
		PromptsOverride: func(base sdk.PromptsResult) sdk.PromptsResult {
			for i := range base.Prompts {
				base.Prompts[i].Description = "overridden " + base.Prompts[i].Name
			}
			return base
		},
		AgentsFilesOverride: func(sdk.AgentsFilesResult) sdk.AgentsFilesResult {
			return sdk.AgentsFilesResult{AgentsFiles: []sdk.AgentsFile{{Path: "/virtual/AGENTS.md", Content: "virtual"}}}
		},
	}))
	check(overridden.Reload(nil))
	r["overridden"] = resourceReport(overridden)

	check(plain.ExtendResources(sdk.ResourceExtensionPaths{SkillPaths: []sdk.ExtensionResourcePath{{Path: filepath.Join(extraSkills, "beta"), Metadata: sdk.PathMetadata{Source: "test", Scope: "project", Origin: "top-level"}}}}))
	r["extended"] = skillReport(ok(plain.GetSkills()))

	settings := ok(sdk.SettingsManagerInMemory(ctx, map[string]any{"skills": []string{extraSkills}}, nil))
	viaSettings := ok(sdk.NewDefaultResourceLoader(ctx, sdk.DefaultResourceLoaderOptions{Cwd: cwd, AgentDir: agentDir, SettingsManager: &settings}))
	check(viaSettings.Reload(nil))
	r["viaSettings"] = skillReport(ok(viaSettings.GetSkills()))

	calls := 0
	trusting := ok(sdk.NewDefaultResourceLoader(ctx, sdk.DefaultResourceLoaderOptions{Cwd: cwd, AgentDir: agentDir}))
	check(trusting.Reload(&sdk.ResourceLoaderReloadOptions{ResolveProjectTrust: func(result sdk.LoadExtensionsResult) bool {
		calls++
		return true
	}}))
	r["trust"] = map[string]any{"calls": calls, "skills": len(ok(trusting.GetSkills()).Skills)}
	bootstrap := ok(trusting.LoadProjectTrustExtensions())
	r["projectTrust"] = map[string]any{"extensions": len(bootstrap.Extensions), "errors": len(bootstrap.Errors)}
	_ = strings.TrimSpace
	return r
}
