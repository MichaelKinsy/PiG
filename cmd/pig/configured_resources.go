package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/term"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func configuredExtensionResolver(resolvers []extsource.ResolveFunc) extsource.ResolveFunc {
	if len(resolvers) > 0 && resolvers[0] != nil {
		return resolvers[0]
	}
	return extsource.Resolve
}

func collectStartupThemePaths(cwd, agentDir string, sm *codingagent.SettingsManager) []string {
	global := sm.GetGlobalSettings()
	paths := collectTopLevelResourcePaths(filepath.Join(agentDir, "themes"), global.Themes, "themes")
	for _, pkg := range global.Packages {
		root := installedPathForConfiguredSource(cwd, sm, pkg.Source, false)
		if root == "" {
			continue
		}
		resources, err := packagecontent.Discover(root)
		if err != nil {
			continue
		}
		for _, source := range resources.ThemeFiles {
			rel, _ := filepath.Rel(root, source)
			if packagecontent.ResourceEnabled(rel, pkg.Themes) {
				paths = append(paths, source)
			}
		}
	}
	return dedupStrings(paths)
}

func projectResourceRoot(cwd string) (string, bool) {
	root := codingagent.ProjectConfigDir(cwd)
	return root, !samePath(root, codingagent.ConfigRoot())
}

func collectPromptPaths(cwd, agentDir string, sm *codingagent.SettingsManager, flags CLIFlags, projectTrusted bool, resolvers ...extsource.ResolveFunc) []string {
	paths := make([]string, 0)
	// Pi keeps the first same-name prompt after ordering CLI, project, user,
	// then Package resources.
	for _, promptPath := range flags.PromptTemplates {
		paths = append(paths, collectResourceFilesFromPaths([]string{resolveSettingsPath(cwd, promptPath)}, "prompts")...)
	}
	if !flags.NoPromptTemplates {

		if projectRoot, ok := projectResourceRoot(cwd); projectTrusted && ok {
			paths = append(paths, collectTopLevelResourcePaths(filepath.Join(projectRoot, "prompts"), sm.GetProjectSettings().Prompts, "prompts")...)
		}
		paths = append(paths, collectTopLevelResourcePaths(filepath.Join(agentDir, "prompts"), sm.GetGlobalSettings().Prompts, "prompts")...)
		paths = append(paths, collectPackagePromptPaths(cwd, sm, resolvers...)...)
	}
	return dedupStrings(paths)
}

func collectThemePaths(cwd, agentDir string, sm *codingagent.SettingsManager, flags CLIFlags, projectTrusted bool, resolvers ...extsource.ResolveFunc) []string {
	paths := make([]string, 0)
	if !flags.NoThemes {
		paths = append(paths, collectPackageThemePaths(cwd, sm, resolvers...)...)
		paths = append(paths, collectTopLevelResourcePaths(filepath.Join(agentDir, "themes"), sm.GetGlobalSettings().Themes, "themes")...)
		if projectRoot, ok := projectResourceRoot(cwd); projectTrusted && ok {
			paths = append(paths, collectTopLevelResourcePaths(filepath.Join(projectRoot, "themes"), sm.GetProjectSettings().Themes, "themes")...)
		}
	}
	for _, p := range flags.Themes {
		paths = append(paths, collectResourceFilesFromPaths([]string{resolveSettingsPath(cwd, p)}, "themes")...)
	}
	return dedupStrings(paths)
}

