package subprocess_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// greetExt is a minimal real SDK extension whose "greet" tool returns a known
// string, used to prove a tool round-trips through the fused path.
func greetExt(name string) *sdk.Extension {
	ext := sdk.New(name)
	ext.Tool("greet", "greet someone", sdk.Schema{
		"type":       "object",
		"properties": map[string]any{"name": map[string]any{"type": "string"}},
	}, func(_ sdk.Context, params map[string]any) (any, error) {
		who, _ := params["name"].(string)
		return sdk.ToolResult{Content: "fused hello, " + who}, nil
	})
	ext.OnProjectTrust(func(sdk.Context, map[string]any) (sdk.ProjectTrustResult, error) {
		return sdk.ProjectTrustResult{Trusted: sdk.ProjectTrustUndecided}, nil
	})
	ext.OnProjectTrust(func(sdk.Context, map[string]any) (sdk.ProjectTrustResult, error) {
		return sdk.ProjectTrustResult{Trusted: sdk.ProjectTrustYes, Remember: true}, nil
	})
	return ext
}

func assertGreet(t *testing.T, ext extension.Extension) {
	t.Helper()
	tool, ok := ext.Tools["greet"]
	if !ok {
		t.Fatalf("extension registered no 'greet' tool: %#v", ext.Tools)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := tool.Definition.Execute(ctx, "tc-1", json.RawMessage(`{"name":"Piglet Binary"}`), nil)
	if err != nil {
		t.Fatalf("fused tool execute: %v", err)
	}
	var got struct {
		Content string `json:"content"`
	}
	b, _ := json.Marshal(result)
	_ = json.Unmarshal(b, &got)
	if got.Content != "fused hello, Piglet Binary" {
		t.Errorf("fused tool content = %q, want %q", got.Content, "fused hello, Piglet Binary")
	}
}

// TestHost_LoadInProcess proves the fused (fuse) runtime: an SDK extension's
// factory runs inside the host process over an in-memory pipe: no subprocess,
// no socket, no runtime build: and its tool round-trips through the same wire
// protocol the subprocess path uses. This is the fuse conformance gate: a fused
// tool answers identically to the same tool run as a subprocess cell.
func TestHost_LoadInProcess(t *testing.T) {
	ext := greetExt("fused")

	h := subprocess.NewHost(t.TempDir())
	h.SetUIBridge(subprocess.NewUIBridge(func() {}))
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	loaded, err := h.LoadInProcess(ctx, subprocess.ExtConfig{Name: "fused", Enabled: true}, ext.RunWithConn)
	if err != nil {
		t.Fatalf("LoadInProcess: %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadInProcess returned nil extension")
	}
	assertGreet(t, *loaded)
	runner := inproc.NewRunner([]extension.Extension{*loaded}, t.TempDir())
	trust, handlerErrors, err := inproc.EmitProjectTrust(runner, ctx, extension.ProjectTrustEvent{Type: "project_trust", Cwd: t.TempDir()})
	if err != nil || len(handlerErrors) != 0 || trust == nil || trust.Trusted != extension.ProjectTrustYes {
		t.Fatalf("fused project_trust result=%+v errors=%+v err=%v", trust, handlerErrors, err)
	}
}

type fusedFocusedComponent struct{ selected int }

func (c *fusedFocusedComponent) Render(width int) []string {
	return []string{fmt.Sprintf("fused width=%d selected=%d", width, c.selected)}
}

func (c *fusedFocusedComponent) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	if data == "\x1b[6~" {
		c.selected = 2
	}
	if data == "\r" {
		return sdk.RemoteComponentResult{Done: true, Value: c.selected}, nil
	}
	return sdk.RemoteComponentResult{}, nil
}

type fusedFocusedUI struct {
	extension.UIContext
	notified chan string
}

func (u *fusedFocusedUI) Notify(message, _ string) { u.notified <- message }
func (*fusedFocusedUI) RunRemoteOverlay(_ extension.RemoteOverlayOptions, host extension.RemoteOverlayHost, onHandle func(extension.RemoteOverlayHandle)) (any, bool) {
	handle := &fusedOverlayHandle{done: make(chan struct{})}
	onHandle(handle)
	host.OnInput("\x1b[6~")
	host.OnInput("\r")
	select {
	case <-handle.done:
		return handle.result, true
	case <-time.After(5 * time.Second):
		return nil, false
	}
}

type fusedOverlayHandle struct {
	result any
	done   chan struct{}
}

func (*fusedOverlayHandle) UpdateLines([]string) {}
func (h *fusedOverlayHandle) Close(result any) {
	h.result = result
	close(h.done)
}

