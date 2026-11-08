package runtime

import (
	"context"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/toolsearch"
)

// Ports apps/p2/src/runtime/durable.ts (the Harness's registry over the worker's Chord registries of extensions and their
// switches), tool-access.ts (who may make the agent call a tool, and where) and origin.ts (who started a tool's turn).
//
// A p2 tool is a Durable tool plus its ToolAccess (P2Tool, Pi's `ToolRegistration & ToolAccess`), so a tool carries its
// requirements and they cannot drift from it.

// ToolAccess is a tool's requirements; the zero value is `any` for each.
type ToolAccess struct {
	// Origin is whose turn may call it: anyone's ("" or "any"), or only a turn the owner started ("owner", `OriginOf`).
	Origin string
	// Scope is where it works: anywhere ("" or "any"); only in the session's top-level conversations ("main": the main
	// conversation and its `/tree` branches, never a subagent or one-shot agent); or offered only to subagents ("subagent"),
	// which get it without choosing.
	Scope string
	// Locked: in a locked session (`SessionLock`) the lock replaces the origin check of an `origin: "owner"` tool, whoever's
	// turn it is: "pass" ("" , the default: what it reaches is limited by the lock elsewhere, MCP servers by their leases,
	// location tools by the sandbox), "never", or only when the owner picked the capability (a LockCapability).
	Locked string
}

// P2Tool is a tool as p2 plugins contribute it: a Durable tool with its requirements (Pi's `P2Tool`).
type P2Tool struct {
	*durable.ToolRegistration
	ToolAccess
}

// P2Extension is a Durable extension whose tools declare their requirements: Extension holds everything but the tools
// (its own Tools are ignored: the registry replaces them with Tools, guarded), Tools are the extension's tools.
type P2Extension struct {
	Extension *durable.Extension
	Tools     []P2Tool
}

// Extensions are the Durable extensions every conversation of the worker may select, by name (host-lifetime objects
// contribute them). Their tools declare their requirements (ToolAccess).
var Extensions = NewKeyedRegistry[*P2Extension]("p2.durable-extensions")

// ExtensionSwitches are live switches of contributed extensions, by extension name: an extension whose switch reads false
// is left out of Durable's registry, as if uninstalled, until it reads true again (nil: no say). Example: the `codemode`
// setting.
var ExtensionSwitches = NewKeyedRegistry[*bool]("p2.extension-switches")

// codemodeExtension is the code mode plugin's extension: its switch is on while the `codemode` setting is, absent without
// the plugin.
const codemodeExtension = "codemode"

// guardTool is tool as Durable runs it: unchanged without requirements, else refusing calls that do not meet them.
func guardTool(tool P2Tool) *durable.ToolRegistration {
	if tool.Origin != "owner" && tool.Scope != "main" {
		return tool.ToolRegistration
	}
	guarded := *tool.ToolRegistration
	guarded.Execute = func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		if tool.Scope == "main" {
			top, err := IsTopLevel(ctx, api, api.ConversationId())
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if !top {
				return refusal(tool.Name + " works only in the session's main conversation."), nil
			}
		}
		if tool.Origin == "owner" {
			lock, err := LockOf(ctx, api)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if lock != nil {
				rule := tool.Locked
				if rule == "never" {
					return refusal(tool.Name + " is not available in a locked session."), nil
				}
				if rule != "" && rule != "pass" && !slices.Contains(lock.Capabilities, LockCapability(rule)) {
					return refusal(tool.Name + " is not among the resources the owner picked for this session."), nil
				}
			} else {
				origin, err := OriginOf(ctx, api)
				if err != nil {
					return durable.ToolExecutionResult{}, err
				}
				if origin != OriginOwner {
					return refusal(tool.Name + " needs a turn the owner started; guests and what they start cannot use it."), nil
				}
			}
		}
		return tool.Execute(ctx, args, api)
	}
	return &guarded
}

func refusal(text string) durable.ToolExecutionResult {
	isError := true
	return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}, IsError: &isError}
}

// OriginOf is who started the calling tool's turn: the owner, or nobody allowed (""). The turn is the conversation's
// current run, a code mode script's nested calls included: it is the owner's only if every input the run placed so far is
// (EntryOrigin). A guest's message steered into the run takes it away for the rest of the run.
func OriginOf(ctx context.Context, api durable.ToolExecutionApi) (ConversationOrigin, error) {
	live, err := durable.Snapshot(ctx, api, harness.LiveDoc, api.ConversationId())
	if err != nil || live == nil || live.Run == nil || len(live.Run.Inputs) == 0 {
		return "", err
	}
	inputs := live.Run.Inputs
	return durable.Commit(ctx, api, func(tx durable.Tx) (ConversationOrigin, error) {
		placed := 0
		for _, id := range inputs {
			submission, err := tx.Submission(id)
			if err != nil {
				return "", err
			}
			// A continue places no entry: it adds no input, and only inputs grant.
			if submission != nil && submission.Type == durable.SubmissionTypeContinue {
				continue
			}
			var entry *durable.EntryRecord
			if submission != nil && submission.Entry != nil {
				if entry, err = tx.Entry(*submission.Entry); err != nil {
					return "", err
				}
			}
			if EntryOrigin(entry) != OriginOwner {
				return "", nil
			}
			placed++
		}
		if placed > 0 {
			return OriginOwner, nil
		}
		return "", nil
	})
}

