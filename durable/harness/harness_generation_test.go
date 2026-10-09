// Ports packages/durable/test/harness-generation.test.ts.

package harness

// pi: packages/durable/src/harness/generation.ts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// error503 is ERROR_503: a retryable provider error.
func error503() ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{StopReason: "error", ErrorMessage: "503 Service Unavailable"})
}

func live(t *testing.T, harness Harness, conversation Conversation) *LiveState {
	t.Helper()
	return liveOf(t, harness, conversation.Id())
}

// runTask waits until the conversation has a run and returns its task.
func runTask(t *testing.T, harness Harness, conversation Conversation) durable.TaskId {
	t.Helper()
	var taskId durable.TaskId
	waitFor(t, func() bool {
		state := live(t, harness, conversation)
		if state.Run == nil {
			return false
		}
		taskId = state.Run.TaskId
		return true
	})
	return taskId
}

// withProvider replaces the faux provider on the setup's models with change applied to a copy of it; the Go form of
// the upstream Models proxy.
func withProvider(setup *chatState, change func(provider *ai.ModelsProvider)) {
	provider := *setup.Faux.Provider()
	change(&provider)
	setup.Models.SetProvider(&provider)
}

// streamOf returns a stream that pushes a start event with partial, waits delay, and ends with final.
func streamOf(partial *ai.AssistantMessage, delay time.Duration, final *ai.AssistantMessage) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream()
	go func() {
		_ = stream.Push(ai.StartEvent{Partial: partial})
		time.Sleep(delay)
		stream.End(final)
	}()
	return stream
}

func fauxMessage(text string, stopReason ai.StopReason) *ai.AssistantMessage {
	message := &ai.AssistantMessage{API: "faux", Provider: "faux", Model: "faux-1", StopReason: stopReason, Timestamp: time.Now().UnixMilli()}
	if text != "" {
		message.Content = []ai.AssistantContentBlock{ai.TextContent{Text: text}}
	}
	return message
}

// livePublications collects every committed pi.live value.
type livePublications struct {
	mu     sync.Mutex
	values []LiveState
}

func observeLive(t *testing.T, harness Harness) *livePublications {
	t.Helper()
	collected := &livePublications{}
	harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
		for _, change := range documentChanges(publication) {
			if change.Record.Kind != "pi.live" || change.Value == nil {
				continue
			}
			value, err := durable.FromJsonValue[LiveState](change.Value)
			if err != nil {
				t.Error(err)
				continue
			}
			collected.mu.Lock()
			collected.values = append(collected.values, value)
			collected.mu.Unlock()
		}
	})
	return collected
}

func (collected *livePublications) some(check func(value LiveState) bool) bool {
	collected.mu.Lock()
	defer collected.mu.Unlock()
	return slices.ContainsFunc(collected.values, check)
}

func (collected *livePublications) all() []LiveState {
	collected.mu.Lock()
	defer collected.mu.Unlock()
	return slices.Clone(collected.values)
}

// messageText decodes a stored message and returns its text.
func messageText(t *testing.T, message *ai.AssistantMessage) (string, bool) {
	t.Helper()
	if message == nil {
		return "", false
	}
	return textOf(*message)
}

// messageTextJSON is messageText of a partial message read from an erased watch, which delivers the document as JSON.
func messageTextJSON(t *testing.T, message durable.JsonObject) (string, bool) {
	t.Helper()
	if message == nil {
		return "", false
	}
	decoded, err := durable.DecodeMessage(must(json.Marshal(message)))
	if err != nil {
		t.Fatal(err)
	}
	return textOf(decoded)
}

func entryKinds(entries []durable.EntryRecord) []string {
	kinds := []string{}
	for _, entry := range entries {
		kinds = append(kinds, entry.Kind)
	}
	return kinds
}

func expectKinds(t *testing.T, entries []durable.EntryRecord, want ...string) {
	t.Helper()
	if got := entryKinds(entries); !slices.Equal(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
}

func submitInput(t *testing.T, conversation Conversation, text string) durable.Submission {
	t.Helper()
	return must(conversation.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(text)}))
}

func conversationTasks(t *testing.T, harness Harness, conversation Conversation) []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue] {
	t.Helper()
	id := conversation.Id()
	page := must(durable.Commit(testContext, harness, func(tx durable.Tx) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
		return tx.ScanTasks(durable.TaskQuery{ConversationId: &id}, 10, nil)
	}))
	return page.Items
}

func expectSettled(t *testing.T, settled durable.SubmissionRecord, status durable.SubmissionStatus, reason string) {
	t.Helper()
	if settled.Status != status || (reason != "" && (settled.Reason == nil || *settled.Reason != reason)) {
		t.Fatalf("settled = %s", jsonText(t, settled))
	}
}

