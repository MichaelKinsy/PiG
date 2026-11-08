package mcp

import (
	"reflect"
	"testing"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
)

// Ports test/p2/mcp-leases.test.ts: a generation sets exactly its locations, a newer generation takes the session over and
// refuses an older one, calls go through the setting incarnation's lease, and a view's end releases only its own
// generation's leases; each lease keeps the project configuration sent with its location.

var (
	locA   = p2runtime.Location{Cwd: "/a"}
	locB   = p2runtime.Location{Cwd: "/b"}
	locGPU = p2runtime.Location{Cwd: "/a", Env: new("gpu")}
	none   = ProjectSource{Kind: ProjectNone}
)

func gen(generation int, incarnation ...string) LeaseCaller {
	name := "i" + string(rune('0'+generation))
	if len(incarnation) > 0 {
		name = incarnation[0]
	}
	return LeaseCaller{Session: "s1", Generation: generation, Incarnation: name}
}

func setLeases(t *testing.T, leases []Lease, caller LeaseCaller, locations ...p2runtime.Location) []Lease {
	t.Helper()
	places := make([]LeasePlace, len(locations))
	for i, location := range locations {
		places[i] = LeasePlace{Location: location, Project: none}
	}
	result := SetLocations(leases, caller, places)
	if result.Refused != "" {
		t.Fatalf("refused: %s", result.Refused)
	}
	return result.Leases
}

func sessionCwds(leases []Lease) [][2]string {
	out := [][2]string{}
	for _, lease := range leases {
		out = append(out, [2]string{lease.Session, lease.Location.Cwd})
	}
	return out
}

func TestAGenerationLeasesExactlyItsLocationsLeavingOneOutEndsItsLease(t *testing.T) {
	other := Lease{Session: "s2", Location: locA, Project: none, Generation: 1, Incarnation: "x"}
	first := setLeases(t, []Lease{other}, gen(1), locA, locB, locA)
	if want := [][2]string{{"s2", "/a"}, {"s1", "/a"}, {"s1", "/b"}}; !reflect.DeepEqual(sessionCwds(first), want) {
		t.Fatalf("got %v", sessionCwds(first))
	}
	// The same location on another environment is another lease.
	if got := setLeases(t, first, gen(1), locA, locGPU); len(got) != 3 {
		t.Fatalf("got %v", sessionCwds(got))
	}
	shrunk := setLeases(t, first, gen(1), locB)
	if want := [][2]string{{"s2", "/a"}, {"s1", "/b"}}; !reflect.DeepEqual(sessionCwds(shrunk), want) {
		t.Fatalf("got %v", sessionCwds(shrunk))
	}
	if got := setLeases(t, shrunk, gen(1)); !reflect.DeepEqual(got, []Lease{other}) {
		t.Fatalf("got %v", got)
	}
}

func TestANewerGenerationTakesTheSessionOverAnOlderOneIsRefused(t *testing.T) {
	older := setLeases(t, nil, gen(1), locA, locB)
	newer := setLeases(t, older, gen(2), locA)
	if len(newer) != 1 || newer[0].Location.Cwd != "/a" || newer[0].Generation != 2 {
		t.Fatalf("got %+v", newer)
	}
	refused := SetLocations(newer, gen(1), []LeasePlace{{Location: locA, Project: none}})
	if refused.Refused != "A newer worker of this session holds its MCP servers" || refused.Leases != nil {
		t.Fatalf("got %+v", refused)
	}
	// Another incarnation of the same generation takes it over too (its calls then refuse the first one's).
	again := setLeases(t, newer, gen(2, "other"), locA)
	if got := HeldLease(again, gen(2, "other"), locA).Kind; got != LeaseHeld {
		t.Fatalf("held = %v", got)
	}
	if got := HeldLease(again, gen(2), locA).Kind; got != LeaseStale {
		t.Fatalf("stale = %v", got)
	}
	if got := HeldLease(again, gen(2, "other"), locB).Kind; got != LeaseNone {
		t.Fatalf("none = %v", got)
	}
}

func TestAViewsEndReleasesOnlyItsGenerationsLeases(t *testing.T) {
	leases := setLeases(t, []Lease{{Session: "s2", Location: locA, Project: none, Generation: 1, Incarnation: "x"}}, gen(2), locA)
	if got := ReleaseGeneration(leases, "s1", 1); !reflect.DeepEqual(got, leases) {
		t.Fatalf("got %v", got)
	}
	got := ReleaseGeneration(leases, "s1", 2)
	if len(got) != 1 || got[0].Session != "s2" {
		t.Fatalf("got %v", got)
	}
}

func TestALeaseKeepsTheProjectConfigurationSentWithItsLocationOfALocationGivenTwiceTheFirstCounts(t *testing.T) {
	result := SetLocations(nil, gen(1), []LeasePlace{
		{Location: locA, Project: ProjectSource{Kind: ProjectFile, Text: "{}"}},
		{Location: locA, Project: none},
		{Location: locB, Project: ProjectSource{Kind: ProjectUnreadable, Error: "denied"}},
	})
	type pair struct {
		cwd  string
		kind ProjectSourceKind
	}
	got := []pair{}
	for _, lease := range result.Leases {
		got = append(got, pair{lease.Location.Cwd, lease.Project.Kind})
	}
	if want := []pair{{"/a", ProjectFile}, {"/b", ProjectUnreadable}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}
