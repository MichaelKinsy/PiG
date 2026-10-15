package tui

// Ports packages/tui/src/tui-main-screen.ts (logRedraw).

import (
	"fmt"
	"os"
	"path/filepath"
)

// logRedraw appends redraw reasons only to the configured log directory when PI_TUI_DEBUG_REDRAW is enabled. Synchronous filesystem failures propagate to the rendering caller.
func (t *TuiMainScreen) logRedraw(reason string, newLen, height int) {
	if os.Getenv("PI_TUI_DEBUG_REDRAW") != "1" || t.logDirectory == "" {
		return
	}
	path := filepath.Join(t.logDirectory, "pi-tui-debug.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		panic(err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			panic(err)
		}
	}()
	_, err = fmt.Fprintf(file, "[%s] fullRender: %s (prev=%d, new=%d, height=%d)\n", t.now().UTC().Format("2006-01-02T15:04:05.000Z"), reason, len(t.prevLines), newLen, height)
	if err != nil {
		panic(err)
	}
}
