package codingagent

import (
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// queueExtensionError is the runner's error listener. Listeners run on the
// goroutine that hit the failure, often the session's event forwarder, so it
// only records the error and wakes the main loop: it never blocks and never
// touches the component tree. Mirrors upstream's onError binding, which calls
// showExtensionError. Every error is kept until the main loop shows it, as
// upstream shows every extension error.
func (m *InteractiveMode) queueExtensionError(err *extension.ExtensionError) {
	if err == nil {
		return
	}
	m.extensionErrorsMu.Lock()
	m.pendingExtensionErrors = append(m.pendingExtensionErrors, *err)
	m.extensionErrorsMu.Unlock()
	select {
	case m.extensionErrorWakeCh <- struct{}{}:
	default:
	}
}

// showPendingExtensionErrors shows the queued extension errors on the main
// loop.
func (m *InteractiveMode) showPendingExtensionErrors() {
	m.extensionErrorsMu.Lock()
	pending := m.pendingExtensionErrors
	m.pendingExtensionErrors = nil
	m.extensionErrorsMu.Unlock()
	if m.chatContainer == nil {
		return
	}
	for _, err := range pending {
		m.showExtensionError(err.ExtensionPath, err.Error, err.Stack)
	}
	if len(pending) > 0 {
		// pig additive (D95): a failed extension may be a cell file another pig pruned.
		m.maybeShowInstallChangeWarning()
		m.requestRender()
	}
}

// showExtensionError mirrors upstream InteractiveMode.showExtensionError: the
// message in the error color, then the stack without its first line, dimmed and indented. Neither block adds a spacer.
func (m *InteractiveMode) showExtensionError(extensionPath, message, stack string) {
	m.chatContainer.Add(themedNotice("error", "Extension \""+extensionPath+"\" error: "+message, 1))
	if stack == "" {
		return
	}
	lines := strings.Split(stack, "\n")[1:]
	if len(lines) > 0 {
		m.chatContainer.Add(tui.NewThemedText(func() string {
			rendered := make([]string, len(lines))
			for i, line := range lines {
				rendered[i] = tui.ActiveTheme().FgText("dim", "  "+strings.TrimSpace(line))
			}
			return strings.Join(rendered, "\n")
		}, 1, 0))
	}
}
