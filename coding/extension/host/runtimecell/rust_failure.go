package runtimecell

import (
	"bytes"
	"fmt"
	"strings"
	"time"
)

// RustBuildFailure reports a failed `cargo build` run in dir: one summary line, and
// the complete output in a log under cacheRoot. recordable reports whether the
// same inputs fail again. That holds for a rustc diagnostic, which names a
// source position. Dependency resolution and network failures do not, so they
// are reported but never recorded.
func RustBuildFailure(cacheRoot, logKey, dir string, buildErr error, output []byte) (failure *BuildFailure, recordable bool) {
	summary, cause, positioned := "cargo build failed", "cargo build failed", false
	lines := strings.Split(string(output), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "error") || strings.HasPrefix(trimmed, "error: could not compile") || strings.HasPrefix(trimmed, "error: aborting") {
			continue
		}
		message := oneLine("cargo build: " + trimmed)
		summary, cause = message, message
		for _, next := range lines[i+1 : min(i+4, len(lines))] {
			if location, ok := strings.CutPrefix(strings.TrimSpace(next), "--> "); ok {
				summary = oneLine(message + " (" + location + ")")
				positioned = true
				break
			}
		}
		break
	}
	recordable = positioned && bytes.Contains(output, []byte("could not compile"))
	var log bytes.Buffer
	fmt.Fprintf(&log, "time: %s\ndirectory: %s\nerror: %v\n\n--- cargo build output ---\n", time.Now().UTC().Format(time.RFC3339), dir, buildErr)
	log.Write(output)
	return &BuildFailure{Summary: summary, Cause: cause, Log: writeBuildLog(cacheRoot, logKey, log.Bytes())}, recordable
}
