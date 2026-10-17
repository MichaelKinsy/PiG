package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
	"github.com/MichaelKinsy/PiG/internal/resolvepath"
)

func collectStartupThemePaths(cwd, agentDir string, sm *codingagent.SettingsManager) []string {
	global := sm.GetGlobalSettings()
	paths := collectTopLevelResourcePaths(filepath.Join(agentDir, "themes"), global.Themes, "themes")
	for _, pkg := range global.Packages {
		root := packagemanager.InstalledPathForConfiguredSource(cwd, sm.AgentDir(), sm, pkg.Source, false)
		if packagemanager.ConfiguredPackageNeedsInstall(packagemanager.ConfiguredPackage{Source: pkg, InstalledPath: root}) {
			continue
		}
		items, err := packagemanager.CollectPackageResourceItems(root, packagemanager.ConfiguredPackage{Source: pkg, Scope: "user", InstalledPath: root}, false)
		if err != nil {
			continue
		}
		for _, item := range items {
			if item.ResourceType == "themes" && item.Enabled {
				paths = append(paths, item.Path)
			}
		}
	}
	return dedupStrings(paths)
}

func collectPromptPaths(cwd, agentDir string, sm *codingagent.SettingsManager, flags Args, projectTrusted bool, resolvers ...extsource.ResolveFunc) []string {
	// Pi keeps the first same-name prompt after ordering -e Package prompts
	// (cliEnabledPrompts, kept under --no-prompt-templates), project, user,
	// then Package resources, with --prompt-template paths last
	// (additionalPromptTemplatePaths, resource-loader.ts reload).
	paths := collectCLIPackageResources(cwd, agentDir, sm, flags).Prompts
	if !flags.NoPromptTemplates {
		paths = append(paths, codingagent.AmbientPromptPaths(cwd, agentDir, sm, projectTrusted)...)
		paths = append(paths, collectPackagePromptPaths(cwd, sm, resolvers...)...)
	}
	for _, promptPath := range flags.PromptTemplates {
		paths = append(paths, collectResourceFilesFromPaths([]string{resolveSettingsPath(cwd, promptPath)}, "prompts")...)
	}
	return dedupStrings(paths)
}

// additionalPromptTemplatePaths returns the local --prompt-template paths of resourceFlags, which resolveCLIResourceFlags resolved
// against the launch directory, without trimming, as main.ts does once (:728). resource-loader.ts reload trims each one and checks it
// for existence (:615-626).
func additionalPromptTemplatePaths(resourceFlags Args) []string {
	paths := make([]string, 0, len(resourceFlags.PromptTemplates))
	for _, p := range resourceFlags.PromptTemplates {
		if codingagent.IsLocalPath(p) {
			paths = append(paths, p)
		}
	}
	return paths
}

// collectThemePaths lists theme paths in upstream precedence order, the first
// theme of a name winning: -e Package themes, project, user, Packages, then --theme paths (resource-loader.ts reload).
func collectThemePaths(cwd, agentDir string, sm *codingagent.SettingsManager, flags Args, projectTrusted bool, resolvers ...extsource.ResolveFunc) []string {
	paths := collectCLIPackageResources(cwd, agentDir, sm, flags).Themes
	if !flags.NoThemes {
		if projectRoot := codingagent.ProjectConfigDir(cwd); projectTrusted {
			paths = append(paths, collectTopLevelResourcePaths(filepath.Join(projectRoot, "themes"), sm.GetProjectSettings().Themes, "themes")...)
		}
		paths = append(paths, collectTopLevelResourcePaths(filepath.Join(agentDir, "themes"), sm.GetGlobalSettings().Themes, "themes")...)
		paths = append(paths, collectPackageThemePaths(cwd, sm, resolvers...)...)
	}
	for _, p := range flags.Themes {
		paths = append(paths, resolveSettingsPath(cwd, p))
	}
	return dedupStrings(paths)
}

// packageManagerHomeDir is Pi's getHomeDir (package-manager.ts).
func packageManagerHomeDir() string { return codingagent.PackageManagerHomeDir() }

