package pico3

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
)

// ConfigKey declares one configuration key and its default. Optional keys
// have no default (upstream `undefined`); only the built-in model is optional.
type ConfigKey struct {
	Key      string
	Default  JsonValue
	Optional bool
}

// KindConfig is a kind's configuration declaration, split by document. Keys
// across all registered kinds must be disjoint.
type KindConfig struct {
	Rewindable []ConfigKey
	Sticky     []ConfigKey
}

// forDoc returns the declarations for "rewindable" or "sticky".
func (config *KindConfig) forDoc(doc string) []ConfigKey {
	if config == nil {
		return nil
	}
	if doc == DocRewindable {
		return config.Rewindable
	}
	return config.Sticky
}

// declared finds key in the declarations for doc.
func (config *KindConfig) declared(doc, key string) (ConfigKey, bool) {
	for _, declaration := range config.forDoc(doc) {
		if declaration.Key == key {
			return declaration, true
		}
	}
	return ConfigKey{}, false
}

// Transition is the result of a Step's Build function: a next checkpoint, a
// terminal completion, or a retry of the whole handler.
type Transition struct {
	Checkpoint Checkpoint
	Completion *Completion
	Retry      bool
}

// Closure runs on the Session line and finishes a task.
type Closure func(ctx context.Context, tx *Tx, current Task) (Completion, error)

// AbortClosure runs on the Session line and returns the aborted result.
type AbortClosure func(ctx context.Context, tx *Tx, current Task) (JsonValue, error)

// Step is what a phase handler returns: advance to Next (or the checkpoint,
// completion, or retry chosen by Build on the line), or finish with Done.
type Step struct {
	Next  Checkpoint
	Build func(ctx context.Context, tx *Tx, current Task) (Transition, error)
	Done  Closure
}

// PhaseHandler runs one phase of a task off the Session line.
type PhaseHandler func(ctx context.Context, task Task, rt *Runtime) (Step, error)

// Kind is a task kind. Initial runs when the task has no checkpoint; Phases
// covers every checkpoint phase. Phases named in Inflight are written by a
// handler immediately before an external effect and are entered only after
// reopen; a Next transition into one is a contract fault. Kind identity is
// pointer identity: a copy is a different, unregistered token.
type Kind struct {
	Name string
	// Turn kinds make the conversation busy for admission.
	Turn   bool
	Config *KindConfig
	// Slot initializes the live slot (sticky.tasks[id]).
	Slot func(input JsonValue) JsonObject
	// Describe renders a non-turn task for the view; slot is nil when absent.
	Describe func(task Task, slot JsonObject) JsonValue
	Inflight []string
	Initial  PhaseHandler
	Phases   map[string]PhaseHandler
	Abort    func(ctx context.Context, task Task, rt *Runtime) (AbortClosure, error)
}

// DefineTask authors an ordinary kind; names may not begin with "pi.".
func DefineTask(definition Kind) (*Kind, error) {
	if strings.HasPrefix(definition.Name, "pi.") {
		return nil, fmt.Errorf(`task kind names beginning with "pi." are reserved: %s`, definition.Name)
	}
	kind := definition
	return &kind, nil
}

func (kind *Kind) isInflight(phase string) bool {
	return slices.Contains(kind.Inflight, phase)
}

// TaskRef is a typed reference to a created task.
type TaskRef struct {
	Id   Id
	Kind *Kind
}

// EntryRef is a typed reference to an appended entry.
type EntryRef struct {
	Id   Id
	Kind *EntryKind
}

// TaskSpec creates a task by kind name (core only).
type TaskSpec struct {
	Kind           string
	ConversationId *Id
	Input          JsonValue
	After          []Id
	Background     bool
}

// TaskOptions are the createTask options for a kind token.
type TaskOptions struct {
	ConversationId *Id
	Background     bool
	After          []Id
}

// HookApi identifies the invocation a hook handler serves.
type HookApi struct {
	Kind           string `json:"kind"`
	TaskId         Id     `json:"taskId"`
	ConversationId Id     `json:"conversationId"`
}

// HookBinding is one registered handler set.
type HookBinding struct {
	Handlers  any
	Namespace *Namespace
	Api       HookApi
}

// HookRunner calls one kind's registered handlers in registration order.
type HookRunner struct {
	handlers func() []HookBinding
	onReport func(error)
}

// Handlers returns the bindings in scope for the invocation.
func (runner HookRunner) Handlers() []HookBinding {
	if runner.handlers == nil {
		return nil
	}
	return runner.handlers()
}

// Each calls fn on each binding. onValue sees non-nil results and may return
// true to stop. A failing handler is reported and skipped unless ctx is done.
func (runner HookRunner) Each(ctx context.Context, fn func(handlers any, api HookApi) (any, error), onValue func(any) bool) error {
	for _, binding := range runner.Handlers() {
		value, err := callHook(binding, fn)
		if err != nil {
			if ctx.Err() != nil {
				return err
			}
			if runner.onReport != nil {
				runner.onReport(err)
			}
			continue
		}
		if value != nil && onValue != nil && onValue(value) {
			return nil
		}
	}
	return nil
}

func callHook(binding HookBinding, fn func(handlers any, api HookApi) (any, error)) (value any, err error) {
	defer recoverInto(&err)
	return fn(binding.Handlers, binding.Api)
}

