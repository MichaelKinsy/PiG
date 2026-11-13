package app

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports the contract of packages/chord/src/node/app/files-index.ts (and the `turn` of files.ts that drives it): every change of the
// index is one transaction decided by the one change running, so changes started together take turns.

func TestChangesStartedTogetherTakeTurnsSoVersionsAndKeysAreDecidedOnce(t *testing.T) {
	f := filesOf(t)
	const writers = 8
	puts := make([]*pending, writers)
	for index := range puts {
		puts[index] = f.start("put", "space", "shared", "path", "a.txt", "mime", "text/plain", "source", "test", "bytes", []byte(fmt.Sprintf("v%d", index)))
	}
	seen := map[float64]bool{}
	for _, call := range puts {
		value, err := call.wait()
		if err != nil {
			t.Fatal(err)
		}
		seen[num(value.(*delta.JsonObject), "version")] = true
	}
	for version := 1; version <= writers; version++ {
		if !seen[float64(version)] {
			t.Fatalf("versions given = %v, want each of 1..%d once (a version number was decided twice)", seen, writers)
		}
	}
	keyed := make([]*pending, writers)
	for index := range keyed {
		keyed[index] = f.start("put", "space", "shared", "path", fmt.Sprintf("k%d.txt", index), "mime", "text/plain", "source", "test", "key", "same:1", "bytes", []byte("x"))
	}
	ids := map[string]bool{}
	for _, call := range keyed {
		value, err := call.wait()
		if err != nil {
			t.Fatal(err)
		}
		ids[str(value.(*delta.JsonObject), "id")] = true
	}
	if len(ids) != 1 {
		t.Fatalf("writes with one key made %d versions, want the one the first made", len(ids))
	}
	f.close()
}
