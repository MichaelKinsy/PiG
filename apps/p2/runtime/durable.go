package runtime

import (
	"context"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
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

// GuardTool is guardTool for the plugins' tests (Pi exports it): tool as Durable runs it.
func GuardTool(tool P2Tool) *durable.ToolRegistration { return guardTool(tool) }

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
	r := &durableRegistry{extensions: extensions, switches: switches, snapshots: map[*KeyedTable[*P2Extension]]map[string]durable.RegistrySnapshot{}}
	// Primed here, before any scheduler holds its lock: the scheduler reads Snapshot under it, and a read of a registry
	// value is a section of the host's loop, whose commits' publications wait for that lock.
	r.composition.Store(r.read())
	return r
}

type durableRegistry struct {
	extensions, switches *registries.RegistryValueSource

	// composition is the registries' last composition seen: both values and the switched-off names. Snapshot reads it
	// without the host's loop, which a section may hold while it waits for the scheduler's lock (Snapshot is called
	// under that lock). Pi reads the registries' values where it takes the snapshot (`extensions.value`), which is the
	// composition of the last batch that composed: here that batch records it (Subscribe's observers run inside it), so a
	// Snapshot taken after the batch, by anyone who saw its effect, reads the composition it made.
	composition atomic.Pointer[composed]
	// writing orders the writes of composition (an observer in its batch, a subscription's refresh) and counts the
	// compositions observed of each registry: generation.
	writing    sync.Mutex
	generation [2]uint64

	mu        sync.Mutex
	snapshots map[*KeyedTable[*P2Extension]]map[string]durable.RegistrySnapshot
}

// composed is what the registries composed: the extensions, and the names of those switched off.
type composed struct {
	table *KeyedTable[*P2Extension]
	off   []string
	key   string
}

// read is the registries' values now, a section of the host's loop.
func (r *durableRegistry) read() *composed {
	table, _ := r.extensions.Value().(*KeyedTable[*P2Extension])
	off := disabledIn(r.switches.Value())
	return &composed{table: table, off: off, key: strings.Join(off, "\n")}
}

// disabledIn is the names switched off in the switches' composition: sorted.
func disabledIn(value any) []string {
	table, ok := value.(*KeyedTable[*bool])
	var off []string
	if !ok {
		return off
	}
	for _, name := range table.Keys() {
		if on, _ := table.Get(name); on != nil && !*on {
			off = append(off, name)
		}
	}
	sort.Strings(off)
	return off
}

// compositionOf names the registry a composition is of.
type compositionOf int

const (
	ofExtensions compositionOf = iota
	ofSwitches
)

// observed records a composition of one registry, inside the batch that made it: update makes the composition from the
// last one.
func (r *durableRegistry) observed(of compositionOf, update func(last *composed) *composed) {
	r.writing.Lock()
	defer r.writing.Unlock()
	r.generation[of]++
	r.composition.Store(update(r.composition.Load()))
}

// refresh records the registries' values now.
func (r *durableRegistry) refresh() { r.refreshFrom(r.read) }

// refreshFrom records what read returns for each registry that did not compose while it read: a composition observed
// meanwhile is newer than the read's. The read sees the last batch that composed before it, so a composition after it is
// observed. Each registry is judged apart: the switches composing meanwhile do not drop the extensions the read saw.
func (r *durableRegistry) refreshFrom(read func() *composed) {
	r.writing.Lock()
	seen := r.generation
	r.writing.Unlock()
	now := read()
	r.writing.Lock()
	defer r.writing.Unlock()
	next := *r.composition.Load()
	if r.generation[ofExtensions] == seen[ofExtensions] {
		next.table = now.table
	}
	if r.generation[ofSwitches] == seen[ofSwitches] {
		next.off, next.key = now.off, now.key
	}
	r.composition.Store(&next)
}

func (r *durableRegistry) Snapshot() durable.RegistrySnapshot {
	seen := r.composition.Load()
	table, off, key := seen.table, seen.off, seen.key
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

// Subscribe calls listener at each composition of either registry, inside the batch that composed (a failed composition
// keeps the last snapshot: nothing to reconsider), after the Snapshot reads it. As Pi's observe, it starts at the value
// the registries have and calls for later compositions only. It panics with the error Observe returns, where Pi's observe
// throws: a value with no composition to start at.
func (r *durableRegistry) Subscribe(listener func()) func() {
	ctx := chordctx.Background()
	extensions, err := r.extensions.Observe(&compositionObserver{record: func(value any) {
		table, _ := value.(*KeyedTable[*P2Extension])
		r.observed(ofExtensions, func(last *composed) *composed { return &composed{table: table, off: last.off, key: last.key} })
	}, wake: listener}, ctx)
	if err != nil {
		panic(err)
	}
	switches, err := r.switches.Observe(&compositionObserver{record: func(value any) {
		off := disabledIn(value)
		r.observed(ofSwitches, func(last *composed) *composed {
			return &composed{table: last.table, off: off, key: strings.Join(off, "\n")}
		})
	}, wake: listener}, ctx)
	if err != nil {
		extensions.Detach()
		panic(err)
	}
	// What composed since the registry was made, before the observers were attached.
	r.refresh()
	return func() {
		extensions.Detach()
		switches.Detach()
	}
}

// compositionObserver records each composed value of a registry and wakes its listener. It is used as a pointer: the source
// finds an observer to detach by comparing interfaces, which panics on a struct holding a func.
type compositionObserver struct {
	record func(value any)
	wake   func()
}

func (observer *compositionObserver) Advance(call chord.NativeWatchCall[any], _ context.Context) {
	if call.Kind != chord.WatchCallValue {
		return
	}
	observer.record(call.Value)
	observer.wake()
}

func (*compositionObserver) End(chord.WatchEnd) {}

var _ state.NativeObserver[any] = (*compositionObserver)(nil)
