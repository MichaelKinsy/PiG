package surfacehost

import "testing"

// TestBrandContrast: every foreground/background pair the native views draw reaches WCAG AA (4.5 for text, 3 for the
// non-text edges of controls) in both themes.
func TestBrandContrast(t *testing.T) {
	type pair struct {
		fg, bg string
		min    float64
	}
	pairs := []pair{
		{"fg", "bg", 4.5}, {"fg", RoleSurface, 4.5}, {"fg", RoleSoft, 4.5},
		{"muted", "bg", 4.5}, {"muted", RoleSurface, 4.5},
		{"accent", "bg", 4.5}, {"accent", RoleSurface, 4.5},
		{RoleOnAccent, "accent", 4.5},
		{"warning", "bg", 4.5}, {"warning", RoleSurface, 4.5},
		{"error", "bg", 4.5}, {"error", RoleSurface, 4.5},
		{"success", "bg", 4.5},
		{"accent", "bg", 3},
	}
	for _, theme := range BrandThemes() {
		for _, p := range pairs {
			fg, err := ParseHex(theme.Colors[p.fg])
			if err != nil {
				t.Fatalf("%s %s: %v", theme.ID, p.fg, err)
			}
			bg, err := ParseHex(theme.Colors[p.bg])
			if err != nil {
				t.Fatalf("%s %s: %v", theme.ID, p.bg, err)
			}
			if got := Contrast(fg, bg); got < p.min {
				t.Errorf("%s: %s %s on %s %s is %.2f, want >= %.1f", theme.ID, p.fg, theme.Colors[p.fg], p.bg, theme.Colors[p.bg], got, p.min)
			}
		}
	}
}

func TestBrandThemesNameEveryBaseRole(t *testing.T) {
	for _, theme := range BrandThemes() {
		for _, role := range []string{"accent", "bg", "fg", "muted", "error", "warning", "success", RoleSurface, RoleSoft, RoleLine, RoleOnAccent} {
			if _, err := ParseHex(theme.Colors[role]); err != nil {
				t.Errorf("%s role %s: %v", theme.ID, role, err)
			}
		}
	}
}

func TestContrastKnownValues(t *testing.T) {
	black, white := RGB{}, RGB{255, 255, 255}
	if got := Contrast(black, white); got < 20.99 || got > 21.01 {
		t.Fatalf("black on white = %.3f, want 21", got)
	}
}

// TestReadableFallsBackToTextWhereAccentOrMutedFailAA: on soft, the light theme's muted and accent are below AA, so a view
// gets the text colour; everywhere else they are kept.
func TestReadableFallsBackToTextWhereAccentOrMutedFailAA(t *testing.T) {
	themes := BrandThemes()
	for _, theme := range themes {
		p := Palette{Theme: theme}
		for _, bg := range []string{"bg", RoleSurface, RoleSoft} {
			for name, got := range map[string]RGB{"secondary": p.Secondary(bg), "accented": p.Accented(bg)} {
				if c := Contrast(got, p.Color(bg)); c < 4.5 {
					t.Errorf("%s %s on %s: %.2f < 4.5", theme.ID, name, bg, c)
				}
			}
		}
	}
	light := Palette{Theme: themes[1]}
	if light.Secondary(RoleSoft) != light.Color("fg") || light.Accented(RoleSoft) != light.Color("fg") {
		t.Error("light on soft should fall back to the text colour")
	}
	if light.Secondary("bg") != light.Color("muted") {
		t.Error("light muted on page reaches AA and is kept")
	}
}
