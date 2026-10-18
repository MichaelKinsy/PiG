package telemetry_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// pi: packages/telemetry/src/memory.ts

var scopePattern = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// outcomeSpec is how a span callback ends: it returns, throws an Error with a name and message, or throws a value that is not an Error.
type outcomeSpec struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Message string `json:"message"`
	Value   string `json:"value"`
}

// linkSpec is a span link: its target (a kept span, nothing, the shared no-op context or the recorder root) and attributes.
type linkSpec struct {
	Target string         `json:"target"`
	Idx    int            `json:"idx"`
	Attrs  map[string]any `json:"attrs,omitempty"`
}

// opSpec is one step of a span program: setAttributes, addEvent, setStatus, addLink, keeping the span, using a kept span (possibly after it settled), awaiting a child span, or
// awaiting a span under a parent extracted from a carrier.
type opSpec struct {
	Op      string          `json:"op"`
	Name    string          `json:"name"`
	Attrs   map[string]any  `json:"attrs,omitempty"`
	Status  json.RawMessage `json:"status,omitempty"`
	Idx     int             `json:"idx"`
	Sub     *opSpec         `json:"sub,omitempty"`
	Body    []opSpec        `json:"body"`
	Outcome *outcomeSpec    `json:"outcome,omitempty"`
	Target  string          `json:"target,omitempty"`
	Carrier string          `json:"carrier,omitempty"`
	Links   []linkSpec      `json:"links,omitempty"`
}

type rootSpec struct {
	Name    string         `json:"name"`
	Attrs   map[string]any `json:"attrs,omitempty"`
	Links   []linkSpec     `json:"links,omitempty"`
	Body    []opSpec       `json:"body"`
	Outcome outcomeSpec    `json:"outcome"`
}

// foreignScope is a valid scope no recorder owns.
const foreignScope = "0a0b0c0d-0e0f-4a0b-8c0d-0e0f0a0b0c0d"

// linkTarget is the context a link names: a kept span, nothing, the shared no-op context or the recorder root.
func linkTarget(recorder *telemetry.InMemoryTelemetryContext, kept []telemetry.TelemetrySpan, target string, idx int) telemetry.TelemetryContext {
	switch target {
	case "kept":
		if len(kept) > 0 {
			return kept[idx%len(kept)]
		}
	case "noop":
		return telemetry.NoopTelemetryContext
	case "recorder":
		return recorder
	}
	return nil
}

func linksOf(recorder *telemetry.InMemoryTelemetryContext, kept []telemetry.TelemetrySpan, links []linkSpec) []telemetry.SpanLink {
	var out []telemetry.SpanLink
	for _, link := range links {
		out = append(out, telemetry.SpanLink{Context: linkTarget(recorder, kept, link.Target, link.Idx), Attributes: attributesOf(link.Attrs)})
	}
	return out
}

// carrierOf is the carrier a remote step extracts: the injected carrier of a kept span, a malformed or foreign shape, or none.
func carrierOf(recorder *telemetry.InMemoryTelemetryContext, kept []telemetry.TelemetrySpan, op opSpec) telemetry.TelemetryCarrier {
	var own telemetry.TelemetryCarrier
	if len(kept) > 0 {
		own = recorder.Inject(kept[op.Idx%len(kept)])
	}
	scope := own["pi-memory-scope"]
	foreign := func(span string) telemetry.TelemetryCarrier {
		return telemetry.TelemetryCarrier{"pi-memory-scope": foreignScope, "pi-memory-span": span}
	}
	switch op.Carrier {
	case "kept":
		return own
	case "extra":
		if own != nil {
			own["extra"] = "key"
		}
		return own
	case "missingSpan":
		if own != nil {
			return telemetry.TelemetryCarrier{"pi-memory-scope": scope}
		}
	case "badScope":
		return telemetry.TelemetryCarrier{"pi-memory-scope": "X", "pi-memory-span": "1"}
	case "upperScope":
		return telemetry.TelemetryCarrier{"pi-memory-scope": strings.ToUpper(foreignScope), "pi-memory-span": "1"}
	case "spanZero":
		return foreign("0")
	case "spanLeadingZero":
		return foreign("01")
	case "spanSign":
		return foreign("+1")
	case "spanUnsafe":
		return foreign("9007199254740992")
	case "spanHuge":
		return foreign("99999999999999999999999")
	case "spanMaxSafe":
		return foreign("9007199254740991")
	case "foreign":
		return foreign("1")
	case "ownUnknown":
		if own != nil {
			return telemetry.TelemetryCarrier{"pi-memory-scope": scope, "pi-memory-span": "99999"}
		}
	case "empty":
		return telemetry.TelemetryCarrier{}
	}
	return nil
}

