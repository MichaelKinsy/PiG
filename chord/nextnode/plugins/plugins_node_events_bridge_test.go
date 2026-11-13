package plugins

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Not upstream's: a type with an event member at the boundary between a Go host and a Node host (plugins_node_contracts.go,
// eventsDoNotCross). Oracle for the rebuilt contract: Pi's own fingerprint, which the Node host computes.

// The Node host rebuilds a contract that crossed as data with its event member as an event member: Pi hashes it to the
// fingerprint the Go contract has. (A rebuild as a method, what contracts.mjs made of an unknown node, hashes to another.)
func TestNodeDescribeRebuildsAnEventMemberAsAnEventMember(t *testing.T) {
	node := evNode(t)
	description, err := node.Describe(evTicker, background)
	if err != nil {
		t.Fatal(err)
	}
	if description.Fingerprint != evTicker.Fingerprint || description.Kind != contracts.KindLocal {
		t.Fatalf("the Node host describes test.ticker as %s %s, the Go contract is local %s: the event member was not rebuilt as one",
			description.Kind, description.Fingerprint, evTicker.Fingerprint)
	}
}

// A type with an event member a Node host serves is not held by the Go side yet (the other direction, a Go core facet's
// type held by a plugin, crosses: plugins_node_bridged_events_test.go): holding one fails on the Go side, before anything
// is sent, with a message that says so, never with a reference whose event member is missing or a method.
func TestNodeGetOfATypeWithAnEventMemberSaysEventsDoNotCrossThePluginsBridge(t *testing.T) {
	node := evNode(t)
	const want = "test.ticker: events do not cross the plugins bridge between a Go host and a Node host, and the type has the event members ticks"
	if ref, err := node.Get(background, evTicker, delta.NewJsonObject(0), host.GetOptions{}); err == nil || err.Error() != want {
		if ref != nil {
			ref.Release()
		}
		t.Fatalf("Get = %v, want the error %q", err, want)
	}
	if ref, err := node.Hold(background, evTicker, delta.NewJsonObject(0)); err == nil || err.Error() != want {
		if ref != nil {
			ref.Release()
		}
		t.Fatalf("Hold = %v, want the error %q", err, want)
	}
}
