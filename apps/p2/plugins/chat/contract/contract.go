// Package contract ports apps/p2/plugins/chat/contract.ts: a conversation as a principal sees it, the worker-internal
// conversation, the user shell command's state and the extension point of `Chat.send` (a leaf package other plugins
// import for the contracts and their schemas).
package contract

import (
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/schema"
)

func must(contract *contracts.Contract, err error) *contracts.Contract {
	if err != nil {
		panic(err)
	}
	return contract
}

var (
	c       = p2runtime.Contract
	str     = schema.Str
	num     = schema.Num
	nul     = schema.Nul
	boolean = schema.Bool
	obj     = func(properties schema.Properties) schema.Schema { return schema.Obj(properties) }
	prop    = schema.Prop
	opt     = schema.Opt
	arr     = func(item schema.Schema, options ...schema.ArrayOption) schema.Schema {
		return schema.Arr(item, options...)
	}
	union   = func(branches ...schema.Schema) schema.Schema { return schema.Union(branches) }
	literal = func(value string) schema.Schema { return p2runtime.Literal(value) }
	method  = func(sides ...schema.Schema) *contracts.Method {
		contractSides := make([]contracts.Schema, len(sides))
		for i, side := range sides {
			contractSides[i] = c(side)
		}
		return contracts.NewMethod(contractSides...)
	}
	// resultOnly is upstream's `method(undefined, result)`.
	resultOnly = func(result schema.Schema) *contracts.Method {
		return contracts.NewMethod(contracts.Schema{}, c(result))
	}
)

// ViewSchema is Durable's conversation view as clients hold it: a window over the transcript (the newest 50 to 100
// entries, `before` the entry just before it) and the conversation's documents; null until the watch delivered it.
// Clients render it through `ConversationTranscript`, which pages in what lies before the window or in a gap
// (`Chat.entries`). The schema leaves the shape open (`unsafe`).
var ViewSchema = schema.Unknown()

// StatsSchema is the numbers about the conversation's active context, which the window does not hold; null until
// computed (open: `unsafe`).
var StatsSchema = schema.Unknown()

// AssistantCostSchema is one response's cost, for the cost sheet (`Chat.costs`). CacheHit is absent when the response
// reported no prompt tokens.
var AssistantCostSchema = obj(schema.Properties{
	prop("entryId", str()),
	prop("timestamp", num()),
	prop("cost", num()),
	opt("cacheHit", num()),
	prop("provider", str()),
	prop("model", str()),
	prop("stopReason", str()),
})

// AuthorSchema is who wrote an input.
var AuthorSchema = obj(schema.Properties{
	prop("kind", union(literal("owner"), literal("guest"))),
	prop("id", str()),
	prop("name", str()),
})

// MaxSendText is the longest text of a `Chat.send`.
const MaxSendText = 1_000_000

// SendSchema is what `Chat.send` takes. RequestID is client-chosen: a retried send with the same ID is admitted once
// (scoped to the author). WhenBusy steers the current run (default) or queues a follow-up. Attachments are upload IDs
// (POST /upload) of this session. ReplyTo is the entry of this conversation the message answers; only a router uses it
// (chat-mode, in chat mode), ordinary inputs ignore it.
var SendSchema = obj(schema.Properties{
	prop("text", str(schema.MaxLength(MaxSendText))),
	prop("requestId", str(schema.MinLength(1), schema.MaxLength(200))),
	opt("whenBusy", union(literal("steer"), literal("followUp"))),
	opt("attachments", arr(str(), schema.MaxItems(p2runtime.MaxAttachments))),
	opt("replyTo", schema.Int(schema.Minimum(1))),
})

// ConfigureSchema is what `Chat.configure` takes: a known thinking level (version 2).
var ConfigureSchema = obj(schema.Properties{
	opt("model", p2runtime.ModelRefSchema),
	opt("thinkingLevel", p2runtime.ThinkingLevelSchema),
})

// conversationConfigureSchema is the worker-internal form (`Conversation.configure`): Chat validated it already.
var conversationConfigureSchema = obj(schema.Properties{
	opt("model", p2runtime.ModelRefSchema),
	opt("thinkingLevel", str()),
})

