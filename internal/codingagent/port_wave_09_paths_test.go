package codingagent

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestPortWave09Paths(t *testing.T) {
	home := isolatePathTestHome(t)

	t.Run("canonicalizePath", func(t *testing.T) {
		t.Parallel()
		// upstream: packages/coding-agent/test/paths.test.ts:30
		t.Run("returns the real path for a regular file", func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "file.txt")
			writeContextFile(t, file, "hello")
			requirePathResult(t, CanonicalizePath(file), realPathForTest(t, file))
		})

		// upstream: packages/coding-agent/test/paths.test.ts:37
		t.Run("resolves symlinks to their targets", func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "target.txt")
			link := filepath.Join(dir, "link.txt")
			writeContextFile(t, target, "hello")
			testenv.RequireSymlink(t, target, link)
			requirePathResult(t, CanonicalizePath(link), realPathForTest(t, target))
		})

		// upstream: packages/coding-agent/test/paths.test.ts:46
		t.Run("resolves directory symlinks", func(t *testing.T) {
			dir := t.TempDir()
			targetDir := filepath.Join(dir, "target-dir")
			linkDir := filepath.Join(dir, "link-dir")
			if err := os.Mkdir(targetDir, 0o700); err != nil {
				t.Fatal(err)
			}
			testenv.RequireDirectoryLink(t, targetDir, linkDir)
			requirePathResult(t, CanonicalizePath(linkDir), realPathForTest(t, targetDir))
		})

		// upstream: packages/coding-agent/test/paths.test.ts:55
		t.Run("falls back to the raw path when the target does not exist", func(t *testing.T) {
			nonexistent := filepath.Join(t.TempDir(), "no-such-file")
			requirePathResult(t, CanonicalizePath(nonexistent), nonexistent)
		})

		// upstream: packages/coding-agent/test/paths.test.ts:61
		t.Run("falls back to the raw path for a dangling symlink", func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "target.txt")
			link := filepath.Join(dir, "link.txt")
			testenv.RequireSymlink(t, target, link)
			requirePathResult(t, CanonicalizePath(link), link)
		})
	})

	t.Run("getCwdRelativePath", func(t *testing.T) {
		t.Parallel()
		// upstream: packages/coding-agent/test/paths.test.ts:73
		t.Run("keeps cwd-relative names that start with dots", func(t *testing.T) {
			cwd := filepath.Join(os.TempDir(), "pi-paths-cwd")
			requirePathResult(t, GetCwdRelativePath(filepath.Join(cwd, "..config", "AGENTS.md"), cwd), filepath.Join("..config", "AGENTS.md"))
		})

		// upstream: packages/coding-agent/test/paths.test.ts:78
		t.Run("rejects parent-directory traversals", func(t *testing.T) {
			cwd := filepath.Join(os.TempDir(), "pi-paths-cwd")
			// Go represents the undefined outside-cwd result with the empty string.
			requirePathResult(t, GetCwdRelativePath(filepath.Join(cwd, "..", "AGENTS.md"), cwd), "")
		})
	})

	t.Run("resolvePath", func(t *testing.T) {
		t.Parallel()
		// upstream: packages/coding-agent/test/paths.test.ts:85
		t.Run("expands only home tilde shortcuts", func(t *testing.T) {
			cwd := filepath.Join(os.TempDir(), "pi-paths-cwd")
			got, err := normalizeSettingsPath("~")
			if err != nil {
				t.Fatal(err)
			}
			requirePathResult(t, got, home)
			got, err = normalizeSettingsPath("~/file.txt")
			if err != nil {
				t.Fatal(err)
			}
			requirePathResult(t, got, filepath.Join(home, "file.txt"))
			got, err = ResolvePath("~draft.md", cwd)
			if err != nil {
				t.Fatal(err)
			}
			requirePathResult(t, got, filepath.Join(cwd, "~draft.md"))
			got, err = normalizeSettingsPath("~draft.md")
			if err != nil {
				t.Fatal(err)
			}
			requirePathResult(t, got, "~draft.md")
		})

		// upstream: packages/coding-agent/test/paths.test.ts:93
		t.Run("resolves relative paths against the base directory", func(t *testing.T) {
			cwd := filepath.Join(os.TempDir(), "pi-paths-cwd")
			got, err := ResolvePath("subdir/file.txt", cwd)
			if err != nil {
				t.Fatal(err)
			}
			requirePathResult(t, got, filepath.Join(cwd, "subdir", "file.txt"))
			got, err = ResolvePath("subdir/file.txt", fileURLForTest(cwd).String())
			if err != nil {
				t.Fatal(err)
			}
			requirePathResult(t, got, filepath.Join(cwd, "subdir", "file.txt"))
		})

		// upstream: packages/coding-agent/test/paths.test.ts:99
		t.Run("accepts file URLs", func(t *testing.T) {
			dir := t.TempDir()
			filePath := filepath.Join(dir, "file with spaces.txt")
			got, err := ResolvePath(fileURLForTest(filePath).String(), filepath.Join(dir, "base"))
			if err != nil {
				t.Fatal(err)
			}
			requirePathResult(t, got, filePath)
		})

		// upstream: packages/coding-agent/test/paths.test.ts:105
		t.Run("throws for invalid file URLs", func(t *testing.T) {
			if got, err := ResolvePath("file:///%E0%A4%A", ""); err == nil {
				t.Fatalf("ResolvePath returned %q without the expected file URL error", got)
			}
		})

		// upstream: packages/coding-agent/test/paths.test.ts:109-117: the case applies only to POSIX hosts and iterates all three percent-bearing filenames.
		if runtime.GOOS != "windows" {
			t.Run("preserves POSIX absolute paths with literal percent sequences", func(t *testing.T) {
				dir := t.TempDir()
				for _, name := range []string{"report%2026.md", "foo%2Fbar", "malformed%A.md"} {
					t.Run(name, func(t *testing.T) {
						filePath := filepath.Join(dir, name)
						got, err := ResolvePath(filePath, filepath.Join(dir, "base"))
						if err != nil {
							t.Fatal(err)
						}
						requirePathResult(t, got, filePath)
					})
				}
			})
		}

		// upstream: packages/coding-agent/test/paths.test.ts:120-130: retain the Windows-only predicate without suppressing failures on Windows.
		if runtime.GOOS == "windows" {
			t.Run("does not treat Windows file URL pathname strings as native paths", func(t *testing.T) {
				filePath := filepath.Join(t.TempDir(), "dir", "SKILL.md")
				pathname := fileURLForTest(filePath).EscapedPath()
				if !regexp.MustCompile(`^/[A-Za-z]:`).MatchString(pathname) {
					t.Fatalf("file URL pathname %q does not begin with /<drive>:", pathname)
				}
				want, err := nodepath.Resolve(pathname)
				if err != nil {
					t.Fatal(err)
				}
				got, err := ResolvePath(pathname, `E:\project`)
				if err != nil {
					t.Fatal(err)
				}
				requirePathResult(t, got, want)
			})
		}
	})

	t.Run("normalizeWindowsShellPath", func(t *testing.T) {
		t.Parallel()
		// upstream: packages/coding-agent/test/paths.test.ts:134-139
		t.Run("converts Git Bash, MSYS, Cygwin, and WSL drive paths", func(t *testing.T) {
			requirePathResult(t, tools.NormalizeWindowsShellPath("/c/Users/example/project"), `C:\Users\example\project`)
			requirePathResult(t, tools.NormalizeWindowsShellPath("/cygdrive/d/work"), `D:\work`)
			requirePathResult(t, tools.NormalizeWindowsShellPath("/mnt/e/source"), `E:\source`)
			requirePathResult(t, tools.NormalizeWindowsShellPath("/c"), `C:\`)
		})

		// upstream: packages/coding-agent/test/paths.test.ts:141-152: every row of the unchanged-path table.
		t.Run("leaves other path forms unchanged", func(t *testing.T) {
			for _, path := range []string{
				"C:/Users/example",
				`C:\Users\example`,
				"//server/share/file",
				`/c/Users\example`,
				"relative/file",
				"/tmp/file",
			} {
				t.Run(path, func(t *testing.T) {
					requirePathResult(t, tools.NormalizeWindowsShellPath(path), path)
				})
			}
		})

		// upstream: packages/coding-agent/test/paths.test.ts:154-157: it.runIf(win32).
		if runtime.GOOS == "windows" {
			t.Run("is applied by normal path handling on Windows", func(t *testing.T) {
				got, err := normalizeSettingsPath("/c/Users/example")
				if err != nil {
					t.Fatal(err)
				}
				requirePathResult(t, got, `C:\Users\example`)
				got, err = ResolvePath("/mnt/c/Users/example", `D:\work`)
				if err != nil {
					t.Fatal(err)
				}
				requirePathResult(t, got, filepath.Clean("C:/Users/example"))
			})
		}
	})

	t.Run("isLocalPath", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name  string
			input string
			want  bool
		}{
			// upstream: packages/coding-agent/test/paths.test.ts:161
			{"returns true for bare names", "my-package", true},
			// upstream: packages/coding-agent/test/paths.test.ts:165
			{"returns true for relative paths", "./foo", true},
			// upstream: packages/coding-agent/test/paths.test.ts:169
			{"returns true for file URLs", "file:///tmp/foo", true},
			// upstream: packages/coding-agent/test/paths.test.ts:173
			{"returns false for npm: protocol", "npm:package", false},
			// upstream: packages/coding-agent/test/paths.test.ts:177
			{"returns false for git: protocol", "git://repo", false},
			// upstream: packages/coding-agent/test/paths.test.ts:181
			{"returns false for https: protocol", "https://example.com", false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				requirePathResult(t, IsLocalPath(tc.input), tc.want)
			})
		}
	})
}
