package telemetry

// Ports packages/telemetry/test/telemetry.test.ts.
//
// The expectTypeOf and @ts-expect-error assertions check TypeScript inference of exact attributes, closed value sets,
// narrowed span names and duplicate span names; they have no Go form and upstream performs no runtime validation for
// them. Object.isFrozen has no Go form either: the noop span is a stateless value.

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// panickingValue is the Go form of upstream's unreadable payload: every method that could inspect it panics.
type panickingValue struct{}

func (panickingValue) String() string               { panic("read") }
func (panickingValue) MarshalJSON() ([]byte, error) { panic("read") }

func awaited(err error) error {
	result := make(chan error, 1)
	go func() { result <- err }()
	return <-result
}

// TestTelemetryUpstream ports packages/telemetry/test/telemetry.test.ts. Each subtest names one upstream case.
// Pi source: packages/telemetry/src/noop.ts
// mutation-checked: zeroing the results of TelemetryContext.StartSpan, TelemetrySpan.SetAttributes, StartSpan and StartTypedSpan fails it
// mutation-checked: dropping the reads and writes of RecordedTelemetrySpan.Name, RecordedTelemetrySpan.ParentID fails it
func TestTelemetryUpstream(t *testing.T) {
	t.Run("telemetry schemas › preserves serializable definitions and infers exact attributes", func(t *testing.T) {
		// upstream: packages/telemetry/test/telemetry.test.ts:30
		definition := &TelemetrySchemaDefinition{
			Version: 1,
			Spans: map[string]TelemetrySpanDefinition{
				"operation": {
					Description: "Test operation",
					Parents:     TelemetryParentDefinition{Kind: TelemetryParentKindAny},
					StartAttributes: map[string]TelemetryStartAttributeDefinition{
						"kind": {Required: true, TelemetryAttributeDefinition: TelemetryAttributeDefinition{
							Type: TelemetryAttributeTypeString, Values: []AttributeValue{"read", "write"},
							TelemetryAttributeMetadata: TelemetryAttributeMetadata{Description: "Kind"},
						}},
					},
					EndAttributes: map[string]TelemetryAttributeDefinition{},
					Events: map[string]TelemetryEventDefinition{
						"result": {
							Description: "Result",
							Attributes: map[string]TelemetryEventAttributeDefinition{
								"outcome": {Required: true, TelemetryAttributeDefinition: TelemetryAttributeDefinition{
									Type: TelemetryAttributeTypeString, Values: []AttributeValue{"ok", "error"},
									TelemetryAttributeMetadata: TelemetryAttributeMetadata{Description: "Outcome"},
								}},
							},
						},
					},
					Status: TelemetrySpanStatusDefinition{Default: SpanStatusCodeOK, ErrorWhen: "The operation fails"},
				},
			},
		}
		schema := DefineTelemetrySchema(definition)
		if schema != definition {
			t.Fatal("defineTelemetrySchema must return the definition itself")
		}
		encoded, err := json.Marshal(schema)
		if err != nil {
			t.Fatalf("schema must serialize: %v", err)
		}
		const want = `{"version":1,"spans":{"operation":{"description":"Test operation","parents":{"kind":"any"},` +
			`"startAttributes":{"kind":{"description":"Kind","type":"string","values":["read","write"],"required":true}},` +
			`"endAttributes":{},"events":{"result":{"description":"Result","attributes":{"outcome":{"description":"Outcome",` +
			`"type":"string","values":["ok","error"],"required":true}}}},"status":{"default":"ok","errorWhen":"The operation fails"}}}}`
		if string(encoded) != want {
			t.Fatalf("serialized schema:\n got %s\nwant %s", encoded, want)
		}
	})

	t.Run("telemetry schemas › combines schema vocabularies and binds child starters to their parent spans", func(t *testing.T) {
		// upstream: packages/telemetry/test/telemetry.test.ts:74
		operationSchema := DefineTelemetrySchema(&TelemetrySchemaDefinition{
			Version: 1,
			Spans: map[string]TelemetrySpanDefinition{"operation": {
				Description: "Operation",
				Parents:     TelemetryParentDefinition{Kind: TelemetryParentKindRootOrExternal},
				StartAttributes: map[string]TelemetryStartAttributeDefinition{"kind": {Required: true, TelemetryAttributeDefinition: TelemetryAttributeDefinition{
					Type: TelemetryAttributeTypeString, Values: []AttributeValue{"read", "write"}, TelemetryAttributeMetadata: TelemetryAttributeMetadata{Description: "Kind"},
				}}},
				EndAttributes: map[string]TelemetryAttributeDefinition{},
				Status:        TelemetrySpanStatusDefinition{Default: SpanStatusCodeOK, ErrorWhen: "The operation fails"},
			}},
		})
		requestSchema := DefineTelemetrySchema(&TelemetrySchemaDefinition{
			Version: 3,
			Spans: map[string]TelemetrySpanDefinition{"request": {
				Description: "Request",
				Parents:     TelemetryParentDefinition{Kind: TelemetryParentKindSpans, Spans: []string{"operation"}},
				StartAttributes: map[string]TelemetryStartAttributeDefinition{"provider": {Required: true, TelemetryAttributeDefinition: TelemetryAttributeDefinition{
					Type: TelemetryAttributeTypeString, TelemetryAttributeMetadata: TelemetryAttributeMetadata{Description: "Provider"},
				}}},
				EndAttributes: map[string]TelemetryAttributeDefinition{"response": {
					Type: TelemetryAttributeTypeString, TelemetryAttributeMetadata: TelemetryAttributeMetadata{Description: "Response kind"},
				}},
				Status: TelemetrySpanStatusDefinition{Default: SpanStatusCodeOK, ErrorWhen: "The request fails"},
			}},
		})
		telemetryContext := NewInMemoryTelemetryContext()
		startSpan := CreateTypedSpanStarter(telemetryContext, operationSchema, requestSchema)

		result, err := StartTypedSpan(startSpan, "operation", SpanAttributes{"kind": "read"}, func(_ TelemetrySpan, startChildSpan TypedSpanStarter) (int, error) {
			return StartTypedSpan(startChildSpan, "request", SpanAttributes{"provider": "example"}, func(requestSpan TelemetrySpan, _ TypedSpanStarter) (int, error) {
				requestSpan.SetAttributes(SpanAttributes{"response": "cached"})
				return 42, nil
			})
		})
		if err != nil || result != 42 {
			t.Fatalf("result = %d, %v; want 42", result, err)
		}
		var operationSpan, requestSpan *RecordedTelemetrySpan
		spans := telemetryContext.GetSpans()
		for i := range spans {
			switch spans[i].Name {
			case "operation":
				operationSpan = &spans[i]
			case "request":
				requestSpan = &spans[i]
			}
		}
		if operationSpan == nil || operationSpan.ParentID != nil {
			t.Fatalf("operation span = %+v, want a root span", operationSpan)
		}
		if requestSpan == nil || requestSpan.ParentID == nil || *requestSpan.ParentID != operationSpan.ID {
			t.Fatalf("request span = %+v, want parent %d", requestSpan, operationSpan.ID)
		}
		if got := requestSpan.Attributes["response"]; got != "cached" {
			t.Fatalf("request response attribute = %v", got)
		}
		// Upstream passes an unreadable schema tuple; Go passes schemas that panic on any read.
		func() {
			defer func() {
				if failure := recover(); failure != nil {
					t.Fatalf("createTypedSpanStarter read its schemas: %v", failure)
				}
			}()
			CreateTypedSpanStarter(telemetryContext, nil, nil)
		}()

		syncError := errors.New("sync")
		if value, err := StartTypedSpan(startSpan, "operation", SpanAttributes{"kind": "write"}, func(TelemetrySpan, TypedSpanStarter) (int, error) {
			return 7, syncError
		}); err != syncError || value != 0 {
			t.Fatalf("sync rejection = %d, %v; want the thrown value and no result", value, err)
		}
		asyncError := errors.New("async")
		if err := startSpan("request", SpanAttributes{"provider": "example"}, func(TelemetrySpan, TypedSpanStarter) error {
			return awaited(asyncError)
		}); err != asyncError {
			t.Fatalf("async rejection = %v, want the thrown value", err)
		}
	})

	t.Run("NOOP_TELEMETRY_CONTEXT › admits callbacks synchronously and reuses one inert span", func(t *testing.T) {
		// upstream: packages/telemetry/test/telemetry.test.ts:157
		admitted := false
		var firstSpan TelemetrySpan
		result, err := StartSpan(NoopTelemetryContext, SpanOptions{Name: "first"}, func(span TelemetrySpan) (int, error) {
			admitted = true
			firstSpan = span
			child, err := StartSpan(span, SpanOptions{Name: "child"}, func(childSpan TelemetrySpan) (TelemetrySpan, error) { return childSpan, nil })
			if err != nil {
				return 0, err
			}
			if child != span {
				t.Errorf("child span = %#v, want the parent's inert span", child)
			}
			return 42, nil
		})
		if !admitted || err != nil || result != 42 {
			t.Fatalf("admitted = %v, result = %d, err = %v", admitted, result, err)
		}
		if firstSpan != NoopTelemetryContext {
			t.Fatalf("callback span = %#v, want NOOP_TELEMETRY_CONTEXT", firstSpan)
		}
	})

	t.Run("NOOP_TELEMETRY_CONTEXT › preserves synchronous and asynchronous rejection values", func(t *testing.T) {
		// upstream: packages/telemetry/test/telemetry.test.ts:173
		syncError := errors.New("sync")
		if value, err := StartSpan(NoopTelemetryContext, SpanOptions{Name: "sync"}, func(TelemetrySpan) (int, error) { return 7, syncError }); err != syncError || value != 0 {
			t.Fatalf("sync rejection = %d, %v; want the thrown value and no result", value, err)
		}
		asyncError := errors.New("async")
		if err := NoopTelemetryContext.StartSpan(SpanOptions{Name: "async"}, func(TelemetrySpan) error { return awaited(asyncError) }); err != asyncError {
			t.Fatalf("async rejection = %v", err)
		}
		thrown := &struct{ kind string }{"panic"}
		func() {
			defer func() {
				if failure := recover(); failure != any(thrown) {
					t.Fatalf("panic = %v, want the thrown value", failure)
				}
			}()
			_ = NoopTelemetryContext.StartSpan(SpanOptions{Name: "panic"}, func(TelemetrySpan) error { panic(thrown) })
		}()
	})

	t.Run("NOOP_TELEMETRY_CONTEXT › does not inspect or retain telemetry payloads", func(t *testing.T) {
		// upstream: packages/telemetry/test/telemetry.test.ts:187
		options := SpanOptions{Name: "operation", Attributes: SpanAttributes{"secret": panickingValue{}}}
		if err := NoopTelemetryContext.StartSpan(options, func(span TelemetrySpan) error {
			attributes := SpanAttributes{"secret": panickingValue{}}
			span.AddEvent("event", attributes)
			span.SetAttributes(attributes)
			span.SetStatus(SpanStatus{Status: SpanStatusCodeOK})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
}

// TestTelemetrySchemaKeepsEmptyMembers checks that schema data serializes as JSON.stringify writes the same object
// (measured with Node v24): an empty values, elementValues, examples or spans array is [] and empty events are {},
// while an absent member is omitted.
func TestTelemetrySchemaKeepsEmptyMembers(t *testing.T) {
	schema := TelemetrySchemaDefinition{Version: 1, Spans: map[string]TelemetrySpanDefinition{"empty": {
		Description: "Empty",
		Parents:     TelemetryParentDefinition{Kind: TelemetryParentKindSpans, Spans: []string{}},
		StartAttributes: map[string]TelemetryStartAttributeDefinition{"kind": {TelemetryAttributeDefinition: TelemetryAttributeDefinition{
			TelemetryAttributeMetadata: TelemetryAttributeMetadata{Description: "Kind"},
			Type:                       TelemetryAttributeTypeStringArray, ElementValues: []AttributeValue{}, Examples: []AttributeValue{},
		}}},
		EndAttributes: map[string]TelemetryAttributeDefinition{"outcome": {
			TelemetryAttributeMetadata: TelemetryAttributeMetadata{Description: "Outcome"},
			Type:                       TelemetryAttributeTypeString, Values: []AttributeValue{},
		}},
		Events: map[string]TelemetryEventDefinition{},
		Status: TelemetrySpanStatusDefinition{Default: SpanStatusCodeOK, ErrorWhen: "Never"},
	}, "absent": {
		Description:     "Absent",
		Parents:         TelemetryParentDefinition{Kind: TelemetryParentKindAny},
		StartAttributes: map[string]TelemetryStartAttributeDefinition{},
		EndAttributes:   map[string]TelemetryAttributeDefinition{"outcome": {Type: TelemetryAttributeTypeString}},
		Status:          TelemetrySpanStatusDefinition{Default: SpanStatusCodeOK, ErrorWhen: "Never"},
	}}}
	encoded, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"version":1,"spans":{"absent":{"description":"Absent","parents":{"kind":"any"},"startAttributes":{},` +
		`"endAttributes":{"outcome":{"description":"","type":"string"}},"status":{"default":"ok","errorWhen":"Never"}},` +
		`"empty":{"description":"Empty","parents":{"kind":"spans","spans":[]},"startAttributes":{"kind":{"description":"Kind",` +
		`"type":"string[]","elementValues":[],"examples":[],"required":false}},"endAttributes":{"outcome":{"description":"Outcome",` +
		`"type":"string","values":[]}},"events":{},"status":{"default":"ok","errorWhen":"Never"}}}}`
	if string(encoded) != want {
		t.Fatalf("serialized schema:\n got %s\nwant %s", encoded, want)
	}
}

// TestTypedSpanStarterRecordsStartAttributesAndNestsByCallbackSpan pins createTypedSpanStarter's pass-through of the
// start attributes to startSpan and the grandchild binding to the child span (packages/telemetry/src/index.ts
// createTypedSpanStarter: `telemetry.startSpan({ name, attributes }, span => callback(span, bind(span)))`).
func TestTypedSpanStarterRecordsStartAttributesAndNestsByCallbackSpan(t *testing.T) {
	recorder := NewInMemoryTelemetryContext()
	startSpan := CreateTypedSpanStarter(recorder, &TelemetrySchemaDefinition{})
	err := startSpan("root", SpanAttributes{"kind": "read", "count": 3}, func(_ TelemetrySpan, child TypedSpanStarter) error {
		return child("middle", SpanAttributes{"flag": true}, func(_ TelemetrySpan, grandchild TypedSpanStarter) error {
			return grandchild("leaf", nil, func(TelemetrySpan, TypedSpanStarter) error { return nil })
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	spans := recorder.GetSpans()
	if len(spans) != 3 {
		t.Fatalf("spans = %+v, want root, middle and leaf", spans)
	}
	if got := spans[0].Attributes; !reflect.DeepEqual(got, SpanAttributes{"kind": "read", "count": 3}) {
		t.Fatalf("root attributes = %#v", got)
	}
	if got := spans[1].Attributes; !reflect.DeepEqual(got, SpanAttributes{"flag": true}) {
		t.Fatalf("middle attributes = %#v", got)
	}
	if spans[1].ParentID == nil || *spans[1].ParentID != spans[0].ID || spans[2].ParentID == nil || *spans[2].ParentID != spans[1].ID {
		t.Fatalf("parents = %v, %v; want middle under root and leaf under middle", spans[1].ParentID, spans[2].ParentID)
	}
}

// packages/telemetry/src/memory.ts:22,71-76 RecordedTelemetrySpan.status and copyStatus: the recorder keeps a detached copy of the status a span sets (an ok status carries
// no error, an error status keeps only name and message), and a settled span keeps its status when it is set again.
func TestInMemoryRecorderKeepsADetachedCopyOfTheSpanStatus(t *testing.T) {
	recorder := NewInMemoryTelemetryContext()
	failure := &SpanStatusError{Name: "Boom", Message: "it failed"}
	status := SpanStatus{Status: SpanStatusCodeError, Error: failure}
	var settled TelemetrySpan
	if err := recorder.StartSpan(SpanOptions{Name: "failing"}, func(span TelemetrySpan) error {
		span.SetStatus(status)
		settled = span
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.StartSpan(SpanOptions{Name: "ok"}, func(span TelemetrySpan) error {
		span.SetStatus(SpanStatus{Status: SpanStatusCodeOK, Error: failure})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	failure.Message = "mutated after recording"
	settled.SetStatus(SpanStatus{Status: SpanStatusCodeOK})
	spans := recorder.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("recorded %d spans, want 2", len(spans))
	}
	if got := spans[0].Status; got.Status != SpanStatusCodeError || got.Error == nil || got.Error.Name != "Boom" || got.Error.Message != "it failed" {
		t.Fatalf("failing span status = %+v, want the status as set, detached from later mutation and from a later set on the settled span", got)
	}
	if got := spans[1].Status; got.Status != SpanStatusCodeOK || got.Error != nil {
		t.Fatalf("ok span status = %+v, want ok without an error", got)
	}
}

// packages/telemetry/src/memory.ts:190-197 InMemoryTelemetryContext: every new instance starts empty and records on its own, so spans of one recorder never appear in another.
func TestNewInMemoryTelemetryContextStartsEmptyAndIsolated(t *testing.T) {
	first, second := NewInMemoryTelemetryContext(), NewInMemoryTelemetryContext()
	if first == second || len(first.GetSpans()) != 0 || len(second.GetSpans()) != 0 {
		t.Fatalf("new recorders: same=%v, spans %d/%d", first == second, len(first.GetSpans()), len(second.GetSpans()))
	}
	if err := first.StartSpan(SpanOptions{Name: "only-first"}, func(TelemetrySpan) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(first.GetSpans()) != 1 || len(second.GetSpans()) != 0 {
		t.Fatalf("spans: first %d, second %d, want 1 and 0", len(first.GetSpans()), len(second.GetSpans()))
	}
}
