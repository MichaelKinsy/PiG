package codingagent

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// TestThemeControllerFailedColorQueryAppliesNoColors covers requestTerminalColors
// (theme-controller.ts:31): `query.then(apply, () => apply({}))`. A query that
// fails still completes, so the startup wait ends instead of hanging, and it
// applies no colors, whatever a partial result carried.
func TestThemeControllerFailedColorQueryAppliesNoColors(t *testing.T) {
	f := themeControllerNew(t, "", nil)
	white := tui.RgbColor{R: 255, G: 255, B: 255}
	f.renderer.answer = func() <-chan tui.TerminalColorsResult {
		result := make(chan tui.TerminalColorsResult, 1)
		result <- tui.TerminalColorsResult{Colors: tui.TerminalColors{Background: &white}, Err: errors.New("terminal write failed")}
		close(result)
		return result
	}
	f.m.applyThemeFromSettings(f.ctx)
	f.flush(t) // returns only once the failed query completed
	colors := f.m.themeState.terminalColors
	if colors == nil {
		t.Fatal("the failed query completed without applying (apply({}) never ran)")
	}
	if colors.Foreground != nil || colors.Background != nil || colors.Palette != nil {
		t.Errorf("a failed query applied colors %+v, want none", *colors)
	}
	if len(f.m.themeState.pendingColorQueries) != 0 {
		t.Errorf("%d color queries still pending after the wait", len(f.m.themeState.pendingColorQueries))
	}
}
