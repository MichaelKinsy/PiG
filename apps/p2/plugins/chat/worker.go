package chat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	draftscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/drafts/contract"
	filescontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/files/contract"
	modelscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	statuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
)

// The chat worker facet (apps/p2/plugins/chat/worker.ts): one Durable conversation of the worker (`Conversation`,
// host-internal, shared by every viewer) and a conversation as a principal sees it (`Chat`, `ChatCosts`).

const (
	// maxInlineImage is the largest image sent inline to a model that accepts images.
	maxInlineImage = 5 * 1024 * 1024
	// entriesPageBytes: a `Chat.entries` page ends once its entries reach about this many bytes of JSON.
	entriesPageBytes = 1_000_000
)

// liveDoc is the conversation's active context numbers (in memory), computed from its transcript.
var liveDoc = must(documents.DefineState("p2.conversation.stats", documents.StateOptions{
	Schema:  p2runtime.Contract(obj(schema.Properties{prop("stats", chatcontract.StatsSchema)})),
	Version: 1,
	Initial: func() *delta.JsonObject { return delta.JsonObjectOf("stats", nil) },
}))

var linger = 30 * time.Second

// Facet is the chat worker facet: the send routers, the shell commands, `Conversation`, `ChatCosts` and `Chat`.
func Facet() host.Facet {
	return p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
		if err := chatcontract.SendRouters.Serve(facet); err != nil {
			return err
		}
		if err := ServeShellCommands(facet); err != nil {
			return err
		}
		// Switching back to a conversation quickly does not reopen its watch.
		if err := facet.Serve(chatcontract.Conversation, host.LocalFactory(serveConversation), host.ServeOptions{Linger: &linger}); err != nil {
			return err
		}
		if err := facet.Serve(chatcontract.ChatCosts, host.RemoteFactory(serveChatCosts), host.ServeOptions{}); err != nil {
			return err
		}
		return facet.Serve(chatcontract.Chat, host.RemoteFactory(serveChat), host.ServeOptions{})
	})
}

// ---- helpers over JSON arguments and results ---------------------------------------------------------------------

func objectOf(value delta.JsonValue) *delta.JsonObject {
	object, _ := value.(*delta.JsonObject)
	return object
}

func authorJSON(author p2runtime.Author) *delta.JsonObject {
	return delta.JsonObjectOf("kind", string(author.Kind), "id", author.ID, "name", author.Name)
}

func authorOfJSON(object *delta.JsonObject) p2runtime.Author {
	return p2runtime.Author{Kind: p2runtime.AuthorKind(stringOf(object, "kind")), ID: stringOf(object, "id"), Name: stringOf(object, "name")}
}

// restored is who wrote a queued input and what they typed.
type restored struct {
	author p2runtime.Author
	text   string
}

func (r restored) json() *delta.JsonObject {
	return delta.JsonObjectOf("author", authorJSON(r.author), "text", r.text)
}

// restoredInput is who wrote a queued input and what they typed, from its entry data; tool-originated input has none.
func restoredInput(item harness.InboxItem) (restored, bool) {
	if item.Mode == harness.InboxMode("write") || item.Entry == nil {
		return restored{}, false
	}
	message, ok := p2runtime.MessageDataOf(item.Entry.Value("data"))
	if !ok || strings.TrimSpace(message.Text) == "" {
		return restored{}, false
	}
	return restored{author: message.Author, text: message.Text}, true
}

func restoredList(items []restored) []any {
	list := make([]any, len(items))
	for i, item := range items {
		list[i] = item.json()
	}
	return list
}

// models is the models of the harness configuration, looked up at each use: the models plugin may reload.
func configuredModels(source interface{ Value() any }) durable.Models {
	table, ok := source.Value().(*p2runtime.KeyedTable[p2runtime.HarnessConfigEntry])
	if !ok {
		return nil
	}
	entry, found := table.Get("default")
	if !found || entry.Models == nil {
		return nil
	}
	return entry.Models()
}

// gatesOf runs every input gate of the registry: the first refusal fails.
func runGates(table *p2runtime.KeyedTable[p2runtime.InputGate], ctx context.Context) error {
	if table == nil {
		return nil
	}
	for _, key := range table.Keys() {
		gate, _ := table.Get(key)
		if err := gate(p2runtime.CtxOf(ctx)); err != nil {
			return err
		}
	}
	return nil
}

// cached is a native getter (HarnessOf, WorkerOf) that keeps its first success: the natives do not change once open, and the
// registry value behind them may be read only inside a section of the host, so the first call is made in one (a handler's
// synchronous part) and later ones, off the loop, take the kept value.
func cached[T any](get func() (*T, error)) func() (*T, error) {
	var kept atomic.Pointer[T]
	return func() (*T, error) {
		if value := kept.Load(); value != nil {
			return value, nil
		}
		value, err := get()
		if err == nil {
			kept.Store(value)
		}
		return value, err
	}
}

// suspended tells the host a method handler is about to wait for Durable, whose publications need the host's loop.
func suspended(ctx context.Context) { chordctx.Suspend(ctx) }

