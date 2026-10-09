package experimental

// pi: packages/coding-agent/src/experimental/plugins/package.ts

import (
	"path/filepath"
	"slices"
	"testing"
)

// plugins/package.ts:149-158 normalizePluginPackagePaths: each path resolves against the working directory in order, an empty path is rejected before the duplicate check, and two spellings of one path are duplicates.
func TestNormalizePluginPackagePathsResolvesInOrderAndRejectsEmptyAndDuplicates(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	got, err := NormalizePluginPackagePaths([]string{"b", "a", filepath.Join(dir, "c", "..", "d")})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "b"), filepath.Join(dir, "a"), filepath.Join(dir, "d")}
	if !slices.Equal(got, want) {
		t.Fatalf("paths = %q, want %q", got, want)
	}
	if got, err := NormalizePluginPackagePaths(nil); err != nil || len(got) != 0 {
		t.Fatalf("no paths = %q, %v", got, err)
	}
	for _, tc := range []struct {
		paths []string
		want  string
	}{
		{[]string{"a", ""}, "Plugin package path must not be empty"},
		{[]string{"a", "a", ""}, "Plugin package path must not be empty"},
		{[]string{"a", "./a"}, "Plugin package paths must be unique"},
		{[]string{"a", filepath.Join(dir, "a")}, "Plugin package paths must be unique"},
	} {
		if _, err := NormalizePluginPackagePaths(tc.paths); err == nil || err.Error() != tc.want {
			t.Errorf("%q: error = %v, want %q", tc.paths, err, tc.want)
		}
	}
}
