// Ports packages/coding-agent/src/core/resource-loader.ts (the ResourceLoader contract) and the resourceLoader option of packages/coding-agent/src/core/sdk.ts.

package coding

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/timings"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
	"github.com/MichaelKinsy/PiG/internal/resolvepath"
	"github.com/MichaelKinsy/PiG/tui"
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
	// GetSystemPromptSource returns the file the system prompt was read from, and whether it came from a file.
	GetSystemPromptSource() (ResourceSource, bool)
	// GetAppendSystemPromptSources returns the files the appended texts were read from, in order.
	GetAppendSystemPromptSources() []ResourceSource
	// GetExtensions returns the extensions the last Reload found (resource-loader.ts getExtensions).
	GetExtensions() LoadExtensionsResult
	// GetThemes returns the themes the last Reload and ExtendResources found (resource-loader.ts getThemes).
	GetThemes() ThemesResult
	// ExtendResources adds the skills, prompt templates and themes extensions contributed (resource-loader.ts extendResources).
	ExtendResources(paths ResourceExtensionPaths) error
	// Reload rediscovers every resource (resource-loader.ts reload).
	Reload(options ...ResourceLoaderReloadOptions) error
}

// ResourceSource names the file a loaded resource came from (resource-loader.ts { path: string }).
type ResourceSource struct {
	Path string
}

// DefaultResourceLoaderOptions configures NewDefaultResourceLoader (resource-loader.ts DefaultResourceLoaderOptions). The Extension Host loads extensions: LoadExtensions is the hook it supplies, in place of upstream's additionalExtensionPaths, extensionFactories and noExtensions.
type DefaultResourceLoaderOptions struct {
	CWD      string
	AgentDir string
	// SettingsManager supplies the project trust, the settings-configured resource paths and the Packages. Nil reads the settings files under CWD and AgentDir with trusted project settings.
	SettingsManager *SettingsManager
	// AdditionalSkillPaths are skill files or directories loaded after the resolved skills; they are the loader's explicit paths (resource-loader.ts additionalSkillPaths).
	AdditionalSkillPaths []string
	// AdditionalPromptTemplatePaths are prompt template files or directories loaded after the resolved prompt templates.
	AdditionalPromptTemplatePaths []string
	// AdditionalThemePaths are theme files or directories loaded after the resolved themes.
	AdditionalThemePaths []string
	// NoThemes keeps only AdditionalThemePaths.
	NoThemes bool
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
	ThemesOverride             func(ThemesResult) ThemesResult
	ExtensionsOverride         func(LoadExtensionsResult) LoadExtensionsResult
	AgentsFilesOverride        func(AgentsFilesResult) AgentsFilesResult
	SystemPromptOverride       func(*string) *string
	AppendSystemPromptOverride func([]string) []string
	// LoadExtensions loads the extension set of a reload; nil loads none. A reload with ResolveProjectTrust calls it first for the bootstrap pass, then once for the final set.
	LoadExtensions func(ctx context.Context, request ExtensionLoadRequest) (LoadExtensionsResult, error)
}

// DefaultResourceLoader resolves skills, prompt templates, context files and system prompt files from the default user and project resource locations, the settings-configured paths, the configured Packages and the additional paths, applying project trust as Pi's does, and installs a missing npm or git Package as Pi's package manager does. It loads themes, and extensions through the LoadExtensions hook the Extension Host supplies. A new loader is empty until Reload runs, as Pi's DefaultResourceLoader is.
type DefaultResourceLoader struct {
	cwd      string
	agentDir string
	settings *SettingsManager
	options  DefaultResourceLoaderOptions

	// opMu serialises Reload and ExtendResources, which rewrite the state below.
	opMu sync.Mutex
	// lastSkillPaths, lastPromptPaths and lastThemePaths are the paths of the last reload or extension; resourceMetadata is the metadata of the last reload (resource-loader.ts resourceMetadataByPath); extensionSources are the source infos ExtendResources recorded. They are touched only under opMu.
	lastSkillPaths, lastPromptPaths, lastThemePaths []string
	resourceMetadata                                *pathMetadataIndex
	extensionSources                                extensionSources

	mu           sync.RWMutex
	skills       SkillsResult
	prompts      PromptsResult
	themes       ThemesResult
	extensions   LoadExtensionsResult
	agentsFiles  []ContextFile
	systemPrompt *string
	appendPrompt []string
	// systemSource and appendSources are the resolved paths of the files the prompts came from.
	systemSource  *ResourceSource
	appendSources []ResourceSource
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
		skills:           SkillsResult{Skills: []*Skill{}, Diagnostics: []extension.ResourceDiagnostic{}},
		prompts:          PromptsResult{Prompts: []PromptTemplate{}, Diagnostics: []extension.ResourceDiagnostic{}},
		themes:           ThemesResult{Themes: []*tui.Theme{}, Diagnostics: []extension.ResourceDiagnostic{}},
		extensions:       LoadExtensionsResult{Extensions: []extension.Extension{}, Errors: []ExtensionLoadError{}, Warnings: []ExtensionLoadWarning{}},
		resourceMetadata: &pathMetadataIndex{},
	}
}

