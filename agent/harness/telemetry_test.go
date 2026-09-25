package harness_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"reflect"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/execution"
)

// These tests follow harness/telemetry.ts and hooks.ts. The recorder observes the
// harness's calls, not a transport or exporter; status inference belongs to the
// supplied TelemetryContext, not to the harness span helpers.
type telemetryRecord struct {
	name       string
	parent     int
	attributes harness.SpanAttributes
	status     harness.SpanStatus
	events     []string
	err        error
	settled    bool
	end        int
}

type telemetryRecorder struct {
	mu      sync.Mutex
	records []telemetryRecord
	nextEnd int
}

type telemetrySpan struct {
	recorder *telemetryRecorder
	index    int
}

func (recorder *telemetryRecorder) StartSpan(options harness.SpanOptions, callback func(harness.TelemetrySpan) error) error {
	return recorder.start(-1, options, callback)
}

func (recorder *telemetryRecorder) start(parent int, options harness.SpanOptions, callback func(harness.TelemetrySpan) error) error {
	recorder.mu.Lock()
	index := len(recorder.records)
	recorder.records = append(recorder.records, telemetryRecord{
		name: options.Name, parent: parent, attributes: maps.Clone(options.Attributes),
	})
	recorder.mu.Unlock()
	err := callback(&telemetrySpan{recorder: recorder, index: index})
	recorder.mu.Lock()
	recorder.nextEnd++
	recorder.records[index].err = err
	recorder.records[index].settled = true
	recorder.records[index].end = recorder.nextEnd
	recorder.mu.Unlock()
	return err
}

func (span *telemetrySpan) StartSpan(options harness.SpanOptions, callback func(harness.TelemetrySpan) error) error {
	return span.recorder.start(span.index, options, callback)
}

func (span *telemetrySpan) SetAttributes(attributes harness.SpanAttributes) {
	span.recorder.mu.Lock()
	defer span.recorder.mu.Unlock()
	current := &span.recorder.records[span.index]
	if current.attributes == nil {
		current.attributes = harness.SpanAttributes{}
	}
	maps.Copy(current.attributes, attributes)
}

func (span *telemetrySpan) SetStatus(status harness.SpanStatus) {
	span.recorder.mu.Lock()
	defer span.recorder.mu.Unlock()
	span.recorder.records[span.index].status = status
}

func (span *telemetrySpan) AddEvent(name string, _ harness.SpanAttributes) {
	span.recorder.mu.Lock()
	defer span.recorder.mu.Unlock()
	span.recorder.records[span.index].events = append(span.recorder.records[span.index].events, name)
}

func (recorder *telemetryRecorder) snapshot() []telemetryRecord {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	result := make([]telemetryRecord, len(recorder.records))
	for i, record := range recorder.records {
		result[i] = record
		result[i].attributes = maps.Clone(record.attributes)
	}
	return result
}

func assertTelemetryErrorIdentity(t *testing.T, got, want error) {
	t.Helper()
	if got != want { //nolint:errorlint // Telemetry must preserve the exact error object; even a wrapping error changes this contract.
		t.Fatalf("error identity changed: got %v (%T), want %v (%T)", got, got, want, want)
	}
}

func TestTelemetrySpanHelpersPreserveContextResultAndError(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start func(harness.Context, string, harness.SpanAttributes, func(harness.TelemetrySpan, harness.Context) (*int, error)) (*int, error)
	}{
		{"ai", harness.StartAiSpan[*int]},
		{"harness", harness.StartHarnessSpan[*int]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, callbackError := range []error{nil, errors.New("callback rejected")} {
				recorder := &telemetryRecorder{}
				key := harness.CreateContextKey[string]("ordinary value")
				base, cancel := context.WithCancelCause(harness.BackgroundContext())
				t.Cleanup(func() { cancel(nil) })
				ctx := harness.WithContextValue(harness.WithTelemetryContext(base, recorder), key, "kept")
				value := 42
				attributes := harness.SpanAttributes{"pi.lane.name": "main", "pi.operation.id": "operation"}
				// Upstream schemas constrain TypeScript, not runtime dispatch: the
				// wrapper forwards the supplied name/attributes without validation.
				got, err := tc.start(ctx, "supplied.name", attributes, func(span harness.TelemetrySpan, child harness.Context) (*int, error) {
					if harness.GetTelemetryContext(child) != span || harness.GetTelemetryContext(ctx) != recorder {
						t.Fatal("child must use the active span without changing its parent context")
					}
					if actual, ok := harness.ContextValue(child, key); !ok || actual != "kept" || child.Done() != ctx.Done() {
						t.Fatal("span context lost ordinary values or cancellation identity")
					}
					_, nestedErr := harness.StartAiSpan(child, "pi.ai.request", nil, func(nested harness.TelemetrySpan, nestedContext harness.Context) (int, error) {
						if harness.GetTelemetryContext(nestedContext) != nested {
							t.Fatal("nested context lost its active span")
						}
						return 1, nil
					})
					if nestedErr != nil {
						t.Fatal(nestedErr)
					}
					return &value, callbackError
				})
				assertTelemetryErrorIdentity(t, err, callbackError)
				if got != &value {
					t.Fatalf("result identity changed: %v", got)
				}
				records := recorder.snapshot()
				if len(records) != 2 || records[0].name != "supplied.name" || !reflect.DeepEqual(records[0].attributes, attributes) {
					t.Fatalf("span options changed: %+v", records)
				}
				if records[0].parent != -1 || records[1].parent != 0 || records[1].name != "pi.ai.request" || records[1].end >= records[0].end {
					t.Fatalf("nested parenting/completion order: %+v", records)
				}
				assertTelemetryErrorIdentity(t, records[0].err, callbackError)
				if records[0].status.Status != "" {
					t.Fatal("wrapper must pass the exact error to the recorder without setting status")
				}
			}
		})
	}
}

