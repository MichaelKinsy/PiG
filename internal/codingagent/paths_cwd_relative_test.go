package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

// Pi's getCwdRelativePath returns path.relative's result, which uses the
// platform separator; only formatPathRelativeToCwdOrAbsolute joins with "/".
func TestGetCwdRelativePathKeepsPlatformSeparators(t *testing.T) {
	cwd := filepath.Join(os.TempDir(), "work")
	if got, want := GetCwdRelativePath(filepath.Join(cwd, ".pi", "SYSTEM.md"), cwd), filepath.Join(".pi", "SYSTEM.md"); got != want {
		t.Fatalf("GetCwdRelativePath = %q, want %q", got, want)
	}
	if got := GetCwdRelativePath(filepath.Join(filepath.Dir(cwd), "other", "AGENTS.md"), cwd); got != "" {
		t.Fatalf("GetCwdRelativePath outside cwd = %q, want empty", got)
	}
}

func TestFormatPathRelativeToCwdOrAbsoluteJoinsWithSlashes(t *testing.T) {
	cwd := filepath.Join(os.TempDir(), "work")
	if got := FormatPathRelativeToCwdOrAbsolute(filepath.Join(cwd, ".pi", "SYSTEM.md"), cwd); got != ".pi/SYSTEM.md" {
		t.Fatalf("inside cwd = %q, want .pi/SYSTEM.md", got)
	}
	outside := filepath.Join(filepath.Dir(cwd), "other", "AGENTS.md")
	if got, want := FormatPathRelativeToCwdOrAbsolute(outside, cwd), filepath.ToSlash(outside); got != want {
		t.Fatalf("outside cwd = %q, want %q", got, want)
	}
}

// Node's path.resolve treats a rooted path without a drive (/x or \x) as
// absolute on Windows, on the drive of the directory it resolves against.
func TestResolveAgainstCwdTreatsRootedPathsAsNodeDoes(t *testing.T) {
	cwd := filepath.Join(os.TempDir(), "work")
	want := filepath.Clean(filepath.VolumeName(cwd) + filepath.FromSlash("/tmp/project/.pi/SYSTEM.md"))
	for _, rooted := range []string{"/tmp/project/.pi/SYSTEM.md", filepath.FromSlash("/tmp/project/.pi/SYSTEM.md")} {
		if got := resolveAgainstCwd(rooted, cwd); got != want {
			t.Fatalf("resolveAgainstCwd(%q) = %q, want %q", rooted, got, want)
		}
	}
	if got, want := resolveAgainstCwd(filepath.Join("sub", "AGENTS.md"), cwd), filepath.Join(cwd, "sub", "AGENTS.md"); got != want {
		t.Fatalf("relative path = %q, want %q", got, want)
	}
}
