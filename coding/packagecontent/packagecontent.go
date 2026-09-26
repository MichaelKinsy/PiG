// Package packagecontent discovers resources exposed by Pi packages and
// cross-harness plugin manifests.
package packagecontent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
	"github.com/MichaelKinsy/PiG/coding/hookconfig"
	"github.com/MichaelKinsy/PiG/internal/codingagent/frontmatter"
	"github.com/MichaelKinsy/PiG/internal/ignorerules"
)

// Kind identifies a discoverable package resource class.
type Kind string

const (
	Extensions        Kind = "extensions"
	Skills            Kind = "skills"
	Prompts           Kind = "prompts"
	Themes            Kind = "themes"
	Agents            Kind = "agents"
	MCP               Kind = "mcp"
	Hooks             Kind = "hooks"
	AgentEnvironments Kind = "agent-environments"
)

// Resources is the resource inventory discovered from one materialized source
// root.
type Resources struct {
	PackageName       string
	PromptFiles       []string
	ThemeFiles        []string
	SkillDirs         []string
	ExtensionEntries  []string
	AgentFiles        []string
	MCPFiles          []string
	HookFiles         []string
	AgentEnvironments []string
}

// MissingMember is one safe authored Package declaration that currently
// resolves to no file. Configuration UIs use it to recover by disabling the
// normal Package filter without treating the declaration as a runtime resource.
type MissingMember struct {
	Kind    Kind
	Path    string
	Pattern string
	Enabled bool
}

type packageManifest struct {
	Name string       `json:"name"`
	PI   *piManifest  `json:"pi"`
	Pig  *pigManifest `json:"pig"`
}

type piManifest struct {
	Extensions *[]string `json:"extensions"`
	Skills     *[]string `json:"skills"`
	Prompts    *[]string `json:"prompts"`
	Themes     *[]string `json:"themes"`
}

type pigManifest struct {
	Hooks             *[]string `json:"hooks"`
	MCPServers        *[]string `json:"mcpServers"`
	AgentEnvironments *[]string `json:"agentEnvironments"`
}

type pluginManifest struct {
	Schema       string     `json:"$schema"`
	Name         string     `json:"name"`
	AgentPlugins bool       `json:"-"`
	Extensions   stringList `json:"extensions"`
	Skills       stringList `json:"skills"`
	Prompts      stringList `json:"prompts"`
	Themes       stringList `json:"themes"`
	Agents       stringList `json:"agents"`
	Commands     stringList `json:"commands"`
	MCPServers   stringList `json:"mcpServers"`
	Hooks        stringList `json:"hooks"`
}

type stringList []string

func (s *stringList) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		if one == "" {
			*s = nil
		} else {
			*s = []string{one}
		}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*s = many
	return nil
}

// Discover returns the package and plugin resources exposed by root. A Pi
// package.json declaration wins for Pi resource kinds; plugin metadata fills
// kinds the Pi manifest does not declare. Missing or malformed manifests fall
// back to conventional directory discovery, matching package installation.
func Discover(root string) (Resources, error) {
	manifest := readPackageManifest(root)
	plugin := readPluginManifest(root)
	return discover(root, manifest, plugin), nil
}

func discover(root string, manifest *packageManifest, plugin *pluginManifest) Resources {
	// Upstream loads a package that has a "pi" manifest from what the manifest
	// declares and never from conventional directories. PiG's own kinds follow
	// that rule: without a "pig" block such a package declares none, so a
	// hooks/ or mcp/ directory another agent's tooling ships is not read.
	pigEntries := func(kind Kind) *[]string {
		entries := coalesceEntries(manifestEntries(manifest, kind), pluginEntries(plugin, kind))
		if entries == nil && manifest != nil && manifest.PI != nil && manifest.Pig == nil {
			return &[]string{}
		}
		return entries
	}
	// A "pi" manifest's Pi kinds come only from its entries (upstream
	// collectPackageResources, addManifestEntries): a kind it does not
	// declare loads nothing unless plugin metadata declares it.
	piEntries := func(kind Kind) *[]string {
		entries := coalesceEntries(manifestEntries(manifest, kind), pluginEntries(plugin, kind))
		if entries == nil && manifest != nil && manifest.PI != nil {
			return &[]string{}
		}
		return entries
	}
	skillEntries := piEntries(Skills)
	if plugin != nil && plugin.AgentPlugins {
		skillEntries = manifestEntries(manifest, Skills)
		if skillEntries == nil && manifest != nil && manifest.PI != nil {
			skillEntries = &[]string{}
		}
	}
	resources := Resources{
		PromptFiles:       collectManifestResources(root, Prompts, piEntries(Prompts)),
		ThemeFiles:        collectManifestResources(root, Themes, piEntries(Themes)),
		SkillDirs:         collectManifestResources(root, Skills, skillEntries),
		ExtensionEntries:  collectExtensionResources(root, piEntries(Extensions)),
		AgentFiles:        collectManifestResources(root, Agents, pluginEntries(plugin, Agents)),
		MCPFiles:          collectMCPResources(root, pigEntries(MCP)),
		HookFiles:         collectManifestResources(root, Hooks, pigEntries(Hooks)),
		AgentEnvironments: collectAgentEnvironments(root, pigEntries(AgentEnvironments)),
	}
	if plugin != nil && plugin.AgentPlugins {
		resources.SkillDirs = validAgentPluginSkills(resources.SkillDirs)
	}
	if manifest != nil {
		resources.PackageName = manifest.Name
	}
	return resources
}

// ValidateMember validates one explicitly selected Package member.
func ValidateMember(root string, kind Kind, resourcePath string) error {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if err := requireWithinRoot(absoluteRoot, resourcePath); err != nil {
		return err
	}
	manifest, err := readStrictPackageManifest(absoluteRoot, false)
	if err != nil {
		return err
	}
	packageName := ""
	if manifest != nil {
		packageName = manifest.Name
	}
	if _, err := memberName(kind, resourcePath, packageName); err != nil {
		return err
	}
	if kind == AgentEnvironments {
		return validateDevContainerClosure(absoluteRoot, resourcePath)
	}
	return nil
}

// Validate checks a source inventory and its portable local file closure. A
// source without package.json remains valid for direct-resource installation.
func Validate(root string) (Resources, error) {
	absoluteRoot, err := validateRoot(root)
	if err != nil {
		return Resources{}, err
	}
	manifest, err := readStrictPackageManifest(absoluteRoot, false)
	if err != nil {
		return Resources{}, err
	}
	plugin, err := readStrictPluginManifest(absoluteRoot)
	if err != nil {
		return Resources{}, err
	}
	if err := validateDeclaredResources(absoluteRoot, manifest, plugin); err != nil {
		return Resources{}, err
	}
	return validateResources(absoluteRoot, discover(absoluteRoot, manifest, plugin))
}

// ValidateConfigured validates only resources enabled by installed Package
// filters. Authored Package validation remains strict through ValidatePackage.
func ValidateConfigured(root string, filters map[Kind][]string) (Resources, error) {
	absoluteRoot, manifest, plugin, err := readConfiguredRoot(root)
	if err != nil {
		return Resources{}, err
	}
	if err := validateDeclaredResourcesFiltered(absoluteRoot, manifest, plugin, filters); err != nil {
		return Resources{}, err
	}
	return validateResources(absoluteRoot, filterConfigured(absoluteRoot, discover(absoluteRoot, manifest, plugin), filters))
}

// ExtensionIssue is one enabled Package extension whose source does not
// resolve to a loadable extension.
type ExtensionIssue struct {
	Path string
	Err  error
}

// ValidateConfiguredForStartup applies ValidateConfigured's checks for session
// startup with two differences. An enabled declaration that matches nothing is
// skipped, as upstream's package manager skips a declared path that does not
// exist, and is returned so a caller can list it; the rest of the Package
// loads. An enabled extension whose source does not resolve is
// returned as an ExtensionIssue, every one of them, and removed from the
// resources instead of failing the Package; upstream loadExtensions likewise
// records each failing extension and continues with the rest. Every other
// failure still fails the Package. Each enabled extension resolves once.
func ValidateConfiguredForStartup(root string, filters map[Kind][]string) (Resources, []MissingMember, []ExtensionIssue, error) {
	return ValidateConfiguredForStartupWithResolver(root, filters, resolveExtensionSource)
}

// ValidateConfiguredForStartupWithResolver applies startup validation using
// resolve for extension source classification. A startup-owned resolver can
// therefore share the exact Definition or error with later loading.
func ValidateConfiguredForStartupWithResolver(root string, filters map[Kind][]string, resolve extsource.ResolveFunc) (Resources, []MissingMember, []ExtensionIssue, error) {
	absoluteRoot, manifest, plugin, err := readConfiguredRoot(root)
	if err != nil {
		return Resources{}, nil, nil, err
	}
	missing, err := configuredMissingMembers(absoluteRoot, manifest, plugin, filters)
	if err != nil {
		return Resources{}, nil, nil, err
	}
	missing = slices.DeleteFunc(missing, func(member MissingMember) bool { return !member.Enabled })
	if err := validateDeclaredEntries(absoluteRoot, manifest, plugin, filters, false); err != nil {
		return Resources{}, nil, nil, err
	}
	var issues []ExtensionIssue
	resources, err := validateResourcesIsolating(absoluteRoot, filterConfigured(absoluteRoot, discover(absoluteRoot, manifest, plugin), filters), &issues, resolve)
	if err != nil {
		return Resources{}, nil, nil, err
	}
	return resources, missing, issues, nil
}

