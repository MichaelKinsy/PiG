package mcptui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestChannelRendererSourceMarker(t *testing.T) {
	message := extension.CustomMessage{CustomType: "mcp-channel", Content: `<channel source="ci">build done</channel>`, Details: map[string]any{"source": "ci"}}
	renderer := Terminal().ChannelRenderer
	component := renderer(message, extension.MessageRenderOptions{OutputPad: 1}, nil)
	text := strings.Join(component.Render(100), "\n")
	if !strings.Contains(text, "← ci") || !strings.Contains(text, "build done") {
		t.Fatalf("channel marker/content missing: %q", text)
	}
}
