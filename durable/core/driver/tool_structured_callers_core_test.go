package driver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Pi 42a3497d harness-structured-output.test.ts over the core (driver.OpenHarness): what callers of a tool get as structured
// output. The harness path has the same cases (durable/harness/tool_structured_callers_test.go).

var coreCountSchema = map[string]any{"type": "object", "properties": map[string]any{"count": map[string]any{"type": "number"}}, "required": []any{"count"}}

func coreReturning(name string, result durable.ToolExecutionResult, edit ...func(*durable.ToolRegistration)) *durable.ToolRegistration {
	tool := nestedCoreTool(name, func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return result, nil
	})
	for _, change := range edit {
		change(tool)
	}
	return tool
}

// coreProbe runs the named tools as nested calls of "batch" in order and returns what the caller received.
func coreProbe(t *testing.T, extension *durable.Extension, names ...string) []durable.NestedToolExecutionResult {
	t.Helper()
	var received []durable.NestedToolExecutionResult
	extension.Tools = append(extension.Tools, nestedCoreTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		for _, name := range names {
			result, err := api.ExecuteTool(ctx, name, map[string]any{})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			received = append(received, result)
		}
		return durable.ToolExecutionResult{}, nil
	}))
	_, _, sub := nestedCoreHarnessWith(t, extension)
	nestedCoreWait(t, sub, durable.SubmissionDone)
	return received
}

func coreJSON(t *testing.T, value any) any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func coreCodes(result durable.NestedToolExecutionResult) []string {
	codes := []string{}
	for _, diagnostic := range result.Diagnostics {
		codes = append(codes, diagnostic.Code)
	}
	return codes
}

// harness-structured-output.test.ts:87.
func TestCoreCallersGetASchemaLessToolsOutputOneTextAsAStringOneImageAsItselfElseTheList(t *testing.T) {
	text := ai.TextContent{Text: "one"}
	image := ai.ImageContent{Data: "AAAA", MimeType: "image/png"}
	for _, tc := range []struct {
		name   string
		output []ai.ToolResultMessageContent
		want   any
	}{
		{"one text", []ai.ToolResultMessageContent{text}, "one"},
		{"one image", []ai.ToolResultMessageContent{image}, coreJSON(t, image)},
		{"nothing", []ai.ToolResultMessageContent{}, ""},
		{"text and image", []ai.ToolResultMessageContent{text, image}, coreJSON(t, []any{text, image})},
		{"two texts", []ai.ToolResultMessageContent{text, text}, coreJSON(t, []any{text, text})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			received := coreProbe(t, &durable.Extension{Name: "s", Tools: []*durable.ToolRegistration{coreReturning("plain", durable.ToolExecutionResult{Output: tc.output})}}, "plain")
			if len(received) != 1 || !received[0].HasStructuredOutput || !reflect.DeepEqual(coreJSON(t, received[0].StructuredOutput), tc.want) || received[0].IsError || len(received[0].Diagnostics) != 0 {
				t.Fatalf("received %+v, want structuredOutput %v, no error, no diagnostics", received, tc.want)
			}
		})
	}
}

// harness-structured-output.test.ts:117.
func TestCoreCallersGetTheBoundedOutputWithoutTheRenderedDiagnostics(t *testing.T) {
	long := coreReturning("long", durable.ToolExecutionResult{
		Output:      []ai.ToolResultMessageContent{ai.TextContent{Text: "a\nb\nc"}},
		Diagnostics: []durable.ToolDiagnostic{{Severity: durable.SeverityInfo, Message: "note"}},
	}, func(registration *durable.ToolRegistration) {
		registration.OutputLimits = &durable.ToolOutputLimits{MaxLines: new(1)}
	})
	received := coreProbe(t, &durable.Extension{Name: "s", Tools: []*durable.ToolRegistration{long}}, "long")
	if len(received) != 1 || received[0].StructuredOutput != "a\n" || !slices.Equal(coreCodes(received[0]), []string{"", "truncated"}) {
		t.Fatalf("received %+v, want structuredOutput \"a\\n\" and the diagnostics note and truncated", received)
	}
}

// harness-structured-output.test.ts:187 (the nested half).
func TestCoreCallersGetASchemaToolsValidatedStructuredOutput(t *testing.T) {
	counter := coreReturning("counter", durable.ToolExecutionResult{
		Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "3 things"}}, StructuredOutput: map[string]any{"count": 3.0}, HasStructuredOutput: true,
	}, func(registration *durable.ToolRegistration) { registration.StructuredOutputSchema = coreCountSchema })
	received := coreProbe(t, &durable.Extension{Name: "s", Tools: []*durable.ToolRegistration{counter}}, "counter")
	if len(received) != 1 || !reflect.DeepEqual(coreJSON(t, received[0].StructuredOutput), map[string]any{"count": 3.0}) || received[0].IsError {
		t.Fatalf("received %+v, want {count: 3} and no error", received)
	}
}

