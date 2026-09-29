package main

// Ports packages/coding-agent/src/core/resource-loader.ts (mapSkillPath and resource source metadata).

import (
	"os"
	"path/filepath"

	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
	"github.com/MichaelKinsy/PiG/tui"
)

func resourceSourceInfoProvider(cwd, agentDir string, sm *codingagent.SettingsManager, flags CLIFlags, resolvers ...extsource.ResolveFunc) func() map[string]codingagent.ResourceSourceInfo {
	return func() map[string]codingagent.ResourceSourceInfo {
		infos := map[string]codingagent.ResourceSourceInfo{}
		promptPaths := collectPromptPaths(cwd, agentDir, sm, flags, sm.IsProjectTrusted(), resolvers...)
		skillInputs := collectSkillInputs(cwd, agentDir, sm, flags, nil, resolvers...)
		themePaths := collectThemePaths(cwd, agentDir, sm, flags, sm.IsProjectTrusted(), resolvers...)
		extConfigs := collectExtensionConfigs(cwd, agentDir, sm, flags, nil, resolvers...)
		if items, err := collectConfigResourceItems(cwd, agentDir, sm, resolvers...); err == nil {
			for _, item := range items {
				info := sourceInfoFromResourceItem(item)
				if _, exists := infos[info.Path]; !exists {
					infos[info.Path] = info
				}
			}
		}
		seen := make(map[string]bool, len(infos))
		for _, info := range infos {
			seen[info.ResourceType+":"+canonicalStatusPath(info.Path)] = true
		}
		add := func(path, kind string) {
			path = resourceMetadataPath(path, kind)
			key := kind + ":" + canonicalStatusPath(path)
			if !seen[key] {
				seen[key] = true
				addInferredSourceInfo(infos, path, cwd, agentDir, kind)
			}
		}
		for _, p := range promptPaths {
			add(p, "prompts")
		}
		for _, p := range skillInputs {
			add(p, "skills")
		}
		for _, p := range themePaths {
			add(p, "themes")
		}
		for _, cfg := range extConfigs {
			path := cfg.Source
			if path == "" {
				path = cfg.Path
			}
			add(path, "extensions")
		}
		return infos
	}
}

// sourceInfoFromResourceItem returns Pi's PathMetadata. Skill bundles expose their SKILL.md entry, and a settings entry omits baseDir.
func sourceInfoFromResourceItem(item tui.ResourceItem) codingagent.ResourceSourceInfo {
	baseDir := item.BaseDir
	if item.Origin == "top-level" && item.Source == "local" {
		baseDir = ""
	}
	return codingagent.ResourceSourceInfo{
		Path:         resourceMetadataPath(item.Path, string(item.ResourceType)),
		ResourceType: string(item.ResourceType),
		Enabled:      item.Enabled,
		Scope:        item.Scope,
		Origin:       item.Origin,
		Source:       item.Source,
		BaseDir:      baseDir,
	}
}

func resourceMetadataPath(path, kind string) string {
	if kind == "skills" {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			file := filepath.Join(path, "SKILL.md")
			if info, err := os.Stat(file); err == nil && !info.IsDir() {
				return file
			}
		}
	}
	return path
}

func addInferredSourceInfo(infos map[string]codingagent.ResourceSourceInfo, path, cwd, agentDir, kind string) {
	path = resourceMetadataPath(path, kind)
	if path == "" {
		return
	}
	if _, ok := infos[path]; ok {
		return
	}
	info := codingagent.ResourceSourceInfo{
		Path:         path,
		ResourceType: kind,
		Enabled:      true,
		Origin:       "top-level",
		Source:       "local",
	}
	// Upstream getDefaultSourceInfoForPath.
	switch {
	case agentDir != "" && packagemanager.IsWithin(path, filepath.Join(agentDir, kind)):
		info.Scope, info.BaseDir = "user", filepath.Join(agentDir, kind)
	case cwd != "" && packagemanager.IsWithin(path, filepath.Join(codingagent.ProjectConfigDir(cwd), kind)):
		info.Scope, info.BaseDir = "project", filepath.Join(codingagent.ProjectConfigDir(cwd), kind)
	default:
		// A path named on the command line (--prompt-template, --skill,
		// --theme) is temporary, as upstream resolves CLI resources with
		// {temporary: true} (resource-loader.ts).
		info.Scope = "temporary"
		info.BaseDir = filepath.Dir(path)
		if stat, err := os.Stat(path); err == nil && stat.IsDir() {
			info.BaseDir = path
		}
	}
	infos[path] = info
}
