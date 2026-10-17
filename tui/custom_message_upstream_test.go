package tui

// pi: packages/coding-agent/src/modes/interactive/components/custom-message.ts

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// upstream: packages/coding-agent/test/custom-message.test.ts:10 "provides output padding to custom renderers and updates it".
// The inputs and expectations are the TypeScript test's: initTheme("dark"); a renderer that records its options and returns Text("custom", options.outputPad, 0); a CustomMessageComponent built with the default markdown theme and outputPad 1.
// The renderer sees {expanded: false, outputPad: 1} once, a line of the 40-column render starts with " custom"; after setOutputPad(0) it last saw {expanded: false, outputPad: 0} and a line starts with "custom".
// mutation-checked: SetOutputPad not rebuilding, the renderer receiving a fixed outputPad, and the renderer receiving Expanded: true each fail it.
func TestCustomMessageComponentProvidesOutputPadToCustomRenderersUpstream(t *testing.T) {
	SetTheme("dark")
	var optionsSeen []MessageRenderOptions
	renderer := func(_ *CustomMessage, options MessageRenderOptions) Component {
		optionsSeen = append(optionsSeen, options)
		return NewPaddedText("custom", options.OutputPad, 0, nil)
	}
	message := &CustomMessage{CustomType: "test", Content: "custom"}
	var component *CustomMessageComponent = NewCustomMessageComponent(message, renderer, nil, 1)

	if want := []MessageRenderOptions{{Expanded: false, OutputPad: 1}}; !reflect.DeepEqual(optionsSeen, want) {
		t.Fatalf("options seen = %v, want %v", optionsSeen, want)
	}
	if !anyLineStartsWith(component.Render(40), " custom") {
		t.Fatalf("no rendered line starts with %q: %q", " custom", component.Render(40))
	}

	component.SetOutputPad(0)

	if last, want := optionsSeen[len(optionsSeen)-1], (MessageRenderOptions{Expanded: false, OutputPad: 0}); last != want {
		t.Fatalf("last options = %v, want %v", last, want)
	}
	if !anyLineStartsWith(component.Render(40), "custom") {
		t.Fatalf("no rendered line starts with %q: %q", "custom", component.Render(40))
	}
}

func anyLineStartsWith(lines []string, prefix string) bool {
	for _, line := range lines {
		if strings.HasPrefix(widthx.StripAnsi(line), prefix) {
			return true
		}
	}
	return false
}
