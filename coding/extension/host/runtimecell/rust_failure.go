package runtimecell

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// BuildRun is what the caller knows about a `cargo build` run that the command's output does not say: how long it ran and whether its context had ended.
type BuildRun struct {
	// Elapsed is the time from starting the command to its exit.
	Elapsed time.Duration
	// ContextErr is the error of the context the command ran under, nil when it had not ended.
	ContextErr error
}

// RustBuildFailure reports a failed `cargo build` run in dir: one summary line, and
// the complete output in a log under cacheRoot. recordable reports whether the
// same inputs fail again. That holds for a rustc diagnostic, which names a
// source position. Dependency resolution and network failures do not, so they
// are reported but never recorded. A run that left no diagnostic (cargo or rustc
// killed by a signal, a context that ended) says how it ended: the exit status or
// signal, the process cargo reports as not exiting successfully, how long it ran
// and whether its context had ended.
func RustBuildFailure(cacheRoot, logKey, dir string, buildErr error, output []byte, run BuildRun) (failure *BuildFailure, recordable bool) {
	summary, cause, positioned := "cargo build failed", "cargo build failed", false
	ended := ""
	if buildErr != nil {
		ended = oneLine(buildErr.Error())
	}
	if crashed := rustProcessCrash(output); crashed != "" {
		ended = oneLine(crashed + "; " + ended)
	}
	if run.ContextErr != nil {
		ended += "; the build's context ended: " + run.ContextErr.Error()
	}
	if ended != "" {
		cause = "cargo build failed: " + ended
		summary = cause
	}
	if run.Elapsed > 0 {
		summary += " after " + run.Elapsed.Round(time.Millisecond).String()
	}
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
	fmt.Fprintf(&log, "time: %s\ndirectory: %s\nerror: %v\nelapsed: %s\ncontext: %v\n\n--- cargo build output ---\n", time.Now().UTC().Format(time.RFC3339), dir, buildErr, run.Elapsed, run.ContextErr)
	log.Write(output)
	return &BuildFailure{Summary: summary, Cause: cause, Log: writeBuildLog(cacheRoot, logKey, log.Bytes())}, recordable
}

// rustProcessCrash names the process cargo reports as not exiting successfully, with how it ended, for example "rustc for `serde` (signal: 9, SIGKILL: kill)": cargo
// prints it under "Caused by:" after "could not compile", and a rustc killed from outside leaves no diagnostic.
func rustProcessCrash(output []byte) string {
	const marker = "process didn't exit successfully:"
	text := string(output)
	before, after, ok := strings.Cut(text, marker)
	if !ok {
		return ""
	}
	line, _, _ := strings.Cut(after, "\n")
	line = strings.TrimSpace(line)
	status := ""
	if open := strings.LastIndex(line, " ("); open >= 0 && strings.HasSuffix(line, ")") {
		status = line[open+1:]
		line = line[:open]
	}
	program, _, _ := strings.Cut(strings.Trim(strings.TrimSpace(line), "`"), " ")
	program = filepath.Base(program)
	crate := ""
	if strings.Contains(before, "could not compile `") {
		_, rest, _ := strings.Cut(before[strings.LastIndex(before, "could not compile `"):], "`")
		crate, _, _ = strings.Cut(rest, "`")
	}
	out := program
	if crate != "" {
		out += " for `" + crate + "`"
	}
	if status != "" {
		out += " " + status
	}
	return out
}
