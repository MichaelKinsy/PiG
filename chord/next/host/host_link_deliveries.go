package host

import "context"

// No Pi file corresponds to this one: Pi's hosts share one event loop, so a test or an app that awaits a reload and then
// `transported()` and `delivered()` waits for the frames of other hosts by hops of that loop. A link whose other end lives in
// another process (chord/nextnode/plugins' Node hosts) has no shared loop, so it asks the host that takes it for the wait.

// pig additive (D223): a link to another process waits for the host that took it.
// DeliveryAwaiter is a link that wants to wait for the host that took it. A host that takes such a link (Accept, Connect, a
// route's Resolve) calls Taken once, after it made the channel over the link and before the channel starts (so before the host sends on the link or listens to it), with await: await blocks until everything
// the link has handed the host so far has been handled and the deliveries it scheduled (a watch's listener calls, in the
// order the host's batch made them) have entered their listeners, up to a listener's first wait, as a host's own `Reload`
// waits before it returns its report. It returns the cause of ctx when ctx ends the wait, and returns at once for a link
// that closed. await may be called from any goroutine, and from a section of the host.
type DeliveryAwaiter interface {
	Taken(await func(ctx context.Context) error)
}

// awaitDeliveries is the await a taken link is given: the channel handled what it received (a message that waits for an
// asynchronous inflate holds the later ones), then the host's deliveries entered their listeners.
func (l *hostLink) awaitDeliveries(ctx context.Context) error {
	if err := l.channel.Idle(ctx); err != nil {
		return err
	}
	return l.host.awaitDelivered(ctx)
}

// awaitDelivered waits for the sections queued so far and for the deliveries scheduled before the call to enter their
// listeners (the wait of `Reload`'s report).
func (h *chordHost) awaitDelivered(ctx context.Context) error {
	h.enter(func() {})
	// pig divergence (D224): a listener that blocks on something the host cannot see, without calling chord.DeliveryEntered, holds
	// this wait back; Pi's listener awaits.
	if err := h.scheduler.AwaitEntered(ctx); err != nil {
		return err
	}
	h.enter(func() {})
	return nil
}
