package host

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	chordtelemetry "github.com/MichaelKinsy/PiG/chord/next/telemetry"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// pi: packages/chord/test/telemetry.test.ts

// Ports packages/chord/test/telemetry.test.ts: "across hosts", "re-holds", "relayed re-holds", "what a peer sends" and the two
// `relayed` cases of "the schema" and "nothing recorded". The pure telemetry cases are chord/next/telemetry's
// (telemetry_upstream_test.go).
//
// Forced Go differences. Pi's "across hosts" cases record to a SQLite telemetry store and read it back with `findTraces`;
// here the hosts record to the in-memory adapter, which keeps the same spans (names, attributes, events, links, parents) in
// start order: the store's own read-back is chord/telemetrysqlite's test. A span's link target is its carrier; a `Foreign`
// adapter is a wrapper whose propagation injects a carrier no other adapter reads.

// tracedUnder runs fn under a root span `name` of recorder.
func tracedUnder(t testing.TB, recorder *telemetry.InMemoryTelemetryContext, name string, fn func(ctx context.Context)) {
	t.Helper()
	must(t, recorder.StartSpan(telemetry.SpanOptions{Name: name}, func(span telemetry.TelemetrySpan) error {
		fn(chordctx.WithTelemetry(background, span))
		return nil
	}))
}

// spansNamed are the recorded spans called name, in start order.
func spansNamed(recorder *telemetry.InMemoryTelemetryContext, name string) []telemetry.RecordedTelemetrySpan {
	var found []telemetry.RecordedTelemetrySpan
	for _, span := range recorder.GetSpans() {
		if span.Name == name {
			found = append(found, span)
		}
	}
	return found
}

// spanWhere is the first recorded span that is called name and has every attribute of want.
func spanWhere(t testing.TB, recorder *telemetry.InMemoryTelemetryContext, name string, want map[string]any) telemetry.RecordedTelemetrySpan {
	t.Helper()
	for _, span := range recorder.GetSpans() {
		if span.Name == name && hasAttributes(span, want) {
			return span
		}
	}
	t.Fatalf("no span %s with %v among %v", name, want, spanNames(recorder))
	return telemetry.RecordedTelemetrySpan{}
}

func spanNames(recorder *telemetry.InMemoryTelemetryContext) []string {
	var names []string
	for _, span := range recorder.GetSpans() {
		names = append(names, span.Name)
	}
	return names
}

func hasAttributes(span telemetry.RecordedTelemetrySpan, want map[string]any) bool {
	for name, value := range want {
		if got, ok := span.Attributes.Get(name); !ok || got != value {
			return false
		}
	}
	return true
}

// attributesWant fails the test unless span has every attribute of want.
func attributesWant(t testing.TB, span telemetry.RecordedTelemetrySpan, want map[string]any) {
	t.Helper()
	for name, value := range want {
		if got, ok := span.Attributes.Get(name); !ok || got != value {
			t.Errorf("%s: %s = %v (present %v), want %v", span.Name, name, got, ok, value)
		}
	}
}

// linksOf are the carriers a span links to.
func linksOf(span telemetry.RecordedTelemetrySpan) []telemetry.TelemetryCarrier {
	var carriers []telemetry.TelemetryCarrier
	for _, each := range span.Links {
		carriers = append(carriers, each.Context)
	}
	return carriers
}

// linksTo reports whether span's links are exactly spans' carriers, in order.
func linksTo(span telemetry.RecordedTelemetrySpan, targets ...telemetry.RecordedTelemetrySpan) bool {
	links := linksOf(span)
	if len(links) != len(targets) {
		return false
	}
	for index, target := range targets {
		if !maps.Equal(links[index], target.Context) {
			return false
		}
	}
	return true
}

// childOf reports whether span is a child of parent (a local parent).
func childOf(span, parent telemetry.RecordedTelemetrySpan) bool {
	return span.ParentID != nil && *span.ParentID == parent.ID
}

