package tuitest

// The conversation fixtures of the chat tests (test/p2/tui-fixture.ts `SESSION`, `sessionsStub`, and the Chat and Drafts stubs the tests build by hand): entries and
// views as the server's JSON, a Sessions stub with one row, and a participant `Chat` stub per conversation whose views the test drives.

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	draftscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/drafts/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Session is the fixture's one session (`SESSION`).
var Session = sessionscontract.Session{
	ID: "s", Cwd: "/work", Title: "Fixture", CreatedAt: 1, UpdatedAt: 1,
	Model: durable.ModelRef{Provider: "faux", ModelId: "faux-1"}, ThinkingLevel: "off", Main: 1,
}

// SessionsStub is a Sessions object with the rows, for a guest variant (`sessionsStub`).
func SessionsStub(rows ...sessionscontract.Session) p2test.Stub {
	return p2test.Stub{Contract: sessionscontract.Sessions, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		list := make([]any, len(rows))
		for i, row := range rows {
			list[i] = row.JSON()
		}
		return p2test.StubMembers{Variant: "guest", State: map[string]any{"sessions": self.Cell(list)}}
	}}
}

// DraftsStub is the caller's own restored drafts, none (participants only).
func DraftsStub() p2test.Stub {
	return p2test.Stub{Contract: draftscontract.Drafts, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		return p2test.StubMembers{Variant: "author", State: map[string]any{"drafts": self.Cell([]any{})}, Methods: map[string]p2test.StubMethod{
			"claim": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
				return delta.JsonObjectOf("texts", []any{}), nil
			},
		}}
	}}
}

// Usage is a response's usage in the fixtures.
var Usage = ai.Usage{Input: 12, Output: 3, TotalTokens: 15}

// Assistant is an assistant message.
func Assistant(stop ai.StopReason, timestamp int64, content ...ai.AssistantContentBlock) ai.AssistantMessage {
	return ai.AssistantMessage{Content: content, API: "openai-completions", Provider: "faux", Model: "faux-1", StopReason: stop, Timestamp: timestamp, Usage: Usage}
}

// Entry is a root conversation entry.
func Entry(id int, kind string, model ...ai.Message) durable.EntryRecord {
	return durable.EntryRecord{Id: durable.EntryId(id), ConversationId: durable.ROOT_CONVERSATION_ID, Kind: kind, Model: model}
}

// UserEntry is a person's message entry (`pi.user`).
func UserEntry(id int, text string) durable.EntryRecord {
	return Entry(id, "pi.user", ai.UserMessage{Content: ai.UserText(text), Timestamp: int64(id)})
}

// AnswerEntry is the agent's text answer.
func AnswerEntry(id int, text string, timestamp int64) durable.EntryRecord {
	return Entry(id, "pi.assistant", Assistant(ai.StopReasonStop, timestamp, ai.TextContent{Text: text}))
}

// InputEntry is a `p2.input` entry, as the server records what the owner sent.
func InputEntry(id int, text string) durable.EntryRecord {
	record := Entry(id, "p2.input", ai.UserMessage{Content: ai.UserText(text), Timestamp: int64(id)})
	record.Data = delta.JsonObjectOf("author", delta.JsonObjectOf("kind", "owner", "id", "owner", "name", "Owner"), "text", text, "attachments", []any{})
	return record
}

// DocsOf is view documents from pairs of a kind and its document (a struct or a JSON object).
func DocsOf(t testing.TB, pairs ...any) harness.ViewDocs {
	t.Helper()
	var docs harness.ViewDocs
	for i := 0; i < len(pairs); i += 2 {
		object, err := durable.ToJsonObject(pairs[i+1])
		if err != nil {
			t.Fatal(err)
		}
		docs = harness.ViewDocsOf(docs, pairs[i].(string), object)
	}
	return docs
}