func TestHost_LoadInProcessAndIsolatedNode(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not found: %v", err)
	}
	source := filepath.Join(t.TempDir(), "isolated.mjs")
	module := `export default function (pi) {
  pi.registerCommand("isolated-ping", {
    description: "Report isolated health",
    handler: async (_args, ctx) => ctx.ui.notify("isolated ready", "info"),
  });
}`
	if err := os.WriteFile(source, []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}

	notified := make(chan string, 1)
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(&fusedFocusedUI{UIContext: extension.NoopUIContext, notified: notified})
	host := subprocess.NewHost(t.TempDir())
	host.SetUIBridge(bridge)
	t.Cleanup(func() { host.Shutdown("test done") })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fused, err := host.LoadInProcess(ctx, subprocess.ExtConfig{Name: "fused", Enabled: true}, greetExt("fused").RunWithConn)
	if err != nil {
		t.Fatal(err)
	}
	isolated, err := host.Load(ctx, subprocess.ExtConfig{Name: "isolated", Source: source, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if isolated == nil {
		t.Fatal("isolated extension did not load")
	}
	if host.ExtensionCount() != 2 {
		t.Fatalf("extension count = %d, want 2", host.ExtensionCount())
	}
	assertGreet(t, *fused)
	command, ok := isolated.Commands["isolated-ping"]
	if !ok {
		t.Fatal("isolated-ping command did not register")
	}
	if err := command.Handler(ctx, ""); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-notified:
		if message != "isolated ready" {
			t.Fatalf("notification = %q", message)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	assertGreet(t, *fused)
}

func TestAC59FusedFocusedComponentUsesProtocolV1(t *testing.T) {
	ext := sdk.New("fused-focused")
	ext.Command("focused", "focused", func(ctx sdk.Context, _ string) error {
		value, err := ctx.Custom(&fusedFocusedComponent{}, nil)
		if err != nil {
			return err
		}
		ctx.Notify(fmt.Sprintf("focused=%v", value), "info")
		return nil
	})
	ui := &fusedFocusedUI{UIContext: extension.NoopUIContext, notified: make(chan string, 1)}
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	host := subprocess.NewHost(t.TempDir())
	host.SetUIBridge(bridge)
	defer host.Shutdown("test done")
	loaded, err := host.LoadInProcess(context.Background(), subprocess.ExtConfig{Name: "fused-focused", Enabled: true}, ext.RunWithConn)
	if err != nil {
		t.Fatal(err)
	}
	if err := loaded.Commands["focused"].Handler(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-ui.notified:
		if got != "focused=2" {
			t.Fatalf("notification = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fused focused component did not complete")
	}
}

type fusedTimerComponent struct {
	mu         sync.Mutex
	frame      int
	invalidate func()
	stop       chan struct{}
	done       chan struct{}
	disposed   bool
	stopOnce   sync.Once
}

func newFusedTimerComponent() *fusedTimerComponent {
	component := &fusedTimerComponent{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(component.done)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				component.mu.Lock()
				component.frame++
				invalidate := component.invalidate
				component.mu.Unlock()
				if invalidate != nil {
					invalidate()
				}
			case <-component.stop:
				return
			}
		}
	}()
	return component
}

func (c *fusedTimerComponent) Render(width int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return []string{fmt.Sprintf("timer frame=%d width=%d", c.frame, width)}
}

func (c *fusedTimerComponent) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if data == "\r" {
		return sdk.RemoteComponentResult{Done: true, Value: c.frame}, nil
	}
	return sdk.RemoteComponentResult{}, nil
}

func (c *fusedTimerComponent) SetInvalidate(invalidate func()) {
	c.mu.Lock()
	c.invalidate = invalidate
	c.mu.Unlock()
}

func (c *fusedTimerComponent) Dispose() {
	c.stopOnce.Do(func() { close(c.stop) })
	<-c.done
	c.mu.Lock()
	c.disposed = true
	c.mu.Unlock()
}

func (c *fusedTimerComponent) cleanupComplete() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.disposed && c.invalidate == nil
}

type fusedTimerUI struct {
	extension.UIContext
}

func (*fusedTimerUI) RunRemoteOverlay(_ extension.RemoteOverlayOptions, host extension.RemoteOverlayHost, onHandle func(extension.RemoteOverlayHandle)) (any, bool) {
	handle := &fusedTimerOverlayHandle{done: make(chan struct{}), changed: make(chan struct{}, 1)}
	onHandle(handle)
	deadline := time.After(5 * time.Second)
	for handle.frame() < 2 {
		select {
		case <-handle.changed:
		case <-deadline:
			return nil, false
		}
	}
	host.OnInput("\r")
	select {
	case <-handle.done:
		return handle.result, true
	case <-deadline:
		return nil, false
	}
}

