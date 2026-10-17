package pigversion

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The constants are generated from .upstream/pin (automation/pin/pin.py). A run
// that overrides the pin with PI_UPSTREAM_REF builds with an overlay of the
// generated file, so only the relations between the constants are asserted here;
// the file-level match is `make pin-check`.
func TestUpstreamConstantsAreConsistent(t *testing.T) {
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(UpstreamCommit) {
		t.Fatalf("UpstreamCommit = %q, want 40 lowercase hex digits", UpstreamCommit)
	}
	wantVersion := UpstreamPackageVersion
	if !regexp.MustCompile(`^\d+\.\d+\.\d+`).MatchString(UpstreamPackageVersion) {
		t.Fatalf("UpstreamPackageVersion = %q is not a release version", UpstreamPackageVersion)
	}
	if UpstreamRef == UpstreamCommit {
		wantVersion += "+" + UpstreamCommit[:8]
	} else if UpstreamRef != "v"+UpstreamPackageVersion {
		t.Fatalf("UpstreamRef = %q is neither the tag of %s nor the commit", UpstreamRef, UpstreamPackageVersion)
	}
	if UpstreamVersion != wantVersion {
		t.Fatalf("UpstreamVersion = %q, want %q for ref %q", UpstreamVersion, wantVersion, UpstreamRef)
	}
}

// Semver allows one plus sign: a commit pin's version joins the package version and
// the commit with a dot inside the single build-metadata field.
func TestVersionHasOneBuildMetadataField(t *testing.T) {
	want := PigVersion + "+" + strings.ReplaceAll(UpstreamVersion, "+", ".")
	if Version != want {
		t.Fatalf("Version = %q, want %q", Version, want)
	}
	if strings.Count(Version, "+") != 1 {
		t.Fatalf("Version %q has more than one plus sign", Version)
	}
}

// The committed pin file agrees with the constants unless a run overrides the pin.
func TestCommittedPinFileMatchesConstants(t *testing.T) {
	if os.Getenv("PIG_UPSTREAM_OVERRIDE") == "true" || os.Getenv("PI_UPSTREAM_REF") != "" {
		t.Skip("this run overrides the pin")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".upstream", "pin"))
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for line := range strings.SplitSeq(string(data), "\n") {
		if key, value, ok := strings.Cut(strings.TrimSpace(line), "="); ok && !strings.HasPrefix(key, "#") {
			values[key] = value
		}
	}
	for key, got := range map[string]string{"ref": UpstreamRef, "commit": UpstreamCommit, "package_version": UpstreamPackageVersion, "version": UpstreamVersion} {
		if values[key] != got {
			t.Errorf(".upstream/pin %s = %q, pigversion.go has %q (run make pin-sync)", key, values[key], got)
		}
	}
}
