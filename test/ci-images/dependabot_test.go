package ciimages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDependabotCoversMaintainedManifests(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, ".github", "dependabot.yml"))
	if err != nil {
		t.Fatal(err)
	}
	config := string(data)
	for _, directory := range []string{
		"/",
		"/automation/images/ci-parity",
		"/automation/images/npm-runtime",
		"/coding/cli/testdata/login-preview",
		"/coding/extension/host/subprocess/testdata/sdk-fixture",
		"/examples/extensions/go-factory",
		"/examples/extensions/rust-factory",
		"/extensions/sdk-py",
		"/extensions/sdk-rs",
		"/extensions/sdk-ts",
		"/test/parity/interface-extractor",
		"/piglets/porter/extensions/pig-porter",
		"/piglets/standard",
		"/test/extension-conformance/testdata/rust-sdk-fixture",
		"/test/term-engines/alacritty",
		"/test/term-engines/pyte",
		"/test/term-engines/wezterm",
		"/test/term-engines/xterm",
	} {
		// A directory is listed either alone or in a grouped entry's list.
		if !strings.Contains(config, "directory: "+directory+"\n") && !strings.Contains(config, "- "+directory+"\n") {
			t.Errorf("dependabot does not cover %s", directory)
		}
	}
}

// The evaluator Dockerfile pins its base images by digest on literal FROM lines
// (the form Dependabot's docker ecosystem reads), so Dependabot must watch its
// directory or the pins would never move. The Go release in the golang tag is
// .go-version's: version bumps of golang are ignored.
func TestDependabotRefreshesTheEvaluatorImageDigests(t *testing.T) {
	root := repoRoot(t)
	config, err := os.ReadFile(filepath.Join(root, ".github", "dependabot.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var docker string
	for entry := range strings.SplitSeq(string(config), "\n  - package-ecosystem: ") {
		if strings.HasPrefix(entry, "docker\n") {
			docker = entry
		}
	}
	if !strings.Contains(docker, "directory: /internal/evals/docker\n") {
		t.Fatalf("dependabot has no docker entry for /internal/evals/docker:\n%s", docker)
	}
	if !strings.Contains(docker, "dependency-name: golang") {
		t.Error("the docker entry does not ignore golang version bumps, which .go-version owns")
	}
	dockerfile, err := os.ReadFile(filepath.Join(root, "internal", "evals", "docker", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(dockerfile), "\n") {
		image, ok := strings.CutPrefix(line, "FROM ")
		if !ok {
			continue
		}
		// The variant stages build on the evaluator stage above them.
		if name := strings.Fields(image)[0]; name != "evaluator" && !strings.Contains(name, "@sha256:") {
			t.Errorf("%q is not pinned by digest", line)
		}
	}
}
