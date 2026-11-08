package terminals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/terminals/tool.ts: the agent's `terminal` tool (worker): tmux commands as argv on the session's
// terminals, the ones the owner sees in the terminal panel. Code mode scripts call it (`tools.terminal({ command, args })`);
// without code mode `tool_search` loads it, as MCP tools. Replay-safe: the host does a call with the tool call's key once,
// and reads start where the first attempt's did (memoized), so a call a restart interrupted runs again without typing or
// opening twice.

// Window is one window of `list-windows` as the backend gives it.
type ListedWindow struct {
	Terminal   TerminalInfo
	Foreground string
}

// TerminalBackend is the server's terminal calls for this session (`AgentTerminals`), each cancelled with ctx.
type TerminalBackend interface {
	List(ctx context.Context) ([]ListedWindow, error)
	Open(ctx context.Context, key string, location p2runtime.Location, name, command string) (TerminalInfo, error)
	Send(ctx context.Context, key, target string, keys []string, literal bool) (TerminalInfo, error)
	Cursor(ctx context.Context, target string) (TerminalInfo, int64, error)
	Read(ctx context.Context, target string, from *int64, wait *TerminalWait) (TerminalRead, error)
	Capture(ctx context.Context, target string, scrollback int, escapes bool) (TerminalInfo, string, error)
	Close(ctx context.Context, key, target string) (string, error)
}

// TerminalReach is how the model reaches the tool, as MCP tools: from code mode scripts (`searchTools()` finds it), or,
// without code mode, by loading it with `tool_search`.
type TerminalReach string

// The ways to reach the tool.
const (
	ReachCodemode   TerminalReach = "codemode"
	ReachToolSearch TerminalReach = "tool_search"
)

// TerminalExtension is the name of the extension offering the tool.
const TerminalExtension = "p2-terminals"

const (
	// outputChars are the characters of output a call returns at most: the last ones.
	outputChars = 16 * 1024
	// defaultTimeoutSeconds is the timeout of waits by default: send-keys returns sooner, its program usually answers at
	// once.
	defaultTimeoutSeconds = 10
	sendTimeoutSeconds    = 5
)

const toolDescription = "Use the session's terminals, the ones the user sees in p2's terminal panel, like tmux. Only when the user asks for a terminal, or to drive an interactive program in one; never instead of bash.\n" +
	"Call tools.terminal({ command, args }), args being tmux's argv after the command name (no quoting). Windows are numbered per session (1, 2, ...); -t takes a number or a name.\n" +
	"- new-window [-n name] [-c dir] [-w W] [-T s] [command ...]: a shell at your cwd (-c: relative to it); command is typed into it, then Enter.\n" +
	"- list-windows\n" +
	"- send-keys -t T [-l] [-w W] [-T s] key ...: tmux key names (Enter, C-c, Up, Escape, Tab, BSpace, M-x, F1) are keys, other args text; -l: all text.\n" +
	"- read -t T [-w W] [-T s]: output since your last read.\n" +
	"- capture-pane -t T [-S -N] [-e]: the screen, plus N scrollback lines; -e keeps colors.\n" +
	"- kill-window -t T\n" +
	"new-window, send-keys and read return the output since your last read of that window, after waiting: W is quiet (0.5 s without output; the default of new-window and send-keys), exit, /regex/ or none (read's default); -T is the timeout in seconds (default 10, send-keys 5, max 120). A timeout is no error.\n" +
	"Result: { window, output, running, foreground?, exitCode?, screen?, skipped?, timedOut? }; output is plain text, or a full-screen program's screen (screen: true); list-windows: { windows }.\n" +
	"const { window } = await tools.terminal({ command: \"new-window\", args: [\"-n\", \"dev\", \"npm run dev\"] });\n" +
	"await tools.terminal({ command: \"send-keys\", args: [\"-t\", String(window), \"C-c\"] });"

var (
	str     = schema.Str
	integer = schema.Int
	boolean = schema.Bool
	opt     = schema.Opt
	prop    = schema.Prop
)

var windowSchema = schema.Obj(schema.Properties{
	prop("window", integer()),
	opt("name", str()),
	opt("title", str()),
	prop("location", str()),
	prop("opener", schema.Union([]schema.Schema{p2runtime.Literal("user"), p2runtime.Literal("agent")})),
	prop("running", boolean()),
	opt("foreground", str()),
	opt("exitCode", integer()),
	opt("error", str()),
})

