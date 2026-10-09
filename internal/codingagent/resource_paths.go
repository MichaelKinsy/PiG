// Ports packages/coding-agent/src/core/package-manager.ts (addAutoDiscoveredResources, collectAncestorAgentsSkillDirs, getHomeDir) and the ambient resource ordering of packages/coding-agent/src/core/resource-loader.ts reload.
package codingagent

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
	"github.com/MichaelKinsy/PiG/internal/text"
)

// PackageManagerHomeDir prefers nonempty HOME on every platform. Otherwise it uses the platform home variable, including an explicit empty value, or the OS user database when that variable is absent.
func PackageManagerHomeDir() string {
	if home := os.Getenv("HOME"); home != "" {
		return home
	}
	key := "HOME"
	if runtime.GOOS == "windows" {
		key = "USERPROFILE"
	}
	if home, present := os.LookupEnv(key); present {
		return home
	}
	if current, err := user.Current(); err == nil {
		return current.HomeDir
	}
	return ""
}

// TopLevelResourcePaths lists a scope's settings entries, then its auto-discovered resources, as package-manager.ts resourcePrecedenceRank orders them.
func TopLevelResourcePaths(autoDir string, entries []string, kind packagecontent.Kind) []string {
	auto := packagecontent.DiscoverAutomatic(autoDir, kind)
	baseDir := filepath.Dir(autoDir)
	explicit := packagecontent.ResolveConfigured(entries, baseDir, kind)
	auto = packagecontent.FilterAutomatic(auto, entries, baseDir, kind)
	return append(explicit, auto...)
}

