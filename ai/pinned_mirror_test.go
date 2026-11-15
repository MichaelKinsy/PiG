package ai

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
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

// oraclePiAIDist is dist of the pi-ai the installed oracle runs.
const oraclePiAIDist = "../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist"

// readOracleProviderData reads providers/data/<provider>.json of the installed oracle's pi-ai. That is the catalog data of the release line's Pi:
// what Pi's own generator (packages/ai/catalog/generate.ts) makes from the recording in automation/gen/catalog-source (automation/pin/oracle.py
// installs it), not the frozen bytes the pinned release shipped, which the mirror (.upstream/current) still holds. A test that wants Pi's model data
// reads it here, so its denominator is Pi's generator and never this repository's.
func readOracleProviderData(t *testing.T, provider string) []byte {
	t.Helper()
	path := filepath.Join(oraclePiAIDist, "providers", "data", provider+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		pioracle.Missing(t, "the oracle Pi's provider data "+path, err)
	}
	return data
}
