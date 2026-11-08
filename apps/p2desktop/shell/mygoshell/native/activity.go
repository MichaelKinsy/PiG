package native

import (
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/activity"
	"github.com/egoist/mygo/ui"
)

// pig additive (D481): a turn's activity row, as the web's: the check, the words ("Thought · 1 step", "Read 3 files · 4 steps"),
// the time it took and a chevron; the row opens to the turn's thinking and calls.

// activityItems are the chat's items for the transcript, kept while the transcript (a new slice at every rebuild) and its
// state stay the same, so a frame costs nothing over a long conversation.
func (a *App) activityItems(tr *transcript.Transcript) []activity.Item {
	var first *transcript.Item
	if len(tr.Items) > 0 {
		first = &tr.Items[0]
	}
	if a.actItems != nil && a.actFor == first && a.actLen == len(tr.Items) && a.actWorking == tr.Busy {
		return a.actItems
	}
	a.actItems = activity.Items(tr.Items, tr.Busy)
	a.actFor, a.actLen, a.actWorking = first, len(tr.Items), tr.Busy
	return a.actItems
}

func kindIcon(k activity.Kind) *ui.SVG {
	switch k {
	case activity.KindCommand:
		return iconTerminal
	case activity.KindSearch:
		return iconSearch
	case activity.KindRead:
		return iconFile
	case activity.KindEdit:
		return iconPencil
	}
	return iconTool
}

func (a *App) activityRow(c *ui.Context, it activity.Activity) {
	t := c.Theme()
	open := a.thinking[it.Key]
	words, extra, label, failed := it.Face()
	colour := t.TextMuted
	if failed {
		colour = t.Danger
	}
	ui.Column(c.Key(it.Key)).Padding(24, 0, 0).Children(func() {
		head := ui.Row(c.Key("head")).Height(44).Gap(8).AlignItems(ui.Center).Radius(12).Focusable().Label(words).Children(func() {
			switch {
			case it.Working && label == nil:
				ui.Icon(c, iconSpark).Size(20, 20).TextColor(colour)
			case it.Working:
				ui.Icon(c, kindIcon(label.Kind)).Size(20, 20).TextColor(colour)
			default:
				ui.Icon(c, iconCheck).Size(20, 20).TextColor(colour)
			}
			ui.Text(c, words+extra).FontSize(12).FontWeight(500).TextColor(colour).SingleLine().Ellipsis("…")
			ui.Spacer(c)
			if d := durationOf(it); d != "" {
				ui.Text(c, d).FontSize(11).TextColor(t.TextMuted)
			}
			ic := iconChevronR
			if open {
				ic = iconChevronU
			}
			ui.Icon(c, ic).Size(20, 20).TextColor(t.TextMuted)
		})
		if head.Hovered() {
			head.Background(t.SurfaceHover)
		}
		if head.Clicked() {
			a.thinking[it.Key] = !open
		}
		if open {
			a.activitySteps(c, it)
		}
	})
}

// durationOf is how long the run took (or has taken, while it works).
func durationOf(it activity.Activity) string {
	switch {
	case it.StartedAt == 0:
		return ""
	case it.EndedAt != 0:
		return activity.Elapsed(it.EndedAt - it.StartedAt)
	case it.Working:
		return activity.Elapsed(time.Now().UnixMilli() - it.StartedAt)
	}
	return ""
}

// activitySteps are the opened row's entries: the thinking in full and each call with its state and what it said.
func (a *App) activitySteps(c *ui.Context, it activity.Activity) {
	t := c.Theme()
	ui.Column(c).Gap(10).Padding(4, 0, 8, 28).Children(func() {
		for _, e := range it.Entries {
			switch e.Kind {
			case activity.EntryThought:
				ui.Column(c.Key(e.ID)).Gap(2).Children(func() {
					ui.Text(c, "Thought").FontSize(12).FontWeight(500).TextColor(t.TextMuted)
					ui.Text(c, e.Text).FontSize(13).TextColor(t.TextMuted).Selectable()
				})
			case activity.EntryCall:
				a.stepRow(c, e.Step)
			}
		}
	})
}

func (a *App) stepRow(c *ui.Context, s *activity.Step) {
	t := c.Theme()
	d := activity.Describe(*s)
	state := activity.StepState(*s)
	colour := t.TextMuted
	if s.Status == activity.StepError {
		colour = t.Danger
	}
	ui.Column(c.Key("step/" + s.ID)).Gap(2).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, kindIcon(d.Label.Kind)).Size(14, 14).TextColor(t.TextMuted)
			ui.Text(c, d.Label.Verb).FontSize(13).TextColor(t.TextMuted)
			obj := ui.Text(c, d.Label.Object).FontSize(13).SingleLine().Ellipsis("…")
			switch d.Label.Style {
			case activity.StyleFile:
				obj.FontWeight(600)
			case activity.StyleCode:
				obj.Font("monospace").FontSize(12)
			}
			ui.Spacer(c)
			ui.Text(c, state).FontSize(11).TextColor(colour)
		})
		if d.Description != "" {
			ui.Text(c, clip(d.Description, 200)).FontSize(12).TextColor(t.TextMuted).Padding(0, 0, 0, 22).SingleLine().Ellipsis("…")
		}
	})
}
