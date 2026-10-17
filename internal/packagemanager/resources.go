package packagemanager

import (
	"errors"
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
	// package-manager.ts:1368-1372 resolveLocalExtensionSource: a local source that is a file is one enabled extension whose base
	// directory is the file's directory; package filters, autoload: false included, and conventional directories do not apply to it.
	stat, statErr := os.Stat(root)
	if statErr == nil && stat.Mode().IsRegular() {
		return []tui.ResourceItem{{
			Path: root, Enabled: true, ResourceType: tui.ResourceExtensions, Scope: pkg.Scope, Origin: "package",
			Source: pkg.Source.Source, BaseDir: filepath.Dir(root),
		}}, nil
	}
	// package-manager.ts:1377-1384: a local directory with no filter that collectPackageResources finds no resources in is
	// itself one enabled extension whose base directory and package root are the directory.
	if statErr == nil && stat.IsDir() && !pkg.Source.Filtered() && DetectSourceKind(pkg.Source.Source) == "local" && !packagecontent.DescribesResources(root) {
		items, _ := ApplyPackageAutoloadStates(pkg, []tui.ResourceItem{{
			Path: root, Enabled: true, ResourceType: tui.ResourceExtensions, Scope: pkg.Scope, Origin: "package",
			Source: pkg.Source.Source, BaseDir: root, PackageRoot: root,
		}})
		return items, nil
	}
	filters := packageItemFilters(pkg)
	if !inspect {
		// Runtime discovery omits missing members and leaves extension failures to the extension loader. A pattern that throws drops its kind and the later ones, as resolveLocalExtensionSource's catch does for a local Package (package-manager.ts:1372-1389); an npm or git Package's throw fails resolution instead (ValidateConfiguredPackagePatterns).
		items, _ := collectPackageItems(root, pkg)
		return items, nil
	}
	inspectConfigured := packagecontent.InspectConfiguredWithResolver
	if pkg.Source.Filtered() {
		inspectConfigured = packagecontent.InspectConfiguredFiltered
	}
	resources, missing, err := inspectConfigured(root, filters, ConfiguredExtensionResolver(resolvers))
	if err != nil {
		return nil, err
	}
	items, _ := PackageResourceItemsFromInventory(root, pkg, resources, missing, filters)
	return items, nil
}

// collectPackageItems is collectPackageResources for the installed Package at root as pkg configures it: the items of the kinds collected before a pattern Pi throws for, and that *packagecontent.PackagePatternError. Pi collects a kind's manifest entries before it applies the kind's filter, and both before the next kind.
func collectPackageItems(root string, pkg ConfiguredPackage) ([]tui.ResourceItem, error) {
	resources, discoverErr := discoverConfiguredPackage(root, pkg)
	if _, thrown := errors.AsType[*packagecontent.PackagePatternError](discoverErr); !thrown {
		discoverErr = nil
	}
	// Discovery leaves the kinds from a throwing glob on empty, so a filter error comes from an earlier kind and goes first.
	items, filterErr := PackageResourceItemsFromInventory(root, pkg, resources, nil, packageItemFilters(pkg))
	if filterErr != nil {
		return items, filterErr
	}
	return items, discoverErr
}

// packageItemFilters are the filters PackageResourceItemsFromInventory applies for pkg: its configured filters, or none for an autoload-disabled Project delta, whose patterns ApplyPackageAutoloadStates applies.
func packageItemFilters(pkg ConfiguredPackage) map[packagecontent.Kind][]string {
	if IsProjectPackageDelta(pkg.Source) {
		return map[packagecontent.Kind][]string{packagecontent.Extensions: {}, packagecontent.Skills: {}, packagecontent.Prompts: {}, packagecontent.Themes: {}}
	}
	return ConfiguredPackageFilters(pkg.Source)
}

// discoverConfiguredPackage discovers the Package at root as Pi's collectPackageResources collects it for pkg (package-manager.ts:2214-2236): an autoload-disabled Package collects only the kinds its patterns name (applyPackageDeltaFilter, package-manager.ts:2316-2333), so a manifest glob of another kind never runs.
func discoverConfiguredPackage(root string, pkg ConfiguredPackage) (packagecontent.Resources, error) {
	var collects func(packagecontent.Kind) bool
	if IsProjectPackageDelta(pkg.Source) {
		filters := ConfiguredPackageFilters(pkg.Source)
		collects = func(kind packagecontent.Kind) bool { return len(filters[kind]) > 0 }
	}
	if pkg.Source.Filtered() {
		// A Package configured as a settings object reads a kind its manifest leaves undeclared from the conventional directory (collectDefaultResources, collectManifestFiles).
		return packagecontent.DiscoverConfigured(root, collects, packageItemFilters(pkg))
	}
	return packagecontent.DiscoverCollecting(root, collects)
}

