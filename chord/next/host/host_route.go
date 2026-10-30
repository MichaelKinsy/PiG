package host

import (
	"context"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/src/next/host/route.ts. Routes (§3): how a host reaches hosts of one kind. `authorize` admits each
// hold routed over the route; `resolve` makes the link of one address, at most once at a time, again after the link drops
// while holds exist, with a backoff per address.
//
// Forced Go differences. `resolve` is an awaited Promise, so it is a blocking call returning (link, error). `void
// this.#resolve(routed)` is an owned task (Loop.Go): the call to `resolve` runs on it, off the loop, and everything Pi
// writes after the `await` runs as one section (Loop.Run) on it, so a `resolve` whose holds were all released meanwhile
// finishes and its result is handled as Pi's. `authorize` and `resolve` may throw in Pi: they return the error. The
// `Path` and `HostLink` of `remote/` (D6), the host's `routeLink` and `AnyHost` are the seams routePath, routeLink and
// routeHost (DESIGN-go-layout.md section 5); the path's constructor is injected as `newPath`. A `Link` of `link` and its
// `Codec` and `PartLimits` are the package's own.

// RouteOptions is what `host.route` takes for a kind.
type RouteOptions struct {
	// Authorize reports whether principal may reach address; it runs for every hold routed over the route, before
	// anything starts.
	Authorize func(principal data.Principal, address contracts.Address) (bool, error)
	// Resolve makes the link to address: the principal is who the host at the other end is, Trusted skips the message
	// check on it, Limits bound its parts (as `accept`'s). ctx is the route's, cancelled when this host closes. It runs at
	// most once at a time per address; it blocks, off the host's loop.
	Resolve func(address contracts.Address, ctx context.Context) (ResolvedLink, error)
}

// ResolvedLink is the link a route's Resolve made, and what it says of it.
type ResolvedLink struct {
	Link      link.Link
	Principal data.Principal
	Trusted   bool
	Codec     link.Codec
	Limits    *link.PartLimits
}

// routeLinkOptions is what a host's routeLink takes of a resolved link besides the link.
type routeLinkOptions struct {
	principal data.Principal
	trusted   bool
	codec     link.Codec
	limits    *link.PartLimits
}

// routedHolder is a holder of a path (Pi's `Upstream`, D6): a route hands them back for the host to end and never
// inspects one.
type routedHolder any

// routeLink is a link of a route as the host made it (Pi's `HostLink`, D6): it is compared by identity.
type routeLink interface {
	// Open reports whether it is open (Pi's `open`).
	Open() bool
	// Greeted reports whether its peer's `hello` was handled (Pi's `greeted`).
	Greeted() bool
	// Role is its role, "parent" among others (Pi's `role`).
	Role() string
	Close()
}

// routePath is the path to one address (Pi's `Path`, D6): its link and holders.
type routePath interface {
	// Link is the current link while one is open, nil otherwise.
	Link() routeLink
	// Linked reports whether it had a link once (it may be down now: its holders wait for the next).
	Linked() bool
	// Held reports whether anything is held over this path.
	Held() bool
	// Holders is every holder, for the host's close.
	Holders() []routedHolder
	// Connected: link is the path's link now: every holder is held on it.
	Connected(link routeLink)
	// Disconnected: link closed (its sides ended their calls already): every holder goes loading until the next link.
	Disconnected(link routeLink)
}

// routeHost is what a route asks of its host.
type routeHost interface {
	loop() sectionLoop
	// Closing reports whether the host is closing.
	Closing() bool
	// Report reports to the host's `onError`.
	Report(err HostError)
	// RouteLink makes the host link of a link a route's resolve returned (Pi's `routeLink`).
	RouteLink(made link.Link, options routeLinkOptions, ctx context.Context) routeLink
}

// routed is one address of a route: its path (link and holders) and whether its `resolve` runs.
type routed struct {
	address   contracts.Address
	path      routePath
	resolving bool
}

// route is a host's route for one kind. An address is kept while it has holders, a link or a running `resolve`: a resolve
// whose holds were all released meanwhile finishes, and its link is kept until it closes.
type route struct {
	kind    string
	params  *contracts.ParamsData
	options RouteOptions
	host    routeHost
	// context is the route's context, cancelled when the host closes: every `resolve` and its links' work run under it.
	context context.Context
	newPath func(check func()) routePath
	// keys and addresses are Pi's `Map<string, Routed>`, in insertion order.
	keys      []string
	addresses map[string]*routed
	// links are the addresses' links, which tell their address when they open or close.
	links map[routeLink]*routed
	// backoff is per address key, separate from objects' identities.
	backoff *backoff
}

// newRoute makes the route of kind. It returns the error `kindParams` throws for a kind not declared by hostKind.
func newRoute(kind *contracts.HostKind, options RouteOptions, host routeHost, ctx context.Context, newPath func(check func()) routePath) (*route, error) {
	params, err := contracts.KindParams(kind)
	if err != nil {
		return nil, err
	}
	r := &route{
		kind: kind.Kind, params: params, options: options, host: host, context: ctx, newPath: newPath,
		addresses: map[string]*routed{}, links: map[routeLink]*routed{},
	}
	r.backoff = newBackoff(host.loop(), r.due)
	return r, nil
}

// path is the path to the address with params, for a hold acting as principal: the params checked against the kind
// (`invalid/host_address`), then `authorize` (`forbidden` when it refuses; an error is the hold's failure).
func (r *route) path(params any, principal data.Principal, ctx context.Context) (routePath, error) {
	address, err := data.CheckAddress(r.kind, r.params, params, ctx)
	if err != nil {
		return nil, err
	}
	admitted := false
	if principal != nil {
		admitted, err = r.options.Authorize(principal, address)
		if err != nil {
			return nil, err
		}
	}
	if !admitted {
		return nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeForbidden, Message: address.Key + ": not authorized"}, nil)
	}
	existing := r.addresses[address.Key]
	if existing == nil {
		made := &routed{address: address}
		made.path = r.newPath(func() { r.check(made) })
		existing = made
		r.keys = append(r.keys, address.Key)
		r.addresses[address.Key] = made
	}
	return existing.path, nil
}

