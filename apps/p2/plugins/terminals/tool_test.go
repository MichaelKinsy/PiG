package terminals

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports test/p2/terminal-tool-unit.test.ts: the terminal tool against a recording backend: what each command asks the
// server for (locations, keys, waits, read starts), its results, and replay safety (a rerun of the same tool call reads
// from the same start and closes the same window; the host does keyed calls once).

func ptr(value string) *string { return &value }

func info(index int, change func(*TerminalInfo)) TerminalInfo {
	terminal := TerminalInfo{ID: fmt.Sprintf("id-%d", index), SessionID: "s", Index: index, Cwd: "/work", Cols: 80, Rows: 24}
	if change != nil {
		change(&terminal)
	}
	return terminal
}

type call struct {
	name string
	args any
}

type recording struct {
	calls  []call
	reads  []func(*TerminalRead)
	cursor int64
	fail   error
}

func (r *recording) List(context.Context) ([]ListedWindow, error) {
	r.calls = append(r.calls, call{"list", nil})
	return []ListedWindow{
		{Terminal: info(1, func(i *TerminalInfo) { i.Agent, i.Name = true, "dev" }), Foreground: "vim"},
		{Terminal: info(2, func(i *TerminalInfo) {
			i.Env, i.Cwd = ptr("gpu"), "/srv"
			code := 2
			i.Exited = &Exited{Code: &code}
		})},
	}, nil
}

func (r *recording) Open(_ context.Context, key string, location p2runtime.Location, name, command string) (TerminalInfo, error) {
	r.calls = append(r.calls, call{"open", fmt.Sprintf("%s %s %s %s %s", key, location.Cwd, derefString(location.Env), name, command)})
	return info(3, func(i *TerminalInfo) { i.Cwd, i.Env = location.Cwd, location.Env }), nil
}

func (r *recording) Send(_ context.Context, key, target string, keys []string, literal bool) (TerminalInfo, error) {
	r.calls = append(r.calls, call{"send", fmt.Sprintf("%s %s %q %v", key, target, keys, literal)})
	return info(1, nil), nil
}

func (r *recording) Cursor(_ context.Context, target string) (TerminalInfo, int64, error) {
	r.calls = append(r.calls, call{"cursor", target})
	if r.fail != nil {
		return TerminalInfo{}, 0, r.fail
	}
	r.cursor += 10
	index := 1
	fmt.Sscanf(target, "%d", &index)
	return info(index, nil), 100 + r.cursor, nil
}

func (r *recording) Read(_ context.Context, target string, from *int64, wait *TerminalWait) (TerminalRead, error) {
	description := target
	if from != nil {
		description += fmt.Sprintf(" from=%d", *from)
	}
	if wait != nil {
		description += fmt.Sprintf(" wait=%s", waitText(wait))
	}
	r.calls = append(r.calls, call{"read", description})
	read := TerminalRead{Terminal: info(1, nil)}
	if from != nil {
		read.From = *from
	}
	if len(r.reads) > 0 {
		change := r.reads[0]
		r.reads = r.reads[1:]
		change(&read)
	}
	return read, nil
}

func (r *recording) Capture(_ context.Context, target string, scrollback int, escapes bool) (TerminalInfo, string, error) {
	r.calls = append(r.calls, call{"capture", fmt.Sprintf("%s %d %v", target, scrollback, escapes)})
	return info(1, nil), "screen", nil
}

func (r *recording) Close(_ context.Context, key, target string) (string, error) {
	r.calls = append(r.calls, call{"close", key + " " + target})
	return target, nil
}

func (r *recording) names(name string) []call {
	var out []call
	for _, each := range r.calls {
		if each.name == name {
			out = append(out, each)
		}
	}
	return out
}

// callAPI is one tool call's API: its task id and memo store (shared by its reruns). The tool uses only these members.
type callAPI struct {
	durable.ToolExecutionApi
	task  durable.TaskId
	memos map[string]durable.JsonValue
}

func newCallAPI(task int) *callAPI {
	return &callAPI{task: durable.TaskId(task), memos: map[string]durable.JsonValue{}}
}

func (a *callAPI) TaskId() durable.TaskId             { return a.task }
func (a *callAPI) ConversationId() durable.ConversationId { return 7 }
func (a *callAPI) Memo(_ context.Context, name string) (durable.JsonValue, bool, error) {
	value, ok := a.memos[name]
	return value, ok, nil
}
func (a *callAPI) MemoCandidate(_ context.Context, name string, candidate durable.JsonValue) (durable.JsonValue, error) {
	if _, ok := a.memos[name]; !ok {
		a.memos[name] = candidate
	}
	return a.memos[name], nil
}

