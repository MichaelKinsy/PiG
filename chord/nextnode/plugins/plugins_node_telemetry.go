package plugins

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// The telemetry of a Node subprocess's host, replayed into the app's adapter (docs/plan/chord-plugins-node-evaluator.md,
// 10.6). No Pi file corresponds to this one: Pi's host takes the app's `TelemetryContext` and calls it in process. Here the
// process's host gets a proxy (node/telemetry.mjs) whose spans and metrics are ordered `telemetry` events on the control
// channel, and this file runs each of them on the adapter, on the goroutine that reads the channel, so the adapter sees the
// order the process made them in.
//
// A span is an open callback of the adapter's `StartSpan`: its goroutine blocks until the process's `end` event, and the
// events in between (`attributes`, `event`, `link`, `status`) call the span the callback received. `end` returns the callback
// with the error the process's callback threw (a thrown Error is an error with its `Name`; any other thrown value is a panic
// of a non-error, which the adapter records as a bare error status, as pi-telemetry does); the adapter has settled the span
// when `StartSpan` returned, and the next event is replayed after that. A span still open when the process ends ends with an
// error status.
//
// Carriers need no call to the process: `inject` in the process makes `{"pig-node-telemetry": "<scope>:<id>"}` from a span's
// id; this file resolves it to the adapter's own carrier of that span (taken when the span started, kept for the last
// carrierMemory spans) and extracts it with the adapter. A carrier that is not of this kind is the adapter's own and is
// extracted by it as it is.

// carrierKey is the key of a carrier the process made from a span's id.
const carrierKey = "pig-node-telemetry"

// carrierMemory is how many spans' own carriers a process's telemetry keeps for resolving a carrier of a span that ended.
const carrierMemory = 4096

// telemetryScopes are the replays of the live processes, by scope: a carrier one process made resolves in any other the same
// app runs.
var telemetryScopes sync.Map

// telemetryWire is what `load` tells the process about the adapter: the scope of its carriers and which of the adapter's
// capabilities exist.
type telemetryWire struct {
	Scope       string `json:"scope"`
	Metrics     bool   `json:"metrics"`
	Propagation bool   `json:"propagation"`
}

// nodeTelemetry is the replay of one process's telemetry into adapter.
type nodeTelemetry struct {
	adapter     telemetry.TelemetryContext
	metrics     telemetry.TelemetryMetrics
	propagation telemetry.TelemetryPropagation
	scope       string

	mu    sync.Mutex
	live  map[int64]*replayedSpan
	own   map[int64]telemetry.TelemetryCarrier
	order []int64
	// instruments are the adapter's instruments, by kind, name and options.
	instruments map[string]any
}

// newNodeTelemetry replays into adapter, with a scope of its own.
func newNodeTelemetry(adapter telemetry.TelemetryContext) (*nodeTelemetry, telemetryWire) {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	t := &nodeTelemetry{
		adapter: adapter, metrics: adapter.Metrics(), propagation: adapter.Propagation(), scope: hex.EncodeToString(raw[:]),
		live: map[int64]*replayedSpan{}, own: map[int64]telemetry.TelemetryCarrier{}, instruments: map[string]any{},
	}
	telemetryScopes.Store(t.scope, t)
	return t, telemetryWire{Scope: t.scope, Metrics: t.metrics != nil, Propagation: t.propagation != nil}
}

// close ends the spans still open (with an error status) and forgets the scope.
func (t *nodeTelemetry) close() {
	telemetryScopes.Delete(t.scope)
	t.mu.Lock()
	open := make([]*replayedSpan, 0, len(t.live))
	for _, span := range t.live {
		open = append(open, span)
	}
	t.live = map[int64]*replayedSpan{}
	t.mu.Unlock()
	for _, span := range open {
		span.end <- spanEnd{abandoned: true}
		<-span.finished
	}
}

