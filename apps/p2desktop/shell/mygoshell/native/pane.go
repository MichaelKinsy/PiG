package native

import (
	"fmt"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/screens"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/sprite"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/surfacehost"
	"github.com/egoist/mygo/ui"
)

// pane is the main area: the top bar of the open session (its title, its location, its options), then the new session form,
// the session's sheet, or the empty state.
func (a *App) pane(c *ui.Context, h *surfacehost.Host, sessions *screens.Sessions, showToggle bool) {
	t := c.Theme()
	var row *screens.Row
	for _, item := range sessions.Items("", a.archived) {
		if item.Row != nil && item.Row.ID == a.sel() {
			row = item.Row
		}
	}
	ui.Row(c).Height(52).AlignItems(ui.Center).Padding(0, 8, 0, 16).Gap(8).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		if showToggle {
			if iconButton(c, iconChevronR, "Open the sidebar").Clicked() {
				a.collapsed = false
				a.showDrawer = true // below the breakpoint the drawer is the whole window
			}
		}
		if row != nil {
			ui.Column(c).Grow(1).Children(func() {
				ui.Text(c, row.Title).Bold().FontSize(15).SingleLine()
				ui.Text(c, row.Location).FontSize(11).Font("monospace").TextColor(t.TextMuted).DottedUnderline().SingleLine()
			})
			a.sessionMenu(c, row, sessions)
		} else {
			ui.Spacer(c)
		}
	})
	ui.Column(c).Grow(1).Children(func() {
		switch {
		case a.creating:
			ui.Column(c).Padding(24).Gap(16).MaxWidth(560).Children(func() { a.newSession(c, sessions) })
		case row != nil:
			ui.Column(c).Padding(24).Gap(16).MaxWidth(720).Children(func() { a.sheet(c, row, sessions) })
		default:
			a.emptyState(c, sessions)
		}
	})
}

// emptyState is the web's: the mark and "What should PiG do?", the folder, and the way to begin.
func (a *App) emptyState(c *ui.Context, sessions *screens.Sessions) {
	t := c.Theme()
	ui.Column(c).Grow(1).Center().Gap(18).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
			mark(c, sprite.Active(), 2)
			ui.Text(c, "What should PiG do?").Bold().FontSize(22)
		})
		if cwd := sessions.DefaultCwd(); cwd != "" {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconFolder).Size(15, 15).TextColor(t.TextMuted)
				ui.Text(c, screens.ShortPath(cwd)).FontSize(12).Font("monospace").TextColor(t.TextMuted).DottedUnderline()
			})
		}
		if ui.Button(c, "New session").Padding(10, 18).Clicked() {
			a.startNew(sessions)
		}
	})
}

// sessionMenu is the top bar's "…": the session's actions, one menu in place of a row of buttons.
func (a *App) sessionMenu(c *ui.Context, row *screens.Row, sessions *screens.Sessions) {
	iconButton(c, iconMore, "Session options").Menu(func(m *ui.Menu) {
		if row.ArchivedAt != nil {
			if m.Item("Restore").Chosen() {
				go func() { _ = sessions.Restore(row.ID) }()
			}
			return
		}
		pin := "Pin"
		if row.Pinned {
			pin = "Unpin"
		}
		if m.Item(pin).Chosen() {
			go func() { _ = sessions.Pin(row.ID, !row.Pinned) }()
		}
		if m.Item("Rename").Chosen() {
			a.renaming, a.draft = row.ID, row.Title
		}
		if m.Item("Clone").Chosen() {
			go func() { _ = sessions.Clone(row.ID) }()
		}
		m.Separator()
		if m.Item("Archive").Chosen() {
			go func() { _ = sessions.Archive(row.ID) }()
		}
	})
}

