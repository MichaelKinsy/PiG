package host

import (
	"context"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/src/next/remote/host-link.ts. One link of a host (§3, §22): its channel, its role, the peer's
// principal, its limits per part, and both sides: a provider side per origin (holds and calls the peer sent, its own or a
// relay's for one of its downstream links) and the consumer side (holds and calls this host sent). Message kinds are
// directional, so each goes to its side; a revocation is honoured from the parent only. Parts are made and opened here,
// with the host's compressor, or inflated asynchronously for their owner when the host has none.

// linkRole is how a link was made: `parent` by `connect` (holds arriving on it may name whom they act for), `child` by
// `accept`, `route` by a route's `resolve`.
type linkRole string

const (
	linkRoleParent linkRole = "parent"
	linkRoleChild  linkRole = "child"
	linkRoleRoute  linkRole = "route"
)

// hostLinkOptions is what a link is made with.
type hostLinkOptions struct {
	role linkRole
	// principal is the peer, as this host stamps it.
	principal data.Principal
	// trusted skips the message schema check (framing and the owners' checks still run).
	trusted bool
	codec   link.Codec
	limits  link.Limits
}

// linkCalls is how many concurrent calls a host accepts on each of its links, announced in `hello`.
const linkCalls = 256

// inflatePart is a deflated part an owner admitted, with its decoded limit.
type inflatePart struct {
	part  *link.DeflatedPart
	limit float64
}

// hostLink is a link of a host, both directions over one channel. Closing it, from either end, ends both sides: the
// provider sides release the peer's holds, the consumer side's holds go loading until their path's next link.
//
// The channel's handler methods (Opened, Message, Closed) are called from the link's goroutine; each enters the host's
// loop as one section, as a link read does. Everything else runs inside a section.
type hostLink struct {
	host      *chordHost
	role      linkRole
	principal data.Principal
	// trusted is whether the link is trusted: activation chains on holds arriving on it are honoured (Q5).
	trusted bool
	// limits are the limits per part of what arrives on this link (Q2).
	limits link.Limits
	// context is the parent of the work arriving on this link.
	context  context.Context
	consumer *consumerSide
	// providers is the provider side of each origin the peer sent holds for, made by its first; `origin` is honoured on
	// every link (decision "Relay flow control per origin").
	providers orderedTable[int, *providerSide]
	channel   *link.Channel
	// closing is cancelled when the link closes: stops its asynchronous inflates at once.
	closing       context.Context
	closingCancel context.CancelFunc
}

var _ link.ChannelHandler = (*hostLink)(nil)

// newHostLink makes the link of made on host. A link closed already calls Closed at once.
func newHostLink(host *chordHost, made link.Link, options hostLinkOptions, ctx context.Context) *hostLink {
	l := &hostLink{
		host: host, role: options.role, principal: options.principal, trusted: options.trusted, limits: options.limits,
		context: ctx,
	}
	l.closing, l.closingCancel = context.WithCancel(context.Background())
	l.consumer = newConsumerSide(l)
	report := func(err error) { host.report(HostError{Kind: ErrProtocolError, Error: err}) }
	channel, err := link.NewChannel(made, l, link.ChannelOptions{
		Codec: options.codec, Incarnation: host.incarnation, Calls: linkCalls, Trusted: options.trusted, Report: report,
		Now: host.loop().Now,
	}, ctx)
	if err != nil {
		// A `hello` the codec cannot write: nothing was started; the link is closed.
		report(err)
		made.Close()
		l.closingCancel()
		return l
	}
	l.channel = channel
	// Last: a link closed already calls `Closed` at once.
	channel.Start()
	return l
}

// peer is the peer's incarnation, from its `hello`.
func (l *hostLink) peer() string { return l.channel.Peer() }

// greeted is whether the peer's `hello` arrived (it may have arrived while the link was made).
func (l *hostLink) greeted() bool { return l.channel != nil && l.channel.Opened() }

// open is whether the link is open; one closed already when made is not kept.
func (l *hostLink) open() bool { return l.channel != nil && !l.channel.Closed() }

func (l *hostLink) send(message link.Message) {
	if l.channel == nil {
		return
	}
	if err := l.channel.Send(message); err != nil {
		l.host.report(HostError{Kind: ErrProtocolError, Error: err})
	}
}

func (l *hostLink) close() {
	if l.channel != nil {
		l.channel.Close()
	}
}

// tick is the heartbeat (see Channel.Tick).
func (l *hostLink) tick(now int64, ping, close time.Duration) {
	if l.channel != nil {
		l.channel.Tick(now, ping.Milliseconds(), close.Milliseconds())
	}
}

// Opened implements link.ChannelHandler: the peer's `hello` arrived.
func (l *hostLink) Opened() {
	l.host.loop().Run(func() {
		l.consumer.opened(l.channel.PeerCalls())
		l.host.linkOpened(l)
	})
}

// part is a JSON part this host originates on this link: deflated above the threshold when the host compresses.
func (l *hostLink) part(json string, ctx context.Context) link.Part {
	return link.OriginPart(json, l.host.compression, ctx)
}

// openPart is the JSON of a part arriving on this link, where it is owned, within limits. It fails as link.OpenPart does.
func (l *hostLink) openPart(part link.Part, limits link.Limits, ctx context.Context) (link.Part, error) {
	return link.OpenPart(part, limits, l.host.compression, ctx)
}

// inflates is whether part waits for an asynchronous inflate before its owner can open it: deflated, not inflated yet, on
// a host without a synchronous compressor, and within the wire limit (past it, opening refuses it uninflated).
func (l *hostLink) inflates(part link.Part, limits link.Limits) (*link.DeflatedPart, bool) {
	deflated, ok := part.(*link.DeflatedPart)
	if !ok || deflated.Inflated != nil || l.host.compression != nil || float64(len(deflated.Bytes)) > limits.Part {
		return nil, false
	}
	return deflated, true
}

// inflate inflates parts (deflated parts an owner admitted, each with its decoded limit) one after another, then lets the
// owner handle its message again (handle, inside a section); the channel holds the link's later messages until the
// returned Handling settles. A link closed meanwhile stops the inflate at once and drops the message (race 13). Upstream
// awaits the web `DecompressionStream`; here the inflate runs in a goroutine of the host's loop.
func (l *hostLink) inflate(parts []inflatePart, handle func() error) *link.Handling {
	handling := link.NewHandling()
	l.host.loop().Go(func() {
		for _, p := range parts {
			link.InflateAsync(p.part, p.limit, l.closing, l.context)
			if l.closing.Err() != nil {
				handling.Resolve()
				return
			}
		}
		var err error
		l.host.loop().Run(func() { err = handle() })
		if err != nil {
			handling.Reject(err)
			return
		}
		handling.Resolve()
	})
	return handling
}

// Message implements link.ChannelHandler: a message from the peer, to its side. One whose owner first waits for an
// asynchronous inflate returns its Handling: the channel holds later messages until it settled.
func (l *hostLink) Message(message link.Message) (*link.Handling, error) {
	var handling *link.Handling
	var err error
	l.host.loop().Run(func() { handling, err = l.dispatch(message) })
	return handling, err
}

func (l *hostLink) dispatch(message link.Message) (*link.Handling, error) {
	switch m := message.(type) {
	case *link.HoldMessage:
		return nil, l.hold(m)
	case *link.ReleaseMessage, *link.CallMessage, *link.AbortMessage, *link.RetryMessage, *link.AckMessage:
		// An origin this link has no holds for: its ids are unknown (Q1).
		origin := link.OriginOf(message)
		if provider, ok := l.providers.get(origin.Value); ok {
			return provider.message(message.(link.ProviderMessage))
		}
	case *link.ClosedMessage:
		if provider, ok := l.providers.get(m.Origin); ok {
			provider.closed()
		}
		l.providers.delete(m.Origin)
	case *link.BoundMessage, *link.StateMessage, *link.FailedMessage, *link.LoadingMessage, *link.ResultMessage, *link.ErrorMessage, *link.GoneMessage:
		return l.consumer.message(message.(link.ConsumerMessage))
	case *link.RevokeMessage:
		// Only the parent revokes; a child or a route cannot (race 6). JSON from the parent, the app's revocation type
		// there as here (the same app code).
		if l.role == linkRoleParent {
			return nil, l.host.Revoke(m.Revocation, l.context)
		}
	}
	// `hello`, `ping` and `pong` are the channel's.
	return nil, nil
}

// revokeUnbound ends the holds on this link that hold nothing and act for a principal revoked names (§4).
func (l *hostLink) revokeUnbound(revoked func(principal data.Principal) bool, err *data.ChordError) {
	for _, provider := range l.providers.values() {
		provider.revokeUnbound(revoked, err)
	}
}

// Closed implements link.ChannelHandler: the link closed, from either end.
func (l *hostLink) Closed() {
	l.host.loop().Run(func() {
		l.closingCancel()
		for _, provider := range l.providers.values() {
			provider.closed()
		}
		l.providers.clear()
		l.consumer.closed()
		l.host.unlink(l)
	})
}

// hold sends a `hold` to its origin's provider side, made for the origin's first.
func (l *hostLink) hold(message *link.HoldMessage) error {
	origin := 0
	if message.Origin.Ok {
		origin = message.Origin.Value
	}
	provider, ok := l.providers.get(origin)
	if !ok {
		provider = newProviderSide(l, origin, linkCalls)
		l.providers.set(origin, provider)
	}
	_, err := provider.message(message)
	return err
}
