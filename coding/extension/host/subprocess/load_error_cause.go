package subprocess

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"strings"

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
// Rust "panicked at" lines.
var stderrCausePattern = lazyregexp.New(`^([A-Za-z_][A-Za-z0-9_.]*(Error|Exception)(\s*\[[A-Z0-9_]+\])?:\s|panic:\s|fatal error:\s|thread '.*' panicked at\s|Error:\s)`)

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
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 4096), stderrCauseLimit)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), " \t")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "at ") {
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
	if len(cause) > maxStderrCauseLength {
		cause = cause[:maxStderrCauseLength] + "…"
	}
	return cause
}
