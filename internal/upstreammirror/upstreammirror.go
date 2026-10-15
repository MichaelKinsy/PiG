// Package upstreammirror checks that a checkout's Pi source mirror, .upstream/current, is the Pi build a ledger version names.
// Parity tests and the ledger generators read Pi's source from that mirror and trust it: a missing or stale mirror silently
// validates PiG against the wrong Pi (a 0.79.4 mirror hid that session_info_changed graduated in 0.80.3; an interface ledger
// update without the mirror reopens every row whose rule reads Pi's source).
package upstreammirror

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// Fix is the command that points .upstream/current at the pinned build.
const Fix = "run `make upstream-mirror` (automation/gen/mirror-upstream.sh points .upstream/current at the pinned build; --force replaces a stale one), or link the shared mirror into a lane worktree with automation/dev/wt-provision.sh <worktree>"

// Check returns nil when root/.upstream/current is the mirror of the Pi build version: its provenance marker
// (.pig-upstream-source.json, which automation/gen/mirror-upstream.sh writes only after a complete extraction) records version, and
// its coding-agent package.json reports version's package version (the part before a commit pin's +<commit>). For the pinned version
// (pigversion.UpstreamVersion) the marker must also record the pinned tag and commit, and package.json the pinned package version.
// The error names what the mirror holds and the fix.
func Check(root, version string) error {
	current := filepath.Join(root, ".upstream", "current")
	pkgVersion, _, _ := strings.Cut(version, "+")
	ref, commit := "", ""
	if version == pigversion.UpstreamVersion {
		pkgVersion, ref, commit = pigversion.UpstreamPackageVersion, pigversion.UpstreamRef, pigversion.UpstreamCommit
	}
	pkgJSON := filepath.Join(current, "packages", "coding-agent", "package.json")
	var pkg struct {
		Version string `json:"version"`
	}
	if err := readJSON(pkgJSON, &pkg); err != nil {
		return fmt.Errorf("the Pi %s mirror is missing at %s (%w); %s", version, current, err, Fix)
	}
	if pkg.Version != pkgVersion {
		return fmt.Errorf("the mirror at %s is Pi %s (%s), not Pi %s; %s", current, pkg.Version, pkgJSON, pkgVersion, Fix)
	}
	markerPath := filepath.Join(current, ".pig-upstream-source.json")
	var marker struct {
		Version string `json:"version"`
		Tag     string `json:"tag"`
		Commit  string `json:"commit"`
	}
	if err := readJSON(markerPath, &marker); err != nil {
		return fmt.Errorf("the mirror at %s has no provenance (%w); %s", current, err, Fix)
	}
	if marker.Version != version || ref != "" && (marker.Tag != ref || marker.Commit != commit) {
		want := version
		if ref != "" {
			want = version + "@" + ref + "@" + commit
		}
		return fmt.Errorf("the mirror at %s is Pi %s@%s@%s, not Pi %s; %s", current, marker.Version, marker.Tag, marker.Commit, want, Fix)
	}
	return nil
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}
