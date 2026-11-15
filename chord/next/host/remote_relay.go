package host

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	chordtelemetry "github.com/MichaelKinsy/PiG/chord/next/telemetry"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/remote/relay.ts. Relayed holds (§4, §22, R1; decision "Relay flow control per origin"):
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
	// relaying is its `chord.relay` span until it is first held upstream (or dropped); nil when this host records nothing.
	relaying *chordtelemetry.OpenSpan
	// trace is the carrier of its first upstream hold: the relay span's, or the arriving one as it came when this host
	// records nothing; a re-hold's span links to it.
	trace map[string]string
	// traceOrder is the order of trace's keys while it is the arriving carrier (nil: the relay span's, written sorted).
	traceOrder []string
	// holds is how many times it was held upstream.
	holds int
	// heldBack is what its downstream was told holds it back (withheld), until it is told that it is admitted again; nil
	// while none.
	heldBack *data.FailureCause
}

var _ upstream = (*relayedHold)(nil)

// newRelayedHold makes the relayed hold id of downstream; request.Params is copied.
func newRelayedHold(id int, downstream *providerSide, request holdRequest, from caller, via *path) *relayedHold {
	// Params are never deflated: the provider side refused deflated ones.
	if bytes, ok := request.Params.(link.Bytes); ok {
		request.Params = link.Bytes(append([]byte(nil), bytes...))
	}
	trace, order := request.Trace, request.TraceOrder
	request.Trace, request.TraceOrder = nil, nil
	h := &relayedHold{id: id, downstream: downstream, request: request, from: from, path: via, trace: trace, traceOrder: order}
	adapter := downstream.link.host.telemetry
	if !chordtelemetry.Untraced(adapter) {
		h.relaying = chordtelemetry.NewOpenSpan(chordtelemetry.ParentOf(trace, adapter), "chord.relay", h.attributes("hold"))
		// Upstream the hold carries the relay's span (a relay that records nothing passes the carrier on as it came).
		if carrier := chordtelemetry.CarrierOf(h.relaying.Span()); carrier != nil {
			h.trace, h.traceOrder = carrier, nil
		}
	}
	return h
}

// attributes are a relay span's: its host's `chord.host` (the downstream link's host), and names from the wire, bounded by
// the message schema (untrusted links) and capped here.
func (h *relayedHold) attributes(member string) telemetry.SpanAttributes {
	return telemetry.SpanAttributes{
		telemetry.Attr("chord.host", h.downstream.link.host.spanHost),
		telemetry.Attr("type", chordtelemetry.Capped(h.request.Type)),
		telemetry.Attr("member", member),
		telemetry.Attr("upstream.kind", chordtelemetry.Capped(h.request.Address.Kind)),
	}
}

// span is its `chord.relay` span until it is held upstream (a route's resolve links to it); nil when not recorded.
func (h *relayedHold) span() telemetry.TelemetrySpan {
	if h.relaying == nil {
		return nil
	}
	return h.relaying.Span()
}

// holdTrace is the carrier of this upstream hold: the first one's; a re-hold after an upstream drop is a fresh
// `chord.relay` root linked to the first (none when this host records nothing), never the first's again (review L1).
func (h *relayedHold) holdTrace(l *hostLink) (map[string]string, []string) {
	first := h.holds == 0
	h.holds++
	if first {
		return h.trace, h.traceOrder
	}
	adapter := l.host.telemetry
	if h.trace == nil || chordtelemetry.Untraced(adapter) {
		return nil, nil
	}
	again := chordtelemetry.NewOpenSpan(adapter, "chord.relay", h.attributes("hold"), telemetry.SpanLink{Context: chordtelemetry.ParentOf(h.trace, adapter)})
	carrier := chordtelemetry.CarrierOf(again.Span())
	again.SetOutcome("ok")
	return carrier, nil
}

// relayed: it was held upstream, failed or dropped first: its `chord.relay` span ends with outcome.
func (h *relayedHold) relayed(outcome string, err error) {
	relaying := h.relaying
	if relaying == nil {
		return
	}
	h.relaying = nil
	if err != nil {
		chordtelemetry.SpanFailed(relaying.Span(), "relay_failed", err)
	}
	relaying.SetOutcome(outcome)
}

