package codingagent

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// Upstream custom-message.test.ts supplies outputPad on construction and every
// changed setting, retaining expansion. Custom components own their padding.
func TestCustomMessageOutputPadProduction(t *testing.T) {
	var seen []string
	ext := extension.Extension{MessageRenderers: map[string]extension.MessageRenderer{
		"notice": func(_ extension.CustomMessage, options extension.MessageRenderOptions, _ extension.Theme) extension.Component {
			wire, err := json.Marshal(options)
			if err != nil {
				t.Fatal(err)
			}
			seen = append(seen, string(wire))
			return tui.NewText("custom")
		},
	}}
	m := &InteractiveMode{
		newRunner:     inproc.NewRunner([]extension.Extension{ext}, t.TempDir()),
		chatContainer: tui.NewContainer(),
		outputPad:     1,
	}
	m.appendCustomMessage(CustomMessageEntry{CustomType: "notice", Content: "custom", Display: true})
	if !reflect.DeepEqual(seen, []string{`{"expanded":false,"outputPad":1}`}) {
		t.Fatalf("initial options = %q", seen)
	}
	component := m.customMessageOrder[0]
	padded, ok := component.(interface{ SetOutputPad(int) })
	if !ok {
		t.Fatal("production custom component cannot update output padding")
	}
	padded.SetOutputPad(0)
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
