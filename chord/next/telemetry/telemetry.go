// Package telemetry ports packages/chord/src/telemetry.ts (Pi main c23b9ec1, slice 9): Chord's typed schema and how its
// sites record through it: no span and no attribute object under the no-op context, spans kept open until an event ends
// them, failures with a fixed category, carriers for links, and the instruments of an adapter's metrics made once.
//
// Forced Go differences (D453):
//   - Upstream's span generics (`ChordSpan<N>`, typed starters) have no Go form: a span is a pitelemetry.TelemetrySpan and
//     an attribute a pitelemetry.Attr. The schema is the same data.
//   - "Start a span, keep it open until a promise settles" is a goroutine here: OpenSpan starts the parent's span in one
//     and waits for the adapter to hand the span over (Chord's adapters call back at once).
//   - Upstream caches an adapter's instruments in a WeakMap keyed by its metrics object. A Go interface value has no weak
//     key, so Metrics makes an Instruments that caches its own instruments; the holder of the adapter (a host) makes it
//     once, and it is collected with the host.
package telemetry

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	pitelemetry "github.com/MichaelKinsy/PiG/telemetry"
)

// MaxMessage is the longest message a span's error or event carries (decision "Errors in telemetry").
const MaxMessage = 500

// attributeLength is the longest string attribute Chord records that a peer chose (an object's label with its params
// key, an address key, a principal's kind): stores keep it for days (slice 9 review M1).
const attributeLength = 256

type countingOnly struct{ metrics pitelemetry.TelemetryMetrics }

func (c countingOnly) Metrics() pitelemetry.TelemetryMetrics { return c.metrics }
func (countingOnly) Propagation() pitelemetry.TelemetryPropagation {
	return pitelemetry.NoopTelemetryPropagation
}
func (c countingOnly) StartSpan(_ pitelemetry.SpanOptions, callback func(pitelemetry.TelemetrySpan) error) error {
	return callback(c)
}
func (countingOnly) AddLink(pitelemetry.SpanLink)                {}
func (countingOnly) AddEvent(string, pitelemetry.SpanAttributes) {}
func (countingOnly) SetAttributes(pitelemetry.SpanAttributes)    {}
func (countingOnly) SetStatus(pitelemetry.SpanStatus)            {}

// CountingOnly is a span recording nothing but carrying metrics (a host's adapter's): the telemetry of work a host does
// under a context without a span (its links' accepted by an app with a background context), so the cost counters counted
// through that work's context reach the host's adapter (review L6). It starts no span: span sites treat it as untraced.
func CountingOnly(metrics pitelemetry.TelemetryMetrics) pitelemetry.TelemetrySpan {
	return countingOnly{metrics: metrics}
}

// Untraced reports whether nothing under parent can be recorded: it starts spans as the no-op context does (a host
// without an adapter, a context without a span, CountingOnly's). Span sites then start no span, build no attributes and
// inject no carrier (§30's cost: one call).
func Untraced(parent pitelemetry.TelemetryContext) bool {
	switch parent.(type) {
	case nil, countingOnly:
		return true
	}
	return parent == pitelemetry.TelemetryContext(pitelemetry.NoopTelemetryContext)
}

// OpenSpan is a span of work whose end is an event, not a return (a hold until bound, an attempt until its outcome, a
// call until its handler settled): open until End. Its Span is the no-op span until the adapter started it (Chord's
// adapters call back at once, and NewOpenSpan waits for that).
type OpenSpan struct {
	span  pitelemetry.TelemetrySpan
	done  chan struct{}
	ended bool
	mu    sync.Mutex
}

// NewOpenSpan starts the span name under parent with attributes. Under an untraced parent it starts nothing.
func NewOpenSpan(parent pitelemetry.TelemetryContext, name string, attributes pitelemetry.SpanAttributes, links ...pitelemetry.SpanLink) *OpenSpan {
	open := &OpenSpan{span: pitelemetry.NoopTelemetryContext, done: make(chan struct{})}
	if Untraced(parent) {
		return open
	}
	// A link names the work itself: an extracted parent wrapped as a span (AsSpan) is the parent.
	for index := range links {
		if wrapped, ok := links[index].Context.(parentSpan); ok {
			links[index].Context = wrapped.TelemetryContext
		}
	}
	started := make(chan struct{})
	go func() {
		handed := false
		defer func() {
			if !handed {
				close(started)
			}
		}()
		_ = parent.StartSpan(pitelemetry.SpanOptions{Name: name, Attributes: attributes, Links: links}, func(span pitelemetry.TelemetrySpan) error {
			open.mu.Lock()
			open.span = span
			open.mu.Unlock()
			handed = true
			close(started)
			<-open.done
			return nil
		})
	}()
	<-started
	return open
}

