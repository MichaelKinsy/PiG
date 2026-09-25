package subprocess

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// ── Protocol framing tests ───────────────────────────────────────────────────

func TestConn_SendAndReceive(t *testing.T) {
	// Create a Unix socket pair.
	sockDir := t.TempDir()
	sockPath := filepath.Join(sockDir, "test.sock")

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()
	clientConn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	serverConn, err := listener.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Wrap the server side with Conn.
	conn := NewConn("test-ext", serverConn)
	conn.Start(ctx)
	defer func() { _ = conn.Close("test done") }()

	// Send a message from host (via Conn) to client.
	env := &Envelope{
		Type: MsgReady,
		Ready: &ReadyPayload{
			Cwd:   "/tmp/test",
			Width: 80,
		},
	}
	if err := conn.Send(env); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Read from client side using raw framing.
	var lenBuf [4]byte
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := clientConn.Read(lenBuf[:]); err != nil {
		t.Fatalf("read len: %v", err)
	}
	msgLen := binary.BigEndian.Uint32(lenBuf[:])
	if msgLen == 0 || msgLen > 1024*1024 {
		t.Fatalf("unexpected length: %d", msgLen)
	}

	buf := make([]byte, msgLen)
	if _, err := clientConn.Read(buf); err != nil {
		t.Fatalf("read payload: %v", err)
	}

	var received Envelope
	if err := json.Unmarshal(buf, &received); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if received.Type != MsgReady {
		t.Errorf("type = %q, want %q", received.Type, MsgReady)
	}
	if received.Ready == nil {
		t.Fatal("Ready payload is nil")
	}
	if received.Ready.Cwd != "/tmp/test" {
		t.Errorf("cwd = %q, want %q", received.Ready.Cwd, "/tmp/test")
	}
	if received.Ready.Width != 80 {
		t.Errorf("width = %d, want 80", received.Ready.Width)
	}
}

func TestHostReload_UsesConfigLoader(t *testing.T) {
	h := NewHost(t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) {
		return nil, fmt.Errorf("boom")
	})
	_, err := h.Reload(context.Background())
	if err == nil || err.Error() != `reload config: boom` {
		t.Fatalf("Reload error = %v, want reload config: boom", err)
	}
}

func TestConn_RequestResponse(t *testing.T) {
	sockDir := t.TempDir()
	sockPath := filepath.Join(sockDir, "test.sock")

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()

	clientConn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	serverConn, err := listener.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn := NewConn("test-ext", serverConn)
	conn.Start(ctx)
	defer func() { _ = conn.Close("test done") }()

	// Simulate: host sends request, client responds.
	// Start the request in a goroutine (it blocks waiting for response).
	type result struct {
		env *Envelope
		err error
	}
	respCh := make(chan result, 1)
	go func() {
		env, err := conn.Request(ctx, &Envelope{
			Type: MsgRequest,
			Request: &RequestPayload{
				Method: "tool_call",
				Tool:   "dispatch_subagent",
				Args:   json.RawMessage(`{"agent":"worker","task":"fix it"}`),
			},
		})
		respCh <- result{env, err}
	}()

	// Client reads the request.
	var lenBuf [4]byte
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := clientConn.Read(lenBuf[:]); err != nil {
		t.Fatalf("read request len: %v", err)
	}
	msgLen := binary.BigEndian.Uint32(lenBuf[:])
	buf := make([]byte, msgLen)
	if _, err := clientConn.Read(buf); err != nil {
		t.Fatalf("read request payload: %v", err)
	}

	var req Envelope
	if err := json.Unmarshal(buf, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if req.ID == "" {
		t.Fatal("request has no ID")
	}
	if req.Request == nil || req.Request.Tool != "dispatch_subagent" {
		t.Fatalf("unexpected request: %+v", req.Request)
	}

	// Client sends response.
	resp := Envelope{
		Type: MsgResponse,
		ID:   req.ID,
		Response: &ResponsePayload{
			Result: json.RawMessage(`{"content":"Dispatched worker (S1) in background."}`),
		},
	}
	respData, _ := json.Marshal(resp)
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(respData)))
	_, _ = clientConn.Write(lenBuf[:])
	_, _ = clientConn.Write(respData)

	// Host receives the response.
	select {
	case r := <-respCh:
		if r.err != nil {
			t.Fatalf("Request error: %v", r.err)
		}
		if r.env.Response == nil {
			t.Fatal("Response is nil")
		}
		var toolResult ToolResult
		if err := json.Unmarshal(r.env.Response.Result, &toolResult); err != nil {
			t.Fatalf("unmarshal tool result: %v", err)
		}
		if toolResult.Content != "Dispatched worker (S1) in background." {
			t.Errorf("content = %q, want %q", toolResult.Content, "Dispatched worker (S1) in background.")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for response")
	}
}

