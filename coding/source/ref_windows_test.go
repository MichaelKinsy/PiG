//go:build windows

package source

import (
	"os"
	"path/filepath"
	"testing"
)

// Pi treats every configured source without an npm or git form as a local
// path (package-manager.ts parseSource, paths.ts isLocalPath), and its config
// selector writes relative sources with the platform separator. On Windows a
// backslash-relative source such as ..\..\pkg is therefore local, even at the
// install boundary where a bare name means npm.
func TestParseBackslashRelativeSourcesAreLocalOnWindows(t *testing.T) {
	base := t.TempDir()
	for _, tc := range []struct{ input, want string }{
		{`..\..\pkg`, filepath.Join(base, "..", "..", "pkg")},
		{`.\pkg`, filepath.Join(base, "pkg")},
		{`..`, filepath.Dir(base)},
	} {
		for _, bare := range []BarePolicy{BareNPM, BareReject} {
			ref, err := Parse(tc.input, Options{Bare: bare})
			if err != nil {
				t.Fatalf("Parse(%q, bare=%d): %v", tc.input, bare, err)
			}
			if ref.Kind != KindLocal {
				t.Fatalf("Parse(%q, bare=%d).Kind = %q, want local", tc.input, bare, ref.Kind)
			}
			id, err := ref.Identity(base)
			if err != nil {
				t.Fatal(err)
			}
			if want := "local:" + filepath.Clean(tc.want); id != want {
				t.Fatalf("Identity(%q) = %q, want %q", tc.input, id, want)
			}
		}
	}
}

// Pi expands ~\ like ~/ on win32 (paths.ts normalizePath).
func TestParseTildeBackslashSourceExpandsHomeOnWindows(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	ref, err := Parse(`~\pkgs\tools`, Options{Bare: BareNPM})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Kind != KindLocal {
		t.Fatalf("Kind = %q, want local", ref.Kind)
	}
	id, err := ref.Identity(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if want := "local:" + filepath.Join(home, "pkgs", "tools"); id != want {
		t.Fatalf("Identity = %q, want %q", id, want)
	}
}
