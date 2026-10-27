package harness

// pi: packages/durable/src/harness/live.ts PROGRESS_COMMIT

import (
	"context"
	"errors"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// Deferred progress commits (ProgressCommit): the partial answer and tool output reach watches at once and Storage at the next
// commit that reads or writes pi.live or within settings.progress.persistWithinMs. Crashes are simulated by failing the
// Harness: from that moment nothing more reaches Storage, as when the process dies; the reopened Harness resumes from what was
// stored. Ports packages/durable/test/harness-deferred-progress.test.ts (Pi 1.4.0+6c2fd73b).

func longText() string { return strings.Repeat("streamed ", 400) }

func infinity() float64 { return math.Inf(1) }

func itoaString(value int) string { return strconv.Itoa(value) }

func contains(texts []string, want string) bool { return slices.Contains(texts, want) }

func must0(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func expectAnswers(t *testing.T, got, want []entryAnswer) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("answers = %+v, want %+v", got, want)
	}
}

// slowSetup streams 4 characters 80 times a second.
func slowProgressSetup(t *testing.T, persistWithinMs float64) *chatState {
	t.Helper()
	four := 4
	setup := chatSetup(t, ai.FauxConfig{TokensPerSecond: 80, TokenSize: &ai.FauxTokenSize{Min: &four, Max: &four}})
	setup.SetSettings(func(settings *HarnessSettings) {
		settings.Progress = &ProgressPolicyPatch{PersistWithinMs: &persistWithinMs}
		// A request a crash interrupted is a failed attempt (#11139): retry at once.
		settings.Retry = &RetryPolicyPatch{Enabled: new(true), MaxRetries: new(5), BaseDelayMs: new(1)}
	})
	return setup
}

// partialLog is the partial text of every pi.live revision memory adopted, the last one last.
type textLog struct {
	mu    sync.Mutex
	texts []string
}

func (log *textLog) all() []string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]string(nil), log.texts...)
}

func (log *textLog) lastLength() int {
	log.mu.Lock()
	defer log.mu.Unlock()
	if len(log.texts) == 0 {
		return 0
	}
	return len(log.texts[len(log.texts)-1])
}

func (log *textLog) last() string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return log.texts[len(log.texts)-1]
}

type progressRun struct {
	harness Harness
	root    Conversation
	memory  *textLog
}

// openProgress opens (or reopens) a Harness over storage and lets it run tasks.
func openProgress(t *testing.T, storage *controlledStorage, setup *chatState) *progressRun {
	t.Helper()
	storage.Reopen()
	harness, root := openChat(t, storage, setup)
	harness.Resume()
	return &progressRun{harness: harness, root: root, memory: memoryPartials(t, harness)}
}

func memoryPartials(t *testing.T, harness Harness) *textLog {
	t.Helper()
	log := &textLog{}
	observeCommits(harness, func(_ context.Context, publication session.ObservedCommit) {
		for _, change := range documentChanges(publication.Changes) {
			if change.Record.Kind != "pi.live" || change.Value == nil {
				continue
			}
			live, err := durable.FromJsonValue[LiveState](change.Value)
			if err != nil {
				t.Error(err)
				return
			}
			if live.Generation != nil && live.Generation.Message != nil {
				text, _ := messageText(t, live.Generation.Message)
				log.mu.Lock()
				log.texts = append(log.texts, text)
				log.mu.Unlock()
			}
		}
	})
	return log
}

// crash fails the Harness now: nothing more is stored, held changes are lost with it.
func crash(t *testing.T, harness Harness) {
	t.Helper()
	impl := harness.(*harnessImpl)
	impl.Fail(testContext, errors.New("crash"))
	<-impl.Closed()
}

// storedLiveValue is pi.live as Storage has it, read past the Session, also after a crash closed it; nil when absent.
func storedLiveValue(t *testing.T, storage *controlledStorage, conversationId durable.ConversationId) durable.JsonObject {
	t.Helper()
	storage.Reopen()
	record, err := storage.FindDocument(testContext, durable.DocumentAddress{Kind: "pi.live", Scope: durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: conversationId}}, durable.CurrentPoint)
	if err != nil || record == nil {
		if err != nil {
			t.Fatal(err)
		}
		return nil
	}
	stored, err := storage.Document(testContext, record.Id, durable.CurrentPoint)
	if err != nil || stored == nil {
		t.Fatalf("pi.live document: %v, %v", stored, err)
	}
	return stored.Value
}

