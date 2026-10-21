//go:build parity

package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A clone without .upstream/current must stop the run with the command that makes it, not fail the scenarios that import the mirrored source one by one.
func TestUpstreamMirrorMissingNamesTheMakeTarget(t *testing.T) {
	for name, prepare := range map[string]func(root string){
		"no .upstream":     func(string) {},
		"dangling current": func(root string) { mustSymlink(t, "v-missing", filepath.Join(root, ".upstream", "current")) },
		"empty mirror": func(root string) {
			mustMkdir(t, filepath.Join(root, ".upstream", "v1"))
			mustSymlink(t, "v1", filepath.Join(root, ".upstream", "current"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			mustMkdir(t, filepath.Join(root, ".upstream"))
			prepare(root)
			err := upstreamMirrorMissing(root)
			if err == nil {
				t.Fatal("upstreamMirrorMissing = nil, want an error")
			}
			for _, want := range []string{"Pi mirror missing", "make upstream-mirror"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestUpstreamMirrorMissingAcceptsAMirror(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".upstream", "v1", "packages", "coding-agent"))
	mustSymlink(t, "v1", filepath.Join(root, ".upstream", "current"))
	if err := upstreamMirrorMissing(root); err != nil {
		t.Fatalf("upstreamMirrorMissing = %v, want nil", err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}
