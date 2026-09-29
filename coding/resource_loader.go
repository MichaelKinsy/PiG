// Ports packages/coding-agent/src/core/resource-loader.ts (the ResourceLoader contract) and the resourceLoader option of packages/coding-agent/src/core/sdk.ts.

package coding

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
	"github.com/MichaelKinsy/PiG/internal/resolvepath"
)

// SkillsResult is a loader's skills with the diagnostics found while resolving them.
type SkillsResult struct {
	Skills      []*Skill
	Diagnostics []extension.ResourceDiagnostic
}

// PromptsResult is a loader's prompt templates with the diagnostics found while resolving them.
type PromptsResult struct {
	Prompts     []PromptTemplate
	Diagnostics []extension.ResourceDiagnostic
}

// ContextFile is a loaded project context file (AGENTS.md or CLAUDE.md).
type ContextFile = icodingagent.ContextFile

// AgentsFilesResult is a loader's context files.
type AgentsFilesResult struct {
	AgentsFiles []ContextFile
}

// ResourceLoader supplies the resolved resources a Session reads while it runs. A Session reads the loader on every use, so a loader that reloads is observed by the next prompt and the next system prompt rebuild.
// It carries the skills, prompt templates, context files and system prompt text a Session consumes (resource-loader.ts ResourceLoader); the Extension Host, themes and extension resources are owned elsewhere in PiG.
type ResourceLoader interface {
	GetSkills() SkillsResult
	GetPrompts() PromptsResult
	GetAgentsFiles() AgentsFilesResult
	// GetSystemPrompt returns the system prompt text that replaces the default prompt, and whether the loader has one.
	GetSystemPrompt() (string, bool)
	// GetAppendSystemPrompt returns the texts appended to the system prompt.
	GetAppendSystemPrompt() []string
}

// DefaultResourceLoaderOptions configures NewDefaultResourceLoader (resource-loader.ts DefaultResourceLoaderOptions). Extension, theme and inline-extension options belong to the Extension Host and are not part of this loader.
type DefaultResourceLoaderOptions struct {
	CWD      string
	AgentDir string
	// SettingsManager supplies the project trust, the settings-configured resource paths and the Packages. Nil reads the settings files under CWD and AgentDir with trusted project settings.
	SettingsManager *SettingsManager
	// AdditionalSkillPaths are skill files or directories loaded after the resolved skills; they are the loader's explicit paths (resource-loader.ts additionalSkillPaths).
	AdditionalSkillPaths []string
	// AdditionalPromptTemplatePaths are prompt template files or directories loaded after the resolved prompt templates.
	AdditionalPromptTemplatePaths []string
	// NoSkills keeps only AdditionalSkillPaths.
	NoSkills bool
	// NoPromptTemplates keeps only AdditionalPromptTemplatePaths.
	NoPromptTemplates bool
	// NoContextFiles loads no AGENTS.md or CLAUDE.md context files.
	NoContextFiles bool
	// SystemPrompt is the system prompt text, or the path of a file holding it, that replaces discovery of SYSTEM.md. Nil discovers SYSTEM.md.
	SystemPrompt *string
	// AppendSystemPrompt is the appended texts, or paths of files holding them, that replace discovery of APPEND_SYSTEM.md. Nil discovers it; an empty non-nil list appends nothing.
	AppendSystemPrompt []string
	// SkillsOverride, PromptsOverride, AgentsFilesOverride, SystemPromptOverride and AppendSystemPromptOverride transform what the loader found (resource-loader.ts *Override).
	SkillsOverride             func(SkillsResult) SkillsResult
	PromptsOverride            func(PromptsResult) PromptsResult
	AgentsFilesOverride        func(AgentsFilesResult) AgentsFilesResult
	SystemPromptOverride       func(*string) *string
	AppendSystemPromptOverride func([]string) []string
}

