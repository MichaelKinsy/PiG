package native

import (
	"fmt"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/screens"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/surfacehost"
	"github.com/MichaelKinsy/PiG/chord/next/surfaces"
	"github.com/egoist/mygo/ui"
)

func fallbackTheme() surfaces.Theme { return surfacehost.BrandThemes()[0] }

// sidebar is the sessions list: the brand, a search field, the sessions grouped by location in a virtualized list, and the
// archive toggle and the new session button below it.
func (a *App) sidebar(c *ui.Context, h *surfacehost.Host, sessions *screens.Sessions) {
	t := c.Theme()
	ui.Row(c).AlignItems(ui.Center).Gap(10).Padding(14, 16).Children(func() {
		ui.Image(c, logo).Size(28, 28).Fit(ui.Contain)
		ui.Text(c, "PiG").Bold().FontSize(18)
		ui.Spacer(c)
		if ui.PrimaryButton(c, "New").Tooltip("New session").Disabled(h.Connection() != surfaces.Connected).Clicked() {
			a.creating = true
			a.setSel("")
			a.newCwd, a.newTitle = sessions.DefaultCwd(), ""
		}
	})
	placeholder := "Search sessions"
	if a.archived {
		placeholder = "Search archive"
	}
	ui.SearchField(c, &a.query).Placeholder(placeholder).Label(placeholder).Margin(0, 12, 8)
	items := sessions.Items(a.query, a.archived)
	// The list keeps its place by session, not by row, as sessions come and go above it.
	a.list.Key = func(i int) any {
		if i >= len(items) {
			return i
		}
		if items[i].Group != nil {
			return "group:" + items[i].Group.Label
		}
		return items[i].Row.ID
	}
	a.list.Header = func(i int) bool { return i < len(items) && items[i].Group != nil }
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
	a.list.Selected = &chosen
	now := time.Now()
	list := ui.List(c, &a.list, len(items), func(i int) {
		if items[i].Group != nil {
			a.groupHeader(c, items[i].Group)
		} else {
			a.sessionRow(c, items[i].Row, now)
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
			ui.Text(c, text).TextColor(t.TextMuted).Padding(16)
		}
	})
	if list.Changed() && chosen >= 0 && chosen < len(items) && items[chosen].Row != nil {
		a.setSel(items[chosen].Row.ID)
		a.creating = false
	}
	ui.Row(c).Padding(8, 12).Gap(8).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		ui.Toggle(c, &a.archived, "Archive").Label("Show archived sessions")
		ui.Spacer(c)
		ui.Text(c, connectionLabel(h.Connection())).TextColor(t.TextMuted).FontSize(12)
	})
}

func connectionLabel(state surfaces.ConnectionState) string {
	switch state {
	case surfaces.Connected:
		return "Connected"
	case surfaces.Connecting:
		return "Connecting…"
	}
	return "Waiting to reconnect"
}

// groupHeader is the line above a location's sessions.
func (a *App) groupHeader(c *ui.Context, g *screens.Group) {
	t := c.Theme()
	ui.Row(c).Background(t.Surface).Padding(10, 16, 4).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, g.Label).Bold().FontSize(12).TextColor(t.TextMuted).SingleLine().Grow(1)
		ui.Text(c, fmt.Sprint(g.Count)).FontSize(12).TextColor(t.TextMuted)
	})
}

// sessionRow is a session: its title (pinned first), where it is in its life (working, failed, an operation), and its age.
func (a *App) sessionRow(c *ui.Context, r *screens.Row, now time.Time) {
	t := c.Theme()
	ui.Column(c).Padding(8, 16).Gap(2).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			if r.Pinned {
				ui.Text(c, "◆").FontSize(10).TextColor(t.Accent)
			}
			ui.Text(c, r.Title).SingleLine().Grow(1)
			if r.Busy {
				ui.Spinner(c).Size(12, 12)
			}
		})
		ui.Row(c).Gap(8).Children(func() {
			switch {
			case r.Error != "":
				ui.Text(c, "Worker failed").FontSize(12).TextColor(t.Danger).Tooltip(r.Error)
			case r.Operation != "":
				colour := t.TextMuted
				if r.OperationFailed {
					colour = t.Danger
				}
				ui.Text(c, r.Operation).FontSize(12).TextColor(colour)
			default:
				ui.Text(c, screens.Ago(time.UnixMilli(r.UpdatedAt), now)).FontSize(12).TextColor(t.TextMuted)
			}
			if r.ArchivedAt != nil {
				ui.Text(c, "Select to restore").FontSize(12).TextColor(t.TextMuted)
			}
		})
	})
}