// placedEditor is the tab's reference to the editor of PlacedNotes for session S1, got under ctx.
func placedEditor(t testing.TB, rel *relayed, ctx context.Context) *Reference {
	t.Helper()
	notes, err := rel.Tab.Get(ctx, PlacedNotes, jsonObject("session", "S1"), GetOptions{})
	must(t, err)
	if variant, _, _ := notes.Variant(); variant != "editor" {
		t.Fatalf("variant = %q, want the editor", variant)
	}
	return notes
}

func noteAdded(t testing.TB, notes *Reference, text string, ctx context.Context) {
	t.Helper()
	if _, err := notes.Call("add", jsonObject("text", text), ctx); err != nil {
		t.Fatal(err)
	}
}

// allRecord is the options of `relayed` with every host recording to recorder.
func allRecord(recorder telemetry.TelemetryContext) relayedOptions {
	var options relayedOptions
	options.Telemetry.Tab, options.Telemetry.Server, options.Telemetry.Worker = recorder, recorder, recorder
	return options
}

func TestTelemetrySchemaDeclaresEveryMetricATracedCallOverARelayRecords(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	rel := relay(t, allRecord(recorder))
	tracedUnder(t, recorder, "test", func(ctx context.Context) {
		notes := placedEditor(t, rel, ctx)
		noteAdded(t, notes, "a", ctx)
	})
	for _, measurement := range recorder.GetMetrics() {
		if _, ok := chordtelemetry.Schema.Metrics[measurement.Name]; !ok {
			t.Errorf("recorded metric %s is not declared", measurement.Name)
		}
	}
	for _, name := range spanNames(recorder) {
		if _, ok := chordtelemetry.Schema.Spans[name]; !ok && name != "test" {
			t.Errorf("recorded span %s is not declared", name)
		}
	}
}

// carrierSpy is a codec that notes the messages it encodes with a trace carrier.
type carrierSpy struct {
	link.Codec
	mu      sync.Mutex
	sent    int
	carried int
}

func (c *carrierSpy) Encode(message link.Message) ([]byte, error) {
	c.mu.Lock()
	c.sent++
	switch typed := message.(type) {
	case *link.HoldMessage:
		if typed.Trace != nil {
			c.carried++
		}
	case *link.CallMessage:
		if typed.Trace != nil {
			c.carried++
		}
	}
	c.mu.Unlock()
	return c.Codec.Encode(message)
}

func TestNothingRecordedRecordsNothingAndSendsNoCarrierWhenNoHostHasAnAdapter(t *testing.T) {
	spy := &carrierSpy{Codec: testCodec()}
	options := relayedOptions{TabCodec: spy}
	rel := relay(t, options)
	notes := placedEditor(t, rel, background)
	noteAdded(t, notes, "a", background)
	spy.mu.Lock()
	defer spy.mu.Unlock()
	if spy.sent == 0 || spy.carried != 0 {
		t.Fatalf("sent %d messages, %d with a carrier; want some, none carried", spy.sent, spy.carried)
	}
}

func TestAcrossHostsFollowsACallFromTheTabThroughTheServersRelayIntoTheWorkersMethodAndItsCommit(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	rel := relay(t, allRecord(recorder))
	tracedUnder(t, recorder, "test", func(ctx context.Context) {
		notes := placedEditor(t, rel, ctx)
		noteAdded(t, notes, "a", ctx)
	})
	flushOwn(rel)
	spans := recorder.GetSpans()
	call := spanWhere(t, recorder, "chord.call", map[string]any{"type": "app.placed-notes", "member": "add", "address.kind": "session"})
	var relayCall telemetry.RecordedTelemetrySpan
	for _, span := range spans {
		if member, _ := span.Attributes.Get("member"); span.Name == "chord.relay" && member != "hold" {
			relayCall = span
		}
	}
	if !childOf(relayCall, call) {
		t.Fatalf("the relay's call span is not a child of chord.call")
	}
	method := spanWhere(t, recorder, "chord.method", map[string]any{"member": "add", "type": "app.placed-notes"})
	if !childOf(method, relayCall) {
		t.Errorf("the worker's method is not a child of the relay")
	}
	attributesWant(t, method, map[string]any{"principal.kind": "owner", "outcome": "ok"})
	local := spanWhere(t, recorder, "chord.method", map[string]any{"type": "app.note-store", "member": "add"})
	if !childOf(local, method) {
		t.Errorf("the note store's method is not a child of the worker's")
	}
	var commits [][]any
	for _, event := range local.Events {
		documents, _ := event.Attributes.Get("documents")
		ops, _ := event.Attributes.Get("ops")
		commits = append(commits, []any{event.Name, documents, ops})
	}
	if len(commits) != 1 || commits[0][0] != "chord.commit" || commits[0][1] != float64(1) || commits[0][2] != float64(1) {
		t.Errorf("the store's method events = %v, want one chord.commit of 1 document and 1 op", commits)
	}
	var shown []string
	for _, span := range spans {
		shown = append(shown, span.Name)
	}
	if !strings.Contains(strings.Join(shown, " "), "chord.call chord.relay chord.method chord.method") {
		t.Errorf("spans in start order = %v", shown)
	}
}

