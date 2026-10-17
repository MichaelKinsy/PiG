package agent

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func imageCount(msgs []ai.Message) (images int, text []string) {
	for _, msg := range msgs {
		switch m := msg.(type) {
		case ai.UserMessage:
			if blocks, ok := m.Content.(ai.UserContentBlocks); ok {
				for _, b := range blocks {
					switch b := b.(type) {
					case ai.ImageContent:
						images++
					case ai.TextContent:
						text = append(text, b.Text)
					}
				}
			}
		case ai.ToolResultMessage:
			for _, b := range m.Content {
				switch b := b.(type) {
				case ai.ImageContent:
					images++
				case ai.TextContent:
					text = append(text, b.Text)
				}
			}
		}
	}
	return images, text
}

// Pi #10162: a long run that reads many images sends every image on each request until the provider answers
// "Too many images in request: 31 > 30". The catalog and models.json carry inputLimits.images.maxPerRequest but nothing enforced it.
// PiG (D137) keeps the newest images and replaces the older ones with a text placeholder.
func TestLimitImagesPerRequestKeepsTheNewestImages(t *testing.T) {
	img := func(n int) ai.ImageContent { return ai.ImageContent{Data: fmt.Sprint("img", n), MimeType: "image/png"} }
	history := []ai.Message{
		ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: "look"}, img(1)}},
		ai.ToolResultMessage{ToolCallID: "a", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Read image"}, img(2)}},
		ai.ToolResultMessage{ToolCallID: "b", ToolName: "read", Content: []ai.ToolResultMessageContent{img(3), img(4)}},
		ai.UserMessage{Content: ai.UserText("plain")},
	}
	model := &ai.Model{InputLimits: &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{MaxPerRequest: 2}}}
	got := limitImagesPerRequest(history, model)
	if images, _ := imageCount(got); images != 2 {
		t.Fatalf("images after limit = %d, want 2", images)
	}
	if last := got[2].(ai.ToolResultMessage); len(last.Content) != 2 || last.Content[0].(ai.ImageContent).Data != "img3" || last.Content[1].(ai.ImageContent).Data != "img4" {
		t.Errorf("newest images were not kept: %+v", last.Content)
	}
	if first := got[0].(ai.UserMessage).Content.(ai.UserContentBlocks); len(first) != 2 || first[1].(ai.TextContent).Text != imageOmittedPlaceholder(2) {
		t.Errorf("oldest image was not replaced in place: %+v", first)
	}
	if images, _ := imageCount(history); images != 4 {
		t.Errorf("the caller's history was mutated: %d images", images)
	}
	for _, unlimited := range []*ai.Model{nil, {}, {InputLimits: &ai.ModelInputLimits{}}, {InputLimits: &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{}}}} {
		if images, _ := imageCount(limitImagesPerRequest(history, unlimited)); images != 4 {
			t.Errorf("model %+v without a limit lost images: %d", unlimited, images)
		}
	}
	if images, _ := imageCount(limitImagesPerRequest(history, &ai.Model{InputLimits: &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{MaxPerRequest: 4}}})); images != 4 {
		t.Errorf("a request at the limit lost images: %d", images)
	}
}
