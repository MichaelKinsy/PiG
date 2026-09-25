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
		m.requestRender()
	}
}

// showExtensionError mirrors upstream InteractiveMode.showExtensionError: the
// message in the error color, then the stack without its first line, dimmed
// and indented.
func (m *InteractiveMode) showExtensionError(extensionPath, message, stack string) {
	theme := tui.ActiveTheme()
	m.appendChatBlock(tui.NewPaddedText(theme.FgText("error", "Extension \""+extensionPath+"\" error: "+message), 1, 0, nil))
	if stack == "" {
		return
	}
	lines := strings.Split(stack, "\n")[1:]
	for i, line := range lines {
		lines[i] = theme.FgText("dim", "  "+strings.TrimSpace(line))
	}
	if stackLines := strings.Join(lines, "\n"); stackLines != "" {
		m.appendChatBlock(tui.NewPaddedText(stackLines, 1, 0, nil))
	}
}
