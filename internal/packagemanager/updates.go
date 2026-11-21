package packagemanager

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/coding-agent/src/core/package-manager.ts (NETWORK_TIMEOUT_MS, isOfflineModeEnabled).
// Network timeout for update checks. Mirrors upstream NETWORK_TIMEOUT_MS
// (package-manager.ts).
const UpdateCheckNetworkTimeout = 10 * time.Second

// IsOfflineModeEnabled returns true when the PIG_OFFLINE (or legacy
// PI_OFFLINE) env var is set to a truthy value. Mirrors upstream
// isOfflineModeEnabled (package-manager.ts:29).
func IsOfflineModeEnabled() bool {
	if value := os.Getenv("PI_OFFLINE"); value != "" {
		lower := jsstring.ToLower(value)
		return value == "1" || lower == "true" || lower == "yes"
	}
	v := strings.ToLower(strings.TrimSpace(os.Getenv("PIG_OFFLINE")))
	return v == "1" || v == "true" || v == "yes"
}

// MissingLocalPackages lists each configured local Package whose directory does not exist, resolved against its scope's settings directory, as the
// install failure the session warns about and `pig install --validate-only` reports (D216). Pi skips such a Package without a word.
func MissingLocalPackages(cwd, agentDir string, sm *codingagent.SettingsManager) []PackageInstallFailure {
	var failures []PackageInstallFailure
	for _, pkg := range ConfiguredPackagesForResolution(cwd, agentDir, sm) {
		if DetectSourceKind(pkg.Source.Source) != "local" {
			continue
		}
		root, err := ResolveLocalPackageRoot(SettingsBaseDir(cwd, agentDir, pkg.Scope == "project"), pkg.Source.Source)
		path := ""
		if err == nil {
			path = root
			if _, statErr := os.Stat(root); statErr == nil {
				continue
			}
			err = fmt.Errorf("Path does not exist: %s", root)
		}
		failures = append(failures, PackageInstallFailure{Source: pkg.Source.Source, Scope: pkg.Scope, Path: path, Message: err.Error()})
	}
	return failures
}

// ConfiguredPackageNeedsInstall reports whether the Package has no installation or an installation that does not satisfy its configured version.
func ConfiguredPackageNeedsInstall(pkg ConfiguredPackage) bool {
	return pkg.InstalledPath == "" || !InstalledPackageMatchesConfiguredVersion(pkg)
}

// InstalledPackageMatchesConfiguredVersion checks configured and temporary npm packages with the same local manifest rule. Inherited deltas use the source that owns their installation.
func InstalledPackageMatchesConfiguredVersion(pkg ConfiguredPackage) bool {
	sourceText := pkg.ResolvedSource
	if sourceText == "" {
		sourceText = pkg.Source.Source
	}
	if DetectSourceKind(sourceText) != "npm" {
		return true
	}
	source, err := ParseNpmInstallRef(sourceText)
	return err == nil && InstalledNpmMatchesConfiguredVersion(source, pkg.InstalledPath)
}
