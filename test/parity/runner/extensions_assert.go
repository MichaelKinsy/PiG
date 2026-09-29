//go:build parity

package runner

import (
	"encoding/json"
	"fmt"
	"slices"
)

type extensionValidationJSON struct {
	Valid       bool                      `json:"valid"`
	Name        string                    `json:"name"`
	Tools       []string                  `json:"tools"`
	Commands    []string                  `json:"commands"`
	Shortcuts   []string                  `json:"shortcuts"`
	Providers   []string                  `json:"providers"`
	Diagnostics []extensionDiagnosticJSON `json:"diagnostics"`
	Extensions  []extensionValidationJSON `json:"extensions"`
	Placement   struct {
		Groups []struct {
			Extensions []string `json:"extensions"`
			Strategy   string   `json:"strategy"`
		} `json:"groups"`
		Cells []struct {
			Extensions []string `json:"extensions"`
			Action     string   `json:"action"`
		} `json:"cells"`
	} `json:"placement"`
}

type extensionDiagnosticJSON struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

func evaluateExtensionAssertions(o *ScenarioOutcome, spec ExtensionAssertSpec) {
	if !hasExtensionAssertions(spec) {
		return
	}
	if len(o.Pig.Runs) == 0 {
		o.Failures = append(o.Failures, "extension assertions require at least one pig run")
		return
	}
	for run, result := range o.Pig.Runs {
		evaluateExtensionRunAssertions(o, spec, run, result)
	}
}

func evaluateExtensionRunAssertions(o *ScenarioOutcome, spec ExtensionAssertSpec, run int, result Result) {
	var report extensionValidationJSON
	if err := json.Unmarshal([]byte(result.Output), &report); err != nil {
		o.Failures = append(o.Failures, fmt.Sprintf("extension run %d assertions could not parse pig JSON: %v", run+1, err))
		return
	}
	if !report.Valid {
		o.Failures = append(o.Failures, fmt.Sprintf("extension run %d validation JSON valid=false", run+1))
	}
	tools := extensionStrings(report, func(r extensionValidationJSON) []string { return r.Tools })
	commands := extensionStrings(report, func(r extensionValidationJSON) []string { return r.Commands })
	providers := extensionStrings(report, func(r extensionValidationJSON) []string { return r.Providers })
	shortcuts := extensionStrings(report, func(r extensionValidationJSON) []string { return r.Shortcuts })
	checkContains := func(kind string, got, want []string) {
		for _, w := range want {
			if !slices.Contains(got, w) {
				o.Failures = append(o.Failures, fmt.Sprintf("extension run %d %s missing %q (got %v)", run+1, kind, w, got))
			}
		}
	}
	checkContains("tool", tools, spec.RegisteredTools)
	checkContains("command", commands, spec.RegisteredCommands)
	checkContains("provider", providers, spec.RegisteredProviders)
	checkContains("shortcut", shortcuts, spec.RegisteredShortcuts)
	if spec.NoDiagnostics && len(report.Diagnostics) > 0 {
		o.Failures = append(o.Failures, fmt.Sprintf("extension run %d diagnostics present: %+v", run+1, report.Diagnostics))
	}
	if len(spec.PlacementStrategy) > 0 {
		placements := map[string]string{}
		for _, g := range report.Placement.Groups {
			for _, ext := range g.Extensions {
				placements[ext] = g.Strategy
			}
		}
		for ext, want := range spec.PlacementStrategy {
			if got := placements[ext]; got != want {
				o.Failures = append(o.Failures, fmt.Sprintf("extension run %d placement for %q = %q, want %q (all %v)", run+1, ext, got, want, placements))
			}
		}
	}
}

func hasExtensionAssertions(spec ExtensionAssertSpec) bool {
	return len(spec.RegisteredTools) > 0 || len(spec.RegisteredCommands) > 0 ||
		len(spec.RegisteredProviders) > 0 || len(spec.RegisteredShortcuts) > 0 ||
		spec.NoDiagnostics || len(spec.PlacementStrategy) > 0
}

func extensionStrings(report extensionValidationJSON, pick func(extensionValidationJSON) []string) []string {
	var out []string
	if len(report.Extensions) == 0 {
		out = append(out, pick(report)...)
		return out
	}
	for _, ext := range report.Extensions {
		out = append(out, pick(ext)...)
	}
	return out
}