// Namespace is the current process authority for one durable namespace.
type Namespace struct {
	Id         string
	unregister func()
}

// Unregister removes this registration; it is idempotent and never removes
// a newer registration of the same id.
func (namespace *Namespace) Unregister() {
	if namespace.unregister != nil {
		namespace.unregister()
	}
}

// namespaceRegistration is the erased registration behind a token.
type namespaceRegistration struct {
	token    *Namespace
	defaults map[string]JsonObject
	routes   map[string]string
	order    []string
	project  func(slice JsonObject) (JsonValue, error)
}

// ToolControl lets a tool end the turn, hand off, or add tools.
type ToolControl struct {
	Terminate bool     `json:"terminate,omitempty"`
	Handoff   *string  `json:"handoff,omitempty"`
	AddTools  []string `json:"addTools,omitempty"`
}

// ToolDiagnostic is one diagnostic attached to a tool result.
type ToolDiagnostic struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Code     string `json:"code,omitempty"`
}

// ToolResult is what a tool returns. A nil Content means the tool streamed
// through ToolApi.Stream and the bounded stream is the content; Details nil
// is absent.
type ToolResult struct {
	Content     []ai.ToolResultMessageContent
	IsError     bool
	Details     JsonValue
	Diagnostics []ToolDiagnostic
	Control     *ToolControl
}

// ToolOutput bounds a tool's output.
type ToolOutput struct {
	MaxBytes *int
	MaxLines *int
	// Retain is "head" or "tail"; empty selects the default.
	Retain string
}

// ToolDeclaration declares a tool. Parameters is its JSON Schema; Replay is
// "safe" or "unsafe" (the default). Identity is pointer identity.
type ToolDeclaration struct {
	Name        string
	Description string
	Parameters  JsonObject
	Replay      string
	Output      *ToolOutput
	Execute     func(ctx context.Context, args JsonValue, api *ToolApi) (ToolResult, error)
}

// RequestOptions is one provider request.
type RequestOptions struct {
	Messages      []JsonObject
	ThinkingLevel string
}

// DeferredResult is a deferred poll: a settled message or a new handle.
type DeferredResult struct {
	Message  *ai.AssistantMessage
	Deferred *ai.DeferredHandle
}

// Models resolves and streams models.
type Models interface {
	Resolve(ref ModelRef) *ai.Model
	Stream(ctx context.Context, model *ai.Model, request RequestOptions) iter.Seq2[ai.AssistantMessageEvent, error]
	FetchDeferred(ctx context.Context, model *ai.Model, handle ai.DeferredHandle) (DeferredResult, error)
	CancelDeferred(ctx context.Context, model *ai.Model, handle ai.DeferredHandle) error
}

// ProcessSpec describes one host process.
type ProcessSpec struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Cwd     string            `json:"cwd"`
	Env     map[string]string `json:"env,omitempty"`
}

// ProcessStatus is "running", "exited", or "unknown".
type ProcessStatus struct {
	Status        string
	ExitCode      int
	Stdout        string
	Stderr        string
	DroppedStdout int
	DroppedStderr int
}

// ProcessHost runs keyed processes that outlive one invocation.
type ProcessHost interface {
	Start(ctx context.Context, key string, spec ProcessSpec) error
	Status(ctx context.Context, key string) (ProcessStatus, error)
	Kill(ctx context.Context, key string, signal string) error
}

// PluginHandler runs one pi.plugin task.
type PluginHandler func(ctx context.Context, input JsonValue, api *ToolApi) (JsonValue, error)

// SendInput is one user input.
type SendInput struct {
	// Content is a string or a user content array.
	Content   JsonValue
	RequestId string
	// WhenBusy is "steer", "followUp" (the default), or "reject".
	WhenBusy string
}

// ConversationSpec creates a conversation.
type ConversationSpec struct {
	Parent     *ConversationParentSpec
	Rewindable JsonObject
	Sticky     JsonObject
	Sections   []SectionSeed
}

// ConversationParentSpec forks from an entry, or from "start" when AtStart.
type ConversationParentSpec struct {
	ConversationId Id
	At             Id
	AtStart        bool
}

// OwnedConversationSpec creates a conversation owned by a task.
type OwnedConversationSpec struct {
	Inherit    bool
	Rewindable JsonObject
	Sticky     JsonObject
}

// ContextView is a derived model context.
type ContextView struct {
	Head     *Entry
	Entries  []Entry
	Messages []JsonObject
}

// EntryScan scans entries newest-first, fork-aware.
type EntryScan struct {
	ConversationId Id
	Kind           string
	WithHead       bool
	// Before keeps entries with ids strictly below it when non-nil.
	Before *Id
	Limit  int
}

// TaskScan filters tasks.
type TaskScan struct {
	ConversationId *Id
	Status         []string
	Kind           string
}

// recoverInto converts a panic raised by misuse of a document or transaction
// surface into an error.
func recoverInto(err *error) {
	if recovered := recover(); recovered != nil {
		if recoveredErr, ok := recovered.(error); ok {
			*err = recoveredErr
			return
		}
		*err = fmt.Errorf("%v", recovered)
	}
}

// decodeInto converts a JSON value into a Go value through JSON.
func decodeInto(value JsonValue, target any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}
