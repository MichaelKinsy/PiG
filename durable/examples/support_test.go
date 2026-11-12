// Support for the ported examples of packages/durable/test/examples. Upstream's examples are scripts run by hand with
// `node --experimental-strip-types`; each Go example is a test that runs the same flow and asserts what the script
// prints.

package examples_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// background is BACKGROUND_CONTEXT: never cancelled.
var background = context.Background()

type notes struct {
	Text string `json:"text"`
}

// notesDoc is the conversation-scoped, rewindable `example.notes` document of examples 01 to 04.
var notesDoc = durable.DefineDoc(durable.DocDefinition[notes]{
	Kind: "example.notes", Version: 1, Initial: func() notes { return notes{} }, CheckpointWhen: func(notes, []durable.Op, durable.CheckpointInfo) bool { return false },
	Scope: durable.ScopeConversation, History: durable.HistoryRewindable, Fork: durable.ForkAsOf,
})

var ownerless = durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}}

func newSession(t *testing.T) *session.SessionImpl {
	t.Helper()
	return session.CreateSession(storage.NewMemoryStorage())
}

func closeSession(t *testing.T, committer interface{ Close(context.Context) error }) {
	t.Helper()
	if err := committer.Close(background); err != nil {
		t.Fatal(err)
	}
}

func commit[T any](t *testing.T, committer durable.Committer, change func(tx durable.Tx) (T, error)) T {
	t.Helper()
	value, err := durable.Commit(background, committer, change)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// setNotes is `(await tx.doc(Notes, id)).text = text`.
func setNotes(tx durable.Tx, id durable.ConversationId, text string) error {
	draft, err := durable.TxDoc[notes](tx, notesDoc, id)
	if err != nil {
		return err
	}
	return draft.Set("text", text)
}

func appendNote(tx durable.Tx, id durable.ConversationId, data string) (durable.EntryRecord, error) {
	return tx.AppendEntry(id, durable.EntryDraft{Kind: "note", Data: data})
}

func snapshotNotes(t *testing.T, session durable.DocumentReader, id durable.ConversationId) string {
	t.Helper()
	snapshot, err := durable.Snapshot[notes](background, session, notesDoc, id)
	if err != nil || snapshot == nil {
		t.Fatalf("notes snapshot: %v %v", snapshot, err)
	}
	return snapshot.Text
}

// eventually polls until check holds, as the flush loops of the upstream test support do.
func eventually(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatal("condition was not reached")
		}
		time.Sleep(time.Millisecond)
	}
}

// The suspendable-work examples (38 to 46) read what the upstream scripts print: each Go example keeps the lines its
// script prints in a printed, from whichever goroutine prints them, and asserts them at the end.

// printed collects the lines of a script's console.log, in the order they are printed.
type printed struct {
	mu    sync.Mutex
	lines []string
}

func (output *printed) add(line string) {
	output.mu.Lock()
	defer output.mu.Unlock()
	output.lines = append(output.lines, line)
}

func (output *printed) all() []string {
	output.mu.Lock()
	defer output.mu.Unlock()
	return append([]string{}, output.lines...)
}

