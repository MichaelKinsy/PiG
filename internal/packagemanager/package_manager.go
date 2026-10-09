package packagemanager

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	sourceref "github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// PackageManager is upstream's PackageManager interface and DefaultPackageManager class (package-manager.ts): it installs, removes and lists the packages named in the user and project settings. CWD is the project directory the sources resolve against.
type PackageManager struct {
	CWD             string
	SettingsManager *codingagent.SettingsManager
	// AgentDir is upstream's agentDir option; empty uses the settings manager's agent directory.
	AgentDir string
	// BuiltinExtensions are the names of built-in extensions that Resolve reports as `builtin:<name>` extension resources (upstream's builtinExtensions option).
	BuiltinExtensions []string
	progress          ProgressCallback
}

// PackageManagerOptions is upstream's PackageManagerOptions (package-manager.ts:132-138).
type PackageManagerOptions struct {
	CWD             string
	AgentDir        string
	SettingsManager *codingagent.SettingsManager
	// BuiltinExtensions are the names of built-in extensions, resolved as `builtin:<name>` extension resources.
	BuiltinExtensions []string
}

// NewPackageManager is upstream's `new DefaultPackageManager(options)` (package-manager.ts:821).
func NewPackageManager(options PackageManagerOptions) *PackageManager {
	return &PackageManager{CWD: options.CWD, AgentDir: options.AgentDir, SettingsManager: options.SettingsManager, BuiltinExtensions: slices.Clone(options.BuiltinExtensions)}
}

// SetProgressCallback is upstream's setProgressCallback: the callback every later install, remove and update reports to.
func (m *PackageManager) SetProgressCallback(callback ProgressCallback) { m.progress = callback }

func (m *PackageManager) agentDir() string {
	if m.AgentDir != "" {
		return m.AgentDir
	}
	return m.SettingsManager.AgentDir()
}

// ListConfiguredPackages is upstream's listConfiguredPackages: user then project packages with their installed paths.
func (m *PackageManager) ListConfiguredPackages() []ConfiguredPackage {
	packages := m.configuredSources()
	for i := range packages {
		pkg := &packages[i]
		pkg.InstalledPath = m.GetInstalledPath(pkg.Source.Source, pkg.Scope)
	}
	return packages
}

func (m *PackageManager) configuredSources() []ConfiguredPackage {
	global := m.SettingsManager.GetGlobalSettings().Packages
	project := m.SettingsManager.GetProjectSettings().Packages
	packages := make([]ConfiguredPackage, 0, len(global)+len(project))
	for _, pkg := range global {
		packages = append(packages, ConfiguredPackage{Source: pkg, Scope: "user"})
	}
	for _, pkg := range project {
		packages = append(packages, ConfiguredPackage{Source: pkg, Scope: "project"})
	}
	return packages
}

// ConfiguredSources lists the configured packages without resolving installed paths: the update path reads sources, not installation paths, so pinned entries must not run legacy-root lookups.
func (m *PackageManager) ConfiguredSources() []ConfiguredPackage { return m.configuredSources() }

// GetInstalledPath is upstream's getInstalledPath: where the source is installed for the scope ("user" or "project"), or "" when it is not.
func (m *PackageManager) GetInstalledPath(source, scope string) string {
	return InstalledPathForConfiguredSource(m.CWD, m.agentDir(), m.SettingsManager, source, scope == "project")
}

// Install is upstream's install: it places the package artifacts without recording the source in settings.
func (m *PackageManager) Install(source string, local bool) error {
	return InstallPackageArtifacts(m.CWD, m.agentDir(), m.SettingsManager, codingagent.PackageSource{Source: source}, local, m.progress)
}

// InstallAndPersist is upstream's installAndPersist: install, then record the source in settings.
func (m *PackageManager) InstallAndPersist(source string, local bool) error {
	if err := m.Install(source, local); err != nil {
		return err
	}
	if err := m.VerifyContributesResources(source, local); err != nil {
		return err
	}
	_, err := m.AddSourceToSettings(source, local)
	return err
}

