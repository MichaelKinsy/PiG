// Package testenv checks host capabilities a test needs, so a test runs
// wherever the host provides them and says why it skipped where the host
// does not. Only test code imports this package.
package testenv

import (
	"os"
	"strings"
	"testing"
)

// RequireLiveEnv skips a live test when any named variable is unset or empty. It reports only missing names, never values, and returns the first value when every requirement is present. With no names it returns an empty string.
func RequireLiveEnv(t testing.TB, names ...string) string {
	t.Helper()
	var missing []string
	var first string
	for i, name := range names {
		value := os.Getenv(name)
		if i == 0 {
			first = value
		}
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) != 0 {
		t.Skipf("live test: missing environment variables: %s", strings.Join(missing, ", "))
	}
	return first
}

// RequireSymlink creates a required fixture link. Missing privileges fail the test instead of removing its parity obligation.
func RequireSymlink(t testing.TB, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		t.Fatalf("create required symlink %s -> %s: %v", newname, oldname, err)
	}
}

// RequireDirectoryLink creates newname as a required link to the directory oldname. On Windows it creates a privilege-free drive-target junction, which Node and this module report as a symbolic link; a volume-GUID target creates a volume mount point instead. Elsewhere it creates a symbolic link. Any failure fails the test.
func RequireDirectoryLink(t testing.TB, oldname, newname string) {
	t.Helper()
	if err := createDirectoryLink(oldname, newname); err != nil {
		t.Fatalf("create required directory link %s -> %s: %v", newname, oldname, err)
	}
}

// Symlink creates newname as a symbolic link to oldname. When the host does
// not let this process create symbolic links (Windows without Developer Mode
// or elevation), the test is skipped; any other error fails it. Use it for
// file links, and RequireDirectoryLink for a directory link.
func Symlink(t testing.TB, oldname, newname string) {
	t.Helper()
	err := os.Symlink(oldname, newname)
	if err == nil {
		return
	}
	if symlinkPrivilegeMissing(err) {
		t.Skipf("creating symbolic links needs Developer Mode or elevation on Windows: %v", err)
	}
	t.Fatalf("create symlink %s -> %s: %v", newname, oldname, err)
}