// resultSchema is what scripts get: one shape for every command, its fields as they apply.
var resultSchema = schema.Obj(schema.Properties{
	opt("window", integer()),
	opt("output", str()),
	opt("running", boolean()),
	opt("foreground", str()),
	opt("exitCode", integer()),
	opt("error", str()),
	opt("screen", boolean()),
	opt("skipped", integer()),
	opt("timedOut", boolean()),
	opt("killed", boolean()),
	opt("windows", schema.Arr(windowSchema)),
})

// TerminalCaller is where the calling conversation works, and the sandbox it is confined to, if any ("" for none).
type TerminalCaller func(ctx context.Context, api durable.ToolExecutionApi) (here p2runtime.Location, sandbox string, err error)

// result is one result of the tool: the fields it has.
type result struct {
	window     *int
	output     *string
	running    *bool
	foreground string
	exitCode   *int
	err        *string
	screen     bool
	skipped    int
	timedOut   bool
	killed     bool
	windows    []windowResult
}

type windowResult struct {
	window   int
	name     string
	title    string
	location string
	opener   string
	result   result
}

func (r result) json() *chordjson.Object {
	object := chordjson.NewObject(0)
	if r.window != nil {
		object.Set("window", float64(*r.window))
	}
	if r.running != nil {
		object.Set("running", *r.running)
	}
	if r.foreground != "" {
		object.Set("foreground", r.foreground)
	}
	if r.exitCode != nil {
		object.Set("exitCode", float64(*r.exitCode))
	}
	if r.err != nil {
		object.Set("error", *r.err)
	}
	if r.output != nil {
		object.Set("output", *r.output)
	}
	if r.skipped != 0 {
		object.Set("skipped", float64(r.skipped))
	}
	if r.screen {
		object.Set("screen", true)
	}
	if r.timedOut {
		object.Set("timedOut", true)
	}
	if r.killed {
		object.Set("killed", true)
	}
	if r.windows != nil {
		windows := make([]any, len(r.windows))
		for index, window := range r.windows {
			item := chordjson.ObjectOf("window", float64(window.window))
			if window.name != "" {
				item.Set("name", window.name)
			}
			if window.title != "" {
				item.Set("title", window.title)
			}
			item.Set("location", window.location)
			item.Set("opener", window.opener)
			item.Set("running", *window.result.running)
			if window.result.foreground != "" {
				item.Set("foreground", window.result.foreground)
			}
			if window.result.exitCode != nil {
				item.Set("exitCode", float64(*window.result.exitCode))
			}
			if window.result.err != nil {
				item.Set("error", *window.result.err)
			}
			windows[index] = item
		}
		object.Set("windows", windows)
	}
	return object
}

func toolFailure(text string) durable.ToolExecutionResult {
	failed := true
	return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}, IsError: &failed}
}

// mapOf is a schema as the plain map a tool registration carries.
func mapOf(value *chordjson.Object) map[string]any {
	out := make(map[string]any, value.Len())
	for key, member := range value.All() {
		out[key] = plain(member)
	}
	return out
}

func plain(value any) any {
	switch typed := value.(type) {
	case *chordjson.Object:
		return mapOf(typed)
	case []any:
		out := make([]any, len(typed))
		for index, each := range typed {
			out[index] = plain(each)
		}
		return out
	}
	return value
}

// terminalToolSchema is the tool as the model sees it.
func terminalToolSchema() (ai.ToolSchema, error) {
	names := make([]schema.Schema, len(Commands))
	for index, name := range Commands {
		names[index] = p2runtime.Literal(name)
	}
	parameters := schema.Obj(schema.Properties{
		prop("command", schema.Union(names)),
		opt("args", schema.Arr(str(), schema.Description("tmux's arguments after the command name, one per element."))),
	})
	encoded, err := json.Marshal(struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	}{"terminal", toolDescription, mustMarshalJSON(parameters)})
	if err != nil {
		return ai.ToolSchema{}, err
	}
	var tool ai.ToolSchema
	//perf:ok the tool schema is built once per worker start (cold)
	if err := json.Unmarshal(encoded, &tool); err != nil { //portlint:allow jsonclone a typed value converted from its stored JSON form, cold (see the perf:ok above)
		return ai.ToolSchema{}, err
	}
	return tool, nil
}

func mustMarshalJSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

