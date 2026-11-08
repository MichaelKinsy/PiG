package native

import (
	"fmt"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/activity"
	"github.com/egoist/mygo/ui"
)

// a turn's activity row, as the web's: the check, the words ("Thought · 1 step", "Read 3 files · 4 steps"),
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
			ui.Icon(c, iconChevronR).Size(20, 20).TextColor(t.TextMuted)
		})
		if head.Hovered() {
			head.Background(t.SurfaceHover)
		}
		if head.Clicked() {
			a.sheetKey, a.sheetStep = it.Key, ""
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

// activitySheet is the opened row as the web's sheet: a modal listing the run's steps (the thinking and each call, with its state and
// what it said); a call opens to its detail (internal tooldetail.go) with a way back.
func (a *App) activitySheet(c *ui.Context) {
	if a.sheetKey == "" {
		return
	}
	var act *activity.Activity
	for _, item := range a.actItems {
		if x, ok := item.(activity.Activity); ok && x.Key == a.sheetKey {
			x := x
			act = &x
		}
	}
	open := act != nil
	t := c.Theme()
	ui.Modal(c, &open, func() {
		if act == nil {
			return
		}
		ui.Column(c).Width(680).Gap(0).Children(func() {
			if a.sheetStep != "" {
				for i := range act.Steps {
					if act.Steps[i].ID == a.sheetStep {
						a.stepDetail(c, &act.Steps[i])
						return
					}
				}
				a.sheetStep = ""
			}
			head := fmt.Sprintf("%d %s", len(act.Entries), map[bool]string{true: "step", false: "steps"}[len(act.Entries) == 1])
			if d := durationOf(*act); d != "" {
				head += " · " + d
			}
			ui.Row(c).Height(44).AlignItems(ui.Center).Children(func() {
				ui.Text(c, head).Bold().FontSize(16).Grow(1)
				if iconButton(c, iconClose, "Close").Clicked() {
					a.sheetKey = ""
				}
			})
			for _, e := range act.Entries {
				e := e
				key := "entry/" + e.ID
				if e.Kind == activity.EntryCall {
					key = "entry/" + e.Step.ID
				}
				row := ui.Row(c.Key(key)).Height(44).Gap(10).AlignItems(ui.Center).Padding(0, 4).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Focusable().Children(func() {
					switch e.Kind {
					case activity.EntryThought:
						ui.Icon(c, iconSpark).Size(16, 16).TextColor(t.TextMuted)
						ui.Text(c, "Thought").FontSize(13).FontWeight(600)
						ui.Text(c, clip(firstLineOf(e.Text), 120)).FontSize(12).TextColor(t.TextMuted).SingleLine().Ellipsis("…").Grow(1)
					case activity.EntryCall:
						d := activity.Describe(*e.Step)
						colour := t.TextMuted
						if e.Step.Status == activity.StepError {
							colour = t.Danger
						}
						ui.Icon(c, kindIcon(d.Label.Kind)).Size(16, 16).TextColor(t.TextMuted)
						ui.Text(c, d.Label.Verb).FontSize(13).FontWeight(600)
						obj := ui.Text(c, d.Label.Object).FontSize(12).TextColor(t.TextMuted).SingleLine().Ellipsis("…").Grow(1)
						if d.Label.Style == activity.StyleCode {
							obj.Font("monospace")
						}
						ui.Text(c, activity.StepState(*e.Step)).FontSize(11).TextColor(colour)
					}
					ui.Icon(c, iconChevronR).Size(16, 16).TextColor(t.TextMuted)
				})
				if row.Hovered() {
					row.Background(t.SurfaceHover.Alpha(0.5))
				}
				if row.Clicked() && e.Kind == activity.EntryCall {
					a.sheetStep = e.Step.ID
				}
			}
		})
	})
	if !open {
		a.sheetKey, a.sheetStep = "", ""
	}
}

func firstLineOf(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return ""
}

// stepDetail is one call in the sheet: its words, then its input and output.
func (a *App) stepDetail(c *ui.Context, s *activity.Step) {
	t := c.Theme()
	d := activity.Describe(*s)
	ui.Row(c).Height(44).Gap(8).AlignItems(ui.Center).Children(func() {
		if iconButton(c, iconChevronL, "Back").Clicked() {
			a.sheetStep = ""
		}
		ui.Icon(c, kindIcon(d.Label.Kind)).Size(16, 16).TextColor(t.TextMuted)
		ui.Text(c, d.Label.Verb).FontSize(14).FontWeight(600)
		ui.Text(c, d.Label.Object).FontSize(13).TextColor(t.TextMuted).SingleLine().Ellipsis("…").Grow(1)
		ui.Text(c, activity.StepState(*s)).FontSize(11).TextColor(t.TextMuted)
	})
	ui.Column(c).MaxHeight(520).Children(func() {
		ui.Scroll(c).Children(func() { a.toolDetail(c, *s) })
	})
}
