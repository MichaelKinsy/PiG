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
	dirs := programDirs(tb, programs)
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

// PathOf sets PATH to the directories of the named programs, in order, until the benchmark ends, and leaves the rest of the environment as it is. A lookup walks PATH one
// directory at a time and allocates for each directory before the one that holds the program, so allocs/op of a benchmark that starts a program follows the position of that
// program on the PATH of whoever runs it: BenchmarkFactoryCacheThroughNodeHost measured 738 on a lane host and 874 with seventeen more directories before node. A benchmark
// whose measured path starts or looks up other programs as well needs them named too.
func PathOf(tb testing.TB, programs ...string) {
	tb.Helper()
	tb.Setenv("PATH", strings.Join(programDirs(tb, programs), string(os.PathListSeparator)))
}

// programDirs returns the distinct directories that hold the named programs, in order of the programs, and fails the test for a program that is not on PATH.
func programDirs(tb testing.TB, programs []string) []string {
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
	return dirs
}

// EmptyPath sets PATH to one empty directory, directly in /tmp, until the benchmark ends, and leaves the rest of the environment as it is. The measured path of a benchmark
// that reloads resolves fd, then fdfind, for the autocomplete provider, and neither is installed on a lane host, so each lookup searches the whole PATH, eight allocations per
// directory for the two names: BenchmarkInteractiveSessionReload/0 measured 948 on a lane host (twelve directories); on main the first run of the pull request's shard read 1,096
// against 956, on a runner whose PATH is longer. The benchmark then measures a machine on which neither is installed, one directory searched, on every machine. A benchmark that needs a program
// found uses PathOf or PathOnlyEnvironment.
func EmptyPath(tb testing.TB) {
	tb.Helper()
	tb.Setenv("PATH", ShortTempDir(tb, "pig-path-"))
}

func contains(list []string, want string) bool {
	return slices.Contains(list, want)
}