var restoredSchema = arr(obj(schema.Properties{prop("author", AuthorSchema), prop("text", str())}))

// LiveShellSchema is a user shell command (`!cmd`/`!!cmd`) while it runs and until its `p2.shell` entry is placed.
// Output is the tail of its output (the shell limits of runtime/shell.go).
var LiveShellSchema = obj(schema.Properties{
	prop("id", str()),
	prop("command", str()),
	prop("author", AuthorSchema),
	prop("includeInContext", boolean()),
	prop("output", str()),
	prop("truncated", boolean()),
	prop("running", boolean()),
	opt("exitCode", num()),
	prop("cancelled", boolean()),
	opt("error", str()),
})

// LiveShell is a running or just finished user shell command (LiveShellSchema).
type LiveShell struct {
	ID               string
	Command          string
	Author           p2runtime.Author
	IncludeInContext bool
	Output           string
	Truncated        bool
	Running          bool
	ExitCode         *float64
	Cancelled        bool
	Error            *string
}

// ShellStateSchema is the conversation's user shell command; null when none.
var ShellStateSchema = union(LiveShellSchema, nul())

// ShellBusy is pi's text when a second command is started while one runs (interactive-mode `isBashRunning`).
const ShellBusy = "A bash command is already running. Press Esc to cancel it first."

// ShellSchema is what `Chat.shell` takes. IncludeInContext: `!` (true) joins the output to the agent's context, `!!`
// (false) displays only. RequestID is client-chosen, the command's `shellId` (`liveShell.id`); a retried call with the
// same ID runs once (per author).
var ShellSchema = obj(schema.Properties{
	prop("command", str(schema.MinLength(1), schema.MaxLength(100_000))),
	prop("includeInContext", boolean()),
	prop("requestId", str(schema.MinLength(1), schema.MaxLength(200))),
})

// ModelMetaSchema is the conversation's current model as anyone who may view it sees it (guest-safe: no credentials,
// no catalog): what a footer needs for the context percentage and a picker for the thinking levels. Null (ModelState)
// without a model, or when the model is not in the server's catalog.
var ModelMetaSchema = obj(schema.Properties{
	prop("provider", str()),
	prop("modelId", str()),
	prop("name", str()),
	prop("contextWindow", num()),
	prop("reasoning", boolean()),
	prop("thinkingLevels", arr(str())),
})

// ModelMeta is a conversation's model as viewers see it (ModelMetaSchema).
type ModelMeta struct {
	Provider       string
	ModelID        string
	Name           string
	ContextWindow  float64
	Reasoning      bool
	ThinkingLevels []string
}

var modelStateSchema = union(ModelMetaSchema, nul())

// CompactSchema is what `Chat.compact` takes: extra instructions for the summary (`/compact <instructions>`).
var CompactSchema = obj(schema.Properties{opt("instructions", str(schema.MaxLength(10_000)))})

// ResetSchema is what `Chat.reset` takes: the text the new context starts with (a handoff note), as a user message.
var ResetSchema = obj(schema.Properties{opt("handoff", str(schema.MaxLength(100_000)))})

// DequeueOwnResultSchema is `dequeueOwn`: the caller's queued texts in submission order, and how many of theirs raced
// (placed or withdrawn).
var DequeueOwnResultSchema = obj(schema.Properties{
	prop("restored", arr(str())),
	prop("skipped", schema.Int(schema.Minimum(0))),
})

// SubmitSchema is an input as `Conversation.submit` takes it: the text the model reads, who wrote it, and its resolved
// attachments. It is placed as its own entry: `entry`, or by default a `p2.input` entry whose data is the author, the
// text and the attachments (runtime/input.go). The model message also starts with the author's sender block. Files
// are listed by path, images also inline when the model accepts them; at most MaxImages of them (the last ones of
// `files`; default: all that qualify) are inline.
var SubmitSchema = obj(append(append(schema.Properties{}, p2runtime.PropertiesOf(SendSchema)...),
	prop("author", AuthorSchema),
	opt("entry", obj(schema.Properties{prop("kind", str(schema.MinLength(1))), prop("data", schema.Unknown())})),
	opt("files", arr(p2runtime.AttachmentSchema)),
	opt("maxImages", schema.Int(schema.Minimum(0))),
))

