package codingagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/resolvepath"
)

// The startup trust walk visits every ancestor of the working directory (the 32-level budget of cmd/pig's TestStartupAllocationPerDirectoryLevel).
// Pi 1be312b4 (#10488) compares each level's .agents/skills with the user's by pathsEqual, which folds case on Windows; the fold must not
// cost a level more than the byte comparison it replaces on other platforms.
func TestHasTrustRequiringProjectResourcesLevelAllocatesTheSameOnWindowsAsElsewhere(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "Home"))
	shallow := filepath.Join(root, "Work")
	deep := shallow
	for level := range 32 {
		deep = filepath.Join(deep, "l"+string(rune('a'+level%26))+"X")
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	perLevel := func(platform string) float64 {
		t.Cleanup(resolvepath.OverridePlatform(platform))
		allocs := func(cwd string) float64 {
			return testing.AllocsPerRun(10, func() {
				if HasTrustRequiringProjectResources(cwd) {
					t.Fatal("no project resource exists")
				}
			})
		}
		return (allocs(deep) - allocs(shallow)) / 32
	}
	posix, windows := perLevel("linux"), perLevel("windows")
	if windows > posix {
		t.Fatalf("a directory level allocates %.2f objects with Windows path comparison, %.2f without", windows, posix)
	}
}