// content is the message text plus attachments: a path list the agent can read with its tools (uploads stay outside the
// project), and images inline when the conversation's model accepts them. maxImages < 0 inlines every image that
// qualifies.
func content(ctx context.Context, text string, files []any, conversation harness.Conversation, models durable.Models, maxImages int) (ai.UserContentBlocks, error) {
	if len(files) == 0 {
		return ai.UserContentBlocks{ai.TextContent{Text: text}}, nil
	}
	var model *ai.Model
	if agent, err := conversation.Agent(ctx); err != nil {
		return nil, err
	} else if agent.Model != nil && models != nil {
		model = models.GetModel(agent.Model.Provider, agent.Model.ModelId)
	}
	acceptsImages := model != nil && slices.Contains(model.Input, "image")
	var eligible []*delta.JsonObject
	for _, each := range files {
		file := objectOf(each)
		size, _ := file.Value("size").(float64)
		if _, ok := file.Value("mimeType").(string); ok && acceptsImages && size <= maxInlineImage {
			eligible = append(eligible, file)
		}
	}
	// With a cap, the newest images (the last ones) are inlined; the rest are only listed.
	if maxImages >= 0 && len(eligible) > maxImages {
		eligible = eligible[len(eligible)-maxImages:]
	}
	images := make([]ai.UserContentBlock, 0, len(eligible))
	for _, file := range eligible {
		path := stringOf(file, "path")
		if local := stringOf(file, "localPath"); local != "" {
			path = local
		}
		bytes, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading attachment %s: %w", stringOf(file, "name"), err)
		}
		images = append(images, ai.ImageContent{MimeType: stringOf(file, "mimeType"), Data: base64.StdEncoding.EncodeToString(bytes)})
	}
	var list strings.Builder
	for i, each := range files {
		file := objectOf(each)
		if i > 0 {
			list.WriteString("\n")
		}
		size, _ := file.Value("size").(float64)
		fmt.Fprintf(&list, "- %s (%s, %s bytes)", stringOf(file, "path"), stringOf(file, "name"), strconv.FormatFloat(size, 'f', -1, 64))
	}
	note := "<attachments>\nThe user attached these files:\n" + list.String() + "\n</attachments>"
	first := note
	if strings.TrimSpace(text) != "" {
		first = text + "\n\n" + note
	}
	blocks := ai.UserContentBlocks{ai.TextContent{Text: first}}
	return append(blocks, images...), nil
}

// ---- the conversation ------------------------------------------------------------------------------------------

// viewFollower hands Durable's view calls to the host's followed source, and keeps the newest typed view for the
// routines that scan its entries.
type viewFollower struct {
	observer host.FollowObserver[any]
	latest   *atomic.Pointer[harness.ConversationView]
}

func (f viewFollower) Advance(ctx context.Context, call chord.WatchCall[harness.ConversationView]) {
	if call.Kind == chord.WatchCallValue {
		view := call.Value
		f.latest.Store(&view)
	}
	value, err := durable.ToJsonValue(call.Value)
	if err != nil {
		panic(err)
	}
	f.observer.Advance(chord.WatchCall[any]{Kind: call.Kind, Value: value, Ops: call.Ops, Retired: call.Retired, Error: call.Error}, ctx)
}

func (f viewFollower) End(end chord.WatchEnd) { f.observer.End(end) }

// modelMeta is the view's model from the catalog: name, window and levels, never provider configuration.
func modelMeta(view *delta.JsonObject, models durable.Models) delta.JsonValue {
	if view == nil || models == nil {
		return nil
	}
	agent := objectOf(objectOf(view.Value("docs")).Value("pi.agent"))
	ref := objectOf(agent.Value("model"))
	if ref == nil {
		return nil
	}
	model := models.GetModel(stringOf(ref, "provider"), stringOf(ref, "modelId"))
	if model == nil {
		return nil
	}
	levels := ai.GetSupportedThinkingLevels(model)
	list := make([]any, len(levels))
	for i, level := range levels {
		list[i] = string(level)
	}
	return delta.JsonObjectOf(
		"provider", model.ProviderID(), "modelId", model.ID, "name", model.DisplayName,
		"contextWindow", float64(model.Capabilities.ContextWindow), "reasoning", model.ProviderMeta.Reasoning, "thinkingLevels", list,
	)
}