func storedLive(t *testing.T, storage *controlledStorage, conversationId durable.ConversationId) *LiveState {
	t.Helper()
	live, err := durable.DecodeDoc[LiveState](storedLiveValue(t, storage, conversationId))
	if err != nil {
		t.Fatal(err)
	}
	return live
}

// storedText is the text of the stored partial message, ok false when none is stored.
func storedText(t *testing.T, live *LiveState) (string, bool) {
	t.Helper()
	if live == nil || live.Generation == nil || live.Generation.Message == nil {
		return "", false
	}
	text, _ := messageText(t, live.Generation.Message)
	return text, true
}

func storedTextLength(t *testing.T, storage *controlledStorage, conversationId durable.ConversationId) int {
	text, _ := storedText(t, storedLive(t, storage, conversationId))
	return len(text)
}

type entryAnswer struct {
	text       string
	stopReason ai.StopReason
}

// answers are the assistant entries: their text and stop reason.
func answers(entries []durable.EntryRecord) []entryAnswer {
	var out []entryAnswer
	for _, entry := range entries {
		if entry.Kind != "pi.assistant" {
			continue
		}
		message := entry.Model[0].(ai.AssistantMessage)
		text, _ := textOf(message)
		out = append(out, entryAnswer{text, message.StopReason})
	}
	return out
}

func waitForLong(t *testing.T, check func() bool) { waitFor(t, check, 20*time.Second) }

func settleSubmission(t *testing.T, run *progressRun, id durable.SubmissionId) durable.SubmissionRecord {
	t.Helper()
	submission := must(run.harness.Submission(testContext, id))
	return must(submission.Wait(testContext))
}