func TestAcrossHostsNamesTheHostThatRecordedEachSpanItsAddressKeyClientWithoutAnAddress(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	rel := relay(t, allRecord(recorder))
	tracedUnder(t, recorder, "test", func(ctx context.Context) {
		notes := placedEditor(t, rel, ctx)
		noteAdded(t, notes, "a", ctx)
	})
	flushOwn(rel)
	hosts := map[string]map[string]bool{}
	for _, span := range recorder.GetSpans() {
		if span.Name == "test" {
			continue
		}
		host, _ := span.Attributes.Get("chord.host")
		if hosts[span.Name] == nil {
			hosts[span.Name] = map[string]bool{}
		}
		hosts[span.Name][host.(string)] = true
	}
	want := map[string][]string{
		"chord.activate": {"session|S1"}, "chord.call": {"client"}, "chord.hold": {"client"},
		"chord.method": {"session|S1"}, "chord.relay": {"server"}, "chord.resolve": {"server"},
	}
	got := map[string][]string{}
	for name, of := range hosts {
		got[name] = slices.Sorted(maps.Keys(of))
	}
	if len(got) != len(want) {
		t.Fatalf("hosts per span = %v, want %v", got, want)
	}
	for name, hosts := range want {
		if !slices.Equal(got[name], hosts) {
			t.Errorf("%s recorded by %v, want %v", name, got[name], hosts)
		}
	}
}

func TestAcrossHostsHoldsThroughTheRelayUnderTheTabsSpanTheWorkersStartAndItsActivationsLinkToTheirTriggers(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	rel := relay(t, allRecord(recorder))
	tracedUnder(t, recorder, "test", func(ctx context.Context) { placedEditor(t, rel, ctx) })
	flushOwn(rel)
	hold := spanWhere(t, recorder, "chord.hold", nil)
	attributesWant(t, hold, map[string]any{"type": "app.placed-notes", "outcome": "bound"})
	var relayHold telemetry.RecordedTelemetrySpan
	for _, span := range spansNamed(recorder, "chord.relay") {
		if childOf(span, hold) {
			relayHold = span
		}
	}
	attributesWant(t, relayHold, map[string]any{"member": "hold", "upstream.kind": "session", "outcome": "ok"})
	resolve := spanWhere(t, recorder, "chord.resolve", nil)
	if resolve.ParentID != nil {
		t.Errorf("chord.resolve has a parent")
	}
	attributesWant(t, resolve, map[string]any{"kind": "session", "address": "session|S1", "outcome": "linked"})
	if !linksTo(resolve, relayHold) {
		t.Errorf("chord.resolve links %v, want the relay's hold span", linksOf(resolve))
	}
	var placed []telemetry.RecordedTelemetrySpan
	for _, span := range spansNamed(recorder, "chord.activate") {
		if kind, _ := span.Attributes.Get("type"); kind == "app.placed-notes" {
			placed = append(placed, span)
		}
	}
	if len(placed) < 2 {
		t.Fatalf("activations of the placed notes = %d, want the parked one and its resumption", len(placed))
	}
	activation, resumed := placed[0], placed[1]
	if activation.ParentID != nil {
		t.Errorf("the activation has a parent")
	}
	// Linked to its trigger, and (waiting for it) to the activation of the store it declared: it parked on it, and its own
	// steps ran in a span linked to the parked one.
	noteStore := spanWhere(t, recorder, "chord.activate", map[string]any{"type": "app.note-store"})
	if !linksTo(noteStore, activation) {
		t.Errorf("the note store's activation links %v, want the placed notes'", linksOf(noteStore))
	}
	if !linksTo(activation, relayHold, noteStore) {
		t.Errorf("the activation links %v; the relay span is %v, the store's activation %v", linksOf(activation), relayHold.Context, noteStore.Context)
	}
	attributesWant(t, activation, map[string]any{"outcome": "parked"})
	if len(activation.Events) != 1 || activation.Events[0].Name != "chord.park" {
		t.Fatalf("the activation's events = %v, want one chord.park", activation.Events)
	}
	if cause, _ := activation.Events[0].Attributes.Get("cause"); cause != "loading" {
		t.Errorf("park cause = %v, want loading", cause)
	}
	if !linksTo(resumed, activation) {
		t.Errorf("the resumed activation links %v, want the parked one", linksOf(resumed))
	}
	attributesWant(t, resumed, map[string]any{"attempt": float64(1), "outcome": "live"})
}

