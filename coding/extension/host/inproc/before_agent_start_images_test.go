package inproc

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi: packages/coding-agent/src/core/extensions/types.ts:912 (BeforeAgentStartEvent.images?: ImageContent[]).
// A before_agent_start handler reads the prompt's images as ImageContent values (data and mimeType), the same ai.ImageContent
// the prompt carries, not as untyped JSON.
func TestBeforeAgentStartHandlerReceivesThePromptImagesAsImageContent(t *testing.T) {
	var seen []ai.ImageContent
	runner := NewRunner([]extension.Extension{{Path: "images", Handlers: map[string][]extension.HandlerFn{
		"before_agent_start": {func(args ...any) (any, error) {
			seen = args[0].(extension.BeforeAgentStartEvent).Images
			return &extension.BeforeAgentStartEventResult{}, nil
		}},
	}}}, t.TempDir())
	images := []ai.ImageContent{{Data: "aGk=", MimeType: "image/png"}, {Data: "eW8=", MimeType: "image/jpeg"}}
	if _, err := runner.EmitBeforeAgentStart(t.Context(), "look", images, extension.BuildSystemPromptOptions{}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(seen, images) {
		t.Fatalf("handler images = %#v, want %#v", seen, images)
	}
}