// ExtractTelemetry is `propagation.extract(carrier)` of the app's adapter for a carrier a plugin made (its `inject` of a span
// of this process's telemetry, which this resolves to the adapter's own carrier of that span) or any carrier of the adapter's:
// the parent whose spans continue that trace. It is what a Go function that a plugin calls with a carrier uses to continue the
// plugin's trace. The no-op parent is returned when the host has no adapter or the adapter propagates nothing, and the
// adapter's root for a carrier it does not know (as the adapter's own extract does).
func (h *NodeHost) ExtractTelemetry(carrier telemetry.TelemetryCarrier) telemetry.TelemetryContext {
	h.cb.mu.Lock()
	replay := h.cb.telemetry
	h.cb.mu.Unlock()
	if replay == nil {
		return telemetry.NoopTelemetryContext
	}
	return replay.extract(carrier)
}

// extract resolves carrier to the adapter's parent (see ExtractTelemetry).
func (t *nodeTelemetry) extract(carrier telemetry.TelemetryCarrier) telemetry.TelemetryContext {
	if t.propagation == nil {
		return telemetry.NoopTelemetryContext
	}
	if id, ok := carrier[carrierKey]; ok {
		scope, span, found := strings.Cut(id, ":")
		owner := t
		if scope != t.scope {
			other, known := telemetryScopes.Load(scope)
			if !known {
				return t.propagation.Extract(nil)
			}
			owner = other.(*nodeTelemetry)
		}
		number, err := strconv.ParseInt(span, 10, 64)
		if !found || err != nil {
			return owner.propagation.Extract(nil)
		}
		owner.mu.Lock()
		own := owner.own[number]
		owner.mu.Unlock()
		return owner.propagation.Extract(own)
	}
	return t.propagation.Extract(carrier)
}

// replayedSpan is a span of the adapter whose callback waits for its end.
type replayedSpan struct {
	started  chan struct{}
	end      chan spanEnd
	finished chan struct{}
	// span is the span the adapter's callback received; nil when the adapter never started the callback.
	span telemetry.TelemetrySpan
	once sync.Once
}

// spanEnd is how a span ended: its callback returned, threw an Error (name, message), threw something else (bare), or the
// process ended (abandoned).
type spanEnd struct {
	failed    bool
	name      string
	message   string
	bare      bool
	abandoned bool
}

// thrown is the error a span's callback returns for an Error the process threw: its Name is `error.name`, which the adapter
// records in the span's status.
type thrown struct{ name, message string }

func (e *thrown) Error() string { return e.message }
func (e *thrown) Name() string  { return e.name }

// bareThrow is the panic of a span whose callback threw a value that is no Error: the adapter records an error status
// without a name or a message, as it does for a thrown non-error.
type bareThrow struct{}

// abandoned is the error of a span open when the process ended.
type abandoned struct{}

func (abandoned) Error() string { return "the node subprocess ended while the span was open" }

// run starts the span on parent and keeps its callback open until its end.
func (r *replayedSpan) run(parent telemetry.TelemetryContext, options telemetry.SpanOptions) {
	defer close(r.finished)
	defer func() {
		if failure := recover(); failure != nil {
			if _, bare := failure.(bareThrow); !bare {
				panic(failure)
			}
		}
	}()
	_ = parent.StartSpan(options, func(span telemetry.TelemetrySpan) error {
		r.span = span
		r.once.Do(func() { close(r.started) })
		end := <-r.end
		switch {
		case end.abandoned:
			return abandoned{}
		case end.bare:
			panic(bareThrow{})
		case end.failed:
			return &thrown{name: end.name, message: end.message}
		}
		return nil
	})
	// An adapter that did not call the callback leaves the span unrecorded.
	r.once.Do(func() { close(r.started) })
}