// collectSkillInputs appends --skill paths after resolved project, user, and Package skills, as DefaultResourceLoader.reload does. --no-skills keeps the explicit paths alone.
// Ports packages/coding-agent/src/core/resource-loader.ts
func collectSkillInputs(cwd, agentDir string, sm *codingagent.SettingsManager, flags Args, ambientScopes *[]string, resolvers ...extsource.ResolveFunc) []string {
	return collectSkillInputsIn(codingagent.NewAgentsSkillScan(cwd), cwd, agentDir, sm, flags, ambientScopes, resolvers...)
}

// collectSkillInputsIn is collectSkillInputs with the ancestor .agents/skills directories of scan, which must be of cwd: one
// resolve's skill inputs and source infos share one walk, as Pi's resolve() walks them once.
func collectSkillInputsIn(scan *codingagent.AgentsSkillScan, cwd, agentDir string, sm *codingagent.SettingsManager, flags Args, ambientScopes *[]string, resolvers ...extsource.ResolveFunc) []string {
	packageSkills := collectCLIPackageResources(cwd, agentDir, sm, flags).Skills
	var cliInputs []string
	for _, p := range flags.Skills {
		cliInputs = append(cliInputs, collectResourceFilesFromPaths([]string{resolveSettingsPath(cwd, p)}, "skills")...)
	}
	if flags.NoSkills {
		return dedupStrings(append(packageSkills, cliInputs...))
	}

	// -e Package skills (cliEnabledSkills) lead and survive --no-skills; the resolved paths follow resourcePrecedenceRank; explicit --skill paths are additionalSkillPaths, last.
	inputs := slices.Clone(packageSkills)
	inputs = append(inputs, codingagent.AmbientSkillPathsIn(scan, cwd, agentDir, sm, packagemanager.AmbientSourceEnabled(ambientScopes, "workspace"), packagemanager.AmbientSourceEnabled(ambientScopes, "user"))...)
	inputs = append(inputs, collectPackageSkillPaths(cwd, sm, ambientScopes, resolvers...)...)
	return dedupStrings(append(inputs, cliInputs...))
}

func collectExtensionConfigs(cwd, agentDir string, sm *codingagent.SettingsManager, flags Args, ambientScopes *[]string, resolvers ...extsource.ResolveFunc) []subprocess.ExtConfig {
	return collectExtensionConfigsWith(collectCLIExtensionConfigs(cwd, agentDir, sm, flags.Extensions, resolvers...), cwd, agentDir, sm, flags, ambientScopes, resolvers...)
}

// cliExtensionSet is the extension configs of the -e sources: those that load first, and the missing or unresolved ones, reported after the rest.
type cliExtensionSet struct {
	configs, missing []subprocess.ExtConfig
}

// collectCLIExtensionConfigs resolves the -e sources once, as Pi's resolveExtensionSources does for one load (resource-loader.ts:525-528).
func collectCLIExtensionConfigs(cwd, agentDir string, sm *codingagent.SettingsManager, sources []string, resolvers ...extsource.ResolveFunc) cliExtensionSet {
	var configs, missing []subprocess.ExtConfig
	for _, p := range sources {
		if strings.HasPrefix(p, codingagent.BuiltinPathPrefix) {
			// `-e builtin:<name>` loads a built-in extension; the extension set loads it and reports an unknown name (package-manager.ts:996-1006).
			continue
		}
		if kind := packagemanager.DetectSourceKind(p); kind == "git" || kind == "npm" {
			// Pi package-manager.ts:1293-1307 collects an npm or git -e checkout only as a Package; unlike a local directory (:1346-1351), it never loads the checkout root itself.
			resolved, err := resolveCLIPackageSource(cwd, agentDir, sm, p)
			if err != nil {
				// Pi resolves the sources in turn and stops at the first rejection, so no later source installs (package-manager.ts:1293-1342).
				missing = append(missing, subprocess.UnresolvedExtConfig(p, &cliSourceResolveError{err: err}))
				return cliExtensionSet{configs: configs, missing: missing}
			}
			for _, resource := range resolved.Extensions {
				if resource.Enabled {
					configs = append(configs, withCLISourceInfo(packageExtensionConfigAt(resource.Path, resolvers...))...)
				}
			}
			continue
		}
		resolved, err := resolveCLIExtensionSource(cwd, agentDir, sm, p, nil)
		if err != nil {
			missing = append(missing, subprocess.UnresolvedExtConfig(p, err))
			continue
		}
		if resolved == "" {
			continue
		}
		for _, config := range cliExtensionConfigs(resolved, resolvers...) {
			if _, absent := errors.AsType[extensionPathMissingError](config.ResolveError()); absent {
				missing = append(missing, config)
				continue
			}
			configs = append(configs, config)
		}
	}
	return cliExtensionSet{configs: configs, missing: missing}
}

