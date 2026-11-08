package native

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/screens"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/surfacehost"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// pig additive (D481): the chat screen. The transcript is the display items of the p2 TUI's shared model
// (apps/p2/tui/transcript, built from the Chat contract's `view`) in a virtualized, follow-the-end list; prose is MyGo's
// markdown view (streaming while the answer is written); the prompt bar is a text area with the model and usage row.

// chatMaxWidth is the conversation's column, as the web's.
const chatMaxWidth = 760

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
	n := len(tr.Items)
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
		return tr.Items[i].ItemKey()
	}
	count := n + more + 1 // the tail: the working line
	ui.List(c, &a.chatList, count, func(i int) {
		ui.Column(c).Padding(0, 24).AlignItems(ui.Center).Children(func() {
			ui.Column(c).MaxWidth(chatMaxWidth).Fill().Children(func() {
				switch {
				case more == 1 && i == 0:
					if ui.Button(c, "Load earlier messages").Clicked() {
						chat.LoadEarlier()
					}
				case i-more >= n:
					a.workingLine(c, tr)
				default:
					a.item(c, tr.Items[i-more], chat)
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
	case tr.Working:
		ui.Row(c).Gap(8).Padding(8, 0).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(12, 12)
			ui.Text(c, "Working…").FontSize(13).TextColor(t.TextMuted)
		})
	default:
		ui.Spacer(c).Height(8)
	}
}

// item is one row of the transcript.
func (a *App) item(c *ui.Context, item transcript.Item, chat *screens.Chat) {
	t := c.Theme()
	switch it := item.(type) {
	case transcript.UserItem:
		ui.Row(c).Padding(10, 0).Children(func() {
			ui.Spacer(c)
			ui.Column(c).MaxWidth(560).Padding(10, 14).Radius(12).Background(t.SurfaceHover).Gap(6).Children(func() {
				if it.Peer != nil || it.Agent != nil {
					who := ""
					if it.Agent != nil {
						who = it.Agent.From
					} else {
						who = it.Peer.Title
					}
					ui.Text(c, who).FontSize(11).TextColor(t.TextMuted)
				}
				ui.Text(c, it.Text).Selectable()
				for _, f := range it.Files {
					ui.Text(c, f).FontSize(12).Font("monospace").TextColor(t.TextMuted)
				}
			})
		})
	case transcript.AssistantItem:
		ui.Column(c).Gap(8).Padding(10, 0).Children(func() { a.assistant(c, &it) })
	case transcript.FailureItem:
		ui.Text(c, it.Text).TextColor(t.Danger).Selectable().Padding(10, 0)
	case transcript.MarkerItem:
		ui.Row(c).Gap(8).Padding(8, 0).AlignItems(ui.Center).Children(func() {
			ui.Divider(c).Grow(1)
			ui.Text(c, strings.TrimSpace(it.Label+" "+it.Detail)).FontSize(12).TextColor(t.TextMuted)
			ui.Divider(c).Grow(1)
		})
	case transcript.ToolItem:
		a.toolCard(c, &it.Tool, it.Key)
	case transcript.GapItem:
		ui.Text(c, "Earlier messages are not loaded.").FontSize(12).TextColor(t.TextMuted).Padding(8, 0)
	}
}

// assistant is an assistant message: its blocks in order (thinking folded, prose as markdown) and its tool cards.
func (a *App) assistant(c *ui.Context, it *transcript.AssistantItem) {
	t := c.Theme()
	if it.Message != nil {
		for i, block := range it.Message.Content {
			switch b := block.(type) {
			case ai.TextContent:
				if strings.TrimSpace(b.Text) == "" {
					continue
				}
				if it.Streaming && i == len(it.Message.Content)-1 {
					markdown.Stream(c.Key(fmt.Sprint(it.Key, "/", i)), b.Text, &a.md)
				} else {
					markdown.View(c.Key(fmt.Sprint(it.Key, "/", i)), b.Text, &a.md)
				}
			case ai.ThinkingContent:
				a.thought(c, fmt.Sprint(it.Key, "/", i), b)
			}
		}
		if it.Message.ErrorMessage != "" {
			ui.Text(c, it.Message.ErrorMessage).TextColor(t.Danger).Selectable()
		}
	}
	for _, tool := range it.Tools {
		a.toolCard(c, &tool, fmt.Sprint(it.Key, "/", tool.ID))
	}
}

// thought is a thinking block: a muted line that opens to its text.
func (a *App) thought(c *ui.Context, key string, b ai.ThinkingContent) {
	t := c.Theme()
	if b.Redacted || strings.TrimSpace(b.Thinking) == "" {
		return
	}
	open := a.thinking[key]
	head := ui.Row(c.Key(key + "/head")).Gap(6).AlignItems(ui.Center).Focusable().Label("Thinking").Children(func() {
		icon := iconChevronR
		if open {
			icon = iconChevronU
		}
		ui.Icon(c, icon).Size(12, 12).TextColor(t.TextMuted)
		ui.Text(c, "Thinking").FontSize(12).TextColor(t.TextMuted)
	})
	if head.Clicked() {
		a.thinking[key] = !open
	}
	if open {
		ui.Column(c).Padding(0, 0, 0, 18).Children(func() {
			ui.Text(c, b.Thinking).FontSize(13).TextColor(t.TextMuted).Selectable()
		})
	}
}

// toolCard is a tool call as one card: its name and arguments' gist, its status, and its output once it has one.
func (a *App) toolCard(c *ui.Context, tool *transcript.ToolView, key string) {
	t := c.Theme()
	status, colour := string(tool.Status), t.TextMuted
	switch tool.Status {
	case transcript.ToolError:
		colour = t.Danger
	case transcript.ToolDone:
		colour = t.Success
	}
	ui.Column(c.Key("tool/"+key)).Gap(6).Padding(10, 12).Radius(8).Border(1, t.Border).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tool.Name).Bold().FontSize(13).Font("monospace")
			ui.Spacer(c)
			if tool.Status == transcript.ToolRunning {
				ui.Spinner(c).Size(12, 12)
			}
			ui.Text(c, status).FontSize(11).TextColor(colour)
		})
		if strings.TrimSpace(tool.Output) != "" {
			ui.Text(c, clip(tool.Output, 2000)).FontSize(12).Font("monospace").TextColor(t.TextMuted).Selectable()
		}
	})
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

