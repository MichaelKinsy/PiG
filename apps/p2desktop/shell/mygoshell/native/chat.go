package native

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/activity"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/screens"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/surfacehost"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// pig additive (D481): the chat screen. The transcript is the display items of the p2 TUI's shared model
// (apps/p2/tui/transcript, built from the Chat contract's `view`) in a virtualized, follow-the-end list; prose is MyGo's
// markdown view (streaming while the answer is written); the prompt bar is a text area with the model and usage row.

// chatMaxWidth is the conversation's column, as the web's.
const chatMaxWidth = 800

// chatOf makes sure the chat screen holds session id's conversation (and lets go of the one it held), and returns it.
func (a *App) chatOf(h *surfacehost.Host, id string) *screens.Chat {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.chat != nil && a.chat.Session() == id {
		return a.chat
	}
	if a.chatScope != nil {
		a.chatScope.Dispose()
		a.chat, a.chatScope = nil, nil
	}
	scope := h.Scope(a.Invalidate)
	chat, err := screens.OpenChat(h, scope, id, 1, a.Invalidate)
	if err != nil {
		scope.Dispose()
		a.chatErr = err
		return nil
	}
	a.chat, a.chatScope, a.chatErr = chat, scope, nil
	a.chatList = ui.ListState{FollowEnd: true}
	a.thinking = map[string]bool{}
	return chat
}

// chatPane is the open session: the transcript, then the prompt bar.
func (a *App) chatPane(c *ui.Context, h *surfacehost.Host, row *screens.Row) {
	chat := a.chatOf(h, row.ID)
	if chat == nil {
		ui.Text(c, "Could not open the conversation: "+a.chatErr.Error()).TextColor(c.Theme().Danger).Padding(24)
		return
	}
	if text := a.firstText; text != "" && a.firstFor == row.ID {
		a.firstText = ""
		go func() { _ = chat.Send(text) }()
	}
	tr, ok := chat.Transcript()
	ui.Column(c).Grow(1).Children(func() {
		switch {
		case !ok:
			ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c) })
		case len(tr.Items) == 0 && !tr.Busy:
			ui.Column(c).Grow(1).Center().Gap(8).Children(func() {
				ui.Text(c, "Say something to start.").TextColor(c.Theme().TextMuted)
			})
		default:
			a.transcriptList(c, chat, &tr)
		}
		a.composer(c, chat, row, &tr)
	})
	if err := chat.LastError(); err != nil && err.Error() != a.shownChatErr {
		a.shownChatErr = err.Error()
		c.Toast(err.Error())
	}
}

// transcriptList is the virtualized list of the conversation's items.
func (a *App) transcriptList(c *ui.Context, chat *screens.Chat, tr *transcript.Transcript) {
	items := a.activityItems(tr)
	n := len(items)
	more := 0
	if tr.Earlier {
		more = 1
	}
	a.chatList.Key = func(i int) any {
		if more == 1 {
			if i == 0 {
				return "earlier"
			}
			i--
		}
		if i >= n {
			return "tail"
		}
		return items[i].ItemKey()
	}
	count := n + more + 1 // the tail: the working line
	ui.List(c, &a.chatList, count, func(i int) {
		top := float32(0)
		if i == 0 {
			top = 16
		}
		ui.Column(c).Padding(top, 24, 0).AlignItems(ui.Center).Children(func() {
			ui.Column(c).MaxWidth(chatMaxWidth).FillWidth().Children(func() {
				switch {
				case more == 1 && i == 0:
					if ui.Button(c, "Load earlier messages").Clicked() {
						chat.LoadEarlier()
					}
				case i-more >= n:
					a.workingLine(c, tr)
				default:
					a.item(c, items[i-more], chat)
				}
			})
		})
	}).Grow(1)
	// Reaching the top loads the page before it, as the web's scroll does.
	if first, _ := a.chatList.Visible(); first == 0 && tr.Earlier {
		chat.LoadEarlier()
	}
}

// workingLine is what the agent is doing after the last item.
func (a *App) workingLine(c *ui.Context, tr *transcript.Transcript) {
	t := c.Theme()
	switch {
	case len(tr.Compactions) > 0:
		ui.Row(c).Gap(8).Padding(8, 0).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(12, 12)
			ui.Text(c, "Compacting the context…").FontSize(13).TextColor(t.TextMuted)
		})
	case tr.Retry != nil:
		ui.Text(c, "Retrying: "+tr.Retry.Error).FontSize(13).TextColor(t.Warning).Padding(8, 0)
	default:
		ui.Spacer(c).Height(8)
	}
}