// InspectConfigured returns the authored configuration inventory, including
// safe declarations whose files are missing. It validates the same lexical,
// absolute-path, glob, and symlink boundaries as startup. Missing declarations
// are data here so pig config can disable them; ValidateConfigured still rejects
// every enabled missing declaration.
func InspectConfigured(root string, filters map[Kind][]string) (Resources, []MissingMember, error) {
	return InspectConfiguredWithResolver(root, filters, resolveExtensionSource)
}

// InspectConfiguredWithResolver returns the configured inventory while using
// resolve for extension validation. It preserves InspectConfigured semantics
// while allowing startup to reuse its source-resolution snapshot.
func InspectConfiguredWithResolver(root string, filters map[Kind][]string, resolve extsource.ResolveFunc) (Resources, []MissingMember, error) {
	absoluteRoot, manifest, plugin, err := readConfiguredRoot(root)
	if err != nil {
		return Resources{}, nil, err
	}
	missing, err := configuredMissingMembers(absoluteRoot, manifest, plugin, filters)
	if err != nil {
		return Resources{}, nil, err
	}
	resources := discover(absoluteRoot, manifest, plugin)
	if _, err := validateResourcesIsolating(absoluteRoot, filterConfigured(absoluteRoot, resources, filters), nil, resolve); err != nil {
		return Resources{}, nil, err
	}
	return resources, missing, nil
}

func readConfiguredRoot(root string) (string, *packageManifest, *pluginManifest, error) {
	absoluteRoot, err := validateRoot(root)
	if err != nil {
		return "", nil, nil, err
	}
	manifest, err := readStrictPackageManifest(absoluteRoot, false)
	if err != nil {
		return "", nil, nil, err
	}
	plugin, err := readStrictPluginManifest(absoluteRoot)
	if err != nil {
		return "", nil, nil, err
	}
	return absoluteRoot, manifest, plugin, nil
}

// configuredMissingMembers validates every declared Pi resource entry against
// the Package root and returns the safe declarations that match no file.
func configuredMissingMembers(absoluteRoot string, manifest *packageManifest, plugin *pluginManifest, filters map[Kind][]string) ([]MissingMember, error) {
	var missing []MissingMember
	for _, kind := range []Kind{Extensions, Skills, Prompts, Themes} {
		entries := coalesceEntries(manifestEntries(manifest, kind), pluginEntries(plugin, kind))
		if entries == nil {
			continue
		}
		for _, declared := range *entries {
			if err := validateDeclaredEntry(absoluteRoot, kind, declared, false); err != nil {
				return nil, err
			}
			entry := declared
			if entry != "" && strings.ContainsRune("+-!", rune(entry[0])) {
				continue
			}
			// Upstream collects a declared path as it exists: a file is itself and
			// a directory is searched (collectFilesFromPaths in
			// core/package-manager.ts), so a skills directory that holds several
			// skills is present even though it has no SKILL.md of its own.
			candidate := filepath.Join(absoluteRoot, filepath.FromSlash(entry))
			pattern := entry
			if kind == Skills && path.Base(pattern) != "SKILL.md" {
				pattern = path.Join(pattern, "SKILL.md")
			}
			missingEntry := false
			if strings.ContainsAny(entry, "*?") {
				matches, globErr := filepath.Glob(candidate)
				if globErr != nil {
					return nil, fmt.Errorf("%s manifest entry %q: %w", kind, declared, globErr)
				}
				missingEntry = len(matches) == 0
			} else if _, statErr := os.Stat(candidate); os.IsNotExist(statErr) {
				missingEntry = true
			}
			if missingEntry {
				missing = append(missing, MissingMember{
					Kind: kind, Path: candidate, Pattern: filepath.ToSlash(pattern),
					Enabled: ResourceEnabled(filepath.ToSlash(pattern), filters[kind]),
				})
			}
		}
	}
	return missing, nil
}

// filterConfigured returns a copy of resources limited to the members enabled
// by the configured Package filters. A kind without a filter entry stays whole.
func filterConfigured(absoluteRoot string, resources Resources, filters map[Kind][]string) Resources {
	filter := func(paths []string, kind Kind) []string {
		patterns, configured := filters[kind]
		if !configured {
			return paths
		}
		return slices.DeleteFunc(slices.Clone(paths), func(resourcePath string) bool {
			relative, relErr := filepath.Rel(absoluteRoot, resourcePath)
			if relErr != nil {
				return false
			}
			if kind == Skills {
				relative = filepath.Join(relative, "SKILL.md")
			}
			return !ResourceEnabled(filepath.ToSlash(relative), patterns)
		})
	}
	resources.ExtensionEntries = filter(resources.ExtensionEntries, Extensions)
	resources.SkillDirs = filter(resources.SkillDirs, Skills)
	resources.PromptFiles = filter(resources.PromptFiles, Prompts)
	resources.ThemeFiles = filter(resources.ThemeFiles, Themes)
	return resources
}

// ValidatePackage checks a Package manifest, inventory, public identities, and
// portable local closure. Unlike Validate, package.json and its name are
// required.
func ValidatePackage(root string) (Resources, error) {
	absoluteRoot, err := validateRoot(root)
	if err != nil {
		return Resources{}, err
	}
	manifestPath := filepath.Join(absoluteRoot, "package.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Resources{}, fmt.Errorf("package manifest %s does not exist", manifestPath)
		}
		return Resources{}, err
	}
	if err := requireWithinRoot(absoluteRoot, manifestPath); err != nil {
		return Resources{}, err
	}
	return ValidatePackageManifest(absoluteRoot, data)
}

// ValidatePackageManifestSyntax checks the typed Package manifest boundary
// without resolving members. It is used before authoring edits so malformed
// existing fields are never silently discarded or rewritten.
func ValidatePackageManifestSyntax(data []byte) error {
	_, err := parsePackageManifest("package.json", data, true)
	return err
}

// ValidatePackageManifest validates prospective package.json data without
// writing it. Package authoring uses this before atomically replacing a
// manifest so a failed operation cannot leave invalid state behind.
func ValidatePackageManifest(root string, data []byte) (Resources, error) {
	absoluteRoot, err := validateRoot(root)
	if err != nil {
		return Resources{}, err
	}
	manifestPath := filepath.Join(absoluteRoot, "package.json")
	if _, err := os.Lstat(manifestPath); err == nil {
		if err := requireWithinRoot(absoluteRoot, manifestPath); err != nil {
			return Resources{}, err
		}
	} else if !os.IsNotExist(err) {
		return Resources{}, err
	}
	manifest, err := parsePackageManifest(manifestPath, data, true)
	if err != nil {
		return Resources{}, err
	}
	plugin, err := readStrictPluginManifest(absoluteRoot)
	if err != nil {
		return Resources{}, err
	}
	if err := validateDeclaredResources(absoluteRoot, manifest, plugin); err != nil {
		return Resources{}, err
	}
	return validateResources(absoluteRoot, discover(absoluteRoot, manifest, plugin))
}

func validateRoot(root string) (string, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absoluteRoot)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("package root %s is not a directory", absoluteRoot)
	}
	return absoluteRoot, nil
}

func readStrictPackageManifest(root string, required bool) (*packageManifest, error) {
	manifestPath := filepath.Join(root, "package.json")
	data, err := os.ReadFile(manifestPath)
	if os.IsNotExist(err) && !required {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := requireWithinRoot(root, manifestPath); err != nil {
		return nil, err
	}
	return parsePackageManifest(manifestPath, data, required)
}

func parsePackageManifest(manifestPath string, data []byte, requireName bool) (*packageManifest, error) {
	var manifest packageManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse %s: %w", manifestPath, err)
	}
	if requireName && strings.TrimSpace(manifest.Name) == "" {
		return nil, fmt.Errorf("package manifest %s requires a non-empty name", manifestPath)
	}
	return &manifest, nil
}