// CreateTerminalExtension is the extension offering the `terminal` tool, reached as `reach` says. A caller confined to a
// sandbox reaches only the session's terminals in that sandbox: the others run outside it.
func CreateTerminalExtension(reach TerminalReach, backend TerminalBackend, caller TerminalCaller) (*p2runtime.P2Extension, error) {
	tool, err := terminalToolSchema()
	if err != nil {
		return nil, err
	}
	limit := outputChars + 1024
	registration := &durable.ToolRegistration{
		ToolSchema:             tool,
		StructuredOutputSchema: mapOf(resultSchema),
		// Not offered to the model; only scripts call it while code mode is on.
		Deferred:     true,
		OutputLimits: &durable.ToolOutputLimits{MaxBytes: &limit, Retain: "tail"},
		Replay:       durable.ReplaySafe,
		Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			fields, _ := args.(map[string]any)
			name, _ := fields["command"].(string)
			var argv []string
			if list, ok := fields["args"].([]any); ok {
				for _, each := range list {
					text, _ := each.(string)
					argv = append(argv, text)
				}
			}
			command, err := ParseCommand(name, argv)
			if err != nil {
				var refused *CommandError
				if errors.As(err, &refused) {
					return toolFailure(refused.Message), nil
				}
				return durable.ToolExecutionResult{}, err
			}
			here, sandbox, err := caller(ctx, api)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			outcome, terminalID, err := runCommand(ctx, command, backend, here, sandbox, api)
			if err != nil {
				if ctx.Err() != nil {
					return durable.ToolExecutionResult{}, err
				}
				return toolFailure(err.Error()), nil
			}
			listed := make([]any, len(argv))
			for index, each := range argv {
				listed[index] = each
			}
			details := chordjson.ObjectOf("command", name, "args", listed)
			if outcome.window != nil {
				details.Set("window", float64(*outcome.window))
			}
			if terminalID != "" {
				details.Set("terminalId", terminalID)
			}
			return durable.ToolExecutionResult{
				Output:              []ai.ToolResultMessageContent{ai.TextContent{Text: describe(outcome)}},
				StructuredOutput:    outcome.json(),
				HasStructuredOutput: true,
				// The card's link to the window.
				Details:    details,
				HasDetails: true,
			}, nil
		},
	}
	if reach == ReachCodemode {
		registration.Callers = []durable.ToolCaller{durable.CallerTools}
	}
	return &p2runtime.P2Extension{
		Extension: &durable.Extension{Name: TerminalExtension},
		Tools:     []p2runtime.P2Tool{{ToolRegistration: registration}},
	}, nil
}

type reachable struct {
	sandbox string
}

// check refuses a terminal outside the caller's sandbox. Checked on reruns too (remembered terminals included): the
// conversation may have been moved into a sandbox since.
func (r reachable) check(env *string, hasSandbox bool) error {
	if hasSandbox && (env == nil || *env != r.sandbox) {
		return fmt.Errorf("That window runs outside sandbox %s, where you work; it is not yours to use.", r.sandbox)
	}
	return nil
}

