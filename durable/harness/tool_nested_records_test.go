package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// Pi 42a3497d harness/tool.ts: what nested calls store, and the results callers get in the cases the scheduler or the
// structured output contract decides.

// A running nested call's slot keeps the member order startNestedCall writes (tool.ts:685-693): callId, parentCallId,
// parentTaskId, name, taskId, arguments, status. A Pi probe of the same run stores
// {"callId":"c1/1","parentCallId":"c1","parentTaskId":12,"name":"slow","taskId":14,"arguments":{"text":"x"},"status":"running"}.
func TestARunningNestedCallSlotKeepsPiMemberOrder(t *testing.T) {
	setup := chatSetup(t)
	running, release := make(chan struct{}), make(chan struct{})
	addTool(t, setup.Registry, tlTool("slow", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		close(running)
		<-release
		return durable.ToolExecutionResult{}, nil
	}))
	var stored string
	var caller, child durable.TaskId
	run := nestedBatch(t, setup, func(ctx context.Context, api durable.ToolExecutionApi) error {
		done := make(chan error, 1)
		go func() {
			result, err := api.ExecuteTool(ctx, "slow", map[string]any{"text": "x"})
			child = result.TaskId
			done <- err
		}()
		<-running
		caller = api.TaskId()
		_, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
			live, err := docDraft(tx, LiveDoc, api.ConversationId())
			if err != nil {
				return nil, err
			}
			encoded, err := json.Marshal(live.Snapshot().Value("nestedTools"))
			stored = string(encoded)
			return nil, err
		})
		close(release)
		if doneErr := <-done; err == nil {
			err = doneErr
		}
		return err
	})
	want := fmt.Sprintf(`[{"callId":"c1/1","parentCallId":"c1","parentTaskId":%d,"name":"slow","taskId":%d,"arguments":{"text":"x"},"status":"running"}]`, caller, child)
	if run.status != durable.SubmissionDone || stored != want {
		t.Fatalf("status %s, stored nestedTools\n%s\nwant\n%s", run.status, stored, want)
	}
	mustClose(t, run.harness)
}

// A tool that is not replay-safe never resumes, so its nested calls and the task-owned children it creates through its
// api are marked abandonOnRestart (tool.ts:338, 360; harness-nested-tools-restart.test.ts:395), unless createTask's
// options say false: Pi spreads them over the default ({ abandonOnRestart: true, ...options }). A replay-safe caller's
// are not marked (harness-nested-tools-restart.test.ts:457).
func TestNestedCallsAndChildTasksOfACallerThatNeverResumesAreAbandonedOnRestart(t *testing.T) {
	type childInput struct{}
	child := durable.DefineTask(durable.TaskDefinition[childInput, stepState, durable.JsonValue, any]{
		Name:    "test.child",
		Version: 1,
		Initial: func(childInput) stepState { return stepState{Phase: "done"} },
		Phases: map[string]durable.PhaseHandler[childInput, stepState, durable.JsonValue, any]{
			"done": func(ctx context.Context, _ durable.RunningTask[childInput, stepState, durable.JsonValue], runtime durable.TaskRuntime[childInput, stepState, durable.JsonValue, any]) error {
				return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[childInput, stepState, durable.JsonValue]) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return completed[stepState, durable.JsonValue](nil), nil
				})
			},
		},
		Abort: tkNoopAbort[childInput, stepState, durable.JsonValue, any],
	})
	for _, tc := range []struct {
		name   string
		replay durable.ToolReplay
		want   string
	}{
		{"replay-unsafe", "", "nested true, default true, explicit false false"},
		{"replay-safe", durable.ReplaySafe, "nested false, default false, explicit false false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setup := chatSetup(t)
			addTool(t, setup.Registry, echoRegistration())
			addTask(t, setup.Registry, child.Erased())
			var got string
			addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				nested, err := api.ExecuteTool(ctx, "echo", map[string]any{"text": "a"})
				if err != nil {
					return durable.ToolExecutionResult{}, err
				}
				owned := durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: api.TaskId()}}
				byDefault, err := api.CreateTaskErased(ctx, child.Erased(), durable.JsonValue(nil), owned)
				if err != nil {
					return durable.ToolExecutionResult{}, err
				}
				owned.AbandonOnRestart = new(false)
				explicit, err := api.CreateTaskErased(ctx, child.Erased(), durable.JsonValue(nil), owned)
				if err != nil {
					return durable.ToolExecutionResult{}, err
				}
				var marks []bool
				for _, id := range []durable.TaskId{nested.TaskId, byDefault, explicit} {
					record, err := api.GetTask(ctx, id)
					if err != nil || record == nil {
						return durable.ToolExecutionResult{}, fmt.Errorf("task %d: %w", id, err)
					}
					marks = append(marks, record.AbandonOnRestart)
				}
				got = fmt.Sprintf("nested %t, default %t, explicit false %t", marks[0], marks[1], marks[2])
				return durable.ToolExecutionResult{}, nil
			}, func(tool *durable.ToolRegistration) { tool.Replay = tc.replay }))
			run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()}, nil)
			if run.status != durable.SubmissionDone || got != tc.want {
				t.Fatalf("status %s, abandonOnRestart: %s, want %s", run.status, got, tc.want)
			}
			mustClose(t, run.harness)
		})
	}
}

