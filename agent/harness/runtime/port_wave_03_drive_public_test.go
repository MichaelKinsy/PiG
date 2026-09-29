package runtime

import (
	"errors"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestPortWave03DrivePublic(t *testing.T) {
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:61
	t.Run("persists model identity without requiring a local registration", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			ctx := t.Context()
			lane := fixture.lane
			require.NoError(t, lane.SetModel(ctx, agentharness.ModelIdentity{Provider: "missing", ModelID: "missing-model"}))
			model, err := lane.GetModel(ctx)
			require.NoError(t, err)
			require.Nil(t, model)
			result, err := lane.Prompt(ctx, "prompt", nil)
			require.NoError(t, err)
			require.NotNil(t, result.Record)
			require.Equal(t, "run", result.Record.Kind)
			require.Equal(t, "failed", result.Record.Status)
			require.NotNil(t, result.Record.Error)
			require.Equal(t, "model_unavailable", result.Record.Error.Code)
			require.Zero(t, fixture.faux.callCount())
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:77
	t.Run("composes prompt, skill, and template acceptance with drive", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{Skills: []harness.Skill{{Name: "review", Description: "Review", Content: "Inspect it", FilePath: "/skills/review/SKILL.md"}}, PromptTemplates: []harness.PromptTemplate{{Name: "fix", Content: "Fix $1"}}})
			ctx := t.Context()
			fixture.faux.setResponses([]ai.FauxResponseStep{laneFauxResponse("prompt answer"), laneFauxResponse("skill answer"), laneFauxResponse("template answer")})
			result, err := fixture.lane.Prompt(ctx, "prompt", nil)
			requireLaneRun(t, result, err, "completed")
			instructions := "strict"
			result, err = fixture.lane.Skill(ctx, "review", &instructions)
			requireLaneRun(t, result, err, "completed")
			result, err = fixture.lane.PromptFromTemplate(ctx, "fix", []string{"it"})
			requireLaneRun(t, result, err, "completed")
			require.Equal(t, 3, fixture.faux.callCount())
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:106
	t.Run("returns a convenience-only suspension observation", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, true, agentharness.Resources{})
			fixture.faux.setResponses([]ai.FauxResponseStep{laneFauxResponse("eventual answer")})
			result, err := fixture.lane.Prompt(t.Context(), "defer", nil)
			require.NoError(t, err)
			require.NotNil(t, result.Suspended)
			require.NotEmpty(t, result.Suspended.OperationID)
			require.Equal(t, "suspended", result.Suspended.Status)
			require.Equal(t, "faux", result.Suspended.Deferred.Provider)
			require.Equal(t, "faux-1", result.Suspended.Deferred.ModelID)
			operation := fixture.lane.SnapshotState().Operation
			require.NotNil(t, operation)
			require.Equal(t, session.AtDeferredSuspended, operation.State.At)
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:121
	t.Run("records caller usage as an adjustment and publishes committed totals", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			events := []agentharness.HarnessEvent{}
			_, err := fixture.harness.Events().On(agentharness.EventUsage, func(_ harness.Context, event agentharness.HarnessEvent) error {
				events = append(events, event)
				return nil
			})
			require.NoError(t, err)
			// fauxAssistantMessage("usage").usage is the pinned zero-usage fixture.
			details := session.JsonValue(map[string]any{"source": "test"})
			entryID := "external"
			id, err := fixture.lane.RecordUsage(t.Context(), ai.Usage{}, &agentharness.RecordUsageOptions{EntryID: &entryID, Details: &details})
			require.NoError(t, err)
			require.Len(t, events, 1)
			require.Equal(t, agentharness.EventUsage, events[0].Type())
			require.Equal(t, "main", events[0].Lane)
			usage, ok := events[0].Payload.(agentharness.UsagePayload)
			require.True(t, ok)
			require.Equal(t, id, usage.Row.ID)
			require.True(t, usage.Row.Adjustment)
			stats, err := fixture.session.GetStats(t.Context())
			require.NoError(t, err)
			require.Equal(t, stats.Usage, usage.Totals)
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:146 (all three rows)
	for _, kind := range []string{"steer", "followUp", "nextRun"} {
		t.Run("starts an ordinary continuation run from queued "+kind+" input", func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fixture := newPublicLane(t, false, agentharness.Resources{})
				ctx := t.Context()
				_, err := fixture.lane.AppendMessage(ctx, laneUser("history", 1))
				require.NoError(t, err)
				started, release, summary := blockedLaneResponse(t, "summary")
				fixture.faux.setResponses([]ai.FauxResponseStep{summary, laneFauxResponse("continuation answer")})
				compacting := asyncLaneCall(func() (agentharness.CompactionOutcome, error) { return fixture.lane.Compact(ctx, nil) })
				<-started
				switch kind {
				case "steer":
					_, err = fixture.lane.SteerText(ctx, "continue", nil)
				case "followUp":
					_, err = fixture.lane.FollowUpText(ctx, "continue", nil)
				case "nextRun":
					_, err = fixture.lane.NextRunText(ctx, "continue", nil)
				}
				require.NoError(t, err)
				release()
				compacted := awaitLaneCall(t, compacting)
				require.Equal(t, "compaction", compacted.Compaction.Kind)
				require.Equal(t, "completed", compacted.Compaction.Status)
				require.NotNil(t, compacted.Run)
				requireLaneRun(t, *compacted.Run, nil, "completed")
				require.Equal(t, 2, fixture.faux.callCount())
			})
		})
	}
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:178
	t.Run("lets a competing acceptance win the structural continuation window", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			ctx := t.Context()
			_, err := fixture.lane.AppendMessage(ctx, laneUser("history", 1))
			require.NoError(t, err)
			started, release, summary := blockedLaneResponse(t, "summary")
			fixture.faux.setResponses([]ai.FauxResponseStep{summary, laneFauxResponse("competitor answer")})
			var competing <-chan laneCallResult[agentharness.OperationAdmission]
			_, err = fixture.harness.Events().On(agentharness.EventCompactionEnd, func(_ harness.Context, event agentharness.HarnessEvent) error {
				if event.Payload.(agentharness.CompactionEndPayload).Status == "completed" {
					competing = asyncLaneCall(func() (agentharness.OperationAdmission, error) {
						text := "competitor"
						return fixture.lane.Accept(ctx, agentharness.OperationRequest{Kind: agentharness.RequestPrompt, PromptText: &text})
					})
					synctest.Wait()
				}
				return nil
			})
			require.NoError(t, err)
			compacting := asyncLaneCall(func() (agentharness.CompactionOutcome, error) { return fixture.lane.Compact(ctx, nil) })
			<-started
			_, err = fixture.lane.NextRunText(ctx, "queued", nil)
			require.NoError(t, err)
			release()
			compacted := awaitLaneCall(t, compacting)
			require.Equal(t, "completed", compacted.Compaction.Status)
			require.Nil(t, compacted.Run)
			require.NotNil(t, competing)
			admission := awaitLaneCall(t, competing)
			result, err := fixture.lane.Drive(ctx, agentharness.DriveOptions{OperationID: admission.OperationID})
			requireLaneSettled(t, result, err, "completed")
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:215
	t.Run("cancels queued input and reports consumed or missing ids", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			ctx := t.Context()
			lane := fixture.lane
			id, err := lane.NextRunText(ctx, "cancel", nil)
			require.NoError(t, err)
			cancelled, err := lane.CancelQueued(ctx, id)
			require.NoError(t, err)
			require.Equal(t, agentharness.CancelQueuedCancelled, cancelled)
			cancelled, err = lane.CancelQueued(ctx, id)
			require.NoError(t, err)
			require.Equal(t, agentharness.CancelQueuedNotFound, cancelled)
			consumed, err := lane.NextRunText(ctx, "consume", nil)
			require.NoError(t, err)
			empty := ""
			admission, err := lane.Accept(ctx, agentharness.OperationRequest{Kind: agentharness.RequestPrompt, PromptText: &empty})
			require.NoError(t, err)
			cancelled, err = lane.CancelQueued(ctx, consumed)
			require.NoError(t, err)
			require.Equal(t, agentharness.CancelQueuedAlreadyConsumed, cancelled)
			fixture.faux.setResponses([]ai.FauxResponseStep{laneFauxResponse("answer")})
			_, err = lane.Drive(ctx, agentharness.DriveOptions{OperationID: admission.OperationID})
			require.NoError(t, err)
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:240
	t.Run("admits input after cancellation and preserves it through reconciliation", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			ctx := t.Context()
			acceptPublicRun(t, fixture.lane, "cancelled")
			_, err := fixture.lane.RequestAbort(ctx, "cancelled")
			require.NoError(t, err)
			_, err = fixture.lane.SteerText(ctx, "late", nil)
			require.NoError(t, err)
			result, err := fixture.lane.Drive(ctx, agentharness.DriveOptions{OperationID: "cancelled"})
			requireLaneSettled(t, result, err, "aborted")
			require.Len(t, fixture.lane.SnapshotState().Inbox, 1)
			fixture.faux.setResponses([]ai.FauxResponseStep{laneFauxResponse("late answer")})
			run, err := fixture.lane.Prompt(ctx, "", nil)
			requireLaneRun(t, run, err, "completed")
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:259
	t.Run("composes standalone compaction acceptance with drive", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			ctx := t.Context()
			_, err := fixture.lane.AppendMessage(ctx, laneUser("history", 1))
			require.NoError(t, err)
			fixture.faux.setResponses([]ai.FauxResponseStep{laneFauxResponse("summary")})
			result, err := fixture.lane.Compact(ctx, nil)
			require.NoError(t, err)
			require.Equal(t, "compaction", result.Compaction.Kind)
			require.Equal(t, "completed", result.Compaction.Status)
			require.Equal(t, 1, fixture.faux.callCount())
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:271 (both rows)
	for _, test := range []struct {
		name      string
		summarize bool
	}{{"false", false}, {"true", true}} {
		t.Run("composes "+test.name+" summarized navigation acceptance with drive", func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fixture := newPublicLane(t, false, agentharness.Resources{})
				ctx := t.Context()
				root, err := fixture.lane.AppendMessage(ctx, laneUser("root", 1))
				require.NoError(t, err)
				_, err = fixture.lane.AppendMessage(ctx, laneUser("source", 2))
				require.NoError(t, err)
				_, err = fixture.session.Mutate(ctx, func(ctx harness.Context, mutator session.SessionMutator) (any, error) {
					return mutator.Commit(ctx, []session.Write{session.InsertEntry(session.Entry{ID: "target", ParentID: &root, Type: session.EntryTypeMessage, Message: laneUser("target", 3)})})
				})
				require.NoError(t, err)
				if test.summarize {
					fixture.faux.setResponses([]ai.FauxResponseStep{laneFauxResponse("branch summary")})
				}
				target, label := "target", "chosen"
				result, err := fixture.lane.NavigateTree(ctx, &target, &agentharness.NavigateOptions{Summarize: test.summarize, Label: &label})
				require.NoError(t, err)
				require.Equal(t, "navigation", result.Navigation.Kind)
				require.Equal(t, "completed", result.Navigation.Status)
				expectedCalls := 0
				if test.summarize {
					expectedCalls = 1
				}
				require.Equal(t, expectedCalls, fixture.faux.callCount())
				tip, err := fixture.lane.GetTipID(ctx)
				require.NoError(t, err)
				require.NotNil(t, tip)
			})
		})
	}
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:303
	t.Run("resumes any current operation after acceptance or reopen", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			acceptPublicRun(t, fixture.lane, "run")
			fixture.faux.setResponses([]ai.FauxResponseStep{laneFauxResponse("answer")})
			result, err := fixture.lane.Resume(t.Context())
			requireLaneRun(t, result, err, "completed")
			require.Equal(t, "run", result.Record.OperationID)
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:314
	t.Run("polls one deferred permit through resume", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, true, agentharness.Resources{})
			fixture.faux.setResponses([]ai.FauxResponseStep{laneFauxResponse("eventual answer")})
			ctx := t.Context()
			suspended, err := fixture.lane.Prompt(ctx, "defer", nil)
			require.NoError(t, err)
			require.NotNil(t, suspended.Suspended)
			result, err := fixture.lane.Resume(ctx)
			requireLaneRun(t, result, err, "completed")
			require.Equal(t, suspended.Suspended.OperationID, result.Record.OperationID)
			require.Nil(t, fixture.lane.SnapshotState().Operation)
			require.Equal(t, 1, fixture.faux.deferredFetchCount())
			_, err = fixture.lane.Resume(ctx)
			require.IsType(t, &harness.NothingToResume{}, err)
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:332
	t.Run("aborts and reconciles the current operation", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			acceptPublicRun(t, fixture.lane, "run")
			result, err := fixture.lane.Abort(t.Context())
			require.NoError(t, err)
			require.Equal(t, agentharness.AbortOutcome{OperationID: "run", Steer: []agent.AgentMessage{}, FollowUp: []agent.AgentMessage{}}, result)
			require.Nil(t, fixture.lane.SnapshotState().Operation)
			require.Zero(t, fixture.faux.callCount())
			_, err = fixture.lane.Abort(t.Context())
			require.IsType(t, &harness.NoActiveOperation{}, err)
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:348
	t.Run("waits for an operation that has no installed drive", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			ctx := t.Context()
			acceptPublicRun(t, fixture.lane, "run")
			waiting := asyncLaneCall(func() (struct{}, error) { return struct{}{}, fixture.lane.WaitForIdle(ctx) })
			synctest.Wait()
			requireLanePending(t, waiting)
			fixture.faux.setResponses([]ai.FauxResponseStep{laneFauxResponse("answer")})
			_, err := fixture.lane.Drive(ctx, agentharness.DriveOptions{OperationID: "run"})
			require.NoError(t, err)
			awaitLaneCall(t, waiting)
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:364
	t.Run("serializes concurrent runWhenIdle callbacks", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			ctx := t.Context()
			started, release := laneTestBarrier(t)
			order := []string{}
			first := asyncLaneCall(func() (struct{}, error) {
				return struct{}{}, fixture.lane.RunWhenIdle(ctx, func(harness.Context) error {
					order = append(order, "first:start")
					close(started)
					<-release.done
					order = append(order, "first:end")
					return nil
				})
			})
			<-started
			second := asyncLaneCall(func() (struct{}, error) {
				return struct{}{}, fixture.lane.RunWhenIdle(ctx, func(harness.Context) error { order = append(order, "second"); return nil })
			})
			synctest.Wait()
			require.Equal(t, []string{"first:start"}, order)
			release.open()
			awaitLaneCall(t, first)
			awaitLaneCall(t, second)
			require.Equal(t, []string{"first:start", "first:end", "second"}, order)
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:387
	t.Run("owns the idle window while runWhenIdle executes", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			ctx := t.Context()
			started, release := laneTestBarrier(t)
			callback := asyncLaneCall(func() (struct{}, error) {
				return struct{}{}, fixture.lane.RunWhenIdle(ctx, func(harness.Context) error { close(started); <-release.done; return nil })
			})
			<-started
			acceptance := asyncLaneCall(func() (agentharness.OperationAdmission, error) {
				text := "after"
				return fixture.lane.Accept(ctx, agentharness.OperationRequest{Kind: agentharness.RequestPrompt, PromptText: &text})
			})
			synctest.Wait()
			requireLanePending(t, acceptance)
			release.open()
			awaitLaneCall(t, callback)
			admission := awaitLaneCall(t, acceptance)
			fixture.faux.setResponses([]ai.FauxResponseStep{laneFauxResponse("answer")})
			_, err := fixture.lane.Drive(ctx, agentharness.DriveOptions{OperationID: admission.OperationID})
			require.NoError(t, err)
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:412
	t.Run("allows coherent lane reads from an idle callback", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			require.NoError(t, fixture.lane.RunWhenIdle(t.Context(), func(ctx harness.Context) error {
				execution, err := fixture.lane.InspectExecution(ctx)
				if err != nil {
					return err
				}
				watch, err := fixture.lane.Watch(ctx)
				if err != nil {
					return err
				}
				defer watch.Unsubscribe()
				assert.Nil(t, execution.Current)
				assert.Nil(t, watch.Snapshot().Operation)
				return nil
			}))
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:424
	t.Run("releases idle ownership when the callback fails", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			failure := errors.New("callback failed")
			require.Same(t, failure, fixture.lane.RunWhenIdle(t.Context(), func(harness.Context) error { return failure }))
			text := "after"
			_, err := fixture.lane.Accept(t.Context(), agentharness.OperationRequest{Kind: agentharness.RequestPrompt, PromptText: &text})
			require.NoError(t, err)
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:436
	t.Run("close waits for an already-running idle callback", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			ctx := t.Context()
			started, release := laneTestBarrier(t)
			callback := asyncLaneCall(func() (struct{}, error) {
				return struct{}{}, fixture.lane.RunWhenIdle(ctx, func(harness.Context) error { close(started); <-release.done; return nil })
			})
			<-started
			closing := asyncLaneCall(func() (struct{}, error) { return struct{}{}, fixture.harness.Close(ctx) })
			synctest.Wait()
			requireLanePending(t, closing)
			release.open()
			awaitLaneCall(t, callback)
			awaitLaneCall(t, closing)
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:457
	t.Run("installs one pass and joins same-operation callers", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			ctx := t.Context()
			started, release, response := blockedLaneResponse(t, "answer")
			fixture.faux.setResponses([]ai.FauxResponseStep{response})
			admission := acceptPublicRun(t, fixture.lane, "run")
			first := asyncLaneCall(func() (agentharness.DriveOutcome, error) {
				return fixture.lane.Drive(ctx, agentharness.DriveOptions{OperationID: admission.OperationID})
			})
			<-started
			second := asyncLaneCall(func() (agentharness.DriveOutcome, error) {
				return fixture.lane.Drive(ctx, agentharness.DriveOptions{OperationID: admission.OperationID})
			})
			synctest.Wait()
			require.NotNil(t, fixture.lane.CurrentDrive())
			require.Equal(t, admission.OperationID, fixture.lane.CurrentDrive().OperationID)
			release()
			firstResult := awaitLaneCall(t, first)
			secondResult := awaitLaneCall(t, second)
			requireLaneSettled(t, firstResult, nil, "completed")
			require.Equal(t, "run", firstResult.Outcome.OperationID)
			require.Equal(t, firstResult, secondResult)
			require.Equal(t, 1, fixture.faux.callCount())
			require.Nil(t, fixture.lane.CurrentDrive())
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:486
	t.Run("returns old result records without disturbing the current operation", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			ctx := t.Context()
			fixture.faux.setResponses([]ai.FauxResponseStep{laneFauxResponse("first")})
			acceptPublicRun(t, fixture.lane, "first")
			first, err := fixture.lane.Drive(ctx, agentharness.DriveOptions{OperationID: "first"})
			require.NoError(t, err)
			require.Equal(t, agentharness.DriveSettled, first.Kind)
			require.NotNil(t, first.Outcome)
			require.Equal(t, "first", first.Outcome.OperationID)
			acceptPublicRun(t, fixture.lane, "second")
			old, err := fixture.lane.Drive(ctx, agentharness.DriveOptions{OperationID: "first"})
			require.NoError(t, err)
			require.Equal(t, agentharness.DriveSettled, old.Kind)
			require.NotNil(t, old.Outcome)
			require.Equal(t, "first", old.Outcome.OperationID)
			operation := fixture.lane.SnapshotState().Operation
			require.NotNil(t, operation)
			require.Equal(t, "second", operation.Meta.OperationID)
			require.Nil(t, fixture.lane.CurrentDrive())
			fixture.faux.setResponses([]ai.FauxResponseStep{laneFauxResponse("second")})
			second, err := fixture.lane.Drive(ctx, agentharness.DriveOptions{OperationID: "second"})
			require.NoError(t, err)
			require.Equal(t, agentharness.DriveSettled, second.Kind)
			require.NotNil(t, second.Outcome)
			require.Equal(t, "second", second.Outcome.OperationID)
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:510
	t.Run("isolates stale operation ids", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			acceptPublicRun(t, fixture.lane, "current")
			_, err := fixture.lane.Drive(t.Context(), agentharness.DriveOptions{OperationID: "stale"})
			var mismatch *harness.OperationMismatch
			require.ErrorAs(t, err, &mismatch)
			require.Equal(t, "stale", mismatch.ExpectedOperationID)
			require.NotNil(t, mismatch.CurrentOperationID)
			require.Equal(t, "current", *mismatch.CurrentOperationID)
			require.Nil(t, fixture.lane.CurrentDrive())
			require.Zero(t, fixture.faux.callCount())
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:522
	t.Run("does not install for a caller already cancelled", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			acceptPublicRun(t, fixture.lane, "run")
			caller, cancel := harness.WithCancel(t.Context())
			cancelled := errors.New("caller cancelled")
			cancel(cancelled)
			_, err := fixture.lane.Drive(caller, agentharness.DriveOptions{OperationID: "run"})
			require.Same(t, cancelled, err)
			require.Nil(t, fixture.lane.CurrentDrive())
			require.Zero(t, fixture.faux.callCount())
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:534
	t.Run("caller cancellation stops only that caller's observation", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			ctx := t.Context()
			started, release, response := blockedLaneResponse(t, "answer")
			fixture.faux.setResponses([]ai.FauxResponseStep{response})
			acceptPublicRun(t, fixture.lane, "run")
			owner := asyncLaneCall(func() (agentharness.DriveOutcome, error) {
				return fixture.lane.Drive(ctx, agentharness.DriveOptions{OperationID: "run"})
			})
			<-started
			caller, cancel := harness.WithCancel(ctx)
			observer := asyncLaneCall(func() (agentharness.DriveOutcome, error) {
				return fixture.lane.Drive(caller, agentharness.DriveOptions{OperationID: "run"})
			})
			synctest.Wait()
			cancelled := errors.New("observer cancelled")
			cancel(cancelled)
			require.Same(t, cancelled, (<-observer).err)
			require.NotNil(t, fixture.lane.CurrentDrive())
			require.Equal(t, "run", fixture.lane.CurrentDrive().OperationID)
			release()
			result := awaitLaneCall(t, owner)
			require.Equal(t, agentharness.DriveSettled, result.Kind)
			require.Equal(t, 1, fixture.faux.callCount())
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:560
	t.Run("close rejects observation without waiting for a non-cooperative effect", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			ctx := t.Context()
			started, release, response := blockedLaneResponse(t, "late")
			fixture.faux.setResponses([]ai.FauxResponseStep{response})
			acceptPublicRun(t, fixture.lane, "run")
			observation := asyncLaneCall(func() (agentharness.DriveOutcome, error) {
				return fixture.lane.Drive(ctx, agentharness.DriveOptions{OperationID: "run"})
			})
			<-started
			require.NoError(t, fixture.harness.Close(ctx))
			require.IsType(t, &harness.HarnessClosed{}, (<-observation).err)
			release()
			synctest.Wait()
			require.Nil(t, fixture.lane.CurrentDrive())
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:581
	t.Run("exposes durable abort and reconciles through public drive", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			ctx := t.Context()
			acceptPublicRun(t, fixture.lane, "run")
			request, err := fixture.lane.RequestAbort(ctx, "run")
			require.NoError(t, err)
			require.Equal(t, agentharness.AbortRequest{OperationID: "run", NewlyRequested: true, Steer: []agent.AgentMessage{}, FollowUp: []agent.AgentMessage{}}, request)
			require.Nil(t, fixture.lane.CurrentDrive())
			result, err := fixture.lane.Drive(ctx, agentharness.DriveOptions{OperationID: "run"})
			requireLaneSettled(t, result, err, "aborted")
			require.Equal(t, "run", result.Outcome.OperationID)
			require.Zero(t, fixture.faux.callCount())
			_, err = fixture.lane.RequestAbort(ctx, "run")
			var mismatch *harness.OperationMismatch
			require.ErrorAs(t, err, &mismatch)
			require.Equal(t, "run", mismatch.ExpectedOperationID)
			require.NotNil(t, mismatch.LastOperationID)
			require.Equal(t, "run", *mismatch.LastOperationID)
		})
	})
	// upstream: packages/agent/test/harness/runtime/drive-public.test.ts:601
	t.Run("faults the harness when a detached pass fails", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fixture := newPublicLane(t, false, agentharness.Resources{})
			ctx := t.Context()
			_, err := fixture.harness.Hooks().OnBeforeDrive(func(harness.Context, agentharness.BeforeDriveEvent) error { return errors.New("drive failed") }, agentharness.HookOptions{})
			require.NoError(t, err)
			acceptPublicRun(t, fixture.lane, "run")
			_, err = fixture.lane.Drive(ctx, agentharness.DriveOptions{OperationID: "run"})
			require.IsType(t, &harness.HarnessFault{}, err)
			require.Nil(t, fixture.lane.CurrentDrive())
			_, err = fixture.lane.GetTipID(ctx)
			require.IsType(t, &harness.HarnessFault{}, err)
		})
	})
}
