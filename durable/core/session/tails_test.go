package session_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// session-conversation-tails.test.ts: the newest entry and head marker of a conversation are known from the Session's commits, so a read
// of them reaches the Store once, and not at all for what the Session wrote.

// readCounts is a Store that counts the reads the tails answer.
type readCounts struct {
	*sessiontest.MemStore
	latest, markers, taskScans int
}

func (c *readCounts) ScanDocuments(scope session.Scope, at session.Point, kind string, limit int, after session.ID) (session.Page, error) {
	if scope.Kind == session.ScopeTask {
		c.taskScans++
	}
	return c.MemStore.ScanDocuments(scope, at, kind, limit, after)
}

func (c *readCounts) ScanEntries(q session.EntryQuery, limit int, after session.ID) (session.Page, error) {
	c.latest++
	return c.MemStore.ScanEntries(q, limit, after)
}

func (c *readCounts) LatestHeadMarker(conv, atOrBefore session.ID) (*jv.Object, error) {
	c.markers++
	return c.MemStore.LatestHeadMarker(conv, atOrBefore)
}

func appendKind(t *testing.T, s *session.Session, conv session.ID, kind string, head session.ID) session.ID {
	t.Helper()
	var id session.ID
	if _, err := s.Commit(func(tx *session.Tx) error {
		d := draft(kind)
		if head != 0 {
			d.Set("head", float64(head))
		}
		e, err := tx.AppendEntry(conv, d)
		id = idOf(e)
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	return id
}

func rootConversation(t *testing.T, s *session.Session) session.ID {
	t.Helper()
	var conv session.ID
	if _, err := s.Commit(func(tx *session.Tx) error {
		c, err := tx.CreateRootConversation()
		conv = idOf(c)
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	return conv
}

func entryChange(id, conv, head session.ID) session.Change {
	rec := jv.NewObject()
	rec.Set("id", float64(id))
	rec.Set("conversationId", float64(conv))
	rec.Set("kind", "note")
	if head != 0 {
		rec.Set("head", float64(head))
	}
	return session.Change{Kind: session.ChangeTable, Table: session.WriteEntry, Record: rec}
}

func TestTailsReadOnceThenFollowTheCommits(t *testing.T) {
	mem := &readCounts{MemStore: sessiontest.New()}
	writer := newSession(mem, mem.NextID())
	conv := rootConversation(t, writer)
	appendKind(t, writer, conv, "note", 0)
	last := appendKind(t, writer, conv, "note", 0)

	// Another Session over the same Store has seen none of it: the first read goes to the Store, the next ones do not.
	s := newSession(mem, mem.NextID())
	id, ticks, err := s.LatestEntryID(conv)
	if err != nil || id != last || mem.latest != 1 || ticks <= hopcost.KnownTail {
		t.Fatalf("first read = %d, %d ticks, %d Store reads, %v; want %d from the Store", id, ticks, mem.latest, err, last)
	}
	if id, ticks, _ = s.LatestEntryID(conv); id != last || mem.latest != 1 || ticks != hopcost.KnownTail {
		t.Fatalf("second read = %d, %d ticks, %d Store reads; want it known", id, ticks, mem.latest)
	}
	next := appendKind(t, s, conv, "note", 0)
	if id, _, _ = s.LatestEntryID(conv); id != next || mem.latest != 1 {
		t.Fatalf("after a commit = %d with %d Store reads, want %d known", id, mem.latest, next)
	}
}

func TestTailsKnowANewRootConversationIsEmptyAndAForkOnlyOnceReadOrWritten(t *testing.T) {
	mem := &readCounts{MemStore: sessiontest.New()}
	s := newSession(mem, mem.NextID())
	root := rootConversation(t, s)
	first := appendKind(t, s, root, "note", 0)
	var fork session.ID
	if _, err := s.Commit(func(tx *session.Tx) error {
		c, err := tx.ForkConversation(root, first, session.Ownership{})
		fork = idOf(c)
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	var other session.ID
	if _, err := s.Commit(func(tx *session.Tx) error {
		c, err := tx.CreateConversation(session.Ownership{})
		other = idOf(c)
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	if id, _, _ := s.LatestEntryID(other); id != 0 || mem.latest != 0 {
		t.Fatalf("a new root conversation: newest %d after %d Store reads, want none known", id, mem.latest)
	}
	if marker, _, _ := s.LatestHeadMarker(other, 0); marker != nil || mem.markers != 0 {
		t.Fatalf("a new root conversation has no marker, known: %v after %d Store reads", marker, mem.markers)
	}
	// A fork sees its parent's entries up to the fork point: read once.
	if id, _, _ := s.LatestEntryID(fork); id != first || mem.latest != 1 {
		t.Fatalf("a fork: newest %d after %d Store reads, want %d read once", id, mem.latest, first)
	}
	if _, _, _ = s.LatestEntryID(fork); mem.latest != 1 {
		t.Fatalf("a fork read again: %d Store reads, want it kept", mem.latest)
	}
}

func TestTailsAnswerACutoffFromTheNewestMarkerOnlyAtOrAfterIt(t *testing.T) {
	mem := &readCounts{MemStore: sessiontest.New()}
	s := newSession(mem, mem.NextID())
	conv := rootConversation(t, s)
	first := appendKind(t, s, conv, "note", 0)
	marker := appendKind(t, s, conv, "pi.compaction", first)
	if m, ticks, _ := s.LatestHeadMarker(conv, 0); idOf(m) != marker || ticks != hopcost.KnownTail || mem.markers != 0 {
		t.Fatalf("newest marker = %v, %d ticks, %d Store reads; want %d known", m, ticks, mem.markers, marker)
	}
	if m, _, _ := s.LatestHeadMarker(conv, marker+1); idOf(m) != marker || mem.markers != 0 {
		t.Fatalf("a cutoff after the marker = %v with %d Store reads, want it known", m, mem.markers)
	}
	// An older cutoff may have an older marker: read, and not kept.
	for want := 1; want <= 2; want++ {
		if m, _, _ := s.LatestHeadMarker(conv, first); m != nil || mem.markers != want {
			t.Fatalf("a cutoff before the marker = %v after %d Store reads, want %d reads and no marker", m, mem.markers, want)
		}
	}
	if m, _, _ := s.LatestHeadMarker(conv, 0); idOf(m) != marker || mem.markers != 2 {
		t.Fatalf("the newest marker after older reads = %v with %d Store reads, want it still known", m, mem.markers)
	}
}

func TestTailsKeepABoundedNumberOfConversationsFewerWithMarkers(t *testing.T) {
	mem := &readCounts{MemStore: sessiontest.New()}
	s := newSession(mem, mem.NextID())
	const conversations = 5000
	for id := session.ID(1); id <= conversations; id++ {
		s.Publish(&session.Publication{Seq: 1, Changes: []session.Change{entryChange(2*id, id, 0), entryChange(2*id+1, id, 2*id)}})
	}
	known := func(id session.ID) (latest, marker bool) {
		_, latest = s.LookupLatestEntryID(id)
		_, marker = s.LookupHeadMarker(id, 0)
		return
	}
	// The newest 4096 conversations keep their newest entry, the newest 256 their marker; the least recently used leave first.
	for _, tc := range []struct {
		id           session.ID
		latest, mark bool
	}{
		{1, false, false},
		{conversations - 4096, false, false},
		{conversations - 4095, true, false},
		{conversations - 256, true, false},
		{conversations - 255, true, true},
		{conversations, true, true},
	} {
		if latest, marker := known(tc.id); latest != tc.latest || marker != tc.mark {
			t.Errorf("conversation %d: newest known %v, marker known %v; want %v and %v", tc.id, latest, marker, tc.latest, tc.mark)
		}
	}
}

var taskNote = &session.DocDef{Kind: "test.task-note", Version: 1, Scope: session.ScopeTask,
	Initial: func(any) *jv.Object { o := jv.NewObject(); o.Set("text", ""); return o }}

// createTaskWithNote creates a task and its task document in one commit of s.
func createTaskWithNote(t *testing.T, s *session.Session, conv session.ID) session.ID {
	t.Helper()
	var id session.ID
	if _, err := s.Commit(func(tx *session.Tx) error {
		def := &session.TaskDef{Name: "t.node", Version: 1, Initial: func(any) any { return jv.NewObject() }}
		var err error
		if id, err = tx.CreateTask(def, nil, session.TaskOptions{ConversationID: conv}); err != nil {
			return err
		}
		note, err := tx.Doc(taskNote, session.DocArgs{Owner: id})
		if err != nil {
			return err
		}
		return note.Set("text", "kept")
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	return id
}

func endTask(t *testing.T, s *session.Session, id session.ID) {
	t.Helper()
	if _, err := s.Commit(func(tx *session.Tx) error {
		rec, err := tx.Task(id)
		if err != nil {
			return err
		}
		ended := jv.Clone(rec).(*jv.Object)
		state := jv.NewObject()
		state.Set("status", session.StatusTerminal)
		outcome := jv.NewObject()
		outcome.Set("status", "completed")
		outcome.Set("result", nil)
		state.Set("outcome", outcome)
		ended.Set("state", state)
		return tx.SetTask(ended)
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
}

func liveTaskNotes(t *testing.T, mem *readCounts, task session.ID) int {
	t.Helper()
	page, err := mem.MemStore.ScanDocuments(session.Scope{Kind: session.ScopeTask, Owner: task}, session.Current, "", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	return len(page.Items)
}

// session.ts #taskDocuments: a task the Session created has its documents known from its commits, so ending it retires them without a
// scan of Storage; the documents of any other task are scanned.
func TestEndingATaskTheSessionCreatedRetiresItsDocumentsWithoutAScan(t *testing.T) {
	mem := &readCounts{MemStore: sessiontest.New()}
	s := newSession(mem, mem.NextID())
	conv := rootConversation(t, s)
	task := createTaskWithNote(t, s, conv)
	if known, ok := s.TaskDocuments(task); !ok || len(known) != 1 {
		t.Fatalf("documents known of the created task = %d, %v, want its one", len(known), ok)
	}
	endTask(t, s, task)
	if mem.taskScans != 0 || liveTaskNotes(t, mem, task) != 0 {
		t.Fatalf("after ending it: %d scans, %d documents left, want no scan and the document retired", mem.taskScans, liveTaskNotes(t, mem, task))
	}
	if _, ok := s.TaskDocuments(task); ok {
		t.Fatal("an ended task stays in the index")
	}
}

func TestEndingATaskAnotherSessionCreatedScansItsDocuments(t *testing.T) {
	mem := &readCounts{MemStore: sessiontest.New()}
	writer := newSession(mem, mem.NextID())
	conv := rootConversation(t, writer)
	task := createTaskWithNote(t, writer, conv)
	s := newSession(mem, mem.NextID())
	if _, ok := s.TaskDocuments(task); ok {
		t.Fatal("a task another Session created is known")
	}
	endTask(t, s, task)
	if mem.taskScans == 0 || liveTaskNotes(t, mem, task) != 0 {
		t.Fatalf("after ending it: %d scans, %d documents left, want a scan and the document retired", mem.taskScans, liveTaskNotes(t, mem, task))
	}
}
