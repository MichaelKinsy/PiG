// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import (
	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/inbox"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// ID is a record ID.
type ID = session.ID

// ModelRef names a model: {provider, modelId}.
type ModelRef struct{ Provider, ModelID string }

// Settings are the resolved Harness settings the built-in tasks read (src/harness/types.ts HarnessSettings after defaults).
type Settings struct {
	RetryEnabled    bool
	MaxRetries      float64
	BaseDelayMs     float64
	MaxAgentDelayMs *float64

	Compaction history.Policy
	// CompactionOverrides is compaction.modelOverrides, keyed by exact "provider/modelId" (Pi c4cf7957); CompactionFor
	// applies it.
	CompactionOverrides map[string]history.PolicyOverride

	// PartialIntervalMs is progress.partialIntervalMs: the throttle of streamed partials committed into pi.live.
	PartialIntervalMs float64
	// OutputIntervalMs is progress.outputIntervalMs: the pace of tool output commits.
	OutputIntervalMs float64

	// Stream is the settings' request options (a JSON object), pinned into the request checkpoint.
	Stream *jv.Object
	// ToolExecution is "parallel" or "sequential".
	ToolExecution string
	Queue         inbox.QueueModes
	// AfterFailedRun is settings.afterFailedRun: "keep", "continue" or "withdraw".
	AfterFailedRun string
}

// Tool is a registered tool of the resolved agent: its transcript declaration, execution mode, replay policy and output limits.
type Tool struct {
	Declaration *jv.Object
	// ExecutionMode is "sequential", "parallel" or "" (the settings decide).
	ExecutionMode string
	// Handler is the index of the tool's execute handler in the registry snapshot.
	Handler int
	// Replay is "safe" or "unsafe"; empty means unsafe.
	Replay string
	// Raw is the host's tool JSON, kept for the tool effect.
	Raw *jv.Object
	// Extension is the last selected extension that provides or wraps the tool (agent.ts toolExtension).
	Extension string
	// Limits are the tool's outputLimits; nil fields default to 50 KiB, 2000 lines and head retention.
	Limits ToolLimits
	// StructuredOutput is whether the tool declares a structured output schema.
	StructuredOutput bool
}

// ToolLimits are the optional output limits of a tool registration.
type ToolLimits struct {
	MaxBytes, MaxLines *int
	Retain             string
}

// ToolRun is one tool execution request: the tool effect (ABI section 6).
type ToolRun struct {
	Task, Conversation ID
	CallID, Name       string
	Arguments          *jv.Object
	// OutputWindow is the retained window for tail retention ({maxBytes, maxLines, minIntervalMs, bytesPerSecond}), else nil.
	OutputWindow *jv.Object
}

// ToolIO is what a running tool reports through its api (tool_progress events). Each function fails once the call has
// settled. Details calls then after the progress commit that includes the value.
type ToolIO struct {
	// Output reports a chunk (text when isString, bytes otherwise) and the output omitted right before it, if any.
	Output     func(chunk []byte, isString bool, skipped *Skip) error
	Diagnostic func(diagnostic *jv.Object) error
	Details    func(value any, then func(error))
}

// Section is a resolved prompt section of the agent: its key, whether its text is wrapped as <key>…</key>, and the index of
// its render handler in the registry snapshot.
type Section struct {
	Key     string
	Tag     bool
	Handler int
	// Extension is the last selected extension that provides or wraps the section (agent.ts sectionExtension); empty when
	// the agent's instructions render it.
	Extension string
}

// Agent is the resolved agent of a conversation (agent.ts resolution): its model, thinking level, prompt sections and tools.
type Agent struct {
	Model         *ModelRef
	ThinkingLevel string
	// Sections are the extension sections, then the agent's instructions, in render order.
	Sections []Section
	Tools    []Tool
	// Callable are the tools a running tool's nested calls (executeTool) resolve among: enabled and callable by tools.
	Callable []Tool
	// Instructions and Cwd are the agent's optional base instructions and working directory (the render input).
	Instructions, Cwd string
	// HookHandlers are the hook handler tables of the selected extensions in extension order: hook name to handler index
	// (agent.ts agentHooks).
	HookHandlers []map[string]int
	// HookExtensions names the extension of each table of HookHandlers.
	HookExtensions []string
}