// opened: one of this route's links completed its handshake: its address's backoff is over.
func (r *route) opened(link routeLink) {
	if routed := r.links[link]; routed != nil {
		r.backoff.delete(routed.address.Key)
	}
}

// closed: one of this route's links closed: the address waits its backoff, then resolves again while it has holders.
func (r *route) closed(link routeLink) bool {
	routed := r.links[link]
	if routed == nil {
		return false
	}
	delete(r.links, link)
	if !r.host.Closing() {
		r.backoff.failed(routed.address.Key)
	}
	routed.path.Disconnected(link)
	return true
}

// close: the host closes: no address resolves again. It returns every holder of every path, for the host to end.
func (r *route) close() []routedHolder {
	r.backoff.clear()
	return r.holders()
}

// holders is every holder of every path of this route, linked or not (revocation ends a revoked principal's).
func (r *route) holders() []routedHolder {
	var holders []routedHolder
	for _, key := range r.keys {
		holders = append(holders, r.addresses[key].path.Holders()...)
	}
	return holders
}

// check: an address's holders or link changed: it resolves when it has holders, no link, no `resolve` running and no
// backoff window; it is forgotten when it has neither holders, a link nor a running `resolve`.
func (r *route) check(routed *routed) {
	path, address := routed.path, routed.address
	if path.Link() != nil || routed.resolving || r.host.Closing() {
		return
	}
	if !path.Held() {
		r.forget(address.Key)
		return
	}
	if !r.backoff.waiting(address.Key) {
		r.resolve(routed)
	}
}

func (r *route) forget(key string) {
	delete(r.addresses, key)
	if index := slices.Index(r.keys, key); index >= 0 {
		r.keys = slices.Delete(r.keys, index, index+1)
	}
}

// due: a backoff window ended.
func (r *route) due(key string) {
	routed := r.addresses[key]
	if routed == nil {
		r.backoff.delete(key)
	} else {
		r.check(routed)
	}
}

// resolve runs `resolve` once for the address, on an owned task. A rejection, or a link closed already, starts the
// address's backoff; a link returned after the host closed is closed at once.
func (r *route) resolve(routed *routed) {
	routed.resolving = true
	loop := r.host.loop()
	loop.Go(func() {
		resolved, err := r.options.Resolve(routed.address, r.context)
		loop.Run(func() { r.resolved(routed, resolved, err) })
	})
}

// resolved is what `#resolve` does once `resolve` settled, in one section.
func (r *route) resolved(routed *routed, resolved ResolvedLink, err error) {
	key := routed.address.Key
	routed.resolving = false
	if err != nil {
		if r.host.Closing() {
			return
		}
		r.host.Report(HostError{Kind: ErrResolveFailed, Address: key, Error: err})
		r.backoff.failed(key)
		r.check(routed)
		return
	}
	if r.host.Closing() {
		resolved.Link.Close()
		return
	}
	made := r.host.RouteLink(resolved.Link, routeLinkOptions{
		principal: resolved.Principal, trusted: resolved.Trusted, codec: resolved.Codec, limits: resolved.Limits,
	}, r.context)
	if !made.Open() {
		r.backoff.failed(key)
		r.check(routed)
		return
	}
	r.links[made] = routed
	// A peer that greeted first had its `hello` handled while the link was made, before it was registered here.
	if made.Greeted() {
		r.backoff.delete(key)
	}
	routed.path.Connected(made)
}
