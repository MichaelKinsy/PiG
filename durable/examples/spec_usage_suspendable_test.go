// Ports packages/durable/test/spec-usage.test.ts (Pi 473515f9c), the examples of suspendable work that use
// durable/harness: the work a run's end asks for (§7.2), a beforeTool gate that sleeps for an approval (§7.2), tools that
// park (§7.3), and a subagent tool that parks until its child conversation answers (§7.3). The task that asks a person
// (§5.7) is ported in durable/spec_usage_upstream_test.go. Like spec_usage_upstream_test.go, upstream compiles these
// examples and never calls them: the Go compiler is the type check, and the names the spec leaves to the application are
// zero values the test never invokes.

package examples_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/schema"
)

// The types of the examples: task input and checkpoints, signal values, tool checkpoints.
type (
	specRunTestsInput struct {
		Command string `json:"command"`
	}
	specRunPhase struct {
		Phase string `json:"phase"`
	}
	specApprovalDecision struct {
		Allow bool `json:"allow"`
	}
	specResearchCheckpoint struct {
		Done []durable.TaskId `json:"done"`
	}
	specCiResult struct {
		Status string `json:"status"`
		LogUrl string `json:"logUrl"`
	}
	specCiCheckpoint struct {
		BuildId string `json:"buildId"`
	}
)

// specTestResult is what the application's test runner reports: { passed: boolean; output: string }.
type specTestResult struct {
	Passed bool
	Output string
}

// specSuspendableNames are the names these examples leave to the application (spec-usage.test.ts declares them).
type specSuspendableNames struct {
	answerText      func(ctx context.Context, api durable.ToolExecutionApi, entry durable.EntryId) (string, error)
	runTests        func(ctx context.Context, command string) (specTestResult, error)
	isRisky         func(args ai.JsonObject) bool
	canonicalDigest func(args ai.JsonObject) string
	newRunId        func() string
	// webhooks is the host's webhook registry: when the webhook for match arrives, signal this key to this task.
	webhooks interface {
		expect(ctx context.Context, match string, taskId durable.TaskId, token durable.AnySignalSource, key string) error
	}
	ci interface {
		trigger(ctx context.Context, ref, correlationId, idempotencyKey string) (buildId string, err error)
		cancel(ctx context.Context, buildId string) error
		cancelByKey(ctx context.Context, idempotencyKey string) error
	}
}

// decodeSpecCheckpoint reads a park's checkpoint, which api.Checkpoint() returns as JSON, as its type.
func decodeSpecCheckpoint[T any](checkpoint durable.JsonValue) (T, error) {
	return durable.FromJsonValue[T](checkpoint)
}