func runCommand(ctx context.Context, command TerminalCommand, backend TerminalBackend, here p2runtime.Location, sandbox string, api durable.ToolExecutionApi) (result, string, error) {
	key := fmt.Sprint(api.TaskId())
	gate := reachable{sandbox: sandbox}
	confined := sandbox != ""
	read := func(target string, from *int64, wait *TerminalWait) (result, string, error) {
		value, err := backend.Read(ctx, target, from, wait)
		if err != nil {
			return result{}, "", err
		}
		return readResult(value), value.Terminal.ID, nil
	}
	switch command.Command {
	case "new-window":
		location := here
		if command.Directory != "" {
			cwd := command.Directory
			if !p2runtime.IsRooted(cwd) {
				cwd = p2runtime.JoinPath(here.Cwd, cwd)
			}
			location = p2runtime.Location{Env: here.Env, Cwd: cwd}
		}
		// A rerun gets the window its first run opened, which may be outside a sandbox entered since.
		opened, err := backend.Open(ctx, key, location, command.Name, command.ShellCommand)
		if err != nil {
			return result{}, "", err
		}
		if err := gate.check(opened.Env, confined); err != nil {
			return result{}, "", err
		}
		zero := int64(0)
		return read(opened.ID, &zero, waitOf(command.WaitOptions, WaitFor{Kind: WaitQuiet}, defaultTimeoutSeconds))
	case "list-windows":
		listed, err := backend.List(ctx)
		if err != nil {
			return result{}, "", err
		}
		windows := []windowResult{}
		for _, each := range listed {
			if confined && (each.Terminal.Env == nil || *each.Terminal.Env != sandbox) {
				continue
			}
			windows = append(windows, windowOf(each.Terminal, each.Foreground))
		}
		return result{windows: windows}, "", nil
	case "send-keys":
		start, err := readStart(ctx, command.Target, backend, gate, confined, api)
		if err != nil {
			return result{}, "", err
		}
		if _, err := backend.Send(ctx, key, start.id, command.Keys, command.Literal); err != nil {
			return result{}, "", err
		}
		return read(start.id, &start.offset, waitOf(command.WaitOptions, WaitFor{Kind: WaitQuiet}, sendTimeoutSeconds))
	case "read":
		start, err := readStart(ctx, command.Target, backend, gate, confined, api)
		if err != nil {
			return result{}, "", err
		}
		return read(start.id, &start.offset, waitOf(command.WaitOptions, WaitFor{Kind: WaitNone}, defaultTimeoutSeconds))
	case "capture-pane":
		// Captured by the checked window's id: its number could be another window's by now.
		terminal, _, err := backend.Cursor(ctx, command.Target)
		if err != nil {
			return result{}, "", err
		}
		if err := gate.check(terminal.Env, confined); err != nil {
			return result{}, "", err
		}
		captured, text, err := backend.Capture(ctx, terminal.ID, command.Scrollback, command.Escapes)
		if err != nil {
			return result{}, "", err
		}
		outcome := stateOf(captured, "")
		output, skipped := cut(text, 0)
		outcome.output, outcome.skipped = &output, skipped
		return outcome, captured.ID, nil
	case "kill-window":
		// A rerun closes the window the first attempt found, never another that took its number since.
		type known struct {
			id     string
			window int
			env    *string
		}
		var target known
		memo, found, err := api.Memo(ctx, "window")
		if err != nil {
			return result{}, "", err
		}
		if found {
			object, _ := memo.(*delta.JsonObject)
			target.id, _ = object.Value("id").(string)
			number, _ := object.Value("window").(float64)
			target.window = int(number)
			if env, ok := object.Value("env").(string); ok {
				target.env = &env
			}
		} else {
			terminal, _, err := backend.Cursor(ctx, command.Target)
			if err != nil {
				return result{}, "", err
			}
			target = known{id: terminal.ID, window: terminal.Index, env: terminal.Env}
		}
		if err := gate.check(target.env, confined); err != nil {
			return result{}, "", err
		}
		var env any
		if target.env != nil {
			env = *target.env
		}
		if _, err := api.MemoCandidate(ctx, "window", chordjson.ObjectOf("id", target.id, "window", float64(target.window), "env", env)); err != nil {
			return result{}, "", err
		}
		if _, err := backend.Close(ctx, key, target.id); err != nil {
			return result{}, "", err
		}
		return result{window: &target.window, killed: true}, "", nil
	}
	return result{}, "", fmt.Errorf("unknown command %s", command.Command)
}

type readFrom struct {
	id     string
	offset int64
}

// readStart is where this call's read starts: the window's read cursor when the call first ran (memoized for a rerun).
func readStart(ctx context.Context, target string, backend TerminalBackend, gate reachable, confined bool, api durable.ToolExecutionApi) (readFrom, error) {
	memo, found, err := api.Memo(ctx, "start")
	if err != nil {
		return readFrom{}, err
	}
	if found {
		object, _ := memo.(*delta.JsonObject)
		id, _ := object.Value("id").(string)
		offset, _ := object.Value("offset").(float64)
		var env *string
		if text, ok := object.Value("env").(string); ok {
			env = &text
		}
		if err := gate.check(env, confined); err != nil {
			return readFrom{}, err
		}
		return readFrom{id: id, offset: int64(offset)}, nil
	}
	terminal, offset, err := backend.Cursor(ctx, target)
	if err != nil {
		return readFrom{}, err
	}
	if err := gate.check(terminal.Env, confined); err != nil {
		return readFrom{}, err
	}
	var env any
	if terminal.Env != nil {
		env = *terminal.Env
	}
	won, err := api.MemoCandidate(ctx, "start", chordjson.ObjectOf("id", terminal.ID, "offset", float64(offset), "env", env))
	if err != nil {
		return readFrom{}, err
	}
	object, _ := won.(*delta.JsonObject)
	id, _ := object.Value("id").(string)
	winner, _ := object.Value("offset").(float64)
	return readFrom{id: id, offset: int64(winner)}, nil
}