func TestTelemetrySpanNoopAndCanceledContextStillInvokeCallback(t *testing.T) {
	for _, ctx := range []harness.Context{harness.BackgroundContext(), harness.TODOContext()} {
		ctx, cancel := context.WithCancelCause(ctx)
		cause := errors.New("already aborted")
		cancel(cause)
		for _, start := range []func(harness.Context, string, harness.SpanAttributes, func(harness.TelemetrySpan, harness.Context) (string, error)) (string, error){
			harness.StartAiSpan[string], harness.StartHarnessSpan[string],
		} {
			result, err := start(ctx, "pi.harness.hook", nil, func(span harness.TelemetrySpan, child harness.Context) (string, error) {
				assertTelemetryErrorIdentity(t, context.Cause(child), cause)
				if span != harness.NoopTelemetryContext || harness.GetTelemetryContext(child) != span {
					t.Fatal("no-op context changed")
				}
				return "callback ran", cause
			})
			assertTelemetryErrorIdentity(t, err, cause)
			if result != "callback ran" {
				t.Fatalf("a span wrapper must not short-circuit cancellation: %q", result)
			}
		}
	}
}

func TestTelemetrySpanWaitsForCallbackAfterCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := &telemetryRecorder{}
		ctx, cancel := context.WithCancelCause(harness.WithTelemetryContext(harness.BackgroundContext(), recorder))
		defer cancel(nil)
		release := make(chan struct{})
		done := make(chan error, 1)
		failure := errors.New("late callback failure")
		go func() {
			_, err := harness.StartHarnessSpan(ctx, "pi.harness.hook", nil, func(_ harness.TelemetrySpan, child harness.Context) (int, error) {
				<-release
				return 0, context.Cause(child)
			})
			done <- err
		}()
		synctest.Wait()
		cancel(failure)
		synctest.Wait()
		records := recorder.snapshot()
		if len(records) != 1 || records[0].settled {
			t.Fatalf("callback has not returned, but span settled: %+v", records)
		}
		select {
		case <-done:
			t.Fatal("span returned before its callback settled")
		default:
		}
		close(release)
		assertTelemetryErrorIdentity(t, <-done, failure)
		records = recorder.snapshot()
		assertTelemetryErrorIdentity(t, records[0].err, failure)
		if !records[0].settled {
			t.Fatalf("callback did not settle the span with its error: %+v", records)
		}
	})
}

