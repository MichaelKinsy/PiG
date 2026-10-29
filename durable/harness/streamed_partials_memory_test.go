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
//
// The watcher case streams its response itself, one chunk per commit. Pi's faux stream emits a chunk a millisecond and the case waits for
// more than 500 frames, which is what makes its bound on the watcher's memory a test of proportionality to the commits: an
// implementation that keeps something of each commit retains, over 500 commits of a text that is up to 140 KB long, more than the
// bound. V8 commits in microtasks, so a commit never lasts longer than a chunk. A Go commit decodes the whole document to check it, and
// under the race detector or on a loaded machine lasts many chunks, which then share one commit: how many commits a run makes is the
// speed of the machine, not the invariant (memory proportional to the text, however many commits it takes), and a bound on the
// frames, or a wait for the stream to pass a fraction of its text, fails when the machine is slow. Here a chunk is pushed only after a
// frame of the watcher showed the chunk before it, so every chunk has a commit and a frame of its own however slowly the process runs,
// and the memory is measured while the stream waits at the point where the watcher's view holds the most. Pi's 500 frames become what
// they stand for: the text the frames showed, in all, is more than the bound by half again, so that what a watcher retains of every
// frame (a copy of the text it showed, or a slice of the array that holds it) would break the bound.

package harness

import (
	"context"
	"encoding/json"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

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

// opsWatcher is a started watch of a conversation that keeps the operations of its frames, not their values, and the longest text and
// tool-call content its frames showed.
type opsWatcher struct {
	mu     sync.Mutex
	frames int
	ops    [][]durable.Op
	// shown is the longest text of the block at content index 0, and the longest content argument of the block at index 1, a frame showed.
	shown [2]int
	// viewed is the bytes of text and tool-call content the frames showed, in all.
	viewed int
	// changed is closed by the next frame.
	changed chan struct{}
}

func newOpsWatcher() *opsWatcher { return &opsWatcher{changed: make(chan struct{})} }

// advance is the watch's listener.
func (watcher *opsWatcher) advance(_ context.Context, change chord.WatchChange[ConversationView]) error {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	watcher.frames++
	watcher.ops = append(watcher.ops, change.Ops)
	for index := range watcher.shown {
		here := shownIn(change.Value, index)
		watcher.viewed += here
		watcher.shown[index] = max(watcher.shown[index], here)
	}
	close(watcher.changed)
	watcher.changed = make(chan struct{})
	return nil
}

func (watcher *opsWatcher) count() (frames, viewed int) {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	return watcher.frames, watcher.viewed
}

// awaitShown waits until a frame showed at least length bytes at content index, and reports false when ctx ends first.
func (watcher *opsWatcher) awaitShown(ctx context.Context, index, length int) bool {
	for {
		watcher.mu.Lock()
		reached, changed := watcher.shown[index] >= length, watcher.changed
		watcher.mu.Unlock()
		if reached {
			return true
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return false
		}
	}
}

func memberOf(object durable.JsonObject, key string) any {
	if object == nil {
		return nil
	}
	return object.Value(key)
}

// shownIn is the length of the text at content index of the partial message that view's pi.live holds, or of that tool call's content
// argument; 0 when the view shows none.
func shownIn(view ConversationView, index int) int {
	live, _ := view.Docs.Get("pi.live")
	generation, _ := memberOf(live, "generation").(durable.JsonObject)
	message, _ := memberOf(generation, "message").(durable.JsonObject)
	content, _ := memberOf(message, "content").([]any)
	if index >= len(content) {
		return 0
	}
	block, _ := content[index].(durable.JsonObject)
	if text, ok := memberOf(block, "text").(string); ok {
		return len(text)
	}
	arguments, _ := memberOf(block, "arguments").(durable.JsonObject)
	argument, _ := memberOf(arguments, "content").(string)
	return len(argument)
}

// retainedLimit is the bytes a watcher may retain of the stream, as Pi's case bounds it.
const retainedLimit = 20_000_000

// answerChunks is the number of chunks each of the answer's two blocks arrives in. The frames then show, in all, about 260 KB for each
// chunk of a block (the text of the first block grows to 130 KB, and the second shows it whole with the content argument growing to
// as much), which is more than the bound by half again.
const answerChunks = 130

// chunkedAnswer is a response of a text and a tool call whose content argument is the same text, streamed as a provider streams it: a
// delta event with the partial message it leaves for each chunk of each block. A chunk is pushed only after a frame of the watcher showed
// the chunk before it, so the stream is as fast as its commits and no chunk shares a commit.
type chunkedAnswer struct {
	text    string
	watcher *opsWatcher
	// nearlyComplete receives once for each block when the watcher showed 95% of the block's text, and the stream waits there until
	// proceed is sent to. ended is closed when the stream stops pushing.
	nearlyComplete, proceed, ended chan struct{}
	chunks                         atomic.Int32
}

// stream starts streaming the answer for a request that ctx covers.
func (answer *chunkedAnswer) stream(ctx context.Context) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream()
	go answer.run(ctx, stream)
	return stream
}

