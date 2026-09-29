package execution_test

import (
	"errors"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/compaction"
	"github.com/MichaelKinsy/PiG/agent/harness/execution"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestPortWave01ExecutionPrimitivesHooks(t *testing.T) {
	t.Parallel()
	// upstream: packages/agent/test/harness/execution-primitives.test.ts:55
	t.Run("aggregates before_run messages in registration order with each handler seeing prior output", func(t *testing.T) {
		failures := []error{}
		hooks := agentharness.NewHookRegistry(func(_ harness.Context, err error, _ agentharness.HookName, _ string) error {
			failures = append(failures, err)
			return nil
		})
		secondCalls := 0
		mustRegister(t)(hooks.OnBeforeRun(func(harness.Context, agentharness.BeforeRunEvent) (*agentharness.BeforeRunResult, error) {
			return &agentharness.BeforeRunResult{Messages: []agent.AgentMessage{userMessage("first", 2)}}, nil
		}, agentharness.HookOptions{}))
		mustRegister(t)(hooks.OnBeforeRun(func(_ harness.Context, event agentharness.BeforeRunEvent) (*agentharness.BeforeRunResult, error) {
			secondCalls++
			requireEqual(t, len(event.Prompt), 2)
			return &agentharness.BeforeRunResult{Messages: []agent.AgentMessage{userMessage("second", 3)}}, nil
		}, agentharness.HookOptions{}))
		result, err := hooks.RunBeforeRun(harness.BackgroundContext(), newEffectGate(t), agentharness.BeforeRunEvent{
			HookScope: agentharness.HookScope{Lane: "main", RunID: "run"},
			Prompt:    []agent.AgentMessage{userMessage("prompt", 1)}, Resources: agentharness.Resources{},
		})
		requireNoError(t, err)
		requireEqual(t, result, &agentharness.BeforeRunResult{Messages: []agent.AgentMessage{userMessage("first", 2), userMessage("second", 3)}})
		requireEqual(t, secondCalls, 1)
		requireEqual(t, failures, []error{})
	})

	// upstream: packages/agent/test/harness/execution-primitives.test.ts:89
	t.Run("checks the effect gate immediately before the complete before_run pipeline", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			hooks := agentharness.NewHookRegistry(ignoreHookError)
			calls := []string{}
			release, resolve := deferred()
			defer resolve()
			mustRegister(t)(hooks.OnBeforeRun(func(harness.Context, agentharness.BeforeRunEvent) (*agentharness.BeforeRunResult, error) {
				calls = append(calls, "first:start")
				<-release
				calls = append(calls, "first:end")
				return nil, nil
			}, agentharness.HookOptions{}))
			mustRegister(t)(hooks.OnBeforeRun(func(harness.Context, agentharness.BeforeRunEvent) (*agentharness.BeforeRunResult, error) {
				calls = append(calls, "second")
				return nil, nil
			}, agentharness.HookOptions{}))
			event := agentharness.BeforeRunEvent{
				HookScope: agentharness.HookScope{Lane: "main", RunID: "run"}, Prompt: []agent.AgentMessage{}, Resources: agentharness.Resources{},
			}
			closed := errors.New("closed")
			closedGate, control := execution.CreateGate()
			control.Close(closed)
			_, err := hooks.RunBeforeRun(harness.BackgroundContext(), closedGate, event)
			requireSameError(t, err, closed)
			requireEqual(t, calls, []string{})

			gate := newEffectGate(t)
			running := make(chan error, 1)
			go func() {
				_, err := hooks.RunBeforeRun(harness.BackgroundContext(), gate, event)
				running <- err
			}()
			// Go's blocking runner runs on an owned goroutine; quiescence observes the upstream first-await boundary.
			synctest.Wait()
			requireEqual(t, calls, []string{"first:start"})
			mustRegister(t)(hooks.OnBeforeRun(func(harness.Context, agentharness.BeforeRunEvent) (*agentharness.BeforeRunResult, error) {
				calls = append(calls, "late")
				return nil, nil
			}, agentharness.HookOptions{}))
			hooks.Close(closed)
			resolve()
			requireNoError(t, <-running)
			requireEqual(t, calls, []string{"first:start", "first:end", "second"})
		})
	})

	// upstream: packages/agent/test/harness/execution-primitives.test.ts:158
	t.Run("passes tool handlers a child context of the active hook span", func(t *testing.T) {
		telemetry := &harness.InMemoryTelemetryContext{}
		valueKey := harness.CreateContextKey[string]("hook.test.value")
		hooks := agentharness.NewHookRegistry(ignoreHookError)
		var received string
		mustRegister(t)(hooks.OnBeforeTool(func(ctx harness.Context, _ agentharness.BeforeToolEvent) (*agentharness.BeforeToolResult, error) {
			received, _ = harness.ContextValue(ctx, valueKey)
			return nil, harness.GetTelemetryContext(ctx).StartSpan(harness.SpanOptions{Name: "handler.child"}, func(harness.TelemetrySpan) error { return nil })
		}, agentharness.HookOptions{}))
		err := telemetry.StartSpan(harness.SpanOptions{Name: "invocation"}, func(invocationSpan harness.TelemetrySpan) error {
			ctx := harness.WithContextValue(harness.WithTelemetryContext(harness.BackgroundContext(), invocationSpan), valueKey, "preserved")
			_, err := hooks.RunBeforeTool(ctx, newEffectGate(t), agentharness.BeforeToolEvent{
				HookScope: agentharness.HookScope{Lane: "main", RunID: "run"}, ToolCallID: "call", ToolName: "tool", Args: map[string]any{},
			})
			return err
		})
		requireNoError(t, err)
		requireEqual(t, received, "preserved")
		spans := telemetry.GetSpans()
		var invocation, hook, child *harness.RecordedTelemetrySpan
		for i := range spans {
			switch spans[i].Name {
			case "invocation":
				invocation = &spans[i]
			case "pi.harness.hook":
				hook = &spans[i]
			case "handler.child":
				child = &spans[i]
			}
		}
		if invocation == nil || hook == nil || child == nil {
			t.Fatalf("missing invocation, hook or child span: %#v", spans)
		}
		requireEqual(t, hook.ParentID, &invocation.ID)
		requireEqual(t, child.ParentID, &hook.ID)
	})

	// upstream: packages/agent/test/harness/execution-primitives.test.ts:221
	t.Run("chains transform_context output and isolates handler failures", func(t *testing.T) {
		failures := []error{}
		hooks := agentharness.NewHookRegistry(func(_ harness.Context, err error, _ agentharness.HookName, _ string) error {
			failures = append(failures, err)
			return nil
		})
		messages := []agent.AgentMessage{userMessage("transformed", 2)}
		mustRegister(t)(hooks.OnTransformContext(func(harness.Context, agentharness.TransformContextEvent) (*agentharness.TransformContextResult, error) {
			return &agentharness.TransformContextResult{Messages: messages, SystemPrompt: new("first")}, nil
		}, agentharness.HookOptions{}))
		check := func(event agentharness.TransformContextEvent) {
			requireEqual(t, len(event.Messages), len(messages))
			if &event.Messages[0] != &messages[0] {
				t.Fatal("handler did not receive the previous messages array")
			}
			requireEqual(t, event.SystemPrompt, "first")
		}
		mustRegister(t)(hooks.OnTransformContext(func(_ harness.Context, event agentharness.TransformContextEvent) (*agentharness.TransformContextResult, error) {
			check(event)
			return nil, errors.New("ignored transform failure")
		}, agentharness.HookOptions{}))
		mustRegister(t)(hooks.OnTransformContext(func(_ harness.Context, event agentharness.TransformContextEvent) (*agentharness.TransformContextResult, error) {
			check(event)
			return &agentharness.TransformContextResult{SystemPrompt: new("final")}, nil
		}, agentharness.HookOptions{}))
		result, err := hooks.RunTransformContext(harness.BackgroundContext(), newEffectGate(t), agentharness.TransformContextEvent{
			HookScope: agentharness.HookScope{Lane: "main", RunID: "run"}, Messages: []agent.AgentMessage{userMessage("original", 1)}, SystemPrompt: "base",
		})
		requireNoError(t, err)
		requireEqual(t, result, &agentharness.TransformContextResult{Messages: messages, SystemPrompt: new("final")})
		requireEqual(t, len(failures), 1)
		requireEqual(t, failures[0].Error(), "ignored transform failure")
	})

	// upstream: packages/agent/test/harness/execution-primitives.test.ts:256
	t.Run("preserves clear-all before_request patches across later handlers", func(t *testing.T) {
		hooks := agentharness.NewHookRegistry(ignoreHookError)
		mustRegister(t)(hooks.OnBeforeRequest(func(harness.Context, agentharness.BeforeRequestEvent) (*agentharness.BeforeRequestResult, error) {
			return &agentharness.BeforeRequestResult{StreamOptions: &harness.AgentHarnessStreamOptionsPatch{
				Headers: harness.MapPatch[string]{Present: true}, Metadata: harness.MapPatch[any]{Present: true},
			}}, nil
		}, agentharness.HookOptions{}))
		mustRegister(t)(hooks.OnBeforeRequest(func(_ harness.Context, event agentharness.BeforeRequestEvent) (*agentharness.BeforeRequestResult, error) {
			requireEqual(t, event.StreamOptions.Headers, map[string]string(nil))
			requireEqual(t, event.StreamOptions.Metadata, map[string]any(nil))
			return &agentharness.BeforeRequestResult{StreamOptions: &harness.AgentHarnessStreamOptionsPatch{
				Headers:  harness.MapPatch[string]{Present: true, Entries: map[string]*string{"c": new("3")}},
				Metadata: harness.MapPatch[any]{Present: true, Entries: map[string]*any{"y": new(any(2))}},
			}}, nil
		}, agentharness.HookOptions{}))
		faux := ai.NewFauxProvider(ai.FauxConfig{})
		t.Cleanup(func() { requireNoError(t, faux.Close()) })
		result, err := hooks.RunBeforeRequest(harness.BackgroundContext(), newEffectGate(t), agentharness.BeforeRequestEvent{
			HookScope: agentharness.HookScope{Lane: "main", RunID: "run"}, Model: faux.GetModel(), Step: "assistant", Attempt: 1,
			StreamOptions: harness.AgentHarnessStreamOptions{Headers: map[string]string{"a": "1", "b": "2"}, Metadata: map[string]any{"x": 1}},
		})
		requireNoError(t, err)
		requireEqual(t, result, &agentharness.BeforeRequestResult{StreamOptions: &harness.AgentHarnessStreamOptionsPatch{
			Headers:  harness.MapPatch[string]{Present: true, Entries: map[string]*string{"a": nil, "b": nil, "c": new("3")}},
			Metadata: harness.MapPatch[any]{Present: true, Entries: map[string]*any{"x": nil, "y": new(any(2))}},
		}})
	})

	// upstream: packages/agent/test/harness/execution-primitives.test.ts:285
	t.Run("preserves earlier after_tool fields when a later patch returns undefined", func(t *testing.T) {
		hooks := agentharness.NewHookRegistry(ignoreHookError)
		mustRegister(t)(hooks.OnAfterTool(func(harness.Context, agentharness.AfterToolEvent) (*agentharness.AfterToolResult, error) {
			return &agentharness.AfterToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "patched"}}}, nil
		}, agentharness.HookOptions{}))
		mustRegister(t)(hooks.OnAfterTool(func(harness.Context, agentharness.AfterToolEvent) (*agentharness.AfterToolResult, error) {
			return &agentharness.AfterToolResult{Content: nil, IsError: new(false)}, nil
		}, agentharness.HookOptions{}))
		result, err := hooks.RunAfterTool(harness.BackgroundContext(), newEffectGate(t), agentharness.AfterToolEvent{
			HookScope: agentharness.HookScope{Lane: "main", RunID: "run"}, ToolCallID: "call", ToolName: "tool", Args: map[string]any{},
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "raw"}}, IsError: true,
		})
		requireNoError(t, err)
		requireEqual(t, result, &agentharness.AfterToolResult{
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "patched"}}, IsError: new(false),
		})
	})

	// upstream: packages/agent/test/harness/execution-primitives.test.ts:306
	t.Run("accepts explicit false structural declines and rejects true conflicts", func(t *testing.T) {
		failures := []error{}
		hooks := agentharness.NewHookRegistry(func(_ harness.Context, err error, _ agentharness.HookName, _ string) error {
			failures = append(failures, err)
			return nil
		})
		ignored := &compaction.BranchSummaryResult{Summary: "ignored", ReadFiles: []string{}, ModifiedFiles: []string{}}
		selected := &compaction.BranchSummaryResult{Summary: "selected", ReadFiles: []string{}, ModifiedFiles: []string{}}
		mustRegister(t)(hooks.OnBeforeNavigation(func(harness.Context, agentharness.BeforeNavigationEvent) (*agentharness.BeforeNavigationResult, error) {
			return &agentharness.BeforeNavigationResult{Decline: true, Summary: ignored}, nil
		}, agentharness.HookOptions{}))
		mustRegister(t)(hooks.OnBeforeNavigation(func(harness.Context, agentharness.BeforeNavigationEvent) (*agentharness.BeforeNavigationResult, error) {
			return &agentharness.BeforeNavigationResult{Decline: false, Summary: selected}, nil
		}, agentharness.HookOptions{}))
		result, err := hooks.RunBeforeNavigation(harness.BackgroundContext(), newEffectGate(t), agentharness.BeforeNavigationEvent{
			HookScope: agentharness.HookScope{Lane: "main", RunID: "run"}, TargetID: "target",
			Preparation: compaction.BranchPreparation{Messages: []agent.AgentMessage{}, FileOps: compaction.CreateFileOps(), TotalTokens: 0},
		})
		requireNoError(t, err)
		requireEqual(t, result, &agentharness.BeforeNavigationResult{Decline: false, Summary: selected})
		requireEqual(t, len(failures), 1)
		if !strings.Contains(failures[0].Error(), "cannot return both decline and summary") {
			t.Fatalf("conflict = %v", failures[0])
		}
	})

	// upstream: packages/agent/test/harness/execution-primitives.test.ts:336
	t.Run("admits the complete before_drive pipeline as one gated effect", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			hooks := agentharness.NewHookRegistry(ignoreHookError)
			calls := []string{}
			release, resolve := deferred()
			defer resolve()
			mustRegister(t)(hooks.OnBeforeDrive(func(harness.Context, agentharness.BeforeDriveEvent) error {
				calls = append(calls, "first:start")
				<-release
				calls = append(calls, "first:end")
				return nil
			}, agentharness.HookOptions{}))
			mustRegister(t)(hooks.OnBeforeDrive(func(harness.Context, agentharness.BeforeDriveEvent) error {
				calls = append(calls, "second")
				return nil
			}, agentharness.HookOptions{}))
			event := agentharness.BeforeDriveEvent{HookScope: agentharness.HookScope{Lane: "main", RunID: "run"}, Operation: agentharness.OperationRun}
			abortFirstGate, abortFirstControl := execution.CreateGate()
			cancellation := func() error { return nil }
			abortFirstControl.BeginAbort(cancellation)
			defer abortFirstControl.Close(errors.New("test cleanup"))
			err := hooks.RunBeforeDrive(harness.BackgroundContext(), abortFirstGate, event)
			if _, ok := err.(*execution.AbortRequested); !ok { //nolint:errorlint // Upstream asserts the thrown AbortRequested instance, not a wrapped cause.
				t.Fatalf("abort-first error = %v (%T), want AbortRequested", err, err)
			}
			requireEqual(t, calls, []string{})

			startFirstGate, startFirstControl := execution.CreateGate()
			defer startFirstControl.Close(errors.New("test cleanup"))
			running := make(chan error, 1)
			go func() { running <- hooks.RunBeforeDrive(harness.BackgroundContext(), startFirstGate, event) }()
			synctest.Wait()
			requireEqual(t, calls, []string{"first:start"})
			startFirstControl.BeginAbort(cancellation)
			startFirstControl.SignalAbort()
			resolve()
			requireNoError(t, <-running)
			requireEqual(t, calls, []string{"first:start", "first:end", "second"})
		})
	})
}

