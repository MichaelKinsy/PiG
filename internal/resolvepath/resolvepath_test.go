package resolvepath

// pi: packages/coding-agent/src/utils/paths.ts

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// paths.ts:75-106: trim is optional, file URLs are case-sensitive, and baseDir
// normalization does not inherit the input's trim or homeDir options.
func TestResolvePathOptions(t *testing.T) {
	base, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", t.TempDir())
	for _, tc := range []struct {
		name, input, want string
		resolve           func(string, string) (string, error)
	}{
		{"empty", "", base, Resolve},
		{"literal spaces", " rel ", filepath.Join(base, " rel "), Resolve},
		{"trim BOM", "\uFEFF rel \uFEFF", filepath.Join(base, "rel"), ResolveTrimmed},
		{"retain NEL", "\u0085rel\u0085", filepath.Join(base, "\u0085rel\u0085"), ResolveTrimmed},
		{"package home", " ~/pkg ", filepath.Join(home, "pkg"), ResolvePackagePath},
		{"package bare home", " ~ ", home, ResolvePackagePath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.resolve(tc.input, base)
			if err != nil || got != tc.want {
				t.Fatalf("resolve(%q) = %q, %v; want %q", tc.input, got, err, tc.want)
			}
		})
	}
}

// Pi calls nodeResolvePath(normalized) for every isAbsolute input, including
// Windows rooted paths. A different base volume must not change the result.
func TestResolveWindowsAbsolutePathsIgnoreBase(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path semantics")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ input, want string }{
		{`\tools\x`, filepath.VolumeName(cwd) + `\tools\x`},
		{`/tools/x`, filepath.VolumeName(cwd) + `\tools\x`},
		{`file:///C:/x`, `C:\x`},
		{`file://server/share/x`, `\\server\share\x`},
		{`/c/tools/x`, `C:\tools\x`},
	} {
		for _, base := range []string{`Z:\project`, `\\server\share\base`} {
			got, err := Resolve(tc.input, base)
			if err != nil || got != tc.want {
				t.Errorf("Resolve(%q, %q) = %q, %v; want %q", tc.input, base, got, err, tc.want)
			}
		}
	}
}
