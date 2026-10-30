package host

import (
	"context"
	"errors"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest/contractstest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Own tests of host_destination.go. Pi has no test file for destination.ts: its cases are covered through hosts
// (route.test.ts, presence.test.ts, the remote tests), which are later lanes. These tests mirror the rules of the
// doc comments of Pi's `Destination.reference`, `hold` and `misplaced` over the route and path fakes.

// routeRemoteNotes is `remote("app.notes", { params: { session: str() }, hostAddress, variants })`.
func routeRemoteNotes(t testing.TB, hostAddress contracts.HostAddress) *contracts.Contract {
	t.Helper()
	contract, err := contracts.Remote("app.notes", contracts.RemoteDeclaration{
		Version:     1,
		Params:      contracts.Params{contracts.Prop("session", contracts.Str())},
		HostAddress: hostAddress,
		Variants:    []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("n", contracts.Int())}})},
	})
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

// routePlacedOnSession is a hostAddress that puts every object on the session host of its `session` param.
func routePlacedOnSession(t testing.TB) contracts.HostAddress {
	kind := routeSessionKind(t)
	return func(params *delta.JsonObject) (contracts.Address, error) {
		return kind.Address(delta.JsonObjectOf("id", params.Value("session"))), nil
	}
}

// destinationRig is a destination whose host is `server`, with a route for the kind "session" and a parent path.
type destinationRig struct {
	*routeRig
	destination *destination
	parent      *rigPath
	server      contracts.Address
}

func newDestinationRig(t testing.TB, own bool) *destinationRig {
	t.Helper()
	rig := newRouteRig(t)
	parent := &rigPath{check: func() {}}
	server := contractstest.Server()
	var address *contracts.Address
	if own {
		address = &server
	}
	return &destinationRig{
		routeRig:    rig,
		destination: newDestination(address, map[string]*route{"session": rig.route}, parent),
		parent:      parent,
		server:      server,
	}
}

func routeNotesParams(session string) *delta.JsonObject {
	return delta.JsonObjectOf("session", session)
}

func TestDestinationReferenceOfALocalTypeIsHere(t *testing.T) {
	rig := newDestinationRig(t, true)
	got, err := rig.destination.reference(scopeItem(t, "app.item"), delta.JsonObjectOf("id", 1.0), nil, chordctx.Background())
	if err != nil || got.kind != placedHere {
		t.Fatalf("reference = %+v, %v, want here", got, err)
	}
}

func TestDestinationReferenceOfARemoteTypeWhoseHostAddressIsThisHostsIsHereServedHereOrNot(t *testing.T) {
	rig := newDestinationRig(t, true)
	notes := routeRemoteNotes(t, contractstest.ServerAddress)
	got, err := rig.destination.reference(notes, routeNotesParams("S1"), nil, chordctx.Background())
	if err != nil || got.kind != placedHere {
		t.Fatalf("reference = %+v, %v, want here", got, err)
	}
}