func TestConn_IncomingCallMessages(t *testing.T) {
	sockDir := t.TempDir()
	sockPath := filepath.Join(sockDir, "test.sock")

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()

	clientConn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	serverConn, err := listener.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn := NewConn("test-ext", serverConn)
	conn.Start(ctx)
	defer func() { _ = conn.Close("test done") }()

	// Client sends a call message (ext→host: ui.notify).
	callEnv := Envelope{
		Type: MsgCall,
		ID:   "c1",
		Call: &CallPayload{
			Method: "ui.notify",
			Args:   json.RawMessage(`{"message":"Hello from ext","level":"info"}`),
		},
	}
	callData, _ := json.Marshal(callEnv)
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(callData)))
	_, _ = clientConn.Write(lenBuf[:])
	_, _ = clientConn.Write(callData)

	// Host receives it on the Incoming channel.
	select {
	case env := <-conn.Incoming():
		if env.Type != MsgCall {
			t.Errorf("type = %q, want %q", env.Type, MsgCall)
		}
		if env.Call == nil || env.Call.Method != "ui.notify" {
			t.Errorf("unexpected call: %+v", env.Call)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for incoming call")
	}
}

// ── Supervisor tests ─────────────────────────────────────────────────────────

func TestSupervisor_ExponentialBackoff(t *testing.T) {
	cfg := SupervisorConfig{
		MaxCrashes:    5,
		CrashWindow:   60 * time.Second,
		InitialDelay:  100 * time.Millisecond,
		MaxDelay:      2 * time.Second,
		BackoffFactor: 2.0,
	}
	s := NewSupervisor(cfg)

	// First crash: initial delay.
	d, err := s.RecordCrash()
	if err != nil {
		t.Fatalf("first crash: %v", err)
	}
	if d != 100*time.Millisecond {
		t.Errorf("first delay = %v, want 100ms", d)
	}

	// Second crash: 200ms.
	d, err = s.RecordCrash()
	if err != nil {
		t.Fatalf("second crash: %v", err)
	}
	if d != 200*time.Millisecond {
		t.Errorf("second delay = %v, want 200ms", d)
	}

	// Third: 400ms.
	d, err = s.RecordCrash()
	if err != nil {
		t.Fatalf("third crash: %v", err)
	}
	if d != 400*time.Millisecond {
		t.Errorf("third delay = %v, want 400ms", d)
	}

	// Fourth: 800ms.
	d, err = s.RecordCrash()
	if err != nil {
		t.Fatalf("fourth crash: %v", err)
	}
	if d != 800*time.Millisecond {
		t.Errorf("fourth delay = %v, want 800ms", d)
	}

	// Fifth: circuit breaker trips.
	_, err = s.RecordCrash()
	if err == nil {
		t.Fatal("expected error from circuit breaker")
	}
	if !s.IsDisabled() {
		t.Error("supervisor should be disabled")
	}
}

func TestSupervisor_SuccessResetsConsecutive(t *testing.T) {
	cfg := SupervisorConfig{
		MaxCrashes:    5,
		CrashWindow:   60 * time.Second,
		InitialDelay:  100 * time.Millisecond,
		MaxDelay:      2 * time.Second,
		BackoffFactor: 2.0,
	}
	s := NewSupervisor(cfg)

	// Crash twice.
	_, _ = s.RecordCrash()
	_, _ = s.RecordCrash()

	// Success resets consecutive counter.
	s.RecordSuccess()

	// Next crash goes back to initial delay.
	d, err := s.RecordCrash()
	if err != nil {
		t.Fatalf("crash after success: %v", err)
	}
	if d != 100*time.Millisecond {
		t.Errorf("delay after success = %v, want 100ms", d)
	}
}

