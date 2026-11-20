package mcptui

import (
	"strings"
	"testing"
)

func TestChannelRendererSourceMarker(t *testing.T) {
	component := RenderChannel("ci", `<channel source="ci">build done</channel>`, 1)
	text := strings.Join(component.Render(100), "\n")
	if !strings.Contains(text, "← ci") || !strings.Contains(text, "build done") {
		t.Fatalf("channel marker/content missing: %q", text)
	}
}
