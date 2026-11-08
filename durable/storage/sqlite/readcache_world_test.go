package sqlite_test

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	chorddelta "github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
)

// A cacheWorld is a random but valid history of durable records and the commits that grow it, so a test can write the same
// database through several connections and read every kind of record back after each write.

type cacheConversation struct {
	id      durable.ConversationId
	parent  *durable.ConversationParent
	entries []durable.EntryId
}

type cacheDocument struct {
	id      durable.DocumentId
	address durable.DocumentAddress
	retired bool
	history bool
}

type cacheWorld struct {
	rng           *rand.Rand
	nextId        int64
	conversations []*cacheConversation
	tasks         []durable.TaskId
	taskKinds     map[durable.TaskId]string
	submissions   []durable.SubmissionId
	requests      map[durable.SubmissionId]string
	documents     []*cacheDocument
	seqs          []durable.Seq
	counter       int
}

func newCacheWorld(seed int64) *cacheWorld {
	return &cacheWorld{
		rng: rand.New(rand.NewPCG(uint64(seed), 0x9e3779b97f4a7c15)), nextId: 100,
		taskKinds: map[durable.TaskId]string{}, requests: map[durable.SubmissionId]string{},
	}
}

func (w *cacheWorld) mint() int64 {
	w.nextId++
	return w.nextId
}

// root is the batch that creates the root conversation.
func (w *cacheWorld) root() []durable.StorageWrite {
	w.conversations = append(w.conversations, &cacheConversation{id: durable.ROOT_CONVERSATION_ID})
	return []durable.StorageWrite{durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}}}
}

func (w *cacheWorld) pickConversation() *cacheConversation {
	return w.conversations[w.rng.IntN(len(w.conversations))]
}

func messages(text string) []ai.Message {
	return []ai.Message{ai.UserMessage{Content: ai.UserText(text), Timestamp: 1}}
}

func (w *cacheWorld) entryWrites() []durable.StorageWrite {
	conversation := w.pickConversation()
	var writes []durable.StorageWrite
	for range 1 + w.rng.IntN(4) {
		id := durable.EntryId(w.mint())
		w.counter++
		record := durable.EntryRecord{
			Id: id, ConversationId: conversation.id, Kind: "m",
			Data: map[string]any{"n": float64(w.counter), "text": fmt.Sprint("entry ", w.counter)},
		}
		if w.rng.IntN(3) == 0 {
			record.Model = messages(fmt.Sprint("model ", w.counter))
		}
		if w.rng.IntN(4) == 0 {
			head := id
			record.Head = &head
		}
		conversation.entries = append(conversation.entries, id)
		writes = append(writes, durable.EntryWrite{Value: record})
	}
	return writes
}

func (w *cacheWorld) forkWrites() []durable.StorageWrite {
	parent := w.pickConversation()
	if len(parent.entries) == 0 {
		return w.entryWrites()
	}
	at := parent.entries[w.rng.IntN(len(parent.entries))]
	child := &cacheConversation{
		id:     durable.ConversationId(w.mint()),
		parent: &durable.ConversationParent{ConversationId: parent.id, At: at},
	}
	w.conversations = append(w.conversations, child)
	return []durable.StorageWrite{durable.ConversationWrite{Value: durable.ConversationRecord{Id: child.id, Parent: child.parent}}}
}

func (w *cacheWorld) taskWrites() []durable.StorageWrite {
	var id durable.TaskId
	if len(w.tasks) == 0 || w.rng.IntN(3) == 0 {
		id = durable.TaskId(w.mint())
		w.tasks = append(w.tasks, id)
		w.taskKinds[id] = []string{"k.a", "k.b", "k.c"}[w.rng.IntN(3)]
	} else {
		id = w.tasks[w.rng.IntN(len(w.tasks))]
	}
	var checkpoint durable.JsonValue = map[string]any{"step": float64(w.rng.IntN(5))}
	record := storedTask{
		Id: id, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: w.taskKinds[id], Version: 1,
		Input: map[string]any{"id": float64(id)}, Background: w.rng.IntN(2) == 0, AbortRequested: w.rng.IntN(4) == 0,
		State: durable.TaskState[durable.JsonValue, durable.JsonValue]{
			Status:     []durable.TaskStatus{durable.TaskPending, durable.TaskRunning, durable.TaskWaiting}[w.rng.IntN(3)],
			Checkpoint: &checkpoint,
		},
	}
	if w.rng.IntN(5) == 0 {
		var result durable.JsonValue = map[string]any{"ok": true}
		record.State = durable.TaskState[durable.JsonValue, durable.JsonValue]{
			Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted, Result: &result},
		}
	}
	return []durable.StorageWrite{durable.TaskWrite{Value: record}}
}

