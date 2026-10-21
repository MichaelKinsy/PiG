package telemetry_test

// Ports packages/telemetry/test/propagation.test.ts.
//
// TypeScript's concurrent Promises are goroutines the test sequences with
// channels in the order the JavaScript microtasks run, and the worker is a re-executed test binary (see
// propagation_worker_test.go).

import (
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry"
)

func spanNamed(t *testing.T, spans []telemetry.RecordedTelemetrySpan, name string) telemetry.RecordedTelemetrySpan {
	t.Helper()
	for _, span := range spans {
		if span.Name == name {
			return span
		}
	}
	t.Fatalf("no recorded span %s", name)
	return telemetry.RecordedTelemetrySpan{}
}

// TestPropagationUpstream ports packages/telemetry/test/propagation.test.ts. Each subtest names one upstream case.
// Pi source: packages/telemetry/src/memory.ts, packages/telemetry/src/noop.ts
func TestPropagationUpstream(t *testing.T) {
	t.Run("propagation and links › continues a parent in another Node process without shared recorder state", func(t *testing.T) {
		// upstream: packages/telemetry/test/propagation.test.ts:23
		caller := telemetry.NewInMemoryTelemetryContext()
		var carrier telemetry.TelemetryCarrier
		if err := caller.StartSpan(telemetry.SpanOptions{Name: "caller"}, func(span telemetry.TelemetrySpan) error {
			carrier = caller.Inject(span)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(carrier)
		if err != nil {
			t.Fatal(err)
		}
		worker := exec.CommandContext(t.Context(), os.Args[0], string(encoded))
		worker.Env = append(os.Environ(), propagationWorkerEnv+"=1")
		output, err := worker.Output()
		if err != nil {
			t.Fatalf("worker: %v", err)
		}
		var recorded []recordedSpanJSON
		if err := json.Unmarshal(output, &recorded); err != nil {
			t.Fatal(err)
		}
		if len(recorded) != 1 {
			t.Fatalf("the worker recorded %d spans, want 1", len(recorded))
		}
		if !reflect.DeepEqual(recorded[0].ParentContext, carrier) {
			t.Fatalf("parentContext = %v, want the injected carrier %v", recorded[0].ParentContext, carrier)
		}
		if recorded[0].ParentID != nil {
			t.Fatalf("parentId = %d, want null: the parent belongs to another process", *recorded[0].ParentID)
		}
		if reflect.DeepEqual(recorded[0].Context, carrier) {
			t.Fatal("the worker's span has the caller's context")
		}
	})

	t.Run("propagation and links › isolates crossed concurrent remote parents and links shared roots", func(t *testing.T) {
		// upstream: packages/telemetry/test/propagation.test.ts:41
		caller := telemetry.NewInMemoryTelemetryContext()
		provider := telemetry.NewInMemoryTelemetryContext()
		var activation telemetry.TelemetrySpan
		gate := make(chan struct{})
		activationStarted := make(chan struct{})
		activationDone := make(chan struct{})
		var startActivation sync.Once
		run := func(name string) error {
			return caller.StartSpan(telemetry.SpanOptions{Name: name}, func(trigger telemetry.TelemetrySpan) error {
				parent := provider.Extract(caller.Inject(trigger))
				return parent.StartSpan(telemetry.SpanOptions{Name: name + ".method"}, func(getter telemetry.TelemetrySpan) error {
					startActivation.Do(func() {
						go func() {
							defer close(activationDone)
							_ = provider.StartSpan(telemetry.SpanOptions{Name: "activation", Links: []telemetry.SpanLink{{Context: getter}}}, func(span telemetry.TelemetrySpan) error {
								activation = span
								close(activationStarted)
								<-gate
								return nil
							})
						}()
					})
					<-activationStarted
					getter.AddLink(telemetry.SpanLink{Context: activation})
					if name == "later" {
						close(gate)
					}
					<-activationDone
					return nil
				})
			})
		}
		// The first trigger starts the shared activation and awaits it; only then does the later trigger run, as the JavaScript
		// microtasks order them, and it releases the activation.
		results := make(chan error, 2)
		go func() { results <- run("first") }()
		<-activationStarted
		go func() { results <- run("later") }()
		for range 2 {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		spans := provider.GetSpans()
		root := spanNamed(t, spans, "activation")
		if root.ParentContext != nil {
			t.Fatalf("activation.parentContext = %v, want null", root.ParentContext)
		}
		first, later := spanNamed(t, spans, "first.method"), spanNamed(t, spans, "later.method")
		callerSpans := caller.GetSpans()
		if !reflect.DeepEqual(first.ParentContext, callerSpans[0].Context) {
			t.Fatalf("first.method.parentContext = %v, want %v", first.ParentContext, callerSpans[0].Context)
		}
		if !reflect.DeepEqual(later.ParentContext, callerSpans[1].Context) {
			t.Fatalf("later.method.parentContext = %v, want %v", later.ParentContext, callerSpans[1].Context)
		}
		if len(root.Links) == 0 || !reflect.DeepEqual(root.Links[0].Context, first.Context) {
			t.Fatalf("activation.links = %v, want the first getter's context first", root.Links)
		}
		if len(later.Links) == 0 || !reflect.DeepEqual(later.Links[0].Context, root.Context) {
			t.Fatalf("later.method.links = %v, want the activation's context first", later.Links)
		}
	})

	t.Run("propagation and links › detaches carrier and link snapshots and retains settled target identities", func(t *testing.T) {
		// upstream: packages/telemetry/test/propagation.test.ts:78
		adapter := telemetry.NewInMemoryTelemetryContext()
		var target telemetry.TelemetrySpan
		if err := adapter.StartSpan(telemetry.SpanOptions{Name: "target"}, func(span telemetry.TelemetrySpan) error {
			target = span
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if target == nil {
			t.Fatal("Expected target")
		}
		original := adapter.Inject(target)
		injected := adapter.Inject(target)
		if injected == nil {
			t.Fatal("Expected carrier")
		}
		injected["pi-memory-span"] = "999"
		if !reflect.DeepEqual(adapter.Inject(target), original) {
			t.Fatal("mutating an injected carrier changed the span's identity")
		}
		if err := adapter.StartSpan(telemetry.SpanOptions{Name: "linked", Links: []telemetry.SpanLink{{Context: target, Attributes: telemetry.SpanAttributes{telemetry.Attr("tags", []string{"original"})}}}}, func(telemetry.TelemetrySpan) error { return nil }); err != nil {
			t.Fatal(err)
		}
		spans := adapter.GetSpans()
		first, linked := spans[0], spans[1]
		if len(linked.Links) != 1 {
			t.Fatalf("links = %v, want one", linked.Links)
		}
		first.Context["pi-memory-span"] = "123"
		linked.Links[0].Context["pi-memory-span"] = "456"
		linked.Links[0].Attributes.Value("tags").([]string)[0] = "mutated"
		linked.Links[0].Attributes.Set("tags", []string{"mutated"})
		if got := adapter.GetSpans()[1].Links; !reflect.DeepEqual(got, []telemetry.RecordedTelemetryLink{{Context: original, Attributes: telemetry.SpanAttributes{telemetry.Attr("tags", []string{"original"})}}}) {
			t.Fatalf("links = %v, want the original target and attributes", got)
		}
	})

	t.Run("propagation and links › links a new root to an extracted remote target", func(t *testing.T) {
		// upstream: packages/telemetry/test/propagation.test.ts:102
		sender := telemetry.NewInMemoryTelemetryContext()
		receiver := telemetry.NewInMemoryTelemetryContext()
		var carrier telemetry.TelemetryCarrier
		if err := sender.StartSpan(telemetry.SpanOptions{Name: "trigger"}, func(span telemetry.TelemetrySpan) error {
			carrier = sender.Inject(span)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(carrier)
		if err != nil {
			t.Fatal(err)
		}
		var wire telemetry.TelemetryCarrier
		if err := json.Unmarshal(encoded, &wire); err != nil {
			t.Fatal(err)
		}
		target := receiver.Extract(wire)
		if err := receiver.StartSpan(telemetry.SpanOptions{Name: "background", Links: []telemetry.SpanLink{{Context: target, Attributes: telemetry.SpanAttributes{telemetry.Attr("reason", "restart")}}}}, func(span telemetry.TelemetrySpan) error {
			span.AddLink(telemetry.SpanLink{Context: target})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		recorded := receiver.GetSpans()[0]
		if recorded.ParentContext != nil {
			t.Fatalf("parentContext = %v, want null", recorded.ParentContext)
		}
		if want := []telemetry.RecordedTelemetryLink{
			{Context: carrier, Attributes: telemetry.SpanAttributes{telemetry.Attr("reason", "restart")}},
			{Context: carrier, Attributes: telemetry.SpanAttributes{}},
		}; !reflect.DeepEqual(recorded.Links, want) {
			t.Fatalf("links = %v, want %v", recorded.Links, want)
		}
	})

	t.Run("propagation and links › uses the root for absent and unknown carriers", func(t *testing.T) {
		// upstream: packages/telemetry/test/propagation.test.ts:116
		adapter := telemetry.NewInMemoryTelemetryContext()
		for _, carrier := range []telemetry.TelemetryCarrier{nil, {}, {"unknown": "format"}} {
			if got := adapter.Extract(carrier); got != telemetry.TelemetryContext(adapter) {
				t.Fatalf("Extract(%v) = %v, want the recorder root", carrier, got)
			}
		}
		if carrier := telemetry.NoopTelemetryPropagation.Inject(telemetry.NoopTelemetryContext); carrier != nil {
			t.Fatalf("no-op Inject returned %v, want undefined", carrier)
		}
		if got := telemetry.NoopTelemetryPropagation.Extract(telemetry.TelemetryCarrier{"unknown": "format"}); got != telemetry.TelemetryContext(telemetry.NoopTelemetryContext) {
			t.Fatalf("no-op Extract returned %v, want the no-op context", got)
		}
		if err := telemetry.NoopTelemetryContext.StartSpan(telemetry.SpanOptions{Name: "noop"}, func(span telemetry.TelemetrySpan) error {
			span.AddLink(telemetry.SpanLink{Context: span})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("propagation and links › treats recognized carrier identifiers as opaque strings", func(t *testing.T) {
		// upstream: packages/telemetry/test/propagation.test.ts:130
		adapter := telemetry.NewInMemoryTelemetryContext()
		carrier := telemetry.TelemetryCarrier{
			"pi-memory-scope": "external-recorder",
			"pi-memory-span":  "external-span",
			"pi-memory-trace": "external-trace",
		}
		if err := adapter.Extract(carrier).StartSpan(telemetry.SpanOptions{Name: "receiver"}, func(telemetry.TelemetrySpan) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if got := adapter.GetSpans()[0].ParentContext; !reflect.DeepEqual(got, carrier) {
			t.Fatalf("parentContext = %v, want %v", got, carrier)
		}
	})
}

// A carrier is accepted when it holds all three identity keys, whatever other keys it holds, and its identifiers are opaque. An incomplete carrier falls back to the recorder
// root, and an accepted one whose scope and span belong to this recorder reports the local parent ID. Local span IDs are the decimal span counter.
// upstream: packages/telemetry/src/memory.ts extract
func TestExtractAcceptsCompleteMemoryCarriers(t *testing.T) {
	adapter := telemetry.NewInMemoryTelemetryContext()
	var own telemetry.TelemetryCarrier
	if err := adapter.StartSpan(telemetry.SpanOptions{Name: "own"}, func(span telemetry.TelemetrySpan) error {
		own = adapter.Inject(span)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	const foreign = "0a0b0c0d-0e0f-4a0b-8c0d-0e0f0a0b0c0d"
	complete := telemetry.TelemetryCarrier{"pi-memory-scope": foreign, "pi-memory-span": "1", "pi-memory-trace": "trace"}
	without := func(key string) telemetry.TelemetryCarrier {
		carrier := telemetry.TelemetryCarrier{}
		maps.Copy(carrier, complete)
		delete(carrier, key)
		return carrier
	}
	withExtra := telemetry.TelemetryCarrier{"extra": "key"}
	maps.Copy(withExtra, own)
	for _, test := range []struct {
		name      string
		carrier   telemetry.TelemetryCarrier
		accepted  bool
		localSpan bool
	}{
		{"nil", nil, false, false},
		{"empty", telemetry.TelemetryCarrier{}, false, false},
		{"missing scope", without("pi-memory-scope"), false, false},
		{"missing span", without("pi-memory-span"), false, false},
		{"missing trace", without("pi-memory-trace"), false, false},
		{"foreign recorder", complete, true, false},
		{"external identifiers", telemetry.TelemetryCarrier{"pi-memory-scope": "s", "pi-memory-span": "x", "pi-memory-trace": "t"}, true, false},
		{"own span", own, true, true},
		{"own span with extra keys", withExtra, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			parent := adapter.Extract(test.carrier)
			if accepted := parent != telemetry.TelemetryContext(adapter); accepted != test.accepted {
				t.Fatalf("accepted = %v, want %v", accepted, test.accepted)
			}
			before := len(adapter.GetSpans())
			if err := parent.StartSpan(telemetry.SpanOptions{Name: "child"}, func(telemetry.TelemetrySpan) error { return nil }); err != nil {
				t.Fatal(err)
			}
			child := adapter.GetSpans()[before]
			switch {
			case !test.accepted:
				if child.ParentContext != nil || child.ParentID != nil {
					t.Fatalf("child = %+v, want a root", child)
				}
			case test.localSpan:
				if child.ParentID == nil || *child.ParentID != 1 || !reflect.DeepEqual(child.ParentContext, own) {
					t.Fatalf("child = %+v, want local parent 1 with context %v", child, own)
				}
			default:
				want := telemetry.TelemetryCarrier{"pi-memory-scope": test.carrier["pi-memory-scope"], "pi-memory-span": test.carrier["pi-memory-span"], "pi-memory-trace": test.carrier["pi-memory-trace"]}
				if child.ParentID != nil || !reflect.DeepEqual(child.ParentContext, want) {
					t.Fatalf("child = %+v, want a remote parent %v", child, want)
				}
			}
		})
	}
}
