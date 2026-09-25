package tui

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var (
	renderCaptureOnce    sync.Once
	renderCaptureEnabled bool
)

// renderCaptureOn reports whether render-frame capture is enabled
// (PIG_RENDER_DEBUG set). Read once; near-zero cost on the render path.
func renderCaptureOn() bool {
	renderCaptureOnce.Do(func() {
		renderCaptureEnabled = os.Getenv("PIG_RENDER_DEBUG") != ""
	})
	return renderCaptureEnabled
}

// appendRenderCapture logs the buffers and diff decision of a render whose line
// count changed. Gated by PIG_RENDER_DEBUG. The two buffers can be replayed
// through the scroll-aware VT in render_grow_test.go to reproduce (or rule out)
// a transient stale-row artifact deterministically, so a fix can be proven
// rather than guessed. Appends to $PIG_HOME/agent/pig-render.log (or ~/.pig/...).
func appendRenderCapture(prevLines, newLines []string, firstChanged, lastChanged, hwCursorRow, prevViewportTop, height int) {
	dir := os.Getenv("PIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = home + "/.pig"
	}
	if err := os.MkdirAll(dir+"/agent", 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(dir+"/agent/pig-render.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "\n=== render %s | prev=%d new=%d firstChanged=%d lastChanged=%d hwCursorRow=%d prevViewportTop=%d height=%d ===\n",
		time.Now().Format(time.RFC3339Nano), len(prevLines), len(newLines), firstChanged, lastChanged, hwCursorRow, prevViewportTop, height)
	_, _ = fmt.Fprintln(f, "--- prevLines ---")
	for i, l := range prevLines {
		_, _ = fmt.Fprintf(f, "[%d] %q\n", i, l)
	}
	_, _ = fmt.Fprintln(f, "--- newLines ---")
	for i, l := range newLines {
		_, _ = fmt.Fprintf(f, "[%d] %q\n", i, l)
	}
}
