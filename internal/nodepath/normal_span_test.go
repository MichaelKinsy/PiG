package nodepath

import (
	"strings"
	"testing"
)

// The substring that normalizeString returns for a path that is already normal is what Node's algorithm builds, for both
// flavors and both settings of allowAboveRoot.
func TestNormalPathSpanIsTheBuiltResult(t *testing.T) {
	paths := []string{
		"", "/", "//", "a", "/a", "a/", "/a/b/c", "/a/b/c/", "//a//b", "a/./b", "a/../b", "../a", "/..", ".", "./a", "a/.", "...",
		"/a/.../b", ".a/b.", "a\\b", "\\a\\b", "C:\\a\\b", "C:/a/b", "a\\/b", "\\\\server\\share\\x",
	}
	for _, row := range randomPathRows(400) {
		paths = append(paths, row.A, row.B, row.C)
	}
	for _, path := range paths {
		for _, flavor := range []struct {
			separator byte
			isSep     func(int) bool
		}{{slash, isPosixSeparator}, {backslash, isSeparator}} {
			for _, above := range []bool{false, true} {
				if got, want := normalizeString(path, above, flavor.separator, flavor.isSep), buildNormal(path, above, flavor.separator, flavor.isSep); got != want {
					t.Errorf("normalizeString(%q, %v, %q) = %q, want %q", path, above, flavor.separator, got, want)
				}
			}
		}
	}
}

// A working directory and its ancestors are absolute and normal: resolving one allocates nothing.
func TestResolveOfANormalAbsolutePathAllocatesNothing(t *testing.T) {
	path := "/tmp/pig/" + strings.Repeat("level/", 16) + "w"
	if got, err := PosixResolve(Env{}, path); err != nil || got != path {
		t.Fatalf("PosixResolve(%q) = %q, %v", path, got, err)
	}
	if allocs := testing.AllocsPerRun(100, func() { _, _ = PosixResolve(Env{}, path) }); allocs != 0 {
		t.Errorf("PosixResolve of a normal absolute path allocates %v times, want 0", allocs)
	}
}
