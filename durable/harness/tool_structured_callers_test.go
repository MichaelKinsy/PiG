package harness

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// Pi 1.2.0 test/harness-structured-output.test.ts: what callers of a tool (executeTool) get as structured output.

// returningTool is a tool that returns result, with edit such as a schema (`returning` of the upstream test).
func returningTool(name string, result durable.ToolExecutionResult, edit ...func(*durable.ToolRegistration)) *durable.ToolRegistration {
	return tlTool(name, func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return result, nil
	}, edit...)
}

// sameJSON compares two JSON texts by value: member order is not what these cases are about.
func equalJSONText(t *testing.T, got, want string) bool {
	t.Helper()
	var a, b any
	if err := json.Unmarshal([]byte(got), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(a, b)
}

func withSchema(schema map[string]any) func(*durable.ToolRegistration) {
	return func(registration *durable.ToolRegistration) { registration.StructuredOutputSchema = schema }
}

func encodedJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// harness-structured-output.test.ts:88 it("gives callers a schema-less tool's output: one text as a string, one image as itself,
// else the list")
func TestCallersGetASchemaLessToolsOutputOneTextAsAStringOneImageAsItselfElseTheList(t *testing.T) {
	text := ai.TextContent{Text: "one"}
	image := ai.ImageContent{Data: "AAAA", MimeType: "image/png"}
	for _, tc := range []struct {
		name   string
		output []ai.ToolResultMessageContent
		want   string
	}{
		{"one text", []ai.ToolResultMessageContent{text}, `"one"`},
		{"one image", []ai.ToolResultMessageContent{image}, encodedJSON(t, image)},
		{"nothing", []ai.ToolResultMessageContent{}, `""`},
		{"text and image", []ai.ToolResultMessageContent{text, image}, encodedJSON(t, []any{text, image})},
		{"two texts", []ai.ToolResultMessageContent{text, text}, encodedJSON(t, []any{text, text})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setup := chatSetup(t)
			addTool(t, setup.Registry, returningTool("plain", durable.ToolExecutionResult{Output: tc.output}))
			received := nestedReceived(t, setup, "plain")
			if got := encodedJSON(t, received.StructuredOutput); !equalJSONText(t, got, tc.want) || !received.HasStructuredOutput || received.IsError || len(received.Diagnostics) != 0 || received.DurationMs == nil {
				t.Fatalf("received %+v (structured %s), want structuredOutput %s, no error, no diagnostics", received, got, tc.want)
			}
		})
	}
}

// harness-structured-output.test.ts:118 it("gives callers the bounded output, as the model sees it, without the rendered diagnostics")
func TestCallersGetTheBoundedOutputAsTheModelSeesItWithoutTheRenderedDiagnostics(t *testing.T) {
	setup := chatSetup(t)
	addTool(t, setup.Registry, returningTool("long", durable.ToolExecutionResult{
		Output:      []ai.ToolResultMessageContent{ai.TextContent{Text: "a\nb\nc"}},
		Diagnostics: []durable.ToolDiagnostic{{Severity: durable.SeverityInfo, Message: "note"}},
	}, func(registration *durable.ToolRegistration) {
		registration.OutputLimits = &durable.ToolOutputLimits{MaxLines: new(1)}
	}))
	received := nestedReceived(t, setup, "long")
	if received.StructuredOutput != "a\n" || !slices.Equal(diagnosticCodes(received), []string{"", "truncated"}) {
		t.Fatalf("received %+v, want structuredOutput \"a\\n\" and the diagnostics note and truncated", received)
	}
}

// harness-structured-output.test.ts:187 it("gives callers a schema tool's validated structured output, which the transcript does
// not store")
func TestCallersGetASchemaToolsValidatedStructuredOutputWhichTheTranscriptDoesNotStore(t *testing.T) {
	result := durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "3 things"}}, StructuredOutput: map[string]any{"count": 3.0}, HasStructuredOutput: true}
	setup := chatSetup(t)
	addTool(t, setup.Registry, returningTool("counter", result, withSchema(countSchema)))
	received := nestedReceived(t, setup, "counter")
	if encodedJSON(t, received.StructuredOutput) != `{"count":3}` || received.IsError {
		t.Fatalf("received %+v, want {count: 3} and no error", received)
	}

	modelSetup := chatSetup(t)
	addTool(t, modelSetup.Registry, returningTool("counter", result, withSchema(countSchema)))
	run := tlRun(t, modelSetup, []ai.FauxResponseStep{tlCallsStep(tlCall{"counter", map[string]any{}, "c1"}), tlDone()}, nil)
	stored := tlResults(run.entries)
	if len(stored) != 1 || stored[0].IsError || tlResultText(stored[0]) != "3 things" {
		t.Fatalf("stored %+v, want one non-error result \"3 things\"", stored)
	}
	if encoded := encodedJSON(t, stored[0]); containsKey(encoded, "structuredOutput") {
		t.Fatalf("the stored result %s has a structuredOutput member", encoded)
	}
}

func containsKey(encoded, key string) bool {
	var object map[string]any
	_ = json.Unmarshal([]byte(encoded), &object)
	_, found := object[key]
	return found
}

