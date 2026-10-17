package storage_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
)

// readRecorder records the conversation each fork-depth read targets.
type readRecorder struct {
	durable.Storage
	scans, heads []durable.ConversationId
}

func (r *readRecorder) ScanEntries(ctx context.Context, query durable.EntryQuery, limit int, cursor durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	r.scans = append(r.scans, query.ConversationId)
	return r.Storage.ScanEntries(ctx, query, limit, cursor)
}

func (r *readRecorder) FindLatestHeadMarker(ctx context.Context, conversationId durable.ConversationId, at *durable.EntryId) (*durable.EntryRecord, error) {
	r.heads = append(r.heads, conversationId)
	return r.Storage.FindLatestHeadMarker(ctx, conversationId, at)
}

// Pi packages/durable/src/testing/storage-benchmark.ts:360-380: the "fork-depth history scan (100)" and "fork-depth head lookup" read benchmarks target dataset.deepestConversationId (the last
// fork of a chain, line 230-257), not the root conversation; against the root they would return the same counts and measure no fork depth.
// mutation-checked: the root conversation id in place of DeepestConversationId in either benchmark fails it.
func TestStorageReadBenchmarkForkDepthScenariosReadTheDeepestConversation(t *testing.T) {
	var forkScan, forkHead durabletest.StorageReadBenchmark
	for _, scenario := range durabletest.STORAGE_READ_BENCHMARKS {
		switch scenario.Name {
		case "fork-depth history scan (100)":
			forkScan = scenario
		case "fork-depth head lookup":
			forkHead = scenario
		}
	}
	if forkScan.Run == nil || forkHead.Run == nil {
		t.Fatal("fork-depth scenarios are missing")
	}
	recorder := &readRecorder{Storage: createFixture(t, "memory")}
	dataset, err := durabletest.SeedStorageBenchmark(benchmarkContext, recorder, durabletest.STORAGE_MEMORY_SCALES[0])
	if err != nil {
		t.Fatal(err)
	}
	if dataset.DeepestConversationId == durable.ROOT_CONVERSATION_ID {
		t.Fatal("the seeded dataset has no fork: the deepest conversation is the root")
	}
	recorder.scans, recorder.heads = nil, nil
	for _, scenario := range []durabletest.StorageReadBenchmark{forkScan, forkHead} {
		got, err := scenario.Run(benchmarkContext, recorder, dataset)
		if err != nil {
			t.Fatalf("%s: %v", scenario.Name, err)
		}
		if want := scenario.Expected(dataset); got != want {
			t.Errorf("%s = %d, want %d", scenario.Name, got, want)
		}
	}
	if len(recorder.scans) != 1 || recorder.scans[0] != dataset.DeepestConversationId {
		t.Errorf("fork-depth scan read conversations %v, want [%d]", recorder.scans, dataset.DeepestConversationId)
	}
	if len(recorder.heads) != 1 || recorder.heads[0] != dataset.DeepestConversationId {
		t.Errorf("fork-depth head lookup read conversations %v, want [%d]", recorder.heads, dataset.DeepestConversationId)
	}
}