func TestDeferredGenerationPartials(t *testing.T) {
	t.Run("hold partials in memory and store them together, at most every persistWithinMs", func(t *testing.T) {
		storage := newControlledStorage()
		setup := slowProgressSetup(t, 400)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer(longText()[:640])})
		run := openProgress(t, storage, setup)
		from := storage.commitCount()
		must(submitInput(t, run.root, "hi").Wait(testContext))
		writesLive := 0
		for index := from; index < storage.commitCount(); index++ {
			for _, write := range storage.commitAt(index) {
				if _, ok := write.(durable.DocumentChangeWrite); ok {
					writesLive++
					break
				}
			}
		}
		// About 2 s of streaming at 100 ms per partial: some 20 partials in memory, a store about every 400 ms.
		memory := len(run.memory.all())
		if memory <= 12 {
			t.Fatalf("memory saw %d partials, want more than 12", memory)
		}
		if float64(writesLive) >= float64(memory)/1.5 {
			t.Fatalf("%d storage batches wrote a document for %d partials", writesLive, memory)
		}
		must0(t, run.harness.Close(testContext))
	})

	t.Run("converts what was stored into the aborted entry after a crash, and retries the charged attempt", func(t *testing.T) {
		storage := newControlledStorage()
		setup := slowProgressSetup(t, 150)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer(longText())})
		run := openProgress(t, storage, setup)
		// A replica fed by SubscribeCommits alone, as a replication feed would be.
		var replicaMu sync.Mutex
		var replica durable.JsonValue = storedLiveValue(t, storage, run.root.Id())
		run.harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
			for _, change := range documentChanges(publication.Changes) {
				if change.Record.Kind != "pi.live" {
					continue
				}
				replicaMu.Lock()
				if replica == nil || (len(change.Ops) == 0 && change.Value != nil) {
					replica = change.Value
				} else {
					replica = must(delta.ApplyImmutable(replica, change.Ops))
				}
				replicaMu.Unlock()
			}
		})
		submission := submitInput(t, run.root, "hi")
		waitForLong(t, func() bool { return storedTextLength(t, storage, run.root.Id()) > 40 })
		inMemory := run.memory.last()
		crash(t, run.harness)
		stored, _ := storedText(t, storedLive(t, storage, run.root.Id()))
		// What Storage has is a state memory had, behind it by at most the window, and what the replica has.
		if !contains(run.memory.all(), stored) || !strings.HasPrefix(inMemory, stored) {
			t.Fatalf("stored %q is not a prefix state memory had (memory ended with %q)", stored, inMemory)
		}
		replicaMu.Lock()
		expectEqualJSON(t, replica, jsonText(t, storedLiveValue(t, storage, run.root.Id())))
		replicaMu.Unlock()

		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("answer")})
		reopened := openProgress(t, storage, setup)
		if settled := settleSubmission(t, reopened, submission.Id()); settled.Status != durable.SubmissionDone {
			t.Fatalf("the submission settled %s", settled.Status)
		}
		expectAnswers(t, answers(allEntries(t, reopened.root)), []entryAnswer{{stored, ai.StopReasonAborted}, {"answer", ai.StopReasonStop}})
		if live := liveOf(t, reopened.harness, reopened.root.Id()); live == nil || !reflect.DeepEqual(*live, LiveState{}) {
			t.Fatalf("pi.live = %+v, want empty", live)
		}
		must0(t, reopened.harness.Close(testContext))
	})

	t.Run("leaves no aborted entry for a stream that crashed before its first store", func(t *testing.T) {
		storage := newControlledStorage()
		setup := slowProgressSetup(t, infinity())
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer(longText())})
		run := openProgress(t, storage, setup)
		submission := submitInput(t, run.root, "hi")
		waitForLong(t, func() bool { return run.memory.lastLength() > 40 })
		crash(t, run.harness)
		live := storedLive(t, storage, run.root.Id())
		if live == nil || live.Generation == nil || live.Generation.Attempt != 1 {
			t.Fatalf("stored pi.live = %+v, want generation attempt 1", live)
		}
		if _, ok := storedText(t, live); ok {
			t.Fatal("a partial was stored")
		}

		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("answer")})
		reopened := openProgress(t, storage, setup)
		if settled := settleSubmission(t, reopened, submission.Id()); settled.Status != durable.SubmissionDone {
			t.Fatalf("the submission settled %s", settled.Status)
		}
		expectAnswers(t, answers(allEntries(t, reopened.root)), []entryAnswer{{"answer", ai.StopReasonStop}})
		must0(t, reopened.harness.Close(testContext))
	})

	t.Run("converts a stored partial once through two crashes, before and after the conversion", func(t *testing.T) {
		storage := newControlledStorage()
		setup := slowProgressSetup(t, 150)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer(longText())})
		run := openProgress(t, storage, setup)
		submission := submitInput(t, run.root, "hi")
		waitForLong(t, func() bool { return storedTextLength(t, storage, run.root.Id()) > 40 })
		crash(t, run.harness)
		stored, _ := storedText(t, storedLive(t, storage, run.root.Id()))

		// Crash again before anything ran: nothing was converted, so nothing is lost or doubled.
		storage.Reopen()
		idle, _ := openChat(t, storage, setup)
		crash(t, idle)
		if got, _ := storedText(t, storedLive(t, storage, run.root.Id())); got != stored {
			t.Fatalf("stored %q after a crash before anything ran, want %q", got, stored)
		}

		// Crash again while the resend streams: its conversion is stored, its own partials only held.
		infinite := infinity()
		setup.SetSettings(func(settings *HarnessSettings) { settings.Progress = &ProgressPolicyPatch{PersistWithinMs: &infinite} })
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer(longText())})
		resend := openProgress(t, storage, setup)
		waitForLong(t, func() bool { return resend.memory.lastLength() > 40 })
		crash(t, resend.harness)
		if got, ok := storedText(t, storedLive(t, storage, run.root.Id())); ok {
			t.Fatalf("stored %q after the resend crashed, want no partial", got)
		}

		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("answer")})
		reopened := openProgress(t, storage, setup)
		if settled := settleSubmission(t, reopened, submission.Id()); settled.Status != durable.SubmissionDone {
			t.Fatalf("the submission settled %s", settled.Status)
		}
		expectAnswers(t, answers(allEntries(t, reopened.root)), []entryAnswer{{stored, ai.StopReasonAborted}, {"answer", ai.StopReasonStop}})
		must0(t, reopened.harness.Close(testContext))
	})

	t.Run("stores everything held at a clean close", func(t *testing.T) {
		storage := newControlledStorage()
		setup := slowProgressSetup(t, infinity())
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer(longText())})
		run := openProgress(t, storage, setup)
		submission := submitInput(t, run.root, "hi")
		waitForLong(t, func() bool { return run.memory.lastLength() > 40 })
		must0(t, run.harness.Close(testContext))
		atClose := run.memory.last()
		if got, _ := storedText(t, storedLive(t, storage, run.root.Id())); got != atClose {
			t.Fatalf("stored %q after a clean close, want %q", got, atClose)
		}

		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("answer")})
		reopened := openProgress(t, storage, setup)
		settleSubmission(t, reopened, submission.Id())
		if got := answers(allEntries(t, reopened.root))[0]; got != (entryAnswer{atClose, ai.StopReasonAborted}) {
			t.Fatalf("first answer = %+v, want the closed partial, aborted", got)
		}
		must0(t, reopened.harness.Close(testContext))
	})
}

