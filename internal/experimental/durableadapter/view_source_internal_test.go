package durableadapter

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

// openRoot opens an in-memory Harness and its root conversation, with a wait for the Session's scheduled deliveries.
func openRoot(t *testing.T) (harness.Conversation, func()) {
	t.Helper()
	opened, err := harness.OpenHarness(t.Context(), storage.NewMemoryStorage(), harness.HarnessOptions{Models: ai.CreateModels(), Registry: harness.CreateRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close(context.Background()) })
	root, err := opened.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return root, opened.(interface{ WaitDeliveries() }).WaitDeliveries
}

func configure(t *testing.T, conversation harness.Conversation, level ai.ModelThinkingLevel) {
	t.Helper()
	if err := conversation.Configure(t.Context(), harness.AgentChange{ThinkingLevel: harness.SetTo(level)}); err != nil {
		t.Fatal(err)
	}
	if err := conversation.WaitForIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// Dispose releases the conversation's view state, as the Transcript facet's env.own(() => state.dispose()) does upstream (transcript-provider.ts:10): the durable state stops publishing and its mount is released.
func TestViewFramesDisposeReleasesTheDurableState(t *testing.T) {
	conversation, waitDeliveries := openRoot(t)
	source, err := attachViewFrames(t.Context(), conversation)
	if err != nil {
		t.Fatal(err)
	}
	var published atomic.Int32
	stop := source.state.SubscribeSource(func([]durable.Op, int, context.Context) { published.Add(1) })
	defer stop()
	source.Dispose()
	configure(t, conversation, ai.ThinkingHigh)
	waitDeliveries()
	if got := published.Load(); got != 0 {
		t.Fatalf("the durable view state published %d batches after Dispose", got)
	}
}

// Subscribing before the snapshot can deliver a publication the snapshot already holds; it is skipped, so the first frame is the next publication with cursor 1 (chord types.ts ReplicatedStateSource: no overlap or gap).
func TestViewFramesSkipsPublicationsInTheSnapshot(t *testing.T) {
	conversation, _ := openRoot(t)
	source, err := attachViewFrames(t.Context(), conversation)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Dispose()
	attachment := source.Attach()
	var frames []chord.ReplicatedStateSourceFrame
	attachment.Activate(func(frame chord.ReplicatedStateSourceFrame) { frames = append(frames, frame) })
	source.mu.Lock()
	held := source.sequence
	source.mu.Unlock()
	source.receive([]durable.Op{{"s", []any{"replayed"}, true}}, held, context.Background())
	if len(frames) != 0 {
		t.Fatalf("a publication the snapshot holds was delivered: %v", frames)
	}
	source.receive([]durable.Op{{"s", []any{"next"}, true}}, held+1, context.Background())
	if len(frames) != 1 || frames[0].Cursor != 1 {
		t.Fatalf("frames = %+v, want one frame at cursor 1", frames)
	}
	// A gap breaks the source contract: nothing more is delivered.
	source.receive([]durable.Op{{"s", []any{"gap"}, true}}, held+3, context.Background())
	source.receive([]durable.Op{{"s", []any{"after"}, true}}, held+4, context.Background())
	if len(frames) != 1 {
		t.Fatalf("frames after a sequence gap = %+v", frames)
	}
}