type fusedTimerOverlayHandle struct {
	mu      sync.Mutex
	lines   []string
	result  any
	done    chan struct{}
	changed chan struct{}
}

func (h *fusedTimerOverlayHandle) UpdateLines(lines []string) {
	h.mu.Lock()
	h.lines = append([]string(nil), lines...)
	h.mu.Unlock()
	select {
	case h.changed <- struct{}{}:
	default:
	}
}

func (h *fusedTimerOverlayHandle) frame() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.lines) == 0 {
		return 0
	}
	var frame int
	_, _ = fmt.Sscanf(h.lines[0], "timer frame=%d", &frame)
	return frame
}

func (h *fusedTimerOverlayHandle) Close(result any) {
	h.mu.Lock()
	h.result = result
	select {
	case <-h.done:
	default:
		close(h.done)
	}
	h.mu.Unlock()
}

func TestFusedFocusedTimerInvalidatesAndCleansUp(t *testing.T) {
	component := newFusedTimerComponent()
	ext := sdk.New("fused-timer")
	ext.Command("timer", "timer", func(ctx sdk.Context, _ string) error {
		value, err := ctx.Custom(component, nil)
		if err != nil {
			return err
		}
		frame, ok := value.(float64)
		if !ok || frame < 2 {
			return fmt.Errorf("timer result = %#v, want frame >= 2", value)
		}
		return nil
	})
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(&fusedTimerUI{UIContext: extension.NoopUIContext})
	host := subprocess.NewHost(t.TempDir())
	host.SetUIBridge(bridge)
	defer host.Shutdown("test done")
	loaded, err := host.LoadInProcess(context.Background(), subprocess.ExtConfig{Name: "fused-timer", Enabled: true}, ext.RunWithConn)
	if err != nil {
		t.Fatal(err)
	}
	if err := loaded.Commands["timer"].Handler(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if !component.cleanupComplete() {
		t.Fatal("fused timer component was not detached before exact-once disposal")
	}
}

func TestHost_LoadInProcessProjectTrustCancellation(t *testing.T) {
	started := make(chan struct{})
	handlerCanceled := make(chan struct{})
	ext := sdk.New("cancel-trust")
	ext.OnProjectTrust(func(ctx sdk.Context, _ map[string]any) (sdk.ProjectTrustResult, error) {
		close(started)
		<-ctx.Done()
		close(handlerCanceled)
		return sdk.ProjectTrustResult{}, ctx.Err()
	})

	host := subprocess.NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	loaded, err := host.LoadInProcess(context.Background(), subprocess.ExtConfig{Name: "cancel-trust", Enabled: true}, ext.RunWithConn)
	if err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner([]extension.Extension{*loaded}, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan []extension.ExtensionError, 1)
	go func() {
		_, handlerErrors, _ := inproc.EmitProjectTrust(runner, ctx, extension.ProjectTrustEvent{Type: "project_trust", Cwd: t.TempDir()})
		done <- handlerErrors
	}()
	<-started
	cancel()
	select {
	case handlerErrors := <-done:
		if len(handlerErrors) != 1 || !strings.Contains(handlerErrors[0].Error, context.Canceled.Error()) {
			t.Fatalf("handler errors = %+v", handlerErrors)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("project_trust host dispatch did not return on cancellation")
	}
	select {
	case <-handlerCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("project_trust cancellation did not reach the SDK handler")
	}
}

// TestHost_LoadInProcess_NilServe is the fail-able guard: a missing serve func
// must be rejected, never silently spawned or left hanging.
func TestHost_LoadInProcess_NilServe(t *testing.T) {
	h := subprocess.NewHost(t.TempDir())
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := h.LoadInProcess(ctx, subprocess.ExtConfig{Name: "x"}, nil); err == nil {
		t.Fatal("LoadInProcess(nil serve) = nil error, want rejection")
	}
}

type fakeFused struct{ serve func(net.Conn) error }

func (f fakeFused) FusedServe(cfg subprocess.ExtConfig) (func(net.Conn) error, bool) {
	if cfg.Name == "fused" {
		return f.serve, true
	}
	return nil, false
}

// TestHost_LoadAll_FusesRegistered proves LoadAll routes a fused extension
// through the in-process path: the config carries no Path or Source, so the
// subprocess cell path would reject it (missing_entrypoint). Success therefore
// proves it loaded fused. Without a resolver the same config takes the cell path
// and errors, so the test is fail-able by construction.
func TestHost_LoadAll_FusesRegistered(t *testing.T) {
	ext := greetExt("fused")
	subprocess.SetFusedResolver(fakeFused{serve: ext.RunWithConn})
	defer subprocess.SetFusedResolver(nil)

	h := subprocess.NewHost(t.TempDir())
	h.SetUIBridge(subprocess.NewUIBridge(func() {}))
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	loaded, errs := h.LoadAll(ctx, []subprocess.ExtConfig{{Name: "fused", Enabled: true}})
	if len(errs) > 0 {
		t.Fatalf("LoadAll errs: %v", errs)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d extensions, want 1", len(loaded))
	}
	assertGreet(t, loaded[0])
}

// TestSubprocessEventPatching proves that a real extension's tool_result and
// input handlers, dispatched over the live wire (LoadInProcess uses the same
// protocol as a subprocess cell), reach the Runner and MUTATE the result:
// tool_result content is replaced and input text is transformed. This is the
// foundation for the RTK/headroom token wrappers and secret-paste input hiding.
func TestSubprocessEventPatching(t *testing.T) {
	ext := sdk.New("patcher")
	ext.OnToolResult(func(_ sdk.Context, _ map[string]any) (any, error) {
		return map[string]any{
			"content": []any{map[string]any{"type": "text", "text": "PATCHED"}},
		}, nil
	})
	ext.OnEvent("input", func(_ sdk.Context, data map[string]any) (any, error) {
		text, _ := data["text"].(string)
		return map[string]any{
			"action": "transform",
			"text":   strings.ReplaceAll(text, "SECRET", "<hidden>"),
		}, nil
	})

	h := subprocess.NewHost(t.TempDir())
	h.SetUIBridge(subprocess.NewUIBridge(func() {}))
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	loaded, err := h.LoadInProcess(ctx, subprocess.ExtConfig{Name: "patcher", Enabled: true}, ext.RunWithConn)
	if err != nil {
		t.Fatalf("LoadInProcess: %v", err)
	}
	r := inproc.NewRunner([]extension.Extension{*loaded}, ".")

	tr, err := r.EmitToolResult(ctx, extension.BashToolResultEvent{
		ToolResultEventBase: extension.ToolResultEventBase{
			Type:    "tool_result",
			Content: []any{map[string]any{"type": "text", "text": "raw output"}},
		},
		ToolName: "bash",
	})
	if err != nil {
		t.Fatalf("EmitToolResult: %v", err)
	}
	if tr == nil || len(tr.Content) == 0 {
		t.Fatalf("tool_result not patched over the wire: %#v", tr)
	}
	if b, _ := json.Marshal(tr.Content); !strings.Contains(string(b), "PATCHED") {
		t.Errorf("tool_result content = %s, want PATCHED", b)
	}

	ir, err := r.EmitInput(ctx, "use SECRET now", nil, "interactive", "")
	if err != nil {
		t.Fatalf("EmitInput: %v", err)
	}
	tf, ok := ir.(extension.InputEventResultTransform)
	if !ok {
		t.Fatalf("EmitInput result = %T, want InputEventResultTransform", ir)
	}
	if tf.Text != "use <hidden> now" {
		t.Errorf("input transform = %q, want %q", tf.Text, "use <hidden> now")
	}
}

func TestHostLoadInProcessUsesRuntimeOAuthRegistration(t *testing.T) {
	ai.ResetOAuthProviders()
	t.Cleanup(ai.ResetOAuthProviders)
	ext := sdk.New("fused-oauth")
	ext.RegisterProvider("fused-login", sdk.ProviderConfig{
		"name": "Fused Login",
		"oauth": &sdk.OAuthProvider{Name: "Fused Login", Login: func(*sdk.OAuthLoginCallbacks) (sdk.OAuthCredentials, error) {
			return sdk.OAuthCredentials{}, nil
		}},
	})
	host := subprocess.NewHost(t.TempDir())
	defer host.Shutdown("test done")
	if _, err := host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "fused-oauth", Enabled: true}, ext.RunWithConn); err != nil {
		t.Fatal(err)
	}
	if got := host.OAuthProviderNames("fused-oauth"); len(got) != 1 || got[0] != "fused-login" {
		t.Fatalf("fused OAuth providers = %v", got)
	}
	provider, ok := ai.GetOAuthProvider("fused-login")
	if !ok || provider.Name() != "Fused Login" {
		t.Fatalf("fused runtime provider = %#v, %t", provider, ok)
	}
}
