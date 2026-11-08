package mcp

import (
	"reflect"
	"slices"
	"testing"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports the placement case of test/p2/mcp-instances.test.ts: a locked session's lease gets only the servers its owner
// picked.

func TestALockedSessionsLeaseGetsOnlyTheServersItsOwnerPicked(t *testing.T) {
	config := mustOwner(t, `{"mcpServers":{"a":{"command":"a"},"b":{"command":"b"}}}`)
	lease := func(session string) Lease {
		return Lease{Session: session, Location: p2runtime.Location{Cwd: "/w"}, Project: none, Generation: 1, Incarnation: "i"}
	}
	lock := p2runtime.SessionLock{MCP: []string{"b"}, ProjectMCP: false, Instructions: false, Capabilities: []p2runtime.LockCapability{}}
	placement := Place(config, []Lease{lease("s1"), lease("s2")}, chordjson.NewObject(0),
		WorkEnvironments{"s1": {""}, "s2": {""}}, map[string]p2runtime.SessionLock{"s2": lock})
	servers := func(session string) []string {
		var names []string
		for _, placed := range placement.Leases {
			if placed.Lease.Session == session {
				for _, use := range placed.Servers {
					names = append(names, use.Server)
				}
			}
		}
		slices.Sort(names)
		return names
	}
	if got := servers("s1"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("s1: %v", got)
	}
	if got := servers("s2"); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("s2: %v", got)
	}
}
