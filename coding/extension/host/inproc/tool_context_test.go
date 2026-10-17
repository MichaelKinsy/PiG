package inproc_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Upstream runner.ts:948-985 createToolContext, wrapper.ts:14-19 and tool-definition-wrapper.ts:7-30: a tool call gets the extension context plus `tools` and `executeTool()`, backed by the actions bindCore installed. The call passes its own id and defaults the nested call's signal to the calling tool's.
func TestRunnerToolContextRunsNestedCallsForTheCallingTool(t *testing.T) {
	callable := []extension.AgentTool{{Name: "echo", Label: "Echo", Description: "Echo", Parameters: json.RawMessage(`{"type":"object"}`)}}
	var gotCaller, gotName string
	var gotArgs json.RawMessage
	var gotCtx context.Context
	outcome := extension.AgentToolCallOutcome{ToolCall: ai.ToolCall{ID: "call-1/1", Name: "echo"}, Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}}}
	runner := inproc.NewRunner(nil, t.TempDir())
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{ToolActions: extension.ToolActions{
		GetCallableTools: func() []extension.AgentTool { return callable },
		ExecuteTool: func(ctx context.Context, callerID, name string, args json.RawMessage, _ extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error) {
			gotCaller, gotName, gotArgs, gotCtx = callerID, name, args, ctx
			return outcome, nil
		},
	}}, nil)

	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	tc := runner.CreateToolContext(parent, "call-1")
	if tc == nil || tc.Context == nil {
		t.Fatal("CreateToolContext returned no ToolContext over an extension Context")
	}
	// ToolCallContext attaches both contexts to the context a tool call runs with (wrapper.ts:14-19).
	toolCtx := runner.ToolCallContext(parent, "call-1")
	if extension.ToolContextFromContext(toolCtx) == nil || extension.FromContext(toolCtx) == nil {
		t.Fatal("ToolCallContext attached no ToolContext or extension Context")
	}
	tools, err := tc.Tools()
	if err != nil || !reflect.DeepEqual(tools, callable) {
		t.Fatalf("Tools() = %v, %v", tools, err)
	}

	// runner.ts:967-981: the outcome of the bound action is returned unchanged.
	got, err := tc.ExecuteTool("echo", map[string]any{"a": 1}, nil)
	if err != nil || !reflect.DeepEqual(got, outcome) {
		t.Fatalf("ExecuteTool = %+v, %v", got, err)
	}
	if gotCaller != "call-1" || gotName != "echo" || string(gotArgs) != `{"a":1}` {
		t.Fatalf("action saw caller=%q name=%q args=%s", gotCaller, gotName, gotArgs)
	}
	// runner.ts:981: `{ ...options, signal: options.signal ?? signal }`: the calling tool's cancellation reaches the nested call.
	if gotCtx.Err() != nil {
		t.Fatal("nested call was cancelled before the calling tool")
	}
	cancel()
	if gotCtx.Err() == nil {
		t.Fatal("cancelling the calling tool did not cancel the nested call's default signal")
	}
	if extension.OwnsSignal(gotCtx) {
		t.Fatal("a nested call without a signal option was marked as running with its own")
	}

	// An explicit signal wins over the calling tool's.
	own, cancelOwn := context.WithCancel(context.Background())
	defer cancelOwn()
	if _, err := tc.ExecuteTool("echo", map[string]any{}, &extension.ExecuteToolOptions{Signal: own}); err != nil {
		t.Fatal(err)
	}
	if !extension.OwnsSignal(gotCtx) {
		t.Fatal("a nested call with an explicit signal was not marked as running with it")
	}
	if gotCtx.Err() != nil {
		t.Fatal("an explicit signal was replaced by the calling tool's already cancelled one")
	}
	cancelOwn()
	if gotCtx.Err() == nil {
		t.Fatal("the explicit signal did not reach the nested call")
	}
}