func collectSkillInputs(cwd, agentDir string, sm *codingagent.SettingsManager, flags CLIFlags, ambientScopes *[]string, resolvers ...extsource.ResolveFunc) []string {
	inputs := make([]string, 0)
	for _, p := range flags.Skills {
		inputs = append(inputs, collectResourceFilesFromPaths([]string{resolveSettingsPath(cwd, p)}, "skills")...)
	}
	if flags.NoSkills {
		return dedupStrings(inputs)
	}

	// Pi resolves same-name Skill collisions first-wins after ordering paths by
	// precedence: CLI, project explicit, project auto, user explicit, user auto,
	// then Package resources.
	projectRoot, projectResourcesEnabled := projectResourceRoot(cwd)
	if ambientSourceEnabled(ambientScopes, "workspace") && projectResourcesEnabled {
		projectSettings := sm.GetProjectSettings().Skills
		inputs = append(inputs, resolveConfiguredResourceEntries(projectSettings, projectRoot, "skills")...)
		projectAuto := collectAutoDiscoveredResourcePaths(filepath.Join(projectRoot, "skills"), "skills")
		inputs = append(inputs, filterAutoDiscoveredPaths(projectAuto, projectSettings, projectRoot, "skills")...)

		home, _ := os.UserHomeDir()
		userAgentsSkills := filepath.Join(home, ".agents", "skills")
		for _, dir := range discoverAncestorAgentsSkillDirs(cwd) {
			abs, _ := filepath.Abs(dir)
			uabs, _ := filepath.Abs(userAgentsSkills)
			if abs == uabs {
				continue
			}
			inputs = append(inputs, filterAutoDiscoveredPaths(discoverSkillDir(dir), projectSettings, filepath.Dir(dir), "skills")...)
		}
	}

	if ambientSourceEnabled(ambientScopes, "user") {
		userSettings := sm.GetGlobalSettings().Skills
		inputs = append(inputs, resolveConfiguredResourceEntries(userSettings, agentDir, "skills")...)
		userAuto := collectAutoDiscoveredResourcePaths(filepath.Join(agentDir, "skills"), "skills")
		inputs = append(inputs, filterAutoDiscoveredPaths(userAuto, userSettings, agentDir, "skills")...)
		home, _ := os.UserHomeDir()
		userAgentsSkills := filepath.Join(home, ".agents", "skills")
		inputs = append(inputs, filterAutoDiscoveredPaths(discoverSkillDir(userAgentsSkills), userSettings, filepath.Dir(userAgentsSkills), "skills")...)
	}

	inputs = append(inputs, collectPackageSkillPaths(cwd, sm, ambientScopes, resolvers...)...)
	return dedupStrings(inputs)
}

func collectExtensionConfigs(cwd, agentDir string, sm *codingagent.SettingsManager, flags CLIFlags, ambientScopes *[]string, resolvers ...extsource.ResolveFunc) []subprocess.ExtConfig {
	// Upstream resource-loader.ts loads -e paths first, then the resolved
	// paths in package-manager.ts resourcePrecedenceRank order: project
	// settings entries, project auto-discovery, user settings entries, user
	// auto-discovery, then Packages. It reports a missing -e path after
	// loading. --no-extensions keeps only the -e paths.
	configs := make([]subprocess.ExtConfig, 0)
	var missing []subprocess.ExtConfig
	for _, p := range flags.Extensions {
		for _, config := range cliExtensionConfigs(resolveSettingsPath(cwd, p), resolvers...) {
			if _, absent := errors.AsType[extensionPathMissingError](config.ResolveError()); absent {
				missing = append(missing, config)
				continue
			}
			configs = append(configs, config)
		}
	}
	if !flags.NoExtensions {
		if projectRoot, ok := projectResourceRoot(cwd); ambientSourceEnabled(ambientScopes, "workspace") && ok {
			configs = append(configs, collectTopLevelExtensionConfigs(filepath.Join(projectRoot, "extensions"), sm.GetProjectSettings().Extensions, "project", resolvers...)...)
		}
		if ambientSourceEnabled(ambientScopes, "user") {
			configs = append(configs, collectTopLevelExtensionConfigs(filepath.Join(agentDir, "extensions"), sm.GetGlobalSettings().Extensions, "user", resolvers...)...)
		}
		configs = append(configs, collectPackageExtensionConfigs(cwd, sm, ambientScopes, resolvers...)...)
	}
	return mergeExtConfigs(append(configs, missing...))
}

