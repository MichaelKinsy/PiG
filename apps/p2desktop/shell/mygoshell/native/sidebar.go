package native

import (
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/screens"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/sprite"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/surfacehost"
	"github.com/MichaelKinsy/PiG/chord/next/surfaces"
	"github.com/egoist/mygo/ui"
)

func fallbackTheme() surfaces.Theme { return surfacehost.BrandThemes()[0] }

// sidebarWidth is the web UI's drawer: 300 px.
const sidebarWidth = 300

// sidebar is the web drawer: the mark and "New session" in a 52 px header, search, the sessions grouped by location in a
// virtualized list, and Archived / More / theme in the footer.
func (a *App) sidebar(c *ui.Context, h *surfacehost.Host, sessions *screens.Sessions, collapse func()) {
	t := c.Theme()
	pal := a.palette(h)
	ui.Row(c).Height(52).AlignItems(ui.Center).Padding(0, 8, 0, 16).Gap(4).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		mark(c, sprite.Active(), 2)
		ui.Spacer(c)
		if ghost(c, "New session", iconPlus, h.Connection() == surfaces.Connected).Clicked() {
			a.startNew(sessions)
		}
		if iconButton(c, iconChevronL, "Collapse the sidebar").Clicked() {
			collapse()
		}
	})
	placeholder := "Search sessions"
	if a.archived {
		placeholder = "Search archive"
	}
	ui.Row(c).AlignItems(ui.Center).Gap(10).Padding(0, 16).Height(44).Children(func() {
		ui.Icon(c, iconSearch).Size(15, 15).TextColor(t.TextMuted)
		ui.TextInput(c.Key("search"), &a.query).Placeholder(placeholder).Label(placeholder).Grow(1).BorderWidth(0).Background(ui.Color{})
	})
	ui.Divider(c).Margin(0, 16)
	items := sessions.Items(a.query, a.archived)
	a.list.Key = func(i int) any {
		if i >= len(items) {
			return i
		}
		if items[i].Group != nil {
			return "group:" + items[i].Group.Label
		}
		return items[i].Row.ID
	}
	a.list.Label = func(i int) string {
		if i < len(items) && items[i].Row != nil {
			return items[i].Row.Title
		}
		return ""
	}
	chosen := -1
	for i, item := range items {
		if item.Row != nil && item.Row.ID == a.sel() {
			chosen = i
		}
	}
	now := time.Now()
	clicked := -1
	ui.List(c, &a.list, len(items), func(i int) {
		if items[i].Group != nil {
			a.groupHeader(c, items[i].Group, h, sessions)
		} else {
			if a.sessionRow(c.Key(items[i].Row.ID), items[i].Row, now, i == chosen, pal) {
				clicked = i
			}
		}
	}).Grow(1).Children(func() {
		if len(items) == 0 {
			text := "No sessions yet."
			switch {
			case !sessions.Ready():
				text = "Loading…"
			case a.query != "":
				text = "Nothing found."
			case a.archived:
				text = "No archived sessions."
			}
			ui.Text(c, text).TextColor(t.TextMuted).FontSize(13).Padding(22, 16, 16).TextAlign(ui.Center)
		}
	})
	if clicked >= 0 && items[clicked].Row != nil {
		row := items[clicked].Row
		a.setSel(row.ID)
		a.creating = false
		a.showDrawer = false
		if row.ArchivedAt != nil {
			// A tap on an archived row restores it, as the drawer's does.
			go func() { _ = sessions.Restore(row.ID) }()
			a.archived = false
		}
	}
	a.footer(c, h)
}

// ghost is a quiet text button with an icon, as the header's "New session".
func ghost(c *ui.Context, label string, ic *ui.SVG, enabled bool) ui.Element {
	t := c.Theme()
	b := ui.Row(c).AlignItems(ui.Center).Gap(6).Padding(6, 10).Radius(6).Disabled(!enabled).Focusable().Children(func() {
		ui.Icon(c, ic).Size(14, 14).TextColor(t.TextMuted)
		ui.Text(c, label).FontSize(13).TextColor(t.TextMuted)
	})
	if b.Hovered() && enabled {
		b.Background(t.SurfaceHover)
	}
	return b
}

