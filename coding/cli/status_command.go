package cli

// pig additive (D41): Pig adds a side-effect-free aggregate status surface.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
	"github.com/MichaelKinsy/PiG/tui"
)

type statusOutput struct {
	Healthy      bool                `json:"healthy"`
	Paths        statusPaths         `json:"paths"`
	Trust        projectTrustReport  `json:"trust"`
	ActivePiglet *statusActivePiglet `json:"activePiglet,omitempty"`
	Packages     statusPackages      `json:"packages"`
	Resources    statusResources     `json:"resources"`
	Piglets      statusPiglets       `json:"piglets"`
	Errors       []string            `json:"errors"`
	Warnings     []string            `json:"warnings"`
}

type statusPaths struct {
	Home    string `json:"home"`
	Agent   string `json:"agent"`
	Project string `json:"project"`
}

type statusPackages struct {
	Total   int `json:"total"`
	User    int `json:"user"`
	Project int `json:"project"`
}

type statusResources struct {
	Total    int                  `json:"total"`
	Enabled  int                  `json:"enabled"`
	Disabled int                  `json:"disabled"`
	Ignored  int                  `json:"ignored"`
	ByKind   map[string]int       `json:"byKind"`
	Items    []statusResourceItem `json:"items"`
}

type statusResourceItem struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	Scope   string `json:"scope"`
	Origin  string `json:"origin"`
	Source  string `json:"source"`
	Enabled bool   `json:"enabled"`
	// Ignored is why this run does not load the resource although it exists: "untrusted" (its project is not trusted) or
	// "piglet-discovery" (the active Piglet's discovery excludes its scope). Empty when it applies. An ignored resource is never enabled.
	Ignored string `json:"ignored,omitempty"`
	Health  string `json:"health"`
}

type statusPiglets struct {
	Total      int `json:"total"`
	WithSource int `json:"withSource"`
	WithBinary int `json:"withBinary"`
}

// statusActivePiglet is the Piglet the session that started this command runs, read from PIG_ACTIVE_PIGLET_SOURCE (or this
// process's own Piglet Binary). Its discovery marks ambient extensions and skills as ignored.
type statusActivePiglet struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Binary bool   `json:"binary"`
}

// statusResourceEntry is one inventory item with the reason this run ignores it, empty when it applies.
type statusResourceEntry struct {
	tui.ResourceItem
	ignored string
}

// Reasons status gives for a resource the run does not load.
const (
	statusIgnoredUntrusted = "untrusted"
	statusIgnoredPiglet    = "piglet-discovery"
)

// statusIgnoredReason reports why the active Piglet's discovery leaves resource out, or "".
func statusIgnoredReason(active *inspectedPiglet, resource tui.ResourceItem) string {
	if active.excludes(string(resource.ResourceType), resource.Scope) {
		return statusIgnoredPiglet
	}
	return ""
}

// collectIgnoredProjectResources lists the project-scope resources an untrusted project holds, as ignored entries.
func collectIgnoredProjectResources(cwd, agentDir string) ([]statusResourceEntry, error) {
	items, err := collectConfigResourceItems(cwd, agentDir, codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, true))
	if err != nil {
		return nil, err
	}
	var ignored []statusResourceEntry
	for _, item := range items {
		if item.Scope == "project" {
			ignored = append(ignored, statusResourceEntry{ResourceItem: item, ignored: statusIgnoredUntrusted})
		}
	}
	return ignored, nil
}

func runStatusCommand(args []string) int {
	if len(args) == 0 || args[0] != "status" {
		return -1
	}
	jsonMode := false
	for _, arg := range args[1:] {
		switch arg {
		case "--json":
			jsonMode = true
		case "--no-input":
		case "-h", "--help":
			fmt.Println("Usage: pig status [--json] [--no-input]")
			return 0
		default:
			fmt.Fprintln(os.Stderr, "Usage: pig status [--json] [--no-input]")
			return 2
		}
	}
	status := collectStatus()
	if jsonMode {
		data, err := json.Marshal(status)
		if err != nil {
			fmt.Fprintf(os.Stderr, "pig status: encode JSON: %v\n", err)
			return 1
		}
		fmt.Println(string(data))
	} else {
		renderStatus(status)
	}
	if !status.Healthy {
		return 1
	}
	return 0
}

// statusLoadHealth runs the loader a session uses on one skill or theme file and returns why it rejects the file, or "". Pi's
// loader leaves out a skill without a description (core/skills.ts loadSkillFromFile) and rejects a theme that does not match
// the theme schema, an unknown color role included (core/theme/theme-schema.ts); both only warn, so the file shows up as a
// resource that the session does not have.
func statusLoadHealth(kind packagecontent.Kind, path, cwd, agentDir string) string {
	switch kind {
	case packagecontent.Skills:
		result, err := codingagent.LoadSkills(codingagent.LoadSkillsOptions{CWD: cwd, AgentDir: agentDir, SkillPaths: []string{path}})
		switch {
		case err != nil:
			return "not loaded: " + err.Error()
		case len(result.Skills) == 0 && len(result.Diagnostics) > 0:
			return "not loaded: " + result.Diagnostics[0].Message
		case len(result.Skills) == 0:
			return "not loaded: the file is not a skill"
		}
	case packagecontent.Themes:
		if _, err := tui.LoadThemeFile(path); err != nil {
			return "invalid: " + err.Error()
		}
	}
	return ""
}

