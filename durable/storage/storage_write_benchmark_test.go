package storage_test

// Ports packages/durable/src/testing/storage-benchmark.ts:384-502 (seedStorageWriteBenchmark and STORAGE_WRITE_BENCHMARKS).

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

func scanAllEntries(t *testing.T, store durable.Storage) []durable.EntryRecord {
	t.Helper()
	ascending := durable.ScanAscending
	page, err := store.ScanEntries(benchmarkContext, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID, Order: &ascending}, 1000, nil)
	if err != nil {
		t.Fatal(err)
	}
	return page.Items
}

// The seed commits the root conversation and 100 `benchmark.baseline` entries carrying their index (:391-407), and each write benchmark returns
// the count it declares and persists exactly the records Pi's run() commits.
func TestStorageWriteBenchmarksSeedAndCommitPisRecords(t *testing.T) {
	var names []string
	var expected []int64
	for _, benchmark := range durabletest.STORAGE_WRITE_BENCHMARKS {
		names = append(names, benchmark.Name)
		expected = append(expected, benchmark.Expected)
	}
	if want := []string{"commit one entry", "commit 100 entries", "commit mixed entry/task/submission/document"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("benchmark names = %q, want %q", names, want)
	}
	if want := []int64{1, 100, 4}; !reflect.DeepEqual(expected, want) {
		t.Fatalf("benchmark expected counts = %v, want %v", expected, want)
	}

	fresh := func() durable.Storage {
		store := storage.NewMemoryStorage()
		t.Cleanup(func() { _ = store.Close(benchmarkContext) })
		if err := durabletest.SeedStorageWriteBenchmark(benchmarkContext, store); err != nil {
			t.Fatal(err)
		}
		return store
	}
	run := func(store durable.Storage, index int) {
		t.Helper()
		benchmark := durabletest.STORAGE_WRITE_BENCHMARKS[index]
		got, err := benchmark.Run(benchmarkContext, store)
		if err != nil {
			t.Fatal(err)
		}
		if got != benchmark.Expected {
			t.Fatalf("%s returned %d, want %d", benchmark.Name, got, benchmark.Expected)
		}
	}

	seeded := scanAllEntries(t, fresh())
	if len(seeded) != 100 {
		t.Fatalf("seed wrote %d entries, want 100", len(seeded))
	}
	for index, entry := range seeded {
		if entry.Kind != "benchmark.baseline" || entry.ConversationId != durable.ROOT_CONVERSATION_ID || !reflect.DeepEqual(entry.Data, map[string]any{"index": index}) {
			t.Fatalf("seed entry %d = %+v, want benchmark.baseline in the root conversation with data {index: %d}", index, entry, index)
		}
	}
	text := strings.Repeat("x", 128)

	t.Run("commit one entry", func(t *testing.T) {
		store := fresh()
		run(store, 0)
		entries := scanAllEntries(t, store)
		if len(entries) != 101 {
			t.Fatalf("%d entries, want 101", len(entries))
		}
		if last := entries[100]; last.Kind != "benchmark.write" || !reflect.DeepEqual(last.Data, map[string]any{"text": text}) {
			t.Fatalf("written entry = %+v, want benchmark.write with a 128-character text", last)
		}
	})
	t.Run("commit 100 entries", func(t *testing.T) {
		store := fresh()
		run(store, 1)
		entries := scanAllEntries(t, store)
		if len(entries) != 200 {
			t.Fatalf("%d entries, want 200", len(entries))
		}
		for index, entry := range entries[100:] {
			if entry.Kind != "benchmark.write" || !reflect.DeepEqual(entry.Data, map[string]any{"index": index, "text": text}) {
				t.Fatalf("written entry %d = %+v, want benchmark.write with data {index: %d, text}", index, entry, index)
			}
		}
	})
	t.Run("commit mixed entry/task/submission/document", func(t *testing.T) {
		store := fresh()
		run(store, 2)
		entries := scanAllEntries(t, store)
		if len(entries) != 101 {
			t.Fatalf("%d entries, want 101", len(entries))
		}
		entry := entries[100]
		if entry.Kind != "benchmark.mixed" || entry.Data != nil {
			t.Fatalf("mixed entry = %+v, want benchmark.mixed without data", entry)
		}

		tasks, err := store.ScanTasks(benchmarkContext, durable.TaskQuery{}, 10, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(tasks.Items) != 1 {
			t.Fatalf("%d tasks, want 1", len(tasks.Items))
		}
		task := tasks.Items[0]
		index := int(task.Id)
		wantKind := "benchmark.other"
		if index%4 == 0 {
			wantKind = "benchmark.filtered"
		}
		wantStatus := []durable.TaskStatus{durable.TaskPending, durable.TaskRunning, durable.TaskTerminal}[index%3]
		if task.ConversationId != durable.ROOT_CONVERSATION_ID || task.Kind != wantKind || task.Version != 1 || task.State.Status != wantStatus ||
			task.Background != (index%5 == 0) || task.AbortRequested != (index%7 == 0) || !reflect.DeepEqual(task.Input, map[string]any{"index": index}) {
			t.Fatalf("mixed task = %+v, want task(id, id): kind %s, status %s, input {index: %d}", task, wantKind, wantStatus, index)
		}

		submissions, err := store.ScanSubmissions(benchmarkContext, durable.SubmissionQuery{}, 10, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(submissions.Items) != 1 {
			t.Fatalf("%d submissions, want 1", len(submissions.Items))
		}
		submission := submissions.Items[0]
		if submission.RequestId == nil || *submission.RequestId != "benchmark-"+itoa(int64(submission.Id)) || submission.ConversationId != durable.ROOT_CONVERSATION_ID ||
			submission.Type != durable.SubmissionTypeWrite || submission.Status != durable.SubmissionDone || submission.Entry == nil || *submission.Entry != entry.Id {
			t.Fatalf("mixed submission = %+v, want request benchmark-<id>, type write, status done, entry %d", submission, entry.Id)
		}

		documents, err := store.ScanDocuments(benchmarkContext, durable.DocumentQuery{Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}, At: durable.CurrentPoint}, 10, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(documents.Items) != 1 {
			t.Fatalf("%d documents, want 1", len(documents.Items))
		}
		record := documents.Items[0]
		if record.Kind != "benchmark.mixed" || record.Key == nil || *record.Key != itoa(int64(record.Id)) {
			t.Fatalf("mixed document = %+v, want kind benchmark.mixed keyed by its id", record)
		}
		stored, err := store.Document(benchmarkContext, record.Id, durable.CurrentPoint)
		if err != nil || stored == nil {
			t.Fatalf("document read = %v, %v", stored, err)
		}
		gotJSON, err := json.Marshal(stored.Value)
		if err != nil {
			t.Fatal(err)
		}
		if want := `{"entryId":` + itoa(int64(entry.Id)) + `,"taskId":` + itoa(int64(task.Id)) + `}`; string(gotJSON) != want {
			t.Fatalf("mixed document content = %s, want %s", gotJSON, want)
		}
	})
}

func itoa(value int64) string { return strconv.FormatInt(value, 10) }