func TestTelemetryConcurrentSpanContextsStayIndependent(t *testing.T) {
	const requests = 128
	recorder := &telemetryRecorder{}
	ctx := harness.WithTelemetryContext(harness.BackgroundContext(), recorder)
	var workers sync.WaitGroup
	for request := range requests {
		workers.Go(func() {
			result, err := harness.StartHarnessSpan(ctx, "pi.harness.run", harness.SpanAttributes{"request": request}, func(_ harness.TelemetrySpan, child harness.Context) (int, error) {
				return harness.StartAiSpan(child, "pi.ai.request", harness.SpanAttributes{"request": request}, func(harness.TelemetrySpan, harness.Context) (int, error) {
					return request, nil
				})
			})
			if err != nil || result != request {
				t.Errorf("request %d: result=%d err=%v", request, result, err)
			}
		})
	}
	workers.Wait()
	records := recorder.snapshot()
	if len(records) != requests*2 { // Each request starts exactly one harness span and one AI child.
		t.Fatalf("recorded %d spans for %d two-span requests", len(records), requests)
	}
	for _, record := range records {
		if !record.settled {
			t.Fatal("joined callback left a pending span")
		}
		if record.name == "pi.harness.run" {
			if record.parent != -1 {
				t.Fatal("independent request acquired another request's parent")
			}
			continue
		}
		if record.parent < 0 || record.parent >= len(records) {
			t.Fatalf("AI span lost its harness parent: %+v", record)
		}
		parent := records[record.parent]
		if parent.name != "pi.harness.run" || parent.attributes["request"] != record.attributes["request"] || record.end >= parent.end {
			t.Fatalf("cross-request parent or premature settlement: parent=%+v child=%+v", parent, record)
		}
	}
}

