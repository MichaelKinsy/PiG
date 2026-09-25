package codingagent

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

// ReloadResourceSnapshot is the recomputed settings/resource view used by
// /reload. It mirrors the upstream resource-loader/session.reload flow where
// prompt/theme/skill/context inputs are re-resolved from current settings.
type ReloadResourceSnapshot struct {
	PromptPaths        []string
	ThemePaths         []string
	SkillPaths         []string
	ContextFiles       []ContextFile
	ResourceSourceInfo map[string]ResourceSourceInfo
}

func cloneResourceSourceInfoMap(in map[string]ResourceSourceInfo) map[string]ResourceSourceInfo {
	if in == nil {
		return nil
	}
	out := make(map[string]ResourceSourceInfo, len(in))
	maps.Copy(out, in)
	return out
}

func (m *InteractiveMode) applyReloadResourceSnapshot(snapshot ReloadResourceSnapshot) {
	m.opts.PromptPaths = append([]string(nil), snapshot.PromptPaths...)
	m.opts.ThemePaths = append([]string(nil), snapshot.ThemePaths...)
	m.opts.SkillPaths = append([]string(nil), snapshot.SkillPaths...)
	m.opts.ContextFiles = append([]ContextFile(nil), snapshot.ContextFiles...)
	if snapshot.ResourceSourceInfo != nil {
		m.resourceSourceInfo = cloneResourceSourceInfoMap(snapshot.ResourceSourceInfo)
	}
}

func deduplicateAgentTools(ts []agent.AgentTool) []agent.AgentTool {
	if len(ts) == 0 {
		return nil
	}
	byName := make(map[string]int, len(ts))
	out := make([]agent.AgentTool, 0, len(ts))
	for _, tool := range ts {
		name := tool.Name()
		if idx, ok := byName[name]; ok {
			out[idx] = tool
			continue
		}
		byName[name] = len(out)
		out = append(out, tool)
	}
	return out
}