func collectStatus() statusOutput {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	agentDir := codingagent.AgentDir()
	// The report leaves the project path empty when the workspace's config directory is the user config root. Resource loading has no such rule.
	projectPath := ""
	if projectRoot := codingagent.ProjectConfigDir(cwd); canonicalStatusPath(projectRoot) != canonicalStatusPath(codingagent.ConfigRoot()) {
		projectPath = canonicalStatusPath(projectRoot)
	}
	status := statusOutput{
		Paths: statusPaths{
			Home:    canonicalStatusPath(codingagent.ConfigRoot()),
			Agent:   canonicalStatusPath(agentDir),
			Project: projectPath,
		},
		Resources: statusResources{ByKind: map[string]int{}},
		Errors:    []string{},
		Warnings:  []string{},
	}
	// pig additive (D41): the report follows the trust decision and the active Piglet of the run it describes. A project this
	// process does not trust contributes no settings, Packages or errors; its resources are listed as ignored.
	trust := reportProjectTrust(cwd, agentDir)
	status.Trust = trust
	if trust.Error != "" {
		status.Warnings = append(status.Warnings, "project trust: "+trust.Error)
	}
	active := inspectActivePiglet(cwd)
	if active != nil {
		status.ActivePiglet = &statusActivePiglet{Name: active.Name, Source: active.Source, Binary: active.Baked}
		if active.Err != nil {
			status.Warnings = append(status.Warnings, active.Err.Error())
		}
	}
	settings := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, trust.Trusted())
	for _, settingsError := range settings.DrainErrors() {
		status.Errors = append(status.Errors, fmt.Sprintf("%s settings: %v", settingsError.Scope, settingsError.Error))
	}
	packages := packagemanager.ConfiguredPackagesForResolution(cwd, settings.AgentDir(), settings)
	status.Packages.Total = len(packages)
	for _, pkg := range packages {
		if pkg.Scope == "project" {
			status.Packages.Project++
		} else {
			status.Packages.User++
		}
		if pkg.InstalledPath == "" {
			status.Errors = append(status.Errors, fmt.Sprintf("%s Package %q is not materialized; run pig update %s", pkg.Scope, pkg.Source.Source, pkg.Source.Source))
			continue
		}
		filters, filterErr := effectiveConfiguredPackageFilters(pkg)
		if filterErr != nil {
			status.Errors = append(status.Errors, fmt.Sprintf("%s Package %q is invalid: %v", pkg.Scope, pkg.Source.Source, filterErr))
		} else if _, err := packagecontent.ValidateConfigured(pkg.InstalledPath, filters); err != nil {
			status.Errors = append(status.Errors, fmt.Sprintf("%s Package %q is invalid: %v", pkg.Scope, pkg.Source.Source, err))
		}
	}
	resources, resourcesErr := collectConfigResourceItems(cwd, agentDir, settings)
	if resourcesErr != nil {
		status.Errors = append(status.Errors, "resource inventory: "+resourcesErr.Error())
	} else {
		entries := make([]statusResourceEntry, 0, len(resources))
		for _, resource := range resources {
			entries = append(entries, statusResourceEntry{ResourceItem: resource, ignored: statusIgnoredReason(active, resource)})
		}
		if !trust.Trusted() {
			ignoredProject, listErr := collectIgnoredProjectResources(cwd, agentDir)
			if listErr != nil {
				status.Warnings = append(status.Warnings, "the untrusted project's resources could not be listed: "+listErr.Error())
			}
			entries = append(entries, ignoredProject...)
		}
		status.Resources.Total = len(entries)
		// The loader identifies an extension by its path, not its name:
		// DisambiguateIdentities renames same-named copies ("ask:2", "ask:3")
		// and loads them all, as upstream Pi loads every extension path. So a
		// shared extension name is a supported condition and a warning, never
		// the fatal error status treats a duplicate as here. Skills, prompts
		// and themes, by contrast, key their runtime set by name (dedupeSkills/
		// dedupePrompts/dedupeThemes), so a same-scope duplicate is unusable
		// and stays an error. pig additive (D41)
		seenIdentities := make(map[string]string, len(resources))
		for _, entry := range entries {
			resource := entry.ResourceItem
			kind := packagecontent.Kind(resource.ResourceType)
			name := resource.DisplayName
			if resource.Health == "" {
				resolvedName, err := packagecontent.PublicName(kind, resource.Path, "")
				switch {
				case err == nil:
					name = resolvedName
				case entry.ignored == "":
					status.Errors = append(status.Errors, fmt.Sprintf("%s resource %s: %v", kind, resource.Path, err))
				}
			}
			if name == "" {
				name = filepath.Base(resource.Path)
			}
			identity := string(resource.ResourceType) + "\x00" + resource.Scope + "\x00" + name
			// A disabled or ignored copy is never loaded, so it cannot collide at runtime; it stays in the inventory.
			if !resource.Enabled || entry.ignored != "" {
			} else if previous, exists := seenIdentities[identity]; exists {
				if kind == packagecontent.Extensions {
					//perf:ok one message per duplicate found, in a one-shot status command
					status.Warnings = append(status.Warnings, fmt.Sprintf("duplicate extensions resource %q in %s scope: %s and %s", name, resource.Scope, previous, resource.Path))
				} else {
					status.Errors = append(status.Errors, fmt.Sprintf("duplicate %s resource %q in %s scope: %s and %s", kind, name, resource.Scope, previous, resource.Path))
				}
			} else {
				seenIdentities[identity] = resource.Path
			}
			// pig additive (D41): health is what the loader says about a resource this run would load, not only whether the file exists.
			health := resource.Health
			if health == "" && resource.Enabled && entry.ignored == "" {
				if health = statusLoadHealth(kind, resource.Path, cwd, agentDir); health != "" {
					//perf:ok one message per rejected resource, in a one-shot status command
					status.Warnings = append(status.Warnings, fmt.Sprintf("%s %s: %s", kind, name, health))
				}
			}
			status.Resources.Items = append(status.Resources.Items, statusResourceItem{
				Kind: string(resource.ResourceType), Name: name, Path: canonicalStatusPath(resource.Path),
				Scope: resource.Scope, Origin: resource.Origin, Source: resource.Source,
				Enabled: resource.Enabled && entry.ignored == "", Ignored: entry.ignored, Health: health,
			})
			status.Resources.ByKind[string(resource.ResourceType)]++
			switch {
			case entry.ignored != "":
				status.Resources.Ignored++
			case resource.Enabled:
				status.Resources.Enabled++
			default:
				status.Resources.Disabled++
			}
		}
		slices.SortFunc(status.Resources.Items, func(a, b statusResourceItem) int {
			for _, value := range []int{strings.Compare(a.Kind, b.Kind), strings.Compare(a.Name, b.Name), strings.Compare(a.Scope, b.Scope), strings.Compare(a.Path, b.Path)} {
				if value != 0 {
					return value
				}
			}
			return 0
		})
	}
	if piglets, err := piglet.List(); err != nil {
		status.Errors = append(status.Errors, "Piglet inventory: "+err.Error())
	} else {
		status.Piglets.Total = len(piglets)
		for _, item := range piglets {
			if item.Path != "" {
				status.Piglets.WithSource++
			}
			if len(item.Records) > 0 {
				status.Piglets.WithBinary++
			}
		}
	}
	status.Errors = deduplicateStatusErrors(status.Errors)
	status.Warnings = deduplicateStatusErrors(status.Warnings)
	status.Healthy = len(status.Errors) == 0
	return status
}