// harness-structured-output.test.ts:255.
func TestCoreStructuredOutputSurvivesAnAfterToolThatReplacesTheOutputAndAReplacedOneIsValidated(t *testing.T) {
	var mu sync.Mutex
	counter := durable.ToolExecutionResult{StructuredOutput: map[string]any{"count": 1.0}, HasStructuredOutput: true}
	schema := func(registration *durable.ToolRegistration) { registration.StructuredOutputSchema = coreCountSchema }
	extension := &durable.Extension{
		Name: "s",
		Tools: []*durable.ToolRegistration{
			coreReturning("plain", durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "secret"}}}),
			coreReturning("counter", counter, schema),
			coreReturning("other", counter, schema),
		},
		Hooks: []durable.HookRegistration{{Task: harness.ToolTask.AnyDefinition().Name, Handlers: &harness.ToolHooks{
			AfterTool: func(_ context.Context, call harness.ToolHookCall, result durable.ToolExecutionResult, _ harness.HookApi) (*durable.ToolExecutionResult, error) {
				mu.Lock()
				defer mu.Unlock()
				switch call.Name {
				case "batch":
					return nil, nil
				case "other":
					result.StructuredOutput = map[string]any{"count": "broken"}
				default:
					result.Output = []ai.ToolResultMessageContent{ai.TextContent{Text: "rewritten"}}
				}
				return &result, nil
			},
		}}},
	}
	received := coreProbe(t, extension, "plain", "counter", "other")
	if len(received) != 3 {
		t.Fatalf("%d results, want 3", len(received))
	}
	// A schema-less tool's value is its output, so redacting the output redacts what programs get.
	if received[0].StructuredOutput != "rewritten" {
		t.Errorf("plain structuredOutput %v, want rewritten", received[0].StructuredOutput)
	}
	if !reflect.DeepEqual(coreJSON(t, received[1].StructuredOutput), map[string]any{"count": 1.0}) || received[1].IsError {
		t.Errorf("counter %+v, want {count: 1} kept", received[1])
	}
	if !received[2].IsError || !slices.Equal(coreCodes(received[2]), []string{"invalid_structured_output"}) {
		t.Errorf("other %+v, want an invalid_structured_output error", received[2])
	}
}

// The resolved agent carries a tool's outputLimits, so a model-issued call's stored result is bounded too (tool.ts finalResult
// bounds the content; this was red before agentJSON sent outputLimits to the core).
func TestCoreABoundedToolsModelIssuedResultIsTruncatedInTheTranscript(t *testing.T) {
	batch := nestedCoreTool("batch", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return nestedCoreText("a\nb\nc"), nil
	})
	batch.OutputLimits = &durable.ToolOutputLimits{MaxLines: new(1)}
	_, root, sub := nestedCoreHarnessWith(t, &durable.Extension{Name: "s", Tools: []*durable.ToolRegistration{batch}})
	nestedCoreWait(t, sub, durable.SubmissionDone)
	page, err := root.Entries(t.Context(), durable.EntryQuery{}, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, entry := range page.Items {
		if entry.Kind != "pi.tool-result" {
			continue
		}
		for _, message := range entry.Model {
			if result, ok := message.(ai.ToolResultMessage); ok {
				for _, content := range result.Content {
					if text, ok := content.(ai.TextContent); ok {
						texts = append(texts, text.Text)
					}
				}
			}
		}
	}
	if len(texts) == 0 || !strings.HasPrefix(texts[0], "a\n") || strings.Contains(texts[0], "b\nc") {
		t.Fatalf("stored tool result texts %q, want the output bounded to its first line", texts)
	}
}

// harness-callers.test.ts:61 over the core: the model is offered only tools it may call, and tools call only tools they may
// call (the core reads the resolved agent's tools and callable lists).
func TestCoreTheModelIsOfferedOnlyToolsItMayCallAndToolsCallOnlyToolsTheyMayCall(t *testing.T) {
	ran := func(name string, callers ...durable.ToolCaller) *durable.ToolRegistration {
		tool := coreReturning(name, nestedCoreText(name+" ran"))
		tool.Callers = callers
		return tool
	}
	extension := &durable.Extension{Name: "s", Tools: []*durable.ToolRegistration{ran("script", durable.CallerModel), ran("hidden", durable.CallerTools), ran("both")}}
	received := coreProbe(t, extension, "hidden", "both")
	if len(received) != 2 || received[0].IsError || received[0].StructuredOutput != "hidden ran" || received[1].IsError || received[1].StructuredOutput != "both ran" {
		t.Fatalf("received %+v, want hidden and both to run", received)
	}
	// A tool only the model may call is unavailable to a nested call.
	var codes []string
	extension2 := &durable.Extension{Name: "s2", Tools: []*durable.ToolRegistration{ran("script", durable.CallerModel)}}
	extension2.Tools = append(extension2.Tools, nestedCoreTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		result, err := api.ExecuteTool(ctx, "script", map[string]any{})
		codes = coreCodes(result)
		return durable.ToolExecutionResult{}, err
	}))
	_, _, sub := nestedCoreHarnessWith(t, extension2)
	nestedCoreWait(t, sub, durable.SubmissionDone)
	if len(codes) == 0 || codes[0] != "tool_unavailable" {
		t.Fatalf("script nested call codes %v, want tool_unavailable", codes)
	}
}