// Upstream runner.ts:966-980: without an executeTool action, nested calls come back as an error outcome with the id `<caller>/0`; upstream never rejects.
func TestRunnerToolContextWithoutExecuteToolActionReturnsErrorOutcome(t *testing.T) {
	runner := inproc.NewRunner(nil, t.TempDir())
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
	tc := runner.CreateToolContext(t.Context(), "call-7")
	if tc == nil {
		t.Fatal("CreateToolContext attached no ToolContext")
	}
	tools, err := tc.Tools()
	if err != nil || len(tools) != 0 {
		t.Fatalf("Tools() without getCallableTools = %v, %v; want an empty list (runner.ts:379)", tools, err)
	}
	got, err := tc.ExecuteTool("echo", map[string]any{"a": 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsError || got.ToolCall.ID != "call-7/0" || got.ToolCall.Name != "echo" {
		t.Fatalf("outcome = %+v", got)
	}
	result, ok := got.Result, true
	if !ok || !strings.Contains(result.Text(), "Nested tool calls are not available in this context") {
		t.Fatalf("result = %#v", got.Result)
	}
}

// Upstream runner.ts:955-964 and 967: `tools` and `executeTool` call `runner.assertActive()`, so a stale runner rejects both.
func TestRunnerToolContextRejectsCallsOnAStaleRunner(t *testing.T) {
	runner := inproc.NewRunner(nil, t.TempDir())
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{ToolActions: extension.ToolActions{
		ExecuteTool: func(context.Context, string, string, json.RawMessage, extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error) {
			t.Error("a stale runner ran a nested call")
			return extension.AgentToolCallOutcome{}, nil
		},
	}}, nil)
	tc := runner.CreateToolContext(t.Context(), "call-1")
	if tc == nil {
		t.Fatal("CreateToolContext attached no ToolContext")
	}
	runner.Invalidate("This extension ctx is stale after session replacement or reload.")
	if _, err := tc.Tools(); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("Tools() on a stale runner = %v", err)
	}
	if _, err := tc.ExecuteTool("echo", map[string]any{}, nil); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("ExecuteTool() on a stale runner = %v", err)
	}
}

// Upstream extensions/codemode/index.ts:35 closes over `pi.appendEntry`, which loader.ts:376-378 asserts active before it calls the runtime action bindCore installed from agent-session.ts:3321-3327. A declarative Go tool reaches that action through its tool context.
func TestRunnerToolContextAppendsEntriesThroughTheBoundAction(t *testing.T) {
	type call struct {
		customType string
		data       any
	}
	var calls []call
	runner := inproc.NewRunner(nil, t.TempDir())
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{ToolActions: extension.ToolActions{AppendEntry: func(customType string, data any) error {
		calls = append(calls, call{customType, data})
		return nil
	}}}, nil)
	tc := runner.CreateToolContext(t.Context(), "call-1")
	if tc == nil {
		t.Fatal("CreateToolContext attached no ToolContext")
	}
	if err := tc.AppendEntry("codemode-store", map[string]any{"set": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []call{{"codemode-store", map[string]any{"set": map[string]any{}}}}) {
		t.Fatalf("bound action calls = %+v", calls)
	}

	runner.Invalidate("This extension ctx is stale after session replacement or reload.")
	if err := tc.AppendEntry("codemode-store", nil); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("AppendEntry() on a stale runner = %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("a stale runner appended an entry: %+v", calls)
	}
}

// Upstream loader.ts:171 initializes `appendEntry` to `notInitialized`, which throws; a runner that never bound the action reports an error instead of dropping the entry.
func TestRunnerToolContextWithoutAppendEntryActionReportsAnError(t *testing.T) {
	runner := inproc.NewRunner(nil, t.TempDir())
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
	tc := runner.CreateToolContext(t.Context(), "call-1")
	if tc == nil {
		t.Fatal("CreateToolContext attached no ToolContext")
	}
	if err := tc.AppendEntry("codemode-store", nil); err == nil {
		t.Fatal("AppendEntry() without a bound action reported success")
	}
}

// namedManager is a comparable ReadonlySessionManager that only knows its id.
type namedManager struct {
	extension.ReadonlySessionManager
	id string
}

func (m namedManager) GetSessionId() string { return m.id }

// A Session that swaps its log in place rebinds ctx.sessionManager while handlers create contexts on other goroutines (runner.ts:889-891 reads the runner's manager; Go's ReplaceInner has no new runner to bind). Run under -race.
func TestRunnerBindSessionManagerWhileContextsAreCreated(t *testing.T) {
	runner := inproc.NewRunner(nil, t.TempDir())
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{SessionManager: namedManager{id: "first"}}, nil)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 200 {
				manager, err := runner.CreateCommandContext().SessionManager()
				if err != nil || (manager != namedManager{id: "first"} && manager != namedManager{id: "second"}) {
					t.Errorf("SessionManager() = %v, %v", manager, err)
					return
				}
			}
		})
	}
	runner.BindSessionManager(namedManager{id: "second"})
	wg.Wait()
	if manager, err := runner.CreateCommandContext().SessionManager(); err != nil || manager != (namedManager{id: "second"}) {
		t.Fatalf("after rebind SessionManager() = %v, %v", manager, err)
	}
}

