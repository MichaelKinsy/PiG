package ciimages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseCandidatePinsPythonForSBOMValidation(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release-candidate.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	if got := strings.Count(workflow, "uses: actions/setup-python@"); got != 4 {
		t.Fatalf("release candidate setup-python steps = %d, want 4", got)
	}
	if got := strings.Count(workflow, "python-version: '3.12'"); got != 4 {
		t.Fatalf("release candidate Python 3.12 pins = %d, want 4", got)
	}
	if got := strings.Count(workflow, "python automation/release/validate-sbom.py"); got != 3 {
		t.Fatalf("release candidate SBOM validation commands = %d, want 3", got)
	}
}

func TestReleaseCandidateVerifiesPortableChecksumsAndNativeArchives(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release-candidate.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	for _, want := range []string{
		"native-smoke:",
		"needs: source",
		"name: pig-${{ inputs.version }}-source",
		"SOURCE_ROOT: ${{ steps.source.outputs.root }}",
		`source_root="$SOURCE_ROOT"`,
		`cp -R "$source_root/LICENSES" "$stage/"`,
		`cd "$source_root"`,
		"runner: ubuntu-24.04",
		"runner: macos-15",
		"runner: windows-2025",
		`"$binary" --help`,
		`"$binary" version`,
		`"$binary" docs show install`,
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("release candidate workflow missing %q", want)
		}
	}
	// Step outputs reach shell code through env, never by inline expansion.
	if strings.Contains(workflow, `="${{ steps.source.outputs.root }}"`) {
		t.Fatal("release candidate interpolates a step output directly into shell code")
	}
	if strings.Contains(workflow, `sha256sum "$ARTIFACT" out/evidence/*`) {
		t.Fatal("release candidate checksums retain the stripped out/ upload prefix")
	}
	if got := strings.Count(workflow, "find . -type f ! -name SHA256SUMS"); got != 3 {
		t.Fatalf("portable checksum manifests = %d, want 3", got)
	}
	// binary, native-smoke, source, publish (its cross-check of the combined
	// SHA256SUMS), and the pig-acp jobs (pig archive check, own checksums,
	// packaged smoke) each verify with sha256sum -c.
	if got := strings.Count(workflow, "sha256sum -c SHA256SUMS"); got != 7 {
		t.Fatalf("checksum verification steps = %d, want 7", got)
	}
}

// TestReleaseCandidateShipsPigACPLikePig checks that pig-acp archives get the
// same evidence as the pig archives: native-looking SBOMs, an SBOM scan that
// fails the build, checksums, a build-provenance attestation, a Sigstore
// bundle, a packaged smoke test, and a place in the publish gate.
func TestReleaseCandidateShipsPigACPLikePig(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release-candidate.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	start := strings.Index(workflow, "\n  acp:\n")
	end := strings.Index(workflow, "\n  source:\n")
	if start < 0 || end < start {
		t.Fatal("release candidate has no acp job before the source job")
	}
	acp := workflow[start:end]
	for _, want := range []string{
		"\n  acp-native-smoke:\n",
		"needs: binary",
		"name: pig-${{ inputs.version }}-${{ matrix.goos }}-${{ matrix.goarch }}",
		"automation/release/build-pig-acp.sh",
		"actions/attest-build-provenance@",
		"fail-build: true",
		"name: pig-acp-${{ inputs.version }}-${{ matrix.goos }}-${{ matrix.goarch }}",
		"automation/release/pig-acp-smoke.py",
		"pattern: pig-acp-${{ inputs.version }}-*",
	} {
		if !strings.Contains(acp, want) && !strings.Contains(workflow, want) {
			t.Errorf("release candidate workflow missing %q", want)
		}
	}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, goarch := range []string{"amd64", "arm64"} {
			want := "- goos: " + goos + "\n            goarch: " + goarch + "\n"
			if !strings.Contains(acp, want) {
				t.Errorf("acp job has no %s/%s target", goos, goarch)
			}
		}
	}
	if strings.Contains(acp, "goos: android") {
		t.Error("acp job builds for android; pig-acp ships for hosted ACP clients only")
	}
	if !strings.Contains(workflow, "needs: [binary, native-smoke, acp, acp-native-smoke, source]") {
		t.Error("publish does not wait for the pig-acp jobs")
	}
	// pig-acp must not be rebuilt-with-pig: the bundled pig comes out of the
	// pig candidate, never from a second pig build in the acp job.
	if strings.Contains(acp, "./cmd/pig") {
		t.Error("acp job builds pig instead of taking it from the pig candidate")
	}
}