func readStrictPluginManifest(root string) (*pluginManifest, error) {
	for _, relative := range []string{"plugin.json", ".plugin/plugin.json", ".claude-plugin/plugin.json", ".cursor-plugin/plugin.json", ".pig-plugin/plugin.json"} {
		manifestPath := filepath.Join(root, filepath.FromSlash(relative))
		data, err := os.ReadFile(manifestPath)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := requireWithinRoot(root, manifestPath); err != nil {
			return nil, err
		}
		var packageBoundary struct {
			Piglets json.RawMessage `json:"piglets"`
		}
		if err := json.Unmarshal(data, &packageBoundary); err != nil {
			return nil, fmt.Errorf("parse %s: %w", manifestPath, err)
		}
		if packageBoundary.Piglets != nil {
			return nil, fmt.Errorf("plugin manifest %s declares piglets; Piglets are independent and cannot be Package members", manifestPath)
		}
		manifest, err := parsePluginManifestData(data)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", manifestPath, err)
		}
		if relative == "plugin.json" && isAgentPluginsSchema(manifest.Schema) {
			if manifest.Schema != agentPluginsV1Schema {
				return nil, fmt.Errorf("plugin manifest %s targets unsupported Agent Plugins schema %q", manifestPath, manifest.Schema)
			}
			if !agentPluginNamePattern.MatchString(manifest.Name) || strings.Contains(manifest.Name, "--") || strings.Contains(manifest.Name, "..") {
				return nil, fmt.Errorf("plugin manifest %s has invalid Agent Plugins name %q", manifestPath, manifest.Name)
			}
		}
	}
	return readPluginManifest(root), nil
}

const agentPluginsV1Schema = "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json"

