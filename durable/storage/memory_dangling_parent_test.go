package storage_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// pi: packages/durable/src/storage/memory.ts (findLatestHeadMarker, visibleEntries and visibleEntriesAscending read `.parent` of a conversation that is not stored and throw that TypeError; the walks stop earlier when they find their answer first)
func TestMemoryStorageLineageThroughAMissingParentConversationFails(t *testing.T) {
	const dangling = "Cannot read properties of undefined (reading 'parent')"
	ctx := context.Background()
	memory := storage.NewMemoryStorage()
	head := durable.EntryId(5)
	for _, writes := range [][]durable.StorageWrite{
		{durable.ConversationWrite{Value: durable.ConversationRecord{Id: 2, Parent: &durable.ConversationParent{ConversationId: 9, At: 5}}}},
		{durable.EntryWrite{Value: durable.EntryRecord{Id: 3, ConversationId: 2, Kind: "note"}}, durable.EntryWrite{Value: durable.EntryRecord{Id: 5, ConversationId: 9, Kind: "note", Head: &head}}},
	} {
		if _, err := memory.Commit(ctx, writes, func(durable.Seq) {}); err != nil {
			t.Fatal(err)
		}
	}
	expectDangling := func(name string, err error) {
		t.Helper()
		if err == nil || err.Error() != dangling {
			t.Errorf("%s: error %v, want %q", name, err, dangling)
		}
	}
	marker, err := memory.FindLatestHeadMarker(ctx, 2, nil)
	if err != nil || marker == nil || marker.Id != 5 {
		t.Errorf("head marker found in the missing conversation's own entries: %+v, %v", marker, err)
	}
	cutoff := durable.EntryId(4)
	_, err = memory.FindLatestHeadMarker(ctx, 2, &cutoff)
	expectDangling("head marker past the missing conversation", err)
	_, err = memory.VisibleEntry(ctx, 2, 1)
	expectDangling("visible entry", err)
	page, err := memory.ScanEntries(ctx, durable.EntryQuery{ConversationId: 2}, 1, nil)
	if err != nil || len(page.Items) != 1 {
		t.Errorf("descending scan filled before the missing conversation: %+v, %v", page, err)
	}
	_, err = memory.ScanEntries(ctx, durable.EntryQuery{ConversationId: 2}, 5, nil)
	expectDangling("descending scan", err)
	ascending := durable.ScanAscending
	_, err = memory.ScanEntries(ctx, durable.EntryQuery{ConversationId: 2, Order: &ascending}, 1, nil)
	expectDangling("ascending scan", err)
}
