package host

import (
	"context"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/src/next/host/route.ts. Routes (§3): how a host reaches hosts of one kind. `authorize` admits each
// hold routed over the route; `resolve` makes the link of one address, at most once at a time. No timer (slice 10 R2): an
// address resolves for a new hold, on `RouteHandle.retry`, or once after its link dropped while held; a rejected `resolve`
// (or a link closed before its handshake) fails its holders `failed { waiting }` until then, and a suspended address
// resolves nothing while held.
//
// Forced Go differences. `resolve` is an awaited Promise, so it is a blocking call returning (link, error). `void
// this.#resolve(routed)` is an owned task (Loop.Go): the call to `resolve` runs on it, off the loop, and everything Pi
// writes after the `await` runs as one section (Loop.Run) on it, so a `resolve` whose holds were all released meanwhile
// finishes and its result is handled as Pi's. Pi calls `resolve` synchronously from `changed`, so the calls reach their first
// await in the order the holds arrived; `resolve` takes the call's place in that order, in the route's section, and the
// `Resolve` task's context carries it (`resolveOrder`), for the effects a `Resolve` orders as Pi's calls are ordered, without
// one address's slow `Resolve` holding up another's. `authorize` and `resolve` may throw in Pi: they return the error. The
// route calls the host's own `*path`, `*hostLink` and `*chordHost` directly, as Pi's classes do. A `Link` of `link` and its
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

// RouteHandle is what `host.route` returns: the app's controls of its addresses (D10b, R1).
type RouteHandle struct {
	// Retry resolves address again now if it has holders and no link: its failure and its suspension end (a Retry button);
	// an address nobody holds resolves at its next hold.
	Retry func(address contracts.Address)
	// Suspend makes address resolve nothing while it has holders: they wait `failed { waiting }` ("suspended") once its
	// link is gone; the suspension ends with its last holder or Retry.
	Suspend func(address contracts.Address)
}

// routed is one address of a route: its path, whether its `resolve` runs, why it fails its holders, whether it is suspended.
type routed struct {
	address   contracts.Address
	path      *path
	resolving bool
	// failure is a rejected `resolve`, or a link closed before its handshake: its holders fail with it until the next resolve.
	failure *data.ChordError
	// suspended is whether the address resolves nothing while held.
	suspended bool
}

// routeLinkEntry is what the route knows of one address's link: its address, and whether it completed its handshake.
type routeLinkEntry struct {
	routed  *routed
	greeted bool
}

// route is a host's route for one kind. An address is kept while it has holders, a link or a running `resolve`: a resolve
// whose holds were all released meanwhile finishes, and its link is kept until it closes.
type route struct {
	kind    string
	params  *contracts.ParamsData
	options RouteOptions
	host    *chordHost
	// context is the route's context, cancelled when the host closes: every `resolve` and its links' work run under it.
	context context.Context
	// keys and addresses are Pi's `Map<string, Routed>`, in insertion order.
	keys      []string
	addresses map[string]*routed
	// links are the addresses' links, which tell their address when they close, and whether each completed its handshake.
	links map[*hostLink]*routeLinkEntry
	// resolves counts the `Resolve` calls made: the next call's place in the route's order of calls.
	resolves int
}

// newRoute makes the route of kind. It returns the error `kindParams` throws for a kind not declared by hostKind.
func newRoute(kind *contracts.HostKind, options RouteOptions, host *chordHost, ctx context.Context) (*route, error) {
	params, err := contracts.KindParams(kind)
	if err != nil {
		return nil, err
	}
	r := &route{
		kind: kind.Kind, params: params, options: options, host: host, context: ctx,
		addresses: map[string]*routed{}, links: map[*hostLink]*routeLinkEntry{},
	}
	return r, nil
}

// resolveOrderKey is the context key of a `Resolve` call's place in its route's order of calls.
type resolveOrderKey struct{}

// resolveOrder is the place of the `Resolve` call whose context is ctx among the calls of its route: 0 for the first call
// `resolve` made, then 1, 2 and so on in the order of the calls (as the order in which Pi's synchronous calls ran).
func resolveOrder(ctx context.Context) (int, bool) {
	order, ok := ctx.Value(resolveOrderKey{}).(int)
	return order, ok
}

// path is the path to the address with params, for a hold acting as principal: the params checked against the kind
// (`invalid/host_address`), then `authorize` (`forbidden` when it refuses; an error is the hold's failure).
func (r *route) path(params any, principal data.Principal, ctx context.Context) (*path, error) {
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
		made.path = newPath(func(event pathEvent) { r.changed(made, event) })
		existing = made
		r.keys = append(r.keys, address.Key)
		r.addresses[address.Key] = made
	}
	return existing.path, nil
}

// retry is `RouteHandle.retry`: the address's failure and suspension end; it resolves now if held and unlinked.
func (r *route) retry(key string) {
	routed := r.addresses[key]
	if routed == nil {
		return
	}
	routed.failure = nil
	routed.suspended = false
	r.resolveIfHeld(routed)
}

