package codingagent

// exit_record.go records why an interactive process ended when no user action asked it to. Pi leaves a trace only for
// an uncaught JavaScript exception (crashes.json); a signal, a closed terminal or a closed input exits with no trace.
// Go adds two more silent endings: a panic or fatal error in a goroutine nobody recovers (it prints to stderr, which a
// closed terminal discards) and a kill by the OS (SIGKILL, memory pressure), which no code in the process can observe.
//
// pig additive (D102): exit.log and crash-output/ in the agent directory.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	exitLogName    = "exit.log"
	crashOutputDir = "crash-output"
	// pig additive (D102): exit.log bounds are PiG's own; Pi keeps no such log.
	maxExitLogBytes = 64 << 10
	// pig additive (D102): see maxExitLogBytes.
	keptExitLogLines  = 100
	crashOutputSuffix = ".log"
)

var exitLogMu sync.Mutex

// ExitLogPath returns the log of unexpected exits for an agent directory.
func ExitLogPath(agentDir string) string { return filepath.Join(agentDir, exitLogName) }

// CrashOutputDir returns the directory that holds one marker file per running interactive session.
func CrashOutputDir(agentDir string) string { return filepath.Join(agentDir, crashOutputDir) }

// RecordExit appends one line saying why this process is ending without a user request. It is best effort: the caller
// is already exiting, and a failed write must not change how it exits.
func RecordExit(agentDir, reason string) {
	debugLog("exit: %s", reason)
	appendExitLog(agentDir, time.Now(), os.Getpid(), os.Getppid(), reason)
}

// SignalName names a termination signal the way a shell does.
func SignalName(sig os.Signal) string {
	switch sig {
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGTERM:
		return "SIGTERM"
	}
	return sig.String()
}

func appendExitLog(agentDir string, at time.Time, pid, ppid int, reason string) {
	if agentDir == "" {
		return
	}
	reason = strings.Join(strings.Fields(reason), " ")
	line := fmt.Sprintf("%s pid=%d ppid=%d %s\n", at.Format("2006-01-02T15:04:05.000Z07:00"), pid, ppid, reason)
	path := ExitLogPath(agentDir)
	exitLogMu.Lock()
	defer exitLogMu.Unlock()
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return
	}
	if info, err := os.Stat(path); err == nil && info.Size() > maxExitLogBytes {
		trimExitLog(path)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = file.WriteString(line)
	_ = file.Close()
}

func trimExitLog(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	if len(lines) > keptExitLogLines {
		lines = lines[len(lines)-keptExitLogLines:]
	}
	// pig additive (D102): a failed trim leaves a longer log, and the caller still appends its line.
	_ = os.WriteFile(path, bytes.Join(lines, nil), 0o644)
}

// ReadExitLogTail returns the newest n lines of the exit log.
func ReadExitLogTail(agentDir string, n int) []string {
	data, err := os.ReadFile(ExitLogPath(agentDir))
	if err != nil {
		return nil
	}
	var lines []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	// pig additive (D102): a line longer than this ends the tail; a reason is a few hundred bytes.
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// sessionMarkerHeader is the first line of a marker file. Go appends its crash report after it.
type sessionMarkerHeader struct {
	PID     int    `json:"pid"`
	PPID    int    `json:"ppid"`
	Release string `json:"release"`
	CWD     string `json:"cwd"`
	Started string `json:"started"`
}

var (
	sessionMarkerMu   sync.Mutex
	sessionMarkerPath string
)

// BeginSessionMarker creates this process's marker file in the agent directory and has the Go runtime copy any
// unrecovered panic or fatal error into it, in addition to stderr. A clean end removes the file (EndSessionMarker). A
// file left behind therefore means the process ended some other way, and ReconcileSessionMarkers reports it at the next
// start. It is best effort: an unwritable agent directory loses the trace, not the session.
func BeginSessionMarker(agentDir, version, cwd string) {
	if agentDir == "" {
		return
	}
	dir := CrashOutputDir(agentDir)
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	path := filepath.Join(dir, strconv.Itoa(os.Getpid())+crashOutputSuffix)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	header, _ := json.Marshal(sessionMarkerHeader{PID: os.Getpid(), PPID: os.Getppid(), Release: version, CWD: cwd, Started: time.Now().Format(time.RFC3339)})
	if _, err := file.Write(append(header, '\n')); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return
	}
	// SetCrashOutput duplicates the descriptor, so the file shares its offset and appends after the header.
	err = debug.SetCrashOutput(file, debug.CrashOptions{})
	_ = file.Close()
	if err != nil {
		_ = os.Remove(path)
		return
	}
	sessionMarkerMu.Lock()
	sessionMarkerPath = path
	sessionMarkerMu.Unlock()
}

