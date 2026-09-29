package tools

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/3302-find-path-glob.test.ts
func TestFindPathGlobRegression(t *testing.T) {
	root := findRegressionTree(t, map[string]string{
		"some/parent/child/file.ext":     "",
		"some/parent/child/test.spec.ts": "",
		"src/foo/bar/example.spec.ts":    "",
	})
	for _, tc := range []struct {
		name    string
		pattern string
		want    []string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/3302-find-path-glob.test.ts:50
		{"basename pattern still matches (regression-safe)", "*.spec.ts", []string{"some/parent/child/test.spec.ts", "src/foo/bar/example.spec.ts"}},
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/3302-find-path-glob.test.ts:55
		{"directory-prefixed pattern with ** tail matches subtree", "some/parent/child/**", []string{"some/parent/child/file.ext", "some/parent/child/test.spec.ts"}},
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/3302-find-path-glob.test.ts:62
		{"leading ** wildcard with path segments matches", "**/parent/child/*", []string{"some/parent/child/file.ext", "some/parent/child/test.spec.ts"}},
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/3302-find-path-glob.test.ts:68
		{"src/**/*.spec.ts matches nested spec file", "src/**/*.spec.ts", []string{"src/foo/bar/example.spec.ts"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertFindRegressionPaths(t, root, tc.pattern, tc.want)
		})
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/3303-find-nested-gitignore.test.ts
func TestFindNestedGitignoreRegression(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/3303-find-nested-gitignore.test.ts:52
	t.Run("applies a/.gitignore only inside a/ and leaves b/ untouched", func(t *testing.T) {
		root := findRegressionTree(t, map[string]string{
			"a/.gitignore":  "ignored.txt\n",
			"a/ignored.txt": "",
			"a/kept.txt":    "",
			"b/ignored.txt": "",
			"b/kept.txt":    "",
			"root.txt":      "",
		})
		assertFindRegressionPaths(t, root, "**/*.txt", []string{"a/kept.txt", "b/ignored.txt", "b/kept.txt", "root.txt"})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/3303-find-nested-gitignore.test.ts:75
	t.Run("scopes each .gitignore to its own subtree", func(t *testing.T) {
		root := findRegressionTree(t, map[string]string{
			"a/.gitignore":       "ignored.txt\n",
			"a/deep/.gitignore":  "secret.txt\n",
			"a/ignored.txt":      "",
			"a/kept.txt":         "",
			"a/deep/ignored.txt": "",
			"a/deep/secret.txt":  "",
			"a/deep/kept.txt":    "",
			"b/ignored.txt":      "",
			"b/kept.txt":         "",
			"root.txt":           "",
		})
		assertFindRegressionPaths(t, root, "**/*.txt", []string{"a/deep/kept.txt", "a/kept.txt", "b/ignored.txt", "b/kept.txt", "root.txt"})
	})
}

func findRegressionTree(t *testing.T, files map[string]string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func assertFindRegressionPaths(t *testing.T, root, pattern string, want []string) {
	t.Helper()
	result := runFind(t, root, map[string]any{"pattern": pattern})
	if result.IsError {
		t.Fatalf("find(%q): %s", pattern, result.Text())
	}
	got := outputLines(result.Text())
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("find(%q) = %q, want %q", pattern, got, want)
	}
}