func TestSupervisor_Reset(t *testing.T) {
	cfg := DefaultSupervisorConfig()
	s := NewSupervisor(cfg)

	// Trip the breaker.
	for range 5 {
		_, _ = s.RecordCrash()
	}
	if !s.IsDisabled() {
		t.Fatal("should be disabled after 5 crashes")
	}

	// Reset re-enables.
	s.Reset()
	if s.IsDisabled() {
		t.Error("should not be disabled after reset")
	}

	// Can crash again.
	d, err := s.RecordCrash()
	if err != nil {
		t.Fatalf("crash after reset: %v", err)
	}
	if d != cfg.InitialDelay {
		t.Errorf("delay = %v, want %v", d, cfg.InitialDelay)
	}
}

// ── Protocol message shape tests ─────────────────────────────────────────────

func TestProtocol_RegisterRoundTrip(t *testing.T) {
	reg := &Envelope{
		Type: MsgRegister,
		Register: &RegisterPayload{
			Name:    "subagent",
			Version: "0.1.0",
			Tools: []ToolDecl{
				{
					Name:          "dispatch_subagent",
					Description:   "Spawn a specialist sub-agent.",
					Parameters:    json.RawMessage(`{"type":"object","properties":{"agent":{"type":"string"}}}`),
					ExecutionMode: "parallel",
				},
			},
			Commands: []CommandDecl{
				{Name: "subs", Description: "Show sub-agent status"},
			},
			Handlers: []HandlerDecl{
				{Event: "session_start", CanBlock: false, HandlerID: 7},
			},
			Widgets: []WidgetDecl{
				{Key: "subagent-status"},
			},
		},
	}

	data, err := json.Marshal(reg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Envelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Type != MsgRegister {
		t.Errorf("type = %q, want %q", decoded.Type, MsgRegister)
	}
	if decoded.Register.Name != "subagent" {
		t.Errorf("name = %q, want %q", decoded.Register.Name, "subagent")
	}
	if len(decoded.Register.Tools) != 1 {
		t.Fatalf("tools count = %d, want 1", len(decoded.Register.Tools))
	}
	if decoded.Register.Tools[0].Name != "dispatch_subagent" {
		t.Errorf("tool name = %q", decoded.Register.Tools[0].Name)
	}
	if decoded.Register.Tools[0].ExecutionMode != "parallel" {
		t.Errorf("exec mode = %q", decoded.Register.Tools[0].ExecutionMode)
	}
	if decoded.Register.Handlers[0].HandlerID != 7 {
		t.Errorf("handler id = %d, want 7", decoded.Register.Handlers[0].HandlerID)
	}
}

func TestRegisterRejectsUnknownWireFields(t *testing.T) {
	var envelope Envelope
	err := json.Unmarshal([]byte(`{"type":"register","register":{"name":"stale","legacy_field":true}}`), &envelope)
	if err == nil || !strings.Contains(err.Error(), "unknown field \"legacy_field\"") {
		t.Fatalf("stale register error = %v", err)
	}
}

func TestValidateHandlerDeclarationsRequiresUniquePositiveIDs(t *testing.T) {
	if err := validateHandlerDeclarations([]HandlerDecl{
		{Event: "project_trust", HandlerID: 1},
		{Event: "project_trust", HandlerID: 2},
	}); err != nil {
		t.Fatalf("identified duplicate handlers rejected: %v", err)
	}
	if err := validateHandlerDeclarations([]HandlerDecl{
		{Event: "project_trust"},
	}); err == nil {
		t.Fatal("zero handler ID accepted")
	}
	if err := validateHandlerDeclarations([]HandlerDecl{
		{Event: "project_trust", HandlerID: 1},
		{Event: "session_start", HandlerID: 1},
	}); err == nil {
		t.Fatal("duplicate handler IDs accepted")
	}
}

// ── Host socket directory resolution ─────────────────────────────────────────

// The Unix rules are checked through resolveSocketDirForGOOS so they hold on
// every host; on Windows the socket directory is always under the system temp
// directory.
func TestResolveSocketDir_XDGRuntime(t *testing.T) {
	dir := resolveSocketDirForGOOS("linux", "/run/user/1000", "/tmp/should-not-use", os.TempDir(), 1000)
	if want := filepath.Join("/run/user/1000", "pig"); dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
}

func TestResolveSocketDir_TMPDIRFallback(t *testing.T) {
	dir := resolveSocketDirForGOOS("darwin", "", "/var/folders/xx/T", os.TempDir(), 501)
	if want := filepath.Join("/var/folders/xx/T", "pig-501"); dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
}

func TestResolveSocketDir_WindowsUsesSystemTemp(t *testing.T) {
	dir := resolveSocketDirForGOOS("windows", "/run/user/1000", "/tmp/should-not-use", `C:\Temp`, 0)
	if want := filepath.Join(`C:\Temp`, "pig"); dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
}

// ── Host buildExtension tests ────────────────────────────────────────────────

func TestHost_BuildExtension_Tools(t *testing.T) {
	h := NewHost("/tmp/test")
	me := &managedExt{
		config: ExtConfig{Name: "test-ext", Path: "/bin/true"},
	}

	reg := &RegisterPayload{
		Name: "test-ext",
		Tools: []ToolDecl{
			{
				Name:             "code_structure",
				Description:      "Parse file structure",
				Parameters:       json.RawMessage(`{"type":"object"}`),
				ExecutionMode:    "sequential",
				PromptGuidelines: []string{"Use before editing"},
			},
			{
				Name:        "project_symbols",
				Description: "Search project symbols",
				Parameters:  json.RawMessage(`{"type":"object"}`),
			},
		},
		Commands: []CommandDecl{
			{Name: "symbols", Description: "Search symbols"},
		},
		Shortcuts: []ShortcutDecl{
			{Key: "ctrl+shift+i", Description: "Toggle info"},
		},
		Handlers: []HandlerDecl{
			{Event: "session_start", CanBlock: false, HandlerID: 1},
			{Event: "tool_execution_end", CanBlock: true, HandlerID: 2},
		},
	}

	ext := h.buildExtension(me, reg)

	// Tools
	if len(ext.Tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(ext.Tools))
	}
	cs, ok := ext.Tools["code_structure"]
	if !ok {
		t.Fatal("missing code_structure tool")
	}
	if cs.Definition.Name != "code_structure" {
		t.Errorf("tool name = %q", cs.Definition.Name)
	}
	if cs.Definition.Description != "Parse file structure" {
		t.Errorf("description = %q", cs.Definition.Description)
	}
	if cs.Definition.ExecutionMode != "sequential" {
		t.Errorf("execution mode = %q", cs.Definition.ExecutionMode)
	}
	if len(cs.Definition.PromptGuidelines) != 1 || cs.Definition.PromptGuidelines[0] != "Use before editing" {
		t.Errorf("prompt guidelines = %v", cs.Definition.PromptGuidelines)
	}
	if cs.Definition.Execute == nil {
		t.Error("Execute func is nil")
	}

	// Commands
	if len(ext.Commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(ext.Commands))
	}
	sym, ok := ext.Commands["symbols"]
	if !ok {
		t.Fatal("missing symbols command")
	}
	if sym.Description != "Search symbols" {
		t.Errorf("cmd description = %q", sym.Description)
	}
	if sym.Handler == nil {
		t.Error("command Handler is nil")
	}

	// Shortcuts
	if len(ext.Shortcuts) != 1 {
		t.Fatalf("shortcuts = %d, want 1", len(ext.Shortcuts))
	}
	sc, ok := ext.Shortcuts["ctrl+shift+i"]
	if !ok {
		t.Fatal("missing ctrl+shift+i shortcut")
	}
	if sc.Description != "Toggle info" {
		t.Errorf("shortcut description = %q", sc.Description)
	}
	if sc.Handler == nil {
		t.Error("shortcut Handler is nil")
	}

	// Handlers
	if len(ext.Handlers) != 2 {
		t.Fatalf("handlers = %d, want 2", len(ext.Handlers))
	}
	if len(ext.Handlers["session_start"]) != 1 {
		t.Error("missing session_start handler")
	}
	if len(ext.Handlers["tool_execution_end"]) != 1 {
		t.Error("missing tool_execution_end handler")
	}
}

