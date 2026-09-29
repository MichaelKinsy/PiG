package packagemanager

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Network timeout for update checks. Mirrors upstream NETWORK_TIMEOUT_MS
// (package-manager.ts).
const UpdateCheckNetworkTimeout = 10 * time.Second

// IsOfflineModeEnabled returns true when the PIG_OFFLINE (or legacy
// PI_OFFLINE) env var is set to a truthy value. Mirrors upstream
// isOfflineModeEnabled (package-manager.ts:29).
func IsOfflineModeEnabled() bool {
	if value := os.Getenv("PI_OFFLINE"); value != "" {
		return value == "1" || strings.EqualFold(value, "true") || strings.EqualFold(value, "yes")
	}
	v := strings.ToLower(strings.TrimSpace(os.Getenv("PIG_OFFLINE")))
	return v == "1" || v == "true" || v == "yes"
}

// EnsureConfiguredPackagesInstalled reinstalls missing installations and npm packages whose local manifest does not satisfy the configured version or range. Offline mode reports these sources as missing without installing them.
//
// Returns the source strings that were reinstalled and the sources that remain unavailable because installation failed or offline mode prevented it.
// upstream: packages/coding-agent/src/core/package-manager.ts:resolvePackageSources
func EnsureConfiguredPackagesInstalled(cwd, agentDir string, sm *codingagent.SettingsManager) (reinstalled, missing []string) {
	packages := ConfiguredPackagesForResolution(cwd, agentDir, sm)
	if IsOfflineModeEnabled() {
		for _, pkg := range packages {
			if ConfiguredPackageNeedsInstall(pkg) {
				missing = append(missing, pkg.Source.Source)
			}
		}
		return nil, missing
	}
	for _, pkg := range packages {
		if !ConfiguredPackageNeedsInstall(pkg) {
			continue
		}
		local := pkg.Scope == "project"
		source := pkg.Source
		if DetectSourceKind(source.Source) == "local" {
			resolved, err := ResolveLocalPackageRoot(SettingsBaseDir(cwd, agentDir, local), source.Source)
			if err != nil {
				missing = append(missing, source.Source)
				continue
			}
			source.Source = resolved
		}
		if err := InstallPackageArtifacts(cwd, agentDir, sm, source, local, nil); err != nil {
			missing = append(missing, pkg.Source.Source)
			continue
		}
		reinstalled = append(reinstalled, pkg.Source.Source)
	}
	return reinstalled, missing
}

// InstallMissingPackages installs each configured npm and git Package whose installation is absent or does not satisfy its configured version, as DefaultPackageManager.resolve installs a source when it has no onMissing callback (package-manager.ts:1251-1305). The first failed installation is returned. A local Package that does not exist is skipped, as resolveLocalExtensionSource skips it, and offline mode installs nothing.
func InstallMissingPackages(cwd, agentDir string, sm *codingagent.SettingsManager) error {
	if IsOfflineModeEnabled() {
		return nil
	}
	for _, pkg := range ConfiguredPackagesForResolution(cwd, agentDir, sm) {
		if DetectSourceKind(pkg.Source.Source) == "local" || !ConfiguredPackageNeedsInstall(pkg) {
			continue
		}
		if err := InstallPackageArtifacts(cwd, agentDir, sm, pkg.Source, pkg.Scope == "project", nil); err != nil {
			return fmt.Errorf("install %s Package %q: %w", pkg.Scope, pkg.Source.Source, err)
		}
	}
	return nil
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
