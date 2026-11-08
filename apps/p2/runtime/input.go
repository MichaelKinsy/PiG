package runtime

import (
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/src/runtime/input.ts: people's messages as Durable entry data. Every input a person sends is placed as
// its own entry kind (`p2.input`; chat-mode's `chat.mention`) whose data says who wrote what. Display-only entries
// (chat-mode's `chat.message`) carry the same data. The model message of an input starts with the sender block
// (sender.go), which nothing reads back: readers use the data.

// InputEntry is the entry kind of a person's ordinary input (`INPUT_ENTRY`).
const InputEntry = "p2.input"

// AuthorSchema is who wrote a message (kinds may add fields).
var AuthorSchema = schema.Obj(schema.Properties{
	schema.Prop("kind", schema.Union([]schema.Schema{Literal("owner"), Literal("guest")})),
	schema.Prop("id", schema.Str()),
	schema.Prop("name", schema.Str()),
}, schema.Open())

// MessageDataSchema is who wrote a message, what they typed, and their attachments (upload IDs with file names). Kinds
// may add fields.
var MessageDataSchema = schema.Obj(schema.Properties{
	schema.Prop("author", AuthorSchema),
	schema.Prop("text", schema.Str()),
	schema.Prop("attachments", schema.Arr(schema.Obj(schema.Properties{
		schema.Prop("id", schema.Str()),
		schema.Prop("name", schema.Str()),
	}, schema.Open()))),
}, schema.Open())

var isMessageData = check(MessageDataSchema)

// IsMessageData reports whether data is the message data a server wrote for an input or display entry
// (`messageData(data) !== undefined`). data is a decoded JSON value (objects as *delta.JsonObject).
func IsMessageData(data any) bool { return isMessageData(data) }