// ── Reload tests ─────────────────────────────────────────────────────────────

// buildFixtureExt compiles testdata/fixture-ext into a temporary binary.
func buildFixtureExt(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("PIG_TEST_FIXTURE_EXT_BIN"); p != "" {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("PIG_TEST_FIXTURE_EXT_BIN=%q does not exist: %v", p, err)
		}
		return p
	}
	binPath := testExtensionBinaryPath(t.TempDir(), "fixture-ext")
	srcDir := filepath.Join("testdata", "fixture-ext")

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = srcDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build fixture-ext: %v\n%s", err, out)
	}
	return binPath
}

func TestHostStartupTraceAttributesExtensionLifecycle(t *testing.T) {
	binPath := buildFixtureExt(t)
	host := NewHost(t.TempDir())
	defer host.Shutdown("test")
	var marks []string
	host.SetStartupTrace(func(label string) { marks = append(marks, label) })

	loaded, errs := host.LoadAll(t.Context(), []ExtConfig{{Name: "fixture", Path: binPath, Enabled: true}})
	if len(errs) != 0 || len(loaded) != 1 {
		t.Fatalf("loaded=%v errors=%v", loaded, errs)
	}
	want := []string{
		"extension.fixture.discover-start",
		"extension.fixture.discover-done",
		"extension.fixture.build-check-start",
		"extension.fixture.build-check-done",
		"extension.fixture.spawn-start",
		"extension.fixture.spawn-done",
		"extension.fixture.handshake-start",
		"extension.fixture.handshake-done",
	}
	if !slices.Equal(marks, want) {
		t.Fatalf("startup marks = %q, want %q", marks, want)
	}
}

