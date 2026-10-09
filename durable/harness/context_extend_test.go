package harness

// Ports packages/durable/test/harness-context.test.ts "extends a task's context read with only newer entries".

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage/sqlite"
)

// countingRanges counts the entries the context cache scans from storage, as packages/durable/test/harness-context.test.ts
// countingSetup counts scanned rows.
type countingRanges struct {
	*sqlite.SqliteStorage
	rows atomic.Int64
}

func (storage *countingRanges) VisibleEntryRange(ctx context.Context, query durable.EntryQuery) ([]durable.EntryRecord, error) {
	entries, err := storage.SqliteStorage.VisibleEntryRange(ctx, query)
	storage.rows.Add(int64(len(entries)))
	return entries, err
}

// Pi 1.1.0 harness-context.test.ts "extends a task's context read with only newer entries": a later read in the same
// task reads the same view as a whole read does, from the entries added since.
func TestTaskContextReadIsExtendedWithOnlyNewerEntries(t *testing.T) {
	storage := &countingRanges{SqliteStorage: cacheTestStorage(t)}
	var failures []string
	fail := func(format string, args ...any) { failures = append(failures, fmt.Sprintf(format, args...)) }
	var opened tkOpened
	var firstId durable.EntryId
	reads := tkOneStep("test.context-reads", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
		root := opened.root
		write := func(draft durable.EntryDraft) error {
			return runtime.Commit(ctx, func(tx durable.Tx, _ stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				_, err := tx.AppendEntry(root.Id(), draft)
				return nil, err
			})
		}
		read := func(name string, at *durable.EntryId) (durable.ContextView, int64) {
			before := storage.rows.Load()
			view, err := runtime.Context(ctx, root.Id(), &durable.ContextOptions{At: at})
			if err != nil {
				fail("%s: %v", name, err)
			}
			return view, storage.rows.Load() - before
		}
		whole := func(name string) durable.ContextView {
			view, err := root.Context(ctx, nil)
			if err != nil {
				fail("%s: %v", name, err)
			}
			return view
		}
		same := func(name string, got, want durable.ContextView) {
			if !reflect.DeepEqual(got, want) {
				fail("%s: the extended read differs from a whole read:\n got %+v\nwant %+v", name, got, want)
			}
		}
		initial, _ := read("initial", nil)
		same("initial", initial, whole("initial"))

		// Three new entries. Pi scans 4 rows (a bounds probe and the 3 new ones); the D104 cache reads no probe row.
		for _, draft := range []durable.EntryDraft{
			{Kind: "message", Model: []ai.Message{user("new")}},
			{Kind: "note", Data: map[string]any{"text": "display only"}},
			{Kind: "message", Model: []ai.Message{ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "answer"}}, StopReason: ai.StopReasonStop}}},
		} {
			if err := write(draft); err != nil {
				return err
			}
		}
		extended, rows := read("extended", nil)
		if rows != 3 {
			fail("extended read scanned %d entries, want the 3 new ones", rows)
		}
		same("extended", extended, whole("extended"))

		// An earlier cutoff reuses the range.
		cutoffAt := extended.Entries[len(extended.Entries)-1].Id
		earlier := extended.Entries[len(extended.Entries)-2].Id
		cutoff, rows := read("cutoff", &earlier)
		if rows != 0 {
			fail("a read at an earlier cutoff scanned %d entries, want 0", rows)
		}
		atEarlier, err := root.Context(ctx, &durable.ContextOptions{At: &earlier})
		if err != nil {
			fail("cutoff: %v", err)
		}
		same("cutoff", cutoff, atEarlier)

		// A newer edit of an older entry applies to the extended range.
		if err := write(durable.EntryDraft{Kind: "edit", Edits: []durable.ContextEdit{{Target: firstId, Action: durable.EditOmit}}}); err != nil {
			return err
		}
		edited, _ := read("edited", nil)
		same("edited", edited, whole("edited"))
		if slices.Contains(describeMessages(edited.Messages), "user:first") {
			fail("an omitted entry is still in the context: %v", describeMessages(edited.Messages))
		}
		// An edit committed after the cutoff does not apply to a read at it.
		cutoff, _ = read("cutoff after an edit", &cutoffAt)
		atCutoff, err := root.Context(ctx, &durable.ContextOptions{At: &cutoffAt})
		if err != nil {
			fail("cutoff after an edit: %v", err)
		}
		same("cutoff after an edit", cutoff, atCutoff)
		if !slices.Contains(describeMessages(cutoff.Messages), "user:first") {
			fail("an edit committed after the cutoff applied to a read at it: %v", describeMessages(cutoff.Messages))
		}

		// A new head marker changes the range: read it whole.
		if err := write(durable.EntryDraft{Kind: "reset", HeadSelf: true, Model: []ai.Message{user("fresh")}}); err != nil {
			return err
		}
		reset, _ := read("reset", nil)
		same("reset", reset, whole("reset"))
		if got := describeMessages(reset.Messages); !reflect.DeepEqual(got, []string{"user:fresh"}) {
			fail("after a reset the context is %v, want [user:fresh]", got)
		}
		return tkComplete(ctx, runtime)
	})
	opened = tkOpenRoot(t, []durable.AnyTask{reads}, tkOptions{storage: storage})
	first, err := opened.root.Commit(testContext, func(tx durable.Tx) (any, error) {
		return tx.AppendEntry(opened.root.Id(), durable.EntryDraft{Kind: "message", Model: []ai.Message{user("first")}})
	})
	if err != nil {
		t.Fatal(err)
	}
	firstId = first.(durable.EntryRecord).Id
	opened.harness.Resume()
	tkWaitOutcome(t, opened.harness, tkStart(t, opened.root, reads))
	mustClose(t, opened.harness)
	for _, failure := range failures {
		t.Error(failure)
	}
}
