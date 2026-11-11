package discord

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/discord/contract"
	filescontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/files/contract"
	sendfilecontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/send-file/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions"
	subagentscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/app"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Ports apps/p2/plugins/discord/worker.ts: what the server hands a session's worker. A batch becomes one input of the main
// conversation (or a thread's subagent), its attachments downloaded into the session's uploads first; a turn's answer is
// read from Durable when the server asks; a thread gets a subagent forked from the main conversation.

// maxUploadBytes: attachments up to this size are downloaded (the composer's upload limit).
const maxUploadBytes = 25 * 1024 * 1024

// imageTypes are the image types the model may get inline (the composer's: files/uploads.ts).
var imageTypes = map[string]string{"png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg", "gif": "image/gif", "webp": "image/webp"}

// LinkState is the session's Discord link as its agents learn it (session-scoped): the channel, and the conversations of
// its threads' subagents with their threads' names. Written at each delivery, cleared when the session is unlinked.
type LinkState struct {
	Link *struct {
		Channel string `json:"channel"`
		Guild   string `json:"guild"`
	} `json:"link"`
	Threads map[string]string `json:"threads"`
}

// DiscordLinkDoc is `p2.discord-link`.
var DiscordLinkDoc = durable.DefineDoc(durable.DocDefinition[LinkState]{
	CommonDocDefinition: durable.CommonDocDefinition[LinkState]{
		Kind:           "p2.discord-link",
		Version:        1,
		Initial:        func() LinkState { return LinkState{Threads: map[string]string{}} },
		CheckpointWhen: func(LinkState, []durable.Op, durable.CheckpointInfo) bool { return true },
	},
	DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeSession},
})

// DiscordPrompt is what an agent answering Discord must know, in two sentences: stable for a link (it changes only with the
// channel or thread), so the prompt's cached prefix stays.
func DiscordPrompt(where string) string {
	return strings.Join([]string{
		"Your input comes from " + where + "; your final answer of each turn is posted there as the reply, with the files you",
		"offer with send_file during the turn attached. Nothing else is posted (not intermediate text, thinking or tool",
		"output), so make the final answer complete on its own.",
	}, " ")
}

const extensionName = "p2-discord"

// discordSection is the Discord section: on for the main agent of a linked session and for its threads' subagents.
func discordSection() *durable.PromptSection {
	return harness.Section("discord", func(ctx context.Context, input durable.PromptInput) (*string, error) {
		doc, err := durable.Snapshot(ctx, input.Read, DiscordLinkDoc)
		if err != nil || doc == nil || doc.Link == nil {
			return nil, err
		}
		channel := fmt.Sprintf("the Discord channel #%s (%s)", doc.Link.Channel, doc.Link.Guild)
		if thread, ok := doc.Threads[strconv.Itoa(int(input.ConversationId))]; ok {
			text := DiscordPrompt(fmt.Sprintf("the thread %q of %s", thread, channel))
			return &text, nil
		}
		top, err := p2runtime.IsTopLevel(ctx, input.Read, input.ConversationId)
		if err != nil || !top {
			return nil, err
		}
		text := DiscordPrompt(channel)
		return &text, nil
	})
}

var workerFacet = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	if err := p2runtime.Contribute(facet, "p2.discord-extension", func(self host.StoreSelf) error {
		p2runtime.ContributeKeyed(self, p2runtime.Extensions, p2runtime.Entry(extensionName, &p2runtime.P2Extension{
			Extension: &durable.Extension{Name: extensionName, Sections: []*durable.PromptSection{discordSection()}},
		}))
		return nil
	}); err != nil {
		return err
	}
	return facet.Serve(contract.DiscordIngress, host.RemoteFactory(serveIngress), host.ServeOptions{})
})

func floatOf(object *delta.JsonObject, key string) float64 {
	f, _ := object.Value(key).(float64)
	return f
}

