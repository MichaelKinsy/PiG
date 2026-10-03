// Ports packages/durable/test/harness-generation-recovery.test.ts.

package harness

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

func openAt(t *testing.T, path string, setup *chatState) (Harness, Conversation) {
	t.Helper()
	return openChat(t, openSqlite(t, path), setup)
}

func runTaskId(t *testing.T, harness Harness) durable.TaskId {
	t.Helper()
	return liveOf(t, harness, durable.ROOT_CONVERSATION_ID).Run.TaskId
}

// taskCheckpoint returns the task's checkpoint, or nil once terminal.
func taskCheckpoint(t *testing.T, harness Harness, id durable.TaskId) durable.JsonValue {
	t.Helper()
	record := must(harness.GetTask(testContext, id))
	if record == nil || record.State.Status == durable.TaskTerminal || record.State.Checkpoint == nil {
		return nil
	}
	return *record.State.Checkpoint
}

// expectMatch fails unless every member of want is present in got with an equal JSON value (toMatchObject).
func expectMatch(t *testing.T, got any, want string) {
	t.Helper()
	var left, right any
	if err := json.Unmarshal([]byte(jsonText(t, got)), &left); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &right); err != nil {
		t.Fatal(err)
	}
	if !matchesObject(left, right) {
		t.Fatalf("got  %s\nwant a match of %s", jsonText(t, got), want)
	}
}

// withModels returns a setup sharing everything with setup except its models.
func withModels(setup *chatState, models *ai.Models) *chatState {
	setup.mu.Lock()
	defer setup.mu.Unlock()
	return &chatState{Faux: setup.Faux, Models: models, Registry: setup.Registry, Reports: setup.Reports, settings: setup.settings, now: setup.now}
}

func submissionOfHarness(t *testing.T, harness Harness, id durable.SubmissionId) durable.Submission {
	t.Helper()
	submission := must(harness.Submission(testContext, id))
	if submission == nil {
		t.Fatalf("submission %d does not exist", id)
	}
	return submission
}

