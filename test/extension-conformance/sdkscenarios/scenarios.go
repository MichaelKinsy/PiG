// Package sdkscenarios is the one table of SDK conformance scenarios. A
// scenario is data: the extension every SDK's scenario driver builds from its
// Spec, the host-side Steps the harness runs against the real host, and the
// observation Want that every SDK must produce byte for byte. The Go, Node,
// Python and Rust drivers (test/extension-conformance/testdata/scenario-driver-*)
// each read the same Spec and register it through their SDK's public API, so a
// scenario exists once and an SDK that differs fails its own row.
package sdkscenarios

// SDK names an extension SDK under test.
type SDK string

const (
	Go     SDK = "go"
	Node   SDK = "node"
	Python SDK = "python"
	Rust   SDK = "rust"
)

// SDKs lists every SDK in a stable order.
var SDKs = []SDK{Go, Node, Python, Rust}

// Red records an SDK's row that is known to fail today. While the entry exists
// the row must produce exactly Got, so a further regression inside a red row
// fails it too; the test fails when the row starts passing, so the entry is
// deleted by the lane that fixes it, and a fix that changes the row's output
// without making it pass updates Got.
type Red struct {
	// Lane owns the fix.
	Lane string
	// Why names the missing behavior.
	Why string
	// Got is the observation the SDK produces today, line by line.
	Got []string
}

// Scenario is one conformance row set: it runs once per SDK.
type Scenario struct {
	ID    string
	Group string
	// Pi cites the Pi source that defines the expected observation.
	Pi string
	// Covers names the wire frames the scenario exercises, as "<frame> <method>".
	Covers []string
	// Spec is what the driver registers.
	Spec Spec
	// Peer, when set, is a second extension (always the Node driver) the harness
	// loads after Spec's, as another process registering after the first.
	Peer *Spec
	// Steps run against the real host after the extension loaded.
	Steps []Step
	// Want is the observation every SDK must produce, line by line.
	Want []string
	// LoadErrorContains, when set, expects the extension to fail to load: the
	// observation is then "<the extension failed to load: contains ...>" if the
	// SDK's failure message holds the text, and the message itself otherwise, so
	// rows compare Pi's wording without depending on an SDK's wrapper text.
	LoadErrorContains string
	// Unrepresentable names the SDKs whose language cannot express the row, with
	// the reason: the row is not run for them. It is for a language limit (Rust
	// strings cannot hold a lone surrogate), never for a bug.
	Unrepresentable map[SDK]string
	// Red lists the SDKs whose row fails today.
	Red map[SDK]Red
}

// Spec is the extension a scenario describes. It is JSON: the driver of each
// SDK reads it from .sdk-scenario.json in its working directory.
type Spec struct {
	Providers []Provider `json:"providers,omitempty"`
	Tools     []Tool     `json:"tools,omitempty"`
	Commands  []Command  `json:"commands,omitempty"`
	Shortcuts []Shortcut `json:"shortcuts,omitempty"`
	Flags     []Flag     `json:"flags,omitempty"`
	Events    []Event    `json:"events,omitempty"`
}

// Event handler behaviors a driver implements.
const (
	// EventThrow fails with the message "event boom": Node throws Error, Python
	// raises RuntimeError, Go returns an error, Rust returns Err.
	EventThrow = "throw"
	// EventCrash fails the way a defect does with the text "event crash": Node
	// throws the string, Python raises KeyError, Go and Rust panic.
	EventCrash = "crash"
	// EventBlock returns {block: true, reason: "event blocked"}.
	EventBlock = "block"
	// EventPass returns nothing.
	EventPass = "pass"
	// EventSetContent returns content [{type: "text", text: "patched"}].
	EventSetContent = "set-content"
	// EventSetError returns isError: true.
	EventSetError = "set-error"
	// EventSeeContent returns details {saw: <the first content block's text the handler was given>}.
	EventSeeContent = "see-content"
	// EventRecord appends "<Tag>:<the event's JSON>" to the file .events and returns nothing.
	EventRecord = "record"
	// EventRecordCounted is EventRecord with "@<n>" after the tag, n counting the records this process has made, so a line shows which generation of the extension wrote it.
	EventRecordCounted = "record-counted"
	// EventTransform returns {action: "transform", text: <the text the handler was given> + "!"}.
	EventTransform = "transform"
	// EventHandled returns {action: "handled"}.
	EventHandled = "handled"
	// EventContinue returns {action: "continue"}.
	EventContinue = "continue"
	// EventSetPrompt returns systemPrompt "base prompt".
	EventSetPrompt = "set-prompt"
	// EventAppendPrompt returns systemPrompt <the event's systemPrompt> + "+".
	EventAppendPrompt = "append-prompt"
	// EventNote returns the message {customType: "note", content: "hello", display: true}.
	EventNote = "note"
	// EventSeePrompt returns the message {customType: "saw", content: <the event's prompt>, display: false}.
	EventSeePrompt = "see-prompt"
)

