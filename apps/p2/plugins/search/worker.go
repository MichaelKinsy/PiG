package search

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/search/contract"
	subagentscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	durableharness "github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/search/worker.ts: feeds the top-level conversations' (the main conversation and its `/tree` branches)
// and subagents' messages into the server index with per-conversation acknowledged catch-up.

// page is how many entries one batch carries.
const page = 200

// retryAfter is how long the indexer waits before scanning again after a failure.
const retryAfter = time.Second

var indexer = func() *contracts.Contract {
	contract, err := contracts.Local("p2.search-indexer", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{},
		// The first full scan since this worker started has committed (an index rebuild waits for it).
		Members: []contracts.LocalEntry{contracts.Mem("scanned", p2runtime.Contract(schema.Bool()))},
	})
	if err != nil {
		panic(err)
	}
	return contract
}()

// contentText is the text of a message's content: a string, or the text parts of its blocks joined by newlines.
func contentText(message ai.Message) (role string, text string, timestamp int64) {
	switch typed := message.(type) {
	case ai.UserMessage:
		return "user", userText(typed.Content), typed.Timestamp
	case *ai.UserMessage:
		return "user", userText(typed.Content), typed.Timestamp
	case ai.AssistantMessage:
		return "assistant", assistantText(typed.Content), typed.Timestamp
	case *ai.AssistantMessage:
		return "assistant", assistantText(typed.Content), typed.Timestamp
	}
	return message.MessageRole(), "", 0
}

