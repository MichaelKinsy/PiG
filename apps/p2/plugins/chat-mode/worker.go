package chatmode

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/chat-mode/contract"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	filescontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/files/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/chat-mode/worker.ts: chat mode (worker): the per-conversation switch and the agent's name (what people
// mention, `@pi` by default), kept in a Durable document; the router that turns messages into chat entries or mentions; and
// the view each browser holds.

// ChatModeState is the switch and the agent's name.
type ChatModeState struct {
	Enabled bool   `json:"enabled"`
	Name    string `json:"name"`
}

// ChatModeDoc is the switch and the agent's name, stored per agent (`AgentConversation`: the main agent's on conversation 1,
// whichever `/tree` branch it works in; a new subagent starts with chat mode off and the default name).
var ChatModeDoc = durable.DefineDoc(durable.DocDefinition[ChatModeState]{
	CommonDocDefinition: durable.CommonDocDefinition[ChatModeState]{
		Kind:    "chat-mode.settings",
		Version: 2,
		Initial: func() ChatModeState { return ChatModeState{Name: contract.DefaultAgentName} },
		// Version 1 had the switch only.
		Migrate: func(value durable.JsonObject, _ int) (ChatModeState, error) {
			return ChatModeState{Enabled: value.Value("enabled") == true, Name: contract.DefaultAgentName}, nil
		},
		CheckpointWhen: func(ChatModeState, []durable.Op, durable.CheckpointInfo) bool { return true },
	},
	DocumentSemantics: durable.NewLatestConversationSemantics(durable.ForkInitial).DocumentSemantics,
})