func TestGeneration(t *testing.T) {
	t.Run("answers an input and settles its submission", func(t *testing.T) {
		// upstream: packages/durable/test/harness-generation.test.ts:89
		setup := chatSetup(t)
		addSection(t, setup.Registry, "preamble", text("You are helpful."), SectionOptions{Tag: new(false)})
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("Hello there")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		harness.Resume()
		submission := submitInput(t, root, "hi")
		settled := must(submission.Wait(testContext))
		if settled.Status != durable.SubmissionDone || settled.Type != durable.SubmissionTypeInput {
			t.Fatalf("Unexpected %s", settled.Status)
		}

		answer := commitValue(t, root, func(tx durable.Tx) (*durable.TypedEntry[durable.Never], error) {
			return durable.TxEntry(tx, durable.AssistantEntry, *settled.Answer)
		})
		if got, _ := textOf(answer.Model[0]); got != "Hello there" {
			t.Fatalf("answer = %q", got)
		}
		entries := allEntries(t, root)
		expectKinds(t, entries, "pi.user", "pi.system", "pi.assistant")
		if entries[0].Id != *settled.Entry {
			t.Fatalf("first entry = %d, want %d", entries[0].Id, *settled.Entry)
		}
		system := entries[1].Model[0].(ai.SystemMessage)
		expectEqualJSON(t, system, jsonText(t, map[string]any{"role": "system", "content": "", "sections": map[string]any{"preamble": "You are helpful."}, "timestamp": system.Timestamp}))
		expectEqualJSON(t, live(t, harness, root), `{}`)
		task := conversationTasks(t, harness, root)[0]
		if task.Kind != "pi.generation" {
			t.Fatalf("task kind = %s", task.Kind)
		}
		// Entries written by the generation are attributed to it; the admitted user entry is not task work.
		if entries[0].ByTaskId != nil || entries[1].ByTaskId == nil || *entries[1].ByTaskId != task.Id || entries[2].ByTaskId == nil || *entries[2].ByTaskId != task.Id {
			t.Fatalf("byTaskId = %v, %v, %v", entries[0].ByTaskId, entries[1].ByTaskId, entries[2].ByTaskId)
		}
		expectEqualJSON(t, task.State, jsonText(t, map[string]any{"status": "terminal", "outcome": map[string]any{"status": "completed", "result": map[string]any{"entryId": *settled.Answer}}}))
		closeHarness(t, harness)
	})

	t.Run("stores partials as deltas and a complete base once nothing is in flight", func(t *testing.T) {
		// upstream: packages/durable/test/harness-generation.test.ts:119
		setup := chatSetup(t, ai.FauxConfig{TokensPerSecond: 200, TokenSize: &ai.FauxTokenSize{Min: new(1), Max: new(1)}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer(strings.Repeat("w", 200))})
		store := newControlledStorage()
		harness, root := openChat(t, store, setup)
		rootId := root.Id()
		record := must(store.FindDocument(testContext, durable.DocumentAddress{Kind: "pi.live", Scope: durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: rootId}}, durable.CurrentPoint))
		harness.Resume()
		must(submitInput(t, root, "hi").Wait(testContext))
		contents := []string{}
		for index := range store.commitCount() {
			for _, write := range store.commitAt(index) {
				if change, ok := write.(durable.DocumentChangeWrite); ok && change.Id == record.Id {
					contents = append(contents, string(change.Content.Kind))
				}
			}
		}
		// Streaming writes deltas; the commit that settles the answer clears generation and writes a base.
		if !slices.Contains(contents, "delta") || contents[len(contents)-1] != "base" {
			t.Fatalf("contents = %v", contents)
		}
		closeHarness(t, harness)
	})

	t.Run("still ends a run whose input something else already settled", func(t *testing.T) {
		// upstream: packages/durable/test/harness-generation.test.ts:142
		setup := chatSetup(t)
		busy := unanswered()
		setup.Faux.SetResponses([]ai.FauxResponseStep{busy.step})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		submission := submitInput(t, root, "hi")
		<-busy.reached
		commitValue(t, root, func(tx durable.Tx) (struct{}, error) {
			return struct{}{}, tx.SettleSubmission(submission.Id(), durable.SubmissionSettlement{Status: durable.SubmissionUnanswered, Reason: "withdrawn"})
		})
		taskId := runTask(t, harness, root)
		must(harness.AbortTask(testContext, taskId))
		expectEqualJSON(t, must(harness.WaitForTask(testContext, taskId)).State.Outcome, `{"status":"aborted"}`)
		// The earlier settlement stays; the run's own settlement leaves it unchanged.
		expectSettled(t, must(submission.Status(testContext)), durable.SubmissionUnanswered, "withdrawn")
		expectEqualJSON(t, live(t, harness, root), `{}`)
		closeHarness(t, harness)
	})

	t.Run("fails with no_model when no model is configured or the model is unknown", func(t *testing.T) {
		// upstream: packages/durable/test/harness-generation.test.ts:162
		setup := chatSetup(t)
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		plain := must(harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: ownerless}))
		harness.Resume()
		expectSettled(t, must(submitInput(t, plain, "hi").Wait(testContext)), durable.SubmissionUnanswered, "no_model")

		if err := root.Configure(testContext, AgentChange{Model: SetTo(durable.ModelRef{Provider: "faux", ModelId: "missing"})}); err != nil {
			t.Fatal(err)
		}
		unknown := must(submitInput(t, root, "hi").Wait(testContext))
		expectSettled(t, unknown, durable.SubmissionUnanswered, "no_model")
		if unknown.Entry == nil {
			t.Fatal("unknown has no entry")
		}
		expectKinds(t, allEntries(t, root), "pi.user")
		expectEqualJSON(t, conversationTasks(t, harness, root)[0].State, `{"status":"terminal","outcome":{"status":"failed","error":{"message":"Model faux/missing is not available","detail":{"reason":"no_model"}}}}`)
		expectEqualJSON(t, live(t, harness, root), `{}`)
		expectEqualJSON(t, live(t, harness, plain), `{}`)
		closeHarness(t, harness)
	})

	t.Run("retries a retryable error after a durable backoff and then answers", func(t *testing.T) {
		// upstream: packages/durable/test/harness-generation.test.ts:187
		setup := chatSetup(t)
		addSection(t, setup.Registry, "preamble", text("p"), SectionOptions{Tag: new(false)})
		setup.Faux.SetResponses([]ai.FauxResponseStep{error503(), fauxAnswer("recovered")})
		setup.SetSettings(func(settings *HarnessSettings) {
			settings.Retry = &RetryPolicyPatch{Enabled: new(true), MaxRetries: new(3), BaseDelayMs: new(1)}
		})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		values := observeLive(t, harness)
		harness.Resume()
		settled := must(submitInput(t, root, "hi").Wait(testContext))
		expectSettled(t, settled, durable.SubmissionDone, "")
		entries := allEntries(t, root)
		expectKinds(t, entries, "pi.user", "pi.system", "pi.assistant", "pi.assistant")
		if stop := entries[2].Model[0].(ai.AssistantMessage).StopReason; stop != ai.StopReasonError {
			t.Fatalf("stopReason = %s, want error", stop)
		}
		if !values.some(func(value LiveState) bool {
			return value.Generation != nil && value.Generation.Retry != nil && value.Generation.Retry.Error == "503 Service Unavailable"
		}) {
			t.Fatal("no live retry status was published")
		}
		if !values.some(func(value LiveState) bool { return value.Generation != nil && value.Generation.Attempt == 2 }) {
			t.Fatal("no second attempt was published")
		}
		expectEqualJSON(t, live(t, harness, root), `{}`)
		closeHarness(t, harness)
	})

	t.Run("fails with model_error once retries are exhausted", func(t *testing.T) {
		// upstream: packages/durable/test/harness-generation.test.ts:206
		setup := chatSetup(t)
		setup.Faux.SetResponses([]ai.FauxResponseStep{error503(), error503(), fauxAnswer("never")})
		setup.SetSettings(func(settings *HarnessSettings) {
			settings.Retry = &RetryPolicyPatch{Enabled: new(true), MaxRetries: new(1), BaseDelayMs: new(1)}
		})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		harness.Resume()
		settled := must(submitInput(t, root, "hi").Wait(testContext))
		expectSettled(t, settled, durable.SubmissionUnanswered, "model_error")
		expectEqualJSON(t, settled.Detail, `"503 Service Unavailable"`)
		expectKinds(t, allEntries(t, root), "pi.user", "pi.assistant", "pi.assistant")
		if pending := setup.Faux.PendingResponseCount(); pending != 1 {
			t.Fatalf("pending responses = %d, want 1", pending)
		}
		closeHarness(t, harness)
	})

	t.Run("fails a retryable error without retrying when the retry policy is disabled", func(t *testing.T) {
		// upstream: packages/durable/test/harness-generation.test.ts:219
		setup := chatSetup(t)
		setup.Faux.SetResponses([]ai.FauxResponseStep{error503(), fauxAnswer("never")})
		setup.SetSettings(func(settings *HarnessSettings) { settings.Retry = &RetryPolicyPatch{Enabled: new(false)} })
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		harness.Resume()
		expectSettled(t, must(submitInput(t, root, "hi").Wait(testContext)), durable.SubmissionUnanswered, "model_error")
		if calls := setup.Faux.CallCount(); calls != 1 {
			t.Fatalf("calls = %d, want 1", calls)
		}
		closeHarness(t, harness)
	})

	t.Run("reports section wrapper failures while preparing", func(t *testing.T) {
		// upstream: packages/durable/test/harness-generation.test.ts:231
		setup := chatSetup(t)
		addSection(t, setup.Registry, "cwd", text("/repo"))
		installOne(t, setup.Registry, new(durable.Extension{
			Name:  "broken",
			Wraps: []durable.Wrap{WrapSection("cwd", func(*durable.PromptSection) *durable.PromptSection { panic(errors.New("wrapper failed")) })},
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("ok")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		harness.Resume()
		expectSettled(t, must(submitInput(t, root, "hi").Wait(testContext)), durable.SubmissionDone, "")
		if !slices.ContainsFunc(setup.Reports.all(), func(err error) bool { return err.Error() == "wrapper failed" }) {
			t.Fatalf("reports = %v, want wrapper failed", setup.Reports.all())
		}
		// The failed section is absent, so nothing was rendered.
		expectKinds(t, allEntries(t, root), "pi.user", "pi.assistant")
		closeHarness(t, harness)
	})

	t.Run("fails a non-retryable error without retrying", func(t *testing.T) {
		// upstream: packages/durable/test/harness-generation.test.ts:254
		setup := chatSetup(t)
		setup.Faux.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{StopReason: "error", ErrorMessage: "Invalid request"}), fauxAnswer("never")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		harness.Resume()
		settled := must(submitInput(t, root, "hi").Wait(testContext))
		expectSettled(t, settled, durable.SubmissionUnanswered, "model_error")
		expectEqualJSON(t, settled.Detail, `"Invalid request"`)
		outcome := conversationTasks(t, harness, root)[0].State.Outcome
		expectEqualJSON(t, outcome.Error, `{"message":"Invalid request","detail":{"reason":"model_error"}}`)
		if outcome.Status != durable.OutcomeFailed {
			t.Fatalf("outcome = %s, want failed", outcome.Status)
		}
		closeHarness(t, harness)
	})

	t.Run("polls a deferred response until it is ready", func(t *testing.T) {
		// upstream: packages/durable/test/harness-generation.test.ts:271
		pollAfter := int64(1)
		setup := chatSetup(t, ai.FauxConfig{Deferred: &ai.FauxDeferredConfig{PendingFetches: 1, PollAfterMS: &pollAfter}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("deferred answer")})
		setup.SetSettings(func(settings *HarnessSettings) {
			settings.Stream = &durable.ConversationStreamOptions{Deferred: &ai.DeferredOption{Enabled: true}}
		})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		values := observeLive(t, harness)
		harness.Resume()
		settled := must(submitInput(t, root, "hi").Wait(testContext))
		if settled.Status != durable.SubmissionDone || settled.Type != durable.SubmissionTypeInput {
			t.Fatalf("Unexpected %s", settled.Status)
		}
		if fetches := setup.Faux.(interface{ DeferredFetchCount() int }).DeferredFetchCount(); fetches != 2 {
			t.Fatalf("deferred fetches = %d, want 2", fetches)
		}
		pollTimes := []float64{}
		for _, value := range values.all() {
			if value.Generation != nil && value.Generation.Deferred != nil {
				pollTimes = append(pollTimes, value.Generation.Deferred.PollAt)
			}
		}
		if len(pollTimes) != 2 || pollTimes[1] <= pollTimes[0] {
			t.Fatalf("poll times = %v", pollTimes)
		}
		answer := commitValue(t, root, func(tx durable.Tx) (*durable.TypedEntry[durable.Never], error) {
			return durable.TxEntry(tx, durable.AssistantEntry, *settled.Answer)
		})
		if got, _ := textOf(answer.Model[0]); got != "deferred answer" {
			t.Fatalf("answer = %q", got)
		}
		closeHarness(t, harness)
	})

	t.Run("converts the committed partial when aborted during streaming", func(t *testing.T) {
		// upstream: packages/durable/test/harness-generation.test.ts:291
		setup := chatSetup(t, ai.FauxConfig{TokensPerSecond: 20, TokenSize: &ai.FauxTokenSize{Min: new(1), Max: new(1)}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer(strings.Repeat("x", 400))})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		harness.Resume()
		submission := submitInput(t, root, "hi")
		taskId := runTask(t, harness, root)
		waitFor(t, func() bool {
			state := live(t, harness, root)
			if state.Generation == nil {
				return false
			}
			_, ok := messageText(t, state.Generation.Message)
			return ok
		})
		partial, _ := messageText(t, live(t, harness, root).Generation.Message)
		if result := must(harness.AbortTask(testContext, taskId)); result != "marked" {
			t.Fatalf("abort = %s, want marked", result)
		}
		expectSettled(t, must(submission.Wait(testContext)), durable.SubmissionUnanswered, "aborted")
		entries := allEntries(t, root)
		expectKinds(t, entries, "pi.user", "pi.assistant")
		converted := entries[1].Model[0].(ai.AssistantMessage)
		if converted.StopReason != ai.StopReasonAborted {
			t.Fatalf("stopReason = %s, want aborted", converted.StopReason)
		}
		if got, _ := textOf(converted); !strings.HasPrefix(got, partial) {
			t.Fatalf("converted %q does not start with the partial %q", got, partial)
		}
		expectEqualJSON(t, live(t, harness, root), `{}`)
		expectEqualJSON(t, must(harness.WaitForTask(testContext, taskId)).State.Outcome, `{"status":"aborted"}`)
		closeHarness(t, harness)
	})

	t.Run("cancels a deferred response when aborted during polling", func(t *testing.T) {
		// upstream: packages/durable/test/harness-generation.test.ts:312
		pollAfter := int64(60_000)
		setup := chatSetup(t, ai.FauxConfig{Deferred: &ai.FauxDeferredConfig{PendingFetches: 100, PollAfterMS: &pollAfter}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("never")})
		setup.SetSettings(func(settings *HarnessSettings) {
			settings.Stream = &durable.ConversationStreamOptions{Deferred: &ai.DeferredOption{Enabled: true}}
		})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		harness.Resume()
		submission := submitInput(t, root, "hi")
		taskId := runTask(t, harness, root)
		waitFor(t, func() bool {
			state := live(t, harness, root)
			return state.Generation != nil && state.Generation.Deferred != nil
		})
		must(harness.AbortTask(testContext, taskId))
		expectSettled(t, must(submission.Wait(testContext)), durable.SubmissionUnanswered, "aborted")
		if cancelled := setup.Faux.(interface{ CancelledDeferred() []ai.DeferredHandle }).CancelledDeferred(); len(cancelled) != 1 {
			t.Fatalf("cancelled deferred = %v, want one", cancelled)
		}
		expectEqualJSON(t, live(t, harness, root), `{}`)
		closeHarness(t, harness)
	})

	t.Run("reports a failed deferred cancellation and still ends the run aborted", func(t *testing.T) {
		// upstream: packages/durable/test/harness-generation.test.ts:328
		pollAfter := int64(60_000)
		setup := chatSetup(t, ai.FauxConfig{Deferred: &ai.FauxDeferredConfig{PendingFetches: 100, PollAfterMS: &pollAfter}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("never")})
		withProvider(setup, func(provider *ai.ModelsProvider) {
			provider.CancelDeferred = func(context.Context, *ai.Model, ai.DeferredHandle, ai.DeferredCancelOptions) error {
				return errors.New("cancel failed")
			}
		})
		setup.SetSettings(func(settings *HarnessSettings) {
			settings.Stream = &durable.ConversationStreamOptions{Deferred: &ai.DeferredOption{Enabled: true}}
		})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		harness.Resume()
		submission := submitInput(t, root, "hi")
		taskId := runTask(t, harness, root)
		waitFor(t, func() bool {
			state := live(t, harness, root)
			return state.Generation != nil && state.Generation.Deferred != nil
		})
		must(harness.AbortTask(testContext, taskId))
		expectSettled(t, must(submission.Wait(testContext)), durable.SubmissionUnanswered, "aborted")
		if !slices.ContainsFunc(setup.Reports.all(), func(err error) bool { return err.Error() == "cancel failed" }) {
			t.Fatalf("reports = %v, want cancel failed", setup.Reports.all())
		}
		expectEqualJSON(t, live(t, harness, root), `{}`)
		closeHarness(t, harness)
	})

	t.Run("forwards stream options and the thinking level", func(t *testing.T) {
		// upstream: packages/durable/test/harness-generation.test.ts:352
		setup := chatSetup(t)
		var mu sync.Mutex
		seen := []ai.StreamOptions{}
		record := func(answer string) ai.FauxResponseStep {
			return ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
				mu.Lock()
				seen = append(seen, options)
				mu.Unlock()
				return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(answer)}}.AssistantMessage(), nil
			})
		}
		setup.Faux.SetResponses([]ai.FauxResponseStep{record("a"), record("b")})
		setup.SetSettings(func(settings *HarnessSettings) {
			settings.Stream = &durable.ConversationStreamOptions{TimeoutMs: new(1234), Headers: map[string]string{"x-test": "1"}}
		})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		if err := root.Configure(testContext, AgentChange{ThinkingLevel: SetTo(ai.ModelThinkingLevel("high"))}); err != nil {
			t.Fatal(err)
		}
		harness.Resume()
		must(submitInput(t, root, "one").Wait(testContext))
		// Both are read at the next preparation: the thinking level from pi.agent, the stream options live from settings.
		if err := root.Configure(testContext, AgentChange{ThinkingLevel: Cleared[ai.ModelThinkingLevel]()}); err != nil {
			t.Fatal(err)
		}
		setup.SetSettings(func(settings *HarnessSettings) {
			settings.Stream = &durable.ConversationStreamOptions{TimeoutMs: new(99)}
		})
		must(submitInput(t, root, "two").Wait(testContext))
		mu.Lock()
		defer mu.Unlock()
		first, second := seen[0], seen[1]
		sessionId := providerSessionId(t, harness, root.Id())
		if first.TimeoutMs == nil || *first.TimeoutMs != 1234 || first.Headers["x-test"] == nil || *first.Headers["x-test"] != "1" || first.Thinking != "high" || first.SessionID != sessionId {
			t.Fatalf("first options = %+v, want sessionId %q", first, sessionId)
		}
		if first.Signal == nil {
			t.Fatal("first options carry no signal")
		}
		if second.Thinking != "" {
			t.Fatalf("second reasoning = %q, want undefined", second.Thinking)
		}
		if second.TimeoutMs == nil || *second.TimeoutMs != 99 || second.SessionID != sessionId {
			t.Fatalf("second timeout = %v, sessionId = %q, want 99 and %q", second.TimeoutMs, second.SessionID, sessionId)
		}
		if second.Headers != nil {
			t.Fatalf("second headers = %v, want undefined", second.Headers)
		}
		closeHarness(t, harness)
	})

	// Regression coverage for #10424 (harness-generation.test.ts:388, Pi 1.0.2).
	t.Run("keeps provider session IDs request-local across concurrent conversations", func(t *testing.T) {
		// upstream: packages/durable/test/harness-generation.test.ts:389
		setup := chatSetup(t)
		var mu sync.Mutex
		seen := map[string][]string{}
		capture := ai.FauxFactoryStep(func(transcript ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
			text := ""
			messages := transcript.Messages()
			for _, message := range slices.Backward(messages) {
				if message, ok := message.(ai.UserMessage); ok {
					if content, ok := message.Content.(ai.UserText); ok {
						text = string(content)
					}
					break
				}
			}
			mu.Lock()
			seen[text] = append(seen[text], options.SessionID)
			mu.Unlock()
			return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("answer:" + text)}}.AssistantMessage(), nil
		})
		setup.Faux.SetResponses([]ai.FauxResponseStep{capture, capture, capture, capture})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		child, err := harness.CreateConversation(testContext, ConversationCreateOptions{
			Ownership: ownerless,
			Agent:     &AgentChange{Model: SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})},
		})
		if err != nil {
			t.Fatal(err)
		}
		harness.Resume()
		for _, round := range []string{"first", "second"} {
			var group sync.WaitGroup
			for index, conversation := range []Conversation{root, child} {
				group.Go(func() {
					must(submitInput(t, conversation, fmt.Sprintf("%s-%d", round, index)).Wait(testContext))
				})
			}
			group.Wait()
		}
		rootId := providerSessionId(t, harness, root.Id())
		childId := providerSessionId(t, harness, child.Id())
		if rootId == childId {
			t.Fatalf("root and child share provider session ID %q", rootId)
		}
		mu.Lock()
		defer mu.Unlock()
		for text, want := range map[string]string{"first-0": rootId, "second-0": rootId, "first-1": childId, "second-1": childId} {
			if !slices.Equal(seen[text], []string{want}) {
				t.Errorf("%s sent session IDs %q, want [%q]", text, seen[text], want)
			}
		}
		closeHarness(t, harness)
	})

	// Regression coverage for #10424 (harness-generation.test.ts:432, Pi 1.0.2).
	t.Run("creates and persists provider state before a legacy conversation's request", func(t *testing.T) {
		// upstream: packages/durable/test/harness-generation.test.ts:432
		setup := chatSetup(t)
		var mu sync.Mutex
		sent := ""
		setup.Faux.SetResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
			mu.Lock()
			sent = options.SessionID
			mu.Unlock()
			return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("ok")}}.AssistantMessage(), nil
		})})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		retireProviderDoc(t, root)
		if stored := providerSessionId(t, harness, root.Id()); stored != "" {
			t.Fatalf("pi.provider survived retirement: %q", stored)
		}
		harness.Resume()
		must(submitInput(t, root, "legacy").Wait(testContext))
		stored := expectProviderSessionId(t, harness, root.Id())
		mu.Lock()
		defer mu.Unlock()
		if sent != stored {
			t.Fatalf("sent session ID %q, stored %q", sent, stored)
		}
		closeHarness(t, harness)
	})

	t.Run("reads settings through getters at every decision", func(t *testing.T) {
		// upstream: packages/durable/test/harness-generation.test.ts:452
		setup := chatSetup(t)
		var mu sync.Mutex
		timeoutMs := 111
		seen := []int{}
		observe := func(options ai.StreamOptions) {
			mu.Lock()
			defer mu.Unlock()
			if options.TimeoutMs != nil {
				seen = append(seen, *options.TimeoutMs)
			}
		}
		setup.Faux.SetResponses([]ai.FauxResponseStep{
			ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
				observe(options)
				// The user changes the setting while the first attempt runs.
				mu.Lock()
				timeoutMs = 222
				mu.Unlock()
				return ai.FauxResponse{StopReason: "error", ErrorMessage: "503 Service Unavailable"}.AssistantMessage(), nil
			}),
			ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
				observe(options)
				return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("ok")}}.AssistantMessage(), nil
			}),
		})
		settings := func() *HarnessSettings {
			mu.Lock()
			defer mu.Unlock()
			return &HarnessSettings{Stream: &durable.ConversationStreamOptions{TimeoutMs: new(timeoutMs)}, Retry: &RetryPolicyPatch{BaseDelayMs: new(1)}}
		}
		harness := must(OpenHarness(testContext, storage.NewMemoryStorage(), HarnessOptions{Models: setup.Models, Registry: setup.Registry, Settings: settings}))
		root := must(harness.Root(testContext, &RootOptions{Agent: &AgentChange{Model: SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}}))
		harness.Resume()
		expectSettled(t, must(submitInput(t, root, "hi").Wait(testContext)), durable.SubmissionDone, "")
		// The retry prepares again, so it resolves the settings again and sends the new timeout.
		mu.Lock()
		if !slices.Equal(seen, []int{111, 222}) {
			t.Fatalf("seen = %v, want [111 222]", seen)
		}
		mu.Unlock()
		closeHarness(t, harness)
	})

	t.Run("resolves settings over the built-in defaults", func(t *testing.T) {
		// upstream: packages/durable/test/harness-generation.test.ts:487
		maxAgentDelayMs := 60000
		want := durable.Settings{
			Stream:        durable.ConversationStreamOptions{},
			Retry:         durable.ConversationRetryPolicy{Enabled: true, MaxRetries: 3, BaseDelayMs: 2000, MaxAgentDelayMs: &maxAgentDelayMs},
			Compaction:    durable.CompactionPolicy{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000, BackgroundTokens: 32768},
			Progress:      durable.ProgressPolicy{PartialIntervalMs: 100, OutputIntervalMs: 100},
			ToolExecution: durable.ToolExecutionParallel,
			SteeringMode:  durable.QueueOneAtATime,
			FollowUpMode:  durable.QueueOneAtATime,

			ContextRetentionMs: 600_000,
		}
		// agent.ts:19 DEFAULT_RETRY_POLICY is the retry policy of unset host settings.
		if !reflect.DeepEqual(DefaultRetryPolicy, want.Retry) {
			t.Fatalf("DefaultRetryPolicy = %+v, want %+v", DefaultRetryPolicy, want.Retry)
		}
		if got := ResolveSettings(nil); !reflect.DeepEqual(got, want) {
			t.Fatalf("settings = %+v, want %+v", got, want)
		}
		resolved := ResolveSettings(&HarnessSettings{Retry: &RetryPolicyPatch{Enabled: new(false)}, Compaction: &CompactionPolicyPatch{BackgroundTokens: new(0)}})
		if resolved.Retry.Enabled || resolved.Retry.MaxRetries != 3 || resolved.Retry.BaseDelayMs != 2000 {
			t.Fatalf("retry = %+v", resolved.Retry)
		}
		if !resolved.Compaction.Enabled || resolved.Compaction.BackgroundTokens != 0 {
			t.Fatalf("compaction = %+v", resolved.Compaction)
		}
		progress := ResolveSettings(&HarnessSettings{Progress: &ProgressPolicyPatch{OutputIntervalMs: new(500.0)}}).Progress
		if progress != (durable.ProgressPolicy{PartialIntervalMs: 100, OutputIntervalMs: 500}) {
			t.Fatalf("progress = %+v", progress)
		}
	})

	t.Run("commits partials no more often than progress.partialIntervalMs", func(t *testing.T) {
		// upstream: packages/durable/test/harness-generation.test.ts:507
		publishedPartial := func(progress *ProgressPolicyPatch) bool {
			setup := chatSetup(t)
			withProvider(setup, func(provider *ai.ModelsProvider) {
				provider.StreamSimple = func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
					// Longer than the default 100 ms, shorter than the configured interval.
					return streamOf(fauxMessage("partial", ai.StopReasonPending), 300*time.Millisecond, fauxMessage("final", ai.StopReasonStop)), nil
				}
			})
			setup.SetSettings(func(settings *HarnessSettings) { settings.Progress = progress })
			harness, root := openChat(t, storage.NewMemoryStorage(), setup)
			values := observeLive(t, harness)
			harness.Resume()
			expectSettled(t, must(submitInput(t, root, "hi").Wait(testContext)), durable.SubmissionDone, "")
			closeHarness(t, harness)
			return values.some(func(value LiveState) bool {
				if value.Generation == nil {
					return false
				}
				got, _ := messageText(t, value.Generation.Message)
				return got == "partial"
			})
		}
		if !publishedPartial(nil) {
			t.Fatal("the default interval published no partial")
		}
		if publishedPartial(&ProgressPolicyPatch{PartialIntervalMs: new(5000.0)}) {
			t.Fatal("a partial was published within partialIntervalMs")
		}
	})

	t.Run("renders sections that read conversation documents through input.read", func(t *testing.T) {
		// upstream: packages/durable/test/harness-generation.test.ts:534
		type agentState struct {
			Cwd  string `json:"cwd"`
			Kind string `json:"kind"`
		}
		agentDoc := durable.DefineDoc(durable.DocDefinition[agentState]{
			CommonDocDefinition: durable.CommonDocDefinition[agentState]{Kind: "test.agent", Version: 1, Initial: func() agentState { return agentState{Cwd: "/", Kind: "main"} }},
			DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: durable.ForkCurrent},
		})
		read := func(ctx context.Context, input durable.PromptInput) (*agentState, error) {
			return durable.Snapshot(ctx, input.Read, agentDoc, input.ConversationId)
		}
		setup := chatSetup(t)
		addSection(t, setup.Registry, "cwd", func(ctx context.Context, input durable.PromptInput) (*string, error) {
			state, err := read(ctx, input)
			if err != nil || state == nil {
				return nil, err
			}
			return new(state.Cwd), nil
		})
		addSection(t, setup.Registry, "agents", func(ctx context.Context, input durable.PromptInput) (*string, error) {
			state, err := read(ctx, input)
			if err != nil {
				return nil, err
			}
			if state != nil && state.Kind == "sub" {
				return nil, nil
			}
			return new("Read AGENTS.md"), nil
		})
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("a"), fauxAnswer("b")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		rootId := root.Id()
		commitValue(t, root, func(tx durable.Tx) (struct{}, error) {
			agent, err := durable.TxDoc[agentState](tx, agentDoc, rootId)
			if err != nil {
				return struct{}{}, err
			}
			return struct{}{}, agent.Set("cwd", "/repo")
		})
		sub := must(harness.CreateConversation(testContext, ConversationCreateOptions{
			Ownership: ownerless,
			Agent:     &AgentChange{Model: SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})},
			Init: func(tx durable.Tx, id durable.ConversationId) error {
				agent, err := durable.TxDoc[agentState](tx, agentDoc, id)
				if err != nil {
					return err
				}
				if err := agent.Set("kind", "sub"); err != nil {
					return err
				}
				return agent.Set("cwd", "/sub")
			},
		}))
		harness.Resume()
		must(submitInput(t, root, "one").Wait(testContext))
		must(submitInput(t, sub, "two").Wait(testContext))
		sections := func(conversation Conversation) ai.SystemMessage {
			for _, entry := range allEntries(t, conversation) {
				if entry.Kind == "pi.system" {
					return entry.Model[0].(ai.SystemMessage)
				}
			}
			t.Fatal("no pi.system entry")
			return ai.SystemMessage{}
		}
		rootSystem := sections(root)
		expectEqualJSON(t, rootSystem.Sections, `{"cwd":"<cwd>\n/repo\n</cwd>","agents":"<agents>\nRead AGENTS.md\n</agents>"}`)
		subSystem := sections(sub)
		expectEqualJSON(t, subSystem, jsonText(t, map[string]any{"role": "system", "content": "", "sections": map[string]any{"cwd": "<cwd>\n/sub\n</cwd>"}, "timestamp": subSystem.Timestamp}))
		closeHarness(t, harness)
	})

	t.Run("commits no partial for a response that turns deferred after an empty start event", func(t *testing.T) {
		// upstream: packages/durable/test/harness-generation.test.ts:586
		setup := chatSetup(t)
		pollAfter := int64(60_000)
		handle := ai.DeferredHandle{Provider: "faux", ModelID: "faux-1", API: "faux", ID: "handle-1", PollAfterMS: &pollAfter}
		withProvider(setup, func(provider *ai.ModelsProvider) {
			provider.StreamSimple = func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				final := fauxMessage("", ai.StopReasonDeferred)
				final.Deferred = &handle
				// Longer than the partial throttle: an empty partial would be committed here.
				return streamOf(fauxMessage("", ai.StopReasonPending), 300*time.Millisecond, final), nil
			}
		})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		values := observeLive(t, harness)
		submission := submitInput(t, root, "hi")
		waitFor(t, func() bool {
			state := live(t, harness, root)
			return state.Generation != nil && state.Generation.Deferred != nil
		})
		if values.some(func(value LiveState) bool { return value.Generation != nil && value.Generation.Message != nil }) {
			t.Fatal("a partial was committed")
		}
		must(harness.AbortTask(testContext, live(t, harness, root).Run.TaskId))
		expectSettled(t, must(submission.Wait(testContext)), durable.SubmissionUnanswered, "aborted")
		expectKinds(t, allEntries(t, root), "pi.user")
		closeHarness(t, harness)
	})

	t.Run("faults a run task, settling its inputs and converting the committed partial", func(t *testing.T) {
		// upstream: packages/durable/test/harness-generation.test.ts:612
		setup := chatSetup(t)
		withProvider(setup, func(provider *ai.ModelsProvider) {
			provider.StreamSimple = func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				// The final message is not strict JSON, so the classification commit fails and the scheduler faults the
				// task. A Go message cannot hold a function, so a non-finite number stands for upstream's function.
				final := fauxMessage("final", ai.StopReasonStop)
				final.Usage.Cost.Total = math.NaN()
				return streamOf(fauxMessage("partial", ai.StopReasonPending), 300*time.Millisecond, final), nil
			}
		})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		values := observeLive(t, harness)
		harness.Resume()
		submission := submitInput(t, root, "hi")
		settled := must(submission.Wait(testContext))
		expectSettled(t, settled, durable.SubmissionUnanswered, "faulted")
		if detail, _ := settled.Detail.(string); !strings.Contains(detail, "is not strict JSON") {
			t.Fatalf("detail = %v, want the strict JSON failure", settled.Detail)
		}
		if !values.some(func(value LiveState) bool {
			if value.Generation == nil {
				return false
			}
			got, _ := messageText(t, value.Generation.Message)
			return got == "partial"
		}) {
			t.Fatal("the partial was never committed")
		}
		entries := allEntries(t, root)
		expectKinds(t, entries, "pi.user", "pi.assistant")
		converted := entries[1].Model[0].(ai.AssistantMessage)
		if converted.StopReason != ai.StopReasonAborted {
			t.Fatalf("stopReason = %s, want aborted", converted.StopReason)
		}
		if got, _ := textOf(converted); got != "partial" {
			t.Fatalf("converted text = %q, want partial", got)
		}
		expectEqualJSON(t, live(t, harness, root), `{}`)
		state := conversationTasks(t, harness, root)[0].State
		if state.Status != durable.TaskTerminal || state.Outcome.Status != durable.OutcomeFaulted {
			t.Fatalf("state = %s", jsonText(t, state))
		}
		closeHarness(t, harness)
	})

	t.Run("orphans a blocked run task with full run cleanup", func(t *testing.T) {
		// upstream: packages/durable/test/harness-generation.test.ts:635
		setup := chatSetup(t)
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		rootId := root.Id()
		// A run whose task was stored by a newer generation definition this process cannot run.
		newer := *GenerationTask.Definition
		newer.Version = 2
		newerTask := durable.DefineTask(newer)
		type created struct {
			taskId       durable.TaskId
			submissionId durable.SubmissionId
		}
		ids := must(commitOnLine(testContext, harness.(*harnessImpl).SessionImpl, session.TransactionScope{}, func(tx *session.Transaction) (created, error) {
			entry, err := durable.TxAppendEntry(tx, durable.UserEntry, rootId, durable.TypedEntryDraft[durable.Never]{Model: []ai.Message{ai.UserMessage{Content: ai.UserText("hi"), Timestamp: 1}}})
			if err != nil {
				return created{}, err
			}
			submission, err := tx.CreateSubmission(durable.SubmissionCreate{ConversationId: rootId, Type: durable.SubmissionTypeInput, Status: durable.SubmissionPlaced, Entry: &entry.Id})
			if err != nil {
				return created{}, err
			}
			taskId, err := durable.CreateTask(tx, newerTask, GenerationInput{}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}, ConversationId: &rootId})
			if err != nil {
				return created{}, err
			}
			liveDraft, err := docDraft(tx, LiveDoc, rootId)
			if err != nil {
				return created{}, err
			}
			if err := setJSON(liveDraft, "run", LiveRun{TaskId: taskId, Inputs: []durable.SubmissionId{submission.Id}}); err != nil {
				return created{}, err
			}
			return created{taskId: taskId, submissionId: submission.Id}, nil
		}))
		harness.Resume()
		_, err := root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("busy"), WhenBusy: durable.WhenBusyReject})
		expectError(t, err, "is busy")
		if result := must(harness.AbortTask(testContext, ids.taskId)); result != "marked" {
			t.Fatalf("abort = %s, want marked", result)
		}
		expectEqualJSON(t, must(harness.WaitForTask(testContext, ids.taskId)).State.Outcome, `{"status":"orphaned","reason":"task_too_old"}`)
		expectSettled(t, must(must(harness.Submission(testContext, ids.submissionId)).Status(testContext)), durable.SubmissionUnanswered, "task_too_old")
		expectEqualJSON(t, live(t, harness, root), `{}`)
		closeHarness(t, harness)
	})

	t.Run("rejects a registry without the built-in tasks", func(t *testing.T) {
		// upstream: packages/durable/test/harness-generation.test.ts:674
		reader := withoutGeneration{snapshot: CreateRegistry().Snapshot()}
		_, err := OpenHarness(testContext, storage.NewMemoryStorage(), HarnessOptions{Models: chatSetup(t).Models, Registry: reader})
		expectError(t, err, "Registry lacks built-in tasks pi.generation")
	})
}

