// Package chatmode ports apps/p2/plugins/chat-mode: the people viewing a conversation talk to each other in its transcript,
// and the agent reads along only when someone mentions it. This file is digest.ts: what the agent receives when someone
// mentions it, and what a reply quotes (pure functions, the worker and the tests). Everything here reads entry data; nothing
// parses text written for the model.
package chatmode

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/chat-mode/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// MaxDigestMessages is at most this many chat messages go into one mention; older ones are counted, not quoted.
const MaxDigestMessages = 100

// MaxDigestFiles is at most this many of their attachments are listed (the newest).
const MaxDigestFiles = 20

// MaxDigestImages is inline at most this many images per mention, the newest; the rest are listed by path.
const MaxDigestImages = 4

// ExcerptLength is at most this many characters of a replied-to message are quoted, whitespace collapsed.
const ExcerptLength = 60

// ReplyAuthor is who a quote's message was written by.
type ReplyAuthor struct{ Kind, ID, Name string }

// ReplyTo is the message a reply answers: an entry of the same conversation, with its author and the start of its text.
type ReplyTo struct {
	EntryID float64
	Author  ReplyAuthor
	Excerpt string
}

func (r ReplyTo) json() *delta.JsonObject {
	return delta.JsonObjectOf("entryId", r.EntryID, "author", delta.JsonObjectOf("kind", r.Author.Kind, "id", r.Author.ID, "name", r.Author.Name), "excerpt", r.Excerpt)
}

func replyToOf(value any) *ReplyTo {
	object, _ := value.(*delta.JsonObject)
	if object == nil || !contract.IsReplyTo(object) {
		return nil
	}
	author, _ := object.Value("author").(*delta.JsonObject)
	id, _ := object.Value("entryId").(float64)
	return &ReplyTo{EntryID: id, Author: ReplyAuthor{Kind: str(author, "kind"), ID: str(author, "id"), Name: str(author, "name")}, Excerpt: str(object, "excerpt")}
}

func str(object *delta.JsonObject, key string) string {
	value, _ := object.Value(key).(string)
	return value
}

// File is an attachment of a chat message: an upload ID with its file name.
type File struct{ ID, Name string }

// ChatMessage is a chat entry's data: a person's message, perhaps answering another.
type ChatMessage struct {
	Author      p2runtime.Author
	Text        string
	Attachments []File
	ReplyTo     *ReplyTo
}

func authorOf(value any) p2runtime.Author {
	object, _ := value.(*delta.JsonObject)
	return p2runtime.Author{Kind: p2runtime.AuthorKind(str(object, "kind")), ID: str(object, "id"), Name: str(object, "name")}
}

func authorJSON(author p2runtime.Author) *delta.JsonObject {
	return delta.JsonObjectOf("kind", string(author.Kind), "id", author.ID, "name", author.Name)
}

func filesOf(value any) []File {
	list, _ := value.([]any)
	files := make([]File, 0, len(list))
	for _, each := range list {
		object, _ := each.(*delta.JsonObject)
		files = append(files, File{ID: str(object, "id"), Name: str(object, "name")})
	}
	return files
}

// chatMessage is an entry's chat message data, if it is one (`chatMessage`).
func chatMessage(data any) (ChatMessage, bool) {
	object, _ := data.(*delta.JsonObject)
	if object == nil || !contract.IsChatMessage(object) {
		return ChatMessage{}, false
	}
	return ChatMessage{Author: authorOf(object.Value("author")), Text: str(object, "text"), Attachments: filesOf(object.Value("attachments")), ReplyTo: replyToOf(object.Value("replyTo"))}, true
}

// includedOf is the chat entries a mention's digest passed on, if data is a mention's (`mention(data)?.included`).
func includedOf(data any) ([]float64, bool) {
	object, _ := data.(*delta.JsonObject)
	if object == nil || !contract.IsMention(object) {
		return nil, false
	}
	list, _ := object.Value("included").([]any)
	included := make([]float64, 0, len(list))
	for _, each := range list {
		id, _ := each.(float64)
		included = append(included, id)
	}
	return included, true
}

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// collapse is text with each run of white space one space, trimmed.
func collapse(text string) string { return strings.Join(strings.FieldsFunc(text, isJSSpace), " ") }

// speaker is how a digest names a message's author: `Ann (guest, replying to Bob: "should we split…")`.
func speaker(author p2runtime.Author, replyTo *ReplyTo) string {
	var notes []string
	if author.Kind == p2runtime.AuthorGuest {
		notes = append(notes, "guest")
	}
	if replyTo != nil {
		notes = append(notes, fmt.Sprintf("replying to %s: \"%s\"", replyTo.Author.Name, replyTo.Excerpt))
	}
	if len(notes) == 0 {
		return author.Name
	}
	return author.Name + " (" + strings.Join(notes, ", ") + ")"
}

