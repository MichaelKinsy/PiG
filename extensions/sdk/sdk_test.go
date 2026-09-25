package sdk

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mockHost simulates the pig host side of the socket for testing.
type mockHost struct {
	nc       net.Conn
	listener net.Listener
	sockPath string
}

func newMockHost(t *testing.T) *mockHost {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "pig-sdk-test")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sockPath := filepath.Join(dir, "test.sock")
	_ = os.Remove(sockPath)

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	return &mockHost{
		listener: listener,
		sockPath: sockPath,
	}
}

func (h *mockHost) accept(t *testing.T) {
	t.Helper()
	nc, err := h.listener.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	h.nc = nc
}

func (h *mockHost) close() {
	if h.nc != nil {
		_ = h.nc.Close()
	}
	_ = h.listener.Close()
}

func (h *mockHost) readEnvelope(t *testing.T) envelope {
	t.Helper()
	for {
		env := h.readEnvelopeRaw(t)
		if env.Type != msgRequestState {
			return env
		}
	}
}

func (h *mockHost) readEnvelopeRaw(t *testing.T) envelope {
	t.Helper()
	var hdr [4]byte
	if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
		t.Fatalf("read header: %v", err)
	}
	size := binary.BigEndian.Uint32(hdr[:])
	data := make([]byte, size)
	if _, err := io.ReadFull(h.nc, data); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return env
}

func (h *mockHost) writeEnvelope(t *testing.T, env envelope) {
	t.Helper()
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	if _, err := h.nc.Write(hdr[:]); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := h.nc.Write(data); err != nil {
		t.Fatalf("write payload: %v", err)
	}
}

func TestModelInfoUnmarshalAcceptsProviderObjectAndCostObject(t *testing.T) {
	raw := []byte(`{
		"id":"claude-sonnet-4.5",
		"displayName":"Claude Sonnet 4.5",
		"provider":{"id":"github-copilot"},
		"contextWindow":200000,
		"maxTokens":8192,
		"cost":{"input":3,"output":15,"cacheRead":0.3,"cacheWrite":3.75}
	}`)
	var info ModelInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if info.Provider != "github-copilot" {
		t.Fatalf("Provider = %q", info.Provider)
	}
	if info.Name != "Claude Sonnet 4.5" {
		t.Fatalf("Name = %q", info.Name)
	}
	if info.MaxOutputTokens != 8192 {
		t.Fatalf("MaxOutputTokens = %d", info.MaxOutputTokens)
	}
	if info.InputCostPer1M != 3 || info.CacheWriteCostPer1M != 3.75 {
		t.Fatalf("costs = %+v", info)
	}
}

func TestStateUpdateModelQualifiedUsesProviderModelID(t *testing.T) {
	ext := New("test")
	ext.handleNotify(envelope{Notify: &notifyMsg{
		Method: "state_update",
		Args:   []byte(`{"state":{"model":{"id":"gpt-5.5","name":"GPT-5.5","provider":{"id":"github-copilot"}}}}`),
	}})

	ctx := Context{ext: ext}
	if got := ctx.ModelQualified(); got != "github-copilot/gpt-5.5" {
		t.Fatalf("ModelQualified() = %q, want github-copilot/gpt-5.5", got)
	}
}

func require084GetBranchSignature(t *testing.T, getBranch func(Context) []BranchEntry) {
	t.Helper()
	if getBranch == nil {
		t.Fatal("Context.GetBranch method expression is nil")
	}
}

func TestGetBranchKeeps084ValueSliceSignature(t *testing.T) {
	// PiG 0.84 exposed []BranchEntry. Existing source extensions must keep
	// compiling when the session mirror changes its internal representation.
	require084GetBranchSignature(t, Context.GetBranch)

	ext := New("branch-compat")
	ext.session.subscribed.Store(true)
	ext.session.seed([]json.RawMessage{json.RawMessage(`{"id":"entry-1","type":"message","message":{"role":"user","content":"hello"}}`)}, 1, "entry-1")
	first := Context{ext: ext}.GetBranch()
	if len(first) != 1 || first[0].Role != "user" {
		t.Fatalf("first branch = %+v", first)
	}
	first[0].Role = "changed"
	if second := (Context{ext: ext}).GetBranch(); len(second) != 1 || second[0].Role != "user" {
		t.Fatalf("caller mutation reached cached branch: %+v", second)
	}
}