// withoutGeneration is a registry reader whose snapshot lacks pi.generation.
type withoutGeneration struct{ snapshot durable.RegistrySnapshot }

func (reader withoutGeneration) Snapshot() durable.RegistrySnapshot {
	return generationlessSnapshot{reader.snapshot}
}

func (withoutGeneration) Subscribe(func()) func() { return func() {} }

type generationlessSnapshot struct{ durable.RegistrySnapshot }

func (snapshot generationlessSnapshot) Installed() []*durable.Extension { return nil }

func (snapshot generationlessSnapshot) Extension(string) *durable.Extension { return nil }

func (snapshot generationlessSnapshot) Tools() []durable.RegistryTool { return nil }

func (snapshot generationlessSnapshot) Sections() []durable.RegistrySection { return nil }

func (snapshot generationlessSnapshot) Tasks() []durable.AnyTask {
	return slices.DeleteFunc(snapshot.RegistrySnapshot.Tasks(), func(task durable.AnyTask) bool {
		return task.AnyDefinition().Name == "pi.generation"
	})
}

func (snapshot generationlessSnapshot) Task(name string) durable.AnyTask {
	if name == "pi.generation" {
		return nil
	}
	return snapshot.RegistrySnapshot.Task(name)
}

// Not an upstream case: streamResponse arms its partial throttle with setTimeout(flush, partialIntervalMs), and Node
// truncates the delay to whole milliseconds and replaces one below 1 ms, above 2147483647 ms, or NaN with 1 ms.
func TestPartialThrottleDelayFollowsSetTimeout(t *testing.T) {
	for _, step := range []struct {
		milliseconds float64
		want         time.Duration
	}{
		{100, 100 * time.Millisecond},
		{150.9, 150 * time.Millisecond},
		{1, time.Millisecond},
		{0.5, time.Millisecond},
		{0, time.Millisecond},
		{-5, time.Millisecond},
		{math.NaN(), time.Millisecond},
		{2_147_483_647, 2_147_483_647 * time.Millisecond},
		{2_147_483_648, time.Millisecond},
		{math.Inf(1), time.Millisecond},
	} {
		if got := millisecondsDuration(step.milliseconds); got != step.want {
			t.Errorf("millisecondsDuration(%v) = %v, want %v", step.milliseconds, got, step.want)
		}
	}
}
