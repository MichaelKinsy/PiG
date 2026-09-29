package codingagent

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// .upstream/v0.87.1/packages/coding-agent/test/interactive-tui.test.ts:51
func TestInteractiveTuiSelectsAlternateRendererOnlyWhenRequested(t *testing.T) {
	for _, name := range []string{"regular", "fullscreen"} {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			mode := NewInteractiveMode(InteractiveOptions{Settings: Settings{TuiMode: name}, AgentDir: t.TempDir()})
			mode.rendererOut = &output
			handle := mode.createInteractiveTui(t.Context())
			defer handle.cleanup()
			mode.tuiInst.SetShowHardwareCursor(false)
			mode.tuiInst.SetRenderDispatcher(func(func()) {})
			_, viewport := mode.tuiInst.(*tui.TuiAltScreen)
			if viewport != (name == "fullscreen") {
				t.Fatalf("mode %s chose %T", name, mode.tuiInst)
			}
			mode.tuiInst.Start()
			mode.tuiInst.Render()
			if strings.Contains(output.String(), "\x1b[?1049h") != (name == "fullscreen") {
				t.Fatalf("mode %s enter bytes=%q", name, output.String())
			}
			mode.tuiInst.StopWithOptions(tui.StopOptions{PreserveScreen: true})
		})
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/interactive-tui.test.ts:81
func TestInteractiveTuiShowsConfiguredJumpShortcut(t *testing.T) {
	original := tui.GetKeybindings()
	t.Cleanup(func() { tui.SetKeybindings(original) })
	tui.SetKeybindings(tui.NewTUIKeybindingsManager(map[string][]string{tui.KBAltScreenBottom: {"ctrl+j"}}))
	var output bytes.Buffer
	renderer := tui.NewTuiAltScreenWithOutput(&output, 50, 4, fullscreenTuiOptions())
	renderer.SetRenderDispatcher(func(func()) {})
	var lines []string
	for i := 1; i <= 8; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	renderer.SetLayoutRoot(tui.NewScrollView(tui.NewPaddedText(strings.Join(lines, "\n"), 0, 0, nil), tui.ScrollViewOptions{Follow: "end", Primary: true}))
	renderer.Start()
	renderer.Render()
	output.Reset()
	renderer.HandleViewportInput("\x1b[<64;1;1M")
	renderer.Render()
	// The alternate renderer addresses each changed row explicitly; inspect the complete bottom-row write rather than a document-only snapshot.
	raw := output.String()
	start := strings.LastIndex(raw, "\x1b[4;1H")
	if start < 0 || !strings.Contains(stripANSITest(raw[start:]), "↓ Jump to latest message · Ctrl+J") {
		t.Fatalf("bottom viewport row lacks configured shortcut: %q", raw)
	}
	renderer.StopWithOptions(tui.StopOptions{PreserveScreen: true})
}
