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
	if got := strings.Count(workflow, "uses: actions/setup-python@"); got != 2 {
		t.Fatalf("release candidate setup-python steps = %d, want 2", got)
	}
	if got := strings.Count(workflow, "python-version: '3.12'"); got != 2 {
		t.Fatalf("release candidate Python 3.12 pins = %d, want 2", got)
	}
	if got := strings.Count(workflow, "python automation/release/validate-sbom.py"); got != 2 {
		t.Fatalf("release candidate SBOM validation commands = %d, want 2", got)
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
	if got := strings.Count(workflow, "find . -type f ! -name SHA256SUMS"); got != 2 {
		t.Fatalf("portable checksum manifests = %d, want 2", got)
	}
	// binary, native-smoke, source, and publish (its cross-check of the
	// combined SHA256SUMS) each verify with sha256sum -c.
	if got := strings.Count(workflow, "sha256sum -c SHA256SUMS"); got != 4 {
		t.Fatalf("checksum verification steps = %d, want 4", got)
	}
}

// The release is blocked unless the release commit builds with GOWORK=off against the nested module tags it just created. Every other CI
// build resolves extensions/sdk through go.work, and make module-publication only proves the tag exists, so without this step a release
// commit using SDK API its tag lacks would publish a root module that `go install ...@v<version>` cannot build.
func TestReleaseCandidateBuildsTheReleaseCommitAgainstPublishedModules(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release-candidate.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	publish := workflow[strings.Index(workflow, "\n  publish:\n"):]
	order := []string{
		"name: Tag the nested Go modules on the release commit",
		"python3 automation/ci/check-module-publication.py",
		"python3 automation/ci/check-module-build.py --fresh-cache",
		"name: Create the draft GitHub Release",
	}
	last := -1
	for _, want := range order {
		at := strings.Index(publish, want)
		if at < 0 {
			t.Fatalf("publish job has no %q", want)
		}
		if at < last {
			t.Fatalf("publish job runs %q out of order; want %q", want, order)
		}
		last = at
	}
	if !strings.Contains(publish, "python3 -B -m unittest automation/ci/test_check_module_build.py") {
		t.Fatal("publish job does not run the module build gate's self-test before trusting it")
	}
	script, err := os.ReadFile(filepath.Join(repoRoot(t), "automation", "ci", "check-module-build.py"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`GOWORK="off"`, `-modcacherw`} {
		if !strings.Contains(string(script), want) {
			t.Errorf("check-module-build.py does not set %s", want)
		}
	}
}
