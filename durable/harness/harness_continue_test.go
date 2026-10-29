// Ports packages/durable/test/harness-continue.test.ts: `submit({ type: "continue" })`, a run without a new user
// message (#11043). See harness_tasks_test.go for the Go mappings that apply to every case.

package harness

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// continueFailure is the model failure of harness-continue.test.ts `failure`.
func continueFailure() ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{StopReason: "error", ErrorMessage: "Invalid request"})
}

// continueSeen collects the roles of the requests an `answering` step saw.
type continueSeen struct {
	mu    sync.Mutex
	roles [][]string
}

func (seen *continueSeen) last() []string {
	seen.mu.Lock()
	defer seen.mu.Unlock()
	return seen.roles[len(seen.roles)-1]
}

// continueAnswering is a response step that records the roles of the context it was asked with, then answers text.
func continueAnswering(text string, seen *continueSeen) ai.FauxResponseStep {
	return ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
		seen.mu.Lock()
		seen.roles = append(seen.roles, roles(request.Messages()))
		seen.mu.Unlock()
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}}.AssistantMessage(), nil
	})
}

func continueDraft(requestId ...string) durable.SubmissionDraft {
	draft := durable.SubmissionDraft{Type: durable.SubmissionTypeContinue}
	if len(requestId) > 0 {
		draft.RequestId = &requestId[0]
	}
	return draft
}

func continueAnswerText(t *testing.T, root Conversation, settled durable.SettledSubmissionRecord) string {
	t.Helper()
	if settled.Status != durable.SubmissionDone || settled.Type == durable.SubmissionTypeWrite || settled.Answer == nil {
		t.Fatalf("Unexpected %s", settled.Status)
	}
	answer := commitValue(t, root, func(tx durable.Tx) (*durable.EntryRecord, error) { return tx.Entry(*settled.Answer) })
	text, _ := textOf(answer.Model[0])
	return text
}

func continueUserEntries(t *testing.T, root Conversation) int {
	t.Helper()
	count := 0
	for _, entry := range allEntries(t, root) {
		if entry.Kind == "pi.user" {
			count++
		}
	}
	return count
}

func continueRoles(t *testing.T, root Conversation) []string {
	t.Helper()
	view := must(root.Context(testContext, nil))
	return roles(view.Messages)
}

// continueKillable is storage whose calls never complete once killed, as a crashed host's.
type continueKillable struct {
	durable.Storage
	dead chan struct{}
	once sync.Once
}

func (store *continueKillable) kill() { store.once.Do(func() { close(store.dead) }) }

func (store *continueKillable) Commit(ctx context.Context, writes []durable.StorageWrite, adopt func(durable.Seq)) (durable.Seq, error) {
	select {
	case <-store.dead:
		// Parked like a crashed process; TestMain ends it after every test has run.
		_ = parkLikeACrashedProcess()
		return 0, context.Cause(crashedProcesses)
	default:
	}
	return store.Storage.Commit(ctx, writes, adopt)
}

