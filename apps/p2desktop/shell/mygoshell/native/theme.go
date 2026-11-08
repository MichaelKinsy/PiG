// Package native is the desktop client's MyGo views: pure functions of what the screens (internal/screens) read from the
// surface host. It is the second importer of MyGo besides the adapter next to it (shell/mygoshell); nothing outside
// shell/mygoshell imports MyGo.
package native

import (
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/surfacehost"
	"github.com/egoist/mygo/ui"
)

// Theme is the MyGo theme of a Chord theme (the active one of the surface host): the site's tokens as the controls'
// colours, over the stock theme of its mode for what the tokens do not name (metrics, hover and pressed shades).
func Theme(p surfacehost.Palette) *ui.Theme {
	var t ui.Theme
	if p.Theme.Mode == "light" {
		t = *ui.LightTheme()
	} else {
		t = *ui.DarkTheme()
	}
	color := func(role string) ui.Color { c := p.Color(role); return ui.RGB(c.R, c.G, c.B) }
	t.Dark = p.Theme.Mode != "light"
	t.Background = color("bg")
	t.Surface = color(surfacehost.RoleSurface)
	t.SurfaceHover = color(surfacehost.RoleSoft)
	t.SurfacePressed = color(surfacehost.RoleSoft).Mix(color("fg"), 0.08)
	t.Border = color(surfacehost.RoleLine)
	t.Text = color("fg")
	sec := p.Secondary("bg")
	t.TextMuted = ui.RGB(sec.R, sec.G, sec.B)
	t.Accent = color("accent")
	t.AccentHover = color("accent").Mix(color("fg"), 0.12)
	t.AccentPressed = color("accent").Mix(color("fg"), 0.24)
	t.AccentText = color(surfacehost.RoleOnAccent)
	t.InactiveAccent = t.Accent
	t.Danger = color("error")
	t.Warning = color("warning")
	t.Success = color("success")
	t.Selection = color("accent").Alpha(0.35)
	t.Focus = color("accent")
	t.Scrollbar = t.TextMuted.Alpha(0.5)
	return &t
}