// Upstream /reload clears its extension factory cache and invokes every
// extension factory again, so an unchanged extension gets a fresh instance
// (new closure state, new session_start) exactly like a changed one. Pig's
// extension instance is its process, so an unchanged extension must be
// restarted, not kept running from before the reload.
func TestHost_Reload_UnchangedExtensionStartsFreshInstance(t *testing.T) {
	binPath := buildFixtureExt(t)
	cwd := t.TempDir()

	configs := []ExtConfig{{Name: "fixture", Path: binPath, Enabled: true}}
	h := NewHost(cwd)
	h.SetConfigLoader(func() ([]ExtConfig, error) { return append([]ExtConfig(nil), configs...), nil })
	defer h.Shutdown("test")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if _, err := h.Load(ctx, ExtConfig{Name: "fixture", Path: binPath}); err != nil {
		t.Fatalf("initial load: %v", err)
	}

	h.mu.Lock()
	oldExt := h.exts["fixture"]
	h.mu.Unlock()
	oldPid := oldExt.proc.Pid

	exts, err := h.Reload(ctx)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(exts) != 1 {
		t.Fatalf("extensions = %d, want 1", len(exts))
	}

	h.mu.Lock()
	newExt := h.exts["fixture"]
	h.mu.Unlock()
	if newExt == oldExt || newExt.proc.Pid == oldPid {
		t.Fatalf("unchanged extension kept its pre-reload instance (pid %d); upstream reload re-invokes every factory", oldPid)
	}
	if _, hasGreet := newExt.ext.Tools["greet"]; !hasGreet {
		t.Error("fresh instance missing 'greet' tool after reload")
	}
	if rep := h.LastReloadReport(); len(rep.Cells) != 1 || !rep.Cells[0].Replaced {
		t.Errorf("reload report cells = %+v, want one replaced cell", rep.Cells)
	}
}

func TestHost_Reload_Extension(t *testing.T) {
	binPath := buildFixtureExt(t)
	cwd := t.TempDir()

	configs := []ExtConfig(nil)
	h := NewHost(cwd)
	h.SetConfigLoader(func() ([]ExtConfig, error) { return append([]ExtConfig(nil), configs...), nil })
	defer h.Shutdown("test")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// No extensions initially.
	if len(h.Extensions()) != 0 {
		t.Fatalf("expected 0 extensions, got %d", len(h.Extensions()))
	}

	configs = []ExtConfig{{Name: "fixture", Path: binPath, Enabled: true}}

	exts, err := h.Reload(ctx)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(exts) != 1 {
		t.Fatalf("extensions = %d, want 1", len(exts))
	}

	// Verify the extension registered with tools.
	h.mu.Lock()
	me, ok := h.exts["fixture"]
	h.mu.Unlock()
	if !ok {
		t.Fatal("fixture not in exts map")
	}
	if me.ext == nil {
		t.Fatal("extension struct is nil")
	}
	if _, hasGreet := me.ext.Tools["greet"]; !hasGreet {
		t.Error("fixture extension missing 'greet' tool")
	}
}