var (
	windowsAbsolutePath    = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
	agentPluginNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,62}[a-z0-9])?$`)
	agentSkillNamePattern  = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
)

// isHostAbsolutePath reports whether raw is absolute or rooted on any host, so
// Package content that validates on one platform validates on every one:
// filepath.IsAbs alone treats /etc as relative on Windows and C:\ as relative
// elsewhere.
func isHostAbsolutePath(raw string) bool {
	return filepath.IsAbs(raw) || windowsAbsolutePath.MatchString(raw) || strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, `\`) || strings.HasPrefix(raw, "~/") || strings.HasPrefix(raw, `~\`)
}

func validateMCPDocument(document any) error {
	object, ok := document.(map[string]any)
	if !ok {
		return fmt.Errorf("definition must be a JSON object")
	}
	for _, field := range []string{"mcpServers", "servers"} {
		serversValue, exists := object[field]
		if !exists {
			continue
		}
		servers, ok := serversValue.(map[string]any)
		if !ok || len(servers) == 0 {
			return fmt.Errorf("%s must be a non-empty object", field)
		}
		for name, serverValue := range servers {
			if err := validateMCPServer(serverValue); err != nil {
				return fmt.Errorf("server %q: %w", name, err)
			}
		}
		return nil
	}
	return validateMCPServer(object)
}

func validateMCPServer(value any) error {
	server, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("configuration must be an object")
	}
	command, hasCommand := server["command"]
	url, hasURL := server["url"]
	if !hasCommand && !hasURL {
		return fmt.Errorf("configuration requires command or url")
	}
	if hasCommand {
		text, ok := command.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return fmt.Errorf("command must be a non-empty string")
		}
	}
	if hasURL {
		text, ok := url.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return fmt.Errorf("url must be a non-empty string")
		}
	}
	return nil
}

func validateDeclaredResources(root string, manifest *packageManifest, plugin *pluginManifest) error {
	return validateDeclaredResourcesFiltered(root, manifest, plugin, nil)
}

func validateDeclaredResourcesFiltered(root string, manifest *packageManifest, plugin *pluginManifest, filters map[Kind][]string) error {
	return validateDeclaredEntries(root, manifest, plugin, filters, true)
}

// validateDeclaredEntries checks every declared entry stays inside the
// Package and that every enabled entry of PiG's own kinds exists. With
// requireEnabled, an enabled entry of Pi's kinds (extensions, skills, prompts,
// themes) must exist too; without it one that does not exist is skipped, as
// upstream's package manager skips it.
func validateDeclaredEntries(root string, manifest *packageManifest, plugin *pluginManifest, filters map[Kind][]string, requireEnabled bool) error {
	for _, kind := range []Kind{Extensions, Skills, Prompts, Themes, Agents, MCP, Hooks, AgentEnvironments} {
		entries := manifestEntries(manifest, kind)
		if kind == Agents {
			entries = pluginEntries(plugin, kind)
		} else if kind != AgentEnvironments {
			entries = coalesceEntries(entries, pluginEntries(plugin, kind))
		}
		if entries == nil {
			continue
		}
		patterns, filtered := filters[kind]
		for _, declared := range *entries {
			resourcePath := declared
			if resourcePath != "" && strings.ContainsRune("+-!", rune(resourcePath[0])) {
				resourcePath = resourcePath[1:]
			}
			if kind == Skills && path.Base(resourcePath) != "SKILL.md" {
				resourcePath = path.Join(resourcePath, "SKILL.md")
			}
			piKind := kind == Extensions || kind == Skills || kind == Prompts || kind == Themes
			requirePresent := (requireEnabled || !piKind) && (!filtered || ResourceEnabled(resourcePath, patterns))
			if err := validateDeclaredEntry(root, kind, declared, requirePresent); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateDeclaredEntry(root string, kind Kind, declared string, requirePresent bool) error {
	entry := declared
	if entry == "" {
		return fmt.Errorf("%s manifest entry must not be empty", kind)
	}
	if entry[0] == '!' || entry[0] == '+' || entry[0] == '-' {
		entry = entry[1:]
	}
	if entry == "" || isHostAbsolutePath(entry) {
		return fmt.Errorf("%s manifest entry %q is not a package-relative path", kind, declared)
	}
	candidate := filepath.Clean(filepath.Join(root, filepath.FromSlash(entry)))
	if err := requireLexicallyWithinRoot(root, candidate); err != nil {
		return fmt.Errorf("%s manifest entry %q: %w", kind, declared, err)
	}
	if declared[0] == '!' || declared[0] == '+' || declared[0] == '-' {
		return nil
	}
	if strings.ContainsAny(entry, "*?") {
		matches, err := filepath.Glob(candidate)
		if err != nil {
			return fmt.Errorf("%s manifest entry %q: %w", kind, declared, err)
		}
		if len(matches) == 0 && requirePresent {
			return fmt.Errorf("%s manifest entry %q matched no resources", kind, declared)
		}
		for _, match := range matches {
			if err := requireWithinRoot(root, match); err != nil {
				return fmt.Errorf("%s manifest entry %q: %w", kind, declared, err)
			}
		}
		return nil
	}
	if _, err := os.Stat(candidate); err != nil {
		if !requirePresent && os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("%s manifest entry %q: %w", kind, declared, err)
	}
	if err := requireWithinRoot(root, candidate); err != nil {
		return fmt.Errorf("%s manifest entry %q: %w", kind, declared, err)
	}
	return nil
}

// resolveExtensionSource resolves one extension entry. Tests replace it to
// count resolutions.
var resolveExtensionSource = extsource.Resolve

func validateResources(root string, resources Resources) (Resources, error) {
	return validateResourcesIsolating(root, resources, nil, resolveExtensionSource)
}

// validateResourcesIsolating validates resources. With non-nil
// extensionIssues, an extension that fails source resolution is recorded there
// and removed from the result instead of failing validation.
func validateResourcesIsolating(root string, resources Resources, extensionIssues *[]ExtensionIssue, resolve extsource.ResolveFunc) (Resources, error) {
	if resolve == nil {
		resolve = resolveExtensionSource
	}
	for _, paths := range [][]string{
		resources.ExtensionEntries, resources.SkillDirs, resources.PromptFiles,
		resources.ThemeFiles, resources.AgentFiles, resources.MCPFiles,
		resources.HookFiles, resources.AgentEnvironments,
	} {
		for _, resourcePath := range paths {
			if err := requireWithinRoot(root, resourcePath); err != nil {
				return Resources{}, err
			}
		}
	}
	for _, kind := range []Kind{Extensions, Skills, Prompts, Themes, Agents, MCP, Hooks, AgentEnvironments} {
		if err := validateUniqueMemberNames(resources, kind); err != nil {
			return Resources{}, err
		}
	}
	var unresolved []string
	for _, extensionPath := range resources.ExtensionEntries {
		if _, err := resolve(extensionPath); err != nil {
			if extensionIssues == nil {
				return Resources{}, fmt.Errorf("validate extension resource %s: %w", extensionPath, err)
			}
			*extensionIssues = append(*extensionIssues, ExtensionIssue{Path: extensionPath, Err: err})
			unresolved = append(unresolved, extensionPath)
		}
	}
	if len(unresolved) > 0 {
		resources.ExtensionEntries = slices.DeleteFunc(slices.Clone(resources.ExtensionEntries), func(extensionPath string) bool {
			return slices.Contains(unresolved, extensionPath)
		})
	}
	for _, item := range []struct {
		kind  Kind
		paths []string
	}{
		{Themes, resources.ThemeFiles},
		{MCP, resources.MCPFiles},
		{Hooks, resources.HookFiles},
	} {
		for _, resourcePath := range item.paths {
			data, err := os.ReadFile(resourcePath)
			if err != nil {
				return Resources{}, err
			}
			var document any
			if err := json.Unmarshal(data, &document); err != nil {
				return Resources{}, fmt.Errorf("parse %s resource %s: %w", item.kind, resourcePath, err)
			}
			if item.kind == MCP {
				if err := validateMCPDocument(document); err != nil {
					return Resources{}, fmt.Errorf("validate mcp resource %s: %w", resourcePath, err)
				}
			}
			if item.kind == Hooks {
				if _, err := hookconfig.Parse(resourcePath, data); err != nil {
					return Resources{}, err
				}
			}
		}
	}
	for _, definition := range resources.AgentEnvironments {
		if err := validateDevContainerClosure(root, definition); err != nil {
			return Resources{}, err
		}
	}
	return resources, nil
}

func validateDevContainerClosure(root, definitionPath string) error {
	data, err := os.ReadFile(definitionPath)
	if err != nil {
		return err
	}
	var definition map[string]any
	normalized, err := normalizeJSONC(data)
	if err != nil {
		return fmt.Errorf("parse agent environment %s: %w", definitionPath, err)
	}
	if err := json.Unmarshal(normalized, &definition); err != nil {
		return fmt.Errorf("parse agent environment %s: %w", definitionPath, err)
	}
	baseDir := filepath.Dir(definitionPath)
	buildValue, hasBuild := definition["build"]
	imageValue, hasImage := definition["image"]
	composeValue, hasCompose := definition["dockerComposeFile"]
	forms := 0
	for _, present := range []bool{hasBuild, hasImage, hasCompose} {
		if present {
			forms++
		}
	}
	if forms == 0 {
		return fmt.Errorf("agent environment %s requires image, build, or dockerComposeFile", definitionPath)
	}
	if forms > 1 {
		return fmt.Errorf("agent environment %s must use exactly one of image, build, or dockerComposeFile", definitionPath)
	}
	if hasImage {
		image, ok := imageValue.(string)
		if !ok || strings.TrimSpace(image) == "" {
			return fmt.Errorf("agent environment %s image must be a non-empty string", definitionPath)
		}
		if strings.Contains(image, "://") || strings.ContainsAny(image, " \t\r\n") {
			return fmt.Errorf("agent environment %s image %q must be an OCI image reference, not a URL or whitespace-bearing value", definitionPath, image)
		}
	}
	if _, hasService := definition["service"]; hasService && !hasCompose {
		return fmt.Errorf("agent environment %s service requires dockerComposeFile", definitionPath)
	}
	if hasBuild {
		build, ok := buildValue.(map[string]any)
		if !ok {
			return fmt.Errorf("agent environment %s build must be an object", definitionPath)
		}
		contextDir := baseDir
		if contextValue, exists := build["context"]; exists {
			contextPath, ok := contextValue.(string)
			if !ok {
				return fmt.Errorf("agent environment %s build.context must be a string", definitionPath)
			}
			contextDir, err = validatePortableReference(root, baseDir, contextPath, true)
			if err != nil {
				return fmt.Errorf("agent environment %s build.context: %w", definitionPath, err)
			}
		}
		if dockerfileValue, exists := build["dockerfile"]; exists {
			dockerfile, ok := dockerfileValue.(string)
			if !ok {
				return fmt.Errorf("agent environment %s build.dockerfile must be a string", definitionPath)
			}
			if _, err := validatePortableReference(root, contextDir, dockerfile, true); err != nil {
				return fmt.Errorf("agent environment %s build.dockerfile: %w", definitionPath, err)
			}
		}
	}
	composeServices := map[string]struct{}{}
	if hasCompose {
		composeFiles, err := requiredStringValues(composeValue, "dockerComposeFile")
		if err != nil {
			return fmt.Errorf("agent environment %s: %w", definitionPath, err)
		}
		for _, composeFile := range composeFiles {
			composePath, err := validatePortableReference(root, baseDir, composeFile, true)
			if err != nil {
				return fmt.Errorf("agent environment %s dockerComposeFile: %w", definitionPath, err)
			}
			services, err := validateComposeClosure(root, composePath)
			if err != nil {
				return err
			}
			for service := range services {
				composeServices[service] = struct{}{}
			}
		}
		service, ok := definition["service"].(string)
		if !ok || strings.TrimSpace(service) == "" {
			return fmt.Errorf("agent environment %s requires service with dockerComposeFile", definitionPath)
		}
		if _, ok := composeServices[service]; !ok {
			return fmt.Errorf("agent environment %s selects missing Compose service %q", definitionPath, service)
		}
	}
	if mountsValue, exists := definition["mounts"]; exists {
		mounts, ok := mountsValue.([]any)
		if !ok {
			return fmt.Errorf("agent environment %s mounts must be an array", definitionPath)
		}
		for _, mount := range mounts {
			if err := validatePortableMount(root, baseDir, mount); err != nil {
				return fmt.Errorf("agent environment %s mounts: %w", definitionPath, err)
			}
		}
	}
	if workspaceMount, exists := definition["workspaceMount"]; exists {
		if err := validatePortableMount(root, baseDir, workspaceMount); err != nil {
			return fmt.Errorf("agent environment %s workspaceMount: %w", definitionPath, err)
		}
	}
	if features, exists := definition["features"]; exists {
		featureMap, ok := features.(map[string]any)
		if !ok {
			return fmt.Errorf("agent environment %s features must be an object", definitionPath)
		}
		if len(featureMap) > 0 {
			return fmt.Errorf("agent environment %s uses unsupported features", definitionPath)
		}
	}
	for _, field := range []string{"initializeCommand", "onCreateCommand", "updateContentCommand", "postCreateCommand", "postStartCommand", "postAttachCommand"} {
		if _, exists := definition[field]; exists {
			return fmt.Errorf("agent environment %s uses unsupported lifecycle command %s", definitionPath, field)
		}
	}
	return nil
}

func validateComposeClosure(root, composePath string) (map[string]struct{}, error) {
	data, err := os.ReadFile(composePath)
	if err != nil {
		return nil, err
	}
	var compose struct {
		Services map[string]map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &compose); err != nil {
		return nil, fmt.Errorf("parse Compose file %s: %w", composePath, err)
	}
	if len(compose.Services) == 0 {
		return nil, fmt.Errorf("Compose file %s defines no services", composePath)
	}
	services := make(map[string]struct{}, len(compose.Services))
	baseDir := filepath.Dir(composePath)
	for serviceName, service := range compose.Services {
		services[serviceName] = struct{}{}
		if buildValue, exists := service["build"]; exists {
			switch build := buildValue.(type) {
			case string:
				if _, err := validatePortableReference(root, baseDir, build, true); err != nil {
					return nil, fmt.Errorf("Compose service %s build: %w", serviceName, err)
				}
			case map[string]any:
				contextDir := baseDir
				if contextValue, exists := build["context"]; exists {
					contextPath, ok := contextValue.(string)
					if !ok {
						return nil, fmt.Errorf("Compose service %s build.context must be a string", serviceName)
					}
					contextDir, err = validatePortableReference(root, baseDir, contextPath, true)
					if err != nil {
						return nil, fmt.Errorf("Compose service %s build.context: %w", serviceName, err)
					}
				}
				if dockerfileValue, exists := build["dockerfile"]; exists {
					dockerfile, ok := dockerfileValue.(string)
					if !ok {
						return nil, fmt.Errorf("Compose service %s build.dockerfile must be a string", serviceName)
					}
					if _, err := validatePortableReference(root, contextDir, dockerfile, true); err != nil {
						return nil, fmt.Errorf("Compose service %s build.dockerfile: %w", serviceName, err)
					}
				}
			default:
				return nil, fmt.Errorf("Compose service %s build must be a string or object", serviceName)
			}
		}
		if envValue, exists := service["env_file"]; exists {
			envFiles, err := requiredStringValues(envValue, "env_file")
			if err != nil {
				return nil, fmt.Errorf("Compose service %s: %w", serviceName, err)
			}
			for _, envFile := range envFiles {
				if _, err := validatePortableReference(root, baseDir, envFile, true); err != nil {
					return nil, fmt.Errorf("Compose service %s env_file: %w", serviceName, err)
				}
			}
		}
		if volumesValue, exists := service["volumes"]; exists {
			volumes, ok := volumesValue.([]any)
			if !ok {
				return nil, fmt.Errorf("Compose service %s volumes must be an array", serviceName)
			}
			for _, volume := range volumes {
				if err := validateComposeVolume(root, baseDir, volume); err != nil {
					return nil, fmt.Errorf("Compose service %s volume: %w", serviceName, err)
				}
			}
		}
	}
	return services, nil
}

func validatePortableReference(root, baseDir, raw string, mustExist bool) (string, error) {
	if raw == "" || strings.Contains(raw, "${") || isHostAbsolutePath(raw) {
		return "", fmt.Errorf("reference %q is not a portable package-relative path", raw)
	}
	resolved := filepath.Clean(filepath.Join(baseDir, filepath.FromSlash(raw)))
	if err := requireLexicallyWithinRoot(root, resolved); err != nil {
		return "", err
	}
	if mustExist {
		if _, err := os.Stat(resolved); err != nil {
			return "", fmt.Errorf("referenced path %s: %w", resolved, err)
		}
	}
	if err := requireWithinRoot(root, resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

func validatePortableMount(root, baseDir string, value any) error {
	mountType := ""
	source := ""
	switch mount := value.(type) {
	case string:
		for field := range strings.SplitSeq(mount, ",") {
			key, value, ok := strings.Cut(strings.TrimSpace(field), "=")
			if !ok {
				continue
			}
			switch key {
			case "type":
				mountType = value
			case "source", "src":
				source = value
			}
		}
	case map[string]any:
		if value, exists := mount["type"]; exists {
			var ok bool
			mountType, ok = value.(string)
			if !ok {
				return fmt.Errorf("mount type must be a string")
			}
		}
		if value, exists := mount["source"]; exists {
			var ok bool
			source, ok = value.(string)
			if !ok {
				return fmt.Errorf("mount source must be a string")
			}
		}
	default:
		return fmt.Errorf("mount must be a string or object")
	}
	if mountType == "" && source != "" {
		return fmt.Errorf("mount with source %q requires an explicit type", source)
	}
	if mountType == "bind" && source == "" {
		return fmt.Errorf("bind mount requires source")
	}
	if mountType != "bind" {
		return nil
	}
	if strings.Contains(source, "${") {
		return fmt.Errorf("bind source %q requires unresolved host substitution", source)
	}
	if isHostAbsolutePath(source) {
		return fmt.Errorf("host source %q is not portable", source)
	}
	if _, err := validatePortableReference(root, baseDir, source, true); err != nil {
		return fmt.Errorf("bind source %q: %w", source, err)
	}
	return nil
}

func validateComposeVolume(root, baseDir string, value any) error {
	volume, ok := value.(string)
	if !ok {
		return validatePortableMount(root, baseDir, value)
	}
	if isHostAbsolutePath(volume) {
		return fmt.Errorf("host source in volume %q is not portable", volume)
	}
	source, _, ok := strings.Cut(volume, ":")
	if !ok || source == "" {
		return nil
	}
	if isHostAbsolutePath(source) {
		return fmt.Errorf("host source %q is not portable", source)
	}
	if strings.Contains(source, "${") {
		return fmt.Errorf("host source %q requires unresolved substitution", source)
	}
	if source == "." || strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") {
		_, err := validatePortableReference(root, baseDir, source, true)
		return err
	}
	return nil
}

func requiredStringValues(value any, field string) ([]string, error) {
	values := stringValues(value)
	if len(values) == 0 {
		return nil, fmt.Errorf("%s must be a non-empty string or string array", field)
	}
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil, fmt.Errorf("%s must not be empty", field)
		}
	case []any:
		if len(typed) != len(values) {
			return nil, fmt.Errorf("%s entries must be strings", field)
		}
	default:
		return nil, fmt.Errorf("%s must be a string or string array", field)
	}
	return values, nil
}

func stringValues(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				values = append(values, text)
			}
		}
		return values
	default:
		return nil
	}
}

func validateUniqueMemberNames(resources Resources, kind Kind) error {
	var paths []string
	switch kind {
	case Extensions:
		paths = resources.ExtensionEntries
	case Skills:
		paths = resources.SkillDirs
	case Prompts:
		paths = resources.PromptFiles
	case Themes:
		paths = resources.ThemeFiles
	case Agents:
		paths = resources.AgentFiles
	case MCP:
		paths = resources.MCPFiles
	case Hooks:
		paths = resources.HookFiles
	case AgentEnvironments:
		paths = resources.AgentEnvironments
	}
	seen := make(map[string]string, len(paths))
	for _, resourcePath := range paths {
		name, err := memberName(kind, resourcePath, resources.PackageName)
		if err != nil {
			return err
		}
		if previous, exists := seen[name]; exists {
			return fmt.Errorf("%s member %q is ambiguous: %s, %s", kind, name, previous, resourcePath)
		}
		seen[name] = resourcePath
	}
	return nil
}

func requireLexicallyWithinRoot(root, target string) error {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	absoluteTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(absoluteRoot, absoluteTarget)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("package resource %s escapes package root %s", target, root)
	}
	return nil
}

func requireWithinRoot(root, target string) error {
	if err := requireLexicallyWithinRoot(root, target); err != nil {
		return err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve package root %s: %w", root, err)
	}
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return fmt.Errorf("resolve package resource %s: %w", target, err)
	}
	if err := requireLexicallyWithinRoot(realRoot, realTarget); err != nil {
		return fmt.Errorf("package resource %s resolves outside package root %s: %w", target, root, err)
	}
	return nil
}

// source may itself be one resource or a package/plugin root containing it.
func FindSourceMember(root string, kind Kind, name string) (string, error) {
	resources, err := Discover(root)
	if err != nil {
		return "", err
	}
	direct := Collect([]string{root}, kind)
	switch kind {
	case Extensions:
		resources.ExtensionEntries = Deduplicate(append(resources.ExtensionEntries, direct...))
	case Skills:
		resources.SkillDirs = Deduplicate(append(resources.SkillDirs, direct...))
	case Prompts:
		resources.PromptFiles = Deduplicate(append(resources.PromptFiles, direct...))
	case Themes:
		resources.ThemeFiles = Deduplicate(append(resources.ThemeFiles, direct...))
	case Agents:
		resources.AgentFiles = Deduplicate(append(resources.AgentFiles, direct...))
	case MCP:
		resources.MCPFiles = Deduplicate(append(resources.MCPFiles, direct...))
	case Hooks:
		resources.HookFiles = Deduplicate(append(resources.HookFiles, direct...))
	case AgentEnvironments:
		resources.AgentEnvironments = Deduplicate(append(resources.AgentEnvironments, direct...))
	}
	return FindMember(resources, kind, name)
}

// FindMember selects one discovered resource by public kind/name identity.
// pig additive (D18): named Piglet Package origins require stable member
// identity across exact and conventional Package resources.
// Extension source paths and Skill frontmatter are authoritative.
func FindMember(resources Resources, kind Kind, name string) (string, error) {
	var paths []string
	switch kind {
	case Extensions:
		paths = resources.ExtensionEntries
	case Skills:
		paths = resources.SkillDirs
	case Prompts:
		paths = resources.PromptFiles
	case Themes:
		paths = resources.ThemeFiles
	case Agents:
		paths = resources.AgentFiles
	case MCP:
		paths = resources.MCPFiles
	case Hooks:
		paths = resources.HookFiles
	case AgentEnvironments:
		paths = resources.AgentEnvironments
	default:
		return "", fmt.Errorf("unsupported package member kind %q", kind)
	}
	matches := make([]string, 0, 1)
	for _, resourcePath := range paths {
		candidateName, err := memberName(kind, resourcePath, resources.PackageName)
		if err != nil {
			return "", err
		}
		if candidateName == name {
			matches = append(matches, resourcePath)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("%s member %q not found", kind, name)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("%s member %q is ambiguous: %s", kind, name, strings.Join(matches, ", "))
	}
}

// PublicName returns the stable kind/name identity for one Package resource.
// PackageName supplies the fallback identity for a root Dev Container.
func PublicName(kind Kind, resourcePath, packageName string) (string, error) {
	return memberName(kind, resourcePath, packageName)
}

func memberName(kind Kind, resourcePath, packageName string) (string, error) {
	switch kind {
	case Extensions:
		base := filepath.Base(filepath.Clean(resourcePath))
		extension := strings.ToLower(filepath.Ext(base))
		name := strings.TrimSuffix(base, filepath.Ext(base))
		if (extension == ".ts" || extension == ".js" || extension == ".mjs" || extension == ".cjs") && (name == "index" || name == "main" || name == "extension") {
			return filepath.Base(filepath.Dir(resourcePath)), nil
		}
		return name, nil
	case Skills:
		data, err := os.ReadFile(filepath.Join(resourcePath, "SKILL.md"))
		if err != nil {
			return "", fmt.Errorf("read skill %s: %w", resourcePath, err)
		}
		if declared := frontmatter.Parse(string(data)).String("name"); declared != "" {
			return declared, nil
		}
		return filepath.Base(resourcePath), nil
	case AgentEnvironments:
		return agentEnvironmentName(resourcePath, packageName)
	default:
		base := filepath.Base(resourcePath)
		return strings.TrimSuffix(base, filepath.Ext(base)), nil
	}
}

func agentEnvironmentName(environmentPath, packageName string) (string, error) {
	data, err := os.ReadFile(environmentPath)
	if err != nil {
		return "", fmt.Errorf("read agent environment %s: %w", environmentPath, err)
	}
	var definition struct {
		Name string `json:"name"`
	}
	normalized, err := normalizeJSONC(data)
	if err != nil {
		return "", fmt.Errorf("parse agent environment %s: %w", environmentPath, err)
	}
	if err := json.Unmarshal(normalized, &definition); err != nil {
		return "", fmt.Errorf("parse agent environment %s: %w", environmentPath, err)
	}
	if strings.TrimSpace(definition.Name) != "" {
		return definition.Name, nil
	}
	parent := filepath.Base(filepath.Dir(environmentPath))
	isRootDefinition := filepath.Base(environmentPath) == ".devcontainer.json" || parent == ".devcontainer" || parent == "."
	if !isRootDefinition {
		return parent, nil
	}
	if strings.TrimSpace(packageName) != "" {
		return packageName, nil
	}
	return "default", nil
}

func normalizeJSONC(data []byte) ([]byte, error) {
	withoutComments := make([]byte, 0, len(data))
	inString := false
	escaped := false
	for i := 0; i < len(data); i++ {
		current := data[i]
		if inString {
			withoutComments = append(withoutComments, current)
			switch {
			case escaped:
				escaped = false
			case current == '\\':
				escaped = true
			case current == '"':
				inString = false
			}
			continue
		}
		if current == '"' {
			inString = true
			withoutComments = append(withoutComments, current)
			continue
		}
		if current == '/' && i+1 < len(data) && data[i+1] == '/' {
			for i < len(data) && data[i] != '\n' {
				i++
			}
			if i < len(data) {
				withoutComments = append(withoutComments, '\n')
			}
			continue
		}
		if current == '/' && i+1 < len(data) && data[i+1] == '*' {
			i += 2
			closed := false
			for i+1 < len(data) {
				if data[i] == '*' && data[i+1] == '/' {
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated block comment")
			}
			i++
			continue
		}
		withoutComments = append(withoutComments, current)
	}
	if inString {
		return nil, fmt.Errorf("unterminated string")
	}

	normalized := make([]byte, 0, len(withoutComments))
	inString = false
	escaped = false
	for i := 0; i < len(withoutComments); i++ {
		current := withoutComments[i]
		if inString {
			normalized = append(normalized, current)
			switch {
			case escaped:
				escaped = false
			case current == '\\':
				escaped = true
			case current == '"':
				inString = false
			}
			continue
		}
		if current == '"' {
			inString = true
			normalized = append(normalized, current)
			continue
		}
		if current == ',' {
			next := i + 1
			for next < len(withoutComments) && (withoutComments[next] == ' ' || withoutComments[next] == '\t' || withoutComments[next] == '\r' || withoutComments[next] == '\n') {
				next++
			}
			if next < len(withoutComments) && (withoutComments[next] == '}' || withoutComments[next] == ']') {
				continue
			}
		}
		normalized = append(normalized, current)
	}
	return normalized, nil
}

// Collect expands files or conventional resource directories for kind.
func Collect(paths []string, kind Kind) []string {
	files := make([]string, 0)
	for _, resourcePath := range paths {
		if resourcePath == "" {
			continue
		}
		info, err := os.Stat(resourcePath)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			files = append(files, resourcePath)
			continue
		}
		switch kind {
		case Skills:
			if _, err := os.Stat(filepath.Join(resourcePath, "SKILL.md")); err == nil {
				files = append(files, resourcePath)
			} else {
				files = append(files, DiscoverSkillDirs(resourcePath)...)
			}
		case Extensions:
			files = append(files, discoverExtensionEntries(resourcePath)...)
		case Prompts:
			files = append(files, walkFilesWithSuffix(resourcePath, ".md")...)
		case Agents:
			files = append(files, walkAgentFiles(resourcePath)...)
		case MCP, Themes, Hooks:
			files = append(files, walkFilesWithSuffix(resourcePath, ".json")...)
		case AgentEnvironments:
			files = append(files, discoverAgentEnvironmentPaths(resourcePath)...)
		}
	}
	return Deduplicate(files)
}

// DiscoverAutomatic returns resources in one conventional resource directory.
func DiscoverAutomatic(dir string, kind Kind) []string {
	switch kind {
	case Prompts:
		return discoverFlatFiles(dir, ".md")
	case Themes:
		return discoverFlatFiles(dir, ".json")
	case Skills:
		return DiscoverSkillDirs(dir)
	case Extensions:
		return discoverExtensionEntries(dir)
	case AgentEnvironments:
		return discoverAgentEnvironmentPaths(dir)
	default:
		return nil
	}
}

// ResolveConfigured expands configured paths and applies their include/exclude
// patterns relative to baseDir.
func ResolveConfigured(entries []string, baseDir string, kind Kind) []string {
	plain, patterns := SplitPatterns(entries)
	resolved := make([]string, 0, len(plain))
	for _, entry := range plain {
		if filepath.IsAbs(entry) {
			resolved = append(resolved, entry)
		} else {
			resolved = append(resolved, filepath.Clean(filepath.Join(baseDir, entry)))
		}
	}
	return ApplyPatterns(Collect(resolved, kind), patterns, baseDir, kind)
}

// FilterAutomatic applies configured overrides to automatically discovered paths.
func FilterAutomatic(paths, overrides []string, baseDir string, kind Kind) []string {
	filtered := make([]string, 0, len(paths))
	for _, resourcePath := range paths {
		if EnabledByOverrides(resourcePath, overrides, baseDir, kind) {
			filtered = append(filtered, resourcePath)
		}
	}
	return filtered
}

// SplitPatterns separates literal entries from glob and override entries.
func SplitPatterns(entries []string) (plain, patterns []string) {
	for _, entry := range entries {
		if entry == "" {
			continue
		}
		if strings.HasPrefix(entry, "!") || strings.HasPrefix(entry, "+") || strings.HasPrefix(entry, "-") || strings.Contains(entry, "*") || strings.Contains(entry, "?") {
			patterns = append(patterns, entry)
		} else {
			plain = append(plain, entry)
		}
	}
	return plain, patterns
}

// ApplyPatterns applies allowlist, exclude, force-include, and force-exclude
// patterns without changing input order.
func ApplyPatterns(allPaths, patterns []string, baseDir string, kind Kind) []string {
	includes := make([]string, 0)
	excludes := make([]string, 0)
	forceIncludes := make([]string, 0)
	forceExcludes := make([]string, 0)
	for _, pattern := range patterns {
		switch {
		case strings.HasPrefix(pattern, "+"):
			forceIncludes = append(forceIncludes, pattern[1:])
		case strings.HasPrefix(pattern, "-"):
			forceExcludes = append(forceExcludes, pattern[1:])
		case strings.HasPrefix(pattern, "!"):
			excludes = append(excludes, pattern[1:])
		default:
			includes = append(includes, pattern)
		}
	}
	result := make([]string, 0, len(allPaths))
	if len(includes) == 0 {
		result = append(result, allPaths...)
	} else {
		for _, resourcePath := range allPaths {
			if matchesAnyPattern(resourcePath, includes, baseDir, kind) {
				result = append(result, resourcePath)
			}
		}
	}
	if len(excludes) > 0 {
		result = slices.DeleteFunc(result, func(resourcePath string) bool {
			return matchesAnyPattern(resourcePath, excludes, baseDir, kind)
		})
	}
	if len(forceIncludes) > 0 {
		for _, resourcePath := range allPaths {
			if !slices.Contains(result, resourcePath) && matchesAnyExactPattern(resourcePath, forceIncludes, baseDir, kind) {
				result = append(result, resourcePath)
			}
		}
	}
	if len(forceExcludes) > 0 {
		result = slices.DeleteFunc(result, func(resourcePath string) bool {
			return matchesAnyExactPattern(resourcePath, forceExcludes, baseDir, kind)
		})
	}
	return result
}

// EnabledByOverrides reports whether one automatically discovered path remains
// active after configured overrides.
func EnabledByOverrides(resourcePath string, patterns []string, baseDir string, kind Kind) bool {
	var excludes, forceIncludes, forceExcludes []string
	for _, pattern := range patterns {
		switch {
		case strings.HasPrefix(pattern, "!"):
			excludes = append(excludes, pattern[1:])
		case strings.HasPrefix(pattern, "+"):
			forceIncludes = append(forceIncludes, pattern[1:])
		case strings.HasPrefix(pattern, "-"):
			forceExcludes = append(forceExcludes, pattern[1:])
		}
	}
	enabled := len(excludes) == 0 || !matchesAnyPattern(resourcePath, excludes, baseDir, kind)
	if len(forceIncludes) > 0 && matchesAnyExactPattern(resourcePath, forceIncludes, baseDir, kind) {
		enabled = true
	}
	if len(forceExcludes) > 0 && matchesAnyExactPattern(resourcePath, forceExcludes, baseDir, kind) {
		enabled = false
	}
	return enabled
}

// ApplyConfiguredDelta resolves a project Package override against an inherited
// Package filter. Project overrides contain only exact +/- member paths; they do
// not autoload a second Package or alter unrelated inherited members.
func ApplyConfiguredDelta(kind Kind, relativePaths, basePatterns, deltaPatterns []string) ([]string, error) {
	states := make(map[string]bool, len(relativePaths))
	for _, relativePath := range relativePaths {
		relativePath = filepath.ToSlash(relativePath)
		states[relativePath] = ResourceEnabled(relativePath, basePatterns)
	}
	for _, patternValue := range deltaPatterns {
		if len(patternValue) < 2 || patternValue[0] != '+' && patternValue[0] != '-' {
			return nil, fmt.Errorf("%s project Package delta %q must be an exact + or - member path", kind, patternValue)
		}
		target := filepath.ToSlash(patternValue[1:])
		if target == "" || target == "." || isHostAbsolutePath(target) || strings.ContainsAny(target, "*?[") {
			return nil, fmt.Errorf("%s project Package delta %q must be an exact package-relative member path", kind, patternValue)
		}
		clean := path.Clean(target)
		if clean == ".." || strings.HasPrefix(clean, "../") {
			return nil, fmt.Errorf("%s project Package delta %q escapes package root", kind, patternValue)
		}
		if _, exists := states[target]; exists {
			states[target] = patternValue[0] == '+'
		}
	}
	enabled := make([]string, 0, len(states))
	for relativePath, isEnabled := range states {
		if isEnabled {
			enabled = append(enabled, relativePath)
		}
	}
	slices.Sort(enabled)
	return enabled, nil
}

// ResourceEnabled applies one PackageSource resource filter to a relative path.
func ResourceEnabled(relativePath string, patterns []string) bool {
	relativePath = filepath.ToSlash(relativePath)
	if patterns == nil {
		return true
	}
	if len(patterns) == 0 {
		return false
	}
	hasAllowlist := false
	for _, pattern := range patterns {
		if pattern != "" && pattern[0] != '-' && pattern[0] != '+' && pattern[0] != '!' {
			hasAllowlist = true
			break
		}
	}
	enabled := !hasAllowlist
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		negated := false
		switch pattern[0] {
		case '-':
			negated = true
			pattern = pattern[1:]
		case '+', '!':
			pattern = pattern[1:]
		}
		pattern = filepath.ToSlash(pattern)
		if matched, _ := filepath.Match(pattern, relativePath); matched || pattern == relativePath {
			enabled = !negated
		}
	}
	return enabled
}

// DiscoverSkillDirs recursively discovers skill roots and root-level Markdown
// skills. A directory containing SKILL.md is one skill and is not traversed
// further. Symlinked files and directories are followed, while hidden entries,
// node_modules, and paths excluded by .gitignore, .ignore, or .fdignore are
// skipped.
func DiscoverSkillDirs(dir string) []string {
	return discoverSkillDirs(dir, false)
}

// DiscoverAgentSkillDirs applies the cross-agent `.agents/skills` convention:
// root Markdown files are documentation, while nested Markdown files are
// standalone skills.
func DiscoverAgentSkillDirs(dir string) []string {
	return discoverSkillDirs(dir, true)
}

func discoverSkillDirs(dir string, agentsMode bool) []string {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil
	}
	var paths []string
	seenDirs := make(map[string]struct{})
	var walk func(string, []ignorerules.Rule)
	walk = func(current string, rules []ignorerules.Rule) {
		canonical, err := filepath.EvalSymlinks(current)
		if err != nil {
			return
		}
		if _, seen := seenDirs[canonical]; seen {
			return
		}
		seenDirs[canonical] = struct{}{}
		rules = ignorerules.Append(rules, current, root)
		entries, err := os.ReadDir(current)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if entry.Name() != "SKILL.md" {
				continue
			}
			fullPath := filepath.Join(current, entry.Name())
			info, err := os.Stat(fullPath)
			if err == nil && info.Mode().IsRegular() && !ignorerules.Ignored(fullPath, false, root, rules) {
				paths = append(paths, current)
				return
			}
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" {
				continue
			}
			fullPath := filepath.Join(current, entry.Name())
			info, err := os.Stat(fullPath)
			if err != nil {
				continue
			}
			if ignorerules.Ignored(fullPath, info.IsDir(), root, rules) {
				continue
			}
			switch {
			case info.IsDir():
				walk(fullPath, rules)
			case info.Mode().IsRegular() && strings.HasSuffix(entry.Name(), ".md") && ((agentsMode && current != root) || (!agentsMode && current == root)):
				paths = append(paths, fullPath)
			}
		}
	}
	walk(root, nil)
	return paths
}

func validAgentPluginSkills(paths []string) []string {
	valid := make([]string, 0, len(paths))
	for _, skillDir := range paths {
		data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
		if err != nil {
			continue
		}
		metadata := frontmatter.Parse(string(data))
		name := metadata.String("name")
		description := metadata.String("description")
		if !agentSkillNamePattern.MatchString(name) || len(description) == 0 || len(description) > 1024 {
			continue
		}
		valid = append(valid, skillDir)
	}
	return valid
}

// Deduplicate removes empty and repeated strings without changing order.
func Deduplicate(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// HasPiManifest reports whether root's package.json has a "pi" object, which
// makes upstream resolve root as a Package (readPiManifest).
func HasPiManifest(root string) bool {
	manifest := readPackageManifest(root)
	return manifest != nil && manifest.PI != nil
}

func readPackageManifest(root string) *packageManifest {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil
	}
	// Like upstream readPiManifest, strip a BOM and read "pi" whatever the
	// other package.json fields hold.
	raw := decodeJSONObject(bytes.TrimPrefix(data, utf8BOM))
	if raw == nil {
		return nil
	}
	manifest := &packageManifest{}
	_ = json.Unmarshal(raw["name"], &manifest.Name)
	if pi := decodeJSONObject(raw["pi"]); pi != nil {
		manifest.PI = &piManifest{
			Extensions: decodeStringSlice(pi["extensions"]),
			Skills:     decodeStringSlice(pi["skills"]),
			Prompts:    decodeStringSlice(pi["prompts"]),
			Themes:     decodeStringSlice(pi["themes"]),
		}
	}
	if pig := decodeJSONObject(raw["pig"]); pig != nil {
		manifest.Pig = &pigManifest{
			Hooks:             decodeStringSlice(pig["hooks"]),
			MCPServers:        decodeStringSlice(pig["mcpServers"]),
			AgentEnvironments: decodeStringSlice(pig["agentEnvironments"]),
		}
	}
	return manifest
}

// decodeStringSlice returns nil for an absent field and an empty declaration
// for a present field that is not an array of strings, so a malformed entry
// suppresses conventional discovery the way upstream's declared manifest does.
func decodeStringSlice(raw json.RawMessage) *[]string {
	if len(raw) == 0 {
		return nil
	}
	values, ok := decodeJSONStringArray(raw)
	if !ok {
		values = []string{}
	}
	return &values
}

func readPluginManifest(root string) *pluginManifest {
	rootManifest := readPluginManifestFile(filepath.Join(root, "plugin.json"))
	var merged *pluginManifest
	if rootManifest != nil {
		copy := *rootManifest
		merged = &copy
	}
	if rootManifest == nil || rootManifest.AgentPlugins {
		for _, relativePath := range []string{".plugin/plugin.json", ".claude-plugin/plugin.json", ".cursor-plugin/plugin.json"} {
			if parsed := readPluginManifestFile(filepath.Join(root, relativePath)); parsed != nil {
				if merged == nil {
					copy := *parsed
					merged = &copy
				} else {
					mergePluginManifest(merged, parsed)
				}
				break
			}
		}
	}
	if pigOnly := readPluginManifestFile(filepath.Join(root, ".pig-plugin", "plugin.json")); pigOnly != nil {
		if merged == nil {
			copy := *pigOnly
			merged = &copy
		} else {
			mergePluginManifest(merged, pigOnly)
		}
	}
	return merged
}

func isAgentPluginsSchema(schema string) bool {
	return strings.HasPrefix(schema, "https://agent-plugins.org/schemas/")
}

func readPluginManifestFile(manifestPath string) *pluginManifest {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil
	}
	manifest, err := parsePluginManifestData(data)
	if err != nil {
		return nil
	}
	return manifest
}

func parsePluginManifestData(data []byte) (*pluginManifest, error) {
	var header struct {
		Schema string `json:"$schema"`
		Name   string `json:"name"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return nil, err
	}
	if isAgentPluginsSchema(header.Schema) {
		return &pluginManifest{Schema: header.Schema, Name: header.Name, AgentPlugins: header.Schema == agentPluginsV1Schema}, nil
	}
	var manifest pluginManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func mergePluginManifest(dst, src *pluginManifest) {
	if src.Extensions != nil {
		dst.Extensions = src.Extensions
	}
	if src.Skills != nil {
		dst.Skills = src.Skills
	}
	if src.Prompts != nil {
		dst.Prompts = src.Prompts
	}
	if src.Themes != nil {
		dst.Themes = src.Themes
	}
	if src.Agents != nil {
		dst.Agents = src.Agents
	}
	if src.Commands != nil {
		dst.Commands = src.Commands
	}
	if src.MCPServers != nil {
		dst.MCPServers = src.MCPServers
	}
	if src.Hooks != nil {
		dst.Hooks = src.Hooks
	}
}