// composer is the prompt bar pinned under the transcript: a text area (Enter sends, Shift+Enter breaks the line), the send or
// stop button, and the model and usage row.
func (a *App) composer(c *ui.Context, chat *screens.Chat, row *screens.Row, tr *transcript.Transcript) {
	var model *string
	if m := chat.Model(); m != nil {
		name := m.Name
		if name == "" {
			name = m.ModelID
		}
		model = &name
	}
	var modelName string
	if model != nil {
		modelName = *model
	}
	var window float64
	if m := chat.Model(); m != nil {
		window = m.ContextWindow
	}
	a.promptBar(c, promptBar{
		Busy: tr != nil && tr.Busy, Model: modelName, Thinking: row.ThinkingLevel, Tokens: chat.Stats().ContextTokens, Window: window,
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
	Send     func(string)
	Stop     func()
}

func (a *App) promptBar(c *ui.Context, p promptBar) {
	t := c.Theme()
	ui.Column(c).Padding(0, 24, 12).AlignItems(ui.Center).Children(func() {
		ui.Column(c).MaxWidth(chatMaxWidth).Fill().Radius(12).Border(1, t.Border).Children(func() {
			input := ui.TextArea(c.Key("prompt"), &a.draftText).Placeholder("Message PiG…").Label("Message PiG").
				BorderWidth(0).Background(ui.Color{}).Lines(1, 8).AutoFocus()
			send := input.Shortcut(0, ui.KeyEnter)
			ui.Row(c).Padding(4, 8).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Spacer(c)
				ready := strings.TrimSpace(a.draftText) != ""
				switch {
				case p.Busy && !ready:
					if iconButton(c, iconStop, "Stop").Clicked() {
						p.Stop()
					}
				default:
					b := iconButton(c, iconSend, "Send").Disabled(!ready)
					if b.Clicked() {
						send = true
					}
				}
			})
			if send {
				text := strings.TrimSpace(a.draftText)
				if text != "" {
					a.draftText = ""
					p.Send(text)
				} else {
					a.draftText = ""
				}
			}
			ui.Row(c).Padding(6, 12).Gap(12).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
				label := p.Model
				if label == "" {
					label = "No model"
				}
				if p.Thinking != "" {
					label += " · " + p.Thinking
				}
				ui.Text(c, label).FontSize(11).TextColor(t.TextMuted).Underline().SingleLine()
				ui.Spacer(c)
				if p.Window > 0 {
					pct := int(p.Tokens / p.Window * 100)
					ui.Text(c, fmt.Sprintf("%d%%/%s", pct, tokensLabel(p.Window))).FontSize(11).Font("monospace").TextColor(t.TextMuted)
				}
			})
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
