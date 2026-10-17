package storage_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
)

// STORAGE_WRITE_BENCHMARKS (packages/durable/src/testing/storage-benchmark.ts:409-470) measure writes, so each must really write what it names on a storage seeded by
// seedStorageWriteBenchmark (:391-407: the root conversation and 100 `benchmark.baseline` entries): "commit one entry" one `benchmark.write` entry carrying 128 "x"
// characters (:411-430), "commit 100 entries" a hundred of them each with its index (:432-452), and "commit mixed entry/task/submission/document" a `benchmark.mixed` entry, a
// task, a done write submission that points at the entry and a session document whose base holds the entry and task ids (:454-470). A StorageWriteBenchmark whose
// run only counted would measure nothing.
// mutation-checked: a run that does not commit, writes another kind, shortens the 128-character text, leaves out the submission or changes the document kind fails it
func TestStorageWriteBenchmarksWriteWhatTheyName(t *testing.T) {
	ctx := benchmarkContext
	root := durable.ROOT_CONVERSATION_ID
	entriesOfKind := func(storage durable.Storage, kind string) []durable.EntryRecord {
		t.Helper()
		page, err := storage.ScanEntries(ctx, durable.EntryQuery{ConversationId: root}, 1000, nil)
		if err != nil {
			t.Fatal(err)
		}
		var matching []durable.EntryRecord
		for _, summary := range page.Items {
			if summary.Kind != kind {
				continue
			}
			full, err := storage.Entry(ctx, summary.Id)
			if err != nil || full == nil {
				t.Fatalf("entry %d = %+v, %v", summary.Id, full, err)
			}
			matching = append(matching, full.Entry)
		}
		return matching
	}
	for _, backend := range storageBenchmarkBackends {
		for _, scenario := range durabletest.STORAGE_WRITE_BENCHMARKS {
			t.Run(backend+"/"+scenario.Name, func(t *testing.T) {
				fixture := createWriteFixture(t, backend)
				if baseline := entriesOfKind(fixture, "benchmark.baseline"); len(baseline) != 100 {
					t.Fatalf("seeded baseline entries = %d, want 100", len(baseline))
				}
				if got, err := scenario.Run(ctx, fixture); err != nil || got != scenario.Expected {
					t.Fatalf("run = %d, %v; want %d", got, err, scenario.Expected)
				}
				switch scenario.Name {
				case "commit one entry", "commit 100 entries":
					written := entriesOfKind(fixture, "benchmark.write")
					if int64(len(written)) != scenario.Expected {
						t.Fatalf("benchmark.write entries = %d, want %d", len(written), scenario.Expected)
					}
					indexes := map[float64]bool{}
					for _, entry := range written {
						var data map[string]any
						if encoded, err := json.Marshal(entry.Data); err != nil || json.Unmarshal(encoded, &data) != nil {
							t.Fatalf("entry data %#v is not a JSON object", entry.Data)
						}
						if text, _ := data["text"].(string); text != strings.Repeat("x", 128) {
							t.Fatalf("entry text = %q, want 128 x", data["text"])
						}
						if index, ok := data["index"].(float64); ok {
							indexes[index] = true
						}
					}
					if scenario.Expected == 100 && len(indexes) != 100 {
						t.Fatalf("distinct entry indexes = %d, want 100", len(indexes))
					}
				default:
					mixed := entriesOfKind(fixture, "benchmark.mixed")
					if len(mixed) != 1 {
						t.Fatalf("benchmark.mixed entries = %d, want 1", len(mixed))
					}
					tasks, err := fixture.ScanTasks(ctx, durable.TaskQuery{ConversationId: &root}, 10, nil)
					if err != nil || len(tasks.Items) != 1 {
						t.Fatalf("tasks = %+v, %v; want one", tasks, err)
					}
					submissions, err := fixture.ScanSubmissions(ctx, durable.SubmissionQuery{ConversationId: &root}, 10, nil)
					if err != nil || len(submissions.Items) != 1 {
						t.Fatalf("submissions = %+v, %v; want one", submissions, err)
					}
					submission := submissions.Items[0]
					if submission.Type != durable.SubmissionTypeWrite || submission.Status != durable.SubmissionDone || submission.Entry == nil || *submission.Entry != mixed[0].Id ||
						submission.RequestId == nil || *submission.RequestId != fmt.Sprintf("benchmark-%d", submission.Id) {
						t.Fatalf("submission = %+v, want a done write of entry %d with its benchmark request id", submission, mixed[0].Id)
					}
					documents, err := fixture.ScanDocuments(ctx, durable.DocumentQuery{Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}, At: durable.CurrentPoint}, 10, nil)
					if err != nil || len(documents.Items) != 1 || documents.Items[0].Kind != "benchmark.mixed" {
						t.Fatalf("documents = %+v, %v; want one benchmark.mixed", documents, err)
					}
				}
			})
		}
	}
}
