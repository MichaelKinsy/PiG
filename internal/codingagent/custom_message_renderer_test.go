package codingagent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestCustomMessageUsesRegisteredProductionRenderer(t *testing.T) {
	var got extension.CustomMessage
	var gotOptions extension.MessageRenderOptions
	ext := extension.Extension{
		MessageRenderers: map[string]extension.MessageRenderer{
			"notice": func(message extension.CustomMessage, options extension.MessageRenderOptions, _ extension.Theme) extension.Component {
				got = message
				gotOptions = options
				return tui.NewText(fmt.Sprintf("renderer:%s:expanded=%t", got.Content.(string), options.Expanded))
			},
		},
	}
	m := &InteractiveMode{
		newRunner:     inproc.NewRunner([]extension.Extension{ext}, t.TempDir()),
		chatContainer: tui.NewContainer(),
		toolsExpanded: true,
	}
	entry := CustomMessageEntry{CustomType: "notice", Content: "hello", Display: true}
	entry.Timestamp = "2026-01-02T03:04:05.678Z"
	m.appendCustomMessage(entry)

	lines := m.chatContainer.Render(80)
	// Pi custom-message.ts:33 adds Spacer(1) ahead of a custom renderer's component too.
	if len(lines) != 2 || lines[0] != "" || strings.TrimSpace(lines[1]) != "renderer:hello:expanded=true" {
		t.Fatalf("rendered lines = %#v", lines)
	}
	if got.CustomType != "notice" || got.Content != "hello" || !gotOptions.Expanded {
		t.Fatalf("renderer input = %+v options=%+v", got, gotOptions)
	}
	// interactive-mode.ts:3861-3872 hands the renderer the whole CustomMessage (messages.ts:46: role "custom", timestamp = Date.parse(entry.timestamp)), not only its customType/content/display/details.
	wire, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"role":"custom","customType":"notice","content":"hello","display":true,"timestamp":1767323045678}`; string(wire) != want {
		t.Fatalf("renderer message = %s, want %s", wire, want)
	}
	m.toggleAllTools()
	lines = m.chatContainer.Render(80)
	if len(lines) != 4 || strings.TrimSpace(lines[1]) != "renderer:hello:expanded=false" || !strings.Contains(lines[3], "Tool output: collapsed") {
		t.Fatalf("collapsed renderer lines = %#v", lines)
	}
}

func TestCustomMessageFallsBackWhenRendererReturnsNoComponent(t *testing.T) {
	ext := extension.Extension{
		MessageRenderers: map[string]extension.MessageRenderer{
			"notice": func(extension.CustomMessage, extension.MessageRenderOptions, extension.Theme) extension.Component {
				return nil
			},
		},
	}
	m := &InteractiveMode{
		newRunner:     inproc.NewRunner([]extension.Extension{ext}, t.TempDir()),
		chatContainer: tui.NewContainer(),
	}
	m.appendCustomMessage(CustomMessageEntry{CustomType: "notice", Content: "fallback", Display: true})
	lines := m.chatContainer.Render(80)
	if len(lines) == 0 {
		t.Fatal("fallback renderer produced no lines")
	}
}

// types.ts MessageRenderer returns `Component | undefined`: a renderer's Component is the TUI's, so what it returns is mounted and rendered as is (interactive-mode.ts:3861-3872),
// and a nil result falls back to the default rendering.
func TestMessageRendererReturnsTheTuiComponentTheTranscriptMounts(t *testing.T) {
	var renderer = func(extension.CustomMessage, extension.MessageRenderOptions, extension.Theme) extension.Component {
		return tui.NewText("from the renderer")
	}
	ext := extension.Extension{MessageRenderers: map[string]extension.MessageRenderer{"notice": renderer}}
	m := &InteractiveMode{newRunner: inproc.NewRunner([]extension.Extension{ext}, t.TempDir()), chatContainer: tui.NewContainer()}
	m.appendCustomMessage(CustomMessageEntry{CustomType: "notice", Content: "body", Display: true})
	if joined := strings.Join(m.chatContainer.Render(80), "\n"); !strings.Contains(joined, "from the renderer") {
		t.Fatalf("transcript = %q, want the renderer's component", joined)
	}
}
