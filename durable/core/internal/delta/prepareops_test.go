package delta

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// A host's tracker continues the draft the core edited, so its operations follow the core's own (a conversation created by
// the Harness, whose pi.agent the host then configures in the same transaction).
func TestPrepareThenFollowsTheDraftsOwnEdits(t *testing.T) {
	tracker := Track(parse(t, `{"a":1}`).(*jv.Object))
	change := tracker.BeginChange()
	if err := change.State().Set("b", 2.0); err != nil {
		t.Fatal(err)
	}
	host, _ := DecodeOps(parse(t, `[["s",["c"],3]]`))
	prepared, err := change.PrepareThen(host)
	if err != nil {
		t.Fatal(err)
	}
	if got := jv.Stringify(prepared.Value()); got != `{"a":1,"b":2,"c":3}` {
		t.Fatalf("value = %s", got)
	}
	if got := jv.Stringify(OpsValue(prepared.Ops())); got != `[["s",["b"],2],["s",["c"],3]]` {
		t.Fatalf("ops = %s", got)
	}
}