// DefaultResourceLoader resolves skills, prompt templates, context files and system prompt files from the default user and project resource locations, the settings-configured paths, the configured Packages and the additional paths, applying project trust as Pi's does, and installs a missing npm or git Package as Pi's package manager does. It does not load extensions or themes, which the Extension Host and the interactive theme controller own. A new loader is empty until Reload runs, as Pi's DefaultResourceLoader is.
type DefaultResourceLoader struct {
	cwd      string
	agentDir string
	settings *SettingsManager
	options  DefaultResourceLoaderOptions

	mu           sync.RWMutex
	skills       SkillsResult
	prompts      PromptsResult
	agentsFiles  []ContextFile
	systemPrompt *string
	appendPrompt []string
}

// NewDefaultResourceLoader returns an empty loader over the options' cwd and agent directory, each resolved with resolvePath as the resource-loader.ts DefaultResourceLoader constructor does ("~" expansion, file URL, absolute path). Call Reload to discover resources.
func NewDefaultResourceLoader(opts DefaultResourceLoaderOptions) *DefaultResourceLoader {
	opts.CWD, opts.AgentDir = resolveLoaderDirectory(opts.CWD), resolveLoaderDirectory(opts.AgentDir)
	settings := opts.SettingsManager
	if settings == nil {
		settings = icodingagent.NewSettingsManager(opts.CWD, opts.AgentDir)
	}
	return &DefaultResourceLoader{
		cwd: opts.CWD, agentDir: opts.AgentDir, settings: settings, options: opts,
		skills:  SkillsResult{Skills: []*Skill{}, Diagnostics: []extension.ResourceDiagnostic{}},
		prompts: PromptsResult{Prompts: []PromptTemplate{}, Diagnostics: []extension.ResourceDiagnostic{}},
	}
}

// Reload reloads the settings, then rediscovers every resource for their current project trust (resource-loader.ts reload). Reloading the settings discards transient overrides applied with ApplyOverrides. A failure while resolving the Packages, settings entries or skills leaves the previous results in place; a failure while resolving the prompt templates keeps the new skills and the previous other results, as Pi's reload does.
func (l *DefaultResourceLoader) Reload() error {
	l.settings.Reload()
	return l.discover()
}

// discover rediscovers every resource from the settings as they currently are.
func (l *DefaultResourceLoader) discover() error {
	trusted := l.settings.IsProjectTrusted()
	if err := packagemanager.ValidateConfiguredPackageSources(l.cwd, l.agentDir, l.settings); err != nil {
		return fmt.Errorf("coding: resolve Packages: %w", err)
	}
	if err := packagemanager.InstallMissingPackages(l.cwd, l.agentDir, l.settings); err != nil {
		return fmt.Errorf("coding: resolve Packages: %w", err)
	}
	if err := packagemanager.ValidateConfiguredResourceEntries(l.cwd, l.agentDir, l.settings); err != nil {
		return fmt.Errorf("coding: resolve settings entries: %w", err)
	}
	packageItems, _ := packagemanager.CollectResolvedPackageResourceItems(l.cwd, l.agentDir, l.settings, nil, false)
	var packageSkills, packagePrompts []icodingagent.ResolvedResource
	for _, item := range packageItems {
		resource := icodingagent.ResolvedResource{Path: item.Path, Enabled: item.Enabled, Metadata: icodingagent.PathMetadata{Source: item.Source, Scope: item.Scope, Origin: item.Origin, BaseDir: item.BaseDir}}
		switch {
		case packagecontent.Kind(item.ResourceType) == packagecontent.Skills:
			packageSkills = append(packageSkills, resource)
		case packagecontent.Kind(item.ResourceType) == packagecontent.Prompts:
			packagePrompts = append(packagePrompts, resource)
		}
	}
	metadata := &pathMetadataIndex{}
	skills, err := l.loadSkills(trusted, packageSkills, metadata)
	if err != nil {
		return err
	}
	// Pi's reload assigns the skills before it resolves the prompt template paths, so a later rejection keeps them (resource-loader.ts:468-488).
	l.mu.Lock()
	l.skills = skills
	l.mu.Unlock()
	prompts, err := l.loadPrompts(trusted, packagePrompts, metadata)
	if err != nil {
		return err
	}
	agentsFiles := AgentsFilesResult{AgentsFiles: []ContextFile{}}
	if !l.options.NoContextFiles {
		agentsFiles.AgentsFiles = icodingagent.LoadProjectContextFiles(l.cwd, l.agentDir)
	}
	if l.options.AgentsFilesOverride != nil {
		agentsFiles = l.options.AgentsFilesOverride(agentsFiles)
	}
	systemPrompt := l.loadSystemPrompt(trusted)
	appendPrompt := l.loadAppendSystemPrompt(trusted)
	l.mu.Lock()
	l.prompts = prompts
	l.agentsFiles = agentsFiles.AgentsFiles
	l.systemPrompt, l.appendPrompt = systemPrompt, appendPrompt
	l.mu.Unlock()
	return nil
}

