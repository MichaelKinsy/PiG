package extensionconformance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A handler that throws is, in Pi, one reported line: print mode writes "Extension error (path): message" (print-mode.ts) and interactive mode shows it in the
// UI (interactive-mode.ts showExtensionError). Pi never writes the stack to the console. A Go, Rust, Python or Lisp SDK writes its own diagnostics, among them the
// stack of a recovered panic, and since the host forwards an extension process's stderr to the terminal (console_output_test.go) that stack overprinted the TUI.
// An SDK writes its diagnostics to the extension's log instead (PIG_EXT_LOG), where the host's load and crash diagnostics read them, and only what the extension's
// own code writes reaches the terminal.
//
// Every SDK cell loads the fixture with a panic-probe command and an on-loaded handler that each write a deliberate stderr line (probe-err-loaded,
// probe-err-command) and then panic with probe-panic-boom. After a console-probe barrier (the same pipe carries its lines, so every earlier byte of the extension's
// stderr has been forwarded when its last line arrives) the terminal must hold the two deliberate lines and none of the SDK's report (the panic's message or
// its stack), in every mode, and the extension's log must hold the report. A panic of the extension's own that the SDK did not catch (the Rust fixture's factory
// probe registers an invalid tool and recovers it itself) is the extension's and stays on the terminal, so the row names the report by the panic's message.
func TestConformance_SDKDiagnosticsReachOnlyTheExtensionLog(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Cleanup(func() { rpcRowMode = "" })
	for _, mode := range []string{"print", "json", "rpc", "tui"} {
		rpcRowMode = mode
		for _, tc := range componentKitSDKCells() {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				t.Setenv("PIG_FIXTURE_PANIC_PROBE", "1")
				started := time.Now().Add(-250 * time.Millisecond)
				capture := swapProcessStd(t)
				h := startKitCell(t, tc)
				fused := strings.HasPrefix(tc.name, "fused")

				command, ok := findCommand(h.runner, "panic-probe")
				if !ok {
					t.Fatal("panic-probe is not registered")
				}
				if err := command.Handler(kitCommandContext(t, h), ""); err == nil {
					t.Fatal("panic-probe returned no error: the handler's panic was not reported to the host")
				}
				barrierArgs := ""
				if fused {
					barrierArgs = "stderr"
				}
				runKitCommand(t, h, "console-probe", barrierArgs)
				waitFor(t, func() bool {
					out, errOut := capture.snapshot()
					return strings.Contains(out+errOut, "probe-err-2")
				})
				out, errOut := capture.stop()
				terminal := out + errOut

				for _, line := range []string{"probe-err-loaded", "probe-err-command"} {
					if !strings.Contains(errOut, line+"\n") {
						t.Errorf("%s: the extension's own stderr line %q did not reach the terminal's stderr: stdout %q stderr %q", tc.name, line, out, errOut)
					}
				}
				for _, report := range []string{"probe-panic-boom", "goroutine ", "Traceback", "recovered from", "debug.Stack"} {
					if strings.Contains(terminal, report) {
						t.Errorf("%s: the terminal holds the SDK's own report (%q):\n%s", tc.name, report, terminal)
						break
					}
				}

				log := extensionLogs(t, started)
				if !strings.Contains(log, "probe-panic-boom") {
					t.Errorf("%s: no extension log holds the report of the panic (probe-panic-boom):\n%s", tc.name, log)
				}
				if marker := sdkStackMarker(tc.name); marker != "" && !strings.Contains(log, marker) {
					t.Errorf("%s: the extension log lacks the stack (%q):\n%s", tc.name, marker, log)
				}
				if !fused && !strings.Contains(log, "probe-err-command") {
					t.Errorf("%s: the extension log lacks the extension's own stderr line probe-err-command (the host copies stderr to it):\n%s", tc.name, log)
				}
			})
		}
	}
}

// sdkStackMarker is text only the stack an SDK reports for a panic in a handler holds, or "" for an SDK whose report is the condition's message alone.
func sdkStackMarker(cell string) string {
	switch {
	case strings.HasSuffix(cell, "go"):
		return "goroutine "
	case strings.HasSuffix(cell, "rust"):
		return "panicked at"
	case strings.HasSuffix(cell, "python"):
		return "Traceback"
	case strings.Contains(cell, "node"):
		return "Error: probe-panic-boom\n    at "
	}
	return ""
}

// extensionLogs is every extension log a host wrote since started, joined: the isolated process's (pig-ext-<name>-*.log), the packed process's
// (pig-packed-<key>-*.log) and the log the fused extensions share (pig-ext-fused-*.log). A host reads its log by the path it chose, which no row can name, so
// the row reads the temporary directory.
func extensionLogs(t *testing.T, started time.Time) string {
	t.Helper()
	var all strings.Builder
	for _, pattern := range []string{"pig-ext-*.log", "pig-packed-*.log"} {
		paths, err := filepath.Glob(filepath.Join(os.TempDir(), pattern))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			info, err := os.Stat(path)
			if err != nil || info.ModTime().Before(started) {
				continue
			}
			if data, err := os.ReadFile(path); err == nil {
				all.WriteString(string(data))
			}
		}
	}
	return all.String()
}
