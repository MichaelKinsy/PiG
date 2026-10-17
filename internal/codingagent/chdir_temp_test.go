package codingagent

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// chdirTemp makes a new directory the working directory and returns the path Pi's process.cwd() reports for it. Off Windows it enters
// through a symbolic link with $PWD naming the link, as a shell's cd does, so every caller also checks that Pig resolves against the
// physical directory (getcwd(3)) and never $PWD. On Windows process.cwd() is GetCurrentDirectoryW, which keeps a junction in the path,
// so the directory is entered directly.
func chdirTemp(t *testing.T) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		t.Chdir(real)
		return real
	}
	link := filepath.Join(t.TempDir(), "cwd-link")
	testenv.RequireDirectoryLink(t, real, link)
	t.Chdir(link)
	t.Setenv("PWD", link)
	return real
}
