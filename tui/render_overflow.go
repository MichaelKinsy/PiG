package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// TUICrashLogName is the crash log written when an over-wide row reaches the
// main screen's differential-render loop. Upstream writes pi-tui-crash.log;
// PiG uses its own product prefix, as it does for pig-debug.log.
const TUICrashLogName = "pig-tui-crash.log"

// RenderOverflowError is the value doRender panics with when an over-wide
// non-image row reaches the differential-render loop. Its message is the
// Error thrown by upstream TuiMainScreen.doRender (tui-main-screen.ts:536-544).
type RenderOverflowError struct {
	Line          int
	LineWidth     int
	TerminalWidth int
	LogPath       string
}

func (e *RenderOverflowError) Error() string {
	return strings.Join([]string{
		fmt.Sprintf("Rendered line %d exceeds terminal width (%d > %d).", e.Line, e.LineWidth, e.TerminalWidth),
		"",
		"This is likely caused by a custom TUI component not truncating its output.",
		"Use visibleWidth() to measure and truncateToWidth() to truncate lines.",
		"",
		"Debug log written to: " + e.LogPath,
	}, "\n")
}

// SetLogDirectory sets the directory for the overflow crash log. Mirrors the
// upstream TuiBase logDirectory constructor argument (getAgentDir() in
// interactive mode). Empty selects the OS temp directory.
func (t *TUI) SetLogDirectory(dir string) {
	t.mu.Lock()
	t.logDirectory = dir
	t.mu.Unlock()
}

// crashOnDifferentialOverflow mirrors tui-main-screen.ts:517-545: write the
// crash log (all rendered rows with their widths), stop the TUI to restore
// terminal state, then throw. A failed log write propagates before the stop,
// as upstream's synchronous fs calls do. The caller holds t.mu.
func (t *TUI) crashOnDifferentialOverflow(newLines []string, index, width int) {
	lineWidth := widthx.VisibleWidth(newLines[index])
	dir := t.logDirectory
	if dir == "" {
		dir = os.TempDir()
	}
	crashLogPath := filepath.Join(dir, TUICrashLogName)
	data := make([]string, 0, capHint(len(newLines), 7))
	data = append(data,
		"Crash at "+t.now().UTC().Format("2006-01-02T15:04:05.000Z"),
		fmt.Sprintf("Terminal width: %d", width),
		fmt.Sprintf("Line %d visible width: %d", index, lineWidth),
		"",
		"=== All rendered lines ===",
	)
	for i, line := range newLines {
		data = append(data, fmt.Sprintf("[%d] (w=%d) %s", i, widthx.VisibleWidth(line), line))
	}
	data = append(data, "")
	if err := os.MkdirAll(filepath.Dir(crashLogPath), 0o777); err != nil {
		panic(err)
	}
	if err := os.WriteFile(crashLogPath, []byte(strings.Join(data, "\n")), 0o666); err != nil {
		panic(err)
	}

	// Clean up terminal state before throwing.
	t.StopWithOptions(StopOptions{})

	panic(&RenderOverflowError{Line: index, LineWidth: lineWidth, TerminalWidth: width, LogPath: crashLogPath})
}
