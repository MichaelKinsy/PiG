package sdk

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

const builtinReply = `{"content":[{"type":"text","text":"file body"}],"details":{"truncation":null}}`

// builtinToolForTest is the definition create<Tool>ToolDefinition would return for spec, without the host call that fetches its metadata.
func builtinToolForTest(t *testing.T, spec builtinSpec) BuiltinToolDefinition {
	t.Helper()
	def := BuiltinToolDefinition{spec: spec}
	def.Name, def.Label, def.Description, def.Parameters = spec.name, spec.name, spec.name, Schema{"type": "object"}
	def.Execute = func(ctx Context, params map[string]any) (any, error) {
		return spec.execute(&ctx, ctx.toolCallID, params, nil, true, nil)
	}
	return def
}

// Pi's `definition.execute(...args)` forwards the tool call id, the calling request's cancellation and the context: the call goes out under the calling request
// (so the request's cancellation cancels it), carries the context and the options' announced callbacks, and its result is the tool's own.
func TestBuiltinToolExecuteForwardsTheCallingRequest(t *testing.T) {
	ext := New("builtin")
	spec, err := readSpec("/factory", &ReadToolOptions{Operations: &ReadOperations{
		ReadFile: func(string) ([]byte, error) { return nil, nil },
		Access:   func(string) error { return nil },
	}})
	if err != nil {
		t.Fatal(err)
	}
	def := builtinToolForTest(t, spec)
	def.Name = "read-probe"
	ext.RegisterTool(def.ToolDefinition)
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	calls, resp := runSurfaceTool(t, host, "read-probe", "call-1", func(*callMsg) *callResultMsg {
		return &callResultMsg{Result: json.RawMessage(builtinReply)}
	})
	if resp.Error != nil {
		t.Fatalf("tool failed: %+v", resp.Error)
	}
	if got := string(resp.Result); got != `{"content":[{"type":"text","text":"file body"}],"details":{"truncation":null}}` {
		t.Fatalf("tool result = %s", got)
	}
	if len(calls) != 1 || calls[0].Method != callBuiltinToolExecute || calls[0].ParentRequestID != "req-call-1" {
		t.Fatalf("calls = %+v, want one builtinTool.execute under the calling request", calls)
	}
	var args struct {
		Name       string `json:"name"`
		Cwd        string `json:"cwd"`
		ToolCallID string `json:"toolCallId"`
		Context    bool   `json:"context"`
		Callbacks  string `json:"callbacks"`
		Aborted    bool   `json:"aborted"`
		Options    struct {
			Operations []string `json:"operations"`
		} `json:"options"`
	}
	if err := json.Unmarshal(calls[0].Args, &args); err != nil {
		t.Fatal(err)
	}
	if args.Name != "read" || args.Cwd != "/factory" || args.ToolCallID != "call-1" || !args.Context || args.Callbacks == "" || args.Aborted ||
		strings.Join(args.Options.Operations, ",") != "readFile,access" {
		t.Fatalf("args = %s", calls[0].Args)
	}
}

// A signal other than the request's detaches the call from the request, and a signal that is aborted before the call is announced with the call, so a tool that
// finishes at once cannot beat the abort.
func TestBuiltinToolExecuteWithDetachesAndAnnouncesAnAbortedSignal(t *testing.T) {
	ext := New("builtin")
	spec, _ := bashSpec("/factory", nil)
	ext.RegisterTool(ToolDefinition{Name: "probe", Label: "probe", Description: "probe", Parameters: Schema{"type": "object"}, Execute: func(ctx Context, _ map[string]any) (any, error) {
		signal, cancel := context.WithCancel(context.Background())
		cancel()
		return BuiltinToolDefinition{spec: spec}.ExecuteWith(ctx, "explicit-id", map[string]any{"command": "true"}, signal, nil)
	}})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	calls, resp := runSurfaceTool(t, host, "probe", "call-2", func(call *callMsg) *callResultMsg {
		if call.Method == callExecuteToolCancel {
			return &callResultMsg{}
		}
		return &callResultMsg{Result: json.RawMessage(builtinReply)}
	})
	if resp.Error != nil {
		t.Fatalf("tool failed: %+v", resp.Error)
	}
	var execute *callMsg
	for _, call := range calls {
		if call.Method == callBuiltinToolExecute {
			execute = call
		}
	}
	if execute == nil || execute.ParentRequestID != "" {
		t.Fatalf("calls = %+v, want a builtinTool.execute outside the request", calls)
	}
	var args struct {
		ToolCallID string `json:"toolCallId"`
		Aborted    bool   `json:"aborted"`
	}
	if err := json.Unmarshal(execute.Args, &args); err != nil || args.ToolCallID != "explicit-id" || !args.Aborted {
		t.Fatalf("args = %s (%v), want the explicit id and aborted", execute.Args, err)
	}
}