// View is a conversation view as the server's JSON; before is the entry just before the window, 0 for none.
func View(t testing.TB, conversation int, entries []durable.EntryRecord, docs harness.ViewDocs, before int) delta.JsonValue {
	t.Helper()
	view := harness.ConversationView{Conversation: durable.ConversationRecord{Id: durable.ConversationId(conversation)}, Entries: entries, Docs: docs}
	if before > 0 {
		id := durable.EntryId(before)
		view.Before = &id
	}
	value, err := durable.ToJsonValue(view)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// SentMessage is a `Chat.send` call.
type SentMessage struct {
	Conversation int
	RequestID    string
	Text         string
}

// EntriesQuery is a `Chat.entries` call as the server received it.
type EntriesQuery struct {
	Min, Max *int
	Order    string
	Limit    int
}

// ChatStub is a participant `Chat` stub per conversation of session "s": every instance's view follows the test's, calls are recorded.
type ChatStub struct {
	Stub p2test.Stub

	mu      sync.Mutex
	views   map[int][]*p2test.Cell
	latest  map[int]delta.JsonValue
	sent    map[int][]SentMessage
	queries []EntriesQuery
	options ChatStubOptions
}

// ChatStubOptions are what a test brings to the stub.
type ChatStubOptions struct {
	// Variant is "participant" by default.
	Variant string
	// Views is a conversation's first view.
	Views func(conversation int) delta.JsonValue
	// Entries answers `Chat.entries`; nil is an empty page.
	Entries func(query EntriesQuery) (items []durable.EntryRecord, more bool)
	// Model is the `model` state, nil for none.
	Model any
	// Methods replace the stub's methods by name.
	Methods map[string]p2test.StubMethod
	// Members may change an instance's members (a live shell cell); conversation is its params'.
	Members func(conversation int, self p2test.StubSelf, members *p2test.StubMembers)
}

// NewChatStub is the stub.
func NewChatStub(t testing.TB, options ChatStubOptions) *ChatStub {
	c := &ChatStub{views: map[int][]*p2test.Cell{}, latest: map[int]delta.JsonValue{}, sent: map[int][]SentMessage{}, options: options}
	variant := options.Variant
	if variant == "" {
		variant = "participant"
	}
	c.Stub = p2test.Stub{Contract: chatcontract.Chat, Factory: func(params *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		conversation := int(params.Value("conversation").(float64))
		c.mu.Lock()
		latest, ok := c.latest[conversation]
		if !ok {
			latest = options.Views(conversation)
			c.latest[conversation] = latest
		}
		c.mu.Unlock()
		view := self.Cell(latest)
		c.mu.Lock()
		c.views[conversation] = append(c.views[conversation], view)
		c.mu.Unlock()
		none := func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }
		methods := map[string]p2test.StubMethod{
			"entries": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
				query := EntriesQuery{Limit: int(p2runtimeNum(args, "limit"))}
				if v, ok := optionalInt(args, "minEntryId"); ok {
					query.Min = &v
				}
				if v, ok := optionalInt(args, "maxEntryId"); ok {
					query.Max = &v
				}
				query.Order, _ = args.(*delta.JsonObject).Value("order").(string)
				c.mu.Lock()
				c.queries = append(c.queries, query)
				c.mu.Unlock()
				var items []durable.EntryRecord
				more := false
				if options.Entries != nil {
					items, more = options.Entries(query)
				}
				list := make([]any, len(items))
				for i, item := range items {
					value, err := durable.ToJsonValue(item)
					if err != nil {
						return nil, err
					}
					list[i] = value
				}
				return delta.JsonObjectOf("items", list, "more", more), nil
			},
			"send": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
				object := args.(*delta.JsonObject)
				c.mu.Lock()
				defer c.mu.Unlock()
				text, _ := object.Value("text").(string)
				id, _ := object.Value("requestId").(string)
				c.sent[conversation] = append(c.sent[conversation], SentMessage{Conversation: conversation, RequestID: id, Text: text})
				return delta.JsonObjectOf("submissionId", float64(len(c.sent[conversation]))), nil
			},
			"stop": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
				return delta.JsonObjectOf("restored", []any{}), nil
			},
			"dequeue": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
				return delta.JsonObjectOf("text", nil), nil
			},
			"configure": none,
			"compact": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
				return delta.JsonObjectOf("taskId", float64(1)), nil
			},
			"abortCompaction": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
				return delta.JsonObjectOf("aborted", float64(0)), nil
			},
			"reset": none,
			"dequeueOwn": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
				return delta.JsonObjectOf("restored", []any{}, "skipped", float64(0)), nil
			},
			"shell": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
				return delta.JsonObjectOf("shellId", "shell-1"), nil
			},
			"abortShell": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
				return delta.JsonObjectOf("aborted", false), nil
			},
		}
		for name, method := range options.Methods {
			methods[name] = method
		}
		if variant == "viewer" {
			// A viewer reads: it has no send, stop or the rest.
			methods = map[string]p2test.StubMethod{"entries": methods["entries"]}
		}
		members := p2test.StubMembers{
			Variant: variant,
			State:   map[string]any{"view": view, "stats": self.Cell(nil), "model": self.Cell(options.Model), "liveShell": self.Cell(nil)},
			Methods: methods,
		}
		if options.Members != nil {
			options.Members(conversation, self, &members)
		}
		return members
	}}
	return c
}

func p2runtimeNum(args delta.JsonValue, key string) float64 {
	number, _ := args.(*delta.JsonObject).Value(key).(float64)
	return number
}

func optionalInt(args delta.JsonValue, key string) (int, bool) {
	number, ok := args.(*delta.JsonObject).Value(key).(float64)
	return int(number), ok
}

// SetView replaces conversation's view in every live instance of the object (released ones are disposed) and in what a new instance starts with.
func (c *ChatStub) SetView(conversation int, view delta.JsonValue) {
	c.mu.Lock()
	c.latest[conversation] = view
	cells := append([]*p2test.Cell(nil), c.views[conversation]...)
	c.mu.Unlock()
	for _, cell := range cells {
		_ = cell.Replace(view)
	}
}

// Instances is how many objects of conversation were made.
func (c *ChatStub) Instances(conversation int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.views[conversation])
}

// Sent are the messages `Chat.send` took for conversation.
func (c *ChatStub) Sent(conversation int) []SentMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]SentMessage(nil), c.sent[conversation]...)
}

// Queries are the `Chat.entries` calls so far.
func (c *ChatStub) Queries() []EntriesQuery {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]EntriesQuery(nil), c.queries...)
}
