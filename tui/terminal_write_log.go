package tui

// Ports packages/tui/src/terminal.ts (PI_TUI_WRITE_LOG: writeLogPath, ProcessTerminal.write).

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// terminalWriteLog is the file PI_TUI_WRITE_LOG names. Each append opens the file, writes and closes it, as upstream's
// appendFileSync does, and a logging failure never reaches the terminal.
type terminalWriteLog struct{ path string }

func (l *terminalWriteLog) append(data []byte) {
	if file, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666); err == nil {
		_, _ = file.Write(data)
		_ = file.Close()
	}
}

// loggingWriter writes to the terminal and then to the log, as upstream's ProcessTerminal.write does.
type loggingWriter struct {
	out io.Writer
	log *terminalWriteLog
}

func (w *loggingWriter) Write(data []byte) (int, error) {
	n, err := w.out.Write(data)
	w.log.append(data)
	return n, err
}

// terminalWriteLogPath resolves the PI_TUI_WRITE_LOG value: an existing directory gets a per-process file named
// tui-<local date>_<local time>-<pid>.log, and any other value is the file path.
func terminalWriteLogPath(value string, now time.Time, pid int) string {
	if value == "" {
		return ""
	}
	if info, err := os.Stat(value); err == nil && info.IsDir() {
		stamp := fmt.Sprintf("%04d-%02d-%02d_%02d-%02d-%02d", now.Year(), int(now.Month()), now.Day(), now.Hour(), now.Minute(), now.Second())
		return filepath.Join(value, fmt.Sprintf("tui-%s-%d.log", stamp, pid))
	}
	return value
}

var resolvedWriteLogs sync.Map // PI_TUI_WRITE_LOG value -> log path

// newTerminalWriteLog opens the log PI_TUI_WRITE_LOG names, or returns nil when it is unset. PiG's renderer and its terminal
// helper are separate objects over one terminal, so a directory value resolves to one file per process, not one per object.
func newTerminalWriteLog() *terminalWriteLog {
	value := os.Getenv("PI_TUI_WRITE_LOG")
	if value == "" {
		return nil
	}
	//portlint:allow clock the log file name carries the process start time (Pi tui terminal.ts new Date()); the TUI.now seam times renders and does not reach a terminal write log
	path, _ := resolvedWriteLogs.LoadOrStore(value, terminalWriteLogPath(value, time.Now(), os.Getpid()))
	return &terminalWriteLog{path: path.(string)}
}