func (answer *chunkedAnswer) run(ctx context.Context, stream *ai.AssistantMessageEventStream) {
	defer close(answer.ended)
	text := answer.text
	message := func(content ...ai.AssistantContentBlock) *ai.AssistantMessage {
		partial := fauxMessage("", ai.StopReasonStop)
		partial.Content = content
		return partial
	}
	call := func(content string) ai.ToolCall {
		return ai.ToolCall{ID: "c1", Name: "missing", Arguments: ai.JsonObject{"path": "a.ts", "content": content}}
	}
	// block pushes the chunks of the text at content index, with push called for each, after the previous chunk's frame; it reports false
	// when the request ended meanwhile.
	block := func(index int, push func(start, end int)) bool {
		size := (len(text) + answerChunks - 1) / answerChunks
		nearly := false
		for start := 0; start < len(text); start += size {
			end := min(start+size, len(text))
			push(start, end)
			answer.chunks.Add(1)
			if !answer.watcher.awaitShown(ctx, index, end) {
				return false
			}
			if nearly || end*100 < len(text)*95 {
				continue
			}
			nearly = true
			select {
			case answer.nearlyComplete <- struct{}{}:
			case <-ctx.Done():
				return false
			}
			select {
			case <-answer.proceed:
			case <-ctx.Done():
				return false
			}
		}
		return true
	}
	_ = stream.Push(ai.StartEvent{Partial: message()})
	_ = stream.Push(ai.TextStartEvent{ContentIndex: 0, Partial: message(ai.TextContent{})})
	if !block(0, func(start, end int) {
		_ = stream.Push(ai.TextDeltaEvent{ContentIndex: 0, Delta: text[start:end], Partial: message(ai.TextContent{Text: text[:end]})})
	}) {
		return
	}
	_ = stream.Push(ai.TextEndEvent{ContentIndex: 0, Content: text, Partial: message(ai.TextContent{Text: text})})
	_ = stream.Push(ai.ToolCallStartEvent{ContentIndex: 1, Partial: message(ai.TextContent{Text: text}, ai.ToolCall{ID: "c1", Name: "missing", Arguments: ai.JsonObject{}})})
	if !block(1, func(start, end int) {
		// The argument text of the chunk: the content string's characters, and the object's beginning and end around the first and last.
		encoded := must(json.Marshal(text[start:end]))
		delta := string(encoded[1 : len(encoded)-1])
		if start == 0 {
			delta = `{"path":"a.ts","content":"` + delta
		}
		if end == len(text) {
			delta += `"}`
		}
		_ = stream.Push(ai.ToolCallDeltaEvent{ContentIndex: 1, Delta: delta, Partial: message(ai.TextContent{Text: text}, call(text[:end]))})
	}) {
		return
	}
	_ = stream.Push(ai.ToolCallEndEvent{ContentIndex: 1, ToolCall: call(text), Partial: message(ai.TextContent{Text: text}, call(text))})
	final := message(ai.TextContent{Text: text}, call(text))
	final.StopReason = ai.StopReasonToolUse
	stream.End(final)
}

// harness-view-run.test.ts:603 "keeps a watcher's memory proportional to a long streamed text and tool call, not to their commits".
func TestStreamedPartialsKeepAWatchersMemoryProportionalToTheText(t *testing.T) {
	setup := chatSetup(t)
	setup.SetSettings(func(settings *HarnessSettings) {
		settings.Progress = &ProgressPolicyPatch{PartialIntervalMs: new(1.0), OutputIntervalMs: new(100.0)}
	})
	var builder strings.Builder
	for index := range 2_000 {
		builder.WriteString("line " + strconv.Itoa(index) + ": some streamed source text with \"quotes\" and \\ escapes\n")
	}
	watcher := newOpsWatcher()
	answer := &chunkedAnswer{text: builder.String(), watcher: watcher, nearlyComplete: make(chan struct{}), proceed: make(chan struct{}), ended: make(chan struct{})}
	// The faux provider answers the request after the tool call's round.
	setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done")})
	withProvider(setup, func(provider *ai.ModelsProvider) {
		faux := provider.StreamSimple
		var streamed sync.Once
		provider.StreamSimple = func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			var stream *ai.AssistantMessageEventStream
			streamed.Do(func() { stream = answer.stream(ctx) })
			if stream != nil {
				return stream, nil
			}
			return faux(ctx, model, transcript, options)
		}
	})
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	watch := must(root.Watch(testContext, nil))
	watch.Start(watcher.advance)
	before := heapInUse(harness)
	submission := submit(t, root, inputDraft("go"))
	// The heap before the stream may hold garbage the later collections free, so the difference is signed.
	var retained []int64
	// Measure while the text, then the tool call, is almost complete: the watcher's view holds the most then.
	for range 2 {
		select {
		case <-answer.nearlyComplete:
		case <-answer.ended:
			t.Fatal("the stream ended before its block was nearly complete")
		}
		retained = append(retained, int64(heapInUse(harness))-int64(before))
		select {
		case answer.proceed <- struct{}{}:
		case <-answer.ended:
			t.Fatal("the stream ended while it waited for the measure")
		}
	}
	must(submission.Wait(testContext))
	frames, viewed := watcher.count()
	chunks := int(answer.chunks.Load())
	t.Logf("%d frames for %d chunks, showing %d bytes in all; the watcher retained %v", frames, chunks, viewed, retained)
	if frames < chunks {
		t.Fatalf("%d frames for %d chunks, want a frame for every chunk", frames, chunks)
	}
	// Pi's more than 500 frames: enough of the text shown that a watcher retaining something of every frame would break the bound.
	if viewed < retainedLimit*3/2 {
		t.Fatalf("the frames showed %d bytes in all, want at least %d, so that retaining something of each frame breaks the bound", viewed, retainedLimit*3/2)
	}
	// The two 140 KB strings, the watcher's recorded operations and the rest of the stream: a few MB. Operations that held a slice of the
	// text per commit made the watcher's memory well over 100 MB.
	for _, bytes := range retained {
		if bytes >= retainedLimit {
			t.Fatalf("the watcher retains %d bytes, want less than 20 MB (measures %v)", bytes, retained)
		}
	}
	must(watch.Stop())
	closeHarness(t, harness)
}