func manifestEntries(manifest *packageManifest, kind Kind) *[]string {
	if manifest == nil {
		return nil
	}
	switch kind {
	case Extensions:
		if manifest.PI != nil {
			return manifest.PI.Extensions
		}
	case Skills:
		if manifest.PI != nil {
			return manifest.PI.Skills
		}
	case Prompts:
		if manifest.PI != nil {
			return manifest.PI.Prompts
		}
	case Themes:
		if manifest.PI != nil {
			return manifest.PI.Themes
		}
	case Hooks:
		if manifest.Pig != nil {
			return manifest.Pig.Hooks
		}
	case MCP:
		if manifest.Pig != nil {
			return manifest.Pig.MCPServers
		}
	case AgentEnvironments:
		if manifest.Pig != nil {
			return manifest.Pig.AgentEnvironments
		}
	}
	return nil
}

func pluginEntries(manifest *pluginManifest, kind Kind) *[]string {
	if manifest == nil {
		return nil
	}
	asPtr := func(values stringList) *[]string {
		if values == nil {
			return nil
		}
		out := []string(values)
		return &out
	}
	switch kind {
	case Extensions:
		return asPtr(manifest.Extensions)
	case Skills:
		return asPtr(manifest.Skills)
	case Prompts:
		return asPtr(manifest.Prompts)
	case Themes:
		return asPtr(manifest.Themes)
	case Agents:
		return asPtr(manifest.Agents)
	case MCP:
		return asPtr(manifest.MCPServers)
	case Hooks:
		return asPtr(manifest.Hooks)
	default:
		return nil
	}
}