func serveIngress(_ *delta.JsonObject, principal data.Principal, raw host.Self) (contracts.Implementation, error) {
	if who, err := p2runtime.FromChord(principal); err != nil || !isServer(who) {
		return contracts.Implementation{}, p2runtime.Forbidden("server only")
	}
	harnessOf := p2runtime.HarnessOf(raw)
	files := raw.Get(app.Files, delta.NewJsonObject(0))
	attachments := raw.Get(filescontract.Attachments, delta.NewJsonObject(0))
	mirror := raw.Get(filescontract.FilesMirror, delta.NewJsonObject(0))
	session := func() (harness.Harness, error) {
		parts, err := harnessOf()
		if err != nil {
			return nil, err
		}
		return parts.Durable()
	}
	conversationOf := func(ctx context.Context, id int) (*host.Reference, error) {
		scope := p2runtime.CtxOf(ctx)
		if scope == nil {
			return nil, errors.New("no call context")
		}
		return scope.Get(chatcontract.Conversation, delta.JsonObjectOf("conversation", float64(id)), host.GetOptions{})
	}
	return contracts.Implementation{Variant: "server", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
		"deliver": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			in := argsOf(args)
			durableSession, err := session()
			if err != nil {
				return nil, err
			}
			// Pi's handler reads the Harness, then awaits: the handler leaves the section of the call here (Durable's
			// publications need the host's loop, which a handler that waits for its commit would hold).
			chordctx.Suspend(ctx)
			if err := sessions.MirrorLock(ctx, durableSession, in.Value("lock")); err != nil {
				return nil, err
			}
			var target int
			var thread *string
			where := argsOf(in.Value("context"))
			if found, ok := in.Value("conversation").(float64); ok {
				target = int(found)
				if text, ok := where.Value("thread").(string); ok {
					thread = &text
				}
			} else {
				main, err := p2runtime.MainConversation(ctx, durableSession)
				if err != nil {
					return nil, err
				}
				target = int(main)
			}
			_, hasConversation := in.Value("conversation").(float64)
			if _, err := durableSession.Commit(ctx, func(tx durable.Tx) (any, error) {
				doc, err := durable.TxDoc[LinkState](tx, DiscordLinkDoc)
				if err != nil {
					return nil, err
				}
				if err := doc.Set("link", delta.JsonObjectOf("channel", str(where, "channel"), "guild", str(where, "guild"))); err != nil {
					return nil, err
				}
				if hasConversation && thread != nil {
					return nil, doc.Object("threads").Set(strconv.Itoa(target), *thread)
				}
				return nil, nil
			}); err != nil {
				return nil, err
			}
			type uploaded struct{ id, name string }
			var ids []uploaded
			downloads, _ := in.Value("files").([]any)
			for _, each := range downloads {
				file := argsOf(each)
				id, err := download(files, file, ctx)
				if err != nil {
					return nil, err
				}
				if id != "" {
					ids = append(ids, uploaded{id, str(file, "name")})
				}
			}
			// The Files sheet lists them as it lists the composer's uploads.
			if len(ids) > 0 {
				if _, err := mirror.Call("push", p2runtime.UploadsSpace, ctx); err != nil {
					return nil, err
				}
			}
			var resolved delta.JsonValue
			if len(ids) > 0 {
				keys := make([]any, len(ids))
				for i, each := range ids {
					keys[i] = each.id
				}
				if resolved, err = attachments.Call("resolve", delta.JsonObjectOf("ids", keys, "conversation", float64(target)), ctx); err != nil {
					return nil, err
				}
			}
			author := argsOf(in.Value("author"))
			who := delta.JsonObjectOf("kind", "guest", "id", "discord:"+str(author, "id"), "name", str(author, "name")+" · Discord")
			listed := make([]any, len(ids))
			for i, each := range ids {
				listed[i] = delta.JsonObjectOf("id", each.id, "name", each.name)
			}
			text := str(in, "text")
			message := delta.JsonObjectOf("author", who, "text", text, "attachments", listed, "discord", where)
			submit := delta.JsonObjectOf("text", text, "requestId", str(in, "requestId"), "whenBusy", "followUp", "author", who)
			if list, ok := resolved.([]any); ok && len(list) > 0 {
				submit.Set("files", list)
			}
			submit.Set("entry", delta.JsonObjectOf("kind", contract.DiscordBatch, "data", message))
			chat, err := conversationOf(ctx, target)
			if err != nil {
				return nil, err
			}
			submitted, err := chat.Call("submit", submit, ctx)
			if err != nil {
				return nil, err
			}
			return delta.JsonObjectOf("conversation", float64(target), "submissionId", floatOf(argsOf(submitted), "submissionId")), nil
		},
		"unlinked": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			durableSession, err := session()
			if err != nil {
				return nil, err
			}
			chordctx.Suspend(ctx)
			_, err = durableSession.Commit(ctx, func(tx durable.Tx) (any, error) {
				doc, err := durable.TxDoc[LinkState](tx, DiscordLinkDoc)
				if err != nil {
					return nil, err
				}
				if err := doc.Set("link", nil); err != nil {
					return nil, err
				}
				return nil, doc.Set("threads", delta.NewJsonObject(0))
			})
			return nil, err
		},
		"turn": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			durableSession, err := session()
			if err != nil {
				return nil, err
			}
			chordctx.Suspend(ctx)
			return turn(ctx, durableSession, files, int64(floatOf(argsOf(args), "submissionId")))
		},
		"read": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			in := argsOf(args)
			found, err := p2runtime.FindDownload(files, str(in, "id"), ctx)
			if err != nil {
				return nil, err
			}
			if found == nil {
				return nil, errors.New("No such file")
			}
			return files.Call("read", delta.JsonObjectOf("id", found.File.Value("id"), "offset", in.Value("offset"), "length", in.Value("length")), ctx)
		},
		"command": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			in := argsOf(args)
			durableSession, err := session()
			if err != nil {
				return nil, err
			}
			chordctx.Suspend(ctx)
			target := 0
			if found, ok := in.Value("conversation").(float64); ok {
				target = int(found)
			} else {
				main, err := p2runtime.MainConversation(ctx, durableSession)
				if err != nil {
					return nil, err
				}
				target = int(main)
			}
			chat, err := conversationOf(ctx, target)
			if err != nil {
				return nil, err
			}
			text, err := runCommand(ctx, durableSession, chat, target, str(in, "command"))
			if err != nil {
				return nil, err
			}
			return delta.JsonObjectOf("text", text), nil
		},
		"thread": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			in := argsOf(args)
			durableSession, err := session()
			if err != nil {
				return nil, err
			}
			chordctx.Suspend(ctx)
			child, err := forkThread(ctx, durableSession, str(in, "name"), floatOf(in, "before"))
			if err != nil {
				return nil, err
			}
			return delta.JsonObjectOf("conversation", float64(child)), nil
		},
	}}}, nil
}