// harness-nested-tools.test.ts:1133 it("gives callers a result with its task ID and only diagnostics for a nested call the
// scheduler faulted"): a nested call the scheduler faulted stored no result, so the caller gets one with its
// task ID and only a "faulted" diagnostic (tool.ts fallbackResult). Details that are not JSON make the result commit fail.
func TestACallerGetsAFallbackResultForANestedCallTheSchedulerFaulted(t *testing.T) {
	setup := chatSetup(t)
	addTool(t, setup.Registry, tlTool("echo", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return durable.ToolExecutionResult{Details: math.NaN(), HasDetails: true}, nil
	}))
	var result durable.NestedToolExecutionResult
	run := nestedBatch(t, setup, func(ctx context.Context, api durable.ToolExecutionApi) error {
		var err error
		result, err = api.ExecuteTool(ctx, "echo", map[string]any{"text": "a"})
		return err
	})
	tasks := tlToolTasks(tlScanTasks(t, run.harness, run.root.Id()))
	if len(tasks) != 2 || !strings.Contains(nestedOutcome(t, tasks[1]), `"status":"faulted"`) {
		t.Fatalf("tool tasks %d, nested state %s", len(tasks), nestedOutcome(t, tasks[len(tasks)-1]))
	}
	if result.TaskId != tasks[1].Id || !result.IsError || result.HasStructuredOutput || result.HasDetails || result.Usage != nil || result.DurationMs != nil ||
		len(result.Diagnostics) != 1 || result.Diagnostics[0].Severity != durable.SeverityError || result.Diagnostics[0].Code != "faulted" ||
		!strings.HasPrefix(result.Diagnostics[0].Message, "Tool echo failed: ") {
		t.Fatalf("result = %+v", result)
	}
	mustClose(t, run.harness)
}

var countSchema = map[string]any{
	"type":       "object",
	"properties": map[string]any{"count": map[string]any{"type": "number"}},
	"required":   []any{"count"},
}

// nestedReceived runs tool name as a nested call of a probe tool and returns what the probe received
// (harness-structured-output.test.ts:51-70).
func nestedReceived(t *testing.T, setup *chatState, name string) durable.NestedToolExecutionResult {
	t.Helper()
	var received durable.NestedToolExecutionResult
	run := nestedBatch(t, setup, func(ctx context.Context, api durable.ToolExecutionApi) error {
		var err error
		received, err = api.ExecuteTool(ctx, name, map[string]any{})
		return err
	})
	mustClose(t, run.harness)
	return received
}

func diagnosticCodes(result durable.NestedToolExecutionResult) []string {
	codes := []string{}
	for _, diagnostic := range result.Diagnostics {
		codes = append(codes, diagnostic.Code)
	}
	return codes
}