func renderStatus(status statusOutput) {
	state := "healthy"
	if !status.Healthy {
		state = "invalid"
	}
	fmt.Printf("Pig status: %s\n", state)
	fmt.Printf("  paths: home=%s agent=%s project=%s\n", status.Paths.Home, status.Paths.Agent, status.Paths.Project)
	fmt.Printf("  project trust: %s (%s)\n", status.Trust.State, status.Trust.Reason)
	if status.ActivePiglet != nil {
		fmt.Printf("  active Piglet: %s (%s)\n", status.ActivePiglet.Name, status.ActivePiglet.Source)
	}
	fmt.Printf("  packages: %d (user=%d project=%d)\n", status.Packages.Total, status.Packages.User, status.Packages.Project)
	fmt.Printf("  resources: %d (enabled=%d disabled=%d ignored=%d)\n", status.Resources.Total, status.Resources.Enabled, status.Resources.Disabled, status.Resources.Ignored)
	for _, item := range status.Resources.Items {
		if item.Ignored != "" {
			fmt.Printf("    ignored (%s): %s %s (%s scope) %s\n", item.Ignored, item.Kind, item.Name, item.Scope, item.Path)
		}
	}
	fmt.Printf("  piglets: %d (source=%d binary=%d)\n", status.Piglets.Total, status.Piglets.WithSource, status.Piglets.WithBinary)
	for _, message := range status.Errors {
		printCLIError("%s", message)
	}
	for _, message := range status.Warnings {
		fmt.Printf("Warning: %s\n", message)
	}
}

func canonicalStatusPath(path string) string {
	absolute, err := filepath.Abs(path)
	if err == nil {
		path = absolute
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

func deduplicateStatusErrors(errors []string) []string {
	slices.Sort(errors)
	return slices.Compact(errors)
}
