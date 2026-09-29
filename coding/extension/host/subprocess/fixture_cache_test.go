package subprocess

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFixtureBuildsHavePackageLifetime(t *testing.T) {
	for _, tc := range []struct {
		name  string
		env   string
		build func(*testing.T) string
	}{
		{"wire", envFixtureExtBin, buildFixture},
		{"wire-host", envFixtureExtBin, buildFixtureExt},
		{"sdk", envSDKFixtureBin, buildSDKFixture},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.env, "")
			var first string
			t.Run("first-consumer", func(t *testing.T) { first = tc.build(t) })
			if _, err := os.Stat(first); err != nil {
				t.Fatalf("fixture must outlive its first consumer: %v", err)
			}
			t.Run("next-consumer", func(t *testing.T) {
				// Reuse must not even invoke the compiler, not merely relink at the same path.
				t.Setenv("PATH", t.TempDir())
				if got := tc.build(t); got != first {
					t.Fatalf("fixture rebuilt: %q, want shared artifact %q", got, first)
				}
			})
		})
	}
}

func TestHomeIsolationKeepsGoBuildCaches(t *testing.T) {
	t.Setenv("GOCACHE", "")
	t.Setenv("GOMODCACHE", "")
	readPaths := func() string {
		t.Helper()
		out, err := exec.CommandContext(t.Context(), "go", "env", "GOCACHE", "GOMODCACHE").Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	want := readPaths()
	keepGoBuildCaches(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	if got := readPaths(); got != want {
		t.Fatalf("HOME isolation changed compiler caches: %q, want %q", got, want)
	}
}

func TestRustBuildsSharePackageTarget(t *testing.T) {
	target := os.Getenv("CARGO_TARGET_DIR")
	if target == "" {
		t.Fatal("Rust fixtures need a shared target instead of recompiling SDK dependencies in every temporary cell")
	}
	// An explicit caller override retains Cargo's normal relative-path semantics.
	if filepath.IsAbs(target) {
		for range 2 {
			if got := cargoBuildTargetDirectory(t.TempDir()); got != target {
				t.Fatalf("Rust build target = %q, want shared target %q", got, target)
			}
		}
	}
}
