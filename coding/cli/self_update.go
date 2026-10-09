//go:build !pig_strip_self_update

package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// This file is the boundary a Piglet Binary compiles out with the
// pig_strip_self_update tag: self_update_stripped.go replaces it.

// selfUpdateTimeout bounds the whole self-update (manifest fetch + binary
// download). Generous because a release binary can be tens of megabytes.
const selfUpdateTimeout = 5 * time.Minute

// errUpdateNotNeeded signals that the standalone tier found no newer release.
// It is not a failure: runSelfUpdate treats it as success.
var errUpdateNotNeeded = errors.New("up to date")

// runSelfUpdate applies one proven installation tier.
// pig divergence (D39): a started tier never falls through to another.
func runSelfUpdate(force bool) int {
	// pig additive (D92): a Piglet that strips self-update refuses, as its Binary does.
	if pigstrip.Has(pigstrip.ListFeatures, pigstrip.SelfUpdate) {
		return reportStrippedFeature("self-update", pigstrip.SelfUpdate, selfUpdateStrippedHint)
	}
	if code, done := checkSelfUpdateVersion(force); done {
		return code
	}
	cwd, _ := os.Getwd()
	settings := codingagent.NewSettingsManager(cwd, codingagent.AgentDir())
	npmCommand := settings.GetGlobalSettings().NpmCommand
	result := codingagent.ApplySelfUpdateTier(
		func(exePath string) error { return applyStandaloneUpdate(exePath, force) },
		func(prov *codingagent.SelfUpdateProvenance) error {
			return applyPackageManagerUpdate(prov, npmCommand, force)
		},
	)
	switch result.Action {
	case "standalone-updated", "package-manager-updated":
		// pig additive (D109): with extensionsAutoUpgrade on, the installed pig upgrades extensions written for an older SDK.
		upgradeExtensionsAfterUpdate(settings.GetExtensionsAutoUpgrade(), runUpdatedExtensionUpgrade)
		return 0
	case "refused":
		// Immutable/container/unsupported/ambiguous: the tier refused mutation
		// and the message is the exact remediation.
		fmt.Fprintln(os.Stderr, result.Message)
		return 1
	default:
		// A started tier failed (standalone-failed, package-manager-failed) or
		// resolution errored. Surface it without fallthrough. "Up to date" is
		// success, not a failure.
		if errors.Is(result.Cause, errUpdateNotNeeded) {
			return 0
		}
		fmt.Fprintf(os.Stderr, "Update failed: %s\n", result.Message)
		return 1
	}
}

// binaryUpdateChecker is the interactive startup check for a newer standalone
// release, or nil when a Piglet strips self-update (no new-version notice).
// pig divergence (D39): standalone-binary self-update notice at startup.
func binaryUpdateChecker() func() *codingagent.BinaryUpdate {
	// pig additive (D92): a Piglet that strips self-update shows no notice, as its Binary does.
	if pigstrip.Has(pigstrip.ListFeatures, pigstrip.SelfUpdate) {
		return nil
	}
	return func() *codingagent.BinaryUpdate {
		if packagemanager.IsOfflineModeEnabled() {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		return codingagent.CheckForBinaryUpdate(ctx, &http.Client{Timeout: 6 * time.Second}, selfUpdateVersion())
	}
}

// checkSelfUpdateVersion checks the release before the installation tier, as
// upstream getSelfUpdatePlan runs before any install-method refusal: an
// installation that is already current exits 0 even when pig cannot update
// it, and an unreachable source fails the update. It reports done when the
// update ends here. Without a configured source there is nothing to check and
// the tier explains that.
func checkSelfUpdateVersion(force bool) (code int, done bool) {
	src := codingagent.UpdateSourceURL()
	if src == "" || force {
		return 0, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), selfUpdateTimeout)
	defer cancel()
	manifest, err := codingagent.FetchUpdateManifest(ctx, &http.Client{Timeout: selfUpdateTimeout}, src, codingagent.FetchUpdateManifestOptions{Retry: true})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Update failed: could not reach the update source: %v\n%s\n", err, codingagent.SelfUpdateFallback())
		return 1, true
	}
	if !selfUpdatePlanRuns(manifest, false) {
		fmt.Fprintf(os.Stderr, "%s %s is up to date.\n", codingagent.AppName, selfUpdateVersion())
		return 0, true
	}
	return 0, false
}

// selfUpdatePlanRuns mirrors upstream getSelfUpdatePlan's shouldRun: the
// update runs when forced, when the release names another package than this
// one, or when the release is newer.
func selfUpdatePlanRuns(manifest *codingagent.UpdateManifest, force bool) bool {
	return force || strings.TrimSpace(manifest.PackageName) != codingagent.PackageName ||
		codingagent.IsNewerPackageVersion(manifest.Version, selfUpdateVersion())
}

