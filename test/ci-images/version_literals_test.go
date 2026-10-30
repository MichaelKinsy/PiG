// SPDX-License-Identifier: MIT

package ciimages

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// A version literal in build automation is a copy of a pin. Every Pi, Go, Node, Rust and npm version has one source
// (docs/project/compliance.md "One source for each version pin"); a script, make recipe, or workflow that names the release
// again drifts when the pin moves, and nothing notices until a build or a default breaks (#379: a script defaulted to the
// removed policy of Pi 0.87.1). This test scans those files for any dotted version. A literal passes only with an entry in
// versionLiteralAllowlist that says why it is not a copy of a pin. A new literal fails here until it derives from its source
// (automation/pin/pinlib.py for Pi, .go-version, .node-version, the env block of .github/workflows/ci.yml) or earns an entry.

// versionLiteral matches x.y.z with the optional +<commit[:8]> a commit pin appends. A match that continues a longer dotted run
// or has a fourth number (an IPv4 address) is not a version.
var versionLiteral = regexp.MustCompile(`\d+\.\d+\.\d+(?:\.\d+)?(?:\+[0-9a-f]{8})?`)

// dottedCommit follows the x.y.z of a commit pin's dotted build metadata (1.4.0.b3a29f11, as pin.py ci_tag writes it into an image tag
// and make pin-check checks it). A commit that starts with a digit already read as a fourth number and was skipped; one that starts with a
// letter was reported as the bare release, so the test failed on every pin whose commit starts with a letter.
var dottedCommit = regexp.MustCompile(`^\.[0-9a-f]{8}(?:[^0-9a-f]|$)`)

// allowedVersionLiteral says why one literal in one file is not a drifting copy of a pin.
type allowedVersionLiteral struct {
	path    string
	literal string
	reason  string
}