func isServer(who p2runtime.Principal) bool { _, ok := who.(p2runtime.ServerPrincipal); return ok }

// download downloads a Discord attachment into the session's uploads (its key makes a retry find it); its upload ID, empty
// when it could not be downloaded.
func download(files *host.Reference, file *delta.JsonObject, ctx context.Context) (string, error) {
	key, name, size := str(file, "key"), str(file, "name"), floatOf(file, "size")
	existing, err := files.Call("byKey", delta.JsonObjectOf("space", p2runtime.UploadsSpace, "key", key), ctx)
	if err != nil {
		return "", err
	}
	if existing != nil {
		return key, nil
	}
	if size > maxUploadBytes {
		return "", nil
	}
	path := strings.ReplaceAll(key, ":", "-") + "-" + p2runtime.SafeFileName(name)
	extension := strings.ToLower(path[strings.LastIndexByte(path, '.')+1:])
	mime := imageTypes[extension]
	if mime == "" {
		mime = "application/octet-stream"
	}
	begun, err := files.Call("begin", delta.JsonObjectOf(
		"space", p2runtime.UploadsSpace, "path", path, "mime", mime, "source", "discord", "key", key, "ref", str(file, "ref"),
		"meta", delta.JsonObjectOf("name", name),
	), ctx)
	if err != nil {
		return "", err
	}
	started := argsOf(begun)
	if started.Value("kind") == "exists" {
		return key, nil
	}
	upload := str(started, "upload")
	fail := func(err error) (string, error) {
		_, _ = files.Call("cancel", upload, ctx)
		fmt.Printf("p2 discord: could not download %s: %s\n", name, err)
		return "", nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, str(file, "url"), nil)
	if err != nil {
		return fail(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fail(err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fail(fmt.Errorf("Download failed: %d", response.StatusCode))
	}
	offset := 0
	buffer := make([]byte, p2runtime.FileChunk)
	for {
		n, err := io.ReadFull(response.Body, buffer)
		if n > 0 {
			if _, callErr := files.Call("append", delta.JsonObjectOf("upload", upload, "offset", float64(offset), "bytes", slices.Clone(buffer[:n])), ctx); callErr != nil {
				return fail(callErr)
			}
			offset += n
			if offset > maxUploadBytes {
				return fail(errors.New("Attachment too large"))
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return fail(err)
		}
	}
	if _, err := files.Call("finish", upload, ctx); err != nil {
		return fail(err)
	}
	return key, nil
}

// offered is the files conversation offered (`send_file`, also from scripts) between start and end (ms since the epoch): a
// turn's, oldest first.
func offered(files *host.Reference, conversation int, start, end float64, ctx context.Context) ([]any, error) {
	found := []any{}
	after := ""
	for {
		query := delta.JsonObjectOf("space", p2runtime.DownloadsSpace, "ref", sendfilecontract.OfferedBy(conversation))
		if after != "" {
			query.Set("after", after)
		}
		result, err := files.Call("byRef", query, ctx)
		if err != nil {
			return nil, err
		}
		page := argsOf(result)
		items, _ := page.Value("items").([]any)
		for _, each := range items {
			file := argsOf(each)
			created := floatOf(file, "created")
			if key := str(file, "key"); key != "" && created >= start && created <= end {
				found = append(found, delta.JsonObjectOf("id", key, "name", p2runtime.DisplayName(file), "size", floatOf(file, "size")))
			}
		}
		next, ok := page.Value("next").(string)
		if !ok {
			return found, nil
		}
		after = next
	}
}

func messageTime(message ai.Message) float64 {
	switch typed := message.(type) {
	case ai.UserMessage:
		return float64(typed.Timestamp)
	case ai.AssistantMessage:
		return float64(typed.Timestamp)
	case ai.ToolResultMessage:
		return float64(typed.Timestamp)
	}
	return 0
}

// entryOf is entry id of a conversation.
func entryOf(ctx context.Context, session harness.Harness, conversationID durable.ConversationId, id durable.EntryId) (*durable.EntryRecord, error) {
	conversation, err := session.Conversation(ctx, conversationID)
	if err != nil || conversation == nil {
		return nil, err
	}
	page, err := conversation.Entries(ctx, durable.EntryQuery{ConversationId: conversationID, MinEntryId: &id, MaxEntryId: &id}, 1, nil)
	if err != nil || len(page.Items) == 0 {
		return nil, err
	}
	return &page.Items[0], nil
}

func turnState(name string, extra ...any) *delta.JsonObject {
	out := delta.JsonObjectOf("state", name)
	for i := 0; i+1 < len(extra); i += 2 {
		out.Set(extra[i].(string), extra[i+1])
	}
	return out
}

func nowMs() float64 { return float64(timeNow().UnixMilli()) }

// turn is the submission's turn: still pending, answered (the answer's text), stopped (aborted, or withdrawn while queued),
// or ended without an answer; with the files its conversation offered during it.
func turn(ctx context.Context, session harness.Harness, files *host.Reference, id int64) (delta.JsonValue, error) {
	submission, err := session.Submission(ctx, durable.SubmissionId(id))
	if err != nil {
		return nil, err
	}
	// Gone: withdrawn while queued (a stop).
	if submission == nil {
		return turnState("stopped"), nil
	}
	record, err := submission.Status(ctx)
	if err != nil {
		return nil, err
	}
	if record.Type != durable.SubmissionTypeInput {
		return turnState("unanswered", "reason", "Not an input.", "files", []any{}), nil
	}
	if record.Status == durable.SubmissionQueued || record.Status == durable.SubmissionPlaced {
		return turnState("pending"), nil
	}
	if record.Status == durable.SubmissionUnanswered && record.Reason != nil && *record.Reason == "aborted" {
		return turnState("stopped"), nil
	}
	// The turn's span: from its input (its message's time) to its answer, or until now without one.
	start := float64(0)
	if record.Entry != nil {
		input, err := entryOf(ctx, session, record.ConversationId, *record.Entry)
		if err != nil {
			return nil, err
		}
		if input != nil && len(input.Model) > 0 {
			start = messageTime(input.Model[0])
		}
	}
	conversation := int(record.ConversationId)
	if record.Status == durable.SubmissionUnanswered {
		reason := "The turn ended without an answer."
		if record.Reason != nil && *record.Reason != "" {
			reason = *record.Reason
		}
		sent, err := offered(files, conversation, start, nowMs(), ctx)
		if err != nil {
			return nil, err
		}
		return turnState("unanswered", "reason", reason, "files", sent), nil
	}
	var message *ai.AssistantMessage
	if record.Answer != nil {
		answer, err := entryOf(ctx, session, record.ConversationId, *record.Answer)
		if err != nil {
			return nil, err
		}
		if answer != nil && len(answer.Model) > 0 {
			if typed, ok := answer.Model[0].(ai.AssistantMessage); ok {
				message = &typed
			}
		}
	}
	if message != nil && message.StopReason == ai.StopReasonAborted {
		return turnState("stopped"), nil
	}
	var text strings.Builder
	end := nowMs()
	if message != nil {
		for _, part := range message.Content {
			if block, ok := part.(ai.TextContent); ok {
				text.WriteString(block.Text)
			}
		}
		end = float64(message.Timestamp)
	}
	answer := strings.TrimFunc(text.String(), func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\u00a0' || r == '\ufeff' || r == '\v' || r == '\f'
	})
	sent, err := offered(files, conversation, start, end, ctx)
	if err != nil {
		return nil, err
	}
	if answer != "" || len(sent) > 0 {
		return turnState("answered", "text", answer, "files", sent), nil
	}
	reason := "The answer has no text."
	if message != nil && message.StopReason == ai.StopReasonError {
		reason = message.ErrorMessage
		if reason == "" {
			reason = "The model failed."
		}
	}
	return turnState("unanswered", "reason", reason, "files", sent), nil
}

// dollars is a cost in dollars, as the cost sheet writes it.
func dollars(value float64) string {
	places := 6
	if value >= 0.01 || value == 0 {
		places = 4
	}
	return "$" + strconv.FormatFloat(value, 'f', places, 64)
}

// grouped is n as `toLocaleString("en-US")` writes an integer.
func grouped(n float64) string {
	digits := strconv.FormatFloat(n, 'f', 0, 64)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	for i := len(digits) - 3; i > 0; i -= 3 {
		digits = digits[:i] + "," + digits[i:]
	}
	return sign + digits
}

// lastInputTime is when the newest input of conversation was sent (its user message's time): where its last turn began.
func lastInputTime(ctx context.Context, session harness.Harness, id durable.ConversationId) (*float64, error) {
	conversation, err := session.Conversation(ctx, id)
	if err != nil || conversation == nil {
		return nil, err
	}
	// Newest first; a turn's tail fits one page.
	page, err := conversation.Entries(ctx, durable.EntryQuery{ConversationId: id}, 200, nil)
	if err != nil {
		return nil, err
	}
	for _, entry := range page.Items {
		if len(entry.Model) > 0 {
			if user, ok := entry.Model[0].(ai.UserMessage); ok {
				at := float64(user.Timestamp)
				return &at, nil
			}
		}
	}
	return nil, nil
}

// runCommand runs a channel command on conversation target: `stop` as the web's Stop (queued inputs withdrawn, the run
// aborted), `status`, `compact`. The reply's text.
func runCommand(ctx context.Context, session harness.Harness, chat *host.Reference, target int, command string) (string, error) {
	id := durable.ConversationId(target)
	// From Durable itself: the conversation's view may not have caught up with a compaction just started.
	live, err := durable.Snapshot(ctx, session, harness.LiveDoc, id)
	if err != nil {
		return "", err
	}
	inbox, err := durable.Snapshot(ctx, session, harness.InboxDoc, id)
	if err != nil {
		return "", err
	}
	queued := 0
	if inbox != nil {
		for _, item := range inbox.Items {
			if item.Mode != harness.InboxWrite {
				queued++
			}
		}
	}
	working := live != nil && live.Run != nil
	compacting := 0
	if live != nil {
		compacting = len(live.Compactions)
	}
	switch command {
	case "stop":
		if !working && queued == 0 && compacting == 0 {
			return "Nothing is running.", nil
		}
		var replies []string
		if working || queued > 0 {
			// The web's Stop: queued inputs withdrawn first, then the run aborted.
			stopped, err := chat.Call("stop", nil, ctx)
			if err != nil {
				return "", err
			}
			restored, _ := argsOf(stopped).Value("restored").([]any)
			if withdrawn := len(restored); withdrawn > 0 {
				plural := "s"
				if withdrawn == 1 {
					plural = ""
				}
				replies = append(replies, fmt.Sprintf("Stopped; %d queued message%s withdrawn.", withdrawn, plural))
			} else {
				replies = append(replies, "Stopped.")
			}
		}
		// A compaction is a task of its own, which Stop leaves running: aborted as the web's Escape does.
		aborted := 0
		if live != nil {
			for _, compaction := range live.Compactions {
				marked, err := session.AbortTask(ctx, compaction.TaskId)
				if err != nil {
					return "", err
				}
				if marked == "marked" {
					if _, err := session.WaitForTask(ctx, compaction.TaskId); err != nil {
						return "", err
					}
					aborted++
				}
			}
		}
		if aborted > 0 {
			replies = append(replies, "Stopped compaction.")
		}
		if len(replies) == 0 {
			return "Nothing is running.", nil
		}
		return strings.Join(replies, " "), nil
	case "compact":
		if _, err := chat.Call("compact", delta.NewJsonObject(0), ctx); err != nil {
			return "", err
		}
		return "Compacting the context.", nil
	}
	modelValue, _ := chat.Member("model").Value()
	model := objectOf(modelValue)
	statsValue, _ := chat.Member("stats").Value()
	var tokens *float64
	if stats := objectOf(statsValue); stats != nil {
		if number, ok := stats.Value("contextTokens").(float64); ok {
			tokens = &number
		}
	}
	// The cost sheet's data: each response of the active range with its cost.
	costsValue, err := chat.Call("costs", nil, ctx)
	if err != nil {
		return "", err
	}
	costs, _ := costsValue.([]any)
	total := float64(0)
	for _, each := range costs {
		total += floatOf(argsOf(each), "cost")
	}
	since, err := lastInputTime(ctx, session, id)
	if err != nil {
		return "", err
	}
	usage := "unknown"
	switch window := float64(0); {
	case tokens == nil:
	default:
		if model != nil {
			window = floatOf(model, "contextWindow")
		}
		if window > 0 {
			usage = fmt.Sprintf("%s of %s tokens (%s%%)", grouped(*tokens), grouped(window), jsRound(*tokens/window*100))
		} else {
			usage = grouped(*tokens) + " tokens"
		}
	}
	modelText := "none"
	if model != nil {
		modelText = fmt.Sprintf("%s (%s/%s)", str(model, "name"), str(model, "provider"), str(model, "modelId"))
	}
	working1 := "no"
	if working {
		working1 = "yes"
	}
	lines := []string{"Model: " + modelText, "Working: " + working1}
	if compacting > 0 {
		lines = append(lines, "Compacting: yes")
	}
	cost := "Cost: " + dollars(total) + " so far"
	if since != nil {
		last := float64(0)
		for _, each := range costs {
			if entry := argsOf(each); floatOf(entry, "timestamp") >= *since {
				last += floatOf(entry, "cost")
			}
		}
		cost += ", last turn " + dollars(last)
	}
	lines = append(lines, fmt.Sprintf("Queued: %d", queued), "Context: "+usage, cost)
	return strings.Join(lines, "\n"), nil
}

// jsRound is Math.round (half up) as an integer's text.
func jsRound(value float64) string { return strconv.FormatFloat(floorHalf(value), 'f', 0, 64) }

var nonSlug = regexp.MustCompile(`[^a-z0-9-]+`)

// finished is when an entry was written, if it is a finished answer (an assistant message that ended its turn).
func finished(entry durable.EntryRecord) (float64, bool) {
	if len(entry.Model) == 0 {
		return 0, false
	}
	message, ok := entry.Model[0].(ai.AssistantMessage)
	if !ok || (message.StopReason != ai.StopReasonStop && message.StopReason != ai.StopReasonLength) {
		return 0, false
	}
	return float64(message.Timestamp), true
}

// forkThread makes a subagent for a Discord thread, forked from the main conversation at its last finished answer before
// `before`, or a new conversation with the main agent's configuration when there is none; registered as subagent `name`
// (unique). It follows the session's location and has no reporter: its answers go back to the thread.
func forkThread(ctx context.Context, session harness.Harness, label string, before float64) (durable.ConversationId, error) {
	main, err := p2runtime.MainConversation(ctx, session)
	if err != nil {
		return 0, err
	}
	conversation, err := session.Conversation(ctx, main)
	if err != nil {
		return 0, err
	}
	if conversation == nil {
		return 0, errors.New("No main conversation")
	}
	// Newest first (the default order), a page at a time, until an answer before the thread's creation.
	var point *durable.EntryRecord
	var cursor durable.Cursor
	for {
		page, err := conversation.Entries(ctx, durable.EntryQuery{ConversationId: main}, 200, cursor)
		if err != nil {
			return 0, err
		}
		for i := range page.Items {
			if at, ok := finished(page.Items[i]); ok && at < before {
				point = &page.Items[i]
				break
			}
		}
		if point != nil || page.Next == nil {
			break
		}
		cursor = *page.Next
	}
	slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(p2runtime.SafeFileName(label)), "-"), "-")
	if len(slug) > 40 {
		slug = slug[:40]
	}
	if slug == "" {
		slug = "thread"
	}
	base := "discord-" + slug
	child, err := session.Commit(ctx, func(tx durable.Tx) (any, error) {
		registry, err := durable.TxDoc[subagentscontract.SubagentState](tx, subagentscontract.SubagentsDoc, durable.ROOT_CONVERSATION_ID)
		if err != nil {
			return nil, err
		}
		agents := registry.Object("agents")
		name := base
		for index := 2; agents.Has(name); index++ {
			name = fmt.Sprintf("%s-%d", base, index)
		}
		owner := durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}}
		// A fork carries the agent as it was at its entry (AgentDoc forks as of it); a new conversation gets today's.
		mainAgent, err := durable.TxDoc[harness.AgentState](tx, harness.AgentDoc, main)
		if err != nil {
			return nil, err
		}
		agent := mainAgent.Snapshot().Clone()
		var child durable.ConversationId
		if point != nil {
			record, err := tx.ForkConversation(main, point.Id, owner)
			if err != nil {
				return nil, err
			}
			child = record.Id
		} else {
			record, err := tx.CreateConversation(owner)
			if err != nil {
				return nil, err
			}
			child = record.Id
			target, err := durable.TxDoc[harness.AgentState](tx, harness.AgentDoc, child)
			if err != nil {
				return nil, err
			}
			for key, value := range agent.All() {
				if err := target.Set(key, value); err != nil {
					return nil, err
				}
			}
		}
		modelText := ""
		if model := objectOf(agent.Value("model")); model != nil {
			modelText = str(model, "provider") + "/" + str(model, "modelId")
		}
		level, _ := agent.Value("thinkingLevel").(string)
		if level == "" {
			level = "off"
		}
		tools := []any{}
		if list, ok := agent.Value("tools").([]any); ok {
			tools = slices.Clone(list)
		}
		return child, agents.Set(name, delta.JsonObjectOf(
			"conversationId", float64(child), "model", modelText, "thinkingLevel", level, "tools", tools, "reported", []any{},
		))
	})
	if err != nil {
		return 0, err
	}
	return child.(durable.ConversationId), nil
}