func serveConversation(params *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
	conversationID, _ := params.Value("conversation").(float64)
	id := durable.ConversationId(conversationID)
	harnessOf := cached(p2runtime.HarnessOf(self))
	config := self.Registry(p2runtime.HarnessConfig.Registry)
	gates := self.Registry(p2runtime.InputGates.Registry)
	configured := self.Registry(p2runtime.ConversationConfigured.Registry)
	// Methods that start or end work push the worker's status before they answer (the sidebar's working dot).
	status := self.Get(statuscontract.StatusReport, delta.NewJsonObject(0))
	live := self.State(liveDoc).(documents.SingletonHandle)
	models := func() durable.Models { return configuredModels(config) }
	push := func(ctx context.Context) error {
		_, err := status.Call("push", nil, ctx)
		return err
	}
	// target is the Durable harness and this conversation.
	target := func(ctx context.Context) (harness.Harness, harness.Conversation, error) {
		parts, err := harnessOf()
		if err != nil {
			return nil, nil, err
		}
		opened, err := parts.Durable()
		if err != nil {
			return nil, nil, err
		}
		found, err := opened.Conversation(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		if found == nil {
			return nil, nil, fmt.Errorf("Unknown conversation %d", id)
		}
		return opened, found, nil
	}
	// Durable's default window (the newest 50, appends, back to 50 past 100), shared by every viewer.
	var latest atomic.Pointer[harness.ConversationView]
	view := self.Follow(func(observer host.FollowObserver[any], ctx context.Context) (*host.FollowAttachment[delta.JsonValue], error) {
		parts, err := harnessOf()
		if err != nil {
			return nil, err
		}
		opened, err := parts.Durable()
		if err != nil {
			return nil, err
		}
		observation, err := opened.ObserveConversation(ctx, id, viewFollower{observer: observer, latest: &latest}, nil)
		if err != nil {
			return nil, err
		}
		initial := observation.Value()
		latest.Store(&initial)
		value, err := durable.ToJsonValue(initial)
		if err != nil {
			observation.Detach()
			return nil, err
		}
		return &host.FollowAttachment[delta.JsonValue]{Value: value, Detach: observation.Detach}, nil
	})
	modelState := self.Derive([]any{view, config}, func(values []any) (delta.JsonValue, error) {
		return modelMeta(objectOf(values[0]), models()), nil
	})
	// The active context's numbers follow the entries the window appends (a scan of the new ones). A whole context is read
	// only when the active range changes otherwise (`ContextTally.Advance`): at the start, after a compaction, reset or edit,
	// or when the window skipped entries. Views arriving while it is read come folded into the routine's next call.
	tally := p2runtime.NewContextTally()
	counted := false
	var written *delta.JsonObject
	self.On(view, func(_ chord.WatchCall[delta.JsonValue], ctx host.Ctx) *host.Handled {
		// First use in this section: later ones, off the loop, take the kept natives.
		_, _ = harnessOf()
		return host.AwaitingCtx(ctx, func() error {
			newest := latest.Load()
			if newest == nil {
				return nil
			}
			if !counted || !tally.Advance(*newest) {
				_, conversation, err := target(ctx)
				if err != nil {
					return err
				}
				whole, err := conversation.Context(ctx, nil)
				if err != nil {
					return err
				}
				tally.Reset(whole)
				counted = true
				if again := latest.Load(); again != nil {
					tally.Advance(*again)
				}
			}
			next := tally.Stats()
			// Only this routine writes the document: what it wrote last is what the document holds.
			if written != nil && statsEqual(written, next) {
				return nil
			}
			stats, err := statsJSON(next)
			if err != nil {
				return err
			}
			if _, err := live.Change(ctx, func(draft *delta.Object) (any, error) { return nil, draft.Set("stats", stats) }); err != nil {
				return err
			}
			written = stats
			return nil
		})
	})
	// queued is the queued inputs (not writes) by submission order.
	queued := func(ctx context.Context, opened harness.Harness) ([]harness.InboxItem, error) {
		inbox, err := durable.Snapshot(ctx, opened, harness.InboxDoc, id)
		if err != nil || inbox == nil {
			return nil, err
		}
		items := slices.DeleteFunc(slices.Clone(inbox.Items), func(item harness.InboxItem) bool { return item.Mode == harness.InboxMode("write") })
		slices.SortFunc(items, func(a, b harness.InboxItem) int { return int(a.Id) - int(b.Id) })
		return items, nil
	}
	stats := self.Derive([]any{live}, func(values []any) (delta.JsonValue, error) {
		return objectOf(values[0]).Value("stats"), nil
	})
	// method runs fn after leaving the handler's section (Durable's publications need the loop); what lives in registries is
	// read before, inside it.
	method := func(fn func(args *delta.JsonObject, ctx context.Context, env conversationEnv) (delta.JsonValue, error)) contracts.MethodFunction {
		return func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			_, _ = harnessOf()
			gateTable, _ := p2runtime.InputGates.Table(gates)
			hooks, _ := p2runtime.ConversationConfigured.Table(configured)
			env := conversationEnv{models: models(), gates: gateTable, hooks: hooks}
			suspended(ctx)
			return fn(objectOf(args), ctx, env)
		}
	}
	return contracts.Implementation{Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{"view": view, "stats": stats, "model": modelState},
		Methods: map[string]contracts.MethodFunction{
			"costs": method(func(_ *delta.JsonObject, ctx context.Context, env conversationEnv) (delta.JsonValue, error) {
				_, conversation, err := target(ctx)
				if err != nil {
					return nil, err
				}
				whole, err := conversation.Context(ctx, nil)
				if err != nil {
					return nil, err
				}
				return costsJSON(p2runtime.ResponseCosts(whole.Entries)), nil
			}),
			"compact": method(func(args *delta.JsonObject, ctx context.Context, env conversationEnv) (delta.JsonValue, error) {
				_, conversation, err := target(ctx)
				if err != nil {
					return nil, err
				}
				taskID, err := conversation.Compact(ctx, trimmed(args, "instructions"))
				if err != nil {
					return nil, err
				}
				if err := push(ctx); err != nil {
					return nil, err
				}
				return delta.JsonObjectOf("taskId", float64(taskID)), nil
			}),
			"reset": method(func(args *delta.JsonObject, ctx context.Context, env conversationEnv) (delta.JsonValue, error) {
				_, conversation, err := target(ctx)
				if err != nil {
					return nil, err
				}
				return nil, conversation.Reset(ctx, trimmed(args, "handoff"))
			}),
			"abortCompaction": method(func(_ *delta.JsonObject, ctx context.Context, env conversationEnv) (delta.JsonValue, error) {
				opened, _, err := target(ctx)
				if err != nil {
					return nil, err
				}
				// The live compaction tasks of this conversation, as its view shows them.
				aborted := 0
				// The view is followed from Durable, whose publications apply in the loop: read it there.
				var current *delta.JsonObject
				if err := host.InSection(ctx, func() { current = objectOf(view.Value()) }); err != nil {
					return nil, err
				}
				if current != nil {
					running := objectOf(objectOf(current.Value("docs")).Value("pi.live"))
					tasks, _ := running.Value("compactions").([]any)
					for _, each := range tasks {
						taskID, _ := objectOf(each).Value("taskId").(float64)
						marked, err := opened.AbortTask(ctx, durable.TaskId(taskID))
						if err != nil {
							return nil, err
						}
						if marked == "marked" {
							// The indicator goes with the task's terminal commit (its abort hook removes its status).
							if _, err := opened.WaitForTask(ctx, durable.TaskId(taskID)); err != nil {
								return nil, err
							}
							aborted++
						}
					}
				}
				if err := push(ctx); err != nil {
					return nil, err
				}
				return delta.JsonObjectOf("aborted", float64(aborted)), nil
			}),
			"dequeueOwn": method(func(args *delta.JsonObject, ctx context.Context, env conversationEnv) (delta.JsonValue, error) {
				opened, _, err := target(ctx)
				if err != nil {
					return nil, err
				}
				authorID := stringOf(args, "authorId")
				// Selected by the author stamped into each input's entry, never by client-supplied IDs.
				var withdrawn []restored
				skipped := 0
				items, err := queued(ctx, opened)
				if err != nil {
					return nil, err
				}
				for _, item := range items {
					input, ok := restoredInput(item)
					if !ok || input.author.ID != authorID {
						continue
					}
					result, err := opened.AbortSubmission(ctx, item.Id, &id)
					if err != nil {
						return nil, err
					}
					if result == durable.SubmissionAborted {
						withdrawn = append(withdrawn, input)
					} else {
						skipped++
					}
				}
				return delta.JsonObjectOf("restored", restoredList(withdrawn), "skipped", float64(skipped)), nil
			}),
			"submit": method(func(args *delta.JsonObject, ctx context.Context, env conversationEnv) (delta.JsonValue, error) {
				if err := runGates(env.gates, ctx); err != nil {
					return nil, err
				}
				_, conversation, err := target(ctx)
				if err != nil {
					return nil, err
				}
				author := authorOfJSON(objectOf(args.Value("author")))
				text := stringOf(args, "text")
				files, _ := args.Value("files").([]any)
				attachments := make([]any, len(files))
				for i, each := range files {
					attachments[i] = delta.JsonObjectOf("id", objectOf(each).Value("id"), "name", objectOf(each).Value("name"))
				}
				maxImages := -1
				if limit, ok := args.Value("maxImages").(float64); ok {
					maxImages = int(limit)
				}
				body, err := content(ctx, text, files, conversation, env.models, maxImages)
				if err != nil {
					return nil, err
				}
				// The sender block is for the model (the system prompt attributes input by it); readers use the data.
				blocks := append(ai.UserContentBlocks{ai.TextContent{Text: p2runtime.SenderText(p2runtime.AuthorSender(author))}}, body...)
				entry := &durable.InputEntry{Kind: p2runtime.InputEntry, Data: delta.JsonObjectOf("author", authorJSON(author), "text", text, "attachments", attachments)}
				if custom := objectOf(args.Value("entry")); custom != nil {
					entry = &durable.InputEntry{Kind: stringOf(custom, "kind"), Data: custom.Value("data")}
				}
				requestID := author.ID + ":" + stringOf(args, "requestId")
				whenBusy := durable.WhenBusySteer
				if args.Value("whenBusy") == "followUp" {
					whenBusy = durable.WhenBusyFollowUp
				}
				submission, err := conversation.Submit(ctx, durable.SubmissionDraft{
					Type: durable.SubmissionTypeInput, Content: blocks, InputEntry: entry, RequestId: &requestID, WhenBusy: whenBusy,
				})
				if err != nil {
					return nil, err
				}
				if err := push(ctx); err != nil {
					return nil, err
				}
				return delta.JsonObjectOf("submissionId", float64(submission.Id())), nil
			}),
			"write": method(func(args *delta.JsonObject, ctx context.Context, env conversationEnv) (delta.JsonValue, error) {
				if err := runGates(env.gates, ctx); err != nil {
					return nil, err
				}
				_, conversation, err := target(ctx)
				if err != nil {
					return nil, err
				}
				requestID := objectOf(args.Value("author")).Value("id").(string) + ":" + stringOf(args, "requestId")
				submission, err := conversation.Submit(ctx, durable.SubmissionDraft{
					Type: durable.SubmissionTypeWrite, RequestId: &requestID,
					Entry: &durable.EntryDraft{Kind: stringOf(args, "kind"), Data: args.Value("data")},
				})
				if err != nil {
					return nil, err
				}
				return delta.JsonObjectOf("submissionId", float64(submission.Id())), nil
			}),
			"stop": method(func(_ *delta.JsonObject, ctx context.Context, env conversationEnv) (delta.JsonValue, error) {
				opened, conversation, err := target(ctx)
				if err != nil {
					return nil, err
				}
				// Conversation.Abort discards queued inputs. Withdraw them first, one by one, so each author gets their text
				// back; already placed ones are skipped.
				var withdrawn []restored
				inbox, err := durable.Snapshot(ctx, opened, harness.InboxDoc, id)
				if err != nil {
					return nil, err
				}
				if inbox != nil {
					for _, item := range inbox.Items {
						if item.Mode == harness.InboxMode("write") {
							continue
						}
						result, err := opened.AbortSubmission(ctx, item.Id, &id)
						if err != nil {
							return nil, err
						}
						if result != durable.SubmissionAborted {
							continue
						}
						if input, ok := restoredInput(item); ok {
							withdrawn = append(withdrawn, input)
						}
					}
				}
				if err := conversation.Abort(ctx, &durable.ConversationAbortOptions{Background: false}); err != nil {
					return nil, err
				}
				if err := push(ctx); err != nil {
					return nil, err
				}
				return delta.JsonObjectOf("restored", restoredList(withdrawn)), nil
			}),
			"dequeue": method(func(args *delta.JsonObject, ctx context.Context, env conversationEnv) (delta.JsonValue, error) {
				opened, _, err := target(ctx)
				if err != nil {
					return nil, err
				}
				submissionID := durable.SubmissionId(args.Value("submissionId").(float64))
				// Removing (no restore) discards the input: its text goes to nobody.
				var item *harness.InboxItem
				if args.Value("restore") == true {
					inbox, err := durable.Snapshot(ctx, opened, harness.InboxDoc, id)
					if err != nil {
						return nil, err
					}
					if inbox != nil {
						for _, each := range inbox.Items {
							if each.Id == submissionID {
								item = &each
								break
							}
						}
					}
				}
				result, err := opened.AbortSubmission(ctx, submissionID, &id)
				if err != nil {
					return nil, err
				}
				if result == durable.SubmissionNotFound {
					return nil, errors.New("Unknown queued message")
				}
				// If it raced into the transcript, it is not returned to the editor (no duplicate).
				var back []restored
				if result == durable.SubmissionAborted && item != nil {
					if input, ok := restoredInput(*item); ok {
						back = append(back, input)
					}
				}
				return delta.JsonObjectOf("restored", restoredList(back)), nil
			}),
			"entries": method(func(args *delta.JsonObject, ctx context.Context, env conversationEnv) (delta.JsonValue, error) {
				_, conversation, err := target(ctx)
				if err != nil {
					return nil, err
				}
				query := durable.EntryQuery{}
				if value, ok := args.Value("minEntryId").(float64); ok {
					entry := durable.EntryId(value)
					query.MinEntryId = &entry
				}
				if value, ok := args.Value("maxEntryId").(float64); ok {
					entry := durable.EntryId(value)
					query.MaxEntryId = &entry
				}
				if order, ok := args.Value("order").(string); ok {
					scan := durable.ScanOrder(order)
					query.Order = &scan
				}
				limit, _ := args.Value("limit").(float64)
				page, err := conversation.Entries(ctx, query, int(limit), nil)
				if err != nil {
					return nil, err
				}
				// Cut at about a megabyte (images make entries large); the loader asks again from where it ends.
				items := make([]any, 0, len(page.Items))
				bytes := 0
				for _, entry := range page.Items {
					if len(items) > 0 && bytes >= entriesPageBytes {
						break
					}
					value, err := durable.ToJsonValue(entry)
					if err != nil {
						return nil, err
					}
					encoded, err := json.Marshal(value)
					if err != nil {
						return nil, err
					}
					items = append(items, value)
					bytes += len(encoded)
				}
				return delta.JsonObjectOf("items", items, "more", len(items) < len(page.Items) || page.Next != nil), nil
			}),
			"configure": method(func(args *delta.JsonObject, ctx context.Context, env conversationEnv) (delta.JsonValue, error) {
				ref := objectOf(args.Value("model"))
				if ref != nil {
					known := env.models
					if known == nil || known.GetModel(stringOf(ref, "provider"), stringOf(ref, "modelId")) == nil {
						return nil, errors.New("Unknown model")
					}
				}
				_, conversation, err := target(ctx)
				if err != nil {
					return nil, err
				}
				change := harness.AgentChange{}
				if ref != nil {
					change.Model = harness.SetTo(durable.ModelRef{Provider: stringOf(ref, "provider"), ModelId: stringOf(ref, "modelId")})
				}
				if level := stringOf(args, "thinkingLevel"); level != "" {
					change.ThinkingLevel = harness.SetTo(ai.ModelThinkingLevel(level))
				}
				if err := conversation.Configure(ctx, change); err != nil {
					return nil, err
				}
				if env.hooks != nil {
					for _, key := range env.hooks.Keys() {
						hook, _ := env.hooks.Get(key)
						if err := hook(p2runtime.CtxOf(ctx), conversation); err != nil {
							return nil, err
						}
					}
				}
				return nil, nil
			}),
		},
	}}, nil
}

