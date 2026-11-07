package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// dirtyText is a renderer component that reports itself changed, as an extension renderer proxy does between frames.
type dirtyText struct {
	*Text
	dirty bool
}

func (d *dirtyText) IsDirty() bool { return d.dirty }

func renderCustomMessage(c *CustomMessageComponent, width int) []string {
	return (*CustomMessageComponent).Render(c, width)
}

// custom-message.ts:57-110: the default rendering is a box whose horizontal padding is the output pad (:90), the label is the bold "[customType]" in the customMessageLabel
// color (:98), and the content follows it. A rebuild replaces the custom renderer's previous component instead of stacking another one, and a component that reports its own
// changes (IsDirty) makes the message live (D91) while a plain one does not.
func TestCustomMessageComponentRendersItsBoxPaddingLabelAndReplacesRebuiltContent(t *testing.T) {
	message := &CustomMessage{CustomType: "note", Content: "hello"}
	for _, pad := range []int{0, 1, 3} {
		c := NewCustomMessageComponent(message, nil, nil, pad)
		lines := renderCustomMessage(c, 40)
		var label string
		for _, line := range lines {
			if strings.Contains(widthx.StripAnsi(line), "[note]") {
				label = line
			}
		}
		if label == "" {
			t.Fatalf("pad %d: no label line in %q", pad, lines)
		}
		if !strings.Contains(label, "\x1b[1m[note]\x1b[22m") {
			t.Errorf("pad %d: label %q is not the bold [note]", pad, label)
		}
		if plain := widthx.StripAnsi(label); !strings.HasPrefix(plain, strings.Repeat(" ", pad)+"[note]") || strings.HasPrefix(plain, strings.Repeat(" ", pad+1)+"[") {
			t.Errorf("pad %d: label line %q, want exactly %d columns before the label", pad, plain, pad)
		}
		if c.SurfaceLive() {
			t.Errorf("pad %d: a default message reports itself live", pad)
		}
	}

	var built []*dirtyText
	c := NewCustomMessageComponent(message, func(*CustomMessage, MessageRenderOptions) Component {
		d := &dirtyText{Text: NewText("custom body")}
		built = append(built, d)
		return d
	}, nil, 1)
	if !c.SurfaceLive() {
		t.Error("a message whose renderer component reports changes is not live")
	}
	built[0].dirty = true
	if !c.IsDirty() {
		t.Error("the message did not report its renderer component's change")
	}
	c.SetExpanded(true)
	c.SetOutputPad(2)
	count := 0
	for _, line := range renderCustomMessage(c, 40) {
		if strings.Contains(widthx.StripAnsi(line), "custom body") {
			count++
		}
	}
	if count != 1 || len(built) != 3 || len(c.Children()) != 2 {
		t.Errorf("after two rebuilds the message shows %d copies of the renderer's content from %d components with %d children, want one copy and Spacer + content", count, len(built), len(c.Children()))
	}
}
