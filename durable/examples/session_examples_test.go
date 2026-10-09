// Ports packages/durable/test/examples/00-conversation.ts through 05-watches.ts.

package examples_test

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/durable"
)

// 00-conversation.ts: a Session stores conversations, transcript entries, tasks, and documents.
func TestExample00Conversation(t *testing.T) {
	session := newSession(t)
	// "ownerless" means no task created this conversation.
	standalone := commit(t, session, func(tx durable.Tx) (durable.ConversationRecord, error) {
		return tx.CreateConversation(ownerless)
	})
	if standalone.Id == 0 || standalone.Owner != nil || standalone.Parent != nil {
		t.Fatalf("standalone conversation: %+v", standalone)
	}
	closeSession(t, session)
}

// 01-documents.ts: a rewindable conversation document keeps one value per entry.
func TestExample01Documents(t *testing.T) {
	session := newSession(t)
	chat := commit(t, session, func(tx durable.Tx) (durable.ConversationRecord, error) { return tx.CreateConversation(ownerless) })
	firstEntry := commit(t, session, func(tx durable.Tx) (durable.EntryRecord, error) {
		entry, err := appendNote(tx, chat.Id, "hello")
		if err != nil {
			return entry, err
		}
		return entry, setNotes(tx, chat.Id, "after hello")
	})
	secondEntry := commit(t, session, func(tx durable.Tx) (durable.EntryRecord, error) {
		entry, err := appendNote(tx, chat.Id, "goodbye")
		if err != nil {
			return entry, err
		}
		return entry, setNotes(tx, chat.Id, "after goodbye")
	})

	if got := snapshotNotes(t, session, chat.Id); got != "after goodbye" {
		t.Fatalf("latest notes: %q", got)
	}
	atFirst, err := durable.SnapshotAsOf[notes](background, session, notesDoc, firstEntry.Id, chat.Id)
	if err != nil || atFirst == nil || atFirst.Text != "after hello" {
		t.Fatalf("notes at first entry: %+v %v", atFirst, err)
	}
	atSecond, err := durable.SnapshotAsOf[notes](background, session, notesDoc, secondEntry.Id, chat.Id)
	if err != nil || atSecond == nil || atSecond.Text != "after goodbye" {
		t.Fatalf("notes at second entry: %+v %v", atSecond, err)
	}
	closeSession(t, session)
}

// 02-forks.ts: a fork starts with the value the notes had at the fork entry and diverges from its parent.
func TestExample02Forks(t *testing.T) {
	session := newSession(t)
	chat := commit(t, session, func(tx durable.Tx) (durable.ConversationRecord, error) { return tx.CreateConversation(ownerless) })
	firstEntry := commit(t, session, func(tx durable.Tx) (durable.EntryRecord, error) {
		entry, err := appendNote(tx, chat.Id, "hello")
		if err != nil {
			return entry, err
		}
		return entry, setNotes(tx, chat.Id, "after hello")
	})
	commit(t, session, func(tx durable.Tx) (struct{}, error) {
		if _, err := appendNote(tx, chat.Id, "goodbye"); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, setNotes(tx, chat.Id, "after goodbye")
	})

	branch := commit(t, session, func(tx durable.Tx) (durable.ConversationRecord, error) {
		return tx.ForkConversation(chat.Id, firstEntry.Id, ownerless)
	})
	branchEntries := commit(t, session, func(tx durable.Tx) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
		return tx.ScanEntries(durable.EntryQuery{ConversationId: branch.Id}, 10, nil)
	})
	var transcript []any
	for _, entry := range branchEntries.Items {
		transcript = append(transcript, entry.Data)
	}
	if !reflect.DeepEqual(transcript, []any{"hello"}) {
		t.Fatalf("fork transcript: %v", transcript)
	}
	if got := snapshotNotes(t, session, branch.Id); got != "after hello" {
		t.Fatalf("fork notes: %q", got)
	}

	commit(t, session, func(tx durable.Tx) (struct{}, error) {
		return struct{}{}, setNotes(tx, branch.Id, "changed only in the fork")
	})
	if got := snapshotNotes(t, session, branch.Id); got != "changed only in the fork" {
		t.Fatalf("fork notes after edit: %q", got)
	}
	if got := snapshotNotes(t, session, chat.Id); got != "after goodbye" {
		t.Fatalf("parent notes after edit: %q", got)
	}
	closeSession(t, session)
}

type agentRef struct {
	ConversationId durable.ConversationId `json:"conversationId"`
	RequestId      string                 `json:"requestId"`
}

type agentRegistry struct {
	Agents map[string]agentRef `json:"agents"`
}

type readyState struct {
	Phase string `json:"phase"`
}

