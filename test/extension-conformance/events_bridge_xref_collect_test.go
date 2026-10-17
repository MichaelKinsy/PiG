package extensionconformance

import (
	"strings"
	"testing"
)

// xrefCollectEnv is the runtime's test switch (xref.mjs installTestCollect): with it set, a Node fixture's collect command runs a full collection and releases the cross-process references the collection freed. A Node process started without it has no such hook.
const xrefCollectEnv = "PIG_TEST_XREF_COLLECT"

// A payload one Node realm emits reaches a listener in another Node realm as a proxy, and the listener's realm holds the Host lease until its collector frees the proxy, as Pi's collector frees the plain object whenever it runs. A collection releases the payload no listener kept, and only that one: the payload a listener kept stays leased and readable through its proxy until the listener drops it.
func TestNodeEventBusCollectReleasesOnlyUnreachablePayloads(t *testing.T) {
	t.Setenv(xrefCollectEnv, "1")
	rig := newBusRig(t,
		busSpec("node", "isolated", "emitter"),
		busSpec("node", "isolated", "listener", busListenerAt{Channel: "drop", Actions: "count"}, busListenerAt{Channel: "keep", Actions: "keep"}),
	)
	leases := func() int { return rig.host.EventBusRetention().Leases }
	before := leases()
	rig.must("emitter", "emit", `drop json {"n":1}`)
	rig.must("emitter", "emit", `keep json {"n":2}`)
	rig.must("listener", "collect", "")
	if got := leases(); got != before+1 {
		t.Fatalf("leases after the listener collected = %d, want %d: one for the kept payload and none for the dropped one", got, before+1)
	}
	rig.must("listener", "kept", "")
	rig.expect(`listener|kept|[{"n":2}]`)
	rig.must("listener", "unkeep", "")
	rig.must("listener", "collect", "")
	if got := leases(); got != before {
		t.Fatalf("leases after the listener dropped the kept payload and collected = %d, want %d", got, before)
	}
}

// The collect hook is a test switch: a Node process started without it refuses the command, so production exposes neither the hook nor --expose-gc.
func TestNodeEventBusCollectNeedsTheTestSwitch(t *testing.T) {
	t.Parallel()
	rig := newBusRig(t, busSpec("node", "isolated", "solo"))
	err := rig.run("solo", "collect", "")
	if err == nil || !strings.Contains(err.Error(), "is not a function") {
		t.Fatalf("collect without %s = %v, want the missing hook's TypeError", xrefCollectEnv, err)
	}
}
