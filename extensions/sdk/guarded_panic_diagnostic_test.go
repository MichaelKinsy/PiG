package sdk

import (
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// Pi writes no stack to the console for a callback of an extension that throws: interactive-mode.ts showExtensionError shows one line and print-mode.ts reports
// one. The host forwards an extension process's stderr to the user's terminal, so the stack of a panic the SDK recovered belongs to the extension's log
// (PIG_EXT_LOG), where the SDK's other recovered panics go (reportRecoveredPanic), and not to stderr.

func readDiagnosticLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestGuardedCallbackPanicStackGoesToTheExtensionLogAndNotStderr(t *testing.T) {
	path := newDiagnosticLog(t)
	var err error
	stderr := captureStderr(t, func() {
		err = callbackError("a probe store's WithLock", func() error { panic("callback-boom") })
	})
	if err == nil || err.Error() != "a probe store's WithLock panicked: callback-boom" {
		t.Errorf("error = %v, want the panic as the call's error", err)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing: the stack belongs to the extension log", stderr)
	}
	log := readDiagnosticLog(t, path)
	for _, want := range []string{"host wrote this line\n", "extension: recovered from panic in a probe store's WithLock: callback-boom", "goroutine "} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

func TestEditorComponentPanicStacksGoToTheExtensionLogAndNotStderr(t *testing.T) {
	path := newDiagnosticLog(t)
	host, peer := net.Pipe()
	defer func() { _ = host.Close(); _ = peer.Close() }()
	frames := readFrames(t, peer)
	s := &editorSession{ext: &Extension{conn: &conn{nc: host}}, key: "editor-1", wake: make(chan struct{}, 1), done: make(chan struct{})}
	guarded := true
	stderr := captureStderr(t, func() {
		s.runTask(func() { panic("task-boom") })
		guarded = s.guard("handleInput", func() error { panic("guard-boom") })
	})
	if guarded {
		t.Error("guard reported success for a call that panicked")
	}
	select {
	case frame := <-frames:
		if !strings.HasPrefix(frame, "ui.notify ") || !strings.Contains(frame, "editor handleInput failed: guard-boom") {
			t.Errorf("frame = %q, want the one-line \"editor handleInput failed: guard-boom\" notice", frame)
		}
	case <-time.After(10 * time.Second):
		t.Error("no notice of the failed editor call reached the host")
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing: the stacks belong to the extension log", stderr)
	}
	log := readDiagnosticLog(t, path)
	for _, want := range []string{"extension: recovered from panic in an editor task: task-boom", "extension: recovered from panic in editor handleInput: guard-boom", "goroutine "} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}