// ResourceLoaderReloadOptions are the options of [DefaultResourceLoader.Reload] (resource-loader.ts ResourceLoaderReloadOptions).
type ResourceLoaderReloadOptions struct {
	// ResolveProjectTrust decides whether the project is trusted before any resource loads; the loader applies the answer to its settings.
	ResolveProjectTrust func(ctx context.Context, input ResolveProjectTrustInput) (bool, error)
}

// ResolveProjectTrustInput is the argument of [ResourceLoaderReloadOptions.ResolveProjectTrust].
type ResolveProjectTrustInput struct {
	// ExtensionsResult is the bootstrap extension set, loaded with the project untrusted (resource-loader.ts loadProjectTrustExtensions).
	ExtensionsResult LoadExtensionsResult
}

// Reload reloads the settings, then rediscovers every resource for their current project trust (resource-loader.ts reload). Reloading the settings discards transient overrides applied with ApplyOverrides. A failure while resolving the Packages, settings entries or skills leaves the previous results in place; a failure while resolving the prompt templates keeps the new skills and the previous other results, as Pi's reload does. The optional options decide project trust first, as [DefaultResourceLoader.ReloadWith] does with a context of context.Background().
func (l *DefaultResourceLoader) Reload(options ...ResourceLoaderReloadOptions) error {
	return l.ReloadWith(context.Background(), options...)
}

// ReloadWith is Reload with the context ctx. A ResolveProjectTrust in options first loads the bootstrap extension set with project settings forced untrusted and hands it to the callback, then applies the answer to the settings (resource-loader.ts reload(options)); the final extension set reuses the bootstrap load. A failed decision or bootstrap load fails the reload and leaves the previous results in place, with the project untrusted as the bootstrap pass left it.
func (l *DefaultResourceLoader) ReloadWith(ctx context.Context, options ...ResourceLoaderReloadOptions) error {
	l.opMu.Lock()
	defer l.opMu.Unlock()
	// resource-loader.ts:510: each reload starts the extensions timeline over.
	timings.ResetTimings(timings.Extensions)
	var preTrust *LoadExtensionsResult
	for _, option := range options {
		if option.ResolveProjectTrust == nil {
			continue
		}
		bootstrap, err := l.loadProjectTrustExtensions(ctx)
		if err != nil {
			return err
		}
		trusted, err := option.ResolveProjectTrust(ctx, ResolveProjectTrustInput{ExtensionsResult: bootstrap})
		if err != nil {
			return err
		}
		l.settings.SetProjectTrusted(trusted)
		preTrust = &bootstrap
	}
	l.settings.Reload()
	return l.discover(ctx, preTrust)
}

// discover rediscovers every resource from the settings as they currently are.
func (l *DefaultResourceLoader) discover(ctx context.Context, preTrust *LoadExtensionsResult) error {
	trusted := l.settings.IsProjectTrusted()
	manager := packagemanager.NewPackageManager(packagemanager.PackageManagerOptions{CWD: l.cwd, SettingsManager: l.settings})
	manager.AgentDir = l.agentDir
	resolved, err := manager.Resolve(nil)
	if err != nil {
		return fmt.Errorf("coding: %w", err)
	}
	extensions, err := l.loadFinalExtensions(ctx, preTrust)
	if err != nil {
		return err
	}
	metadata := &pathMetadataIndex{}
	sources := extensionSources{}
	skills, skillPaths, err := l.loadSkills(resolved.Skills, metadata, &sources)
	if err != nil {
		return err
	}
	// Pi's reload assigns the skills before it resolves the prompt template paths, so a later rejection keeps them (resource-loader.ts:468-488).
	l.mu.Lock()
	l.skills = skills
	l.mu.Unlock()
	l.lastSkillPaths, l.resourceMetadata, l.extensionSources = skillPaths, metadata, sources
	prompts, promptPaths, err := l.loadPrompts(resolved.Prompts, metadata, &sources)
	if err != nil {
		return err
	}
	themes, themePaths, err := l.loadThemes(resolved.Themes, metadata, &sources)
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
	systemPrompt, systemSource := l.loadSystemPrompt(trusted)
	appendPrompt, appendSources := l.loadAppendSystemPrompt(trusted)
	l.mu.Lock()
	l.extensions = extensions
	l.prompts = prompts
	l.themes = themes
	l.agentsFiles = agentsFiles.AgentsFiles
	l.systemPrompt, l.appendPrompt = systemPrompt, appendPrompt
	l.systemSource, l.appendSources = systemSource, appendSources
	l.mu.Unlock()
	l.lastPromptPaths, l.lastThemePaths = promptPaths, themePaths
	return nil
}

