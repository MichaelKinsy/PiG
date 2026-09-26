package main

import (
	"context"
	"fmt"
	"maps"
	"os"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
)

// promptSkillsFor lists skills as the system prompt describes them.
func promptSkillsFor(skills []*codingagent.SkillDef) []prompts.Skill {
	out := make([]prompts.Skill, 0, len(skills))
	for _, skill := range skills {
		out = append(out, prompts.Skill{Name: skill.Name, Description: skill.Description, Path: skill.Path, DisableModelInvocation: skill.DisableModelInvocation})
	}
	return out
}

// extendFromExtensions is upstream AgentSession.extendResourcesFromExtensions
// for print, JSON and RPC mode. After session_start it asks the extensions'
// resources_discover handlers for resource paths, records each path's
// discovering extension as its provenance, and adds the skills and prompt
// templates the paths hold after the ones already loaded. It reports whether
// the skills changed, so the caller rebuilds the system prompt.
func (c *headlessCommandCatalog) extendFromExtensions(ctx context.Context, runner *inproc.Runner, reason string) bool {
	if runner == nil || !runner.HasHandlers(codingagent.EventResourcesDiscover) {
		return false
	}
	discovered, err := runner.EmitResourcesDiscover(ctx, c.cwd, reason)
	if err != nil || discovered == nil {
		return false
	}
	if len(discovered.SkillPaths) == 0 && len(discovered.PromptPaths) == 0 && len(discovered.ThemePaths) == 0 {
		return false
	}
	sourceInfo := make(map[string]codingagent.ResourceSourceInfo, len(c.sourceInfo)+len(discovered.SkillPaths)+len(discovered.PromptPaths))
	maps.Copy(sourceInfo, c.sourceInfo)
	record := func(entries []extension.AttributedResourcePath, kind string) []string {
		paths := make([]string, 0, len(entries))
		for _, entry := range entries {
			sourceInfo[entry.Path] = codingagent.ExtensionDiscoveredSourceInfo(entry.Path, kind, entry.ExtensionPath)
			paths = append(paths, entry.Path)
		}
		return paths
	}
	skillPaths := record(discovered.SkillPaths, "skills")
	promptPaths := record(discovered.PromptPaths, "prompts")
	record(discovered.ThemePaths, "themes")
	c.sourceInfo = sourceInfo

	if len(promptPaths) > 0 {
		templates := append([]codingagent.PromptTemplate(nil), c.promptTemplates...)
		seen := make(map[string]struct{}, len(templates))
		for _, template := range templates {
			seen[template.Name] = struct{}{}
		}
		for _, template := range codingagent.LoadPromptTemplates("", "", promptPaths...).Templates {
			if _, duplicate := seen[template.Name]; duplicate {
				continue
			}
			seen[template.Name] = struct{}{}
			templates = append(templates, template)
		}
		c.promptTemplates = templates
	}
	if len(skillPaths) == 0 {
		return false
	}
	skills := append([]*codingagent.SkillDef(nil), c.skills...)
	for _, path := range skillPaths {
		loaded, err := codingagent.LoadSkillsFromPath(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skill %s: %v\n", path, err)
		}
		for _, skill := range loaded {
			for _, diagnostic := range codingagent.SkillDiagnostics(skill) {
				fmt.Fprintf(os.Stderr, "warning: %s: %s\n", skill.Path, diagnostic)
			}
			if strings.TrimSpace(skill.Description) != "" {
				skills = append(skills, skill)
			}
		}
	}
	c.skills = codingagent.DeduplicateSkills(skills)
	return true
}
