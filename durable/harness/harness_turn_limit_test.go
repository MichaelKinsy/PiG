// Ports packages/durable/test/harness-turn-limit.test.ts: per-conversation turn limits (AgentState.maxTurns) and continuing a
// run that reached one (Pi #11043).

package harness

import (
	"context"
	"math"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// turnTool is a tool `name` whose execution runs execute.
func turnTool(name string, execute func(ctx context.Context, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error), replay ...durable.ToolReplay) *durable.ToolRegistration {
	registration := durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{Name: name, Description: name, Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
		Execute: func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return execute(ctx, api)
		},
	}
	if len(replay) > 0 {
		registration.Replay = replay[0]
	}
	return DefineTool(registration)
}

// turnRound is a tool-calling answer with one noop call.
func turnRound(id string) ai.FauxResponseStep {
	return toolCallStep(ai.FauxToolCall("noop", map[string]any{}, &ai.FauxToolCallOptions{ID: id}))
}

func noopResult() (durable.ToolExecutionResult, error) {
	return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}, nil
}

// turnLimited is a chat with a noop tool and the given turn limit (0 for none).
func turnLimited(t *testing.T, maxTurns float64, configure ...func(setup *chatState)) (*chatState, Harness, Conversation) {
	t.Helper()
	setup := chatSetup(t)
	addTool(t, setup.Registry, turnTool("noop", func(context.Context, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return noopResult()
	}))
	for _, apply := range configure {
		apply(setup)
	}
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	if maxTurns != 0 {
		if err := root.Configure(testContext, AgentChange{MaxTurns: SetTo(maxTurns)}); err != nil {
			t.Fatal(err)
		}
	}
	return setup, harness, root
}

func turnLastKind(t *testing.T, root Conversation) string {
	t.Helper()
	entries := allEntries(t, root)
	return entries[len(entries)-1].Kind
}

func detailOf(t *testing.T, settled durable.SubmissionRecord) string {
	t.Helper()
	detail, _ := settled.Detail.(string)
	return detail
}