// conversationEnv is what a conversation method reads from registries before it leaves its section.
type conversationEnv struct {
	models durable.Models
	gates  *p2runtime.KeyedTable[p2runtime.InputGate]
	hooks  *p2runtime.KeyedTable[p2runtime.ConversationConfiguredHook]
}

func trimmed(args *delta.JsonObject, key string) *string {
	text := strings.TrimSpace(stringOf(args, key))
	if text == "" {
		return nil
	}
	return &text
}

func statsEqual(current *delta.JsonObject, next p2runtime.ActiveStats) bool {
	tokens, _ := current.Value("contextTokens").(float64)
	if int(tokens) != next.ContextTokens {
		return false
	}
	usage := current.Value("latestAssistantUsage")
	if (usage == nil) != (next.LatestAssistantUsage == nil) {
		return false
	}
	if usage == nil {
		return true
	}
	expected, err := durable.ToJsonValue(*next.LatestAssistantUsage)
	return err == nil && jsonEqual(usage, expected)
}

func jsonEqual(a, b delta.JsonValue) bool {
	left, errA := chordjson.Marshal(a)
	right, errB := chordjson.Marshal(b)
	//perf:ok the stats usage is compared once per turn; Chord deltas have no deep equality
	return errA == nil && errB == nil && string(left) == string(right)
}

