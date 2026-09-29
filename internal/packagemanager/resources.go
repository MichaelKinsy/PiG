package packagemanager

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
	"github.com/MichaelKinsy/PiG/tui"
)

func CollectPackageResourceItems(root string, pkg ConfiguredPackage, inspect bool, resolvers ...extsource.ResolveFunc) ([]tui.ResourceItem, error) {
	filters := ConfiguredPackageFilters(pkg.Source)
	if IsProjectPackageDelta(pkg.Source) {
		filters = map[packagecontent.Kind][]string{packagecontent.Extensions: {}, packagecontent.Skills: {}, packagecontent.Prompts: {}, packagecontent.Themes: {}}
	}
	var resources packagecontent.Resources
	var missing []packagecontent.MissingMember
	var err error
	if inspect {
		resources, missing, err = packagecontent.InspectConfiguredWithResolver(root, filters, ConfiguredExtensionResolver(resolvers))
	} else {
		// Runtime discovery omits missing members and leaves extension failures to the extension loader.
		resources, err = packagecontent.Discover(root)
	}
	if err != nil {
		return nil, err
	}
	return PackageResourceItemsFromInventory(root, pkg, resources, missing, filters), nil
}

func PackageResourceItemsFromInventory(root string, pkg ConfiguredPackage, resources packagecontent.Resources, missing []packagecontent.MissingMember, filters map[packagecontent.Kind][]string) []tui.ResourceItem {
	out := make([]tui.ResourceItem, 0)
	for _, path := range resources.ExtensionEntries {
		rel, _ := filepath.Rel(root, path)
		out = append(out, tui.ResourceItem{Path: path, Enabled: packagecontent.ResourceEnabled(rel, filters[packagecontent.Extensions]), ResourceType: tui.ResourceExtensions, Scope: pkg.Scope, Origin: "package", Source: pkg.Source.Source, BaseDir: root})
	}
	for _, path := range resources.SkillDirs {
		rel, _ := filepath.Rel(root, packagecontent.SkillFile(path))
		out = append(out, tui.ResourceItem{Path: path, Enabled: packagecontent.ResourceEnabled(rel, filters[packagecontent.Skills]), ResourceType: tui.ResourceSkills, Scope: pkg.Scope, Origin: "package", Source: pkg.Source.Source, BaseDir: root})
	}
	for _, path := range resources.PromptFiles {
		rel, _ := filepath.Rel(root, path)
		out = append(out, tui.ResourceItem{Path: path, Enabled: packagecontent.ResourceEnabled(rel, filters[packagecontent.Prompts]), ResourceType: tui.ResourcePrompts, Scope: pkg.Scope, Origin: "package", Source: pkg.Source.Source, BaseDir: root})
	}
	for _, path := range resources.ThemeFiles {
		rel, _ := filepath.Rel(root, path)
		out = append(out, tui.ResourceItem{Path: path, Enabled: packagecontent.ResourceEnabled(rel, filters[packagecontent.Themes]), ResourceType: tui.ResourceThemes, Scope: pkg.Scope, Origin: "package", Source: pkg.Source.Source, BaseDir: root})
	}
	for _, member := range missing {
		resourceType := tui.ResourceType(member.Kind)
		memberPath := member.Path
		if member.Kind == packagecontent.Skills {
			memberPath = filepath.Dir(memberPath)
		}
		out = append(out, tui.ResourceItem{
			Path: memberPath, Pattern: member.Pattern, Enabled: packagecontent.ResourceEnabled(member.Pattern, filters[member.Kind]),
			ResourceType: resourceType, Scope: pkg.Scope, Origin: "package",
			Source: pkg.Source.Source, BaseDir: root, Health: "missing",
		})
	}
	return ApplyPackageAutoloadStates(pkg, out)
}

func ConfiguredExtensionResolver(resolvers []extsource.ResolveFunc) extsource.ResolveFunc {
	if len(resolvers) > 0 && resolvers[0] != nil {
		return resolvers[0]
	}
	return extsource.Resolve
}

func ConfiguredPackageFilters(source codingagent.PackageSource) map[packagecontent.Kind][]string {
	return map[packagecontent.Kind][]string{
		packagecontent.Extensions: source.Extensions,
		packagecontent.Skills:     source.Skills,
		packagecontent.Prompts:    source.Prompts,
		packagecontent.Themes:     source.Themes,
	}
}

func PackageScopeEnabled(ambientScopes *[]string, scope string) bool {
	if scope == "project" {
		return AmbientSourceEnabled(ambientScopes, "workspace")
	}
	return AmbientSourceEnabled(ambientScopes, "user")
}

func AmbientSourceEnabled(ambientScopes *[]string, source string) bool {
	return ambientScopes == nil || slices.Contains(*ambientScopes, source)
}

func ConfiguredPackagesForResolution(cwd, agentDir string, sm *codingagent.SettingsManager) []ConfiguredPackage {
	return ResolvedConfiguredPackageSources(cwd, agentDir, sm, false)
}

