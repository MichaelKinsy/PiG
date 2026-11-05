package subprocess

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	goEnv := func(keys ...string) string {
		t.Helper()
		out, err := exec.CommandContext(t.Context(), "go", append([]string{"env"}, keys...)...).Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	want := goEnv("GOCACHE", "GOMODCACHE")
	// Go keeps its telemetry state under the configuration directory, which follows HOME only where XDG_CONFIG_HOME is empty.
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	isolateHome(t, home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	if got := goEnv("GOCACHE", "GOMODCACHE"); got != want {
		t.Fatalf("HOME isolation changed compiler caches: %q, want %q", got, want)
	}
	// A go command that finds telemetry on in a fresh home claims the upload token and forks a detached sidecar. The sidecar outlives the command and creates files in the home while the cleanup of this test removes it: TempDir RemoveAll "directory not empty".
	telemetry := strings.TrimSpace(goEnv("GOTELEMETRYDIR"))
	if rel, err := filepath.Rel(home, telemetry); err == nil && filepath.IsLocal(rel) {
		if _, err := os.Stat(filepath.Join(telemetry, "local")); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("a go command in the isolated home opened telemetry counters in %q (stat err = %v); it forked a sidecar that outlives the test", telemetry, err)
		}
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
