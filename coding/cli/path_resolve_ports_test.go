package cli

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/packagemanager"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// resource-loader.ts isUnderPath: a root that ends in a separator is its own
// prefix, so everything under the filesystem root is inside it.
func TestIsWithinMatchesResourceLoaderIsUnderPath(t *testing.T) {
	root := filepath.VolumeName(os.TempDir()) + string(filepath.Separator)
	inside := filepath.Join(os.TempDir(), "x")
	if !packagemanager.IsWithin(inside, root) {
		t.Errorf("packagemanager.IsWithin(%q, %q) = false; Pi's isUnderPath is true for a root that already ends in a separator", inside, root)
	}
	if !packagemanager.IsWithin(root, root) {
		t.Errorf("packagemanager.IsWithin(%q, %q) = false", root, root)
	}
	base := filepath.Join(os.TempDir(), "base")
	if packagemanager.IsWithin(base+"x", base) || packagemanager.IsWithin(filepath.Dir(base), base) {
		t.Error("a sibling with the same prefix and a parent are not inside the base")
	}
	if !packagemanager.IsWithin(filepath.Join(base, "a", "..", "b"), base) || packagemanager.IsWithin(filepath.Join(base, "..", "b"), base) {
		t.Error("isWithin resolves dot segments before comparing")
	}
}

// package-manager.ts resolveManagedPath: path.resolve(resolve(root), ...parts),
// then refuse a result outside the root.
func TestResolveManagedPackagePathMatchesPackageManager(t *testing.T) {
	root := filepath.Join(os.TempDir(), "managed")
	if got, err := packagemanager.ResolveManagedPackagePath(root, "a", "b"); err != nil || got != filepath.Join(root, "a", "b") {
		t.Errorf("resolveManagedPackagePath = %q, %v", got, err)
	}
	if got, err := packagemanager.ResolveManagedPackagePath(root); err != nil || got != root {
		t.Errorf("no parts resolves to the root: %q, %v", got, err)
	}
	if got, err := packagemanager.ResolveManagedPackagePath(filepath.Join(root, "x", ".."), "a"); err != nil || got != filepath.Join(root, "a") {
		t.Errorf("the root is resolved first: %q, %v", got, err)
	}
	for _, escape := range [][]string{{".."}, {"a", "..", ".."}, {filepath.Join(os.TempDir(), "other")}} {
		if got, err := packagemanager.ResolveManagedPackagePath(root, escape...); err == nil {
			t.Errorf("packagemanager.ResolveManagedPackagePath(%q) = %q; want a refusal", escape, got)
		}
	}
}

func TestSamePathAndAncestorSkillDirsResolveLikeNode(t *testing.T) {
	dir := t.TempDir()
	if !samePath(filepath.Join(dir, "a", "..", "b"), filepath.Join(dir, "b")) {
		t.Error("samePath resolves dot segments")
	}
	if samePath(filepath.Join(dir, "a"), filepath.Join(dir, "b")) {
		t.Error("distinct paths are not the same")
	}
	t.Chdir(dir)
	// process.cwd() is the physical path (getcwd), and path.resolve keeps symlinks: on macOS the temporary directory
	// /var/... is a symlink to /private/var/..., so a relative path resolves under the physical directory.
	physical := physicalPath(t, dir)
	if !samePath("rel", filepath.Join(physical, "rel")) {
		t.Error("a relative path resolves against the working directory")
	}
	dirs := discoverAncestorAgentsSkillDirs(filepath.Join("rel", "sub"))
	if len(dirs) == 0 || dirs[0] != filepath.Join(physical, "rel", "sub", ".agents", "skills") {
		t.Errorf("discoverAncestorAgentsSkillDirs = %v; want the first entry under the resolved start", dirs)
	}
}

func TestPathPortsReportAnUnreadableWorkingDirectory(t *testing.T) {
	testenv.DeletedWorkingDirectory(t)
	if samePath("a", "a") {
		t.Error("an unresolvable path is not the same as itself")
	}
	if packagemanager.IsWithin("a/b", "a") {
		t.Error("an unresolvable path is not within a base")
	}
	if dirs := discoverAncestorAgentsSkillDirs("rel"); dirs != nil {
		t.Errorf("discoverAncestorAgentsSkillDirs = %v; want nil", dirs)
	}
	if got, err := packagemanager.ResolveManagedPackagePath("managed", "a"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("resolveManagedPackagePath = %q, %v; want ENOENT", got, err)
	}
}

// physicalPath is dir as process.cwd() reports it: the kernel's path with symlinks resolved (macOS /var -> /private/var).
// Windows has no such indirection, and EvalSymlinks there expands an 8.3 short name (RUNNER~1) that process.cwd() keeps.
func physicalPath(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return dir
	}
	physical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return physical
}