// namedFailure is a JavaScript Error whose name was assigned.
type namedFailure struct{ name, message string }

func (failure *namedFailure) Error() string { return failure.message }
func (failure *namedFailure) Name() string  { return failure.name }

func attributesOf(attrs map[string]any) telemetry.SpanAttributes {
	if attrs == nil {
		return nil
	}
	out := telemetry.SpanAttributes{}
	for name, value := range attrs {
		if marker, ok := value.(map[string]any); ok && marker["$undef"] == true {
			out[name] = nil
			continue
		}
		out[name] = value
	}
	return out
}

func finish(outcome outcomeSpec) error {
	switch outcome.Kind {
	case "plain":
		return errors.New(outcome.Message)
	case "error":
		return &namedFailure{outcome.Name, outcome.Message}
	case "nonError":
		panic(outcome.Value)
	}
	return nil
}

func applyOp(recorder *telemetry.InMemoryTelemetryContext, span telemetry.TelemetrySpan, op opSpec, kept *[]telemetry.TelemetrySpan) {
	switch op.Op {
	case "attrs":
		span.SetAttributes(attributesOf(op.Attrs))
	case "event":
		span.AddEvent(op.Name, attributesOf(op.Attrs))
	case "status":
		var status struct {
			Status telemetry.SpanStatusCode `json:"status"`
			Error  *struct{ Name, Message string }
		}
		if err := json.Unmarshal(op.Status, &status); err != nil {
			panic(err)
		}
		value := telemetry.SpanStatus{Status: status.Status}
		if status.Error != nil {
			value.Error = &telemetry.SpanStatusError{Name: status.Error.Name, Message: status.Error.Message}
		}
		span.SetStatus(value)
	case "keep":
		*kept = append(*kept, span)
	case "useKept":
		if len(*kept) > 0 {
			applyOp(recorder, (*kept)[op.Idx%len(*kept)], *op.Sub, kept)
		}
	case "link":
		span.AddLink(telemetry.SpanLink{Context: linkTarget(recorder, *kept, op.Target, op.Idx), Attributes: attributesOf(op.Attrs)})
	case "child", "remote":
		parent := telemetry.TelemetryContext(span)
		if op.Op == "remote" {
			parent = recorder.Extract(carrierOf(recorder, *kept, op))
		}
		func() {
			defer func() { _ = recover() }()
			_ = parent.StartSpan(telemetry.SpanOptions{Name: op.Name, Attributes: attributesOf(op.Attrs), Links: linksOf(recorder, *kept, op.Links)}, func(child telemetry.TelemetrySpan) error {
				for _, sub := range op.Body {
					applyOp(recorder, child, sub, kept)
				}
				return finish(*op.Outcome)
			})
		}()
	}
}

