package tools

// pi: packages/coding-agent/src/core/tools/path-utils.ts

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPortWave11PathUtils(t *testing.T) {
	// EvalSymlinks gives the long form of a Windows 8.3 temp path
	// (C:\Users\RUNNER~1\...), whose "~" is not a home prefix.
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())

	t.Run("expandPath", func(t *testing.T) {
		t.Parallel()
		// upstream: packages/coding-agent/test/path-utils.test.ts:9
		t.Run("should expand ~ to home directory", func(t *testing.T) {
			if result := expandPath("~"); strings.Contains(result, "~") {
				t.Fatalf("expandPath(~) = %q, must not contain ~", result)
			}
		})

		// upstream: packages/coding-agent/test/path-utils.test.ts:14
		t.Run("should expand ~/path to home directory", func(t *testing.T) {
			if result := expandPath("~/Documents/file.txt"); strings.Contains(result, "~/") {
				t.Fatalf("expandPath(~/Documents/file.txt) = %q, must not contain ~/", result)
			}
		})

		// upstream: packages/coding-agent/test/path-utils.test.ts:19
		t.Run("should keep tilde-prefixed filenames literal", func(t *testing.T) {
			for _, input := range []string{"~draft.md", "@~draft.md"} {
				if result := expandPath(input); result != "~draft.md" {
					t.Fatalf("expandPath(%q) = %q, want ~draft.md", input, result)
				}
			}
		})

		// upstream: packages/coding-agent/test/path-utils.test.ts:24
		t.Run("should normalize Unicode spaces", func(t *testing.T) {
			if result := expandPath("file\u00a0name.txt"); result != "file name.txt" {
				t.Fatalf("expandPath(NBSP) = %q, want file name.txt", result)
			}
		})
	})

	t.Run("resolveToCwd", func(t *testing.T) {
		t.Parallel()
		// upstream: packages/coding-agent/test/path-utils.test.ts:33
		t.Run("should resolve absolute paths as-is", func(t *testing.T) {
			absolutePath, err := filepath.Abs(filepath.Join(os.TempDir(), "absolute", "path", "file.txt"))
			if err != nil {
				t.Fatal(err)
			}
			cwd, err := filepath.Abs(filepath.Join(os.TempDir(), "some", "cwd"))
			if err != nil {
				t.Fatal(err)
			}
			if result := mustResolveToCwd(t, absolutePath, cwd); result != absolutePath {
				t.Fatalf("resolveToCwd = %q, want %q", result, absolutePath)
			}
		})

		// upstream: packages/coding-agent/test/path-utils.test.ts:39
		t.Run("should resolve relative paths against cwd", func(t *testing.T) {
			want, err := filepath.Abs(filepath.Join("/some/cwd", "relative/file.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if result := mustResolveToCwd(t, "relative/file.txt", "/some/cwd"); result != want {
				t.Fatalf("resolveToCwd = %q, want %q", result, want)
			}
		})

		// upstream: packages/coding-agent/test/path-utils.test.ts:44
		t.Run("should resolve tilde-prefixed filenames against cwd", func(t *testing.T) {
			cwd := filepath.Join(os.TempDir(), "pi-path-utils-cwd")
			want, err := filepath.Abs(filepath.Join(cwd, "~draft.md"))
			if err != nil {
				t.Fatal(err)
			}
			for _, input := range []string{"~draft.md", "@~draft.md"} {
				if result := mustResolveToCwd(t, input, cwd); result != want {
					t.Fatalf("resolveToCwd(%q) = %q, want %q", input, result, want)
				}
			}
		})
	})

	t.Run("resolveReadPath", func(t *testing.T) {
		// findRegressionTree changes HOME and XDG_CONFIG_HOME, so this group stays serial.
		// upstream: packages/coding-agent/test/path-utils.test.ts:71
		t.Run("should resolve existing file path", func(t *testing.T) {
			fileName := "test-file.txt"
			tempDir := findRegressionTree(t, map[string]string{fileName: "content"})
			if result := mustResolveReadPath(t, fileName, tempDir); result != filepath.Join(tempDir, fileName) {
				t.Fatalf("resolveReadPath = %q, want %q", result, filepath.Join(tempDir, fileName))
			}
		})

		// upstream: packages/coding-agent/test/path-utils.test.ts:79
		t.Run("should handle NFC vs NFD Unicode normalization (macOS filenames with accents)", func(t *testing.T) {
			nfdFileName := "file\u0065\u0301.txt"
			nfcFileName := "file\u00e9.txt"
			if nfdFileName == nfcFileName {
				t.Fatal("NFD and NFC names must differ")
			}
			if bytes.Equal([]byte(nfdFileName), []byte(nfcFileName)) {
				t.Fatal("NFD and NFC byte sequences must differ")
			}
			tempDir := findRegressionTree(t, map[string]string{nfdFileName: "content"})
			result := mustResolveReadPath(t, nfcFileName, tempDir)
			if !strings.Contains(result, tempDir) {
				t.Fatalf("resolveReadPath = %q, must contain %q", result, tempDir)
			}
			if !regexp.MustCompile(`file.+\.txt$`).MatchString(result) {
				t.Fatalf("resolveReadPath = %q, must match file.+\\.txt$", result)
			}
		})

		// upstream: packages/coding-agent/test/path-utils.test.ts:107
		t.Run("should handle curly quotes vs straight quotes (macOS filenames)", func(t *testing.T) {
			curlyQuoteName := "Capture d\u2019cran.txt"
			straightQuoteName := "Capture d'cran.txt"
			if curlyQuoteName == straightQuoteName {
				t.Fatal("curly and straight quote names must differ")
			}
			tempDir := findRegressionTree(t, map[string]string{curlyQuoteName: "content"})
			if result := mustResolveReadPath(t, straightQuoteName, tempDir); result != filepath.Join(tempDir, curlyQuoteName) {
				t.Fatalf("resolveReadPath = %q, want %q", result, filepath.Join(tempDir, curlyQuoteName))
			}
		})

		// upstream: packages/coding-agent/test/path-utils.test.ts:127
		t.Run("should handle combined NFC + curly quote (French macOS screenshots)", func(t *testing.T) {
			nfcCurlyName := "Capture d\u2019\u00e9cran.txt"
			nfcStraightName := "Capture d'\u00e9cran.txt"
			if nfcCurlyName == nfcStraightName {
				t.Fatal("NFC curly and straight quote names must differ")
			}
			tempDir := findRegressionTree(t, map[string]string{nfcCurlyName: "content"})
			if result := mustResolveReadPath(t, nfcStraightName, tempDir); result != filepath.Join(tempDir, nfcCurlyName) {
				t.Fatalf("resolveReadPath = %q, want %q", result, filepath.Join(tempDir, nfcCurlyName))
			}
		})

		// upstream: packages/coding-agent/test/path-utils.test.ts:144
		t.Run("should handle macOS screenshot AM/PM variant with narrow no-break space", func(t *testing.T) {
			macosName := "Screenshot 2024-01-01 at 10.00.00\u202fAM.png"
			userName := "Screenshot 2024-01-01 at 10.00.00 AM.png"
			tempDir := findRegressionTree(t, map[string]string{macosName: "content"})
			if result := mustResolveReadPath(t, userName, tempDir); result != filepath.Join(tempDir, macosName) {
				t.Fatalf("resolveReadPath = %q, want %q", result, filepath.Join(tempDir, macosName))
			}
		})

		// upstream: packages/coding-agent/test/path-utils.test.ts:159
		t.Run("should handle macOS screenshot lowercase am/pm variant (en_AU locale)", func(t *testing.T) {
			macosName := "Screenshot 2024-01-01 at 10.00.00\u202fam.png"
			userName := "Screenshot 2024-01-01 at 10.00.00 am.png"
			tempDir := findRegressionTree(t, map[string]string{macosName: "content"})
			if result := mustResolveReadPath(t, userName, tempDir); result != filepath.Join(tempDir, macosName) {
				t.Fatalf("resolveReadPath = %q, want %q", result, filepath.Join(tempDir, macosName))
			}
		})
	})
}