// relayEnded: a relayed call's `chord.relay` span ends with its reply forwarded downstream (`ok`, or the error's:
// `aborted` for a cancel), or `aborted` without one (an abort, a release, a closed link).
func relayEnded(span *chordtelemetry.OpenSpan, reply link.OriginMessage) {
	switch typed := reply.(type) {
	case nil:
		span.SetOutcome("aborted")
	case *link.ResultMessage:
		span.SetOutcome("ok")
	case *link.ErrorMessage:
		failure, _ := link.ErrorFromWire(typed.Error)
		span.SetOutcome(chordtelemetry.Ended(span.Span(), "relay_failed", failure, nil))
	default:
		span.SetOutcome("ok")
	}
}

// kind is "relayed".
func (h *relayedHold) kind() string { return "relayed" }

// caller is whom the hold acts for.
func (h *relayedHold) caller() caller { return h.from }

// principal is the principal the hold acts for (Pi's `caller.principal`).
func (h *relayedHold) principal() data.Principal { return h.from.principal }

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
	trace, traceOrder := h.holdTrace(l)
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
		Trace:       trace,
		TraceOrder:  traceOrder,
	})
	h.relayed("ok", nil)
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
	h.relayed("error", err)
	h.downstream.send(&link.FailedMessage{Hold: h.id, Cause: link.WireCause{Kind: string(data.FailureWaiting), Error: wireError(err)}})
}

// withheld: its route's admission holds it back with cause (slice 14 §2.3): released upstream when it is held there (what
// still arrives for it from that link is dropped: a link answers only for what is relayed over it); downstream its calls, in
// flight and waiting, are rejected and it is told `failed` with the cause. It stays on its path: an allow holds it again,
// under its id.
func (h *relayedHold) withheld(cause data.FailureCause) {
	upstream := h.upstream
	h.upstream = nil
	if upstream != nil {
		upstream.link.send(&link.ReleaseMessage{Origin: link.Some(upstream.origin), Hold: h.id})
	}
	h.relayed("error", cause.Error)
	h.heldBack = &cause
	h.downstream.withheld(h, cause)
}

// admitted: its route's admission allows it: a hold that was told what held it back is `loading` downstream from here on,
// through the fence and its host's start, until its next `bound` (the caller has access again: it is not shown "no access"
// meanwhile). A hold never told anything is loading already.
func (h *relayedHold) admitted() {
	if h.heldBack == nil {
		return
	}
	h.heldBack = nil
	h.downstream.send(&link.LoadingMessage{Hold: h.id})
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

// submit: a call from downstream (registered there under its id) waits in its upstream link's window for this origin and
// is forwarded (`call`) as a slot frees, at once when one is free and nothing waits (a trusted upstream announced no
// window); its reply comes back by the origin. Without an upstream link it is answered at once: `unavailable` (it crossed
// the `loading` notice), or, held back by its route's admission, what held it back (it crossed that `failed`:
// `forbidden/revoked` for a denial, access withdrawn under a call already made).
func (h *relayedHold) submit(call *relayedCall) {
	upstream := h.upstream
	if upstream == nil {
		err := h.unreached()
		h.downstream.reply(call.message.Call, &link.ErrorMessage{Call: call.message.Call, Error: link.WireErrorOf(err)})
		return
	}
	call.queue(upstream.link.consumer.relayWindow(upstream.origin))
}

// unreached is what a call that finds it on no upstream link is answered.
func (h *relayedHold) unreached() *data.ChordError {
	cause := h.heldBack
	label := fmt.Sprintf("hold %d", h.id)
	if cause == nil {
		return data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: label + ": its upstream link is down"}, nil)
	}
	if cause.Kind == data.FailureDenied {
		return revoked(label, cause.Error)
	}
	return cause.Error
}