func (w *cacheWorld) submissionWrites() []durable.StorageWrite {
	var id durable.SubmissionId
	if len(w.submissions) == 0 || w.rng.IntN(3) == 0 {
		id = durable.SubmissionId(w.mint())
		w.submissions = append(w.submissions, id)
		w.requests[id] = fmt.Sprint("request-", id)
	} else {
		id = w.submissions[w.rng.IntN(len(w.submissions))]
	}
	request := w.requests[id]
	record := durable.SubmissionRecord{
		Id: id, ConversationId: durable.ROOT_CONVERSATION_ID, RequestId: &request, Type: durable.SubmissionTypeWrite,
		Status: []durable.SubmissionStatus{durable.SubmissionQueued, durable.SubmissionPlaced, durable.SubmissionDone}[w.rng.IntN(3)],
	}
	return []durable.StorageWrite{durable.SubmissionWrite{Value: record}}
}

// noKey names the address of a singleton document, which has no key: the keys of a family may be any string, the empty
// one too.
const noKey = "\x00none"

var (
	cacheDocumentKeys = []string{"a", "b", "c", "", noKey}
	keyOf             = func(key string) *string {
		if key == noKey {
			return nil
		}
		return &key
	}
	sessionAddress = func(key string) durable.DocumentAddress {
		return durable.DocumentAddress{Kind: "t.s", Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}, Key: keyOf(key)}
	}
	historyAddress = func(conversation durable.ConversationId, key string) durable.DocumentAddress {
		return durable.DocumentAddress{Kind: "t.h", Scope: durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: conversation}, Key: keyOf(key)}
	}
)

func (w *cacheWorld) documentWrites() []durable.StorageWrite {
	live := func(address durable.DocumentAddress) *cacheDocument {
		for _, document := range w.documents {
			if !document.retired && reflect.DeepEqual(document.address, address) {
				return document
			}
		}
		return nil
	}
	var address durable.DocumentAddress
	history := w.rng.IntN(2) == 0
	if history {
		address = historyAddress(w.pickConversation().id, cacheDocumentKeys[w.rng.IntN(len(cacheDocumentKeys))])
	} else {
		address = sessionAddress(cacheDocumentKeys[w.rng.IntN(len(cacheDocumentKeys))])
	}
	document := live(address)
	switch {
	case document == nil:
		id := durable.DocumentId(w.mint())
		created := &cacheDocument{id: id, address: address, history: history}
		w.documents = append(w.documents, created)
		record := durable.DocumentCreate{Id: id, Kind: address.Kind, Key: address.Key, Scope: address.Scope}
		if history {
			record.History, record.Fork = durable.HistoryRewindable, durable.ForkAsOf
		}
		return []durable.StorageWrite{durable.DocumentCreateWrite{
			Record:  record,
			Content: durable.DocumentContent{Kind: durable.ContentBase, Version: 1, Value: chorddelta.JsonObjectOf("count", 0, "text", "base")},
		}}
	case w.rng.IntN(6) == 0:
		document.retired = true
		return []durable.StorageWrite{durable.DocumentRetireWrite{Id: document.id}}
	case w.rng.IntN(5) == 0:
		w.counter++
		return []durable.StorageWrite{durable.DocumentChangeWrite{Id: document.id, Content: durable.DocumentContent{
			Kind: durable.ContentBase, Version: 1, Value: chorddelta.JsonObjectOf("count", w.counter, "text", "rebased"),
		}}}
	default:
		w.counter++
		return []durable.StorageWrite{durable.DocumentChangeWrite{Id: document.id, Content: durable.DocumentContent{
			Kind: durable.ContentDelta, Version: 1, Ops: []durable.Op{{"s", []any{"count"}, w.counter}},
		}}}
	}
}

// next is a random batch of writes that the storage accepts.
func (w *cacheWorld) next() []durable.StorageWrite {
	if len(w.conversations) == 0 {
		return w.root()
	}
	switch w.rng.IntN(10) {
	case 0:
		return w.forkWrites()
	case 1, 2:
		return w.taskWrites()
	case 3:
		return w.submissionWrites()
	case 4, 5:
		return w.documentWrites()
	default:
		return w.entryWrites()
	}
}