func (l *DefaultResourceLoader) loadSkills(trusted bool, packageSkills []icodingagent.ResolvedResource, metadata *pathMetadataIndex) (SkillsResult, error) {
	resolved := icodingagent.OrderResolvedResources(icodingagent.SkillEntryResources(packageSkills), icodingagent.SkillEntryResources(icodingagent.AmbientSkillResources(l.cwd, l.agentDir, l.settings, trusted, true)))
	for _, resource := range resolved {
		metadata.add(resource.Path, resource.Metadata)
	}
	mapped := make([]string, 0, len(resolved))
	for _, resource := range resolved {
		if resource.Enabled {
			mapped = append(mapped, mapSkillPath(resource, metadata))
		}
	}
	var enabled []string
	if !l.options.NoSkills {
		enabled = mapped
	}
	paths, err := l.mergePaths(enabled, l.options.AdditionalSkillPaths)
	if err != nil {
		return SkillsResult{}, err
	}
	result := SkillsResult{Skills: []*Skill{}, Diagnostics: []extension.ResourceDiagnostic{}}
	if !l.options.NoSkills || len(paths) > 0 {
		loaded, err := icodingagent.LoadSkills(icodingagent.LoadSkillsOptions{CWD: l.cwd, AgentDir: l.agentDir, SkillPaths: paths})
		if err != nil {
			return SkillsResult{}, fmt.Errorf("coding: load skills: %w", err)
		}
		result = SkillsResult{Skills: loaded.Skills, Diagnostics: loaded.Diagnostics}
	}
	if l.options.SkillsOverride != nil {
		result = l.options.SkillsOverride(result)
	}
	skills := make([]*Skill, 0, len(result.Skills))
	for _, skill := range result.Skills {
		stamped := *skill
		if info, found := metadata.sourceInfo(skill.Path); found {
			stamped.SourceInfo = info
		} else if stamped.SourceInfo == (icodingagent.PiSourceInfo{}) {
			stamped.SourceInfo = icodingagent.DefaultSourceInfoForPath(l.cwd, l.agentDir, skill.Path)
		}
		skills = append(skills, &stamped)
	}
	diagnostics := cloneCollection(result.Diagnostics)
	for _, path := range l.options.AdditionalSkillPaths {
		resolved, err := l.resolveResourcePath(path)
		if err != nil {
			return SkillsResult{}, err
		}
		if icodingagent.IsLocalPath(path) && !exists(resolved) && !hasDiagnosticPath(diagnostics, resolved) {
			diagnostics = append(diagnostics, extension.ResourceDiagnostic{Type: "error", Message: "Skill path does not exist", Path: resolved})
		}
	}
	return SkillsResult{Skills: skills, Diagnostics: diagnostics}, nil
}

// mapSkillPath is resource-loader.ts mapSkillPath: an enabled auto-discovered or Package skill directory that holds a SKILL.md loads through that file, which takes the directory's metadata unless it already has some.
func mapSkillPath(resource icodingagent.ResolvedResource, metadata *pathMetadataIndex) string {
	if resource.Metadata.Source != "auto" && resource.Metadata.Origin != "package" {
		return resource.Path
	}
	if !isDirectory(resource.Path) {
		return resource.Path
	}
	file := filepath.Join(resource.Path, "SKILL.md")
	if !exists(file) {
		return resource.Path
	}
	metadata.add(file, resource.Metadata)
	return file
}