// specUsageSuspendableExamples is the suspendable-work part of `examples()` of spec-usage.test.ts: the extensions and
// tools it defines, which upstream returns so they are used.
func specUsageSuspendableExamples(names specSuspendableNames) []any {
	// ─── Section 7.2: work at the end of a run ───────────────────────────────────

	runTestsTask := durable.DefineTask(durable.TaskDefinition[specRunTestsInput, specRunPhase, string, struct{}]{
		Name:    "app.run-tests",
		Version: 1,
		Initial: func(specRunTestsInput) specRunPhase { return specRunPhase{Phase: "run"} },
		Phases: map[string]durable.PhaseHandler[specRunTestsInput, specRunPhase, string, struct{}]{
			"run": func(ctx context.Context, task durable.RunningTask[specRunTestsInput, specRunPhase, string], runtime durable.TaskRuntime[specRunTestsInput, specRunPhase, string, struct{}]) error {
				// The app's helper over ExecutionEnv.Exec(): a rerun after a crash runs it again.
				result, err := names.runTests(ctx, task.Input.Command)
				if err != nil {
					return err
				}
				handle, err := runtime.Conversation(ctx, runtime.ConversationId())
				if err != nil {
					return err
				}
				return runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[specRunTestsInput, specRunPhase, string]) (*durable.NextTaskState[specRunPhase, string], error) {
					// Admitted in this commit, before its table writes; placed when the run ends, in queue order with other input.
					if !result.Passed {
						if _, err := handle.SubmitIn(tx, durable.SubmissionDraft{
							Type:     durable.SubmissionTypeInput,
							Content:  ai.UserText("Tests failed:\n" + result.Output),
							WhenBusy: durable.WhenBusyFollowUp,
						}); err != nil {
							return nil, err
						}
					}
					if _, err := tx.AppendEntry(runtime.ConversationId(), durable.EntryDraft{Kind: "app.tests", Data: delta.JsonObjectOf("passed", result.Passed)}); err != nil {
						return nil, err
					}
					return &durable.NextTaskState[specRunPhase, string]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeCompleted, Result: &result.Output}}, nil
				})
			},
		},
		Abort: func(ctx context.Context, _ durable.RunningTask[specRunTestsInput, specRunPhase, string], runtime durable.TaskRuntime[specRunTestsInput, specRunPhase, string, struct{}]) error {
			return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[specRunTestsInput, specRunPhase, string]) (*durable.NextTaskState[specRunPhase, string], error) {
				return &durable.NextTaskState[specRunPhase, string]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeAborted}}, nil
			})
		},
	})
	testsAfterEdits := new(durable.Extension{
		Name:  "tests-after-edits",
		Tasks: []durable.AnyTask{runTestsTask.Erased()},
		Hooks: []durable.HookRegistration{
			harness.Hook(harness.GenerationTask, &harness.GenerationHooks{
				AfterRun: func(ctx context.Context, run harness.RunSummary, api harness.HookApi) (*harness.HookTasks, error) {
					ascending := durable.ScanAscending
					page, err := api.Entries(ctx, durable.EntryQuery{MinEntryId: &run.First, MaxEntryId: &run.Last, Order: &ascending}, 1000, nil)
					if err != nil {
						return nil, err
					}
					edited := false
					for _, entry := range page.Items {
						if !durable.ToolResultEntry.Is(&entry) || len(entry.Model) == 0 {
							continue
						}
						if message, ok := entry.Model[0].(ai.ToolResultMessage); ok && (message.ToolName == "edit" || message.ToolName == "write") {
							edited = true
						}
					}
					if !edited {
						return nil, nil
					}
					return &harness.HookTasks{Tasks: []harness.HookTask{harness.HookTaskOf(runTestsTask, specRunTestsInput{Command: "npm test"})}}, nil
				},
			}),
		},
	})

	// ─── Section 7.2: gates ──────────────────────────────────────────────────────

	approval := durable.DefineSignal(durable.SignalDefinition[specApprovalDecision]{
		Name: "approvals.decision", Version: 1,
		Schema: schema.Obj(schema.Properties{schema.Prop("allow", schema.Bool())}),
	})
	const day = 24 * 60 * 60 * 1000
	approvals := new(durable.Extension{
		Name: "approvals",
		Hooks: []durable.HookRegistration{
			harness.Hook(harness.ToolTask, &harness.ToolHooks{
				BeforeTool: func(ctx context.Context, call harness.ToolHookCall, api harness.HookApi) (*harness.BeforeToolResult, error) {
					if call.Name != "bash" || !names.isRisky(call.Arguments) {
						return nil, nil
					}
					key := names.canonicalDigest(call.Arguments) // other arguments, another question
					state, err := durable.ReadSignal(ctx, api, approval, key)
					if err != nil {
						return nil, err
					}
					// One deadline per question, fixed at the first pass: later passes (wakes, registry changes) keep it.
					memo, err := api.MemoCandidate(ctx, "approvals.until."+key, api.Now()+day)
					if err != nil {
						return nil, err
					}
					until, _ := memo.(float64)
					if state == nil {
						return &harness.BeforeToolResult{Sleep: &harness.GateSleep{Signals: []durable.SignalKey{approval.Key(key)}, Until: &until}}, nil
					}
					if state.Expired {
						return &harness.BeforeToolResult{Block: new("No approval within a day")}, nil
					}
					if !state.Value.Allow {
						return &harness.BeforeToolResult{Block: new("Not approved")}, nil
					}
					return nil, nil
				},
			}),
		},
	})

	// ─── Section 7.3: tools that park ────────────────────────────────────────────

	research := harness.DefineTool(durable.ToolRegistration{
		Name:        "research_both",
		Description: "Research two topics in parallel",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"x": map[string]any{"type": "string"}, "y": map[string]any{"type": "string"}},
			"required":   []any{"x", "y"},
		},
		Replay: durable.ReplaySafe,
		Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			input := args.(map[string]any)
			first, err := api.ExecuteToolNoWait(ctx, "research", map[string]any{"topic": input["x"]}, durable.NestedCallOptions{Key: new("a")})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			second, err := api.ExecuteToolNoWait(ctx, "research", map[string]any{"topic": input["y"]}, durable.NestedCallOptions{Key: new("b")})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			done := map[durable.TaskId]bool{}
			if checkpoint, ok := api.Checkpoint(); ok {
				parked, err := decodeSpecCheckpoint[specResearchCheckpoint](checkpoint)
				if err != nil {
					return durable.ToolExecutionResult{}, err
				}
				for _, id := range parked.Done {
					done[id] = true
				}
			}
			if wake := api.Wake(); wake != nil && wake.Reason == durable.WakeTasks {
				for _, id := range wake.Ended {
					done[id] = true
				}
			}
			var open []durable.TaskId
			for _, call := range []durable.NestedToolCall{first, second} {
				if !done[call.TaskId] {
					open = append(open, call.TaskId)
				}
			}
			if len(open) > 0 {
				ended := make([]durable.TaskId, 0, len(done))
				for id := range done {
					ended = append(ended, id)
				}
				slices.Sort(ended)
				checkpoint, err := durable.ToJsonValue(specResearchCheckpoint{Done: ended})
				if err != nil {
					return durable.ToolExecutionResult{}, err
				}
				return durable.ToolExecutionResult{Park: &durable.ToolPark{Wait: &durable.ToolParkWait{Checkpoint: checkpoint, HasCheckpoint: true, On: open, Policy: durable.JoinAny}}}, nil
			}
			var texts []string
			for _, call := range []durable.NestedToolCall{first, second} {
				result, err := call.Result(ctx)
				if err != nil {
					return durable.ToolExecutionResult{}, err
				}
				texts = append(texts, fmt.Sprint(result.StructuredOutput))
			}
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: texts[0] + "\n" + texts[1]}}}, nil
		},
	})

	const hour = 60 * 60 * 1000
	ciResult := durable.DefineSignal(durable.SignalDefinition[specCiResult]{
		Name: "app.ci-result", Version: 1,
		Schema: schema.Obj(schema.Properties{schema.Prop("status", schema.OneOf([]any{"passed", "failed"})), schema.Prop("logUrl", schema.Str())}),
	})
	runCi := harness.DefineTool(durable.ToolRegistration{
		Name:        "run_ci",
		Description: "Run CI on a git ref and report the result",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{"ref": map[string]any{"type": "string"}}, "required": []any{"ref"}},
		Replay:      durable.ReplaySafe,
		Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			ref, _ := args.(map[string]any)["ref"].(string)
			// The result, the deadline's expiry, or nothing yet (a fresh call).
			state, err := durable.ReadSignal(ctx, api, ciResult, "done")
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if state != nil && !state.Expired {
				failed := state.Value.Status != "passed"
				return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("CI %s: %s", state.Value.Status, state.Value.LogUrl)}}, IsError: &failed}, nil
			}
			if state != nil {
				var parked specCiCheckpoint
				if checkpoint, ok := api.Checkpoint(); ok {
					if parked, err = decodeSpecCheckpoint[specCiCheckpoint](checkpoint); err != nil {
						return durable.ToolExecutionResult{}, err
					}
				}
				failed := true
				return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "CI build " + parked.BuildId + " never reported"}}, IsError: &failed}, nil
			}
			// The tool's own idempotency key with CI, stable across reruns (memos are first-writer-wins).
			memo, err := api.MemoCandidate(ctx, "ci.run", names.newRunId())
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			runId, _ := memo.(string)
			// Registered before triggering, so a fast webhook already finds this task.
			if err := names.webhooks.expect(ctx, runId, api.TaskId(), ciResult, "done"); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			buildId, err := names.ci.trigger(ctx, ref, runId, runId)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			checkpoint, err := durable.ToJsonValue(specCiCheckpoint{BuildId: buildId})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			until := api.Now() + 2*hour
			return durable.ToolExecutionResult{Park: &durable.ToolPark{Sleep: &durable.ToolParkSleep{Checkpoint: checkpoint, HasCheckpoint: true, Signals: []durable.SignalKey{ciResult.Key("done")}, Until: &until}}}, nil
		},
		Cancel: func(ctx context.Context, _ any, api durable.ToolExecutionApi) error {
			if checkpoint, ok := api.Checkpoint(); ok {
				parked, err := decodeSpecCheckpoint[specCiCheckpoint](checkpoint)
				if err != nil {
					return err
				}
				return names.ci.cancel(ctx, parked.BuildId)
			}
			memo, found, err := api.Memo(ctx, "ci.run")
			if err != nil || !found {
				return err
			}
			key, _ := memo.(string)
			return names.ci.cancelByKey(ctx, key)
		},
	})

	var parkingSubagent *durable.Extension
	parkingSubagent = new(durable.Extension{
		Name: "parking-subagent",
		Tools: []*durable.ToolRegistration{
			harness.DefineTool(durable.ToolRegistration{
				Name:        "subagent",
				Description: "Delegate a task to a subagent and get its answer back",
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{"task": map[string]any{"type": "string"}}, "required": []any{"task"}},
				Replay:      durable.ReplaySafe,
				Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
					task, _ := args.(map[string]any)["task"].(string)
					state, err := durable.ReadSignal(ctx, api, durable.SubmissionSettledSignal, "answered")
					if err != nil {
						return durable.ToolExecutionResult{}, err
					}
					if state != nil && !state.Expired {
						settled := state.Value
						if settled.Status != durable.SubmissionDone || settled.Type != durable.SubmissionTypeInput {
							failed := true
							return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "Subagent ended: " + string(settled.Status)}}, IsError: &failed}, nil
						}
						answer, err := names.answerText(ctx, api, *settled.Answer)
						return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: answer}}}, err
					}
					created, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
						taskId := api.TaskId()
						existing, err := tx.ScanConversations(durable.ConversationQuery{OwnerTaskId: &taskId}, 1, nil)
						if err != nil {
							return nil, err
						}
						if len(existing.Items) > 0 {
							return existing.Items[0].Id, nil
						}
						child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: taskId}})
						if err != nil {
							return nil, err
						}
						err = harness.Configure(tx, child.Id, harness.AgentChange{Extensions: harness.SetTo(harness.ExtensionChange{Edit: &harness.ExtensionEdit{Remove: []*durable.Extension{parkingSubagent}}})})
						return child.Id, err
					})
					if err != nil {
						return durable.ToolExecutionResult{}, err
					}
					handle, err := api.Conversation(ctx, created.(durable.ConversationId))
					if err != nil {
						return durable.ToolExecutionResult{}, err
					}
					// The request ID makes a rerun find the same submission.
					if _, err := handle.Submit(ctx, durable.InputSubmissionDraft{
						Type: durable.SubmissionTypeInput, Content: ai.UserText(task), RequestId: new(fmt.Sprintf("subagent:%d", api.TaskId())),
						Notify: &durable.SubmissionNotify{TaskId: api.TaskId(), Key: "answered"},
					}); err != nil {
						return durable.ToolExecutionResult{}, err
					}
					return durable.ToolExecutionResult{Park: &durable.ToolPark{Sleep: &durable.ToolParkSleep{Signals: []durable.SignalKey{durable.SubmissionSettledSignal.Key("answered")}}}}, nil
				},
			}),
		},
	})

	return []any{testsAfterEdits, approvals, research, runCi, parkingSubagent}
}

// spec-usage.test.ts it("compiles the spec's usage examples"): the suspendable-work examples build their extensions and tools.
func TestSpecUsageCompilesTheSuspendableExamples(t *testing.T) {
	defined := specUsageSuspendableExamples(specSuspendableNames{})
	if len(defined) != 5 {
		t.Fatalf("%d examples, want 5", len(defined))
	}
	approvals := defined[1].(*durable.Extension)
	if approvals.Name != "approvals" || len(approvals.Hooks) != 1 || approvals.Hooks[0].Task != harness.ToolTask.AnyDefinition().Name {
		t.Fatalf("approvals extension %+v", approvals)
	}
	if tool := defined[3].(*durable.ToolRegistration); tool.Name != "run_ci" || tool.Cancel == nil || tool.Replay != durable.ReplaySafe {
		t.Fatalf("run_ci %+v", tool)
	}
}