func TestDestinationReferenceOfARemoteTypeGoesOverTheRouteOfItsHostAddressKindAndAuthorizesThePrincipal(t *testing.T) {
	rig := newDestinationRig(t, true)
	notes := routeRemoteNotes(t, routePlacedOnSession(t))
	owner := delta.JsonObjectOf("kind", "owner")
	var got placed
	var err error
	rig.within(func() {
		got, err = rig.destination.reference(notes, routeNotesParams("S1"), owner, chordctx.Background())
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.kind != placedPath || got.path != routePath(rig.route.addresses["session|S1"].path) || got.address.Kind != "session" {
		t.Fatalf("reference = %+v, want the route's path of session|S1", got)
	}
	// A refusal is `forbidden`; no principal is a refusal too.
	rig.authorize = func(data.Principal, contracts.Address) (bool, error) { return false, nil }
	rig.within(func() {
		_, err = rig.destination.reference(notes, routeNotesParams("S2"), owner, chordctx.Background())
	})
	wantScopeError(t, err, data.CodeForbidden, "", "session|S2: not authorized")
	// The route's own errors are the reference's: an address whose params do not fit its kind.
	bad := routeRemoteNotes(t, func(params *delta.JsonObject) (contracts.Address, error) {
		return routeSessionKind(t).Address(delta.JsonObjectOf("id", 7.0)), nil
	})
	rig.within(func() { _, err = rig.destination.reference(bad, routeNotesParams("S1"), owner, chordctx.Background()) })
	if chordErr, ok := data.IsChordError(err); !ok || chordErr.Reason != data.ReasonHostAddress {
		t.Fatalf("err = %v, want invalid/host_address", err)
	}
}

func TestDestinationReferenceOfARemoteTypeWithoutARouteForItsKindGoesOverTheParentLink(t *testing.T) {
	rig := newDestinationRig(t, false)
	rig.destination.routes = map[string]*route{}
	notes := routeRemoteNotes(t, routePlacedOnSession(t))
	got, err := rig.destination.reference(notes, routeNotesParams("S1"), nil, chordctx.Background())
	if err != nil || got.kind != placedPath || got.path != routePath(rig.parent) || got.address.Kind != "session" {
		t.Fatalf("reference = %+v, %v, want the parent path with the host address", got, err)
	}
}

func TestDestinationReferenceReturnsWhatAHostAddressThrows(t *testing.T) {
	rig := newDestinationRig(t, true)
	threw := errors.New("hostAddress threw")
	notes := routeRemoteNotes(t, func(*delta.JsonObject) (contracts.Address, error) { return contracts.Address{}, threw })
	if _, err := rig.destination.reference(notes, routeNotesParams("S1"), nil, chordctx.Background()); !errors.Is(err, threw) {
		t.Fatalf("err = %v, want the hostAddress's", err)
	}
}

func TestDestinationHoldIsHereWhenTheAddressIsThisHostsARelayNeverCapturesAHoldForServingTheSameName(t *testing.T) {
	rig := newDestinationRig(t, true)
	address := link.WireAddress{Kind: "server", Params: delta.NewJsonObject(0)}
	got, err := rig.destination.hold(address, "app.notes", &rigHostLink{role: "child"}, nil, chordctx.Background())
	if err != nil || got.kind != placedHere {
		t.Fatalf("hold = %+v, %v, want here", got, err)
	}
	// The same host with no address of its own never has a hold here.
	other := newDestinationRig(t, false)
	other.parent.linked = false
	_, err = other.destination.hold(address, "app.notes", &rigHostLink{role: "child"}, nil, chordctx.Background())
	wantScopeError(t, err, data.CodeUnavailable, "", "app.notes is not served here")
}

func TestDestinationHoldGoesOverTheRouteOfItsKindElseOverTheParentLinkUnlessItCameOnTheParentLinkOrThereWasNone(t *testing.T) {
	rig := newDestinationRig(t, true)
	owner := delta.JsonObjectOf("kind", "owner")
	session := link.WireAddress{Kind: "session", Params: map[string]any{"id": "S1"}}
	var got placed
	var err error
	rig.within(func() {
		got, err = rig.destination.hold(session, "app.notes", &rigHostLink{role: "child"}, owner, chordctx.Background())
	})
	if err != nil || got.kind != placedPath || got.path != routePath(rig.route.addresses["session|S1"].path) || got.address.Kind != "session" {
		t.Fatalf("hold = %+v, %v, want the route's path", got, err)
	}
	// An address of a kind without a route goes up the parent link, waiting for it while it is down...
	elsewhere := link.WireAddress{Kind: "tab", Params: delta.NewJsonObject(0)}
	rig.parent.linked = true
	got, err = rig.destination.hold(elsewhere, "app.notes", &rigHostLink{role: "child"}, owner, chordctx.Background())
	if err != nil || got.kind != placedPath || got.path != routePath(rig.parent) {
		t.Fatalf("hold = %+v, %v, want the parent path", got, err)
	}
	// ...unless it came on the parent link, or this host never had one.
	_, err = rig.destination.hold(elsewhere, "app.notes", &rigHostLink{role: "parent"}, owner, chordctx.Background())
	wantScopeError(t, err, data.CodeUnavailable, "", "app.notes is not served here")
	rig.parent.linked = false
	_, err = rig.destination.hold(elsewhere, "app.notes", &rigHostLink{role: "child"}, owner, chordctx.Background())
	wantScopeError(t, err, data.CodeUnavailable, "", "app.notes is not served here")
	// The route's refusal is the hold's.
	rig.authorize = func(data.Principal, contracts.Address) (bool, error) { return false, nil }
	rig.within(func() {
		_, err = rig.destination.hold(session, "app.notes", &rigHostLink{role: "child"}, owner, chordctx.Background())
	})
	wantScopeError(t, err, data.CodeForbidden, "", "session|S1: not authorized")
}

func TestDestinationMisplacedIsNilWhenTheObjectLivesHereElseInvalidHostAddressNamingBothHosts(t *testing.T) {
	rig := newDestinationRig(t, true)
	here, err := rig.destination.misplaced(routeRemoteNotes(t, contractstest.ServerAddress), routeNotesParams("S1"))
	if here != nil || err != nil {
		t.Fatalf("misplaced = %v, %v, want nil for an object that lives here", here, err)
	}
	elsewhere, err := rig.destination.misplaced(routeRemoteNotes(t, routePlacedOnSession(t)), routeNotesParams("S1"))
	if err != nil || elsewhere == nil || elsewhere.Code != data.CodeInvalid || elsewhere.Reason != data.ReasonHostAddress ||
		elsewhere.Message != "app.notes: placed on session, this host is server" {
		t.Fatalf("misplaced = %v, %v", elsewhere, err)
	}
	threw := errors.New("hostAddress threw")
	failing := routeRemoteNotes(t, func(*delta.JsonObject) (contracts.Address, error) { return contracts.Address{}, threw })
	if _, err := rig.destination.misplaced(failing, routeNotesParams("S1")); !errors.Is(err, threw) {
		t.Fatalf("err = %v, want the hostAddress's", err)
	}
}

func TestSameAddressNamesTheHostWithTheSameKindAndTheSameValueForEachParam(t *testing.T) {
	session := routeSessionKind(t)
	own := session.Address(delta.JsonObjectOf("id", "S1"))
	numbers, err := contracts.NewHostKind("shard", contracts.Prop("n", contracts.Int()), contracts.Prop("ok", contracts.Bool()))
	if err != nil {
		t.Fatal(err)
	}
	shard := numbers.Address(delta.JsonObjectOf("n", 3.0, "ok", true))
	for _, tc := range []struct {
		name    string
		own     contracts.Address
		address link.WireAddress
		want    bool
	}{
		{"the same address", own, link.WireAddress{Kind: "session", Params: delta.JsonObjectOf("id", "S1")}, true},
		{"the same address as a decoded map", own, link.WireAddress{Kind: "session", Params: map[string]any{"id": "S1"}}, true},
		{"another kind", own, link.WireAddress{Kind: "tab", Params: delta.JsonObjectOf("id", "S1")}, false},
		{"another value", own, link.WireAddress{Kind: "session", Params: delta.JsonObjectOf("id", "S2")}, false},
		{"an extra param", own, link.WireAddress{Kind: "session", Params: delta.JsonObjectOf("id", "S1", "more", "x")}, false},
		{"a missing param", own, link.WireAddress{Kind: "session", Params: delta.NewJsonObject(0)}, false},
		{"params that are no object", own, link.WireAddress{Kind: "session", Params: "S1"}, false},
		{"absent params", own, link.WireAddress{Kind: "session"}, false},
		{"numbers by value, whatever their Go type", shard, link.WireAddress{Kind: "shard", Params: map[string]any{"n": 3, "ok": true}}, true},
		{"a number that is not the value", shard, link.WireAddress{Kind: "shard", Params: map[string]any{"n": 4.0, "ok": true}}, false},
		{"a boolean that is not the value", shard, link.WireAddress{Kind: "shard", Params: map[string]any{"n": 3.0, "ok": false}}, false},
		{"a string where a number is", shard, link.WireAddress{Kind: "shard", Params: map[string]any{"n": "3", "ok": true}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameAddress(tc.own, tc.address); got != tc.want {
				t.Fatalf("sameAddress = %v, want %v", got, tc.want)
			}
		})
	}
}

var _ context.Context = chordctx.Background()