// streamingTool writes a line every 20 ms until its invocation is cancelled on its first run. A later run waits for
// cancellation while stall is set (stalled resolves then), and otherwise writes one line and returns.
type streamingTool struct {
	mu           sync.Mutex
	runs         int
	lines        []string
	stall        bool
	stalled      *deferredGate
	registration *durable.ToolRegistration
}

func newStreamingTool(replay string) *streamingTool {
	state := &streamingTool{stalled: deferred()}
	state.registration = DefineTool(durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{Name: "work", Description: "work", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
		Replay:     durable.ToolReplay(replay),
		Execute: func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			state.mu.Lock()
			state.runs++
			run, stall := state.runs, state.stall
			state.mu.Unlock()
			if run > 1 && stall {
				state.stalled.resolve()
				return durable.ToolExecutionResult{}, abortedBy(ctx)
			}
			if run > 1 {
				api.Output("rerun " + itoaString(run) + "\n")
				return durable.ToolExecutionResult{}, nil
			}
			for line := 0; ctx.Err() == nil; line++ {
				text := "line " + itoaString(line) + "\n"
				api.Output(text)
				state.mu.Lock()
				state.lines = append(state.lines, text)
				state.mu.Unlock()
				time.Sleep(20 * time.Millisecond)
			}
			return durable.ToolExecutionResult{}, context.Cause(ctx)
		},
	})
	return state
}

func (state *streamingTool) joined() string {
	state.mu.Lock()
	defer state.mu.Unlock()
	return strings.Join(state.lines, "")
}

func (state *streamingTool) runCount() int {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.runs
}

func toolResultTexts(entries []durable.EntryRecord) []string {
	var results []string
	for _, entry := range entries {
		if entry.Kind != durable.ToolResultEntry.Kind {
			continue
		}
		message, ok := entry.Model[0].(ai.ToolResultMessage)
		if !ok {
			results = append(results, "")
			continue
		}
		var texts []string
		for _, item := range message.Content {
			if text, ok := item.(ai.TextContent); ok {
				texts = append(texts, text.Text)
			} else {
				texts = append(texts, "")
			}
		}
		results = append(results, strings.Join(texts, "|"))
	}
	return results
}

func storedToolOutput(t *testing.T, storage *controlledStorage, conversationId durable.ConversationId) (string, bool) {
	t.Helper()
	live := storedLive(t, storage, conversationId)
	if live == nil || len(live.Tools) == 0 || live.Tools[0].Output == nil {
		return "", false
	}
	return *live.Tools[0].Output, true
}

