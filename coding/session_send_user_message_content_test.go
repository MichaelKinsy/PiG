package coding

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// agent-session.ts sendUserMessage: an array content joins its text parts with "\n" and passes its image parts as the prompt images; a string is the text unchanged.
func TestSendUserMessageNormalizesArrayContentToTextAndImages(t *testing.T) {
	for _, tc := range []struct {
		name       string
		content    any
		wantText   string
		wantImages []string
	}{
		{"string", "plain", "plain", nil},
		{"text parts", ai.UserContentBlocks{ai.TextContent{Text: "a"}, ai.TextContent{Text: "b"}}, "a\nb", nil},
		{"text and images", []ai.UserContentBlock{ai.TextContent{Text: "look"}, ai.ImageContent{Data: promptTinyPNG, MimeType: "image/png"}, ai.TextContent{Text: "here"}}, "look\nhere", []string{"image/png"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRecoveryHarness(t, harnessOptions{})
			if err := h.session.SendUserMessage(t.Context(), tc.content, &extension.SendUserMessageOptions{}); err != nil {
				t.Fatal(err)
			}
			var texts, images []string
			for _, message := range h.session.Messages() {
				if message.User == nil {
					continue
				}
				texts = append(texts, extractUserMessageText(message.User.Content))
				if blocks, ok := message.User.Content.(ai.UserContentBlocks); ok {
					for _, block := range blocks {
						if image, ok := block.(ai.ImageContent); ok {
							images = append(images, image.MimeType)
						}
					}
				}
			}
			if !slices.Equal(texts, []string{tc.wantText}) || !slices.Equal(images, tc.wantImages) {
				t.Fatalf("texts=%q images=%v want %q %v", texts, images, tc.wantText, tc.wantImages)
			}
		})
	}
}