// The spans Pi's InMemoryTelemetryContext records for seeded random span programs (attributes with undefined values, array values, events, explicit and automatic statuses,
// callbacks that return, throw a named Error or throw a non-Error, spans kept and used after they settled, children started under a settled parent) against PiG's, as
// the JSON of getSpans(). Programs await every child, since a Go callback runs to completion before its caller continues.
func TestInMemoryTelemetryMatchesPiOnRandomPrograms(t *testing.T) {
	rng := rand.New(rand.NewSource(151))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	attributes := func() map[string]any {
		if rng.Intn(5) == 0 {
			return nil
		}
		out := map[string]any{}
		for range rng.Intn(4) {
			name := pick("a", "b", "c", "10", "2", "é", "")
			switch rng.Intn(7) {
			case 0:
				out[name] = pick("x", "", "é😀")
			case 1:
				out[name] = float64(rng.Intn(10)) + []float64{0, 0.5}[rng.Intn(2)]
			case 2:
				out[name] = rng.Intn(2) == 0
			case 3:
				out[name] = []any{pick("p", "q"), pick("r", "s")}
			case 4:
				out[name] = []any{float64(rng.Intn(3)), float64(rng.Intn(3))}
			case 5:
				out[name] = []any{}
			default:
				out[name] = map[string]any{"$undef": true}
			}
		}
		return out
	}
	status := func() json.RawMessage {
		switch rng.Intn(5) {
		case 0:
			return json.RawMessage(`{"status":"ok"}`)
		case 1:
			return json.RawMessage(`{"status":"error"}`)
		case 2:
			return json.RawMessage(fmt.Sprintf(`{"status":"error","error":{"name":%q,"message":%q}}`, pick("E", "TypeError", ""), pick("m", "", "é")))
		case 3:
			return json.RawMessage(`{"status":"ok","error":{"name":"E","message":"m"}}`)
		}
		return json.RawMessage(`{"status":"error","error":{"name":"","message":""}}`)
	}
	outcome := func() outcomeSpec {
		switch rng.Intn(7) {
		case 0:
			return outcomeSpec{Kind: "error", Name: pick("Error", "TypeError", "Custom", ""), Message: pick("boom", "", "é")}
		case 2:
			// A thrown Error with no assigned name: Pi's `error.name` is "Error", PiG's error has no Name method.
			return outcomeSpec{Kind: "plain", Name: "Error", Message: pick("boom", "", "é")}
		case 1:
			return outcomeSpec{Kind: "nonError", Value: pick("s", "")}
		}
		return outcomeSpec{Kind: "return"}
	}
	link := func() linkSpec {
		return linkSpec{Target: pick("kept", "kept", "kept", "none", "noop", "recorder"), Idx: rng.Intn(4), Attrs: attributes()}
	}
	links := func() []linkSpec {
		var out []linkSpec
		for range rng.Intn(3) {
			out = append(out, link())
		}
		return out
	}
	var body func(depth int) []opSpec
	var op func(depth int, allowKept bool) opSpec
	op = func(depth int, allowKept bool) opSpec {
		for {
			switch rng.Intn(11) {
			case 0:
				return opSpec{Op: "attrs", Attrs: attributes()}
			case 1:
				return opSpec{Op: "event", Name: pick("e1", "e2", ""), Attrs: attributes()}
			case 2:
				return opSpec{Op: "status", Status: status()}
			case 3:
				return opSpec{Op: "keep"}
			case 4:
				if !allowKept {
					continue
				}
				sub := op(depth, false)
				return opSpec{Op: "useKept", Idx: rng.Intn(4), Sub: &sub}
			case 5, 6:
				l := link()
				return opSpec{Op: "link", Target: l.Target, Idx: l.Idx, Attrs: l.Attrs}
			case 7:
				if depth >= 3 {
					continue
				}
				o := outcome()
				carrier := pick("kept", "kept", "kept", "extra", "missingSpan", "badScope", "upperScope", "spanZero", "spanLeadingZero", "spanSign", "spanUnsafe", "spanHuge", "spanMaxSafe", "foreign", "ownUnknown", "empty", "none")
				return opSpec{Op: "remote", Carrier: carrier, Idx: rng.Intn(4), Name: pick("r1", "r2", ""), Attrs: attributes(), Links: links(), Body: body(depth + 1), Outcome: &o}
			default:
				if depth >= 3 {
					continue
				}
				o := outcome()
				return opSpec{Op: "child", Name: pick("c1", "c2", ""), Attrs: attributes(), Links: links(), Body: body(depth + 1), Outcome: &o}
			}
		}
	}
	body = func(depth int) []opSpec {
		ops := []opSpec{}
		for range rng.Intn(6) {
			ops = append(ops, op(depth, true))
		}
		return ops
	}
	var programs [][]rootSpec
	for range 1500 {
		var roots []rootSpec
		for range 1 + rng.Intn(3) {
			roots = append(roots, rootSpec{Name: pick("r1", "r2", ""), Attrs: attributes(), Links: links(), Body: body(1), Outcome: outcome()})
		}
		programs = append(programs, roots)
	}
	input, err := json.Marshal(programs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/memory_oracle.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []json.RawMessage
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(programs) {
		t.Fatalf("Pi answered %d programs, want %d", len(want), len(programs))
	}
	failures, spans, linked, remote := 0, 0, 0, 0
	for i, program := range programs {
		recorder := telemetry.NewInMemoryTelemetryContext()
		var kept []telemetry.TelemetrySpan
		for _, root := range program {
			func() {
				defer func() { _ = recover() }()
				_ = recorder.StartSpan(telemetry.SpanOptions{Name: root.Name, Attributes: attributesOf(root.Attrs), Links: linksOf(recorder, kept, root.Links)}, func(span telemetry.TelemetrySpan) error {
					for _, sub := range root.Body {
						applyOp(recorder, span, sub, &kept)
					}
					return finish(root.Outcome)
				})
			}()
		}
		recorded := recorder.GetSpans()
		spans += len(recorded)
		for _, span := range recorded {
			if len(span.Links) > 0 {
				linked++
			}
			if span.ParentContext != nil && span.ParentID == nil {
				remote++
			}
		}
		got := make([]map[string]any, len(recorded))
		for j, span := range recorded {
			events := make([]map[string]any, len(span.Events))
			for k, event := range span.Events {
				events[k] = map[string]any{"name": event.Name, "attributes": event.Attributes}
			}
			status := map[string]any{"status": string(span.Status.Status)}
			if span.Status.Error != nil {
				status["error"] = map[string]any{"name": span.Status.Error.Name, "message": span.Status.Error.Message}
			}
			links := make([]map[string]any, len(span.Links))
			for k, link := range span.Links {
				links[k] = map[string]any{"context": link.Context, "attributes": link.Attributes}
			}
			var parentContext any
			if span.ParentContext != nil {
				parentContext = span.ParentContext
			}
			got[j] = map[string]any{"id": span.ID, "parentId": nil, "context": span.Context, "parentContext": parentContext, "links": links, "name": span.Name, "attributes": span.Attributes, "events": events, "status": status, "settled": span.Settled}
			if span.ParentID != nil {
				got[j]["parentId"] = *span.ParentID
			}
			if span.EndSequence != nil {
				got[j]["endSequence"] = *span.EndSequence
			}
		}
		gotJSON, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		// The recorder's scope is a random UUID: Pi's output prints it as SCOPE, and so does PiG's.
		gotJSON = scopePattern.ReplaceAll(gotJSON, []byte("SCOPE"))
		var gotValue, wantValue any
		if err := json.Unmarshal(gotJSON, &gotValue); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(want[i], &wantValue); err != nil {
			t.Fatal(err)
		}
		gotCanonical, _ := json.Marshal(gotValue)
		wantCanonical, _ := json.Marshal(wantValue)
		if !bytes.Equal(gotCanonical, wantCanonical) {
			if failures++; failures <= 3 {
				programJSON, _ := json.Marshal(program)
				t.Errorf("program %d %s:\n PiG %s\n Pi  %s", i, programJSON, gotCanonical, wantCanonical)
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d programs differ from Pi", failures, len(programs))
	}
	if spans < len(programs)*2 {
		t.Fatalf("the programs recorded only %d spans", spans)
	}
	if linked < len(programs)/2 || remote < len(programs)/10 {
		t.Fatalf("the programs recorded %d linked spans and %d spans under a remote parent in %d programs", linked, remote, len(programs))
	}
}