// jsonText is JSON.stringify of a value.
func jsonText(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// outcomeText is the terminal outcome of a task as JSON, the way a script prints `task.state.outcome`.
func outcomeText(task durable.SettledTask[durable.JsonValue]) string {
	return jsonText(task.State.Outcome)
}

// signalStateText is JSON.stringify of a signal key's state: `{"value":...}` once delivered, `{"expired":true}` once the
// deadline came first, and "undefined" (what a template prints) while nothing came.
func signalStateText[T any](state *durable.SignalState[T]) string {
	switch {
	case state == nil:
		return "undefined"
	case state.Expired:
		return `{"expired":true}`
	default:
		return jsonText(map[string]any{"value": state.Value})
	}
}

// waitSleeping returns once the task is stored sleeping.
func waitSleeping(t *testing.T, opened harness.Harness, id durable.TaskId) {
	t.Helper()
	eventually(t, func() bool {
		task := must(opened.GetTask(background, id))
		return task != nil && task.State.Status == durable.TaskSleeping
	})
}

// settledWake is Harness.Wake once no task is running any more. The upstream scripts print it after a pause of a few
// milliseconds; a goroutine has no event loop turn to stand for "the invocation that parked has ended", so this polls
// until Wake is inactive (a wake that never settles is returned as it is after ten seconds, for the assertion to show).
func settledWake(opened harness.Harness) harness.Wake {
	deadline := time.Now().Add(10 * time.Second)
	for {
		wake := opened.Wake()
		if !wake.Active || time.Now().After(deadline) {
			return wake
		}
		time.Sleep(time.Millisecond)
	}
}

// partsText joins the text parts of a tool result, as `content.map(part => part.type === "text" ? part.text : "").join("")`.
func partsText(parts []ai.ToolResultMessageContent) string {
	var text strings.Builder
	for _, part := range parts {
		if part, ok := part.(ai.TextContent); ok {
			text.WriteString(part.Text)
		}
	}
	return text.String()
}

// newestToolResult is the text of the newest tool result among the conversation's latest `limit` entries.
func newestToolResult(t *testing.T, conversation harness.Conversation, limit int) string {
	t.Helper()
	page := must(conversation.Entries(background, durable.EntryQuery{}, limit, nil))
	for _, entry := range page.Items {
		if durable.ToolResultEntry.Is(&entry) {
			return partsText(entry.Model[0].(ai.ToolResultMessage).Content)
		}
	}
	t.Fatal("no tool result")
	return ""
}

// newestToolResultMessage is the newest tool result message among the conversation's latest `limit` entries.
func newestToolResultMessage(t *testing.T, conversation harness.Conversation, limit int) ai.ToolResultMessage {
	t.Helper()
	page := must(conversation.Entries(background, durable.EntryQuery{}, limit, nil))
	for _, entry := range page.Items {
		if durable.ToolResultEntry.Is(&entry) {
			return entry.Model[0].(ai.ToolResultMessage)
		}
	}
	t.Fatal("no tool result")
	return ai.ToolResultMessage{}
}

// liveOf is the conversation's pi.live document.
func liveOf(t *testing.T, reader durable.DocumentReader, conversation harness.Conversation) harness.LiveState {
	t.Helper()
	live := must(durable.Snapshot(background, reader, harness.LiveDoc, conversation.Id()))
	if live == nil {
		return harness.LiveState{}
	}
	return *live
}

// wakeText is a Wake as the scripts print it: `{"active":false}` or `{"active":false,"at":1762678800000}`.
func wakeText(wake harness.Wake) string {
	if wake.At == nil {
		return jsonText(map[string]any{"active": wake.Active})
	}
	return jsonText(struct {
		Active bool    `json:"active"`
		At     float64 `json:"at"`
	}{wake.Active, *wake.At})
}

// checkpointOf is `api.checkpoint` of a tool call that parked, decoded as T; T's zero value for a fresh call.
func checkpointOf[T any](api durable.ToolExecutionApi) (T, error) {
	var checkpoint T
	value, parked := api.Checkpoint()
	if !parked {
		return checkpoint, nil
	}
	return durable.FromJsonValue[T](value)
}

// The waits of the suspendable-work examples are bounded: a call that never wakes ends the test with the wait failing, not
// with the test binary's timeout.
const parkedWaitLimit = 30 * time.Second

// waitTask is Harness.WaitForTask, failing the test after parkedWaitLimit.
func waitTask(t *testing.T, opened harness.Harness, id durable.TaskId) durable.SettledTask[durable.JsonValue] {
	t.Helper()
	ctx, cancel := context.WithTimeout(background, parkedWaitLimit)
	defer cancel()
	task, err := opened.WaitForTask(ctx, id)
	if err != nil {
		t.Fatalf("waiting for task %d: %v", id, err)
	}
	return task
}

// waitSubmission is Submission.Wait, failing the test after parkedWaitLimit.
func waitSubmission(t *testing.T, submission durable.Submission) durable.SettledSubmissionRecord {
	t.Helper()
	ctx, cancel := context.WithTimeout(background, parkedWaitLimit)
	defer cancel()
	settled, err := submission.Wait(ctx)
	if err != nil {
		t.Fatalf("waiting for submission %d: %v", submission.Id(), err)
	}
	return settled
}

// waitIdle is Conversation.WaitForIdle, failing the test after parkedWaitLimit.
func waitIdle(t *testing.T, conversation harness.Conversation) {
	t.Helper()
	ctx, cancel := context.WithTimeout(background, parkedWaitLimit)
	defer cancel()
	if err := conversation.WaitForIdle(ctx); err != nil {
		t.Fatalf("waiting for the conversation to be idle: %v", err)
	}
}
