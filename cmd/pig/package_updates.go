package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/crossspawn"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// updateCheckConcurrency limits parallel npm/git remote queries.
const updateCheckConcurrency = 5

// PackageUpdate describes an available update for a configured package.
// Mirrors upstream PackageUpdate (package-manager.ts).
type PackageUpdate struct {
	Source      string // configured source string
	DisplayName string // human-readable name (npm package name or git org/repo)
	Type        string // "npm" or "git"
	Scope       string // "user" or "project"
}

// CheckForAvailableUpdates queries npm registry and git remotes for updates to installed, unpinned packages with at most five joined workers. It returns a non-nil slice, including when offline or no updates are available.
// Mirrors upstream DefaultPackageManager.checkForAvailableUpdates (packages/coding-agent/src/core/package-manager.ts:1186-1253).
// User-package metadata uses managed storage (D79); trusted project packages use cwd.
func CheckForAvailableUpdates(cwd string, sm *codingagent.SettingsManager) []PackageUpdate {
	if packagemanager.IsOfflineModeEnabled() {
		return []PackageUpdate{}
	}
	pkgs := packagemanager.ConfiguredPackagesForResolution(cwd, sm.AgentDir(), sm)
	if len(pkgs) == 0 {
		return []PackageUpdate{}
	}

	type result struct {
		update *PackageUpdate
	}

	results := make([]result, len(pkgs))
	check := func(idx int) {
		p := pkgs[idx]

		source := p.Source.Source
		installed := p.InstalledPath
		if installed == "" {
			return
		}
		kind := packagemanager.DetectSourceKind(source)
		// Exact npm versions are fixed; tags and ranges remain eligible for metadata lookup.
		if kind == "npm" && isPinnedNpm(source) {
			return
		}
		switch kind {
		case "npm":
			ref, err := packagemanager.ParseNpmInstallRef(source)
			if err != nil {
				return
			}
			if npmHasAvailableUpdate(cwd, sm, ref, installed, p.Scope == "project") {
				results[idx] = result{update: &PackageUpdate{
					Source:      source,
					DisplayName: ref.NPMName,
					Type:        "npm",
					Scope:       p.Scope,
				}}
			}
		case "git":
			ref, ok := parseGitPackageSource(source)
			if !ok || ref.pinned {
				return
			}
			if gitHasAvailableUpdate(installed) {
				results[idx] = result{update: &PackageUpdate{
					Source:      source,
					DisplayName: ref.host + "/" + ref.path,
					Type:        "git",
					Scope:       p.Scope,
				}}
			}
		}
	}
	// upstream: packages/coding-agent/src/core/package-manager.ts:runWithConcurrency
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(updateCheckConcurrency, len(pkgs)) {
		wg.Go(func() {
			for idx := range jobs {
				check(idx)
			}
		})
	}
	for idx := range pkgs {
		jobs <- idx
	}
	close(jobs)
	wg.Wait()

	updates := []PackageUpdate{}
	for _, r := range results {
		if r.update != nil {
			updates = append(updates, *r.update)
		}
	}
	return updates
}

// isPinnedNpm reports whether a source specifies an exact semantic version, rather than a mutable tag or range.
func isPinnedNpm(source string) bool {
	ref, err := packagemanager.ParseNpmInstallRef(source)
	if err != nil {
		return false
	}
	return isExactNpmVersion(ref.NPMVer)
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

// gitHasAvailableUpdate compares local HEAD against remote HEAD via
// `git ls-remote`. Mirrors upstream gitHasAvailableUpdate.
func gitHasAvailableUpdate(installedPath string) bool {
	if packagemanager.IsOfflineModeEnabled() {
		return false
	}
	localHead, err := runWithTimeout(gitCaptureCommand(installedPath, "rev-parse", "HEAD"), packagemanager.UpdateCheckNetworkTimeout)
	if err != nil {
		return false
	}
	remoteHead, err := getRemoteGitHead(installedPath)
	return err == nil && strings.TrimSpace(localHead) != strings.TrimSpace(remoteHead)
}

func getRemoteGitHead(installedPath string) (string, error) {
	if upstreamRef := getGitUpstreamRef(installedPath); upstreamRef != "" {
		out, err := runGitRemoteCommand(installedPath, "ls-remote", "origin", upstreamRef)
		if err != nil {
			return "", err
		}
		if match := regexp.MustCompile(`(?m)^([0-9a-f]{40})\s+`).FindStringSubmatch(out); len(match) > 1 {
			return match[1], nil
		}
	}
	out, err := runGitRemoteCommand(installedPath, "ls-remote", "origin", "HEAD")
	if err != nil {
		return "", err
	}
	if match := regexp.MustCompile(`(?m)^([0-9a-f]{40})\s+HEAD$`).FindStringSubmatch(out); len(match) > 1 {
		return match[1], nil
	}
	return "", fmt.Errorf("Failed to determine remote HEAD")
}

func getGitUpstreamRef(installedPath string) string {
	out, err := runWithTimeout(gitCaptureCommand(installedPath, "rev-parse", "--abbrev-ref", "@{upstream}"), packagemanager.UpdateCheckNetworkTimeout)
	if err != nil {
		return ""
	}
	branch, ok := strings.CutPrefix(strings.TrimSpace(out), "origin/")
	if !ok || branch == "" {
		return ""
	}
	return "refs/heads/" + branch
}

// runGitRemoteCommand disables interactive credential prompts only for this remote query.
func runGitRemoteCommand(installedPath string, args ...string) (string, error) {
	cmd := gitCaptureCommand(installedPath, args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return runWithTimeout(cmd, packagemanager.UpdateCheckNetworkTimeout)
}

// gitCaptureCommand starts git in installedPath as Pi's spawnCaptureCommand does, through spawnProcess, so a Windows git.cmd shim receives its arguments escaped for cmd.exe.
func gitCaptureCommand(installedPath string, args ...string) *exec.Cmd {
	return crossspawn.Command(context.Background(), installedPath, "git", args...)
}

// runWithTimeout captures a command and waits for child/output cleanup before returning, including after a timeout.
// Mirrors packages/coding-agent/src/core/package-manager.ts:runCommandCapture.
func runWithTimeout(cmd *exec.Cmd, timeout time.Duration) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var err error
	select {
	case err = <-done:
	case <-timer.C:
		packagemanager.TerminatePackageCapture(cmd.Process)
		<-done
		return "", fmt.Errorf("%s timed out after %dms", strings.Join(cmd.Args, " "), timeout.Milliseconds())
	}
	if err != nil {
		if cmd.ProcessState == nil {
			return "", err
		}
		diagnostic := stderr.String()
		if diagnostic == "" {
			diagnostic = stdout.String()
		}
		return "", fmt.Errorf("%s failed with %s: %s", strings.Join(cmd.Args, " "), packagemanager.PackageCaptureExitStatus(cmd.ProcessState), diagnostic)
	}
	return strings.TrimSpace(stdout.String()), nil
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
