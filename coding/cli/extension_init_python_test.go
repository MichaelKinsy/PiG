//go:build !pig_strip_extension_sdk_python

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The scaffold stages the python SDK, which a build with pig_strip_extension_sdk_python does not carry.
func TestRunExtensionInitPython(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)

	dir := filepath.Join(t.TempDir(), "planner")
	if code := runExtensionInit([]string{dir, "--lang", "python", "--json"}); code != 0 {
		t.Fatalf("python init exit code = %d, want 0", code)
	}

	if _, err := os.Stat(filepath.Join(stagedSDKDir(t, home, "python"), "pig_sdk", "__init__.py")); err != nil {
		t.Fatalf("python init did not stage the Python SDK: %v", err)
	}
	mod := filepath.Join(dir, "planner.py")
	if _, err := os.Stat(mod); err != nil {
		t.Fatalf("missing python module: %v", err)
	}
	moduleSource, err := os.ReadFile(mod)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(moduleSource), filepath.ToSlash(home)) {
		t.Fatalf("Python scaffold contains a machine-specific SDK path:\n%s", moduleSource)
	}
	assertScaffoldHasNoYAML(t, dir)

}
