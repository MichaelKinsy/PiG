package codingagent

import (
	"io"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

// headerFooterView is a view surface whose structure a frontend can read.
type headerFooterView struct{ text string }

func (v headerFooterView) Render(int) []string       { return []string{v.text} }
func (headerFooterView) Invalidate()                 {}
func (headerFooterView) HandleViewInput(string) bool { return false }
func (v headerFooterView) FrontendView(int) *frontend.View {
	return &frontend.View{Root: frontend.ViewNode{Kind: frontend.ViewKindText, Text: v.text}}
}

// pig additive (D107): a header or footer an extension draws from a view
// reaches the host through Pi's typed factory and keeps its structure: the
// slot installs the view, so a D91 frontend reads it, and the footer
// replaces the built-in one.
func TestExtUIContextInstallsHeaderAndFooterViewsFromFactories(t *testing.T) {
	m := &InteractiveMode{
		extHeader:  newSpecialLinesComponent(nil),
		extFooter:  newSpecialLinesComponent(nil),
		statusLine: NewFooterComponent(nil, "", nil),
		tuiInst:    tui.NewWithOutput(io.Discard, 100, 45),
	}
	ui := &ExtUIContext{m: m}
	ui.SetHeader(extension.ViewHeader(extension.FramedView{View: headerFooterView{"kit header"}}))
	ui.SetFooter(extension.ViewFooter(extension.FramedView{View: headerFooterView{"kit footer"}}))
	for _, slot := range []struct {
		name      string
		component *specialLinesComponent
		want      string
	}{{"header", m.extHeader, "kit header"}, {"footer", m.extFooter, "kit footer"}} {
		if got := slot.component.Render(100); !slices.Equal(got, []string{slot.want}) {
			t.Errorf("%s lines = %q, want %q", slot.name, got, slot.want)
		}
		if view := slot.component.FrontendView(100); view == nil || view.Root.Text != slot.want {
			t.Errorf("%s frontend view = %#v, want the view's structure", slot.name, view)
		}
	}
	if m.statusLine.Render(100) != nil {
		t.Error("the built-in footer still draws beside the extension's footer view")
	}
}
