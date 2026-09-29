package packagemanager

import (
	"encoding/json"
	"os"
	"path/filepath"

	sourceref "github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/nodesemver"
	"github.com/MichaelKinsy/PiG/internal/text"
)

// GetNpmVersionRange is Pi's getNpmVersionRange: the node-semver range a selector denotes, or "" when it is absent or not a valid npm range, such as a dist tag.
func GetNpmVersionRange(version string) string {
	if version == "" {
		return ""
	}
	versionRange, _ := nodesemver.ValidRange(version)
	return versionRange
}

// InstalledNpmMatchesConfiguredVersion checks the local manifest only; dist tags and omitted versions never trigger registry lookups during resolution.
// upstream: packages/coding-agent/src/core/package-manager.ts:installedNpmMatchesConfiguredVersion
func InstalledNpmMatchesConfiguredVersion(source sourceref.Ref, installedPath string) bool {
	installed := ReadInstalledNpmVersion(installedPath)
	if installed == "" {
		return false
	}
	versionRange := GetNpmVersionRange(source.NPMVer)
	return versionRange == "" || nodesemver.Satisfies(installed, versionRange)
}

func ReadInstalledNpmVersion(installedPath string) string {
	data, err := os.ReadFile(filepath.Join(installedPath, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(text.StripBomBytes(data), &pkg); err != nil {
		return ""
	}
	return pkg.Version
}
