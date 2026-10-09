package packagemanager

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// ResolvedPaths are the resources a resolution found, one list per resource type, in precedence order, each with the PathMetadata of its source (package-manager.ts ResolvedPaths).
type ResolvedPaths struct {
	Extensions []codingagent.ResolvedResource
	Skills     []codingagent.ResolvedResource
	Prompts    []codingagent.ResolvedResource
	Themes     []codingagent.ResolvedResource
}

// MissingSourceAction is what [MissingSourceCallback] decides for a Package that is not installed (package-manager.ts MissingSourceAction).
type MissingSourceAction string

const (
	// MissingSourceInstall installs the Package.
	MissingSourceInstall MissingSourceAction = "install"
	// MissingSourceSkip leaves the Package out of the result.
	MissingSourceSkip MissingSourceAction = "skip"
	// MissingSourceError fails the resolution.
	MissingSourceError MissingSourceAction = "error"
)

// MissingSourceCallback is the onMissing argument of [PackageManager.Resolve].
type MissingSourceCallback func(source string) (MissingSourceAction, error)

// Resolve is upstream's resolve: it resolves the Packages, settings entries and auto-discovered locations into the resources they enable, per resource type (package-manager.ts:920-985). Each Package that is not installed, or whose installation does not satisfy its configured version, is installed unless onMissing decides otherwise; a nil onMissing installs it, as DefaultPackageManager does. A local Package that does not exist, and every missing Package in offline mode, contributes nothing. The result lists each type's Package resources, settings entries and auto-discovered resources as a first-wins accumulator keyed by path, ordered by resourcePrecedenceRank. A skill is keyed by its SKILL.md file when it has one.
func (m *PackageManager) Resolve(onMissing MissingSourceCallback) (ResolvedPaths, error) {
	cwd, agentDir, settings := m.CWD, m.agentDir(), m.SettingsManager
	if err := ValidateConfiguredPackageSources(cwd, agentDir, settings); err != nil {
		return ResolvedPaths{}, fmt.Errorf("resolve Packages: %w", err)
	}
	if err := m.installMissing(onMissing); err != nil {
		return ResolvedPaths{}, err
	}
	if err := ValidateConfiguredResourceEntries(cwd, agentDir, settings); err != nil {
		return ResolvedPaths{}, fmt.Errorf("resolve settings entries: %w", err)
	}
	items, _ := CollectResolvedPackageResourceItems(cwd, agentDir, settings, nil, false)
	var fromPackages ResolvedPaths
	for _, item := range items {
		resource := codingagent.ResolvedResource{Path: item.Path, Enabled: item.Enabled, Metadata: codingagent.PathMetadata{Source: item.Source, Scope: item.Scope, Origin: item.Origin, BaseDir: item.BaseDir, PackageRoot: item.PackageRoot}}
		switch packagecontent.Kind(item.ResourceType) {
		case packagecontent.Extensions:
			fromPackages.Extensions = append(fromPackages.Extensions, resource)
		case packagecontent.Skills:
			fromPackages.Skills = append(fromPackages.Skills, resource)
		case packagecontent.Prompts:
			fromPackages.Prompts = append(fromPackages.Prompts, resource)
		case packagecontent.Themes:
			fromPackages.Themes = append(fromPackages.Themes, resource)
		}
	}
	project := settings.IsProjectTrusted()
	return ResolvedPaths{
		Extensions: codingagent.OrderResolvedResources(fromPackages.Extensions, slices.Concat(codingagent.AmbientExtensionResources(cwd, agentDir, settings, project), m.builtinExtensionResources())),
		Skills:     codingagent.OrderResolvedResources(codingagent.SkillEntryResources(fromPackages.Skills), codingagent.SkillEntryResources(codingagent.AmbientSkillResources(cwd, agentDir, settings, project, true))),
		Prompts:    codingagent.OrderResolvedResources(fromPackages.Prompts, codingagent.AmbientPromptResources(cwd, agentDir, settings, project)),
		Themes:     codingagent.OrderResolvedResources(fromPackages.Themes, codingagent.AmbientThemeResources(cwd, agentDir, settings, project)),
	}, nil
}

// installMissing installs each configured npm or git Package that needs it, asking onMissing first when it is set (package-manager.ts:1282-1340 resolvePackageSources, installMissing).
func (m *PackageManager) installMissing(onMissing MissingSourceCallback) error {
	if IsOfflineModeEnabled() {
		return nil
	}
	for _, pkg := range ConfiguredPackagesForResolution(m.CWD, m.agentDir(), m.SettingsManager) {
		if DetectSourceKind(pkg.Source.Source) == "local" || !ConfiguredPackageNeedsInstall(pkg) {
			continue
		}
		if onMissing != nil {
			action, err := onMissing(pkg.Source.Source)
			if err != nil {
				return err
			}
			switch action {
			case MissingSourceSkip:
				continue
			case MissingSourceError:
				return errors.New("Missing source: " + pkg.Source.Source)
			}
		}
		if err := InstallPackageArtifacts(m.CWD, m.agentDir(), m.SettingsManager, pkg.Source, pkg.Scope == "project", nil); err != nil {
			return fmt.Errorf("install %s Package %q: %w", pkg.Scope, pkg.Source.Source, err)
		}
	}
	return nil
}