// suspend is `RouteHandle.suspend`: a held address resolves nothing until `retry` or its last holder; unheld, nothing is kept.
func (r *route) suspend(key string) {
	routed := r.addresses[key]
	if routed == nil || !routed.path.held() {
		return
	}
	routed.suspended = true
	if routed.path.link() == nil && !routed.resolving {
		routed.path.unreachable(suspendedError(routed))
	}
}

// opened: one of this route's links completed its handshake: a later drop of it resolves its address again.
func (r *route) opened(link *hostLink) {
	if entry := r.links[link]; entry != nil {
		entry.greeted = true
	}
}

// closed: one of this route's links closed: its address resolves again once while held if the link had completed its
// handshake, else its holders fail (no loop over a worker that dies at start).
func (r *route) closed(link *hostLink) bool {
	entry := r.links[link]
	if entry == nil {
		return false
	}
	delete(r.links, link)
	routed := entry.routed
	if !entry.greeted {
		routed.failure = data.NewChordError(data.ChordErrorInit{
			Code: data.CodeUnavailable, Message: routed.address.Key + ": the link closed before its handshake",
		}, nil)
	}
	routed.path.disconnected(link)
	return true
}

// holders is every holder of every path of this route, linked or not (revocation ends a revoked principal's).
func (r *route) holders() []upstream {
	var holders []upstream
	for _, key := range r.keys {
		holders = append(holders, r.addresses[key].path.holders()...)
	}
	return holders
}

// changed: an address's path changed without a link: a new holder resolves it (a failed address too: a new hold is a new
// request), a dropped link resolves it once (or fails it, closed before its handshake), the last holder's release forgets
// it (its failure and suspension too) unless its link or `resolve` keeps it.
func (r *route) changed(routed *routed, event pathEvent) {
	if r.host.closing() {
		return
	}
	switch event.kind {
	case pathReleased:
		routed.suspended = false
		if routed.path.link() == nil && !routed.resolving {
			r.forget(routed.address.Key)
		}
	case pathAttached:
		if routed.suspended {
			event.holder.unreachable(suspendedError(routed))
		} else if !routed.resolving {
			r.resolve(routed)
		}
	case pathDropped:
		switch {
		case !routed.path.held():
			r.forget(routed.address.Key)
		case routed.suspended:
			routed.path.unreachable(suspendedError(routed))
		case routed.failure != nil:
			routed.path.unreachable(routed.failure)
		default:
			r.resolve(routed)
		}
	}
}

func (r *route) forget(key string) {
	delete(r.addresses, key)
	if index := slices.Index(r.keys, key); index >= 0 {
		r.keys = slices.Delete(r.keys, index, index+1)
	}
}

// resolveIfHeld resolves the address if it has holders, no link, no running `resolve` and no suspension.
func (r *route) resolveIfHeld(routed *routed) {
	if !routed.path.held() || routed.path.link() != nil || routed.resolving || r.host.closing() {
		return
	}
	r.resolve(routed)
}

// resolve runs `resolve` once for the address, on an owned task, whose context carries the call's place in the route's
// order of calls. A rejection, or a link closed already, fails its holders with the error until the next resolve; a link
// returned after the host closed is closed at once.
func (r *route) resolve(routed *routed) {
	routed.resolving = true
	ctx := context.WithValue(r.context, resolveOrderKey{}, r.resolves)
	r.resolves++
	loop := r.host.loop()
	loop.Go(func() {
		resolved, err := r.options.Resolve(routed.address, ctx)
		loop.Run(func() { r.resolved(routed, resolved, err) })
	})
}

// resolved is what `#resolve` does once `resolve` settled, in one section.
func (r *route) resolved(routed *routed, resolved ResolvedLink, err error) {
	key := routed.address.Key
	routed.resolving = false
	if err != nil {
		if r.host.closing() {
			return
		}
		r.host.report(HostError{Kind: ErrResolveFailed, Address: key, Error: err})
		r.settle(routed, data.AsChordError(err, key))
		return
	}
	if r.host.closing() {
		resolved.Link.Close()
		return
	}
	made := r.host.routeLink(resolved.Link, routeLinkOptions{
		principal: resolved.Principal, trusted: resolved.Trusted, codec: resolved.Codec, limits: resolved.Limits,
	}, r.context)
	if !made.open() {
		r.settle(routed, data.NewChordError(data.ChordErrorInit{
			Code: data.CodeUnavailable, Message: key + ": the link closed before its handshake",
		}, nil))
		return
	}
	routed.failure = nil
	// A peer that greeted first had its `hello` handled while the link was made, before it was registered here.
	r.links[made] = &routeLinkEntry{routed: routed, greeted: made.greeted()}
	routed.path.connected(made)
}

// settle: a resolve failed with err: its holders fail `failed { waiting }` with it (they stay on its path for its next
// link), or the address goes when nobody holds it any more.
func (r *route) settle(routed *routed, err *data.ChordError) {
	if !routed.path.held() {
		r.forget(routed.address.Key)
		return
	}
	routed.failure = err
	if routed.suspended {
		err = suspendedError(routed)
	}
	routed.path.unreachable(err)
}

// suspendedError is the failure holders of a suspended address see.
func suspendedError(routed *routed) *data.ChordError {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: routed.address.Key + ": suspended"}, nil)
}
