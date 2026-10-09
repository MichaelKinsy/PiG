// Ports packages/durable/test/harness-tools-recovery.test.ts, except the real bash command case, which needs the bash
// tool and so lives in the external test package (harness_coding_tools_test.go). See harness_tasks_test.go for the Go
// mappings that apply to every case.

package harness

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	envnode "github.com/MichaelKinsy/PiG/durable/env/node"
)

func trOpen(t *testing.T, path string, setup *chatState, options ...openChatOptions) (Harness, Conversation) {
	t.Helper()
	harness, root := openChat(t, tkOpenSqlite(t, path), setup, options...)
	harness.Resume()
	return harness, root
}

func trCall(name, id string) ai.FauxResponseStep {
	return tlCallsStep(tlCall{name, map[string]any{}, id})
}

func trText(message ai.ToolResultMessage) string {
	parts := []string{}
	for _, item := range message.Content {
		if text, ok := item.(ai.TextContent); ok {
			parts = append(parts, text.Text)
		} else {
			parts = append(parts, "")
		}
	}
	return strings.Join(parts, "|")
}

func trToolTaskId(t *testing.T, harness Harness) durable.TaskId {
	t.Helper()
	var id durable.TaskId
	waitFor(t, func() bool {
		slot := tlLiveTool(t, harness, 1)
		if slot == nil {
			return false
		}
		value, ok := slot["taskId"].(float64)
		id = durable.TaskId(value)
		return ok
	})
	return id
}

func trTool(name string, execute tlExecute, edit ...func(*durable.ToolRegistration)) *durable.ToolRegistration {
	return ownTool(name, name, emptyObjectSchema(), execute, edit...)
}

type blockingState struct {
	runs     atomicCounter
	blocking int64
}

// blockingTool writes output, then blocks until its invocation is cancelled the first blocking times it runs. started resolves once the output is durable.
func blockingTool(name string, edit ...func(*durable.ToolRegistration)) (*durable.ToolRegistration, *deferredGate, *blockingState) {
	started := deferred()
	state := &blockingState{blocking: 1}
	registration := trTool(name, func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		run := state.runs.add()
		api.Output(fmt.Sprintf("run %d\n", run))
		if err := api.Details(ctx, map[string]any{"run": float64(run)}); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if run <= state.blocking {
			started.resolve()
			return durable.ToolExecutionResult{}, abortedBy(ctx)
		}
		return durable.ToolExecutionResult{}, nil
	}, edit...)
	return registration, started, state
}