// Event is a handler for the Pi event Name.
type Event struct {
	Name string `json:"name"`
	Op   string `json:"op"`
	// Tag labels the handler in the EventRecord line.
	Tag string `json:"tag,omitempty"`
}

// Tool behaviors a driver implements.
const (
	// ToolEcho returns "echo:" and the text parameter.
	ToolEcho = "echo"
	// ToolFail fails with the message "boom".
	ToolFail = "fail"
	// ToolCooperative marks itself started, waits for the abort signal, then
	// returns "cooperative stopped".
	ToolCooperative = "cooperative"
	// ToolDetails returns the text "with details" and the details
	// {"count":2,"nested":{"a":[1,2]}}.
	ToolDetails = "details"
	// ToolSoftError returns the text "soft failure" with isError set: a failure
	// reported without throwing.
	ToolSoftError = "soft-error"
	// ToolEmpty returns a result with no content at all.
	ToolEmpty = "empty"
	// ToolBlocks returns two text blocks, "a" then "b".
	ToolBlocks = "blocks"
	// ToolBadText returns the text "a", a character no UTF-8 string holds, "b":
	// Node and Python return the lone surrogate U+D83D and Go its WTF-8 bytes
	// (the SDK writes them as the escape \ud83d); Rust strings cannot hold one.
	ToolBadText = "bad-text"
	// ToolCrash fails the way a defect does: Node throws the string "tool crash",
	// Python raises KeyError("tool crash"), Go and Rust panic with it.
	ToolCrash = "crash"
	// ToolHang marks itself started and then blocks without ever looking at the
	// abort signal.
	ToolHang = "hang"
)

// Tool is a registered tool taking an object with a string "text".
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Behavior    string `json:"behavior"`
	// Schema, when set, is the parameter schema as JSON instead of the object with a string "text".
	Schema string `json:"schema,omitempty"`
}

// Command ops a driver implements. Each op that reports writes its answer to the
// file named by the command's argument.
const (
	// CommandPing writes "pong".
	CommandPing = "ping"
	// CommandRegisteredProvider writes the name of the native provider the
	// extension's model registry returns for Arg, or "none".
	CommandRegisteredProvider = "registered-provider"
	// CommandFlag writes the JSON of the extension's getFlag(Arg).
	CommandFlag = "flag"
	// CommandNoop returns at once and writes nothing: the cheapest round trip
	// the host can make to the extension.
	CommandNoop = "noop"
	// CommandError fails the way the language reports a failure: Node throws
	// Error("command boom"), Python raises RuntimeError("command boom"), Go
	// returns errors.New("command boom"), Rust returns CommandResult::Error.
	CommandError = "error"
	// CommandCrash fails the way a defect does: Node throws the string "command
	// crash", Python raises KeyError("command crash"), Go panics with it, Rust
	// panics with it.
	CommandCrash = "crash"
	// CommandEcho writes the arguments it received to ".echo-args" in the extension's working directory.
	CommandEcho = "echo"
	// CommandLateRegister registers, after the extension loaded, the declaration Arg names: "command" ("late-command", "Registered late", writing "ran late" to its argument path), "shortcut" (ctrl+alt+l, "Registered late", writing "late" to ".shortcut-fired"), "flag" (the string flag "late-flag", default "late", "Registered late"), "message-renderer" or "entry-renderer" (custom type "late-type").
	CommandLateRegister = "late-register"
	// CommandFooterData reads Pi's ReadonlyFooterDataProvider (footer-data-provider.ts) and writes {"branch": getGitBranch() (null for none), "statuses": getExtensionStatuses() as an object, "count": getAvailableProviderCount()} as JSON. The first call also subscribes onBranchChange twice: the callback of one appends the branch it was told about and a newline to ".branch-changes" in the extension's working directory, the other is unsubscribed at once and must never run. Node reaches the provider as the third argument of the footer factory it installs; Go, Python and Rust read it from the context.
	CommandFooterData = "footer-data"
	// CommandHang marks itself started and then blocks forever.
	CommandHang = "hang"
	// CommandQuery writes the JSON of the context query named by Arg: "idle",
	// "pending", "system-prompt", "thinking", "all-tools" (name and description
	// of each tool), "session-header" (type, id and cwd), "leaf-entry" (type and
	// id) or "session-manager-id" (the session id read through ctx.sessionManager).
	CommandQuery = "query"
	// CommandSignal writes what ctx.signal reports: "none" while no run is
	// active, "live" during one, "aborted" once the run was aborted.
	CommandSignal = "signal"
	// CommandSignalWait marks itself started, waits until ctx.signal is aborted
	// and writes "signal aborted while running".
	CommandSignalWait = "signal-wait"
	// CommandAction calls the context action named by Arg ("abort", "shutdown" or
	// "compact", which carries the instructions "be brief") and writes "called".
	CommandAction = "action"
	// CommandRegistry writes the JSON of the model registry query named by Arg
	// "<query>:<provider>": "has-auth", "using-oauth" (a model of that provider)
	// or "provider-name" (the name of the Provider object ModelRegistry.getProvider returns, or null).
	CommandRegistry = "registry"
	// CommandReloadStale awaits ctx.reload() and then reads the command's own
	// ctx.cwd, writing "cwd live" or "cwd threw: " and the error message.
	CommandReloadStale = "reload-stale"
)

