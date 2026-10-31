package host

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/src/next/remote/relay.ts. Relayed holds (§4, §22, R1; decision "Relay flow control per origin"):
// a hold that arrived on one link (downstream) and that this host neither serves nor parses, held again on its path's
// link (upstream) with the same id under its downstream origin's number there; messages are forwarded between the two
// with the origin rewritten and parts untouched.

// relayedUpstream is the upstream link of a relayed hold, and the downstream origin's number on it.
type relayedUpstream struct {
	link   *hostLink
	origin int
}

// relayedHold is one relayed hold: id on its downstream origin, and the same id upstream on its path's current link
// (none while the path has no link). It keeps its `hold` message to hold again on the next link. Calls, `abort`, `retry`
// and `release` go upstream at once; what comes back is forwarded by its downstream origin (providerSide.forward). Two
// downstream holds are two upstream holds, each with its own encoders (R1).
type relayedHold struct {
	id         int
	downstream *providerSide
	// request is what the upstream hold asks for, with the chain and decoded limit this host honoured; its params owned,
	// not a view of the message they came in.
	request holdRequest
	// from is whom the hold acts for, sent on route links.
	from caller
	path *path
	// upstream is the upstream link while the path has one; nil while it has none.
	upstream *relayedUpstream
}

var _ upstream = (*relayedHold)(nil)

// newRelayedHold makes the relayed hold id of downstream; request.Params is copied.
func newRelayedHold(id int, downstream *providerSide, request holdRequest, from caller, via *path) *relayedHold {
	// Params are never deflated: the provider side refused deflated ones.
	if bytes, ok := request.Params.(link.Bytes); ok {
		request.Params = link.Bytes(append([]byte(nil), bytes...))
	}
	return &relayedHold{id: id, downstream: downstream, request: request, from: from, path: via}
}

// kind is "relayed".
func (h *relayedHold) kind() string { return "relayed" }

// caller is whom the hold acts for.
func (h *relayedHold) caller() caller { return h.from }

// start takes the pending hold's place downstream and holds upstream (now, or when the path has a link).
func (h *relayedHold) start() {
	h.downstream.relayed(h)
	h.path.attach(h)
}

// heldOn reports whether it is held on l now: only that link answers for it.
func (h *relayedHold) heldOn(l *hostLink) bool { return h.upstream != nil && h.upstream.link == l }

// attach holds on l, the path's link now: its kept `hold` message, with whom it acts for on a route link.
func (h *relayedHold) attach(l *hostLink) {
	origin := h.downstream.relayTo(l)
	h.upstream = &relayedUpstream{link: l, origin: origin}
	principal, incarnation := actingFor(l, h.from)
	l.send(&link.HoldMessage{
		Origin:      link.Some(origin),
		Hold:        h.id,
		Type:        h.request.Type,
		Version:     h.request.Version,
		Fingerprint: h.request.Fingerprint,
		Params:      h.request.Params,
		Address:     h.request.Address,
		Chain:       h.request.Chain,
		Limit:       h.request.Limit,
		Principal:   principal,
		Incarnation: incarnation,
	})
}

// dropped: the upstream link dropped: downstream its calls fail and the hold goes loading until the path's next link.
func (h *relayedHold) dropped(err *data.ChordError) {
	upstream := h.upstream
	h.upstream = nil
	if upstream != nil {
		h.downstream.upstreamDropped(h, err)
	}
}

// unreachable: its path's address cannot be reached for now: downstream it fails `waiting` with err until the next link.
func (h *relayedHold) unreachable(err *data.ChordError) {
	h.downstream.send(&link.FailedMessage{Hold: h.id, Cause: link.WireCause{Kind: string(data.FailureWaiting), Error: wireError(err)}})
}

// waiting reports whether it waits for its path's link (a route still resolving, or a dropped link's next): a
// revocation ends it then, since the link it is held on next gets no past `revoke` (§4).
func (h *relayedHold) waiting() bool { return h.upstream == nil }

// revoked: revoked while it waited for its path's link: forgotten, and `gone` with err downstream.
func (h *relayedHold) revoked(err *data.ChordError) {
	h.drop()
	h.downstream.forget(h, nil)
	h.downstream.send(&link.GoneMessage{Hold: h.id, Error: link.WireErrorOf(err)})
}

// lost: `gone` from upstream: the hold is forgotten (the downstream origin forwards the message).
func (h *relayedHold) lost() {
	h.drop()
	h.downstream.forget(h, nil)
}

// call forwards a call from downstream (registered there under its id) upstream at once; its reply comes back by the
// origin. Without an upstream link it is answered `unavailable` at once (it crossed the `loading` notice).
func (h *relayedHold) call(message *link.CallMessage) {
	if upstream := h.upstream; upstream != nil {
		upstream.link.send(link.WithOrigin(message, link.Some(upstream.origin)))
		return
	}
	err := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: fmt.Sprintf("hold %d: its upstream link is down", h.id)}, nil)
	h.downstream.reply(message.Call, &link.ErrorMessage{Call: message.Call, Error: link.WireErrorOf(err)})
}

// abort forwards a downstream `abort` of call id.
func (h *relayedHold) abort(id int) {
	if upstream := h.upstream; upstream != nil {
		upstream.link.send(&link.AbortMessage{Origin: link.Some(upstream.origin), Call: id})
	}
}

// retry forwards a downstream `retry` with its operation and the objects it passed through.
func (h *relayedHold) retry(message *link.RetryMessage) {
	if upstream := h.upstream; upstream != nil {
		upstream.link.send(link.WithOrigin(message, link.Some(upstream.origin)))
	}
}

// release: released downstream: released upstream (its calls with it) and no later link holds it.
func (h *relayedHold) release() {
	upstream := h.upstream
	h.drop()
	if upstream != nil {
		upstream.link.send(&link.ReleaseMessage{Origin: link.Some(upstream.origin), Hold: h.id})
	}
}

// drop: no later link holds it (gone, or its downstream origin closed, which `closed` tells the upstream links).
func (h *relayedHold) drop() {
	h.upstream = nil
	h.path.detach(h)
}