func TestTelemetryToolHookOutcomesAndParenting(t *testing.T) {
	for _, hookName := range []agentharness.HookName{agentharness.HookBeforeTool, agentharness.HookAfterTool} {
		for _, outcome := range []string{"completed", "blocked", "failed"} {
			if hookName == agentharness.HookAfterTool && outcome == "blocked" {
				continue // after_tool cannot return a block.
			}
			t.Run(string(hookName)+"/"+outcome, func(t *testing.T) {
				recorder := &telemetryRecorder{}
				parent := harness.WithTelemetryContext(harness.BackgroundContext(), recorder)
				gate, control := execution.CreateGate()
				t.Cleanup(func() { control.Close(errors.New("test complete")) })
				failure := errors.New("hook failed")
				var reported []error
				registry := agentharness.NewHookRegistry(func(ctx harness.Context, err error, hook agentharness.HookName, lane string) error {
					if hook != hookName || lane != "main" || harness.GetTelemetryContext(ctx) != recorder {
						t.Fatal("error reporter must receive the admitted parent context")
					}
					reported = append(reported, err)
					return nil
				})
				calls := 0
				invoke := func(ctx harness.Context) error {
					calls++
					if _, ok := harness.GetTelemetryContext(ctx).(*telemetrySpan); !ok {
						t.Fatal("handler did not receive its active hook span")
					}
					_, err := harness.StartAiSpan(ctx, "pi.ai.request", harness.SpanAttributes{"pi.ai.provider": "local"}, func(_ harness.TelemetrySpan, _ harness.Context) (int, error) {
						return 7, nil
					})
					if err != nil {
						t.Fatal(err)
					}
					if outcome == "failed" {
						return failure
					}
					return nil
				}
				// The second registration has a present empty id; the first omits it.
				emptyID := ""
				for _, id := range []*string{nil, &emptyID} {
					var err error
					if hookName == agentharness.HookBeforeTool {
						_, err = registry.OnBeforeTool(func(ctx harness.Context, _ agentharness.BeforeToolEvent) (*agentharness.BeforeToolResult, error) {
							if err := invoke(ctx); err != nil {
								return nil, err
							}
							result := &agentharness.BeforeToolResult{}
							if outcome == "blocked" {
								result.Block = &agentharness.ToolBlock{Reason: "policy"}
							}
							return result, nil
						}, agentharness.HookOptions{ID: id})
					} else {
						_, err = registry.OnAfterTool(func(ctx harness.Context, _ agentharness.AfterToolEvent) (*agentharness.AfterToolResult, error) {
							return &agentharness.AfterToolResult{}, invoke(ctx)
						}, agentharness.HookOptions{ID: id})
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				scope := agentharness.HookScope{Lane: "main", RunID: "run"}
				wantCalls := 2 // One invocation for each registration, unless before_tool stops.
				if hookName == agentharness.HookBeforeTool {
					result, err := registry.RunBeforeTool(parent, gate, agentharness.BeforeToolEvent{HookScope: scope})
					if err != nil {
						t.Fatal(err)
					}
					if outcome != "completed" {
						wantCalls = 1
						if result.Block == nil {
							t.Fatal("before_tool did not block")
						}
						if outcome == "failed" && result.Block.Reason != failure.Error() {
							t.Fatal("block lost original error message")
						}
					}
				} else if _, err := registry.RunAfterTool(parent, gate, agentharness.AfterToolEvent{HookScope: scope}); err != nil {
					t.Fatal(err)
				}
				if calls != wantCalls {
					t.Fatalf("handler calls = %d, want %d", calls, wantCalls)
				}
				records := recorder.snapshot()
				if len(records) != wantCalls*2 {
					t.Fatalf("each handler should create a hook span and nested AI span: %+v", records)
				}
				for i := range wantCalls {
					hook, ai := records[i*2], records[i*2+1]
					want := harness.SpanAttributes{"pi.lane.name": "main", "pi.operation.id": "run", "pi.hook.name": string(hookName), "pi.hook.outcome": outcome}
					if i != 0 {
						want["pi.hook.registration_id"] = ""
					}
					if hook.name != "pi.harness.hook" || hook.parent != -1 || !reflect.DeepEqual(hook.attributes, want) || len(hook.events) != 0 {
						t.Fatalf("wrong hook telemetry: %+v, want attributes %v", hook, want)
					}
					if ai.name != "pi.ai.request" || ai.parent != i*2 || ai.end >= hook.end || !hook.settled || !ai.settled {
						t.Fatalf("wrong AI nesting/settlement: hook=%+v ai=%+v", hook, ai)
					}
					wantStatus := harness.SpanStatus{}
					if outcome == "failed" {
						wantStatus.Status = harness.SpanStatusCodeError
						assertTelemetryErrorIdentity(t, hook.err, failure)
					}
					if !reflect.DeepEqual(hook.status, wantStatus) {
						t.Fatalf("status = %+v, want %+v (failure is explicit, without error details)", hook.status, wantStatus)
					}
				}
				if outcome == "failed" {
					if len(reported) != wantCalls {
						t.Fatalf("reports = %v", reported)
					}
					for _, err := range reported {
						assertTelemetryErrorIdentity(t, err, failure)
					}
				} else if len(reported) != 0 {
					t.Fatalf("successful/blocked hooks reported errors: %v", reported)
				}
			})
		}
	}
}

func TestTelemetryNonToolHooksAndRefusedAdmissionDoNotTrace(t *testing.T) {
	recorder := &telemetryRecorder{}
	ctx := harness.WithTelemetryContext(harness.BackgroundContext(), recorder)
	gate, control := execution.CreateGate()
	closed := errors.New("closed")
	t.Cleanup(func() { control.Close(closed) })
	registry := agentharness.NewHookRegistry(func(harness.Context, error, agentharness.HookName, string) error { return nil })
	calls := 0
	_, err := registry.OnBeforeRun(func(child harness.Context, _ agentharness.BeforeRunEvent) (*agentharness.BeforeRunResult, error) {
		calls++
		if harness.GetTelemetryContext(child) != recorder {
			t.Fatal("before_run unexpectedly creates an active span")
		}
		return &agentharness.BeforeRunResult{}, nil
	}, agentharness.HookOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.RunBeforeRun(ctx, gate, agentharness.BeforeRunEvent{}); err != nil || calls != 1 {
		t.Fatalf("before_run failed: calls=%d, err=%v", calls, err)
	}
	_, err = registry.OnBeforeTool(func(harness.Context, agentharness.BeforeToolEvent) (*agentharness.BeforeToolResult, error) {
		t.Error("refused hook was invoked")
		return &agentharness.BeforeToolResult{}, nil
	}, agentharness.HookOptions{})
	if err != nil {
		t.Fatal(err)
	}
	control.Close(closed)
	_, err = registry.RunBeforeTool(ctx, gate, agentharness.BeforeToolEvent{})
	assertTelemetryErrorIdentity(t, err, closed)
	if records := recorder.snapshot(); len(records) != 0 {
		t.Fatalf("non-tool and refused invocations should not trace: %+v", records)
	}
}

func TestTelemetrySchemasMatchUpstream(t *testing.T) {
	// This is an executable upstream snapshot, not a hand-counted schema list.
	// Regenerate with: node testdata/telemetry-schema.mjs --write (from this package).
	oracle, err := os.ReadFile("testdata/telemetry-schema.json")
	if err != nil {
		t.Fatal(err)
	}
	actual, err := json.Marshal(harness.AgentTelemetrySchemas)
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err := json.Unmarshal(oracle, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(actual, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("schema metadata differs from upstream\ngot: %s\nwant: %s", actual, oracle)
	}
	if !reflect.DeepEqual(harness.AgentTelemetrySchemas, []harness.TelemetrySchemaDefinition{harness.AITelemetrySchema, harness.HarnessTelemetrySchema}) {
		t.Fatal("combined schemas differ from AI/harness schemas")
	}
	var schemas []struct {
		Spans json.RawMessage `json:"spans"`
	}
	if err := json.Unmarshal(oracle, &schemas); err != nil {
		t.Fatal(err)
	}
	for i, schema := range schemas {
		assertTelemetryDefinitionOrder(t, schema.Spans, harness.AgentTelemetrySchemas[i].Spans)
		var spans map[string]struct {
			Start json.RawMessage `json:"startAttributes"`
			End   json.RawMessage `json:"endAttributes"`
		}
		if err := json.Unmarshal(schema.Spans, &spans); err != nil {
			t.Fatal(err)
		}
		for name, upstream := range spans {
			span, ok := harness.AgentTelemetrySchemas[i].Spans.Get(name)
			if !ok {
				t.Fatalf("missing span %q", name)
			}
			assertTelemetryDefinitionOrder(t, upstream.Start, span.StartAttributes)
			assertTelemetryDefinitionOrder(t, upstream.End, span.EndAttributes)
		}
	}
}

func TestTelemetrySchemaOptionalMembersPreserveEmpty(t *testing.T) {
	attribute := harness.TelemetryAttributeDefinition{Type: "string"}
	empty := harness.TelemetryAttributeDefinition{Type: "string", Values: []harness.AttributeValue{}, Examples: []harness.AttributeValue{}}
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"absent", attribute, `{"description":"","type":"string"}`},
		{"present empty", empty, `{"description":"","type":"string","values":[],"examples":[]}`},
		{"array values", harness.TelemetryAttributeDefinition{Type: "string[]", ElementValues: []harness.AttributeValue{}}, `{"description":"","type":"string[]","elementValues":[]}`},
		{"empty parent spans", harness.TelemetryParentDefinition{Kind: "spans", Spans: []string{}}, `{"kind":"spans","spans":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tc.want {
				t.Fatalf("optional members = %s, want %s", data, tc.want)
			}
		})
	}
	for _, events := range []harness.TelemetryDefinitions[harness.TelemetryEventDefinition]{nil, {}} {
		data, err := json.Marshal(harness.TelemetrySpanDefinition{Events: events})
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil {
			t.Fatal(err)
		}
		value, present := object["events"]
		if present != (events != nil) || (present && string(value) != "{}") {
			t.Fatalf("empty/absent events changed: %s", data)
		}
	}
}

func assertTelemetryDefinitionOrder[T any](t *testing.T, upstream json.RawMessage, definitions harness.TelemetryDefinitions[T]) {
	t.Helper()
	actual, err := json.Marshal(definitions)
	if err != nil {
		t.Fatal(err)
	}
	want, got := telemetryDefinitionNames(t, upstream), telemetryDefinitionNames(t, actual)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("schema member order = %v, want %v", got, want)
	}
}

func telemetryDefinitionNames(t *testing.T, object json.RawMessage) []string {
	t.Helper()
	// Decode object member names without sorting them through a map.
	decoder := json.NewDecoder(bytes.NewReader(object))
	if _, err := decoder.Token(); err != nil {
		t.Fatal(err)
	}
	var names []string
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, name.(string))
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
	}
	return names
}

func BenchmarkTelemetryToolHook(b *testing.B) {
	for _, recording := range []bool{false, true} {
		name := "noop"
		if recording {
			name = "recording"
		}
		b.Run(name, func(b *testing.B) {
			registry := agentharness.NewHookRegistry(func(harness.Context, error, agentharness.HookName, string) error { return nil })
			_, err := registry.OnBeforeTool(func(ctx harness.Context, _ agentharness.BeforeToolEvent) (*agentharness.BeforeToolResult, error) {
				return harness.StartAiSpan(ctx, "pi.ai.request", nil, func(harness.TelemetrySpan, harness.Context) (*agentharness.BeforeToolResult, error) {
					return &agentharness.BeforeToolResult{}, nil
				})
			}, agentharness.HookOptions{})
			if err != nil {
				b.Fatal(err)
			}
			closed := errors.New("benchmark complete")
			ctx := harness.BackgroundContext()
			recorder := &telemetryRecorder{}
			if recording {
				ctx = harness.WithTelemetryContext(ctx, recorder)
			}
			event := agentharness.BeforeToolEvent{HookScope: agentharness.HookScope{Lane: "main", RunID: "run"}}
			b.ReportAllocs()
			for b.Loop() {
				gate, control := execution.CreateGate()
				_, err := registry.RunBeforeTool(ctx, gate, event)
				control.Close(closed)
				if err != nil {
					b.Fatal(err)
				}
				// Reuse the test recorder without retaining benchmark history.
				recorder.records = recorder.records[:0]
			}
		})
	}
}