// Excerpt is the start of a message: whitespace collapsed, cut at ExcerptLength characters with an ellipsis.
func Excerpt(text string) string {
	flat := collapse(text)
	if p2runtime.UTF16Length(flat) > ExcerptLength {
		return strings.TrimRightFunc(p2runtime.UTF16Slice(flat, ExcerptLength-1), isJSSpace) + "…"
	}
	return flat
}

var agent = ReplyAuthor{Kind: "agent", ID: "agent", Name: "Pi"}

// QuoteOf is what a reply quotes from an entry, derived from the entry's own data: a chat message, a person's input (for a
// mention, what they wrote, not the digest), or one of Pi's answers. Nil for anything else.
func QuoteOf(entry durable.EntryRecord) *ReplyTo {
	quote := func(author ReplyAuthor, text string) *ReplyTo {
		result := ReplyTo{EntryID: float64(entry.Id), Author: author, Excerpt: Excerpt(text)}
		if !contract.IsReplyTo(result.json()) {
			return nil
		}
		return &result
	}
	if len(entry.Model) > 0 {
		if assistant, ok := assistantMessage(entry.Model[0]); ok {
			var parts []string
			for _, block := range assistant.Content {
				if text, isText := block.(ai.TextContent); isText {
					parts = append(parts, text.Text)
				}
			}
			return quote(agent, strings.Join(parts, " "))
		}
	}
	if person, ok := p2runtime.MessageDataOf(entry.Data); ok {
		return quote(ReplyAuthor{Kind: string(person.Author.Kind), ID: person.Author.ID, Name: person.Author.Name}, person.Text)
	}
	return nil
}

// ChatEntryRef is a chat entry: its ID (what a mention records) and its message.
type ChatEntryRef struct {
	ID      float64
	Message ChatMessage
}

// boundaryOf is where a scan may stop: the lowest chat entry the latest placed mention passed on. Its digest took every chat
// message not yet passed on, so older ones were passed on before, or dropped by the digest's cap. A mention that passed on
// nothing bounds nothing: chat placed while it waited lies below it.
func boundaryOf(entries []durable.EntryRecord) (float64, bool) {
	for _, entry := range entries {
		if entry.Kind != contract.MentionEntry {
			continue
		}
		if included, ok := includedOf(entry.Data); ok && len(included) > 0 {
			return slices.Min(included), true
		}
	}
	return 0, false
}

// ReadEnough is whether a newest-first read has gone far enough for Unmentioned (the reader may stop).
func ReadEnough(entries []durable.EntryRecord) bool {
	boundary, ok := boundaryOf(entries)
	if !ok {
		return false
	}
	var oldest float64
	if len(entries) > 0 {
		oldest = float64(entries[len(entries)-1].Id)
	}
	return oldest <= boundary
}

// Unmentioned are the chat messages no mention has passed on to Pi yet, oldest first. entries are newest first, as a
// backwards scan reads them; inbox holds mentions still queued. Each mention records the chat entries its digest included, so
// a chat message placed while a mention waited in the queue is not covered by it and goes with the next one. The scan stops at
// the lowest chat entry the latest mention passed on (see boundaryOf).
func Unmentioned(entries []durable.EntryRecord, inbox *harness.InboxState) []ChatEntryRef {
	covered := map[float64]bool{}
	if inbox != nil {
		for _, item := range inbox.Items {
			if item.Mode == harness.InboxWrite || item.Entry == nil || item.Entry.Value("kind") != contract.MentionEntry {
				continue
			}
			if included, ok := includedOf(item.Entry.Value("data")); ok {
				for _, id := range included {
					covered[id] = true
				}
			}
		}
	}
	boundary, ok := boundaryOf(entries)
	if !ok {
		boundary = math.Inf(-1)
	}
	var found []ChatEntryRef
	for _, entry := range entries {
		if float64(entry.Id) < boundary {
			break
		}
		if entry.Kind == contract.MentionEntry {
			if included, ok := includedOf(entry.Data); ok {
				for _, id := range included {
					covered[id] = true
				}
			}
		}
		if entry.Kind == contract.ChatEntry {
			if message, ok := chatMessage(entry.Data); ok && !covered[float64(entry.Id)] {
				found = append(found, ChatEntryRef{ID: float64(entry.Id), Message: message})
			}
		}
	}
	slices.Reverse(found)
	return found
}