func TestPortWave01ExecutionPrimitivesEvents(t *testing.T) {
	t.Parallel()
	// upstream: packages/agent/test/harness/execution-primitives.test.ts:370
	t.Run("buffers between snapshot and start, then delivers each event once in order", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			bus := agentharness.NewHarnessEventBus()
			defer bus.Close(errors.New("test cleanup"))
			snapshot := map[string]*string{"tipId": nil}
			watcher, err := agentharness.Watch(bus, snapshot, func(agentharness.HarnessEvent) bool { return true }, nil)
			requireNoError(t, err)
			defer watcher.Unsubscribe()
			bus.Emit(harness.BackgroundContext(), runStartEvent("one", "main"))
			seen := []string{}
			requireNoError(t, watcher.Start(func(_ harness.Context, event agentharness.HarnessEvent) error {
				seen = append(seen, string(event.Type())+":"+event.Payload.(agentharness.RunStartPayload).RunID)
				return nil
			}))
			bus.Emit(harness.BackgroundContext(), runStartEvent("two", "main"))
			synctest.Wait()
			requireEqual(t, watcher.Snapshot(), map[string]*string{"tipId": nil})
			requireEqual(t, seen, []string{"run_start:one", "run_start:two"})
			watcher.Unsubscribe()
			bus.Emit(harness.BackgroundContext(), runStartEvent("three", "main"))
			synctest.Wait()
			requireEqual(t, len(seen), 2)
		})
	})

	// upstream: packages/agent/test/harness/execution-primitives.test.ts:387
	t.Run("drops pre-snapshot delivery and holds later events when resnapshotting inside a listener", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			bus := agentharness.NewHarnessEventBus()
			defer bus.Close(errors.New("test cleanup"))
			listenerStarted, startListener := deferred()
			releaseListener, release := deferred()
			defer release()
			resnapshotDone, finishResnapshot := deferred()
			watcher, err := agentharness.Watch(bus, map[string]string{"version": "old"}, func(agentharness.HarnessEvent) bool { return true },
				func(_ harness.Context, markBoundary func() error) (map[string]string, error) {
					if err := markBoundary(); err != nil {
						return nil, err
					}
					return map[string]string{"version": "fresh"}, nil
				})
			requireNoError(t, err)
			defer watcher.Unsubscribe()
			queueEvents := []string{}
			var resnapshotError error
			requireNoError(t, watcher.Start(func(ctx harness.Context, event agentharness.HarnessEvent) error {
				switch payload := event.Payload.(type) {
				case agentharness.RunStartPayload:
					if payload.RunID == "blocking" {
						startListener()
						<-releaseListener
					}
				case agentharness.NavigationEndPayload:
					_, resnapshotError = watcher.Resnapshot(ctx)
					finishResnapshot()
				case agentharness.QueueUpdatePayload:
					entryID := "empty"
					if len(payload.Queues) != 0 {
						entryID = payload.Queues[0].EntryID
					}
					queueEvents = append(queueEvents, entryID)
				}
				return nil
			}))
			bus.Emit(harness.BackgroundContext(), runStartEvent("blocking", "main"))
			<-listenerStarted
			queued := make(chan struct{})
			go func() {
				defer close(queued)
				bus.EmitBatch(harness.BackgroundContext(), []agentharness.HarnessEvent{
					{Payload: agentharness.NavigationEndPayload{RunID: "navigation", Status: "completed", FromTipID: nil, TipID: nil, EndedAt: 2}, Lane: "main"},
					queueUpdateEvent("stale", 2),
				})
			}()
			synctest.Wait()
			release()
			<-queued
			<-resnapshotDone
			requireNoError(t, resnapshotError)
			synctest.Wait()
			requireEqual(t, watcher.Snapshot(), map[string]string{"version": "fresh"})
			requireEqual(t, queueEvents, []string{})
			bus.Emit(harness.BackgroundContext(), queueUpdateEvent("later", 3))
			synctest.Wait()
			requireEqual(t, queueEvents, []string{"later"})
		})
	})

	// upstream: packages/agent/test/harness/execution-primitives.test.ts:464
	t.Run("isolates each listener from payload mutation", func(t *testing.T) {
		bus := agentharness.NewHarnessEventBus()
		defer bus.Close(errors.New("test cleanup"))
		observed := []string{}
		mustRegister(t)(bus.On(agentharness.EventConfigUpdate, func(_ harness.Context, event agentharness.HarnessEvent) error {
			// A pointer payload retains the upstream object's mutable array property; appending only a local Go slice header would not test isolation.
			payload := event.Payload.(*agentharness.ConfigUpdatePayload)
			if payload.Property == agentharness.ConfigActiveTools {
				payload.Value = append(payload.Value.([]string), "mutated")
			}
			return nil
		}))
		mustRegister(t)(bus.On(agentharness.EventConfigUpdate, func(_ harness.Context, event agentharness.HarnessEvent) error {
			payload := event.Payload.(*agentharness.ConfigUpdatePayload)
			if payload.Property == agentharness.ConfigActiveTools {
				observed = payload.Value.([]string)
			}
			return nil
		}))
		bus.Emit(harness.BackgroundContext(), agentharness.HarnessEvent{Payload: &agentharness.ConfigUpdatePayload{
			Property: agentharness.ConfigActiveTools, Value: []string{"read"}, Previous: []string{},
		}, Lane: "main"})
		requireEqual(t, observed, []string{"read"})
	})

	// upstream: packages/agent/test/harness/execution-primitives.test.ts:510
	t.Run("keeps concurrent batches contiguous with their emitting contexts", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			bus := agentharness.NewHarnessEventBus()
			defer bus.Close(errors.New("test cleanup"))
			sourceKey := harness.CreateContextKey[string]("event.batch.source")
			firstContext := harness.WithContextValue(harness.BackgroundContext(), sourceKey, "first")
			secondContext := harness.WithContextValue(harness.BackgroundContext(), sourceKey, "second")
			type observation struct {
				runID string
				ctx   harness.Context
			}
			seen := []observation{}
			release, resolve := deferred()
			defer resolve()
			mustRegister(t)(bus.On(agentharness.EventRunStart, func(ctx harness.Context, event agentharness.HarnessEvent) error {
				seen = append(seen, observation{runID: event.Payload.(agentharness.RunStartPayload).RunID, ctx: ctx})
				<-release
				return nil
			}))
			first := make(chan struct{})
			go func() {
				defer close(first)
				bus.EmitBatch(firstContext, []agentharness.HarnessEvent{runStartEvent("a1", "main"), runStartEvent("a2", "main")})
			}()
			synctest.Wait()
			second := make(chan struct{})
			go func() {
				defer close(second)
				bus.EmitBatch(secondContext, []agentharness.HarnessEvent{runStartEvent("b1", "worker"), runStartEvent("b2", "worker")})
			}()
			synctest.Wait()
			resolve()
			<-first
			<-second
			runIDs := make([]string, len(seen))
			for i, entry := range seen {
				runIDs[i] = entry.runID
			}
			requireEqual(t, runIDs, []string{"a1", "a2", "b1", "b2"})
			for _, entry := range seen[:2] {
				if entry.ctx != firstContext {
					t.Fatal("first batch did not retain the emitting context identity")
				}
			}
			for _, entry := range seen[2:] {
				if entry.ctx != secondContext {
					t.Fatal("second batch did not retain the emitting context identity")
				}
			}
		})
	})

	// upstream: packages/agent/test/harness/execution-primitives.test.ts:642
	t.Run("isolates listener failures and emits handler_error", func(t *testing.T) {
		bus := agentharness.NewHarnessEventBus()
		defer bus.Close(errors.New("test cleanup"))
		failures := []string{}
		mustRegister(t)(bus.On(agentharness.EventRunStart, func(harness.Context, agentharness.HarnessEvent) error {
			return errors.New("listener failed")
		}))
		mustRegister(t)(bus.On(agentharness.EventHandlerError, func(_ harness.Context, event agentharness.HarnessEvent) error {
			failures = append(failures, event.Payload.(agentharness.HandlerErrorPayload).Error)
			return nil
		}))
		bus.Emit(harness.BackgroundContext(), runStartEvent("run", "main"))
		requireEqual(t, failures, []string{"listener failed"})
	})

	// upstream: packages/agent/test/harness/execution-primitives.test.ts:655
	t.Run("does not recurse when a handler_error listener fails", func(t *testing.T) {
		bus := agentharness.NewHarnessEventBus()
		defer bus.Close(errors.New("test cleanup"))
		handlerErrors := 0
		mustRegister(t)(bus.On(agentharness.EventRunStart, func(harness.Context, agentharness.HarnessEvent) error {
			return errors.New("listener failed")
		}))
		mustRegister(t)(bus.On(agentharness.EventHandlerError, func(harness.Context, agentharness.HarnessEvent) error {
			handlerErrors++
			return errors.New("error listener failed")
		}))
		bus.Emit(harness.BackgroundContext(), runStartEvent("run", "main"))
		requireEqual(t, handlerErrors, 1)
	})
}
