package telemetry

// Ports packages/telemetry/test/telemetry.test.ts.
//
// The expectTypeOf and @ts-expect-error assertions check TypeScript inference of exact attributes, closed value sets,
// narrowed span names and duplicate span names; they have no Go form and upstream performs no runtime validation for
// them. Object.isFrozen has no Go form either: the noop span is a stateless value.

import (
	"encoding/json"
	"errors"
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
		telemetryContext := &InMemoryTelemetryContext{}
		startSpan := CreateTypedSpanStarter(telemetryContext, operationSchema, requestSchema)

		result := 0
		err := startSpan("operation", SpanAttributes{"kind": "read"}, func(_ TelemetrySpan, startChildSpan TypedSpanStarter) error {
			return startChildSpan("request", SpanAttributes{"provider": "example"}, func(requestSpan TelemetrySpan, _ TypedSpanStarter) error {
				requestSpan.SetAttributes(SpanAttributes{"response": "cached"})
				result = 42
				return nil
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
		if err := startSpan("operation", SpanAttributes{"kind": "write"}, func(TelemetrySpan, TypedSpanStarter) error {
			return syncError
		}); err != syncError {
			t.Fatalf("sync rejection = %v, want the thrown value", err)
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
		result := 0
		err := NoopTelemetryContext.StartSpan(SpanOptions{Name: "first"}, func(span TelemetrySpan) error {
			admitted = true
			firstSpan = span
			var child TelemetrySpan
			if err := span.StartSpan(SpanOptions{Name: "child"}, func(childSpan TelemetrySpan) error {
				child = childSpan
				return nil
			}); err != nil {
				return err
			}
			if child != span {
				t.Errorf("child span = %#v, want the parent's inert span", child)
			}
			result = 42
			return nil
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
		if err := NoopTelemetryContext.StartSpan(SpanOptions{Name: "sync"}, func(TelemetrySpan) error { return syncError }); err != syncError {
			t.Fatalf("sync rejection = %v", err)
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