func workStep() ai.FauxResponseStep {
	return toolCallStep(ai.FauxToolCall("work", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"}))
}

func TestDeferredToolOutput(t *testing.T) {
	// #11139: a generation's request is charged at once after a crash, so its partials never stream with a count; a
	// replay-safe tool reruns and streams its output with one.
	t.Run("never counts progress commits as progress of a task cut off again and again", func(t *testing.T) {
		storage := newControlledStorage()
		setup := slowProgressSetup(t, 50)
		var mu sync.Mutex
		runs := 0
		addTool(t, setup.Registry, DefineTool(durable.ToolRegistration{
			ToolSchema: ai.ToolSchema{Name: "work", Description: "work", Parameters: emptyObjectSchema()},
			Replay:     durable.ToolReplay("safe"),
			Execute: func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				mu.Lock()
				runs++
				run := runs
				mu.Unlock()
				for line := 0; ctx.Err() == nil; line++ {
					api.Output("run " + itoaString(run) + " line " + itoaString(line) + "\n")
					time.Sleep(20 * time.Millisecond)
				}
				return durable.ToolExecutionResult{}, context.Cause(ctx)
			},
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{workStep()})
		run := openProgress(t, storage, setup)
		submitInput(t, run.root, "go")
		for crashes := 1; crashes <= 2; crashes++ {
			// This run stored output of its own, a progress commit, before it was cut off.
			waitForLong(t, func() bool {
				output, _ := storedToolOutput(t, storage, run.root.Id())
				return strings.HasPrefix(output, "run "+itoaString(crashes)+" ")
			})
			crash(t, run.harness)
			run = openProgress(t, storage, setup)
			kind := "pi.tool"
			tool := must(durable.Commit(testContext, run.harness, func(tx durable.Tx) (durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error) {
				page, err := tx.ScanTasks(durable.TaskQuery{Kind: &kind}, 1, nil)
				return page.Items[0], err
			}))
			if tool.Restarts != crashes {
				t.Fatalf("after %d crashes the tool task counts %d restarts", crashes, tool.Restarts)
			}
		}
		must0(t, run.harness.Close(testContext))
	})

	t.Run("answers a tool without replay that crashed with the output stored by then", func(t *testing.T) {
		storage := newControlledStorage()
		setup := slowProgressSetup(t, 150)
		tool := newStreamingTool("unsafe")
		addTool(t, setup.Registry, tool.registration)
		setup.Faux.SetResponses([]ai.FauxResponseStep{workStep()})
		run := openProgress(t, storage, setup)
		submission := submitInput(t, run.root, "go")
		waitForLong(t, func() bool { output, _ := storedToolOutput(t, storage, run.root.Id()); return len(output) > 20 })
		crash(t, run.harness)
		stored, _ := storedToolOutput(t, storage, run.root.Id())
		if !strings.HasPrefix(tool.joined(), stored) {
			t.Fatalf("stored output %q is not a prefix of what the tool wrote %q", stored, tool.joined())
		}

		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done")})
		reopened := openProgress(t, storage, setup)
		if settled := settleSubmission(t, reopened, submission.Id()); settled.Status != durable.SubmissionDone {
			t.Fatalf("the submission settled %s", settled.Status)
		}
		if runs := tool.runCount(); runs != 1 {
			t.Fatalf("the tool ran %d times, want 1", runs)
		}
		expectEqualJSON(t, toolResultTexts(allEntries(t, reopened.root)), jsonText(t, []string{stored + "|<harness>\n[error] Tool work was interrupted and may have partially run\n</harness>"}))
		must0(t, reopened.harness.Close(testContext))
	})

	t.Run("reruns a replay-safe tool after a crash with its stored progress cleared, also through a second crash", func(t *testing.T) {
		storage := newControlledStorage()
		setup := slowProgressSetup(t, 150)
		tool := newStreamingTool("safe")
		addTool(t, setup.Registry, tool.registration)
		setup.Faux.SetResponses([]ai.FauxResponseStep{workStep()})
		run := openProgress(t, storage, setup)
		submission := submitInput(t, run.root, "go")
		waitForLong(t, func() bool { output, _ := storedToolOutput(t, storage, run.root.Id()); return len(output) > 20 })
		crash(t, run.harness)

		// The rerun's first commit stores the cleared progress; it crashes again before writing any of its own.
		tool.mu.Lock()
		tool.stall = true
		tool.mu.Unlock()
		rerun := openProgress(t, storage, setup)
		<-tool.stalled.done
		if output, ok := storedToolOutput(t, storage, run.root.Id()); ok {
			t.Fatalf("stored output %q after the rerun began, want it cleared", output)
		}
		crash(t, rerun.harness)
		if output, ok := storedToolOutput(t, storage, run.root.Id()); ok {
			t.Fatalf("stored output %q after the second crash, want it cleared", output)
		}

		tool.mu.Lock()
		tool.stall = false
		tool.mu.Unlock()
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done")})
		reopened := openProgress(t, storage, setup)
		if settled := settleSubmission(t, reopened, submission.Id()); settled.Status != durable.SubmissionDone {
			t.Fatalf("the submission settled %s", settled.Status)
		}
		if runs := tool.runCount(); runs != 3 {
			t.Fatalf("the tool ran %d times, want 3", runs)
		}
		expectEqualJSON(t, toolResultTexts(allEntries(t, reopened.root)), jsonText(t, []string{"rerun 3\n"}))
		must0(t, reopened.harness.Close(testContext))
	})
}

type auditInput struct {
	ConversationId durable.ConversationId `json:"conversationId"`
}

type auditState struct {
	Phase string `json:"phase"`
}

func TestReadingPiLiveInACommitStoresTheHeldPartial(t *testing.T) {
	// A third-party task's commit that reads pi.live stores the held partial with what it derived.
	storage := newControlledStorage()
	setup := slowProgressSetup(t, infinity())
	audit := durable.DefineTask(durable.TaskDefinition[auditInput, auditState, durable.JsonValue, any]{
		Name:    "test.audit",
		Version: 1,
		Initial: func(auditInput) auditState { return auditState{Phase: "run"} },
		Phases: map[string]durable.PhaseHandler[auditInput, auditState, durable.JsonValue, any]{
			"run": func(ctx context.Context, task durable.RunningTask[auditInput, auditState, durable.JsonValue], runtime durable.TaskRuntime[auditInput, auditState, durable.JsonValue, any]) error {
				return runtime.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[auditInput, auditState, durable.JsonValue]) (*durable.NextTaskState[auditState, durable.JsonValue], error) {
					draft, err := docDraft(tx, LiveDoc, task.Input.ConversationId)
					if err != nil {
						return nil, err
					}
					live, err := durable.DecodeDoc[LiveState](draft.Snapshot())
					if err != nil {
						return nil, err
					}
					text := ""
					if live != nil && live.Generation != nil && live.Generation.Message != nil {
						text, _ = textOf(*live.Generation.Message)
					}
					if _, err := tx.AppendEntry(task.Input.ConversationId, durable.EntryDraft{Kind: "test.audit", Data: text}); err != nil {
						return nil, err
					}
					return completed[auditState, durable.JsonValue](nil), nil
				})
			},
		},
		Abort: func(context.Context, durable.RunningTask[auditInput, auditState, durable.JsonValue], durable.TaskRuntime[auditInput, auditState, durable.JsonValue, any]) error {
			return nil
		},
	})
	addTask(t, setup.Registry, audit.Erased())
	setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer(longText())})
	run := openProgress(t, storage, setup)
	submission := submitInput(t, run.root, "hi")
	waitForLong(t, func() bool { return run.memory.lastLength() > 40 })
	conversationId := run.root.Id()
	auditId := must(durable.Commit(testContext, run.harness, func(tx durable.Tx) (durable.TaskId, error) {
		return durable.CreateTask(tx, audit, auditInput{ConversationId: conversationId}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}, ConversationId: &conversationId})
	}))
	must(run.harness.WaitForTask(testContext, auditId))
	var derived string
	for _, entry := range allEntries(t, run.root) {
		if entry.Kind == "test.audit" {
			derived, _ = entry.Data.(string)
		}
	}
	if len(derived) <= 40 {
		t.Fatalf("the audit derived %q, want the held partial", derived)
	}
	// The read stored the partial it saw, in the same storage transaction as the entry.
	found := false
	for index := range storage.commitCount() {
		batch := storage.commitAt(index)
		entry, document := false, false
		for _, write := range batch {
			switch typed := write.(type) {
			case durable.EntryWrite:
				entry = entry || typed.Value.Kind == "test.audit"
			case durable.DocumentChangeWrite:
				document = true
			}
		}
		if entry {
			found = true
			if !document {
				t.Fatal("the audit's commit stored no document change")
			}
		}
	}
	if !found {
		t.Fatal("no batch stored the audit entry")
	}
	crash(t, run.harness)
	if got, _ := storedText(t, storedLive(t, storage, conversationId)); got != derived {
		t.Fatalf("stored %q after the crash, want what the audit derived %q", got, derived)
	}

	setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("answer")})
	reopened := openProgress(t, storage, setup)
	settleSubmission(t, reopened, submission.Id())
	if got := answers(allEntries(t, reopened.root))[0]; got != (entryAnswer{derived, ai.StopReasonAborted}) {
		t.Fatalf("first answer = %+v, want the derived partial, aborted", got)
	}
	must0(t, reopened.harness.Close(testContext))
}