// Upstream reload does not keep a failed extension's previous runtime: the
// extension is not loaded and its failure is reported.
func TestHost_Reload_FailedReplacementDropsOldExtension(t *testing.T) {
	binPath := buildFixtureExt(t)
	cwd := t.TempDir()
	configs := []ExtConfig{{Name: "fixture", Path: binPath, Enabled: true}}
	h := NewHost(cwd)
	h.SetConfigLoader(func() ([]ExtConfig, error) { return append([]ExtConfig(nil), configs...), nil })
	defer h.Shutdown("test")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if _, err := h.Load(ctx, ExtConfig{Name: "fixture", Path: binPath}); err != nil {
		t.Fatalf("initial load: %v", err)
	}

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	configs = []ExtConfig{{Name: "fixture", Path: missing, Enabled: true}}
	if _, err := h.Reload(ctx); err != nil {
		t.Fatalf("reload failed as a whole: %v", err)
	}

	h.mu.Lock()
	got := h.exts["fixture"]
	h.mu.Unlock()
	if got != nil {
		t.Fatal("old extension kept after its replacement failed to load")
	}
	if rep := h.LastReloadReport(); len(rep.Issues) != 1 || !strings.HasPrefix(rep.Issues[0], missing+": Failed to load extension: ") {
		t.Fatalf("issues = %q", rep.Issues)
	}
}

func TestValidateRegisterPayloadRejectsInvalidRegistration(t *testing.T) {
	tests := []struct {
		name string
		reg  *RegisterPayload
		want string
	}{
		{
			name: "missing extension name",
			reg:  &RegisterPayload{},
			want: "register.name is required",
		},
		{
			name: "flag default not JSON",
			reg:  &RegisterPayload{Name: "bad", Flags: []FlagDecl{{Name: "mode", Type: "string", Default: json.RawMessage(`{broken`)}}},
			want: `flag "mode" has an invalid default`,
		},
		{
			name: "empty command",
			reg:  &RegisterPayload{Name: "bad", Commands: []CommandDecl{{Name: ""}}},
			want: "command name is required",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRegisterPayload("bad", tc.reg)
			if err == nil {
				t.Fatalf("validateRegisterPayload error = nil, want %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err, tc.want)
			}
		})
	}
}

// Upstream registration writes into a Map: registering a name again replaces
// the definition and keeps the first position, and the extension still loads.
func TestRepeatedRegistrationLastDefinitionWins(t *testing.T) {
	reg := &RegisterPayload{
		Name: "repeat",
		Tools: []ToolDecl{
			{Name: "echo", Description: "one", Parameters: json.RawMessage(`{}`)},
			{Name: "other", Description: "other", Parameters: json.RawMessage(`{}`)},
			{Name: "echo", Description: "two", Parameters: json.RawMessage(`{}`)},
		},
		Commands: []CommandDecl{{Name: "go", Description: "first"}, {Name: "go", Description: "last"}},
	}
	if err := validateRegisterPayload("repeat", reg); err != nil {
		t.Fatalf("repeated registration refused: %v", err)
	}
	if len(reg.Tools) != 2 || reg.Tools[0].Name != "echo" || reg.Tools[0].Description != "two" || reg.Tools[1].Name != "other" {
		t.Fatalf("tools = %+v, want echo(two) then other", reg.Tools)
	}
	if len(reg.Commands) != 1 || reg.Commands[0].Description != "last" {
		t.Fatalf("commands = %+v, want one command with the last description", reg.Commands)
	}
}

func TestHost_Reload_RemovedExtension(t *testing.T) {
	binPath := buildFixtureExt(t)
	cwd := t.TempDir()

	configs := []ExtConfig{{Name: "fixture", Path: binPath, Enabled: true}}
	h := NewHost(cwd)
	h.SetConfigLoader(func() ([]ExtConfig, error) { return append([]ExtConfig(nil), configs...), nil })
	defer h.Shutdown("test")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Load fixture.
	_, err := h.Load(ctx, ExtConfig{Name: "fixture", Path: binPath})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(h.Extensions()) != 1 {
		t.Fatalf("expected 1 extension, got %d", len(h.Extensions()))
	}

	// Update config to remove fixture.
	configs = nil

	exts, err := h.Reload(ctx)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(exts) != 0 {
		t.Fatalf("extensions = %d, want 0", len(exts))
	}

	// Verify removed from map.
	h.mu.Lock()
	_, exists := h.exts["fixture"]
	h.mu.Unlock()
	if exists {
		t.Error("fixture still in exts map after removal")
	}
}