func TestBranchEntryUnmarshalPreservesIDAndFirstKeptEntryID(t *testing.T) {
	raw := []byte(`{"type":"compaction","id":"leaf-1","summary":"summary","firstKeptEntryId":"kept-1"}`)
	var entry BranchEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if entry.ID != "leaf-1" || entry.Type != "compaction" || entry.FirstKeptEntryID != "kept-1" || entry.Content != "summary" {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestBranchEntryUnmarshalPreservesMessageID(t *testing.T) {
	raw := []byte(`{"type":"message","id":"leaf-1","message":{"role":"assistant","model":"claude-sonnet-4.5","usage":{"input":10,"output":2,"totalTokens":12}}}`)
	var entry BranchEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if entry.ID != "leaf-1" || entry.Role != "assistant" || entry.ModelID != "claude-sonnet-4.5" {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.Usage == nil || entry.Usage.TotalTokens != 12 {
		t.Fatalf("usage = %+v", entry.Usage)
	}
}

func TestExtension_RegisterAndReady(t *testing.T) {
	host := newMockHost(t)
	defer host.close()

	ext := New("test-ext")
	ext.Tool("greet", "Says hello", Schema{"type": "object"}, func(ctx Context, params map[string]any) (any, error) {
		return nil, nil
	})
	ext.Command("hello", "Say hi", func(ctx Context, args string) error {
		return nil
	})
	ext.OnSessionStart(func(ctx Context, data map[string]any) (any, error) {
		return nil, nil
	})
	ext.OnProjectTrust(func(Context, map[string]any) (ProjectTrustResult, error) {
		return ProjectTrustResult{Trusted: ProjectTrustUndecided}, nil
	})
	ext.OnProjectTrust(func(Context, map[string]any) (ProjectTrustResult, error) {
		return ProjectTrustResult{Trusted: ProjectTrustYes}, nil
	})
	ext.Flag("plan", FlagOptions{Description: "Plan mode", Type: FlagString, Default: "auto"})
	ext.RegisterProvider("custom", ProviderConfig{"baseUrl": "https://example.com", "api": "openai-completions"})
	ext.MessageRenderer("notice", func(ctx Context, message map[string]any, options MessageRenderOptions, width int) ([]string, error) {
		return []string{"notice"}, nil
	})

	// Set socket env and run in background.
	t.Setenv("PIG_EXT_SOCKET", host.sockPath)

	done := make(chan error, 1)
	go func() {
		done <- ext.Run()
	}()

	// Accept connection.
	host.accept(t)

	// Read register message.
	reg := host.readEnvelope(t)
	if reg.Type != "register" {
		t.Fatalf("expected register, got %s", reg.Type)
	}
	if reg.Register.Name != "test-ext" {
		t.Errorf("name = %q", reg.Register.Name)
	}
	if len(reg.Register.Tools) != 1 || reg.Register.Tools[0].Name != "greet" {
		t.Errorf("tools = %+v", reg.Register.Tools)
	}
	if len(reg.Register.Commands) != 1 || reg.Register.Commands[0].Name != "hello" {
		t.Errorf("commands = %+v", reg.Register.Commands)
	}
	if len(reg.Register.Handlers) != 3 || reg.Register.Handlers[0].Event != "session_start" ||
		reg.Register.Handlers[1].Event != "project_trust" || reg.Register.Handlers[2].Event != "project_trust" ||
		reg.Register.Handlers[0].HandlerID != 1 || reg.Register.Handlers[1].HandlerID != 2 || reg.Register.Handlers[2].HandlerID != 3 {
		t.Errorf("handlers = %+v", reg.Register.Handlers)
	}
	if len(reg.Register.Flags) != 1 || reg.Register.Flags[0].Name != "plan" {
		t.Errorf("flags = %+v", reg.Register.Flags)
	}
	if len(reg.Register.Providers) != 1 || reg.Register.Providers[0].Name != "custom" {
		t.Errorf("providers = %+v", reg.Register.Providers)
	}
	if len(reg.Register.Renderers) != 1 || reg.Register.Renderers[0].CustomType != "notice" {
		t.Errorf("renderers = %+v", reg.Register.Renderers)
	}

	// Send ready.
	host.writeEnvelope(t, envelope{
		Type: msgReady,
		Ready: &readyMsg{
			SessionName: "test-session",
			Cwd:         "/tmp/test",
			Width:       120,
			Model:       "claude-4-sonnet",
		},
	})

	// Small delay for ready to be processed.
	time.Sleep(50 * time.Millisecond)

	// Send shutdown.
	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "test"}})

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Run to exit")
	}
}

