package subprocess

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"regexp"
	"strings"
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
// Rust "panicked at" lines.
var stderrCausePattern = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_.]*(Error|Exception)(\s*\[[A-Z0-9_]+\])?:\s|panic:\s|fatal error:\s|thread '.*' panicked at\s|Error:\s)`)

// stderrCause returns the error line an extension process wrote before
// exiting, so a load error states the loader's cause the way upstream's
// "Failed to load extension: <message>" does. It reads at most the last
// stderrCauseLimit bytes of the log and returns the last matching line, or the
// last non-empty line that is not a stack frame. It returns "" when the log is
// missing or empty.
func stderrCause(path string) string {
	if path == "" {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	if info, statErr := file.Stat(); statErr == nil && info.Size() > stderrCauseLimit {
		if _, seekErr := file.Seek(-stderrCauseLimit, io.SeekEnd); seekErr != nil {
			return ""
		}
	}
	data, err := io.ReadAll(io.LimitReader(file, stderrCauseLimit))
	if err != nil {
		return ""
	}
	var matched, fallback string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 4096), stderrCauseLimit)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), " \t")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "at ") {
			continue
		}
		if stderrCausePattern.MatchString(trimmed) {
			matched = trimmed
		}
		if line == trimmed {
			fallback = trimmed
		}
	}
	cause := matched
	if cause == "" {
		cause = fallback
	}
	if len(cause) > maxStderrCauseLength {
		cause = cause[:maxStderrCauseLength] + "…"
	}
	return cause
}
