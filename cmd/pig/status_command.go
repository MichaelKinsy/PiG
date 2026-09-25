package main

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
)

type statusOutput struct {
	Healthy   bool            `json:"healthy"`
	Paths     statusPaths     `json:"paths"`
	Packages  statusPackages  `json:"packages"`
	Resources statusResources `json:"resources"`
	Piglets   statusPiglets   `json:"piglets"`
	Errors    []string        `json:"errors"`
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
	Health  string `json:"health"`
}

type statusPiglets struct {
	Total      int `json:"total"`
	WithSource int `json:"withSource"`
	WithBinary int `json:"withBinary"`
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

func collectStatus() statusOutput {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	agentDir := codingagent.AgentDir()
	projectPath := ""
	if projectRoot, ok := projectResourceRoot(cwd); ok {
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
	}
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	for _, settingsError := range settings.DrainErrors() {
		status.Errors = append(status.Errors, fmt.Sprintf("%s settings: %v", settingsError.Scope, settingsError.Error))
	}
	packages := configuredPackagesForResolution(cwd, settings)
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
	if resources, err := collectConfigResourceItems(cwd, agentDir, settings); err != nil {
		status.Errors = append(status.Errors, "resource inventory: "+err.Error())
	} else {
		status.Resources.Total = len(resources)
		seenIdentities := make(map[string]string, len(resources))
		for _, resource := range resources {
			kind := packagecontent.Kind(resource.ResourceType)
			name := resource.DisplayName
			if resource.Health == "" {
				resolvedName, err := packagecontent.PublicName(kind, resource.Path, "")
				if err != nil {
					status.Errors = append(status.Errors, fmt.Sprintf("%s resource %s: %v", kind, resource.Path, err))
				} else {
					name = resolvedName
				}
			}
			if name == "" {
				name = filepath.Base(resource.Path)
			}
			identity := string(resource.ResourceType) + "\x00" + resource.Scope + "\x00" + name
			if previous, exists := seenIdentities[identity]; exists {
				status.Errors = append(status.Errors, fmt.Sprintf("duplicate %s resource %q in %s scope: %s and %s", kind, name, resource.Scope, previous, resource.Path))
			} else {
				seenIdentities[identity] = resource.Path
			}
			status.Resources.Items = append(status.Resources.Items, statusResourceItem{
				Kind: string(resource.ResourceType), Name: name, Path: canonicalStatusPath(resource.Path),
				Scope: resource.Scope, Origin: resource.Origin, Source: resource.Source,
				Enabled: resource.Enabled, Health: resource.Health,
			})
			status.Resources.ByKind[string(resource.ResourceType)]++
			if resource.Enabled {
				status.Resources.Enabled++
			} else {
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
	fmt.Printf("  packages: %d (user=%d project=%d)\n", status.Packages.Total, status.Packages.User, status.Packages.Project)
	fmt.Printf("  resources: %d (enabled=%d disabled=%d)\n", status.Resources.Total, status.Resources.Enabled, status.Resources.Disabled)
	fmt.Printf("  piglets: %d (source=%d binary=%d)\n", status.Piglets.Total, status.Piglets.WithSource, status.Piglets.WithBinary)
	for _, message := range status.Errors {
		fmt.Fprintln(os.Stderr, "error:", message)
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