// Upstream ctx.sessionManager is a runner constructor field (runner.ts:362, 398-405, 889-891), and a mode's session.bindExtensions reaches bindCore only through _bindExtensionCore (agent-session.ts:3173-3194, 3301), which binds executeTool, getCallableTools and appendEntry every time. A mode's own BindCore that omits them keeps the Session's; one that supplies them replaces them.
func TestRunnerBindCoreKeepsTheSessionBindingAModeOmits(t *testing.T) {
	var appended []string
	runner := inproc.NewRunner(nil, t.TempDir())
	var executed []string
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{SessionManager: namedManager{id: "session-log"}, ToolActions: extension.ToolActions{
		ExecuteTool: func(_ context.Context, callerID, name string, _ json.RawMessage, _ extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error) {
			executed = append(executed, name)
			return extension.AgentToolCallOutcome{ToolCall: ai.ToolCall{ID: callerID + "/1", Name: name}}, nil
		},
		AppendEntry: func(customType string, _ any) error {
			appended = append(appended, customType)
			return nil
		},
	}}, nil)
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
	tc := runner.CreateToolContext(t.Context(), "call-1")
	if manager, err := tc.SessionManager(); err != nil || manager != (namedManager{id: "session-log"}) {
		t.Fatalf("ctx.sessionManager after a mode binding = %v, %v; want the Session's log", manager, err)
	}
	if err := tc.AppendEntry("kept", nil); err != nil || !reflect.DeepEqual(appended, []string{"kept"}) {
		t.Fatalf("AppendEntry after a mode binding = %v, calls %v", err, appended)
	}
	if outcome, err := tc.ExecuteTool("nested", nil, nil); err != nil || outcome.IsError || !reflect.DeepEqual(executed, []string{"nested"}) {
		t.Fatalf("ExecuteTool after a mode binding = %+v, %v; Session action calls %v", outcome, err, executed)
	}

	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{SessionManager: namedManager{id: "next-log"}, ToolActions: extension.ToolActions{AppendEntry: func(customType string, _ any) error {
		appended = append(appended, "next:"+customType)
		return nil
	}}}, nil)
	tc = runner.CreateToolContext(t.Context(), "call-2")
	if manager, _ := tc.SessionManager(); manager != (namedManager{id: "next-log"}) {
		t.Fatalf("ctx.sessionManager after a Session binding = %v, want next-log", manager)
	}
	if err := tc.AppendEntry("replaced", nil); err != nil || !reflect.DeepEqual(appended, []string{"kept", "next:replaced"}) {
		t.Fatalf("AppendEntry after a Session binding = %v, calls %v", err, appended)
	}
}

type bindCoreRegistry struct {
	extension.ModelRegistry
	registered []string
}

func (r *bindCoreRegistry) RegisterExtensionProvider(name string, _ extension.ProviderConfig) error {
	r.registered = append(r.registered, name)
	return nil
}

func (r *bindCoreRegistry) UnregisterProvider(string) {}

// Upstream's ExtensionRunner takes the ModelRegistry as a constructor argument (runner.ts:363, 399-406), serves ctx.modelRegistry from it (runner.ts:835, 893-895) and rebinds extension provider registration to it on every bindCore (runner.ts:468-541); no bindCore replaces it, and a mode's bindExtensions reaches bindCore only through _bindExtensionCore (agent-session.ts:3185, 3287-3313). A mode's own BindCore that omits the registry keeps the Session's for ctx.modelRegistry. The pi.registerProvider check guards that the rebind it now triggers targets the Session's registry; it is not a red regression, because a binding without a registry left the runtime's provider actions untouched.
func TestRunnerBindCoreKeepsTheSessionModelRegistryAModeOmits(t *testing.T) {
	registry := &bindCoreRegistry{}
	runtime := extension.CreateExtensionRuntime()
	runner := inproc.NewRunner(nil, t.TempDir(), runtime)
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{ModelRegistry: registry}, nil)
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)

	tc := runner.CreateToolContext(t.Context(), "call-1")
	if got, err := tc.ModelRegistry(); err != nil || got != registry {
		t.Fatalf("ctx.modelRegistry after a mode binding = %v, %v; want the Session's registry", got, err)
	}
	if got, err := runner.CreateCommandContext().ModelRegistry(); err != nil || got != registry {
		t.Fatalf("command ctx.modelRegistry after a mode binding = %v, %v; want the Session's registry", got, err)
	}
	if err := runtime.RegisterProvider("late", extension.ProviderConfig{}); err != nil || !reflect.DeepEqual(registry.registered, []string{"late"}) {
		t.Fatalf("pi.registerProvider after a mode binding = %v, registered %v; want the Session's registry to receive it", err, registry.registered)
	}

	next := &bindCoreRegistry{}
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{ModelRegistry: next}, nil)
	if got, _ := runner.CreateCommandContext().ModelRegistry(); got != next {
		t.Fatalf("ctx.modelRegistry after a Session binding = %v, want the new registry", got)
	}
}