func TestContext_GetFlag_DefaultFallback(t *testing.T) {
	host := newMockHost(t)
	defer host.close()

	ext := New("test-ext")
	ext.Flag("plan", FlagOptions{Type: FlagString, Default: "auto"})
	valueCh := make(chan any, 1)
	ext.Command("flag", "Read flag", func(ctx Context, args string) error {
		valueCh <- ctx.GetFlag("plan")
		return nil
	})

	t.Setenv("PIG_EXT_SOCKET", host.sockPath)

	done := make(chan error, 1)
	go func() { done <- ext.Run() }()

	host.accept(t)
	host.readEnvelope(t)
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80}})
	time.Sleep(50 * time.Millisecond)

	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "req-flag", Request: &requestMsg{Method: "command", Tool: "flag"}})
	call := host.readEnvelope(t)
	if call.Type != msgCall || call.Call.Method != "getFlag" {
		t.Fatalf("expected getFlag call, got %+v", call)
	}
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{Result: json.RawMessage(`{"value":null}`)}})
	_ = host.readEnvelope(t)
	if got := <-valueCh; got != "auto" {
		t.Fatalf("GetFlag fallback = %#v, want %q", got, "auto")
	}

	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "done"}})
	<-done
}

func TestExtension_CommandResponseWaitsForHandlerCompletion(t *testing.T) {
	host := newMockHost(t)
	defer host.close()

	started := make(chan struct{})
	release := make(chan struct{})
	ext := New("test-ext")
	ext.Command("awaited", "Wait before completing", func(Context, string) error {
		close(started)
		<-release
		return errors.New("after-release")
	})

	t.Setenv("PIG_EXT_SOCKET", host.sockPath)
	done := make(chan error, 1)
	go func() { done <- ext.Run() }()
	host.accept(t)
	_ = host.readEnvelope(t)
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80}})
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "req-awaited", Request: &requestMsg{Method: "command", Tool: "awaited"}})
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for command start")
	}
	state := host.readEnvelopeRaw(t)
	if state.Type != msgRequestState || state.RequestState == nil || state.RequestState.RequestID != "req-awaited" || state.RequestState.State != "started" {
		t.Fatalf("request state before handler completion = %+v", state)
	}

	if err := host.nc.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var one [1]byte
	if _, err := host.nc.Read(one[:]); err == nil {
		t.Fatal("host received a response before the handler completed")
	} else if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("read before release: %v", err)
	}
	if err := host.nc.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	close(release)
	resp := host.readEnvelope(t)
	if resp.ID != "req-awaited" || resp.Response == nil || resp.Response.Error == nil || !strings.Contains(resp.Response.Error.Message, "after-release") {
		t.Fatalf("response after release = %+v", resp)
	}

	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "done"}})
	<-done
}

func TestExtension_ShutdownWaitsForCooperativeHandler(t *testing.T) {
	host := newMockHost(t)
	defer host.close()

	handlerDone := make(chan struct{})
	ext := New("test-ext")
	ext.Command("wait", "Wait for shutdown", func(ctx Context, _ string) error {
		<-ctx.Done()
		close(handlerDone)
		return ctx.Err()
	})
	t.Setenv("PIG_EXT_SOCKET", host.sockPath)
	runDone := make(chan error, 1)
	go func() { runDone <- ext.Run() }()
	host.accept(t)
	_ = host.readEnvelope(t)
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Width: 80}})
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "req-shutdown", Request: &requestMsg{Method: "command", Tool: "wait"}})
	state := host.readEnvelopeRaw(t)
	if state.Type != msgRequestState || state.RequestState == nil || state.RequestState.State != "started" {
		t.Fatalf("request start state = %+v", state)
	}
	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "test"}})
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("shutdown cancellation did not reach the handler")
	}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("extension did not return after draining its cancelled handler")
	}
}

