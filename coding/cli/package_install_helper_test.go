package cli

import (
	"slices"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// ensureConfiguredPackagesInstalled runs the install step of a load (resource-loader.ts reload: resolve installs each missing configured npm and git Package) and
// reports the sources it installed and the ones that stay unavailable: failed installs, and, in offline mode, every source that needs an install.
func ensureConfiguredPackagesInstalled(cwd, agentDir string, sm *codingagent.SettingsManager) (installed, missing []string) {
	var needed []string
	for _, pkg := range packagemanager.ConfiguredPackagesForResolution(cwd, agentDir, sm) {
		if packagemanager.DetectSourceKind(pkg.Source.Source) != "local" && packagemanager.ConfiguredPackageNeedsInstall(pkg) {
			needed = append(needed, pkg.Source.Source)
		}
	}
	if packagemanager.IsOfflineModeEnabled() {
		return nil, needed
	}
	failures, err := packagemanager.NewPackageManager(packagemanager.PackageManagerOptions{CWD: cwd, AgentDir: agentDir, SettingsManager: sm}).InstallMissing(nil)
	if err != nil {
		return nil, needed
	}
	for _, failure := range failures {
		missing = append(missing, failure.Source)
	}
	for _, source := range needed {
		if !slices.Contains(missing, source) {
			installed = append(installed, source)
		}
	}
	return installed, missing
}
