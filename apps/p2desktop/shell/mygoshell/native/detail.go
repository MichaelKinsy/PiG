package native

import (
	"fmt"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/screens"
	"github.com/egoist/mygo/ui"
)

// detail is the pane beside the list: the new session form, the selected session's sheet (title, where it works, which
// model, its actions), or an invitation to pick one.
func (a *App) detail(c *ui.Context, sessions *screens.Sessions) {
	t := c.Theme()
	if a.creating {
		a.newSession(c, sessions)
		return
	}
	var row *screens.Row
	for _, item := range sessions.Items("", a.archived) {
		if item.Row != nil && item.Row.ID == a.sel() {
			row = item.Row
		}
	}
	if row == nil {
		ui.Column(c).Grow(1).Center().Gap(8).Children(func() {
			ui.Image(c, logo).Size(64, 64).Fit(ui.Contain)
			ui.Text(c, "Select a session, or start a new one.").TextColor(t.TextMuted)
		})
		return
	}
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
	} else {
		ui.Text(c, row.Title).Bold().FontSize(24).Selectable()
	}
	ui.Text(c, row.Location).TextColor(t.TextMuted).Selectable()
	ui.Column(c).Gap(4).Children(func() {
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
	ui.Row(c).Gap(8).Children(func() {
		if row.ArchivedAt != nil {
			if ui.PrimaryButton(c, "Restore").Clicked() {
				go func() { _ = sessions.Restore(row.ID) }()
			}
			return
		}
		pin := "Pin"
		if row.Pinned {
			pin = "Unpin"
		}
		if ui.Button(c, pin).Clicked() {
			go func() { _ = sessions.Pin(row.ID, !row.Pinned) }()
		}
		if ui.Button(c, "Rename").Clicked() {
			a.renaming, a.draft = row.ID, row.Title
		}
		if ui.Button(c, "Clone").Clicked() {
			go func() { _ = sessions.Clone(row.ID) }()
		}
		if ui.Button(c, "Archive").Clicked() {
			go func() { _ = sessions.Archive(row.ID) }()
		}
	})
}

// line is a label and its value.
func line(c *ui.Context, label, value string) {
	ui.Row(c).Gap(12).Children(func() {
		ui.Text(c, label).Width(90).TextColor(c.Theme().TextMuted)
		ui.Text(c, value).Selectable()
	})
}

// newSession is the form of a session to create: its directory and an optional title.
func (a *App) newSession(c *ui.Context, sessions *screens.Sessions) {
	ui.Text(c, "New session").Bold().FontSize(24)
	ui.Text(c, "Directory").TextColor(c.Theme().TextMuted)
	ui.TextInput(c.Key("new-cwd"), &a.newCwd).Label("Directory")
	ui.Text(c, "Title (optional)").TextColor(c.Theme().TextMuted)
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

// selectAfter selects a session the server created, from any goroutine.
func (a *App) selectAfter(id string) {
	a.setSel(id)
	a.Invalidate()
}

func (a *App) sel() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.selected
}

func (a *App) setSel(id string) {
	a.mu.Lock()
	a.selected = id
	a.mu.Unlock()
}

// OpenSession selects a session (a deep link, a notification): the list shows it chosen and its sheet opens.
func (a *App) OpenSession(id string) {
	a.mu.Lock()
	a.selected, a.creating, a.archived = id, false, false
	a.mu.Unlock()
	a.Invalidate()
}
