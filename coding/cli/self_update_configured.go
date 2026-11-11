//go:build !pig_strip_self_update

package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/crossspawn"
	"github.com/MichaelKinsy/PiG/internal/installchange"
)

// runConfiguredSelfUpdate runs the user-configured self-update command for the signed release, then reports the version
// installed. Ports package-manager-cli.ts runConfiguredSelfUpdate (Pi 8bc94b19).
// pig divergence (D39): the release comes from the signed update manifest, not pi.dev; without a source there is no
// release to fill the placeholders from, so nothing runs.
func runConfiguredSelfUpdate(argv []string, force bool) int {
	src := codingagent.UpdateSourceURL()
	if src == "" {
		fmt.Fprintf(os.Stderr, "Update failed: no update source configured\n%s\n", codingagent.SelfUpdateFallback())
		return 1
	}
	//portlint:allow ctxbackground Pi's runConfiguredSelfUpdate (package-manager-cli.ts:745) takes no AbortSignal: the update runs to its own end
	ctx, cancel := context.WithTimeout(context.Background(), selfUpdateTimeout)
	defer cancel()
	manifest, err := codingagent.FetchUpdateManifest(ctx, &http.Client{Timeout: selfUpdateTimeout}, src, codingagent.FetchUpdateManifestOptions{Retry: true})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Update failed: could not reach the update source: %v\n%s\n", err, codingagent.SelfUpdateFallback())
		return 1
	}
	if !selfUpdatePlanRuns(manifest, force) {
		fmt.Fprintf(os.Stderr, "%s %s is up to date.\n", codingagent.AppName, selfUpdateVersion())
		return 0
	}
	// FetchUpdateManifest rejects an empty package name, so the release always names its package.
	command := codingagent.MakeConfiguredSelfUpdateCommand(argv, codingagent.SelfUpdateRelease{PackageName: manifest.PackageName, Version: manifest.Version})
	if note := strings.TrimSpace(manifest.Notes); note != "" {
		fmt.Println()
		fmt.Println(chalkBold(chalkYellow("Update note")))
		fmt.Println(note)
		fmt.Println()
	}
	fmt.Println(chalkDim(fmt.Sprintf("Updating %s with %s...", codingagent.AppName, command.Display)))
	//portlint:allow ctxbackground Pi's runConfiguredSelfUpdate (package-manager-cli.ts:745) takes no AbortSignal: the update runs to its own end
	if err := codingagent.RunConfiguredSelfUpdateCommand(context.Background(), command); err != nil {
		fmt.Fprintln(os.Stderr, chalkRed("Error: "+err.Error()))
		return 1
	}
	// Exit code 0 does not prove an update. A wrapper may install each version into a new directory and leave this
	// executable untouched, so fall back to the pig on PATH.
	installed := installedVersionAfterUpdate(manifest.Version)
	if sameRelease(installed, manifest.Version) {
		fmt.Println(chalkGreen(fmt.Sprintf("Updated %s from %s to %s", codingagent.AppName, selfUpdateVersion(), manifest.Version)))
		return 0
	}
	reported := installed
	if reported == "" {
		reported = "no version"
	}
	fmt.Println(chalkYellow(fmt.Sprintf("Ran %s, but `%s --version` reports %s, not %s.", command.Display, codingagent.AppName, reported, manifest.Version)))
	return 0
}

func sameRelease(installed, want string) bool {
	order, ok := codingagent.ComparePackageVersions(installed, want)
	return ok && order == 0
}

// installedVersionAfterUpdate is the release version when the command replaced this executable in place and the
// replaced executable reports it, else the version `pig --version` prints from PATH, the pig the user runs next. Empty
// when neither answers. Ports package-manager-cli.ts runConfiguredSelfUpdate: the in-place result counts only when it
// equals the release.
func installedVersionAfterUpdate(want string) string {
	if change := installchange.Detect(); change != nil && change.Kind == installchange.BinaryReplaced {
		if version := readVersionOf(change.Path); sameRelease(version, want) {
			return version
		}
	}
	path, err := exec.LookPath(codingagent.AppName)
	if err != nil {
		return ""
	}
	return readVersionOf(path)
}

// readVersionOf runs `path --version`. The package directory overrides name this installation and would make the new
// pig report the old version; PIG_VERSION_FORMAT (D63) would make it print the pinned Pi version, which never equals a
// PiG release, while self-update comparisons use PiG's own version.
func readVersionOf(path string) string {
	var env []string
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "PIG_PACKAGE_DIR="), strings.HasPrefix(kv, "PI_PACKAGE_DIR="), strings.HasPrefix(kv, "PIG_VERSION_FORMAT="):
		default:
			env = append(env, kv)
		}
	}
	//portlint:allow ctxbackground Pi's readVersionOnPath (package-manager-cli.ts:775) is a synchronous spawn with no AbortSignal; the timeout is pig's bound on it
	ctx, cancel := context.WithTimeout(context.Background(), selfUpdateTimeout)
	defer cancel()
	cmd := crossspawn.Command(ctx, "", filepath.Clean(path), "--version")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
