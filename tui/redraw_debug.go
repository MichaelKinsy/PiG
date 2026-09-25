package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Redraw-reason logging follows upstream tui-main-screen.ts logRedraw.
//
// A full redraw repaints the whole buffer and discards terminal scrollback, so
// the reader loses their place. Which branch decided that is not recoverable
// from the output because every full-redraw branch emits the same bytes.

var (
	redrawDebugOnce    sync.Once
	redrawDebugEnabled bool
)

// redrawDebug reports whether redraw-reason logging is on.
func redrawDebug() bool {
	redrawDebugOnce.Do(func() {
		redrawDebugEnabled = os.Getenv("PI_TUI_DEBUG_REDRAW") == "1"
	})
	return redrawDebugEnabled
}

// logRedraw appends one line naming why the whole buffer is being repainted.
// Mirrors upstream's record: reason, previous and new buffer lengths, height.
func (t *TUI) logRedraw(reason string, newLen, height int) {
	if !redrawDebug() {
		return
	}
	logDir := os.Getenv("PIG_HOME")
	if logDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		logDir = filepath.Join(home, ".pig")
	}
	dir := filepath.Join(logDir, "agent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "pig-debug.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close() //nolint:errcheck // diagnostic log
	_, _ = fmt.Fprintf(f, "[%s] fullRender: %s (prev=%d, new=%d, height=%d)\n",
		time.Now().UTC().Format(time.RFC3339), reason, len(t.prevLines), newLen, height)
}