// collectExtensionConfigsWith is collectExtensionConfigs with the -e sources already resolved.
func collectExtensionConfigsWith(cli cliExtensionSet, cwd, agentDir string, sm *codingagent.SettingsManager, flags Args, ambientScopes *[]string, resolvers ...extsource.ResolveFunc) []subprocess.ExtConfig {
	// Upstream resource-loader.ts loads -e paths first, then the resolved
	// paths in package-manager.ts resourcePrecedenceRank order: project
	// settings entries, project auto-discovery, user settings entries, user
	// auto-discovery, then Packages. It reports a missing -e path after
	// loading. --no-extensions keeps only the -e paths.
	configs := append(make([]subprocess.ExtConfig, 0, len(cli.configs)), cli.configs...)
	if !flags.NoExtensions {
		if projectRoot := codingagent.ProjectConfigDir(cwd); packagemanager.AmbientSourceEnabled(ambientScopes, "workspace") {
			configs = append(configs, collectTopLevelExtensionConfigs(filepath.Join(projectRoot, "extensions"), sm.GetProjectSettings().Extensions, "project", resolvers...)...)
		}
		if packagemanager.AmbientSourceEnabled(ambientScopes, "user") {
			configs = append(configs, collectTopLevelExtensionConfigs(filepath.Join(agentDir, "extensions"), sm.GetGlobalSettings().Extensions, "user", resolvers...)...)
		}
		configs = append(configs, collectPackageExtensionConfigs(cwd, sm, ambientScopes, resolvers...)...)
	}
	return mergeExtConfigs(uniqueExtensionPaths(append(configs, cli.missing...)))
}

// uniqueExtensionPaths retains the first selected alias of each canonical extension source before any factory runs.
func uniqueExtensionPaths(configs []subprocess.ExtConfig) []subprocess.ExtConfig {
	type sourceKey struct{ path, module, pkg, factory string }
	seen := make(map[sourceKey]struct{}, len(configs))
	out := make([]subprocess.ExtConfig, 0, len(configs))
	for _, config := range configs {
		path := config.Source
		if path == "" {
			path = config.Path
		}
		if path == "" {
			out = append(out, config)
			continue
		}
		if absolute, err := filepath.Abs(path); err == nil {
			path = absolute
		}
		// pig additive (D20): distinct native factories in one source module retain their separate registrations.
		key := sourceKey{codingagent.CanonicalizePath(path), config.ModulePath, config.Package, config.Factory}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, config)
	}
	return out
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
	if err == nil && info.IsDir() && (packagecontent.HasPiManifest(resolved) || hasConventionalPackageDir(resolved)) {
		// Upstream resolves a directory with a "pi" manifest, or with an
		// extensions, skills, prompts or themes directory, as a Package
		// (resolveLocalExtensionSource, collectPackageResources): each entry
		// its manifest or its extensions directory yields is its own
		// extension, and a Package that yields none loads nothing.
		return withCLISourceInfo(packageExtensionConfigs(resolved, resolvers...))
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

// hasConventionalPackageDir reports whether root holds one of the resource directories collectPackageResources treats as a Package without a manifest.
func hasConventionalPackageDir(root string) bool {
	for _, name := range []string{"extensions", "skills", "prompts", "themes"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			return true
		}
	}
	return false
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
	automatic := filterAutoDiscoveredPaths(collectAutoDiscoveredResourcePaths(autoDir, "extensions"), entries, baseDir, "extensions")
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
	return codingagent.TopLevelResourcePaths(autoDir, entries, packagecontent.Kind(kind))
}