// coreAwaitRunEnd waits until the stream has delivered the run's end event. The core delivers a stream's batches on its own
// goroutine, so a finished submission and an idle harness do not mean the events are delivered, and Stop ends the batches
// still queued: stopping first loses the tail of the run.
func coreAwaitRunEnd(t *testing.T, mu *sync.Mutex, events *[]harness.AgentEvent) {
	t.Helper()
	pollUntil(t, "the run_end event", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.ContainsFunc(*events, func(event harness.AgentEvent) bool { return event.EventType() == "run_end" })
	})
}

// harness-events.test.ts:106 and :154 over the core: a run streams as lifecycle events, and a tool run reports its start, its
// output and its result entry before the result message, in two turns.
func TestCoreStreamsTheLifecycleAndTheToolEventsOfARun(t *testing.T) {
	gate := make(chan struct{})
	printer := nestedCoreTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		api.Output("one\n")
		select {
		case <-gate:
		case <-ctx.Done():
			return durable.ToolExecutionResult{}, ctx.Err()
		}
		api.Output("two\n")
		return durable.ToolExecutionResult{}, nil
	})
	h, root, submit := nestedCoreOpen(t, &durable.Extension{Name: "s", Tools: []*durable.ToolRegistration{printer}})
	var mu sync.Mutex
	var events []harness.AgentEvent
	stream, err := harness.WatchEvents(t.Context(), h, root.Id())
	if err != nil {
		t.Fatal(err)
	}
	stream.Start(func(_ context.Context, batch []harness.AgentEvent) error {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, batch...)
		return nil
	})
	sub := submit()
	// The output events rebuild the retained window while the tool still runs.
	deadline := time.Now().Add(20 * time.Second)
	for {
		mu.Lock()
		updates := 0
		for _, event := range events {
			if event.EventType() == "tool_execution_update" {
				updates++
			}
		}
		mu.Unlock()
		if updates > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no tool_execution_update while the tool ran")
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(gate)
	nestedCoreWait(t, sub, durable.SubmissionDone)
	if err := h.WaitForIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	coreAwaitRunEnd(t, &mu, &events)
	if _, err := stream.Stop(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	var types []string
	for _, event := range events {
		if kind := event.EventType(); len(types) == 0 || types[len(types)-1] != kind {
			types = append(types, kind)
		}
	}
	for _, want := range []string{"submission", "run_start", "turn_start", "tool_execution_start", "tool_execution_update", "tool_execution_end", "turn_end", "run_end"} {
		if !slices.Contains(types, want) {
			t.Errorf("event types %v lack %s", types, want)
		}
	}
	endIndex := slices.IndexFunc(events, func(event harness.AgentEvent) bool { return event.EventType() == "tool_execution_end" })
	if endIndex < 0 || endIndex+2 >= len(events) || events[endIndex+1].EventType() != "message_start" || events[endIndex+2].EventType() != "message_end" {
		t.Fatalf("event types %v: the tool must end directly before its result message", types)
	}
	if end, ok := events[endIndex].(harness.ToolExecutionEndEvent); !ok || end.ToolCallId != "c1" || end.Entry == nil || end.Entry.Kind != "pi.tool-result" {
		t.Fatalf("end event %+v, want call c1 with a pi.tool-result entry", events[endIndex])
	}
	starts, ends := 0, 0
	for _, event := range events {
		switch event.EventType() {
		case "turn_start":
			starts++
		case "turn_end":
			ends++
		}
	}
	if starts != 2 || ends != 2 {
		t.Fatalf("turns %d/%d, want the tool round and the answer", starts, ends)
	}
}

// harness-events.test.ts:404 over the core: a call that never runs (a tool that is not offered) ends after its calling message and
// directly before its result message, and a tool aborted with its generation ends with its aborted result.
func TestCoreEndsACallThatNeverRunsAndAToolAbortedWithItsGeneration(t *testing.T) {
	wait := nestedCoreTool("wait", func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		<-ctx.Done()
		return durable.ToolExecutionResult{}, context.Cause(ctx)
	})
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	registry := harness.CreateRegistry()
	if err := registry.Install(&durable.Extension{Name: "s", Tools: []*durable.ToolRegistration{wait}}); err != nil {
		t.Fatal(err)
	}
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{
		ai.FauxToolCall("ghost", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"}),
		ai.FauxToolCall("wait", map[string]any{}, &ai.FauxToolCallOptions{ID: "c2"}),
	}, StopReason: "toolUse"})})
	h, err := driver.OpenHarness(t.Context(), filepath.Join(t.TempDir(), "store.db"), harness.HarnessOptions{Models: models, Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	root, err := h.Root(t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		t.Fatal(err)
	}
	h.Resume()
	var mu sync.Mutex
	var events []harness.AgentEvent
	stream, err := harness.WatchEvents(t.Context(), h, root.Id())
	if err != nil {
		t.Fatal(err)
	}
	stream.Start(func(_ context.Context, batch []harness.AgentEvent) error {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, batch...)
		return nil
	})
	sub, err := root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("go")})
	if err != nil {
		t.Fatal(err)
	}
	started := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.ContainsFunc(events, func(event harness.AgentEvent) bool { return event.EventType() == "tool_execution_start" })
	}
	for deadline := time.Now().Add(20 * time.Second); !started(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no tool_execution_start")
		}
	}
	// Aborting the generation aborts its round: the tool ends with its aborted result first (spec 8.5).
	live, err := durable.Snapshot(t.Context(), h, harness.LiveDoc, root.Id())
	if err != nil || live == nil || live.Run == nil {
		t.Fatalf("live state %+v, %v, want a run", live, err)
	}
	if _, err := h.AbortTask(t.Context(), live.Run.TaskId); err != nil {
		t.Fatal(err)
	}
	// The abort settles the submission within a second here. A hang (CI 38963134950 attempt 2: the tool's run invocation
	// never ended after its effect was cancelled, 1 in ~1000 under -race with 2 processors) fails here with the live
	// task records instead of the package's 10-minute timeout, so a CI hit is data for the root cause.
	settle, cancelSettle := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancelSettle()
	if _, err := sub.Wait(settle); err != nil {
		var dump strings.Builder
		if inspection, ierr := h.Inspect(context.Background()); ierr == nil {
			for _, task := range inspection.Tasks {
				fmt.Fprintf(&dump, "\n  task %d %s status=%s abortRequested=%v abortReason=%q state=%+v", task.Record.Id, task.Record.Kind, task.Record.State.Status, task.Record.AbortRequested, task.Record.AbortReason, task.Record.State)
			}
		} else {
			fmt.Fprintf(&dump, "\n  inspect: %v", ierr)
		}
		t.Fatalf("the aborted generation's submission did not settle: %v; live tasks:%s", err, dump.String())
	}
	if err := h.WaitForIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	coreAwaitRunEnd(t, &mu, &events)
	if _, err := stream.Stop(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	var round []string
	var tool []string
	for _, event := range events {
		switch typed := event.(type) {
		case harness.MessageEndEvent:
			round = append(round, "end:"+typed.Entry.Kind)
		case harness.MessageStartEvent:
			round = append(round, "start:"+messageRole(typed.Message))
		default:
			round = append(round, event.EventType())
		}
		switch typed := event.(type) {
		case harness.ToolExecutionStartEvent:
			tool = append(tool, "tool_execution_start "+typed.ToolCallId)
		case harness.ToolExecutionEndEvent:
			tool = append(tool, "tool_execution_end "+typed.ToolCallId+" entry="+strconv.FormatBool(typed.Entry != nil))
		}
	}
	ghostEnd := slices.Index(round, "tool_execution_end")
	if ghostEnd < 1 || ghostEnd+3 >= len(round) || !slices.Equal(round[ghostEnd-1:ghostEnd+3], []string{"end:pi.assistant", "tool_execution_end", "start:toolResult", "end:pi.tool-result"}) {
		t.Fatalf("round %v: the call that never ran must end after its calling message and directly before its result", round)
	}
	if want := []string{"tool_execution_end c1 entry=true", "tool_execution_start c2", "tool_execution_end c2 entry=true"}; !slices.Equal(tool, want) {
		t.Fatalf("tool events %v, want %v", tool, want)
	}
}

func messageRole(message ai.Message) string {
	switch message.(type) {
	case ai.ToolResultMessage:
		return "toolResult"
	case ai.AssistantMessage:
		return "assistant"
	case ai.UserMessage:
		return "user"
	}
	return "?"
}