func TestToolRecovery(t *testing.T) {
	t.Run("answers an unsafe tool interrupted after intent with its durable partial output", func(t *testing.T) {
		path := sqlitePath(t)
		setup := chatSetup(t)
		registration, started, state := blockingTool("work")
		addTool(t, setup.Registry, registration)
		setup.Faux.SetResponses([]ai.FauxResponseStep{trCall("work", "c1"), tlDone()})
		harness, root := trOpen(t, path, setup)
		id := ownSubmit(t, root, ownInput("go")).Id()
		if err := started.wait(testContext); err != nil {
			t.Fatal(err)
		}
		taskId := trToolTaskId(t, harness)
		mustClose(t, harness)

		harness, root = trOpen(t, path, setup)
		record := tkTaskRecord(t, harness, taskId)
		wantCheckpoint := map[string]any{"phase": "execute", "arguments": map[string]any{}, "replay": "unsafe"}
		if record.State.Checkpoint == nil || !reflect.DeepEqual(plainObject(*record.State.Checkpoint), wantCheckpoint) {
			t.Fatalf("checkpoint %v, want %v", record.State.Checkpoint, wantCheckpoint)
		}
		submission, err := harness.Submission(testContext, id)
		if err != nil || submission == nil {
			t.Fatalf("Submission %v %v", submission, err)
		}
		if settled, err := submission.Wait(testContext); err != nil || settled.Status != durable.SubmissionDone {
			t.Fatalf("input %+v %v, want done", settled, err)
		}
		if state.runs.get() != 1 {
			t.Fatalf("%d runs, want 1", state.runs.get())
		}
		result := tlResults(allEntries(t, root))[0]
		if !result.IsError || !reflect.DeepEqual(result.Details, map[string]any{"run": float64(1)}) {
			t.Fatalf("result %+v, want an error with details run 1", result)
		}
		tlExpectText(t, trText(result), "run 1\n|<harness>\n[error] Tool work was interrupted and may have partially run\n</harness>")
		if live, err := harness.SnapshotErased(testContext, LiveDoc, root.Id()); err != nil || live.Len() != 0 {
			t.Fatalf("live document %v %v, want {}", live, err)
		}
		mustClose(t, harness)
	})

	t.Run("reruns a tool only when both the stored and the current replay policy are safe", func(t *testing.T) {
		cases := []struct {
			stored, current durable.ToolReplay
			reruns          bool
		}{
			{durable.ReplaySafe, durable.ReplaySafe, true},
			{durable.ReplaySafe, durable.ReplayUnsafe, false},
			{durable.ReplayUnsafe, durable.ReplaySafe, false},
		}
		for _, each := range cases {
			t.Run(string(each.stored)+" then "+string(each.current), func(t *testing.T) {
				path := sqlitePath(t)
				setup := chatSetup(t)
				registration, started, state := blockingTool("work", func(tool *durable.ToolRegistration) { tool.Replay = each.stored })
				installedTool := addTool(t, setup.Registry, registration)
				setup.Faux.SetResponses([]ai.FauxResponseStep{trCall("work", "c1"), tlDone()})
				harness, root := trOpen(t, path, setup)
				id := ownSubmit(t, root, ownInput("go")).Id()
				if err := started.wait(testContext); err != nil {
					t.Fatal(err)
				}
				mustClose(t, harness)

				installedTool.dispose()
				swapped := *registration
				swapped.Replay = each.current
				addTool(t, setup.Registry, &swapped)
				harness, root = trOpen(t, path, setup)
				submission, err := harness.Submission(testContext, id)
				if err != nil || submission == nil {
					t.Fatalf("Submission %v %v", submission, err)
				}
				if settled, err := submission.Wait(testContext); err != nil || settled.Status != durable.SubmissionDone {
					t.Fatalf("input %+v %v, want done", settled, err)
				}
				result := tlResults(allEntries(t, root))[0]
				wantRuns := int64(1)
				if each.reruns {
					wantRuns = 2
				}
				if state.runs.get() != wantRuns || result.IsError != !each.reruns {
					t.Fatalf("%d runs and isError %v, want %d runs and isError %v", state.runs.get(), result.IsError, wantRuns, !each.reruns)
				}
				if each.reruns {
					tlExpectText(t, trText(result), "run 2\n")
				}
				mustClose(t, harness)
			})
		}
	})

	t.Run("reruns a safe tool with the environment of the conversation's cwd at rerun, and not once it is deselected", func(t *testing.T) {
		for _, change := range []string{"cwd", "deselect"} {
			t.Run(change, func(t *testing.T) {
				path := sqlitePath(t)
				setup := chatSetup(t)
				cwds := &syncList[string]{}
				started := deferred()
				addTool(t, setup.Registry, trTool("work", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
					cwds.add(api.Env().Cwd())
					if cwds.len() == 1 {
						started.resolve()
						return durable.ToolExecutionResult{}, abortedBy(ctx)
					}
					return durable.ToolExecutionResult{}, nil
				}, func(tool *durable.ToolRegistration) { tool.Replay = durable.ReplaySafe }))
				setup.Faux.SetResponses([]ai.FauxResponseStep{trCall("work", "c1"), tlDone()})
				environment := openChatOptions{envFunc: func(_ context.Context, target EnvTarget) (env.ExecutionEnv, error) {
					cwd := "/"
					if target.Cwd != nil {
						cwd = *target.Cwd
					}
					return envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: cwd}), nil
				}}
				harness, root := trOpen(t, path, setup, environment)
				mustConfigure(t, root, AgentChange{Cwd: SetTo("/one")})
				id := ownSubmit(t, root, ownInput("go")).Id()
				if err := started.wait(testContext); err != nil {
					t.Fatal(err)
				}
				if change == "cwd" {
					mustConfigure(t, root, AgentChange{Cwd: SetTo("/two")})
				} else {
					mustConfigure(t, root, AgentChange{Extensions: SetTo(ExtensionChange{Exact: true})})
				}
				mustClose(t, harness)

				harness, root = trOpen(t, path, setup, environment)
				submission, err := harness.Submission(testContext, id)
				if err != nil || submission == nil {
					t.Fatalf("Submission %v %v", submission, err)
				}
				if settled, err := submission.Wait(testContext); err != nil || settled.Status != durable.SubmissionDone {
					t.Fatalf("input %+v %v, want done", settled, err)
				}
				result := tlResults(allEntries(t, root))[0]
				if change == "cwd" {
					if !reflect.DeepEqual(cwds.all(), []string{"/one", "/two"}) || result.IsError {
						t.Fatalf("cwds %v and isError %v, want /one then /two and no error", cwds.all(), result.IsError)
					}
				} else {
					// A tool that no longer resolves is treated as unsafe: interrupted, not rerun.
					if !reflect.DeepEqual(cwds.all(), []string{"/one"}) || !strings.Contains(trText(result), "was interrupted") {
						t.Fatalf("cwds %v and result %q, want /one only and an interruption", cwds.all(), trText(result))
					}
				}
				mustClose(t, harness)
			})
		}
	})

	t.Run("reruns beforeTool when interrupted before intent, and executes once", func(t *testing.T) {
		path := sqlitePath(t)
		setup := chatSetup(t)
		var runs, asked atomicCounter
		addTool(t, setup.Registry, trTool("work", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			runs.add()
			return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}}, nil
		}))
		reached := deferred()
		decisions := &syncList[string]{}
		addHooks(t, setup.Registry, ToolTask, &ToolHooks{BeforeTool: func(ctx context.Context, _ ai.ToolCall, api HookApi) (*BeforeToolResult, error) {
			attempt := asked.add()
			// A durable first-writer-wins decision survives the rerun.
			decision, err := api.MemoCandidate(ctx, "test:decision", fmt.Sprintf("attempt %d", attempt))
			if err != nil {
				return nil, err
			}
			decisions.add(decision.(string))
			if attempt == 1 {
				reached.resolve()
				return nil, abortedBy(ctx)
			}
			return nil, nil
		}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{trCall("work", "c1"), tlDone()})
		harness, root := trOpen(t, path, setup)
		id := ownSubmit(t, root, ownInput("go")).Id()
		if err := reached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		mustClose(t, harness)

		harness, _ = trOpen(t, path, setup)
		submission, err := harness.Submission(testContext, id)
		if err != nil || submission == nil {
			t.Fatalf("Submission %v %v", submission, err)
		}
		if settled, err := submission.Wait(testContext); err != nil || settled.Status != durable.SubmissionDone {
			t.Fatalf("input %+v %v, want done", settled, err)
		}
		if asked.get() != 2 || runs.get() != 1 {
			t.Fatalf("beforeTool ran %d times and the tool %d, want 2 and 1", asked.get(), runs.get())
		}
		if !reflect.DeepEqual(decisions.all(), []string{"attempt 1", "attempt 1"}) {
			t.Fatalf("decisions %v, want attempt 1 twice", decisions.all())
		}
		mustClose(t, harness)
	})

	t.Run("reruns the generation tools phase interrupted before its commit", func(t *testing.T) {
		path := sqlitePath(t)
		setup := chatSetup(t)
		addTool(t, setup.Registry, trTool("work", tlNoContent))
		reached := deferred()
		var observed atomicCounter
		addHooks(t, setup.Registry, GenerationTask, &GenerationHooks{AfterTools: func(ctx context.Context, _ durable.EntryId, _ []durable.EntryId, _ HookApi) error {
			if observed.add() == 1 {
				reached.resolve()
				return abortedBy(ctx)
			}
			return nil
		}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{trCall("work", "c1"), tlDone()})
		harness, root := trOpen(t, path, setup)
		id := ownSubmit(t, root, ownInput("go")).Id()
		if err := reached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		mustClose(t, harness)

		harness, root = trOpen(t, path, setup)
		submission, err := harness.Submission(testContext, id)
		if err != nil || submission == nil {
			t.Fatalf("Submission %v %v", submission, err)
		}
		if settled, err := submission.Wait(testContext); err != nil || settled.Status != durable.SubmissionDone {
			t.Fatalf("input %+v %v, want done", settled, err)
		}
		if observed.get() != 2 {
			t.Fatalf("afterTools ran %d times, want 2", observed.get())
		}
		kinds := []string{}
		for _, entry := range allEntries(t, root) {
			kinds = append(kinds, entry.Kind)
		}
		if want := []string{"pi.user", "pi.system", "pi.assistant", "pi.tool-result", "pi.assistant"}; !reflect.DeepEqual(kinds, want) {
			t.Fatalf("entries %v, want %v", kinds, want)
		}
		mustClose(t, harness)
	})

	t.Run("answers an aborted tool with its partial output and continues the run", func(t *testing.T) {
		path := sqlitePath(t)
		setup := chatSetup(t)
		registration, started, _ := blockingTool("work")
		addTool(t, setup.Registry, registration)
		setup.Faux.SetResponses([]ai.FauxResponseStep{trCall("work", "c1"), tlDone()})
		harness, root := trOpen(t, path, setup)
		submission := ownSubmit(t, root, ownInput("go"))
		if err := started.wait(testContext); err != nil {
			t.Fatal(err)
		}
		taskId := trToolTaskId(t, harness)
		if result, err := harness.AbortTask(testContext, taskId); err != nil || result != "marked" {
			t.Fatalf("AbortTask %q %v", result, err)
		}
		outcome := tkWaitOutcome(t, harness, taskId)
		if outcome.Status != durable.OutcomeAborted || outcome.Result == nil {
			t.Fatalf("outcome %s, want aborted with a result entry", describeOutcome(outcome))
		}
		if entry, ok := plainObject(*outcome.Result)["entryId"].(float64); !ok || entry == 0 {
			t.Fatalf("outcome result %v has no entry ID", *outcome.Result)
		}
		if settled, err := submission.Wait(testContext); err != nil || settled.Status != durable.SubmissionDone {
			t.Fatalf("input %+v %v, want done", settled, err)
		}
		tlExpectText(t, trText(tlResults(allEntries(t, root))[0]), "run 1\n|<harness>\n[error] Tool work was aborted\n</harness>")
		mustClose(t, harness)
	})

	t.Run("lets context derivation answer a faulted tool and continues the run", func(t *testing.T) {
		path := sqlitePath(t)
		setup := chatSetup(t)
		// A result that is not strict JSON makes the result commit fail, so the scheduler faults the task.
		addTool(t, setup.Registry, trTool("bad", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}, Details: map[string]any{"fn": func() int { return 1 }}, HasDetails: true}, nil
		}))
		requests := &syncList[string]{}
		setup.Faux.SetResponses([]ai.FauxResponseStep{
			trCall("bad", "c1"),
			ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
				seen := "none"
				for _, message := range request.Messages() {
					if result, ok := message.(ai.ToolResultMessage); ok {
						seen = trText(result)
						break
					}
				}
				requests.add(seen)
				return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}}.AssistantMessage(), nil
			}),
		})
		harness, root := trOpen(t, path, setup)
		if settled := ownSubmitAndWait(t, root, "go"); settled.Status != durable.SubmissionDone {
			t.Fatalf("input %s, want done", settled.Status)
		}
		if results := tlResults(allEntries(t, root)); len(results) != 0 {
			t.Fatalf("results %+v, want none", results)
		}
		if want := []string{"Tool result unavailable: history ends before this call completed."}; !reflect.DeepEqual(requests.all(), want) {
			t.Fatalf("requests %v, want %v", requests.all(), want)
		}
		tools := tlToolTasks(tlScanTasks(t, harness, root.Id()))
		if len(tools) == 0 || tools[0].State.Status != durable.TaskTerminal || tools[0].State.Outcome.Status != durable.OutcomeFaulted {
			t.Fatalf("tool tasks %+v, want a faulted terminal task", tools)
		}
		mustClose(t, harness)
	})

	t.Run("clears the interrupted attempt's progress before a safe rerun", func(t *testing.T) {
		path := sqlitePath(t)
		setup := chatSetup(t)
		started := []*deferredGate{deferred(), deferred()}
		var runs atomicCounter
		addTool(t, setup.Registry, trTool("work", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			run := runs.add() - 1
			if run == 0 {
				api.Diagnostic(durable.ToolDiagnostic{Severity: durable.SeverityInfo, Message: "first a"})
				api.Diagnostic(durable.ToolDiagnostic{Severity: durable.SeverityInfo, Message: "first b"})
				if err := api.Details(ctx, map[string]any{"run": float64(1), "extra": true}); err != nil {
					return durable.ToolExecutionResult{}, err
				}
			} else {
				api.Diagnostic(durable.ToolDiagnostic{Severity: durable.SeverityInfo, Message: "second"})
				if err := api.Details(ctx, map[string]any{"run": float64(2)}); err != nil {
					return durable.ToolExecutionResult{}, err
				}
			}
			started[run].resolve()
			return durable.ToolExecutionResult{}, abortedBy(ctx)
		}, func(tool *durable.ToolRegistration) { tool.Replay = durable.ReplaySafe }))
		setup.Faux.SetResponses([]ai.FauxResponseStep{trCall("work", "c1"), tlDone()})
		harness, root := trOpen(t, path, setup)
		ownSubmit(t, root, ownInput("go"))
		if err := started[0].wait(testContext); err != nil {
			t.Fatal(err)
		}
		mustClose(t, harness)

		harness, root = trOpen(t, path, setup)
		if err := started[1].wait(testContext); err != nil {
			t.Fatal(err)
		}
		slot := tlLiveTool(t, harness, root.Id())
		wantDiagnostics := []any{map[string]any{"severity": "info", "message": "second"}}
		if !reflect.DeepEqual(slot["diagnostics"], wantDiagnostics) || !reflect.DeepEqual(slot["details"], map[string]any{"run": float64(2)}) {
			t.Fatalf("slot %v, want diagnostics %v and details run 2", slot, wantDiagnostics)
		}
		mustClose(t, harness)
	})
}