// ResolveExtensionSources is upstream's resolveExtensionSources: it resolves each source, an `-e` argument, into the extensions and other resources it exposes, in the scope "temporary", "project" or "user" that temporary and local select (package-manager.ts:991-1010). A `builtin:<name>` source is an enabled built-in extension. A local source that does not exist contributes nothing; an npm or git source that is not installed installs unless offline mode is on. A temporary source installs below the agent's temporary extension folder, and an unpinned cached git checkout refreshes first.
func (m *PackageManager) ResolveExtensionSources(sources []string, local, temporary bool) (ResolvedPaths, error) {
	scope := "user"
	if temporary {
		scope = "temporary"
	} else if local {
		scope = "project"
	}
	var resolved ResolvedPaths
	for _, source := range sources {
		if strings.HasPrefix(source, codingagent.BuiltinPathPrefix) {
			resolved.Extensions = append(resolved.Extensions, codingagent.ResolvedResource{Path: source, Enabled: true, Metadata: codingagent.PathMetadata{Source: "builtin", Scope: scope, Origin: "top-level"}})
		}
	}
	for _, source := range sources {
		if strings.HasPrefix(source, codingagent.BuiltinPathPrefix) {
			continue
		}
		root, err := m.extensionSourceRoot(source, scope, local)
		if err != nil {
			return ResolvedPaths{}, err
		}
		if root == "" {
			continue
		}
		items, err := CollectPackageResourceItems(root, ConfiguredPackage{Source: codingagent.PackageSource{Source: source}, Scope: scope, InstalledPath: root}, false)
		if err != nil {
			return ResolvedPaths{}, err
		}
		for _, item := range items {
			resource := codingagent.ResolvedResource{Path: item.Path, Enabled: item.Enabled, Metadata: codingagent.PathMetadata{Source: item.Source, Scope: item.Scope, Origin: item.Origin, BaseDir: item.BaseDir, PackageRoot: item.PackageRoot}}
			switch packagecontent.Kind(item.ResourceType) {
			case packagecontent.Extensions:
				resolved.Extensions = append(resolved.Extensions, resource)
			case packagecontent.Skills:
				resolved.Skills = append(resolved.Skills, resource)
			case packagecontent.Prompts:
				resolved.Prompts = append(resolved.Prompts, resource)
			case packagecontent.Themes:
				resolved.Themes = append(resolved.Themes, resource)
			}
		}
	}
	resolved.Skills = codingagent.SkillEntryResources(resolved.Skills)
	return resolved, nil
}

// extensionSourceRoot is the directory or file an `-e` source resolves to, installing an npm or git source that is missing; "" means the source contributes nothing.
func (m *PackageManager) extensionSourceRoot(source, scope string, local bool) (string, error) {
	cwd, agentDir := m.CWD, m.agentDir()
	if scope == "temporary" {
		return ResolveTemporarySource(cwd, agentDir, m.SettingsManager, source, m.progress)
	}
	root, err := SourceRootForResources(cwd, agentDir, m.SettingsManager, source, local)
	if err != nil {
		return "", err
	}
	if DetectSourceKind(source) == "local" {
		if _, statErr := os.Stat(root); statErr != nil {
			return "", nil
		}
		return root, nil
	}
	pkg := ConfiguredPackage{Source: codingagent.PackageSource{Source: source}, Scope: scope, InstalledPath: root}
	if _, statErr := os.Stat(root); statErr != nil {
		pkg.InstalledPath = ""
	}
	if ConfiguredPackageNeedsInstall(pkg) {
		if IsOfflineModeEnabled() {
			return "", nil
		}
		if err := InstallPackageArtifacts(cwd, agentDir, m.SettingsManager, pkg.Source, local, m.progress); err != nil {
			return "", err
		}
	}
	return root, nil
}

// builtinExtensionResources are the built-in extensions of the BuiltinExtensions option, enabled unless the user `extensions` setting excludes them and overridden by the project setting; they come after the auto-discovered resources (package-manager.ts:970-986).
func (m *PackageManager) builtinExtensionResources() []codingagent.ResolvedResource {
	items := ResolveBuiltinExtensions(m.SettingsManager, m.BuiltinExtensions)
	resources := make([]codingagent.ResolvedResource, 0, len(items))
	for _, item := range items {
		resources = append(resources, codingagent.ResolvedResource{Path: item.Path, Enabled: item.Enabled, Metadata: codingagent.PathMetadata{Source: item.Source, Scope: item.Scope, Origin: item.Origin}})
	}
	return resources
}
