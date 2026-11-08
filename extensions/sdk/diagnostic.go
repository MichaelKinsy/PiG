package sdk

import (
	"fmt"
	"os"
	"sync/atomic"
)

// extLogEnv names the log the host keeps for the extension's process (coding/extension/host/subprocess ext_log.go). The SDK writes its own diagnostics there, among
// them the stack of a handler panic it recovered, because the host forwards the process's stderr to the user's terminal and Pi writes no stack for a throwing handler
// (print-mode.ts reports one line, interactive-mode.ts showExtensionError shows one). What the extension's code writes to stderr, and what the Go runtime writes for a
// fatal error, stays on stderr.
const extLogEnv = "PIG_EXT_LOG"

// diagnosticLogMax bounds the diagnostics a process writes to the log: the host bounds what a process writes to stderr (processStderrLog), and a handler that
// panics on every event must not fill the disk through this path. The diagnostic that crosses the bound is the last.
const diagnosticLogMax = 1 << 20

// diagnosticBytes counts what this process asked to write to the log, in bytes.
var diagnosticBytes atomic.Int64

// diagnosticf writes one SDK-owned diagnostic. It writes to stderr when the process has no log (no host started it) or the log cannot be written.
func diagnosticf(format string, args ...any) {
	writeDiagnostic([]byte(fmt.Sprintf(format, args...)))
}

func writeDiagnostic(text []byte) {
	if path := os.Getenv(extLogEnv); path != "" {
		total := diagnosticBytes.Add(int64(len(text)))
		if total > diagnosticLogMax {
			if total-int64(len(text)) <= diagnosticLogMax {
				_ = appendToLog(path, []byte("\n[... further SDK diagnostics dropped ...]\n"))
			}
			return
		}
		if appendToLog(path, text) == nil {
			return
		}
	}
	_, _ = os.Stderr.Write(text)
}

// appendToLog writes text in one write to the end of the file at path, which the host opened for appending too.
func appendToLog(path string, text []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	_, err = file.Write(text)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}