func TestAcrossHostsCostsACallOverARelayExactlyItsSpansAndNoExtraEncode(t *testing.T) {
	// oneCall is one call's spans and encodes, after the reference is live, with hosts recording to recorder or not.
	oneCall := func(recorder *telemetry.InMemoryTelemetryContext) (spans []string, encodes int) {
		_, ctx, counts := meteredHost(t)
		options := relayedOptions{Context: ctx}
		if recorder != nil {
			options = allRecord(recorder)
			options.Context = ctx
		}
		rel := relay(t, options)
		// A commit inside a recorded method counts through the method's span: both adapters are summed.
		encoded := func() int {
			total := int(counts()["chord.link.encodes"])
			if recorder != nil {
				for _, measurement := range recorder.GetMetrics() {
					if measurement.Name == "chord.link.encodes" {
						total++
					}
				}
			}
			return total
		}
		call := func(caller context.Context) {
			notes := placedEditor(t, rel, caller)
			noteAdded(t, notes, "warm", caller)
			flushOwn(rel)
			before, encodedBefore := 0, encoded()
			if recorder != nil {
				before = len(recorder.GetSpans())
			}
			noteAdded(t, notes, "a", caller)
			flushOwn(rel)
			if recorder != nil {
				for _, span := range recorder.GetSpans()[before:] {
					kind, _ := span.Attributes.Get("type")
					spans = append(spans, span.Name+" "+kind.(string))
				}
			}
			encodes = encoded() - encodedBefore
		}
		if recorder == nil {
			call(ctx)
		} else {
			tracedUnder(t, recorder, "test", call)
		}
		return spans, encodes
	}
	_, plain := oneCall(nil)
	spans, recorded := oneCall(telemetry.NewInMemoryTelemetryContext())
	want := []string{"chord.call app.placed-notes", "chord.relay app.placed-notes", "chord.method app.placed-notes", "chord.method app.note-store"}
	if !slices.Equal(spans, want) {
		t.Errorf("spans of one call = %v, want %v", spans, want)
	}
	if recorded != plain {
		t.Errorf("encodes with recording = %d, without = %d: recording must add none", recorded, plain)
	}
}

// foreignPropagation injects a carrier no other adapter reads.
type foreignPropagation struct{ telemetry.TelemetryPropagation }

func (foreignPropagation) Inject(telemetry.TelemetryContext) telemetry.TelemetryCarrier {
	return telemetry.TelemetryCarrier{"unknown": "carrier"}
}

// foreignContext is an adapter whose carriers no other adapter reads: its spans (and theirs) inject `{ unknown: carrier }`.
type foreignContext struct{ telemetry.TelemetryContext }

func (f foreignContext) Propagation() telemetry.TelemetryPropagation {
	return foreignPropagation{f.TelemetryContext.Propagation()}
}

