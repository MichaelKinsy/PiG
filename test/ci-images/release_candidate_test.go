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
	if got := strings.Count(workflow, "uses: actions/setup-python@"); got != 5 {
		t.Fatalf("release candidate setup-python steps = %d, want 5", got)
	}
	if got := strings.Count(workflow, "python-version: '3.12'"); got != 5 {
		t.Fatalf("release candidate Python 3.12 pins = %d, want 5", got)
	}
	if got := strings.Count(workflow, "python automation/release/validate-sbom.py"); got != 4 {
		t.Fatalf("release candidate SBOM validation commands = %d, want 4", got)
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
	if got := strings.Count(workflow, "find . -type f ! -name SHA256SUMS"); got != 4 {
		t.Fatalf("portable checksum manifests = %d, want 4", got)
	}
	// binary, native-smoke, source, publish (its cross-check of the combined
	// SHA256SUMS), the pig-acp jobs (pig archive check, own checksums,
	// packaged smoke) and the PiG Standard jobs (own checksums, packaged
	// smoke) each verify with sha256sum -c.
	if got := strings.Count(workflow, "sha256sum -c SHA256SUMS"); got != 9 {
		t.Fatalf("checksum verification steps = %d, want 9", got)
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
	if !strings.Contains(workflow, "needs: [binary, native-smoke, acp, acp-native-smoke, standard, standard-native-smoke, source]") {
		t.Error("publish does not wait for the pig-acp and PiG Standard jobs")
	}
	// pig-acp must not be rebuilt-with-pig: the bundled pig comes out of the
	// pig candidate, never from a second pig build in the acp job.
	if strings.Contains(acp, "./cmd/pig") {
		t.Error("acp job builds pig instead of taking it from the pig candidate")
	}
}

// jobText returns the text of one top-level job of release-candidate.yml.
func jobText(t *testing.T, workflow, job string) string {
	t.Helper()
	start := strings.Index(workflow, "\n  "+job+":\n")
	if start < 0 {
		t.Fatalf("release candidate has no %s job", job)
	}
	text := workflow[start+1:]
	if end := strings.Index(text, "\n\n  "); end >= 0 {
		text = text[:end]
	}
	return text
}

// TestReleaseCandidateShipsPiGStandardAsItsOwnArtifact checks that PiG Standard
// has its own archives per hosted target, built by the Piglet Binary build on
// native runners, with the evidence a pig archive has (SBOMs, a failing scan,
// checksums, attestation, Sigstore bundle, packaged smoke) kept apart from
// Stock's by name, and that publication waits for it.
func TestReleaseCandidateShipsPiGStandardAsItsOwnArtifact(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release-candidate.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	build := jobText(t, workflow, "standard-build")
	standard := jobText(t, workflow, "standard")
	smoke := jobText(t, workflow, "standard-native-smoke")
	for _, want := range []string{
		"automation/release/build-pig-standard.sh",
		"name: pig-standard-build-${{ inputs.version }}-${{ matrix.goos }}-${{ matrix.goarch }}",
		"needs: source",
	} {
		if !strings.Contains(build, want) {
			t.Errorf("standard-build job missing %q", want)
		}
	}
	for _, want := range []string{
		"needs: [source, standard-build]",
		"name: pig-standard-build-${{ inputs.version }}-${{ matrix.goos }}-${{ matrix.goarch }}",
		`name="pig-standard-${VERSION}-${GOOS}-${GOARCH}"`,
		"anchore/sbom-action@",
		"format: spdx-json",
		"format: cyclonedx-json",
		"python automation/release/validate-sbom.py",
		"fail-build: true",
		"actions/attest-build-provenance@",
		"steps.attest.outputs.bundle-path",
		"find . -type f ! -name SHA256SUMS",
		"name: pig-standard-${{ inputs.version }}-${{ matrix.goos }}-${{ matrix.goarch }}",
	} {
		if !strings.Contains(standard, want) {
			t.Errorf("standard job missing %q", want)
		}
	}
	for _, want := range []string{
		"needs: standard",
		"name: pig-standard-${{ inputs.version }}-${{ matrix.goos }}-${{ matrix.goarch }}",
		"sha256sum -c SHA256SUMS",
		`"$binary" version`,
		`"$binary" verify --json`,
		`"$binary" status`,
		"PIG_OFFLINE=1",
	} {
		if !strings.Contains(smoke, want) {
			t.Errorf("standard-native-smoke job missing %q", want)
		}
	}
	// Each target builds and starts natively, on a runner of its own architecture.
	for _, target := range []struct{ runner, goos, goarch string }{
		{"ubuntu-24.04", "linux", "amd64"},
		{"ubuntu-24.04-arm", "linux", "arm64"},
		{"macos-15-intel", "darwin", "amd64"},
		{"macos-15", "darwin", "arm64"},
		{"windows-2025", "windows", "amd64"},
		{"windows-11-arm", "windows", "arm64"},
	} {
		want := "- runner: " + target.runner + "\n            goos: " + target.goos + "\n            goarch: " + target.goarch + "\n"
		for name, job := range map[string]string{"standard-build": build, "standard-native-smoke": smoke} {
			if !strings.Contains(job, want) {
				t.Errorf("%s job has no %s/%s target on %s", name, target.goos, target.goarch, target.runner)
			}
		}
		if want := "- goos: " + target.goos + "\n            goarch: " + target.goarch + "\n"; !strings.Contains(standard, want) {
			t.Errorf("standard job has no %s/%s target", target.goos, target.goarch)
		}
	}
	for name, job := range map[string]string{"standard-build": build, "standard": standard, "standard-native-smoke": smoke} {
		if strings.Contains(job, "goos: android") {
			t.Errorf("%s job targets android; the native Piglet builder builds only its host target and no runner runs Android", name)
		}
	}
	// Standard's artifacts never share a name, and so never share evidence, with a pig archive.
	for name, job := range map[string]string{"standard": standard, "standard-native-smoke": smoke} {
		if strings.Contains(job, "name: pig-${{ inputs.version }}-${{ matrix.goos }}") {
			t.Errorf("%s job names a pig candidate artifact", name)
		}
	}
	// The Binary comes out of the Piglet Binary build, never a plain go build of cmd/pig, in the packaging job.
	if strings.Contains(standard, "go build") || strings.Contains(standard, "./cmd/pig") {
		t.Error("standard job builds a binary instead of packaging the one the native runner built")
	}
	if !strings.Contains(workflow, "pattern: pig-standard-${{ inputs.version }}-*") {
		t.Error("publish does not download the PiG Standard candidates")
	}
}
