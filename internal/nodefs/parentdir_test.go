package nodefs

import (
	"path/filepath"
	"runtime"
	"testing"
)

// A clean path's parent by slicing is filepath.Dir's, all the way up to the root; on Windows that includes drive and UNC
// volumes, whose root is the volume plus one separator.
func TestParentDirMatchesFilepathDir(t *testing.T) {
	sep := string(filepath.Separator)
	raws := []string{sep, sep + "a", sep + "a" + sep + "b", sep + "a" + sep + "b" + sep + "c", "a", "a" + sep + "b", ".", ""}
	if runtime.GOOS == "windows" {
		raws = append(raws, `C:\`, `C:\a`, `C:\a\b\c`, `C:a`, `\\server\share`, `\\server\share\a\b`, `\\?\C:\a\b`)
	}
	for _, raw := range raws {
		p := filepath.Clean(raw)
		if raw == "" {
			continue
		}
		for range 6 {
			if got, want := ParentDir(p, true), filepath.Dir(p); got != want {
				t.Fatalf("ParentDir(%q) = %q, want %q", p, got, want)
			}
			p = filepath.Dir(p)
		}
	}
	if got, want := ParentDir(sep+"a"+sep+".."+sep+"b", false), sep+"a"; got == want {
		t.Fatalf("unclean path was sliced: %q", got)
	}
}