func (l *DefaultResourceLoader) loadSkills(resolved []icodingagent.ResolvedResource, metadata *pathMetadataIndex, sources *extensionSources) (SkillsResult, []string, error) {
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
		return SkillsResult{}, nil, err
	}
	result, err := l.skillsFromPaths(paths, metadata, sources)
	if err != nil {
		return SkillsResult{}, nil, err
	}
	for _, path := range l.options.AdditionalSkillPaths {
		resolved, err := l.resolveResourcePath(path)
		if err != nil {
			return SkillsResult{}, nil, err
		}
		if icodingagent.IsLocalPath(path) && !exists(resolved) && !hasDiagnosticPath(result.Diagnostics, resolved) {
			result.Diagnostics = append(result.Diagnostics, extension.ResourceDiagnostic{Type: "error", Message: "Skill path does not exist", Path: resolved})
		}
	}
	return result, paths, nil
}

// skillsFromPaths is resource-loader.ts updateSkillsFromPaths: it loads the skills at paths without the default locations, applies the override and gives each skill the source info the extension paths, the metadata or the default location yield.
func (l *DefaultResourceLoader) skillsFromPaths(paths []string, metadata *pathMetadataIndex, sources *extensionSources) (SkillsResult, error) {
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
		if info, found := l.findSourceInfo(skill.FilePath, sources.skills, metadata); found {
			stamped.SourceInfo = info
		} else if stamped.SourceInfo == (icodingagent.PiSourceInfo{}) {
			stamped.SourceInfo = icodingagent.DefaultSourceInfoForPath(l.cwd, l.agentDir, skill.FilePath)
		}
		skills = append(skills, &stamped)
	}
	return SkillsResult{Skills: skills, Diagnostics: cloneCollection(result.Diagnostics)}, nil
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

func (l *DefaultResourceLoader) loadPrompts(resolved []icodingagent.ResolvedResource, metadata *pathMetadataIndex, sources *extensionSources) (PromptsResult, []string, error) {
	for _, resource := range resolved {
		metadata.add(resource.Path, resource.Metadata)
	}
	var enabled []string
	if !l.options.NoPromptTemplates {
		enabled = icodingagent.ResourcePaths(resolved)
	}
	paths, err := l.mergePaths(enabled, l.options.AdditionalPromptTemplatePaths)
	if err != nil {
		return PromptsResult{}, nil, err
	}
	result := l.promptsFromPaths(paths, metadata, sources)
	for _, path := range l.options.AdditionalPromptTemplatePaths {
		resolved, err := l.resolveResourcePath(path)
		if err != nil {
			return PromptsResult{}, nil, err
		}
		if icodingagent.IsLocalPath(path) && !exists(resolved) && !hasDiagnosticPath(result.Diagnostics, resolved) {
			result.Diagnostics = append(result.Diagnostics, extension.ResourceDiagnostic{Type: "error", Message: "Prompt template path does not exist", Path: resolved})
		}
	}
	return result, paths, nil
}

// promptsFromPaths is resource-loader.ts updatePromptsFromPaths: it loads the prompt templates at paths without the default locations, drops the name collisions, applies the override and gives each template its source info.
func (l *DefaultResourceLoader) promptsFromPaths(paths []string, metadata *pathMetadataIndex, sources *extensionSources) PromptsResult {
	result := PromptsResult{Prompts: []PromptTemplate{}, Diagnostics: []extension.ResourceDiagnostic{}}
	if !l.options.NoPromptTemplates || len(paths) > 0 {
		loaded := icodingagent.LoadPromptTemplatesFromOptions(icodingagent.LoadPromptTemplatesOptions{Cwd: l.cwd, AgentDir: l.agentDir, PromptPaths: paths})
		prompts, collisions := icodingagent.DedupePromptTemplates(loaded.Templates)
		result = PromptsResult{Prompts: prompts, Diagnostics: append(loaded.Diagnostics, collisions...)}
	}
	if l.options.PromptsOverride != nil {
		result = l.options.PromptsOverride(result)
	}
	prompts := make([]PromptTemplate, 0, len(result.Prompts))
	for _, prompt := range result.Prompts {
		if info, found := l.findSourceInfo(prompt.FilePath, sources.prompts, metadata); found {
			prompt.SourceInfo = info
		} else if prompt.SourceInfo == (icodingagent.PiSourceInfo{}) {
			prompt.SourceInfo = icodingagent.DefaultSourceInfoForPath(l.cwd, l.agentDir, prompt.FilePath)
		}
		if prompt.SourceInfo.Scope == "user" || prompt.SourceInfo.Scope == "project" {
			prompt.Scope = prompt.SourceInfo.Scope
		}
		prompts = append(prompts, prompt)
	}
	return PromptsResult{Prompts: prompts, Diagnostics: cloneCollection(result.Diagnostics)}
}

