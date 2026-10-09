package packagemanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sourceref "github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/crossspawn"
	"github.com/MichaelKinsy/PiG/internal/nodesemver"
)

// Ports packages/coding-agent/src/core/package-manager.ts (GetLatestNpmVersion, shouldUpdateNpmSource, npmHasAvailableUpdate).
// GetLatestNpmVersion retains the selected command and user configuration. D79 isolates user-package lookups from the invoking project's npmrc.
func GetLatestNpmVersion(cwd string, sm *codingagent.SettingsManager, source sourceref.Ref, local bool) (string, error) {
	if local && !sm.IsProjectTrusted() {
		return "", errors.New("Project is not trusted; refusing to access project package storage")
	}
	if !local {
		// pig divergence (D79): user-package metadata runs in managed storage, never the invoking project's cwd.
		cwd = NpmInstallRoot(cwd, sm.AgentDir(), source, false)
		if err := EnsureManagedPackageRoot(cwd); err != nil {
			return "", err
		}
	}
	command := DefaultNpmCommand(sm)
	args := append([]string{}, command[1:]...)
	args = append(args, "view", source.Locator, "version", "--json")
	// pig additive (D18): a source-qualified registry keeps its explicitly selected endpoint.
	if source.NPMRegistry != "" {
		args = append(args, "--registry", source.NPMRegistry)
	}
	ctx, cancel := context.WithTimeout(context.Background(), UpdateCheckNetworkTimeout)
	defer cancel()
	cmd := crossspawn.Command(ctx, cwd, command[0], args...)
	cmd.Env = append(os.Environ(), "NPM_CONFIG_FUND=false", "NPM_CONFIG_AUDIT=false")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("npm metadata lookup: %w", err)
	}
	return LatestNpmVersionFromJSON(out, source.NPMVer)
}

// LatestNpmVersionFromJSON reads npm view's version output as Pi's GetLatestNpmVersion does: a single version is returned as is, and a list yields its maxSatisfying version for a valid range or its highest version otherwise.
func LatestNpmVersionFromJSON(data []byte, version string) (string, error) {
	raw := strings.TrimSpace(string(data))
	if raw == "" {
		return "", errors.New("Empty response from npm view")
	}
	var single string
	if err := json.Unmarshal(data, &single); err == nil && raw != "null" {
		return single, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		return "", errors.New("Unexpected response from npm view")
	}
	versions := make([]string, 0, len(entries))
	for _, entry := range entries {
		var value string
		if err := json.Unmarshal(entry, &value); err == nil && value != "" {
			versions = append(versions, value)
		}
	}
	if versionRange := GetNpmVersionRange(version); versionRange != "" {
		if latest, ok := nodesemver.MaxSatisfying(versions, versionRange); ok {
			return latest, nil
		}
		return "", errors.New("Unexpected response from npm view")
	}
	return highestNpmVersion(versions)
}

// highestNpmVersion is [...versions].sort(rcompare)[0]: the first highest version. Sorting compares every element of a list with two or more entries, so any invalid version throws; a single entry is returned without comparison.
func highestNpmVersion(versions []string) (string, error) {
	switch len(versions) {
	case 0:
		return "", errors.New("Unexpected response from npm view")
	case 1:
		return versions[0], nil
	}
	var latest *nodesemver.SemVer
	var result string
	for _, value := range versions {
		parsed, err := nodesemver.Parse(value)
		if err != nil {
			return "", fmt.Errorf("Invalid Version: %s", value)
		}
		if latest == nil || parsed.Compare(latest) > 0 {
			latest, result = parsed, value
		}
	}
	return result, nil
}

// isExactNpmVersion is Pi's isExactNpmVersion.
func isExactNpmVersion(version string) bool {
	_, err := nodesemver.Parse(version)
	return err == nil
}

// newerNpmVersion is node-semver gt(target, installed), which throws for an invalid version.
func newerNpmVersion(target, installed string) (bool, error) {
	targetVersion, err := nodesemver.Parse(target)
	if err != nil {
		return false, err
	}
	installedVersion, err := nodesemver.Parse(installed)
	if err != nil {
		return false, err
	}
	return targetVersion.Compare(installedVersion) > 0, nil
}

func shouldUpdateNpmSource(cwd string, sm *codingagent.SettingsManager, source sourceref.Ref, local bool) bool {
	installedPath := filepath.Join(NpmInstallRoot(cwd, sm.AgentDir(), source, local), "node_modules", filepath.FromSlash(source.NPMName))
	installed := ReadInstalledNpmVersion(installedPath)
	if installed == "" {
		return true
	}
	latest, err := GetLatestNpmVersion(cwd, sm, source, local)
	if err != nil {
		// upstream: packages/coding-agent/src/core/package-manager.ts:shouldUpdateNpmSource
		return true
	}
	newer, err := newerNpmVersion(latest, installed)
	return err != nil || newer
}

func npmHasAvailableUpdate(cwd string, sm *codingagent.SettingsManager, source sourceref.Ref, installedPath string, local bool) bool {
	installed := ReadInstalledNpmVersion(installedPath)
	if installed == "" {
		return false
	}
	latest, err := GetLatestNpmVersion(cwd, sm, source, local)
	if err != nil {
		return false
	}
	newer, err := newerNpmVersion(latest, installed)
	return err == nil && newer
}
