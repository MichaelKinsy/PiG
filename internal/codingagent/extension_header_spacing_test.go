package codingagent

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

func BenchmarkExtensionHeaderContainer(b *testing.B) {
	m := &InteractiveMode{opts: InteractiveOptions{LoginVisible: true}, extHeader: newSpecialLinesComponent(nil)}
	m.extHeader.SetLinesAt([]string{"header", "", "model", "context", "last row"}, 100)
	header := m.headerContainer()
	b.ReportAllocs()
	for b.Loop() {
		header.Render(100)
	}
}

// Pi interactive-mode.ts:1008-1015,2467-2488 keeps host spacers outside the replaced header, including empty components and quiet startup.
func TestExtensionHeaderKeepsHostSpacers(t *testing.T) {
	for _, visible := range []bool{true, false} {
		m := newSwitchTuiProbe(t)
		m.opts.LoginVisible = visible
		after := newSpecialLinesComponent(nil)
		after.SetLines([]string{"after-header"})
		m.loadedResourcesContainer = tui.NewContainer(after)
		m.mountInteractiveTui(true)
		ui := &ExtUIContext{m: m}
		for _, lines := range [][]string{{"first"}, {"", "second", ""}, {}, {"last"}} {
			ui.SetHeader(extension.WidthLines{Lines: lines, Width: 80})
			want := slices.Clone(lines)
			if visible {
				want = append(append([]string{""}, want...), "")
			}
			want = append(want, "after-header")
			got := m.layout.Render(80)
			if len(got) < len(want) || !slices.Equal(got[:len(want)], want) {
				t.Fatalf("visible=%v lines=%q: layout prefix %q, want %q", visible, lines, got, want)
			}
		}
		ui.SetHeader(nil)
		got := m.layout.Render(80)
		if visible {
			if got[0] != "" {
				t.Fatalf("restored header lost leading spacer: %q", got)
			}
			i := slices.Index(got, "after-header")
			if i < 1 || got[i-1] != "" {
				t.Fatalf("restored header lost trailing spacer: %q", got)
			}
		} else if got[0] != "after-header" {
			t.Fatalf("quiet restoration: %q", got)
		}
	}
}