// item is one row of the transcript: the activity model's items (internal/activity: a turn's thinking and calls on one row, the
// agent's texts, its answer) and the people's messages, markers and failures as they are.
func (a *App) item(c *ui.Context, item activity.Item, chat *screens.Chat) {
	t := c.Theme()
	switch it := item.(type) {
	case activity.Activity:
		a.activityRow(c, it)
	case activity.Text:
		ui.Column(c).Padding(8, 0, 0).Children(func() { markdown.View(c.Key(it.Key), it.Text, &a.md) })
	case activity.Answer:
		ui.Column(c).Padding(8, 0, 0).Gap(8).Children(func() {
			if it.Text != "" {
				if it.Streaming {
					markdown.Stream(c.Key(it.Key), it.Text, &a.md)
				} else {
					markdown.View(c.Key(it.Key), it.Text, &a.md)
				}
			}
			if !it.Streaming && it.Text != "" {
				copy := ui.Row(c.Key(it.Key+"/copy")).Size(32, 32).Center().Radius(6).Focusable().Label("Copy").Tooltip("Copy").Children(func() {
					ui.Icon(c, iconCopy).Size(15, 15).TextColor(t.TextMuted)
				})
				if copy.Clicked() {
					mygo.Clipboard.WriteText(it.Text)
				}
			}
			if it.Message != nil && it.Message.ErrorMessage != "" {
				ui.Text(c, it.Message.ErrorMessage).TextColor(t.Danger).Selectable()
			}
		})
	case activity.Passed:
		a.passed(c, it.Item)
	}
}

// passed is a transcript item the chat shows as it is.
func (a *App) passed(c *ui.Context, item transcript.Item) {
	t := c.Theme()
	switch it := item.(type) {
	case transcript.UserItem:
		ui.Row(c).Padding(8, 0, 0).Children(func() {
			ui.Spacer(c)
			ui.Column(c).MaxWidth(560).Padding(8, 12).Radius(4).Background(t.SurfaceHover).Children(func() {
				who := ""
				switch {
				case it.Agent != nil:
					who = it.Agent.From
				case it.Peer != nil:
					who = it.Peer.Title
				case it.Author != nil:
					who = it.Author.Name
				}
				if who != "" {
					ui.Text(c, who).FontSize(11).FontWeight(600).TextColor(t.TextMuted).Padding(0, 0, 4)
				}
				ui.Text(c, it.Text).FontSize(16).LineHeight(1.5).Selectable()
				for _, f := range it.Files {
					ui.Text(c, f).FontSize(12).Font("monospace").TextColor(t.TextMuted)
				}
			})
		})
	case transcript.FailureItem:
		ui.Text(c, it.Text).TextColor(t.Danger).Selectable().Padding(10, 0)
	case transcript.MarkerItem:
		ui.Row(c).Gap(8).Padding(8, 0).AlignItems(ui.Center).Children(func() {
			ui.Divider(c).Grow(1)
			ui.Text(c, strings.TrimSpace(it.Label+" "+it.Detail)).FontSize(12).TextColor(t.TextMuted)
			ui.Divider(c).Grow(1)
		})
	case transcript.GapItem:
		ui.Text(c, "Earlier messages are not loaded.").FontSize(12).TextColor(t.TextMuted).Padding(8, 0)
	}
}

// clip is s cut to at most n bytes at a rune boundary, with an ellipsis.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n] + "…"
}

// composer is the prompt bar pinned under the transcript, as the web's: a rule, the one-line text area that grows with what is typed
// (Enter sends, Shift+Enter breaks the line), the + and the send or stop button, then the model, the viewers, cache hit rate and
// cost, and the context bar.
func (a *App) composer(c *ui.Context, chat *screens.Chat, row *screens.Row, tr *transcript.Transcript) {
	var modelName string
	var window float64
	if m := chat.Model(); m != nil {
		modelName = m.Name
		if modelName == "" {
			modelName = m.ModelID
		}
		window = m.ContextWindow
	}
	stats := chat.Stats()
	hit, hasHit := stats.CacheHit()
	m := chat.Metrics()
	a.promptBar(c, promptBar{
		Busy: tr != nil && tr.Busy, Model: modelName, Thinking: row.ThinkingLevel, Tokens: stats.ContextTokens, Window: window,
		Clients: m.Clients, Cost: m.Cost, HasCost: m.HasCost, Hit: hit, HasHit: hasHit,
		Send: func(text string) { go func() { _ = chat.Send(text) }() },
		Stop: func() { go func() { _ = chat.Stop() }() },
	})
}

// promptBar is the composer's data: what it shows and what it does.
type promptBar struct {
	Busy     bool
	Model    string
	Thinking string
	Tokens   float64
	Window   float64
	Clients  float64
	Cost     float64
	HasCost  bool
	Hit      float64
	HasHit   bool
	Send     func(string)
	Stop     func()
}

// cost is a dollar amount as the web's usage row shows it.
func cost(v float64) string {
	switch {
	case v >= 1:
		return fmt.Sprintf("$%.2f", v)
	case v >= 0.01:
		return fmt.Sprintf("$%.3f", v)
	}
	return fmt.Sprintf("$%.4f", v)
}