func filterAllowedAgentTools(ts []agent.AgentTool, allowed map[string]struct{}) []agent.AgentTool {
	if allowed == nil {
		return ts
	}
	filtered := make([]agent.AgentTool, 0, len(ts))
	for _, tool := range ts {
		if _, ok := allowed[tool.Name()]; ok {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

// removeExcludedAgentTools drops every tool whose name is in the denylist,
// gating built-in and extension tools alike on reload. Mirrors upstream
// isAllowedTool's `!excludedToolNames?.has(name)` (agent-session.ts:2288).
func removeExcludedAgentTools(ts []agent.AgentTool, excluded map[string]struct{}) []agent.AgentTool {
	if len(excluded) == 0 {
		return ts
	}
	filtered := make([]agent.AgentTool, 0, len(ts))
	for _, tool := range ts {
		if _, ok := excluded[tool.Name()]; !ok {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

func (m *InteractiveMode) refreshAgentTools() []error {
	if m.agent == nil {
		return nil
	}
	builtin := tools.CreateAllTools(m.opts.CWD, m.opts.Settings, filepath.Join(m.opts.AgentDir, "bin"))
	allTools := tools.SelectBuiltinTools(builtin, m.opts.ActiveBuiltinTools, m.opts.AllowedTools)
	if m.newRunner != nil && m.opts.BridgeExtensionTools != nil {
		bridged, errs := m.opts.BridgeExtensionTools(m.newRunner.Tools())
		allTools = append(allTools, bridged...)
		allTools = deduplicateAgentTools(allTools)
		allTools = filterAllowedAgentTools(allTools, m.opts.AllowedTools)
		allTools = removeExcludedAgentTools(allTools, m.opts.ExcludedTools)
		m.agent.SetTools(allTools)
		return errs
	}
	allTools = deduplicateAgentTools(allTools)
	allTools = filterAllowedAgentTools(allTools, m.opts.AllowedTools)
	allTools = removeExcludedAgentTools(allTools, m.opts.ExcludedTools)
	m.agent.SetTools(allTools)
	return nil
}

func (m *InteractiveMode) replaceExtensionRunner(exts []extension.Extension) []error {
	previousRunner := m.newRunner
	allExts := ExtensionsInLoadOrder(exts, m.opts.BuiltinExtensions)
	m.newRunner = inproc.NewRunner(allExts, m.opts.CWD)
	m.opts.ExtensionRunner = m.newRunner
	m.wireInprocContextActions()
	if session, ok := m.opts.SessionHandle.(interface{ ReplaceRunner(*inproc.Runner) }); ok {
		session.ReplaceRunner(m.newRunner)
		// ReplaceRunner binds the Session's command actions; keep interactive's.
		m.bindExtensionCommandActions()
	}
	if previousRunner != nil {
		previousRunner.Invalidate("")
	}
	ctx := m.runCtx
	if ctx == nil {
		ctx = context.Background()
	}
	m.setupExtensionShortcutListener(ctx)
	return m.refreshAgentTools()
}

func (m *InteractiveMode) reloadSkillsFromPaths() {
	if m.opts.NoSkills {
		m.opts.Skills = nil
		return
	}
	if len(m.opts.SkillPaths) == 0 {
		m.opts.Skills = nil
		return
	}
	var allSkills []*SkillDef
	for _, skillPath := range m.opts.SkillPaths {
		loaded, err := LoadSkillsFromPath(skillPath)
		if err != nil {
			_, _ = fmt.Fprintf(stderrWriter(), "skill reload %s: %v\n", skillPath, err)
		}
		for _, skill := range loaded {
			for _, diagnostic := range SkillDiagnostics(skill) {
				_, _ = fmt.Fprintf(stderrWriter(), "skill reload %s: %s\n", skill.Path, diagnostic)
			}
			if strings.TrimSpace(skill.Description) != "" {
				allSkills = append(allSkills, skill)
			}
		}
	}
	m.opts.Skills = DeduplicateSkills(allSkills)
}

func (m *InteractiveMode) rebuildSystemPromptFromResources() {
	if m.opts.RebuildSystemPrompt == nil {
		return
	}
	systemPrompt, promptOptions := m.opts.RebuildSystemPrompt(m.opts.Skills, m.opts.ContextFiles)
	m.opts.SystemPrompt = systemPrompt
	m.opts.SystemPromptOptions = promptOptions
	if m.agent != nil {
		m.agent.SetSystemPrompt(systemPrompt)
	}
}

func mergeUniqueStrings(base []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(base)+len(additions))
	out := make([]string, 0, len(base)+len(additions))
	for _, item := range base {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	for _, item := range additions {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func extensionSourceLabel(extensionPath string) string {
	if strings.HasPrefix(extensionPath, "<") {
		return "extension:" + strings.Trim(extensionPath, "<>")
	}
	base := filepath.Base(extensionPath)
	name := strings.TrimSuffix(strings.TrimSuffix(base, ".ts"), ".js")
	return "extension:" + name
}

func extensionBaseDir(extensionPath string) string {
	if strings.HasPrefix(extensionPath, "<") {
		return ""
	}
	return filepath.Dir(extensionPath)
}

// extendResourcesFromExtensions merges extension-discovered resources. Its
// caller shows the final prompt diagnostics once, as upstream
// showLoadedResources does after startup and after reload.
func (m *InteractiveMode) extendResourcesFromExtensions(reason string) {
	if m.newRunner == nil || !m.newRunner.HasHandlers(EventResourcesDiscover) {
		return
	}
	agg, err := m.newRunner.EmitResourcesDiscover(context.Background(), m.opts.CWD, reason)
	if err != nil || agg == nil {
		return
	}
	if len(agg.SkillPaths) == 0 && len(agg.PromptPaths) == 0 && len(agg.ThemePaths) == 0 {
		return
	}
	if m.resourceSourceInfo == nil {
		m.resourceSourceInfo = map[string]ResourceSourceInfo{}
	}
	for _, entry := range agg.SkillPaths {
		m.opts.SkillPaths = mergeUniqueStrings(m.opts.SkillPaths, entry.Path)
		m.resourceSourceInfo[entry.Path] = ResourceSourceInfo{
			Path:         entry.Path,
			ResourceType: "skills",
			Enabled:      true,
			Scope:        "temporary",
			Origin:       "top-level",
			Source:       extensionSourceLabel(entry.ExtensionPath),
			BaseDir:      extensionBaseDir(entry.ExtensionPath),
		}
	}
	for _, entry := range agg.PromptPaths {
		m.opts.PromptPaths = mergeUniqueStrings(m.opts.PromptPaths, entry.Path)
		m.resourceSourceInfo[entry.Path] = ResourceSourceInfo{
			Path:         entry.Path,
			ResourceType: "prompts",
			Enabled:      true,
			Scope:        "temporary",
			Origin:       "top-level",
			Source:       extensionSourceLabel(entry.ExtensionPath),
			BaseDir:      extensionBaseDir(entry.ExtensionPath),
		}
	}
	for _, entry := range agg.ThemePaths {
		m.opts.ThemePaths = mergeUniqueStrings(m.opts.ThemePaths, entry.Path)
		m.resourceSourceInfo[entry.Path] = ResourceSourceInfo{
			Path:         entry.Path,
			ResourceType: "themes",
			Enabled:      true,
			Scope:        "temporary",
			Origin:       "top-level",
			Source:       extensionSourceLabel(entry.ExtensionPath),
			BaseDir:      extensionBaseDir(entry.ExtensionPath),
		}
	}

	if m.opts.NoPromptTemplates {
		m.promptTemplates = nil
	} else {
		m.loadPromptTemplates()
	}
	m.reloadSkillsFromPaths()
	if !m.opts.NoThemes {
		registry := tui.ActiveThemeRegistry()
		for _, themePath := range m.opts.ThemePaths {
			if err := loadThemePath(registry, themePath); err != nil {
				_, _ = fmt.Fprintf(stderrWriter(), "theme reload: %v\n", err)
			}
		}
	}
	m.rebuildSystemPromptFromResources()
}

// stderrWriter is the destination for resource-reload diagnostics.
var stderrWriter = func() interface{ Write([]byte) (int, error) } { return os.Stderr }

func toolNames(ts []agent.AgentTool) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Name())
	}
	slices.Sort(out)
	return out
}