func TestGenerationRecovery(t *testing.T) {
	t.Run("reruns preparation interrupted before its commit", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		setup := chatSetup(t)
		reached := deferred()
		var mu sync.Mutex
		block := true
		addSection(t, setup.Registry, "preamble", func(ctx context.Context, _ durable.PromptInput) (*string, error) {
			mu.Lock()
			blocking := block
			block = false
			mu.Unlock()
			if blocking {
				reached.resolve()
				return nil, abortedBy(ctx)
			}
			return new("p"), nil
		})
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("answer")})
		harness, root := openAt(t, path, setup)
		harness.Resume()
		id := submitInput(t, root, "hi").Id()
		if err := reached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		taskId := runTaskId(t, harness)
		closeHarness(t, harness)

		harness, root = openAt(t, path, setup)
		expectEqualJSON(t, taskCheckpoint(t, harness, taskId), `{"phase":"prepare","attempt":1}`)
		harness.Resume()
		expectSettled(t, must(submissionOfHarness(t, harness, id).Wait(testContext)), durable.SubmissionDone, "")
		expectKinds(t, allEntries(t, root), "pi.user", "pi.system", "pi.assistant")
		closeHarness(t, harness)
	})

	t.Run("resends a request interrupted before any partial without repeating preparation", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		setup := chatSetup(t)
		addSection(t, setup.Registry, "preamble", text("p"), SectionOptions{Tag: new(false)})
		reached := deferred()
		var mu sync.Mutex
		sent := [][]string{}
		timeouts := []*int{}
		setup.Faux.SetResponses([]ai.FauxResponseStep{
			ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.FauxResponse, error) {
				reached.resolve()
				return ai.FauxResponse{}, abortedBy(options.Signal)
			}),
			ai.FauxFactoryStep(func(request ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.FauxResponse, error) {
				mu.Lock()
				sent = append(sent, roles(request.Messages()))
				timeouts = append(timeouts, options.TimeoutMs)
				mu.Unlock()
				return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("answer")}}, nil
			}),
		})
		harness, root := openAt(t, path, setup)
		setup.SetSettings(func(settings *HarnessSettings) {
			settings.Stream = &durable.ConversationStreamOptions{TimeoutMs: new(1234)}
		})
		harness.Resume()
		id := submitInput(t, root, "hi").Id()
		if err := reached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		taskId := runTaskId(t, harness)
		closeHarness(t, harness)

		harness, root = openAt(t, path, setup)
		expectMatch(t, taskCheckpoint(t, harness, taskId), `{"phase":"request","attempt":1,"thinkingLevel":"off","streamOptions":{"timeoutMs":1234}}`)
		// The resend uses the pinned request, not options changed after preparation.
		setup.SetSettings(func(settings *HarnessSettings) {
			settings.Stream = &durable.ConversationStreamOptions{TimeoutMs: new(999)}
		})
		expectMatch(t, liveOf(t, harness, root.Id()), `{"generation":{"attempt":1}}`)
		harness.Resume()
		expectSettled(t, must(submissionOfHarness(t, harness, id).Wait(testContext)), durable.SubmissionDone, "")
		mu.Lock()
		expectEqualJSON(t, sent, `[["user","system"]]`)
		expectEqualJSON(t, timeouts, `[1234]`)
		mu.Unlock()
		expectKinds(t, allEntries(t, root), "pi.user", "pi.system", "pi.assistant")
		closeHarness(t, harness)
	})

	t.Run("converts a committed partial into an aborted entry and resends the same messages", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		slow := chatSetup(t, ai.FauxConfig{TokensPerSecond: 20, MinTokenSize: 1, MaxTokenSize: 1})
		slow.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer(strings.Repeat("z", 400))})
		harness, root := openAt(t, path, slow)
		harness.Resume()
		watch := must(harness.WatchDocErased(testContext, LiveDoc, root.Id()))
		var mu sync.Mutex
		watched := ""
		seen := false
		watch.Start(func(_ context.Context, value durable.JsonObject, _ []durable.Op) error {
			generation, _ := value["generation"].(map[string]any)
			message, _ := generation["message"].(map[string]any)
			if got, ok := messageText(t, message); ok {
				mu.Lock()
				watched, seen = got, true
				mu.Unlock()
			}
			return nil
		})
		id := submitInput(t, root, "hi").Id()
		waitFor(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return seen
		})
		closeHarness(t, harness)

		setup := chatSetup(t)
		sent := [][]string{}
		setup.Faux.SetResponses([]ai.FauxResponseStep{
			ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.FauxResponse, error) {
				mu.Lock()
				sent = append(sent, roles(request.Messages()))
				mu.Unlock()
				return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("answer")}}, nil
			}),
		})
		harness, root = openAt(t, path, setup)
		stored := liveOf(t, harness, root.Id())
		partial, _ := messageText(t, stored.Generation.Message)
		// Everything observers saw before the crash is durable.
		mu.Lock()
		if !strings.HasPrefix(partial, watched) {
			t.Fatalf("stored partial %q does not extend the watched %q", partial, watched)
		}
		mu.Unlock()
		harness.Resume()
		expectSettled(t, must(submissionOfHarness(t, harness, id).Wait(testContext)), durable.SubmissionDone, "")
		mu.Lock()
		expectEqualJSON(t, sent, `[["user"]]`)
		mu.Unlock()
		entries := allEntries(t, root)
		expectKinds(t, entries, "pi.user", "pi.assistant", "pi.assistant")
		converted := entries[1].Model[0].(ai.AssistantMessage)
		if converted.StopReason != ai.StopReasonAborted {
			t.Fatalf("stopReason = %s, want aborted", converted.StopReason)
		}
		if got, _ := textOf(converted); got != partial {
			t.Fatalf("converted = %q, want %q", got, partial)
		}
		expectEqualJSON(t, liveOf(t, harness, root.Id()), `{}`)
		closeHarness(t, harness)
	})

	t.Run("resumes a retry backoff after reopen", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		setup := chatSetup(t)
		var mu sync.Mutex
		now := 1_000.0
		setup.SetNow(func() float64 {
			mu.Lock()
			defer mu.Unlock()
			return now
		})
		setup.Faux.SetResponses([]ai.FauxResponseStep{error503(), fauxAnswer("recovered")})
		harness, root := openAt(t, path, setup)
		harness.Resume()
		setup.SetSettings(func(settings *HarnessSettings) {
			settings.Retry = &RetryPolicyPatch{Enabled: new(true), MaxRetries: new(2), BaseDelayMs: new(60_000)}
		})
		id := submitInput(t, root, "hi").Id()
		waitFor(t, func() bool {
			state := liveOf(t, harness, root.Id())
			return state.Generation != nil && state.Generation.Retry != nil
		})
		taskId := runTaskId(t, harness)
		closeHarness(t, harness)

		harness, root = openAt(t, path, setup)
		expectEqualJSON(t, taskCheckpoint(t, harness, taskId), `{"phase":"retry","attempt":1,"until":61000}`)
		expectEqualJSON(t, liveOf(t, harness, root.Id()), jsonText(t, map[string]any{
			"run":        map[string]any{"taskId": taskId, "inputs": []any{id}},
			"generation": map[string]any{"attempt": 1, "retry": map[string]any{"at": 61_000, "error": "503 Service Unavailable"}},
		}))
		mu.Lock()
		now = 61_000
		mu.Unlock()
		harness.Resume()
		expectSettled(t, must(submissionOfHarness(t, harness, id).Wait(testContext)), durable.SubmissionDone, "")
		expectKinds(t, allEntries(t, root), "pi.user", "pi.assistant", "pi.assistant")
		closeHarness(t, harness)
	})

	t.Run("resumes polling a deferred response after reopen", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		pollAfter := int64(60_000)
		setup := chatSetup(t, ai.FauxConfig{Deferred: &ai.FauxDeferredConfig{PollAfterMS: &pollAfter}})
		var mu sync.Mutex
		now := 1_000.0
		setup.SetNow(func() float64 {
			mu.Lock()
			defer mu.Unlock()
			return now
		})
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("deferred answer")})
		harness, root := openAt(t, path, setup)
		harness.Resume()
		setup.SetSettings(func(settings *HarnessSettings) {
			settings.Stream = &durable.ConversationStreamOptions{Deferred: &ai.DeferredOption{Enabled: true}}
		})
		id := submitInput(t, root, "hi").Id()
		waitFor(t, func() bool {
			state := liveOf(t, harness, root.Id())
			return state.Generation != nil && state.Generation.Deferred != nil
		})
		taskId := runTaskId(t, harness)
		closeHarness(t, harness)

		harness, root = openAt(t, path, setup)
		expectMatch(t, taskCheckpoint(t, harness, taskId), `{"phase":"poll","attempt":1,"pollAt":61000}`)
		mu.Lock()
		now = 61_000
		mu.Unlock()
		harness.Resume()
		settled := must(submissionOfHarness(t, harness, id).Wait(testContext))
		if settled.Status != durable.SubmissionDone || settled.Type != durable.SubmissionTypeInput {
			t.Fatalf("Unexpected %s", settled.Status)
		}
		answer := commitValue(t, root, func(tx durable.Tx) (*durable.TypedEntry[durable.Never], error) {
			return durable.TxEntry(tx, durable.AssistantEntry, *settled.Answer)
		})
		if got, _ := textOf(answer.Model[0]); got != "deferred answer" {
			t.Fatalf("answer = %q", got)
		}
		if fetches := setup.Faux.(interface{ DeferredFetchCount() int }).DeferredFetchCount(); fetches != 1 {
			t.Fatalf("deferred fetches = %d, want 1", fetches)
		}
		closeHarness(t, harness)
	})

	t.Run("fails no_model when the pinned model is gone after reopen, in request and in poll", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		pollAfter := int64(60_000)
		setup := chatSetup(t, ai.FauxConfig{Deferred: &ai.FauxDeferredConfig{PollAfterMS: &pollAfter}})
		busy := unanswered()
		setup.Faux.SetResponses([]ai.FauxResponseStep{busy.step, fauxAnswer("deferred")})
		harness, root := openAt(t, path, setup)
		harness.Resume()
		requesting := submitInput(t, root, "one").Id()
		<-busy.reached
		closeHarness(t, harness)

		// Reopened without the faux provider: the request's pinned model is unknown.
		empty := withModels(setup, ai.CreateModels())
		harness, _ = openAt(t, path, empty)
		harness.Resume()
		expectSettled(t, must(submissionOfHarness(t, harness, requesting).Wait(testContext)), durable.SubmissionUnanswered, "no_model")
		closeHarness(t, harness)

		harness, root = openAt(t, path, setup)
		setup.SetSettings(func(settings *HarnessSettings) {
			settings.Stream = &durable.ConversationStreamOptions{Deferred: &ai.DeferredOption{Enabled: true}}
		})
		harness.Resume()
		polling := submitInput(t, root, "two").Id()
		waitFor(t, func() bool {
			state := liveOf(t, harness, root.Id())
			return state.Generation != nil && state.Generation.Deferred != nil
		})
		closeHarness(t, harness)

		harness, _ = openAt(t, path, empty)
		harness.Resume()
		expectSettled(t, must(submissionOfHarness(t, harness, polling).Wait(testContext)), durable.SubmissionUnanswered, "no_model")
		closeHarness(t, harness)
	})

	t.Run("runs a print-style turn and reads the durable answer after reopen", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		setup := chatSetup(t)
		addSection(t, setup.Registry, "preamble", text("You are terse."), SectionOptions{Tag: new(false)})
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("42")})
		harness, root := openAt(t, path, setup)
		harness.Resume()
		submission := submitInput(t, root, "answer?")
		settled := must(submission.Wait(testContext))
		if settled.Status != durable.SubmissionDone || settled.Type != durable.SubmissionTypeInput {
			t.Fatalf("Unexpected %s", settled.Status)
		}
		readAnswer := func(conversation Conversation) *durable.TypedEntry[durable.Never] {
			return commitValue(t, conversation, func(tx durable.Tx) (*durable.TypedEntry[durable.Never], error) {
				return durable.TxEntry(tx, durable.AssistantEntry, *settled.Answer)
			})
		}
		answer := readAnswer(root)
		if got, _ := textOf(answer.Model[0]); got != "42" {
			t.Fatalf("answer = %q", got)
		}
		closeHarness(t, harness)

		harness, root = openAt(t, path, setup)
		expectEqualJSON(t, must(submissionOfHarness(t, harness, submission.Id()).Status(testContext)), jsonText(t, settled))
		expectEqualJSON(t, readAnswer(root).EntryRecord, jsonText(t, answer.EntryRecord))
		closeHarness(t, harness)
	})
}

func roles(messages []ai.Message) []string {
	names := []string{}
	for _, message := range messages {
		var role struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(must(json.Marshal(message)), &role); err != nil {
			panic(err)
		}
		names = append(names, role.Role)
	}
	return names
}

// matchesObject is toMatchObject over JSON values: objects match by subset recursively, other values by equality.
func matchesObject(got, want any) bool {
	switch typed := want.(type) {
	case map[string]any:
		object, ok := got.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range typed {
			member, present := object[key]
			if !present || !matchesObject(member, value) {
				return false
			}
		}
		return true
	case []any:
		array, ok := got.([]any)
		if !ok || len(array) != len(typed) {
			return false
		}
		for index := range typed {
			if !matchesObject(array[index], typed[index]) {
				return false
			}
		}
		return true
	}
	return jsonEqual(got, want)
}
