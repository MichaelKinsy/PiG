package codingagent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestCustomMessageUsesRegisteredProductionRenderer(t *testing.T) {
	var got extension.CustomMessageRef
	var gotOptions extension.MessageRenderOptions
	ext := extension.Extension{
		MessageRenderers: map[string]extension.MessageRenderer{
			"notice": func(message extension.CustomMessage, options extension.MessageRenderOptions, _ extension.Theme) extension.Component {
				var ok bool
				got, ok = message.(extension.CustomMessageRef)
				if !ok {
					t.Fatalf("message type = %T", message)
				}
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
	m.appendCustomMessage(CustomMessageEntry{CustomType: "notice", Content: "hello", Display: true})

	lines := m.chatContainer.Render(80)
	if len(lines) != 1 || strings.TrimSpace(lines[0]) != "renderer:hello:expanded=true" {
		t.Fatalf("rendered lines = %#v", lines)
	}
	if got.CustomType != "notice" || got.Content != "hello" || !gotOptions.Expanded {
		t.Fatalf("renderer input = %+v options=%+v", got, gotOptions)
	}
	m.toggleAllTools()
	lines = m.chatContainer.Render(80)
	if len(lines) != 1 || strings.TrimSpace(lines[0]) != "renderer:hello:expanded=false" {
		t.Fatalf("collapsed renderer lines = %#v", lines)
	}
}

func TestCustomMessageFallsBackWhenRendererReturnsNonComponent(t *testing.T) {
	ext := extension.Extension{
		MessageRenderers: map[string]extension.MessageRenderer{
			"notice": func(extension.CustomMessage, extension.MessageRenderOptions, extension.Theme) extension.Component {
				return "not a component"
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