func TestContinue(t *testing.T) {
	// #11043
	t.Run("answers a run that failed, without a new user message", func(t *testing.T) {
		setup := chatSetup(t)
		seen := &continueSeen{}
		setup.Faux.SetResponses([]ai.FauxResponseStep{continueFailure(), continueAnswering("recovered", seen)})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		first := submitInput(t, root, "Analyze this")
		if settled := must(first.Wait(testContext)); settled.Status != durable.SubmissionUnanswered || *settled.Reason != "model_error" {
			t.Fatalf("first = %s", jsonText(t, settled))
		}
		if live := liveOf(t, harness, root.Id()); live.RunFailure == nil || live.RunFailure.Reason != RunModelError {
			t.Fatalf("runFailure = %+v", live.RunFailure)
		}

		resumed := submit(t, root, continueDraft())
		if status := must(resumed.Status(testContext)).Status; status != durable.SubmissionPlaced {
			t.Fatalf("continue status = %s, want placed", status)
		}
		settled := must(resumed.Wait(testContext))
		if settled.Type != durable.SubmissionTypeContinue {
			t.Fatalf("type = %s", settled.Type)
		}
		if got := continueAnswerText(t, root, settled); got != "recovered" {
			t.Fatalf("answer = %q", got)
		}
		if last := seen.last(); last[len(last)-1] != "user" {
			t.Fatalf("request roles %v, want the last to be user", last)
		}
		// The failed input stays unanswered; no user entry was added.
		if status := must(first.Status(testContext)).Status; status != durable.SubmissionUnanswered {
			t.Fatalf("first = %s", status)
		}
		if got := continueUserEntries(t, root); got != 1 {
			t.Fatalf("user entries = %d, want 1", got)
		}
		if live := liveOf(t, harness, root.Id()); live.RunFailure != nil {
			t.Fatalf("runFailure = %+v, want none", live.RunFailure)
		}
		closeHarness(t, harness)
	})

	// #11043
	t.Run("answers the tool results of a round aborted midway", func(t *testing.T) {
		setup := chatSetup(t)
		started := deferred()
		addTool(t, setup.Registry, ownTool("wait", "Waits until aborted", emptyObjectSchema(), func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			started.resolve()
			return durable.ToolExecutionResult{}, abortedBy(ctx)
		}))
		seen := &continueSeen{}
		setup.Faux.SetResponses([]ai.FauxResponseStep{ownCall("wait", map[string]any{}, "c1"), continueAnswering("after the abort", seen)})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		first := submitInput(t, root, "go")
		if err := started.wait(testContext); err != nil {
			t.Fatal(err)
		}
		if err := root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		if settled := must(first.Wait(testContext)); settled.Status != durable.SubmissionUnanswered || *settled.Reason != "aborted" {
			t.Fatalf("first = %s", jsonText(t, settled))
		}
		settled := must(submit(t, root, continueDraft()).Wait(testContext))
		if got := continueAnswerText(t, root, settled); got != "after the abort" {
			t.Fatalf("answer = %q", got)
		}
		if last := seen.last(); last[len(last)-1] != "toolResult" {
			t.Fatalf("request roles %v, want the last to be toolResult", last)
		}
		closeHarness(t, harness)
	})

	// #11043
	t.Run("skips system messages when deciding there is something to answer", func(t *testing.T) {
		setup := chatSetup(t)
		seen := &continueSeen{}
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("first answer"), continueFailure(), continueAnswering("resumed", seen)})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		if err := root.Configure(testContext, AgentChange{Instructions: SetTo("Be thorough.")}); err != nil {
			t.Fatal(err)
		}
		must(submitInput(t, root, "one").Wait(testContext))
		// Changed instructions append a system delta after the next user message, before its request.
		if err := root.Configure(testContext, AgentChange{Instructions: SetTo("Be brief.")}); err != nil {
			t.Fatal(err)
		}
		second := submitInput(t, root, "two")
		if settled := must(second.Wait(testContext)); settled.Status != durable.SubmissionUnanswered || *settled.Reason != "model_error" {
			t.Fatalf("second = %s", jsonText(t, settled))
		}
		got := continueRoles(t, root)
		if got[len(got)-1] != "system" {
			t.Fatalf("roles %v, want the last to be system", got)
		}
		settled := must(submit(t, root, continueDraft()).Wait(testContext))
		if answer := continueAnswerText(t, root, settled); answer != "resumed" {
			t.Fatalf("answer = %q", answer)
		}
		closeHarness(t, harness)
	})

	// #11043
	t.Run("is rejected while busy, after request ID deduplication", func(t *testing.T) {
		setup := chatSetup(t)
		busy := unanswered()
		setup.Faux.SetResponses([]ai.FauxResponseStep{continueFailure(), busy.step})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		must(submitInput(t, root, "go").Wait(testContext))
		resumed := submit(t, root, continueDraft("resume-1"))
		<-busy.reached
		_, err := root.Submit(testContext, continueDraft())
		if _, ok := errors.AsType[*durable.ConversationBusy](err); !ok {
			t.Fatalf("busy continue = %v, want ConversationBusy", err)
		}
		// The same request ID returns the existing submission instead of rejecting.
		if again := submit(t, root, continueDraft("resume-1")); again.Id() != resumed.Id() {
			t.Fatalf("deduplicated id = %d, want %d", again.Id(), resumed.Id())
		}
		closeHarness(t, harness)
	})

	// #11043
	t.Run("ends without a request when there is nothing to answer", func(t *testing.T) {
		setup := chatSetup(t)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("answered")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		empty := must(submit(t, root, continueDraft()).Wait(testContext))
		if empty.Type != durable.SubmissionTypeContinue || empty.Status != durable.SubmissionUnanswered || *empty.Reason != "nothing_to_answer" {
			t.Fatalf("empty = %s", jsonText(t, empty))
		}
		must(submitInput(t, root, "hi").Wait(testContext))
		calls := setup.Faux.CallCount()
		// A context ending in an answer has nothing to continue.
		after := must(submit(t, root, continueDraft()).Wait(testContext))
		if after.Status != durable.SubmissionUnanswered || *after.Reason != "nothing_to_answer" {
			t.Fatalf("after = %s", jsonText(t, after))
		}
		if setup.Faux.CallCount() != calls {
			t.Fatalf("call count %d, want %d", setup.Faux.CallCount(), calls)
		}
		if live := liveOf(t, harness, root.Id()); live.RunFailure == nil || live.RunFailure.Reason != RunNothingToAnswer {
			t.Fatalf("runFailure = %+v", live.RunFailure)
		}
		closeHarness(t, harness)
	})

	// #11043
	t.Run("continues an answer with tool calls cut off at its output limit, which ends in their missing results", func(t *testing.T) {
		setup := chatSetup(t)
		seen := &continueSeen{}
		setup.Faux.SetResponses([]ai.FauxResponseStep{
			ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("calling"), ai.FauxToolCall("echo", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"})}, StopReason: "length"}),
			continueAnswering("resumed", seen),
		})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		// The run answered: a cut-off answer is an answer, and its calls never ran.
		if status := must(submitInput(t, root, "go").Wait(testContext)).Status; status != durable.SubmissionDone {
			t.Fatalf("first = %s", status)
		}
		settled := must(submit(t, root, continueDraft()).Wait(testContext))
		if answer := continueAnswerText(t, root, settled); answer != "resumed" {
			t.Fatalf("answer = %q", answer)
		}
		if last := seen.last(); !reflect.DeepEqual(last[len(last)-2:], []string{"assistant", "toolResult"}) {
			t.Fatalf("request roles %v, want to end in assistant, toolResult", last)
		}
		closeHarness(t, harness)
	})

	// #11043
	t.Run("does not continue a text answer cut off at its output limit", func(t *testing.T) {
		setup := chatSetup(t)
		setup.Faux.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("cut")}, StopReason: "length"})})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		must(submitInput(t, root, "write a lot").Wait(testContext))
		settled := must(submit(t, root, continueDraft()).Wait(testContext))
		if settled.Status != durable.SubmissionUnanswered || *settled.Reason != "nothing_to_answer" {
			t.Fatalf("settled = %s", jsonText(t, settled))
		}
		if setup.Faux.CallCount() != 1 {
			t.Fatalf("call count %d, want 1", setup.Faux.CallCount())
		}
		closeHarness(t, harness)
	})

	// #11043
	t.Run("answers a run interrupted by a host that died while the model answered", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		open := func(setup *chatState) (Harness, Conversation, *continueKillable) {
			store := &continueKillable{Storage: openSqlite(t, path), dead: make(chan struct{})}
			t.Cleanup(func() { _ = store.Close(testContext) })
			harness, root := openChat(t, store, setup)
			harness.Resume()
			return harness, root, store
		}
		first := chatSetup(t, ai.FauxConfig{TokensPerSecond: 20, TokenSize: &ai.FauxTokenSize{Min: new(1), Max: new(1)}})
		first.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer(strings.Repeat("z", 400))})
		first.SetSettings(func(settings *HarnessSettings) { settings.Progress = &ProgressPolicyPatch{PersistWithinMs: new(0.0)} })
		harness, root, store := open(first)
		id := submitInput(t, root, "hi").Id()
		waitFor(t, func() bool {
			live := liveOf(t, harness, root.Id())
			return live.Generation != nil && live.Generation.Message != nil
		})
		time.Sleep(50 * time.Millisecond)
		store.kill()

		setup := chatSetup(t)
		setup.SetSettings(func(settings *HarnessSettings) { settings.Retry = &RetryPolicyPatch{Enabled: new(false)} })
		seen := &continueSeen{}
		setup.Faux.SetResponses([]ai.FauxResponseStep{continueAnswering("after the crash", seen)})
		reopened, reopenedRoot, _ := open(setup)
		interrupted := must(submissionOfHarness(t, reopened, id).Wait(testContext))
		if interrupted.Status != durable.SubmissionUnanswered || *interrupted.Reason != "interrupted" {
			t.Fatalf("interrupted = %s", jsonText(t, interrupted))
		}
		settled := must(submit(t, reopenedRoot, continueDraft()).Wait(testContext))
		if answer := continueAnswerText(t, reopenedRoot, settled); answer != "after the crash" {
			t.Fatalf("answer = %q", answer)
		}
		// The aborted partial is not model context: the request ends in the user message.
		if last := seen.last(); last[len(last)-1] != "user" {
			t.Fatalf("request roles %v, want the last to be user", last)
		}
		closeHarness(t, reopened)
	})

	// #11043
	t.Run("places a reset a failed run kept, and finds nothing to answer in the context it starts", func(t *testing.T) {
		setup := chatSetup(t)
		gate := deferred()
		reached := deferred()
		setup.Faux.SetResponses([]ai.FauxResponseStep{
			ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
				reached.resolve()
				if err := gate.wait(options.Signal); err != nil {
					return ai.AssistantMessage{}, err
				}
				return ai.FauxResponse{StopReason: "error", ErrorMessage: "Invalid request"}.AssistantMessage(), nil
			}),
		})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		first := submitInput(t, root, "first")
		if err := reached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		if err := root.Reset(testContext, nil); err != nil {
			t.Fatal(err)
		}
		gate.resolve()
		if settled := must(first.Wait(testContext)); settled.Status != durable.SubmissionUnanswered || *settled.Reason != "model_error" {
			t.Fatalf("first = %s", jsonText(t, settled))
		}
		if items := must(durable.Snapshot(testContext, harness, InboxDoc, root.Id())).Items; len(items) != 1 {
			t.Fatalf("inbox items = %d, want 1", len(items))
		}

		settled := must(submit(t, root, continueDraft()).Wait(testContext))
		if settled.Status != durable.SubmissionUnanswered || *settled.Reason != "nothing_to_answer" {
			t.Fatalf("settled = %s", jsonText(t, settled))
		}
		if items := must(durable.Snapshot(testContext, harness, InboxDoc, root.Id())).Items; len(items) != 0 {
			t.Fatalf("inbox items = %d, want 0", len(items))
		}
		for _, role := range continueRoles(t, root) {
			if role != "system" {
				t.Fatalf("context holds a %s message, want only system messages", role)
			}
		}
		if setup.Faux.CallCount() != 1 {
			t.Fatalf("call count %d, want 1", setup.Faux.CallCount())
		}
		closeHarness(t, harness)
	})

	// #11043
	t.Run("starts the inputs queued behind a continue with nothing to answer under afterFailedRun continue", func(t *testing.T) {
		setup := chatSetup(t)
		setup.SetSettings(func(settings *HarnessSettings) { settings.AfterFailedRun = durable.AfterFailedRunContinue })
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("answered"), fauxAnswer("second answered")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		must(submitInput(t, root, "one").Wait(testContext))
		resumed := submit(t, root, continueDraft())
		queued := submit(t, root, inputDraft("two", durable.WhenBusyFollowUp))
		if status := must(queued.Status(testContext)).Status; status != durable.SubmissionQueued {
			t.Fatalf("queued = %s", status)
		}
		if settled := must(resumed.Wait(testContext)); settled.Status != durable.SubmissionUnanswered || *settled.Reason != "nothing_to_answer" {
			t.Fatalf("resumed = %s", jsonText(t, settled))
		}
		settled := must(queued.Wait(testContext))
		if answer := continueAnswerText(t, root, settled); answer != "second answered" {
			t.Fatalf("answer = %q", answer)
		}
		closeHarness(t, harness)
	})

	// #11043
	t.Run("is submitted by a tool through the handle of a conversation it owns, and rejected there while busy", func(t *testing.T) {
		setup := chatSetup(t)
		busy := deferred()
		release := deferred()
		var mu sync.Mutex
		var first, resumedStatus string
		var busyErr error
		addTool(t, setup.Registry, ownTool("delegate", "Delegates", emptyObjectSchema(), func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			child, err := ownCreateChild(ctx, api)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			handle, err := api.Conversation(ctx, child)
			if err != nil || handle == nil {
				return durable.ToolExecutionResult{}, errors.New("no handle")
			}
			work, err := handle.Submit(ctx, inputDraft("work"))
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			settled, err := work.Wait(ctx)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			resumed, err := handle.Submit(ctx, continueDraft())
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if err := busy.wait(ctx); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			_, again := handle.Submit(ctx, continueDraft())
			release.resolve()
			done, err := resumed.Wait(ctx)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			mu.Lock()
			first, resumedStatus, busyErr = string(settled.Status), string(done.Status), again
			mu.Unlock()
			return ownText(string(done.Status)), nil
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{
			ownCall("delegate", map[string]any{}, "c1"),
			continueFailure(),
			ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
				busy.resolve()
				if err := release.wait(options.Signal); err != nil {
					return ai.AssistantMessage{}, err
				}
				return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("child recovered")}}.AssistantMessage(), nil
			}),
			fauxAnswer("done"),
		})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		must(submitInput(t, root, "go").Wait(testContext))
		mu.Lock()
		defer mu.Unlock()
		if first != "unanswered" || resumedStatus != "done" {
			t.Fatalf("outcome first=%q resumed=%q, want unanswered and done", first, resumedStatus)
		}
		if _, ok := errors.AsType[*durable.ConversationBusy](busyErr); !ok {
			t.Fatalf("busy continue through the handle = %v, want ConversationBusy", busyErr)
		}
		closeHarness(t, harness)
	})

	// #11043
	t.Run("starts a root trace linked to its caller, as an input does", func(t *testing.T) {
		setup := chatSetup(t)
		recorder := telemetry.NewInMemoryTelemetryContext()
		setup.Faux.SetResponses([]ai.FauxResponseStep{continueFailure(), fauxAnswer("recovered")})
		harness, root := openTracked(t, storage.NewMemoryStorage(), setup, recorder)
		must(submitInput(t, root, "go").Wait(testContext))
		var id durable.SubmissionId
		_, err := telemetry.StartSpan(recorder, telemetry.SpanOptions{Name: "caller"}, func(caller telemetry.TelemetrySpan) (struct{}, error) {
			callContext := chordctx.WithTelemetry(testContext, caller)
			resumed := must(root.Submit(callContext, continueDraft()))
			must(resumed.Wait(callContext))
			id = resumed.Id()
			return struct{}{}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool { return allSettled(recorder) })
		callers := telemetrySpans(recorder, "caller")
		if len(callers) != 1 {
			t.Fatalf("%d caller spans", len(callers))
		}
		var submitSpan telemetry.RecordedTelemetrySpan
		for _, span := range telemetrySpans(recorder, "pi.durable.submit") {
			if span.Attributes.Value("submission.id") == numberOf(id) {
				submitSpan = span
			}
		}
		if submitSpan.Name == "" || submitSpan.ParentID != nil || submitSpan.Attributes.Value("result") != "placed" {
			t.Fatalf("submit span %+v", submitSpan)
		}
		if len(submitSpan.Links) != 1 || !reflect.DeepEqual(submitSpan.Links[0].Context, callers[0].Context) {
			t.Fatalf("submit links %+v, want the caller's context", submitSpan.Links)
		}
		if record := must(must(harness.Submission(testContext, id)).Status(testContext)); !reflect.DeepEqual(record.Trace, submitSpan.Context) {
			t.Fatalf("record trace %v, want %v", record.Trace, submitSpan.Context)
		}
		found := false
		for _, span := range telemetrySpans(recorder, "pi.durable.phase") {
			if span.Attributes.Value("phase") == "prepare" && reflect.DeepEqual(span.ParentContext, submitSpan.Context) {
				found = true
			}
		}
		if !found {
			t.Fatal("no prepare phase span is a child of the continue's submit span")
		}
		closeHarness(t, harness)
	})

	// #11043
	t.Run("places inputs a failed run left queued first, and they join its run", func(t *testing.T) {
		setup := chatSetup(t)
		gate := deferred()
		reached := deferred()
		setup.Faux.SetResponses([]ai.FauxResponseStep{
			ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
				reached.resolve()
				if err := gate.wait(options.Signal); err != nil {
					return ai.AssistantMessage{}, err
				}
				return ai.FauxResponse{StopReason: "error", ErrorMessage: "Invalid request"}.AssistantMessage(), nil
			}),
			fauxAnswer("both handled"),
		})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		submitInput(t, root, "first")
		if err := reached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		queued := submit(t, root, inputDraft("second", durable.WhenBusyFollowUp))
		gate.resolve()
		if err := harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		if status := must(queued.Status(testContext)).Status; status != durable.SubmissionQueued {
			t.Fatalf("queued = %s", status)
		}

		resumed := submit(t, root, continueDraft())
		live := liveOf(t, harness, root.Id())
		if live.Run == nil || !reflect.DeepEqual(live.Run.Inputs, []durable.SubmissionId{queued.Id(), resumed.Id()}) {
			t.Fatalf("run = %+v, want inputs [%d %d]", live.Run, queued.Id(), resumed.Id())
		}
		byQueued, byContinue := must(queued.Wait(testContext)), must(resumed.Wait(testContext))
		if answer := continueAnswerText(t, root, byContinue); answer != "both handled" {
			t.Fatalf("answer = %q", answer)
		}
		if byQueued.Status != durable.SubmissionDone || byQueued.Answer == nil || *byQueued.Answer != *byContinue.Answer {
			t.Fatalf("byQueued = %s, want done with the continue's answer", jsonText(t, byQueued))
		}
		closeHarness(t, harness)
	})
}