// cliExtensionConfigs resolves one -e path. Like upstream resource-loader.ts,
// a missing local path is a load failure: "Extension path does not exist".
// A directory that is not itself an extension loads the extensions inside it.
// Each loaded extension carries upstream's CLI provenance.
func cliExtensionConfigs(resolved string, resolvers ...extsource.ResolveFunc) []subprocess.ExtConfig {
	info, err := os.Stat(resolved)
	if os.IsNotExist(err) {
		return []subprocess.ExtConfig{subprocess.UnresolvedExtConfig(resolved, extensionPathMissingError{path: resolved})}
	}
	if err == nil && info.IsDir() && packagecontent.HasPiManifest(resolved) {
		// Upstream resolves a directory with a "pi" manifest as a Package
		// (resolveLocalExtensionSource, collectPackageResources): each entry
		// its manifest yields is its own extension, and a manifest that yields
		// none loads nothing.
		return withCLISourceInfo(packageExtensionConfigs(resolved, nil, resolvers...))
	}
	configs := pathToExtConfigs(resolved, resolvers...)
	if len(configs) > 0 && configs[0].ResolveError() == nil {
		return withCLISourceInfo(configs)
	}
	var expanded []subprocess.ExtConfig
	for _, path := range collectResourceFilesFromPaths([]string{resolved}, "extensions") {
		expanded = append(expanded, pathToExtConfigs(path, resolvers...)...)
	}
	if len(expanded) == 0 {
		return configs
	}
	return withCLISourceInfo(expanded)
}

// withCLISourceInfo stamps the SourceInfo upstream records for an extension
// named on the command line, {source: "cli", scope: "temporary", origin:
// "top-level"} with no baseDir (resource-loader.ts "Add CLI paths
// metadata"), onto each config. Its tools and commands report it.
func withCLISourceInfo(configs []subprocess.ExtConfig) []subprocess.ExtConfig {
	for i := range configs {
		path := configs[i].Source
		if path == "" {
			path = configs[i].Path
		}
		configs[i].SourceInfo = codingagent.CLISourceInfo(path)
	}
	return configs
}

// collectTopLevelExtensionConfigs resolves a scope's settings entries, then
// its auto-discovered extensions, stamping the SourceInfo upstream
// package-manager.ts records for each: {source: "local"} without a baseDir for
// a settings entry, {source: "auto", baseDir: <config dir>} for a discovered
// one.
func collectTopLevelExtensionConfigs(autoDir string, entries []string, scope string, resolvers ...extsource.ResolveFunc) []subprocess.ExtConfig {
	baseDir := filepath.Dir(autoDir)
	automatic := filterAutoDiscoveredPaths(collectAutoDiscoveredResourcePaths(autoDir, "extensions"), entries, autoDir, "extensions")
	configs := make([]subprocess.ExtConfig, 0, len(automatic)+len(entries))
	// Settings entries rank before auto-discovery in the same scope.
	for _, path := range resolveConfiguredResourceEntries(entries, baseDir, "extensions") {
		for _, config := range pathToExtConfigs(path, resolvers...) {
			config.SourceInfo = codingagent.PiSourceInfo{Path: path, Source: "local", Scope: scope, Origin: "top-level"}
			configs = append(configs, config)
		}
	}
	for _, path := range automatic {
		selected := path
		if name := filepath.Base(path); (name == "index.ts" || name == "index.js") && !extsource.NodeDeclaresExtensions(filepath.Dir(path)) {
			selected = filepath.Dir(path)
		}
		// Upstream loads and names the discovered entry file; PiG loads the
		// selected directory and names the entry file in load errors.
		for _, config := range pathToExtConfigs(selected, resolvers...) {
			config.SourceInfo = codingagent.PiSourceInfo{Path: path, Source: "auto", Scope: scope, Origin: "top-level", BaseDir: baseDir}
			configs = append(configs, config.SelectedAs(path))
		}
	}
	return configs
}

func collectTopLevelResourcePaths(autoDir string, entries []string, kind string) []string {
	auto := collectAutoDiscoveredResourcePaths(autoDir, kind)
	baseDir := filepath.Dir(autoDir)
	explicit := resolveConfiguredResourceEntries(entries, baseDir, kind)
	auto = filterAutoDiscoveredPaths(auto, entries, baseDir, kind)
	return append(auto, explicit...)
}

