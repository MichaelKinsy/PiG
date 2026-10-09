package cli

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// pi: packages/coding-agent/src/cli/initial-message.ts

// Ports packages/coding-agent/test/initial-message.test.ts and the file-image rule of initial-message.ts:35-38.
func TestBuildInitialMessageMatchesPi(t *testing.T) {
	t.Run("merges piped stdin with the first CLI message into one prompt", func(t *testing.T) {
		got, _, rest := buildInitialMessage([]string{"Summarize the text given"}, "", nil, "README contents\n")
		if got != "README contents\nSummarize the text given" || len(rest) != 0 {
			t.Fatalf("got %q, rest %q", got, rest)
		}
	})
	t.Run("uses stdin as the initial prompt when no CLI message is present", func(t *testing.T) {
		got, _, rest := buildInitialMessage(nil, "", nil, "README contents")
		if got != "README contents" || len(rest) != 0 {
			t.Fatalf("got %q, rest %q", got, rest)
		}
	})
	t.Run("combines stdin, file text, and first CLI message in one prompt", func(t *testing.T) {
		got, _, rest := buildInitialMessage([]string{"Explain it", "Second message"}, "file\n", nil, "stdin\n")
		if got != "stdin\nfile\nExplain it" || !slices.Equal(rest, []string{"Second message"}) {
			t.Fatalf("got %q, rest %q", got, rest)
		}
	})
	t.Run("returns file images only when there are some", func(t *testing.T) {
		image := ai.ImageContent{Data: "AAAA", MimeType: "image/png"}
		if _, images, _ := buildInitialMessage([]string{"m"}, "", []ai.ImageContent{image}, ""); len(images) != 1 || images[0] != image {
			t.Fatalf("images = %v, want the file image", images)
		}
		if _, images, _ := buildInitialMessage([]string{"m"}, "", []ai.ImageContent{}, ""); images != nil {
			t.Fatalf("images = %v, want none for an empty list", images)
		}
	})
}