func TestExtension_CancelRequestCancelsContext(t *testing.T) {
	host := newMockHost(t)
	defer host.close()

	started := make(chan struct{})
	doneCtx := make(chan error, 1)
	ext := New("test-ext")
	ext.Command("wait", "Wait for cancellation", func(ctx Context, args string) error {
		close(started)
		<-ctx.Done()
		doneCtx <- ctx.Err()
		return ctx.Err()
	})

	t.Setenv("PIG_EXT_SOCKET", host.sockPath)
	done := make(chan error, 1)
	go func() { done <- ext.Run() }()
	host.accept(t)
	_ = host.readEnvelope(t) // register
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80}})
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "req-wait", Request: &requestMsg{Method: "command", Tool: "wait"}})
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for command start")
	}
	host.writeEnvelope(t, envelope{Type: msgCancel, ID: "req-wait", Cancel: &cancelMsg{RequestID: "req-wait", Reason: "test"}})

	select {
	case err := <-doneCtx:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ctx err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for context cancellation")
	}
	resp := host.readEnvelope(t)
	if resp.Type != msgResponse || resp.ID != "req-wait" || resp.Response == nil || resp.Response.Error == nil {
		t.Fatalf("cancelled response = %+v, want response error", resp)
	}
	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "done"}})
	<-done
}

func TestExtension_RenderMessageRequest(t *testing.T) {
	host := newMockHost(t)
	defer host.close()

	ext := New("test-ext")
	ext.MessageRenderer("notice", func(ctx Context, message map[string]any, options MessageRenderOptions, width int) ([]string, error) {
		return []string{message["content"].(string)}, nil
	})

	t.Setenv("PIG_EXT_SOCKET", host.sockPath)

	done := make(chan error, 1)
	go func() { done <- ext.Run() }()

	host.accept(t)
	host.readEnvelope(t)
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80}})
	time.Sleep(50 * time.Millisecond)

	args, _ := json.Marshal(map[string]any{
		"message": map[string]any{"content": "hello"},
		"options": map[string]any{"expanded": true},
		"width":   80,
	})
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "req-render", Request: &requestMsg{Method: "render_message", Tool: "notice", Args: args}})
	resp := host.readEnvelope(t)
	if resp.Type != msgResponse {
		t.Fatalf("expected response, got %s", resp.Type)
	}
	var out struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(resp.Response.Result, &out); err != nil {
		t.Fatalf("unmarshal render result: %v", err)
	}
	if len(out.Lines) != 1 || out.Lines[0] != "hello" {
		t.Fatalf("render lines = %v", out.Lines)
	}

	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "done"}})
	<-done
}

func TestExtension_ToolExecution(t *testing.T) {
	host := newMockHost(t)
	defer host.close()

	ext := New("test-ext")
	ext.Tool("greet", "Says hello", Schema{"type": "object"}, func(ctx Context, params map[string]any) (any, error) {
		name, _ := params["name"].(string)
		return map[string]any{"greeting": "Hello, " + name + "!"}, nil
	})

	t.Setenv("PIG_EXT_SOCKET", host.sockPath)

	done := make(chan error, 1)
	go func() { done <- ext.Run() }()

	host.accept(t)
	host.readEnvelope(t) // register

	// Send ready.
	host.writeEnvelope(t, envelope{
		Type:  msgReady,
		Ready: &readyMsg{Cwd: "/tmp", Width: 80},
	})
	time.Sleep(50 * time.Millisecond)

	// Send tool request.
	params, _ := json.Marshal(map[string]string{"name": "World"})
	host.writeEnvelope(t, envelope{
		Type: msgRequest,
		ID:   "req-1",
		Request: &requestMsg{
			Method:     "tool_call",
			Tool:       "greet",
			ToolCallID: "tc-1",
			Args:       params,
		},
	})

	// Read response.
	resp := host.readEnvelope(t)
	if resp.Type != "response" {
		t.Fatalf("expected response, got %s", resp.Type)
	}
	if resp.ID != "req-1" {
		t.Errorf("response ID = %q", resp.ID)
	}
	if resp.Response.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Response.Error)
	}

	var result map[string]any
	_ = json.Unmarshal(resp.Response.Result, &result)
	if result["greeting"] != "Hello, World!" {
		t.Errorf("result = %v", result)
	}

	// Shutdown.
	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "done"}})
	<-done
}

