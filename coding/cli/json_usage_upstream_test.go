package cli

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/7911-json-stream-usage.test.ts:13
func TestJSONUpdatesIncludeCumulativeUsageWithoutSnapshotsUpstream(t *testing.T) {
	for _, update := range collectUpstreamJSONUpdates(t, "respond", []ai.FauxResponse{fauxTextResponse("hello")}) {
		if update.usage.TotalTokens <= 0 {
			continue
		}
		if want := decodeRPCEvent(t, update.usage); !reflect.DeepEqual(update.wire["usage"], want) {
			t.Fatalf("usage=%#v, want %#v", update.wire["usage"], want)
		}
		assertDeltaOnlyUpstream(t, update.wire)
		return
	}
	t.Fatal("expected assistant update with populated usage")
}