func terminalTool(t *testing.T, backend TerminalBackend, reach TerminalReach, sandbox string) *durable.ToolRegistration {
	t.Helper()
	extension, err := CreateTerminalExtension(reach, backend, func(_ context.Context, api durable.ToolExecutionApi) (p2runtime.Location, string, error) {
		if api.ConversationId() != 7 {
			t.Errorf("conversation = %d", api.ConversationId())
		}
		return p2runtime.Location{Cwd: "/work/sub", Env: ptr("gpu")}, sandbox, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return extension.Tools[0].ToolRegistration
}

func execute(t *testing.T, tool *durable.ToolRegistration, command string, args []string, api *callAPI) durable.ToolExecutionResult {
	t.Helper()
	list := make([]any, len(args))
	for index, each := range args {
		list[index] = each
	}
	result, err := tool.Execute(context.Background(), map[string]any{"command": command, "args": list}, api)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func run(t *testing.T, backend TerminalBackend, command string, args ...string) durable.ToolExecutionResult {
	return execute(t, terminalTool(t, backend, ReachCodemode, ""), command, args, newCallAPI(42))
}

func structured(t *testing.T, result durable.ToolExecutionResult) string {
	t.Helper()
	text, err := chordjson.Marshal(result.StructuredOutput)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

func outputText(result durable.ToolExecutionResult) string {
	return result.Output[0].(ai.TextContent).Text
}

func equal(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestCodeModeOnlyWhileCodeModeIsOnToolSearchReachesItOtherwise(t *testing.T) {
	backend := &recording{}
	tool := terminalTool(t, backend, ReachCodemode, "")
	equal(t, tool.Callers, []durable.ToolCaller{durable.CallerTools})
	if !tool.Deferred || tool.Replay != durable.ReplaySafe {
		t.Fatalf("deferred %v replay %s", tool.Deferred, tool.Replay)
	}
	search := terminalTool(t, backend, ReachToolSearch, "")
	if search.Callers != nil || !search.Deferred {
		t.Fatalf("tool_search tool: callers %v deferred %v", search.Callers, search.Deferred)
	}
	if !strings.Contains(tool.Description, "Only when the user asks for a terminal") {
		t.Fatal(tool.Description)
	}
}

func TestNewWindowOpensAtTheCallingConversationsLocationKeyedByTheCallAndReadsFromItsStart(t *testing.T) {
	backend := &recording{reads: []func(*TerminalRead){func(r *TerminalRead) { r.Text, r.Terminal = "$ ", info(3, nil) }}}
	result := run(t, backend, "new-window", "-n", "dev", "-c", "../web", "npm", "run", "dev")
	equal(t, backend.calls, []call{
		{"open", "42 /work/web gpu dev npm run dev"},
		{"read", `id-3 from=0 wait={"until":"quiet","timeoutMs":10000}`},
	})
	if got := structured(t, result); got != `{"window":3,"running":true,"output":"$ "}` {
		t.Fatal(got)
	}
	if got := outputText(result); got != "window 3 · at the prompt\n$ " {
		t.Fatalf("%q", got)
	}
	absolute := &recording{}
	run(t, absolute, "new-window", "-c", "/tmp", "-w", "none")
	equal(t, absolute.calls, []call{{"open", "42 /tmp gpu  "}, {"read", "id-3 from=0"}})
}

func TestSendKeysAndReadWaitByDefaultAsDocumentedWAndTOverride(t *testing.T) {
	backend := &recording{}
	tool := terminalTool(t, backend, ReachCodemode, "")
	execute(t, tool, "send-keys", []string{"-t", "1", "q"}, newCallAPI(1))
	execute(t, tool, "send-keys", []string{"-t", "1", "-w", "none", "-l", "C-c"}, newCallAPI(2))
	execute(t, tool, "read", []string{"-t", "1"}, newCallAPI(3))
	execute(t, tool, "read", []string{"-t", "1", "-w", "/x/i", "-T", "2"}, newCallAPI(4))
	equal(t, backend.calls, []call{
		{"cursor", "1"},
		{"send", `1 id-1 ["q"] false`},
		{"read", `id-1 from=110 wait={"until":"quiet","timeoutMs":5000}`},
		{"cursor", "1"},
		{"send", `2 id-1 ["C-c"] true`},
		{"read", `id-1 from=120`},
		{"cursor", "1"},
		{"read", `id-1 from=130`},
		{"cursor", "1"},
		{"read", `id-1 from=140 wait={"until":"match","pattern":"x","ignoreCase":true,"timeoutMs":2000}`},
	})
}

func TestARerunOfTheSameCallReadsFromTheSameStartAndClosesTheSameWindow(t *testing.T) {
	backend := &recording{}
	tool := terminalTool(t, backend, ReachCodemode, "")
	api := newCallAPI(9)
	execute(t, tool, "send-keys", []string{"-t", "1", "ls", "Enter"}, api)
	execute(t, tool, "send-keys", []string{"-t", "1", "ls", "Enter"}, api)
	// One cursor: the rerun's start is the memoized one; the send repeats with the same key (the host does it once).
	var others []call
	for _, each := range backend.calls {
		if each.name != "read" {
			others = append(others, each)
		}
	}
	equal(t, others, []call{{"cursor", "1"}, {"send", `9 id-1 ["ls" "Enter"] false`}, {"send", `9 id-1 ["ls" "Enter"] false`}})
	var froms []any
	for _, each := range backend.names("read") {
		froms = append(froms, strings.Fields(each.args.(string))[1])
	}
	equal(t, froms, []any{"from=110", "from=110"})
	killing := &recording{}
	kill := newCallAPI(10)
	killTool := terminalTool(t, killing, ReachCodemode, "")
	if got := structured(t, execute(t, killTool, "kill-window", []string{"-t", "2"}, kill)); got != `{"window":2,"killed":true}` {
		t.Fatal(got)
	}
	execute(t, killTool, "kill-window", []string{"-t", "2"}, kill)
	equal(t, killing.calls, []call{{"cursor", "2"}, {"close", "10 id-2"}, {"close", "10 id-2"}})
}

func TestResultsTheScreenOfFullScreenProgramsSkippedOutputTimeoutsWindowsErrors(t *testing.T) {
	long := strings.Repeat("x", 20_000)
	backend := &recording{reads: []func(*TerminalRead){
		func(r *TerminalRead) {
			screen := "VIM"
			r.Text, r.Screen, r.Foreground, r.Waited = "ignored", &screen, "vim", "timeout"
		},
		func(r *TerminalRead) {
			code := 1
			r.Text, r.Skipped, r.Truncated = long, 5, 7
			r.Terminal = info(1, func(i *TerminalInfo) { i.Exited = &Exited{Code: &code} })
		},
	}}
	screen := run(t, backend, "read", "-t", "1")
	if got := structured(t, screen); got != `{"window":1,"running":true,"foreground":"vim","output":"VIM","screen":true,"timedOut":true}` {
		t.Fatal(got)
	}
	if got := outputText(screen); got != "window 1 · running vim · full-screen program\nVIM\n(timed out: still running (foreground: vim))" {
		t.Fatalf("%q", got)
	}
	skipped := run(t, backend, "read", "-t", "1").StructuredOutput.(*chordjson.Object)
	if got := len(skipped.Value("output").(string)); got != 16*1024 {
		t.Fatalf("output length %d", got)
	}
	if got := skipped.Value("skipped"); got != float64(5+7+20_000-16*1024) {
		t.Fatalf("skipped %v", got)
	}
	listed := run(t, backend, "list-windows")
	want := `{"windows":[{"window":1,"name":"dev","location":"/work","opener":"agent","running":true,"foreground":"vim"},{"window":2,"location":"gpu:/srv","opener":"user","running":false,"exitCode":2}]}`
	if got := structured(t, listed); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if got := outputText(listed); got != "1: dev · /work · opened by agent · running vim\n2: gpu:/srv · opened by user · exited with code 2" {
		t.Fatalf("%q", got)
	}
	usage := run(t, backend, "kill-window")
	if outputText(usage) != "usage: kill-window -t target-window" || usage.IsError == nil || !*usage.IsError {
		t.Fatalf("usage result %+v", usage)
	}
	backend.fail = fmt.Errorf("can't find window: 9")
	missing := run(t, backend, "read", "-t", "9")
	if outputText(missing) != "can't find window: 9" || missing.IsError == nil || !*missing.IsError {
		t.Fatalf("missing result %+v", missing)
	}
}

func TestACallerInASandboxSeesAndDrivesOnlyTheSessionsTerminalsInIt(t *testing.T) {
	backend := &recording{}
	confined := terminalTool(t, backend, ReachCodemode, "gpu")
	listed := execute(t, confined, "list-windows", nil, newCallAPI(1))
	object := listed.StructuredOutput.(*chordjson.Object)
	windows := object.Value("windows").([]any)
	if len(windows) != 1 || windows[0].(*chordjson.Object).Value("window") != float64(2) {
		t.Fatalf("windows: %v", windows)
	}
	// Window 1 runs on this device (env null), outside the sandbox.
	for _, each := range [][]string{{"send-keys", "-t", "1", "rm -rf ~"}, {"read", "-t", "1"}, {"capture-pane", "-t", "1"}, {"kill-window", "-t", "1"}} {
		result := execute(t, confined, each[0], each[1:], newCallAPI(2))
		if result.IsError == nil || !*result.IsError || !strings.Contains(outputText(result), "outside sandbox gpu") {
			t.Fatalf("%s: %+v", each[0], result)
		}
	}
	for _, name := range []string{"send", "read", "capture", "close"} {
		if len(backend.names(name)) != 0 {
			t.Fatalf("%s called: %v", name, backend.calls)
		}
	}
	// A rerun after the conversation was moved into the sandbox: the window its first run remembered is refused too.
	api := newCallAPI(9)
	execute(t, terminalTool(t, backend, ReachCodemode, ""), "send-keys", []string{"-t", "1", "ls"}, api)
	sends := len(backend.names("send"))
	rerun := execute(t, confined, "send-keys", []string{"-t", "1", "ls"}, api)
	if rerun.IsError == nil || !*rerun.IsError || len(backend.names("send")) != sends {
		t.Fatalf("rerun: %+v, sends %d -> %d", rerun, sends, len(backend.names("send")))
	}
}

func waitText(wait *TerminalWait) string {
	text, _ := chordjson.Marshal(wait.JSON())
	return string(text)
}