func (f foreignContext) StartSpan(options telemetry.SpanOptions, callback func(telemetry.TelemetrySpan) error) error {
	return f.TelemetryContext.StartSpan(options, func(span telemetry.TelemetrySpan) error { return callback(foreignSpan{span}) })
}

type foreignSpan struct{ telemetry.TelemetrySpan }

func (f foreignSpan) Propagation() telemetry.TelemetryPropagation {
	return foreignPropagation{f.TelemetrySpan.Propagation()}
}

func (f foreignSpan) StartSpan(options telemetry.SpanOptions, callback func(telemetry.TelemetrySpan) error) error {
	return f.TelemetrySpan.StartSpan(options, func(span telemetry.TelemetrySpan) error { return callback(foreignSpan{span}) })
}

func TestAcrossHostsStartsANewRootForAWellFormedCarrierTheServersAdapterDoesNotKnow(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	options := relayedOptions{}
	options.Telemetry.Tab = foreignContext{telemetry.NewInMemoryTelemetryContext()}
	options.Telemetry.Server, options.Telemetry.Worker = recorder, recorder
	rel := relay(t, options)
	tabRecorder := foreignContext{telemetry.NewInMemoryTelemetryContext()}
	must(t, tabRecorder.StartSpan(telemetry.SpanOptions{Name: "test"}, func(span telemetry.TelemetrySpan) error {
		ctx := chordctx.WithTelemetry(background, span)
		notes := placedEditor(t, rel, ctx)
		noteAdded(t, notes, "a", ctx)
		return nil
	}))
	flushOwn(rel)
	relays := spansNamed(recorder, "chord.relay")
	if len(relays) != 2 {
		t.Fatalf("relay spans = %d, want 2", len(relays))
	}
	for _, span := range relays {
		if span.ParentID != nil || span.ParentContext != nil {
			t.Errorf("a relay span of an unknown carrier has parent %v / %v, want a root", span.ParentID, span.ParentContext)
		}
	}
	// The worker's method continues the relay's new trace.
	var call telemetry.RecordedTelemetrySpan
	for _, span := range relays {
		if member, _ := span.Attributes.Get("member"); member != "hold" {
			call = span
		}
	}
	method := spanWhere(t, recorder, "chord.method", map[string]any{"member": "add", "type": "app.placed-notes"})
	if !childOf(method, call) {
		t.Errorf("the worker's method is not a child of the relay's call span")
	}
}

