package tui

import (
	"strings"
	"testing"
)

// Upstream 0.99.2 oracle: initTheme(name); theme.fg(token, "X") under
// setCapabilities({trueColor}) for bashMode, dim, muted, customMessageLabel,
// thinkingText, thinkingLow/Medium/High (theme.ts getThinkingBorderColor) and
// accent (getSettingsListTheme().cursor, theme.ts:1231), recorded from the
// published @earendil-works/pi-coding-agent 0.99.2 package. Each site below must
// emit the active theme's token, not a fixed dark.json hex.
func TestThemeTokenSitesMatchPiOracle(t *testing.T) {
	type pinned struct {
		bashMode, dim, muted, customLabel, thinkingText string
		low, medium, high, accent                       string
	}
	tc := func(rgb string) string { return "\x1b[38;2;" + rgb + "m" }
	c256 := func(n string) string { return "\x1b[38;5;" + n + "m" }
	cases := []struct {
		name      string
		theme     string
		trueColor bool
		want      pinned
	}{
		{"dark truecolor", "dark", true, pinned{
			bashMode: tc("94;178;134"), dim: tc("126;136;142"), muted: tc("157;165;169"),
			customLabel: tc("167;152;215"), thinkingText: tc("150;160;164"),
			low: tc("84;137;164"), medium: tc("97;133;204"), high: tc("151;118;229"),
			accent: tc("167;152;215"),
		}},
		{"light truecolor", "light", true, pinned{
			bashMode: tc("64;151;108"), dim: tc("135;144;149"), muted: tc("103;113;118"),
			customLabel: tc("116;89;180"), thinkingText: tc("124;134;140"),
			low: tc("159;194;213"), medium: tc("162;183;224"), high: tc("181;165;232"),
			accent: tc("116;89;180"),
		}},
		{"dark 256color", "dark", false, pinned{
			bashMode: c256("72"), dim: c256("102"), muted: c256("145"),
			customLabel: c256("140"), thinkingText: c256("109"),
			low: c256("67"), medium: c256("68"), high: c256("104"),
			accent: c256("140"),
		}},
		{"light 256color", "light", false, pinned{
			bashMode: c256("65"), dim: c256("102"), muted: c256("60"),
			customLabel: c256("97"), thinkingText: c256("102"),
			low: c256("146"), medium: c256("146"), high: c256("146"),
			accent: c256("97"),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withTrueColor(t, tc.trueColor)
			SetTheme(tc.theme)

			for _, site := range []struct{ name, got, want string }{
				{"bashHeaderColor", bashHeaderColor(), tc.want.bashMode},
				{"bashDimColor", bashDimColor(), tc.want.dim},
				{"bashMutedColor", bashMutedColor(), tc.want.muted},
				{"customMsgLabelFg", customMsgLabelFg(), tc.want.customLabel},
				{"thinkingBorderSGR(low)", thinkingBorderSGR("low"), tc.want.low},
				{"thinkingBorderSGR(medium)", thinkingBorderSGR("medium"), tc.want.medium},
				{"thinkingBorderSGR(high)", thinkingBorderSGR("high"), tc.want.high},
			} {
				if site.got != site.want {
					t.Errorf("%s = %q, want %q", site.name, site.got, site.want)
				}
			}

			list := NewSettingsList([]SettingItem{{ID: "a", Label: "Alpha", CurrentValue: "on", Values: []string{"on", "off"}}})
			var cursorLine string
			for _, line := range list.Render(40) {
				if strings.Contains(line, "→ ") {
					cursorLine = line
					break
				}
			}
			if !strings.HasPrefix(cursorLine, tc.want.accent+"→ ") {
				t.Errorf("settings cursor line = %q, want prefix %q", cursorLine, tc.want.accent+"→ ")
			}

			block := NewThinkingBlock(false)
			block.SetContent("step")
			lines := block.Render(40)
			if len(lines) != 1 || !strings.HasPrefix(lines[0], tc.want.thinkingText+"\x1b[3m") {
				t.Errorf("visible thinking block = %q, want prefix %q", lines, tc.want.thinkingText+"\x1b[3m")
			}
		})
	}
}

// The production renderers that consume those sites emit Pi's light-theme
// tokens (upstream 0.99.2 oracle, light truecolor): the bash-execution.ts top border
// theme.fg(colorKey) with colorKey "bashMode", or "dim" for "!!"; the
// compaction/branch summary labels theme.fg("customMessageLabel", ...); and
// the editor top border theme.getThinkingBorderColor(level)
// (interactive-mode.ts updateEditorBorderColor).
func TestThemeTokenSitesRenderLightTheme(t *testing.T) {
	withTrueColor(t, true)
	SetTheme("light")
	const (
		bashMode    = "\x1b[38;2;64;151;108m"
		dim         = "\x1b[38;2;135;144;149m"
		customLabel = "\x1b[38;2;116;89;180m"
		high        = "\x1b[38;2;181;165;232m"
	)

	for _, tc := range []struct {
		name    string
		exclude bool
		want    string
	}{{"!cmd", false, bashMode}, {"!!cmd", true, dim}} {
		block := NewBashExecutionBlock("echo hi", tc.exclude)
		code := 0
		block.SetComplete(&code, false, false)
		lines := block.Render(20)
		if len(lines) < 2 || !strings.HasPrefix(lines[1], tc.want+"─") {
			t.Errorf("%s bash border = %q, want prefix %q", tc.name, lines, tc.want+"─")
		}
	}

	compaction := strings.Join(NewCompactionSummaryComponent("summary", 1000).Render(40), "\n")
	if !strings.Contains(compaction, customLabel+"\x1b[1m[compaction]") {
		t.Errorf("compaction label missing %q in %q", customLabel+"\x1b[1m[compaction]", compaction)
	}
	branch := strings.Join(NewBranchSummaryComponent("summary").Render(40), "\n")
	if !strings.Contains(branch, customLabel+"\x1b[1m[branch]") {
		t.Errorf("branch label missing %q in %q", customLabel+"\x1b[1m[branch]", branch)
	}

	editor := NewEditor()
	editor.ThinkingLevel = "high"
	if top := editor.Render(20)[0]; !strings.HasPrefix(top, high+"─") {
		t.Errorf("editor high border = %q, want prefix %q", top, high+"─")
	}
}
