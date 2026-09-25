package main

// pig additive (D41): Pig adds namespaced, side-effect-free Package inventory
// and validation without changing upstream Package installation or settings.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

type packageValidationOutput struct {
	Valid     bool           `json:"valid"`
	Root      string         `json:"root"`
	Resources map[string]int `json:"resources,omitempty"`
	Error     string         `json:"error,omitempty"`
}

type packageOperationOutput struct {
	Command string `json:"command"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

type packageListOutput struct {
	Packages []packageListItem `json:"packages"`
}

type packageListItem struct {
	Source        string `json:"source"`
	Scope         string `json:"scope"`
	InstalledPath string `json:"installedPath,omitempty"`
	Filtered      bool   `json:"filtered"`
}

func runPackageManagementCommand(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printPackageManagementHelp()
		return 0
	}
	switch args[0] {
	case "list":
		return runPackageList(args[1:])
	case "validate":
		return runPackageValidate(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "pig package: unknown command %q; use list or validate\n", args[0])
		return 2
	}
}

func printPackageManagementHelp() {
	fmt.Print(`Usage:
  pig package list [--json] [--no-input]
  pig package validate <dir> [--json] [--no-input]

Package source is authored as ordinary package.json. Install, remove, update,
and configure Packages through Pig's upstream-compatible top-level commands.
`)
}

func runPackageList(args []string) int {
	jsonOutput := false
	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			fmt.Println("Usage: pig package list [--json] [--no-input]")
			return 0
		case "--json":
			jsonOutput = true
		case "--no-input":
		default:
			fmt.Fprintln(os.Stderr, "Usage: pig package list [--json] [--no-input]")
			return 2
		}
	}
	cwd, _, sm, err := packageContext()
	if err != nil {
		if jsonOutput {
			writeJSON(packageOperationOutput{Command: "list", Success: false, Error: err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "pig package list: %v\n", err)
		}
		return 1
	}
	if jsonOutput {
		return listPackagesJSON(cwd, sm)
	}
	return listPackages(cwd, sm)
}

// pig additive (D41): namespaced Package inventory has stable JSON.
func listPackagesJSON(cwd string, sm *codingagent.SettingsManager) int {
	packages := listConfiguredPackages(cwd, sm)
	items := make([]packageListItem, 0, len(packages))
	for _, pkg := range packages {
		items = append(items, packageListItem{
			Source:        pkg.Source.Source,
			Scope:         pkg.Scope,
			InstalledPath: pkg.InstalledPath,
			Filtered:      pkg.Source.Filtered(),
		})
	}
	writeJSON(packageListOutput{Packages: items})
	return 0
}

func writeJSON(value any) {
	data, err := json.Marshal(value)
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode JSON output: %v\n", err)
		return
	}
	fmt.Println(string(data))
}

func runPackageValidate(args []string) int {
	jsonOutput := false
	var dir string
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOutput = true
		case "--no-input":
		case "-h", "--help":
			fmt.Println("Usage: pig package validate <dir> [--json] [--no-input]")
			return 0
		default:
			if strings.HasPrefix(arg, "-") || dir != "" {
				fmt.Fprintln(os.Stderr, "Usage: pig package validate <dir> [--json] [--no-input]")
				return 2
			}
			dir = arg
		}
	}
	if dir == "" {
		fmt.Fprintln(os.Stderr, "Usage: pig package validate <dir> [--json] [--no-input]")
		return 2
	}
	root, _ := filepath.Abs(dir)
	resources, err := packagecontent.ValidatePackage(root)
	output := packageValidationOutput{Valid: err == nil, Root: root}
	if err != nil {
		output.Error = err.Error()
	} else {
		output.Resources = map[string]int{
			"extensions":        len(resources.ExtensionEntries),
			"skills":            len(resources.SkillDirs),
			"prompts":           len(resources.PromptFiles),
			"themes":            len(resources.ThemeFiles),
			"agents":            len(resources.AgentFiles),
			"hooks":             len(resources.HookFiles),
			"mcpServers":        len(resources.MCPFiles),
			"agentEnvironments": len(resources.AgentEnvironments),
		}
	}
	switch {
	case jsonOutput:
		writeJSON(output)
	case err != nil:
		fmt.Fprintln(os.Stderr, "Package invalid:", err)
	default:
		fmt.Printf("Package valid: %s\n", root)
		for _, kind := range []string{"extensions", "skills", "prompts", "themes", "agents", "hooks", "mcpServers", "agentEnvironments"} {
			if count := output.Resources[kind]; count > 0 {
				fmt.Printf("  %s: %d\n", kind, count)
			}
		}
	}
	if err != nil {
		return 1
	}
	return 0
}
