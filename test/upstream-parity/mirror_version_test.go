package parity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

// TestUpstreamMirror_MatchesPin fails loudly when the local `.upstream/current`
// mirror does not match the pinned coding.UpstreamVersion and coding.UpstreamCommit.
// Every other parity test reads types.ts from that mirror and trusts it; a
// stale mirror silently validates pig against the wrong upstream version and
// (observed: a 0.79.4 mirror hid that session_info_changed graduated to
// upstream in 0.80.3).
// This guard turns that silent drift into an explicit failure with the fix.
func TestUpstreamMirror_MatchesPin(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine caller path")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	pkgJSON := filepath.Join(repoRoot, ".upstream", "current",
		"packages", "coding-agent", "package.json")
	raw, err := os.ReadFile(pkgJSON)
	if err != nil {
		t.Fatalf("mirror missing at %s; run automation/gen/mirror-upstream.sh: %v", pkgJSON, err)
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		t.Fatalf("parse %s: %v", pkgJSON, err)
	}
	if pkg.Version != coding.UpstreamVersion {
		t.Fatalf("mirror version %q != pinned coding.UpstreamVersion %q; "+
			"parity results are against the wrong upstream. Refresh with "+
			"`automation/gen/mirror-upstream.sh --force`.", pkg.Version, coding.UpstreamVersion)
	}
	markerPath := filepath.Join(repoRoot, ".upstream", "current", ".pig-upstream-source.json")
	markerRaw, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("mirror provenance missing at %s; run automation/gen/mirror-upstream.sh --force: %v", markerPath, err)
	}
	var marker struct {
		Version string `json:"version"`
		Tag     string `json:"tag"`
		Commit  string `json:"commit"`
	}
	if err := json.Unmarshal(markerRaw, &marker); err != nil {
		t.Fatalf("parse %s: %v", markerPath, err)
	}
	if marker.Version != coding.UpstreamVersion || marker.Tag != "v"+coding.UpstreamVersion || marker.Commit != coding.UpstreamCommit {
		t.Fatalf("mirror provenance %s@%s@%s != pinned %s@v%s@%s; refresh with automation/gen/mirror-upstream.sh --force", marker.Version, marker.Tag, marker.Commit, coding.UpstreamVersion, coding.UpstreamVersion, coding.UpstreamCommit)
	}
}