func TestExtension_HandlerPanicIsolated(t *testing.T) {
	host := newMockHost(t)
	defer host.close()

	ext := New("test-ext")
	ext.Tool("boom", "Panics", Schema{"type": "object"}, func(ctx Context, params map[string]any) (any, error) {
		var p *int
		return *p, nil // nil deref -> panic
	})
	ext.Tool("greet", "Says hello", Schema{"type": "object"}, func(ctx Context, params map[string]any) (any, error) {
		return map[string]any{"greeting": "hi"}, nil
	})

	t.Setenv("PIG_EXT_SOCKET", host.sockPath)

	done := make(chan error, 1)
	go func() { done <- ext.Run() }()

	host.accept(t)
	host.readEnvelope(t) // register
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80}})
	time.Sleep(50 * time.Millisecond)

	// A panicking handler must fail only its own request, with an error response.
	host.writeEnvelope(t, envelope{
		Type:    msgRequest,
		ID:      "req-boom",
		Request: &requestMsg{Method: "tool_call", Tool: "boom", ToolCallID: "tc-boom"},
	})
	resp := host.readEnvelope(t)
	if resp.ID != "req-boom" {
		t.Fatalf("response ID = %q, want req-boom", resp.ID)
	}
	if resp.Response == nil || resp.Response.Error == nil {
		t.Fatalf("panicking handler must return an error response, got %+v", resp.Response)
	}

	// The extension must still be alive and serve subsequent requests.
	params, _ := json.Marshal(map[string]string{})
	host.writeEnvelope(t, envelope{
		Type:    msgRequest,
		ID:      "req-ok",
		Request: &requestMsg{Method: "tool_call", Tool: "greet", ToolCallID: "tc-ok", Args: params},
	})
	resp2 := host.readEnvelope(t)
	if resp2.ID != "req-ok" || resp2.Response == nil || resp2.Response.Error != nil {
		t.Fatalf("extension did not survive a handler panic; response = %+v", resp2)
	}

	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "done"}})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ext.Run did not return after shutdown")
	}
}

func TestExtension_CommandExecution(t *testing.T) {
	host := newMockHost(t)
	defer host.close()

	var receivedArgs string
	ext := New("test-ext")
	ext.Command("hello", "Say hi", func(ctx Context, args string) error {
		receivedArgs = args
		return nil
	})

	t.Setenv("PIG_EXT_SOCKET", host.sockPath)

	done := make(chan error, 1)
	go func() { done <- ext.Run() }()

	host.accept(t)
	host.readEnvelope(t) // register
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80}})
	time.Sleep(50 * time.Millisecond)

	// Send command.
	argsJSON, _ := json.Marshal("world --flag")
	host.writeEnvelope(t, envelope{
		Type: msgRequest,
		ID:   "req-2",
		Request: &requestMsg{
			Method: "command",
			Tool:   "hello",
			Args:   argsJSON,
		},
	})

	// Read response.
	resp := host.readEnvelope(t)
	if resp.Type != "response" {
		t.Fatalf("expected response, got %s", resp.Type)
	}
	if resp.Response.Error != nil {
		t.Fatalf("error: %v", resp.Response.Error)
	}
	if receivedArgs != "world --flag" {
		t.Errorf("args = %q", receivedArgs)
	}

	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "done"}})
	<-done
}

func TestExtension_NotifyCallback(t *testing.T) {
	host := newMockHost(t)
	defer host.close()

	ext := New("test-ext")
	var notified bool
	ext.Tool("ping", "Ping", nil, func(ctx Context, params map[string]any) (any, error) {
		ctx.Notify("pong", "info")
		notified = true
		return "ok", nil
	})

	t.Setenv("PIG_EXT_SOCKET", host.sockPath)

	done := make(chan error, 1)
	go func() { done <- ext.Run() }()

	host.accept(t)
	host.readEnvelope(t) // register
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80}})
	time.Sleep(50 * time.Millisecond)

	// Send tool request (trigger notify inside handler).
	host.writeEnvelope(t, envelope{
		Type:    msgRequest,
		ID:      "req-3",
		Request: &requestMsg{Method: "tool_call", Tool: "ping", ToolCallID: "tc-3"},
	})

	// Read the extension's call (ui.notify).
	callEnv := host.readEnvelope(t)
	if callEnv.Type != "call" {
		t.Fatalf("expected call, got %s", callEnv.Type)
	}
	if callEnv.Call.Method != "ui.notify" {
		t.Errorf("method = %q", callEnv.Call.Method)
	}

	// Send call_result.
	host.writeEnvelope(t, envelope{
		Type:       msgCallResult,
		ID:         callEnv.ID,
		CallResult: &callResultMsg{},
	})

	// Read tool response.
	resp := host.readEnvelope(t)
	if resp.Type != "response" {
		t.Fatalf("expected response, got %s", resp.Type)
	}

	if !notified {
		t.Error("notify was not called")
	}

	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "done"}})
	<-done
}

