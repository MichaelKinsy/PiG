package subprocess

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsstring"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// stderrCauseLimit bounds how much of an extension's stderr log is read to
// find the cause of a failed start.
// pig additive (D19): subprocess stderr diagnostics remain memory-bounded.
const stderrCauseLimit = 64 << 10

// maxStderrCauseLength bounds the cause copied into a load error message.
// pig additive (D19): subprocess stderr diagnostics remain display-bounded.
const maxStderrCauseLength = 500

// stderrCausePattern matches the line that states an error in the output of
// the extension runtimes: JavaScript and Python "<Name>Error: message" and
// "Error [CODE]: message" lines, Go "panic:" and "fatal error:" lines, and
// Rust "panicked at" lines (newer toolchains put the thread id after the name:
// "thread 'main' (4242) panicked at src/lib.rs:3:5:").
var stderrCausePattern = lazyregexp.New(`^([A-Za-z_][A-Za-z0-9_.]*(Error|Exception)(\s*\[[A-Z0-9_]+\])?:\s|panic:\s|fatal error:\s|thread '.*'( \(\d+\))? panicked at\s|Error:\s)`)

// stderrDrainWait bounds how long a failed start waits for the extension process to exit before its log is read for the cause. The host copies the process's stderr into the log, and the copy ends only when the process has exited, so a log read earlier can miss the lines the failing process wrote last.
// pig divergence (D56): the wait bounds only a load-failure diagnostic, never extension work.
const stderrDrainWait = 2 * time.Second

// awaitStderrDrained returns when the process behind the log has exited and its stderr is in the log, after stderrDrainWait, or when the load is cancelled. A load with no process of its own has nothing to wait for.
func awaitStderrDrained(ctx context.Context, exited <-chan struct{}) {
	if exited == nil {
		return
	}
	timer := time.NewTimer(stderrDrainWait)
	defer timer.Stop()
	select {
	case <-exited:
	case <-timer.C:
	case <-ctx.Done():
	}
}

// readStderrTail bounds diagnostic reads even when an extension produces a large log.
func readStderrTail(path string) []byte {
	if path == "" {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()
	if info, statErr := file.Stat(); statErr == nil && info.Size() > stderrCauseLimit {
		if _, seekErr := file.Seek(-stderrCauseLimit, io.SeekEnd); seekErr != nil {
			return nil
		}
	}
	data, err := io.ReadAll(io.LimitReader(file, stderrCauseLimit))
	if err != nil {
		return nil
	}
	return data
}

// stderrCause returns the error line belonging to an extension's failed start. A packed Node error block can start with a source location before its named error line; the selected member's error takes precedence over warnings and sibling blocks. It reads at most stderrCauseLimit bytes and returns an empty string for a missing or empty log.
func stderrCause(path, extensionName string) string {
	data := readStderrTail(path)
	var owned, matched, fallback string
	inOwnedBlock, ownedError := false, false
	// A Rust panic names its location on one line, ending in a colon, and its message on the next: the message is the cause.
	panicLocation := ""
	for rawLine := range bytes.Lines(data) {
		line := strings.TrimRight(string(rawLine), " \t\r\n")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "at ") {
			continue
		}
		if panicLocation != "" {
			if !strings.HasPrefix(trimmed, "note: ") {
				matched = panicLocation + " " + trimmed
			}
			panicLocation = ""
			continue
		}
		if cause, ok := strings.CutPrefix(trimmed, `extension "`+extensionName+`" failed to load: `); ok {
			owned = cause
			inOwnedBlock, ownedError = true, stderrCausePattern.MatchString(cause)
			continue
		}
		if strings.HasPrefix(trimmed, `extension "`) && strings.Contains(trimmed, `" failed to load: `) {
			inOwnedBlock = false
			continue
		}
		if stderrCausePattern.MatchString(trimmed) {
			matched = trimmed
			if strings.HasPrefix(trimmed, "thread '") && strings.HasSuffix(trimmed, ":") {
				panicLocation = trimmed
			}
			if inOwnedBlock && !ownedError {
				owned, ownedError = trimmed, true
			}
		}
		if line == trimmed {
			fallback = trimmed
		}
	}
	cause := owned
	if cause == "" {
		cause = matched
	}
	if cause == "" {
		cause = fallback
	}
	if jsstring.Length(cause) > maxStderrCauseLength {
		cause = jsstring.StreamText(jsstring.Slice(cause, 0, maxStderrCauseLength)) + "…"
	}
	return cause
}
