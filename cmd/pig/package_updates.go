package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/crossspawn"
)

// Network timeout for update checks. Mirrors upstream NETWORK_TIMEOUT_MS
// (package-manager.ts).
const updateCheckNetworkTimeout = 10 * time.Second

// updateCheckConcurrency limits parallel npm/git remote queries.
const updateCheckConcurrency = 5

// IsOfflineModeEnabled returns true when the PIG_OFFLINE (or legacy
// PI_OFFLINE) env var is set to a truthy value. Mirrors upstream
// isOfflineModeEnabled (package-manager.ts:29).
func IsOfflineModeEnabled() bool {
	if strings.TrimSpace(os.Getenv("PI_OFFLINE")) != "" {
		return true
	}
	v := strings.ToLower(strings.TrimSpace(os.Getenv("PIG_OFFLINE")))
	return v == "1" || v == "true" || v == "yes"
}

// PackageUpdate describes an available update for a configured package.
// Mirrors upstream PackageUpdate (package-manager.ts).
type PackageUpdate struct {
	Source      string // configured source string
	DisplayName string // human-readable name (npm package name or git org/repo)
	Type        string // "npm" or "git"
	Scope       string // "user" or "project"
}

// CheckForAvailableUpdates queries npm registry and git remotes for
// available updates to installed packages. Mirrors upstream
// DefaultPackageManager.checkForAvailableUpdates (package-manager.ts:1114).
// Returns empty slice when offline mode is enabled.
func CheckForAvailableUpdates(cwd string, sm *codingagent.SettingsManager) []PackageUpdate {
	if IsOfflineModeEnabled() {
		return nil
	}
	pkgs := configuredPackagesForResolution(cwd, sm)
	if len(pkgs) == 0 {
		return nil
	}

	type result struct {
		update *PackageUpdate
	}

	sem := make(chan struct{}, updateCheckConcurrency)
	results := make([]result, len(pkgs))
	var wg sync.WaitGroup

	for i, pkg := range pkgs {
		wg.Add(1)
		go func(idx int, p configuredPackage) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			source := p.Source.Source
			installed := p.InstalledPath
			if installed == "" {
				return
			}
			kind := detectSourceKind(source)
			// Skip pinned versions (any explicit @suffix). Mirrors upstream
			// parseSource + checkForAvailableUpdates.
			if kind == "npm" && isPinnedNpm(source) {
				return
			}
			switch kind {
			case "npm":
				name, _ := parseNpmSpec(strings.TrimSpace(strings.TrimPrefix(source, "npm:")))
				if name == "" {
					return
				}
				if npmHasAvailableUpdate(installed, name) {
					results[idx] = result{update: &PackageUpdate{
						Source:      source,
						DisplayName: name,
						Type:        "npm",
						Scope:       p.Scope,
					}}
				}
			case "git":
				url := strings.TrimPrefix(source, "git:")
				if gitHasAvailableUpdate(installed) {
					results[idx] = result{update: &PackageUpdate{
						Source:      source,
						DisplayName: gitDisplayName(url),
						Type:        "git",
						Scope:       p.Scope,
					}}
				}
			}
		}(i, pkg)
	}
	wg.Wait()

	var updates []PackageUpdate
	for _, r := range results {
		if r.update != nil {
			updates = append(updates, *r.update)
		}
	}
	return updates
}

// isPinnedNpm returns true for any npm source with an explicit version suffix.
// Mirrors upstream parseSource: any `npm:name@something` sets pinned=true.
func isPinnedNpm(source string) bool {
	_, version := parseNpmSpec(strings.TrimPrefix(source, "npm:"))
	return version != ""
}

// parseNpmSpec splits "@scope/name@1.2.3" into ("@scope/name", "1.2.3").
// Mirrors upstream parseNpmSpec (package-manager.ts:1637).
func parseNpmSpec(spec string) (name, version string) {
	m := regexp.MustCompile(`^(@?[^@]+(?:/[^@]+)?)(?:@(.+))?$`).FindStringSubmatch(spec)
	if len(m) == 0 {
		return spec, ""
	}
	return m[1], m[2]
}

// npmHasAvailableUpdate compares the installed package.json version
// against `npm view <name> version`. Returns true if the latest
// published version differs from the installed version.
func npmHasAvailableUpdate(installedPath, packageName string) bool {
	installedVer := readInstalledNpmVersion(installedPath)
	if installedVer == "" {
		return false
	}
	latest := fetchLatestNpmVersion(packageName)
	if latest == "" {
		return false
	}
	return installedVer != latest
}

