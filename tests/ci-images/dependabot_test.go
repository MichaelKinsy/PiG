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
		"/cmd/pig/testdata/login-preview",
		"/coding/extension/host/subprocess/testdata/sdk-fixture",
		"/examples/extensions/go-factory",
		"/examples/extensions/rust-factory",
		"/extensions/sdk",
		"/extensions/sdk-py",
		"/extensions/sdk-rs",
		"/extensions/sdk-ts",
		"/parity/interface-extractor",
		"/piglets/porter/extensions/pig-porter",
		"/piglets/standard",
		"/tests/extension-conformance/testdata/rust-sdk-fixture",
	} {
		// A directory is listed either alone or in a grouped entry's list.
		if !strings.Contains(config, "directory: "+directory+"\n") && !strings.Contains(config, "- "+directory+"\n") {
			t.Errorf("dependabot does not cover %s", directory)
		}
	}
}