func TestContextSelectSurfacesHostUIFailure(t *testing.T) {
	host := newMockHost(t)
	defer host.close()

	ext := New("test-ext")
	ext.Command("ask", "Ask", func(ctx Context, _ string) error {
		_, _, err := ctx.Select("Pick", []string{"one"})
		return err
	})

	t.Setenv("PIG_EXT_SOCKET", host.sockPath)
	done := make(chan error, 1)
	go func() { done <- ext.Run() }()

	host.accept(t)
	host.readEnvelope(t)
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80}})
	host.writeEnvelope(t, envelope{
		Type:    msgRequest,
		ID:      "req-ui-error",
		Request: &requestMsg{Method: "command", Tool: "ask"},
	})

	call := host.readEnvelope(t)
	if call.Type != msgCall || call.Call.Method != "ui.select" {
		t.Fatalf("call = %+v", call)
	}
	host.writeEnvelope(t, envelope{
		Type: msgCallResult,
		ID:   call.ID,
		CallResult: &callResultMsg{Error: &errorInfo{
			Code:    "ui_error",
			Message: "dialog transport failed",
		}},
	})

	resp := host.readEnvelope(t)
	if resp.Response == nil || resp.Response.Error == nil || !strings.Contains(resp.Response.Error.Message, "dialog transport failed") {
		t.Fatalf("response = %+v", resp.Response)
	}
	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "done"}})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestExtension_WidgetPush(t *testing.T) {
	host := newMockHost(t)
	defer host.close()

	ext := New("test-ext")
	ext.Tool("widget", "Push widget", nil, func(ctx Context, params map[string]any) (any, error) {
		if err := ctx.SetWidget("status", []string{"● 3 agents running"}); err != nil {
			return nil, err
		}
		return "ok", nil
	})

	t.Setenv("PIG_EXT_SOCKET", host.sockPath)

	done := make(chan error, 1)
	go func() { done <- ext.Run() }()

	host.accept(t)
	host.readEnvelope(t) // register
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80}})
	time.Sleep(50 * time.Millisecond)

	// Trigger tool that pushes widget.
	host.writeEnvelope(t, envelope{
		Type:    msgRequest,
		ID:      "req-4",
		Request: &requestMsg{Method: "tool_call", Tool: "widget", ToolCallID: "tc-4"},
	})

	// Read widget_push.
	pushEnv := host.readEnvelope(t)
	if pushEnv.Type != "widget_push" {
		t.Fatalf("expected widget_push, got %s", pushEnv.Type)
	}
	if pushEnv.WidgetPush.Key != "status" {
		t.Errorf("key = %q", pushEnv.WidgetPush.Key)
	}
	if len(pushEnv.WidgetPush.Lines) != 1 || pushEnv.WidgetPush.Lines[0] != "● 3 agents running" {
		t.Errorf("lines = %v", pushEnv.WidgetPush.Lines)
	}

	// Read tool response.
	resp := host.readEnvelope(t)
	if resp.Type != "response" {
		t.Fatalf("expected response, got %s", resp.Type)
	}

	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "done"}})
	<-done
}

func TestParam(t *testing.T) {
	params := map[string]any{
		"name":  "hello",
		"count": float64(42), // JSON numbers are float64
		"flag":  true,
	}

	if got := Param[string](params, "name"); got != "hello" {
		t.Errorf("Param[string] = %q, want hello", got)
	}
	if got := Param[float64](params, "count"); got != 42 {
		t.Errorf("Param[float64] = %v, want 42", got)
	}
	if got := Param[bool](params, "flag"); got != true {
		t.Errorf("Param[bool] = %v, want true", got)
	}
	// Missing key.
	if got := Param[string](params, "missing"); got != "" {
		t.Errorf("Param[string](missing) = %q, want empty", got)
	}
	// Wrong type.
	if got := Param[int](params, "name"); got != 0 {
		t.Errorf("Param[int](name) = %v, want 0", got)
	}
}