// Remove is upstream's remove: it deletes the installed artifacts and keeps the settings entry.
func (m *PackageManager) Remove(source string, local bool) error {
	if local && !m.SettingsManager.IsProjectTrusted() {
		return errors.New("Project is not trusted; refusing to access project package storage")
	}
	switch DetectSourceKind(source) {
	case "npm":
		return m.uninstallManagedNPM(source, local)
	case "git":
		checkout, err := GitCheckoutPath(m.CWD, m.agentDir(), source, local)
		if err != nil {
			return err
		}
		if m.ConfiguredGitCheckoutInUse(source, local) {
			return nil
		}
		if err := os.RemoveAll(checkout); err != nil {
			return err
		}
		if err := RemoveGitUpdateMarker(GitUpdateMarkerPath(checkout)); err != nil {
			return err
		}
		return PruneEmptyGitParents(checkout, codingagent.GitInstallRoot(m.CWD, m.agentDir(), local))
	default:
		return nil
	}
}

// RemoveAndPersist is upstream's removeAndPersist. It runs the removal with the caller's settings before persisting, so failures retain both the package record and command overrides.
func (m *PackageManager) RemoveAndPersist(source string, local bool) (bool, error) {
	if err := m.Remove(source, local); err != nil {
		return false, err
	}
	return m.RemoveSourceFromSettings(source, local)
}

// AddSourceToSettings is upstream's addSourceToSettings. It reports whether it adds a source or replaces its ref, retaining filters and avoiding writes for identical sources.
func (m *PackageManager) AddSourceToSettings(source string, local bool) (bool, error) {
	sm := m.SettingsManager
	baseDir := SettingsBaseDir(sm.CWD(), sm.AgentDir(), local)
	normalized := m.NormalizeSourceForSettings(baseDir, source)
	current := sm.GetGlobalSettings().Packages
	if local {
		current = sm.GetProjectSettings().Packages
	}
	current = slices.Clone(current)
	// package-manager.ts:830 findIndex stops at the first match; PackageSourcesMatch throws for an invalid file: URL before it.
	index := -1
	for i, existing := range current {
		matched, err := PackageSourcesMatch(m.CWD, baseDir, existing.Source, source)
		if err != nil {
			return false, err
		}
		if matched {
			index = i
			break
		}
	}
	if index >= 0 {
		if current[index].Source == normalized {
			return false, nil
		}
		current[index].Source = normalized
	} else {
		current = append(current, codingagent.PackageSource{Source: normalized})
	}
	if local {
		return true, sm.SetProjectPackages(current)
	}
	return true, sm.SetPackages(current)
}

// RemoveSourceFromSettings is upstream's removeSourceFromSettings (package-manager.ts:855-871). PackageSourcesMatch (:1428-1433) resolves the stored key and then the input key for each entry, so an invalid file: URL fails the removal with fileURLToPath's error once any package is configured.
func (m *PackageManager) RemoveSourceFromSettings(source string, local bool) (bool, error) {
	sm := m.SettingsManager
	current := sm.GetGlobalSettings().Packages
	if local {
		current = sm.GetProjectSettings().Packages
	}
	baseDir := SettingsBaseDir(sm.CWD(), sm.AgentDir(), local)
	next := make([]codingagent.PackageSource, 0, len(current))
	for _, pkg := range current {
		matched, err := PackageSourcesMatch(m.CWD, baseDir, pkg.Source, source)
		if err != nil {
			return false, err
		}
		if !matched {
			next = append(next, pkg)
		}
	}
	if len(next) == len(current) {
		return false, nil
	}
	if local {
		return true, sm.SetProjectPackages(next)
	}
	return true, sm.SetPackages(next)
}

// NormalizeSourceForSettings stores local package paths relative to the settings directory, matching upstream.
func (m *PackageManager) NormalizeSourceForSettings(baseDir, source string) string {
	kind := DetectSourceKind(source)
	if kind == "npm" && !strings.HasPrefix(strings.TrimSpace(source), "npm:") {
		return "npm:" + strings.TrimSpace(source)
	}
	if kind != "local" {
		return source
	}
	resolved, _ := ResolveLocalPackageRoot(m.CWD, source)
	rel, err := filepath.Rel(baseDir, resolved)
	if err != nil {
		return source
	}
	return filepath.Clean(rel)
}