// waitingDoc is how many chat messages a mention would pass on now: computed from the transcript, which the view only windows.
var waitingDoc = func() *documents.StateDefinition {
	definition, err := documents.DefineState("p2.chat-mode-setting.waiting", documents.StateOptions{
		Schema:  p2runtime.Contract(schema.Obj(schema.Properties{schema.Prop("count", schema.Num())})),
		Version: 1,
		Initial: func() *delta.JsonObject { return delta.JsonObjectOf("count", float64(0)) },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

var linger = 30 * time.Second

func object(value any) *delta.JsonObject {
	object, _ := value.(*delta.JsonObject)
	return object
}

func invalidArgs(message string) error {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeInvalid, Reason: data.ReasonArgs, Message: message}, nil)
}

// RecentEntries are a conversation's newest entries, newest first, until Unmentioned has what it needs (at most limit).
func RecentEntries(ctx context.Context, d harness.Harness, id durable.ConversationId, limit int) ([]durable.EntryRecord, error) {
	conversation, err := d.Conversation(ctx, id)
	if err != nil {
		return nil, err
	}
	var entries []durable.EntryRecord
	before := float64(1<<53 - 1)
	for conversation != nil && len(entries) < limit && !ReadEnough(entries) {
		maxID := durable.EntryId(before - 1)
		page, err := conversation.Entries(ctx, durable.EntryQuery{ConversationId: id, MaxEntryId: &maxID}, 256, nil)
		if err != nil {
			return nil, err
		}
		for _, entry := range page.Items {
			entries = append(entries, entry)
			before = float64(entry.Id)
		}
		if page.Next == nil {
			break
		}
	}
	return entries, nil
}

// ReplyTarget is what a reply quotes, read from the entry itself. Only an entry of this conversation qualifies: Durable lists
// a conversation's own entries, so an ID from another conversation is not found.
func ReplyTarget(ctx context.Context, d harness.Harness, id durable.ConversationId, entryID int) (*ReplyTo, error) {
	conversation, err := d.Conversation(ctx, id)
	if err != nil {
		return nil, err
	}
	var quote *ReplyTo
	if conversation != nil {
		only := durable.EntryId(entryID)
		page, err := conversation.Entries(ctx, durable.EntryQuery{ConversationId: id, MinEntryId: &only, MaxEntryId: &only}, 1, nil)
		if err != nil {
			return nil, err
		}
		if len(page.Items) > 0 && page.Items[0].Id == only {
			quote = QuoteOf(page.Items[0])
		}
	}
	if quote == nil {
		return nil, invalidArgs("That message cannot be replied to")
	}
	return quote, nil
}

// serveSetting serves ChatModeSetting: one conversation's switch and agent name, followed from its Durable document.
func serveSetting(params *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
	conversationValue, _ := params.Value("conversation").(float64)
	id := durable.ConversationId(conversationValue)
	harnessOf := p2runtime.Cached(p2runtime.HarnessOf(self))
	durableOf := func() (harness.Harness, error) {
		parts, err := harnessOf()
		if err != nil {
			return nil, err
		}
		return parts.Durable()
	}
	// The conversation's view (shared with its viewers): its entries and queue change what waits for a mention.
	chat := self.Get(chatcontract.Conversation, delta.JsonObjectOf("conversation", conversationValue))
	// holder is where the agent's chat mode is (the main agent's on conversation 1); found when the document is followed.
	var holder atomic.Int64
	holder.Store(int64(id))
	settings := self.Follow(p2runtime.FollowDurable(func(ctx context.Context, observer durable.Observer[ChatModeState]) (durable.Observation[ChatModeState], error) {
		d, err := durableOf()
		if err != nil {
			return nil, err
		}
		where, err := p2runtime.AgentConversation(ctx, d, id)
		if err != nil {
			return nil, err
		}
		holder.Store(int64(where))
		observed, err := durable.ObserveDoc(ctx, d, ChatModeDoc, observer, where)
		if err != nil || observed != nil {
			return observed, err
		}
		// Nobody switched it yet: the document is absent, and an absent address cannot be followed. Create it as it reads
		// (off, the default name).
		if _, err := d.Commit(ctx, func(tx durable.Tx) (any, error) {
			_, err := durable.TxDoc(tx, ChatModeDoc, where)
			return nil, err
		}); err != nil {
			return nil, err
		}
		observed, err = durable.ObserveDoc(ctx, d, ChatModeDoc, observer, where)
		if err == nil && observed == nil {
			err = fmt.Errorf("chat mode of conversation %d: its document is absent", id)
		}
		return observed, err
	}))
	waiting := self.State(waitingDoc).(documents.SingletonHandle)
	counted := self.Derive([]any{settings, chat.Member("view")}, func(values []any) (delta.JsonValue, error) {
		doc, view := object(values[0]), object(values[1])
		newest := float64(0)
		entries, _ := view.Value("entries").([]any)
		if len(entries) > 0 {
			newest, _ = object(entries[len(entries)-1]).Value("id").(float64)
		}
		var inbox delta.JsonValue
		if docs := object(view.Value("docs")); docs != nil {
			inbox = docs.Value("pi.inbox")
		}
		return delta.JsonObjectOf("enabled", doc.Value("enabled") == true, "newest", newest, "inbox", inbox), nil
	})
	// The view holds only a window of the entries: the router's backwards scan counts what waits. Only this routine writes the
	// document: what it wrote last is what the document holds.
	written := 0
	self.On(counted, func(call chord.WatchCall[delta.JsonValue], ctx host.Ctx) *host.Handled {
		// First use in this section: later ones, off the loop, take the kept natives.
		_, _ = harnessOf()
		value := object(call.Value)
		return host.AwaitingCtx(ctx, func() error {
			count := 0
			if value.Value("enabled") == true {
				d, err := durableOf()
				if err != nil {
					return err
				}
				var inbox *harness.InboxState
				if raw := value.Value("inbox"); raw != nil {
					state, err := durable.FromJsonValue[harness.InboxState](raw)
					if err != nil {
						return err
					}
					inbox = &state
				}
				entries, err := RecentEntries(ctx, d, id, 2000)
				if err != nil {
					return err
				}
				count = len(Unmentioned(entries, inbox))
			}
			if written != count {
				if _, err := waiting.Change(ctx, func(draft *delta.Object) (any, error) { return nil, draft.Set("count", float64(count)) }); err != nil {
					return err
				}
				written = count
			}
			return nil
		})
	})
	commit := func(ctx context.Context, d harness.Harness, change func(doc *delta.Object) error) error {
		found, err := d.Conversation(ctx, id)
		if err != nil {
			return err
		}
		if found == nil {
			return invalidArgs(fmt.Sprintf("Unknown conversation %d", id))
		}
		_, err = d.Commit(ctx, func(tx durable.Tx) (any, error) {
			draft, err := durable.TxDoc(tx, ChatModeDoc, durable.ConversationId(holder.Load()))
			if err != nil {
				return nil, err
			}
			return nil, change(draft)
		})
		return err
	}
	// change runs a switch or rename after leaving the handler's section (Durable's publications need the loop); the Harness is
	// read before, inside it.
	change := func(field string, valueOf func(args *delta.JsonObject) any) contracts.MethodFunction {
		return func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			d, err := durableOf()
			if err != nil {
				return nil, err
			}
			chordctx.Suspend(ctx)
			value := valueOf(object(args))
			return nil, commit(ctx, d, func(doc *delta.Object) error { return doc.Set(field, value) })
		}
	}
	return contracts.Implementation{Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{
			"enabled": self.Derive([]any{settings}, func(values []any) (delta.JsonValue, error) { return object(values[0]).Value("enabled"), nil }),
			"name":    self.Derive([]any{settings}, func(values []any) (delta.JsonValue, error) { return object(values[0]).Value("name"), nil }),
			"waiting": self.Derive([]any{waiting}, func(values []any) (delta.JsonValue, error) { return object(values[0]).Value("count"), nil }),
		},
		Methods: map[string]contracts.MethodFunction{
			"set":    change("enabled", func(args *delta.JsonObject) any { return args.Value("enabled") == true }),
			"rename": change("name", func(args *delta.JsonObject) any { return args.Value("name") }),
		},
	}}, nil
}

