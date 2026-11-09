package app

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// FilesFacet's default for the uploads under way (files.ts: 64 per object when none is given): the 64th begin is accepted and
// the 65th refused `unavailable/busy`.

func TestFilesFacetDefaultsTo64UploadsUnderWay(t *testing.T) {
	f := filesOf(t)
	begin := func(index int) []any {
		return []any{"space", "shared", "path", fmt.Sprintf("u%d", index), "mime", "x", "source", "test"}
	}
	for index := range 64 {
		result := f.must("begin", begin(index)...).(*delta.JsonObject)
		if str(result, "kind") != "upload" {
			t.Fatalf("begin %d answered %v, want an upload", index, result)
		}
	}
	_, err := f.call("begin", begin(64)...)
	_ = wantCode(t, err, data.CodeUnavailable, data.ReasonBusy)
	f.close()
}