// Span is the span.
func (open *OpenSpan) Span() pitelemetry.TelemetrySpan {
	open.mu.Lock()
	defer open.mu.Unlock()
	return open.span
}

// Ended reports whether End ran.
func (open *OpenSpan) Ended() bool {
	open.mu.Lock()
	defer open.mu.Unlock()
	return open.ended
}

// End ends the span (once; later calls do nothing) and waits until the adapter has settled it.
func (open *OpenSpan) End() {
	open.mu.Lock()
	first := !open.ended
	open.ended = true
	open.mu.Unlock()
	if first {
		close(open.done)
	}
}

// SetOutcome sets the outcome attribute and ends the span.
func (open *OpenSpan) SetOutcome(outcome string) {
	open.Span().SetAttributes(pitelemetry.SpanAttributes{pitelemetry.Attr("outcome", outcome)})
	open.End()
}

// EndWith ends the span with how work that returned err (nil: it settled) ended: ok, aborted (a cancel, ctx's or
// Chord's `aborted`: status ok) or error with category.
func (open *OpenSpan) EndWith(err error, category string, ctx context.Context) {
	if err == nil {
		open.SetOutcome("ok")
		return
	}
	open.SetOutcome(Ended(open.Span(), category, err, ctx))
}

// Capped returns text as a span attribute: its first 256 characters (UTF-16 units, as upstream's slice).
func Capped(text string) string { return truncate(text, attributeLength) }

// HostAttribute is the `chord.host` attribute of the spans of a host at address: its key, `client` without one; capped.
func HostAttribute(address *contracts.Address) string {
	if address == nil {
		return "client"
	}
	return Capped(address.Key)
}

// SpanFailed marks span failed with category and err's message (at most MaxMessage characters).
func SpanFailed(span pitelemetry.TelemetrySpan, category string, err any) {
	message := fmt.Sprint(err)
	if e, ok := err.(error); ok {
		message = e.Error()
	}
	span.SetStatus(pitelemetry.SpanStatus{Status: pitelemetry.SpanStatusCodeError, Error: &pitelemetry.SpanStatusError{Name: category, Message: truncate(message, MaxMessage)}})
}

// truncate is the first n UTF-16 units of text, as upstream's `slice(0, n)` (a split surrogate is dropped).
func truncate(text string, n int) string {
	units := 0
	for i, r := range text {
		width := 1
		if r > 0xFFFF {
			width = 2
		}
		if units+width > n {
			return text[:i]
		}
		units += width
	}
	return text
}

// Ended is how work under span that failed with err ended: "aborted" for a cancel (ctx done, or Chord's `aborted`), its
// status set ok explicitly (an adapter marks a span failed when its callback fails); else "error", the span failed with
// category. The outcome attribute is the caller's to set. A nil ctx is no signal.
func Ended(span pitelemetry.TelemetrySpan, category string, err error, ctx context.Context) string {
	if (ctx != nil && ctx.Err() != nil) || isAborted(err) {
		span.SetStatus(pitelemetry.SpanStatus{Status: pitelemetry.SpanStatusCodeOK})
		return "aborted"
	}
	SpanFailed(span, category, err)
	return "error"
}

func isAborted(err error) bool {
	_, ok := data.IsChordErrorCode(err, data.CodeAborted)
	return ok
}

// CarrierOf is the carrier of span for a link, when its adapter propagates; nil under the no-op context.
func CarrierOf(span pitelemetry.TelemetryContext) pitelemetry.TelemetryCarrier {
	if Untraced(span) {
		return nil
	}
	propagation := span.Propagation()
	if propagation == nil {
		return nil
	}
	return propagation.Inject(span)
}

// ParentOf is the parent carrier names on adapter (a host's), its root when absent or malformed; it never fails.
func ParentOf(carrier pitelemetry.TelemetryCarrier, adapter pitelemetry.TelemetryContext) pitelemetry.TelemetryContext {
	if Untraced(adapter) {
		return adapter
	}
	propagation := adapter.Propagation()
	if propagation == nil {
		return adapter
	}
	parent := propagation.Extract(carrier)
	if parent == nil {
		return adapter
	}
	return parent
}