// commit writes batch through storage and records the sequence number it was stored at.
func (w *cacheWorld) commit(t *testing.T, storage durable.Storage, batch []durable.StorageWrite) {
	t.Helper()
	w.seqs = append(w.seqs, must(durabletest.CommitWrites(testContext, storage, batch)))
}

// answer is what a read returned: its value, or the error it failed with.
type answer struct {
	Value any
	Err   string
}

func answerOf(value any, err error) answer {
	if err != nil {
		return answer{Err: err.Error()}
	}
	return answer{Value: value}
}

type probe struct {
	name string
	read func(storage durable.Storage) answer
}

// probes are reads of every kind the cache keeps, over the records of the cacheWorld and over ones it does not hold.
func (w *cacheWorld) probes() []probe {
	var probes []probe
	add := func(name string, read func(storage durable.Storage) answer) {
		probes = append(probes, probe{name, read})
	}
	entryIds := []durable.EntryId{1, 7}
	var allEntries []durable.EntryId
	for _, conversation := range w.conversations {
		allEntries = append(allEntries, conversation.entries...)
	}
	for range 3 {
		if len(allEntries) > 0 {
			entryIds = append(entryIds, allEntries[w.rng.IntN(len(allEntries))])
		}
	}
	conversationIds := []durable.ConversationId{durable.ROOT_CONVERSATION_ID, 9999}
	for _, conversation := range w.conversations {
		conversationIds = append(conversationIds, conversation.id)
	}
	sort.Slice(conversationIds, func(i, j int) bool { return conversationIds[i] < conversationIds[j] })
	for _, id := range entryIds {
		add(fmt.Sprint("Entry ", id), func(s durable.Storage) answer { return answerOf(s.Entry(testContext, id)) })
		for _, conversation := range conversationIds[:min(len(conversationIds), 4)] {
			add(fmt.Sprint("VisibleEntry ", conversation, " ", id), func(s durable.Storage) answer {
				return answerOf(s.VisibleEntry(testContext, conversation, id))
			})
		}
	}
	for _, id := range conversationIds {
		add(fmt.Sprint("Conversation ", id), func(s durable.Storage) answer { return answerOf(s.Conversation(testContext, id)) })
		add(fmt.Sprint("FindLatestHeadMarker ", id), func(s durable.Storage) answer { return answerOf(s.FindLatestHeadMarker(testContext, id, nil)) })
		for _, at := range entryIds[:min(len(entryIds), 3)] {
			add(fmt.Sprint("FindLatestHeadMarker ", id, " at ", at), func(s durable.Storage) answer {
				return answerOf(s.FindLatestHeadMarker(testContext, id, &at))
			})
		}
		for _, order := range []*durable.ScanOrder{nil, new(durable.ScanAscending), new(durable.ScanDescending)} {
			for _, limit := range []int{1, 3, 100} {
				query := durable.EntryQuery{ConversationId: id, Order: order}
				add(fmt.Sprint("ScanEntries ", id, " ", order, " ", limit), func(s durable.Storage) answer {
					return answerOf(scanEntryPages(s, query, limit))
				})
			}
		}
		bounded := durable.EntryQuery{ConversationId: id, MinEntryId: new(entryIds[2%len(entryIds)]), MaxEntryId: new(entryIds[len(entryIds)-1])}
		add(fmt.Sprint("ScanEntries bounded ", id), func(s durable.Storage) answer { return answerOf(s.ScanEntries(testContext, bounded, 5, nil)) })
	}
	for _, order := range []*durable.ScanOrder{nil, new(durable.ScanDescending)} {
		add(fmt.Sprint("ScanConversations ", order), func(s durable.Storage) answer {
			return answerOf(s.ScanConversations(testContext, durable.ConversationQuery{Order: order}, 2, nil))
		})
	}
	for _, id := range append([]durable.TaskId{42}, w.tasks...) {
		add(fmt.Sprint("Task ", id), func(s durable.Storage) answer { return answerOf(s.Task(testContext, id)) })
	}
	for _, kind := range []*string{nil, new("k.a"), new("k.b")} {
		for _, status := range []*durable.TaskStatus{nil, new(durable.TaskPending), new(durable.TaskTerminal)} {
			for _, background := range []*bool{nil, new(true), new(false)} {
				query := durable.TaskQuery{Kind: kind, Status: status, Background: background}
				add(fmt.Sprint("ScanTasks ", kind, status, background), func(s durable.Storage) answer {
					return answerOf(scanTaskPages(s, query, 2))
				})
			}
		}
	}
	add("ScanTasks abort", func(s durable.Storage) answer {
		return answerOf(s.ScanTasks(testContext, durable.TaskQuery{AbortRequested: new(true), ConversationId: new(durable.ROOT_CONVERSATION_ID)}, 50, nil))
	})
	for _, id := range append([]durable.SubmissionId{43}, w.submissions...) {
		add(fmt.Sprint("Submission ", id), func(s durable.Storage) answer { return answerOf(s.Submission(testContext, id)) })
		request := w.requests[id]
		add(fmt.Sprint("SubmissionByRequest ", id), func(s durable.Storage) answer {
			return answerOf(s.SubmissionByRequest(testContext, durable.ROOT_CONVERSATION_ID, request))
		})
	}
	add("ScanSubmissions", func(s durable.Storage) answer {
		return answerOf(s.ScanSubmissions(testContext, durable.SubmissionQuery{Status: new(durable.SubmissionDone)}, 2, nil))
	})
	points := []durable.DocumentPoint{durable.CurrentPoint, durable.AtSeq(1)}
	for range 3 {
		if len(w.seqs) > 0 {
			points = append(points, durable.AtSeq(w.seqs[w.rng.IntN(len(w.seqs))]))
		}
	}
	for _, document := range w.documents {
		for _, at := range points {
			add(fmt.Sprint("Document ", document.id, " ", at), func(s durable.Storage) answer { return answerOf(s.Document(testContext, document.id, at)) })
			add(fmt.Sprint("FindDocument ", document.id, " ", at), func(s durable.Storage) answer { return answerOf(s.FindDocument(testContext, document.address, at)) })
		}
	}
	add("Document 12345", func(s durable.Storage) answer { return answerOf(s.Document(testContext, 12345, durable.CurrentPoint)) })
	for _, address := range []durable.DocumentAddress{sessionAddress("a"), sessionAddress("zz"), historyAddress(1, "b")} {
		for _, at := range points[:2] {
			add(fmt.Sprint("ScanDocuments ", address.Scope, at), func(s durable.Storage) answer {
				return answerOf(s.ScanDocuments(testContext, durable.DocumentQuery{Scope: address.Scope, At: at, Kind: &address.Kind}, 2, nil))
			})
		}
	}
	add("ScanDocuments all", func(s durable.Storage) answer {
		return answerOf(s.ScanDocuments(testContext, durable.DocumentQuery{Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}, At: durable.CurrentPoint}, 100, nil))
	})
	return probes
}