func resolveConfiguredResourceEntries(entries []string, baseDir string, kind string) []string {
	return packagecontent.ResolveConfigured(entries, baseDir, packagecontent.Kind(kind))
}

func filterAutoDiscoveredPaths(paths, overrides []string, baseDir string, kind string) []string {
	return packagecontent.FilterAutomatic(paths, overrides, baseDir, packagecontent.Kind(kind))
}

func collectAutoDiscoveredResourcePaths(dir string, kind string) []string {
	return packagecontent.DiscoverAutomatic(dir, packagecontent.Kind(kind))
}

func collectResourceFilesFromPaths(paths []string, kind string) []string {
	return packagecontent.Collect(paths, packagecontent.Kind(kind))
}

func splitResourcePatterns(entries []string) (plain, patterns []string) {
	return packagecontent.SplitPatterns(entries)
}

func applyResourcePatterns(allPaths, patterns []string, baseDir, kind string) []string {
	return packagecontent.ApplyPatterns(allPaths, patterns, baseDir, packagecontent.Kind(kind))
}

func isEnabledByOverrides(filePath string, patterns []string, baseDir, kind string) bool {
	return packagecontent.EnabledByOverrides(filePath, patterns, baseDir, packagecontent.Kind(kind))
}

func configuredPackageFilters(source codingagent.PackageSource) map[packagecontent.Kind][]string {
	return map[packagecontent.Kind][]string{
		packagecontent.Extensions: source.Extensions,
		packagecontent.Skills:     source.Skills,
		packagecontent.Prompts:    source.Prompts,
		packagecontent.Themes:     source.Themes,
	}
}

func effectiveConfiguredPackageFilters(pkg configuredPackage, resolvers ...extsource.ResolveFunc) (map[packagecontent.Kind][]string, error) {
	filters := configuredPackageFilters(pkg.Source)
	if pkg.ProjectDelta == nil {
		return filters, nil
	}
	inspectionFilters := map[packagecontent.Kind][]string{
		packagecontent.Extensions: {},
		packagecontent.Skills:     {},
		packagecontent.Prompts:    {},
		packagecontent.Themes:     {},
	}
	resources, missing, err := packagecontent.InspectConfiguredWithResolver(pkg.InstalledPath, inspectionFilters, configuredExtensionResolver(resolvers))
	if err != nil {
		return nil, err
	}
	paths := map[packagecontent.Kind][]string{
		packagecontent.Extensions: resources.ExtensionEntries,
		packagecontent.Skills:     resources.SkillDirs,
		packagecontent.Prompts:    resources.PromptFiles,
		packagecontent.Themes:     resources.ThemeFiles,
	}
	for kind, entries := range paths {
		relativePaths := make([]string, 0, len(entries))
		for _, resourcePath := range entries {
			target := resourcePath
			if kind == packagecontent.Skills {
				target = filepath.Join(target, "SKILL.md")
			}
			relative, relErr := filepath.Rel(pkg.InstalledPath, target)
			if relErr != nil {
				return nil, relErr
			}
			relativePaths = append(relativePaths, filepath.ToSlash(relative))
		}
		for _, member := range missing {
			if member.Kind == kind {
				relativePaths = append(relativePaths, member.Pattern)
			}
		}
		effective, deltaErr := packagecontent.ApplyConfiguredDelta(
			kind,
			packagecontent.Deduplicate(relativePaths),
			filters[kind],
			configuredPackageFilters(*pkg.ProjectDelta)[kind],
		)
		if deltaErr != nil {
			return nil, deltaErr
		}
		filters[kind] = effective
	}
	return filters, nil
}

// extensionLoadFailureHint mirrors upstream main.ts EXTENSION_LOAD_FAILURE_HINT.
const extensionLoadFailureHint = `Hint: Start without extensions using "` + codingagent.AppName + ` -ne".`