func userText(content ai.UserContent) string {
	switch typed := content.(type) {
	case ai.UserText:
		return string(typed)
	case ai.UserContentBlocks:
		var parts []string
		for _, block := range typed {
			if text, ok := block.(ai.TextContent); ok {
				parts = append(parts, text.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func assistantText(content []ai.AssistantContentBlock) string {
	var parts []string
	for _, block := range content {
		if text, ok := block.(ai.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// indexedMessage is what an input or answer said: a person's, a peer session's or a subagent's input by its data (what they
// typed, not a mention's digest), a tool's input and an answer by their text.
func indexedMessage(entry *durable.EntryRecord) (*delta.JsonObject, bool) {
	if len(entry.Model) == 0 {
		return nil, false
	}
	role := entry.Model[0].MessageRole()
	if role != "user" && role != "assistant" {
		return nil, false
	}
	// Durable's own entries with user messages (compaction summaries, handoffs) are not inputs; neither are user shell
	// commands (`!cmd`), whose model message is the command's output (it must not become the session's title).
	if role == "user" && strings.HasPrefix(entry.Kind, "pi.") && entry.Kind != "pi.user" {
		return nil, false
	}
	if entry.Kind == p2runtime.ShellEntry {
		return nil, false
	}
	if role == "assistant" && !durable.AssistantEntry.Is(entry) {
		return nil, false
	}
	var texts []string
	timestamp, first := int64(0), true
	for _, message := range entry.Model {
		messageRole, text, at := contentText(message)
		if messageRole != role {
			continue
		}
		if first {
			timestamp, first = at, false
		}
		texts = append(texts, text)
	}
	typed, found := "", false
	if role == "user" {
		if message, ok := p2runtime.MessageDataOf(entry.Data); ok {
			typed, found = message.Text, true
		} else if peer, ok := p2runtime.PeerMessageOf(entry.Kind, entry.Data); ok {
			typed, found = peer.Text, true
		} else if agent, ok := p2runtime.AgentMessageOf(entry.Kind, entry.Data); ok {
			typed, found = agent.Text, true
		}
	}
	text := strings.Join(texts, "\n")
	if found {
		text = typed
	}
	if strings.TrimSpace(text) == "" {
		return nil, false
	}
	return delta.JsonObjectOf("conversationId", float64(entry.ConversationId), "entryId", float64(entry.Id), "role", role, "timestamp", float64(timestamp), "text", text), true
}

// catchUp sends a conversation's entries after through up to the newest, a page at a time; each call returns once the server
// committed it. The cursor sent with a page stays through until the last page, so an interruption makes the next scan start
// over the same range (indexing is idempotent by entry ID). It returns the new cursor.
func catchUp(ctx context.Context, root durableharness.Conversation, feed *host.Reference, through int) (int, error) {
	newest, err := root.Entries(ctx, durable.EntryQuery{}, 1, nil)
	if err != nil {
		return through, err
	}
	if len(newest.Items) == 0 || int(newest.Items[0].Id) <= through {
		return through, nil
	}
	upper := int(newest.Items[0].Id)
	var cursor durable.Cursor
	for {
		first, last := durable.EntryId(through+1), durable.EntryId(upper)
		result, err := root.Entries(ctx, durable.EntryQuery{MinEntryId: &first, MaxEntryId: &last}, page, cursor)
		if err != nil {
			return through, err
		}
		messages := []any{}
		for _, entry := range result.Items {
			if message, ok := indexedMessage(&entry); ok {
				messages = append(messages, message)
			}
		}
		sent := through
		if result.Next == nil {
			sent = upper
		}
		if _, err := feed.Call("index", delta.JsonObjectOf("conversation", float64(root.Id()), "messages", messages, "through", float64(sent)), ctx); err != nil {
			return through, err
		}
		if result.Next == nil {
			return upper, nil
		}
		cursor = *result.Next
	}
}

// indexing is what the indexer's native holds: the commit signal and the session's Harness.
type indexing struct {
	signal *p2runtime.CommitSignal
	parts  *p2runtime.HarnessParts
}

func serveIndexer(facet host.FacetHost) error {
	return facet.Serve(indexer, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		harness := p2runtime.HarnessOf(self)
		feed := self.Get(contract.SearchFeed, delta.NewJsonObject(0))
		scanned := self.Writable(false)
		commits := self.Open(
			func(context.Context) (any, error) {
				parts, err := harness()
				if err != nil {
					return nil, err
				}
				session, err := parts.Durable()
				if err != nil {
					return nil, err
				}
				signal := p2runtime.NewCommitSignal(session, func(publication durable.CommitPublication) bool {
					for _, change := range publication.Changes {
						switch typed := change.(type) {
						case durable.EntryWrite:
							return true
						case durable.DocumentChange:
							if typed.Record.Kind == subagentscontract.SubagentsDoc.AnyDefinition().Kind {
								return true
							}
						}
					}
					return false
				})
				return &indexing{signal: signal, parts: parts}, nil
			},
			func(value any, _ host.NativeCloseReason, _ context.Context) error {
				value.(*indexing).signal.Close()
				return nil
			},
		)
		// A watch is not an audit log: scan immutable history at start and after every entry commit, so overflow, resets,
		// compaction and crashes cannot lose entries.
		self.OnLive(func(ctx host.Ctx) *host.Handled {
			value, err := commits.Value()
			if err != nil {
				return host.Threw(err)
			}
			signal, parts := value.(*indexing).signal, value.(*indexing).parts
			return host.AwaitingCtx(ctx, func() error {
				for ctx.Err() == nil {
					seen := signal.Version()
					if err := scanOnce(ctx, parts, feed); err != nil {
						if ctx.Err() != nil {
							return nil
						}
						// The server was unreachable or reloading: ask for its cursor again and retry the range.
						if chordErr, ok := data.IsChordError(err); !ok || chordErr.Code != data.CodeUnavailable {
							fmt.Fprintln(os.Stderr, "p2 search: indexing failed, retrying:", err)
						}
						select {
						case <-time.After(retryAfter):
						case <-ctx.Done():
						}
						continue
					}
					_ = scanned.Set(true)
					_ = signal.Next(ctx, seen)
				}
				return nil
			})
		})
		return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
			"scanned": self.Derive([]any{scanned}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
		}}}, nil
	}), host.ServeOptions{})
}

// scanOnce sends every conversation's entries the server has not indexed yet.
func scanOnce(ctx host.Ctx, parts *p2runtime.HarnessParts, feed *host.Reference) error {
	session, err := parts.Durable()
	if err != nil {
		return err
	}
	registry, err := durable.Snapshot(ctx, session, subagentscontract.SubagentsDoc, durable.ROOT_CONVERSATION_ID)
	if err != nil {
		return err
	}
	// A branch's history starts after its fork point: what it inherits is indexed with the conversation storing it.
	records, err := p2runtime.TopLevelRecords(ctx, session)
	if err != nil {
		return err
	}
	starts := map[durable.ConversationId]int{}
	var order []durable.ConversationId
	add := func(id durable.ConversationId, start int) {
		if _, seen := starts[id]; !seen {
			order = append(order, id)
		}
		starts[id] = start
	}
	for _, record := range records {
		start := 0
		if record.Parent != nil {
			start = int(record.Parent.At)
		}
		add(record.Id, start)
	}
	if registry != nil {
		names := make([]string, 0, len(registry.Agents))
		for name := range registry.Agents {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			add(registry.Agents[name].ConversationId, 0)
		}
	}
	for _, id := range order {
		conversation, err := session.Conversation(ctx, id)
		if err != nil {
			return err
		}
		if conversation == nil {
			continue
		}
		cursor, err := feed.Call("cursor", delta.JsonObjectOf("conversation", float64(id)), ctx)
		if err != nil {
			return err
		}
		through, _ := cursor.(float64)
		if _, err := catchUp(ctx, conversation, feed, max(int(through), starts[id])); err != nil {
			return err
		}
	}
	return nil
}

func serveScan(facet host.FacetHost) error {
	return facet.Serve(contract.IndexScan, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		if who, err := p2runtime.FromChord(principal); err != nil || who != (p2runtime.ServerPrincipal{}) {
			return contracts.Implementation{}, p2runtime.Forbidden("server only")
		}
		scan := self.Get(indexer, delta.NewJsonObject(0))
		return contracts.Implementation{Variant: "server", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			"scanned": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				_, err := p2runtime.Until(ctx, scan.Member("scanned"), func(value delta.JsonValue) bool { return value == true })
				return nil, err
			},
		}}}, nil
	}), host.ServeOptions{})
}

// WorkerFacet serves the worker's indexer and the scan the server's rebuild waits for.
var WorkerFacet = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	if err := serveIndexer(facet); err != nil {
		return err
	}
	return serveScan(facet)
})