// The host calls the options' functions back by handle while the call runs: binary content travels as base64, a function's error is the callback's throw, and a
// handle that is gone is refused.
func TestBuiltinToolCallbacksRunTheOptionsFunctions(t *testing.T) {
	var written []string
	spec, err := readSpec("/factory", &ReadToolOptions{Operations: &ReadOperations{
		ReadFile: func(path string) ([]byte, error) { return []byte("bytes of " + path), nil },
		Access:   func(path string) error { return errors.New("denied: " + path) },
	}})
	if err != nil {
		t.Fatal(err)
	}
	editSpec, _ := editSpec("/factory", &EditToolOptions{Operations: &EditOperations{
		ReadFile:  func(string) ([]byte, error) { return nil, nil },
		WriteFile: func(path, content string) error { written = append(written, path+"="+content); return nil },
		Access:    func(string) error { return nil },
	}})
	handle, release := registerBuiltinCallbacks(spec.callbacks)
	editHandle, editRelease := registerBuiltinCallbacks(editSpec.callbacks)
	ext := New("builtin")
	reply, err := ext.dispatchBuiltinToolCallback(json.RawMessage(`{"handle":"` + handle + `","member":"operations.readFile","args":{"path":"/a b"}}`))
	if err != nil || reply != base64.StdEncoding.EncodeToString([]byte("bytes of /a b")) {
		t.Fatalf("readFile = %v, %v", reply, err)
	}
	if _, err := ext.dispatchBuiltinToolCallback(json.RawMessage(`{"handle":"` + handle + `","member":"operations.access","args":{"path":"/x"}}`)); err == nil || err.Error() != "denied: /x" {
		t.Fatalf("access error = %v, want the function's", err)
	}
	if _, err := ext.dispatchBuiltinToolCallback(json.RawMessage(`{"handle":"` + editHandle + `","member":"operations.writeFile","args":{"path":"/f","content":"c\n"}}`)); err != nil || len(written) != 1 || written[0] != "/f=c\n" {
		t.Fatalf("writeFile = %v, %v", written, err)
	}
	release()
	editRelease()
	if _, err := ext.dispatchBuiltinToolCallback(json.RawMessage(`{"handle":"` + handle + `","member":"operations.readFile","args":{"path":"/a"}}`)); err == nil {
		t.Fatal("a released handle was served")
	}
}

// Pi's operations objects are complete: a missing required member is refused before the call, as TypeScript refuses it at compile time.
func TestBuiltinToolOperationsMissingAMemberAreRefused(t *testing.T) {
	if _, err := readSpec("/c", &ReadToolOptions{Operations: &ReadOperations{ReadFile: func(string) ([]byte, error) { return nil, nil }}}); err == nil || !strings.Contains(err.Error(), "operations.access is required") {
		t.Fatalf("err = %v", err)
	}
	if _, err := bashSpec("/c", &BashToolOptions{Operations: &BashOperations{}}); err == nil || !strings.Contains(err.Error(), "operations.exec is required") {
		t.Fatalf("err = %v", err)
	}
	// detectImageMimeType and grep's readFile are optional.
	spec, err := grepSpec("/c", &GrepToolOptions{Operations: &GrepOperations{IsDirectory: func(string) (bool, error) { return true, nil }}})
	if err != nil || strings.Join(spec.options.Operations, ",") != "isDirectory" {
		t.Fatalf("grep spec = %+v, %v", spec.options, err)
	}
}

// A result of a built-in tool is returned from a handler, or passed to OnUpdate, as it is: it marshals as the wire object a tool returns, and an empty text
// block keeps its text member.
func TestAgentToolResultMarshalsAsAToolResult(t *testing.T) {
	var result AgentToolResult
	if err := json.Unmarshal([]byte(`{"content":[{"type":"text","text":""},{"type":"image","data":"AAAA","mimeType":"image/png"}],"details":{"a":1},"structuredContent":{"exit_code":0},"isError":true}`), &result); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"content":[{"type":"text","text":""},{"type":"image","data":"AAAA","mimeType":"image/png"}],"details":{"a":1},"structured_content":{"exit_code":0},"is_error":true}`
	if string(encoded) != want {
		t.Fatalf("encoded = %s, want %s", encoded, want)
	}
}
