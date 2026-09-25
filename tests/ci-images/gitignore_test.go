package ciimages

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRepositoryIgnorePolicyProtectsLocalState(t *testing.T) {
	root := repoRoot(t)
	for _, path := range []string{
		".env",
		".envrc",
		".direnv/cache",
		".idea/workspace.xml",
		".vscode/settings.json",
		"private.pem",
		"private.key",
		".ruff_cache/state",
		"extensions/example/target/debug/binary",
		"coverage.out",
	} {
		cmd := exec.Command("git", "check-ignore", "--no-index", "-q", path)
		cmd.Dir = root
		if err := cmd.Run(); err != nil {
			t.Errorf("%s is not ignored: %v", path, err)
		}
	}
	for _, path := range []string{
		"coding/extension/host/cellpack/cells/manifest.json",
		"parity/scenarios/footer/testdata/multi-pi-agent/auth.json",
		"parity/scenarios/footer/testdata/multi-pig-agent/auth.json",
		"parity/scenarios/footer/testdata/pi-agent/auth.json",
		"parity/scenarios/footer/testdata/pig-agent/auth.json",
		"parity/scenarios/model-resolver-selector/testdata/pi-agent-expired/auth.json",
		"parity/scenarios/model-resolver-selector/testdata/pig-agent-expired/auth.json",
		"parity/scenarios/providers-registry/testdata/oauth-agent/auth.json",
		"parity/scenarios/selectors/testdata/pi-agent/auth.json",
		"parity/scenarios/selectors/testdata/pig-home/agent/auth.json",
		"parity/scenarios/project-trust/testdata/session-target-cwd/target/README.txt",
	} {
		cmd := exec.Command("git", "check-ignore", "--no-index", "-q", filepath.FromSlash(path))
		cmd.Dir = root
		if err := cmd.Run(); err == nil {
			t.Errorf("required source %s is ignored", path)
		}
	}
}