// EntryOrigin is the origin an input entry records in its data: a person's input is the owner's when its author is the
// owner; what an agent wrote (a subagent's task or report, a `team` or peer message, a one-shot agent's task) says
// `origin: "owner"` when the turn that wrote it was the owner's. Anything else, a tool's plain input included, grants
// nothing.
func EntryOrigin(entry *durable.EntryRecord) ConversationOrigin {
	if entry == nil {
		return ""
	}
	if person, ok := MessageDataOf(entry.Data); ok {
		if person.Author.Kind == AuthorOwner && person.Author.ID == "owner" {
			return OriginOwner
		}
		return ""
	}
	if object, ok := entry.Data.(interface{ Value(string) any }); ok && object.Value("origin") == "owner" {
		return OriginOwner
	}
	return ""
}

// AnswerOrigin is the origin of the run that ended with the answer `answer` of conversation, read after it ended (a
// subagent's report, when `pi.live.run` is gone): its inputs are the input entries after the previous run's final answer.
// The owner's only if all are. Only a final answer (`stop`, `length`) bounds the scan: a retried model error appends an
// `error` answer and an overflow compaction a head marker inside a run, and scanning past a failed or aborted run only adds
// inputs, so the result can only be stricter.
func AnswerOrigin(tx durable.Tx, conversation durable.ConversationId, answer durable.EntryId) (ConversationOrigin, error) {
	found := false
	var cursor durable.Cursor
	for {
		max := answer - 1
		page, err := tx.ScanEntries(durable.EntryQuery{ConversationId: conversation, MaxEntryId: &max}, 100, cursor)
		if err != nil {
			return "", err
		}
		for i := range page.Items {
			entry := &page.Items[i]
			var message ai.Message
			if len(entry.Model) > 0 {
				message = entry.Model[0]
			}
			if assistant, ok := message.(ai.AssistantMessage); ok && (assistant.StopReason == ai.StopReasonStop || assistant.StopReason == ai.StopReasonLength) {
				if found {
					return OriginOwner, nil
				}
				return "", nil
			}
			// Durable's own entries with user messages (compaction summaries, handoffs) are not inputs.
			if _, ok := message.(ai.UserMessage); !ok || (strings.HasPrefix(entry.Kind, "pi.") && entry.Kind != "pi.user") {
				continue
			}
			if EntryOrigin(entry) != OriginOwner {
				return "", nil
			}
			found = true
		}
		if page.Next == nil {
			if found {
				return OriginOwner, nil
			}
			return "", nil
		}
		cursor = *page.Next
	}
}

// DurableRegistry is Durable's registry over the worker's Extensions and ExtensionSwitches: a snapshot per (extensions,
// switched-off set), made once and kept while the composition lives. Without code mode, deferred tools the model may call
// (MCP tools, the terminal tool) are reachable only through `tool_search`, as pi falls back: it is installed then, once,
// whichever plugins offer such tools. Subscribe observes both registries' compositions (its listener called inside the
// Chord batch that composed, so it only wakes the scheduler).
func DurableRegistry(extensions, switches *registries.RegistryValueSource) harness.RegistryReader {
	return &durableRegistry{extensions: extensions, switches: switches, snapshots: map[*KeyedTable[*P2Extension]]map[string]durable.RegistrySnapshot{}}
}

type durableRegistry struct {
	extensions, switches *registries.RegistryValueSource

	mu        sync.Mutex
	snapshots map[*KeyedTable[*P2Extension]]map[string]durable.RegistrySnapshot
}

func (r *durableRegistry) disabled() ([]string, error) {
	table, ok := r.switches.Value().(*KeyedTable[*bool])
	var off []string
	if !ok {
		return off, nil
	}
	for _, name := range table.Keys() {
		if on, _ := table.Get(name); on != nil && !*on {
			off = append(off, name)
		}
	}
	sort.Strings(off)
	return off, nil
}

func (r *durableRegistry) Snapshot() durable.RegistrySnapshot {
	table, _ := r.extensions.Value().(*KeyedTable[*P2Extension])
	off, _ := r.disabled()
	key := strings.Join(off, "\n")
	r.mu.Lock()
	defer r.mu.Unlock()
	byKey := r.snapshots[table]
	if byKey == nil {
		byKey = map[string]durable.RegistrySnapshot{}
		r.snapshots[table] = byKey
	}
	if snapshot, ok := byKey[key]; ok {
		return snapshot
	}
	builder := harness.CreateRegistry()
	var installed []*P2Extension
	for _, name := range table.Keys() {
		if slices.Contains(off, name) {
			continue
		}
		extension, _ := table.Get(name)
		installed = append(installed, extension)
	}
	deferred := false
	for _, extension := range installed {
		guarded := *extension.Extension
		guarded.Tools = make([]*durable.ToolRegistration, len(extension.Tools))
		for i, tool := range extension.Tools {
			guarded.Tools[i] = guardTool(tool)
			if tool.Deferred && tool.CallableBy(durable.CallerModel) {
				deferred = true
			}
		}
		// An invalid extension publishes nothing: the plugin that contributed it reported it at its own install.
		_ = builder.Install(&guarded)
	}
	_, hasCodemode := table.Get(codemodeExtension)
	search := toolsearch.CreateExtension()
	_, hasSearch := table.Get(search.Name)
	if !(hasCodemode && !slices.Contains(off, codemodeExtension)) && deferred && !hasSearch {
		_ = builder.Install(search)
	}
	snapshot := builder.Snapshot()
	byKey[key] = snapshot
	return snapshot
}

// Subscribe calls listener at each composition of either registry (a failed composition keeps the last snapshot: nothing to
// reconsider).
func (r *durableRegistry) Subscribe(listener func()) func() {
	stops := []func(){
		Follow[any](chord.NativeWatchable[any](r.extensions), func(any) { listener() }),
		Follow[any](chord.NativeWatchable[any](r.switches), func(any) { listener() }),
	}
	return func() {
		for _, stop := range stops {
			stop()
		}
	}
}