func TestToolError(t *testing.T) {
	err := NewToolError("file not found: /tmp/foo.go")
	if err.Error() != "file not found: /tmp/foo.go" {
		t.Errorf("Error() = %q", err.Error())
	}
	// Verify it satisfies error interface.
	var e error = err
	if e.Error() != "file not found: /tmp/foo.go" {
		t.Errorf("interface Error() = %q", e.Error())
	}
}

func TestBranchEntryUnmarshal(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  BranchEntry
	}{
		{
			name:  "assistant message with usage and cache",
			input: `{"type":"message","id":"abc","parentId":"xyz","timestamp":"2026-01-01T00:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":"hello"}],"usage":{"input":3091,"output":475,"cacheRead":2500,"cacheWrite":9605,"totalTokens":15671}}}`,
			want: BranchEntry{
				Type:    "message",
				Role:    "assistant",
				Content: "hello",
				Usage: &UsageInfo{
					Input:      3091,
					Output:     475,
					CacheRead:  2500,
					CacheWrite: 9605,
				},
			},
		},
		{
			name:  "user message with string content",
			input: `{"type":"message","id":"def","message":{"role":"user","content":"hi there"}}`,
			want: BranchEntry{
				Type:    "message",
				Role:    "user",
				Content: "hi there",
			},
		},
		{
			name:  "assistant with tool calls",
			input: `{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"let me check"},{"type":"tool_use","name":"bash","id":"tc1","input":{"command":"ls"}}],"usage":{"input":100,"output":50}}}`,
			want: BranchEntry{
				Type:    "message",
				Role:    "assistant",
				Content: "let me check",
				Usage:   &UsageInfo{Input: 100, Output: 50},
				ToolCalls: []ToolCallInfo{
					{Name: "bash", ID: "tc1", Args: `{"command":"ls"}`},
				},
			},
		},
		{
			name:  "compaction entry",
			input: `{"type":"compaction","summary":"session summary here"}`,
			want: BranchEntry{
				Type:    "compaction",
				Content: "session summary here",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got BranchEntry
			if err := json.Unmarshal([]byte(tt.input), &got); err != nil {
				t.Fatalf("UnmarshalJSON: %v", err)
			}
			if got.Type != tt.want.Type {
				t.Errorf("Type = %q, want %q", got.Type, tt.want.Type)
			}
			if got.Role != tt.want.Role {
				t.Errorf("Role = %q, want %q", got.Role, tt.want.Role)
			}
			if got.Content != tt.want.Content {
				t.Errorf("Content = %q, want %q", got.Content, tt.want.Content)
			}
			if tt.want.Usage != nil {
				if got.Usage == nil {
					t.Fatal("Usage is nil, want non-nil")
				}
				if got.Usage.Input != tt.want.Usage.Input {
					t.Errorf("Usage.Input = %d, want %d", got.Usage.Input, tt.want.Usage.Input)
				}
				if got.Usage.Output != tt.want.Usage.Output {
					t.Errorf("Usage.Output = %d, want %d", got.Usage.Output, tt.want.Usage.Output)
				}
				if got.Usage.CacheRead != tt.want.Usage.CacheRead {
					t.Errorf("Usage.CacheRead = %d, want %d", got.Usage.CacheRead, tt.want.Usage.CacheRead)
				}
				if got.Usage.CacheWrite != tt.want.Usage.CacheWrite {
					t.Errorf("Usage.CacheWrite = %d, want %d", got.Usage.CacheWrite, tt.want.Usage.CacheWrite)
				}
			}
			if len(tt.want.ToolCalls) > 0 {
				if len(got.ToolCalls) != len(tt.want.ToolCalls) {
					t.Fatalf("ToolCalls len = %d, want %d", len(got.ToolCalls), len(tt.want.ToolCalls))
				}
				for i, tc := range tt.want.ToolCalls {
					if got.ToolCalls[i].Name != tc.Name {
						t.Errorf("ToolCalls[%d].Name = %q, want %q", i, got.ToolCalls[i].Name, tc.Name)
					}
					if got.ToolCalls[i].ID != tc.ID {
						t.Errorf("ToolCalls[%d].ID = %q, want %q", i, got.ToolCalls[i].ID, tc.ID)
					}
					if got.ToolCalls[i].Args != tc.Args {
						t.Errorf("ToolCalls[%d].Args = %q, want %q", i, got.ToolCalls[i].Args, tc.Args)
					}
				}
			}
		})
	}
}