func statsJSON(stats p2runtime.ActiveStats) (*delta.JsonObject, error) {
	object := delta.JsonObjectOf("contextTokens", float64(stats.ContextTokens))
	if stats.LatestAssistantUsage != nil {
		usage, err := durable.ToJsonValue(*stats.LatestAssistantUsage)
		if err != nil {
			return nil, err
		}
		object.Set("latestAssistantUsage", usage)
	}
	return object, nil
}

func costsJSON(costs []p2runtime.AssistantCost) []any {
	list := make([]any, len(costs))
	for i, cost := range costs {
		object := delta.JsonObjectOf("entryId", cost.EntryID, "timestamp", float64(cost.Timestamp), "cost", cost.Cost)
		if cost.CacheHit != nil {
			object.Set("cacheHit", *cost.CacheHit)
		}
		object.Set("provider", cost.Provider)
		object.Set("model", cost.Model)
		object.Set("stopReason", cost.StopReason)
		list[i] = object
	}
	return list
}

// ---- chat, as a principal sees it ------------------------------------------------------------------------------

func serveChatCosts(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
	session, _ := params.Value("session").(string)
	conversation, _ := params.Value("conversation").(float64)
	who, err := p2runtime.FromChord(principal)
	if err != nil || !p2runtime.CanView(who, session) {
		return contracts.Implementation{}, p2runtime.Forbidden("viewers only")
	}
	chat := self.Get(chatcontract.Conversation, delta.JsonObjectOf("conversation", conversation))
	return contracts.Implementation{Variant: "viewer", Members: contracts.VariantImplementation{
		Methods: map[string]contracts.MethodFunction{
			"costs": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return chat.Call("costs", nil, ctx)
			},
		},
	}}, nil
}

