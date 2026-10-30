// Ports packages/durable/test/conversation-view-bench.ts: a long conversation (thousands of entries) and the watches of its view, on
// SQLite so history stays out of the heap. Pi runs each scenario in a child process to isolate its heap numbers; a Go benchmark
// reads the heap after a full collection, so the scenarios run in one process, one sub-benchmark each. Run:
//
//	go test ./durable/harness -run '^$' -bench ConversationView -benchtime 1x
//
// Scenarios: start (acquire a watch of a conversation of N entries and stop it), resume (a watch with `from` 20 entries back, as a
// client that reconnects after a short drop), append (K started watches; commit entries one per commit, or in bursts of 50 per
// commit, and wait for delivery), stream (K started watches; commits that change only pi.live, a streamed answer, no entries) and
// long-lived (one started watch while 20,000 entries are appended: heap after a collection, and whether the first entry delivered is
// still reachable).

package harness

import (
	"context"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/durable"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

var viewBenchText = strings.Repeat("The quick brown fox jumps over the lazy dog. ", 10)

// viewBenchRoot opens a Harness over a SQLite file holding a root conversation of entries notes.
func viewBenchRoot(b *testing.B, entries int) (Harness, Conversation) {
	b.Helper()
	store, err := sqlitenode.OpenNodeSqliteStorage(filepath.Join(b.TempDir(), "session.sqlite"), sqlitenode.NodeSqliteStorageOptions{})
	if err != nil {
		b.Fatal(err)
	}
	harness, err := OpenHarness(context.Background(), store, HarnessOptions{Models: ai.CreateModels(), Registry: CreateRegistry()})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = harness.Close(context.Background()) })
	root, err := harness.Root(context.Background(), nil)
	if err != nil {
		b.Fatal(err)
	}
	viewBenchAppend(b, root, entries, 500)
	return harness, root
}

// viewBenchAppend commits count notes, perCommit to a commit.
func viewBenchAppend(b *testing.B, conversation Conversation, count, perCommit int) []durable.EntryId {
	b.Helper()
	var last []durable.EntryId
	for done := 0; done < count; done += perCommit {
		appended, err := conversation.Commit(context.Background(), func(tx durable.Tx) (any, error) {
			var ids []durable.EntryId
			for index := done; index < min(count, done+perCommit); index++ {
				entry, err := tx.AppendEntry(conversation.Id(), durable.EntryDraft{Kind: "note", Data: map[string]any{"text": strconv.Itoa(index) + " " + viewBenchText}})
				if err != nil {
					return nil, err
				}
				ids = append(ids, entry.Id)
			}
			return ids, nil
		})
		if err != nil {
			b.Fatal(err)
		}
		last = appended.([]durable.EntryId)
	}
	return last
}

func viewBenchNewest(b *testing.B, conversation Conversation) durable.EntryId {
	b.Helper()
	page, err := conversation.Entries(context.Background(), durable.EntryQuery{}, 1, nil)
	if err != nil {
		b.Fatal(err)
	}
	return page.Items[0].Id
}

// viewBenchHeapMiB is the heap in use after a full collection, in MiB.
func viewBenchHeapMiB() float64 {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return float64(stats.HeapAlloc) / (1 << 20)
}

// viewBenchDelivered waits until every watch holds a value whose newest entry is id.
func viewBenchDelivered(b *testing.B, watches []ConversationWatch, id func() durable.EntryId) {
	b.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		target := id()
		all := true
		for _, watch := range watches {
			if entries := watch.Value().Entries; len(entries) == 0 || entries[len(entries)-1].Id != target {
				all = false
			}
		}
		if all {
			return
		}
		if time.Now().After(deadline) {
			b.Fatal("the watches did not deliver the newest entry")
		}
		time.Sleep(time.Millisecond)
	}
}

func viewBenchWatches(b *testing.B, root Conversation, count int, listener func(context.Context, chord.WatchChange[ConversationView]) error) []ConversationWatch {
	b.Helper()
	watches := make([]ConversationWatch, count)
	for index := range watches {
		watch, err := root.Watch(context.Background(), nil)
		if err != nil {
			b.Fatal(err)
		}
		watch.Start(listener)
		watches[index] = watch
	}
	return watches
}

func BenchmarkConversationViewStart(b *testing.B) {
	for _, resume := range []bool{false, true} {
		name := "watch start, 5000 entries"
		if resume {
			name = "resume 20 back, 5000 entries"
		}
		b.Run(name, func(b *testing.B) {
			_, root := viewBenchRoot(b, 5000)
			var window *ConversationWindow
			if resume {
				window = &ConversationWindow{From: new(viewBenchNewest(b, root) - 19)}
			}
			b.ResetTimer()
			var size int
			for range b.N {
				watch, err := root.Watch(context.Background(), window)
				if err != nil {
					b.Fatal(err)
				}
				size = len(watch.Value().Entries)
				if _, err := watch.Stop(); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(size), "windowEntries")
		})
	}
}