func TestReHoldsCarryTheCarrierOnlyOnAReferencesFirstHoldAReHoldAfterAReconnectIsAFreshRootLinkedToIt(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	rel := relay(t, allRecord(recorder))
	var notes *Reference
	tracedUnder(t, recorder, "page load", func(ctx context.Context) { notes = placedEditor(t, rel, ctx) })
	flushOwn(rel)
	var current link.Link = rel.TabLink
	var pair []*link.MemoryLink
	for range 3 {
		current.Close()
		// The close reaches the tab's end too before it connects again (Pi's `await transported()` flushes every memory link).
		transported(pair...)
		flushOwn(rel)
		a, b := link.MemoryLinkPair()
		current, pair = a, []*link.MemoryLink{a, b}
		must(t, rel.Server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
		must(t, rel.Tab.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
		for range 5 {
			transported(a, b)
			flushOwn(rel)
		}
	}
	load := spanWhere(t, recorder, "page load", nil)
	holds := spansNamed(recorder, "chord.hold")
	if len(holds) != 4 {
		t.Fatalf("holds = %d, want 4 (the first and three re-holds)", len(holds))
	}
	if !childOf(holds[0], load) {
		t.Errorf("the first hold is not a child of the page load")
	}
	for _, hold := range holds[1:] {
		if hold.ParentID != nil {
			t.Errorf("a re-hold has a parent: a re-hold is a fresh root")
		}
		if !linksTo(hold, holds[0]) {
			t.Errorf("a re-hold links %v, want the first hold", linksOf(hold))
		}
		attributesWant(t, hold, map[string]any{"outcome": "bound"})
	}
	var relayParents []int
	for _, span := range spansNamed(recorder, "chord.relay") {
		if member, _ := span.Attributes.Get("member"); member == "hold" {
			if span.ParentID == nil {
				t.Fatalf("a relayed hold is a root")
			}
			relayParents = append(relayParents, *span.ParentID)
		}
	}
	var holdIDs []int
	for _, hold := range holds {
		holdIDs = append(holdIDs, hold.ID)
	}
	if !slices.Equal(relayParents, holdIDs) {
		t.Errorf("the relays of the holds are children of %v, want the holds %v", relayParents, holdIDs)
	}
	notes.Release()
}

func TestRelayedReHoldsReHoldUpstreamAfterADropInAFreshRelayRootLinkedToTheFirstRelaySpan(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	rel := relay(t, allRecord(recorder))
	var notes *Reference
	tracedUnder(t, recorder, "page load", func(ctx context.Context) { notes = placedEditor(t, rel, ctx) })
	flushOwn(rel)
	links := rel.RouteLinks()
	links[0].Close()
	for range 8 {
		flushOwn(rel)
	}
	if got := len(rel.RouteLinks()); got != 2 {
		t.Fatalf("route links = %d, want 2", got)
	}
	hold := spanWhere(t, recorder, "chord.hold", nil)
	var relays []telemetry.RecordedTelemetrySpan
	for _, span := range spansNamed(recorder, "chord.relay") {
		if member, _ := span.Attributes.Get("member"); member == "hold" {
			relays = append(relays, span)
		}
	}
	if len(relays) != 2 {
		t.Fatalf("relayed hold spans = %d, want 2", len(relays))
	}
	if !childOf(relays[0], hold) || relays[1].ParentID != nil {
		t.Errorf("relay parents = %v, %v; want the hold, then none", relays[0].ParentID, relays[1].ParentID)
	}
	if !linksTo(relays[1], relays[0]) {
		t.Errorf("the re-hold's relay links %v, want the first relay span", linksOf(relays[1]))
	}
	notes.Release()
}

// hostile is a server routing Session (recording to recorder) and a raw guest peer on an untrusted link to it.
func hostile(t testing.TB) (*telemetry.InMemoryTelemetryContext, *RawPeer, *relayed) {
	t.Helper()
	recorder := telemetry.NewInMemoryTelemetryContext()
	var options relayedOptions
	options.Telemetry.Server, options.Telemetry.Worker = recorder, recorder
	rel := relay(t, options)
	peer := rawTab(t, rel.Server, ownerPrincipal(), link.PartLimits{})
	peer.Next("hello")
	return recorder, peer, rel
}

func TestWhatAPeerSendsRefusesAMalformedHeaderTheLinkClosesNothingIsRecorded(t *testing.T) {
	long := strings.Repeat
	nineKeys := map[string]string{}
	for index := range 9 {
		nineKeys["k"+string(rune('0'+index))] = "v"
	}
	for _, each := range []struct {
		name     string
		override func(*link.HoldMessage)
	}{
		{"a 500,000-character type", func(h *link.HoldMessage) { h.Type = long("x", 500_000) }},
		{"a 257-character host kind", func(h *link.HoldMessage) { h.Address = link.WireAddress{Kind: long("k", 257), Params: jsonObject()} }},
		{"a carrier with a control character", func(h *link.HoldMessage) { h.Trace = map[string]string{"pi-memory-span": "\x1b]0;PWNED\x07"} }},
		{"a carrier key with a space", func(h *link.HoldMessage) { h.Trace = map[string]string{"a b": "c"} }},
		{"a carrier of 9 keys", func(h *link.HoldMessage) { h.Trace = nineKeys }},
		{"a carrier value of 257 characters", func(h *link.HoldMessage) { h.Trace = map[string]string{"k": long("v", 257)} }},
	} {
		t.Run(each.name, func(t *testing.T) {
			recorder, peer, rel := hostile(t)
			hold := placedHold(t, 0, "S1")
			each.override(hold)
			peer.Send(hold)
			transported()
			flushOwn(rel)
			for i := 0; i < 50 && !peer.Closed(); i++ {
				transported(peer.link.(*link.MemoryLink))
				flushOwn(rel)
			}
			if !peer.Closed() {
				t.Fatalf("the link stayed open: peer got %v, server errors %v", peer.Kinds(), rel.Errors.Server.All())
			}
			protocolError := false
			for _, err := range rel.Errors.Server.All() {
				protocolError = protocolError || err.Kind == ErrProtocolError
			}
			if !protocolError {
				t.Errorf("the server reported no protocol_error")
			}
			if relays := spansNamed(recorder, "chord.relay"); len(relays) != 0 {
				t.Errorf("recorded %d relay spans for a malformed header", len(relays))
			}
		})
	}
}

func TestWhatAPeerSendsCapsAnObjectsLabelItsClientChosenParamsKeyAt256CharactersInItsSpans(t *testing.T) {
	recorder, peer, rel := hostile(t)
	session := strings.Repeat("s", 10_000)
	hold := placedHold(t, 0, session)
	hold.Params = mustJSONPart(t, jsonObject("session", session))
	hold.Address = link.WireAddress{Kind: "session", Params: jsonObject("id", session)}
	peer.Send(hold)
	peer.Next("bound")
	peer.Send(&link.CallMessage{Call: 0, Hold: 0, Method: wireNames(t, PlacedNotes, "editor").Methods["add"], Args: mustJSONPart(t, jsonObject("text", "a"))})
	peer.Next("result")
	flushOwn(rel)
	labelled := map[string]bool{}
	for _, span := range recorder.GetSpans() {
		if object, ok := span.Attributes.Get("object"); ok {
			labelled[span.Name] = true
			if len(object.(string)) > 256 {
				t.Errorf("%s: object attribute is %d characters", span.Name, len(object.(string)))
			}
		}
	}
	if !labelled["chord.activate"] || !labelled["chord.method"] {
		t.Errorf("labelled spans = %v, want chord.activate and chord.method", labelled)
	}
	resolve := spanWhere(t, recorder, "chord.resolve", nil)
	if address, _ := resolve.Attributes.Get("address"); len(address.(string)) != 256 {
		t.Errorf("resolve address = %d characters, want 256", len(address.(string)))
	}
}

var _ = data.CodeAborted

// Pi provider.ts #call/#run passes the carrier's parent to attempt.call separately from the link's context (attempt.ts
// 876-884): a type served `untraced` gets no method span and its handler's ctx keeps the link's context, so nothing the
// handler does (ctx.trace, a local call to a traced type) is recorded under the caller's carrier.
var quietOverALink = mustContract(contracts.Remote("app.quiet", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
	Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{
		Methods: []contracts.MethodEntry{contracts.Meth("work", contracts.NewMethod())},
	})},
}))

