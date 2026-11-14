package host

import (
	"context"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/internal/parking"
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
// Forced Go differences. `resolve` is an awaited Promise, so it is a blocking call returning (link, error). Pi's `#resolve`
// runs `options.resolve` synchronously, inside the section that triggered it, up to its first `await`, and handles the
// result in a later microtask: the Go call is lent that section like a method handler (Loop.Lend, DESIGN-go-layout.md
// section 10): its synchronous part runs inside the section, which goes on once `Resolve` returned or suspended
// (`chordctx.Suspend(ctx)`, or a host primitive that waits), and everything Pi writes after the `await` runs as one section
// (Loop.Run) afterwards, so a `resolve` whose holds were all released meanwhile finishes and its result is handled as Pi's.
// `authorize` and `resolve` may throw in Pi: they return the error. The route calls the host's own `*path`, `*hostLink` and
// `*chordHost` directly, as Pi's classes do. A `Link` of `link` and its `Codec` and `PartLimits` are the package's own.

// RouteOptions is what `host.route` takes for a kind.
type RouteOptions struct {
	// Authorize reports whether principal may reach address; it runs for every hold routed over the route, before
	// anything starts.
	Authorize func(principal data.Principal, address contracts.Address) (bool, error)
	// Resolve makes the link to address: the principal is who the host at the other end is, Trusted skips the message
	// check on it, Limits bound its parts (as `accept`'s). ctx is the route's, cancelled when this host closes. It runs at
	// most once at a time per address. Its synchronous part runs inside the section that triggered it (a hold, a Retry), as
	// Pi's does: it must call `chordctx.Suspend(ctx)` with its ctx before it waits for anything that is not a section of the
	// host (a channel, a process, I/O), the same contract as a method handler's (DESIGN-go-layout.md section 10). Lock
	// order: the synchronous part runs while RouteHandle.Retry's caller is inside the host's loop, so it must not enter a
	// loop or lock that the caller of Retry or Suspend may hold; see the package doc, "Lock order".
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
	// an address nobody holds resolves at its next hold. Like Pi's `retry` it enters the host's loop and runs the app's
	// `Resolve` inline, up to where it returns or suspends, before it returns. It reports whether it started a resolve
	// (held, no link, none running): Pi's caller reads what its synchronous `resolve` recorded, which is one event loop's
	// state, so a Go caller that reads its own state in a later section cannot tell from it whether this call started the
	// resolve it finds there. The workers' rollout does not ask; as in Pi it lists the start its address has, whoever began it.
	//
	// Lock order (Pi has one event loop, so it states none): do not call Retry or Suspend while holding another loop or
	// lock that `Resolve` (before its `chordctx.Suspend`) enters, nor one that a Link's Close enters (the host closes links
	// inside its sections): that is an AB/BA deadlock with the host's loop. Call them from outside the holder.
	Retry func(address contracts.Address) bool
	// Suspend makes address resolve nothing while it has holders: they wait `failed { waiting }` ("suspended") once its
	// link is gone; the suspension ends with its last holder or Retry. It enters the host's loop: the lock-order rule of
	// Retry applies.
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
func (r *route) retry(key string) bool {
	routed := r.addresses[key]
	if routed == nil {
		return false
	}
	routed.failure = nil
	routed.suspended = false
	return r.resolveIfHeld(routed)
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

// resolveIfHeld resolves the address if it has holders, no link, no running `resolve` and no suspension, and reports
// whether it did.
func (r *route) resolveIfHeld(routed *routed) bool {
	if !routed.path.held() || routed.path.link() != nil || routed.resolving || r.host.closing() {
		return false
	}
	r.resolve(routed)
	return true
}

// resolve runs `resolve` once for the address: its synchronous part inside this section (lent to it), the rest as a
// section of its own. A rejection, or a link closed already, fails its holders with the error until the next resolve; a
// link returned after the host closed is closed at once.
func (r *route) resolve(routed *routed) {
	routed.resolving = true
	loop := r.host.loop()
	signal := parking.New()
	ctx := context.WithValue(r.context, parking.Key, signal)
	loop.Lend(signal, func(abandoned bool) {
		var resolved ResolvedLink
		var err error
		if abandoned {
			// `resolve` ended its goroutine (runtime.Goexit) without returning: Pi's `resolve` that throws, the address's
			// holders fail with it.
			err = errHandlerGoexit
		} else {
			resolved, err = resolvePlugin(r.options.Resolve, routed.address, ctx)
		}
		signal.Return()
		loop.Run(func() {
			// A Goexit after the result was handled leaves nothing to do.
			if !abandoned || routed.resolving {
				r.resolved(routed, resolved, err)
			}
		})
	})
}

// resolvePlugin calls the app's `resolve`: plugin code, so a panic is the rejection, as a `throw` is.
func resolvePlugin(resolve func(contracts.Address, context.Context) (ResolvedLink, error), address contracts.Address, ctx context.Context) (resolved ResolvedLink, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			resolved, err = ResolvedLink{}, recoveredError(recovered)
		}
	}()
	return resolve(address, ctx)
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