// harness-structured-output.test.ts:200 it("turns structured output that breaks the schema into an error result for callers, and
// a report for the model's calls"): for callers an error result without the broken value (a schema-less tool's error result
// still carries its output, here ""), with one invalid_structured_output diagnostic; the model's call stands and the host
// gets a report.
func TestANestedCallsBrokenStructuredOutputIsAnErrorResult(t *testing.T) {
	for _, tc := range []struct {
		name    string
		result  func() durable.ToolExecutionResult
		schema  bool
		message string
	}{
		{"wrong", func() durable.ToolExecutionResult {
			value, _ := durable.ToJsonValue(map[string]any{"count": "three"})
			return durable.ToolExecutionResult{StructuredOutput: value, HasStructuredOutput: true}
		}, true, "does not match its schema: count:"},
		{"missing", func() durable.ToolExecutionResult {
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}
		}, true, "returned no structuredOutput"},
		{"undeclared", func() durable.ToolExecutionResult {
			value, _ := durable.ToJsonValue(map[string]any{"count": 3.0})
			return durable.ToolExecutionResult{StructuredOutput: value, HasStructuredOutput: true}
		}, false, "declares no structuredOutputSchema"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := func() *durable.ToolRegistration {
				return tlTool(tc.name, func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
					return tc.result(), nil
				}, func(tool *durable.ToolRegistration) {
					if tc.schema {
						tool.StructuredOutputSchema = countSchema
					}
				})
			}
			setup := chatSetup(t)
			addTool(t, setup.Registry, tool())
			received := nestedReceived(t, setup, tc.name)
			if !received.IsError {
				t.Fatalf("received %+v, want an error result", received)
			}
			if tc.schema && received.HasStructuredOutput {
				t.Fatalf("structuredOutput %v kept, want none", received.StructuredOutput)
			}
			if !tc.schema && (!received.HasStructuredOutput || received.StructuredOutput != "") {
				t.Fatalf("structuredOutput %v (%t), want the empty output", received.StructuredOutput, received.HasStructuredOutput)
			}
			if codes := diagnosticCodes(received); len(codes) != 1 || codes[0] != "invalid_structured_output" || !strings.Contains(received.Diagnostics[0].Message, tc.message) {
				t.Fatalf("diagnostics %+v, want one invalid_structured_output containing %q", received.Diagnostics, tc.message)
			}

			// The model never sees structured output: its call stands, and the host gets a report.
			modelSetup := chatSetup(t)
			addTool(t, modelSetup.Registry, tool())
			run := tlRun(t, modelSetup, []ai.FauxResponseStep{tlCallsStep(tlCall{tc.name, map[string]any{}, "c1"}), tlDone()}, nil)
			mustClose(t, run.harness)
			stored := tlResults(run.entries)
			if len(stored) == 0 || stored[0].IsError || strings.Contains(encodedJSON(t, stored[0].Content), tc.message) {
				t.Fatalf("stored %+v, want a non-error result without %q", stored, tc.message)
			}
			messages := []string{}
			for _, report := range modelSetup.Reports.all() {
				messages = append(messages, report.Error())
			}
			if !strings.Contains(strings.Join(messages, "\n"), tc.message) {
				t.Fatalf("reports %q, want one containing %q", messages, tc.message)
			}
		})
	}
}