// passThrough is a state member that is another object's state member as it is.
func passThrough(self host.Self, source any) chord.Watchable[delta.JsonValue] {
	return self.Derive([]any{source}, func(values []any) (delta.JsonValue, error) { return values[0], nil })
}

func serveChat(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
	session, _ := params.Value("session").(string)
	conversationValue, _ := params.Value("conversation").(float64)
	conversation := int(conversationValue)
	who, err := p2runtime.FromChord(principal)
	if err != nil || !p2runtime.CanView(who, session) {
		return contracts.Implementation{}, p2runtime.Forbidden("viewers only")
	}
	if !p2runtime.IsOwner(who) && !p2runtime.IsGuest(who) {
		return contracts.Implementation{}, p2runtime.Forbidden("people only")
	}
	authorID := p2runtime.AuthorID(who)
	chat := self.Get(chatcontract.Conversation, delta.JsonObjectOf("conversation", conversationValue))
	settings := self.Get(modelscontract.WorkerModels, delta.NewJsonObject(0))
	// The author an entry records, with the owner's name as settings.json says now.
	author := func() p2runtime.Author {
		name := p2runtime.DefaultOwnerName
		if current, err := settings.Member("settings").Value(); err == nil {
			if configured, ok := objectOf(current).Value("ownerName").(string); ok {
				name = configured
			}
		}
		return p2runtime.AuthorOf(who, name)
	}
	shells := self.Get(ShellCommands, delta.NewJsonObject(0))
	liveShell := self.Derive([]any{shells.Member("shells")}, func(values []any) (delta.JsonValue, error) {
		return objectOf(values[0]).Value(strconv.Itoa(conversation)), nil
	})
	entries := func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
		return chat.Call("entries", args, ctx)
	}
	state := map[string]chord.Watchable[delta.JsonValue]{
		"view": passThrough(self, chat.Member("view")), "stats": passThrough(self, chat.Member("stats")),
		"model": passThrough(self, chat.Member("model")), "liveShell": liveShell,
	}
	if !p2runtime.CanSend(who, session) {
		return contracts.Implementation{Variant: "viewer", Members: contracts.VariantImplementation{
			State: state, Methods: map[string]contracts.MethodFunction{"entries": entries},
		}}, nil
	}
	worker := cached(p2runtime.WorkerOf(self))
	harnessOf := cached(p2runtime.HarnessOf(self))
	readOnly := self.Registry(p2runtime.ReadOnlyConversations.Registry)
	routers := self.Registry(chatcontract.SendRouters.Registry)
	gates := self.Registry(p2runtime.InputGates.Registry)
	// call is a participant's call: refused while the session moves, which waits for running calls (`Liveness.Call`).
	call := func(fn func() (delta.JsonValue, error)) (delta.JsonValue, error) {
		parts, err := worker()
		if err != nil {
			return nil, err
		}
		var result delta.JsonValue
		err = parts.Liveness.Call(func() (err error) { result, err = fn(); return err })
		return result, err
	}
	// writable refuses writes a plugin does not allow here (a one-shot agent's conversation); Stop is not one of them.
	writable := func(ctx context.Context) error {
		table, _ := p2runtime.ReadOnlyConversations.Table(readOnly)
		if table == nil {
			return nil
		}
		for _, key := range table.Keys() {
			check, _ := table.Get(key)
			reason, err := check(p2runtime.CtxOf(ctx), conversation)
			if err != nil {
				return err
			}
			if reason != "" {
				return p2runtime.Forbidden(reason)
			}
		}
		return nil
	}
	// own is the caller's own texts; other authors' go to their drafts (restored to their composers).
	own := func(items []any, ctx context.Context) ([]any, error) {
		var others, mine []any
		for _, each := range items {
			item := objectOf(each)
			if stringOf(objectOf(item.Value("author")), "id") == authorID {
				mine = append(mine, item.Value("text"))
			} else {
				others = append(others, item)
			}
		}
		if len(others) > 0 {
			store, err := ctx.(host.Ctx).Get(draftscontract.DraftStore, delta.NewJsonObject(0), host.GetOptions{})
			if err != nil {
				return nil, err
			}
			if _, err := store.Call("add", delta.JsonObjectOf("conversation", conversationValue, "items", others), ctx); err != nil {
				return nil, err
			}
		}
		return mine, nil
	}
	forbidOwner := func(message string) error {
		if !p2runtime.IsOwner(who) {
			return p2runtime.Forbidden(message)
		}
		return nil
	}
	return contracts.Implementation{Variant: "participant", Members: contracts.VariantImplementation{
		State: state,
		Methods: map[string]contracts.MethodFunction{
			"entries": entries,
			"compact": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return call(func() (delta.JsonValue, error) {
					if err := writable(ctx); err != nil {
						return nil, err
					}
					return chat.Call("compact", args, ctx)
				})
			},
			"abortCompaction": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return call(func() (delta.JsonValue, error) { return chat.Call("abortCompaction", nil, ctx) })
			},
			"reset": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return call(func() (delta.JsonValue, error) {
					// A new context changes what the agent knows for everyone in the session: the owner's choice.
					if err := forbidOwner("only the owner starts a new context"); err != nil {
						return nil, err
					}
					if err := writable(ctx); err != nil {
						return nil, err
					}
					return chat.Call("reset", args, ctx)
				})
			},
			"shell": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return call(func() (delta.JsonValue, error) {
					input := objectOf(args)
					// Anyone who may send may run commands, as tools run them for them (full guests included).
					if err := writable(ctx); err != nil {
						return nil, err
					}
					if strings.TrimSpace(stringOf(input, "command")) == "" {
						return nil, errors.New("Invalid command")
					}
					gateTable, _ := p2runtime.InputGates.Table(gates)
					if err := runGates(gateTable, ctx); err != nil {
						return nil, err
					}
					return shells.Call("start", delta.JsonObjectOf(
						"conversation", conversationValue, "command", input.Value("command"), "includeInContext", input.Value("includeInContext"),
						"author", authorJSON(author()), "requestId", input.Value("requestId"),
					), ctx)
				})
			},
			"abortShell": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return call(func() (delta.JsonValue, error) {
					shellID := stringOf(objectOf(args), "shellId")
					// Its author or the owner; the command's author comes from its running state.
					current := objectOf(liveShell.Value())
					// Only this conversation's command: `shellId` is its caller's request ID, not secret.
					if current == nil || current.Value("id") != shellID {
						return delta.JsonObjectOf("aborted", false), nil
					}
					if !p2runtime.IsOwner(who) && stringOf(objectOf(current.Value("author")), "id") != authorID {
						return nil, p2runtime.Forbidden("only its author or the owner cancels a command")
					}
					return shells.Call("abort", delta.JsonObjectOf("shellId", shellID), ctx)
				})
			},
			"dequeueOwn": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return call(func() (delta.JsonValue, error) {
					if err := writable(ctx); err != nil {
						return nil, err
					}
					result, err := chat.Call("dequeueOwn", delta.JsonObjectOf("authorId", authorID), ctx)
					if err != nil {
						return nil, err
					}
					items, _ := objectOf(result).Value("restored").([]any)
					texts := make([]any, len(items))
					for i, each := range items {
						texts[i] = objectOf(each).Value("text")
					}
					return delta.JsonObjectOf("restored", texts, "skipped", objectOf(result).Value("skipped")), nil
				})
			},
			"send": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return call(func() (delta.JsonValue, error) {
					return send(objectOf(args), ctx, conversation, author(), writable, routers, chat)
				})
			},
			"stop": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return call(func() (delta.JsonValue, error) {
					result, err := chat.Call("stop", nil, ctx)
					if err != nil {
						return nil, err
					}
					items, _ := objectOf(result).Value("restored").([]any)
					mine, err := own(items, ctx)
					if err != nil {
						return nil, err
					}
					return delta.JsonObjectOf("restored", orEmpty(mine)), nil
				})
			},
			"dequeue": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return call(func() (delta.JsonValue, error) {
					if err := writable(ctx); err != nil {
						return nil, err
					}
					// Moving back to the editor returns the caller's own text and files other authors' as their drafts;
					// removing (x) discards it, whoever wrote it.
					input := objectOf(args)
					result, err := chat.Call("dequeue", delta.JsonObjectOf("submissionId", input.Value("submissionId"), "restore", input.Value("restore") == true), ctx)
					if err != nil {
						return nil, err
					}
					items, _ := objectOf(result).Value("restored").([]any)
					mine, err := own(items, ctx)
					if err != nil {
						return nil, err
					}
					if len(mine) == 0 {
						return delta.JsonObjectOf("text", nil), nil
					}
					return delta.JsonObjectOf("text", mine[0]), nil
				})
			},
			"configure": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return call(func() (delta.JsonValue, error) {
					// The model and thinking level are the owner's choice (and cost).
					if err := forbidOwner("only the owner configures the model"); err != nil {
						return nil, err
					}
					if err := writable(ctx); err != nil {
						return nil, err
					}
					change := objectOf(args)
					parts, err := harnessOf()
					if err != nil {
						return nil, err
					}
					if _, err := chat.Call("configure", change, ctx); err != nil {
						return nil, err
					}
					// The main conversation's model is the session's: the sidebar and the next worker start use it. Either field
					// alone updates it (Pi Mobile's configure).
					if change.Value("model") == nil && change.Value("thinkingLevel") == nil {
						return nil, nil
					}
					suspended(ctx)
					opened, err := parts.Durable()
					if err != nil {
						return nil, err
					}
					main, err := p2runtime.MainConversation(ctx, opened)
					if err != nil {
						return nil, err
					}
					if int(main) != conversation {
						return nil, nil
					}
					sessions, err := ctx.(host.Ctx).Get(sessionscontract.Sessions, delta.NewJsonObject(0), host.GetOptions{})
					if err != nil {
						return nil, err
					}
					if variant, _, _ := sessions.Variant(); variant != "worker" {
						return nil, fmt.Errorf("expected the worker variant of Sessions, got %q", variant)
					}
					configure := delta.NewJsonObject(2)
					if model := change.Value("model"); model != nil {
						configure.Set("model", model)
					}
					if level := change.Value("thinkingLevel"); level != nil {
						configure.Set("thinkingLevel", level)
					}
					_, err = sessions.Call("configureSelf", configure, ctx)
					return nil, err
				})
			},
		},
	}}, nil
}

