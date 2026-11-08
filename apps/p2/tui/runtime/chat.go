package runtime

// The chat surface as the plugins that extend it see it (p2-tui-port.md P11): chat mode's reply switch, the sent files' list.
//
// pi: apps/p2/src/runtime/tui-chat.ts

import (
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// ReplyTarget is a message a reply can answer.
type ReplyTarget struct {
	EntryID int
	Name    string
	Excerpt string
}

// ReplyTo is the message the next send answers.
type ReplyTo struct {
	EntryID int
	Name    string
}

// ChatSurfaceAPI is what the chat plugin's surface offers the others; ChatSurfaceMarker brands it (Pi's CHAT_SURFACE).
type ChatSurfaceAPI interface {
	pitui.Component
	ChatSurfaceMarker()
	// ReplyTo is the message the next send answers, nil for none.
	ReplyTo() *ReplyTo
	// SetCanReply switches replies on or off (chat mode); mentionName is the agent's name to mention ("" selects "Pi").
	SetCanReply(enabled bool, mentionName string)
	// SetReplyTo makes the next send answer this entry (chat mode); nil cancels.
	SetReplyTo(target *ReplyTo)
	// ReplyTargets are the messages a reply can answer: people's and Pi's committed messages, newest first.
	ReplyTargets() []ReplyTarget
	// ToolViews are the viewed conversation's finished calls of the tool name.
	ToolViews(name string) []transcript.ToolView
}

// ChatSurfaceOf is the mounted chat surface, nil when the chat plugin is not loaded or shows none.
func ChatSurfaceOf(ctx TuiContext) ChatSurfaceAPI {
	surface, _ := ctx.Surface("chat").(ChatSurfaceAPI)
	return surface
}