// validateConfiguredPackagesForStartup validates the configured Packages for
// session startup and returns a Package failure as the error. An extension
// that does not resolve does not fail its Package: the extension collector
// emits it as an unresolved config, and the final extension load reports it
// with every other extension failure, as upstream main.ts reports all
// extension errors together before it exits. A Package whose scope does not
// load extensions, as under --no-extensions, validates without resolving its
// extensions, so the hint's `-ne` recovery starts the session as it does
// upstream.
func validateConfiguredPackagesForStartup(cwd string, sm *codingagent.SettingsManager, loadsExtensions func(scope string) bool, resolvers ...extsource.ResolveFunc) error {
	for _, pkg := range configuredPackagesForResolution(cwd, sm) {
		if pkg.InstalledPath == "" {
			continue
		}
		filters, err := effectiveConfiguredPackageFilters(pkg, resolvers...)
		if err != nil {
			return invalidConfiguredPackageError(pkg, err)
		}
		if !loadsExtensions(pkg.Scope) {
			filters[packagecontent.Extensions] = []string{}
		}
		// A declared member that matches nothing is skipped, as upstream's
		// package manager skips it; the rest of the Package still loads.
		if _, _, _, err := packagecontent.ValidateConfiguredForStartupWithResolver(pkg.InstalledPath, filters, configuredExtensionResolver(resolvers)); err != nil {
			return invalidConfiguredPackageError(pkg, err)
		}
	}
	return nil
}

type extensionPathMissingError struct{ path string }

func (e extensionPathMissingError) Error() string {
	return "Extension path does not exist: " + e.path
}

// extensionLoadFailureDiagnostic formats one failed extension as upstream
// main.ts does: `Failed to load extension "<path>": <loader error>`, where the
// loader error is `Failed to load extension: <message>` except for a missing
// -e path.
func extensionLoadFailureDiagnostic(path string, err error) codingagent.AgentSessionRuntimeDiagnostic {
	message := "Failed to load extension: " + err.Error()
	if _, missing := errors.AsType[extensionPathMissingError](err); missing {
		message = err.Error()
	}
	return codingagent.AgentSessionRuntimeDiagnostic{Type: "error", Message: fmt.Sprintf(`Failed to load extension "%s": %s`, path, message)}
}

// extensionLoadDiagnostics formats host load errors. An error without an
// extension path, such as an embedded cell failure, keeps its own text.
func extensionLoadDiagnostics(errs []error) []codingagent.AgentSessionRuntimeDiagnostic {
	diagnostics := make([]codingagent.AgentSessionRuntimeDiagnostic, 0, len(errs))
	for _, err := range errs {
		if loadErr, ok := errors.AsType[*subprocess.ExtensionLoadError](err); ok && loadErr.Path != "" {
			diagnostics = append(diagnostics, extensionLoadFailureDiagnostic(loadErr.Path, loadErr.Err))
			continue
		}
		diagnostics = append(diagnostics, codingagent.AgentSessionRuntimeDiagnostic{Type: "error", Message: "Failed to load extension: " + err.Error()})
	}
	return diagnostics
}

// extensionConflictDiagnostics formats tool and flag conflicts as upstream
// main.ts formats its extension errors: `Failed to load extension "<path>":
// <conflict>`.
func extensionConflictDiagnostics(conflicts []codingagent.ExtensionConflict) []codingagent.AgentSessionRuntimeDiagnostic {
	diagnostics := make([]codingagent.AgentSessionRuntimeDiagnostic, 0, len(conflicts))
	for _, conflict := range conflicts {
		diagnostics = append(diagnostics, codingagent.AgentSessionRuntimeDiagnostic{Type: "error", Message: fmt.Sprintf(`Failed to load extension "%s": %s`, conflict.Path, conflict.Message)})
	}
	return diagnostics
}

// reportExtensionLoadFailures mirrors upstream main.ts on extension load
// errors: it reports the startup diagnostics, then the -ne hint in yellow.
func reportExtensionLoadFailures(diagnostics []codingagent.AgentSessionRuntimeDiagnostic) {
	codingagent.ReportDiagnostics(codingagent.DeduplicateDiagnostics(diagnostics))
	hint := extensionLoadFailureHint
	if term.IsTerminal(int(os.Stderr.Fd())) {
		hint = "\x1b[33m" + hint + "\x1b[39m"
	}
	fmt.Fprintln(os.Stderr, hint)
}

