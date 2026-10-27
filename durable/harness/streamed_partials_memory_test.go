// Ports the two cases of harness-view-run.test.ts "streamed partials" (Pi #9265) that measure memory: a cost per commit that follows the
// new text, and a watcher's retained memory. Pi measures V8's heap (GCProfiler, getHeapStatistics); these measure Go's: the bytes
// the process allocates (runtime.MemStats.TotalAlloc, which collections do not reduce) and the heap in use after a full collection.
//
// Go mappings forced by language mechanics. A Go string is flat, so extending a streamed text by one token copies the text once per
// commit where V8 concatenates in O(1): the allocation per commit grows with the text in Pi's regression and here alike, and
// the bound is on what the copies are of. Each commit may copy a growing string a constant number of times, so the bytes per commit
// per byte of the strings stay flat as the strings grow, where comparing, re-encoding or resending the whole message per commit
// makes them grow with the commits. A watcher that records frames holds each frame's value, which Go does not share between
// revisions either, so the watcher records the operations of its frames (a substring of a growing text would pin each revision's
// array: the regression the case protects) and keeps its latest value.

package harness

import (
	"context"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// streamedTextAndCall answers with a text of size bytes and a tool call whose content argument is as long, streamed in 64-token
// chunks.
func streamedTextAndCall(t *testing.T, size int, tokens int, tokensPerSecond int) *chatState {
	t.Helper()
	setup := chatSetup(t, ai.FauxConfig{TokensPerSecond: tokensPerSecond, TokenSize: &ai.FauxTokenSize{Min: new(tokens), Max: new(tokens)}})
	setup.SetSettings(func(settings *HarnessSettings) {
		settings.Progress = &ProgressPolicyPatch{PartialIntervalMs: new(1.0), OutputIntervalMs: new(100.0)}
		// Keep pi.live cached between commits, as the default retention does: evicted after each use, every commit replays the
		// document's delta chain, a cost of eviction, not of appending.
		settings.DocumentRetentionMs = new(60_000.0)
	})
	text := strings.Repeat("streamed text with \"quotes\" and \\ escapes\n", size/40)
	setup.Faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text), ai.FauxToolCall("missing", map[string]any{"path": "a.ts", "content": text}, &ai.FauxToolCallOptions{ID: "c1"})}, StopReason: "toolUse"}),
		fauxAnswer("done"),
	})
	return setup
}

// harness-view-run.test.ts:567 "commits a growing text and tool call at a cost that follows the new text, not the string lengths".
func TestStreamedPartialsCommitAtACostThatFollowsTheNewText(t *testing.T) {
	// stream returns the bytes allocated while streaming one answer of size characters of text and as much tool-call content, and the
	// commits made.
	stream := func(size int) (allocated uint64, commits int) {
		setup := streamedTextAndCall(t, size, 64, 64_000)
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		var mu sync.Mutex
		observeCommits(harness, func(context.Context, session.ObservedCommit) {
			mu.Lock()
			commits++
			mu.Unlock()
		})
		submission := submit(t, root, inputDraft("go"))
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		must(submission.Wait(testContext))
		runtime.ReadMemStats(&after)
		closeHarness(t, harness)
		mu.Lock()
		defer mu.Unlock()
		return after.TotalAlloc - before.TotalAlloc, commits
	}
	smallBytes, smallCommits := stream(40_000)
	largeBytes, largeCommits := stream(400_000)
	// Pi's faux stream commits a partial per token, hundreds of times more for the larger text; a Go run commits the newest partial once
	// per interval, so the commits of the two streams are compared as they come.
	if smallCommits < 20 || largeCommits < smallCommits {
		t.Fatalf("%d commits for the large stream, %d for the small one: want at least 20 and not fewer for the larger text", largeCommits, smallCommits)
	}
	perCommit := func(bytes uint64, commits, size int) float64 {
		return float64(bytes) / float64(commits) / float64(size)
	}
	small, large := perCommit(smallBytes, smallCommits, 40_000), perCommit(largeBytes, largeCommits, 400_000)
	t.Logf("small: %d bytes over %d commits, %.2f per commit per byte of text; large: %d bytes over %d commits, %.2f", smallBytes, smallCommits, small, largeBytes, largeCommits, large)
	// Per commit and per byte of the strings, the cost stays flat: comparing the whole strings on every commit made it grow with their
	// length.
	if large/small >= 2 {
		t.Fatalf("the cost per commit per byte of text grew %.1fx from 40 KB to 400 KB", large/small)
	}
}