func (l *DefaultResourceLoader) loadPrompts(trusted bool, packagePrompts []icodingagent.ResolvedResource, metadata *pathMetadataIndex) (PromptsResult, error) {
	resolved := icodingagent.OrderResolvedResources(packagePrompts, icodingagent.AmbientPromptResources(l.cwd, l.agentDir, l.settings, trusted))
	for _, resource := range resolved {
		metadata.add(resource.Path, resource.Metadata)
	}
	var enabled []string
	if !l.options.NoPromptTemplates {
		enabled = icodingagent.ResourcePaths(resolved)
	}
	paths, err := l.mergePaths(enabled, l.options.AdditionalPromptTemplatePaths)
	if err != nil {
		return PromptsResult{}, err
	}
	result := PromptsResult{Prompts: []PromptTemplate{}, Diagnostics: []extension.ResourceDiagnostic{}}
	if !l.options.NoPromptTemplates || len(paths) > 0 {
		loaded := icodingagent.LoadPromptTemplates("", "", paths...)
		result = PromptsResult{Prompts: loaded.Templates, Diagnostics: loaded.Diagnostics}
	}
	if l.options.PromptsOverride != nil {
		result = l.options.PromptsOverride(result)
	}
	prompts := make([]PromptTemplate, 0, len(result.Prompts))
	for _, prompt := range result.Prompts {
		if info, found := metadata.sourceInfo(prompt.FilePath); found {
			prompt.SourceInfo = info
		} else if prompt.SourceInfo == (icodingagent.PiSourceInfo{}) {
			prompt.SourceInfo = icodingagent.DefaultSourceInfoForPath(l.cwd, l.agentDir, prompt.FilePath)
		}
		if prompt.SourceInfo.Scope == "user" || prompt.SourceInfo.Scope == "project" {
			prompt.Scope = prompt.SourceInfo.Scope
		}
		prompts = append(prompts, prompt)
	}
	diagnostics := cloneCollection(result.Diagnostics)
	for _, path := range l.options.AdditionalPromptTemplatePaths {
		resolved, err := l.resolveResourcePath(path)
		if err != nil {
			return PromptsResult{}, err
		}
		if icodingagent.IsLocalPath(path) && !exists(resolved) && !hasDiagnosticPath(diagnostics, resolved) {
			diagnostics = append(diagnostics, extension.ResourceDiagnostic{Type: "error", Message: "Prompt template path does not exist", Path: resolved})
		}
	}
	return PromptsResult{Prompts: prompts, Diagnostics: diagnostics}, nil
}

// loadSystemPrompt resolves the system prompt text as resource-loader.ts reload does: the option, else the discovered SYSTEM.md, read through resolvePromptInput.
func (l *DefaultResourceLoader) loadSystemPrompt(trusted bool) *string {
	source := ""
	if l.options.SystemPrompt != nil {
		source = *l.options.SystemPrompt
	} else {
		source = icodingagent.DiscoverPromptFile(l.cwd, l.agentDir, "SYSTEM.md", trusted)
	}
	var systemPrompt *string
	if source != "" {
		systemPrompt = new(icodingagent.ResolvePromptInput(source, "system prompt"))
	}
	if l.options.SystemPromptOverride != nil {
		systemPrompt = l.options.SystemPromptOverride(systemPrompt)
	}
	return systemPrompt
}

func (l *DefaultResourceLoader) loadAppendSystemPrompt(trusted bool) []string {
	sources := l.options.AppendSystemPrompt
	if sources == nil {
		sources = []string{}
		if discovered := icodingagent.DiscoverPromptFile(l.cwd, l.agentDir, "APPEND_SYSTEM.md", trusted); discovered != "" {
			sources = append(sources, discovered)
		}
	}
	appended := make([]string, 0, len(sources))
	for _, source := range sources {
		if source != "" {
			appended = append(appended, icodingagent.ResolvePromptInput(source, "append system prompt"))
		}
	}
	if l.options.AppendSystemPromptOverride != nil {
		appended = l.options.AppendSystemPromptOverride(appended)
	}
	return appended
}

