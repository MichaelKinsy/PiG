package codingagent

import (
	"io"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// recordingFooter is a custom footer that records when it is disposed relative to the other footer calls.
type recordingFooter struct {
	widthComponent
	log *[]string
	id  string
}

func (f *recordingFooter) Dispose() {
	f.widthComponent.Dispose()
	*f.log = append(*f.log, "dispose "+f.id)
}

func newFooterProbe() (*ExtUIContext, *InteractiveMode, *int) {
	renders := new(int)
	mode := &InteractiveMode{
		tuiInst:    tui.NewWithOutput(io.Discard, 80, 24),
		extHeader:  newSpecialLinesComponent(func() {}),
		extFooter:  newSpecialLinesComponent(func() { *renders++ }),
		statusLine: NewFooterComponent(nil, "", nil),
	}
	return &ExtUIContext{m: mode}, mode, renders
}

// setExtensionFooter (interactive-mode.ts) in upstream order: the existing custom footer is disposed first, the footer container is cleared, then either the factory's
// component replaces the built-in footer (the factory gets the TUI, the active theme and the live footer data provider) or the built-in footer is restored, and the TUI
// is asked to render. Disposal is exactly once per component, the built-in footer's keyed-status row is replaced with the custom footer and returns with it, and clearing
// when no custom footer is set disposes nothing.
func TestSetExtensionFooterFollowsUpstreamOrder(t *testing.T) {
	ui, mode, renders := newFooterProbe()
	mode.statusLine.SetExtensionStatus("key", "status text")
	var log []string
	first := &recordingFooter{prefix: "f1:", log: &log, id: "first"}
	second := &recordingFooter{prefix: "f2:", log: &log, id: "second"}

	before := *renders
	ui.SetFooter(nil)
	if len(log) != 0 || mode.statusLine.Render(80) == nil || *renders == before {
		t.Fatalf("clearing with no custom footer: log %q, built-in rows %d, renders +%d; want nothing disposed, the built-in footer shown and a render requested", log, len(mode.statusLine.Render(80)), *renders-before)
	}

	var seen []string
	var liveProvider extension.ReadonlyFooterDataProvider
	factory := func(footer *recordingFooter) extension.FooterFactory {
		return func(host tui.TUI, theme *tui.Theme, data extension.ReadonlyFooterDataProvider) extension.DisposableComponent {
			log = append(log, "factory "+footer.id)
			seen = append(seen, footer.id)
			if host != mode.tuiInst || theme != tui.ActiveTheme() {
				t.Errorf("factory %s got host %v and theme %p, want the TUI and the active theme %p", footer.id, host, theme, tui.ActiveTheme())
			}
			liveProvider = data
			return footer
		}
	}
	before = *renders
	ui.SetFooter(factory(first))
	if *renders == before {
		t.Error("installing a custom footer did not request a render")
	}
	if rows := mode.statusLine.Render(80); len(rows) != 0 {
		t.Errorf("the built-in footer still draws %d rows under a custom footer", len(rows))
	}
	// The provider is the live one: a status set after the factory ran is visible through it.
	mode.statusLine.SetExtensionStatus("late", "set after the factory")
	if got := liveProvider.GetExtensionStatuses()["late"]; got != "set after the factory" {
		t.Errorf("the factory's provider shows %q for a status set later, want the live statuses", got)
	}
	before = *renders
	ui.SetFooter(factory(second))
	if *renders == before {
		t.Error("replacing the custom footer did not request a render")
	}
	if want := []string{"factory first", "dispose first", "factory second"}; !slices.Equal(log, want) {
		t.Errorf("order of calls = %q, want %q: the previous footer is disposed before the next factory runs", log, want)
	}
	if got := mode.extFooter.Render(5); !slices.Equal(got, []string{"f2:xxxxx"}) {
		t.Errorf("footer rows after the replacement = %q, want only the second custom footer", got)
	}
	before = *renders
	ui.SetFooter(nil)
	if *renders == before {
		t.Error("restoring the built-in footer did not request a render")
	}
	if want := []string{"factory first", "dispose first", "factory second", "dispose second"}; !slices.Equal(log, want) {
		t.Errorf("order of calls = %q, want %q", log, want)
	}
	if got := mode.extFooter.Render(5); len(got) != 0 {
		t.Errorf("the custom footer still draws %q after it was cleared", got)
	}
	rows := mode.statusLine.Render(80)
	if len(rows) == 0 || stripANSI(rows[len(rows)-1]) != "status text set after the factory" {
		t.Errorf("the built-in footer after the clear = %q, want its keyed-status row back", rows)
	}
	ui.SetFooter(nil)
	if first.disposed != 1 || second.disposed != 1 {
		t.Errorf("disposals = %d/%d, want each footer disposed once across the replacements and two clears", first.disposed, second.disposed)
	}
}
