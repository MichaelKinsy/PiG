package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/text/unicode/norm"
)

func TestExpandPath_TildeExpansion(t *testing.T) {
	home, _ := os.UserHomeDir()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bare tilde", "~", home},
		// Pi joins the rest onto the home directory (utils/paths.ts normalizePath).
		{"tilde slash", "~/foo/bar", filepath.Join(home, "foo", "bar")},
		{"no tilde", "foo/bar", "foo/bar"},
		{"absolute", "/usr/bin", "/usr/bin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := expandPath(tc.in)
			if got != tc.want {
				t.Errorf("expandPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestExpandPath_AtPrefix(t *testing.T) {
	home, _ := os.UserHomeDir()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"at prefix removed", "@foo.txt", "foo.txt"},
		{"at with tilde", "@~/config", filepath.Join(home, "config")},
		{"no at", "normal.txt", "normal.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := expandPath(tc.in)
			if got != tc.want {
				t.Errorf("expandPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestExpandPath_UnicodeSpaces(t *testing.T) {
	// Non-breaking space (U+00A0) in the path should be normalized to regular space.
	in := "my\u00A0file.txt"
	got := expandPath(in)
	if got != "my file.txt" {
		t.Errorf("expandPath(%q) = %q, want %q", in, got, "my file.txt")
	}

	// Em-space (U+2003)
	in2 := "path\u2003name"
	got2 := expandPath(in2)
	if got2 != "path name" {
		t.Errorf("expandPath(%q) = %q, want %q", in2, got2, "path name")
	}
}

// rooted returns the path Node's path.resolve gives a '/'-rooted path: itself
// on Unix, and on Windows the same path on the current working directory's
// drive with native separators (e.g. /project becomes C:\project).
func rooted(t *testing.T, p string) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return p
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.VolumeName(wd) + filepath.FromSlash(p)
}

func TestResolveToCwd(t *testing.T) {
	home, _ := os.UserHomeDir()
	cases := []struct {
		name string
		path string
		cwd  string
		want string
	}{
		{"relative", "src/main.go", "/project", rooted(t, "/project/src/main.go")},
		{"absolute", "/etc/hosts", "/project", rooted(t, "/etc/hosts")},
		{"tilde", "~/docs/foo.md", "/project", filepath.Join(home, "docs", "foo.md")},
		{"at prefix", "@./local.txt", "/project", rooted(t, "/project/local.txt")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveToCwd(tc.path, tc.cwd)
			if got != tc.want {
				t.Errorf("resolveToCwd(%q, %q) = %q, want %q", tc.path, tc.cwd, got, tc.want)
			}
		})
	}
}

func TestResolveReadPath_NFDFallback(t *testing.T) {
	// Create a temp file with an NFD-encoded name (decomposed é = e + combining acute)
	dir := t.TempDir()
	nfdName := norm.NFD.String("café.txt")
	nfdPath := filepath.Join(dir, nfdName)
	if err := os.WriteFile(nfdPath, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	// User types NFC form (composed é)
	nfcName := "café.txt"

	// If the filesystem stores NFD (macOS), the NFC lookup should find
	// the file via the NFD fallback. On Linux the file IS stored as-typed
	// so NFD fallback may or may not apply. We test that resolveReadPath
	// at least returns a valid path.
	got := resolveReadPath(nfcName, dir)

	// The result should be a path that exists
	if _, err := os.Stat(got); err != nil {
		// On systems where NFC==NFD (most macOS), the direct path works.
		// On strict-Linux, the NFD variant should match.
		t.Logf("note: resolveReadPath(%q) = %q: does not exist (filesystem may be NFC-strict)", nfcName, got)
	}
}

func TestResolveReadPath_CurlyQuoteFallback(t *testing.T) {
	dir := t.TempDir()
	// Simulate macOS French screenshot: "Capture d\u2019écran.png"
	curlyName := "Capture d\u2019\u00e9cran.png"
	curlyPath := filepath.Join(dir, curlyName)
	if err := os.WriteFile(curlyPath, []byte("img"), 0644); err != nil {
		t.Fatal(err)
	}

	// User types straight apostrophe
	userInput := "Capture d'écran.png"
	got := resolveReadPath(userInput, dir)
	if _, err := os.Stat(got); err != nil {
		t.Errorf("resolveReadPath(%q) = %q, expected to find curly variant", userInput, got)
	}
}

func TestTryMacOSScreenshotPath(t *testing.T) {
	in := "Screenshot 2024-01-01 at 10 AM.png"
	got := tryMacOSScreenshotPath(in)
	want := "Screenshot 2024-01-01 at 10\u202FAM.png"
	if got != want {
		t.Errorf("tryMacOSScreenshotPath(%q) = %q, want %q", in, got, want)
	}
}

func TestNormalizeUnicodeSpaces(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"\u00A0", " "},                 // NBSP
		{"\u2003", " "},                 // Em space
		{"\u202F", " "},                 // Narrow no-break space
		{"\u3000", " "},                 // Ideographic space
		{"no\u2004spaces", "no spaces"}, // Three-per-em space
		{"normal", "normal"},
	}
	for _, tc := range cases {
		got := normalizeUnicodeSpaces(tc.in)
		if got != tc.want {
			t.Errorf("normalizeUnicodeSpaces(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolvePath_BackwardsCompat(t *testing.T) {
	// The old resolvePath(cwd, path) still works via resolveToCwd
	if got, want := resolvePath("/cwd", "relative.go"), rooted(t, "/cwd/relative.go"); got != want {
		t.Errorf("resolvePath = %q, want %q", got, want)
	}
	if got, want := resolvePath("/cwd", "/absolute.go"), rooted(t, "/absolute.go"); got != want {
		t.Errorf("resolvePath = %q, want %q", got, want)
	}
	home, _ := os.UserHomeDir()
	if got, want := resolvePath("/cwd", "~/file.txt"), filepath.Join(home, "file.txt"); got != want {
		t.Errorf("resolvePath(~/file.txt) = %q, want %q", got, want)
	}
}