func TestACompactionCutOffByACrashRerunsFromItsCheckpoint(t *testing.T) {
	storage := newControlledStorage()
	chat := openCompaction(t, compactionOptions{storage: storage})
	chat.history(t)
	summarizing := deferred()
	chat.faux.pushSummary(func(request scriptRequest) (ai.FauxResponse, error) {
		summarizing.resolve()
		return ai.FauxResponse{}, abortedBy(request.options.Signal)
	})
	id := chat.compact(t, nil)
	<-summarizing.done
	crash(t, chat.harness)

	chat.faux.pushSummary(scriptSummary())
	storage.Reopen()
	reopened, root := openChat(t, storage, chat.setup)
	reopened.Resume()
	record := must(reopened.WaitForTask(testContext, id))
	if record.State.Outcome == nil || record.State.Outcome.Status != durable.OutcomeCompleted {
		t.Fatalf("the compaction ended %+v, want completed", record.State.Outcome)
	}
	waitFor(t, func() bool { return slices.Contains(entryKinds(allEntries(t, root)), "pi.compaction") })
	if live := liveOf(t, reopened, root.Id()); live == nil || !reflect.DeepEqual(*live, LiveState{}) {
		t.Fatalf("pi.live = %+v, want empty", live)
	}
	must0(t, reopened.Close(testContext))
}

