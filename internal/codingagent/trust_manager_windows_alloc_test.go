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

// Pi 1.5.0 (#10947) asks the trust store for a saved decision at startup even when the project has nothing that needs trust. With nothing saved the answer
// is "none" whatever the path, so the store must not spend a realpath of the working directory, one more allocation per directory level, to find it.
func TestProjectTrustStoreGetWithNoSavedDecisionsCostsNothingPerDirectoryLevel(t *testing.T) {
	root := t.TempDir()
	store := NewProjectTrustStore(filepath.Join(root, "agent"))
	shallow := filepath.Join(root, "work")
	deep := shallow
	for level := range 32 {
		deep = filepath.Join(deep, "l"+string(rune('a'+level%26)))
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	allocs := func(cwd string) float64 {
		return testing.AllocsPerRun(10, func() {
			if decision, err := store.Get(cwd); err != nil || decision != nil {
				t.Fatalf("Get(%q) = %v, %v; want no decision", cwd, decision, err)
			}
		})
	}
	if shallowAllocs, deepAllocs := allocs(shallow), allocs(deep); deepAllocs > shallowAllocs {
		t.Fatalf("Get allocates %.0f objects at 32 levels deeper, %.0f at the shallow directory", deepAllocs, shallowAllocs)
	}
}