// iconButton is an icon alone, with a name for assistive technology and a tooltip.
func iconButton(c *ui.Context, ic *ui.SVG, name string) ui.Element {
	t := c.Theme()
	b := ui.Row(c).Size(32, 32).Center().Radius(6).Focusable().Label(name).Tooltip(name).Children(func() {
		ui.Icon(c, ic).Size(16, 16).TextColor(t.TextMuted)
	})
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	return b
}

// startNew opens the new session form.
func (a *App) startNew(sessions *screens.Sessions) {
	a.creating = true
	a.setSel("")
	a.newCwd, a.newTitle = sessions.DefaultCwd(), ""
}

// footer is Archived / More / the theme toggle.
func (a *App) footer(c *ui.Context, h *surfacehost.Host) {
	t := c.Theme()
	ui.Row(c).Height(52).AlignItems(ui.Center).Padding(0, 8, 0, 16).Gap(18).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		colour := t.TextMuted
		if a.archived {
			colour = t.Accent
		}
		archived := ui.Text(c, "Archived").FontSize(13).TextColor(colour).Focusable().Label("Show archived sessions")
		if archived.Clicked() {
			a.archived = !a.archived
		}
		more := ui.Row(c).AlignItems(ui.Center).Gap(4).Focusable().Label("More").Children(func() {
			ui.Text(c, "More").FontSize(13).TextColor(t.TextMuted)
			ui.Icon(c, iconChevronU).Size(12, 12).TextColor(t.TextMuted)
		})
		more.Menu(func(m *ui.Menu) {
			if m.Item("Sprite…").Chosen() {
				a.pickSprite = true
			}
		})
		ui.Spacer(c)
		icon, name := iconSystem, "Theme: system"
		switch a.modeChoice {
		case "light":
			icon, name = iconSun, "Theme: light"
		case "dark":
			icon, name = iconMoon, "Theme: dark"
		}
		if iconButton(c, icon, name).Clicked() {
			a.cycleTheme()
		}
	})
}

// groupHeader is the line above a location's sessions: its folder, its path in the mono font, and "+" to start one there.
func (a *App) groupHeader(c *ui.Context, g *screens.Group, h *surfacehost.Host, sessions *screens.Sessions) {
	t := c.Theme()
	ui.Row(c).Padding(12, 8, 1, 16).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, iconFolder).Size(15, 15).TextColor(t.TextMuted)
		ui.Text(c, g.Label).FontSize(11).Font("monospace").TextColor(t.TextMuted).SingleLine().Grow(1)
		if !a.archived {
			if iconButton(c, iconPlus, "New session in "+g.Label).Clicked() {
				a.startNew(sessions)
				a.newCwd = g.Cwd
			}
		}
	})
}

// sessionRow is a session as the drawer shows it: its title, its age in the mono font at the right, the whole width
// filled when it is the open one.
func (a *App) sessionRow(c *ui.Context, r *screens.Row, now time.Time, selected bool, pal surfacehost.Palette) bool {
	t := c.Theme()
	row := ui.Row(c).Padding(11, 16).Gap(8).AlignItems(ui.Center).Focusable().Label(r.Title).Children(func() {
		if r.Pinned {
			ui.Icon(c, iconPin).Size(12, 12).TextColor(t.TextMuted)
		}
		ui.Text(c, r.Title).FontSize(15).SingleLine().Grow(1)
		switch {
		case r.Busy:
			ui.Spinner(c).Size(12, 12)
		case r.Error != "":
			ui.Text(c, "failed").FontSize(11).Font("monospace").TextColor(t.Danger).Tooltip(r.Error)
		case r.Operation != "":
			colour := t.TextMuted
			if r.OperationFailed {
				colour = t.Danger
			}
			ui.Text(c, r.Operation).FontSize(11).Font("monospace").TextColor(colour)
		default:
			col := pal.Secondary(rowBackground(selected)) // the web's .session-meta: muted
			ui.Text(c, screens.Ago(time.UnixMilli(r.UpdatedAt), now)).FontSize(11).Font("monospace").TextColor(ui.RGB(col.R, col.G, col.B))
		}
	})
	switch {
	case selected:
		row.Background(t.SurfaceHover)
	case row.Hovered():
		row.Background(t.SurfaceHover.Alpha(0.5))
	}
	return row.Clicked()
}

func rowBackground(selected bool) string {
	if selected {
		return surfacehost.RoleSoft
	}
	return "bg"
}
