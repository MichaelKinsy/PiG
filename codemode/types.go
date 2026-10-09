package codemode

import (
	"context"
	"encoding/json"
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
	InputSchema json.RawMessage
	// OutputSchema is the JSON Schema of the resolved value, used only to render declarations. Nil means unknown.
	OutputSchema json.RawMessage
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
	// ErrorTimeout: the overall deadline expired and the VM was stopped.
	ErrorTimeout ErrorKind = "timeout"
	// ErrorAborted: the caller's context ended or the sandbox was closed.
	ErrorAborted ErrorKind = "aborted"
	// ErrorSandbox: the VM failed outside the script's control (a wasm trap, a missing or corrupt wasm module).
	ErrorSandbox ErrorKind = "sandbox"
)

// Error describes a failed execution.
type Error struct {
	Kind    ErrorKind
	Name    string
	Message string
	Stack   string
}

// StoreWrites are the keys the script changed with store(). Only successful executions report writes.
type StoreWrites struct {
	Set map[string]json.RawMessage
	// Delete lists the keys stored as undefined.
	Delete []string
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