func invalidConfiguredPackageError(pkg configuredPackage, err error) error {
	return fmt.Errorf("%s Package %q at %s is invalid: %w", pkg.Scope, pkg.Source.Source, pkg.InstalledPath, err)
}

func collectPackagePromptPaths(cwd string, sm *codingagent.SettingsManager, resolvers ...extsource.ResolveFunc) []string {
	var paths []string
	for _, pkg := range configuredPackagesForResolution(cwd, sm) {
		root := pkg.InstalledPath
		if root == "" {
			continue
		}
		filters, err := effectiveConfiguredPackageFilters(pkg, resolvers...)
		if err != nil {
			continue
		}
		resources, err := packagecontent.Discover(root)
		if err != nil {
			continue
		}
		for _, src := range resources.PromptFiles {
			rel, _ := filepath.Rel(root, src)
			if packagecontent.ResourceEnabled(rel, filters[packagecontent.Prompts]) {
				paths = append(paths, src)
			}
		}
	}
	return paths
}

func collectPackageThemePaths(cwd string, sm *codingagent.SettingsManager, resolvers ...extsource.ResolveFunc) []string {
	var paths []string
	for _, pkg := range configuredPackagesForResolution(cwd, sm) {
		root := pkg.InstalledPath
		if root == "" {
			continue
		}
		filters, err := effectiveConfiguredPackageFilters(pkg, resolvers...)
		if err != nil {
			continue
		}
		resources, err := packagecontent.Discover(root)
		if err != nil {
			continue
		}
		for _, src := range resources.ThemeFiles {
			rel, _ := filepath.Rel(root, src)
			if packagecontent.ResourceEnabled(rel, filters[packagecontent.Themes]) {
				paths = append(paths, src)
			}
		}
	}
	return paths
}

func collectPackageSkillPaths(cwd string, sm *codingagent.SettingsManager, ambientScopes *[]string, resolvers ...extsource.ResolveFunc) []string {
	var paths []string
	for _, pkg := range configuredPackagesForResolution(cwd, sm) {
		if !packageScopeEnabled(ambientScopes, pkg.Scope) {
			continue
		}
		root := pkg.InstalledPath
		if root == "" {
			continue
		}
		filters, err := effectiveConfiguredPackageFilters(pkg, resolvers...)
		if err != nil {
			continue
		}
		resources, err := packagecontent.Discover(root)
		if err != nil {
			continue
		}
		for _, src := range resources.SkillDirs {
			rel, _ := filepath.Rel(root, filepath.Join(src, "SKILL.md"))
			if packagecontent.ResourceEnabled(rel, filters[packagecontent.Skills]) {
				paths = append(paths, src)
			}
		}
	}
	return paths
}

func collectPackageExtensionConfigs(cwd string, sm *codingagent.SettingsManager, ambientScopes *[]string, resolvers ...extsource.ResolveFunc) []subprocess.ExtConfig {
	var configs []subprocess.ExtConfig
	for _, pkg := range configuredPackagesForResolution(cwd, sm) {
		if !packageScopeEnabled(ambientScopes, pkg.Scope) {
			continue
		}
		root := pkg.InstalledPath
		if root == "" {
			continue
		}
		filters, err := effectiveConfiguredPackageFilters(pkg, resolvers...)
		if err != nil {
			continue
		}
		for _, config := range packageExtensionConfigs(root, filters[packagecontent.Extensions], resolvers...) {
			path := config.Source
			if path == "" {
				path = config.Path
			}
			config.SourceInfo = codingagent.PiSourceInfo{Path: path, Source: pkg.Source.Source, Scope: pkg.Scope, Origin: "package", BaseDir: root}
			configs = append(configs, config)
		}
	}
	return configs
}

