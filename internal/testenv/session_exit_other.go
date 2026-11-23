//go:build !darwin

package testenv

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// pgrepDiagnosticLimit bounds stderr and version output attached to a pgrep failure so a CI log line stays readable.
const pgrepDiagnosticLimit = 512

// LiveSessionProcesses lists the processes of terminal session sid that have not exited. A zombie has exited and writes nothing more; it stays in the process table until its parent reaps it, which on a loaded host can lag the exit by seconds, so it does not count.
func LiveSessionProcesses(sid int) ([]int, error) {
	//nolint:gosec // G204 (class a): program is literal pgrep; the argument is a process ID formatted by strconv.
	members, err := exec.Command("pgrep", "-s", strconv.Itoa(sid)).Output()
	if err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); ok && exit.ExitCode() == 1 {
			return nil, nil // pgrep exits 1 when no process matches.
		}
		return nil, formatPgrepError(sid, err)
	}
	var stderr bytes.Buffer
	//nolint:gosec // G204 (class a): program is literal ps; the argument is the list of process IDs that pgrep printed.
	states := exec.Command("ps", "-o", "pid=,stat=", "-p", strings.Join(strings.Fields(string(members)), ","))
	states.Stderr = &stderr
	out, err := states.Output()
	if err != nil {
		// ps exits 1 without a word when every listed process has gone since pgrep listed it.
		if exit, ok := errors.AsType[*exec.ExitError](err); ok && exit.ExitCode() == 1 && stderr.Len() == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("ps: %w: %s", err, stderr.String())
	}
	var live []int
	for line := range strings.Lines(string(out)) {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("ps reported %q, want a process ID and a state", line)
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			return nil, fmt.Errorf("ps process ID %q: %w", fields[0], err)
		}
		if !strings.HasPrefix(fields[1], "Z") {
			live = append(live, pid)
		}
	}
	return live, nil
}

func formatPgrepError(sid int, err error) error {
	var stderrText string
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		stderrText = boundDiagnosticText(exit.Stderr)
	}
	pgrepPath, lookErr := exec.LookPath("pgrep")
	if lookErr != nil {
		pgrepPath = fmt.Sprintf("LookPath failed: %v", lookErr)
	}
	versionLine := probePgrepVersion()
	return fmt.Errorf("pgrep -s %d: %w\npgrep stderr: %s\npgrep path: %s\npgrep -V: %s",
		sid, err, stderrText, pgrepPath, versionLine)
}

func probePgrepVersion() string {
	out, err := exec.Command("pgrep", "-V").Output()
	if err == nil {
		return boundDiagnosticText(out)
	}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		if text := boundDiagnosticText(exit.Stderr); text != "" {
			return fmt.Sprintf("failed (%v): %s", err, text)
		}
	}
	return fmt.Sprintf("failed (%v)", err)
}

func boundDiagnosticText(raw []byte) string {
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) <= pgrepDiagnosticLimit {
		return trimmed
	}
	return trimmed[:pgrepDiagnosticLimit] + "...(truncated)"
}