// scanEntryPages reads every page of a scan, following its cursors.
func scanEntryPages(storage durable.Storage, query durable.EntryQuery, limit int) ([]durable.Page[durable.EntryRecord, durable.Cursor], error) {
	var pages []durable.Page[durable.EntryRecord, durable.Cursor]
	var cursor durable.Cursor
	for range 100 {
		page, err := storage.ScanEntries(testContext, query, limit, cursor)
		if err != nil {
			return nil, err
		}
		pages = append(pages, page)
		if page.Next == nil {
			return pages, nil
		}
		cursor = *page.Next
	}
	return pages, nil
}

func scanTaskPages(storage durable.Storage, query durable.TaskQuery, limit int) ([]durable.Page[storedTask, durable.Cursor], error) {
	var pages []durable.Page[storedTask, durable.Cursor]
	var cursor durable.Cursor
	for range 100 {
		page, err := storage.ScanTasks(testContext, query, limit, cursor)
		if err != nil {
			return nil, err
		}
		pages = append(pages, page)
		if page.Next == nil {
			return pages, nil
		}
		cursor = *page.Next
	}
	return pages, nil
}

// sameAnswers fails the test when a read of cached differs from the same read of reference, which reads the database.
func sameAnswers(t *testing.T, step string, probes []probe, cached, reference durable.Storage) {
	t.Helper()
	for _, probe := range probes {
		got, want := probe.read(cached), probe.read(reference)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %s\n cached:    %s\n reference: %s", step, probe.name, durabletest.Describe(got), durabletest.Describe(want))
		}
	}
}

func itoa(value int) string { return strconv.Itoa(value) }
