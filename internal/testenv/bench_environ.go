package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// PathOnlyEnvironment gives a benchmark that starts programs a process environment of one variable, PATH, which holds the directories of the named programs, and
// restores the environment when the benchmark ends. A child's environment is copied and de-duplicated on every start (one allocation per variable) and each directory of
// PATH costs a lookup, so allocs/op of a benchmark that spawns otherwise follows the variables and the PATH of whoever runs it: BenchmarkClipboardImageCommand
// measured 82 on a developer's shell and 153 in the nightly's runner, BenchmarkNpmMetadataLookup 138 and 176, BenchmarkPackageInheritedOutput 118 and 150. Call it after the
// benchmark has made its temporary directories (TMPDIR is gone from the environment) and before the measured loop.
func PathOnlyEnvironment(tb testing.TB, programs ...string) {
	tb.Helper()
	var dirs []string
	for _, program := range programs {
		found, err := exec.LookPath(program)
		if err != nil {
			tb.Fatal(err)
		}
		if dir := filepath.Dir(found); !contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	environ := os.Environ()
	os.Clearenv()
	if err := os.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator))); err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		os.Clearenv()
		for _, entry := range environ {
			if key, value, ok := strings.Cut(entry, "="); ok && key != "" {
				_ = os.Setenv(key, value)
			}
		}
	})
}

func contains(list []string, want string) bool {
	return slices.Contains(list, want)
}