func TestExtensionHeartbeatAndRequestStateBypassHandlers(t *testing.T) {
	host := newMockHost(t)
	defer host.close()

	ext := New("liveness-test")
	ext.Command("done", "complete immediately", func(Context, string) error { return nil })
	t.Setenv("PIG_EXT_SOCKET", host.sockPath)
	done := make(chan error, 1)
	go func() { done <- ext.Run() }()
	host.accept(t)
	_ = host.readEnvelope(t)
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80}})

	host.writeEnvelope(t, envelope{Type: msgPing, Ping: &pingMsg{Nonce: "heartbeat-1"}})
	pong := host.readEnvelopeRaw(t)
	if pong.Type != msgPong || pong.Pong == nil || pong.Pong.Nonce != "heartbeat-1" {
		t.Fatalf("heartbeat reply = %+v", pong)
	}

	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "req-state", Request: &requestMsg{Method: "command", Tool: "done"}})
	started := host.readEnvelopeRaw(t)
	completed := host.readEnvelopeRaw(t)
	response := host.readEnvelopeRaw(t)
	if started.RequestState == nil || started.RequestState.State != "started" || started.RequestState.RequestID != "req-state" {
		t.Fatalf("started state = %+v", started)
	}
	if completed.RequestState == nil || completed.RequestState.State != "completed" || completed.RequestState.RequestID != "req-state" {
		t.Fatalf("completed state = %+v", completed)
	}
	if response.Type != msgResponse || response.ID != "req-state" {
		t.Fatalf("response = %+v", response)
	}

	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "done"}})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestContextUserWaitReportsBlockedAndParentsHostCall(t *testing.T) {
	host := newMockHost(t)
	defer host.close()
	ext := New("blocked-test")
	ext.Command("ask", "wait for input", func(ctx Context, _ string) error {
		_, _, err := ctx.Input("Question", "Answer")
		return err
	})
	t.Setenv("PIG_EXT_SOCKET", host.sockPath)
	done := make(chan error, 1)
	go func() { done <- ext.Run() }()
	host.accept(t)
	_ = host.readEnvelope(t)
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80}})
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "req-user", Request: &requestMsg{Method: "command", Tool: "ask"}})

	started := host.readEnvelopeRaw(t)
	blocked := host.readEnvelopeRaw(t)
	call := host.readEnvelopeRaw(t)
	if started.RequestState == nil || started.RequestState.State != "started" {
		t.Fatalf("started = %+v", started)
	}
	if blocked.RequestState == nil || blocked.RequestState.State != "blocked" || blocked.RequestState.Reason != "user" {
		t.Fatalf("blocked = %+v", blocked)
	}
	if call.Call == nil || call.Call.Method != "ui.input" || call.Call.ParentRequestID != "req-user" {
		t.Fatalf("parented host call = %+v", call)
	}
	host.writeEnvelope(t, envelope{Type: msgPing, Ping: &pingMsg{Nonce: "while-blocked"}})
	if pong := host.readEnvelopeRaw(t); pong.Type != msgPong || pong.Pong == nil || pong.Pong.Nonce != "while-blocked" {
		t.Fatalf("blocked-handler pong = %+v", pong)
	}
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{Result: json.RawMessage(`{"text":"ok","ok":true}`)}})
	progress := host.readEnvelopeRaw(t)
	completed := host.readEnvelopeRaw(t)
	response := host.readEnvelopeRaw(t)
	if progress.RequestState == nil || progress.RequestState.State != "progress" {
		t.Fatalf("progress = %+v", progress)
	}
	if completed.RequestState == nil || completed.RequestState.State != "completed" || response.Type != msgResponse {
		t.Fatalf("completed/response = %+v / %+v", completed, response)
	}
	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "done"}})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