// sheet is the session's facts: where it works, which model, when, its worker's trouble, and the rename line.
func (a *App) sheet(c *ui.Context, row *screens.Row, sessions *screens.Sessions) {
	t := c.Theme()
	if a.renaming == row.ID {
		ui.Row(c).Gap(8).Children(func() {
			input := ui.TextInput(c.Key("rename"), &a.draft).Grow(1).AutoFocus()
			if input.Submitted() {
				if a.draft != "" && a.draft != row.Title {
					title := a.draft
					go func() { _ = sessions.Rename(row.ID, title) }()
				}
				a.renaming = ""
			}
			if ui.Button(c, "Cancel").Clicked() {
				a.renaming = ""
			}
		})
	}
	ui.Column(c).Gap(6).Children(func() {
		line(c, "Model", fmt.Sprintf("%s / %s", row.Model.Provider, row.Model.ModelId))
		line(c, "Thinking", row.ThinkingLevel)
		line(c, "Created", time.UnixMilli(row.CreatedAt).Format("2 Jan 2006 15:04"))
		line(c, "Updated", screens.Ago(time.UnixMilli(row.UpdatedAt), time.Now())+" ago")
		if n := len(row.Branches); n > 1 {
			line(c, "Branches", fmt.Sprint(n))
		}
	})
	if row.Error != "" {
		ui.Text(c, "Worker failed: "+row.Error).TextColor(t.Danger).Selectable()
	}
	if row.OperationFailed {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, row.Operation+": "+row.OperationError).TextColor(t.Danger)
			if ui.Button(c, "Dismiss").Clicked() {
				id := row.OperationID
				go func() { _ = sessions.Dismiss(id) }()
			}
		})
	}
}

// line is a label and its value.
func line(c *ui.Context, label, value string) {
	ui.Row(c).Gap(12).Children(func() {
		ui.Text(c, label).Width(90).FontSize(13).TextColor(c.Theme().TextMuted)
		ui.Text(c, value).FontSize(13).Selectable()
	})
}

// newSession is the form of a session to create: its directory and an optional title.
func (a *App) newSession(c *ui.Context, sessions *screens.Sessions) {
	ui.Text(c, "New session").Bold().FontSize(22)
	ui.Text(c, "Directory").FontSize(13).TextColor(c.Theme().TextMuted)
	ui.TextInput(c.Key("new-cwd"), &a.newCwd).Label("Directory")
	ui.Text(c, "Title (optional)").FontSize(13).TextColor(c.Theme().TextMuted)
	title := ui.TextInput(c.Key("new-title"), &a.newTitle).Label("Title")
	create := title.Submitted()
	ui.Row(c).Gap(8).Children(func() {
		if ui.PrimaryButton(c, "Create").Disabled(a.newCwd == "").Clicked() {
			create = true
		}
		if ui.Button(c, "Cancel").Clicked() {
			a.creating = false
		}
	})
	if create && a.newCwd != "" {
		cwd, name := a.newCwd, a.newTitle
		a.creating = false
		go func() {
			id, err := sessions.Create(cwd, name)
			if err == nil {
				a.selectAfter(id)
			}
		}()
	}
}

// spritePicker is the dialog of the sprite catalogue: each sprite drawn from its own grid and palette, the selected one
// marked; choosing one saves it to the state file /sprite writes.
func (a *App) spritePicker(c *ui.Context) {
	t := c.Theme()
	ui.Modal(c, &a.pickSprite, func() {
		ui.Text(c, "Choose a PiG sprite").Bold().FontSize(18)
		ui.Text(c, "The same choice as /sprite in the terminal: both follow it.").FontSize(12).TextColor(t.TextMuted)
		active := sprite.Active()
		ui.Grid(c).Columns(4).Gap(10).Children(func() {
			for _, v := range sprite.Catalogue() {
				selected := v.ID == active.ID
				cell := ui.Column(c.Key(v.ID)).AlignItems(ui.Center).Gap(4).Padding(10).Radius(8).Focusable().Label(v.Name).Children(func() {
					mark(c, v, 3)
					ui.Text(c, v.Name).FontSize(12).SingleLine()
				})
				switch {
				case selected:
					cell.Background(t.SurfaceHover).BorderWidth(1).BorderColor(t.Accent)
				case cell.Hovered():
					cell.Background(t.SurfaceHover)
				}
				if cell.Clicked() {
					if err := sprite.Select(v.ID); err != nil {
						c.Toast(err.Error())
					}
				}
			}
		})
		if ui.Button(c, "Done").Clicked() {
			a.pickSprite = false
		}
	})
}
