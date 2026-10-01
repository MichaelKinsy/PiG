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
)

// ReloadResourceSnapshot is the recomputed settings/resource view used by
// /reload. It mirrors the upstream resource-loader/session.reload flow where
// prompt/theme/skill/context inputs are re-resolved from current settings.
type ReloadResourceSnapshot struct {
	PromptPaths  []string
	ThemePaths   []string
	SkillPaths   []string
	ContextFiles []ContextFile
	// SystemPromptSourcePaths are the system prompt files the loaded
	// resources [Context] section lists before ContextFiles.
	SystemPromptSourcePaths []string
	ResourceSourceInfo      map[string]ResourceSourceInfo
	// Err is the error resource resolution threw (an invalid settings file:
	// URL); /reload fails with it and applies nothing.
	Err error
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
	m.opts.SystemPromptSourcePaths = append([]string(nil), snapshot.SystemPromptSourcePaths...)
	if snapshot.ResourceSourceInfo != nil {
		m.resourceSourceInfo = cloneResourceSourceInfoMap(snapshot.ResourceSourceInfo)
	}
	m.publishSlashCommandCatalog()
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
	// A Session owns the active selection across a reload: tools newly added to defaultTools are active, removed ones stay active and tools disabled during the session stay disabled (agent-session.ts:3591-3609). Rebuilding from the startup selection alone would undo that.
	session, ownsSelection := m.opts.SessionHandle.(interface {
		ActiveToolNames() []string
		SetActiveToolsByName([]string)
	})
	var active []string
	if ownsSelection {
		active = session.ActiveToolNames()
	}
	allTools, errs := m.buildAgentTools()
	m.agent.SetTools(allTools)
	if ownsSelection {
		session.SetActiveToolsByName(active)
	}
	m.rebuildToolSystemPrompt()
	return errs
}

func (m *InteractiveMode) buildAgentTools() ([]agent.AgentTool, []error) {
	builtin := tools.CreateAllTools(m.opts.CWD, m.opts.Settings, filepath.Join(m.opts.AgentDir, "bin"))
	// An explicit tool set (--tools, or an extension's setActiveTools) names the
	// active built-ins itself; the startup default set applies only without one.
	active := m.opts.ActiveBuiltinTools
	if m.opts.AllowedTools != nil {
		active = nil
	}
	allTools := tools.SelectBuiltinTools(builtin, active, m.opts.AllowedTools)
	if m.newRunner != nil && m.opts.BridgeExtensionTools != nil {
		bridged, errs := m.opts.BridgeExtensionTools(m.newRunner.Tools())
		allTools = append(allTools, bridged...)
		allTools = deduplicateAgentTools(allTools)
		allTools = filterAllowedAgentTools(allTools, m.opts.AllowedTools)
		allTools = removeExcludedAgentTools(allTools, m.opts.ExcludedTools)
		return allTools, errs
	}
	allTools = deduplicateAgentTools(allTools)
	allTools = filterAllowedAgentTools(allTools, m.opts.AllowedTools)
	allTools = removeExcludedAgentTools(allTools, m.opts.ExcludedTools)
	return allTools, nil
}

func (m *InteractiveMode) replaceExtensionRunner(exts []extension.Extension) []error {
	previousRunner := m.newRunner
	allExts := ExtensionsInLoadOrder(exts, m.opts.BuiltinExtensions)
	// The runner shares the host's registrations (MCP servers, providers, virtual models) as the Session's first runner does.
	var hostRuntime *extension.ExtensionRuntime
	if m.opts.SubprocessHost != nil && previousRunner != nil {
		hostRuntime = previousRunner.Runtime()
	}
	m.newRunner = inproc.NewRunner(allExts, m.opts.CWD, hostRuntime)
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
	m.reconfigureRemoteEditor()
	return m.refreshToolsAfterReload()
}

// refreshToolsAfterReload rebuilds the Session's tool registry as a reload does, which activates the tools a settings reload newly added to defaultTools and every extension tool that activates on registration, then the agent's tools.
func (m *InteractiveMode) refreshToolsAfterReload() []error {
	var errs []error
	// upstream: agent-session.ts:3604-3609 rebuilds the registry with includeAllExtensionTools, so an extension tool disabled during the session is active again.
	if session, ok := m.opts.SessionHandle.(interface{ RefreshToolsAfterReload() error }); ok {
		if err := session.RefreshToolsAfterReload(); err != nil {
			errs = append(errs, err)
		}
	} else if session, ok := m.opts.SessionHandle.(interface{ RefreshTools() error }); ok {
		if err := session.RefreshTools(); err != nil {
			errs = append(errs, err)
		}
	}
	return append(errs, m.refreshAgentTools()...)
}