// Digest is the mention's text for the model: the chat not passed on yet, with its authors, then who asks and what.
func Digest(chat []ChatEntryRef, author p2runtime.Author, text string, answering *ReplyTo) string {
	quoted := chat[max(0, len(chat)-MaxDigestMessages):]
	omitted := len(chat) - len(quoted)
	var lines []string
	for _, entry := range quoted {
		message := entry.Message
		files := ""
		if len(message.Attachments) > 0 {
			names := make([]string, len(message.Attachments))
			for i, file := range message.Attachments {
				names[i] = file.Name
			}
			files = " [attached: " + strings.Join(names, ", ") + "]"
		}
		lines = append(lines, speaker(message.Author, message.ReplyTo)+": "+message.Text+files)
	}
	var out []string
	if len(chat) > 0 {
		out = append(out, "<chat>", "Since you were last mentioned, people in this session talked to each other. They did not address you:")
		if omitted > 0 {
			out = append(out, fmt.Sprintf("(%d earlier messages omitted)", omitted))
		}
		out = append(out, lines...)
		out = append(out, "</chat>", "")
	}
	return strings.Join(append(out, speaker(author, answering)+" mentioned you:", text), "\n")
}

// DigestFiles are the upload IDs to resolve for a mention: the chat's newest attachments, then the asker's own.
func DigestFiles(chat []ChatEntryRef, own []string) []string {
	var ids []string
	for _, entry := range chat {
		for _, file := range entry.Message.Attachments {
			ids = append(ids, file.ID)
		}
	}
	ids = ids[max(0, len(ids)-MaxDigestFiles):]
	out := slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return slices.Contains(own, id) })
	return append(out, own...)
}

// Mention is what a mention is made of: who asked and what, the chat passed on, the quote it answers, the asker's own
// attachments and every resolved file (the chat's and the asker's, as the files store returned them).
type Mention struct {
	Author      p2runtime.Author
	Text        string
	Chat        []ChatEntryRef
	ReplyTo     *ReplyTo
	Attachments []File
	Files       []any
	RequestID   string
	WhenBusy    string
}

// Submit is a mention as the chat plugin submits it (`Conversation.submit`'s arguments).
type Submit struct {
	Text      string
	RequestID string
	WhenBusy  string
	Author    p2runtime.Author
	Files     []any
	MaxImages int
	// Entry is the kind and data the input is placed as.
	EntryKind string
	EntryData *delta.JsonObject
}

// JSON is the submission as `Conversation.submit` takes it.
func (s Submit) JSON() *delta.JsonObject {
	submit := delta.JsonObjectOf("text", s.Text, "requestId", s.RequestID)
	if s.WhenBusy != "" {
		submit.Set("whenBusy", s.WhenBusy)
	}
	submit.Set("author", authorJSON(s.Author))
	submit.Set("files", slices.Clone(s.Files))
	submit.Set("maxImages", float64(s.MaxImages))
	submit.Set("entry", delta.JsonObjectOf("kind", s.EntryKind, "data", s.EntryData))
	return submit
}

// ComposeMention is a mention as the chat plugin submits it: the one place that decides what a mention is. It is placed as a
// `chat.mention` entry whose data is what the person wrote (author, text, attachments, reply) and the chat entries passed on;
// the model reads the digest, which the chat plugin starts with the author's sender block.
func ComposeMention(input Mention) Submit {
	attachments := make([]any, len(input.Attachments))
	for i, file := range input.Attachments {
		attachments[i] = delta.JsonObjectOf("id", file.ID, "name", file.Name)
	}
	included := make([]any, 0, min(len(input.Chat), MaxDigestMessages))
	for _, entry := range input.Chat[max(0, len(input.Chat)-MaxDigestMessages):] {
		included = append(included, entry.ID)
	}
	data := delta.JsonObjectOf("author", authorJSON(input.Author), "text", input.Text, "attachments", attachments)
	if input.ReplyTo != nil {
		data.Set("replyTo", input.ReplyTo.json())
	}
	data.Set("included", included)
	return Submit{
		Text: Digest(input.Chat, input.Author, input.Text, input.ReplyTo), RequestID: input.RequestID, WhenBusy: input.WhenBusy,
		Author: input.Author, Files: input.Files, MaxImages: MaxDigestImages, EntryKind: contract.MentionEntry, EntryData: data,
	}
}

func assistantMessage(message ai.Message) (ai.AssistantMessage, bool) {
	switch typed := message.(type) {
	case ai.AssistantMessage:
		return typed, true
	case *ai.AssistantMessage:
		if typed != nil {
			return *typed, true
		}
	}
	return ai.AssistantMessage{}, false
}
