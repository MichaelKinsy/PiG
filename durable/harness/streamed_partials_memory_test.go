// Ports the two cases of harness-view-run.test.ts "streamed partials" (Pi #9265) that measure memory: a cost per commit that follows the
// new text, and a watcher's retained memory. Pi measures V8's heap (GCProfiler, getHeapStatistics); these measure Go's: the bytes
// the process allocates (runtime.MemStats.TotalAlloc, which collections do not reduce) and the heap in use after a full collection.
//
// The cost case has Pi's bounds. Go mapping for the watcher case: a watcher that records frames holds each frame's value, which Go does
// not share between revisions, so the watcher records the operations of its frames (a substring of a growing text would pin each
// revision's array: the regression the case protects) and keeps its latest value.
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
//
// The cost case streams its answer itself too, and counts from before its submission, as Pi's allocatedBytes does: its bound measured
// the machine. The bytes were read after the submission started, behind a collection, so a slow collection (the race detector, a large
// heap, a loaded runner) let the stream run unmeasured meanwhile: a nightly on macOS read 5.6 MB for the 40 KB stream, which allocates
// 15 to 17 MB, and failed the bound at 4.9x; with a 1.5 GB heap on Linux the same reading was 2 KB. And Pi's faux chunks, a millisecond
// apart, share a commit when a commit lasts longer than a chunk, as it does under the race detector. The bytes of a commit are mostly
// those of the chunks it carries, so the ratio between two streams run seconds apart followed the load between them (970 commits for the
// 400 KB stream in that nightly, 1377 in a run on Linux) and not the length of the strings. Here a chunk is pushed only after a commit
// showed the chunk before it, so every commit carries one chunk however slowly the process runs.

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
func streamedTextAndCall(t testing.TB, size int, tokens int, tokensPerSecond int) *chatState {
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

// costChunk is the bytes each of the cost case's two blocks gains in a chunk.
const costChunk = 1_024

// costText is the text of a cost-case stream of size bytes.
func costText(size int) string {
	return strings.Repeat("streamed text with \"quotes\" and \\ escapes\n", size/40)
}

// allocatedBytes is the bytes the process allocates while run runs, counted from a settled process before run starts anything
// (harness-view-run.test.ts allocatedBytes). Collections do not reduce the count: the collection keeps the setup's garbage from being
// collected during the first chunks of the stream.
func allocatedBytes(harness Harness, run func()) uint64 {
	drained(harness)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	run()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// harness-view-run.test.ts:547 "commits a growing text and tool call at a cost that follows the new text, not the string lengths".
func TestStreamedPartialsCommitAtACostThatFollowsTheNewText(t *testing.T) {
	// stream returns what streaming one answer of size characters of text and as much tool-call content allocated, and how many commits
	// and chunks it made.
	type streamed struct {
		bytes           uint64
		commits, chunks int
	}
	stream := func(size int) streamed {
		setup := chatSetup(t)
		setup.SetSettings(func(settings *HarnessSettings) {
			settings.Progress = &ProgressPolicyPatch{PartialIntervalMs: new(1.0), OutputIntervalMs: new(100.0)}
			// Keep pi.live cached between commits, as the default retention does: evicted after each use, every commit replays the
			// document's delta chain, a cost of eviction, not of appending.
			settings.DocumentRetentionMs = new(60_000.0)
		})
		shown := newShownGate()
		answer := &chunkedAnswer{text: costText(size), shown: shown, chunkSize: costChunk, ended: make(chan struct{})}
		answersFirst(setup, answer)
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		var (
			mu      sync.Mutex
			commits int
		)
		followCommits(harness, func(_ context.Context, commit session.ObservedCommit) {
			mu.Lock()
			commits++
			mu.Unlock()
			for _, change := range commit.Changes {
				if document, ok := change.(durable.DocumentChange); ok && document.Record.Kind == "pi.live" {
					shown.show(shownLengths(document.Value))
				}
			}
		})
		allocated := allocatedBytes(harness, func() { must(submit(t, root, inputDraft("go")).Wait(testContext)) })
		closeHarness(t, harness)
		mu.Lock()
		defer mu.Unlock()
		return streamed{allocated, commits, int(answer.chunks.Load())}
	}
	small, large := stream(40_000), stream(400_000)
	t.Logf("small: %d bytes over %d commits for %d chunks; large: %d bytes over %d commits for %d chunks", small.bytes, small.commits, small.chunks, large.bytes, large.commits, large.chunks)
	for _, run := range []streamed{small, large} {
		if run.commits < run.chunks {
			t.Fatalf("%d commits for %d chunks, want a commit for every chunk", run.commits, run.chunks)
		}
	}
	// harness-view-run.test.ts:576.
	if large.commits <= small.commits*5 {
		t.Fatalf("%d commits for the large stream, %d for the small one: want more than 5x", large.commits, small.commits)
	}
	// Per commit, the cost stays flat: comparing the whole strings on every commit made it grow with their length (harness-view-run.test.ts:578).
	if ratio := float64(large.bytes) / float64(large.commits) / (float64(small.bytes) / float64(small.commits)); ratio >= 2 {
		t.Fatalf("the bytes allocated per commit grew %.1fx from 40 KB to 400 KB", ratio)
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

// shownGate is what the frames or commits of a stream showed so far of its two blocks: the longest text of the block at content index 0,
// and the longest content argument of the block at index 1.
type shownGate struct {
	mu    sync.Mutex
	shown [2]int
	// changed is closed by the next show.
	changed chan struct{}
}

func newShownGate() *shownGate { return &shownGate{changed: make(chan struct{})} }

// show records the lengths a frame or commit showed.
func (gate *shownGate) show(lengths [2]int) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	for index, length := range lengths {
		gate.shown[index] = max(gate.shown[index], length)
	}
	close(gate.changed)
	gate.changed = make(chan struct{})
}

// awaitShown waits until a frame or commit showed at least length bytes at content index, and reports false when ctx ends first.
func (gate *shownGate) awaitShown(ctx context.Context, index, length int) bool {
	for {
		gate.mu.Lock()
		reached, changed := gate.shown[index] >= length, gate.changed
		gate.mu.Unlock()
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

// opsWatcher is a started watch of a conversation that keeps the operations of its frames, not their values, and the longest text and
// tool-call content its frames showed.
type opsWatcher struct {
	*shownGate
	mu     sync.Mutex
	frames int
	ops    [][]durable.Op
	// viewed is the bytes of text and tool-call content the frames showed, in all.
	viewed int
}

func newOpsWatcher() *opsWatcher { return &opsWatcher{shownGate: newShownGate()} }

// advance is the watch's listener.
func (watcher *opsWatcher) advance(_ context.Context, change chord.WatchCall[ConversationView]) error {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	watcher.frames++
	watcher.ops = append(watcher.ops, change.Ops)
	live, _ := change.Value.Docs.Get("pi.live")
	lengths := shownLengths(live)
	watcher.viewed += lengths[0] + lengths[1]
	watcher.show(lengths)
	return nil
}

func (watcher *opsWatcher) count() (frames, viewed int) {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	return watcher.frames, watcher.viewed
}

func memberOf(object durable.JsonObject, key string) any {
	if object == nil {
		return nil
	}
	return object.Value(key)
}

// shownLengths is the length of the text at content index 0 of the partial message that live, a pi.live value, holds, and of the content
// argument of the tool call at content index 1; 0 for a block it does not show.
func shownLengths(live durable.JsonObject) [2]int {
	generation, _ := memberOf(live, "generation").(durable.JsonObject)
	message, _ := memberOf(generation, "message").(durable.JsonObject)
	content, _ := memberOf(message, "content").([]any)
	var lengths [2]int
	for index := range lengths {
		if index >= len(content) {
			break
		}
		block, _ := content[index].(durable.JsonObject)
		if text, ok := memberOf(block, "text").(string); ok {
			lengths[index] = len(text)
			continue
		}
		arguments, _ := memberOf(block, "arguments").(durable.JsonObject)
		argument, _ := memberOf(arguments, "content").(string)
		lengths[index] = len(argument)
	}
	return lengths
}

// retainedLimit is the bytes a watcher may retain of the stream, as Pi's case bounds it.
const retainedLimit = 20_000_000

// answerChunks is the number of chunks each of the answer's two blocks arrives in. The frames then show, in all, about 260 KB for each
// chunk of a block (the text of the first block grows to 130 KB, and the second shows it whole with the content argument growing to
// as much), which is more than the bound by half again.
const answerChunks = 130

// chunkedAnswer is a response of a text and a tool call whose content argument is the same text, streamed as a provider streams it: a
// delta event with the partial message it leaves for each chunk of each block. A chunk is pushed only after a frame or commit of the
// shown gate showed the chunk before it, so the stream is as fast as its commits and no chunk shares a commit.
type chunkedAnswer struct {
	text  string
	shown *shownGate
	// chunkSize is the bytes of a block's text in a chunk; zero is answerChunks chunks to a block.
	chunkSize int
	// nearlyComplete, if not nil, receives once for each block when the gate showed 95% of the block's text, and the stream waits there
	// until proceed is sent to. ended is closed when the stream stops pushing.
	nearlyComplete, proceed, ended chan struct{}
	chunks                         atomic.Int32
}

// answersFirst makes the request of setup's provider that comes first answer with answer, and the faux provider answer the ones after it
// (the request after the tool call's round) with "done".
func answersFirst(setup *chatState, answer *chunkedAnswer) {
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
	// block pushes the chunks of the text at content index, with push called for each, after the previous chunk's frame or commit; it
	// reports false when the request ended meanwhile.
	block := func(index int, push func(start, end int)) bool {
		size := answer.chunkSize
		if size == 0 {
			size = (len(text) + answerChunks - 1) / answerChunks
		}
		nearly := false
		for start := 0; start < len(text); start += size {
			end := min(start+size, len(text))
			push(start, end)
			answer.chunks.Add(1)
			if !answer.shown.awaitShown(ctx, index, end) {
				return false
			}
			if answer.nearlyComplete == nil || nearly || end*100 < len(text)*95 {
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
	answer := &chunkedAnswer{text: builder.String(), shown: watcher.shownGate, nearlyComplete: make(chan struct{}), proceed: make(chan struct{}), ended: make(chan struct{})}
	answersFirst(setup, answer)
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	watch := must(root.Watch(testContext, nil))
	watch.Start(onValue(watcher.advance))
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
