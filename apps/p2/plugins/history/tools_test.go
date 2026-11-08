package history_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/history"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// Ports the unit cases of test/p2/history-tools.test.ts (Pi main c23b9ec1): reading entries (items, range, tail, around: 0),
// rendering and the output budget. The query-building and SQL-bounds cases (`ftsQuery`, `SearchDb`) and the end-to-end case
// (a compaction and a handoff through the search index) belong to the search plugin: see rows/p2-chattree.md.

func entry(id int64, kind string) durable.EntryRecord {
	return durable.EntryRecord{Id: durable.EntryId(id), ConversationId: 1, Kind: kind}
}

func user(id int64, text string) durable.EntryRecord {
	record := entry(id, "p2.input")
	record.Data = delta.JsonObjectOf(
		"author", delta.JsonObjectOf("kind", "owner", "id", "owner", "name", "Owner"),
		"text", text, "attachments", []any{},
	)
	record.Model = []ai.Message{ai.UserMessage{Content: ai.UserText(text)}}
	return record
}

// fakeTx is a transaction over entries (one conversation), enough for ReadEntries.
type fakeTx struct {
	durable.Tx
	entries []durable.EntryRecord
	head    *durable.EntryId
}

func (f fakeTx) LatestHeadMarker(durable.ConversationId) (*durable.EntryRecord, error) {
	if f.head == nil {
		return nil, nil
	}
	marker := entry(int64(*f.head), "p2.input")
	marker.Head = f.head
	return &marker, nil
}

func (f fakeTx) ScanEntries(query durable.EntryQuery, limit int, cursor durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	var matching []durable.EntryRecord
	for _, each := range f.entries {
		if (query.MinEntryId == nil || each.Id >= *query.MinEntryId) && (query.MaxEntryId == nil || each.Id <= *query.MaxEntryId) {
			matching = append(matching, each)
		}
	}
	ascending := query.Order != nil && *query.Order == durable.ScanAscending
	slices.SortFunc(matching, func(a, b durable.EntryRecord) int {
		if ascending {
			return int(a.Id - b.Id)
		}
		return int(b.Id - a.Id)
	})
	start := 0
	if at, ok := cursor["at"].(float64); ok {
		start = int(at)
	}
	end := min(start+limit, len(matching))
	page := durable.Page[durable.EntryRecord, durable.Cursor]{Items: matching[min(start, end):end]}
	if start+limit < len(matching) {
		next := durable.Cursor{"at": float64(start + limit)}
		page.Next = &next
	}
	return page, nil
}

func ids(records []durable.EntryRecord) []int64 {
	out := []int64{}
	for _, record := range records {
		out = append(out, int64(record.Id))
	}
	return out
}