var versionLiteralAllowlist = []allowedVersionLiteral{
	// The sources of truth that live in scanned files.
	{".github/workflows/ci.yml", "12.1.0", "source of truth for npm: the NPM_VERSION env; make compliance checks every copy against it"},
	{".github/workflows/ci.yml", "1.97.1", "source of truth for Rust: the RUST_VERSION env; make compliance checks every copy against it"},

	// Checked copies. make compliance compares each with its source and fails on drift.
	{"automation/images/ci-go/Dockerfile", "1.27.3", "ARG GO_IMAGE: a digest-pinned image tag; make compliance checks it against .go-version"},
	{"automation/images/ci-go/metadata.env", "1.27.3", "IMAGE_TAG; make compliance checks it against .go-version"},
	{"automation/images/ci-parity/Dockerfile", "1.27.3", "ARG GO_VERSION; make compliance checks it against .go-version"},
	{"automation/images/ci-parity/metadata.env", "1.27.3", "IMAGE_TAG; make compliance checks it against .go-version"},
	{"automation/images/ci-parity/Dockerfile", "1.4.0", "ARG PI_VERSION; make pin-sync writes it from .upstream/pin and make compliance checks it"},
	{"automation/images/ci-parity/Dockerfile", "12.1.0", "the image's npm check; make compliance checks it against NPM_VERSION"},

	// Pins of tools that are not a Pi, Go, Node, Rust or npm release, kept beside the digest that pins the tool.
	{".github/workflows/ci-images.yml", "0.114.0", "local tag of the digest-pinned grype image; the digest in seed-ci-image-bases.sh is the pin"},
	{".github/workflows/ci-images.yml", "0.32.2", "local tag of the digest-pinned buildkit image; the digest in seed-ci-image-bases.sh is the pin"},
	{".github/workflows/ci-images.yml", "1.45.1", "local tag of the digest-pinned syft image; the digest in seed-ci-image-bases.sh is the pin"},
	{"automation/ci/build-ci-image.sh", "0.32.2", "local tag of the digest-pinned buildkit image; the digest in seed-ci-image-bases.sh is the pin"},
	{"automation/ci/seed-ci-image-bases.sh", "0.114.0", "local tag of the digest-pinned grype image; the digest on the line above is the pin"},
	{"automation/ci/seed-ci-image-bases.sh", "0.32.2", "local tag of the digest-pinned buildkit image; the digest on the line above is the pin"},
	{"automation/ci/seed-ci-image-bases.sh", "1.45.1", "local tag of the digest-pinned syft image; the digest on the line above is the pin"},
	{"automation/ci/seed-ci-image-bases.sh", "0.20.6", "go-containerregistry release; the sha256 on the next line is the pin"},
	{"automation/gen/vendor-typebox.sh", "1.3.27", "the typebox release the script vendors; its integrity hash is the next line"},
	{"automation/gen/vendor-typebox.sh", "0.25.10", "the esbuild release the script bundles with"},
	{".github/workflows/npm-publish.yml", "11.5.1", "the npm release that added trusted publishing, a registry feature floor and not a pin"},
	{"Makefile", "0.7.4", "the version of Pi-Bolt, a different product from Pi"},

	// Versions of PiG's own releases and benchmark labels.
	{".github/workflows/startup-performance.yml", "0.2.0", "the PiG release the startup benchmark measures against; the job name and the checked-out tag name the same release"},
	{"Makefile", "0.4.1", "benchmark arm label pig-0.4.1"},
	{"Makefile", "1.1.0", "benchmark arm label pi-1.1.0"},
	{"Makefile", "0.5.0", "the Durable 0.5.0 memory bar, a PiG milestone name"},

	// Examples and placeholders in help text, usage text and fixtures.
	{".github/workflows/npm-publish.yml", "0.2.0", "example in an input description"},
	{"Makefile", "0.163.0", "example in a target's help text"},
	{"automation/gen/mirror-upstream.sh", "0.70.5", "example in the usage text"},
	{"automation/release/gen-update-manifest.py", "0.2.0", "example in the usage text"},
	{"automation/release/gen-update-manifest.py", "0.2.1", "example in the usage text"},
	{"automation/release/module-tags.sh", "0.2.0", "example in a comment"},
	{"automation/release/npm/pack_npm.py", "0.2.0", "example in the usage text"},
	{"automation/pin/pinlib.py", "1.1.0", "example in the module docstring"},
	{"automation/pin/oracle.py", "1.3.0", "a fact about the published 1.3.0 release, which predates the facade's checkpointWhen"},
	{"automation/pin/oracle.py", "0.0.0", "placeholder version of a generated private package"},
	{"automation/dev/goproxy-github.py", "0.0.0", "the module version of a probe request"},
	{"automation/dev/setup.sh", "0.0.0", "the module version of a probe request"},
	{"automation/layout/external_imports.py", "0.0.0", "placeholder module version in a generated fixture"},
	{"automation/layout/external_imports.py", "1.26.0", "the language floor of a generated fixture go.mod, not the build toolchain"},
	{"automation/ci/check-compliance.py", "1.2.0", "the CFF schema version CITATION.cff must declare"},
}