// replayEvent is one `telemetry` event of the process.
type replayEvent struct {
	Op          string            `json:"op"`
	Span        int64             `json:"span"`
	Parent      *contextReference `json:"parent"`
	Name        string            `json:"name"`
	Attributes  *delta.JsonObject `json:"attributes"`
	Links       []linkWire        `json:"links"`
	Context     *contextReference `json:"context"`
	Status      *statusWire       `json:"status"`
	Error       *thrownWire       `json:"error"`
	Kind        string            `json:"kind"`
	Description *string           `json:"description"`
	Unit        *string           `json:"unit"`
	Boundaries  []float64         `json:"boundaries"`
	Value       float64           `json:"value"`
}

// contextReference names a context of the process: a span of its proxy, or the carrier an extracted parent was made from.
type contextReference struct {
	Span    int64                      `json:"span"`
	Carrier telemetry.TelemetryCarrier `json:"carrier"`
}

type linkWire struct {
	Context    contextReference  `json:"context"`
	Attributes *delta.JsonObject `json:"attributes"`
}

type statusWire struct {
	Status string `json:"status"`
	Error  *struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	} `json:"error"`
}

// thrownWire is a span's `end` error: the thrown Error's name and message; both empty for a value that is no Error.
type thrownWire struct {
	Name    *string `json:"name"`
	Message string  `json:"message"`
}

// attributesOf is an attribute bag as the process made it, in its insertion order.
func attributesOf(object *delta.JsonObject) telemetry.SpanAttributes {
	if object == nil {
		return nil
	}
	attributes := make(telemetry.SpanAttributes, 0, object.Len())
	for name, value := range object.All() {
		attributes = append(attributes, telemetry.Attr(name, attributeValue(value)))
	}
	return attributes
}

// attributeValue is a JSON attribute as the adapter's types take it: a string, a number, a boolean, or a slice of one of them.
func attributeValue(value delta.JsonValue) telemetry.AttributeValue {
	list, ok := value.([]any)
	if !ok {
		return value
	}
	switch {
	case allOf[string](list):
		return typedList[string](list)
	case allOf[float64](list):
		return typedList[float64](list)
	case allOf[bool](list):
		return typedList[bool](list)
	}
	return list
}

func allOf[T any](list []any) bool {
	for _, item := range list {
		if _, ok := item.(T); !ok {
			return false
		}
	}
	return true
}

func typedList[T any](list []any) []T {
	typed := make([]T, len(list))
	for i, item := range list {
		typed[i] = item.(T)
	}
	return typed
}

// contextOf is the adapter's context a reference names: a live span of the process, or what the adapter extracts from the
// reference's carrier. nil when the reference names none (the span's carrier is no longer kept).
func (t *nodeTelemetry) contextOf(reference *contextReference) telemetry.TelemetryContext {
	if reference == nil {
		return nil
	}
	if reference.Carrier != nil {
		return t.extract(reference.Carrier)
	}
	t.mu.Lock()
	live := t.live[reference.Span]
	own, kept := t.own[reference.Span]
	t.mu.Unlock()
	switch {
	case live != nil && live.span != nil:
		return live.span
	case kept && t.propagation != nil:
		return t.propagation.Extract(own)
	}
	return nil
}

// replay runs one `telemetry` event on the adapter.
func (t *nodeTelemetry) replay(payload []byte) {
	var event replayEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return
	}
	switch event.Op {
	case "start":
		t.start(event)
	case "end":
		t.end(event)
	case "attributes", "event", "link", "status":
		t.update(event)
	case "metric":
		t.measure(event)
	}
}

