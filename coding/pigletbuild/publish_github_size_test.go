package pigletbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
)

func TestPublishGitHubRejectsAssetAbovePullSizeLimit(t *testing.T) {
	gh, tmp := publishTestEnv(t)
	source := writePublishPiglet(t, releasedPorter)
	keyPath, key, _ := writePublishKey(t)
	dist := t.TempDir()
	path := filepath.Join(dist, "asset")
	if err := os.WriteFile(path, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pull's reviewed 512 MiB complete-file bound: an executable body at
	// that bound exceeds it once the valid signature trailer is appended.
	if err := os.Truncate(path, 512<<20); err != nil {
		t.Fatal(err)
	}
	manifest := releaseManifest(t, source, "linux/amd64")
	manifest.PigVersion = publishTestPigVersion
	digest := "sha256:" + strings.Repeat("d", 64)
	manifest.PigletDigest, manifest.ResolutionDigest, manifest.ComponentPlanDigest = digest, digest, digest
	if _, err := signature.Sign(path, manifest, key); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	code := RunPigletPublishCommand([]string{source, "--to", "github", "--repo", "acme/porter", "--sign-key", keyPath, "--artifacts", dist, "--yes"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "above the 536870912-byte limit") {
		t.Errorf("oversized asset: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if got := ghArgs(gh.calls()); len(got) != 1 || got[0][1] != "view" {
		t.Errorf("oversized asset reached gh release create: %q", got)
	}
	if strings.Contains(stdout.String(), "Published") {
		t.Error("oversized asset reported a published release")
	}
	assertNoStagedRelease(t, tmp)
}