// versionLiteralFiles lists, relative to root and in slash form, the files the scan reads: scripts, make recipes, Dockerfiles and
// image metadata under automation, the top-level Makefile, and the workflows. Test fixtures (test_*.py, *_test.py), testdata and
// installed packages are not build automation, so they may name any version.
func versionLiteralFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(filepath.Join(root, "automation"), func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "node_modules" || entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		base := entry.Name()
		switch {
		case strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py"), strings.HasSuffix(base, "_test.py"):
			return nil
		case slices.Contains([]string{".py", ".sh", ".mk", ".env"}, path.Ext(base)), base == "Makefile", strings.HasPrefix(base, "Dockerfile"):
			rel, err := filepath.Rel(root, name)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, "Makefile")
	workflows, err := os.ReadDir(filepath.Join(root, ".github", "workflows"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range workflows {
		if ext := path.Ext(entry.Name()); ext == ".yml" || ext == ".yaml" {
			files = append(files, ".github/workflows/"+entry.Name())
		}
	}
	slices.Sort(files)
	return files
}

// versionLiterals returns the version literals of one line. An action pin (`uses: owner/name@<sha> # v1.2.3`) names the
// release in a comment beside its commit; make compliance checks every action pin, so the line is not scanned.
func versionLiterals(line string) []string {
	if strings.Contains(line, "uses:") {
		return nil
	}
	var literals []string
	for _, span := range versionLiteral.FindAllStringIndex(line, -1) {
		if span[0] > 0 && strings.ContainsRune("0123456789.", rune(line[span[0]-1])) {
			continue
		}
		literal := line[span[0]:span[1]]
		if strings.Count(literal, ".") == 3 && !strings.Contains(literal, "+") {
			continue
		}
		if dottedCommit.MatchString(line[span[1]:]) {
			continue
		}
		literals = append(literals, literal)
	}
	return literals
}

func TestBuildAutomationNamesNoUnrecordedVersionLiteral(t *testing.T) {
	root := repoRoot(t)
	allowed := map[[2]string]string{}
	for _, entry := range versionLiteralAllowlist {
		key := [2]string{entry.path, entry.literal}
		if entry.reason == "" {
			t.Errorf("allowlist entry %s %s has no reason", entry.path, entry.literal)
		}
		if _, dup := allowed[key]; dup {
			t.Errorf("allowlist entry %s %s is listed twice", entry.path, entry.literal)
		}
		allowed[key] = entry.reason
	}
	seen := map[[2]string]bool{}
	for _, name := range versionLiteralFiles(t, root) {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		for number, line := range strings.Split(string(data), "\n") {
			for _, literal := range versionLiterals(line) {
				key := [2]string{name, literal}
				seen[key] = true
				if _, ok := allowed[key]; !ok {
					t.Errorf("%s\n\tnames version %s: derive it from its source (Pi: automation/pin/pinlib.py committed(); Go: .go-version; Node: .node-version; Rust and npm: the env block of .github/workflows/ci.yml), or add it to versionLiteralAllowlist with the reason it is not a copy of a pin",
						fmt.Sprintf("%s:%d", name, number+1), literal)
				}
			}
		}
	}
	for _, entry := range versionLiteralAllowlist {
		if !seen[[2]string{entry.path, entry.literal}] {
			t.Errorf("allowlist entry %s %s matches nothing; remove it", entry.path, entry.literal)
		}
	}
}

func TestVersionLiteralScanSeesVersionsAndSkipsAddresses(t *testing.T) {
	for line, want := range map[string][]string{
		`rustup default 1.97.1`:                        {"1.97.1"},
		`--version "0.87.1"`:                           {"0.87.1"},
		`pi 1.4.0+6c384aa3 and v2.0.0`:                 {"1.4.0+6c384aa3", "2.0.0"},
		`http://127.0.0.1:8765/probe`:                  nil,
		`build 1.2.3.4`:                                nil,
		`      - uses: actions/checkout@abc # v7.0.1`:  nil,
		`sha 6c384aa30f619d7b28963f2bab24d8dacbe73cb6`: nil,
		// The Pi part of an image tag is the pin's dotted build metadata (pin.py ci_tag writes it, make pin-check checks it); it is skipped
		// whether the commit's first character is a digit or a letter.
		`IMAGE_TAG=go1.27.3-node24.22-pi1.4.0.6c384aa3-rust1.97-tools2`: {"1.27.3"},
		`IMAGE_TAG=go1.27.3-node24.22-pi1.4.0.b3a29f11-rust1.97-tools2`: {"1.27.3"},
		`pi 1.4.0.b3a29f11a`: {"1.4.0"},
	} {
		if got := versionLiterals(line); !slices.Equal(got, want) {
			t.Errorf("versionLiterals(%q) = %q, want %q", line, got, want)
		}
	}
}
