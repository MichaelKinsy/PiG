package native

import (
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/tree/contract"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/screens"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/surfacehost"
	"github.com/egoist/mygo/ui"
)

// `/tree` as the web's drawer and chat have it (docs/tree.md): a session with two branches or more lists
// them under its row behind a toggle (choosing another switches the session to it, the pencil renames it), and a message of the
// main conversation offers "Branch from here" (a user message's text goes back to the prompt).

// branchEdit is the sheet of a branch's label.
type branchEdit struct {
	open         bool
	session      string
	conversation int
	// label is what the row shows, title the session's (what none shows), draft the text being typed.
	label, title, draft string
}

// treeOf is the tree actions of the open host.
func (a *App) treeOf(h *surfacehost.Host) *screens.Tree {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tree == nil {
		a.tree = screens.NewTree(h)
	}
	return a.tree
}

// branchToggle is the count of a session's branches with a chevron, the web's expand control; true when it was clicked.
func (a *App) branchToggle(c *ui.Context, r *screens.Row) bool {
	t := c.Theme()
	open := a.expanded[r.ID]
	icon := iconChevronD
	if open {
		icon = iconChevronU
	}
	toggle := ui.Row(c.Key("toggle/"+r.ID)).AlignItems(ui.Center).Gap(3).Padding(2, 4).Radius(4).Focusable().
		Label(screens.BranchCount(r)).Tooltip(screens.BranchCount(r)).Children(func() {
		ui.Icon(c, iconBranch).Size(11, 11).TextColor(t.TextMuted)
		ui.Text(c, strconv.Itoa(len(r.Branches))).FontSize(11).Font("monospace").TextColor(t.TextMuted)
		ui.Icon(c, icon).Size(12, 12).TextColor(t.TextMuted)
	})
	if toggle.Hovered() {
		toggle.Background(t.Surface)
	}
	if !toggle.Clicked() {
		return false
	}
	if a.expanded == nil {
		a.expanded = map[string]bool{}
	}
	a.expanded[r.ID] = !open
	return true
}

// branchRow is a branch under its session: its label (the session title for none), "current" for the one the session continues
// in, and the pencil. Choosing another branch switches the session to it.
func (a *App) branchRow(c *ui.Context, b *screens.BranchItem, open bool, pal surfacehost.Palette) {
	t := c.Theme()
	key := c.Key(strconv.Itoa(b.Branch.Conversation) + "/" + b.Row.ID)
	active := open && b.Current
	var rename ui.Element
	row := ui.Row(key).Padding(8, 8, 8, 32).Gap(8).AlignItems(ui.Center).Focusable().Label(b.Label).
		Tooltip(map[bool]string{true: "The current branch", false: "Switch the session to this branch"}[b.Current]).Children(func() {
		ui.Icon(c, iconBranch).Size(13, 13).TextColor(t.TextMuted)
		ui.Text(c, b.Label).FontSize(14).SingleLine().Grow(1)
		if b.Current {
			col := pal.Secondary(rowBackground(active))
			ui.Text(c, "current").FontSize(11).Font("monospace").TextColor(ui.RGB(col.R, col.G, col.B))
		}
		rename = ui.Row(c.Key("rename/"+strconv.Itoa(b.Branch.Conversation)+"/"+b.Row.ID)).Size(24, 24).Center().Radius(4).Focusable().
			Label("Rename branch " + b.Label).Tooltip("Rename branch").Children(func() {
			ui.Icon(c, iconPencil).Size(12, 12).TextColor(t.TextMuted)
		})
	})
	switch {
	case active:
		row.Background(t.SurfaceHover)
	case row.Hovered():
		row.Background(t.SurfaceHover.Alpha(0.5))
	}
	if rename.Clicked() {
		a.branch = branchEdit{open: true, session: b.Row.ID, conversation: b.Branch.Conversation, label: b.Label, title: b.Row.Title, draft: b.Label}
		return
	}
	if !row.Clicked() {
		return
	}
	a.setSel(b.Row.ID)
	a.creating, a.showDrawer = false, false
	if !b.Current {
		h, session, conversation := a.host, b.Row.ID, b.Branch.Conversation
		go func() { a.toastError(a.treeOf(h).Select(session, conversation)) }()
	}
}

// branchSheet is the sheet of a branch's label (the web's BranchSheet): saving closes it, the row shows the new label from the
// catalog; only conversation 1 may go back to the session title.
func (a *App) branchSheet(c *ui.Context) {
	if !a.branch.open {
		return
	}
	t := c.Theme()
	edit := &a.branch
	h := a.host
	ui.Modal(c, &edit.open, func() {
		ui.Text(c, "Branch name").Bold().FontSize(18)
		ui.Text(c, "Label in the sidebar and /tree").FontSize(12).TextColor(t.TextMuted)
		ui.TextInput(c.Key("branch-label"), &edit.draft).Label("Branch label").Placeholder(edit.title)
		next := strings.Join(strings.Fields(edit.draft), " ")
		resettable := edit.conversation == 1
		unchanged := next == edit.label
		if resettable {
			ui.Text(c, "The first branch shows the session title until it gets a label of its own.").FontSize(12).TextColor(t.TextMuted)
		}
		save := func(label string) {
			session, conversation := edit.session, edit.conversation
			edit.open = false
			go func() { a.toastError(a.treeOf(h).Rename(session, conversation, label)) }()
		}
		ui.Row(c).Gap(8).Children(func() {
			canSave := !unchanged && (next != "" || resettable) && len([]rune(next)) <= contract.PreviewLength
			if ui.Button(c, "Save").Disabled(!canSave).Clicked() {
				save(next)
			}
			if resettable && edit.label != edit.title && ui.Button(c, "Use the session title").Clicked() {
				save("")
			}
			if ui.Button(c, "Cancel").Clicked() {
				edit.open = false
			}
		})
	})
}

// branchFrom continues the session at message entry (docs/tree.md); a user message's text goes back to the prompt.
func (a *App) branchFrom(session string, entry int) {
	h := a.host
	go func() {
		_, text, err := a.treeOf(h).Navigate(session, entry)
		if err != nil {
			a.toastError(err)
			return
		}
		if text != "" {
			a.mu.Lock()
			a.pendingDraft = text
			a.mu.Unlock()
		}
		a.Invalidate()
	}()
}

// branchButton is "Branch from here" under a message of the main conversation.
func (a *App) branchButton(c *ui.Context, key, session string, entry int) {
	if iconButton(c.Key(key+"/branch"), iconBranch, "Branch from here").Clicked() {
		a.branchFrom(session, entry)
	}
}