// call forwards a submitted call upstream, now that its window has a slot; it returns its `chord.relay` span when this
// host records.
func (h *relayedHold) call(message *link.CallMessage) *chordtelemetry.OpenSpan {
	// A call waits only in its hold's current upstream window: the drop answers it first (upstreamDropped), a release
	// forgets it.
	upstream := h.upstream
	// A downstream link that is not trusted (a page) cannot claim old work: its flag is dropped here.
	if message.Old && !h.downstream.link.trusted {
		unmarked := *message
		unmarked.Old = false
		message = &unmarked
	}
	adapter := upstream.link.host.telemetry
	if chordtelemetry.Untraced(adapter) {
		upstream.link.send(link.WithOrigin(message, link.Some(upstream.origin)))
		return nil
	}
	span := chordtelemetry.NewOpenSpan(chordtelemetry.ParentOf(message.Trace, adapter), "chord.relay", h.attributes(fmt.Sprintf("#%d", message.Method)))
	forwarded := *message
	if carrier := chordtelemetry.CarrierOf(span.Span()); carrier != nil {
		forwarded.Trace = carrier
		forwarded.TraceOrder = nil
	}
	upstream.link.send(link.WithOrigin(&forwarded, link.Some(upstream.origin)))
	return span
}

// relayedCallKind is where a relayed call stands: new (registered, answered at once when its hold has no upstream link),
// queued (waiting in its upstream link's window for its origin), forwarded (upstream, its `chord.relay` span when this host
// records) or ended (answered, aborted, its hold or origin gone).
type relayedCallKind uint8

const (
	relayedCallNew relayedCallKind = iota
	relayedCallQueued
	relayedCallForwarded
	relayedCallEnded
)

// relayedCall is a downstream call on a relayed hold, as its downstream origin keeps it (decision "Call windows only for
// untrusted links": a relay honours each upstream window): queued in its upstream link's window for the origin until a
// slot forwards it (send), then in flight until its reply, its abort, its hold's end or the upstream's drop.
type relayedCall struct {
	hold    *relayedHold
	message *link.CallMessage
	state   relayedCallKind
	window  *callWindow[*relayedCall]
	span    *chordtelemetry.OpenSpan
}

func newRelayedCall(hold *relayedHold, message *link.CallMessage) *relayedCall {
	return &relayedCall{hold: hold, message: message}
}

func (c *relayedCall) holdID() int { return c.hold.id }

// queue waits in window (its hold's upstream link's, for its origin), forwarded at once when a slot is free.
func (c *relayedCall) queue(window *callWindow[*relayedCall]) {
	c.state, c.window = relayedCallQueued, window
	window.submit(c)
}

// send: its window has a slot for it: forwarded upstream. A window sends only the calls queued in it.
func (c *relayedCall) send() {
	c.state = relayedCallForwarded
	c.span = c.hold.call(c.message)
}

// unqueue takes it out of its window when it still waits there (ended unsent); whether it did.
func (c *relayedCall) unqueue() bool {
	if c.state != relayedCallQueued {
		return false
	}
	c.state = relayedCallEnded
	c.window.remove(c)
	return true
}

// end ends it: a waiting one leaves its window, a forwarded one frees its slot (which may forward the next waiting call)
// and its span ends with reply (nil: `aborted`).
func (c *relayedCall) end(reply link.OriginMessage) {
	state := c.state
	c.state = relayedCallEnded
	if state == relayedCallQueued {
		c.window.remove(c)
	}
	if state != relayedCallForwarded {
		return
	}
	if c.span != nil {
		relayEnded(c.span, reply)
	}
	c.window.freed()
}

// endAll ends calls (of one hold or origin): the waiting ones leave their windows first, so a slot a forwarded one frees
// never forwards one of them.
func endAll(calls []*relayedCall) {
	for _, call := range calls {
		call.unqueue()
	}
	for _, call := range calls {
		call.end(nil)
	}
}

// abort forwards a downstream `abort` of call id.
func (h *relayedHold) abort(id int) {
	if upstream := h.upstream; upstream != nil {
		upstream.link.send(&link.AbortMessage{Origin: link.Some(upstream.origin), Call: id})
	}
}

// retry forwards a downstream `retry` with its hop budget as it is (a relay visits no object).
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
	h.relayed("aborted", nil)
	h.upstream = nil
	h.path.detach(h)
}