func BenchmarkConversationViewAppend(b *testing.B) {
	for _, scenario := range []struct{ watches, burst, commits int }{{1, 1, 2000}, {4, 1, 2000}, {4, 50, 200}} {
		b.Run("append x"+strconv.Itoa(scenario.burst)+" per commit, "+strconv.Itoa(scenario.watches)+" watches", func(b *testing.B) {
			_, root := viewBenchRoot(b, 5000)
			watches := viewBenchWatches(b, root, scenario.watches, func(context.Context, chord.WatchChange[ConversationView]) error { return nil })
			b.ResetTimer()
			started := time.Now()
			var last durable.EntryId
			for range scenario.commits {
				ids := viewBenchAppend(b, root, scenario.burst, scenario.burst)
				last = ids[len(ids)-1]
			}
			viewBenchDelivered(b, watches, func() durable.EntryId { return last })
			b.StopTimer()
			b.ReportMetric(float64(time.Since(started).Microseconds())/float64(scenario.commits), "us/commit")
			b.ReportMetric(float64(len(watches[0].Value().Entries)), "windowEntries")
			b.ReportMetric(viewBenchHeapMiB(), "heapWithWatchesMiB")
			for _, watch := range watches {
				if _, err := watch.Stop(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkConversationViewStream(b *testing.B) {
	const watchCount, commits = 4, 5000
	b.Run("pi.live only, 4 watches", func(b *testing.B) {
		_, root := viewBenchRoot(b, 5000)
		var calls atomic.Int64
		watches := viewBenchWatches(b, root, watchCount, func(context.Context, chord.WatchChange[ConversationView]) error {
			calls.Add(1)
			return nil
		})
		b.ResetTimer()
		started := time.Now()
		for index := range commits {
			if _, err := root.Commit(context.Background(), func(tx durable.Tx) (any, error) {
				live, err := durable.TxDoc(tx, LiveDoc, root.Id())
				if err != nil {
					return nil, err
				}
				return nil, live.Set("compactionFailure", map[string]any{"taskId": 1, "reason": "manual", "error": strconv.Itoa(index)})
			}); err != nil {
				b.Fatal(err)
			}
		}
		// Every watch delivered the final value, not just one of them.
		last := strconv.Itoa(commits - 1)
		deadline := time.Now().Add(2 * time.Minute)
		for {
			done := calls.Load() >= watchCount
			for _, watch := range watches {
				if failure := liveFailure(watch.Value()); failure != last {
					done = false
				}
			}
			if done {
				break
			}
			if time.Now().After(deadline) {
				b.Fatal("the watches did not deliver the last value")
			}
			time.Sleep(time.Millisecond)
		}
		b.StopTimer()
		b.ReportMetric(float64(time.Since(started).Microseconds())/float64(commits), "us/commit")
		for _, watch := range watches {
			if _, err := watch.Stop(); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// liveFailure is the error text of the compaction failure in a view's pi.live document.
func liveFailure(view ConversationView) string {
	document, present := view.Docs.Get(LiveDoc.AnyDefinition().Kind)
	if !present {
		return ""
	}
	failure, _ := document.Value("compactionFailure").(durable.JsonObject)
	if failure == nil {
		return ""
	}
	text, _ := failure.Value("error").(string)
	return text
}

func BenchmarkConversationViewLongLived(b *testing.B) {
	b.Run("long-lived watch, 20000 appends", func(b *testing.B) {
		_, root := viewBenchRoot(b, 20000)
		var mu sync.Mutex
		var first weak.Pointer[durable.EntryRecord]
		seen := false
		watch, err := root.Watch(context.Background(), nil)
		if err != nil {
			b.Fatal(err)
		}
		watch.Start(func(_ context.Context, change chord.WatchChange[ConversationView]) error {
			mu.Lock()
			defer mu.Unlock()
			if entries := change.Value.Entries; !seen && len(entries) > 0 {
				first = weak.Make(&entries[len(entries)-1])
				seen = true
			}
			return nil
		})
		b.ResetTimer()
		viewBenchAppend(b, root, 1, 1)
		id := viewBenchNewest(b, root)
		viewBenchDelivered(b, []ConversationWatch{watch}, func() durable.EntryId { return id })
		for range 20000 {
			viewBenchAppend(b, root, 1, 1)
		}
		last := viewBenchNewest(b, root)
		viewBenchDelivered(b, []ConversationWatch{watch}, func() durable.EntryId { return last })
		b.StopTimer()
		b.ReportMetric(float64(len(watch.Value().Entries)), "windowEntries")
		b.ReportMetric(viewBenchHeapMiB(), "heapWithWatchMiB")
		// A weak pointer's target survives the collection that finds it unreachable only until the next: collect again in a later step.
		time.Sleep(10 * time.Millisecond)
		runtime.GC()
		mu.Lock()
		collected := 0.0
		if first.Value() == nil {
			collected = 1
		}
		mu.Unlock()
		b.ReportMetric(collected, "firstEntryCollected")
		if _, err := watch.Stop(); err != nil {
			b.Fatal(err)
		}
	})
}
