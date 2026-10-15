package codemode

import (
	"context"
	"encoding/json"
	"maps"
	"slices"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Tool is a host function a script can call.
//
// Ports packages/codemode/src/types.ts (CodemodeTool). Go mechanics, not divergences: the arguments and the result
// are JSON text, the form in which they cross the VM boundary (a nil json.RawMessage is `undefined`), and the
// upstream ToolContext.signal is the ctx passed to Execute, cancelled when the script finishes, times out, is
// aborted or the sandbox closes.
type Tool struct {
	// Name is the tool's name. Scripts call tools as tools.<ToCodemodeIdentifier(Name)>(args) and tools["<Name>"](args).
	// A global is called as <Name>(args) and must be an identifier or <namespace>.<member>.
	Name string
	// Description is a doc comment in RenderDeclarations and the entry text in ALL_TOOLS. Empty means none.
	Description string
	// InputSchema is the JSON Schema of the single argument, used only to render declarations. Nil means unknown.
	InputSchema CodemodeJsonSchema
	// OutputSchema is the JSON Schema of the resolved value, used only to render declarations. Nil means unknown.
	OutputSchema CodemodeJsonSchema
	// Spread makes a global receive all call arguments as an array instead of the first one.
	Spread bool
	// Signature is a TypeScript parameter list and return type that replaces the rendering from the schemas
	// (globals only). Nil means none; a non-nil empty string renders as Pi does, `declare function name;`.
	Signature *string
	// AwaitsInitiation makes the script's next tool call wait until Execute has initiated this one: until it calls
	// CallInitiated with its context, or returns. Pi runs each call's synchronous prefix in call order, so two calls a script
	// starts together reach a server in script order; a Go goroutine per call does not, and a tool whose prefix must keep the
	// order (a request to a server) sets this and calls CallInitiated once the order is fixed.
	AwaitsInitiation bool
	// Execute runs the tool. args is what the script passed, after a JSON round trip.
	Execute func(ctx context.Context, args json.RawMessage) (json.RawMessage, error)
}

// OutputItemType is the closed `type` union of an output item (types.ts CodemodeOutputItem).
type OutputItemType string

// The output item types.
const (
	OutputItemText  OutputItemType = "text"
	OutputItemImage OutputItemType = "image"
)

// OutputItem is one item of the script's output, in the order the script produced it: text() and console.* produce
// "text" items, with Console set for console.*, and image() "image" items. Data is base64.
type OutputItem struct {
	Type OutputItemType `json:"type"`
	Text string         `json:"text,omitempty"`
	// Console marks a text item produced by console.*, so hosts can tell it apart from text() output.
	Console  bool   `json:"console,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// CallStatus is the outcome of one nested tool call.
type CallStatus string

// The nested call outcomes.
const (
	CallOK        CallStatus = "ok"
	CallError     CallStatus = "error"
	CallCancelled CallStatus = "cancelled"
)

// Call records one tool call the script made (globals are not recorded).
type Call struct {
	Name       string
	Status     CallStatus
	DurationMs float64
}

// ErrorKind classifies a failed execution.
type ErrorKind string

// The failure kinds.
const (
	// ErrorScript: the script threw or failed to parse; Name and Stack come from the script's error.
	ErrorScript ErrorKind = "script"
	// ErrorTimeout: the overall deadline expired. The execution ended and the VM was asked to stop; a worker VM may still
	// be computing.
	ErrorTimeout ErrorKind = "timeout"
	// ErrorAborted: the caller's context ended or the sandbox was closed. The execution ended and the VM was asked to stop.
	ErrorAborted ErrorKind = "aborted"
	// ErrorSandbox: the VM failed outside the script's control (a wasm trap, a missing or corrupt wasm module).
	ErrorSandbox ErrorKind = "sandbox"
)

// Error describes a failed execution.
type Error struct {
	Kind    ErrorKind `json:"kind"`
	Name    string    `json:"name,omitempty"`
	Message string    `json:"message"`
	Stack   string    `json:"stack,omitempty"`
}

// StoreWrites are the keys the script changed with store(). Only successful executions report writes.
type StoreWrites struct {
	Set map[string]json.RawMessage
	// Delete lists the keys stored as undefined.
	Delete []string
	// setOrder lists the keys of Set in the order the script first wrote them.
	setOrder []string
}

// SetKeys returns the keys of Set in the order a JavaScript object enumerates them once the host assigned them one by one (host.ts parseStoreWrites).
func (w StoreWrites) SetKeys() []string { return OrderStoreKeys(w.Set, w.setOrder) }

// OrderStoreKeys returns the keys of set as a JavaScript object enumerates them: array-index keys ascending, then the others in the order of written
// (the order the script first wrote them). A key of set that written lacks follows in sorted order.
func OrderStoreKeys(set map[string]json.RawMessage, written []string) []string {
	keys := make([]string, 0, len(set))
	seen := make(map[string]bool, len(set))
	for _, key := range written {
		if _, ok := set[key]; ok && !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(set)) {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	jsstring.SortOwnKeys(keys, func(key string) string { return key })
	return keys
}

// Result is the outcome of one execution. Output is kept for failed executions too, up to the failure. exit()
// completes with a nil Value.
type Result struct {
	OK          bool
	Value       json.RawMessage
	Output      []OutputItem
	Calls       []Call
	StoreWrites StoreWrites
	// Error is set when OK is false.
	Error *Error
	// During is set in a Start run that the script itself ended (it returned, threw or exited): the result being processed
	// when it did.
	//
	// Ports packages/codemode/src/types.ts (CodemodeResult.during).
	During *During
}

// SandboxOptions configures NewSandbox.
type SandboxOptions struct {
	Tools []Tool
	// Globals are exposed as top-level identifiers instead of on tools; they behave like tools but are not recorded
	// in Result.Calls. Names must be identifiers or <namespace>.<member> and may not shadow the built-in globals.
	Globals []Tool
	// TimeoutMs is the overall deadline per execution, including time in tools. Zero means the default,
	// 300000; +Inf disables the deadline.
	TimeoutMs float64
	// MemoryLimitBytes is the most memory the QuickJS VM may allocate; allocations beyond it fail inside the script
	// as `InternalError: out of memory`. Zero means no limit beyond the wasm32 address space.
	MemoryLimitBytes uint32
	// CacheDir holds wazero's on-disk compilation cache, which makes a later process start in tens of milliseconds
	// instead of hundreds. Empty compiles in memory; a directory that cannot be used is ignored.
	CacheDir string
	// Wasm supplies the QuickJS wasm module. Nil means the embedded module (upstream: loadQuickJSWasm()).
	Wasm func() ([]byte, error)
	// AcquireWorker reserves capacity immediately before each worker VM is created, after the module is loaded. An error
	// denies admission without starting a VM and fails the execution as a sandbox error with its message. Otherwise the
	// returned release, which may be nil, is called once when the VM actually exits, not when the result is returned.
	// Reservations can be shared across sandboxes.
	AcquireWorker func() (release func(), err error)
	// OnWorkerStarted is called once for each worker VM started by Execute or Start. exited is closed when that VM
	// actually exits, which may be after the result and Close return: a call that does not poll the interrupt flag runs
	// until the grace period halts the instance. An error fails the execution as a sandbox error and ends the VM.
	OnWorkerStarted func(exited <-chan struct{}) error
}

// ExecuteOptions configures one execution.
type ExecuteOptions struct {
	// TimeoutMs overrides the sandbox default for this execution; zero keeps it.
	TimeoutMs float64
	// Store holds the values the script reads with load(key), each JSON text.
	Store map[string]json.RawMessage
}

type callInitiationKey struct{}

// CallInitiated tells the host that the call ctx belongs to has initiated (see Tool.AwaitsInitiation). It is a no-op
// for a context the host did not create and for a second call.
func CallInitiated(ctx context.Context) {
	if mark, ok := ctx.Value(callInitiationKey{}).(func()); ok {
		mark()
	}
}

// CodemodeJsonSchema is a JSON Schema document, an object or a boolean schema (packages/codemode/src/types.ts:11-12). It is carried as its raw JSON.
type CodemodeJsonSchema = json.RawMessage

// HostCall is a call the script made in a Start run, answered with Run.Deliver.
//
// Ports packages/codemode/src/types.ts (CodemodeHostCall).
type HostCall struct {
	// ID is the VM's own number for the call: 1, 2, ... in the order the script made its calls, tools and globals.
	ID int
	// Target is "tool" or "global".
	Target string
	Name   string
	// Args is what the script passed, after a JSON round trip; for spread globals the argument list. Nil is `undefined`.
	Args json.RawMessage
}

// DeclaredTool is a tool as a Start run's script sees it: tools.<JSName> and its ALL_TOOLS entry.
//
// Ports packages/codemode/src/types.ts (CodemodeDeclaredTool).
type DeclaredTool struct {
	Name string
	// JSName is the identifier the script uses, tools.<JSName>. Empty means ToCodemodeIdentifier(Name). A host that
	// replays a run passes the one the run started with, so a changed mapping cannot rename a tool under a resumed script.
	JSName      string
	Description string
}

// DeclaredGlobal is a global as a Start run's script sees it; the names are those Globals allow.
//
// Ports packages/codemode/src/types.ts (CodemodeDeclaredGlobal).
type DeclaredGlobal struct {
	Name   string
	Spread bool
}

// Outcome is a call's answer. A rejection becomes an Error with Message in the script, carrying the own properties of
// Properties (JSON values; name, message and stack are ignored), for example structuredOutput.
//
// Ports packages/codemode/src/types.ts (CodemodeOutcome).
type Outcome struct {
	OK bool
	// Value is the JSON text of the result when OK; nil is `undefined`.
	Value   json.RawMessage
	Message string
	// Properties is the JSON text of the rejection's properties; nil is `undefined`. Run.Deliver refuses a value that is
	// not a JSON object (null, an array, a primitive), as Pi's deliver() does (packages/codemode/src/runtime/execution.ts).
	Properties json.RawMessage
}

// During is, in a Start run, the result the VM was processing when the script finished (0 for its first run) and the
// number of calls it had issued by then.
//
// Ports packages/codemode/src/types.ts (CodemodeDuring).
type During struct {
	Seq    int `json:"seq"`
	Issued int `json:"issued"`
}

// RunOptions configure Sandbox.Start.
//
// Ports packages/codemode/src/types.ts (CodemodeRunOptions).
type RunOptions struct {
	// StartedAt is the VM clock's start, in ms since the epoch: Date.now() until the first result arrives.
	StartedAt float64
	// Seed seeds Math.random() and the VM's other randomness.
	Seed    uint32
	Tools   []DeclaredTool
	Globals []DeclaredGlobal
	Store   map[string]json.RawMessage
	// TimeoutMs is the deadline of this run in ms. Nil means none (not the sandbox's default).
	TimeoutMs *float64
	// OnStarted receives the VM's fingerprint, before the script runs and before any of its calls.
	OnStarted func(fingerprint string)
	// OnCall receives a call the script made. Answer it with Run.Deliver, now or later, from any goroutine.
	OnCall func(call HostCall)
	// OnIdle receives the result the VM processed (0: the script's first run, before any result) and the calls it has
	// issued in total, each time the script waits again.
	OnIdle func(seq, issued int)
}
