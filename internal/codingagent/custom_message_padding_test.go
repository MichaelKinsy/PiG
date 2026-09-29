package codingagent

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Ports packages/coding-agent/test/custom-message.test.ts:11: custom renderers receive outputPad on construction and after updates, and own the rendered padding.
func TestCustomMessageOutputPadProduction(t *testing.T) {
	var seen []string
	ext := extension.Extension{MessageRenderers: map[string]extension.MessageRenderer{
		"test": func(_ extension.CustomMessage, options extension.MessageRenderOptions, _ extension.Theme) extension.Component {
			wire, err := json.Marshal(options)
			if err != nil {
				t.Fatal(err)
			}
			seen = append(seen, string(wire))
			return tui.NewPaddedText("custom", options.OutputPad, 0, nil)
		},
	}}
	m := &InteractiveMode{
		newRunner:     inproc.NewRunner([]extension.Extension{ext}, t.TempDir()),
		chatContainer: tui.NewContainer(),
		outputPad:     1,
	}
	m.appendCustomMessage(CustomMessageEntry{CustomType: "test", Content: "custom", Display: true})
	if !reflect.DeepEqual(seen, []string{`{"expanded":false,"outputPad":1}`}) {
		t.Fatalf("initial options = %q", seen)
	}
	component := m.customMessageOrder[0]
	assertPrefix := func(prefix string) {
		t.Helper()
		lines := component.Render(40)
		if !slices.ContainsFunc(lines, func(line string) bool { return strings.HasPrefix(widthx.StripAnsi(line), prefix) }) {
			t.Fatalf("render = %q, want a line starting with %q", lines, prefix)
		}
	}
	assertPrefix(" custom")
	padded, ok := component.(interface{ SetOutputPad(int) })
	if !ok {
		t.Fatal("production custom component cannot update output padding")
	}
	padded.SetOutputPad(0)
	if got := seen[len(seen)-1]; got != `{"expanded":false,"outputPad":0}` {
		t.Fatalf("updated options = %q", got)
	}
	assertPrefix("custom")
	padded.SetOutputPad(0)
	component.SetExpanded(true)
	padded.SetOutputPad(1)
	want := []string{
		`{"expanded":false,"outputPad":1}`,
		`{"expanded":false,"outputPad":0}`,
		`{"expanded":true,"outputPad":0}`,
		`{"expanded":true,"outputPad":1}`,
	}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("renderer options = %q, want %q", seen, want)
	}
}

func TestCustomMessageFallbackOutputPadProduction(t *testing.T) {
	m := &InteractiveMode{chatContainer: tui.NewContainer(), outputPad: 1}
	message := CustomMessageEntry{CustomType: "notice", Content: "custom", Display: true}
	m.appendCustomMessage(message)
	before := m.chatContainer.Render(40)
	m.chatContainer.Clear()
	m.outputPad = 0
	m.appendCustomMessage(message)
	if after := m.chatContainer.Render(40); !reflect.DeepEqual(before, after) {
		t.Fatalf("default Box(1,1) changed with outputPad: before=%q after=%q", before, after)
	}
}