// harness-structured-output.test.ts:323 it("keeps structured output when afterTool replaces the output, and validates one
// afterTool replaces")
func TestStructuredOutputSurvivesAnAfterToolThatReplacesTheOutputAndAReplacedOneIsValidated(t *testing.T) {
	setup := chatSetup(t)
	addTool(t, setup.Registry, returningTool("plain", durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "secret"}}}))
	counter := durable.ToolExecutionResult{StructuredOutput: map[string]any{"count": 1.0}, HasStructuredOutput: true}
	addTool(t, setup.Registry, returningTool("counter", counter, withSchema(countSchema)))
	addTool(t, setup.Registry, returningTool("other", counter, withSchema(countSchema)))
	addHooks(t, setup.Registry, ToolTask.Erased(), &ToolHooks{AfterTool: func(_ context.Context, call ToolHookCall, result durable.ToolExecutionResult, _ HookApi) (*durable.ToolExecutionResult, error) {
		switch call.Name {
		case "probe":
			return nil, nil
		case "other":
			result.StructuredOutput = map[string]any{"count": "broken"}
		default:
			result.Output = []ai.ToolResultMessageContent{ai.TextContent{Text: "rewritten"}}
		}
		return &result, nil
	}})
	var plain, kept, other durable.NestedToolExecutionResult
	addTool(t, setup.Registry, tlTool("probe", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		var err error
		if plain, err = api.ExecuteTool(ctx, "plain", map[string]any{}); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if kept, err = api.ExecuteTool(ctx, "counter", map[string]any{}); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		other, err = api.ExecuteTool(ctx, "other", map[string]any{})
		return durable.ToolExecutionResult{}, err
	}))
	run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"probe", map[string]any{}, "c1"}), tlDone()}, nil)
	mustClose(t, run.harness)
	// A schema-less tool's value is its output, so redacting the output redacts what programs get.
	if plain.StructuredOutput != "rewritten" {
		t.Errorf("plain structuredOutput %v, want rewritten", plain.StructuredOutput)
	}
	if encodedJSON(t, kept.StructuredOutput) != `{"count":1}` || kept.IsError {
		t.Errorf("counter %+v, want {count: 1} kept", kept)
	}
	if !other.IsError || !slices.Equal(diagnosticCodes(other), []string{"invalid_structured_output"}) {
		t.Errorf("other %+v, want an invalid_structured_output error", other)
	}
}

// harness-structured-output.test.ts:133 it("stores exactly what callers and events get, without the model's output, and keeps a
// stored null")
func TestTheHarnessStoresExactlyWhatCallersAndEventsGetAndKeepsAStoredNull(t *testing.T) {
	setup := chatSetup(t)
	addTool(t, setup.Registry, returningTool("plain", durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "a"}}}))
	addTool(t, setup.Registry, returningTool("nothing", durable.ToolExecutionResult{HasStructuredOutput: true}, withSchema(map[string]any{"type": "null"})))
	var received []durable.NestedToolExecutionResult
	addTool(t, setup.Registry, tlTool("probe", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		for _, name := range []string{"plain", "nothing"} {
			result, err := api.ExecuteTool(ctx, name, map[string]any{})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			received = append(received, result)
		}
		return durable.ToolExecutionResult{}, nil
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"probe", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	// The result each nested call's document got, as committed (4ac0bd8c: written by an update of the call's document).
	var storedMu sync.Mutex
	var stored []string
	harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
		for _, change := range publication.Changes {
			doc, ok := change.(durable.DocumentChange)
			if !ok || doc.Record.Kind != nestedCallKind || doc.Value == nil || len(doc.Ops) == 0 {
				continue
			}
			if result := doc.Value.Value("result"); result != nil {
				storedMu.Lock()
				stored = append(stored, encodedJSON(t, result))
				storedMu.Unlock()
			}
		}
	})
	stream := must(WatchEvents(testContext, harness, root.Id()))
	var mu sync.Mutex
	var ends []string
	stream.Start(func(_ context.Context, batch []AgentEvent) error {
		mu.Lock()
		defer mu.Unlock()
		for _, event := range batch {
			if end, ok := event.(ToolExecutionEndEvent); ok && end.ParentToolCallId != "" {
				ends = append(ends, encodedJSON(t, end.Result))
			}
		}
		return nil
	})
	if _, err := ownSubmit(t, root, ownInput("go")).Wait(testContext); err != nil {
		t.Fatal(err)
	}
	if err := harness.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Stop(); err != nil {
		t.Fatal(err)
	}
	mustClose(t, harness)

	if len(received) != 2 || received[0].StructuredOutput != "a" || received[0].IsError || !received[1].HasStructuredOutput || received[1].StructuredOutput != nil || received[1].IsError {
		t.Fatalf("received %+v, want \"a\" and a kept null", received)
	}
	want := []string{encodedJSON(t, received[0]), encodedJSON(t, received[1])}
	if containsKey(want[0], "output") || !strings.Contains(want[1], `"structuredOutput":null`) {
		t.Fatalf("results %v: the first has an output member or the second lost its null", want)
	}
	storedMu.Lock()
	gotStored := slices.Clone(stored)
	storedMu.Unlock()
	mu.Lock()
	gotEnds := slices.Clone(ends)
	mu.Unlock()
	if len(gotStored) != 2 || len(gotEnds) != 2 {
		t.Fatalf("stored %v, events %v, want two of each", gotStored, gotEnds)
	}
	for i := range want {
		if !equalJSONText(t, gotStored[i], want[i]) || !equalJSONText(t, gotEnds[i], want[i]) {
			t.Errorf("result %d: stored %s, event %s, callers got %s", i, gotStored[i], gotEnds[i], want[i])
		}
	}
}
