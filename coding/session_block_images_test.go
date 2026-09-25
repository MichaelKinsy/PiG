package coding

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Upstream createAgentSession wraps convertToLlm (sdk.ts
// convertToLlmWithBlockImages): with images.blockImages on, every image in a
// user or toolResult message becomes the text "Image reading is disabled.",
// consecutive placeholders collapse to one, and the setting is read per
// request so a mid-session change applies.
func TestSessionBlockImagesReplacesImagesInProviderRequest(t *testing.T) {
	svcs := newTestServices(t)
	provider := &transcriptCaptureProvider{}
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModelWithProvider(provider)})
	if err != nil {
		t.Fatal(err)
	}
	defer func(s *Session) { _ = s.Close() }(sess)
	image := sessionImageFixture(t)
	content := ai.UserContentBlocks{ai.TextContent{Text: "look"}, image, image}

	if _, err := sess.SendContent(context.Background(), []ai.UserContentBlock(content)); err != nil {
		t.Fatal(err)
	}
	if err := svcs.SettingsManager().SetBlockImages(true); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Send(context.Background(), "again"); err != nil {
		t.Fatal(err)
	}

	if len(provider.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(provider.requests))
	}
	first := firstUserContent(t, provider.requests[0])
	if images := countImages(first); images != 2 {
		t.Fatalf("blockImages off: request images = %d, want 2 (%#v)", images, first)
	}
	blocked := firstUserContent(t, provider.requests[1])
	want := ai.UserContentBlocks{ai.TextContent{Text: "look"}, ai.TextContent{Text: "Image reading is disabled."}}
	if !reflect.DeepEqual(blocked, want) {
		t.Fatalf("blockImages on: user content = %#v, want %#v", blocked, want)
	}
}

func firstUserContent(t *testing.T, transcript ai.TranscriptContext) ai.UserContentBlocks {
	t.Helper()
	for _, message := range transcript.Messages() {
		if user, ok := message.(ai.UserMessage); ok {
			blocks, _ := user.Content.(ai.UserContentBlocks)
			return blocks
		}
	}
	t.Fatal("no user message in request")
	return nil
}

func countImages(blocks ai.UserContentBlocks) int {
	count := 0
	for _, block := range blocks {
		if _, ok := block.(ai.ImageContent); ok {
			count++
		}
	}
	return count
}

// The dedupe compares against the mapped array, as upstream's filter does, so
// an existing placeholder text followed by an image also collapses; messages
// without images keep repeated placeholder text.
func TestBlockImagesMatchesUpstreamMapAndDedupe(t *testing.T) {
	image := ai.ImageContent{Data: "x", MimeType: "image/png"}
	placeholder := ai.TextContent{Text: blockedImageText}
	in := []ai.Message{
		ai.UserMessage{Content: ai.UserContentBlocks{placeholder, image, ai.TextContent{Text: "a"}, image}, Timestamp: 1},
		ai.UserMessage{Content: ai.UserContentBlocks{placeholder, placeholder}},
		ai.UserMessage{Content: ai.UserText("plain")},
		ai.ToolResultMessage{ToolCallID: "t", Content: []ai.ToolResultMessageContent{image, image, ai.TextContent{Text: "b"}}},
		ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "c"}}},
	}
	want := []ai.Message{
		ai.UserMessage{Content: ai.UserContentBlocks{placeholder, ai.TextContent{Text: "a"}, placeholder}, Timestamp: 1},
		ai.UserMessage{Content: ai.UserContentBlocks{placeholder, placeholder}},
		ai.UserMessage{Content: ai.UserText("plain")},
		ai.ToolResultMessage{ToolCallID: "t", Content: []ai.ToolResultMessageContent{placeholder, ai.TextContent{Text: "b"}}},
		ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "c"}}},
	}
	original := in[0].(ai.UserMessage).Content.(ai.UserContentBlocks)[1]
	if got := blockImages(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("blockImages =\n%#v\nwant\n%#v", got, want)
	}
	if in[0].(ai.UserMessage).Content.(ai.UserContentBlocks)[1] != original {
		t.Fatal("blockImages mutated its input")
	}
}
