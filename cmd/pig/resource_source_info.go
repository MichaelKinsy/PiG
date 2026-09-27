package main

import (
	"path/filepath"
	"strings"

	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
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
				infos[item.Path] = sourceInfoFromResourceItem(item)
			}
		}
		for _, p := range promptPaths {
			addInferredSourceInfo(infos, p, cwd, agentDir, "prompts")
		}
		for _, p := range skillInputs {
			addInferredSourceInfo(infos, p, cwd, agentDir, "skills")
		}
		for _, p := range themePaths {
			addInferredSourceInfo(infos, p, cwd, agentDir, "themes")
		}
		for _, cfg := range extConfigs {
			path := cfg.Source
			if path == "" {
				path = cfg.Path
			}
			addInferredSourceInfo(infos, path, cwd, agentDir, "extensions")
		}
		return infos
	}
}

func sourceInfoFromResourceItem(item tui.ResourceItem) codingagent.ResourceSourceInfo {
	return codingagent.ResourceSourceInfo{
		Path:         item.Path,
		ResourceType: string(item.ResourceType),
		Enabled:      item.Enabled,
		Scope:        item.Scope,
		Origin:       item.Origin,
		Source:       item.Source,
		BaseDir:      item.BaseDir,
	}
}

func addInferredSourceInfo(infos map[string]codingagent.ResourceSourceInfo, path, cwd, agentDir, kind string) {
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
	switch {
	case agentDir != "" && isWithin(path, filepath.Join(agentDir, kind)):
		info.Scope = "user"
	case cwd != "" && isWithin(path, filepath.Join(cwd, ".pig", kind)):
		info.Scope = "project"
	default:
		// A path named on the command line (--prompt-template, --skill,
		// --theme) is temporary, as upstream resolves CLI resources with
		// {temporary: true} (resource-loader.ts).
		info.Scope = "temporary"
	}
	infos[path] = info
}

func isWithin(path, base string) bool {
	if path == "" || base == "" {
		return false
	}
	ap, err1 := filepath.Abs(path)
	ab, err2 := filepath.Abs(base)
	if err1 != nil || err2 != nil {
		return false
	}
	if ap == ab {
		return true
	}
	return strings.HasPrefix(ap, ab+string(filepath.Separator))
}
