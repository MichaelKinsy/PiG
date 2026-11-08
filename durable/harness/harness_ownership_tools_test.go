// Ports packages/durable/test/harness-ownership.test.ts: the "owned conversations from tools and supervisors" cases.
// See harness_tasks_test.go for the Go mappings that apply to every case.

package harness

// pi: packages/durable/src/harness/submissions.ts

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// ownTool builds an executable tool with an object schema.
func ownTool(name, description string, parameters map[string]any, execute func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error), edit ...func(*durable.ToolRegistration)) *durable.ToolRegistration {
	tool := DefineTool(durable.ToolRegistration{
		Name: name, Description: description, Parameters: parameters,
		Execute: execute,
	})
	for _, apply := range edit {
		apply(tool)
	}
	return tool
}

func ownText(text string) durable.ToolExecutionResult {
	return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}}
}

// ownCall is a tool-calling faux answer with one call.
func ownCall(name string, args map[string]any, id string) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall(name, args, &ai.FauxToolCallOptions{ID: id})}, StopReason: "toolUse"})
}

// ownGatedStep is a faux response held until release or cancellation; reached resolves when the request is sent.
type ownGatedStep struct {
	step    ai.FauxResponseStep
	reached *deferredGate
	gate    *deferredGate
}

func ownGated(text string) ownGatedStep {
	held := ownGatedStep{reached: deferred(), gate: deferred()}
	held.step = ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
		held.reached.resolve()
		if err := held.gate.wait(options.Signal); err != nil {
			return ai.FauxResponse{}.AssistantMessage(), err
		}
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}}.AssistantMessage(), nil
	})
	return held
}

var fauxModel = AgentChange{Model: SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}

// ownCreateChild creates a conversation owned by the calling tool task, configured with the faux model.
func ownCreateChild(ctx context.Context, api durable.ToolExecutionApi) (durable.ConversationId, error) {
	value, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
		created, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: api.TaskId()}})
		if err != nil {
			return nil, err
		}
		return created.Id, Configure(tx, created.Id, fauxModel)
	})
	if err != nil {
		return 0, err
	}
	return value.(durable.ConversationId), nil
}

