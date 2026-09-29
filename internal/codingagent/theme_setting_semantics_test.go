package codingagent

import (
	"io"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

func TestThemeSettingBOMPairReachesInteractiveAutoSelection(t *testing.T) {
	restoreStartupTheme(t)
	t.Setenv("COLORFGBG", "15;0")
	mode, _ := newCustomEditorDispatchMode(t)
	mode.opts.Settings.Theme = "\ufefflight / dark\ufeff"
	query := mode.beginThemeDetection(io.Discard)
	if query == nil {
		t.Fatal("automatic theme pair was treated as a fixed theme")
	}
	defer mode.disposeTheme()
	if !mode.consumeTerminalThemeInput("\x1b[?997;2n") {
		t.Fatal("preferred light scheme was not consumed")
	}
	select {
	case <-query.done:
	default:
		t.Fatal("preferred scheme did not settle automatic selection")
	}
	if got := tui.ActiveTheme().Name; got != "light" {
		t.Fatalf("active theme=%q, want light after trimming the automatic setting", got)
	}
}
