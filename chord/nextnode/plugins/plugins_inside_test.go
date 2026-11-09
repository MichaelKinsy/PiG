package plugins

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// Not upstream's: `inside` is `relative(dir, path)` not empty, not starting with `..`, not absolute.
func TestInsideIsAPathUnderADirectoryNotTheDirectoryItselfNorADotDotName(t *testing.T) {
	for _, each := range []struct {
		dir, path string
		want      bool
	}{
		{"/a/b", "/a/b/c", true},
		{"/a/b", "/a/b/c/d.ts", true},
		{"/a/b", "/a/b", false},
		{"/a/b", "/a/c", false},
		{"/a/b", "/a", false},
		{"/a/b", "/a/b/../c", false},
		// A file named `..x` counts as outside, as in upstream.
		{"/a/b", "/a/b/..x", false},
	} {
		if got := model.Inside(each.dir, each.path); got != each.want {
			t.Errorf("inside(%q, %q) = %v, want %v", each.dir, each.path, got, each.want)
		}
	}
}
