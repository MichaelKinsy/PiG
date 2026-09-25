//go:build windows

package tools

import (
	"os"
	"path/filepath"
	"testing"
)

// Expected values are Pi 0.87.1 resolveToCwd (core/tools/path-utils.ts) run
// natively on Windows with Node 24.19.0. Node treats a path rooted at '\' or
// '/' as absolute on the cwd's drive, and returns native separators.
func TestResolveToCwdMatchesPiOnWindows(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	const cwd = `C:\proj`
	for _, tc := range []struct{ path, want string }{
		{"src/main.go", `C:\proj\src\main.go`},
		{`src\win.go`, `C:\proj\src\win.go`},
		{"/etc/hosts", `C:\etc\hosts`},
		{`\etc\hosts`, `C:\etc\hosts`},
		{"~/docs/foo.md", filepath.Join(home, "docs", "foo.md")},
		{`~\docs`, filepath.Join(home, "docs")},
		{"@./local.txt", `C:\proj\local.txt`},
		{"D:/data/x", `D:\data\x`},
		{"C:rel", `C:\proj\rel`},
	} {
		if got := resolveToCwd(tc.path, cwd); got != tc.want {
			t.Errorf("resolveToCwd(%q, %q) = %q, want Pi's %q", tc.path, cwd, got, tc.want)
		}
	}
}

// Expected values are Pi 0.87.1 relativizeFindResultPath (core/tools/find.ts)
// run natively on Windows: a path rooted at '/' is absolute there too, and the
// result always uses '/'.
func TestRelativizeFindResultPathMatchesPiOnWindows(t *testing.T) {
	for _, c := range [][3]string{
		{"/root/a/b.ts", "/root", "a/b.ts"},
		{"/root/dir/", "/root", "dir/"},
		{"rel/x", "/root", "rel/x"},
		{`C:\root\a\b.ts`, `C:\root`, "a/b.ts"},
		{`C:\root\dir\`, `C:\root`, "dir/"},
	} {
		if got := relativizeFindResultPath(c[0], c[1]); got != c[2] {
			t.Errorf("relativizeFindResultPath(%q, %q) = %q, want Pi's %q", c[0], c[1], got, c[2])
		}
	}
}