func TestAHeldDeferredPollIsResumedAfterACrash(t *testing.T) {
	storage := newControlledStorage()
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
	run := openProgress(t, storage, setup)
	setup.SetSettings(func(settings *HarnessSettings) {
		settings.Stream = &durable.ConversationStreamOptions{Deferred: &ai.DeferredOption{Enabled: true}}
	})
	id := submitInput(t, run.root, "hi").Id()
	waitForLong(t, func() bool {
		live := storedLive(t, storage, run.root.Id())
		return live != nil && live.Generation != nil && live.Generation.Deferred != nil
	})
	crash(t, run.harness)
	reopened := openProgress(t, storage, setup)
	mu.Lock()
	now = 61_000
	mu.Unlock()
	reopened.harness.Resume()
	settled := settleSubmission(t, reopened, id)
	if settled.Status != durable.SubmissionDone || settled.Type != durable.SubmissionTypeInput {
		t.Fatalf("Unexpected %s", settled.Status)
	}
	answer := must(durable.Commit(testContext, reopened.root, func(tx durable.Tx) (*durable.TypedEntry[durable.Never], error) {
		return durable.TxEntry(tx, durable.AssistantEntry, *settled.Answer)
	}))
	if got, _ := textOf(answer.Model[0]); got != "deferred answer" {
		t.Fatalf("answer = %q", got)
	}
	if fetches := setup.Faux.(interface{ DeferredFetchCount() int }).DeferredFetchCount(); fetches != 1 {
		t.Fatalf("deferred fetches = %d, want 1", fetches)
	}
	must0(t, reopened.harness.Close(testContext))
}
