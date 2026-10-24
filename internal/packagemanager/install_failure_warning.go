package packagemanager

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Ports packages/coding-agent/src/core/resource-loader.ts describePackageInstallFailure and formatPackageInstallWarning (Pi 6dfb54678).

// DescribePackageInstallFailure describes a Package that was skipped because it could not be installed, including how to drop it: a temporary -e
// source says to check the source, a configured Package names `<app> remove <source>` (with --local for a project Package).
func DescribePackageInstallFailure(failure PackageInstallFailure) string {
	var hint string
	switch failure.Scope {
	case "temporary":
		hint = "Check the source and try again."
	case "project":
		hint = fmt.Sprintf("Run \"%s remove %s --local\" to remove it from settings.json.", codingagent.AppName, failure.Source)
	default:
		hint = fmt.Sprintf("Run \"%s remove %s\" to remove it from settings.json.", codingagent.AppName, failure.Source)
	}
	return fmt.Sprintf("failed to install (%s). %s", failure.Message, hint)
}

// FormatPackageInstallWarning is the one-line startup warning for a Package that could not be installed.
func FormatPackageInstallWarning(failure PackageInstallFailure) string {
	return fmt.Sprintf("Extension package %q: %s", failure.Source, DescribePackageInstallFailure(failure))
}

// PackageInstallWarnings are the extension warnings of the failures, one per Package, keyed by its source (resource-loader.ts collectPackageInstallWarnings).
func PackageInstallWarnings(failures ...[]PackageInstallFailure) []codingagent.ExtensionWarning {
	var warnings []codingagent.ExtensionWarning
	for _, entries := range failures {
		for _, failure := range entries {
			warnings = append(warnings, codingagent.ExtensionWarning{Path: failure.Source, Warning: DescribePackageInstallFailure(failure)})
		}
	}
	return warnings
}