// heapInUse is the heap in use after a full collection.
func heapInUse(harness Harness) uint64 {
	for range 3 {
		drained(harness)
		runtime.GC()
	}
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// opsWatcher is a started watch of a conversation that keeps its latest value and the operations of its frames, not their values.
type opsWatcher struct {
	mu     sync.Mutex
	frames int
	ops    [][]durable.Op
}

func (watcher *opsWatcher) count() int {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	return watcher.frames
}

// harness-view-run.test.ts:603 "keeps a watcher's memory proportional to a long streamed text and tool call, not to their commits".
func TestStreamedPartialsKeepAWatchersMemoryProportionalToTheText(t *testing.T) {
	setup := chatSetup(t, ai.FauxConfig{TokensPerSecond: 16_000, TokenSize: &ai.FauxTokenSize{Min: new(16), Max: new(16)}})
	setup.SetSettings(func(settings *HarnessSettings) {
		settings.Progress = &ProgressPolicyPatch{PartialIntervalMs: new(1.0), OutputIntervalMs: new(100.0)}
	})
	var builder strings.Builder
	for index := range 2_000 {
		builder.WriteString("line " + strconv.Itoa(index) + ": some streamed source text with \"quotes\" and \\ escapes\n")
	}
	text := builder.String()
	setup.Faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text), ai.FauxToolCall("missing", map[string]any{"path": "a.ts", "content": text}, &ai.FauxToolCallOptions{ID: "c1"})}, StopReason: "toolUse"}),
		fauxAnswer("done"),
	})
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	watch := must(root.Watch(testContext, nil))
	watcher := &opsWatcher{}
	watch.Start(func(_ context.Context, change chord.WatchChange[ConversationView]) error {
		watcher.mu.Lock()
		defer watcher.mu.Unlock()
		watcher.frames++
		watcher.ops = append(watcher.ops, change.Ops)
		return nil
	})
	before := heapInUse(harness)
	submission := submit(t, root, inputDraft("go"))
	// shown is the length of the streamed block at index in the committed partial.
	shown := func(index int) int {
		generation := liveOf(t, harness, root.Id()).Generation
		if generation == nil || generation.Message == nil || index >= len(generation.Message.Content) {
			return 0
		}
		switch block := generation.Message.Content[index].(type) {
		case ai.TextContent:
			return len(block.Text)
		case ai.ToolCall:
			content, _ := block.Arguments["content"].(string)
			return len(content)
		}
		return 0
	}
	var retained []uint64
	// Measure while the text, then the tool call, is almost complete: the watcher's view holds the most then.
	waitFor(t, func() bool { return float64(shown(0)) > float64(len(text))*0.95 }, 60*time.Second)
	retained = append(retained, heapInUse(harness)-before)
	waitFor(t, func() bool { return float64(shown(1)) > float64(len(text))*0.95 }, 60*time.Second)
	retained = append(retained, heapInUse(harness)-before)
	must(submission.Wait(testContext))
	if commits := watcher.count(); commits <= 500 {
		t.Fatalf("%d frames, want more than 500", commits)
	}
	// The two 140 KB strings, the watcher's recorded operations and the rest of the stream: a few MB. Operations that held a slice of the
	// text per commit made the watcher's memory well over 100 MB.
	for _, bytes := range retained {
		if bytes >= 20_000_000 {
			t.Fatalf("the watcher retains %d bytes, want less than 20 MB (measures %v)", bytes, retained)
		}
	}
	must(watch.Stop())
	closeHarness(t, harness)
}
