package tools

// Ports packages/coding-agent/test/suite/regressions/12066-find-ancestor-glob.test.ts (Pi 282b01c80, #12116).

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Regression test for https://github.com/earendil-works/pi/issues/12066
//
// fd matches --full-path globs against the absolute path, and find prefixed path patterns with "**/". A pattern could therefore match
// through folders above the search path: with the project in .../src/app, "src/**/*.ts" returned every .ts file in the project. Path
// patterns are anchored at the search path, like grep's glob.
func TestFindPathGlobsDoNotMatchAboveTheSearchPath(t *testing.T) {
	newRoot := func(t *testing.T) string {
		t.Helper()
		tempRoot, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return tempRoot
	}
	createProject := func(t *testing.T, root string) {
		t.Helper()
		for _, dir := range []string{"src", "lib", "test", "pkg/src"} {
			if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		for _, file := range []string{"src/a.ts", "lib/b.ts", "test/c.ts", "pkg/src/d.ts"} {
			if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(file)), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	find := func(t *testing.T, searchPath, pattern string) []string {
		t.Helper()
		text := runFind(t, searchPath, map[string]any{"pattern": pattern}).Text()
		if text == "No files found matching pattern" {
			return nil
		}
		got := strings.Split(text, "\n")
		slices.Sort(got)
		return got
	}
	expect := func(t *testing.T, searchPath, pattern string, want ...string) {
		t.Helper()
		if got := find(t, searchPath, pattern); !slices.Equal(got, want) {
			t.Errorf("find %q in %s = %q, want %q", pattern, searchPath, got, want)
		}
	}

	t.Run("does not match through an ancestor folder with the same name", func(t *testing.T) {
		project := filepath.Join(newRoot(t), "src", "app")
		createProject(t, project)

		expect(t, project, "src/**/*.ts", "src/a.ts")
		expect(t, project, "app/**/*.ts")
	})

	t.Run("anchors path patterns at the search path; a leading **/ matches at any depth", func(t *testing.T) {
		project := filepath.Join(newRoot(t), "src", "app")
		createProject(t, project)

		expect(t, project, "src/*.ts", "src/a.ts")
		expect(t, project, "./src/*.ts", "src/a.ts")
		expect(t, project, "pkg/src/*.ts", "pkg/src/d.ts")
		expect(t, project, "**/src/*.ts", "pkg/src/d.ts", "src/a.ts")
		expect(t, project, "**/src/**/*.ts", "pkg/src/d.ts", "src/a.ts")
		// Basename and absolute patterns are unchanged.
		expect(t, project, "*.ts", "lib/b.ts", "pkg/src/d.ts", "src/a.ts", "test/c.ts")
		absolute := strings.ReplaceAll(project, `\`, "/") + "/lib/*.ts"
		expect(t, project, absolute, "lib/b.ts")
	})

	t.Run("treats glob characters in the search path literally", func(t *testing.T) {
		// `*` and `?` are not allowed in Windows file names.
		name := "w*?[1]{x,y}"
		if runtime.GOOS == "windows" {
			name = "w[1]{x,y}"
		}
		project := filepath.Join(newRoot(t), name, "src")
		createProject(t, project)

		expect(t, project, "src/*.ts", "src/a.ts")
	})

	t.Run("keeps smart case when the search path has upper-case letters", func(t *testing.T) {
		project := filepath.Join(newRoot(t), "Upper")
		if err := os.MkdirAll(filepath.Join(project, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, file := range []string{"A.ts", "b.ts"} {
			if err := os.WriteFile(filepath.Join(project, "src", file), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}

		expect(t, project, "src/a.ts", "src/A.ts")
		expect(t, project, "src/B.ts")
	})

	t.Run("works through a symlinked search path", func(t *testing.T) {
		tempRoot := newRoot(t)
		project := filepath.Join(tempRoot, "src", "app")
		createProject(t, project)
		link := filepath.Join(tempRoot, "src", "link")
		// A junction on Windows needs no extra privileges.
		testenv.RequireDirectoryLink(t, project, link)

		expect(t, link, "src/**/*.ts", "src/a.ts")
	})
}