// harness-structured-output.test.ts:227, 233 and 240: error results and structured output for callers.
func TestNestedErrorResultsAndStructuredOutput(t *testing.T) {
	yes := true
	// harness-structured-output.test.ts:227 it("gives callers the output of an error result of a tool without a schema")
	t.Run("gives callers the output of an error result of a tool without a schema", func(t *testing.T) {
		setup := chatSetup(t)
		addTool(t, setup.Registry, tlTool("bad", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "bad"}}, IsError: &yes}, nil
		}))
		if received := nestedReceived(t, setup, "bad"); !received.IsError || received.StructuredOutput != "bad" {
			t.Errorf("schema-less error result: %+v, want isError and structuredOutput \"bad\"", received)
		}
	})

	// harness-structured-output.test.ts:233 it("gives callers no structured output for an error result the Harness wrote, only
	// diagnostics")
	t.Run("gives callers no structured output for an error result the Harness wrote, only diagnostics", func(t *testing.T) {
		setup := chatSetup(t)
		if received := nestedReceived(t, setup, "missing"); !received.IsError || received.HasStructuredOutput || strings.Join(diagnosticCodes(received), ",") != "tool_unavailable" {
			t.Errorf("unavailable tool: %+v, want isError, no structuredOutput, tool_unavailable", received)
		}
	})

	failing := func(result durable.ToolExecutionResult) *durable.ToolRegistration {
		return tlTool("fails", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return result, nil
		}, func(tool *durable.ToolRegistration) { tool.StructuredOutputSchema = countSchema })
	}
	nope := durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "nope"}}, IsError: &yes}

	// harness-structured-output.test.ts:240 it("gives callers a schema tool's error text as a diagnostic, never as structured
	// output")
	t.Run("gives callers a schema tool's error text as a diagnostic, never as structured output", func(t *testing.T) {
		// Its text reaches the caller as an error diagnostic: structuredOutput is only ever the tool's own value.
		setup := chatSetup(t)
		addTool(t, setup.Registry, failing(nope))
		received := nestedReceived(t, setup, "fails")
		if !received.IsError || received.HasStructuredOutput || len(received.Diagnostics) != 1 ||
			received.Diagnostics[0] != (durable.ToolDiagnostic{Severity: durable.SeverityError, Code: "tool_error", Message: "nope"}) {
			t.Errorf("schema tool error result: %+v, want isError, no structuredOutput, one tool_error diagnostic \"nope\"", received)
		}

		// The model's call keeps the text in its output, without the extra diagnostic.
		setup = chatSetup(t)
		addTool(t, setup.Registry, failing(nope))
		run := tlRun(t, setup, []ai.FauxResponseStep{tlCallsStep(tlCall{"fails", map[string]any{}, "c1"}), tlDone()}, nil)
		if results := tlResults(run.entries); len(results) != 1 || encodedJSON(t, results[0].Content) != `[{"type":"text","text":"nope"}]` {
			t.Errorf("model results %+v, want the content [text nope]", results)
		}
		mustClose(t, run.harness)

		// A thrown error's message is in its tool_error diagnostic, not in the output: one diagnostic, no structured output.
		setup = chatSetup(t)
		addTool(t, setup.Registry, tlTool("throws", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{}, errors.New("permission denied")
		}, func(tool *durable.ToolRegistration) { tool.StructuredOutputSchema = countSchema }))
		thrown := nestedReceived(t, setup, "throws")
		if !thrown.IsError || thrown.HasStructuredOutput || len(thrown.Diagnostics) != 1 || thrown.Diagnostics[0].Code != "tool_error" || thrown.Diagnostics[0].Message != "permission denied" {
			t.Errorf("thrown error result: %+v, want one tool_error diagnostic \"permission denied\" and no structuredOutput", thrown)
		}

		// An error result whose own diagnostics say what went wrong gets no second one from its output.
		setup = chatSetup(t)
		own := durable.ToolDiagnostic{Severity: durable.SeverityError, Code: "quota", Message: "quota exceeded"}
		addTool(t, setup.Registry, failing(durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "partial log"}}, IsError: &yes, Diagnostics: []durable.ToolDiagnostic{own}}))
		if explained := nestedReceived(t, setup, "fails"); len(explained.Diagnostics) != 1 || explained.Diagnostics[0] != own {
			t.Errorf("explained error result diagnostics %+v, want only %+v", explained.Diagnostics, own)
		}

		// An error result that has structured output keeps it.
		setup = chatSetup(t)
		value, _ := durable.ToJsonValue(map[string]any{"count": 0.0})
		addTool(t, setup.Registry, failing(durable.ToolExecutionResult{Output: nope.Output, StructuredOutput: value, HasStructuredOutput: true, IsError: &yes}))
		if kept := nestedReceived(t, setup, "fails"); !kept.IsError || !kept.HasStructuredOutput || encodedJSON(t, kept.StructuredOutput) != `{"count":0}` {
			t.Errorf("kept: %+v, want isError and structuredOutput {count: 0}", kept)
		}
	})

	// PiG regressions beyond the Pi file (docs/bug-ledger.json), on the same finalResult rules (tool.ts).
	t.Run("joins the text of several items into one diagnostic, one item per line", func(t *testing.T) {
		setup := chatSetup(t)
		addTool(t, setup.Registry, failing(durable.ToolExecutionResult{IsError: &yes, Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "first"}, ai.ImageContent{Data: "x", MimeType: "image/png"}, ai.TextContent{Text: "second"}}}))
		if several := nestedReceived(t, setup, "fails"); len(several.Diagnostics) != 1 || several.Diagnostics[0].Message != "first\nsecond" {
			t.Errorf("several items: %+v, want one diagnostic \"first\\nsecond\"", several.Diagnostics)
		}
	})
	t.Run("adds no diagnostic from the output of an error result that has structured output", func(t *testing.T) {
		setup := chatSetup(t)
		value, _ := durable.ToJsonValue(map[string]any{"count": 3.0})
		addTool(t, setup.Registry, failing(durable.ToolExecutionResult{IsError: &yes, StructuredOutput: value, HasStructuredOutput: true, Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "partial"}}}))
		if kept := nestedReceived(t, setup, "fails"); !kept.IsError || !kept.HasStructuredOutput || len(kept.Diagnostics) != 0 {
			t.Errorf("kept: %+v, want isError, structuredOutput kept and no diagnostics", kept)
		}
	})
	// A dropped broken value is an error the Harness explains already (tool.ts finalResult checks
	// `[...final.diagnostics, ...harness]`): the output's text adds no tool_error diagnostic.
	t.Run("adds no tool_error diagnostic to a dropped broken value", func(t *testing.T) {
		setup := chatSetup(t)
		addTool(t, setup.Registry, tlTool("wrong", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			value, _ := durable.ToJsonValue(map[string]any{"count": "three"})
			return durable.ToolExecutionResult{StructuredOutput: value, HasStructuredOutput: true, Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "3 things"}}}, nil
		}, func(tool *durable.ToolRegistration) { tool.StructuredOutputSchema = countSchema }))
		if broken := nestedReceived(t, setup, "wrong"); !broken.IsError || broken.HasStructuredOutput || !slices.Equal(diagnosticCodes(broken), []string{"invalid_structured_output"}) {
			t.Errorf("broken value with output: %+v, want isError, no structuredOutput, only invalid_structured_output", broken)
		}
	})
}