// Declarations returns the tool declarations in order.
func (a *Agent) Declarations() []*jv.Object {
	out := make([]*jv.Object, len(a.Tools))
	for i, t := range a.Tools {
		out[i] = t.Declaration
	}
	return out
}

// StreamRequest is one model request: the model_context effect (ABI section 6). Context is the committed context through the
// cutoff; Replaced, when non-nil, replaces the context's messages after a beforeRequest hook.
type StreamRequest struct {
	// Conversation is the conversation the request runs in: the host keeps its newest message list for the delta form.
	Conversation ID
	Model        ModelRef
	Context      RequestContext
	Replaced     []*jv.Object
	// Options are the request options without the signal: the pinned stream options, the provider session ID and the reasoning level.
	Options *jv.Object
}

// CompleteRequest is a non-streaming model request (completeSimple): the compaction summarizer.
type CompleteRequest struct {
	Model ModelRef
	// Context is the pi-ai Context ({messages}); Options the request options without the signal.
	Context, Options *jv.Object
}

// KeptRange is the head marker entry (0: none) and newest entry of a kept context range; Ok is false when none is kept.
type KeptRange struct {
	Head, Tail ID
	Ok         bool
}

// Host is what the built-in tasks need of the surrounding Harness: the clock, settings, agent and model resolution,
// committed context, and the effects a task starts (model request, prompt sections, hooks, deferred responses, timers).
// The Session handler implements it by emitting ABI effects and resolving the continuations when their completions
// arrive; tests implement it with a script. Every continuation runs on the session Loop's turn, never re-entrantly.
type Host struct {
	Now      func() float64
	Settings func() Settings
	// Agent resolves the agent of a conversation (runtime.agent: the host resolves it and may call extension code, so it is
	// asynchronous). The resolution is fixed for the phase that asked.
	Agent func(conversation ID, cancel *session.Cancel, then func(*Agent, error))
	// ContextWindow returns the context window of a model; ok is false when the model is not available.
	ContextWindow func(model ModelRef) (window float64, ok bool)
	// KeptRange is the range the scheduler keeps for the conversation (scheduler.ts #contexts), read when runtime.context is
	// called: readContextFrom receives it as an argument, so a keep that lands later does not change that read.
	KeptRange func(conversation ID) KeptRange
	// Context derives the committed context of a conversation as of entry at (0: the tail), given the range KeptRange
	// returned when the read was called. It runs inside the bounds job on the Session line and returns the hops from the end of
	// that job until the caller resumes (a scan off the line and its return, or the return alone when the kept range serves the
	// read: context.ts readContextFrom :75-102) and keep, which the caller runs once the scan has
	// finished, before the read resolves: the scheduler keeps the range only after readContextFrom returns
	// (scheduler.ts:1231-1252), so a read whose bounds job runs while another's scan is in flight scans too. keep takes
	// whether the invocation has ended and may be nil.
	Context func(conversation, at ID, kept KeptRange) (view *history.View, scan int, keep func(ended bool) error, err error)
	// RequestContext is Context for a model request: the context the request is prepared from, which the Host may take from
	// the host that stores it instead of deriving it (compaction and overflow handling still need the view). It has the
	// hops, keep and kept-range contract of Context.
	RequestContext func(conversation, at ID, kept KeptRange) (ctx RequestContext, scan int, keep func(ended bool) error, err error)
	// Render renders the agent's prompt sections in order for one request preparation (renderSections, with the
	// environment). shown are the sections the context currently shows; a section that throws keeps its shown text.
	// task is the generation task, whose environment the sections see (runtime.env, built before the first section).
	Render func(conversation, task ID, agent *Agent, shown *Sections, cancel *session.Cancel, then func(desired *Sections, err error))
	// Stream runs the model request. onPartial receives the text of each partial assistant message, a JSON object, which the
	// request parses only when it commits it; then receives the terminal message.
	Stream func(req *StreamRequest, onPartial func(partial jv.Raw), cancel *session.Cancel, then func(message *jv.Object, err error))
	// PrepareArguments applies the tool's prepareArguments to the call's arguments: the repaired arguments, any JSON value
	// (harness/tool.ts prepare casts the result, it does not check it), or errText when the repair throws.
	PrepareArguments func(tool string, args *jv.Object, then func(args any, errText string, err error))
	// ValidateArguments validates and coerces arguments, any JSON value, against the tool's schema (validateToolArguments):
	// the coerced arguments, or errText describing why they are invalid.
	ValidateArguments func(tool string, call *jv.Object, args any, then func(args *jv.Object, errText string, err error))
	// CheckStructuredOutput asks the host whether a settled result's structured output keeps the tool's contract
	// (structuredOutputError): errText is why it does not, empty when it does. The schema lives in the host's registration.
	CheckStructuredOutput func(tool string, result *jv.Object, then func(errText string, err error))
	// RunTool builds the call's environment and runs the tool's execute: the tool effect. then receives the tool's result
	// object ({content?, details?, isError?, diagnostics?, usage?, control?}), or the error execute (or the environment) threw.
	RunTool func(run *ToolRun, io *ToolIO, cancel *session.Cancel, then func(result *jv.Object, err error))
	// MaxTokens is the model's own output token limit (0: none); ok is false when the model is not available.
	MaxTokens func(model ModelRef) float64
	// Complete runs one non-streaming model request and delivers the terminal assistant message.
	Complete func(req *CompleteRequest, cancel *session.Cancel, then func(message *jv.Object, err error))
	// Hooks returns the number of handlers the conversation's agent selects for a generation, tool or compaction hook.
	Hooks func(name string, conversation ID) int
	// HookExtension is the name of the extension of handler index of the hook name (agent.ts agentHooks).
	HookExtension func(name string, index int, conversation ID) string
	// Hook calls handler index of the hook name. The payload is the hook's first argument; the result is its return value (nil for none).
	Hook func(name string, index int, conversation, task ID, payload *jv.Object, then func(result any, err error))
	// FetchDeferred polls a provider-side deferred response; CancelDeferred abandons it.
	FetchDeferred  func(model ModelRef, handle *jv.Object, cancel *session.Cancel, then func(message *jv.Object, err error))
	CancelDeferred func(model ModelRef, handle *jv.Object, cancel *session.Cancel, then func(err error))
	// Snapshot reads the committed value of a conversation document, or nil when it does not exist.
	Snapshot func(def *session.DocDef, conversation ID) (*jv.Object, error)
	// SnapshotLoaded reports whether Snapshot is a cache hit: Pi's Session.snapshot takes a line job only on a miss.
	SnapshotLoaded func(def *session.DocDef, conversation ID) bool
	// Entry reads one committed entry record, or nil.
	Entry func(id ID) (*jv.Object, error)
	// AddTools adds tools to the agent's loadout in this commit (agent.ts addTools), for a tool result's addTools control.
	AddTools func(tx *session.Tx, conversation ID, added []any) error
	// After arms a volatile timer; the returned function cancels it.
	After func(ms float64, fire func()) (cancel func())
	// NewSessionID returns a fresh provider session ID (uuidv7).
	NewSessionID func() string
	// Hops runs then after n microtask hops of the shared host/core queue (ABI section 5.3, a hops effect), so a Pi await
	// chain of n hops lets host work queued meanwhile run first. n <= 0 runs then as the next microtask.
	Hops func(n int, then func())

	// ScanHops is Hops for the wait on a context range scan (context.ts scanRange awaits Storage.scanEntries): n is the scan's ticks
	// with the return to the caller, and a Host whose Storage queues statements runs the scan's statements through that queue.
	// A Host without it waits with Hops.
	ScanHops func(n int, then func())
}

// ResultFault is the error RunTool answers with when the tool returned a value the host could not hand over as strict
// JSON. The call faults: there is no result to record.
type ResultFault struct{ Text string }

func (e *ResultFault) Error() string { return e.Text }

// CompactionFor is compaction.ts compactionPolicy: the model's modelOverrides entry over the Harness-wide policy, field by
// field.
func (s Settings) CompactionFor(model ModelRef) history.Policy {
	policy := s.Compaction
	override, ok := s.CompactionOverrides[model.Provider+"/"+model.ModelID]
	if !ok {
		return policy
	}
	if override.Enabled != nil {
		policy.Enabled = *override.Enabled
	}
	if override.ReserveTokens != nil {
		policy.ReserveTokens = *override.ReserveTokens
	}
	if override.KeepRecentTokens != nil {
		policy.KeepRecentTokens = *override.KeepRecentTokens
	}
	if override.BackgroundTokens != nil {
		policy.BackgroundTokens = *override.BackgroundTokens
	}
	return policy
}
