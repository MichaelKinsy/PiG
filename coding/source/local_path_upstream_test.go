package source

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// package-manager.ts:221-223,1375-1393,2145-2151 uses one trimmed,
// HOME-aware resolver for every local identity, including a bare tilde.
func TestLocalIdentityUsesPackagePathResolution(t *testing.T) {
	base, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", t.TempDir())
	urlPath := filepath.ToSlash(filepath.Join(home, "pkg dir"))
	if runtime.GOOS == "windows" {
		urlPath = "/" + urlPath
	}
	for _, tc := range []struct{ input, want string }{
		{"~", home},
		{"~/pkg dir", filepath.Join(home, "pkg dir")},
		{"\uFEFF./pkg\uFEFF", filepath.Join(base, "pkg")},
		{(&url.URL{Scheme: "file", Path: urlPath}).String(), filepath.Join(home, "pkg dir")},
		{"\uFEFF" + (&url.URL{Scheme: "file", Path: urlPath}).String() + "\uFEFF", filepath.Join(home, "pkg dir")},
	} {
		t.Run(tc.input, func(t *testing.T) {
			ref, err := Parse(tc.input, Options{Bare: BareLocal})
			if err != nil {
				t.Fatal(err)
			}
			got, err := ref.Identity(base)
			if err != nil || got != "local:"+tc.want {
				t.Fatalf("Identity(%q) = %q, %v; want local:%s", tc.input, got, err, tc.want)
			}
		})
	}
}

// paths.ts:105 calls nodeResolvePath(normalized) for Windows rooted paths,
// not nodeResolvePath(base, normalized): the process drive wins over base.
func TestLocalIdentityWindowsRootedPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path semantics")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	ref := Ref{Kind: KindLocal, Locator: `\tools\x`}
	got, err := ref.Identity(`Z:\project`)
	want := "local:" + filepath.VolumeName(cwd) + `\tools\x`
	if err != nil || got != want {
		t.Fatalf("Identity = %q, %v; want %q", got, err, want)
	}
}
