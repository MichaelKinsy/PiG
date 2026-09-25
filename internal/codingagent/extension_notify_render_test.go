package codingagent

import (
	"io"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Upstream showExtensionNotify delegates info to showStatus (theme dim,
// padding 1, coalescing), warning to showWarning, and error to showError.
func TestExtensionNotifyUsesStatusRendering(t *testing.T) {
	for _, adapter := range []string{"context", "subprocess-notify"} {
		for _, kind := range []string{"info", "warning", "error", ""} {
			t.Run(adapter+"/"+kind, func(t *testing.T) {
				m := NewInteractiveMode(InteractiveOptions{})
				m.runCtx = t.Context()
				m.tuiInst = tui.NewWithOutput(io.Discard, 80, 24)
				m.chatContainer = tui.NewContainer()
				ui := &ExtUIContext{m: m}
				notify := ui.Notify
				if adapter == "subprocess-notify" {
					legacy := NewTUIUIContext(m.tuiInst)
					legacy.interactiveMode = m
					legacy.NotifyFunc = func(message string) { m.appendChatBlock(tui.NewText(message)) }
					notify = legacy.Notify
				}
				message := "chain finished: 4 steps in 4.6s"
				notify(message, kind)
				for len(m.uiTaskCh) > 0 {
					(<-m.uiTaskCh)()
				}
				token, prefix := "dim", ""
				switch kind {
				case "warning":
					token, prefix = "warning", "Warning: "
				case "error":
					token, prefix = "error", "Error: "
				}
				for _, width := range []int{10, 80} {
					want := tui.NewPaddedText(tui.ActiveTheme().FgText(token, prefix+message), 1, 0, nil).Render(width)
					want = append([]string{""}, want...)
					got := m.chatContainer.Render(width)
					if !slices.Equal(got, want) {
						t.Errorf("width %d: notification rows = %q, want suffix %q", width, got, want)
					}
				}
				if kind == "info" || kind == "" {
					notify("replacement", kind)
					for len(m.uiTaskCh) > 0 {
						(<-m.uiTaskCh)()
					}
					if m.chatContainer.ChildCount() != 2 {
						t.Errorf("consecutive info notices did not coalesce: %d children", m.chatContainer.ChildCount())
					}
				}
			})
		}
	}
}
