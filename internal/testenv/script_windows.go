//go:build windows

package testenv

import (
	"os"
	"path/filepath"
	"testing"
)

// needsInterpreter is true: Windows cannot run a script by its #! line.
const needsInterpreter = true

// posixTool finds name.exe in Git for Windows' bin directory, where Git
// installs bash and sh, and skips the test when Git for Windows is absent.
func posixTool(t testing.TB, name string) string {
	t.Helper()
	for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		if base == "" {
			continue
		}
		path := filepath.Join(base, "Git", "bin", name+".exe")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	t.Skipf("Git for Windows %s is not installed; repository scripts need its POSIX shell", name)
	return ""
}
