package coding

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// SessionManager.open starts an empty session file in `cwdOverride ?? header cwd ?? process.cwd()` (session-manager.ts:1782), and
// process.cwd() is the physical directory (getcwd(3)) even when $PWD reaches it through a symbolic link. Switching to an empty file
// therefore runs the replacement session in, and records, the physical directory.
func TestSwitchToAnEmptySessionFileUsesThePhysicalWorkingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no $PWD")
	}
	h := newRuntimeTestHarness(t, runtimeTestOptions{})
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	testenv.RequireDirectoryLink(t, real, link)
	t.Chdir(link)
	t.Setenv("PWD", link)
	empty := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := h.runtime.SwitchSession(t.Context(), empty); err != nil || result.Cancelled {
		t.Fatalf("switch = %+v, %v", result, err)
	}
	if got := h.runtime.Session().CWD(); got != real {
		t.Fatalf("session cwd = %q, want the physical %q", got, real)
	}
}
