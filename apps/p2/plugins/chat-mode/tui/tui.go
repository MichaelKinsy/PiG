// Package tui is chat mode in the TUI (p2-tui-port.md P11b), after the web facet: chat and mention entries show their reply quote and note (the shared
// `TranscriptEntries` display), a composer chip shows the mode (the owner toggles it with `/chat-mode`), and while it is on `/reply` picks the message the next
// send answers and `@pi` completes first.
//
// The entry displays are chat-mode's display.go over the same data; this package cannot import it (the plugin's package imports this one to link the facet).
//
// Ports apps/p2/plugins/chat-mode/tui.ts.
package tui

import (
	"context"
	"fmt"
	"strconv"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/chat-mode/contract"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	chordtui "github.com/MichaelKinsy/PiG/chord/next/tui"
	pitui "github.com/MichaelKinsy/PiG/tui"

	p2tui "github.com/MichaelKinsy/PiG/apps/p2/tui"
)

func hint(name string) string {
	return fmt.Sprintf("messages go to the people here; %s asks %s", contract.MentionToken(name), name)
}

// Chip is the `composer:chat-mode` chip: the viewed conversation's chat mode, and the reply switch of the chat surface.
type Chip struct {
	runtime  *runtime.TuiRuntime
	scope    *chordtui.TuiScope
	identity string
	hasID    bool
	mode     *host.Reference
	// shownEnabled is the switch as shown (the owner's flip while its call is in flight); switching flips it.
	shownEnabled func() bool
	switching    chordtui.OptimisticAdd[bool]
	enabledRead  func() (delta.JsonValue, bool)
	waitingRead  func() (delta.JsonValue, bool)
	nameRead     func() (delta.JsonValue, bool)
	remove       func()
}

// NewChip is the chip of rt.
func NewChip(rt *runtime.TuiRuntime) *Chip {
	c := &Chip{runtime: rt, shownEnabled: func() bool { return false }}
	c.remove = rt.AddListener(c.changed)
	c.changed()
	return c
}

func (c *Chip) changed() {
	c.syncReplies()
	rt := c.runtime
	if c.hasID && rt.Identity() == c.identity {
		return
	}
	c.identity, c.hasID = rt.Identity(), true
	if c.scope != nil {
		c.scope.Dispose()
		c.scope = nil
	}
	c.mode, c.switching = nil, nil
	c.shownEnabled = func() bool { return false }
	c.enabledRead, c.waitingRead, c.nameRead = nil, nil, nil
	if !rt.HasSession {
		return
	}
	c.scope = rt.Ctx.Scope()
	mode, err := c.scope.Hold(contract.ChatMode, delta.JsonObjectOf("session", rt.Session, "conversation", float64(rt.Conversation)))
	if err != nil {
		return
	}
	c.mode = mode
	c.enabledRead = chordtui.WatchMember(c.scope, mode.Member("enabled"))
	c.waitingRead = chordtui.WatchMember(c.scope, mode.Member("waiting"))
	c.nameRead = chordtui.WatchMember(c.scope, mode.Member("name"))
	c.shownEnabled, c.switching, _ = chordtui.OptimisticLast(c.scope, func() bool {
		value, _ := c.enabledRead()
		enabled, _ := value.(bool)
		return enabled
	})
}

// Name is the agent's name in this conversation (what people mention).
func (c *Chip) Name() string {
	if c.nameRead != nil {
		if name, ok := c.value(c.nameRead).(string); ok && name != "" {
			return name
		}
	}
	return contract.DefaultAgentName
}

func (c *Chip) value(read func() (delta.JsonValue, bool)) any {
	value, _ := read()
	return value
}

func (c *Chip) waiting() int {
	if c.waitingRead == nil {
		return 0
	}
	waiting, _ := c.value(c.waitingRead).(float64)
	return int(waiting)
}

// Enabled is whether chat mode is on, as shown: the owner's switch while its call is in flight, else the state.
func (c *Chip) Enabled() bool { return c.shownEnabled() }

// owner is the owner's live reference (to switch the mode).
func (c *Chip) owner() *host.Reference {
	if c.mode != nil && p2runtime.StatusOf(c.mode) == host.RefStateLive {
		if variant, _, _ := c.mode.Variant(); variant == "owner" {
			return c.mode
		}
	}
	return nil
}

func (c *Chip) live() bool { return c.mode != nil && p2runtime.StatusOf(c.mode) == host.RefStateLive }

// Toggle flips the mode: the chip shows it at once; a refused call flips it back and says why.
func (c *Chip) Toggle() {
	rt := c.runtime
	owner := c.owner()
	if owner == nil || c.switching == nil {
		rt.Notify("Chat mode is not available here", runtime.NoticeWarning)
		return
	}
	enabled := !c.Enabled()
	name := c.Name()
	switching := c.switching
	go func() {
		err := switching(enabled, func() error {
			_, err := owner.Call("set", delta.JsonObjectOf("enabled", enabled), context.Background())
			return err
		})
		rt.Post(func() {
			switch {
			case err != nil:
				rt.Fail(err)
			case enabled:
				rt.Notify("Chat mode on: "+hint(name), runtime.NoticeInfo)
			default:
				rt.Notify("Chat mode off", runtime.NoticeInfo)
			}
		})
	}()
}

// syncReplies: replies belong to chat mode (the web's `setCanReply`).
func (c *Chip) syncReplies() {
	if chat := runtime.ChatSurfaceOf(c.runtime.Ctx); chat != nil {
		chat.SetCanReply(c.live() && c.Enabled(), c.Name())
	}
}

