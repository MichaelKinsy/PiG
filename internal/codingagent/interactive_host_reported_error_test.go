package codingagent

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// The subprocess host reports every error of a pi.events listener, and the diagnostic of a refused report_error tag, through the runtime's error reporter (ExtensionRuntime.ReportRuntimeError, Pi's runtime.reportError), never to the terminal's own stderr. In the interactive mode that reporter reaches the runner's error listeners, which show the error as a chat line; nothing is written to the process's stdout or stderr, which the screen's frames own.
// Pi: packages/coding-agent/src/core/extensions/runner.ts:411-414 (runtime.reportError), runner.ts:472 (emitError).
func TestInteractiveShowsAnErrorTheHostReportsThroughTheRuntimeAndWritesNothingToTheTerminal(t *testing.T) {
	m := newExtensionErrorProbe(t)
	runtime := extension.CreateExtensionRuntime()
	m.newRunner = inproc.NewRunner(nil, t.TempDir(), runtime)
	m.wireInprocContextActions()

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdoutW, stderrW
	restored := false
	restore := func() {
		if !restored {
			restored = true
			os.Stdout, os.Stderr = stdout, stderr
		}
	}
	t.Cleanup(restore)

	runtime.ReportRuntimeError(&extension.ExtensionError{ExtensionPath: "/ext/old.mjs", Event: "events:ch", Error: `report_error names the extension "foreign", which does not run in the process of old: reported as old's error`})
	runtime.ReportRuntimeError(&extension.ExtensionError{ExtensionPath: "/ext/old.mjs", Event: "events:ch", Error: "by another process"})
	restore()
	_ = stdoutW.Close()
	_ = stderrW.Close()
	gotStdout, _ := io.ReadAll(stdoutR)
	gotStderr, _ := io.ReadAll(stderrR)
	if len(gotStdout) != 0 || len(gotStderr) != 0 {
		t.Fatalf("the report wrote to the terminal: stdout %q, stderr %q, want nothing outside the screen's frames", gotStdout, gotStderr)
	}

	select {
	case <-m.extensionErrorWakeCh:
		m.showPendingExtensionErrors()
	default:
		t.Fatal("the reported error did not wake the main loop")
	}
	chat := chatPlainText(m)
	refusal := strings.Index(chat, `Extension "/ext/old.mjs" error: report_error names the extension "foreign"`)
	report := strings.Index(chat, `Extension "/ext/old.mjs" error: by another process`)
	if refusal < 0 || report < 0 || refusal > report {
		t.Fatalf("chat = %q, want the refusal's error line then the report's, both as extension errors of the connection's extension", chat)
	}
}
