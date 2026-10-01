package codingagent

import (
	"bytes"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

func TestThemeSettingBOMPairReachesInteractiveAutoSelection(t *testing.T) {
	t.Setenv("COLORFGBG", "15;0")
	mode, ctx := newThemeDispatchMode(t)
	var output bytes.Buffer
	mode.themeState.output = &output
	mode.opts.Settings.Theme = "\ufefflight / dark\ufeff"
	mode.initTheme()
	mode.applyThemeFromSettings(ctx)
	if !strings.HasSuffix(output.String(), "\x1b[?2031h") {
		t.Fatalf("automatic theme pair was treated as a fixed theme: %q", output.String())
	}
	if !mode.consumeTerminalThemeInput("\x1b[?997;2n") {
		t.Fatal("preferred light scheme was not consumed")
	}
	if got := tui.ActiveTheme().Name; got != "light" {
		t.Fatalf("active theme=%q, want light after trimming the automatic setting", got)
	}
}
