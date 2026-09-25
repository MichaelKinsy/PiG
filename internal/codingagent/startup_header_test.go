package codingagent

import (
	"io"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestVerboseHeaderExpansionAndRestoration(t *testing.T) {
	// Pi applies verbose only when constructing ExpandableText. setToolsExpanded and setExtensionHeader(undefined) subsequently use toolOutputExpanded.
	for _, restore := range []bool{false, true} {
		name := "toggle"
		if restore {
			name = "restore"
		}
		t.Run(name, func(t *testing.T) {
			m := &InteractiveMode{
				opts:      InteractiveOptions{LoginVisible: true, Verbose: true},
				extHeader: newSpecialLinesComponent(nil),
				tuiInst:   tui.NewWithOutput(io.Discard, 100, 45),
			}
			ui := &ExtUIContext{m: m}
			m.restoreBuiltInHeader()
			for _, expanded := range []bool{true, false, true, false} {
				if restore {
					ui.SetHeader([]string{"custom header"})
				}
				m.setAllToolsExpanded(expanded)
				if restore {
					if got := strings.Join(m.extHeader.Render(100), "\n"); got != "custom header" {
						t.Fatalf("tool expansion replaced custom header: %q", got)
					}
					ui.SetHeader(nil)
				}
				got := stripANSITest(strings.Join(m.extHeader.Render(100), "\n"))
				if strings.Contains(got, "drop files to attach") != expanded ||
					strings.Contains(got, "Press ctrl+o to show full startup help and loaded resources.") == expanded {
					t.Fatalf("verbose header expanded=%v (restore=%v): %q", expanded, restore, got)
				}
			}
		})
	}
}

func TestVerboseHeaderRestorationUsesCurrentToolState(t *testing.T) {
	m := &InteractiveMode{
		opts:      InteractiveOptions{LoginVisible: true, Verbose: true},
		extHeader: newSpecialLinesComponent(nil),
	}
	// No tool toggle is required: restoring the built-in header uses the current false tool state, not verbose's initial expanded header state.
	ui := &ExtUIContext{m: m}
	ui.SetHeader([]string{"custom header"})
	ui.SetHeader(nil)
	got := stripANSITest(strings.Join(m.extHeader.Render(100), "\n"))
	if !strings.Contains(got, "show full startup help") || strings.Contains(got, "drop files to attach") {
		t.Fatalf("verbose header restoration ignored collapsed tools: %q", got)
	}
}

func TestBuiltInHeaderInitialExpansion(t *testing.T) {
	for _, tc := range []struct {
		name     string
		verbose  bool
		expanded bool
	}{
		{name: "default"},
		{name: "verbose", verbose: true},
		{name: "expanded tools", expanded: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &InteractiveMode{
				opts:          InteractiveOptions{LoginVisible: true, Verbose: tc.verbose},
				extHeader:     newSpecialLinesComponent(nil),
				toolsExpanded: tc.expanded,
			}
			m.setBuiltInHeader(m.opts.Verbose || m.toolsExpanded)
			initial := strings.Join(m.extHeader.Render(100), "\n")
			if strings.Contains(initial, "drop files") != (tc.verbose || tc.expanded) {
				t.Fatalf("initial expansion: %q", initial)
			}
			// Pi returns early for the same tool state, even when the initial verbose header is expanded independently of collapsed tools.
			m.setAllToolsExpanded(tc.expanded)
			if got := strings.Join(m.extHeader.Render(100), "\n"); got != initial {
				t.Fatalf("unchanged tool state changed initial help: %q", got)
			}
		})
	}
}

func BenchmarkBuiltInHeaderRender(b *testing.B) {
	for _, expanded := range []bool{false, true} {
		name := "compact"
		if expanded {
			name = "expanded"
		}
		b.Run(name, func(b *testing.B) {
			km := &KeybindingsManager{definitions: appKeybindingDefinitions, ordered: appKeybindingOrder, platform: tui.HostKeybindingPlatform()}
			km.rebuild()
			m := &InteractiveMode{
				opts:        InteractiveOptions{LoginVisible: true},
				keybindings: km,
				extHeader:   newSpecialLinesComponent(nil),
			}
			m.setBuiltInHeader(expanded)
			b.ReportAllocs()
			for b.Loop() {
				m.extHeader.Render(100)
			}
		})
	}
}

// Pi's built-in header is expandable startup help, not a cwd/config/bin report.
// D2 and D63 change only the product identity and composite version.
func TestBuiltInHeaderMatchesPiStartupHelp(t *testing.T) {
	km := &KeybindingsManager{definitions: appKeybindingDefinitions, ordered: appKeybindingOrder, platform: tui.HostKeybindingPlatform()}
	km.rebuild()
	m := &InteractiveMode{
		opts:        InteractiveOptions{LoginVisible: true},
		keybindings: km,
		extHeader:   newSpecialLinesComponent(nil),
		tuiInst:     tui.NewWithOutput(io.Discard, 100, 40),
	}
	m.restoreBuiltInHeader()
	compact := stripANSITest(strings.Join(m.extHeader.Render(100), "\n"))
	for _, want := range []string{
		"pig v" + pigversion.Version,
		"escape interrupt · ctrl+c/ctrl+d clear/exit · / commands · ! bash · ctrl+o more",
		"Press ctrl+o to show full startup help and loaded resources.",
		"PiG can explain its own features and look up its docs. Ask it how to use or extend PiG.",
	} {
		if !strings.Contains(compact, want) {
			t.Errorf("compact header missing %q: %q", want, compact)
		}
	}
	m.setAllToolsExpanded(true)
	expanded := stripANSITest(strings.Join(m.extHeader.Render(100), "\n"))
	for _, want := range []string{"escape to interrupt", "ctrl+c twice to exit", "ctrl+k to delete to end", "shift+tab to cycle thinking level", "!! to run bash (no context)", "drop files to attach"} {
		if !strings.Contains(expanded, want) {
			t.Errorf("expanded header missing %q: %q", want, expanded)
		}
	}
	if strings.Contains(expanded, "show full startup help") {
		t.Error("expanded header retained compact onboarding")
	}
	m.setAllToolsExpanded(false)
	if got := stripANSITest(strings.Join(m.extHeader.Render(100), "\n")); got != compact {
		t.Errorf("collapse did not restore compact header: %q", got)
	}
	m.opts.LoginVisible = false
	m.restoreBuiltInHeader()
	if got := m.extHeader.Render(100); len(got) != 0 {
		t.Fatalf("quiet startup header = %q", got)
	}
}