// EndSessionMarker removes this process's marker file after an exit that already left its own trace or was requested.
func EndSessionMarker() {
	sessionMarkerMu.Lock()
	path := sessionMarkerPath
	sessionMarkerPath = ""
	sessionMarkerMu.Unlock()
	if path == "" {
		return
	}
	_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
	_ = os.Remove(path)
}

// ReconcileSessionMarkers reports every session whose marker file outlived its process. A marker with a Go crash report
// becomes a crash record, shown at this start and attached by /bug. A marker without one became an exit.log line: the
// session was killed from outside (SIGKILL, memory pressure, power loss) or died where Go could not write.
func ReconcileSessionMarkers(agentDir string, now time.Time) {
	if agentDir == "" {
		return
	}
	entries, err := os.ReadDir(CrashOutputDir(agentDir))
	if err != nil {
		return
	}
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), crashOutputSuffix)
		pid, convErr := strconv.Atoi(name)
		if !ok || convErr != nil || pid == os.Getpid() || processAlive(pid) {
			continue
		}
		reconcileSessionMarker(agentDir, filepath.Join(CrashOutputDir(agentDir), entry.Name()), pid, now)
	}
}

func reconcileSessionMarker(agentDir, path string, pid int, now time.Time) {
	// Claim the file first so two starting processes do not both report it.
	claimed := path + ".claimed-" + strconv.Itoa(os.Getpid())
	if os.Rename(path, claimed) != nil {
		return
	}
	defer func() { _ = os.Remove(claimed) }()
	data, err := os.ReadFile(claimed)
	if err != nil {
		return
	}
	// The runtime writes its report when the process dies, so the file's modification time is the crash time, which the
	// record keeps as Pi's recordCrash keeps the time of the throw.
	crashedAt := now
	if info, statErr := os.Stat(claimed); statErr == nil {
		crashedAt = info.ModTime()
	}
	headerLine, report, _ := strings.Cut(string(data), "\n")
	var header sessionMarkerHeader
	_ = json.Unmarshal([]byte(headerLine), &header)
	report = strings.TrimSpace(report)
	if report == "" {
		appendExitLog(agentDir, now, pid, header.PPID, fmt.Sprintf("session started %s in %s ended without recording an exit: the OS killed it (SIGKILL, memory pressure, power loss) or it died where Go could not report", header.Started, header.CWD))
		return
	}
	message, rest, _ := strings.Cut(report, "\n")
	kind := "uncaught_exception"
	switch {
	case strings.HasPrefix(report, "fatal error:"):
		kind = "fatal_error"
	case strings.HasPrefix(report, "goroutine "):
		// The runtime prints "fatal error: <message>" to stderr only; its copy starts at the goroutine header.
		kind = "fatal_error"
		topFrame, _, _ := strings.Cut(strings.TrimSpace(rest), "\n")
		message = "fatal error: the Go runtime stopped the process; its message is on stderr only, top frame " + topFrame
	}
	RecordCrash(CrashInput{Kind: kind, Message: message, Stack: report, CWD: header.CWD, Version: header.Release}, CrashLogPath(agentDir), crashedAt)
	appendExitLog(agentDir, crashedAt, pid, header.PPID, "session crashed in a goroutine: "+message+" (recorded in crashes.json)")
}
