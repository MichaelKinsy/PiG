package host

import (
	"context"
	"math"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/src/next/host/destination.ts. Destination (§3, section 1's table; dependency model §1): where a
// reference made on a host, or a hold arriving on one of its links, goes, by address alone: here when the address is this
// host's (served here or not), else over the route of its host address's kind, or over the parent link; and the check
// that an object served here lives here.
//
// Forced Go differences. Every throw is a returned error (`reference` and `hold` return (placed, error); `misplaced`
// returns the ChordError it hands back, and the error its `hostAddress` throws). `Placed` is a struct with a kind. The
// `Path`, `HostLink` and `HoldRequest` of `remote/` (D6) are the seams routePath, routeLink and, for the request, its two
// fields `address` and `type` taken as arguments of `hold`. An address's params are a *delta.JsonObject, the one params
// type `contracts.HostAddress` takes and returns; a wire address's params are what a hold carried (a *delta.JsonObject or
// a map[string]any).

// placedKind is where something is placed: here, or over a path.
type placedKind string

const (
	placedHere placedKind = "here"
	placedPath placedKind = "path"
)

// placed is where something is placed: here, or over a path (with the host address it was computed or named with).
type placed struct {
	kind    placedKind
	path    routePath
	address link.WireAddress
}

// here is the one placement here, shared.
var here = placed{kind: placedHere}

// destination is a host's destinations: its address, its routes and its parent path, as the host keeps them.
type destination struct {
	// address is this host's address; nil for a host without one (Pi's `undefined`).
	address *contracts.Address
	routes  map[string]*route
	parent  routePath
}

func newDestination(address *contracts.Address, routes map[string]*route, parent routePath) *destination {
	return &destination{address: address, routes: routes, parent: parent}
}

// reference is where a reference to Type with params made as principal goes: here for a local type, and for a remote type
// whose host address is this host's, served here or not (the check against what is served is the host's); otherwise over
// the route of its host address's kind, or over the parent link (the host address decides where an object lives: a worker
// reaches another worker's object of a type it serves too). It returns `invalid/host_address` for a host address whose
// params do not fit its kind, `forbidden` when the route refuses, and what a `hostAddress` or `authorize` returns.
func (d *destination) reference(Type *contracts.Contract, params *delta.JsonObject, principal data.Principal, ctx context.Context) (placed, error) {
	if Type.Kind == contracts.KindLocal {
		return here, nil
	}
	address, err := hostAddressOf(Type, params)
	if err != nil {
		return placed{}, err
	}
	if d.isHere(address) {
		return here, nil
	}
	path, err := d.pathTo(address, principal, ctx)
	if err != nil {
		return placed{}, err
	}
	if path == nil {
		path = d.parent
	}
	return placed{kind: placedPath, path: path, address: address}, nil
}

// hold is where a hold arriving on from acting as principal goes, by the address it names (a request's `address`, and
// its `type` for the message): here when it is this host's (a relay never captures a hold for serving the same name); over
// the route of its kind; else over the parent link (waiting for it while it is down), unless it came on the parent link
// or this host never had one (`unavailable`). It returns what `reference` does.
func (d *destination) hold(address link.WireAddress, requestType string, from routeLink, principal data.Principal, ctx context.Context) (placed, error) {
	if d.isHere(address) {
		return here, nil
	}
	routedPath, err := d.pathTo(address, principal, ctx)
	if err != nil {
		return placed{}, err
	}
	if routedPath != nil {
		return placed{kind: placedPath, path: routedPath, address: address}, nil
	}
	if from.Role() == "parent" || !d.parent.Linked() {
		return placed{}, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: requestType + " is not served here"}, nil)
	}
	return placed{kind: placedPath, path: d.parent, address: address}, nil
}

// misplaced is the provider's own check of a request placed here for the remote Type it serves, with params (checked):
// `invalid/host_address` when its `hostAddress` puts the object on another host (code skew: the consumer computed this
// host), nil when it lives here. A `hostAddress` that fails returns its error (the provider's own code). The address a
// request names only routes it.
func (d *destination) misplaced(Type *contracts.Contract, params *delta.JsonObject) (*data.ChordError, error) {
	address, err := hostAddressOf(Type, params)
	if err != nil {
		return nil, err
	}
	if d.isHere(address) {
		return nil, nil
	}
	// A host serving a remote type has an address (`serve` refuses one without).
	return data.Invalid(data.ReasonHostAddress, Type.Name+": placed on "+address.Kind+", this host is "+d.address.Key), nil
}

// pathTo is the path to address when this host routes its kind (its params checked, `authorize` run for principal); nil
// without a route for its kind.
func (d *destination) pathTo(address link.WireAddress, principal data.Principal, ctx context.Context) (routePath, error) {
	routed := d.routes[address.Kind]
	if routed == nil {
		return nil, nil
	}
	return routed.path(address.Params, principal, ctx)
}

// isHere reports whether an object whose host address is address lives here: this host has that address.
func (d *destination) isHere(address link.WireAddress) bool {
	return d.address != nil && sameAddress(*d.address, address)
}

// hostAddressOf is the address a remote Type's `hostAddress` computes for params.
func hostAddressOf(Type *contracts.Contract, params *delta.JsonObject) (link.WireAddress, error) {
	contract, err := contracts.ContractDataOf(Type)
	if err != nil {
		return link.WireAddress{}, err
	}
	// Every remote contract has one (declaration policy, `remote`).
	address, err := contract.HostAddress(params)
	if err != nil {
		return link.WireAddress{}, err
	}
	// Plugin code: what it returns is checked where it is routed or served.
	wire := link.WireAddress{Kind: address.Kind}
	if address.Params != nil {
		wire.Params = address.Params
	}
	return wire, nil
}

// sameAddress reports whether address names the host at own: the same kind and the same value for each of its params.
func sameAddress(own contracts.Address, address link.WireAddress) bool {
	if own.Kind != address.Kind {
		return false
	}
	var length int
	var get func(field string) any
	switch values := address.Params.(type) {
	case *delta.JsonObject:
		if values == nil {
			return false
		}
		length, get = values.Len(), values.Value
	case map[string]any:
		length, get = len(values), func(field string) any { return values[field] }
	default:
		return false
	}
	fields := own.Params.Keys()
	if length != len(fields) {
		return false
	}
	for _, field := range fields {
		if !strictEqual(get(field), own.Params.Value(field)) {
			return false
		}
	}
	return true
}

// strictEqual is JavaScript's `===` of two JSON scalar values: numbers by value whatever their Go type.
func strictEqual(a, b any) bool {
	if x, ok := numberOf(a); ok {
		y, ok := numberOf(b)
		return ok && x == y && !math.IsNaN(x)
	}
	switch x := a.(type) {
	case string:
		y, ok := b.(string)
		return ok && x == y
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case nil:
		return b == nil
	}
	return false
}

func numberOf(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	}
	return 0, false
}
