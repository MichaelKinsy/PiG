package harness

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage/sqlite"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

// The context cache must return exactly what DeriveContext derives, through every sequence of commits and reads.
// DeriveContext, which reads the table on every call, is the oracle.

func cacheTestStorage(t *testing.T) *sqlite.SqliteStorage {
	t.Helper()
	storage, err := sqlitenode.OpenNodeSqliteStorage(filepath.Join(t.TempDir(), "s.sqlite"), sqlitenode.NodeSqliteStorageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close(testContext) })
	return storage
}

func commitWrites(t *testing.T, storage durable.Storage, writes ...durable.StorageWrite) {
	t.Helper()
	if _, err := storage.Commit(testContext, writes); err != nil {
		t.Fatal(err)
	}
}

type cacheScenario struct {
	t       *testing.T
	storage durable.Storage
	random  *rand.Rand
	next    durable.EntryId
	ids     map[durable.ConversationId][]durable.EntryId
	calls   int
	cache   *contextCache
}

func (s *cacheScenario) userEntry(conversation durable.ConversationId, text string) durable.EntryRecord {
	s.next++
	return durable.EntryRecord{Id: s.next, ConversationId: conversation, Kind: "message", Model: []ai.Message{ai.UserMessage{Content: ai.UserText(text), Timestamp: int64(s.next)}}}
}

// systemEntry is a system message such as the baseline a run renders after its input is committed (Pi 1.1.0 leads the
// context with a system message that only user messages precede).
func (s *cacheScenario) systemEntry(conversation durable.ConversationId) durable.EntryRecord {
	s.next++
	return durable.EntryRecord{Id: s.next, ConversationId: conversation, Kind: "pi.system", Model: []ai.Message{
		system(ai.PromptSection{Name: fmt.Sprint("section ", s.next), Value: new(fmt.Sprint("prompt ", s.next))}),
	}}
}

func (s *cacheScenario) assistantEntry(conversation durable.ConversationId, calls int, stop ai.StopReason) durable.EntryRecord {
	s.next++
	content := []ai.AssistantContentBlock{ai.TextContent{Text: fmt.Sprint("answer ", s.next)}}
	for call := range calls {
		content = append(content, ai.ToolCall{ID: fmt.Sprintf("c%d-%d", s.next, call), Name: "lookup", Arguments: ai.JsonObject{"n": float64(call)}})
	}
	return durable.EntryRecord{Id: s.next, ConversationId: conversation, Kind: "message", Model: []ai.Message{ai.AssistantMessage{Content: content, StopReason: stop, Timestamp: int64(s.next)}}}
}

func (s *cacheScenario) resultEntry(conversation durable.ConversationId, callOf durable.EntryId, call int) durable.EntryRecord {
	s.next++
	return durable.EntryRecord{Id: s.next, ConversationId: conversation, Kind: "message", Model: []ai.Message{ai.ToolResultMessage{
		ToolCallID: fmt.Sprintf("c%d-%d", callOf, call), ToolName: "lookup", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprint("result ", s.next)}}, Timestamp: int64(s.next),
	}}}
}

func (s *cacheScenario) add(conversation durable.ConversationId, records ...durable.EntryRecord) {
	writes := make([]durable.StorageWrite, len(records))
	for index, record := range records {
		writes[index] = durable.EntryWrite{Value: record}
		s.ids[conversation] = append(s.ids[conversation], record.Id)
	}
	commitWrites(s.t, s.storage, writes...)
}

// turn commits one realistic exchange: a user entry, an assistant with calls, and their results, in one commit or several.
func (s *cacheScenario) turn(conversation durable.ConversationId) {
	calls := s.random.Intn(3)
	user := s.userEntry(conversation, fmt.Sprint("turn tools=", calls))
	assistant := s.assistantEntry(conversation, calls, []ai.StopReason{"toolUse", "stop", "error", "aborted"}[s.random.Intn(4)])
	records := []durable.EntryRecord{user}
	if s.random.Intn(3) == 0 {
		records = append(records, s.systemEntry(conversation))
	}
	records = append(records, assistant)
	for call := range calls {
		if s.random.Intn(5) != 0 {
			records = append(records, s.resultEntry(conversation, assistant.Id, call))
		}
	}
	if s.random.Intn(3) == 0 {
		for _, record := range records {
			s.add(conversation, record)
		}
		return
	}
	s.add(conversation, records...)
}

func (s *cacheScenario) read(conversation durable.ConversationId) {
	s.t.Helper()
	var at *durable.EntryId
	if ids := s.ids[conversation]; len(ids) > 0 && s.random.Intn(3) == 0 {
		at = new(ids[s.random.Intn(len(ids))])
	}
	bounds, err := CaptureContextBounds(testContext, s.storage, conversation, at)
	if err != nil {
		// An entry of another conversation's ancestry the fork cannot see.
		return
	}
	want, err := DeriveContext(testContext, s.storage, conversation, bounds)
	if err != nil {
		s.t.Fatal(err)
	}
	got, err := s.cache.derive(testContext, s.storage, conversation, bounds)
	if err != nil {
		s.t.Fatal(err)
	}
	s.calls++
	if !reflect.DeepEqual(got, want) {
		s.t.Fatalf("read %d of conversation %d at %v: cached view differs\n got entries %v messages %d\nwant entries %v messages %d", s.calls, conversation, at, cachedEntryIds(got.Entries), len(got.Messages), cachedEntryIds(want.Entries), len(want.Messages))
	}
}

