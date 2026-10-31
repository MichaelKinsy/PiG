package host

import (
	"slices"

	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/src/next/remote/path.ts. Paths (§3 recovery): where this host's upstream holds go, the parent
// link or a route address's link, across links. Each new link of a path holds every holder of the path again; when it
// drops, every holder is told.

// upstream is what a path holds on its links: one of this host's remote references (*remoteObject), or a hold it relays
// (*relayedHold). It must be comparable (a pointer).
type upstream interface {
	// kind is "remote" for a remote object, "relayed" for a relayed hold.
	kind() string
	// attach holds it on link, the path's link now (a first hold, or a re-hold on a new link).
	attach(link *hostLink)
	// dropped: the path's link closed; it goes loading until the next link's hold is bound.
	dropped(err *data.ChordError)
	// unreachable: its path's address cannot be reached for now (a route's `resolve` failed, a suspension): it fails
	// `waiting` with err, still on the path, bound by its next link.
	unreachable(err *data.ChordError)
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
// to resolve), saying which.
type path struct {
	// holders are Pi's `Set<Upstream>`: insertion order.
	holders_ []upstream
	changed  func(pathEvent)
	current  *hostLink
	// everLinked is whether it ever had a link: a host never connected to a parent has no parent path to wait on.
	everLinked bool
}

func newPath(changed func(pathEvent)) *path { return &path{changed: changed} }

// link is the current link while one is open, nil otherwise.
func (p *path) link() *hostLink { return p.current }

// linked is whether it had a link once (it may be down now: its holders wait for the next).
func (p *path) linked() bool { return p.everLinked }

// held is whether anything is held over this path.
func (p *path) held() bool { return len(p.holders_) > 0 }

// attach holds holder over this path: on the current link now, and on every next one.
func (p *path) attach(holder upstream) {
	if !slices.Contains(p.holders_, holder) {
		p.holders_ = append(p.holders_, holder)
	}
	if p.current == nil {
		p.changed(pathEvent{kind: pathAttached, holder: holder})
	} else {
		holder.attach(p.current)
	}
}

// detach: holder was released or ended; no later link holds it.
func (p *path) detach(holder upstream) {
	if at := slices.Index(p.holders_, holder); at >= 0 {
		p.holders_ = slices.Delete(slices.Clone(p.holders_), at, at+1)
	}
	if len(p.holders_) == 0 {
		p.changed(pathEvent{kind: pathReleased})
	}
}

// connected: link is the path's link now: every holder is held on it.
func (p *path) connected(link *hostLink) {
	p.current = link
	p.everLinked = true
	for _, holder := range slices.Clone(p.holders_) {
		holder.attach(link)
	}
}

// disconnected: link closed (its sides ended their calls already): every holder goes loading until the next link.
func (p *path) disconnected(link *hostLink) {
	if p.current != link {
		return
	}
	p.current = nil
	err := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "the link closed"}, nil)
	for _, holder := range slices.Clone(p.holders_) {
		holder.dropped(err)
	}
	p.changed(pathEvent{kind: pathDropped})
}

// unreachable: its address cannot be reached for now (a route's `resolve` failed, a suspension): every holder fails
// waiting.
func (p *path) unreachable(err *data.ChordError) {
	for _, holder := range slices.Clone(p.holders_) {
		holder.unreachable(err)
	}
}

// holders is every holder, for the host's close.
func (p *path) holders() []upstream { return slices.Clone(p.holders_) }
