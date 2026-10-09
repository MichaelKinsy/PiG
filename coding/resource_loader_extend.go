// Ports packages/coding-agent/src/core/resource-loader.ts DefaultResourceLoader.getExtensions, getThemes, extendResources and loadProjectTrustExtensions, with the themes half of reload.

package coding

import (
	"context"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// ThemesResult is a loader's themes with the diagnostics found while resolving them.
type ThemesResult struct {
	Themes      []*tui.Theme
	Diagnostics []extension.ResourceDiagnostic
}

// ExtensionLoadError is one {path, error} entry of types.ts LoadExtensionsResult.errors.
type ExtensionLoadError struct{ Path, Error string }

// ExtensionLoadWarning is one {path, warning} entry of types.ts LoadExtensionsResult.warnings.
type ExtensionLoadWarning struct{ Path, Warning string }

// LoadExtensionsResult is types.ts LoadExtensionsResult: the extensions of a load with its errors and warnings, and the runtime they share.
type LoadExtensionsResult struct {
	Extensions []extension.Extension
	Errors     []ExtensionLoadError
	Warnings   []ExtensionLoadWarning
	// Runtime is the runtime the extensions of this load share; the runner the Session builds over them binds it (agent-session.ts _buildRuntime passes extensionsResult.runtime to the ExtensionRunner). nil means the runner owns a fresh one.
	Runtime *extension.ExtensionRuntime
}

// ExtensionLoadRequest asks [DefaultResourceLoaderOptions.LoadExtensions] for one pass of the extension set.
type ExtensionLoadRequest struct {
	// Bootstrap marks the pass that runs with project settings forced untrusted: it loads the user and temporary extensions and keeps project-local ones out (resource-loader.ts loadProjectTrustExtensions).
	Bootstrap bool
	// PreTrust is the bootstrap result a final pass reuses; nil when no bootstrap pass ran.
	PreTrust *LoadExtensionsResult
}

// ExtensionResourcePath is one {path, metadata} entry of types.ts ResourceExtensionPaths.
type ExtensionResourcePath struct {
	Path     string
	Metadata icodingagent.PathMetadata
}

// ResourceExtensionPaths are the resource paths extensions contribute (resource-loader.ts ResourceExtensionPaths).
type ResourceExtensionPaths struct {
	SkillPaths  []ExtensionResourcePath
	PromptPaths []ExtensionResourcePath
	ThemePaths  []ExtensionResourcePath
}

// extensionSourceInfos is an insertion-ordered Map<string, SourceInfo>: setting a path again replaces its value and keeps its position.
type extensionSourceInfos struct {
	paths []string
	infos map[string]icodingagent.PiSourceInfo
}

type extensionSources struct{ skills, prompts, themes extensionSourceInfos }

func (m *extensionSourceInfos) set(path string, info icodingagent.PiSourceInfo) {
	if _, present := m.infos[path]; !present {
		m.paths = append(m.paths, path)
	}
	if m.infos == nil {
		m.infos = map[string]icodingagent.PiSourceInfo{}
	}
	m.infos[path] = info
}

// find returns the info of the first recorded path that is resourcePath or contains it, with the path replaced by resourcePath.
func (m extensionSourceInfos) find(resourcePath string) (icodingagent.PiSourceInfo, bool) {
	normalized := absPath(resourcePath)
	for _, path := range m.paths {
		source := absPath(path)
		if normalized == source || strings.HasPrefix(normalized, source+string(filepath.Separator)) {
			info := m.infos[path]
			info.Path = resourcePath
			return info, true
		}
	}
	return icodingagent.PiSourceInfo{}, false
}

func absPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// findSourceInfo is resource-loader.ts findSourceInfoForPath: a synthetic path has its default source info, then the extension-recorded infos and the reload metadata decide.
func (l *DefaultResourceLoader) findSourceInfo(resourcePath string, extra extensionSourceInfos, metadata *pathMetadataIndex) (icodingagent.PiSourceInfo, bool) {
	if resourcePath == "" {
		return icodingagent.PiSourceInfo{}, false
	}
	if strings.HasPrefix(resourcePath, "<") && strings.HasSuffix(resourcePath, ">") {
		return icodingagent.DefaultSourceInfoForPath(l.cwd, l.agentDir, resourcePath), true
	}
	if info, found := extra.find(resourcePath); found {
		return info, true
	}
	return metadata.sourceInfo(resourcePath)
}

// GetExtensions returns the extensions found by the last Reload (resource-loader.ts getExtensions).
func (l *DefaultResourceLoader) GetExtensions() LoadExtensionsResult {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return LoadExtensionsResult{Extensions: cloneCollection(l.extensions.Extensions), Errors: cloneCollection(l.extensions.Errors), Warnings: cloneCollection(l.extensions.Warnings), Runtime: l.extensions.Runtime}
}

// GetThemes returns the themes found by the last Reload and ExtendResources (resource-loader.ts getThemes).
func (l *DefaultResourceLoader) GetThemes() ThemesResult {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return ThemesResult{Themes: cloneCollection(l.themes.Themes), Diagnostics: cloneCollection(l.themes.Diagnostics)}
}

// LoadProjectTrustExtensions loads the extension set with project settings forced untrusted and returns it (resource-loader.ts loadProjectTrustExtensions). The loader's settings stay untrusted until the next Reload or an explicit trust decision.
func (l *DefaultResourceLoader) LoadProjectTrustExtensions(ctx context.Context) (LoadExtensionsResult, error) {
	l.opMu.Lock()
	defer l.opMu.Unlock()
	return l.loadProjectTrustExtensions(ctx)
}

func (l *DefaultResourceLoader) loadProjectTrustExtensions(ctx context.Context) (LoadExtensionsResult, error) {
	// Forcing untrusted project settings for the bootstrap pass keeps project-local extensions and Packages out while user and temporary extensions load (resource-loader.ts:501-507).
	l.settings.SetProjectTrusted(false)
	l.settings.Reload()
	return l.loadExtensions(ctx, ExtensionLoadRequest{Bootstrap: true})
}

func (l *DefaultResourceLoader) loadExtensions(ctx context.Context, request ExtensionLoadRequest) (LoadExtensionsResult, error) {
	if l.options.LoadExtensions == nil {
		return LoadExtensionsResult{Extensions: []extension.Extension{}, Errors: []ExtensionLoadError{}, Warnings: []ExtensionLoadWarning{}}, nil
	}
	return l.options.LoadExtensions(ctx, request)
}

// loadFinalExtensions loads the extension set of a reload, applies ExtensionsOverride and gives an extension that has no source info the one its path yields.
func (l *DefaultResourceLoader) loadFinalExtensions(ctx context.Context, preTrust *LoadExtensionsResult) (LoadExtensionsResult, error) {
	result, err := l.loadExtensions(ctx, ExtensionLoadRequest{PreTrust: preTrust})
	if err != nil {
		return LoadExtensionsResult{}, err
	}
	if l.options.ExtensionsOverride != nil {
		result = l.options.ExtensionsOverride(result)
	}
	// The Extension Host sets the source info of the extensions it loads from Packages and settings; the loader fills the ones it left empty (resource-loader.ts applyExtensionSourceInfo).
	result.Extensions = slices.Clone(result.Extensions)
	for i := range result.Extensions {
		ext := &result.Extensions[i]
		if ext.SourceInfo != (icodingagent.PiSourceInfo{}) {
			continue
		}
		info, found := l.findSourceInfo(ext.Path, extensionSourceInfos{}, l.resourceMetadata)
		if !found {
			info = icodingagent.DefaultSourceInfoForPath(l.cwd, l.agentDir, ext.Path)
		}
		ext.SourceInfo = info
		ext.Commands = maps.Clone(ext.Commands)
		for name, command := range ext.Commands {
			command.SourceInfo = info
			ext.Commands[name] = command
		}
		ext.Tools = maps.Clone(ext.Tools)
		for name, tool := range ext.Tools {
			tool.SourceInfo = info
			ext.Tools[name] = tool
		}
	}
	return result, nil
}

func (l *DefaultResourceLoader) loadThemes(resolved []icodingagent.ResolvedResource, metadata *pathMetadataIndex, sources *extensionSources) (ThemesResult, []string, error) {
	for _, resource := range resolved {
		metadata.add(resource.Path, resource.Metadata)
	}
	var enabled []string
	if !l.options.NoThemes {
		enabled = icodingagent.ResourcePaths(resolved)
	}
	paths, err := l.mergePaths(enabled, l.options.AdditionalThemePaths)
	if err != nil {
		return ThemesResult{}, nil, err
	}
	result := l.themesFromPaths(paths, metadata, sources)
	for _, path := range l.options.AdditionalThemePaths {
		resolved, err := l.resolveResourcePath(path)
		if err != nil {
			return ThemesResult{}, nil, err
		}
		if !exists(resolved) && !hasDiagnosticPath(result.Diagnostics, resolved) {
			result.Diagnostics = append(result.Diagnostics, extension.ResourceDiagnostic{Type: "error", Message: "Theme path does not exist", Path: resolved})
		}
	}
	return result, paths, nil
}

// themesFromPaths is resource-loader.ts updateThemesFromPaths: it loads the themes at paths without the default locations, applies the override and gives each theme its source info.
func (l *DefaultResourceLoader) themesFromPaths(paths []string, metadata *pathMetadataIndex, sources *extensionSources) ThemesResult {
	result := ThemesResult{Themes: []*tui.Theme{}, Diagnostics: []extension.ResourceDiagnostic{}}
	if !l.options.NoThemes || len(paths) > 0 {
		files, diagnostics := icodingagent.LoadThemeFiles(l.settings, paths)
		for _, file := range files {
			result.Themes = append(result.Themes, file.Theme)
		}
		result.Diagnostics = diagnostics
	}
	if l.options.ThemesOverride != nil {
		result = l.options.ThemesOverride(result)
	}
	themes := make([]*tui.Theme, 0, len(result.Themes))
	for _, theme := range result.Themes {
		// resource-loader.ts:902-909 sets theme.sourceInfo from the theme's sourcePath: the extension's or package's recorded source, else the default for the path.
		if theme != nil && theme.SourcePath != "" {
			info, found := l.findSourceInfo(theme.SourcePath, sources.themes, metadata)
			switch {
			case found:
				theme.SourceInfo = &info
			case theme.SourceInfo == nil:
				defaultInfo := icodingagent.DefaultSourceInfoForPath(l.cwd, l.agentDir, theme.SourcePath)
				theme.SourceInfo = &defaultInfo
			}
		}
		themes = append(themes, theme)
	}
	return ThemesResult{Themes: themes, Diagnostics: cloneCollection(result.Diagnostics)}
}

// ExtendResources adds the skills, prompt templates and themes extensions contributed to the ones the last Reload found (resource-loader.ts extendResources). Each path records its metadata as the source info of the resources under it; the loader then reloads the collection from the previous paths plus the new ones. A path that cannot be resolved fails the call, and collections updated before it stay updated.
func (l *DefaultResourceLoader) ExtendResources(paths ResourceExtensionPaths) error {
	l.opMu.Lock()
	defer l.opMu.Unlock()
	skillPaths, err := l.normalizeExtensionPaths(paths.SkillPaths)
	if err != nil {
		return err
	}
	promptPaths, err := l.normalizeExtensionPaths(paths.PromptPaths)
	if err != nil {
		return err
	}
	themePaths, err := l.normalizeExtensionPaths(paths.ThemePaths)
	if err != nil {
		return err
	}
	for _, entry := range skillPaths {
		l.extensionSources.skills.set(entry.Path, entry.Metadata.SourceInfo(entry.Path))
	}
	for _, entry := range promptPaths {
		l.extensionSources.prompts.set(entry.Path, entry.Metadata.SourceInfo(entry.Path))
	}
	for _, entry := range themePaths {
		l.extensionSources.themes.set(entry.Path, entry.Metadata.SourceInfo(entry.Path))
	}
	if len(skillPaths) > 0 {
		merged, err := l.mergePaths(l.lastSkillPaths, extensionPathList(skillPaths))
		if err != nil {
			return err
		}
		skills, err := l.skillsFromPaths(merged, l.resourceMetadata, &l.extensionSources)
		if err != nil {
			return err
		}
		l.lastSkillPaths = merged
		l.mu.Lock()
		l.skills = skills
		l.mu.Unlock()
	}
	if len(promptPaths) > 0 {
		merged, err := l.mergePaths(l.lastPromptPaths, extensionPathList(promptPaths))
		if err != nil {
			return err
		}
		prompts := l.promptsFromPaths(merged, l.resourceMetadata, &l.extensionSources)
		l.lastPromptPaths = merged
		l.mu.Lock()
		l.prompts = prompts
		l.mu.Unlock()
	}
	if len(themePaths) > 0 {
		merged, err := l.mergePaths(l.lastThemePaths, extensionPathList(themePaths))
		if err != nil {
			return err
		}
		themes := l.themesFromPaths(merged, l.resourceMetadata, &l.extensionSources)
		l.lastThemePaths = merged
		l.mu.Lock()
		l.themes = themes
		l.mu.Unlock()
	}
	return nil
}

// normalizeExtensionPaths is resource-loader.ts normalizeExtensionPaths: each path and base directory resolves against the loader's cwd.
func (l *DefaultResourceLoader) normalizeExtensionPaths(entries []ExtensionResourcePath) ([]ExtensionResourcePath, error) {
	normalized := make([]ExtensionResourcePath, 0, len(entries))
	for _, entry := range entries {
		if entry.Metadata.BaseDir != "" {
			baseDir, err := l.resolveResourcePath(entry.Metadata.BaseDir)
			if err != nil {
				return nil, err
			}
			entry.Metadata.BaseDir = baseDir
		}
		path, err := l.resolveResourcePath(entry.Path)
		if err != nil {
			return nil, err
		}
		entry.Path = path
		normalized = append(normalized, entry)
	}
	return normalized, nil
}

func extensionPathList(entries []ExtensionResourcePath) []string {
	paths := make([]string, len(entries))
	for i, entry := range entries {
		paths[i] = entry.Path
	}
	return paths
}