func orEmpty(list []any) []any {
	if list == nil {
		return []any{}
	}
	return list
}

// send admits a participant's message: to a router that takes it, or as an ordinary input.
func send(input *delta.JsonObject, ctx context.Context, conversation int, author p2runtime.Author, writable func(context.Context) error,
	routers *registries.RegistryValueSource, chat *host.Reference) (delta.JsonValue, error) {
	attachments, _ := input.Value("attachments").([]any)
	// As Pi Mobile: a message has text or attachments.
	if strings.TrimSpace(stringOf(input, "text")) == "" && len(attachments) == 0 {
		return nil, errors.New("Invalid content")
	}
	if err := writable(ctx); err != nil {
		return nil, err
	}
	table, _ := chatcontract.SendRouters.Table(routers)
	if table != nil {
		routed := chatcontract.RoutedSend{
			Text: stringOf(input, "text"), RequestID: stringOf(input, "requestId"), WhenBusy: stringOf(input, "whenBusy"),
			Conversation: conversation, Author: author, Attachments: make([]string, len(attachments)),
		}
		for i, each := range attachments {
			routed.Attachments[i], _ = each.(string)
		}
		if reply, ok := input.Value("replyTo").(float64); ok {
			value := int(reply)
			routed.ReplyTo = &value
		}
		for _, key := range table.Keys() {
			route, _ := table.Get(key)
			submission, err := route(p2runtime.CtxOf(ctx), routed)
			if err != nil {
				return nil, err
			}
			if submission != nil {
				return delta.JsonObjectOf("submissionId", *submission), nil
			}
		}
	}
	submit := delta.NewJsonObject(8)
	for _, key := range input.Keys() {
		if key != "attachments" {
			submit.Set(key, input.Value(key))
		}
	}
	submit.Set("author", authorJSON(author))
	if len(attachments) > 0 {
		store, err := ctx.(host.Ctx).Get(filescontract.Attachments, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		files, err := store.Call("resolve", delta.JsonObjectOf("ids", attachments, "conversation", float64(conversation)), ctx)
		if err != nil {
			return nil, err
		}
		submit.Set("files", files)
	}
	return chat.Call("submit", submit, ctx)
}
