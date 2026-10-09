package packagemanager

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/crossspawn"
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

// IsPinnedNpm reports whether a source specifies an exact semantic version, rather than a mutable tag or range.
func IsPinnedNpm(source string) bool {
	ref, err := ParseNpmInstallRef(source)
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

// GitHasAvailableUpdate compares local HEAD against remote HEAD via
// `git ls-remote`. Mirrors upstream GitHasAvailableUpdate.
func GitHasAvailableUpdate(installedPath string) bool {
	if IsOfflineModeEnabled() {
		return false
	}
	localHead, err := RunWithTimeout(gitCaptureCommand(installedPath, "rev-parse", "HEAD"), UpdateCheckNetworkTimeout)
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
	out, err := RunWithTimeout(gitCaptureCommand(installedPath, "rev-parse", "--abbrev-ref", "@{upstream}"), UpdateCheckNetworkTimeout)
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
	return RunWithTimeout(cmd, UpdateCheckNetworkTimeout)
}

// gitCaptureCommand starts git in installedPath as Pi's spawnCaptureCommand does, through spawnProcess, so a Windows git.cmd shim receives its arguments escaped for cmd.exe.
func gitCaptureCommand(installedPath string, args ...string) *exec.Cmd {
	return crossspawn.Command(context.Background(), installedPath, "git", args...)
}

// RunWithTimeout captures a command and waits for child/output cleanup before returning, including after a timeout.
// Mirrors packages/coding-agent/src/core/package-manager.ts:runCommandCapture.
func RunWithTimeout(cmd *exec.Cmd, timeout time.Duration) (string, error) {
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
		TerminatePackageCapture(cmd.Process)
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
		return "", fmt.Errorf("%s failed with %s: %s", strings.Join(cmd.Args, " "), PackageCaptureExitStatus(cmd.ProcessState), diagnostic)
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