func coalesceEntries(primary, fallback *[]string) *[]string {
	if primary != nil {
		return primary
	}
	return fallback
}

func collectExtensionResources(root string, entries *[]string) []string {
	return Deduplicate(collectManifestResources(root, Extensions, entries))
}

func collectMCPResources(root string, entries *[]string) []string {
	if entries == nil {
		if _, err := os.Stat(filepath.Join(root, ".mcp.json")); err == nil {
			return []string{filepath.Join(root, ".mcp.json")}
		}
		return Collect([]string{filepath.Join(root, "mcp")}, MCP)
	}
	return collectManifestResources(root, MCP, entries)
}

func collectAgentEnvironments(root string, entries *[]string) []string {
	if entries != nil {
		return collectManifestResources(root, AgentEnvironments, entries)
	}
	candidates := []string{
		filepath.Join(root, ".devcontainer.json"),
		filepath.Join(root, ".devcontainer", "devcontainer.json"),
	}
	if matches, err := filepath.Glob(filepath.Join(root, ".devcontainer", "*", "devcontainer.json")); err == nil {
		candidates = append(candidates, matches...)
	}
	return existingFiles(candidates)
}

func discoverAgentEnvironmentPaths(root string) []string {
	info, err := os.Stat(root)
	if err != nil {
		return nil
	}
	if !info.IsDir() {
		if filepath.Base(root) == "devcontainer.json" || filepath.Base(root) == ".devcontainer.json" {
			return []string{root}
		}
		return nil
	}
	if filepath.Base(root) == ".devcontainer" || filepath.Base(root) == "agent-environments" {
		var candidates []string
		if direct := filepath.Join(root, "devcontainer.json"); fileExists(direct) {
			candidates = append(candidates, direct)
		}
		if matches, err := filepath.Glob(filepath.Join(root, "*", "devcontainer.json")); err == nil {
			candidates = append(candidates, matches...)
		}
		return existingFiles(candidates)
	}
	for _, candidate := range []string{filepath.Join(root, ".devcontainer.json"), filepath.Join(root, ".devcontainer", "devcontainer.json")} {
		if fileExists(candidate) {
			return []string{candidate}
		}
	}
	return nil
}

