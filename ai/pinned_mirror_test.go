package ai

import (
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/upstreammirror"
)

const pinnedMirrorPrefix = "../.upstream/current/"

var pickedMirrorPaths = sync.OnceValues(func() ([]string, error) { return upstreammirror.PickedPaths("..") })

// readPinnedMirror reads a file of the Pi source mirror (a path below ../.upstream/current) as the release line's Pi has it: the pinned
// release's file, plus the post-release Pi fixes the release carries for the files automation/gen/pi-post-release-backports.mjs covers.
// The built-in tables and catalogs follow those fixes, so a comparison with Pi's sources or provider data reads them here.
func readPinnedMirror(t *testing.T, path string) []byte {
	t.Helper()
	picked, err := pickedMirrorPaths()
	if err != nil {
		t.Fatal(err)
	}
	rel, ok := strings.CutPrefix(path, pinnedMirrorPrefix)
	if !ok {
		t.Fatalf("%s is not a path of the Pi mirror", path)
	}
	var data []byte
	if slices.Contains(picked, rel) {
		data, err = upstreammirror.ReadPicked("..", rel)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		t.Fatal(err)
	}
	return data
}
