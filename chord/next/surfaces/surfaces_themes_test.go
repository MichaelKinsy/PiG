package surfaces

import "testing"

// pi: packages/chord/test/surfaces/themes.test.ts

func baseColors() map[string]string {
	return map[string]string{
		"accent": "#e8590c", "bg": "#111317", "fg": "#e8e8e8", "muted": "#8a8f98",
		"error": "#ff0000", "warning": "#ffaa00", "success": "#00ff00",
	}
}

func with(colors map[string]string, extra map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range colors {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

var (
	solarized = Theme{ID: "solarized", Name: "Solarized", Mode: "dark", Colors: with(baseColors(), map[string]string{"link": "#268bd2"})}
	paper     = Theme{ID: "paper", Name: "Paper", Mode: "light", Colors: baseColors(), Web: map[string]string{"radius": "10px"}}
	night     = Theme{ID: "night", Name: "Night", Mode: "dark", Colors: baseColors(), Tui: map[string]string{"accent": "208"}}
)

func ptr(s string) *string { return &s }

func TestActiveThemeIsThePickedOneOfTheModeElseTheFirstOfTheModeElseTheFallback(t *testing.T) {
	themes := []Theme{solarized, paper, night}
	for _, c := range []struct {
		pick ThemePick
		mode string
		want string
	}{
		{ThemePick{Dark: ptr("night")}, "dark", "night"},
		{ThemePick{}, "dark", "solarized"},
		{ThemePick{Dark: ptr("paper")}, "dark", "solarized"},
		{ThemePick{Light: ptr("gone")}, "light", "paper"},
	} {
		if got := ActiveTheme(themes, c.pick, c.mode).ID; got != c.want {
			t.Errorf("ActiveTheme(%v, %s) = %s, want %s", c.pick, c.mode, got, c.want)
		}
	}
	if got := ActiveTheme([]Theme{solarized}, ThemePick{}, "light"); got.ID != FallbackThemes["light"].ID {
		t.Errorf("fallback = %s", got.ID)
	}
}

func TestThemeVariablesGivesEveryRoleAndEveryWebExtraACustomProperty(t *testing.T) {
	variables := ThemeVariables(solarized)
	for k, v := range map[string]string{"--accent": "#e8590c", "--bg": "#111317", "--link": "#268bd2"} {
		if variables[k] != v {
			t.Errorf("%s = %q, want %q", k, variables[k], v)
		}
	}
	if got := ThemeVariables(paper)["--radius"]; got != "10px" {
		t.Errorf("--radius = %q", got)
	}
}

func TestTerminalColorGivesTrueColourOrTheNearestOf256ForARole(t *testing.T) {
	for _, c := range []struct{ role, depth, layer, want string }{
		{"accent", "truecolor", "fg", "38;2;232;89;12"},
		{"bg", "truecolor", "bg", "48;2;17;19;23"},
		{"error", "256", "fg", "38;5;196"},
		{"fg", "256", "fg", "38;5;254"},
		{"bg", "256", "bg", "48;5;233"},
	} {
		if got := TerminalColor(solarized, c.role, c.depth, c.layer); got != c.want {
			t.Errorf("TerminalColor(%s, %s, %s) = %s, want %s", c.role, c.depth, c.layer, got, c.want)
		}
	}
}

func TestTerminalColorTakesTheThemesTuiOverrideFirstTheFallbackForAMissingRoleTheDefaultForAnUnknownOne(t *testing.T) {
	if got := TerminalColor(night, "accent", "truecolor", "fg"); got != "38;5;208" {
		t.Errorf("override = %s", got)
	}
	partial := solarized
	partial.Colors = with(baseColors(), map[string]string{"muted": "not a colour"})
	if got := TerminalColor(partial, "muted", "truecolor", "fg"); got != "39" {
		t.Errorf("not a colour = %s", got)
	}
	if got := TerminalColor(solarized, "nobody", "256", "bg"); got != "49" {
		t.Errorf("unknown = %s", got)
	}
	missing := solarized
	missing.Colors = with(baseColors(), nil)
	delete(missing.Colors, "accent")
	if got := TerminalColor(missing, "accent", "truecolor", "fg"); got != "38;2;76;154;255" {
		t.Errorf("missing = %s", got)
	}
}