// InstalledSourceRoot returns the directory an install of source contributes, or "" when none exists. A local source is still the path as typed, relative to the project directory; only the settings record rebases it onto the settings directory.
func (m *PackageManager) InstalledSourceRoot(source string, local bool) string {
	if DetectSourceKind(source) != "local" {
		return InstalledPathForSource(m.CWD, m.agentDir(), m.SettingsManager, source, local)
	}
	root, err := ResolveInputPackageSourceRoot(m.CWD, m.agentDir(), m.SettingsManager, source)
	if err != nil {
		return ""
	}
	if _, err := os.Stat(root); err != nil {
		return ""
	}
	return root
}

// VerifyContributesResources rejects an npm or Git source that resolves to a proven extension root Package discovery cannot load. A local source is never refused: a local directory that declares no Package resources loads as an extension itself (package-manager.ts:1377-1384).
func (m *PackageManager) VerifyContributesResources(source string, local bool) error {
	if DetectSourceKind(source) == "local" {
		return nil
	}
	// pig divergence (D57): the npm and Git refusal has no upstream counterpart.
	root := m.InstalledSourceRoot(source, local)
	if root == "" {
		// The source resolved to no local root (a remote form this build cannot inspect). Nothing to assert against, so stay out of the way.
		return nil
	}
	resources, err := packagecontent.Discover(root)
	if err != nil {
		return fmt.Errorf("inspect installed package %s: %w", source, err)
	}
	if PackageResourceCount(resources) > 0 {
		return nil
	}
	// Refuse only a complete extension contract. Empty Packages remain valid.
	if !directoryIsProvablyAnExtension(root) {
		return nil
	}
	return fmt.Errorf("%s is an extension, not a package, so installing it as one loads nothing.\n"+
		"Load it directly with `pig -e %s`, put it in ~/.pig/agent/extensions/, or publish it inside a "+
		"package's extensions/ directory", source, source)
}

// directoryIsProvablyAnExtension accepts a statically proven factory or standalone contract. Node factory resolution only selects an entrypoint; proving its exports requires execution, which Package installation must not perform.
func directoryIsProvablyAnExtension(root string) bool {
	definition, err := extsource.Resolve(root)
	return err == nil && (definition.Language != "node" || definition.Form != extsource.Factory)
}

// PackageResourceCount counts the resources a package contributes.
func PackageResourceCount(r packagecontent.Resources) int {
	return len(r.ExtensionEntries) + len(r.SkillDirs) + len(r.PromptFiles) + len(r.ThemeFiles) +
		len(r.AgentFiles) + len(r.MCPFiles) + len(r.HookFiles) + len(r.AgentEnvironments)
}

// ConfiguredGitCheckoutInUse reports whether another configured source of the same scope shares the Git checkout of removedSource.
func (m *PackageManager) ConfiguredGitCheckoutInUse(removedSource string, local bool) bool {
	removed, err := sourceref.Parse(removedSource, sourceref.Options{Bare: sourceref.BareReject})
	if err != nil || removed.Kind != sourceref.KindGit {
		return false
	}
	wantScope := "user"
	if local {
		wantScope = "project"
	}
	sm := m.SettingsManager
	removedKey := PackageSourceIdentity(m.CWD, removedSource)
	for _, pkg := range m.ListConfiguredPackages() {
		if pkg.Scope != wantScope || PackageSourceIdentity(SettingsBaseDir(sm.CWD(), sm.AgentDir(), local), pkg.Source.Source) == removedKey {
			continue
		}
		ref, err := sourceref.Parse(pkg.Source.Source, sourceref.Options{Bare: sourceref.BareReject})
		if err == nil && ref.Kind == sourceref.KindGit && ref.GitHost == removed.GitHost && ref.GitPath == removed.GitPath {
			return true
		}
	}
	return false
}

func (m *PackageManager) uninstallManagedNPM(source string, local bool) error {
	ref, err := ParseNpmInstallRef(source)
	if err != nil {
		return err
	}
	installRoot := NpmInstallRoot(m.CWD, m.agentDir(), ref, local)
	if _, err := os.Stat(installRoot); os.IsNotExist(err) {
		return nil
	}
	command := DefaultNpmCommand(m.SettingsManager)
	args := append([]string{}, command[1:]...)
	manager, err := PackageManagerName(command)
	if err != nil {
		return err
	}
	if manager == "bun" {
		args = append(args, "uninstall", ref.NPMName, "--cwd", installRoot)
	} else {
		args = append(args, "uninstall", ref.NPMName, "--prefix", installRoot)
		if manager != "pnpm" {
			args = append(args, "--legacy-peer-deps")
		}
	}
	if ref.NPMRegistry != "" {
		args = append(args, "--registry", ref.NPMRegistry)
	}
	return RunPackageProcess("", command[0], args...)
}