// packageExtensionConfigs resolves each extension entry the Package at root
// exposes and filter enables as its own extension, as upstream loads every
// file its manifest entries collect.
func packageExtensionConfigs(root string, filter []string, resolvers ...extsource.ResolveFunc) []subprocess.ExtConfig {
	resources, err := packagecontent.Discover(root)
	if err != nil {
		return nil
	}
	var configs []subprocess.ExtConfig
	for _, src := range resources.ExtensionEntries {
		rel, _ := filepath.Rel(root, src)
		if !packagecontent.ResourceEnabled(rel, filter) {
			continue
		}
		name, err := packagecontent.PublicName(packagecontent.Extensions, src, "")
		if err != nil {
			continue
		}
		config, _, err := subprocess.ResolveExtConfigWithResolver(src, name, configuredExtensionResolver(resolvers))
		if err != nil {
			config = subprocess.UnresolvedExtConfig(src, err)
		}
		configs = append(configs, config)
	}
	return configs
}

func packageScopeEnabled(ambientScopes *[]string, scope string) bool {
	if scope == "project" {
		return ambientSourceEnabled(ambientScopes, "workspace")
	}
	return ambientSourceEnabled(ambientScopes, "user")
}

func ambientSourceEnabled(ambientScopes *[]string, source string) bool {
	return ambientScopes == nil || slices.Contains(*ambientScopes, source)
}

func trustedAmbientScopes(scopes *[]string) *[]string {
	if scopes == nil {
		userOnly := []string{"user"}
		return &userOnly
	}
	trusted := make([]string, 0, len(*scopes))
	for _, scope := range *scopes {
		if scope != "workspace" {
			trusted = append(trusted, scope)
		}
	}
	return &trusted
}

func configuredPackagesForResolution(cwd string, sm *codingagent.SettingsManager) []configuredPackage {
	global := sm.GetGlobalSettings().Packages
	project := sm.GetProjectSettings().Packages
	globals := make([]configuredPackage, 0, len(global))
	globalByIdentity := make(map[string]int, len(global))
	for _, pkg := range global {
		identity := packageSourceIdentity(settingsBaseDirForManager(sm, false), pkg.Source)
		if _, exists := globalByIdentity[identity]; exists {
			continue
		}
		globalByIdentity[identity] = len(globals)
		globals = append(globals, configuredPackage{
			Source: pkg, Scope: "user", InstalledPath: installedPathForConfiguredSource(cwd, sm, pkg.Source, false),
		})
	}

	projects := make([]configuredPackage, 0, len(project))
	seenProject := make(map[string]struct{}, len(project))
	for i := range project {
		pkg := project[i]
		identity := packageSourceIdentity(settingsBaseDirForManager(sm, true), pkg.Source)
		if _, exists := seenProject[identity]; exists {
			continue
		}
		seenProject[identity] = struct{}{}
		globalIndex, inherited := globalByIdentity[identity]
		if inherited && isProjectPackageDelta(pkg) {
			delta := pkg
			globals[globalIndex].ProjectDelta = &delta
			continue
		}
		if inherited {
			delete(globalByIdentity, identity)
			globals[globalIndex].Source.Source = ""
		}
		base := pkg
		if isProjectPackageDelta(pkg) {
			base = codingagent.PackageSource{
				Source: pkg.Source, Extensions: []string{}, Skills: []string{}, Prompts: []string{}, Themes: []string{},
			}
		}
		delta := (*codingagent.PackageSource)(nil)
		if isProjectPackageDelta(pkg) {
			delta = &pkg
		}
		projects = append(projects, configuredPackage{
			Source: base, ProjectDelta: delta, Scope: "project", InstalledPath: installedPathForConfiguredSource(cwd, sm, pkg.Source, true),
		})
	}
	out := projects
	for _, pkg := range globals {
		if pkg.Source.Source != "" {
			out = append(out, pkg)
		}
	}
	return out
}

func installedPathForConfiguredSource(cwd string, sm *codingagent.SettingsManager, source string, local bool) string {
	if detectSourceKind(source) != "local" {
		return installedPathForSource(cwd, source, local)
	}
	root, err := resolveLocalPackageRoot(settingsBaseDirForManager(sm, local), source)
	if err != nil {
		return ""
	}
	if _, err := os.Stat(root); err != nil {
		return ""
	}
	return root
}