func cachedEntryIds(entries []durable.EntryRecord) []durable.EntryId {
	ids := make([]durable.EntryId, len(entries))
	for index := range entries {
		ids[index] = entries[index].Id
	}
	return ids
}

func newCacheScenario(t *testing.T, seed int64) *cacheScenario {
	storage := cacheTestStorage(t)
	commitWrites(t, storage, durable.ConversationWrite{Value: durable.ConversationRecord{Id: 1}})
	return &cacheScenario{t: t, storage: storage, random: rand.New(rand.NewSource(seed)), next: 100, ids: map[durable.ConversationId][]durable.EntryId{}, cache: newContextCache()}
}

func TestContextCacheMatchesDeriveContextWhileAConversationGrows(t *testing.T) {
	for seed := int64(1); seed <= 6; seed++ {
		t.Run(fmt.Sprint("seed ", seed), func(t *testing.T) {
			s := newCacheScenario(t, seed)
			for range 60 {
				s.turn(1)
				for range 1 + s.random.Intn(3) {
					s.read(1)
				}
			}
			s.read(1)
		})
	}
}

func TestContextCacheMatchesDeriveContextThroughHeadMarkersEditsAndForks(t *testing.T) {
	for seed := int64(1); seed <= 8; seed++ {
		t.Run(fmt.Sprint("seed ", seed), func(t *testing.T) {
			s := newCacheScenario(t, seed)
			forks := []durable.ConversationId{1}
			for step := range 120 {
				conversation := forks[s.random.Intn(len(forks))]
				switch s.random.Intn(10) {
				case 0, 1, 2, 3:
					s.turn(conversation)
				case 4:
					// A head marker: starts the active context at an earlier entry, with a summary.
					if ids := s.ids[conversation]; len(ids) > 2 {
						from := ids[s.random.Intn(len(ids))]
						s.next++
						marker := durable.EntryRecord{Id: s.next, ConversationId: conversation, Kind: "compaction", Head: new(from), Model: []ai.Message{ai.UserMessage{Content: ai.UserText("summary"), Timestamp: 1}}}
						s.add(conversation, marker)
					}
				case 5:
					// An entry whose edits omit or replace earlier ones.
					if ids := s.ids[conversation]; len(ids) > 2 {
						s.next++
						edits := []durable.ContextEdit{{Target: ids[s.random.Intn(len(ids))], Action: durable.EditOmit}}
						if s.random.Intn(2) == 0 {
							edits = append(edits, durable.ContextEdit{Target: ids[s.random.Intn(len(ids))], Action: durable.EditReplace, Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("replaced"), Timestamp: 2}}})
						}
						s.add(conversation, durable.EntryRecord{Id: s.next, ConversationId: conversation, Kind: "edit", Edits: edits})
					}
				case 6:
					// A fork at a visible entry of this conversation.
					if ids := s.ids[conversation]; len(ids) > 1 && len(forks) < 4 {
						id := durable.ConversationId(len(forks) + 1)
						commitWrites(t, s.storage, durable.ConversationWrite{Value: durable.ConversationRecord{Id: id, Parent: &durable.ConversationParent{ConversationId: conversation, At: ids[s.random.Intn(len(ids))]}}})
						forks = append(forks, id)
					}
				case 7:
					// An entry committed out of ID order: an ID below the newest of the conversation.
					if ids := s.ids[conversation]; len(ids) > 0 {
						s.next++
						late := durable.EntryRecord{Id: s.next, ConversationId: conversation, Kind: "message", Model: []ai.Message{ai.UserMessage{Content: ai.UserText("early id"), Timestamp: 3}}}
						s.turn(conversation)
						s.read(conversation)
						s.add(conversation, late)
						s.read(conversation)
					}
				default:
					s.read(conversation)
				}
				if step%7 == 0 {
					s.read(forks[s.random.Intn(len(forks))])
				}
			}
			for _, conversation := range forks {
				s.read(conversation)
				s.read(conversation)
			}
		})
	}
}

func TestContextCacheViewsAreNotWrittenAfterTheyAreHandedOut(t *testing.T) {
	s := newCacheScenario(t, 99)
	s.turn(1)
	bounds := func() *ContextBounds {
		bounds, err := CaptureContextBounds(testContext, s.storage, 1, nil)
		if err != nil {
			t.Fatal(err)
		}
		return bounds
	}
	first, err := s.cache.derive(testContext, s.storage, 1, bounds())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := durable.ContextView{Head: first.Head, Entries: append([]durable.EntryRecord(nil), first.Entries...), Contributions: append([][]ai.Message(nil), first.Contributions...), Messages: append([]ai.Message(nil), first.Messages...)}
	for range 20 {
		s.turn(1)
		if _, err := s.cache.derive(testContext, s.storage, 1, bounds()); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, snapshot) {
			t.Fatal("a view changed after later reads extended the cache")
		}
	}
	// Messages is a fresh slice per view; Entries and Contributions are windows of the cache.
	if cap(first.Entries) != len(first.Entries) || cap(first.Contributions) != len(first.Contributions) {
		t.Fatal("a view has spare capacity, so an append would write into the cache")
	}
}