// Update is upstream's update: it refreshes every configured package that is not pinned, or the packages whose identity matches source.
func (m *PackageManager) Update(source string) error {
	cwd, sm, progress := m.CWD, m.SettingsManager, m.progress
	// update reads configured sources, not installation paths; pinned entries must not run legacy-root lookups.
	pkgs := m.configuredSources()
	if source == "" {
		return updateConfiguredSources(cwd, sm, pkgs, progress)
	}
	// package-manager.ts:1062-1077 resolves the input identity before scanning settings; resolvePath throws for an invalid file: URL.
	identity, err := PackageSourceIdentityChecked(cwd, source)
	if err != nil {
		return err
	}
	var matched []ConfiguredPackage
	for _, pkg := range pkgs {
		stored, err := PackageSourceIdentityChecked(SettingsBaseDir(cwd, sm.AgentDir(), pkg.Scope == "project"), pkg.Source.Source)
		if err != nil {
			return err
		}
		if stored == identity {
			matched = append(matched, pkg)
		}
	}
	if len(matched) == 0 {
		return errors.New(noMatchingPackageMessage(source, pkgs))
	}
	return updateConfiguredSources(cwd, sm, matched, progress)
}

// CheckForAvailableUpdates queries npm registry and git remotes for updates to installed, unpinned packages with at most five joined workers. It returns a non-nil slice, including when offline or no updates are available.
// Mirrors upstream DefaultPackageManager.checkForAvailableUpdates (packages/coding-agent/src/core/package-manager.ts:1186-1253).
// User-package metadata uses managed storage (D79); trusted project packages use cwd.
func (m *PackageManager) CheckForAvailableUpdates() []PackageUpdate {
	cwd, sm := m.CWD, m.SettingsManager
	if IsOfflineModeEnabled() {
		return []PackageUpdate{}
	}
	pkgs := ConfiguredPackagesForResolution(cwd, sm.AgentDir(), sm)
	if len(pkgs) == 0 {
		return []PackageUpdate{}
	}

	type result struct {
		update *PackageUpdate
	}

	results := make([]result, len(pkgs))
	check := func(idx int) {
		p := pkgs[idx]

		source := p.Source.Source
		installed := p.InstalledPath
		if installed == "" {
			return
		}
		kind := DetectSourceKind(source)
		// Exact npm versions are fixed; tags and ranges remain eligible for metadata lookup.
		if kind == "npm" && IsPinnedNpm(source) {
			return
		}
		switch kind {
		case "npm":
			ref, err := ParseNpmInstallRef(source)
			if err != nil {
				return
			}
			if npmHasAvailableUpdate(cwd, sm, ref, installed, p.Scope == "project") {
				results[idx] = result{update: &PackageUpdate{
					Source:      source,
					DisplayName: ref.NPMName,
					Type:        "npm",
					Scope:       p.Scope,
				}}
			}
		case "git":
			ref, ok := ParseGitPackageSource(source)
			if !ok || ref.Pinned {
				return
			}
			if GitHasAvailableUpdate(installed) {
				results[idx] = result{update: &PackageUpdate{
					Source:      source,
					DisplayName: ref.Host + "/" + ref.Path,
					Type:        "git",
					Scope:       p.Scope,
				}}
			}
		}
	}
	// upstream: packages/coding-agent/src/core/package-manager.ts:runWithConcurrency
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(updateCheckConcurrency, len(pkgs)) {
		wg.Go(func() {
			for idx := range jobs {
				check(idx)
			}
		})
	}
	for idx := range pkgs {
		jobs <- idx
	}
	close(jobs)
	wg.Wait()

	updates := []PackageUpdate{}
	for _, r := range results {
		if r.update != nil {
			updates = append(updates, *r.update)
		}
	}
	return updates
}