func (t *nodeTelemetry) start(event replayEvent) {
	parent := t.adapter
	if event.Parent != nil {
		if resolved := t.contextOf(event.Parent); resolved != nil {
			parent = resolved
		}
	}
	options := telemetry.SpanOptions{Name: event.Name, Attributes: attributesOf(event.Attributes)}
	for _, link := range event.Links {
		if context := t.contextOf(&link.Context); context != nil {
			options.Links = append(options.Links, telemetry.SpanLink{Context: context, Attributes: attributesOf(link.Attributes)})
		}
	}
	span := &replayedSpan{started: make(chan struct{}), end: make(chan spanEnd, 1), finished: make(chan struct{})}
	//portlint:allow golifetime ends when the span's `end` event arrives, or the process's end (close)
	go span.run(parent, options)
	<-span.started
	if span.span == nil {
		return
	}
	var own telemetry.TelemetryCarrier
	if t.propagation != nil {
		own = t.propagation.Inject(span.span)
	}
	t.mu.Lock()
	t.live[event.Span] = span
	if own != nil {
		t.own[event.Span] = own
		t.order = append(t.order, event.Span)
		if len(t.order) > carrierMemory {
			delete(t.own, t.order[0])
			t.order = t.order[1:]
		}
	}
	t.mu.Unlock()
}

func (t *nodeTelemetry) end(event replayEvent) {
	t.mu.Lock()
	span := t.live[event.Span]
	delete(t.live, event.Span)
	t.mu.Unlock()
	if span == nil {
		return
	}
	ended := spanEnd{}
	if event.Error != nil {
		ended.failed = true
		if event.Error.Name == nil {
			ended.bare = true
		} else {
			ended.name, ended.message = *event.Error.Name, event.Error.Message
		}
	}
	span.end <- ended
	<-span.finished
}

func (t *nodeTelemetry) update(event replayEvent) {
	t.mu.Lock()
	live := t.live[event.Span]
	t.mu.Unlock()
	if live == nil || live.span == nil {
		return
	}
	switch event.Op {
	case "attributes":
		live.span.SetAttributes(attributesOf(event.Attributes))
	case "event":
		live.span.AddEvent(event.Name, attributesOf(event.Attributes))
	case "link":
		if context := t.contextOf(event.Context); context != nil {
			live.span.AddLink(telemetry.SpanLink{Context: context, Attributes: attributesOf(event.Attributes)})
		}
	case "status":
		status := telemetry.SpanStatus{Status: telemetry.SpanStatusCode(event.Status.Status)}
		if event.Status.Error != nil {
			status.Error = &telemetry.SpanStatusError{Name: event.Status.Error.Name, Message: event.Status.Error.Message}
		}
		live.span.SetStatus(status)
	}
}

// measure records one measurement on the adapter's instrument of its kind, name and options.
func (t *nodeTelemetry) measure(event replayEvent) {
	if t.metrics == nil {
		return
	}
	options := telemetry.InstrumentOptions{Description: event.Description, Unit: event.Unit}
	key := event.Kind + "\x00" + event.Name
	if event.Description != nil {
		key += "\x00d" + *event.Description
	}
	if event.Unit != nil {
		key += "\x00u" + *event.Unit
	}
	if event.Boundaries != nil {
		key += "\x00b" + strings.Join(floatsText(event.Boundaries), ",")
	}
	t.mu.Lock()
	instrument, ok := t.instruments[key]
	t.mu.Unlock()
	if !ok {
		switch event.Kind {
		case "counter":
			instrument = t.metrics.Counter(event.Name, options)
		case "upDownCounter":
			instrument = t.metrics.UpDownCounter(event.Name, options)
		case "histogram":
			instrument = t.metrics.Histogram(event.Name, telemetry.HistogramOptions{InstrumentOptions: options, Boundaries: event.Boundaries})
		default:
			return
		}
		t.mu.Lock()
		t.instruments[key] = instrument
		t.mu.Unlock()
	}
	attributes := attributesOf(event.Attributes)
	switch typed := instrument.(type) {
	case telemetry.TelemetryHistogram:
		typed.Record(event.Value, attributes, t.contextOf(event.Context))
	case telemetry.TelemetryCounter:
		typed.Add(event.Value, attributes)
	}
}

func floatsText(values []float64) []string {
	text := make([]string, len(values))
	for i, value := range values {
		text[i] = strconv.FormatFloat(value, 'g', -1, 64)
	}
	return text
}
