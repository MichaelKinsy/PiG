// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT
package ciimages

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestReleaseCandidatePublishesADraftGitHubRelease checks the shape of the
// publish job release-candidate.yml needs to attach every archive plus one
// combined SHA256SUMS to a draft GitHub Release, and to gate that behind an
// approved environment with its own, job-scoped write permission.
func TestReleaseCandidatePublishesADraftGitHubRelease(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release-candidate.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)

	for _, want := range []string{
		"\n  publish:\n",
		"needs: [binary, native-smoke, source]",
		"environment:\n      name: release\n",
		"contents: write",
		"automation/release/combine-checksums.py release",
		"gh release create \"v${VERSION}\" release/*",
		"--draft",
		"--target \"$GITHUB_SHA\"",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("release-candidate.yml publish job missing %q", want)
		}
	}

	// The tag pattern matches RELEASING.md's "PiG uses semantic versions and
	// annotated tags in the form vMAJOR.MINOR.PATCH."
	if !regexp.MustCompile(`gh release create "v\$\{VERSION}"`).MatchString(workflow) {
		t.Error("release-candidate.yml does not tag the release v<version>")
	}

	// contents: write must appear exactly once, inside the publish job. The
	// top-level default token permissions (contents: read) must stay
	// read-only: compliance's check_permissions only reads the unindented
	// "permissions:" block, but a second job-level "contents: write" would
	// widen a job that does not need it.
	if got := strings.Count(workflow, "contents: write"); got != 1 {
		t.Fatalf(`"contents: write" appears %d times, want exactly 1 (the publish job only)`, got)
	}
	if !strings.HasPrefix(workflow, "# SPDX-FileCopyrightText") {
		t.Fatal("release-candidate.yml lost its SPDX header")
	}
	if !regexp.MustCompile(`(?m)^permissions:\n  contents: read\n`).MatchString(workflow) {
		t.Fatal("release-candidate.yml top-level permissions must stay contents: read")
	}
}

// TestCombineChecksumsMatchesInstallShFormat runs the release combine script
// against a fixture set of archives and proves the SHA256SUMS it writes is
// byte-for-byte the format install.sh's expected_sha256 awk parser and
// `sha256sum -c` both require: a lowercase 64-hex digest, exactly two
// spaces, then the bare archive name, one line per archive.
func TestCombineChecksumsMatchesInstallShFormat(t *testing.T) {
	root := repoRoot(t)
	script := filepath.Join(root, "automation", "release", "combine-checksums.py")
	dir := t.TempDir()

	archives := map[string]string{
		"pig-0.2.0-linux-amd64.tar.gz":  "linux amd64 fixture bytes",
		"pig-0.2.0-linux-arm64.tar.gz":  "linux arm64 fixture bytes",
		"pig-0.2.0-darwin-amd64.tar.gz": "darwin amd64 fixture bytes",
		"pig-0.2.0-darwin-arm64.tar.gz": "darwin arm64 fixture bytes",
		"pig-0.2.0-windows-amd64.zip":   "windows amd64 fixture bytes",
	}
	for name, content := range archives {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A non-archive file in the same directory (evidence, sbom, and so on
	// under release-candidate.yml's out/evidence) must not end up in the
	// combined SHA256SUMS: install.sh looks up one exact archive name.
	if err := os.WriteFile(filepath.Join(dir, "sbom.spdx.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("python3", script, dir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("combine-checksums.py failed: %v\n%s", err, output)
	}

	sums, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(sums), "\n"), "\n")
	if len(lines) != len(archives) {
		t.Fatalf("SHA256SUMS has %d lines, want %d (one per archive, none for sbom.spdx.json)", len(lines), len(archives))
	}

	// The install.sh line format: NF == 2 once split on whitespace, a
	// 64-character lowercase hex digest, and the bare file name (mirroring
	// docs/site/public/install.sh's expected_sha256()).
	lineFormat := regexp.MustCompile(`^[0-9a-f]{64}  (\S+)$`)
	seen := map[string]bool{}
	for _, line := range lines {
		match := lineFormat.FindStringSubmatch(line)
		if match == nil {
			t.Fatalf("SHA256SUMS line does not match install.sh's expected format: %q", line)
		}
		name := match[1]
		if _, ok := archives[name]; !ok {
			t.Fatalf("SHA256SUMS lists unexpected file %q", name)
		}
		seen[name] = true
	}
	for name := range archives {
		if !seen[name] {
			t.Errorf("SHA256SUMS is missing %q", name)
		}
	}

	// Simulate install.sh's own awk lookup for one archive, proving a
	// real installer invocation would accept this file unmodified.
	awk := `($2 == name || $2 == "./" name || $2 == "*" name || $2 == "*./" name) && NF == 2 {
		count++
		digest = tolower($1)
	}
	END {
		if (count == 1 && digest ~ /^[0-9a-f]+$/ && length(digest) == 64) print digest
	}`
	target := "pig-0.2.0-linux-amd64.tar.gz"
	// Through sh, as install.sh runs it; Git for Windows keeps awk off PATH.
	out, err := exec.Command(testenv.Sh(t), "-c", `awk -v name="$1" "$2" "$3"`, "sh", target, awk, filepath.Join(dir, "SHA256SUMS")).CombinedOutput()
	if err != nil {
		t.Fatalf("awk lookup failed: %v\n%s", err, out)
	}
	digest := strings.TrimSpace(string(out))
	if len(digest) != 64 {
		t.Fatalf("install.sh's awk lookup did not resolve a single digest for %s, got %q", target, digest)
	}
}

// TestCombineChecksumsRejectsAnEmptyDirectory proves the script fails closed
// (matching install.sh's own fail-closed contract) instead of writing an
// empty SHA256SUMS that would silently verify nothing.
func TestCombineChecksumsRejectsAnEmptyDirectory(t *testing.T) {
	root := repoRoot(t)
	script := filepath.Join(root, "automation", "release", "combine-checksums.py")
	dir := t.TempDir()
	cmd := exec.Command("python3", script, dir)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("combine-checksums.py accepted a directory with no archives")
	}
	if !strings.Contains(string(output), "no archives") {
		t.Fatalf("unexpected failure: %v\n%s", err, output)
	}
}
