package codingagent

import (
	"bytes"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

func BenchmarkExtensionHeaderContainer(b *testing.B) {
	m := &InteractiveMode{opts: InteractiveModeOptions{LoginVisible: true}, extHeader: newSpecialLinesComponent(nil)}
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
		// The layout is mounted once: a header component installed in two mounted layouts is drawn by the one that consumes its dirty flag first.
		model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
		m := newUnmountedSwitchTuiProbe(t, InteractiveModeOptions{CWD: t.TempDir(), Model: model, AgentDir: t.TempDir(), Settings: Settings{TuiMode: "regular"}}, &bytes.Buffer{})
		m.opts.LoginVisible = visible
		after := newSpecialLinesComponent(nil)
		after.SetLines([]string{"after-header"})
		m.loadedResourcesContainer = tui.NewContainer(after)
		m.mountInteractiveTui(true)
		ui := &ExtUIContext{m: m}
		for _, lines := range [][]string{{"first"}, {"", "second", ""}, {}, {"last"}} {
			ui.SetHeader(extension.FrameHeader(lines, 80))
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

// Pi's headerContainer has no children until startup has the terminal's colors (interactive-mode.ts init(): ui.start(), then
// applyFromSettings() and await waitForTerminalColors(), then Spacer, header, Spacer). The container already mounted in the first
// layout stays empty while the query is pending and gains the spacers and header afterwards.
func TestStartupHeaderStaysOutOfTheFirstPaintUntilTheColorsArrive(t *testing.T) {
	for _, visible := range []bool{true, false} {
		m := newSwitchTuiProbe(t)
		m.opts.LoginVisible = visible
		m.startupHeaderPending = true
		first := m.headerContainer()
		if first != m.headerContainer() {
			t.Fatalf("visible=%v: a pending header must be one container shared by every mount", visible)
		}
		if got := first.Render(80); len(got) != 0 {
			t.Fatalf("visible=%v: header rendered %q before the terminal's colors arrived", visible, got)
		}
		m.mountStartupHeader()
		got := first.Render(80)
		if visible && (len(got) < 3 || got[0] != "" || got[len(got)-1] != "") {
			t.Fatalf("visible=%v: header after the colors lacks its host spacers: %q", visible, got)
		}
		if !visible && len(got) != 0 {
			t.Fatalf("visible=%v: a silenced startup header rendered %q", visible, got)
		}
		want := got
		if m.startupHeaderPending {
			t.Fatalf("visible=%v: still pending after mountStartupHeader", visible)
		}
		if fresh := m.headerContainer(); fresh == first || len(fresh.Render(80)) != len(want) {
			t.Fatalf("visible=%v: a later mount must build its own populated container", visible)
		}
	}
}