func TestAnUntracedTypeOverALinkRecordsNothingUnderTheCallersCarrier(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	s := rawLinked(t, func(h FacetHost) error {
		return h.Serve(quietOverALink, func(_ *delta.JsonObject, _ data.Principal, _ Self) (contracts.Implementation, error) {
			return contracts.Implementation{Variant: "main", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{},
				Methods: map[string]contracts.MethodFunction{
					"work": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						return ctx.(Ctx).Trace("app.inner", nil, func(Ctx) (any, error) { return nil, nil })
					},
				},
			}}, nil
		}, ServeOptions{Untraced: true})
	}, rawLinkedOptions{Telemetry: recorder})
	provHold(t, s.Peer, 0, specOf(quietOverALink), jsonObject())
	s.Peer.Next("bound")
	var carrier telemetry.TelemetryCarrier
	must(t, recorder.StartSpan(telemetry.SpanOptions{Name: "caller"}, func(span telemetry.TelemetrySpan) error {
		carrier = recorder.Inject(span)
		return nil
	}))
	work := wireNames(t, quietOverALink, "main").Methods["work"]
	s.Peer.Send(&link.CallMessage{Call: 0, Hold: 0, Method: work, Trace: carrier})
	s.Peer.Next("result")
	if names := spanNames(recorder); slices.Contains(names, "app.inner") || slices.Contains(names, "chord.method") {
		t.Fatalf("spans = %v, want none but caller (Pi: the untraced handler's ctx is the link's, untraced)", names)
	}
}
