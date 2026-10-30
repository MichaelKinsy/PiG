package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// configuredPackagesReport is the JSON of `pig install --validate-only --json` with no source: whether every configured local Package resolves.
// Checked counts the local entries of the user and trusted project settings; Missing lists each one that does not resolve to a directory.
type configuredPackagesReport struct {
	Valid   bool                       `json:"valid"`
	Checked int                        `json:"checked"`
	Missing []missingConfiguredPackage `json:"missing"`
}

type missingConfiguredPackage struct {
	Source string `json:"source"`
	Scope  string `json:"scope"`
	// Path is the directory the source resolved to; omitted when the source did not resolve to a path.
	Path   string `json:"path,omitempty"`
	Error  string `json:"error"`
	Remove string `json:"remove"`
}

// validateConfiguredPackages reports each configured local Package whose path does not resolve under the agent directory and exits 1 when any is missing.
// npm and git entries are not checked: resolving them needs the network.
//
// pig additive (D216): Pi skips a missing local Package silently; `pig install --validate-only` names it with its resolved path and the `pig remove` that drops it.
func validateConfiguredPackages(cwd string, sm *codingagent.SettingsManager, jsonOut bool) int {
	report := configuredPackagesReport{Missing: []missingConfiguredPackage{}}
	for _, pkg := range packagemanager.ConfiguredPackagesForResolution(cwd, sm.AgentDir(), sm) {
		if packagemanager.DetectSourceKind(pkg.Source.Source) == "local" {
			report.Checked++
		}
	}
	for _, failure := range packagemanager.MissingLocalPackages(cwd, sm.AgentDir(), sm) {
		remove := codingagent.AppName + " remove " + failure.Source
		if failure.Scope == "project" {
			remove += " -l"
		}
		report.Missing = append(report.Missing, missingConfiguredPackage{Source: failure.Source, Scope: failure.Scope, Path: failure.Path, Error: failure.Message, Remove: remove})
	}
	report.Valid = len(report.Missing) == 0
	switch {
	case jsonOut:
		// JSON.stringify(value, null, 2) writes <, > and & as they are; Go's default encoder escapes them.
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(report)
	case report.Valid:
		if report.Checked == 1 {
			_, _ = fmt.Fprintln(os.Stdout, "valid: 1 configured local package resolves")
		} else {
			_, _ = fmt.Fprintf(os.Stdout, "valid: %d configured local packages resolve\n", report.Checked)
		}
	default:
		for _, missing := range report.Missing {
			_, _ = fmt.Fprintf(os.Stderr, "missing %s package %q: %s. Run \"%s\"\n", missing.Scope, missing.Source, missing.Error, missing.Remove)
		}
	}
	if !report.Valid {
		return 1
	}
	return 0
}