// pickFiles opens the system's file dialog for the composer's +; the chosen files go into the message as @path mentions (the
// agent reads them with its tools, as with the web's @ completion), added at the next frame.
func (a *App) pickFiles() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "Attach files", Multiple: true})
		if err != nil || len(paths) == 0 {
			return
		}
		a.mu.Lock()
		a.picked = append(a.picked, paths...)
		a.mu.Unlock()
		a.Invalidate()
	}()
}

func (a *App) promptBar(c *ui.Context, p promptBar) {
	t := c.Theme()
	a.mu.Lock()
	picked := a.picked
	a.picked = nil
	a.mu.Unlock()
	for _, path := range picked {
		sep := ""
		if a.draftText != "" && !strings.HasSuffix(a.draftText, " ") && !strings.HasSuffix(a.draftText, "\n") {
			sep = " "
		}
		a.draftText += sep + "@" + path + " "
	}
	ui.Column(c).AlignItems(ui.Center).Children(func() {
		ui.Column(c).MaxWidth(832).FillWidth().Padding(8, 16, 0).BorderWidth(1, 0, 1, 0).BorderColor(t.Border).Children(func() {
			input := ui.TextAreaBase(c.Key("prompt"), &a.draftText).Placeholder("Message PiG…").Label("Message PiG").
				FontSize(16).LineHeight(1.5).Lines(1, 8).AutoFocus()
			send := input.Shortcut(0, ui.KeyEnter)
			ui.Row(c).Height(40).AlignItems(ui.Center).Gap(4).Children(func() {
				attach := ui.Row(c).Size(40, 40).Focusable().Label("Attach files").Tooltip("Attach files").Children(func() {
					ui.Icon(c, iconPlus).Size(20, 20).TextColor(t.TextMuted)
				})
				if attach.Clicked() {
					a.pickFiles()
				}
				ui.Spacer(c)
				ready := strings.TrimSpace(a.draftText) != ""
				if p.Busy && !ready {
					b := ui.Row(c).Size(40, 40).Focusable().Label("Stop").Tooltip("Stop").Children(func() {
						ui.Icon(c, iconStop).Size(20, 20).TextColor(t.Text)
					})
					if b.Clicked() {
						p.Stop()
					}
				} else {
					colour := t.Text
					if !ready {
						colour = t.TextMuted.Alpha(0.7)
					}
					b := ui.Row(c).Size(40, 40).Focusable().Label("Send").Tooltip("Send").Disabled(!ready).Children(func() {
						ui.Icon(c, iconSend).Size(20, 20).TextColor(colour)
					})
					if b.Clicked() {
						send = true
					}
				}
			})
			if send {
				text := strings.TrimSpace(a.draftText)
				a.draftText = ""
				if text != "" {
					p.Send(text)
				}
			}
		})
		ui.Column(c).MaxWidth(832).FillWidth().Padding(4, 16, 8).Children(func() {
			ui.Row(c).Height(24).AlignItems(ui.Center).Gap(12).Children(func() {
				label := p.Model
				if label == "" {
					label = "No model"
				}
				if p.Thinking != "" && p.Thinking != "off" {
					label += " · " + p.Thinking
				}
				ui.Text(c, label).FontSize(11).TextColor(t.TextMuted).Underline().SingleLine()
				ui.Spacer(c)
				ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, iconUsers).Size(13, 13).TextColor(t.TextMuted)
					ui.Text(c, fmt.Sprint(int(p.Clients))).FontSize(11).Font("monospace").TextColor(t.TextMuted)
					ui.Text(c, "·").FontSize(11).Font("monospace").TextColor(t.TextMuted)
					hit := "chr —"
					if p.HasHit {
						hit = fmt.Sprintf("chr %d%%", int(p.Hit*100+0.5))
					}
					ui.Text(c, hit).FontSize(11).Font("monospace").TextColor(t.TextMuted)
					if p.HasCost {
						ui.Text(c, "·").FontSize(11).Font("monospace").TextColor(t.TextMuted)
						ui.Text(c, cost(p.Cost)).FontSize(11).Font("monospace").TextColor(t.TextMuted)
					}
				})
			})
			if p.Window > 0 {
				pct := int(p.Tokens/p.Window*100 + 0.5)
				ui.Row(c).Height(16).AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Progress(c, float64(min(100, max(0, pct)))/100).Height(2).Grow(1)
					ui.Text(c, fmt.Sprintf("%d%%/%s", pct, strings.Replace(tokensLabel(p.Window), ".0k", "k", 1))).FontSize(11).Font("monospace").TextColor(t.TextMuted)
				})
			}
		})
	})
}

// tokensLabel is a token count as the web's usage row shows it: 128k, 1M, 900.
func tokensLabel(n float64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%gM", n/1_000_000)
	case n >= 1000:
		return fmt.Sprintf("%gk", float64(int(n/100))/10)
	}
	return fmt.Sprint(int(n))
}