// resolveLoaderDirectory resolves a cwd or agentDir option. Pi requires both, so an empty Go value stays empty rather than becoming the process directory.
func resolveLoaderDirectory(dir string) string {
	if dir == "" {
		return ""
	}
	if resolved, err := resolvepath.Resolve(dir, ""); err == nil {
		return resolved
	}
	return dir
}

// resolveResourcePath is resource-loader.ts resolveResourcePath: resolvePath(p, cwd, { trim: true }). Its error is the throw of fileURLToPath on a malformed file URL (utils/paths.ts:95-106), which rejects the reload.
func (l *DefaultResourceLoader) resolveResourcePath(path string) (string, error) {
	resolved, err := resolvepath.ResolveTrimmed(path, l.cwd)
	if err != nil {
		return "", fmt.Errorf("coding: resolve resource path %q: %w", path, err)
	}
	return resolved, nil
}

// mergePaths is resource-loader.ts mergePaths: the primary paths then the additional ones, resolved and deduplicated by canonical path.
func (l *DefaultResourceLoader) mergePaths(primary, additional []string) ([]string, error) {
	merged := make([]string, 0, len(primary)+len(additional))
	seen := make(map[string]struct{}, len(primary)+len(additional))
	for _, path := range slices.Concat(primary, additional) {
		resolved, err := l.resolveResourcePath(path)
		if err != nil {
			return nil, err
		}
		canonical := icodingagent.CanonicalizePath(resolved)
		if _, duplicate := seen[canonical]; duplicate {
			continue
		}
		seen[canonical] = struct{}{}
		merged = append(merged, resolved)
	}
	return merged, nil
}

// pathMetadataIndex is resource-loader.ts metadataByPath: the metadata of the first resolved resource at each path, in resolution order.
type pathMetadataIndex struct {
	paths    []string
	metadata map[string]icodingagent.PathMetadata
}

func (i *pathMetadataIndex) add(path string, metadata icodingagent.PathMetadata) {
	if _, present := i.metadata[path]; present {
		return
	}
	if i.metadata == nil {
		i.metadata = map[string]icodingagent.PathMetadata{}
	}
	i.paths = append(i.paths, path)
	i.metadata[path] = metadata
}

// sourceInfo is resource-loader.ts findSourceInfoForPath over metadataByPath: the exact path, else the first resolved path that contains it.
func (i *pathMetadataIndex) sourceInfo(resourcePath string) (icodingagent.PiSourceInfo, bool) {
	if resourcePath == "" {
		return icodingagent.PiSourceInfo{}, false
	}
	normalized, err := filepath.Abs(resourcePath)
	if err != nil {
		normalized = resourcePath
	}
	if metadata, found := i.metadata[normalized]; found {
		return metadata.SourceInfo(resourcePath), true
	}
	if metadata, found := i.metadata[resourcePath]; found {
		return metadata.SourceInfo(resourcePath), true
	}
	for _, sourcePath := range i.paths {
		source, err := filepath.Abs(sourcePath)
		if err != nil {
			source = sourcePath
		}
		if normalized == source || strings.HasPrefix(normalized, source+string(filepath.Separator)) {
			return i.metadata[sourcePath].SourceInfo(resourcePath), true
		}
	}
	return icodingagent.PiSourceInfo{}, false
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func hasDiagnosticPath(diagnostics []extension.ResourceDiagnostic, path string) bool {
	return slices.ContainsFunc(diagnostics, func(d extension.ResourceDiagnostic) bool { return d.Path == path })
}

// GetSkills returns the skills found by the last Reload.
func (l *DefaultResourceLoader) GetSkills() SkillsResult {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return SkillsResult{Skills: cloneCollection(l.skills.Skills), Diagnostics: cloneCollection(l.skills.Diagnostics)}
}

// GetPrompts returns the prompt templates found by the last Reload.
func (l *DefaultResourceLoader) GetPrompts() PromptsResult {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return PromptsResult{Prompts: cloneCollection(l.prompts.Prompts), Diagnostics: cloneCollection(l.prompts.Diagnostics)}
}

// GetAgentsFiles returns the context files found by the last Reload.
func (l *DefaultResourceLoader) GetAgentsFiles() AgentsFilesResult {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return AgentsFilesResult{AgentsFiles: cloneCollection(l.agentsFiles)}
}

// GetSystemPrompt returns the SYSTEM.md text found by the last Reload.
func (l *DefaultResourceLoader) GetSystemPrompt() (string, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.systemPrompt == nil {
		return "", false
	}
	return *l.systemPrompt, true
}

// GetAppendSystemPrompt returns the APPEND_SYSTEM.md text found by the last Reload.
func (l *DefaultResourceLoader) GetAppendSystemPrompt() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return cloneCollection(l.appendPrompt)
}