// Command is a registered slash command.
type Command struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Op          string `json:"op"`
	Arg         string `json:"arg,omitempty"`
}

// Shortcut is a registered keyboard shortcut; its handler writes a marker file.
type Shortcut struct {
	Key         string `json:"key"`
	Description string `json:"description"`
}

// Flag is a registered CLI flag. Type is "boolean" or "string"; a nil Default is
// a flag with no default.
type Flag struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Default     any    `json:"default,omitempty"`
}

// Provider is a Pi Provider object (registerNativeProvider).
type Provider struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	APIKey *APIKey `json:"apiKey,omitempty"`
	OAuth  *OAuth  `json:"oauth,omitempty"`
}

// APIKey is the provider's api-key method. A nil Login is an ambient-only method.
type APIKey struct {
	Name string `json:"name"`
	// CheckSource, when set, gives the method a check that reports it as the credential source.
	CheckSource string `json:"checkSource,omitempty"`
	Login       *Flow  `json:"login,omitempty"`
}

// OAuth is the provider's account login.
type OAuth struct {
	Name         string `json:"name"`
	LoginLabel   string `json:"loginLabel,omitempty"`
	Subscription bool   `json:"subscription,omitempty"`
	Login        Flow   `json:"login"`
	// Refresh is a JSON object whose fields refresh sets over the credential it receives.
	Refresh string `json:"refresh,omitempty"`
}

// Flow is a login flow: its interaction steps, then the credential it returns.
type Flow struct {
	Steps []FlowStep `json:"steps,omitempty"`
	// Result is the credential as a JSON object; "${name}" in a string is the
	// answer of the prompt named name. Key order is the order the flow returns.
	Result string `json:"result"`
}

// FlowStep is one interaction: a Notify event, a Prompt whose answer is kept as As,
// or a DeviceID step that keeps LoginOptions.getDeviceId() as As ("<none>" when
// the login has no getDeviceId).
type FlowStep struct {
	Notify   string `json:"notify,omitempty"`
	Prompt   string `json:"prompt,omitempty"`
	DeviceID bool   `json:"deviceId,omitempty"`
	As       string `json:"as,omitempty"`
}

// Step is one host-side action. Op selects which other fields apply.
type Step struct {
	// Op is one of: "settle", "methods", "runtime-methods", "models", "check",
	// "login", "authjson", "setcredential", "resolve" (providers and auth);
	// "host-calls" (the fire-and-forget host actions an extension triggered);
	// "host-provider", "load-peer", "command", "reload", "end-input" (host events); "abort-tool"
	// (the abort rule); "registrations", "tool", "tool-result", "tool-call-event", "tool-result-event", "input-event", "before-agent-start-event", "run-signal", "abort-run-in-command", "emit-event" (the runner emits the event Name with the JSON payload Args and the step records the lines EventRecord handlers wrote), "shortcut" (tools, commands,
	// shortcuts and flags), "footer-state" (the host's footer data).
	Op       string
	Provider string
	// Kind is "api_key" or "oauth".
	Kind string
	// Answers script the login's prompts in order. "<cancel>" cancels that prompt.
	Answers []string
	// Name names the command, tool or shortcut key a step runs.
	Name string
	// JSON makes a command step record its result as canonical JSON (sorted keys,
	// no spaces), so a row is not about an SDK's key order.
	JSON bool
	// Changes is the number of branch-change callbacks a footer-state step waits for, counting the lines of ".branch-changes".
	Changes int
	// Count is the number of host calls in all a host-calls step waits for; it records the ones since the previous host-calls step.
	Count int
	// Args is the JSON object a tool step passes.
	Args string
	// Spec is the extension a reload step swaps in.
	Spec *Spec
	// Credential is the JSON a setcredential step writes to auth.json.
	Credential string
	// Raw makes an authjson step record the entry's bytes as written; otherwise
	// the entry is recorded with its keys sorted, so a row that is not about key
	// order does not depend on it.
	Raw bool
	// DeviceID, when set, is the device ID an oauth login step offers as
	// LoginOptions.GetDeviceID; the observation records how often the flow called it.
	DeviceID string
	// ErrorContains, when set, is the text a failed login's or command's message
	// must contain; the observation then records that text instead of the SDK's
	// wording. A defect (a panic, an exception that is not a failure value) is
	// worded by its language, so such a row is about the message reaching the
	// host and not the wrapper around it.
	ErrorContains string
}