var submissionResult = obj(schema.Properties{prop("submissionId", num())})
var restoredResult = obj(schema.Properties{prop("restored", restoredSchema)})

// Conversation is one Durable conversation of this worker (`p2.conversation`; host-internal, shared by every viewer):
// its view mirrored from a Durable watch, and the operations that need an author, which callers pass explicitly. 2:
// user shell commands moved to `ShellCommands` (chat/shell.go).
var Conversation = must(contracts.Local("p2.conversation", contracts.LocalDeclaration{
	Version: 2,
	Params:  contracts.Params{contracts.Prop("conversation", contracts.Int())},
	Members: []contracts.LocalEntry{
		// The view's window, as every viewer holds it.
		contracts.Mem("view", c(ViewSchema)),
		// The active context's numbers.
		contracts.Mem("stats", c(StatsSchema)),
		// Each response of the active range with its cost.
		contracts.Mem("costs", resultOnly(arr(AssistantCostSchema))),
		contracts.Mem("submit", method(SubmitSchema, submissionResult)),
		// Appends a display-only entry (no model message) through a Durable write submission: placed at once when
		// idle, at the run's next boundary when busy. A retried `requestId` is admitted once per author.
		contracts.Mem("write", method(obj(schema.Properties{
			prop("kind", str(schema.MinLength(1))),
			prop("data", schema.Unknown()),
			prop("requestId", str(schema.MinLength(1), schema.MaxLength(200))),
			prop("author", AuthorSchema),
		}), submissionResult)),
		contracts.Mem("stop", resultOnly(restoredResult)),
		contracts.Mem("dequeue", method(obj(schema.Properties{prop("submissionId", num()), prop("restore", boolean())}), restoredResult)),
		contracts.Mem("entries", method(p2runtime.EntriesQuerySchema, p2runtime.EntriesPageSchema)),
		contracts.Mem("configure", method(conversationConfigureSchema)),
		// The current model's metadata (see `ModelMetaSchema`), following the view's `pi.agent` and the catalog.
		contracts.Mem("model", c(modelStateSchema)),
		// Admits a manual compaction task (Durable `Conversation.compact`).
		contracts.Mem("compact", method(CompactSchema, obj(schema.Properties{prop("taskId", num())}))),
		// Starts a new context (Durable `Conversation.reset`), placed at the next boundary while busy.
		contracts.Mem("reset", method(ResetSchema)),
		// Aborts this conversation's live compaction tasks; returns how many were marked.
		contracts.Mem("abortCompaction", resultOnly(obj(schema.Properties{prop("aborted", schema.Int(schema.Minimum(0)))}))),
		// Withdraws `authorId`'s queued inputs (by the server-stamped author) in submission order.
		contracts.Mem("dequeueOwn", method(obj(schema.Properties{prop("authorId", str())}), obj(schema.Properties{
			prop("restored", restoredSchema), prop("skipped", schema.Int(schema.Minimum(0))),
		}))),
	},
}))

func viewState() []contracts.Property {
	return []contracts.Property{
		p2runtime.StateProp("view", ViewSchema),
		p2runtime.StateProp("stats", StatsSchema),
		p2runtime.StateProp("model", modelStateSchema),
		p2runtime.StateProp("liveShell", ShellStateSchema),
	}
}

func entriesMethod() contracts.MethodEntry {
	return contracts.Meth("entries", method(p2runtime.EntriesQuerySchema, p2runtime.EntriesPageSchema))
}

