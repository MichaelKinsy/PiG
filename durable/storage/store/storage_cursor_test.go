package store

// pi: packages/durable/src/storage/store/storage.ts
// cursorId, scanFrom and page of storage.ts are module internals of Pi (its public entry point exports none of them); the cases below
// assert them through the scans of StoreStorage, against the text of storage.ts.

import (
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage/internal/scan"
)

func TestScanDocumentsCursorIsASafeIntegerAndNothingElse(t *testing.T) {
	store := memoryStore(t)
	storage := value(Open(background, store, "harness"))
	ids := sessionDocuments(t, storage, 3)
	query := durable.DocumentQuery{Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}, At: durable.CurrentPoint}
	invalid := map[string]any{
		"a fraction":            1.5,
		"2^53 as a float":       float64(1 << 53),
		"beyond 2^53":           float64(1 << 60),
		"NaN":                   math.NaN(),
		"infinity":              math.Inf(1),
		"negative infinity":     math.Inf(-1),
		"2^53 as an integer":    int64(1 << 53),
		"-2^53 as an integer":   int64(-(1 << 53)),
		"-2^53 as an int":       int(-(1 << 53)),
		"-2^53 as a float":      float64(-(1 << 53)),
		"a string":              "7",
		"a boolean":             true,
		"null":                  nil,
		"a list":                []any{1},
		"a different int width": int32(1),
	}
	for name, after := range invalid {
		_, err := storage.ScanDocuments(background, query, 10, durable.Cursor{"after": after})
		if err == nil || !errors.Is(err, scan.ErrInvalidCursor) && err.Error() != "Invalid storage cursor" {
			t.Errorf("%s: ScanDocuments = %v, want Invalid storage cursor", name, err)
		}
	}
	// A safe integer continues after it, whatever its form; a cursor without `after` starts at the beginning.
	const maxSafe = 1<<53 - 1
	valid := []struct {
		name  string
		after any
		want  int
	}{
		{"none", nil, 3},
		{"before the first id", int64(-5), 3},
		{"the smallest safe integer", int64(-maxSafe), 3},
		{"the smallest safe integer as a float", float64(-maxSafe), 3},
		{"a whole float", float64(ids[0]), 2},
		{"an int", int(ids[1]), 1},
		{"an int64", int64(ids[2]), 0},
		{"the largest safe integer", int64(maxSafe), 0},
		{"the largest safe integer as a float", float64(maxSafe), 0},
	}
	for _, c := range valid {
		cursor := durable.Cursor{}
		if c.name != "none" {
			cursor["after"] = c.after
		}
		page, err := storage.ScanDocuments(background, query, 10, cursor)
		if err != nil || len(page.Items) != c.want || page.Next != nil {
			t.Errorf("%s: ScanDocuments after %v = %d items, %v (next %v), want %d", c.name, c.after, len(page.Items), err, page.Next, c.want)
		}
	}
	must(t, store.Close(background))
}

func TestScanDocumentsPagesByLimit(t *testing.T) {
	store := memoryStore(t)
	storage := value(Open(background, store, "harness"))
	ids := sessionDocuments(t, storage, 3)
	query := durable.DocumentQuery{Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}, At: durable.CurrentPoint}
	// A page holds up to limit items and a cursor after its last one only when more follow; a page that ends the scan has none.
	for _, c := range []struct {
		limit int
		items int
		more  bool
	}{{1, 1, true}, {2, 2, true}, {3, 3, false}, {4, 3, false}, {0, 0, true}} {
		page := value(storage.ScanDocuments(background, query, c.limit, nil))
		if len(page.Items) != c.items || (page.Next != nil) != c.more {
			t.Errorf("limit %d: %d items, next %v, want %d items, more %v", c.limit, len(page.Items), page.Next, c.items, c.more)
		}
	}
	first := value(storage.ScanDocuments(background, query, 2, nil))
	if first.Next == nil || (*first.Next)["after"] != int64(ids[1]) || (*first.Next)["order"] != "ascending" {
		t.Fatalf("cursor after two = %v, want after %d, ascending", first.Next, ids[1])
	}
	rest := value(storage.ScanDocuments(background, query, 2, *first.Next))
	if len(rest.Items) != 1 || rest.Items[0].Id != ids[2] || rest.Next != nil {
		t.Fatalf("rest = %+v, want the last document and no cursor", rest)
	}
	must(t, store.Close(background))
}

func TestScanConversationsStartAtTheEndsOfTheSafeIntegers(t *testing.T) {
	store := memoryStore(t)
	storage := value(Open(background, store, "harness"))
	// Ids at both ends of what a scan reaches: 0 (below every id Durable mints) and the largest safe integer less one.
	const high = 1<<53 - 2
	value(commitWrites(storage,
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: 0}},
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: high}},
	))
	ascending, descending := durable.ScanAscending, durable.ScanDescending
	up := value(storage.ScanConversations(background, durable.ConversationQuery{Order: &ascending}, 10, nil))
	down := value(storage.ScanConversations(background, durable.ConversationQuery{Order: &descending}, 10, nil))
	if len(up.Items) != 2 || up.Items[0].Id != 0 || up.Items[1].Id != high {
		t.Fatalf("ascending scan = %+v, want conversations 0 and %d", up.Items, int64(high))
	}
	if len(down.Items) != 2 || down.Items[0].Id != high || down.Items[1].Id != 0 {
		t.Fatalf("descending scan = %+v, want conversations %d and 0", down.Items, int64(high))
	}
	must(t, store.Close(background))
}

// sessionDocuments commits count keyed documents of one kind in the session scope and returns their ids in order.
func sessionDocuments(t *testing.T, storage *StoreStorage, count int) []durable.DocumentId {
	t.Helper()
	var ids []durable.DocumentId
	for range count {
		id := mintID[durable.DocumentId](t, storage)
		ids = append(ids, id)
		key := fmt.Sprint(id)
		value(commitWrites(storage, durable.DocumentCreateWrite{
			Record:  durable.DocumentCreate{Id: id, Kind: "k", Key: &key, Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}},
			Content: base(1),
		}))
	}
	return ids
}
