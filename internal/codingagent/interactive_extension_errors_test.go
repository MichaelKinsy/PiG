package codingagent

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func newExtensionErrorProbe(t *testing.T) *InteractiveMode {
	t.Helper()
	model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir(), Model: model})
	m.chatContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.tuiInst.Add(m.chatContainer)
	return m
}

func chatPlainText(m *InteractiveMode) string {
	lines := m.chatContainer.Render(100)
	for i, line := range lines {
		lines[i] = widthx.StripAnsi(line)
	}
	return strings.Join(lines, "\n")
}

// Upstream binds the extension runner's onError to showExtensionError. Pig had
// no listener in interactive mode, so handler failures and host-reported
// errors were never shown. The listener is wired with the runner, runs on the
// failing goroutine without blocking, and the main loop shows the error.
func TestInteractiveShowsExtensionRunnerErrors(t *testing.T) {
	m := newExtensionErrorProbe(t)
	m.newRunner = inproc.NewRunner(nil, t.TempDir())
	m.wireInprocContextActions()

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.newRunner.EmitError(&extension.ExtensionError{
			ExtensionPath: "<agent-event>",
			Event:         "agent_start",
			Error:         "panic: listener failed",
			Stack:         "panic: listener failed\ngoroutine 7 [running]:\nmain.fail()",
		})
	}()
	<-done

	select {
	case <-m.extensionErrorWakeCh:
		m.showPendingExtensionErrors()
	default:
		t.Fatal("the error did not wake the main loop")
	}
	got := chatPlainText(m)
	for _, want := range []string{`Extension "<agent-event>" error: panic: listener failed`, "  goroutine 7 [running]:", "  main.fail()"} {
		if !strings.Contains(got, want) {
			t.Fatalf("chat missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "panic: listener failed") != 1 {
		t.Fatalf("stack's first line should be skipped as in upstream:\n%s", got)
	}
}

// A stalled main loop never blocks the reporting goroutine, and every queued
// error is shown once it runs, as upstream shows every extension error.
func TestExtensionErrorQueueShowsEveryErrorWithoutBlocking(t *testing.T) {
	m := newExtensionErrorProbe(t)
	const reported = 150
	for i := range reported {
		m.queueExtensionError(&extension.ExtensionError{ExtensionPath: "x", Error: fmt.Sprintf("e%d", i)})
	}
	<-m.extensionErrorWakeCh
	m.showPendingExtensionErrors()
	got := chatPlainText(m)
	shown := make(map[string]bool)
	for line := range strings.SplitSeq(got, "\n") {
		shown[strings.TrimSpace(line)] = true
	}
	for i := range reported {
		if !shown[fmt.Sprintf(`Extension "x" error: e%d`, i)] {
			t.Fatalf("error e%d was not shown:\n%s", i, got)
		}
	}
	if strings.Contains(got, "were not shown") {
		t.Fatalf("errors were dropped:\n%s", got)
	}
}
