package sqlite_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	chorddelta "github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
	"github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

// Pi stores an entry as JSON.stringify text and reads it with JSON.parse: a lone UTF-16 surrogate ("\ud800", WTF-8 in a Go
// string) is the escape in the stored text and the same unit on reading, a high unit followed by a low one is the character,
// in the kind, in the messages and in the data. The text holds only ASCII escapes of the unit, so the rows Pi binds as text
// (more than 8 Ki units: node:sqlite writes U+FFFD for a lone surrogate it is given) store the same text as the small ones.
func TestSqliteEntryTextKeepsLoneSurrogatesAtBothSidesOfTheBoundOfLargeRows(t *testing.T) {
	const lone, high, low = "\xed\xa0\x80", "\xed\xa0\xbd", "\xed\xb8\x80"
	for _, padding := range []int{0, 100, 8000, 8192, 9000, 70000} {
		storage, path := createSqliteStorage(t, node.NodeSqliteStorageOptions{})
		_ = must(durabletest.CommitWrites(testContext, storage, []durable.StorageWrite{durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}}}))
		pad := strings.Repeat("p", padding)
		record := durable.EntryRecord{
			Id: durable.EntryId(must(storage.MintId())), ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "k" + lone + high + low,
			Model: []ai.Message{ai.UserMessage{Content: ai.UserText("m" + high + low + lone + pad), Timestamp: 1}},
			Data:  chorddelta.JsonObjectOf("a"+low, "v"+high+low+high+pad, "b", []any{lone}),
		}
		_ = must(durabletest.CommitWrites(testContext, storage, []durable.StorageWrite{durable.EntryWrite{Value: record}}))
		mustDo(t, storage.Close(testContext))
		want := `{"id":` + strconv.FormatInt(int64(record.Id), 10) + `,"conversationId":1,"kind":"k\ud800😀","model":[{"role":"user","content":"m😀\ud800` + pad +
			`","timestamp":1}],"data":{"a\ude00":"v😀\ud83d` + pad + `","b":["\ud800"]}}`
		db := must(node.OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{}))
		rows := must(db.All("SELECT record FROM entries WHERE id = " + strconv.FormatInt(int64(record.Id), 10)))
		if len(rows) != 1 || rows[0]["record"] != want {
			t.Fatalf("padding %d: stored %v\nwant %s", padding, rows, want)
		}
		_ = db.Close()
		back := must(reopen(t, path).Entry(testContext, record.Id))
		if back == nil || back.Entry.Kind != "k"+lone+"😀" {
			t.Fatalf("padding %d: kind after reopen = %+v", padding, back)
		}
		if text := back.Entry.Model[0].(ai.UserMessage).Content.(ai.UserText); string(text) != "m😀"+lone+pad {
			t.Fatalf("padding %d: user text after reopen = %q", padding, text[:min(len(text), 20)])
		}
	}
}