func TestTurnLimit(t *testing.T) {
	// #11043
	t.Run("ends a run after the tool round that reaches the limit, like a failure", func(t *testing.T) {
		// upstream: packages/durable/test/harness-turn-limit.test.ts:63
		setup, harness, root := turnLimited(t, 1)
		setup.Faux.SetResponses([]ai.FauxResponseStep{turnRound("c1"), fauxAnswer("done")})
		submission := submit(t, root, inputDraft("go"))
		settled := must(submission.Wait(testContext))
		expectSettled(t, settled, durable.SubmissionUnanswered, "turn_limit")
		if detail := detailOf(t, settled); detail != "1 turn" {
			t.Fatalf("detail = %q, want 1 turn", detail)
		}
		if calls := setup.Faux.CallCount(); calls != 1 {
			t.Fatalf("callCount = %d, want 1", calls)
		}
		// The transcript ends with the round's results; the generation completed, so nothing is reported.
		if kind := turnLastKind(t, root); kind != durable.ToolResultEntry.Kind {
			t.Fatalf("last kind = %s, want %s", kind, durable.ToolResultEntry.Kind)
		}
		failure := runFailureOf(t, harness, root)
		expectRunFailure(t, failure, []durable.SubmissionId{submission.Id()}, RunTurnLimit, new("1 turn"))
		generations := conversationTasks(t, harness, root)
		var generation *durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
		for index := range generations {
			if generations[index].Kind == generationKind {
				generation = &generations[index]
			}
		}
		if generation == nil || generation.State.Status != durable.TaskTerminal || generation.State.Outcome == nil || generation.State.Outcome.Status != durable.OutcomeCompleted {
			t.Fatalf("generation = %s", jsonText(t, generation))
		}
		if reports := setup.Reports.all(); len(reports) != 0 {
			t.Fatalf("reports = %v", reports)
		}
		closeHarness(t, harness)
	})

	// #11043
	t.Run("counts tool rounds only: a run answers below its limit, and an answer at the limit settles done", func(t *testing.T) {
		// upstream: packages/durable/test/harness-turn-limit.test.ts:92
		setup, harness, root := turnLimited(t, 2)
		setup.Faux.SetResponses([]ai.FauxResponseStep{turnRound("c1"), fauxAnswer("done"), turnRound("c2"), turnRound("c3"), fauxAnswer("done")})
		if status := must(submit(t, root, inputDraft("one")).Wait(testContext)).Status; status != durable.SubmissionDone {
			t.Fatalf("one = %s, want done", status)
		}
		// A new run starts with a fresh budget.
		second := must(submit(t, root, inputDraft("two")).Wait(testContext))
		expectSettled(t, second, durable.SubmissionUnanswered, "turn_limit")
		if detail := detailOf(t, second); detail != "2 turns" {
			t.Fatalf("detail = %q, want 2 turns", detail)
		}
		closeHarness(t, harness)
	})

	// #11043
	t.Run("runs onYield at the limit, and a continuation starts the count over", func(t *testing.T) {
		// upstream: packages/durable/test/harness-turn-limit.test.ts:104
		yields := 0
		setup, harness, root := turnLimited(t, 2, func(chat *chatState) {
			addHooks(t, chat.Registry, GenerationTask.Erased(), &GenerationHooks{OnYield: func(context.Context, ai.AssistantMessage, HookApi) (*YieldContinue, error) {
				yields++
				if yields == 1 {
					return &YieldContinue{Continue: ai.UserText("keep going")}, nil
				}
				return nil, nil
			}})
		})
		// Round (1 turn), answer: the continuation resets the count; then two rounds reach the limit.
		setup.Faux.SetResponses([]ai.FauxResponseStep{turnRound("c1"), fauxAnswer("done"), turnRound("c2"), turnRound("c3"), fauxAnswer("done")})
		settled := must(submit(t, root, inputDraft("go")).Wait(testContext))
		expectSettled(t, settled, durable.SubmissionUnanswered, "turn_limit")
		if yields != 1 {
			t.Fatalf("yields = %d, want 1", yields)
		}
		if calls := setup.Faux.CallCount(); calls != 4 {
			t.Fatalf("callCount = %d, want 4", calls)
		}
		closeHarness(t, harness)
	})

	// #11043
	t.Run("settles a steer that joined the run at an earlier round with turn_limit too", func(t *testing.T) {
		// upstream: packages/durable/test/harness-turn-limit.test.ts:123
		gate, started := deferred(), deferred()
		setup := chatSetup(t)
		calls := 0
		addTool(t, setup.Registry, turnTool("noop", func(ctx context.Context, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			calls++
			if calls == 1 {
				started.resolve()
				if err := gate.wait(ctx); err != nil {
					return durable.ToolExecutionResult{}, err
				}
			}
			return noopResult()
		}))
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		if err := root.Configure(testContext, AgentChange{MaxTurns: SetTo(2.0)}); err != nil {
			t.Fatal(err)
		}
		setup.Faux.SetResponses([]ai.FauxResponseStep{turnRound("c1"), turnRound("c2"), fauxAnswer("done")})
		first := submit(t, root, inputDraft("go"))
		if err := started.wait(testContext); err != nil {
			t.Fatal(err)
		}
		steer := submit(t, root, inputDraft("also this", durable.WhenBusySteer))
		gate.resolve()
		for _, submission := range []durable.Submission{first, steer} {
			settled := must(submission.Wait(testContext))
			expectSettled(t, settled, durable.SubmissionUnanswered, "turn_limit")
			if detail := detailOf(t, settled); detail != "2 turns" {
				t.Fatalf("detail = %q, want 2 turns", detail)
			}
		}
		if got := runFailureOf(t, harness, root).Inputs; !slices.Equal(got, []durable.SubmissionId{first.Id(), steer.Id()}) {
			t.Fatalf("runFailure inputs = %v", got)
		}
		if calls := setup.Faux.CallCount(); calls != 2 {
			t.Fatalf("callCount = %d, want 2", calls)
		}
		closeHarness(t, harness)
	})

	// #11043
	t.Run("leaves items queued during the last round to afterFailedRun", func(t *testing.T) {
		// upstream: packages/durable/test/harness-turn-limit.test.ts:151
		for _, policy := range []durable.AfterFailedRun{durable.AfterFailedRunKeep, durable.AfterFailedRunContinue, durable.AfterFailedRunWithdraw} {
			gate, started := deferred(), deferred()
			setup := chatSetup(t)
			setup.SetSettings(func(settings *HarnessSettings) { settings.AfterFailedRun = policy })
			addTool(t, setup.Registry, turnTool("noop", func(ctx context.Context, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				started.resolve()
				if err := gate.wait(ctx); err != nil {
					return durable.ToolExecutionResult{}, err
				}
				return noopResult()
			}))
			setup.Faux.SetResponses([]ai.FauxResponseStep{turnRound("c1"), fauxAnswer("done")})
			harness, root := openChat(t, storage.NewMemoryStorage(), setup)
			if err := root.Configure(testContext, AgentChange{MaxTurns: SetTo(1.0)}); err != nil {
				t.Fatal(err)
			}
			first := submit(t, root, inputDraft("go"))
			if err := started.wait(testContext); err != nil {
				t.Fatal(err)
			}
			steer := submit(t, root, inputDraft("also", durable.WhenBusyFollowUp))
			gate.resolve()
			if status := must(first.Wait(testContext)).Status; status != durable.SubmissionUnanswered {
				t.Fatalf("%s: first = %s, want unanswered", policy, status)
			}
			if err := harness.WaitForIdle(testContext); err != nil {
				t.Fatal(err)
			}
			queued := status(t, steer)
			switch policy {
			case durable.AfterFailedRunKeep:
				if queued.Status != durable.SubmissionQueued {
					t.Fatalf("keep: queued = %s", queued.Status)
				}
			case durable.AfterFailedRunContinue:
				if queued.Status != durable.SubmissionDone {
					t.Fatalf("continue: queued = %s", queued.Status)
				}
			case durable.AfterFailedRunWithdraw:
				expectSettled(t, queued, durable.SubmissionUnanswered, "run_failed")
			}
			closeHarness(t, harness)
		}
	})

	// #11043
	t.Run("applies a changed limit at the end of the round; null removes it", func(t *testing.T) {
		// upstream: packages/durable/test/harness-turn-limit.test.ts:187
		setup := chatSetup(t)
		var root Conversation
		rounds := 0
		addTool(t, setup.Registry, turnTool("noop", func(context.Context, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			rounds++
			// Lowered during the second round: that round ends the run.
			if rounds == 2 {
				if err := root.Configure(testContext, AgentChange{MaxTurns: SetTo(1.0)}); err != nil {
					return durable.ToolExecutionResult{}, err
				}
			}
			return noopResult()
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{turnRound("c1"), turnRound("c2"), turnRound("c3"), fauxAnswer("done")})
		harness, opened := openChat(t, storage.NewMemoryStorage(), setup)
		root = opened
		if err := root.Configure(testContext, AgentChange{MaxTurns: SetTo(5.0)}); err != nil {
			t.Fatal(err)
		}
		settled := must(submit(t, root, inputDraft("go")).Wait(testContext))
		expectSettled(t, settled, durable.SubmissionUnanswered, "turn_limit")
		if detail := detailOf(t, settled); detail != "2 turns" {
			t.Fatalf("detail = %q, want 2 turns", detail)
		}
		if err := root.Configure(testContext, AgentChange{MaxTurns: Cleared[float64]()}); err != nil {
			t.Fatal(err)
		}
		if agent := mustAgent(t, root); agent.MaxTurns != nil {
			t.Fatalf("maxTurns = %d, want unset", *agent.MaxTurns)
		}
		setup.Faux.SetResponses([]ai.FauxResponseStep{turnRound("c4"), turnRound("c5"), fauxAnswer("done")})
		if status := must(submit(t, root, inputDraft("again")).Wait(testContext)).Status; status != durable.SubmissionDone {
			t.Fatalf("again = %s, want done", status)
		}
		closeHarness(t, harness)
	})

	// #11043
	t.Run("rejects a limit that is not an integer 1 or more", func(t *testing.T) {
		// upstream: packages/durable/test/harness-turn-limit.test.ts:218
		_, harness, root := turnLimited(t, 0)
		for _, value := range []float64{0, -1, 1.5, math.NaN(), math.Inf(1)} {
			err := root.Configure(testContext, AgentChange{MaxTurns: SetTo(value)})
			if err == nil {
				t.Fatalf("maxTurns %v accepted", value)
			}
		}
		expectErrorContains(t, root.Configure(testContext, AgentChange{MaxTurns: SetTo(1.5)}), "maxTurns must be an integer 1 or more, got 1.5")
		expectErrorContains(t, root.Configure(testContext, AgentChange{MaxTurns: SetTo(math.NaN())}), "maxTurns must be an integer 1 or more, got NaN")
		expectErrorContains(t, root.Configure(testContext, AgentChange{MaxTurns: SetTo(math.Inf(1))}), "maxTurns must be an integer 1 or more, got Infinity")
		if err := root.Configure(testContext, AgentChange{MaxTurns: SetTo(3.0)}); err != nil {
			t.Fatal(err)
		}
		if agent := mustAgent(t, root); agent.MaxTurns == nil || *agent.MaxTurns != 3 {
			t.Fatalf("maxTurns = %v, want 3", agent.MaxTurns)
		}
		closeHarness(t, harness)
	})

	// #11043
	t.Run("lets terminate settle done at the limit, and writes a handoff's reset before ending", func(t *testing.T) {
		// upstream: packages/durable/test/harness-turn-limit.test.ts:231
		setup, harness, root := turnLimited(t, 1, func(chat *chatState) {
			addHooks(t, chat.Registry, ToolTask.Erased(), &ToolHooks{AfterTool: func(_ context.Context, call ToolHookCall, result durable.ToolExecutionResult, _ HookApi) (*durable.ToolExecutionResult, error) {
				if call.ID == "t" {
					result.Control = &durable.ToolControl{Terminate: true}
				} else {
					result.Control = &durable.ToolControl{Handoff: new("fresh")}
				}
				return &result, nil
			}})
		})
		setup.Faux.SetResponses([]ai.FauxResponseStep{turnRound("t"), turnRound("h"), fauxAnswer("done")})
		if status := must(submit(t, root, inputDraft("one")).Wait(testContext)).Status; status != durable.SubmissionDone {
			t.Fatalf("one = %s, want done", status)
		}
		handedOff := must(submit(t, root, inputDraft("two")).Wait(testContext))
		expectSettled(t, handedOff, durable.SubmissionUnanswered, "turn_limit")
		var resets []string
		for _, entry := range allEntries(t, root) {
			if entry.Kind == durable.ResetEntry.Kind {
				text, _ := textOf(entry.Model[0])
				resets = append(resets, text)
			}
		}
		if !slices.Equal(resets, []string{"fresh"}) {
			t.Fatalf("resets = %v", resets)
		}
		closeHarness(t, harness)
	})

	// #11043
	t.Run("is resumed by a continue, with a fresh budget, on the round's results", func(t *testing.T) {
		// upstream: packages/durable/test/harness-turn-limit.test.ts:251
		setup, harness, root := turnLimited(t, 1)
		continued := ""
		setup.Faux.SetResponses([]ai.FauxResponseStep{
			turnRound("c1"),
			ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
				messages := roles(request.Messages())
				continued = messages[len(messages)-1]
				return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("finished")}}.AssistantMessage(), nil
			}),
		})
		must(submit(t, root, inputDraft("go")).Wait(testContext))
		resumed := submit(t, root, durable.SubmissionDraft{Type: durable.SubmissionTypeContinue})
		if run := liveOf(t, harness, root.Id()).Run; run == nil || run.Turns != 0 {
			t.Fatalf("run = %s, want turns 0", jsonText(t, run))
		}
		settled := must(resumed.Wait(testContext))
		if settled.Status != durable.SubmissionDone || settled.Type == durable.SubmissionTypeWrite {
			t.Fatalf("Unexpected %s", settled.Status)
		}
		var answer *durable.EntryRecord
		for _, entry := range allEntries(t, root) {
			if entry.Id == *settled.Answer {
				answer = &entry
			}
		}
		if text, _ := textOf(answer.Model[0]); text != "finished" {
			t.Fatalf("answer = %q", text)
		}
		if continued != "toolResult" {
			t.Fatalf("continued = %q, want toolResult", continued)
		}
		closeHarness(t, harness)
	})

	// #11043
	t.Run("is inherited by forks and children, which may set their own", func(t *testing.T) {
		// upstream: packages/durable/test/harness-turn-limit.test.ts:278
		setup := chatSetup(t)
		var child durable.ConversationId
		addTool(t, setup.Registry, turnTool("spawn", func(ctx context.Context, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			created, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
				record, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: api.TaskId()}})
				if err != nil {
					return nil, err
				}
				return record.Id, nil
			})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			child = created.(durable.ConversationId)
			return noopResult()
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{toolCallStep(ai.FauxToolCall("spawn", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"})), fauxAnswer("done")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		if err := root.Configure(testContext, AgentChange{MaxTurns: SetTo(4.0)}); err != nil {
			t.Fatal(err)
		}
		must(submit(t, root, inputDraft("go")).Wait(testContext))
		spawned := must(harness.Conversation(testContext, child))
		if agent := mustAgent(t, spawned); agent.MaxTurns == nil || *agent.MaxTurns != 4 {
			t.Fatalf("child maxTurns = %v, want 4", agent.MaxTurns)
		}
		entries := allEntries(t, root)
		fork := mustFork(t, root, entries[len(entries)-1].Id, ConversationCreateOptions{Ownership: ownerless})
		if agent := mustAgent(t, fork); agent.MaxTurns == nil || *agent.MaxTurns != 4 {
			t.Fatalf("fork maxTurns = %v, want 4", agent.MaxTurns)
		}
		own := must(harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: ownerless, Agent: &AgentChange{MaxTurns: SetTo(2.0)}}))
		if agent := mustAgent(t, own); agent.MaxTurns == nil || *agent.MaxTurns != 2 {
			t.Fatalf("own maxTurns = %v, want 2", agent.MaxTurns)
		}
		closeHarness(t, harness)
	})

	// #11043
	t.Run("keeps the stored count across a restart", func(t *testing.T) {
		// upstream: packages/durable/test/harness-turn-limit.test.ts:312
		path := filepath.Join(t.TempDir(), "session.sqlite")
		setup := chatSetup(t)
		setup.SetSettings(func(settings *HarnessSettings) {
			settings.Progress = &ProgressPolicyPatch{OutputIntervalMs: new(0.0), PersistWithinMs: new(0.0)}
		})
		started := deferred()
		runs := 0
		addTool(t, setup.Registry, turnTool("noop", func(ctx context.Context, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			runs++
			if runs == 2 {
				started.resolve()
				return durable.ToolExecutionResult{}, abortedBy(ctx)
			}
			return noopResult()
		}, durable.ReplaySafe))
		setup.Faux.SetResponses([]ai.FauxResponseStep{turnRound("c1"), turnRound("c2"), fauxAnswer("done")})
		first, firstRoot := openAt(t, path, setup)
		if err := firstRoot.Configure(testContext, AgentChange{MaxTurns: SetTo(2.0)}); err != nil {
			t.Fatal(err)
		}
		id := submit(t, firstRoot, inputDraft("go")).Id()
		if err := started.wait(testContext); err != nil {
			t.Fatal(err)
		}
		if run := liveOf(t, first, firstRoot.Id()).Run; run == nil || run.Turns != 2 {
			t.Fatalf("run = %s, want turns 2", jsonText(t, run))
		}
		closeHarness(t, first)

		// The replay-safe call runs again after reopen; the stored count still ends the run at this round.
		runs = 0
		second, secondRoot := openAt(t, path, setup)
		second.Resume()
		if run := liveOf(t, second, secondRoot.Id()).Run; run == nil || run.Turns != 2 {
			t.Fatalf("run after reopen = %s, want turns 2", jsonText(t, run))
		}
		settled := must(submissionOfHarness(t, second, id).Wait(testContext))
		expectSettled(t, settled, durable.SubmissionUnanswered, "turn_limit")
		if detail := detailOf(t, settled); detail != "2 turns" {
			t.Fatalf("detail = %q, want 2 turns", detail)
		}
		closeHarness(t, second)
	})
}