func ownChildEntries(t *testing.T, harness Harness, id durable.ConversationId) []durable.EntryRecord {
	t.Helper()
	page, err := ownConversation(t, harness, id).Entries(testContext, durable.EntryQuery{}, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	return page.Items
}

func ownCountKind(entries []durable.EntryRecord, kind string) int {
	count := 0
	for _, entry := range entries {
		if entry.Kind == kind {
			count++
		}
	}
	return count
}

func ownSubmitAndWait(t *testing.T, conversation Conversation, text string) durable.SettledSubmissionRecord {
	t.Helper()
	settled, err := ownSubmit(t, conversation, ownInput(text)).Wait(testContext)
	if err != nil {
		t.Fatal(err)
	}
	return settled
}

// Pi TaskRuntime.conversation: packages/durable/src/types.ts:217.
func TestOwnedConversationsFromTools(t *testing.T) {
	t.Run("gives a tool an invocation-bound handle whose submissions stay durable after the call ends", func(t *testing.T) {
		setup := chatSetup(t)
		handle := &syncValue[durable.ConversationHandle]{}
		missing := &syncValue[durable.ConversationHandle]{}
		submission := &syncValue[durable.Submission]{}
		toolContext := &syncValue[context.Context]{}
		addTool(t, setup.Registry, ownTool("delegate", "Delegates", emptyObjectSchema(), func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			toolContext.put(ctx)
			child, err := ownCreateChild(ctx, api)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			absent, err := api.Conversation(ctx, 99_999)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			missing.put(absent)
			bound, err := api.Conversation(ctx, child)
			if err != nil || bound == nil {
				return durable.ToolExecutionResult{}, errors.New("child conversation is missing")
			}
			handle.put(bound)
			submitted, err := bound.Submit(ctx, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("child task"), RequestId: new("child")})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			submission.put(submitted)
			settled, err := submitted.Wait(ctx)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			return ownText(string(settled.Status)), nil
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{ownCall("delegate", map[string]any{}, "c1"), fauxAnswer("child answer"), fauxAnswer("done")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		ownSubmitAndWait(t, root, "go")
		if absent, _ := missing.get(); absent != nil {
			t.Fatalf("handle of an absent conversation is %v, want nil", absent)
		}
		// The call's invocation ends in the step after its phase, on its own goroutine, and the parent's turn goes on from the call's result
		// without waiting for it. The call's context is the invocation's, which the end cancels after it marks the invocation ended.
		callContext, _ := toolContext.get()
		<-callContext.Done()
		// The call ended: the handle and its submission reject, while the submission record remains.
		bound, _ := handle.get()
		_, submitErr := bound.Submit(testContext, ownInput("again"))
		tkContainsError(t, submitErr, "invocation has ended")
		tkContainsError(t, bound.WaitForIdle(testContext), "invocation has ended")
		tkContainsError(t, bound.Abort(testContext, nil), "invocation has ended")
		submitted, _ := submission.get()
		_, waitErr := submitted.Wait(testContext)
		tkContainsError(t, waitErr, "invocation has ended")
		// Nothing was admitted or marked after the call ended.
		if users := ownCountKind(ownChildEntries(t, harness, bound.Id()), "pi.user"); users != 1 {
			t.Fatalf("%d user entries in the child, want 1", users)
		}
		inspection, err := harness.Inspect(testContext)
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range inspection.Tasks {
			if task.Record.ConversationId == bound.Id() {
				t.Fatalf("live task %+v in the child", task.Record)
			}
		}
		if status := ownSubmissionStatus(t, harness, submitted.Id()).Status; status != durable.SubmissionDone {
			t.Fatalf("child submission is %s, want done", status)
		}
		mustClose(t, harness)
	})

	// harness-ownership.test.ts:1067 it("runs a handle operation queued behind the abort mark before the mark's publication,
	// and the cascade aborts it") (Pi e5c6182b)
	t.Run("runs a handle operation queued behind the abort mark before the mark's publication, and the cascade aborts it", func(t *testing.T) {
		setup := chatSetup(t)
		type ready struct {
			child  durable.ConversationId
			taskId durable.TaskId
		}
		type submitted struct {
			submission durable.Submission
			err        error
		}
		readyValue := &syncValue[ready]{}
		goSubmit := deferred()
		submitting := make(chan submitted, 1)
		submitQueued := deferred()
		addTool(t, setup.Registry, ownTool("delegate", "Delegates late", emptyObjectSchema(), func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			value, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
				created, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: api.TaskId()}})
				return created.Id, err
			})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			child := value.(durable.ConversationId)
			bound, err := api.Conversation(ctx, child)
			if err != nil || bound == nil {
				return durable.ToolExecutionResult{}, errors.New("child conversation is missing")
			}
			readyValue.put(ready{child: child, taskId: api.TaskId()})
			if err := goSubmit.wait(testContext); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			// Called while the invocation is alive, with a context that is not the call's: the handle binds it to the
			// invocation. It reaches the line only after the abort mark.
			go func() {
				submission, submitErr := bound.Submit(testContext, ownInput("late"))
				submitting <- submitted{submission, submitErr}
			}()
			submitQueued.resolve()
			return durable.ToolExecutionResult{}, abortedBy(ctx)
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{ownCall("delegate", map[string]any{}, "c1")})
		// Holds the storage call that writes the abort mark, so the mark is published only once the test lets it.
		store := &holdMarkStorage{MemoryStorage: storage.NewMemoryStorage(), entered: deferred(), stored: deferred()}
		harness, root := openChat(t, store, setup)
		ownSubmit(t, root, ownInput("go"))
		eventually(t, readyValue.isSet)
		info, _ := readyValue.get()
		store.hold(info.taskId)
		// Hold the Session line, queue the abort mark behind it, then let the tool queue its submit.
		entered, release := deferred(), deferred()
		holding := goCall(func() error {
			_, err := root.Commit(testContext, func(durable.Tx) (any, error) {
				entered.resolve()
				return nil, release.wait(testContext)
			})
			return err
		})
		if err := entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		line := harness.(*harnessImpl).SessionImpl
		queued := line.LineJobs()
		aborting := goCall(func() error {
			_, err := harness.AbortTask(testContext, info.taskId)
			return err
		})
		// Upstream's abortTask and submit reach the line in call order; wait for each to queue behind the held commit.
		eventually(t, func() bool { return line.LineJobs() > queued })
		queued = line.LineJobs()
		goSubmit.resolve()
		if err := submitQueued.wait(testContext); err != nil {
			t.Fatal(err)
		}
		eventually(t, func() bool { return line.LineJobs() > queued })
		release.resolve()
		if err := holding.wait(t); err != nil {
			t.Fatal(err)
		}
		// The submit starts while the mark's storage call is in flight: once the call is held, the submit's job waits for the
		// mark's publication, which it does only after its start check passed. Nothing else takes the task's gate meanwhile.
		if err := store.entered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		eventually(t, func() bool { return line.GateTouches(session.TaskKey(info.taskId)) > 0 })
		store.stored.resolve()
		if err := aborting.wait(t); err != nil {
			t.Fatal(err)
		}
		// Cancellation is checked when a job starts: the submit's job started, and passed its check, before the mark is
		// published, so it lands. The mark's cascade then aborts the run it started in the owned conversation.
		var late submitted
		select {
		case late = <-submitting:
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for the late submit")
		}
		if late.err != nil {
			t.Fatalf("the late submit failed: %v", late.err)
		}
		waitForIdle(t, ownConversation(t, harness, info.child))
		record := ownSubmissionStatus(t, harness, late.submission.Id())
		if record.Status != durable.SubmissionUnanswered || record.Reason == nil || *record.Reason != "aborted" {
			t.Fatalf("submission %+v, want unanswered, aborted", record)
		}
		kinds := []string{}
		for _, entry := range ownChildEntries(t, harness, info.child) {
			kinds = append(kinds, entry.Kind)
		}
		if !reflect.DeepEqual(kinds, []string{"pi.user"}) {
			t.Fatalf("child entries %v, want [pi.user]", kinds)
		}
		inspection, err := harness.Inspect(testContext)
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range inspection.Submissions {
			if record.ConversationId == info.child {
				t.Fatalf("submission %+v still open in the child", record)
			}
		}
		mustClose(t, harness)
	})

	// Go-only: not a case of harness-ownership.test.ts at v1.5.0. It keeps the expectations of the v1.4.0 case `rejects a handle
	// operation queued on the line when the invocation ends before it runs`, which Pi replaced by the case above, as the other
	// order of the same race: the mark is published while an earlier job holds the line, so the invocation is aborted before the
	// submit's job starts and session.ts commit's `context.abortSignal?.throwIfAborted()` rejects it (Pi e5c6182b, harness.ts submit).
	t.Run("rejects a handle submit whose invocation was aborted while it was queued behind a line job, and stores nothing", func(t *testing.T) {
		setup := chatSetup(t)
		type ready struct {
			child  durable.ConversationId
			taskId durable.TaskId
			ctx    context.Context
			bound  durable.ConversationHandle
		}
		readyValue := &syncValue[ready]{}
		addTool(t, setup.Registry, ownTool("delegate", "Delegates late", emptyObjectSchema(), func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			child, err := ownCreateChild(ctx, api)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			bound, err := api.Conversation(ctx, child)
			if err != nil || bound == nil {
				return durable.ToolExecutionResult{}, errors.New("child conversation is missing")
			}
			readyValue.put(ready{child: child, taskId: api.TaskId(), ctx: ctx, bound: bound})
			return durable.ToolExecutionResult{}, abortedBy(ctx)
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{ownCall("delegate", map[string]any{}, "c1")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		ownSubmit(t, root, ownInput("go"))
		eventually(t, readyValue.isSet)
		info, _ := readyValue.get()
		// The line holds two jobs that outlast the abort mark's publication: the first, the mark behind it, then the second,
		// with the handle's submit behind that. The mark publishes while the second job holds the line.
		hold := func(entered, release *deferredGate) *future[struct{}] {
			var holding *future[struct{}]
			queueOnLineAdmitted(t, func(admitted context.Context) <-chan struct{} {
				holding = asyncErr(func() error {
					_, err := root.Commit(admitted, func(durable.Tx) (any, error) {
						entered.resolve()
						return nil, release.wait(testContext)
					})
					return err
				})
				return holding.done
			})
			return holding
		}
		firstEntered, firstRelease := deferred(), deferred()
		secondEntered, secondRelease := deferred(), deferred()
		first := hold(firstEntered, firstRelease)
		if err := firstEntered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		var aborting *future[struct{}]
		queueOnLineAdmitted(t, func(admitted context.Context) <-chan struct{} {
			aborting = asyncErr(func() error {
				_, err := harness.AbortTask(admitted, info.taskId)
				return err
			})
			return aborting.done
		})
		second := hold(secondEntered, secondRelease)
		var submitting *future[durable.Submission]
		queueOnLineAdmitted(t, func(admitted context.Context) <-chan struct{} {
			submitting = async(func() (durable.Submission, error) { return info.bound.Submit(admitted, ownInput("late")) })
			return submitting.done
		})
		firstRelease.resolve()
		if err := secondEntered.wait(testContext); err != nil {
			t.Fatal(err)
		}
		// The mark was published while the second job runs: the invocation is aborted before the submit's job starts.
		<-info.ctx.Done()
		secondRelease.resolve()
		for _, done := range []*future[struct{}]{first, aborting, second} {
			if _, err := done.wait(); err != nil {
				t.Fatal(err)
			}
		}
		if submission, err := submitting.wait(); err == nil {
			t.Fatalf("the submit queued before the invocation was aborted was admitted as %v, want it rejected when its job started", submission.Id())
		}
		if users := ownCountKind(ownChildEntries(t, harness, info.child), "pi.user"); users != 0 {
			t.Fatalf("%d user entries in the child, want 0", users)
		}
		inspection, err := harness.Inspect(testContext)
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range inspection.Submissions {
			if record.ConversationId == info.child {
				t.Fatalf("submission %+v stored in the child", record)
			}
		}
		mustClose(t, harness)
	})

	t.Run("aborts a subagent run with its parent's conversation, rejecting the waiting tool", func(t *testing.T) {
		setup := chatSetup(t)
		child := &syncValue[durable.ConversationId]{}
		toolWait := make(chan string, 1)
		addTool(t, setup.Registry, ownTool("delegate", "Delegates", emptyObjectSchema(), func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			created, err := ownCreateChild(ctx, api)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			child.put(created)
			bound, err := api.Conversation(ctx, created)
			if err != nil || bound == nil {
				return durable.ToolExecutionResult{}, errors.New("child conversation is missing")
			}
			submitted, err := bound.Submit(ctx, ownInput("child task"))
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			settled, err := submitted.Wait(ctx)
			if err != nil {
				toolWait <- err.Error()
				return durable.ToolExecutionResult{}, err
			}
			return ownText(string(settled.Status)), nil
		}))
		childRun := ownGated("never")
		setup.Faux.SetResponses([]ai.FauxResponseStep{ownCall("delegate", map[string]any{}, "c1"), childRun.step})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		input := ownSubmit(t, root, ownInput("go"))
		if err := childRun.reached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		if err := root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		settled, err := input.Wait(testContext)
		if err != nil || settled.Status != durable.SubmissionUnanswered || settled.Reason == nil || *settled.Reason != "aborted" {
			t.Fatalf("input %+v %v, want unanswered: aborted", settled, err)
		}
		if message := <-toolWait; message == "" {
			t.Fatal("the waiting tool saw an empty error")
		}
		childId, _ := child.get()
		if err := ownConversation(t, harness, childId).WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		expectOnlyRunFailure(t, harness, childId, RunAborted)
		inspection, err := harness.Inspect(testContext)
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range inspection.Submissions {
			if record.ConversationId == childId {
				t.Fatalf("child submission %+v left live", record)
			}
		}
		if err := harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		mustClose(t, harness)
	})

	t.Run("lets a running tool abort its owned child and continue", func(t *testing.T) {
		setup := chatSetup(t)
		childRun := ownGated("never")
		addTool(t, setup.Registry, ownTool("delegate", "Delegates and cancels", emptyObjectSchema(), func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			child, err := ownCreateChild(ctx, api)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			bound, err := api.Conversation(ctx, child)
			if err != nil || bound == nil {
				return durable.ToolExecutionResult{}, errors.New("child conversation is missing")
			}
			submitted, err := bound.Submit(ctx, ownInput("child task"))
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if err := childRun.reached.wait(ctx); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if err := bound.Abort(ctx, nil); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			settledChild, err := submitted.Wait(ctx)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			return ownText("child " + string(settledChild.Status)), nil
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{ownCall("delegate", map[string]any{}, "c1"), childRun.step, fauxAnswer("done")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		if status := ownSubmitAndWait(t, root, "go").Status; status != durable.SubmissionDone {
			t.Fatalf("input %s, want done", status)
		}
		mustClose(t, harness)
	})

	t.Run("aborts the children of a tool call that throws or is interrupted, while the run continues", func(t *testing.T) {
		path := sqlitePath(t)
		fixtures := newOwnFixtures()
		children := &syncList[durable.TaskId]{}
		toolRunning := deferred()
		setup := chatSetup(t)
		addTask(t, setup.Registry, fixtures.hold.Erased())
		addTool(t, setup.Registry, ownTool("spawn", "Starts owned work, then throws or hangs",
			map[string]any{"type": "object", "properties": map[string]any{"mode": map[string]any{"type": "string"}}, "required": []any{"mode"}},
			func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				value, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
					created, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: api.TaskId()}})
					if err != nil {
						return nil, err
					}
					return durable.CreateTask(tx, fixtures.hold, holdInput{Name: "child." + api.CallId()}, durable.TaskOptions{Ownership: conversationOwned, ConversationId: &created.Id})
				})
				if err != nil {
					return durable.ToolExecutionResult{}, err
				}
				children.add(value.(durable.TaskId))
				if args.(map[string]any)["mode"] == "throw" {
					return durable.ToolExecutionResult{}, errors.New("spawn failed")
				}
				toolRunning.resolve()
				return durable.ToolExecutionResult{}, abortedBy(ctx)
			}))
		call := func(mode, id string) ai.FauxResponseStep { return ownCall("spawn", map[string]any{"mode": mode}, id) }
		setup.Faux.SetResponses([]ai.FauxResponseStep{call("throw", "c1"), fauxAnswer("after throw"), call("hang", "c2")})
		harness, root := openChat(t, tkOpenSqlite(t, path), setup)
		if status := ownSubmitAndWait(t, root, "one").Status; status != durable.SubmissionDone {
			t.Fatalf("first input %s, want done", status)
		}
		ownExpectOutcome(t, harness, children.all()[0], durable.OutcomeAborted)

		// The second call hangs until the process stops; on reopen it is interrupted.
		second := ownSubmit(t, root, ownInput("two")).Id()
		if err := toolRunning.wait(testContext); err != nil {
			t.Fatal(err)
		}
		mustClose(t, harness)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("after interrupt")})
		harness, _ = openChat(t, tkOpenSqlite(t, path), setup)
		harness.Resume()
		ownExpectOutcome(t, harness, children.all()[1], durable.OutcomeAborted)
		submission, err := harness.Submission(testContext, second)
		if err != nil || submission == nil {
			t.Fatalf("Submission %v %v", submission, err)
		}
		if settled, err := submission.Wait(testContext); err != nil || settled.Status != durable.SubmissionDone {
			t.Fatalf("second input %+v %v, want done", settled, err)
		}
		kind := "pi.tool"
		page, err := durable.Commit(testContext, harness, func(tx durable.Tx) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
			return tx.ScanTasks(durable.TaskQuery{Kind: &kind}, 10, nil)
		})
		if err != nil {
			t.Fatal(err)
		}
		statuses := []durable.TaskOutcomeStatus{}
		for _, task := range page.Items {
			if task.State.Status == durable.TaskTerminal {
				statuses = append(statuses, task.State.Outcome.Status)
			}
		}
		if !reflect.DeepEqual(statuses, []durable.TaskOutcomeStatus{durable.OutcomeFailed, durable.OutcomeFailed}) {
			t.Fatalf("tool task outcomes %v, want [failed failed]", statuses)
		}
		mustClose(t, harness)
	})

	t.Run("reruns a replay-safe subagent tool after a restart with the same child and submission", func(t *testing.T) {
		path := sqlitePath(t)
		children := &syncList[durable.ConversationId]{}
		setup := chatSetup(t)
		addTool(t, setup.Registry, ownTool("subagent", "Delegates", emptyObjectSchema(), func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			value, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
				owner := api.TaskId()
				existing, err := tx.ScanConversations(durable.ConversationQuery{OwnerTaskId: &owner}, 1, nil)
				if err != nil {
					return nil, err
				}
				if len(existing.Items) > 0 {
					return existing.Items[0].Id, nil
				}
				created, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: owner}})
				if err != nil {
					return nil, err
				}
				return created.Id, Configure(tx, created.Id, fauxModel)
			})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			child := value.(durable.ConversationId)
			children.add(child)
			bound, err := api.Conversation(ctx, child)
			if err != nil || bound == nil {
				return durable.ToolExecutionResult{}, errors.New("child conversation is missing")
			}
			request := "subagent:" + strconv.FormatInt(int64(api.TaskId()), 10)
			submitted, err := bound.Submit(ctx, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("child task"), RequestId: &request})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			settled, err := submitted.Wait(ctx)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			return ownText(string(settled.Status)), nil
		}, func(tool *durable.ToolRegistration) { tool.Replay = durable.ReplaySafe }))
		childRun := ownGated("never")
		setup.Faux.SetResponses([]ai.FauxResponseStep{ownCall("subagent", map[string]any{}, "c1"), childRun.step})
		harness, root := openChat(t, tkOpenSqlite(t, path), setup)
		input := ownSubmit(t, root, ownInput("go")).Id()
		if err := childRun.reached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		mustClose(t, harness)

		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("child answer"), fauxAnswer("done")})
		harness, root = openChat(t, tkOpenSqlite(t, path), setup)
		submission, err := harness.Submission(testContext, input)
		if err != nil || submission == nil {
			t.Fatalf("Submission %v %v", submission, err)
		}
		if settled, err := submission.Wait(testContext); err != nil || settled.Status != durable.SubmissionDone {
			t.Fatalf("input %+v %v, want done", settled, err)
		}
		seen := children.all()
		if len(seen) != 2 || seen[1] != seen[0] {
			t.Fatalf("children %v, want the same child twice", seen)
		}
		if users := ownCountKind(ownChildEntries(t, harness, seen[0]), "pi.user"); users != 1 {
			t.Fatalf("%d user entries in the child, want 1", users)
		}
		view, err := root.Context(testContext, nil)
		if err != nil {
			t.Fatal(err)
		}
		results := []bool{}
		for _, message := range view.Messages {
			if result, ok := message.(ai.ToolResultMessage); ok {
				results = append(results, result.IsError)
			}
		}
		if !reflect.DeepEqual(results, []bool{false}) {
			t.Fatalf("tool results %v, want [false]", results)
		}
		mustClose(t, harness)
	})

	t.Run("lets a background supervisor resubmit after a restart without submitting twice", func(t *testing.T) {
		path := sqlitePath(t)
		type childrenState struct {
			Child *durable.ConversationId `json:"child,omitempty"`
		}
		children := durable.DefineDoc(durable.DocDefinition[childrenState]{
			Kind: "test.children", Version: 1, Initial: func() childrenState { return childrenState{} }, CheckpointWhen: func(childrenState, []durable.Op, durable.CheckpointInfo) bool { return false },
			Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: durable.ForkInitial,
		})
		type supervisorInput struct {
			Parent durable.ConversationId `json:"parent"`
		}
		type supervisorResult struct {
			Answer durable.EntryId `json:"answer"`
		}
		type supervisorRuntime = durable.TaskRuntime[supervisorInput, stepState, supervisorResult, any]
		type supervisorRecord = durable.RunningTask[supervisorInput, stepState, supervisorResult]
		supervisor := durable.DefineTask(durable.TaskDefinition[supervisorInput, stepState, supervisorResult, any]{
			Name:    "test.supervisor",
			Version: 1,
			Initial: func(supervisorInput) stepState { return stepState{Phase: "run"} },
			Phases: map[string]durable.PhaseHandler[supervisorInput, stepState, supervisorResult, any]{
				"run": func(ctx context.Context, task supervisorRecord, runtime supervisorRuntime) error {
					state, err := durable.Snapshot[childrenState](ctx, runtime, children, task.Input.Parent)
					if err != nil || state == nil || state.Child == nil {
						return errors.New("the children document has no child")
					}
					child, err := runtime.Conversation(ctx, *state.Child)
					if err != nil || child == nil {
						return errors.New("child conversation is missing")
					}
					stable := "stable"
					submitted, err := child.Submit(ctx, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("work"), RequestId: &stable})
					if err != nil {
						return err
					}
					settled, err := submitted.Wait(ctx)
					if err != nil {
						return err
					}
					if settled.Status != durable.SubmissionDone || settled.Type != durable.SubmissionTypeInput {
						return errors.New(string(settled.Status))
					}
					return runtime.Commit(ctx, func(durable.Tx, supervisorRecord) (*durable.NextTaskState[stepState, supervisorResult], error) {
						return completed[stepState, supervisorResult](supervisorResult{Answer: *settled.Answer}), nil
					})
				},
			},
			Abort: func(ctx context.Context, _ supervisorRecord, runtime supervisorRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, supervisorRecord) (*durable.NextTaskState[stepState, supervisorResult], error) {
					return &durable.NextTaskState[stepState, supervisorResult]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[supervisorResult]{Status: durable.OutcomeAborted}}, nil
				})
			},
		})
		setup := chatSetup(t)
		addTask(t, setup.Registry, supervisor.Erased())
		first := ownGated("never")
		setup.Faux.SetResponses([]ai.FauxResponseStep{first.step, fauxAnswer("answer")})
		harness, root := openChat(t, tkOpenSqlite(t, path), setup)
		type created struct {
			supervisor durable.TaskId
			child      durable.ConversationId
		}
		made, err := durable.Commit(testContext, root, func(tx durable.Tx) (created, error) {
			supervisorId, err := durable.CreateTask(tx, supervisor, supervisorInput{Parent: root.Id()}, durable.TaskOptions{Ownership: conversationOwned, Background: true})
			if err != nil {
				return created{}, err
			}
			child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: supervisorId}})
			if err != nil {
				return created{}, err
			}
			if err := Configure(tx, child.Id, fauxModel); err != nil {
				return created{}, err
			}
			draft, err := durable.TxDoc[childrenState](tx, children, root.Id())
			if err != nil {
				return created{}, err
			}
			return created{supervisor: supervisorId, child: child.Id}, draft.Set("child", float64(child.Id))
		})
		if err != nil {
			t.Fatal(err)
		}
		harness.Resume()
		// The submission is admitted and its generation requested; then the process stops.
		if err := first.reached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		mustClose(t, harness)

		harness, root = openChat(t, tkOpenSqlite(t, path), setup)
		harness.Resume()
		if outcome := tkWaitOutcome(t, harness, made.supervisor); outcome.Status != durable.OutcomeCompleted {
			t.Fatalf("supervisor %s, want completed", describeOutcome(outcome))
		}
		if users := ownCountKind(ownChildEntries(t, harness, made.child), "pi.user"); users != 1 {
			t.Fatalf("%d user entries in the child, want 1", users)
		}
		// The root never waited for the background supervisor.
		if err := root.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		mustClose(t, harness)
	})
}

// holdMarkStorage holds the storage call that writes the abort mark of one task until stored resolves; entered resolves
// once the call is held.
type holdMarkStorage struct {
	*storage.MemoryStorage
	mu      sync.Mutex
	target  durable.TaskId
	entered *deferredGate
	stored  *deferredGate
}

func (store *holdMarkStorage) hold(id durable.TaskId) {
	store.mu.Lock()
	store.target = id
	store.mu.Unlock()
}

func (store *holdMarkStorage) Commit(ctx context.Context, batches [][]durable.StorageWrite, receive func(index int, seq durable.Seq)) (durable.CommitResult, error) {
	store.mu.Lock()
	target := store.target
	marks := target != 0 && slices.ContainsFunc(slices.Concat(batches...), func(write durable.StorageWrite) bool {
		task, ok := write.(durable.TaskWrite)
		return ok && task.Value.Id == target && task.Value.AbortRequested
	})
	if marks {
		store.target = 0
	}
	store.mu.Unlock()
	if marks {
		store.entered.resolve()
		_ = store.stored.wait(testContext)
	}
	return store.MemoryStorage.Commit(ctx, batches, receive)
}