// resolveConfiguredResourceEntries lists a settings array's resources. An entry
// that fails to resolve empties the list: validateConfiguredResourceEntries
// reports that error where Pi's resourceLoader.reload() throws, before any
// collector runs.
func resolveConfiguredResourceEntries(entries []string, baseDir string, kind string) []string {
	resolved, err := packagecontent.ResolveConfiguredWithError(entries, baseDir, packagecontent.Kind(kind))
	if err != nil {
		return nil
	}
	return resolved
}

// validateConfiguredResourceEntries returns the first error Pi's packageManager.resolve() throws for configured settings: package identities first (validateConfiguredPackageSources), then an installed npm or git Package's pattern that globSync or minimatch throws for (packagemanager.ValidateConfiguredPackagePatterns), then the settings extensions, skills, prompts and themes arrays in package-manager.ts:933-958 order (project scope, then user scope, for each type), as resolvePath(entry, baseDir, { trim: true }) or the array's patterns throw, then the overrides the auto-discovered resources meet, then the built-in extensions' overrides (:960-986). An untrusted project contributes no settings or Packages.
func validateConfiguredResourceEntries(cwd, agentDir string, sm *codingagent.SettingsManager, projectTrusted bool) error {
	if err := validateConfiguredPackageSources(sm, projectTrusted); err != nil {
		return err
	}
	if err := packagemanager.ValidateConfiguredPackagePatterns(cwd, agentDir, sm); err != nil {
		return err
	}
	projectRoot, projectEnabled := codingagent.ProjectConfigDir(cwd), projectTrusted
	project, user := sm.GetProjectSettings(), sm.GetGlobalSettings()
	for _, kind := range []struct {
		project, user []string
		kind          packagecontent.Kind
	}{
		{project.Extensions, user.Extensions, packagecontent.Extensions},
		{project.Skills, user.Skills, packagecontent.Skills},
		{project.Prompts, user.Prompts, packagecontent.Prompts},
		{project.Themes, user.Themes, packagecontent.Themes},
	} {
		if projectEnabled {
			if err := packagecontent.ValidateConfiguredEntries(kind.project, projectRoot, kind.kind); err != nil {
				return err
			}
		}
		if err := packagecontent.ValidateConfiguredEntries(kind.user, agentDir, kind.kind); err != nil {
			return err
		}
	}
	if err := codingagent.ValidateAutoDiscoveredOverrides(cwd, agentDir, sm, projectEnabled); err != nil {
		return err
	}
	_, err := packagemanager.ResolveBuiltinExtensions(sm, cliBuiltinExtensionNames())
	return err
}

// cliSourceResolveError is an npm or git -e source's resolution failure: an install that fails, or a pattern of its Package that globSync or minimatch throws for. Pi's resolveExtensionSources resolves those sources outside any catch (package-manager.ts:1293-1342), so the first one rejects the load.
type cliSourceResolveError struct{ err error }

func (e *cliSourceResolveError) Error() string { return e.err.Error() }

func (e *cliSourceResolveError) Unwrap() error { return e.err }

// cliPackageSourceError returns the first error Pi's resolveExtensionSources throws for the -e sources, in -e order: an npm or git source's resolution failure, which collectExtensionConfigs keeps as that source's unresolved config. Pi resolves the -e sources after the settings, in the reload and in the pre-trust extension load, before any extension factory runs (resource-loader.ts:525-528, 676-680). A local source's throw is caught and drops only its own kinds, and a missing local path is reported after the load.
func cliPackageSourceError(configs []subprocess.ExtConfig) error {
	for _, config := range configs {
		if failed, ok := errors.AsType[*cliSourceResolveError](config.ResolveError()); ok {
			return failed.err
		}
	}
	return nil
}

