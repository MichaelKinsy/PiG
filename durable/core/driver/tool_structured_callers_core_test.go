package driver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
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

// harness-view-run.test.ts:111 over the core: a tool call's slot is listed pending with the answer that calls it, shows its
// running output, and is done with its result entry, appended in the same commit.
func TestCoreShowsAToolCallsSlotFromPendingThroughItsRunningOutputToItsResultEntry(t *testing.T) {
	gate, finish := make(chan struct{}), make(chan struct{})
	printer := nestedCoreTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		api.Output("one\n")
		for _, wait := range []chan struct{}{gate, finish} {
			select {
			case <-wait:
			case <-ctx.Done():
				return durable.ToolExecutionResult{}, ctx.Err()
			}
			if wait == gate {
				api.Output("two\n")
			}
		}
		return durable.ToolExecutionResult{}, nil
	})
	h, root, submit := nestedCoreOpen(t, &durable.Extension{Name: "s", Tools: []*durable.ToolRegistration{printer}})
	history := viewHistory(t, h, root.Id())
	sub := submit()
	// The output shows in the slot while the tool still runs.
	pollUntil(t, "the running output of the tool", func() bool {
		slot := firstSlot(slotsOf(history.Live(), "c1"), func(slot *slotView) bool { return slot.Output != nil && *slot.Output == "one\n" })
		return slot != nil
	})
	close(gate)
	pollUntil(t, "the second output of the tool", func() bool {
		return firstSlot(slotsOf(history.Live(), "c1"), func(slot *slotView) bool { return slot.Output != nil && *slot.Output == "one\ntwo\n" }) != nil
	})
	close(finish)
	nestedCoreWait(t, sub, durable.SubmissionDone)
	if err := h.WaitForIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	pollUntil(t, "the end of the run", func() bool {
		states := history.Live()
		return liveIndexAfter(states, slices.IndexFunc(states, func(state harness.LiveState) bool { return state.Run != nil }), func(state harness.LiveState) bool { return state.Run == nil && state.RunFailure == nil }) >= 0
	})
	history.Detach()
	views := history.All()
	slots := slotsOf(history.Live(), "c1")
	listed := slices.IndexFunc(slots, func(slot *slotView) bool { return slot != nil })
	// Listed pending with the tool-calling answer, in the same commit.
	if slot := slots[listed]; slot.CallId != "c1" || slot.Name != "batch" || slot.Status != harness.ToolSlotPending || slot.TaskId == nil || slot.Output != nil || slot.Entry != nil {
		t.Fatalf("slot when listed = %+v", slot)
	}
	if kind := views[listed].Entries[len(views[listed].Entries)-1].Kind; kind != "pi.assistant" {
		t.Fatalf("the slot is listed with an entry of kind %s, want pi.assistant", kind)
	}
	var outputs []string
	for _, slot := range slots {
		if slot != nil && slot.Status == harness.ToolSlotRunning {
			output := ""
			if slot.Output != nil {
				output = *slot.Output
			}
			if !slices.Contains(outputs, output) {
				outputs = append(outputs, output)
			}
		}
	}
	if want := []string{"", "one\n", "one\ntwo\n"}; !slices.Equal(outputs, want) {
		t.Fatalf("outputs of the running slot %q, want %q", outputs, want)
	}
	// Done with its result entry, appended in the same commit.
	done := slices.IndexFunc(slots, func(slot *slotView) bool { return slot != nil && slot.Status == harness.ToolSlotDone })
	last := views[done].Entries[len(views[done].Entries)-1]
	if slot := slots[done]; slot.TaskId == nil || slot.Entry == nil || *slot.Entry != last.Id || last.Kind != "pi.tool-result" {
		t.Fatalf("done slot = %+v, want the result entry %d", slot, last.Id)
	}
	// The run's two generations, the tool round and the answer, hand the run over.
	runTasks := map[durable.TaskId]bool{}
	for _, state := range history.Live() {
		if state.Run != nil {
			runTasks[state.Run.TaskId] = true
		}
	}
	if len(runTasks) != 2 {
		t.Fatalf("%d run tasks, want the tool round and the answer", len(runTasks))
	}
}

// harness-view-run.test.ts:331 over the core: a call that never runs (a tool that is not offered) is done with its result entry in
// the commit of its calling answer, and a tool aborted with its generation ends with its result entry before the run ends.
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
	history := viewHistory(t, h, root.Id())
	sub, err := root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("go")})
	if err != nil {
		t.Fatal(err)
	}
	pollUntil(t, "the running tool", func() bool {
		return slices.ContainsFunc(slotsOf(history.Live(), "c2"), func(slot *slotView) bool { return slot != nil && slot.Status == harness.ToolSlotRunning })
	})
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
	// The core delivers on its own goroutine, so a settled submission does not mean the last view was delivered.
	pollUntil(t, "the run's failure", func() bool {
		states := history.Live()
		return states[len(states)-1].RunFailure != nil
	})
	history.Detach()
	states, views := history.Live(), history.All()
	// The call not offered is listed done, with its result entry, in the commit of its calling answer.
	ghostSlots := slotsOf(states, "c1")
	listed := slices.IndexFunc(ghostSlots, func(slot *slotView) bool { return slot != nil })
	ghost := ghostSlots[listed]
	if ghost.Status != harness.ToolSlotDone {
		t.Fatalf("the call not offered is %s when listed, want done", ghost.Status)
	}
	listing := views[listed].Entries
	if kinds := []string{listing[len(listing)-2].Kind, listing[len(listing)-1].Kind}; !slices.Equal(kinds, []string{"pi.assistant", "pi.tool-result"}) {
		t.Fatalf("the call not offered is listed with entries %v", kinds)
	}
	if ghost.Entry == nil || *ghost.Entry != listing[len(listing)-1].Id {
		t.Fatalf("the call not offered names entry %v, want %d", ghost.Entry, listing[len(listing)-1].Id)
	}
	// The aborted tool ends with its result entry, then the run ends without an answer.
	waitSlots := slotsOf(states, "c2")
	done := slices.IndexFunc(waitSlots, func(slot *slotView) bool { return slot != nil && slot.Status == harness.ToolSlotDone })
	if done < 0 || waitSlots[done].Entry == nil || *waitSlots[done].Entry != views[done].Entries[len(views[done].Entries)-1].Id {
		t.Fatalf("the aborted tool's done slot = %+v", waitSlots[done])
	}
	failure := states[len(states)-1].RunFailure
	if failure == nil || failure.Reason != harness.RunAborted || !slices.Equal(failure.Inputs, []durable.SubmissionId{sub.Id()}) {
		t.Fatalf("runFailure = %+v, want aborted for the submission", failure)
	}
}