// Render is the chip.
func (c *Chip) Render(int) []string {
	current := c.runtime.Theme().Current()
	if !c.live() {
		return nil
	}
	label := "Chat"
	if c.Enabled() && c.waiting() > 0 {
		label = "Chat · " + strconv.Itoa(c.waiting())
	}
	if c.Enabled() {
		return []string{current.Bg("selectedBg", current.Fg("accent", " "+label+" ")) + " " + current.Fg("dim", hint(c.Name()))}
	}
	// Off: only the owner sees the switch.
	if c.owner() != nil {
		return []string{current.Fg("dim", "["+label+" off · /chat-mode]")}
	}
	return nil
}

// Invalidate has nothing cached.
func (c *Chip) Invalidate() {}

// Dispose stops following.
func (c *Chip) Dispose() {
	c.remove()
	if c.scope != nil {
		c.scope.Dispose()
	}
}

// ChipKey is the chip's slot.
const ChipKey = "composer:chat-mode"

func chipOf(ctx runtime.TuiContext) *Chip {
	chip, _ := ctx.Surface(ChipKey).(*Chip)
	return chip
}

// reply is `/reply`: the message the next send answers (a pick list, newest first); again with a target set, cancels.
func reply(ctx runtime.TuiContext) {
	chat := runtime.ChatSurfaceOf(ctx)
	if chat == nil {
		return
	}
	if chat.ReplyTo() != nil {
		chat.SetReplyTo(nil)
		ctx.Notify("Reply cancelled", runtime.NoticeInfo)
		return
	}
	targets := chat.ReplyTargets()
	var handle runtime.TuiOverlayHandle
	handle = ctx.Overlay("reply", components.NewListOverlay(runtime.Selectors(ctx), components.ListOverlayOptions{
		Title: func() string { return "Reply to" },
		Rows: func() []components.ListRow {
			rows := make([]components.ListRow, len(targets))
			for i, target := range targets {
				rows[i] = components.ListRow{Key: strconv.Itoa(target.EntryID), Label: target.Name + ": " + target.Excerpt}
			}
			return rows
		},
		OnSelect: func(row components.ListRow) {
			handle.Close()
			for _, target := range targets {
				if strconv.Itoa(target.EntryID) == row.Key {
					chat.SetReplyTo(&runtime.ReplyTo{EntryID: target.EntryID, Name: target.Name})
					return
				}
			}
		},
		OnClose: func() { handle.Close() },
	}), pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"})
}

// Commands are chat mode's commands.
func Commands() p2runtime.Keyed[runtime.TuiCommand] {
	return p2runtime.Keyed[runtime.TuiCommand]{
		p2runtime.Entry("p2.chatMode.toggle", runtime.TuiCommand{
			Title: "Toggle chat mode", Category: "Chat", When: "chat && owner && !overlay",
			Slash: &runtime.TuiCommandSlash{Name: "chat-mode"},
			Run: func(ctx runtime.TuiContext, _ string) error {
				if chip := chipOf(ctx); chip != nil {
					chip.Toggle()
				} else {
					ctx.Notify("Chat mode is not available here", runtime.NoticeWarning)
				}
				return nil
			},
		}),
		p2runtime.Entry("p2.chatMode.reply", runtime.TuiCommand{
			Title: "Reply to a message (chat mode)", Category: "Chat", When: "chat && participant && canReply && !overlay",
			Slash: &runtime.TuiCommandSlash{Name: "reply"},
			Run:   func(ctx runtime.TuiContext, _ string) error { reply(ctx); return nil },
		}),
	}
}

func replyOf(value any) *chatcontract.EntryReply {
	if !contract.IsReplyTo(value) {
		return nil
	}
	return &chatcontract.EntryReply{
		EntryID: p2tui.JNum(p2tui.JField(value, "entryId")), Name: p2tui.JStr(p2tui.JField(p2tui.JField(value, "author"), "name")),
		Excerpt: p2tui.JStr(p2tui.JField(value, "excerpt")),
	}
}

// Displays are how chat entries add to the transcript's display of people's messages: a reply's quoted line and a mention's note.
var Displays = p2runtime.Keyed[func(data any) *chatcontract.EntryDisplay]{
	p2runtime.Entry(contract.ChatEntry, func(data any) *chatcontract.EntryDisplay {
		if !contract.IsChatMessage(data) {
			return nil
		}
		quote := replyOf(p2tui.JField(data, "replyTo"))
		if quote == nil {
			return nil
		}
		return &chatcontract.EntryDisplay{ReplyTo: quote}
	}),
	p2runtime.Entry(contract.MentionEntry, func(data any) *chatcontract.EntryDisplay {
		if !contract.IsMention(data) {
			return nil
		}
		display := &chatcontract.EntryDisplay{ReplyTo: replyOf(p2tui.JField(data, "replyTo"))}
		switch included := len(p2tui.JList(p2tui.JField(data, "included"))); {
		case included == 1:
			display.Note = "with 1 chat message"
		case included > 1:
			display.Note = fmt.Sprintf("with %d chat messages", included)
		}
		return display
	}),
}

// Facet is chat mode's TUI facet.
var Facet = p2runtime.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
	if err := chatcontract.TranscriptEntries.Contribute(self, Displays...); err != nil {
		return err
	}
	if err := runtime.TuiSlots.Contribute(self, p2runtime.Entry(ChipKey, &runtime.TuiSlot{Factory: func(rt *runtime.TuiRuntime) runtime.TuiSurface { return NewChip(rt) }})); err != nil {
		return err
	}
	return runtime.TuiCommands.Contribute(self, Commands()...)
})
