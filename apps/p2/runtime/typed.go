package runtime

import "github.com/MichaelKinsy/PiG/chord/delta"

// Typed readers of the data the shared schemas describe: upstream's `peerMessage`, `agentMessage` and `messageData`
// return the checked data typed as `Static<Schema>`; Go returns the same fields as structs. Each reads decoded JSON
// (objects as *delta.JsonObject) and reports false when the schema check fails. The schemas are open objects: fields
// the struct does not name are ignored.

// PeerMessage is a peer session's message: the sending session and what it wrote. Origin is the sending turn's, so the
// recipient's turn may act for the owner too (nil: absent).
type PeerMessage struct {
	FromID    string
	FromTitle string
	Text      string
	Origin    *ConversationOrigin
}

// AgentIntent is what a subagent message says.
type AgentIntent string

// The intents of a subagent message.
const (
	IntentAnswered AgentIntent = "answered"
	IntentFailed   AgentIntent = "failed"
	IntentQuestion AgentIntent = "question"
	IntentNote     AgentIntent = "note"
)

// AgentMessage is a message from a subagent: its end-of-turn report to the main agent, or what it sent with the `team`
// tool. Origin is the origin of the subagent's turn that wrote it (nil: absent).
type AgentMessage struct {
	FromName           string
	FromConversationID float64
	Intent             AgentIntent
	Text               string
	Origin             *ConversationOrigin
}

// MessageAttachment is an attachment of a person's message: an upload ID with its file name.
type MessageAttachment struct {
	ID   string
	Name string
}

// MessageData is who wrote a message, what they typed, and their attachments.
type MessageData struct {
	Author      Author
	Text        string
	Attachments []MessageAttachment
}

func object(value any) *delta.JsonObject {
	object, _ := value.(*delta.JsonObject)
	return object
}

func text(object *delta.JsonObject, key string) string {
	value, _ := object.Value(key).(string)
	return value
}

func origin(object *delta.JsonObject) *ConversationOrigin {
	if object.Value("origin") == nil {
		return nil
	}
	owner := OriginOwner
	return &owner
}

// PeerMessageOf returns the peer message an entry of kind kind with data records, if it is one (`peerMessage`).
func PeerMessageOf(kind string, data any) (PeerMessage, bool) {
	if !IsPeerMessage(kind, data) {
		return PeerMessage{}, false
	}
	message, from := object(data), object(object(data).Value("from"))
	return PeerMessage{FromID: text(from, "id"), FromTitle: text(from, "title"), Text: text(message, "text"), Origin: origin(message)}, true
}

// AgentMessageOf returns the subagent message an entry of kind kind with data records, if it is one (`agentMessage`).
func AgentMessageOf(kind string, data any) (AgentMessage, bool) {
	if !IsAgentMessage(kind, data) {
		return AgentMessage{}, false
	}
	message, from := object(data), object(object(data).Value("from"))
	conversation, _ := from.Value("conversationId").(float64)
	return AgentMessage{
		FromName:           text(from, "name"),
		FromConversationID: conversation,
		Intent:             AgentIntent(text(message, "intent")),
		Text:               text(message, "text"),
		Origin:             origin(message),
	}, true
}

// MessageDataOf returns the message data a server wrote for an input or display entry, if data is one (`messageData`).
func MessageDataOf(data any) (MessageData, bool) {
	if !IsMessageData(data) {
		return MessageData{}, false
	}
	message, author := object(data), object(object(data).Value("author"))
	result := MessageData{
		Author: Author{Kind: AuthorKind(text(author, "kind")), ID: text(author, "id"), Name: text(author, "name")},
		Text:   text(message, "text"),
	}
	attachments, _ := message.Value("attachments").([]any)
	for _, item := range attachments {
		result.Attachments = append(result.Attachments, MessageAttachment{ID: text(object(item), "id"), Name: text(object(item), "name")})
	}
	return result, true
}

var isArtifactInfo = check(ArtifactInfoSchema)

// ArtifactInfoOf returns the artifact version data describes, if it is one (`isArtifactInfo` of the schema).
func ArtifactInfoOf(data any) (ArtifactInfo, bool) {
	if !isArtifactInfo(data) {
		return ArtifactInfo{}, false
	}
	info := object(data)
	version, _ := info.Value("version").(float64)
	return ArtifactInfo{Name: text(info, "name"), Title: text(info, "title"), Type: ArtifactType(text(info, "type")), Version: int(version)}, true
}