// ValidateConfiguredPackagePatterns returns the first pattern error of an installed npm or git Package, in Pi's resolution order (Project Packages first): a manifest glob that Node's globSync throws for, or a manifest override or configured filter that minimatch cannot compile. Pi collects such a Package's resources outside any catch (resolvePackageSources, package-manager.ts:1315-1342), so the throw rejects resolve(); a local Package's throw is caught and drops only its own kinds. A Package that still needs installation contributes nothing here.
func ValidateConfiguredPackagePatterns(cwd, agentDir string, sm *codingagent.SettingsManager) error {
	for _, pkg := range ResolvedConfiguredPackageSources(cwd, agentDir, sm, true) {
		source := pkg.ResolvedSource
		if source == "" {
			source = pkg.Source.Source
		}
		if DetectSourceKind(source) == "local" || ConfiguredPackageNeedsInstall(pkg) {
			continue
		}
		// A delta's autoload patterns, not its item filters, are the patterns it compiles (ApplyPackageAutoloadStates).
		patterns := packageItemFilters(pkg)
		if IsProjectPackageDelta(pkg.Source) {
			patterns = ConfiguredPackageFilters(pkg.Source)
		}
		if stat, err := os.Stat(pkg.InstalledPath); err != nil || !stat.IsDir() || !packagecontent.PackagePatternsThrow(pkg.InstalledPath, patterns) {
			continue
		}
		if _, err := collectPackageItems(pkg.InstalledPath, pkg); err != nil {
			return err
		}
	}
	return nil
}

// PackageResourceItemsFromInventory lists a Package's resources with the enabled state its configured filters give each, kind by kind in Pi's order (collectPackageResources, package-manager.ts:2214-2236). A filter's patterns match each absolute path against the Package root, as applyPackageFilter does; a skill is matched by its SKILL.md. A pattern minimatch cannot compile stops at its kind: the items of the kinds before it come back with a *packagecontent.PackagePatternError.
func PackageResourceItemsFromInventory(root string, pkg ConfiguredPackage, resources packagecontent.Resources, missing []packagecontent.MissingMember, filters map[packagecontent.Kind][]string) ([]tui.ResourceItem, error) {
	out := make([]tui.ResourceItem, 0)
	var failed *packagecontent.PackagePatternError
	collected := make(map[packagecontent.Kind]bool, 4)
	for _, k := range []struct {
		kind         packagecontent.Kind
		resourceType tui.ResourceType
		paths        []string
	}{
		{packagecontent.Extensions, tui.ResourceExtensions, resources.ExtensionEntries},
		{packagecontent.Skills, tui.ResourceSkills, resources.SkillDirs},
		{packagecontent.Prompts, tui.ResourcePrompts, resources.PromptFiles},
		{packagecontent.Themes, tui.ResourceThemes, resources.ThemeFiles},
	} {
		files := k.paths
		if k.kind == packagecontent.Skills {
			files = make([]string, len(k.paths))
			for i, path := range k.paths {
				files[i] = packagecontent.SkillFile(path)
			}
		}
		enabled, err := packageFilterEnabled(files, filters[k.kind], root, k.kind)
		if err != nil {
			failed = &packagecontent.PackagePatternError{Kind: k.kind, Err: err}
			break
		}
		collected[k.kind] = true
		for i, path := range k.paths {
			out = append(out, tui.ResourceItem{Path: path, Enabled: enabled[files[i]], ResourceType: k.resourceType, Scope: pkg.Scope, Origin: "package", Source: pkg.Source.Source, BaseDir: root, PackageRoot: root})
		}
	}
	for _, member := range missing {
		if !collected[member.Kind] && (member.Kind == packagecontent.Extensions || member.Kind == packagecontent.Skills || member.Kind == packagecontent.Prompts || member.Kind == packagecontent.Themes) {
			continue
		}
		resourceType := tui.ResourceType(member.Kind)
		memberPath := member.Path
		if member.Kind == packagecontent.Skills {
			memberPath = filepath.Dir(memberPath)
		}
		// A missing member is no path Pi's filter sees, so a pattern it alone reaches never throws there.
		enabled, _ := packagecontent.MemberEnabled(root, member.Pattern, filters[member.Kind])
		out = append(out, tui.ResourceItem{
			Path: memberPath, Pattern: member.Pattern, Enabled: enabled,
			ResourceType: resourceType, Scope: pkg.Scope, Origin: "package",
			Source: pkg.Source.Source, BaseDir: root, PackageRoot: root, Health: "missing",
		})
	}
	out, deltaErr := ApplyPackageAutoloadStates(pkg, out)
	if failed != nil {
		return out, failed
	}
	return out, deltaErr
}