// ValidateConfiguredPackageSources resolves every local Package source against its scope's settings directory, Project Packages first, and returns the first resolution error. Pi's DefaultPackageManager.resolve dedupes the Packages with getPackageIdentity, which calls resolvePathFromBase outside any catch, before it installs or collects any, so a malformed file URL throws instead of being skipped (package-manager.ts:912-928, 1687-1717; utils/paths.ts:95-106). The settings manager exposes Project settings only for a trusted Project, so an untrusted Project contributes none.
func ValidateConfiguredPackageSources(cwd, agentDir string, sm *codingagent.SettingsManager) error {
	for _, scope := range []struct {
		packages []codingagent.PackageSource
		local    bool
	}{{sm.GetProjectSettings().Packages, true}, {sm.GetGlobalSettings().Packages, false}} {
		baseDir := SettingsBaseDir(cwd, agentDir, scope.local)
		for _, pkg := range scope.packages {
			if DetectSourceKind(pkg.Source) != "local" {
				continue
			}
			if _, err := ResolveLocalPackageRoot(baseDir, pkg.Source); err != nil {
				return fmt.Errorf("resolve Package %q: %w", pkg.Source, err)
			}
		}
	}
	return nil
}

// ValidateConfiguredResourceEntries resolves every plain extension, skill, prompt and theme settings entry against its scope's settings directory and returns the first resolution error. Pi's resolve runs resolveLocalEntries after it has installed and collected the Packages, for each resource type in RESOURCE_TYPES order with the Project entries before the user ones, and its resolvePathFromBase throws on a malformed file URL (package-manager.ts:204, 933-959, 2340).
func ValidateConfiguredResourceEntries(cwd, agentDir string, sm *codingagent.SettingsManager) error {
	global, project := sm.GetGlobalSettings(), sm.GetProjectSettings()
	projectDir, globalDir := SettingsBaseDir(cwd, agentDir, true), SettingsBaseDir(cwd, agentDir, false)
	for _, entries := range []struct {
		project, global []string
	}{
		{project.Extensions, global.Extensions},
		{project.Skills, global.Skills},
		{project.Prompts, global.Prompts},
		{project.Themes, global.Themes},
	} {
		for _, scope := range []struct {
			entries []string
			baseDir string
		}{{entries.project, projectDir}, {entries.global, globalDir}} {
			plain, _ := packagecontent.SplitPatterns(scope.entries)
			for _, entry := range plain {
				if _, err := ResolveLocalPackageRoot(scope.baseDir, entry); err != nil {
					return fmt.Errorf("resolve resource path %q: %w", entry, err)
				}
			}
		}
	}
	return nil
}

// ResolvedConfiguredPackageSources keeps separate project deltas for resource accumulation; installation consumers share the inherited user package.
func ResolvedConfiguredPackageSources(cwd, agentDir string, sm *codingagent.SettingsManager, resourceEntries bool) []ConfiguredPackage {
	global := sm.GetGlobalSettings().Packages
	project := sm.GetProjectSettings().Packages
	globals := make([]ConfiguredPackage, 0, len(global))
	globalByIdentity := make(map[string]int, len(global))
	for _, pkg := range global {
		identity := PackageSourceIdentity(SettingsBaseDir(cwd, agentDir, false), pkg.Source)
		if _, exists := globalByIdentity[identity]; exists {
			continue
		}
		globalByIdentity[identity] = len(globals)
		globals = append(globals, ConfiguredPackage{
			Source: pkg, Scope: "user", InstalledPath: InstalledPathForConfiguredSource(cwd, agentDir, sm, pkg.Source, false),
		})
	}

	projects := make([]ConfiguredPackage, 0, len(project))
	seenProject := make(map[string]struct{}, len(project))
	for i := range project {
		pkg := project[i]
		identity := PackageSourceIdentity(SettingsBaseDir(cwd, agentDir, true), pkg.Source)
		if _, exists := seenProject[identity]; exists {
			continue
		}
		seenProject[identity] = struct{}{}
		globalIndex, inherited := globalByIdentity[identity]
		if inherited && IsProjectPackageDelta(pkg) {
			if resourceEntries {
				projects = append(projects, ConfiguredPackage{Source: pkg, Scope: "project", InstalledPath: globals[globalIndex].InstalledPath, ResolvedSource: globals[globalIndex].Source.Source})
			}
			continue
		}
		if inherited {
			delete(globalByIdentity, identity)
			globals[globalIndex].Source.Source = ""
		}
		projects = append(projects, ConfiguredPackage{
			Source: pkg, Scope: "project", InstalledPath: InstalledPathForConfiguredSource(cwd, agentDir, sm, pkg.Source, true),
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

func InstalledPathForConfiguredSource(cwd, agentDir string, sm *codingagent.SettingsManager, source string, local bool) string {
	if DetectSourceKind(source) != "local" {
		return InstalledPathForSource(cwd, agentDir, sm, source, local)
	}
	root, err := ResolveLocalPackageRoot(SettingsBaseDir(cwd, agentDir, local), source)
	if err != nil {
		return ""
	}
	if _, err := os.Stat(root); err != nil {
		return ""
	}
	return root
}

func IsProjectPackageDelta(pkg codingagent.PackageSource) bool {
	return pkg.Autoload != nil && !*pkg.Autoload
}

// IsWithin is resource-loader.ts isUnderPath on the resolved path: the root itself, or a path under it, where a root that already ends in a separator (a drive or filesystem root) is not given another.
func IsWithin(path, base string) bool {
	if path == "" || base == "" {
		return false
	}
	ap, err1 := nodepath.Resolve(path)
	ab, err2 := nodepath.Resolve(base)
	if err1 != nil || err2 != nil {
		return false
	}
	if ap == ab {
		return true
	}
	prefix := ab
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(ap, prefix)
}