func applyPackageManagerUpdate(prov *codingagent.SelfUpdateProvenance, npmCommand []string, force bool) error {
	src := codingagent.UpdateSourceURL()
	if src == "" {
		return fmt.Errorf("no update source configured\n%s", codingagent.SelfUpdateFallback())
	}
	ctx, cancel := context.WithTimeout(context.Background(), selfUpdateTimeout)
	defer cancel()
	client := &http.Client{Timeout: selfUpdateTimeout}
	manifest, err := codingagent.FetchUpdateManifest(ctx, client, src, codingagent.FetchUpdateManifestOptions{Retry: true})
	if err != nil {
		return fmt.Errorf("could not reach the update source: %w", err)
	}
	if !selfUpdatePlanRuns(manifest, force) {
		fmt.Fprintf(os.Stderr, "%s %s is up to date.\n", codingagent.AppName, selfUpdateVersion())
		return errUpdateNotNeeded
	}
	updateName := strings.TrimSpace(manifest.PackageName)
	target := codingagent.SelfUpdatePackageTarget{PackageName: updateName, InstallSpec: updateName + "@" + manifest.Version}
	cmd := prov.GetSelfUpdateCommand(npmCommand, target)
	if cmd == nil {
		return fmt.Errorf("no update command for package manager %s", prov.PackageOwner)
	}
	if prov.PackageOwner == "npm" {
		if err := codingagent.PrepareWindowsNpmSelfUpdate(ctx, prov.ExePath); err != nil {
			return err
		}
	}
	if err := codingagent.RunPackageManagerUpdate(cmd); err != nil {
		return err
	}
	printSelfUpdateSuccess(manifest.Version, manifest.Notes)
	return nil
}

// applyStandaloneUpdate is the standalone-binary tier: fetch the manifest from
// the configured source, compare versions, download, verify SHA256 and size,
// and atomically replace the executable. It is invoked only after tier
// resolution proves a writable standalone binary; its failures surface from
// this tier with no fallback.
func applyStandaloneUpdate(exePath string, force bool) error {
	src := codingagent.UpdateSourceURL()
	if src == "" {
		return fmt.Errorf("no update source configured\n%s", codingagent.SelfUpdateFallback())
	}
	ctx, cancel := context.WithTimeout(context.Background(), selfUpdateTimeout)
	defer cancel()
	client := &http.Client{Timeout: selfUpdateTimeout}

	manifest, err := codingagent.FetchUpdateManifest(ctx, client, src, codingagent.FetchUpdateManifestOptions{Retry: true})
	if err != nil {
		return fmt.Errorf("could not reach the update source: %w\n%s", err, codingagent.SelfUpdateFallback())
	}
	if !force && !codingagent.IsNewerPackageVersion(manifest.Version, selfUpdateVersion()) {
		fmt.Fprintf(os.Stderr, "%s %s is up to date.\n", codingagent.AppName, selfUpdateVersion())
		return errUpdateNotNeeded
	}
	bin, ok := manifest.PlatformBinary()
	if !ok {
		return fmt.Errorf("%s %s is available but ships no binary for %s/%s\n%s",
			codingagent.AppName, manifest.Version, runtime.GOOS, runtime.GOARCH, codingagent.SelfUpdateFallback())
	}
	fmt.Fprintf(os.Stderr, "Updating %s %s → %s...\n", codingagent.AppName, selfUpdateVersion(), manifest.Version)
	if err := codingagent.SelfReplaceAtWithCommit(ctx, client, bin, exePath, func() error {
		return codingagent.WriteStandaloneReceipt(exePath, manifest.Version, src)
	}); err != nil {
		return fmt.Errorf("%w\n%s", err, codingagent.SelfUpdateFallback())
	}
	printSelfUpdateSuccess(manifest.Version, manifest.Notes)
	return nil
}

func printSelfUpdateSuccess(version, notes string) {
	fmt.Fprintf(os.Stderr, "Updated to %s %s.\n", codingagent.AppName, version)
	if notes = strings.TrimSpace(notes); notes != "" {
		fmt.Fprintln(os.Stderr, notes)
	}
	// The replaced binary carries its own embedded SDK, which only it can
	// stage; this process still holds the previous bytes, so staging here
	// would write the old SDK back. The next start stages the new one and
	// every source extension rebuilds against it, which is slow enough to
	// read as a hang if unannounced.
	fmt.Fprintln(os.Stderr, "Source extensions rebuild on next start; the first launch will be slower.")
}