func existingFiles(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, candidate := range paths {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			out = append(out, candidate)
		}
	}
	return Deduplicate(out)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func collectManifestResources(root string, kind Kind, entries *[]string) []string {
	if entries == nil {
		return Collect([]string{filepath.Join(root, string(kind))}, kind)
	}
	if len(*entries) == 0 {
		return nil
	}
	sourceEntries := make([]string, 0, len(*entries))
	overrides := make([]string, 0)
	for _, entry := range *entries {
		if strings.HasPrefix(entry, "!") || strings.HasPrefix(entry, "+") || strings.HasPrefix(entry, "-") {
			overrides = append(overrides, entry)
			continue
		}
		sourceEntries = append(sourceEntries, entry)
	}
	resolved := make([]string, 0, len(sourceEntries))
	for _, entry := range sourceEntries {
		if strings.Contains(entry, "*") || strings.Contains(entry, "?") {
			matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(entry)))
			if err == nil {
				resolved = append(resolved, matches...)
			}
			continue
		}
		resolved = append(resolved, filepath.Join(root, filepath.FromSlash(entry)))
	}
	allFiles := Collect(resolved, kind)
	if len(overrides) == 0 {
		return allFiles
	}
	return ApplyPatterns(allFiles, overrides, root, kind)
}