// staticResourceLoader serves collections its owner resolved elsewhere.
type staticResourceLoader struct {
	templates []PromptTemplate
	skills    []*Skill
}

func (l *staticResourceLoader) GetSkills() SkillsResult {
	return SkillsResult{Skills: cloneCollection(l.skills), Diagnostics: []extension.ResourceDiagnostic{}}
}

func (l *staticResourceLoader) GetPrompts() PromptsResult {
	return PromptsResult{Prompts: cloneCollection(l.templates), Diagnostics: []extension.ResourceDiagnostic{}}
}

func (*staticResourceLoader) GetAgentsFiles() AgentsFilesResult {
	return AgentsFilesResult{AgentsFiles: []ContextFile{}}
}

func (*staticResourceLoader) GetSystemPrompt() (string, bool) { return "", false }

func (*staticResourceLoader) GetAppendSystemPrompt() []string { return []string{} }

// promptResourcesOverlay replaces a loader's skills and prompt templates with collections its owner resolved elsewhere and keeps the loader's other resources.
type promptResourcesOverlay struct {
	ResourceLoader
	static *staticResourceLoader
}

func (o promptResourcesOverlay) GetSkills() SkillsResult   { return o.static.GetSkills() }
func (o promptResourcesOverlay) GetPrompts() PromptsResult { return o.static.GetPrompts() }

// cloneCollection copies a loader collection, reporting an absent one as empty: Pi's loaders always return arrays (resource-loader.ts getSkills, getPrompts).
func cloneCollection[T any](collection []T) []T {
	if collection == nil {
		return []T{}
	}
	return slices.Clone(collection)
}

// NoResources is a ResourceLoader with no resources. A caller that expands resources and builds its system prompt itself supplies it so the Session neither discovers, expands nor renders any.
var NoResources ResourceLoader = &staticResourceLoader{}

// resourceLoaderRef is the loader a Session is bound to. promptFromLoader reports that the Session builds its default system prompt from the loader (agent-session.ts _rebuildSystemPrompt); a Session bound to NoResources leaves the prompt to its caller.
type resourceLoaderRef struct {
	loader           ResourceLoader
	promptFromLoader bool
}

// resolveResourceLoader returns the supplied loader, or a DefaultResourceLoader over the Services' cwd, agent directory and settings that has reloaded when none is supplied (sdk.ts createAgentSession awaits resourceLoader.reload()). The reload rereads the Services' settings and drops the transient overrides applied to them.
func resolveResourceLoader(svcs *Services, supplied ResourceLoader) (*resourceLoaderRef, error) {
	if supplied != nil {
		return &resourceLoaderRef{loader: supplied, promptFromLoader: supplied != NoResources}, nil
	}
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: svcs.CWD(), AgentDir: svcs.AgentDir(), SettingsManager: svcs.SettingsManager()})
	if err := loader.Reload(); err != nil {
		return nil, err
	}
	return &resourceLoaderRef{loader: loader, promptFromLoader: true}, nil
}
