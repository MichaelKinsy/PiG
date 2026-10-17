package sdkscenarios

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
)

// longTextValue is a tool argument above the 64 KiB frame chunk size, mixing one, two, three and four byte characters.
var longTextValue = strings.Repeat("ab🦀é-", 20000)

// registrations are the tools, commands, shortcuts and flags an extension
// registers, as the host holds them and as they behave when run.
func registrations() []Scenario {
	const group = "registrations"
	params := `{"properties":{"text":{"type":"string"}},"type":"object"}`
	spec := Spec{
		Tools: []Tool{
			{Name: "echo", Description: "Echo the text", Behavior: ToolEcho},
			{Name: "fail", Description: "Always fails", Behavior: ToolFail},
		},
		Commands: []Command{
			{Name: "ping", Description: "Answer pong", Op: CommandPing},
			{Name: "flag-text", Description: "Report the string flag", Op: CommandFlag, Arg: "greeting"},
			{Name: "flag-bool", Description: "Report the boolean flag", Op: CommandFlag, Arg: "verbose"},
			{Name: "flag-unset", Description: "Report the flag without a default", Op: CommandFlag, Arg: "nodefault"},
		},
		Shortcuts: []Shortcut{{Key: "ctrl+shift+x", Description: "Fire the marker"}},
		Flags: []Flag{
			{Name: "greeting", Description: "Greeting text", Type: "string", Default: "hello"},
			{Name: "verbose", Description: "Verbose output", Type: "boolean", Default: true},
			{Name: "nodefault", Description: "No default", Type: "string"},
		},
	}
	shapes := Spec{Tools: []Tool{
		{Name: "details", Description: "Result with details", Behavior: ToolDetails},
		{Name: "soft", Description: "Result flagged as an error", Behavior: ToolSoftError},
		{Name: "empty", Description: "Result without content", Behavior: ToolEmpty},
		{Name: "blocks", Description: "Result of two text blocks", Behavior: ToolBlocks},
		{Name: "crash", Description: "Fails as a defect", Behavior: ToolCrash},
	}}
	loadFails := func(id, pi, contains string, spec Spec) Scenario {
		return Scenario{ID: id, Group: group, Pi: pi, Spec: spec, LoadErrorContains: contains,
			Want: []string{"<the extension failed to load: contains " + strconv.Quote(contains) + ">"}}
	}
	return []Scenario{
		{
			ID: "duplicate-registrations-replace-in-place", Group: group,
			Pi: "loader.ts:296,313,328,341: tools, commands, shortcuts and flags are Maps keyed by name, so a second registration replaces the first and keeps its position; :342-347 a flag's initial value is the first default registered, whatever later registrations declare",
			Spec: Spec{
				Tools: []Tool{
					{Name: "echo", Description: "first echo", Behavior: ToolEcho},
					{Name: "fail", Description: "Always fails", Behavior: ToolFail},
					{Name: "echo", Description: "second echo", Behavior: ToolEcho},
				},
				Commands: []Command{
					{Name: "ping", Description: "first ping", Op: CommandPing},
					{Name: "flag-text", Description: "Report the string flag", Op: CommandFlag, Arg: "greeting"},
					{Name: "ping", Description: "second ping", Op: CommandPing},
				},
				Shortcuts: []Shortcut{{Key: "ctrl+shift+x", Description: "first"}, {Key: "ctrl+shift+x", Description: "second"}},
				Flags: []Flag{
					{Name: "greeting", Description: "first greeting", Type: "string", Default: "hello"},
					{Name: "other", Description: "Other", Type: "boolean"},
					{Name: "greeting", Description: "second greeting", Type: "string", Default: "goodbye"},
				},
			},
			Steps: []Step{{Op: "registrations"}, {Op: "command", Name: "flag-text"}},
			Want: []string{
				`tool echo "second echo" params={"properties":{"text":{"type":"string"}},"type":"object"}`,
				`tool fail "Always fails" params={"properties":{"text":{"type":"string"}},"type":"object"}`,
				`command ping "second ping"`,
				`command flag-text "Report the string flag"`,
				`shortcut ctrl+shift+x "second"`,
				`flag greeting type=string default="goodbye" "second greeting"`,
				`flag other type=boolean default=null "Other"`,
				`command flag-text "hello"`,
			},
		},
		loadFails("tool-without-an-object-schema-fails-to-load",
			"loader.ts:290-294 registerTool throws 'Tool \"<name>\" registered by extension \"<path>\" must define an object parameter schema.' for a schema that is not an object",
			"must define an object parameter schema",
			Spec{Tools: []Tool{{Name: "bad", Description: "Bad schema", Behavior: ToolEcho, Schema: `[]`}}}),
		loadFails("flag-default-of-the-wrong-type-fails-to-load",
			"loader.ts:335-339 registerFlag throws 'Invalid default for flag \"<name>\": expected <type>, got <typeof default>'",
			`Invalid default for flag "bad": expected string, got boolean`,
			Spec{Flags: []Flag{{Name: "bad", Description: "Bad default", Type: "string", Default: true}}}),
		loadFails("command-without-a-name-fails-to-load",
			"loader.ts:305-309 registerCommand throws 'Command registered by extension \"<path>\" must have a non-empty string name.'",
			"must have a non-empty string name",
			Spec{Commands: []Command{{Name: "", Description: "Nameless", Op: CommandPing}}}),
		{
			ID: "command-failures-reach-the-host-and-the-next-command-runs", Group: group,
			Pi: "agent-session.ts/runner.ts: a command handler that throws is reported as the command's error with the thrown message, and the extension keeps serving: the next command runs",
			Spec: Spec{Commands: []Command{
				{Name: "fails", Description: "Fails", Op: CommandError},
				{Name: "crashes", Description: "Crashes", Op: CommandCrash},
				{Name: "ping", Description: "Answers", Op: CommandPing},
			}},
			Steps: []Step{{Op: "command", Name: "fails"}, {Op: "command", Name: "crashes", ErrorContains: "command crash"}, {Op: "command", Name: "ping"}},
			Want: []string{
				"command fails error: command boom",
				`command crashes error contains "command crash"`,
				"command ping pong",
			},
		},
		{
			ID: "footer-data-provider-reads-the-hosts-footer-state", Group: group,
			Pi:     "footer-data-provider.ts:103-163,387 ReadonlyFooterDataProvider: getGitBranch, getExtensionStatuses, getAvailableProviderCount read the host's footer data, which follows the host as it changes; onBranchChange subscribers are told only when the branch changes and an unsubscribed one never is",
			Covers: []string{"notify state_update"},
			Spec:   Spec{Commands: []Command{{Name: "footer", Description: "Reads the footer data", Op: CommandFooterData}}},
			Steps: []Step{
				{Op: "command", Name: "footer", JSON: true},
				{Op: "footer-state", Name: "main", Args: `{"a":"two","b":"three"}`, Count: 3},
				{Op: "command", Name: "footer", JSON: true},
				{Op: "footer-state", Name: "feature", Args: `{"b":"three"}`, Count: 3, Changes: 1},
				{Op: "command", Name: "footer", JSON: true},
			},
			Want: []string{
				`command footer {"branch":"main","count":2,"statuses":{"a":"one"}}`,
				"footer state branch changes: ",
				`command footer {"branch":"main","count":3,"statuses":{"a":"two","b":"three"}}`,
				"footer state branch changes: feature",
				`command footer {"branch":"feature","count":3,"statuses":{"b":"three"}}`,
			},
		},
		{
			ID: "late-registrations-reach-the-host", Group: group,
			Pi:     "loader.ts registerCommand/registerShortcut/registerFlag/registerMessageRenderer/registerEntryRenderer write the extension's own Map and every runner getter reads it live: one registered after load (here from a command handler) is visible to the host, keeps its place after the load-time ones, and runs its handler when invoked",
			Covers: []string{"call registerDeclaration"},
			Spec: Spec{Commands: []Command{
				{Name: "add-command", Description: "Adds a command", Op: CommandLateRegister, Arg: "command"},
				{Name: "add-shortcut", Description: "Adds a shortcut", Op: CommandLateRegister, Arg: "shortcut"},
				{Name: "add-flag", Description: "Adds a flag", Op: CommandLateRegister, Arg: "flag"},
				{Name: "add-message-renderer", Description: "Adds a message renderer", Op: CommandLateRegister, Arg: "message-renderer"},
				{Name: "add-entry-renderer", Description: "Adds an entry renderer", Op: CommandLateRegister, Arg: "entry-renderer"},
			}},
			Steps: []Step{
				{Op: "command", Name: "add-command"},
				{Op: "command", Name: "add-shortcut"},
				{Op: "command", Name: "add-flag"},
				{Op: "command", Name: "add-message-renderer"},
				{Op: "command", Name: "add-entry-renderer"},
				{Op: "registrations"},
				{Op: "command", Name: "late-command"},
				{Op: "shortcut", Name: "ctrl+alt+l"},
			},
			Want: []string{
				"command add-command registered",
				"command add-shortcut registered",
				"command add-flag registered",
				"command add-message-renderer registered",
				"command add-entry-renderer registered",
				`command add-command "Adds a command"`,
				`command add-shortcut "Adds a shortcut"`,
				`command add-flag "Adds a flag"`,
				`command add-message-renderer "Adds a message renderer"`,
				`command add-entry-renderer "Adds an entry renderer"`,
				`command late-command "Registered late"`,
				`shortcut ctrl+alt+l "Registered late"`,
				`flag late-flag type=string default="late" "Registered late"`,
				"message renderer late-type",
				"entry renderer late-type",
				"command late-command ran late",
				"shortcut ctrl+alt+l fired late",
			},
		},
		{
			ID: "command-arguments-survive-the-wire-byte-for-byte", Group: group,
			Pi:     "agent-session.ts/runner.ts: a command receives the text after its name exactly as typed (no trimming): non-ASCII, astral characters, U+2028/U+2029, NUL, leading and trailing spaces, markup characters and a payload above the 64 KiB frame chunk size",
			Covers: []string{"request command"},
			Spec:   Spec{Commands: []Command{{Name: "echo", Description: "Echoes its arguments", Op: CommandEcho}}},
			Steps: []Step{
				{Op: "command", Name: "echo", Args: "  h\u00e9llo \U0001f980 \u2028\u2029 \x00 \t <&> \u65e5\u672c\u8a9e \\ \"  "},
				{Op: "command", Name: "echo", Args: longTextValue},
			},
			Want: []string{
				`command echo args "  héllo 🦀 \u2028\u2029 \x00 \t <&> 日本語 \\ \"  "`,
				fmt.Sprintf("command echo args %d bytes sha256 %x", len(longTextValue), sha256.Sum256([]byte(longTextValue))),
			},
		},
		{
			ID: "tool-result-shapes-reach-the-model", Group: group,
			Pi:     "agent-loop.ts executePreparedToolCall/finalizeExecutedToolCall: the tool's content blocks, details and isError reach the toolResult message as the tool wrote them; isError without a throw is an error result that keeps its details",
			Covers: []string{"request tool_call"},
			Spec:   shapes,
			Steps: []Step{
				{Op: "tool-result", Name: "details", Args: `{}`},
				{Op: "tool-result", Name: "soft", Args: `{}`},
				{Op: "tool-result", Name: "empty", Args: `{}`},
				{Op: "tool-result", Name: "blocks", Args: `{}`},
				{Op: "tool-result", Name: "crash", Args: `{}`, ErrorContains: "tool crash"},
				{Op: "tool-result", Name: "blocks", Args: `{}`},
			},
			Want: []string{
				`tool details blocks=1 {"text":"with details","type":"text"} error=false details={"count":2,"nested":{"a":[1,2]}}`,
				`tool soft blocks=1 {"text":"soft failure","type":"text"} error=true details=absent`,
				`tool empty blocks=0  error=false details=absent`,
				`tool blocks blocks=2 {"text":"a","type":"text"} {"text":"b","type":"text"} error=false details=absent`,
				`tool crash blocks=1 <text contains "tool crash"> error=true details={}`,
				`tool blocks blocks=2 {"text":"a","type":"text"} {"text":"b","type":"text"} error=false details=absent`,
			},
		},
		{
			ID: "registrations-reach-the-host", Group: group,
			Pi: "loader.ts:289-349 registerTool/registerCommand/registerShortcut/registerFlag: the runtime holds each registration in order with its description, the tool's parameters, and each flag's type and default",
			// No Covers: the registrations travel in the register handshake, which is no call, request or notify method, and the row runs no tool.
			Spec:  spec,
			Steps: []Step{{Op: "registrations"}},
			Want: []string{
				`tool echo "Echo the text" params=` + params,
				`tool fail "Always fails" params=` + params,
				`command ping "Answer pong"`,
				`command flag-text "Report the string flag"`,
				`command flag-bool "Report the boolean flag"`,
				`command flag-unset "Report the flag without a default"`,
				`shortcut ctrl+shift+x "Fire the marker"`,
				`flag greeting type=string default="hello" "Greeting text"`,
				`flag verbose type=boolean default=true "Verbose output"`,
				`flag nodefault type=string default=null "No default"`,
			},
		},
		{
			ID: "tools-run-through-the-agent", Group: group,
			Pi:     "agent-loop.ts executePreparedToolCall: the tool's result text reaches the model; a tool that throws is an error result carrying its message",
			Covers: []string{"request tool_call"},
			Spec:   spec,
			Steps: []Step{
				{Op: "tool", Name: "echo", Args: `{"text":"hi"}`},
				{Op: "tool", Name: "fail", Args: `{}`},
			},
			Want: []string{
				`tool echo result "echo:hi" error=false`,
				`tool fail result "boom" error=true`,
			},
		},
		{
			ID: "tool-call-handler-block-is-the-calls-decision", Group: group,
			Pi:     "runner.ts emitToolCall: a handler that returns nothing lets the next one run, and a block result with its reason ends the walk",
			Covers: []string{"request event"},
			Spec: Spec{Events: []Event{
				{Name: "tool_call", Op: EventPass},
				{Name: "tool_call", Op: EventBlock},
			}},
			Steps: []Step{{Op: "tool-call-event", Name: "any"}},
			Want:  []string{"tool_call any blocked: event blocked"},
		},
		{
			ID: "tool-result-handlers-chain-each-sees-the-last-ones-edit", Group: group,
			Pi:     "runner.ts emitToolResult: each handler gets the event as the earlier handlers left it (content replaced, then read by the next), and the result carries the final content, details and isError",
			Covers: []string{"request event"},
			Spec: Spec{Events: []Event{
				{Name: "tool_result", Op: EventSetContent},
				{Name: "tool_result", Op: EventSeeContent},
			}},
			Steps: []Step{{Op: "tool-result-event", Name: "any"}},
			Want:  []string{`tool_result any content="patched" isError=false details={"saw":"patched"}`},
		},
		{
			ID: "tool-result-handler-throw-is-swallowed-and-later-handlers-run", Group: group,
			Pi:     "runner.ts emitToolResult: a handler that throws is reported (emitError) and the walk goes on, unlike tool_call; a later handler's edit applies to the unchanged content",
			Covers: []string{"request event"},
			Spec: Spec{Events: []Event{
				{Name: "tool_result", Op: EventThrow},
				{Name: "tool_result", Op: EventSetError},
			}},
			Steps: []Step{{Op: "tool-result-event", Name: "any"}},
			Want:  []string{`tool_result any content="original" isError=true details=null`},
		},
		{
			ID: "tool-result-handlers-that-return-nothing-change-nothing", Group: group,
			Pi:     "runner.ts emitToolResult: no handler result means no modification, and the runner returns undefined",
			Covers: []string{"request event"},
			Spec:   Spec{Events: []Event{{Name: "tool_result", Op: EventPass}}},
			Steps:  []Step{{Op: "tool-result-event", Name: "any"}},
			Want:   []string{"tool_result any unchanged"},
		},
		{
			ID: "input-handlers-chain-transforms-each-sees-the-last-ones-text", Group: group,
			Pi:     "runner.ts emitInput: each handler gets the text the earlier ones produced, a transform replaces it, and the runner returns the final text",
			Covers: []string{"request event"},
			Spec:   Spec{Events: []Event{{Name: "input", Op: EventTransform}, {Name: "input", Op: EventTransform}}},
			Steps:  []Step{{Op: "input-event", Name: "hi"}},
			Want:   []string{`input "hi" becomes "hi!!"`},
		},
		{
			ID: "input-handled-stops-the-chain-and-a-throw-or-continue-does-not", Group: group,
			Pi:     "runner.ts emitInput: a handler that throws is reported and the walk goes on, continue changes nothing, and handled returns at once so later transforms never apply",
			Covers: []string{"request event"},
			Spec: Spec{Events: []Event{
				{Name: "input", Op: EventThrow},
				{Name: "input", Op: EventContinue},
				{Name: "input", Op: EventPass},
				{Name: "input", Op: EventHandled},
				{Name: "input", Op: EventTransform},
			}},
			Steps: []Step{{Op: "input-event", Name: "hi"}},
			Want:  []string{`input "hi" handled`},
		},
		{
			ID: "input-handlers-that-continue-or-fail-leave-the-text-alone", Group: group,
			Pi:     "runner.ts emitInput: with no transform the runner returns {action: continue}, and a throwing handler leaves the text as it was",
			Covers: []string{"request event"},
			Spec:   Spec{Events: []Event{{Name: "input", Op: EventContinue}, {Name: "input", Op: EventCrash}, {Name: "input", Op: EventPass}}},
			Steps:  []Step{{Op: "input-event", Name: "hi"}},
			Want:   []string{`input "hi" unchanged`},
		},
		{
			// "unchanged" above is also what a host that never delivers the event, or stops at the throw, reports; here the
			// last handler's transform proves the walk reached it with the text the continue and the throws left.
			ID: "input-handlers-after-a-continue-or-a-throw-see-the-text-unchanged", Group: group,
			Pi:     "runner.ts emitInput: the catch only reports the error, continue does not touch currentText, so a later transform applies to the original text",
			Covers: []string{"request event"},
			Spec: Spec{Events: []Event{
				{Name: "input", Op: EventContinue},
				{Name: "input", Op: EventThrow},
				{Name: "input", Op: EventCrash},
				{Name: "input", Op: EventPass},
				{Name: "input", Op: EventTransform},
			}},
			Steps: []Step{{Op: "input-event", Name: "hi"}},
			Want:  []string{`input "hi" becomes "hi!"`},
		},
		{
			ID: "before-agent-start-system-prompts-chain-and-messages-accumulate", Group: group,
			Pi:     "runner.ts emitBeforeAgentStart: a returned systemPrompt becomes the prompt later handlers see as event.systemPrompt, the last one wins, and every returned message is kept in handler order",
			Covers: []string{"request event"},
			Spec: Spec{Events: []Event{
				{Name: "before_agent_start", Op: EventSetPrompt},
				{Name: "before_agent_start", Op: EventNote},
				{Name: "before_agent_start", Op: EventAppendPrompt},
				{Name: "before_agent_start", Op: EventSeePrompt},
			}},
			Steps: []Step{{Op: "before-agent-start-event", Name: "do the thing"}},
			Want:  []string{`before_agent_start "do the thing" systemPrompt="base prompt+" messages=[note/hello/display=true saw/do the thing/display=false]`},
		},
		{
			ID: "before-agent-start-throw-is-swallowed-and-passing-handlers-change-nothing", Group: group,
			Pi:     "runner.ts emitBeforeAgentStart: a handler that throws is reported and the walk goes on, and a handler that returns nothing adds nothing; Pi returns {messages: [], systemPromptOptions} (runner.ts:1471), which the Go runner reports as no result",
			Covers: []string{"request event"},
			Spec: Spec{Events: []Event{
				{Name: "before_agent_start", Op: EventThrow},
				{Name: "before_agent_start", Op: EventPass},
			}},
			Steps: []Step{{Op: "before-agent-start-event", Name: "x"}},
			Want:  []string{`before_agent_start "x" unchanged`},
		},
		{
			// "unchanged" above is also what a host reports when the SDK never runs the handlers or the walk stops at the
			// throw; here handlers after the throw and the pass force a prompt and add a message, so the walk must reach them.
			ID: "before-agent-start-handlers-after-a-throw-still-change-the-run", Group: group,
			Pi:     "runner.ts emitBeforeAgentStart: the catch only reports the error and the walk goes on, so a later handler's systemPrompt and message reach the result",
			Covers: []string{"request event"},
			Spec: Spec{Events: []Event{
				{Name: "before_agent_start", Op: EventThrow},
				{Name: "before_agent_start", Op: EventPass},
				{Name: "before_agent_start", Op: EventSetPrompt},
				{Name: "before_agent_start", Op: EventNote},
			}},
			Steps: []Step{{Op: "before-agent-start-event", Name: "x"}},
			Want:  []string{`before_agent_start "x" systemPrompt="base prompt" messages=[note/hello/display=true]`},
		},
		{
			ID: "lifecycle-events-arrive-with-their-payload-intact", Group: group,
			Pi:     "runner.ts emit: a handler gets the event object as the host built it, whatever its type: session_start's reason and previousSessionFile, session_shutdown's reason and targetSessionFile, turn_start's index and timestamp",
			Covers: []string{"request event"},
			Spec: Spec{Events: []Event{
				{Name: "session_start", Op: EventRecord, Tag: "a"},
				{Name: "session_shutdown", Op: EventRecord, Tag: "a"},
				{Name: "turn_start", Op: EventRecord, Tag: "a"},
			}},
			Steps: []Step{
				{Op: "emit-event", Name: "session_start", Args: `{"reason":"resume","previousSessionFile":"/x/prev é.jsonl"}`},
				{Op: "emit-event", Name: "session_shutdown", Args: `{"reason":"fork","targetSessionFile":"/x/t.jsonl"}`},
				{Op: "emit-event", Name: "turn_start", Args: `{"turnIndex":3,"timestamp":1700000000123}`},
			},
			Want: []string{
				`emit session_start handler a saw {"previousSessionFile":"/x/prev é.jsonl","reason":"resume","type":"session_start"}`,
				`emit session_shutdown handler a saw {"reason":"fork","targetSessionFile":"/x/t.jsonl","type":"session_shutdown"}`,
				`emit turn_start handler a saw {"timestamp":1700000000123,"turnIndex":3,"type":"turn_start"}`,
			},
		},
		{
			ID: "handlers-of-one-event-run-in-registration-order-and-a-failing-one-does-not-stop-the-rest", Group: group,
			Pi:     "runner.ts emit: an extension's handlers for an event run in the order they were registered, and one that throws is reported while the next still runs",
			Covers: []string{"request event"},
			Spec: Spec{Events: []Event{
				{Name: "session_start", Op: EventRecord, Tag: "first"},
				{Name: "session_start", Op: EventThrow},
				{Name: "session_start", Op: EventRecord, Tag: "second"},
				{Name: "session_start", Op: EventRecord, Tag: "third"},
			}},
			Steps: []Step{{Op: "emit-event", Name: "session_start", Args: `{"reason":"startup"}`}},
			Want: []string{
				`emit session_start handler first saw {"reason":"startup","type":"session_start"}`,
				`emit session_start handler second saw {"reason":"startup","type":"session_start"}`,
				`emit session_start handler third saw {"reason":"startup","type":"session_start"}`,
			},
		},
		{
			ID: "tool-call-handler-throw-is-the-calls-error", Group: group,
			Pi:     "runner.ts emitToolCall: no try/catch, the handler's error message is the call's error",
			Covers: []string{"request event"},
			Spec:   Spec{Events: []Event{{Name: "tool_call", Op: EventThrow}}},
			Steps:  []Step{{Op: "tool-call-event", Name: "any"}},
			Want:   []string{`tool_call any error: event boom`},
		},
		{
			ID: "tool-call-handler-defect-is-the-calls-error", Group: group,
			Pi:     "agent-session.ts _beforeToolCall: a thrown value that is not an Error becomes `Extension failed, blocking execution: <value>`; a defect in any SDK reaches the host as an error carrying its text",
			Covers: []string{"request event"},
			Spec:   Spec{Events: []Event{{Name: "tool_call", Op: EventCrash}}},
			Steps:  []Step{{Op: "tool-call-event", Name: "any", ErrorContains: "event crash"}},
			Want:   []string{`tool_call any error contains "event crash"`},
		},
		{
			ID: "tool-text-survives-the-wire-byte-for-byte", Group: group,
			Pi:     "loader.ts/runner.ts: a tool receives its arguments and returns its text unchanged: non-ASCII, astral characters, U+2028/U+2029, NUL, markup characters and a payload above the 64 KiB frame chunk size",
			Covers: []string{"request tool_call"},
			Spec:   spec,
			Steps: []Step{
				{Op: "tool", Name: "echo", Args: `{"text":"h\u00e9llo \ud83e\udd80 \u2028\u2029 \u0000 \t <&> \u65e5\u672c\u8a9e \\ \""}`},
				{Op: "tool", Name: "echo", Args: `{"text":""}`},
				{Op: "tool", Name: "echo", Args: `{"text":"` + longTextValue + `"}`},
			},
			Want: []string{
				`tool echo result "echo:héllo 🦀 \u2028\u2029 \x00 \t <&> 日本語 \\ \"" error=false`,
				`tool echo result "echo:" error=false`,
				fmt.Sprintf("tool echo result %d bytes sha256 %x error=false", len("echo:"+longTextValue), sha256.Sum256([]byte("echo:"+longTextValue))),
			},
		},
		{
			ID: "tool-text-with-a-lone-surrogate-keeps-it", Group: group,
			Pi:              "JSON.parse/JSON.stringify: a lone surrogate in an extension's text stays one code unit (the escape \\ud83d on the wire, WTF-8 in a Go string) in the tool result a model reads, whichever form of content the tool returns",
			Unrepresentable: map[SDK]string{Rust: "a Rust String is valid UTF-8 and cannot hold a lone surrogate"},
			Covers:          []string{"request tool_call"},
			Spec:            Spec{Tools: []Tool{{Name: "bad", Description: "Returns invalid text", Behavior: ToolBadText}}},
			Steps:           []Step{{Op: "tool", Name: "bad", Args: `{}`}},
			Want:            []string{`tool bad result "a\xed\xa0\xbdb" error=false`},
		},
		{
			ID: "commands-and-shortcuts-run", Group: group,
			Pi:     "runner.ts: a registered command runs with its argument and a registered shortcut's handler runs when fired",
			Covers: []string{"request command", "request shortcut"},
			Spec:   spec,
			Steps: []Step{
				{Op: "command", Name: "ping"},
				{Op: "shortcut", Name: "ctrl+shift+x"},
			},
			Want: []string{
				`command ping pong`,
				`shortcut ctrl+shift+x fired ctrl+shift+x`,
			},
		},
		{
			ID: "flag-defaults-are-visible-to-the-extension", Group: group,
			Pi:     "loader.ts:331-349 registerFlag initializes each flag with its default and :374-378 getFlag returns it; a flag declared without a default is undefined",
			Covers: []string{"call getFlag", "request command"},
			Spec:   spec,
			Steps: []Step{
				{Op: "command", Name: "flag-text"},
				{Op: "command", Name: "flag-bool"},
				{Op: "command", Name: "flag-unset"},
			},
			Want: []string{
				`command flag-text "hello"`,
				`command flag-bool true`,
				`command flag-unset null`,
			},
		},
	}
}
