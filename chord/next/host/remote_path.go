package host

import (
	"slices"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/remote/path.ts. Paths (§3 recovery): where this host's upstream holds go, the parent
// link or a route address's link, across links. Each new link of a path holds every holder of the path again; when it
// drops, every holder is told. A route with an admission gates its paths (slice 14 §2.3): a holder is forwarded only while
// its decision allows it, released when it is denied, and forwarded again behind a fence.

// upstream is what a path holds on its links: one of this host's remote references (*remoteObject), or a hold it relays
// (*relayedHold). It must be comparable (a pointer).
type upstream interface {
	// kind is "remote" for a remote object, "relayed" for a relayed hold.
	kind() string
	// principal is the principal it acts for (Pi's `caller.principal`): what a route's admission decides about. A hold routed
	// over a route acts for one (route.path refused a hold without).
	principal() data.Principal
	// attach holds it on link, the path's link now (a first hold, or a re-hold on a new link).
	attach(link *hostLink)
	// dropped: the path's link closed; it goes loading until the next link's hold is bound.
	dropped(err *data.ChordError)
	// unreachable: its path's address cannot be reached for now (a route's `resolve` failed, a suspension): it fails
	// `waiting` with err, still on the path, bound by its next link.
	unreachable(err *data.ChordError)
	// span is its hold's (or relay's) span until it is bound; nil when nothing records: a route's resolve links to it.
	span() telemetry.TelemetrySpan
	// withheld: its path's gate holds it back with cause (slice 14 §2.3): released where it is held, its calls rejected, and
	// failed with cause. It stays on its path: an allow holds it again.
	withheld(cause data.FailureCause)
	// admitted: its path's gate allows it: loading (again, after what held it back) until its next binding.
	admitted()
}

// pathGate is what decides whether a path forwards a holder (a route's admission, one decision per principal and address).
// It forwards a holder it held back with path.forward, and takes one back with path.withhold.
type pathGate interface {
	// joined: holder joined the path: whether it is forwarded at once (its principal's decision allows it already).
	joined(holder upstream) bool
	// left: holder left the path.
	left(holder upstream)
	// unlinked: the path lost its link (its holders were told): the gate takes back, with path.withhold, the holders it
	// cannot forward on the next one, so a new link holds only what a decision that can be read allows.
	unlinked()
}

// pathEventKind is the kind of a pathEvent: Pi's `PathEvent["kind"]`.
type pathEventKind uint8

const (
	// pathAttached: a holder attached to a path without a link.
	pathAttached pathEventKind = iota
	// pathDropped: the path's link dropped (its holders went loading).
	pathDropped
	// pathReleased: the path's last holder left.
	pathReleased
)

// pathEvent is what changed on a path without a link, for its route: a holder attached (holder), its link dropped (its
// holders went loading), its last holder left.
type pathEvent struct {
	kind   pathEventKind
	holder upstream
}

// path is the parent link or one route address: its current link and its holders, held on that link and again on every
// next one. changed runs when it gets a holder without a link, loses its link or its last holder (a route decides whether
// to resolve), saying which; suspended_ tells whether its route resolves nothing for it now (nil for a path no route
// suspends: the parent's, the host's unplaced one). With a gate, only the holders it admits are held on the links; the others
// wait withheld, told by the gate.
type path struct {
	// holders are Pi's `Set<Upstream>`: insertion order.
	holders_   []upstream
	changed    func(pathEvent)
	suspended_ func() bool
	gate       pathGate
	current    *hostLink
	// everLinked is whether it ever had a link: a host never connected to a parent has no parent path to wait on.
	everLinked bool
	// withheldSet are the holders its gate holds back (undecided, denied, refused): on no link. Made by the first.
	withheldSet map[upstream]struct{}
	// released are the holders a denial released on the current link, each with what cancels its fence while one waits for
	// its `pong` (forward; nil while none does); gone with the link. Made by the first.
	released map[upstream]func()
	// shared are the remote objects the references of its host share over it, by sharing key (decision "References share
	// one wire hold"; Pi's host keeps them in a WeakMap by path): each entry owned by its object, which takes it out.
	shared map[string]*remoteObject
}

// newPath makes a path (Pi's `new Path(changed, suspended?, gate?)`): suspended nil is Pi's absent argument (never
// suspended), gate nil a path without a gate (the parent's, the host's unplaced one, a route without an admission: it
// admits every holder).
func newPath(changed func(pathEvent), suspended func() bool, gate pathGate) *path {
	return &path{changed: changed, suspended_: suspended, gate: gate}
}

// suspended is whether its route suspended it: a new remote reference then holds on its own instead of sharing a held
// object's state.
func (p *path) suspended() bool { return p.suspended_ != nil && p.suspended_() }

// link is the current link while one is open, nil otherwise.
func (p *path) link() *hostLink { return p.current }

// linked is whether it had a link once (it may be down now: its holders wait for the next).
func (p *path) linked() bool { return p.everLinked }

// tableOf is its sharing table, made by its first shared object.
func (p *path) tableOf() map[string]*remoteObject {
	if p.shared == nil {
		p.shared = map[string]*remoteObject{}
	}
	return p.shared
}

// held is whether anything is held over this path.
func (p *path) held() bool { return len(p.holders_) > 0 }

// wanted is whether a holder wants a link: one its gate admits (every holder without a gate). A route resolves nothing for
// an address whose holders are all denied or undecided (slice 14 §2.3).
func (p *path) wanted() bool { return len(p.holders_) > len(p.withheldSet) }

// withholds is whether its gate holds holder back now (it is on no link).
func (p *path) withholds(holder upstream) bool {
	_, held := p.withheldSet[holder]
	return held
}

// attach holds holder over this path: on the current link now, and on every next one. A gated path asks its gate first: a
// holder it does not admit yet is on no link until forward.
func (p *path) attach(holder upstream) {
	if !slices.Contains(p.holders_, holder) {
		p.holders_ = append(p.holders_, holder)
	}
	if p.gate != nil {
		// Withheld while its gate is asked: what the gate tells it meanwhile finds it on no link.
		if p.withheldSet == nil {
			p.withheldSet = map[upstream]struct{}{}
		}
		p.withheldSet[holder] = struct{}{}
		if !p.gate.joined(holder) {
			return
		}
		delete(p.withheldSet, holder)
	}
	if p.current == nil {
		p.changed(pathEvent{kind: pathAttached, holder: holder})
	} else {
		holder.attach(p.current)
	}
}

// requested: a reference joined holder, which the path holds already: without a link it counts as a new request, as an
// attach does (a route whose resolve failed resolves again).
func (p *path) requested(holder upstream) {
	if p.current == nil && !p.withholds(holder) {
		p.changed(pathEvent{kind: pathAttached, holder: holder})
	}
}

// detach: holder was released or ended; no later link holds it.
func (p *path) detach(holder upstream) {
	at := slices.Index(p.holders_, holder)
	if at < 0 {
		return
	}
	p.holders_ = slices.Delete(slices.Clone(p.holders_), at, at+1)
	delete(p.withheldSet, holder)
	p.unfence(holder)
	if p.gate != nil {
		p.gate.left(holder)
	}
	if len(p.holders_) == 0 {
		p.changed(pathEvent{kind: pathReleased})
	}
}

// forward: its gate admits holder now: told so (one that was told what held it back is `loading` again until its next
// binding), then held on the current link, or the path asks for one. A holder a denial released on this very link is held
// again, under its id (relays never renumber, §3), only behind a fence: the release, then a `ping`, and the hold after
// that ping's `pong`. A link is ordered and a peer answers a `ping` after everything it sent before it, so every reply of
// the released hold has arrived, and was dropped, before the hold exists again. **This relies on the route's `resolve`
// returning the link to the final provider that serves the address**, as the route contract requires: an intermediate
// relay would answer the `ping` before the final provider's old replies have drained through it.
func (p *path) forward(holder upstream) {
	if !p.withholds(holder) {
		return
	}
	delete(p.withheldSet, holder)
	holder.admitted()
	link := p.current
	if link == nil {
		p.changed(pathEvent{kind: pathAttached, holder: holder})
		return
	}
	released := p.released
	if _, fenced := released[holder]; !fenced {
		holder.attach(link)
		return
	}
	released[holder] = link.fence(func() {
		delete(released, holder)
		holder.attach(link)
	})
}

// withhold: its gate holds holder back with cause (denied, or a decision that cannot be made): told so, and released on the
// link it is held on. A fence still waiting for its `pong` ends: the next allow sends a new one.
func (p *path) withhold(holder upstream, cause data.FailureCause) {
	if !slices.Contains(p.holders_, holder) {
		return
	}
	if p.withheldSet == nil {
		p.withheldSet = map[upstream]struct{}{}
	}
	forwarded := !p.withholds(holder)
	p.withheldSet[holder] = struct{}{}
	if cancel := p.released[holder]; cancel != nil {
		cancel()
	}
	if _, fenced := p.released[holder]; p.current != nil && (forwarded || fenced) {
		if p.released == nil {
			p.released = map[upstream]func(){}
		}
		p.released[holder] = nil
	}
	holder.withheld(cause)
}

// connected: link is the path's link now: every holder its gate admits is held on it, under its current decision.
func (p *path) connected(link *hostLink) {
	p.current = link
	p.everLinked = true
	for _, holder := range slices.Clone(p.holders_) {
		if !p.withholds(holder) {
			holder.attach(link)
		}
	}
}

// disconnected: link closed (its sides ended their calls already): every holder held on it goes loading until the next link.
// Its fences go with it, and its gate takes back what it cannot forward now: the next link holds what the gate admits then.
func (p *path) disconnected(link *hostLink) {
	if p.current != link {
		return
	}
	p.current = nil
	for _, cancel := range p.released {
		if cancel != nil {
			cancel()
		}
	}
	p.released = nil
	err := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "the link closed"}, nil)
	for _, holder := range slices.Clone(p.holders_) {
		if !p.withholds(holder) {
			holder.dropped(err)
		}
	}
	if p.gate != nil {
		p.gate.unlinked()
	}
	p.changed(pathEvent{kind: pathDropped})
}

// unreachable: its address cannot be reached for now (a route's `resolve` failed, a suspension): every holder that wants its
// link fails waiting.
func (p *path) unreachable(err *data.ChordError) {
	for _, holder := range slices.Clone(p.holders_) {
		if !p.withholds(holder) {
			holder.unreachable(err)
		}
	}
}

// unfence forgets what a denial left of holder on the current link: its fence is cancelled.
func (p *path) unfence(holder upstream) {
	if cancel := p.released[holder]; cancel != nil {
		cancel()
	}
	delete(p.released, holder)
}

// holders is every holder, for the host's close.
func (p *path) holders() []upstream { return slices.Clone(p.holders_) }