// parentSpan is a parent context (a carrier's, extracted by an adapter) as the span a ctx carries: it starts children and
// propagates as it does; setting attributes, links or events on it, which Pi's context (a TelemetryContext without those
// members) cannot, does nothing.
type parentSpan struct{ pitelemetry.TelemetryContext }

func (parentSpan) AddLink(pitelemetry.SpanLink)                {}
func (parentSpan) AddEvent(string, pitelemetry.SpanAttributes) {}
func (parentSpan) SetAttributes(pitelemetry.SpanAttributes)    {}
func (parentSpan) SetStatus(pitelemetry.SpanStatus)            {}

// AsSpan is parent as the span a ctx carries (`chordctx.WithTelemetry`). parent is traced: not the no-op context.
func AsSpan(parent pitelemetry.TelemetryContext) pitelemetry.TelemetrySpan {
	return parentSpan{parent}
}

// Instruments are Chord's instruments on one adapter's metrics, each made once per name.
type Instruments struct {
	typed *pitelemetry.TypedTelemetryMetrics
	mu    sync.Mutex
	made  map[instrumentKey]any
}

// instrumentKey names one made instrument: its kind (`c`, `u` or `h`) and name.
type instrumentKey struct {
	kind byte
	name string
}

// noopInstruments is shared: no adapter, nothing made per use.
var noopInstruments = &Instruments{}

// Metrics is Chord's instruments on metrics (an adapter's, or nil: no-op).
func Metrics(metrics pitelemetry.TelemetryMetrics) *Instruments {
	if metrics == nil || metrics == pitelemetry.NoopTelemetryMetrics {
		return noopInstruments
	}
	return &Instruments{typed: pitelemetry.CreateTypedMetrics(metrics, Schema), made: map[instrumentKey]any{}}
}

func once[T any](instruments *Instruments, kind byte, name string, make func() T) T {
	key := instrumentKey{kind, name}
	instruments.mu.Lock()
	defer instruments.mu.Unlock()
	if instrument, ok := instruments.made[key]; ok {
		return instrument.(T)
	}
	instrument := make()
	instruments.made[key] = instrument
	return instrument
}

// noop is whether these are the shared no-op instruments: they are the adapter's own no-op ones, with no metadata of the
// schema to hand them (a call that records nothing allocates nothing for it).
func (instruments *Instruments) noop() bool { return instruments.made == nil }

// Counter is the counter name.
func (instruments *Instruments) Counter(name string) pitelemetry.TelemetryCounter {
	if instruments.noop() {
		return pitelemetry.NoopTelemetryMetrics.Counter(name, pitelemetry.InstrumentOptions{})
	}
	return once(instruments, 'c', name, func() pitelemetry.TelemetryCounter { return instruments.typed.Counter(name) })
}

// UpDownCounter is the up/down counter name.
func (instruments *Instruments) UpDownCounter(name string) pitelemetry.TelemetryUpDownCounter {
	if instruments.noop() {
		return pitelemetry.NoopTelemetryMetrics.UpDownCounter(name, pitelemetry.InstrumentOptions{})
	}
	return once(instruments, 'u', name, func() pitelemetry.TelemetryUpDownCounter { return instruments.typed.UpDownCounter(name) })
}

// Histogram is the histogram name.
func (instruments *Instruments) Histogram(name string) pitelemetry.TelemetryHistogram {
	if instruments.noop() {
		return pitelemetry.NoopTelemetryMetrics.Histogram(name, pitelemetry.HistogramOptions{})
	}
	return once(instruments, 'h', name, func() pitelemetry.TelemetryHistogram { return instruments.typed.Histogram(name) })
}

// CarrierKeys are the keys of carrier in order: order when it is exactly its keys (the insertion order a carrier came with:
// Pi's object keeps it, a Go map has none), otherwise sorted.
func CarrierKeys(carrier map[string]string, order []string) []string {
	if len(order) == len(carrier) {
		seen := make(map[string]struct{}, len(order))
		exact := true
		for _, key := range order {
			if _, ok := carrier[key]; !ok {
				exact = false
				break
			}
			if _, dup := seen[key]; dup {
				exact = false
				break
			}
			seen[key] = struct{}{}
		}
		if exact {
			return order
		}
	}
	return slices.Sorted(maps.Keys(carrier))
}
