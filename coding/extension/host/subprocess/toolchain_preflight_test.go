package subprocess

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestEnsureBuildToolchain_MissingToolchainIsActionable guards the toolchain-less
// host experience: instead of an opaque "exec: \"go\": ... not found", the user
// gets an error naming the missing toolchain and the Piglet Binary/prebuilt fallback.
func TestEnsureBuildToolchain_MissingToolchainIsActionable(t *testing.T) {
	t.Setenv("PATH", "") // no toolchains discoverable
	cases := []struct {
		buildType string
		tool      string
	}{
		{"go", "go"},
		{"rust", "cargo"},
	}
	for _, tc := range cases {
		t.Run(tc.buildType, func(t *testing.T) {
			err := ensureBuildToolchain(t.Context(), tc.buildType)
			if err == nil {
				t.Fatalf("expected error when %s toolchain absent", tc.buildType)
			}
			msg := err.Error()
			for _, want := range []string{tc.tool, "Piglet Binary", "prebuilt"} {
				if !strings.Contains(msg, want) {
					t.Fatalf("error %q missing %q", msg, want)
				}
			}
		})
	}
}

func TestEnsureNodeRuntimeAcceptsLoaderVersionFloor(t *testing.T) {
	binDir := t.TempDir()
	writeFakeNode(t, binDir, filepath.Join(t.TempDir(), "node-invocations"), "v22.13.0")
	t.Setenv("PATH", binDir)
	want := filepath.Join(binDir, "node")
	if runtime.GOOS == "windows" {
		want += ".cmd"
	}

	path, err := ensureNodeRuntime(t.Context())
	if err != nil {
		t.Fatalf("Node at loader floor was rejected: %v", err)
	}
	if path != want {
		t.Fatalf("node path = %q, want %q", path, want)
	}
}

func TestEnsureBuildToolchain_PresentToolchainPasses(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}
	if err := ensureBuildToolchain(t.Context(), "go"); err != nil {
		t.Fatalf("unexpected error with go on PATH: %v", err)
	}
}

// Unknown or non-compiled build types (e.g. python, handled elsewhere) must not
// be gated by this check.
func TestEnsureBuildToolchain_UnknownTypeIsNoop(t *testing.T) {
	t.Setenv("PATH", "")
	if err := ensureBuildToolchain(t.Context(), "python"); err != nil {
		t.Fatalf("unknown build type should be a no-op, got %v", err)
	}
}