func equal(t *testing.T, got, want []int64) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestReadsExactlyTheRequestedEntriesNeighboursRangesAndTheTailBeforeABoundary(t *testing.T) {
	var entries []durable.EntryRecord
	for id := int64(1); id <= 8; id++ {
		entries = append(entries, user(id, "message "+string(rune('0'+id))))
	}
	head := durable.EntryId(6)
	tx := fakeTx{entries: entries, head: &head}
	zero, one, three := 0, 1, 3
	// pi-history's slice(-0) bug: before 0 returned every entry instead of none.
	exact, err := history.ReadEntries(tx, 1, history.ReadArgs{Mode: "items", Entries: []int64{3}, Around: &zero})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, ids(exact.Records), []int64{3})
	near, err := history.ReadEntries(tx, 1, history.ReadArgs{Mode: "items", Entries: []int64{3, 99}, Around: &one})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, ids(near.Records), []int64{2, 3, 4})
	if !strings.Contains(near.Note, "#99") {
		t.Fatalf("note %q", near.Note)
	}
	from := int64(2)
	first, err := history.ReadEntries(tx, 1, history.ReadArgs{Mode: "range", From: &from, Limit: &three})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, ids(first.Records), []int64{2, 3, 4})
	if first.Next == "" {
		t.Fatal("no cursor")
	}
	second, err := history.ReadEntries(tx, 1, history.ReadArgs{Mode: "range", From: &from, Limit: &three, Cursor: &first.Next})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, ids(second.Records), []int64{5, 6, 7})
	// The tail ends right before the boundary (a compaction's head or a reset): from there on it is in context.
	tail, err := history.ReadEntries(tx, 1, history.ReadArgs{Mode: "tail", Count: &three})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, ids(tail.Records), []int64{3, 4, 5})
	none, err := history.ReadEntries(fakeTx{entries: entries}, 1, history.ReadArgs{Mode: "tail"})
	if err != nil {
		t.Fatal(err)
	}
	if len(none.Records) != 0 || !strings.Contains(none.Note, "No compaction or handoff") {
		t.Fatalf("none: %+v", none)
	}
	// Arguments the modes need.
	if _, err := history.ReadEntries(tx, 1, history.ReadArgs{Mode: "range"}); err == nil || err.Error() != "range needs from (or a cursor)" {
		t.Fatalf("range without from: %v", err)
	}
	if _, err := history.ReadEntries(tx, 1, history.ReadArgs{Mode: "items"}); err == nil || err.Error() != "items needs entries" {
		t.Fatalf("items without entries: %v", err)
	}
	bad := "e30" // `{}` base64url: an object is a cursor; `[]` is not
	if _, err := history.ReadEntries(tx, 1, history.ReadArgs{Mode: "range", From: &from, Cursor: &bad}); err != nil {
		t.Fatalf("an object cursor: %v", err)
	}
	array := "W10"
	if _, err := history.ReadEntries(tx, 1, history.ReadArgs{Mode: "range", From: &from, Cursor: &array}); err == nil || err.Error() != "Invalid cursor" {
		t.Fatalf("an array cursor: %v", err)
	}
}

func TestRendersAuthorsToolCallsAndPlaceholdersNeverToolResultsAndCutsAtTheBudget(t *testing.T) {
	assistant := entry(2, "pi.assistant")
	assistant.Model = []ai.Message{ai.AssistantMessage{Content: []ai.AssistantContentBlock{
		ai.ThinkingContent{Thinking: "secret"},
		ai.TextContent{Text: "Looking."},
		ai.ToolCall{ID: "c1", Name: "bash", Arguments: ai.JsonObject{"command": "rm -rf x"}},
	}}}
	result := entry(3, "pi.tool-result")
	result.Model = []ai.Message{ai.ToolResultMessage{ToolCallID: "c1", ToolName: "bash", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "OUT"}}}}
	rendered, ok := history.RenderEntry(assistant, map[string]bool{"c1": false})
	if !ok || rendered.Text != "[thinking]\nLooking.\n[tool: bash, done]" {
		t.Fatalf("rendered %+v", rendered)
	}
	encoded, _ := json.Marshal(rendered)
	if strings.Contains(string(encoded), "rm -rf") || strings.Contains(string(encoded), "secret") {
		t.Fatalf("leaked: %s", encoded)
	}
	if _, ok := history.RenderEntry(result, map[string]bool{}); ok {
		t.Fatal("tool results are never returned")
	}
	if failed, _ := history.RenderEntry(assistant, map[string]bool{"c1": true}); !strings.HasSuffix(failed.Text, "[tool: bash, failed]") {
		t.Fatalf("failed call: %q", failed.Text)
	}
	attached := entry(4, "p2.input")
	attached.Data = delta.JsonObjectOf(
		"author", delta.JsonObjectOf("kind", "guest", "id", "g", "name", "Ann"),
		"text", "see", "attachments", []any{delta.JsonObjectOf("id", "u", "name", "a.png")},
	)
	attached.Model = []ai.Message{ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: "see"}, ai.ImageContent{}}}}
	got, ok := history.RenderEntry(attached, map[string]bool{})
	want := history.Entry{Entry: 4, Role: "user", Author: "Ann", Text: "see\n[attachment: a.png]"}
	if !ok || got != want {
		t.Fatalf("attached %+v, want %+v", got, want)
	}
	big := make([]history.Entry, 3)
	for i := range big {
		big[i] = history.Entry{Entry: int64(i + 1), Role: "user", Text: strings.Repeat("x", 30_000)}
	}
	kept, truncated := history.WithinBudget(big, history.ReadBudget)
	if len(kept) != 1 || !truncated {
		t.Fatalf("kept %d truncated %v", len(kept), truncated)
	}
}