// loadSystemPrompt resolves the system prompt text as resource-loader.ts reload does: the option, else the discovered SYSTEM.md, read through resolvePromptInput.
func (l *DefaultResourceLoader) loadSystemPrompt(trusted bool) (*string, *ResourceSource) {
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
	return systemPrompt, promptSourcePath(source)
}

// promptSourcePath is the resolved path of a prompt input that names an existing file; literal prompt text has none (resource-loader.ts systemPromptSourcePath).
func promptSourcePath(source string) *ResourceSource {
	if source == "" || !exists(source) {
		return nil
	}
	return &ResourceSource{Path: resolveLoaderDirectory(source)}
}

func (l *DefaultResourceLoader) loadAppendSystemPrompt(trusted bool) ([]string, []ResourceSource) {
	sources := l.options.AppendSystemPrompt
	if sources == nil {
		sources = []string{}
		if discovered := icodingagent.DiscoverPromptFile(l.cwd, l.agentDir, "APPEND_SYSTEM.md", trusted); discovered != "" {
			sources = append(sources, discovered)
		}
	}
	appended := make([]string, 0, len(sources))
	var appendSources []ResourceSource
	for _, source := range sources {
		if found := promptSourcePath(source); found != nil {
			appendSources = append(appendSources, *found)
		}
		if source != "" {
			appended = append(appended, icodingagent.ResolvePromptInput(source, "append system prompt"))
		}
	}
	if l.options.AppendSystemPromptOverride != nil {
		appended = l.options.AppendSystemPromptOverride(appended)
	}
	return appended, appendSources
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

// GetSystemPromptSource returns the file the system prompt was read from by the last Reload; false when it came from literal text or there is none.
func (l *DefaultResourceLoader) GetSystemPromptSource() (ResourceSource, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.systemSource == nil {
		return ResourceSource{}, false
	}
	return *l.systemSource, true
}

// GetAppendSystemPromptSources returns the files the appended texts were read from by the last Reload.
func (l *DefaultResourceLoader) GetAppendSystemPromptSources() []ResourceSource {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return cloneCollection(l.appendSources)
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

func (*staticResourceLoader) GetSystemPromptSource() (ResourceSource, bool) {
	return ResourceSource{}, false
}

func (*staticResourceLoader) GetAppendSystemPromptSources() []ResourceSource {
	return []ResourceSource{}
}

func (*staticResourceLoader) GetExtensions() LoadExtensionsResult {
	return LoadExtensionsResult{Extensions: []extension.Extension{}, Errors: []ExtensionLoadError{}, Warnings: []ExtensionLoadWarning{}}
}

func (*staticResourceLoader) GetThemes() ThemesResult {
	return ThemesResult{Themes: []*tui.Theme{}, Diagnostics: []extension.ResourceDiagnostic{}}
}

// errStaticResourceLoader reports a mutation of a loader that holds only the collections its owner resolved elsewhere.
var errStaticResourceLoader = errors.New("a static resource loader holds fixed resources and cannot be extended or reloaded")

func (*staticResourceLoader) ExtendResources(ResourceExtensionPaths) error {
	return errStaticResourceLoader
}

func (*staticResourceLoader) Reload(...ResourceLoaderReloadOptions) error {
	return errStaticResourceLoader
}

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
func resolveResourceLoader(svcs *AgentSessionServices, supplied ResourceLoader) (*resourceLoaderRef, error) {
	if supplied != nil {
		return &resourceLoaderRef{loader: supplied, promptFromLoader: supplied != NoResources}, nil
	}
	if loader := svcs.ResourceLoader(); loader != nil {
		// sdk.ts: a loader created with the Services is reloaded already and is not reloaded again.
		return &resourceLoaderRef{loader: loader, promptFromLoader: true}, nil
	}
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: svcs.CWD(), AgentDir: svcs.AgentDir(), SettingsManager: svcs.SettingsManager()})
	if err := loader.Reload(); err != nil {
		return nil, err
	}
	timings.Time("resourceLoader.reload")
	return &resourceLoaderRef{loader: loader, promptFromLoader: true}, nil
}
