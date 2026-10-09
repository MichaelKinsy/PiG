package sqlite_test

import (
	"encoding/json"
	"testing"

	chorddelta "github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

// Pi packages/durable/src/storage/sqlite/storage.ts stores a document base and its delta batches as JSON.stringify text and
// reads them with JSON.parse, so a reopened document keeps each object's key order and a lone UTF-16 surrogate ("\ud800",
// WTF-8 in a Go string). The base content and the operation batches both decode with the JavaScript string semantics.
func TestSqliteDocumentTextKeepsKeyOrderAndLoneSurrogates(t *testing.T) {
	const lone = "\xed\xa0\x80"
	storage, path := createSqliteStorage(t, node.NodeSqliteStorageOptions{})
	_ = must(storage.Commit(testContext, []durable.StorageWrite{durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}}}))
	id := durable.DocumentId(must(storage.MintId()))
	_ = must(storage.Commit(testContext, []durable.StorageWrite{durable.DocumentCreateWrite{
		Record:  durable.DocumentCreate{Id: id, Kind: "text", Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}},
		Content: durable.DocumentContent{Kind: durable.ContentBase, Version: 1, Value: chorddelta.JsonObjectOf("z", lone, "a", chorddelta.JsonObjectOf("y", 1.0, "b", 2.0))},
	}}))
	_ = must(storage.Commit(testContext, []durable.StorageWrite{durable.DocumentChangeWrite{
		Id:      id,
		Content: durable.DocumentContent{Kind: durable.ContentDelta, Version: 1, Ops: []durable.Op{{"s", []any{"m"}, chorddelta.JsonObjectOf("q", lone, "c", 3.0)}}},
	}}))
	mustDo(t, storage.Close(testContext))
	stored := must(reopen(t, path).Document(testContext, id, durable.CurrentPoint))
	if stored == nil {
		t.Fatal("document is missing after reopen")
	}
	got := must(json.Marshal(stored.Value))
	if want := `{"z":"\ud800","a":{"y":1,"b":2},"m":{"q":"\ud800","c":3}}`; string(got) != want {
		t.Fatalf("document = %s, want %s", got, want)
	}
	if stored.Value.Value("z") != lone || stored.Value.Value("m").(*chorddelta.JsonObject).Value("q") != lone {
		t.Fatalf("the lone surrogate did not survive: %q", got)
	}
}
