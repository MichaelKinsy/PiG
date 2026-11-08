package runtime

import "github.com/MichaelKinsy/PiG/schema"

// Ports apps/p2/src/runtime/entries.ts: inputs that are not a person's: a peer conversation's message, a main agent's
// task for its subagent and a subagent's message. Their entry kinds and data are read across plugins (attribution,
// search) and by both UIs (transcript rows).

// PeerMessageEntry is a peer session's message (the conversations tool), as its own entry kind: the sending session
// and what it wrote. Readers use this data; the model reads the server-written `<sender kind="session" …/>` block in
// front of the text.
const PeerMessageEntry = "p2.peer-message"

// PeerMessageSchema is the data of a peer message. `origin` is the sending turn's (`originOf`), so the recipient's turn
// may act for the owner too.
var PeerMessageSchema = schema.Obj(schema.Properties{
	schema.Prop("from", schema.Obj(schema.Properties{schema.Prop("id", schema.Str()), schema.Prop("title", schema.Str())}, schema.Open())),
	schema.Prop("text", schema.Str()),
	schema.Opt("origin", Literal("owner")),
}, schema.Open())

var isPeerMessage = check(PeerMessageSchema)

// IsPeerMessage reports whether an entry of kind kind with data records a peer message (`peerMessage(kind, data) !==
// undefined`).
func IsPeerMessage(kind string, data any) bool {
	return kind == PeerMessageEntry && isPeerMessage(data)
}

// ConversationOrigin is who started a turn, when it may act for the owner (`originOf`): the owner, directly or
// through what an agent of such a turn wrote. Agent-written entries record it as `origin` in their data.
type ConversationOrigin string

// OriginOwner is the only origin: `"owner"`.
const OriginOwner ConversationOrigin = "owner"

// SubagentTaskEntry is an agent's task for another agent: a main agent's message to its subagent (`subagent
// spawn`/`send`) or a script's prompt to its one-shot agent, as its own entry kind, with `{ origin: "owner" }` when the
// owner started the writing turn. The receiving turn inherits it (`originOf`), so the owner-only tools it was given
// work for it.
const SubagentTaskEntry = "p2.subagent-task"

// AgentMessageEntry is a message from a subagent, as its own entry kind: its end-of-turn report to the main agent
// (`answered`, `failed`), or what it sent with the `team` tool (`question`, `note`) to the main agent or another
// subagent. Readers use this data; the model reads a server-written `<sender kind="subagent" name="…"/>` block, then
// `[<intent>] <text>`.
const AgentMessageEntry = "p2.agent-message"

// AgentMessageSchema is the data of a subagent message. `origin` is the origin of the subagent's turn that wrote it
// (`originOf`): the receiving turn may act for the owner too.
var AgentMessageSchema = schema.Obj(schema.Properties{
	schema.Prop("from", schema.Obj(schema.Properties{schema.Prop("name", schema.Str()), schema.Prop("conversationId", schema.Num())}, schema.Open())),
	schema.Prop("intent", schema.Union([]schema.Schema{Literal("answered"), Literal("failed"), Literal("question"), Literal("note")})),
	schema.Prop("text", schema.Str()),
	schema.Opt("origin", Literal("owner")),
}, schema.Open())

var isAgentMessage = check(AgentMessageSchema)

// IsAgentMessage reports whether an entry of kind kind with data records a subagent message (`agentMessage(kind, data)
// !== undefined`).
func IsAgentMessage(kind string, data any) bool {
	return kind == AgentMessageEntry && isAgentMessage(data)
}