// 03-owned-conversations.ts: a task owns a child conversation created in the same commit.
func TestExample03OwnedConversations(t *testing.T) {
	session := newSession(t)
	supervisor := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, readyState, durable.JsonValue, any]{
		Name:    "example.supervisor",
		Version: 1,
		Initial: func(durable.JsonValue) readyState { return readyState{Phase: "ready"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, readyState, durable.JsonValue, any]{
			"ready": func(context.Context, durable.RunningTask[durable.JsonValue, readyState, durable.JsonValue], durable.TaskRuntime[durable.JsonValue, readyState, durable.JsonValue, any]) error {
				return nil
			},
		},
		Abort: func(context.Context, durable.RunningTask[durable.JsonValue, readyState, durable.JsonValue], durable.TaskRuntime[durable.JsonValue, readyState, durable.JsonValue, any]) error {
			return nil
		},
	})
	registryDoc := durable.DefineDoc(durable.DocDefinition[agentRegistry]{
		CommonDocDefinition: durable.CommonDocDefinition[agentRegistry]{Kind: "example.agent-registry", Version: 1, Initial: func() agentRegistry { return agentRegistry{Agents: map[string]agentRef{}} }},
		DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: durable.ForkInitial},
	})

	main := commit(t, session, func(tx durable.Tx) (durable.ConversationRecord, error) { return tx.CreateConversation(ownerless) })
	type setupResult struct {
		supervisorId durable.TaskId
		child        durable.ConversationRecord
	}
	setup := commit(t, session, func(tx durable.Tx) (setupResult, error) {
		// background: true means the task is side work: waiting for the main conversation to finish does not wait
		// for it.
		supervisorId, err := durable.CreateTask(tx, supervisor, nil, durable.TaskOptions{
			Ownership:      durable.TaskOwnership{Kind: durable.TaskOwnedByConversation},
			ConversationId: &main.Id,
			Background:     true,
		})
		if err != nil {
			return setupResult{}, err
		}
		// The child records that it belongs to the supervisor task. The task was created a few lines above in this
		// same commit, which is allowed.
		child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: supervisorId}})
		if err != nil {
			return setupResult{}, err
		}
		draft, err := durable.TxDoc[agentRegistry](tx, registryDoc, main.Id)
		if err != nil {
			return setupResult{}, err
		}
		reference, err := durable.ToJsonValue(agentRef{ConversationId: child.Id, RequestId: fmt.Sprintf("researcher:first-message:%d", supervisorId)})
		if err != nil {
			return setupResult{}, err
		}
		return setupResult{supervisorId, child}, draft.Object("agents").Set("researcher", reference)
	})

	if setup.child.Owner == nil || setup.child.Owner.TaskId != setup.supervisorId {
		t.Fatalf("child conversation: %+v", setup.child)
	}
	registry, err := durable.Snapshot[agentRegistry](background, session, registryDoc, main.Id)
	want := agentRef{ConversationId: setup.child.Id, RequestId: fmt.Sprintf("researcher:first-message:%d", setup.supervisorId)}
	if err != nil || registry == nil || !reflect.DeepEqual(registry.Agents, map[string]agentRef{"researcher": want}) {
		t.Fatalf("registry: %+v %v", registry, err)
	}
	closeSession(t, session)
}

// 04-chord-state.ts: documentState() never creates a document; it returns a hydrated read-only Chord state.
// Pi source: packages/chord/src/api.ts, packages/chord/src/services/state-internals.ts
// mutation-checked: dropping the reads and writes of ReplicatedStateDelivery.Sequence fails it
func TestExample04ChordState(t *testing.T) {
	session := newSession(t)
	chat := commit(t, session, func(tx durable.Tx) (durable.ConversationRecord, error) {
		conversation, err := tx.CreateConversation(ownerless)
		if err != nil {
			return conversation, err
		}
		return conversation, setNotes(tx, conversation.Id, "first")
	})
	notesState, err := session.DocumentStateErased(background, notesDoc, chat.Id)
	if err != nil || notesState == nil {
		t.Fatalf("notes are absent: %v %v", notesState, err)
	}
	type frame struct {
		kind     chord.DeliveryKind
		sequence int
		text     string
	}
	var mu sync.Mutex
	var frames []frame
	stopNotes, err := notesState.Subscribe(func(value durable.JsonObject, _ context.Context, delivery chord.ReplicatedStateDelivery) {
		mu.Lock()
		defer mu.Unlock()
		text, _ := value.Value("text").(string)
		frames = append(frames, frame{delivery.Kind, delivery.Sequence, text})
	})
	if err != nil {
		t.Fatal(err)
	}
	commit(t, session, func(tx durable.Tx) (struct{}, error) {
		return struct{}{}, setNotes(tx, chat.Id, "published through Chord")
	})
	eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(frames) == 2
	})
	stopNotes()
	notesState.Dispose()
	mu.Lock()
	defer mu.Unlock()
	want := []frame{{chord.DeliveryHydrate, 0, "first"}, {chord.DeliveryUpdate, 1, "published through Chord"}}
	if !reflect.DeepEqual(frames, want) {
		t.Fatalf("Chord notes: %+v, want %+v", frames, want)
	}
	closeSession(t, session)
}

// 05-watches.ts: a watch serializes asynchronous document work from one stable acquisition revision.
func TestExample05Watches(t *testing.T) {
	session := newSession(t)
	chat := commit(t, session, func(tx durable.Tx) (durable.ConversationRecord, error) {
		conversation, err := tx.CreateConversation(ownerless)
		if err != nil {
			return conversation, err
		}
		return conversation, setNotes(tx, conversation.Id, "first")
	})
	notesWatch, err := session.WatchDocErased(background, notesDoc, chat.Id)
	if err != nil || notesWatch == nil {
		t.Fatalf("notes are absent: %v %v", notesWatch, err)
	}
	if baseline, _ := notesWatch.Value().Value("text").(string); baseline != "first" {
		t.Fatalf("watch baseline: %q", baseline)
	}
	updates := make(chan string, 1)
	notesWatch.Start(func(_ context.Context, value durable.JsonObject, _ []durable.Op) error {
		text, _ := value.Value("text").(string)
		select {
		case updates <- text:
		default:
		}
		return nil
	})
	commit(t, session, func(tx durable.Tx) (struct{}, error) {
		return struct{}{}, setNotes(tx, chat.Id, "observed asynchronously")
	})
	if got := <-updates; got != "observed asynchronously" {
		t.Fatalf("watch update: %q", got)
	}
	if _, err := notesWatch.Stop(); err != nil {
		t.Fatal(err)
	}
	closeSession(t, session)
}