// waitOf is the wait a command asks for, or its default; nil for `-w none`.
func waitOf(options WaitOptions, fallback WaitFor, timeout float64) *TerminalWait {
	wait := fallback
	if options.Wait != nil {
		wait = *options.Wait
	}
	seconds := timeout
	if options.Timeout != nil {
		seconds = *options.Timeout
	}
	// JavaScript's Math.round: halves round up.
	timeoutMs := int(floorHalf(seconds * 1000))
	switch wait.Kind {
	case WaitQuiet, WaitExit:
		return &TerminalWait{Until: wait.Kind, TimeoutMs: timeoutMs}
	case WaitMatch:
		pattern := wait.Pattern
		return &TerminalWait{Until: WaitMatch, Pattern: &pattern, IgnoreCase: wait.IgnoreCase, TimeoutMs: timeoutMs}
	}
	return nil
}

func floorHalf(value float64) float64 {
	floor := float64(int64(value))
	if value-floor >= 0.5 {
		return floor + 1
	}
	return floor
}

func readResult(read TerminalRead) result {
	screen := read.Screen != nil
	text := read.Text
	skipped := int(read.Skipped) + read.Truncated
	if screen {
		text, skipped = *read.Screen, 0
	}
	outcome := stateOf(read.Terminal, read.Foreground)
	output, total := cut(text, skipped)
	outcome.output, outcome.skipped = &output, total
	outcome.screen = screen
	outcome.timedOut = read.Waited == "timeout"
	return outcome
}

// cut is the output's last outputChars (UTF-16 units), with what was left out before it counted in skipped.
func cut(text string, skipped int) (string, int) {
	length := jsstring.Length(text)
	over := max(0, length-outputChars)
	if over > 0 {
		text = jsstring.Slice(text, over)
	}
	return text, skipped + over
}

func stateOf(terminal TerminalInfo, foreground string) result {
	index := terminal.Index
	running := terminal.Exited == nil
	outcome := result{window: &index, running: &running, foreground: foreground}
	if terminal.Exited != nil {
		outcome.exitCode, outcome.err = terminal.Exited.Code, terminal.Exited.Error
	}
	return outcome
}

func windowOf(terminal TerminalInfo, foreground string) windowResult {
	location := terminal.Cwd
	if terminal.Env != nil && *terminal.Env != "" {
		location = *terminal.Env + ":" + terminal.Cwd
	}
	opener := "user"
	if terminal.Agent {
		opener = "agent"
	}
	state := stateOf(terminal, foreground)
	return windowResult{window: terminal.Index, name: terminal.Name, title: terminal.Title, location: location, opener: opener, result: state}
}

// describe is the result as the transcript and the model (without code mode) read it.
func describe(outcome result) string {
	if outcome.windows != nil {
		if len(outcome.windows) == 0 {
			return "No windows."
		}
		lines := make([]string, len(outcome.windows))
		for index, window := range outcome.windows {
			label := window.name
			if label == "" {
				label = window.title
			}
			var parts []string
			for _, part := range []string{label, window.location, "opened by " + window.opener, status(window.result)} {
				if part != "" {
					parts = append(parts, part)
				}
			}
			lines[index] = strconv.Itoa(window.window) + ": " + strings.Join(parts, " · ")
		}
		return strings.Join(lines, "\n")
	}
	if outcome.killed {
		return "Killed window " + strconv.Itoa(*outcome.window) + "."
	}
	header := "window " + strconv.Itoa(*outcome.window) + " · " + status(outcome)
	if outcome.screen {
		header += " · full-screen program"
	}
	lines := []string{header}
	if outcome.skipped != 0 {
		lines = append(lines, "… "+strconv.Itoa(outcome.skipped)+" characters skipped")
	}
	if outcome.output != nil && *outcome.output != "" {
		lines = append(lines, *outcome.output)
	}
	if outcome.timedOut && outcome.running != nil && *outcome.running {
		note := "(timed out: still running"
		if outcome.foreground != "" {
			note += " (foreground: " + outcome.foreground + ")"
		}
		lines = append(lines, note+")")
	}
	return strings.Join(lines, "\n")
}

func status(outcome result) string {
	if outcome.running != nil && *outcome.running {
		if outcome.foreground != "" {
			return "running " + outcome.foreground
		}
		return "at the prompt"
	}
	if outcome.err != nil && *outcome.err != "" {
		return "failed: " + *outcome.err
	}
	if outcome.exitCode == nil {
		return "exited"
	}
	return "exited with code " + strconv.Itoa(*outcome.exitCode)
}