// packageFilterEnabled is applyPackageFilter's choice among a kind's files: no filter enables every file, an empty one none, and patterns those applyPatterns keeps (package-manager.ts:2280-2306).
func packageFilterEnabled(files, patterns []string, root string, kind packagecontent.Kind) (map[string]bool, error) {
	enabled := make(map[string]bool, len(files))
	if patterns == nil {
		for _, file := range files {
			enabled[file] = true
		}
		return enabled, nil
	}
	if len(patterns) == 0 {
		return enabled, nil
	}
	kept, err := packagecontent.ApplyPatterns(files, patterns, root, kind)
	if err != nil {
		return nil, err
	}
	for _, file := range kept {
		enabled[file] = true
	}
	return enabled, nil
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

// ValidateConfiguredResourceEntries resolves every plain extension, skill, prompt and theme settings entry against its scope's settings directory, applies each array's patterns, then checks the auto-discovered resources against the arrays' overrides, and returns the first error. Pi's resolve runs resolveLocalEntries after it has installed and collected the Packages, for each resource type in RESOURCE_TYPES order with the Project entries before the user ones, and its resolvePathFromBase throws on a malformed file URL (package-manager.ts:204, 933-959, 2340).
func ValidateConfiguredResourceEntries(cwd, agentDir string, sm *codingagent.SettingsManager) error {
	global, project := sm.GetGlobalSettings(), sm.GetProjectSettings()
	projectDir, globalDir := SettingsBaseDir(cwd, agentDir, true), SettingsBaseDir(cwd, agentDir, false)
	for _, entries := range []struct {
		project, global []string
		kind            packagecontent.Kind
	}{
		{project.Extensions, global.Extensions, packagecontent.Extensions},
		{project.Skills, global.Skills, packagecontent.Skills},
		{project.Prompts, global.Prompts, packagecontent.Prompts},
		{project.Themes, global.Themes, packagecontent.Themes},
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
			// resolveLocalEntries applies the array's patterns to the files its entries collect (package-manager.ts:2398-2411).
			if err := packagecontent.ValidateConfiguredEntries(scope.entries, scope.baseDir, entries.kind); err != nil {
				return err
			}
		}
	}
	return codingagent.ValidateAutoDiscoveredOverrides(cwd, agentDir, sm, sm.IsProjectTrusted())
}

// ResolvedConfiguredPackageSources keeps separate project deltas for resource accumulation; installation consumers share the inherited user package.
func ResolvedConfiguredPackageSources(cwd, agentDir string, sm *codingagent.SettingsManager, resourceEntries bool) []ConfiguredPackage {
	global := sm.GetGlobalSettings().Packages
	project := sm.GetProjectSettings().Packages
	globals := make([]ConfiguredPackage, 0, len(global))
	globalByIdentity := make(map[string]int, len(global))
	// repeatedGlobal marks a user entry for an identity an earlier user entry already has. dedupePackages (package-manager.ts:1742-1762) ignores it, except that every user entry is kept when the project's entry for the identity has autoload: false: the seen index stays on that project entry.
	repeatedGlobal := make(map[int]string)
	for _, pkg := range global {
		identity := PackageSourceIdentity(SettingsBaseDir(cwd, agentDir, false), pkg.Source)
		if _, exists := globalByIdentity[identity]; exists {
			repeatedGlobal[len(globals)] = identity
		} else {
			globalByIdentity[identity] = len(globals)
		}
		globals = append(globals, ConfiguredPackage{
			Source: pkg, Scope: "user", InstalledPath: InstalledPathForConfiguredSource(cwd, agentDir, sm, pkg.Source, false),
		})
	}
	projectDelta := make(map[string]bool)

	// dedupePackages (package-manager.ts:1742-1763) lets a later project entry replace an earlier one for the same Package, keeping the earlier position; a repeated user entry is ignored.
	projectIndex := make(map[string]int, len(project))
	deduped := make([]codingagent.PackageSource, 0, len(project))
	for _, pkg := range project {
		identity := PackageSourceIdentity(SettingsBaseDir(cwd, agentDir, true), pkg.Source)
		if index, exists := projectIndex[identity]; exists {
			deduped[index] = pkg
			continue
		}
		projectIndex[identity] = len(deduped)
		deduped = append(deduped, pkg)
	}
	project = deduped
	projects := make([]ConfiguredPackage, 0, len(project))
	for i := range project {
		pkg := project[i]
		identity := PackageSourceIdentity(SettingsBaseDir(cwd, agentDir, true), pkg.Source)
		globalIndex, inherited := globalByIdentity[identity]
		if inherited && IsProjectPackageDelta(pkg) {
			projectDelta[identity] = true
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
	for i, pkg := range globals {
		if identity, repeated := repeatedGlobal[i]; repeated && !projectDelta[identity] {
			continue
		}
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
