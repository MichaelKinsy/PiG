package plugins

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Not upstream's: a type with an event member at the boundary between a Go host and a Node host (plugins_node_contracts.go).
// Oracle for the rebuilt contract: Pi's own fingerprint, which the Node host computes.

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