// DiscoverAncestorAgentsSkillDirs walks from startDir up to the git root (or filesystem root), collecting .agents/skills directories at each level (package-manager.ts collectAncestorAgentsSkillDirs).
func DiscoverAncestorAgentsSkillDirs(startDir string) []string {
	resolved, err := nodepath.Resolve(startDir)
	if err != nil {
		return nil
	}
	gitRoot := findGitRepoRoot(resolved)

	var dirs []string
	dir := resolved
	for {
		dirs = append(dirs, filepath.Join(dir, ".agents", "skills"))
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

// findGitRepoRoot walks up from startDir looking for a .git entry and returns its directory, or "" when there is none.
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

// PathMetadata is package-manager.ts PathMetadata: where a resolved resource came from. BaseDir and PackageRoot are the Package directory for an npm, git or local-directory Package resource; a single-file local source has its directory as BaseDir and no PackageRoot. Extension package warnings read the root through ExtensionPackageRoot.
type PathMetadata struct {
	Source  string
	Scope   string
	Origin  string
	BaseDir string
	// PackageRoot is the directory of the npm, git or local-directory Package that supplied the resource; it is empty for a top-level resource and for a local source that is a single file.
	PackageRoot string
}

// ResolvedResource is a resolved resource path with its PathMetadata (package-manager.ts ResolvedResource).
type ResolvedResource struct {
	Path string
	// Enabled is false for a resource that a filter or override disabled; its metadata still applies to the path.
	Enabled  bool
	Metadata PathMetadata
}

// SourceInfo is source-info.ts createSourceInfo.
func (m PathMetadata) SourceInfo(path string) PiSourceInfo {
	return PiSourceInfo{Path: path, Source: m.Source, Scope: m.Scope, Origin: m.Origin, BaseDir: m.BaseDir}
}

// ResourcePaths lists the paths of the enabled resources.
func ResourcePaths(resources []ResolvedResource) []string {
	paths := make([]string, 0, len(resources))
	for _, resource := range resources {
		if resource.Enabled {
			paths = append(paths, resource.Path)
		}
	}
	return paths
}

func resolvedResources(states []packagecontent.ResourceState, metadata PathMetadata) []ResolvedResource {
	resources := make([]ResolvedResource, len(states))
	for i, state := range states {
		resources[i] = ResolvedResource{Path: state.Path, Enabled: state.Enabled, Metadata: metadata}
	}
	return resources
}

// AmbientSkillResources lists the skills that settings and auto-discovery yield, in resourcePrecedenceRank order: project settings entries, project auto-discovery, ancestor .agents/skills, user settings entries, user auto-discovery, then ~/.agents/skills, each with the PathMetadata addAutoDiscoveredResources and resolveLocalEntries record and with the enabled state the settings' patterns give it; a disabled resource is listed, not dropped (package-manager.ts:188-195, 2329-2350, 2352-2495). The project half is skipped when project is false, as project trust does upstream.
func AmbientSkillResources(cwd, agentDir string, sm *SettingsManager, project, user bool) []ResolvedResource {
	inputs := make([]ResolvedResource, 0)
	if project {
		projectRoot := ProjectConfigDir(cwd)
		projectSettings := sm.GetProjectSettings().Skills
		inputs = append(inputs, resolvedResources(packagecontent.ResolveConfiguredStates(projectSettings, projectRoot, packagecontent.Skills), PathMetadata{Source: "local", Scope: "project", Origin: "top-level"})...)
		projectAuto := packagecontent.DiscoverAutomatic(filepath.Join(projectRoot, "skills"), packagecontent.Skills)
		inputs = append(inputs, resolvedResources(packagecontent.AutomaticStates(projectAuto, projectSettings, projectRoot, packagecontent.Skills), PathMetadata{Source: "auto", Scope: "project", Origin: "top-level", BaseDir: projectRoot})...)

		userAgentsSkills := filepath.Join(PackageManagerHomeDir(), ".agents", "skills")
		for _, dir := range DiscoverAncestorAgentsSkillDirs(cwd) {
			if samePath(dir, userAgentsSkills) {
				continue
			}
			inputs = append(inputs, resolvedResources(packagecontent.AutomaticStates(packagecontent.DiscoverAgentSkillDirs(dir), projectSettings, filepath.Dir(dir), packagecontent.Skills), PathMetadata{Source: "auto", Scope: "project", Origin: "top-level", BaseDir: filepath.Dir(dir)})...)
		}
	}
	if user {
		userSettings := sm.GetGlobalSettings().Skills
		inputs = append(inputs, resolvedResources(packagecontent.ResolveConfiguredStates(userSettings, agentDir, packagecontent.Skills), PathMetadata{Source: "local", Scope: "user", Origin: "top-level"})...)
		userAuto := packagecontent.DiscoverAutomatic(filepath.Join(agentDir, "skills"), packagecontent.Skills)
		inputs = append(inputs, resolvedResources(packagecontent.AutomaticStates(userAuto, userSettings, agentDir, packagecontent.Skills), PathMetadata{Source: "auto", Scope: "user", Origin: "top-level", BaseDir: agentDir})...)
		userAgentsSkills := filepath.Join(PackageManagerHomeDir(), ".agents", "skills")
		inputs = append(inputs, resolvedResources(packagecontent.AutomaticStates(packagecontent.DiscoverAgentSkillDirs(userAgentsSkills), userSettings, filepath.Dir(userAgentsSkills), packagecontent.Skills), PathMetadata{Source: "auto", Scope: "user", Origin: "top-level", BaseDir: filepath.Dir(userAgentsSkills)})...)
	}
	return inputs
}

// AmbientSkillPaths lists the enabled skills that settings and auto-discovery yield once DefaultPackageManager.resolve has keyed each by its SKILL.md and reduced the first-wins accumulator, so an explicit exclusion disables an auto-discovered skill at the same path; a skill that Pi keys by its SKILL.md is listed by its directory (package-manager.ts:365-400, 927-961, 2555-2565).
func AmbientSkillPaths(cwd, agentDir string, sm *SettingsManager, project, user bool) []string {
	ambient := AmbientSkillResources(cwd, agentDir, sm, project, user)
	named := SkillEntryResources(ambient)
	directories := make(map[string]string, len(ambient))
	for i, resource := range named {
		if resource.Path != ambient[i].Path {
			directories[resource.Path] = ambient[i].Path
		}
	}
	paths := ResourcePaths(OrderResolvedResources(nil, named))
	for i, path := range paths {
		if directory, ok := directories[path]; ok {
			paths[i] = directory
		}
	}
	return paths
}

// AmbientPromptResources lists the prompt templates that settings and auto-discovery yield: project entries and auto-discovery, then user entries and auto-discovery, each with the PathMetadata package-manager.ts records. The project half is skipped when project is false.
func AmbientPromptResources(cwd, agentDir string, sm *SettingsManager, project bool) []ResolvedResource {
	resources := make([]ResolvedResource, 0)
	if project {
		resources = append(resources, topLevelResources("project", ProjectConfigDir(cwd), sm.GetProjectSettings().Prompts, packagecontent.Prompts)...)
	}
	return append(resources, topLevelResources("user", agentDir, sm.GetGlobalSettings().Prompts, packagecontent.Prompts)...)
}

// AmbientThemeResources lists the themes that settings and auto-discovery yield: project entries and auto-discovery, then user entries and auto-discovery, each with the PathMetadata package-manager.ts records. The project half is skipped when project is false.
func AmbientThemeResources(cwd, agentDir string, sm *SettingsManager, project bool) []ResolvedResource {
	resources := make([]ResolvedResource, 0)
	if project {
		resources = append(resources, topLevelResources("project", ProjectConfigDir(cwd), sm.GetProjectSettings().Themes, packagecontent.Themes)...)
	}
	return append(resources, topLevelResources("user", agentDir, sm.GetGlobalSettings().Themes, packagecontent.Themes)...)
}

// AmbientExtensionResources lists the extensions that settings and auto-discovery yield: project entries and auto-discovery, then user entries and auto-discovery, each with the PathMetadata package-manager.ts records. The project half is skipped when project is false.
func AmbientExtensionResources(cwd, agentDir string, sm *SettingsManager, project bool) []ResolvedResource {
	resources := make([]ResolvedResource, 0)
	if project {
		resources = append(resources, topLevelResources("project", ProjectConfigDir(cwd), sm.GetProjectSettings().Extensions, packagecontent.Extensions)...)
	}
	return append(resources, topLevelResources("user", agentDir, sm.GetGlobalSettings().Extensions, packagecontent.Extensions)...)
}

// AmbientPromptPaths lists the paths of AmbientPromptResources.
func AmbientPromptPaths(cwd, agentDir string, sm *SettingsManager, project bool) []string {
	return ResourcePaths(AmbientPromptResources(cwd, agentDir, sm, project))
}

func topLevelResources(scope, baseDir string, entries []string, kind packagecontent.Kind) []ResolvedResource {
	auto := packagecontent.DiscoverAutomatic(filepath.Join(baseDir, string(kind)), kind)
	resources := resolvedResources(packagecontent.ResolveConfiguredStates(entries, baseDir, kind), PathMetadata{Source: "local", Scope: scope, Origin: "top-level"})
	return append(resources, resolvedResources(packagecontent.AutomaticStates(auto, entries, baseDir, kind), PathMetadata{Source: "auto", Scope: scope, Origin: "top-level", BaseDir: baseDir})...)
}

// DiscoverPromptFile returns the SYSTEM.md or APPEND_SYSTEM.md file a loader reads: the workspace file when project resources are trusted, otherwise the agent directory file, or "" when neither exists (resource-loader.ts discoverSystemPromptFile, discoverAppendSystemPromptFile).
func DiscoverPromptFile(cwd, agentDir, name string, projectTrusted bool) string {
	if projectTrusted {
		projectPath := filepath.Join(ProjectConfigDir(cwd), name)
		if _, err := os.Stat(projectPath); err == nil {
			return projectPath
		}
	}
	globalPath := filepath.Join(agentDir, name)
	if _, err := os.Stat(globalPath); err == nil {
		return globalPath
	}
	return ""
}

// ResolvePromptInput returns the BOM-stripped contents of the file input names, or input itself when no such file exists or it cannot be read (resource-loader.ts resolvePromptInput).
func ResolvePromptInput(input, description string) string {
	if input == "" {
		return ""
	}
	if _, err := os.Stat(input); err != nil {
		return input
	}
	data, err := os.ReadFile(input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not read %s file %s: %v\n", description, input, err)
		return input
	}
	return text.StripBom(string(data))
}

// SystemPromptSkills is the Skill shape Pi hands extensions in systemPromptOptions.skills (skills.ts Skill).
func SystemPromptSkills(skills []*SkillDef) []extension.SystemPromptSkill {
	out := make([]extension.SystemPromptSkill, 0, len(skills))
	for _, skill := range skills {
		out = append(out, extension.SystemPromptSkill{
			Name: skill.Name, Description: skill.Description, FilePath: skill.FilePath, BaseDir: skill.BaseDir,
			SourceInfo: skill.SourceInfo, DisableModelInvocation: skill.DisableModelInvocation,
		})
	}
	return out
}

// DefaultSourceInfoForPath is resource-loader.ts getDefaultSourceInfoForPath: a path under the agent or project resource directories is local to that scope, and any other path is temporary.
func DefaultSourceInfoForPath(cwd, agentDir, filePath string) PiSourceInfo {
	if strings.HasPrefix(filePath, "<") && strings.HasSuffix(filePath, ">") {
		source, _, _ := strings.Cut(filePath[1:len(filePath)-1], ":")
		if source == "" {
			source = "temporary"
		}
		return CreateSyntheticSourceInfo(filePath, SyntheticSourceInfoOptions{Source: source})
	}
	normalized, _ := filepath.Abs(filePath)
	for _, root := range resourceRoots(agentDir) {
		if isUnderPath(normalized, root) {
			return PiSourceInfo{Path: filePath, Source: "local", Scope: "user", Origin: "top-level", BaseDir: root}
		}
	}
	for _, root := range resourceRoots(ProjectConfigDir(cwd)) {
		if isUnderPath(normalized, root) {
			return PiSourceInfo{Path: filePath, Source: "local", Scope: "project", Origin: "top-level", BaseDir: root}
		}
	}
	baseDir := filepath.Dir(normalized)
	if info, err := os.Stat(normalized); err == nil && info.IsDir() {
		baseDir = normalized
	}
	return CreateSyntheticSourceInfo(filePath, SyntheticSourceInfoOptions{Source: "local", BaseDir: baseDir})
}

func resourceRoots(dir string) []string {
	return []string{filepath.Join(dir, "skills"), filepath.Join(dir, "prompts"), filepath.Join(dir, "themes"), filepath.Join(dir, "extensions")}
}

func isUnderPath(target, root string) bool {
	root, _ = filepath.Abs(root)
	return target == root || strings.HasPrefix(target, root+string(filepath.Separator))
}

// OrderResolvedResources is DefaultPackageManager.resolve's result for one resource type: the Packages' resources, then the settings entries (project, then user), then the auto-discovered resources fill a first-wins accumulator keyed by path, which keeps the earliest source's metadata and enabled state for a path several reach; the accumulator is stably sorted by resourcePrecedenceRank and reduced to the first resource of each canonical path (package-manager.ts:927-961, 2555-2565, 2576-2594). ambient is in AmbientSkillResources or AmbientPromptResources order.
func OrderResolvedResources(packages, ambient []ResolvedResource) []ResolvedResource {
	settingsEntries := make([]ResolvedResource, 0, len(ambient))
	autoDiscovered := make([]ResolvedResource, 0, len(ambient))
	for _, resource := range ambient {
		if resource.Metadata.Source == "local" {
			settingsEntries = append(settingsEntries, resource)
		} else {
			autoDiscovered = append(autoDiscovered, resource)
		}
	}
	accumulated := make([]ResolvedResource, 0, len(packages)+len(ambient))
	byPath := make(map[string]struct{}, len(packages)+len(ambient))
	for _, resource := range slices.Concat(packages, settingsEntries, autoDiscovered) {
		if _, present := byPath[resource.Path]; present || resource.Path == "" {
			continue
		}
		byPath[resource.Path] = struct{}{}
		accumulated = append(accumulated, resource)
	}
	slices.SortStableFunc(accumulated, func(a, b ResolvedResource) int {
		return ResourcePrecedenceRank(a.Metadata) - ResourcePrecedenceRank(b.Metadata)
	})
	ordered := make([]ResolvedResource, 0, len(accumulated))
	seen := make(map[string]struct{}, len(accumulated))
	for _, resource := range accumulated {
		canonical := CanonicalizePath(resource.Path)
		if _, duplicate := seen[canonical]; duplicate {
			continue
		}
		seen[canonical] = struct{}{}
		ordered = append(ordered, resource)
	}
	return ordered
}

// ResourcePrecedenceRank is package-manager.ts resourcePrecedenceRank: project settings entries, project auto-discovered, user settings entries, user auto-discovered, then Package resources.
func ResourcePrecedenceRank(metadata PathMetadata) int {
	if metadata.Origin == "package" {
		return 4
	}
	rank := 2
	if metadata.Scope == "project" {
		rank = 0
	}
	if metadata.Source != "local" {
		rank++
	}
	return rank
}

// SkillEntryResources names each skill directory resource by its SKILL.md file, as Pi's collectSkillEntries does for settings entries, auto-discovery and Package manifests (package-manager.ts:365-400, 645-653, 2317-2327, 2518-2535). PiG's resource lists name the directory; Pi's accumulator and metadataByPath key the skill by the file, so a settings entry naming the file and one naming the directory reach the same resource.
func SkillEntryResources(resources []ResolvedResource) []ResolvedResource {
	named := make([]ResolvedResource, len(resources))
	for i, resource := range resources {
		if isDirectoryPath(resource.Path) {
			if file := filepath.Join(resource.Path, "SKILL.md"); isRegularFilePath(file) {
				resource.Path = file
			}
		}
		named[i] = resource
	}
	return named
}

func isDirectoryPath(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func isRegularFilePath(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// samePath reports whether Node's path.resolve gives both paths one result.
func samePath(a, b string) bool {
	aa, err1 := nodepath.Resolve(a)
	bb, err2 := nodepath.Resolve(b)
	return err1 == nil && err2 == nil && aa == bb
}
