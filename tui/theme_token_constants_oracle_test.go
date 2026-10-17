package tui

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi: packages/coding-agent/src/modes/interactive/theme/theme.ts:108-117 (ThemeBg, ThemeToken) and :247-249, 301-304 (the theme's fgAnsi and bgAnsi maps).
// The ThemeBg and ThemeColor constants are checked against the live Pi theme, not against a Go table: Pi's dark theme holds exactly these background tokens
// and foreground tokens, accepts every background token through bg and rejects it through fg, and the reverse for every foreground token.
// mutation-checked: the mutant "ThemeBgToolErrorBg value toolErrBg" fails it.
// mutation-checked: the mutant "ThemeBgSearchMatchBg value searchMatch" fails it.
func TestThemeTokenConstantsMatchTheLivePiTheme(t *testing.T) {
	type slot struct {
		Fg bool `json:"fg"`
		Bg bool `json:"bg"`
	}
	var want struct {
		BgKeys []string        `json:"bgKeys"`
		FgKeys []string        `json:"fgKeys"`
		Slots  map[string]slot `json:"slots"`
	}
	bgs, colors := allThemeBgs(), allThemeColors()
	pioracle.Run(t, `
const th = await load("pi-coding-agent/modes/interactive/theme/theme.js");
th.initTheme("dark", false);
const active = th.theme;
const attempt = (f) => { try { f(); return true; } catch { return false; } };
const slots = {};
for (const token of input) slots[token] = { fg: attempt(() => active.fg(token, "x")), bg: attempt(() => active.bg(token, "x")) };
emit({ bgKeys: [...active.bgAnsi.keys()], fgKeys: [...active.fgAnsi.keys()], slots });`, append(slices.Clone(bgs), colors...), &want)

	for _, token := range bgs {
		if got := want.Slots[token]; !got.Bg || got.Fg {
			t.Errorf("Pi: background token %q accepted by bg=%v fg=%v, want bg only", token, got.Bg, got.Fg)
		}
	}
	for _, token := range colors {
		if got := want.Slots[token]; !got.Fg || got.Bg {
			t.Errorf("Pi: foreground token %q accepted by fg=%v bg=%v, want fg only", token, got.Fg, got.Bg)
		}
	}
	slices.Sort(want.BgKeys)
	slices.Sort(want.FgKeys)
	gotBgs, gotColors := slices.Clone(bgs), slices.Clone(colors)
	slices.Sort(gotBgs)
	slices.Sort(gotColors)
	if !slices.Equal(gotBgs, want.BgKeys) {
		t.Errorf("ThemeBg constants %v differ from Pi's background tokens %v", gotBgs, want.BgKeys)
	}
	if !slices.Equal(gotColors, want.FgKeys) {
		t.Errorf("ThemeColor constants %v differ from Pi's foreground tokens %v", gotColors, want.FgKeys)
	}
}
