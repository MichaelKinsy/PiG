package runtimecell

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// BuildFailure is a compiler failure reduced to what a person reads: one line, a
// cause that failures of the same kind share, and the file that holds the full
// compiler output. The compiler output never travels in the error, so a
// failed build cannot fill a terminal.
//
// pig additive (D20): PiG compiles extensions, which Pi does not, so Pi has no
// counterpart to this report.
type BuildFailure struct {
	// Summary is one line naming what failed.
	Summary string
	// Cause is the part of Summary that does not depend on which extension
	// failed. Failures with equal Cause have one root cause.
	Cause string
	// Log is the file holding the complete compiler output, or empty when it
	// could not be written.
	Log string
	// Cached is set when the failure is a recorded earlier one: the build did not
	// run again because none of its inputs changed.
	Cached bool

	// cause is the error the failure was recorded from, when it was not already a report, so errors.Is and errors.As reach it.
	cause error
}

// Unwrap returns the error the failure was recorded from, or nil for a report that was built directly or read from the cache.
func (e *BuildFailure) Unwrap() error { return e.cause }

// RetryCachedBuildCommand is the command that clears recorded build failures so
// the next start compiles again.
const RetryCachedBuildCommand = "pig extensions cache prune --failures"

func (e *BuildFailure) Error() string {
	prefix := ""
	if e.Cached {
		prefix = "cached build failure (inputs unchanged): "
	}
	return prefix + e.Summary + e.Detail("")
}

// Detail is the parenthetical that points at the log and, for a recorded failure, at
// the way to retry. lead is placed inside it before those, for example the
// extensions a group covers.
func (e *BuildFailure) Detail(lead string) string {
	var parts []string
	if lead != "" {
		parts = append(parts, lead)
	}
	if e.Log != "" {
		parts = append(parts, "details: "+e.Log)
	}
	if e.Cached {
		parts = append(parts, "retry without changing an input: "+RetryCachedBuildCommand)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, "; ") + ")"
}

// maxSummaryRunes bounds a summary line. Compiler messages quote user code.
// pig additive (D20): PiG compiles extensions, which Pi does not.
const maxSummaryRunes = 240

func oneLine(text string) string {
	line := strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(line) > maxSummaryRunes {
		runes := []rune(line)
		line = string(runes[:maxSummaryRunes-1]) + "…"
	}
	return line
}

// pig additive (D20): the newest maxBuildLogs build logs are kept, each at most maxBuildLogBytes, so failures cannot grow the cache without bound.
const (
	buildLogDirectory = "logs"
	maxBuildLogs      = 64
	maxBuildLogBytes  = 4 << 20
)

// writeBuildLog stores the complete output of one failed build under the cache
// root and returns its path, or "" when it cannot be written. The newest
// maxBuildLogs logs are kept and each is bounded, so failures cannot grow the
// cache without limit.
func writeBuildLog(cacheRoot, name string, content []byte) string {
	directory := filepath.Join(cacheRoot, buildLogDirectory)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return ""
	}
	if len(content) > maxBuildLogBytes {
		omitted := len(content) - maxBuildLogBytes
		content = append([]byte(fmt.Sprintf("[%d earlier bytes omitted]\n", omitted)), content[omitted:]...)
	}
	sum := sha256.Sum256([]byte(name))
	path := filepath.Join(directory, "build-"+hex.EncodeToString(sum[:6])+".log")
	temporary, err := os.CreateTemp(directory, ".build-log-*")
	if err != nil {
		return ""
	}
	temporaryPath := temporary.Name()
	_, writeErr := temporary.Write(content)
	if closeErr := temporary.Close(); writeErr != nil || closeErr != nil {
		_ = os.Remove(temporaryPath)
		return ""
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		_ = os.Remove(temporaryPath)
		return ""
	}
	trimBuildLogs(directory)
	return path
}

func trimBuildLogs(directory string) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	type logFile struct {
		path string
		when time.Time
	}
	var logs []logFile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "build-") {
			continue
		}
		if info, err := entry.Info(); err == nil {
			logs = append(logs, logFile{filepath.Join(directory, entry.Name()), info.ModTime()})
		}
	}
	if len(logs) <= maxBuildLogs {
		return
	}
	slices.SortFunc(logs, func(a, b logFile) int { return b.when.Compare(a.when) })
	for _, old := range logs[maxBuildLogs:] {
		_ = os.Remove(old.path)
	}
}

// diagnosticMessage returns the message of a positioned compiler line
// (`file.go:6:43: message`), and the line with its directory removed.
func diagnosticMessage(line []byte) (message, located string, ok bool) {
	if !isGoSourceDiagnostic(line) {
		return "", "", false
	}
	text := string(bytes.TrimSpace(line))
	before, after, _ := strings.Cut(text, ".go:")
	start := strings.LastIndexAny(before, `/\`) + 1
	located = text[start:]
	rest := after
	for range 2 {
		digits := len(rest) - len(strings.TrimLeft(rest, "0123456789"))
		if digits == 0 || digits >= len(rest) || rest[digits] != ':' {
			break
		}
		rest = rest[digits+1:]
	}
	return strings.TrimSpace(rest), located, true
}
