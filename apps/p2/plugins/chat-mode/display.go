package chatmode

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/chat-mode/contract"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports apps/p2/plugins/chat-mode/display.ts: how chat entries add to the transcript's display of people's messages (the
// shared `TranscriptEntries` registry): a reply's quoted line and a mention's note. Pure; the web and the TUI facets use it.

// quoted is a reply's quoted line, from the quote the worker stored with it.
func quoted(replyTo *ReplyTo) *chatcontract.EntryReply {
	if replyTo == nil {
		return nil
	}
	return &chatcontract.EntryReply{EntryID: replyTo.EntryID, Name: replyTo.Author.Name, Excerpt: replyTo.Excerpt}
}

func plural(count int) string {
	if count == 1 {
		return "1 chat message"
	}
	return fmt.Sprintf("%d chat messages", count)
}

// ChatEntryDisplays are the displays by entry kind: nil for data that is not the kind's.
var ChatEntryDisplays = map[string]func(data any) *chatcontract.EntryDisplay{
	contract.ChatEntry: func(data any) *chatcontract.EntryDisplay {
		message, ok := chatMessage(data)
		if !ok || message.ReplyTo == nil {
			return nil
		}
		return &chatcontract.EntryDisplay{ReplyTo: quoted(message.ReplyTo)}
	},
	contract.MentionEntry: func(data any) *chatcontract.EntryDisplay {
		included, ok := includedOf(data)
		if !ok {
			return nil
		}
		display := &chatcontract.EntryDisplay{}
		object, _ := data.(*delta.JsonObject)
		display.ReplyTo = quoted(replyToOf(object.Value("replyTo")))
		if len(included) > 0 {
			display.Note = "with " + plural(len(included))
		}
		return display
	},
}