// resolve validates upload IDs of this session (copying them to a remote environment, like any input's).
func resolve(ctx host.Ctx, ids []string, conversation int) ([]any, error) {
	files := []any{}
	if len(ids) == 0 {
		return files, nil
	}
	attachments, err := ctx.Get(filescontract.Attachments, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		return nil, err
	}
	for start := 0; start < len(ids); start += filescontract.MaxAttachments {
		chunk := ids[start:min(start+filescontract.MaxAttachments, len(ids))]
		list := make([]any, len(chunk))
		for i, id := range chunk {
			list[i] = id
		}
		resolved, err := attachments.Call("resolve", delta.JsonObjectOf("ids", list, "conversation", float64(conversation)), ctx)
		if err != nil {
			return nil, err
		}
		more, _ := resolved.([]any)
		files = append(files, more...)
	}
	return files, nil
}

// route is the chat-mode send router: chat entries while chat mode is on, a mention when someone mentions the agent.
func route(harnessOf func() (*p2runtime.HarnessParts, error)) chatcontract.SendRouter {
	return func(ctx host.Ctx, input chatcontract.RoutedSend) (*float64, error) {
		// `Chat.send` runs the routers in its handler's section: the Durable reads below need the loop, so the section is parked
		// first (the natives are read before, in it).
		parts, err := harnessOf()
		if err != nil {
			return nil, err
		}
		d, err := parts.Durable()
		if err != nil {
			return nil, err
		}
		chordctx.Suspend(ctx)
		id := durable.ConversationId(input.Conversation)
		where, err := p2runtime.AgentConversation(ctx, d, id)
		if err != nil {
			return nil, err
		}
		settings, err := durable.Snapshot(ctx, d, ChatModeDoc, where)
		if err != nil {
			return nil, err
		}
		if settings == nil || !settings.Enabled {
			return nil, nil
		}
		chat, err := ctx.Get(chatcontract.Conversation, delta.JsonObjectOf("conversation", float64(input.Conversation)), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		// The client names the entry; whom it quotes and what comes from the entry.
		var answering *ReplyTo
		if input.ReplyTo != nil {
			if answering, err = ReplyTarget(ctx, d, id, *input.ReplyTo); err != nil {
				return nil, err
			}
		}
		name := settings.Name
		if name == "" {
			name = contract.DefaultAgentName
		}
		submitted := func(result delta.JsonValue, err error) (*float64, error) {
			if err != nil {
				return nil, err
			}
			submission, _ := object(result).Value("submissionId").(float64)
			return &submission, nil
		}
		if !contract.MentionsAgent(input.Text, name) {
			files, err := resolve(ctx, input.Attachments, input.Conversation)
			if err != nil {
				return nil, err
			}
			attachments := make([]any, len(files))
			for i, file := range files {
				attachments[i] = delta.JsonObjectOf("id", object(file).Value("id"), "name", object(file).Value("name"))
			}
			message := delta.JsonObjectOf("author", authorJSON(input.Author), "text", input.Text, "attachments", attachments)
			if answering != nil {
				message.Set("replyTo", answering.json())
			}
			return submitted(chat.Call("write", delta.JsonObjectOf(
				"kind", contract.ChatEntry, "data", message, "requestId", input.RequestID, "author", authorJSON(input.Author)), ctx))
		}
		inbox, err := durable.Snapshot(ctx, d, harness.InboxDoc, id)
		if err != nil {
			return nil, err
		}
		entries, err := RecentEntries(ctx, d, id, 2000)
		if err != nil {
			return nil, err
		}
		earlier := Unmentioned(entries, inbox)
		files, err := resolve(ctx, DigestFiles(earlier, input.Attachments), input.Conversation)
		if err != nil {
			return nil, err
		}
		var own []File
		for _, file := range files {
			if fileID := str(object(file), "id"); slices.Contains(input.Attachments, fileID) {
				own = append(own, File{ID: str(object(file), "id"), Name: str(object(file), "name")})
			}
		}
		mention := ComposeMention(Mention{
			Author: input.Author, Text: input.Text, Chat: earlier, ReplyTo: answering, Attachments: own, Files: files,
			RequestID: input.RequestID, WhenBusy: input.WhenBusy,
		})
		return submitted(chat.Call("submit", mention.JSON(), ctx))
	}
}

// Worker is the chat-mode plugin's worker facet.
var Worker = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	linger := linger
	if err := facet.Serve(contract.ChatModeSetting, host.LocalFactory(serveSetting), host.ServeOptions{Linger: &linger}); err != nil {
		return err
	}
	if err := p2runtime.Contribute(facet, "p2.chat-mode-router", func(self host.StoreSelf) error {
		harnessOf := p2runtime.Cached(p2runtime.HarnessOf(self))
		// The Harness is read in a section of the host first; the router, run off the loop by `Chat.send`, takes the kept one.
		_, _ = harnessOf()
		self.OnLive(func(host.Ctx) *host.Handled {
			_, _ = harnessOf()
			return nil
		})
		p2runtime.ContributeKeyed(self, chatcontract.SendRouters, p2runtime.Entry("chat-mode", route(harnessOf)))
		return nil
	}); err != nil {
		return err
	}
	return facet.Serve(contract.ChatMode, host.RemoteFactory(func(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		session, _ := params.Value("session").(string)
		conversation, _ := params.Value("conversation").(float64)
		who, err := p2runtime.FromChord(principal)
		if err != nil || !p2runtime.CanView(who, session) {
			return contracts.Implementation{}, p2runtime.Forbidden("viewers only")
		}
		setting := self.Get(contract.ChatModeSetting, delta.JsonObjectOf("conversation", conversation))
		through := func(member string) chord.Watchable[delta.JsonValue] {
			return self.Derive([]any{setting.Member(member)}, func(values []any) (delta.JsonValue, error) { return values[0], nil })
		}
		members := contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
			"enabled": through("enabled"), "waiting": through("waiting"), "name": through("name"),
		}}
		if !p2runtime.IsOwner(who) {
			return contracts.Implementation{Variant: "reader", Members: members}, nil
		}
		members.Methods = map[string]contracts.MethodFunction{
			"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return setting.Call("set", args, ctx)
			},
			"rename": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return setting.Call("rename", args, ctx)
			},
		}
		return contracts.Implementation{Variant: "owner", Members: members}, nil
	}), host.ServeOptions{})
})