func isProjectPackageDelta(pkg codingagent.PackageSource) bool {
	found := false
	for _, entries := range [][]string{pkg.Extensions, pkg.Skills, pkg.Prompts, pkg.Themes} {
		if entries == nil {
			continue
		}
		found = true
		for _, entry := range entries {
			if entry == "" || !strings.ContainsRune("+-!", rune(entry[0])) {
				return false
			}
		}
	}
	return found
}

func resolveSettingsPath(baseDir, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Clean(filepath.Join(baseDir, p))
}

func dedupStrings(in []string) []string {
	return packagecontent.Deduplicate(in)
}

func pathToExtConfig(path string) (subprocess.ExtConfig, bool) {
	configs := pathToExtConfigs(path)
	if len(configs) != 1 || configs[0].ResolveError() != nil {
		return subprocess.ExtConfig{}, false
	}
	return configs[0], true
}

// pathToExtConfigs resolves one extension path. A directory whose source does
// not resolve becomes an unresolved config, so loading reports it as a failed
// extension the way upstream loadExtensions does.
func pathToExtConfigs(path string, resolvers ...extsource.ResolveFunc) []subprocess.ExtConfig {
	if path == "" {
		return nil
	}
	// Use the same conventional factory/standalone resolver as validation and
	// convention-directory discovery.
	config, _, err := subprocess.ResolveExtConfigWithResolver(path, "", configuredExtensionResolver(resolvers))
	if err == nil {
		return []subprocess.ExtConfig{config}
	}
	if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
		return []subprocess.ExtConfig{subprocess.UnresolvedExtConfig(path, err)}
	}
	// Fallback: direct binary or script path that the spec system cannot
	// resolve (e.g. a bare binary path passed via -e /usr/local/bin/my-ext).
	return []subprocess.ExtConfig{{Name: filepath.Base(path), Enabled: true, Path: path}}
}

func mergeExtConfigs(configs []subprocess.ExtConfig) []subprocess.ExtConfig {
	byOrigin := make(map[string]subprocess.ExtConfig, len(configs))
	keys := make([]string, 0, len(configs))
	for _, config := range configs {
		if config.Name == "" {
			base := config.Source
			if base == "" {
				base = config.Path
			}
			config.Name = filepath.Base(strings.TrimSuffix(base, string(filepath.Separator)))
		}
		origin := config.Source
		if origin == "" {
			origin = config.Path
		}
		if absolute, err := filepath.Abs(origin); err == nil {
			origin = absolute
		}
		key := config.Name + "\x00" + filepath.Clean(origin)
		if _, duplicate := byOrigin[key]; !duplicate {
			keys = append(keys, key)
		}
		byOrigin[key] = config
	}
	out := make([]subprocess.ExtConfig, 0, len(keys))
	for _, key := range keys {
		out = append(out, byOrigin[key])
	}
	return out
}

func discoverSkillDir(dir string) []string {
	return packagecontent.DiscoverAgentSkillDirs(dir)
}

// discoverAncestorAgentsSkillDirs walks from startDir up to the git root
// (or filesystem root), collecting .agents/skills/ directories at each level.
// Mirrors upstream collectAncestorAgentsSkillDirs (package-manager.ts).
func discoverAncestorAgentsSkillDirs(startDir string) []string {
	resolved, err := filepath.Abs(startDir)
	if err != nil {
		return nil
	}
	gitRoot := findGitRepoRoot(resolved)

	var dirs []string
	dir := resolved
	for {
		candidate := filepath.Join(dir, ".agents", "skills")
		dirs = append(dirs, candidate)
		if gitRoot != "" && dir == gitRoot {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return dirs
}

// findGitRepoRoot walks up from startDir looking for a .git directory.
// Returns the directory containing .git, or "" if none found.
func findGitRepoRoot(startDir string) string {
	dir := startDir
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