func readInstalledNpmVersion(installedPath string) string {
	data, err := os.ReadFile(filepath.Join(installedPath, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return ""
	}
	return pkg.Version
}

func fetchLatestNpmVersion(packageName string) string {
	cmd := crossspawn.Command(context.Background(), "npm", "view", packageName, "version", "--json")
	cmd.Env = append(os.Environ(), "NPM_CONFIG_FUND=false", "NPM_CONFIG_AUDIT=false")
	out, err := runWithTimeout(cmd, updateCheckNetworkTimeout)
	if err != nil {
		return ""
	}
	raw := strings.TrimSpace(out)
	if raw == "" {
		return ""
	}
	var version string
	if err := json.Unmarshal([]byte(raw), &version); err != nil {
		return ""
	}
	return version
}

// gitHasAvailableUpdate compares local HEAD against remote HEAD via
// `git ls-remote`. Mirrors upstream gitHasAvailableUpdate.
func gitHasAvailableUpdate(installedPath string) bool {
	localCmd := exec.Command("git", "rev-parse", "HEAD")
	localCmd.Dir = installedPath
	localOut, err := runWithTimeout(localCmd, updateCheckNetworkTimeout)
	if err != nil {
		return false
	}
	localHead := strings.TrimSpace(localOut)

	// Try upstream ref first; fall back to HEAD.
	upstreamCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "@{upstream}")
	upstreamCmd.Dir = installedPath
	upstreamOut, _ := runWithTimeout(upstreamCmd, updateCheckNetworkTimeout)
	upstreamRef := strings.TrimSpace(upstreamOut)

	var remoteHead string
	if upstreamRef != "" {
		remoteCmd := exec.Command("git", "ls-remote", "origin", upstreamRef)
		remoteCmd.Dir = installedPath
		out, err := runWithTimeout(remoteCmd, updateCheckNetworkTimeout)
		if err == nil {
			if m := regexp.MustCompile(`(?m)^([0-9a-f]{40})\s+`).FindStringSubmatch(out); len(m) > 1 {
				remoteHead = m[1]
			}
		}
	}
	if remoteHead == "" {
		remoteCmd := exec.Command("git", "ls-remote", "origin", "HEAD")
		remoteCmd.Dir = installedPath
		out, err := runWithTimeout(remoteCmd, updateCheckNetworkTimeout)
		if err != nil {
			return false
		}
		if m := regexp.MustCompile(`(?m)^([0-9a-f]{40})\s+HEAD$`).FindStringSubmatch(out); len(m) > 1 {
			remoteHead = m[1]
		}
	}
	if remoteHead == "" {
		return false
	}
	return localHead != remoteHead
}

// gitDisplayName turns a git URL into "org/repo".
func gitDisplayName(url string) string {
	clean := strings.TrimSuffix(url, ".git")
	if idx := strings.LastIndex(clean, "://"); idx >= 0 {
		clean = clean[idx+3:]
	}
	// Drop "host/" prefix if present.
	parts := strings.SplitN(clean, "/", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return clean
}

// runWithTimeout runs a command and returns stdout, killing the process
// if it exceeds the timeout.
func runWithTimeout(cmd *exec.Cmd, timeout time.Duration) (string, error) {
	type result struct {
		out string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		out, err := cmd.Output()
		ch <- result{string(out), err}
	}()
	select {
	case r := <-ch:
		return r.out, r.err
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		return "", errors.New("command timeout")
	}
}

// FormatPackageUpdates formats a list of available updates for display.
// Mirrors upstream showPackageUpdateNotification's body.
func FormatPackageUpdates(updates []PackageUpdate) string {
	if len(updates) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Package Updates Available\n")
	b.WriteString("Package updates are available. Run `pig update`\n")
	b.WriteString("Packages:\n")
	for _, u := range updates {
		fmt.Fprintf(&b, "- %s\n", u.DisplayName)
	}
	return strings.TrimRight(b.String(), "\n")
}

// EnsureConfiguredPackagesInstalled reinstalls any configured packages
// whose install directory is missing on disk. Mirrors upstream's
// onMissing callback flow in DefaultPackageManager.resolve
// (package-manager.ts:839). Skipped in offline mode.
//
// Returns the list of source strings that were reinstalled, and a list
// of source strings that could not be reinstalled (e.g. network error,
// offline mode).
func EnsureConfiguredPackagesInstalled(cwd string, sm *codingagent.SettingsManager) (reinstalled, missing []string) {
	packages := configuredPackagesForResolution(cwd, sm)
	if IsOfflineModeEnabled() {
		for _, pkg := range packages {
			if pkg.InstalledPath == "" {
				missing = append(missing, pkg.Source.Source)
			}
		}
		return nil, missing
	}
	for _, pkg := range packages {
		if pkg.InstalledPath != "" {
			continue
		}
		local := pkg.Scope == "project"
		source := pkg.Source
		if detectSourceKind(source.Source) == "local" {
			resolved, err := resolveLocalPackageRoot(settingsBaseDirForManager(sm, local), source.Source)
			if err != nil {
				missing = append(missing, source.Source)
				continue
			}
			source.Source = resolved
		}
		if err := installPackageArtifacts(cwd, source, local, nil); err != nil {
			missing = append(missing, pkg.Source.Source)
			continue
		}
		reinstalled = append(reinstalled, pkg.Source.Source)
	}
	return reinstalled, missing
}
