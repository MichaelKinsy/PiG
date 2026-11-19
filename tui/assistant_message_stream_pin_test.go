package tui

import (
	"runtime"
	"slices"
	"testing"
)

// streamPinText is a streamed answer with two closed code blocks and text after them.
const streamPinText = "intro\n\n```ts\nx\n```\n\nmiddle\n\n```ts\ny\n```\n\ntail"

// TestStreamingMessageRebuildsNoUnchangedBlockAfterACollection: a full garbage collection between two frames of a message that streams makes the next frame render no block again that its delta did not change. The counting highlighter runs once for each code block over the whole stream; a Markdown nothing holds renders every block again after a collection (TestUpstreamMarkdownBlockReuse, "holds rendered blocks weakly").
func TestStreamingMessageRebuildsNoUnchangedBlockAfterACollection(t *testing.T) {
	mdCaseSetup(t)
	var calls []string
	block := NewAssistantMessageComponent(nil, false, countingMarkdownTheme(&calls), "", nil, nil, "", "")
	block.SetTextDelta(streamPinText)
	block.Render(80)
	want := []string{"ts:x", "ts:y"}
	if !slices.Equal(calls, want) {
		t.Fatalf("calls after the first frame = %q, want %q", calls, want)
	}
	for frame := 2; frame <= 4; frame++ {
		runtime.GC()
		block.SetTextDelta(" more")
		block.Render(80)
		if !slices.Equal(calls, want) {
			t.Fatalf("calls after a collection and frame %d = %q, want %q: a block the delta did not change was rendered again", frame, calls, want)
		}
	}
}

// TestStreamingMessageLetsGoOfItsBlocksWhenItEnds: the block holds what its last frame kept only while its message streams. A collection releases it once the stream ends, the block is invalidated or it is disposed, as it releases the blocks of a Markdown nothing holds.
func TestStreamingMessageLetsGoOfItsBlocksWhenItEnds(t *testing.T) {
	mdCaseSetup(t)
	for _, c := range []struct {
		name string
		end  func(*AssistantMessageComponent)
	}{
		{"FinishStreaming", (*AssistantMessageComponent).FinishStreaming},
		{"Invalidate", (*AssistantMessageComponent).Invalidate},
		{"Dispose", (*AssistantMessageComponent).Dispose},
	} {
		t.Run(c.name, func(t *testing.T) {
			var calls []string
			block := NewAssistantMessageComponent(nil, false, countingMarkdownTheme(&calls), "", nil, nil, "", "")
			block.SetTextDelta(streamPinText)
			block.Render(80)
			if len(block.segments) != 1 {
				t.Fatalf("%d segments, want 1", len(block.segments))
			}
			kept := block.segments[0].md.memosRef
			runtime.GC()
			if kept.Value() == nil {
				t.Fatal("a collection released what the last frame of a streaming message kept")
			}
			c.end(block)
			runtime.GC()
			if kept.Value() != nil {
				t.Fatalf("what the last frame kept survived a collection after %s", c.name)
			}
		})
	}
}

// TestStreamingMessageHoldsOnlyItsLastFrame: what the block holds is what its segments kept at the frame last drawn, one entry for each segment that kept something, however many frames were drawn.
func TestStreamingMessageHoldsOnlyItsLastFrame(t *testing.T) {
	pauseGC(t) // a collection between a segment's render and the block's pin frees that segment's memos, and the count below holds both; a WeakRef target is kept until its job ends
	mdCaseSetup(t)
	var calls []string
	block := NewAssistantMessageComponent(nil, false, countingMarkdownTheme(&calls), "", nil, nil, "", "")
	block.SetThinkingDelta("thinking")
	block.SetTextDelta(streamPinText)
	for frame := 1; frame <= 5; frame++ {
		block.SetTextDelta(" more")
		block.Render(80)
		var want []*markdownMemos
		for _, segment := range block.segments {
			if memos := segment.md.memosRef.Value(); memos != nil {
				want = append(want, memos)
			}
		}
		if len(want) != 2 {
			t.Fatalf("frame %d: %d segments kept memos, want 2", frame, len(want))
		}
		if !slices.Equal(block.streamMemos, want) {
			t.Fatalf("frame %d: the block holds %d memos, want those of its %d segments", frame, len(block.streamMemos), len(want))
		}
	}
	block.FinishStreaming()
	if held := block.streamMemos; len(held) != 0 {
		t.Fatalf("the block holds %d memos after FinishStreaming", len(held))
	}
	block.Render(80)
	if held := block.streamMemos; len(held) != 0 {
		t.Fatalf("the block holds %d memos after a frame drawn when its stream had ended", len(held))
	}
}
