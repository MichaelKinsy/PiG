package tui

import (
	"strings"
	"testing"
)

// countingRenderUI counts the renders a Loader asks for; it embeds a nil TUI, so a Loader sees no RequestRenderWithin.
type countingRenderUI struct {
	TUI
	requests int
}

func (u *countingRenderUI) RequestRender(...bool) { u.requests++ }

// pig divergence (D652): SetMessageStep replaces the message for an owner that draws the change itself, so it asks for no frame; SetMessage asks for one.
func TestLoaderSetMessageStepAsksForNoFrame(t *testing.T) {
	ui := &countingRenderUI{}
	loader := NewLoader(ui, nil, nil, "before", nil)
	ui.requests = 0
	loader.SetMessageStep("after")
	if ui.requests != 0 {
		t.Fatalf("SetMessageStep asked for %d renders, want 0", ui.requests)
	}
	if got := loader.Render(80); len(got) < 2 || !strings.Contains(got[1], "after") {
		t.Fatalf("rendered %q, want the new message", got)
	}
	loader.SetMessage("again")
	if ui.requests == 0 {
		t.Fatal("SetMessage asked for no render")
	}
}