// reloadSkillsFromPaths loads the paths already selected by the resolver, including explicit paths when automatic discovery is disabled.
func (m *InteractiveMode) reloadSkillsFromPaths() {
	m.opts.SkillDiagnostics = nil
	if len(m.opts.SkillPaths) == 0 {
		m.opts.Skills = nil
		m.publishSlashCommandCatalog()
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
	m.opts.Skills, m.opts.SkillDiagnostics = DeduplicateSkillsWithDiagnostics(allSkills)
	catalog := SlashCommandCatalog{CWD: m.opts.CWD, AgentDir: m.opts.AgentDir, SourceInfo: m.resourceSourceInfo}
	m.opts.Skills = catalog.WithSkillSources(m.opts.Skills)
	m.publishSlashCommandCatalog()
}

func (m *InteractiveMode) rebuildSystemPromptFromResources() {
	if m.opts.RebuildSystemPrompt == nil {
		return
	}
	systemPrompt, promptOptions := m.opts.RebuildSystemPrompt(m.opts.Skills, m.opts.ContextFiles)
	m.opts.SystemPrompt = systemPrompt
	m.opts.SystemPromptOptions = promptOptions
	m.rebuildToolSystemPrompt()
	if m.agent != nil {
		m.agent.SetSystemPrompt(m.currentSystemPrompt())
	}
}

func mergeUniqueStrings(base []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(base))
	out := make([]string, 0, len(base))
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

// ExtensionDiscoveredSourceInfo is the provenance upstream
// buildExtensionResourcePaths records for a resource path an extension's
// resources_discover handler returned: source "extension:<name>", scope
// "temporary", and the extension's directory as baseDir.
func ExtensionDiscoveredSourceInfo(path, kind, extensionPath string) ResourceSourceInfo {
	return ResourceSourceInfo{
		Path:         path,
		ResourceType: kind,
		Enabled:      true,
		Scope:        "temporary",
		Origin:       "top-level",
		Source:       extensionSourceLabel(extensionPath),
		BaseDir:      extensionBaseDir(extensionPath),
	}
}

// ResourcesDiscoverReason maps a session_start reason to the resources_discover reason. Pi's AgentSession passes "reload" for a reload and "startup" for every other Session start, including one a replacement created (agent-session.ts:2941; the union is "startup" | "reload", extensions/types.ts:551-555).
func ResourcesDiscoverReason(sessionStartReason string) string {
	if sessionStartReason == "reload" {
		return "reload"
	}
	return "startup"
}

// extendResourcesFromExtensions merges extension-discovered resources. Its
// caller shows the final prompt diagnostics once, as upstream
// showLoadedResources does after startup and after reload.
func (m *InteractiveMode) extendResourcesFromExtensions(ctx context.Context, reason string) error {
	runner, cwd := m.newRunner, m.opts.CWD
	if runner == nil || !runner.HasHandlers(EventResourcesDiscover) {
		return nil
	}
	var agg *extension.ResourcesDiscoverAggregateResult
	await := m.awaitExtensionUI
	if reason == "reload" {
		await = m.awaitReloadStep
	}
	if err := await(ctx, func(ctx context.Context) error {
		var err error
		agg, err = runner.EmitResourcesDiscover(ctx, cwd, reason)
		return err
	}); err != nil {
		return err
	}
	return m.applyDiscoveredResources(agg)
}

func (m *InteractiveMode) applyDiscoveredResources(agg *extension.ResourcesDiscoverAggregateResult) error {
	var err error
	agg, err = NormalizeExtensionPaths(m.opts.CWD, agg)
	if err != nil {
		return err
	}
	if agg == nil {
		return nil
	}
	if len(agg.SkillPaths) == 0 && len(agg.PromptPaths) == 0 && len(agg.ThemePaths) == 0 {
		return nil
	}
	if m.resourceSourceInfo == nil {
		m.resourceSourceInfo = map[string]ResourceSourceInfo{}
	}
	for _, entry := range agg.SkillPaths {
		m.opts.SkillPaths = mergeUniqueStrings(m.opts.SkillPaths, entry.Path)
		m.resourceSourceInfo[entry.Path] = ExtensionDiscoveredSourceInfo(entry.Path, "skills", entry.ExtensionPath)
	}
	for _, entry := range agg.PromptPaths {
		m.opts.PromptPaths = mergeUniqueStrings(m.opts.PromptPaths, entry.Path)
		m.resourceSourceInfo[entry.Path] = ExtensionDiscoveredSourceInfo(entry.Path, "prompts", entry.ExtensionPath)
	}
	for _, entry := range agg.ThemePaths {
		m.opts.ThemePaths = mergeUniqueStrings(m.opts.ThemePaths, entry.Path)
		m.resourceSourceInfo[entry.Path] = ExtensionDiscoveredSourceInfo(entry.Path, "themes", entry.ExtensionPath)
	}

	m.loadPromptTemplates()
	m.reloadSkillsFromPaths()
	m.loadThemes()
	m.rebuildSystemPromptFromResources()
	return nil
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
