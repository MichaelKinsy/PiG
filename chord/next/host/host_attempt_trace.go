package host

import (
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	chordtelemetry "github.com/MichaelKinsy/PiG/chord/next/telemetry"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports the telemetry of packages/chord/src/host/attempt.ts: an attempt's `chord.activate` span, its park and its outcome.

// attemptTrace is what an attempt's activation span links to (its object's first trigger, or its last attempt's span)
// and its number.
type attemptTrace struct {
	trigger telemetry.TelemetryContext
	attempt int
}

// startActivation starts an activation span on the host's adapter linked to link (none when it records nothing); the
// activation's steps (its natives' opens, its references' holds) run under it.
func (a *attempt) startActivation(link telemetry.TelemetryContext) {
	object := a.object
	host := object.host
	if chordtelemetry.Untraced(host.telemetry) {
		return
	}
	attributes := telemetry.SpanAttributes{
		telemetry.Attr("chord.host", host.spanHost),
		telemetry.Attr("type", a.served.contract.Name),
		telemetry.Attr("object", chordtelemetry.Capped(object.text)),
		telemetry.Attr("attempt", float64(a.trace.attempt)),
	}
	var links []telemetry.SpanLink
	if !chordtelemetry.Untraced(link) {
		links = append(links, telemetry.SpanLink{Context: link})
	}
	activation := chordtelemetry.NewOpenSpan(host.telemetry, "chord.activate", attributes, links...)
	a.activation = activation
	a.opening = chordctx.WithTelemetry(a.opening, activation.Span())
	a.opener = chordctx.WithValue(a.opening, openingKey, object)
}

// activationSpan is its current (or last) activation span; nil when the host records nothing.
func (a *attempt) activationSpan() telemetry.TelemetrySpan {
	if a.activation == nil {
		return nil
	}
	return a.activation.Span()
}

// parkedOn: it parked showing shown: its activation span ends `parked` with the cause as an event.
func (a *attempt) parkedOn(shown *RefState) {
	if a.parkedAt.IsZero() {
		a.parkedAt = time.Now()
	}
	activation := a.activation
	if activation == nil || activation.Ended() {
		return
	}
	cause := "loading"
	if shown.Kind == RefStateFailed {
		cause = chordtelemetry.Message(shown.Cause.Error.Error())
	}
	activation.Span().AddEvent("chord.park", telemetry.SpanAttributes{telemetry.Attr("cause", cause)})
	activation.SetOutcome("parked")
}

// parkEnded: it leaves its parking (its dependencies live, or abandoned): its park's duration is recorded.
func (a *attempt) parkEnded() {
	if a.parkedAt.IsZero() {
		return
	}
	a.object.host.metrics.Histogram("chord.park.duration").Record(float64(time.Since(a.parkedAt))/float64(time.Millisecond), nil, nil)
	a.parkedAt = time.Time{}
}

// unparked: its dependencies are live after it parked: its own steps run in a new activation span, linked to the parked one.
func (a *attempt) unparkedTrace() {
	a.parkEnded()
	if parked := a.activation; parked != nil && parked.Ended() {
		a.startActivation(parked.Span())
	}
}

// activated is its activation's outcome, as its object took it: its span ends with it (a failure's error and a transient
// one's backoff an event). An attempt still parked (its span ended `parked`) that fails or ends there records it in a
// short span linked to the parked one, as its resumed steps would run in one (review L4).
func (a *attempt) activated(outcome activation, retryAt *int64) {
	current := a.activation
	if current == nil {
		return
	}
	if current.Ended() {
		if outcome.kind == activationLive {
			return
		}
		a.startActivation(current.Span())
	}
	current = a.activation
	span := current.Span()
	name := "live"
	switch outcome.kind {
	case activationFailed:
		name = "failed"
		chordtelemetry.SpanFailed(span, "activation_failed", outcome.cause.Error)
		if retryAt != nil {
			backoff := *retryAt - a.object.host.loop().Now()
			span.AddEvent("chord.retry", telemetry.SpanAttributes{telemetry.Attr("backoff", float64(max(backoff, 0)))})
		}
	case activationEnded:
		name = "ended"
	}
	current.SetOutcome(name)
}
