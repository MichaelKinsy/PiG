package mcp

import (
	"slices"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
)

// Ports apps/p2/plugins/mcp/leases.ts: the rules of MCP leases, as pure functions over the lease list: a session worker
// sets the locations it works at for its generation (a newer generation takes the session over, an older one is refused),
// its calls go through the lease at a location only from the incarnation that set it, and its view's end releases its
// generation's leases.

// ProjectSourceKind tells what a lease knows of its location's project configuration.
type ProjectSourceKind string

// The project configuration at a location as the worker read it: none, the file's text, or why it could not.
const (
	ProjectNone       ProjectSourceKind = "none"
	ProjectFile       ProjectSourceKind = "file"
	ProjectUnreadable ProjectSourceKind = "unreadable"
)

// ProjectSource is the project configuration at a location as the worker read it.
type ProjectSource struct {
	Kind ProjectSourceKind
	// Text is the file's text (ProjectFile).
	Text string
	// Error is why it could not be read (ProjectUnreadable).
	Error string
}

// Lease is a session worker's lease on the servers at one location, from the generation and incarnation that set it,
// with the project configuration found there.
type Lease struct {
	Session     string
	Location    p2runtime.Location
	Project     ProjectSource
	Generation  int
	Incarnation string
}

// LeasePlace is a location to lease, with its project configuration.
type LeasePlace struct {
	Location p2runtime.Location
	Project  ProjectSource
}

// LeaseCaller is who asks: one incarnation of one generation of a session's worker.
type LeaseCaller struct {
	Session     string
	Generation  int
	Incarnation string
}

// LeasesSet is SetLocations' outcome: the new lease list, or why the caller may not set it.
type LeasesSet struct {
	Leases []Lease
	// Refused is why the caller may not set it (empty: set).
	Refused string
}

// LeaseHeldKind is whom the lease a call goes through belongs to.
type LeaseHeldKind int

// The lease a call goes through: held by the caller, none at that location, or another incarnation's.
const (
	LeaseHeld LeaseHeldKind = iota
	LeaseNone
	LeaseStale
)

// LeaseHeldResult is HeldLease's outcome.
type LeaseHeldResult struct {
	Kind  LeaseHeldKind
	Lease Lease
}

// SetLocations makes caller's session lease exactly places from now on, for caller's generation and incarnation (of a
// location given twice, the first counts): its leases elsewhere end, those of older generations (a worker that is gone) are
// taken over. A newer generation's lease refuses.
func SetLocations(leases []Lease, caller LeaseCaller, places []LeasePlace) LeasesSet {
	if slices.ContainsFunc(leases, func(lease Lease) bool {
		return lease.Session == caller.Session && lease.Generation > caller.Generation
	}) {
		return LeasesSet{Refused: "A newer worker of this session holds its MCP servers"}
	}
	next := make([]Lease, 0, len(leases)+len(places))
	for _, lease := range leases {
		if lease.Session != caller.Session {
			next = append(next, lease)
		}
	}
	for index, place := range places {
		first := slices.IndexFunc(places, func(other LeasePlace) bool { return p2runtime.SameLocation(other.Location, place.Location) })
		if first == index {
			next = append(next, Lease{Session: caller.Session, Location: place.Location, Project: place.Project, Generation: caller.Generation, Incarnation: caller.Incarnation})
		}
	}
	return LeasesSet{Leases: next}
}

// HeldLease is the lease caller calls through at location.
func HeldLease(leases []Lease, caller LeaseCaller, location p2runtime.Location) LeaseHeldResult {
	index := slices.IndexFunc(leases, func(lease Lease) bool {
		return lease.Session == caller.Session && p2runtime.SameLocation(lease.Location, location)
	})
	if index < 0 {
		return LeaseHeldResult{Kind: LeaseNone}
	}
	lease := leases[index]
	if lease.Incarnation == caller.Incarnation && lease.Generation == caller.Generation {
		return LeaseHeldResult{Kind: LeaseHeld, Lease: lease}
	}
	return LeaseHeldResult{Kind: LeaseStale}
}

// ReleaseGeneration is leases without those of session's generation (its view ended).
func ReleaseGeneration(leases []Lease, session string, generation int) []Lease {
	kept := make([]Lease, 0, len(leases))
	for _, lease := range leases {
		if lease.Session != session || lease.Generation != generation {
			kept = append(kept, lease)
		}
	}
	return kept
}