// harness-nested-tools.test.ts:724 it("aborts a nested call whose admission was still committing when its caller returned"): a
// nested call whose admission was still committing when its caller returned is
// listed and aborted by the caller's settlement (tool.ts:399-401: ended, then the admissions underway are awaited). The
// admission's storage commit is held until the caller's execute has returned.
func TestANestedCallWhoseAdmissionWasCommittingWhenItsCallerReturnedIsAborted(t *testing.T) {
	setup := chatSetup(t)
	addTool(t, setup.Registry, echoRegistration())
	addTool(t, setup.Registry, tlTool("hang", func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		<-ctx.Done()
		return durable.ToolExecutionResult{}, context.Cause(ctx)
	}))
	store := newControlledStorage()
	late := make(chan error, 1)
	addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		if _, err := api.ExecuteTool(ctx, "echo", map[string]any{"text": "warm"}, durable.NestedCallOptions{Key: new("warm")}); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		held := store.holdCommits()
		go func() {
			_, err := api.ExecuteTool(ctx, "hang", map[string]any{}, durable.NestedCallOptions{Key: new("late")})
			late <- err
		}()
		if err := held.entered.wait(ctx); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		// Released once execute has returned: the admission is underway, the call is ending.
		defer func() { go held.release() }()
		return durable.ToolExecutionResult{}, nil
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, store, setup)
	// A late call left running holds the run open: bound the wait, so that failure is a failure, not a hang.
	bounded, cancel := context.WithTimeout(testContext, 30*time.Second)
	defer cancel()
	settled, err := ownSubmit(t, root, ownInput("go")).Wait(bounded)
	if err != nil || settled.Status != durable.SubmissionDone {
		t.Fatalf("submission %+v, %v", settled, err)
	}
	if err := root.WaitForIdle(testContext); err != nil {
		t.Fatal(err)
	}
	var hang *durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
	for _, task := range tlToolTasks(tlScanTasks(t, harness, root.Id())) {
		input, err := durable.FromJsonValue[ToolTaskInput](task.Input)
		if err == nil && input.Kind == ToolTaskNested && input.Call != nil && input.Call.Name == "hang" {
			hang = &task
		}
	}
	if hang == nil || !strings.Contains(nestedOutcome(t, *hang), `"outcome":{"status":"aborted"`) {
		t.Fatalf("late nested call = %+v", hang)
	}
	expectLiveEmpty(t, harness, root.Id())
	<-late
	mustClose(t, harness)
}