// validateConfiguredPackageSources computes every configured package identity in package-manager.ts:912-926 order (project packages, then user packages) and returns the first error dedupePackages throws: fileURLToPath's error for an invalid local file: URL. Pi computes these identities before installing or resolving any package. An untrusted project contributes no packages.
func validateConfiguredPackageSources(sm *codingagent.SettingsManager, projectTrusted bool) error {
	if projectTrusted {
		for _, pkg := range sm.GetProjectSettings().Packages {
			if _, err := packagemanager.PackageSourceIdentityChecked(packagemanager.SettingsBaseDir(sm.CWD(), sm.AgentDir(), true), pkg.Source); err != nil {
				return err
			}
		}
	}
	for _, pkg := range sm.GetGlobalSettings().Packages {
		if _, err := packagemanager.PackageSourceIdentityChecked(packagemanager.SettingsBaseDir(sm.CWD(), sm.AgentDir(), false), pkg.Source); err != nil {
			return err
		}
	}
	return nil
}

// filterAutoDiscoveredPaths keeps the auto-discovered paths the overrides leave enabled; a pattern minimatch cannot compile leaves none, and validateConfiguredResourceEntries reports it where Pi's resolve() rejects.
func filterAutoDiscoveredPaths(paths, overrides []string, baseDir string, kind string) []string {
	filtered, err := packagecontent.FilterAutomatic(paths, overrides, baseDir, packagecontent.Kind(kind))
	if err != nil {
		return nil
	}
	return filtered
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

// applyResourcePatterns keeps the paths the patterns enable; a pattern minimatch cannot compile keeps none, and validateConfiguredResourceEntries reports it where Pi's resolve() rejects.
func applyResourcePatterns(allPaths, patterns []string, baseDir, kind string) []string {
	kept, err := packagecontent.ApplyPatterns(allPaths, patterns, baseDir, packagecontent.Kind(kind))
	if err != nil {
		return nil
	}
	return kept
}

// isEnabledByOverrides reports whether the overrides leave filePath enabled; a pattern minimatch cannot compile disables it, and validateConfiguredResourceEntries reports it where Pi's resolve() rejects.
func isEnabledByOverrides(filePath string, patterns []string, baseDir, kind string) bool {
	enabled, err := packagecontent.EnabledByOverrides(filePath, patterns, baseDir, packagecontent.Kind(kind))
	return enabled && err == nil
}

func effectiveConfiguredPackageFilters(pkg packagemanager.ConfiguredPackage, resolvers ...extsource.ResolveFunc) (map[packagecontent.Kind][]string, error) {
	filters := packagemanager.ConfiguredPackageFilters(pkg.Source)
	if !packagemanager.IsProjectPackageDelta(pkg.Source) {
		return filters, nil
	}
	items, err := packagemanager.CollectPackageResourceItems(pkg.InstalledPath, pkg, true, resolvers...)
	if err != nil {
		return nil, err
	}
	for kind := range filters {
		filters[kind] = []string{}
	}
	for _, item := range items {
		if !item.Enabled {
			continue
		}
		path := item.Path
		if item.ResourceType == "skills" {
			path = packagecontent.SkillFile(path)
		}
		relative, err := filepath.Rel(pkg.InstalledPath, path)
		if err != nil {
			return nil, err
		}
		kind := packagecontent.Kind(item.ResourceType)
		filters[kind] = append(filters[kind], filepath.ToSlash(relative))
	}
	return filters, nil
}

// extensionLoadFailureHint mirrors upstream main.ts EXTENSION_LOAD_FAILURE_HINT.
const extensionLoadFailureHint = `Hint: Start without extensions using "` + codingagent.AppName + ` -ne".`

// validateConfiguredPackagesForStartup returns a Package diagnostic without
// making startup fatal. Manifest parsing matches discovery. Extension failures
// belong to the final extension load, which reports every failure together.
// Scopes that do not load extensions skip source resolution.
func validateConfiguredPackagesForStartup(cwd string, sm *codingagent.SettingsManager, loadsExtensions func(scope string) bool, resolvers ...extsource.ResolveFunc) error {
	for _, pkg := range packagemanager.ResolvedConfiguredPackageSources(cwd, sm.AgentDir(), sm, true) {
		if packagemanager.ConfiguredPackageNeedsInstall(pkg) {
			continue
		}
		// upstream: package-manager.ts:1368-1372 resolveLocalExtensionSource: a local source that is a file is one extension, not a Package with a manifest; its load failure is reported by the extension load.
		if stat, err := os.Stat(pkg.InstalledPath); err == nil && stat.Mode().IsRegular() {
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
		if _, _, _, err := packagecontent.ValidateConfiguredForStartupWithResolver(pkg.InstalledPath, filters, packagemanager.ConfiguredExtensionResolver(resolvers)); err != nil {
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
// -e path. An extension runtime that reported its own loader error (Node's
// cell.mjs) already words it as Pi's loader does.
func extensionLoadFailureDiagnostic(path string, err error) codingagent.AgentSessionRuntimeDiagnostic {
	message := "Failed to load extension: " + err.Error()
	if build, built := errors.AsType[*runtimecell.BuildFailure](err); built {
		// pig additive (D20): a failed compile is one summary line in Pi's loader-error shape (Pi 0.87.1 loader.ts:578 `Failed to load extension: ${message}`); the compiler output is in the log it names.
		message = "Failed to load extension: " + build.Error()
	}
	if _, missing := errors.AsType[extensionPathMissingError](err); missing {
		message = err.Error()
	}
	if factoryErr, reported := errors.AsType[*subprocess.FactoryLoadError](err); reported {
		message = factoryErr.Error()
	}
	return codingagent.AgentSessionRuntimeDiagnostic{Type: "error", Message: fmt.Sprintf(`Failed to load extension "%s": %s`, path, message)}
}

// extensionLoadDiagnostics formats host load errors. An error without an
// extension path, such as an embedded cell failure, keeps its own text.
func extensionLoadDiagnostics(errs []error) []codingagent.AgentSessionRuntimeDiagnostic {
	diagnostics := make([]codingagent.AgentSessionRuntimeDiagnostic, 0, len(errs))
	for _, err := range subprocess.GroupLoadErrors(errs) {
		if group, ok := errors.AsType[*subprocess.BuildFailureGroup](err); ok {
			diagnostics = append(diagnostics, codingagent.AgentSessionRuntimeDiagnostic{Type: "error", Message: group.Error()})
			continue
		}
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

// reportExtensionLoadFailures mirrors upstream main.ts on extension startup
// errors: it reports the startup diagnostics, then the -ne hint in yellow when
// one of them is a load failure (main.ts:912-914). A failed virtual-model
// registration alone reports no hint.
func reportExtensionLoadFailures(diagnostics []codingagent.AgentSessionRuntimeDiagnostic) {
	codingagent.ReportDiagnostics(codingagent.DeduplicateDiagnostics(diagnostics))
	if !slices.ContainsFunc(diagnostics, func(diagnostic codingagent.AgentSessionRuntimeDiagnostic) bool {
		// pig additive (D20): a grouped build failure stands for one `Failed to load extension` error per member, so it gets Pi's hint too.
		return strings.Contains(diagnostic.Message, "Failed to load extension") || subprocess.IsBuildFailureGroupMessage(diagnostic.Message)
	}) {
		return
	}
	hint := extensionLoadFailureHint
	if chalkStdoutColor() {
		hint = codingagent.ChalkApply("\x1b[33m", "\x1b[39m", hint)
	}
	fmt.Fprintln(os.Stderr, hint)
}

func invalidConfiguredPackageError(pkg packagemanager.ConfiguredPackage, err error) error {
	return fmt.Errorf("%s Package %q at %s is invalid: %w", pkg.Scope, pkg.Source.Source, pkg.InstalledPath, err)
}

func collectPackagePromptPaths(cwd string, sm *codingagent.SettingsManager, resolvers ...extsource.ResolveFunc) []string {
	return packagemanager.CollectEnabledPackagePaths(cwd, sm.AgentDir(), sm, packagecontent.Prompts, nil, resolvers...)
}

func collectPackageThemePaths(cwd string, sm *codingagent.SettingsManager, resolvers ...extsource.ResolveFunc) []string {
	return packagemanager.CollectEnabledPackagePaths(cwd, sm.AgentDir(), sm, packagecontent.Themes, nil, resolvers...)
}

func collectPackageSkillPaths(cwd string, sm *codingagent.SettingsManager, ambientScopes *[]string, resolvers ...extsource.ResolveFunc) []string {
	return packagemanager.CollectEnabledPackagePaths(cwd, sm.AgentDir(), sm, packagecontent.Skills, ambientScopes, resolvers...)
}

func collectPackageExtensionConfigs(cwd string, sm *codingagent.SettingsManager, ambientScopes *[]string, resolvers ...extsource.ResolveFunc) []subprocess.ExtConfig {
	items, _ := packagemanager.CollectResolvedPackageResourceItems(cwd, sm.AgentDir(), sm, ambientScopes, false, resolvers...)
	var configs []subprocess.ExtConfig
	for _, item := range items {
		if item.ResourceType != "extensions" || !item.Enabled {
			continue
		}
		for _, config := range pathToExtConfigs(item.Path, resolvers...) {
			config.SourceInfo = codingagent.PiSourceInfo{Path: item.Path, Source: item.Source, Scope: item.Scope, Origin: "package", BaseDir: item.BaseDir}
			config.PackageRoot = item.PackageRoot
			configs = append(configs, config)
		}
	}
	return configs
}

// packageExtensionConfigAt is the extension config of one extension entry of a Package: the entry's public name, or an unresolved config when it does not resolve; an entry without a public name yields none.
func packageExtensionConfigAt(src string, resolvers ...extsource.ResolveFunc) []subprocess.ExtConfig {
	name, err := packagecontent.PublicName(packagecontent.Extensions, src, "")
	if err != nil {
		return nil
	}
	config, _, err := subprocess.ResolveExtConfigWithResolver(src, name, packagemanager.ConfiguredExtensionResolver(resolvers))
	if err != nil {
		config = subprocess.UnresolvedExtConfig(src, err)
	}
	return []subprocess.ExtConfig{config}
}

// packageExtensionConfigs resolves each extension entry the Package at root
// exposes as its own extension, as upstream loads every file its manifest
// entries collect.
func packageExtensionConfigs(root string, resolvers ...extsource.ResolveFunc) []subprocess.ExtConfig {
	// A manifest glob that throws ends discovery, and resolveLocalExtensionSource's catch keeps the extensions collected before it (package-manager.ts:1372-1389).
	resources, _ := packagecontent.Discover(root)
	var configs []subprocess.ExtConfig
	for _, src := range resources.ExtensionEntries {
		configs = append(configs, packageExtensionConfigAt(src, resolvers...)...)
	}
	return configs
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

// resolveSettingsPath is Pi's resolvePath(p, baseDir, { trim: true }) for
// already-validated settings entries and already-resolved CLI paths. The
// lenient fallback for an invalid file: URL is unreachable at startup, where
// validateConfiguredResourceEntries and resolveCLIResourceFlags fail first.
func resolveSettingsPath(baseDir, p string) string {
	if p == "" {
		return p
	}
	if resolved, err := resolvepath.ResolveTrimmed(p, baseDir); err == nil {
		return resolved
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Clean(filepath.Join(baseDir, p))
}

func dedupStrings(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, value := range in {
		key := canonicalStatusPath(value)
		if value != "" && !seen[key] {
			seen[key] = true
			out = append(out, value)
		}
	}
	return out
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
	config, _, err := subprocess.ResolveExtConfigWithResolver(path, "", packagemanager.ConfiguredExtensionResolver(resolvers))
	if err == nil {
		return []subprocess.ExtConfig{config}
	}
	if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
		return []subprocess.ExtConfig{subprocess.UnresolvedExtConfig(path, err)}
	}
	// A file upstream's loader cannot import, such as a packed .tgz, fails to
	// load with that error rather than being run as a program.
	if _, ok := errors.AsType[*extsource.UnknownFileExtensionError](err); ok {
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