// Chat is a conversation as a principal sees it (`p2.chat`): viewers read, participants also send, stop and configure
// (owner), compact, abort compaction, restore their own queued inputs and (owner) reset the context.
//
// 2: `configure` takes only known thinking levels; `send` refuses blank text without attachments. 3: `model` state;
// `compact`, `abortCompaction`, `reset` and `dequeueOwn`. 4: `liveShell` state; `shell` and `abortShell` (user shell
// commands, `!cmd`/`!!cmd`). 5: `view` holds the newest entries only. 6: `view` is Durable's window over the transcript
// (`before`); `stats` state; `entries` replaces `history`.
var Chat = must(contracts.Remote("p2.chat", contracts.RemoteDeclaration{
	Version: 6,
	Params: contracts.Params{
		contracts.Prop("session", contracts.Str()), contracts.Prop("conversation", contracts.Int()),
	},
	HostAddress: p2runtime.SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("viewer", contracts.VariantDeclaration{State: viewState(), Methods: []contracts.MethodEntry{entriesMethod()}}),
		contracts.Var("participant", contracts.VariantDeclaration{State: viewState(), Methods: []contracts.MethodEntry{
			entriesMethod(),
			contracts.Meth("send", method(SendSchema, submissionResult)),
			// Aborts the current run; queued inputs are withdrawn and the caller's own come back as text.
			contracts.Meth("stop", resultOnly(obj(schema.Properties{prop("restored", arr(str()))}))),
			// Withdraws a queued input; with `restore`, returns its text if the caller wrote it.
			contracts.Meth("dequeue", method(
				obj(schema.Properties{prop("submissionId", num()), opt("restore", boolean())}),
				obj(schema.Properties{prop("text", union(str(), nul()))}),
			)),
			contracts.Meth("configure", method(ConfigureSchema)),
			// Summarizes earlier context now (pi's `/compact [instructions]`); returns the compaction task.
			contracts.Meth("compact", method(CompactSchema, obj(schema.Properties{prop("taskId", num())}))),
			// Aborts running compactions (pi: Escape while compacting); returns how many were marked.
			contracts.Meth("abortCompaction", resultOnly(obj(schema.Properties{prop("aborted", schema.Int(schema.Minimum(0)))}))),
			// Starts a new context with an optional handoff (owner only: it changes the context for everyone).
			contracts.Meth("reset", method(ResetSchema)),
			// Withdraws the caller's own queued inputs (by the server-stamped author, in submission order) and returns
			// their texts; other authors' stay queued. Inputs placed or withdrawn concurrently are counted as skipped.
			contracts.Meth("dequeueOwn", resultOnly(DequeueOwnResultSchema)),
			// Runs a shell command in this conversation's working directory on the session's environment (pi's `!cmd`;
			// `!!cmd` without context). It never starts a run: the finished command becomes a `p2.shell` entry. One
			// command at a time per conversation.
			contracts.Meth("shell", method(ShellSchema, obj(schema.Properties{prop("shellId", str())}))),
			// Cancels the running command (its author or the owner).
			contracts.Meth("abortShell", method(obj(schema.Properties{prop("shellId", str())}), obj(schema.Properties{prop("aborted", boolean())}))),
		}}),
	},
}))

// ChatCosts is each response of a conversation's active range with its cost, for whoever may view it (the cost sheet:
// a client's view holds only a window).
var ChatCosts = must(contracts.Remote("p2.chat-costs", contracts.RemoteDeclaration{
	Version: 1,
	Params: contracts.Params{
		contracts.Prop("session", contracts.Str()), contracts.Prop("conversation", contracts.Int()),
	},
	HostAddress: p2runtime.SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("viewer", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			contracts.Meth("costs", resultOnly(arr(AssistantCostSchema))),
		}}),
	},
}))

// RoutedSend is what a participant sent, with the server-stamped author, as `Chat.send` hands it to a router.
type RoutedSend struct {
	Text         string
	RequestID    string
	WhenBusy     string // "", "steer" or "followUp"
	ReplyTo      *int
	Conversation int
	Author       p2runtime.Author
	Attachments  []string
}

// SendRouter takes a message (returning its submission ID) or leaves it (nil).
type SendRouter func(ctx host.Ctx, input RoutedSend) (*float64, error)

// SendRouters is the worker extension point of `Chat.send`: each router may take a message or leave it. Untaken
// messages become ordinary inputs. Example: chat-mode writes chat entries.
var SendRouters = p2runtime.NewKeyedRegistry[SendRouter]("p2.chat-send-routers")

// EntryDisplay is what a plugin adds to the display of its entry kind's messages: the message it answers, and a short
// note (the browser's `TranscriptEntries`, keyed `p2.chat-transcript-entries`, is the web client's).
type EntryDisplay struct {
	ReplyTo *EntryReply
	Note    string
}

// EntryReply is the message an entry answers.
type EntryReply struct {
	EntryID float64
	Name    string
	Excerpt string
}