func discoverFlatFiles(dir, suffix string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var paths []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	return paths
}

func discoverExtensionEntries(dir string) []string {
	if root := resolveExtensionEntries(dir); len(root) > 0 {
		return root
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var paths []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" {
			continue
		}
		full := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			paths = append(paths, resolveExtensionEntries(full)...)
			continue
		}
		if strings.HasSuffix(entry.Name(), ".ts") || strings.HasSuffix(entry.Name(), ".js") {
			paths = append(paths, full)
		}
	}
	return paths
}

// resolveExtensionEntries is upstream's resolveExtensionEntries: each
// existing entry a Pi manifest declares is its own extension, else index.ts,
// else index.js. A manifest that declares only missing entries, with no index,
// contributes nothing. A directory with another language's or PiG's
// conventional source loads as one extension that source.Resolve classifies.
func resolveExtensionEntries(dir string) []string {
	if dir == "" {
		return nil
	}
	if entries := extsource.NodeRootEntries(dir); len(entries) > 0 {
		return entries
	}
	if extsource.NodeDeclaresExtensions(dir) {
		return nil
	}
	if hasBuildFile(dir) {
		return []string{dir}
	}
	return nil
}

func walkAgentFiles(root string) []string {
	out := make([]string, 0)
	_ = filepath.Walk(root, func(filePath string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			if filePath != root && strings.HasPrefix(filepath.Base(filePath), ".") {
				return filepath.SkipDir
			}
			if filepath.Base(filePath) == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		name := filepath.Base(filePath)
		if strings.HasSuffix(name, ".agent.md") || strings.HasSuffix(name, ".md") {
			out = append(out, filePath)
		}
		return nil
	})
	return out
}

func walkFilesWithSuffix(root, suffix string) []string {
	out := make([]string, 0)
	_ = filepath.Walk(root, func(filePath string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			if filePath != root && strings.HasPrefix(filepath.Base(filePath), ".") {
				return filepath.SkipDir
			}
			if filepath.Base(filePath) == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(filePath, suffix) {
			out = append(out, filePath)
		}
		return nil
	})
	return out
}

func matchesAnyPattern(filePath string, patterns []string, baseDir string, kind Kind) bool {
	relative := filepath.ToSlash(mustRel(baseDir, filePath))
	name := filepath.Base(filePath)
	absolute := filepath.ToSlash(filePath)
	isSkill := kind == Skills
	parentDir := filepath.Dir(filePath)
	parentRelative := filepath.ToSlash(mustRel(baseDir, parentDir))
	parentName := filepath.Base(parentDir)
	parentAbsolute := filepath.ToSlash(parentDir)
	for _, patternValue := range patterns {
		normalized := filepath.ToSlash(patternValue)
		if matchGlob(relative, normalized) || matchGlob(name, normalized) || matchGlob(absolute, normalized) {
			return true
		}
		if isSkill && (matchGlob(parentRelative, normalized) || matchGlob(parentName, normalized) || matchGlob(parentAbsolute, normalized)) {
			return true
		}
	}
	return false
}

func matchesAnyExactPattern(filePath string, patterns []string, baseDir string, kind Kind) bool {
	relative := filepath.ToSlash(mustRel(baseDir, filePath))
	absolute := filepath.ToSlash(filePath)
	isSkill := kind == Skills
	parentDir := filepath.Dir(filePath)
	parentRelative := filepath.ToSlash(mustRel(baseDir, parentDir))
	parentAbsolute := filepath.ToSlash(parentDir)
	for _, patternValue := range patterns {
		normalized := normalizeExactPattern(patternValue)
		if normalized == relative || normalized == absolute {
			return true
		}
		if isSkill && (normalized == parentRelative || normalized == parentAbsolute) {
			return true
		}
	}
	return false
}

func normalizeExactPattern(patternValue string) string {
	if after, ok := strings.CutPrefix(patternValue, "./"); ok {
		return filepath.ToSlash(after)
	}
	if after, ok := strings.CutPrefix(patternValue, ".\\"); ok {
		return filepath.ToSlash(after)
	}
	return filepath.ToSlash(patternValue)
}

func mustRel(baseDir, target string) string {
	relative, err := filepath.Rel(baseDir, target)
	if err != nil {
		return target
	}
	return relative
}

func matchGlob(name, patternValue string) bool {
	matched, err := path.Match(patternValue, name)
	if err != nil {
		return name == patternValue
	}
	return matched || name == patternValue
}

func hasBuildFile(dir string) bool {
	for _, name := range []string{"go.mod", "Cargo.toml", "package.json", "index.ts", "index.js", "main.ts", "main.js", "extension.ts", "extension.js"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}