func TestToolResultUnmarshal_StringContent(t *testing.T) {
	data := `{"content":"hello world","is_error":false}`
	var r ToolResult
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		t.Fatalf("unmarshal string content: %v", err)
	}
	if r.Content != "hello world" {
		t.Fatalf("content = %q, want %q", r.Content, "hello world")
	}
}

func TestToolResultUnmarshal_ArrayContent(t *testing.T) {
	data := `{"content":[{"type":"text","text":"Edited file.go."}],"details":{"diff":"..."},"is_error":false}`
	var r ToolResult
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		t.Fatalf("unmarshal array content: %v", err)
	}
	if r.Content != "Edited file.go." {
		t.Fatalf("content = %q, want %q", r.Content, "Edited file.go.")
	}
}

func TestToolResultUnmarshal_EmptyContent(t *testing.T) {
	data := `{"is_error":true}`
	var r ToolResult
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		t.Fatalf("unmarshal empty content: %v", err)
	}
	if r.Content != "" {
		t.Fatalf("content = %q, want empty", r.Content)
	}
	if !r.IsError {
		t.Fatal("is_error should be true")
	}
}

func TestPrependUniquePathPreservesEnvironmentWithoutDuplicates(t *testing.T) {
	separator := string(os.PathListSeparator)
	got := prependUniquePath("/sdk", strings.Join([]string{"/other", "/sdk", "/other", "/last"}, separator))
	want := strings.Join([]string{"/sdk", "/other", "/last"}, separator)
	if got != want {
		t.Fatalf("PYTHONPATH = %q, want %q", got, want)
	}
}

func TestHostLoadRejectsRegisteredIdentityMismatch(t *testing.T) {
	binary := buildFixtureExt(t)
	host := NewHost(t.TempDir())
	defer host.Shutdown("test done")
	_, err := host.Load(t.Context(), ExtConfig{Name: "selected-name", Path: binary, Enabled: true})
	message := fmt.Sprint(err)
	for _, want := range []string{"name_mismatch", `selected identity "selected-name"`, `registered identity "fixture"`, binary, "renamed/reselected", "Package/Piglet declaration"} {
		if !strings.Contains(message, want) {
			t.Fatalf("Load error = %v, want actionable identity detail %q", err, want)
		}
	}
}

// Upstream identifies an extension by its path, so two copies of one identity
// are loaded, and fail, separately; a duplicate never rejects the whole set.
func TestHostLoadAllLoadsDuplicateSelectedIdentitySeparately(t *testing.T) {
	host := NewHost(t.TempDir())
	defer host.Shutdown("test done")
	_, errs := host.LoadAll(t.Context(), []ExtConfig{
		{Name: "duplicate", Path: "/first", Enabled: true},
		{Name: "duplicate", Path: "/second", Enabled: true},
	})
	if len(errs) != 2 {
		t.Fatalf("LoadAll errors = %v, want one per copy", errs)
	}
	var origins []string
	for _, err := range errs {
		loadErr, ok := errors.AsType[*ExtensionLoadError](err)
		if !ok || strings.Contains(err.Error(), "duplicate extension identity") {
			t.Fatalf("error = %v, want a copy's own failure", err)
		}
		origins = append(origins, loadErr.Path)
	}
	slices.Sort(origins)
	if !slices.Equal(origins, []string{"/first", "/second"}) {
		t.Fatalf("failed copies = %v", origins)
	}
}

// A valid flag default survives registration into the built extension.
func TestRegisteredFlagDefaultReachesExtension(t *testing.T) {
	reg := &RegisterPayload{Name: "flags", Flags: []FlagDecl{{Name: "mode", Type: "string", Default: json.RawMessage(`"fast"`)}, {Name: "plain", Type: "boolean"}}}
	if err := validateRegisterPayload("flags", reg); err != nil {
		t.Fatal(err)
	}
	h := NewHost(t.TempDir())
	ext := h.buildExtension(&managedExt{config: ExtConfig{Name: "flags"}, host: h}, reg)
	if got := ext.Flags["mode"].Default; got != "fast" {
		t.Fatalf("mode default = %#v, want fast", got)
	}
	if got := ext.Flags["plain"].Default; got != nil {
		t.Fatalf("plain default = %#v, want nil", got)
	}
}
