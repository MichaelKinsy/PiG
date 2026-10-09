package codingagent

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// widthComponent renders the width it is asked for and counts its disposals, like a Pi component with a dispose method.
type widthComponent struct {
	prefix   string
	disposed int
}

func (c *widthComponent) Render(width int) []string {
	return []string{c.prefix + strings.Repeat("x", width)}
}
func (*widthComponent) Invalidate() {}
func (c *widthComponent) Dispose()  { c.disposed++ }

// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:2524-2580 setExtensionFooter / setExtensionHeader: the host calls the
// factory with (ui, theme[, footerDataProvider]), shows the component it returns at the width it renders, and disposes the previous custom
// component before it installs another or restores the built-in one.
func TestExtUIContextHeaderAndFooterFactoriesBuildAndDisposeComponents(t *testing.T) {
	mode := &InteractiveMode{
		extHeader:  newSpecialLinesComponent(func() {}),
		extFooter:  newSpecialLinesComponent(func() {}),
		statusLine: NewFooterComponent(nil, "", nil),
	}
	ui := &ExtUIContext{m: mode}

	var gotTheme *tui.Theme
	var gotData extension.ReadonlyFooterDataProvider
	first := &widthComponent{prefix: "f1:"}
	ui.SetFooter(func(_ tui.TUI, theme *tui.Theme, data extension.ReadonlyFooterDataProvider) extension.DisposableComponent {
		gotTheme, gotData = theme, data
		return first
	})
	if gotTheme == nil || gotData == nil || gotData != extension.ReadonlyFooterDataProvider(mode.statusLine.FooterDataProvider) {
		t.Fatalf("footer factory got theme=%v data=%v, want the active theme and the footer's data provider", gotTheme, gotData)
	}
	if got := mode.extFooter.Render(7); !slices.Equal(got, []string{"f1:xxxxxxx"}) {
		t.Fatalf("custom footer at width 7 = %q", got)
	}
	if got := mode.extFooter.Render(3); !slices.Equal(got, []string{"f1:xxx"}) {
		t.Fatalf("custom footer at width 3 = %q, want it rendered at that width", got)
	}

	second := &widthComponent{prefix: "f2:"}
	ui.SetFooter(func(tui.TUI, *tui.Theme, extension.ReadonlyFooterDataProvider) extension.DisposableComponent {
		return second
	})
	if first.disposed != 1 || second.disposed != 0 {
		t.Fatalf("after a replacement disposed = %d/%d, want 1/0", first.disposed, second.disposed)
	}
	ui.SetFooter(nil)
	if second.disposed != 1 {
		t.Fatalf("restoring the built-in footer disposed the custom one %d times, want 1", second.disposed)
	}
	if lines := mode.statusLine.Render(80); len(lines) == 0 {
		t.Fatal("clearing the custom footer did not restore the built-in footer")
	}

	header := &widthComponent{prefix: "h:"}
	ui.SetHeader(func(tui.TUI, *tui.Theme) extension.DisposableComponent { return header })
	if got := mode.extHeader.Render(4); !slices.Equal(got, []string{"h:xxxx"}) {
		t.Fatalf("custom header at width 4 = %q", got)
	}
	replacement := &widthComponent{prefix: "h2:"}
	ui.SetHeader(func(tui.TUI, *tui.Theme) extension.DisposableComponent { return replacement })
	ui.SetHeader(nil)
	if header.disposed != 1 || replacement.disposed != 1 {
		t.Fatalf("header disposals = %d/%d, want each custom header disposed once", header.disposed, replacement.disposed)
	}
}
